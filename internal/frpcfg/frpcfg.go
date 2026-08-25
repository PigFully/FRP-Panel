// Package frpcfg renders frpc and frps TOML configuration (frp v0.61+ format)
// and computes the frps allowPorts whitelist with reserved-segment exclusions.
package frpcfg

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Proxy is one frpc proxy definition (one mapping target).
type Proxy struct {
	Name       string
	Type       string // tcp|udp
	LocalIP    string
	LocalPort  int
	RemotePort int
	Comment    string
}

// FrpcParams are the inputs for a node's frpc config.
type FrpcParams struct {
	ServerAddr    string
	ServerPort    int
	Token         string
	TLSEnable     bool
	AdminAddr     string
	AdminPort     int
	AdminUser     string
	AdminPassword string
	LogFile       string
	LogLevel      string
	Proxies       []Proxy
}

func q(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// RenderFrpc renders a complete frpc TOML.
func RenderFrpc(p FrpcParams) string {
	if p.LogLevel == "" {
		p.LogLevel = "info"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "serverAddr = %s\n", q(p.ServerAddr))
	fmt.Fprintf(&b, "serverPort = %d\n", p.ServerPort)
	fmt.Fprintf(&b, "loginFailExit = false\n")
	fmt.Fprintf(&b, "auth.method = \"token\"\n")
	fmt.Fprintf(&b, "auth.token = %s\n", q(p.Token))
	fmt.Fprintf(&b, "transport.tls.enable = %t\n", p.TLSEnable)
	fmt.Fprintf(&b, "transport.heartbeatInterval = 10\n")
	fmt.Fprintf(&b, "transport.heartbeatTimeout = 30\n")
	if p.AdminPort > 0 {
		fmt.Fprintf(&b, "webServer.addr = %s\n", q(p.AdminAddr))
		fmt.Fprintf(&b, "webServer.port = %d\n", p.AdminPort)
		fmt.Fprintf(&b, "webServer.user = %s\n", q(p.AdminUser))
		fmt.Fprintf(&b, "webServer.password = %s\n", q(p.AdminPassword))
	}
	if p.LogFile != "" {
		fmt.Fprintf(&b, "log.to = %s\n", q(p.LogFile))
		fmt.Fprintf(&b, "log.level = %s\n", q(p.LogLevel))
		fmt.Fprintf(&b, "log.maxDays = 3\n")
	}
	for _, px := range p.Proxies {
		typ := px.Type
		if typ == "" {
			typ = "tcp"
		}
		lip := px.LocalIP
		if lip == "" {
			lip = "127.0.0.1"
		}
		b.WriteString("\n[[proxies]]\n")
		fmt.Fprintf(&b, "name = %s\n", q(px.Name))
		fmt.Fprintf(&b, "type = %s\n", q(typ))
		fmt.Fprintf(&b, "localIP = %s\n", q(lip))
		fmt.Fprintf(&b, "localPort = %d\n", px.LocalPort)
		fmt.Fprintf(&b, "remotePort = %d\n", px.RemotePort)
		if px.Comment != "" {
			fmt.Fprintf(&b, "# %s\n", strings.ReplaceAll(px.Comment, "\n", " "))
		}
	}
	return b.String()
}

// FrpsParams are the inputs for a node's static frps config.
type FrpsParams struct {
	BindPort      int
	Token         string
	TLSForce      bool
	AdminAddr     string
	AdminPort     int
	AdminUser     string
	AdminPassword string
	LogFile       string
	LogLevel      string
	AllowLo       int
	AllowHi       int
	Reserved      [][2]int // inclusive ranges to exclude from allowPorts
}

// RenderFrps renders a complete frps TOML with an allowPorts whitelist that
// excludes the reserved segments.
func RenderFrps(p FrpsParams) string {
	if p.LogLevel == "" {
		p.LogLevel = "info"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "bindPort = %d\n", p.BindPort)
	fmt.Fprintf(&b, "auth.method = \"token\"\n")
	fmt.Fprintf(&b, "auth.token = %s\n", q(p.Token))
	fmt.Fprintf(&b, "transport.tls.force = %t\n", p.TLSForce)
	if p.AdminPort > 0 {
		// Admin/dashboard bound to loopback only: needed by the agent to read
		// the proxy list and per-proxy traffic. Never exposed publicly.
		fmt.Fprintf(&b, "webServer.addr = %s\n", q(p.AdminAddr))
		fmt.Fprintf(&b, "webServer.port = %d\n", p.AdminPort)
		fmt.Fprintf(&b, "webServer.user = %s\n", q(p.AdminUser))
		fmt.Fprintf(&b, "webServer.password = %s\n", q(p.AdminPassword))
	}
	if p.LogFile != "" {
		fmt.Fprintf(&b, "log.to = %s\n", q(p.LogFile))
		fmt.Fprintf(&b, "log.level = %s\n", q(p.LogLevel))
		fmt.Fprintf(&b, "log.maxDays = 3\n")
	}
	ranges := AllowedRanges(p.AllowLo, p.AllowHi, p.Reserved)
	b.WriteString("allowPorts = [\n")
	for _, r := range ranges {
		fmt.Fprintf(&b, "  { start = %d, end = %d },\n", r[0], r[1])
	}
	b.WriteString("]\n")
	return b.String()
}

// AllowedRanges returns the sub-ranges of [lo,hi] that remain after removing
// the given (inclusive) excluded ranges. Excludes are normalized, sorted and
// merged; ranges fully outside [lo,hi] are ignored.
func AllowedRanges(lo, hi int, excludes [][2]int) [][2]int {
	if lo > hi {
		return nil
	}
	// Normalize + clamp excludes into [lo,hi].
	var ex [][2]int
	for _, e := range excludes {
		a, b := e[0], e[1]
		if a > b {
			a, b = b, a
		}
		if b < lo || a > hi {
			continue
		}
		if a < lo {
			a = lo
		}
		if b > hi {
			b = hi
		}
		ex = append(ex, [2]int{a, b})
	}
	sort.Slice(ex, func(i, j int) bool { return ex[i][0] < ex[j][0] })
	// Merge overlaps/adjacency.
	var merged [][2]int
	for _, e := range ex {
		if len(merged) > 0 && e[0] <= merged[len(merged)-1][1]+1 {
			if e[1] > merged[len(merged)-1][1] {
				merged[len(merged)-1][1] = e[1]
			}
			continue
		}
		merged = append(merged, e)
	}
	// Walk the gaps.
	var out [][2]int
	cur := lo
	for _, e := range merged {
		if e[0] > cur {
			out = append(out, [2]int{cur, e[0] - 1})
		}
		if e[1]+1 > cur {
			cur = e[1] + 1
		}
	}
	if cur <= hi {
		out = append(out, [2]int{cur, hi})
	}
	return out
}

// ProxyName builds the deterministic proxy name for a mapping target.
func ProxyName(mappingID, nodeID int64) string {
	return fmt.Sprintf("m%d_n%d", mappingID, nodeID)
}

// ParseProxyName extracts (mappingID, nodeID) from a proxy name. ok is false if
// the name is not in the expected m<id>_n<id> form (e.g. a foreign proxy).
func ParseProxyName(name string) (mappingID, nodeID int64, ok bool) {
	if !strings.HasPrefix(name, "m") {
		return 0, 0, false
	}
	rest := name[1:]
	i := strings.IndexByte(rest, '_')
	if i < 0 || i+1 >= len(rest) || rest[i+1] != 'n' {
		return 0, 0, false
	}
	m, err1 := strconv.ParseInt(rest[:i], 10, 64)
	n, err2 := strconv.ParseInt(rest[i+2:], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return m, n, true
}
