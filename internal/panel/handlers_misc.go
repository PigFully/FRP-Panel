package panel

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frpanel/frpanel/internal/selfupdate"
	"github.com/frpanel/frpanel/internal/version"
)

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	ctx := r.Context()
	nodes, err := a.store.ListNodes(ctx)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	online := 0
	var rxBps, txBps, tunIn, tunOut int64
	nameByID := map[int64]string{}
	for _, n := range nodes {
		nameByID[n.ID] = n.Name
		if c := a.nodes.Conn(n.ID); c != nil && c.Connected() {
			online++
		}
		if s, ok := a.pipe.LastSample(n.ID); ok {
			rxBps += s.NetRxBps
			txBps += s.NetTxBps
			tunIn += s.TunInBps
			tunOut += s.TunOutBps
		}
	}
	mps, _ := a.store.ListMappings(ctx)
	enabled := 0
	for _, m := range mps {
		if m.Enabled {
			enabled++
		}
	}
	today, last30, _ := a.store.TrafficTotals(ctx)
	top, _ := a.store.TrafficTopN(ctx, 30, 5)
	type topDTO struct {
		NodeID   int64  `json:"node_id"`
		NodeName string `json:"node_name"`
		TunIn    int64  `json:"tun_in"`
		TunOut   int64  `json:"tun_out"`
		NodeRx   int64  `json:"node_rx"`
		NodeTx   int64  `json:"node_tx"`
	}
	tops := make([]topDTO, 0, len(top))
	for _, t := range top {
		tops = append(tops, topDTO{NodeID: t.NodeID, NodeName: nameByID[t.NodeID], TunIn: t.TunIn, TunOut: t.TunOut, NodeRx: t.NodeRx, NodeTx: t.NodeTx})
	}
	logs, _, _ := a.store.ListLogs(ctx, LogFilter{Page: 1, Size: 10})

	ok(w, map[string]any{
		"stats": map[string]any{
			"node_total":      len(nodes),
			"node_online":     online,
			"mapping_total":   len(mps),
			"mapping_enabled": enabled,
		},
		"live": map[string]any{ // two scopes kept separate (node NIC vs tunnel)
			"node_rx_bps": rxBps, "node_tx_bps": txBps,
			"tun_in_bps": tunIn, "tun_out_bps": tunOut,
		},
		"traffic_today":  today,
		"traffic_last30": last30,
		"top_nodes":      tops,
		"recent_logs":    logs,
	})
}

func (a *App) handleListLogs(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	q := r.URL.Query()
	f := LogFilter{Type: q.Get("type")}
	if nid := q.Get("node_id"); nid != "" {
		if id, err := strconv.ParseInt(nid, 10, 64); err == nil {
			f.NodeID = &id
		}
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Size, _ = strconv.Atoi(q.Get("size"))
	logs, total, err := a.store.ListLogs(r.Context(), f)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	ok(w, map[string]any{"items": logs, "total": total, "page": f.Page, "size": f.Size})
}

type cleanLogsReq struct {
	All bool `json:"all"`
}

func (a *App) handleCleanLogs(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	var req cleanLogsReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	n, err := a.store.CleanLogs(r.Context(), req.All)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	scope := "30 天前的"
	if req.All {
		scope = "全部"
	}
	a.AddLog("panel_op", currentUser(r), nil, fmt.Sprintf("清理%s操作日志（%d 条）", scope, n))
	ok(w, map[string]any{"deleted": n})
}

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	autoBackup, _ := a.store.GetSetting(r.Context(), "auto_backup")
	base, mirror := a.updateSource(r.Context())
	ok(w, map[string]any{
		"panel_name":         a.PanelName(),
		"conn_rate_limit":    a.connRateLimit(),
		"tcp_ping_interval":  a.pingIntervalSec(),
		"auto_backup":        autoBackup == "1",
		"version":            version.Version,
		"tls_enabled":        a.cfg.TLS.Enabled,
		"update_base":        base,
		"update_mirror":      mirror,
	})
}

type updateSettingsReq struct {
	PanelName       *string `json:"panel_name"`
	ConnRateLimit   *int    `json:"conn_rate_limit"`
	TCPPingInterval *int    `json:"tcp_ping_interval"`
	AutoBackup      *bool   `json:"auto_backup"`
	UpdateMirror    *string `json:"update_mirror"`
}

