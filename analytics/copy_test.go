package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The copy of the documents in DuckDB (ADR-0089), fed by a real
// database. The tests of the feed itself, with a stand-in for the copy,
// are in the core.

const copyInvoice = `{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "customer": {"type": "text"},
    "net":      {"type": "money", "scale": 2},
    "rate":     {"type": "number"},
    "due":      {"type": "date"},
    "urgent":   {"type": "bool"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}],
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}
}`

var olga = fado.As(fado.Principal{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners, fado.GroupAdministrators}})

// opened opens a database whose program gives DuckDB as its analytical
// engine. The feed that runs by itself is set to a period no test
// reaches: a test feeds when it says so, and the closing feeds once
// more.
func opened(t testing.TB, path string) *fado.DB {
	t.Helper()
	db, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// asked opens the copy of a closed database as an analyst's tool would,
// to read, and answers the first value of a statement.
func asked(t testing.TB, database, statement string) string {
	t.Helper()
	d, err := sql.Open("duckdb", CopyPath(database)+"?access_mode=read_only")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var v any
	if err := d.QueryRow(statement).Scan(&v); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return fmt.Sprint(v)
}

func TestCopy_OfARealDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	db := opened(t, path)
	if _, err := db.DefineClass(ctx, []byte(copyInvoice), olga); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 1; i <= 20; i++ {
		customer := "Globex"
		if i%4 == 0 {
			customer = "Initech"
		}
		d, err := db.Create(ctx, "Invoice", fado.Values{"number": fmt.Sprintf("FV-%d", i), "customer": customer,
			"net": fmt.Sprintf("%d.50 PLN", 100*i), "rate": 0.23, "due": "2026-12-01", "urgent": i%2 == 0}, olga)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	if _, err := db.TransitionMany(ctx, "Invoice", "file", ids[:10], nil, olga); err != nil {
		t.Fatal(err)
	}
	if err := db.FeedCopy(ctx); err != nil {
		t.Fatalf("the feed: %v", err)
	}
	if _, err := db.TransitionMany(ctx, "Invoice", "approve", ids[:5], nil, olga); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "Invoice", ids[19], olga); err != nil {
		t.Fatal(err)
	}
	if st := db.CopyState(); !st.Kept || st.Err != nil || st.Feeds == 0 {
		t.Fatalf("the state of the copy: %+v", st)
	}
	// Closing brings the copy up to the database a last time.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for statement, want := range map[string]string{
		`SELECT count(*) FROM "Invoice"`:                                      "19",
		`SELECT count(*) FROM "Invoice_versions"`:                             "35",
		`SELECT count(*) FROM "Invoice" WHERE "state" = 'paid'`:               "5",
		`SELECT count(*) FROM "Invoice" WHERE "state" = 'review'`:             "5",
		`SELECT count(*) FROM "Invoice" WHERE "customer" = 'Initech'`:         "4",
		`SELECT sum("net") FROM "Invoice" WHERE "number" IN ('FV-1', 'FV-2')`: "30100",
		`SELECT count(DISTINCT "net_currency") FROM "Invoice"`:                "1",
		`SELECT round(sum("rate"), 2) FROM "Invoice"`:                         "4.37",
		`SELECT count(*) FROM "Invoice" WHERE "urgent"`:                       "9",
		`SELECT count(*) FROM "Invoice" WHERE NOT "urgent"`:                   "10",
		`SELECT typeof("urgent") || ' ' || typeof("due") || ' ' || typeof("net") || ' ' || typeof("rate") || ' ' || typeof("created_at") FROM "Invoice" LIMIT 1`: "BOOLEAN INTEGER BIGINT DOUBLE BIGINT",
		`SELECT CAST(DATE '1970-01-01' + max("due") AS VARCHAR) FROM "Invoice"`:                                                                                  "2026-12-01",
		`SELECT count(DISTINCT "due") FROM "Invoice"`:                                                                                                            "1",
		`SELECT count(*) FROM "events" WHERE "class" = 'Invoice' AND "kind" = 'transition'`:                                                                      "15",
		`SELECT count(*) FROM "events" WHERE "class" = 'Invoice' AND "kind" = 'deleted'`:                                                                         "1",
		`SELECT max("version") FROM "Invoice_versions" WHERE "number" = 'FV-1'`:                                                                                  "3",
		`SELECT count(*) FROM "Invoice_versions" WHERE "created_by" = 'olga'`:                                                                                    "35",
		`SELECT count(*) FROM (SELECT "id" FROM "Invoice_versions" GROUP BY 1)`:                                                                                  "20",
		`SELECT count(*) FROM "Invoice" i JOIN "Invoice_versions" v USING ("id", "version")`:                                                                     "19",
	} {
		if got := asked(t, path, statement); got != want {
			t.Errorf("%s: %s, want %s", statement, got, want)
		}
	}

	// The database is opened again: the copy is found, and is fed only
	// what is written since.
	again := opened(t, path)
	if _, err := again.Create(ctx, "Invoice", fado.Values{"number": "FV-21"}, olga); err != nil {
		t.Fatal(err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	if got := asked(t, path, `SELECT count(*) FROM "Invoice_versions"`); got != "36" {
		t.Errorf("%s versions after the database was opened again and one invoice made, want 36", got)
	}
	if got := asked(t, path, `SELECT count(*) FROM "Invoice"`); got != "20" {
		t.Errorf("%s invoices after the database was opened again, want 20", got)
	}

	// The copy is lost, and then it is a file that does not open: both
	// times the database makes it again, whole.
	for name, harm := range map[string]func(){
		"a copy that is lost":           func() { os.Remove(CopyPath(path)) },
		"a copy that is no DuckDB file": func() { os.WriteFile(CopyPath(path), []byte("this is no database"), 0o600) },
	} {
		harm()
		os.Remove(CopyPath(path) + ".wal")
		third := opened(t, path)
		if err := third.FeedCopy(ctx); err != nil {
			t.Fatalf("%s: the feed: %v", name, err)
		}
		if err := third.Close(); err != nil {
			t.Fatal(err)
		}
		if got := asked(t, path, `SELECT count(*) FROM "Invoice_versions"`); got != "36" {
			t.Errorf("%s: %s versions after it was made again, want 36", name, got)
		}
		if got := asked(t, path, `SELECT count(*) FROM "Invoice"`); got != "20" {
			t.Errorf("%s: %s invoices after it was made again, want 20", name, got)
		}
	}
}

// One opening of a database feeds its copy. A second opening in the
// same program works without one, and says why.
func TestCopy_OneOpeningFeedsIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	first := opened(t, path)
	if _, err := first.DefineClass(ctx, []byte(copyInvoice), olga); err != nil {
		t.Fatal(err)
	}
	second, err := fado.Open(path, fado.Options{Analytics: Engine{}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("a second opening with the engine is refused: %v", err)
	}
	if st := second.CopyState(); st.Kept || st.Err == nil || !strings.Contains(st.Err.Error(), "open in this program already") {
		t.Errorf("the state of the copy for a second opening: %+v", st)
	}
	if _, err := second.Create(ctx, "Invoice", fado.Values{"number": "FV-1"}, olga); err != nil {
		t.Errorf("a second opening without a copy creates no document: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.FeedCopy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := asked(t, path, `SELECT count(*) FROM "Invoice_versions"`); got != "1" {
		t.Errorf("%s versions in the copy, want the one", got)
	}
	// Once the first has closed, the copy is free again.
	third := opened(t, path)
	if st := third.CopyState(); !st.Kept || st.Err != nil {
		t.Errorf("the state of the copy after the first opening closed: %+v", st)
	}
}

// A class gains a variable, and a variable changes its type while no
// document holds a value of it: the copy follows.
func TestCopy_FollowsAClassThatChanges(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	db := opened(t, path)
	if _, err := db.DefineClass(ctx, []byte(copyInvoice), olga); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV-1", "customer": "Globex"}, olga); err != nil {
		t.Fatal(err)
	}
	if err := db.FeedCopy(ctx); err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(copyInvoice, `"name": "Invoice",`, `"name": "Invoice", "version": 2,`, 1)
	second = strings.Replace(second, `"customer": {"type": "text"},`, `"customer": {"type": "text"}, "region": {"type": "text"},`, 1)
	// No invoice holds a rate: it may become a text.
	second = strings.Replace(second, `"rate":     {"type": "number"},`, `"rate":     {"type": "text"},`, 1)
	if _, err := db.DefineClass(ctx, []byte(second), olga); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV-2", "region": "north", "rate": "a fifth"}, olga); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for statement, want := range map[string]string{
		`SELECT count(*) FROM "Invoice" WHERE "region" = 'north'`:          "1",
		`SELECT count(*) FROM "Invoice" WHERE "region" IS NULL`:            "1",
		`SELECT count(*) FROM "Invoice_versions" WHERE "rate" = 'a fifth'`: "1",
		`SELECT count(*) FROM "Invoice" WHERE "customer" = 'Globex'`:       "1",
	} {
		if got := asked(t, path, statement); got != want {
			t.Errorf("%s: %s, want %s", statement, got, want)
		}
	}
}

