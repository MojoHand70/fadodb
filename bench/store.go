package bench

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Target is one storage engine under measurement. database/sql carries
// the statements; the target spells what differs between engines.
type Target interface {
	// Name names the engine in reports.
	Name() string
	// Open opens or creates the database at path for the single writer,
	// configured as the store would configure it.
	Open(path string) (*sql.DB, error)
	// OpenReadOnly opens the database at path for reading, as a second
	// process would while the writer holds it.
	OpenReadOnly(path string) (*sql.DB, error)
	// ColumnType spells the SQL type of a column holding values of the
	// kind, as the generated tables of ADR-0014 would declare it.
	ColumnType(k formula.Kind) string
	// FillRates fills the rates table of the lookup benchmark with n rows
	// whose code is "K" and seven digits and whose rate is the row number
	// times 0.001, in the engine's own bulk way, on the given connection.
	FillRates(ctx context.Context, conn Execer, n int) error
	// FileSize reports the bytes the database at path occupies on disk,
	// its log included.
	FileSize(path string) int64
}

// Execer is what a target writes through: a *sql.Conn or a *sql.DB.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Column is one typed column of the scenario class.
type Column struct {
	Name string
	Kind formula.Kind
}

// The class of the store scenario: an invoice with scalars of every stored
// kind, a money variable as its two columns (ADR-0022), two objects
// flattened to prefixed columns (ADR-0031) and a rows variable in a child
// table (ADR-0014). Twenty-one variables plus the title.
var variables = []Column{
	{"number", formula.KindText}, {"customer", formula.KindRef},
	{"issued", formula.KindDate}, {"due", formula.KindDate},
	{"owner", formula.KindText}, {"currency", formula.KindText},
	{"discount", formula.KindDecimal},
	{"amount", formula.KindDecimal}, {"amount" + formula.CurrencySuffix, formula.KindText},
	{"total", formula.KindDecimal}, {"total_eur", formula.KindDecimal},
	{"paid", formula.KindBool}, {"priority", formula.KindNumber},
	{"notes", formula.KindText},
	{"shipping__line1", formula.KindText}, {"shipping__city", formula.KindText},
	{"shipping__postal_code", formula.KindText}, {"shipping__country", formula.KindText},
	{"contact__id", formula.KindText}, {"contact__name", formula.KindText},
	{"contact__email", formula.KindText},
}

// system columns of the current row (ADR-0013, ADR-0018).
var systemColumns = []Column{
	{"id", formula.KindText}, {"title", formula.KindText}, {"state", formula.KindText},
	{"version", formula.KindDecimal}, {"created_at", formula.KindDateTime},
	{"updated_at", formula.KindDateTime}, {"state_entered_at", formula.KindDateTime},
}

// history-only columns (ADR-0014).
var historyColumns = []Column{
	{"valid_from", formula.KindDateTime}, {"valid_to", formula.KindDateTime},
	{"actor_id", formula.KindText}, {"event_id", formula.KindText},
}

// lineColumns is the schema of the rows variable `lines`.
var lineColumns = []Column{
	{"sku", formula.KindText}, {"qty", formula.KindNumber}, {"price", formula.KindDecimal},
}

var states = []string{"draft", "submitted", "approved", "paid"}

// Options size a store run.
type Options struct {
	// Commits is the number of commits to write.
	Commits int
	// Documents is the number of documents the commits are spread over.
	// The first Documents commits create, the rest update, so that every
	// document receives Commits/Documents versions.
	Documents int
	// Lines is the number of child rows per version.
	Lines int
	// Batch is the number of commits per transaction; 1 is unbatched, one
	// transaction per commit, which is how a user's edit commits.
	Batch int
	// Seed seeds the values.
	Seed uint64
}

// Report is the result of one store run.
type Report struct {
	Target    string
	Commits   int
	Documents int
	Lines     int
	Batch     int
	// Elapsed is the wall time of all commits; PerSecond the throughput.
	Elapsed   time.Duration
	PerSecond float64
	// The latency percentiles of one commit (Batch 1) or of one batch.
	P50, P90, P99, Max time.Duration
	// FileBytes is the size of the database on disk after the run.
	FileBytes int64
}

