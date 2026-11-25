package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/query"
)

// A free question that two indexes fit (ADR-0075, item 10): the store
// has no numbers on the data, so the engine looks into each index and
// asks through the one that ends first. Acme has thirty orders of
// eighty invoices each. Globex has half of the invoices and ten small
// customers share the rest; twenty-four invoices wait, sixteen of them
// Globex's, and the others are done. Jan wrote them all. Piotr wrote
// three more, of order ORD-5 for Globex, one of which waits. Ewa, in
// finance, reads every one.
const lookInvoice = `{
 "name": "Invoice", "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "order":    {"type": "ref", "class": "Order", "index": true},
   "customer": {"type": "text", "index": true},
   "stage":    {"type": "text", "index": true},
   "note":     {"type": "text"},
   "owner":    {"type": "text", "default": "USER()", "index": true}
 },
 "states": {"initial": "draft", "list": ["draft"]},
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE"}
}`

const lookOrder = `{
 "name": "Order", "title": "number",
 "variables": {
   "number": {"type": "text", "required": true},
   "globex": {"mode": "live", "formula": "QUERY(Invoice, {number}, order = REF() AND customer = \"Globex\", {number})"},
   "plain":  {"mode": "live", "formula": "QUERY(Invoice, {number}, customer = \"Globex\" AND stage = \"waiting\", {number})"}
 },
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

func openLook(t testing.TB) (*Store, context.Context) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "look.fado"), Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(lookOrder), []byte(lookInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	for o := 0; o < 30; o++ {
		id := fmt.Sprintf("ORD-%d", o)
		if _, err := s.Create(ctx, "Order", Values{"number": id}, As(liveJan), WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	batch := make([]Values, 0, 2400)
	for k := 0; k < 2400; k++ {
		// Every second round of the thirty orders is Globex's, so that
		// each order has invoices of Globex and of the others.
		customer, stage := "Globex", "done"
		if (k/30)%2 == 1 {
			customer = fmt.Sprintf("C-%d", k%10)
		}
		if k%100 == 6 {
			stage = "waiting"
		}
		batch = append(batch, Values{"number": fmt.Sprintf("FV-%04d", k), "order": fmt.Sprintf("Order:ORD-%d", k%30), "customer": customer, "stage": stage})
	}
	if _, err := s.CreateMany(ctx, "Invoice", batch, As(liveJan)); err != nil {
		t.Fatal(err)
	}
	for i, number := range []string{"P-1", "P-2", "P-3"} {
		if _, err := s.Create(ctx, "Invoice", Values{"number": number, "order": "Order:ORD-5", "customer": "Globex", "stage": []string{"done", "waiting", "done"}[i]}, As(livePiotr)); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

// taken says through which column's index the engine asks a question,
// "" when the question is not left to a look, and how the store then
// reads it.
func taken(t *testing.T, s *Store, where string, who Principal) (column, how string) {
	t.Helper()
	ctx := context.Background()
	c, _ := s.class("Invoice")
	o := options{principal: who}
	plan, err := query.Compile(Filter{Class: "Invoice", Where: where}, c.cls, s.sources(), caller(o, c))
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	asked, args, err := s.through(ctx, c, plan, o)
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	for _, f := range plan.Fits {
		if f.Where == asked {
			column = f.Column
		}
	}
	if asked == "" {
		return column, ""
	}
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT "id" FROM "Invoice" WHERE `+asked, args...)
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		how += d + "\n"
	}
	return column, how
}

// foundNumbers are the numbers of the invoices a question finds, in order.
func foundNumbers(t *testing.T, s *Store, where string, who Principal) string {
	t.Helper()
	docs, err := s.Query(context.Background(), Filter{Class: "Invoice", Where: where, Order: []string{"number"}, Limit: 1000}, As(who))
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Values["number"].(string)
	}
	return strings.Join(out, " ")
}

