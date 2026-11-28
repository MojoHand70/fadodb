package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Filter is a query over a class (ADR-0038).
type Filter = query.Filter

// Rows is the result of a SQL statement: the column names and the rows,
// each value as the database returns it, an integer, a float, text, bytes
// or nil.
type Rows struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// MaxSQLRows is the most rows a SQL statement may return.
const MaxSQLRows = 10000

// Query reads the documents of a class that match a filter, in its order,
// at most its limit, with the fields it names (ADR-0038), each projected
// for the principal (ADR-0041): a variable the principal may not read in
// a row's state is absent from that document, and a variable the where
// or the order names matches and sorts only where the principal may read
// it.
func (s *Store) Query(ctx context.Context, f Filter, opts ...Option) ([]*Document, error) {
	c, e := s.class(f.Class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	plan, err := query.Compile(f, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, filterErr(err)
	}
	if s.feeder != nil {
		docs, _, err := s.pageWithCopy(ctx, c, o, plan, f, nil, "query", false)
		return docs, err
	}
	docs, _, err := s.answer(ctx, c, o, plan, "query", false)
	return docs, err
}

// lookLimit is how far the engine looks into an index before it stops
// counting (ADR-0075, item 10).
const lookLimit = 1000

// lookUpTo is how far a look counts once an earlier one found least
// documents: no further than least, rounded up to 1, 10, 100 or
// lookLimit, so that each fit has at most four statements. A later look
// is taken only when it ends before least, and it does whenever it
// would have before lookLimit.
func lookUpTo(least int) string {
	switch {
	case least <= 1:
		return "1"
	case least <= 10:
		return "10"
	case least <= 100:
		return "100"
	}
	return "1000"
}

// through answers the condition and the parameters to ask a plan with.
// A question that two or more indexes fit is asked through the one that
// ends first: the engine looks into each for the value the question
// compares, for at most lookLimit documents, since the store has no
// numbers on the data to choose by (ADR-0075, item 10). A look after
// the first counts no further than the fewest an earlier one found
// (lookUpTo). Of two looks that end alike the earlier fit is taken,
// and a look that finds nothing ends the looking: through that index
// the answer is empty.
func (s *Store) through(ctx context.Context, c *class, plan *query.Plan, o options) (string, []any, error) {
	named := s.contextArgs(o)
	if len(plan.Fits) < 2 {
		return plan.Where, append(plan.Args, named...), nil
	}
	best, least := 0, lookLimit+1
	for i := range plan.Fits {
		f := &plan.Fits[i]
		text := "SELECT count(*) FROM (SELECT 1 FROM " + quote(c.table) + " WHERE " + f.Look + " LIMIT " + lookUpTo(least) + ")"
		st, err := s.filters.acquire(ctx, s.db, text)
		if err != nil {
			return "", nil, storageErr("prepare a look into "+c.cls.Name, err)
		}
		var n int
		err = st.stmt.QueryRowContext(ctx, append(f.Args[:len(f.Args):len(f.Args)], named...)...).Scan(&n)
		s.filters.release(st)
		if err != nil {
			return "", nil, storageErr("look into "+c.cls.Name, err)
		}
		if n < least {
			best, least = i, n
		}
		if n == 0 {
			break
		}
	}
	f := &plan.Fits[best]
	return f.Where, append(f.Args[:len(f.Args):len(f.Args)], named...), nil
}

// bounded gives the context a free question is asked under (ADR-0075,
// item 6). One that an index serves is asked as its caller asked it.
// One that no index serves reads the documents of its class one by one,
// which takes as long as the class is large: it runs for as long as the
// database allows, and stop ends the count of its time.
func (s *Store) bounded(ctx context.Context, plan *query.Plan) (asked context.Context, stop func()) {
	if plan.Served || s.maxQuestion <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.maxQuestion)
}

