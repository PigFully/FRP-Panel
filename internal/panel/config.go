// Package panel implements the management panel: REST API + embedded SPA, MySQL
// persistence, per-node frpc supervision, outbound WSS connections to agents,
// the three-tier metrics pipeline and the browser realtime hub.
package panel

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the panel configuration (/etc/frp-panel/config.yaml).
type Config struct {
	Listen    string      `yaml:"listen"`
	MySQL     MySQLConfig `yaml:"mysql"`
	JWTSecret string      `yaml:"jwt_secret"`
	TLS       TLSConfig   `yaml:"tls"`
	DataDir       string  `yaml:"data_dir"`
	FrpcBin       string  `yaml:"frpc_bin"`
	PanelName     string  `yaml:"panel_name"`
	UpdateBaseURL string  `yaml:"update_base_url"`
	Debug         bool    `yaml:"debug"`
}

// MySQLConfig holds the DB connection parameters (user-provided at install).
type MySQLConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
}

// TLSConfig optionally enables self-signed TLS for the panel web server.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// DSN builds the go-sql-driver/mysql DSN. Times are stored/read as UTC.
// multiStatements lets the migration runner execute a whole file at once (all
// queries elsewhere are parameterized, so this widens no injection surface).
func (m MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=UTC&charset=utf8mb4,utf8&timeout=8s&readTimeout=30s&writeTimeout=30s&multiStatements=true",
		m.User, m.Password, m.Host, m.Port, m.Database)
}

// DSNNoDB is the DSN without a database (for CREATE DATABASE at install/migrate).
func (m MySQLConfig) DSNNoDB() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=true&loc=UTC&charset=utf8mb4,utf8&timeout=8s",
		m.User, m.Password, m.Host, m.Port)
}

// LoadConfig reads and validates the YAML config.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = "0.0.0.0:8080"
	}
	if c.DataDir == "" {
		c.DataDir = "/opt/frp-panel"
	}
	if c.FrpcBin == "" {
		c.FrpcBin = c.DataDir + "/frpc"
	}
	if c.PanelName == "" {
		c.PanelName = "FRPanel"
	}
	if c.JWTSecret == "" {
		return nil, fmt.Errorf("jwt_secret 未配置")
	}
	if c.MySQL.Port == 0 {
		c.MySQL.Port = 3306
	}
	return &c, nil
}
