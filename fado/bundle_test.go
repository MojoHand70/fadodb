package fado_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
	"github.com/MojoHand70/fadodb/internal/formula"
)

// bundlePage is the chapter on the bundle, its line endings made plain.
func bundlePage(t *testing.T) []byte {
	t.Helper()
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "the-bundle.md"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
}

// jsoncAt is the jsonc block that follows a marker of the chapter.
func jsoncAt(t *testing.T, page []byte, marker string) []byte {
	t.Helper()
	_, rest, found := strings.Cut(string(page), "<!-- "+marker+" -->\n```jsonc\n")
	if !found {
		t.Fatalf("the chapter has no block %q", marker)
	}
	body, _, _ := strings.Cut(rest, "\n```")
	return []byte(body)
}

// olasInvoice is a database with the invoice of the chapter on the
// class and its Polish words.
func olasInvoice(t *testing.T) *fado.DB {
	t.Helper()
	db, err := fado.Open(filepath.Join(t.TempDir(), "bundle.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.DefineClass(ctx, classIn(t, "class.md", "Invoice"), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineWords(ctx, wordsIn(t, "class.md", "Invoice"), setup); err != nil {
		t.Fatal(err)
	}
	return db
}

// compiledOut writes every compiled formula of a bundle as the chapter
// does, <compiled>: the bytecode in base64 begins with FADO.
func compiledOut(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = compiledOut(e)
		}
	case []any:
		for i, e := range x {
			x[i] = compiledOut(e)
		}
	case string:
		if strings.HasPrefix(x, base64.StdEncoding.EncodeToString([]byte("FADO"))[:6]) {
			return "<compiled>"
		}
	}
	return v
}

func decoded(t *testing.T, text []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(text, &v); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return v
}

// The chapter's call, its answer and the answer to a page that holds
// it, as the engine gives them; and the same bundle to a browser, kept
// by its tag.
func TestBundle_TheCall(t *testing.T) {
	db := olasInvoice(t)
	ctx := context.Background()
	page := bundlePage(t)
	b, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice", Language: "pl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Tag) != 16 || len(b.JSON) < 2500 || len(b.JSON) > 3500 {
		t.Errorf("the bundle has the tag %q and %d bytes, and the chapter says about 3 KB", b.Tag, len(b.JSON))
	}
	ola := fado.Principal{ID: "ola", Kind: fado.ActorPerson, Groups: []string{"finance"}}
	answer, err := db.Exec(ctx, ola, "get_bundle", jsoncAt(t, page, "the call"))
	if err != nil || !bytes.Equal(answer, b.JSON) {
		t.Errorf("the chapter's call answers %v\n%s", err, answer)
	}
	got := compiledOut(decoded(t, b.JSON)).(map[string]any)
	want := decoded(t, jsoncAt(t, page, "Ola's bundle"))
	got["tag"] = want["tag"]
	if !reflect.DeepEqual(got, want) {
		text, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("the engine's bundle is not the chapter's:\n%s", text)
	}

	// A page that holds it.
	unchanged := decoded(t, jsoncAt(t, page, "unchanged"))
	answer, err = db.Exec(ctx, ola, "get_bundle", []byte(`{"class": "Invoice", "language": "pl", "tag": "`+b.Tag+`"}`))
	unchanged["tag"] = b.Tag
	if err != nil || !reflect.DeepEqual(decoded(t, answer), unchanged) {
		t.Errorf("a page that holds the bundle is answered %v %s", err, answer)
	}
	if answer, err = db.Exec(ctx, ola, "get_bundle", []byte(`{"class": "Invoice", "language": "pl", "tag": "0000000000000000"}`)); err != nil || !bytes.Equal(answer, b.JSON) {
		t.Errorf("a page that holds an older bundle is answered %v %s", err, answer)
	}

	// A browser.
	door, err := httpdoor.New(db, httpdoor.Options{Who: func(*http.Request) (fado.Principal, error) { return ola, nil }})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/bundle?class=Invoice&language=pl", nil)
	w := httptest.NewRecorder()
	door.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != string(b.JSON)+"\n" || w.Header().Get("ETag") != `"`+b.Tag+`"` ||
		w.Header().Get("Cache-Control") != "private, no-cache" || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("GET /v1/bundle: %d %v %s", w.Code, w.Header(), w.Body)
	}
	r = httptest.NewRequest(http.MethodGet, "/v1/bundle?class=Invoice&language=pl", nil)
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	door.ServeHTTP(w, r)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("ETag") != `"`+b.Tag+`"` {
		t.Errorf("GET /v1/bundle from a browser that holds it: %d %v %s", w.Code, w.Header(), w.Body)
	}
}

