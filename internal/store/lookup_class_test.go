package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

const lkPurchaseBare = `{"name": "Purchase",
 "variables": {"needed": {"type": "number", "default": "2"}},
 "states": {"initial": "waiting", "list": ["waiting", "approved"]},
 "transitions": []}`

const lkApproval = `{"name": "Approval",
 "variables": {"purchase": {"type": "text", "required": true, "index": true}, "amount": {"type": "decimal(2)"}},
 "states": {"initial": "asked", "list": ["asked", "granted", "refused"]},
 "transitions": [{"name": "grant", "from": "asked", "to": "granted"},
                 {"name": "refuse", "from": "asked", "to": "refused"}],
 "rules": [{"on": "enter", "state": "granted",
            "do": [{"action": "refresh", "class": "Purchase", "id": "purchase"}]}]}`

const lkPurchase = `{"name": "Purchase",
 "variables": {
   "needed":  {"type": "number", "default": "2"},
   "granted": {"type": "number", "formula": "LOOKUP(Approval, COUNT(id), purchase = ID() AND state = \"granted\")",
               "mode": "triggered", "on": ["refresh"]},
   "sum":     {"type": "decimal(2)", "formula": "LOOKUP(Approval, SUM(amount), state = \"granted\" AND purchase = ID())",
               "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "waiting", "list": ["waiting", "approved"]},
 "transitions": [{"from": "waiting", "to": "approved", "guard": "granted >= needed"}]}`

