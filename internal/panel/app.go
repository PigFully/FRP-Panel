package panel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frpanel/frpanel/internal/frpcfg"
	"github.com/frpanel/frpanel/internal/protocol"
	"github.com/frpanel/frpanel/internal/version"
)

const (
	defaultConnRateLimit = 200
	defaultPingInterval  = 15 // seconds
)

// App wires together every panel subsystem and owns their lifecycle.
type App struct {
	cfg   *Config
	store *Store
	auth  *Auth
	frpc  *FrpcManager
	hub   *Hub
	pipe  *Pipeline
	nodes *NodeManager
	log   *slog.Logger

	startedAt time.Time
	dbHealthy atomic.Bool

	// panelName is the runtime-mutable display name. It is read by request
	// handlers and the realtime hub while handleUpdateSettings writes it, so it
	// lives in an atomic pointer rather than the plain cfg field (a bare string
	// field read/written across goroutines is a data race that can tear).
	panelName atomic.Pointer[string]

	// restartFn triggers a clean shutdown (main's signal context) so systemd's
	// Restart=always respawns the process — used after a panel self-update.
	restartFn func()

	liveMu sync.RWMutex
	live   map[string]string // "nodeID:remotePort" -> tunnel status

	latMu   sync.RWMutex
	ctrlLat map[int64]int  // nodeID -> RTT ms to agent port (control path)
	linkLat map[string]int // "nodeID:remotePort" -> RTT ms to public tunnel endpoint (FRP link)

	droppedBatches atomic.Int64
}

// New constructs the App and its subsystems.
func New(cfg *Config, store *Store, log *slog.Logger) *App {
	a := &App{
		cfg:       cfg,
		store:     store,
		log:       log,
		startedAt: time.Now(),
		live:      map[string]string{},
		ctrlLat:   map[int64]int{},
		linkLat:   map[string]int{},
	}
	a.dbHealthy.Store(true)
	a.setPanelName(cfg.PanelName)
	a.auth = NewAuth(cfg.JWTSecret, store, cfg.TLS.Enabled, a.DBUp)
	a.frpc = NewFrpcManager(cfg.FrpcBin, cfg.DataDir+"/frpc.d", cfg.JWTSecret, log, a.AddLog)
	a.hub = NewHub(a)
	a.pipe = NewPipeline(a)
	a.nodes = NewNodeManager(a)
	return a
}

// DBUp reports the latest DB health.
func (a *App) DBUp() bool { return a.dbHealthy.Load() }

// PanelName returns the current runtime panel display name.
func (a *App) PanelName() string {
	if p := a.panelName.Load(); p != nil {
		return *p
	}
	return a.cfg.PanelName
}

// setPanelName atomically updates the runtime panel display name.
func (a *App) setPanelName(name string) { a.panelName.Store(&name) }

// OnRestart registers the clean-shutdown trigger used to restart into a newly
// installed binary (wired to main's signal-context stop).
func (a *App) OnRestart(fn func()) { a.restartFn = fn }

// updateSource resolves the distribution base URL and optional ghproxy-style
// mirror prefix for online updates (config wins over the DB setting for the
// base; the mirror lives only in settings).
func (a *App) updateSource(ctx context.Context) (base, mirror string) {
	base = a.cfg.UpdateBaseURL
	if a.DBUp() {
		if base == "" {
			base, _ = a.store.GetSetting(ctx, "update_base_url")
		}
		mirror, _ = a.store.GetSetting(ctx, "update_mirror")
	}
	return strings.TrimRight(strings.TrimSpace(base), "/"), strings.TrimSpace(mirror)
}

// versionString returns the running panel version.
func (a *App) versionString() string { return version.Version }

// setDBHealth records DB up/down transitions and logs them once each.
func (a *App) setDBHealth(up bool) {
	prev := a.dbHealthy.Swap(up)
	if prev == up {
		return
	}
	if up {
		a.log.Warn("mysql recovered")
		a.AddLog("panel_op", "system", nil, "MySQL 已恢复，指标续写")
	} else {
		a.log.Error("mysql unavailable; degrading")
		// Best-effort log may itself fail; that's fine.
		a.AddLog("panel_op", "system", nil, "MySQL 故障，进入降级（实时通道与隧道继续运行）")
	}
}

// AddLog writes an operation log (best effort; never blocks core paths).
func (a *App) AddLog(typ, source string, nodeID *int64, detail string) {
	if !a.DBUp() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := a.store.AddLog(ctx, typ, source, nodeID, detail); err != nil {
		a.log.Warn("add log failed", "err", err)
	}
}

