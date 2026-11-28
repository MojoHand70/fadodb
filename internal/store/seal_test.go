package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The seal of the log (ADR-0103). Ann files expense E-1042 for
// 4,800.00 PLN, a hotel and a train; she adds a note; on 2 November at
// 14:05 Jan approves it; Ann files E-1043. Piotr, who looks after the
// server, then opens the database file with an SQLite tool.
const sealExpense = `{"name": "Expense", "title": "number",
 "variables": {"number": {"type": "text"}, "amount": {"type": "money", "scale": 2}, "note": {"type": "text"},
   "lines": {"type": "rows", "schema": {"what": "text", "amount": "money(2)"}}},
 "states": {"initial": "filed", "list": ["filed", "approved", "refused"]},
 "transitions": [{"name": "approve", "from": "filed", "to": "approved", "guard": "amount < 10000"},
   {"name": "refuse", "from": "filed", "to": "refused"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`

var (
	sealAnn = Principal{ID: "ann", Kind: ActorPerson}
	sealJan = Principal{ID: "jan", Kind: ActorPerson}
)

// sealClock is a clock the story sets.
type sealClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *sealClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *sealClock) set(day, hour, minute int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = time.Date(2026, 11, day, hour, minute, 0, 0, time.UTC)
}

// sealStory is the database after the story, with the event of Jan's
// approval.
type sealStory struct {
	s        *Store
	path     string
	clock    *sealClock
	approval *Event
	history  string
	lines    string
}

func tellSealStory(t *testing.T) *sealStory {
	t.Helper()
	cl := &sealClock{}
	cl.set(2, 9, 0)
	s, path := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(sealExpense), asSetup); err != nil {
		t.Fatal(err)
	}
	lines := []any{map[string]any{"what": "Hotel", "amount": "3200.00 PLN"}, map[string]any{"what": "Train", "amount": "1600.00 PLN"}}
	if _, err := s.Create(ctx, "Expense", Values{"number": "E-1042", "amount": "4800.00 PLN", "lines": lines}, As(sealAnn), WithID("E-1042")); err != nil {
		t.Fatal(err)
	}
	cl.set(2, 9, 30)
	if _, err := s.Set(ctx, "Expense", "E-1042", Values{"note": "Kraków, the client visit"}, As(sealAnn)); err != nil {
		t.Fatal(err)
	}
	cl.set(2, 14, 5)
	if _, err := s.Transition(ctx, "Expense", "E-1042", "approve", nil, As(sealJan)); err != nil {
		t.Fatal(err)
	}
	cl.set(2, 15, 0)
	if _, err := s.Create(ctx, "Expense", Values{"number": "E-1043", "amount": "120.00 PLN"}, As(sealAnn), WithID("E-1043")); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Events(ctx, "Expense", "E-1042", As(sealJan))
	if err != nil || len(evs) != 3 || evs[2].Name != "approve" {
		t.Fatalf("the events of E-1042: %v %s", err, dump(evs))
	}
	c, e := s.class("Expense")
	if e != nil {
		t.Fatal(e)
	}
	return &sealStory{s: s, path: path, clock: cl, approval: evs[2], history: c.history, lines: c.children[0].table}
}

