package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The purchase of ADR-0078, item 4: the limit is brought before approve
// is asked, and the guard of approve reads the document alone.
const beforeLimits = `{"name": "Limits", "columns": {"grade": "text", "limit": "decimal(2)"}, "key": ["grade"]}`

func beforePurchase(on, guard, more string) string {
	return `{"name": "Purchase",
 "variables": {
   "amount": {"type": "decimal", "scale": 2},
   "grade":  {"type": "text"},
   "limit":  {"type": "decimal", "scale": 2, "formula": "LOOKUP(Limits, limit, grade = THIS.grade)", "mode": "triggered", "on": [` + on + `]}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "closed"], "final": ["approved", "closed"]},
 "transitions": [
   {"name": "file", "from": "draft", "to": "review"},
   {"name": "approve", "from": "review", "to": "approved", "guard": "` + guard + `"},
   {"name": "close", "from": "approved", "to": "closed"}` + more + `
 ]}`
}

func TestDefine_BeforeATransition(t *testing.T) {
	tbl, diags := DefineTable([]byte(beforeLimits))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Sources: formula.NewSourceSet(tbl.Source())}
	const reads = `NOT ISBLANK(limit) AND amount <= limit`
	cls, diags := DefineIn([]byte(beforePurchase(`"enter:review", "before:approve"`, reads, "")), env)
	if len(diags) > 0 {
		t.Fatalf("a variable brought before a transition: %v", diags)
	}
	limit, _ := cls.Variable("limit")
	if !limit.On("before", "approve") || !limit.On("enter", "review") || limit.On("before", "file") {
		t.Errorf("the triggers of limit: %+v", limit.Triggers)
	}

	const looks = `amount <= LOOKUP(Limits, limit, grade = THIS.grade)`
	for _, tc := range []struct{ name, def, code, path, hint string }{
		{"before names nothing", beforePurchase(`"before"`, reads, ""), CodeBadTrigger, "/variables/limit/on/0", ""},
		{"before an unknown transition", beforePurchase(`"before:sign"`, reads, ""), CodeUnknownTransition, "/variables/limit/on/0", ""},
		{"before a transition that leaves a terminal state", beforePurchase(`"before:close"`, reads, ""), CodeNotAllowed, "/variables/limit/on/0", "frozen"},
		{"a guard looks in a table", beforePurchase(`"enter:review"`, looks, ""), CodeNotAllowed, "/transitions/1/guard", `"before:approve"`},
		{"the guard of a transition without a name looks in a table", beforePurchase(`"enter:review"`, reads,
			`, {"from": "review", "to": "approved", "guard": "amount * 2 <= LOOKUP(Limits, limit, grade = THIS.grade)"}`), CodeNotAllowed, "/transitions/3/guard", `"enter:<state>"`},
	} {
		_, diags := DefineIn([]byte(tc.def), env)
		if len(diags) != 1 || diags[0].Code != tc.code || diags[0].Path != tc.path || !strings.Contains(diags[0].Message+" "+diags[0].Hint, tc.hint) {
			t.Errorf("%s: want %s at %s alone, with %q, got:\n%v", tc.name, tc.code, tc.path, tc.hint, diags)
		}
	}
	// A definition stored before a guard was held to its document is
	// compiled as it was written, and still runs.
	lenient := env
	lenient.Lenient = true
	cls, diags = DefineIn([]byte(beforePurchase(`"enter:review"`, looks, "")), lenient)
	if len(diags) > 0 || cls.Transitions[1].Guard == nil || len(cls.Transitions[1].Guard.Queries) != 1 {
		t.Errorf("a stored definition whose guard looks in a table: %v", diags)
	}
}
