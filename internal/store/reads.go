package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The log of reads (ADR-0067). A content administrator sees past the
// rules only with a reason, and every document, row and file that only
// the override showed is written down, with the rules it got past and
// the reason; so is every SQL statement an analyst runs. The entry is
// committed before the answer leaves: no trace, no answer. Database
// administrators read the log with Reads; SQL cannot, since the log is
// a table of the engine's and not a view.

// readsDDL makes the table of the log. Nothing in the engine changes or
// deletes a row of it (ADR-0067, item 8).
var readsDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_reads (seq INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, by_id TEXT NOT NULL, by_kind TEXT NOT NULL, by_for TEXT NOT NULL, command TEXT NOT NULL, class TEXT NOT NULL, tbl TEXT NOT NULL, documents TEXT NOT NULL, rules TEXT NOT NULL, reason TEXT NOT NULL, statement TEXT NOT NULL, args TEXT NOT NULL, rows INTEGER, counted INTEGER, blob TEXT NOT NULL, read_at INTEGER, failure TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_reads_by ON fado_reads (by_id, seq)`,
	`CREATE INDEX IF NOT EXISTS fado_reads_class ON fado_reads (class, seq)`,
}

// Read is one entry of the log of reads (ADR-0067, item 3): when, who,
// of what kind and for whom, the command, the class or the table, the
// documents it showed past a rule and the places of the rules, and the
// reason. A count names no document and says under Counted how many
// documents only the override counted; a table's rows say how many under
// Rows; a file opened by its hash names it under Blob. A SQL statement
// keeps its text, its arguments, and how many rows it returned or the
// code it failed with. ReadAt is the moment the command read at when it
// was not now (ADR-0042).
type Read struct {
	Seq       int64    `json:"seq"`
	At        int64    `json:"at"`
	By        string   `json:"by,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	For       string   `json:"for,omitempty"`
	Command   string   `json:"command"`
	Class     string   `json:"class,omitempty"`
	Table     string   `json:"table,omitempty"`
	Documents []string `json:"documents,omitempty"`
	Rules     []string `json:"rules,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Statement string   `json:"statement,omitempty"`
	Args      []any    `json:"args,omitempty"`
	Rows      *int64   `json:"rows,omitempty"`
	Counted   *int64   `json:"counted,omitempty"`
	Blob      string   `json:"blob,omitempty"`
	ReadAt    *int64   `json:"read_at,omitempty"`
	Failure   string   `json:"failure,omitempty"`
}

// ReadsFilter narrows the log of reads. Every member is optional: By the
// caller, Class and ID a document (ID needs Class), Table a table,
// Command the command; Since and Until the period, from Since and before
// Until, as the flow of a class takes it; After the seq to read on from;
// Limit the most entries to answer, DefaultReads when 0, at most
// MaxReads.
type ReadsFilter struct {
	By, Class, ID, Table, Command string
	Since, Until                  time.Time
	After                         int64
	Limit                         int
}

// The size of one answer of Reads.
const (
	DefaultReads = 1000
	MaxReads     = 10000
)

// readLog is the entry one call is making while it reads. past says the
// call showed something past a rule, so that the entry is written.
type readLog struct {
	command, class, table string
	docs                  []string
	seen                  map[string]bool
	rules                 []string
	counted, rows         int64
	hasCounted, hasRows   bool
	blob                  string
	sql                   bool
	statement             string
	args                  []any
	failure               string
	past                  bool
}

// newRead starts the entry of a call over a class.
func newRead(command string, c *class) *readLog {
	r := &readLog{command: command}
	if c != nil {
		r.class = c.cls.Name
	}
	return r
}

// newRead starts the entry of a call only when the call may need one: a
// content administrator's with a reason, in a class the engine does not
// bring. Every other read makes none and allocates nothing for it.
func (o *options) newRead(command string, c *class) *readLog {
	if !o.records(c) {
		return nil
	}
	return newRead(command, c)
}

// saw notes a document the call showed past the rules named. A document
// shown under no rule is not noted.
func (r *readLog) saw(id string, rules ...string) {
	if r == nil || len(rules) == 0 {
		return
	}
	r.past = true
	if !r.seen[id] {
		if r.seen == nil {
			r.seen = map[string]bool{}
		}
		r.seen[id] = true
		r.docs = append(r.docs, id)
	}
	for _, rule := range rules {
		known := false
		for _, have := range r.rules {
			known = known || have == rule
		}
		if !known {
			r.rules = append(r.rules, rule)
		}
	}
}

// passes reports whether a content administrator gets past the rules of
// the class with this call: in a class the engine brings always, as they
// see any document of it and with no mark (ADR-0052); elsewhere only
// with a reason (ADR-0067, item 2).
func (o *options) passes(c *class) bool {
	return o.principal.content() && (c.brought || o.reason != "")
}

// records reports whether what the call shows past a rule of the class
// is written in the log of reads.
func (o *options) records(c *class) bool {
	return o.principal.content() && o.reason != "" && !c.brought
}

// rightRead is the place of the read right of a variable in a state, as
// the place of a write right is written (ADR-0052).
func rightRead(state, variable string) string {
	return "/rights/" + state + "/" + variable + "/read"
}

// reasonToRead refuses a content administrator who would see past a rule
// and gave no reason (ADR-0067, item 2), with the place of the rule.
func reasonToRead(p Principal, what, path string) *Error {
	e := hintf(CodeReasonRequired, "", who(p)+" would see "+what+" past a rule, as a content administrator only, and a read past a rule needs a reason",
		"give the reason with the command; the log of reads keeps it beside the rule, and without one the rules apply as to anybody")
	e.Path = path
	return e
}

// look decides whether a read shows a stored document to its caller: the
// rules let the caller through; or a content administrator passes them
// with a reason, and the entry notes the document; or, without one, the
// read is refused with reason_required. false is a document the caller
// does not see, which does not exist for them (ADR-0049, item 3).
func (s *Store) look(c, vc *class, row *docRow, o options, r *readLog) (bool, error) {
	if s.seesRow(c, vc, row, o) {
		return true, nil
	}
	if !o.principal.content() {
		return false, nil
	}
	if o.reason == "" {
		return false, reasonToRead(o.principal, "the document "+row.id+" of "+c.cls.Name, "/access/read")
	}
	r.saw(row.id, "/access/read")
	return true, nil
}

// lookID is look over the document with the id as it is stored now, or
// as it was last when it was deleted (ADR-0049, item 6). false for an id
// no document has.
func (s *Store) lookID(ctx context.Context, c *class, id string, o options, r *readLog) (bool, error) {
	vc, row, err := s.storedRow(ctx, c, id)
	if err != nil || row == nil {
		return false, err
	}
	return s.look(c, vc, row, o, r)
}

// pastRow names the places of the rules that would keep an ordinary
// caller from seeing the row whole: the read rule, and the read right of
// each variable it hides in the row's state. The evaluator is the
// caller's, so that a read path and the writer can both ask.
func (s *Store) pastRow(ev *evaluator, c, vc *class, row *docRow, p Principal, now int64) []string {
	if vc != c {
		row = c.adopted(vc, row)
	}
	ctx := contextOf(p, now)
	ctx.State, ctx.ID = row.state, row.id
	var rules []string
	if ok, _ := ev.may(c, ruleRead, row, &ctx); !ok {
		rules = append(rules, "/access/read")
	}
	if c.rights == nil {
		return rules
	}
	for i, cell := range c.cells(row.state) {
		if cell.read == nil {
			continue
		}
		if ok, _ := ev.allowed(c, cell.read, row, &ctx); !ok {
			rules = append(rules, rightRead(row.state, c.vars[i].v.Name))
		}
	}
	return rules
}

// logRead writes the entries in a transaction of their own through the
// writer, before the call answers (ADR-0067, item 6). An entry that
// showed nothing past a rule is not written.
func (s *Store) logRead(ctx context.Context, o options, entries ...*readLog) error {
	if !anyPast(entries) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logReadHeld(ctx, o, entries...)
}

// logReadHeld is logRead for a caller that holds the writer's lock and
// no transaction.
func (s *Store) logReadHeld(ctx context.Context, o options, entries ...*readLog) error {
	if !anyPast(entries) {
		return nil
	}
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	w := s.writer
	if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin the log of reads", err)
	}
	for _, r := range entries {
		if err := s.insertRead(ctx, w, o, r); err != nil {
			_, _ = w.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
			return err
		}
	}
	// The log of events did not grow, so no stream is woken.
	if err := s.endQuietly(ctx); err != nil {
		_, _ = w.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return storageErr("write the log of reads", err)
	}
	return nil
}

func anyPast(entries []*readLog) bool {
	for _, r := range entries {
		if r != nil && r.past {
			return true
		}
	}
	return false
}

// insertRead writes one entry with w, inside the transaction its caller
// holds, so that a write past a table's rule and its entry are one.
func (s *Store) insertRead(ctx context.Context, w execer, o options, r *readLog) error {
	if r == nil || !r.past {
		return nil
	}
	docs, rules := r.docs, r.rules
	if docs == nil {
		docs = []string{}
	}
	if rules == nil {
		rules = []string{}
	}
	docsText, _ := json.Marshal(docs)
	rulesText, _ := json.Marshal(rules)
	args := ""
	if r.sql && len(r.args) > 0 {
		text, err := json.Marshal(r.args)
		if err != nil {
			text, _ = json.Marshal(fmt.Sprint(r.args))
		}
		args = string(text)
	}
	var rows, counted, readAt any
	if r.hasRows {
		rows = r.rows
	}
	if r.hasCounted {
		counted = r.counted
	}
	if !o.at.IsZero() {
		readAt = o.at.Unix()
	}
	p := o.principal
	if _, err := w.ExecContext(ctx, `INSERT INTO fado_reads (at, by_id, by_kind, by_for, command, class, tbl, documents, rules, reason, statement, args, rows, counted, blob, read_at, failure) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.now().Unix(), p.ID, p.Kind, p.For, r.command, r.class, r.table, string(docsText), string(rulesText), o.reason, r.statement, args, rows, counted, r.blob, readAt, r.failure); err != nil {
		return storageErr("write the log of reads", err)
	}
	return nil
}

