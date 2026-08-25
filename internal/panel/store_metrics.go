package panel

import (
	"context"
	"time"
)

// MinuteRow is one node's aggregated resource sample for a minute bucket.
type MinuteRow struct {
	NodeID int64
	TS     time.Time
	CPUAvg float64
	MemAvg float64
	RxPeak int64
	TxPeak int64
}

// BatchUpsertMinutely writes a batch of minute aggregates in one transaction,
// grouped so multiple nodes' rows commit together (spec §6.5 layer 2).
func (s *Store) BatchUpsertMinutely(ctx context.Context, rows []MinuteRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO metrics_minutely (node_id, ts, cpu_avg, mem_avg, rx_peak_bps, tx_peak_bps)
		VALUES (?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE cpu_avg=VALUES(cpu_avg), mem_avg=VALUES(mem_avg), rx_peak_bps=VALUES(rx_peak_bps), tx_peak_bps=VALUES(tx_peak_bps)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.NodeID, r.TS, r.CPUAvg, r.MemAvg, r.RxPeak, r.TxPeak); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IncrTrafficDaily accumulates byte deltas into a node's Asia/Shanghai day row.
func (s *Store) IncrTrafficDaily(ctx context.Context, nodeID int64, day string, nodeRx, nodeTx, tunIn, tunOut int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO traffic_daily (node_id, day, node_rx_bytes, node_tx_bytes, tun_in_bytes, tun_out_bytes)
		VALUES (?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE node_rx_bytes=node_rx_bytes+VALUES(node_rx_bytes), node_tx_bytes=node_tx_bytes+VALUES(node_tx_bytes),
			tun_in_bytes=tun_in_bytes+VALUES(tun_in_bytes), tun_out_bytes=tun_out_bytes+VALUES(tun_out_bytes)`,
		nodeID, day, nodeRx, nodeTx, tunIn, tunOut)
	return err
}

// RollupHourly aggregates recent minutely rows into hourly buckets (idempotent).
func (s *Store) RollupHourly(ctx context.Context) error {
	since := time.Now().UTC().Add(-3 * time.Hour)
	_, err := s.db.ExecContext(ctx, `INSERT INTO metrics_hourly (node_id, ts, cpu_avg, mem_avg, rx_peak_bps, tx_peak_bps)
		SELECT node_id, DATE_FORMAT(ts, '%Y-%m-%d %H:00:00') AS h, AVG(cpu_avg), AVG(mem_avg), MAX(rx_peak_bps), MAX(tx_peak_bps)
		FROM metrics_minutely WHERE ts >= ? GROUP BY node_id, h
		ON DUPLICATE KEY UPDATE cpu_avg=VALUES(cpu_avg), mem_avg=VALUES(mem_avg), rx_peak_bps=VALUES(rx_peak_bps), tx_peak_bps=VALUES(tx_peak_bps)`, since)
	return err
}

// RollupDaily aggregates recent hourly rows into daily buckets (idempotent).
func (s *Store) RollupDaily(ctx context.Context) error {
	since := time.Now().UTC().Add(-48 * time.Hour)
	_, err := s.db.ExecContext(ctx, `INSERT INTO metrics_daily (node_id, ts, cpu_avg, mem_avg, rx_peak_bps, tx_peak_bps)
		SELECT node_id, DATE_FORMAT(ts, '%Y-%m-%d 00:00:00') AS d, AVG(cpu_avg), AVG(mem_avg), MAX(rx_peak_bps), MAX(tx_peak_bps)
		FROM metrics_hourly WHERE ts >= ? GROUP BY node_id, d
		ON DUPLICATE KEY UPDATE cpu_avg=VALUES(cpu_avg), mem_avg=VALUES(mem_avg), rx_peak_bps=VALUES(rx_peak_bps), tx_peak_bps=VALUES(tx_peak_bps)`, since)
	return err
}

// CleanupMetrics enforces retention: minutely 30 days, hourly 1 year.
func (s *Store) CleanupMetrics(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM metrics_minutely WHERE ts < ?", time.Now().UTC().AddDate(0, 0, -30)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM metrics_hourly WHERE ts < ?", time.Now().UTC().AddDate(-1, 0, 0))
	return err
}

// MetricPoint is one row from a rollup table for history charts.
type MetricPoint struct {
	TS     time.Time `db:"ts" json:"ts"`
	CPUAvg float64   `db:"cpu_avg" json:"cpu_avg"`
	MemAvg float64   `db:"mem_avg" json:"mem_avg"`
	RxPeak int64     `db:"rx_peak_bps" json:"rx_peak_bps"`
	TxPeak int64     `db:"tx_peak_bps" json:"tx_peak_bps"`
}

// NodeHistory returns rollup points for a node. hours<=48 reads hourly, else
// daily. History queries only ever hit rollup tables (spec §6.5).
func (s *Store) NodeHistory(ctx context.Context, nodeID int64, hours int) ([]MetricPoint, error) {
	var pts []MetricPoint
	if hours <= 48 {
		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		err := s.db.SelectContext(ctx, &pts,
			"SELECT ts, cpu_avg, mem_avg, rx_peak_bps, tx_peak_bps FROM metrics_hourly WHERE node_id=? AND ts>=? ORDER BY ts", nodeID, since)
		return pts, err
	}
	days := hours / 24
	since := time.Now().UTC().AddDate(0, 0, -days)
	err := s.db.SelectContext(ctx, &pts,
		"SELECT ts, cpu_avg, mem_avg, rx_peak_bps, tx_peak_bps FROM metrics_daily WHERE node_id=? AND ts>=? ORDER BY ts", nodeID, since)
	return pts, err
}

// TrafficTop is a per-node traffic total over a window.
type TrafficTop struct {
	NodeID   int64 `db:"node_id" json:"node_id"`
	NodeRx   int64 `db:"node_rx" json:"node_rx"`
	NodeTx   int64 `db:"node_tx" json:"node_tx"`
	TunIn    int64 `db:"tun_in" json:"tun_in"`
	TunOut   int64 `db:"tun_out" json:"tun_out"`
}

// TrafficTopN returns per-node traffic totals over the last N Asia/Shanghai
// days from the daily rollup table (never scans minutely).
func (s *Store) TrafficTopN(ctx context.Context, days, limit int) ([]TrafficTop, error) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	from := time.Now().In(loc).AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var out []TrafficTop
	err := s.db.SelectContext(ctx, &out, `SELECT node_id,
		SUM(node_rx_bytes) AS node_rx, SUM(node_tx_bytes) AS node_tx,
		SUM(tun_in_bytes) AS tun_in, SUM(tun_out_bytes) AS tun_out
		FROM traffic_daily WHERE day >= ? GROUP BY node_id ORDER BY (tun_in+tun_out) DESC LIMIT ?`, from, limit)
	return out, err
}

// TrafficForNodeDays returns a node's daily traffic rows for the last N days.
func (s *Store) TrafficForNodeDays(ctx context.Context, nodeID int64, days int) ([]TrafficDaily, error) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	from := time.Now().In(loc).AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var out []TrafficDaily
	err := s.db.SelectContext(ctx, &out, "SELECT * FROM traffic_daily WHERE node_id=? AND day>=? ORDER BY day", nodeID, from)
	return out, err
}

// TrafficTotals returns aggregate today + last-N-day traffic across all nodes.
func (s *Store) TrafficTotals(ctx context.Context) (today, last30 TrafficTop, err error) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	todayStr := time.Now().In(loc).Format("2006-01-02")
	from30 := time.Now().In(loc).AddDate(0, 0, -29).Format("2006-01-02")
	err = s.db.GetContext(ctx, &today, `SELECT COALESCE(SUM(node_rx_bytes),0) node_rx, COALESCE(SUM(node_tx_bytes),0) node_tx,
		COALESCE(SUM(tun_in_bytes),0) tun_in, COALESCE(SUM(tun_out_bytes),0) tun_out FROM traffic_daily WHERE day=?`, todayStr)
	if err != nil {
		return
	}
	err = s.db.GetContext(ctx, &last30, `SELECT COALESCE(SUM(node_rx_bytes),0) node_rx, COALESCE(SUM(node_tx_bytes),0) node_tx,
		COALESCE(SUM(tun_in_bytes),0) tun_in, COALESCE(SUM(tun_out_bytes),0) tun_out FROM traffic_daily WHERE day>=?`, from30)
	return
}

// TodayTrafficByNode returns today's (Asia/Shanghai) traffic row per node, in a
// single query, keyed by node_id.
func (s *Store) TodayTrafficByNode(ctx context.Context) (map[int64]TrafficDaily, error) {
	today := ShanghaiDay(time.Now())
	var rows []TrafficDaily
	if err := s.db.SelectContext(ctx, &rows, "SELECT * FROM traffic_daily WHERE day=?", today); err != nil {
		return nil, err
	}
	out := make(map[int64]TrafficDaily, len(rows))
	for _, r := range rows {
		out[r.NodeID] = r
	}
	return out, nil
}

// ShanghaiDay returns the YYYY-MM-DD day string for t in Asia/Shanghai.
func ShanghaiDay(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return t.In(loc).Format("2006-01-02")
}
