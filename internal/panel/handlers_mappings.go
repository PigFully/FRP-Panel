package panel

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/frpanel/frpanel/internal/portutil"
)

type targetDTO struct {
	MappingTarget
	NodeName      string `json:"node_name"`
	NodeIP        string `json:"node_ip"`
	NodeRegion    string `json:"node_region"`
	NodeOnline    bool   `json:"node_online"`
	NodeLatencyMs int    `json:"node_latency_ms"`
	LiveStatus    string `json:"live_status"`
}

type mappingDTO struct {
	Mapping
	Targets []targetDTO `json:"targets"`
}

func (a *App) handleListMappings(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	mps, err := a.store.ListMappings(r.Context())
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	ids := make([]int64, 0, len(mps))
	for _, m := range mps {
		ids = append(ids, m.ID)
	}
	targets, err := a.store.ListTargetsForMappings(r.Context(), ids) // single IN() query
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	nodes, _ := a.store.ListNodes(r.Context())
	nodeByID := map[int64]Node{}
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	byMapping := map[int64][]targetDTO{}
	for _, t := range targets {
		n := nodeByID[t.NodeID]
		live := a.getLive(t.NodeID, t.RemotePort)
		if live == "" {
			live = t.TunnelStatus
		}
		online := false
		if c := a.nodes.Conn(t.NodeID); c != nil {
			online = c.Connected()
		}
		byMapping[t.MappingID] = append(byMapping[t.MappingID], targetDTO{
			MappingTarget: t, NodeName: n.Name, NodeIP: n.IP, NodeRegion: n.Region,
			NodeOnline: online, NodeLatencyMs: a.LinkLatency(t.NodeID, t.RemotePort), LiveStatus: live,
		})
	}
	out := make([]mappingDTO, 0, len(mps))
	for _, m := range mps {
		out = append(out, mappingDTO{Mapping: m, Targets: byMapping[m.ID]})
	}
	ok(w, out)
}

type mappingReq struct {
	LocalPort int             `json:"local_port"`
	Proto     string          `json:"proto"`
	Remark    string          `json:"remark"`
	Enabled   bool            `json:"enabled"`
	Version   int             `json:"version"`
	Targets   []mappingTarget `json:"targets"`
}

type mappingTarget struct {
	NodeID     int64 `json:"node_id"`
	RemotePort int   `json:"remote_port"`
}

// validateMapping performs range/reserved/duplicate checks and, when enabling,
// the local-port LISTEN precheck plus a best-effort remote occupancy check.
func (a *App) validateMapping(ctx context.Context, req *mappingReq) error {
	if err := portutil.ValidatePort(req.LocalPort); err != nil {
		return Err(CodeValidation, "本地端口："+err.Error())
	}
	if req.Proto != "tcp" && req.Proto != "udp" {
		return Err(CodeValidation, "协议必须为 tcp 或 udp")
	}
	if len(req.Targets) == 0 {
		return Err(CodeValidation, "请至少添加一个目标节点")
	}
	seen := map[int64]bool{}
	for _, t := range req.Targets {
		if seen[t.NodeID] {
			return Err(CodeValidation, "同一映射内不允许重复选择同一节点")
		}
		seen[t.NodeID] = true
		if err := portutil.ValidateRemotePort(t.RemotePort); err != nil {
			if portutil.IsReserved(t.RemotePort) {
				return Err(CodeReservedPort, err.Error())
			}
			return Err(CodeValidation, "公网端口："+err.Error())
		}
	}
	if req.Enabled {
		if listening, _ := portutil.LocalListen(req.LocalPort, req.Proto); !listening {
			return Err(CodeLocalNoListen, fmt.Sprintf("本地端口 %d 当前没有任何程序监听，请先启动对应服务", req.LocalPort))
		}
		// Best-effort remote occupancy check for connected nodes.
		for _, t := range req.Targets {
			if c := a.nodes.Conn(t.NodeID); c != nil && c.Connected() {
				if res, err := a.nodes.PortCheck(ctx, t.NodeID, t.RemotePort, req.Proto); err == nil && !res.Available {
					return Err(CodePortBusy, fmt.Sprintf("节点公网端口 %d 已被占用（%s）", t.RemotePort, res.Process))
				}
			}
		}
	}
	return nil
}

