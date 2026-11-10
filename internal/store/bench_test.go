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
  "rate": {"formula": "LOOKUP(Fx, rate, code = THIS.currency)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}, "states": {"initial": "open", "list": ["open"]}}`
const lookupScanClass = `{"name": "Quote", "variables": {"currency": {"type": "text"}, "net": {"type": "decimal(2)"},
  "rate": {"formula": "LOOKUP(Fx, rate, code = THIS.currency AND rate > 0)"}, "net_pln": {"formula": "ROUND(net * rate, 2)"}}, "states": {"initial": "open", "list": ["open"]}}`

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
	// Ticket has no rights; Guarded is the same class with a formula read
	// right on the indexed variable, which the filter compiles into its
	// predicate (ADR-0041), the budget being under 5% on the indexed
	// filter.
	const ticket = `{"name": "%s", "variables": {"number": {"type": "text", "index": true}, "priority": {"type": "number"}, "owner": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}%s}`
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(ticket, "Ticket", ""))); err != nil {
		b.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(ticket, "Guarded", `, "rights": {"*": {"number": {"read": "INGROUP(\"support\") OR owner = USER()"}}}`))); err != nil {
		b.Fatal(err)
	}
	support := As(Principal{ID: "agent", Groups: []string{"support"}})
	start := time.Now()
	for _, class := range []string{"Ticket", "Guarded"} {
		for i := 0; i < n; i++ {
			if _, err := s.Create(ctx, class, Values{"number": fmt.Sprintf("T-%07d", i), "priority": float64(i % 7), "owner": fmt.Sprintf("u%d", i%100)}); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Logf("%d documents of two classes created in %s", 2*n, time.Since(start).Round(time.Millisecond))
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
	if _, err := s.DefineClass(ctx, []byte(benchFlow)); err != nil {
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
	if _, err := s.DefineClass(ctx, []byte(benchFlow)); err != nil {
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
	if _, err := s.DefineClass(ctx, []byte(benchFlow)); err != nil {
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
	if _, err := s.DefineClass(ctx, []byte(benchFlow)); err != nil {
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
	if _, err := s.DefineClass(ctx, []byte(fiftyRights())); err != nil {
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
	if _, err := s.DefineClass(ctx, []byte(benchListing)); err != nil {
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
	if _, err := s.DefineClass(ctx, []byte(benchRuled)); err != nil {
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

// benchTransitions takes submit and back in turn on a document of the
// class.
func benchTransitions(b *testing.B, definition, class string) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(definition)); err != nil {
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
