package panel

import (
	"context"
	"sync"
	"time"

	"github.com/frpanel/frpanel/internal/metrics"
	"github.com/frpanel/frpanel/internal/protocol"
)

const (
	metricsIntervalSec = 5
	ringCapacity       = 90 // ~7.5 min of 5s samples (spec wants ≥5 min)
)

type dayTraffic struct{ nodeRx, nodeTx, tunIn, tunOut int64 }

type nodeState struct {
	ring *metrics.Ring
	seq  *metrics.SeqTracker

	aggMu sync.Mutex
	agg   *metrics.MinuteAgg

	trafMu sync.Mutex
	traf   map[string]*dayTraffic

	lastPersistedSeq int64
}

// Pipeline is the three-tier metrics path: in-memory ring (realtime) -> minute
// aggregate (batch INSERT) -> hourly/daily rollups. It also does exactly-once
// traffic accounting keyed by (node, seq).
type Pipeline struct {
	app *App
	mu  sync.Mutex
	st  map[int64]*nodeState
}

// NewPipeline builds a pipeline bound to the app.
func NewPipeline(app *App) *Pipeline {
	return &Pipeline{app: app, st: map[int64]*nodeState{}}
}

// InitNode ensures state for a node, seeding the seq watermark from the DB.
func (p *Pipeline) InitNode(nodeID, lastSeq int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.st[nodeID]; ok {
		if lastSeq > s.seq.Last() {
			s.seq = metrics.NewSeqTracker(lastSeq)
		}
		return
	}
	p.st[nodeID] = &nodeState{
		ring: metrics.NewRing(ringCapacity),
		seq:  metrics.NewSeqTracker(lastSeq),
		agg:  &metrics.MinuteAgg{},
		traf: map[string]*dayTraffic{},
		lastPersistedSeq: lastSeq,
	}
}

// RemoveNode drops a node's in-memory state.
func (p *Pipeline) RemoveNode(nodeID int64) {
	p.mu.Lock()
	delete(p.st, nodeID)
	p.mu.Unlock()
}

func (p *Pipeline) get(nodeID int64) *nodeState {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.st[nodeID]
	if s == nil {
		s = &nodeState{ring: metrics.NewRing(ringCapacity), seq: metrics.NewSeqTracker(0), agg: &metrics.MinuteAgg{}, traf: map[string]*dayTraffic{}}
		p.st[nodeID] = s
	}
	return s
}

// Account folds a metrics sample into the pipeline. It returns the realtime
// sample and true when the caller should broadcast it to browsers (i.e. the
// sample is new and not a WAL backfill).
func (p *Pipeline) Account(nodeID int64, m protocol.Metrics) (metrics.Sample, bool) {
	s := p.get(nodeID)
	if !s.seq.Accept(m.Seq) {
		return metrics.Sample{}, false // duplicate / stale (exactly-once)
	}

	var tunIn, tunOut int64
	for _, px := range m.Proxies {
		tunIn += px.DeltaIn
		tunOut += px.DeltaOut
		// frps reports proxies it remembers but has no conf block for (offline /
		// unconfigured) with no remote port, which arrives here as 0. Such an
		// entry addresses no mapping target: folding it in logs a bogus
		// "隧道 tcp:0 状态变更为 offline" and updates zero rows. Its traffic deltas
		// are still real, so they stay in the node totals above.
		if px.RemotePort <= 0 {
			continue
		}
		// Live tunnel status transitions -> event log + browser push.
		if px.Status != "" && p.app.setLive(nodeID, px.RemotePort, px.Status) {
			p.app.onTunnelStatusChange(nodeID, px.RemotePort, px.Proto, px.Status)
		}
	}

	// Traffic accounting (both scopes) into the current day bucket.
	at := m.SampledAt
	if at == 0 {
		at = time.Now().UnixMilli()
	}
	day := ShanghaiDay(time.UnixMilli(at))
	s.trafMu.Lock()
	d := s.traf[day]
	if d == nil {
		d = &dayTraffic{}
		s.traf[day] = d
	}
	d.nodeRx += m.NetRxDelta
	d.nodeTx += m.NetTxDelta
	d.tunIn += tunIn
	d.tunOut += tunOut
	s.trafMu.Unlock()

	// Minute aggregate (resource + bandwidth peaks).
	s.aggMu.Lock()
	s.agg.Add(metrics.AggInput{
		AtUnixMs: at, CPU: m.CPU, Mem: m.Mem,
		NetRxBps: m.NetRxBps, NetTxBps: m.NetTxBps,
		NodeRxDelta: m.NetRxDelta, NodeTxDelta: m.NetTxDelta,
		TunInDelta: tunIn, TunOutDelta: tunOut,
	})
	s.aggMu.Unlock()

	if m.Backfill {
		return metrics.Sample{}, false
	}
	sample := metrics.Sample{
		AtUnixMs: at, CPU: m.CPU, Mem: m.Mem,
		NetRxBps: m.NetRxBps, NetTxBps: m.NetTxBps,
		TunInBps: tunIn / metricsIntervalSec, TunOutBps: tunOut / metricsIntervalSec,
	}
	s.ring.Add(sample)
	return sample, true
}

// RingSnapshot returns a node's recent realtime samples (for browser backfill).
func (p *Pipeline) RingSnapshot(nodeID int64) []metrics.Sample {
	p.mu.Lock()
	s := p.st[nodeID]
	p.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.ring.Snapshot()
}

