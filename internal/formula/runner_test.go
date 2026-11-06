package formula

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The VM runner: every evaluation case in conformance/formula is bound and
// executed, and the value or the error code must match. This is the Go
// VM's half of "three evaluators, one behaviour".

func scopeFor(vars map[string]string) (*Scope, error) {
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	vs := make([]Variable, 0, len(names))
	for _, n := range names {
		t, err := ParseType(vars[n])
		if err != nil {
			return nil, fmt.Errorf("variable %s: %w", n, err)
		}
		vs = append(vs, Variable{Name: n, Type: t})
	}
	return NewScope(vs...)
}

func parseSuiteDate(s string) (int64, error) {
	layout := dateLayout
	if len(s) > len(dateLayout) {
		layout = dateTimeLayout
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// valueFromJSON reads a case value written in the suite notation as a
// value of the given type.
func valueFromJSON(raw json.RawMessage, t Type) (Value, error) {
	if raw == nil || string(raw) == "null" {
		return Blank(), nil
	}
	switch t.Kind {
	case KindNull:
		return Value{}, fmt.Errorf("a blank-typed result cannot be %s", raw)
	case KindNumber:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return Value{}, fmt.Errorf("want a number, got %s", raw)
		}
		return Number(f), nil
	case KindBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return Value{}, fmt.Errorf("want a bool, got %s", raw)
		}
		return Bool(b), nil
	case KindText, KindRef, KindJSON, KindDate:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return Value{}, fmt.Errorf("want text, got %s", raw)
		}
		switch t.Kind {
		case KindText:
			return Text(s), nil
		case KindRef:
			return Ref(s), nil
		case KindJSON:
			return JSON(s), nil
		}
		secs, err := parseSuiteDate(s)
		if err != nil {
			return Value{}, fmt.Errorf("want a date, got %s", raw)
		}
		return Date(secs), nil
	}
	return Value{}, fmt.Errorf("values of type %s are not supported by the runner yet", t)
}

func contextFor(c *CaseContext) (*Context, error) {
	if c == nil {
		return nil, nil
	}
	ctx := &Context{User: c.User, Groups: c.Groups}
	if c.Now != "" {
		secs, err := parseSuiteDate(c.Now)
		if err != nil {
			return nil, fmt.Errorf("context now: %w", err)
		}
		ctx.Now = secs
	}
	return ctx, nil
}

func TestConformance_VM(t *testing.T) {
	suites, err := LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM()
	total, failed := 0, 0
	for _, s := range suites {
		scope, err := scopeFor(s.Variables)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
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
				v, err := valueFromJSON(raw, slot.Type)
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
			ctx, err := contextFor(c.Context)
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
			want, err := valueFromJSON(c.Expected, prog.Result)
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
		scope, err := scopeFor(s.Variables)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		opts := BindOptions{}
		if s.Mode == "auto" {
			opts.Mode = ModeAuto
		}
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
