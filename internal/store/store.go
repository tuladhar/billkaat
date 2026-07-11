// Package store is the SQLite persistence layer. One local file, WAL mode,
// a single connection to keep concurrent writes trivially safe.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/billkaat/billkaat/internal/checks"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_fk=1")
	if err != nil {
		return nil, err
	}
	// A local single-user tool: one connection avoids SQLITE_BUSY entirely.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS scans (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	region         TEXT NOT NULL,
	account_id     TEXT NOT NULL DEFAULT '',
	account_label  TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT 'running', -- running|completed|failed
	error          TEXT NOT NULL DEFAULT '',
	started_at     TEXT NOT NULL,
	finished_at    TEXT,
	total_savings  REAL NOT NULL DEFAULT 0,
	findings_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS scan_checks (
	scan_id        INTEGER NOT NULL,
	check_id       TEXT NOT NULL,
	status         TEXT NOT NULL DEFAULT 'pending', -- pending|running|passed|flagged|locked|error
	error          TEXT NOT NULL DEFAULT '',
	findings_count INTEGER NOT NULL DEFAULT 0,
	savings        REAL NOT NULL DEFAULT 0,
	duration_ms    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (scan_id, check_id)
);
CREATE TABLE IF NOT EXISTS findings (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	scan_id         INTEGER NOT NULL,
	check_id        TEXT NOT NULL,
	resource_id     TEXT NOT NULL DEFAULT '',
	resource_type   TEXT NOT NULL DEFAULT '',
	region          TEXT NOT NULL DEFAULT '',
	severity        TEXT NOT NULL DEFAULT 'info',
	title           TEXT NOT NULL DEFAULT '',
	detail          TEXT NOT NULL DEFAULT '',
	recommendation  TEXT NOT NULL DEFAULT '',
	monthly_savings REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_findings_scan ON findings(scan_id);
CREATE INDEX IF NOT EXISTS idx_checks_scan   ON scan_checks(scan_id);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY CHECK (id = 1), -- single-user tool: exactly one row, ever
	username      TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	kdf_salt      BLOB NOT NULL,
	created_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS aws_accounts (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT NOT NULL,
	account_id    TEXT NOT NULL DEFAULT '',
	access_key_id TEXT NOT NULL,
	secret_enc    BLOB NOT NULL, -- AES-256-GCM, keyed off the login password — never plaintext
	created_at    TEXT NOT NULL
);`)
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// ---- scans ----

type Scan struct {
	ID            int64   `json:"id"`
	Region        string  `json:"region"`
	AccountID     string  `json:"account_id"`
	AccountLabel  string  `json:"account_label"`
	Status        string  `json:"status"`
	Error         string  `json:"error"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    string  `json:"finished_at"`
	TotalSavings  float64 `json:"total_savings"`
	FindingsCount int     `json:"findings_count"`
}

type CheckStatus struct {
	CheckID       string  `json:"check_id"`
	Status        string  `json:"status"`
	Error         string  `json:"error"`
	FindingsCount int     `json:"findings_count"`
	Savings       float64 `json:"savings"`
	DurationMs    int64   `json:"duration_ms"`
}

type ScanDetail struct {
	Scan     Scan             `json:"scan"`
	Checks   []CheckStatus    `json:"checks"`
	Findings []checks.Finding `json:"findings"`
}

