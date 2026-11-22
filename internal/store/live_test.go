package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// An invoice with three live variables (ADR-0076): how long it has
// waited, whether the reader wrote it, and one whose formula fails for
// a number of four letters. A kept computed variable stands beside
// them.
const liveInvoice = `{
 "name": "Invoice",
 "title": "number",
 "variables": {
   "number":       {"type": "text", "required": true},
   "owner":        {"type": "text", "default": "USER()"},
   "net":          {"type": "money(2)"},
   "waiting_days": {"label": "Days waiting", "mode": "live", "formula": "DAYS(TODAY(), DATEOF(ENTEREDAT()))"},
   "mine":         {"type": "bool", "mode": "live", "formula": "owner = USER()"},
   "odd":          {"type": "number", "mode": "live", "formula": "1 / (LEN(number) - 4)"},
   "shout":        {"formula": "UPPER(number)"}
 },
 "states": {"initial": "review", "list": ["review", "paid"], "final": ["paid"]},
 "transitions": [{"name": "pay", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "TRUE", "update": "owner = USER()", "delete": "FALSE"},
 "rights": {"review": {"mine": {"read": ["sales"]}}}
}`

var (
	liveJan   = Principal{ID: "jan", Groups: []string{"sales"}}
	livePiotr = Principal{ID: "piotr", Groups: []string{"sales"}}
	liveEwa   = Principal{ID: "ewa", Groups: []string{"finance"}}
	liveRoot  = Principal{ID: "root", System: []string{GroupContentAdministrators}}
)

