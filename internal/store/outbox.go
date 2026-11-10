package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The outbox (ADR-0009, ADR-0044). An action of a rule that changes
// something other than the rule's own document is not done inside the
// commit: the commit evaluates its arguments and writes a row of the
// outbox in its own transaction, so the action can be neither lost nor
// invented, and a worker runs the row afterwards. The engine's own
// actions write to the same database: the worker makes the action's
// commit, marks the row done and appends the outcome's event in one
// transaction, so each happens exactly once. An action that got no answer
// is tried again later; one that was refused is dead at once and says
// why. Either way the document that caused it gets one event.

// OutboxOptions configure the outbox of an open database.
type OutboxOptions struct {
	// Manual keeps the worker from starting: Drain is then the only thing
	// that runs actions. A test, a simulation and a copy of a file opened
	// under a chosen clock run the queue at the moment they choose.
	Manual bool
	// Workers is how many actions that leave the database run at once; 0
	// is DefaultOutboxWorkers. The engine's own actions run one at a
	// time, since they take the writer.
	Workers int
	// MaxAttempts is how often an action that gets no answer is tried
	// before it is dead; 0 is DefaultMaxAttempts.
	MaxAttempts int
	// Backoff is the wait before the second attempt, doubled before every
	// further one; 0 is DefaultBackoff, about nine minutes over ten
	// attempts.
	Backoff time.Duration
}

// The defaults of OutboxOptions, and the bound on chains of actions.
const (
	DefaultOutboxWorkers = 4
	DefaultMaxAttempts   = 10
	DefaultBackoff       = time.Second
	// MaxActionDepth bounds a chain of actions (ADR-0044): an action
	// queued by a caller's command has depth 0, one queued by the commit
	// of an action of depth d has depth d+1, and one deeper than this is
	// written dead with too_deep.
	MaxActionDepth = 16
)

// The statuses of a queued action.
const (
	ActionPending = "pending"
	ActionDone    = "done"
	ActionDead    = "dead"
)

// outboxKeyPrefix starts the command key of the commit an action makes;
// a caller's key may not use it.
const outboxKeyPrefix = "outbox:"

