package panel

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// Store is the data-access layer. All statements are parameterized; list
// endpoints batch child rows with IN(...) to avoid N+1.
type Store struct{ db *sqlx.DB }

// NewStore wraps a DB handle.
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// DB exposes the underlying handle (for pings / tx in the pipeline).
func (s *Store) DB() *sqlx.DB { return s.db }

func now() time.Time { return time.Now().UTC() }

// ----- users -----

func (s *Store) GetUserByUsername(ctx context.Context, name string) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u, "SELECT * FROM users WHERE username=?", name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &u, err
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u, "SELECT * FROM users WHERE id=?", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &u, err
}

func (s *Store) CreateUser(ctx context.Context, name, hash string) (int64, error) {
	t := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, pwd_version, created_at, updated_at)
		 VALUES (?,?,1,?,?)
		 ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`, name, hash, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePassword sets a new hash and bumps pwd_version (invalidating old JWTs).
func (s *Store) UpdatePassword(ctx context.Context, id int64, hash string) (int, error) {
	t := now()
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET password_hash=?, pwd_version=pwd_version+1, updated_at=? WHERE id=?", hash, t, id)
	if err != nil {
		return 0, err
	}
	var v int
	err = s.db.GetContext(ctx, &v, "SELECT pwd_version FROM users WHERE id=?", id)
	return v, err
}

// ----- nodes -----

func (s *Store) CreateOrUpdateNode(ctx context.Context, n *Node) (int64, error) {
	t := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO nodes
		(name, ip, agent_port, agent_token, fingerprint, frps_token, frps_port, region, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?, 'offline', ?, ?)
		ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id), name=VALUES(name), agent_token=VALUES(agent_token),
			fingerprint=VALUES(fingerprint), region=VALUES(region), frps_port=VALUES(frps_port), updated_at=VALUES(updated_at)`,
		n.Name, n.IP, n.AgentPort, n.AgentToken, n.Fingerprint, n.FrpsToken, n.FrpsPort, n.Region, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetNode(ctx context.Context, id int64) (*Node, error) {
	var n Node
	err := s.db.GetContext(ctx, &n, "SELECT * FROM nodes WHERE id=?", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &n, err
}

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	var ns []Node
	err := s.db.SelectContext(ctx, &ns, "SELECT * FROM nodes ORDER BY id")
	return ns, err
}

func (s *Store) UpdateNodeStatus(ctx context.Context, id int64, status, agentVer, frpsVer string) error {
	t := now()
	_, err := s.db.ExecContext(ctx,
		"UPDATE nodes SET status=?, agent_version=?, frps_version=?, last_seen=?, updated_at=? WHERE id=?",
		status, agentVer, frpsVer, t, t, id)
	return err
}

func (s *Store) SetNodeOffline(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE nodes SET status='offline', updated_at=? WHERE id=?", now(), id)
	return err
}

func (s *Store) UpdateNodeFrps(ctx context.Context, id int64, frpsToken string, frpsPort int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE nodes SET frps_token=?, frps_port=?, updated_at=? WHERE id=?", frpsToken, frpsPort, now(), id)
	return err
}

func (s *Store) UpdateNodeMeta(ctx context.Context, id int64, name, region string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE nodes SET name=?, region=?, updated_at=? WHERE id=?", name, region, now(), id)
	return err
}

func (s *Store) SetNodeLastCommitSeq(ctx context.Context, id, seq int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE nodes SET last_commit_seq=? WHERE id=? AND last_commit_seq<?", seq, id, seq)
	return err
}

