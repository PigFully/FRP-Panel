package frpcfg

import (
	"strings"
	"testing"
)

func TestAllowedRangesReservedExclusion(t *testing.T) {
	// Exclude 7000, 8443 and 7400-7500 from [1024,65535].
	got := AllowedRanges(1024, 65535, [][2]int{{7000, 7000}, {8443, 8443}, {7400, 7500}})
	want := [][2]int{{1024, 6999}, {7001, 7399}, {7501, 8442}, {8444, 65535}}
	if len(got) != len(want) {
		t.Fatalf("got %v ranges, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	// Sanity: reserved ports must not be covered by any allowed range.
	covered := func(p int) bool {
		for _, r := range got {
			if p >= r[0] && p <= r[1] {
				return true
			}
		}
		return false
	}
	for _, p := range []int{7000, 8443, 7400, 7450, 7500, 22, 1023} {
		if covered(p) {
			t.Errorf("reserved port %d must not be in allowPorts", p)
		}
	}
	for _, p := range []int{1024, 6999, 7001, 8080, 18443, 65535} {
		if !covered(p) {
			t.Errorf("allowed port %d must be in allowPorts", p)
		}
	}
}

func TestRenderFrpcProxies(t *testing.T) {
	cfg := RenderFrpc(FrpcParams{
		ServerAddr: "1.2.3.4", ServerPort: 7000, Token: "tok\"en", TLSEnable: true,
		AdminAddr: "127.0.0.1", AdminPort: 7401, AdminUser: "admin", AdminPassword: "pw",
		LogFile: "/x/frpc.log",
		Proxies: []Proxy{{Name: "m1_n1", Type: "tcp", LocalPort: 80, RemotePort: 18080}},
	})
	for _, must := range []string{
		`serverAddr = "1.2.3.4"`, `serverPort = 7000`,
		`auth.token = "tok\"en"`, // quote escaping
		`transport.tls.enable = true`, `webServer.port = 7401`,
		`[[proxies]]`, `name = "m1_n1"`, `localPort = 80`, `remotePort = 18080`,
	} {
		if !strings.Contains(cfg, must) {
			t.Errorf("frpc config missing %q\n---\n%s", must, cfg)
		}
	}
}

func TestRenderFrpsAllowPorts(t *testing.T) {
	cfg := RenderFrps(FrpsParams{
		BindPort: 7000, Token: "t", TLSForce: true,
		AdminAddr: "127.0.0.1", AdminPort: 7500, AdminUser: "admin", AdminPassword: "pw",
		LogFile: "/x/frps.log", AllowLo: 1024, AllowHi: 65535,
		Reserved: [][2]int{{7000, 7000}, {8443, 8443}, {7400, 7500}},
	})
	for _, must := range []string{
		`bindPort = 7000`, `transport.tls.force = true`,
		`{ start = 1024, end = 6999 }`, `{ start = 8444, end = 65535 }`,
		`webServer.addr = "127.0.0.1"`,
	} {
		if !strings.Contains(cfg, must) {
			t.Errorf("frps config missing %q\n---\n%s", must, cfg)
		}
	}
}

func TestProxyNameRoundTrip(t *testing.T) {
	name := ProxyName(12, 3)
	if name != "m12_n3" {
		t.Fatalf("ProxyName = %q", name)
	}
	m, n, ok := ParseProxyName(name)
	if !ok || m != 12 || n != 3 {
		t.Fatalf("ParseProxyName(%q) = %d,%d,%v", name, m, n, ok)
	}
	if _, _, ok := ParseProxyName("foreign_proxy"); ok {
		t.Error("foreign proxy name should not parse")
	}
	if _, _, ok := ParseProxyName("m1x_n2"); ok {
		t.Error("malformed name should not parse")
	}
}
