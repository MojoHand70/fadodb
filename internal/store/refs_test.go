package store

import (
	"context"
	"strings"
	"testing"
)

// An order and the invoices that point at it (ADR-0076): Jan and Piotr
// are in sales and each reads the orders he owns; Ewa is in finance and
// reads every order.
const (
	refOrder = `{
 "name": "Order", "title": "number",
 "variables": {
   "number": {"type": "text", "required": true},
   "owner":  {"type": "text", "default": "USER()"}
 },
 "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
 "transitions": [{"name": "close", "from": "open", "to": "closed"}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "TRUE", "update": "owner = USER()", "delete": "owner = USER()"}
}`
	refInvoice = `{
 "name": "Invoice", "title": "number",
 "variables": {
   "number": {"type": "text", "required": true},
   "order":  {"type": "ref", "class": "Order"},
   "note":   {"type": "text"}
 },
 "states": {"initial": "draft", "list": ["draft"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}
}`
)

func openRefs(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{refOrder, refInvoice} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-17"}, As(liveJan), WithID("ORD-17")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-18"}, As(livePiotr), WithID("ORD-18")); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// gone holds a refusal to unknown_document about the variable, in the
// words get uses, whether the document is not there or not the caller's.
func gone(t *testing.T, what string, err error, variable, id string) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Code != CodeUnknownDocument || e.Variable != variable || e.Message != "no document "+id+" of class Order" {
		t.Errorf("%s: got %v, want unknown_document about %s: no document %s of class Order", what, err, variable, id)
	}
}

// A ref is checked when it is set: the document must be there, and the
// caller must be able to read it. The answer is the same for "not there"
// and "not yours", so that setting a pointer tells nobody what exists.
func TestRef_CheckedWhenItIsSet(t *testing.T) {
	s, ctx := openRefs(t)
	// Jan files an invoice on his own order.
	doc, err := s.Create(ctx, "Invoice", Values{"number": "FV-1", "order": "Order:ORD-17"}, As(liveJan), WithID("FV-1"))
	if err != nil || doc.Values["order"] != "Order:ORD-17" {
		t.Fatalf("an invoice on an order that is there = %v, %v", doc, err)
	}
	// A slip of the id, and an order that is Piotr's: one answer.
	_, err = s.Create(ctx, "Invoice", Values{"number": "FV-9", "order": "Order:ORD-71"}, As(liveJan))
	gone(t, "an order that is not there", err, "order", "ORD-71")
	_, err = s.Create(ctx, "Invoice", Values{"number": "FV-9", "order": "Order:ORD-18"}, As(liveJan))
	gone(t, "an order the caller may not read", err, "order", "ORD-18")
	if n, _ := s.Count(ctx, Filter{Class: "Invoice"}, As(liveJan)); n != 1 {
		t.Errorf("%d invoices after two refused ones, want 1", n)
	}
	// Ewa reads every order, so she may point at Piotr's.
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-2", "order": "Order:ORD-18"}, As(liveEwa), WithID("FV-2")); err != nil {
		t.Errorf("Ewa points at an order she may read: %v", err)
	}
	// A set is checked as a create is.
	_, err = s.Set(ctx, "Invoice", "FV-1", Values{"order": "Order:ORD-71"}, As(liveJan))
	gone(t, "a set to an order that is not there", err, "order", "ORD-71")
	_, err = s.Set(ctx, "Invoice", "FV-1", Values{"order": "Order:ORD-18"}, As(liveJan))
	gone(t, "a set to an order the caller may not read", err, "order", "ORD-18")
	// No pointer, and a pointer taken away, are asked nothing.
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-3"}, As(liveJan), WithID("FV-3")); err != nil {
		t.Errorf("an invoice with no order: %v", err)
	}
	if got, err := s.Set(ctx, "Invoice", "FV-1", Values{"order": nil}, As(liveJan)); err != nil || got.Values["order"] != nil {
		t.Errorf("taking the pointer away = %v, %v", got, err)
	}
	// A preview says what the commit would refuse, and writes nothing.
	pv, err := s.Preview(ctx, "Invoice", "FV-1", Values{"order": "Order:ORD-71"}, "", As(liveJan))
	if err != nil || len(pv.Errors) != 1 || pv.Errors[0].Code != CodeUnknownDocument || pv.Errors[0].Variable != "order" {
		t.Errorf("a preview of a set to an order that is not there = %+v, %v; want the one error unknown_document about order", pv, err)
	}
}

