package panel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/frpanel/frpanel/internal/protocol"
)

// NodeManager owns every node's outbound control connection and coordinates the
// data-plane frpc lifecycle. The data plane (frpc<->frps) runs independently of
// the control link, so tunnels survive a panel<->agent outage.
type NodeManager struct {
	app     *App
	ctx     context.Context
	mu      sync.Mutex
	conns   map[int64]*NodeConn
	cancels map[int64]context.CancelFunc
}

// NewNodeManager builds an empty manager.
func NewNodeManager(app *App) *NodeManager {
	return &NodeManager{app: app, conns: map[int64]*NodeConn{}, cancels: map[int64]context.CancelFunc{}}
}

// StartAll loads all nodes and starts their connections + data-plane frpc.
//
// If the node list cannot be loaded at startup (DB unreachable — e.g. the panel
// booted before the local MySQL was ready), it does NOT give up: it retries in
// the background until the DB answers, then starts every node. Without this a
// degraded start left the panel running with zero control links and zero frpc
// forever — every tunnel down until someone manually restarted the panel — even
// though the rest of the panel recovered its DB connection on its own.
func (nm *NodeManager) StartAll(ctx context.Context) {
	nm.ctx = ctx
	if nm.tryStartAll(ctx) {
		return
	}
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if nm.tryStartAll(ctx) {
					nm.app.log.Info("nodes started after DB became reachable")
					return
				}
			}
		}
	}()
}

// tryStartAll loads the node list and starts any node not already running,
// reporting whether the load succeeded. start() is idempotent per node, so a
// node added via Add() during the retry window is not started twice.
func (nm *NodeManager) tryStartAll(ctx context.Context) bool {
	nodes, err := nm.app.store.ListNodes(ctx)
	if err != nil {
		nm.app.log.Error("list nodes on startup (will retry until DB is reachable)", "err", err)
		return false
	}
	for i := range nodes {
		nm.start(nodes[i])
	}
	return true
}

func (nm *NodeManager) start(node Node) {
	nm.mu.Lock()
	if _, ok := nm.conns[node.ID]; ok {
		nm.mu.Unlock()
		return
	}
	cctx, cancel := context.WithCancel(nm.ctx)
	c := newNodeConn(nm.app, node)
	nm.conns[node.ID] = c
	nm.cancels[node.ID] = cancel
	nm.mu.Unlock()

	nm.app.pipe.InitNode(node.ID, node.LastCommitSeq)
	// If we already know the frps token (panel restart), bring the data plane
	// up immediately without waiting for the control handshake.
	if node.FrpsToken != "" {
		if proxies, _, err := nm.app.desiredProxies(nm.ctx, node.ID); err == nil {
			_ = nm.app.frpc.EnsureNode(&node, proxies)
		}
	}
	go c.Run(cctx)
}

// Add starts (or restarts) a node's connection after it is created.
func (nm *NodeManager) Add(ctx context.Context, node Node) {
	nm.stop(node.ID)
	if nm.ctx == nil {
		nm.ctx = ctx
	}
	nm.start(node)
}

// Restart reloads a node from the DB and restarts its connection (after edit).
func (nm *NodeManager) Restart(ctx context.Context, nodeID int64) {
	node, err := nm.app.store.GetNode(ctx, nodeID)
	if err != nil || node == nil {
		return
	}
	nm.stop(nodeID)
	nm.start(*node)
}

