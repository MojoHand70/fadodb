package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// The flow of a class (ADR-0063): its states with the documents that
// are in each now, and its transitions with the moves along each, in
// one answer. It is what a chart of a process draws, and what an agent
// reads to learn where documents wait. It counts the documents its
// caller may read and no other, so that a count tells nobody of a
// document they may not see.

// Flow is the flow of a class as one caller sees it.
type Flow struct {
	Class        string `json:"class"`
	ClassVersion int    `json:"class_version"`
	// Since and Until are the period the moves and the stays are
	// counted in, in seconds; 0 where the caller named no end.
	Since int64 `json:"since,omitempty"`
	Until int64 `json:"until,omitempty"`
	// Created is how many of the documents were created in the period.
	Created     int64            `json:"created"`
	States      []FlowState      `json:"states"`
	Transitions []FlowTransition `json:"transitions"`
}

// FlowState is a state of the class in its flow.
type FlowState struct {
	Name    string `json:"name"`
	Initial bool   `json:"initial,omitempty"`
	Final   bool   `json:"final,omitempty"`
	// Retired says the class no longer has the state, and documents
	// left it in the period: a class that is defined again keeps what
	// happened (ADR-0039). A state that documents are in cannot be taken
	// out of a class.
	Retired bool `json:"retired,omitempty"`
	// Documents is how many documents are in the state now, and
	// OldestSince the moment the one that is there longest entered it.
	Documents   int64 `json:"documents"`
	OldestSince int64 `json:"oldest_since,omitempty"`
	// Stays is how many stays in the state ended in the period, and
	// AverageSeconds and LongestSeconds how long they lasted.
	Stays          int64 `json:"stays"`
	AverageSeconds int64 `json:"average_seconds"`
	LongestSeconds int64 `json:"longest_seconds"`
}

// FlowTransition is a transition of the class in its flow, from one
// state to one state: a transition that leaves several states is listed
// once for each.
type FlowTransition struct {
	Name string `json:"name,omitempty"`
	From string `json:"from"`
	To   string `json:"to"`
	// Automatic says the transition has no name: the engine takes it
	// when its guard holds. Timed says it fires by time.
	Automatic bool `json:"automatic,omitempty"`
	Timed     bool `json:"timed,omitempty"`
	// Retired says the class no longer has the transition, and
	// documents moved along it in the period.
	Retired bool `json:"retired,omitempty"`
	// Moves is how many times a document moved along it in the period.
	Moves int64 `json:"moves"`
}

// FlowPeriod is the period the moves and the stays of a flow are
// counted in: from Since, and before Until. A zero moment leaves that
// end open, so the zero period is since the class began.
type FlowPeriod struct {
	Since, Until time.Time
}

