package store

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// still is a clock that stands where a test puts it.
type still struct {
	mu  sync.Mutex
	now time.Time
}

func (c *still) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *still) days(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.AddDate(0, 0, n)
}

// stopping is a context that reports it was cancelled once it has been
// asked after times, with a Done that never fires: what a caller that
// gives up between two documents looks like to the loop over them.
type stopping struct {
	context.Context
	asked, after int
}

func (c *stopping) Err() error {
	if c.asked++; c.asked > c.after {
		return context.Canceled
	}
	return nil
}

// dueDay is the day the tests of Due begin on, at noon.
var dueDay = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func day(n int) string { return dueDay.AddDate(0, 0, n).Format("2006-01-02") }

const dueHolds = `{"name": "Holds", "columns": {"customer": "text", "stop": "bool"}, "key": ["customer"]}`

// dueInvoice has a guard of each kind: two on the clock, which
// translate to SQL and leave one state in a set order; one on the days
// in a state and one that looks a table up, which do not.
const dueInvoice = `{"name": "Invoice", "variables": {
  "due": {"type": "date", "index": true}, "amount": {"type": "number"}, "owner": {"type": "text"}, "customer": {"type": "text"}},
 "states": {"initial": "sent", "list": ["sent", "overdue", "chased", "lost", "held", "paid"], "final": ["paid"]},
 "transitions": [
   {"from": "sent", "to": "lost", "guard": "TODAY() > ADDDAYS(due, 90)"},
   {"from": "sent", "to": "overdue", "guard": "TODAY() > due"},
   {"from": "overdue", "to": "chased", "guard": "DAYSIN(\"overdue\") >= 7"},
   {"from": "chased", "to": "held", "guard": "LOOKUP(Holds, stop, customer = THIS.customer)"},
   {"name": "pay", "from": ["sent", "overdue", "chased", "held"], "to": "paid"}
 ]}`

func openDue(t *testing.T) (*Store, *still) {
	t.Helper()
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(dueHolds), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(dueInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, cl
}

// previewed is what Due must list: for every document of the class, in
// the order of the class's states and then of the ids, what the preview
// of a commit that changes nothing says of it.
func previewed(t *testing.T, s *Store, class string, opts ...Option) []DueDocument {
	t.Helper()
	ctx := context.Background()
	c, _ := s.class(class)
	rows, err := s.db.QueryContext(ctx, `SELECT "id", "state" FROM `+quote(c.table))
	if err != nil {
		t.Fatal(err)
	}
	type doc struct{ id, state string }
	var docs []doc
	for rows.Next() {
		var d doc
		if err := rows.Scan(&d.id, &d.state); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, d)
	}
	rows.Close()
	order := map[string]int{}
	for i, st := range c.cls.States.List {
		order[st] = i
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].state != docs[j].state {
			return order[docs[i].state] < order[docs[j].state]
		}
		return docs[i].id < docs[j].id
	})
	out := []DueDocument{}
	for _, d := range docs {
		if c.cls.States.IsFinal(d.state) {
			continue
		}
		pv, err := s.Preview(ctx, class, d.id, nil, "", opts...)
		if err != nil {
			t.Fatalf("preview %s: %v", d.id, err)
		}
		if len(pv.Path) > 1 {
			out = append(out, DueDocument{ID: d.id, State: d.state, To: pv.Path[1]})
		}
	}
	return out
}

func sameDue(t *testing.T, what string, got, want []DueDocument) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: due\n  %v\nwant\n  %v", what, got, want)
	}
}

