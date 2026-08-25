package panel

import (
	"testing"
	"time"
)

// The login limiter must not accumulate a permanent entry for every IP that ever
// failed a login once: after the window, a later failure sweeps the stale IPs.
func TestLoginLimiterPrunesStaleEntries(t *testing.T) {
	l := newLoginLimiter()
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 500; i++ {
		l.Fail(ipN(i), base)
	}
	if got := len(l.fails); got < 500 {
		t.Fatalf("expected ~500 tracked IPs, got %d", got)
	}
	l.Fail("203.0.113.7", base.Add(loginLimiterWindow+time.Second))
	if got := len(l.fails); got > 5 {
		t.Fatalf("stale entries not pruned: %d remain", got)
	}
}

// A still-active ban must survive a prune sweep. (window == banDur here, so the
// ban is refreshed just before the first sweep to keep it live past it.)
func TestLoginLimiterBanSurvivesPrune(t *testing.T) {
	l := newLoginLimiter()
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		l.Fail("198.51.100.9", base) // banned until base+10m; lastPrune=base
	}
	if !l.Blocked("198.51.100.9", base) {
		t.Fatal("IP should be locked after 5 failures")
	}
	// Refresh the ban just inside the window (no sweep yet) -> banned until base+19m.
	l.Fail("198.51.100.9", base.Add(9*time.Minute))
	// Now a failure past the window triggers a sweep; the refreshed ban is still active.
	l.Fail(ipN(88888), base.Add(11*time.Minute))
	if !l.Blocked("198.51.100.9", base.Add(11*time.Minute)) {
		t.Fatal("active ban dropped by prune")
	}
}

func ipN(i int) string {
	return "10." + itoa(i/65536%256) + "." + itoa(i/256%256) + "." + itoa(i%256)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [3]byte
	p := len(b)
	for n > 0 {
		p--
		b[p] = byte('0' + n%10)
		n /= 10
	}
	return string(b[p:])
}
