package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// The time zones, for a machine that has no database of them.
	_ "time/tzdata"
)

// A kept value that follows the clock (ADR-0093). Acme's invoices keep
// how many days each is late, worked out every night at three, by the
// clock of Warsaw: one in the morning by the engine's clock in summer,
// two in winter. Ola enters invoices, Anna is in accounting.
const lateInvoice = `{"name": "Invoice", "title": "\"Invoice \" & number",
 "variables": {
   "number":    {"type": "text", "required": true},
   "due":       {"type": "date", "required": true},
   "days_late": {"type": "number",
                 "formula": "MAX(DAYS(TODAY(), due), 0)",
                 "mode": "triggered", "on": ["enter:entered", "schedule:daily at 03:00"]},
   "reminder":  {"formula": "days_late >= 14"}
 },
 "states": {"initial": "entered", "list": ["entered", "disputed", "paid"], "final": ["paid"]},
 "transitions": [
   {"name": "dispute", "from": "entered", "to": "disputed"},
   {"name": "pay",     "from": ["entered", "disputed"], "to": "paid"}
 ],
 "rights": {
   "disputed": {"days_late": {"write": "FALSE"}}
 },
 "access": {
   "read":   "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
   "create": "INGROUP(\"everyone\")",
   "update": "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
   "delete": "FALSE",
   "transition": {"dispute": "INGROUP(\"ACCOUNTING\")", "pay": "INGROUP(\"ACCOUNTING\")"}
 }}`

// still is a clock a test sets.
type stillClock struct{ now time.Time }

func (c *stillClock) Now() time.Time { return c.now }

func utc(text string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", text)
	if err != nil {
		panic(err)
	}
	return t
}

