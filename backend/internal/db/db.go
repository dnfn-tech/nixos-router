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
		`CREATE TABLE IF NOT EXISTS users (
			username TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			created_at INTEGER
		);`,
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

func (d *DB) GetUserPasswordHash(username string) (string, error) {
	var hash string
	err := d.SQL.QueryRow(`SELECT password_hash FROM users WHERE username=?`, username).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func (d *DB) CreateUser(username, passwordHash string) error {
	_, err := d.SQL.Exec(`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, passwordHash, time.Now().Unix())
	return err
}

func (d *DB) HasAnyUser() (bool, error) {
	var n int
	if err := d.SQL.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d *DB) CreateSession(id, username string, ttl time.Duration) error {
	now := time.Now()
	_, err := d.SQL.Exec(`INSERT INTO sessions (id, user, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		id, username, now.Unix(), now.Add(ttl).Unix())
	return err
}

func (d *DB) GetSession(id string) (username string, expiresAt time.Time, ok bool, err error) {
	var ts int64
	err = d.SQL.QueryRow(`SELECT user, expires_at FROM sessions WHERE id=?`, id).Scan(&username, &ts)
	if err == sql.ErrNoRows {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	expiresAt = time.Unix(ts, 0)
	if time.Now().After(expiresAt) {
		// cleanup best-effort
		_, _ = d.SQL.Exec(`DELETE FROM sessions WHERE id=?`, id)
		return "", time.Time{}, false, nil
	}
	return username, expiresAt, true, nil
}

func (d *DB) DeleteSession(id string) error {
	_, err := d.SQL.Exec(`DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (d *DB) ListJobs(limit int) ([]Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := d.SQL.Query(`SELECT id, kind, status, created_at, updated_at, payload, error FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var created, updated int64
		if err := rows.Scan(&j.ID, &j.Kind, &j.Status, &created, &updated, &j.Payload, &j.Error); err != nil {
			return nil, err
		}
		j.CreatedAt = time.Unix(created, 0)
		j.UpdatedAt = time.Unix(updated, 0)
		out = append(out, j)
	}
	return out, rows.Err()
}

type Job struct {
	ID        string
	Kind      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Payload   string
	Error     string
}

func (d *DB) CreateJob(id, kind, payload string) error {
	now := time.Now().Unix()
	_, err := d.SQL.Exec(`INSERT INTO jobs (id, kind, status, created_at, updated_at, payload) VALUES (?, ?, ?, ?, ?, ?)`,
		id, kind, "running", now, now, payload)
	return err
}

func (d *DB) UpdateJobStatus(id, status, errText string) error {
	_, err := d.SQL.Exec(`UPDATE jobs SET status=?, updated_at=?, error=? WHERE id=?`,
		status, time.Now().Unix(), errText, id)
	return err
}

func (d *DB) GetJob(id string) (Job, bool, error) {
	var j Job
	var created, updated int64
	err := d.SQL.QueryRow(`SELECT id, kind, status, created_at, updated_at, payload, error FROM jobs WHERE id=?`, id).
		Scan(&j.ID, &j.Kind, &j.Status, &created, &updated, &j.Payload, &j.Error)
	if err == sql.ErrNoRows {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	j.CreatedAt = time.Unix(created, 0)
	j.UpdatedAt = time.Unix(updated, 0)
	return j, true, nil
}

