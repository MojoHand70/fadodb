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

// A page of a list that no index serves is found in the copy (ADR-0089,
// item 6): which documents the question takes, in its order. The
// documents themselves are the store's. The page is the one a program
// without the engine answers: the same documents in the same order,
// with the same values, for the same reader.

const pagesBooks = `{
  "name": "Books",
  "title": "name",
  "variables": {
    "name": {"type": "text", "required": true},
    "urgent_ones": {"mode": "live", "formula": "QUERY(Invoice, {number, net}, urgent, {number})"},
    "by_amount":   {"mode": "live", "formula": "QUERY(Invoice, {number, net, due}, net > 0, {-net, number})"},
    "by_day":      {"mode": "live", "formula": "QUERY(Invoice, {number, due}, NOT urgent, {due, -number})"},
    "in_review":   {"mode": "live", "formula": "QUERY(Invoice, {number, net}, state = \"review\", {number})"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

func TestPages_FromTheCopyAreTheStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(time.Minute); return now }
	withCopy, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer withCopy.Close()
	if _, err := withCopy.DefineClasses(ctx, [][]byte{[]byte(flowInvoice), []byte(pagesBooks)}, olga); err != nil {
		t.Fatal(err)
	}
	ewa, jan, dan, nobody := person("ewa", "finance"), person("jan"), person("dan", "duty"), person("zed")
	root := []fado.Option{fado.As(fado.Principal{ID: "root", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}}), fado.Reason("audit of March")}
	var ids []string
	for i := 0; i < 40; i++ {
		// Some invoices have no amount and some no day: a blank sorts
		// before every value, in the store and in the copy.
		values := fado.Values{"number": fmt.Sprintf("FV-%02d", i), "owner": []string{"jan", "ola", "ewa"}[i%3],
			"customer": []string{"Globex", "Initech", "Hooli", "Umbrella"}[i%4], "urgent": i%5 == 0}
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
	// The pages asked with the command query. sorts says the store
	// would read and sort every document the question takes, since no
	// index serves its condition or holds its order: such a page is
	// found in the copy at once. Any other is the store's for a moment
	// and the copy's after it, so only its answer is held here.
	queries := []struct {
		name  string
		f     fado.Filter
		sorts bool
	}{
		{"the invoices over an amount, the dearest first", fado.Filter{Class: "Invoice", Where: `net > 40`, Order: []string{"-net", "number"}}, true},
		{"every invoice by its day, blanks and all", fado.Filter{Class: "Invoice", Order: []string{"due", "number"}}, true},
		{"every invoice by its day, the latest first", fado.Filter{Class: "Invoice", Order: []string{"-due", "-number"}}, true},
		{"the invoices of one customer", fado.Filter{Class: "Invoice", Where: `customer = "Hooli"`}, false},
		{"a page of them: five after the first three", fado.Filter{Class: "Invoice", Where: `NOT urgent`, Order: []string{"net"}, Limit: 5, Offset: 3}, true},
		{"two fields of the urgent ones", fado.Filter{Class: "Invoice", Where: `urgent`, Fields: []string{"number", "net"}}, false},
		{"what no invoice is", fado.Filter{Class: "Invoice", Where: `net > 100000`}, false},
		{"the invoices in review", fado.Filter{Class: "Invoice", Where: `state = "review"`}, false},
		{"the newest invoices: an index holds the order", fado.Filter{Class: "Invoice", Order: []string{"-created_at"}, Limit: 5}, false},
		{"every invoice, as it comes", fado.Filter{Class: "Invoice"}, false},
	}
	lists := map[string]bool{"urgent_ones": true, "by_amount": true, "by_day": true, "in_review": false}
	for name, who := range map[string][]fado.Option{"ewa, who reads all": {ewa}, "jan": {jan}, "dan, on duty": {dan}, "somebody who reads none": {nobody}, "a content administrator with a reason": root} {
		readsAll := name == "ewa, who reads all" || name == "a content administrator with a reason"
		for _, q := range queries {
			before := withCopy.CopyState().Answers
			docs, err := withCopy.Query(ctx, q.f, who...)
			got, asked := said(docs, err), withCopy.CopyState().Answers-before
			docs, err = plain.Query(ctx, q.f, who...)
			if want := said(docs, err); got != want {
				t.Errorf("%s asks for %s:\nthe copy's program  %s\nthe store's program %s", name, q.name, got, want)
			}
			if readsAll && q.sorts && asked != 1 {
				t.Errorf("%s asks for %s: the copy answered %d times, want once (%+v)", name, q.name, asked, withCopy.CopyState())
			}
		}
		for list, sorts := range lists {
			for _, read := range []fado.LiveRead{{}, {Limit: 3}, {Limit: 3, Offset: 3}, {Order: []string{"-number"}, Limit: 4}} {
				read.Class, read.ID, read.Variable = "Books", "acme", list
				before := withCopy.CopyState().Answers
				v, err := withCopy.LivePage(ctx, read, who...)
				got, asked := said(v, err), withCopy.CopyState().Answers-before
				v, err = plain.LivePage(ctx, read, who...)
				if want := said(v, err); got != want {
					t.Errorf("%s reads the list %s with %+v:\nthe copy's program  %s\nthe store's program %s", name, list, read, got, want)
				}
				if readsAll && sorts && asked != 1 {
					t.Errorf("%s reads the list %s with %+v: the copy answered %d times, want once (%+v)", name, list, read, asked, withCopy.CopyState())
				}
			}
		}
	}
	// What a page is, so that two wrong ones that agree are not taken
	// for right: the dearest invoices first, and a blank day before
	// every day.
	dear, err := withCopy.Query(ctx, fado.Filter{Class: "Invoice", Where: `net > 40`, Order: []string{"-net", "number"}, Limit: 3, Fields: []string{"number", "net"}}, ewa)
	if err != nil || len(dear) != 3 || dear[0].Values["net"] != "90.50 PLN" || dear[0].Values["number"] != "FV-08" {
		t.Errorf("the three dearest invoices: %v, %s", err, said(dear, nil))
	}
	first, err := withCopy.Query(ctx, fado.Filter{Class: "Invoice", Order: []string{"due", "number"}, Limit: 2, Fields: []string{"number", "due"}}, ewa)
	if err != nil || len(first) != 2 || first[0].Values["due"] != nil || first[0].Values["number"] != "FV-01" {
		t.Errorf("the first invoices by their day: %v, %s", err, said(first, nil))
	}
	// A page is never older than its question: an invoice made a moment
	// ago is on it.
	if _, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": "FV-40", "owner": "jan", "net": "999.00 PLN", "urgent": true}, ewa); err != nil {
		t.Fatal(err)
	}
	dear, err = withCopy.Query(ctx, fado.Filter{Class: "Invoice", Order: []string{"-net"}, Limit: 1}, ewa)
	if err != nil || len(dear) != 1 || dear[0].Values["number"] != "FV-40" {
		t.Errorf("the dearest invoice right after a dearer one was made: %v, %s", err, said(dear, nil))
	}
}
