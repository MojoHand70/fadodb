package formula

import (
	"errors"
	"strings"
	"testing"
)

func quoteSlot(s Slot) string { return `"` + s.Name + `"` }

func TestGenerateSQL_Shapes(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)}, Variable{Name: "name", Type: Scalar(KindText)},
		Variable{Name: "due", Type: Scalar(KindDate)}, Variable{Name: "paid", Type: Scalar(KindBool)})
	cases := []struct {
		src       string
		wantValue []string
		wantError []string
		noError   bool
		params    []string
	}{
		{src: "qty * 2", wantValue: []string{`(COALESCE(CAST("qty" AS REAL), 0.0) * 2.0)`}, wantError: []string{"abs(", "'not_a_number'"}},
		{src: "qty / 0", wantValue: []string{"CASE WHEN 0.0 = 0.0 THEN NULL ELSE"}, wantError: []string{"'div_by_zero'", "'not_a_number'"}},
		{src: `qty \ 2`, wantError: []string{"'div_by_zero'"}},
		{src: `name & qty`, wantValue: []string{`substr(COALESCE("name", '') || fado_text(CAST("qty" AS REAL)), 1, 65537)`}, wantError: []string{`length(COALESCE("name", ''))`, "> 65536 THEN 'out_of_range'"}},
		{src: `REPEAT(name, 2)`, wantValue: []string{`fado_repeat(COALESCE("name", ''), 2.0, 65536)`}, wantError: []string{`fado_repeat_err(COALESCE("name", ''), 2.0, 65536)`}},
		{src: `LEFT(name, 1e10)`, wantValue: []string{`substr(COALESCE("name", ''), 1, CAST(min(max(trunc(1e+10), -1), 1000000000 + 1) AS INTEGER))`}, wantError: []string{`< 0 THEN 'out_of_range'`}},
		{src: `name = "x"`, wantValue: []string{`(COALESCE("name", '') = 'x')`}, noError: true},
		{src: `qty = BLANK`, wantValue: []string{`(COALESCE(CAST("qty" AS REAL), 0.0) = 0.0)`}, noError: true},
		{src: `IF(paid, 1, 2)`, wantValue: []string{`(CASE WHEN COALESCE("paid", 0) THEN 1.0 ELSE 2.0 END)`}, noError: true},
		{src: `IF(paid, 1 \ qty, 0)`, wantError: []string{`CASE WHEN COALESCE("paid", 0) THEN COALESCE(CASE WHEN COALESCE(CAST("qty" AS REAL), 0.0) = 0.0 THEN 'div_by_zero' END, CASE WHEN`, `'not_a_number' END) END`}},
		{src: `paid OR 1 \ qty > 0`, wantError: []string{`CASE WHEN NOT COALESCE("paid", 0) THEN`}},
		{src: `paid AND NOT ISBLANK(name)`, wantValue: []string{`(COALESCE("paid", 0) AND (NOT ("name" IS NULL)))`}, noError: true},
		{src: `ROUND(qty, 2)`, wantValue: []string{`fado_round(COALESCE(CAST("qty" AS REAL), 0.0), 2.0, 'half_up')`}, noError: true},
		{src: `ROUND(qty, 2, "half_even")`, wantValue: []string{`fado_round(COALESCE(CAST("qty" AS REAL), 0.0), 2.0, 'half_even')`}, noError: true},
		{src: `VALUE(name)`, wantValue: []string{`fado_value(COALESCE("name", ''))`}, wantError: []string{`fado_value_err(COALESCE("name", ''))`}},
		{src: `DAYS(due, TODAY())`, wantValue: []string{`(CAST("due" AS REAL) - floor(CAST(:now AS REAL) / 86400.0))`}, noError: true, params: []string{"now"}},
		{src: `USER() = "a" AND INGROUP("g")`, wantValue: []string{`:user`, `instr(:groups, char(31)`}, noError: true, params: []string{"user", "groups"}},
		{src: `STATE() = "draft" AND ID() <> ""`, wantValue: []string{`(COALESCE(:state, '') = 'draft')`, `(COALESCE(:id, '') <> '')`}, noError: true, params: []string{"state", "id"}},
		{src: `TEXT(due)`, wantValue: []string{`strftime('%Y-%m-%d', CAST("due" AS REAL) * 86400.0, 'unixepoch')`}, noError: true},
		{src: `"it's"`, wantValue: []string{`'it''s'`}, noError: true},
		{src: `-5 ^ 2`, wantValue: []string{`fado_power((-5.0), 2.0)`}, wantError: []string{"IS NULL OR abs("}},
	}
	for _, c := range cases {
		p := mustBind(t, c.src, scope)
		stmt, err := GenerateSQL(p, NewSQLite(), quoteSlot)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		for _, w := range c.wantValue {
			if !strings.Contains(stmt.Value, w) {
				t.Errorf("%s: value %s\n  lacks %s", c.src, stmt.Value, w)
			}
		}
		for _, w := range c.wantError {
			if !strings.Contains(stmt.Error, w) {
				t.Errorf("%s: error %s\n  lacks %s", c.src, stmt.Error, w)
			}
		}
		if c.noError && stmt.Error != "" {
			t.Errorf("%s: error expression %s, want none", c.src, stmt.Error)
		}
		if len(c.params) != len(stmt.Params) {
			t.Errorf("%s: params %v, want %v", c.src, stmt.Params, c.params)
		}
		if stmt.Kind != p.Result.Kind {
			t.Errorf("%s: SQL kind %s, program result %s", c.src, stmt.Kind, p.Result)
		}
	}
}

