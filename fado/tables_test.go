package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on tables is a reference: it says how a table is defined,
// filled, read by a formula and written by a rule, and who may do each.
// TestTables takes the table, the rows and the two classes out of the
// chapter, plays its story step by step, and holds every table of the
// chapter to what the engine answers.

// codeOf says what a definition was answered with: "accepted", or the
// code of the first finding or of the refusal.
func codeOf(err error) string {
	if err == nil {
		return "accepted"
	}
	var found fado.Diagnostics
	if errors.As(err, &found) {
		for _, d := range found {
			if !d.Note {
				return d.Code
			}
		}
	}
	var refusal *fado.Error
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return err.Error()
}

// tableFinding says what the engine finds in a table definition. No
// call checks a table without storing it, so the table is defined.
func tableFinding(db *fado.DB, definition string) string {
	_, err := db.DefineTable(context.Background(), []byte(definition), setup)
	return codeOf(err)
}

// heading returns the cells of the first line of a table of a chapter,
// each without its backticks.
func heading(table string) []string {
	line, _, _ := strings.Cut(strings.TrimSpace(table), "\n")
	var cells []string
	for _, cell := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
		cells = append(cells, strings.ReplaceAll(strings.TrimSpace(cell), "`", ""))
	}
	return cells
}

// shown writes a value as a table of a chapter does: a blank as the
// word, anything else as it prints.
func shown(value any) string {
	if value == nil {
		return "blank"
	}
	return fmt.Sprint(value)
}

// checks plays a table of a chapter whose rows are each held by a
// check of their own, under the words of the row's first cell. A row
// without a check fails, and so does a check without a row.
func checks(t *testing.T, page []byte, marker string, by map[string]func(row []string)) {
	t.Helper()
	listed := map[string]bool{}
	for _, row := range rows(marked(t, page, marker)) {
		listed[row[0]] = true
		check, ok := by[row[0]]
		if !ok {
			t.Errorf("%s: the chapter has the row %q, and the test does not play it", marker, row[0])
			continue
		}
		check(row)
	}
	for what := range by {
		if !listed[what] {
			t.Errorf("%s: the test plays %q, and the chapter has no such row", marker, what)
		}
	}
}

// fenced returns the body of the fenced block, of any kind, that
// follows a marker comment of a chapter.
func fenced(t *testing.T, page []byte, marker string) string {
	t.Helper()
	text := strings.ReplaceAll(string(page), "\r\n", "\n")
	_, rest, found := strings.Cut(text, "<!-- "+marker+" -->\n```")
	if !found {
		t.Fatalf("the chapter has no fenced block under the marker %q", marker)
	}
	_, rest, _ = strings.Cut(rest, "\n")
	body, _, _ := strings.Cut(rest, "\n```")
	return body
}

// told writes the answer to a call as a story of a chapter does.
func told(err error) string {
	if err == nil {
		return "allowed"
	}
	return "refused, " + answer(err)
}

// tale plays the story of a chapter, the table under the marker "what
// it does": each step of the test is held to the answer of its row.
type tale struct {
	t      *testing.T
	rows   [][]string
	played int
}

func taleOf(t *testing.T, page []byte) *tale {
	t.Helper()
	return &tale{t: t, rows: rows(marked(t, page, "what it does"))}
}

func (s *tale) step(n int, got string) {
	s.t.Helper()
	s.played++
	if n > len(s.rows) {
		s.t.Fatalf("the test plays step %d, and the chapter has %d steps", n, len(s.rows))
	}
	row := s.rows[n-1]
	if row[0] != strconv.Itoa(n) {
		s.t.Errorf("row %d of the story is step %s", n, row[0])
	}
	if got != row[3] {
		s.t.Errorf("step %d, %s %s: the engine answers %q, and the chapter says %q", n, row[1], row[2], got, row[3])
	}
}

func (s *tale) done() {
	s.t.Helper()
	if s.played != len(s.rows) {
		s.t.Errorf("the test played %d steps, and the chapter has %d", s.played, len(s.rows))
	}
}