// TestDue_Paths is which guards Due evaluates as a filter and which
// document by document (ADR-0047, item 8).
func TestDue_Paths(t *testing.T) {
	s, _ := openDue(t)
	c, _ := s.class("Invoice")
	plan := s.duePlan(c)
	var got []string
	for _, st := range plan.states {
		got = append(got, fmt.Sprintf("%s:%d:%d", st.name, len(st.leaving), len(st.filters)))
	}
	// sent has two guards and a filter for each; overdue and chased have
	// a guard that does not translate; nothing eventless leaves lost and
	// held, and paid is terminal.
	want := []string{"sent:2:2", "overdue:1:0", "chased:1:0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the plan %v, want %v", got, want)
	}
	if again := s.duePlan(c); again != plan {
		t.Error("the plan was compiled twice")
	}
	// A state with one guard that translates and one that does not goes
	// document by document as a whole: the order of the two decides
	// where a document moves.
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Mixed", "variables": {"due": {"type": "date"}},
 "states": {"initial": "a", "list": ["a", "b", "c"]},
 "transitions": [{"from": "a", "to": "b", "guard": "ENTRIES(\"a\") > 5"}, {"from": "a", "to": "c", "guard": "TODAY() > due"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	m, _ := s.class("Mixed")
	if st := s.duePlan(m).states; len(st) != 1 || st[0].filters != nil || len(st[0].leaving) != 2 {
		t.Errorf("the plan of a state with a guard that does not translate: %+v", st)
	}
}

// TestDue is Due against the preview of every document: over guards on
// the clock, which run as filters, a guard on the days in a state and a
// guard that looks a table up, which run document by document, at the
// database's clock and at a moment.
func TestDue(t *testing.T) {
	s, cl := openDue(t)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 120; i++ {
		v := Values{"amount": i, "owner": []string{"ann", "bob"}[i%2], "customer": fmt.Sprintf("C%d", i%6)}
		if i%13 != 0 {
			// From thirty days ago to thirty days ahead; every thirteenth
			// has no date, which compares as the first day there is.
			v["due"] = day(i%61 - 30)
		}
		doc, err := s.Create(ctx, "Invoice", v, WithID(fmt.Sprintf("I-%03d", i)), As(ann))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	// A create follows its own chain, so nothing is due at the moment
	// the documents were made.
	if due, err := s.Due(ctx, "Invoice"); err != nil || len(due) != 0 {
		t.Fatalf("due at once: %v %v", due, err)
	}
	check := func(what string, min int, opts ...Option) []DueDocument {
		t.Helper()
		got, err := s.Due(ctx, "Invoice", opts...)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		sameDue(t, what, got, previewed(t, s, "Invoice", opts...))
		if len(got) < min {
			t.Errorf("%s: %d documents are due, want at least %d for the test to say anything", what, len(got), min)
		}
		return got
	}
	// At moments ahead: more and more of the invoices sent pass their
	// date, the overdue ones their week, and after ninety days the first
	// transition of the two that leave sent is the one that holds.
	for _, days := range []int{1, 3, 8, 31, 95, 130} {
		check(fmt.Sprintf("at %d days", days), 1, At(dueDay.AddDate(0, 0, days)))
	}
	// A moment before the documents were made, and one before the first
	// day there is, where a blank date is the later of the two.
	check("a year before", 0, At(dueDay.AddDate(-1, 0, 0)))
	check("before the first day", 0, At(time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)))

	// The filter and the document-by-document path give one answer.
	c, _ := s.class("Invoice")
	sent := s.duePlan(c).states[0]
	slow := &dueState{name: sent.name, leaving: sent.leaving}
	for _, days := range []int{1, 31, 95} {
		o := apply([]Option{At(dueDay.AddDate(0, 0, days))})
		fast, err := s.dueIn(ctx, c, sent, o, MaxDue)
		if err != nil {
			t.Fatal(err)
		}
		one, err := s.dueIn(ctx, c, slow, o, MaxDue)
		if err != nil {
			t.Fatal(err)
		}
		sameDue(t, fmt.Sprintf("document by document at %d days", days), one, fast)
		if len(fast) == 0 {
			t.Errorf("at %d days nothing in sent is due", days)
		}
	}

	// The clock moves, and what is due moves when a commit touches it.
	cl.days(8)
	due := check("eight days on", 10)
	states := map[string]int{}
	for _, d := range due {
		states[d.State+">"+d.To]++
		if _, err := s.Refresh(ctx, "Invoice", d.ID, nil, As(bob)); err != nil {
			t.Fatalf("touch %s: %v", d.ID, err)
		}
	}
	if states["sent>overdue"] == 0 || states["overdue>chased"] == 0 {
		t.Errorf("eight days on the moves are %v, want some from sent and some from overdue", states)
	}
	// A touch follows the whole chain, so a second look finds nothing.
	if again := check("after the touches", 0); len(again) != 0 {
		t.Errorf("after every due document was touched %d are due", len(again))
	}

	// A guard that looks a table up: a row in the table makes documents
	// due with no clock and no commit of theirs.
	if _, err := s.ImportRows(ctx, "Holds", []Values{{"customer": "C2", "stop": true}, {"customer": "C3", "stop": false}}); err != nil {
		t.Fatal(err)
	}
	held := check("after the hold", 1)
	for _, d := range held {
		doc, err := s.Get(ctx, "Invoice", d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.State != "chased" || d.To != "held" || doc.Values["customer"] != "C2" {
			t.Errorf("due after the hold: %+v of customer %v", d, doc.Values["customer"])
		}
	}

	// Due wrote nothing: the events are the creates, the touches and
	// their moves.
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE kind NOT IN ('created', 'refresh', 'transition')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d events of another kind, %v", n, err)
	}
	_ = ids
}

// TestDue_Principal is a guard that reads who asks: USER() and INGROUP
// are the principal's of the call, in a filter as in a commit.
func TestDue_Principal(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Task", "variables": {"due": {"type": "date"}, "owner": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "mine", "audit"]},
 "transitions": [
   {"from": "open", "to": "mine", "guard": "owner = USER() AND TODAY() > due"},
   {"from": "open", "to": "audit", "guard": "INGROUP(\"finance\") AND TODAY() > ADDDAYS(due, 5)"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for i, owner := range []string{"ann", "bob", "ann", "carl"} {
		if _, err := s.Create(ctx, "Task", Values{"owner": owner, "due": day(i)}, WithID(fmt.Sprintf("T-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := s.class("Task")
	if st := s.duePlan(c).states; len(st) != 1 || len(st[0].filters) != 2 {
		t.Fatalf("the guards on the principal do not run as filters: %+v", st)
	}
	at := At(dueDay.AddDate(0, 0, 7))
	for _, tc := range []struct {
		name string
		opts []Option
		want []DueDocument
	}{
		{"ann", []Option{As(ann), at}, []DueDocument{{"T-0", "open", "mine", 0}, {"T-2", "open", "mine", 0}}},
		// Bob owns one and is in finance: his own first, as the class
		// orders the two, and the others that are five days late.
		{"bob", []Option{As(bob), at}, []DueDocument{{"T-0", "open", "audit", 0}, {"T-1", "open", "mine", 0}}},
		{"nobody", []Option{at}, []DueDocument{}},
	} {
		got, err := s.Due(ctx, "Task", tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		sameDue(t, tc.name, got, tc.want)
		sameDue(t, tc.name+" against the previews", got, previewed(t, s, "Task", tc.opts...))
		// Document by document the principal is the same one.
		st := s.duePlan(c).states[0]
		one, err := s.dueIn(ctx, c, &dueState{name: st.name, leaving: st.leaving}, apply(tc.opts), MaxDue)
		if err != nil {
			t.Fatal(err)
		}
		sameDue(t, tc.name+" document by document", one, tc.want)
	}
}

// TestDue_OlderVersions is a class that was defined again (ADR-0039): a
// document the older version computed is evaluated as the commit that
// adopts it would, its formulas recomputed under the current version,
// and not by the filter, which reads what is stored.
func TestDue_OlderVersions(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	const bill = `{"name": "Bill", "variables": {"due": {"type": "date"}, "amount": {"type": "number"},
  "grace": {"formula": "%s"}, "share": {"formula": "%s"}},
 "states": {"initial": "open", "list": ["open", "late"]},
 "transitions": [{"from": "open", "to": "late", "guard": "TODAY() > ADDDAYS(due, grace)"}]}`
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(bill, "10", "amount")), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct {
		id     string
		due    int
		amount float64
	}{{"B-1", -5, 1}, {"B-2", 5, 2}, {"B-3", -5, 5}, {"B-4", -8, 3}} {
		if _, err := s.Create(ctx, "Bill", Values{"due": day(b.due), "amount": b.amount}, WithID(b.id)); err != nil {
			t.Fatal(err)
		}
	}
	old, _ := s.class("Bill")
	if due, err := s.Due(ctx, "Bill"); err != nil || len(due) != 0 {
		t.Fatalf("under the first version: %v %v", due, err)
	}
	if st := s.duePlan(old).states[0]; st.noOlder.Load() {
		t.Error("a class at its first version looked for older documents")
	}
	// The second version gives no days of grace, and a formula that
	// fails for one of the documents.
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(bill, "0", "10 / (amount - 5)")), asSetup); err != nil {
		t.Fatal(err)
	}
	c, _ := s.class("Bill")
	if c == old || c.cls.Version != 2 {
		t.Fatalf("the class is at version %d", c.cls.Version)
	}
	st := s.duePlan(c).states[0]
	// The filter reads the current version's documents, and there are
	// none.
	found := map[string]dueHit{}
	if err := s.dueFiltered(ctx, c, st, st.filters[0], options{}, MaxDue, "late", found); err != nil || len(found) != 0 {
		t.Fatalf("the filter found %v, %v", found, err)
	}
	// B-1 and B-4 are past their date with no grace. B-3 is too, but
	// the commit that adopted it would be refused, since its formula
	// fails: it is not due, as it cannot be touched.
	want := []DueDocument{{"B-1", "open", "late", 0}, {"B-4", "open", "late", 0}}
	got, err := s.Due(ctx, "Bill")
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "after the redefinition", got, want)
	if st.noOlder.Load() {
		t.Error("the state is marked free of older documents while it holds four")
	}
	if _, err := s.Refresh(ctx, "Bill", "B-3", nil); err == nil {
		t.Error("the touch of the document whose formula fails went through")
	}
	// A touch adopts and moves.
	doc, err := s.Refresh(ctx, "Bill", "B-1", nil)
	if err != nil || doc.State != "late" || doc.ClassVersion != 2 {
		t.Fatalf("the touch: %+v %v", doc, err)
	}
	got, err = s.Due(ctx, "Bill")
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "after one touch", got, want[1:])
	// With every document that can be brought up at the current version
	// but one, the search for older ones still runs; fix the last one and
	// it stops.
	if _, err := s.Migrate(ctx, "Bill", asSetup); err == nil {
		t.Error("the migration of the document whose formula fails went through")
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(bill, "0", "amount * 2")), asSetup); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Migrate(ctx, "Bill", asSetup); err != nil || n == 0 {
		t.Fatalf("migrate: %d %v", n, err)
	}
	c, _ = s.class("Bill")
	st = s.duePlan(c).states[0]
	got, err = s.Due(ctx, "Bill")
	if err != nil {
		t.Fatal(err)
	}
	// A migration moves nothing (ADR-0039): the two documents are at the
	// current version now and still due, and the filter finds them.
	want = []DueDocument{{"B-3", "open", "late", 0}, {"B-4", "open", "late", 0}}
	sameDue(t, "after the migration", got, want)
	if err := s.dueFiltered(ctx, c, st, st.filters[0], options{}, MaxDue, "late", found); err != nil || len(found) != 2 {
		t.Errorf("the filter found %v, %v", found, err)
	}
	if !st.noOlder.Load() {
		t.Error("the state holds no older document and is not marked")
	}
	cl.days(6)
	got, err = s.Due(ctx, "Bill")
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "six days on", got, []DueDocument{{"B-2", "open", "late", 0}, {"B-3", "open", "late", 0}, {"B-4", "open", "late", 0}})
	sameDue(t, "six days on, against the previews", got, previewed(t, s, "Bill"))
	// The state is marked, so the older documents were not looked for
	// again.
	lists := s.dueLists.Load()
	if _, err := s.Due(ctx, "Bill"); err != nil || s.dueLists.Load() != lists {
		t.Errorf("a state marked free of older documents was searched again: %d lists, %v", s.dueLists.Load()-lists, err)
	}
	// A fourth version gives ten days of grace again. What is stored
	// says all three are due; under the version a commit would adopt,
	// B-2 has its ten days, and the two that are older do not.
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(bill, "10", "amount * 2")), asSetup); err != nil {
		t.Fatal(err)
	}
	got, err = s.Due(ctx, "Bill")
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "under the fourth version", got, []DueDocument{{"B-3", "open", "late", 0}, {"B-4", "open", "late", 0}})
	sameDue(t, "under the fourth version, against the previews", got, previewed(t, s, "Bill"))
	if s.dueLists.Load() == lists {
		t.Error("the documents of the older version were not looked for")
	}
}

