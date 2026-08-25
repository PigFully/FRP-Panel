// Package protocol defines the JSON message envelope and payloads exchanged
// over the panel<->agent WebSocket-over-TLS control channel.
//
// Direction rule: the panel always dials out to the agent (the panel has no
// public IP). A single long-lived connection multiplexes handshake, heartbeat,
// commands and metric reports. Every message carries a ver field; receivers
// MUST tolerate unknown Type values (log a warning, do not drop the link).
package protocol

import (
	"encoding/json"
	"time"
)

// Message types. Keep values stable across versions.
const (
	TypeHello        = "hello"         // panel -> agent (handshake, includes auth)
	TypeHelloAck     = "hello_ack"     // agent -> panel (handshake reply)
	TypePing         = "ping"          // panel -> agent heartbeat
	TypePong         = "pong"          // agent -> panel heartbeat reply
	TypePortCheck    = "port_check"    // panel -> agent: is remote port free?
	TypePortCheckRes = "port_check_res"
	TypeListProxies  = "list_proxies" // panel -> agent: report frps-registered proxies (reconciliation)
	TypeProxyList    = "proxy_list"   // agent -> panel reply
	TypeRestartFrps  = "restart_frps" // panel -> agent: rewrite+restart frps (token/config change)
	TypeRestartRes   = "restart_res"
	TypeSetRateLimit = "set_ratelimit" // panel -> agent: program per-port new-conn rate limit
	TypeRateLimitRes = "ratelimit_res"
	TypeUpdateAgent  = "update_agent" // panel -> agent: self-update from the distribution base (proto >= 2)
	TypeUpdateRes    = "update_res"
	TypeMetrics      = "metrics" // agent -> panel: periodic sample (unsolicited)
	TypeEvent        = "event"   // agent -> panel: frp/agent event for the operation log
	TypeError        = "error"   // either direction: structured error reply
)

// Envelope wraps every message. Ver is the protocol version of the sender.
// ID correlates a request with its reply (empty for unsolicited pushes).
type Envelope struct {
	Ver     int             `json:"ver"`
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	TS      int64           `json:"ts"`             // unix seconds, for anti-replay (+/-60s tolerance)
	Nonce   string          `json:"nonce,omitempty"`
	Sig     string          `json:"sig,omitempty"`  // hex HMAC-SHA256 over canonical fields, keyed by node token
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Hello is sent by the panel to authenticate and negotiate versions. The panel
// includes its last committed traffic seq so the agent can backfill WAL entries
// accumulated while the link was down.
type Hello struct {
	PanelVersion  string `json:"panel_version"`
	MinAgentProto int    `json:"min_agent_proto"`
	LastCommitSeq int64  `json:"last_commit_seq"`
}

// HelloAck is the agent's reply describing itself. FrpsToken is delivered here
// (over the pinned+authenticated channel) rather than in the copy-pasted
// receipt, so the panel can configure its frpc without exposing the secret.
type HelloAck struct {
	AgentVersion string `json:"agent_version"`
	FrpsVersion  string `json:"frps_version"`
	Proto        int    `json:"proto"`
	FrpsPort     int    `json:"frps_port"`
	FrpsToken    string `json:"frps_token"`
	Compatible   bool   `json:"compatible"`
	Message      string `json:"message,omitempty"`
}

// PortCheck asks whether a public port can be bound on the node.
type PortCheck struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"` // tcp|udp
}

// PortCheckRes reports availability and, if busy, the occupying process.
type PortCheckRes struct {
	Port      int    `json:"port"`
	Proto     string `json:"proto"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`  // e.g. "listen", "reserved", "frps_registered"
	Process   string `json:"process,omitempty"` // occupying process name if known
}

// ProxyInfo describes one proxy registered on frps.
type ProxyInfo struct {
	Name       string `json:"name"`
	Proto      string `json:"proto"`
	RemotePort int    `json:"remote_port"`
	Status     string `json:"status"` // online|offline|... (frps status)
	TodayIn    int64  `json:"today_in"`
	TodayOut   int64  `json:"today_out"`
}

// ProxyList is the agent's reply to ListProxies (reconciliation source of truth check).
type ProxyList struct {
	Proxies []ProxyInfo `json:"proxies"`
}

