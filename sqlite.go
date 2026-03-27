// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"database/sql"
	"time"

	core "dappco.re/go/core"
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
		return nil, core.E("ratelimit.newSQLiteStore", "open", err)
	}

	// Single connection for PRAGMA consistency.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, core.E("ratelimit.newSQLiteStore", "WAL", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, core.E("ratelimit.newSQLiteStore", "busy_timeout", err)
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
			return core.E("ratelimit.createSchema", "exec", err)
		}
	}
	return nil
}

// saveQuotas writes a complete quota snapshot to the quotas table.
func (s *sqliteStore) saveQuotas(quotas map[string]ModelQuota) error {
	tx, err := s.db.Begin()
	if err != nil {
		return core.E("ratelimit.saveQuotas", "begin", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM quotas"); err != nil {
		return core.E("ratelimit.saveQuotas", "clear", err)
	}

	if err := insertQuotas(tx, quotas); err != nil {
		return err
	}

	return commitTx(tx, "ratelimit.saveQuotas")
}

// loadQuotas reads all rows from the quotas table.
func (s *sqliteStore) loadQuotas() (map[string]ModelQuota, error) {
	rows, err := s.db.Query("SELECT model, max_rpm, max_tpm, max_rpd FROM quotas")
	if err != nil {
		return nil, core.E("ratelimit.loadQuotas", "query", err)
	}
	defer rows.Close()

	result := make(map[string]ModelQuota)
	for rows.Next() {
		var model string
		var q ModelQuota
		if err := rows.Scan(&model, &q.MaxRPM, &q.MaxTPM, &q.MaxRPD); err != nil {
			return nil, core.E("ratelimit.loadQuotas", "scan", err)
		}
		result[model] = q
	}
	if err := rows.Err(); err != nil {
		return nil, core.E("ratelimit.loadQuotas", "rows", err)
	}
	return result, nil
}

// saveSnapshot writes quotas and state as a single atomic snapshot.
func (s *sqliteStore) saveSnapshot(quotas map[string]ModelQuota, state map[string]*UsageStats) error {
	tx, err := s.db.Begin()
	if err != nil {
		return core.E("ratelimit.saveSnapshot", "begin", err)
	}
	defer tx.Rollback()

	if err := clearSnapshotTables(tx, true); err != nil {
		return err
	}

	if err := insertQuotas(tx, quotas); err != nil {
		return err
	}
	if err := insertState(tx, state); err != nil {
		return err
	}

	return commitTx(tx, "ratelimit.saveSnapshot")
}

// saveState writes all usage state to SQLite in a single transaction.
// It uses a truncate-and-insert approach for simplicity in this version,
// but ensures atomicity via a single transaction.
func (s *sqliteStore) saveState(state map[string]*UsageStats) error {
	tx, err := s.db.Begin()
	if err != nil {
		return core.E("ratelimit.saveState", "begin", err)
	}
	defer tx.Rollback()

	if err := clearSnapshotTables(tx, false); err != nil {
		return err
	}

	if err := insertState(tx, state); err != nil {
		return err
	}

	return commitTx(tx, "ratelimit.saveState")
}

func clearSnapshotTables(tx *sql.Tx, includeQuotas bool) error {
	if includeQuotas {
		if _, err := tx.Exec("DELETE FROM quotas"); err != nil {
			return core.E("ratelimit.saveSnapshot", "clear quotas", err)
		}
	}
	if _, err := tx.Exec("DELETE FROM requests"); err != nil {
		return core.E("ratelimit.saveState", "clear requests", err)
	}
	if _, err := tx.Exec("DELETE FROM tokens"); err != nil {
		return core.E("ratelimit.saveState", "clear tokens", err)
	}
	if _, err := tx.Exec("DELETE FROM daily"); err != nil {
		return core.E("ratelimit.saveState", "clear daily", err)
	}
	return nil
}

func insertQuotas(tx *sql.Tx, quotas map[string]ModelQuota) error {
	stmt, err := tx.Prepare("INSERT INTO quotas (model, max_rpm, max_tpm, max_rpd) VALUES (?, ?, ?, ?)")
	if err != nil {
		return core.E("ratelimit.saveQuotas", "prepare", err)
	}
	defer stmt.Close()

	for model, q := range quotas {
		if _, err := stmt.Exec(model, q.MaxRPM, q.MaxTPM, q.MaxRPD); err != nil {
			return core.E("ratelimit.saveQuotas", core.Concat("exec ", model), err)
		}
	}
	return nil
}

func insertState(tx *sql.Tx, state map[string]*UsageStats) error {
	reqStmt, err := tx.Prepare("INSERT INTO requests (model, ts) VALUES (?, ?)")
	if err != nil {
		return core.E("ratelimit.saveState", "prepare requests", err)
	}
	defer reqStmt.Close()

	tokStmt, err := tx.Prepare("INSERT INTO tokens (model, ts, count) VALUES (?, ?, ?)")
	if err != nil {
		return core.E("ratelimit.saveState", "prepare tokens", err)
	}
	defer tokStmt.Close()

	dayStmt, err := tx.Prepare("INSERT INTO daily (model, day_start, day_count) VALUES (?, ?, ?)")
	if err != nil {
		return core.E("ratelimit.saveState", "prepare daily", err)
	}
	defer dayStmt.Close()

	for model, stats := range state {
		if stats == nil {
			continue
		}
		for _, t := range stats.Requests {
			if _, err := reqStmt.Exec(model, t.UnixNano()); err != nil {
				return core.E("ratelimit.saveState", core.Concat("insert request ", model), err)
			}
		}
		for _, te := range stats.Tokens {
			if _, err := tokStmt.Exec(model, te.Time.UnixNano(), te.Count); err != nil {
				return core.E("ratelimit.saveState", core.Concat("insert token ", model), err)
			}
		}
		if _, err := dayStmt.Exec(model, stats.DayStart.UnixNano(), stats.DayCount); err != nil {
			return core.E("ratelimit.saveState", core.Concat("insert daily ", model), err)
		}
	}
	return nil
}

func commitTx(tx *sql.Tx, scope string) error {
	if err := tx.Commit(); err != nil {
		return core.E(scope, "commit", err)
	}
	return nil
}

// loadState reconstructs the UsageStats map from SQLite tables.
func (s *sqliteStore) loadState() (map[string]*UsageStats, error) {
	result := make(map[string]*UsageStats)

	// Load daily counters first (these define which models have state).
	rows, err := s.db.Query("SELECT model, day_start, day_count FROM daily")
	if err != nil {
		return nil, core.E("ratelimit.loadState", "query daily", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model string
		var dayStartNano int64
		var dayCount int
		if err := rows.Scan(&model, &dayStartNano, &dayCount); err != nil {
			return nil, core.E("ratelimit.loadState", "scan daily", err)
		}
		result[model] = &UsageStats{
			DayStart: time.Unix(0, dayStartNano),
			DayCount: dayCount,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, core.E("ratelimit.loadState", "daily rows", err)
	}

	// Load requests.
	reqRows, err := s.db.Query("SELECT model, ts FROM requests ORDER BY ts")
	if err != nil {
		return nil, core.E("ratelimit.loadState", "query requests", err)
	}
	defer reqRows.Close()

	for reqRows.Next() {
		var model string
		var tsNano int64
		if err := reqRows.Scan(&model, &tsNano); err != nil {
			return nil, core.E("ratelimit.loadState", "scan requests", err)
		}
		if _, ok := result[model]; !ok {
			result[model] = &UsageStats{}
		}
		result[model].Requests = append(result[model].Requests, time.Unix(0, tsNano))
	}
	if err := reqRows.Err(); err != nil {
		return nil, core.E("ratelimit.loadState", "request rows", err)
	}

	// Load tokens.
	tokRows, err := s.db.Query("SELECT model, ts, count FROM tokens ORDER BY ts")
	if err != nil {
		return nil, core.E("ratelimit.loadState", "query tokens", err)
	}
	defer tokRows.Close()

	for tokRows.Next() {
		var model string
		var tsNano int64
		var count int
		if err := tokRows.Scan(&model, &tsNano, &count); err != nil {
			return nil, core.E("ratelimit.loadState", "scan tokens", err)
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
		return nil, core.E("ratelimit.loadState", "token rows", err)
	}

	return result, nil
}

// close closes the underlying database connection.
func (s *sqliteStore) close() error {
	return s.db.Close()
}
