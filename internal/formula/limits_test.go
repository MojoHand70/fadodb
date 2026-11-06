package formula

import (
	"regexp"
	"strings"
	"testing"
)

// The text limit of ADR-0021: a VM evaluates under its database's limits,
// a nil VM under the defaults, the dialects pass the limit to every
// builder, and REGEXREPLACE's own replacement, built from the matches it
// counted, is the standard library's.

func TestLimits_VMReadsItsLimits(t *testing.T) {
	vm := NewVM()
	if vm.Limits.MaxText != DefaultMaxText || vm.maxText() != DefaultMaxText {
		t.Fatalf("NewVM limits = %+v, want MaxText %d", vm.Limits, DefaultMaxText)
	}
	var none *VM
	if none.maxText() != DefaultMaxText || (&VM{}).maxText() != DefaultMaxText {
		t.Error("a nil or zero VM must evaluate under the default limit")
	}
	scope, _ := NewScope(Variable{Name: "t", Type: Scalar(KindText)})
	prog, err := Bind(`t & REPEAT("x", 10)`, scope, BindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vm.Limits.MaxText = MinMaxText
	v, err := vm.Run(prog, []Value{Text(strings.Repeat("a", 54))}, nil)
	if err != nil || len(v.Str()) != 64 {
		t.Errorf("64 code points under a limit of 64: %s, %v", v, err)
	}
	_, err = vm.Run(prog, []Value{Text(strings.Repeat("a", 55))}, nil)
	if ee, ok := err.(*EvalError); !ok || ee.Code != ErrOutOfRange {
		t.Errorf("65 code points under a limit of 64: %v, want out_of_range", err)
	}
}

func TestLimits_DialectsPassTheLimit(t *testing.T) {
	scope, _ := NewScope(Variable{Name: "t", Type: Scalar(KindText)})
	for _, src := range []string{`REPEAT(t, 2)`, `PAD(t, 5)`, `SUBSTITUTE(t, "a", "b")`, `REPLACE(t, 1, 1, "x")`, `REGEXREPLACE(t, "a", "b")`, `t & t`, `CONCAT(t, 1, t)`} {
		prog, err := Bind(src, scope, BindOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []Dialect{NewSQLite().WithLimits(Limits{MaxText: 777}), NewDuckDB().WithLimits(Limits{MaxText: 777})} {
			stmt, err := GenerateSQL(prog, d, func(s Slot) string { return s.Name })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stmt.Value+stmt.Error, "777") {
				t.Errorf("%s on %s does not carry the limit:\n%s\n%s", src, d.Name(), stmt.Value, stmt.Error)
			}
			// The check is spelled out for a concatenation and lives in the
			// registered function or the macro for a builder.
			if !strings.Contains(stmt.Error, "'out_of_range'") && !strings.Contains(stmt.Error, "_err(") {
				t.Errorf("%s on %s has no out_of_range check: %q", src, d.Name(), stmt.Error)
			}
		}
	}
	if NewSQLite().Limits().MaxText != DefaultMaxText || NewDuckDB().Limits().MaxText != DefaultMaxText {
		t.Error("a new dialect must carry the default limits")
	}
	limited := 0
	for _, f := range SQLiteFunctions() {
		if f.Limited {
			limited++
			b := builtins[builtinByName[strings.ToUpper(strings.TrimPrefix(f.Name, "fado_"))]]
			if f.NArgs != len(b.args)+1 {
				t.Errorf("%s: NArgs %d, want %d plus the limit", f.Name, f.NArgs, len(b.args))
			}
		}
	}
	if limited != len(limitedFunctions) {
		t.Errorf("%d limited registrations, want %d", limited, len(limitedFunctions))
	}
}

func TestLimits_RegexReplaceMatchesTheStandardLibrary(t *testing.T) {
	vm := NewVM()
	texts := []string{"", "a", "abc", "aaa", "a1b22c", "hello world", "xaxbx", "żółw żółw", "ab", "abcab", "a.b.c"}
	patterns := []string{"", "a", "a*", "x*", "^", "$", ".", "(ab)+", "[0-9]+", `\s`, "o", "l+", "(?i)HELLO", `\.`, "[^a-z]", "^a|b$", "ż*", "(a|b)*"}
	for _, text := range texts {
		for _, pat := range patterns {
			for _, repl := range []string{"", "-", "<$1>", "xyz"} {
				got, err := fnRegexReplace(vm, []Value{Text(text), Text(pat), Text(repl)})
				if err != nil {
					t.Fatalf("%q %q %q: %v", text, pat, repl, err)
				}
				want := regexp.MustCompile(pat).ReplaceAllLiteralString(text, repl)
				if got.Str() != want {
					t.Errorf("REGEXREPLACE(%q, %q, %q) = %q, want %q", text, pat, repl, got.Str(), want)
				}
			}
		}
	}
}
