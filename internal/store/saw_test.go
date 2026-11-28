package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// flat is a value as JSON on one line.
func flat(x any) string {
	b, _ := json.Marshal(x)
	return string(b)
}

// The case of ADR-0078: Tess keeps the limits, Ola writes purchases,
// Tomek approves them, and Piotr has no part in it.
const sawLimits = `{
 "name": "Limits",
 "columns": {"grade": "text", "limit": "decimal(2)"},
 "key": ["grade"],
 "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}`

const sawPurchase = `{
 "name": "Purchase",
 "title": "\"Purchase \" & number",
 "variables": {
   "number": {"type": "text", "required": true},
   "amount": {"type": "decimal", "scale": 2, "required": true},
   "grade":  {"type": "text", "required": true},
   "note":   {"type": "text", "default": "\"none\""},
   "limit":  {"type": "decimal", "scale": 2,
              "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
              "mode": "triggered", "on": ["enter:review", "refresh"]}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "booked"], "final": ["booked"]},
 "transitions": [
   {"name": "file",    "from": "draft",  "to": "review"},
   {"name": "return",  "from": "review", "to": "draft"},
   {"name": "approve", "from": "review", "to": "approved", "guard": "NOT ISBLANK(limit) AND amount <= limit"},
   {"from": "approved", "to": "booked", "guard": "amount < 100"}
 ],
 "access": {
   "read":   "INGROUP(\"everyone\") AND (grade <> \"S\" OR INGROUP(\"managers\"))",
   "create": "INGROUP(\"everyone\")",
   "update": "CREATEDBY() = USER()",
   "delete": "CREATEDBY() = USER()",
   "transition": {
     "file":    "CREATEDBY() = USER()",
     "return":  "INGROUP(\"managers\")",
     "approve": "INGROUP(\"managers\")"
   }
 }
}`

var (
	sawOla   = As(Principal{ID: "ola", Kind: ActorPerson})
	sawTomek = As(Principal{ID: "tomek", Kind: ActorPerson, Groups: []string{"managers"}})
	sawPiotr = As(Principal{ID: "piotr", Kind: ActorPerson})
	sawTess  = As(Principal{ID: "tess", Kind: ActorPerson, Groups: []string{"treasury"}})
)

// sawStore is a database with the limits and the class of purchases.
func sawStore(t *testing.T) *Store {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.DefineTable(ctx, []byte(sawLimits), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Limits", []Values{{"grade": "B", "limit": "10000.00"}, {"grade": "S", "limit": "500.00"}}, sawTess); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(sawPurchase), asSetup); err != nil {
		t.Fatal(err)
	}
	return s
}

// sawFiled is purchase P-7 of 9,000.00, filed by Ola: version 2.
func sawFiled(t *testing.T, s *Store, id, amount string) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Create(ctx, "Purchase", Values{"number": id, "amount": amount, "grade": "B"}, sawOla, WithID(id)); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Transition(ctx, "Purchase", id, "file", nil, sawOla); err != nil || d.Version != 2 {
		t.Fatalf("file: %v, %v", d, err)
	}
}

// stale wants a refusal with stale_version that names both versions, and
// the document left at the version it was at.
func stale(t *testing.T, s *Store, what string, err error, id string, at, saw int64) {
	t.Helper()
	if code(err) != CodeStaleVersion {
		t.Fatalf("%s: %v; want stale_version", what, err)
	}
	want := "Purchase " + id + " is at version " + itoa(int(at)) + ", and the command saw version " + itoa(int(saw))
	if e := err.(*Error); e.Message != want || e.Hint == "" {
		t.Errorf("%s: message %q, hint %q; want %q and a hint", what, e.Message, e.Hint, want)
	}
	d, gerr := s.Get(t.Context(), "Purchase", id, sawTomek)
	if gerr != nil || d.Version != at {
		t.Errorf("%s: the document is at %v, %v; want version %d", what, d, gerr, at)
	}
}

