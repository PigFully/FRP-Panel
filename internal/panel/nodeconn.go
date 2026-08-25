package panel

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frpanel/frpanel/internal/certutil"
	"github.com/frpanel/frpanel/internal/frpcfg"
	"github.com/frpanel/frpanel/internal/protocol"
	"github.com/frpanel/frpanel/internal/version"
	"github.com/gorilla/websocket"
)

// readEnvelope reads one WS message and decodes the protocol envelope.
func readEnvelope(conn *websocket.Conn) (protocol.Envelope, error) {
	var env protocol.Envelope
	_, data, err := conn.ReadMessage()
	if err != nil {
		return env, err
	}
	err = json.Unmarshal(data, &env)
	return env, err
}

const (
	panelHeartbeat   = 5 * time.Second
	panelReadTimeout = 16 * time.Second // ~3 missed 5s samples -> offline within ~15s
	requestTimeout   = 8 * time.Second
)

// NodeConn maintains the panel's outbound control connection to one agent and
// drives reconnection, reconciliation and request/response correlation.
type NodeConn struct {
	app    *App
	nodeID int64

	mu   sync.RWMutex
	node Node

	writeMu sync.Mutex
	// conn is replaced on every reconnect by Run's session loop while request()
	// may be reading it from an HTTP handler goroutine, so it is accessed
	// atomically. A request that loses that race writes to the superseded
	// connection and fails cleanly with "与节点通信失败" rather than tearing.
	conn    atomic.Pointer[websocket.Conn]
	up      atomic.Bool
	pending sync.Map // requestID -> chan protocol.Envelope
	reqSeq  atomic.Uint64
	// agentProto is the protocol version the agent reported in hello_ack; it
	// gates commands old agents cannot understand (e.g. update_agent needs >=2).
	agentProto atomic.Int32
}

// Proto returns the connected agent's protocol version (0 if never connected).
func (c *NodeConn) Proto() int { return int(c.agentProto.Load()) }

// newNodeConn creates a driver from a node snapshot.
func newNodeConn(app *App, node Node) *NodeConn {
	c := &NodeConn{app: app, nodeID: node.ID}
	c.node = node
	return c
}

func (c *NodeConn) snapshot() Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.node
}

func (c *NodeConn) setNode(n Node) {
	c.mu.Lock()
	c.node = n
	c.mu.Unlock()
}

// Connected reports whether the control link is currently up.
func (c *NodeConn) Connected() bool { return c.up.Load() }

// Run drives connect/serve/reconnect until ctx is cancelled. Offline is logged
// only on an online->offline transition (avoids log storms while a dead node is
// retried), and the browser is pushed the red status immediately.
func (c *NodeConn) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		_ = c.session(ctx)
		wasOnline := c.up.Swap(false)
		if wasOnline {
			c.app.markNodeOffline(c.nodeID)
			id := c.nodeID
			c.app.AddLog("frp_event", c.snapshot().Name, &id, "节点离线")
			c.app.broadcastEvent("node_offline", "节点离线", &id, time.Now().UnixMilli())
		}
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 60*time.Second {
			backoff = time.Second // healthy session: reset backoff
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

// pinnedTLSConfig verifies the peer by SHA-256 fingerprint (pinning) instead of
// a CA chain, so the agent's self-signed cert is accepted iff it matches.
func pinnedTLSConfig(fingerprint string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no server certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := "sha256:" + hex.EncodeToString(sum[:])
			if !certutil.EqualFingerprint(got, fingerprint) {
				return fmt.Errorf("FPMISMATCH: 期望 %s 实际 %s", fingerprint, got)
			}
			return nil
		},
	}
}

func (c *NodeConn) pinnedTLS(fingerprint string) *tls.Config { return pinnedTLSConfig(fingerprint) }

// verifyAgent performs a one-shot handshake to validate reachability, the
// pinned certificate and the token when a node is being added. It returns a
// specific Chinese error for each failure mode.
func verifyAgent(ctx context.Context, ip string, port int, token, fp string) error {
	u := url.URL{Scheme: "wss", Host: ip + ":" + strconv.Itoa(port), Path: "/agent/ws"}
	dialer := websocket.Dialer{TLSClientConfig: pinnedTLSConfig(fp), HandshakeTimeout: 8 * time.Second}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := dialer.DialContext(dctx, u.String(), nil)
	cancel()
	if err != nil {
		if strings.Contains(err.Error(), "FPMISMATCH") {
			return Err(CodeBadRequest, "证书指纹不符，请确认回执与目标服务器一致")
		}
		return Err(CodeNodeOffline, "连接超时或被拒绝：请检查节点公网 IP、8443 端口与防火墙")
	}
	defer conn.Close()
	hello := protocol.Hello{PanelVersion: version.Version, MinAgentProto: version.MinAgentProtocol}
	env, _ := protocol.Marshal(version.ProtocolVersion, protocol.TypeHello, "verify", hello)
	env.Sign(token)
	b, _ := json.Marshal(env)
	_ = conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		return Err(CodeNodeOffline, "节点写入失败")
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	ack, err := readEnvelope(conn)
	if err != nil {
		return Err(CodeNodeOffline, "节点无响应或 Token 错误")
	}
	if verr := ack.Verify(token, time.Now().Unix()); verr != nil {
		if verr == protocol.ErrBadSig {
			return Err(CodeBadRequest, "Token 错误：回执中的 Token 与节点不匹配")
		}
		return Err(CodeBadRequest, "节点时间与面板相差过大（防重放），请检查节点时钟同步")
	}
	return nil
}

