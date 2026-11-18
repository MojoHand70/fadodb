package model

import (
	"strings"
	"testing"
)

// The invoice of ADR-0069, item 3: Acme's supplier, amounts and delivery,
// the address inside the delivery, and the notes in no section.
const sectionsInvoice = `{
  "name": "Invoice", "label": "Invoice", "description": "A supplier's invoice, from receipt to payment.",
  "variables": {
    "customer":     {"type": "text",  "label": "Supplier",   "list": true},
    "number":       {"type": "text",  "label": "Number",     "list": true, "required": true},
    "net":          {"type": "money", "label": "Net amount", "list": true},
    "vat":          {"type": "money", "label": "VAT"},
    "total":        {"type": "money", "label": "Total",      "formula": "net + vat"},
    "has_delivery": {"type": "bool",  "label": "Delivered to us"},
    "street":       {"type": "text",  "label": "Street"},
    "city":         {"type": "text",  "label": "City"},
    "notes":        {"type": "text",  "label": "Notes"}
  },
  "sections": {
    "supplier": {"label": "Supplier", "description": "Who sent the invoice.",
                 "variables": ["customer", "number"]},
    "amounts":  {"label": "Amounts",  "description": "What the invoice asks us to pay.",
                 "variables": ["net", "vat", "total"]},
    "delivery": {"label": "Delivery", "description": "Where the goods came to.",
                 "variables": ["has_delivery"],
                 "sections": {
                   "address": {"label": "Address", "description": "The address on the delivery note.",
                               "variables": ["street", "city"]}
                 }}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file",    "from": "draft",  "to": "review", "label": "Send for approval"},
    {"name": "approve", "from": "review", "to": "paid",   "label": "Approve"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"finance\") OR INGROUP(\"approvers\")",
    "create": "INGROUP(\"everyone\")",
    "update": "TRUE",
    "transition": {"file": "CREATEDBY() = USER()", "approve": "INGROUP(\"approvers\")"}
  },
  "rights": {
    "review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}
  }
}`

// The record's invoice defines, its outline reads as written, and the
// rights of review decide as the record's table says.
func TestSections_TheRecordsInvoice(t *testing.T) {
	cls := mustDefine(t, sectionsInvoice)
	var outline []string
	var walk func(prefix string, ss []*Section)
	walk = func(prefix string, ss []*Section) {
		for _, s := range ss {
			outline = append(outline, prefix+s.Name+"("+strings.Join(s.Variables, " ")+")")
			walk(prefix+s.Name+".", s.Sections)
		}
	}
	walk("", cls.Sections)
	want := "supplier(customer number) amounts(net vat total) delivery(has_delivery) delivery.address(street city)"
	if got := strings.Join(outline, " "); got != want {
		t.Errorf("the outline is\n%s\nwant\n%s", got, want)
	}
	if s, ok := cls.Section("ADDRESS"); !ok || s.Label != "Address" || s.Description != "The address on the delivery note." {
		t.Errorf("the address section by name: %+v", s)
	}
	if got := strings.Join(cls.SectionsOf("Street"), " "); got != "address delivery" {
		t.Errorf("the sections around street: %q", got)
	}
	if got := cls.SectionsOf("notes"); got != nil {
		t.Errorf("notes is in no section, got %v", got)
	}

	// Who sets what in review (ADR-0069, item 4).
	for _, c := range []struct {
		state, variable string
		groups, src     string
		cell            string
	}{
		{"review", "net", "finance", "", "review / amounts"},
		{"review", "total", "finance", "", "review / amounts"},
		{"review", "vat", "", "FALSE", "review / vat"},
		{"review", "street", "", "", ""},
		{"draft", "net", "", "", ""},
	} {
		rule := cls.WriteRule(c.state, c.variable)
		switch {
		case c.cell == "" && rule != nil:
			t.Errorf("%s in %s: no cell decides, got %s", c.variable, c.state, rule.Cell())
		case c.cell == "":
		case rule == nil:
			t.Errorf("%s in %s: no rule, want %s", c.variable, c.state, c.cell)
		case rule.Cell() != c.cell || strings.Join(rule.Groups, ",") != c.groups || rule.Groups == nil && rule.Src != c.src:
			t.Errorf("%s in %s: %s %v %q, want %s", c.variable, c.state, rule.Cell(), rule.Groups, rule.Src, c.cell)
		}
		if c.cell != "" && cls.ReadRule(c.state, c.variable) != nil {
			t.Errorf("%s in %s: a read rule where only a write is declared", c.variable, c.state)
		}
	}
}

