package query

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
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
	return Compile(f, cls, nil, Principal{})
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

// The read rights of ADR-0041 compile into the predicate for the
// principal: a CASE over the state column with the resolved rule of each
// state, a group list as a literal, a formula through the generator; a
// variable readable in every state adds nothing, one readable in no
// state refuses the filter.
func TestCompile_ReadRights(t *testing.T) {
	cls, diags := model.Define([]byte(`{"name": "Ticket", "variables": {
	  "owner": {"type": "text"}, "note": {"type": "text"}, "secret": {"type": "text"}, "amount": {"type": "number"}},
	  "states": {"initial": "open", "list": ["open", "review"]},
	  "rights": {"*": {"secret": {"read": ["finance"]}}, "review": {"note": {"read": "owner = USER() OR INGROUP(\"finance\")"}}}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	alice := Principal{ID: "alice", Groups: []string{"sales"}}
	fin := Principal{ID: "fin", Groups: []string{"finance"}}
	admin := Principal{ID: "root", Admin: true}
	for _, tc := range []struct {
		name  string
		f     Filter
		p     Principal
		want  string // a fragment the WHERE must contain, or "" for no rights predicate
		never bool   // the filter is refused with forbidden
	}{
		{"no right on the variable", Filter{Class: "Ticket", Where: `amount > 1`}, alice, "", false},
		{"a group list the principal fails in every state", Filter{Class: "Ticket", Where: `secret = "x"`}, alice, "", true},
		{"a group list the principal is in", Filter{Class: "Ticket", Where: `secret = "x"`}, fin, "", false},
		{"a formula in one state", Filter{Class: "Ticket", Where: `note = "n"`}, alice, `(CASE "state" WHEN 'open' THEN (1) WHEN 'review' THEN (`, false},
		{"the formula binds the principal", Filter{Class: "Ticket", Where: `note = "n"`}, alice, `:user`, false},
		{"order alone compiles the right", Filter{Class: "Ticket", Order: []string{"-note"}}, alice, `CASE "state"`, false},
		{"an administrator reads everything", Filter{Class: "Ticket", Where: `secret = "x"`}, admin, "", false},
		{"nobody is refused where every state needs a group", Filter{Class: "Ticket", Order: []string{"secret"}}, Principal{}, "", true},
	} {
		plan, err := Compile(tc.f, cls, nil, tc.p)
		if tc.never {
			var qe *Error
			if err == nil || !errorsAs(err, &qe) || qe.Code != CodeForbidden || qe.Variable != "secret" {
				t.Errorf("%s: want forbidden naming secret, got %v", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		switch {
		case tc.want == "" && strings.Contains(plan.Where, "CASE"):
			t.Errorf("%s: no rights predicate expected, got %s", tc.name, plan.Where)
		case tc.want != "" && !strings.Contains(plan.Where, tc.want):
			t.Errorf("%s: want %q in %s", tc.name, tc.want, plan.Where)
		}
	}
}

func errorsAs(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

// BenchmarkCompile is the compiler alone on the indexed equality of
// BenchmarkFilter, without and with a formula read right on the filtered
// variable (ADR-0041): the right's share of the filter's budget.
func BenchmarkCompile(b *testing.B) {
	const ticket = `{"name": "%s", "variables": {"number": {"type": "text", "index": true}, "priority": {"type": "number"}, "owner": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}%s}`
	plain, diags := model.Define([]byte(fmt.Sprintf(ticket, "Ticket", "")), nil)
	if len(diags) > 0 {
		b.Fatal(diags)
	}
	guarded, diags := model.Define([]byte(fmt.Sprintf(ticket, "Guarded", `, "rights": {"*": {"number": {"read": "INGROUP(\"support\") OR owner = USER()"}}}`)), nil)
	if len(diags) > 0 {
		b.Fatal(diags)
	}
	support := Principal{ID: "agent", Groups: []string{"support"}}
	for _, bc := range []struct {
		name string
		cls  *model.Class
	}{{"plain", plain}, {"with a read right", guarded}} {
		b.Run(bc.name, func(b *testing.B) {
			f := Filter{Class: bc.cls.Name, Where: `number = "T-0000500"`}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compile(f, bc.cls, nil, support); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestCompile_PrefilterOfTheClock is the prefilter of a comparison whose
// other operand reads no document, the clock or a constant expression:
// one value for the whole statement, so the comparison seeks an index as
// one with a literal does. A blank on either side compares as zero.
func TestCompile_PrefilterOfTheClock(t *testing.T) {
	cls, diags := model.Define([]byte(`{"name": "Task", "variables": {
  "due": {"type": "date", "index": true}, "sent": {"type": "datetime"}, "n": {"type": "number"},
  "owner": {"type": "text"}, "fee": {"type": "decimal(2)"}, "other": {"type": "date"}}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	const today = `COALESCE(floor(CAST(:now AS REAL) / 86400.0), 0)`
	cases := []struct {
		where string
		pre   string // what the WHERE must start with; "" for no prefilter
	}{
		{`due < TODAY()`, `("due" < ` + today + ` OR ("due" IS NULL AND 0 < ` + today + `)) AND (`},
		{`TODAY() > due`, `("due" < ` + today + ` OR ("due" IS NULL AND 0 < ` + today + `)) AND (`},
		{`TODAY() >= due`, `("due" <= ` + today + ` OR ("due" IS NULL AND 0 <= ` + today + `)) AND (`},
		{`due = TODAY()`, `("due" = ` + today + ` OR ("due" IS NULL AND 0 = ` + today + `)) AND (`},
		{`due >= TODAY() AND n > 2`, `("due" >= ` + today + ` OR ("due" IS NULL AND 0 >= ` + today + `)) AND "n" > :f0 AND (`},
		{`due < ADDDAYS(TODAY(), -30)`, `("due" < COALESCE(`},
		{`sent <= NOW()`, `("sent" <= COALESCE(`},
		{`n > 2 + 1`, `("n" > COALESCE(`},
		// Not a prefilter: a text, an exact value, a column or a value of
		// the row on the other side, an inequality, an OR.
		{`owner = USER()`, ""},
		{`fee > 1 + 1`, ""},
		{`due < other`, ""},
		{`n > VERSION()`, ""},
		{`n > LEN(STATE())`, ""},
		{`n > LEN(ID())`, ""},
		{`sent < CREATEDAT()`, ""},
		{`due <> TODAY()`, ""},
		{`due < TODAY() OR n > 2 + 1`, ""},
	}
	for _, tc := range cases {
		plan, err := Compile(Filter{Class: "Task", Where: tc.where}, cls, nil, Principal{})
		if err != nil {
			t.Errorf("%s: %v", tc.where, err)
			continue
		}
		if tc.pre == "" {
			if strings.Contains(plan.Where, "IS NULL AND 0 ") {
				t.Errorf("%s: unexpected prefilter in %s", tc.where, plan.Where)
			}
		} else if !strings.HasPrefix(plan.Where, tc.pre) {
			t.Errorf("%s:\n  %s\nwant a prefix of\n  %s", tc.where, plan.Where, tc.pre)
		}
	}
}

// Value gives the SQL of a formula's value over the rows of a class, for
// a statement of the store's own (ADR-0047): the columns the formula
// reads, the system columns for the functions of the row, NULL where it
// fails, and a refusal of what does not run over rows.
func TestValue(t *testing.T) {
	cls, diags := model.Define([]byte(`{"name": "Bill", "variables": {"due": {"type": "date"}, "ring": {"type": "datetime"}, "n": {"type": "number"}},
	  "states": {"initial": "open", "list": ["open"]}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	for _, tc := range []struct {
		src  string
		kind formula.Kind
		has  []string
	}{
		{"due", formula.KindDate, []string{`"due"`}},
		{"ring", formula.KindDateTime, []string{`"ring"`}},
		{"ADDDAYS(due, 1)", formula.KindDate, []string{`"due"`}},
		{"ADDDAYS(ENTEREDAT(), 7)", formula.KindDateTime, []string{`"state_entered_at"`}},
		{"ADDDAYS(CREATEDAT(), 7)", formula.KindDateTime, []string{`"created_at"`}},
		// A formula that can fail is NULL where it does.
		{"ADDDAYS(due, 10 / n)", formula.KindDate, []string{"CASE WHEN (", ") IS NULL THEN ", " END"}},
	} {
		got, kind, err := Value(tc.src, cls, nil)
		if err != nil || kind != tc.kind {
			t.Errorf("%s: kind %v, %v", tc.src, kind, err)
			continue
		}
		for _, part := range tc.has {
			if !strings.Contains(got, part) {
				t.Errorf("%s: %s lacks %s", tc.src, got, part)
			}
		}
		if strings.Contains(got, ":created_at") || strings.Contains(got, ":state_entered_at") {
			t.Errorf("%s reads the life from a parameter, not from the row: %s", tc.src, got)
		}
	}
	for _, tc := range []struct{ src, code string }{
		{"due +", CodeBadFormula},
		{"nope", CodeBadFormula},
		{`ADDDAYS(FIRSTENTERED("open"), 7)`, CodeBadFormula},
		{`LOOKUP(Rates, since, code = "EUR")`, CodeBadFormula},
	} {
		_, _, err := Value(tc.src, cls, nil)
		var qe *Error
		if !errors.As(err, &qe) || qe.Code != tc.code {
			t.Errorf("%s: want %s, got %v", tc.src, tc.code, err)
		}
	}
	// A lookup of a table the database has binds, and is refused as one.
	rates := formula.NewSourceSet(&formula.Source{Name: "Rates", Key: []string{"code"},
		Columns: []formula.Field{{Name: "code", Type: formula.Scalar(formula.KindText)}, {Name: "since", Type: formula.Scalar(formula.KindDate)}}})
	_, _, err := Value(`LOOKUP(Rates, since, code = "EUR")`, cls, rates)
	var qe *Error
	if !errors.As(err, &qe) || qe.Code != CodeNotAllowed {
		t.Errorf("a lookup: %v", err)
	}
	if _, kind, err := Value("due", cls, rates); err != nil || kind != formula.KindDate {
		t.Errorf("a column beside a table: %v %v", kind, err)
	}
}

// The read rule of a class joins every filter as one more predicate
// (ADR-0049): none for an administrator, the rule alone for a filter
// without a where, with its comparisons to literals narrowed as a where's
// are and its parameters named apart from the where's.
func TestCompile_ReadRule(t *testing.T) {
	const ruled = `{"name": "Claim", "variables": {"owner": {"type": "text", "index": true}, "region": {"type": "text", "index": true}, "n": {"type": "number"}},
  "states": {"initial": "a", "list": ["a"]},
  "access": {"read": "region = \"EU\" AND (owner = USER() OR INGROUP(\"hr\"))"}}`
	cls, diags := model.Define([]byte(ruled), nil)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	t.Cleanup(func() { Forget(cls) })
	admin, err := Compile(Filter{Class: "Claim"}, cls, nil, Principal{Admin: true})
	if err != nil || admin.Where != "" || len(admin.Args) != 0 {
		t.Errorf("an administrator's filter: %v %q", err, admin.Where)
	}
	alone, err := Compile(Filter{Class: "Claim"}, cls, nil, Principal{ID: "ann"})
	if err != nil || !strings.HasPrefix(alone.Where, `("region" = :r0 AND "owner" = :user AND (`) || len(alone.Args) != 1 {
		t.Fatalf("the rule alone: %v %q %v", err, alone.Where, alone.Args)
	}
	both, err := Compile(Filter{Class: "Claim", Where: `n > 2 OR region = "US"`}, cls, nil, Principal{ID: "ann"})
	if err != nil || !strings.HasSuffix(both.Where, ") AND "+alone.Where) || !strings.HasPrefix(both.Where, "(") {
		t.Errorf("the rule beside a where with OR: %v %q", err, both.Where)
	}
	narrowed, err := Compile(Filter{Class: "Claim", Where: `n > 2`}, cls, nil, Principal{ID: "ann"})
	if err != nil || !strings.HasPrefix(narrowed.Where, `("n" > :f0 AND (`) || len(narrowed.Args) != 2 {
		t.Errorf("the rule beside a narrowed where: %v %q %v", err, narrowed.Where, narrowed.Args)
	}
	// A where that compares an indexed column leads by itself: the rule
	// then stands beside it without its own terms for an index.
	led, err := Compile(Filter{Class: "Claim", Where: `region = "US" AND n > 2`}, cls, nil, Principal{ID: "ann"})
	if err != nil || strings.Contains(led.Where, ":r0") || strings.Contains(led.Where, `"owner" = :user`) || !strings.Contains(led.Where, `COALESCE("owner", '') = COALESCE(:user, '')`) || len(led.Args) != 2 {
		t.Errorf("the rule beside a where that leads to an index: %v %q %v", err, led.Where, led.Args)
	}
	// The caller's part is decided before the statement is written: the
	// owner's test is an index term for ann, as the prefix above has it,
	// and gone for a member of HR.
	// A call without a principal has no id: USER() is blank, a blank owner
	// equals it, and the term says so.
	nobody, _ := Compile(Filter{Class: "Claim"}, cls, nil, Principal{})
	if !strings.Contains(nobody.Where, `("owner" = COALESCE(:user, '') OR ("owner" IS NULL AND '' = COALESCE(:user, '')))`) {
		t.Errorf("the rule for nobody: %s", nobody.Where)
	}
	hr, _ := Compile(Filter{Class: "Claim"}, cls, nil, Principal{ID: "hana", Groups: []string{"hr"}})
	if strings.Contains(hr.Where, "owner") || strings.Contains(hr.Where, ":groups") || !strings.HasPrefix(hr.Where, `("region" = :r0 AND (`) {
		t.Errorf("the rule for a member of HR: %s", hr.Where)
	}
	// One text for each outcome, not for each caller.
	for _, p := range []Principal{{ID: "bob"}, {ID: "eve", Groups: []string{"sales"}}} {
		if again, _ := Compile(Filter{Class: "Claim"}, cls, nil, p); again.Where != alone.Where {
			t.Errorf("the rule for %s is another text:\n %s\n %s", p.ID, again.Where, alone.Where)
		}
	}
}

// The rules a read rule is folded by (ADR-0049, item 5) are the ones the
// evaluators follow: AND and OR stop at the operand that decides them,
// left to right, so an operand that may stop with an error is dropped
// only when it stands right of the constant that decides.
func TestCompile_ReadRuleFolded(t *testing.T) {
	hr := Principal{ID: "hana", Groups: []string{"hr"}}
	ann := Principal{ID: "ann", Groups: []string{"staff"}}
	const all, none, some = "", "0", "some"
	for _, tc := range []struct {
		rule       string
		hr, ann    string
		annHasTerm string // a prefilter term ann's predicate must carry
	}{
		{`owner = USER() OR INGROUP(\"hr\")`, all, some, `"owner" = :user AND `},
		{`INGROUP(\"hr\") OR owner = USER()`, all, some, `"owner" = :user AND `},
		{`INGROUP(\"hr\")`, all, none, ""},
		{`NOT INGROUP(\"hr\")`, none, all, ""},
		{`INGROUP(\"hr\") AND n > 0`, some, none, ""},
		{`n > 0 AND INGROUP(\"hr\")`, some, none, ""},
		{`INGROUP(\"hr\") OR INGROUP(\"staff\")`, all, all, ""},
		{`INGROUP(\"hr\") AND INGROUP(\"staff\")`, none, none, ""},
		{`(owner = USER() OR INGROUP(\"hr\")) AND region = \"EU\"`, some, some, `"owner" = :user AND `},
		// 1 / n may stop with an error. Left of the constant it is asked
		// first and stays; right of it, it is never asked.
		{`1 / n > 0 OR INGROUP(\"hr\")`, some, some, ""},
		{`INGROUP(\"hr\") OR 1 / n > 0`, all, some, ""},
		{`1 / n > 0 AND INGROUP(\"hr\")`, some, some, ""},
		{`INGROUP(\"hr\") AND 1 / n > 0`, some, none, ""},
		// INGROUP of something computed is left to the statement.
		{`INGROUP(region)`, some, some, ""},
	} {
		def := `{"name": "Claim", "variables": {"owner": {"type": "text", "index": true}, "region": {"type": "text"}, "n": {"type": "number"}},
  "states": {"initial": "a", "list": ["a"]}, "access": {"read": "` + tc.rule + `"}}`
		cls, diags := model.Define([]byte(def), nil)
		if len(diags) > 0 {
			t.Fatalf("%s: %v", tc.rule, diags)
		}
		for _, who := range []struct {
			p    Principal
			want string
		}{{hr, tc.hr}, {ann, tc.ann}} {
			plan, err := Compile(Filter{Class: "Claim"}, cls, nil, who.p)
			if err != nil {
				t.Errorf("%s for %s: %v", tc.rule, who.p.ID, err)
				continue
			}
			got := plan.Where
			if got != all && got != none {
				got = some
			}
			if got != who.want {
				t.Errorf("%s for %s: %q, want %s", tc.rule, who.p.ID, plan.Where, who.want)
			}
			if who.p.ID == "ann" && tc.annHasTerm != "" && !strings.Contains(plan.Where, tc.annHasTerm) {
				t.Errorf("%s for ann lacks the term %s: %s", tc.rule, tc.annHasTerm, plan.Where)
			}
		}
		Forget(cls)
	}
}
