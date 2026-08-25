package panel

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frpanel/frpanel/internal/receipt"
	"github.com/frpanel/frpanel/internal/selfupdate"
)

// nodeDTO is the browser view of a node: DB fields (secrets hidden) plus live
// gauges from the ring buffer and derived counts.
type nodeDTO struct {
	Node
	Connected   bool    `json:"connected"`
	LatencyMs   int     `json:"latency_ms"`
	TargetCount int     `json:"target_count"`
	CPU         float64 `json:"cpu"`
	Mem         float64 `json:"mem"`
	RxBps       int64   `json:"rx_bps"`
	TxBps       int64   `json:"tx_bps"`
	TodayTunIn  int64   `json:"today_tun_in"`
	TodayTunOut int64   `json:"today_tun_out"`
}

func (a *App) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	nodes, err := a.store.ListNodes(r.Context())
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	counts, _ := a.store.TargetCounts(r.Context())
	today, _ := a.store.TodayTrafficByNode(r.Context())
	out := make([]nodeDTO, 0, len(nodes))
	for _, n := range nodes {
		d := nodeDTO{Node: n, TargetCount: counts[n.ID], LatencyMs: a.NodeLatency(n.ID)}
		if c := a.nodes.Conn(n.ID); c != nil {
			d.Connected = c.Connected()
		}
		if s, ok := a.pipe.LastSample(n.ID); ok {
			d.CPU, d.Mem, d.RxBps, d.TxBps = s.CPU, s.Mem, s.NetRxBps, s.NetTxBps
		}
		if t, ok := today[n.ID]; ok {
			d.TodayTunIn, d.TodayTunOut = t.TunInBytes, t.TunOutBytes
		}
		out = append(out, d)
	}
	ok(w, out)
}

type createNodeReq struct {
	Name    string `json:"name"`
	Region  string `json:"region"`
	Receipt string `json:"receipt"`
}

func (a *App) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	var req createNodeReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if req.Name == "" {
		failCode(w, CodeValidation, "请填写节点名称")
		return
	}
	rc, err := receipt.Decode(req.Receipt)
	if err != nil {
		failCode(w, CodeValidation, err.Error())
		return
	}
	if req.Region == "" {
		req.Region = "overseas"
	}
	// Verify reachability + pinning + token before persisting, so the operator
	// gets an immediate, specific result.
	if err := verifyAgent(r.Context(), rc.IP, rc.Port, rc.Token, rc.FP); err != nil {
		fail(w, err)
		return
	}
	node := &Node{
		Name: req.Name, IP: rc.IP, AgentPort: rc.Port, AgentToken: rc.Token,
		Fingerprint: rc.FP, FrpsPort: rc.FrpsPort, Region: req.Region,
	}
	id, err := a.store.CreateOrUpdateNode(r.Context(), node)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	node.ID = id
	a.nodes.Add(r.Context(), *node)
	nid := id
	a.AddLog("panel_op", currentUser(r), &nid, fmt.Sprintf("添加节点 %s (%s)", req.Name, rc.IP))
	ok(w, map[string]any{"id": id})
}

func (a *App) handleGetNode(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	n, err := a.store.GetNode(r.Context(), id)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	if n == nil {
		fail(w, ErrNotFound)
		return
	}
	d := nodeDTO{Node: *n, LatencyMs: a.NodeLatency(id)}
	if c := a.nodes.Conn(id); c != nil {
		d.Connected = c.Connected()
	}
	if s, ok := a.pipe.LastSample(id); ok {
		d.CPU, d.Mem, d.RxBps, d.TxBps = s.CPU, s.Mem, s.NetRxBps, s.NetTxBps
	}
	ok(w, d)
}

type updateNodeReq struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

func (a *App) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	var req updateNodeReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if req.Name == "" {
		failCode(w, CodeValidation, "节点名称不能为空")
		return
	}
	if err := a.store.UpdateNodeMeta(r.Context(), id, req.Name, req.Region); err != nil {
		fail(w, ErrDBDown)
		return
	}
	a.nodes.UpdateMeta(id, req.Name, req.Region)
	nid := id
	a.AddLog("panel_op", currentUser(r), &nid, "编辑节点信息")
	ok(w, nil)
}

func (a *App) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	n, _ := a.store.GetNode(r.Context(), id)
	a.nodes.Remove(id)
	if err := a.store.DeleteNode(r.Context(), id); err != nil {
		fail(w, ErrDBDown)
		return
	}
	name := ""
	if n != nil {
		name = n.Name
	}
	nid := id
	a.AddLog("panel_op", currentUser(r), &nid, "删除节点 "+name)
	ok(w, nil)
}

func (a *App) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.nodes.RotateFrpsToken(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	ok(w, nil)
}

// handleUpdateAgent tells one node's agent to self-update from the update
// source. The reply only confirms the download started; completion shows up as
// the node reconnecting with the new agent_version (failures land in the
// operation log via an agent_update_failed event).
func (a *App) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	base, mirror := a.updateSource(r.Context())
	if base == "" {
		failCode(w, CodeValidation, "未配置更新源（update_base_url）")
		return
	}
	// Best-effort target label for the agent's log; the agent installs whatever
	// the release currently is regardless.
	latest := ""
	vctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	if b, err := selfupdate.Fetch(vctx, base, mirror, "VERSION"); err == nil {
		latest = strings.TrimSpace(string(b))
	}
	cancel()
	if err := a.nodes.UpdateAgent(r.Context(), id, latest, base, mirror); err != nil {
		fail(w, err)
		return
	}
	nid := id
	a.AddLog("panel_op", currentUser(r), &nid, "下发 Agent 在线升级指令 "+latest)
	ok(w, map[string]any{"started": true, "target": latest})
}

func (a *App) handleNodeHistory(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, e := strconv.Atoi(h); e == nil && n > 0 && n <= 24*400 {
			hours = n
		}
	}
	pts, err := a.store.NodeHistory(r.Context(), id, hours)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	days := hours / 24
	if days < 1 {
		days = 7
	}
	traffic, _ := a.store.TrafficForNodeDays(r.Context(), id, days)
	ok(w, map[string]any{"points": pts, "traffic": traffic})
}

// currentUser returns the authed username for audit logs.
func currentUser(r *http.Request) string {
	if c := FromContext(r.Context()); c != nil {
		return c.Uname
	}
	return "system"
}
