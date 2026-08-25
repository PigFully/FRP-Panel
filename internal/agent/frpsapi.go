package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/frpanel/frpanel/internal/protocol"
)

// FrpsClient talks to the frps admin/dashboard API on loopback.
type FrpsClient struct {
	base string
	user string
	pass string
	hc   *http.Client
}

// NewFrpsClient builds a client for 127.0.0.1:adminPort.
func NewFrpsClient(cfg *Config) *FrpsClient {
	return &FrpsClient{
		base: fmt.Sprintf("http://%s:%d", cfg.FrpsAdminAddr, cfg.FrpsAdminPort),
		user: cfg.FrpsAdminUser,
		pass: cfg.FrpsAdminPass,
		hc:   &http.Client{Timeout: 4 * time.Second},
	}
}

func (c *FrpsClient) get(path string, out any) error {
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("frps admin %s: http %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type serverInfo struct {
	Version  string `json:"version"`
	BindPort int    `json:"bindPort"`
}

// ServerInfo returns the frps version and whether the admin API is reachable.
func (c *FrpsClient) ServerInfo() (version string, up bool) {
	var si serverInfo
	if err := c.get("/api/serverinfo", &si); err != nil {
		return "", false
	}
	return si.Version, true
}

// frps /api/proxy/<type> response.
type proxyResp struct {
	Proxies []struct {
		Name string `json:"name"`
		Conf *struct {
			RemotePort int `json:"remotePort"`
		} `json:"conf"`
		TodayTrafficIn  int64  `json:"todayTrafficIn"`
		TodayTrafficOut int64  `json:"todayTrafficOut"`
		Status          string `json:"status"`
	} `json:"proxies"`
}

// Proxies returns the proxies frps currently knows about (tcp + udp), including
// per-proxy today traffic and status. This is the reconciliation source of
// truth on the node side, and the per-proxy traffic (tunnel scope) source.
func (c *FrpsClient) Proxies() ([]protocol.ProxyInfo, error) {
	var out []protocol.ProxyInfo
	for _, proto := range []string{"tcp", "udp"} {
		var pr proxyResp
		if err := c.get("/api/proxy/"+proto, &pr); err != nil {
			return out, err
		}
		for _, p := range pr.Proxies {
			rp := 0
			if p.Conf != nil {
				rp = p.Conf.RemotePort
			}
			out = append(out, protocol.ProxyInfo{
				Name:       p.Name,
				Proto:      proto,
				RemotePort: rp,
				Status:     p.Status,
				TodayIn:    p.TodayTrafficIn,
				TodayOut:   p.TodayTrafficOut,
			})
		}
	}
	return out, nil
}