func (a *App) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	var req updateSettingsReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	ctx := r.Context()
	if req.PanelName != nil {
		name := strings.TrimSpace(*req.PanelName)
		if name == "" || len(name) > 64 {
			failCode(w, CodeValidation, "面板名称长度需为 1-64 字符")
			return
		}
		if err := a.store.SetSetting(ctx, "panel_name", name); err != nil {
			fail(w, ErrDBDown)
			return
		}
		a.setPanelName(name)
	}
	if req.ConnRateLimit != nil {
		if *req.ConnRateLimit < 0 || *req.ConnRateLimit > 1000000 {
			failCode(w, CodeValidation, "最大回源数需为 0-1000000（0 表示不限制）")
			return
		}
		if err := a.store.SetSetting(ctx, "conn_rate_limit", strconv.Itoa(*req.ConnRateLimit)); err != nil {
			fail(w, ErrDBDown)
			return
		}
		a.nodes.PushRateLimitAll(ctx) // re-program nftables on every connected node
	}
	if req.TCPPingInterval != nil {
		if *req.TCPPingInterval < 5 || *req.TCPPingInterval > 3600 {
			failCode(w, CodeValidation, "TCP 延迟刷新间隔需为 5-3600 秒")
			return
		}
		if err := a.store.SetSetting(ctx, "tcp_ping_interval", strconv.Itoa(*req.TCPPingInterval)); err != nil {
			fail(w, ErrDBDown)
			return
		}
	}
	if req.AutoBackup != nil {
		v := "0"
		if *req.AutoBackup {
			v = "1"
		}
		_ = a.store.SetSetting(ctx, "auto_backup", v)
	}
	if req.UpdateMirror != nil {
		m := strings.TrimSpace(*req.UpdateMirror)
		if m != "" && !strings.HasPrefix(m, "https://") && !strings.HasPrefix(m, "http://") {
			failCode(w, CodeValidation, "镜像前缀需为 http(s):// 开头的地址，或留空关闭镜像")
			return
		}
		if err := a.store.SetSetting(ctx, "update_mirror", m); err != nil {
			fail(w, ErrDBDown)
			return
		}
	}
	a.AddLog("panel_op", currentUser(r), nil, "修改面板设置")
	ok(w, nil)
}

func (a *App) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	base, mirror := a.updateSource(r.Context())
	if base == "" {
		ok(w, map[string]any{"current": version.Version, "latest": version.Version, "has_update": false, "message": "未配置更新源"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	b, err := selfupdate.Fetch(ctx, base, mirror, "VERSION")
	if err != nil {
		ok(w, map[string]any{"current": version.Version, "latest": "", "has_update": false, "message": "无法访问更新服务器"})
		return
	}
	latest := strings.TrimSpace(string(b))
	if len(latest) > 64 {
		latest = latest[:64]
	}
	ok(w, map[string]any{
		"current":     version.Version,
		"latest":      latest,
		"has_update":  latest != "" && latest != version.Version,
		"upgrade_cmd": "curl -fsSL " + base + "/install-panel.sh | bash -s -- --upgrade",
	})
}

// handleSelfUpdate downloads the latest panel binary from the update source,
// verifies it against sha256sums.txt, atomically swaps the executable and
// schedules a graceful restart (systemd Restart=always respawns the new
// build). frpc children are respawned with the panel, so tunnels blip for a
// few seconds exactly as on any panel restart; the browser reconnects on its
// own. The 4-minute budget runs on a background context so a dropped browser
// connection cannot abort the download mid-way.
func (a *App) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	base, mirror := a.updateSource(r.Context())
	if base == "" {
		failCode(w, CodeValidation, "未配置更新源（update_base_url）")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fail(w, Err(CodeInternal, "定位面板二进制失败: "+err.Error()))
		return
	}
	user := currentUser(r)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	asset := "frpanel-panel-" + runtime.GOARCH
	if err := selfupdate.Run(ctx, base, mirror, asset, exe); err != nil {
		a.AddLog("panel_op", user, nil, "面板在线升级失败: "+err.Error())
		fail(w, Err(CodeInternal, "在线升级失败: "+err.Error()))
		return
	}
	a.AddLog("panel_op", user, nil, "面板在线升级：新二进制已就位，服务即将重启")
	ok(w, map[string]any{"restarting": true})
	if a.restartFn != nil {
		time.AfterFunc(800*time.Millisecond, a.restartFn)
	}
}

func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	dir := filepath.Join(a.cfg.DataDir, "backups")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fail(w, Err(CodeInternal, "无法创建备份目录"))
		return
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	file := filepath.Join(dir, "frpanel-"+stamp+".sql")
	if err := a.mysqldump(r.Context(), file); err != nil {
		a.log.Error("backup failed", "err", err)
		fail(w, Err(CodeInternal, "备份失败："+err.Error()))
		return
	}
	pruneBackups(dir, 7)
	fi, _ := os.Stat(file)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	a.AddLog("panel_op", currentUser(r), nil, "执行数据库备份 "+filepath.Base(file))
	ok(w, map[string]any{"file": filepath.Base(file), "size": size})
}

func (a *App) mysqldump(ctx context.Context, outFile string) error {
	m := a.cfg.MySQL
	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()
	// Pass credentials via a 0600 defaults-extra-file rather than --password= on
	// the command line, which is visible to any local user through `ps`/`/proc`.
	credFile, err := os.CreateTemp(a.cfg.DataDir, ".my.cnf-*")
	if err != nil {
		return err
	}
	defer os.Remove(credFile.Name())
	if _, err := fmt.Fprintf(credFile, "[client]\nuser=%s\npassword=%s\nhost=%s\nport=%d\n", m.User, m.Password, m.Host, m.Port); err != nil {
		credFile.Close()
		return err
	}
	credFile.Close()
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "mysqldump",
		"--defaults-extra-file="+credFile.Name(),
		"--single-transaction", "--quick", m.Database)
	cmd.Stdout = f
	var errb strings.Builder
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

func pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "frpanel-") && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // timestamped names sort chronologically
	for i := 0; i < len(files)-keep; i++ {
		_ = os.Remove(filepath.Join(dir, files[i]))
	}
}
