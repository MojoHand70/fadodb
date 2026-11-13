package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
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
	if _, err := s.defineOpen(ctx, []byte(invoice), asSetup); err != nil {
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
	if _, err := s.defineOpen(ctx, []byte(invoice), asSetup); err != nil {
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
  "rate": {"formula": "LOOKUP(Fx, rate, code = THIS.currency)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}, "states": {"initial": "open", "list": ["open"]}}`
const lookupScanClass = `{"name": "Quote", "variables": {"currency": {"type": "text"}, "net": {"type": "decimal(2)"},
  "rate": {"formula": "LOOKUP(Fx, rate, code >= THIS.currency AND rate > 0)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}, "states": {"initial": "open", "list": ["open"]}}`

// lookupNarrowedClass has the key as one term of an AND (ADR-0046): the
// statement is narrowed by it and the condition decides among its rows.
const lookupNarrowedClass = `{"name": "Quote", "variables": {"currency": {"type": "text"}, "net": {"type": "decimal(2)"},
  "rate": {"formula": "LOOKUP(Fx, rate, code = THIS.currency AND rate > 0)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}, "states": {"initial": "open", "list": ["open"]}}`

func benchLookup(b *testing.B, class string, o Options) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), o)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpenTable(ctx, []byte(`{"name": "Fx", "columns": {"code": "text", "rate": "decimal(6)"}, "key": ["code"]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	rows := make([]Values, 1000)
	for i := range rows {
		rows[i] = Values{"code": fmt.Sprintf("C%04d", i), "rate": fmt.Sprintf("%d.%06d", i%9+1, i)}
	}
	if _, err := s.ImportRows(ctx, "Fx", rows); err != nil {
		b.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(class), asSetup); err != nil {
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
// statement for a condition that holds no equality to narrow by, which
// runs the condition over the table.
func BenchmarkLookupStatement(b *testing.B) { benchLookup(b, lookupScanClass, Options{}) }

// BenchmarkLookupNarrowed is the recalculation with the key as one term
// of an AND: a statement narrowed to the key through the table's index,
// whatever the size of the table (ADR-0046).
func BenchmarkLookupNarrowed(b *testing.B) { benchLookup(b, lookupNarrowedClass, Options{}) }

// benchAggregate is the recalculation of a class with one aggregate over
// ten rows of a table of 1,000, narrowed by the key (ADR-0046).
func benchAggregate(b *testing.B, expr string, want any) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpenTable(ctx, []byte(`{"name": "Lines", "columns": {"ord": "text", "n": "number", "qty": "number", "amount": "decimal(2)"}, "key": ["ord", "n"]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	rows := make([]Values, 1000)
	for i := range rows {
		rows[i] = Values{"ord": fmt.Sprintf("O%03d", i/10), "n": i % 10, "qty": float64(i%10) + 0.5, "amount": fmt.Sprintf("%d.%02d", i%10, i%100)}
	}
	if _, err := s.ImportRows(ctx, "Lines", rows); err != nil {
		b.Fatal(err)
	}
	class := `{"name": "Bill", "variables": {"ord": {"type": "text"}, "x": {"formula": "LOOKUP(Lines, ` + expr + `, ord = THIS.ord)"}}, "states": {"initial": "open", "list": ["open"]}}`
	if _, err := s.defineOpen(ctx, []byte(class), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Bill", Values{"ord": "O050"})
	if err != nil {
		b.Fatal(err)
	}
	if doc.Values["x"] != want {
		b.Fatalf("x is %v, want %v", doc.Values["x"], want)
	}
	c, _ := s.class("Bill")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
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

// BenchmarkLookupCount counts ten rows of a key.
func BenchmarkLookupCount(b *testing.B) { benchAggregate(b, "COUNT(n)", 10.0) }

// BenchmarkLookupSumExact adds ten exact values with the engine's own
// aggregate, in two halves.
func BenchmarkLookupSumExact(b *testing.B) { benchAggregate(b, "SUM(amount)", "45.45") }

// BenchmarkLookupSumNumber adds ten numbers in key order, each handed to
// the Go function the dialect registers.
func BenchmarkLookupSumNumber(b *testing.B) { benchAggregate(b, "SUM(qty)", 50.0) }

// BenchmarkLookupKeyedStatement is the equality shape above
// MaxIndexedRows: the statement narrowed to the key through the table's
// index, the cold budget of ADR-0016 under 100 µs.
func BenchmarkLookupKeyedStatement(b *testing.B) {
	benchLookup(b, lookupClass, Options{MaxIndexedRows: 1})
}

// benchClassLookup evaluates one formula of a purchase that reads its
// approvals (ADR-0046): docs approvals in all, 100 of them of the
// purchase, half of those granted. The formula runs as a commit runs it,
// on the writer inside a transaction. FADODB_BENCH_DOCS sets the size of
// the class, 10,000 by default and 100,000 for the budget of the record;
// with FADODB_BENCH_DIR the file is on disk.
func benchClassLookup(b *testing.B, expr string, want any) {
	s, doc := classLookupStore(b, expr, want)
	ctx := context.Background()
	c, _ := s.class("Purchase")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	prog := c.byName["X"].v.Formula
	if _, err := s.writer.ExecContext(ctx, "BEGIN"); err != nil {
		b.Fatal(err)
	}
	defer s.writer.ExecContext(ctx, "ROLLBACK")
	evalCtx := &formula.Context{Now: time.Now().Unix(), ID: doc.ID, State: doc.State}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.eval(c, prog, row, evalCtx); err != nil {
			b.Fatal(err)
		}
	}
}

// classLookupStore makes the store of benchClassLookup and the purchase
// P-00010, refreshed once, whose x is want.
func classLookupStore(b *testing.B, expr string, want any) (*Store, *Document) {
	docs := 10000
	if v := os.Getenv("FADODB_BENCH_DOCS"); v != "" {
		fmt.Sscanf(v, "%d", &docs)
	}
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		dir = b.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("reads-%d.fado", time.Now().UnixNano()))
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
	for _, def := range []string{
		`{"name": "Approval", "variables": {"purchase": {"type": "text", "index": true}, "amount": {"type": "decimal(2)"}, "ok": {"type": "bool"}},
  "states": {"initial": "asked", "list": ["asked"]}}`,
		`{"name": "Purchase", "variables": {"x": {"type": "decimal(2)", "formula": "DECIMAL(LOOKUP(Approval, ` + expr + `, purchase = ID() AND ok), 2)", "mode": "triggered", "on": ["refresh"]}},
  "states": {"initial": "waiting", "list": ["waiting", "approved"]},
  "transitions": [{"from": "waiting", "to": "approved", "guard": "x > 1000000"}]}`,
	} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			b.Fatal(err)
		}
	}
	batch := make([]Values, 0, 1000)
	for i := 0; i < docs; i++ {
		batch = append(batch, Values{"purchase": fmt.Sprintf("P-%05d", i/100), "amount": fmt.Sprintf("%d.%02d", i%7, i%100), "ok": i%2 == 0})
		if len(batch) == cap(batch) || i == docs-1 {
			if _, err := s.CreateMany(ctx, "Approval", batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	doc, err := s.Create(ctx, "Purchase", Values{}, WithID("P-00010"))
	if err != nil {
		b.Fatal(err)
	}
	if doc, err = s.Refresh(ctx, "Purchase", doc.ID, nil); err != nil || doc.Values["x"] != want {
		b.Fatalf("x is %v, want %v: %v", doc.Values["x"], want, err)
	}
	return s, doc
}

// BenchmarkRefreshClassCount is the whole command the pattern of
// parallel approvals runs when an approval is granted: the refresh of
// the purchase, which counts its approvals, looks at its eventless guard
// and commits. The budget is the 2 ms of a transition commit.
func BenchmarkRefreshClassCount(b *testing.B) {
	s, doc := classLookupStore(b, "COUNT(id)", "50.00")
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Refresh(ctx, "Purchase", doc.ID, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLookupClass counts the granted approvals of one purchase: 50
// of its 100, found through the index of the purchase among every
// approval of the class. The budget of ADR-0046 is under 100 µs with
// 100,000 approvals; the gate watches it with 10,000.
func BenchmarkLookupClass(b *testing.B) { benchClassLookup(b, "COUNT(id)", "50.00") }

// BenchmarkLookupClassSum adds their amounts, which are exact.
func BenchmarkLookupClassSum(b *testing.B) { benchClassLookup(b, "SUM(amount)", "177.50") }

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
	// Ticket has no rights; Guarded is the same class with a formula read
	// right on the indexed variable, which the filter compiles into its
	// predicate (ADR-0041), the budget being under 5% on the indexed
	// filter.
	const ticket = `{"name": "%s", "variables": {"number": {"type": "text", "index": true}, "priority": {"type": "number"}, "owner": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}%s}`
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(ticket, "Ticket", "")), asSetup); err != nil {
		b.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(ticket, "Guarded", `, "rights": {"*": {"number": {"read": "INGROUP(\"support\") OR owner = USER()"}}}`)), asSetup); err != nil {
		b.Fatal(err)
	}
	// Owned is Ticket with an index on the owner, and Ruled the same with
	// a read rule (ADR-0049): support sees every ticket, anybody else
	// their own. The budgets are under 5% on the indexed filter where the
	// caller alone decides the rule, and under 15% where it reads the row.
	const owned = `{"name": "%s", "variables": {"number": {"type": "text", "index": true}, "priority": {"type": "number"}, "owner": {"type": "text", "index": true}}, "states": {"initial": "open", "list": ["open"]}%s}`
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(owned, "Owned", "")), asSetup); err != nil {
		b.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(owned, "Ruled", `, "access": {"read": "INGROUP(\"support\") OR owner = USER()"}`)), asSetup); err != nil {
		b.Fatal(err)
	}
	support := As(Principal{ID: "agent", Groups: []string{"support"}})
	// u7 owns one ticket in a hundred, and the one the equality finds.
	half := n/2 - (n/2)%100 + 7
	u7 := As(Principal{ID: "u7", Groups: []string{"callers"}})
	start := time.Now()
	for _, class := range []string{"Ticket", "Guarded", "Owned", "Ruled"} {
		for i := 0; i < n; i++ {
			if _, err := s.Create(ctx, class, Values{"number": fmt.Sprintf("T-%07d", i), "priority": float64(i % 7), "owner": fmt.Sprintf("u%d", i%100)}); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Logf("%d documents of four classes created in %s", 4*n, time.Since(start).Round(time.Millisecond))
	for _, bc := range []struct {
		name string
		f    Filter
		opts []Option
	}{
		{"indexed equality", Filter{Class: "Ticket", Where: fmt.Sprintf("number = \"T-%07d\"", n/2)}, nil},
		{"indexed range", Filter{Class: "Ticket", Where: fmt.Sprintf("number >= \"T-%07d\" AND number < \"T-%07d\"", n/2, n/2+10), Limit: 10}, nil},
		{"plain column, first 10", Filter{Class: "Ticket", Where: "priority = 3 AND owner = \"u7\"", Limit: 10}, nil},
		{"count over a plain column", Filter{Class: "Ticket", Where: "priority = 3"}, nil},
		{"indexed equality with a read right", Filter{Class: "Guarded", Where: fmt.Sprintf("number = \"T-%07d\"", n/2)}, []Option{support}},
		{"indexed range with a read right", Filter{Class: "Guarded", Where: fmt.Sprintf("number >= \"T-%07d\" AND number < \"T-%07d\"", n/2, n/2+10), Limit: 10}, []Option{support}},
		// The read rule of ADR-0049: the same equality on a class
		// without one, with one the caller alone decides, and with one
		// that reads the row.
		{"indexed equality, no read rule", Filter{Class: "Owned", Where: fmt.Sprintf("number = \"T-%07d\"", half)}, []Option{u7}},
		{"indexed equality, a read rule the caller decides", Filter{Class: "Ruled", Where: fmt.Sprintf("number = \"T-%07d\"", half)}, []Option{support}},
		{"indexed equality, a read rule over the row", Filter{Class: "Ruled", Where: fmt.Sprintf("number = \"T-%07d\"", half)}, []Option{u7}},
		// A caller's own tickets: by the rule alone, and by the same
		// condition written out on the class without a rule.
		{"own tickets by the read rule, first 10", Filter{Class: "Ruled", Limit: 10}, []Option{u7}},
		{"own tickets by a condition, first 10", Filter{Class: "Owned", Where: "owner = \"u7\"", Limit: 10}, []Option{u7}},
		{"count own tickets by the read rule", Filter{Class: "Ruled"}, []Option{u7}},
		{"count own tickets by a condition", Filter{Class: "Owned", Where: "owner = \"u7\""}, []Option{u7}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if strings.HasPrefix(bc.name, "count") {
					if _, err := s.Count(ctx, bc.f, bc.opts...); err != nil {
						b.Fatal(err)
					}
					continue
				}
				docs, err := s.Query(ctx, bc.f, bc.opts...)
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
	b.WriteString(`}, "states": {"initial": "open", "list": ["open"]}}`)
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
	if _, err := s.defineOpen(ctx, []byte(fiftyFormulas()), asSetup); err != nil {
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

// benchFlow is the class of the transition benchmarks: a guard over a
// value and one over the principal, an eventless transition, and a
// handful of formulas so that the recalculation is not trivial.
const benchFlow = `{"name": "Flow", "variables": {
  "amount": {"type": "number"}, "note": {"type": "text"}, "qty": {"type": "number"},
  "big": {"formula": "amount > 1000"}, "unit": {"formula": "IF(qty > 0, amount / qty, 0)"}, "label": {"formula": "note & \": \" & TEXT(amount)"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "booked", "closed"]},
 "transitions": [
   {"name": "submit",  "from": "draft",    "to": "review",   "guard": "amount > 0"},
   {"name": "approve", "from": "review",   "to": "approved", "guard": "INGROUP(\"finance\")"},
   {"name": "back",    "from": "review",   "to": "draft"},
   {"from": "approved", "to": "booked", "guard": "NOT big"},
   {"name": "close",   "from": ["booked", "approved"], "to": "closed"},
   {"name": "reopen",  "from": "closed",   "to": "draft"}
 ]}`

// BenchmarkTransition measures a transition commit with its guard, write
// and event (ADR-0040): the document goes submit, back, submit, ... so
// that every iteration is one named transition and no chain.
func BenchmarkTransition(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchFlow), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Flow", Values{"amount": 500, "qty": 5, "note": "n"})
	if err != nil {
		b.Fatal(err)
	}
	names := [2]string{"submit", "back"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := s.Transition(ctx, "Flow", doc.ID, names[i%2], nil, As(alice)); err != nil {
			b.Fatal(err)
		}
	}
}

// benchOwned runs BenchmarkTransition's loop on its class with an owner
// variable added, and with the access block given.
func benchOwned(b *testing.B, access string) { benchOwnedAs(b, access, alice) }

// benchOwnedAs is benchOwned with the caller of the transitions given:
// alice, who owns the document, somebody who acts for her, with the
// substitution that lets them, or anybody else with the options their
// call needs.
func benchOwnedAs(b *testing.B, access string, caller Principal, more ...Option) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	owned := strings.Replace(benchFlow, `"variables": {`, `"variables": {"owner": {"type": "text", "default": "USER()"},`, 1)
	owned = strings.Replace(owned, `"states":`, access+`"states":`, 1)
	if _, err := s.defineOpen(ctx, []byte(owned), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Flow", Values{"amount": 500, "qty": 5, "note": "n"}, As(alice))
	if err != nil {
		b.Fatal(err)
	}
	if caller.For != "" {
		if _, err := s.Create(ctx, model.SubstitutionClass, Values{"by": caller.ID}, As(Principal{ID: caller.For})); err != nil {
			b.Fatal(err)
		}
	}
	names := [2]string{"submit", "back"}
	opts := append([]Option{As(caller)}, more...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := s.Transition(ctx, "Flow", doc.ID, names[i%2], nil, opts...); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTransitionAccess is BenchmarkTransition on a class with the
// rules of ADR-0049: the commit asks the read rule, then the update rule,
// since submit and back have no rule of their own, and the read rule
// again for the document it answers with. BenchmarkTransitionOwned is
// the same class without the rules, to compare with: three evaluations
// more, and no allocation more.
func BenchmarkTransitionAccess(b *testing.B) {
	benchOwned(b, `"access": {"read": "owner = USER() OR INGROUP(\"finance\")", "update": "owner = USER() AND amount < 100000", "delete": "FALSE"},
 `)
}

func BenchmarkTransitionOwned(b *testing.B) { benchOwned(b, "") }

// BenchmarkTransitionFor is BenchmarkTransitionAccess for a caller that
// acts for the owner (ADR-0053): the rules accept whoever validly acts
// for her, and every commit asks the substitution, which the engine has
// in memory after the first: one lookup by the pair, and no allocation.
// The two allocations it has more than the owner's own commit are those
// of a caller who names a "for", which the event keeps (ADR-0050): a
// host that answers for the "for" itself pays the same two.
func BenchmarkTransitionFor(b *testing.B) {
	benchOwnedAs(b, `"access": {"read": "owner = USER() OR owner = ACTINGFOR() OR INGROUP(\"finance\")", "update": "(owner = USER() OR owner = ACTINGFOR()) AND amount < 100000", "delete": "FALSE"},
 `, Principal{ID: "bob", For: alice.ID})
}

// BenchmarkTransitionOverride is the same transition by a content
// administrator who is neither the owner nor in finance (ADR-0052, item
// 4): the rules are asked and refuse, the group lets the command
// through, and the event keeps the two rules lifted and the reason.
func BenchmarkTransitionOverride(b *testing.B) {
	benchOwnedAs(b, `"access": {"read": "owner = USER() OR INGROUP(\"finance\")", "update": "owner = USER() AND amount < 100000", "delete": "FALSE"},
 `, Principal{ID: "cora", System: []string{GroupContentAdministrators}}, Reason("the owner is away and the customer waits"))
}

// benchMany opens a store with the transition benchmarks' class and n
// documents of it in draft, with the amount given.
func benchMany(b *testing.B, n int, amount float64) (*Store, []string) {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchFlow), asSetup); err != nil {
		b.Fatal(err)
	}
	docs := make([]Values, n)
	for i := range docs {
		docs[i] = Values{"amount": amount, "qty": 5, "note": "n"}
	}
	made, err := s.CreateMany(ctx, "Flow", docs)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, n)
	for i, d := range made {
		ids[i] = d.ID
	}
	return s, ids
}

// BenchmarkTransitionMany measures a batch of 1,000 moves in one
// transaction (ADR-0048): the documents go submit, back, submit, ... as
// the one of BenchmarkTransition does, so an iteration is 1,000 of its
// iterations with one transaction and a savepoint a document.
func BenchmarkTransitionMany(b *testing.B) {
	s, ids := benchMany(b, 1000, 500)
	ctx := context.Background()
	names := [2]string{"submit", "back"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		out, err := s.TransitionMany(ctx, "Flow", names[i%2], ids, nil, As(alice))
		if err != nil {
			b.Fatal(err)
		}
		if !out[len(out)-1].Moved() {
			b.Fatal(out[len(out)-1].Refusal)
		}
	}
}

// BenchmarkTransitionManyRefused measures a batch of 1,000 documents
// that are all refused by their guard: what a document that does not
// move costs the batch.
func BenchmarkTransitionManyRefused(b *testing.B) {
	s, ids := benchMany(b, 1000, 0)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		out, err := s.TransitionMany(ctx, "Flow", "submit", ids, nil, As(alice))
		if err != nil {
			b.Fatal(err)
		}
		if out[0].Refusal == nil || out[0].Refusal.Code != CodeGuardFalse {
			b.Fatal(out[0].Refusal)
		}
	}
}

// BenchmarkPreviewMany measures the batch of BenchmarkTransitionMany
// rolled back.
func BenchmarkPreviewMany(b *testing.B) {
	s, ids := benchMany(b, 1000, 500)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.PreviewMany(ctx, "Flow", "submit", ids, nil, As(alice)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSavepoint measures the mark a batch of moves sets before a
// document and releases after it, by itself (ADR-0048, item 3).
func BenchmarkSavepoint(b *testing.B) {
	s, _ := benchMany(b, 1, 500)
	ctx := context.Background()
	if _, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		b.Fatal(err)
	}
	defer s.writer.ExecContext(ctx, "ROLLBACK")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.stmts.mark.ExecContext(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := s.stmts.unmark.ExecContext(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTransitionChain measures a named transition followed by one
// eventless hop, two commits in one transaction: approve books, close,
// reopen, submit, and again.
func BenchmarkTransitionChain(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchFlow), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Flow", Values{"amount": 500, "qty": 5, "note": "n"})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Flow", doc.ID, "submit", nil); err != nil {
		b.Fatal(err)
	}
	fin := Principal{ID: "fin", Groups: []string{"finance"}}
	cycle := [4]string{"approve", "close", "reopen", "submit"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := s.Transition(ctx, "Flow", doc.ID, cycle[i%4], nil, As(fin)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTransitions measures the listing of the transitions leaving a
// state, guards evaluated, over the stored document: budget 10 µs for
// ten candidates (ADR-0040); here three leave review.
func BenchmarkTransitions(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchFlow), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Flow", Values{"amount": 500, "qty": 5, "note": "n"})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Flow", doc.ID, "submit", nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Transitions(ctx, "Flow", doc.ID, As(alice)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGuardLoop measures the guard evaluation alone, the loop of the
// chain and the listing, which must not allocate.
func BenchmarkGuardLoop(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchFlow), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Flow", Values{"amount": 500, "qty": 5, "note": "n"})
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Flow")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	evalCtx := &formula.Context{User: "alice", Groups: []string{"sales"}, Now: time.Now().Unix(), State: row.state, ID: row.id}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, t := range c.cls.Transitions {
			if _, err := s.holds(c, t, row, evalCtx); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// fiftyRights is a class of an owner and fifty numbers with a rights cell
// each: the odd ones a group list, the even ones a formula over the
// document, read and write alike (ADR-0041).
func fiftyRights() string {
	var b strings.Builder
	b.WriteString(`{"name": "Guarded", "variables": {"owner": {"type": "text", "default": "USER()"}`)
	for k := 1; k <= 50; k++ {
		fmt.Fprintf(&b, `, "v%02d": {"type": "number"}`, k)
	}
	b.WriteString(`}, "states": {"initial": "open", "list": ["open"]}, "rights": {"*": {`)
	for k := 1; k <= 50; k++ {
		if k > 1 {
			b.WriteString(", ")
		}
		if k%2 == 1 {
			fmt.Fprintf(&b, `"v%02d": {"read": ["sales", "finance"], "write": ["sales"]}`, k)
		} else {
			fmt.Fprintf(&b, `"v%02d": {"read": "owner = USER() OR v%02d > 1000", "write": "owner = USER()"}`, k, k)
		}
	}
	b.WriteString(`}}}`)
	return b.String()
}

func openGuarded(b *testing.B) (*Store, *class, *docRow) {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(fiftyRights()), asSetup); err != nil {
		b.Fatal(err)
	}
	values := Values{}
	for k := 1; k <= 50; k++ {
		values[fmt.Sprintf("v%02d", k)] = float64(k)
	}
	doc, err := s.Create(ctx, "Guarded", values, As(alice))
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Guarded")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	return s, c, row
}

// BenchmarkProject is the read projection of ADR-0041 over a document of
// fifty variables with a rights cell each, group lists and formulas
// alike: the budget of ADR-0028 is 5 µs per document, and the loop must
// not allocate.
func BenchmarkProject(b *testing.B) {
	s, c, row := openGuarded(b)
	doc := c.document(row, nil)
	template := make(Values, len(doc.Values))
	for k, v := range doc.Values {
		template[k] = v
	}
	ev := s.reader()
	defer s.release(ev)
	p := Principal{ID: "bob", Groups: []string{"finance"}}
	now := time.Now().Unix()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for k, v := range template {
			doc.Values[k] = v
		}
		s.project(ev, c, c, row, doc, p, now)
	}
}

// BenchmarkWriteCheck is the write check of ADR-0041 over a command that
// sets ten variables, five under a group list and five under a formula:
// one formula per set variable inside the commit budget, allocating
// nothing.
func BenchmarkWriteCheck(b *testing.B) {
	s, c, row := openGuarded(b)
	values := Values{}
	for k := 1; k <= 10; k++ {
		values[fmt.Sprintf("v%02d", k)] = float64(k)
	}
	cmd := command{op: opSet, c: c, values: values, o: options{principal: alice}}
	evalCtx := &formula.Context{User: "alice", Groups: alice.Groups, Now: time.Now().Unix(), State: row.state, ID: row.id}
	res := &result{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.checkWrites(c, &cmd, row, evalCtx, res); err != nil {
			b.Fatal(err)
		}
	}
}

// benchChain is a class whose two named transitions each start a chain
// of three: the named move and two eventless ones, so that every command
// is three state changes in one transaction (ADR-0040).
const benchChain = `{"name": "Chain", "variables": {"n": {"type": "number"}, "note": {"type": "text"}},
 "states": {"initial": "s0", "list": ["s0", "s1", "s2", "s3", "s4", "s5"]},
 "transitions": [
   {"name": "go",   "from": "s0", "to": "s1"},
   {"from": "s1", "to": "s2", "guard": "n >= 0"},
   {"from": "s2", "to": "s3", "guard": "n >= 0"},
   {"name": "back", "from": "s3", "to": "s4"},
   {"from": "s4", "to": "s5", "guard": "n >= 0"},
   {"from": "s5", "to": "s0", "guard": "n >= 0"}
 ]}`

// benchListing is a class with ten named transitions leaving its one
// working state, each with a guard of its own, for the listing budget of
// ADR-0040: ten candidates, guards evaluated, under 10 µs.
const benchListing = `{"name": "Hub", "variables": {"amount": {"type": "number"}, "qty": {"type": "number"}, "note": {"type": "text"}, "owner": {"type": "text"}, "due": {"type": "date"}},
 "states": {"initial": "hub", "list": ["hub", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j"]},
 "transitions": [
   {"name": "t1",  "from": "hub", "to": "a", "guard": "amount > 0"},
   {"name": "t2",  "from": "hub", "to": "b", "guard": "amount > 1000"},
   {"name": "t3",  "from": "hub", "to": "c", "guard": "INGROUP(\"finance\")"},
   {"name": "t4",  "from": "hub", "to": "d", "guard": "owner = USER()"},
   {"name": "t5",  "from": "hub", "to": "e", "guard": "qty > 0 AND amount / qty > 10"},
   {"name": "t6",  "from": "hub", "to": "f", "guard": "LEN(note) > 3"},
   {"name": "t7",  "from": "hub", "to": "g", "guard": "TODAY() > due"},
   {"name": "t8",  "from": "hub", "to": "h", "guard": "NOT ISBLANK(owner)"},
   {"name": "t9",  "from": "hub", "to": "i", "guard": "ROUND(amount * 1.23, 2) > 100"},
   {"name": "t10", "from": "hub", "to": "j", "guard": "IF(qty > 5, amount > 50, amount > 500)"}
 ]}`

var benchHubValues = Values{"amount": 500, "qty": 5, "note": "a note", "owner": "alice", "due": "2026-01-01"}

// BenchmarkTransitions10 is the listing of ten candidates with their
// guards evaluated, the budget of ADR-0040 being 10 µs.
func BenchmarkTransitions10(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchListing), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Hub", benchHubValues, As(alice))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		infos, err := s.Transitions(ctx, "Hub", doc.ID, As(alice))
		if err != nil || len(infos) != 10 {
			b.Fatalf("%v, %d transitions", err, len(infos))
		}
	}
}

// benchRules is what the rules of a commit run with over a row, outside a
// preview.
func benchRules(c *class, row *docRow) *ruleRun {
	evalCtx := &formula.Context{User: "alice", Groups: []string{"sales"}, Now: start.Unix(), ID: row.id, State: row.state}
	return &ruleRun{c: c, res: &result{}, evalCtx: evalCtx}
}

// benchRuled is the flow of the transition benchmarks with the rules of
// ADR-0043's budget on one transition: three rules fire, one of them with
// a set that makes an auto variable recalculate, one with a condition
// that is FALSE, one that emits; and ten more rules on triggers the
// transition does not raise.
const benchRuled = `{
 "name": "Ruled",
 "variables": {
   "amount":   {"type": "number"},
   "qty":      {"type": "number"},
   "note":     {"type": "text"},
   "owner":    {"type": "text"},
   "seen_at":  {"type": "datetime"},
   "big":      {"formula": "amount > 1000"},
   "total":    {"formula": "amount * qty"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "closed"]},
 "transitions": [
   {"name": "submit",  "from": "draft",  "to": "review", "guard": "amount > 0"},
   {"name": "back",    "from": "review", "to": "draft"},
   {"name": "approve", "from": "review", "to": "approved"},
   {"name": "close",   "from": "approved", "to": "closed"}
 ],
 "rules": [
   {"on": "exit", "state": "draft", "do": [{"action": "set", "values": {"owner": "USER()", "seen_at": "NOW()", "qty": "qty + 1"}}]},
   {"on": "transition", "name": "submit", "if": "big", "do": [{"action": "set", "values": {"note": "\"big\""}}]},
   {"on": "enter", "state": "review", "if": "total > 0", "do": [{"action": "emit", "event": "ruled.in_review"}]},
   {"on": "enter", "state": "approved", "do": [{"action": "emit", "event": "r1"}]},
   {"on": "enter", "state": "closed", "do": [{"action": "emit", "event": "r2"}]},
   {"on": "exit", "state": "approved", "do": [{"action": "emit", "event": "r3"}]},
   {"on": "exit", "state": "closed", "do": [{"action": "emit", "event": "r4"}]},
   {"on": "transition", "name": "approve", "do": [{"action": "emit", "event": "r5"}]},
   {"on": "transition", "name": "close", "do": [{"action": "emit", "event": "r6"}]},
   {"on": "transition", "name": "back", "if": "big", "do": [{"action": "emit", "event": "r7"}]},
   {"on": "create", "do": [{"action": "emit", "event": "r8"}]},
   {"on": "delete", "do": [{"action": "emit", "event": "r9"}]},
   {"on": "enter", "state": "draft", "if": "big", "do": [{"action": "emit", "event": "r10"}]}
 ]}`

// BenchmarkRules measures the rules of one commit alone, as
// BenchmarkRecalculate measures its formulas (ADR-0043): the triggers of
// a transition out of draft, of which three rules fire, a set of three
// values with the recalculation after it, a condition that is FALSE and
// an emit. It must not allocate.
func BenchmarkRules(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(benchRuled), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Ruled", Values{"amount": 500, "qty": 5, "note": "n"})
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Ruled")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	run := benchRules(c, row)
	qty := c.byName["QTY"].leaf
	five := row.vals[qty]
	row.state = "review"
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.emits = s.emits[:0]
		row.vals[qty] = five
		if err := s.fireCommit(run, row, commitTransition, "draft", "submit", nil); err != nil {
			b.Fatal(err)
		}
	}
	if len(s.emits) != 1 {
		b.Fatalf("%d events emitted per commit, want 1", len(s.emits))
	}
}

// BenchmarkTransitionRuled is a transition commit end to end on the class
// of BenchmarkRules: submit fires three rules and writes a custom event,
// back fires one whose condition is FALSE.
func BenchmarkTransitionRuled(b *testing.B) {
	benchTransitions(b, benchRuled, "Ruled")
}

// idleRules are ten rules on triggers the submit and back of the
// transition benchmark never raise.
const idleRules = `, "rules": [
   {"on": "enter", "state": "approved", "do": [{"action": "emit", "event": "r1"}]},
   {"on": "enter", "state": "booked", "if": "big", "do": [{"action": "set", "values": {"note": "\"booked\""}}]},
   {"on": "enter", "state": "closed", "do": [{"action": "emit", "event": "r3"}]},
   {"on": "exit", "state": "approved", "do": [{"action": "emit", "event": "r4"}]},
   {"on": "exit", "state": "closed", "do": [{"action": "emit", "event": "r5"}]},
   {"on": "transition", "name": "approve", "do": [{"action": "set", "values": {"note": "\"approved\""}}]},
   {"on": "transition", "name": "close", "do": [{"action": "emit", "event": "r7"}]},
   {"on": "transition", "name": "reopen", "do": [{"action": "reset", "variables": ["note"]}]},
   {"on": "create", "do": [{"action": "emit", "event": "r9"}]},
   {"on": "delete", "do": [{"action": "emit", "event": "r10"}]}
 ]}`

// BenchmarkTransitionIdleRules is BenchmarkTransition on the same class
// with ten rules none of whose triggers the commit raises: the budget of
// ADR-0043 is that they cost the commit nothing, no evaluation and no
// allocation, so the two benchmarks read the same.
func BenchmarkTransitionIdleRules(b *testing.B) {
	benchTransitions(b, strings.TrimSuffix(strings.TrimSpace(benchFlow), "}")+idleRules, "Flow")
}

// BenchmarkTransitionTimers is BenchmarkTransition on the same class
// with two timed transitions out of each of the two states the document
// moves between, an after and an at over a date it computes, none of
// which has come (ADR-0047): what a commit that enters a state pays for
// its timers. It asks each whether its time has come, one addition for
// an after and one evaluation of its formula for an at, and queues a
// wake-up for each, two rows of the outbox in the commit's transaction.
func BenchmarkTransitionTimers(b *testing.B) {
	timers := `,
   {"from": ["draft", "review"], "to": "closed", "after": "30d"},
   {"name": "lapse", "from": ["draft", "review"], "to": "closed", "at": "ADDDAYS(CREATEDAT(), 90 + qty)"}
 ]}`
	benchTransitions(b, strings.TrimSuffix(strings.TrimSpace(benchFlow), "]}")+timers, "Flow")
}

// benchTransitions takes submit and back in turn on a document of the
// class.
func benchTransitions(b *testing.B, definition, class string) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(definition), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, class, Values{"amount": 500, "qty": 5, "note": "n"})
	if err != nil {
		b.Fatal(err)
	}
	names := [2]string{"submit", "back"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := s.Transition(ctx, class, doc.ID, names[i%2], nil, As(alice)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTriggered measures the triggered variables of one commit
// alone (ADR-0043): a transition that raises the triggers of two, one of
// which an auto variable reads and so recalculates. It must not allocate.
func BenchmarkTriggered(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{
 "name": "Locked",
 "variables": {
   "amount": {"type": "number"},
   "rate":   {"type": "number"},
   "locked": {"formula": "rate", "mode": "triggered", "on": ["transition:send", "refresh"]},
   "price":  {"formula": "amount * locked"},
   "by":     {"formula": "USER()", "mode": "triggered", "on": ["enter:sent"]}
 },
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent"}]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Locked", Values{"amount": 200, "rate": 0.5})
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Locked")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	evalCtx := &formula.Context{User: "alice", Now: start.Unix(), ID: row.id}
	var errs []*Error
	row.state = "sent"
	rate := c.byName["RATE"].leaf
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		row.vals[rate] = formula.Number(float64(i&1) + 0.5) // the value changes every time
		if err := s.recomputeFor(c, row, commitTransition, "draft", "send", evalCtx, start.Unix(), false, &errs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOutbox measures an action of the outbox end to end
// (ADR-0044): a transition whose rule queues a create, which is the
// insert of the row in the commit's transaction, and the drain that runs
// it, which is the created document's commit, the mark of the row and
// the event of the outcome in one transaction.
func BenchmarkOutbox(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Entry", "variables": {"job": {"type": "text", "required": true}, "amount": {"type": "number"}}, "states": {"initial": "booked", "list": ["booked"]}}`,
		`{"name": "Job", "variables": {"amount": {"type": "number"}},
 "states": {"initial": "open", "list": ["open"]}, "transitions": [{"name": "book", "from": "open", "to": "open"}],
 "rules": [{"on": "transition", "name": "book", "do": [{"action": "create", "class": "Entry", "values": {"job": "ID()", "amount": "amount"}}]}]}`,
	} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			b.Fatal(err)
		}
	}
	doc, err := s.Create(ctx, "Job", Values{"amount": 500})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Transition(ctx, "Job", doc.ID, "book", nil, As(alice)); err != nil {
			b.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
}

// BenchmarkTimer measures a timer end to end (ADR-0047, items 10 and
// 11): the wake-up that is due, the engine's touch of the document, the
// transition that fires as a version with its event, and the wake-up of
// the stay that begins, queued in the same transaction. Every turn the
// clock moves a minute and one reminder is made. The budget is the
// transition's: a commit that queues a wake-up stays inside 2 ms.
func BenchmarkTimer(b *testing.B) {
	var mins atomic.Int64
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true},
		Now: func() time.Time { return start.Add(time.Duration(mins.Load()) * time.Minute) }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Job", "variables": {"amount": {"type": "number"}},
 "states": {"initial": "idle", "list": ["idle"]}, "transitions": [{"name": "run", "from": "idle", "to": "idle", "after": "1m"}]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Create(ctx, "Job", Values{"amount": 500}, As(alice)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		mins.Add(1)
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
}

// BenchmarkTimerIdle measures a wake-up that finds nothing to do
// (ADR-0047, item 11): its document left the state before the time
// came. The budget is one read and one update, and no event. The moves
// that queue the wake-up and leave the state are outside the measure.
func BenchmarkTimerIdle(b *testing.B) {
	var mins atomic.Int64
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true},
		Now: func() time.Time { return start.Add(time.Duration(mins.Load()) * time.Minute) }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Job", "variables": {"amount": {"type": "number"}},
 "states": {"initial": "busy", "list": ["busy", "idle"]},
 "transitions": [{"name": "rest", "from": "busy", "to": "idle"}, {"name": "work", "from": "idle", "to": "busy"}, {"from": "idle", "to": "busy", "after": "1m"}]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Job", Values{"amount": 500}, As(alice))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		for _, name := range [2]string{"rest", "work"} {
			if _, err := s.Transition(ctx, "Job", doc.ID, name, nil, As(alice)); err != nil {
				b.Fatal(err)
			}
		}
		mins.Add(1)
		b.StartTimer()
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
}

// BenchmarkOutboxEach10 measures a create with each over ten elements
// (ADR-0044, item 12): a transition whose rule queues ten children, one
// row each, and the drain that makes them. It is measured and published,
// not budgeted, so it is outside the gate.
func BenchmarkOutboxEach10(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Entry", "variables": {"job": {"type": "text", "required": true}, "who": {"type": "text"}}, "states": {"initial": "booked", "list": ["booked"]}}`,
		`{"name": "Job", "variables": {"crew": {"type": "array<text>"}},
 "states": {"initial": "open", "list": ["open"]}, "transitions": [{"name": "book", "from": "open", "to": "open"}],
 "rules": [{"on": "transition", "name": "book", "do": [{"action": "create", "class": "Entry", "each": "crew", "as": "member", "values": {"job": "ID()", "who": "member"}}]}]}`,
	} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			b.Fatal(err)
		}
	}
	crew := make([]any, 10)
	for i := range crew {
		crew[i] = "member-" + itoa(i)
	}
	doc, err := s.Create(ctx, "Job", Values{"crew": crew})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Transition(ctx, "Job", doc.ID, "book", nil, As(alice)); err != nil {
			b.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 10 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
}