// QueuedAction is one row of the outbox: an action a commit queued, with
// what became of it. A done row is kept: it is the record of what was
// asked, with its arguments.
type QueuedAction struct {
	// Key is the event of the commit that queued the action and the
	// action's position among those it queued: "<event_id>:<idx>".
	Key string `json:"key"`
	// Class, DocID and Version are the document whose commit queued it.
	Class   string `json:"class"`
	DocID   string `json:"doc_id"`
	Version int64  `json:"version"`
	// Rule is the action's place in the class definition: /rules/2/do/0.
	Rule   string `json:"rule"`
	Action string `json:"action"`
	// Args are the arguments as the commit evaluated them.
	Args json.RawMessage `json:"args"`
	// PrincipalID is who the action runs as: the principal of the command
	// whose commit queued it (ADR-0044, item 11).
	PrincipalID string `json:"principal_id,omitempty"`
	Depth       int    `json:"depth"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	// DueAt is when the next attempt may run, seconds since the epoch.
	DueAt int64 `json:"due_at"`
	// ErrorCode and ErrorMessage are the last failure.
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	FinishedAt   int64  `json:"finished_at,omitempty"`
	// OutcomeEvent is the action_run or action_failed event of the row.
	OutcomeEvent string `json:"outcome_event,omitempty"`
}

// OutboxFilter selects rows of the outbox: by status, by the class and by
// the document whose commit queued them; empty matches all. Limit bounds
// the listing; 0 is 1000.
type OutboxFilter struct {
	Status string
	Class  string
	DocID  string
	Limit  int
}

// queued is an action a rule asked for while a command was computed, with
// the commit of the command it belongs to and its evaluated arguments.
type queued struct {
	commit int
	act    *caction
	args   []byte
}

// ccreate is a create laid out for the commit.
type ccreate struct {
	id     *formula.Program
	values []cvalue
}

// cvalue is one value of a create: the variable of the class it fills
// and the formula over the document that fires the rule.
type cvalue struct {
	name string
	prog *formula.Program
}

// outboxRow is a row as the worker reads it.
type outboxRow struct {
	eventID   string
	idx       int64
	class     string
	docID     string
	action    string
	args      []byte
	principal Principal
	depth     int
}

func (r *outboxRow) key() string { return r.eventID + ":" + strconv.FormatInt(r.idx, 10) }

// createArgs evaluates the arguments of a create over the document that
// fires the rule (ADR-0043, item 7): the action carries the values of the
// moment that caused it, not of the moment a worker reaches it.
func (s *Store) createArgs(run *ruleRun, row *docRow, act *caction) (Values, *Error) {
	values := make(Values, len(act.create.values))
	for _, cv := range act.create.values {
		val, err := s.eval(run.c, cv.prog, row, run.evalCtx)
		if err != nil {
			err.Variable = cv.name
			return nil, err
		}
		values[cv.name] = valueOut(val)
	}
	args := Values{"class": act.a.Target, "values": values}
	if act.create.id != nil {
		val, err := s.eval(run.c, act.create.id, row, run.evalCtx)
		if err != nil {
			err.Variable = "id"
			return nil, err
		}
		if !val.IsBlank() {
			args["id"] = formula.Render(val)
		}
	}
	return args, nil
}

// writeQueued writes the outbox rows of one commit of a command, in the
// commit's transaction: commit is 0 for the command's own and n for its
// n-th eventless transition, eventID that commit's event and version the
// version it made. A row deeper than MaxActionDepth is written dead with
// too_deep and its action_failed event.
func (s *Store) writeQueued(ctx context.Context, c *class, cmd *command, commit int, eventID, docID string, version int64, day, secs int64, now time.Time) (int, error) {
	p := cmd.o.principal
	groups, _ := json.Marshal(p.Groups)
	n := 0
	for i := range s.queued {
		q := &s.queued[i]
		if q.commit != commit {
			continue
		}
		status := ActionPending
		var code, message, finished, outcome any
		if cmd.depth > MaxActionDepth {
			id := s.ids.next(now)
			status, code, finished, outcome = ActionDead, CodeTooDeep, secs, id
			message = "the action is " + strconv.Itoa(cmd.depth) + " actions deep in a chain; the bound is " + strconv.Itoa(MaxActionDepth)
			ev := eventInfo{kind: EventActionFailed, qualifier: "caused", name: q.act.a.Name, cause: eventID}
			if err := s.writeEvent(ctx, id, c.cls.Name, docID, ev, p.ID, nil, nil, "", "", day, secs); err != nil {
				return n, err
			}
		}
		admin := 0
		if p.Admin {
			admin = 1
		}
		if _, err := s.stmts.insertOutbox.ExecContext(ctx, eventID, n, c.cls.Name, docID, version, c.cls.Version, q.act.a.Path(), q.act.a.Name, string(q.args),
			nullText(p.ID), string(groups), admin, cmd.depth, status, secs, code, message, secs, finished, outcome); err != nil {
			return n, storageErr("queue the action", err)
		}
		n++
	}
	return n, nil
}

// Drain runs every action of the outbox that is due by the database's
// clock, on the caller's goroutine, and returns how many it attempted
// (ADR-0044). The actions of one document run in the order they were
// queued, and one that waits for a retry holds back the later ones of its
// document. With OutboxOptions.Manual it is the only thing that runs
// actions; otherwise the worker calls it.
func (s *Store) Drain(ctx context.Context) (int, error) {
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		batch, err := s.dueRows(ctx)
		if err != nil {
			return n, err
		}
		if len(batch) == 0 {
			return n, nil
		}
		progress := false
		for _, row := range batch {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			ran, err := s.runAction(ctx, row)
			if err != nil {
				return n, err
			}
			if ran {
				n++
				progress = true
			}
		}
		if !progress {
			return n, nil
		}
	}
}

// dueRows reads the next actions that may run: pending, due by the
// database's clock, and not behind an earlier pending action of the same
// document. There is at most one per document, so a batch may run in any
// order.
func (s *Store) dueRows(ctx context.Context) ([]*outboxRow, error) {
	rows, err := s.stmts.dueOutbox.QueryContext(ctx, s.now().Unix())
	if err != nil {
		return nil, storageErr("read the outbox", err)
	}
	defer rows.Close()
	var out []*outboxRow
	for rows.Next() {
		r := &outboxRow{}
		var id, groups sql.NullString
		var admin int64
		var args string
		if err := rows.Scan(&r.eventID, &r.idx, &r.class, &r.docID, &r.action, &args, &id, &groups, &admin, &r.depth); err != nil {
			return nil, storageErr("read the outbox", err)
		}
		r.args = []byte(args)
		r.principal = Principal{ID: id.String, Admin: admin != 0}
		if groups.Valid {
			_ = json.Unmarshal([]byte(groups.String), &r.principal.Groups)
		}
		out = append(out, r)
	}
	return out, storageErr("read the outbox", rows.Err())
}

// runAction runs one row under the writer. For an action the engine runs
// itself the action's commit, the mark and the event of the outcome are
// one transaction (ADR-0044, item 5). It reports whether the row was
// attempted: a row another runner has taken in the meantime is not.
func (s *Store) runAction(ctx context.Context, row *outboxRow) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, storageErr("begin", err)
	}
	var status string
	var attempts int
	var day int64
	err := w.QueryRowContext(ctx, `SELECT o.status, o.attempts, e.business_date FROM outbox o JOIN events e ON e.event_id = o.event_id WHERE o.event_id = ? AND o.idx = ?`, row.eventID, row.idx).Scan(&status, &attempts, &day)
	if err != nil || status != ActionPending {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		if err != nil && err != sql.ErrNoRows {
			return false, storageErr("read the action", err)
		}
		return false, nil
	}
	var queuedMore bool
	failure := s.at("perform", row)
	if failure == nil {
		queuedMore, failure = s.perform(ctx, row, day, now)
	}
	if failure == nil {
		failure = s.at("performed", row)
	}
	if failure == nil {
		if err := s.finish(ctx, row, ActionDone, attempts+1, nil, day, now); err != nil {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			return false, err
		}
		if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			return false, storageErr("commit", err)
		}
		_ = s.at("committed", row)
		if queuedMore {
			s.wakeWorker()
		}
		return true, nil
	}
	// The action's commit is undone; what became of the action is written
	// in a transaction of its own.
	if _, err := w.ExecContext(ctx, "ROLLBACK"); err != nil {
		return false, storageErr("rollback", err)
	}
	if ctx.Err() != nil {
		// The caller gave up; the row is as it was.
		return false, ctx.Err()
	}
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, storageErr("begin", err)
	}
	attempts++
	if noAnswer(failure) && attempts < s.outbox.MaxAttempts {
		// A question that got no answer is asked again, later.
		wait := s.outbox.Backoff << (attempts - 1)
		due := now.Add(wait).Unix()
		if due <= now.Unix() {
			due = now.Unix() + 1
		}
		_, err = w.ExecContext(ctx, `UPDATE outbox SET attempts = ?, due_at = ?, error_code = ?, error_message = ? WHERE event_id = ? AND idx = ?`,
			attempts, due, failure.Code, failure.Message, row.eventID, row.idx)
		if err != nil {
			err = storageErr("record the attempt", err)
		}
	} else {
		err = s.finish(ctx, row, ActionDead, attempts, failure, day, now)
	}
	if err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, err
	}
	if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, storageErr("commit", err)
	}
	return true, nil
}

// at is the tests' window into the steps of an action: before it is
// performed, after it is and before its mark, and after the commit of
// both. A test makes a step fail, or stops the process there
// (ADR-0044, item 10). nil outside tests.
func (s *Store) at(step string, row *outboxRow) *Error {
	if s.testStep == nil {
		return nil
	}
	return s.testStep(step, row)
}

// noAnswer says whether a failure is a question that got no answer, to be
// asked again, and not a refusal, which is an answer (ADR-0044, item 4):
// the store was busy or failed.
func noAnswer(e *Error) bool {
	return e.Code == CodeStorage || e.Code == CodeClosed
}

// finish marks a row done or dead and appends the event of the outcome,
// on the document whose commit queued the action: its name is the
// action's and its cause the event of that commit.
func (s *Store) finish(ctx context.Context, row *outboxRow, status string, attempts int, failure *Error, day int64, now time.Time) error {
	secs := now.Unix()
	kind := EventActionRun
	var code, message any
	if status == ActionDead {
		kind, code, message = EventActionFailed, failure.Code, failure.Message
	}
	id := s.ids.next(now)
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = ?, finished_at = ?, error_code = ?, error_message = ?, outcome_event = ? WHERE event_id = ? AND idx = ?`,
		status, attempts, secs, code, message, id, row.eventID, row.idx); err != nil {
		return storageErr("mark the action", err)
	}
	ev := eventInfo{kind: kind, qualifier: "caused", name: row.action, cause: row.eventID}
	return s.writeEvent(ctx, id, row.class, row.docID, ev, row.principal.ID, nil, nil, "", "", day, secs)
}

