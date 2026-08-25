package procmetrics

import (
	"strings"
	"testing"
)

const statA = `cpu  100 0 100 800 0 0 0 0 0 0
cpu0 50 0 50 400 0 0 0 0 0 0
intr 12345
`
const statB = `cpu  150 0 150 900 0 0 0 0 0 0
`

func TestParseStatAndPercent(t *testing.T) {
	a, ok := ParseStat(strings.NewReader(statA))
	if !ok {
		t.Fatal("parse A failed")
	}
	// total = 100+0+100+800 = 1000, idle = 800
	if a.Total != 1000 || a.Idle != 800 {
		t.Fatalf("A = %+v, want total=1000 idle=800", a)
	}
	b, _ := ParseStat(strings.NewReader(statB))
	// dt = 1200-1000=200, di=900-800=100 -> busy 100/200 = 50%
	if got := CPUPercent(a, b); got != 50 {
		t.Errorf("CPUPercent = %v, want 50", got)
	}
	// No movement -> 0.
	if got := CPUPercent(b, b); got != 0 {
		t.Errorf("CPUPercent(no delta) = %v, want 0", got)
	}
}

func TestParseMeminfo(t *testing.T) {
	mem := `MemTotal:       1000000 kB
MemFree:         200000 kB
MemAvailable:    600000 kB
Buffers:          10000 kB
`
	pct, total := ParseMeminfo(strings.NewReader(mem))
	// used = 1000000-600000 = 400000 -> 40%
	if pct != 40 {
		t.Errorf("mem pct = %v, want 40", pct)
	}
	if total != 1000000*1024 {
		t.Errorf("mem total = %d, want %d", total, 1000000*1024)
	}
}

func TestParseNetDevExcludesLo(t *testing.T) {
	dev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets
    lo: 5000       50    0    0    0     0          0         0    5000       50
  eth0: 1000       10    0    0    0     0          0         0    2000       20
 veth0: 9999       10    0    0    0     0          0         0    9999       20
`
	nc := ParseNetDev(strings.NewReader(dev))
	if nc.RxBytes != 1000 || nc.TxBytes != 2000 {
		t.Errorf("net = %+v, want rx=1000 tx=2000 (lo+veth excluded)", nc)
	}
}
