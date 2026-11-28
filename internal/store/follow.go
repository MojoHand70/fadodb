package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// A kept value that follows its table (ADR-0091). A triggered variable
// that lists table:<name> is worked out again in every document of its
// class that is not finished when the rows of that table change, and
// kept where it comes out different.
//
// The change of the table writes one row of the outbox for each class
// that follows it, in its own transaction, so the work can be neither
// lost nor invented. The worker runs the row afterwards: one statement of
// the store finds the documents whose kept value is no longer what the
// formula gives, and they are brought a thousand in a transaction, each
// with a version and an event of its own, so that other people's commits
// go in between. The row is marked done in the transaction of the last
// thousand. A process that stops in the middle leaves the row pending,
// and the next look finds what is left: the documents already brought
// hold what the formula gives, and are found no more.
//
// Nobody's right is asked for it (item 3). The right is asked where a
// person decides: the write rule of the table, of whoever changes it.
// What follows in the documents is what the class says, and is the
// engine's write, as a rule's set is. A class stops a value in a state
// with a write right of FALSE there (item 4).
//
// A kept value that follows the clock (ADR-0093) is the same machine
// with a clock in front: a variable that lists schedule:<when> has a
// row of the outbox waiting for its next moment, by the clock of the
// database's time zone, and the row that ran queues the one after it.
// Nobody changed anything, so its commits have no actor: the engine
// itself brings the value, and the event says the schedule.
//
// What a row follows is its trigger, as the class writes it:
// table:Rates, or schedule:daily at 03:00.

// ActionFollow is the action of a row the change of a table queues for a
// class whose variables follow that table (ADR-0091, item 6). Like a
// wake-up it stands for no document's action: no rule names it, and no
// definition can, since a name without a dot is the engine's.
const ActionFollow = "follow"

// followMany is how many documents one transaction of a follow brings.
// It is the size a batch of moves is told to have (ADR-0048): about
// forty milliseconds of the writer, between which other commits go.
const followMany = 1000

// followSize is followMany, or what a test set for its store, so that a
// test of what happens between two transactions needs a hundred
// documents and not thousands.
func (s *Store) followSize() int {
	if s.followEach > 0 {
		return s.followEach
	}
	return followMany
}

// followPause is how long a follow leaves the writer alone between two
// of its transactions. A commit that waited for the transaction takes
// the writer in that moment; without it the follow, which asks again at
// once, is often there first, and the commit waits for a second
// transaction.
const followPause = time.Millisecond

