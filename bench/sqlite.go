package bench

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"time"

	_ "modernc.org/sqlite" // the pure-Go driver, the core's one dependency (ADR-0002)

	"github.com/MojoHand70/fadodb/internal/formula"
)

// SQLite is the transactional store candidate of ADR-0017 as the
// architecture page configures it: WAL mode, synchronous NORMAL, one
// writer connection.
type SQLite struct {
	// Synchronous is the PRAGMA synchronous setting: NORMAL, the
	// architecture page's choice, where a commit is durable against a
	// crash of the process but the last commits may be lost on power
	// failure, or FULL, where every commit is fsynced.
	Synchronous string
	// BackgroundCheckpoints turns SQLite's automatic checkpoint off, where
	// the committing connection copies the log into the database file
	// every 1,000 pages and pays that fsync inside a commit, and runs
	// PRAGMA wal_checkpoint(PASSIVE) on a second connection every 100 ms
	// instead, off the commit path. A passive checkpoint never resets the
	// log while a writer keeps appending, so when the log passes
	// WALLimit bytes the checkpointer runs wal_checkpoint(TRUNCATE),
	// which waits for the writer's transaction, holds the next one back
	// while it copies the rest, and empties the log.
	BackgroundCheckpoints bool
	// WALLimit is the log size that triggers a truncating checkpoint; 0
	// is 64 MB.
	WALLimit int64
}

// DefaultWALLimit is the write-ahead log size at which the background
// checkpointer truncates: sixteen times SQLite's own threshold of 1,000
// pages, so that the pause the truncation costs comes sixteen times less
// often than the automatic checkpoint's.
const DefaultWALLimit = 64 << 20

// Name implements Target.
func (s SQLite) Name() string {
	name := "SQLite"
	if s.Synchronous != "" && s.Synchronous != "NORMAL" {
		name += ", synchronous " + s.Synchronous
	}
	if s.BackgroundCheckpoints {
		name += ", background checkpoints"
	}
	return name
}

func (s SQLite) dsn(path string, readOnly bool) string {
	sync := s.Synchronous
	if sync == "" {
		sync = "NORMAL"
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous("+sync+")")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(OFF)")
	if s.BackgroundCheckpoints && !readOnly {
		q.Add("_pragma", "wal_autocheckpoint(0)")
	}
	if readOnly {
		q.Set("mode", "ro")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open implements Target: one connection for the writer, and a second one
// for the checkpointer when checkpoints run in the background.
func (s SQLite) Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", s.dsn(path, false))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if s.BackgroundCheckpoints {
		db.SetMaxOpenConns(2)
		limit := s.WALLimit
		if limit <= 0 {
			limit = DefaultWALLimit
		}
		go checkpoint(db, path+"-wal", limit)
	}
	return db, nil
}

// checkpoint copies the write-ahead log into the database file every
// 100 ms without waiting for the writer, and truncates the log once it
// passes limit bytes, until the database is closed.
func checkpoint(db *sql.DB, wal string, limit int64) {
	for {
		time.Sleep(100 * time.Millisecond)
		mode := "PASSIVE"
		if fi, err := os.Stat(wal); err == nil && fi.Size() > limit {
			mode = "TRUNCATE"
		}
		if _, err := db.Exec("PRAGMA wal_checkpoint(" + mode + ")"); err != nil {
			return
		}
	}
}

// OpenReadOnly implements Target.
func (s SQLite) OpenReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", s.dsn(path, true))
}

// ColumnType implements Target: SQLite's storage classes, a date as
// INTEGER days and a datetime as INTEGER seconds (ADR-0023), an exact
// value as its INTEGER mantissa (ADR-0022).
func (SQLite) ColumnType(k formula.Kind) string {
	switch k {
	case formula.KindNumber:
		return "REAL"
	case formula.KindBool, formula.KindDecimal, formula.KindMoney, formula.KindDate, formula.KindDateTime:
		return "INTEGER"
	}
	return "TEXT"
}

// FillRates implements Target with a recursive CTE.
func (SQLite) FillRates(ctx context.Context, conn Execer, n int) error {
	_, err := conn.ExecContext(ctx, fmt.Sprintf(`INSERT INTO rates (code, rate)
		WITH RECURSIVE s(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM s WHERE i + 1 < %d)
		SELECT printf('K%%07d', i), i * 0.001 FROM s`, n))
	return err
}

// FileSize implements Target: the database file and its write-ahead log.
func (SQLite) FileSize(path string) int64 { return FileSizeOf(path, path+"-wal") }