// perform does what an action stands for, inside the worker's
// transaction, as the principal of the command whose commit queued it
// (ADR-0044, item 11): the field rights and the transition rules of the
// target apply to that person, and what they may not do is a refusal. It
// reports whether the action's own commit queued further actions.
func (s *Store) perform(ctx context.Context, row *outboxRow, day int64, now time.Time) (bool, *Error) {
	switch row.action {
	case model.ActionCreate:
		var args struct {
			Class  string `json:"class"`
			ID     string `json:"id"`
			Values Values `json:"values"`
		}
		if err := json.Unmarshal(row.args, &args); err != nil {
			return false, errf(CodeBadValue, "", "the arguments of the action do not read: %v", err)
		}
		c, e := s.class(args.Class)
		if e != nil {
			return false, e
		}
		cmd := command{op: opCreate, c: c, id: args.ID, values: args.Values, o: options{principal: row.principal, key: outboxKeyPrefix + row.key()},
			cause: row.eventID, depth: row.depth + 1, internal: true, day: day, hasDay: true}
		res, err := s.run(ctx, cmd, now)
		if err != nil {
			return false, asError(err)
		}
		return res.queued > 0, nil
	}
	return false, hintf(CodeUnknownAction, "", "the engine does not run the action "+row.action, "the actions it runs are in the class's rules as the definition accepted them; this row was queued by another build")
}