// openLate opens a database in the zone at a moment, with the class.
func openLate(t *testing.T, path string, c *stillClock, zone string) *Store {
	t.Helper()
	s, err := Open(path, Options{Now: c.Now, TimeZone: zone, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// waiting are the rows of schedules that wait, by their rule.
func waiting(t *testing.T, s *Store, ctx context.Context) map[string]QueuedAction {
	t.Helper()
	rows, err := s.Outbox(ctx, OutboxFilter{Status: ActionPending}, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]QueuedAction{}
	for _, r := range rows {
		if r.Action == ActionFollow && strings.HasPrefix(r.Rule, "schedule:") {
			if _, twice := out[r.Class+" "+r.Rule]; twice {
				t.Errorf("two rows wait for %s of %s", r.Rule, r.Class)
			}
			out[r.Class+" "+r.Rule] = r
		}
	}
	return out
}

func TestSchedule_TheClockBringsTheValue(t *testing.T) {
	ctx := context.Background()
	c := &stillClock{now: utc("2026-11-26 10:00")}
	s := openLate(t, filepath.Join(t.TempDir(), "test.fado"), c, "Europe/Warsaw")
	if s.TimeZone() != "Europe/Warsaw" {
		t.Fatalf("the database's time zone: %s", s.TimeZone())
	}
	if _, err := s.DefineClass(ctx, []byte(lateInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	// The class has one schedule, and one row waits for its next moment:
	// three in the night of the 27th in Warsaw, which is two by the
	// engine's clock. The row names nobody.
	rows := waiting(t, s, ctx)
	row, ok := rows["Invoice schedule:daily at 03:00"]
	if !ok || len(rows) != 1 || row.DueAt != utc("2026-11-27 02:00").Unix() || row.PrincipalID != "" || row.DocID != "" ||
		string(row.Args) != `{"schedule":"daily at 03:00"}` {
		t.Fatalf("the row that waits for the schedule: %+v", rows)
	}

	make := func(number, due, state string) *Document {
		t.Helper()
		doc, err := s.Create(ctx, "Invoice", Values{"number": number, "due": due}, As(followOla))
		if err != nil {
			t.Fatal(err)
		}
		if state == "disputed" || state == "paid" {
			name := map[string]string{"disputed": "dispute", "paid": "pay"}[state]
			if doc, err = s.Transition(ctx, "Invoice", doc.ID, name, nil, As(followAnna)); err != nil {
				t.Fatal(err)
			}
		}
		return doc
	}
	late := make("FV-1", "2026-11-20", "entered")
	early := make("FV-2", "2026-12-10", "entered")
	disputed := make("FV-3", "2026-11-20", "disputed")
	paid := make("FV-4", "2026-11-20", "paid")
	if late.Values["days_late"] != 6.0 || early.Values["days_late"] != 0.0 {
		t.Fatalf("the invoices as entered: %v, %v", late.Values, early.Values)
	}

	// Before its moment the row does not run.
	c.now = utc("2026-11-27 01:59")
	if n := drained(t, s, ctx); n != 0 {
		t.Fatalf("the outbox ran %d rows before the moment of the schedule", n)
	}
	c.now = utc("2026-11-27 02:00")
	if n := drained(t, s, ctx); n != 1 {
		t.Fatalf("the outbox ran %d rows at the moment of the schedule", n)
	}
	doc, err := s.Get(ctx, "Invoice", late.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["days_late"] != 7.0 || doc.Version != late.Version+1 {
		t.Errorf("the late invoice after the night: %v, version %d", doc.Values, doc.Version)
	}
	// Nobody changed it: the engine did.
	if doc.UpdatedBy != "" || doc.CreatedBy != "ola" {
		t.Errorf("the invoice says it was made by %q and last changed by %q", doc.CreatedBy, doc.UpdatedBy)
	}
	// The engine itself brought it: the event names the schedule and no
	// actor.
	events, err := s.Events(ctx, "Invoice", late.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if e := events[len(events)-1]; e.Kind != EventRefresh || e.Name != "schedule:daily at 03:00" || e.ActorID != "" || e.AfterVersion != doc.Version {
		t.Errorf("the event of the invoice the schedule brought: %+v", e)
	}
	// An invoice that is not late yet comes out the same and gets no
	// version; the class closes the value in disputed; a paid invoice is
	// finished.
	for _, was := range []*Document{early, disputed, paid} {
		got, err := s.Get(ctx, "Invoice", was.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if got.Version != was.Version || got.Values["days_late"] != was.Values["days_late"] {
			t.Errorf("%v in %s changed: %v, version %d", was.Values["number"], was.State, got.Values, got.Version)
		}
	}
	// The row is done, and the next waits for the night after.
	rows = waiting(t, s, ctx)
	if next := rows["Invoice schedule:daily at 03:00"]; len(rows) != 1 || next.Key == row.Key || next.DueAt != utc("2026-11-28 02:00").Unix() {
		t.Fatalf("the row of the next night: %+v", rows)
	}

	// The database is not asked for a week: the schedule runs once, for
	// today, and not once for each night it missed.
	c.now = utc("2026-12-05 09:00")
	if n := drained(t, s, ctx); n != 1 {
		t.Fatalf("the outbox ran %d rows after a week", n)
	}
	if doc, _ := s.Get(ctx, "Invoice", late.ID, As(followAnna)); doc.Values["days_late"] != 15.0 || doc.Values["reminder"] != true || doc.Version != late.Version+2 {
		t.Errorf("the late invoice after a week: %v, version %d", doc.Values, doc.Version)
	}
	rows = waiting(t, s, ctx)
	if next := rows["Invoice schedule:daily at 03:00"]; len(rows) != 1 || next.DueAt != utc("2026-12-06 02:00").Unix() {
		t.Fatalf("the row after a week: %+v", rows)
	}
}

// The zone is the database's: it is kept, a program that names another
// replaces it, and the rows that wait are timed again.
func TestSchedule_TheTimeZoneOfTheDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.fado")
	c := &stillClock{now: utc("2026-07-10 10:00")}

	// A database that names no zone is in UTC.
	s := openLate(t, path, c, "")
	if s.TimeZone() != "UTC" {
		t.Fatalf("the zone of a database that names none: %s", s.TimeZone())
	}
	if _, err := s.DefineClass(ctx, []byte(lateInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	if row := waiting(t, s, ctx)["Invoice schedule:daily at 03:00"]; row.DueAt != utc("2026-07-11 03:00").Unix() {
		t.Fatalf("three in the night in UTC: %+v", row)
	}
	s.Close()

	// Warsaw in summer is two hours ahead: three in the night is one.
	s = openLate(t, path, c, "Europe/Warsaw")
	if row := waiting(t, s, ctx)["Invoice schedule:daily at 03:00"]; s.TimeZone() != "Europe/Warsaw" || row.DueAt != utc("2026-07-11 01:00").Unix() {
		t.Fatalf("three in the night in Warsaw, in summer: %s, %+v", s.TimeZone(), row)
	}
	s.Close()

	// A program that names no zone keeps the database's.
	s = openLate(t, path, c, "")
	if row := waiting(t, s, ctx)["Invoice schedule:daily at 03:00"]; s.TimeZone() != "Europe/Warsaw" || row.DueAt != utc("2026-07-11 01:00").Unix() {
		t.Fatalf("the zone the database kept: %s, %+v", s.TimeZone(), row)
	}
	s.Close()

	// A zone nobody knows is refused, and the database keeps its own.
	if _, err := Open(path, Options{Now: c.Now, TimeZone: "Europe/Atlantis", Outbox: OutboxOptions{Manual: true}}); code(err) != CodeBadValue {
		t.Errorf("a zone nobody knows: %v", err)
	}
	if _, err := Open(path, Options{Now: c.Now, TimeZone: "Local", Outbox: OutboxOptions{Manual: true}}); code(err) != CodeBadValue {
		t.Errorf("the zone of the machine, which is no zone of the database: %v", err)
	}
	s = openLate(t, path, c, "")
	if s.TimeZone() != "Europe/Warsaw" {
		t.Errorf("the zone after a refused one: %s", s.TimeZone())
	}
}

// A class that is defined again: a schedule it no longer has ends with
// the row that waited, one it has anew gets a row, and two variables on
// one schedule share one.
func TestSchedule_AClassIsDefinedAgain(t *testing.T) {
	ctx := context.Background()
	c := &stillClock{now: utc("2026-11-26 10:00")}
	s := openLate(t, filepath.Join(t.TempDir(), "test.fado"), c, "")
	both := strings.Replace(lateInvoice, `"reminder":`, `"day": {"type": "number", "formula": "DAY(TODAY())", "mode": "triggered", "on": ["schedule:daily at 03:00", "schedule:weekly"]},
   "reminder":`, 1)
	if _, err := s.DefineClass(ctx, []byte(both), asSetup); err != nil {
		t.Fatal(err)
	}
	rows := waiting(t, s, ctx)
	if len(rows) != 2 || rows["Invoice schedule:daily at 03:00"].DueAt != utc("2026-11-27 03:00").Unix() || rows["Invoice schedule:weekly"].DueAt != utc("2026-11-30 00:00").Unix() {
		t.Fatalf("the rows of two schedules: %+v", rows)
	}
	doc, err := s.Create(ctx, "Invoice", Values{"number": "FV-1", "due": "2026-11-20"}, As(followOla))
	if err != nil {
		t.Fatal(err)
	}
	// A value that follows the clock is there from the first moment: the
	// day of the month lists no entry, and the new invoice has it.
	if doc.Values["day"] != 26.0 || doc.Computed["day"].Version != 1 {
		t.Fatalf("the new invoice: %v, %+v", doc.Values, doc.Computed)
	}
	// Both variables of the nightly schedule are brought in one version.
	c.now = utc("2026-11-27 03:00")
	drained(t, s, ctx)
	got, err := s.Get(ctx, "Invoice", doc.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != doc.Version+1 || got.Values["days_late"] != 7.0 || got.Values["day"] != 27.0 || got.Computed["day"].Version != got.Version || got.Computed["days_late"].Version != got.Version {
		t.Errorf("the invoice after the night: %v, version %d, %+v", got.Values, got.Version, got.Computed)
	}

	// The class drops the weekly schedule and moves the nightly one.
	moved := strings.NewReplacer(`"schedule:daily at 03:00", "schedule:weekly"`, `"schedule:daily at 05:30"`,
		`"schedule:daily at 03:00"`, `"schedule:daily at 05:30"`).Replace(both)
	if _, err := s.DefineClass(ctx, []byte(moved), asSetup); err != nil {
		t.Fatal(err)
	}
	rows = waiting(t, s, ctx)
	if len(rows) != 3 || rows["Invoice schedule:daily at 05:30"].DueAt != utc("2026-11-27 05:30").Unix() {
		t.Fatalf("the rows after the class was defined again: %+v", rows)
	}
	// The rows of the schedules it no longer has run at their moments,
	// bring nothing and are followed by none.
	c.now = utc("2026-11-30 00:00")
	drained(t, s, ctx)
	rows = waiting(t, s, ctx)
	if len(rows) != 1 || rows["Invoice schedule:daily at 05:30"].DueAt != utc("2026-11-30 05:30").Unix() {
		t.Fatalf("the rows after the old schedules ran out: %+v", rows)
	}
	if got, _ := s.Get(ctx, "Invoice", doc.ID, As(followAnna)); got.Values["days_late"] != 10.0 {
		t.Errorf("the invoice, brought by the schedule the class has: %v", got.Values)
	}
}

// A schedule that waits holds nothing back: a table the same class
// follows brings its values at once.
func TestSchedule_AWaitingScheduleHoldsNothingBack(t *testing.T) {
	ctx := context.Background()
	class := strings.Replace(followInvoice, `"on": ["enter:entered", "table:Rates"]`, `"on": ["enter:entered", "table:Rates", "schedule:monthly on last day at 23:00"]`, 1)
	s, _ := openFollow(t, class)
	if rows := waiting(t, s, ctx); len(rows) != 1 {
		t.Fatalf("the row of the schedule: %+v", rows)
	}
	inv := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if n := drained(t, s, ctx); n != 1 {
		t.Fatalf("the outbox ran %d rows", n)
	}
	if doc, _ := s.Get(ctx, "Invoice", inv.ID, As(followAnna)); doc.Values["rate"] != "4.3100" {
		t.Errorf("the invoice after the table changed, with a schedule waiting: %v", doc.Values)
	}
	if rows := waiting(t, s, ctx); len(rows) != 1 {
		t.Errorf("the row of the schedule after the table's follow ran: %+v", rows)
	}
}
