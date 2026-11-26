package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Throughput is how many documents of a class were made and how many
// finished in each day, week or month, as one caller sees it
// (ADR-0096): whether the pile grows. A document is made when it is
// created, and finished when it comes into a final state.
type Throughput struct {
	Class        string `json:"class"`
	ClassVersion int    `json:"class_version"`
	// Since and Until are the period that was asked for, in seconds; 0
	// where the caller named no end.
	Since int64 `json:"since,omitempty"`
	Until int64 `json:"until,omitempty"`
	// By is the length of a period: day, week or month. TimeZone is the
	// zone by whose clock a day begins: the database's own (ADR-0093).
	By       string `json:"by"`
	TimeZone string `json:"time_zone"`
	// Created and Finished are the sums over the periods.
	Created  int64 `json:"created"`
	Finished int64 `json:"finished"`
	// Periods are the periods from the first in which a document was
	// made or finished to the last, each of them, in their order.
	Periods []ThroughputPeriod `json:"periods"`
}

// ThroughputPeriod is one day, week or month: From is its first day,
// as 2026-11-02; a week begins on a Monday and a month on its first.
type ThroughputPeriod struct {
	From     string `json:"from"`
	Created  int64  `json:"created"`
	Finished int64  `json:"finished"`
}

// ThroughputAsk says what a throughput is asked over: from Since and
// before Until, a zero moment leaving that end open, in periods of By,
// which is ThroughputDay when left empty.
type ThroughputAsk struct {
	Since, Until time.Time
	By           string
}

// The lengths of a period of a throughput.
const (
	ThroughputDay   = "day"
	ThroughputWeek  = "week"
	ThroughputMonth = "month"
)

// MaxThroughputPeriods is the most periods a throughput answers: more
// is refused, and is asked by a longer period or over a shorter time.
const MaxThroughputPeriods = 10000

// throughputStep is the stretch of time the engines count by: a quarter
// of an hour. Every time zone in use stands a whole number of quarters
// from UTC, so a quarter lies in one day of the database's zone, and
// the counts are put into days, weeks and months here, where the zones
// and their changes of the clock are known. Neither SQLite nor the
// statement written for the copy knows them.
const throughputStep = 900