func liveKey(nodeID int64, remotePort int) string {
	return fmt.Sprintf("%d:%d", nodeID, remotePort)
}

// setLive records a tunnel's live status, returning true on a change.
func (a *App) setLive(nodeID int64, remotePort int, status string) bool {
	k := liveKey(nodeID, remotePort)
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	if a.live[k] == status {
		return false
	}
	a.live[k] = status
	return true
}

// getLive returns a tunnel's live status, or "" if unknown.
func (a *App) getLive(nodeID int64, remotePort int) string {
	a.liveMu.RLock()
	defer a.liveMu.RUnlock()
	return a.live[liveKey(nodeID, remotePort)]
}

// clearLiveForNode drops all live statuses of a node (on offline / delete).
func (a *App) clearLiveForNode(nodeID int64) {
	prefix := fmt.Sprintf("%d:", nodeID)
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	for k := range a.live {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(a.live, k)
		}
	}
}

// markNodeOnline updates status+versions and pushes the change to browsers.
func (a *App) markNodeOnline(nodeID int64, agentVer, frpsVer string) {
	if a.DBUp() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_ = a.store.UpdateNodeStatus(ctx, nodeID, "online", agentVer, frpsVer)
		cancel()
	}
	a.hub.Broadcast(wsNodeStatus{Type: "node_status", NodeID: nodeID, Status: "online"})
}

// markNodeOffline flips a node offline, clears its live tunnel statuses and
// pushes the change (browser turns red without a refresh).
func (a *App) markNodeOffline(nodeID int64) {
	if a.DBUp() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_ = a.store.SetNodeOffline(ctx, nodeID)
		cancel()
	}
	a.clearLiveForNode(nodeID)
	a.hub.Broadcast(wsNodeStatus{Type: "node_status", NodeID: nodeID, Status: "offline"})
}

// onTunnelStatusChange persists a tunnel status transition, logs it and pushes.
func (a *App) onTunnelStatusChange(nodeID int64, remotePort int, proto, status string) {
	a.hub.Broadcast(wsTunnelStatus{Type: "tunnel_status", NodeID: nodeID, RemotePort: remotePort, Status: status})
	if a.DBUp() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_ = a.store.UpdateTargetStatusByPort(ctx, nodeID, remotePort, status)
		cancel()
	}
	id := nodeID
	a.AddLog("frp_event", "node", &id, fmt.Sprintf("隧道 %s:%d 状态变更为 %s", proto, remotePort, status))
}

// broadcastEvent pushes an event to browsers (used for node up/down, frp events).
func (a *App) broadcastEvent(kind, detail string, nodeID *int64, atMs int64) {
	a.hub.Broadcast(wsEvent{Type: "event", Kind: kind, Detail: detail, NodeID: nodeID, AtMs: atMs})
}

// LoadRuntimeSettings loads mutable settings (panel name) from the DB into the
// in-memory config at startup.
func (a *App) LoadRuntimeSettings(ctx context.Context) {
	if v, _ := a.store.GetSetting(ctx, "panel_name"); v != "" {
		a.setPanelName(v)
	}
}

// connRateLimit returns the configured max new-connection rate per second per
// managed public port (0 = unlimited), defaulting to 200.
func (a *App) connRateLimit() int {
	if !a.DBUp() {
		return defaultConnRateLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, err := a.store.GetSetting(ctx, "conn_rate_limit")
	if err != nil || v == "" {
		return defaultConnRateLimit
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultConnRateLimit
	}
	return n
}

// pingIntervalSec returns the configured TCP-ping refresh interval (>=5s).
func (a *App) pingIntervalSec() int {
	if a.DBUp() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		v, _ := a.store.GetSetting(ctx, "tcp_ping_interval")
		cancel()
		if n, err := strconv.Atoi(v); err == nil && n >= 5 {
			return n
		}
	}
	return defaultPingInterval
}

func (a *App) setCtrlLat(id int64, ms int) {
	a.latMu.Lock()
	a.ctrlLat[id] = ms
	a.latMu.Unlock()
}

func (a *App) setLinkLat(node int64, port, ms int) {
	a.latMu.Lock()
	a.linkLat[liveKey(node, port)] = ms
	a.latMu.Unlock()
}

// clearLatForNode drops a node's cached latencies (on delete), so the linkLat
// map cannot accumulate entries for nodes/ports that no longer exist.
func (a *App) clearLatForNode(nodeID int64) {
	prefix := strconv.FormatInt(nodeID, 10) + ":"
	a.latMu.Lock()
	delete(a.ctrlLat, nodeID)
	for k := range a.linkLat {
		if strings.HasPrefix(k, prefix) {
			delete(a.linkLat, k)
		}
	}
	a.latMu.Unlock()
}

