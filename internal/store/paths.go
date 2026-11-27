package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Paths are the paths the documents of a class took through its states,
// as one caller sees them (ADR-0097): the most taken first, each with
// how many documents took it.
type Paths struct {
	Class        string `json:"class"`
	ClassVersion int    `json:"class_version"`
	// Since and Until are the period the documents were made in, in
	// seconds; 0 where the caller named no end.
	Since int64 `json:"since,omitempty"`
	Until int64 `json:"until,omitempty"`
	// Documents is how many documents were counted, and Different how
	// many different paths they took.
	Documents int64 `json:"documents"`
	Different int64 `json:"different"`
	// Paths are the most taken, at most as many as were asked for, and
	// Others how many documents took a path that is not among them.
	Paths  []Path `json:"paths"`
	Others int64  `json:"others"`
}

// Path is one way through the states: the states in the order a
// document came into them, the first being the state it was made in,
// and a state as often as it was entered. Finished says the path ends
// in a final state; a path that does not is of documents still open,
// and is their path so far.
type Path struct {
	States    []string `json:"states"`
	Documents int64    `json:"documents"`
	Finished  bool     `json:"finished,omitempty"`
}

// PathsAsk says which paths are asked for: of the documents made from
// Since and before Until, a zero moment leaving that end open, and the
// Limit most taken, DefaultPaths when it is 0.
type PathsAsk struct {
	Since, Until time.Time
	Limit        int
}

// DefaultPaths and MaxPaths are how many paths an answer lists when it
// is told nothing, and at the most.
const (
	DefaultPaths = 20
	MaxPaths     = 1000
)

// pathStep stands between two states of a path in the one text the
// engines hand over: no name of a state holds it.
const pathStep = ">"

