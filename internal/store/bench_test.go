package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// BenchmarkRecalculate is the compute step of a commit on the invoice
// class: every auto variable and the title, from the dirty flags all set,
// as a create runs it. It is the hot path of ADR-0012 inside the store and
// is what the benchmark gate watches; the write step is SQLite's and is
// measured by BenchmarkCommit.
func BenchmarkRecalculate(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(filepath.Join(dir, "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(invoice)); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Invoice")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	evalCtx := &formula.Context{User: "alice", Now: time.Now().Unix()}
	var errs []*Error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dirty := s.dirtyFor(c)
		if err := s.recalc(c, row, dirty, true, evalCtx, false, &errs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCommit is one commit end to end, compute and write: a set of
// one input on the invoice class, with its recalculation, history row,
// child rows carried forward and event, one transaction per commit. It
// writes to disk and runs only when FADODB_BENCH_DIR names a directory
// on a real disk, since a temporary directory may be in memory
// (docs/perf/README.md).
func BenchmarkCommit(b *testing.B) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		b.Skip("set FADODB_BENCH_DIR to a directory on disk to run the commit benchmark")
	}
	path := filepath.Join(dir, fmt.Sprintf("commit-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	s, err := Open(path, Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(invoice)); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		i++
		if _, err := s.Set(ctx, "Invoice", doc.ID, Values{"discount": fmt.Sprintf("0.%02d", i%50)}, As(alice)); err != nil {
			b.Fatal(err)
		}
	}
}

// lookupClass reads one table in an indexed lookup; lookupScan the same
// table through a condition the index cannot serve.
const lookupClass = `{"name": "Quote", "variables": {"currency": {"type": "text"}, "net": {"type": "decimal(2)"},
  "rate": {"formula": "LOOKUP(Fx, rate, code = THIS.currency)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}}`
const lookupScanClass = `{"name": "Quote", "variables": {"currency": {"type": "text"}, "net": {"type": "decimal(2)"},
  "rate": {"formula": "LOOKUP(Fx, rate, code = THIS.currency AND rate > 0)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}}`

func benchLookup(b *testing.B, class string, o Options) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), o)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Fx", "columns": {"code": "text", "rate": "decimal(6)"}, "key": ["code"]}`)); err != nil {
		b.Fatal(err)
	}
	rows := make([]Values, 1000)
	for i := range rows {
		rows[i] = Values{"code": fmt.Sprintf("C%04d", i), "rate": fmt.Sprintf("%d.%06d", i%9+1, i)}
	}
	if _, err := s.ImportRows(ctx, "Fx", rows); err != nil {
		b.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(class)); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Quote", Values{"currency": "C0500", "net": "100.00"})
	if err != nil {
		b.Fatal(err)
	}
	if doc.Values["rate"] != "6.000500" {
		b.Fatalf("rate %v", doc.Values["rate"])
	}
	c, _ := s.class("Quote")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	// The executor runs inside the writer's transaction.
	if _, err := s.writer.ExecContext(ctx, "BEGIN"); err != nil {
		b.Fatal(err)
	}
	defer s.writer.ExecContext(ctx, "ROLLBACK")
	evalCtx := &formula.Context{Now: time.Now().Unix()}
	var errs []*Error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dirty := s.dirtyFor(c)
		if err := s.recalc(c, row, dirty, true, evalCtx, false, &errs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLookupIndexed is the recalculation of a two-formula class whose
// lookup the in-memory index serves (ADR-0036): the cached budget of
// ADR-0016 is under 1 µs.
func BenchmarkLookupIndexed(b *testing.B) { benchLookup(b, lookupClass, Options{}) }

// BenchmarkLookupStatement is the same recalculation through the prepared
// statement for a condition that is not one equality, which runs the
// condition over the table.
func BenchmarkLookupStatement(b *testing.B) { benchLookup(b, lookupScanClass, Options{}) }

// BenchmarkLookupKeyedStatement is the equality shape above
// MaxIndexedRows: the statement narrowed to the key through the table's
// index, the cold budget of ADR-0016 under 100 µs.
func BenchmarkLookupKeyedStatement(b *testing.B) {
	benchLookup(b, lookupClass, Options{MaxIndexedRows: 1})
}

// BenchmarkFilter is the indexed-filter budget of ADR-0012: an equality
// on an indexed text variable over FADODB_BENCH_DOCS documents (100,000
// by default), and for contrast the same over a plain number column. It
// writes to disk and runs only with FADODB_BENCH_DIR, like
// BenchmarkCommit.
func BenchmarkFilter(b *testing.B) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		b.Skip("set FADODB_BENCH_DIR to a directory on disk to run the filter benchmark")
	}
	n := 100000
	if v := os.Getenv("FADODB_BENCH_DOCS"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	path := filepath.Join(dir, fmt.Sprintf("filter-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	s, err := Open(path, Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Ticket", "variables": {"number": {"type": "text", "index": true}, "priority": {"type": "number"}, "owner": {"type": "text"}}}`)); err != nil {
		b.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := s.Create(ctx, "Ticket", Values{"number": fmt.Sprintf("T-%07d", i), "priority": float64(i % 7), "owner": fmt.Sprintf("u%d", i%100)}); err != nil {
			b.Fatal(err)
		}
	}
	b.Logf("%d documents created in %s", n, time.Since(start).Round(time.Millisecond))
	for _, bc := range []struct {
		name string
		f    Filter
	}{
		{"indexed equality", Filter{Class: "Ticket", Where: fmt.Sprintf("number = \"T-%07d\"", n/2)}},
		{"indexed range", Filter{Class: "Ticket", Where: fmt.Sprintf("number >= \"T-%07d\" AND number < \"T-%07d\"", n/2, n/2+10), Limit: 10}},
		{"plain column, first 10", Filter{Class: "Ticket", Where: "priority = 3 AND owner = \"u7\"", Limit: 10}},
		{"count over a plain column", Filter{Class: "Ticket", Where: "priority = 3"}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if strings.HasPrefix(bc.name, "count") {
					if _, err := s.Count(ctx, bc.f); err != nil {
						b.Fatal(err)
					}
					continue
				}
				docs, err := s.Query(ctx, bc.f)
				if err != nil || len(docs) == 0 {
					b.Fatalf("%v, %d documents", err, len(docs))
				}
			}
		})
	}
}

// fiftyFormulas is a class with ten inputs and fifty auto formulas, each
// reading an input and the formula before it, so that every one runs and
// the order matters: the full recalculation of ADR-0012, budget 50 µs.
func fiftyFormulas() string {
	var b strings.Builder
	b.WriteString(`{"name": "Model", "variables": {`)
	inputs := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for _, in := range inputs {
		fmt.Fprintf(&b, `"%s": {"type": "number"},`, in)
	}
	fmt.Fprintf(&b, `"t": {"type": "text"},`)
	prev := "a"
	for k := 1; k <= 50; k++ {
		in := inputs[k%len(inputs)]
		var f string
		switch k % 5 {
		case 0:
			f = fmt.Sprintf("ROUND(%s * 1.1 + %s, 2)", prev, in)
		case 1:
			f = fmt.Sprintf("IF(%s > %s, %s - %s, %s + %s)", prev, in, prev, in, prev, in)
		case 2:
			f = fmt.Sprintf("MAX(%s, %s) / 2", prev, in)
		case 3:
			f = fmt.Sprintf("ABS(%s - %s) + 1", prev, in)
		default:
			f = fmt.Sprintf("%s * 0.5 + %s", prev, in)
		}
		name := fmt.Sprintf("f%02d", k)
		fmt.Fprintf(&b, `"%s": {"formula": "%s"}`, name, f)
		if k < 50 {
			b.WriteString(",")
		}
		prev = name
	}
	b.WriteString("}}")
	return b.String()
}

// BenchmarkRecalculate50 is the full recalculation of a fifty-formula
// document, the ADR-0012 budget of 50 µs.
func BenchmarkRecalculate50(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(fiftyFormulas())); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Model", Values{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10})
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Model")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	evalCtx := &formula.Context{Now: time.Now().Unix()}
	var errs []*Error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dirty := s.dirtyFor(c)
		if err := s.recalc(c, row, dirty, true, evalCtx, false, &errs); err != nil {
			b.Fatal(err)
		}
	}
}