// Flow answers the flow of a class for its caller: the documents the
// caller may read, by the read rule of the class, in each state now;
// and the moves of those documents along each transition, with how long
// the stays that ended lasted, in the period. A deleted document is not
// counted. The work is two statements, whatever the number of
// documents.
func (s *Store) Flow(ctx context.Context, class string, period FlowPeriod, opts ...Option) (*Flow, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	if c.cls.States == nil {
		return nil, hintf(CodeStateless, "", c.cls.Name+" declares no states: it is a stateless application and has no flow", "add a states block with an initial state")
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	if !period.Since.IsZero() && !period.Until.IsZero() && !period.Until.After(period.Since) {
		return nil, hintf(CodeBadValue, "", "the period ends before it begins", "give an until that is after since, or leave one out")
	}
	flow := &Flow{Class: c.cls.Name, ClassVersion: c.cls.Version}
	if !period.Since.IsZero() {
		flow.Since = period.Since.Unix()
	}
	if !period.Until.IsZero() {
		flow.Until = period.Until.Unix()
	}
	// The two answers a flow is made of. A program that keeps a copy of
	// the documents in an analytical engine asks the copy, which is
	// built for reading everything (ADR-0089, items 5 to 7); what the
	// copy cannot answer, and every program without one, the store
	// answers. The answer is the same.
	now, moves, asked := s.flowFromCopy(ctx, c, o, flow)
	if !asked {
		var err error
		if now, moves, err = s.flowFromStore(ctx, c, o, flow); err != nil {
			return nil, err
		}
	}

	states := map[string]*FlowState{}
	state := func(name string) *FlowState {
		if st, known := states[name]; known {
			return st
		}
		st := &FlowState{Name: name, Retired: true}
		states[name] = st
		return st
	}
	for _, name := range c.cls.States.List {
		state(name).Retired = false
	}
	states[c.cls.States.Initial].Initial = true
	for _, name := range c.cls.States.Final {
		states[name].Final = true
	}
	// The documents in each state now.
	for _, in := range now {
		st := state(in.state)
		st.Documents, st.OldestSince = in.documents, in.oldest
	}
	// The moves of those documents in the period, and how long each
	// stay that a move ended had lasted.
	type edge struct{ name, from, to string }
	moved := map[edge]int64{}
	lasted := map[string]*struct{ timed, seconds int64 }{}
	for _, m := range moves {
		if m.kind == EventCreated {
			flow.Created += m.moves
			continue
		}
		moved[edge{m.name, m.from, m.to}] += m.moves
		left := state(m.from)
		state(m.to)
		left.Stays += m.moves
		left.LongestSeconds = max(left.LongestSeconds, m.longest)
		sum := lasted[m.from]
		if sum == nil {
			sum = &struct{ timed, seconds int64 }{}
			lasted[m.from] = sum
		}
		sum.timed += m.timed
		sum.seconds += m.seconds
	}
	for name, sum := range lasted {
		if sum.timed > 0 {
			states[name].AverageSeconds = (sum.seconds + sum.timed/2) / sum.timed
		}
	}

	// The states in the order of the class, and after them the ones it
	// no longer has, by name.
	for _, name := range c.cls.States.List {
		flow.States = append(flow.States, *states[name])
		delete(states, name)
	}
	retired := make([]string, 0, len(states))
	for name := range states {
		retired = append(retired, name)
	}
	sort.Strings(retired)
	for _, name := range retired {
		flow.States = append(flow.States, *states[name])
	}
	// The transitions in the order of the class, one for each state a
	// transition leaves, and after them the moves along what the class
	// no longer has.
	flow.Transitions = []FlowTransition{}
	for _, t := range c.cls.Transitions {
		for _, from := range t.From {
			at := edge{t.Name, from, t.To}
			flow.Transitions = append(flow.Transitions, FlowTransition{Name: t.Name, From: from, To: t.To, Automatic: t.Name == "", Timed: t.Timed(), Moves: moved[at]})
			delete(moved, at)
		}
	}
	gone := make([]edge, 0, len(moved))
	for at := range moved {
		gone = append(gone, at)
	}
	sort.Slice(gone, func(i, j int) bool {
		a, b := gone[i], gone[j]
		if a.from != b.from {
			return a.from < b.from
		}
		if a.to != b.to {
			return a.to < b.to
		}
		return a.name < b.name
	})
	for _, at := range gone {
		flow.Transitions = append(flow.Transitions, FlowTransition{Name: at.name, From: at.from, To: at.to, Automatic: at.name == "", Retired: true, Moves: moved[at]})
	}
	// A content administrator past the rules: the entry holds how many of
	// the documents in the states now only the override counted
	// (ADR-0067, item 3).
	if o.records(c) {
		var all int64
		for _, in := range flow.States {
			all += in.Documents
		}
		past, err := s.pastCount(ctx, c, o, all, func(p query.Principal) (string, []any, error) {
			plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), p)
			if err != nil {
				return "", nil, filterErr(err)
			}
			return plan.Where, plan.Args, nil
		})
		if err != nil {
			return nil, err
		}
		r := newRead("flow", c)
		r.counted, r.hasCounted, r.past = past, true, past > 0
		if err := s.logRead(ctx, o, r); err != nil {
			return nil, err
		}
	}
	return flow, nil
}