// defineApprovals defines the two classes that name each other: the
// purchase without its count, the approval, whose rule names the
// purchase, and the purchase again with the count that reads approvals.
func defineApprovals(t testing.TB, s *Store) {
	t.Helper()
	for _, def := range []string{lkPurchaseBare, lkApproval, lkPurchase} {
		if _, err := s.defineOpen(context.Background(), []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLookup_ParallelApprovals is the pattern ADR-0046 exists for: each
// approval is a document; granting one refreshes the purchase through
// the outbox, the purchase counts its granted approvals with a LOOKUP
// over the class, and its own guard moves it when enough are in.
func TestLookup_ParallelApprovals(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApprovals(t, s)
	p, err := s.Create(ctx, "Purchase", Values{}, WithID("P-1"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Values["granted"] != nil || p.State != "waiting" {
		t.Fatalf("a new purchase: %s", dump(p))
	}
	other, err := s.Create(ctx, "Purchase", Values{}, WithID("P-2"))
	if err != nil {
		t.Fatal(err)
	}
	for i, purchase := range []string{"P-1", "P-1", "P-1", "P-2"} {
		if _, err := s.Create(ctx, "Approval", Values{"purchase": purchase, "amount": "10.25"}, WithID("A-"+itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	grant := func(id string) {
		t.Helper()
		if _, err := s.Transition(ctx, "Approval", id, "grant", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Drain(ctx, asSetup); err != nil {
			t.Fatal(err)
		}
	}
	grant("A-0")
	p, _ = s.Get(ctx, "Purchase", "P-1")
	if p.Values["granted"] != 1.0 || p.Values["sum"] != "10.25" || p.State != "waiting" {
		t.Fatalf("after one approval: %s", dump(p))
	}
	// A refusal refreshes nothing, and an approval of another purchase
	// counts for that one.
	if _, err := s.Transition(ctx, "Approval", "A-1", "refuse", nil); err != nil {
		t.Fatal(err)
	}
	grant("A-3")
	p, _ = s.Get(ctx, "Purchase", "P-1")
	other, _ = s.Get(ctx, "Purchase", other.ID)
	if p.Values["granted"] != 1.0 || p.State != "waiting" || other.Values["granted"] != 1.0 || other.State != "waiting" {
		t.Fatalf("after a refusal and a foreign approval: %s %s", dump(p), dump(other))
	}
	// The second approval is enough: the refresh computes 2, and the
	// eventless transition of the same command moves the purchase.
	grant("A-2")
	p, _ = s.Get(ctx, "Purchase", "P-1")
	if p.Values["granted"] != 2.0 || p.Values["sum"] != "20.50" || p.State != "approved" {
		t.Fatalf("after two approvals: %s", dump(p))
	}
	// Both lookups were answered through the index of purchase, whichever
	// way the author wrote the terms.
	c, _ := s.class("Purchase")
	for _, name := range []string{"granted", "sum"} {
		q := c.byName[strings.ToUpper(name)].v.Formula.Queries[0]
		plan := s.plans[&c.byName[strings.ToUpper(name)].v.Formula.Queries[0]]
		if q.IndexColumn != "purchase" || !q.KeyPart || plan == nil || plan.keyed == nil || plan.stmt != nil || plan.tbl != nil {
			t.Errorf("%s: key %s part %v plan %+v", name, q.IndexColumn, q.KeyPart, plan)
		}
	}
	// SQLite finds the approvals of the purchase through the index of the
	// variable, not by reading the class.
	granted := s.plans[&c.byName["GRANTED"].v.Formula.Queries[0]]
	steps, err := s.writer.QueryContext(ctx, "EXPLAIN QUERY PLAN "+granted.keyedSQL, sql.Named("k", "P-1"), sql.Named("id", "P-1"))
	if err != nil {
		t.Fatal(err)
	}
	how := ""
	for steps.Next() {
		var id, parent, unused int
		var detail string
		if err := steps.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		how += detail + "; "
	}
	steps.Close()
	if !strings.Contains(how, "USING INDEX doc_Approval_purchase (purchase=?)") || strings.Contains(how, "SCAN") {
		t.Errorf("the count of a purchase's approvals runs as: %s", how)
	}
	// A preview reads the documents as they are stored now and writes
	// nothing: a draft whose default counts the granted approvals.
	const tally = `{"name": "Tally", "variables": {"granted": {"type": "number", "default": "LOOKUP(Approval, COUNT(id), state = \"granted\")"},
  "largest": {"type": "decimal(2)", "default": "LOOKUP(Approval, MAX(amount), TRUE)"}}, "states": {"initial": "open", "list": ["open"]}}`
	pv, err := s.PreviewDraft(ctx, opened([]byte(tally)), "", Values{}, "", asSetup)
	if err != nil || pv.Document.Values["granted"] != 3.0 || pv.Document.Values["largest"] != "10.25" {
		t.Errorf("preview of a draft: %v %s", err, dump(pv))
	}
	// Everything comes back after a reopen, whichever class is read first.
	path := s.Path()
	s.Close()
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Create(ctx, "Approval", Values{"purchase": other.ID, "amount": "1.00"}, WithID("A-9")); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Transition(ctx, "Approval", "A-9", "grant", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	other, _ = s2.Get(ctx, "Purchase", other.ID)
	if other.Values["granted"] != 2.0 || other.Values["sum"] != "11.25" || other.State != "approved" {
		t.Errorf("after the reopen: %s", dump(other))
	}
}

// TestLookup_ClassDefinition is what a definition that reads a class is
// refused for, and what a redefinition of a class that is read is.
func TestLookup_ClassDefinition(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApprovals(t, s)
	refused := func(def, code, path, says string) {
		t.Helper()
		_, err := s.defineOpen(ctx, []byte(def), asSetup)
		var diags model.Diagnostics
		if !errors.As(err, &diags) {
			t.Errorf("want a refusal with %q, got %v", says, err)
			return
		}
		for _, d := range diags {
			if d.Code == code && d.Path == path && strings.Contains(d.Message+" "+d.Hint, says) {
				return
			}
		}
		t.Errorf("want %s at %s saying %q, got %v", code, path, says, diags)
	}
	const states = `"states": {"initial": "open", "list": ["open"]}}`
	// An auto formula may not read other documents.
	refused(`{"name": "Auto", "variables": {"n": {"type": "number", "formula": "LOOKUP(Approval, COUNT(id), TRUE)"}}, `+states,
		model.CodeBadFormula, "/variables/n/formula", "an auto formula may not read other documents")
	// A variable computed from other documents declares its type, and the
	// engine says which.
	refused(`{"name": "Untyped", "variables": {"n": {"formula": "LOOKUP(Approval, SUM(amount), TRUE)", "mode": "triggered", "on": ["refresh"]}}, `+states,
		model.CodeMissing, "/variables/n/type", `"type": "decimal(2)"`)
	// The columns are the system columns and the scalar variables.
	refused(`{"name": "Wrong", "variables": {"n": {"type": "number", "formula": "LOOKUP(Approval, COUNT(id), nope = 1)", "mode": "triggered", "on": ["refresh"]}}, `+states,
		model.CodeBadFormula, "/variables/n/formula", "unknown column nope of Approval; its columns are id, title, state, version, created_at, updated_at, state_entered_at, created_by, updated_by, state_entered_by, state_entered_from, purchase, amount")
	refused(`{"name": "Unknown", "variables": {"n": {"type": "number", "formula": "LOOKUP(Nobody, COUNT(id), TRUE)", "mode": "triggered", "on": ["refresh"]}}, `+states,
		model.CodeBadFormula, "/variables/n/formula", "unknown source Nobody")
	// A redefinition may not take away, or retype, what another class's
	// formula reads; the reader is changed first.
	refused(strings.Replace(lkApproval, `, "amount": {"type": "decimal(2)"}`, ``, 1),
		model.CodeNotAllowed, "/variables", "it would break a formula of the class Purchase, at /variables/sum/formula")
	refused(strings.Replace(lkApproval, `"amount": {"type": "decimal(2)"}`, `"amount": {"type": "text"}`, 1),
		model.CodeNotAllowed, "/variables", "it would break a formula of the class Purchase")
	refused(strings.Replace(lkApproval, `"amount": {"type": "decimal(2)"}`, `"amount": {"type": "decimal(4)"}`, 1),
		model.CodeNotAllowed, "/variables", "it would break a formula of the class Purchase, at /variables/sum/formula")
	// One that keeps what is read goes through.
	if _, err := s.defineOpen(ctx, []byte(strings.Replace(lkApproval, `"amount": {"type": "decimal(2)"}`, `"amount": {"type": "decimal(2)"}, "note": {"type": "text"}`, 1)), asSetup); err != nil {
		t.Errorf("adding a variable to a class that is read: %v", err)
	}
	// An object is not a column yet, and the id leads an index like an
	// indexed variable: the key is the equality on it, wherever it stands.
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Site", "variables": {"address": {"type": "object", "schema": "Address"}, "kind": {"type": "text"}}, `+states), asSetup); err != nil {
		t.Fatal(err)
	}
	refused(`{"name": "Visit", "variables": {"n": {"type": "number", "formula": "LOOKUP(Site, COUNT(address), TRUE)", "mode": "triggered", "on": ["refresh"]}}, `+states,
		model.CodeBadFormula, "/variables/n/formula", "unknown column address of Site; its columns are id, title, state, version, created_at, updated_at, state_entered_at, created_by, updated_by, state_entered_by, state_entered_from, kind")
	visit, err := s.defineOpen(ctx, []byte(`{"name": "Visit", "variables": {"site": {"type": "text"},
  "kind": {"type": "text", "formula": "LOOKUP(Site, kind, kind <> \"\" AND state = \"open\" AND id = THIS.site)", "mode": "triggered", "on": ["refresh"]}}, `+states), asSetup)

	if err != nil {
		t.Fatal(err)
	}
	if v, _ := visit.Variable("kind"); v.Formula.Queries[0].IndexColumn != "id" || len(visit.Reads) != 1 || visit.Reads[0] != "Site" {
		t.Errorf("the key of a lookup by id is %q, and the class reads %v", v.Formula.Queries[0].IndexColumn, visit.Reads)
	}
	// A class may read itself, in a guard too: a number may not be used
	// twice.
	const invoice = `{"name": "Invoice",
 "variables": {"number": {"type": "text", "index": true}},
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent",
                  "guard": "LOOKUP(Invoice, COUNT(id), number = THIS.number AND id <> ID() AND state = \"sent\") = 0"}]}`
	if _, err := s.defineOpen(ctx, []byte(invoice), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"I-1", "I-2"} {
		if _, err := s.Create(ctx, "Invoice", Values{"number": "2026/7"}, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(ctx, "Invoice", "I-1", "send", nil); err != nil {
		t.Errorf("the first invoice of its number: %v", err)
	}
	_, err = s.Transition(ctx, "Invoice", "I-2", "send", nil)
	if se, _ := err.(*Error); se == nil || se.Code != CodeGuardFalse {
		t.Errorf("the second invoice of the same number: %v", err)
	}
}

// TestLookup_EarlierVersionsAndReopen is what keeps a file that holds
// classes reading each other openable (ADR-0046): a variable computed
// from another class declares its type, so every class has its layout
// before any formula that reads another class is bound; the current
// version of a class binds everything, and an earlier version, whose
// formulas no longer run, may read what the other class has since
// dropped.
func TestLookup_EarlierVersionsAndReopen(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	const states = `"states": {"initial": "open", "list": ["open"]}}`
	define := func(st *Store, def string) {
		t.Helper()
		if _, err := st.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	define(s, `{"name": "Part", "variables": {"z": {"type": "decimal(2)"}, "w": {"type": "number"}}, `+states)
	define(s, `{"name": "Kit", "variables": {"top": {"type": "decimal(2)", "formula": "LOOKUP(Part, MAX(z), TRUE)", "mode": "triggered", "on": ["refresh"]},
  "note": {"type": "text"}, "first": {"type": "decimal(2)", "default": "LOOKUP(Part, MIN(z), TRUE)"}},
 "states": {"initial": "open", "list": ["open", "sold"]},
 "transitions": [{"name": "sell", "from": "open", "to": "sold", "guard": "LOOKUP(Part, COUNT(z), TRUE) > 0"}],
 "access": {"transition": {"sell": "LOOKUP(Part, COUNT(z), TRUE) < 100"}},
 "rules": [{"on": "exit", "state": "open", "if": "LOOKUP(Part, COUNT(z), TRUE) > 1",
            "do": [{"action": "set", "values": {"note": "\"dearest \" & TEXT(LOOKUP(Part, MAX(z), TRUE))"}},
                   {"action": "create", "class": "Part", "values": {"w": "NUMBER(LOOKUP(Part, MAX(z), TRUE))"}}]}]}`)
	for i, z := range []string{"1.50", "7.25"} {
		if _, err := s.Create(ctx, "Part", Values{"z": z, "w": float64(i + 3)}, WithID("part-"+itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "Kit", Values{}, WithID("K-1")); err != nil {
		t.Fatal(err)
	}
	k, err := s.Refresh(ctx, "Kit", "K-1", nil)
	if err != nil || k.Values["top"] != "7.25" {
		t.Fatalf("version 1 of the kit: %v %s", err, dump(k))
	}
	if _, err := s.Create(ctx, "Kit", Values{}, WithID("K-2")); err != nil {
		t.Fatal(err)
	}
	// While the kit reads z, the part may not drop it. The kit stops
	// reading it, and then the part may; K-1 and K-2 stay on version 1 of
	// the kit, whose formula now names a column that is gone.
	_, err = s.defineOpen(ctx, []byte(`{"name": "Part", "variables": {"w": {"type": "number"}}, `+states), asSetup)
	if err == nil || !strings.Contains(err.Error(), "it would break a formula of the class Kit, at /variables/top/formula") {
		t.Fatalf("the part dropped a variable the kit reads: %v", err)
	}
	define(s, `{"name": "Kit", "variables": {"top": {"type": "decimal(2)", "formula": "LOOKUP(Part, MAX(DECIMAL(w, 2)), TRUE)", "mode": "triggered", "on": ["refresh"]},
  "note": {"type": "text"}, "first": {"type": "decimal(2)"}},
 "states": {"initial": "open", "list": ["open", "sold"]},
 "transitions": [{"name": "sell", "from": "open", "to": "sold"}]}`)
	for _, id := range []string{"part-0", "part-1"} {
		if _, err := s.Set(ctx, "Part", id, Values{"z": nil}); err != nil {
			t.Fatal(err)
		}
	}
	define(s, `{"name": "Part", "variables": {"w": {"type": "number"},
  "kits": {"type": "number", "formula": "LOOKUP(Kit, COUNT(id), TRUE)", "mode": "triggered", "on": ["refresh"]}}, `+states)
	s.Close()

	// The two classes read each other now, and the file opens whichever
	// is compiled first.
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	// A document of the earlier version is read under it, with the value
	// it computed then.
	k, err = s2.Get(ctx, "Kit", "K-1")
	if err != nil || k.Values["top"] != "7.25" || k.ClassVersion != 1 {
		t.Errorf("a kit of version 1 after the reopen: %v %s", err, dump(k))
	}
	// Its next commit adopts the current version and computes by it.
	k, err = s2.Refresh(ctx, "Kit", "K-2", nil)
	if err != nil || k.Values["top"] != "4.00" || k.ClassVersion != 2 {
		t.Errorf("a kit refreshed after the reopen: %v %s", err, dump(k))
	}
	p, err := s2.Refresh(ctx, "Part", "part-0", nil)
	if err != nil || p.Values["kits"] != 2.0 {
		t.Errorf("a part that counts kits: %v %s", err, dump(p))
	}

	// Whatever part of an earlier version was left unbound, preparing the
	// class steps over it.
	if kit := s2.classAt("Kit", 1); kit == nil || kit.bindSlots(nil, "anything") != nil {
		t.Errorf("version 1 of the kit: %v", kit)
	}
	// A current version that does not compile is not opened quietly with
	// a formula missing: the file says so.
	s2.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"classes", "class_versions"} {
		if _, err := raw.Exec("UPDATE " + table + " SET json = replace(json, 'MAX(DECIMAL(w, 2))', 'MAX(gone)')"); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	before := openHandles(t, path)
	if broken, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}}); err == nil || !strings.Contains(err.Error(), "the stored class Kit version 2 does not compile") {
		if broken != nil {
			broken.Close()
		}
		t.Fatalf("a file whose current class reads a column that is gone: %v", err)
	}
	// The refusal leaves the file free: the classes that did load before
	// the one that failed had prepared statements, and they are released.
	if after := openHandles(t, path); after != before {
		t.Errorf("the refused open left %d handles on the file, %d before it", after, before)
	}
	raw, _ = sql.Open("sqlite", path)
	for _, table := range []string{"classes", "class_versions"} {
		if _, err := raw.Exec("UPDATE " + table + " SET json = replace(json, 'MAX(gone)', 'MAX(DECIMAL(w, 2))')"); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	if s2, err = Open(path, Options{Outbox: OutboxOptions{Manual: true}}); err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// A draft reads the documents of the stored class; the draft of a
	// class nobody has defined reads none.
	pv, err := s2.PreviewDraft(ctx, opened([]byte(`{"name": "Fresh", "variables": {"seq": {"type": "number", "default": "LOOKUP(Fresh, COUNT(id), TRUE) + 1"},
  "any": {"type": "text", "default": "LOOKUP(Fresh, id, TRUE)"}}, `+states)),
		"", Values{}, "", asSetup)

	if err != nil || pv.Document.Values["seq"] != 1.0 || pv.Document.Values["any"] != nil {
		t.Errorf("the draft of a new class that reads itself: %v %s", err, dump(pv))
	}
	pv, err = s2.PreviewDraft(ctx, opened([]byte(`{"name": "Kit", "variables": {"n": {"type": "number", "default": "LOOKUP(Kit, COUNT(id), TRUE)"}}, `+states)), "", Values{}, "", asSetup)
	if err != nil || pv.Document.Values["n"] != 2.0 {
		t.Errorf("the draft of a new version counts the stored documents: %v %s", err, dump(pv))
	}
	// A draft that reads a variable the stored class lacks is told so: the
	// preview goes on and reports the variable it could not compute.
	pv, err = s2.PreviewDraft(ctx, opened([]byte(`{"name": "Kit", "variables": {"tag": {"type": "text"}, "n": {"type": "number", "default": "LOOKUP(Kit, COUNT(id), tag = \"x\")"}}, `+states)), "", Values{}, "", asSetup)
	if err != nil || len(pv.Errors) != 1 || pv.Errors[0].Variable != "n" || !strings.Contains(pv.Errors[0].Message, "a draft reads the documents of the stored class") {
		t.Errorf("a draft that reads a variable the stored class lacks: %v %s", err, dump(pv))
	}
}

// openHandles counts the file descriptors of this process that are open
// on the database file or its companions, where the system can say: on
// Linux, through /proc. Elsewhere it is 0, and Windows says the same in
// its own way, by refusing to remove a file that is still open.
func openHandles(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if target, err := os.Readlink("/proc/self/fd/" + e.Name()); err == nil && strings.HasPrefix(target, path) {
			n++
		}
	}
	return n
}

// TestDefineClasses is several classes defined in one call (ADR-0046,
// item 12): classes that name each other compile against each other,
// all are stored or none is, a finding says which definition it is in,
// and the single call still behaves as it did.
func TestDefineClasses(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	pair := [][]byte{[]byte(lkPurchase), []byte(lkApproval)}
	// Alone, neither of the two can be defined: each names the other.
	for i, def := range pair {
		var diags model.Diagnostics
		if _, err := s.defineOpen(ctx, def, asSetup); !errors.As(err, &diags) {
			t.Fatalf("definition %d alone: %v", i, err)
		}
	}
	if diags := s.ValidateClasses(openedAll(pair)); len(diags) != 0 {
		t.Fatalf("the pair validates with %v", diags)
	}
	// A definition that is wrong by itself is the one reported: the class
	// that names it would only say that it does not know it.
	wrong := strings.Replace(lkPurchase, `"default": "2"`, `"default": "2 +"`, 1)
	if diags := s.ValidateClasses(openedAll([][]byte{[]byte(lkApproval), []byte(wrong)})); len(diags) != 1 || diags[0].Path != "/1/variables/needed/default" {
		t.Errorf("a pair with one wrong definition: %v", diags)
	}
	if got := s.Classes(); len(got) != 1 || got[0] != model.SubstitutionClass {
		t.Fatalf("a validation stored %v", got)
	}
	// Together they are, in either order, as version 1 each.
	classes, err := s.defineOpenAll(ctx, pair, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	if len(classes) != 2 || classes[0].Name != "Purchase" || classes[1].Name != "Approval" || classes[0].Version != 1 || classes[1].Version != 1 {
		t.Fatalf("defined %v", classes)
	}
	if got := strings.Join(s.Classes(), ","); got != "Substitution,Purchase,Approval" {
		t.Errorf("classes %s", got)
	}
	// They run: two approvals granted, the purchase approved.
	if _, err := s.Create(ctx, "Purchase", Values{}, WithID("P-1")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A-1", "A-2"} {
		if _, err := s.Create(ctx, "Approval", Values{"purchase": "P-1", "amount": "5.00"}, WithID(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, "Approval", id, "grant", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(ctx, "Purchase", "P-1"); p.State != "approved" || p.Values["sum"] != "10.00" {
		t.Errorf("the purchase: %s", dump(p))
	}
	// The same definitions again are a no-op, alone or together.
	again, err := s.defineOpenAll(ctx, [][]byte{[]byte(lkApproval), []byte(lkPurchase)}, asSetup)
	if err != nil || again[0].Version != 1 || again[1].Version != 1 || again[0].Name != "Approval" {
		t.Errorf("the same pair again: %v %v", again, err)
	}

	// A set is all or nothing. The purchase gains a variable the approval
	// starts to set; the third definition is wrong, so nothing is stored
	// and both findings say where they are.
	purchase2 := strings.Replace(lkPurchase, `"needed":  {"type": "number", "default": "2"},`, `"needed":  {"type": "number", "default": "2"}, "last": {"type": "text"},`, 1)
	approval2 := strings.Replace(lkApproval, `"do": [{"action": "refresh", "class": "Purchase", "id": "purchase"}]`,
		`"do": [{"action": "refresh", "class": "Purchase", "id": "purchase"}, {"action": "transition", "class": "Purchase", "id": "purchase", "name": "note", "values": {"last": "ID()"}}]`, 1)
	purchase2 = strings.Replace(purchase2, `"transitions": [`, `"transitions": [{"name": "note", "from": "waiting", "to": "waiting"}, `, 1)
	const broken = `{"name": "Ledger", "variables": {"n": {"formula": "LOOKUP(Approval, COUNT(id), nope = 1)", "mode": "triggered", "on": ["refresh"]}, "m": {"type": "number", "formula": "missing + 1"}},
 "states": {"initial": "open", "list": ["open"]}}`
	_, err = s.defineOpenAll(ctx, [][]byte{[]byte(purchase2), []byte(approval2), []byte(broken)}, asSetup)
	var diags model.Diagnostics
	if !errors.As(err, &diags) || len(diags) != 2 || diags[0].Path != "/2/variables/n/formula" || diags[1].Path != "/2/variables/m/formula" {
		t.Fatalf("a set with one wrong definition: %v", err)
	}
	if c, _ := s.class("Purchase"); c.cls.Version != 1 {
		t.Errorf("a refused set stored version %d of the purchase", c.cls.Version)
	}
	if c, _ := s.class("Approval"); c.cls.Version != 1 || len(s.Classes()) != 3 {
		t.Errorf("a refused set stored something: %v", s.Classes())
	}
	// Alone, the new approval is refused: the stored purchase has no
	// transition note. Together with the new purchase it goes through,
	// and both are version 2.
	if _, err := s.defineOpen(ctx, []byte(approval2), asSetup); !errors.As(err, &diags) || !strings.Contains(diags.Error(), "note") {
		t.Errorf("the new approval alone: %v", err)
	}
	classes, err = s.defineOpenAll(ctx, [][]byte{[]byte(approval2), []byte(purchase2)}, asSetup)
	if err != nil || classes[0].Version != 2 || classes[1].Version != 2 {
		t.Fatalf("the two redefined together: %v %v", classes, err)
	}
	// One of a set unchanged and one changed: only the changed one moves.
	purchase3 := strings.Replace(purchase2, `"last": {"type": "text"},`, `"last": {"type": "text"}, "memo": {"type": "text"},`, 1)
	classes, err = s.defineOpenAll(ctx, [][]byte{[]byte(approval2), []byte(purchase3)}, asSetup)
	if err != nil || classes[0].Version != 2 || classes[1].Version != 3 {
		t.Fatalf("one unchanged, one changed: %v %v", classes, err)
	}

	// What a set may not do. A class twice in one call.
	_, err = s.defineOpenAll(ctx, [][]byte{[]byte(purchase3), []byte(strings.Replace(purchase3, `"memo"`, `"memo2"`, 1))}, asSetup)
	if !errors.As(err, &diags) || diags[0].Code != model.CodeDuplicate || diags[0].Path != "/1/name" {
		t.Errorf("a class twice in one call: %v", err)
	}
	// Bad JSON says which definition.
	_, err = s.defineOpenAll(ctx, [][]byte{[]byte(purchase3), []byte(`{"name": `)}, asSetup)
	if !errors.As(err, &diags) || diags[0].Code != model.CodeBadJSON || diags[0].Path != "/1" {
		t.Errorf("bad JSON in a set: %v", err)
	}
	// A redefinition inside a set is held to what a redefinition alone is:
	// the purchase may not drop a transition a stored class outside the
	// set names, and may when that class is redefined with it.
	purchase4 := strings.Replace(purchase3, `{"name": "note", "from": "waiting", "to": "waiting"}, `, ``, 1)
	const other = `{"name": "Other", "variables": {"x": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}}`
	_, err = s.defineOpenAll(ctx, [][]byte{[]byte(purchase4), []byte(other)}, asSetup)
	if !errors.As(err, &diags) || diags[0].Code != model.CodeNotAllowed || !strings.HasPrefix(diags[0].Path, "/0/") || !strings.Contains(diags[0].Message, "it would break a rule of the class Approval") {
		t.Errorf("a set that breaks a class outside it: %v", err)
	}
	if _, err := s.class("Other"); err == nil {
		t.Error("the refused set stored its second class")
	}
	classes, err = s.defineOpenAll(ctx, [][]byte{[]byte(purchase4), []byte(lkApproval), []byte(other)}, asSetup)
	if err != nil || classes[0].Version != 4 || classes[1].Version != 3 || classes[2].Version != 1 {
		t.Fatalf("the purchase and the approval changed together: %v %v", classes, err)
	}
	// and to what the documents allow (ADR-0039): the approval may not
	// drop a variable its documents hold values for.
	_, err = s.defineOpenAll(ctx, [][]byte{[]byte(other), []byte(strings.Replace(lkApproval, `, "amount": {"type": "decimal(2)"}`, ``, 1)), []byte(strings.Replace(purchase4, `"sum":`, `"sum2":`, 1))}, asSetup)
	held := false
	if errors.As(err, &diags) {
		for _, d := range diags {
			held = held || d.Code == model.CodeNotAllowed && d.Path == "/1/variables/amount" && strings.Contains(d.Message, "documents hold a value")
		}
	}
	// The purchase is in the set too, so it is judged by its own new
	// definition and not as a reader the approval breaks.
	if !held || strings.Contains(err.Error(), "it would break a formula of the class Purchase") {
		t.Errorf("a set that drops a variable with values: %v", err)
	}
	// An empty call does nothing; a single definition in a set is a set.
	if out, err := s.defineOpenAll(ctx, nil, asSetup); err != nil || len(out) != 0 {
		t.Errorf("an empty call: %v %v", out, err)
	}
	_, err = s.defineOpenAll(ctx, [][]byte{[]byte(broken)}, asSetup)
	if !errors.As(err, &diags) || diags[0].Path != "/0/variables/n/formula" {
		t.Errorf("a set of one says where: %v", err)
	}
	// A name taken by a table of another class of the same call rolls the
	// whole call back.
	_, err = s.defineOpenAll(ctx, [][]byte{
		[]byte(`{"name": "Box", "variables": {"x": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}}`),
		[]byte(`{"name": "Box_history", "variables": {"x": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}}`)}, asSetup)

	if err == nil {
		t.Error("two classes whose tables collide were both stored")
	}
	if _, err := s.class("Box"); err == nil {
		t.Error("the first class of a call that failed while writing was kept")
	}

	// Everything comes back after a reopen, and the documents still move.
	s.Close()
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := strings.Join(s2.Classes(), ","); got != "Substitution,Purchase,Approval,Other" {
		t.Errorf("classes after the reopen: %s", got)
	}
	if p, err := s2.Refresh(ctx, "Purchase", "P-1", nil); err != nil || p.Values["granted"] != 2.0 || p.ClassVersion != 4 {
		t.Errorf("the purchase after the reopen: %v %s", err, dump(p))
	}
}
