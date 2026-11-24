package store

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The case of ADR-0067: at Acme, Jan has a salary review and Ewa is his
// manager. Olga is a content administrator, Marek an analyst and Tomek a
// database administrator. Hana of HR writes the reviews.
const salaryReview = `{
  "name": "SalaryReview",
  "variables": {
    "employee": {"type": "text", "required": true},
    "manager":  {"type": "text", "required": true},
    "salary":   {"type": "number"},
    "scan":     {"type": "object", "schema": "File"}
  },
  "states": {"initial": "draft", "list": ["draft", "agreed"], "final": ["agreed"]},
  "transitions": [{"name": "agree", "from": "draft", "to": "agreed"}],
  "access": {
    "read":   "employee = USER() OR manager = USER()",
    "create": "INGROUP(\"hr\")",
    "update": "manager = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {"agree": "manager = USER()"}
  },
  "rights": {"*": {"salary": {"read": "manager = USER()"}}}
}`

var (
	acmeJan   = Principal{ID: "jan", Kind: ActorPerson}
	acmeEwa   = Principal{ID: "ewa", Kind: ActorPerson}
	acmeHana  = Principal{ID: "hana", Kind: ActorPerson, Groups: []string{"hr"}}
	acmeOlga  = Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupContentAdministrators}}
	acmeMarek = Principal{ID: "marek", Kind: ActorPerson, System: []string{GroupAnalysts}}
	acmeTomek = Principal{ID: "tomek", Kind: ActorPerson, System: []string{GroupAdministrators}}
	why       = Reason("Jan asked HR to correct his manager")
)

// acme opens a store with the class and two reviews, Jan's and Ann's,
// both managed by Ewa, and one Olga manages herself.
func acme(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(salaryReview), asSetup); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, r := range []struct{ name, employee, manager string }{{"jan", "jan", "ewa"}, {"ann", "ann", "ewa"}, {"olgas", "piotr", "olga"}} {
		d, err := s.Create(ctx, "SalaryReview", Values{"employee": r.employee, "manager": r.manager, "salary": 21000}, As(acmeHana))
		if err != nil {
			t.Fatal(err)
		}
		ids[r.name] = d.ID
	}
	return s, ids
}

// logOf is the log of reads as Tomek reads it.
func logOf(t *testing.T, s *Store, f ReadsFilter) []Read {
	t.Helper()
	reads, err := s.Reads(context.Background(), f, As(acmeTomek))
	if err != nil {
		t.Fatal(err)
	}
	return reads
}