// Over document rows STATE() and ID() are the row's columns, and the
// statement takes no parameter for them (ADR-0040).
func TestGenerateSQLBound_RowBindsStateAndID(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)})
	p := mustBind(t, `STATE() = "draft" AND ID() <> "" AND USER() = "a"`, scope)
	for _, d := range []Dialect{NewSQLite(), NewDuckDB()} {
		stmt, err := GenerateSQLBound(p, d, SQLBinding{Column: quoteSlot, Row: func(name string) string { return "d." + quoteIdent(name) }})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range []string{`d."state"`, `d."id"`} {
			if !strings.Contains(stmt.Value, w) {
				t.Errorf("%s: value %s\n  lacks %s", d.Name(), stmt.Value, w)
			}
		}
		if len(stmt.Params) != 1 || stmt.Params[0] != "user" {
			t.Errorf("%s: params %v, want [user]", d.Name(), stmt.Params)
		}
		if stmt.Error != "" {
			t.Errorf("%s: error expression %s, want none", d.Name(), stmt.Error)
		}
	}
}

// Over document rows the life of the document is the row's system
// columns too, by the names LifeColumns gives them, and a statement over
// no row takes a parameter of the same name for each (ADR-0047).
// TestGenerateSQL_StatesAreOneParameter: a function of a state is
// translated over no document row, where the states are one parameter,
// and refused over the rows of a query, which do not have a document's
// events beside them (ADR-0047, item 6).
func TestGenerateSQL_StatesAreOneParameter(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)})
	p := mustBind(t, `ENTRIES("review") >= qty AND FIRSTENTEREDBY("review") <> USER() AND DAYSIN(STATE()) > 1`, scope)
	for _, d := range []Dialect{NewSQLite(), NewDuckDB()} {
		free, err := GenerateSQL(p, d, quoteSlot)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(free.Params, ","); got != "stays,user,state,now" {
			t.Errorf("%s: params %v, want stays, user, state and now", d.Name(), free.Params)
		}
		_, err = GenerateSQLBound(p, d, SQLBinding{Column: quoteSlot, Row: func(name string) string { return "d." + quoteIdent(name) }})
		if !errors.Is(err, ErrStateOverRows) {
			t.Errorf("%s: over the rows of a query: %v, want ErrStateOverRows", d.Name(), err)
		}
	}
}