// BenchmarkOutboxRegistered measures an action a Go function implements,
// end to end (ADR-0045): a transition whose rule queues the call, which
// is the insert of the row with its arguments, and the drain that makes
// it, which reads the event and the document at that version, calls a
// function that does nothing, and marks the row with the event of the
// outcome. ADR-0044 asks 2,000 such actions a second of the drain; the
// campaign of the release measures that on disk. It is outside the gate.
func BenchmarkOutboxRegistered(b *testing.B) {
	calls := 0
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true},
		Actions: map[string]ActionFunc{"books.post": func(context.Context, *ActionCall) (Values, error) { calls++; return nil, nil }}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "books.post", "params": {"job": {"type": "text", "required": true}, "amount": "number"}}`), asSetup); err != nil {
		b.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Job", "variables": {"amount": {"type": "number"}},
 "states": {"initial": "open", "list": ["open"]}, "transitions": [{"name": "book", "from": "open", "to": "open"}],
 "rules": [{"on": "transition", "name": "book", "do": [{"action": "books.post", "args": {"job": "ID()", "amount": "amount"}}]}]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Job", Values{"amount": 500})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Transition(ctx, "Job", doc.ID, "book", nil, As(alice)); err != nil {
			b.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
	if calls != b.N {
		b.Fatalf("%d calls for %d transitions", calls, b.N)
	}
}

// BenchmarkOutboxDeclared measures what an action sent as a request costs
// the engine beside the network (ADR-0045, Performance): a transition
// whose rule queues the call, and the drain that reads the secret, builds
// the request from the definition's templates, hands it to a sender that
// answers at once, reads the JSON reply with ok and result, and marks the
// row. BenchmarkDeclaredAction, in fado/outbound, adds a real round trip
// to a receiver in the same process. It is outside the gate.
func BenchmarkOutboxDeclared(b *testing.B) {
	net := &fakeOutbound{connections: map[string][]string{"slack": {"slack.com"}}}
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true}, Outbound: net,
		Secrets: func(context.Context, string, string) (string, error) { return "xoxb-bench", nil }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(slackPost), asSetup); err != nil {
		b.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Job", "variables": {"note": {"type": "text"}},
 "states": {"initial": "open", "list": ["open"]}, "transitions": [{"name": "tell", "from": "open", "to": "open"}],
 "rules": [{"on": "transition", "name": "tell", "do": [{"action": "slack.post", "args": {"channel": "\"#ops\"", "text": "note"}}]}]}`), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Job", Values{"note": "done"})
	if err != nil {
		b.Fatal(err)
	}
	reply := &fakeReply{Body: []byte(`{"ok": true, "ts": "1700000000.000100"}`)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		reply.used = false
		net.replies, net.sent = []*fakeReply{reply}, net.sent[:0]
		if _, err := s.Transition(ctx, "Job", doc.ID, "tell", nil, As(alice)); err != nil {
			b.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
	b.StopTimer()
	if dead, err := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup); err != nil || len(dead) != 0 {
		b.Fatalf("%d actions died: %v", len(dead), err)
	}
}

