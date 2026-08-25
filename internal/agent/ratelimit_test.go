package agent

import (
	"testing"
	"time"
)

// A public 8443 is scanned continuously; each one-shot scanner that sends a bad
// handshake must not leave a permanent map entry. After the window elapses, a
// later failure sweeps the stale IPs so the maps stay bounded.
func TestAuthLimiterPrunesStaleEntries(t *testing.T) {
	l := newAuthLimiter()
	base := time.Unix(1_700_000_000, 0)

	// 1000 distinct scanners each fail once (below the ban threshold).
	for i := 0; i < 1000; i++ {
		l.Fail(ipN(i), base)
	}
	if got := len(l.fails); got < 1000 {
		t.Fatalf("expected ~1000 tracked IPs, got %d", got)
	}

	// One more failure past the window triggers a sweep of the now-expired ones.
	l.Fail("203.0.113.7", base.Add(2*time.Minute))
	if got := len(l.fails); got > 5 {
		t.Fatalf("stale entries not pruned: %d remain", got)
	}
}

// A banned IP is retained until its ban lapses, then swept.
func TestAuthLimiterKeepsBannedUntilExpiry(t *testing.T) {
	l := newAuthLimiter()
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < l.maxFails; i++ {
		l.Fail("198.51.100.9", base)
	}
	if !l.Blocked("198.51.100.9", base.Add(time.Minute)) {
		t.Fatal("IP should be banned after maxFails")
	}
	// A sweep during the ban must not evict the ban.
	l.Fail(ipN(99999), base.Add(2*time.Minute))
	if !l.Blocked("198.51.100.9", base.Add(3*time.Minute)) {
		t.Fatal("ban dropped by prune before it expired")
	}
	// After banDur, the ban lapses.
	if l.Blocked("198.51.100.9", base.Add(l.banDur+time.Second)) {
		t.Fatal("ban should have expired")
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
