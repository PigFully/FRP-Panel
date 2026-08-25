package panel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/frpanel/frpanel/internal/frpcfg"
	"github.com/frpanel/frpanel/internal/portutil"
)

// FrpcManager supervises one frpc child process per node. Proxy changes are
// hot-reloaded via the frpc admin API; the process is only restarted for
// server-address/token/TLS changes (spec §2.1). Crashed processes are respawned
// with exponential backoff and throttled logging (spec §6.2).
type FrpcManager struct {
	bin    string
	dir    string
	secret string
	log    *slog.Logger

	mu         sync.Mutex
	procs      map[int64]*frpcProc
	adminPorts map[int64]int
	addLog     func(typ, source string, nodeID *int64, detail string)
}

type frpcProc struct {
	nodeID    int64
	adminPort int
	cfgPath   string
	cancel    context.CancelFunc
	done      chan struct{}
	// throttle state
	thMu       sync.Mutex
	lastKey    string
	lastEmit   time.Time
	suppressed int
}

// NewFrpcManager creates a manager. secret derives per-node admin passwords.
func NewFrpcManager(bin, dir, secret string, log *slog.Logger, addLog func(string, string, *int64, string)) *FrpcManager {
	_ = os.MkdirAll(dir, 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	return &FrpcManager{
		bin: bin, dir: dir, secret: secret, log: log,
		procs: map[int64]*frpcProc{}, adminPorts: map[int64]int{}, addLog: addLog,
	}
}

// adminPass derives a stable per-node frpc admin password from the panel secret.
func (m *FrpcManager) adminPass(nodeID int64) string {
	h := hmac.New(sha256.New, []byte(m.secret))
	fmt.Fprintf(h, "frpc-admin:%d", nodeID)
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// AdminPort returns (allocating if needed) the loopback admin port for a node.
// Base is 7400+id, linear-probed within [7400,7500] to avoid collisions.
func (m *FrpcManager) AdminPort(nodeID int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.adminPorts[nodeID]; ok {
		return p
	}
	used := map[int]bool{}
	for _, p := range m.adminPorts {
		used[p] = true
	}
	base := portutil.AdminLow + int(nodeID)
	if base > portutil.AdminHigh {
		base = portutil.AdminLow
	}
	for i := 0; i <= (portutil.AdminHigh - portutil.AdminLow); i++ {
		p := portutil.AdminLow + (base-portutil.AdminLow+i)%(portutil.AdminHigh-portutil.AdminLow+1)
		if used[p] {
			continue
		}
		if listening, _ := portutil.LocalListen(p, "tcp"); listening {
			continue
		}
		m.adminPorts[nodeID] = p
		if p != portutil.AdminLow+int(nodeID) {
			m.log.Warn("frpc admin port collision; shifted", "node", nodeID, "port", p)
		}
		return p
	}
	m.adminPorts[nodeID] = base
	return base
}

// EnsureNode renders the frpc config for a node and makes sure the process is
// running with it. If already running, it hot-reloads instead of restarting.
func (m *FrpcManager) EnsureNode(node *Node, proxies []frpcfg.Proxy) error {
	adminPort := m.AdminPort(node.ID)
	cfg := frpcfg.RenderFrpc(frpcfg.FrpcParams{
		ServerAddr:    node.IP,
		ServerPort:    node.FrpsPort,
		Token:         node.FrpsToken,
		TLSEnable:     true,
		AdminAddr:     "127.0.0.1",
		AdminPort:     adminPort,
		AdminUser:     "admin",
		AdminPassword: m.adminPass(node.ID),
		LogFile:       filepath.Join(m.dir, "logs", fmt.Sprintf("frpc-%d.log", node.ID)),
		Proxies:       proxies,
	})
	cfgPath := filepath.Join(m.dir, fmt.Sprintf("node-%d.toml", node.ID))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return err
	}

	m.mu.Lock()
	_, running := m.procs[node.ID]
	m.mu.Unlock()
	if running {
		// Hot reload; if it fails (e.g. frpc still starting), the supervisor
		// will pick up the new config on its next (re)start anyway.
		if err := m.reload(node.ID, adminPort); err != nil {
			m.log.Warn("frpc reload failed; will apply on next cycle", "node", node.ID, "err", err)
		}
		return nil
	}
	m.startProc(node.ID, adminPort, cfgPath)
	return nil
}

// RestartNode forces a full process restart (server addr/token/TLS change).
func (m *FrpcManager) RestartNode(node *Node, proxies []frpcfg.Proxy) error {
	m.StopNode(node.ID)
	return m.EnsureNode(node, proxies)
}

func (m *FrpcManager) startProc(nodeID int64, adminPort int, cfgPath string) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &frpcProc{nodeID: nodeID, adminPort: adminPort, cfgPath: cfgPath, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.procs[nodeID] = p
	m.mu.Unlock()
	go m.supervise(ctx, p)
}

func (m *FrpcManager) supervise(ctx context.Context, p *frpcProc) {
	defer close(p.done)
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		start := time.Now()
		cmd := exec.CommandContext(ctx, m.bin, "-c", p.cfgPath)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		err := cmd.Start()
		if err != nil {
			m.throttled(p, "start:"+err.Error(), fmt.Sprintf("frpc 启动失败: %v", err))
		} else {
			werr := cmd.Wait()
			select {
			case <-ctx.Done():
				return
			default:
			}
			if time.Since(start) > 30*time.Second {
				backoff = time.Second // ran healthily; reset backoff
			}
			m.throttled(p, "exit", fmt.Sprintf("frpc 进程退出(%v)，%.0fs 后重启", werr, backoff.Seconds()))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
	}
}

// throttled emits at most one log per (node,key) per 5 minutes, folding a
// suppressed count into the next emitted line to avoid a disk log storm.
func (m *FrpcManager) throttled(p *frpcProc, key, msg string) {
	p.thMu.Lock()
	now := time.Now()
	if key == p.lastKey && now.Sub(p.lastEmit) < 5*time.Minute {
		p.suppressed++
		p.thMu.Unlock()
		return
	}
	sup := p.suppressed
	p.suppressed = 0
	p.lastKey = key
	p.lastEmit = now
	p.thMu.Unlock()

	if sup > 0 {
		msg = fmt.Sprintf("%s（期间抑制了 %d 条相同日志）", msg, sup)
	}
	m.log.Warn("frpc supervisor", "node", p.nodeID, "msg", msg)
	if m.addLog != nil {
		id := p.nodeID
		m.addLog("frp_event", "frpc-supervisor", &id, msg)
	}
}

// StopNode stops and forgets a node's frpc process.
func (m *FrpcManager) StopNode(nodeID int64) {
	m.mu.Lock()
	p, ok := m.procs[nodeID]
	if ok {
		delete(m.procs, nodeID)
	}
	m.mu.Unlock()
	if ok {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(6 * time.Second):
		}
	}
	_ = os.Remove(filepath.Join(m.dir, fmt.Sprintf("node-%d.toml", nodeID)))
}

