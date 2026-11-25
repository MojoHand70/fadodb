package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// BenchmarkTransitionBefore is a transition commit with one value
// brought before its guard (ADR-0078, item 4): the order counts the
// votes that name it, 50 of its 100, found through the index of the
// order among the 10,000 votes of the class, and its guard reads the
// count. The order goes submit, back, submit, ... and each of the two
// brings the count, so every iteration is one named transition with one
// lookup before its guard and no chain. It is BenchmarkTransition and
// BenchmarkLookupClass in one command, and is held in the gate beside
// them. The classes are its own: a variable added to the class of
// another benchmark changes that benchmark's allocations.
func BenchmarkTransitionBefore(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Vote", "variables": {"order": {"type": "text", "index": true}, "ok": {"type": "bool"}},
  "states": {"initial": "cast", "list": ["cast"]}}`,
		`{"name": "Order", "variables": {
    "amount": {"type": "number"},
    "yes":    {"type": "number", "formula": "LOOKUP(Vote, COUNT(id), order = ID() AND ok)", "mode": "triggered", "on": ["before:submit", "before:back"]}},
  "states": {"initial": "draft", "list": ["draft", "open"]},
  "transitions": [{"name": "submit", "from": "draft", "to": "open", "guard": "yes >= 1 AND amount > 0"},
                  {"name": "back", "from": "open", "to": "draft", "guard": "yes >= 1"}]}`,
	} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			b.Fatal(err)
		}
	}
	batch := make([]Values, 0, 1000)
	for i := 0; i < 10000; i++ {
		batch = append(batch, Values{"order": fmt.Sprintf("O-%05d", i/100), "ok": i%2 == 0})
		if len(batch) == cap(batch) {
			if _, err := s.CreateMany(ctx, "Vote", batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	doc, err := s.Create(ctx, "Order", Values{"amount": 500}, WithID("O-00010"))
	if err != nil {
		b.Fatal(err)
	}
	names := [2]string{"submit", "back"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		d, err := s.Transition(ctx, "Order", doc.ID, names[i%2], nil, As(alice))
		if err != nil {
			b.Fatal(err)
		}
		if i == 0 && d.Values["yes"] != 50.0 {
			b.Fatalf("the count brought before the guard is %v, want 50", d.Values["yes"])
		}
	}
}