// A feed is kept whole or not at all: the rows an appender was handed
// are the transaction's, and go with it.
func TestCopy_AFeedIsWholeOrNothing(t *testing.T) {
	ctx := context.Background()
	c, err := Engine{}.OpenCopy(ctx, filepath.Join(t.TempDir(), "acme.fado"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	table := fado.CopyTable{Name: "Invoice", Columns: []fado.CopyColumn{{Name: "id", Type: fado.CopyText}, {Name: "net", Type: fado.CopyWhole}, {Name: "rate", Type: fado.CopyNumber}}}
	if err := c.Lay(ctx, []fado.CopyTable{table}); err != nil {
		t.Fatal(err)
	}
	count := func() string {
		t.Helper()
		_, rows, err := c.Ask(ctx, `SELECT count(*), coalesce(sum("net"), 0) FROM "Invoice"`)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(rows[0]...)
	}
	give := func(n int, fail error) func(fado.CopyWriter) error {
		return func(w fado.CopyWriter) error {
			for i := 0; i < n; i++ {
				if err := w.Append("Invoice", []any{fmt.Sprintf("i-%d", i), int64(100), 0.5}); err != nil {
					return err
				}
			}
			return fail
		}
	}
	if err := c.Feed(ctx, give(3000, nil), map[string]string{"event": "3000 e"}); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != "3000 300000" {
		t.Fatalf("after the first feed the copy holds %s", got)
	}
	// A feed that fails after it handed rows over leaves nothing, and
	// the mark is as it was.
	broken := errors.New("the database could not be read to its end")
	if err := c.Feed(ctx, give(5000, broken), map[string]string{"event": "8000 f"}); !errors.Is(err, broken) {
		t.Fatalf("a feed that failed answered %v", err)
	}
	if got := count(); got != "3000 300000" {
		t.Errorf("a feed that failed left the copy with %s", got)
	}
	if marks, err := c.Marks(ctx); err != nil || marks["event"] != "3000 e" {
		t.Errorf("the marks after a feed that failed: %v, %v", marks, err)
	}
	// Rows are taken out and put back in one feed; a mark given empty
	// is taken away.
	err = c.Feed(ctx, func(w fado.CopyWriter) error {
		if err := w.Remove("Invoice", []string{"i-0", "i-1", "i-2999", "no such invoice"}); err != nil {
			return err
		}
		if err := w.Append("Invoice", []any{"i-0", int64(7), nil}); err != nil {
			return err
		}
		return w.Append("Invoice", []any{"i-new", nil, 1.5})
	}, map[string]string{"event": "", "class:Invoice": "3 g"})
	if err != nil {
		t.Fatal(err)
	}
	if got := count(); got != "2999 299707" {
		t.Errorf("after rows were taken out and put back the copy holds %s", got)
	}
	if marks, err := c.Marks(ctx); err != nil || len(marks) != 1 || marks["class:Invoice"] != "3 g" {
		t.Errorf("the marks: %v, %v", marks, err)
	}
	// Many rows are taken out in one statement, and what was appended
	// before in the same feed goes with them when it is named.
	var many []string
	err = c.Feed(ctx, func(w fado.CopyWriter) error {
		for i := 0; i < 1500; i++ {
			id := fmt.Sprintf("m-%d", i)
			many = append(many, id)
			if err := w.Append("Invoice", []any{id, int64(1), nil}); err != nil {
				return err
			}
		}
		return w.Remove("Invoice", many[:1200])
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(); got != "3299 300007" {
		t.Errorf("after 1,200 of 1,500 rows were taken out again the copy holds %s", got)
	}
	// A table is emptied and filled in one feed.
	err = c.Feed(ctx, func(w fado.CopyWriter) error {
		if err := w.Append("Invoice", []any{"i-late", int64(1), nil}); err != nil {
			return err
		}
		if err := w.Empty("Invoice"); err != nil {
			return err
		}
		return w.Append("Invoice", []any{"i-only", int64(9), nil})
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(); got != "1 9" {
		t.Errorf("after the table was emptied and filled the copy holds %s", got)
	}
	// A row for a table that was never laid is refused.
	if err := c.Feed(ctx, func(w fado.CopyWriter) error { return w.Append("Order", []any{"o-1"}) }, nil); err == nil {
		t.Errorf("a row of a table that was never laid was taken")
	}
}

// BenchmarkFeed is bringing the copy up to a database after a thousand
// new versions (ADR-0089, Performance): the budget is under 50 ms. The
// thousand moves themselves are outside the measured time.
func BenchmarkFeed(b *testing.B) {
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "acme.fado")
	db := opened(b, path)
	// A ticket goes between two states for as long as the benchmark
	// asks.
	ticket := `{"name": "Ticket", "title": "number",
 "variables": {"number": {"type": "text", "required": true}, "customer": {"type": "text"}, "net": {"type": "money", "scale": 2}},
 "states": {"initial": "open", "list": ["open", "held"]},
 "transitions": [{"name": "hold", "from": "open", "to": "held"}, {"name": "release", "from": "held", "to": "open"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	if _, err := db.DefineClass(ctx, []byte(ticket), olga); err != nil {
		b.Fatal(err)
	}
	// A class that holds documents already: the feed takes the ones a
	// thousand versions changed out of them and puts them back.
	const held, batch = 20000, 1000
	ids := make([]string, 0, held)
	for have := 0; have < held; have += batch {
		docs := make([]fado.Values, batch)
		for i := range docs {
			docs[i] = fado.Values{"number": fmt.Sprintf("T-%d", have+i), "customer": "Globex", "net": "100.00 PLN"}
		}
		made, err := db.CreateMany(ctx, "Ticket", docs, olga)
		if err != nil {
			b.Fatal(err)
		}
		for _, d := range made {
			ids = append(ids, d.ID)
		}
	}
	if err := db.FeedCopy(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		at := (i * batch) % held
		move := "hold"
		if (i*batch/held)%2 == 1 {
			move = "release"
		}
		out, err := db.TransitionMany(ctx, "Ticket", move, ids[at:at+batch], nil, olga)
		if err != nil || len(out) != batch {
			b.Fatalf("the moves of the benchmark: %d, %v", len(out), err)
		}
		b.StartTimer()
		if err := db.FeedCopy(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCommitsBesideTheFeed is a thousand moves in one call, in a
// database that keeps no copy and in one whose copy is fed every tenth
// of a second (ADR-0089, Consequences): the feed reads the database as
// any reader does, and this says what that costs the writer.
func BenchmarkCommitsBesideTheFeed(b *testing.B) {
	ticket := `{"name": "Ticket", "title": "number",
 "variables": {"number": {"type": "text", "required": true}, "customer": {"type": "text"}, "net": {"type": "money", "scale": 2}},
 "states": {"initial": "open", "list": ["open", "held"]},
 "transitions": [{"name": "hold", "from": "open", "to": "held"}, {"name": "release", "from": "held", "to": "open"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	for _, tc := range []struct {
		name string
		o    fado.Options
	}{
		{"no copy", fado.Options{Outbox: fado.OutboxOptions{Manual: true}}},
		{"a copy fed every tenth of a second", fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: 100 * time.Millisecond, Outbox: fado.OutboxOptions{Manual: true}}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			db, err := fado.Open(filepath.Join(b.TempDir(), "acme.fado"), tc.o)
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if _, err := db.DefineClass(ctx, []byte(ticket), olga); err != nil {
				b.Fatal(err)
			}
			const held, batch = 20000, 1000
			ids := make([]string, 0, held)
			for have := 0; have < held; have += batch {
				docs := make([]fado.Values, batch)
				for i := range docs {
					docs[i] = fado.Values{"number": fmt.Sprintf("T-%d", have+i), "customer": "Globex", "net": "100.00 PLN"}
				}
				made, err := db.CreateMany(ctx, "Ticket", docs, olga)
				if err != nil {
					b.Fatal(err)
				}
				for _, d := range made {
					ids = append(ids, d.ID)
				}
			}
			if err := db.FeedCopy(ctx); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				at := (i * batch) % held
				move := "hold"
				if (i*batch/held)%2 == 1 {
					move = "release"
				}
				if out, err := db.TransitionMany(ctx, "Ticket", move, ids[at:at+batch], nil, olga); err != nil || len(out) != batch {
					b.Fatalf("the moves of the benchmark: %d, %v", len(out), err)
				}
			}
			b.StopTimer()
			if st := db.CopyState(); st.Err != nil {
				b.Fatalf("the copy: %v", st.Err)
			}
		})
	}
}
