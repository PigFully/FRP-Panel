// Package procmetrics reads host resource usage from /proc: CPU% via diff
// sampling of /proc/stat, memory% from /proc/meminfo, and NIC byte counters
// from /proc/net/dev. Pure parsers are separated from OS access for testing.
package procmetrics

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// CPUTimes holds the aggregate jiffy counters from the first /proc/stat line.
type CPUTimes struct {
	Idle  uint64
	Total uint64
}

// ParseStat parses the aggregate "cpu" line of /proc/stat.
func ParseStat(r io.Reader) (CPUTimes, bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		f := strings.Fields(line)[1:]
		var total uint64
		var idle uint64
		for i, v := range f {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			total += n
			if i == 3 || i == 4 { // idle + iowait
				idle += n
			}
		}
		return CPUTimes{Idle: idle, Total: total}, true
	}
	return CPUTimes{}, false
}

// CPUPercent computes utilization between two samples. Returns 0 when the delta
// is non-positive (e.g. first sample / no movement).
func CPUPercent(prev, cur CPUTimes) float64 {
	dt := float64(cur.Total) - float64(prev.Total)
	di := float64(cur.Idle) - float64(prev.Idle)
	if dt <= 0 {
		return 0
	}
	pct := (dt - di) / dt * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// ParseMeminfo returns used-memory percent and total bytes from /proc/meminfo.
func ParseMeminfo(r io.Reader) (usedPct float64, totalBytes int64) {
	var totalKB, availKB int64
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			totalKB, _ = strconv.ParseInt(f[1], 10, 64)
		case "MemAvailable:":
			availKB, _ = strconv.ParseInt(f[1], 10, 64)
		}
	}
	if totalKB <= 0 {
		return 0, 0
	}
	used := totalKB - availKB
	if used < 0 {
		used = 0
	}
	return float64(used) / float64(totalKB) * 100, totalKB * 1024
}

// NetCounters is the summed RX/TX byte totals across physical interfaces.
type NetCounters struct {
	RxBytes int64
	TxBytes int64
}

// skipIface reports whether an interface should be excluded from the node-wide
// bandwidth counter (loopback and common virtual devices).
func skipIface(name string) bool {
	name = strings.TrimSpace(name)
	if name == "lo" {
		return true
	}
	for _, p := range []string{"veth", "docker", "br-", "virbr", "tun", "tap", "kube"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ParseNetDev sums RX (field 0) and TX (field 8) byte counters over all
// non-virtual interfaces in /proc/net/dev.
func ParseNetDev(r io.Reader) NetCounters {
	var nc NetCounters
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue // header rows
		}
		name := line[:colon]
		if skipIface(name) {
			continue
		}
		f := strings.Fields(line[colon+1:])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseInt(f[0], 10, 64)
		tx, _ := strconv.ParseInt(f[8], 10, 64)
		nc.RxBytes += rx
		nc.TxBytes += tx
	}
	return nc
}

// --- OS-backed readers ---

func readCPU() (CPUTimes, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUTimes{}, false
	}
	defer f.Close()
	return ParseStat(f)
}

func readMem() (float64, int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	return ParseMeminfo(f)
}

func readNet() NetCounters {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return NetCounters{}
	}
	defer f.Close()
	return ParseNetDev(f)
}
