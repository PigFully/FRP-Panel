package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func appendN(t *testing.T, w *WAL, n int) []int64 {
	t.Helper()
	var seqs []int64
	for i := 0; i < n; i++ {
		seq := w.NextSeq()
		if err := w.Append(TrafficRec{
			Seq: seq, AtMs: time.Now().UnixMilli(),
			NodeRxDelta: int64(i), NodeTxDelta: int64(i * 2),
			Proxies: []ProxyDelta{{RemotePort: 33223, Proto: "tcp", In: int64(i), Out: int64(i), Status: "online"}},
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

func streamAll(t *testing.T, w *WAL, after int64) []TrafficRec {
	t.Helper()
	var got []TrafficRec
	if err := w.Stream(after, func(r TrafficRec) bool {
		got = append(got, r)
		return true
	}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	return got
}

func TestWALStreamFiltersAndOrders(t *testing.T) {
	w, err := OpenWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	appendN(t, w, 10)

	// Everything.
	got := streamAll(t, w, 0)
	if len(got) != 10 {
		t.Fatalf("got %d records, want 10", len(got))
	}
	for i, r := range got {
		if r.Seq != int64(i+1) {
			t.Fatalf("record %d has seq %d, want %d (must be ascending, gap-free)", i, r.Seq, i+1)
		}
	}
	// Past a watermark: strictly greater, never equal.
	got = streamAll(t, w, 7)
	if len(got) != 3 || got[0].Seq != 8 || got[2].Seq != 10 {
		t.Fatalf("after seq 7 got %d records starting at %d", len(got), got[0].Seq)
	}
	// Caught up.
	if got = streamAll(t, w, 10); len(got) != 0 {
		t.Fatalf("after seq 10 got %d records, want 0", len(got))
	}
	if got = streamAll(t, w, 999); len(got) != 0 {
		t.Fatalf("beyond the end got %d records, want 0", len(got))
	}
}

func TestWALStreamEarlyStop(t *testing.T) {
	w, err := OpenWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	appendN(t, w, 50)

	seen := 0
	err = w.Stream(0, func(TrafficRec) bool {
		seen++
		return seen < 5 // stop after the 5th
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if seen != 5 {
		t.Errorf("callback ran %d times, want 5 (early stop must halt the scan)", seen)
	}
}

func TestWALStreamPayloadRoundTrip(t *testing.T) {
	w, err := OpenWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	want := TrafficRec{
		Seq: w.NextSeq(), AtMs: 1755000000000,
		NodeRxDelta: 4096, NodeTxDelta: 8192,
		Proxies: []ProxyDelta{
			{RemotePort: 33223, Proto: "tcp", In: 11, Out: 22, Status: "online"},
			{RemotePort: 40000, Proto: "udp", In: 33, Out: 44, Status: "offline"},
		},
	}
	if err := w.Append(want); err != nil {
		t.Fatal(err)
	}
	got := streamAll(t, w, 0)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	g := got[0]
	if g.Seq != want.Seq || g.AtMs != want.AtMs || g.NodeRxDelta != want.NodeRxDelta || g.NodeTxDelta != want.NodeTxDelta {
		t.Errorf("header mismatch: got %+v want %+v", g, want)
	}
	if len(g.Proxies) != 2 || g.Proxies[0].RemotePort != 33223 || g.Proxies[1].Proto != "udp" || g.Proxies[1].Out != 44 {
		t.Errorf("proxy deltas mismatch: got %+v", g.Proxies)
	}
}

// A half-written trailing line (agent killed mid-Append) must not abort the
// replay of the records before it.
func TestWALStreamSkipsCorruptLine(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, w, 3)
	w.Close()

	f, err := os.OpenFile(filepath.Join(dir, "traffic.wal"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"seq":4,"at_ms":17550`) // torn
	f.WriteString("\n")
	f.Close()

	w2, err := OpenWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if got := streamAll(t, w2, 0); len(got) != 3 {
		t.Errorf("got %d records, want the 3 intact ones", len(got))
	}
}

func TestWALStreamMissingFile(t *testing.T) {
	w := &WAL{path: filepath.Join(t.TempDir(), "nope.wal")}
	if err := w.Stream(0, func(TrafficRec) bool { return true }); err != nil {
		t.Errorf("a missing WAL is not an error, got %v", err)
	}
}

// Seq numbering must not restart across an agent restart, or the panel's
// watermark would reject every record after the restart.
func TestWALSeqMonotonicAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, w, 5)
	w.Close()

	w2, err := OpenWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if next := w2.NextSeq(); next != 6 {
		t.Errorf("first seq after reopen = %d, want 6", next)
	}
}

func TestWALRotateDropsOldKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	for i := 0; i < 3; i++ {
		if err := w.Append(TrafficRec{Seq: w.NextSeq(), AtMs: old}); err != nil {
			t.Fatal(err)
		}
	}
	appendN(t, w, 2) // recent
	if err := w.Rotate(24 * time.Hour); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	got := streamAll(t, w, 0)
	if len(got) != 2 {
		t.Fatalf("after rotate got %d records, want the 2 recent ones", len(got))
	}
	// Appends must still work against the rewritten file.
	appendN(t, w, 1)
	if got = streamAll(t, w, 0); len(got) != 3 {
		t.Errorf("append after rotate lost records: got %d, want 3", len(got))
	}
}
