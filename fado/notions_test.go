package fado_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapters on the notions explain what the engine does in words and
// in tables. These tests play each chapter against the engine: its
// class, its story step by step, and every table that says what is
// answered, so that a chapter cannot explain what the engine does not
// do.

// notion reads a chapter under docs/manual/notions.
func notion(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "docs", "manual", "notions", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// notionClass returns the first whole class a chapter shows.
func notionClass(t *testing.T, page []byte) []byte {
	t.Helper()
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		if kindOf(m[1]) == "class" {
			return m[1]
		}
	}
	t.Fatal("the chapter shows no class")
	return nil
}

// notionClasses returns every whole class a chapter shows, in the
// order of the page.
func notionClasses(t *testing.T, page []byte) [][]byte {
	t.Helper()
	var classes [][]byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		if kindOf(m[1]) == "class" {
			classes = append(classes, m[1])
		}
	}
	return classes
}

// marked returns what follows a marker comment of a chapter up to the
// next empty line: a table, or a fenced block without its fences.
func marked(t *testing.T, page []byte, marker string) string {
	t.Helper()
	text := strings.ReplaceAll(string(page), "\r\n", "\n")
	_, rest, found := strings.Cut(text, "<!-- "+marker+" -->\n")
	if !found {
		t.Fatalf("the chapter has no marker %q", marker)
	}
	if fenced, ok := strings.CutPrefix(rest, "```json\n"); ok {
		body, _, _ := strings.Cut(fenced, "\n```")
		return body
	}
	body, _, _ := strings.Cut(rest, "\n\n")
	return body
}

// rows returns the cells of a table of a chapter, its heading and the
// line under it left out, each cell without its backticks.
func rows(table string) [][]string {
	var out [][]string
	for i, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if i < 2 {
			continue
		}
		var cells []string
		for _, cell := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
			cells = append(cells, strings.ReplaceAll(strings.TrimSpace(cell), "`", ""))
		}
		out = append(out, cells)
	}
	return out
}

