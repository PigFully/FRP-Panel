package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const browserSendBuffer = 256

// Hub fans realtime updates out to browser clients over a single WebSocket per
// client. Broadcasts are incremental (one new point at a time); on connect a
// client is backfilled from the in-memory ring buffers, then streamed live.
type Hub struct {
	app *App
	up  *websocket.Upgrader
	mu  sync.Mutex
	cl  map[*browserClient]struct{}
}

type browserClient struct {
	conn *websocket.Conn
	send chan []byte
	once sync.Once
}

// NewHub builds the hub with a same-origin check (mitigates cross-site WS
// hijacking on top of the SameSite=Lax cookie).
func NewHub(app *App) *Hub {
	return &Hub{
		app: app,
		cl:  map[*browserClient]struct{}{},
		up: &websocket.Upgrader{
			ReadBufferSize: 2048, WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // non-browser or same-origin
				}
				u, err := url.Parse(origin)
				if err != nil {
					return false
				}
				return u.Host == r.Host
			},
		},
	}
}

// ServeWS authenticates via the JWT cookie then upgrades and streams.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	if _, err := h.app.auth.claimsFromRequest(r); err != nil {
		fail(w, err)
		return
	}
	conn, err := h.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &browserClient{conn: conn, send: make(chan []byte, browserSendBuffer)}
	h.mu.Lock()
	h.cl[c] = struct{}{}
	h.mu.Unlock()

	go h.writeLoop(c)
	h.sendInit(c)

	// Read loop: we don't expect client messages; it exists to detect close and
	// to honor pongs. Any read error ends the client.
	conn.SetReadLimit(4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	h.mu.Lock()
	delete(h.cl, c)
	h.mu.Unlock()
	c.close()
	conn.Close()
}

func (c *browserClient) close() { c.once.Do(func() { close(c.send) }) }

func (h *Hub) writeLoop(c *browserClient) {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}
		case <-ping.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// trySend enqueues b, dropping the oldest queued message when the buffer is
// full (bounded queue: drop old metrics rather than grow unbounded).
func (c *browserClient) trySend(b []byte) {
	select {
	case c.send <- b:
	default:
		select {
		case <-c.send:
		default:
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

// Broadcast marshals v and sends it to every connected client.
func (h *Hub) Broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	for c := range h.cl {
		c.trySend(b)
	}
	h.mu.Unlock()
}

// Clients returns the current browser client count (internal metrics).
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.cl)
}

// ---- message DTOs ----

type wsPoint struct {
	TS        int64   `json:"ts"`
	CPU       float64 `json:"cpu"`
	Mem       float64 `json:"mem"`
	RxBps     int64   `json:"rx_bps"`
	TxBps     int64   `json:"tx_bps"`
	TunInBps  int64   `json:"tun_in_bps"`
	TunOutBps int64   `json:"tun_out_bps"`
}

type wsMetric struct {
	Type   string  `json:"type"`
	NodeID int64   `json:"node_id"`
	Point  wsPoint `json:"point"`
}

type wsNodeStatus struct {
	Type   string `json:"type"`
	NodeID int64  `json:"node_id"`
	Status string `json:"status"`
}

type wsTunnelStatus struct {
	Type       string `json:"type"`
	NodeID     int64  `json:"node_id"`
	RemotePort int    `json:"remote_port"`
	Status     string `json:"status"`
}

type wsEvent struct {
	Type   string `json:"type"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	NodeID *int64 `json:"node_id"`
	AtMs   int64  `json:"at_ms"`
}

type wsInit struct {
	Type      string              `json:"type"`
	PanelName string              `json:"panel_name"`
	Statuses  map[int64]string    `json:"statuses"`
	Series    map[int64][]wsPoint `json:"series"`
}

// sendInit backfills a freshly connected client with node statuses and each
// node's recent ring-buffer series so charts render immediately.
func (h *Hub) sendInit(c *browserClient) {
	init := wsInit{Type: "init", PanelName: h.app.PanelName(), Statuses: map[int64]string{}, Series: map[int64][]wsPoint{}}
	if h.app.DBUp() {
		if nodes, err := h.app.store.ListNodes(context.Background()); err == nil {
			for _, n := range nodes {
				init.Statuses[n.ID] = n.Status
			}
		}
	}
	h.app.pipe.mu.Lock()
	ids := make([]int64, 0, len(h.app.pipe.st))
	for id := range h.app.pipe.st {
		ids = append(ids, id)
	}
	h.app.pipe.mu.Unlock()
	for _, id := range ids {
		for _, s := range h.app.pipe.RingSnapshot(id) {
			init.Series[id] = append(init.Series[id], wsPoint{
				TS: s.AtUnixMs, CPU: s.CPU, Mem: s.Mem,
				RxBps: s.NetRxBps, TxBps: s.NetTxBps, TunInBps: s.TunInBps, TunOutBps: s.TunOutBps,
			})
		}
	}
	if b, err := json.Marshal(init); err == nil {
		c.trySend(b)
	}
}