// TestDue_AtMost is the bound of one call: MaxDue documents, the first
// in the order of the states and then of the ids, on both paths.
func TestDue_AtMost(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Note", "variables": {"due": {"type": "date"}},
 "states": {"initial": "new", "list": ["kept", "new", "old", "stale"]},
 "transitions": [
   {"from": "new", "to": "old", "guard": "TODAY() > ADDDAYS(due, 1)"},
   {"from": "new", "to": "stale", "guard": "TODAY() > due"},
   {"from": "kept", "to": "old", "guard": "TODAY() > due"},
   {"name": "keep", "from": "new", "to": "kept"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	// Every second note is a day older, so that in three days the first
	// guard holds for half of them and the second for all.
	const n = MaxDue + 40
	batch := make([]Values, n)
	for i := range batch {
		batch[i] = Values{"due": day(1 + i%2)}
	}
	docs, err := s.CreateMany(ctx, "Note", batch)
	if err != nil {
		t.Fatal(err)
	}
	to := map[string]string{}
	var kept, fresh []string
	for i, d := range docs {
		to[d.ID] = []string{"old", "stale"}[i%2]
		if i%400 == 7 {
			if _, err := s.Transition(ctx, "Note", d.ID, "keep", nil); err != nil {
				t.Fatal(err)
			}
			kept = append(kept, d.ID)
			to[d.ID] = "old"
			continue
		}
		fresh = append(fresh, d.ID)
	}
	// Three made last whose ids sort first: the order is the ids', not
	// the order the documents were made in.
	for _, id := range []string{"0-c", "0-a", "0-b"} {
		if _, err := s.Create(ctx, "Note", Values{"due": day(2)}, WithID(id)); err != nil {
			t.Fatal(err)
		}
		fresh = append(fresh, id)
		to[id] = "stale"
	}
	sort.Strings(kept)
	sort.Strings(fresh)
	cl.days(3)
	got, err := s.Due(ctx, "Note")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxDue {
		t.Fatalf("%d documents listed, want %d", len(got), MaxDue)
	}
	// kept comes first in the class's states, then as many of new as
	// there is room for.
	for i, d := range got {
		want := DueDocument{State: "new"}
		if i < len(kept) {
			want.ID, want.State = kept[i], "kept"
		} else {
			want.ID = fresh[i-len(kept)]
		}
		want.To = to[want.ID]
		if d != want {
			t.Fatalf("document %d is %+v, want %+v", i, d, want)
		}
	}
	// One state with room for 25: each of its two filters finds 25, and
	// the list is the first 25 of both together.
	c, _ := s.class("Note")
	st := s.duePlan(c).states[1]
	fast, err := s.dueIn(ctx, c, st, options{}, 25)
	if err != nil {
		t.Fatal(err)
	}
	var want []DueDocument
	for _, id := range fresh[:25] {
		want = append(want, DueDocument{ID: id, State: "new", To: to[id]})
	}
	sameDue(t, "the first 25 by the filters", fast, want)
	// Document by document the bound is the same, and a look at every
	// document of the state reads them all, batch after batch.
	slow := &dueState{name: st.name, leaving: st.leaving}
	lists := s.dueLists.Load()
	one, err := s.dueIn(ctx, c, slow, options{}, 25)
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "the first 25 document by document", one, want)
	// It stopped when it had its 25, in the first batch of ids.
	if n := s.dueLists.Load() - lists; n != 1 {
		t.Errorf("the look for 25 documents read %d batches of ids, want 1", n)
	}
	all, err := s.dueIn(ctx, c, slow, options{}, len(fresh)+10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(fresh) || all[0].ID != fresh[0] || all[len(all)-1].ID != fresh[len(fresh)-1] {
		t.Errorf("document by document over the whole state: %d listed of %d", len(all), len(fresh))
	}
}

// TestDue_Rights is a guard over a variable the caller may not read: a
// guard is the engine's own condition and sees every variable, in a
// filter as in a commit (ADR-0041).
func TestDue_Rights(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Claim", "variables": {"due": {"type": "date"}, "limit": {"type": "date"}},
 "states": {"initial": "open", "list": ["open", "late", "closed"]},
 "transitions": [
   {"from": "open", "to": "closed", "guard": "TODAY() > limit"},
   {"from": "open", "to": "late", "guard": "TODAY() > due"}],
 "rights": {"*": {"due": {"read": ["finance"]}, "limit": {"read": "INGROUP(\"finance\")"}}}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ctx, "Claim", Values{"due": day(1 + i), "limit": day(10 * (i + 1))}, WithID(fmt.Sprintf("C-%d", i)), As(bob)); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := s.class("Claim")
	if st := s.duePlan(c).states; len(st) != 1 || len(st[0].filters) != 2 {
		t.Fatalf("the guards over variables with read rights do not run as filters: %+v", st)
	}
	want := []DueDocument{{"C-0", "open", "closed", 0}, {"C-1", "open", "late", 0}, {"C-2", "open", "late", 0}}
	for _, p := range []Principal{ann, bob, {}} {
		got, err := s.Due(ctx, "Claim", As(p), At(dueDay.AddDate(0, 0, 12)))
		if err != nil {
			t.Fatal(err)
		}
		sameDue(t, "as "+p.ID, got, want)
		// The caller who may not read the dates is told what is due all
		// the same, and may not filter on them.
		if _, err := s.Query(ctx, Filter{Class: "Claim", Where: "TODAY() > due"}, As(p)); (err == nil) != (p.ID == "bob") {
			t.Errorf("a filter on the date as %q: %v", p.ID, err)
		}
	}
}

// TestDue_Who is a guard that asks who made the document, where it
// came from, its state and its id, on both paths: the filter reads the
// row's columns, and the look at one document reads the row with them.
func TestDue_Who(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	const def = `{"name": "%s", "variables": {"due": {"type": "date"}},
 "states": {"initial": "open", "list": ["open", "late"]},
 "transitions": [{"from": "open", "to": "late", "guard": "CREATEDBY() = \"ann\" AND ENTEREDFROM() = \"\" AND STATE() = \"open\" AND LEFT(ID(), 2) = \"W-\" AND TODAY() > due%s"}]}`
	for _, cls := range []struct{ name, more string }{{"Fast", ""}, {"Slow", ` AND ENTRIES(\"open\") = 1`}} {
		if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, cls.name, cls.more)), asSetup); err != nil {
			t.Fatal(err)
		}
		for i, p := range []Principal{ann, bob, ann} {
			if _, err := s.Create(ctx, cls.name, Values{"due": day(1)}, WithID(fmt.Sprintf("W-%d", i)), As(p)); err != nil {
				t.Fatal(err)
			}
		}
		c, _ := s.class(cls.name)
		if st := s.duePlan(c).states[0]; (st.filters != nil) != (cls.name == "Fast") {
			t.Fatalf("%s: filters %v", cls.name, st.filters)
		}
		got, err := s.Due(ctx, cls.name, At(dueDay.AddDate(0, 0, 3)), As(bob))
		if err != nil {
			t.Fatal(err)
		}
		sameDue(t, cls.name, got, []DueDocument{{"W-0", "open", "late", 0}, {"W-2", "open", "late", 0}})
	}
}

