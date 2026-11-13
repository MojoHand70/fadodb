package store

import (
	"context"
	"fmt"
	"testing"
)

func TestFilterStatementsAreCached(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{"name": "T", "variables": {"a": {"type": "number"}}, "states": {"initial": "s", "list": ["s"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ctx, "T", Values{"a": float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// The same filter twice is one statement; another filter a second.
	for i := 0; i < 2; i++ {
		docs, err := s.Query(ctx, Filter{Class: "T", Where: "a > 0"})
		if err != nil || len(docs) != 2 {
			t.Fatalf("query: %v %d", err, len(docs))
		}
	}
	if n, err := s.Count(ctx, Filter{Class: "T", Where: "a > 0"}); err != nil || n != 2 {
		t.Fatalf("count: %v %d", err, n)
	}
	if s.filters.size() != 2 {
		t.Errorf("%d statements cached, want 2 (the query and the count)", s.filters.size())
	}
	// The cache is bounded and keeps answering.
	for i := 1; i <= maxFilterStmts+10; i++ {
		if _, err := s.Query(ctx, Filter{Class: "T", Limit: i}); err != nil {
			t.Fatal(err)
		}
	}
	if s.filters.size() != maxFilterStmts {
		t.Errorf("%d statements cached, want the bound %d", s.filters.size(), maxFilterStmts)
	}
	// A redefinition empties it; filters work after.
	if _, err := s.defineOpen(ctx, []byte(`{"name": "T", "variables": {"a": {"type": "number"}, "b": {"type": "text"}}, "states": {"initial": "s", "list": ["s"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if s.filters.size() != 0 {
		t.Errorf("%d statements cached after a redefinition, want none", s.filters.size())
	}
	if docs, err := s.Query(ctx, Filter{Class: "T", Where: `a > 0 AND ISBLANK(b)`}); err != nil || len(docs) != 2 {
		t.Errorf("after the redefinition: %v %d", err, len(docs))
	}
}

// A statement in use is never closed under a call: eviction skips it,
// clear retires it, and the last release closes it.
func TestFilterStatementsInUse(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	var f filterStmts
	held, err := f.acquire(ctx, s.db, "SELECT 0")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxFilterStmts; i++ {
		e, err := f.acquire(ctx, s.db, fmt.Sprintf("SELECT %d", i))
		if err != nil {
			t.Fatal(err)
		}
		f.release(e)
	}
	// Full: the next statement evicts an idle one, never the held one.
	e, err := f.acquire(ctx, s.db, "SELECT 100")
	if err != nil {
		t.Fatal(err)
	}
	f.release(e)
	if f.size() != maxFilterStmts || f.stmts["SELECT 0"] != held {
		t.Fatalf("the held statement left the cache")
	}
	// Every statement in use: the new one is transient.
	for text, e := range f.stmts {
		if text != "SELECT 0" {
			e.inUse++
		}
	}
	tmp, err := f.acquire(ctx, s.db, "SELECT 200")
	if err != nil {
		t.Fatal(err)
	}
	if !tmp.retired || f.size() != maxFilterStmts {
		t.Errorf("a statement with the cache full of running ones should be used once")
	}
	var n int
	if err := tmp.stmt.QueryRowContext(ctx).Scan(&n); err != nil || n != 200 {
		t.Errorf("the transient statement runs: %v %d", err, n)
	}
	f.release(tmp)
	if err := tmp.stmt.QueryRowContext(ctx).Scan(&n); err == nil {
		t.Error("the transient statement is closed on release")
	}
	// clear retires the held statement; it runs until released.
	f.clear()
	if f.size() != 0 || !held.retired {
		t.Errorf("clear left %d statements, held retired %v", f.size(), held.retired)
	}
	if err := held.stmt.QueryRowContext(ctx).Scan(&n); err != nil || n != 0 {
		t.Errorf("the retired statement runs while held: %v", err)
	}
	f.release(held)
	if err := held.stmt.QueryRowContext(ctx).Scan(&n); err == nil {
		t.Error("the retired statement is closed on the last release")
	}
}
