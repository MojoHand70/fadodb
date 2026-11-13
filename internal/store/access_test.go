package store

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
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
	// root is in all four system groups (ADR-0052): it does the work that
	// is not about one document, and as a content administrator passes
	// the rules of every class.
	root = Principal{ID: "root", System: []string{GroupAdministrators, GroupDesigners, GroupContentAdministrators, GroupAnalysts}}
	// setup does the three kinds of work that are not about one document,
	// as the setup code of a host program does: it defines, operates the
	// queue and runs SQL. It is no content administrator, so the rules of
	// a class hold for it as for anybody.
	setup   = Principal{ID: "setup", System: []string{GroupAdministrators, GroupDesigners, GroupAnalysts}}
	asSetup = As(setup)
	// past is the reason a test gives where it acts past a rule as a
	// content administrator (ADR-0052, item 4).
	past = Reason("the test acts past the rule")
)

// designing is the principal as a designer too, which a caller who
// previews a draft or migrates has to be (ADR-0052).
func designing(p Principal) Option {
	p.System = append(append([]string(nil), p.System...), GroupDesigners)
	return As(p)
}

func openClaims(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(claimClass), asSetup); err != nil {
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
	if _, err := s.Create(ctx, "Claim", Values{"amount": 5000}, As(root), past); err != nil {
		t.Errorf("a content administrator passes the rule: %v", err)
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
	if _, err := s.defineOpen(ctx, []byte(complaint), asSetup); err != nil {
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
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
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
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	dead, err := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup)
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
	if _, err := s.Drain(ctx, asSetup); err != nil {
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
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Vault"}, As(root)); n != 2 {
		t.Errorf("vaults after HR sent a slip: %d", n)
	}
	if dead, _ := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup); len(dead) != 2 {
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
	if _, err := s.defineOpenTable(ctx, []byte(`{"name": "Flags", "columns": {"name": "text", "on": "bool"}, "key": ["name"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(task), asSetup); err != nil {
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
	if _, err := s.defineOpen(ctx, []byte(memo), asSetup); err != nil {
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
	if _, err := s.defineOpen(ctx, []byte(note("")), asSetup); err != nil {
		t.Fatal(err)
	}
	eu, _ := s.Create(ctx, "Note", Values{"region": "EU", "n": 1}, As(clerkAnn))
	us, _ := s.Create(ctx, "Note", Values{"region": "US", "n": 2}, As(clerkBob))
	if n, _ := s.Count(ctx, Filter{Class: "Note"}, As(guest)); n != 2 {
		t.Fatalf("before the class has a rule a stranger counts %d", n)
	}
	// The class gains a read rule: a new version, which touches no
	// document.
	if _, err := s.defineOpen(ctx, []byte(note(`, "access": {"read": "region = \"EU\" AND (n > 0 OR owner = USER())"}`)), asSetup); err != nil {
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

// Every path agrees (ADR-0049, item 14): for a set of read rules, some of
// which stop with an error on some rows, and callers the rules treat
// differently, a read by id, a filter and a count show the same
// documents. The filter folds the caller's part of a rule before it
// writes its statement, and the read by id runs the rule whole on the Go
// VM; this is the test that the two are one rule.
func TestAccess_EveryPathAgrees(t *testing.T) {
	rules := []string{
		`owner = USER() OR INGROUP(\"hr\")`,
		`INGROUP(\"hr\") OR owner = USER()`,
		`(owner = USER() OR INGROUP(\"hr\")) AND region = \"EU\"`,
		`NOT INGROUP(\"staff\") AND n >= 0`,
		`INGROUP(\"hr\") AND INGROUP(\"staff\") OR n > 1`,
		`1 / n > 0 OR INGROUP(\"hr\")`,
		`INGROUP(\"hr\") OR 1 / n > 0`,
		`1 / n > 0 AND INGROUP(\"staff\")`,
		`INGROUP(\"staff\") AND 1 / n > 0`,
		`NOT (INGROUP(\"hr\") OR owner = USER())`,
		`INGROUP(region)`,
		`owner = USER() AND STATE() = \"a\" OR INGROUP(\"hr\") AND CREATEDBY() <> USER()`,
		`n > 0`,
		`FALSE`,
		`TRUE`,
	}
	callers := []Principal{clerkAnn, clerkBob, hr, guest, {ID: "both", Groups: []string{"hr", "staff"}}, {ID: "eu", Groups: []string{"EU"}}, {}, root}
	for _, rule := range rules {
		s, _ := openStore(t, Options{})
		ctx := context.Background()
		def := `{"name": "Doc", "variables": {"owner": {"type": "text", "index": true}, "region": {"type": "text"}, "n": {"type": "number"}},
 "states": {"initial": "a", "list": ["a"]}, "access": {"read": "` + rule + `"}}`
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatalf("%s: %v", rule, err)
		}
		var ids []string
		for _, owner := range []any{"ann", "bob", "hana", nil} {
			for _, region := range []any{"EU", "US", nil} {
				for _, n := range []any{0, 1, 2, nil} {
					doc, err := s.Create(ctx, "Doc", Values{"owner": owner, "region": region, "n": n}, As(root))
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, doc.ID)
				}
			}
		}
		for _, p := range callers {
			byID := map[string]bool{}
			for _, id := range ids {
				_, err := s.Get(ctx, "Doc", id, As(p))
				switch {
				case err == nil:
					byID[id] = true
				case code(err) != CodeUnknownDocument:
					t.Fatalf("%s for %q: a read by id: %v", rule, p.ID, err)
				}
			}
			for _, where := range []string{"", "n >= 1", "region = \"EU\" OR owner = \"bob\""} {
				f := Filter{Class: "Doc", Where: where}
				// What the filter should show: what a read by id shows
				// of what an administrator's filter shows.
				all, err := s.Query(ctx, f, As(root))
				if err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for _, d := range all {
					if byID[d.ID] {
						want = append(want, d.ID)
					}
				}
				docs, err := s.Query(ctx, f, As(p))
				if err != nil {
					t.Fatalf("%s for %q: the filter %q: %v", rule, p.ID, where, err)
				}
				got := []string{}
				for _, d := range docs {
					got = append(got, d.ID)
				}
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("%s for %q: the filter %q shows %d documents and a read by id %d", rule, p.ID, where, len(got), len(want))
				}
				if n, err := s.Count(ctx, f, As(p)); err != nil || int(n) != len(want) {
					t.Errorf("%s for %q: the count %q is %d (%v) and a read by id shows %d", rule, p.ID, where, n, err, len(want))
				}
			}
		}
		s.Close()
	}
}

