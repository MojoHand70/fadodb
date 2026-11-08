package bench

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// LookupReport is the result of the point-lookup benchmark at one table
// size: the cost of one keyed lookup on a Go map holding the table and
// through a prepared statement on the engine.
type LookupReport struct {
	Target string
	Rows   int
	// Map is the time of one lookup in a map[string]formula.Value built
	// from the table; MapBytes the heap the map and its keys took, as
	// the runtime measured it.
	Map      time.Duration
	MapBytes int64
	// Stmt is the time of one prepared-statement lookup on the engine.
	Stmt time.Duration
}

// String renders the report as one Markdown table row.
func (r LookupReport) String() string {
	return fmt.Sprintf("| %s | %s | %s | %.1f MB | %s |", r.Target, thousands(int64(r.Rows)), ns(r.Map), float64(r.MapBytes)/1e6, ns(r.Stmt))
}

// LookupHeader is the header of the table String rows belong to.
const LookupHeader = "| Engine | Rows | Go map | Map heap | Prepared statement |\n|---|---|---|---|---|"

func ns(d time.Duration) string {
	switch {
	case d < 10*time.Microsecond:
		return fmt.Sprintf("%d ns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1f µs", float64(d)/1e3)
	}
	return fmt.Sprintf("%.2f ms", float64(d)/1e6)
}

// RunLookup measures one keyed lookup at each table size: the rates table
// is filled by the target, loaded into a Go map as the in-memory index of
// ADR-0016 would hold it, and then looked up iters times by
// pseudo-random key, once on the map and once through a prepared
// SELECT on a pinned connection. The keys found must carry the rate the
// fill wrote, which checks that both paths read the same table.
func RunLookup(ctx context.Context, t Target, db *sql.DB, sizes []int, iters int) ([]LookupReport, error) {
	var out []LookupReport
	for _, n := range sizes {
		r, err := lookupAt(ctx, t, db, n, iters)
		if err != nil {
			return out, fmt.Errorf("%s at %d rows: %w", t.Name(), n, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func lookupAt(ctx context.Context, t Target, db *sql.DB, n, iters int) (LookupReport, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return LookupReport{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS rates"); err != nil {
		return LookupReport{}, err
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE rates (code "+t.ColumnType(formula.KindText)+" PRIMARY KEY, rate "+t.ColumnType(formula.KindNumber)+")"); err != nil {
		return LookupReport{}, err
	}
	if err := t.FillRates(ctx, conn, n); err != nil {
		return LookupReport{}, fmt.Errorf("fill: %w", err)
	}

	// The in-memory index: read the table once, column-wise, as the
	// store's QueryExecutor would (ADR-0020).
	index, bytes, err := loadIndex(ctx, conn, n)
	if err != nil {
		return LookupReport{}, err
	}
	stmt, err := conn.PrepareContext(ctx, "SELECT rate FROM rates WHERE code = ?")
	if err != nil {
		return LookupReport{}, err
	}
	defer stmt.Close()

	// Keys in a pseudo-random order so that neither path profits from
	// sequential access. Formatting is kept out of the timed loops.
	keys := make([]string, 4096)
	state := uint64(0x9e3779b97f4a7c15)
	for i := range keys {
		state = state*6364136223846793005 + 1442695040888963407
		keys[i] = fmt.Sprintf("K%07d", int(state>>33)%n)
	}
	// The fill wrote the row number times 0.001; an engine that multiplies
	// in decimal and one that multiplies in binary differ in the last
	// bit, so the check allows that.
	want := func(key string, got float64) bool {
		var i int
		fmt.Sscanf(key, "K%d", &i)
		return math.Abs(got-float64(i)*0.001) < 1e-9
	}

	var sink float64
	start := time.Now()
	for i := 0; i < iters; i++ {
		v, ok := index[keys[i&4095]]
		if !ok {
			return LookupReport{}, fmt.Errorf("map: %s not found", keys[i&4095])
		}
		sink += v.Num()
	}
	mapTime := time.Since(start) / time.Duration(iters)
	if got := index[keys[7]].Num(); !want(keys[7], got) {
		return LookupReport{}, fmt.Errorf("map: %s = %v, want the row number times 0.001", keys[7], got)
	}

	var rate float64
	start = time.Now()
	for i := 0; i < iters; i++ {
		if err := stmt.QueryRowContext(ctx, keys[i&4095]).Scan(&rate); err != nil {
			return LookupReport{}, fmt.Errorf("statement: %w", err)
		}
		sink += rate
	}
	stmtTime := time.Since(start) / time.Duration(iters)
	if err := stmt.QueryRowContext(ctx, keys[7]).Scan(&rate); err != nil || !want(keys[7], rate) || rate != index[keys[7]].Num() {
		return LookupReport{}, fmt.Errorf("statement: %s = %v (%v), want the map's %v", keys[7], rate, err, index[keys[7]].Num())
	}
	_ = sink
	return LookupReport{Target: t.Name(), Rows: n, Map: mapTime, MapBytes: bytes, Stmt: stmtTime}, nil
}

// loadIndex reads the rates table into a map and measures the heap it
// takes: the live heap after a collection, before and after the load.
func loadIndex(ctx context.Context, conn *sql.Conn, n int) (map[string]formula.Value, int64, error) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rows, err := conn.QueryContext(ctx, "SELECT code, rate FROM rates")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	index := make(map[string]formula.Value, n)
	for rows.Next() {
		var code string
		var rate float64
		if err := rows.Scan(&code, &rate); err != nil {
			return nil, 0, err
		}
		index[code] = formula.Number(rate)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(index) != n {
		return nil, 0, fmt.Errorf("index holds %d rows, want %d", len(index), n)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	return index, max(0, int64(after.HeapAlloc)-int64(before.HeapAlloc)), nil
}