// codesIn returns the codes a table of refusals names: the words in
// backticks, after its first column, that are written like a code.
func codesIn(table string) []string {
	var out []string
	code := regexp.MustCompile("`([a-z]+(_[a-z]+)*)`")
	for i, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if i < 2 {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for _, cell := range cells[1:] {
			for _, m := range code.FindAllStringSubmatch(cell, -1) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// held plays a chapter's table of what is checked when a class is
// defined. For each row, the definitions the test has under the words
// of that row must be found with the code the row gives. A row the test
// has no definition for fails, and so does a definition under words no
// row has, so the words and the code of a row cannot drift apart.
func held(t *testing.T, db *fado.DB, page []byte, cases map[string][]string) {
	t.Helper()
	listed := map[string]bool{}
	for _, row := range rows(marked(t, page, "checked when the class is defined")) {
		what, code := row[0], row[len(row)-1]
		listed[what] = true
		if len(cases[what]) == 0 {
			t.Errorf("the chapter says a class that %q is refused with %s, and the test has no such class", what, code)
		}
		for _, definition := range cases[what] {
			if got := finding(db, definition); got != code {
				t.Errorf("a class that %s: the engine finds %s, and the chapter says %s", what, got, code)
			}
		}
	}
	for what := range cases {
		if !listed[what] {
			t.Errorf("the test has a class that %q, and the chapter has no such row", what)
		}
	}
}

// finding says what the engine finds in a class definition: the code of
// the first finding, or "accepted".
func finding(db *fado.DB, definition string) string {
	for _, d := range db.ValidateClass([]byte(definition)) {
		if !d.Note {
			return d.Code
		}
	}
	return "accepted"
}

// scratch wraps variables, and what else a class needs, into a class
// that anybody may use, for the statements a chapter makes in passing.
func scratch(name, variables, more string) string {
	if more != "" {
		more = "," + more
	}
	return `{"name":"` + name + `","variables":{` + variables + `}` + more +
		`,"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`
}

const oneState = `"states":{"initial":"a","list":["a"]}`

// seen collects the codes a test met, so that a chapter's table of
// refusals can be held to them.
type seen map[string]bool

func (s seen) note(answered string) string {
	code, _, _ := strings.Cut(answered, ",")
	code, _, _ = strings.Cut(code, " ")
	s[code] = true
	return answered
}

func (s seen) covers(t *testing.T, what, table string) {
	t.Helper()
	codes := codesIn(table)
	if len(codes) == 0 {
		t.Errorf("%s: the table names no code", what)
	}
	for _, code := range codes {
		if !s[code] {
			t.Errorf("%s: the chapter names %s, and the test never met it", what, code)
		}
	}
}

func notionDB(t *testing.T, o fado.Options) *fado.DB {
	t.Helper()
	o.Outbox.Manual = true
	db, err := fado.Open(filepath.Join(t.TempDir(), "notion.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNotion_ClassesAndDocuments(t *testing.T) {
	page := notion(t, "classes-and-documents.md")
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	ctx := context.Background()
	met := seen{}
	check := func(step string, err error, answered string) {
		t.Helper()
		want(t, step, err, met.note(answered))
	}

	invoice, err := os.ReadFile(filepath.Join("..", "examples", "first", "invoice.json"))
	if err != nil {
		t.Fatal(err)
	}
	class, err := db.DefineClass(ctx, invoice, setup)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := db.DefineClass(ctx, invoice, setup); err != nil || again.Version != class.Version {
		t.Errorf("a class defined again unchanged: version %d, %v; the chapter says it stays the version it was", again.Version, err)
	}
	krzysztof, anna := person("krzysztof"), person("anna", "ACCOUNTING")

	// The document as the engine answers it.
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/017", "supplier": "ACME", "net": "1000.00 PLN"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	answered, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	made := regexp.MustCompile(`"id": "[0-9A-HJKMNP-TV-Z]{26}"`)
	shown := marked(t, page, "the document")
	if !made.MatchString(shown) || !made.Match(answered) {
		t.Errorf("an id the engine makes is 26 letters and digits; the chapter shows another, or the engine made another: %s", doc.ID)
	}
	if got := made.ReplaceAllString(string(answered), `"id": "..."`); got != made.ReplaceAllString(shown, `"id": "..."`) {
		t.Errorf("the document of the chapter is not what the engine answers:\n%s", got)
	}

	// The table of its parts names every part, and no other.
	var parts, named []string
	kind := reflect.TypeOf(fado.Document{})
	for i := 0; i < kind.NumField(); i++ {
		tag, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
		parts = append(parts, tag)
	}
	for _, row := range rows(marked(t, page, "what a document carries")) {
		for _, name := range strings.Split(row[0], ",") {
			named = append(named, strings.TrimSpace(name))
		}
	}
	sort.Strings(parts)
	sort.Strings(named)
	if !reflect.DeepEqual(parts, named) {
		t.Errorf("a document carries %v, and the chapter names %v", parts, named)
	}

	// The id.
	second, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/018", "supplier": "ACME", "net": "5.00 PLN"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID <= doc.ID {
		t.Errorf("the second id made, %s, does not sort after the first, %s", second.ID, doc.ID)
	}
	own, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/019", "supplier": "ACME", "net": "5.00 PLN"}, krzysztof, fado.WithID("ERP-4711"))
	if err != nil || own.ID != "ERP-4711" {
		t.Fatalf("a document under the caller's id: %v", err)
	}
	_, err = db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/020", "supplier": "ACME", "net": "5.00 PLN"}, krzysztof, fado.WithID("ERP-4711"))
	check("a second invoice under the same id", err, "duplicate")

	// Nothing is overwritten.
	clock = clock.Add(time.Hour)
	if _, err := db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1200.00 PLN"}, krzysztof); err != nil {
		t.Fatal(err)
	}
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1.00 PLN"}, anna)
	check("Anna changes the net amount in INAPPROVAL", err, "forbidden, variable net")
	if now, err := db.Get(ctx, "Invoice", doc.ID, krzysztof); err != nil || now.Version != 2 {
		t.Errorf("after a refused commit the invoice is at version %d, %v; the chapter says it stays", now.Version, err)
	}
	clock = clock.Add(time.Hour)
	if _, err := db.Transition(ctx, "Invoice", doc.ID, "approve", nil, krzysztof); err != nil {
		t.Fatal(err)
	}
	history, err := db.History(ctx, "Invoice", doc.ID, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	versions := rows(marked(t, page, "the versions"))
	if len(history) != len(versions) {
		t.Fatalf("the history has %d versions, the chapter %d", len(history), len(versions))
	}
	for i, row := range versions {
		h := history[i]
		got := []string{fmt.Sprint(h.Version), fmt.Sprint(h.Values["net"]), fmt.Sprint(h.Values["gross"]), h.State}
		if shown := []string{row[0], row[2], row[3], row[4]}; !reflect.DeepEqual(got, shown) {
			t.Errorf("version %d is %v, and the chapter says %v", i+1, got, shown)
		}
		if h.ActorID != "krzysztof" || h.ValidFrom == 0 || (h.ValidTo == 0) != (i == len(history)-1) {
			t.Errorf("version %d: made by %q, current from %d until %d", i+1, h.ActorID, h.ValidFrom, h.ValidTo)
		}
	}
	if first, err := db.Version(ctx, "Invoice", doc.ID, 1, krzysztof); err != nil || fmt.Sprint(first.Values["net"]) != "1000.00 PLN" {
		t.Errorf("version 1 read by its number: %v", err)
	}

	// Deleting.
	check("Krzysztof deletes the invoice", db.Delete(ctx, "Invoice", doc.ID, krzysztof), "forbidden at /access/delete")
	note := notionClass(t, page)
	if _, err := db.DefineClass(ctx, note, setup); err != nil {
		t.Fatal(err)
	}
	n, err := db.Create(ctx, "Note", fado.Values{"text": "first"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Set(ctx, "Note", n.ID, fado.Values{"text": "second"}, krzysztof); err != nil {
		t.Fatal(err)
	}
	check("Krzysztof deletes the note", db.Delete(ctx, "Note", n.ID, krzysztof), "allowed")
	_, err = db.Get(ctx, "Note", n.ID, krzysztof)
	check("the note after the delete", err, "unknown_document")
	if kept, err := db.History(ctx, "Note", n.ID, krzysztof); err != nil || len(kept) != 2 || fmt.Sprint(kept[0].Values["text"]) != "first" {
		t.Errorf("the history after the delete: %d versions, %v; the chapter says both, as they were", len(kept), err)
	}
	events, err := db.Events(ctx, "Note", n.ID, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.ActorID != "krzysztof" || e.TS == 0 {
			t.Errorf("the event %s has no who or no when", e.Kind)
		}
	}
	if got := strings.Join(kinds, ", "); got != "created, set, deleted" {
		t.Errorf("the events after the delete are %s", got)
	}
	_, err = db.Create(ctx, "Note", fado.Values{"text": "again"}, krzysztof, fado.WithID(n.ID))
	check("a new note under the id of the deleted one", err, "duplicate")
	done, err := db.Create(ctx, "Note", fado.Values{"text": "kept"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Note", done.ID, "finish", nil, krzysztof); err != nil {
		t.Fatal(err)
	}
	check("Krzysztof deletes a note that is done", db.Delete(ctx, "Note", done.ID, krzysztof), "terminal")
	_, err = db.Set(ctx, "Note", done.ID, fado.Values{"text": "late"}, krzysztof)
	check("Krzysztof changes a note that is done", err, "terminal")
	_, err = db.Create(ctx, "Note", fado.Values{"text": "same id, another class"}, krzysztof, fado.WithID("ERP-4711"))
	check("a note under an id an invoice has", err, "allowed")

	// A class has versions too.
	open, err := db.Create(ctx, "Note", fado.Values{"text": "open"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(note, []byte(`"text": {"type": "text"}`), []byte(`"text": {"type": "text"}, "tag": {"type": "text"}`), 1)
	if next, err := db.DefineClass(ctx, changed, setup); err != nil || next.Version != 2 {
		t.Fatalf("the class defined again with a change: %v", err)
	}
	if still, err := db.Get(ctx, "Note", open.ID, krzysztof); err != nil || still.ClassVersion != 1 {
		t.Errorf("a new version of the class touched a document: %v", err)
	}
	if moved, err := db.Set(ctx, "Note", open.ID, fado.Values{"text": "changed"}, krzysztof); err != nil || moved.ClassVersion != 2 {
		t.Errorf("a document takes the new version at its next change: %v", err)
	}
	definitions, err := db.Definitions(ctx, "class", "Note")
	if err != nil || len(definitions) != 2 || definitions[0].By != "setup" || definitions[0].At == 0 {
		t.Errorf("the versions of the class, with who defined each and when: %v, %v", definitions, err)
	}

	// The classes of a database.
	if classes := db.Classes(); len(classes) == 0 || classes[0] != fado.SubstitutionClass {
		t.Errorf("the classes of a database are %v; the chapter says every database has Substitution", classes)
	}
	if got := finding(db, scratch("Substitution", `"a":{"type":"text"}`, oneState)); got != "reserved_name" {
		t.Errorf("a class of one's own under the engine's name: %s", got)
	}
	if _, err := db.DefineClass(ctx, []byte(scratch("Calculator", `"a":{"type":"number"},"twice":{"formula":"a * 2"}`, "")), setup); err != nil {
		t.Fatal(err)
	}
	_, err = db.Create(ctx, "Calculator", fado.Values{"a": 2}, krzysztof)
	check("a document of a class without states", err, "stateless")

	// What is refused.
	_, err = db.Create(ctx, "Invoyce", fado.Values{"number": "1"}, krzysztof)
	check("a class the database does not have", err, "unknown_class")
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"nett": "1.00 PLN"}, krzysztof)
	check("a variable the class does not have", err, "unknown_variable, variable nett")
	_, err = db.Get(ctx, "Invoice", "no-such-invoice", krzysztof)
	check("a document that is not there", err, "unknown_document")
	met.covers(t, "what is refused", marked(t, page, "what is refused"))
}

func TestNotion_VariablesAndFormulas(t *testing.T) {
	page := notion(t, "variables-and-formulas.md")
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	ctx := context.Background()
	class := notionClass(t, page)
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	krzysztof := person("krzysztof")
	met := seen{}

	// The story.
	offer, err := db.Create(ctx, "Offer", fado.Values{"customer": "ACME", "quantity": 3, "unit_price": "19.99 EUR"}, krzysztof)
	want(t, "1: creates an offer", err, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows(marked(t, page, "the values after step 1")) {
		got := fmt.Sprint(offer.Values[row[0]])
		switch {
		case offer.Values[row[0]] == nil:
			got = "blank"
		case got == "false":
			got = "FALSE"
		}
		if got != row[1] {
			t.Errorf("after step 1 %s is %s, and the chapter says %s", row[0], got, row[1])
		}
	}
	if len(offer.Values) != len(rows(marked(t, page, "the values after step 1"))) {
		t.Errorf("the offer has %d variables, and the table of the chapter another number", len(offer.Values))
	}
	set := func(step string, values fado.Values, answered string) *fado.Document {
		t.Helper()
		doc, err := db.Set(ctx, "Offer", offer.ID, values, krzysztof)
		want(t, step, err, met.note(answered))
		return doc
	}
	value := func(doc *fado.Document, variable, shown string) {
		t.Helper()
		if doc == nil {
			return
		}
		got := "blank"
		if doc.Values[variable] != nil {
			got = fmt.Sprint(doc.Values[variable])
		}
		if got != shown {
			t.Errorf("%s is %s, and the chapter says %s", variable, got, shown)
		}
	}
	doc := set("2: sets the discount to 0.15", fado.Values{"discount": "0.15"}, "allowed")
	value(doc, "net", "50.97 EUR")
	value(doc, "per_piece", "16.99 EUR")
	set("3: sets the quantity to 2000", fado.Values{"quantity": 2000}, "out_of_range, variable quantity")
	set("4: sets the quantity to 1.5", fado.Values{"quantity": 1.5}, "bad_value, variable quantity")
	set("5: sets the kind to rental", fado.Values{"kind": "rental"}, "not_in_values, variable kind")
	set("6: sets the unit price with no currency", fado.Values{"unit_price": "19.99"}, "bad_value, variable unit_price")
	set("7: sets the unit price to three places", fado.Values{"unit_price": "19.999 EUR"}, "bad_value, variable unit_price")
	set("8: sets net", fado.Values{"net": "1.00 EUR"}, "read_only, variable net")
	set("9: blanks the customer", fado.Values{"customer": nil}, "required, variable customer")
	doc = set("10: blanks the discount", fado.Values{"discount": nil}, "allowed")
	value(doc, "net", "59.97 EUR")
	value(doc, "discount", "blank")
	set("11: sets the quantity to 0", fado.Values{"quantity": 0}, "div_by_zero, variable per_piece")
	doc, err = db.Reset(ctx, "Offer", offer.ID, []string{"discount"}, krzysztof)
	want(t, "12: resets the discount", err, "allowed")
	value(doc, "discount", "0.00")

	// Inputs are checked on the way in: the rest of that table.
	set("text for a number", fado.Values{"quantity": "three"}, "bad_value, variable quantity")
	set("a day that does not exist", fado.Values{"issued": "2026-13-01"}, "bad_value, variable issued")
	set("money as a plain number", fado.Values{"unit_price": 19.99}, "bad_value, variable unit_price")
	doc = set("a decimal as a plain number", fado.Values{"discount": 0.1}, "allowed")
	value(doc, "discount", "0.10")

	// IFERROR catches the failure where one is expected.
	caught := bytes.Replace(class, []byte(`"ROUND(net / quantity, 2)"`), []byte(`"IFERROR(ROUND(net / quantity, 2), BLANK)"`), 1)
	if bytes.Equal(caught, class) {
		t.Fatal("the class of the chapter has no per_piece formula to replace")
	}
	other := notionDB(t, fado.Options{})
	if _, err := other.DefineClass(ctx, caught, setup); err != nil {
		t.Fatalf("the class with IFERROR is refused: %v", err)
	}
	nothing, err := other.Create(ctx, "Offer", fado.Values{"customer": "ACME", "quantity": 0, "unit_price": "19.99 EUR"}, krzysztof)
	want(t, "an offer of nothing, with IFERROR", err, "allowed")
	value(nothing, "per_piece", "blank")

	// Exact numbers and plain numbers, dates and text.
	numbers := scratch("Numbers",
		`"a":{"type":"money","scale":2},"b":{"type":"money","scale":2},
		 "r":{"type":"decimal","scale":2},"s":{"type":"decimal","scale":2},
		 "n":{"type":"number"},"m":{"type":"number"},"day":{"type":"date"},
		 "first":{"type":"int"},"next":{"type":"int","default":"first + 1"},
		 "sum":{"formula":"a + b"},"times":{"formula":"a * (1 + r)"},"third":{"formula":"a / 3"},
		 "exact":{"formula":"r + s = 0.3"},"plain":{"formula":"n + m = 0.3"},"plainsum":{"formula":"n + m"},
		 "month":{"formula":"ADDMONTHS(day, 1)"},"days":{"formula":"DAYS(ADDMONTHS(day, 1), day)"},
		 "same":{"formula":"\"abc\" = \"ABC\""},"upper":{"formula":"UPPER(\"abc\") = UPPER(\"ABC\")"}`, oneState)
	if _, err := db.DefineClass(ctx, []byte(numbers), setup); err != nil {
		t.Fatal(err)
	}
	n, err := db.Create(ctx, "Numbers", fado.Values{"a": "10.00 PLN", "b": "5.55 PLN", "r": "0.10", "s": "0.20", "n": 0.1, "m": 0.2, "day": "2027-01-31", "first": 1}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	for variable, shown := range map[string]string{
		"sum": "15.55 PLN", "times": "11.0000 PLN", "third": "3.333333 PLN",
		"exact": "true", "plain": "false", "plainsum": "0.30000000000000004",
		"month": "2027-02-28", "days": "28", "same": "false", "upper": "true", "next": "2",
	} {
		value(n, variable, shown)
	}
	_, err = db.Create(ctx, "Numbers", fado.Values{"a": "10.00 PLN", "b": "5.55 EUR"}, krzysztof)
	want(t, "two currencies in one sum", err, "currency_mismatch, variable sum")

	// A failure in a guard or in a line of access counts as FALSE.
	failing := `{"name":"Failing","variables":{"n":{"type":"number"}},
	  "states":{"initial":"a","list":["a","b"]},
	  "transitions":[{"name":"go","from":"a","to":"b","guard":"100 / n > 1"}],
	  "access":{"read":"TRUE","create":"TRUE","update":"100 / n > 1","delete":"FALSE","transition":{"go":"TRUE"}}}`
	if _, err := db.DefineClass(ctx, []byte(failing), setup); err != nil {
		t.Fatal(err)
	}
	f, err := db.Create(ctx, "Failing", fado.Values{"n": 0}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Transition(ctx, "Failing", f.ID, "go", nil, krzysztof)
	want(t, "a guard that divides by zero", err, "guard_false")
	_, err = db.Set(ctx, "Failing", f.ID, fado.Values{"n": 1}, krzysztof)
	want(t, "a line of access that divides by zero", err, "forbidden at /access/update")

	// What is checked when the class is defined.
	variables := func(variables string) string { return scratch("Checked", variables, oneState) }
	held(t, db, page, map[string][]string{
		"names a variable the class does not have":      {variables(`"a":{"type":"number"},"b":{"formula":"aa + 1"}`)},
		"adds a number to a date":                       {variables(`"a":{"type":"date"},"b":{"formula":"a + 1"}`)},
		"uses a number where a condition is needed":     {variables(`"a":{"type":"number"},"b":{"formula":"IF(a, 1, 2)"}`)},
		"multiplies money by money":                     {variables(`"a":{"type":"money"},"b":{"formula":"a * a"}`)},
		"gives an int where a plain number is expected": {variables(`"a":{"type":"date"},"i":{"type":"int"},"b":{"formula":"ADDDAYS(a, i)"}`)},
		"reads the clock or the caller in a computed variable": {
			variables(`"a":{"type":"date"},"b":{"formula":"DAYS(TODAY(), a)"}`),
			variables(`"a":{"type":"datetime"},"b":{"formula":"a < NOW()"}`),
			variables(`"a":{"type":"text"},"b":{"formula":"a = USER()"}`),
		},
		"is of another type than the variable says": {variables(`"a":{"type":"number"},"b":{"type":"text","formula":"a * 2"}`)},
		"reads a variable that reads it back":       {variables(`"a":{"formula":"b + 1"},"b":{"formula":"a + 1"}`)},
	})
	for what, c := range map[string][2]string{
		"a type that agrees with the formula":   {`"a":{"type":"number"},"b":{"type":"number","formula":"a * 2"}`, "accepted"},
		"a name the engine keeps":               {`"state":{"type":"text"}`, "reserved_name"},
		"a default that reads another variable": {`"a":{"type":"int"},"b":{"type":"int","default":"a + 1"}`, "accepted"},
	} {
		if got := finding(db, variables(c[0])); got != c[1] {
			t.Errorf("%s: the engine finds %s, and the chapter says %s", what, got, c[1])
		}
	}
}

func TestNotion_StatesAndTransitions(t *testing.T) {
	page := notion(t, "states-and-transitions.md")
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	ctx := context.Background()
	class := notionClass(t, page)
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	ola, marek := person("ola"), person("marek")
	met := seen{}

	// The chart draws the class, and nothing else.
	var def struct {
		States struct {
			Initial string   `json:"initial"`
			List    []string `json:"list"`
			Final   []string `json:"final"`
		} `json:"states"`
		Transitions []struct {
			Name string          `json:"name"`
			From json.RawMessage `json:"from"`
			To   string          `json:"to"`
		} `json:"transitions"`
	}
	if err := json.Unmarshal(class, &def); err != nil {
		t.Fatal(err)
	}
	var declared []string
	declared = append(declared, "[*] --> "+def.States.Initial)
	for _, tr := range def.Transitions {
		var from []string
		if json.Unmarshal(tr.From, &from) != nil {
			var one string
			if err := json.Unmarshal(tr.From, &one); err != nil {
				t.Fatal(err)
			}
			from = []string{one}
		}
		for _, state := range from {
			declared = append(declared, state+" --> "+tr.To+": "+tr.Name)
		}
	}
	for _, state := range def.States.Final {
		declared = append(declared, state+" --> [*]")
	}
	chart := regexp.MustCompile("(?s)```mermaid\r?\n(.*?)\r?\n```").FindSubmatch(page)
	if chart == nil {
		t.Fatal("the chapter has no chart")
	}
	var drawn []string
	for _, line := range strings.Split(string(chart[1]), "\n") {
		if line = strings.TrimSpace(line); strings.Contains(line, "-->") {
			drawn = append(drawn, line)
		}
	}
	sort.Strings(declared)
	sort.Strings(drawn)
	if !reflect.DeepEqual(declared, drawn) {
		t.Errorf("the chart draws\n%s\nand the class declares\n%s", strings.Join(drawn, "\n"), strings.Join(declared, "\n"))
	}

	// The story.
	leave, err := db.Create(ctx, "Leave", fado.Values{"manager": "marek", "days": 45}, ola)
	want(t, "1: Ola creates a request for 45 days", err, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	state := func(step, shown string) *fado.Document {
		t.Helper()
		doc, err := db.Get(ctx, "Leave", leave.ID, ola)
		if err != nil {
			t.Fatal(err)
		}
		if doc.State != shown {
			t.Errorf("%s: the request is in %s, and the chapter says %s", step, doc.State, shown)
		}
		return doc
	}
	move := func(step string, as fado.Option, name string, values fado.Values, answered string) {
		t.Helper()
		clock = clock.Add(time.Hour)
		_, err := db.Transition(ctx, "Leave", leave.ID, name, values, as)
		want(t, step, err, met.note(answered))
	}
	state("1", "draft")
	move("2: Ola requests it", ola, "request", nil, "guard_false")

	// A preview runs the move and writes nothing.
	preview, err := db.Preview(ctx, "Leave", leave.ID, fado.Values{"days": 10}, "request", ola)
	if err != nil || preview.Document == nil || preview.Document.State != "requested" || len(preview.Errors) != 0 {
		t.Errorf("a preview of the request with 10 days: %v", err)
	}
	if doc := state("after the preview", "draft"); doc.Version != 1 {
		t.Errorf("a preview wrote version %d", doc.Version)
	}

	move("3: Ola requests it, setting the days to 10", ola, "request", fado.Values{"days": 10}, "allowed")
	if doc := state("3", "requested"); doc.Version != 2 {
		t.Errorf("a transition with values made %d versions; the chapter says it is one commit", doc.Version-1)
	}
	move("4: Ola approves it", ola, "approve", nil, "forbidden at /access/transition/approve")
	move("5: Marek rejects it, with no comment", marek, "reject", nil, "guard_false")

	// What is open now, for each of the two.
	for column, as := range map[int]fado.Option{2: marek, 3: ola} {
		open, err := db.Transitions(ctx, "Leave", leave.ID, as)
		if err != nil {
			t.Fatal(err)
		}
		table := rows(marked(t, page, "what is open in requested"))
		if len(open) != len(table) {
			t.Fatalf("%d transitions leave requested, and the chapter lists %d", len(open), len(table))
		}
		for i, row := range table {
			got := "yes"
			if !open[i].Enabled {
				got = "no: " + open[i].Code
			}
			if open[i].Name != row[0] || open[i].To != row[1] || got != row[column] {
				t.Errorf("open in requested: %s to %s, %s; the chapter says %s to %s, %s", open[i].Name, open[i].To, got, row[0], row[1], row[column])
			}
		}
	}

	before := state("before the reminder", "requested")
	move("6: Ola reminds", ola, "remind", nil, "allowed")
	after := state("6", "requested")
	if after.Version != before.Version+1 || after.StateEnteredAt <= before.StateEnteredAt || after.StateEnteredFrom != "requested" {
		t.Errorf("a move into the same state: version %d after %d, entered at %d after %d, from %q",
			after.Version, before.Version, after.StateEnteredAt, before.StateEnteredAt, after.StateEnteredFrom)
	}
	move("7: Marek returns it", marek, "return", nil, "allowed")
	state("7", "draft")
	clock = clock.Add(time.Hour)
	_, err = db.Set(ctx, "Leave", leave.ID, fado.Values{"days": 12}, ola)
	want(t, "8: Ola sets the days to 12", err, "allowed")
	move("9: Ola requests it again", ola, "request", nil, "allowed")
	move("10: Marek approves it, setting the days to 300", marek, "approve", fado.Values{"days": 300}, "forbidden, variable days")
	move("11: Marek approves it", marek, "approve", nil, "allowed")
	state("11", "approved")
	_, err = db.Set(ctx, "Leave", leave.ID, fado.Values{"comment": "thank you"}, ola)
	want(t, "12: Ola writes a comment", err, met.note("terminal"))
	move("13: Marek returns it", marek, "return", nil, "wrong_state")
	move("14: Ola cancels it", ola, "cancel", nil, "allowed")
	state("14", "cancelled")
	move("15: Ola cancels it again", ola, "cancel", nil, "wrong_state")
	move("a transition the class does not have", ola, "aprove", nil, "unknown_transition")

	// What the log keeps.
	events, err := db.Events(ctx, "Leave", leave.ID, ola)
	if err != nil {
		t.Fatal(err)
	}
	log := rows(marked(t, page, "the events"))
	if len(events) != len(log) {
		t.Fatalf("the log has %d events, and the chapter %d", len(events), len(log))
	}
	for i, row := range log {
		e := events[i]
		if got := []string{e.Kind, e.Name, e.FromState, e.ToState, e.ActorID}; !reflect.DeepEqual(got, row) {
			t.Errorf("event %d is %v, and the chapter says %v", i+1, got, row)
		}
	}

	// A rejection carries its comment, and Marek changes nothing else.
	second, err := db.Create(ctx, "Leave", fado.Values{"manager": "marek", "days": 5}, ola)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Leave", second.ID, "request", nil, ola); err != nil {
		t.Fatal(err)
	}
	_, err = db.Set(ctx, "Leave", second.ID, fado.Values{"comment": "busy week"}, marek)
	want(t, "Marek writes a comment without a move", err, "forbidden at /access/update")
	rejected, err := db.Transition(ctx, "Leave", second.ID, "reject", fado.Values{"comment": "busy week"}, marek)
	want(t, "Marek rejects with a comment", err, "allowed")
	if err == nil && rejected.State != "rejected" {
		t.Errorf("the rejected request is in %s", rejected.State)
	}

	// Without the rights block Marek could set the days while approving.
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(class, &whole); err != nil {
		t.Fatal(err)
	}
	if whole["rights"] == nil {
		t.Fatal("the class of the chapter has no rights block")
	}
	delete(whole, "rights")
	open, err := json.Marshal(whole)
	if err != nil {
		t.Fatal(err)
	}
	other := notionDB(t, fado.Options{})
	if _, err := other.DefineClass(ctx, open, setup); err != nil {
		t.Fatal(err)
	}
	third, err := other.Create(ctx, "Leave", fado.Values{"manager": "marek", "days": 5}, ola)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Transition(ctx, "Leave", third.ID, "request", nil, ola); err != nil {
		t.Fatal(err)
	}
	approved, err := other.Transition(ctx, "Leave", third.ID, "approve", fado.Values{"days": 300}, marek)
	if err != nil || fmt.Sprint(approved.Values["days"]) != "300" {
		t.Errorf("without rights, Marek approving with 300 days: %v; the chapter says he could", err)
	}

	// A move into the same state runs the rules of leaving and entering.
	counting := `{"name":"Counting","variables":{"entered":{"type":"int","default":"0"},"left":{"type":"int","default":"0"}},
	  "states":{"initial":"draft","list":["draft","asked"]},
	  "transitions":[{"name":"ask","from":"draft","to":"asked"},{"name":"remind","from":"asked","to":"asked"}],
	  "rules":[{"on":"enter","state":"asked","do":[{"action":"set","values":{"entered":"entered + 1"}}]},
	           {"on":"exit","state":"asked","do":[{"action":"set","values":{"left":"left + 1"}}]}],
	  "access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`
	if _, err := db.DefineClass(ctx, []byte(counting), setup); err != nil {
		t.Fatal(err)
	}
	c, err := db.Create(ctx, "Counting", nil, ola)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Counting", c.ID, "ask", nil, ola); err != nil {
		t.Fatal(err)
	}
	reminded, err := db.Transition(ctx, "Counting", c.ID, "remind", nil, ola)
	if err != nil || fmt.Sprint(reminded.Values["entered"]) != "2" || fmt.Sprint(reminded.Values["left"]) != "1" {
		t.Errorf("a move into the same state: entered %v times, left %v times, %v", reminded.Values["entered"], reminded.Values["left"], err)
	}

	// What is checked when the class is defined, and what is not.
	chart2 := func(states, transitions string) string {
		return scratch("Checked", `"a":{"type":"number"}`, `"states":`+states+`,"transitions":`+transitions)
	}
	held(t, db, page, map[string][]string{
		"names a state in a transition, or as initial, that is not in list": {
			chart2(`{"initial":"a","list":["a","b"]}`, `[{"name":"go","from":"a","to":"z"}]`),
			chart2(`{"initial":"x","list":["a","b"]}`, `[{"name":"go","from":"a","to":"b"}]`),
		},
		"uses the name of a transition twice":               {chart2(`{"initial":"a","list":["a","b","c"]}`, `[{"name":"go","from":"a","to":"b"},{"name":"go","from":"b","to":"c"}]`)},
		"has a guard that is not a condition":               {chart2(`{"initial":"a","list":["a","b"]}`, `[{"name":"go","from":"a","to":"b","guard":"a + 1"}]`)},
		"leads from a final state to one that is not final": {chart2(`{"initial":"a","list":["a","b"],"final":["b"]}`, `[{"name":"go","from":"a","to":"b"},{"name":"back","from":"b","to":"a"}]`)},
	})
	// Two things the engine does not check.
	for what, definition := range map[string]string{
		"a state no transition reaches":                     chart2(`{"initial":"a","list":["a","b","c"]}`, `[{"name":"go","from":"a","to":"b"}]`),
		"a state that is not final and that nothing leaves": chart2(`{"initial":"a","list":["a","b"]}`, `[{"name":"go","from":"a","to":"b"}]`),
	} {
		if got := finding(db, definition); got != "accepted" {
			t.Errorf("%s: the engine finds %s, and the chapter says it is accepted without a remark", what, got)
		}
	}
	met.covers(t, "what is refused", marked(t, page, "what is refused"))
}

func TestNotion_RulesAndActions(t *testing.T) {
	page := notion(t, "rules-and-actions.md")
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	ctx := context.Background()
	classes := notionClasses(t, page)
	if len(classes) != 2 {
		t.Fatalf("the chapter shows %d classes; the test plays an order and a purchase", len(classes))
	}
	for _, class := range classes {
		if _, err := db.DefineClass(ctx, class, setup); err != nil {
			t.Fatal(err)
		}
	}
	ola, iza, marek, piotr := person("ola"), person("iza", "controllers"), person("marek", "managers"), person("piotr")

	// The story.
	purchase, err := db.Create(ctx, "Purchase", fado.Values{"item": "laptop", "amount": "8000.00 PLN", "approver": "marek"}, ola)
	want(t, "1: Ola creates a purchase", err, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	set := func(step string, as fado.Option, values fado.Values, answered string) *fado.Document {
		t.Helper()
		clock = clock.Add(time.Hour)
		doc, err := db.Set(ctx, "Purchase", purchase.ID, values, as)
		want(t, step, err, answered)
		return doc
	}
	approve := func(step string, answered string) *fado.Document {
		t.Helper()
		clock = clock.Add(time.Hour)
		doc, err := db.Transition(ctx, "Purchase", purchase.ID, "approve", nil, marek)
		want(t, step, err, answered)
		return doc
	}
	set("2: Ola ticks checked herself", ola, fado.Values{"checked": true}, "forbidden, variable checked")
	set("3: Iza ticks checked", iza, fado.Values{"checked": true}, "allowed")
	changed := set("4: Ola changes the amount", ola, fado.Values{"amount": "12000.00 PLN"}, "allowed")
	if changed != nil && (changed.Values["checked"] != false || changed.Version != 3) {
		t.Errorf("4: checked is %v at version %d; the chapter says rule 1 takes the check back in the same version", changed.Values["checked"], changed.Version)
	}
	approve("5: Marek approves", "guard_false")
	set("by hand: Marek writes approved_by", marek, fado.Values{"approved_by": "marek"}, "forbidden at /access/update")
	set("6: Iza ticks checked again", iza, fado.Values{"checked": true}, "allowed")

	// Looking before: the preview lists rules 2, 3 and 4, the last queued.
	preview, err := db.Preview(ctx, "Purchase", purchase.ID, nil, "approve", marek)
	if err != nil {
		t.Fatal(err)
	}
	var would []string
	for _, rule := range preview.Rules {
		if !rule.Ran {
			continue
		}
		for _, action := range rule.Actions {
			what := rule.Path + " " + action.Action
			if action.Queued {
				what += " queued"
			}
			would = append(would, what)
		}
	}
	if got := strings.Join(would, ", "); got != "/rules/1 set, /rules/2 emit, /rules/3 create queued" {
		t.Errorf("the preview of the approval lists %s", got)
	}
	if queue, err := db.Outbox(ctx, fado.OutboxFilter{}, setup); err != nil || len(queue) != 0 {
		t.Errorf("a preview queued %d actions, %v", len(queue), err)
	}

	approved := approve("7: Marek approves", "allowed")
	if approved != nil && (approved.Values["approved_by"] != "marek" || approved.Values["approved_at"] == nil || approved.Version != 5) {
		t.Errorf("7: approved by %v at %v, version %d", approved.Values["approved_by"], approved.Values["approved_at"], approved.Version)
	}
	orders := func() []*fado.Document {
		t.Helper()
		found, err := db.Query(ctx, fado.Filter{Class: "Order"}, ola)
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	queue, err := db.Outbox(ctx, fado.OutboxFilter{DocID: purchase.ID}, setup)
	if err != nil || len(queue) != 1 || queue[0].Status != "pending" || queue[0].Action != "create" || queue[0].PrincipalID != "marek" || queue[0].Rule != "/rules/3/do/0" {
		t.Fatalf("after the approval the queue holds %v, %v", queue, err)
	}
	if len(orders()) != 0 {
		t.Error("the order exists before the engine ran the queue")
	}
	if ran, err := db.Drain(ctx, setup); err != nil || ran != 1 {
		t.Errorf("8: the engine ran %d actions, %v", ran, err)
	}
	made := orders()
	if len(made) != 1 || made[0].CreatedBy != "marek" || fmt.Sprint(made[0].Values["amount"]) != "12000.00 PLN" || made[0].Values["purchase"] != purchase.ID {
		t.Fatalf("8: the orders are %v", made)
	}
	if ran, err := db.Drain(ctx, setup); err != nil || ran != 0 {
		t.Errorf("9: the engine ran %d actions the second time, %v", ran, err)
	}
	if history, err := db.History(ctx, "Purchase", purchase.ID, ola); err != nil || len(history) != 5 {
		t.Errorf("the purchase has %d versions, %v; the chapter says five", len(history), err)
	}

	// What the log keeps.
	events, err := db.Events(ctx, "Purchase", purchase.ID, ola)
	if err != nil {
		t.Fatal(err)
	}
	log := rows(marked(t, page, "the events"))
	if len(events) != len(log) {
		t.Fatalf("the log has %d events, and the chapter %d", len(events), len(log))
	}
	for i, row := range log {
		e := events[i]
		if got := []string{e.Kind, e.Name, e.FromState, e.ToState, e.ActorID}; !reflect.DeepEqual(got, row) {
			t.Errorf("event %d is %v, and the chapter says %v", i+1, got, row)
		}
		if (e.Kind == "custom" || e.Kind == "action_run") && e.CauseEventID != events[4].ID {
			t.Errorf("the event %s does not name the approval as its cause", e.Kind)
		}
	}

	// An action that is refused: Piotr approves, and may not create orders.
	second, err := db.Create(ctx, "Purchase", fado.Values{"item": "desk", "amount": "900.00 PLN", "approver": "piotr"}, ola)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Set(ctx, "Purchase", second.ID, fado.Values{"checked": true}, iza); err != nil {
		t.Fatal(err)
	}
	_, err = db.Transition(ctx, "Purchase", second.ID, "approve", nil, piotr)
	want(t, "Piotr approves the purchase", err, "allowed")
	if ran, err := db.Drain(ctx, setup); err != nil || ran != 1 {
		t.Errorf("the engine ran %d actions, %v", ran, err)
	}
	if len(orders()) != 1 {
		t.Errorf("there are %d orders; the chapter says the second is refused", len(orders()))
	}
	queue, err = db.Outbox(ctx, fado.OutboxFilter{DocID: second.ID}, setup)
	if err != nil || len(queue) != 1 || queue[0].Status != "dead" || queue[0].ErrorCode != "forbidden" || queue[0].PrincipalID != "piotr" {
		t.Errorf("the queue afterwards: %v, %v", queue, err)
	}
	events, err = db.Events(ctx, "Purchase", second.ID, ola)
	if err != nil || len(events) == 0 {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Kind != "action_failed" || last.Name != "create" || last.Code != "forbidden" {
		t.Errorf("the last event of the purchase is %s %s %s", last.Kind, last.Name, last.Code)
	}
	if ran, err := db.Drain(ctx, setup); err != nil || ran != 0 {
		t.Errorf("a dead action was tried again by itself: %d, %v", ran, err)
	}
	_, err = db.Outbox(ctx, fado.OutboxFilter{}, ola)
	want(t, "Ola lists the queue", err, "forbidden at /system/administrators")

	// When a rule runs: a class whose rules name the trigger that set
	// them off, in an order the triggers do not have.
	triggers := `{"name":"Triggers","variables":{"a":{"type":"number"}},
	  "states":{"initial":"draft","list":["draft","review"]},
	  "transitions":[{"name":"submit","from":"draft","to":"review"}],
	  "rules":[
	    {"on":"enter","state":"review","do":[{"action":"emit","event":"enter"}]},
	    {"on":"delete","do":[{"action":"emit","event":"delete"}]},
	    {"on":"transition","name":"submit","do":[{"action":"emit","event":"transition"}]},
	    {"on":"exit","state":"draft","do":[{"action":"emit","event":"exit"}]},
	    {"on":"values_change","variables":["a"],"do":[{"action":"emit","event":"values_change"}]},
	    {"on":"enter","state":"draft","do":[{"action":"emit","event":"enter"}]},
	    {"on":"create","do":[{"action":"emit","event":"create"}]}
	  ],
	  "access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`
	if _, err := db.DefineClass(ctx, []byte(triggers), setup); err != nil {
		t.Fatal(err)
	}
	emitted := func(id string, from int) (string, int) {
		t.Helper()
		events, err := db.Events(ctx, "Triggers", id, ola)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range events[from:] {
			if e.Kind == "custom" {
				names = append(names, e.Name)
			}
		}
		return strings.Join(names, ", "), len(events)
	}
	word := regexp.MustCompile("`([a-z_]+)`")
	order := map[string]string{}
	for _, row := range rows(strings.ReplaceAll(marked(t, page, "the order of the triggers"), "`", "'")) {
		var names []string
		for _, m := range word.FindAllStringSubmatch(strings.ReplaceAll(row[1], "'", "`"), -1) {
			names = append(names, m[1])
		}
		order[row[0]] = strings.Join(names, ", ")
	}
	one, err := db.Create(ctx, "Triggers", fado.Values{"a": 1}, ola)
	if err != nil {
		t.Fatal(err)
	}
	got, seenSoFar := emitted(one.ID, 0)
	if got != order["a create"] {
		t.Errorf("a create sets off %s, and the chapter says %s", got, order["a create"])
	}
	if _, err := db.Set(ctx, "Triggers", one.ID, fado.Values{"a": 2}, ola); err != nil {
		t.Fatal(err)
	}
	if got, seenSoFar = emitted(one.ID, seenSoFar); got != order["a change of values"] {
		t.Errorf("a change of values sets off %s, and the chapter says %s", got, order["a change of values"])
	}
	if _, err := db.Transition(ctx, "Triggers", one.ID, "submit", fado.Values{"a": 3}, ola); err != nil {
		t.Fatal(err)
	}
	if got, seenSoFar = emitted(one.ID, seenSoFar); got != order["a transition"] {
		t.Errorf("a transition sets off %s, and the chapter says %s", got, order["a transition"])
	}
	if err := db.Delete(ctx, "Triggers", one.ID, ola); err != nil {
		t.Fatal(err)
	}
	if got, _ = emitted(one.ID, seenSoFar); got != order["a delete"] {
		t.Errorf("a delete sets off %s, and the chapter says %s", got, order["a delete"])
	}

	// Inside the commit: no rule sets another off, a failing action
	// refuses the commit with its place, a failing condition is FALSE.
	inside := `{"name":"Inside","variables":{"a":{"type":"number"},"b":{"type":"number"},"c":{"type":"number"},
	    "m":{"type":"number"},"r":{"type":"number"},"q":{"type":"number"},"small":{"type":"int","max":5},"seen":{"type":"bool","default":"FALSE"}},
	  "states":{"initial":"draft","list":["draft"]},
	  "rules":[
	    {"on":"values_change","variables":["a"],"do":[{"action":"set","values":{"b":"a + 1"}}]},
	    {"on":"values_change","variables":["b"],"do":[{"action":"set","values":{"c":"b + 1"}}]},
	    {"on":"values_change","variables":["m"],"do":[{"action":"set","values":{"r":"100 / m"}}]},
	    {"on":"values_change","variables":["q"],"do":[{"action":"set","values":{"small":"9"}}]},
	    {"on":"values_change","variables":["c"],"if":"100 / c > 1","do":[{"action":"set","values":{"seen":"TRUE"}}]}
	  ],
	  "access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`
	if _, err := db.DefineClass(ctx, []byte(inside), setup); err != nil {
		t.Fatal(err)
	}
	in, err := db.Create(ctx, "Inside", nil, ola)
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.Set(ctx, "Inside", in.ID, fado.Values{"a": 1}, ola)
	if err != nil || fmt.Sprint(after.Values["b"]) != "2" || after.Values["c"] != nil {
		t.Errorf("a rule's set ran another rule: b %v, c %v, %v", after.Values["b"], after.Values["c"], err)
	}
	place := func(step string, values fado.Values, code, path string) {
		t.Helper()
		_, err := db.Set(ctx, "Inside", in.ID, values, ola)
		var refusal *fado.Error
		if !errors.As(err, &refusal) || refusal.Code != code || refusal.Path != path {
			t.Errorf("%s: %v; the chapter says the commit is refused with %s and the place %s", step, err, code, path)
		}
	}
	place("an action that cannot work out its value", fado.Values{"m": 0}, "div_by_zero", "/rules/2/do/0")
	place("an action that writes a value out of range", fado.Values{"q": 1}, "out_of_range", "/rules/3/do/0")
	after, err = db.Set(ctx, "Inside", in.ID, fado.Values{"c": 0}, ola)
	if err != nil || after.Values["seen"] != false {
		t.Errorf("a rule whose condition cannot be worked out: seen %v, %v; the chapter says it does not run", after.Values["seen"], err)
	}
	after, err = db.Set(ctx, "Inside", in.ID, fado.Values{"c": 50}, ola)
	if err != nil || after.Values["seen"] != true {
		t.Errorf("a rule whose condition is TRUE: seen %v, %v", after.Values["seen"], err)
	}

	// What is checked when the class is defined.
	chart := `"states":{"initial":"a","list":["a","b","c","d"],"final":["c","d"]},` +
		`"transitions":[{"name":"go","from":"a","to":"b"},{"name":"end","from":"b","to":"c"},{"name":"over","from":"c","to":"d"}]`
	rule := func(rules string) string {
		return scratch("Checked", `"n":{"type":"number"},"twice":{"formula":"n * 2"}`, chart+`,"rules":`+rules)
	}
	held(t, db, page, map[string][]string{
		"names a state the class does not have":                    {rule(`[{"on":"enter","state":"zz","do":[{"action":"emit","event":"x"}]}]`)},
		"names a transition the class does not have":               {rule(`[{"on":"transition","name":"zz","do":[{"action":"emit","event":"x"}]}]`)},
		"sets a variable the class does not have":                  {rule(`[{"on":"create","do":[{"action":"set","values":{"zz":"1"}}]}]`)},
		"sets a computed variable":                                 {rule(`[{"on":"create","do":[{"action":"set","values":{"twice":"1"}}]}]`)},
		"sets a value of another type than the variable":           {rule(`[{"on":"create","do":[{"action":"set","values":{"n":"\"x\""}}]}]`)},
		"is on values_change and names no variables":               {rule(`[{"on":"values_change","do":[{"action":"emit","event":"x"}]}]`)},
		"has an if that is not a condition":                        {rule(`[{"on":"create","if":"n + 1","do":[{"action":"emit","event":"x"}]}]`)},
		"names an action the database does not have":               {rule(`[{"on":"create","do":[{"action":"mail.send"}]}]`)},
		"creates a document of a class the database does not have": {rule(`[{"on":"create","do":[{"action":"create","class":"Nope","values":{}}]}]`)},
		"changes a document that is already finished": {
			rule(`[{"on":"exit","state":"c","do":[{"action":"set","values":{"n":"1"}}]}]`),
			rule(`[{"on":"enter","state":"d","do":[{"action":"set","values":{"n":"1"}}]}]`),
		},
	})
	if got := finding(db, rule(`[{"on":"enter","state":"d","do":[{"action":"emit","event":"x"}]}]`)); got != "accepted" {
		t.Errorf("an emit on entering a state a final one leads to: %s; the chapter says it may run there", got)
	}
	stateless := scratch("Calculator", `"n":{"type":"number"}`, `"rules":[{"on":"create","do":[{"action":"emit","event":"x"}]}]`)
	if got := finding(db, stateless); got != "not_allowed" {
		t.Errorf("rules in a class without states: %s", got)
	}
}

func TestNotion_WhoMay(t *testing.T) {
	page := notion(t, "who-may.md")
	db := notionDB(t, fado.Options{})
	ctx := context.Background()
	class := notionClass(t, page)
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	ola, krzysztof, anna, piotr := person("ola"), person("krzysztof"), person("anna", "ACCOUNTING"), person("piotr")
	bot := fado.As(fado.Principal{ID: "ledger-bot", Kind: fado.ActorAgent, Groups: []string{"ACCOUNTING"}})
	celina := fado.As(fado.Principal{ID: "celina", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}})
	met := seen{}
	check := func(step string, err error, answered string) {
		t.Helper()
		want(t, step, err, met.note(answered))
	}
	values := fado.Values{"number": "FV/2026/11/021", "supplier": "ACME", "net": "100.00 PLN", "approver": "krzysztof"}

	// The story.
	_, err := db.Create(ctx, "Invoice", values)
	check("1: a call that names nobody creates an invoice", err, "forbidden at /access/create")
	invoice, err := db.Create(ctx, "Invoice", values, ola)
	check("2: Ola creates the invoice", err, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	set := func(step string, as fado.Option, values fado.Values, answered string, more ...fado.Option) {
		t.Helper()
		_, err := db.Set(ctx, "Invoice", invoice.ID, values, append([]fado.Option{as}, more...)...)
		check(step, err, answered)
	}
	move := func(step string, as fado.Option, name, answered string) {
		t.Helper()
		_, err := db.Transition(ctx, "Invoice", invoice.ID, name, nil, as)
		check(step, err, answered)
	}
	_, err = db.Get(ctx, "Invoice", invoice.ID, piotr)
	check("3: Piotr reads it", err, "unknown_document")
	// He is told the same as for an id that was never used.
	_, never := db.Get(ctx, "Invoice", "no-such-invoice", piotr)
	if err == nil || never == nil || strings.ReplaceAll(err.Error(), invoice.ID, "X") != strings.ReplaceAll(never.Error(), "no-such-invoice", "X") {
		t.Errorf("an invoice Piotr may not see is answered %q, one that does not exist %q", err, never)
	}
	for who, n := range map[string]int64{"piotr": 0, "krzysztof": 1} {
		if got, err := db.Count(ctx, fado.Filter{Class: "Invoice"}, person(who)); err != nil || got != n {
			t.Errorf("%s counts %d invoices, %v; the chapter says %d", who, got, err, n)
		}
	}
	set("4: Anna changes the net amount", anna, fado.Values{"net": "120.00 PLN"}, "forbidden, variable net")
	set("5: Krzysztof writes an audit note", krzysztof, fado.Values{"audit_note": "seen"}, "forbidden, variable audit_note")
	set("6: Ola changes the net amount", ola, fado.Values{"net": "120.00 PLN"}, "allowed")
	// Who edits in INAPPROVAL: the rest of that table.
	set("INAPPROVAL: Krzysztof changes the supplier", krzysztof, fado.Values{"supplier": "ACME Ltd"}, "allowed")
	set("INAPPROVAL: Ola writes an audit note", ola, fado.Values{"audit_note": "seen"}, "forbidden, variable audit_note")
	set("INAPPROVAL: Anna writes an audit note", anna, fado.Values{"audit_note": "the bank account is new"}, "allowed")
	if doc, err := db.Get(ctx, "Invoice", invoice.ID, krzysztof); err != nil {
		t.Fatal(err)
	} else if _, there := doc.Values["audit_note"]; there {
		t.Error("in INAPPROVAL Krzysztof sees the audit note; the read right under * has to be found past the state's own lines, which give only write")
	}
	move("7: Ola approves", ola, "approve", "forbidden at /access/transition/approve")
	move("8: Krzysztof approves", krzysztof, "approve", "allowed")
	set("9: Krzysztof changes the net amount", krzysztof, fado.Values{"net": "1.00 PLN"}, "forbidden, variable net")
	set("10: ledger-bot sets the account and writes an audit note", bot, fado.Values{"account": "401", "audit_note": "checked the bank account"}, "allowed")
	set("INACCOUNTING: Anna changes the net amount", anna, fado.Values{"net": "121.00 PLN"}, "allowed")
	seenBy := func(as fado.Option) bool {
		t.Helper()
		doc, err := db.Get(ctx, "Invoice", invoice.ID, as)
		if err != nil {
			t.Fatal(err)
		}
		_, there := doc.Values["audit_note"]
		if len(doc.Values) != 5 && !there || len(doc.Values) != 6 && there {
			t.Errorf("the invoice has %d values", len(doc.Values))
		}
		return there
	}
	if seenBy(krzysztof) || !seenBy(anna) || !seenBy(celina) {
		t.Error("11: the audit note is seen by accounting and by a content administrator, and not by Krzysztof")
	}
	move("12: ledger-bot books", bot, "book", "forbidden at /access/transition/book")
	open, err := db.Transitions(ctx, "Invoice", invoice.ID, celina)
	if err != nil || len(open) != 1 || !open[0].Enabled || open[0].Override != "/access/transition/book" {
		t.Errorf("the open transitions for Celina: %v, %v; the chapter says the list names the rule she would get past", open, err)
	}
	set("13: Celina corrects the supplier, giving no reason", celina, fado.Values{"supplier": "ACME Sp. z o.o."}, "reason_required at /access/read")
	set("14: Celina corrects it, with the reason", celina, fado.Values{"supplier": "ACME Sp. z o.o."}, "allowed", fado.Reason("the supplier was renamed, ticket 4711"))
	move("15: Anna books", anna, "book", "allowed")
	set("16: Celina changes the booked invoice", celina, fado.Values{"supplier": "x"}, "terminal", fado.Reason("a try"))

	// The log: what the bot was, and what Celina got past and why.
	events, err := db.Events(ctx, "Invoice", invoice.ID, anna)
	if err != nil {
		t.Fatal(err)
	}
	past := rows(marked(t, page, "the event of step 14"))[0]
	var agent, override bool
	for _, e := range events {
		if e.ActorID == "ledger-bot" && e.ActorKind == "agent" {
			agent = true
		}
		if e.Override != "" {
			override = true
			if got := []string{e.Kind, e.ActorID, e.Override, e.Reason}; !reflect.DeepEqual(got, past) {
				t.Errorf("the event of step 14 is %q, and the chapter says %q", got, past)
			}
		}
	}
	if !agent || !override {
		t.Errorf("the log says an agent made step 10: %v; it keeps the act past a rule: %v", agent, override)
	}

	// How a right is found: with audit_note closed under * alone, the
	// line of INAPPROVAL for every variable is found first.
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(class, &whole); err != nil {
		t.Fatal(err)
	}
	whole["rights"] = json.RawMessage(`{
	  "INAPPROVAL":   {"*": {"write": "CREATEDBY() = USER() OR approver = USER()"}},
	  "INACCOUNTING": {"*": {"write": ["ACCOUNTING"]}},
	  "*":            {"audit_note": {"read": ["ACCOUNTING"], "write": ["ACCOUNTING"]}}}`)
	trap, err := json.Marshal(whole)
	if err != nil {
		t.Fatal(err)
	}
	other := notionDB(t, fado.Options{})
	if _, err := other.DefineClass(ctx, trap, setup); err != nil {
		t.Fatal(err)
	}
	second, err := other.Create(ctx, "Invoice", values, ola)
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.Set(ctx, "Invoice", second.ID, fado.Values{"audit_note": "written by the approver"}, krzysztof)
	want(t, "audit_note closed under * alone: Krzysztof writes it in INAPPROVAL", err, "allowed")

	// A call that names nobody gets through where a line says TRUE.
	if _, err := db.DefineClass(ctx, []byte(scratch("Open", `"a":{"type":"text"}`, oneState)), setup); err != nil {
		t.Fatal(err)
	}
	anonymous, err := db.Create(ctx, "Open", fado.Values{"a": "x"})
	want(t, "a call that names nobody, where the class says TRUE", err, "allowed")
	if err == nil {
		_, err = db.Get(ctx, "Open", anonymous.ID)
		want(t, "a call that names nobody reads it", err, "allowed")
	}

	// The people who run the database.
	system := func(id string, groups ...string) fado.Option {
		return fado.As(fado.Principal{ID: id, Kind: fado.ActorPerson, System: groups})
	}
	designer, administrator, analyst := system("dana", fado.GroupDesigners), system("adam", fado.GroupAdministrators), system("alex", fado.GroupAnalysts)
	work := map[string]func(as fado.Option) error{
		"define": func(as fado.Option) error {
			_, err := db.DefineClass(ctx, []byte(scratch("Another", `"a":{"type":"text"}`, oneState)), as)
			return err
		},
		"queue": func(as fado.Option) error { _, err := db.Outbox(ctx, fado.OutboxFilter{}, as); return err },
		"sql":   func(as fado.Option) error { _, err := db.SQL(ctx, "select count(*) from Invoice", nil, as); return err },
		"see":   func(as fado.Option) error { _, err := db.Get(ctx, "Invoice", invoice.ID, as); return err },
	}
	for _, c := range []struct {
		who                     string
		as                      fado.Option
		define, queue, sql, see string
	}{
		{"a designer", designer, "allowed", "forbidden at /system/administrators", "forbidden at /system/analysts", "unknown_document"},
		{"an administrator", administrator, "forbidden at /system/designers", "allowed", "forbidden at /system/analysts", "unknown_document"},
		{"an analyst", analyst, "forbidden at /system/designers", "forbidden at /system/administrators", "allowed", "unknown_document"},
		{"a content administrator", celina, "forbidden at /system/designers", "forbidden at /system/administrators", "forbidden at /system/analysts", "allowed"},
		{"Anna", anna, "forbidden at /system/designers", "forbidden at /system/administrators", "forbidden at /system/analysts", "allowed"},
	} {
		check(c.who+" defines a class", work["define"](c.as), c.define)
		check(c.who+" lists the queue", work["queue"](c.as), c.queue)
		check(c.who+" runs SQL", work["sql"](c.as), c.sql)
		check(c.who+" reads the invoice", work["see"](c.as), c.see)
	}
	for _, group := range []string{"administrators", "everyone"} {
		_, err := db.Get(ctx, "Invoice", invoice.ID, fado.As(fado.Principal{ID: "eve", Groups: []string{group}}))
		check("an ordinary group called "+group, err, "bad_value")
	}

	// What is checked when the class is defined.
	full := `"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"`
	definition := func(access, rights string) string {
		d := `{"name":"Checked","variables":{"n":{"type":"number"}},"states":{"initial":"a","list":["a","b"]},` +
			`"transitions":[{"name":"go","from":"a","to":"b"}],"access":{` + access + `}`
		if rights != "" {
			d += `,"rights":` + rights
		}
		return d + `}`
	}
	held(t, db, page, map[string][]string{
		"has a line of access or a right that is not a condition": {
			definition(`"read":"n + 1","create":"TRUE","update":"TRUE","delete":"FALSE"`, ""),
			definition(full, `{"*":{"n":{"write":"n + 1"}}}`),
		},
		"reads a variable it does not have in a line of access":                {definition(`"read":"zz = USER()","create":"TRUE","update":"TRUE","delete":"FALSE"`, "")},
		"has a line under access.transition for a transition it does not have": {definition(full+`,"transition":{"zz":"TRUE"}`, "")},
		"names a state under rights that it does not have":                     {definition(full, `{"zz":{"*":{"write":"TRUE"}}}`)},
		"names a variable under rights that it does not have":                  {definition(full, `{"*":{"zz":{"write":"TRUE"}}}`)},
		"has a word under access or rights that the engine does not know": {
			definition(full+`,"approve":"TRUE"`, ""),
			definition(full, `{"*":{"n":{"edit":"TRUE"}}}`),
		},
	})

	// A line left out is noted, and lets nobody through.
	bare := `{"name":"Bare","variables":{"n":{"type":"number"}},"states":{"initial":"a","list":["a"]}}`
	notes := 0
	for _, d := range db.ValidateClass([]byte(bare)) {
		if !d.Note || d.Code != "rule_left_out" {
			t.Errorf("a class with no access block: %s, note %v", d.Code, d.Note)
		}
		notes++
	}
	if notes != 4 {
		t.Errorf("a class with no access block gets %d notes; it leaves four lines out", notes)
	}
	if _, err := db.DefineClass(ctx, []byte(bare), setup); err != nil {
		t.Errorf("a class with a line left out is not defined: %v", err)
	}
	_, err = db.Create(ctx, "Bare", fado.Values{"n": 1}, ola)
	check("Ola creates a document of a class with no access block", err, "forbidden at /access/create")

	met.covers(t, "what is refused", marked(t, page, "what is refused"))
}

func TestNotion_HistoryAndTheLog(t *testing.T) {
	page := notion(t, "history-and-the-log.md")
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	at := func(hour, minute int) { clock = time.Date(2026, 11, 16, hour, minute, 0, 0, time.UTC) }
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	ctx := context.Background()
	invoice, err := os.ReadFile(filepath.Join("..", "examples", "first", "invoice.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, invoice, setup); err != nil {
		t.Fatal(err)
	}
	krzysztof, anna := person("krzysztof"), person("anna", "ACCOUNTING")
	met := seen{}
	hour := func(seconds int64) string {
		if seconds == 0 {
			return ""
		}
		return time.Unix(seconds, 0).UTC().Format("15:04")
	}

	// The story.
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/017", "supplier": "ACME", "net": "1000.00 PLN"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	version := func(step string, shown int64) {
		t.Helper()
		now, err := db.Get(ctx, "Invoice", doc.ID, krzysztof)
		if err != nil || now.Version != shown {
			t.Errorf("%s: the invoice is at version %d, %v; the chapter says %d", step, now.Version, err, shown)
		}
	}
	at(10, 0)
	first, err := db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1200.00 PLN"}, krzysztof, fado.Key("net-fix-1"))
	want(t, "10:00: Krzysztof changes the net amount, with a key", err, "allowed")
	at(10, 1)
	again, err := db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1200.00 PLN"}, krzysztof, fado.Key("net-fix-1"))
	want(t, "10:01: the same call again with the same key", err, "allowed")
	if err == nil && (again.Version != first.Version || again.UpdatedAt != first.UpdatedAt) {
		t.Errorf("10:01: the second answer is version %d of %d; the chapter says the caller gets the first answer", again.Version, first.Version)
	}
	version("10:01", 2)
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1300.00 PLN"}, krzysztof, fado.Key("net-fix-1"))
	want(t, "a different call under the same key", err, met.note("key_reused"))
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1200.00 PLN"}, krzysztof)
	want(t, "a change that changes nothing", err, "allowed")
	version("a change that changes nothing", 2)
	at(10, 2)
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"note": "urgent"}, anna)
	want(t, "10:02: Anna tries to write a note", err, "forbidden, variable note")
	version("10:02", 2)
	at(11, 0)
	_, err = db.Transition(ctx, "Invoice", doc.ID, "approve", nil, krzysztof)
	want(t, "11:00: Krzysztof approves", err, "allowed")

	// Looking before.
	_, err = db.Preview(ctx, "Invoice", doc.ID, nil, "book", anna)
	want(t, "a preview of book with no account", err, "guard_false")
	preview, err := db.Preview(ctx, "Invoice", doc.ID, fado.Values{"account": "401"}, "book", anna)
	if err != nil || preview.Document.State != "BOOKED" || preview.Document.Version != 4 || strings.Join(preview.Path, " ") != "INACCOUNTING BOOKED" || preview.Now != clock.Unix() {
		t.Errorf("a preview of book with the account: %v", err)
	}
	version("after the previews", 3)

	at(12, 0)
	_, err = db.Transition(ctx, "Invoice", doc.ID, "book", fado.Values{"account": "401"}, anna, fado.BusinessDate(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)))
	want(t, "12:00: Anna gives the account and books", err, "allowed")

	// The history.
	history, err := db.History(ctx, "Invoice", doc.ID, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	events, err := db.Events(ctx, "Invoice", doc.ID, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	shown := rows(marked(t, page, "the history"))
	if len(history) != len(shown) {
		t.Fatalf("the history has %d versions, and the chapter %d", len(history), len(shown))
	}
	for i, row := range shown {
		h := history[i]
		if got := []string{fmt.Sprint(h.Version), hour(h.ValidFrom), hour(h.ValidTo), h.ActorID, h.State, fmt.Sprint(h.Values["net"])}; !reflect.DeepEqual(got, row) {
			t.Errorf("version %d is %v, and the chapter says %v", i+1, got, row)
		}
	}

	// The log, and how the two point at each other.
	log := rows(marked(t, page, "the log"))
	if len(events) != len(log) {
		t.Fatalf("the log has %d events, and the chapter %d", len(events), len(log))
	}
	for i, row := range log {
		e := events[i]
		if got := []string{e.Kind, e.Name, e.FromState, e.ToState, e.ActorID, hour(e.TS), e.BusinessDate, e.CommandKey}; !reflect.DeepEqual(got, row) {
			t.Errorf("event %d is %v, and the chapter says %v", i+1, got, row)
		}
		if history[i].EventID != e.ID || e.AfterVersion != history[i].Version || e.BeforeVersion != history[i].Version-1 {
			t.Errorf("version %d and event %d do not point at each other", i+1, i+1)
		}
		if i > 0 && e.ID <= events[i-1].ID {
			t.Errorf("the id of event %d does not sort after the one before", i+1)
		}
	}

	// What an event carries: every part, and no other.
	var parts, named []string
	kind := reflect.TypeOf(fado.Event{})
	for i := 0; i < kind.NumField(); i++ {
		tag, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
		parts = append(parts, tag)
	}
	for _, row := range rows(marked(t, page, "what an event carries")) {
		for _, name := range strings.Split(row[0], ",") {
			named = append(named, strings.TrimSpace(name))
		}
	}
	sort.Strings(parts)
	sort.Strings(named)
	if !reflect.DeepEqual(parts, named) {
		t.Errorf("an event carries %v, and the chapter names %v", parts, named)
	}

	// The business day the host program names for the caller.
	hosted := fado.As(fado.Principal{ID: "krzysztof", Kind: fado.ActorPerson, Today: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)})
	friday, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/018", "supplier": "ACME", "net": "5.00 PLN"}, hosted)
	if err != nil {
		t.Fatal(err)
	}
	if made, err := db.Events(ctx, "Invoice", friday.ID, krzysztof); err != nil || len(made) != 1 || made[0].BusinessDate != "2026-11-13" {
		t.Errorf("the business day the host names for the caller: %v, %v", made, err)
	}

	// Reading it with SQL.
	query := regexp.MustCompile("(?s)```sql\r?\n(.*?)\r?\n```").FindSubmatch(page)
	if query == nil {
		t.Fatal("the chapter shows no SQL")
	}
	stays, err := db.SQL(ctx, strings.Replace(string(query[1]), "from Invoice_states", "from Invoice_states where id = ?", 1), []any{doc.ID}, setup)
	if err != nil {
		t.Fatal(err)
	}
	table := rows(marked(t, page, "the stays"))
	if len(stays.Rows) != len(table) {
		t.Fatalf("the query answers %d stays, and the chapter shows %d", len(stays.Rows), len(table))
	}
	for i, row := range table {
		var got []string
		for _, cell := range stays.Rows[i] {
			if cell == nil {
				got = append(got, "")
			} else {
				got = append(got, fmt.Sprint(cell))
			}
		}
		if !reflect.DeepEqual(got, row) {
			t.Errorf("stay %d is %v, and the chapter says %v", i+1, got, row)
		}
	}
	for view, column := range map[string]string{"Invoice": "id", "Invoice_history": "id", "Invoice_events": "event_id", "Invoice_states": "id"} {
		if _, err := db.SQL(ctx, "select count("+column+") from "+view, nil, setup); err != nil {
			t.Errorf("the view %s: %v", view, err)
		}
	}
	_, err = db.SQL(ctx, "select count(*) from Invoice", nil, anna)
	want(t, "Anna runs SQL", err, "forbidden at /system/analysts")

	// Who may read the record.
	private := `{"name":"Private","variables":{"a":{"type":"text"},"secret":{"type":"text"}},"states":{"initial":"x","list":["x"]},
	  "access":{"read":"CREATEDBY() = USER() OR INGROUP(\"readers\")","create":"TRUE","update":"TRUE","delete":"FALSE"},
	  "rights":{"*":{"secret":{"read":"CREATEDBY() = USER()"}}}}`
	if _, err := db.DefineClass(ctx, []byte(private), setup); err != nil {
		t.Fatal(err)
	}
	own, err := db.Create(ctx, "Private", fado.Values{"a": "1", "secret": "s"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := db.History(ctx, "Private", own.ID, anna); err != nil || len(h) != 0 {
		t.Errorf("the history of a document Anna may not see: %d versions, %v", len(h), err)
	}
	if e, err := db.Events(ctx, "Private", own.ID, anna); err != nil || len(e) != 0 {
		t.Errorf("the events of a document Anna may not see: %d, %v", len(e), err)
	}
	_, err = db.Version(ctx, "Private", own.ID, 1, anna)
	want(t, "a version of a document Anna may not see", err, met.note("unknown_document"))
	reader := person("renata", "readers")
	if h, err := db.History(ctx, "Private", own.ID, reader); err != nil || len(h) != 1 {
		t.Errorf("the history for a reader: %d versions, %v", len(h), err)
	} else if _, there := h[0].Values["secret"]; there || h[0].Values["a"] != "1" {
		t.Errorf("a value the reader may not see is in the version: %v", h[0].Values)
	}
	_, err = db.Version(ctx, "Invoice", doc.ID, 99, krzysztof)
	want(t, "a version the document does not have", err, met.note("unknown_document"))

	// The kinds of event: a class that goes through all of them.
	kinds := `{"name":"Kinds","variables":{"a":{"type":"number","default":"1"},"seen":{"formula":"NOW()","mode":"triggered","on":["refresh"]}},
	  "states":{"initial":"x","list":["x","y"]},
	  "transitions":[{"name":"go","from":"x","to":"y"}],
	  "rules":[
	    {"on":"create","do":[{"action":"emit","event":"made"},{"action":"transition","id":"ID()","name":"go"}]},
	    {"on":"enter","state":"y","do":[{"action":"transition","id":"ID()","name":"go"}]}
	  ],
	  "access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`
	if _, err := db.DefineClass(ctx, []byte(kinds), setup); err != nil {
		t.Fatal(err)
	}
	k, err := db.Create(ctx, "Kinds", fado.Values{"a": 5}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Set(ctx, "Kinds", k.ID, fado.Values{"a": 6}, krzysztof); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Reset(ctx, "Kinds", k.ID, []string{"a"}, krzysztof); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Refresh(ctx, "Kinds", k.ID, nil, krzysztof); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, []byte(strings.Replace(kinds, `"a":{"type":"number","default":"1"}`, `"a":{"type":"number","default":"1"},"b":{"type":"text"}`, 1)), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, "Kinds", setup); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "Kinds", k.ID, krzysztof); err != nil {
		t.Fatal(err)
	}
	all, err := db.Events(ctx, "Kinds", k.ID, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	written := map[string]bool{}
	for _, e := range all {
		written[e.Kind] = true
	}
	var got, listed []string
	for kind := range written {
		got = append(got, kind)
	}
	for _, row := range rows(marked(t, page, "the kinds of event")) {
		listed = append(listed, row[0])
	}
	sort.Strings(got)
	sort.Strings(listed)
	if !reflect.DeepEqual(got, listed) {
		t.Errorf("the engine wrote the kinds %v, and the chapter lists %v", got, listed)
	}

	met.covers(t, "what is refused", marked(t, page, "what is refused"))
}

func TestNotion_Time(t *testing.T) {
	page := notion(t, "time.md")
	clock := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	on := func(day int, month time.Month, year int) time.Time {
		return time.Date(year, month, day, 9, 0, 0, 0, time.UTC)
	}
	file := filepath.Join(t.TempDir(), "time.fado")
	options := fado.Options{Now: func() time.Time { return clock }, Outbox: fado.OutboxOptions{Manual: true}}
	db, err := fado.Open(file, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	ctx := context.Background()
	class := notionClass(t, page)
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	krzysztof, ewa := person("krzysztof"), person("ewa")
	// run stands for the engine's worker waking at this moment.
	run := func(step string, shown int) {
		t.Helper()
		if ran, err := db.Drain(ctx, setup); err != nil || ran != shown {
			t.Errorf("%s: the engine made %d moves, %v; the chapter says %d", step, ran, err, shown)
		}
	}
	offer, err := db.Create(ctx, "Offer", fado.Values{"customer": "ACME"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	is := func(step, state string, reminders int, valid string) {
		t.Helper()
		now, err := db.Get(ctx, "Offer", offer.ID, krzysztof)
		if err != nil {
			t.Fatal(err)
		}
		if now.State != state || fmt.Sprint(now.Values["reminders"]) != fmt.Sprint(reminders) || fmt.Sprint(now.Values["valid_until"]) != valid {
			t.Errorf("%s: the offer is %s with %v reminders, valid until %v; the chapter says %s, %d, %s",
				step, now.State, now.Values["reminders"], now.Values["valid_until"], state, reminders, valid)
		}
	}
	is("1 December", "sent", 0, "2026-12-31")
	moment := func(seconds int64) string {
		if seconds == 0 {
			return ""
		}
		return time.Unix(seconds, 0).UTC().Format("2 January 2006, 15:04")
	}

	// When the next one comes.
	open, err := db.Transitions(ctx, "Offer", offer.ID, ewa)
	if err != nil {
		t.Fatal(err)
	}
	table := rows(marked(t, page, "what is open on 1 December"))
	if len(open) != len(table) {
		t.Fatalf("%d transitions leave sent, and the chapter lists %d", len(open), len(table))
	}
	for i, row := range table {
		may := "yes"
		if !open[i].Enabled {
			may = "no: " + open[i].Code
		}
		if got := []string{open[i].Name, open[i].To, may, moment(open[i].FiresAt)}; !reflect.DeepEqual(got, row) {
			t.Errorf("open on 1 December: %v, and the chapter says %v", got, row)
		}
	}

	// Looking at another day.
	due := func(step string, as []fado.Option, shown int) []fado.DueDocument {
		t.Helper()
		found, err := db.Due(ctx, "Offer", append([]fado.Option{krzysztof}, as...)...)
		if err != nil || len(found) != shown {
			t.Errorf("%s: %d offers would move, %v; the chapter says %d", step, len(found), err, shown)
		}
		return found
	}
	due("which offers would move, now", nil, 0)
	if found := due("which offers would move, as of 10 December", []fado.Option{fado.At(on(10, 12, 2026))}, 1); len(found) == 1 &&
		(found[0].ID != offer.ID || found[0].To != "sent" || moment(found[0].Since) != "8 December 2026, 09:00") {
		t.Errorf("as of 10 December: %v; the chapter says a reminder has been due since 8 December", found[0])
	}
	preview, err := db.Preview(ctx, "Offer", offer.ID, nil, "accept", ewa, fado.At(on(20, 12, 2026)))
	if err != nil || preview.Document.State != "accepted" || preview.Now != on(20, 12, 2026).Unix() {
		t.Errorf("may Ewa accept, as of 20 December: %v", err)
	}
	_, err = db.Preview(ctx, "Offer", offer.ID, nil, "accept", ewa, fado.At(on(2, 1, 2027)))
	want(t, "may Ewa accept, as of 2 January", err, "guard_false")
	_, err = db.Transition(ctx, "Offer", offer.ID, "accept", nil, ewa, fado.At(on(20, 12, 2026)))
	want(t, "a change made as of another moment", err, "not_allowed")
	_, err = db.Transition(ctx, "Offer", offer.ID, "remind", nil, krzysztof)
	want(t, "Krzysztof takes remind by hand", err, "forbidden at /access/transition/remind")
	is("after looking", "sent", 0, "2026-12-31")

	// The story.
	run("1 December", 0)
	clock = on(8, 12, 2026)
	run("8 December", 1)
	is("8 December", "sent", 1, "2026-12-31")
	// 15 December: no program is running. What is due is in the file.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	clock = on(20, 12, 2026)
	if db, err = fado.Open(file, options); err != nil {
		t.Fatal(err)
	}
	is("20 December, before the engine ran", "sent", 1, "2026-12-31")
	run("20 December", 1)
	is("20 December", "sent", 2, "2026-12-31")
	run("20 December, again", 0)
	_, err = db.Set(ctx, "Offer", offer.ID, fado.Values{"valid_until": "2026-12-22"}, krzysztof)
	want(t, "20 December: Krzysztof shortens it", err, "allowed")
	open, err = db.Transitions(ctx, "Offer", offer.ID, ewa)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range open {
		if tr.Name == "expire" && moment(tr.FiresAt) != "23 December 2026, 00:00" {
			t.Errorf("after the change expire fires on %s; the chapter says 23 December", moment(tr.FiresAt))
		}
		if tr.Name == "remind" && moment(tr.FiresAt) != "27 December 2026, 09:00" {
			t.Errorf("the next reminder is due on %s; the chapter says seven days after the late one", moment(tr.FiresAt))
		}
	}
	clock = on(23, 12, 2026)
	_, err = db.Transition(ctx, "Offer", offer.ID, "accept", nil, ewa)
	want(t, "23 December: Ewa accepts", err, "guard_false")
	run("23 December", 1)
	is("23 December", "expired", 2, "2026-12-22")

	// The log.
	events, err := db.Events(ctx, "Offer", offer.ID, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	log := rows(marked(t, page, "the log"))
	if len(events) != len(log) {
		t.Fatalf("the log has %d events, and the chapter %d", len(events), len(log))
	}
	for i, row := range log {
		e := events[i]
		if got := []string{e.Kind, e.Name, e.FromState, e.ToState, e.ActorID, e.Origin}; !reflect.DeepEqual(got, row) {
			t.Errorf("event %d is %v, and the chapter says %v", i+1, got, row)
		}
	}

	// A timer whose document has moved on does nothing.
	clock = on(1, 12, 2026)
	second, err := db.Create(ctx, "Offer", fado.Values{"customer": "Borealis"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	clock = on(5, 12, 2026)
	if _, err := db.Transition(ctx, "Offer", second.ID, "accept", nil, ewa); err != nil {
		t.Fatal(err)
	}
	clock = on(2, 1, 2027)
	if _, err := db.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if kept, err := db.Get(ctx, "Offer", second.ID, krzysztof); err != nil || kept.State != "accepted" || kept.Version != 2 || fmt.Sprint(kept.Values["reminders"]) != "0" {
		t.Errorf("an accepted offer after its timers were due: %v, %v", kept, err)
	}

	// With another formula under access, a person may take a timed
	// transition before its time.
	early := bytes.Replace(class, []byte(`"remind": "FALSE"`), []byte(`"remind": "CREATEDBY() = USER()"`), 1)
	if bytes.Equal(early, class) {
		t.Fatal("the class of the chapter has no line for remind to replace")
	}
	other := notionDB(t, fado.Options{})
	if _, err := other.DefineClass(ctx, early, setup); err != nil {
		t.Fatal(err)
	}
	third, err := other.Create(ctx, "Offer", fado.Values{"customer": "ACME"}, krzysztof)
	if err != nil {
		t.Fatal(err)
	}
	reminded, err := other.Transition(ctx, "Offer", third.ID, "remind", nil, krzysztof)
	if err != nil || fmt.Sprint(reminded.Values["reminders"]) != "1" {
		t.Errorf("a timed transition taken by hand before its time: %v", err)
	}

	// What is checked when the class is defined.
	timed := func(transition string) string {
		return scratch("Checked", `"n":{"type":"number"},"d":{"type":"date"}`,
			`"states":{"initial":"a","list":["a","b","c","c2"],"final":["c","c2"]},`+
				`"transitions":[{"name":"end","from":"b","to":"c"},`+transition+`]`)
	}
	held(t, db, page, map[string][]string{
		"has both after and at":                  {timed(`{"name":"x","from":"a","to":"b","after":"7d","at":"d"}`)},
		"has a time and leaves a final state":    {timed(`{"name":"x","from":"c","to":"c2","after":"7d"}`)},
		"has an at and leads into its own state": {timed(`{"name":"x","from":"a","to":"a","at":"d"}`)},
		"has an at that reads the clock or the caller": {
			timed(`{"name":"x","from":"a","to":"b","at":"ADDDAYS(TODAY(), 1)"}`),
			timed(`{"name":"x","from":"a","to":"b","at":"IF(USER() = \"a\", d, d)"}`),
		},
		"has an at that is not a date or a datetime": {timed(`{"name":"x","from":"a","to":"b","at":"n + 1"}`)},
		"has an after that is not a length of time":  {timed(`{"name":"x","from":"a","to":"b","after":"a week"}`)},
	})
	for what, c := range map[string][2]string{
		"an after in hours": {timed(`{"name":"x","from":"a","to":"b","after":"36h"}`), "accepted"},
		"a computed variable that reads the life of the document": {scratch("Checked", `"n":{"type":"number"},"waited":{"formula":"DAYSIN(\"a\")"}`, oneState), "bad_formula"},
		"a default that reads the life of the document":           {scratch("Checked", `"n":{"type":"number","default":"DAYSIN(\"a\")"}`, oneState), "accepted"},
	} {
		if got := finding(db, c[0]); got != c[1] {
			t.Errorf("%s: the engine finds %s, and the chapter says %s", what, got, c[1])
		}
	}
}
