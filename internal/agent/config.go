// Package agent implements the node agent: a TLS+token WebSocket server the
// panel dials into, an frps lifecycle manager, a /proc metrics collector, a
// remote-port occupancy precheck, and a traffic WAL for exactly-once backfill.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultDataDir is where the agent stores its state and binaries.
const DefaultDataDir = "/opt/frp-agent"

// Config is the agent's persisted state (created by `agent init`).
type Config struct {
	Version       string `json:"version"`
	BindAddr      string `json:"bind_addr"`       // e.g. :8443
	AgentToken    string `json:"agent_token"`     // panel<->agent HMAC key
	FrpsToken     string `json:"frps_token"`      // frpc<->frps auth token (independent)
	FrpsBindPort  int    `json:"frps_bind_port"`  // 7000
	FrpsAdminAddr string `json:"frps_admin_addr"` // 127.0.0.1
	FrpsAdminPort int    `json:"frps_admin_port"` // random loopback
	FrpsAdminUser string `json:"frps_admin_user"`
	FrpsAdminPass string `json:"frps_admin_pass"`
	CertFile      string `json:"cert_file"`
	KeyFile       string `json:"key_file"`
	Fingerprint   string `json:"fingerprint"`
	PublicIP      string `json:"public_ip"`
	DataDir       string `json:"data_dir"`
}

// Paths derived from DataDir.
func (c *Config) ConfigPath() string { return filepath.Join(c.DataDir, "config.json") }
func (c *Config) FrpsTOMLPath() string { return filepath.Join(c.DataDir, "frps.toml") }
func (c *Config) FrpsBin() string     { return filepath.Join(c.DataDir, "frps") }
func (c *Config) LogsDir() string     { return filepath.Join(c.DataDir, "logs") }
func (c *Config) WALDir() string      { return filepath.Join(c.DataDir, "wal") }
func (c *Config) TLSDir() string      { return filepath.Join(c.DataDir, "tls") }

// ConfigPathFor returns the config.json path for a data dir.
func ConfigPathFor(dataDir string) string { return filepath.Join(dataDir, "config.json") }

// Load reads a config.json from dataDir.
func Load(dataDir string) (*Config, error) {
	b, err := os.ReadFile(ConfigPathFor(dataDir))
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.DataDir == "" {
		c.DataDir = dataDir
	}
	return &c, nil
}

// Save writes config.json atomically with 0600 perms (contains secrets).
func (c *Config) Save() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.ConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.ConfigPath())
}
