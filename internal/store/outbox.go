package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
// transaction, so each happens exactly once. An action that leaves the
// database (ADR-0045) is called outside any transaction and marked
// afterwards, so it happens at least once, each time with the same key.
// An action that got no answer is tried again later; one that was refused
// is dead at once and says why. Either way the document that caused it
// gets one event.

// OutboxOptions configure the outbox of an open database.
type OutboxOptions struct {
	// Manual keeps the worker from starting: Drain is then the only thing
	// that runs actions. A test, a simulation and a copy of a file opened
	// under a chosen clock run the queue at the moment they choose.
	Manual bool
	// Workers is how many actions that leave the database the worker runs
	// at once; 0 is DefaultOutboxWorkers. The engine's own actions run one
	// at a time, since they take the writer, and Drain runs every action
	// one at a time on the caller's goroutine.
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

// ActionTimer is the action of a wake-up (ADR-0047, item 10): the row a
// commit queues for the moment of a timed transition, with which the
// engine touches the document when the time comes. No rule names it, and
// no definition can: a name without a dot is the engine's.
const ActionTimer = "timer"

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
	// PrincipalKind is what that principal was, person, agent or service,
	// and PrincipalFor the id it acted for (ADR-0050).
	PrincipalKind string `json:"principal_kind,omitempty"`
	PrincipalFor  string `json:"principal_for,omitempty"`
	Depth         int    `json:"depth"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	// DueAt is when the next attempt may run, seconds since the epoch.
	DueAt int64 `json:"due_at"`
	// ErrorCode and ErrorMessage are the last failure.
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	FinishedAt   int64  `json:"finished_at,omitempty"`
	// OutcomeEvent is the action_run or action_failed event of the row.
	OutcomeEvent string `json:"outcome_event,omitempty"`
	// IntoCode and IntoMessage say why the result of an action that has an
	// into was not written back to the document (ADR-0045, item 8): the
	// document was frozen or gone, or refused a value. Empty when the
	// result was stored, and for an action without an into. The action's
	// own outcome stands either way.
	IntoCode    string `json:"into_code,omitempty"`
	IntoMessage string `json:"into_message,omitempty"`
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

// cqueued is an action of the outbox the engine runs itself, laid out
// for the commit: the formulas of its arguments over the document that
// fires the rule.
type cqueued struct {
	// id gives the id of the document: of the one a create makes, nil when
	// the engine gives it, and of the one a transition or a refresh acts
	// on.
	id *formula.Program
	// values are the values of a create or a transition, the row of a
	// table.insert or the columns a table.update changes; key the key of
	// the row a table.update or a table.delete addresses.
	values, key []cvalue
	// each is the composite a create makes one document for each element
	// of, nil for one document (ADR-0044, item 12). Its formulas then
	// read the element too.
	each *child
	// args are the arguments of an action that names a definition
	// (ADR-0045), in the order written.
	args []carg
}

// carg is one argument of a defined action: the parameter it gives and
// the formula of its value, or for a json parameter the template that
// makes it.
type carg struct {
	name string
	prog *formula.Program
	tmpl *model.Template
}

// cvalue is one argument of a queued action: the variable or the column
// it fills and the formula that gives its value.
type cvalue struct {
	name string
	prog *formula.Program
}

// list is a composite a create with each reads, as the command leaves
// it.
type list struct {
	ch   *child
	rows []childRow
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

// layoutQueued binds the slots of the formulas of an outbox action. The
// formulas of a create with each read the element of the list beside the
// document.
func (c *class) layoutQueued(a *model.Action) (*cqueued, error) {
	out := &cqueued{}
	if a.Each != nil {
		out.each = c.byName[strings.ToUpper(a.Each.Name)].child
	}
	bind := func(prog *formula.Program, part, name string) error {
		owner := a.Path() + "/" + part
		if name != "" {
			owner += "/" + name
		}
		if out.each != nil {
			return c.bindElemSlots(prog, owner, a.As, out.each)
		}
		return c.bindSlots(prog, owner)
	}
	for _, arg := range a.Args {
		if arg.Formula != nil {
			if err := bind(arg.Formula, "args", arg.Name); err != nil {
				return nil, err
			}
		} else if err := c.bindTemplate(arg.Template, a.Path()+"/args/"+arg.Name); err != nil {
			return nil, err
		}
		out.args = append(out.args, carg{name: arg.Name, prog: arg.Formula, tmpl: arg.Template})
	}
	part := "values"
	switch a.Name {
	case model.ActionTableInsert:
		part = "row"
	case model.ActionTableUpdate:
		part = "set"
	}
	for _, set := range a.Sets {
		if err := bind(set.Formula, part, set.Name); err != nil {
			return nil, err
		}
		out.values = append(out.values, cvalue{name: set.Name, prog: set.Formula})
	}
	for _, key := range a.Keys {
		if err := bind(key.Formula, "key", key.Name); err != nil {
			return nil, err
		}
		out.key = append(out.key, cvalue{name: key.Name, prog: key.Formula})
	}
	if a.ID != nil {
		if err := bind(a.ID, "id", ""); err != nil {
			return nil, err
		}
		out.id = a.ID
	}
	return out, nil
}

// bindTemplate binds the slots of every formula of a template.
func (c *class) bindTemplate(t *model.Template, owner string) error {
	if t.Formula != nil {
		return c.bindSlots(t.Formula, owner)
	}
	for _, f := range t.Fields {
		if err := c.bindTemplate(f.Value, owner+"/"+f.Name); err != nil {
			return err
		}
	}
	for i, item := range t.Items {
		if err := c.bindTemplate(item, owner+"/"+strconv.Itoa(i)); err != nil {
			return err
		}
	}
	return nil
}

// bindElemSlots resolves the slots of a formula of a create with each: a
// slot that reads the element, under the name as gives it, resolves to a
// leaf of the composite's rows and is kept below zero, -1 for the first;
// any other is a leaf of the document.
func (c *class) bindElemSlots(p *formula.Program, owner, as string, ch *child) error {
	c.asks(p)
	idx := make([]int, len(p.Slots))
	elem := strings.ToUpper(as)
	for i, s := range p.Slots {
		name := strings.ToUpper(s.Name)
		li, ok := 0, false
		switch {
		case name == elem:
			// An element of an array is its one column.
			if li, ok = ch.leafIndex["VALUE"]; ok {
				li = -1 - li
			}
		case strings.HasPrefix(name, elem+"."):
			if li, ok = ch.leafIndex[name[len(elem)+1:]]; ok {
				li = -1 - li
			}
		default:
			li, ok = c.leafIndex[name]
		}
		if !ok {
			return hintf(CodeNotSupported, owner, "the formula reads "+s.Name+", which is not a stored scalar", "only scalar variables, object fields and the fields of the element can be read in this build")
		}
		idx[i] = li
	}
	c.slots[p] = idx
	c.maxSlots = max(c.maxSlots, len(idx))
	return nil
}

// loadLists reads the composites the class's creates with each go over,
// as the command leaves them, before its rules run: the rows the command
// gave, else the rows of the version before it, and none for a new
// document. stored is the class a draft reads its documents from. A
// reset that empties one afterwards empties it here too.
func (s *Store) loadLists(ctx context.Context, c, stored *class, before *docRow, kids map[*child][]childRow) error {
	s.lists = s.lists[:0]
	for _, ch := range c.rules.lists {
		rows, given := kids[ch]
		if !given && before != nil {
			from := ch
			if stored != nil {
				// A draft has no statements: the stored class reads the rows,
				// when it has the composite in the same shape.
				prev, ok := stored.childOfName(ch.v.Name)
				if from = prev; !ok || !sameLeaves(prev.leaves, ch.leaves) {
					from = nil
				}
			}
			if from != nil {
				var err error
				if rows, err = from.load(ctx, from.selectRows, before.id, before.version); err != nil {
					return err
				}
			}
		}
		s.lists = append(s.lists, list{ch: ch, rows: rows})
	}
	return nil
}

// queueAction evaluates the arguments of an outbox action over the
// document that fires the rule and queues it with the commit (ADR-0043,
// item 7): once, or once for each element of the list a create's each
// names, in the order of the list, so that every child has a row of its
// own (ADR-0044, item 12). A blank or empty list queues nothing. A
// preview reports each with its arguments and queues none.
func (s *Store) queueAction(run *ruleRun, row *docRow, act *caction, at int) *Error {
	if act.out.each == nil {
		return s.queueOne(run, row, act, at, nil, -1)
	}
	for i := range s.lists {
		if s.lists[i].ch != act.out.each {
			continue
		}
		for pos, elem := range s.lists[i].rows {
			if err := s.queueOne(run, row, act, at, elem, pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// queueOne queues the action once: for the element at pos of its list,
// or with pos below zero for an action without one. An argument that
// errors is the action's failure, and names the element.
func (s *Store) queueOne(run *ruleRun, row *docRow, act *caction, at int, elem childRow, pos int) *Error {
	args, err := s.actionArgs(run, row, act, elem)
	switch {
	case err != nil:
		if pos >= 0 {
			err.Message = act.out.each.v.Name + "[" + strconv.Itoa(pos) + "]: " + err.Message
		}
		if err = run.fail(act, err); err != nil {
			return err
		}
		args = nil
	case !run.preview:
		raw, jerr := json.Marshal(args)
		if jerr != nil {
			err = errf(CodeBadValue, "", "the arguments of the action do not encode: %v", jerr)
			err.Path = act.a.Path()
			return err
		}
		s.queued = append(s.queued, queued{commit: run.commit, act: act, args: raw})
	}
	if run.preview {
		var into []string
		for _, iv := range act.a.Into {
			into = append(into, iv.Name)
		}
		run.res.rules[at].Actions = append(run.res.rules[at].Actions, ActionRun{Action: act.a.Name, Queued: true, Args: args, Into: into})
	}
	return nil
}

// actionArgs evaluates the arguments of an outbox action, in the shape
// the worker reads them back in: the action carries the values of the
// moment that caused it, not of the moment a worker reaches it. elem is
// the element of the list for a create with each.
func (s *Store) actionArgs(run *ruleRun, row *docRow, act *caction, elem childRow) (Values, *Error) {
	a, out := act.a, act.out
	if a.Call {
		// The arguments of a defined action, by parameter (ADR-0045).
		args := make(Values, len(out.args))
		for _, arg := range out.args {
			var err *Error
			if arg.prog != nil {
				var val formula.Value
				if val, err = s.evalArg(run, row, arg.prog, nil); err == nil {
					args[arg.name] = valueOut(val)
				}
			} else {
				args[arg.name], err = s.evalTemplate(run, row, arg.tmpl)
			}
			if err != nil {
				err.Variable = arg.name
				return nil, err
			}
		}
		return args, nil
	}
	values, err := s.argValues(run, row, out.values, elem)
	if err != nil {
		return nil, err
	}
	var id string
	if out.id != nil {
		val, err := s.evalArg(run, row, out.id, elem)
		if err != nil {
			err.Variable = "id"
			return nil, err
		}
		if !val.IsBlank() {
			id = formula.Render(val)
		}
	}
	switch a.Name {
	case model.ActionCreate:
		args := Values{"class": a.Target, "values": values}
		if id != "" {
			args["id"] = id
		}
		return args, nil
	case model.ActionTransition:
		args := Values{"class": a.Target, "id": id, "name": a.Transition}
		if len(values) > 0 {
			args["values"] = values
		}
		return args, nil
	case model.ActionRefresh:
		args := Values{"class": a.Target, "id": id}
		if len(a.Names) > 0 {
			names := make([]any, len(a.Names))
			for i, name := range a.Names {
				names[i] = name
			}
			args["variables"] = names
		}
		return args, nil
	case model.ActionTableInsert:
		return Values{"table": a.Target, "row": values}, nil
	}
	key, err := s.argValues(run, row, out.key, elem)
	if err != nil {
		return nil, err
	}
	if a.Name == model.ActionTableUpdate {
		return Values{"table": a.Target, "key": key, "set": values}, nil
	}
	return Values{"table": a.Target, "key": key}, nil
}

// argValues evaluates the formulas of one part of an action's arguments,
// by the name of what each fills, in the wire shape.
func (s *Store) argValues(run *ruleRun, row *docRow, vals []cvalue, elem childRow) (Values, *Error) {
	out := make(Values, len(vals))
	for _, cv := range vals {
		val, err := s.evalArg(run, row, cv.prog, elem)
		if err != nil {
			err.Variable = cv.name
			return nil, err
		}
		out[cv.name] = valueOut(val)
	}
	return out, nil
}

// evalArg runs a formula of an outbox action over the row, and over the
// element of the list when the action has one.
func (s *Store) evalArg(run *ruleRun, row *docRow, prog *formula.Program, elem childRow) (formula.Value, *Error) {
	if elem == nil {
		return s.eval(run.c, prog, row, run.evalCtx)
	}
	val, err := s.wev.runWith(run.c, prog, row, elem, run.evalCtx)
	if err != nil {
		return formula.Value{}, evalErr("", err)
	}
	return val, nil
}

// evalTemplate makes the JSON value of a template over the row: every
// formula evaluated and rendered in the wire shape, in the place its
// string had.
func (s *Store) evalTemplate(run *ruleRun, row *docRow, t *model.Template) (any, *Error) {
	switch {
	case t.Formula != nil:
		val, err := s.evalArg(run, row, t.Formula, nil)
		if err != nil {
			return nil, err
		}
		return valueOut(val), nil
	case t.Object:
		out := make(map[string]any, len(t.Fields))
		for _, f := range t.Fields {
			val, err := s.evalTemplate(run, row, f.Value)
			if err != nil {
				return nil, err
			}
			out[f.Name] = val
		}
		return out, nil
	case t.Array:
		out := make([]any, len(t.Items))
		for i, item := range t.Items {
			val, err := s.evalTemplate(run, row, item)
			if err != nil {
				return nil, err
			}
			out[i] = val
		}
		return out, nil
	}
	return t.Literal, nil
}

// principalText is what an outbox row keeps of its principal beside the
// id and the administrator's flag: the groups, as a JSON array, and for a
// principal that says what it is or for whom it acts (ADR-0050) an object
// with the groups, the kind and the id acted for. Most rows have neither,
// and are written and read as before: the row gained no column, since
// the driver allocates for every column the statement that reads the due
// rows returns, on every action the worker runs.
func principalText(p Principal) []byte {
	if p.Kind == "" && p.For == "" {
		groups, _ := json.Marshal(p.Groups)
		return groups
	}
	more, _ := json.Marshal(principalMore{Groups: p.Groups, Kind: p.Kind, For: p.For})
	return more
}

type principalMore struct {
	Groups []string `json:"groups"`
	Kind   string   `json:"kind,omitempty"`
	For    string   `json:"for,omitempty"`
}

// readPrincipalText reads what principalText wrote into the principal.
func readPrincipalText(text string, p *Principal) {
	if strings.HasPrefix(text, "{") {
		var more principalMore
		if json.Unmarshal([]byte(text), &more) == nil {
			p.Groups, p.Kind, p.For = more.Groups, more.Kind, more.For
		}
		return
	}
	_ = json.Unmarshal([]byte(text), &p.Groups)
}

// writeQueued writes the outbox rows of one commit of a command, in the
// commit's transaction: commit is 0 for the command's own and n for its
// n-th eventless transition, eventID that commit's event and version the
// version it made. A row deeper than MaxActionDepth is written dead with
// too_deep and its action_failed event.
func (s *Store) writeQueued(ctx context.Context, c *class, cmd *command, commit int, eventID, docID string, version int64, day, secs int64, now time.Time) (int, error) {
	p := cmd.o.principal
	groups := principalText(p)
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
			ev := eventInfo{kind: EventActionFailed, name: q.act.a.Name, cause: eventID}
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
// clock, one at a time on the caller's goroutine, and returns how many it
// attempted (ADR-0044). The actions of one document run in the order they
// were queued, and one that waits for a retry holds back the later ones
// of its document. With OutboxOptions.Manual it is the only thing that
// runs actions; beside a running worker it leaves alone the documents
// whose action the worker has in flight.
func (s *Store) Drain(ctx context.Context, opts ...Option) (int, error) {
	if _, e := s.worker(workOperate, opts); e != nil {
		return 0, e
	}
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		batch, err := s.dueRows(ctx, s.stmts.dueOutbox, s.now().Unix())
		if err != nil {
			return n, err
		}
		progress := false
		for _, row := range batch {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			if !s.claim(row) {
				continue
			}
			ran, err := s.runAction(ctx, row, false)
			s.unclaim(row)
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

// claim takes the row's document for one runner: the actions of a
// document run one at a time (ADR-0044, item 3), and a call is in flight
// outside the writer, where the worker and a drain by hand could both
// reach its row. It reports false when another runner has the document.
func (s *Store) claim(row *outboxRow) bool {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	key := flight{class: row.class, docID: row.docID}
	if _, taken := s.inFlight[key]; taken {
		return false
	}
	s.inFlight[key] = struct{}{}
	return true
}

func (s *Store) unclaim(row *outboxRow) {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	delete(s.inFlight, flight{class: row.class, docID: row.docID})
}

// flight names a document that has an action in flight.
type flight struct{ class, docID string }

// dueRows reads the next actions that may run, with one of the three
// statements that select them: pending, due at now, a reading of the
// database's clock in seconds, and not behind an earlier pending action
// of the same document. There is at most one action per document, so a
// batch may run in any order; a wake-up stands outside the order of its
// document's actions, and does nothing when the document has moved on.
func (s *Store) dueRows(ctx context.Context, due *sql.Stmt, now int64) ([]*outboxRow, error) {
	rows, err := due.QueryContext(ctx, now)
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
		// The row runs as the principal of the command that queued it,
		// with what that principal was and for whom it acted (ADR-0050).
		r.principal = Principal{ID: id.String, Admin: admin != 0}
		if groups.Valid {
			readPrincipalText(groups.String, &r.principal)
		}
		out = append(out, r)
	}
	return out, storageErr("read the outbox", rows.Err())
}

// runAction runs one row. For an action the engine runs itself it holds
// the writer, and the action's commit, the mark and the event of the
// outcome are one transaction (ADR-0044, item 5); any other action is
// called outside it, by runCall. It reports whether the row was
// attempted: a row another runner has taken in the meantime is not. own
// says the worker itself runs the row, in a look of its own.
func (s *Store) runAction(ctx context.Context, row *outboxRow, own bool) (bool, error) {
	timer := row.action == ActionTimer
	if !timer && !model.EngineAction(row.action) {
		return s.runCall(ctx, row)
	}
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
	if timer {
		// What a timer does belongs to the day it fires, by the database's
		// clock, and not to the day of the commit that queued it.
		day = businessDay(options{}, now)
	}
	var queuedMore bool
	var made *Document
	var moved string
	failure := s.at("perform", row)
	if failure == nil {
		if timer {
			moved, queuedMore, failure = s.touch(ctx, row, now)
		} else {
			made, queuedMore, failure = s.perform(ctx, row, day, now)
		}
	}
	if failure == nil {
		failure = s.at("performed", row)
	}
	if failure == nil {
		// The action's commit, the mark, the event of the outcome and the
		// result written back are one transaction. A wake-up has neither
		// an event of its outcome nor a result.
		var err error
		if timer {
			err = s.woke(ctx, row, attempts+1, moved, now)
		} else {
			var ended string
			if ended, err = s.finish(ctx, row, ActionDone, attempts+1, nil, day, now); err == nil {
				var more bool
				more, err = s.applyInto(ctx, row, ended, outcome{}, made, day, now)
				queuedMore = queuedMore || more
			}
		}
		if e := s.at("marked", row); err == nil && e != nil {
			err = e
		}
		if err != nil {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			return false, err
		}
		if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			return false, storageErr("commit", err)
		}
		_ = s.at("committed", row)
		s.wakeQueued(queuedMore, own)
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
	ended, err := s.failed(ctx, row, attempts, failure, noAnswer(failure), 0, day, now)
	if err == nil && ended != "" && !timer {
		// The action is dead: its document hears of that too.
		queuedMore, err = s.applyInto(ctx, row, ended, outcome{failure: failure}, nil, day, now)
	}
	if err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, err
	}
	if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, storageErr("commit", err)
	}
	s.wakeQueued(queuedMore, own)
	return true, nil
}

// failed records an attempt that did not run the action, in the
// transaction the caller began; attempts counts the ones before it. A
// question that got no answer is asked again later, with retry, until
// MaxAttempts; any other failure, and the last attempt, leave the row
// dead with its event (ADR-0044, item 4), which it returns: "" says the
// row waits for another attempt. after is how long the receiver asked to
// be left alone, which moves the next attempt later and never sooner
// (ADR-0045, item 5).
func (s *Store) failed(ctx context.Context, row *outboxRow, attempts int, failure *Error, retry bool, after time.Duration, day int64, now time.Time) (string, error) {
	attempts++
	if !retry || attempts >= s.outbox.MaxAttempts {
		return s.finish(ctx, row, ActionDead, attempts, failure, day, now)
	}
	wait := max(s.outbox.Backoff<<(attempts-1), after)
	due := now.Add(wait).Unix()
	if due <= now.Unix() {
		due = now.Unix() + 1
	}
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET attempts = ?, due_at = ?, error_code = ?, error_message = ? WHERE event_id = ? AND idx = ?`,
		attempts, due, failure.Code, failure.Message, row.eventID, row.idx); err != nil {
		return "", storageErr("record the attempt", err)
	}
	return "", nil
}

