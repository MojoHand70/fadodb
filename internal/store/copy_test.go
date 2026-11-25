package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memCopy stands in for the copy of an analytical engine (ADR-0089): it
// keeps the tables in memory, takes a feed whole or not at all, and
// counts what it was asked, so that the tests of the feed need no
// DuckDB. The real one is in the module analytics.
type memCopy struct {
	mu      sync.Mutex
	columns map[string][]CopyColumn
	rows    map[string][][]any
	marks   map[string]string
	lays    int
	feeds   int
	empties []string
	// failFeed, when it is not nil, fails the next feed after it has
	// run, as a transaction that cannot be kept does.
	failFeed error
	// failAt, when it is not 0, fails the feed that would be the
	// failAt-th the copy keeps.
	failAt int
	closed bool
}

func newMemCopy() *memCopy {
	return &memCopy{columns: map[string][]CopyColumn{}, rows: map[string][][]any{}, marks: map[string]string{}}
}

// memEngine gives the same copy at every opening, as a file beside the
// database would be found again.
type memEngine struct{ copy *memCopy }

func (e memEngine) OpenCopy(context.Context, string) (Copy, error) {
	e.copy.mu.Lock()
	defer e.copy.mu.Unlock()
	e.copy.closed = false
	return e.copy, nil
}

func (c *memCopy) Marks(context.Context) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for k, v := range c.marks {
		out[k] = v
	}
	return out, nil
}

func (c *memCopy) Lay(_ context.Context, tables []CopyTable) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lays++
	for _, t := range tables {
		have := c.columns[t.Name]
		// A column the table lacks is added at the place Lay names it,
		// and the rows there are blank in it.
		if len(have) > 0 && len(have) != len(t.Columns) {
			at := map[string]int{}
			for i, col := range have {
				at[col.Name] = i
			}
			for r, row := range c.rows[t.Name] {
				wide := make([]any, len(t.Columns))
				for i, col := range t.Columns {
					if old, ok := at[col.Name]; ok {
						wide[i] = row[old]
					}
				}
				c.rows[t.Name][r] = wide
			}
		}
		c.columns[t.Name] = append([]CopyColumn{}, t.Columns...)
	}
	return nil
}

type memWriter struct {
	c    *memCopy
	rows map[string][][]any
}

func (w *memWriter) table(name string) ([][]any, error) {
	if _, ok := w.c.columns[name]; !ok {
		return nil, fmt.Errorf("the copy has no table %s", name)
	}
	if _, ok := w.rows[name]; !ok {
		w.rows[name] = append([][]any{}, w.c.rows[name]...)
	}
	return w.rows[name], nil
}

func (w *memWriter) Append(table string, row []any) error {
	rows, err := w.table(table)
	if err != nil {
		return err
	}
	if len(row) != len(w.c.columns[table]) {
		return fmt.Errorf("%s has %d columns and was given %d values", table, len(w.c.columns[table]), len(row))
	}
	w.rows[table] = append(rows, append([]any{}, row...))
	return nil
}

func (w *memWriter) Remove(table string, ids []string) error {
	rows, err := w.table(table)
	if err != nil {
		return err
	}
	gone := map[string]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	kept := rows[:0:0]
	for _, row := range rows {
		if id, _ := row[0].(string); !gone[id] {
			kept = append(kept, row)
		}
	}
	w.rows[table] = kept
	return nil
}

func (w *memWriter) Empty(table string) error {
	if _, err := w.table(table); err != nil {
		return err
	}
	w.rows[table] = nil
	w.c.empties = append(w.c.empties, table)
	return nil
}

func (c *memCopy) Feed(_ context.Context, give func(CopyWriter) error, marks map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &memWriter{c: c, rows: map[string][][]any{}}
	before := len(c.empties)
	if err := give(w); err != nil {
		c.empties = c.empties[:before]
		return err
	}
	if c.failAt != 0 && c.feeds+1 == c.failAt {
		c.failAt = 0
		c.failFeed = errors.New("the copy could not keep the piece")
	}
	if err := c.failFeed; err != nil {
		c.failFeed = nil
		c.empties = c.empties[:before]
		return err
	}
	for name, rows := range w.rows {
		c.rows[name] = rows
	}
	for name, value := range marks {
		if value == "" {
			delete(c.marks, name)
		} else {
			c.marks[name] = value
		}
	}
	c.feeds++
	return nil
}