// The steps of the record: Tomek decides on version 2, the purchase moves
// on to version 3, and his approval of what he saw is refused; he reads
// version 3 and approves that, and the event says which version he saw.
func TestSaw_ACommandSaysWhichVersionItSaw(t *testing.T) {
	s := sawStore(t)
	ctx := t.Context()
	sawFiled(t, s, "P-7", "9000.00")
	seenByTomek, err := s.Get(ctx, "Purchase", "P-7", sawTomek)
	if err != nil || seenByTomek.Version != 2 || seenByTomek.Values["limit"] != "10000.00" {
		t.Fatalf("Tomek reads: %v, %v", seenByTomek, err)
	}
	if d, err := s.Set(ctx, "Purchase", "P-7", Values{"amount": "9900.00"}, sawOla); err != nil || d.Version != 3 {
		t.Fatalf("Ola corrects: %v, %v", d, err)
	}
	before, _ := s.Events(ctx, "Purchase", "P-7", sawTomek)
	_, err = s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(seenByTomek.Version))
	stale(t, s, "approve on version 2", err, "P-7", 3, 2)
	// A preview answers the refusal the command would get.
	_, err = s.Preview(ctx, "Purchase", "P-7", nil, "approve", sawTomek, Saw(2))
	stale(t, s, "preview of approve on version 2", err, "P-7", 3, 2)
	// A version the document never had is refused the same way.
	_, err = s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(99))
	stale(t, s, "approve on version 99", err, "P-7", 3, 99)
	if after, _ := s.Events(ctx, "Purchase", "P-7", sawTomek); len(after) != len(before) {
		t.Errorf("a refused command wrote %d events", len(after)-len(before))
	}
	// He reads it again, and approves what he read.
	if p, err := s.Preview(ctx, "Purchase", "P-7", nil, "approve", sawTomek, Saw(3)); err != nil || p.Document.State != "approved" {
		t.Fatalf("preview of approve on version 3: %v, %v", p, err)
	}
	d, err := s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(3), Key("approve-P-7"))
	if err != nil || d.Version != 4 || d.State != "approved" {
		t.Fatalf("approve on version 3: %v, %v", d, err)
	}
	// Sent again with its key, the command is answered as the first was:
	// it is not refused by its own success.
	again, err := s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(3), Key("approve-P-7"))
	if err != nil || again.Version != 4 {
		t.Fatalf("approve sent again: %v, %v", again, err)
	}
	events, err := s.Events(ctx, "Purchase", "P-7", sawTomek)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int64{}
	for _, ev := range events {
		seen[ev.Kind+" "+ev.Name] = ev.SeenVersion
	}
	// Ola named no version: her events say none. Tomek's says 3.
	if len(events) != 4 || seen["created "] != 0 || seen["transition file"] != 0 || seen["set "] != 0 || seen["transition approve"] != 3 {
		t.Errorf("seen versions: %v of %d events", seen, len(events))
	}
	// The view of the events shows it to an analyst.
	rows, err := s.SQL(ctx, `SELECT "name", "before_version", "seen_version" FROM "Purchase_events" WHERE "doc_id" = ? AND "kind" = 'transition' ORDER BY "after_version"`, []any{"P-7"},
		As(Principal{ID: "marek", System: []string{GroupAnalysts}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := flat(rows.Rows); got != `[["file",1,null],["approve",3,3]]` {
		t.Errorf("the view of events: %s", got)
	}
}

// Every command that changes one stored document takes the version: a
// set, a reset, a refresh and a delete beside a transition.
func TestSaw_EveryCommandOnOneDocument(t *testing.T) {
	s := sawStore(t)
	ctx := t.Context()
	sawFiled(t, s, "P-8", "50.00")
	_, err := s.Set(ctx, "Purchase", "P-8", Values{"note": "urgent"}, sawOla, Saw(1))
	stale(t, s, "set", err, "P-8", 2, 1)
	if d, err := s.Set(ctx, "Purchase", "P-8", Values{"note": "urgent"}, sawOla, Saw(2)); err != nil || d.Version != 3 {
		t.Fatalf("set on version 2: %v, %v", d, err)
	}
	_, err = s.Reset(ctx, "Purchase", "P-8", []string{"note"}, sawOla, Saw(2))
	stale(t, s, "reset", err, "P-8", 3, 2)
	if d, err := s.Reset(ctx, "Purchase", "P-8", []string{"note"}, sawOla, Saw(3)); err != nil || d.Version != 4 || d.Values["note"] != "none" {
		t.Fatalf("reset on version 3: %v, %v", d, err)
	}
	_, err = s.Refresh(ctx, "Purchase", "P-8", nil, sawTomek, Saw(3))
	stale(t, s, "refresh", err, "P-8", 4, 3)
	if d, err := s.Refresh(ctx, "Purchase", "P-8", nil, sawTomek, Saw(4)); err != nil || d.Version != 5 {
		t.Fatalf("refresh on version 4: %v, %v", d, err)
	}
	_, err = s.PreviewDelete(ctx, "Purchase", "P-8", sawOla, Saw(4))
	stale(t, s, "preview of delete", err, "P-8", 5, 4)
	err = s.Delete(ctx, "Purchase", "P-8", sawOla, Saw(4))
	stale(t, s, "delete", err, "P-8", 5, 4)
	if err := s.Delete(ctx, "Purchase", "P-8", sawOla, Saw(5)); err != nil {
		t.Fatalf("delete on version 5: %v", err)
	}
	events, err := s.Events(ctx, "Purchase", "P-8", As(Principal{ID: "olga", System: []string{GroupContentAdministrators}}), Reason("the test reads the log of a removed purchase"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range events {
		got = append(got, ev.Kind+":"+itoa(int(ev.BeforeVersion))+":"+itoa(int(ev.SeenVersion)))
	}
	if want := "created:0:0 transition:1:0 set:2:2 reset:3:3 refresh:4:4 deleted:5:5"; strings.Join(got, " ") != want {
		t.Errorf("events as kind:before:seen = %v; want %s", got, want)
	}
	// A new document has no version anybody read.
	if _, err := s.Create(ctx, "Purchase", Values{"number": "P-9", "amount": "1.00", "grade": "B"}, sawOla, Saw(1)); code(err) != CodeNotAllowed {
		t.Errorf("a create that names a version: %v; want not_allowed", err)
	}
}

// A caller who may not see the document learns nothing from the version:
// the document does not exist for them, whatever version they name.
func TestSaw_ACallerWhoMayNotReadLearnsNothing(t *testing.T) {
	s := sawStore(t)
	ctx := t.Context()
	if _, err := s.Create(ctx, "Purchase", Values{"number": "S-1", "amount": "10.00", "grade": "S"}, sawTomek, WithID("S-1")); err != nil {
		t.Fatal(err)
	}
	for _, saw := range []int64{1, 2, 99} {
		if _, err := s.Set(ctx, "Purchase", "S-1", Values{"note": "x"}, sawPiotr, Saw(saw)); code(err) != CodeUnknownDocument {
			t.Errorf("Piotr sets a purchase he may not see, naming version %d: %v; want unknown_document", saw, err)
		}
	}
}

// The command's own event keeps the version; the move the document then
// makes by itself is the engine's and names none.
func TestSaw_TheChainNamesNoVersion(t *testing.T) {
	s := sawStore(t)
	ctx := t.Context()
	sawFiled(t, s, "P-5", "50.00")
	d, err := s.Transition(ctx, "Purchase", "P-5", "approve", nil, sawTomek, Saw(2))
	if err != nil || d.State != "booked" || d.Version != 4 {
		t.Fatalf("approve, and booked by itself: %v, %v", d, err)
	}
	events, err := s.Events(ctx, "Purchase", "P-5", sawTomek)
	if err != nil || len(events) != 4 {
		t.Fatalf("events: %d, %v", len(events), err)
	}
	if approve, booked := events[2], events[3]; approve.Name != "approve" || approve.SeenVersion != 2 || booked.ToState != "booked" || booked.SeenVersion != 0 {
		t.Errorf("approve saw %d, the move by itself %d; want 2 and none", approve.SeenVersion, booked.SeenVersion)
	}
}

// A batch of moves names a version for each document: the one that moved
// on is reported with stale_version, and the others move (ADR-0048).
func TestSaw_ManyDocuments(t *testing.T) {
	s := sawStore(t)
	ctx := t.Context()
	for _, id := range []string{"A", "B", "C"} {
		sawFiled(t, s, id, "5000.00")
	}
	if _, err := s.Set(ctx, "Purchase", "B", Values{"note": "changed"}, sawOla); err != nil {
		t.Fatal(err)
	}
	ids := []string{"A", "B", "C"}
	pre, err := s.PreviewMany(ctx, "Purchase", "approve", ids, nil, sawTomek, SawEach([]int64{2, 2, 0}))
	if err != nil || !pre[0].Moved() || pre[1].Refusal == nil || pre[1].Refusal.Code != CodeStaleVersion || !pre[2].Moved() {
		t.Fatalf("the preview of the batch: %s, %v", dump(pre), err)
	}
	out, err := s.TransitionMany(ctx, "Purchase", "approve", ids, nil, sawTomek, SawEach([]int64{2, 2, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if !out[0].Moved() || !out[2].Moved() {
		t.Errorf("A and C: %s", dump(out))
	}
	if r := out[1].Refusal; r == nil || r.Code != CodeStaleVersion || r.Message != "Purchase B is at version 3, and the command saw version 2" {
		t.Errorf("B: %s", dump(out[1]))
	}
	seen := func(id string) int64 {
		events, err := s.Events(ctx, "Purchase", id, sawTomek)
		if err != nil {
			t.Fatal(err)
		}
		return events[len(events)-1].SeenVersion
	}
	// A named its version, C named none, and B did not move.
	if a, b, c := seen("A"), seen("B"), seen("C"); a != 2 || b != 0 || c != 0 {
		t.Errorf("seen versions of A, B, C: %d, %d, %d; want 2, 0, 0", a, b, c)
	}
	if _, err := s.TransitionMany(ctx, "Purchase", "approve", ids, nil, sawTomek, SawEach([]int64{2, 2})); code(err) != CodeBadValue {
		t.Errorf("two versions for three documents: %v; want bad_value", err)
	}
	if _, err := s.TransitionMany(ctx, "Purchase", "approve", ids, nil, sawTomek, Saw(2)); code(err) != CodeBadValue {
		t.Errorf("one version for a batch: %v; want bad_value", err)
	}
}

// A database of schema 13, the one v0.7.0 made, is brought to the current
// schema when it is opened: its events gain the column, the view of each
// class shows it, and what the database held is as it was. An earlier
// schema is refused, as before.
func TestSaw_ADatabaseOfSchema13IsBroughtUp(t *testing.T) {
	s := sawStore(t)
	ctx := context.Background()
	sawFiled(t, s, "P-7", "9000.00")
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Make the file what v0.7.0 left: no column, the view without it,
	// no seal on the events (ADR-0103), and the number 13.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(eventsViewDDL("Purchase"), `, e."seen_version"`, "", 1)
	if old == eventsViewDDL("Purchase") {
		t.Fatal("the view of events does not name seen_version")
	}
	for _, stmt := range []string{`DROP VIEW "Purchase_events"`, `DROP VIEW "Substitution_events"`, `ALTER TABLE events DROP COLUMN seen_version`, `ALTER TABLE events DROP COLUMN seal`,
		old, strings.ReplaceAll(old, "Purchase", "Substitution"), `UPDATE schema_version SET version = 13`} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()

	s, err = Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("open a database of schema 13: %v", err)
	}
	defer s.Close()
	var version int64
	if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != engineSchema {
		t.Errorf("the schema after the upgrade: %d, %v; want %d", version, err, engineSchema)
	}
	d, err := s.Get(ctx, "Purchase", "P-7", sawTomek)
	if err != nil || d.Version != 2 || d.Values["limit"] != "10000.00" {
		t.Fatalf("the purchase after the upgrade: %v, %v", d, err)
	}
	if d, err = s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(2)); err != nil || d.Version != 3 {
		t.Fatalf("approve after the upgrade: %v, %v", d, err)
	}
	analyst := As(Principal{ID: "marek", System: []string{GroupAnalysts}})
	rows, err := s.SQL(ctx, `SELECT "kind", "seen_version" FROM "Purchase_events" ORDER BY "after_version"`, nil, analyst)
	if err != nil {
		t.Fatal(err)
	}
	if got := flat(rows.Rows); got != `[["created",null],["transition",null],["transition",2]]` {
		t.Errorf("the view of events after the upgrade: %s", got)
	}
	if _, err := s.SQL(ctx, `SELECT "seen_version" FROM "Substitution_events"`, nil, analyst); err != nil {
		t.Errorf("the view of the events of a class the engine brings: %v", err)
	}
	// Opened again, it is a database of the current schema like any other.
	s.Close()
	if s, err = Open(path, Options{Outbox: OutboxOptions{Manual: true}}); err != nil {
		t.Fatalf("open it again: %v", err)
	}
	s.Close()

	// A schema before 13 is refused.
	raw, _ = sql.Open("sqlite", path)
	if _, err := raw.Exec(`UPDATE schema_version SET version = 12`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if _, err := Open(path, Options{}); code(err) != CodeNotSupported || !strings.Contains(err.Error(), "schema 12") {
		t.Errorf("a database of schema 12: %v; want not_supported", err)
	}
}
