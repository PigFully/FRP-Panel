package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieJWT  = "frpanel_jwt"
	cookieCSRF = "frpanel_csrf"
	jwtTTL     = 7 * 24 * time.Hour
	bcryptCost = 12
)

// Claims is the JWT payload. PV (pwd_version) enables instant revocation on
// password change: every request re-checks PV against the DB.
type Claims struct {
	UID   int64  `json:"uid"`
	Uname string `json:"uname"`
	PV    int    `json:"pv"`
	jwt.RegisteredClaims
}

// HashPassword bcrypts a plaintext password.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(b), err
}

// CheckPassword verifies a plaintext against a bcrypt hash.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// RandomToken returns n random bytes hex-encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RandomPassword returns a 16-char strong password from a safe alphabet.
func RandomPassword() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%^&*"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// Auth bundles JWT signing, session validation and login throttling.
type Auth struct {
	secret  []byte
	store   *Store
	secure  bool
	limiter *loginLimiter
	dbUp    func() bool
}

// NewAuth builds an Auth helper.
func NewAuth(secret string, store *Store, secure bool, dbUp func() bool) *Auth {
	return &Auth{secret: []byte(secret), store: store, secure: secure, limiter: newLoginLimiter(), dbUp: dbUp}
}

// Issue signs a 7-day JWT for the user.
func (a *Auth) Issue(u *User) (string, error) {
	claims := Claims{
		UID: u.ID, Uname: u.Username, PV: u.PwdVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
}

// Parse validates a token's signature and expiry (not pwd_version).
func (a *Auth) Parse(tokenStr string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return a.secret, nil
	})
	if err != nil {
		return nil, err
	}
	return &claims, nil
}

// SetSession writes the JWT + CSRF cookies.
func (a *Auth) SetSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieJWT, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.secure, MaxAge: int(jwtTTL.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name: cookieCSRF, Value: RandomToken(16), Path: "/", HttpOnly: false,
		SameSite: http.SameSiteLaxMode, Secure: a.secure, MaxAge: int(jwtTTL.Seconds()),
	})
}

// ClearSession expires the cookies (logout).
func (a *Auth) ClearSession(w http.ResponseWriter) {
	for _, name := range []string{cookieJWT, cookieCSRF} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: name == cookieJWT, MaxAge: -1, Secure: a.secure})
	}
}

type ctxKey int

const claimsKey ctxKey = 1

// FromContext returns the authenticated claims, if any.
func FromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

// RequireAuth is middleware that validates the JWT cookie and, when the DB is
// reachable, checks pwd_version so a password change instantly kills old JWTs.
func (a *Auth) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := a.claimsFromRequest(r)
		if err != nil {
			fail(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Auth) claimsFromRequest(r *http.Request) (*Claims, error) {
	c, err := r.Cookie(cookieJWT)
	if err != nil || c.Value == "" {
		return nil, ErrUnauthorized
	}
	claims, err := a.Parse(c.Value)
	if err != nil {
		return nil, ErrUnauthorized
	}
	// pwd_version revocation check (skipped only if DB is down, to avoid locking
	// everyone out during an outage; realtime/session stays usable).
	if a.dbUp == nil || a.dbUp() {
		u, err := a.store.GetUserByID(r.Context(), claims.UID)
		if err != nil {
			return nil, ErrDBDown
		}
		if u == nil || u.PwdVersion != claims.PV {
			return nil, ErrSessionStale
		}
	}
	return claims, nil
}

// CSRFGuard enforces double-submit CSRF on mutating requests.
func (a *Auth) CSRFGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(cookieCSRF)
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || cookie.Value == "" || header == "" || cookie.Value != header {
			fail(w, ErrCSRF)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoginAllowed reports whether an IP may attempt login (not currently locked).
func (a *Auth) LoginAllowed(ip string) bool { return !a.limiter.Blocked(ip, time.Now()) }

// LoginFail records a failed login for throttling.
func (a *Auth) LoginFail(ip string) { a.limiter.Fail(ip, time.Now()) }

// LoginSuccess clears an IP's failure history.
func (a *Auth) LoginSuccess(ip string) { a.limiter.Success(ip) }

// ClientIP extracts the source IP from a request.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return strings.TrimSpace(host)
}

// loginLimiter: 5 failures within a window locks the IP for 10 minutes.
type loginLimiter struct {
	mu        sync.Mutex
	fails     map[string][]int64
	banned    map[string]int64
	lastPrune int64 // unix-nanos of the last sweep
}

const loginLimiterWindow = 10 * time.Minute

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string][]int64{}, banned: map[string]int64{}}
}

// prune drops expired failure history and lapsed bans so the maps stay bounded
// to IPs seen within the window rather than accumulating an entry for every IP
// that ever failed once. Called under l.mu.
func (l *loginLimiter) prune(now int64) {
	windowCut := now - loginLimiterWindow.Nanoseconds()
	for ip, ts := range l.fails {
		keep := ts[:0]
		for _, t := range ts {
			if t >= windowCut {
				keep = append(keep, t)
			}
		}
		if len(keep) == 0 {
			if until, banned := l.banned[ip]; !banned || now >= until {
				delete(l.fails, ip)
			}
		} else {
			l.fails[ip] = keep
		}
	}
	for ip, until := range l.banned {
		if now >= until {
			delete(l.banned, ip)
			delete(l.fails, ip)
		}
	}
}

// maybePrune sweeps at most once per window.
func (l *loginLimiter) maybePrune(now int64) {
	if now-l.lastPrune < loginLimiterWindow.Nanoseconds() {
		return
	}
	l.lastPrune = now
	l.prune(now)
}

func (l *loginLimiter) Blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.banned[ip]
	if !ok {
		return false
	}
	if now.UnixNano() >= until {
		delete(l.banned, ip)
		delete(l.fails, ip)
		return false
	}
	return true
}

func (l *loginLimiter) Fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maybePrune(now.UnixNano())
	cutoff := now.Add(-loginLimiterWindow).UnixNano()
	kept := l.fails[ip][:0:0]
	for _, t := range l.fails[ip] {
		if t >= cutoff {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now.UnixNano())
	l.fails[ip] = kept
	if len(kept) >= 5 {
		l.banned[ip] = now.Add(10 * time.Minute).UnixNano()
	}
}

func (l *loginLimiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
	delete(l.banned, ip)
}
