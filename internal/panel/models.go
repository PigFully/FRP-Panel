package panel

import "time"

// User is the single admin account.
type User struct {
	ID           int64     `db:"id" json:"id"`
	Username     string    `db:"username" json:"username"`
	PasswordHash string    `db:"password_hash" json:"-"`
	PwdVersion   int       `db:"pwd_version" json:"-"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

// Node is a public cloud node running an agent+frps.
type Node struct {
	ID           int64     `db:"id" json:"id"`
	Name         string    `db:"name" json:"name"`
	IP           string    `db:"ip" json:"ip"`
	AgentPort    int       `db:"agent_port" json:"agent_port"`
	AgentToken   string    `db:"agent_token" json:"-"` // never returned to the browser
	Fingerprint  string    `db:"fingerprint" json:"fingerprint"`
	FrpsToken    string    `db:"frps_token" json:"-"`
	FrpsPort     int       `db:"frps_port" json:"frps_port"`
	Region       string    `db:"region" json:"region"` // domestic|overseas free label
	Status       string    `db:"status" json:"status"` // online|offline
	AgentVersion string    `db:"agent_version" json:"agent_version"`
	FrpsVersion  string    `db:"frps_version" json:"frps_version"`
	LastCommitSeq int64    `db:"last_commit_seq" json:"-"`
	LastSeen     *time.Time `db:"last_seen" json:"last_seen"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

// Mapping is one local port exposed to N node targets.
type Mapping struct {
	ID        int64     `db:"id" json:"id"`
	LocalPort int       `db:"local_port" json:"local_port"`
	Proto     string    `db:"proto" json:"proto"`
	Remark    string    `db:"remark" json:"remark"`
	Enabled   bool      `db:"enabled" json:"enabled"`
	Version   int       `db:"version" json:"version"` // optimistic lock
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// MappingTarget is one (node, remote_port) destination of a mapping.
type MappingTarget struct {
	ID           int64     `db:"id" json:"id"`
	MappingID    int64     `db:"mapping_id" json:"mapping_id"`
	NodeID       int64     `db:"node_id" json:"node_id"`
	RemotePort   int       `db:"remote_port" json:"remote_port"`
	TunnelStatus string    `db:"tunnel_status" json:"tunnel_status"` // pending|online|offline|error
	StatusDetail string    `db:"status_detail" json:"status_detail"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
}

// OperationLog is one audit/event record.
type OperationLog struct {
	ID        int64     `db:"id" json:"id"`
	Type      string    `db:"type" json:"type"`     // panel_op|frp_event|reconcile
	Source    string    `db:"source" json:"source"` // username or node name
	NodeID    *int64    `db:"node_id" json:"node_id"`
	Detail    string    `db:"detail" json:"detail"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// Setting is a KV configuration row.
type Setting struct {
	K string `db:"k" json:"k"`
	V string `db:"v" json:"v"`
}

// TrafficDaily is per-node per-day byte totals (both scopes), day = Asia/Shanghai.
type TrafficDaily struct {
	NodeID      int64  `db:"node_id" json:"node_id"`
	Day         string `db:"day" json:"day"` // YYYY-MM-DD
	NodeRxBytes int64  `db:"node_rx_bytes" json:"node_rx_bytes"`
	NodeTxBytes int64  `db:"node_tx_bytes" json:"node_tx_bytes"`
	TunInBytes  int64  `db:"tun_in_bytes" json:"tun_in_bytes"`
	TunOutBytes int64  `db:"tun_out_bytes" json:"tun_out_bytes"`
}