// Paths answers the paths the documents of a class took, for its
// caller: of the documents the caller may read that were made in the
// period, each with every state it came into since, up to now. A
// deleted document is not counted. It is one statement, whatever the
// number of documents.
func (s *Store) Paths(ctx context.Context, class string, ask PathsAsk, opts ...Option) (*Paths, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	if c.cls.States == nil {
		return nil, hintf(CodeStateless, "", c.cls.Name+" declares no states: its documents take no path", "add a states block with an initial state")
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	if !ask.Since.IsZero() && !ask.Until.IsZero() && !ask.Until.After(ask.Since) {
		return nil, hintf(CodeBadValue, "", "the period ends before it begins", "give an until that is after since, or leave one out")
	}
	limit := ask.Limit
	switch {
	case limit == 0:
		limit = DefaultPaths
	case limit < 0 || limit > MaxPaths:
		return nil, &Error{Code: CodeBadValue, Path: "/limit", Message: "the limit is 1 to " + strconv.Itoa(MaxPaths) + ", got " + strconv.Itoa(ask.Limit)}
	}
	out := &Paths{Class: c.cls.Name, ClassVersion: c.cls.Version, Paths: []Path{}}
	if !ask.Since.IsZero() {
		out.Since = ask.Since.Unix()
	}
	if !ask.Until.IsZero() {
		out.Until = ask.Until.Unix()
	}
	taken, asked := s.pathsFromCopy(ctx, c, o, out, limit)
	if !asked {
		var err error
		if taken, err = s.pathsFromStore(ctx, c, o, out, limit); err != nil {
			return nil, err
		}
	}
	final := map[string]bool{}
	for _, name := range c.cls.States.Final {
		final[name] = true
	}
	listed := int64(0)
	for _, p := range taken {
		states := strings.Split(p.path, pathStep)
		out.Paths = append(out.Paths, Path{States: states, Documents: p.documents, Finished: final[states[len(states)-1]]})
		out.Documents, out.Different = p.all, p.different
		listed += p.documents
	}
	out.Others = out.Documents - listed
	if err := s.readPast(ctx, c, o, "paths"); err != nil {
		return nil, err
	}
	return out, nil
}

// pathTaken is one path as an engine hands it over: its states as one
// text, how many documents took it, and, the same in every row, how
// many documents were counted and how many different paths they took.
type pathTaken struct {
	path                      string
	documents, all, different int64
}

// pathsFromStore reads the paths from the store, over the events: the
// states a document came into, in the order of the versions the moves
// made, which tells also the moves of one second apart.
func (s *Store) pathsFromStore(ctx context.Context, c *class, o options, out *Paths, limit int) ([]pathTaken, error) {
	plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, filterErr(err)
	}
	args := []any{c.cls.Name}
	var only []string
	if out.Since != 0 {
		only = append(only, `"created_at" >= ?`)
		args = append(args, out.Since)
	}
	if out.Until != 0 {
		only = append(only, `"created_at" < ?`)
		args = append(args, out.Until)
	}
	if plan.Where != "" {
		only = append(only, "("+plan.Where+")")
	}
	where := ""
	if len(only) > 0 {
		where = " WHERE " + strings.Join(only, " AND ")
	}
	text := `SELECT "p", "n", sum("n") OVER (), count(*) OVER () FROM (SELECT "p", count(*) AS "n" FROM (` +
		`SELECT group_concat(e."to_state", '` + pathStep + `' ORDER BY e."after_version") AS "p" FROM "events" e` +
		` WHERE e."class" = ? AND e."kind" IN ('` + EventCreated + `', '` + EventTransition + `')` +
		` AND e."doc_id" IN (SELECT "id" FROM ` + quote(c.cls.Name) + where + `) GROUP BY e."doc_id") GROUP BY "p")` +
		` ORDER BY "n" DESC, "p" LIMIT ` + strconv.Itoa(limit)
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, storageErr("prepare the paths of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	rows, err := st.stmt.QueryContext(ctx, append(append(args, plan.Args...), s.contextArgs(o)...)...)
	if err != nil {
		return nil, storageErr("the paths of "+c.cls.Name, err)
	}
	defer rows.Close()
	var taken []pathTaken
	for rows.Next() {
		var p pathTaken
		if err := rows.Scan(&p.path, &p.documents, &p.all, &p.different); err != nil {
			return nil, storageErr("the paths of "+c.cls.Name, err)
		}
		taken = append(taken, p)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("the paths of "+c.cls.Name, err)
	}
	return taken, nil
}

// pathsFromCopy reads the paths from the copy in the analytical engine,
// over the moves it keeps ready, each with the version it made
// (ADR-0089, item 2; ADR-0097), after the copy was brought up to the
// database. It answers false when the store has to answer.
func (s *Store) pathsFromCopy(ctx context.Context, c *class, o options, out *Paths, limit int) ([]pathTaken, bool) {
	if s.feeder == nil {
		return nil, false
	}
	d := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(d, Filter{Class: c.cls.Name}, nil, c.cls, s.sources(), caller(o, c))
	if err != nil {
		// The store says what is wrong with the question.
		return nil, false
	}
	var args []any
	var only []string
	if out.Since != 0 {
		only = append(only, `"created_at" >= `+d.Param("since"))
		args = append(args, sql.Named("since", out.Since))
	}
	if out.Until != 0 {
		only = append(only, `"created_at" < `+d.Param("until"))
		args = append(args, sql.Named("until", out.Until))
	}
	if plan.Where != "" {
		only = append(only, "("+plan.Where+")")
		evalCtx := s.contextOf(o.principal, s.clock(o))
		args = append(append(args, plan.Args...), d.ContextArgs(&evalCtx)...)
	}
	// The moves of a deleted document are not in the copy, so a caller
	// who reads every document of every time is asked no more than the
	// moves.
	of := ""
	if len(only) > 0 {
		of = ` WHERE "id" IN (SELECT "id" FROM ` + quote(c.cls.Name) + ` WHERE ` + strings.Join(only, " AND ") + `)`
	}
	text := `SELECT "p", "n", CAST(sum("n") OVER () AS BIGINT), count(*) OVER () FROM (SELECT "p", count(*) AS "n" FROM (` +
		`SELECT string_agg("to_state", '` + pathStep + `' ORDER BY "version") AS "p" FROM ` + quote(c.cls.Name+copyMoves) + of +
		` GROUP BY "id") GROUP BY "p") ORDER BY "n" DESC, "p" LIMIT ` + strconv.Itoa(limit)
	f, ready := s.copyReady(ctx)
	if !ready {
		return nil, false
	}
	_, rows, err := f.copy.Ask(ctx, text, args...)
	if err != nil {
		f.notAnswered(err)
		return nil, false
	}
	taken := make([]pathTaken, 0, len(rows))
	for _, row := range rows {
		if len(row) != 4 {
			f.notAnswered(errors.New("the copy answered the paths with a row of another shape"))
			return nil, false
		}
		taken = append(taken, pathTaken{path: text1(row[0]), documents: whole(copyCell(row[1])), all: whole(copyCell(row[2])), different: whole(copyCell(row[3]))})
	}
	f.answered()
	return taken, true
}

// text1 reads a text the copy answered, whichever way its driver hands
// one over.
func text1(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return text(v)
}
