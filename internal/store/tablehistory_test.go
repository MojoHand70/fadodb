package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The history of a table's rows (ADR-0099, step 3).

const historyRates = `{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(4)"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`

// historyClock is a clock that starts on 2 December 2026 at 09:00 UTC.
func historyClock() *still {
	return &still{now: time.Date(2026, 12, 2, 9, 0, 0, 0, time.UTC)}
}

// at sets the clock to an hour and minute of 2 December.
func (c *still) at(hour, minute int) {
	c.set(time.Date(2026, 12, 2, hour, minute, 0, 0, time.UTC))
}

// change names a change of a table by its number: the time of day, its
// kind and who made it, or "before" for none.
func change(t *testing.T, s *Store, seq sql.NullString) string {
	t.Helper()
	if !seq.Valid {
		return "before"
	}
	var id, kind, by sql.NullString
	if err := s.db.QueryRow("SELECT event_id, kind, actor_id FROM fado_table_changes WHERE seq = ?", seq.String).Scan(&id, &kind, &by); err != nil {
		t.Fatalf("the change %s: %v", seq.String, err)
	}
	return time.UnixMilli(idMillis(id.String)).UTC().Format("15:04") + " " + kind.String + " " + by.String
}

// rowsOfTable reads the rows of a table with the change that gave each
// its values, in key order: "EUR 43100 since 10:00 import tess".
func rowsOfTable(t *testing.T, s *Store, table string, cols ...string) []string {
	t.Helper()
	return historyLines(t, s, "SELECT "+strings.Join(quoteAll(cols), ", ")+", "+quote(sinceColumn)+", NULL FROM "+quote("tbl_"+table)+" ORDER BY 1", len(cols))
}

// pastOfTable reads the history of a table in the order it was kept:
// "EUR 42500 since 09:00 import tess until 10:00 import tess".
func pastOfTable(t *testing.T, s *Store, table string, cols ...string) []string {
	t.Helper()
	return historyLines(t, s, "SELECT "+strings.Join(quoteAll(cols), ", ")+", "+quote(sinceColumn)+", "+quote(untilColumn)+" FROM "+quote("tbl_"+table+historySuffix)+" ORDER BY rowid", len(cols))
}