// behindTheBack closes the database, runs the statements on its file as
// an SQLite tool would, and opens it again.
func (st *sealStory) behindTheBack(t *testing.T, stmts ...string) {
	t.Helper()
	if err := st.s.Close(); err != nil {
		t.Fatal(err)
	}
	alteredOnPurpose(st.path)
	raw, err := sql.Open("sqlite", st.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range stmts {
		if _, err := raw.Exec(stmt); err != nil {
			raw.Close()
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()
	s, err := Open(st.path, Options{Now: st.clock.Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	st.s = s
}

func (st *sealStory) check(t *testing.T) *LogCheck {
	t.Helper()
	got, err := st.s.CheckLog(context.Background(), asSetup)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// faultsOf are the faults of a kind.
func faultsOf(c *LogCheck, what string) []LogFault {
	var out []LogFault
	for _, f := range c.Faults {
		if f.What == what {
			out = append(out, f)
		}
	}
	return out
}

func TestSeal_TheLogIsSealed(t *testing.T) {
	st := tellSealStory(t)
	got := st.check(t)
	if len(got.Faults) != 0 || got.Events != 4 || got.Sealed != 4 {
		t.Fatalf("the check of the story: %s; want 4 events, all sealed, no fault", dump(got))
	}
	if want := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC).Unix(); got.SealedFrom != want {
		t.Errorf("sealed from %d, want %d: the first event", got.SealedFrom, want)
	}
	// Every event carries 32 bytes, and E-1042's, which has a list, one
	// byte more: whether each version carried the lines forward.
	var lens []int
	rows, err := st.s.db.Query("SELECT length(seal) FROM events ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		lens = append(lens, n)
	}
	rows.Close()
	if fmt.Sprint(lens) != "[33 33 33 33]" {
		t.Errorf("the lengths of the seals: %v; want 33 each", lens)
	}
}

// Piotr makes Olga the approver. The check names that event, and no
// other.
func TestSeal_PiotrChangesTheApprover(t *testing.T) {
	st := tellSealStory(t)
	st.behindTheBack(t, "UPDATE events SET actor_id = 'olga' WHERE event_id = '"+st.approval.ID+"'")
	got := st.check(t)
	changed := faultsOf(got, FaultChanged)
	at := time.Date(2026, 11, 2, 14, 5, 0, 0, time.UTC).Unix()
	if len(got.Faults) != 1 || len(changed) != 1 || changed[0].Event != st.approval.ID || changed[0].Name != "approve" || changed[0].At != at || changed[0].ID != "E-1042" || changed[0].Version != 3 {
		t.Fatalf("the check after Piotr changed the approver: %s; want one fault, the approval of 14:05", dump(got))
	}
}

// Piotr takes the approval out, its event_docs row too. The event after
// it no longer follows the seal before it, and the version the approval
// wrote has no event.
func TestSeal_PiotrTakesTheApprovalOut(t *testing.T) {
	st := tellSealStory(t)
	st.behindTheBack(t, "DELETE FROM events WHERE event_id = '"+st.approval.ID+"'", "DELETE FROM event_docs WHERE event_id = '"+st.approval.ID+"'")
	got := st.check(t)
	changed, strays := faultsOf(got, FaultChanged), faultsOf(got, FaultStray)
	if len(changed) != 1 || changed[0].ID != "E-1043" || len(strays) != 1 || strays[0].ID != "E-1042" || strays[0].Version != 3 || got.Events != 3 {
		t.Fatalf("the check after Piotr took the approval out: %s; want E-1043's event changed and version 3 of E-1042 a stray", dump(got))
	}
}

// Piotr changes the note of the version Jan approved, in the history and
// in the document.
func TestSeal_PiotrChangesTheNote(t *testing.T) {
	st := tellSealStory(t)
	c, _ := st.s.class("Expense")
	st.behindTheBack(t, `UPDATE "`+st.history+`" SET note = 'Gdańsk' WHERE id = 'E-1042' AND version = 3`, `UPDATE "`+c.table+`" SET note = 'Gdańsk' WHERE id = 'E-1042'`)
	got := st.check(t)
	changed := faultsOf(got, FaultChanged)
	if len(got.Faults) != 1 || len(changed) != 1 || changed[0].Event != st.approval.ID {
		t.Fatalf("the check after Piotr changed the note: %s; want the approval's event changed", dump(got))
	}
}

// Piotr changes the note in the document alone: its history still says
// what it held.
func TestSeal_PiotrChangesTheDocumentAlone(t *testing.T) {
	st := tellSealStory(t)
	c, _ := st.s.class("Expense")
	st.behindTheBack(t, `UPDATE "`+c.table+`" SET note = 'Gdańsk' WHERE id = 'E-1042'`)
	got := st.check(t)
	current := faultsOf(got, FaultCurrent)
	if len(got.Faults) != 1 || len(current) != 1 || current[0].ID != "E-1042" || !strings.HasSuffix(current[0].Message, ": note") {
		t.Fatalf("the check after Piotr changed the document: %s; want one fault, the note of E-1042", dump(got))
	}
}

// Piotr makes the hotel a taxi: in the version Ann filed, and then in
// every version.
func TestSeal_PiotrChangesALine(t *testing.T) {
	st := tellSealStory(t)
	st.behindTheBack(t, `UPDATE "`+st.lines+`" SET what = 'Taxi' WHERE doc_id = 'E-1042' AND version = 1 AND what = 'Hotel'`)
	got := st.check(t)
	changed := faultsOf(got, FaultChanged)
	if len(got.Faults) != 1 || len(changed) != 1 || changed[0].Version != 1 {
		t.Fatalf("the check after Piotr changed a line of version 1: %s; want version 1's event changed", dump(got))
	}

	st = tellSealStory(t)
	st.behindTheBack(t, `UPDATE "`+st.lines+`" SET what = 'Taxi' WHERE doc_id = 'E-1042' AND what = 'Hotel'`)
	got = st.check(t)
	if changed := faultsOf(got, FaultChanged); len(got.Faults) != 3 || len(changed) != 3 {
		t.Fatalf("the check after Piotr changed the line in every version: %s; want the three events of E-1042 changed", dump(got))
	}
}

// A version the engine makes by itself, an eventless move after a
// commit, carries the lines of the version before forward: its seal
// notes that it did, and the check holds its lines to the ones before.
const sealTrip = `{"name": "Trip", "title": "number",
 "variables": {"number": {"type": "text"}, "lines": {"type": "rows", "schema": {"what": "text", "amount": "money(2)"}}},
 "states": {"initial": "filed", "list": ["filed", "approved", "booked"]},
 "transitions": [{"name": "approve", "from": "filed", "to": "approved"}, {"from": "approved", "to": "booked", "guard": "TRUE"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`

func TestSeal_PiotrChangesACarriedLine(t *testing.T) {
	st := tellSealStory(t)
	s, ctx := st.s, context.Background()
	if _, err := s.defineOpen(ctx, []byte(sealTrip), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Trip", Values{"number": "T-7", "lines": []any{map[string]any{"what": "Hotel", "amount": "900.00 PLN"}}}, As(sealAnn), WithID("T-7")); err != nil {
		t.Fatal(err)
	}
	if doc, err := s.Transition(ctx, "Trip", "T-7", "approve", nil, As(sealJan)); err != nil || doc.State != "booked" || doc.Version != 3 {
		t.Fatalf("the approval of T-7: %v %s; want it booked at version 3", err, dump(doc))
	}
	var flags []byte
	if err := s.db.QueryRow("SELECT substr(seal, 33) FROM events WHERE class = 'Trip' AND after_version = 3").Scan(&flags); err != nil || len(flags) != 1 || flags[0] != 1 {
		t.Fatalf("the seal of the move to booked notes %v, %v; want its one list carried", flags, err)
	}
	if got := st.check(t); len(got.Faults) != 0 {
		t.Fatalf("the check before Piotr: %s", dump(got))
	}
	c, _ := s.class("Trip")
	st.behindTheBack(t, `UPDATE "`+c.children[0].table+`" SET what = 'Taxi' WHERE doc_id = 'T-7' AND version = 3`)
	got := st.check(t)
	carried := faultsOf(got, FaultCarried)
	if len(got.Faults) != 1 || len(carried) != 1 || carried[0].ID != "T-7" || carried[0].Version != 3 {
		t.Fatalf("the check after Piotr changed the carried line: %s; want version 3 of T-7 holding other lines than it carried", dump(got))
	}
}

// Piotr slips in a version that no command made, and a line.
func TestSeal_PiotrSlipsInAVersion(t *testing.T) {
	st := tellSealStory(t)
	st.behindTheBack(t,
		`INSERT INTO "`+st.history+`" (id, version, class_version, created_at, updated_at, valid_from, event_id, number) VALUES ('E-1042', 9, 1, 0, 0, 0, 'forged', 'E-1042')`,
		`INSERT INTO "`+st.lines+`" (doc_id, version, idx, what) VALUES ('E-1042', 8, 0, 'Taxi')`)
	got := st.check(t)
	strays := faultsOf(got, FaultStray)
	if len(got.Faults) != 2 || len(strays) != 2 || strays[0].Version != 9 || strays[1].Version != 8 {
		t.Fatalf("the check after Piotr slipped in a version and a line: %s; want two strays", dump(got))
	}
}

// Piotr takes a seal away.
func TestSeal_PiotrTakesASealAway(t *testing.T) {
	st := tellSealStory(t)
	st.behindTheBack(t, "UPDATE events SET seal = NULL WHERE event_id = '"+st.approval.ID+"'")
	got := st.check(t)
	unsealed, changed := faultsOf(got, FaultUnsealed), faultsOf(got, FaultChanged)
	if len(unsealed) != 1 || unsealed[0].Event != st.approval.ID || len(changed) != 1 || changed[0].ID != "E-1043" || got.Sealed != 3 {
		t.Fatalf("the check after Piotr took a seal away: %s; want the approval unsealed and E-1043's event changed", dump(got))
	}
}

// What a commit took back, whole or to a savepoint, leaves no gap in the
// chain: a preview, a refused move, a batch where one document is
// refused; and the chain goes on across a reopening.
func TestSeal_TakenBackLeavesNoGap(t *testing.T) {
	st := tellSealStory(t)
	s, ctx := st.s, context.Background()
	for _, id := range []string{"E-1044", "E-1045", "E-1046"} {
		amount := "300.00 PLN"
		if id == "E-1045" {
			amount = "25000.00 PLN"
		}
		if _, err := s.Create(ctx, "Expense", Values{"number": id, "amount": amount}, As(sealAnn), WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Preview(ctx, "Expense", "E-1044", Values{"note": "a preview writes and takes back"}, "approve", As(sealJan)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Expense", "E-1045", "approve", nil, As(sealJan)); code(err) != CodeGuardFalse {
		t.Fatalf("the approval of 25,000.00 PLN: %v; want guard_false", err)
	}
	out, err := s.TransitionMany(ctx, "Expense", "approve", []string{"E-1044", "E-1045", "E-1046"}, nil, As(sealJan))
	if err != nil || out[0].Refusal != nil || out[1].Refusal == nil || out[2].Refusal != nil {
		t.Fatalf("the batch: %v %s; want E-1045 refused and the others moved", err, dump(out))
	}
	st.behindTheBack(t)
	if _, err := st.s.Transition(ctx, "Expense", "E-1045", "refuse", nil, As(sealJan)); err != nil {
		t.Fatal(err)
	}
	got := st.check(t)
	if len(got.Faults) != 0 || got.Events != got.Sealed || got.Events != 10 {
		t.Fatalf("the check after what was taken back: %s; want 10 events sealed, no fault", dump(got))
	}
}

// A write the writer did not count, as a transaction taken back without
// its knowing: the event is written at a row the writer did not expect,
// and its seal is worked out again after the event the row follows.
func TestSeal_TheWriterFindsItsPlaceAgain(t *testing.T) {
	st := tellSealStory(t)
	s, ctx := st.s, context.Background()
	s.mu.Lock()
	// Two events of a transaction that SQLite took back and the writer
	// was not told of.
	last := s.seal.committed
	s.seal.committed = sealPoint{row: last.row + 2, seal: [sealSize]byte{1}}
	s.seal.last = s.seal.committed
	s.mu.Unlock()
	if _, err := s.Create(ctx, "Expense", Values{"number": "E-1044"}, As(sealAnn)); err != nil {
		t.Fatal(err)
	}
	// After the last event it wrote, which it no longer knows: the gap
	// stays for the check to find. Then the writer goes on from there.
	got := st.check(t)
	if changed := faultsOf(got, FaultChanged); len(got.Faults) != 1 || len(changed) != 1 {
		t.Fatalf("the check after the writer lost count: %s; want the next event changed", dump(got))
	}
	alteredOnPurpose(st.path)

	// Within a transaction, the writer knows the event before each
	// savepoint: an event taken back to one without its knowing is
	// passed over.
	z := &sealer{committed: sealPoint{row: 10, seal: [sealSize]byte{10}}, last: sealPoint{row: 12, seal: [sealSize]byte{12}},
		marks: []sealMark{{name: "many", at: sealPoint{row: 11, seal: [sealSize]byte{11}}}}}
	z.start(0)
	z.buf = append(z.buf, "the event"...)
	z.seal()
	if err := z.wrote(ctx, 12); err != nil {
		t.Fatal(err)
	}
	want := &sealer{h: z.h, buf: z.buf}
	want.sum(&[sealSize]byte{11})
	if z.last.row != 12 || z.last.seal != [sealSize]byte(want.out[:sealSize]) {
		t.Errorf("the writer after row 12 came again: %v; want it sealed after row 11", z.last)
	}
}

// A database made before the seal is sealed from the first event an
// engine with the seal writes; the check says from when.
func TestSeal_AnOlderDatabaseIsSealedFromWhenItIsOpened(t *testing.T) {
	st := tellSealStory(t)
	st.behindTheBack(t, `ALTER TABLE events DROP COLUMN seal`, `UPDATE schema_version SET version = 16`)
	st.clock.set(3, 8, 0)
	if _, err := st.s.Create(context.Background(), "Expense", Values{"number": "E-1044"}, As(sealAnn), WithID("E-1044")); err != nil {
		t.Fatal(err)
	}
	got := st.check(t)
	if len(got.Faults) != 0 || got.Events != 5 || got.Sealed != 1 || got.SealedFrom != time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("the check of a database of schema 16: %s; want 5 events, 1 sealed from 3 November, no fault", dump(got))
	}
}

// Every write of the store begins in beginWriter, and every savepoint is
// set, gone back to and released through the seal's functions: a write
// begun beside them would leave the seal thinking the events of a
// transaction taken back were still there.
func TestEveryWriteBeginsInOnePlace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	direct := regexp.MustCompile(`ExecContext\(ctx, ("BEGIN|"SAVEPOINT|"ROLLBACK TO|"RELEASE|begin\))|stmts\.(mark|unmark|toMark)\.ExecContext`)
	var places []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "seal.go" {
			continue
		}
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(text), "\n") {
			// The copy reads in a transaction of its own connection.
			if direct.MatchString(line) && !strings.Contains(line, "conn.ExecContext") {
				places = append(places, file+":"+itoa(i+1))
			}
		}
	}
	if len(places) != 0 {
		t.Errorf("a write begins, or a savepoint is set, beside the seal at %v; use beginWriter, savepoint, backTo, releasePoint or the batch's three", places)
	}
}
