CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(100) NOT NULL,
  pwd_version INT NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS nodes (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  ip VARCHAR(64) NOT NULL,
  agent_port INT NOT NULL DEFAULT 8443,
  agent_token VARCHAR(128) NOT NULL,
  fingerprint VARCHAR(128) NOT NULL DEFAULT '',
  frps_token VARCHAR(128) NOT NULL DEFAULT '',
  frps_port INT NOT NULL DEFAULT 7000,
  region VARCHAR(32) NOT NULL DEFAULT 'overseas',
  status VARCHAR(16) NOT NULL DEFAULT 'offline',
  agent_version VARCHAR(64) NOT NULL DEFAULT '',
  frps_version VARCHAR(64) NOT NULL DEFAULT '',
  last_commit_seq BIGINT NOT NULL DEFAULT 0,
  last_seen DATETIME NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uq_nodes_ip_port (ip, agent_port)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS mappings (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  local_port INT NOT NULL,
  proto VARCHAR(8) NOT NULL DEFAULT 'tcp',
  remark VARCHAR(255) NOT NULL DEFAULT '',
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  version INT NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS mapping_targets (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  mapping_id BIGINT NOT NULL,
  node_id BIGINT NOT NULL,
  remote_port INT NOT NULL,
  tunnel_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  status_detail VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  KEY idx_mt_mapping (mapping_id),
  KEY idx_mt_node (node_id),
  UNIQUE KEY uq_mt_mapping_node (mapping_id, node_id),
  UNIQUE KEY uq_mt_node_port (node_id, remote_port)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS metrics_minutely (
  node_id BIGINT NOT NULL,
  ts DATETIME NOT NULL,
  cpu_avg DOUBLE NOT NULL DEFAULT 0,
  mem_avg DOUBLE NOT NULL DEFAULT 0,
  rx_peak_bps BIGINT NOT NULL DEFAULT 0,
  tx_peak_bps BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS metrics_hourly (
  node_id BIGINT NOT NULL,
  ts DATETIME NOT NULL,
  cpu_avg DOUBLE NOT NULL DEFAULT 0,
  mem_avg DOUBLE NOT NULL DEFAULT 0,
  rx_peak_bps BIGINT NOT NULL DEFAULT 0,
  tx_peak_bps BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS metrics_daily (
  node_id BIGINT NOT NULL,
  ts DATETIME NOT NULL,
  cpu_avg DOUBLE NOT NULL DEFAULT 0,
  mem_avg DOUBLE NOT NULL DEFAULT 0,
  rx_peak_bps BIGINT NOT NULL DEFAULT 0,
  tx_peak_bps BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS traffic_daily (
  node_id BIGINT NOT NULL,
  day VARCHAR(10) NOT NULL,
  node_rx_bytes BIGINT NOT NULL DEFAULT 0,
  node_tx_bytes BIGINT NOT NULL DEFAULT 0,
  tun_in_bytes BIGINT NOT NULL DEFAULT 0,
  tun_out_bytes BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS operation_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  type VARCHAR(16) NOT NULL,
  source VARCHAR(128) NOT NULL DEFAULT '',
  node_id BIGINT NULL,
  detail TEXT,
  created_at DATETIME NOT NULL,
  KEY idx_log_created (created_at),
  KEY idx_log_type (type),
  KEY idx_log_node (node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS settings (
  k VARCHAR(64) PRIMARY KEY,
  v TEXT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