func historyLines(t *testing.T, s *Store, text string, n int) []string {
	t.Helper()
	rows, err := s.db.Query(text)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		cells := make([]sql.NullString, n+2)
		ptrs := make([]any, len(cells))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, c := range cells[:n] {
			parts = append(parts, c.String)
		}
		line := strings.Join(parts, " ") + " since " + change(t, s, cells[n])
		if cells[n+1].Valid {
			line += " until " + change(t, s, cells[n+1])
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameLines(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// changesOf lists the changes of a table in their order: "11:00 import tess".
func changesOf(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query("SELECT seq FROM fado_table_changes WHERE table_key = ? ORDER BY seq", strings.ToUpper(table))
	if err != nil {
		t.Fatal(err)
	}
	var ids []sql.NullString
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = change(t, s, id)
	}
	return out
}

// Tess keeps the rates. Each import, delete and replace keeps the rows it
// changes as they were, until that change, and who made it; a row
// brought again as it was is no change, and a delete that removes
// nothing leaves none. The token of the rows is the last change, and all
// of it is there when the database is opened again.
func TestATableKeepsTheHistoryOfItsRows(t *testing.T) {
	cl := historyClock()
	s, path := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(historyRates), asSetup); err != nil {
		t.Fatal(err)
	}
	tess := As(Principal{ID: "tess", Kind: ActorPerson})
	imp := func(replace bool, rows ...Values) {
		t.Helper()
		opts := []Option{tess}
		if replace {
			opts = append(opts, Replace())
		}
		if _, err := s.ImportRows(ctx, "Rates", rows, opts...); err != nil {
			t.Fatal(err)
		}
	}
	imp(false, Values{"currency": "EUR", "rate": "4.2500"}, Values{"currency": "PLN", "rate": "1.0000"})
	cl.at(10, 0)
	imp(false, Values{"currency": "EUR", "rate": "4.3100"})
	cl.at(11, 0)
	imp(false, Values{"currency": "EUR", "rate": "4.3100"})
	cl.at(12, 0)
	if n, err := s.DeleteRows(ctx, "Rates", []Values{{"currency": "PLN"}}, tess); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	cl.at(12, 30)
	if n, err := s.DeleteRows(ctx, "Rates", []Values{{"currency": "CHF"}}, tess); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	cl.at(13, 0)
	imp(true, Values{"currency": "EUR", "rate": "4.3100"}, Values{"currency": "USD", "rate": "3.9000"})
	cl.at(14, 0)
	imp(true, Values{"currency": "USD", "rate": "3.9500"})

	check := func(s *Store) {
		t.Helper()
		sameLines(t, "the rows", rowsOfTable(t, s, "Rates", "currency", "rate"),
			"USD 39500 since 14:00 replace tess")
		sameLines(t, "the history", pastOfTable(t, s, "Rates", "currency", "rate"),
			"EUR 42500 since 09:00 import tess until 10:00 import tess",
			"PLN 10000 since 09:00 import tess until 12:00 delete tess",
			"USD 39000 since 13:00 replace tess until 14:00 replace tess",
			"EUR 43100 since 10:00 import tess until 14:00 replace tess")
		sameLines(t, "the changes", changesOf(t, s, "Rates"),
			"09:00 import tess", "10:00 import tess", "11:00 import tess", "12:00 delete tess", "13:00 replace tess", "14:00 replace tess")
		var token, last string
		if err := s.db.QueryRow("SELECT value FROM metadata WHERE key = 'rows:RATES'").Scan(&token); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("SELECT event_id FROM fado_table_changes ORDER BY seq DESC LIMIT 1").Scan(&last); err != nil || token != last {
			t.Errorf("the token of the rows is %s, the last change %s: %v", token, last, err)
		}
	}
	check(s)
	var layout int
	if err := s.db.QueryRow("PRAGMA schema_version").Scan(&layout); err != nil {
		t.Fatal(err)
	}
	// What a lookup reads is the row as it is.
	if rows, err := s.TableRows(ctx, "Rates"); err != nil || len(rows) != 1 || rows[0]["rate"] != "3.9500" {
		t.Errorf("the rows read %v, %v", rows, err)
	}
	// Opened again: the same, and the layout is not written to.
	s.Close()
	again, err := Open(path, Options{Now: cl.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	check(again)
	var reopened int
	if err := again.db.QueryRow("PRAGMA schema_version").Scan(&reopened); err != nil || reopened != layout {
		t.Errorf("opening the database again wrote its layout: %d, then %d, %v", layout, reopened, err)
	}
	cl.at(15, 0)
	if _, err := again.ImportRows(ctx, "Rates", []Values{{"currency": "USD", "rate": "3.9600"}}, tess); err != nil {
		t.Fatal(err)
	}
	sameLines(t, "the rows, opened again", rowsOfTable(t, again, "Rates", "currency", "rate"), "USD 39600 since 15:00 import tess")
	if got := pastOfTable(t, again, "Rates", "currency", "rate"); len(got) != 5 || got[4] != "USD 39500 since 14:00 replace tess until 15:00 import tess" {
		t.Errorf("the history, opened again: %q", got)
	}
}

// A rule that writes a table keeps the history as an import does, under
// the name of its action, as the person whose commit set it off.
func TestARuleKeepsTheHistoryOfTheRowsItWrites(t *testing.T) {
	cl := historyClock()
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(historyRates), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "RateEntry",
 "variables": {"currency": {"type": "text"}, "rate": {"type": "decimal", "scale": 4}},
 "states": {"initial": "draft", "list": ["draft", "entered", "changed", "withdrawn"]},
 "transitions": [{"name": "enter", "from": "draft", "to": "entered"}, {"name": "change", "from": "entered", "to": "changed"}, {"name": "withdraw", "from": "changed", "to": "withdrawn"}],
 "rules": [
   {"on": "transition", "name": "enter", "do": [{"action": "table.insert", "table": "Rates", "row": {"currency": "currency", "rate": "rate"}}]},
   {"on": "transition", "name": "change", "do": [{"action": "table.update", "table": "Rates", "key": {"currency": "currency"}, "set": {"rate": "rate + 0.01"}}]},
   {"on": "transition", "name": "withdraw", "do": [{"action": "table.delete", "table": "Rates", "key": {"currency": "currency"}}]}
 ]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "RateEntry", Values{"currency": "EUR", "rate": "4.2500"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"enter", "change", "withdraw"} {
		cl.at(10+i, 0)
		if _, err := s.Transition(ctx, "RateEntry", doc.ID, name, nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			t.Fatalf("%s: drain %d, %v", name, n, err)
		}
	}
	sameLines(t, "the rows", rowsOfTable(t, s, "Rates", "currency", "rate"))
	sameLines(t, "the history", pastOfTable(t, s, "Rates", "currency", "rate"),
		"EUR 42500 since 10:00 table.insert alice until 11:00 table.update alice",
		"EUR 42600 since 11:00 table.update alice until 12:00 table.delete alice")
}

// A table that says "history": false keeps none: no table of it, and a
// change keeps nothing of the row as it was. Each row still says which
// change gave it its values, and who made the changes is kept.
func TestATableThatKeepsNoHistory(t *testing.T) {
	cl := historyClock()
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Prices", "columns": {"sku": "text", "price": "decimal(2)"}, "key": ["sku"], "history": false, "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	tbl, _ := s.Table("Prices")
	if tbl.History {
		t.Error("the table says it keeps its history")
	}
	tess := As(Principal{ID: "tess", Kind: ActorPerson})
	if _, err := s.ImportRows(ctx, "Prices", []Values{{"sku": "A1", "price": "7.25"}, {"sku": "B2", "price": "3.10"}}, tess); err != nil {
		t.Fatal(err)
	}
	cl.at(10, 0)
	if _, err := s.ImportRows(ctx, "Prices", []Values{{"sku": "A1", "price": "7.50"}}, tess); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'tbl_Prices%' AND type IN ('table', 'trigger')").Scan(&n); err != nil || n != 2 {
		t.Errorf("the table and its trigger are %d objects, %v", n, err)
	}
	sameLines(t, "the rows", rowsOfTable(t, s, "Prices", "sku", "price"),
		"A1 750 since 10:00 import tess", "B2 310 since 09:00 import tess")
	sameLines(t, "the changes", changesOf(t, s, "Prices"), "09:00 import tess", "10:00 import tess")
}

// A version of a table carries the history of its rows: a column added
// is blank in the past, a column whose type changes converts in the past
// as in the rows, each row keeps the change that gave it its values. A
// version that stops keeping the history drops it; one that starts
// again keeps it from then, and lineage reads the past only after.
func TestAVersionOfATableCarriesItsHistory(t *testing.T) {
	cl := historyClock()
	s, path := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(historyRates), asSetup); err != nil {
		t.Fatal(err)
	}
	tess := As(Principal{ID: "tess", Kind: ActorPerson})
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.2500"}}, tess); err != nil {
		t.Fatal(err)
	}
	cl.at(10, 0)
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.3100"}}, tess); err != nil {
		t.Fatal(err)
	}
	// A column added: blank in the past, and the triggers compare it.
	cl.at(11, 0)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(4)", "source": "text"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	cl.at(11, 30)
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.3100", "source": "ECB"}}, tess); err != nil {
		t.Fatal(err)
	}
	sameLines(t, "the history, a column added", pastOfTable(t, s, "Rates", "currency", "rate", "source"),
		"EUR 42500  since 09:00 import tess until 10:00 import tess",
		"EUR 43100  since 10:00 import tess until 11:30 import tess")
	// A type changed: the rows and the history convert, each with its
	// moments.
	cl.at(12, 0)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(6)", "source": "text"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	sameLines(t, "the rows, a type changed", rowsOfTable(t, s, "Rates", "currency", "rate", "source"), "EUR 4310000 ECB since 11:30 import tess")
	sameLines(t, "the history, a type changed", pastOfTable(t, s, "Rates", "currency", "rate", "source"),
		"EUR 4250000  since 09:00 import tess until 10:00 import tess",
		"EUR 4310000  since 10:00 import tess until 11:30 import tess")
	// A type that would lose a value of the past is refused, and says so.
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(1)", "source": "text"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err == nil ||
		!strings.Contains(err.Error(), "do not convert") {
		t.Errorf("a type that loses values: %v", err)
	}
	// No more history: dropped. And again: from then.
	cl.at(13, 0)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(6)", "source": "text"}, "key": ["currency"], "history": false, "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'tbl_Rates__history'").Scan(&n); err != nil || n != 0 {
		t.Errorf("the history after history false: %d, %v", n, err)
	}
	cl.at(14, 0)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(6)", "source": "text"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	var began string
	if err := s.db.QueryRow("SELECT value FROM metadata WHERE key = 'history:RATES'").Scan(&began); err != nil || time.UnixMilli(idMillis(began)).UTC().Format("15:04") != "14:00" {
		t.Errorf("the history began %s, %v", began, err)
	}
	// The store's ids never go back in time: the moments are made apart.
	var ids idGen
	for _, at := range []struct {
		event string
		kept  bool
	}{{ids.next(time.Date(2026, 12, 2, 13, 30, 0, 0, time.UTC)), false}, {ids.next(time.Date(2026, 12, 2, 14, 30, 0, 0, time.UTC)), true}} {
		tbl, _ := s.table("Rates")
		if kept, err := s.historyAt(ctx, tbl, at.event); err != nil || kept != at.kept {
			t.Errorf("the history reaches %s: %v, %v", at.event, kept, err)
		}
	}
	cl.at(15, 0)
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.3300", "source": "ECB"}}, tess); err != nil {
		t.Fatal(err)
	}
	sameLines(t, "the history, kept again", pastOfTable(t, s, "Rates", "currency", "rate", "source"),
		"EUR 4310000 ECB since 11:30 import tess until 15:00 import tess")
	// A version that changes a type and stops the history keeps no
	// moment of it; one that changes a type and starts it again keeps
	// it from then.
	cl.at(15, 30)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(8)", "source": "text"}, "key": ["currency"], "history": false, "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM metadata WHERE key = 'history:RATES'").Scan(&n); err != nil || n != 0 {
		t.Errorf("a rebuilt table with no history keeps %d moments of it, %v", n, err)
	}
	cl.at(16, 0)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(10)", "source": "text"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT value FROM metadata WHERE key = 'history:RATES'").Scan(&began); err != nil || time.UnixMilli(idMillis(began)).UTC().Format("15:04") != "16:00" {
		t.Errorf("the history of a rebuilt table began %s, %v", began, err)
	}
	sameLines(t, "the rows, rebuilt twice", rowsOfTable(t, s, "Rates", "currency", "rate"), "EUR 43300000000 since 15:00 import tess")
	// The table removed: its history goes with it.
	if err := s.RemoveTable(ctx, "Rates", asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'tbl_Rates%'").Scan(&n); err != nil || n != 0 {
		t.Errorf("the table removed leaves %d objects, %v", n, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM metadata WHERE key IN ('rows:RATES', 'history:RATES')").Scan(&n); err != nil || n != 0 {
		t.Errorf("the table removed leaves %d keys, %v", n, err)
	}
	s.Close()
	if again, err := Open(path, Options{Now: cl.Now}); err != nil {
		t.Fatal(err)
	} else {
		again.Close()
	}
}

