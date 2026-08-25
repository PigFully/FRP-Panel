package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/frpanel/frpanel/internal/protocol"
)

// nftTable is a dedicated table so we never touch the host's existing
// ip filter / ip nat tables (e.g. an Xray relay's DNAT rules).
const nftTable = "frpanel"

func nftRun(ctx context.Context, stdin string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "nft", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nft %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// ApplyRateLimit rebuilds the isolated `inet frpanel` table with a per-port
// new-connection rate limit. rate<=0 (or no ports) removes the table.
// It also keeps ufw allow rules in sync with the managed ports so mapped ports
// stay reachable on hosts where ufw's default-deny INPUT policy would otherwise
// pre-empt the frpanel table (observed on iptables-nft/ufw Ubuntu hosts). A ufw
// failure is reported but never aborts the nftables programming (the primary
// mechanism), so the agent keeps running.
func ApplyRateLimit(ctx context.Context, rl protocol.SetRateLimit) (int, error) {
	// Always start clean; ignore "No such file" when the table is absent.
	_ = nftRun(ctx, "", "delete", "table", "inet", nftTable)

	// Reconcile ufw first so a newly mapped port is open before frps starts
	// accepting on it. Collect the error without failing the whole call; the nft
	// table below still provides the rate limit on hosts where ufw is not in the
	// path.
	var ufwErr error
	if err := ufwSync(ctx, rl.Ports); err != nil {
		ufwErr = fmt.Errorf("ufw sync: %w", err)
	}

	if rl.Rate <= 0 || len(rl.Ports) == 0 {
		if ufwErr != nil {
			return 0, ufwErr
		}
		return 0, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", nftTable)
	b.WriteString("  chain input {\n")
	// priority ahead of the default filter hook, policy accept so nothing else
	// is ever blocked — only excess NEW connections to managed ports are dropped.
	b.WriteString("    type filter hook input priority -10; policy accept;\n")
	applied := 0
	for _, p := range rl.Ports {
		if strings.HasPrefix(strings.ToLower(p.Proto), "udp") {
			fmt.Fprintf(&b, "    udp dport %d limit rate over %d/second burst %d packets counter drop\n", p.Port, rl.Rate, rl.Rate)
		} else {
			fmt.Fprintf(&b, "    tcp dport %d ct state new limit rate over %d/second burst %d packets counter drop\n", p.Port, rl.Rate, rl.Rate)
		}
		applied++
	}
	b.WriteString("  }\n}\n")
	if err := nftRun(ctx, b.String(), "-f", "-"); err != nil {
		if ufwErr != nil {
			return 0, fmt.Errorf("%v; nft: %w", ufwErr, err)
		}
		return 0, err
	}
	if ufwErr != nil {
		return applied, ufwErr
	}
	return applied, nil
}

// RemoveRateLimit deletes the dedicated table and any ufw rules the agent added
// (used on uninstall).
func RemoveRateLimit(ctx context.Context) {
	_ = nftRun(ctx, "", "delete", "table", "inet", nftTable)
	_ = ufwRemoveManagedRules(ctx)
}