func (c *NodeConn) session(ctx context.Context) error {
	node := c.snapshot()
	u := url.URL{Scheme: "wss", Host: node.IP + ":" + strconv.Itoa(node.AgentPort), Path: "/agent/ws"}
	dialer := websocket.Dialer{
		TLSClientConfig:  c.pinnedTLS(node.Fingerprint),
		HandshakeTimeout: 10 * time.Second,
	}
	dctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	conn, _, err := dialer.DialContext(dctx, u.String(), nil)
	cancel()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	c.conn.Store(conn)
	// Leave no dangling pointer to a closed connection once this session ends
	// (only if a newer session has not already taken over).
	defer c.conn.CompareAndSwap(conn, nil)

	// Handshake: send Hello, expect HelloAck.
	lastSeq := node.LastCommitSeq
	if s := c.app.pipe.CommitSeq(c.nodeID); s > lastSeq {
		lastSeq = s
	}
	hello := protocol.Hello{PanelVersion: version.Version, MinAgentProto: version.MinAgentProtocol, LastCommitSeq: lastSeq}
	if err := c.write(conn, node.AgentToken, protocol.TypeHello, c.newID(), hello); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(12 * time.Second))
	ackEnv, err := readEnvelope(conn)
	if err != nil {
		return fmt.Errorf("read hello_ack: %w", err)
	}
	if verr := ackEnv.Verify(node.AgentToken, time.Now().Unix()); verr != nil {
		return fmt.Errorf("hello_ack verify: %w", verr)
	}
	var ack protocol.HelloAck
	_ = ackEnv.Decode(&ack)
	c.agentProto.Store(int32(ack.Proto))

	// Learn frps token/port over the secure channel; reconfigure if changed.
	changed := false
	if ack.FrpsToken != "" && ack.FrpsToken != node.FrpsToken {
		node.FrpsToken = ack.FrpsToken
		changed = true
	}
	if ack.FrpsPort != 0 && ack.FrpsPort != node.FrpsPort {
		node.FrpsPort = ack.FrpsPort
		changed = true
	}
	if changed {
		if c.app.DBUp() {
			_ = c.app.store.UpdateNodeFrps(ctx, c.nodeID, node.FrpsToken, node.FrpsPort)
		}
		// frps server-address/token is a connection-level parameter: a hot
		// reload cannot re-auth with a new token, so force a fresh frpc start
		// (reconcile's EnsureNode will start it with the new config).
		c.app.frpc.StopNode(c.nodeID)
	}
	c.setNode(node)

	c.up.Store(true)
	c.app.pipe.InitNode(c.nodeID, lastSeq)
	c.app.markNodeOnline(c.nodeID, ack.AgentVersion, ack.FrpsVersion)
	id := c.nodeID
	if !ack.Compatible {
		c.app.AddLog("frp_event", node.Name, &id, "Agent 协议版本过旧，请升级 Agent")
	}
	c.app.AddLog("frp_event", node.Name, &id, "节点上线")
	c.app.broadcastEvent("node_online", "节点上线", &id, time.Now().UnixMilli())

	// Heartbeat and reconciliation run concurrently with the reader loop below,
	// so request/reply commands they issue (list_proxies, set_ratelimit) can be
	// answered — the reader must be live to deliver replies.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go c.heartbeat(hbCtx, conn, node.AgentToken)
	go c.reconcile(hbCtx)

	// Reader loop.
	for {
		_ = conn.SetReadDeadline(time.Now().Add(panelReadTimeout))
		env, err := readEnvelope(conn)
		if err != nil {
			return err
		}
		if verr := env.Verify(node.AgentToken, time.Now().Unix()); verr != nil {
			if verr == protocol.ErrBadSig {
				return fmt.Errorf("bad signature from agent")
			}
			continue // replay-window skew: drop
		}
		c.handle(env)
	}
}

// heartbeat pings over the connection it was started with. Taking conn as a
// parameter (rather than re-reading the field) keeps it pinned to its own
// session, so a reconnect cannot make this goroutine write to a newer link.
func (c *NodeConn) heartbeat(ctx context.Context, conn *websocket.Conn, token string) {
	t := time.NewTicker(panelHeartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.write(conn, token, protocol.TypePing, c.newID(), nil); err != nil {
				return
			}
		}
	}
}