// followArgs are the arguments of a follow row: the table that changed,
// or the schedule whose moment it waits for.
type followArgs struct {
	Table    string `json:"table,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

// trigger is what the row follows, as a class writes it.
func (a followArgs) trigger() string {
	switch {
	case a.Table != "":
		return "table:" + a.Table
	case a.Schedule != "":
		return "schedule:" + a.Schedule
	}
	return ""
}

// follower is one variable of a class that follows a table or a
// schedule, with the states it follows it in: every state that is not
// final and in which the class does not close the variable to everybody.
type follower struct {
	cv     *cvar
	states map[string]bool
}

// layFollows notes, when a class is laid out, which of its variables
// follow which table or schedule and in which states, under the trigger
// in upper case, and the schedules of the class, as it writes them.
func (c *class) layFollows() {
	for _, cv := range c.triggered {
		for _, tr := range cv.v.Triggers {
			if (tr.Kind != "table" && tr.Kind != "schedule") || cv.leaf < 0 || c.cls.States == nil {
				continue
			}
			f := follower{cv: cv, states: map[string]bool{}}
			for _, st := range c.cls.States.List {
				if c.cls.States.IsFinal(st) || c.cls.WriteRule(st, cv.v.Name).Nobody() {
					continue
				}
				f.states[st] = true
			}
			// The states are the variable's, whichever table or schedule
			// it follows: a commit that brings a document into one of
			// them brings the value (triggered.go).
			cv.followIn = f.states
			if c.follows == nil {
				c.follows = map[string][]follower{}
			}
			key := strings.ToUpper(tr.Kind + ":" + tr.Arg)
			if tr.Kind == "schedule" && len(c.follows[key]) == 0 {
				c.schedules = append(c.schedules, tr.Arg)
			}
			c.follows[key] = append(c.follows[key], f)
		}
	}
}

// following are the variables a trigger, table:<name> or
// schedule:<when>, brings again in a document in the state, in the order
// of the class.
func (c *class) following(trigger, state string) []*cvar {
	var out []*cvar
	for _, f := range c.follows[strings.ToUpper(trigger)] {
		if f.states[state] {
			out = append(out, f.cv)
		}
	}
	return out
}

// queueFollows writes, in the transaction that changed rows of the
// table, one row of the outbox for each class that has a variable
// following it (ADR-0091, item 6), in the order of the classes' names.
// The row runs as p, who changed the table: the events of the documents
// it brings name them. depth is the depth of the command that changed
// the table, 0 for a caller's own; a follow deeper than MaxActionDepth
// is written dead with too_deep, as an action is, so that a rule which
// writes the table its own class follows ends. It returns how many rows
// it wrote; the caller wakes the worker once its transaction is
// committed.
func (s *Store) queueFollows(ctx context.Context, t *table, p Principal, reason string, depth int, now time.Time) (int, error) {
	s.cmu.RLock()
	classes := s.followersOf(t.def.Name)
	s.cmu.RUnlock()
	return s.queueFollowsOf(ctx, t, classes, p, reason, depth, now)
}

// followersOf lists the classes that have a variable following the
// table. The caller holds the lock of the definitions.
func (s *Store) followersOf(table string) []*class {
	key := strings.ToUpper("table:" + table)
	var classes []*class
	for _, c := range s.classes {
		if len(c.follows[key]) > 0 {
			classes = append(classes, c)
		}
	}
	return classes
}

// queueFollowsOf is queueFollows for the classes given, for a caller
// that holds the lock of the definitions itself.
func (s *Store) queueFollowsOf(ctx context.Context, t *table, classes []*class, p Principal, reason string, depth int, now time.Time) (int, error) {
	if len(classes) == 0 {
		return 0, nil
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].cls.Name < classes[j].cls.Name })
	args, _ := json.Marshal(followArgs{Table: t.def.Name})
	groups := principalText(p, reason)
	secs := now.Unix()
	id := s.ids.next(now)
	for i, c := range classes {
		status := ActionPending
		var code, message, finished any
		if depth > MaxActionDepth {
			status, code, finished = ActionDead, CodeTooDeep, secs
			message = "the change of " + t.def.Name + " is " + strconv.Itoa(depth) + " actions deep in a chain; the bound is " + strconv.Itoa(MaxActionDepth)
		}
		if _, err := s.stmts.insertOutbox.ExecContext(ctx, id, i, c.cls.Name, "", 0, c.cls.Version, "table:"+t.def.Name, ActionFollow, string(args),
			nullText(p.ID), string(groups), depth, status, secs, code, message, secs, finished, nil); err != nil {
			return i, storageErr("queue the follow of "+t.def.Name, err)
		}
	}
	return len(classes), nil
}

// followSQL is the statement that finds the documents of the class a
// trigger brings again, a change of a table or the moment of a
// schedule, after a place in the class's table
// and at most followMany of them, in the order of that table, with the
// context parameters it reads. A document is found when it is in a state
// in which one of the following variables follows, and that variable's
// kept value is not what its formula gives now. The comparison is made
// where the formula is one statement over the class and gives the very
// type the variable keeps; a variable for which it is not takes every
// document in its states, and the commit of each decides, by working
// the value out, whether anything is written.
func (s *Store) followSQL(c *class, trigger string) (string, []string) {
	const doc = "d"
	sources := s.sources()
	tableOf := func(source string) string {
		if t, ok := s.tables[strings.ToUpper(source)]; ok {
			return quote(t.name)
		}
		return quote(source)
	}
	d := formula.NewSQLite()
	var terms, params []string
	seen := map[string]bool{}
	for _, f := range c.follows[strings.ToUpper(trigger)] {
		if len(f.states) == 0 {
			continue
		}
		states := make([]string, 0, len(f.states))
		for st := range f.states {
			states = append(states, "'"+strings.ReplaceAll(st, "'", "''")+"'")
		}
		sort.Strings(states)
		term := doc + `."state" IN (` + strings.Join(states, ", ") + ")"
		l := c.leaves[f.cv.leaf]
		if stmt, err := query.Kept(f.cv.v.Def.Formula, c.cls, sources, doc, tableOf); err == nil && stmt.Kind == l.typ.Kind && stmt.Scale == l.typ.Scale {
			differs := "(" + stmt.Value + ") IS NOT " + d.Load(doc+"."+quote(l.column), l.typ.Kind)
			if l.typ.Kind == formula.KindMoney {
				differs += " OR (" + stmt.CurrencyOrNull() + ") IS NOT " + doc + "." + quote(l.column+formula.CurrencySuffix)
			}
			if stmt.Error != "" {
				differs = "(" + stmt.Error + ") IS NOT NULL OR " + differs
			}
			term += " AND (" + differs + ")"
			for _, name := range stmt.Params {
				if !seen[name] {
					seen[name] = true
					params = append(params, name)
				}
			}
		}
		terms = append(terms, "("+term+")")
	}
	if len(terms) == 0 {
		return "", nil
	}
	// The table is read in its own order, from the place given, and the
	// statement stops at the last document it may take: an index on the
	// state would have every document of that state read and sorted for
	// each thousand, so none is used.
	return "SELECT " + doc + ".rowid, " + doc + `."id" FROM ` + quote(c.table) + " AS " + doc + " NOT INDEXED" +
		" WHERE " + doc + ".rowid > :fado_after AND (" + strings.Join(terms, " OR ") + ")" +
		" ORDER BY " + doc + ".rowid LIMIT " + strconv.Itoa(s.followSize()), params
}

// followRun is what a follow did so far, over the transactions it took.
type followRun struct {
	// after is the place in the class's table the next statement finds
	// from.
	after int64
	// brought and refused count the documents that got a version and the
	// ones that could not take the value; first is the first refusal.
	brought, refused int
	first            *Error
	// careful says a document of the run was refused: from then on each
	// document has a savepoint of its own, to be taken back to. Until
	// then none has, and a refusal takes the transaction back whole
	// (bringMany).
	careful bool
}

// runFollow runs a follow row (ADR-0091, item 6): transaction after
// transaction of at most followMany documents, with the writer let go
// between them, and the mark of the row in the last. It reports whether
// the row was attempted: a row another runner has taken, or that is no
// longer pending, is not. A failure of the store leaves the row pending
// for another attempt, as an action that got no answer is; the documents
// brought before it stay brought.
//
// The worker, own, runs one transaction of it and returns, with the row
// still pending when there is more: it looks at what else is due before
// the next thousand, so that the actions and the timers of other
// documents do not wait for a large class to be brought. A drain by
// hand, which runs one row after another, runs the row to its end.
func (s *Store) runFollow(ctx context.Context, row *outboxRow, own bool) (bool, error) {
	var args followArgs
	if err := json.Unmarshal(row.args, &args); err != nil || args.trigger() == "" {
		return s.followEnd(ctx, row, hintf(CodeBadValue, "", "the arguments of the follow do not read", "the row was queued by another build"))
	}
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		more, attempted, err := s.followBatch(ctx, row, args, own)
		if err != nil || !attempted {
			return false, err
		}
		if !more {
			return true, nil
		}
		// Other people's commits go in between (item 6).
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(followPause):
		}
		if own {
			return true, nil
		}
	}
}

// followBatch is one transaction of a follow: it finds the next
// documents, brings each, and marks the row done when they were the
// last. more says another transaction follows. Where the follow stands
// between two transactions is kept in memory, under the row's key: a
// process that stops loses it, and the row, still pending, is then run
// from the start of the class, which finds what is left.
func (s *Store) followBatch(ctx context.Context, row *outboxRow, args followArgs, own bool) (more, attempted bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, false, errf(CodeClosed, "", "the store is closed")
	}
	key := row.key()
	run := s.followRuns[key]
	if run == nil {
		run = &followRun{}
		if s.followRuns == nil {
			s.followRuns = map[string]*followRun{}
		}
		s.followRuns[key] = run
	}
	// The place is kept only while this transaction is followed by
	// another of the same run.
	keep := false
	defer func() {
		if !keep {
			delete(s.followRuns, key)
		}
	}()
	now := s.now()
	w := s.writer
	// Where the run stood before this transaction: one taken back to be
	// brought again begins there.
	start := *run
	var attempts int
	var failure *Error
	found, queued := 0, 0
	for again := false; ; {
		if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
			return false, false, storageErr("begin", err)
		}
		var status string
		if err := w.QueryRowContext(ctx, `SELECT status, attempts FROM outbox WHERE event_id = ? AND idx = ?`, row.eventID, row.idx).Scan(&status, &attempts); err != nil || status != ActionPending {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			if err != nil && err != sql.ErrNoRows {
				return false, false, storageErr("read the follow", err)
			}
			return false, false, nil
		}
		if !again {
			failure = s.at("perform", row)
		}
		if failure == nil {
			found, queued, again, failure = s.bringMany(ctx, row, args.trigger(), run, now)
		}
		if !again {
			break
		}
		// A document was refused with no savepoint to take it back to:
		// the transaction goes back whole, with the moment of the
		// earliest wake-up its commits queued, and its documents are
		// brought again, each with a savepoint, as is every later one of
		// the run.
		if _, err := w.ExecContext(ctx, "ROLLBACK"); err != nil {
			return false, false, storageErr("rollback", err)
		}
		s.wakeAt = 0
		*run = start
		run.careful = true
	}
	if failure == nil {
		failure = s.at("performed", row)
	}
	more = found == s.followSize()
	if failure == nil && !more {
		if err := s.followed(ctx, row, attempts+1, run, now); err != nil {
			failure = asError(err)
		}
	}
	if failure == nil && !more && args.Schedule != "" {
		// The row of the next moment, in the transaction that ends this
		// one: a schedule is never without a row while its class has it.
		if err := s.queueSchedule(ctx, row.class, args.Schedule, now); err != nil {
			failure = asError(err)
		}
	}
	if failure == nil {
		failure = s.at("marked", row)
	}
	if failure == nil {
		if err := s.endTransaction(ctx); err != nil {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			return false, false, storageErr("commit", err)
		}
		_ = s.at("committed", row)
		s.wakeQueued(queued > 0, own)
		keep = more
		return more, true, nil
	}
	// The transaction is undone, and with it the documents it brought;
	// what became of the row is written in a transaction of its own, and
	// the next attempt begins at the start of the class.
	s.wakeAt = 0
	if _, err := w.ExecContext(ctx, "ROLLBACK"); err != nil {
		return false, false, storageErr("rollback", err)
	}
	if ctx.Err() != nil {
		return false, false, ctx.Err()
	}
	if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, false, storageErr("begin", err)
	}
	if err := s.followFailed(ctx, row, attempts, failure, now); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, false, err
	}
	if err := s.endTransaction(ctx); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, false, storageErr("commit", err)
	}
	s.wakeQueued(false, own)
	return false, true, nil
}

// bringMany finds the next documents a follow brings and brings each, inside
// the worker's transaction: a refresh of the engine's own (item 3), as
// the principal who changed the table, for the record and for what the
// commit queues in its turn. A document that cannot take the value,
// because a value computed from it fails or a rule refuses the commit,
// is taken back and keeps what it holds; the row says how many did. It
// returns how many documents the statement found and how many rows of
// the outbox their commits queued. Only a failure of the store is a
// failure of the follow.
//
// A document is taken back to the savepoint set before it, once the run
// is careful. Until then no document has one, since a savepoint costs
// SQLite a copy of each page the document's commit writes; again then
// says a document was refused, and the caller takes the transaction
// back and brings it again, careful.
func (s *Store) bringMany(ctx context.Context, row *outboxRow, trigger string, run *followRun, now time.Time) (found, queued int, again bool, failure *Error) {
	c, e := s.class(row.class)
	if e != nil || len(c.follows[strings.ToUpper(trigger)]) == 0 {
		// The class is gone, or no longer follows the table or the
		// schedule: nothing is left to bring.
		return 0, 0, false, nil
	}
	text, params := s.followSQL(c, trigger)
	if text == "" {
		return 0, 0, false, nil
	}
	evalCtx := contextOf(row.principal, now.Unix())
	named := []any{sql.Named("fado_after", run.after)}
	named = formula.NewSQLite().ContextArgsFor(&evalCtx, params, named)
	rows, err := s.writer.QueryContext(ctx, text, named...)
	if err != nil {
		return 0, 0, false, asError(storageErr("find the documents that follow "+trigger, err))
	}
	type place struct {
		rowid int64
		id    string
	}
	var places []place
	for rows.Next() {
		var p place
		if err := rows.Scan(&p.rowid, &p.id); err != nil {
			rows.Close()
			return 0, 0, false, asError(storageErr("find the documents that follow "+trigger, err))
		}
		places = append(places, p)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, false, asError(storageErr("find the documents that follow "+trigger, err))
	}
	cmd := command{op: opRefresh, c: c, o: options{principal: row.principal, reason: row.reason}, internal: true, engine: true,
		depth: row.depth + 1, follow: trigger, day: businessDay(options{}, now), hasDay: true}
	marked := false
	for _, p := range places {
		if run.careful && !marked {
			if err := s.markMany(ctx); err != nil {
				return 0, 0, false, asError(storageErr("mark the follow", err))
			}
			marked = true
		}
		cmd.id = p.id
		res, err := s.run(ctx, cmd, now)
		run.after = p.rowid
		if err != nil {
			refusal, ok := err.(*Error)
			if !ok || refusal.Code == CodeStorage || refusal.Code == CodeClosed || ctx.Err() != nil {
				return 0, 0, false, asError(err)
			}
			if !run.careful {
				// What its commit wrote has no savepoint to go back to.
				return 0, 0, true, nil
			}
			if err := s.backToMany(ctx); err != nil {
				return 0, 0, false, asError(storageErr("take a refused document back", err))
			}
			run.refused++
			if run.first == nil {
				first := *refusal
				first.Message = c.cls.Name + " " + p.id + ": " + refusal.Message
				run.first = &first
			}
			continue
		}
		if res.noop {
			// Its value came out the same: nothing was written.
			continue
		}
		if marked {
			if err := s.releaseMany(ctx); err != nil {
				return 0, 0, false, asError(storageErr("release the mark", err))
			}
			marked = false
		}
		run.brought++
		queued += res.queued
	}
	if marked {
		if err := s.releaseMany(ctx); err != nil {
			return 0, 0, false, asError(storageErr("release the mark", err))
		}
	}
	return len(places), queued, false, nil
}

// followed marks a follow done, in the transaction of its last
// documents. Like a wake-up it appends no event of its own: the events
// of the documents it brought are the record. Where documents could not
// take the value, the row says how many and why the first could not,
// where an action says why its result was not written back.
func (s *Store) followed(ctx context.Context, row *outboxRow, attempts int, run *followRun, now time.Time) error {
	var code, message any
	if run.refused > 0 {
		code = run.first.Code
		message = fmt.Sprintf("%d of %d documents kept their value; the first: %s", run.refused, run.refused+run.brought, run.first.Message)
	}
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = ?, finished_at = ?, error_code = NULL, error_message = NULL, outcome_event = NULL, into_code = ?, into_message = ? WHERE event_id = ? AND idx = ?`,
		ActionDone, attempts, now.Unix(), code, message, row.eventID, row.idx); err != nil {
		return storageErr("mark the follow", err)
	}
	return nil
}

