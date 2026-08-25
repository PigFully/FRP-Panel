// Package selfupdate implements the panel/agent online update: download a
// release binary from the distribution base (GitHub Releases), verify it
// against the published sha256sums.txt, and atomically replace the running
// executable. The caller then exits cleanly and systemd (Restart=always)
// brings the new version up — config, tokens, certs and WAL are all files on
// disk and survive untouched.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// maxAssetSize caps a downloaded asset so a misbehaving server cannot balloon
// memory (release binaries are ~10 MB).
const maxAssetSize = 256 << 20

// httpTimeout bounds one download attempt end to end.
const httpTimeout = 3 * time.Minute

// Fetch downloads <base>/<name>, following redirects (GitHub Releases answer
// with a 302 to the CDN). When the direct download fails and mirror is set, it
// retries via "<mirror>/<full-url>" — the ghproxy-style prefix the install
// scripts' --mirror flag uses — and reports the direct error if both fail.
func Fetch(ctx context.Context, base, mirror, name string) ([]byte, error) {
	url := strings.TrimRight(base, "/") + "/" + name
	b, err := fetchOne(ctx, url)
	if err == nil {
		return b, nil
	}
	if mirror = strings.TrimSpace(mirror); mirror != "" {
		if b2, err2 := fetchOne(ctx, strings.TrimRight(mirror, "/")+"/"+url); err2 == nil {
			return b2, nil
		}
	}
	return nil, err
}

func fetchOne(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAssetSize))
}

// SumFor extracts the hex sha256 for name from a sha256sums.txt body
// ("<hex>  <name>" per line; a leading '*' on the name — sha256sum's binary
// marker — is tolerated).
func SumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("sha256sums.txt 中没有 %s 的条目", name)
}

// Apply stages the new binary next to target and atomically renames it over.
// Rename (not in-place write) is required twice over: writing a running
// executable fails with ETXTBSY, and rename guarantees the path never holds a
// half-written file.
func Apply(bin []byte, target string) error {
	tmp := target + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Run performs the full update of target from base: fetch the checksum list
// and the asset, verify, swap. It does NOT restart the process — the caller
// decides when to exit for systemd to respawn it.
func Run(ctx context.Context, base, mirror, asset, target string) error {
	sums, err := Fetch(ctx, base, mirror, "sha256sums.txt")
	if err != nil {
		return fmt.Errorf("下载校验清单失败: %w", err)
	}
	want, err := SumFor(sums, asset)
	if err != nil {
		return err
	}
	bin, err := Fetch(ctx, base, mirror, asset)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", asset, err)
	}
	if len(bin) < 1<<20 {
		return fmt.Errorf("下载的 %s 过小（%d 字节），疑似不完整", asset, len(bin))
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s sha256 校验不通过，已放弃替换", asset)
	}
	if err := Apply(bin, target); err != nil {
		return fmt.Errorf("替换二进制失败: %w", err)
	}
	return nil
}
