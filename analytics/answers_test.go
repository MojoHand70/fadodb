package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The questions a class already asks are answered by the copy when no
// index narrows them (ADR-0089, item 6): a count, and the total or the
// first row a LOOKUP or a figure over a list works out. Nothing is
// written differently for it, and the answer is the store's: the same
// value for the same reader, never older than the question.

// answersBooks holds, on one document, the questions of the test: each
// a live variable, read for whoever asks.
const answersBooks = `{
  "name": "Books",
  "title": "name",
  "variables": {
    "name":     {"type": "text", "required": true},
    "limit":    {"type": "money", "scale": 2},
    "customer": {"type": "text"},
    "total":        {"mode": "live", "formula": "LOOKUP(Invoice, SUM(net), TRUE)"},
    "urgent":       {"mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), urgent)"},
    "over":         {"mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), net > THIS.limit)"},
    "mean":         {"mode": "live", "formula": "LOOKUP(Invoice, AVERAGE(net), NOT urgent)"},
    "middle":       {"mode": "live", "formula": "LOOKUP(Invoice, MEDIAN(net), net > THIS.limit)"},
    "customers":    {"mode": "live", "formula": "LOOKUP(Invoice, COUNTUNIQUE(customer), TRUE)"},
    "largest":      {"mode": "live", "formula": "LOOKUP(Invoice, MAX(net), due >= DATE(2026, 1, 1))"},
    "first_over":   {"mode": "live", "formula": "LOOKUP(Invoice, number, net > THIS.limit)"},
    "of_customer":  {"mode": "live", "formula": "LOOKUP(Invoice, SUM(net), customer = THIS.customer)"},
    "in_review":    {"mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), state = \"review\")"},
    "urgent_ones":  {"mode": "live", "formula": "QUERY(Invoice, {number, net}, urgent, {number})"},
    "urgent_total": {"mode": "live", "formula": "SUM(urgent_ones.net)"},
    "urgent_count": {"mode": "live", "formula": "COUNT(urgent_ones)"},
    "urgent_names": {"mode": "live", "formula": "JOIN(urgent_ones.number, \", \")"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

// keyed are the questions of answersBooks that an index narrows: the
// sum of one customer, whose index the engine makes for the lookup
// keyed by it, and the count of one state. They are the store's.
var keyed = map[string]bool{"of_customer": true, "in_review": true}

// figures are the questions of answersBooks that are one value.
var figures = []string{"total", "urgent", "over", "mean", "middle", "customers", "largest", "first_over", "of_customer", "in_review", "urgent_total", "urgent_count", "urgent_names"}

func TestAnswers_FromTheCopyAreTheStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(time.Minute); return now }
	withCopy, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer withCopy.Close()
	if _, err := withCopy.DefineClasses(ctx, [][]byte{[]byte(flowInvoice), []byte(answersBooks)}, olga); err != nil {
		t.Fatal(err)
	}
	ewa, jan, dan, nobody := person("ewa", "finance"), person("jan"), person("dan", "duty"), person("zed")
	root := []fado.Option{fado.As(fado.Principal{ID: "root", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}}), fado.Reason("audit of March")}
	var ids []string
	for i := 0; i < 40; i++ {
		d, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": fmt.Sprintf("FV-%02d", i), "owner": []string{"jan", "ola", "ewa"}[i%3],
			"customer": []string{"Globex", "Initech", "Hooli", "Umbrella"}[i%4], "net": fmt.Sprintf("%d.50 PLN", (i+1)*10),
			"urgent": i%5 == 0, "due": []string{"2025-12-31", "2026-06-30"}[i%2]}, ewa)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	if _, err := withCopy.TransitionMany(ctx, "Invoice", "file", ids[:15], nil, ewa); err != nil {
		t.Fatal(err)
	}
	for id, values := range map[string]fado.Values{
		"acme": {"name": "Acme", "limit": "200.00 PLN", "customer": "Globex"},
		"none": {"name": "Nothing is over it", "limit": "999999.00 PLN", "customer": "Nobody"},
	} {
		if _, err := withCopy.Create(ctx, "Books", values, ewa, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := withCopy.FeedCopy(ctx); err != nil {
		t.Fatal(err)
	}
	// A second opening of the same database, with no engine: the store
	// answers there.
	plain, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()

	// said is an answer or its refusal, as one text.
	said := func(v any, err error) string {
		var refusal *fado.Error
		if errors.As(err, &refusal) {
			return "refused: " + refusal.Code
		}
		if err != nil {
			return "failed: " + err.Error()
		}
		text, _ := json.Marshal(v)
		return string(text)
	}
	live := func(db *fado.DB, books, variable string, who ...fado.Option) string {
		v, err := db.Live(ctx, "Books", books, variable, who...)
		if err != nil {
			return said(nil, err)
		}
		return said(v.Value, nil)
	}
	counts := map[string]string{
		"every invoice":                  "",
		"the invoices over an amount":    `net > 150`,
		"the urgent ones due this year":  `urgent AND due >= DATE(2026, 1, 1)`,
		"the invoices of one customer":   `customer = "Hooli"`,
		"the invoices in review":         `state = "review"`,
		"the invoices in review, urgent": `state = "review" AND urgent`,
	}
	// led are the counts whose condition compares an indexed column:
	// the state, and the customer, on which the engine keeps an index
	// since the books look invoices up by it.
	led := map[string]bool{"the invoices in review": true, "the invoices in review, urgent": true, "the invoices of one customer": true}
	same := func(when string) {
		t.Helper()
		for name, who := range map[string][]fado.Option{"ewa, who reads all": {ewa}, "jan": {jan}, "dan, on duty": {dan}, "somebody who reads none": {nobody}, "a content administrator with a reason": root} {
			// The rule of ewa and of the administrator names no column, so
			// nothing narrows what they ask but the question itself.
			readsAll := name == "ewa, who reads all" || name == "a content administrator with a reason"
			for _, books := range []string{"acme", "none"} {
				for _, variable := range figures {
					before := withCopy.CopyState().Answers
					got := live(withCopy, books, variable, who...)
					asked := withCopy.CopyState().Answers - before
					if want := live(plain, books, variable, who...); got != want {
						t.Errorf("%s, %s reads %s of %s: the copy's program says %s, the store's %s", when, name, variable, books, got, want)
					}
					// What no index narrows is the copy's at once. What an
					// index narrows is the store's for a moment and the
					// copy's after it, so only its answer is held here.
					if readsAll && !keyed[variable] && asked != 1 {
						t.Errorf("%s, %s reads %s of %s: the copy answered %d times, want once (%+v)", when, name, variable, books, asked, withCopy.CopyState())
					}
				}
			}
			for label, where := range counts {
				before := withCopy.CopyState().Answers
				n, err := withCopy.Count(ctx, fado.Filter{Class: "Invoice", Where: where}, who...)
				got, asked := said(n, err), withCopy.CopyState().Answers-before
				m, err := plain.Count(ctx, fado.Filter{Class: "Invoice", Where: where}, who...)
				if want := said(m, err); got != want {
					t.Errorf("%s, %s counts %s: the copy's program says %s, the store's %s", when, name, label, got, want)
				}
				if readsAll && !led[label] && asked != 1 {
					t.Errorf("%s, %s counts %s: the copy answered %d times, want once (%+v)", when, name, label, asked, withCopy.CopyState())
				}
			}
		}
	}
	same("after the load")
	// What the answers are, so that two wrong ones that agree are not
	// taken for right.
	for variable, want := range map[string]string{
		"total":        `"8220.00 PLN"`,
		"urgent":       `8`,
		"over":         `21`,
		"customers":    `4`,
		"first_over":   `"FV-19"`,
		"urgent_count": `8`,
		"urgent_names": `"FV-00, FV-05, FV-10, FV-15, FV-20, FV-25, FV-30, FV-35"`,
	} {
		if got := live(withCopy, "acme", variable, ewa); got != want {
			t.Errorf("ewa reads %s: %s; want %s", variable, got, want)
		}
	}
	if got := live(withCopy, "none", "middle", ewa); got != "null" {
		t.Errorf("the middle of no invoice: %s; want a blank", got)
	}

	// An answer is never older than its question: an invoice made a
	// moment ago is in the next total, and one sent on is counted where
	// it is.
	if _, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": "FV-40", "owner": "jan", "customer": "Globex", "net": "1000.00 PLN", "urgent": true, "due": "2026-06-30"}, ewa); err != nil {
		t.Fatal(err)
	}
	if got := live(withCopy, "acme", "total", ewa); got != `"9220.00 PLN"` {
		t.Errorf("the total right after one more invoice: %s; want 9220.00 PLN", got)
	}
	if _, err := withCopy.TransitionMany(ctx, "Invoice", "file", ids[15:20], nil, ewa); err != nil {
		t.Fatal(err)
	}
	same("after one more invoice and five moves")

	// What stops a LOOKUP stops it in the copy too: money in two
	// currencies does not add.
	if _, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": "FV-41", "owner": "ola", "customer": "Hooli", "net": "10.00 EUR", "urgent": false, "due": "2026-06-30"}, ewa); err != nil {
		t.Fatal(err)
	}
	if got := live(withCopy, "acme", "total", ewa); got != "refused: currency_mismatch" {
		t.Errorf("a sum of zloty and euro from the copy: %s", got)
	}
	same("after an invoice in euro")
}