// String renders the report as one Markdown table row: target, mode,
// throughput, p50, p99, max and the file size.
func (r Report) String() string {
	mode := "unbatched"
	if r.Batch > 1 {
		mode = fmt.Sprintf("batched ×%d", r.Batch)
	}
	return fmt.Sprintf("| %s | %s | %d commits over %d documents | %s commits/s | %s | %s | %s | %s | %.0f MB |",
		r.Target, mode, r.Commits, r.Documents, thousands(int64(r.PerSecond)),
		ms(r.P50), ms(r.P90), ms(r.P99), ms(r.Max), float64(r.FileBytes)/1e6)
}

// Header is the header of the table String rows belong to.
const Header = "| Target | Mode | Work | Throughput | p50 | p90 | p99 | max | File |\n|---|---|---|---|---|---|---|---|---|"

func ms(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.3f ms", float64(d)/1e6)
	case d < 100*time.Millisecond:
		return fmt.Sprintf("%.2f ms", float64(d)/1e6)
	}
	return fmt.Sprintf("%.0f ms", float64(d)/1e6)
}

func thousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Schema returns the DDL of the scenario for a target: the current table,
// the history table, the child table, events and event_docs, with the
// indexes ADR-0014 and ADR-0026 name.
func Schema(t Target) []string {
	cols := func(cs []Column) string {
		parts := make([]string, len(cs))
		for i, c := range cs {
			parts[i] = c.Name + " " + t.ColumnType(c.Kind)
		}
		return strings.Join(parts, ", ")
	}
	current := append(append([]Column{}, systemColumns...), variables...)
	history := append(append(append([]Column{}, systemColumns...), historyColumns...), variables...)
	lines := append([]Column{{"doc_id", formula.KindText}, {"version", formula.KindDecimal}, {"idx", formula.KindDecimal}}, lineColumns...)
	text, integer := t.ColumnType(formula.KindText), t.ColumnType(formula.KindDecimal)
	return []string{
		"CREATE TABLE doc_Invoice (" + cols(current) + ", PRIMARY KEY (id))",
		"CREATE TABLE doc_Invoice_history (" + cols(history) + ", PRIMARY KEY (id, version))",
		"CREATE INDEX doc_Invoice_history_valid ON doc_Invoice_history (id, valid_from)",
		"CREATE TABLE doc_Invoice__lines (" + cols(lines) + ", PRIMARY KEY (doc_id, version, idx))",
		"CREATE TABLE events (event_id " + text + ", class " + text + ", doc_id " + text + ", kind " + text + ", name " + text +
			", from_state " + text + ", to_state " + text + ", actor_id " + text + ", before_version " + integer + ", after_version " + integer +
			", cause_event_id " + text + ", command_key " + text + ", business_date " + t.ColumnType(formula.KindDate) + ", ts " + t.ColumnType(formula.KindDateTime) +
			", PRIMARY KEY (event_id))",
		"CREATE UNIQUE INDEX events_command_key ON events (command_key)",
		"CREATE INDEX events_class_doc ON events (class, doc_id, ts)",
		"CREATE INDEX events_business_date ON events (business_date)",
		"CREATE TABLE event_docs (event_id " + text + ", class " + text + ", doc_id " + text + ", qualifier " + text + ", PRIMARY KEY (event_id, class, doc_id))",
	}
}

// statements are the DML of one commit, shared by every target: both
// engines take positional ? parameters and the ON CONFLICT upsert.
type statements struct {
	upsert, closeHistory, insertHistory, insertLine, insertEvent, insertEventDoc *sql.Stmt
}

func names(cs []Column) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

func prepare(ctx context.Context, conn *sql.Conn) (*statements, error) {
	current := append(names(systemColumns), names(variables)...)
	history := append(append(names(systemColumns), names(historyColumns)...), names(variables)...)
	sets := make([]string, 0, len(current))
	for _, c := range current[1:] {
		sets = append(sets, c+" = excluded."+c)
	}
	texts := map[string]string{
		"upsert":        "INSERT INTO doc_Invoice (" + strings.Join(current, ", ") + ") VALUES (" + marks(len(current)) + ") ON CONFLICT (id) DO UPDATE SET " + strings.Join(sets, ", "),
		"closeHistory":  "UPDATE doc_Invoice_history SET valid_to = ? WHERE id = ? AND version = ?",
		"insertHistory": "INSERT INTO doc_Invoice_history (" + strings.Join(history, ", ") + ") VALUES (" + marks(len(history)) + ")",
		"insertLine":    "INSERT INTO doc_Invoice__lines (doc_id, version, idx, sku, qty, price) VALUES (?, ?, ?, ?, ?, ?)",
		"insertEvent": "INSERT INTO events (event_id, class, doc_id, kind, name, from_state, to_state, actor_id, before_version, after_version, cause_event_id, command_key, business_date, ts)" +
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"insertEventDoc": "INSERT INTO event_docs (event_id, class, doc_id, qualifier) VALUES (?, ?, ?, ?)",
	}
	s := &statements{}
	for name, text := range texts {
		st, err := conn.PrepareContext(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("prepare %s: %w", name, err)
		}
		switch name {
		case "upsert":
			s.upsert = st
		case "closeHistory":
			s.closeHistory = st
		case "insertHistory":
			s.insertHistory = st
		case "insertLine":
			s.insertLine = st
		case "insertEvent":
			s.insertEvent = st
		case "insertEventDoc":
			s.insertEventDoc = st
		}
	}
	return s, nil
}

