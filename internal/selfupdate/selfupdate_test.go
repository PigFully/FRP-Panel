package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSumFor(t *testing.T) {
	sums := []byte("aabb  frpanel-agent-amd64\nccdd *frpanel-panel-amd64\n\nbad line\n")
	if got, _ := SumFor(sums, "frpanel-agent-amd64"); got != "aabb" {
		t.Fatalf("got %q", got)
	}
	// The '*' binary marker sha256sum emits must not break the lookup.
	if got, _ := SumFor(sums, "frpanel-panel-amd64"); got != "ccdd" {
		t.Fatalf("got %q", got)
	}
	if _, err := SumFor(sums, "missing"); err == nil {
		t.Fatal("expected an error for a missing entry")
	}
}

// A corrupted download must be rejected before touching the target binary.
func TestRunRejectsBadChecksum(t *testing.T) {
	bin := bytes.Repeat([]byte{0xAB}, 2<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "sha256sums.txt"):
			fmt.Fprintf(w, "%064d  frpanel-agent-amd64\n", 0) // wrong hash
		default:
			w.Write(bin)
		}
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "agent")
	os.WriteFile(target, []byte("old"), 0o755)
	err := Run(context.Background(), srv.URL, "", "frpanel-agent-amd64", target)
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatal("target binary must be untouched after a failed verify")
	}
}

// A valid download replaces the target atomically.
func TestRunAppliesVerifiedBinary(t *testing.T) {
	bin := bytes.Repeat([]byte{0xCD}, 2<<20)
	sum := sha256.Sum256(bin)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "sha256sums.txt"):
			fmt.Fprintf(w, "%s  frpanel-agent-amd64\n", hex.EncodeToString(sum[:]))
		default:
			w.Write(bin)
		}
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "agent")
	os.WriteFile(target, []byte("old"), 0o755)
	if err := Run(context.Background(), srv.URL, "", "frpanel-agent-amd64", target); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, bin) {
		t.Fatal("target was not replaced with the downloaded binary")
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Fatal("staging file must not linger after a successful swap")
	}
}

// When the direct source fails, the ghproxy-style mirror prefix is tried.
func TestFetchFallsBackToMirror(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer direct.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ghproxy semantics: the full upstream URL rides after the prefix.
		if !strings.Contains(r.URL.String(), "VERSION") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("v9.9.9"))
	}))
	defer mirror.Close()

	b, err := Fetch(context.Background(), direct.URL, mirror.URL, "VERSION")
	if err != nil {
		t.Fatalf("fetch via mirror: %v", err)
	}
	if string(b) != "v9.9.9" {
		t.Fatalf("got %q", b)
	}
}

// Both failing must surface the direct error (the actionable one).
func TestFetchReportsDirectError(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer direct.Close()
	_, err := Fetch(context.Background(), direct.URL, "http://127.0.0.1:1", "VERSION")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected the direct 502 to surface, got %v", err)
	}
}
