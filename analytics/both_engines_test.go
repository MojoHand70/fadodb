package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// One program asks one class of both engines: the flow of a class of
// the copy, a list and a count of the store. The reader's rule is in
// every one of those statements, written for the engine that runs it
// (ADR-0089, item 7). Whichever engine is asked first, each answers
// what a program without the analytical engine answers, for the same
// reader. Dan's rule compares a true-or-false and a date, which the two
// engines write differently.
func TestOneClassAskedOfBothEngines(t *testing.T) {
	ctx := context.Background()
	ewa, jan, dan := person("ewa", "finance"), person("jan"), person("dan", "duty")
	for _, first := range []string{"the copy", "the store"} {
		t.Run(first+" is asked first", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "acme.fado")
			now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
			clock := func() time.Time { now = now.Add(time.Minute); return now }
			both, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
			if err != nil {
				t.Fatal(err)
			}
			defer both.Close()
			if _, err := both.DefineClass(ctx, []byte(flowInvoice), olga); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 12; i++ {
				if _, err := both.Create(ctx, "Invoice", fado.Values{"number": fmt.Sprintf("FV-%d", i), "owner": []string{"jan", "ola"}[i%2], "customer": "Globex",
					"net": "100.00 PLN", "urgent": i%3 == 0, "due": []string{"2025-12-31", "2026-06-30"}[i%2]}, ewa, fado.WithID(fmt.Sprintf("FV-%d", i))); err != nil {
					t.Fatal(err)
				}
			}
			if err := both.FeedCopy(ctx); err != nil {
				t.Fatal(err)
			}
			// A program without the engine, on the same database.
			plain, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer plain.Close()

			flow := func(db *fado.DB, who fado.Option) string {
				t.Helper()
				f, err := db.Flow(ctx, "Invoice", fado.FlowPeriod{}, who)
				if err != nil {
					t.Fatalf("the flow: %v", err)
				}
				text, _ := json.Marshal(f)
				return string(text)
			}
			list := func(db *fado.DB, who fado.Option) string {
				t.Helper()
				docs, err := db.Query(ctx, fado.Filter{Class: "Invoice", Where: `net > 0`}, who)
				if err != nil {
					t.Fatalf("the list: %v", err)
				}
				n, err := db.Count(ctx, fado.Filter{Class: "Invoice", Where: `state = "draft"`}, who)
				if err != nil {
					t.Fatalf("the count: %v", err)
				}
				ids := make([]string, len(docs))
				for i, d := range docs {
					ids[i] = d.ID
				}
				return fmt.Sprintf("%d: %s", n, strings.Join(ids, " "))
			}
			for name, who := range map[string]fado.Option{"ewa": ewa, "jan": jan, "dan": dan} {
				wantFlow, wantList := flow(plain, who), list(plain, who)
				ask := []func(){
					func() {
						before := both.CopyState().Answers
						if got := flow(both, who); got != wantFlow {
							t.Errorf("%s, the flow:\n got %s\nwant %s", name, got, wantFlow)
						}
						if st := both.CopyState(); st.Answers != before+1 || st.AskErr != nil {
							t.Errorf("%s: the copy did not answer the flow: %+v", name, st)
						}
					},
					func() {
						if got := list(both, who); got != wantList {
							t.Errorf("%s, the list and the count:\n got %s\nwant %s", name, got, wantList)
						}
					},
				}
				if first == "the store" {
					ask[0], ask[1] = ask[1], ask[0]
				}
				// Each twice: the second time the rule is found ready.
				for _, a := range append(ask, ask...) {
					a()
				}
			}
			// What the answers are, so that two wrong ones that agree are
			// not taken for right: dan, on duty, reads the urgent invoices
			// that are due this year, FV-3 and FV-9.
			if got := list(both, dan); got != "2: FV-3 FV-9" {
				t.Errorf("dan reads %s; want the two urgent invoices due in 2026", got)
			}
		})
	}
}
