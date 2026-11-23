package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// liveListStore makes the store of BenchmarkLiveList: orders with a
// hundred invoices each, every invoice pointing at its order through a
// pointer that has an index, and on the order the list of its invoices
// (ADR-0076). FADODB_BENCH_DOCS sets how many invoices there are, 10,000
// by default and 1,000,000 for the budget of the record; with
// FADODB_BENCH_DIR the file is on disk. It answers the ids of the
// orders.
func liveListStore(b *testing.B) (*Store, []string) {
	const each = 100
	docs := 10000
	if v := os.Getenv("FADODB_BENCH_DOCS"); v != "" {
		fmt.Sscanf(v, "%d", &docs)
	}
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		dir = b.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("list-%d.fado", time.Now().UnixNano()))
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
		[]byte(`{"name": "Order", "title": "number", "variables": {"number": {"type": "text"},
  "invoices": {"mode": "live", "formula": "QUERY(Invoice, {number, net, state}, order = REF(), {number})"}},
  "states": {"initial": "open", "list": ["open"]}, ` + open + `}`),
		[]byte(`{"name": "Invoice", "title": "number", "variables": {"number": {"type": "text"},
  "order": {"type": "ref", "class": "Order", "index": true}, "net": {"type": "money(2)"}},
  "states": {"initial": "draft", "list": ["draft"]}, ` + open + `}`),
	}, asSetup); err != nil {
		b.Fatal(err)
	}
	who := As(benchReader)
	orders := docs / each
	ids := make([]string, 0, orders)
	batch := make([]Values, 0, 1000)
	flush := func(class string) {
		if len(batch) == 0 {
			return
		}
		made, err := s.CreateMany(ctx, class, batch, who)
		if err != nil {
			b.Fatal(err)
		}
		if class == "Order" {
			for _, d := range made {
				ids = append(ids, d.ID)
			}
		}
		batch = batch[:0]
	}
	for o := 0; o < orders; o++ {
		batch = append(batch, Values{"number": fmt.Sprintf("ORD-%07d", o)})
		if len(batch) == cap(batch) {
			flush("Order")
		}
	}
	flush("Order")
	// Invoice by invoice across the orders, so that the hundred of one
	// order lie apart in the file, as invoices written over a year do.
	for i := 0; i < each; i++ {
		for o, id := range ids {
			batch = append(batch, Values{"number": fmt.Sprintf("FV-%07d-%03d", o, i), "order": "Order:" + id, "net": "100.00 PLN"})
			if len(batch) == cap(batch) {
				flush("Invoice")
			}
		}
	}
	flush("Invoice")
	return s, ids
}

// benchReader reads the lists of the benchmark.
var benchReader = Principal{ID: "ewa", Groups: []string{"finance"}}

// BenchmarkLiveList reads the list of one order: its hundred invoices,
// found through the index of the pointer among every invoice of the
// class, each read with three columns. It is the whole call of the
// command `live`: the order is read on the writer's turn, the list
// beside the writer. A read takes the next order each time, so that no
// one order's pages answer every read. The budget of ADR-0076 is under
// 1 ms with 1,000,000 invoices; the gate watches it with 10,000.
func BenchmarkLiveList(b *testing.B) {
	s, ids := liveListStore(b)
	ctx := context.Background()
	who := As(benchReader)
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		v, err := s.LivePage(ctx, LiveRead{Class: "Order", ID: ids[i*7919%len(ids)], Variable: "invoices"}, who)
		if err != nil || len(v.Rows) != 100 {
			b.Fatalf("%v, %d rows", err, len(v.Rows))
		}
		i++
	}
}

// BenchmarkLiveListPage is the same list read a page at a time: the
// first twenty of the hundred, with the word that more follow.
func BenchmarkLiveListPage(b *testing.B) {
	s, ids := liveListStore(b)
	ctx := context.Background()
	who := As(benchReader)
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		v, err := s.LivePage(ctx, LiveRead{Class: "Order", ID: ids[i*7919%len(ids)], Variable: "invoices", Limit: 20}, who)
		if err != nil || len(v.Rows) != 20 || !v.More {
			b.Fatalf("%v, %d rows, more %v", err, len(v.Rows), v.More)
		}
		i++
	}
}