// Only what the override shows is recorded, and only with a reason
// (ADR-0067, items 1 to 3).
func TestReads_WhatIsRecorded(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	// Without a reason Olga reads as anybody: Jan's review answers that a
	// reason is needed, at the place of the read rule.
	_, err := s.Get(ctx, "SalaryReview", ids["jan"], As(acmeOlga))
	if e, ok := err.(*Error); !ok || e.Code != CodeReasonRequired || e.Path != "/access/read" {
		t.Fatalf("a read past the rule with no reason: %v", err)
	}
	// The review she manages herself is hers to read, and costs no entry.
	if d, err := s.Get(ctx, "SalaryReview", ids["olgas"], As(acmeOlga)); err != nil || d.Values["salary"] != 21000.0 {
		t.Fatalf("a read the rules allow: %v %s", err, dump(d))
	}
	if docs, err := s.Query(ctx, Filter{Class: "SalaryReview"}, As(acmeOlga)); err != nil || len(docs) != 1 {
		t.Fatalf("a list with no reason: %v %d", err, len(docs))
	}
	if reads := logOf(t, s, ReadsFilter{}); len(reads) != 0 {
		t.Fatalf("reads the rules allow were recorded: %s", dump(reads))
	}
	// With a reason she sees Jan's review whole, and one entry keeps it.
	d, err := s.Get(ctx, "SalaryReview", ids["jan"], As(acmeOlga), why)
	if err != nil || d.Values["salary"] != 21000.0 {
		t.Fatalf("a read with a reason: %v %s", err, dump(d))
	}
	reads := logOf(t, s, ReadsFilter{})
	if len(reads) != 1 {
		t.Fatalf("%d entries, want 1: %s", len(reads), dump(reads))
	}
	r := reads[0]
	if r.By != "olga" || r.Kind != ActorPerson || r.Command != "get" || r.Class != "SalaryReview" || !reflect.DeepEqual(r.Documents, []string{ids["jan"]}) ||
		!reflect.DeepEqual(r.Rules, []string{"/access/read", "/rights/draft/salary/read"}) || r.Reason != "Jan asked HR to correct his manager" || r.At == 0 || r.ReadAt != nil {
		t.Errorf("the entry: %s", dump(r))
	}
	// A list names, in one entry, every review only the override showed.
	if docs, err := s.Query(ctx, Filter{Class: "SalaryReview"}, As(acmeOlga), why); err != nil || len(docs) != 3 {
		t.Fatalf("a list with a reason: %v %d", err, len(docs))
	}
	if n, err := s.Count(ctx, Filter{Class: "SalaryReview"}, As(acmeOlga), why); err != nil || n != 3 {
		t.Fatalf("a count with a reason: %v %d", err, n)
	}
	if _, err := s.Flow(ctx, "SalaryReview", FlowPeriod{}, As(acmeOlga), why); err != nil {
		t.Fatal(err)
	}
	reads = logOf(t, s, ReadsFilter{After: reads[0].Seq})
	if len(reads) != 3 {
		t.Fatalf("%d entries, want 3: %s", len(reads), dump(reads))
	}
	if q := reads[0]; q.Command != "query" || !reflect.DeepEqual(q.Documents, []string{ids["jan"], ids["ann"]}) || q.Counted != nil {
		t.Errorf("the entry of the list: %s", dump(q))
	}
	for _, r := range reads[1:] {
		if r.Documents != nil || r.Counted == nil || *r.Counted != 2 {
			t.Errorf("the entry of %s: %s", r.Command, dump(r))
		}
	}
	// A read at another moment says which (ADR-0042).
	if _, err := s.Get(ctx, "SalaryReview", ids["ann"], As(acmeOlga), why, At(start)); err != nil {
		t.Fatal(err)
	}
	if last := logOf(t, s, ReadsFilter{Command: "get", After: reads[2].Seq}); len(last) != 1 || last[0].ReadAt == nil || *last[0].ReadAt != start.Unix() {
		t.Errorf("a read at a moment: %s", dump(last))
	}
	// The history, the events, a version and the listing of transitions
	// are reads of the document too.
	for name, read := range map[string]func(...Option) error{
		"history":     func(o ...Option) error { _, err := s.History(ctx, "SalaryReview", ids["jan"], o...); return err },
		"events":      func(o ...Option) error { _, err := s.Events(ctx, "SalaryReview", ids["jan"], o...); return err },
		"version":     func(o ...Option) error { _, err := s.Version(ctx, "SalaryReview", ids["jan"], 1, o...); return err },
		"transitions": func(o ...Option) error { _, err := s.Transitions(ctx, "SalaryReview", ids["jan"], o...); return err },
	} {
		if err := read(As(acmeOlga)); code(err) != CodeReasonRequired {
			t.Errorf("%s with no reason: %v", name, err)
		}
		if err := read(As(acmeOlga), why); err != nil {
			t.Errorf("%s with a reason: %v", name, err)
		}
		if got := logOf(t, s, ReadsFilter{Command: name}); len(got) != 1 || !reflect.DeepEqual(got[0].Documents, []string{ids["jan"]}) {
			t.Errorf("the entry of %s: %s", name, dump(got))
		}
	}
	// Who may only see what the rules give is never recorded, whatever
	// they say.
	if _, err := s.Get(ctx, "SalaryReview", ids["jan"], As(acmeJan), why); err != nil {
		t.Fatal(err)
	}
	if got := logOf(t, s, ReadsFilter{By: "jan"}); len(got) != 0 {
		t.Errorf("Jan's read of his own review was recorded: %s", dump(got))
	}
	// "Who has seen Jan's review?" has one exact answer.
	seen := map[string]int{}
	for _, r := range logOf(t, s, ReadsFilter{Class: "SalaryReview", ID: ids["jan"]}) {
		seen[r.Command]++
	}
	want := map[string]int{"get": 1, "query": 1, "history": 1, "events": 1, "version": 1, "transitions": 1}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("the reads of Jan's review: %v, want %v", seen, want)
	}
}