func (s *statements) close() {
	for _, st := range []*sql.Stmt{s.upsert, s.closeHistory, s.insertHistory, s.insertLine, s.insertEvent, s.insertEventDoc} {
		if st != nil {
			st.Close()
		}
	}
}

// document is the generated state of one document between commits.
type document struct {
	version   int64
	createdAt int64
	state     int
}

// commit writes one commit: the current row, the previous history row
// closed, the new history row, the child rows, the event and its
// event_docs row. It is the write step of ADR-0015 as the store will do
// it, without the compute.
func commit(ctx context.Context, s *statements, rng *rand.Rand, docs []document, doc int, n int, now int64, lines int, args []any) error {
	d := &docs[doc]
	before := d.version
	d.version++
	kind, qualifier, name := "set", "changed", ""
	if before == 0 {
		d.createdAt = now
		kind, qualifier = "created", "created"
	} else if rng.IntN(4) == 0 && d.state < len(states)-1 {
		d.state++
		kind, qualifier, name = "transition", "transitioned", "to_"+states[d.state]
	}
	id := fmt.Sprintf("inv_%08d", doc)
	eventID := fmt.Sprintf("evt_%010d", n)
	state := states[d.state]
	day := int64(20700 + rng.IntN(365))
	mant := int64(rng.IntN(1_000_000))
	args = args[:0]
	args = append(args, id, "INV-2026-"+fmt.Sprint(doc), state, d.version, d.createdAt, now, now)
	vars := func(a []any) []any {
		return append(a,
			fmt.Sprintf("2026-%06d", doc), fmt.Sprintf("Customer:%d", rng.IntN(5000)),
			day, day+30, fmt.Sprintf("user%d", rng.IntN(200)), "PLN",
			int64(rng.IntN(2000)), mant, "PLN", mant, mant*23/100,
			rng.IntN(2) == 1, float64(rng.IntN(5)),
			"Some notes on the invoice, version "+fmt.Sprint(d.version),
			"Długa 1", "Gdańsk", "80-831", "PL",
			fmt.Sprintf("anna.%d", rng.IntN(1000)), "Anna Kowalska", "anna@acme.example")
	}
	args = vars(args)
	if _, err := s.upsert.ExecContext(ctx, args...); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	if before > 0 {
		if _, err := s.closeHistory.ExecContext(ctx, now, id, before); err != nil {
			return fmt.Errorf("close history: %w", err)
		}
	}
	args = args[:0]
	args = append(args, id, "INV-2026-"+fmt.Sprint(doc), state, d.version, d.createdAt, now, now, now, nil, "user7", eventID)
	args = vars(args)
	if _, err := s.insertHistory.ExecContext(ctx, args...); err != nil {
		return fmt.Errorf("insert history: %w", err)
	}
	for i := 0; i < lines; i++ {
		if _, err := s.insertLine.ExecContext(ctx, id, d.version, int64(i), fmt.Sprintf("SKU-%d", rng.IntN(100)), float64(rng.IntN(10)), int64(rng.IntN(100000))); err != nil {
			return fmt.Errorf("insert line: %w", err)
		}
	}
	var fromState, toState any
	if kind == "transition" {
		fromState, toState = states[d.state-1], state
	}
	if _, err := s.insertEvent.ExecContext(ctx, eventID, "Invoice", id, kind, name, fromState, toState, "user7", before, d.version, nil, "key-"+eventID, day, now); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	if _, err := s.insertEventDoc.ExecContext(ctx, eventID, "Invoice", id, qualifier); err != nil {
		return fmt.Errorf("insert event_docs: %w", err)
	}
	return nil
}

