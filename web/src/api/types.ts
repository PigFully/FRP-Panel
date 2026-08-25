export interface Node {
  id: number
  name: string
  ip: string
  agent_port: number
  fingerprint: string
  frps_port: number
  region: string
  status: string
  agent_version: string
  frps_version: string
  last_seen: string | null
  created_at: string
  updated_at: string
  connected: boolean
  latency_ms: number
  target_count: number
  cpu: number
  mem: number
  rx_bps: number
  tx_bps: number
  today_tun_in: number
  today_tun_out: number
}

export interface Target {
  id: number
  mapping_id: number
  node_id: number
  remote_port: number
  tunnel_status: string
  status_detail: string
  node_name: string
  node_ip: string
  node_region: string
  node_online: boolean
  node_latency_ms: number
  live_status: string
}

export interface Mapping {
  id: number
  local_port: number
  proto: string
  remark: string
  enabled: boolean
  version: number
  created_at: string
  updated_at: string
  targets: Target[]
}

export interface LogItem {
  id: number
  type: string
  source: string
  node_id: number | null
  detail: string
  created_at: string
}

export interface TrafficTotals {
  node_rx: number
  node_tx: number
  tun_in: number
  tun_out: number
}

export interface TopNode extends TrafficTotals {
  node_id: number
  node_name: string
}

export interface Overview {
  stats: { node_total: number; node_online: number; mapping_total: number; mapping_enabled: number }
  live: { node_rx_bps: number; node_tx_bps: number; tun_in_bps: number; tun_out_bps: number }
  traffic_today: TrafficTotals
  traffic_last30: TrafficTotals
  top_nodes: TopNode[]
  recent_logs: LogItem[]
}

export interface Settings {
  panel_name: string
  conn_rate_limit: number
  tcp_ping_interval: number
  auto_backup: boolean
  version: string
  tls_enabled: boolean
  update_base: string
  update_mirror: string
}

export interface PortCheckResult {
  available: boolean
  reason?: string
  process?: string
}

export interface MetricPoint {
  ts: string
  cpu_avg: number
  mem_avg: number
  rx_peak_bps: number
  tx_peak_bps: number
}

export interface TargetInput {
  node_id: number
  remote_port: number
}