func TestTables(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "tables.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var rates []byte
	var classes [][]byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		switch kindOf(m[1]) {
		case "table":
			rates = m[1]
		case "class":
			classes = append(classes, m[1])
		}
	}
	// The rows of the first import stand in a block of their own kind,
	// so that the general check of the manual does not take them for a
	// piece of a class.
	given := regexp.MustCompile("(?s)```jsonc\r?\n(.*?)\r?\n```").FindSubmatch(page)
	statement := regexp.MustCompile("(?s)```sql\r?\n(.*?)\r?\n```").FindSubmatch(page)
	if rates == nil || len(classes) != 2 || given == nil || statement == nil {
		t.Fatal("the chapter shows a table, the rows of an import, two classes and a statement")
	}
	var imported []fado.Values
	if err := json.Unmarshal(given[1], &imported); err != nil {
		t.Fatal(err)
	}

	dan := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	celina := fado.As(fado.Principal{ID: "celina", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}})
	tess, olga, ann := person("tess", "treasury"), person("olga", "treasury"), person("ann")

	// tableIs holds the rows of a table to a table of the chapter.
	tableIs := func(db *fado.DB, name, marker string, stands map[string]string) {
		t.Helper()
		got, err := db.TableRows(ctx, name, ann)
		if err != nil {
			t.Fatal(err)
		}
		table := marked(t, page, marker)
		columns, want := heading(table), rows(table)
		if len(got) != len(want) {
			t.Fatalf("%s: the table has %d rows, and the chapter %d", marker, len(got), len(want))
		}
		for i, row := range want {
			for j, cell := range row {
				if other, ok := stands[cell]; ok {
					cell = other
				}
				if value := shown(got[i][columns[j]]); value != cell {
					t.Errorf("%s, row %d: %s is %s, and the chapter says %s", marker, i+1, columns[j], value, cell)
				}
			}
		}
	}

	// The story.
	db := notionDB(t, fado.Options{})
	story := taleOf(t, page)
	step := story.step
	written := func(n int, err error) string {
		switch {
		case err != nil:
			return told(err)
		case n == 1:
			return "1 row written"
		}
		return fmt.Sprintf("%d rows written", n)
	}
	says := func(doc *fado.Document, err error) string {
		if err != nil {
			return told(err)
		}
		return "rate " + shown(doc.Values["rate"]) + ", in_pln " + shown(doc.Values["in_pln"])
	}
	count := func() int {
		t.Helper()
		got, err := db.TableRows(ctx, "Rates", ann)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	ran := func() int {
		t.Helper()
		n, err := db.Drain(ctx, setup)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	defined, err := db.DefineTable(ctx, rates, dan)
	if err != nil {
		t.Fatal(err)
	}
	step(1, fmt.Sprintf("defined, version %d", defined.Version))
	for _, class := range classes {
		if _, err := db.DefineClass(ctx, class, dan); err != nil {
			t.Fatal(err)
		}
	}
	step(2, written(db.ImportRows(ctx, "Rates", imported, tess)))
	step(3, written(db.ImportRows(ctx, "Rates", []fado.Values{{"currency": "CHF", "day": "2026-11-13", "rate": "4.6000"}}, ann)))
	step(4, fmt.Sprintf("%d rows", count()))
	tableIs(db, "Rates", "the rows as they are read", nil)
	first, err := db.Create(ctx, "Expense", fado.Values{"currency": "EUR", "spent_on": "2026-11-13", "amount": "100.00"}, ann)
	step(5, says(first, err))
	step(6, written(db.ImportRows(ctx, "Rates", []fado.Values{{"currency": "EUR", "day": "2026-11-13", "rate": "4.3000", "source": "NBP"}}, tess)))
	if count() != 3 {
		t.Errorf("after step 6 the table has %d rows; a row whose key the table has replaces that row", count())
	}
	step(7, says(db.Get(ctx, "Expense", first.ID, ann)))
	second, err := db.Create(ctx, "Expense", fado.Values{"currency": "EUR", "spent_on": "2026-11-16", "amount": "50.00"}, ann)
	step(8, says(second, err))
	_, err = db.Transition(ctx, "Expense", second.ID, "file", nil, ann)
	step(9, told(err))
	entry, err := db.Create(ctx, "RateEntry", fado.Values{"currency": "EUR", "day": "2026-11-16", "rate": "4.3100"}, tess)
	step(10, told(err))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Transition(ctx, "RateEntry", entry.ID, "publish", nil, tess)
	step(11, told(err))
	_, err = db.Transition(ctx, "RateEntry", entry.ID, "publish", nil, olga)
	step(12, told(err))
	if count() != 3 {
		t.Errorf("between steps 12 and 13 the table has %d rows; the chapter says the row is not there yet", count())
	}
	step(13, fmt.Sprintf("%d action ran; the table has %d rows", ran(), count()))
	tableIs(db, "Rates", "the rows after step 13", map[string]string{"the id of the rate entry": entry.ID})
	step(14, says(db.Get(ctx, "Expense", second.ID, ann)))
	if _, err := db.Set(ctx, "Expense", second.ID, fado.Values{"spent_on": "2026-11-13"}, ann); err != nil {
		t.Fatal(err)
	}
	step(15, says(db.Set(ctx, "Expense", second.ID, fado.Values{"spent_on": "2026-11-16"}, ann)))
	_, err = db.Transition(ctx, "Expense", second.ID, "file", nil, ann)
	step(16, told(err))
	_, err = db.Set(ctx, "RateEntry", entry.ID, fado.Values{"rate": "4.3200"}, olga)
	step(17, told(err))
	n := ran()
	all, err := db.TableRows(ctx, "Rates", ann)
	if err != nil {
		t.Fatal(err)
	}
	rate := "no such row"
	for _, row := range all {
		if row["currency"] == "EUR" && row["day"] == "2026-11-16" {
			rate = shown(row["rate"])
		}
	}
	step(18, fmt.Sprintf("%d action ran; the rate of the row is %s", n, rate))
	step(19, says(db.Get(ctx, "Expense", second.ID, ann)))
	_, err = db.Transition(ctx, "RateEntry", entry.ID, "withdraw", nil, olga)
	step(20, told(err))
	step(21, fmt.Sprintf("%d action ran; the table has %d rows", ran(), count()))
	story.done()

	// The record is the document's.
	events, err := db.Events(ctx, "RateEntry", entry.ID, ann)
	if err != nil {
		t.Fatal(err)
	}
	log := rows(marked(t, page, "the log of the rate entry"))
	if len(events) != len(log) {
		t.Fatalf("the rate entry has %d events, and the chapter %d", len(events), len(log))
	}
	for i, row := range log {
		if got := []string{events[i].Kind, events[i].Name, events[i].ActorID}; !reflect.DeepEqual(got, row) {
			t.Errorf("event %d of the rate entry is %v, and the chapter says %v", i+1, got, row)
		}
	}
	// A value of a published entry other than the rate is closed, and
	// so is the rate of an entry to those outside the treasury.
	third, err := db.Create(ctx, "RateEntry", fado.Values{"currency": "EUR", "day": "2026-11-16", "rate": "4.3100"}, tess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "RateEntry", third.ID, "publish", nil, olga); err != nil {
		t.Fatal(err)
	}
	if ran() != 1 || count() != 4 {
		t.Fatalf("the third entry is not in the table: %d rows", count())
	}
	_, err = db.Set(ctx, "RateEntry", third.ID, fado.Values{"day": "2026-11-17"}, olga)
	want(t, "Olga moves a published entry to another day", err, "forbidden, variable day")
	_, err = db.Set(ctx, "RateEntry", third.ID, fado.Values{"rate": "9.0000"}, ann)
	want(t, "Ann changes the rate of a published entry", err, "forbidden at /access/update")

	// An action that is refused. The last action a document queued, and
	// how it ended.
	ended := func(id string) string {
		t.Helper()
		ran()
		queue, err := db.Outbox(ctx, fado.OutboxFilter{DocID: id}, setup)
		if err != nil || len(queue) == 0 {
			t.Fatalf("the queue of %s: %v, %v", id, queue, err)
		}
		last := queue[0]
		for _, row := range queue {
			if row.Key > last.Key {
				last = row
			}
		}
		switch last.Status {
		case "dead":
			return "is refused, " + last.ErrorCode
		case "done":
			return "is done"
		}
		return last.Status
	}
	lastEvent := func(class, id string) string {
		t.Helper()
		events, err := db.Events(ctx, class, id, ann)
		if err != nil || len(events) == 0 {
			t.Fatal(err)
		}
		last := events[len(events)-1]
		return strings.TrimSpace(last.Kind + " " + last.Name + " " + last.Code)
	}
	if _, err := db.DefineClass(ctx, []byte(`{"name":"Loose","variables":{"currency":{"type":"text"},"day":{"type":"date"}},
	  "states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"push","from":"a","to":"b"}],
	  "rules":[{"on":"transition","name":"push","do":[{"action":"table.insert","table":"Rates","row":{"currency":"currency","day":"day"}}]}],
	  "access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`), dan); err != nil {
		t.Fatal(err)
	}
	holds := func(row []string, got string) {
		t.Helper()
		if !strings.HasPrefix(row[1], got) {
			t.Errorf("%s: the action %s, and the chapter says it %s", row[0], got, row[1])
		}
	}
	checks(t, page, "an action that is refused", map[string]func(row []string){
		"a second entry for EUR on 16 November is published": func(row []string) {
			twin, err := db.Create(ctx, "RateEntry", fado.Values{"currency": "EUR", "day": "2026-11-16", "rate": "4.4000"}, tess)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Transition(ctx, "RateEntry", twin.ID, "publish", nil, olga); err != nil {
				t.Fatal(err)
			}
			holds(row, ended(twin.ID))
			if got := lastEvent("RateEntry", twin.ID); got != "action_failed table.insert duplicate" {
				t.Errorf("the last event of the second entry is %s", got)
			}
			found, _ := db.TableRows(ctx, "Rates", ann)
			if len(found) != 4 || found[2]["rate"] != "4.3100" || found[2]["source"] != third.ID {
				t.Errorf("the row of the first entry did not stay: %v", found)
			}
		},
		"the rate of a published entry is changed after its row was removed from the table": func(row []string) {
			if _, err := db.ImportRows(ctx, "Rates", imported, tess, fado.Replace()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Set(ctx, "RateEntry", third.ID, fado.Values{"rate": "4.3300"}, olga); err != nil {
				t.Fatal(err)
			}
			holds(row, ended(third.ID))
		},
		"an entry is withdrawn after its row was removed from the table": func(row []string) {
			if _, err := db.Transition(ctx, "RateEntry", third.ID, "withdraw", nil, olga); err != nil {
				t.Fatal(err)
			}
			holds(row, ended(third.ID))
			if got := lastEvent("RateEntry", third.ID); got != "action_run table.delete" {
				t.Errorf("the last event of the withdrawn entry is %s", got)
			}
		},
		"a class lets somebody outside treasury take a transition whose rule writes Rates": func(row []string) {
			loose, err := db.Create(ctx, "Loose", fado.Values{"currency": "SEK", "day": "2026-11-13"}, ann)
			if err != nil {
				t.Fatal(err)
			}
			moved, err := db.Transition(ctx, "Loose", loose.ID, "push", nil, ann)
			if err != nil || moved.State != "b" {
				t.Fatalf("the transition does not stand: %v", err)
			}
			holds(row, ended(loose.ID))
			if got := lastEvent("Loose", loose.ID); got != "action_failed table.insert forbidden" {
				t.Errorf("the last event of the document is %s", got)
			}
			if after, _ := db.Get(ctx, "Loose", loose.ID, ann); after == nil || after.State != "b" {
				t.Error("the transition was undone")
			}
		},
	})
	if ran() != 0 {
		t.Error("a dead action was tried again by itself")
	}

	// The three actions are the ones the rate entry uses.
	var actions []string
	for _, row := range rows(marked(t, page, "the three actions")) {
		actions = append(actions, row[0])
		if !strings.Contains(string(classes[1]), `"action": "`+row[0]+`"`) {
			t.Errorf("the rate entry has no %s", row[0])
		}
	}
	if !reflect.DeepEqual(actions, []string{"table.insert", "table.update", "table.delete"}) {
		t.Errorf("the chapter lists the actions %v", actions)
	}

	// What is checked when a class with such a rule is defined.
	rule := func(action string) string {
		return scratch("Feeder", `"c":{"type":"text"},"d":{"type":"date"},"r":{"type":"decimal","scale":4},"n":{"type":"number"}`,
			`"states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"go","from":"a","to":"b"}],"rules":[{"on":"transition","name":"go","do":[`+action+`]}]`)
	}
	if got := finding(db, rule(`{"action":"table.delete","table":"Rates","key":{"currency":"c","day":"d"}}`)); got != "accepted" {
		t.Errorf("a sound table action is found %s", got)
	}
	held(t, db, page, map[string][]string{
		"names a table the database does not have": {rule(`{"action":"table.insert","table":"Nope","row":{"currency":"c","day":"d"}}`)},
		"names no table": {rule(`{"action":"table.insert","row":{"currency":"c","day":"d"}}`)},
		"inserts a row with no value for a key column": {rule(`{"action":"table.insert","table":"Rates","row":{"currency":"c"}}`)},
		"addresses a row with no value for a key column": {
			rule(`{"action":"table.update","table":"Rates","key":{"currency":"c"},"set":{"rate":"r"}}`),
			rule(`{"action":"table.delete","table":"Rates","key":{"currency":"c"}}`),
		},
		"names a column the table does not have": {
			rule(`{"action":"table.insert","table":"Rates","row":{"currency":"c","day":"d","nope":"c"}}`),
			rule(`{"action":"table.update","table":"Rates","key":{"currency":"c","day":"d"},"set":{"nope":"c"}}`),
		},
		"gives a column a value of another type": {
			rule(`{"action":"table.insert","table":"Rates","row":{"currency":"c","day":"c"}}`),
			rule(`{"action":"table.update","table":"Rates","key":{"currency":"c","day":"d"},"set":{"rate":"n"}}`),
		},
		"changes a key column": {rule(`{"action":"table.update","table":"Rates","key":{"currency":"c","day":"d"},"set":{"day":"d"}}`)},
		"addresses a row by a column that is not in the key": {
			rule(`{"action":"table.update","table":"Rates","key":{"currency":"c","day":"d","rate":"r"},"set":{"source":"c"}}`),
		},
		"has a part its action does not take": {
			rule(`{"action":"table.insert","table":"Rates","row":{"currency":"c","day":"d"},"key":{"currency":"c","day":"d"}}`),
			rule(`{"action":"table.delete","table":"Rates","key":{"currency":"c","day":"d"},"set":{"rate":"r"}}`),
		},
	})
	reads := func(formula string) string {
		quoted, _ := json.Marshal(formula)
		return scratch("Reader", `"c":{"type":"text"},"r":{"formula":`+string(quoted)+`}`, oneState)
	}
	for what, formula := range map[string]string{
		"names a table that is not there":      `LOOKUP(Nope, rate, currency = THIS.c)`,
		"names a column that is not there":     `LOOKUP(Rates, nope, currency = THIS.c)`,
		"reads the document without THIS.":     `LOOKUP(Rates, rate, currency = c)`,
		"puts one lookup inside another":       `LOOKUP(Rates, rate, day = LOOKUP(Rates, day, currency = THIS.c))`,
		"takes the largest of the days":        `LOOKUP(Rates, MAX(day), currency = THIS.c)`,
		"takes the smallest of the sources":    `LOOKUP(Rates, MIN(source), currency = THIS.c)`,
		"adds up the days":                     `LOOKUP(Rates, SUM(day), currency = THIS.c)`,
		"has a condition that is no condition": `LOOKUP(Rates, rate, rate)`,
	} {
		if got := finding(db, reads(formula)); got != "bad_formula" {
			t.Errorf("a formula that %s is found %s, and the chapter says bad_formula", what, got)
		}
	}
	if got := finding(db, reads(`LOOKUP(Rates, rate, currency = THIS.c) + LOOKUP(Rates, rate, currency = "USD")`)); got != "accepted" {
		t.Errorf("two lookups side by side are found %s", got)
	}
	expense, _ := db.Class("Expense")
	for _, v := range expense.Variables {
		if v.Name == "rate" && v.Type.String() != "decimal(4)" {
			t.Errorf("rate of the expense is %s; the chapter says it has the type of the column", v.Type)
		}
	}

	// With SQL. The table holds the three rows of the first import again.
	result, err := db.SQL(ctx, string(statement[1]), nil, setup)
	if err != nil {
		t.Fatal(err)
	}
	sqlTable := marked(t, page, "as SQL gives it")
	if !reflect.DeepEqual(result.Columns, heading(sqlTable)) {
		t.Errorf("the statement gives the columns %v", result.Columns)
	}
	var gave [][]string
	for _, row := range result.Rows {
		var cells []string
		for _, cell := range row {
			cells = append(cells, fmt.Sprint(cell))
		}
		gave = append(gave, cells)
	}
	if !reflect.DeepEqual(gave, rows(sqlTable)) {
		t.Errorf("the statement gives %v, and the chapter says %v", gave, rows(sqlTable))
	}

	// What is refused, each row by its own call.
	refusal := func(err error) *fado.Error {
		var r *fado.Error
		if !errors.As(err, &r) {
			return &fado.Error{Code: codeOf(err)}
		}
		return r
	}
	calls := map[string]func() error{
		"names a table the database does not have": func() error {
			_, err := db.TableRows(ctx, "Nope", ann)
			if _, other := db.ImportRows(ctx, "Nope", nil, tess); codeOf(other) != codeOf(err) {
				t.Errorf("an import into a table that is not there: %v", other)
			}
			return err
		},
		"may not read the rows": func() error {
			_, err := db.TableRows(ctx, "Rates")
			return err
		},
		"may not write the rows": func() error {
			_, err := db.ImportRows(ctx, "Rates", []fado.Values{{"currency": "CHF", "day": "2026-11-13", "rate": "4.6000"}}, ann)
			return err
		},
		"defines a table and is no designer": func() error {
			_, err := db.DefineTable(ctx, []byte(`{"name":"Mine","columns":{"k":"text"},"key":["k"]}`), ann)
			return err
		},
		"gives a row with a blank in a key column": func() error {
			_, err := db.ImportRows(ctx, "Rates", []fado.Values{
				{"currency": "CHF", "day": "2026-11-13", "rate": "4.6000"},
				{"currency": "GBP", "rate": "5.2000"},
			}, tess)
			if count() != 3 {
				t.Errorf("an import that was refused wrote rows: the table has %d", count())
			}
			return err
		},
		"gives a value that does not fit its column": func() error {
			_, err := db.ImportRows(ctx, "Rates", []fado.Values{{"currency": "CHF", "day": "13.11.2026", "rate": "4.6000"}}, tess)
			if _, other := db.ImportRows(ctx, "Rates", []fado.Values{{"currency": "CHF", "day": "2026-11-13", "rate": "4.61234"}}, tess); codeOf(other) != codeOf(err) {
				t.Errorf("a rate with more places than the column: %v", other)
			}
			return err
		},
		"gives a column the table does not have": func() error {
			_, err := db.ImportRows(ctx, "Rates", []fado.Values{{"currency": "CHF", "day": "2026-11-13", "price": "4.6000"}}, tess)
			return err
		},
		"imports as of another moment": func() error {
			_, err := db.ImportRows(ctx, "Rates", imported, tess, fado.At(time.Now().Add(time.Hour)))
			return err
		},
	}
	by := map[string]func(row []string){}
	for what, call := range calls {
		by[what] = func(row []string) {
			got := refusal(call())
			if got.Code != row[1] {
				t.Errorf("a caller who %s: the engine answers %s, and the chapter says %s", what, got.Code, row[1])
			}
			names := row[2]
			if _, example, ok := strings.Cut(names, ", as "); ok {
				names = example
			}
			switch {
			case strings.HasPrefix(names, "/"):
				if got.Path != names {
					t.Errorf("a caller who %s: the refusal names %q, and the chapter says %s", what, got.Path, names)
				}
			case strings.HasPrefix(names, "Rates["):
				if got.Variable != names {
					t.Errorf("a caller who %s: the refusal names %q, and the chapter says %s", what, got.Variable, names)
				}
			case names == "the row and the column":
				if !strings.HasPrefix(got.Variable, "Rates[0].") {
					t.Errorf("a caller who %s: the refusal names %q", what, got.Variable)
				}
			case names != "":
				t.Errorf("the test cannot read what the refusal names: %q", names)
			}
		}
	}
	checks(t, page, "what is refused", by)

	// Who may. needs holds the second cell of a row to the refusal of a
	// caller who lacks what the row asks for: the last word of the place
	// the refusal names, a line of the table or a system group, stands
	// in the cell.
	needs := func(row []string, err error) {
		t.Helper()
		got := refusal(err)
		word := got.Path[strings.LastIndex(got.Path, "/")+1:]
		if got.Code != "forbidden" || word == "" || !strings.Contains(row[1], "the "+word+" line") && !strings.Contains(row[1], "the system group "+word) {
			t.Errorf("to %s the chapter asks for %q, and the engine refuses with %s at %q", row[0], row[1], got.Code, got.Path)
		}
	}
	checks(t, page, "who may", map[string]func(row []string){
		"define a table": func(row []string) {
			_, err := db.DefineTable(ctx, []byte(`{"name":"Mine","columns":{"k":"text"},"key":["k"]}`), tess)
			needs(row, err)
			if _, err := db.DefineTable(ctx, []byte(`{"name":"Mine","columns":{"k":"text"},"key":["k"]}`), dan); err != nil {
				t.Errorf("Dan defines a table: %v", err)
			}
		},
		"read the definition of a table": func(row []string) {
			if !strings.HasPrefix(row[1], "nothing") {
				t.Errorf("the chapter asks for %q to read a definition, and the calls take no caller", row[1])
			}
			if def, ok := db.Table("rates"); !ok || def.Label != "Exchange rates" || def.Defined.By != "dan" {
				t.Errorf("the definition of Rates, read with no caller: %v", def)
			}
			if !reflect.DeepEqual(db.Tables(), []string{"Rates", "Mine"}) {
				t.Errorf("the tables are %v", db.Tables())
			}
			versions, err := db.Definitions(ctx, fado.DefinitionTable, "Rates")
			if err != nil || len(versions) != 1 || versions[0].By != "dan" || versions[0].At == 0 {
				t.Errorf("the versions of Rates: %v, %v", versions, err)
			}
		},
		"read the rows": func(row []string) {
			if _, err := db.TableRows(ctx, "Rates", ann); err != nil {
				t.Errorf("Ann reads the rows: %v", err)
			}
			_, err := db.TableRows(ctx, "Rates")
			needs(row, err)
		},
		"write rows, by an import or through a rule": func(row []string) {
			_, err := db.ImportRows(ctx, "Rates", imported, ann)
			needs(row, err)
			if _, err := db.ImportRows(ctx, "Rates", imported, tess); err != nil {
				t.Errorf("Tess imports: %v", err)
			}
			// Through a rule: the table of refused actions, above.
		},
		"read the rows with SQL": func(row []string) {
			_, err := db.SQL(ctx, string(statement[1]), nil, tess)
			needs(row, err)
			_, err = db.SQL(ctx, "delete from Rates", nil, setup)
			if err == nil || count() != 3 {
				t.Errorf("SQL wrote: %v", err)
			}
		},
	})

	// The second database: what a formula reads, with the three rows of
	// the first import.
	db2 := notionDB(t, fado.Options{})
	if _, err := db2.DefineTable(ctx, rates, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.ImportRows(ctx, "Rates", imported, tess); err != nil {
		t.Fatal(err)
	}
	// value defines a class with one formula and answers it for a
	// document with the values given.
	classNo := 0
	value := func(formula string, values fado.Values) string {
		t.Helper()
		classNo++
		name := fmt.Sprintf("Sample%d", classNo)
		quoted, _ := json.Marshal(formula)
		class := scratch(name, `"currency":{"type":"text"},"spent_on":{"type":"date"},"r":{"formula":`+string(quoted)+`}`, oneState)
		if _, err := db2.DefineClass(ctx, []byte(class), setup); err != nil {
			return "refused: " + codeOf(err)
		}
		doc, err := db2.Create(ctx, name, values, ann)
		if err != nil {
			return "refused: " + codeOf(err)
		}
		return shown(doc.Values["r"])
	}
	for _, row := range rows(marked(t, page, "one value from many rows")) {
		if got := value(row[0], fado.Values{"currency": "EUR"}); got != row[2] {
			t.Errorf("%s for EUR is %s, and the chapter says %s", row[0], got, row[2])
		}
		if got := value(row[0], fado.Values{"currency": "GBP"}); got != row[3] {
			t.Errorf("%s for a currency with no row is %s, and the chapter says %s", row[0], got, row[3])
		}
	}
	lookup := "LOOKUP(Rates, rate, currency = THIS.currency AND day = THIS.spent_on)"
	if !strings.Contains(string(classes[0]), lookup) {
		t.Error("the expense does not have the lookup the chapter reads aloud")
	}
	friday := fado.Values{"currency": "EUR", "spent_on": "2026-11-13"}
	if a, b := value(lookup, friday), value("LOOKUP(rates, RATE, CURRENCY = THIS.currency AND DAY = THIS.spent_on)", friday); a != "4.2700" || b != a {
		t.Errorf("names in another case of letters: %s and %s", a, b)
	}
	if got := value(lookup, fado.Values{"currency": "eur", "spent_on": "2026-11-13"}); got != "blank" {
		t.Errorf("an expense in eur finds %s", got)
	}
	// COUNT counts the rows that have a value in the column.
	if _, err := db2.ImportRows(ctx, "Rates", []fado.Values{{"currency": "EUR", "day": "2026-11-10", "rate": "4.2000"}}, tess); err != nil {
		t.Fatal(err)
	}
	if a, b := value("LOOKUP(Rates, COUNT(source), currency = THIS.currency)", friday), value("LOOKUP(Rates, COUNT(day), currency = THIS.currency)", friday); a != "2" || b != "3" {
		t.Errorf("COUNT of a column with a blank is %s, and of a key column %s", a, b)
	}
	// A row for which the condition cannot be worked out does not match.
	if _, err := db2.DefineTable(ctx, []byte(`{"name":"Prices","columns":{"sku":"text","price":"money(2)"},"key":["sku"],"access":{"read":"TRUE","write":"TRUE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.ImportRows(ctx, "Prices", []fado.Values{{"sku": "A", "price": "5.00 EUR"}, {"sku": "B", "price": "7.00 PLN"}, {"sku": "C", "price": "9.00 PLN"}}); err != nil {
		t.Errorf("TRUE lets a call through that names nobody: %v", err)
	}
	if got := value(`LOOKUP(Prices, COUNT(sku), price < MONEY(8, "PLN"))`, nil); got != "1" {
		t.Errorf("the prices under 8.00 PLN are %s; the chapter says a price in EUR is passed over", got)
	}
	priced, err := db2.SQL(ctx, "select * from Prices", nil, setup)
	if err != nil || !reflect.DeepEqual(priced.Columns, []string{"sku", "price", "price_currency"}) {
		t.Errorf("a money column in SQL: %v, %v", priced, err)
	}

	// When a lookup is worked out.
	checks(t, page, "when a lookup is worked out", map[string]func(row []string){
		"a computed variable": func([]string) {
			if _, err := db2.DefineClass(ctx, classes[0], setup); err != nil {
				t.Fatal(err)
			}
			doc, err := db2.Create(ctx, "Expense", fado.Values{"currency": "EUR", "spent_on": "2026-11-13", "amount": "100.00"}, ann)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db2.ImportRows(ctx, "Rates", []fado.Values{{"currency": "EUR", "day": "2026-11-13", "rate": "4.3000"}}, tess); err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct {
				what   string
				values fado.Values
				says   string
			}{
				{"another value changes", fado.Values{"amount": "200.00"}, "rate 4.2700, in_pln 854.00"},
				{"a value the lookup reads changes", fado.Values{"spent_on": "2026-11-12"}, "rate 4.2500, in_pln 850.00"},
				{"it changes back", fado.Values{"spent_on": "2026-11-13"}, "rate 4.3000, in_pln 860.00"},
			} {
				if got := says(db2.Set(ctx, "Expense", doc.ID, c.values, ann)); got != c.says {
					t.Errorf("a computed variable when %s: %s, and the test expects %s", c.what, got, c.says)
				}
			}
		},
		"a default": func([]string) {
			class := scratch("Counted", `"currency":{"type":"text"},"note":{"type":"text"},"known":{"type":"number","default":"LOOKUP(Rates, COUNT(day), currency = THIS.currency)"}`, oneState)
			if _, err := db2.DefineClass(ctx, []byte(class), setup); err != nil {
				t.Fatal(err)
			}
			doc, err := db2.Create(ctx, "Counted", fado.Values{"currency": "USD"}, ann)
			if err != nil || shown(doc.Values["known"]) != "1" {
				t.Fatalf("a default with a lookup: %v, %v", doc, err)
			}
			if _, err := db2.ImportRows(ctx, "Rates", []fado.Values{{"currency": "USD", "day": "2026-11-12", "rate": "3.8000"}}, tess); err != nil {
				t.Fatal(err)
			}
			after, err := db2.Set(ctx, "Counted", doc.ID, fado.Values{"note": "x", "currency": "EUR"}, ann)
			if err != nil || shown(after.Values["known"]) != "1" {
				t.Errorf("a default is worked out once: %v, %v", after, err)
			}
		},
		"a guard, a line of access, a right to write, a rule": func([]string) {
			class := `{"name":"Late","variables":{"currency":{"type":"text"},"day":{"type":"date"},"taken":{"type":"decimal","scale":4}},
			  "states":{"initial":"a","list":["a","b"]},
			  "transitions":[{"name":"go","from":"a","to":"b","guard":"LOOKUP(Rates, COUNT(day), currency = THIS.currency AND day = THIS.day) > 0"}],
			  "rules":[{"on":"transition","name":"go","do":[{"action":"set","values":{"taken":"LOOKUP(Rates, rate, currency = THIS.currency AND day = THIS.day)"}}]}],
			  "access":{"read":"TRUE","create":"TRUE","update":"LOOKUP(Rates, COUNT(day), currency = \"NOK\") > 0","delete":"TRUE"},
			  "rights":{"b":{"day":{"write":"LOOKUP(Rates, COUNT(day), currency = \"DKK\") > 0"}}}}`
			if _, err := db2.DefineClass(ctx, []byte(class), setup); err != nil {
				t.Fatal(err)
			}
			doc, err := db2.Create(ctx, "Late", fado.Values{"currency": "NOK", "day": "2026-11-16"}, ann)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db2.Set(ctx, "Late", doc.ID, fado.Values{"day": "2026-11-17"}, ann)
			want(t, "a line of access, before the row", err, "forbidden at /access/update")
			if _, err := db2.ImportRows(ctx, "Rates", []fado.Values{{"currency": "NOK", "day": "2026-11-16", "rate": "0.3900"}}, tess); err != nil {
				t.Fatal(err)
			}
			_, err = db2.Set(ctx, "Late", doc.ID, fado.Values{"day": "2026-11-17"}, ann)
			want(t, "a line of access, after the row", err, "allowed")
			_, err = db2.Transition(ctx, "Late", doc.ID, "go", nil, ann)
			want(t, "a guard, before the row", err, "guard_false")
			if _, err := db2.ImportRows(ctx, "Rates", []fado.Values{{"currency": "NOK", "day": "2026-11-17", "rate": "0.4000"}}, tess); err != nil {
				t.Fatal(err)
			}
			moved, err := db2.Transition(ctx, "Late", doc.ID, "go", nil, ann)
			want(t, "a guard, after the row", err, "allowed")
			if moved == nil || shown(moved.Values["taken"]) != "0.4000" {
				t.Errorf("a rule sets the rate of that moment: %v", moved)
			}
			_, err = db2.Set(ctx, "Late", doc.ID, fado.Values{"day": "2026-11-18"}, ann)
			want(t, "a right, before the row", err, "forbidden, variable day")
			if _, err := db2.ImportRows(ctx, "Rates", []fado.Values{{"currency": "DKK", "day": "2026-11-17", "rate": "0.5700"}}, tess); err != nil {
				t.Fatal(err)
			}
			_, err = db2.Set(ctx, "Late", doc.ID, fado.Values{"day": "2026-11-18"}, ann)
			want(t, "a right, after the row", err, "allowed")
		},
	})

	// Where a lookup may stand.
	count2 := `LOOKUP(Rates, COUNT(day), currency = \"EUR\") > 0`
	chart := `"states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"go","from":"a","to":"b"}]`
	open := `"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}`
	heldBy(t, page, "where a lookup may stand", map[string][]string{
		"a computed variable, a default": {
			scratch("Place", `"c":{"type":"text"},"r":{"formula":"LOOKUP(Rates, rate, currency = THIS.c)"},"n":{"type":"number","default":"LOOKUP(Rates, COUNT(day), currency = THIS.c)"}`, oneState),
		},
		"a guard": {
			`{"name":"Place","variables":{"c":{"type":"text"}},"states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"go","from":"a","to":"b","guard":"` + count2 + `"}],` + open + `}`,
		},
		"create, update, delete and the line of a transition under access; a right to write": {
			`{"name":"Place","variables":{"c":{"type":"text"}},` + chart + `,"access":{"read":"TRUE","create":"` + count2 + `","update":"` + count2 + `","delete":"` + count2 + `","transition":{"go":"` + count2 + `"}},` +
				`"rights":{"*":{"c":{"write":"` + count2 + `"}}}}`,
		},
		"the if of a rule, a value a rule sets": {
			`{"name":"Place","variables":{"c":{"type":"text"},"r":{"type":"decimal","scale":4}},` + chart +
				`,"rules":[{"on":"transition","name":"go","if":"` + count2 + `","do":[{"action":"set","values":{"r":"LOOKUP(Rates, rate, currency = THIS.c)"}}]}],` + open + `}`,
		},
		"access.read, a right to read": {
			`{"name":"Place","variables":{"c":{"type":"text"}},` + chart + `,"access":{"read":"` + count2 + `","create":"TRUE","update":"TRUE","delete":"TRUE"}}`,
			`{"name":"Place","variables":{"c":{"type":"text"}},` + chart + `,` + open + `,"rights":{"*":{"c":{"read":"` + count2 + `"}}}}`,
		},
		"the at of a transition": {
			`{"name":"Place","variables":{"c":{"type":"text"}},"states":{"initial":"a","list":["a","b"]},` +
				`"transitions":[{"name":"go","from":"a","to":"b","at":"LOOKUP(Rates, day, currency = THIS.c)"}],` + open + `}`,
		},
		"a line of access of a table": {
			`{"name":"Guarded","columns":{"k":"text"},"key":["k"],"access":{"read":"` + count2 + `"}}`,
		},
	}, func(definition string) string {
		got := finding(db2, definition)
		if kindOf([]byte(definition)) == "table" {
			got = tableFinding(db2, definition)
		}
		if got == "accepted" {
			return "yes"
		}
		return "no, " + got
	})
	// The documents of a class are read with the same function, in every
	// place but a computed variable.
	if got := finding(db2, scratch("Place", `"c":{"type":"text"},"n":{"formula":"LOOKUP(Expense, COUNT(id), currency = THIS.c)"}`, oneState)); got == "accepted" {
		t.Error("a computed variable that reads the documents of a class is accepted")
	}
	if got := finding(db2, scratch("Place", `"c":{"type":"text"},"n":{"type":"number","default":"LOOKUP(Expense, COUNT(id), currency = THIS.c)"}`, oneState)); got != "accepted" {
		t.Errorf("a default that reads the documents of a class is found %s", got)
	}
	// The way round access.read that the chapter gives.
	if got := finding(db2, `{"name":"Place","variables":{"c":{"type":"text"},"listed":{"formula":"LOOKUP(Rates, COUNT(day), currency = THIS.c) > 0"}},`+chart+
		`,"access":{"read":"listed","create":"TRUE","update":"TRUE","delete":"TRUE"}}`); got != "accepted" {
		t.Errorf("a read line that tests a computed variable with a lookup is found %s", got)
	}

	// A formula reads with the engine's eyes.
	if _, err := db2.DefineTable(ctx, []byte(`{"name":"Closed","columns":{"k":"text","v":"text"},"key":["k"],"access":{"read":"FALSE","write":"INGROUP(\"treasury\")"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.ImportRows(ctx, "Closed", []fado.Values{{"k": "a", "v": "kept from Ann"}}, tess); err != nil {
		t.Fatal(err)
	}
	_, err = db2.TableRows(ctx, "Closed", ann)
	want(t, "Ann reads a table that is closed to her", err, "forbidden at /access/read")
	if got := value(`LOOKUP(Closed, v, k = "a")`, nil); got != "kept from Ann" {
		t.Errorf("a formula reads a closed table for Ann: %s", got)
	}

	// The types of a column: one column for each row of the table, one
	// row written as the chapter writes it and read back.
	kinds := rows(marked(t, page, "the types"))
	var columns []string
	row := fado.Values{}
	for i, kind := range kinds {
		name := fmt.Sprintf("c%d", i)
		columns = append(columns, `"`+name+`":"`+kind[0]+`"`)
		var given any
		if err := json.Unmarshal([]byte(kind[2]), &given); err != nil {
			t.Fatalf("the chapter writes a %s as %s: %v", kind[0], kind[2], err)
		}
		row[name] = given
	}
	if _, err := db2.DefineTable(ctx, []byte(`{"name":"Kinds","columns":{`+strings.Join(columns, ",")+`},"key":["c0"],"access":{"read":"TRUE","write":"TRUE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.ImportRows(ctx, "Kinds", []fado.Values{row}, ann); err != nil {
		t.Fatal(err)
	}
	back, err := db2.TableRows(ctx, "Kinds", ann)
	if err != nil || len(back) != 1 {
		t.Fatalf("the row of every type: %v, %v", back, err)
	}
	for i, kind := range kinds {
		var said any
		if err := json.Unmarshal([]byte(kind[3]), &said); err != nil {
			t.Fatalf("the chapter reads a %s back as %s: %v", kind[0], kind[3], err)
		}
		raw, _ := json.Marshal(back[0][fmt.Sprintf("c%d", i)])
		var got any
		json.Unmarshal(raw, &got)
		if !reflect.DeepEqual(got, said) {
			t.Errorf("a %s written as %s is read back as %s, and the chapter says %s", kind[0], kind[2], raw, kind[3])
		}
	}
	for _, composite := range []string{"array<text>", "rows", "object"} {
		if got := tableFinding(db2, `{"name":"Wide","columns":{"k":"text","c":"`+composite+`"},"key":["k"]}`); got == "accepted" {
			t.Errorf("a column of the type %s is accepted", composite)
		}
	}

	// Rows: what an import does.
	if _, err := db2.DefineTable(ctx, []byte(`{"name":"Codes","columns":{"code":"text","name":"text","note":"text"},"key":["code"],"access":{"read":"TRUE","write":"TRUE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	codes := func() string {
		t.Helper()
		found, err := db2.TableRows(ctx, "Codes", ann)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range found {
			out = append(out, fmt.Sprintf("%v %v %v", r["code"], shown(r["name"]), shown(r["note"])))
		}
		return strings.Join(out, "; ")
	}
	imports := func(what, after string, rows []fado.Values, opts ...fado.Option) {
		t.Helper()
		if _, err := db2.ImportRows(ctx, "Codes", rows, append(opts, ann)...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got := codes(); got != after {
			t.Errorf("%s: the table holds %q, and the test expects %q", what, got, after)
		}
	}
	imports("rows are read back in the order of the key", "a one first; b two blank",
		[]fado.Values{{"code": "b", "name": "two"}, {"code": "a", "name": "one", "note": "first"}})
	imports("a row whose key the table has replaces that row, whole", "a uno blank; b two blank",
		[]fado.Values{{"code": "a", "name": "uno"}})
	imports("a row whose key is new is added", "a uno blank; b two blank; c three blank",
		[]fado.Values{{"code": "c", "name": "three"}})
	imports("an import can replace the table", "d four blank",
		[]fado.Values{{"code": "d", "name": "four"}}, fado.Replace())
	imports("an import of no rows that replaces empties the table", "", nil, fado.Replace())

	// Who may, the lines of a table.
	if _, err := db2.DefineTable(ctx, []byte(`{"name":"Unsaid","columns":{"k":"text"},"key":["k"]}`), setup); err != nil {
		t.Fatal(err)
	}
	for who, as := range map[string]fado.Option{"Tess": tess, "a designer, administrator and analyst": setup} {
		_, err := db2.ImportRows(ctx, "Unsaid", []fado.Values{{"k": "a"}}, as)
		want(t, who+" writes a table with no lines", err, "forbidden at /access/write")
		_, err = db2.TableRows(ctx, "Unsaid", as)
		want(t, who+" reads a table with no lines", err, "forbidden at /access/read")
	}
	_, err = db2.ImportRows(ctx, "Unsaid", []fado.Values{{"k": "a"}}, celina)
	want(t, "a content administrator writes a table past its line, giving no reason", err, "reason_required at /access/write")
	_, err = db2.TableRows(ctx, "Unsaid", celina)
	want(t, "a content administrator reads a table past its line, giving no reason", err, "reason_required at /access/read")
	why := fado.Reason("the rates of the old system")
	if _, err := db2.ImportRows(ctx, "Unsaid", []fado.Values{{"k": "a"}}, celina, why); err != nil {
		t.Errorf("a content administrator writes any table, with a reason: %v", err)
	}
	if found, err := db2.TableRows(ctx, "Unsaid", celina, why); err != nil || len(found) != 1 {
		t.Errorf("a content administrator reads any table, with a reason: %v, %v", found, err)
	}
	if got := tableFinding(db2, `{"name":"Kind","columns":{"k":"text"},"key":["k"],"access":{"read":"USERKIND() = \"person\" AND TODAY() > DATE(2026, 1, 1)","write":"INGROUP(\"treasury\")"}}`); got != "accepted" {
		t.Errorf("a line over the caller and the clock is found %s", got)
	}

	// The commands: each row by the command it names, on a database of
	// its own.
	db4 := notionDB(t, fado.Options{})
	exec := func(as fado.Principal, name, object string) map[string]json.RawMessage {
		t.Helper()
		got, err := db4.Exec(ctx, as, name, []byte(object))
		if err != nil {
			t.Fatalf("the command %s: %v", name, err)
		}
		var answer map[string]json.RawMessage
		if err := json.Unmarshal(got, &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}
	designer := fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}}
	treasurer := fado.Principal{ID: "tess", Kind: fado.ActorPerson, Groups: []string{"treasury"}}
	reader := fado.Principal{ID: "ann", Kind: fado.ActorPerson}
	sent, _ := json.Marshal(imported)
	commands := []struct {
		to, name string
		holds    func() bool
	}{
		{"define a table", "define_table", func() bool {
			return exec(designer, "define_table", `{"definition":`+string(rates)+`}`)["table"] != nil
		}},
		{"write rows", "import_rows", func() bool {
			return string(exec(treasurer, "import_rows", `{"table":"Rates","rows":`+string(sent)+`}`)["imported"]) == "3"
		}},
		{"read the rows", "table_rows", func() bool {
			var got []fado.Values
			json.Unmarshal(exec(reader, "table_rows", `{"table":"Rates"}`)["rows"], &got)
			return len(got) == 3 && got[0]["day"] == "2026-11-12"
		}},
		{"read its definition back", "get_table", func() bool {
			return strings.Contains(string(exec(reader, "get_table", `{"name":"Rates"}`)["table"]), `"Exchange rates"`)
		}},
		{"list the tables, or those of one app", "list_tables", func() bool {
			return string(exec(reader, "list_tables", `{}`)["tables"]) == `["Rates"]`
		}},
	}
	listed := map[string]string{}
	for _, row := range rows(marked(t, page, "the commands")) {
		listed[row[0]] = row[1]
	}
	if len(listed) != len(commands) {
		t.Errorf("the chapter lists %d commands, and the test plays %d", len(listed), len(commands))
	}
	for _, c := range commands {
		if listed[c.to] != c.name {
			t.Errorf("to %s the chapter names the command %q, and the test plays %s", c.to, listed[c.to], c.name)
		}
		if !c.holds() {
			t.Errorf("the command %s does not %s", c.name, c.to)
		}
	}

	// The third database: defining and changing.
	db3 := notionDB(t, fado.Options{})
	plain := func(name, more string) string {
		return `{"name":"` + name + `","columns":{"k":"text","v":"text"},"key":["k"]` + more + `}`
	}
	lines := `,"access":{"read":"TRUE","write":"TRUE"}`
	define := func(definition string) {
		t.Helper()
		if got := tableFinding(db3, definition); got != "accepted" {
			t.Fatalf("a table of the test is found %s: %s", got, definition)
		}
	}
	define(plain("Empty", lines))
	if def, _ := db3.Table("Empty"); def == nil || def.Version != 1 {
		t.Fatal("a new table is not version 1")
	}
	define(plain("Empty", lines))
	if def, _ := db3.Table("Empty"); def == nil || def.Version != 1 {
		t.Error("the same definition again made a version")
	}
	if _, err := db3.DefineClass(ctx, []byte(scratch("Taken", `"c":{"type":"text"}`, oneState)), setup); err != nil {
		t.Fatal(err)
	}
	checks(t, page, "changing a table", map[string]func(row []string){
		"has no rows, and no class reads it": func(row []string) {
			define(plain("Empty", lines+`,"label":"Changed"`))
			def, _ := db3.Table("Empty")
			if def == nil || def.Version != 2 || row[1] != "is taken, as the next version" {
				t.Errorf("a changed definition of an empty table: %v, and the chapter says it %s", def, row[1])
			}
			versions, err := db3.Definitions(ctx, fado.DefinitionTable, "Empty")
			if err != nil || len(versions) != 2 || versions[1].By != "setup" || versions[1].At == 0 {
				t.Errorf("the versions of the table: %v, %v", versions, err)
			}
		},
		"has rows, and the change is more than new columns": func(row []string) {
			define(plain("Filled", lines))
			if _, err := db3.ImportRows(ctx, "Filled", []fado.Values{{"k": "a"}}, ann); err != nil {
				t.Fatal(err)
			}
			if got := "is refused, " + tableFinding(db3, plain("Filled", lines+`,"label":"Changed"`)); got != row[1] {
				t.Errorf("a changed definition of a table with rows %s, and the chapter says it %s", got, row[1])
			}
		},
		"is read by a formula of a class, and the change is more than new columns": func(row []string) {
			define(plain("Read", lines))
			if _, err := db3.DefineClass(ctx, []byte(scratch("Reads", `"c":{"type":"text"},"r":{"formula":"LOOKUP(Read, v, k = THIS.c)"}`, oneState)), setup); err != nil {
				t.Fatal(err)
			}
			if got := "is refused, " + tableFinding(db3, plain("Read", lines+`,"label":"Changed"`)); got != row[1] {
				t.Errorf("a changed definition of a table a class reads %s, and the chapter says it %s", got, row[1])
			}
			// The class moves over to a table under another name, and
			// the old one can then be changed.
			define(plain("Read2", lines))
			moved, err := db3.DefineClass(ctx, []byte(scratch("Reads", `"c":{"type":"text"},"r":{"formula":"LOOKUP(Read2, v, k = THIS.c)"}`, oneState)), setup)
			if err != nil || moved.Version != 2 {
				t.Fatalf("the class moves over to another table: %v", err)
			}
			define(plain("Read", lines+`,"label":"Changed"`))
		},
		"gains columns, none of them in its key, and nothing else changes": func(row []string) {
			// Grown is in use: it holds a row and a class reads it. Two
			// columns join it, and its row is blank in them (ADR-0080).
			define(plain("Grown", lines))
			if _, err := db3.ImportRows(ctx, "Grown", []fado.Values{{"k": "a", "v": "b"}}, ann); err != nil {
				t.Fatal(err)
			}
			if _, err := db3.DefineClass(ctx, []byte(scratch("ReadsGrown", `"c":{"type":"text"},"r":{"formula":"LOOKUP(Grown, v, k = THIS.c)"}`, oneState)), setup); err != nil {
				t.Fatal(err)
			}
			grown := `{"name":"Grown","columns":{"k":"text","v":"text","w":"text","n":"decimal(2)"},"key":["k"]` + lines + `}`
			define(grown)
			def, _ := db3.Table("Grown")
			rows, err := db3.TableRows(ctx, "Grown", ann)
			if def == nil || def.Version != 2 || err != nil || len(rows) != 1 || rows[0]["v"] != "b" || rows[0]["w"] != nil || rows[0]["n"] != nil ||
				row[1] != "is taken, as the next version, whatever rows it holds and whoever reads it; its rows are blank in each new column" {
				t.Errorf("a table in use gained columns: %+v, %v %v, and the chapter says it %s", def, rows, err, row[1])
			}
			// A new column in its key is refused: its rows would have none.
			if got := tableFinding(db3, `{"name":"Grown","columns":{"k":"text","v":"text","w":"text","n":"decimal(2)","x":"text"},"key":["k","x"]`+lines+`}`); got != "not_supported" {
				t.Errorf("a new column in the key of a table in use is found %s", got)
			}
		},
		"names another app, or none, and nothing else changes": func(row []string) {
			// Rates is in use: it holds a row and a class reads it. It
			// moves into the app Expenses, and out of every app again,
			// keeping its row (ADR-0079, item 4).
			if _, err := db3.DefineApp(ctx, []byte(`{"name":"Expenses"}`), setup); err != nil {
				t.Fatal(err)
			}
			define(plain("Kept", lines))
			if _, err := db3.ImportRows(ctx, "Kept", []fado.Values{{"k": "a", "v": "b"}}, ann); err != nil {
				t.Fatal(err)
			}
			if _, err := db3.DefineClass(ctx, []byte(scratch("ReadsKept", `"c":{"type":"text"},"r":{"formula":"LOOKUP(Kept, v, k = THIS.c)"}`, oneState)), setup); err != nil {
				t.Fatal(err)
			}
			define(plain("Kept", lines+`,"app":"Expenses"`))
			def, _ := db3.Table("Kept")
			in, err := db3.TablesOf("Expenses")
			rows, rerr := db3.TableRows(ctx, "Kept", ann)
			if def == nil || def.Version != 2 || def.App != "Expenses" || err != nil || len(in) != 1 || rerr != nil || len(rows) != 1 || row[1] != "is taken, as the next version, whatever rows it holds and whoever reads it" {
				t.Errorf("a table in use moved to another app: %+v, %v %v, %d rows %v, and the chapter says it %s", def, in, err, len(rows), rerr, row[1])
			}
			define(plain("Kept", lines))
			if in, err := db3.TablesOf("Expenses"); err != nil || len(in) != 0 {
				t.Errorf("the table did not leave the app: %v, %v", in, err)
			}
		},
		"would break a rule of a class that writes it": func(row []string) {
			define(plain("Fed", lines))
			class := scratch("Feeds", `"c":{"type":"text"}`,
				`"states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"go","from":"a","to":"b"}],`+
					`"rules":[{"on":"transition","name":"go","do":[{"action":"table.insert","table":"Fed","row":{"k":"c","v":"c"}}]}]`)
			if _, err := db3.DefineClass(ctx, []byte(class), setup); err != nil {
				t.Fatal(err)
			}
			if got := "is refused, " + tableFinding(db3, `{"name":"Fed","columns":{"k":"text"},"key":["k"]`+lines+`}`); got != row[1] {
				t.Errorf("a changed definition that breaks a rule %s, and the chapter says it %s", got, row[1])
			}
			// A change the rule still fits is taken.
			define(plain("Fed", lines+`,"label":"Changed"`))
		},
	})

	// What is checked when the table is defined.
	col := func(name, columns, more string) string {
		return `{"name":"` + name + `","columns":{` + columns + `}` + more + `}`
	}
	key := `,"key":["k"]`
	heldBy(t, page, "checked when the table is defined", map[string][]string{
		"has no name, or a name that is not letters, digits and underscores": {
			`{"columns":{"k":"text"},"key":["k"]}`,
			col("my table", `"k":"text"`, key),
		},
		"has a name that begins with doc_ or tbl_":                       {col("doc_x", `"k":"text"`, key), col("tbl_x", `"k":"text"`, key)},
		"names an app the database does not have":                        {col("T", `"k":"text"`, key+`,"app":"Nowhere"`)},
		"has the name of a class":                                        {col("Taken", `"k":"text"`, key)},
		"has the name of another table in another case of letters":       {col("empty", `"k":"text"`, key)},
		"has no columns":                                                 {`{"name":"T","columns":{},"key":[]}`},
		"has no key":                                                     {col("T", `"k":"text"`, "")},
		"has two columns whose names differ only in the case of letters": {col("T", `"k":"text","K":"text"`, key)},
		"has a column of a type the engine does not know":                {col("T", `"k":"text","c":"string"`, key), col("T", `"k":"text","c":"decimal(19)"`, key)},
		"has a column that is a list or rows":                            {col("T", `"k":"text","c":"array<text>"`, key), col("T", `"k":"text","c":"rows"`, key)},
		"has a column that is an object":                                 {col("T", `"k":"text","c":"object"`, key)},
		"names a column in its key that it does not have":                {col("T", `"k":"text"`, `,"key":["z"]`)},
		"names a column twice in its key":                                {col("T", `"k":"text"`, `,"key":["k","k"]`)},
		"has a json column in its key":                                   {col("T", `"k":"json"`, key)},
		"has an index that names no column":                              {col("T", `"k":"text"`, key+`,"indexes":[[]]`)},
		"has an index that names a column it does not have":              {col("T", `"k":"text"`, key+`,"indexes":[["z"]]`)},
		"has a binding that is not built yet, duckdb or external": {
			col("T", `"k":"text"`, key+`,"binding":"duckdb"`),
			col("T", `"k":"text"`, key+`,"binding":"external"`),
		},
		"has a binding the engine does not know": {col("T", `"k":"text"`, key+`,"binding":"oracle"`)},
		"has a line of access that reads a column, another table or a document": {
			col("T", `"k":"text"`, key+`,"access":{"read":"k = \"a\""}`),
			col("T", `"k":"text"`, key+`,"access":{"write":"LOOKUP(Empty, v, k = \"a\") = \"x\""}`),
			col("T", `"k":"text"`, key+`,"access":{"read":"LOOKUP(Taken, COUNT(id), c = \"a\") > 0"}`),
		},
		"has a line of access that is not a condition": {col("T", `"k":"text"`, key+`,"access":{"read":"1 + 1"}`)},
		"has a word the engine does not know": {
			col("T", `"k":"text"`, key+`,"colour":"red"`),
			col("T", `"k":"text"`, key+`,"access":{"read":"TRUE","update":"TRUE"}`),
		},
	}, func(definition string) string { return tableFinding(db3, definition) })
	if got := tableFinding(db3, col("T", `"k":"text"`, key+`,"binding":"sqlite"`)); got != "accepted" {
		t.Errorf("the binding sqlite is found %s", got)
	}
	_, err = db3.DefineClass(ctx, []byte(scratch("Empty", `"c":{"type":"text"}`, oneState)), setup)
	if got := codeOf(err); got != "duplicate" {
		t.Errorf("a class with the name of a table is found %s", got)
	}
}
