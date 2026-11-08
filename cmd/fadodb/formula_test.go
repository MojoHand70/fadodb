package main

import (
	"bytes"
	"strings"
	"testing"
)

func evalCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(append([]string{"formula", "eval"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestFormulaEval(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"1 + 2 * 3"}, "7"},
		{[]string{"-var", "qty:number=3", "-var", "price:number=9.99", "ROUND(qty * price, 2)"}, "29.97"},
		{[]string{"ROUND(qty * price, 2)", "-var", "qty:number=3", "-var", "price:number=9.99"}, "29.97"},
		{[]string{"-var", "name:text=invoice", `UPPER(LEFT(name, 3)) & "-" & TEXT(42)`}, `"INV-42"`},
		{[]string{"-var", "due:date=2024-01-31", "ADDDAYS(due, 1)"}, "2024-02-01"},
		{[]string{"-var", "due:date", "ISBLANK(due)"}, "TRUE"},
		{[]string{"-var", "paid:bool=true", "IF(paid, 1, 0)"}, "1"},
		{[]string{"-var", `doc:json={"a":{"b":"deep"}}`, `JSONVALUE(doc, "a.b")`}, `"deep"`},
		{[]string{"-user", "alice", "-group", "finance", `INGROUP("finance") AND USER() = "alice"`}, "TRUE"},
		{[]string{"-now", "2024-02-29 10:00:00", "TEXT(TODAY())"}, `"2024-02-29"`},
		{[]string{"BLANK"}, "BLANK"},
		{[]string{"-ast", "1 + 2 * 3"}, "(+ 1 (* 2 3))"},
	}
	for _, c := range cases {
		code, out, errOut := evalCmd(t, c.args...)
		if code != 0 {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, errOut)
			continue
		}
		if got := strings.TrimSpace(out); got != c.want {
			t.Errorf("%v: got %q, want %q", c.args, got, c.want)
		}
	}
}

func TestFormulaEval_ErrorsExit1(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{`1 \ 0`}, "div_by_zero"},
		{[]string{"-max-text", "64", `REPEAT("x", 65)`}, "out_of_range"},
		{[]string{"nope + 1"}, "nope"},
		{[]string{"1 +"}, "column"},
		{[]string{"-ast", "1 +"}, "column"},
	}
	for _, c := range cases {
		code, out, errOut := evalCmd(t, c.args...)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1; stdout %q stderr %q", c.args, code, out, errOut)
			continue
		}
		if !strings.Contains(errOut, c.want) {
			t.Errorf("%v: stderr %q lacks %q", c.args, errOut, c.want)
		}
	}
}

func TestFormulaEval_UsageExit2(t *testing.T) {
	cases := [][]string{
		{},
		{"1", "2"},
		{"-max-text", "10", "1"},
		{"-sql", "mysql", "1"},
		{"-var", "qty", "1"},
		{"-var", "qty:money=1", "1"},
		{"-var", "qty:number=abc", "1"},
		{"-var", "paid:bool=maybe", "1"},
		{"-var", "due:date=tomorrow", "1"},
		{"-now", "tomorrow", "1"},
		{"-frobnicate", "1"},
	}
	for _, args := range cases {
		code, _, errOut := evalCmd(t, args...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2; stderr %q", args, code, errOut)
			continue
		}
		if !strings.Contains(errOut, "Usage:") {
			t.Errorf("%v: stderr %q lacks the usage", args, errOut)
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"formula"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("formula alone: exit %d, stderr %q", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"formula", "help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "-var name:type") {
		t.Errorf("formula help: exit %d, stdout %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"formula", "eval", "-h"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Errorf("eval -h: exit %d, stdout %q", code, out.String())
	}
}

func TestFormulaEval_SQLAndBytecode(t *testing.T) {
	code, out, errOut := evalCmd(t, "-sql", "sqlite", "-var", "qty:number", "qty * 2")
	if code != 0 || !strings.Contains(out, `value  (COALESCE(CAST("qty" AS REAL), 0.0) * 2.0)`) || !strings.Contains(out, "error  CASE WHEN") {
		t.Errorf("sqlite: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = evalCmd(t, "-sql", "duckdb", "-var", "due:date", "DAYS(due, TODAY())")
	if code != 0 || !strings.Contains(out, "AS DOUBLE") || !strings.Contains(out, "params now") {
		t.Errorf("duckdb: exit %d, stdout %q", code, out)
	}
	code, out, _ = evalCmd(t, "-sql", "sqlite", "-max-text", "64", `"a" & "b"`)
	if code != 0 || !strings.Contains(out, "> 64 THEN 'out_of_range'") {
		t.Errorf("limit in SQL: exit %d, stdout %q", code, out)
	}
	code, out, _ = evalCmd(t, "-bytecode", "-var", "qty:number", "qty + 1")
	if code != 0 || !strings.Contains(out, `"format": "FADO v3"`) || !strings.Contains(out, "load 0 (qty)") || !strings.Contains(out, `"result": "number"`) {
		t.Errorf("bytecode: exit %d, stdout %q", code, out)
	}
}
