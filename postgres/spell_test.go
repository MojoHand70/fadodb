package postgres

import (
	"database/sql/driver"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestRewrite(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		from     []source
	}{
		{"SELECT 1", "SELECT 1", nil},
		{"SELECT ?, ?", "SELECT $1, $2", []source{{place: 0}, {place: 1}}},
		{"SELECT @now, @user, @now", "SELECT $1, $2, $1", []source{{name: "now"}, {name: "user"}}},
		{"SELECT ? + @n + ?", "SELECT $1 + $2 + $3", []source{{place: 0}, {name: "n"}, {place: 1}}},
		// What is quoted stays.
		{"SELECT '?' || '@x' || 'it''s ?', ?", "SELECT '?' || '@x' || 'it''s ?', $1", []source{{place: 0}}},
		{`SELECT "a?b", "@c""?" FROM t WHERE x = ?`, `SELECT "a?b", "@c""?" FROM t WHERE x = $1`, []source{{place: 0}}},
		{`SELECT E'a\'?' , ?`, `SELECT E'a\'?' , $1`, []source{{place: 0}}},
		{"SELECT 1 -- what? @x\n, ?", "SELECT 1 -- what? @x\n, $1", []source{{place: 0}}},
		{"SELECT /* ? /* @x */ ? */ ?", "SELECT /* ? /* @x */ ? */ $1", []source{{place: 0}}},
		{"CREATE FUNCTION f() RETURNS text AS $fado$ SELECT '?' || $1 $fado$ -- @x", "CREATE FUNCTION f() RETURNS text AS $fado$ SELECT '?' || $1 $fado$ -- @x", nil},
		{"SELECT $$ ? @x $$, ?", "SELECT $$ ? @x $$, $1", []source{{place: 0}}},
		// A $n the statement has is left, and a $ inside a name.
		{"SELECT $1, a$b FROM t", "SELECT $1, a$b FROM t", nil},
		// The operators written with @ stay operators.
		{"SELECT a @> b, a <@ b, a @@ b, a <@c, @n", "SELECT a @> b, a <@ b, a @@ b, a <@c, $1", []source{{name: "n"}}},
		{"SELECT CAST(@v0 AS float8)::text, x::int", "SELECT CAST($1 AS float8)::text, x::int", []source{{name: "v0"}}},
	} {
		got := rewrite(tc.in)
		if got.text != tc.want || !reflect.DeepEqual(got.from, tc.from) {
			t.Errorf("%s\n  got  %s %v\n  want %s %v", tc.in, got.text, got.from, tc.want, tc.from)
		}
	}
}

func TestOrder(t *testing.T) {
	nv := func(name string, v any) driver.NamedValue { return driver.NamedValue{Name: name, Value: v} }
	values := func(args []driver.NamedValue) []any {
		out := make([]any, len(args))
		for i, a := range args {
			if a.Ordinal != i+1 || a.Name != "" {
				t.Errorf("argument %d goes on as ordinal %d, name %q", i+1, a.Ordinal, a.Name)
			}
			out[i] = a.Value
		}
		return out
	}
	// The arguments go in the order of the statement, one for a name
	// written twice, and what the statement does not name is left out.
	s := rewrite("SELECT @b, ?, @a, @b, ?")
	got, err := s.order([]driver.NamedValue{nv("", 1), nv("a", "A"), nv("unused", "U"), nv("b", "B"), nv("", 2)})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{"B", 1, "A", 2}; !reflect.DeepEqual(values(got), want) {
		t.Errorf("got %v, want %v", values(got), want)
	}
	if _, err := s.order([]driver.NamedValue{nv("", 1), nv("", 2), nv("a", "A")}); err == nil {
		t.Error("a name with no argument is taken")
	}
	if _, err := s.order([]driver.NamedValue{nv("", 1), nv("a", "A"), nv("b", "B")}); err == nil {
		t.Error("a ? with no argument is taken")
	}
	// A statement with nothing to spell takes its arguments as they are.
	plain := rewrite("SELECT $1, $2")
	in := []driver.NamedValue{{Ordinal: 1, Value: 1}, {Ordinal: 2, Value: 2}}
	if got, err := plain.order(in); err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("got %v, %v", got, err)
	}
	// And leaves out the named ones, which it has no place for.
	got, err = plain.order([]driver.NamedValue{nv("now", 0), {Ordinal: 2, Value: 1}, nv("user", "u"), {Ordinal: 4, Value: 2}})
	if err != nil || !reflect.DeepEqual(values(got), []any{1, 2}) {
		t.Errorf("got %v, %v", got, err)
	}
}

// The statements kept are bounded: more of them than are kept empty
// the store of them, and a long one is never kept.
func TestSpell_KeepsABoundedNumber(t *testing.T) {
	spellings.Clear()
	spelt.Store(0)
	for i := 0; i < 3*spellingsKept; i++ {
		q := "SELECT ? + " + strconv.Itoa(i)
		if got := spell(q); got.text != "SELECT $1 + "+strconv.Itoa(i) {
			t.Fatalf("%s spelled %s", q, got.text)
		}
	}
	kept := 0
	spellings.Range(func(any, any) bool { kept++; return true })
	if kept == 0 || kept > spellingsKept {
		t.Errorf("%d statements kept, want at most %d", kept, spellingsKept)
	}
	long := "SELECT ? /* " + strings.Repeat("x", spellingMax) + " */"
	spell(long)
	if _, ok := spellings.Load(long); ok {
		t.Error("a statement longer than the bound is kept")
	}
	if s := spell("SELECT @a, @a"); s != spell("SELECT @a, @a") {
		t.Error("a statement spelled twice is not kept")
	}
}