// The words put in place for each language the chapter's table asks.
func TestBundle_WhichWords(t *testing.T) {
	db := olasInvoice(t)
	ctx := context.Background()
	table := rows(marked(t, bundlePage(t), "which words"))
	if len(table) != 5 {
		t.Fatalf("the table has %d rows", len(table))
	}
	for _, row := range table {
		asked := row[0]
		if asked == "nothing" {
			asked = ""
		}
		b, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice", Language: asked})
		if err != nil {
			t.Errorf("%s: %v", row[0], err)
			continue
		}
		var got struct {
			Language   *string        `json:"language"`
			Words      map[string]int `json:"words"`
			Definition struct {
				Label string `json:"label"`
			} `json:"definition"`
		}
		json.Unmarshal(b.JSON, &got)
		words := "none: the class's own"
		for name := range got.Words {
			words = name
		}
		language := "absent"
		if got.Language != nil {
			language = *got.Language
		}
		label := "Faktura"
		if words != "class/Invoice/pl" {
			label = "Invoice"
		}
		if words != row[1] || language != row[2] || got.Definition.Label != label {
			t.Errorf("%s: words %s, language %s, label %s; the chapter says %s, %s", row[0], words, language, got.Definition.Label, row[1], row[2])
		}
	}
}

// pageBundle is what a page reads of a bundle to grey a field out.
type pageBundle struct {
	Compiled struct {
		Access struct {
			Update string `json:"update"`
		} `json:"access"`
		Rights map[string]map[string]struct {
			Write *struct {
				Groups  *[]string `json:"groups"`
				Formula string    `json:"formula"`
				Cell    string    `json:"cell"`
			} `json:"write"`
		} `json:"rights"`
	} `json:"compiled"`
}

// runCompiled runs a compiled formula of a bundle, as a page does, for
// a principal; the formulas this test runs read no value.
func runCompiled(t *testing.T, b64 string, p fado.Principal, state string) bool {
	t.Helper()
	code, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := formula.Decode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Slots) > 0 {
		t.Fatalf("the formula reads %d values, and this test gives none", len(prog.Slots))
	}
	v, err := formula.NewVM().Run(prog, nil, &formula.Context{User: p.ID, Groups: p.Groups, System: p.System, State: state})
	return err == nil && v.Truth()
}

// What Ola's page greys out in review, from the bundle alone, and what
// the server answers when the value is set.
func TestBundle_WhatOlasPageGreysOut(t *testing.T) {
	db := olasInvoice(t)
	ctx := context.Background()
	inv, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Acme", "number": "FV/9", "net": "100.00 PLN", "vat": "23.00 PLN"}, person("jan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Invoice", inv.ID, "file", nil, person("jan")); err != nil {
		t.Fatal(err)
	}
	b, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice", Language: "pl"})
	if err != nil {
		t.Fatal(err)
	}
	var pb pageBundle
	if err := json.Unmarshal(b.JSON, &pb); err != nil {
		t.Fatal(err)
	}
	people := map[string]fado.Principal{
		"Ewa, an approver":  {ID: "ewa", Kind: fado.ActorPerson, Groups: []string{"approvers"}},
		"Marta, in finance": {ID: "marta", Kind: fado.ActorPerson, Groups: []string{"finance"}},
		"Ola, in finance":   {ID: "ola", Kind: fado.ActorPerson, Groups: []string{"finance"}},
	}
	values := map[string]any{"net": "120.00 PLN", "vat": "27.60 PLN", "street": "Długa 1"}
	table := rows(marked(t, bundlePage(t), "what Ola's page greys out"))
	if len(table) != 4 {
		t.Fatalf("the table has %d rows", len(table))
	}
	for _, row := range table {
		p, ok := people[row[0]]
		if !ok {
			t.Fatalf("who is %s?", row[0])
		}
		variable := row[1]
		// The page: the update rule, then the variable's right in review.
		open := runCompiled(t, pb.Compiled.Access.Update, p, "review")
		rule := "update alone"
		if right := pb.Compiled.Rights["review"][variable].Write; right != nil {
			if right.Groups != nil {
				rule = right.Cell + ": the groups " + strings.Join(*right.Groups, ", ")
				in := false
				for _, g := range *right.Groups {
					for _, h := range p.Groups {
						in = in || g == h
					}
				}
				open = open && in
			} else {
				rule = right.Cell + ": FALSE"
				open = open && runCompiled(t, right.Formula, p, "review")
			}
		}
		shows := "greyed out"
		if open {
			shows = "open"
		}
		// The server.
		pre, err := db.Preview(ctx, "Invoice", inv.ID, fado.Values{variable: values[variable]}, "", fado.As(p))
		if err != nil {
			t.Fatal(err)
		}
		says := "set"
		for _, e := range pre.Errors {
			says = "refused, " + e.Code
		}
		if got := []string{rule, shows, says}; !reflect.DeepEqual(got, row[2:]) {
			t.Errorf("%s sets %s: %v, the chapter says %v", row[0], variable, got, row[2:])
		}
	}
}