// A database of schema 15 is brought to 16 when it is opened: every
// table gains the moment of its rows and its history, which begins
// then. Its rows have no moment; a change after keeps them as they were,
// "before".
func TestATableFromSchema15KeepsItsHistory(t *testing.T) {
	cl := historyClock()
	s, path := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(historyRates), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.2500"}}, asSetup); err != nil {
		t.Fatal(err)
	}
	// The layout of schema 15: no history, no moments, no changes, and
	// no seal on the events (ADR-0103).
	for _, text := range []string{
		`DROP TRIGGER "tbl_Rates__changed"`, `DROP TRIGGER "tbl_Rates__since"`, `DROP TRIGGER "tbl_Rates__removed"`, `DROP TABLE "tbl_Rates__history"`,
		`ALTER TABLE "tbl_Rates" DROP COLUMN "__since"`, `DROP TABLE fado_table_changes`,
		`ALTER TABLE events DROP COLUMN seal`,
		`UPDATE schema_version SET version = 15`,
	} {
		if _, err := s.writer.ExecContext(ctx, text); err != nil {
			t.Fatalf("%s: %v", text, err)
		}
	}
	s.Close()
	cl.at(10, 0)
	again, err := Open(path, Options{Now: cl.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	var version int
	if err := again.db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != engineSchema {
		t.Errorf("the schema: %d, %v", version, err)
	}
	sameLines(t, "the rows, brought", rowsOfTable(t, again, "Rates", "currency", "rate"), "EUR 42500 since before")
	var began string
	if err := again.db.QueryRow("SELECT value FROM metadata WHERE key = 'history:RATES'").Scan(&began); err != nil || time.UnixMilli(idMillis(began)).UTC().Format("15:04") != "10:00" {
		t.Errorf("the history began %s, %v", began, err)
	}
	cl.at(11, 0)
	if _, err := again.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.3100"}}, As(Principal{ID: "tess"})); err != nil {
		t.Fatal(err)
	}
	sameLines(t, "the history, brought", pastOfTable(t, again, "Rates", "currency", "rate"), "EUR 42500 since before until 11:00 import tess")
}