func TestGenerateSQLBound_RowBindsTheLife(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)})
	p := mustBind(t, `CREATEDBY() <> USER() AND NOW() >= ADDDAYS(ENTEREDAT(), 7) AND ENTEREDFROM() = "review" AND UPDATEDBY() = ENTEREDBY() AND VERSION() > 1 AND UPDATEDAT() >= CREATEDAT()`, scope)
	columns := []string{"created_by", "state_entered_at", "state_entered_from", "updated_by", "state_entered_by", "version", "updated_at", "created_at"}
	for _, d := range []Dialect{NewSQLite(), NewDuckDB()} {
		stmt, err := GenerateSQLBound(p, d, SQLBinding{Column: quoteSlot, Row: func(name string) string { return "d." + quoteIdent(name) }})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			if !strings.Contains(stmt.Value, `d."`+c+`"`) {
				t.Errorf("%s: value %s\n  lacks the column %s", d.Name(), stmt.Value, c)
			}
		}
		if got := strings.Join(stmt.Params, ","); got != "user,now" {
			t.Errorf("%s: params %v, want user and now", d.Name(), stmt.Params)
		}
		free, err := GenerateSQL(p, d, quoteSlot)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			found := false
			for _, name := range free.Params {
				found = found || name == c
			}
			if !found || strings.Contains(free.Value, `d."`+c+`"`) {
				t.Errorf("%s: over no row %s is a parameter: params %v", d.Name(), c, free.Params)
			}
		}
	}
}

func TestGenerateSQL_RefusesUnstructuredBytecode(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)})
	p := mustBind(t, "IF(qty > 0, 1, 2)", scope)
	// Break the IF shape: the jump before the else branch becomes a nop.
	for i, in := range p.Code {
		if in.Op == OpJump {
			p.Code[i].Op = OpNop
		}
	}
	if _, err := GenerateSQL(p, NewSQLite(), quoteSlot); err == nil || !strings.Contains(err.Error(), "binder's shape") {
		t.Errorf("unstructured IF: %v", err)
	}
	q := mustBind(t, "qty + 1", scope)
	q.Code = q.Code[:len(q.Code)-1]
	if _, err := GenerateSQL(q, NewSQLite(), quoteSlot); err == nil {
		t.Error("a program without return should be refused")
	}
}

func TestSQLiteFunctions_ListIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range SQLiteFunctions() {
		if !strings.HasPrefix(f.Name, "fado_") || seen[f.Name] {
			t.Errorf("bad or duplicate registered name %s", f.Name)
		}
		seen[f.Name] = true
		if f.Fn == nil {
			t.Errorf("%s has no implementation", f.Name)
		}
	}
	for _, want := range []string{"fado_round", "fado_min", "fado_value", "fado_jsonvalue"} {
		if !seen[want] {
			t.Errorf("%s is not registered", want)
		}
	}
	if f := SQLiteFunctions()[4]; f.Name != "fado_min" || f.NArgs != -1 {
		t.Errorf("MIN should be variadic: %+v", f)
	}
}

func TestValueFromDriverAndBack(t *testing.T) {
	cases := []struct {
		in   any
		kind Kind
		want Value
	}{
		{nil, KindNumber, Blank()},
		{int64(3), KindNumber, Number(3)},
		{2.5, KindNumber, Number(2.5)},
		{int64(1), KindBool, Bool(true)},
		{0.0, KindBool, Bool(false)},
		{19753.0, KindDate, Date(19753)},
		{1706659200.0, KindDateTime, DateTime(1706659200)},
		{int64(1230), KindDecimal, Decimal(1230, 0)},
		{"x", KindText, Text("x")},
		{[]byte("y"), KindText, Text("y")},
		{"{}", KindJSON, JSON("{}")},
		{int64(7), KindNull, Number(7)},
		{"z", KindNull, Text("z")},
	}
	for _, c := range cases {
		if got := ValueFromDriver(c.in, c.kind); !Equal(got, c.want) {
			t.Errorf("ValueFromDriver(%v, %s) = %s, want %s", c.in, c.kind, got, c.want)
		}
	}
	for _, v := range []Value{Blank(), Number(2.5), Bool(true), Date(5), DateTime(86401), Text("a")} {
		back := ValueFromDriver(v.DriverValue(), v.Kind())
		if !Equal(back, v) {
			t.Errorf("round trip of %s gave %s", v, back)
		}
	}
}

