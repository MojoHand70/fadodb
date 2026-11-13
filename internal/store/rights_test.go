package store

import (
	"context"
	"strings"
	"testing"
)

// The rights and the per-transition rules of ADR-0041: a write right is
// checked over the stored document before the assignments, a transition's
// rule the same way, every document the API returns is projected for the
// principal, and a filter compiles the read right of the variables it
// names.
const ticketRights = `{"name": "Ticket",
 "variables": {
   "owner":  {"type": "text", "default": "USER()"},
   "amount": {"type": "number"},
   "note":   {"type": "text"},
   "secret": {"type": "text"},
   "odd":    {"type": "number"},
   "cost":   {"formula": "amount * 2"}
 },
 "states": {"initial": "open", "list": ["open", "review", "closed"]},
 "transitions": [
   {"name": "submit",  "from": "open",   "to": "review", "guard": "amount > 0"},
   {"name": "approve", "from": "review", "to": "closed", "guard": "amount > 5"},
   {"name": "back",    "from": "review", "to": "open"}
 ],
 "access": {"transition": {"approve": "INGROUP(\"finance\")", "back": "owner = USER()"}},
 "rights": {
   "*": {"secret": {"read": ["finance"], "write": ["finance"]},
         "amount": {"write": "owner = USER()"},
         "odd":    {"write": "1 / amount > 0"}},
   "review": {"note": {"read": "owner = USER() OR INGROUP(\"finance\")", "write": "FALSE"}}
 }}`

var (
	ticketOwner    = Principal{ID: "alice", Groups: []string{"sales"}}
	ticketFinance  = Principal{ID: "fin", Groups: []string{"finance"}}
	ticketStranger = Principal{ID: "bob", Groups: []string{"other"}}
	ticketAdmin    = Principal{ID: "root", System: []string{GroupContentAdministrators}}
)

func openTickets(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(ticketRights), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// forbidden checks a refusal is forbidden about the variable.
func forbidden(t *testing.T, err error, variable string) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Code != CodeForbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
	if e.Variable != variable {
		t.Fatalf("forbidden names %q, want %q: %v", e.Variable, variable, err)
	}
	return e
}

func has(d *Document, name string) bool {
	_, ok := d.Values[name]
	return ok
}

func TestRights_WriteCheck(t *testing.T) {
	s, ctx := openTickets(t)

	// The write right runs over the new document after its defaults: the
	// owner defaults to the caller, so the caller may set the amount.
	doc, err := s.Create(ctx, "Ticket", Values{"amount": 10, "note": "n"}, As(ticketOwner))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["owner"] != "alice" || doc.Values["cost"] != 20.0 {
		t.Fatalf("created %v", doc.Values)
	}
	// A group list the caller is not in.
	_, err = s.Create(ctx, "Ticket", Values{"secret": "x"}, As(ticketOwner))
	e := forbidden(t, err, "secret")
	if !strings.Contains(e.Message, "alice may not set secret") || !strings.Contains(e.Hint, "the groups finance") {
		t.Errorf("message %q hint %q", e.Message, e.Hint)
	}
	if _, err := s.Create(ctx, "Ticket", Values{"secret": "x"}, As(ticketFinance)); err != nil {
		t.Errorf("finance sets secret: %v", err)
	}
	// A formula over the stored document: only the owner sets the amount.
	_, err = s.Set(ctx, "Ticket", doc.ID, Values{"amount": 11}, As(ticketStranger))
	forbidden(t, err, "amount")
	if _, err := s.Set(ctx, "Ticket", doc.ID, Values{"amount": 11}, As(ticketOwner)); err != nil {
		t.Errorf("the owner sets the amount: %v", err)
	}
	// The first forbidden variable in declaration order is reported.
	_, err = s.Set(ctx, "Ticket", doc.ID, Values{"secret": "x", "amount": 12, "note": "m"}, As(ticketOwner))
	forbidden(t, err, "secret")
	// A right that errors is FALSE and the message carries the error.
	_, err = s.Create(ctx, "Ticket", Values{"odd": 1}, As(ticketOwner))
	e = forbidden(t, err, "odd")
	if !strings.Contains(e.Message, "div_by_zero") {
		t.Errorf("message %q", e.Message)
	}
	// A preview reports every forbidden variable, in declaration order,
	// and still computes.
	pv, err := s.Preview(ctx, "Ticket", doc.ID, Values{"secret": "x", "amount": 99, "note": "m"}, "", As(ticketStranger))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range pv.Errors {
		if e.Code == CodeForbidden {
			names = append(names, e.Variable)
		}
	}
	if strings.Join(names, " ") != "amount secret" || pv.Document.Values["cost"] != 198.0 {
		t.Errorf("preview forbids %v, want amount and secret; cost %v", names, pv.Document.Values["cost"])
	}
	// An administrator bypasses; a call without a principal is nobody.
	if _, err := s.Set(ctx, "Ticket", doc.ID, Values{"secret": "x"}, As(ticketAdmin)); err != nil {
		t.Errorf("admin: %v", err)
	}
	_, err = s.Set(ctx, "Ticket", doc.ID, Values{"amount": 13})
	e = forbidden(t, err, "amount")
	if !strings.Contains(e.Message, "a call without a principal") {
		t.Errorf("message %q", e.Message)
	}
	// Rights are read in the state the command arrives in: the note may
	// be set on the way into review and not once there.
	moved, err := s.Transition(ctx, "Ticket", doc.ID, "submit", Values{"note": "on the way"}, As(ticketOwner))
	if err != nil || moved.State != "review" {
		t.Fatalf("submit: %v %v", moved, err)
	}
	_, err = s.Set(ctx, "Ticket", doc.ID, Values{"note": "later"}, As(ticketOwner))
	forbidden(t, err, "note")
	// A caller may hand the document over and lose the right in the
	// same commit; nobody grants themselves a right by the change that
	// uses it.
	if _, err := s.Set(ctx, "Ticket", doc.ID, Values{"owner": "carol", "amount": 14}, As(ticketOwner)); err != nil {
		t.Fatalf("hand over: %v", err)
	}
	_, err = s.Set(ctx, "Ticket", doc.ID, Values{"amount": 15}, As(ticketOwner))
	forbidden(t, err, "amount")
	// A class without rights is unchanged: anyone sets anything.
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Open", "variables": {"a": {"type": "number"}}, "states": {"initial": "s", "list": ["s"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Open", Values{"a": 1}); err != nil {
		t.Errorf("no rights: %v", err)
	}
}

