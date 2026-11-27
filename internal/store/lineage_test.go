package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The case of the owner's page (ADR-0099): a region keeps what its
// customers add up to, a customer what its filed invoices add up to, an
// invoice its net from an amount Ann types and a rate the table Rates
// had when she filed it.
const (
	lineageRates = `{"name": "Rates", "columns": {"currency": "text", "rate": "decimal(4)"}, "key": ["currency"],
 "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}}`
	lineageRegion = `{"name": "Region", "title": "name",
 "variables": {"name": {"type": "text", "required": true},
   "sales": {"type": "decimal", "scale": 2, "mode": "triggered", "on": ["schedule:daily at 04:00", "refresh"],
             "formula": "LOOKUP(Customer, SUM(invoiced), region = REF())"}},
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"leads\")", "update": "INGROUP(\"leads\")", "delete": "FALSE"}}`
	lineageCustomer = `{"name": "Customer", "title": "name",
 "variables": {"name": {"type": "text", "required": true},
   "region": {"type": "ref", "class": "Region", "index": true},
   "invoiced": {"type": "decimal", "scale": 2, "mode": "triggered", "on": ["schedule:daily at 03:00", "refresh"],
                "formula": "LOOKUP(Invoice, SUM(net), customer = REF() AND state <> \"draft\")"},
   "open_now": {"mode": "live", "formula": "LOOKUP(Invoice, SUM(net), customer = REF() AND state <> \"draft\")"}},
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"leads\")", "update": "INGROUP(\"leads\")", "delete": "FALSE"}}`
	lineageInvoice = `{"name": "Invoice", "title": "number",
 "variables": {"number": {"type": "text", "required": true},
   "customer": {"type": "ref", "class": "Customer", "index": true},
   "owner": {"type": "text", "default": "USER()"},
   "amount": {"type": "decimal", "scale": 2, "required": true},
   "currency": {"type": "text", "required": true},
   "rate": {"type": "decimal", "scale": 4, "mode": "triggered", "on": ["transition:file"], "formula": "LOOKUP(Rates, rate, currency = THIS.currency)"},
   "net": {"type": "decimal", "scale": 2, "formula": "ROUND(amount * rate, 2)"}},
 "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
 "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR INGROUP(\"leads\")", "create": "INGROUP(\"sales\")", "update": "owner = USER() AND STATE() <> \"paid\"", "delete": "FALSE",
   "transition": {"file": "owner = USER()", "approve": "INGROUP(\"leads\")"}}}`
)

var (
	lineageTess  = As(Principal{ID: "tess", Kind: ActorPerson, Groups: []string{"treasury"}})
	lineageAnn   = As(Principal{ID: "ann", Kind: ActorPerson, Groups: []string{"sales"}})
	lineagePiotr = As(Principal{ID: "piotr", Kind: ActorPerson, Groups: []string{"sales"}})
	lineageDan   = As(Principal{ID: "dan", Kind: ActorPerson, Groups: []string{"leads"}})
)

