package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	SQL *sql.DB
}

const (
	DBFilename = "state.db"
)

func Open(stateDir string) (*DB, error) {
	if stateDir == "" {
		return nil, fmt.Errorf("stateDir is required")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, DBFilename)
	// modernc sqlite is pure go; flags are simple. Busy timeout 5s.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout=5000", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	db := &DB{SQL: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user TEXT,
			created_at INTEGER,
			expires_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER,
			actor TEXT,
			action TEXT,
			detail TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			kind TEXT,
			status TEXT,
			created_at INTEGER,
			updated_at INTEGER,
			payload TEXT,
			error TEXT
		);`,
	}
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() {
		// In case of panic, rollback
		_ = tx.Rollback()
	}()
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// AddAudit is a tiny helper for future milestones; no-op errors ignored here.
func (d *DB) AddAudit(actor, action, detail string) {
	if d == nil || d.SQL == nil {
		return
	}
	_, _ = d.SQL.Exec(`INSERT INTO audit_log (ts, actor, action, detail) VALUES (?, ?, ?, ?)`,
		time.Now().Unix(), actor, action, detail)
}