// A ref already in a document is not checked again when other values
// change: the invoice Ewa filed on Piotr's order stays Jan's to annotate,
// though he may not read that order, and stays so when the order is gone.
func TestRef_NotCheckedAgain(t *testing.T) {
	s, ctx := openRefs(t)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-2", "order": "Order:ORD-18"}, As(liveEwa), WithID("FV-2")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Set(ctx, "Invoice", "FV-2", Values{"note": "seen by Jan"}, As(liveJan))
	if err != nil || got.Values["order"] != "Order:ORD-18" || got.Values["note"] != "seen by Jan" {
		t.Fatalf("another value changed beside a pointer the caller may not follow = %v, %v", got, err)
	}
	// The same pointer given again is not a new pointer.
	if _, err := s.Set(ctx, "Invoice", "FV-2", Values{"order": "Order:ORD-18", "note": "again"}, As(liveJan)); err != nil {
		t.Errorf("the pointer the document holds, given again: %v", err)
	}
	// The order is deleted: the pointer stays and names a document that is
	// gone (the record leaves what a delete does to pointers undecided).
	if err := s.Delete(ctx, "Order", "ORD-18", As(livePiotr)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Set(ctx, "Invoice", "FV-2", Values{"note": "after the delete"}, As(liveJan)); err != nil || got.Values["order"] != "Order:ORD-18" {
		t.Errorf("a change beside a pointer to a deleted document = %v, %v", got, err)
	}
	// But a pointer newly set at the deleted order is refused, for Ewa too.
	_, err = s.Create(ctx, "Invoice", Values{"number": "FV-4", "order": "Order:ORD-18"}, As(liveEwa))
	gone(t, "a new pointer at a deleted order", err, "order", "ORD-18")
}

// Many documents made in one call: each ref is checked, and a document
// of the same call may be pointed at by one that comes after it.
func TestRef_ManyInOneCall(t *testing.T) {
	s, ctx := openRefs(t)
	docs, err := s.CreateMany(ctx, "Invoice", []Values{
		{"number": "FV-1", "order": "Order:ORD-17"}, {"number": "FV-2", "order": "Order:ORD-17"}}, As(liveJan))
	if err != nil || len(docs) != 2 {
		t.Fatalf("two invoices on one order = %v, %v", docs, err)
	}
	_, err = s.CreateMany(ctx, "Invoice", []Values{{"number": "FV-3", "order": "Order:ORD-17"}, {"number": "FV-4", "order": "Order:ORD-71"}}, As(liveJan))
	if e, ok := err.(*Error); !ok || e.Code != CodeUnknownDocument || !strings.Contains(e.Variable, "order") {
		t.Errorf("a batch with a pointer at an order that is not there = %v, want unknown_document about its order", err)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Invoice"}, As(liveJan)); n != 2 {
		t.Errorf("%d invoices after a refused batch, want 2: all or none", n)
	}
}

// A class says what points at it (ADR-0076, item 12): the ref variables
// of the classes defined now, worked out and written by nobody.
func TestRef_WhatPointsAtAClass(t *testing.T) {
	s, ctx := openRefs(t)
	order, err := s.Definition(ctx, DefinitionClass, "Order", 0)
	if err != nil || len(order.PointedAtBy) != 1 || order.PointedAtBy[0] != (Pointer{Class: "Invoice", Variable: "order"}) {
		t.Fatalf("what points at Order = %+v, %v; want the order of Invoice", order, err)
	}
	if invoice, err := s.Definition(ctx, DefinitionClass, "Invoice", 0); err != nil || invoice.PointedAtBy != nil {
		t.Errorf("what points at Invoice = %+v, %v; want nothing", invoice, err)
	}
	// A second class points at orders too, and Invoice gains a second
	// pointer: they are listed in the order the classes were defined and
	// their variables declared.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Shipment", "variables": {"of": {"type": "ref", "class": "order"}, "invoice": {"type": "ref", "class": "Invoice"}},
 "states": {"initial": "packed", "list": ["packed"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(refInvoice, `"note":   {"type": "text"}`, `"note":   {"type": "text"}, "first": {"type": "ref", "class": "Order"}`, 1)
	if _, err := s.DefineClass(ctx, []byte(next), asSetup); err != nil {
		t.Fatal(err)
	}
	order, err = s.Definition(ctx, DefinitionClass, "Order", 0)
	want := []Pointer{{"Invoice", "order"}, {"Invoice", "first"}, {"Shipment", "of"}}
	if err != nil || len(order.PointedAtBy) != len(want) {
		t.Fatalf("what points at Order now = %+v, %v; want %v", order.PointedAtBy, err, want)
	}
	for i, p := range want {
		if order.PointedAtBy[i] != p {
			t.Errorf("pointer %d at Order = %+v, want %+v", i, order.PointedAtBy[i], p)
		}
	}
	if invoice, err := s.Definition(ctx, DefinitionClass, "Invoice", 0); err != nil || len(invoice.PointedAtBy) != 1 || invoice.PointedAtBy[0] != (Pointer{"Shipment", "invoice"}) {
		t.Errorf("what points at Invoice now = %+v, %v; want the invoice of Shipment", invoice.PointedAtBy, err)
	}
	// An earlier version of the class is told what points at the class now.
	if first, err := s.Definition(ctx, DefinitionClass, "Invoice", 1); err != nil || len(first.PointedAtBy) != 1 {
		t.Errorf("what points at Invoice, asked of its first version = %+v, %v", first, err)
	}
}