// lineageCase plays the story of the page: on 2 December the rates, the
// invoices filed; that night the customers at 03:00 and the region at
// 04:00; on 3 December Tess changes the euro at 10:00 and Piotr corrects
// INV-18 at 11:00. A definition of Rates given takes the place of the
// case's.
func lineageCase(t *testing.T, rates ...string) *Store {
	t.Helper()
	cl := &still{now: time.Date(2026, 12, 2, 9, 0, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	def := lineageRates
	if len(rates) > 0 {
		def = rates[0]
	}
	if _, err := s.DefineTable(ctx, []byte(def), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{
		`{"name": "Region", "title": "name", "variables": {"name": {"type": "text", "required": true}}, "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`,
		`{"name": "Customer", "title": "name", "variables": {"name": {"type": "text", "required": true}}, "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`,
		lineageInvoice, lineageCustomer, lineageRegion,
	} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	must := func(_ *Document, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.2500"}, {"currency": "PLN", "rate": "1.0000"}}, lineageTess); err != nil {
		t.Fatal(err)
	}
	must(s.Create(ctx, "Region", Values{"name": "North"}, lineageDan, WithID("north")))
	for _, c := range []string{"Acme", "Borg"} {
		must(s.Create(ctx, "Customer", Values{"name": c, "region": "Region:north"}, lineageDan, WithID(c)))
	}
	for _, inv := range []struct {
		who                         Option
		id, customer, amount, money string
	}{{lineageAnn, "INV-17", "Acme", "300.00", "EUR"}, {lineagePiotr, "INV-18", "Acme", "2000.00", "PLN"}, {lineageAnn, "INV-19", "Borg", "500.00", "EUR"}} {
		must(s.Create(ctx, "Invoice", Values{"number": inv.id, "customer": "Customer:" + inv.customer, "amount": inv.amount, "currency": inv.money}, inv.who, WithID(inv.id)))
		must(s.Transition(ctx, "Invoice", inv.id, "file", nil, inv.who))
	}
	cl.set(time.Date(2026, 12, 3, 3, 0, 0, 0, time.UTC))
	must(s.Refresh(ctx, "Customer", "Acme", nil, lineageDan))
	must(s.Refresh(ctx, "Customer", "Borg", nil, lineageDan))
	cl.set(time.Date(2026, 12, 3, 4, 0, 0, 0, time.UTC))
	must(s.Refresh(ctx, "Region", "north", nil, lineageDan))
	cl.set(time.Date(2026, 12, 3, 10, 0, 0, 0, time.UTC))
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"currency": "EUR", "rate": "4.3100"}}, lineageTess); err != nil {
		t.Fatal(err)
	}
	cl.set(time.Date(2026, 12, 3, 11, 0, 0, 0, time.UTC))
	must(s.Set(ctx, "Invoice", "INV-18", Values{"amount": "2200.00"}, lineagePiotr))
	cl.set(time.Date(2026, 12, 3, 12, 0, 0, 0, time.UTC))
	return s
}

