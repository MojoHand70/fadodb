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

// A list of totals by group (ADR-0090) is answered by the copy when no
// index narrows what it counts (ADR-0089, item 6), and the answer is
// the store's: the same lines, in the same order, with the same totals,
// for the same reader.

const totalsBooks = `{
  "name": "Books",
  "title": "name",
  "variables": {
    "name": {"type": "text", "required": true},
    "by_customer": {"mode": "live", "formula": "QUERY(Invoice, {customer, COUNT(id), SUM(net)})"},
    "largest":     {"mode": "live", "formula": "QUERY(Invoice, {customer, SUM(net)}, NOT urgent, {-SUM(net)})"},
    "whole":       {"mode": "live", "formula": "QUERY(Invoice, {COUNT(id), SUM(net), MIN(net), MAX(net), AVERAGE(net), COUNTUNIQUE(customer)})"},
    "by_state":    {"mode": "live", "formula": "QUERY(Invoice, {state, urgent, COUNT(id)})"},
    "by_day":      {"mode": "live", "formula": "QUERY(Invoice, {due, COUNT(id), SUM(net)}, net > 0, {-due})"},
    "in_review":   {"mode": "live", "formula": "QUERY(Invoice, {customer, COUNT(id)}, state = \"review\")"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

func TestTotals_FromTheCopyAreTheStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(time.Minute); return now }
	withCopy, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer withCopy.Close()
	if _, err := withCopy.DefineClasses(ctx, [][]byte{[]byte(flowInvoice), []byte(totalsBooks)}, olga); err != nil {
		t.Fatal(err)
	}
	ewa, jan, dan, nobody := person("ewa", "finance"), person("jan"), person("dan", "duty"), person("zed")
	root := []fado.Option{fado.As(fado.Principal{ID: "root", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}}), fado.Reason("audit of March")}
	var ids []string
	for i := 0; i < 40; i++ {
		// Some invoices have no customer, no amount or no day: a blank
		// is a group of its own, and stands first.
		values := fado.Values{"number": fmt.Sprintf("FV-%02d", i), "owner": []string{"jan", "ola", "ewa"}[i%3], "urgent": i%5 == 0}
		if i%8 != 5 {
			values["customer"] = []string{"Globex", "Initech", "Hooli", "Umbrella"}[i%4]
		}
		if i%7 != 3 {
			values["net"] = fmt.Sprintf("%d.50 PLN", (i%9+1)*10)
		}
		if i%6 != 1 {
			values["due"] = []string{"2025-12-31", "2026-06-30", "2026-02-14"}[i%3]
		}
		d, err := withCopy.Create(ctx, "Invoice", values, ewa)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	if _, err := withCopy.TransitionMany(ctx, "Invoice", "file", ids[:15], nil, ewa); err != nil {
		t.Fatal(err)
	}
	if _, err := withCopy.Create(ctx, "Books", fado.Values{"name": "Acme"}, ewa, fado.WithID("acme")); err != nil {
		t.Fatal(err)
	}
	if err := withCopy.FeedCopy(ctx); err != nil {
		t.Fatal(err)
	}
	plain, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()

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
	// narrowed says an index narrows what the list counts.
	lists := map[string]bool{"by_customer": false, "largest": false, "whole": false, "by_state": false, "by_day": false, "in_review": true}
	same := func(when string) {
		t.Helper()
		for name, who := range map[string][]fado.Option{"ewa, who reads all": {ewa}, "jan": {jan}, "dan, on duty": {dan}, "somebody who reads none": {nobody}, "a content administrator with a reason": root} {
			readsAll := name == "ewa, who reads all" || name == "a content administrator with a reason"
			for list, narrowed := range lists {
				for _, read := range []fado.LiveRead{{}, {Limit: 2}, {Limit: 2, Offset: 2}, {Order: []string{"-count_id"}}} {
					if list == "largest" && len(read.Order) > 0 {
						continue // it has no count to be sorted by
					}
					read.Class, read.ID, read.Variable = "Books", "acme", list
					before := withCopy.CopyState().Answers
					v, err := withCopy.LivePage(ctx, read, who...)
					got, asked := said(v, err), withCopy.CopyState().Answers-before
					v, err = plain.LivePage(ctx, read, who...)
					if want := said(v, err); got != want {
						t.Errorf("%s, %s reads %s with %+v:\nthe copy's program  %s\nthe store's program %s", when, name, list, read, got, want)
					}
					// What no index narrows is the copy's at once. What an
					// index narrows is the store's for a moment and the
					// copy's after it, so only its lines are held here.
					if readsAll && !narrowed && asked != 1 {
						t.Errorf("%s, %s reads %s: the copy answered %d times, want once (%+v)", when, name, list, asked, withCopy.CopyState())
					}
				}
			}
		}
	}
	same("after the load")
	// What the lines are, so that two wrong lists that agree are not
	// taken for right: the invoices with no customer are a line of
	// their own, the first.
	v, err := withCopy.Live(ctx, "Books", "acme", "by_customer", ewa)
	if err != nil || len(v.Rows) != 5 {
		t.Fatalf("by customer: %v, %s", err, said(v, nil))
	}
	if first, _ := json.Marshal(v.Rows[0]); string(first) != `{"customer":null,"count_id":5,"sum_net":"202.50 PLN"}` {
		t.Errorf("the line of the invoices with no customer: %s", first)
	}
	// A list of totals is never older than its question.
	if _, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": "FV-40", "owner": "jan", "customer": "Globex", "net": "1000.00 PLN", "urgent": false}, ewa); err != nil {
		t.Fatal(err)
	}
	same("after one more invoice")
	// Money in two currencies within one group stops the list, in the
	// copy as in the store.
	if _, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": "FV-41", "owner": "ola", "customer": "Hooli", "net": "10.00 EUR", "urgent": false}, ewa); err != nil {
		t.Fatal(err)
	}
	if _, err := withCopy.Live(ctx, "Books", "acme", "by_customer", ewa); said(nil, err) != "refused: currency_mismatch" {
		t.Errorf("a sum of zloty and euro from the copy: %v", err)
	}
	same("after an invoice in euro")
}