// Reads lists the log of reads, oldest first (ADR-0067, item 5). It is
// for database administrators: whoever reads past the rules is not
// whoever checks the log, unless the host puts one person in both
// groups.
func (s *Store) Reads(ctx context.Context, f ReadsFilter, opts ...Option) ([]Read, error) {
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, e
	}
	if !o.principal.in(GroupAdministrators) {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not read the log of reads",
			"the log of reads is for the members of the system group "+GroupAdministrators+", so that whoever reads past the rules is not whoever checks the log")
		e.Path = "/system/" + GroupAdministrators
		return nil, e
	}
	if f.ID != "" && f.Class == "" {
		return nil, hintf(CodeBadValue, "", "id names a document, and no class was given", "give the class of the document too")
	}
	if f.Limit < 0 || f.Limit > MaxReads {
		return nil, hintf(CodeBadValue, "", fmt.Sprintf("limit is %d; it is at most %d", f.Limit, MaxReads), "ask again from after, the seq of the last entry answered")
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Until.After(f.Since) {
		return nil, hintf(CodeBadValue, "", "the period ends before it begins", "give an until that is after since, or leave one out")
	}
	limit := f.Limit
	if limit == 0 {
		limit = DefaultReads
	}
	var where []string
	var args []any
	add := func(cond string, arg any) {
		where = append(where, cond)
		args = append(args, arg)
	}
	if f.After > 0 {
		add("seq > ?", f.After)
	}
	if f.By != "" {
		add("by_id = ?", f.By)
	}
	if f.Class != "" {
		name := f.Class
		if c, e := s.class(f.Class); e == nil {
			name = c.cls.Name
		}
		add("class = ?", name)
	}
	if f.ID != "" {
		add("EXISTS (SELECT 1 FROM json_each(documents) WHERE value = ?)", f.ID)
	}
	if f.Table != "" {
		name := f.Table
		if t, e := s.table(f.Table); e == nil {
			name = t.def.Name
		}
		add("tbl = ?", name)
	}
	if f.Command != "" {
		add("command = ?", f.Command)
	}
	if !f.Since.IsZero() {
		add("at >= ?", f.Since.Unix())
	}
	if !f.Until.IsZero() {
		add("at < ?", f.Until.Unix())
	}
	text := "SELECT seq, at, by_id, by_kind, by_for, command, class, tbl, documents, rules, reason, statement, args, rows, counted, blob, read_at, failure FROM fado_reads"
	if len(where) > 0 {
		text += " WHERE " + strings.Join(where, " AND ")
	}
	text += " ORDER BY seq LIMIT " + strconv.Itoa(limit)
	rows, err := s.db.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, storageErr("read the log of reads", err)
	}
	defer rows.Close()
	out := []Read{}
	for rows.Next() {
		var r Read
		var docs, rules, argsText string
		var nrows, counted, readAt *int64
		if err := rows.Scan(&r.Seq, &r.At, &r.By, &r.Kind, &r.For, &r.Command, &r.Class, &r.Table, &docs, &rules, &r.Reason, &r.Statement, &argsText, &nrows, &counted, &r.Blob, &readAt, &r.Failure); err != nil {
			return nil, storageErr("read the log of reads", err)
		}
		r.Rows, r.Counted, r.ReadAt = nrows, counted, readAt
		if err := errors.Join(json.Unmarshal([]byte(docs), &r.Documents), json.Unmarshal([]byte(rules), &r.Rules)); err != nil {
			return nil, &Error{Code: CodeStorage, Message: fmt.Sprintf("the entry %d of the log of reads cannot be read", r.Seq)}
		}
		if argsText != "" {
			if err := json.Unmarshal([]byte(argsText), &r.Args); err != nil {
				return nil, &Error{Code: CodeStorage, Message: fmt.Sprintf("the arguments of the entry %d of the log of reads cannot be read", r.Seq)}
			}
		}
		if len(r.Documents) == 0 {
			r.Documents = nil
		}
		if len(r.Rules) == 0 {
			r.Rules = nil
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the log of reads", err)
	}
	return out, nil
}

// sortedNames is the keys of a set in order, for entries that are
// written the same way every time.
func sortedNames(set map[string][]string) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