// TestDue_Nothing is a class that has nothing to be due, a document that
// is gone or has moved between the list and the look, and the refusals.
func TestDue_Nothing(t *testing.T) {
	s, cl := openDue(t)
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Calc", "variables": {"a": {"type": "number"}, "b": {"formula": "a * 2"}}}`,
		`{"name": "Named", "variables": {"a": {"type": "number"}}, "states": {"initial": "x", "list": ["x", "y"]}, "transitions": [{"name": "go", "from": "x", "to": "y", "guard": "a > 0"}]}`,
	} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "Named", Values{"a": 1}); err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{"Calc", "Named"} {
		due, err := s.Due(ctx, class)
		if err != nil || due == nil || len(due) != 0 {
			t.Errorf("%s: %v %v, want an empty list", class, due, err)
		}
	}
	if _, err := s.Due(ctx, "Nope"); code(err) != CodeUnknownClass {
		t.Errorf("an unknown class: %v", err)
	}
	// A terminal document is never due, though a guard on the clock
	// would hold over it.
	if _, err := s.Create(ctx, "Invoice", Values{"due": day(1)}, WithID("I-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Invoice", Values{"due": day(1)}, WithID("I-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Invoice", "I-2", "pay", nil); err != nil {
		t.Fatal(err)
	}
	cl.days(3)
	due, err := s.Due(ctx, "Invoice")
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "one sent, one paid", due, []DueDocument{{"I-1", "sent", "overdue", 0}})
	// One look: a document that is not there, one that has left the state
	// it was listed in, and one that is due.
	c, _ := s.class("Invoice")
	evalCtx := &formula.Context{Now: cl.Now().Unix()}
	for _, tc := range []struct{ id, in, want string }{
		{"I-0", "sent", ""},
		{"I-1", "overdue", ""},
		{"I-2", "sent", ""},
		{"I-1", "sent", "overdue"},
	} {
		got, err := s.dueOne(ctx, c, tc.id, tc.in, evalCtx)
		if err != nil || got != (dueHit{to: tc.want}) {
			t.Errorf("a look at %s in %s: %+v %v, want %q", tc.id, tc.in, got, err, tc.want)
		}
	}
	// A call that is cancelled stops between two documents: here after
	// the list was read and before the first document is looked at.
	st := s.duePlan(c).states[0]
	stopped := &stopping{Context: ctx}
	found := map[string]dueHit{}
	if _, err := s.dueOneByOne(stopped, c, st, options{}, MaxDue, false, found); err != context.Canceled || len(found) != 0 {
		t.Errorf("a cancelled call went on: %v, found %v", err, found)
	}
	// A closed database refuses, on either path.
	s.Close()
	if _, err := s.Due(ctx, "Invoice"); err == nil {
		t.Error("Due of a closed database answered")
	}
	if _, err := s.dueOne(ctx, c, "I-1", "sent", evalCtx); code(err) != CodeClosed {
		t.Errorf("a look in a closed database: %v", err)
	}
}

