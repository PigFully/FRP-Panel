package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// FrpsManager owns the node's frps lifecycle. frps runs as its own systemd unit
// (Restart=always, watchdog) created by the installer; the agent controls it
// via systemctl for config changes and monitors health, so crash pull-up is
// handled by systemd while the agent handles config-driven restarts + reporting.
type FrpsManager struct {
	cfg  *Config
	unit string
}

// NewFrpsManager builds a manager for the frps systemd unit named "frps".
func NewFrpsManager(cfg *Config) *FrpsManager {
	return &FrpsManager{cfg: cfg, unit: "frps"}
}

// Client returns an admin API client for the current config.
func (m *FrpsManager) Client() *FrpsClient { return NewFrpsClient(m.cfg) }

func run(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Restart rewrites frps.toml from the current config and restarts the unit.
// This interrupts the node's tunnels briefly (they auto-recover as frpc
// reconnects). Used for token rotation / config changes.
func (m *FrpsManager) Restart(ctx context.Context) error {
	if err := WriteFrpsTOML(m.cfg); err != nil {
		return err
	}
	if _, err := run(ctx, "systemctl", "restart", m.unit); err != nil {
		return fmt.Errorf("systemctl restart %s: %w", m.unit, err)
	}
	return nil
}

// EnsureUp starts frps if the unit is not active (belt-and-suspenders on top of
// systemd's own Restart=always).
func (m *FrpsManager) EnsureUp(ctx context.Context) {
	out, _ := run(ctx, "systemctl", "is-active", m.unit)
	if out != "active" {
		_, _ = run(ctx, "systemctl", "start", m.unit)
	}
}

// BinVersion returns the frps binary version string.
func (m *FrpsManager) BinVersion(ctx context.Context) string {
	out, err := run(ctx, m.cfg.FrpsBin(), "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RotateToken updates the frps auth token and restarts frps.
func (m *FrpsManager) RotateToken(ctx context.Context, newToken string) error {
	if newToken != "" {
		m.cfg.FrpsToken = newToken
		if err := m.cfg.Save(); err != nil {
			return err
		}
	}
	return m.Restart(ctx)
}
