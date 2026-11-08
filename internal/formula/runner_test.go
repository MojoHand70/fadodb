package formula

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The VM runner: every evaluation case in conformance/formula is bound and
// executed, and the value or the error code must match. This is the Go
// VM's half of "three evaluators, one behaviour".

func TestConformance_VM(t *testing.T) {
	suites, err := LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	total, failed := 0, 0
	for _, s := range suites {
		scope, err := s.Scope()
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		vm.Limits = s.EvalLimits()
		for i, c := range s.Cases {
			total++
			label := fmt.Sprintf("%s[%d] %s", s.Suite, i, c.Formula)
			if c.Name != "" {
				label = fmt.Sprintf("%s[%d] %q: %s", s.Suite, i, c.Name, c.Formula)
			}
			fail := func(format string, args ...any) {
				failed++
				t.Errorf("%s\n    %s", label, fmt.Sprintf(format, args...))
			}
			prog, err := Bind(c.Formula, scope, BindOptions{})
			if err != nil {
				fail("bind: %v", err)
				continue
			}
			if c.Type != "" && prog.Result.String() != c.Type {
				fail("static type %s, want %s", prog.Result, c.Type)
				continue
			}
			vars := make([]Value, len(prog.Slots))
			bad := false
			for si, slot := range prog.Slots {
				raw, ok := c.Inputs[slot.Name]
				if !ok {
					continue
				}
				v, err := ParseCaseValue(raw, slot.Type)
				if err != nil {
					fail("input %s: %v", slot.Name, err)
					bad = true
					break
				}
				vars[si] = v
			}
			if bad {
				continue
			}
			ctx, err := c.Context.Context()
			if err != nil {
				fail("%v", err)
				continue
			}
			got, err := vm.Run(prog, vars, ctx)
			if c.Error != "" {
				if err == nil {
					fail("got %s, want error %s", got, c.Error)
					continue
				}
				ee, ok := err.(*EvalError)
				if !ok || ee.Code != c.Error {
					fail("got error %v, want %s", err, c.Error)
				}
				continue
			}
			if err != nil {
				fail("got error %v, want %s", err, string(c.Expected))
				continue
			}
			want, err := ParseCaseValue(c.Expected, prog.Result)
			if err != nil {
				fail("expected: %v", err)
				continue
			}
			if !Equal(got, want) {
				fail("got %s, want %s", got, want)
			}
		}
	}
	t.Logf("%d evaluation cases executed on the VM, %d failed", total, failed)
}

func TestConformance_Binding(t *testing.T) {
	suites, err := LoadBindingSuites(filepath.Join(conformanceDir, "binding"))
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) == 0 {
		t.Fatal("no binding suites found")
	}
	accepted, rejected := 0, 0
	for _, s := range suites {
		scope, err := s.Scope()
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		opts := BindOptions{Mode: s.BindMode()}
		for _, f := range s.Accept {
			accepted++
			if _, err := Bind(f, scope, opts); err != nil {
				t.Errorf("%s: accept %q: %v", s.File, f, err)
			}
		}
		for _, r := range s.Reject {
			rejected++
			if _, err := Bind(r.Formula, scope, opts); err == nil {
				t.Errorf("%s: reject %q (%s): bound without error", s.File, r.Formula, r.Reason)
			} else if _, ok := err.(*Error); !ok {
				t.Errorf("%s: reject %q: error %T is not a formula Error", s.File, r.Formula, err)
			} else if !strings.Contains(err.Error(), "at column") {
				t.Errorf("%s: reject %q: error %q has no column", s.File, r.Formula, err)
			}
		}
	}
	t.Logf("%d accept and %d reject binding cases in %d suites", accepted, rejected, len(suites))
}