func (nm *NodeManager) stop(nodeID int64) {
	nm.mu.Lock()
	cancel := nm.cancels[nodeID]
	delete(nm.conns, nodeID)
	delete(nm.cancels, nodeID)
	nm.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Remove tears a node down completely (connection + frpc + pipeline state).
func (nm *NodeManager) Remove(nodeID int64) {
	nm.stop(nodeID)
	nm.app.frpc.StopNode(nodeID)
	nm.app.pipe.RemoveNode(nodeID)
	nm.app.clearLiveForNode(nodeID)
	nm.app.clearLatForNode(nodeID)
}

// NodeAddr is a node's dial target for latency probing.
type NodeAddr struct {
	ID   int64
	IP   string
	Port int
}

// Snapshots returns dial targets for all managed nodes.
func (nm *NodeManager) Snapshots() []NodeAddr {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	out := make([]NodeAddr, 0, len(nm.conns))
	for id, c := range nm.conns {
		n := c.snapshot()
		out = append(out, NodeAddr{ID: id, IP: n.IP, Port: n.AgentPort})
	}
	return out
}

// Conn returns a node's connection driver, or nil if unknown.
func (nm *NodeManager) Conn(nodeID int64) *NodeConn {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	return nm.conns[nodeID]
}

// PortCheck asks a node's agent whether a public port is free.
func (nm *NodeManager) PortCheck(ctx context.Context, nodeID int64, port int, proto string) (protocol.PortCheckRes, error) {
	c := nm.Conn(nodeID)
	if c == nil || !c.Connected() {
		return protocol.PortCheckRes{}, Err(CodeNodeOffline, "节点当前离线，无法查询端口占用")
	}
	env, err := c.request(ctx, protocol.TypePortCheck, protocol.PortCheck{Port: port, Proto: proto})
	if err != nil {
		return protocol.PortCheckRes{}, err
	}
	var res protocol.PortCheckRes
	_ = env.Decode(&res)
	return res, nil
}

// SyncNode recomputes and applies a node's frpc config (proxies from enabled
// mappings) and pushes the origin rate limit. Safe whether or not the agent
// control link is up — the frpc data plane is applied regardless.
func (nm *NodeManager) SyncNode(ctx context.Context, nodeID int64) error {
	node, err := nm.app.store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return Err(CodeNotFound, "节点不存在")
	}
	proxies, ports, err := nm.app.desiredProxies(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := nm.app.frpc.EnsureNode(node, proxies); err != nil {
		return err
	}
	if c := nm.Conn(nodeID); c != nil && c.Connected() {
		c.pushRateLimit(ctx, ports)
	}
	return nil
}

// RotateFrpsToken rotates a node's frps auth token (restart path, §2.1): the
// agent restarts frps with the new token, then the panel rewrites+restarts the
// node's frpc. Tunnels briefly drop and auto-recover.
func (nm *NodeManager) RotateFrpsToken(ctx context.Context, nodeID int64) error {
	c := nm.Conn(nodeID)
	if c == nil || !c.Connected() {
		return Err(CodeNodeOffline, "节点当前离线，无法轮换 token")
	}
	node := c.snapshot()
	newTok := RandomToken(32)
	env, err := c.request(ctx, protocol.TypeRestartFrps, protocol.RestartFrps{NewFrpsToken: newTok, Reason: "面板触发 frps token 轮换"})
	if err != nil {
		return err
	}
	var res protocol.RestartRes
	_ = env.Decode(&res)
	if !res.OK {
		return Err(CodeInternal, "节点 frps 重启失败: "+res.Message)
	}
	node.FrpsToken = newTok
	if err := nm.app.store.UpdateNodeFrps(ctx, nodeID, newTok, node.FrpsPort); err != nil {
		return err
	}
	c.setNode(node)
	proxies, ports, err := nm.app.desiredProxies(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := nm.app.frpc.RestartNode(&node, proxies); err != nil {
		return err
	}
	c.pushRateLimit(ctx, ports)
	id := nodeID
	nm.app.AddLog("panel_op", "admin", &id, fmt.Sprintf("轮换节点 frps token（隧道短暂中断后自动恢复）"))
	return nil
}

// UpdateAgent instructs a node's agent to self-update from the distribution
// base (integrity-checked against its sha256sums.txt). The agent acknowledges
// as soon as the download starts; the swap and restart happen node-side, after
// which the control link reconnects and hello_ack reports the new version.
// Tunnels are unaffected (frps and frpc keep running through the agent swap).
func (nm *NodeManager) UpdateAgent(ctx context.Context, nodeID int64, version, base, mirror string) error {
	c := nm.Conn(nodeID)
	if c == nil || !c.Connected() {
		return Err(CodeNodeOffline, "节点当前离线，无法在线升级")
	}
	if c.Proto() < 2 {
		return Err(CodeValidation, "该节点 Agent 版本过旧，不支持在线升级；请在节点上重跑安装脚本升级一次")
	}
	env, err := c.request(ctx, protocol.TypeUpdateAgent, protocol.UpdateAgent{Version: version, BaseURL: base, Mirror: mirror})
	if err != nil {
		return err
	}
	var res protocol.UpdateRes
	_ = env.Decode(&res)
	if !res.OK {
		return Err(CodeInternal, "节点拒绝升级: "+res.Message)
	}
	return nil
}

// SyncAll re-applies frpc config for every node (used after a mapping change;
// at ≤10 nodes this is cheap and guarantees convergence of add/remove).
func (nm *NodeManager) SyncAll(ctx context.Context) {
	nm.mu.Lock()
	ids := make([]int64, 0, len(nm.conns))
	for id := range nm.conns {
		ids = append(ids, id)
	}
	nm.mu.Unlock()
	for _, id := range ids {
		if err := nm.SyncNode(ctx, id); err != nil {
			nm.app.log.Warn("sync node", "node", id, "err", err)
		}
	}
}

// PushRateLimitAll re-pushes the rate limit to every connected node (after the
// global setting changes).
func (nm *NodeManager) PushRateLimitAll(ctx context.Context) {
	nm.mu.Lock()
	conns := make([]*NodeConn, 0, len(nm.conns))
	for _, c := range nm.conns {
		conns = append(conns, c)
	}
	nm.mu.Unlock()
	for _, c := range conns {
		if !c.Connected() {
			continue
		}
		if _, ports, err := nm.app.desiredProxies(ctx, c.nodeID); err == nil {
			c.pushRateLimit(ctx, ports)
		}
	}
}

// UpdateMeta updates a live connection's name/region snapshot without dropping
// the link (used for cosmetic node edits).
func (nm *NodeManager) UpdateMeta(nodeID int64, name, region string) {
	c := nm.Conn(nodeID)
	if c == nil {
		return
	}
	n := c.snapshot()
	n.Name = name
	n.Region = region
	c.setNode(n)
}

// StopAll cancels every connection and stops every frpc (graceful shutdown).
func (nm *NodeManager) StopAll() {
	nm.mu.Lock()
	ids := make([]int64, 0, len(nm.cancels))
	for id := range nm.cancels {
		ids = append(ids, id)
	}
	nm.mu.Unlock()
	for _, id := range ids {
		nm.stop(id)
	}
	nm.app.frpc.StopAll()
}