// flowNow is how many documents the caller may read are in a state now,
// and since when the one that is there longest; flowMove is how many
// times those documents moved one way in the period, how many of the
// stays those moves ended are timed, and how long they lasted in all
// and at the longest. A flow is made of the two.
type (
	flowNow struct {
		state             string
		documents, oldest int64
	}
	flowMove struct {
		kind, name, from, to           string
		moves, timed, seconds, longest int64
	}
)

// flowStatements are the two statements of a flow: where is the
// reader's rule over the documents as they are now, " WHERE ..." or
// nothing; versions is the table of every version; bind takes a value
// of the second statement and answers what stands for it there; and
// whole writes a sum as a whole number of the engine. The work is two
// statements, whatever the number of documents.
func flowStatements(c *class, flow *Flow, where, versions string, bind func(name string, v any) string, whole func(string) string) (string, string) {
	now := `SELECT "state", count(*), min("state_entered_at") FROM ` + quote(c.cls.Name) + where + ` GROUP BY "state"`
	// The version a move left knows when its state was entered.
	moves := `SELECT e."kind", e."name", e."from_state", e."to_state", count(*), count(h."state_entered_at"), ` +
		whole(`sum(e."ts" - h."state_entered_at")`) + `, max(e."ts" - h."state_entered_at")` +
		` FROM "events" e LEFT JOIN ` + quote(versions) + ` h ON h."id" = e."doc_id" AND h."version" = e."before_version"` +
		` WHERE e."class" = ` + bind("flow_class", c.cls.Name) + ` AND e."kind" IN ('` + EventCreated + `', '` + EventTransition + `')`
	if flow.Since != 0 {
		moves += ` AND e."ts" >= ` + bind("flow_since", flow.Since)
	}
	if flow.Until != 0 {
		moves += ` AND e."ts" < ` + bind("flow_until", flow.Until)
	}
	moves += ` AND e."doc_id" IN (SELECT "id" FROM ` + quote(c.cls.Name) + where + `) GROUP BY e."kind", e."name", e."from_state", e."to_state"`
	return now, moves
}

