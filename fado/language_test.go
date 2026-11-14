package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// TestFormulaLanguage works out every example of the chapter on the
// formula language. The chapter shows a schema, a class and the values
// of one document, and then tables of formulas with the answer and the
// type of each. For every row the test adds the formula to the class as
// a computed variable, defines the class, creates the document, and
// compares what the engine answers, and the type it gives the result,
// with the row. A formula the chapter calls refused must be refused when
// the class is defined, and one it says fails must fail when the
// document is created, each with the code of the row.
func TestFormulaLanguage(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "formula-language.md"))
	if err != nil {
		t.Fatal(err)
	}
	var schema, class []byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		switch kindOf(m[1]) {
		case "schema":
			schema = m[1]
		case "class":
			class = m[1]
		}
	}
	// The values of the document stand in a block of their own kind, so
	// that the general check of the manual does not take them for a
	// piece of the class.
	given := regexp.MustCompile("(?s)```jsonc\r?\n(.*?)\r?\n```").FindSubmatch(page)
	if schema == nil || class == nil || given == nil {
		t.Fatal("the chapter shows no schema, no class or no values")
	}
	var values fado.Values
	if err := json.Unmarshal(given[1], &values); err != nil {
		t.Fatal(err)
	}
	var definition map[string]json.RawMessage
	if err := json.Unmarshal(class, &definition); err != nil {
		t.Fatal(err)
	}
	var variables map[string]json.RawMessage
	if err := json.Unmarshal(definition["variables"], &variables); err != nil {
		t.Fatal(err)
	}

	db, err := fado.Open(filepath.Join(t.TempDir(), "language.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineSchema(ctx, schema, setup); err != nil {
		t.Fatal(err)
	}

	// answer works a formula out and writes the result as the chapter
	// does, with the type the engine gives it.
	n := 0
	answer := func(formula string) (string, string) {
		n++
		name := fmt.Sprintf("Sample%d", n)
		quoted, _ := json.Marshal(formula)
		variables["result"] = json.RawMessage(`{"formula":` + string(quoted) + `}`)
		definition["variables"], _ = json.Marshal(variables)
		definition["name"], _ = json.Marshal(name)
		whole, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range db.ValidateClass(whole) {
			if !d.Note {
				return "refused: " + d.Code, ""
			}
		}
		defined, err := db.DefineClass(ctx, whole, setup)
		if err != nil {
			return "not defined: " + err.Error(), ""
		}
		doc, err := db.Create(ctx, name, values, person("krzysztof"))
		if err != nil {
			var refusal *fado.Error
			if errors.As(err, &refusal) && refusal.Variable == "result" {
				return "fails: " + refusal.Code, ""
			}
			return "not created: " + err.Error(), ""
		}
		kind := ""
		for _, v := range defined.Variables {
			if v.Name == "result" {
				kind = v.Type.String()
			}
		}
		value := doc.Values["result"]
		switch {
		case value == nil:
			return "blank", kind
		case kind == "text":
			return `"` + fmt.Sprint(value) + `"`, kind
		case kind == "bool":
			return strings.ToUpper(fmt.Sprint(value)), kind
		case kind == "datetime":
			var seconds int64
			raw, _ := json.Marshal(value)
			if json.Unmarshal(raw, &seconds) != nil {
				return fmt.Sprint(value), kind
			}
			return time.Unix(seconds, 0).UTC().Format("2006-01-02 15:04:05"), kind
		}
		return fmt.Sprint(value), kind
	}

	text := strings.ReplaceAll(string(page), "\r\n", "\n")
	tables := strings.Split(text, "<!-- examples -->\n")[1:]
	if len(tables) < 10 {
		t.Fatalf("the chapter has %d tables of examples", len(tables))
	}
	examples := 0
	for _, rest := range tables {
		table, _, _ := strings.Cut(rest, "\n\n")
		for _, row := range rows(table) {
			if len(row) != 3 {
				t.Errorf("a row of examples has %d cells: %v", len(row), row)
				continue
			}
			examples++
			got, kind := answer(row[0])
			if got != row[1] || kind != row[2] {
				t.Errorf("%s is %s of type %q, and the chapter says %s of type %q", row[0], got, kind, row[1], row[2])
			}
		}
	}
	t.Logf("%d formulas worked out", examples)
}

// TestFormulaPlaces holds the chapter's table of where a formula stands:
// what a formula may read in each place.
func TestFormulaPlaces(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "places.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	chart := `"states":{"initial":"a","list":["a","b"]},"transitions":[{"name":"go","from":"a","to":"b"}]`
	class := func(variables, more string) string {
		return `{"name":"Place","variables":{"d":{"type":"date"},"who":{"type":"text"}` + variables + `},` + chart + more +
			`,"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`
	}
	timed := func(at string) string {
		return `{"name":"Place","variables":{"d":{"type":"date"}},"states":{"initial":"a","list":["a","b"]},` +
			`"transitions":[{"name":"go","from":"a","to":"b","at":"` + at + `"}],` +
			`"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`
	}
	for what, c := range map[string][2]string{
		"a computed variable that reads the document":        {class(`,"r":{"formula":"ADDDAYS(d, 1)"}`, ""), "accepted"},
		"a computed variable that reads the clock":           {class(`,"r":{"formula":"DAYS(TODAY(), d)"}`, ""), "bad_formula"},
		"a computed variable that reads the caller":          {class(`,"r":{"formula":"who = USER()"}`, ""), "bad_formula"},
		"a computed variable that reads the document's life": {class(`,"r":{"formula":"DAYSIN(\"a\")"}`, ""), "bad_formula"},
		"a title that reads the clock":                       {class("", `,"title":"TEXT(TODAY())"`), "bad_formula"},
		"a title that reads the document":                    {class("", `,"title":"\"Due \" & d"`), "accepted"},
		"a default that reads the clock and the caller":      {class(`,"made":{"type":"date","default":"TODAY()"},"by":{"type":"text","default":"USER()"}`, ""), "accepted"},
		"a guard that reads the clock, the caller and the life": {
			`{"name":"Place","variables":{"d":{"type":"date"},"who":{"type":"text"}},"states":{"initial":"a","list":["a","b"]},` +
				`"transitions":[{"name":"go","from":"a","to":"b","guard":"TODAY() > d AND who = USER() AND DAYSIN(\"a\") > 1"}],` +
				`"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`, "accepted"},
		"a line of access and a right that read the clock and the caller": {
			`{"name":"Place","variables":{"d":{"type":"date"},"who":{"type":"text"}},` + chart +
				`,"access":{"read":"who = USER() OR TODAY() > d","create":"TRUE","update":"TRUE","delete":"FALSE"},` +
				`"rights":{"*":{"d":{"write":"who = USER() AND NOW() > ENTEREDAT()"}}}}`, "accepted"},
		"a rule that reads the clock and the caller": {class("", `,"rules":[{"on":"transition","name":"go","if":"TODAY() > d","do":[{"action":"set","values":{"who":"USER()"}}]}]`), "accepted"},
		"an at that reads the document":              {timed(`ADDDAYS(d, 1)`), "accepted"},
		"an at that reads the life of the document":  {timed(`ADDDAYS(FIRSTENTERED(\"a\"), 30)`), "accepted"},
		"an at that reads the clock":                 {timed(`ADDDAYS(TODAY(), 1)`), "not_allowed"},
		"an at that reads the caller":                {timed(`IF(USER() = \"a\", d, d)`), "not_allowed"},
	} {
		if got := finding(db, c[0]); got != c[1] {
			t.Errorf("%s: the engine finds %s, and the chapter says %s", what, got, c[1])
		}
	}
}
