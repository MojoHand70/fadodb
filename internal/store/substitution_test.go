package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// The class of the tests of acting for somebody (ADR-0053, item 4): the
// owner, or whoever validly acts for the owner, sees and changes a trip;
// approving it takes the approver or their substitute, and never the one
// who filed it.
const subTrip = `{"name": "Trip",
 "variables": {
   "owner":    {"type": "text", "default": "IF(ACTINGFOR() <> \"\", ACTINGFOR(), USER())"},
   "approver": {"type": "text"},
   "cost":     {"type": "number"}
 },
 "states": {"initial": "draft", "list": ["draft", "filed", "approved"]},
 "transitions": [
   {"name": "file",    "from": "draft", "to": "filed"},
   {"name": "approve", "from": "filed", "to": "approved"}
 ],
 "access": {
   "read":   "owner = USER() OR owner = ACTINGFOR() OR approver = USER() OR approver = ACTINGFOR()",
   "create": "INGROUP(\"everyone\")",
   "update": "owner = USER() OR owner = ACTINGFOR()",
   "delete": "owner = USER()",
   "transition": {"approve": "(approver = USER() OR approver = ACTINGFOR()) AND CREATEDBY() <> USER()"}
 }}`

const subMemo = `{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`

var (
	subAnn   = Principal{ID: "ann", Kind: ActorPerson}
	subBob   = Principal{ID: "bob", Kind: ActorPerson}
	subCarol = Principal{ID: "carol", Kind: ActorPerson}
	bobForAn = Principal{ID: "bob", Kind: ActorPerson, For: "ann"}
	subOlaf  = Principal{ID: "olaf", Kind: ActorPerson, System: []string{GroupAdministrators}}
	subCora  = Principal{ID: "cora", Kind: ActorPerson, System: []string{GroupContentAdministrators}}
)