func openLive(t *testing.T) (*Store, context.Context, *clock) {
	t.Helper()
	cl := &clock{now: start}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(liveInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, ctx, cl
}

// A live value is worked out when it is read, for the reader, and is
// right at every moment though no commit writes it.
func TestLive_WorkedOutWhenRead(t *testing.T) {
	s, ctx, _ := openLive(t)
	doc, err := s.Create(ctx, "Invoice", Values{"number": "FV-17", "net": "100.00 PLN"}, As(liveJan), WithID("FV-17"))
	if err != nil {
		t.Fatal(err)
	}
	// Kept nowhere: the document does not carry it, on any read.
	for _, name := range []string{"waiting_days", "mine", "odd"} {
		if has(doc, name) {
			t.Errorf("the created document carries %s; a live value is kept nowhere", name)
		}
	}
	got, err := s.Get(ctx, "Invoice", "FV-17", As(liveJan))
	if err != nil || has(got, "waiting_days") || has(got, "mine") || !has(got, "shout") {
		t.Fatalf("get = %v, %v; want the kept values and no live one", got, err)
	}
	found, err := s.Query(ctx, Filter{Class: "Invoice"}, As(liveJan))
	if err != nil || len(found) != 1 || has(found[0], "waiting_days") || has(found[0], "mine") {
		t.Fatalf("query = %v, %v; want the document with no live value", found, err)
	}

	// For the reader: Jan wrote it, Ewa did not.
	for _, tc := range []struct {
		who  Principal
		name string
		want any
	}{{liveJan, "mine", true}, {liveJan, "waiting_days", float64(0)}, {liveEwa, "waiting_days", float64(0)}} {
		v, err := s.Live(ctx, "Invoice", "FV-17", tc.name, As(tc.who))
		if err != nil || v.Value != tc.want {
			t.Errorf("live %s for %s = %v, %v; want %v", tc.name, tc.who.ID, v, err, tc.want)
		}
	}
	// At every moment: nine days later it says nine, and the document is
	// still at its first version, for nothing was written.
	later := start.Add(9 * 24 * time.Hour)
	if v, err := s.Live(ctx, "Invoice", "FV-17", "waiting_days", As(liveJan), At(later)); err != nil || v.Value != float64(9) {
		t.Errorf("live waiting_days nine days later = %v, %v; want 9", v, err)
	}
	if after, err := s.Get(ctx, "Invoice", "FV-17", As(liveJan)); err != nil || after.Version != 1 {
		t.Errorf("the document after the reads = %v, %v; want it at version 1", after, err)
	}
	if versions, err := s.History(ctx, "Invoice", "FV-17", As(liveJan)); err != nil || len(versions) != 1 || has(&versions[0].Document, "waiting_days") {
		t.Errorf("history = %v, %v; want one version with no live value", versions, err)
	}
	// The name is taken in any case of letters, as a variable is.
	if v, err := s.Live(ctx, "Invoice", "FV-17", "MINE", As(liveJan)); err != nil || v.Value != true {
		t.Errorf("live MINE = %v, %v; want true", v, err)
	}
}

// Who may: whoever may read the document, and the variable by its read
// right. A content administrator reads past either only with a reason,
// and the log of reads keeps it.
func TestLive_WithTheReadersRights(t *testing.T) {
	s, ctx, _ := openLive(t)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-17"}, As(liveJan), WithID("FV-17")); err != nil {
		t.Fatal(err)
	}
	refused := func(what string, err error, code, variable string) *Error {
		t.Helper()
		e, ok := err.(*Error)
		if !ok || e.Code != code || e.Variable != variable {
			t.Fatalf("%s: got %v, want %s about %q", what, err, code, variable)
		}
		return e
	}
	// Piotr may not read Jan's invoice: for him it is not there.
	_, err := s.Live(ctx, "Invoice", "FV-17", "waiting_days", As(livePiotr))
	refused("a document the caller may not read", err, CodeUnknownDocument, "")
	_, err = s.Live(ctx, "Invoice", "FV-99", "waiting_days", As(liveJan))
	refused("a document that is not there", err, CodeUnknownDocument, "")
	// Ewa reads the invoice, and in review the variable mine is for sales.
	_, err = s.Live(ctx, "Invoice", "FV-17", "mine", As(liveEwa))
	if e := refused("a variable the caller may not read", err, CodeForbidden, "mine"); !strings.Contains(e.Hint, "review / mine") {
		t.Errorf("the refusal's hint = %q, want the cell of the right", e.Hint)
	}
	// What is no live variable, and what is no variable.
	_, err = s.Live(ctx, "Invoice", "FV-17", "shout", As(liveJan))
	refused("a kept variable", err, CodeNotAllowed, "shout")
	_, err = s.Live(ctx, "Invoice", "FV-17", "nothing", As(liveJan))
	refused("an unknown variable", err, CodeUnknownVariable, "nothing")
	// A formula that fails says so, with the variable.
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-1"}, As(liveJan), WithID("FV-1")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Live(ctx, "Invoice", "FV-1", "odd", As(liveJan)); err == nil || err.(*Error).Variable != "odd" {
		t.Errorf("a failing formula = %v, want an error about odd", err)
	}

	// A content administrator is nobody's exception without a reason.
	_, err = s.Live(ctx, "Invoice", "FV-17", "waiting_days", As(liveRoot))
	refused("a content administrator without a reason", err, CodeReasonRequired, "")
	if v, err := s.Live(ctx, "Invoice", "FV-17", "mine", As(liveRoot), Reason("audit of November")); err != nil || v.Value != false {
		t.Fatalf("a content administrator with a reason = %v, %v; want false", v, err)
	}
	reads, err := s.Reads(ctx, ReadsFilter{}, As(Principal{ID: "adm", System: []string{GroupAdministrators}}))
	if err != nil || len(reads) != 1 {
		t.Fatalf("the log of reads = %v, %v; want the one read past the rules", reads, err)
	}
	if r := reads[0]; r.Command != "live" || r.By != "root" || r.Reason != "audit of November" || strings.Join(r.Documents, ",") != "FV-17" ||
		strings.Join(r.Rules, ",") != "/access/read,/rights/review/mine/read" {
		t.Errorf("the entry = %+v; want live by root on FV-17 past the read rule and the read right of mine", r)
	}
}

