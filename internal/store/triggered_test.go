package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// quote1 is a class with one triggered variable that locks a rate when a
// quote is sent, and an auto variable that reads it.
const quote1 = `{
 "name": "Quote",
 "variables": {
   "amount": {"type": "number"},
   "rate":   {"type": "number"},
   "locked": {"formula": "rate", "mode": "triggered", "on": ["transition:send", "refresh"]},
   "price":  {"formula": "amount * locked"},
   "margin": {"formula": "100 / rate", "mode": "triggered", "on": ["enter:sent"]}
 },
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent"}, {"name": "again", "from": "sent", "to": "sent"}],
 "rules": [{"on": "transition", "name": "again", "do": [{"action": "refresh"}]}],
 "rights": {"*": {"margin": {"read": ["finance"]}}}
}`

func defineQuote(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DefineClass(context.Background(), []byte(quote1), asSetup); err != nil {
		t.Fatal(err)
	}
}

// The moment of a triggered value is in the row that holds it, at every
// version: the version whose commit computed it, and the time of that
// commit, which is the valid_from of that version and not of the row
// read (ADR-0043).
func TestTriggered_MomentAtEveryVersion(t *testing.T) {
	s, _ := openStore(t, Options{}) // the test clock moves a second per commit
	defineQuote(t, s)
	ctx := context.Background()
	admin := As(root)
	doc, err := s.Create(ctx, "Quote", Values{"amount": 200, "rate": 0.5}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Computed != nil {
		t.Errorf("a new quote has moments: %s", dump(doc.Computed))
	}
	if doc, err = s.Transition(ctx, "Quote", doc.ID, "send", nil, admin); err != nil {
		t.Fatal(err)
	}
	if doc, err = s.Set(ctx, "Quote", doc.ID, Values{"rate": 0.25}, admin); err != nil {
		t.Fatal(err)
	}
	history, err := s.History(ctx, "Quote", doc.ID, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("%d versions, want 3", len(history))
	}
	sent := history[1].ValidFrom
	if sent == history[0].ValidFrom || sent == history[2].ValidFrom {
		t.Fatalf("the clock did not move between the commits: %d %d %d", history[0].ValidFrom, sent, history[2].ValidFrom)
	}
	want := map[string]Moment{"locked": {Version: 2, At: sent}, "margin": {Version: 2, At: sent}}
	if history[0].Computed != nil {
		t.Errorf("version 1 has moments: %s", dump(history[0].Computed))
	}
	for _, h := range history[1:] {
		if !reflect.DeepEqual(h.Computed, want) {
			t.Errorf("version %d: moments %s, want %s", h.Version, dump(h.Computed), dump(want))
		}
		if h.Values["locked"] != 0.5 || h.Values["price"] != 100.0 {
			t.Errorf("version %d: locked %v, price %v", h.Version, h.Values["locked"], h.Values["price"])
		}
	}
	// The document a commit returns, the one read back and the one a query
	// finds say the same.
	got, err := s.Get(ctx, "Quote", doc.ID, admin)
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.Query(ctx, Filter{Class: "Quote", Where: `state = "sent"`}, admin)
	if err != nil || len(found) != 1 {
		t.Fatalf("query: %v, %d documents", err, len(found))
	}
	for name, d := range map[string]*Document{"returned": doc, "read": got, "found": found[0]} {
		if !reflect.DeepEqual(d.Computed, want) {
			t.Errorf("the document %s: moments %s, want %s", name, dump(d.Computed), dump(want))
		}
	}
	// A refresh moves the moment to its own commit.
	if doc, err = s.Transition(ctx, "Quote", doc.ID, "again", nil, admin); err != nil {
		t.Fatal(err)
	}
	if m := doc.Computed["locked"]; m.Version != 4 || m.At <= sent || doc.Values["locked"] != 0.25 || doc.Values["price"] != 50.0 {
		t.Errorf("after the refresh: moment %+v, locked %v, price %v", m, doc.Values["locked"], doc.Values["price"])
	}
	if m := doc.Computed["margin"]; m.Version != 4 {
		t.Errorf("margin recomputes on the entry of sent again: %+v", m)
	}
}

// The views show the moment beside the value: the stored version and
// error, and the time as <name>__at (ADR-0014, ADR-0043).
func TestTriggered_Views(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineQuote(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Quote", Values{"amount": 200, "rate": 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Quote", doc.ID, "send", nil); err != nil {
		t.Fatal(err)
	}
	history, _ := s.History(ctx, "Quote", doc.ID)
	sent := history[1].ValidFrom
	rows, err := s.SQL(ctx, `SELECT locked, locked__asof, locked__error, locked__at, margin, margin__asof, margin__error, margin__at FROM Quote`, nil, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{0.0, int64(2), nil, sent, nil, nil, "div_by_zero", nil}
	if len(rows.Rows) != 1 || !reflect.DeepEqual(rows.Rows[0], want) {
		t.Errorf("the view: %v, want %v", rows.Rows, want)
	}
	rows, err = s.SQL(ctx, `SELECT version, locked__asof, locked__at FROM Quote_history ORDER BY version`, nil, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows.Rows, [][]any{{int64(1), nil, nil}, {int64(2), int64(2), sent}}) {
		t.Errorf("the history view: %v", rows.Rows)
	}
	// The moment is no variable: a filter cannot name it.
	if _, err := s.Query(ctx, Filter{Class: "Quote", Where: `locked__asof = 2`}); err == nil {
		t.Error("a filter named a moment column")
	}
}

// A moment is hidden with its variable by the read right, and a query
// that asks for some fields returns their moments alone.
func TestTriggered_RightsAndFields(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineQuote(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Quote", Values{"amount": 200, "rate": 0.5}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc, err = s.Transition(ctx, "Quote", doc.ID, "send", nil, As(alice)); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Computed["margin"]; ok || len(doc.Computed) != 1 {
		t.Errorf("alice, who may not read margin, sees the moments %s", dump(doc.Computed))
	}
	seen, err := s.Get(ctx, "Quote", doc.ID, As(finance))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen.Computed["margin"]; !ok || len(seen.Computed) != 2 {
		t.Errorf("finance sees the moments %s", dump(seen.Computed))
	}
	found, err := s.Query(ctx, Filter{Class: "Quote", Fields: []string{"amount", "locked"}}, As(finance))
	if err != nil || len(found) != 1 {
		t.Fatalf("query: %v", err)
	}
	if len(found[0].Computed) != 1 || found[0].Computed["locked"].Version != 2 {
		t.Errorf("the fields amount and locked came with the moments %s", dump(found[0].Computed))
	}
	found, err = s.Query(ctx, Filter{Class: "Quote", Fields: []string{"amount"}}, As(finance))
	if err != nil || len(found) != 1 || found[0].Computed != nil {
		t.Errorf("the field amount came with the moments %s (%v)", dump(found[0].Computed), err)
	}
}

// A preview shows the moments a command would leave, a draft's included,
// and writes none.
func TestTriggered_Preview(t *testing.T) {
	s, _ := openStore(t, Options{Now: atStart})
	defineQuote(t, s)
	ctx := context.Background()
	admin := As(root)
	doc, err := s.Create(ctx, "Quote", Values{"amount": 200, "rate": 0.5}, admin)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := s.Preview(ctx, "Quote", doc.ID, nil, "send", admin)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Moment{"locked": {Version: 2, At: start.Unix()}, "margin": {Version: 2, At: start.Unix()}}
	if !reflect.DeepEqual(pv.Document.Computed, want) || pv.Document.Values["price"] != 100.0 || len(pv.Errors) != 0 {
		t.Errorf("the preview: moments %s, price %v, errors %s", dump(pv.Document.Computed), pv.Document.Values["price"], dump(pv.Errors))
	}
	got, _ := s.Get(ctx, "Quote", doc.ID, admin)
	if got.Version != 1 || got.Computed != nil {
		t.Errorf("the preview wrote: version %d, moments %s", got.Version, dump(got.Computed))
	}
	// A draft that adds a triggered variable: the stored document has no
	// such column, and the preview computes it.
	draft := strings.Replace(quote1, `"price":`, `"sender": {"formula": "USER()", "mode": "triggered", "on": ["transition:send"]}, "price":`, 1)
	pv, err = s.PreviewDraft(ctx, []byte(draft), doc.ID, nil, "send", admin)
	if err != nil {
		t.Fatal(err)
	}
	if m := pv.Document.Computed["sender"]; m.Version != 2 || pv.Document.Values["sender"] != "root" || len(pv.Document.Computed) != 3 {
		t.Errorf("the draft's preview: %s, sender %v", dump(pv.Document.Computed), pv.Document.Values["sender"])
	}
}

// A redefinition that makes a variable triggered adds its moment columns
// and touches no document; a document adopts the new version with its
// next commit and keeps the moments it had (ADR-0039, ADR-0043).
func TestTriggered_Redefinition(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	v1 := `{"name": "Meter", "variables": {"n": {"type": "number"}, "snap": {"formula": "n", "mode": "triggered", "on": ["transition:read"]}, "twice": {"formula": "n * 2"}},
 "states": {"initial": "on", "list": ["on"]}, "transitions": [{"name": "read", "from": "on", "to": "on"}]}`
	v2 := strings.Replace(v1, `"twice": {"formula": "n * 2"}`, `"twice": {"formula": "n * 2", "mode": "triggered", "on": ["transition:read"]}, "note": {"type": "text"}`, 1)
	if _, err := s.DefineClass(ctx, []byte(v1), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Meter", Values{"n": 5})
	if err != nil {
		t.Fatal(err)
	}
	if doc, err = s.Transition(ctx, "Meter", doc.ID, "read", nil); err != nil {
		t.Fatal(err)
	}
	first := doc.Computed["snap"]
	if first.Version != 2 || first.At == 0 {
		t.Fatalf("snap after the first read: %+v", first)
	}
	if _, err := s.DefineClass(ctx, []byte(v2), asSetup); err != nil {
		t.Fatal(err)
	}
	// Read under the version that computed it: untouched.
	got, err := s.Get(ctx, "Meter", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClassVersion != 1 || got.Computed["snap"] != first || got.Values["twice"] != 10.0 {
		t.Errorf("the document after the redefinition: %s", dump(got))
	}
	// A set adopts the new version: snap keeps its moment, twice keeps its
	// value and has none yet, being triggered now and never computed so.
	if doc, err = s.Set(ctx, "Meter", doc.ID, Values{"n": 7, "note": "adopted"}); err != nil {
		t.Fatal(err)
	}
	if doc.ClassVersion != 2 || doc.Computed["snap"] != first || doc.Values["snap"] != 5.0 || doc.Values["twice"] != 10.0 {
		t.Errorf("the adopting commit: %s", dump(doc))
	}
	if _, ok := doc.Computed["twice"]; ok {
		t.Errorf("twice has a moment before any trigger: %s", dump(doc.Computed))
	}
	if doc, err = s.Transition(ctx, "Meter", doc.ID, "read", nil); err != nil {
		t.Fatal(err)
	}
	if doc.Computed["snap"].Version != 4 || doc.Computed["twice"].Version != 4 || doc.Values["twice"] != 14.0 {
		t.Errorf("after a read under the new version: %s", dump(doc))
	}
	// The file opens again with its moments.
	s.Close()
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	again, err := s2.Get(ctx, "Meter", doc.ID)
	if err != nil || !reflect.DeepEqual(again.Computed, doc.Computed) {
		t.Errorf("after a reopen: %s (%v), want %s", dump(again.Computed), err, dump(doc.Computed))
	}
}

// A file made by the engine schema before the moment columns is refused:
// before 1.0 there is no upgrade path (ADR-0043).
func TestTriggered_EngineSchema(t *testing.T) {
	s, path := openStore(t, Options{})
	// ADR-0043 made it 3, for the moment columns; the columns of an
	// action's result that was not stored made it 4 (ADR-0045, item 8);
	// who made a version and where the document came from, and the origin
	// of an entry in the log, made it 5 (ADR-0047, items 3 and 4); what
	// the actor of an event was and for whom it acted made it 6
	// (ADR-0050, item 4); the outbox row losing the administrator's flag,
	// which system groups replace, made it 7 (ADR-0052).
	if engineSchema != 7 {
		t.Fatalf("the engine schema is %d, want 7", engineSchema)
	}
	if _, err := s.writer.ExecContext(context.Background(), "UPDATE schema_version SET version = 3"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, err := Open(path, Options{})
	if code(err) != CodeNotSupported || !strings.Contains(err.Error(), "schema 3") {
		t.Errorf("a schema 3 file: %v", err)
	}
}

// A batch computes the triggered variables of every document it creates.
func TestTriggered_CreateMany(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Stamp", "variables": {"by": {"formula": "USER()", "mode": "triggered", "on": ["enter:new"]}},
 "states": {"initial": "new", "list": ["new"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	docs, err := s.CreateMany(ctx, "Stamp", []Values{{}, {}}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.Values["by"] != "alice" || d.Computed["by"].Version != 1 {
			t.Errorf("%s: by %v, moment %+v", d.ID, d.Values["by"], d.Computed["by"])
		}
	}
}

// Recomputing a triggered variable inside a commit allocates nothing.
func TestTriggered_AllocatesNothing(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineQuote(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Quote", Values{"amount": 200, "rate": 0.5})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.class("Quote")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		t.Fatal(err)
	}
	evalCtx := &formula.Context{User: "alice", Now: start.Unix(), ID: row.id}
	var errs []*Error
	row.state = "sent"
	rate := c.byName["RATE"].leaf
	var failed *Error
	n := 0.0
	allocs := testing.AllocsPerRun(200, func() {
		n++
		row.vals[rate] = formula.Number(n) // so that the value changes and price recalculates
		if err := s.recomputeFor(c, row, commitTransition, "draft", "send", evalCtx, start.Unix(), false, &errs); err != nil {
			failed = err
		}
	})
	if failed != nil {
		t.Fatal(failed)
	}
	if allocs != 0 {
		t.Errorf("%v allocations per recomputation, want none", allocs)
	}
	if got := row.vals[c.byName["LOCKED"].leaf].Num(); got != n {
		t.Errorf("locked is %v, want %v", got, n)
	}
}

// A triggered variable that never held a value may change type, as any
// variable may (ADR-0039); its moment columns stay, since the version
// that computed them still reads them.
func TestTriggered_RetypeKeepsTheMoment(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	v1 := `{"name": "Probe", "variables": {"n": {"type": "number"}, "x": {"type": "number", "formula": "n", "mode": "triggered", "on": ["transition:t"]}},
 "states": {"initial": "on", "list": ["on"]}, "transitions": [{"name": "t", "from": "on", "to": "on"}]}`
	v2 := strings.Replace(v1, `"x": {"type": "number", "formula": "n", "mode": "triggered", "on": ["transition:t"]}`, `"x": {"type": "text", "formula": "TEXT(n)"}`, 1)
	if _, err := s.DefineClass(ctx, []byte(v1), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Probe", Values{})
	if err != nil {
		t.Fatal(err)
	}
	// n is blank, so x is computed and blank: it has a moment and no value.
	if doc, err = s.Transition(ctx, "Probe", doc.ID, "t", nil); err != nil {
		t.Fatal(err)
	}
	if doc.Values["x"] != nil || doc.Computed["x"].Version != 2 {
		t.Fatalf("x after the transition: %v, %+v", doc.Values["x"], doc.Computed["x"])
	}
	if _, err := s.DefineClass(ctx, []byte(v2), asSetup); err != nil {
		t.Fatalf("the retype: %v", err)
	}
	got, err := s.Get(ctx, "Probe", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClassVersion != 1 || got.Computed["x"].Version != 2 {
		t.Errorf("the document under the version that computed it: %s", dump(got))
	}
	if doc, err = s.Set(ctx, "Probe", doc.ID, Values{"n": 4}); err != nil {
		t.Fatal(err)
	}
	if doc.ClassVersion != 2 || doc.Values["x"] != "4" || doc.Computed != nil {
		t.Errorf("after adopting the version where x is an auto text: %s", dump(doc))
	}
}
