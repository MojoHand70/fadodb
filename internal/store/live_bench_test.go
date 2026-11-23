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
func liveListStore(b *testing.B) (*Store, []string) { return liveListStoreWith(b, "") }

// liveFigures are three figures over the list of an order's invoices
// (ADR-0081), for the benchmarks of a figure. The benchmarks of the
// list run on the order without them, as they did before figures were
// built, so that the gate compares like with like.
const liveFigures = `,
  "count": {"mode": "live", "formula": "COUNT(invoices)"},
  "total": {"mode": "live", "formula": "SUM(invoices.net)"},
  "drafts": {"mode": "live", "formula": "SUMIF(invoices, state = \"draft\", net)"}`

// liveFiguresMore are the mean, the middle value and the number of
// different values of the same column. They stand on an order of their
// own, so that the order of BenchmarkLiveFigure, which the gate holds to
// its allocations, stays as it was.
const liveFiguresMore = `,
  "mean": {"mode": "live", "formula": "AVERAGE(invoices.net)"},
  "middle": {"mode": "live", "formula": "MEDIAN(invoices.net)"},
  "kinds": {"mode": "live", "formula": "COUNTUNIQUE(invoices.net)"}`

// liveListStoreWith is liveListStore with more variables on the order.
func liveListStoreWith(b *testing.B, more string) (*Store, []string) {
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
  "invoices": {"mode": "live", "formula": "QUERY(Invoice, {number, net, state}, order = REF(), {number})"}` + more + `},
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

// benchLiveFigure reads one figure over the list of one order: one
// value worked out of its hundred invoices, which no read brings into
// memory (ADR-0081). It is the whole call of the command `live`: the
// order is read on the writer's turn, the figure beside the writer, with
// one statement. A read takes the next order each time.
func benchLiveFigure(b *testing.B, variable string, want any) {
	benchLiveFigureOf(b, liveFigures, variable, want)
}

// benchLiveFigureOf is benchLiveFigure on an order with the figures
// given.
func benchLiveFigureOf(b *testing.B, figures, variable string, want any) {
	s, ids := liveListStoreWith(b, figures)
	ctx := context.Background()
	who := As(benchReader)
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		v, err := s.Live(ctx, "Order", ids[i*7919%len(ids)], variable, who)
		if err != nil || v.Value != want {
			b.Fatalf("%v, %v; want %v", err, v, want)
		}
		i++
	}
}

// BenchmarkLiveFigure adds up a column of the hundred invoices of one
// order, found through the index of the pointer among every invoice of
// the class. The budget of ADR-0081 is under 0.5 ms with 1,000,000
// invoices; the gate watches it with 10,000.
func BenchmarkLiveFigure(b *testing.B) { benchLiveFigure(b, "total", "10000.00 PLN") }

// BenchmarkLiveFigureCount counts them, and BenchmarkLiveFigureIf adds
// up those that meet a condition.
func BenchmarkLiveFigureCount(b *testing.B) { benchLiveFigure(b, "count", 100.0) }

func BenchmarkLiveFigureIf(b *testing.B) { benchLiveFigure(b, "drafts", "10000.00 PLN") }

// BenchmarkLiveFigureAverage is the mean of the column: the sum and the
// count in one statement, divided as the language divides an exact
// amount. BenchmarkLiveFigureMedian is its middle value, for which the
// hundred amounts are put in order inside the statement, and
// BenchmarkLiveFigureUnique how many different amounts there are.
func BenchmarkLiveFigureAverage(b *testing.B) {
	benchLiveFigureOf(b, liveFiguresMore, "mean", "100.000000 PLN")
}

func BenchmarkLiveFigureMedian(b *testing.B) {
	benchLiveFigureOf(b, liveFiguresMore, "middle", "100.000 PLN")
}

func BenchmarkLiveFigureUnique(b *testing.B) { benchLiveFigureOf(b, liveFiguresMore, "kinds", 1.0) }