// Ann's quote reads its rate from a table: the bundle marks the rate as
// the server's, and works out what reads it.
func TestBundle_TheRate(t *testing.T) {
	page := bundlePage(t)
	var table, class []byte
	for _, m := range regexp.MustCompile("(?s)```json\n(.*?)\n```").FindAllSubmatch(page, -1) {
		switch kindOf(m[1]) {
		case "table":
			table = m[1]
		case "class":
			class = m[1]
		}
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "quote.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineTable(ctx, table, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	b, err := db.GetBundle(ctx, fado.BundleOf{Class: "Quote"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Compiled struct {
			Title    *json.RawMessage  `json:"title"`
			Computed []json.RawMessage `json:"computed"`
		} `json:"compiled"`
	}
	json.Unmarshal(b.JSON, &got)
	if len(got.Compiled.Computed) != 2 || got.Compiled.Title == nil {
		t.Fatalf("the quote's formulas: %s", b.JSON)
	}
	if want := decoded(t, jsoncAt(t, page, "the rate")); !reflect.DeepEqual(decoded(t, got.Compiled.Computed[0]), want) {
		t.Errorf("the rate is %s", got.Compiled.Computed[0])
	}
	inPLN := decoded(t, got.Compiled.Computed[1])
	if inPLN["variable"] != "in_pln" || inPLN["server"] != nil || inPLN["formula"] == nil || !reflect.DeepEqual(inPLN["reads"], []any{"amount", "rate"}) {
		t.Errorf("in_pln is %v", inPLN)
	}
}

