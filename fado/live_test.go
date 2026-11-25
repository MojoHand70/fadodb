package fado_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on live variables is a reference: it says what a value
// worked out when it is read is, what such a variable may say, what may
// read it and who. TestLiveVariables takes the class out of the chapter,
// plays its story, and holds every table of the chapter to what the
// engine answers.
func TestLiveVariables(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "live-variables.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows checks the chapter out with \r\n; the blocks are found by \n.
	page = bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
	ctx := context.Background()
	var class []byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		if kindOf(m[1]) == "class" && className(string(m[1])) == "Invoice" {
			class = m[1]
		}
	}
	if class == nil {
		t.Fatal("the chapter shows the class Invoice")
	}
	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	clock := start
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	// code is the code of a refusal, without the variable it names.
	code := func(err error) string { c, _, _ := strings.Cut(answer(err), ","); return c }
	people := map[string]fado.Option{"Jan": person("jan", "sales"), "Piotr": person("piotr", "sales"), "Ewa": person("ewa", "finance")}
	if _, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV-17", "net": "100.00 PLN"}, people["Jan"], fado.WithID("FV-17")); err != nil {
		t.Fatal(err)
	}

	// The story: who asks for what on which day, and the answer.
	for _, row := range rows(marked(t, page, "the story")) {
		on, who, name, want := row[0], row[1], row[2], row[3]
		day, err := time.Parse("2 January 2006", on+" 2026")
		if err != nil {
			t.Fatalf("the story names the day %q", on)
		}
		clock = day.Add(9 * time.Hour)
		got, err := db.Live(ctx, "Invoice", "FV-17", name, people[who])
		if err != nil || fmt.Sprint(got.Value) != want {
			t.Errorf("on %s %s asks for %s: the engine answers %v, %v, and the chapter says %s", on, who, name, got, err, want)
		}
	}
	// Nothing was written meanwhile.
	doc, err := db.Get(ctx, "Invoice", "FV-17", people["Jan"])
	if err != nil || doc.Version != 1 {
		t.Fatalf("after the story the invoice is %v, %v; the chapter says it is still at version 1", doc, err)
	}
	if lines, err := db.History(ctx, "Invoice", "FV-17", people["Jan"]); err != nil || len(lines) != 1 {
		t.Errorf("after the story the history has %d lines, %v; the chapter says one", len(lines), err)
	}
	// What the document carries, by get and by query.
	found, err := db.Query(ctx, fado.Filter{Class: "Invoice"}, people["Jan"])
	if err != nil || len(found) != 1 {
		t.Fatalf("query answers %v, %v", found, err)
	}
	carried := rows(marked(t, page, "what the document carries"))
	if len(carried) != 6 {
		t.Errorf("the chapter lists %d variables of the invoice, and the class has six", len(carried))
	}
	for _, row := range carried {
		for how, d := range map[string]*fado.Document{"get": doc, "query": found[0]} {
			if _, in := d.Values[row[0]]; in != (row[1] == "yes") {
				t.Errorf("%s carries %s: %v, and the chapter says %s", how, row[0], in, row[1])
			}
		}
	}

	// The command and its answer, as the chapter shows them.
	clock = start.Add(9 * 24 * time.Hour)
	var asked struct{ Class, ID, Variable string }
	if err := json.Unmarshal(jsoncAt(t, page, "the command"), &asked); err != nil {
		t.Fatal(err)
	}
	got, err := db.Live(ctx, asked.Class, asked.ID, asked.Variable, people["Ewa"])
	if err != nil {
		t.Fatal(err)
	}
	answered, _ := json.Marshal(got)
	if want := strings.Join(strings.Fields(string(jsoncAt(t, page, "the answer"))), ""); string(answered) != want {
		t.Errorf("the command answers %s, and the chapter shows %s", answered, want)
	}

	// Who may not read one.
	asks := map[string]func() error{
		"Piotr, who may not read Jan's invoice | waiting_days": func() error {
			_, err := db.Live(ctx, "Invoice", "FV-17", "waiting_days", people["Piotr"])
			return err
		},
		"Ewa, who is not in sales | mine": func() error { _, err := db.Live(ctx, "Invoice", "FV-17", "mine", people["Ewa"]); return err },
		"Jan | shout, which is kept":      func() error { _, err := db.Live(ctx, "Invoice", "FV-17", "shout", people["Jan"]); return err },
		"Jan | nothing, which the class has not": func() error {
			_, err := db.Live(ctx, "Invoice", "FV-17", "nothing", people["Jan"])
			return err
		},
		"Jan, of an invoice that is not there | waiting_days": func() error {
			_, err := db.Live(ctx, "Invoice", "FV-99", "waiting_days", people["Jan"])
			return err
		},
	}
	refusals := rows(marked(t, page, "who may not"))
	if len(refusals) != len(asks) {
		t.Errorf("the chapter has %d rows of who may not read, and the test %d", len(refusals), len(asks))
	}
	for _, row := range refusals {
		ask, ok := asks[row[0]+" | "+row[1]]
		if !ok {
			t.Errorf("the chapter says that %s asking for %s is answered %s, and the test has no such case", row[0], row[1], row[2])
			continue
		}
		if got := code(ask()); got != row[2] {
			t.Errorf("%s asks for %s: the engine answers %s, and the chapter says %s", row[0], row[1], got, row[2])
		}
	}

	// What a live variable may not say, and what may not read it.
	live := func(more string) string {
		return scratch("T", `"a": {"type": "int"}, "s": {"type": "text"}, "l": {"mode": "live", "formula": "a + 1"`+more+`}`, oneState)
	}
	heldBy(t, page, "what it may not say", map[string][]string{
		"on":       {live(`, "on": ["refresh"]`)},
		"default":  {live(`, "default": "1"`)},
		"required": {live(`, "required": true`)},
		"values":   {scratch("T", `"s": {"type": "text"}, "l": {"mode": "live", "formula": "s", "values": ["x"]}`, oneState)},
		"min":      {live(`, "min": 0`)},
		"max":      {live(`, "max": 9`)},
		"index":    {live(`, "index": true`)},
		"list":     {live(`, "list": true`)},
	}, func(definition string) string { return finding(db, definition) })

	const base = `"a": {"type": "int"}, "l": {"mode": "live", "formula": "a + 1"}`
	two := `"states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"go","from":"a","to":"b","guard":"l > 0"}]`
	heldBy(t, page, "what may not read it", map[string][]string{
		"a computed variable":                {scratch("T", base+`, "c": {"formula": "l * 2"}`, oneState)},
		"a triggered variable":               {scratch("T", base+`, "c": {"type": "int", "formula": "l", "mode": "triggered", "on": ["refresh"]}`, oneState)},
		"a default":                          {scratch("T", base+`, "c": {"type": "int", "default": "l"}`, oneState)},
		"the title":                          {`{"name":"T","title":"TEXT(l)","variables":{` + base + `},` + oneState + `,"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`},
		"a guard":                            {scratch("T", base, two)},
		"an access rule":                     {`{"name":"T","variables":{` + base + `},` + oneState + `,"access":{"read":"l > 0","create":"TRUE","update":"TRUE","delete":"TRUE"}}`},
		"another live variable":              {scratch("T", base+`, "m": {"mode": "live", "formula": "l + 1"}`, oneState)},
		"the filter of query":                {"query"},
		"a set, which would give it a value": {"set"},
	}, func(definition string) string {
		switch definition {
		case "query":
			_, err := db.Query(ctx, fado.Filter{Class: "Invoice", Where: "mine"}, people["Jan"])
			return code(err)
		case "set":
			_, err := db.Set(ctx, "Invoice", "FV-17", fado.Values{"mine": true}, people["Jan"])
			return code(err)
		}
		return finding(db, definition)
	})

	// A live variable may look one value up, in a table or among the
	// documents of a class (ADR-0076, item 5); the store's own tests hold
	// what each reader is answered.
	if got := finding(db, scratch("T", `"a": {"type": "int"}, "l": {"type": "int", "mode": "live", "formula": "LOOKUP(T, MAX(a), TRUE)"}`, oneState)); got != "accepted" {
		t.Errorf("a live variable that looks at other documents: the engine finds %s, and the chapter says it is taken", got)
	}
}