func toTargetInputs(ts []mappingTarget) []TargetInput {
	out := make([]TargetInput, 0, len(ts))
	for _, t := range ts {
		out = append(out, TargetInput{NodeID: t.NodeID, RemotePort: t.RemotePort})
	}
	return out
}

func (a *App) handleCreateMapping(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	var req mappingReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := a.validateMapping(r.Context(), &req); err != nil {
		fail(w, err)
		return
	}
	m := &Mapping{LocalPort: req.LocalPort, Proto: req.Proto, Remark: req.Remark, Enabled: req.Enabled}
	id, err := a.store.CreateMapping(r.Context(), m, toTargetInputs(req.Targets))
	if err != nil {
		fail(w, mapMappingErr(err))
		return
	}
	a.nodes.SyncAll(r.Context())
	a.AddLog("panel_op", currentUser(r), nil, fmt.Sprintf("新增映射：本地端口 %d -> %d 个目标", req.LocalPort, len(req.Targets)))
	ok(w, map[string]any{"id": id})
}

func (a *App) handleUpdateMapping(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	var req mappingReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := a.validateMapping(r.Context(), &req); err != nil {
		fail(w, err)
		return
	}
	m := &Mapping{LocalPort: req.LocalPort, Proto: req.Proto, Remark: req.Remark, Enabled: req.Enabled}
	err = a.store.UpdateMapping(r.Context(), id, req.Version, m, toTargetInputs(req.Targets))
	if err == ErrOptimisticConflict {
		failCode(w, CodeConflict, "配置已被其他人修改，请刷新后重试")
		return
	}
	if err != nil {
		fail(w, mapMappingErr(err))
		return
	}
	a.nodes.SyncAll(r.Context())
	a.AddLog("panel_op", currentUser(r), nil, fmt.Sprintf("编辑映射 #%d", id))
	ok(w, nil)
}

func (a *App) handleDeleteMapping(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.store.DeleteMapping(r.Context(), id); err != nil {
		fail(w, ErrDBDown)
		return
	}
	a.nodes.SyncAll(r.Context())
	a.AddLog("panel_op", currentUser(r), nil, fmt.Sprintf("删除映射 #%d", id))
	ok(w, nil)
}

type toggleReq struct {
	Enabled bool `json:"enabled"`
	Version int  `json:"version"`
}

func (a *App) handleToggleMapping(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, err)
		return
	}
	var req toggleReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	m, err := a.store.GetMapping(r.Context(), id)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	if m == nil {
		fail(w, ErrNotFound)
		return
	}
	if req.Enabled {
		if listening, _ := portutil.LocalListen(m.LocalPort, m.Proto); !listening {
			failCode(w, CodeLocalNoListen, fmt.Sprintf("本地端口 %d 当前没有任何程序监听，请先启动对应服务", m.LocalPort))
			return
		}
	}
	if err := a.store.SetMappingEnabled(r.Context(), id, req.Version, req.Enabled); err != nil {
		if err == ErrOptimisticConflict {
			failCode(w, CodeConflict, "配置已被其他人修改，请刷新后重试")
			return
		}
		fail(w, ErrDBDown)
		return
	}
	a.nodes.SyncAll(r.Context())
	state := "停用"
	if req.Enabled {
		state = "启用"
	}
	a.AddLog("panel_op", currentUser(r), nil, fmt.Sprintf("%s映射 #%d", state, id))
	ok(w, nil)
}

type portCheckReq struct {
	NodeID int64  `json:"node_id"`
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
}

func (a *App) handlePortCheck(w http.ResponseWriter, r *http.Request) {
	var req portCheckReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if reason := portutil.ReservedReason(req.Port); reason != "" {
		ok(w, map[string]any{"available": false, "reason": "reserved", "process": reason})
		return
	}
	res, err := a.nodes.PortCheck(r.Context(), req.NodeID, req.Port, req.Proto)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

type localCheckReq struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

func (a *App) handleLocalCheck(w http.ResponseWriter, r *http.Request) {
	var req localCheckReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	listening, proc := portutil.LocalListen(req.Port, req.Proto)
	ok(w, map[string]any{"listening": listening, "process": proc})
}

// mapMappingErr translates DB unique-constraint violations into friendly codes.
func mapMappingErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "uq_mt_node_port") || strings.Contains(msg, "Duplicate") {
		return Err(CodePortBusy, "该节点上的公网端口已被其他映射占用")
	}
	return ErrDBDown
}