// A change says who made it, of what kind and for whom, whatever
// characters the id holds; a call with no principal is nobody's.
func TestAChangeSaysWhoMadeIt(t *testing.T) {
	cl := historyClock()
	s, _ := openStore(t, Options{Now: cl.Now, HostAnswersFor: true})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(historyRates), asSetup); err != nil {
		t.Fatal(err)
	}
	for i, who := range []Option{
		As(Principal{ID: "żaneta·agent 7", Kind: ActorAgent, For: "anna"}),
		As(Principal{ID: "svc", Kind: ActorService}),
		As(Principal{ID: strings.Repeat("ø", 40), Kind: ActorPerson, For: "Ölaf"}),
		As(Principal{}),
	} {
		cl.at(10+i, 0)
		if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.2500"}}, who); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query("SELECT event_id, kind, actor_id, actor_kind, actor_for FROM fado_table_changes ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, kind, by, byKind, byFor sql.NullString
		if err := rows.Scan(&id, &kind, &by, &byKind, &byFor); err != nil {
			t.Fatal(err)
		}
		if len(id.String) != 26 {
			t.Errorf("the id %q", id.String)
		}
		got = append(got, kind.String+"|"+by.String+"|"+byKind.String+"|"+byFor.String+"|"+strconv.FormatBool(by.Valid))
	}
	sameLines(t, "the changes", got,
		"import|żaneta·agent 7|agent|anna|true",
		"import|svc|service||true",
		"import|"+strings.Repeat("ø", 40)+"|person|Ölaf|true",
		"import||||false")
}

