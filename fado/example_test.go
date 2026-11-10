package fado_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The invoice of docs/architecture.md, as far as this build takes it: a
// class with defaults, objects, lines and auto formulas; a document
// created, changed, previewed and read back with its history.
func Example() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	db, err := fado.Open(filepath.Join(tmp, "example.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Invoice",
	  "title": "\"INV-\" & number",
	  "variables": {
	    "number":   {"type": "text", "required": true},
	    "customer": {"type": "ref", "class": "Customer"},
	    "issued":   {"type": "date", "default": "TODAY()"},
	    "due":      {"type": "date", "default": "ADDDAYS(issued, 30)"},
	    "owner":    {"type": "text", "default": "USER()"},
	    "discount": {"type": "decimal", "scale": 2, "default": "0"},
	    "net":      {"type": "money", "scale": 2},
	    "shipping": {"type": "object", "schema": "Address"},
	    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "number", "price": "decimal(2)"}},
	    "total":     {"formula": "ROUND(net * (1 - discount), 2)"},
	    "ship_city": {"formula": "shipping.city"}
	  },
	  "states": {"initial": "draft", "list": ["draft", "approved", "paid"]}
	}`))
	if err != nil {
		panic(err)
	}

	alice := fado.Principal{ID: "alice", Groups: []string{"sales"}}
	values := fado.Values{
		"number":   "2026-0042",
		"customer": "Customer:42",
		"net":      "100.00 EUR",
		"shipping": fado.Values{"line1": "Długa 1", "city": "Gdańsk", "country": "PL"},
		"lines":    []fado.Values{{"sku": "A1", "qty": 2, "price": "9.50"}},
	}
	doc, err := db.Create(ctx, "Invoice", values, fado.As(alice), fado.Key("ksef-2026-0042"), fado.WithID("inv_42"))
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.Title, doc.State, doc.Version, doc.Values["issued"], doc.Values["due"], doc.Values["owner"], doc.Values["total"], doc.Values["ship_city"])

	// The same command with the same key is the same commit, not a second
	// document.
	again, err := db.Create(ctx, "Invoice", values, fado.As(alice), fado.Key("ksef-2026-0042"), fado.WithID("inv_42"))
	fmt.Println("replayed:", err == nil && again.Version == doc.Version)

	// A change recalculates what depends on it and is a new version.
	doc, _ = db.Set(ctx, "Invoice", "inv_42", fado.Values{"discount": "0.10"}, fado.As(alice))
	fmt.Println(doc.Version, doc.Values["total"])

	// A preview computes and writes nothing.
	preview, _ := db.Preview(ctx, "Invoice", "inv_42", fado.Values{"discount": "0.50"}, "", fado.As(alice))
	current, _ := db.Get(ctx, "Invoice", "inv_42")
	fmt.Println("preview:", preview.Document.Values["total"], "stored:", current.Values["total"])

	// A refusal names the variable and a stable code.
	_, err = db.Set(ctx, "Invoice", "inv_42", fado.Values{"total": "1.00 EUR"})
	var e *fado.Error
	if errors.As(err, &e) {
		fmt.Println(e.Code, e.Variable)
	}

	// Every version is in the history, with who committed it.
	history, _ := db.History(ctx, "Invoice", "inv_42")
	for _, h := range history {
		fmt.Println("version", h.Version, h.Values["total"], h.ActorID, h.ValidTo == 0)
	}

	// A filter is a formula over the class's variables; SQL runs over the
	// views.
	open, _ := db.Query(ctx, fado.Filter{Class: "Invoice", Where: "state = \"draft\" AND total > 50.00", Fields: []string{"number", "total"}})
	fmt.Println("filtered:", len(open), open[0].Values["number"], open[0].Values["total"])
	rows, _ := db.SQL(ctx, `SELECT count(*) AS n, sum(total) AS cents FROM Invoice`, nil)
	fmt.Println("sql:", rows.Columns, rows.Rows[0])

	// A definition's problems come back at once, each with a place.
	_, err = db.DefineClass(ctx, []byte(`{"name": "Receipt", "variables": {"amount": {"type": "money", "default": "1"}, "paid": {"type": "bool", "formula": "amount"}}}`))
	var diags fado.Diagnostics
	if errors.As(err, &diags) {
		for _, d := range diags {
			fmt.Println(d.Path, d.Code)
		}
	}
	// Output:
	// INV-2026-0042 draft 1 2026-11-08 2026-12-08 alice 100.00 EUR Gdańsk
	// replayed: true
	// 2 90.00 EUR
	// preview: 50.00 EUR stored: 90.00 EUR
	// read_only total
	// version 1 100.00 EUR alice false
	// version 2 90.00 EUR alice true
	// filtered: 1 2026-0042 90.00 EUR
	// sql: [n cents] [1 9000]
	// /variables/amount/default type_mismatch
	// /variables/paid/formula type_mismatch
}

// Rules run inside the commit (ADR-0043): a transition stamps who took it
// and when, into variables nobody may write by hand, and says so in the
// event log; a preview reports what would run before anything does.
func Example_rules() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	db, err := fado.Open(filepath.Join(tmp, "rules.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Request",
	  "variables": {
	    "amount":      {"type": "money", "scale": 2},
	    "approved_by": {"type": "text"},
	    "approved_at": {"type": "datetime"},
	    "large":       {"formula": "AMOUNT(amount) > 1000"}
	  },
	  "states": {"initial": "open", "list": ["open", "approved"]},
	  "transitions": [{"name": "approve", "from": "open", "to": "approved"}],
	  "rules": [
	    {"on": "transition", "name": "approve",
	     "do": [{"action": "set", "values": {"approved_by": "USER()", "approved_at": "NOW()"}}]},
	    {"on": "enter", "state": "approved", "if": "large",
	     "do": [{"action": "emit", "event": "request.large_approved"}]}
	  ],
	  "rights": {"*": {"approved_by": {"write": "FALSE"}, "approved_at": {"write": "FALSE"}}}
	}`))
	if err != nil {
		panic(err)
	}
	fin := fado.Principal{ID: "fin", Groups: []string{"finance"}}
	doc, err := db.Create(ctx, "Request", fado.Values{"amount": "2500.00 EUR"}, fado.As(fin), fado.WithID("req_1"))
	if err != nil {
		panic(err)
	}

	// Nobody sets the stamp by hand.
	_, err = db.Set(ctx, "Request", doc.ID, fado.Values{"approved_by": "fin"}, fado.As(fin))
	var refusal *fado.Error
	if errors.As(err, &refusal) {
		fmt.Println("by hand:", refusal.Code)
	}

	// The preview names the rules the transition would run.
	preview, _ := db.Preview(ctx, "Request", doc.ID, nil, "approve", fado.As(fin))
	for _, r := range preview.Rules {
		fmt.Println("would run:", r.Path, r.Ran, r.Actions[0].Action)
	}

	doc, err = db.Transition(ctx, "Request", doc.ID, "approve", nil, fado.As(fin))
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.State, "by", doc.Values["approved_by"], "at", doc.Values["approved_at"], "version", doc.Version)

	events, _ := db.Events(ctx, "Request", doc.ID)
	for _, e := range events {
		fmt.Println(e.Kind, e.Name, e.AfterVersion)
	}
	// Output:
	// by hand: forbidden
	// would run: /rules/0 true set
	// would run: /rules/1 true emit
	// approved by fin at 1794301200 version 2
	// created  1
	// transition approve 2
	// custom request.large_approved 2
}