// Each refusal of the chapter's table.
func TestBundle_Refused(t *testing.T) {
	db := olasInvoice(t)
	ctx := context.Background()
	door, err := httpdoor.New(db, httpdoor.Options{Who: func(*http.Request) (fado.Principal, error) {
		return fado.Principal{ID: "ola", Kind: fado.ActorPerson}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	refusal := func(w *httptest.ResponseRecorder) *fado.Error {
		var got struct {
			Error *fado.Error `json:"error"`
		}
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Error == nil {
			return &fado.Error{}
		}
		return got.Error
	}
	table := rows(marked(t, bundlePage(t), "refused"))
	if len(table) != 5 {
		t.Fatalf("the table has %d rows", len(table))
	}
	for _, row := range table {
		var answer string
		switch row[0] {
		case "the bundle of a class the database does not have":
			_, err := db.GetBundle(ctx, fado.BundleOf{Class: "Receipt"})
			answer = "refused, " + codeOf(err)
		case "the bundle in the language polish":
			_, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice", Language: "polish"})
			var e *fado.Error
			if errors.As(err, &e) {
				answer = "refused, " + e.Code + " at " + e.Path
			}
		case "version 7 of a class at version 1":
			_, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice", Version: 7})
			answer = "refused, " + codeOf(err)
		case "POST /v1/bundle":
			w := httptest.NewRecorder()
			door.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/bundle?class=Invoice", nil))
			answer = "refused, " + refusal(w).Code + ", with Allow: " + w.Header().Get("Allow")
			if w.Code != http.StatusMethodNotAllowed {
				answer += ", status " + http.StatusText(w.Code)
			}
		case "GET /v1/bundle without a class":
			w := httptest.NewRecorder()
			door.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/bundle?language=pl", nil))
			e := refusal(w)
			answer = "refused, " + e.Code + " at " + e.Path
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if answer != row[1] {
			t.Errorf("%s: %s, the chapter says %s", row[0], answer, row[1])
		}
	}
}

// The bundle follows the definitions: the tag stays while nothing
// changes, and changes with the words and with the class; an earlier
// version is answered while a document it computed is there.
func TestBundle_FollowsTheDefinitions(t *testing.T) {
	db := olasInvoice(t)
	ctx := context.Background()
	of := fado.BundleOf{Class: "Invoice", Language: "pl"}
	first, err := db.GetBundle(ctx, of)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := db.GetBundle(ctx, of)
	if again.Tag != first.Tag {
		t.Errorf("the tag changed with nothing defined: %s, %s", first.Tag, again.Tag)
	}
	// Words defined again.
	polish := strings.Replace(string(wordsIn(t, "class.md", "Invoice")), `"Razem"`, `"Do zapłaty"`, 1)
	if _, err := db.DefineWords(ctx, []byte(polish), setup); err != nil {
		t.Fatal(err)
	}
	words, _ := db.GetBundle(ctx, of)
	if words.Tag == first.Tag || !strings.Contains(string(words.JSON), `"label":"Do zapłaty"`) || !strings.Contains(string(words.JSON), `"class/Invoice/pl":2`) {
		t.Errorf("the bundle after the words: %s %s", words.Tag, words.JSON)
	}
	// A document computed by version 1, then version 2 of the class.
	if _, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Acme", "number": "FV/1"}, person("jan")); err != nil {
		t.Fatal(err)
	}
	class := strings.Replace(string(classIn(t, "class.md", "Invoice")), `"label": "Notes"`, `"label": "Remarks"`, 1)
	if _, err := db.DefineClass(ctx, []byte(class), setup); err != nil {
		t.Fatal(err)
	}
	second, err := db.GetBundle(ctx, of)
	if err != nil || second.Version != 2 || second.Tag == words.Tag {
		t.Errorf("the bundle of version 2: %v %+v", err, second)
	}
	if old, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice", Language: "pl", Version: 1}); err != nil || old.Version != 1 || old.Tag != words.Tag {
		t.Errorf("the bundle of version 1, which computed a document: %v", err)
	}
	// Words taken away leave the class's own.
	if err := db.RemoveWords(ctx, fado.WordsOf{Class: "Invoice", Language: "pl"}, setup); err != nil {
		t.Fatal(err)
	}
	gone, _ := db.GetBundle(ctx, of)
	if !strings.Contains(string(gone.JSON), `"languages":[],"words":{}`) || !strings.Contains(string(gone.JSON), `"label":"Invoice"`) {
		t.Errorf("the bundle after the words were taken away: %s", gone.JSON)
	}
}

// The samples carry every kind of formula a bundle has: a title, the
// defaults, the guards, a rule of a transition, rights in every state
// and a right to read.
func TestBundle_TheSamples(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "samples.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, name := range []string{"receipt.json", "invoice.json"} {
		data, err := os.ReadFile(filepath.Join("..", "examples", "classes", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DefineClass(ctx, data, setup); err != nil {
			t.Fatal(err)
		}
	}
	b, err := db.GetBundle(ctx, fado.BundleOf{Class: "Invoice"})
	if err != nil {
		t.Fatal(err)
	}
	got := compiledOut(decoded(t, b.JSON)).(map[string]any)["compiled"]
	want := decoded(t, []byte(`{
	  "title": {"variable": "title", "reads": ["customer"], "formula": "<compiled>"},
	  "defaults": [{"variable": "due", "type": "date", "reads": [], "formula": "<compiled>"},
	               {"variable": "discount", "type": "decimal(2)", "reads": [], "formula": "<compiled>"}],
	  "computed": [{"variable": "total", "type": "money(2)", "reads": ["net", "discount"], "formula": "<compiled>"}],
	  "access": {"create": "<compiled>", "update": "<compiled>", "delete": "<compiled>",
	             "transition": {"approve": "<compiled>", "pay": "<compiled>"}},
	  "guards": {"approve": "<compiled>", "submit": "<compiled>"},
	  "rights": {
	    "draft":     {"discount": {"write": {"groups": ["sales"], "cell": "* / discount"}}},
	    "submitted": {"discount": {"write": {"groups": ["sales"], "cell": "* / discount"}}},
	    "approved":  {"discount": {"read": {"groups": ["finance"], "cell": "approved / discount"},
	                               "write": {"groups": ["sales"], "cell": "* / discount"}}},
	    "paid":      {"discount": {"write": {"groups": ["sales"], "cell": "* / discount"}}}
	  },
	  "schemas": {}
	}`))
	if !reflect.DeepEqual(got, map[string]any(want)) {
		text, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("the compiled formulas of the sample invoice:\n%s", text)
	}
}
