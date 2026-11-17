package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// What a class says to people (ADR-0068): the invoice of the record,
// whole, defines with nothing refused, and is told only of what it leaves
// undescribed.
func TestPeople_TheRecordsInvoice(t *testing.T) {
	cls := mustDefine(t, `{
	  "name": "Invoice", "label": "Invoice", "description": "A supplier's invoice, from receipt to payment.",
	  "variables": {
	    "number":   {"type": "text", "required": true, "label": "Number", "list": true,
	                 "description": "The supplier's own number, as printed on the invoice."},
	    "customer": {"type": "text", "label": "Supplier", "list": true},
	    "net":      {"type": "money", "scale": 2, "label": "Net amount", "list": true, "format": "#,##0.00",
	                 "description": "The amount before VAT, as on the supplier's invoice."},
	    "due":      {"type": "date", "label": "Due date", "list": true, "format": "d mmm yyyy"},
	    "lines":    {"type": "rows", "label": "Lines", "schema": {
	                   "sku":   {"type": "text",  "label": "Product code"},
	                   "qty":   {"type": "int",   "label": "Quantity"},
	                   "price": {"type": "money", "label": "Unit price", "format": "#,##0.00"}}}
	  },
	  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"],
	             "labels": {"draft": "Draft", "review": "In review", "paid": "Paid"},
	             "descriptions": {"review": "Waiting for the approver named on the invoice."}},
	  "transitions": [{"name": "approve", "from": "review", "to": "paid", "label": "Approve",
	                   "description": "The approver accepts the invoice for payment."}],
	  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
	}`)
	var paths []string
	for _, n := range cls.Notes {
		if n.Code != CodeNoDescription || !n.Note || n.Hint == "" {
			t.Errorf("a note: %+v", n)
		}
		paths = append(paths, n.Path)
	}
	want := "/variables/customer/description /variables/due/description /variables/lines/description /states/descriptions/draft /states/descriptions/paid"
	if got := strings.Join(paths, " "); got != want {
		t.Errorf("the notes are at\n%s\nwant\n%s", got, want)
	}
	if v, _ := cls.Variable("net"); v.Def.Label != "Net amount" || !v.Def.List || v.Def.Format != "#,##0.00" {
		t.Errorf("net as written: %+v", v.Def)
	}
	// A nameless transition is noted by where it goes.
	nameless := mustDefine(t, `{"name": "N", "variables": {"a": {"type": "number", "description": "A number."}},
	  "states": {"initial": "s", "list": ["s", "t"], "descriptions": {"s": "First.", "t": "Last."}},
	  "transitions": [{"from": "s", "to": "t", "guard": "a > 1"}],
	  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`)
	if len(nameless.Notes) != 1 || nameless.Notes[0].Path != "/transitions/0/description" || !strings.Contains(nameless.Notes[0].Message, "the transition to t") {
		t.Errorf("a nameless transition's note: %v", nameless.Notes)
	}
}

