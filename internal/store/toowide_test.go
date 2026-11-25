package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A free question that no index serves is stopped after the time the
// database allows it, and told what would serve it (ADR-0075, item 6).
// Here the database allows five thousandths of a second, and the slow
// questions do heavy work on each of two thousand invoices, a fifth of
// a second in all, so that they are stopped long before they end.
func openTooWide(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := openStore(t, Options{MaxQuestion: 5 * time.Millisecond})
	ctx := context.Background()
	orders := strings.Replace(ownOrder, `"variables": {"number": {"type": "text", "required": true}}`,
		`"variables": {"number": {"type": "text", "required": true}, "remarked": {"mode": "live", "formula": "QUERY(Invoice, {number}, note = \"late\", {number})"}}`, 1)
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(orders), []byte(ownInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-1"}, As(liveJan), WithID("ORD-1")); err != nil {
		t.Fatal(err)
	}
	batch := make([]Values, 0, 2000)
	for i := 0; i < 2000; i++ {
		customer := "Globex"
		if i >= 4 {
			customer = "Initech"
		}
		batch = append(batch, Values{"number": fmt.Sprintf("FV-%04d", i), "order": "Order:ORD-1", "customer": customer, "note": "late"})
	}
	if _, err := s.CreateMany(ctx, "Invoice", batch, As(liveJan)); err != nil {
		t.Fatal(err)
	}
	return s, path
}

// heavy is a condition that holds for no invoice and costs a tenth of a
// thousandth of a second to ask of each.
const heavy = `LEN(SUBSTITUTE(REPEAT(number, 5000), "F", "GG")) < 0`

func TestTooWide_AQuestionNoIndexServesIsStopped(t *testing.T) {
	s, _ := openTooWide(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, where string
		who         Principal
		variable    string // "" when the question is answered
		fix         string
		found       int
	}{
		{"by a variable that has no index", `note = "late" AND ` + heavy, liveEwa, "note", `note has no index: say "index": true on it in the class Invoice`, 0},
		{"by what is no comparison with a value", heavy, liveEwa, "where", "begin the where with a comparison of a variable that has an index", 0},
		{"by a variable a designer indexed", `customer = "Globex"`, liveEwa, "", "", 4},
		{"by a variable a designer indexed, and heavy work on the four it finds", `customer = "Globex" AND NOT (` + heavy + `)`, liveEwa, "", "", 4},
		{"by the id", `id = "nobody" AND note = "late"`, liveEwa, "", "", 0},
		// No condition and no order: read in the order of the ids, a
		// page at a time, which the key of the table serves.
		{"everything, a page at a time", ``, liveEwa, "", "", -1},
	} {
		docs, err := s.Query(ctx, Filter{Class: "Invoice", Where: tc.where}, As(tc.who))
		n, cerr := s.Count(ctx, Filter{Class: "Invoice", Where: tc.where}, As(tc.who))
		if tc.found < 0 {
			if err != nil || cerr != nil || len(docs) != 1000 || n != 2000 {
				t.Errorf("%s: %d documents, %v; counted %d, %v; want the first thousand of two thousand", tc.name, len(docs), err, n, cerr)
			}
			continue
		}
		if tc.variable == "" {
			if err != nil || cerr != nil || len(docs) != tc.found || n != int64(tc.found) {
				t.Errorf("%s: %d documents, %v; counted %d, %v; want %d", tc.name, len(docs), err, n, cerr, tc.found)
			}
			continue
		}
		for what, err := range map[string]error{"query": err, "count": cerr} {
			e, ok := err.(*Error)
			if !ok || e.Code != CodeTooWide || e.Variable != tc.variable || !strings.Contains(e.Hint, tc.fix) ||
				!strings.Contains(e.Message, "the question was stopped after 0.005 seconds: no index serves it, so it reads the documents of Invoice one by one") {
				t.Errorf("%s, %s: %v; want too_wide about %s with the fix %q", tc.name, what, err, tc.variable, tc.fix)
			}
		}
	}
	// A list of a document is a declared question: it is not stopped,
	// and the class was noted for it when it was defined.
	if v, err := s.LivePage(ctx, LiveRead{Class: "Order", ID: "ORD-1", Variable: "remarked", Limit: 7}, As(liveEwa)); err != nil || len(v.Rows) != 7 {
		t.Errorf("the list of an order whose condition no index serves: %v, %v; want its first seven rows", v, err)
	}
	// A caller who left is told so, and not that the question was wide.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Query(gone, Filter{Class: "Invoice", Where: heavy}, As(liveEwa)); err == nil || code(err) == CodeTooWide {
		t.Errorf("a caller who left: %v; want the failure of the call, and not too_wide", err)
	}
}

// The limit is the database's: it is kept with it, a value given when
// the database is opened replaces it, and a new database allows five
// seconds.
func TestTooWide_TheLimitIsTheDatabases(t *testing.T) {
	s, path := openTooWide(t)
	if got := s.MaxQuestion(); got != 5*time.Millisecond {
		t.Errorf("the database allows %v; want the five thousandths of a second it was made with", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		given, want time.Duration
	}{{0, 5 * time.Millisecond}, {20 * time.Second, 20 * time.Second}, {0, 20 * time.Second}} {
		again, err := Open(path, Options{MaxQuestion: tc.given, Outbox: OutboxOptions{Manual: true}})
		if err != nil {
			t.Fatal(err)
		}
		if got := again.MaxQuestion(); got != tc.want {
			t.Errorf("opened with %v the database allows %v; want %v", tc.given, got, tc.want)
		}
		if tc.want == 20*time.Second {
			// With time enough the slow question is answered: no invoice.
			if docs, err := again.Query(context.Background(), Filter{Class: "Invoice", Where: heavy}, As(liveEwa)); err != nil || len(docs) != 0 {
				t.Errorf("a question no index serves, with time enough: %d documents, %v", len(docs), err)
			}
			// And so is a quick one that no index serves.
			if n, err := again.Count(context.Background(), Filter{Class: "Invoice", Where: `number = "FV-0007"`}, As(liveEwa)); err != nil || n != 1 {
				t.Errorf("a quick question no index serves: %d, %v", n, err)
			}
		}
		if err := again.Close(); err != nil {
			t.Fatal(err)
		}
	}
	fresh, _ := openStore(t, Options{})
	if got := fresh.MaxQuestion(); got != 5*time.Second {
		t.Errorf("a new database allows %v; want five seconds", got)
	}
}
