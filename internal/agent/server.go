package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frpanel/frpanel/internal/portutil"
	"github.com/frpanel/frpanel/internal/procmetrics"
	"github.com/frpanel/frpanel/internal/protocol"
	"github.com/frpanel/frpanel/internal/selfupdate"
	"github.com/frpanel/frpanel/internal/version"
	"github.com/gorilla/websocket"
)

const (
	metricsInterval = 5 * time.Second
	readTimeout     = 25 * time.Second
	writeTimeout    = 10 * time.Second
	sendBuffer      = 128
	// backfillSendTimeout bounds how long one replayed record waits for room in
	// the send queue, so a wedged socket cannot pin the replay goroutine forever.
	backfillSendTimeout = 30 * time.Second
	// maxBackfillRounds caps the catch-up loop. Each round only has to carry the
	// samples produced during the previous one, so it converges in a handful of
	// rounds; the cap just guarantees the session eventually goes live.
	maxBackfillRounds = 24
)

// Server is the agent's control-plane WebSocket server.
type Server struct {
	cfg     *Config
	frps    *FrpsManager
	wal     *WAL
	col     *procmetrics.Collector
	limiter *authLimiter
	log     *slog.Logger
	up      *websocket.Upgrader

	mu       sync.Mutex
	sessions map[*session]struct{}

	tunMu   sync.Mutex
	lastTun map[string]tunPrev

	// restartFn triggers a clean shutdown (main's signal context) so systemd's
	// Restart=always respawns the process — used after a self-update swap.
	restartFn func()
	updating  atomic.Bool

	frpsUpPrev bool
	started    time.Time
}

// OnRestart registers the clean-shutdown trigger used to restart into a newly
// installed binary.
func (s *Server) OnRestart(fn func()) { s.restartFn = fn }

type tunPrev struct{ in, out int64 }

type session struct {
	conn  *websocket.Conn
	send  chan protocol.Envelope
	ip    string
	proto int

	// ready gates live metric samples. A reconnecting panel must receive its WAL
	// replay in strict ascending seq order first, because its (node,seq) tracker
	// only accepts a strictly greater seq — one live sample slipping in mid-replay
	// would jump the watermark to the newest seq and make every remaining (older)
	// replayed record be discarded as stale.
	ready atomic.Bool
	// done is closed on teardown. The send channel is deliberately never closed:
	// the replay goroutine is a concurrent sender, and closing the channel under
	// it would panic the whole agent.
	done chan struct{}
	once sync.Once
}

func (s *session) close() { s.once.Do(func() { close(s.done) }) }

// NewServer builds the agent server from a loaded config.
func NewServer(cfg *Config, log *slog.Logger) (*Server, error) {
	wal, err := OpenWAL(cfg.WALDir())
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:      cfg,
		frps:     NewFrpsManager(cfg),
		wal:      wal,
		col:      procmetrics.NewCollector(),
		limiter:  newAuthLimiter(),
		log:      log,
		up:       &websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(*http.Request) bool { return true }},
		sessions: map[*session]struct{}{},
		lastTun:  map[string]tunPrev{},
		started:  time.Now(),
	}, nil
}