// followFailed records an attempt that did not finish a follow, in the
// transaction the caller began; attempts counts the ones before it. A
// failure of the store is tried again later, until MaxAttempts; any
// other, and the last attempt, leave the row dead with its code. A dead
// follow has no event, since it has no document; an administrator reads
// it in the outbox and sends it again with RetryAction.
func (s *Store) followFailed(ctx context.Context, row *outboxRow, attempts int, failure *Error, now time.Time) error {
	attempts++
	if !noAnswer(failure) || attempts >= s.outbox.MaxAttempts {
		if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = ?, finished_at = ?, error_code = ?, error_message = ? WHERE event_id = ? AND idx = ?`,
			ActionDead, attempts, now.Unix(), failure.Code, failure.Message, row.eventID, row.idx); err != nil {
			return storageErr("mark the follow", err)
		}
		return nil
	}
	due := now.Add(s.outbox.Backoff << (attempts - 1)).Unix()
	if due <= now.Unix() {
		due = now.Unix() + 1
	}
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET attempts = ?, due_at = ?, error_code = ?, error_message = ? WHERE event_id = ? AND idx = ?`,
		attempts, due, failure.Code, failure.Message, row.eventID, row.idx); err != nil {
		return storageErr("record the attempt", err)
	}
	return nil
}