func (c *NodeConn) handle(env protocol.Envelope) {
	switch env.Type {
	case protocol.TypePong:
		// liveness handled by read deadline
	case protocol.TypeMetrics:
		var m protocol.Metrics
		if env.Decode(&m) == nil {
			if sample, live := c.app.pipe.Account(c.nodeID, m); live {
				c.app.hub.Broadcast(wsMetric{Type: "metric", NodeID: c.nodeID, Point: wsPoint{
					TS: sample.AtUnixMs, CPU: sample.CPU, Mem: sample.Mem,
					RxBps: sample.NetRxBps, TxBps: sample.NetTxBps,
					TunInBps: sample.TunInBps, TunOutBps: sample.TunOutBps,
				}})
			}
		}
	case protocol.TypeEvent:
		var e protocol.Event
		if env.Decode(&e) == nil {
			id := c.nodeID
			c.app.AddLog("frp_event", c.snapshot().Name, &id, e.Detail)
			c.app.broadcastEvent(e.Kind, e.Detail, &id, e.AtMs)
		}
	default:
		// Reply to a pending request?
		if env.ID != "" {
			if ch, ok := c.pending.Load(env.ID); ok {
				select {
				case ch.(chan protocol.Envelope) <- env:
				default:
				}
			}
		}
	}
}

// request sends a command and waits for the correlated reply.
func (c *NodeConn) request(ctx context.Context, typ string, payload any) (protocol.Envelope, error) {
	conn := c.conn.Load()
	if !c.up.Load() || conn == nil {
		return protocol.Envelope{}, Err(CodeNodeOffline, "节点当前离线，无法执行该操作")
	}
	id := c.newID()
	ch := make(chan protocol.Envelope, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)
	if err := c.write(conn, c.snapshot().AgentToken, typ, id, payload); err != nil {
		return protocol.Envelope{}, Err(CodeNodeOffline, "与节点通信失败")
	}
	cctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	select {
	case <-cctx.Done():
		return protocol.Envelope{}, Err(CodeNodeOffline, "节点响应超时")
	case env := <-ch:
		return env, nil
	}
}

func (c *NodeConn) newID() string {
	return strconv.FormatUint(c.reqSeq.Add(1), 10) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (c *NodeConn) write(conn *websocket.Conn, token, typ, id string, payload any) error {
	env, err := protocol.Marshal(version.ProtocolVersion, typ, id, payload)
	if err != nil {
		return err
	}
	env.Sign(token)
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, b)
}

// reconcile performs the §2.4 reconciliation: compare frps-registered proxies
// against the DB desired state, log the diff, then converge the frpc config.
func (c *NodeConn) reconcile(ctx context.Context) {
	node := c.snapshot()
	desired, ports, err := c.app.desiredProxies(ctx, c.nodeID)
	if err != nil {
		c.app.log.Warn("reconcile desired failed", "node", c.nodeID, "err", err)
		return
	}
	desiredNames := map[string]bool{}
	for _, p := range desired {
		desiredNames[p.Name] = true
	}
	// Ask the agent what frps currently has registered.
	registered := map[string]bool{}
	if env, err := c.request(ctx, protocol.TypeListProxies, nil); err == nil {
		var pl protocol.ProxyList
		if env.Decode(&pl) == nil {
			for _, p := range pl.Proxies {
				if _, _, ok := frpcfg.ParseProxyName(p.Name); ok {
					registered[p.Name] = true
				}
			}
		}
	}
	id := c.nodeID
	for name := range desiredNames {
		if !registered[name] {
			c.app.AddLog("reconcile", node.Name, &id, fmt.Sprintf("对账修复: 补写缺失隧道 %s", name))
		}
	}
	for name := range registered {
		if !desiredNames[name] {
			c.app.AddLog("reconcile", node.Name, &id, fmt.Sprintf("对账修复: 移除多余隧道 %s", name))
		}
	}
	// Converge: rewrite frpc config to the desired set and reload.
	if err := c.app.frpc.EnsureNode(&node, desired); err != nil {
		c.app.log.Warn("reconcile ensure frpc", "node", c.nodeID, "err", err)
	}
	// Push the origin-connection rate limit for the managed ports.
	c.pushRateLimit(ctx, ports)
}

// pushRateLimit sends the global new-connection rate limit for the given ports.
func (c *NodeConn) pushRateLimit(ctx context.Context, ports []protocol.PortSpec) {
	rate := c.app.connRateLimit()
	if _, err := c.request(ctx, protocol.TypeSetRateLimit, protocol.SetRateLimit{Rate: rate, Ports: ports}); err != nil {
		c.app.log.Warn("push ratelimit", "node", c.nodeID, "err", err)
	}
}