// Run starts the metrics loop and the TLS HTTP server, blocking until ctx done.
func (s *Server) Run(ctx context.Context) error {
	cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("load tls keypair: %w", err)
	}
	go s.metricsLoop(ctx)
	go s.maintenanceLoop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/agent/ws", s.handleWS)

	srv := &http.Server{
		Addr:      s.cfg.BindAddr,
		Handler:   mux,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ErrorLog:  nil,
	}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	s.log.Info("agent listening", "addr", s.cfg.BindAddr, "public_ip", s.cfg.PublicIP, "version", version.Version)
	if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.limiter.Blocked(ip, time.Now()) {
		http.Error(w, "too many failures", http.StatusTooManyRequests)
		return
	}
	conn, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)

	// Handshake: expect a signed Hello within the read timeout.
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	_, data, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return
	}
	var env protocol.Envelope
	if json.Unmarshal(data, &env) != nil || env.Type != protocol.TypeHello {
		s.limiter.Fail(ip, time.Now())
		conn.Close()
		return
	}
	if verr := env.Verify(s.cfg.AgentToken, time.Now().Unix()); verr != nil {
		s.limiter.Fail(ip, time.Now())
		s.log.Warn("agent auth failed", "ip", ip, "err", verr)
		conn.Close()
		return
	}
	s.limiter.Success(ip)
	var hello protocol.Hello
	_ = env.Decode(&hello)

	sess := &session{
		conn: conn, send: make(chan protocol.Envelope, sendBuffer),
		ip: ip, proto: version.ProtocolVersion, done: make(chan struct{}),
	}
	compatible := hello.MinAgentProto <= version.ProtocolVersion
	ack := protocol.HelloAck{
		AgentVersion: version.Version,
		FrpsVersion:  s.frps.BinVersion(r.Context()),
		Proto:        version.ProtocolVersion,
		FrpsPort:     s.cfg.FrpsBindPort,
		FrpsToken:    s.cfg.FrpsToken,
		Compatible:   compatible,
	}
	if !compatible {
		ack.Message = "panel requires a newer agent protocol"
	}
	s.enqueueReply(sess, protocol.TypeHelloAck, env.ID, ack)

	s.mu.Lock()
	s.sessions[sess] = struct{}{}
	n := len(s.sessions)
	s.mu.Unlock()
	s.log.Info("panel connected", "ip", ip, "panel_version", hello.PanelVersion, "sessions", n, "last_commit_seq", hello.LastCommitSeq)

	go s.writer(sess)
	// Replay WAL entries accumulated past the panel's committed watermark.
	go s.backfill(sess, hello.LastCommitSeq)
	s.reader(sess) // blocks until the connection ends

	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
	sess.close()
	conn.Close()
	s.log.Info("panel disconnected", "ip", ip)
}

// backfill replays every WAL record past the panel's committed watermark, then
// opens the gate on live samples.
//
// It loops because live sampling keeps appending to the WAL while the replay is
// in flight, and those samples are being withheld from this session (see
// session.ready). Each round therefore only has to carry what the previous round
// produced, which converges quickly. Nothing is lost by withholding them: a
// sample is written to the WAL before it is broadcast, so the next round picks it
// up. Sends block rather than drop — dropping is what silently broke
// exactly-once accounting before.
func (s *Server) backfill(sess *session, afterSeq int64) {
	defer sess.ready.Store(true) // never leave a session gated shut
	cursor := afterSeq
	total := 0
	for round := 0; round < maxBackfillRounds; round++ {
		sent, ended := 0, false
		err := s.wal.Stream(cursor, func(rec TrafficRec) bool {
			m := protocol.Metrics{
				Seq: rec.Seq, SampledAt: rec.AtMs,
				NetRxDelta: rec.NodeRxDelta, NetTxDelta: rec.NodeTxDelta,
				Backfill: true,
			}
			for _, p := range rec.Proxies {
				m.Proxies = append(m.Proxies, protocol.ProxyTraffic{
					RemotePort: p.RemotePort, Proto: p.Proto, Status: p.Status,
					DeltaIn: p.In, DeltaOut: p.Out,
				})
			}
			if !s.sendWait(sess, protocol.TypeMetrics, m) {
				ended = true
				return false
			}
			cursor = rec.Seq
			sent++
			return true
		})
		total += sent
		switch {
		case err != nil:
			s.log.Warn("wal replay failed", "err", err, "through_seq", cursor, "sent", total)
			return
		case ended:
			s.log.Warn("wal replay interrupted", "through_seq", cursor, "sent", total)
			return
		case sent == 0:
			if total > 0 {
				s.log.Info("wal replay complete", "records", total, "after_seq", afterSeq, "through_seq", cursor)
			}
			return
		}
	}
	s.log.Warn("wal replay hit round cap; going live", "through_seq", cursor, "sent", total)
}