// asError is the store's error of a failure, a storage one when the
// failure is not the store's own.
func asError(err error) *Error {
	if se, ok := err.(*Error); ok {
		return se
	}
	return &Error{Code: CodeStorage, Message: err.Error()}
}

// Outbox lists rows of the outbox in the order they were queued
// (ADR-0044, item 8): what is waiting, what ran and what is dead, each
// with its key, its arguments, its attempts and its last error.
func (s *Store) Outbox(ctx context.Context, f OutboxFilter) ([]QueuedAction, error) {
	var where []string
	var args []any
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Class != "" {
		c, e := s.class(f.Class)
		if e != nil {
			return nil, e
		}
		where = append(where, "class = ?")
		args = append(args, c.cls.Name)
	}
	if f.DocID != "" {
		where = append(where, "doc_id = ?")
		args = append(args, f.DocID)
	}
	text := `SELECT event_id, idx, class, doc_id, version, rule, action, args, principal_id, depth, status, attempts, due_at, error_code, error_message, created_at, finished_at, outcome_event FROM outbox`
	if len(where) > 0 {
		text += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 1000
	}
	text += " ORDER BY event_id, idx LIMIT " + strconv.Itoa(limit)
	rows, err := s.db.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, storageErr("read the outbox", err)
	}
	defer rows.Close()
	out := []QueuedAction{}
	for rows.Next() {
		var q QueuedAction
		var eventID, argsText string
		var idx int64
		var principal, code, message, outcome sql.NullString
		var finished sql.NullInt64
		if err := rows.Scan(&eventID, &idx, &q.Class, &q.DocID, &q.Version, &q.Rule, &q.Action, &argsText, &principal, &q.Depth, &q.Status, &q.Attempts, &q.DueAt,
			&code, &message, &q.CreatedAt, &finished, &outcome); err != nil {
			return nil, storageErr("read the outbox", err)
		}
		q.Key = eventID + ":" + strconv.FormatInt(idx, 10)
		q.Args = json.RawMessage(argsText)
		q.PrincipalID, q.ErrorCode, q.ErrorMessage, q.OutcomeEvent, q.FinishedAt = principal.String, code.String, message.String, outcome.String, finished.Int64
		out = append(out, q)
	}
	return out, storageErr("read the outbox", rows.Err())
}