// The engine asks through the index that ends first, whichever way the
// question is written, and the answer is the one the question has
// without any index.
func TestLook_TheIndexThatEndsFirst(t *testing.T) {
	s, ctx := openLook(t)
	for _, tc := range []struct {
		name, where, other, slow string
		column                   string
		found                    int
	}{
		{"the invoices of one order for Globex", `order = REF("Order", "ORD-5") AND customer = "Globex"`, `customer = "Globex" AND order = REF("Order", "ORD-5")`,
			`TEXT(order) = "Order:ORD-5" AND customer & "" = "Globex"`, "order", 43},
		{"Globex's invoices that wait", `customer = "Globex" AND stage = "waiting"`, `stage = "waiting" AND customer = "Globex"`,
			`customer & "" = "Globex" AND stage & "" = "waiting"`, "stage", 17},
		{"the done invoices of a small customer", `stage = "done" AND customer = "C-3"`, `customer = "C-3" AND stage = "done"`,
			`customer & "" = "C-3" AND stage & "" = "done"`, "customer", 120},
		{"three comparisons", `stage = "done" AND customer = "Globex" AND order = REF("Order", "ORD-7")`, `order = REF("Order", "ORD-7") AND stage = "done" AND customer = "Globex"`,
			`stage & "" = "done" AND customer & "" = "Globex" AND TEXT(order) = "Order:ORD-7"`, "order", 40},
	} {
		for _, where := range []string{tc.where, tc.other} {
			column, how := taken(t, s, where, liveEwa)
			if column != tc.column || !strings.Contains(how, "doc_Invoice_"+tc.column) || strings.Contains(how, "SCAN") {
				t.Errorf("%s, %s: asked through %q; want the index on %s\n%s", tc.name, where, column, tc.column, how)
			}
			if got, want := foundNumbers(t, s, where, liveEwa), foundNumbers(t, s, tc.slow, liveEwa); got != want || len(strings.Fields(got)) != tc.found {
				t.Errorf("%s, %s: finds %d invoices; want the %d the question finds without an index (%d)", tc.name, where, len(strings.Fields(got)), tc.found, len(strings.Fields(want)))
			}
			if n, err := s.Count(ctx, Filter{Class: "Invoice", Where: where}, As(liveEwa)); err != nil || n != int64(tc.found) {
				t.Errorf("%s, %s: counts %d, %v; want %d", tc.name, where, n, err, tc.found)
			}
		}
	}
}

// The comparison of the read rule is a fit like the caller's own: Jan,
// who wrote nearly every invoice, is led by the order he asks for, and
// Piotr, who wrote three, by what he wrote.
func TestLook_TheReadRuleIsAFitToo(t *testing.T) {
	s, _ := openLook(t)
	if column, how := taken(t, s, `order = REF("Order", "ORD-5")`, liveJan); column != "order" || !strings.Contains(how, "doc_Invoice_order") {
		t.Errorf("Jan asks the invoices of one order through %q:\n%s", column, how)
	}
	if got := foundNumbers(t, s, `order = REF("Order", "ORD-5")`, liveJan); len(strings.Fields(got)) != 80 {
		t.Errorf("Jan finds %d invoices of ORD-5; want his 80", len(strings.Fields(got)))
	}
	if column, how := taken(t, s, `customer = "Globex"`, livePiotr); column != "owner" || !strings.Contains(how, "doc_Invoice_owner") {
		t.Errorf("Piotr asks Globex's invoices through %q:\n%s", column, how)
	}
	if got := foundNumbers(t, s, `customer = "Globex"`, livePiotr); got != "P-1 P-2 P-3" {
		t.Errorf("Piotr finds %q; want his three", got)
	}
	// Two looks that end alike, at the limit: the earlier fit is taken,
	// the caller's own before the rule's.
	if column, _ := taken(t, s, `customer = "Globex"`, liveJan); column != "customer" {
		t.Errorf("Jan asks Globex's invoices through %q; want the caller's own comparison", column)
	}
	if got := foundNumbers(t, s, `customer = "Globex" AND stage = "waiting"`, liveJan); len(strings.Fields(got)) != 16 {
		t.Errorf("Jan finds %d of Globex's invoices that wait; want his 16", len(strings.Fields(got)))
	}
}

