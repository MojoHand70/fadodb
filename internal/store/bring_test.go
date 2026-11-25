package store

import (
	"strings"
	"testing"
)

// The purchase of ADR-0078 with its rights: the limit is brought when
// the purchase is filed, and brought again by a manager while it is in
// review and by nobody anywhere else. checked lists refresh and has no
// cell: whoever may read the purchase may bring it again.
const bringPurchase = `{
 "name": "Purchase",
 "title": "\"Purchase \" & number",
 "variables": {
   "number":  {"type": "text", "required": true},
   "amount":  {"type": "decimal", "scale": 2, "required": true},
   "grade":   {"type": "text", "required": true},
   "limit":   {"type": "decimal", "scale": 2,
               "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
               "mode": "triggered", "on": ["enter:review", "refresh"]},
   "checked": {"type": "datetime", "formula": "NOW()", "mode": "triggered", "on": ["refresh"]}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved"], "final": ["approved"]},
 "transitions": [
   {"name": "file",    "from": "draft",  "to": "review"},
   {"name": "return",  "from": "review", "to": "draft"},
   {"name": "approve", "from": "review", "to": "approved", "guard": "NOT ISBLANK(limit) AND amount <= limit"}
 ],
 "access": {
   "read":   "INGROUP(\"everyone\")",
   "create": "INGROUP(\"everyone\")",
   "update": "CREATEDBY() = USER()",
   "delete": "FALSE",
   "transition": {
     "file":    "CREATEDBY() = USER()",
     "return":  "INGROUP(\"managers\")",
     "approve": "INGROUP(\"managers\")"
   }
 },
 "rights": {
   "*":      {"limit": {"write": "FALSE"}},
   "review": {"limit": {"write": ["managers"]}}
 }
}`

// Two values that list refresh, each closed to all but treasury.
const bringRates = `{
 "name": "Rates",
 "variables": {
   "buy":  {"type": "datetime", "formula": "NOW()", "mode": "triggered", "on": ["refresh"]},
   "sell": {"type": "datetime", "formula": "NOW()", "mode": "triggered", "on": ["refresh"]}
 },
 "states": {"initial": "kept", "list": ["kept"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"},
 "rights": {"*": {"buy": {"write": ["treasury"]}, "sell": {"write": ["treasury"]}}}
}`

func bringStore(t *testing.T) *Store {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.DefineTable(ctx, []byte(sawLimits), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Limits", []Values{{"grade": "B", "limit": "10000.00"}}, sawTess); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{bringPurchase, bringRates} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// forbiddenAt wants a refusal with forbidden that names the variable.
func forbiddenAt(t *testing.T, what string, err error, variable, message string) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Code != CodeForbidden || e.Variable != variable || e.Message != message {
		t.Fatalf("%s: %v; want forbidden, variable %q, %q", what, err, variable, message)
	}
	return e
}

// The steps of the record: Piotr, who has no part in the purchase, may
// not bring its limit again; Tomek, a manager, may while it is in review;
// and nobody may once it is back in draft.
func TestBring_TheWriteRightOfATriggeredVariable(t *testing.T) {
	s := bringStore(t)
	ctx := t.Context()
	sawFiled(t, s, "P-7", "9000.00")
	if _, err := s.ImportRows(ctx, "Limits", []Values{{"grade": "B", "limit": "5000.00"}}, sawTess); err != nil {
		t.Fatal(err)
	}
	_, err := s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, sawPiotr)
	e := forbiddenAt(t, "Piotr refreshes the limit", err, "limit", "piotr may not bring limit again while the document is in review")
	if !strings.Contains(e.Hint, "review / limit") || !strings.Contains(e.Hint, "managers") {
		t.Errorf("the hint names neither the cell nor the groups: %q", e.Hint)
	}
	if d, _ := s.Get(ctx, "Purchase", "P-7", sawTomek); d.Version != 2 || d.Values["limit"] != "10000.00" {
		t.Fatalf("a refused refresh changed the purchase: %v", d)
	}
	// A refresh that names none brings what its caller may bring, and
	// leaves the rest as it is.
	d, err := s.Refresh(ctx, "Purchase", "P-7", nil, sawPiotr)
	if err != nil || d.Version != 3 {
		t.Fatalf("Piotr refreshes the purchase: %v, %v", d, err)
	}
	if d.Values["limit"] != "10000.00" || d.Computed["limit"].Version != 2 || d.Computed["checked"].Version != 3 {
		t.Errorf("after Piotr's refresh: limit %v worked out at version %d, checked at version %d; want 10000.00 at 2 and checked at 3",
			d.Values["limit"], d.Computed["limit"].Version, d.Computed["checked"].Version)
	}
	// A manager brings the limit again while the purchase is in review.
	d, err = s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, sawTomek)
	if err != nil || d.Version != 4 || d.Values["limit"] != "5000.00" || d.Computed["limit"].Version != 4 {
		t.Fatalf("Tomek refreshes the limit: %v, %v", d, err)
	}
	d, err = s.Refresh(ctx, "Purchase", "P-7", nil, sawTomek)
	if err != nil || d.Computed["limit"].Version != 5 || d.Computed["checked"].Version != 5 {
		t.Fatalf("Tomek refreshes the purchase: %v, %v", d, err)
	}
	// Back in draft the cell of every state decides: nobody.
	if _, err := s.Transition(ctx, "Purchase", "P-7", "return", nil, sawTomek); err != nil {
		t.Fatal(err)
	}
	_, err = s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, sawTomek)
	e = forbiddenAt(t, "Tomek refreshes the limit of a draft", err, "limit", "tomek may not bring limit again while the document is in draft")
	if !strings.Contains(e.Hint, "* / limit") || !strings.Contains(e.Hint, "FALSE") {
		t.Errorf("the hint names neither the cell nor the rule: %q", e.Hint)
	}
	// The update rule of the class is not asked: Tomek did not create
	// the purchase, and brings checked again all the same.
	if d, err = s.Refresh(ctx, "Purchase", "P-7", []string{"checked"}, sawTomek); err != nil {
		t.Fatalf("Tomek refreshes checked: %v, %v", d, err)
	}
}

