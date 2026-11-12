package model

import (
	"fmt"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// timed is a class with one transition, written by the test, out of
// waiting: a document waits, is reminded, and is closed or dropped.
func timed(transition string) string {
	return `{"name": "Ticket", "variables": {
	    "due": {"type": "date"}, "ring": {"type": "datetime"}, "n": {"type": "number"}, "owner": {"type": "text"},
	    "kept": {"type": "date", "formula": "ADDDAYS(due, 1)", "mode": "triggered", "on": ["refresh"]}},
	  "states": {"initial": "waiting", "list": ["waiting", "late", "closed", "dropped"], "final": ["closed", "dropped"]},
	  "transitions": [` + transition + `]}`
}

func TestParseAfter(t *testing.T) {
	for _, tc := range []struct {
		in   string
		secs int64
	}{
		{"30d", 30 * 86400}, {"12h", 12 * 3600}, {"90m", 90 * 60}, {"45s", 45}, {"1s", 1}, {"007d", 7 * 86400},
		// A thousand years of 365 days is the most, in any unit.
		{"365000d", MaxAfter}, {"8760000h", MaxAfter}, {"31536000000s", MaxAfter},
	} {
		if got, ok := ParseAfter(tc.in); !ok || got != tc.secs {
			t.Errorf("ParseAfter(%q) = %d, %v; want %d", tc.in, got, ok, tc.secs)
		}
	}
	for _, in := range []string{"", "d", "30", "0d", "00s", "-1d", "+1d", "1.5d", "30 d", " 30d", "30d ", "30D", "1w", "1y", "1ms", "d30", "3d4h",
		"365001d", "8760001h", "31536000001s", "99999999999999999999d", "9223372036854775807s"} {
		if got, ok := ParseAfter(in); ok {
			t.Errorf("ParseAfter(%q) = %d; want a refusal", in, got)
		}
	}
}

// What a class with a timed transition is, once compiled (ADR-0047, item
// 9).
func TestDefine_Timers(t *testing.T) {
	for _, tc := range []struct {
		name, transition string
		after            int64
		at               string
		kind             formula.Kind
	}{
		{"after, without a name or a guard", `{"from": "waiting", "to": "late", "after": "30d"}`, 30 * 86400, "", 0},
		{"after with a guard", `{"from": "waiting", "to": "late", "after": "12h", "guard": "n > 0"}`, 12 * 3600, "", 0},
		{"a reminder: after into its own state, with a name", `{"name": "remind", "from": "waiting", "to": "waiting", "after": "3d"}`, 3 * 86400, "", 0},
		{"at a date the document holds", `{"from": "waiting", "to": "late", "at": "due"}`, 0, "due", formula.KindDate},
		{"at a date it computes", `{"from": "waiting", "to": "late", "at": "ADDDAYS(due, 1)"}`, 0, "ADDDAYS(due, 1)", formula.KindDate},
		{"at a datetime", `{"name": "ring", "from": "waiting", "to": "late", "at": "ring"}`, 0, "ring", formula.KindDateTime},
		{"at a triggered variable", `{"from": "waiting", "to": "late", "at": "kept"}`, 0, "kept", formula.KindDate},
		{"at a moment of the document's life", `{"from": "waiting", "to": "late", "at": "ADDDAYS(FIRSTENTERED(\"waiting\"), 14)"}`, 0, `ADDDAYS(FIRSTENTERED("waiting"), 14)`, formula.KindDateTime},
		{"at the creation, which reads the row", `{"from": "waiting", "to": "late", "at": "ADDDAYS(CREATEDAT(), 2)", "guard": "STATE() = \"waiting\""}`, 0, "ADDDAYS(CREATEDAT(), 2)", formula.KindDateTime},
		{"at, into a terminal state", `{"from": "waiting", "to": "closed", "at": "due"}`, 0, "due", formula.KindDate},
		{"from several states", `{"from": ["waiting", "late"], "to": "dropped", "after": "90d"}`, 90 * 86400, "", 0},
	} {
		cls, diags := Define([]byte(timed(tc.transition)), nil)
		if len(diags) > 0 {
			t.Errorf("%s: %s", tc.name, diags.Error())
			continue
		}
		tr := cls.Transitions[0]
		if !tr.Timed() || tr.After != tc.after || tr.AtSrc != tc.at || (tr.At != nil) != (tc.at != "") {
			t.Errorf("%s: timed %v, after %d, at %q (%v)", tc.name, tr.Timed(), tr.After, tr.AtSrc, tr.At != nil)
		}
		if tr.At != nil && tr.At.Result.Kind != tc.kind {
			t.Errorf("%s: the at gives %s", tc.name, tr.At.Result)
		}
	}
	// A transition without either is not timed.
	cls := mustDefine(t, timed(`{"name": "close", "from": "waiting", "to": "closed"}, {"from": "waiting", "to": "late", "guard": "n > 0"}`))
	for _, tr := range cls.Transitions {
		if tr.Timed() || tr.After != 0 || tr.At != nil || tr.AtSrc != "" {
			t.Errorf("%q is timed: %+v", tr.Name, tr)
		}
	}
}

// What is refused when the class is defined (ADR-0047, item 14).
func TestDefine_TimerDiagnostics(t *testing.T) {
	tbl, diags := DefineTable([]byte(rates))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Sources: formula.NewSourceSet(tbl.Source())}
	for _, tc := range []struct{ name, transition, code, path string }{
		{"both", `{"from": "waiting", "to": "late", "after": "3d", "at": "due"}`, CodeNotAllowed, "/transitions/0/at"},
		{"both, the after wrong", `{"from": "waiting", "to": "late", "after": "soon", "at": "due"}`, CodeNotAllowed, "/transitions/0/at"},
		{"after is no length of time", `{"from": "waiting", "to": "late", "after": "soon"}`, CodeBadValue, "/transitions/0/after"},
		{"after without a unit", `{"from": "waiting", "to": "late", "after": "30"}`, CodeBadValue, "/transitions/0/after"},
		{"after of nothing", `{"from": "waiting", "to": "late", "after": "0d"}`, CodeBadValue, "/transitions/0/after"},
		{"after in weeks", `{"from": "waiting", "to": "late", "after": "2w"}`, CodeBadValue, "/transitions/0/after"},
		{"after past a thousand years", `{"from": "waiting", "to": "late", "after": "365001d"}`, CodeBadValue, "/transitions/0/after"},
		{"after out of a terminal state", `{"from": "closed", "to": "dropped", "after": "3d"}`, CodeNotAllowed, "/transitions/0/from"},
		{"at out of a terminal state", `{"name": "drop", "from": ["waiting", "closed"], "to": "dropped", "at": "due"}`, CodeNotAllowed, "/transitions/0/from"},
		{"at into its own state", `{"name": "remind", "from": "waiting", "to": "waiting", "at": "due"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at into one of its own states", `{"from": ["waiting", "late"], "to": "late", "at": "due"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at does not bind", `{"from": "waiting", "to": "late", "at": "due +"}`, CodeBadFormula, "/transitions/0/at"},
		{"at of an unknown variable", `{"from": "waiting", "to": "late", "at": "deadline"}`, CodeBadFormula, "/transitions/0/at"},
		{"at is a number", `{"from": "waiting", "to": "late", "at": "n"}`, CodeTypeMismatch, "/transitions/0/at"},
		{"at is a text", `{"from": "waiting", "to": "late", "at": "\"2026-12-01\""}`, CodeTypeMismatch, "/transitions/0/at"},
		{"at is a condition", `{"from": "waiting", "to": "late", "at": "TODAY() > due"}`, CodeTypeMismatch, "/transitions/0/at"},
		{"at is always blank", `{"from": "waiting", "to": "late", "at": "BLANK"}`, CodeTypeMismatch, "/transitions/0/at"},
		{"at reads the clock", `{"from": "waiting", "to": "late", "at": "ADDDAYS(TODAY(), 3)"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at reads the instant", `{"from": "waiting", "to": "late", "at": "ADDDAYS(NOW(), 3)"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at reads the clock deep inside", `{"from": "waiting", "to": "late", "at": "IF(n > 0, due, ADDDAYS(due, DAYS(due, TODAY())))"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at reads the user", `{"from": "waiting", "to": "late", "at": "IF(USER() = owner, due, ADDDAYS(due, 1))"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at reads the user's groups", `{"from": "waiting", "to": "late", "at": "IF(INGROUP(\"vip\"), due, ADDDAYS(due, 1))"}`, CodeNotAllowed, "/transitions/0/at"},
		{"at looks a table up", `{"from": "waiting", "to": "late", "at": "LOOKUP(Rates, since, code = THIS.owner)"}`, CodeNotAllowed, "/transitions/0/at"},
		{"neither a name, a guard nor a timer", `{"from": "waiting", "to": "late"}`, CodeMissing, "/transitions/0/guard"},
		{"the second transition", `{"name": "close", "from": "waiting", "to": "closed"}, {"from": "waiting", "to": "late", "after": "1"}`, CodeBadValue, "/transitions/1/after"},
	} {
		_, diags := DefineIn([]byte(timed(tc.transition)), env)
		if len(diags) != 1 || diags[0].Code != tc.code || diags[0].Path != tc.path {
			t.Errorf("%s: want %s at %s alone, got:\n%s", tc.name, tc.code, tc.path, fmt.Sprint(diags))
		}
	}
	// A guard may read all of that: only the moment may not.
	if _, diags := DefineIn([]byte(timed(`{"from": "waiting", "to": "late", "after": "3d", "guard": "TODAY() > due AND USER() = owner AND LOOKUP(Rates, rate, code = THIS.owner) > 0"}`)), env); len(diags) > 0 {
		t.Errorf("a guard beside an after: %v", diags)
	}
}
