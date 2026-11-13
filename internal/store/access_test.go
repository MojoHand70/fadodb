package store

import (
	"context"
	"io"
	"strings"
	"testing"
)

// The four rules of a class (ADR-0049): who may see, create, change and
// delete a document. A claim is seen by its owner and by HR; staff create
// small ones; the owner changes it while it is a draft; HR deletes and
// approves. file and back have no rule of their own.
const claimClass = `{"name": "Claim",
 "variables": {
   "owner":  {"type": "text", "default": "USER()", "index": true},
   "amount": {"type": "number"},
   "note":   {"type": "text"},
   "scan":   {"type": "object", "schema": "File"},
   "double": {"formula": "amount * 2"}
 },
 "states": {"initial": "draft", "list": ["draft", "filed", "closed"], "final": ["closed"]},
 "transitions": [
   {"name": "file",    "from": "draft", "to": "filed"},
   {"name": "approve", "from": "filed", "to": "closed"},
   {"name": "back",    "from": "filed", "to": "draft"}
 ],
 "access": {
   "read":   "owner = USER() OR INGROUP(\"hr\")",
   "create": "INGROUP(\"staff\") AND double <= 2000",
   "update": "owner = USER() AND STATE() = \"draft\"",
   "delete": "INGROUP(\"hr\")",
   "transition": {"approve": "INGROUP(\"hr\")"}
 },
 "rights": {"*": {"scan": {"read": "owner = USER()"}}}}`

var (
	clerkAnn = Principal{ID: "ann", Groups: []string{"staff"}}
	clerkBob = Principal{ID: "bob", Groups: []string{"staff"}}
	hr       = Principal{ID: "hana", Groups: []string{"hr"}}
	guest    = Principal{ID: "guest"}
	root     = Principal{ID: "root", Admin: true}
)

func openClaims(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(claimClass)); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// refusedBy checks a refusal is forbidden and names the rule's place.
func refusedBy(t *testing.T, err error, path string) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Code != CodeForbidden || e.Path != path {
		t.Fatalf("want forbidden at %s, got %v", path, err)
	}
	return e
}

func TestAccess_Create(t *testing.T) {
	s, ctx := openClaims(t)
	if _, err := s.Create(ctx, "Claim", Values{"amount": 100}, As(clerkAnn)); err != nil {
		t.Fatalf("staff creates a small claim: %v", err)
	}
	// The rule reads a computed value of the new document.
	e := refusedBy(t, func() error { _, err := s.Create(ctx, "Claim", Values{"amount": 5000}, As(clerkAnn)); return err }(), "/access/create")
	if !strings.Contains(e.Message, "ann may not create") || !strings.Contains(e.Hint, "double <= 2000") {
		t.Errorf("the refusal: %v", e)
	}
	refusedBy(t, func() error { _, err := s.Create(ctx, "Claim", Values{"amount": 1}, As(guest)); return err }(), "/access/create")
	refusedBy(t, func() error { _, err := s.Create(ctx, "Claim", Values{"amount": 1}); return err }(), "/access/create")
	if _, err := s.Create(ctx, "Claim", Values{"amount": 5000}, As(root)); err != nil {
		t.Errorf("an administrator passes the rule: %v", err)
	}
	// A refused create wrote nothing, and a batch is all or none.
	if n, _ := s.Count(ctx, Filter{Class: "Claim"}, As(root)); n != 2 {
		t.Errorf("documents after the refusals: %d", n)
	}
	if _, err := s.CreateMany(ctx, "Claim", []Values{{"amount": 1}, {"amount": 5000}}, As(clerkAnn)); code(err) != CodeForbidden {
		t.Errorf("a batch with a document its caller may not create: %v", err)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Claim"}, As(root)); n != 2 {
		t.Errorf("documents after the refused batch: %d", n)
	}
	// A preview reports the refusal under its errors and still answers.
	pre, err := s.Preview(ctx, "Claim", "", Values{"amount": 5000}, "", As(clerkAnn))
	if err != nil || len(pre.Errors) != 1 || pre.Errors[0].Code != CodeForbidden || pre.Errors[0].Path != "/access/create" {
		t.Errorf("the preview of a create the rule refuses: %v %s", err, dump(pre))
	}
}