// The formats the engine reads, and the ones it refuses with the place in
// the pattern (ADR-0068, item 5).
func TestPeople_Formats(t *testing.T) {
	number, date, moment := formula.Scalar(formula.KindNumber), formula.Scalar(formula.KindDate), formula.Scalar(formula.KindDateTime)
	money := formula.Scalar(formula.KindMoney)
	for _, ok := range []struct {
		t       formula.Type
		pattern string
	}{
		{number, "0"}, {number, "#,##0.00"}, {number, "0%"}, {number, "0.0%"}, {number, "#,##0;(#,##0)"},
		{number, `0;-0;"none"`}, {money, `#,##0.00 "zł"`}, {money, "$#,##0.00"}, {number, "#,##0;;"},
		{date, "d mmm yyyy"}, {date, "dd.mm.yyyy"}, {date, "yyyy-mm-dd"}, {date, "dddd, d mmmm yyyy"},
		{date, `d "de" mmmm "de" yyyy`}, {date, "m/d/yy"},
		{moment, "yyyy-mm-dd hh:mm"}, {moment, "h:mm AM/PM"}, {moment, "dd.mm.yyyy hh:mm:ss"}, {moment, "hh:mm"},
	} {
		var c collector
		checkFormat(&c, "/f", ok.pattern, ok.t)
		if c.failed() {
			t.Errorf("%s %q is refused: %v", ok.t, ok.pattern, c.list)
		}
	}
	for _, bad := range []struct {
		t       formula.Type
		pattern string
		pos     int
		says    string
	}{
		{number, "#,##0,", 5, "between digit places"},
		{number, ",##0", 0, "between digit places"},
		{number, "0.0.0", 3, "one decimal point"},
		{number, "[Red]0", 0, "brackets"},
		{number, "0.00E+00", 4, "scientific"},
		{number, "General", 0, `"G"`},
		{number, "0;0;0;0", 5, "three sections"},
		{number, `0 "pcs`, 2, "not closed"},
		{number, ";0", 0, "first section"},
		{number, `"none"`, 0, "no digit place"},
		{number, "0 ł", 2, `"ł"`},
		{date, "yyyy-mm-dd hh:mm", 11, "no time of day"},
		{date, "h:mm AM/PM", 0, "no time of day"},
		{date, "yyy", 0, "yy or yyyy"},
		{date, "ddddd", 0, "d, dd, ddd or dddd"},
		{date, "mmmmm", 0, "m, mm, mmm or mmmm"},
		{date, "#,##0", 0, `"#"`},
		{moment, "hhh", 0, "h or hh"},
		{moment, "hh:mm:s", 6, "ss"},
		{moment, "hh:mmm", 3, "m or mm"},
		{moment, `"at" hh`, 5, ""},
		{date, `"x"`, 0, "no part of the date"},
	} {
		var c collector
		checkFormat(&c, "/f", bad.pattern, bad.t)
		if bad.says == "" {
			if c.failed() {
				t.Errorf("%s %q is refused: %v", bad.t, bad.pattern, c.list)
			}
			continue
		}
		if len(c.list) != 1 || c.list[0].Code != CodeBadValue || c.list[0].Pos != bad.pos || !strings.Contains(c.list[0].Message, bad.says) || c.list[0].Hint == "" {
			t.Errorf("%s %q: %v, want bad_value at offset %d saying %s", bad.t, bad.pattern, c.list, bad.pos, bad.says)
		}
	}
	// A format belongs to numbers and moments.
	for _, kind := range []formula.Kind{formula.KindText, formula.KindBool, formula.KindRef, formula.KindJSON} {
		var c collector
		checkFormat(&c, "/f", "0", formula.Scalar(kind))
		if len(c.list) != 1 || c.list[0].Code != CodeNotAllowed {
			t.Errorf("a format on %s: %v", kind, c.list)
		}
	}
}