func lineageJSON(t *testing.T, l *Lineage, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Dan asks where North's 5,400.00 comes from, and walks down: each level
// is what its formula read as it was then, and adds up (ADR-0099,
// part 2).
func TestWhereAValueComesFrom(t *testing.T) {
	s := lineageCase(t)
	ctx := context.Background()
	at := func(h int) string {
		return secondsText(time.Date(2026, 12, 3, h, 0, 0, 0, time.UTC).Unix())
	}
	filed := secondsText(time.Date(2026, 12, 2, 9, 0, 0, 0, time.UTC).Unix())
	for _, c := range []struct {
		what                string
		class, id, variable string
		version             int64
		as                  Option
		want                string
	}{
		{"the region", "Region", "north", "sales", 0, lineageDan,
			`{"class":"Region","id":"north","variable":"sales","version":2,"value":"5400.00","kind":"kept","formula":"LOOKUP(Customer, SUM(invoiced), region = REF())",` +
				`"since":{"version":2,"at":` + at(4) + `,"by":"dan","by_kind":"person","event":"refresh"},` +
				`"lookups":[{"class":"Customer","aggregate":"SUM","column":"invoiced","value":"5400.00","count":2,` +
				`"parts":[{"id":"Acme","version":2,"value":"3275.00"},{"id":"Borg","version":2,"value":"2125.00"}],"more":false,` +
				`"changed_since":{"count":0,"parts":[]},"now":"5400.00"}],"adds_up":true}`},
		{"the customer", "Customer", "Acme", "invoiced", 0, lineageDan,
			`{"class":"Customer","id":"Acme","variable":"invoiced","version":2,"value":"3275.00","kind":"kept","formula":"LOOKUP(Invoice, SUM(net), customer = REF() AND state \u003c\u003e \"draft\")",` +
				`"since":{"version":2,"at":` + at(3) + `,"by":"dan","by_kind":"person","event":"refresh"},` +
				`"lookups":[{"class":"Invoice","aggregate":"SUM","column":"net","value":"3275.00","count":2,` +
				`"parts":[{"id":"INV-18","version":2,"value":"2000.00"},{"id":"INV-17","version":2,"value":"1275.00"}],"more":false,` +
				`"changed_since":{"count":1,"parts":[{"id":"INV-18","version":3,"at":` + at(11) + `,"by":"piotr"}]},"now":"3475.00"}],"adds_up":true}`},
		{"the invoice's net, worked out from its own values", "Invoice", "INV-17", "net", 0, lineageDan,
			`{"class":"Invoice","id":"INV-17","variable":"net","version":2,"value":"1275.00","kind":"computed","formula":"ROUND(amount * rate, 2)",` +
				`"since":{"version":2,"at":` + filed + `,"by":"ann","by_kind":"person","event":"transition","name":"file"},` +
				`"variables":[{"variable":"amount","value":"300.00"},{"variable":"rate","value":"4.2500"}],"adds_up":true}`},
		{"the rate, from the row as it was, though the table has changed since", "Invoice", "INV-17", "rate", 0, lineageDan,
			`{"class":"Invoice","id":"INV-17","variable":"rate","version":2,"value":"4.2500","kind":"kept","formula":"LOOKUP(Rates, rate, currency = THIS.currency)",` +
				`"since":{"version":2,"at":` + filed + `,"by":"ann","by_kind":"person","event":"transition","name":"file"},` +
				`"variables":[{"variable":"currency","value":"EUR"}],` +
				`"lookups":[{"table":"Rates","column":"rate","value":"4.2500","count":1,"parts":[{"key":{"currency":"EUR"},"value":"4.2500",` +
				`"since":{"at":` + filed + `,"by":"tess","by_kind":"person","event":"import"},"until":{"at":` + at(10) + `,"by":"tess","by_kind":"person","event":"import"}}],` +
				`"more":false,"table_changed_since":true}],` +
				`"adds_up":true}`},
		{"the amount Ann typed", "Invoice", "INV-17", "amount", 0, lineageDan,
			`{"class":"Invoice","id":"INV-17","variable":"amount","version":2,"value":"300.00","kind":"typed",` +
				`"since":{"version":1,"at":` + filed + `,"by":"ann","by_kind":"person","event":"created"}}`},
		{"an invoice as it was at 03:00", "Invoice", "INV-18", "net", 2, lineageDan,
			`{"class":"Invoice","id":"INV-18","variable":"net","version":2,"value":"2000.00","kind":"computed","formula":"ROUND(amount * rate, 2)",` +
				`"since":{"version":2,"at":` + filed + `,"by":"piotr","by_kind":"person","event":"transition","name":"file"},` +
				`"variables":[{"variable":"amount","value":"2000.00"},{"variable":"rate","value":"1.0000"}],"adds_up":true}`},
		// Piotr reads only the invoices he wrote: the one he may not read
		// is counted and summed, never shown.
		{"the customer, for Piotr", "Customer", "Acme", "invoiced", 0, lineagePiotr,
			`{"class":"Customer","id":"Acme","variable":"invoiced","version":2,"value":"3275.00","kind":"kept","formula":"LOOKUP(Invoice, SUM(net), customer = REF() AND state \u003c\u003e \"draft\")",` +
				`"since":{"version":2,"at":` + at(3) + `,"by":"dan","by_kind":"person","event":"refresh"},` +
				`"lookups":[{"class":"Invoice","aggregate":"SUM","column":"net","value":"3275.00","count":2,` +
				`"parts":[{"id":"INV-18","version":2,"value":"2000.00"}],"more":false,"hidden":{"count":1,"value":"1275.00"},` +
				`"changed_since":{"count":1,"parts":[{"id":"INV-18","version":3,"at":` + at(11) + `,"by":"piotr"}]},"now":"3475.00"}],"adds_up":true}`},
		// A live value is the reader's, now: Piotr's invoices alone.
		{"a live total, for Piotr", "Customer", "Acme", "open_now", 0, lineagePiotr,
			`{"class":"Customer","id":"Acme","variable":"open_now","version":2,"value":"2200.00","kind":"live","formula":"LOOKUP(Invoice, SUM(net), customer = REF() AND state \u003c\u003e \"draft\")",` +
				`"lookups":[{"class":"Invoice","aggregate":"SUM","column":"net","value":"2200.00","count":1,"parts":[{"id":"INV-18","version":3,"value":"2200.00"}],"more":false}]}`},
	} {
		l, err := s.Lineage(ctx, c.class, c.id, c.variable, LineageAsk{Version: c.version}, c.as)
		if got := lineageJSON(t, l, err); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.what, got, c.want)
		}
	}
}