func (c *memCopy) Ask(context.Context, string, ...any) ([]string, [][]any, error) {
	return nil, nil, errors.New("the stand-in answers no statement")
}

func (c *memCopy) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// column answers the values of one column of a table, as texts, sorted.
func (c *memCopy) column(t *testing.T, table, column string) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	at := -1
	for i, col := range c.columns[table] {
		if col.Name == column {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the copy's table %s has no column %s: %v", table, column, c.columns[table])
	}
	var out []string
	for _, row := range c.rows[table] {
		out = append(out, fmt.Sprint(row[at]))
	}
	sort.Strings(out)
	return out
}

func (c *memCopy) count(table string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.rows[table])
}

const copyInvoice = `{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "customer": {"type": "text"},
    "net":      {"type": "money", "scale": 2},
    "due":      {"type": "date"},
    "urgent":   {"type": "bool"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int"}}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}]
}`

// copied opens a database whose program gave an analytical engine, the
// stand-in, and defines the class of the tests. The feed that runs by
// itself is set to a period no test reaches: each test feeds when it
// says so.
func copied(t *testing.T, c *memCopy) (*Store, string, context.Context) {
	t.Helper()
	s, path := openStore(t, Options{Analytics: memEngine{c}, CopyEvery: time.Hour})
	ctx := context.Background()
	// The feed of the opening has ended before the test begins.
	<-s.feeder.first
	if _, err := s.defineOpen(ctx, []byte(copyInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, path, ctx
}

func mustFeed(t *testing.T, s *Store) {
	t.Helper()
	if err := s.feed(context.Background()); err != nil {
		t.Fatalf("the feed: %v", err)
	}
}

// The copy holds every version of every document, the documents as they
// are now, and the events; a feed that finds nothing new writes nothing.
func TestCopy_HoldsWhatTheDatabaseHolds(t *testing.T) {
	c := newMemCopy()
	s, _, ctx := copied(t, c)
	for i, customer := range []string{"Globex", "Acme", "Globex"} {
		values := Values{"number": fmt.Sprintf("FV-%d", i+1), "customer": customer, "net": "100.00 PLN", "due": "2026-12-01"}
		if i < 2 {
			values["urgent"] = i == 0
		}
		if _, err := s.Create(ctx, "Invoice", values, WithID(fmt.Sprintf("i-%d", i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(ctx, "Invoice", "i-1", "file", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Invoice", "i-2", Values{"net": "250.50 PLN"}); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if got := strings.Join(c.column(t, "Invoice", "id"), " "); got != "i-1 i-2 i-3" {
		t.Errorf("the invoices as they are now: %s", got)
	}
	if got := strings.Join(c.column(t, "Invoice", "state"), " "); got != "draft draft review" {
		t.Errorf("their states: %s", got)
	}
	// An exact value is its mantissa and a date the number of its day,
	// as in the store.
	if got := strings.Join(c.column(t, "Invoice", "net"), " "); got != "10000 10000 25050" {
		t.Errorf("their amounts: %s", got)
	}
	if got := c.column(t, "Invoice", "due"); len(got) != 3 || got[0] != got[2] || got[0] == "<nil>" {
		t.Errorf("their dates: %v", got)
	}
	// A true or a false is handed over as one, and a blank as a blank.
	if got := strings.Join(c.column(t, "Invoice", "urgent"), " "); got != "<nil> false true" {
		t.Errorf("what the copy was handed for true and false: %s", got)
	}
	kinds := map[string]string{}
	for _, col := range c.columns["Invoice_versions"] {
		kinds[col.Name] = col.Type
	}
	for name, want := range map[string]string{"number": CopyText, "net": CopyWhole, "net_currency": CopyText, "due": CopyDay, "urgent": CopyBool,
		"version": CopyWhole, "valid_from": CopyWhole, "created_at": CopyWhole, "event_id": CopyText, "state": CopyText} {
		if kinds[name] != want {
			t.Errorf("the column %s of the versions is laid as %q, want %q", name, kinds[name], want)
		}
	}
	if _, has := kinds["valid_to"]; has {
		t.Errorf("when a version ended is copied")
	}
	if n := c.count("Invoice_versions"); n != 5 {
		t.Errorf("%d versions in the copy, want 5", n)
	}
	var events int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM "events"`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if n := c.count("events"); n != events || events < 5 {
		t.Errorf("%d events in the copy and %d in the database", n, events)
	}
	// The list of a document is not in the copy: ADR-0089 leaves it out.
	for _, col := range c.columns["Invoice"] {
		if col.Name == "lines" {
			t.Errorf("the copy has a column for the list")
		}
	}
	// Nothing new: nothing is written.
	fed := c.feeds
	mustFeed(t, s)
	if c.feeds != fed {
		t.Errorf("a feed with nothing new wrote to the copy")
	}
	if st := s.CopyState(); !st.Kept || st.Err != nil || st.Feeds == 0 {
		t.Errorf("the state of the copy: %+v", st)
	}
	// A document that is removed leaves the documents as they are now,
	// and its versions stay.
	if err := s.Delete(ctx, "Invoice", "i-3"); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if got := strings.Join(c.column(t, "Invoice", "id"), " "); got != "i-1 i-2" {
		t.Errorf("the invoices after one was removed: %s", got)
	}
	if n := c.count("Invoice_versions"); n != 5 {
		t.Errorf("%d versions after one invoice was removed, want 5", n)
	}
}

// The copy is found again when the database is opened again, and is fed
// only what was written since. A copy that is lost is made again whole,
// and so is one that was fed from another database.
func TestCopy_FoundAgainLostAndForeign(t *testing.T) {
	c := newMemCopy()
	s, path, ctx := copied(t, c)
	for i := 1; i <= 4; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"number": fmt.Sprintf("FV-%d", i)}, WithID(fmt.Sprintf("i-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	// Closing feeds the copy a last time.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := c.count("Invoice_versions"); n != 4 || !c.closed {
		t.Fatalf("after the database closed the copy has %d versions, closed %v", n, c.closed)
	}
	reopen := func(with *memCopy) *Store {
		t.Helper()
		again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}, Analytics: memEngine{with}, CopyEvery: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { again.Close() })
		<-again.feeder.first
		return again
	}
	again := reopen(c)
	if _, err := again.Transition(ctx, "Invoice", "i-1", "file", nil); err != nil {
		t.Fatal(err)
	}
	empties := len(c.empties)
	mustFeed(t, again)
	if n := c.count("Invoice_versions"); n != 5 {
		t.Errorf("%d versions after the database was opened again and one invoice moved, want 5", n)
	}
	if len(c.empties) != empties {
		t.Errorf("a copy that was found again was emptied: %v", c.empties[empties:])
	}
	if got := strings.Join(c.column(t, "Invoice", "state"), " "); got != "draft draft draft review" {
		t.Errorf("the states: %s", got)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}

	// The copy is lost: a new one is fed everything.
	fresh := newMemCopy()
	third := reopen(fresh)
	mustFeed(t, third)
	if n, now := fresh.count("Invoice_versions"), fresh.count("Invoice"); n != 5 || now != 4 {
		t.Errorf("a copy made again has %d versions and %d invoices, want 5 and 4", n, now)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}

	// A copy that names an event the database does not have was fed
	// from another database, or from a later moment than a backup
	// brought back: it is emptied and made again.
	fresh.mu.Lock()
	fresh.marks[copyMarkEvent] = "3 01ZZZZZZZZZZZZZZZZZZZZZZZZ"
	fresh.rows["Invoice"] = append(fresh.rows["Invoice"], append([]any{"a-stranger"}, make([]any, len(fresh.columns["Invoice"])-1)...))
	fresh.mu.Unlock()
	fourth := reopen(fresh)
	mustFeed(t, fourth)
	if got := strings.Join(fresh.column(t, "Invoice", "id"), " "); got != "i-1 i-2 i-3 i-4" {
		t.Errorf("a copy of another database after it was made again: %s", got)
	}
	if n := fresh.count("Invoice_versions"); n != 5 {
		t.Errorf("%d versions after the copy was made again, want 5", n)
	}

	if err := fourth.Close(); err != nil {
		t.Fatal(err)
	}

	// The mark of a class that names a version the class does not hold
	// at that place: the copy of that class alone is made again.
	fresh.mu.Lock()
	fresh.marks[copyMarkClass+"Invoice"] = "2 01ZZZZZZZZZZZZZZZZZZZZZZZZ"
	fresh.rows["Invoice_versions"] = fresh.rows["Invoice_versions"][:2]
	events := len(fresh.rows["events"])
	fresh.mu.Unlock()
	fifth := reopen(fresh)
	if _, err := fifth.Create(ctx, "Invoice", Values{"number": "FV-5"}, WithID("i-5")); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, fifth)
	if n, now := fresh.count("Invoice_versions"), fresh.count("Invoice"); n != 6 || now != 5 {
		t.Errorf("a class made again in the copy has %d versions and %d invoices, want 6 and 5", n, now)
	}
	if n := fresh.count("events"); n != events+1 {
		t.Errorf("the events were fed again with the class: %d, want %d", n, events+1)
	}
}

// A feed is kept whole or not at all, and a copy that fails never fails
// a commit.
func TestCopy_AFeedThatFails(t *testing.T) {
	c := newMemCopy()
	s, _, ctx := copied(t, c)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-1"}, WithID("i-1")); err != nil {
		t.Fatal(err)
	}
	c.failFeed = errors.New("the disk of the copy is full")
	if err := s.feed(ctx); err == nil {
		t.Fatal("a feed the copy could not keep reported nothing")
	}
	if st := s.CopyState(); st.Err == nil || !strings.Contains(st.Err.Error(), "disk of the copy") {
		t.Errorf("the state after a feed that failed: %+v", st)
	}
	if n := c.count("Invoice_versions"); n != 0 {
		t.Errorf("a feed that failed left %d versions in the copy", n)
	}
	// The database goes on, and the next feed gives everything.
	if _, err := s.Transition(ctx, "Invoice", "i-1", "file", nil); err != nil {
		t.Fatalf("a commit after the copy failed: %v", err)
	}
	mustFeed(t, s)
	if n := c.count("Invoice_versions"); n != 2 {
		t.Errorf("%d versions after the next feed, want 2", n)
	}
	if st := s.CopyState(); st.Err != nil {
		t.Errorf("the state after a feed that worked: %+v", st)
	}
}

// A class that gains a variable gains a column in the copy, and many
// new versions at once have the documents as they are now read whole.
func TestCopy_AClassChangesAndALongLoad(t *testing.T) {
	c := newMemCopy()
	s, _, ctx := copied(t, c)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-1", "customer": "Globex"}, WithID("i-1")); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	second := strings.Replace(strings.Replace(copyInvoice, `"name": "Invoice",`, `"name": "Invoice", "version": 2,`, 1), `"customer": {"type": "text"},`, `"customer": {"type": "text"}, "region": {"type": "text"},`, 1)
	if _, err := s.defineOpen(ctx, []byte(second), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-2", "region": "north"}, WithID("i-2")); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if got := strings.Join(c.column(t, "Invoice", "region"), " "); got != "<nil> north" {
		t.Errorf("the new column in the copy: %s", got)
	}
	if got := strings.Join(c.column(t, "Invoice_versions", "region"), " "); got != "<nil> north" {
		t.Errorf("the new column among the versions: %s", got)
	}
	if got := strings.Join(c.column(t, "Invoice", "customer"), " "); got != "<nil> Globex" {
		t.Errorf("the column that was there: %s", got)
	}

	// More new versions than the feed keeps ids for: the documents as
	// they are now are read whole.
	old := copyRemakeNow
	copyRemakeNow = 3
	defer func() { copyRemakeNow = old }()
	for i := 3; i <= 8; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"number": fmt.Sprintf("FV-%d", i)}, WithID(fmt.Sprintf("i-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	empties := len(c.empties)
	mustFeed(t, s)
	if got := c.empties[empties:]; len(got) != 1 || got[0] != "Invoice" {
		t.Errorf("after many new versions the feed emptied %v, want the invoices as they are now alone", got)
	}
	if n, v := c.count("Invoice"), c.count("Invoice_versions"); n != 8 || v != 8 {
		t.Errorf("%d invoices and %d versions in the copy, want 8 and 8", n, v)
	}
}

// A database opened without an analytical engine keeps no copy.
func TestCopy_NoneWithoutAnEngine(t *testing.T) {
	s, _ := openStore(t, Options{})
	if st := s.CopyState(); st.Kept || st.Err != nil {
		t.Errorf("the state of a copy nobody asked for: %+v", st)
	}
	if err := s.feed(context.Background()); err != nil {
		t.Errorf("a feed without a copy: %v", err)
	}
}

// A long load is copied in pieces, each kept when it is done. When a
// piece fails, the pieces before it stay, and the next feed goes on
// from them; the documents as they are now are given with the last.
func TestCopy_ALongLoadInPieces(t *testing.T) {
	c := newMemCopy()
	s, _, ctx := copied(t, c)
	old := copySlice
	copySlice = 3
	defer func() { copySlice = old }()
	for i := 1; i <= 8; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"number": fmt.Sprintf("FV-%d", i)}, WithID(fmt.Sprintf("i-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(ctx, "Invoice", "i-8", "file", nil); err != nil {
		t.Fatal(err)
	}
	// Nine versions in pieces of three: the second piece fails.
	c.mu.Lock()
	c.failAt = c.feeds + 2
	c.mu.Unlock()
	if err := s.feed(ctx); err == nil || !strings.Contains(err.Error(), "could not keep the piece") {
		t.Fatalf("a feed whose second piece failed answered %v", err)
	}
	if n := c.count("Invoice_versions"); n != 3 {
		t.Errorf("after the second piece failed the copy has %d versions, want the three of the first", n)
	}
	if mark := c.marks[copyMarkClass+"Invoice"]; !strings.HasSuffix(mark, " "+copyPartial) {
		t.Errorf("the mark of a class in the middle of its load: %q", mark)
	}
	if n := c.count("Invoice"); n != 0 {
		t.Errorf("the documents as they are now were given before the last piece: %d", n)
	}
	mustFeed(t, s)
	if n, now := c.count("Invoice_versions"), c.count("Invoice"); n != 9 || now != 8 {
		t.Errorf("after the load the copy has %d versions and %d invoices, want 9 and 8", n, now)
	}
	if mark := c.marks[copyMarkClass+"Invoice"]; strings.HasSuffix(mark, copyPartial) || mark == "" {
		t.Errorf("the mark of the class after its load: %q", mark)
	}
	if got := strings.Join(c.column(t, "Invoice", "state"), " "); got != "draft draft draft draft draft draft draft review" {
		t.Errorf("the states after the load: %s", got)
	}
	var events int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM "events"`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if n := c.count("events"); n != events {
		t.Errorf("%d events in the copy and %d in the database", n, events)
	}
	// One more invoice after the load is fed as a few versions are: by
	// its id, with nothing emptied.
	empties := len(c.empties)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-9"}, WithID("i-9")); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if n, now := c.count("Invoice_versions"), c.count("Invoice"); n != 10 || now != 9 || len(c.empties) != empties {
		t.Errorf("after one more invoice the copy has %d versions and %d invoices, and emptied %v", n, now, c.empties[empties:])
	}
}
