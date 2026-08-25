package agent

import (
	"reflect"
	"testing"
)

// Verbatim `ufw status numbered` output from a production node (Ubuntu 24.04,
// ufw 0.36.2). Two properties matter here and both are load-bearing:
//
//   - the agent's own mapped-port rule sits at index [24] / [48], i.e. past the
//     single-digit range where ufw pads the index to "[ 9]" but not "[10]";
//   - the installer's rules for the agent port and frps port are tagged
//     "frpanel agent mgmt" / "frps bind", which must NOT match the agent's
//     hyphenated tag — deleting 8443 would cut the panel's control link.
const ufwStatusFixture = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 20/tcp                     ALLOW IN    Anywhere
[ 3] 22/tcp                     ALLOW IN    Anywhere
[ 9] 11451/tcp                  ALLOW IN    Anywhere
[11] 30011/tcp                  ALLOW IN    Anywhere
[12] 11010/tcp                  ALLOW IN    Anywhere                   # EasyTier game TCP
[22] 7000/tcp                   ALLOW IN    Anywhere                   # frps bind
[23] 8443/tcp                   ALLOW IN    Anywhere                   # frpanel agent mgmt
[24] 33223/tcp                  ALLOW IN    Anywhere                   # frpanel-agent
[46] 7000/tcp (v6)              ALLOW IN    Anywhere (v6)              # frps bind
[47] 8443/tcp (v6)              ALLOW IN    Anywhere (v6)              # frpanel agent mgmt
[48] 33223/tcp (v6)             ALLOW IN    Anywhere (v6)              # frpanel-agent
`

func TestParseManagedSpecsRealOutput(t *testing.T) {
	got := parseManagedSpecs(ufwStatusFixture)
	// v4 and v6 rows collapse to one spec: `ufw delete allow <spec>` drops both.
	want := map[string]bool{"33223/tcp": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseManagedSpecs = %v, want %v", got, want)
	}
}

// A two-digit index must yield the index-independent spec, not the column that
// naive field-splitting lands on. Splitting "[24] 33223/tcp ..." on whitespace
// puts "33223/tcp" in field 1 — the old code passed that to `ufw delete` as a
// rule number.
func TestParseManagedSpecsIndexPadding(t *testing.T) {
	for _, line := range []string{
		"[ 1] 33223/tcp                  ALLOW IN    Anywhere                   # frpanel-agent",
		"[ 9] 33223/tcp                  ALLOW IN    Anywhere                   # frpanel-agent",
		"[10] 33223/tcp                  ALLOW IN    Anywhere                   # frpanel-agent",
		"[24] 33223/tcp                  ALLOW IN    Anywhere                   # frpanel-agent",
		"[100] 33223/tcp                 ALLOW IN    Anywhere                   # frpanel-agent",
	} {
		got := parseManagedSpecs(line)
		if !got["33223/tcp"] || len(got) != 1 {
			t.Errorf("line %q parsed to %v", line, got)
		}
	}
}

// The agent must never consider the installer's management-port rules its own.
func TestParseManagedSpecsIgnoresInstallerRules(t *testing.T) {
	in := "[23] 8443/tcp   ALLOW IN   Anywhere   # frpanel agent mgmt\n" +
		"[22] 7000/tcp   ALLOW IN   Anywhere   # frps bind\n"
	if got := parseManagedSpecs(in); len(got) != 0 {
		t.Errorf("installer rules must not be treated as agent-managed, got %v", got)
	}
}

func TestParseManagedSpecsUDPAndMultiplePorts(t *testing.T) {
	in := "[ 5] 40000/udp   ALLOW IN   Anywhere   # frpanel-agent\n" +
		"[ 6] 40001/tcp   ALLOW IN   Anywhere   # frpanel-agent\n" +
		"[12] 40001/tcp (v6)  ALLOW IN  Anywhere (v6)  # frpanel-agent\n"
	want := map[string]bool{"40000/udp": true, "40001/tcp": true}
	if got := parseManagedSpecs(in); !reflect.DeepEqual(got, want) {
		t.Errorf("parseManagedSpecs = %v, want %v", got, want)
	}
}

func TestParseManagedSpecsInactiveOrEmpty(t *testing.T) {
	for _, in := range []string{"", "Status: inactive\n", "Status: active\n\nTo  Action  From\n"} {
		if got := parseManagedSpecs(in); len(got) != 0 {
			t.Errorf("parseManagedSpecs(%q) = %v, want empty", in, got)
		}
	}
}

func TestUfwRuleSpec(t *testing.T) {
	cases := []struct {
		port  int
		proto string
		want  string
	}{
		{33223, "tcp", "33223/tcp"},
		{33223, "TCP", "33223/tcp"},
		{40000, "udp", "40000/udp"},
		{40000, "UDP", "40000/udp"},
		{40000, "udp4", "40000/udp"},
		{8080, "", "8080/tcp"},
	}
	for _, c := range cases {
		if got := ufwRuleSpec(c.port, c.proto); got != c.want {
			t.Errorf("ufwRuleSpec(%d,%q) = %q, want %q", c.port, c.proto, got, c.want)
		}
	}
}

func TestSortedKeysDeterministic(t *testing.T) {
	in := map[string]bool{"40001/tcp": true, "33223/tcp": true, "40000/udp": true}
	want := []string{"33223/tcp", "40000/udp", "40001/tcp"}
	if got := sortedKeys(in); !reflect.DeepEqual(got, want) {
		t.Errorf("sortedKeys = %v, want %v", got, want)
	}
}
