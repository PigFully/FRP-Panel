package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/frpanel/frpanel/internal/protocol"
)

// ufwComment tags every ufw rule the agent manages. ufw renders it as a
// trailing "# <comment>" in `ufw status`, which is how we find the rules we
// added earlier when a mapped port is released or on uninstall.
//
// The installer's own rules for the agent port and the frps port are tagged
// differently ("frpanel agent mgmt" / "frps bind") on purpose: a Contains match
// on this hyphenated tag can never pick them up, so the agent cannot delete the
// rule that keeps its own management port reachable.
const ufwComment = "frpanel-agent"

// ufwRuleLine matches a `ufw status numbered` row and captures the rule spec
// (e.g. "33223/tcp"). The leading index is padded to width 2 by ufw — "[ 9]"
// but "[10]" — so field splitting silently reads the wrong column past 9 rules;
// anchoring on the bracket avoids that entirely.
var ufwRuleLine = regexp.MustCompile(`^\[\s*\d+\]\s+(\S+)`)

// ufwAvailable reports whether ufw exists and is active. An absent or inactive
// ufw cannot block mapped ports, so there is nothing to adapt.
func ufwAvailable(ctx context.Context) bool {
	out, err := run(ctx, "ufw", "status")
	return err == nil && strings.Contains(out, "Status: active")
}

// ufwRuleSpec returns the "<port>/<proto>" token ufw expects (e.g. "33223/tcp").
func ufwRuleSpec(port int, proto string) string {
	p := "tcp"
	if strings.HasPrefix(strings.ToLower(proto), "udp") {
		p = "udp"
	}
	return fmt.Sprintf("%d/%s", port, p)
}

// ufwManagedSpecs returns the deduped set of rule specs the agent manages.
//
// Specs rather than rule numbers: `ufw delete allow <spec>` removes the v4 and
// v6 rows together (ufw creates both for one `allow`) and cannot be thrown off
// by the renumbering that happens as earlier rules are deleted.
func ufwManagedSpecs(ctx context.Context) (map[string]bool, error) {
	out, err := run(ctx, "ufw", "status", "numbered")
	if err != nil {
		return nil, fmt.Errorf("ufw status numbered: %w", err)
	}
	return parseManagedSpecs(out), nil
}

// parseManagedSpecs extracts the agent-managed rule specs from the text of
// `ufw status numbered`. Split out from the exec call so it can be tested
// against real ufw output.
func parseManagedSpecs(out string) map[string]bool {
	specs := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, ufwComment) {
			continue
		}
		if m := ufwRuleLine.FindStringSubmatch(line); m != nil {
			specs[m[1]] = true
		}
	}
	return specs
}

// ufwDelete removes one managed rule by spec. --force is required: without it
// `ufw delete` prompts "Proceed with operation (y|n)?" and the agent has no
// stdin, so the command fails and every later sync step is skipped.
func ufwDelete(ctx context.Context, spec string) error {
	if _, err := run(ctx, "ufw", "--force", "delete", "allow", spec); err != nil {
		return fmt.Errorf("ufw delete allow %s: %w", spec, err)
	}
	return nil
}

// ufwRemoveManagedRules deletes every rule the agent added (idempotent, used on
// uninstall). It keeps going past a failure so one bad rule cannot strand the
// rest, and reports the first error it hit.
func ufwRemoveManagedRules(ctx context.Context) error {
	specs, err := ufwManagedSpecs(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, spec := range sortedKeys(specs) {
		if err := ufwDelete(ctx, spec); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ufwSync reconciles ufw allow rules to the given port set. Safe to call on
// hosts without ufw (no-op). The nftables table remains the primary rate-limit
// mechanism; this only keeps ufw's default-deny INPUT policy from blocking
// mapped ports on hosts where it would otherwise pre-empt the frpanel table.
//
// It diffs against the rules already present instead of rebuilding them:
//
//   - an unchanged port set (the overwhelmingly common case — this runs on every
//     control-link reconnect and every mapping change) issues no write at all,
//     so there is no ufw reload churn;
//   - missing rules are added before stale ones are removed, so a mapped port is
//     never briefly closed by our own reconciliation.
func ufwSync(ctx context.Context, ports []protocol.PortSpec) error {
	if !ufwAvailable(ctx) {
		return nil
	}
	have, err := ufwManagedSpecs(ctx)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, p := range ports {
		want[ufwRuleSpec(p.Port, p.Proto)] = true
	}

	var first error
	// Open what is missing first.
	for _, spec := range sortedKeys(want) {
		if have[spec] {
			continue
		}
		if _, err := run(ctx, "ufw", "allow", spec, "comment", ufwComment); err != nil {
			if first == nil {
				first = fmt.Errorf("ufw allow %s: %w", spec, err)
			}
		}
	}
	// Then drop what we manage and no longer need.
	for _, spec := range sortedKeys(have) {
		if want[spec] {
			continue
		}
		if err := ufwDelete(ctx, spec); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// sortedKeys gives map iteration a stable order so the ufw commands the agent
// runs (and the tests asserting them) are deterministic.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
