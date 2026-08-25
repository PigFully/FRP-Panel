package agent

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/frpanel/frpanel/internal/protocol"
)

// testServer builds the minimum Server the replay path touches: a WAL, a token
// to sign with, and a logger. It deliberately avoids NewServer, which would also
// spin up frps management and a /proc collector.
func testServer(t *testing.T) *Server {
	t.Helper()
	w, err := OpenWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return &Server{
		cfg:      &Config{AgentToken: "test-token"},
		wal:      w,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: map[*session]struct{}{},
	}
}

func newTestSession(buf int) *session {
	return &session{send: make(chan protocol.Envelope, buf), ip: "127.0.0.1", done: make(chan struct{})}
}

// replayInto runs backfill while concurrently consuming exactly want envelopes,
// standing in for the writer goroutine. Returning only after the consumer has
// finished makes the collected slice safe to read without further sync.
func replayInto(t *testing.T, s *Server, sess *session, afterSeq int64, want int) []int64 {
	t.Helper()
	seqs := make([]int64, 0, want)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for len(seqs) < want {
			select {
			case env := <-sess.send:
				if env.Type != protocol.TypeMetrics {
					continue
				}
				var m protocol.Metrics
				if err := env.Decode(&m); err != nil {
					return
				}
				if !m.Backfill {
					t.Errorf("replayed record seq=%d is not flagged Backfill; the panel would re-broadcast it to browsers", m.Seq)
				}
				seqs = append(seqs, m.Seq)
			case <-time.After(4 * time.Second):
				return
			}
		}
	}()
	s.backfill(sess, afterSeq)
	select {
	case <-finished:
	case <-time.After(6 * time.Second):
		t.Fatal("consumer did not finish; replay delivered fewer records than expected")
	}
	return seqs
}

// The core invariant: replay delivers every record past the watermark, in
// strictly ascending seq order, with nothing dropped. The panel's tracker accepts
// only a strictly greater seq, so any gap or reordering is silent data loss.
func TestBackfillDeliversAllRecordsInOrder(t *testing.T) {
	s := testServer(t)
	// Far more records than the send buffer holds: the old drop-on-full path lost
	// everything past the buffer's depth.
	const n = 1000
	appendN(t, s.wal, n)

	sess := newTestSession(8) // deliberately tiny, to force the sender to wait
	seqs := replayInto(t, s, sess, 0, n)

	if len(seqs) != n {
		t.Fatalf("replayed %d records, want %d (none may be dropped)", len(seqs), n)
	}
	for i, sq := range seqs {
		if sq != int64(i+1) {
			t.Fatalf("record %d has seq %d, want %d (must be ascending and gap-free)", i, sq, i+1)
		}
	}
	if !sess.ready.Load() {
		t.Error("session must be marked ready once replay caught up")
	}
}

func TestBackfillResumesFromWatermark(t *testing.T) {
	s := testServer(t)
	appendN(t, s.wal, 20)

	sess := newTestSession(64)
	seqs := replayInto(t, s, sess, 15, 5) // panel already committed through 15

	if len(seqs) != 5 || seqs[0] != 16 || seqs[4] != 20 {
		t.Fatalf("resumed with seqs %v, want 16..20", seqs)
	}
}

// With nothing to replay the session must go live immediately, not stay gated.
func TestBackfillEmptyWALOpensGate(t *testing.T) {
	s := testServer(t)
	sess := newTestSession(4)
	s.backfill(sess, 0)
	if !sess.ready.Load() {
		t.Error("empty WAL must still open the live-sample gate")
	}
	if len(sess.send) != 0 {
		t.Errorf("nothing should have been queued, got %d", len(sess.send))
	}
}

// Live samples must be withheld while a session is replaying, then flow once it
// is ready. This is what stops a live sample from jumping the panel's watermark
// past records that are still being replayed.
func TestLiveSamplesGatedUntilReady(t *testing.T) {
	s := testServer(t)
	sess := newTestSession(16)
	s.sessions[sess] = struct{}{}

	s.broadcastSample(protocol.Metrics{Seq: 9999})
	if n := len(sess.send); n != 0 {
		t.Fatalf("a gated session received %d live samples, want 0", n)
	}

	// Events carry no seq, so they must NOT be gated.
	s.broadcast(protocol.TypeEvent, protocol.Event{Kind: "frps_up", Detail: "ready"})
	if n := len(sess.send); n != 1 {
		t.Fatalf("events must flow while gated: queued %d, want 1", n)
	}

	sess.ready.Store(true)
	s.broadcastSample(protocol.Metrics{Seq: 10000})
	if n := len(sess.send); n != 2 {
		t.Fatalf("after ready, live samples must flow: queued %d, want 2", n)
	}
}

// A panel that disconnects mid-replay must stop the replay, not panic. The old
// code closed the send channel out from under this goroutine, and a send on a
// closed channel panics even inside a select — crashing the whole agent.
func TestBackfillSessionEndsMidReplay(t *testing.T) {
	s := testServer(t)
	appendN(t, s.wal, 500)

	sess := newTestSession(4) // fills immediately, so the sender blocks
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.backfill(sess, 0) // must return, not panic
	}()

	time.Sleep(50 * time.Millisecond)
	sess.close() // panel went away

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill did not stop after the session ended")
	}
	if !sess.ready.Load() {
		t.Error("ready must be set even when replay aborts, so a session is never stuck gated")
	}
}

// close() must be idempotent and must not make later senders panic.
func TestSessionCloseIsSafeForSenders(t *testing.T) {
	s := testServer(t)
	sess := newTestSession(1)
	sess.close()
	sess.close() // second close must not panic

	if s.sendWait(sess, protocol.TypeMetrics, protocol.Metrics{Seq: 1}) {
		t.Error("sendWait must report failure on a closed session")
	}
	// enqueue on a closed session must be a no-op, not a panic.
	s.enqueue(sess, protocol.TypeMetrics, "", protocol.Metrics{Seq: 2})
}
