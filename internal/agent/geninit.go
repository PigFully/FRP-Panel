package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/frpanel/frpanel/internal/certutil"
	"github.com/frpanel/frpanel/internal/frpcfg"
	"github.com/frpanel/frpanel/internal/receipt"
	"github.com/frpanel/frpanel/internal/version"
)

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var ipv4re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

// ProbePublicIP tries several probe sources (CN + international); the first
// that yields a valid public IPv4 wins. Returns "" if all fail.
func ProbePublicIP() string {
	sources := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://ipinfo.io/ip",
		"https://4.ipw.cn",
		"http://ip.3322.net",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, u := range sources {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "curl/8")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		m := ipv4re.FindString(string(body))
		if ip := net.ParseIP(m); ip != nil && ip.To4() != nil && !ip.IsPrivate() && !ip.IsLoopback() {
			return m
		}
	}
	return ""
}

// InitOptions configures Init.
type InitOptions struct {
	DataDir  string
	BindAddr string // default :8443
	ManualIP string // override public IP probe
}

// Init creates or upgrades the agent config. On a fresh install it generates
// both tokens, a self-signed cert, the frps.toml and probes the public IP. On
// re-run (upgrade) it preserves tokens/cert/IP and only refreshes the version
// and regenerates derived files so an upgrade never breaks existing mappings.
func Init(opt InitOptions) (*Config, error) {
	if opt.DataDir == "" {
		opt.DataDir = DefaultDataDir
	}
	if opt.BindAddr == "" {
		opt.BindAddr = ":8443"
	}
	upgrade := false
	cfg, err := Load(opt.DataDir)
	if err == nil {
		upgrade = true
		cfg.Version = version.Version
		if opt.ManualIP != "" {
			cfg.PublicIP = opt.ManualIP
		}
	} else {
		cfg = &Config{
			Version:       version.Version,
			BindAddr:      opt.BindAddr,
			AgentToken:    randHex(32),
			FrpsToken:     randHex(32),
			FrpsBindPort:  7000,
			FrpsAdminAddr: "127.0.0.1",
			FrpsAdminPort: pickAdminPort(),
			FrpsAdminUser: "admin",
			FrpsAdminPass: randHex(18),
			DataDir:       opt.DataDir,
		}
		ip := opt.ManualIP
		if ip == "" {
			ip = ProbePublicIP()
		}
		cfg.PublicIP = ip
	}

	for _, d := range []string{cfg.DataDir, cfg.LogsDir(), cfg.WALDir(), cfg.TLSDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	// TLS cert: generate once, preserve across upgrades.
	cfg.CertFile = filepath.Join(cfg.TLSDir(), "agent.crt")
	cfg.KeyFile = filepath.Join(cfg.TLSDir(), "agent.key")
	if !fileExists(cfg.CertFile) || !fileExists(cfg.KeyFile) {
		hosts := []string{"127.0.0.1"}
		if cfg.PublicIP != "" {
			hosts = append(hosts, cfg.PublicIP)
		}
		if hn, _ := os.Hostname(); hn != "" {
			hosts = append(hosts, hn)
		}
		certPEM, keyPEM, err := certutil.GenerateSelfSigned(hosts)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.CertFile, certPEM, 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.KeyFile, keyPEM, 0o600); err != nil {
			return nil, err
		}
	}
	fp, err := fingerprintOfFile(cfg.CertFile)
	if err != nil {
		return nil, err
	}
	cfg.Fingerprint = fp

	if err := WriteFrpsTOML(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	_ = upgrade
	return cfg, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fingerprintOfFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return certutil.FingerprintPEM(b)
}

func pickAdminPort() int {
	// Loopback-only admin port for frps; pick a random high port not in the
	// reserved segments and not currently listening.
	for i := 0; i < 50; i++ {
		b := make([]byte, 2)
		_, _ = rand.Read(b)
		p := 20000 + int(uint16(b[0])<<8|uint16(b[1]))%9000 // 20000..28999
		return p
	}
	return 21000
}

// WriteFrpsTOML renders and writes the node's static frps config. The admin
// port is added to the reserved set so a user-requested remote_port can never
// collide with the loopback dashboard.
func WriteFrpsTOML(cfg *Config) error {
	reserved := [][2]int{
		{7000, 7000},               // frps bind
		{8443, 8443},               // agent mgmt
		{7400, 7500},               // frpc admin range
		{cfg.FrpsAdminPort, cfg.FrpsAdminPort},
	}
	toml := frpcfg.RenderFrps(frpcfg.FrpsParams{
		BindPort:      cfg.FrpsBindPort,
		Token:         cfg.FrpsToken,
		TLSForce:      true,
		AdminAddr:     cfg.FrpsAdminAddr,
		AdminPort:     cfg.FrpsAdminPort,
		AdminUser:     cfg.FrpsAdminUser,
		AdminPassword: cfg.FrpsAdminPass,
		LogFile:       filepath.Join(cfg.LogsDir(), "frps.log"),
		AllowLo:       1024,
		AllowHi:       65535,
		Reserved:      reserved,
	})
	return os.WriteFile(cfg.FrpsTOMLPath(), []byte(toml), 0o600)
}

// BuildReceipt constructs the base64 registration receipt for this agent.
func (c *Config) BuildReceipt() (string, error) {
	port := 8443
	if strings.HasPrefix(c.BindAddr, ":") {
		fmt.Sscanf(c.BindAddr, ":%d", &port)
	} else if i := strings.LastIndexByte(c.BindAddr, ':'); i >= 0 {
		fmt.Sscanf(c.BindAddr[i:], ":%d", &port)
	}
	return receipt.Encode(receipt.Receipt{
		IP:       c.PublicIP,
		Port:     port,
		Token:    c.AgentToken,
		FP:       c.Fingerprint,
		Ver:      version.Version,
		FrpsPort: c.FrpsBindPort,
	})
}

// PrintReceiptBox prints the receipt inside a prominent box for the operator.
func (c *Config) PrintReceiptBox(w io.Writer) error {
	r, err := c.BuildReceipt()
	if err != nil {
		return err
	}
	line := strings.Repeat("=", 68)
	fmt.Fprintf(w, "\n%s\n", line)
	fmt.Fprintf(w, "  节点注册回执 —— 请完整复制以下一行内容，粘贴到面板\n")
	fmt.Fprintf(w, "  （公网IP=%s  管理端口=%s  frps端口=%d）\n", c.PublicIP, c.BindAddr, c.FrpsBindPort)
	fmt.Fprintf(w, "%s\n", line)
	fmt.Fprintf(w, "%s\n", r)
	fmt.Fprintf(w, "%s\n\n", line)
	return nil
}