// A document its caller may not see does not exist for them, in the words
// used for an id no document has, on every call that names it.
func TestAccess_HiddenMeansAbsent(t *testing.T) {
	s, ctx := openClaims(t)
	doc, err := s.Create(ctx, "Claim", Values{"amount": 100, "note": "taxi"}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Claim", doc.ID, Values{"note": "taxi, late"}, As(clerkAnn)); err != nil {
		t.Fatal(err)
	}
	const missing = "01M0000000000000000000NONE"
	same := func(what string, hidden, absent error) {
		t.Helper()
		if code(hidden) != CodeUnknownDocument || code(absent) != CodeUnknownDocument {
			t.Errorf("%s: hidden %v, absent %v", what, hidden, absent)
			return
		}
		if got, want := hidden.Error(), strings.ReplaceAll(absent.Error(), missing, doc.ID); got != want {
			t.Errorf("%s answers a hidden document in other words:\n %s\n %s", what, got, want)
		}
	}
	calls := map[string]func(id string) error{
		"Get":         func(id string) error { _, err := s.Get(ctx, "Claim", id, As(clerkBob)); return err },
		"Version":     func(id string) error { _, err := s.Version(ctx, "Claim", id, 1, As(clerkBob)); return err },
		"Transitions": func(id string) error { _, err := s.Transitions(ctx, "Claim", id, As(clerkBob)); return err },
		"Set": func(id string) error {
			_, err := s.Set(ctx, "Claim", id, Values{"note": "x"}, As(clerkBob))
			return err
		},
		"Reset":      func(id string) error { _, err := s.Reset(ctx, "Claim", id, []string{"note"}, As(clerkBob)); return err },
		"Refresh":    func(id string) error { _, err := s.Refresh(ctx, "Claim", id, nil, As(clerkBob)); return err },
		"Transition": func(id string) error { _, err := s.Transition(ctx, "Claim", id, "file", nil, As(clerkBob)); return err },
		// A transition the document is not in the state for says nothing
		// of its state either.
		"Transition from another state": func(id string) error {
			_, err := s.Transition(ctx, "Claim", id, "approve", nil, As(clerkBob))
			return err
		},
		"Delete": func(id string) error { return s.Delete(ctx, "Claim", id, As(clerkBob)) },
		"Preview": func(id string) error {
			_, err := s.Preview(ctx, "Claim", id, Values{"note": "x"}, "", As(clerkBob))
			return err
		},
		"OpenFile": func(id string) error {
			_, _, err := s.OpenFile(ctx, "Claim", id, "sha256:none", As(clerkBob))
			return err
		},
	}
	for what, call := range calls {
		same(what, call(doc.ID), call(missing))
	}
	// The history and the events of a hidden document are those of no
	// document: none.
	if h, err := s.History(ctx, "Claim", doc.ID, As(clerkBob)); err != nil || len(h) != 0 {
		t.Errorf("the history of a hidden document: %v %d", err, len(h))
	}
	if ev, err := s.Events(ctx, "Claim", doc.ID, As(clerkBob)); err != nil || len(ev) != 0 {
		t.Errorf("the events of a hidden document: %v %d", err, len(ev))
	}
	// A filter and a count leave it out, with a where and without.
	for _, where := range []string{"", "amount > 0", "note <> \"\" OR amount = 100"} {
		f := Filter{Class: "Claim", Where: where}
		if docs, err := s.Query(ctx, f, As(clerkBob)); err != nil || len(docs) != 0 {
			t.Errorf("bob's filter %q: %v %d", where, err, len(docs))
		}
		if n, err := s.Count(ctx, f, As(clerkBob)); err != nil || n != 0 {
			t.Errorf("bob's count %q: %v %d", where, err, n)
		}
		for _, p := range []Principal{clerkAnn, hr, root} {
			if docs, err := s.Query(ctx, f, As(p)); err != nil || len(docs) != 1 {
				t.Errorf("%s's filter %q: %v %d", p.ID, where, err, len(docs))
			}
			if n, err := s.Count(ctx, f, As(p)); err != nil || n != 1 {
				t.Errorf("%s's count %q: %v %d", p.ID, where, err, n)
			}
		}
	}
	// Nothing bob did changed the document, and those who may see it do.
	for _, p := range []Principal{clerkAnn, hr, root} {
		got, err := s.Get(ctx, "Claim", doc.ID, As(p))
		if err != nil || got.Version != 2 || got.Values["note"] != "taxi, late" {
			t.Errorf("%s reads the claim: %v %s", p.ID, err, dump(got))
		}
		if h, err := s.History(ctx, "Claim", doc.ID, As(p)); err != nil || len(h) != 2 {
			t.Errorf("%s reads its history: %v %d", p.ID, err, len(h))
		}
		if ev, err := s.Events(ctx, "Claim", doc.ID, As(p)); err != nil || len(ev) != 2 {
			t.Errorf("%s reads its events: %v %d", p.ID, err, len(ev))
		}
	}
	// A batch answers for a hidden document as for one that is not there.
	out, err := s.TransitionMany(ctx, "Claim", "file", []string{doc.ID, missing}, nil, As(clerkBob))
	if err != nil || outcomes(out) != "unknown_document unknown_document" {
		t.Errorf("bob's batch: %v %s", err, outcomes(out))
	}
}

