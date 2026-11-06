package sqlitefn

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The SQLite runner: every evaluation case is bound, translated to SQL for
// the SQLite dialect and executed by the pure-Go driver, and the value or
// the error code must match the case. With the VM runner this is the first
// VM = SQL differential test (ADR-0003).

const conformanceDir = "../../../conformance"

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// query builds one SELECT over a one-row subselect holding the inputs.
func query(prog *formula.Program, stmt formula.SQL) string {
	cols := make([]string, len(prog.Slots))
	for i, s := range prog.Slots {
		cols[i] = fmt.Sprintf(":v%d AS %q", i, s.Name)
	}
	from := "(SELECT 1)"
	if len(cols) > 0 {
		from = "(SELECT " + strings.Join(cols, ", ") + ")"
	}
	return "SELECT (" + stmt.Value + ") AS v, (" + stmt.ErrorOrNull() + ") AS e FROM " + from
}

// contextArgs binds the context parameters. The now parameter is always
// bound, with the wall clock when the case gives none, as the VM does.
func contextArgs(ctx *formula.Context) []any {
	var user, groups any
	now := any(time.Now().Unix())
	if ctx != nil {
		if ctx.Now != 0 {
			now = ctx.Now
		}
		if ctx.User != "" {
			user = ctx.User
		}
		if ctx.Groups != nil {
			b, _ := json.Marshal(ctx.Groups)
			groups = string(b)
		}
	}
	return []any{sql.Named("now", now), sql.Named("user", user), sql.Named("groups", groups)}
}

func TestConformance_SQLite(t *testing.T) {
	suites, err := formula.LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	column := func(s formula.Slot) string { return fmt.Sprintf("%q", s.Name) }
	total, failed := 0, 0
	for _, s := range suites {
		scope, err := formula.ScopeOf(s.Variables)
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
			prog, err := formula.Bind(c.Formula, scope, formula.BindOptions{})
			if err != nil {
				fail("bind: %v", err)
				continue
			}
			stmt, err := formula.GenerateSQL(prog, formula.SQLite{}, column)
			if err != nil {
				fail("generate: %v", err)
				continue
			}
			args := make([]any, 0, len(prog.Slots)+3)
			bad := false
			for si, slot := range prog.Slots {
				var v formula.Value
				if raw, ok := c.Inputs[slot.Name]; ok {
					v, err = formula.ParseCaseValue(raw, slot.Type)
					if err != nil {
						fail("input %s: %v", slot.Name, err)
						bad = true
						break
					}
				}
				args = append(args, sql.Named(fmt.Sprintf("v%d", si), v.DriverValue()))
			}
			if bad {
				continue
			}
			ctx, err := c.Context.Context()
			if err != nil {
				fail("%v", err)
				continue
			}
			args = append(args, contextArgs(ctx)...)
			q := query(prog, stmt)
			var v, e any
			if err := db.QueryRow(q, args...).Scan(&v, &e); err != nil {
				fail("sqlite: %v\n    %s", err, q)
				continue
			}
			if c.Error != "" {
				if code, _ := e.(string); code != c.Error {
					fail("got error %v (value %v), want %s\n    %s", e, v, c.Error, q)
				}
				continue
			}
			if e != nil {
				fail("got error %v, want %s\n    %s", e, string(c.Expected), q)
				continue
			}
			want, err := formula.ParseCaseValue(c.Expected, prog.Result)
			if err != nil {
				fail("expected: %v", err)
				continue
			}
			got := formula.ValueFromDriver(v, prog.Result.Kind)
			if !formula.Equal(got, want) {
				fail("got %s (%T %v), want %s\n    %s", got, v, v, want, q)
			}
		}
	}
	t.Logf("%d evaluation cases executed on SQLite, %d failed", total, failed)
}

func TestRegister_IsIdempotent(t *testing.T) {
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	var got float64
	if err := db.QueryRow("SELECT fado_round(2.675, 2)").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 2.68 {
		t.Errorf("fado_round(2.675, 2) = %v, want 2.68", got)
	}
	var code any
	if err := db.QueryRow("SELECT fado_value_err('abc')").Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "bad_text" {
		t.Errorf("fado_value_err('abc') = %v, want bad_text", code)
	}
}