// followEnd leaves a follow row that cannot run dead, in a transaction
// of its own.
func (s *Store) followEnd(ctx context.Context, row *outboxRow, failure *Error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, storageErr("begin", err)
	}
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = attempts + 1, finished_at = ?, error_code = ?, error_message = ? WHERE event_id = ? AND idx = ? AND status = ?`,
		ActionDead, now.Unix(), failure.Code, failure.Message, row.eventID, row.idx, ActionPending); err != nil {
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		return false, storageErr("mark the follow", err)
	}
	if err := s.endTransaction(ctx); err != nil {
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		return false, storageErr("commit", err)
	}
	return true, nil
}

// followVar works out, in the commit of a follow, a variable that
// follows its table (ADR-0091, item 2), and reports whether the document
// changed by it. A value that comes out the same is left with the moment
// it had: the commit of a document none of whose values moved writes
// nothing. One that cannot be worked out, or is not a value the variable
// accepts, leaves the value and records the code beside it, as any
// trigger does (ADR-0019), which is a change when the code is new.
func (s *Store) followVar(c *class, cv *cvar, row *docRow, evalCtx *formula.Context, secs int64, preview bool, errs *[]*Error) (bool, *Error) {
	val, err := s.eval(c, cv.v.Formula, row, evalCtx)
	if err == nil {
		val, err = fit(cv.v, val)
	}
	if err != nil {
		code := formula.Text(err.Code)
		if formula.Equal(row.vals[cv.errLeaf], code) {
			return false, nil
		}
		row.vals[cv.errLeaf] = code
		return true, nil
	}
	if formula.Equal(row.vals[cv.leaf], val) && row.vals[cv.errLeaf].IsBlank() {
		return false, nil
	}
	return true, s.kept(c, cv, row, val, evalCtx, secs, preview, errs)
}