// Throughput answers how many documents of a class were made and how
// many finished in each period, for its caller: the documents the
// caller may read, by the read rule of the class. A deleted document is
// not counted. It is one statement, whatever the number of documents.
func (s *Store) Throughput(ctx context.Context, class string, ask ThroughputAsk, opts ...Option) (*Throughput, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	if c.cls.States == nil {
		return nil, hintf(CodeStateless, "", c.cls.Name+" declares no states: nothing of it is finished", "add a states block with an initial state")
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	if !ask.Since.IsZero() && !ask.Until.IsZero() && !ask.Until.After(ask.Since) {
		return nil, hintf(CodeBadValue, "", "the period ends before it begins", "give an until that is after since, or leave one out")
	}
	by := ask.By
	if by == "" {
		by = ThroughputDay
	}
	if by != ThroughputDay && by != ThroughputWeek && by != ThroughputMonth {
		return nil, &Error{Code: CodeBadValue, Path: "/by", Message: "by is day, week or month, got " + by, Hint: `write "day", "week" or "month", or leave it out for days`}
	}
	out := &Throughput{Class: c.cls.Name, ClassVersion: c.cls.Version, By: by, TimeZone: s.zone.String(), Periods: []ThroughputPeriod{}}
	if !ask.Since.IsZero() {
		out.Since = ask.Since.Unix()
	}
	if !ask.Until.IsZero() {
		out.Until = ask.Until.Unix()
	}
	steps, asked := s.throughputFromCopy(ctx, c, o, out)
	if !asked {
		var err error
		if steps, err = s.throughputFromStore(ctx, c, o, out); err != nil {
			return nil, err
		}
	}
	// The quarters into the periods of the database's zone.
	in := map[time.Time]*ThroughputPeriod{}
	var first, last time.Time
	for _, st := range steps {
		if st.created == 0 && st.finished == 0 {
			continue
		}
		from := periodOf(time.Unix(st.step*throughputStep, 0).In(s.zone), by)
		p := in[from]
		if p == nil {
			p = &ThroughputPeriod{From: from.Format(time.DateOnly)}
			in[from] = p
			if first.IsZero() || from.Before(first) {
				first = from
			}
			if from.After(last) {
				last = from
			}
		}
		p.Created += st.created
		p.Finished += st.finished
		out.Created += st.created
		out.Finished += st.finished
	}
	// Every period from the first to the last, the empty ones too: a
	// chart has no gaps.
	if len(in) > 0 {
		for from := first; !from.After(last); from = nextPeriod(from, by) {
			if len(out.Periods) == MaxThroughputPeriods {
				return nil, hintf(CodeTooManyRows, "", "the throughput of "+c.cls.Name+" by "+by+" has more than 10,000 periods", "ask by week or by month, or give a since and an until that lie closer")
			}
			if p := in[from]; p != nil {
				out.Periods = append(out.Periods, *p)
			} else {
				out.Periods = append(out.Periods, ThroughputPeriod{From: from.Format(time.DateOnly)})
			}
		}
	}
	// A content administrator past the rules: the entry holds how many
	// of the documents only the override counted (ADR-0067, item 3).
	if o.records(c) {
		build := func(p query.Principal) (string, []any, error) {
			plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), p)
			if err != nil {
				return "", nil, filterErr(err)
			}
			return plan.Where, plan.Args, nil
		}
		plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o, c))
		if err != nil {
			return nil, filterErr(err)
		}
		all, err := s.countInStore(ctx, c, plan, o)
		if err != nil {
			return nil, err
		}
		past, err := s.pastCount(ctx, c, o, all, build)
		if err != nil {
			return nil, err
		}
		r := newRead("throughput", c)
		r.counted, r.hasCounted, r.past = past, true, past > 0
		if err := s.logRead(ctx, o, r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// periodOf is the first midnight of the day, the week or the month a
// moment lies in, by the clock of the moment's own zone. A week begins
// on a Monday.
func periodOf(t time.Time, by string) time.Time {
	y, m, d := t.Date()
	switch by {
	case ThroughputWeek:
		d -= (int(t.Weekday()) + 6) % 7
	case ThroughputMonth:
		d = 1
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// nextPeriod is the first day of the period after the one that begins
// on from. The days are counted in UTC, where each has 24 hours: they
// are days of a calendar, and no moments.
func nextPeriod(from time.Time, by string) time.Time {
	switch by {
	case ThroughputWeek:
		return from.AddDate(0, 0, 7)
	case ThroughputMonth:
		return from.AddDate(0, 1, 0)
	}
	return from.AddDate(0, 0, 1)
}

// throughputStepRow is what one quarter of an hour holds: how many of
// the caller's documents were made in it, and how many finished.
type throughputStepRow struct {
	step, created, finished int64
}

// finalStates writes the final states of a class as the values of an
// IN, or answers false when it has none: nothing is then ever finished.
func finalStates(c *class) (string, bool) {
	if len(c.cls.States.Final) == 0 {
		return "", false
	}
	quoted := make([]string, len(c.cls.States.Final))
	for i, name := range c.cls.States.Final {
		quoted[i] = "'" + strings.ReplaceAll(name, "'", "''") + "'"
	}
	return strings.Join(quoted, ", "), true
}

// throughputFromStore reads the quarters from the store, over the
// events, as the flow reads its moves: a document that was deleted is
// in none.
func (s *Store) throughputFromStore(ctx context.Context, c *class, o options, out *Throughput) ([]throughputStepRow, error) {
	plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, filterErr(err)
	}
	where := ""
	if plan.Where != "" {
		where = " WHERE " + plan.Where
	}
	finished := "0"
	if finals, any := finalStates(c); any {
		finished = `sum(CASE WHEN e."to_state" IN (` + finals + `) THEN 1 ELSE 0 END)`
	}
	args := []any{c.cls.Name}
	text := `SELECT e."ts" / 900, sum(CASE WHEN e."kind" = '` + EventCreated + `' THEN 1 ELSE 0 END), ` + finished +
		` FROM "events" e WHERE e."class" = ? AND e."kind" IN ('` + EventCreated + `', '` + EventTransition + `')`
	if out.Since != 0 {
		text += ` AND e."ts" >= ?`
		args = append(args, out.Since)
	}
	if out.Until != 0 {
		text += ` AND e."ts" < ?`
		args = append(args, out.Until)
	}
	text += ` AND e."doc_id" IN (SELECT "id" FROM ` + quote(c.cls.Name) + where + `) GROUP BY 1`
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, storageErr("prepare the throughput of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	rows, err := st.stmt.QueryContext(ctx, append(append(args, plan.Args...), s.contextArgs(o)...)...)
	if err != nil {
		return nil, storageErr("the throughput of "+c.cls.Name, err)
	}
	defer rows.Close()
	var steps []throughputStepRow
	for rows.Next() {
		var st throughputStepRow
		if err := rows.Scan(&st.step, &st.created, &st.finished); err != nil {
			return nil, storageErr("the throughput of "+c.cls.Name, err)
		}
		steps = append(steps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("the throughput of "+c.cls.Name, err)
	}
	return steps, nil
}

// throughputFromCopy reads the quarters from the copy in the analytical
// engine, over the moves it keeps ready (ADR-0089, item 2), after the
// copy was brought up to the database. It answers false when the store
// has to answer: the program keeps no copy, or the copy did not answer.
func (s *Store) throughputFromCopy(ctx context.Context, c *class, o options, out *Throughput) ([]throughputStepRow, bool) {
	if s.feeder == nil {
		return nil, false
	}
	d := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(d, Filter{Class: c.cls.Name}, nil, c.cls, s.sources(), caller(o, c))
	if err != nil {
		// The store says what is wrong with the question.
		return nil, false
	}
	finished := "CAST(0 AS BIGINT)"
	if finals, any := finalStates(c); any {
		finished = `count(*) FILTER (WHERE "to_state" IN (` + finals + `))`
	}
	var args []any
	bind := func(name string, v any) string {
		args = append(args, sql.Named(name, v))
		return d.Param(name)
	}
	text := `SELECT "ts" // 900, count(*) FILTER (WHERE "kind" = '` + EventCreated + `'), ` + finished + ` FROM ` + quote(c.cls.Name+copyMoves)
	var only []string
	if out.Since != 0 {
		only = append(only, `"ts" >= `+bind("since", out.Since))
	}
	if out.Until != 0 {
		only = append(only, `"ts" < `+bind("until", out.Until))
	}
	if plan.Where != "" {
		only = append(only, `"id" IN (SELECT "id" FROM `+quote(c.cls.Name)+` WHERE `+plan.Where+`)`)
		evalCtx := contextOf(o.principal, s.clock(o))
		args = append(append(args, plan.Args...), d.ContextArgs(&evalCtx)...)
	}
	if len(only) > 0 {
		text += " WHERE " + strings.Join(only, " AND ")
	}
	text += " GROUP BY 1"
	f, ready := s.copyReady(ctx)
	if !ready {
		return nil, false
	}
	_, rows, err := f.copy.Ask(ctx, text, args...)
	if err != nil {
		f.notAnswered(err)
		return nil, false
	}
	steps := make([]throughputStepRow, 0, len(rows))
	for _, row := range rows {
		if len(row) != 3 {
			f.notAnswered(errors.New("the copy answered the throughput with a row of another shape"))
			return nil, false
		}
		steps = append(steps, throughputStepRow{step: whole(row[0]), created: whole(row[1]), finished: whole(row[2])})
	}
	f.answered()
	sort.Slice(steps, func(i, j int) bool { return steps[i].step < steps[j].step })
	return steps, true
}