// A caller who sees only their own documents lists them through the
// index on the owner (ADR-0049, item 5): the caller's part of the rule is
// decided first, and what is left compares a column with one value.
func TestAccess_OwnDocumentsThroughTheIndex(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const def = `{"name": "Doc", "variables": {"owner": {"type": "text", "default": "USER()", "index": true}, "n": {"type": "number"}},
 "states": {"initial": "a", "list": ["a"]}, "access": {"read": "owner = USER() OR INGROUP(\"hr\")"}}`
	if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		p := clerkAnn
		if i%5 != 0 {
			p = Principal{ID: "u" + strings.Repeat("x", i%7), Groups: []string{"staff"}}
		}
		if _, err := s.Create(ctx, "Doc", Values{"n": i}, As(p)); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(p Principal) string {
		t.Helper()
		c, _ := s.class("Doc")
		o := options{principal: p}
		compiled, err := query.Compile(query.Filter{Class: "Doc"}, c.cls, s.sources(), caller(o))
		if err != nil {
			t.Fatal(err)
		}
		text := `SELECT "id" FROM ` + quote(c.cls.Name)
		if compiled.Where != "" {
			text += " WHERE " + compiled.Where
		}
		rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+text, append(append([]any{}, compiled.Args...), s.contextArgs(o)...)...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var detail string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			detail += d + "\n"
		}
		return detail
	}
	if detail := plan(clerkAnn); !strings.Contains(detail, "doc_Doc_owner") || strings.Contains(detail, "SCAN") {
		t.Errorf("a clerk's listing does not read through the index on owner:\n%s", detail)
	}
	if detail := plan(hr); strings.Contains(detail, "doc_Doc_owner") {
		t.Errorf("HR's listing, which the rule does not narrow, reads the index on owner:\n%s", detail)
	}
	if docs, err := s.Query(ctx, Filter{Class: "Doc"}, As(clerkAnn)); err != nil || len(docs) != 10 {
		t.Errorf("ann's listing: %v %d", err, len(docs))
	}
	if n, err := s.Count(ctx, Filter{Class: "Doc"}, As(hr)); err != nil || n != 50 {
		t.Errorf("HR's count: %v %d", err, n)
	}
	if n, err := s.Count(ctx, Filter{Class: "Doc"}, As(guest)); err != nil || n != 0 {
		t.Errorf("a stranger's count: %v %d", err, n)
	}
}

