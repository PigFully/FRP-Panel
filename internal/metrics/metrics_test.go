package metrics

import "testing"

func TestRingOverwritesAndOrders(t *testing.T) {
	r := NewRing(3)
	for i := int64(1); i <= 5; i++ {
		r.Add(Sample{AtUnixMs: i, CPU: float64(i)})
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3 samples, got %d", len(snap))
	}
	// Oldest-first; should be 3,4,5.
	for i, want := range []int64{3, 4, 5} {
		if snap[i].AtUnixMs != want {
			t.Errorf("snap[%d].AtUnixMs = %d, want %d", i, snap[i].AtUnixMs, want)
		}
	}
}

func TestRingSince(t *testing.T) {
	r := NewRing(10)
	for i := int64(1); i <= 6; i++ {
		r.Add(Sample{AtUnixMs: i * 1000})
	}
	got := r.Since(4000)
	if len(got) != 3 { // 4000,5000,6000
		t.Fatalf("Since(4000) len = %d, want 3", len(got))
	}
	if got[0].AtUnixMs != 4000 {
		t.Errorf("first = %d, want 4000", got[0].AtUnixMs)
	}
}

func TestSeqTrackerIdempotent(t *testing.T) {
	tr := NewSeqTracker(10)
	if tr.Accept(10) {
		t.Error("seq 10 == last should be rejected (duplicate)")
	}
	if tr.Accept(5) {
		t.Error("seq 5 < last should be rejected (stale)")
	}
	if !tr.Accept(11) {
		t.Error("seq 11 > last should be accepted")
	}
	if tr.Accept(11) {
		t.Error("replaying seq 11 should be rejected")
	}
	if !tr.Accept(12) {
		t.Error("seq 12 should be accepted")
	}
	if tr.Last() != 12 {
		t.Errorf("Last = %d, want 12", tr.Last())
	}
}

func TestMinuteAggAccumulate(t *testing.T) {
	var a MinuteAgg
	base := int64(1_700_000_000_000) // some unix ms
	a.Add(AggInput{AtUnixMs: base, CPU: 10, Mem: 40, NetRxBps: 100, NodeRxDelta: 500, TunInDelta: 200})
	a.Add(AggInput{AtUnixMs: base + 5000, CPU: 30, Mem: 60, NetRxBps: 300, NodeRxDelta: 700, TunInDelta: 300})
	if a.Count != 2 {
		t.Fatalf("count = %d", a.Count)
	}
	if a.AvgCPU() != 20 {
		t.Errorf("AvgCPU = %v, want 20", a.AvgCPU())
	}
	if a.AvgMem() != 50 {
		t.Errorf("AvgMem = %v, want 50", a.AvgMem())
	}
	if a.PeakRxBps != 300 {
		t.Errorf("PeakRxBps = %d, want 300", a.PeakRxBps)
	}
	if a.NodeRxBytes != 1200 {
		t.Errorf("NodeRxBytes = %d, want 1200", a.NodeRxBytes)
	}
	if a.TunInBytes != 500 {
		t.Errorf("TunInBytes = %d, want 500", a.TunInBytes)
	}
	if a.MinuteUnix != MinuteOf(base) {
		t.Errorf("MinuteUnix = %d, want %d", a.MinuteUnix, MinuteOf(base))
	}
}