// Who may change a document: the update rule for a set, a reset and a
// transition without a rule of its own; the transition's own rule
// otherwise, which does not ask update.
func TestAccess_UpdateAndTransitions(t *testing.T) {
	s, ctx := openClaims(t)
	doc, err := s.Create(ctx, "Claim", Values{"amount": 100}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	// HR sees the claim and may not change it.
	e := refusedBy(t, func() error { _, err := s.Set(ctx, "Claim", doc.ID, Values{"note": "x"}, As(hr)); return err }(), "/access/update")
	if !strings.Contains(e.Message, "hana may not change the document "+doc.ID) || !strings.Contains(e.Hint, "owner = USER()") {
		t.Errorf("the refusal: %v", e)
	}
	refusedBy(t, func() error { _, err := s.Reset(ctx, "Claim", doc.ID, []string{"note"}, As(hr)); return err }(), "/access/update")
	// A set with nothing to set changes nothing and asks nothing more than
	// to see the document; a refresh likewise.
	if got, err := s.Set(ctx, "Claim", doc.ID, nil, As(hr)); err != nil || got.Version != 1 {
		t.Errorf("a set of nothing by one who only sees: %v", err)
	}
	if _, err := s.Refresh(ctx, "Claim", doc.ID, nil, As(hr)); err != nil {
		t.Errorf("a refresh by one who only sees: %v", err)
	}
	// A preview of the set reports the rule and still shows the document.
	pre, err := s.Preview(ctx, "Claim", doc.ID, Values{"note": "x"}, "", As(hr))
	if err != nil || len(pre.Errors) != 1 || pre.Errors[0].Path != "/access/update" || pre.Document == nil {
		t.Errorf("the preview of a set the rule refuses: %v %s", err, dump(pre))
	}
	// file has no rule of its own: the update rule answers for it.
	e = refusedBy(t, func() error { _, err := s.Transition(ctx, "Claim", doc.ID, "file", nil, As(hr)); return err }(), "/access/update")
	if !strings.Contains(e.Message, "may not take file from draft") || !strings.Contains(e.Message, "no rule of its own") {
		t.Errorf("the refusal of a transition without a rule: %v", e)
	}
	infos, err := s.Transitions(ctx, "Claim", doc.ID, As(hr))
	if err != nil || len(infos) != 1 || infos[0].Name != "file" || infos[0].Enabled || infos[0].Code != CodeForbidden {
		t.Errorf("the listing for one who may not change the document: %v %s", err, dump(infos))
	}
	if _, err := s.Transition(ctx, "Claim", doc.ID, "file", Values{"note": "sent"}, As(clerkAnn)); err != nil {
		t.Fatalf("the owner files her draft: %v", err)
	}
	// In filed the update rule lets nobody through: the owner may neither
	// set nor take back, which has no rule of its own.
	refusedBy(t, func() error { _, err := s.Set(ctx, "Claim", doc.ID, Values{"note": "y"}, As(clerkAnn)); return err }(), "/access/update")
	refusedBy(t, func() error { _, err := s.Transition(ctx, "Claim", doc.ID, "back", nil, As(clerkAnn)); return err }(), "/access/update")
	// approve has a rule of its own, and is not asked update: HR approves
	// a claim it may not edit, and the owner may not.
	refusedBy(t, func() error { _, err := s.Transition(ctx, "Claim", doc.ID, "approve", nil, As(clerkAnn)); return err }(), "/access/transition/approve")
	got, err := s.Transition(ctx, "Claim", doc.ID, "approve", nil, As(hr))
	if err != nil || got.State != "closed" {
		t.Fatalf("HR approves: %v", err)
	}
	// An administrator passes the rules, and never a guard or a terminal
	// state.
	if _, err := s.Set(ctx, "Claim", doc.ID, Values{"note": "z"}, As(root)); code(err) != CodeTerminal {
		t.Errorf("an administrator on a terminal document: %v", err)
	}
}

func TestAccess_Delete(t *testing.T) {
	s, ctx := openClaims(t)
	doc, err := s.Create(ctx, "Claim", Values{"amount": 100}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	e := refusedBy(t, s.Delete(ctx, "Claim", doc.ID, As(clerkAnn)), "/access/delete")
	if !strings.Contains(e.Message, "ann may not delete the document "+doc.ID) {
		t.Errorf("the refusal: %v", e)
	}
	if err := s.Delete(ctx, "Claim", doc.ID, As(hr)); err != nil {
		t.Fatalf("HR deletes: %v", err)
	}
	// The past of a deleted document is read under the rule over its last
	// version: its owner and HR read it, a stranger does not.
	for _, p := range []Principal{clerkAnn, hr} {
		if h, err := s.History(ctx, "Claim", doc.ID, As(p)); err != nil || len(h) != 1 {
			t.Errorf("%s reads the history of the deleted claim: %v %d", p.ID, err, len(h))
		}
		if _, err := s.Version(ctx, "Claim", doc.ID, 1, As(p)); err != nil {
			t.Errorf("%s reads its version: %v", p.ID, err)
		}
	}
	if h, err := s.History(ctx, "Claim", doc.ID, As(clerkBob)); err != nil || len(h) != 0 {
		t.Errorf("a stranger reads the history of the deleted claim: %v %d", err, len(h))
	}
	if _, err := s.Version(ctx, "Claim", doc.ID, 1, As(clerkBob)); code(err) != CodeUnknownDocument {
		t.Errorf("a stranger reads its version: %v", err)
	}
}

// A command whose caller cannot see what it left is answered with the id,
// the version and the state and no values, and a repeat of its key the
// same way (ADR-0049, item 7).
func TestAccess_TheBareReply(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const complaint = `{"name": "Complaint",
 "title": "\"About \" & about",
 "variables": {"about": {"type": "text"}, "text": {"type": "text"}},
 "states": {"initial": "filed", "list": ["filed", "heard"]},
 "transitions": [{"name": "hear", "from": "filed", "to": "heard"}],
 "access": {"read": "INGROUP(\"hr\")"}}`
	if _, err := s.DefineClass(ctx, []byte(complaint)); err != nil {
		t.Fatal(err)
	}
	isBare := func(what string, d *Document, err error) {
		t.Helper()
		if err != nil || d == nil || d.ID == "" || d.Version != 1 || d.State != "filed" || len(d.Values) != 0 || d.Title != "" {
			t.Errorf("%s: %v %s", what, err, dump(d))
		}
	}
	doc, err := s.Create(ctx, "Complaint", Values{"about": "the kitchen", "text": "no spoons"}, As(clerkAnn), Key("c-1"))
	isBare("the reply to one who may not see what she filed", doc, err)
	again, err := s.Create(ctx, "Complaint", Values{"about": "the kitchen", "text": "no spoons"}, As(clerkAnn), Key("c-1"))
	isBare("the reply to the same key again", again, err)
	if again.ID != doc.ID {
		t.Errorf("the repeat made a second document: %s and %s", doc.ID, again.ID)
	}
	// Whoever may see the document is answered with it, by the same key.
	seen, err := s.Create(ctx, "Complaint", Values{"about": "the kitchen", "text": "no spoons"}, As(hr), Key("c-1"))
	if err != nil || seen.ID != doc.ID || seen.Values["text"] != "no spoons" || seen.Title != "About the kitchen" {
		t.Errorf("the reply to the key for one who may see: %v %s", err, dump(seen))
	}
	pre, err := s.Preview(ctx, "Complaint", "", Values{"about": "the lift"}, "", As(clerkAnn))
	if err != nil || len(pre.Errors) != 0 || len(pre.Document.Values) != 0 {
		t.Errorf("the preview of a create its caller will not see: %v %s", err, dump(pre))
	}
	if n, _ := s.Count(ctx, Filter{Class: "Complaint"}, As(root)); n != 1 {
		t.Errorf("complaints: %d", n)
	}
}

// What a rule does to another document it does as the person who set it
// off (ADR-0044, item 11): the rules of the target are asked of them, and
// what they may not do is a dead row that says so. What the engine does
// by itself asks nothing: the timed transition below moves a document
// that nobody but HR may see or change.
func TestAccess_RulesAndTimers(t *testing.T) {
	now := &clock{now: start}
	s, _ := openStore(t, Options{Now: now.Now})
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Vault", "variables": {"n": {"type": "number", "mode": "triggered", "on": ["refresh"], "formula": "1"}},
 "states": {"initial": "shut", "list": ["shut"]},
 "access": {"read": "INGROUP(\"hr\")", "create": "INGROUP(\"hr\")"}}`,
		`{"name": "Slip", "variables": {"vault": {"type": "text"}},
 "states": {"initial": "new", "list": ["new", "sent", "late"]},
 "transitions": [{"name": "send", "from": "new", "to": "sent"}, {"from": "sent", "to": "late", "after": "1h"}],
 "access": {"read": "INGROUP(\"hr\") OR STATE() = \"new\"", "update": "STATE() = \"new\""},
 "rules": [{"on": "transition", "name": "send",
            "do": [{"action": "create", "class": "Vault", "values": {}},
                   {"action": "refresh", "class": "Vault", "id": "vault"}]}]}`,
	} {
		if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := s.Create(ctx, "Vault", nil, As(hr))
	if err != nil {
		t.Fatal(err)
	}
	slip, err := s.Create(ctx, "Slip", Values{"vault": vault.ID}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	// Ann sends her slip, and with it loses sight of it: the reply is bare.
	sent, err := s.Transition(ctx, "Slip", slip.ID, "send", nil, As(clerkAnn))
	if err != nil || sent.State != "sent" || len(sent.Values) != 0 {
		t.Fatalf("ann sends the slip: %v %s", err, dump(sent))
	}
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	dead, err := s.Outbox(ctx, OutboxFilter{Status: ActionDead})
	if err != nil || len(dead) != 2 {
		t.Fatalf("the actions ann may not cause: %v %s", err, dump(dead))
	}
	for _, row := range dead {
		if row.ErrorCode != CodeForbidden || row.PrincipalID != "ann" {
			t.Errorf("a dead action: %s", dump(row))
		}
		want := "ann may not create this document of Vault"
		if row.Action == "refresh" {
			// The row says the true reason, not that there is no such
			// document: whoever operates the queue reads it.
			want = "ann may not see the document " + vault.ID
		}
		if !strings.Contains(row.ErrorMessage, want) {
			t.Errorf("the message of the dead %s: %s", row.Action, row.ErrorMessage)
		}
	}
	if n, _ := s.Count(ctx, Filter{Class: "Vault"}, As(root)); n != 1 {
		t.Errorf("vaults: %d", n)
	}
	// The timer comes. The wake-up is made as ann, who can neither see
	// nor change the slip any more, and the slip moves all the same.
	now.mu.Lock()
	now.now = now.now.Add(2 * 3600 * 1e9)
	now.mu.Unlock()
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "Slip", slip.ID, As(hr))
	if err != nil || got.State != "late" || got.StateEnteredBy != "ann" {
		t.Errorf("the slip after its hour: %v %s", err, dump(got))
	}
	// The same rule set off by HR does both.
	slip2, _ := s.Create(ctx, "Slip", Values{"vault": vault.ID}, As(hr))
	if _, err := s.Transition(ctx, "Slip", slip2.ID, "send", nil, As(hr)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Vault"}, As(root)); n != 2 {
		t.Errorf("vaults after HR sent a slip: %d", n)
	}
	if dead, _ := s.Outbox(ctx, OutboxFilter{Status: ActionDead}); len(dead) != 2 {
		t.Errorf("dead actions after HR sent a slip: %s", dump(dead))
	}
}

// A file is opened by one who sees the document and the variable that
// holds it (ADR-0049, item 11).
func TestAccess_Files(t *testing.T) {
	s, ctx := openClaims(t)
	fact, err := s.PutBlob(ctx, "scan.pdf", "application/pdf", strings.NewReader("%PDF-1.4 a taxi receipt"))
	if err != nil {
		t.Fatal(err)
	}
	hash := fact["hash"].(string)
	doc, err := s.Create(ctx, "Claim", Values{"amount": 100, "scan": fact}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Principal{clerkAnn, root} {
		rc, info, err := s.OpenFile(ctx, "Claim", doc.ID, hash, As(p))
		if err != nil || info.Name != "scan.pdf" {
			t.Fatalf("%s opens the scan: %v", p.ID, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.HasPrefix(string(b), "%PDF") {
			t.Errorf("the bytes %s got: %q", p.ID, b)
		}
	}
	// HR sees the claim and not the scan: the document does not hold the
	// file for them. A stranger does not see the claim at all.
	if _, _, err := s.OpenFile(ctx, "Claim", doc.ID, hash, As(hr)); code(err) != CodeUnknownBlob {
		t.Errorf("one who may not read the variable opens its file: %v", err)
	}
	if _, _, err := s.OpenFile(ctx, "Claim", doc.ID, hash, As(clerkBob)); code(err) != CodeUnknownDocument {
		t.Errorf("one who may not see the document opens its file: %v", err)
	}
}

// Due lists a document only for a caller who may see it.
func TestAccess_Due(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const task = `{"name": "Task",
 "variables": {"owner": {"type": "text", "default": "USER()"}, "ready": {"type": "bool"}},
 "states": {"initial": "open", "list": ["open", "done"]},
 "transitions": [{"from": "open", "to": "done", "guard": "LOOKUP(Flags, on, name = \"go\")"}],
 "access": {"read": "owner = USER()"}}`
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Flags", "columns": {"name": "text", "on": "bool"}, "key": ["name"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(task)); err != nil {
		t.Fatal(err)
	}
	mine, _ := s.Create(ctx, "Task", nil, As(clerkAnn))
	if _, err := s.Create(ctx, "Task", nil, As(clerkBob)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Flags", []Values{{"name": "go", "on": true}}); err != nil {
		t.Fatal(err)
	}
	due, err := s.Due(ctx, "Task", As(clerkAnn))
	if err != nil || len(due) != 1 || due[0].ID != mine.ID {
		t.Errorf("what is due for ann: %v %s", err, dump(due))
	}
	if due, err := s.Due(ctx, "Task", As(root)); err != nil || len(due) != 2 {
		t.Errorf("what is due for an administrator: %v %s", err, dump(due))
	}
	if due, err := s.Due(ctx, "Task", As(guest)); err != nil || len(due) != 0 {
		t.Errorf("what is due for a stranger: %v %s", err, dump(due))
	}
}

// A class that leaves a rule out answers as it always has, until the
// owner rules on question A of ADR-0049: a transition with no rule and a
// class with no update rule is open, and a document of a class with no
// read rule is seen by every caller.
func TestAccess_ARuleLeftOut(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const memo = `{"name": "Memo", "variables": {"text": {"type": "text"}},
 "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b"}]}`
	if _, err := s.DefineClass(ctx, []byte(memo)); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Memo", Values{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Memo", doc.ID, Values{"text": "hello again"}, As(guest)); err != nil {
		t.Errorf("a set on a class without rules: %v", err)
	}
	if _, err := s.Transition(ctx, "Memo", doc.ID, "go", nil, As(guest)); err != nil {
		t.Errorf("a transition on a class without rules: %v", err)
	}
	if got, err := s.Get(ctx, "Memo", doc.ID, As(clerkBob)); err != nil || got.Values["text"] != "hello again" {
		t.Errorf("a read on a class without rules: %v", err)
	}
	if err := s.Delete(ctx, "Memo", doc.ID, As(guest)); err != nil {
		t.Errorf("a delete on a class without rules: %v", err)
	}
}

// A delete is tried first like any other command (ADR-0049, item 13):
// the preview says whether its caller may, and writes nothing.
func TestAccess_PreviewDelete(t *testing.T) {
	s, ctx := openClaims(t)
	doc, err := s.Create(ctx, "Claim", Values{"amount": 100, "note": "taxi"}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	pre, err := s.PreviewDelete(ctx, "Claim", doc.ID, As(clerkAnn))
	if err != nil || len(pre.Errors) != 1 || pre.Errors[0].Code != CodeForbidden || pre.Errors[0].Path != "/access/delete" || pre.Document.Values["note"] != "taxi" {
		t.Errorf("the preview of a delete its caller may not make: %v %s", err, dump(pre))
	}
	pre, err = s.PreviewDelete(ctx, "Claim", doc.ID, As(hr))
	if err != nil || len(pre.Errors) != 0 || pre.Document.ID != doc.ID {
		t.Errorf("the preview of a delete its caller may make: %v %s", err, dump(pre))
	}
	if _, err := s.PreviewDelete(ctx, "Claim", doc.ID, As(clerkBob)); code(err) != CodeUnknownDocument {
		t.Errorf("the preview of a delete of a hidden document: %v", err)
	}
	if got, err := s.Get(ctx, "Claim", doc.ID, As(clerkAnn)); err != nil || got.Version != 1 {
		t.Errorf("the document after the previews: %v", err)
	}
	if ev, _ := s.Events(ctx, "Claim", doc.ID, As(root)); len(ev) != 1 {
		t.Errorf("events after the previews: %d", len(ev))
	}
}

// A read rule with a comparison to a literal is narrowed through the
// bare column as a where is (ADR-0038), and a document that an older
// version of the class computed is read under the rule of the current
// version, as its rights are (ADR-0041).
func TestAccess_LiteralsAndOlderVersions(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	note := func(access string) string {
		return `{"name": "Note", "variables": {"region": {"type": "text", "index": true}, "owner": {"type": "text", "default": "USER()"}, "n": {"type": "number"}},
 "states": {"initial": "a", "list": ["a"]}` + access + `}`
	}
	if _, err := s.DefineClass(ctx, []byte(note(""))); err != nil {
		t.Fatal(err)
	}
	eu, _ := s.Create(ctx, "Note", Values{"region": "EU", "n": 1}, As(clerkAnn))
	us, _ := s.Create(ctx, "Note", Values{"region": "US", "n": 2}, As(clerkBob))
	if n, _ := s.Count(ctx, Filter{Class: "Note"}, As(guest)); n != 2 {
		t.Fatalf("before the class has a rule a stranger counts %d", n)
	}
	// The class gains a read rule: a new version, which touches no
	// document.
	if _, err := s.DefineClass(ctx, []byte(note(`, "access": {"read": "region = \"EU\" AND (n > 0 OR owner = USER())"}`))); err != nil {
		t.Fatal(err)
	}
	for _, where := range []string{"", "n > 0", "region <> \"\""} {
		f := Filter{Class: "Note", Where: where}
		docs, err := s.Query(ctx, f, As(guest))
		if err != nil || len(docs) != 1 || docs[0].ID != eu.ID || docs[0].ClassVersion != 1 {
			t.Errorf("the filter %q under the new rule: %v %s", where, err, dump(docs))
		}
		if n, err := s.Count(ctx, f, As(guest)); err != nil || n != 1 {
			t.Errorf("the count %q under the new rule: %v %d", where, err, n)
		}
		if n, err := s.Count(ctx, f, As(root)); err != nil || n != 2 {
			t.Errorf("an administrator's count %q: %v %d", where, err, n)
		}
	}
	if got, err := s.Get(ctx, "Note", eu.ID, As(guest)); err != nil || got.ClassVersion != 1 {
		t.Errorf("a read of a document of the older version the rule lets through: %v", err)
	}
	// Its owner does not see the other one either: the rule says EU.
	if _, err := s.Get(ctx, "Note", us.ID, As(clerkBob)); code(err) != CodeUnknownDocument {
		t.Errorf("a read of a document of the older version the rule hides: %v", err)
	}
	if _, err := s.Set(ctx, "Note", us.ID, Values{"n": 3}, As(clerkBob)); code(err) != CodeUnknownDocument {
		t.Errorf("a set on it: %v", err)
	}
	if got, err := s.Set(ctx, "Note", eu.ID, Values{"n": 5}, As(guest)); err != nil || got.ClassVersion != 2 {
		t.Errorf("a set on the one the rule lets through adopts the version: %v", err)
	}
}
