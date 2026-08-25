// Package metrics provides the in-memory ring buffer for realtime samples,
// minute-level aggregation, and the (node,seq) idempotent merge that gives the
// traffic pipeline exactly-once semantics.
package metrics

import (
	"sync"
	"time"
)

// Sample is one realtime data point held in the ring buffer.
type Sample struct {
	AtUnixMs int64
	CPU      float64
	Mem      float64
	NetRxBps int64
	NetTxBps int64
	TunInBps int64 // tunnel (frp) aggregate, bytes/s
	TunOutBps int64
}

// Ring is a fixed-length circular buffer of realtime samples for one node.
// It never grows: old points are overwritten. Safe for concurrent use.
type Ring struct {
	mu   sync.RWMutex
	buf  []Sample
	head int // index of next write
	size int // number of valid entries
}

// NewRing creates a ring holding at most capacity samples.
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Sample, capacity)}
}

// Add appends a sample, overwriting the oldest when full.
func (r *Ring) Add(s Sample) {
	r.mu.Lock()
	r.buf[r.head] = s
	r.head = (r.head + 1) % len(r.buf)
	if r.size < len(r.buf) {
		r.size++
	}
	r.mu.Unlock()
}

// Snapshot returns all valid samples in chronological (oldest-first) order.
func (r *Ring) Snapshot() []Sample {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Sample, 0, r.size)
	start := (r.head - r.size + len(r.buf)) % len(r.buf)
	for i := 0; i < r.size; i++ {
		out = append(out, r.buf[(start+i)%len(r.buf)])
	}
	return out
}

// Since returns valid samples with AtUnixMs >= sinceMs, chronological order.
func (r *Ring) Since(sinceMs int64) []Sample {
	all := r.Snapshot()
	i := 0
	for i < len(all) && all[i].AtUnixMs < sinceMs {
		i++
	}
	return all[i:]
}

// Len returns the number of valid samples.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// SeqTracker enforces monotonic (node,seq) acceptance for exactly-once merge.
// A sample whose seq is <= the last committed seq is a duplicate/stale replay
// and must be dropped. last is persisted by the panel per node.
type SeqTracker struct {
	mu   sync.Mutex
	last int64
}

// NewSeqTracker starts a tracker at the given last-committed seq (0 for fresh).
func NewSeqTracker(last int64) *SeqTracker { return &SeqTracker{last: last} }

// Accept reports whether seq is new (strictly greater than the last committed
// seq). When accepted it advances the watermark. Duplicates return false.
func (t *SeqTracker) Accept(seq int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= t.last {
		return false
	}
	t.last = seq
	return true
}

// Last returns the current committed watermark.
func (t *SeqTracker) Last() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// MinuteAgg accumulates samples for one node within a minute bucket, producing
// averages/peaks for gauges and summed byte deltas for traffic.
type MinuteAgg struct {
	MinuteUnix int64 // truncated to minute
	Count      int
	sumCPU     float64
	sumMem     float64
	PeakRxBps  int64
	PeakTxBps  int64
	NodeRxBytes int64 // summed node NIC deltas in the minute
	NodeTxBytes int64
	TunInBytes  int64 // summed tunnel deltas in the minute
	TunOutBytes int64
}

// AggInput is one contribution to a minute aggregate.
type AggInput struct {
	AtUnixMs   int64
	CPU        float64
	Mem        float64
	NetRxBps   int64
	NetTxBps   int64
	NodeRxDelta int64
	NodeTxDelta int64
	TunInDelta  int64
	TunOutDelta int64
}

// MinuteOf truncates a unix-ms timestamp to the start of its minute (unix sec).
func MinuteOf(atUnixMs int64) int64 {
	return time.UnixMilli(atUnixMs).Truncate(time.Minute).Unix()
}

// Add folds one input into the aggregate.
func (a *MinuteAgg) Add(in AggInput) {
	if a.Count == 0 {
		a.MinuteUnix = MinuteOf(in.AtUnixMs)
	}
	a.Count++
	a.sumCPU += in.CPU
	a.sumMem += in.Mem
	if in.NetRxBps > a.PeakRxBps {
		a.PeakRxBps = in.NetRxBps
	}
	if in.NetTxBps > a.PeakTxBps {
		a.PeakTxBps = in.NetTxBps
	}
	a.NodeRxBytes += in.NodeRxDelta
	a.NodeTxBytes += in.NodeTxDelta
	a.TunInBytes += in.TunInDelta
	a.TunOutBytes += in.TunOutDelta
}

// AvgCPU returns the mean CPU over the bucket (0 if empty).
func (a *MinuteAgg) AvgCPU() float64 {
	if a.Count == 0 {
		return 0
	}
	return a.sumCPU / float64(a.Count)
}

// AvgMem returns the mean memory over the bucket (0 if empty).
func (a *MinuteAgg) AvgMem() float64 {
	if a.Count == 0 {
		return 0
	}
	return a.sumMem / float64(a.Count)
}