// A content administrator gets past the cell as past any right, with a
// reason, and the event keeps both.
func TestBring_AContentAdministratorGivesAReason(t *testing.T) {
	s := bringStore(t)
	ctx := t.Context()
	sawFiled(t, s, "P-7", "9000.00")
	olga := As(Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupContentAdministrators}})
	if _, err := s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, olga); code(err) != CodeReasonRequired {
		t.Fatalf("Olga refreshes the limit and gives no reason: %v; want reason_required", err)
	}
	d, err := s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, olga, Reason("the limits were loaded late"))
	if err != nil || d.Computed["limit"].Version != 3 {
		t.Fatalf("Olga refreshes the limit with a reason: %v, %v", d, err)
	}
	events, err := s.Events(ctx, "Purchase", "P-7", sawTomek)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Kind != EventRefresh || last.Override != "/rights/review/limit/write" || last.Reason != "the limits were loaded late" {
		t.Errorf("the event of Olga's refresh: %s", flat(last))
	}
}

// A refresh that names none is refused when its caller may bring none of
// the values that list refresh; and a class with no rights is as it was.
func TestBring_NoneOfThem(t *testing.T) {
	s := bringStore(t)
	ctx := t.Context()
	if _, err := s.Create(ctx, "Rates", nil, sawPiotr, WithID("R-1")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Refresh(ctx, "Rates", "R-1", nil, sawPiotr)
	forbiddenAt(t, "Piotr refreshes the rates", err, "", "piotr may bring none of buy, sell again while the document is in kept")
	_, err = s.Refresh(ctx, "Rates", "R-1", []string{"sell"}, sawPiotr)
	forbiddenAt(t, "Piotr refreshes sell", err, "sell", "piotr may not bring sell again while the document is in kept")
	if d, err := s.Refresh(ctx, "Rates", "R-1", nil, sawTess); err != nil || d.Computed["buy"].Version != 2 || d.Computed["sell"].Version != 2 {
		t.Fatalf("Tess refreshes the rates: %v, %v", d, err)
	}
	// The purchases of the other tests of the version have no rights
	// table: whoever may read one may refresh it, as before.
	plain := sawStore(t)
	sawFiled(t, plain, "P-1", "10.00")
	if d, err := plain.Refresh(ctx, "Purchase", "P-1", nil, sawPiotr); err != nil || d.Computed["limit"].Version != 3 {
		t.Fatalf("Piotr refreshes a purchase of a class with no rights: %v, %v", d, err)
	}
}

// A note on a purchase asks, by a rule, for the purchase's limit to be
// brought again. The refresh runs through the outbox as the person
// whose command set the rule off, and is asked that person's right.
const bringNote = `{
 "name": "Note",
 "variables": {"purchase": {"type": "text", "required": true}, "text": {"type": "text"}},
 "states": {"initial": "open", "list": ["open"]},
 "rules": [{"on": "enter", "state": "open", "do": [{"action": "refresh", "class": "Purchase", "id": "purchase", "variables": ["limit"]}]}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

func TestBring_ARefreshARuleQueuedAsksWhoeverSetItOff(t *testing.T) {
	s := bringStore(t)
	ctx := t.Context()
	if _, err := s.DefineClass(ctx, []byte(bringNote), asSetup); err != nil {
		t.Fatal(err)
	}
	sawFiled(t, s, "P-7", "9000.00")
	if _, err := s.ImportRows(ctx, "Limits", []Values{{"grade": "B", "limit": "5000.00"}}, sawTess); err != nil {
		t.Fatal(err)
	}
	note := func(as Option, id string) QueuedAction {
		t.Helper()
		if _, err := s.Create(ctx, "Note", Values{"purchase": "P-7"}, as, WithID(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Drain(ctx, asSetup); err != nil {
			t.Fatal(err)
		}
		queued, err := s.Outbox(ctx, OutboxFilter{Class: "Note", DocID: id}, asSetup)
		if err != nil || len(queued) != 1 {
			t.Fatalf("the queue of note %s: %s, %v", id, flat(queued), err)
		}
		return queued[0]
	}
	// Piotr's note: the refresh is his, and the limit is not his to bring.
	if a := note(sawPiotr, "N-1"); a.Status != ActionDead || a.ErrorCode != CodeForbidden {
		t.Errorf("the refresh Piotr's note queued: %s; want dead, forbidden", flat(a))
	}
	if d, _ := s.Get(ctx, "Purchase", "P-7", sawTomek); d.Values["limit"] != "10000.00" {
		t.Errorf("after Piotr's note the limit is %v; want 10000.00, as it was", d.Values["limit"])
	}
	// Tomek's note: he is a manager, and the purchase is in review.
	if a := note(sawTomek, "N-2"); a.Status != ActionDone {
		t.Errorf("the refresh Tomek's note queued: %s; want done", flat(a))
	}
	if d, _ := s.Get(ctx, "Purchase", "P-7", sawTomek); d.Values["limit"] != "5000.00" {
		t.Errorf("after Tomek's note the limit is %v; want 5000.00", d.Values["limit"])
	}
}
