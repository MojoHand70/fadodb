package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// draftsStore makes the store of BenchmarkDraftsOfOneOrder: orders with
// a hundred invoices each, every invoice pointing at its order, and
// every index the engine keeps by itself in place (ADR-0075, item 2):
// on the pointer, on the state and on the two moments. One invoice in
// ten is a draft and the others are paid. FADODB_BENCH_DOCS sets how
// many invoices there are, 10,000 by default and 1,000,000 for the test
// of the record; with FADODB_BENCH_DIR the file is on disk. It answers
// the ids of the orders.
func draftsStore(b *testing.B) (*Store, []string) {
	const each = 100
	docs := 10000
	if v := os.Getenv("FADODB_BENCH_DOCS"); v != "" {
		fmt.Sscanf(v, "%d", &docs)
	}
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		dir = b.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("drafts-%d.fado", time.Now().UnixNano()))
	s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		s.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	})
	ctx := context.Background()
	const open = `"access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}`
	if _, err := s.DefineClasses(ctx, [][]byte{
		[]byte(`{"name": "Order", "title": "number", "variables": {"number": {"type": "text"}},
  "states": {"initial": "open", "list": ["open"]}, ` + open + `}`),
		[]byte(`{"name": "Invoice", "title": "number", "variables": {"number": {"type": "text"},
  "order": {"type": "ref", "class": "Order"}, "net": {"type": "money(2)"}},
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}], ` + open + `}`),
	}, asSetup); err != nil {
		b.Fatal(err)
	}
	who := As(benchReader)
	orders := docs / each
	ids := make([]string, 0, orders)
	batch := make([]Values, 0, 1000)
	drafts := 0
	flush := func(class string, paid bool) {
		if len(batch) == 0 {
			return
		}
		made, err := s.CreateMany(ctx, class, batch, who)
		if err != nil {
			b.Fatal(err)
		}
		batch = batch[:0]
		if class == "Order" {
			for _, d := range made {
				ids = append(ids, d.ID)
			}
			return
		}
		if !paid {
			drafts += len(made)
			return
		}
		moved := make([]string, len(made))
		for i, d := range made {
			moved[i] = d.ID
		}
		for _, name := range []string{"file", "approve"} {
			outcomes, err := s.TransitionMany(ctx, "Invoice", name, moved, nil, who)
			if err != nil {
				b.Fatal(err)
			}
			for _, o := range outcomes {
				if o.Refusal != nil {
					b.Fatal(o.Refusal)
				}
			}
		}
	}
	for o := 0; o < orders; o++ {
		batch = append(batch, Values{"number": fmt.Sprintf("ORD-%07d", o)})
		if len(batch) == cap(batch) {
			flush("Order", false)
		}
	}
	flush("Order", false)
	// Invoice by invoice across the orders, so that the hundred of one
	// order lie apart in the file. Every tenth round stays a draft.
	for i := 0; i < each; i++ {
		for o, id := range ids {
			batch = append(batch, Values{"number": fmt.Sprintf("FV-%07d-%03d", o, i), "order": "Order:" + id, "net": "100.00 PLN"})
			if len(batch) == cap(batch) {
				flush("Invoice", i%10 != 3)
			}
		}
		flush("Invoice", i%10 != 3)
	}
	if drafts != docs/10 {
		b.Fatalf("%d drafts of %d invoices; want one in ten", drafts, docs)
	}
	return s, ids
}

// BenchmarkDraftsOfOneOrder is the test ADR-0075 sets before the engine
// adds an index of its own (item 10): in a class that has every one of
// them, the drafts of one order, which the index on the pointer and the
// index on the state both fit. The engine looks into both and asks
// through the pointer: ten documents of a hundred, where the state
// holds a tenth of the class. It is the whole call of the command
// `query`, for a different order each time. The record asks for under
// 1 ms with 1,000,000 invoices; the gate watches it with 10,000.
func BenchmarkDraftsOfOneOrder(b *testing.B) {
	s, ids := draftsStore(b)
	ctx := context.Background()
	who := As(benchReader)
	ask := func(i int) error {
		where := `order = REF("Order", "` + ids[i*7919%len(ids)] + `") AND state = "draft"`
		docs, err := s.Query(ctx, Filter{Class: "Invoice", Where: where}, who)
		if err == nil && len(docs) != 10 {
			err = fmt.Errorf("%d drafts, want 10", len(docs))
		}
		return err
	}
	warmLive(b, ask)
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := ask(i); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
