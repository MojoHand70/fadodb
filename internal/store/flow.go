package store

import (
	"context"
	"database/sql"
	"sort"
	"time"

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
	// The read rule of the class for this caller, as query and count
	// ask it.
	plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o))
	if err != nil {
		return nil, filterErr(err)
	}
	readable := "SELECT \"id\" FROM " + quote(c.cls.Name)
	where := ""
	if plan.Where != "" {
		where = " WHERE " + plan.Where
	}
	rule := append(append([]any{}, plan.Args...), s.contextArgs(o)...)

	flow := &Flow{Class: c.cls.Name, ClassVersion: c.cls.Version}
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
	st, err := s.filters.acquire(ctx, s.db, "SELECT \"state\", count(*), min(\"state_entered_at\") FROM "+quote(c.cls.Name)+where+" GROUP BY \"state\"")
	if err != nil {
		return nil, storageErr("prepare the flow of "+c.cls.Name, err)
	}
	rows, err := st.stmt.QueryContext(ctx, rule...)
	if err != nil {
		s.filters.release(st)
		return nil, storageErr("the flow of "+c.cls.Name, err)
	}
	for rows.Next() {
		var name sql.NullString
		var documents int64
		var oldest sql.NullInt64
		if err := rows.Scan(&name, &documents, &oldest); err != nil {
			rows.Close()
			s.filters.release(st)
			return nil, storageErr("the flow of "+c.cls.Name, err)
		}
		in := state(name.String)
		in.Documents, in.OldestSince = documents, oldest.Int64
	}
	rows.Close()
	s.filters.release(st)
	if err := rows.Err(); err != nil {
		return nil, storageErr("the flow of "+c.cls.Name, err)
	}

	// The moves of those documents in the period, and how long each
	// stay that a move ended had lasted: the version a move left knows
	// when its state was entered.
	text := `SELECT e."kind", e."name", e."from_state", e."to_state", count(*), count(h."state_entered_at"), sum(e."ts" - h."state_entered_at"), max(e."ts" - h."state_entered_at")` +
		` FROM "events" e LEFT JOIN ` + quote(c.history) + ` h ON h."id" = e."doc_id" AND h."version" = e."before_version"` +
		` WHERE e."class" = ? AND e."kind" IN ('` + EventCreated + `', '` + EventTransition + `')`
	args := []any{c.cls.Name}
	if !period.Since.IsZero() {
		flow.Since = period.Since.Unix()
		text += ` AND e."ts" >= ?`
		args = append(args, flow.Since)
	}
	if !period.Until.IsZero() {
		flow.Until = period.Until.Unix()
		text += ` AND e."ts" < ?`
		args = append(args, flow.Until)
	}
	text += ` AND e."doc_id" IN (` + readable + where + `) GROUP BY e."kind", e."name", e."from_state", e."to_state"`
	st, err = s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, storageErr("prepare the moves of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	rows, err = st.stmt.QueryContext(ctx, append(args, rule...)...)
	if err != nil {
		return nil, storageErr("the moves of "+c.cls.Name, err)
	}
	defer rows.Close()
	type edge struct{ name, from, to string }
	moved := map[edge]int64{}
	lasted := map[string]*struct{ timed, seconds int64 }{}
	for rows.Next() {
		var kind string
		var name, from, to sql.NullString
		var moves, timed int64
		var seconds, longest sql.NullInt64
		if err := rows.Scan(&kind, &name, &from, &to, &moves, &timed, &seconds, &longest); err != nil {
			return nil, storageErr("the moves of "+c.cls.Name, err)
		}
		if kind == EventCreated {
			flow.Created += moves
			continue
		}
		moved[edge{name.String, from.String, to.String}] += moves
		left := state(from.String)
		state(to.String)
		left.Stays += moves
		left.LongestSeconds = max(left.LongestSeconds, longest.Int64)
		sum := lasted[from.String]
		if sum == nil {
			sum = &struct{ timed, seconds int64 }{}
			lasted[from.String] = sum
		}
		sum.timed += timed
		sum.seconds += seconds.Int64
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("the moves of "+c.cls.Name, err)
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
	return flow, nil
}