// RetryAction makes a dead action pending again with its attempts at
// zero (ADR-0044, item 8); the outcome it then reaches is a further
// event. A dead row is never retried by itself.
func (s *Store) RetryAction(ctx context.Context, key string) error {
	i := strings.LastIndexByte(key, ':')
	idx, perr := strconv.ParseInt(key[i+1:], 10, 64)
	if i <= 0 || perr != nil {
		return hintf(CodeUnknownAction, "", "no action has the key "+strconv.Quote(key), "a key is <event_id>:<idx>, as Outbox lists it")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	var status string
	err := s.writer.QueryRowContext(ctx, `SELECT status FROM outbox WHERE event_id = ? AND idx = ?`, key[:i], idx).Scan(&status)
	switch {
	case err == sql.ErrNoRows:
		return hintf(CodeUnknownAction, "", "no action has the key "+strconv.Quote(key), "Outbox lists the actions with their keys")
	case err != nil:
		return storageErr("read the action", err)
	case status != ActionDead:
		return hintf(CodeNotAllowed, "", "the action is "+status+"; only a dead action is retried", "a pending action runs by itself, and a done one has run")
	}
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = 0, due_at = ?, finished_at = NULL WHERE event_id = ? AND idx = ? AND status = ?`,
		ActionPending, s.now().Unix(), key[:i], idx, ActionDead); err != nil {
		return storageErr("retry the action", err)
	}
	s.wakeWorker()
	return nil
}

// wakeWorker tells the worker there may be something to run; it never
// blocks.
func (s *Store) wakeWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// work is the worker (ADR-0044, item 2): it drains the outbox when a
// commit queued something and when the earliest pending action falls
// due, until the store closes. A failure of the store is tried again
// after a pause.
func (s *Store) work(ctx context.Context) {
	defer close(s.workDone)
	for {
		// The wait is counted on the database's clock, which is what makes
		// a row due, and bounded so that a clock that stands still or
		// jumps is looked at again.
		wait := time.Minute
		if _, err := s.Drain(ctx); err != nil {
			wait = time.Second
		} else if next, ok := s.nextDue(ctx); ok {
			wait = min(max(time.Duration(next-s.now().Unix())*time.Second, 10*time.Millisecond), time.Minute)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// nextDue is when the earliest pending action falls due, in seconds.
func (s *Store) nextDue(ctx context.Context) (int64, bool) {
	var due sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT min(due_at) FROM outbox WHERE status = ?`, ActionPending).Scan(&due); err != nil || !due.Valid {
		return 0, false
	}
	return due.Int64, true
}
