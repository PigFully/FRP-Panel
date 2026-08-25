package agent

import (
	"sync"
	"time"
)

// authLimiter throttles repeated authentication failures per source IP to blunt
// public brute-force attempts: >maxFails within window bans the IP for banDur.
type authLimiter struct {
	mu        sync.Mutex
	fails     map[string][]int64 // ip -> recent failure unix-nanos
	banned    map[string]int64   // ip -> ban-until unix-nanos
	maxFails  int
	window    time.Duration
	banDur    time.Duration
	lastPrune int64 // unix-nanos of the last sweep
}

func newAuthLimiter() *authLimiter {
	return &authLimiter{
		fails:    map[string][]int64{},
		banned:   map[string]int64{},
		maxFails: 10,
		window:   time.Minute,
		banDur:   10 * time.Minute,
	}
}

// prune drops expired failure history and lapsed bans. The 8443 port faces the
// public internet and is scanned continuously; without this, every one-shot
// scanner IP that sends a single bad handshake would leave a map entry that is
// never revisited (Blocked/Success only touch an IP that connects again), so the
// maps would grow without bound and slowly leak memory. Called under l.mu.
func (l *authLimiter) prune(now int64) {
	windowCut := now - l.window.Nanoseconds()
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

// maybePrune sweeps at most once per window so a flood cannot make every failure
// pay an O(n) scan.
func (l *authLimiter) maybePrune(now int64) {
	if now-l.lastPrune < l.window.Nanoseconds() {
		return
	}
	l.lastPrune = now
	l.prune(now)
}

// Blocked reports whether ip is currently banned.
func (l *authLimiter) Blocked(ip string, now time.Time) bool {
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

// Fail records a failed auth for ip and bans it if the threshold is exceeded.
func (l *authLimiter) Fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maybePrune(now.UnixNano())
	cutoff := now.Add(-l.window).UnixNano()
	kept := l.fails[ip][:0:0]
	for _, t := range l.fails[ip] {
		if t >= cutoff {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now.UnixNano())
	l.fails[ip] = kept
	if len(kept) >= l.maxFails {
		l.banned[ip] = now.Add(l.banDur).UnixNano()
	}
}

// Success clears a source IP's failure history.
func (l *authLimiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
	delete(l.banned, ip)
}