func (s *Server) reader(sess *session) {
	for {
		_ = sess.conn.SetReadDeadline(time.Now().Add(readTimeout))
		_, data, err := sess.conn.ReadMessage()
		if err != nil {
			return
		}
		var env protocol.Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		if verr := env.Verify(s.cfg.AgentToken, time.Now().Unix()); verr != nil {
			if verr == protocol.ErrBadSig {
				s.log.Warn("bad signature on live message; closing", "ip", sess.ip)
				return
			}
			s.log.Warn("dropping message outside replay window", "ip", sess.ip, "type", env.Type)
			continue
		}
		s.dispatch(sess, env)
	}
}

func (s *Server) dispatch(sess *session, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypePing:
		s.enqueueReply(sess, protocol.TypePong, env.ID, nil)
	case protocol.TypePortCheck:
		var pc protocol.PortCheck
		_ = env.Decode(&pc)
		s.enqueueReply(sess, protocol.TypePortCheckRes, env.ID, s.checkPort(pc))
	case protocol.TypeListProxies:
		proxies, err := s.frps.Client().Proxies()
		if err != nil {
			s.log.Warn("list proxies failed", "err", err)
		}
		s.enqueueReply(sess, protocol.TypeProxyList, env.ID, protocol.ProxyList{Proxies: proxies})
	case protocol.TypeRestartFrps:
		var rf protocol.RestartFrps
		_ = env.Decode(&rf)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := s.frps.RotateToken(ctx, rf.NewFrpsToken)
		cancel()
		res := protocol.RestartRes{OK: err == nil, FrpsVersion: s.frps.BinVersion(context.Background())}
		if err != nil {
			res.Message = err.Error()
			s.log.Error("frps restart failed", "err", err)
		} else {
			s.log.Info("frps restarted", "reason", rf.Reason)
		}
		s.enqueueReply(sess, protocol.TypeRestartRes, env.ID, res)
	case protocol.TypeSetRateLimit:
		var rl protocol.SetRateLimit
		_ = env.Decode(&rl)
		// Generous budget: a first-time sync on a ufw host runs a few ufw
		// commands, each of which reloads the ruleset. The panel gives up on the
		// reply sooner, but the agent must not abandon the work half-applied.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		applied, err := ApplyRateLimit(ctx, rl)
		cancel()
		res := protocol.RateLimitRes{OK: err == nil, Applied: applied}
		if err != nil {
			res.Message = err.Error()
			s.log.Warn("apply ratelimit failed", "err", err)
		} else {
			s.log.Info("ratelimit applied", "rate", rl.Rate, "ports", applied)
		}
		s.enqueueReply(sess, protocol.TypeRateLimitRes, env.ID, res)
	case protocol.TypeUpdateAgent:
		var up protocol.UpdateAgent
		_ = env.Decode(&up)
		res := protocol.UpdateRes{OK: true, Started: true}
		switch {
		case !strings.HasPrefix(up.BaseURL, "https://") && !strings.HasPrefix(up.BaseURL, "http://"):
			res = protocol.UpdateRes{OK: false, Message: "无效的更新源地址"}
		case !s.updating.CompareAndSwap(false, true):
			res = protocol.UpdateRes{OK: false, Message: "已有一次升级正在进行"}
		}
		s.enqueueReply(sess, protocol.TypeUpdateRes, env.ID, res)
		if res.OK {
			go s.runSelfUpdate(up)
		}
	default:
		// Unknown type: tolerate per protocol versioning rules.
		s.log.Warn("unknown message type; skipping", "type", env.Type, "ver", env.Ver)
	}
}

// runSelfUpdate downloads, verifies and installs the new agent binary, then
// triggers a clean restart. The reply to the panel went out before this runs;
// the observable outcome is the agent reconnecting with a new version, or an
// agent_update_failed event landing in the panel's operation log.
func (s *Server) runSelfUpdate(up protocol.UpdateAgent) {
	defer s.updating.Store(false)
	exe, err := os.Executable()
	if err != nil {
		s.updateFailed("定位自身二进制失败: " + err.Error())
		return
	}
	asset := "frpanel-agent-" + runtime.GOARCH
	s.log.Info("agent self-update started", "asset", asset, "base", up.BaseURL, "target_version", up.Version)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := selfupdate.Run(ctx, up.BaseURL, up.Mirror, asset, exe); err != nil {
		s.updateFailed(err.Error())
		return
	}
	s.log.Info("agent self-update applied; restarting", "target_version", up.Version)
	s.broadcast(protocol.TypeEvent, protocol.Event{Kind: "agent_update", Detail: "Agent 新版本已就位，正在重启生效（隧道不中断）", AtMs: time.Now().UnixMilli()})
	time.Sleep(500 * time.Millisecond) // let the event drain to connected panels
	if s.restartFn != nil {
		s.restartFn()
	}
}