// subStore is a store with the two classes, a clock the test moves, and
// the outbox drained by hand.
func subStore(t *testing.T, o Options) (*Store, *clock) {
	t.Helper()
	now := &clock{now: start}
	o.Now = now.Now
	s, _ := openStore(t, o)
	for _, def := range []string{subTrip, subMemo} {
		if _, err := s.DefineClass(context.Background(), []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	return s, now
}

// refusedFor reports whether the error is the refusal of a "for" no
// substitution lets through.
func refusedFor(err error) bool {
	e, ok := err.(*Error)
	return ok && e.Code == CodeForbidden && e.Path == "/principal/for"
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *clock) at() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// The engine brings the class to every database, under a name no
// designer may take (ADR-0053, item 7).
func TestSubstitution_TheEngineBringsTheClass(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	cls, ok := s.Class(model.SubstitutionClass)
	if !ok || cls.Version != 1 || cls.Defined.By != "fadodb" || cls.Defined.Kind != ActorService {
		t.Fatalf("the class of a new database: %v %+v", ok, cls)
	}
	if defs, err := s.Definitions(ctx, DefinitionClass, model.SubstitutionClass); err != nil || len(defs) != 1 || defs[0].By != "fadodb" {
		t.Fatalf("who defined it: %v %+v", err, defs)
	}
	// A designer may not define it, in any case of the name, nor check or
	// preview a definition of that name.
	for _, name := range []string{"Substitution", "substitution"} {
		def := []byte(`{"name": "` + name + `", "variables": {"a": {"type": "text"}}, "states": {"initial": "s", "list": ["s"]}}`)
		_, err := s.DefineClass(ctx, def, asSetup)
		if diags, ok := err.(model.Diagnostics); !ok || len(diags) == 0 || diags[0].Code != model.CodeReservedName || diags[0].Path != "/name" {
			t.Errorf("defining %s: %v", name, err)
		}
		if diags := s.Validate(def); !diags.Refuses() || diags[0].Code != model.CodeReservedName {
			t.Errorf("checking %s: %v", name, diags)
		}
		if _, err := s.PreviewDraft(ctx, def, "", nil, "", asSetup); err == nil || !strings.Contains(err.Error(), model.CodeReservedName) {
			t.Errorf("previewing a draft of %s: %v", name, err)
		}
	}
	// The engine's own definition is no designer's to send either: it is
	// the stored one, so nothing is defined and nothing refused.
	if again, err := s.DefineClass(ctx, model.SubstitutionDefinition(), asSetup); err != nil || again.Version != 1 {
		t.Errorf("the stored definition sent again: %v", err)
	}
	// Opening the database again defines nothing.
	if _, err := s.Create(ctx, model.SubstitutionClass, Values{"by": "bob"}, As(subAnn)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if cls, _ := s2.Class(model.SubstitutionClass); cls == nil || cls.Version != 1 {
		t.Fatalf("the class after the reopen: %+v", cls)
	}
	if n, err := s2.Count(ctx, Filter{Class: model.SubstitutionClass}, As(subAnn)); err != nil || n != 1 {
		t.Fatalf("the substitution after the reopen: %d %v", n, err)
	}
}

// A caller that says it acts for somebody is refused on every call
// until a substitution says it may, and let through from the commit that
// gave one (ADR-0053, item 2).
func TestSubstitution_EveryCallIsAsked(t *testing.T) {
	s, _ := subStore(t, Options{})
	ctx := context.Background()
	trip, err := s.Create(ctx, "Trip", Values{"cost": 100, "approver": "mia"}, As(subAnn))
	if err != nil {
		t.Fatal(err)
	}
	forAnn := As(bobForAn)
	design := As(Principal{ID: "bob", For: "ann", System: []string{GroupDesigners, GroupAdministrators, GroupAnalysts, GroupContentAdministrators}})
	calls := map[string]func() error{
		"Create":        func() error { _, err := s.Create(ctx, "Trip", Values{"cost": 1}, forAnn); return err },
		"CreateMany":    func() error { _, err := s.CreateMany(ctx, "Trip", []Values{{"cost": 1}}, forAnn); return err },
		"Set":           func() error { _, err := s.Set(ctx, "Trip", trip.ID, Values{"cost": 2}, forAnn); return err },
		"Reset":         func() error { _, err := s.Reset(ctx, "Trip", trip.ID, []string{"cost"}, forAnn); return err },
		"Refresh":       func() error { _, err := s.Refresh(ctx, "Trip", trip.ID, nil, forAnn); return err },
		"Transition":    func() error { _, err := s.Transition(ctx, "Trip", trip.ID, "file", nil, forAnn); return err },
		"Delete":        func() error { return s.Delete(ctx, "Trip", trip.ID, forAnn) },
		"Preview":       func() error { _, err := s.Preview(ctx, "Trip", trip.ID, Values{"cost": 2}, "", forAnn); return err },
		"PreviewDelete": func() error { _, err := s.PreviewDelete(ctx, "Trip", trip.ID, forAnn); return err },
		"TransitionMany": func() error {
			_, err := s.TransitionMany(ctx, "Trip", "file", []string{trip.ID}, nil, forAnn)
			return err
		},
		"PreviewMany": func() error {
			_, err := s.PreviewMany(ctx, "Trip", "file", []string{trip.ID}, nil, forAnn)
			return err
		},
		"Get":         func() error { _, err := s.Get(ctx, "Trip", trip.ID, forAnn); return err },
		"Version":     func() error { _, err := s.Version(ctx, "Trip", trip.ID, 1, forAnn); return err },
		"History":     func() error { _, err := s.History(ctx, "Trip", trip.ID, forAnn); return err },
		"Events":      func() error { _, err := s.Events(ctx, "Trip", trip.ID, forAnn); return err },
		"Transitions": func() error { _, err := s.Transitions(ctx, "Trip", trip.ID, forAnn); return err },
		"Query":       func() error { _, err := s.Query(ctx, Filter{Class: "Trip"}, forAnn); return err },
		"Count":       func() error { _, err := s.Count(ctx, Filter{Class: "Trip"}, forAnn); return err },
		"Due":         func() error { _, err := s.Due(ctx, "Trip", forAnn); return err },
		"OpenFile":    func() error { _, _, err := s.OpenFile(ctx, "Trip", trip.ID, "sha256:x", forAnn); return err },
		// Work that names no class.
		"DefineClass":   func() error { _, err := s.DefineClass(ctx, []byte(subMemo), design); return err },
		"DefineClasses": func() error { _, err := s.DefineClasses(ctx, [][]byte{[]byte(subMemo)}, design); return err },
		"DefineSchema": func() error {
			_, err := s.DefineSchema(ctx, []byte(`{"name": "Leg", "fields": {"km": {"type": "number"}}}`), design)
			return err
		},
		"DefineTable": func() error {
			_, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "key": ["k"], "columns": {"k": "text"}}`), design)
			return err
		},
		"DefineAction":  func() error { _, err := s.DefineAction(ctx, []byte(`{"name": "audit.note"}`), design); return err },
		"PreviewDraft":  func() error { _, err := s.PreviewDraft(ctx, []byte(subMemo), "", nil, "", design); return err },
		"Migrate":       func() error { _, err := s.Migrate(ctx, "Trip", design); return err },
		"Drain":         func() error { _, err := s.Drain(ctx, design); return err },
		"Outbox":        func() error { _, err := s.Outbox(ctx, OutboxFilter{}, design); return err },
		"RetryAction":   func() error { return s.RetryAction(ctx, "x", design) },
		"TryAction":     func() error { _, err := s.TryAction(ctx, "audit.note", nil, design); return err },
		"PreviewAction": func() error { _, err := s.PreviewAction(ctx, "audit.note", nil, design); return err },
		"CheckBlobs":    func() error { _, _, err := s.CheckBlobs(ctx, design); return err },
		"OpenBlob":      func() error { _, _, err := s.OpenBlob(ctx, "sha256:x", design); return err },
		"SQL":           func() error { _, err := s.SQL(ctx, "SELECT 1", nil, design); return err },
		"ImportRows":    func() error { _, err := s.ImportRows(ctx, "Rates", nil, design); return err },
		"TableRows":     func() error { _, err := s.TableRows(ctx, "Rates", design); return err },
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "key": ["k"], "columns": {"k": "text"}, "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for what, call := range calls {
		err := call()
		if !refusedFor(err) {
			t.Errorf("%s for ann with no substitution: %v", what, err)
			continue
		}
		e := err.(*Error)
		if !strings.Contains(e.Message, "no substitution lets bob act for ann") || !strings.Contains(e.Hint, `by "bob"`) || !strings.Contains(e.Hint, "database administrator") {
			t.Errorf("%s: the refusal does not say what is missing: %v", what, err)
		}
	}
	// Nothing was written and nothing defined.
	if ev, _ := s.Events(ctx, "Trip", trip.ID, As(subAnn)); len(ev) != 1 {
		t.Fatalf("the events after the refusals: %s", dump(ev))
	}
	// A caller that acts for nobody is asked nothing.
	if _, err := s.Get(ctx, "Trip", trip.ID, As(subAnn)); err != nil {
		t.Fatal(err)
	}

	// Ann lets Bob act for her, and every call goes on to what it does.
	sub := gives(t, s, "bob", "ann")
	if sub.State != model.SubstitutionActive || sub.Values["for"] != "ann" || sub.CreatedBy != "ann" {
		t.Fatalf("the substitution: %s", dump(sub))
	}
	for what, call := range calls {
		if err := call(); refusedFor(err) {
			t.Errorf("%s for ann with a substitution: %v", what, err)
		}
	}
	// The log says who acted and for whom, checked now.
	ev, _ := s.Events(ctx, "Trip", trip.ID, As(subAnn))
	last := ev[len(ev)-1]
	if last.ActorID != "bob" || last.ActorFor != "ann" {
		t.Errorf("the last event: %s", dump(last))
	}
}

// USER() is the one who acts, so a substitute cannot approve what they
// filed themselves (ADR-0053, item 3), and a class says where a
// substitute is accepted (item 4).
func TestSubstitution_FourEyes(t *testing.T) {
	s, _ := subStore(t, Options{})
	ctx := context.Background()
	gives(t, s, "bob", "ann")
	// Bob files a trip of his own; Ann approves trips, and is away.
	own, err := s.Create(ctx, "Trip", Values{"cost": 900, "approver": "ann"}, As(subBob))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Trip", own.ID, "file", nil, As(subBob)); err != nil {
		t.Fatal(err)
	}
	// As Ann's substitute he sees it, and may not approve it: he made it.
	_, err = s.Transition(ctx, "Trip", own.ID, "approve", nil, As(bobForAn))
	if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/access/transition/approve" {
		t.Fatalf("Bob approves his own trip as Ann: %v", err)
	}
	// Carol's trip he approves for Ann, and the event says both.
	hers, err := s.Create(ctx, "Trip", Values{"cost": 300, "approver": "ann"}, As(subCarol))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Trip", hers.ID, "file", nil, As(subCarol)); err != nil {
		t.Fatal(err)
	}
	// Without saying for whom, Bob is only Bob, and the trip is not his.
	if _, err := s.Transition(ctx, "Trip", hers.ID, "approve", nil, As(subBob)); code(err) != CodeUnknownDocument {
		t.Fatalf("Bob approves Carol's trip as himself: %v", err)
	}
	done, err := s.Transition(ctx, "Trip", hers.ID, "approve", nil, As(bobForAn))
	if err != nil || done.State != "approved" || done.StateEnteredBy != "bob" {
		t.Fatalf("Bob approves Carol's trip for Ann: %v %s", err, dump(done))
	}
	ev, _ := s.Events(ctx, "Trip", hers.ID, As(subCarol))
	if last := ev[len(ev)-1]; last.ActorID != "bob" || last.ActorFor != "ann" || last.Name != "approve" {
		t.Errorf("the event of the approval: %s", dump(last))
	}
	// Nobody acts for somebody who acts for somebody (item 6): Ann acts
	// for Mia, and that gives Bob nothing.
	gives(t, s, "ann", "mia")
	if _, err := s.Get(ctx, "Trip", hers.ID, As(Principal{ID: "bob", For: "mia"})); !refusedFor(err) {
		t.Errorf("Bob for Mia, through Ann: %v", err)
	}
	// Bob's groups stay Bob's (item 5): nothing here to pass on, and the
	// context of his calls for Ann carries his own.
	if _, err := s.Get(ctx, "Trip", hers.ID, As(Principal{ID: "ann", For: "mia"})); err != nil {
		t.Errorf("Ann for Mia: %v", err)
	}
}

// A substitution holds from its from until its until, by the dates and
// not by the state its document is in (ADR-0053, items 1, 2 and 7).
func TestSubstitution_FromAndUntil(t *testing.T) {
	s, now := subStore(t, Options{})
	ctx := context.Background()
	trip, err := s.Create(ctx, "Trip", Values{"cost": 100}, As(subAnn))
	if err != nil {
		t.Fatal(err)
	}
	from := now.at().Add(24 * time.Hour)
	until := from.Add(48 * time.Hour)
	sub, err := s.Create(ctx, model.SubstitutionClass, Values{"by": "bob", "from": from, "until": until}, As(subAnn))
	if err != nil || sub.State != model.SubstitutionPlanned {
		t.Fatalf("a substitution to come: %v %s", err, dump(sub))
	}
	// Before its from Bob is refused, and told when it begins.
	_, err = s.Get(ctx, "Trip", trip.ID, As(bobForAn))
	if !refusedFor(err) || !strings.Contains(err.Error(), sub.ID+" begins at "+from.Format("2006-01-02 15:04:05")) {
		t.Fatalf("before the from: %v", err)
	}
	// A preview at a moment inside it is asked as of that moment.
	inside := from.Add(time.Hour)
	if _, err := s.Preview(ctx, "Trip", trip.ID, Values{"cost": 150}, "", As(bobForAn), At(inside)); err != nil {
		t.Fatalf("a preview at a moment inside: %v", err)
	}
	if _, err := s.Get(ctx, "Trip", trip.ID, As(bobForAn), At(inside)); err != nil {
		t.Fatalf("a read at a moment inside: %v", err)
	}
	if _, err := s.Preview(ctx, "Trip", trip.ID, Values{"cost": 150}, "", As(bobForAn), At(until.Add(time.Second))); !refusedFor(err) {
		t.Fatalf("a preview at a moment past the until: %v", err)
	}
	// The clock passes the from. The worker has not run, the document is
	// still planned, and Bob is let through all the same.
	now.add(25 * time.Hour)
	if d, _ := s.Get(ctx, model.SubstitutionClass, sub.ID, As(subAnn)); d.State != model.SubstitutionPlanned {
		t.Fatalf("the document before the worker ran: %s", d.State)
	}
	if _, err := s.Set(ctx, "Trip", trip.ID, Values{"cost": 150}, As(bobForAn)); err != nil {
		t.Fatalf("inside the substitution: %v", err)
	}
	// The worker moves the document at its from.
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Get(ctx, model.SubstitutionClass, sub.ID, As(subAnn)); d.State != model.SubstitutionActive {
		t.Fatalf("the document after the worker ran: %s", d.State)
	}
	// The clock passes the until: Bob is refused before the worker ends
	// the document, and told when it ended.
	now.add(48 * time.Hour)
	_, err = s.Set(ctx, "Trip", trip.ID, Values{"cost": 160}, As(bobForAn))
	if !refusedFor(err) || !strings.Contains(err.Error(), sub.ID+" ended at ") {
		t.Fatalf("past the until: %v", err)
	}
	// A read at a past moment is asked of the substitutions that stand
	// now: one that has ended lets nobody through.
	if _, err := s.Get(ctx, "Trip", trip.ID, As(bobForAn), At(inside)); !refusedFor(err) {
		t.Fatalf("a read at a past moment, after the end: %v", err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Get(ctx, model.SubstitutionClass, sub.ID, As(subAnn))
	if d.State != model.SubstitutionEnded {
		t.Fatalf("the document after its until: %s", d.State)
	}
	// Its events tell its life: given by Ann, begun and ended by time.
	ev, _ := s.Events(ctx, model.SubstitutionClass, sub.ID, As(subAnn))
	var life []string
	for _, e := range ev {
		life = append(life, e.Kind+" "+e.ToState+" "+e.Origin)
	}
	if got := strings.Join(life, ", "); got != "created planned command, transition active timer, transition ended timer" {
		t.Errorf("the life of the substitution: %s", got)
	}
	// An ended substitution is frozen.
	if _, err := s.Transition(ctx, model.SubstitutionClass, sub.ID, model.SubstitutionWithdraw, nil, As(subAnn)); err == nil {
		t.Error("an ended substitution was withdrawn")
	}
}

// A substitution may be kept to some classes (ADR-0053, item 1). Work
// that names no class needs one for every class.
func TestSubstitution_Classes(t *testing.T) {
	s, _ := subStore(t, Options{})
	ctx := context.Background()
	sub, err := s.Create(ctx, model.SubstitutionClass, Values{"by": "bob", "classes": []string{"trip"}}, As(subAnn))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Trip", Values{"cost": 5}, As(bobForAn)); err != nil {
		t.Fatalf("on the class it names, in any case of the name: %v", err)
	}
	_, err = s.Create(ctx, "Memo", Values{"t": "x"}, As(bobForAn))
	if !refusedFor(err) || !strings.Contains(err.Error(), sub.ID+" is for TRIP only") {
		t.Fatalf("on another class: %v", err)
	}
	_, err = s.SQL(ctx, "SELECT 1", nil, As(Principal{ID: "bob", For: "ann", System: []string{GroupAnalysts}}))
	if !refusedFor(err) || !strings.Contains(err.Error(), "this call names no class") {
		t.Fatalf("SQL, which names no class: %v", err)
	}
	// A second one, for every class, covers the rest.
	gives(t, s, "bob", "ann")
	if _, err := s.Create(ctx, "Memo", Values{"t": "x"}, As(bobForAn)); err != nil {
		t.Fatalf("with one for every class: %v", err)
	}
}

// Who gives a substitution and who withdraws it (ADR-0053, item 6), and
// who sees it.
func TestSubstitution_WhoGivesAndWithdraws(t *testing.T) {
	s, _ := subStore(t, Options{})
	ctx := context.Background()
	create := func(values Values, p Principal, opts ...Option) (*Document, error) {
		return s.Create(ctx, model.SubstitutionClass, values, append(opts, As(p))...)
	}
	forbidden := func(what string, err error) {
		t.Helper()
		if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/access/create" {
			t.Errorf("%s: %v", what, err)
		}
	}
	// The person gives one for themselves; for is theirs when left out.
	hers, err := create(Values{"by": "bob"}, subAnn)
	if err != nil || hers.Values["for"] != "ann" || hers.Title != "bob for ann" {
		t.Fatalf("Ann gives Bob one: %v %s", err, dump(hers))
	}
	// Nobody gives one for somebody else,
	_, err = create(Values{"by": "bob", "for": "ann"}, subBob)
	forbidden("Bob gives himself one for Ann", err)
	_, err = create(Values{"by": "bob", "for": "ann"}, subCarol)
	forbidden("Carol gives Bob one for Ann", err)
	// nor passes one on: Bob, acting for Ann, cannot let Carol act for Ann,
	_, err = create(Values{"by": "carol", "for": "ann"}, bobForAn)
	forbidden("Bob passes Ann's on to Carol", err)
	// nor gives one to themselves, or one that ends before it begins.
	_, err = create(Values{"by": "ann"}, subAnn)
	forbidden("Ann gives herself one", err)
	_, err = create(Values{"by": "bob", "from": start.Add(48 * time.Hour), "until": start.Add(24 * time.Hour)}, subAnn)
	forbidden("one that ends before it begins", err)
	// A database administrator gives one when the person cannot, with a
	// reason, and never to themselves (question I).
	_, err = create(Values{"by": "carol", "for": "ann"}, subOlaf)
	forbidden("an administrator gives one without a reason", err)
	_, err = create(Values{"by": "olaf", "for": "ann", "reason": "Ann is ill"}, subOlaf)
	forbidden("an administrator gives themselves one", err)
	given, err := create(Values{"by": "carol", "for": "ann", "reason": "Ann is ill"}, subOlaf)
	if err != nil || given.CreatedBy != "olaf" {
		t.Fatalf("an administrator gives Carol one for Ann: %v", err)
	}
	// A content administrator does not, with a reason or without: the
	// rules of this class hold for them too.
	_, err = create(Values{"by": "cora", "for": "ann"}, subCora, Reason("I need to"))
	forbidden("a content administrator gives themselves one", err)
	_, err = create(Values{"by": "dan", "for": "ann"}, subCora)
	forbidden("a content administrator gives one without a reason", err)
	if pv, err := s.Preview(ctx, model.SubstitutionClass, "", Values{"by": "cora", "for": "ann"}, "", As(subCora)); err != nil || len(pv.Override) != 0 || len(pv.Errors) != 1 || pv.Errors[0].Code != CodeForbidden {
		t.Errorf("a content administrator's preview: %v %s", err, dump(pv))
	}

	// Ann sees every substitution for her, Bob the one he holds, Carol
	// hers, an administrator all, and Dan none.
	count := func(p Principal) int64 {
		n, err := s.Count(ctx, Filter{Class: model.SubstitutionClass}, As(p))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if a, b, c, o, d := count(subAnn), count(subBob), count(subCarol), count(subOlaf), count(Principal{ID: "dan"}); a != 2 || b != 1 || c != 1 || o != 2 || d != 0 {
		t.Errorf("who sees how many: ann %d, bob %d, carol %d, olaf %d, dan %d", a, b, c, o, d)
	}
	if _, err := s.Get(ctx, model.SubstitutionClass, hers.ID, As(subCarol)); code(err) != CodeUnknownDocument {
		t.Errorf("Carol reads Bob's: %v", err)
	}

	// Nothing changes a substitution and nothing deletes it.
	if _, err := s.Set(ctx, model.SubstitutionClass, hers.ID, Values{"by": "carol"}, As(subAnn)); code(err) != CodeForbidden {
		t.Errorf("Ann changes hers: %v", err)
	}
	if err := s.Delete(ctx, model.SubstitutionClass, hers.ID, As(subAnn)); code(err) != CodeForbidden {
		t.Errorf("Ann deletes hers: %v", err)
	}
	if _, err := s.Set(ctx, model.SubstitutionClass, hers.ID, Values{"by": "carol"}, As(subCora), Reason("tidying")); code(err) != CodeForbidden {
		t.Errorf("a content administrator changes one: %v", err)
	}

	// The one who holds it may not withdraw it, nor a content
	// administrator; the listing says so to each.
	withdraw := func(id string, p Principal) error {
		_, err := s.Transition(ctx, model.SubstitutionClass, id, model.SubstitutionWithdraw, nil, As(p), Reason("no longer needed"))
		return err
	}
	if err := withdraw(hers.ID, subBob); code(err) != CodeForbidden {
		t.Errorf("Bob withdraws the one he holds: %v", err)
	}
	if err := withdraw(hers.ID, subCora); code(err) != CodeForbidden {
		t.Errorf("a content administrator withdraws one: %v", err)
	}
	infos, err := s.Transitions(ctx, model.SubstitutionClass, hers.ID, As(subCora))
	if err != nil || len(infos) != 1 || infos[0].Enabled || infos[0].Code != CodeForbidden || infos[0].Override != "" {
		t.Errorf("what a content administrator may do with one: %v %s", err, dump(infos))
	}
	// The person withdraws hers, at any time, and it holds no more from
	// that commit.
	if _, err := s.Get(ctx, "Trip", "none", As(bobForAn)); code(err) != CodeUnknownDocument {
		t.Fatalf("Bob for Ann before the withdrawal: %v", err)
	}
	if err := withdraw(hers.ID, subAnn); err != nil {
		t.Fatalf("Ann withdraws hers: %v", err)
	}
	if _, err := s.Get(ctx, "Trip", "none", As(bobForAn)); !refusedFor(err) {
		t.Fatalf("Bob for Ann after the withdrawal: %v", err)
	}
	// An administrator withdraws the one they gave; Ann could have too.
	if err := withdraw(given.ID, subOlaf); err != nil {
		t.Fatalf("an administrator withdraws one: %v", err)
	}
	ev, _ := s.Events(ctx, model.SubstitutionClass, given.ID, As(subAnn))
	if len(ev) < 2 || ev[0].ActorID != "olaf" || ev[len(ev)-1].ActorID != "olaf" || ev[len(ev)-1].ToState != model.SubstitutionWithdrawn {
		t.Errorf("the events say who gave it and who withdrew it: %s", dump(ev))
	}
}

// The engine keeps what it read of a pair in memory, and reads again
// only after a commit wrote a substitution (ADR-0053, Performance).
func TestSubstitution_KeptInMemory(t *testing.T) {
	s, _ := subStore(t, Options{})
	ctx := context.Background()
	gives(t, s, "bob", "ann")
	trip, err := s.Create(ctx, "Trip", Values{"cost": 100}, As(subAnn))
	if err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		if _, err := s.Get(ctx, "Trip", trip.ID, As(bobForAn)); err != nil {
			t.Fatal(err)
		}
	}
	read()
	before := s.subs.reads.Load()
	for i := 0; i < 50; i++ {
		read()
		if _, err := s.Set(ctx, "Trip", trip.ID, Values{"cost": 100 + i}, As(bobForAn)); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.subs.reads.Load(); got != before {
		t.Fatalf("a hundred calls read the substitutions %d times", got-before)
	}
	// A preview of a substitution writes nothing, so nothing is read again.
	if _, err := s.Preview(ctx, model.SubstitutionClass, "", Values{"by": "carol"}, "", As(subAnn)); err != nil {
		t.Fatal(err)
	}
	read()
	if got := s.subs.reads.Load(); got != before {
		t.Fatalf("a preview made the engine read again")
	}
	// A commit of the class does, once for the pair.
	gives(t, s, "carol", "ann")
	read()
	read()
	if got := s.subs.reads.Load(); got != before+1 {
		t.Fatalf("after a commit of the class the pair was read %d times, want once", got-before)
	}
	// A pair that has none is remembered too.
	for i := 0; i < 3; i++ {
		if _, err := s.Get(ctx, "Trip", trip.ID, As(Principal{ID: "dan", For: "ann"})); !refusedFor(err) {
			t.Fatal(err)
		}
	}
	if got := s.subs.reads.Load(); got != before+2 {
		t.Fatalf("a pair with no substitution was read %d times, want once", got-before-1)
	}
}

// What a commit queued runs with the "for" its caller had, and is not
// asked again (ADR-0053, item 2): the step was decided while the
// substitution held.
func TestSubstitution_WhatACommitQueued(t *testing.T) {
	s, _ := subStore(t, Options{})
	ctx := context.Background()
	const note = `{"name": "Note", "variables": {"about": {"type": "text"}, "for_whom": {"type": "text", "default": "ACTINGFOR()"}},
 "states": {"initial": "kept", "list": ["kept"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "FALSE", "delete": "FALSE"}}`
	const filed = `{"name": "Filed", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]},
 "rules": [{"on": "enter", "state": "a", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()"}}]}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	for _, def := range []string{note, filed} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	sub := gives(t, s, "bob", "ann")
	if _, err := s.Create(ctx, "Filed", Values{"t": "x"}, As(bobForAn)); err != nil {
		t.Fatal(err)
	}
	// Ann withdraws before the action runs.
	if _, err := s.Transition(ctx, model.SubstitutionClass, sub.ID, model.SubstitutionWithdraw, nil, As(subAnn)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drained %d: %v", n, err)
	}
	notes, err := s.Query(ctx, Filter{Class: "Note"})
	if err != nil || len(notes) != 1 || notes[0].CreatedBy != "bob" || notes[0].Values["for_whom"] != "ann" {
		t.Fatalf("the note the action made: %v %s", err, dump(notes))
	}
	// A new command of Bob's for Ann is refused.
	if _, err := s.Create(ctx, "Filed", Values{"t": "y"}, As(bobForAn)); !refusedFor(err) {
		t.Fatalf("after the withdrawal: %v", err)
	}
}

// A host program that keeps its own substitutions says so at Open, and
// the engine records what it is told and asks nothing (ADR-0053, item 8).
func TestSubstitution_AHostThatKeepsItsOwn(t *testing.T) {
	s, _ := subStore(t, Options{HostAnswersFor: true})
	ctx := context.Background()
	doc, err := s.Create(ctx, "Trip", Values{"cost": 1}, As(bobForAn))
	if err != nil || doc.Values["owner"] != "ann" {
		t.Fatalf("a host that answers for the for: %v %s", err, dump(doc))
	}
	ev, _ := s.Events(ctx, "Trip", doc.ID, As(subAnn))
	if len(ev) != 1 || ev[0].ActorID != "bob" || ev[0].ActorFor != "ann" {
		t.Errorf("the event: %s", dump(ev))
	}
	if got := s.subs.reads.Load(); got != 0 {
		t.Errorf("the engine read the substitutions %d times", got)
	}
	// The class is there all the same, as in every database.
	if _, ok := s.Class(model.SubstitutionClass); !ok {
		t.Error("the class is missing")
	}
}