// flowFromStore reads the two answers of a flow from the store.
func (s *Store) flowFromStore(ctx context.Context, c *class, o options, flow *Flow) ([]flowNow, []flowMove, error) {
	// The read rule of the class for this caller, as query and count
	// ask it.
	plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, nil, filterErr(err)
	}
	where := ""
	if plan.Where != "" {
		where = " WHERE " + plan.Where
	}
	rule := append(append([]any{}, plan.Args...), s.contextArgs(o)...)
	var args []any
	nowSQL, movesSQL := flowStatements(c, flow, where, c.history, func(_ string, v any) string {
		args = append(args, v)
		return "?"
	}, func(sum string) string { return sum })

	st, err := s.filters.acquire(ctx, s.db, nowSQL)
	if err != nil {
		return nil, nil, storageErr("prepare the flow of "+c.cls.Name, err)
	}
	rows, err := st.stmt.QueryContext(ctx, rule...)
	if err != nil {
		s.filters.release(st)
		return nil, nil, storageErr("the flow of "+c.cls.Name, err)
	}
	var now []flowNow
	for rows.Next() {
		var name sql.NullString
		var documents int64
		var oldest sql.NullInt64
		if err := rows.Scan(&name, &documents, &oldest); err != nil {
			rows.Close()
			s.filters.release(st)
			return nil, nil, storageErr("the flow of "+c.cls.Name, err)
		}
		now = append(now, flowNow{state: name.String, documents: documents, oldest: oldest.Int64})
	}
	rows.Close()
	s.filters.release(st)
	if err := rows.Err(); err != nil {
		return nil, nil, storageErr("the flow of "+c.cls.Name, err)
	}

	st, err = s.filters.acquire(ctx, s.db, movesSQL)
	if err != nil {
		return nil, nil, storageErr("prepare the moves of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	rows, err = st.stmt.QueryContext(ctx, append(args, rule...)...)
	if err != nil {
		return nil, nil, storageErr("the moves of "+c.cls.Name, err)
	}
	defer rows.Close()
	var moves []flowMove
	for rows.Next() {
		var kind string
		var name, from, to sql.NullString
		var n, timed int64
		var seconds, longest sql.NullInt64
		if err := rows.Scan(&kind, &name, &from, &to, &n, &timed, &seconds, &longest); err != nil {
			return nil, nil, storageErr("the moves of "+c.cls.Name, err)
		}
		moves = append(moves, flowMove{kind: kind, name: name.String, from: from.String, to: to.String, moves: n, timed: timed, seconds: seconds.Int64, longest: longest.Int64})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, storageErr("the moves of "+c.cls.Name, err)
	}
	return now, moves, nil
}

// flowFromCopy reads the two answers of a flow from the copy in the
// analytical engine, after it has brought the copy up to the database:
// an answer is never older than its question (ADR-0089, item 5). It
// reports false when the copy did not answer, for whatever reason: the
// program keeps none, the feed failed, or the copy cannot run the
// statement. The store then answers, and what the copy said is kept
// for CopyState.
func (s *Store) flowFromCopy(ctx context.Context, c *class, o options, flow *Flow) ([]flowNow, []flowMove, bool) {
	if s.feeder == nil {
		return nil, nil, false
	}
	// The reader's rule, written for the analytical engine. The copy
	// holds the documents of the class in a table of its name, with
	// the columns of its view.
	d := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(d, Filter{Class: c.cls.Name}, nil, c.cls, s.sources(), caller(o, c))
	if err != nil {
		// The store says what is wrong with the question.
		return nil, nil, false
	}
	where := ""
	if plan.Where != "" {
		where = " WHERE " + plan.Where
	}
	evalCtx := contextOf(o.principal, s.clock(o))
	rule := append(append([]any{}, plan.Args...), d.ContextArgs(&evalCtx)...)
	// DuckDB takes the values of a statement by name or by place and
	// not both; the reader's rule names its own, so these are named.
	var args []any
	nowSQL, movesSQL := flowStatements(c, flow, where, c.cls.Name+copyVersions, func(name string, v any) string {
		args = append(args, sql.Named(name, v))
		return d.Param(name)
	}, func(sum string) string { return "CAST(" + sum + " AS BIGINT)" })
	f, ready := s.copyReady(ctx)
	if !ready {
		return nil, nil, false
	}
	fail := func(err error) ([]flowNow, []flowMove, bool) {
		f.notAnswered(err)
		return nil, nil, false
	}
	_, rows, err := f.copy.Ask(ctx, nowSQL, rule...)
	if err != nil {
		return fail(err)
	}
	now := make([]flowNow, 0, len(rows))
	for _, row := range rows {
		if len(row) != 3 {
			return fail(errors.New("the copy answered the flow with a row of another shape"))
		}
		now = append(now, flowNow{state: text(row[0]), documents: whole(row[1]), oldest: whole(row[2])})
	}
	_, rows, err = f.copy.Ask(ctx, movesSQL, append(args, rule...)...)
	if err != nil {
		return fail(err)
	}
	moves := make([]flowMove, 0, len(rows))
	for _, row := range rows {
		if len(row) != 8 {
			return fail(errors.New("the copy answered the moves with a row of another shape"))
		}
		moves = append(moves, flowMove{kind: text(row[0]), name: text(row[1]), from: text(row[2]), to: text(row[3]),
			moves: whole(row[4]), timed: whole(row[5]), seconds: whole(row[6]), longest: whole(row[7])})
	}
	f.answered()
	return now, moves, true
}

// text and whole read a value the copy answered: a blank is the empty
// text and zero.
func text(v any) string {
	s, _ := v.(string)
	return s
}

func whole(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
