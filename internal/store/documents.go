package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// DocumentsAsk names one number of a flow, of a throughput or of the
// paths, whose documents are asked for in place of the numbers, and the
// page of them (ADR-0099, part 1). Exactly one number is named.
type DocumentsAsk struct {
	// Of a flow: Created the documents made in the period; State those
	// in the state now; Stays each stay in the state that ended in the
	// period, AtLeast keeping those that lasted at least the middle stay
	// or the slow tenth; Transition each move along it in the period,
	// From and To narrowing it to the state it left and the state it
	// entered, or naming with no Transition one that has no name.
	Created    bool   `json:"created,omitempty"`
	State      string `json:"state,omitempty"`
	Stays      string `json:"stays,omitempty"`
	AtLeast    string `json:"at_least,omitempty"`
	Transition string `json:"transition,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	// Of a throughput: the first day of one of its periods, as
	// 2026-11-02, whose made or finished documents are asked for.
	Made     string `json:"made,omitempty"`
	Finished string `json:"finished,omitempty"`
	// Of the paths: the states of one path, in their order.
	Path []string `json:"path,omitempty"`
	// Limit is the most rows answered, DefaultDocuments when it is 0
	// and MaxDocuments at the most; Offset how many are skipped first.
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// DefaultDocuments and MaxDocuments are how many rows the documents
// behind a number answer when told nothing, and at the most.
const (
	DefaultDocuments = 50
	MaxDocuments     = 1000
)

// The stays a flow's documents may keep: those that lasted at least the
// middle stay, or the slow tenth (ADR-0095).
const (
	AtLeastMiddle    = "middle"
	AtLeastSlowTenth = "slow_tenth"
)

// Documents are the documents behind one number (ADR-0099): Count is
// the number, counted as the command counts it; Rows the page asked
// for; More says that rows follow the last one.
type Documents struct {
	Count int64         `json:"count"`
	Rows  []DocumentRow `json:"rows"`
	More  bool          `json:"more"`
}

// DocumentRow is one document behind a number, as often as it is in the
// number: a document that moved twice along a transition is two rows.
// ID, Title and State are the document as it is now; At is the moment
// that put it into the number, in seconds: when it moved, entered its
// state, was made or finished; Seconds how long a stay lasted, absent
// where the engine does not know when it began; Version the version
// the move made; By who made the move, or made the document.
type DocumentRow struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	State   string `json:"state,omitempty"`
	At      int64  `json:"at,omitempty"`
	Seconds *int64 `json:"seconds,omitempty"`
	Version int64  `json:"version,omitempty"`
	By      string `json:"by,omitempty"`
}

// The numbers a drill opens.
const (
	drillCreated  = "created"
	drillState    = "state"
	drillStays    = "stays"
	drillMoves    = "moves"
	drillFinished = "finished"
	drillPath     = "path"
)

// drill is one number to open: what it counts, over which period, and
// the page asked for.
type drill struct {
	command       string
	kind          string
	since, until  int64
	name          string // the state, or the transition
	from, to      string // what narrows a transition
	named         bool   // a transition named by Transition, not by From and To alone
	atLeast       string
	path          string
	limit, offset int
	threshold     int64 // the length of stay at_least keeps, once known
}

// FlowDocuments answers the documents behind one number of a flow
// (ADR-0099): the flow is asked as Flow asks it, and ask names the
// number.
func (s *Store) FlowDocuments(ctx context.Context, class string, period FlowPeriod, ask DocumentsAsk, opts ...Option) (*Documents, error) {
	c, o, d, e := s.drillOf(ctx, class, "flow", opts, period.Since, period.Until, ask)
	if e != nil {
		return nil, e
	}
	if err := onlyOf(ask, "a flow", "created", "state", "stays", "at_least", "transition", "from", "to"); err != nil {
		return nil, err
	}
	moves := ask.Transition != "" || ask.From != "" || ask.To != ""
	given := 0
	for _, g := range []bool{ask.Created, ask.State != "", ask.Stays != "", moves} {
		if g {
			given++
		}
	}
	if given != 1 {
		return nil, memberErr("/documents", "documents names one number of the flow, and names "+strconv.Itoa(given), `give one of "created": true, "state", "stays" or "transition"`)
	}
	if ask.AtLeast != "" && ask.Stays == "" {
		return nil, memberErr("/documents/at_least", "at_least keeps some of the stays in a state, and documents names no stays", `give "stays" with it, or leave it out`)
	}
	switch {
	case ask.Created:
		d.kind = drillCreated
	case ask.State != "":
		d.kind, d.name = drillState, ask.State
	case ask.Stays != "":
		d.kind, d.name = drillStays, ask.Stays
		switch ask.AtLeast {
		case "", AtLeastMiddle, AtLeastSlowTenth:
			d.atLeast = ask.AtLeast
		default:
			return nil, memberErr("/documents/at_least", "at_least is middle or slow_tenth, got "+ask.AtLeast, `write "middle" or "slow_tenth"`)
		}
	default:
		d.kind, d.name, d.from, d.to, d.named = drillMoves, ask.Transition, ask.From, ask.To, ask.Transition != ""
	}
	return s.documents(ctx, c, o, d)
}

// ThroughputDocuments answers the documents behind one number of a
// throughput (ADR-0099): those made, or finished, in the period that
// begins on the day ask names, of the length the throughput is asked
// by, and within its since and until.
func (s *Store) ThroughputDocuments(ctx context.Context, class string, t ThroughputAsk, ask DocumentsAsk, opts ...Option) (*Documents, error) {
	c, o, d, e := s.drillOf(ctx, class, "throughput", opts, t.Since, t.Until, ask)
	if e != nil {
		return nil, e
	}
	if err := onlyOf(ask, "throughput", "made", "finished"); err != nil {
		return nil, err
	}
	if (ask.Made != "") == (ask.Finished != "") {
		return nil, memberErr("/documents", "documents names one number of the throughput: the documents made in a period, or those finished in it", `give "made" or "finished", with the first day of the period`)
	}
	by := t.By
	if by == "" {
		by = ThroughputDay
	}
	if by != ThroughputDay && by != ThroughputWeek && by != ThroughputMonth {
		return nil, &Error{Code: CodeBadValue, Path: "/by", Message: "by is day, week or month, got " + by, Hint: `write "day", "week" or "month", or leave it out for days`}
	}
	member, day := "made", ask.Made
	d.kind = drillCreated
	if ask.Finished != "" {
		member, day, d.kind = "finished", ask.Finished, drillFinished
	}
	first, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return nil, memberErr("/documents/"+member, member+" is the first day of a period, as 2026-11-02, got "+day, "write the day as the periods of the throughput write it")
	}
	if !periodOf(first, by).Equal(first) {
		return nil, memberErr("/documents/"+member, day+" does not begin a period of a "+by, "give the first day of the period, as the throughput writes it: "+periodOf(first, by).Format(time.DateOnly))
	}
	// The period is the days of the database's own clock, as the
	// throughput puts its quarters into them, and within its since and
	// until.
	next := nextPeriod(first, by)
	lo := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, s.zone).Unix()
	hi := time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, s.zone).Unix()
	if d.since == 0 || lo > d.since {
		d.since = lo
	}
	if d.until == 0 || hi < d.until {
		d.until = hi
	}
	if d.until <= d.since {
		return &Documents{Rows: []DocumentRow{}}, nil
	}
	return s.documents(ctx, c, o, d)
}

// PathsDocuments answers the documents behind one path (ADR-0099): those
// made in the period the paths are asked over that took the path ask
// names.
func (s *Store) PathsDocuments(ctx context.Context, class string, p PathsAsk, ask DocumentsAsk, opts ...Option) (*Documents, error) {
	c, o, d, e := s.drillOf(ctx, class, "paths", opts, p.Since, p.Until, ask)
	if e != nil {
		return nil, e
	}
	if err := onlyOf(ask, "the paths", "path"); err != nil {
		return nil, err
	}
	if len(ask.Path) == 0 {
		return nil, memberErr("/documents/path", "documents names a path of the paths, and names none", `give the states of the path in their order, as "path": ["draft", "review"]`)
	}
	for i, st := range ask.Path {
		if st == "" || strings.Contains(st, pathStep) {
			return nil, memberErr("/documents/path/"+strconv.Itoa(i), "a state of a path is the name of a state, got "+strconv.Quote(st), "give the states as the paths name them")
		}
	}
	d.kind, d.path = drillPath, strings.Join(ask.Path, pathStep)
	return s.documents(ctx, c, o, d)
}

// memberErr refuses a member of documents, by its place in the
// command's object.
func memberErr(path, message, hint string) *Error {
	return &Error{Code: CodeBadValue, Path: path, Message: message, Hint: hint}
}

// onlyOf refuses a member of documents that opens a number of another
// command than the one asked.
func onlyOf(ask DocumentsAsk, of string, mine ...string) *Error {
	members := []struct {
		name  string
		given bool
	}{
		{"created", ask.Created}, {"state", ask.State != ""}, {"stays", ask.Stays != ""}, {"at_least", ask.AtLeast != ""},
		{"transition", ask.Transition != ""}, {"from", ask.From != ""}, {"to", ask.To != ""},
		{"made", ask.Made != ""}, {"finished", ask.Finished != ""}, {"path", len(ask.Path) > 0},
	}
	for _, m := range members {
		if m.given && !slices.Contains(mine, m.name) {
			return memberErr("/documents/"+m.name, m.name+" is not a number of "+of, "documents of "+of+" names "+strings.Join(mine, " or "))
		}
	}
	return nil
}

// drillOf checks what every opening of a number checks: the class has
// states, the period ends after it begins, and the page is one the
// engine answers.
func (s *Store) drillOf(ctx context.Context, class, command string, opts []Option, since, until time.Time, ask DocumentsAsk) (*class, options, drill, error) {
	d := drill{command: command, limit: ask.Limit, offset: ask.Offset}
	c, e := s.class(class)
	if e != nil {
		return nil, options{}, d, e
	}
	if c.cls.States == nil {
		return nil, options{}, d, hintf(CodeStateless, "", c.cls.Name+" declares no states: it is a stateless application and has no flow", "add a states block with an initial state")
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, options{}, d, e
	}
	if !since.IsZero() && !until.IsZero() && !until.After(since) {
		return nil, options{}, d, hintf(CodeBadValue, "", "the period ends before it begins", "give an until that is after since, or leave one out")
	}
	if !since.IsZero() {
		d.since = since.Unix()
	}
	if !until.IsZero() {
		d.until = until.Unix()
	}
	switch {
	case d.limit == 0:
		d.limit = DefaultDocuments
	case d.limit < 0 || d.limit > MaxDocuments:
		return nil, options{}, d, memberErr("/documents/limit", "limit is from 1 to 1,000, got "+strconv.Itoa(d.limit), "ask for fewer rows, and the next ones with offset")
	}
	if d.offset < 0 {
		return nil, options{}, d, memberErr("/documents/offset", "offset is how many rows are skipped, 0 or more, got "+strconv.Itoa(d.offset), "leave it out for the first page")
	}
	return c, o, d, nil
}

// documents opens one number: the copy answers it when the program
// keeps one, the store otherwise, and the answer is the same.
func (s *Store) documents(ctx context.Context, c *class, o options, d drill) (*Documents, error) {
	if d.atLeast != "" {
		stays, err := s.spreadOf(ctx, c, o, d)
		if err != nil {
			return nil, err
		}
		for _, st := range stays {
			if st.state == d.name {
				d.threshold = st.slowTenth
				if d.atLeast == AtLeastMiddle {
					d.threshold = st.middle
				}
			}
		}
	}
	out, asked := s.documentsFromCopy(ctx, c, o, d)
	if !asked {
		var err error
		if out, err = s.documentsFromStore(ctx, c, o, d); err != nil {
			return nil, err
		}
	}
	if out.Count == 0 {
		if e := unknownPart(c, d); e != nil {
			return nil, e
		}
	}
	out.More = int64(d.offset+len(out.Rows)) < out.Count
	// A content administrator past the rules: the entry names the
	// documents of the page only the override showed (ADR-0067).
	if r := o.newRead(d.command, c); r != nil {
		for _, row := range out.Rows {
			sees, err := s.sees(ctx, c, row.ID, o)
			if err != nil {
				return nil, err
			}
			if !sees {
				r.saw(row.ID, "/access/read")
			}
		}
		if err := s.logRead(ctx, o, r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// unknownPart refuses a state or a transition that neither the class
// has nor any document of it ever went through: a number nobody can
// have read. One the class no longer has, with documents that went
// through it, is answered as any other.
func unknownPart(c *class, d drill) *Error {
	states := c.cls.States.List
	known := func(name string) bool { return slices.Contains(states, name) }
	switch d.kind {
	case drillState, drillStays:
		if !known(d.name) {
			return memberErr("/documents/"+d.kind, c.cls.Name+" has no state "+d.name+", and no document of it was ever in one", "the states are "+strings.Join(states, ", "))
		}
	case drillMoves:
		var names []string
		for _, t := range c.cls.Transitions {
			if t.Name != "" && !slices.Contains(names, t.Name) {
				names = append(names, t.Name)
			}
		}
		if d.named && !slices.Contains(names, d.name) {
			return memberErr("/documents/transition", c.cls.Name+" has no transition "+d.name+", and no document of it ever moved along one", "the transitions are "+strings.Join(names, ", "))
		}
		for _, end := range []struct{ member, state string }{{"from", d.from}, {"to", d.to}} {
			if end.state != "" && !known(end.state) {
				return memberErr("/documents/"+end.member, c.cls.Name+" has no state "+end.state+", and no document of it was ever in one", "the states are "+strings.Join(states, ", "))
			}
		}
	}
	return nil
}

// spreadOf answers how the stays of each state spread in the period, as
// the flow answers it, so that at_least keeps the stays the flow's
// middle and slow tenth speak of.
func (s *Store) spreadOf(ctx context.Context, c *class, o options, d drill) ([]flowStay, error) {
	flow := &Flow{Since: d.since, Until: d.until}
	if stays, asked := s.spreadFromCopy(ctx, c, o, flow); asked {
		return stays, nil
	}
	return s.spreadFromStore(ctx, c, o, flow)
}

// spreadFromCopy is how the stays spread, read from the copy; false when
// the store has to answer.
func (s *Store) spreadFromCopy(ctx context.Context, c *class, o options, flow *Flow) ([]flowStay, bool) {
	if s.feeder == nil {
		return nil, false
	}
	d := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(d, Filter{Class: c.cls.Name}, nil, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, false
	}
	where := ""
	if plan.Where != "" {
		where = " WHERE " + plan.Where
	}
	var args []any
	text := flowSpreadKept(c, flow, where, func(name string, v any) string {
		args = append(args, sql.Named(name, v))
		return d.Param(name)
	})
	if where != "" {
		evalCtx := contextOf(o.principal, s.clock(o))
		args = append(append(args, plan.Args...), d.ContextArgs(&evalCtx)...)
	}
	f, ready := s.copyReady(ctx)
	if !ready {
		return nil, false
	}
	_, rows, err := f.copy.Ask(ctx, text, args...)
	if err != nil {
		f.notAnswered(err)
		return nil, false
	}
	stays := make([]flowStay, 0, len(rows))
	for _, row := range rows {
		if len(row) != 3 {
			f.notAnswered(errors.New("the copy answered the stays with a row of another shape"))
			return nil, false
		}
		stays = append(stays, flowStay{state: text1(row[0]), middle: whole(copyCell(row[1])), slowTenth: whole(copyCell(row[2]))})
	}
	f.answered()
	return stays, true
}

// spreadFromStore is how the stays spread, read from the store: the
// third statement of a flow (ADR-0095).
func (s *Store) spreadFromStore(ctx context.Context, c *class, o options, flow *Flow) ([]flowStay, error) {
	plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, filterErr(err)
	}
	where := ""
	if plan.Where != "" {
		where = " WHERE " + plan.Where
	}
	var args []any
	text := flowSpreadSQL(c, flow, where, c.history, func(_ string, v any) string {
		args = append(args, v)
		return "?"
	})
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, storageErr("prepare the stays of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	rows, err := st.stmt.QueryContext(ctx, append(append(args, plan.Args...), s.contextArgs(o)...)...)
	if err != nil {
		return nil, storageErr("the stays of "+c.cls.Name, err)
	}
	defer rows.Close()
	var stays []flowStay
	for rows.Next() {
		var state sql.NullString
		var middle, slow sql.NullInt64
		if err := rows.Scan(&state, &middle, &slow); err != nil {
			return nil, storageErr("the stays of "+c.cls.Name, err)
		}
		stays = append(stays, flowStay{state: state.String, middle: middle.Int64, slowTenth: slow.Int64})
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("the stays of "+c.cls.Name, err)
	}
	return stays, nil
}

// drillOrder is the order of the rows of a number, over the columns of
// the statement's rows under the prefix given.
func drillOrder(d drill, p string) string {
	switch d.kind {
	case drillState:
		return p + `"at" ASC NULLS LAST, ` + p + `"id"`
	case drillStays:
		return p + `"secs" DESC NULLS LAST, ` + p + `"at" DESC, ` + p + `"id", ` + p + `"ver"`
	}
	return p + `"at" DESC, ` + p + `"id", ` + p + `"ver"`
}

// drillRows writes the rows of a number: a statement whose rows are
// "id", "at", "secs", "ver" and "by", over the events and the versions
// (store) or over the moves the copy keeps (copy). bind takes a value
// and answers what stands for it; rule is " WHERE ..." of the reader's
// rule, or nothing, and ruleArgs binds its values where it stands.
func drillRows(c *class, d drill, copy bool, rule string, bind func(name string, v any) string, ruleArgs func()) string {
	view := quote(c.cls.Name)
	whole, txt := "NULL", "NULL"
	if copy {
		whole, txt = "CAST(NULL AS BIGINT)", "CAST(NULL AS VARCHAR)"
	}
	period := func(col string) string {
		out := ""
		if d.since != 0 {
			out += " AND " + col + " >= " + bind("drill_since", d.since)
		}
		if d.until != 0 {
			out += " AND " + col + " < " + bind("drill_until", d.until)
		}
		return out
	}
	// The reader's documents: in the store always, for a deleted
	// document's events stay; in the copy only for a reader whose rule
	// leaves some out, for the copy keeps no moves of a deleted one.
	reader := func(col string) string {
		if copy && rule == "" {
			return ""
		}
		out := " AND " + col + ` IN (SELECT "id" FROM ` + view + rule + ")"
		ruleArgs()
		return out
	}
	finals, anyFinal := finalStates(c)
	if !anyFinal {
		finals = "NULL"
	}
	if d.kind == drillState {
		text := `SELECT "id", "state_entered_at" AS "at", ` + whole + ` AS "secs", "version" AS "ver", "state_entered_by" AS "by" FROM ` + view +
			` WHERE "state" = ` + bind("drill_name", d.name)
		if rule != "" {
			text += " AND (" + strings.TrimPrefix(rule, " WHERE ") + ")"
			ruleArgs()
		}
		return text
	}
	if d.kind == drillPath {
		if copy {
			var made []string
			if d.since != 0 {
				made = append(made, `"created_at" >= `+bind("drill_since", d.since))
			}
			if d.until != 0 {
				made = append(made, `"created_at" < `+bind("drill_until", d.until))
			}
			if rule != "" {
				made = append(made, "("+strings.TrimPrefix(rule, " WHERE ")+")")
				ruleArgs()
			}
			ways := `SELECT "id", string_agg("to_state", '` + pathStep + `' ORDER BY "version") AS "p" FROM ` + quote(c.cls.Name+copyMoves)
			if len(made) > 0 {
				ways += ` WHERE "id" IN (SELECT "id" FROM ` + view + ` WHERE ` + strings.Join(made, " AND ") + ")"
			}
			ways += ` GROUP BY "id"`
			return `SELECT w."id", m."created_at" AS "at", ` + whole + ` AS "secs", ` + whole + ` AS "ver", m."created_by" AS "by" FROM (` + ways + `) w JOIN ` + view +
				` m ON m."id" = w."id" WHERE w."p" = ` + bind("drill_path", d.path)
		}
		// In the store the reader's rule stands last: SQLite numbers the
		// values of a statement in the order the text holds them, and
		// the rule names its values about the caller.
		ways := `SELECT e."doc_id" AS "id", group_concat(e."to_state", '` + pathStep + `' ORDER BY e."after_version") AS "p" FROM "events" e` +
			` WHERE e."class" = ` + bind("drill_class", c.cls.Name) + ` AND e."kind" IN ('` + EventCreated + `', '` + EventTransition + `') GROUP BY e."doc_id"`
		text := `SELECT w."id", m."created_at" AS "at", ` + whole + ` AS "secs", ` + whole + ` AS "ver", m."created_by" AS "by" FROM (` + ways + `) w JOIN ` + view +
			` m ON m."id" = w."id" WHERE w."p" = ` + bind("drill_path", d.path)
		if d.since != 0 {
			text += ` AND m."created_at" >= ` + bind("drill_since", d.since)
		}
		if d.until != 0 {
			text += ` AND m."created_at" < ` + bind("drill_until", d.until)
		}
		if rule != "" {
			text += ` AND w."id" IN (SELECT "id" FROM ` + view + rule + ")"
			ruleArgs()
		}
		return text
	}
	if copy {
		moves := quote(c.cls.Name + copyMoves)
		secs := whole
		var only string
		switch d.kind {
		case drillCreated:
			only = `"kind" = '` + EventCreated + `'`
		case drillFinished:
			only = `"kind" IN ('` + EventCreated + `', '` + EventTransition + `') AND "to_state" IN (` + finals + `)`
		case drillStays:
			only = `"kind" = '` + EventTransition + `' AND "from_state" = ` + bind("drill_name", d.name)
			secs = `CASE WHEN "left_since" IS NULL THEN NULL ELSE "ts" - "left_since" END`
			if d.atLeast != "" {
				only += ` AND "left_since" IS NOT NULL AND "ts" - "left_since" >= ` + bind("drill_least", d.threshold)
			}
		case drillMoves:
			only = `"kind" = '` + EventTransition + `' AND coalesce("name", '') = ` + bind("drill_name", d.name)
			if d.from != "" {
				only += ` AND "from_state" = ` + bind("drill_from", d.from)
			}
			if d.to != "" {
				only += ` AND "to_state" = ` + bind("drill_to", d.to)
			}
		}
		return `SELECT "id", "ts" AS "at", ` + secs + ` AS "secs", "version" AS "ver", ` + txt + ` AS "by" FROM ` + moves +
			` WHERE ` + only + period(`"ts"`) + reader(`"id"`)
	}
	from := `FROM "events" e`
	secs := whole
	only := `e."class" = ` + bind("drill_class", c.cls.Name)
	switch d.kind {
	case drillCreated:
		only += ` AND e."kind" = '` + EventCreated + `'`
	case drillFinished:
		only += ` AND e."kind" IN ('` + EventCreated + `', '` + EventTransition + `') AND e."to_state" IN (` + finals + `)`
	case drillStays:
		from += ` LEFT JOIN ` + quote(c.history) + ` h ON h."id" = e."doc_id" AND h."version" = e."before_version"`
		secs = `e."ts" - h."state_entered_at"`
		only += ` AND e."kind" = '` + EventTransition + `' AND e."from_state" = ` + bind("drill_name", d.name)
		if d.atLeast != "" {
			only += ` AND h."state_entered_at" IS NOT NULL AND e."ts" - h."state_entered_at" >= ` + bind("drill_least", d.threshold)
		}
	case drillMoves:
		only += ` AND e."kind" = '` + EventTransition + `' AND coalesce(e."name", '') = ` + bind("drill_name", d.name)
		if d.from != "" {
			only += ` AND e."from_state" = ` + bind("drill_from", d.from)
		}
		if d.to != "" {
			only += ` AND e."to_state" = ` + bind("drill_to", d.to)
		}
	}
	return `SELECT e."doc_id" AS "id", e."ts" AS "at", ` + secs + ` AS "secs", e."after_version" AS "ver", e."actor_id" AS "by" ` + from +
		` WHERE ` + only + period(`e."ts"`) + reader(`e."doc_id"`)
}

// drillPage wraps the rows of a number into the page asked for: how
// many there are in all, the page in its order, and each document's
// title and state now. In the copy the moves do not keep who made
// them, so the page reads that from the events, for its own rows.
func drillPage(c *class, d drill, rows string, copy bool, bind func(name string, v any) string) string {
	view := quote(c.cls.Name)
	page := `SELECT m."id", m."at", m."secs", m."ver", m."by", count(*) OVER () AS "n" FROM (` + rows + `) m ORDER BY ` + drillOrder(d, `m.`) +
		` LIMIT ` + strconv.Itoa(d.limit) + ` OFFSET ` + strconv.Itoa(d.offset)
	by := `p."by"`
	from := ` FROM (` + page + `) p LEFT JOIN ` + view + ` v ON v."id" = p."id"`
	if copy {
		by = `coalesce(p."by", e."actor_id")`
		from += ` LEFT JOIN "` + copyEvents + `" e ON e."class" = ` + bind("drill_page_class", c.cls.Name) + ` AND e."doc_id" = p."id" AND e."after_version" = p."ver"`
	}
	return `SELECT p."id", v."title", v."state", p."at", p."secs", p."ver", ` + by + `, p."n"` + from + ` ORDER BY ` + drillOrder(d, `p.`)
}

// documentsFromStore opens a number over the events and the versions of
// the store.
func (s *Store) documentsFromStore(ctx context.Context, c *class, o options, d drill) (*Documents, error) {
	plan, err := query.Compile(Filter{Class: c.cls.Name}, c.cls, s.sources(), caller(o, c))
	if err != nil {
		return nil, filterErr(err)
	}
	rule := ""
	if plan.Where != "" {
		rule = " WHERE " + plan.Where
	}
	// The values in the order the text holds them, and the rule's named
	// values about the caller after all of them: SQLite counts a plain
	// value by its place among all.
	var args []any
	bind := func(_ string, v any) string {
		args = append(args, v)
		return "?"
	}
	ruleArgs := func() { args = append(args, plan.Args...) }
	named := s.contextArgs(o)
	rows := drillRows(c, d, false, rule, bind, ruleArgs)
	text := drillPage(c, d, rows, false, bind)
	args = append(args, named...)
	out, err := s.drillAsk(ctx, text, args, func(text string, args []any) ([][]any, error) {
		st, err := s.filters.acquire(ctx, s.db, text)
		if err != nil {
			return nil, storageErr("prepare the documents of "+c.cls.Name, err)
		}
		defer s.filters.release(st)
		r, err := st.stmt.QueryContext(ctx, args...)
		if err != nil {
			return nil, storageErr("the documents of "+c.cls.Name, err)
		}
		defer r.Close()
		var got [][]any
		for r.Next() {
			cells := make([]any, 8)
			ptrs := make([]any, 8)
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				return nil, storageErr("the documents of "+c.cls.Name, err)
			}
			got = append(got, cells)
		}
		if err := r.Err(); err != nil {
			return nil, storageErr("the documents of "+c.cls.Name, err)
		}
		return got, nil
	})
	if err != nil {
		return nil, err
	}
	if len(out.Rows) == 0 && d.offset > 0 {
		args = args[:0]
		rows := drillRows(c, d, false, rule, bind, ruleArgs)
		args = append(args, named...)
		st, err := s.filters.acquire(ctx, s.db, `SELECT count(*) FROM (`+rows+`)`)
		if err != nil {
			return nil, storageErr("prepare the documents of "+c.cls.Name, err)
		}
		defer s.filters.release(st)
		if err := st.stmt.QueryRowContext(ctx, args...).Scan(&out.Count); err != nil {
			return nil, storageErr("the documents of "+c.cls.Name, err)
		}
	}
	return out, nil
}

// documentsFromCopy opens a number over the moves the copy keeps
// (ADR-0089, item 2), after the copy was brought up to the database. It
// answers false when the store has to answer.
func (s *Store) documentsFromCopy(ctx context.Context, c *class, o options, d drill) (*Documents, bool) {
	if s.feeder == nil {
		return nil, false
	}
	dd := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(dd, Filter{Class: c.cls.Name}, nil, c.cls, s.sources(), caller(o, c))
	if err != nil {
		// The store says what is wrong with the question.
		return nil, false
	}
	rule := ""
	if plan.Where != "" {
		rule = " WHERE " + plan.Where
	}
	var args []any
	bound := map[string]bool{}
	bind := func(name string, v any) string {
		if !bound[name] {
			bound[name] = true
			args = append(args, sql.Named(name, v))
		}
		return dd.Param(name)
	}
	ruled := false
	ruleArgs := func() {
		if !ruled {
			ruled = true
			evalCtx := contextOf(o.principal, s.clock(o))
			args = append(append(args, plan.Args...), dd.ContextArgs(&evalCtx)...)
		}
	}
	rows := drillRows(c, d, true, rule, bind, ruleArgs)
	text := drillPage(c, d, rows, true, bind)
	f, ready := s.copyReady(ctx)
	if !ready {
		return nil, false
	}
	ask := func(text string, args []any) ([][]any, error) {
		_, got, err := f.copy.Ask(ctx, text, args...)
		return got, err
	}
	out, err := s.drillAsk(ctx, text, args, ask)
	if err == nil && len(out.Rows) == 0 && d.offset > 0 {
		var got [][]any
		if got, err = ask(`SELECT count(*) FROM (`+rows+`)`, args); err == nil {
			if len(got) == 1 && len(got[0]) == 1 {
				out.Count = whole(copyCell(got[0][0]))
			} else {
				err = errors.New("the copy answered a count with a row of another shape")
			}
		}
	}
	if err != nil {
		f.notAnswered(err)
		return nil, false
	}
	f.answered()
	return out, true
}

// drillAsk asks the page of a number and reads it into Documents: the
// same eight columns from either engine.
func (s *Store) drillAsk(ctx context.Context, text string, args []any, ask func(string, []any) ([][]any, error)) (*Documents, error) {
	got, err := ask(text, args)
	if err != nil {
		return nil, err
	}
	out := &Documents{Rows: make([]DocumentRow, 0, len(got))}
	for _, row := range got {
		if len(row) != 8 {
			return nil, errors.New("the documents of a number came as a row of another shape")
		}
		r := DocumentRow{ID: text1(row[0]), Title: text1(row[1]), State: text1(row[2]), At: whole(copyCell(row[3])), Version: whole(copyCell(row[5])), By: text1(row[6])}
		if row[4] != nil {
			n := whole(copyCell(row[4]))
			r.Seconds = &n
		}
		out.Count = whole(copyCell(row[7]))
		out.Rows = append(out.Rows, r)
	}
	return out, nil
}
