package panel

import (
	"embed"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Connect opens the MySQL pool with conservative limits (≤10 nodes, ≤3 browsers).
func Connect(cfg *Config) (*sqlx.DB, error) {
	db, err := sqlx.Open("mysql", cfg.MySQL.DSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

// Migrate applies any unapplied embedded migrations in filename order. It is
// safe to run repeatedly and forward-compatible: each file is tracked in
// schema_migrations and skipped once applied; all DDL uses IF NOT EXISTS.
func Migrate(db *sqlx.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(64) PRIMARY KEY,
		applied_at DATETIME NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists int
		if err := db.Get(&exists, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", name); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := db.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", name, time.Now().UTC()); err != nil {
			return fmt.Errorf("record %s: %w", name, err)
		}
	}
	return nil
}