// LinkLatency returns the FRP-link RTT to a node's public tunnel endpoint
// (0 unknown, -1 timeout).
func (a *App) LinkLatency(node int64, port int) int {
	a.latMu.RLock()
	defer a.latMu.RUnlock()
	return a.linkLat[liveKey(node, port)]
}

// NodeLatency returns a representative latency for a node: the best (lowest)
// FRP-link latency across its enabled tunnels, falling back to the control-path
// RTT when the node has no tunnels.
func (a *App) NodeLatency(node int64) int {
	a.latMu.RLock()
	defer a.latMu.RUnlock()
	prefix := strconv.FormatInt(node, 10) + ":"
	best := 0
	for k, v := range a.linkLat {
		if strings.HasPrefix(k, prefix) && v > 0 && (best == 0 || v < best) {
			best = v
		}
	}
	if best > 0 {
		return best
	}
	return a.ctrlLat[node]
}

// tcpPingLoop periodically measures both the control-path RTT (to the agent
// port) and the real FRP-link RTT (to each enabled tunnel's public port).
func (a *App) tcpPingLoop(ctx context.Context) {
	for {
		for _, na := range a.nodes.Snapshots() {
			go a.probe(net.JoinHostPort(na.IP, strconv.Itoa(na.Port)), func(ms int) { a.setCtrlLat(na.ID, ms) })
		}
		if a.DBUp() {
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			targets, err := a.store.EnabledTargetsForProbe(pctx)
			cancel()
			if err == nil {
				for _, t := range targets {
					t := t
					go a.probeLink(net.JoinHostPort(t.IP, strconv.Itoa(t.RemotePort)), func(ms int) { a.setLinkLat(t.NodeID, t.RemotePort, ms) })
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(a.pingIntervalSec()) * time.Second):
		}
	}
}

// probe TCP-dials addr and reports the connect RTT in ms (-1 on failure).
// Used for the control path (agent port).
func (a *App) probe(addr string, set func(ms int)) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		set(-1)
		return
	}
	_ = c.Close()
	set(int(time.Since(start).Milliseconds()))
}

// probeLink measures the REAL end-to-end FRP link latency to a public tunnel
// endpoint: it connects to node_ip:remote_port (frps), sends a minimal HTTP
// request that traverses frps→frpc→local service, and times the first response
// byte coming back. That whole round trip is the true link latency. If the
// local service does not answer (non-HTTP), it falls back to the connect RTT.
func (a *App) probeLink(addr string, set func(ms int)) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", addr, 4*time.Second)
	if err != nil {
		set(-1)
		return
	}
	defer c.Close()
	connectMs := int(time.Since(start).Milliseconds())
	_ = c.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := c.Write([]byte("HEAD / HTTP/1.0\r\nHost: frpanel-probe\r\nUser-Agent: frpanel\r\nConnection: close\r\n\r\n")); err != nil {
		set(connectMs)
		return
	}
	buf := make([]byte, 1)
	if _, err := c.Read(buf); err != nil {
		// Tunnel is up but the service didn't answer within the window; report
		// the connect latency rather than the wasted read-deadline duration.
		set(connectMs)
		return
	}
	set(int(time.Since(start).Milliseconds())) // connect + full app round trip
}

// desiredProxies builds the frpc proxy list and managed port set for a node
// from its enabled mappings (single JOIN, no N+1).
func (a *App) desiredProxies(ctx context.Context, nodeID int64) ([]frpcfg.Proxy, []protocol.PortSpec, error) {
	rows, err := a.store.EnabledProxiesForNode(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}
	var proxies []frpcfg.Proxy
	var ports []protocol.PortSpec
	for _, r := range rows {
		proxies = append(proxies, frpcfg.Proxy{
			Name: frpcfg.ProxyName(r.MappingID, r.NodeID), Type: r.Proto,
			LocalIP: "127.0.0.1", LocalPort: r.LocalPort, RemotePort: r.RemotePort, Comment: r.Remark,
		})
		ports = append(ports, protocol.PortSpec{Port: r.RemotePort, Proto: r.Proto})
	}
	return proxies, ports, nil
}

// pingDBLoop periodically checks MySQL and flips the health flag.
func (a *App) pingDBLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := a.store.DB().PingContext(c)
			cancel()
			a.setDBHealth(err == nil)
		}
	}
}
