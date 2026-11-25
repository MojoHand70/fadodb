package store

import (
	"testing"
)

// A bill with lines and tags, and a rule that empties the lines of a
// bill that is marked void when it moves.
const batchBill = `{"name": "Bill",
 "variables": {"n": {"type": "text"}, "void": {"type": "bool"},
   "lines": {"type": "rows", "schema": {"sku": "text"}}, "tags": {"type": "array<text>"},
   "count": {"formula": "COUNT(lines)"}},
 "states": {"initial": "a", "list": ["a", "b"]},
 "transitions": [{"name": "go", "from": "a", "to": "b"}],
 "rules": [{"on": "transition", "name": "go", "if": "void", "do": [{"action": "reset", "variables": ["lines"]}]}]}`

func billLines(skus ...string) []any {
	out := make([]any, len(skus))
	for i, sku := range skus {
		out[i] = map[string]any{"sku": sku}
	}
	return out
}

// lists says the lines and the tags a bill holds, as the store has them.
func lists(t *testing.T, s *Store, id string) string {
	t.Helper()
	d, err := s.Get(t.Context(), "Bill", id)
	if err != nil {
		t.Fatal(err)
	}
	return flat(d.Values["lines"]) + " " + flat(d.Values["tags"]) + " count " + flat(d.Values["count"])
}

// Each document of a batch has its own lists: one that gives none has
// none, and not the lists of the document before it in the batch.
func TestBatch_CreateManyKeepsListsApart(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.defineOpen(ctx, []byte(batchBill), asSetup); err != nil {
		t.Fatal(err)
	}
	docs, err := s.CreateMany(ctx, "Bill", []Values{
		{"n": "one", "lines": billLines("A", "B"), "tags": []any{"x"}},
		{"n": "two"},
		{"n": "three", "tags": []any{"y", "z"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{`[{"sku":"A"},{"sku":"B"}] ["x"] count 2`, `[] [] count 0`, `[] ["y","z"] count 0`} {
		if got := flat(docs[i].Values["lines"]) + " " + flat(docs[i].Values["tags"]) + " count " + flat(docs[i].Values["count"]); got != want {
			t.Errorf("document %d of the batch is answered with %s; want %s", i, got, want)
		}
		if got := lists(t, s, docs[i].ID); got != want {
			t.Errorf("document %d of the batch holds %s; want %s", i, got, want)
		}
	}
}

// A batch of moves leaves each document its own lists: the lists of one
// are not written to the next, and a rule that empties the lines of one
// does not empty the lines of the others.
func TestBatch_TransitionManyKeepsListsApart(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.defineOpen(ctx, []byte(batchBill), asSetup); err != nil {
		t.Fatal(err)
	}
	for id, v := range map[string]Values{
		"B-1": {"n": "one", "void": true, "lines": billLines("A", "B"), "tags": []any{"x"}},
		"B-2": {"n": "two", "lines": billLines("C"), "tags": []any{"y"}},
		"B-3": {"n": "three"},
		"B-4": {"n": "four", "lines": billLines("D", "E", "F")},
	} {
		if _, err := s.Create(ctx, "Bill", v, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{
		"B-1": `[] ["x"] count 0`, // void: its rule empties its lines
		"B-2": `[{"sku":"C"}] ["y"] count 1`,
		"B-3": `[] [] count 0`,
		"B-4": `[{"sku":"D"},{"sku":"E"},{"sku":"F"}] [] count 3`,
	}
	ids := []string{"B-1", "B-2", "B-3", "B-4"}
	pre, err := s.PreviewMany(ctx, "Bill", "go", ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.TransitionMany(ctx, "Bill", "go", ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		for what, m := range map[string]MoveOutcome{"the preview": pre[i], "the batch": out[i]} {
			if !m.Moved() {
				t.Fatalf("%s: %s did not move: %s", what, id, flat(m))
			}
			if got := flat(m.Document.Values["lines"]) + " " + flat(m.Document.Values["tags"]) + " count " + flat(m.Document.Values["count"]); got != want[id] {
				t.Errorf("%s answers %s with %s; want %s", what, id, got, want[id])
			}
		}
		if got := lists(t, s, id); got != want[id] {
			t.Errorf("after the batch %s holds %s; want %s", id, got, want[id])
		}
	}
}

// What is worked out from a list is worked out again when a reset
// empties the list: by a command, in its preview, and by a rule.
func TestReset_AFigureOverAnEmptiedList(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.defineOpen(ctx, []byte(batchBill), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"R-1", "R-2"} {
		if d, err := s.Create(ctx, "Bill", Values{"n": id, "void": id == "R-2", "lines": billLines("A", "B"), "tags": []any{"x"}}, WithID(id)); err != nil || d.Values["count"] != 2.0 {
			t.Fatalf("%s: %s, %v", id, flat(d), err)
		}
	}
	// A command resets the lines.
	d, err := s.Reset(ctx, "Bill", "R-1", []string{"lines"})
	if err != nil || d.Version != 2 || d.Values["count"] != 0.0 {
		t.Errorf("a reset of the lines answers %s, %v; want version 2 and count 0", flat(d), err)
	}
	if got := lists(t, s, "R-1"); got != `[] ["x"] count 0` {
		t.Errorf("after the reset R-1 holds %s", got)
	}
	// A reset of lines that are empty already changes nothing.
	if d, err := s.Reset(ctx, "Bill", "R-1", []string{"lines"}); err != nil || d.Version != 2 {
		t.Errorf("a reset of empty lines: %s, %v; want version 2 still", flat(d), err)
	}
	// A rule resets the lines of a void bill when it moves.
	p, err := s.Preview(ctx, "Bill", "R-2", nil, "go")
	if err != nil || p.Document.Values["count"] != 0.0 {
		t.Errorf("the preview of the move answers %s, %v; want count 0", flat(p.Document), err)
	}
	d, err = s.Transition(ctx, "Bill", "R-2", "go", nil)
	if err != nil || d.Values["count"] != 0.0 {
		t.Errorf("the move answers %s, %v; want count 0", flat(d), err)
	}
	if got := lists(t, s, "R-2"); got != `[] ["x"] count 0` {
		t.Errorf("after the move R-2 holds %s", got)
	}
}