// What a change answers is a read too: with a reason it is whole, and
// what it shows past a read rule that its event does not name is written
// down with the commit. A preview keeps no event, so the log keeps all
// it showed past a rule (ADR-0067, items 2 and 9).
func TestReads_TheAnswerOfAChange(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	// The change past the update rule names the read rule on its event,
	// and the right on salary in the log of reads.
	d, err := s.Set(ctx, "SalaryReview", ids["jan"], Values{"salary": 21500}, As(acmeOlga), why)
	if err != nil || d.Values["salary"] != 21500.0 {
		t.Fatalf("a change with a reason: %v %s", err, dump(d))
	}
	reads := logOf(t, s, ReadsFilter{})
	if len(reads) != 1 || reads[0].Command != "set" || !reflect.DeepEqual(reads[0].Rules, []string{"/rights/draft/salary/read"}) {
		t.Errorf("the entry of the change: %s", dump(reads))
	}
	pv, err := s.Preview(ctx, "SalaryReview", ids["ann"], Values{"salary": 22000}, "", As(acmeOlga), why)
	if err != nil || pv.Document.Values["salary"] != 22000.0 {
		t.Fatalf("a preview with a reason: %v %s", err, dump(pv))
	}
	reads = logOf(t, s, ReadsFilter{Command: "preview"})
	if len(reads) != 1 || !reflect.DeepEqual(reads[0].Rules, []string{"/access/read", "/rights/draft/salary/read"}) {
		t.Errorf("the entry of the preview: %s", dump(reads))
	}
	// A preview of many keeps one entry for the batch, written once the
	// batch is taken back.
	if _, err := s.PreviewMany(ctx, "SalaryReview", "agree", []string{ids["jan"], ids["ann"]}, nil, As(acmeOlga), why); err != nil {
		t.Fatal(err)
	}
	reads = logOf(t, s, ReadsFilter{Command: "preview_many"})
	if len(reads) != 1 || !reflect.DeepEqual(reads[0].Documents, []string{ids["jan"], ids["ann"]}) {
		t.Errorf("the entry of the preview of many: %s", dump(reads))
	}
	if got := logOf(t, s, ReadsFilter{Command: "transition"}); len(got) != 0 {
		t.Errorf("the moves the preview took back left entries: %s", dump(got))
	}
	// Without a reason she is answered as anybody: the change is refused,
	// and nothing is recorded.
	if _, err := s.Set(ctx, "SalaryReview", ids["jan"], Values{"salary": 21600}, As(acmeOlga)); code(err) != CodeReasonRequired {
		t.Errorf("a change past the rules with no reason: %v", err)
	}
	if got := logOf(t, s, ReadsFilter{}); len(got) != 3 {
		t.Errorf("%d entries, want 3", len(got))
	}
}

// Every SQL statement is recorded, the failing ones too, and never its
// rows; the log itself is not a view (ADR-0067, items 4 and 5).
func TestReads_EverySQLStatement(t *testing.T) {
	s, _ := acme(t)
	ctx := context.Background()
	out, err := s.SQL(ctx, `SELECT employee, salary FROM SalaryReview WHERE salary > ?`, []any{20000}, As(acmeMarek))
	if err != nil || len(out.Rows) != 3 {
		t.Fatalf("the statement: %v %s", err, dump(out))
	}
	if _, err := s.SQL(ctx, `SELECT nothing FROM nowhere`, nil, As(acmeMarek), Reason("trying a name")); err == nil {
		t.Fatal("a statement over no view ran")
	}
	if _, err := s.SQL(ctx, `SELECT * FROM fado_reads`, nil, As(acmeMarek)); err == nil {
		t.Error("SQL reads the log of reads")
	}
	// Who is not an analyst runs nothing, and nothing is recorded.
	if _, err := s.SQL(ctx, `SELECT 1`, nil, As(acmeOlga)); code(err) != CodeForbidden {
		t.Errorf("a statement by a content administrator: %v", err)
	}
	reads := logOf(t, s, ReadsFilter{Command: "sql"})
	if len(reads) != 3 {
		t.Fatalf("%d statements recorded, want 3: %s", len(reads), dump(reads))
	}
	first := reads[0]
	if first.By != "marek" || first.Statement != `SELECT employee, salary FROM SalaryReview WHERE salary > ?` || !reflect.DeepEqual(first.Args, []any{20000.0}) ||
		first.Rows == nil || *first.Rows != 3 || first.Failure != "" || first.Reason != "" || first.Documents != nil {
		t.Errorf("the entry of the statement: %s", dump(first))
	}
	if second := reads[1]; second.Rows != nil || second.Failure == "" || second.Reason != "trying a name" {
		t.Errorf("the entry of the failing statement: %s", dump(second))
	}
}