// TestDue_WhileCommitting runs Due beside commits that move the same
// documents: it takes the writer's turn for one document at a time, and
// every answer names a state the document could be in.
func TestDue_WhileCommitting(t *testing.T) {
	s, cl := openDue(t)
	ctx := context.Background()
	const n = 60
	for i := 0; i < n; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"due": day(1 + i%3), "customer": "C1"}, WithID(fmt.Sprintf("I-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	cl.days(10)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := s.Refresh(ctx, "Invoice", fmt.Sprintf("I-%02d", i), nil); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			due, err := s.Due(ctx, "Invoice", At(dueDay.AddDate(0, 0, 30)))
			if err != nil {
				errs <- err
				return
			}
			for _, d := range due {
				if (d.State != "sent" || d.To != "overdue") && (d.State != "overdue" || d.To != "chased") {
					errs <- fmt.Errorf("due %+v", d)
					return
				}
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestQuery_ComparesWithTheClock is a filter whose comparison the
// compiler gives an index to seek (a date, a datetime or a number
// against an operand that reads no document) beside the same condition
// written so that it gets none: one answer, with blanks on the column,
// at moments either side of the first day there is.
func TestQuery_ComparesWithTheClock(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Task", "variables": {
  "due": {"type": "date", "index": true}, "seen": {"type": "datetime", "index": true}, "n": {"type": "number", "index": true}},
 "states": {"initial": "open", "list": ["open"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for i := -3; i <= 3; i++ {
		v := Values{"due": day(i), "seen": dueDay.Add(time.Duration(i) * time.Hour), "n": i}
		if _, err := s.Create(ctx, "Task", v, WithID(fmt.Sprintf("T%+d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "Task", Values{}, WithID("blank")); err != nil {
		t.Fatal(err)
	}
	// A day and an instant before the first there is, and a number below
	// zero: what a blank operand, which compares as zero, lies above.
	if _, err := s.Create(ctx, "Task", Values{"due": "1965-03-01", "seen": "1965-03-01 08:00:00", "n": -40}, WithID("early")); err != nil {
		t.Fatal(err)
	}
	moments := []time.Time{dueDay, dueDay.AddDate(0, 0, 2), dueDay.AddDate(0, 0, -40), time.Date(1960, 5, 1, 0, 0, 0, 0, time.UTC), time.Unix(0, 0).UTC()}
	n := 0
	for _, operand := range []struct{ column, expr string }{
		{"due", "TODAY()"}, {"due", "ADDDAYS(TODAY(), -1)"}, {"due", "DATE(2026, 11, 9)"},
		{"seen", "NOW()"}, {"seen", "ADDDAYS(NOW(), 0.05)"},
		{"n", "1 + 1"}, {"n", "0 - 1"}, {"n", "DAYSBETWEEN(DATE(2026, 11, 8), TODAY())"},
		// Operands that are blank, and one that fails, which matches
		// nothing.
		{"due", `DATEVALUE("")`}, {"n", `VALUE("")`}, {"n", "1 / 0"},
	} {
		for _, op := range []string{"<", "<=", "=", ">=", ">"} {
			for _, flipped := range []bool{false, true} {
				where := operand.column + " " + op + " " + operand.expr
				if flipped {
					where = operand.expr + " " + op + " " + operand.column
				}
				for _, at := range moments {
					fast, err := s.Query(ctx, Filter{Class: "Task", Where: where}, At(at))
					if err != nil {
						t.Fatalf("%s: %v", where, err)
					}
					// NOT(NOT(...)) is the same condition, which the
					// compiler finds no comparison in.
					plain, err := s.Query(ctx, Filter{Class: "Task", Where: "NOT(NOT(" + where + "))"}, At(at))
					if err != nil {
						t.Fatalf("%s: %v", where, err)
					}
					var a, b []string
					for _, d := range fast {
						a = append(a, d.ID)
					}
					for _, d := range plain {
						b = append(b, d.ID)
					}
					if !reflect.DeepEqual(a, b) {
						t.Errorf("%s at %s: %v with the index, %v without", where, at.Format(time.DateOnly), a, b)
					}
					// A count has no order to keep, so it is the statement
					// that seeks the index.
					count, err := s.Count(ctx, Filter{Class: "Task", Where: where}, At(at))
					if err != nil || count != int64(len(b)) {
						t.Errorf("%s at %s: the count is %d, %v; %d match", where, at.Format(time.DateOnly), count, err, len(b))
					}
					n += len(a)
				}
			}
		}
	}
	if n == 0 {
		t.Error("no filter matched anything")
	}
}