// benchLife is a class with a formula that reads what the life says of
// a state, and a loop of two states to walk. %s is the function the
// formula calls.
const benchLife = `{"name": "Case",
 "variables": {"note": {"type": "text"}, "rounds": {"type": "text", "formula": "TEXT(%s(\"review\"))", "mode": "triggered", "on": ["enter:review"]}},
 "states": {"initial": "draft", "list": ["draft", "review"]},
 "transitions": [{"name": "send", "from": "draft", "to": "review"}, {"name": "back", "from": "review", "to": "draft"}]}`

// BenchmarkLifeStates measures what the functions of a state cost a
// command (ADR-0047, item 6): the one read of the events of a document
// with a hundred versions, a move in every fourth, folded into what the
// life says of each state. The budget is the cold lookup's, 100 µs
// (ADR-0016). A class that asks for no editor reads the moves alone.
func BenchmarkLifeStates(b *testing.B) { benchLifeStates(b, "ENTRIES") }

// BenchmarkLifeEditors is the same read for a class that asks who
// changed the document in a state, which takes every change from the log.
func BenchmarkLifeEditors(b *testing.B) { benchLifeStates(b, "LASTEDITOR") }

func benchLifeStates(b *testing.B, function string) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(benchLife, function)), asSetup); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Case", Values{"note": "0"}, As(ann))
	if err != nil {
		b.Fatal(err)
	}
	// Another document's events lie between this one's in the log.
	other, err := s.Create(ctx, "Case", Values{"note": "0"}, As(bob))
	if err != nil {
		b.Fatal(err)
	}
	for v := 2; v <= 100; v++ {
		switch {
		case v%8 == 4:
			_, err = s.Transition(ctx, "Case", doc.ID, "send", nil, As(ann))
		case v%8 == 0:
			_, err = s.Transition(ctx, "Case", doc.ID, "back", nil, As(bob))
		default:
			_, err = s.Set(ctx, "Case", doc.ID, Values{"note": fmt.Sprint(v)}, As(bob))
		}
		if err == nil {
			_, err = s.Set(ctx, "Case", other.ID, Values{"note": fmt.Sprint(v)}, As(ann))
		}
		if err != nil {
			b.Fatal(err)
		}
	}
	c, _ := s.class("Case")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	if row.version != 100 {
		b.Fatalf("the document is at version %d, want 100", row.version)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		row.told = false
		if err := s.tellLife(c, row); err != nil {
			b.Fatal(err)
		}
	}
	if st := row.life.States; len(st) != 2 || st[0].Entries != 13 || (st[0].LastEditor == "bob") != c.editors {
		b.Fatalf("the life told: %+v", st)
	}
}

