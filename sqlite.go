package ratelimit

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore is the internal SQLite persistence layer for rate limit state.
type sqliteStore struct {
	db *sql.DB
}

// newSQLiteStore opens (or creates) a SQLite database at dbPath and initialises
// the schema. It follows the go-store pattern: single connection, WAL journal
// mode, and a 5-second busy timeout for contention handling.
func newSQLiteStore(dbPath string) (*sqliteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("ratelimit.newSQLiteStore: open: %w", err)
	}

	// Single connection for PRAGMA consistency.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("ratelimit.newSQLiteStore: WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("ratelimit.newSQLiteStore: busy_timeout: %w", err)
	}

	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &sqliteStore{db: db}, nil
}

// createSchema creates the tables and indices if they do not already exist.
func createSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS quotas (
			model   TEXT PRIMARY KEY,
			max_rpm INTEGER NOT NULL DEFAULT 0,
			max_tpm INTEGER NOT NULL DEFAULT 0,
			max_rpd INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS requests (
			model TEXT NOT NULL,
			ts    INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			model TEXT NOT NULL,
			ts    INTEGER NOT NULL,
			count INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS daily (
			model     TEXT PRIMARY KEY,
			day_start INTEGER NOT NULL,
			day_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_model_ts ON requests(model, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_tokens_model_ts ON tokens(model, ts)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("ratelimit.createSchema: %w", err)
		}
	}
	return nil
}

// saveQuotas upserts all quotas into the quotas table.
func (s *sqliteStore) saveQuotas(quotas map[string]ModelQuota) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("ratelimit.saveQuotas: begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO quotas (model, max_rpm, max_tpm, max_rpd)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(model) DO UPDATE SET
			max_rpm = excluded.max_rpm,
			max_tpm = excluded.max_tpm,
			max_rpd = excluded.max_rpd`)
	if err != nil {
		return fmt.Errorf("ratelimit.saveQuotas: prepare: %w", err)
	}
	defer stmt.Close()

	for model, q := range quotas {
		if _, err := stmt.Exec(model, q.MaxRPM, q.MaxTPM, q.MaxRPD); err != nil {
			return fmt.Errorf("ratelimit.saveQuotas: exec %s: %w", model, err)
		}
	}

	return tx.Commit()
}

// loadQuotas reads all rows from the quotas table.
func (s *sqliteStore) loadQuotas() (map[string]ModelQuota, error) {
	rows, err := s.db.Query("SELECT model, max_rpm, max_tpm, max_rpd FROM quotas")
	if err != nil {
		return nil, fmt.Errorf("ratelimit.loadQuotas: query: %w", err)
	}
	defer rows.Close()

	result := make(map[string]ModelQuota)
	for rows.Next() {
		var model string
		var q ModelQuota
		if err := rows.Scan(&model, &q.MaxRPM, &q.MaxTPM, &q.MaxRPD); err != nil {
			return nil, fmt.Errorf("ratelimit.loadQuotas: scan: %w", err)
		}
		result[model] = q
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ratelimit.loadQuotas: rows: %w", err)
	}
	return result, nil
}

// saveState writes all usage state to SQLite in a single transaction.
// It uses a truncate-and-insert approach for simplicity in this version,
// but ensures atomicity via a single transaction.
func (s *sqliteStore) saveState(state map[string]*UsageStats) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("ratelimit.saveState: begin: %w", err)
	}
	defer tx.Rollback()

	// Clear existing state in the transaction.
	if _, err := tx.Exec("DELETE FROM requests"); err != nil {
		return fmt.Errorf("ratelimit.saveState: clear requests: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM tokens"); err != nil {
		return fmt.Errorf("ratelimit.saveState: clear tokens: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM daily"); err != nil {
		return fmt.Errorf("ratelimit.saveState: clear daily: %w", err)
	}

	reqStmt, err := tx.Prepare("INSERT INTO requests (model, ts) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("ratelimit.saveState: prepare requests: %w", err)
	}
	defer reqStmt.Close()

	tokStmt, err := tx.Prepare("INSERT INTO tokens (model, ts, count) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("ratelimit.saveState: prepare tokens: %w", err)
	}
	defer tokStmt.Close()

	dayStmt, err := tx.Prepare("INSERT INTO daily (model, day_start, day_count) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("ratelimit.saveState: prepare daily: %w", err)
	}
	defer dayStmt.Close()

	for model, stats := range state {
		for _, t := range stats.Requests {
			if _, err := reqStmt.Exec(model, t.UnixNano()); err != nil {
				return fmt.Errorf("ratelimit.saveState: insert request %s: %w", model, err)
			}
		}
		for _, te := range stats.Tokens {
			if _, err := tokStmt.Exec(model, te.Time.UnixNano(), te.Count); err != nil {
				return fmt.Errorf("ratelimit.saveState: insert token %s: %w", model, err)
			}
		}
		if _, err := dayStmt.Exec(model, stats.DayStart.UnixNano(), stats.DayCount); err != nil {
			return fmt.Errorf("ratelimit.saveState: insert daily %s: %w", model, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ratelimit.saveState: commit: %w", err)
	}
	return nil
}

// loadState reconstructs the UsageStats map from SQLite tables.
func (s *sqliteStore) loadState() (map[string]*UsageStats, error) {
	result := make(map[string]*UsageStats)

	// Load daily counters first (these define which models have state).
	rows, err := s.db.Query("SELECT model, day_start, day_count FROM daily")
	if err != nil {
		return nil, fmt.Errorf("ratelimit.loadState: query daily: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model string
		var dayStartNano int64
		var dayCount int
		if err := rows.Scan(&model, &dayStartNano, &dayCount); err != nil {
			return nil, fmt.Errorf("ratelimit.loadState: scan daily: %w", err)
		}
		result[model] = &UsageStats{
			DayStart: time.Unix(0, dayStartNano),
			DayCount: dayCount,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ratelimit.loadState: daily rows: %w", err)
	}

	// Load requests.
	reqRows, err := s.db.Query("SELECT model, ts FROM requests ORDER BY ts")
	if err != nil {
		return nil, fmt.Errorf("ratelimit.loadState: query requests: %w", err)
	}
	defer reqRows.Close()

	for reqRows.Next() {
		var model string
		var tsNano int64
		if err := reqRows.Scan(&model, &tsNano); err != nil {
			return nil, fmt.Errorf("ratelimit.loadState: scan requests: %w", err)
		}
		if _, ok := result[model]; !ok {
			result[model] = &UsageStats{}
		}
		result[model].Requests = append(result[model].Requests, time.Unix(0, tsNano))
	}
	if err := reqRows.Err(); err != nil {
		return nil, fmt.Errorf("ratelimit.loadState: request rows: %w", err)
	}

	// Load tokens.
	tokRows, err := s.db.Query("SELECT model, ts, count FROM tokens ORDER BY ts")
	if err != nil {
		return nil, fmt.Errorf("ratelimit.loadState: query tokens: %w", err)
	}
	defer tokRows.Close()

	for tokRows.Next() {
		var model string
		var tsNano int64
		var count int
		if err := tokRows.Scan(&model, &tsNano, &count); err != nil {
			return nil, fmt.Errorf("ratelimit.loadState: scan tokens: %w", err)
		}
		if _, ok := result[model]; !ok {
			result[model] = &UsageStats{}
		}
		result[model].Tokens = append(result[model].Tokens, TokenEntry{
			Time:  time.Unix(0, tsNano),
			Count: count,
		})
	}
	if err := tokRows.Err(); err != nil {
		return nil, fmt.Errorf("ratelimit.loadState: token rows: %w", err)
	}

	return result, nil
}

// close closes the underlying database connection.
func (s *sqliteStore) close() error {
	return s.db.Close()
}