func (s *Store) DeleteNode(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM mapping_targets WHERE node_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM metrics_minutely WHERE node_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM metrics_hourly WHERE node_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM metrics_daily WHERE node_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM traffic_daily WHERE node_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// TargetCounts returns the number of mapping targets per node in one query.
func (s *Store) TargetCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryxContext(ctx, "SELECT node_id, COUNT(*) AS c FROM mapping_targets GROUP BY node_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var c int
		if err := rows.Scan(&id, &c); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ----- mappings + targets -----

func (s *Store) ListMappings(ctx context.Context) ([]Mapping, error) {
	var ms []Mapping
	err := s.db.SelectContext(ctx, &ms, "SELECT * FROM mappings ORDER BY id DESC")
	return ms, err
}

func (s *Store) GetMapping(ctx context.Context, id int64) (*Mapping, error) {
	var m Mapping
	err := s.db.GetContext(ctx, &m, "SELECT * FROM mappings WHERE id=?", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

// ListTargetsForMappings batch-loads all targets for the given mapping ids in a
// single query (avoids N+1 in the mapping list endpoint).
func (s *Store) ListTargetsForMappings(ctx context.Context, ids []int64) ([]MappingTarget, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q, args, err := sqlx.In("SELECT * FROM mapping_targets WHERE mapping_id IN (?) ORDER BY id", ids)
	if err != nil {
		return nil, err
	}
	q = s.db.Rebind(q)
	var ts []MappingTarget
	err = s.db.SelectContext(ctx, &ts, q, args...)
	return ts, err
}

func (s *Store) ListTargetsByMapping(ctx context.Context, mappingID int64) ([]MappingTarget, error) {
	var ts []MappingTarget
	err := s.db.SelectContext(ctx, &ts, "SELECT * FROM mapping_targets WHERE mapping_id=? ORDER BY id", mappingID)
	return ts, err
}

func (s *Store) ListTargetsByNode(ctx context.Context, nodeID int64) ([]MappingTarget, error) {
	var ts []MappingTarget
	err := s.db.SelectContext(ctx, &ts, "SELECT * FROM mapping_targets WHERE node_id=? ORDER BY id", nodeID)
	return ts, err
}

// TargetInput is one desired target when creating/updating a mapping.
type TargetInput struct {
	NodeID     int64
	RemotePort int
}

// CreateMapping inserts a mapping and its targets in one transaction.
func (s *Store) CreateMapping(ctx context.Context, m *Mapping, targets []TargetInput) (int64, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.ExecContext(ctx,
		"INSERT INTO mappings (local_port, proto, remark, enabled, version, created_at, updated_at) VALUES (?,?,?,?,1,?,?)",
		m.LocalPort, m.Proto, m.Remark, m.Enabled, t, t)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	for _, tg := range targets {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO mapping_targets (mapping_id, node_id, remote_port, tunnel_status, created_at) VALUES (?,?,?, 'pending', ?)",
			id, tg.NodeID, tg.RemotePort, t); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// ErrOptimisticConflict indicates the mapping was modified concurrently.
var ErrOptimisticConflict = errors.New("optimistic lock conflict")

// UpdateMapping replaces a mapping's fields and target set atomically, guarded
// by the optimistic-lock version. Returns ErrOptimisticConflict on mismatch.
func (s *Store) UpdateMapping(ctx context.Context, id int64, expectVersion int, m *Mapping, targets []TargetInput) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.ExecContext(ctx,
		"UPDATE mappings SET local_port=?, proto=?, remark=?, enabled=?, version=version+1, updated_at=? WHERE id=? AND version=?",
		m.LocalPort, m.Proto, m.Remark, m.Enabled, t, id, expectVersion)
	if err != nil {
		return err
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		return ErrOptimisticConflict
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mapping_targets WHERE mapping_id=?", id); err != nil {
		return err
	}
	for _, tg := range targets {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO mapping_targets (mapping_id, node_id, remote_port, tunnel_status, created_at) VALUES (?,?,?, 'pending', ?)",
			id, tg.NodeID, tg.RemotePort, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetMappingEnabled toggles enable state (bumps version), guarded by version.
func (s *Store) SetMappingEnabled(ctx context.Context, id int64, expectVersion int, enabled bool) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE mappings SET enabled=?, version=version+1, updated_at=? WHERE id=? AND version=?",
		enabled, now(), id, expectVersion)
	if err != nil {
		return err
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		return ErrOptimisticConflict
	}
	return nil
}

func (s *Store) DeleteMapping(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM mapping_targets WHERE mapping_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mappings WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateTargetStatus(ctx context.Context, mappingID, nodeID int64, status, detail string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE mapping_targets SET tunnel_status=?, status_detail=? WHERE mapping_id=? AND node_id=?",
		status, detail, mappingID, nodeID)
	return err
}

// UpdateTargetStatusByPort sets live tunnel status for all targets matching a
// (node, remote_port) pair.
func (s *Store) UpdateTargetStatusByPort(ctx context.Context, nodeID int64, remotePort int, status string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE mapping_targets SET tunnel_status=? WHERE node_id=? AND remote_port=?",
		status, nodeID, remotePort)
	return err
}

// NodeProxyRow is a desired proxy for a node (enabled mapping target), joined.
type NodeProxyRow struct {
	MappingID  int64  `db:"mapping_id"`
	NodeID     int64  `db:"node_id"`
	LocalPort  int    `db:"local_port"`
	Proto      string `db:"proto"`
	RemotePort int    `db:"remote_port"`
	Remark     string `db:"remark"`
}

// ProbeTarget is an enabled tunnel endpoint to latency-probe.
type ProbeTarget struct {
	NodeID     int64  `db:"node_id"`
	IP         string `db:"ip"`
	RemotePort int    `db:"remote_port"`
}

// EnabledTargetsForProbe returns all enabled (node, public-port) endpoints so
// the panel can measure the real FRP-link latency to each.
func (s *Store) EnabledTargetsForProbe(ctx context.Context) ([]ProbeTarget, error) {
	var rows []ProbeTarget
	err := s.db.SelectContext(ctx, &rows, `SELECT t.node_id, n.ip, t.remote_port
		FROM mapping_targets t JOIN mappings m ON m.id = t.mapping_id JOIN nodes n ON n.id = t.node_id
		WHERE m.enabled = 1`)
	return rows, err
}

// EnabledProxiesForNode returns the desired proxies for a node — one JOIN, no
// N+1 — used by frpc config generation and reconciliation.
func (s *Store) EnabledProxiesForNode(ctx context.Context, nodeID int64) ([]NodeProxyRow, error) {
	var rows []NodeProxyRow
	err := s.db.SelectContext(ctx, &rows, `SELECT m.id AS mapping_id, t.node_id, m.local_port, m.proto, t.remote_port, m.remark
		FROM mapping_targets t JOIN mappings m ON m.id = t.mapping_id
		WHERE t.node_id=? AND m.enabled=1 ORDER BY m.id`, nodeID)
	return rows, err
}

// ----- logs -----

func (s *Store) AddLog(ctx context.Context, typ, source string, nodeID *int64, detail string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO operation_logs (type, source, node_id, detail, created_at) VALUES (?,?,?,?,?)",
		typ, source, nodeID, detail, now())
	return err
}

// LogFilter parameterizes the paginated log query.
type LogFilter struct {
	Type   string
	NodeID *int64
	Since  *time.Time
	Until  *time.Time
	Page   int
	Size   int
}

func (s *Store) ListLogs(ctx context.Context, f LogFilter) ([]OperationLog, int, error) {
	where := "WHERE 1=1"
	var args []any
	if f.Type != "" {
		where += " AND type=?"
		args = append(args, f.Type)
	}
	if f.NodeID != nil {
		where += " AND node_id=?"
		args = append(args, *f.NodeID)
	}
	if f.Since != nil {
		where += " AND created_at>=?"
		args = append(args, *f.Since)
	}
	if f.Until != nil {
		where += " AND created_at<=?"
		args = append(args, *f.Until)
	}
	var total int
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM operation_logs "+where, args...); err != nil {
		return nil, 0, err
	}
	if f.Size <= 0 {
		f.Size = 20
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	offset := (f.Page - 1) * f.Size
	q := "SELECT * FROM operation_logs " + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, f.Size, offset)
	var logs []OperationLog
	if err := s.db.SelectContext(ctx, &logs, q, args...); err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func (s *Store) CleanLogs(ctx context.Context, all bool) (int64, error) {
	var res sql.Result
	var err error
	if all {
		res, err = s.db.ExecContext(ctx, "DELETE FROM operation_logs")
	} else {
		res, err = s.db.ExecContext(ctx, "DELETE FROM operation_logs WHERE created_at < ?", now().AddDate(0, 0, -30))
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ----- settings -----

func (s *Store) GetSetting(ctx context.Context, k string) (string, error) {
	var v string
	err := s.db.GetContext(ctx, &v, "SELECT v FROM settings WHERE k=?", k)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (k, v) VALUES (?, ?) ON DUPLICATE KEY UPDATE v=VALUES(v)", k, v)
	return err
}