func TestRights_TransitionRule(t *testing.T) {
	s, ctx := openTickets(t)
	doc, err := s.Create(ctx, "Ticket", Values{"amount": 10}, As(ticketOwner))
	if err != nil {
		t.Fatal(err)
	}
	if doc, err = s.Transition(ctx, "Ticket", doc.ID, "submit", nil, As(ticketOwner)); err != nil {
		t.Fatal(err)
	}
	// The rule refuses before the guard; the hint carries the rule.
	_, err = s.Transition(ctx, "Ticket", doc.ID, "approve", nil, As(ticketOwner))
	e := forbidden(t, err, "")
	if !strings.Contains(e.Message, "alice may not take approve from review") || !strings.Contains(e.Hint, `INGROUP("finance")`) {
		t.Errorf("message %q hint %q", e.Message, e.Hint)
	}
	// The listing: forbidden for the rule, then the guard.
	want := map[string]string{
		"alice": "approve:forbidden back:ok",
		"bob":   "approve:forbidden back:forbidden",
		"fin":   "approve:ok back:forbidden",
		"root":  "approve:ok back:ok",
	}
	for _, p := range []Principal{ticketOwner, ticketStranger, ticketFinance, ticketAdmin} {
		infos, err := s.Transitions(ctx, "Ticket", doc.ID, As(p))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, in := range infos {
			if in.Enabled {
				got = append(got, in.Name+":ok")
			} else {
				got = append(got, in.Name+":"+in.Code)
			}
		}
		if strings.Join(got, " ") != want[p.ID] {
			t.Errorf("%s: %v, want %s", p.ID, got, want[p.ID])
		}
	}
	// The preview of a forbidden transition is refused like the commit.
	if _, err := s.Preview(ctx, "Ticket", doc.ID, nil, "approve", As(ticketOwner)); code(err) != CodeForbidden {
		t.Errorf("preview: %v", err)
	}
	// An administrator bypasses the rule and never the guard.
	small, err := s.Create(ctx, "Ticket", Values{"amount": 1}, As(ticketOwner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Ticket", small.ID, "submit", nil, As(ticketOwner)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Ticket", small.ID, "approve", nil, As(ticketAdmin)); code(err) != CodeGuardFalse {
		t.Errorf("admin against the guard: %v", err)
	}
	// Finance takes it; the document is closed.
	if moved, err := s.Transition(ctx, "Ticket", doc.ID, "approve", nil, As(ticketFinance)); err != nil || moved.State != "closed" {
		t.Errorf("finance approves: %v %v", moved, err)
	}
}

func TestRights_Projection(t *testing.T) {
	s, ctx := openTickets(t)
	// The result of a commit is projected: alice never sees the secret.
	doc, err := s.Create(ctx, "Ticket", Values{"amount": 10, "note": "n"}, As(ticketOwner))
	if err != nil {
		t.Fatal(err)
	}
	if has(doc, "secret") || !has(doc, "note") || !has(doc, "owner") {
		t.Errorf("create as alice: %v", doc.Values)
	}
	if _, err := s.Set(ctx, "Ticket", doc.ID, Values{"secret": "s"}, As(ticketFinance)); err != nil {
		t.Fatal(err)
	}
	// Get: the secret for finance and the administrator only; in open
	// everyone reads the note.
	for _, tc := range []struct {
		p            Principal
		secret, note bool
	}{{ticketOwner, false, true}, {ticketFinance, true, true}, {ticketStranger, false, true}, {ticketAdmin, true, true}, {Principal{}, false, true}} {
		d, err := s.Get(ctx, "Ticket", doc.ID, As(tc.p))
		if err != nil {
			t.Fatal(err)
		}
		if has(d, "secret") != tc.secret || has(d, "note") != tc.note {
			t.Errorf("%q in open sees %v", tc.p.ID, d.Values)
		}
		if d.ID != doc.ID || d.Version != 2 || d.State != "open" {
			t.Errorf("system variables stay: %+v", d)
		}
	}
	// In review the note is the owner's and finance's.
	if _, err := s.Transition(ctx, "Ticket", doc.ID, "submit", nil, As(ticketOwner)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p    Principal
		note bool
	}{{ticketOwner, true}, {ticketFinance, true}, {ticketStranger, false}, {ticketAdmin, true}, {Principal{}, false}} {
		d, err := s.Get(ctx, "Ticket", doc.ID, As(tc.p))
		if err != nil {
			t.Fatal(err)
		}
		if has(d, "note") != tc.note {
			t.Errorf("%q in review sees the note: %v", tc.p.ID, has(d, "note"))
		}
	}
	// History and Version: each row under its own state. The stranger
	// reads the note of the open versions and not of the review one.
	rows, err := s.History(ctx, "Ticket", doc.ID, As(ticketStranger))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, r := range rows {
		seen = append(seen, r.State+":"+boolWord(has(&r.Document, "note"))+boolWord(has(&r.Document, "secret")))
	}
	if strings.Join(seen, " ") != "open:yn open:yn review:nn" {
		t.Errorf("history for the stranger: %v", seen)
	}
	if v, err := s.Version(ctx, "Ticket", doc.ID, 3, As(ticketStranger)); err != nil || has(&v.Document, "note") {
		t.Errorf("version 3 for the stranger: %v %v", v, err)
	}
	if v, err := s.Version(ctx, "Ticket", doc.ID, 3, As(ticketOwner)); err != nil || !has(&v.Document, "note") {
		t.Errorf("version 3 for the owner: %v %v", v, err)
	}
	// Query and its fields: a hidden variable is absent, with no refusal.
	docs, err := s.Query(ctx, Filter{Class: "Ticket", Fields: []string{"secret", "amount"}}, As(ticketStranger))
	if err != nil || len(docs) != 1 {
		t.Fatalf("query: %v %d", err, len(docs))
	}
	if has(docs[0], "secret") || !has(docs[0], "amount") {
		t.Errorf("fields for the stranger: %v", docs[0].Values)
	}
	// A keyed replay is projected for the principal of the replay.
	first, err := s.Create(ctx, "Ticket", Values{"secret": "k"}, As(ticketFinance), Key("k-1"))
	if err != nil || !has(first, "secret") {
		t.Fatalf("keyed create: %v %v", first, err)
	}
	again, err := s.Create(ctx, "Ticket", Values{"secret": "k"}, As(ticketFinance), Key("k-1"))
	if err != nil || again.ID != first.ID || !has(again, "secret") {
		t.Errorf("replay for finance: %v %v", again, err)
	}
}

func boolWord(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

// The rights table of the current class version applies by name to the
// rows an older version computed.
func TestRights_OlderVersionRows(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	v1 := `{"name": "Ticket", "variables": {"amount": {"type": "number"}, "secret": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}}`
	if _, err := s.defineOpen(ctx, []byte(v1), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Ticket", Values{"amount": 1, "secret": "s"}, As(ticketStranger))
	if err != nil {
		t.Fatal(err)
	}
	v2 := `{"name": "Ticket", "variables": {"amount": {"type": "number"}, "secret": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]},
	  "rights": {"*": {"secret": {"read": ["finance"]}}}}`
	if _, err := s.defineOpen(ctx, []byte(v2), asSetup); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "Ticket", doc.ID, As(ticketStranger))
	if err != nil {
		t.Fatal(err)
	}
	if got.ClassVersion != 1 || has(got, "secret") || !has(got, "amount") {
		t.Errorf("a version-1 row under the version-2 rights: %+v", got)
	}
	if got, err := s.Get(ctx, "Ticket", doc.ID, As(ticketFinance)); err != nil || !has(got, "secret") {
		t.Errorf("finance: %v %v", got, err)
	}
	docs, err := s.Query(ctx, Filter{Class: "Ticket"}, As(ticketStranger))
	if err != nil || len(docs) != 1 || has(docs[0], "secret") {
		t.Errorf("query: %v %v", docs, err)
	}
}

func TestRights_Filter(t *testing.T) {
	s, ctx := openTickets(t)
	// Two documents with the same note, one open and one in review,
	// alice's.
	open, err := s.Create(ctx, "Ticket", Values{"amount": 1, "note": "n"}, As(ticketOwner))
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.Create(ctx, "Ticket", Values{"amount": 2, "note": "n"}, As(ticketOwner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Ticket", review.ID, "submit", nil, As(ticketOwner)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Ticket", open.ID, Values{"secret": "x"}, As(ticketFinance)); err != nil {
		t.Fatal(err)
	}
	// A variable readable in no state refuses the filter; readable in
	// some, it matches where the row's state allows.
	_, err = s.Query(ctx, Filter{Class: "Ticket", Where: `secret = "x"`}, As(ticketStranger))
	e := forbidden(t, err, "secret")
	if !strings.Contains(e.Message, "bob may read secret in no state") {
		t.Errorf("message %q", e.Message)
	}
	if _, err := s.Query(ctx, Filter{Class: "Ticket", Order: []string{"-secret"}}, As(ticketStranger)); code(err) != CodeForbidden {
		t.Errorf("order by a hidden variable: %v", err)
	}
	if n, err := s.Count(ctx, Filter{Class: "Ticket", Where: `secret = "x"`}, As(ticketFinance)); err != nil || n != 1 {
		t.Errorf("finance counts %d, %v", n, err)
	}
	for _, tc := range []struct {
		p    Principal
		want int64
	}{{ticketOwner, 2}, {ticketFinance, 2}, {ticketStranger, 1}, {ticketAdmin, 2}, {Principal{}, 1}} {
		n, err := s.Count(ctx, Filter{Class: "Ticket", Where: `note = "n"`}, As(tc.p))
		if err != nil {
			t.Fatalf("%q: %v", tc.p.ID, err)
		}
		if n != tc.want {
			t.Errorf("%q counts %d notes, want %d", tc.p.ID, n, tc.want)
		}
		docs, err := s.Query(ctx, Filter{Class: "Ticket", Order: []string{"note", "-amount"}}, As(tc.p))
		if err != nil {
			t.Fatalf("%q: %v", tc.p.ID, err)
		}
		if int64(len(docs)) != tc.want {
			t.Errorf("%q sorts %d documents by note, want %d", tc.p.ID, len(docs), tc.want)
		}
	}
	// The right rides beside the index term and the where.
	docs, err := s.Query(ctx, Filter{Class: "Ticket", Where: `note = "n" AND amount > 1`}, As(ticketStranger))
	if err != nil || len(docs) != 0 {
		t.Errorf("the stranger finds %d, %v", len(docs), err)
	}
	docs, err = s.Query(ctx, Filter{Class: "Ticket", Where: `note = "n" AND amount > 1`}, As(ticketOwner))
	if err != nil || len(docs) != 1 || docs[0].ID != review.ID {
		t.Errorf("the owner finds %v, %v", docs, err)
	}
}

// A read right is compiled into every filter, so it may not look a
// table up; a write right may.
func TestRights_ReadRightWithoutLookups(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpenTable(ctx, []byte(`{"name": "Fx", "columns": {"code": "text", "rate": "decimal(6)"}, "key": ["code"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	read := `{"name": "Q", "variables": {"cur": {"type": "text"}, "net": {"type": "number"}}, "states": {"initial": "s", "list": ["s"]},
	  "rights": {"*": {"net": {"read": "LOOKUP(Fx, rate, code = THIS.cur) > 1"}}}}`
	if diags := s.Validate(opened([]byte(read))); len(diags) != 1 || diags[0].Code != "not_allowed" || diags[0].Path != "/rights/*/net/read" {
		t.Errorf("read right with a lookup: %v", diags)
	}
	write := `{"name": "Q", "variables": {"cur": {"type": "text"}, "net": {"type": "number"}}, "states": {"initial": "s", "list": ["s"]},
	  "rights": {"*": {"net": {"write": "LOOKUP(Fx, rate, code = THIS.cur) > 1"}}}}`
	if diags := s.Validate(opened([]byte(write))); len(diags) != 0 {
		t.Errorf("write right with a lookup: %v", diags)
	}
}