// Lineage reads a table as it was when the history of its rows reaches
// back to the moment; a table that keeps none is read as it is now, the
// answer says so, and each row still says which change gave it its
// values.
func TestLineageReadsATableWithNoHistoryAsItIsNow(t *testing.T) {
	s := lineageCase(t, strings.Replace(lineageRates, `"key": ["currency"],`, `"key": ["currency"], "history": false,`, 1))
	l, err := s.Lineage(context.Background(), "Invoice", "INV-17", "rate", LineageAsk{}, lineageDan)
	got := lineageJSON(t, l, err)
	at10 := strconv.FormatInt(time.Date(2026, 12, 3, 10, 0, 0, 0, time.UTC).Unix(), 10)
	if want := `"lookups":[{"table":"Rates","column":"rate","value":"4.3100","count":1,"parts":[{"key":{"currency":"EUR"},"value":"4.3100",` +
		`"since":{"at":` + at10 + `,"by":"tess","by_kind":"person","event":"import"}}],"more":false,"table_changed_since":true,"rows_now":true}],` +
		`"adds_up":false,"rebuilt":"4.3100"}`; !strings.HasSuffix(got, want) {
		t.Errorf("the rate:\n got %s\nwant …%s", got, want)
	}
}

// The history of one row: the row now, then as it was, the newest first,
// a page at a time, for whoever the table's read rule lets through.
func TestTheHistoryOfOneRow(t *testing.T) {
	cl := historyClock()
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(4)"}, "key": ["currency"],
 "access": {"read": "INGROUP(\"treasury\")", "write": "INGROUP(\"treasury\")"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	tess := As(Principal{ID: "tess", Kind: ActorPerson, Groups: []string{"treasury"}})
	for i, rate := range []string{"4.2500", "4.3100", "4.3300"} {
		cl.at(9+i, 0)
		if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": rate}, {"currency": "PLN", "rate": "1.0000"}}, tess); err != nil {
			t.Fatal(err)
		}
	}
	lines := func(h *RowHistory) []string {
		var out []string
		for _, v := range h.Rows {
			line := fmt.Sprint(v.Values["rate"]) + " since " + time.Unix(v.Since.At, 0).UTC().Format("15:04") + " " + v.Since.Event + " " + v.Since.By
			if v.Until != nil {
				line += " until " + time.Unix(v.Until.At, 0).UTC().Format("15:04") + " " + v.Until.Event + " " + v.Until.By
			}
			out = append(out, line)
		}
		return out
	}
	h, err := s.RowHistory(ctx, "Rates", Values{"currency": "EUR"}, RowHistoryAsk{}, tess)
	if err != nil {
		t.Fatal(err)
	}
	if !h.History || h.Count != 3 || h.More || h.Key["currency"] != "EUR" || len(h.Key) != 1 {
		t.Errorf("the history of EUR: %+v", h)
	}
	sameLines(t, "the history of EUR", lines(h),
		"4.3300 since 11:00 import tess",
		"4.3100 since 10:00 import tess until 11:00 import tess",
		"4.2500 since 09:00 import tess until 10:00 import tess")
	// A row brought again as it was is one version.
	if h, err := s.RowHistory(ctx, "Rates", Values{"currency": "PLN"}, RowHistoryAsk{}, tess); err != nil || h.Count != 1 {
		t.Errorf("the history of PLN: %+v, %v", h, err)
	}
	// A page.
	if h, err := s.RowHistory(ctx, "Rates", Values{"currency": "EUR"}, RowHistoryAsk{Limit: 1, Offset: 1}, tess); err != nil || h.Count != 3 || !h.More ||
		strings.Join(lines(h), "") != "4.3100 since 10:00 import tess until 11:00 import tess" {
		t.Errorf("the second version of EUR: %+v, %v", h, err)
	}
	if h, err := s.RowHistory(ctx, "Rates", Values{"currency": "EUR"}, RowHistoryAsk{Offset: 5}, tess); err != nil || h.Count != 3 || h.More || len(h.Rows) != 0 {
		t.Errorf("past the last version of EUR: %+v, %v", h, err)
	}
	// A key the table never had.
	if h, err := s.RowHistory(ctx, "Rates", Values{"currency": "CHF"}, RowHistoryAsk{}, tess); err != nil || h.Count != 0 || len(h.Rows) != 0 {
		t.Errorf("the history of CHF: %+v, %v", h, err)
	}
	// The read rule, and what is refused.
	for what, c := range map[string]struct {
		key  Values
		ask  RowHistoryAsk
		who  Option
		code string
		path string
	}{
		"ann is not in the treasury": {Values{"currency": "EUR"}, RowHistoryAsk{}, As(Principal{ID: "ann"}), CodeForbidden, "/access/read"},
		"a key with another column":  {Values{"rate": "4.25"}, RowHistoryAsk{}, tess, CodeUnknownVariable, "/key/rate"},
		"a key with no value":        {Values{}, RowHistoryAsk{}, tess, CodeRequired, "/key/currency"},
		"too many":                   {Values{"currency": "EUR"}, RowHistoryAsk{Limit: 1001}, tess, CodeBadValue, "/limit"},
	} {
		_, err := s.RowHistory(ctx, "Rates", c.key, c.ask, c.who)
		if code(err) != c.code || errPath(err) != c.path {
			t.Errorf("%s: %v (%s), want %s at %s", what, err, errPath(err), c.code, c.path)
		}
	}
	if _, err := s.RowHistory(ctx, "Nothing", Values{"currency": "EUR"}, RowHistoryAsk{}, tess); code(err) != CodeUnknownTable {
		t.Errorf("an unknown table: %v", err)
	}
	// A table that keeps no history answers the row as it is.
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Prices", "columns": {"sku": "text", "price": "decimal(2)"}, "key": ["sku"], "history": false, "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, price := range []string{"7.25", "7.50"} {
		if _, err := s.ImportRows(ctx, "Prices", []Values{{"sku": "A1", "price": price}}, tess); err != nil {
			t.Fatal(err)
		}
	}
	if h, err := s.RowHistory(ctx, "Prices", Values{"sku": "A1"}, RowHistoryAsk{}, tess); err != nil || h.History || h.Count != 1 || fmt.Sprint(h.Rows[0].Values["price"]) != "7.50" {
		t.Errorf("a table with no history: %+v, %v", h, err)
	}
}