func (s *Server) updateFailed(msg string) {
	s.log.Error("agent self-update failed", "err", msg)
	s.broadcast(protocol.TypeEvent, protocol.Event{Kind: "agent_update_failed", Detail: "Agent 在线升级失败: " + msg, AtMs: time.Now().UnixMilli()})
}

func (s *Server) checkPort(pc protocol.PortCheck) protocol.PortCheckRes {
	res := protocol.PortCheckRes{Port: pc.Port, Proto: pc.Proto, Available: true}
	if r := portutil.ReservedReason(pc.Port); r != "" {
		res.Available = false
		res.Reason = "reserved"
		res.Process = r
		return res
	}
	if listening, proc := portutil.LocalListen(pc.Port, pc.Proto); listening {
		res.Available = false
		res.Reason = "listen"
		if proc == "" {
			proc = "未知进程"
		}
		res.Process = proc
		return res
	}
	if proxies, err := s.frps.Client().Proxies(); err == nil {
		for _, p := range proxies {
			if p.RemotePort == pc.Port {
				res.Available = false
				res.Reason = "frps_registered"
				res.Process = "frps(" + p.Name + ")"
				return res
			}
		}
	}
	return res
}

func (s *Server) writer(sess *session) {
	for {
		var env protocol.Envelope
		select {
		case <-sess.done:
			return
		case env = <-sess.send:
		}
		b, err := json.Marshal(env)
		if err != nil {
			continue
		}
		_ = sess.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := sess.conn.WriteMessage(websocket.TextMessage, b); err != nil {
			return
		}
	}
}

// sign builds and signs an envelope, reporting false if it cannot be marshalled.
func (s *Server) sign(typ, id string, payload any) (protocol.Envelope, bool) {
	env, err := protocol.Marshal(version.ProtocolVersion, typ, id, payload)
	if err != nil {
		return env, false
	}
	env.Sign(s.cfg.AgentToken)
	return env, true
}

// enqueue signs and queues a message, dropping it if the queue is full. Fine for
// live samples (the next one is 5s away, and the WAL has the durable copy) but
// never for WAL replay — use sendWait there.
func (s *Server) enqueue(sess *session, typ, id string, payload any) {
	env, ok := s.sign(typ, id, payload)
	if !ok {
		return
	}
	select {
	case sess.send <- env:
	default:
		// Buffer full: drop rather than block or grow unboundedly.
	}
}

func (s *Server) enqueueReply(sess *session, typ, id string, payload any) {
	s.enqueue(sess, typ, id, payload)
}

// sendWait queues a message, waiting for room. Reports false if the session ended
// or the queue stayed full implausibly long, in which case the caller must stop.
func (s *Server) sendWait(sess *session, typ string, payload any) bool {
	// Test for teardown before offering the send: select chooses randomly among
	// ready cases, so an ended session with room left in the queue would
	// otherwise keep accepting records instead of stopping the replay.
	select {
	case <-sess.done:
		return false
	default:
	}
	env, ok := s.sign(typ, "", payload)
	if !ok {
		return false
	}
	t := time.NewTimer(backfillSendTimeout)
	defer t.Stop()
	select {
	case sess.send <- env:
		return true
	case <-sess.done:
		return false
	case <-t.C:
		return false
	}
}

// broadcast fans a message out to every live panel session.
func (s *Server) broadcast(typ string, payload any) { s.fanout(typ, payload, false) }

