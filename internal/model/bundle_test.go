package model

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The tags the words of a language are looked for under, as RFC 4647
// truncates them: a subtag of one letter is never left last.
func TestBundle_LanguageLookup(t *testing.T) {
	for tag, want := range map[string]string{
		"pl":                 "pl",
		"pt-BR":              "pt-BR pt",
		"zh-Hant-TW":         "zh-Hant-TW zh-Hant zh",
		"en-US-u-ca-gregory": "en-US-u-ca-gregory en-US-u-ca en-US en",
		"en-x-acme":          "en-x-acme en",
		"de-CH-1901":         "de-CH-1901 de-CH de",
	} {
		if got := strings.Join(LanguageLookup(tag), " "); got != want {
			t.Errorf("%s is looked for as %s, want %s", tag, got, want)
		}
	}
	if got, why := LanguageTag("PT-br"); got != "pt-BR" || why != "" {
		t.Errorf("PT-br reads as %q (%s)", got, why)
	}
}

// The words put in place in a class keep the order it was written in,
// write a column of the short form in the long one, reach a section
// however deep, give the states labels where they had none, and leave
// out what the class does not have.
func TestBundle_PutClassWords(t *testing.T) {
	src := `{"name": "Order", "label": "Order",
	  "variables": {"lines": {"type": "rows", "label": "Lines", "schema": {"sku": "text", "qty": {"type": "int", "label": "Quantity"}}},
	                "street": {"type": "text"}, "city": {"type": "text", "label": "City"}},
	  "sections": {"delivery": {"label": "Delivery", "variables": ["lines"],
	                            "sections": {"address": {"label": "Address", "variables": ["street", "city"]}}}},
	  "states": {"initial": "open", "list": ["open", "done"], "descriptions": {"open": "Being filled in."}},
	  "transitions": [{"name": "close", "from": "open", "to": "done", "label": "Close"}]}`
	cls := mustDefine(t, src)
	words, diags := ParseWords([]byte(`{"class": "Order", "language": "pl", "label": "Zamówienie",
	  "variables": {"LINES.sku": {"label": "Kod"}, "lines.qty": {"description": "Ile sztuk."}, "street": {"label": "Ulica"}},
	  "sections": {"address": {"label": "Adres"}},
	  "states": {"open": {"label": "Otwarte", "description": "Wypełniane."}},
	  "transitions": {"CLOSE": {"label": "Zamknij"}}}`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if diags := CheckWords(words, WordsTarget{Class: cls}); len(diags) > 0 {
		t.Fatal(diags)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(src)); err != nil {
		t.Fatal(err)
	}
	out, err := PutClassWords(compact.Bytes(), cls, words)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"Order","label":"Zamówienie",` +
		`"variables":{"lines":{"type":"rows","label":"Lines","schema":{"sku":{"type":"text","label":"Kod"},"qty":{"type":"int","label":"Quantity","description":"Ile sztuk."}}},` +
		`"street":{"type":"text","label":"Ulica"},"city":{"type":"text","label":"City"}},` +
		`"sections":{"delivery":{"label":"Delivery","variables":["lines"],"sections":{"address":{"label":"Adres","variables":["street","city"]}}}},` +
		`"states":{"initial":"open","list":["open","done"],"descriptions":{"open":"Wypełniane."},"labels":{"open":"Otwarte"}},` +
		`"transitions":[{"name":"close","from":"open","to":"done","label":"Zamknij"}]}`
	if string(out) != want {
		t.Errorf("the class in Polish:\n%s\nwant\n%s", out, want)
	}
	// The class with its words put in place is still a class the engine
	// takes, the same but for what it says to people.
	again := mustDefine(t, string(out))
	if again.Label != "Zamówienie" || len(again.Variables) != len(cls.Variables) {
		t.Errorf("the class in Polish defines %+v", again)
	}
	// Words of a path the class no longer has are left out.
	smaller := mustDefine(t, `{"name": "Order", "variables": {"city": {"type": "text"}}}`)
	if out, err := PutClassWords([]byte(`{"name":"Order","variables":{"city":{"type":"text"}}}`), smaller, words); err != nil || string(out) != `{"name":"Order","variables":{"city":{"type":"text"}},"label":"Zamówienie"}` {
		t.Errorf("the words of a smaller class: %v %s", err, out)
	}
}

// The words of a schema's fields, a field of the short form written in
// the long one.
func TestBundle_PutSchemaWords(t *testing.T) {
	words, _ := ParseWords([]byte(`{"schema": "Address", "language": "pl", "fields": {"CITY": {"label": "Miasto"}, "line1": {"label": "Ulica"}}}`))
	out, err := PutSchemaWords([]byte(`{"name":"Address","fields":{"line1":"text","city":{"type":"text","label":"City"}}}`), words)
	if err != nil || string(out) != `{"name":"Address","fields":{"line1":{"type":"text","label":"Ulica"},"city":{"type":"text","label":"Miasto"}}}` {
		t.Errorf("the schema in Polish: %v %s", err, out)
	}
}

// A formula that reads the history of the document's states is the
// server's: its stays are not on the wire. Only a guard or a rule may
// read it; a formula of a variable that works itself out may not.
func TestBundle_TheServersFormulas(t *testing.T) {
	cls := mustDefine(t, `{"name": "Leave", "variables": {
	    "days":     {"type": "int"},
	    "twice":    {"formula": "days * 2"}},
	  "states": {"initial": "asked", "list": ["asked", "approved"]},
	  "transitions": [{"name": "approve", "from": "asked", "to": "approved", "guard": "ENTRIES(\"asked\") < 3"}]}`)
	c := Compiled(cls)
	byName := map[string]BundleFormula{}
	for _, f := range c.Computed {
		byName[f.Variable] = f
	}
	if f := byName["twice"]; f.Server || f.Formula == "" || !reflect.DeepEqual(f.Reads, []string{"days"}) {
		t.Errorf("a formula the page works out: %+v", f)
	}
	if g := c.Guards["approve"]; g == nil || !g.Server {
		t.Errorf("a guard that reads the history: %+v", g)
	}
	if text, _ := json.Marshal(c.Guards["approve"]); string(text) != `{"server":true,"reads":[]}` {
		t.Errorf("the guard is written %s", text)
	}
}

// The guard of a transition that a value is brought before is the
// server's (ADR-0078, item 4): the page holds the value of an earlier
// version, and the move decides on the one it brings. A guard beside it,
// of a transition nothing is brought before, is the page's as ever.
func TestBundle_AGuardOverABroughtValueIsTheServers(t *testing.T) {
	cls := mustDefine(t, `{"name": "Bid", "variables": {
	    "price": {"type": "number"},
	    "floor": {"type": "number", "formula": "price / 2", "mode": "triggered", "on": ["before:take"]}},
	  "states": {"initial": "open", "list": ["open", "taken", "dropped"]},
	  "transitions": [{"name": "take", "from": "open", "to": "taken", "guard": "price >= floor"},
	                  {"name": "drop", "from": "open", "to": "dropped", "guard": "price < 1"}]}`)
	c := Compiled(cls)
	if text, _ := json.Marshal(c.Guards["take"]); string(text) != `{"server":true,"reads":["price","floor"]}` {
		t.Errorf("the guard of a transition a value is brought before is written %s", text)
	}
	if g := c.Guards["drop"]; g == nil || g.Server || g.Formula == "" {
		t.Errorf("the guard of a transition nothing is brought before: %+v", g)
	}
}
