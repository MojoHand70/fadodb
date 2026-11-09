package query

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

const invoice = `{"name": "Invoice", "variables": {
  "number": {"type": "text", "index": true}, "priority": {"type": "number"}, "discount": {"type": "decimal(2)"},
  "net": {"type": "money(2)"}, "paid": {"type": "bool"}, "shipping": {"type": "object", "schema": "Address"},
  "lines": {"type": "rows", "schema": {"sku": "text"}}}}`

func compile(t *testing.T, f Filter) (*Plan, error) {
	t.Helper()
	cls, diags := model.Define([]byte(invoice), nil)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	return Compile(f, cls, nil)
}

func TestCompile_Prefilter(t *testing.T) {
	cases := []struct {
		where string
		pre   string // the prefilter terms the WHERE must start with
	}{
		{`number = "A"`, `"number" = :f0 AND (`},
		{`"A" = number`, `"number" = :f0 AND (`},
		{`priority > 2`, `"priority" > :f0 AND (`},
		{`2 < priority`, `"priority" > :f0 AND (`},
		{`priority < 2`, `("priority" < :f0 OR "priority" IS NULL) AND (`},
		{`priority = 0`, `("priority" = :f0 OR "priority" IS NULL) AND (`},
		{`priority >= -1`, `("priority" >= :f0 OR "priority" IS NULL) AND (`},
		{`discount >= 0.10`, `"discount" >= :f0 AND (`},
		{`paid = FALSE`, `("paid" = :f0 OR "paid" IS NULL) AND (`},
		{`shipping.city = "Sopot"`, `"shipping__city" = :f0 AND (`},
		{`number = "A" AND priority > 2`, `"number" = :f0 AND "priority" > :f1 AND (`},
		{`state = "draft"`, `"state" = :f0 AND (`},
		{`STATE() = "draft"`, `"state" = :f0 AND (`},
		{`"draft" = State()`, `"state" = :f0 AND (`},
		{`ID() = "01J0"`, `"id" = :f0 AND (`},
		{`ID() = "01J0" AND STATE() = "draft"`, `"id" = :f0 AND "state" = :f1 AND (`},
		// No prefilter: OR, a function, money, a non-literal, a decimal that does not fit.
		{`number = "A" OR priority > 2`, ""},
		{`UPPER(number) = "A"`, ""},
		{`net > 10.00`, ""},
		{`priority > version`, ""},
		{`discount = 0.125`, ""},
		{`number <> "A"`, ""},
	}
	for _, tc := range cases {
		plan, err := compile(t, Filter{Class: "Invoice", Where: tc.where})
		if err != nil {
			t.Errorf("%s: %v", tc.where, err)
			continue
		}
		if tc.pre == "" {
			if strings.Contains(plan.Where, ":f0") {
				t.Errorf("%s: unexpected prefilter in %s", tc.where, plan.Where)
			}
		} else if !strings.HasPrefix(plan.Where, tc.pre) {
			t.Errorf("%s:\n  %s\nwant a prefix of\n  %s", tc.where, plan.Where, tc.pre)
		}
		if plan.Order != `"id"` || plan.Limit != DefaultLimit {
			t.Errorf("%s: order %q limit %d", tc.where, plan.Order, plan.Limit)
		}
	}
}

func TestCompile_Errors(t *testing.T) {
	cases := []struct {
		f       Filter
		code    string
		element string
	}{
		{Filter{Where: "priority >"}, CodeBadFormula, "where"},
		{Filter{Where: "priority"}, CodeBadFormula, "where"},
		{Filter{Where: "lines"}, CodeBadFormula, "where"},
		{Filter{Where: "LOOKUP(Rates, rate, TRUE) > 1"}, CodeBadFormula, "where"},
		{Filter{Order: []string{"nope"}}, CodeUnknownVariable, "order/0"},
		{Filter{Order: []string{"shipping"}}, CodeUnknownVariable, "order/0"},
		{Filter{Order: []string{"number", "number"}}, CodeBadValue, "order/1"},
		{Filter{Limit: -1}, CodeBadValue, "limit"},
		{Filter{Limit: MaxLimit + 1}, CodeBadValue, "limit"},
		{Filter{Offset: -3}, CodeBadValue, "offset"},
		{Filter{Fields: []string{"nope"}}, CodeUnknownVariable, "fields/0"},
	}
	for _, tc := range cases {
		_, err := compile(t, tc.f)
		e, ok := err.(*Error)
		if !ok || e.Code != tc.code || e.Element != tc.element {
			t.Errorf("%+v: want %s on %s, got %v", tc.f, tc.code, tc.element, err)
		}
	}
	plan, err := compile(t, Filter{Order: []string{"-priority", "shipping.city"}, Limit: 5, Offset: 10, Fields: []string{"number", "lines", "NUMBER"}})
	if err != nil || plan.Order != `"priority" DESC, "shipping__city", "id"` || plan.Limit != 5 || plan.Offset != 10 || strings.Join(plan.Fields, ",") != "number,lines" {
		t.Errorf("plan %+v %v", plan, err)
	}
}

func TestIsPhysical(t *testing.T) {
	for name, want := range map[string]bool{"doc_Invoice": true, "tbl_Rates": true, "events": true, "sqlite_master": true, "Invoice": false, "Rates": false, "Invoice_history": false} {
		if IsPhysical(name) != want {
			t.Errorf("IsPhysical(%s) = %v", name, !want)
		}
	}
}