// What refuses a class: a format on a text, a format the engine does not
// read, a state's words for a state the class does not have, a column's
// long form with a word the format does not know, and a label past the
// text limit.
func TestPeople_Diagnostics(t *testing.T) {
	states := `"states": {"initial": "a", "list": ["a"], `
	long := strings.Repeat("x", maxWords+1)
	for _, c := range []struct{ name, src, code, path string }{
		{"format on a text", variables(`"a": {"type": "text", "format": "0"}`), CodeNotAllowed, "/variables/a/format"},
		{"format on an array", variables(`"a": {"type": "array<number>", "format": "0"}`), CodeNotAllowed, "/variables/a/format"},
		{"format the engine does not read", variables(`"a": {"type": "number", "format": "0.00E+00"}`), CodeBadValue, "/variables/a/format"},
		{"format of a computed variable, by its formula's type", variables(`"a": {"type": "date"}, "b": {"formula": "a", "format": "0.00"}`), CodeBadValue, "/variables/b/format"},
		{"label of an unknown state", `{"name": "T", "variables": {"n": {"type": "number"}}, ` + states + `"labels": {"zz": "Zz"}}}`, CodeUnknownState, "/states/labels/zz"},
		{"description of an unknown state", `{"name": "T", "variables": {"n": {"type": "number"}}, ` + states + `"descriptions": {"zz": "Zz"}}}`, CodeUnknownState, "/states/descriptions/zz"},
		{"a word the long form does not know", variables(`"l": {"type": "rows", "schema": {"q": {"type": "int", "caption": "Q"}}}`), CodeBadJSON, "/variables/l/schema"},
		{"a column's format on a text", variables(`"l": {"type": "rows", "schema": {"q": {"type": "text", "format": "0"}}}`), CodeNotAllowed, "/variables/l/schema/q/format"},
		{"a column's long form without a type", variables(`"l": {"type": "rows", "schema": {"q": {"label": "Q"}}}`), CodeUnknownType, "/variables/l/schema/q"},
		{"a label past the limit", variables(`"a": {"type": "number", "label": "` + long + `"}`), CodeBadValue, "/variables/a/label"},
		{"a description past the limit", `{"name": "T", "description": "` + long + `", "variables": {"a": {"type": "number"}}}`, CodeBadValue, "/description"},
		{"a transition's label past the limit", `{"name": "T", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b", "label": "` + long + `"}]}`, CodeBadValue, "/transitions/0/label"},
	} {
		expectDiag(t, c.name, c.src, c.code, c.path)
	}
}

// The long form of a column of an explicit table and of a field of a
// schema (ADR-0068, item 2), and the short form written back as it was.
func TestPeople_LongForms(t *testing.T) {
	def, diags := ParseTable([]byte(`{"name": "Rates", "label": "Rates", "columns": {
	  "code": {"type": "text", "label": "Currency", "description": "ISO 4217."},
	  "rate": {"type": "decimal(6)", "label": "Rate", "format": "0.000000"},
	  "day":  "date"}, "key": ["code"]}`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if _, diags := CompileTable(def); len(diags) > 0 {
		t.Fatalf("a table with long columns: %v", diags)
	}
	if def.Columns["rate"].Type != "decimal(6)" || def.Columns["code"].Label != "Currency" || def.Columns["day"].Type != "date" {
		t.Errorf("the columns as read: %+v", def.Columns)
	}
	bad, _ := ParseTable([]byte(`{"name": "R", "columns": {"code": {"type": "text", "format": "0"}}, "key": ["code"]}`))
	if _, diags := CompileTable(bad); !diags.Has(CodeNotAllowed) || diags[0].Path != "/columns/code/format" {
		t.Errorf("a format on a text column: %v", diags)
	}

	s := NewSchemaSet()
	sdef, diags := ParseSchema([]byte(`{"name": "Line", "fields": {
	  "sku": {"type": "text", "label": "Product code"},
	  "qty": {"type": "int", "label": "Quantity", "format": "#,##0"},
	  "at":  {"type": "object<Address>", "label": "Delivered to"}}}`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if _, diags := s.Define(sdef); len(diags) > 0 {
		t.Fatalf("a schema with long fields: %v", diags)
	}
	odd, _ := ParseSchema([]byte(`{"name": "Odd", "fields": {"at": {"type": "object<Address>", "format": "0"}}}`))
	if _, diags := s.Define(odd); !diags.Has(CodeNotAllowed) {
		t.Errorf("a format on an object field: %v", diags)
	}

	for _, c := range []struct {
		f    FieldDef
		want string
	}{
		{FieldDef{Type: "int"}, `"int"`},
		{FieldDef{Type: "int", Label: "Quantity"}, `{"type":"int","label":"Quantity"}`},
	} {
		got, err := json.Marshal(c.f)
		if err != nil || string(got) != c.want {
			t.Errorf("%+v is written %s, want %s", c.f, got, c.want)
		}
	}
}