// Database administrators read the log, and nobody else (ADR-0067,
// item 5).
func TestReads_WhoReadsTheLog(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	for _, p := range []Principal{acmeOlga, acmeMarek, acmeJan, {}} {
		_, err := s.Reads(ctx, ReadsFilter{}, As(p))
		if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/system/administrators" {
			t.Errorf("%q reads the log: %v", p.ID, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Get(ctx, "SalaryReview", ids["jan"], As(acmeOlga), why); err != nil {
			t.Fatal(err)
		}
	}
	if got := logOf(t, s, ReadsFilter{Limit: 2}); len(got) != 2 {
		t.Errorf("a limit of 2 answered %d", len(got))
	}
	all := logOf(t, s, ReadsFilter{})
	if got := logOf(t, s, ReadsFilter{After: all[0].Seq}); len(got) != 2 || got[0].Seq != all[1].Seq {
		t.Errorf("reading on after the first: %s", dump(got))
	}
	if got := logOf(t, s, ReadsFilter{Since: time.Unix(all[2].At+1, 0)}); len(got) != 0 {
		t.Errorf("since after the last: %d", len(got))
	}
	if got := logOf(t, s, ReadsFilter{Until: time.Unix(all[0].At, 0)}); len(got) != 0 {
		t.Errorf("until the first: %d", len(got))
	}
	for _, f := range []ReadsFilter{{ID: ids["jan"]}, {Limit: MaxReads + 1}, {Since: start, Until: start}} {
		if _, err := s.Reads(ctx, f, As(acmeTomek)); code(err) != CodeBadValue {
			t.Errorf("%+v: %v", f, err)
		}
	}
}

// No trace, no answer (ADR-0067, item 6): a read whose entry cannot be
// written shows nothing.
func TestReads_NoTraceNoAnswer(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	if _, err := s.writer.ExecContext(ctx, "DROP TABLE fado_reads"); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Get(ctx, "SalaryReview", ids["jan"], As(acmeOlga), why); code(err) != CodeStorage || d != nil {
		t.Errorf("a read that leaves no trace: %v %s", err, dump(d))
	}
	if out, err := s.SQL(ctx, `SELECT 1`, nil, As(acmeMarek)); code(err) != CodeStorage || out != nil {
		t.Errorf("a statement that leaves no trace: %v %s", err, dump(out))
	}
	// A read nobody records is not touched.
	if _, err := s.Get(ctx, "SalaryReview", ids["jan"], As(acmeEwa)); err != nil {
		t.Errorf("Ewa's read: %v", err)
	}
}

// The stream of changes: one entry for each batch and class it told of
// past the rule (ADR-0067, item 3).
func TestReads_TheStream(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	quiet := listening(t, s, ChangeFilter{}, As(acmeOlga))
	told := listening(t, s, ChangeFilter{}, Option(func(o *options) { As(acmeOlga)(o); why(o) }))
	if _, err := s.Set(ctx, "SalaryReview", ids["olgas"], Values{"salary": 1}, As(acmeOlga)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "SalaryReview", ids["jan"], Values{"salary": 2}, As(acmeEwa)); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*Changes{"with no reason": quiet, "with a reason": told} {
		if _, err := c.fill(ctx, 0); err != nil {
			t.Fatal(err)
		}
		var heard []string
		for _, ev := range c.read {
			heard = append(heard, ev.DocID)
		}
		want := []string{ids["olgas"]}
		if c == told {
			want = []string{ids["olgas"], ids["jan"]}
		}
		if !reflect.DeepEqual(heard, want) {
			t.Errorf("%s she hears of %v, want %v", name, heard, want)
		}
	}
	reads := logOf(t, s, ReadsFilter{Command: "subscribe"})
	if len(reads) != 1 || !reflect.DeepEqual(reads[0].Documents, []string{ids["jan"]}) || reads[0].Reason == "" {
		t.Errorf("the entry of the stream: %s", dump(reads))
	}
}

// Files: one opened through a document, past a right, and one opened by
// its hash alone, which always needs a reason (ADR-0067, item 2).
func TestReads_Files(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	scan, err := s.PutBlob(ctx, "review.pdf", "application/pdf", bytes.NewReader([]byte("%PDF-1.4 a review")), As(acmeHana))
	if err != nil {
		t.Fatal(err)
	}
	hash := scan["hash"].(string)
	if _, err := s.Set(ctx, "SalaryReview", ids["jan"], Values{"scan": scan}, As(acmeEwa)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenBlob(ctx, hash, As(acmeOlga)); code(err) != CodeReasonRequired {
		t.Errorf("a file by its hash with no reason: %v", err)
	}
	if _, _, err := s.OpenFile(ctx, "SalaryReview", ids["jan"], hash, As(acmeOlga)); code(err) != CodeReasonRequired {
		t.Errorf("a file of a document past its rule with no reason: %v", err)
	}
	for _, open := range []func() (io.ReadCloser, error){
		func() (io.ReadCloser, error) { rc, _, err := s.OpenBlob(ctx, hash, As(acmeOlga), why); return rc, err },
		func() (io.ReadCloser, error) {
			rc, _, err := s.OpenFile(ctx, "SalaryReview", ids["jan"], hash, As(acmeOlga), why)
			return rc, err
		},
	} {
		rc, err := open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.HasPrefix(string(b), "%PDF") {
			t.Errorf("the bytes: %q", b)
		}
	}
	reads := logOf(t, s, ReadsFilter{})
	if len(reads) != 2 || reads[0].Command != "open_blob" || reads[0].Blob != hash || reads[0].Documents != nil ||
		reads[1].Command != "open_file" || reads[1].Blob != hash || !reflect.DeepEqual(reads[1].Documents, []string{ids["jan"]}) {
		t.Errorf("the entries of the files: %s", dump(reads))
	}
}

// Tables: an import past the write rule and its entry are one
// transaction, and the worker's table action carries the reason of the
// commit that queued it (ADR-0067, item 7).
func TestReads_Tables(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Grades", "columns": {"employee": "text", "grade": "number"}, "key": ["employee"],
		"access": {"read": "INGROUP(\"hr\")", "write": "INGROUP(\"hr\")"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	rows := []Values{{"employee": "jan", "grade": 3}, {"employee": "ann", "grade": 4}}
	_, err := s.ImportRows(ctx, "Grades", rows, As(acmeOlga))
	if e, ok := err.(*Error); !ok || e.Code != CodeReasonRequired || e.Path != "/access/write" {
		t.Errorf("an import past the rule with no reason: %v", err)
	}
	if _, err := s.ImportRows(ctx, "Grades", rows, As(acmeOlga), Reason("HR is away")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TableRows(ctx, "Grades", As(acmeOlga)); code(err) != CodeReasonRequired {
		t.Errorf("rows past the rule with no reason: %v", err)
	}
	if got, err := s.TableRows(ctx, "Grades", As(acmeOlga), Reason("HR is away")); err != nil || len(got) != 2 {
		t.Fatalf("rows with a reason: %v %d", err, len(got))
	}
	// HR's own import is HR's work, and costs no entry.
	if _, err := s.ImportRows(ctx, "Grades", rows, As(acmeHana)); err != nil {
		t.Fatal(err)
	}
	reads := logOf(t, s, ReadsFilter{Table: "Grades"})
	if len(reads) != 2 || reads[0].Command != "import_rows" || reads[0].Rows == nil || *reads[0].Rows != 2 || !reflect.DeepEqual(reads[0].Rules, []string{"/access/write"}) ||
		reads[1].Command != "table_rows" || !reflect.DeepEqual(reads[1].Rules, []string{"/access/read"}) {
		t.Errorf("the entries of the table: %s", dump(reads))
	}
	// The class writes a grade when a review is agreed. Olga agrees Jan's
	// past the rule of the transition, with a reason, and the worker's
	// row past the table's rule is recorded with it.
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(salaryReview, `"rights":`,
		`"rules": [{"on": "transition", "name": "agree", "do": [{"action": "table.insert", "table": "Grades", "row": {"employee": "\"x-\" & employee", "grade": "5"}}]}], "rights":`, 1)), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "SalaryReview", ids["jan"], "agree", nil, As(acmeOlga), why); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("draining: %d %v", n, err)
	}
	reads = logOf(t, s, ReadsFilter{Command: "table.insert"})
	if len(reads) != 1 || reads[0].Table != "Grades" || reads[0].By != "olga" || reads[0].Reason != "Jan asked HR to correct his manager" {
		t.Errorf("the entry of the worker's row: %s", dump(reads))
	}
}

// A class the engine brings is seen by a content administrator as any
// other, with no reason and no entry (ADR-0052).
func TestReads_TheEnginesClasses(t *testing.T) {
	s, _ := acme(t)
	ctx := context.Background()
	gives(t, s, "ewa", "jan")
	docs, err := s.Query(ctx, Filter{Class: "Substitution"}, As(acmeOlga))
	if err != nil || len(docs) != 1 {
		t.Fatalf("the substitutions: %v %d", err, len(docs))
	}
	if reads := logOf(t, s, ReadsFilter{}); len(reads) != 0 {
		t.Errorf("a read of the engine's class was recorded: %s", dump(reads))
	}
}