// A class may turn a kept variable into a live one, and a live one into
// a kept one, with a new version of its definition. A document is what
// the version that last computed it made of it (ADR-0039): one made
// before the change still carries the value that was kept then, until a
// commit brings it to the current version; from then on, and in every
// document made since, the value is worked out when it is read.
func TestLive_AVariableChangesItsMode(t *testing.T) {
	s, ctx, _ := openLive(t)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-17"}, As(liveJan), WithID("FV-17")); err != nil {
		t.Fatal(err)
	}
	// shout was kept; now it is live, and mine, which was live, is kept.
	next := strings.NewReplacer(
		`"shout":        {"formula": "UPPER(number)"}`, `"shout":        {"mode": "live", "formula": "UPPER(number)"}`,
		`"mine":         {"type": "bool", "mode": "live", "formula": "owner = USER()"}`, `"mine":         {"type": "bool", "mode": "triggered", "on": ["refresh"], "formula": "owner = USER()"}`,
	).Replace(liveInvoice)
	if next == liveInvoice {
		t.Fatal("the next version of the class is the first")
	}
	if _, err := s.DefineClass(ctx, []byte(next), asSetup); err != nil {
		t.Fatalf("the next version of the class: %v", err)
	}
	// The document of the first version, untouched: as that version left it.
	got, err := s.Get(ctx, "Invoice", "FV-17", As(liveJan))
	if err != nil || got.ClassVersion != 1 || got.Values["shout"] != "FV-17" || has(got, "waiting_days") {
		t.Fatalf("get after the change = %v, %v; want the document as version 1 computed it", got, err)
	}
	// The call asks the class as it is now.
	if v, err := s.Live(ctx, "Invoice", "FV-17", "shout", As(liveJan)); err != nil || v.Value != "FV-17" {
		t.Errorf("live shout = %v, %v; want FV-17", v, err)
	}
	if _, err := s.Live(ctx, "Invoice", "FV-17", "mine", As(liveJan)); err == nil || err.(*Error).Code != CodeNotAllowed {
		t.Errorf("live mine = %v, want not_allowed: it is kept now", err)
	}
	// A commit brings the document to the current version: shout is no
	// longer carried, and the live read answers what the document says now.
	set, err := s.Set(ctx, "Invoice", "FV-17", Values{"number": "fv-17a"}, As(liveJan))
	if err != nil || set.ClassVersion != 2 || has(set, "shout") {
		t.Fatalf("set after the change = %v, %v; want the document at version 2 of the class, without shout", set, err)
	}
	if v, err := s.Live(ctx, "Invoice", "FV-17", "shout", As(liveJan)); err != nil || v.Value != "FV-17A" {
		t.Errorf("live shout after the set = %v, %v; want FV-17A", v, err)
	}
	// A document made under the new version carries no shout from the start.
	made, err := s.Create(ctx, "Invoice", Values{"number": "FV-18"}, As(liveJan), WithID("FV-18"))
	if err != nil || has(made, "shout") {
		t.Fatalf("create after the change = %v, %v; want a document without shout", made, err)
	}
	if v, err := s.Live(ctx, "Invoice", "FV-18", "shout", As(liveJan)); err != nil || v.Value != "FV-18" {
		t.Errorf("live shout of a new document = %v, %v; want FV-18", v, err)
	}
	if found, err := s.Query(ctx, Filter{Class: "Invoice"}, As(liveJan)); err != nil || len(found) != 2 || has(found[0], "shout") || has(found[1], "shout") {
		t.Errorf("query after the change = %v, %v; want two documents without shout", found, err)
	}
	// The history keeps what each version of the document was.
	if versions, err := s.History(ctx, "Invoice", "FV-17", As(liveJan)); err != nil || len(versions) != 2 || versions[0].Values["shout"] != "FV-17" || has(&versions[1].Document, "shout") {
		t.Errorf("history after the change = %v, %v; want the first version with shout and the second without", versions, err)
	}
}