// broadcastSample fans out a metric sample, skipping any session still receiving
// its WAL replay — those samples must not overtake it (see session.ready).
func (s *Server) broadcastSample(m protocol.Metrics) { s.fanout(protocol.TypeMetrics, m, true) }

func (s *Server) fanout(typ string, payload any, seqOrdered bool) {
	env, ok := s.sign(typ, "", payload)
	if !ok {
		return
	}
	s.mu.Lock()
	for sess := range s.sessions {
		if seqOrdered && !sess.ready.Load() {
			continue // still replaying; the WAL copy goes out in the catch-up round
		}
		select {
		case sess.send <- env:
		default:
		}
	}
	s.mu.Unlock()
}

func (s *Server) metricsLoop(ctx context.Context) {
	t := time.NewTicker(metricsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.sampleAndBroadcast(now)
		}
	}
}

func (s *Server) sampleAndBroadcast(now time.Time) {
	r := s.col.Sample(now)
	_, up := s.frps.Client().ServerInfo()

	var proxies []protocol.ProxyTraffic
	var walProxies []ProxyDelta
	if up {
		if list, err := s.frps.Client().Proxies(); err == nil {
			s.tunMu.Lock()
			seen := make(map[string]struct{}, len(list))
			for _, p := range list {
				key := fmt.Sprintf("%s:%d", p.Proto, p.RemotePort)
				seen[key] = struct{}{}
				prev := s.lastTun[key]
				din := p.TodayIn - prev.in
				dout := p.TodayOut - prev.out
				if din < 0 {
					din = p.TodayIn // counter reset (daily rollover / frps restart)
				}
				if dout < 0 {
					dout = p.TodayOut
				}
				s.lastTun[key] = tunPrev{in: p.TodayIn, out: p.TodayOut}
				proxies = append(proxies, protocol.ProxyTraffic{
					RemotePort: p.RemotePort, Proto: p.Proto, Status: p.Status,
					DeltaIn: din, DeltaOut: dout,
				})
				walProxies = append(walProxies, ProxyDelta{RemotePort: p.RemotePort, Proto: p.Proto, In: din, Out: dout, Status: p.Status})
			}
			// Drop counters for proxies frps no longer reports (mapping removed /
			// port changed), so lastTun cannot grow without bound over time.
			for key := range s.lastTun {
				if _, ok := seen[key]; !ok {
					delete(s.lastTun, key)
				}
			}
			s.tunMu.Unlock()
		}
	}

	seq := s.wal.NextSeq()
	atMs := now.UnixMilli()
	_ = s.wal.Append(TrafficRec{Seq: seq, AtMs: atMs, NodeRxDelta: r.NetRxDelta, NodeTxDelta: r.NetTxDelta, Proxies: walProxies})

	m := protocol.Metrics{
		Seq: seq, CPU: r.CPUPercent, Mem: r.MemPercent, MemTotal: r.MemTotal,
		NetRxBps: r.NetRxBps, NetTxBps: r.NetTxBps,
		NetRxDelta: r.NetRxDelta, NetTxDelta: r.NetTxDelta,
		FrpsUp: up, Proxies: proxies, SampledAt: atMs,
	}
	s.broadcastSample(m)

	if up != s.frpsUpPrev {
		kind := "frps_down"
		detail := "frps 管理接口不可达"
		if up {
			kind = "frps_up"
			detail = "frps 已就绪"
		}
		s.broadcast(protocol.TypeEvent, protocol.Event{Kind: kind, Detail: detail, AtMs: atMs})
		s.frpsUpPrev = up
	}
}

func (s *Server) maintenanceLoop(ctx context.Context) {
	ensure := time.NewTicker(30 * time.Second)
	rotate := time.NewTicker(6 * time.Hour)
	defer ensure.Stop()
	defer rotate.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ensure.C:
			s.frps.EnsureUp(ctx)
		case <-rotate.C:
			if err := s.wal.Rotate(7 * 24 * time.Hour); err != nil {
				s.log.Warn("wal rotate", "err", err)
			}
		}
	}
}

// Close releases resources.
func (s *Server) Close() { _ = s.wal.Close() }
