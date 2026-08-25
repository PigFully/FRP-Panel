// Package portutil parses /proc/net/{tcp,tcp6,udp,udp6} to find listening
// ports and their owning processes, and validates public port choices against
// the reserved-segment policy. Used by the panel (local-port LISTEN precheck)
// and the agent (remote-port occupancy precheck).
package portutil

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TCP socket state codes from the kernel (hex). We only care about LISTEN.
const tcpListen = "0A"

// Reserved is the fixed set of ports that must never be used as a public
// remote_port: ssh, frps bind, agent mgmt, and the frpc admin range.
// (Port 22 and anything <1024 is also excluded by the frps allowPorts config.)
const (
	PortSSH       = 22
	PortFrpsBind  = 7000
	PortAgentMgmt = 8443
	AdminLow      = 7400
	AdminHigh     = 7500
)

// SockEntry is one row of a /proc/net/{tcp,udp}[6] file (all four share layout).
type SockEntry struct {
	LocalPort int
	State     string
	Inode     string
}

// ParseProcNet parses a /proc/net/tcp|tcp6|udp|udp6 stream. The four files share
// an identical column layout; only the address width differs, which does not
// affect the fields we read (local address column and state/inode).
func ParseProcNet(r io.Reader) ([]SockEntry, error) {
	var out []SockEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first { // header row: "sl  local_address rem_address   st ..."
			first = false
			continue
		}
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		// Columns: 0 sl, 1 local_address, 2 rem_address, 3 st, ... 9 inode
		if len(f) < 10 {
			continue
		}
		local := f[1]
		colon := strings.LastIndexByte(local, ':')
		if colon < 0 {
			continue
		}
		port, err := strconv.ParseInt(local[colon+1:], 16, 32)
		if err != nil {
			continue
		}
		out = append(out, SockEntry{LocalPort: int(port), State: f[3], Inode: f[9]})
	}
	return out, sc.Err()
}

// ListenPorts returns the set of occupied ports from parsed entries. For tcp,
// "occupied" means state LISTEN; for udp (which has no LISTEN state) any bound
// socket counts. The value is the socket inode (for process resolution).
func ListenPorts(entries []SockEntry, proto string) map[int]string {
	m := make(map[int]string)
	udp := strings.HasPrefix(strings.ToLower(proto), "udp")
	for _, e := range entries {
		if udp || e.State == tcpListen {
			if _, ok := m[e.LocalPort]; !ok {
				m[e.LocalPort] = e.Inode
			}
		}
	}
	return m
}

// procFiles maps a logical proto to the kernel files to inspect.
func procFiles(proto string) []string {
	if strings.HasPrefix(strings.ToLower(proto), "udp") {
		return []string{"/proc/net/udp", "/proc/net/udp6"}
	}
	return []string{"/proc/net/tcp", "/proc/net/tcp6"}
}

// LocalListen reports whether the given port is currently occupied for proto
// on this host, and (best effort) the owning process name.
func LocalListen(port int, proto string) (listening bool, process string) {
	var inode string
	for _, fp := range procFiles(proto) {
		f, err := os.Open(fp)
		if err != nil {
			continue
		}
		entries, _ := ParseProcNet(f)
		f.Close()
		if in, ok := ListenPorts(entries, proto)[port]; ok {
			inode = in
			listening = true
			break
		}
	}
	if !listening {
		return false, ""
	}
	if name, ok := processForInode(inode); ok {
		process = name
	}
	return true, process
}

// processForInode scans /proc/<pid>/fd for a socket with the given inode and
// returns the process comm. Best effort; returns false if not resolvable.
func processForInode(inode string) (string, bool) {
	if inode == "" || inode == "0" {
		return "", false
	}
	target := "socket:[" + inode + "]"
	pids, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return "", false
	}
	for _, pdir := range pids {
		fds, err := os.ReadDir(filepath.Join(pdir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(pdir, "fd", fd.Name()))
			if err != nil {
				continue
			}
			if link == target {
				if b, err := os.ReadFile(filepath.Join(pdir, "comm")); err == nil {
					return strings.TrimSpace(string(b)), true
				}
				return "", false
			}
		}
	}
	return "", false
}