func (s *Store) CreateScan(region, accountLabel string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO scans (region, account_label, started_at) VALUES (?, ?, ?)`,
		region, accountLabel, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetScanAccount(id int64, account string) error {
	_, err := s.db.Exec(`UPDATE scans SET account_id = ? WHERE id = ?`, account, id)
	return err
}

func (s *Store) FinishScan(id int64, totalSavings float64, findingsCount int) error {
	_, err := s.db.Exec(
		`UPDATE scans SET status='completed', finished_at=?, total_savings=?, findings_count=? WHERE id=?`,
		now(), totalSavings, findingsCount, id)
	return err
}

func (s *Store) FailScan(id int64, msg string) error {
	_, err := s.db.Exec(
		`UPDATE scans SET status='failed', finished_at=?, error=? WHERE id=?`, now(), msg, id)
	return err
}

// RunningScan returns the id of a scan still in progress, or 0.
func (s *Store) RunningScan() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM scans WHERE status='running' ORDER BY id DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (s *Store) ListScans(limit int) ([]Scan, error) {
	rows, err := s.db.Query(`
		SELECT id, region, account_id, account_label, status, error, started_at,
		       COALESCE(finished_at,''), total_savings, findings_count
		FROM scans ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Scan
	for rows.Next() {
		var sc Scan
		if err := rows.Scan(&sc.ID, &sc.Region, &sc.AccountID, &sc.AccountLabel, &sc.Status, &sc.Error,
			&sc.StartedAt, &sc.FinishedAt, &sc.TotalSavings, &sc.FindingsCount); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) GetScan(id int64) (*ScanDetail, error) {
	var sc Scan
	err := s.db.QueryRow(`
		SELECT id, region, account_id, account_label, status, error, started_at,
		       COALESCE(finished_at,''), total_savings, findings_count
		FROM scans WHERE id = ?`, id).
		Scan(&sc.ID, &sc.Region, &sc.AccountID, &sc.AccountLabel, &sc.Status, &sc.Error,
			&sc.StartedAt, &sc.FinishedAt, &sc.TotalSavings, &sc.FindingsCount)
	if err != nil {
		return nil, err
	}

	d := &ScanDetail{Scan: sc, Checks: []CheckStatus{}, Findings: []checks.Finding{}}

	rows, err := s.db.Query(`
		SELECT check_id, status, error, findings_count, savings, duration_ms
		FROM scan_checks WHERE scan_id = ? ORDER BY check_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CheckStatus
		if err := rows.Scan(&c.CheckID, &c.Status, &c.Error, &c.FindingsCount,
			&c.Savings, &c.DurationMs); err != nil {
			return nil, err
		}
		d.Checks = append(d.Checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	frows, err := s.db.Query(`
		SELECT check_id, resource_id, resource_type, region, severity,
		       title, detail, recommendation, monthly_savings
		FROM findings WHERE scan_id = ? ORDER BY monthly_savings DESC, id`, id)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var f checks.Finding
		if err := frows.Scan(&f.CheckID, &f.ResourceID, &f.ResourceType, &f.Region,
			&f.Severity, &f.Title, &f.Detail, &f.Recommendation, &f.MonthlySavingsUSD); err != nil {
			return nil, err
		}
		d.Findings = append(d.Findings, f)
	}
	return d, frows.Err()
}

// ---- per-check status ----

func (s *Store) InitCheck(scanID int64, checkID, status string) error {
	_, err := s.db.Exec(`
		INSERT INTO scan_checks (scan_id, check_id, status) VALUES (?, ?, ?)
		ON CONFLICT(scan_id, check_id) DO UPDATE SET status = excluded.status`,
		scanID, checkID, status)
	return err
}

func (s *Store) SetCheckStatus(scanID int64, checkID, status, errMsg string,
	findings int, savings float64, durationMs int64) error {
	_, err := s.db.Exec(`
		INSERT INTO scan_checks (scan_id, check_id, status, error, findings_count, savings, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scan_id, check_id) DO UPDATE SET
			status = excluded.status, error = excluded.error,
			findings_count = excluded.findings_count, savings = excluded.savings,
			duration_ms = excluded.duration_ms`,
		scanID, checkID, status, errMsg, findings, savings, durationMs)
	return err
}

func (s *Store) AddFindings(scanID int64, fs []checks.Finding) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO findings (scan_id, check_id, resource_id, resource_type, region,
			severity, title, detail, recommendation, monthly_savings)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, f := range fs {
		if _, err := stmt.Exec(scanID, f.CheckID, f.ResourceID, f.ResourceType,
			f.Region, string(f.Severity), f.Title, f.Detail, f.Recommendation,
			f.MonthlySavingsUSD); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ---- settings ----

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// ---- user (single row: this is a single-user local tool) ----

type User struct {
	Username     string
	PasswordHash string
	KDFSalt      []byte
}

// HasUser reports whether the one-time setup has run yet.
func (s *Store) HasUser() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

// CreateUser stores the single user row. Fails if one already exists (the
// `id = 1` primary key collides), which is the intended guard against
// re-running setup.
func (s *Store) CreateUser(username, passwordHash string, kdfSalt []byte) error {
	_, err := s.db.Exec(`
		INSERT INTO users (id, username, password_hash, kdf_salt, created_at)
		VALUES (1, ?, ?, ?, ?)`, username, passwordHash, kdfSalt, now())
	return err
}

// GetUser returns the single user row, or nil if setup hasn't run yet.
func (s *Store) GetUser() (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT username, password_hash, kdf_salt FROM users WHERE id = 1`).
		Scan(&u.Username, &u.PasswordHash, &u.KDFSalt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ---- AWS accounts ----

// AWSAccount is what the UI lists — never the decrypted secret.
type AWSAccount struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AccountID   string `json:"account_id"`
	AccessKeyID string `json:"access_key_id"`
	CreatedAt   string `json:"created_at"`
}

// CreateAWSAccount stores a new account. secretEnc must already be encrypted
// (see internal/auth) — the store never sees a plaintext secret key.
func (s *Store) CreateAWSAccount(name, accountID, accessKeyID string, secretEnc []byte) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO aws_accounts (name, account_id, access_key_id, secret_enc, created_at)
		VALUES (?, ?, ?, ?, ?)`, name, accountID, accessKeyID, secretEnc, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListAWSAccounts() ([]AWSAccount, error) {
	rows, err := s.db.Query(`
		SELECT id, name, account_id, access_key_id, created_at
		FROM aws_accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AWSAccount{}
	for rows.Next() {
		var a AWSAccount
		if err := rows.Scan(&a.ID, &a.Name, &a.AccountID, &a.AccessKeyID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAWSAccountSecret returns what a scan needs to authenticate: the access
// key id, the account's friendly name (for labeling the scan), and the
// still-encrypted secret (the caller decrypts it with the session's key).
func (s *Store) GetAWSAccountSecret(id int64) (accessKeyID, name string, secretEnc []byte, err error) {
	err = s.db.QueryRow(`
		SELECT access_key_id, name, secret_enc FROM aws_accounts WHERE id = ?`, id).
		Scan(&accessKeyID, &name, &secretEnc)
	return
}

func (s *Store) DeleteAWSAccount(id int64) error {
	_, err := s.db.Exec(`DELETE FROM aws_accounts WHERE id = ?`, id)
	return err
}
