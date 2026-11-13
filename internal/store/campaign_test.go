package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCommitCampaign is the commit benchmark the records ask for: the
// commit with its compute at p99 against the 2 ms budget (ADR-0014,
// ADR-0035), keyed beside unkeyed commits (ADR-0026), a preview beside the
// commit it precedes, at the clock and at a chosen moment (ADR-0025,
// ADR-0042), batched creates against the 10,000 per second budget
// (ADR-0012), commits whose formulas look a table up (ADR-0036), the
// statechart rows of ADR-0040: a transition, a transition with values, a
// chain of three state changes in one command and the listing of ten
// candidates; the batch of moves of ADR-0048 against its 15,000 documents
// per second, and single commits made while batches run back to back;
// and the time to open the database afterwards. It writes to
// disk and runs only with FADODB_BENCH_DIR set to a directory on a real
// disk; FADODB_BENCH_COMMITS sizes it, 100,000 by default, and
// FADODB_BENCH_WAL_MB sets the log size at which the checkpointer
// truncates, 64 by default, so that the policy of ADR-0035 item 7 can be
// measured with the real commit. It prints a Markdown table with -v.
func TestCommitCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the commit campaign")
	}
	n := 100000
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_COMMITS")); err == nil && v > 0 {
		n = v
	}
	path := filepath.Join(dir, fmt.Sprintf("campaign-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	var opts Options
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_WAL_MB")); err == nil && v > 0 {
		opts.WALLimit = int64(v) << 20
	}
	s, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(invoice), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{ratesTable, pricesTable} {
		if _, err := s.DefineTable(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	rates := make([]Values, 1000)
	for i := range rates {
		rates[i] = Values{"code": fmt.Sprintf("C%03d", i), "rate": fmt.Sprintf("%d.%06d", i%9+1, i), "fee": fmt.Sprintf("%d.00 PLN", i%5), "name": "Currency " + fmt.Sprint(i)}
	}
	if _, err := s.ImportRows(ctx, "Rates", rates); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Prices", priceRows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(order), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{benchFlow, benchChain, benchListing} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	var table []string
	report := func(work string, commits int, lat []time.Duration, elapsed time.Duration) {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		pct := func(p float64) time.Duration { return lat[min(len(lat)-1, int(float64(len(lat))*p))] }
		table = append(table, fmt.Sprintf("| %s | %s | %s/s | %s | %s | %s | %s |", work, thousands(commits), thousands(int(float64(commits)/elapsed.Seconds())),
			ms(pct(0.5)), ms(pct(0.9)), ms(pct(0.99)), ms(lat[len(lat)-1])))
	}
	values := func(i int) Values {
		return Values{
			"number": fmt.Sprintf("INV-%07d", i), "customer": fmt.Sprintf("Customer:%d", i%5000), "net": fmt.Sprintf("%d.00 PLN", 10+i%990),
			"priority": float64(i % 5), "parts": float64(i%7 + 1), "notes": "Some notes on the invoice", "settings": `{"reminders": true}`,
			"shipping": Values{"line1": "Długa 1", "city": "Gdańsk", "postal_code": "80-831", "country": "PL"},
			"contact":  Values{"id": fmt.Sprintf("anna.%d", i%1000), "name": "Anna Kowalska", "email": "anna@acme.example"},
			"lines":    []any{Values{"sku": "A1", "qty": 2, "price": "9.50"}, Values{"sku": "B2", "qty": 1, "price": "4.00"}, Values{"sku": "C3", "qty": 5, "price": "1.25"}},
			"tags":     []any{"a", "b"},
		}
	}

	// 1. Creates, one transaction each.
	ids := make([]string, 0, n)
	lat := make([]time.Duration, 0, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		doc, err := s.Create(ctx, "Invoice", values(i), As(alice))
		if err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		ids = append(ids, doc.ID)
	}
	report("create, one transaction each", n, lat, time.Since(start))

	// 2. Sets without a key, over a fifth of the documents.
	docs := max(1, n/5)
	set := func(i int) Values {
		return Values{"discount": fmt.Sprintf("0.%02d", i%50), "notes": "version " + fmt.Sprint(i)}
	}
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := s.Set(ctx, "Invoice", ids[i%docs], set(i), As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("set, one transaction each, no key", n, lat, time.Since(start))

	// 3. The same sets with an idempotency key.
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := s.Set(ctx, "Invoice", ids[i%docs], set(i+1), As(alice), Key(fmt.Sprintf("k-%d", i))); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("set, one transaction each, with a key", n, lat, time.Since(start))

	// 4. A preview of the same set.
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := s.Preview(ctx, "Invoice", ids[i%docs], set(i+2), "", As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("preview of that set", n, lat, time.Since(start))

	// 4b. The same preview at a moment the caller chooses (ADR-0042): the
	// difference must be nothing.
	moment := At(time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC))
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := s.Preview(ctx, "Invoice", ids[i%docs], set(i+2), "", As(alice), moment); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("preview of that set at a chosen moment", n, lat, time.Since(start))

	// 5. Batched creates, 1,000 per transaction; the latency is the batch's.
	const batch = 1000
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i += batch {
		m := min(batch, n-i)
		many := make([]Values, m)
		for j := range many {
			many[j] = values(n + i + j)
		}
		t0 := time.Now()
		if _, err := s.CreateMany(ctx, "Invoice", many, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("create, batched 1,000 per transaction", n, lat, time.Since(start))

	// 6. Sets on a class whose formulas look a table up: one indexed
	// lookup, one statement lookup, one lookup in a default.
	orders := make([]Values, docs)
	for i := range orders {
		orders[i] = Values{"currency": fmt.Sprintf("C%03d", i%1000), "net": "100.00", "sku": "A1", "qty": float64(i%20 + 1)}
	}
	for i := 0; i < docs; i += batch {
		if _, err := s.CreateMany(ctx, "Order", orders[i:min(i+batch, docs)]); err != nil {
			t.Fatal(err)
		}
	}
	orderDocs, err := s.Query(ctx, Filter{Class: "Order", Limit: 10000, Fields: []string{"currency"}})
	if err != nil {
		t.Fatal(err)
	}
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		// Every pass over the documents sets another currency, so that
		// every set is a change and recomputes both lookups.
		pass := i / len(orderDocs)
		t0 := time.Now()
		if _, err := s.Set(ctx, "Order", orderDocs[i%len(orderDocs)].ID, Values{"currency": fmt.Sprintf("C%03d", (i+1+pass*331)%1000), "qty": float64((i+pass)%20 + 1)}); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("set with two lookups recomputed", n, lat, time.Since(start))

	// 8. Transitions (ADR-0040): a fifth of the documents as Flow, every
	// command a named transition with a guard, submit then back, so that
	// no eventless transition follows and each commit is one state
	// change; then the same with values.
	flows := make([]string, 0, docs)
	for i := 0; i < docs; i += batch {
		many := make([]Values, min(batch, docs-i))
		for j := range many {
			many[j] = Values{"amount": float64(100 + j%900), "qty": float64(j%9 + 1), "note": "flow"}
		}
		made, err := s.CreateMany(ctx, "Flow", many, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range made {
			flows = append(flows, d.ID)
		}
	}
	inReview := make([]bool, len(flows))
	move := func(k int) string {
		inReview[k] = !inReview[k]
		if inReview[k] {
			return "submit"
		}
		return "back"
	}
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		k := i % len(flows)
		t0 := time.Now()
		if _, err := s.Transition(ctx, "Flow", flows[k], move(k), nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("transition, one transaction each", n, lat, time.Since(start))
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		k := i % len(flows)
		t0 := time.Now()
		if _, err := s.Transition(ctx, "Flow", flows[k], move(k), Values{"note": "moved " + fmt.Sprint(i)}, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("transition with values", n, lat, time.Since(start))

	// 8b. The same transitions in batches of 1,000 (ADR-0048): one
	// transaction a batch, a commit a document; the latency is the
	// batch's. The documents of a batch are in one state, since every
	// pass above went over all of them alike.
	window := func(done, most int) (string, []string, error) {
		lo := done % len(flows)
		hi := min(lo+batch, len(flows), lo+most)
		name := move(lo)
		for k := lo + 1; k < hi; k++ {
			if move(k) != name {
				return "", nil, fmt.Errorf("the documents of a batch are in different states at %d", k)
			}
		}
		return name, flows[lo:hi], nil
	}
	lat = lat[:0]
	start = time.Now()
	for done := 0; done < n; {
		name, many, err := window(done, n-done)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		out, err := s.TransitionMany(ctx, "Flow", name, many, nil, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		for j := range out {
			if !out[j].Moved() {
				t.Fatalf("a document of a batch did not move: %v", out[j].Refusal)
			}
		}
		done += len(many)
	}
	report("transition, batched 1,000 per transaction", n, lat, time.Since(start))

	// 8c. Another writer while batches run back to back: single sets on
	// the invoices, each of which waits its turn behind a batch. The
	// batches go on until the sets are done.
	stop, movedBy := make(chan struct{}), make(chan int)
	go func() {
		moved := 0
		for {
			select {
			case <-stop:
				movedBy <- moved
				return
			default:
			}
			// The batches go on where the row above stopped.
			name, many, err := window(n+moved, batch)
			if err == nil {
				_, err = s.TransitionMany(ctx, "Flow", name, many, nil, As(alice))
			}
			if err != nil {
				t.Error(err)
				movedBy <- moved
				return
			}
			moved += len(many)
		}
	}()
	between := min(n, 1000)
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < between; i++ {
		t0 := time.Now()
		if _, err := s.Set(ctx, "Invoice", ids[i%docs], set(2*n+i), As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	elapsed := time.Since(start)
	close(stop)
	report(fmt.Sprintf("set, one transaction each, while batches moved %s documents", thousands(<-movedBy)), between, lat, elapsed)

	// 9. A chain of three: every command on the Chain class is a named
	// transition and two eventless ones, three versions, three history
	// rows and three events in one transaction.
	chains := make([]string, 0, docs)
	for i := 0; i < docs; i += batch {
		many := make([]Values, min(batch, docs-i))
		for j := range many {
			many[j] = Values{"n": float64(j), "note": "chain"}
		}
		made, err := s.CreateMany(ctx, "Chain", many, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range made {
			chains = append(chains, d.ID)
		}
	}
	atEnd := make([]bool, len(chains))
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		k := i % len(chains)
		name := "go"
		if atEnd[k] {
			name = "back"
		}
		atEnd[k] = !atEnd[k]
		t0 := time.Now()
		if _, err := s.Transition(ctx, "Chain", chains[k], name, nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("transition starting a chain of three", n, lat, time.Since(start))

	// 10. The listing of ten candidates, guards evaluated, writing
	// nothing: the budget is 10 µs.
	hub, err := s.Create(ctx, "Hub", benchHubValues, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		infos, err := s.Transitions(ctx, "Hub", hub.ID, As(alice))
		if err != nil || len(infos) != 10 {
			t.Fatalf("%v, %d transitions", err, len(infos))
		}
		lat = append(lat, time.Since(t0))
	}
	report("listing of ten candidate transitions", n, lat, time.Since(start))

	// 11. Open the database again.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	opened := time.Since(t0)
	count, _ := s2.Count(ctx, Filter{Class: "Invoice"})
	s2.Close()
	size := int64(0)
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(path + suffix); err == nil {
			size += fi.Size()
		}
	}
	wal := opts.WALLimit
	if wal == 0 {
		wal = DefaultWALLimit
	}
	t.Logf("\nWAL truncated past %d MB.\n| Work | Commits | Throughput | p50 | p90 | p99 | max |\n|---|---|---|---|---|---|---|\n%s\n\n%s documents in the Invoice class; open took %s; the file is %.0f MB.",
		wal>>20, strings.Join(table, "\n"), thousands(int(count)), ms(opened), float64(size)/1e6)
}

func ms(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.3f ms", float64(d)/1e6)
	case d < 100*time.Millisecond:
		return fmt.Sprintf("%.2f ms", float64(d)/1e6)
	}
	return fmt.Sprintf("%.0f ms", float64(d)/1e6)
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