// A rule a class leaves out lets nobody through (ADR-0052, item 5), and
// the class is accepted with a note for each: a finding that refuses
// nothing and says what is missing.
func TestAccess_LeftOutIsNoted(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const slip = `{"name": "Slip", "variables": {"note": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	notes := s.Validate([]byte(slip))
	var paths []string
	for _, d := range notes {
		if !d.Note || d.Code != model.CodeRuleLeftOut || d.Hint == "" || !strings.Contains(d.Message, "Slip has no ") {
			t.Errorf("a note of a rule left out: %+v", d)
		}
		paths = append(paths, d.Path)
	}
	if got := strings.Join(paths, " "); got != "/access/read /access/create /access/update /access/delete" {
		t.Errorf("the notes of a class with no access block: %s", got)
	}
	if notes.Refuses() || notes.Err() != nil {
		t.Errorf("notes refuse nothing: %v", notes.Err())
	}
	// A class that writes two of its rules is noted for the other two, in
	// a set under the position of its definition; one that writes all four
	// for none; a class without states for the two its previews ask.
	for what, want := range map[string]struct {
		defs  []string
		paths string
	}{
		"two rules written":      {[]string{slip, `{"name": "Half", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a"]}, "access": {"read": "TRUE", "delete": "FALSE"}}`}, "/0/access/read /0/access/create /0/access/update /0/access/delete /1/access/create /1/access/update"},
		"all four written":       {[]string{`{"name": "Full", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "FALSE", "delete": "FALSE"}}`}, ""},
		"a class without states": {[]string{`{"name": "Sum", "variables": {"a": {"type": "number"}, "twice": {"formula": "a * 2"}}}`}, "/0/access/read /0/access/create"},
	} {
		var defs [][]byte
		for _, d := range want.defs {
			defs = append(defs, []byte(d))
		}
		var got []string
		for _, d := range s.ValidateClasses(defs) {
			got = append(got, d.Path)
		}
		if strings.Join(got, " ") != want.paths {
			t.Errorf("%s: notes at %s, want %s", what, strings.Join(got, " "), want.paths)
		}
	}
	// What refuses a definition is reported alone, without the notes.
	if diags := s.Validate([]byte(`{"name": "Bad", "variables": {"n": {"formula": "1 +"}}, "states": {"initial": "a", "list": ["a"]}}`)); !diags.Refuses() || diags.Has(model.CodeRuleLeftOut) {
		t.Errorf("a definition that is refused: %v", diags)
	}
	// The class is defined all the same, and carries its notes.
	cls, err := s.DefineClass(ctx, []byte(slip), asSetup)
	if err != nil || len(cls.Notes) != 4 {
		t.Fatalf("defining a class with no access block: %v, %d notes", err, len(cls.Notes))
	}
	// Nobody but a content administrator reaches its documents, and the
	// refusal says which rule is missing and what to add.
	_, err = s.Create(ctx, "Slip", Values{"note": "x"}, As(clerkAnn))
	if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/access/create" || !strings.Contains(e.Hint, "add access.create") {
		t.Errorf("creating where the class says nothing: %v", err)
	}
	doc, err := s.Create(ctx, "Slip", Values{"note": "x"}, As(root), past)
	if err != nil {
		t.Fatalf("a content administrator creates: %v", err)
	}
	if _, err := s.Get(ctx, "Slip", doc.ID, As(clerkAnn)); code(err) != CodeUnknownDocument {
		t.Errorf("seeing where the class says nothing: %v", err)
	}
	if n, err := s.Count(ctx, Filter{Class: "Slip"}, As(clerkAnn)); err != nil || n != 0 {
		t.Errorf("counting where the class says nothing: %v %d", err, n)
	}
	if n, err := s.Count(ctx, Filter{Class: "Slip"}, As(root)); err != nil || n != 1 {
		t.Errorf("a content administrator counts: %v %d", err, n)
	}
	// A table that says nothing is the same: no rows for anybody but a
	// content administrator, and a formula still reads it (ADR-0046).
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	_, err = s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": 4.3}}, As(clerkAnn))
	if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/access/write" || !strings.Contains(e.Hint, "add access.write") {
		t.Errorf("writing a table that says nothing: %v", err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": 4.3}}, As(root)); err != nil {
		t.Fatalf("a content administrator writes the table: %v", err)
	}
	if _, err := s.TableRows(ctx, "Rates", As(clerkAnn)); code(err) != CodeForbidden {
		t.Errorf("reading a table that says nothing: %v", err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Quote", "variables": {"rate": {"formula": "LOOKUP(Rates, rate, code = \"EUR\")", "type": "number"}}, "states": {"initial": "a", "list": ["a"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if q, err := s.Create(ctx, "Quote", nil, As(clerkAnn)); err != nil || q.Values["rate"] != 4.3 {
		t.Errorf("a formula reads the table with the engine's eyes: %v %s", err, dump(q))
	}
}

// A content administrator gets past a rule by the group alone, and that
// is an override (ADR-0052, item 4): it needs a reason, and the event of
// the commit keeps the reason beside the rules it lifted. A command the
// rules let through is an ordinary one, whoever sends it.
func TestAccess_Override(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Log", "variables": {"about": {"type": "text"}}, "states": {"initial": "kept", "list": ["kept"]},
 "access": {"read": "INGROUP(\"everyone\")", "create": "FALSE", "update": "FALSE", "delete": "FALSE"}}`,
		`{"name": "Memo",
 "variables": {"owner": {"type": "text", "default": "USER()"}, "total": {"type": "number", "default": "0"}, "secret": {"type": "text"}},
 "states": {"initial": "draft", "list": ["draft", "sent", "filed"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent", "guard": "total > 0"}, {"from": "sent", "to": "filed", "guard": "total > 0"}],
 "rules": [{"on": "transition", "name": "send", "do": [{"action": "create", "class": "Log", "values": {"about": "ID()"}}]}],
 "access": {"read": "owner = USER()", "create": "INGROUP(\"everyone\")", "update": "owner = USER()", "delete": "FALSE",
            "transition": {"send": "owner = USER()"}},
 "rights": {"*": {"secret": {"write": ["hr"]}}}}`,
	} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	cora := Principal{ID: "cora", System: []string{GroupContentAdministrators}}
	why := Reason("the owner left the company")
	memo, err := s.Create(ctx, "Memo", Values{"total": 5}, As(clerkAnn))
	if err != nil {
		t.Fatal(err)
	}
	marks := func(class, id string) []string {
		t.Helper()
		events, err := s.Events(ctx, class, id, As(cora))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range events {
			out = append(out, strings.TrimSpace(e.Kind+":"+e.Name+" "+e.ActorID+" ["+e.Override+"] "+e.Reason))
		}
		return out
	}
	// Without a reason the command is refused, and names the rules.
	_, err = s.Set(ctx, "Memo", memo.ID, Values{"total": 6}, As(cora))
	if e, ok := err.(*Error); !ok || e.Code != CodeReasonRequired || e.Path != "/access/read" || !strings.Contains(e.Message, "/access/read and /access/update") || e.Hint == "" {
		t.Fatalf("an override without a reason: %v", err)
	}
	// A preview says which rules the command would lift, and that it
	// needs a reason; with one it reports no error.
	pv, err := s.Preview(ctx, "Memo", memo.ID, Values{"total": 6}, "", As(cora))
	if err != nil || strings.Join(pv.Override, " ") != "/access/read /access/update" || len(pv.Errors) != 1 || pv.Errors[0].Code != CodeReasonRequired {
		t.Errorf("the preview of an override without a reason: %v %s", err, dump(pv))
	}
	pv, err = s.Preview(ctx, "Memo", memo.ID, Values{"total": 6}, "", As(cora), why)
	if err != nil || strings.Join(pv.Override, " ") != "/access/read /access/update" || len(pv.Errors) != 0 {
		t.Errorf("the preview of an override with a reason: %v %s", err, dump(pv))
	}
	// A read by override asks no reason and leaves no trace.
	if got, err := s.Get(ctx, "Memo", memo.ID, As(cora)); err != nil || got.Values["total"] != 5.0 {
		t.Errorf("a content administrator reads: %v %s", err, dump(got))
	}
	// With a reason the command is made, and its event keeps both.
	if _, err := s.Set(ctx, "Memo", memo.ID, Values{"total": 6}, As(cora), why); err != nil {
		t.Fatalf("an override with a reason: %v", err)
	}
	// A write right is a rule too.
	_, err = s.Set(ctx, "Memo", memo.ID, Values{"secret": "x"}, As(cora))
	if e, ok := err.(*Error); !ok || e.Code != CodeReasonRequired || !strings.Contains(e.Message, "/rights/draft/secret/write") {
		t.Errorf("a write past a right, without a reason: %v", err)
	}
	// The listing says what taking the transition would lift.
	infos, err := s.Transitions(ctx, "Memo", memo.ID, As(cora))
	if err != nil || len(infos) != 1 || infos[0].Name != "send" || !infos[0].Enabled || infos[0].Override != "/access/transition/send" {
		t.Errorf("the listing for a content administrator: %v %s", err, dump(infos))
	}
	if infos, _ := s.Transitions(ctx, "Memo", memo.ID, As(clerkAnn)); len(infos) != 1 || !infos[0].Enabled || infos[0].Override != "" {
		t.Errorf("the listing for the owner: %s", dump(infos))
	}
	// The transition by override: its own event is marked, the eventless
	// move that follows is the engine's and is not.
	if _, err := s.Transition(ctx, "Memo", memo.ID, "send", nil, As(cora), why); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"created: ann []",
		"set: cora [/access/read /access/update] the owner left the company",
		"transition:send cora [/access/read /access/transition/send] the owner left the company",
		"transition: cora []",
	}
	if got := marks("Memo", memo.ID); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the events of the memo:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// What the commit queued acts as the same caller for the same reason:
	// nobody may create a Log, and the rule's action does by override.
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("draining: %d %v", n, err)
	}
	logs, err := s.Query(ctx, Filter{Class: "Log"}, As(clerkAnn))
	if err != nil || len(logs) != 1 {
		t.Fatalf("the log the rule made: %v %d", err, len(logs))
	}
	if got := marks("Log", logs[0].ID); len(got) != 1 || got[0] != "created: cora [/access/create] the owner left the company" {
		t.Errorf("the event of the log: %v", got)
	}
	// The owner's own transition queues the same action, and it is dead:
	// she is no content administrator.
	own, _ := s.Create(ctx, "Memo", Values{"total": 1}, As(clerkAnn))
	if _, err := s.Transition(ctx, "Memo", own.ID, "send", nil, As(clerkAnn), why); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	if dead, _ := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup); len(dead) != 1 || dead[0].ErrorCode != CodeForbidden {
		t.Errorf("the action of the owner's commit: %s", dump(dead))
	}
	// A reason given where the rules let the caller through is ignored.
	if got := marks("Memo", own.ID); got[0] != "created: ann []" || got[1] != "transition:send ann []" {
		t.Errorf("the events of a memo nobody overrode: %v", got)
	}
	// A content administrator's own document is an ordinary one.
	hers, err := s.Create(ctx, "Memo", Values{"total": 2}, As(cora))
	if err != nil {
		t.Fatalf("a content administrator creates by the rule: %v", err)
	}
	if _, err := s.Set(ctx, "Memo", hers.ID, Values{"total": 3}, As(cora)); err != nil {
		t.Errorf("a content administrator changes her own memo, no reason asked: %v", err)
	}
	if got := marks("Memo", hers.ID); len(got) != 2 || got[1] != "set: cora []" {
		t.Errorf("the events of her own memo: %v", got)
	}
	// Every override, by whom, of which rule and why, is one statement.
	rows, err := s.SQL(ctx, `SELECT kind, actor_id, override, reason FROM Memo_events WHERE override IS NOT NULL ORDER BY ts, event_id`, nil, asSetup)
	if err != nil || len(rows.Rows) != 2 || fmt.Sprint(rows.Rows[0]) != "[set cora /access/read /access/update the owner left the company]" {
		t.Errorf("the overrides of the class: %v %v", err, rows)
	}
	// A delete past the rule that says nobody deletes.
	if err := s.Delete(ctx, "Memo", hers.ID, As(cora)); code(err) != CodeReasonRequired {
		t.Errorf("a delete past the rule, without a reason: %v", err)
	}
	if err := s.Delete(ctx, "Memo", hers.ID, As(cora), why); err != nil {
		t.Errorf("a delete past the rule, with a reason: %v", err)
	}
	if got := marks("Memo", hers.ID); len(got) != 3 || got[2] != "deleted: cora [/access/delete] the owner left the company" {
		t.Errorf("the events after the delete: %v", got)
	}
}