// RestartFrps triggers a frps rewrite+restart (interrupts that node's tunnels).
// NewFrpsToken, when set, rotates the frps auth token; the agent owns the rest
// of the TOML template (admin creds, allowPorts). Empty token = plain restart.
type RestartFrps struct {
	NewFrpsToken string `json:"new_frps_token,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// RestartRes reports the outcome of a frps restart.
type RestartRes struct {
	OK          bool   `json:"ok"`
	FrpsVersion string `json:"frps_version,omitempty"`
	Message     string `json:"message,omitempty"`
}

// PortSpec identifies a managed public port.
type PortSpec struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

// SetRateLimit asks the agent to cap new-connection rate (per second) on each
// managed public port, enforced via an isolated nftables table. Rate<=0 clears
// the limit. This blunts TCP floods that could get the node IP mis-flagged.
type SetRateLimit struct {
	Rate  int        `json:"rate"`
	Ports []PortSpec `json:"ports"`
}

// RateLimitRes reports whether the limit was applied.
type RateLimitRes struct {
	OK      bool   `json:"ok"`
	Applied int    `json:"applied"` // number of ports programmed
	Message string `json:"message,omitempty"`
}

// UpdateAgent asks the agent to self-update: download frpanel-agent-<arch>
// from BaseURL, verify it against BaseURL's sha256sums.txt, atomically replace
// its own executable and exit cleanly so systemd (Restart=always) brings the
// new version up. Mirror, when set, is a ghproxy-style prefix tried after a
// direct download fails. Version is informational (for logs).
type UpdateAgent struct {
	Version string `json:"version,omitempty"`
	BaseURL string `json:"base_url"`
	Mirror  string `json:"mirror,omitempty"`
}

// UpdateRes acknowledges an update command. Started=true means the download
// and swap proceed asynchronously after this reply — the outcome is observable
// as the agent restarting with a new version (or an agent_update_failed event).
type UpdateRes struct {
	OK      bool   `json:"ok"`
	Started bool   `json:"started"`
	Message string `json:"message,omitempty"`
}

// ProxyTraffic is a per-proxy traffic sample inside a Metrics message. Delta*
// are byte increments since the previous sample (tunnel scope), tagged by the
// enclosing Seq for exactly-once accounting.
type ProxyTraffic struct {
	RemotePort int    `json:"remote_port"`
	Proto      string `json:"proto"`
	Status     string `json:"status"`
	DeltaIn    int64  `json:"delta_in"`
	DeltaOut   int64  `json:"delta_out"`
}

// Metrics is pushed by the agent every ~5s. Two independent scopes are carried
// and must never be mixed:
//   - node bandwidth: whole-NIC counters from /proc/net/dev (includes non-frp)
//   - tunnel traffic: per-proxy counters from the frps admin API
// Seq is a monotonically increasing sample sequence used for exactly-once
// idempotent merge on the panel keyed by (node_id, seq). WAL backfill reuses it.
type Metrics struct {
	Seq        int64          `json:"seq"`
	CPU        float64        `json:"cpu"`        // percent 0..100
	Mem        float64        `json:"mem"`        // percent 0..100
	MemTotal   int64          `json:"mem_total"`  // bytes
	NetRxBps   int64          `json:"net_rx_bps"` // node NIC instantaneous, bytes/s
	NetTxBps   int64          `json:"net_tx_bps"`
	NetRxDelta int64          `json:"net_rx_delta"` // node NIC bytes since last sample
	NetTxDelta int64          `json:"net_tx_delta"`
	FrpsUp     bool           `json:"frps_up"`
	Proxies    []ProxyTraffic `json:"proxies"`
	SampledAt  int64          `json:"sampled_at"` // unix ms on the agent
	Backfill   bool           `json:"backfill,omitempty"` // WAL replay: account only, no realtime broadcast
}

// Event is a discrete happening the agent wants recorded in the operation log.
type Event struct {
	Kind    string `json:"kind"`    // frps_bind_fail|frps_up|frps_down|proxy_online|proxy_offline
	Detail  string `json:"detail"`
	AtMs    int64  `json:"at_ms"`
}

// ErrorPayload is a structured error reply.
type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Now returns the current unix seconds (used for anti-replay stamping).
func Now() int64 { return time.Now().Unix() }

// Marshal builds an envelope with the given payload (payload may be nil).
func Marshal(ver int, typ, id string, payload any) (Envelope, error) {
	e := Envelope{Ver: ver, Type: typ, ID: id, TS: Now()}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return e, err
		}
		e.Payload = b
	}
	return e, nil
}

// Decode unmarshals the envelope payload into v.
func (e Envelope) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}