// at is the tests' window into the steps of an action: before it is
// performed, after it is and before its mark, after the mark and the
// result written back and before their commit, and after the commit of
// all. A test makes a step fail, or stops the process there (ADR-0044,
// item 10). nil outside tests.
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
// action's and its cause the event of that commit. It returns the id of
// that event.
func (s *Store) finish(ctx context.Context, row *outboxRow, status string, attempts int, failure *Error, day int64, now time.Time) (string, error) {
	secs := now.Unix()
	kind := EventActionRun
	var code, message any
	if status == ActionDead {
		kind, code, message = EventActionFailed, failure.Code, failure.Message
	}
	id := s.ids.next(now)
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = ?, finished_at = ?, error_code = ?, error_message = ?, outcome_event = ?, into_code = NULL, into_message = NULL WHERE event_id = ? AND idx = ?`,
		status, attempts, secs, code, message, id, row.eventID, row.idx); err != nil {
		return "", storageErr("mark the action", err)
	}
	ev := eventInfo{kind: kind, name: row.action, cause: row.eventID}
	s.actAs(row.principal)
	return id, s.writeEvent(ctx, id, row.class, row.docID, ev, row.principal.ID, nil, nil, "", "", day, secs)
}

// perform does what an action stands for, inside the worker's
// transaction, as the principal of the command whose commit queued it
// (ADR-0044, item 11): the field rights and the transition rules of the
// target apply to that person, and what they may not do is a refusal. It
// returns the document the action's commit left, from which what the
// action returns is read (ADR-0045, item 8), nil for a table action, and
// reports whether that commit queued further actions.
func (s *Store) perform(ctx context.Context, row *outboxRow, day int64, now time.Time) (*Document, bool, *Error) {
	var args struct {
		Class     string   `json:"class"`
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Values    Values   `json:"values"`
		Variables []string `json:"variables"`
		Table     string   `json:"table"`
		Row       Values   `json:"row"`
		Key       Values   `json:"key"`
		Set       Values   `json:"set"`
	}
	if err := json.Unmarshal(row.args, &args); err != nil {
		return nil, false, errf(CodeBadValue, "", "the arguments of the action do not read: %v", err)
	}
	// The command the action stands for: the worker's own, caused by the
	// event that queued it, on that event's business day.
	cmd := command{o: options{principal: row.principal, key: outboxKeyPrefix + row.key()},
		cause: row.eventID, depth: row.depth + 1, internal: true, day: day, hasDay: true}
	switch row.action {
	case model.ActionCreate:
		cmd.op, cmd.id, cmd.values = opCreate, args.ID, args.Values
	case model.ActionTransition:
		cmd.op, cmd.id, cmd.transition, cmd.values = opTransition, args.ID, args.Name, args.Values
	case model.ActionRefresh:
		cmd.op, cmd.id, cmd.variables = opRefresh, args.ID, args.Variables
	case model.ActionTableInsert, model.ActionTableUpdate, model.ActionTableDelete:
		t, e := s.table(args.Table)
		if e != nil {
			return nil, false, e
		}
		// The write rule of the table, asked of the person who set the
		// rule off (ADR-0051, item 7).
		if e := s.tableRefusal(t, true, row.principal, now.Unix()); e != nil {
			return nil, false, e
		}
		return nil, false, s.tableAction(ctx, t, row.action, args.Row, args.Key, args.Set)
	default:
		return nil, false, hintf(CodeUnknownAction, "", "the engine does not run the action "+row.action, "the actions it runs are in the class's rules as the definition accepted them; this row was queued by another build")
	}
	c, e := s.class(args.Class)
	if e != nil {
		return nil, false, e
	}
	if cmd.op != opCreate && args.ID == "" {
		return nil, false, hintf(CodeUnknownDocument, "", "the action names no document of "+c.cls.Name+": its id is blank", "the id is a formula over the document that fired the rule; it gave no value there")
	}
	cmd.c = c
	res, err := s.run(ctx, cmd, now)
	if err != nil {
		return nil, false, asError(err)
	}
	return res.doc, res.queued > 0, nil
}

// tableAction writes one row of an explicit table, addressed by its key
// (ADR-0044, item 7): an insert of a key the table has is refused with
// duplicate, an update of one it lacks with unknown_row, and a delete of
// one it lacks deletes nothing and is done. The values are read as an
// import reads them, each against its column. The table's index is
// dropped, as an import drops it (ADR-0036). A table has no rights of its
// own, so the principal is not consulted.
func (s *Store) tableAction(ctx context.Context, t *table, action string, row, key, set Values) *Error {
	vals := make([]formula.Value, len(t.leaves))
	read := func(fields []formula.Field, from Values) *Error {
		for i := range vals {
			vals[i] = formula.Blank()
		}
		if err := setFields(vals, t.leafIndex, "", fields, from, "", s.limits); err != nil {
			err.Variable = t.def.Name + "." + err.Variable
			return err
		}
		return nil
	}
	// keyOf is the condition that addresses the row by the key columns
	// among the values read, with its arguments; a key column is never
	// blank. A money key is its amount, as the table's primary key is.
	keyOf := func() (string, []any, *Error) {
		terms := make([]string, len(t.def.Key))
		args := make([]any, len(t.def.Key))
		for i, k := range t.def.Key {
			li := t.leafIndex[strings.ToUpper(k)]
			if vals[li].IsBlank() {
				return "", nil, errf(CodeRequired, t.def.Name+"."+k, "a key column is not blank")
			}
			terms[i] = quote(t.leaves[li].column) + " = ?"
			args[i] = encode(nil, t.leaves[li], vals[li])[0]
		}
		return " WHERE " + strings.Join(terms, " AND "), args, nil
	}
	var res sql.Result
	var err error
	switch action {
	case model.ActionTableInsert:
		if e := read(t.def.Fields, row); e != nil {
			return e
		}
		if _, _, e := keyOf(); e != nil {
			return e
		}
		args := make([]any, 0, len(t.def.Columns))
		for i, l := range t.leaves {
			args = encode(args, l, vals[i])
		}
		if res, err = t.insert.ExecContext(ctx, args...); err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return hintf(CodeDuplicate, "", "the table "+t.def.Name+" has a row with that key", "table.update changes a row that exists")
			}
		}
	case model.ActionTableUpdate:
		if e := read(t.keyFields(true), key); e != nil {
			return e
		}
		where, keyArgs, e := keyOf()
		if e != nil {
			return e
		}
		if e := read(t.keyFields(false), set); e != nil {
			return e
		}
		given := make(map[string]bool, len(set))
		for name := range set {
			given[strings.ToUpper(name)] = true
		}
		var sets []string
		var args []any
		for li, l := range t.leaves {
			if !given[strings.ToUpper(l.path)] {
				continue
			}
			sets = append(sets, quote(l.column)+" = ?")
			if l.typ.Kind == formula.KindMoney {
				sets = append(sets, quote(l.column+formula.CurrencySuffix)+" = ?")
			}
			args = encode(args, l, vals[li])
		}
		if len(sets) == 0 {
			return errf(CodeBadValue, "", "the update of %s changes no column", t.def.Name)
		}
		if res, err = s.writer.ExecContext(ctx, "UPDATE "+quote(t.name)+" SET "+strings.Join(sets, ", ")+where, append(args, keyArgs...)...); err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return hintf(CodeUnknownRow, "", "the table "+t.def.Name+" has no row with that key", "table.insert adds a row")
			}
		}
	case model.ActionTableDelete:
		if e := read(t.keyFields(true), key); e != nil {
			return e
		}
		where, keyArgs, e := keyOf()
		if e != nil {
			return e
		}
		_, err = s.writer.ExecContext(ctx, "DELETE FROM "+quote(t.name)+where, keyArgs...)
	}
	if err != nil {
		return asError(storageErr("write "+t.def.Name, err))
	}
	t.cache, t.tooBig = nil, false
	return nil
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
func (s *Store) Outbox(ctx context.Context, f OutboxFilter, opts ...Option) ([]QueuedAction, error) {
	if _, e := s.worker(workOperate, opts); e != nil {
		return nil, e
	}
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
	text := `SELECT event_id, idx, class, doc_id, version, rule, action, args, principal_id, depth, status, attempts, due_at, error_code, error_message, created_at, finished_at, outcome_event, into_code, into_message, principal_groups FROM outbox`
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
		var principal, code, message, outcome, intoCode, intoMessage, more sql.NullString
		var finished sql.NullInt64
		if err := rows.Scan(&eventID, &idx, &q.Class, &q.DocID, &q.Version, &q.Rule, &q.Action, &argsText, &principal, &q.Depth, &q.Status, &q.Attempts, &q.DueAt,
			&code, &message, &q.CreatedAt, &finished, &outcome, &intoCode, &intoMessage, &more); err != nil {
			return nil, storageErr("read the outbox", err)
		}
		if more.Valid {
			var p Principal
			readPrincipalText(more.String, &p)
			q.PrincipalKind, q.PrincipalFor = p.Kind, p.For
		}
		q.IntoCode, q.IntoMessage = intoCode.String, intoMessage.String
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
func (s *Store) RetryAction(ctx context.Context, key string, opts ...Option) error {
	if _, e := s.worker(workOperate, opts); e != nil {
		return e
	}
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
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = 0, due_at = ?, finished_at = NULL, into_code = NULL, into_message = NULL WHERE event_id = ? AND idx = ? AND status = ?`,
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

// work is the worker (ADR-0044, item 2): it runs what is due when a
// commit queued something, when a call in flight ends and when the
// earliest waiting action falls due, until the store closes; then it
// waits for the calls in flight. A failure of the store is tried again
// after a pause.
func (s *Store) work(ctx context.Context) {
	defer close(s.workDone)
	var calls sync.WaitGroup
	defer calls.Wait()
	// free holds a token for every call in flight; ended says that one
	// ended since the worker last looked, and failed that the store could
	// not mark one.
	free := make(chan struct{}, s.outbox.Workers)
	ended := make(chan struct{}, 1)
	var failed atomic.Bool
	for {
		// The wait is counted on the database's clock, which is what makes
		// a row due, and bounded so that a clock that stands still or
		// jumps is looked at again. What falls due next is asked of the
		// reading of the clock the last look at what is due was made
		// with: a row whose second begins between two readings would be
		// due for neither, and would wait out the bound.
		wait := time.Minute
		s.looks.Add(1)
		looked, err := s.dispatch(ctx, free, ended, &failed, &calls)
		if err != nil {
			wait = time.Second
		} else if next, ok, err := s.nextDue(ctx, looked); err != nil {
			wait = time.Second
		} else if ok {
			wait = min(max(time.Unix(next, 0).Sub(s.now()), 10*time.Millisecond), time.Minute)
		}
		// A commit that queues a wake-up for a later second than this
		// sleep ends at leaves the worker asleep: it looks then anyway.
		s.sleepUntil.Store(s.now().Add(wait).Unix())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
			s.sleepUntil.Store(0)
		case <-ended:
			s.sleepUntil.Store(0)
			timer.Stop()
			if failed.Swap(false) {
				// A call was made and its mark was not: the row is asked
				// again, and not at once.
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		case <-timer.C:
			s.sleepUntil.Store(0)
		}
	}
}

// dispatch runs what is due and returns when nothing more can start: the
// engine's own actions here, one at a time, since they take the writer,
// and the actions that leave the database each on a goroutine of its
// own, up to Workers at once (ADR-0044, item 2). A call that takes long
// holds one worker and the later actions of its document, and neither a
// commit nor the engine's own actions of other documents (ADR-0027). It
// returns the reading of the clock, in seconds, of its last look, the
// one that started nothing: every row due by then runs, waits for a
// worker or waits behind an action of its document.
func (s *Store) dispatch(ctx context.Context, free, ended chan struct{}, failed *atomic.Bool, calls *sync.WaitGroup) (int64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		progress := false
		// One reading of the clock for the look at both kinds of action.
		now := s.now().Unix()
		batch, err := s.dueRows(ctx, s.stmts.dueEngine, now)
		if err != nil {
			return 0, err
		}
		for _, row := range batch {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if !s.claim(row) {
				continue
			}
			ran, err := s.runAction(ctx, row, true)
			s.unclaim(row)
			if err != nil {
				return 0, err
			}
			progress = progress || ran
		}
		if len(free) < cap(free) {
			if batch, err = s.dueRows(ctx, s.stmts.dueCalls, now); err != nil {
				return 0, err
			}
		start:
			for _, row := range batch {
				if !s.claim(row) {
					continue
				}
				select {
				case free <- struct{}{}:
				default:
					// Every worker is busy; the call that ends next looks again.
					s.unclaim(row)
					break start
				}
				progress = true
				calls.Add(1)
				go func() {
					defer calls.Done()
					if _, err := s.runCall(ctx, row); err != nil && ctx.Err() == nil {
						failed.Store(true)
					}
					s.unclaim(row)
					<-free
					select {
					case ended <- struct{}{}:
					default:
					}
				}()
			}
		}
		if !progress {
			return now, nil
		}
	}
}

// nextDue is when the earliest action that was not due at looked falls
// due, in seconds; looked is the reading of the clock the worker last
// looked at what is due with. What was due by then runs, waits for a
// worker or waits behind an action of its document, and each of those
// ends with a wake-up. A failure of the store is the caller's to wait
// out for a moment, not for the bound of an empty queue.
func (s *Store) nextDue(ctx context.Context, looked int64) (int64, bool, error) {
	var due sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT min(due_at) FROM outbox WHERE status = ? AND due_at > ?`, ActionPending, looked).Scan(&due); err != nil {
		return 0, false, storageErr("read the outbox", err)
	}
	return due.Int64, due.Valid, nil
}