// RunStore creates the scenario schema in db, which Open gave for path,
// and writes the commits of o, measuring each transaction. The run pins
// one connection, as the store's single writer will, and prepares every
// statement once. The statements are closed before the connection goes
// back to the pool, where another goroutine of the target may take it.
func RunStore(ctx context.Context, t Target, db *sql.DB, path string, o Options) (Report, error) {
	if o.Commits <= 0 || o.Documents <= 0 || o.Documents > o.Commits || o.Batch <= 0 {
		return Report{}, fmt.Errorf("bench: want 0 < documents <= commits and batch > 0, got %+v", o)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return Report{}, err
	}
	var s *statements
	release := func() error {
		if s != nil {
			s.close()
			s = nil
		}
		return conn.Close()
	}
	for _, ddl := range Schema(t) {
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			release()
			return Report{}, fmt.Errorf("%s: %w\n%s", t.Name(), err, ddl)
		}
	}
	if s, err = prepare(ctx, conn); err != nil {
		release()
		return Report{}, err
	}

	rng := rand.New(rand.NewPCG(o.Seed, o.Seed^0x9e3779b97f4a7c15))
	docs := make([]document, o.Documents)
	args := make([]any, 0, 40)
	units := (o.Commits + o.Batch - 1) / o.Batch
	lat := make([]time.Duration, 0, units)
	now := time.Now().Unix()
	start := time.Now()
	for n := 0; n < o.Commits; {
		unit := time.Now()
		if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
			release()
			return Report{}, fmt.Errorf("begin: %w", err)
		}
		for k := 0; k < o.Batch && n < o.Commits; k, n = k+1, n+1 {
			if err := commit(ctx, s, rng, docs, n%o.Documents, n, now+int64(n), o.Lines, args); err != nil {
				_, _ = conn.ExecContext(ctx, "ROLLBACK")
				release()
				return Report{}, fmt.Errorf("commit %d: %w", n, err)
			}
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			release()
			return Report{}, fmt.Errorf("commit: %w", err)
		}
		lat = append(lat, time.Since(unit))
	}
	elapsed := time.Since(start)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration { return lat[min(len(lat)-1, int(float64(len(lat))*p))] }
	r := Report{Target: t.Name(), Commits: o.Commits, Documents: o.Documents, Lines: o.Lines, Batch: o.Batch,
		Elapsed: elapsed, PerSecond: float64(o.Commits) / elapsed.Seconds(),
		P50: pct(0.50), P90: pct(0.90), P99: pct(0.99), Max: lat[len(lat)-1]}
	if err := release(); err != nil {
		return r, err
	}
	r.FileBytes = t.FileSize(path)
	return r, nil
}

// Verify checks the invariants the store will rely on after a run: one
// current row per document, one history row per commit, Lines child rows
// per version, one event and one event_docs row per commit, and every
// current row's version equal to its last history row's.
func Verify(ctx context.Context, db *sql.DB, o Options) error {
	count := func(q string) (int64, error) {
		var n int64
		err := db.QueryRowContext(ctx, q).Scan(&n)
		return n, err
	}
	checks := []struct {
		q    string
		want int64
	}{
		{"SELECT count(*) FROM doc_Invoice", int64(o.Documents)},
		{"SELECT count(*) FROM doc_Invoice_history", int64(o.Commits)},
		{"SELECT count(*) FROM doc_Invoice__lines", int64(o.Commits * o.Lines)},
		{"SELECT count(*) FROM events", int64(o.Commits)},
		{"SELECT count(*) FROM event_docs", int64(o.Commits)},
		{"SELECT count(*) FROM doc_Invoice_history WHERE valid_to IS NULL", int64(o.Documents)},
		{"SELECT count(*) FROM doc_Invoice d WHERE version <> (SELECT max(version) FROM doc_Invoice_history h WHERE h.id = d.id)", 0},
	}
	for _, c := range checks {
		n, err := count(c.q)
		if err != nil {
			return fmt.Errorf("%s: %w", c.q, err)
		}
		if n != c.want {
			return fmt.Errorf("%s = %d, want %d", c.q, n, c.want)
		}
	}
	return nil
}

// FileSizeOf sums the sizes of the files at the paths that exist.
func FileSizeOf(paths ...string) int64 {
	var total int64
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return total
}