// StopAll stops every supervised process (graceful shutdown).
func (m *FrpcManager) StopAll() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.StopNode(id)
	}
}

func (m *FrpcManager) reload(nodeID int64, adminPort int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/reload", adminPort)
	req, _ := http.NewRequest("GET", url, nil)
	req.SetBasicAuth("admin", m.adminPass(nodeID))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("frpc reload http %d", resp.StatusCode)
	}
	return nil
}

// ProxyStatus is one proxy's runtime status from the frpc admin API.
type ProxyStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Err    string `json:"err"`
	RemoteAddr string `json:"remote_addr"`
}

// Status fetches proxy statuses from a node's frpc admin API.
func (m *FrpcManager) Status(nodeID int64) (map[string]ProxyStatus, error) {
	m.mu.Lock()
	adminPort := m.adminPorts[nodeID]
	m.mu.Unlock()
	if adminPort == 0 {
		return nil, fmt.Errorf("frpc for node %d not running", nodeID)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/status", adminPort)
	req, _ := http.NewRequest("GET", url, nil)
	req.SetBasicAuth("admin", m.adminPass(nodeID))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return parseFrpcStatus(body)
}

func parseFrpcStatus(body []byte) (map[string]ProxyStatus, error) {
	// frpc /api/status returns {"tcp":[...],"udp":[...],"http":[...],...}
	var raw map[string][]ProxyStatus
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := map[string]ProxyStatus{}
	for _, list := range raw {
		for _, ps := range list {
			out[ps.Name] = ps
		}
	}
	return out, nil
}

// Running reports whether a node's frpc process is currently supervised.
func (m *FrpcManager) Running(nodeID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.procs[nodeID]
	return ok
}
