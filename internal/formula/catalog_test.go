package formula

import (
	"strings"
	"testing"
)

func TestCatalog_Loads(t *testing.T) {
	fns := Functions()
	if len(fns) < 60 {
		t.Fatalf("catalog has %d functions, expected at least 60", len(fns))
	}
	classes := Classes()
	for _, c := range []Class{ClassPure, ClassContext, ClassLookup, ClassQuery} {
		if classes[c] == "" {
			t.Errorf("class %s is not described", c)
		}
	}
	seen := map[Class]bool{}
	for _, fn := range fns {
		seen[fn.Class] = true
	}
	for c := range classes {
		if !seen[c] {
			t.Errorf("class %s has no function", c)
		}
	}
}

func TestCatalog_LookupIsCaseInsensitiveAndKnowsAliases(t *testing.T) {
	for name, want := range map[string]string{
		"round": "ROUND", "Round": "ROUND", "CEIL": "CEILING", "ceil": "CEILING",
		"find": "FINDFIRST", "if": "IF", "lookup": "LOOKUP",
	} {
		fn := LookupFunction(name)
		if fn == nil {
			t.Errorf("LookupFunction(%q) = nil", name)
			continue
		}
		if fn.Name != want {
			t.Errorf("LookupFunction(%q).Name = %s, want %s", name, fn.Name, want)
		}
	}
	if LookupFunction("NOSUCH") != nil {
		t.Error("LookupFunction(NOSUCH) should be nil")
	}
}

func TestCatalog_Arity(t *testing.T) {
	cases := []struct {
		name     string
		min, max int
	}{
		{"IF", 3, 3}, {"ROUND", 1, 2}, {"MIN", 1, -1}, {"NOW", 0, 0},
		{"MID", 2, 3}, {"QUERY", 2, 6}, {"LOOKUP", 3, 3}, {"AND", 1, -1},
	}
	for _, c := range cases {
		fn := LookupFunction(c.name)
		if fn == nil {
			t.Fatalf("%s missing from the catalog", c.name)
		}
		if fn.MinArgs() != c.min || fn.MaxArgs() != c.max {
			t.Errorf("%s: arity %d..%d, want %d..%d", c.name, fn.MinArgs(), fn.MaxArgs(), c.min, c.max)
		}
	}
}

func TestCatalog_ArityMessagesNameTheFunctionAsSpelled(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want string
	}{
		{"find", 1, "FIND takes 2 arguments, got 1"},
		{"FINDFIRST", 1, "FINDFIRST takes 2 arguments, got 1"},
		{"round", 3, "ROUND takes 1 to 2 arguments, got 3"},
		{"MIN", 0, "MIN takes at least 1 argument, got 0"},
		{"NOW", 1, "NOW takes no arguments, got 1"},
		{"TRIM", 0, "TRIM takes 1 argument, got 0"},
	}
	for _, c := range cases {
		err := LookupFunction(c.name).checkArity(c.name, c.got)
		if err == nil || err.Error() != c.want {
			t.Errorf("checkArity(%s, %d) = %v, want %q", c.name, c.got, err, c.want)
		}
	}
	if err := LookupFunction("MIN").checkArity("MIN", 5); err != nil {
		t.Errorf("MIN with 5 arguments: %v", err)
	}
}

func TestCatalog_Signatures(t *testing.T) {
	for name, want := range map[string]string{
		"ROUND": "ROUND(number[, digits])",
		"MIN":   "MIN(value[, value, ...])",
		"NOW":   "NOW()",
		"MID":   "MID(text, position[, length])",
		"CEIL":  "CEIL(number[, digits])",
	} {
		if got := LookupFunction(name).Signature(name); got != want {
			t.Errorf("Signature(%s) = %s, want %s", name, got, want)
		}
	}
}

func TestCatalog_RejectsMalformedEntries(t *testing.T) {
	bad := map[string]string{
		"lower case name":         `{"version":1,"classes":{"pure":"x"},"functions":[{"name":"abs","class":"pure","category":"n","params":[],"returns":"number","summary":"s"}]}`,
		"unknown class":           `{"version":1,"classes":{"pure":"x"},"functions":[{"name":"ABS","class":"magic","category":"n","params":[],"returns":"number","summary":"s"}]}`,
		"duplicate alias":         `{"version":1,"classes":{"pure":"x"},"functions":[{"name":"ABS","aliases":["ABS"],"class":"pure","category":"n","params":[],"returns":"number","summary":"s"}]}`,
		"required after optional": `{"version":1,"classes":{"pure":"x"},"functions":[{"name":"F","class":"pure","category":"n","params":[{"name":"a","type":"number","optional":true},{"name":"b","type":"number"}],"returns":"number","summary":"s"}]}`,
		"repeat not last":         `{"version":1,"classes":{"pure":"x"},"functions":[{"name":"F","class":"pure","category":"n","params":[{"name":"a","type":"number","repeat":true},{"name":"b","type":"number","optional":true}],"returns":"number","summary":"s"}]}`,
		"unknown field":           `{"version":1,"classes":{"pure":"x"},"functions":[{"name":"F","class":"pure","category":"n","params":[],"returns":"number","summary":"s","extra":1}]}`,
		"wrong version":           `{"version":2,"classes":{},"functions":[]}`,
	}
	for name, raw := range bad {
		if _, err := loadCatalog([]byte(raw)); err == nil {
			t.Errorf("%s: loaded without error", name)
		} else if !strings.HasPrefix(err.Error(), "catalog.json") {
			t.Errorf("%s: error %q should name catalog.json", name, err)
		}
	}
}