func TestGenerateSQL_DuckDBShapes(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)}, Variable{Name: "name", Type: Scalar(KindText)},
		Variable{Name: "due", Type: Scalar(KindDate)}, Variable{Name: "paid", Type: Scalar(KindBool)})
	cases := []struct {
		src       string
		wantValue []string
		wantError []string
		params    []string
	}{
		{src: "qty * 2.5", wantValue: []string{`(COALESCE(CAST("qty" AS DOUBLE), 0.0::DOUBLE) * CAST(2.5 AS DOUBLE))`}, wantError: []string{"NOT isfinite("}},
		{src: "IF(paid, TRUE, FALSE)", wantValue: []string{`CASE WHEN COALESCE(CAST("paid" AS BOOLEAN), FALSE) THEN TRUE ELSE FALSE END`}},
		{src: "ROUND(qty)", wantValue: []string{`fado_round_m(COALESCE(CAST("qty" AS DOUBLE), 0.0::DOUBLE), CAST(0 AS DOUBLE), 'half_up')`}},
		{src: "YEAR(due)", wantValue: []string{`CAST(year(fado_ts((CAST("due" AS DOUBLE) * CAST(86400 AS DOUBLE)))) AS DOUBLE)`}},
		{src: "INGROUP(\"g\") AND USER() = \"a\"", wantValue: []string{`list_contains($groups, 'g')`, `CAST($user AS VARCHAR)`}, params: []string{"groups", "user"}},
		{src: "STATE() = \"draft\" OR ID() = \"x\"", wantValue: []string{`CAST($state AS VARCHAR)`, `CAST($id AS VARCHAR)`}, params: []string{"state", "id"}},
		{src: "VALUE(name)", wantValue: []string{`fado_value(COALESCE("name", ''))`}, wantError: []string{`fado_value_err(COALESCE("name", ''))`}},
		{src: "SUM(qty, 1)", wantValue: []string{`(COALESCE(CAST("qty" AS DOUBLE), 0.0::DOUBLE) + CAST(1 AS DOUBLE))`}},
		{src: "TEXT(qty)", wantValue: []string{`COALESCE(printf('%.15g', CAST("qty" AS DOUBLE) + 0.0::DOUBLE), '')`}},
	}
	for _, c := range cases {
		p := mustBind(t, c.src, scope)
		stmt, err := GenerateSQL(p, NewDuckDB(), quoteSlot)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		for _, w := range c.wantValue {
			if !strings.Contains(stmt.Value, w) {
				t.Errorf("%s: value %s\n  lacks %s", c.src, stmt.Value, w)
			}
		}
		for _, w := range c.wantError {
			if !strings.Contains(stmt.Error, w) {
				t.Errorf("%s: error %s\n  lacks %s", c.src, stmt.Error, w)
			}
		}
		if len(c.params) != len(stmt.Params) {
			t.Errorf("%s: params %v, want %v", c.src, stmt.Params, c.params)
		}
	}
	if n := len(NewDuckDB().Setup()); n < 20 {
		t.Errorf("DuckDB setup has %d statements, expected the macro set", n)
	}
	for _, stmt := range NewDuckDB().Setup() {
		if !strings.HasPrefix(stmt, "CREATE OR REPLACE MACRO fado_") {
			t.Errorf("not a macro definition: %.60s", stmt)
		}
		if strings.Contains(stmt, "(SELECT") {
			t.Errorf("a macro body must be an expression, not a subquery: %.60s", stmt)
		}
	}
}

func TestNearlyEqual(t *testing.T) {
	if !NearlyEqual(Number(0.6000000000000001), Number(0.6), 1e-12) {
		t.Error("last-bit difference should pass")
	}
	if NearlyEqual(Number(0.61), Number(0.6), 1e-12) {
		t.Error("a real difference should fail")
	}
	if NearlyEqual(Text("a"), Text("b"), 1) || !NearlyEqual(Blank(), Blank(), 0) {
		t.Error("non-numbers compare exactly")
	}
}
