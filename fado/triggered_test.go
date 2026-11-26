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

	// The time zones, for a machine that has no database of them: the
	// chapter's database is in Warsaw.
	_ "time/tzdata"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on triggered variables is a reference: it says how a
// variable is worked out only at chosen moments, what it keeps beside
// its value, what happens when working it out fails, and what a refresh
// does. TestTriggeredVariables takes the table and the two classes out
// of the chapter, plays its two stories step by step, and holds every
// table of the chapter to what the engine answers.

// moment writes when a triggered variable of a document was worked out,
// as the chapter does.
func moment(doc *fado.Document, name string) string {
	m, ok := doc.Computed[name]
	switch {
	case !ok:
		return "never"
	case m.Version == 0:
		return "never, and failed with " + m.Error
	case m.Error != "":
		return fmt.Sprintf("at version %d, and failed since with %s", m.Version, m.Error)
	}
	return fmt.Sprintf("at version %d", m.Version)
}

func TestTriggeredVariables(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "triggered-variables.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var table, invoice, offer, estimate, bill []byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		switch kindOf(m[1]) {
		case "table":
			table = m[1]
		case "class":
			switch className(string(m[1])) {
			case "Invoice":
				invoice = m[1]
			case "Offer":
				offer = m[1]
			case "Estimate":
				estimate = m[1]
			case "Bill":
				bill = m[1]
			}
		}
	}
	if table == nil || invoice == nil || offer == nil || estimate == nil || bill == nil {
		t.Fatal("the chapter shows the table Rates and the classes Invoice, Offer, Estimate and Bill")
	}

	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	clock := start
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	if _, err := db.DefineTable(ctx, table, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, invoice, setup); err != nil {
		t.Fatal(err)
	}
	ola, piotr, tess := person("ola"), person("piotr"), person("tess", "treasury")
	anna, adam := person("anna", "ACCOUNTING"), person("adam", "ACCOUNTING")
	if _, err := db.ImportRows(ctx, "Rates", []fado.Values{{"code": "EUR", "rate": "4.3000"}, {"code": "USD", "rate": "3.9000"}}, tess); err != nil {
		t.Fatal(err)
	}

	// The story of the invoice. Each step comes ten minutes after the one
	// before; holds keeps the invoice as each step that made a version
	// left it.
	story := taleOf(t, page)
	holds := map[int]*fado.Document{}
	n := 0
	tick := func() {
		n++
		clock = start.Add(time.Duration(n-1) * 10 * time.Minute)
	}
	play := func(do func() (*fado.Document, error)) {
		t.Helper()
		tick()
		doc, err := do()
		if err != nil {
			story.step(n, told(err))
			return
		}
		holds[n] = doc
		story.step(n, fmt.Sprintf("allowed, version %d", doc.Version))
	}
	rate := func(code, value string) {
		t.Helper()
		tick()
		_, err := db.ImportRows(ctx, "Rates", []fado.Values{{"code": code, "rate": value}}, tess)
		story.step(n, told(err))
	}
	refresh := func(as fado.Option, variables ...string) func() (*fado.Document, error) {
		return func() (*fado.Document, error) { return db.Refresh(ctx, "Invoice", "I-1", variables, as) }
	}
	move := func(as fado.Option, name string) func() (*fado.Document, error) {
		return func() (*fado.Document, error) { return db.Transition(ctx, "Invoice", "I-1", name, nil, as) }
	}
	set := func(as fado.Option, values fado.Values) func() (*fado.Document, error) {
		return func() (*fado.Document, error) { return db.Set(ctx, "Invoice", "I-1", values, as) }
	}

	play(func() (*fado.Document, error) {
		return db.Create(ctx, "Invoice", fado.Values{"number": "FV/1", "amount": "100.00", "currency": "EUR"}, ola, fado.WithID("I-1"))
	})
	// A preview shows what the move would work out, and writes nothing.
	tried, err := db.Preview(ctx, "Invoice", "I-1", nil, "check", anna)
	if err != nil || shown(tried.Document.Values["rate"]) != "4.3000" || shown(tried.Document.Values["checked_by"]) != "anna" {
		t.Errorf("a preview of the check shows %v, %v", tried, err)
	}
	if read, err := db.Get(ctx, "Invoice", "I-1", ola); err != nil || read.Version != 1 || read.Values["rate"] != nil {
		t.Errorf("after the preview the invoice is %v, %v", read, err)
	}
	play(move(anna, "check"))
	rate("EUR", "4.4000")
	play(set(anna, fado.Values{"amount": "120.00"}))
	// Piotr may see the invoice and may not change it.
	if _, err := db.Set(ctx, "Invoice", "I-1", fado.Values{"amount": "1.00"}, piotr); codeOf(err) != "forbidden" {
		t.Errorf("Piotr's change of the invoice is answered %s", codeOf(err))
	}
	play(refresh(piotr))
	play(refresh(anna, "checked_by"))
	play(set(anna, fado.Values{"rate": "4.5000"}))
	rate("EUR", "44.0000")
	play(refresh(anna))
	// The moments of the invoice after step 9, and the condition that
	// finds it in SQL.
	if holds[9] != nil {
		moments, err := json.MarshalIndent(holds[9].Computed, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if string(moments) != fenced(t, page, "the moments after step 9") {
			t.Errorf("the moments of the chapter are not what the engine answers:\n%s", moments)
		}
	}
	failed := func() string {
		found, err := db.SQL(ctx, `select id, rate__error from Invoice where rate__error is not null`, nil, setup)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprint(found.Rows)
	}
	if got := failed(); got != "[[I-1 out_of_range]]" {
		t.Errorf("after step 9 the condition on rate__error finds %s", got)
	}
	rate("EUR", "4.4500")
	play(func() (*fado.Document, error) {
		// Step 11 is sent as the command of the chapter.
		answer, err := db.Exec(ctx, fado.Principal{ID: "anna", Kind: fado.ActorPerson, Groups: []string{"ACCOUNTING"}}, "refresh", []byte(fenced(t, page, "command: refresh")))
		if err != nil {
			return nil, err
		}
		var got struct {
			Document *fado.Document `json:"document"`
		}
		if err := json.Unmarshal(answer, &got); err != nil {
			return nil, err
		}
		return got.Document, nil
	})
	if got := failed(); got != "[]" {
		t.Errorf("after step 11 the condition on rate__error finds %s", got)
	}
	play(set(anna, fado.Values{"currency": "USD"}))
	play(move(anna, "return"))
	play(move(adam, "check"))
	play(move(adam, "book"))
	rate("USD", "4.0000")
	play(refresh(adam))
	tick()
	last, err := db.Get(ctx, "Invoice", "I-1", ola)
	if err != nil {
		t.Fatal(err)
	}
	story.step(n, fmt.Sprintf("rate %s, in_pln %s", shown(last.Values["rate"]), shown(last.Values["in_pln"])))
	story.done()

	// What the invoice holds after each step that made a version.
	listed := map[int]bool{}
	for _, row := range rows(marked(t, page, "what the invoice holds")) {
		step, _ := strconv.Atoi(row[0])
		listed[step] = true
		doc := holds[step]
		if doc == nil {
			t.Errorf("the chapter says what the invoice holds after step %s, which made no version", row[0])
			continue
		}
		got := []string{row[0], shown(doc.Values["rate"]), shown(doc.Values["in_pln"]), shown(doc.Values["checked_by"]), shown(doc.Values["as_entered"]), moment(doc, "rate")}
		if !reflect.DeepEqual(got, row) {
			t.Errorf("after step %s the invoice holds %v, and the chapter says %v", row[0], got[1:], row[1:])
		}
	}
	for step := range holds {
		if !listed[step] {
			t.Errorf("step %d made a version, and the chapter does not say what the invoice holds after it", step)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	// The moment: its parts are the members of fado.Moment; a variable
	// never worked out has none; the same value moves it; the history
	// keeps it; the time is the commit's.
	var parts []string
	for i, of := 0, reflect.TypeOf(fado.Moment{}); i < of.NumField(); i++ {
		name, _, _ := strings.Cut(of.Field(i).Tag.Get("json"), ",")
		parts = append(parts, name)
	}
	var named []string
	for _, row := range rows(marked(t, page, "the parts of a moment")) {
		named = append(named, row[0])
	}
	if !reflect.DeepEqual(named, parts) {
		t.Errorf("the chapter names the parts %v of a moment, and the engine has %v", named, parts)
	}
	tenPast := start.Add(10 * time.Minute)
	if at := holds[2].Computed["rate"].At; at != tenPast.Unix() || !strings.Contains(string(page), fmt.Sprintf("%d is 1 December 2026, 09:10 UTC", at)) {
		t.Errorf("the rate of step 2 was worked out at %d, and step 2 was at %d", at, tenPast.Unix())
	}
	if len(holds[1].Computed) != 0 {
		t.Errorf("after step 1 the invoice has the moments %v", holds[1].Computed)
	}
	if holds[13].Values["rate"] != holds[14].Values["rate"] || holds[13].Computed["rate"].Version != 7 || holds[14].Computed["rate"].Version != 9 {
		t.Errorf("the same rate worked out again: %v, then %v", holds[13].Computed["rate"], holds[14].Computed["rate"])
	}
	history, err := db.History(ctx, "Invoice", "I-1", ola)
	if err != nil || len(history) != 10 {
		t.Fatalf("the history has %d versions, %v", len(history), err)
	}
	if history[4].Computed["rate"].Error != "out_of_range" || history[5].Computed["rate"].Error != "" || history[1].Values["checked_by"] != "anna" {
		t.Errorf("the history keeps the moments %v and %v", history[4].Computed["rate"], history[5].Computed["rate"])
	}
	events, err := db.Events(ctx, "Invoice", "I-1", ola)
	if err != nil || len(events) != 10 || events[3].Kind != "refresh" || events[3].ActorID != "piotr" {
		t.Errorf("the events of the invoice: %v, %v", events, err)
	}

	// In SQL.
	view, err := db.SQL(ctx, `select * from Invoice`, nil, setup)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows(marked(t, page, "the columns")) {
		found := false
		for _, column := range view.Columns {
			found = found || column == row[0]
		}
		if !found {
			t.Errorf("the view Invoice has no column %s: %v", row[0], view.Columns)
		}
	}
	answer, err := db.SQL(ctx, fenced(t, page, "the statement"), nil, setup)
	if err != nil {
		t.Fatal(err)
	}
	if want := heading(marked(t, page, "its answer")); !reflect.DeepEqual(answer.Columns, want) {
		t.Errorf("the statement answers the columns %v, and the chapter says %v", answer.Columns, want)
	}
	var answered [][]string
	for _, row := range answer.Rows {
		var cells []string
		for _, cell := range row {
			if cell == nil {
				cells = append(cells, "null")
				continue
			}
			cells = append(cells, fmt.Sprint(cell))
		}
		answered = append(answered, cells)
	}
	if want := rows(marked(t, page, "its answer")); !reflect.DeepEqual(answered, want) {
		t.Errorf("the statement answers %v, and the chapter says %v", answered, want)
	}

	// A lookup that finds nothing is no failure, and the guard of book
	// asks for the rate.
	if _, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2", "amount": "10.00", "currency": "CHF"}, ola, fado.WithID("I-2")); err != nil {
		t.Fatal(err)
	}
	francs, err := db.Transition(ctx, "Invoice", "I-2", "check", nil, anna)
	if err != nil || francs.Values["rate"] != nil || moment(francs, "rate") != "at version 2" {
		t.Errorf("an invoice in a currency the table does not have: %v, %v", francs, err)
	}
	if _, err := db.Transition(ctx, "Invoice", "I-2", "book", nil, anna); codeOf(err) != "guard_false" {
		t.Errorf("booking an invoice without a rate is answered %s", codeOf(err))
	}

	// The statements the chapter makes in passing are played on classes
	// of the test's own, in a database of their own.
	side := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	define := func(name, variables, more string) {
		t.Helper()
		if more == "" {
			more = oneState
		}
		if _, err := side.DefineClass(ctx, []byte(scratch(name, variables, more)), setup); err != nil {
			t.Fatalf("the class %s: %v", name, err)
		}
	}
	create := func(class string, values fado.Values) string {
		t.Helper()
		doc, err := side.Create(ctx, class, values, ola)
		if err != nil {
			t.Fatalf("a document of %s: %v", class, err)
		}
		return doc.ID
	}
	fresh := func(class, id string, as fado.Option) *fado.Document {
		t.Helper()
		doc, err := side.Refresh(ctx, class, id, nil, as)
		if err != nil {
			t.Fatalf("a refresh of %s: %v", class, err)
		}
		return doc
	}
	went := func(class, id, name string) *fado.Document {
		t.Helper()
		doc, err := side.Transition(ctx, class, id, name, nil, ola)
		if err != nil {
			t.Fatalf("%s of %s: %v", name, class, err)
		}
		return doc
	}

	// Following a table: the estimates, in a database of their own, with
	// the rates the chapter says. gauge is a class of the test's own,
	// which keeps the state it was in when the table brought it.
	follows := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	marek := person("marek", "sales-lead")
	if _, err := follows.DefineTable(ctx, table, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := follows.ImportRows(ctx, "Rates", []fado.Values{{"code": "EUR", "rate": "4.2500"}, {"code": "USD", "rate": "3.9000"}}, tess); err != nil {
		t.Fatal(err)
	}
	if _, err := follows.DefineClass(ctx, estimate, setup); err != nil {
		t.Fatalf("the class Estimate of the chapter: %v", err)
	}
	if _, err := follows.DefineClass(ctx, []byte(scratch("Gauge",
		`"at_table":{"type":"text","formula":"IF(ISBLANK(LOOKUP(Rates, rate, code = \"EUR\")), \"\", STATE())","mode":"triggered","on":["table:Rates"]}`,
		`"states":{"initial":"off","list":["off","on"]},"transitions":[{"name":"light","from":"off","to":"on"}]`)), setup); err != nil {
		t.Fatal(err)
	}
	gauge, err := follows.Create(ctx, "Gauge", nil, ola)
	if err != nil {
		t.Fatal(err)
	}
	if gauge, err = follows.Transition(ctx, "Gauge", gauge.ID, "light", nil, ola); err != nil || gauge.Values["at_table"] != nil {
		t.Fatalf("the gauge before the table changed: %v, %v", gauge, err)
	}
	estimates := map[string]*fado.Document{}
	for _, e := range []struct{ id, currency, state string }{
		{"E-1", "EUR", "draft"}, {"E-2", "EUR", "sent"}, {"E-3", "USD", "draft"}, {"E-4", "EUR", "accepted"},
	} {
		doc, err := follows.Create(ctx, "Estimate", fado.Values{"number": e.id, "amount": "100.00", "currency": e.currency}, ola, fado.WithID(e.id))
		if err == nil && e.state != "draft" {
			doc, err = follows.Transition(ctx, "Estimate", e.id, "send", nil, ola)
		}
		if err == nil && e.state == "accepted" {
			doc, err = follows.Transition(ctx, "Estimate", e.id, "accept", nil, marek)
		}
		if err != nil || doc.State != e.state {
			t.Fatalf("the estimate %s in %s: %v, %v", e.id, e.state, doc, err)
		}
		estimates[e.id] = doc
	}
	holding := func(doc *fado.Document) string {
		return fmt.Sprintf("version %d, rate %s, %s in PLN", doc.Version, shown(doc.Values["rate"]), shown(doc.Values["in_pln"]))
	}
	// Tess changes the rate. She may not see an estimate, and the work
	// waits in the outbox until the worker runs.
	if _, err := follows.ImportRows(ctx, "Rates", []fado.Values{{"code": "EUR", "rate": "4.3100"}}, tess); err != nil {
		t.Fatal(err)
	}
	if _, err := follows.Get(ctx, "Estimate", "E-1", tess); codeOf(err) != "unknown_document" {
		t.Errorf("Tess reads an estimate: %v", err)
	}
	if doc, err := follows.Get(ctx, "Estimate", "E-1", ola); err != nil || holding(doc) != holding(estimates["E-1"]) {
		t.Errorf("before the worker ran E-1 holds %v, %v", doc, err)
	}
	queued, err := follows.Outbox(ctx, fado.OutboxFilter{Class: "Estimate"}, setup)
	if err != nil || len(queued) != 1 || queued[0].Action != "follow" || queued[0].Rule != "table:Rates" || queued[0].Status != "pending" || queued[0].PrincipalID != "tess" {
		t.Errorf("the row the change of the table queued: %+v, %v", queued, err)
	}
	if _, err := follows.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	// The gauge was brought by the same change, in the state it is in.
	lit2, err := follows.Get(ctx, "Gauge", gauge.ID, ola)
	if err != nil {
		t.Fatal(err)
	}
	checks(t, page, "what follows the table", func() map[string]func(row []string) {
		by := map[string]func(row []string){}
		for id, before := range estimates {
			after, err := follows.Get(ctx, "Estimate", id, marek)
			if err != nil {
				t.Fatal(err)
			}
			by[id+", in "+fmt.Sprint(before.Values["currency"])] = func(row []string) {
				want := row[3]
				if want == "the same" {
					want = row[2]
				}
				if row[1] != before.State || holding(before) != row[2] || holding(after) != want {
					t.Errorf("%s in %s held %q and holds %q; the chapter says %s, %q and %q", row[0], before.State, holding(before), holding(after), row[1], row[2], row[3])
				}
			}
		}
		return by
	}())
	events, err = follows.Events(ctx, "Estimate", "E-1", marek)
	if err != nil || len(events) != 2 {
		t.Fatalf("the events of E-1: %v, %v", events, err)
	}
	if e := events[1]; e.Kind != "refresh" || e.Name != "table:Rates" || e.ActorID != "tess" || e.AfterVersion != 2 {
		t.Errorf("the event of the version the table brought: %+v", e)
	}
	// Ola read E-1 before the change, and her command says so.
	if _, err := follows.Transition(ctx, "Estimate", "E-1", "send", nil, ola, fado.Saw(1)); codeOf(err) != "stale_version" {
		t.Errorf("a command that saw the version before the table brought the rate is answered %s", codeOf(err))
	}
	// A removed row is a change of the table too: the drafts in euro
	// have no rate, and the estimate that was sent keeps its own.
	if _, err := follows.DeleteRows(ctx, "Rates", []fado.Values{{"code": "EUR"}}, tess); err != nil {
		t.Fatal(err)
	}
	if _, err := follows.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if doc, err := follows.Get(ctx, "Estimate", "E-1", ola); err != nil || doc.Values["rate"] != nil || doc.Version != 3 {
		t.Errorf("E-1 after the row of its currency was removed: %v, %v", doc, err)
	}
	if doc, err := follows.Get(ctx, "Estimate", "E-2", ola); err != nil || holding(doc) != holding(estimates["E-2"]) {
		t.Errorf("E-2 after the row of its currency was removed: %v, %v", doc, err)
	}

	// Following the clock: Acme's bills, in a database in the time zone
	// of Warsaw, on Thursday 26 November 2026 at eleven in the morning
	// there. dial is a class of the test's own, which keeps the state it
	// was in when its schedule brought it.
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	thursday := time.Date(2026, 11, 26, 11, 0, 0, 0, warsaw)
	night := thursday
	nightly := notionDB(t, fado.Options{Now: func() time.Time { return night }, TimeZone: "Europe/Warsaw"})
	if got := nightly.TimeZone(); got != "Europe/Warsaw" {
		t.Errorf("the time zone of the database: %s", got)
	}
	if _, err := nightly.DefineClass(ctx, bill, setup); err != nil {
		t.Fatalf("the class Bill of the chapter: %v", err)
	}
	if _, err := nightly.DefineClass(ctx, []byte(scratch("Dial",
		`"at_schedule":{"formula":"STATE()","mode":"triggered","on":["schedule:hourly"]}`,
		`"states":{"initial":"off","list":["off","on"]},"transitions":[{"name":"light","from":"off","to":"on"}]`)), setup); err != nil {
		t.Fatal(err)
	}
	dial, err := nightly.Create(ctx, "Dial", nil, ola)
	if err != nil {
		t.Fatal(err)
	}
	if dial, err = nightly.Transition(ctx, "Dial", dial.ID, "light", nil, ola); err != nil || dial.Values["at_schedule"] != nil {
		t.Fatalf("the dial before its schedule: %v, %v", dial, err)
	}
	bills := map[string]*fado.Document{}
	for _, b := range []struct{ id, due, words, state string }{
		{"B-1", "2026-11-20", "20 November", "sent"}, {"B-2", "2026-12-10", "10 December", "sent"},
		{"B-3", "2026-11-20", "20 November", "disputed"}, {"B-4", "2026-11-20", "20 November", "paid"},
	} {
		doc, err := nightly.Create(ctx, "Bill", fado.Values{"number": b.id, "due": b.due}, ola, fado.WithID(b.id))
		if err == nil && b.state != "sent" {
			doc, err = nightly.Transition(ctx, "Bill", b.id, map[string]string{"disputed": "dispute", "paid": "pay"}[b.state], nil, anna)
		}
		if err != nil || doc.State != b.state {
			t.Fatalf("the bill %s in %s: %v, %v", b.id, b.state, doc, err)
		}
		bills[b.id+", due "+b.words] = doc
	}
	lateBy := func(doc *fado.Document) string {
		return fmt.Sprintf("version %d, %s days late", doc.Version, shown(doc.Values["days_late"]))
	}
	// Before three in the night nothing runs; at three it does.
	night = time.Date(2026, 11, 27, 2, 59, 0, 0, warsaw)
	if _, err := nightly.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if doc, err := nightly.Get(ctx, "Bill", "B-1", anna); err != nil || doc.Version != 1 {
		t.Errorf("B-1 a minute before its schedule: %v, %v", doc, err)
	}
	night = time.Date(2026, 11, 27, 3, 0, 0, 0, warsaw)
	if _, err := nightly.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	checks(t, page, "what follows the clock", func() map[string]func(row []string) {
		by := map[string]func(row []string){}
		for label, before := range bills {
			after, err := nightly.Get(ctx, "Bill", before.ID, anna)
			if err != nil {
				t.Fatal(err)
			}
			by[label] = func(row []string) {
				want := row[3]
				if want == "the same" {
					want = row[2]
				}
				if row[1] != before.State || lateBy(before) != row[2] || lateBy(after) != want {
					t.Errorf("%s in %s held %q and holds %q; the chapter says %s, %q and %q", row[0], before.State, lateBy(before), lateBy(after), row[1], row[2], row[3])
				}
			}
		}
		return by
	}())
	// The engine itself brought it: the event names the schedule, and
	// nobody.
	events, err = nightly.Events(ctx, "Bill", "B-1", anna)
	if err != nil || len(events) != 2 {
		t.Fatalf("the events of B-1: %v, %v", events, err)
	}
	if e := events[1]; e.Kind != "refresh" || e.Name != "schedule:daily at 03:00" || e.ActorID != "" || e.AfterVersion != 2 {
		t.Errorf("the event of the version the schedule brought: %+v", e)
	}
	if doc, err := nightly.Get(ctx, "Bill", "B-1", anna); err != nil || doc.Values["remind"] != false {
		t.Errorf("B-1 after the night: %v, %v", doc, err)
	}
	// The row of the next night waits in the queue.
	queued, err = nightly.Outbox(ctx, fado.OutboxFilter{Class: "Bill", Status: "pending"}, setup)
	if err != nil || len(queued) != 1 || queued[0].Action != "follow" || queued[0].Rule != "schedule:daily at 03:00" ||
		queued[0].DueAt != time.Date(2026, 11, 28, 3, 0, 0, 0, warsaw).Unix() || queued[0].PrincipalID != "" {
		t.Errorf("the row that waits for the next night: %+v, %v", queued, err)
	}
	dialled, err := nightly.Get(ctx, "Dial", dial.ID, ola)
	if err != nil {
		t.Fatal(err)
	}
	// The words of a schedule, each on a class of the test's own: the
	// next three moments, from that Thursday at eleven on.
	for i, row := range rows(marked(t, page, "the words of a schedule")) {
		at := thursday
		each := notionDB(t, fado.Options{Now: func() time.Time { return at }, TimeZone: "Europe/Warsaw"})
		name := fmt.Sprintf("Word%d", i)
		if _, err := each.DefineClass(ctx, []byte(scratch(name, `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["schedule:`+row[0]+`"]}`, oneState)), setup); err != nil {
			t.Errorf("a class on the schedule %q: %v", row[0], err)
			continue
		}
		var next []string
		for n := 0; n < 3; n++ {
			waits, err := each.Outbox(ctx, fado.OutboxFilter{Class: name, Status: "pending"}, setup)
			if err != nil || len(waits) != 1 {
				t.Fatalf("the row that waits for %q: %v, %v", row[0], waits, err)
			}
			at = time.Unix(waits[0].DueAt, 0)
			next = append(next, at.In(warsaw).Format("2 Jan 15:04"))
			if _, err := each.Drain(ctx, setup); err != nil {
				t.Fatal(err)
			}
		}
		if got := strings.Join(next, ", "); got != row[2] {
			t.Errorf("the schedule %q fires next at %s, and the chapter says %s", row[0], got, row[2])
		}
	}

	// The triggers, and what STATE() is in each.
	define("Lamp", `"at_first":{"formula":"STATE()","mode":"triggered","on":["enter:off"]},
		"at_enter":{"formula":"STATE()","mode":"triggered","on":["enter:on"]},
		"at_exit":{"formula":"STATE()","mode":"triggered","on":["exit:off"]},
		"at_move":{"formula":"STATE()","mode":"triggered","on":["transition:light"]},
		"at_before":{"formula":"STATE()","mode":"triggered","on":["before:light"]},
		"at_refresh":{"formula":"STATE()","mode":"triggered","on":["refresh"]},
		"times":{"type":"int","formula":"times + 1","mode":"triggered","on":["exit:off","transition:light","enter:on","enter:off"]}`,
		`"states":{"initial":"off","list":["off","on"]},"transitions":[{"name":"light","from":"off","to":"on"},{"name":"blink","from":"off","to":"off"}]`)
	lamp := create("Lamp", nil)
	made, err := side.Get(ctx, "Lamp", lamp, ola)
	if err != nil {
		t.Fatal(err)
	}
	blinked := went("Lamp", lamp, "blink")
	lit := went("Lamp", lamp, "light")
	shone := fresh("Lamp", lamp, ola)
	state := map[string]string{"the state entered": "on", "the state left": "off", "the state the document is in": "on"}
	checks(t, page, "the triggers", map[string]func(row []string){
		"enter:<state>": func(row []string) {
			if made.Values["at_first"] != "off" || moment(made, "at_first") != "at version 1" {
				t.Errorf("the create does not fire the entry of the first state: %v", made.Values)
			}
			if lit.Values["at_enter"] != state[row[2]] {
				t.Errorf("on %s STATE() is %v, and the chapter says %s", row[0], lit.Values["at_enter"], row[2])
			}
		},
		"exit:<state>": func(row []string) {
			if lit.Values["at_exit"] != state[row[2]] {
				t.Errorf("on %s STATE() is %v, and the chapter says %s", row[0], lit.Values["at_exit"], row[2])
			}
		},
		"transition:<name>": func(row []string) {
			if lit.Values["at_move"] != state[row[2]] || blinked.Values["at_move"] != nil {
				t.Errorf("on %s STATE() is %v, and the chapter says %s", row[0], lit.Values["at_move"], row[2])
			}
		},
		"before:<name>": func(row []string) {
			if lit.Values["at_before"] != state[row[2]] || blinked.Values["at_before"] != nil || moment(lit, "at_before") != "at version 3" {
				t.Errorf("on %s STATE() is %v, worked out %s, and the chapter says %s", row[0], lit.Values["at_before"], moment(lit, "at_before"), row[2])
			}
		},
		"refresh": func(row []string) {
			if shone.Values["at_refresh"] != state[row[2]] || lit.Values["at_refresh"] != nil {
				t.Errorf("on %s STATE() is %v, and the chapter says %s", row[0], shone.Values["at_refresh"], row[2])
			}
		},
		"table:<name>": func(row []string) {
			if lit2.Values["at_table"] != state[row[2]] {
				t.Errorf("on %s STATE() is %v, and the chapter says %s", row[0], lit2.Values["at_table"], row[2])
			}
		},
		"schedule:<when>": func(row []string) {
			if dialled.Values["at_schedule"] != state[row[2]] {
				t.Errorf("on %s STATE() is %v, and the chapter says %s", row[0], dialled.Values["at_schedule"], row[2])
			}
		},
	})
	// Once in a commit: the create fires one of its triggers, the move
	// into the same state two, the move to another state three.
	if got := []any{made.Values["times"], blinked.Values["times"], lit.Values["times"]}; fmt.Sprint(got) != "[1 2 3]" {
		t.Errorf("a variable that lists several triggers was worked out %v times", got)
	}
	// A transition that time makes fires the triggers, as whoever began
	// the wait.
	define("Hold", `"a":{"type":"int"},"by":{"formula":"USER()","mode":"triggered","on":["transition:expire"]}`,
		`"states":{"initial":"held","list":["held","gone"]},"transitions":[{"name":"expire","from":"held","to":"gone","after":"1d"}]`)
	hold := create("Hold", nil)
	clock = clock.Add(25 * time.Hour)
	if _, err := side.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if gone, err := side.Get(ctx, "Hold", hold, ola); err != nil || gone.State != "gone" || gone.Values["by"] != "ola" {
		t.Errorf("a transition that time made: %v, %v", gone, err)
	}

	// How it works: the guard of a move sees the value from before, a
	// rule of the move the new one.
	define("Gate", `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["enter:in"]},"seen":{"type":"int"}`,
		`"states":{"initial":"out","list":["out","in"]},"transitions":[{"name":"guarded","from":"out","to":"in","guard":"NOT ISBLANK(b)"},{"name":"open","from":"out","to":"in"}],
		 "rules":[{"on":"enter","state":"in","do":[{"action":"set","values":{"seen":"b"}}]}]`)
	gate := create("Gate", fado.Values{"a": 1})
	if _, err := side.Transition(ctx, "Gate", gate, "guarded", nil, ola); codeOf(err) != "guard_false" {
		t.Errorf("a guard that reads what its own move works out is answered %s", codeOf(err))
	}
	if in := went("Gate", gate, "open"); shown(in.Values["b"]) != "2" || shown(in.Values["seen"]) != "2" {
		t.Errorf("a rule of the move reads %v where the variable is %v", in.Values["seen"], in.Values["b"])
	}
	// A change of a value the formula reads does not work it out again;
	// the variables of one commit are worked out in the order of the
	// class.
	define("Order", `"a":{"type":"int"},
		"early":{"type":"int","formula":"late * 10","mode":"triggered","on":["refresh"]},
		"late":{"formula":"a","mode":"triggered","on":["refresh"]},
		"after":{"formula":"late + 1","mode":"triggered","on":["refresh"]}`, "")
	order := create("Order", fado.Values{"a": 1})
	first := fresh("Order", order, ola)
	three := func(doc *fado.Document) string {
		return shown(doc.Values["early"]) + " " + shown(doc.Values["late"]) + " " + shown(doc.Values["after"])
	}
	if got := three(first); got != "0 1 2" {
		t.Errorf("the first refresh works out early, late and after as %s", got)
	}
	changed, err := side.Set(ctx, "Order", order, fado.Values{"a": 5}, ola)
	if err != nil || shown(changed.Values["late"]) != "1" {
		t.Errorf("a change of a value the formula reads: %v, %v", changed, err)
	}
	if got := three(fresh("Order", order, ola)); got != "10 5 6" {
		t.Errorf("the second refresh works out early, late and after as %s", got)
	}

	// A caller who may not read the variable sees neither its value nor
	// its moment.
	define("Sealed", `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["refresh"]}`,
		oneState+`,"rights":{"*":{"b":{"read":["ACCOUNTING"]}}}`)
	sealed := create("Sealed", fado.Values{"a": 1})
	if doc := fresh("Sealed", sealed, anna); shown(doc.Values["b"]) != "2" || moment(doc, "b") != "at version 2" {
		t.Errorf("a caller who may read the variable is shown %v", doc.Values)
	}
	if doc, err := side.Get(ctx, "Sealed", sealed, ola); err != nil || len(doc.Computed) != 0 {
		t.Errorf("a caller who may not read the variable is shown the moment %v, %v", doc.Computed, err)
	} else if _, has := doc.Values["b"]; has {
		t.Errorf("a caller who may not read the variable is shown its value")
	}

	// When working it out fails: the triggered variable keeps what it
	// had and says the code; the same formula in an ordinary computed
	// variable refuses the commit.
	fails := func(variables string, values fado.Values) (triggered, auto string) {
		t.Helper()
		name := fmt.Sprintf("Fails%d", len(side.Classes()))
		define(name, strings.ReplaceAll(variables, "MODE", `,"mode":"triggered","on":["refresh"]`), "")
		doc := fresh(name, create(name, values), ola)
		if doc.Values["b"] != nil || doc.Computed["b"].Version != 0 {
			t.Errorf("a variable that has only ever failed holds %v, worked out %s", doc.Values["b"], moment(doc, "b"))
		}
		define(name+"Auto", strings.ReplaceAll(variables, "MODE", ""), "")
		_, err := side.Create(ctx, name+"Auto", values, ola)
		return doc.Computed["b"].Error, codeOf(err)
	}
	failing := func(variables string, values fado.Values) func(row []string) {
		return func(row []string) {
			t.Helper()
			if triggered, auto := fails(variables, values); triggered != row[1] || auto != row[1] {
				t.Errorf("%s: the triggered variable says %s and the ordinary one refuses with %s, and the chapter says %s", row[0], triggered, auto, row[1])
			}
		}
	}
	checks(t, page, "what fails", map[string]func(row []string){
		"the formula fails, as a division by zero does":                    failing(`"a":{"type":"number"},"b":{"formula":"100 / a"MODE}`, fado.Values{"a": 0}),
		"the value is outside the min and max of the variable":             failing(`"a":{"type":"int"},"b":{"type":"int","formula":"a * 2","max":10MODE}`, fado.Values{"a": 50}),
		"the value is a text that is not among the values of the variable": failing(`"a":{"type":"text"},"b":{"type":"text","formula":"a & \"!\"","values":["x!","y!"]MODE}`, fado.Values{"a": "z"}),
	})
	// A range needs the type: without one the range is taken and not
	// applied. This pins what the engine does today.
	define("Loose", `"a":{"type":"int"},"b":{"formula":"a * 2","max":10},"c":{"formula":"a * 2","max":10,"mode":"triggered","on":["refresh"]}`, "")
	if loose := fresh("Loose", create("Loose", fado.Values{"a": 50}), ola); shown(loose.Values["b"]) != "100" || shown(loose.Values["c"]) != "100" || loose.Computed["c"].Error != "" {
		t.Errorf("a range on a computed variable that names no type now holds: %v, %v; the chapter says it does not", loose.Values, loose.Computed)
	}

	// Refreshing: a variable that reads the caller and lists refresh
	// names whoever refreshed; a refresh is a version though nothing is
	// worked out.
	define("Who", `"a":{"type":"int"},"who":{"formula":"USER()","mode":"triggered","on":["refresh"]}`, "")
	if doc := fresh("Who", create("Who", nil), piotr); doc.Values["who"] != "piotr" {
		t.Errorf("a variable that reads the caller holds %v after Piotr's refresh", doc.Values["who"])
	}
	define("Plain", `"a":{"type":"int"}`, "")
	if doc := fresh("Plain", create("Plain", nil), ola); doc.Version != 2 {
		t.Errorf("a refresh of a class with no triggered variable leaves version %d", doc.Version)
	}

	// A finished document: the move into a final state works out.
	define("Shut", `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["enter:done"]}`,
		`"states":{"initial":"open","list":["open","done"],"final":["done"]},"transitions":[{"name":"finish","from":"open","to":"done"}]`)
	if done := went("Shut", create("Shut", fado.Values{"a": 1}), "finish"); shown(done.Values["b"]) != "2" {
		t.Errorf("the move into a final state works out %v", done.Values["b"])
	}

	// Changing a class.
	note := func(variables string) {
		t.Helper()
		define("Note", variables, "")
	}
	note(`"a":{"type":"int"},"b":{"formula":"a * 2"}`)
	kept := create("Note", fado.Values{"a": 5})
	note(`"a":{"type":"int"},"b":{"formula":"a * 3","mode":"triggered","on":["refresh"]},"c":{"formula":"a + 100","mode":"triggered","on":["refresh"]}`)
	if doc, err := side.Get(ctx, "Note", kept, ola); err != nil || shown(doc.Values["b"]) != "10" || len(doc.Computed) != 0 || doc.Values["c"] != nil {
		t.Errorf("after the class made the variable triggered the document holds %v, %v, %v", doc.Values, doc.Computed, err)
	}
	if doc := fresh("Note", kept, ola); shown(doc.Values["b"]) != "15" || shown(doc.Values["c"]) != "105" || moment(doc, "c") != "at version 2" {
		t.Errorf("a refresh after the change of the class leaves %v", doc.Values)
	}

	// The commands.
	exec := func(as fado.Principal, name, object string) map[string]json.RawMessage {
		t.Helper()
		got, err := db.Exec(ctx, as, name, []byte(object))
		if err != nil {
			t.Fatalf("the command %s: %v", name, err)
		}
		var answer map[string]json.RawMessage
		if err := json.Unmarshal(got, &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}
	accountant := fado.Principal{ID: "anna", Kind: fado.ActorPerson, Groups: []string{"ACCOUNTING"}}
	commands := []struct {
		to, name string
		holds    func() bool
	}{
		{"work triggered variables out again", "refresh", func() bool {
			return strings.Contains(string(exec(accountant, "refresh", `{"class":"Invoice","id":"I-2"}`)["document"]), `"version":3`)
		}},
		{"read a document with its moments", "get", func() bool {
			return strings.Contains(string(exec(accountant, "get", `{"class":"Invoice","id":"I-1"}`)["document"]), `"computed":{"as_entered":{"version":9,`)
		}},
		{"see what a move would work out, and write nothing", "preview", func() bool {
			if _, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/3", "amount": "10.00", "currency": "USD"}, ola, fado.WithID("I-3")); err != nil {
				t.Fatal(err)
			}
			tried := string(exec(accountant, "preview", `{"class":"Invoice","id":"I-3","transition":"check"}`)["preview"])
			after, err := db.Get(ctx, "Invoice", "I-3", ola)
			return strings.Contains(tried, `"checked_by":"anna"`) && strings.Contains(tried, `"rate":{"version":2,`) &&
				err == nil && after.Version == 1 && after.Values["rate"] == nil
		}},
	}
	named = nil
	for i, row := range rows(marked(t, page, "the commands")) {
		named = append(named, row[1])
		if i >= len(commands) || commands[i].to != row[0] || commands[i].name != row[1] {
			t.Errorf("the chapter names the command %s to %s, and the test does not play it so", row[1], row[0])
			continue
		}
		if !commands[i].holds() {
			t.Errorf("the command %s does not %s", row[1], row[0])
		}
	}
	if len(named) != len(commands) {
		t.Errorf("the chapter lists the commands %v, and the test plays %d", named, len(commands))
	}

	// The two kinds side by side.
	checks(t, page, "two kinds", map[string]func(row []string){
		"Written with": func([]string) {
			for _, variables := range []string{`"a":{"type":"int"},"b":{"formula":"a + 1"}`, `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"auto"}`,
				`"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["refresh"]}`} {
				if got := finding(side, scratch("Kinds", variables, oneState)); got != "accepted" {
					t.Errorf("the variables %s are found %s", variables, got)
				}
			}
		},
		"Worked out": func([]string) {
			if holds[4].Values["in_pln"] == holds[2].Values["in_pln"] || holds[4].Values["rate"] != holds[2].Values["rate"] {
				t.Errorf("after the amount changed the invoice holds %v", holds[4].Values)
			}
		},
		"In between": func([]string) {
			if holds[5].Values["rate"] == holds[4].Values["rate"] {
				t.Errorf("the refresh of step 5 left the rate %v", holds[5].Values["rate"])
			}
		},
		"Its formula may read": func([]string) {
			if got := finding(db, scratch("Kinds", `"c":{"type":"text"},"b":{"formula":"LOOKUP(Rates, rate, code = THIS.c)"}`, oneState)); got != "accepted" {
				t.Errorf("a computed variable that reads a table is found %s", got)
			}
		},
		"Says when it was worked out": func([]string) {
			if _, has := holds[2].Computed["in_pln"]; has || moment(holds[2], "rate") != "at version 2" {
				t.Errorf("the moments after step 2 are %v", holds[2].Computed)
			}
		},
		"When working it out fails": func([]string) {
			if holds[9] == nil || holds[9].Computed["rate"].Error == "" {
				t.Errorf("step 9 made no version with a failure")
			}
		},
	})

	// What is checked when the class is defined.
	one := func(variables string) string { return scratch("T", variables, oneState) }
	chart := `"states":{"initial":"a","list":["a","b","z","y"],"final":["z","y"]},"transitions":[{"name":"go","from":"a","to":"b"},{"name":"end","from":"b","to":"z"},{"name":"undo","from":"z","to":"y"}]`
	on := func(trigger string) string {
		return scratch("T", `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["`+trigger+`"]}`, chart)
	}
	ruled := func(variables, do string) string {
		return scratch("T", variables, oneState+`,"rules":[{"on":"create","do":[`+do+`]}]`)
	}
	notOnRefresh := `"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["enter:a"]}`
	held(t, db, page, map[string][]string{
		"an input has a mode or an on": {
			one(`"a":{"type":"int","mode":"triggered","on":["refresh"]}`),
			one(`"a":{"type":"int","on":["refresh"]}`),
		},
		"a computed variable has an on and is not triggered": {one(`"a":{"type":"int"},"b":{"formula":"a + 1","on":["refresh"]}`)},
		"a triggered variable has no on, or an empty one": {
			one(`"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered"}`),
			one(`"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":[]}`),
		},
		"the mode is neither auto nor triggered": {one(`"a":{"type":"int"},"b":{"formula":"a + 1","mode":"sometimes","on":["refresh"]}`)},
		"a trigger is none of the seven, or is written wrongly, as enter with no state": {
			on("values_change"), on("create"), on("enter"), on("exit"), on("transition"), on("before"), on("refresh:now"), on("table"),
		},
		"a trigger names a state the class does not have":      {on("enter:nowhere"), on("exit:nowhere")},
		"a trigger names a transition the class does not have": {on("transition:fly"), on("before:fly")},
		"schedule:<when> is written in words the engine does not read": {
			on("schedule:"), on("schedule:nightly"), on("schedule:daily at 25:00"), on("schedule:every 7h"), on("schedule:every 1m"), on("schedule:monthly on 31"),
		},
		"table:<name> names a table the variable's formula does not look into": {
			on("table:Rates"),
			one(`"c":{"type":"text"},"b":{"formula":"LOOKUP(Rates, rate, code = THIS.c)","mode":"triggered","on":["table:Limits"]}`),
		},
		"table:<name> names a class, or it or schedule:<when> stands on a list that is kept": {
			one(`"b":{"type":"rows","schema":{"code":"text","rate":"decimal(4)"},"formula":"QUERY(Rates, {code, rate})","mode":"triggered","on":["schedule:daily"]}`),
			one(`"a":{"type":"int"},"b":{"type":"number","formula":"LOOKUP(Invoice, COUNT(id), currency = \"EUR\")","mode":"triggered","on":["table:Invoice"]}`),
			one(`"b":{"type":"rows","schema":{"code":"text","rate":"decimal(4)"},"formula":"QUERY(Rates, {code, rate})","mode":"triggered","on":["table:Rates"]}`),
		},
		"a trigger could fire only on a frozen document": {on("exit:z"), on("transition:undo"), on("before:undo"), on("enter:y")},
		"a triggered variable has a default or is required": {
			one(`"a":{"type":"int"},"b":{"formula":"a + 1","default":"1","mode":"triggered","on":["refresh"]}`),
			one(`"a":{"type":"int"},"b":{"formula":"a + 1","required":true,"mode":"triggered","on":["refresh"]}`),
		},
		"a triggered formula reads other documents and names no type": {one(`"a":{"type":"int"},"b":{"formula":"LOOKUP(Invoice, COUNT(id), currency = \"EUR\")","mode":"triggered","on":["refresh"]}`)},
		"a triggered formula reads its own value and names no type":   {one(`"a":{"type":"int"},"b":{"formula":"b + 1","mode":"triggered","on":["refresh"]}`)},
		"the type is not the type of the formula":                     {one(`"a":{"type":"int"},"b":{"type":"text","formula":"a + 1","mode":"triggered","on":["refresh"]}`)},
		"a rule refreshes a variable that is not triggered, or does not list refresh": {
			ruled(`"a":{"type":"int"},"b":{"formula":"a + 1"}`, `{"action":"refresh","variables":["b"]}`),
			ruled(notOnRefresh, `{"action":"refresh","variables":["b"]}`),
		},
		"a rule refreshes, and no variable of the class lists refresh": {ruled(notOnRefresh, `{"action":"refresh"}`)},
		"a rule sets a triggered variable":                             {ruled(notOnRefresh, `{"action":"set","values":{"b":"1"}}`)},
	})
	// The entry of a final state is a trigger like any other.
	if got := finding(db, on("enter:z")); got != "accepted" {
		t.Errorf("a variable worked out on the entry of a final state is found %s", got)
	}

	// What is refused.
	refused := func(err error) func(row []string) {
		return func(row []string) {
			t.Helper()
			var refusal *fado.Error
			if !errors.As(err, &refusal) {
				t.Errorf("%s: the engine answers %v", row[0], err)
				return
			}
			names := ""
			if refusal.Variable != "" {
				names = "the variable"
			}
			if refusal.Code != row[1] || names != row[2] {
				t.Errorf("%s: the engine answers %s and names %q, and the chapter says %s and %q", row[0], refusal.Code, refusal.Variable, row[1], row[2])
			}
		}
	}
	// I-2 is in the state checked; I-1 is booked.
	try := func(variables ...string) error {
		_, err := db.Refresh(ctx, "Invoice", "I-2", variables, anna)
		return err
	}
	_, setErr := db.Set(ctx, "Invoice", "I-2", fado.Values{"rate": "4.0000"}, anna)
	_, resetErr := db.Reset(ctx, "Invoice", "I-2", []string{"rate"}, anna)
	_, finalErr := db.Refresh(ctx, "Invoice", "I-1", nil, anna)
	mine := `{"name":"Mine","variables":{"a":{"type":"int"},"b":{"formula":"a + 1","mode":"triggered","on":["refresh"]}},` + oneState +
		`,"access":{"read":"CREATEDBY() = USER()","create":"TRUE","update":"TRUE","delete":"TRUE"}}`
	if _, err := side.DefineClass(ctx, []byte(mine), setup); err != nil {
		t.Fatal(err)
	}
	_, unseenErr := side.Refresh(ctx, "Mine", create("Mine", nil), nil, piotr)
	// A formula that reads other documents sees every document, whoever
	// the caller is: Piotr sees no document of Mine, and his refresh
	// counts Ola's.
	define("Tally", `"a":{"type":"int"},"n":{"type":"number","formula":"LOOKUP(Mine, COUNT(id), id <> \"\")","mode":"triggered","on":["refresh"]}`, "")
	if seen, err := side.Count(ctx, fado.Filter{Class: "Mine"}, piotr); err != nil || seen != 0 {
		t.Errorf("Piotr sees %d documents of Mine, %v", seen, err)
	}
	if doc := fresh("Tally", create("Tally", nil), piotr); shown(doc.Values["n"]) != "1" {
		t.Errorf("a formula that counts the documents of a class nobody else may see counts %v", doc.Values["n"])
	}
	checks(t, page, "what is refused", map[string]func(row []string){
		"refreshes a variable that does not list refresh": refused(try("checked_by")),
		"refreshes a variable that is not triggered":      refused(try("in_pln")),
		"refreshes a variable the class does not have":    refused(try("nothing")),
		"names a variable twice in a refresh":             refused(try("rate", "rate")),
		"sets a triggered variable":                       refused(setErr),
		"resets a triggered variable":                     refused(resetErr),
		"refreshes a document in a final state":           refused(finalErr),
		"refreshes a document they may not see":           refused(unseenErr),
	})

	// The second story: what a triggered formula may read.
	day := start
	sales := notionDB(t, fado.Options{Now: func() time.Time { return day }})
	if _, err := sales.DefineClass(ctx, offer, setup); err != nil {
		t.Fatal(err)
	}
	iza, marek := person("iza"), person("marek", "sales-lead")
	steps := []func() error{
		func() error {
			_, err := sales.Create(ctx, "Offer", fado.Values{"customer": "ACME"}, ola, fado.WithID("O-1"))
			return err
		},
		func() error { _, err := sales.Transition(ctx, "Offer", "O-1", "send", nil, ola); return err },
		func() error { _, err := sales.Transition(ctx, "Offer", "O-1", "return", nil, marek); return err },
		func() error {
			if _, err := sales.Create(ctx, "Offer", fado.Values{"customer": "Borg"}, iza, fado.WithID("O-2")); err != nil {
				return err
			}
			_, err := sales.Transition(ctx, "Offer", "O-2", "send", nil, iza)
			return err
		},
		func() error { _, err := sales.Transition(ctx, "Offer", "O-1", "send", nil, ola); return err },
		func() error { _, err := sales.Transition(ctx, "Offer", "O-1", "accept", nil, marek); return err },
	}
	offered := rows(marked(t, page, "the offer"))
	if len(offered) != len(steps) {
		t.Fatalf("the story of the offer has %d steps, and the test plays %d", len(offered), len(steps))
	}
	for i, row := range offered {
		of, month, _ := strings.Cut(row[1], " ")
		date, err := strconv.Atoi(of)
		if err != nil || month != "December" || row[0] != strconv.Itoa(i+1) {
			t.Fatalf("step %s of the offer is on %q", row[0], row[1])
		}
		day = time.Date(2026, 12, date, 9, 0, 0, 0, time.UTC)
		if err := steps[i](); err != nil {
			t.Fatalf("step %s of the offer: %v", row[0], err)
		}
		doc, err := sales.Get(ctx, "Offer", "O-1", ola)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{shown(doc.Values["sent_on"]), shown(doc.Values["rounds"]), shown(doc.Values["ahead"]), shown(doc.Values["in_review"])}
		if !reflect.DeepEqual(got, row[4:]) {
			t.Errorf("after step %s the offer holds %v, and the chapter says %v", row[0], got, row[4:])
		}
	}
	// A filter asks for the value, and not for the function.
	if found, err := sales.Count(ctx, fado.Filter{Class: "Offer", Where: `rounds >= 2`}, marek); err != nil || found != 1 {
		t.Errorf("the filter on rounds finds %d, %v", found, err)
	}
	if _, err := sales.Count(ctx, fado.Filter{Class: "Offer", Where: `ENTRIES("review") >= 2`}, marek); err == nil {
		t.Errorf("a filter that asks ENTRIES is taken")
	}
	// What a triggered formula may read, and an ordinary one may not.
	kind := map[string]string{"other documents": `"type":"number",`, "the value it holds": `"type":"int",`}
	reads := map[string]func(row []string){}
	for _, what := range []string{"the caller", "the clock", "the state of the document", "the life of the document", "other documents", "the value it holds"} {
		reads[what] = func(row []string) {
			t.Helper()
			formula, _ := json.Marshal(row[1])
			chart := `"states":{"initial":"draft","list":["draft","review"]},"transitions":[{"name":"send","from":"draft","to":"review"}]`
			variable := func(mode string) string {
				return scratch("Reads", `"x":{"type":"int"},"rounds":{`+kind[row[0]]+`"formula":`+string(formula)+mode+`}`, chart)
			}
			if got := finding(sales, variable(`,"mode":"triggered","on":["enter:review"]`)); got != "accepted" {
				t.Errorf("a triggered formula that reads %s, %s, is found %s", row[0], row[1], got)
			}
			if got := finding(sales, variable("")); got != row[2] {
				t.Errorf("an ordinary formula that reads %s, %s, is found %s, and the chapter says %s", row[0], row[1], got, row[2])
			}
		}
	}
	checks(t, page, "what its formula may read", reads)
}