// BenchmarkDue is the budget of ADR-0047 for the list of what is due:
// over FADODB_BENCH_DOCS documents (100,000 by default) of which 100 are
// due, a guard on the clock that compares an indexed date, the budget
// being the indexed filter's of ADR-0012; for contrast the same guard
// over a date with no index, which reads every document of the state;
// and a guard that calls a function of a state, which is evaluated
// document by document, over a hundredth of the documents; and a
// backlog, every document due. Then the timed transitions: an at over an
// indexed date, which the same index serves, and an after, which
// compares the moment of entry and reads every document of the state.
// It writes to disk and runs only with FADODB_BENCH_DIR, like
// BenchmarkFilter.
func BenchmarkDue(b *testing.B) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		b.Skip("set FADODB_BENCH_DIR to a directory on disk to run the benchmark of what is due")
	}
	n := 100000
	if v := os.Getenv("FADODB_BENCH_DOCS"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	path := filepath.Join(dir, fmt.Sprintf("due-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	day := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	// The clock stands at day, but for the documents created early.
	var early atomic.Int64
	s, err := Open(path, Options{Now: func() time.Time { return day.AddDate(0, 0, -int(early.Load())) }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	const invoice = `{"name": "%s", "variables": {"due": {"type": "date"%s}, "amount": {"type": "number"}},
 "states": {"initial": "sent", "list": ["sent", "overdue"]},
 "transitions": [{"from": "sent", "to": "overdue", %s}]}`
	classes := []struct {
		name, index, condition string
		docs                   int
		// early is how many days before the clock a hundred documents
		// are created, for a class whose timer counts from the entry.
		early int
	}{
		{"Indexed", `, "index": true`, `"guard": "TODAY() > due"`, n, 0},
		{"Plain", "", `"guard": "TODAY() > due"`, n, 0},
		{"Aged", "", `"guard": "DAYSIN(\"sent\") >= 3 AND TODAY() > due"`, n / 100, 0},
		{"Timed", `, "index": true`, `"at": "due"`, n, 0},
		{"After", "", `"after": "30d"`, n, 28},
	}
	start, made := time.Now(), 0
	for _, c := range classes {
		if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(invoice, c.name, c.index, c.condition)), asSetup); err != nil {
			b.Fatal(err)
		}
		// Of every class a hundred documents pass their date in two days,
		// or their thirty days in the state; the others have a month and
		// more.
		every := max(c.docs/100, 1)
		batch := make([]Values, 0, 1000)
		create := func() {
			if len(batch) > 0 {
				if _, err := s.CreateMany(ctx, c.name, batch); err != nil {
					b.Fatal(err)
				}
				made += len(batch)
				batch = batch[:0]
			}
		}
		for i := 0; i < c.docs; i++ {
			due := day.AddDate(0, 0, 30+i%300)
			if i%every == 0 && c.early == 0 {
				due = day.AddDate(0, 0, 1)
			}
			if c.early > 0 && i == 100 {
				// The first hundred were created early; the rest are of today.
				create()
				early.Store(0)
			} else if c.early > 0 && i == 0 {
				early.Store(int64(c.early))
			}
			if batch = append(batch, Values{"due": due.Format(time.DateOnly), "amount": i}); len(batch) == cap(batch) {
				create()
			}
		}
		create()
		early.Store(0)
	}
	b.Logf("%d documents of %d classes created in %s", made, len(classes), time.Since(start).Round(time.Millisecond))
	soon, late := At(day.AddDate(0, 0, 4)), At(day.AddDate(0, 0, 400))
	for _, bc := range []struct {
		name, class string
		at          Option
		want        int
	}{
		{"indexed date, 100 due", "Indexed", soon, 100},
		{"plain date, 100 due", "Plain", soon, 100},
		{"document by document, 100 due", "Aged", soon, 100},
		// A backlog: every document is due, and the call lists its bound.
		{"indexed date, all due", "Indexed", late, min(n, MaxDue)},
		{"timer at an indexed date, 100 due", "Timed", soon, 100},
		{"timer after a time in the state, 100 due", "After", soon, 100},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				due, err := s.Due(ctx, bc.class, bc.at)
				if err != nil || len(due) != bc.want {
					b.Fatalf("%v, %d documents", err, len(due))
				}
			}
		})
	}
}
