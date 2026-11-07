package analytics

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/duckdb/duckdb-go/v2" // the DuckDB driver; cgo lives in this module only (ADR-0017)

	"github.com/MojoHand70/fadodb/bench"
	"github.com/MojoHand70/fadodb/internal/formula"
)

// DuckDBTarget is DuckDB as a candidate transactional store, the second
// half of the primary-store benchmark of ADR-0017. It opens a database
// file with the client's defaults and one connection for the writer.
type DuckDBTarget struct {
	// Threads sets DuckDB's thread count; 0 leaves the default, one per
	// core. A single writer issuing one statement at a time may pay for
	// the coordination of a pool it cannot use.
	Threads int
}

var _ bench.Target = DuckDBTarget{}

// Name implements bench.Target.
func (t DuckDBTarget) Name() string {
	if t.Threads > 0 {
		return fmt.Sprintf("DuckDB, %d thread(s)", t.Threads)
	}
	return "DuckDB"
}

// Open implements bench.Target.
func (t DuckDBTarget) Open(path string) (*sql.DB, error) {
	dsn := path
	if t.Threads > 0 {
		dsn += fmt.Sprintf("?threads=%d", t.Threads)
	}
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// OpenReadOnly implements bench.Target: a second process attaching the
// file in read-only mode.
func (DuckDBTarget) OpenReadOnly(path string) (*sql.DB, error) {
	return sql.Open("duckdb", path+"?access_mode=read_only")
}

// ColumnType implements bench.Target: a date as INTEGER days and a
// datetime as BIGINT seconds (ADR-0023), an exact value as its BIGINT
// mantissa (ADR-0022), as the Phase 8 projection would hold them.
func (DuckDBTarget) ColumnType(k formula.Kind) string {
	switch k {
	case formula.KindNumber:
		return "DOUBLE"
	case formula.KindBool:
		return "BOOLEAN"
	case formula.KindDecimal, formula.KindMoney, formula.KindDateTime:
		return "BIGINT"
	case formula.KindDate:
		return "INTEGER"
	}
	return "VARCHAR"
}

// FillRates implements bench.Target with a range scan.
func (DuckDBTarget) FillRates(ctx context.Context, conn bench.Execer, n int) error {
	_, err := conn.ExecContext(ctx, fmt.Sprintf("INSERT INTO rates SELECT 'K' || lpad(CAST(i AS VARCHAR), 7, '0'), i * 0.001 FROM range(%d) t(i)", n))
	return err
}

// FileSize implements bench.Target: the database file and its write-ahead log.
func (DuckDBTarget) FileSize(path string) int64 { return bench.FileSizeOf(path, path+".wal") }
