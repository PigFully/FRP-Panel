package procmetrics

import "time"

// Reading is one full host-metrics sample.
type Reading struct {
	CPUPercent float64
	MemPercent float64
	MemTotal   int64
	NetRxBps   int64 // instantaneous, bytes/s
	NetTxBps   int64
	NetRxDelta int64 // bytes since previous reading
	NetTxDelta int64
	Primed     bool // false on the very first sample (no diff baseline yet)
}

// Collector holds the previous CPU/net baselines for diff sampling. The first
// call to Sample only primes the baselines and returns Primed=false, because
// CPU% and bandwidth are meaningless without a delta.
type Collector struct {
	haveCPU  bool
	prevCPU  CPUTimes
	haveNet  bool
	prevNet  NetCounters
	prevTime time.Time
}

// NewCollector returns an unprimed collector.
func NewCollector() *Collector { return &Collector{} }

// Sample reads /proc and returns a Reading. now is injected for testability.
func (c *Collector) Sample(now time.Time) Reading {
	var r Reading
	cpu, okc := readCPU()
	mem, total := readMem()
	net := readNet()
	r.MemPercent = mem
	r.MemTotal = total

	primed := true
	if okc {
		if c.haveCPU {
			r.CPUPercent = CPUPercent(c.prevCPU, cpu)
		} else {
			primed = false
		}
		c.prevCPU = cpu
		c.haveCPU = true
	}
	if c.haveNet {
		dr := net.RxBytes - c.prevNet.RxBytes
		dt := net.TxBytes - c.prevNet.TxBytes
		if dr < 0 {
			dr = 0 // counter reset (reboot/iface flap)
		}
		if dt < 0 {
			dt = 0
		}
		r.NetRxDelta = dr
		r.NetTxDelta = dt
		secs := now.Sub(c.prevTime).Seconds()
		if secs > 0 {
			r.NetRxBps = int64(float64(dr) / secs)
			r.NetTxBps = int64(float64(dt) / secs)
		}
	} else {
		primed = false
	}
	c.prevNet = net
	c.haveNet = true
	c.prevTime = now
	r.Primed = primed
	return r
}