// A question that one index fits, or none, is asked as it always was,
// and so is one a look finds nothing for.
func TestLook_OnlyWhereTwoIndexesFit(t *testing.T) {
	s, ctx := openLook(t)
	for _, where := range []string{`customer = "Globex"`, `note = "x" AND number = "FV-0001"`, `customer = "Globex" AND note = "x"`, `customer <> "Globex" AND stage = "waiting"`, ``} {
		if column, _ := taken(t, s, where, liveEwa); column != "" {
			t.Errorf("%q is left to a look, through %s; one index fits it at most", where, column)
		}
	}
	if got := foundNumbers(t, s, `customer <> "Globex" AND stage = "waiting"`, liveEwa); len(strings.Fields(got)) != 8 {
		t.Errorf("the invoices that wait and are not Globex's: %q; want eight", got)
	}
	// Nothing points at an order nobody made: the first look ends the
	// looking.
	if got := foundNumbers(t, s, `order = REF("Order", "ORD-999") AND customer = "Globex"`, liveEwa); got != "" {
		t.Errorf("the invoices of an order that is not there: %q", got)
	}
	if n, err := s.Count(ctx, Filter{Class: "Invoice", Where: `order = REF("Order", "ORD-999") AND customer = "Globex"`}, As(liveEwa)); err != nil || n != 0 {
		t.Errorf("counted %d, %v; want 0", n, err)
	}
}

// A declared question names its index (ADR-0075, items 2 and 10): the
// list of an order's invoices is asked through the pointer it compares
// with the order, whatever else its condition compares, and nobody
// looks. A list that compares nothing of its document is left to a
// look, as a free question is.
func TestLook_ADeclaredQuestionNamesItsIndex(t *testing.T) {
	s, ctx := openLook(t)
	c, _ := s.class("Order")
	src, _ := s.class("Invoice")
	o := options{principal: liveEwa}
	for variable, want := range map[string]string{"GLOBEX": "", "PLAIN": "stage"} {
		_, on, _, err := s.liveOn(ctx, c, c.byName[variable], "ORD-5", o, nil)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.CompileOn(Filter{Class: "Invoice"}, on, src.cls, s.sources(), caller(o, src))
		if err != nil {
			t.Fatal(err)
		}
		asked, args, err := s.through(ctx, src, plan, o)
		if err != nil {
			t.Fatal(err)
		}
		column := ""
		for _, f := range plan.Fits {
			if f.Where == asked {
				column = f.Column
			}
		}
		if column != want {
			t.Errorf("the list %s is asked through a look at %q; want %q", variable, column, want)
		}
		if variable == "GLOBEX" && (len(plan.Fits) != 0 || !strings.Contains(asked, `"order" = `) || strings.Contains(asked, `"customer" = :`)) {
			t.Errorf("the list of an order's invoices does not name the pointer alone: %d fits, %s", len(plan.Fits), asked)
		}
		rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT "id" FROM "Invoice" WHERE `+asked, args...)
		if err != nil {
			t.Fatal(err)
		}
		var how string
		for rows.Next() {
			var a, b, c int
			var d string
			rows.Scan(&a, &b, &c, &d)
			how += d + "\n"
		}
		rows.Close()
		through := map[string]string{"GLOBEX": "doc_Invoice_order", "PLAIN": "doc_Invoice_stage"}[variable]
		if !strings.Contains(how, through) || strings.Contains(how, "SCAN") {
			t.Errorf("the list %s is not read through %s:\n%s", variable, through, how)
		}
	}
	v, err := s.Live(ctx, "Order", "ORD-5", "globex", As(liveEwa))
	if err != nil || len(v.Rows) != 43 {
		t.Errorf("Globex's invoices of ORD-5: %d, %v; want 43", len(v.Rows), err)
	}
	v, err = s.Live(ctx, "Order", "ORD-5", "plain", As(liveEwa))
	if err != nil || len(v.Rows) != 17 {
		t.Errorf("Globex's invoices that wait: %d, %v; want 17", len(v.Rows), err)
	}
}