// errPath is the place an error of the store names, "" for none.
func errPath(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Path
	}
	return ""
}

// The last change before a moment is found by halves, at every edge: no
// change yet, before the first, between any two, after the last.
func TestTheChangeBeforeAMoment(t *testing.T) {
	cl := historyClock()
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if got, err := s.changeBefore(ctx, "01ZZZZZZZZZZZZZZZZZZZZZZZZ"); err != nil || got != 0 {
		t.Fatalf("with no change: %d, %v", got, err)
	}
	if _, err := s.DefineTable(ctx, []byte(historyRates), asSetup); err != nil {
		t.Fatal(err)
	}
	var ids idGen
	var between []string
	for i := 0; i < 37; i++ {
		cl.set(time.Date(2026, 12, 2, 9, 0, i*2, 0, time.UTC))
		if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": strconv.Itoa(1+i%2) + ".0000"}}, asSetup); err != nil {
			t.Fatal(err)
		}
		between = append(between, ids.next(time.Date(2026, 12, 2, 9, 0, i*2+1, 0, time.UTC)))
	}
	// A generator of its own: one never goes back in time.
	var ids2 idGen
	if got, err := s.changeBefore(ctx, ids2.next(time.Date(2026, 12, 2, 8, 0, 0, 0, time.UTC))); err != nil || got != 0 {
		t.Errorf("before the first: %d, %v", got, err)
	}
	for i, at := range between {
		if got, err := s.changeBefore(ctx, at); err != nil || got != int64(i+1) {
			t.Errorf("after change %d: %d, %v", i+1, got, err)
		}
	}
}