// What is refused (ADR-0099, part 2).
func TestWhereAValueComesFromRefused(t *testing.T) {
	s := lineageCase(t)
	ctx := context.Background()
	for _, c := range []struct {
		what, class, id, variable string
		ask                       LineageAsk
		as                        Option
		code                      string
	}{
		{"a variable the class has not", "Customer", "Acme", "invoiced_total", LineageAsk{}, lineageDan, CodeUnknownVariable},
		{"an invoice Piotr may not read", "Invoice", "INV-17", "net", LineageAsk{}, lineagePiotr, CodeUnknownDocument},
		{"a version the document has not", "Customer", "Acme", "invoiced", LineageAsk{Version: 9}, lineageDan, CodeUnknownDocument},
		{"the past of a live value", "Customer", "Acme", "open_now", LineageAsk{Version: 1}, lineageDan, CodeBadValue},
		{"too long a page", "Customer", "Acme", "invoiced", LineageAsk{Limit: MaxDocuments + 1}, lineageDan, CodeBadValue},
	} {
		_, err := s.Lineage(ctx, c.class, c.id, c.variable, c.ask, c.as)
		var e *Error
		if !errors.As(err, &e) || e.Code != c.code {
			t.Errorf("%s: %v, want %s", c.what, err, c.code)
		}
	}
	// A value that follows a schedule is worked out when its document is
	// made, too: Acme's first version kept 0.00, of no invoice, and both
	// invoices have changed since.
	l, err := s.Lineage(ctx, "Customer", "Acme", "invoiced", LineageAsk{Version: 1}, lineageDan)
	if got := lineageJSON(t, l, err); !strings.Contains(got, `"version":1,"value":"0.00","kind":"kept"`) || !strings.Contains(got, `"event":"created"`) ||
		!strings.Contains(got, `"count":0,"parts":[],"more":false,"changed_since":{"count":2,`) || !strings.Contains(got, `"now":"3475.00"`) {
		t.Errorf("Acme's first version: %s", got)
	}
}

// A content administrator who asks past the rules gives a reason, and
// the log of reads names the parts only the override showed (ADR-0067).
func TestWhereAValueComesFromPastTheRule(t *testing.T) {
	s := lineageCase(t)
	ctx := context.Background()
	cora := As(Principal{ID: "cora", System: []string{GroupContentAdministrators}})
	l, err := s.Lineage(ctx, "Customer", "Acme", "invoiced", LineageAsk{}, cora, Reason("the audit of December"))
	if got := lineageJSON(t, l, err); !strings.Contains(got, `"parts":[{"id":"INV-18","version":2,"value":"2000.00"},{"id":"INV-17","version":2,"value":"1275.00"}]`) {
		t.Errorf("cora's lineage: %s", got)
	}
	reads, err := s.Reads(ctx, ReadsFilter{}, As(Principal{ID: "root", System: []string{GroupAdministrators}}))
	if err != nil {
		t.Fatal(err)
	}
	var entry *Read
	for i := range reads {
		if reads[i].Command == "lineage" && reads[i].Class == "Invoice" {
			entry = &reads[i]
		}
	}
	if entry == nil || entry.By != "cora" || entry.Reason != "the audit of December" || strings.Join(entry.Documents, ",") != "INV-18,INV-17" {
		t.Errorf("the log of reads keeps %+v", entry)
	}
}

// A part that matched then and does not now is still a part: INV-18 was
// Acme's at 03:00, and Piotr moves it to Borg afterwards. Acme's kept
// total still adds up over the invoices as they were, and the lookup
// says it gives 1,275.00 now (ADR-0099, part 2).
func TestAPartThatNoLongerMatches(t *testing.T) {
	s := lineageCase(t)
	ctx := context.Background()
	if _, err := s.Set(ctx, "Invoice", "INV-18", Values{"customer": "Customer:Borg"}, lineagePiotr); err != nil {
		t.Fatal(err)
	}
	l, err := s.Lineage(ctx, "Customer", "Acme", "invoiced", LineageAsk{}, lineageDan)
	got := lineageJSON(t, l, err)
	for _, want := range []string{
		`"parts":[{"id":"INV-18","version":2,"value":"2000.00"},{"id":"INV-17","version":2,"value":"1275.00"}]`,
		`"changed_since":{"count":1,"parts":[{"id":"INV-18","version":4,`,
		`"now":"1275.00"`, `"adds_up":true`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Acme's total after INV-18 moved: %s\nwant %s in it", got, want)
		}
	}
}