// stopped answers what a free question failed with: the refusal
// too_wide when the time the database allows it ran out and its caller
// was still waiting, with the variable an index would be needed on, and
// the failure itself otherwise.
func (s *Store) stopped(ctx, asked context.Context, c *class, plan *query.Plan, failure error) error {
	if asked == ctx || asked.Err() != context.DeadlineExceeded || ctx.Err() != nil {
		return failure
	}
	message := "the question was stopped after " + shortTime(s.maxQuestion) + ": no index serves it, so it reads the documents of " + c.cls.Name + " one by one"
	if plan.Lacks == "" {
		return hintf(CodeTooWide, "where", message, "begin the where with a comparison of a variable that has an index with a value, as order = REF(\"Order\", \"ORD-17\"); a question over everything is asked with sql")
	}
	return hintf(CodeTooWide, plan.Lacks, message, plan.Lacks+` has no index: say "index": true on it in the class `+c.cls.Name)
}

// shortTime writes a length of time for a person: 5 seconds, 0.25
// seconds.
func shortTime(d time.Duration) string {
	if d == time.Second {
		return "1 second"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " seconds"
}

// answer runs a compiled filter over the documents of a class and
// projects each document found for the caller. command is the call the
// log of reads names. With probe it reads one document past the limit,
// leaves it out, and says whether there was one: the caller of a page
// then knows that rows follow, and nothing else was fetched to learn it.
func (s *Store) answer(ctx context.Context, c *class, o options, plan *query.Plan, command string, probe bool) ([]*Document, bool, error) {
	found, more, err := s.find(ctx, c, o, plan, command, probe)
	if err != nil {
		return nil, false, err
	}
	return s.shown(ctx, c, o, plan, command, found, more)
}

// find runs a compiled filter in the store and answers the documents
// it takes, as rows: the page asked for, and with probe whether
// documents follow it.
func (s *Store) find(ctx context.Context, c *class, o options, plan *query.Plan, command string, probe bool) ([]*docRow, bool, error) {
	limit := plan.Limit
	if probe {
		limit++
	}
	cols := append(quoteAll(systemColumns), quoteAll(columnNames(c.cls.Columns))...)
	cols = append(cols, quoteAll(c.atNames())...)
	text := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(c.cls.Name)
	where, args, err := s.through(ctx, c, plan, o)
	if err != nil {
		return nil, false, err
	}
	if where != "" {
		text += " WHERE " + where
	}
	text += " ORDER BY " + plan.Order + " LIMIT " + fmt.Sprint(limit) + " OFFSET " + fmt.Sprint(plan.Offset)
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, false, storageErr("prepare the query of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	// A free question that no index serves runs for as long as the
	// database allows, and no longer (ADR-0075, item 6). A list of a
	// document is a declared question, and is not stopped.
	asked, stop := ctx, func() {}
	if command == "query" {
		asked, stop = s.bounded(ctx, plan)
	}
	defer stop()
	rows, err := st.stmt.QueryContext(asked, args...)
	if err != nil {
		return nil, false, s.stopped(ctx, asked, c, plan, storageErr("query "+c.cls.Name, err))
	}
	var found []*docRow
	for rows.Next() {
		row, err := c.scanRow(rows, false)
		if err != nil {
			rows.Close()
			return nil, false, s.stopped(ctx, asked, c, plan, storageErr("query "+c.cls.Name, err))
		}
		found = append(found, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, s.stopped(ctx, asked, c, plan, storageErr("query "+c.cls.Name, err))
	}
	more := false
	if probe && len(found) > plan.Limit {
		found, more = found[:plan.Limit], true
	}
	return found, more, nil
}

// shown projects the documents found for a question for its caller:
// their lists, for the fields asked for, and of each document what the
// caller may read. more is handed through.
func (s *Store) shown(ctx context.Context, c *class, o options, plan *query.Plan, command string, found []*docRow, more bool) ([]*Document, bool, error) {
	// The composites of every document in one statement per child table,
	// for the fields asked for.
	wanted := map[*child]bool{}
	for _, ch := range c.children {
		if plan.Fields == nil {
			wanted[ch] = true
			continue
		}
		for _, f := range plan.Fields {
			if strings.EqualFold(f, ch.v.Name) {
				wanted[ch] = true
			}
		}
	}
	kids := map[*child]map[string][]childRow{}
	for ch := range wanted {
		byDoc, err := ch.loadCurrent(ctx, s.db, c, found)
		if err != nil {
			return nil, false, err
		}
		kids[ch] = byDoc
	}
	ev := s.reader()
	defer s.release(ev)
	now := s.clock(o)
	// A content administrator who passes the rules sees every document
	// whole; the entry names each one only the override showed, and the
	// rules it got past (ADR-0067, item 3).
	passes, r := o.passes(c), o.newRead(command, c)
	shown := func(vc *class, row *docRow, d *Document) {
		switch {
		case !passes:
			s.project(ev, c, vc, row, d, o.principal, now)
		case o.records(c):
			r.saw(row.id, s.pastRow(ev, c, vc, row, o.principal, now)...)
		}
	}
	docs := make([]*Document, len(found))
	for i, row := range found {
		var d *Document
		if row.classVersion != c.cls.Version && row.classVersion != 0 {
			// A document computed by an older version is read under it.
			vc, vrow, err := s.underVersion(ctx, c, row, false)
			if err != nil {
				return nil, false, err
			}
			vkids, err := vc.loadChildren(ctx, vrow.id, vrow.version, nil, false)
			if err != nil {
				return nil, false, err
			}
			d = vc.document(vrow, vkids)
			shown(vc, vrow, d)
		} else {
			have := map[*child][]childRow{}
			for ch := range wanted {
				have[ch] = kids[ch][row.id]
			}
			d = c.document(row, have)
			shown(c, row, d)
		}
		if plan.Fields != nil {
			// A hidden variable is absent from the fields as from the
			// document, with no refusal.
			values := make(Values, len(plan.Fields))
			var computed map[string]Moment
			for _, f := range plan.Fields {
				if v, ok := d.Values[f]; ok {
					values[f] = v
				}
				if m, ok := d.Computed[f]; ok {
					if computed == nil {
						computed = map[string]Moment{}
					}
					computed[f] = m
				}
			}
			d.Values, d.Computed = values, computed
		}
		docs[i] = d
	}
	if err := s.logRead(ctx, o, r); err != nil {
		return nil, false, err
	}
	return docs, more, nil
}

// caller is the principal as the filter compiler sees it over the class:
// a content administrator past the rules only where they pass them
// (ADR-0067, item 2), and otherwise held by the read rule and the rights
// as anybody.
func caller(o options, c *class) query.Principal {
	return query.Principal{ID: o.principal.ID, Groups: o.principal.Groups, System: o.principal.System, Content: o.passes(c)}
}

// pastCount is how many of the documents a statement over the class
// counts only because a content administrator passes the rules: the
// count with the filter as they pass it, less the count with the filter
// as anybody would have it (ADR-0067, item 3). where is the condition of
// the filter, built by build for a principal.
func (s *Store) pastCount(ctx context.Context, c *class, o options, all int64, build func(query.Principal) (string, []any, error)) (int64, error) {
	ordinary := o
	ordinary.reason = ""
	where, args, err := build(caller(ordinary, c))
	if err != nil {
		return 0, err
	}
	text := "SELECT count(*) FROM " + quote(c.cls.Name)
	if where != "" {
		text += " WHERE " + where
	}
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return 0, storageErr("prepare the count of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	var n int64
	if err := st.stmt.QueryRowContext(ctx, append(args, s.contextArgs(ordinary)...)...).Scan(&n); err != nil {
		return 0, storageErr("count "+c.cls.Name, err)
	}
	return all - n, nil
}

// countInStore runs the count of a compiled filter in the store.
func (s *Store) countInStore(ctx context.Context, c *class, plan *query.Plan, o options) (int64, error) {
	text := "SELECT count(*) FROM " + quote(c.cls.Name)
	where, args, err := s.through(ctx, c, plan, o)
	if err != nil {
		return 0, err
	}
	if where != "" {
		text += " WHERE " + where
	}
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return 0, storageErr("prepare the count of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	asked, stop := s.bounded(ctx, plan)
	defer stop()
	var n int64
	if err := st.stmt.QueryRowContext(asked, args...).Scan(&n); err != nil {
		return 0, s.stopped(ctx, asked, c, plan, storageErr("count "+c.cls.Name, err))
	}
	return n, nil
}

// Count counts the documents of a class that match a filter's where.
func (s *Store) Count(ctx context.Context, f Filter, opts ...Option) (int64, error) {
	c, e := s.class(f.Class)
	if e != nil {
		return 0, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return 0, e
	}
	f.Order, f.Fields, f.Limit, f.Offset = nil, nil, 0, 0
	plan, err := query.Compile(f, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return 0, filterErr(err)
	}
	// In a program that keeps the copy (ADR-0089, item 6): a count that
	// no index narrows reads the class, and is the copy's at once. One
	// that an index narrows is the store's for a moment, since most
	// such counts are of few documents, and the copy's when the moment
	// has passed: the documents of one state may be millions.
	n, counted := int64(0), false
	switch {
	case s.feeder == nil:
	case !plan.Narrowed, copyStoreFirst == 0:
		n, counted = s.countFromCopy(ctx, c, f, o)
	case copyStoreFirst != 0:
		moment, done := context.WithTimeout(ctx, copyStoreFirst)
		m, merr := s.countInStore(moment, c, plan, o)
		passed := merr != nil && moment.Err() != nil && ctx.Err() == nil
		done()
		switch {
		case merr == nil:
			n, counted = m, true
		case passed:
			n, counted = s.countFromCopy(ctx, c, f, o)
		default:
			return 0, merr
		}
	}
	if !counted {
		if n, err = s.countInStore(ctx, c, plan, o); err != nil {
			return 0, err
		}
	}
	if o.records(c) {
		past, err := s.pastCount(ctx, c, o, n, func(p query.Principal) (string, []any, error) {
			plan, err := query.Compile(f, c.cls, s.sources(), p)
			if err != nil {
				return "", nil, filterErr(err)
			}
			return plan.Where, plan.Args, nil
		})
		if err != nil {
			return 0, err
		}
		r := newRead("count", c)
		r.counted, r.hasCounted, r.past = past, true, past > 0
		if err := s.logRead(ctx, o, r); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// loadCurrent reads the current rows of a composite for the documents
// found, in batches, one statement per batch.
func (ch *child) loadCurrent(ctx context.Context, db *sql.DB, c *class, docs []*docRow) (map[string][]childRow, error) {
	out := make(map[string][]childRow, len(docs))
	cols := quoteAll(columnNames(ch.def.Columns))
	const batch = 500
	for start := 0; start < len(docs); start += batch {
		end := min(start+batch, len(docs))
		args := make([]any, 0, end-start)
		for _, d := range docs[start:end] {
			args = append(args, d.id)
		}
		text := `SELECT c."doc_id", ` + prefixed("c", strings.Join(cols, ", ")) + " FROM " + quote(ch.table) + " c JOIN " + quote(c.table) +
			` d ON d."id" = c."doc_id" AND d."version" = c."version" WHERE c."doc_id" IN (` + marks(len(args)) + `) ORDER BY c."doc_id", c."idx"`
		rows, err := db.QueryContext(ctx, text, args...)
		if err != nil {
			return nil, storageErr("read "+ch.v.Name, err)
		}
		n := len(ch.def.Columns) + 1
		raw := make([]any, n)
		dest := make([]any, n)
		for i := range raw {
			dest[i] = &raw[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, storageErr("read "+ch.v.Name, err)
			}
			id := toString(raw[0])
			row := make(childRow, len(ch.leaves))
			rest := raw[1:]
			for i, l := range ch.leaves {
				row[i], rest = decode(l, rest)
			}
			out[id] = append(out[id], row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, storageErr("read "+ch.v.Name, err)
		}
	}
	return out, nil
}

// contextArgs binds the context parameters a filter's formula may read:
// the principal, and the clock, the moment chosen with At or the
// database's (ADR-0042).
func (s *Store) contextArgs(o options) []any {
	ctx := contextOf(o.principal, s.clock(o))
	return formula.NewSQLite().ContextArgs(&ctx)
}

func filterErr(err error) error {
	var qe *query.Error
	if errors.As(err, &qe) {
		variable := qe.Element
		if qe.Variable != "" {
			variable = qe.Variable
		}
		return &Error{Code: qe.Code, Variable: variable, Pos: qe.Pos, Message: qe.Message, Hint: qe.Hint}
	}
	return err
}

// SQL runs one read-only statement over the views (ADR-0038): SELECT
// only, the views and the functions, nothing physical. Positional
// parameters bind args. At most MaxSQLRows rows come back; more is the
// error too_many_rows.
func (s *Store) SQL(ctx context.Context, text string, args []any, opts ...Option) (*Rows, error) {
	// A statement its caller writes is given no rule: it reads every row
	// and every variable, so it is for whoever the host program names an
	// analyst (ADR-0049, item 12; ADR-0051).
	o, e := s.worker(ctx, workAnalyse, opts)
	if e != nil {
		return nil, e
	}
	out, err := s.runSQL(ctx, text, args)
	// Every statement is written down, with how many rows it returned or
	// the code it failed with, and never its rows; the entry is written
	// before the answer leaves (ADR-0067, items 4 and 6).
	r := &readLog{command: "sql", sql: true, statement: text, args: args, past: true}
	if err != nil {
		r.failure = CodeStorage
		if se, ok := err.(*Error); ok {
			r.failure = se.Code
		}
	} else {
		r.rows, r.hasRows = int64(len(out.Rows)), true
	}
	if lerr := s.logRead(context.WithoutCancel(ctx), o, r); lerr != nil {
		return nil, lerr
	}
	return out, err
}

// runSQL runs the statement on the connection of the SQL endpoint, which
// one statement holds at a time.
func (s *Store) runSQL(ctx context.Context, text string, args []any) (*Rows, error) {
	s.sqlMu.Lock()
	defer s.sqlMu.Unlock()
	if s.sqlConn == nil {
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return nil, storageErr("open the SQL connection", err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA query_only = 1"); err != nil {
			conn.Close()
			return nil, storageErr("set query_only", err)
		}
		if err := conn.Raw(func(driverConn any) error {
			r, ok := driverConn.(sqlite.AuthorizerRegisterer)
			if !ok {
				return fmt.Errorf("the driver has no authorizer")
			}
			return r.RegisterAuthorizer(query.Authorizer(s.viewNames()))
		}); err != nil {
			conn.Close()
			return nil, storageErr("install the authorizer", err)
		}
		s.sqlConn = conn
	}
	// A statement that was stopped, because the time its caller gave it
	// was up or because its caller left, did not fail by what it says:
	// that is the storage's word, as with every other read, and no
	// bad_value. A door that set the time then answers that it was up.
	failed := func(err error) error {
		if ctx.Err() != nil {
			return storageErr("run the statement", ctx.Err())
		}
		return sqlErr(err)
	}
	rows, err := s.sqlConn.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, failed(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, failed(err)
	}
	out := &Rows{Columns: cols}
	for rows.Next() {
		if len(out.Rows) >= MaxSQLRows {
			return nil, hintf(CodeTooManyRows, "", fmt.Sprintf("the statement returns more than %d rows", MaxSQLRows), "add a LIMIT, or filter further")
		}
		vals := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, failed(err)
		}
		out.Rows = append(out.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, failed(err)
	}
	return out, nil
}

// sources are the store's tables as the binder sees them.
func (s *Store) sources() formula.Sources {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.env().Sources
}

// viewNames lists, upper-cased, the views a SQL statement may read: the
// classes' views and the tables'.
func (s *Store) viewNames() map[string]bool {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	views := map[string]bool{}
	for _, c := range s.classes {
		for _, v := range c.viewNames() {
			views[strings.ToUpper(v)] = true
		}
	}
	for _, t := range s.tables {
		views[strings.ToUpper(t.def.Name)] = true
	}
	return views
}

// sqlErr turns the database's refusal into the store's: a statement the
// authorizer refused is not_allowed, anything else is bad_value with the
// engine's words.
func sqlErr(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "authorization denied") || strings.Contains(msg, "not authorized") || strings.Contains(msg, "prohibited") ||
		strings.Contains(msg, "query_only") || strings.Contains(msg, "readonly") {
		return hintf(CodeNotAllowed, "", "the statement is not allowed: "+msg, "the endpoint runs SELECT over the views; physical tables, writes and pragmas are refused")
	}
	return &Error{Code: CodeBadValue, Message: msg}
}
