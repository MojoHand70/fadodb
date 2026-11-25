package query

import (
	"sync"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/testdef"
)

// The read rule of a class and the read rights of its variables are
// kept ready as pieces of a statement, and a statement is written in
// the dialect of the engine that runs it: the store's, or the
// analytical engine's (ADR-0089, item 7). One class is asked by both in
// one program. Each gets the rule and the rights in its own dialect,
// whichever asked first.
func TestCompile_EachDialectItsOwnRuleAndRights(t *testing.T) {
	const claim = `{"name": "Claim", "variables": {
  "owner": {"type": "text"}, "urgent": {"type": "bool"}, "due": {"type": "date"}, "note": {"type": "text"}},
  "states": {"initial": "open", "list": ["open", "review"]},
  "access": {"read": "owner = USER() OR (urgent AND due >= DATE(2026, 1, 1))"},
  "rights": {"review": {"note": {"read": "urgent AND owner = USER()"}}}}`
	define := func() *model.Class {
		t.Helper()
		cls, diags := model.Define(testdef.Open([]byte(claim)), nil)
		if len(diags) > 0 {
			t.Fatal(diags)
		}
		t.Cleanup(func() { Forget(cls) })
		return cls
	}
	ann := Principal{ID: "ann", Groups: []string{"sales"}}
	// What the question is in each dialect: the caller's own condition
	// names a variable with a right, so the rule and the right are both
	// in it.
	asked := Filter{Class: "Claim", Where: `note = "late"`, Order: []string{"note"}}
	store, analytical := formula.Dialect(formula.NewSQLite()), formula.Dialect(formula.NewDuckDB())
	where := func(cls *model.Class, d formula.Dialect) string {
		t.Helper()
		plan, err := CompileFor(d, asked, nil, cls, nil, ann)
		if err != nil {
			t.Fatalf("for %s: %v", d.Name(), err)
		}
		return plan.Where
	}
	want := map[string]string{store.Name(): where(define(), store), analytical.Name(): where(define(), analytical)}
	if want[store.Name()] == want[analytical.Name()] {
		t.Fatal("the test needs a question the two dialects write differently")
	}
	for _, order := range [][]formula.Dialect{{store, analytical}, {analytical, store}, {analytical, store, analytical, store}} {
		cls := define()
		for i, d := range order {
			if got := where(cls, d); got != want[d.Name()] {
				t.Errorf("asked for %s as number %d of %d on one class:\n got %s\nwant %s", d.Name(), i+1, len(order), got, want[d.Name()])
			}
		}
	}
	// Many callers at one moment, half for each dialect, on a class
	// nobody asked yet.
	cls := define()
	var wg sync.WaitGroup
	got := make([]string, 16)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := []formula.Dialect{store, analytical}[i%2]
			if plan, err := CompileFor(d, asked, nil, cls, nil, ann); err == nil {
				got[i] = plan.Where
			}
		}(i)
	}
	wg.Wait()
	for i, where := range got {
		if d := []formula.Dialect{store, analytical}[i%2]; where != want[d.Name()] {
			t.Errorf("caller %d of sixteen at one moment, for %s:\n got %s\nwant %s", i, d.Name(), where, want[d.Name()])
		}
	}
}