// The most specific name wins: a variable over its section, a section
// over the one around it, the state's own row over the "*" state, and
// the state's "*" over a section of the "*" state.
func TestSections_MostSpecificWins(t *testing.T) {
	src := strings.Replace(sectionsInvoice, `"review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}`, `
	  "review": {"delivery": {"write": ["logistics"]}, "address": {"write": ["post"]}, "city": {"write": "TRUE"}},
	  "paid":   {"*": {"write": "FALSE"}},
	  "*":      {"amounts": {"read": ["finance"], "write": ["finance"]}, "*": {"read": "TRUE"}}`, 1)
	cls := mustDefine(t, src)
	for _, c := range []struct{ state, variable, write, read string }{
		{"review", "has_delivery", "review / delivery", "* / *"},
		{"review", "street", "review / address", "* / *"},
		{"review", "city", "review / city", "* / *"},
		{"review", "net", "* / amounts", "* / amounts"},
		{"paid", "net", "paid / *", "* / amounts"},
		{"draft", "notes", "", "* / *"},
	} {
		cell := func(r *RightRule) string {
			if r == nil {
				return ""
			}
			return r.Cell()
		}
		if got := cell(cls.WriteRule(c.state, c.variable)); got != c.write {
			t.Errorf("the write of %s in %s from %q, want %q", c.variable, c.state, got, c.write)
		}
		if got := cell(cls.ReadRule(c.state, c.variable)); got != c.read {
			t.Errorf("the read of %s in %s from %q, want %q", c.variable, c.state, got, c.read)
		}
	}
}