// Run drives the aggregate flush, rollups, retention cleanup and durable
// commit-seq persistence until ctx is done.
func (p *Pipeline) Run(ctx context.Context) {
	flush := time.NewTicker(60 * time.Second)
	rollup := time.NewTicker(5 * time.Minute)
	cleanup := time.NewTicker(6 * time.Hour)
	defer flush.Stop()
	defer rollup.Stop()
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			p.flush(context.Background()) // final flush on shutdown
			return
		case <-flush.C:
			p.flush(ctx)
		case <-rollup.C:
			if p.app.DBUp() {
				if err := p.app.store.RollupHourly(ctx); err != nil {
					p.app.log.Warn("rollup hourly", "err", err)
				}
				if err := p.app.store.RollupDaily(ctx); err != nil {
					p.app.log.Warn("rollup daily", "err", err)
				}
			}
		case <-cleanup.C:
			if p.app.DBUp() {
				if err := p.app.store.CleanupMetrics(ctx); err != nil {
					p.app.log.Warn("cleanup metrics", "err", err)
				}
			}
		}
	}
}

// flush snapshots+resets each node's minute aggregate and traffic buffer and
// writes them to the DB in batches. On DB failure the batch is dropped and a
// counter incremented (spec §6.8), and processing continues.
func (p *Pipeline) flush(ctx context.Context) {
	p.mu.Lock()
	ids := make([]int64, 0, len(p.st))
	states := make([]*nodeState, 0, len(p.st))
	for id, s := range p.st {
		ids = append(ids, id)
		states = append(states, s)
	}
	p.mu.Unlock()

	var rows []MinuteRow
	type tflush struct {
		nodeID int64
		day    string
		d      dayTraffic
	}
	var trafRows []tflush
	// seqAt[i] pairs with states[i]: the watermark this batch would make durable
	// for that node. Captured before the buffers are drained, so anything
	// accounted afterwards lands in the next batch instead of being claimed here.
	seqAt := make([]int64, len(ids))

	for i, id := range ids {
		s := states[i]
		seqAt[i] = s.seq.Last()
		s.aggMu.Lock()
		agg := s.agg
		s.agg = &metrics.MinuteAgg{}
		s.aggMu.Unlock()
		if agg.Count > 0 {
			rows = append(rows, MinuteRow{
				NodeID: id, TS: time.Unix(agg.MinuteUnix, 0).UTC(),
				CPUAvg: agg.AvgCPU(), MemAvg: agg.AvgMem(),
				RxPeak: agg.PeakRxBps, TxPeak: agg.PeakTxBps,
			})
		}
		s.trafMu.Lock()
		traf := s.traf
		s.traf = map[string]*dayTraffic{}
		s.trafMu.Unlock()
		for day, d := range traf {
			if d.nodeRx|d.nodeTx|d.tunIn|d.tunOut != 0 {
				trafRows = append(trafRows, tflush{nodeID: id, day: day, d: *d})
			}
		}
	}

	if !p.app.DBUp() {
		if len(rows) > 0 || len(trafRows) > 0 {
			p.app.droppedBatches.Add(1)
		}
		return
	}
	if len(rows) > 0 {
		if err := p.app.store.BatchUpsertMinutely(ctx, rows); err != nil {
			p.app.droppedBatches.Add(1)
			p.app.log.Warn("flush minutely dropped", "err", err)
		}
	}
	// Traffic is the only scope the agent's WAL can replay, so a node whose
	// traffic write fails must not have its watermark advanced past the lost rows.
	failed := map[int64]bool{}
	for _, t := range trafRows {
		if err := p.app.store.IncrTrafficDaily(ctx, t.nodeID, t.day, t.d.nodeRx, t.d.nodeTx, t.d.tunIn, t.d.tunOut); err != nil {
			failed[t.nodeID] = true
			p.app.droppedBatches.Add(1)
			p.app.log.Warn("flush traffic dropped", "node", t.nodeID, "err", err)
		}
	}
	// Persist commit-seq watermarks. This value is the agent's WAL replay resume
	// point, so it may only move over records that actually reached the DB —
	// advancing it past a dropped batch is what previously turned a MySQL blip
	// into permanent traffic loss. A node held back here is recovered on the next
	// panel restart, when its tracker is seeded from this same value and the
	// agent replays from it.
	for i, id := range ids {
		if failed[id] {
			continue
		}
		s := states[i]
		if seqAt[i] > s.lastPersistedSeq {
			if err := p.app.store.SetNodeLastCommitSeq(ctx, id, seqAt[i]); err == nil {
				s.lastPersistedSeq = seqAt[i]
			}
		}
	}
}

// LastSample returns a node's most recent realtime sample, if any.
func (p *Pipeline) LastSample(nodeID int64) (metrics.Sample, bool) {
	snap := p.RingSnapshot(nodeID)
	if len(snap) == 0 {
		return metrics.Sample{}, false
	}
	return snap[len(snap)-1], true
}

// CommitSeq returns the current in-memory seq watermark for a node.
func (p *Pipeline) CommitSeq(nodeID int64) int64 {
	p.mu.Lock()
	s := p.st[nodeID]
	p.mu.Unlock()
	if s == nil {
		return 0
	}
	return s.seq.Last()
}