// What refuses an outline, each at its place.
func TestSections_Diagnostics(t *testing.T) {
	class := func(sections string) string {
		return `{"name": "T", "variables": {"a": {"type": "number"}, "b": {"type": "text"}, "c": {"type": "date"}},
		  "sections": ` + sections + `}`
	}
	deep := `{"s1": {"sections": {"s2": {"sections": {"s3": {"sections": {"s4": {"variables": ["a"], "sections": {"s5": {"variables": ["b"]}}}}}}}}}}`
	long := strings.Repeat("x", maxWords+1)
	for _, c := range []struct{ name, src, code, path string }{
		{"a variable in two sections", class(`{"x": {"variables": ["a"]}, "y": {"variables": ["b", "A"]}}`), CodeDuplicate, "/sections/y/variables/1"},
		{"a variable twice in one section", class(`{"x": {"variables": ["a", "b", "a"]}}`), CodeDuplicate, "/sections/x/variables/2"},
		{"a variable in a section and one inside it", class(`{"x": {"variables": ["a"], "sections": {"y": {"variables": ["a"]}}}}`), CodeDuplicate, "/sections/x/sections/y/variables/0"},
		{"a section with a variable's name", class(`{"A": {"variables": ["b"]}}`), CodeDuplicate, "/sections/A"},
		{"a section's name twice in the outline", class(`{"x": {"variables": ["a"], "sections": {"X": {"variables": ["b"]}}}}`), CodeDuplicate, "/sections/x/sections/X"},
		{"a fifth level", class(deep), CodeTooDeep, "/sections/s1/sections/s2/sections/s3/sections/s4/sections/s5"},
		{"a variable the class does not have", class(`{"x": {"variables": ["a", "zz"]}}`), CodeUnknownVariable, "/sections/x/variables/1"},
		{"an empty section", class(`{"x": {"label": "X"}}`), CodeMissing, "/sections/x"},
		{"an empty list", class(`{"x": {"variables": []}}`), CodeMissing, "/sections/x"},
		{"a null section", class(`{"x": null}`), CodeMissing, "/sections/x"},
		{"a name that is not an identifier", class(`{"1x": {"variables": ["a"]}}`), CodeBadName, "/sections/1x"},
		{"a word a section does not know", class(`{"x": {"variables": ["a"], "visible": "a > 1"}}`), CodeBadJSON, ""},
		{"a section's label past the limit", class(`{"x": {"variables": ["a"], "label": "` + long + `"}}`), CodeBadValue, "/sections/x/label"},
		{"a section's description past the limit", class(`{"x": {"variables": ["a"], "sections": {"y": {"variables": ["b"], "description": "` + long + `"}}}}`), CodeBadValue, "/sections/x/sections/y/description"},
		{"rights naming neither a variable nor a section", `{"name": "T", "variables": {"a": {"type": "number"}}, "sections": {"x": {"variables": ["a"]}},
		  "states": {"initial": "s", "list": ["s"]}, "rights": {"s": {"zz": {"write": "FALSE"}}}}`, CodeUnknownVariable, "/rights/s/zz"},
		{"rights naming a section twice in a state", `{"name": "T", "variables": {"a": {"type": "number"}}, "sections": {"x": {"variables": ["a"]}},
		  "states": {"initial": "s", "list": ["s"]}, "rights": {"s": {"x": {"write": "FALSE"}, "X": {"read": "TRUE"}}}}`, CodeDuplicate, "/rights/s/X"},
	} {
		diags := expectDiag(t, c.name, c.src, c.code, c.path)
		for _, d := range diags {
			if d.Code == c.code && d.Path == c.path && d.Code != CodeBadJSON && (d.Message == "" || d.Hint == "" && d.Code != CodeBadName && d.Code != CodeBadValue && d.Code != CodeDuplicate) {
				t.Errorf("%s: the finding says too little: %+v", c.name, d)
			}
		}
	}
	// Four levels define.
	mustDefine(t, class(`{"s1": {"sections": {"s2": {"sections": {"s3": {"sections": {"s4": {"variables": ["a"]}}}}}}}}`))
	// A variable refused for its own fault is not refused by the outline too.
	_, diags := Define([]byte(`{"name": "T", "variables": {"a": {"type": "number"}, "d": {"type": "nope"}},
	  "sections": {"x": {"variables": ["a", "d"]}}}`), nil)
	if len(diags) != 1 || diags[0].Path != "/variables/d/type" && diags[0].Path != "/variables/d" {
		t.Errorf("a variable with a bad type, in a section: %v", diags)
	}
}

// The outline keeps the written order, and every section without a
// description is noted, however deep (ADR-0069, item 5).
func TestSections_OrderAndNotes(t *testing.T) {
	cls := mustDefine(t, `{"name": "T", "variables": {
	    "a": {"type": "number", "description": "A."}, "b": {"type": "text", "description": "B."}, "c": {"type": "date", "description": "C."}},
	  "sections": {
	    "zeta":  {"variables": ["c", "a"], "description": "Last letter, first section."},
	    "alpha": {"sections": {"inner": {"variables": ["b"]}}}}}`)
	if len(cls.Sections) != 2 || cls.Sections[0].Name != "zeta" || cls.Sections[1].Name != "alpha" ||
		strings.Join(cls.Sections[0].Variables, " ") != "c a" || cls.Sections[1].Sections[0].Name != "inner" {
		t.Errorf("the written order: %+v %+v", cls.Sections[0], cls.Sections[1])
	}
	var paths []string
	for _, n := range cls.Notes {
		if n.Code == CodeNoDescription {
			if !n.Note || n.Hint == "" || !strings.Contains(n.Message, "section") {
				t.Errorf("a section's note: %+v", n)
			}
			paths = append(paths, n.Path)
		}
	}
	if got := strings.Join(paths, " "); got != "/sections/alpha/description /sections/alpha/sections/inner/description" {
		t.Errorf("the notes are at %s", got)
	}
	// A class without sections has none.
	if plain := mustDefine(t, variables(`"a": {"type": "number"}`)); plain.Sections != nil || plain.SectionsOf("a") != nil {
		t.Errorf("a class without an outline: %+v", plain.Sections)
	}
}
