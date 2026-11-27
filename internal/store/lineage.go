package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Where a value comes from (ADR-0099, part 2). Lineage answers one value
// of one document and what it is made of, one level: the values of its
// own document its formula read, and for each lookup the documents or
// the rows it read, with their values. Nothing is written for it by any
// commit: a kept value is worked out again, by its own formula, on the
// version that worked it out, with each lookup answered from the history
// as it stood just before that commit; it adds up when the two agree.

// LineageAsk says which value of a document is asked about: of Version,
// the document's current version when 0; and the page of the parts each
// lookup lists, Limit rows, DefaultDocuments when 0 and MaxDocuments at
// the most, after Offset.
type LineageAsk struct {
	Version       int64
	Limit, Offset int
}

// The kinds of a value.
const (
	// LineageTyped is a value somebody gave: an input.
	LineageTyped = "typed"
	// LineageComputed is a value its document works out by itself.
	LineageComputed = "computed"
	// LineageKept is a value worked out at a moment, and kept.
	LineageKept = "kept"
	// LineageLive is a value worked out when it is read, for its reader.
	LineageLive = "live"
)

// Lineage is where one value of one document comes from.
type Lineage struct {
	Class    string `json:"class"`
	ID       string `json:"id"`
	Variable string `json:"variable"`
	// Version is the version of the document the value is of.
	Version int64 `json:"version"`
	Value   any   `json:"value"`
	// Kind is typed, computed, kept or live.
	Kind string `json:"kind"`
	// Formula is the formula as the class writes it; none for a typed
	// value.
	Formula string `json:"formula,omitempty"`
	// Since is the moment the value came: for a kept one the commit that
	// worked it out, for a typed or computed one the version since which
	// it is what it is. A live value has none.
	Since *LineageMoment `json:"since,omitempty"`
	// Variables are the values of the document itself the formula read,
	// as they were at that moment.
	Variables []LineageVariable `json:"variables,omitempty"`
	// Lookups are the documents and the rows each lookup of the formula
	// read, in the order the formula holds them.
	Lookups []LineageLookup `json:"lookups,omitempty"`
	// AddsUp says the formula, worked out again over what it read, gives
	// the value; Rebuilt is what it gives when it does not. Left out for
	// a typed or a live value.
	AddsUp  *bool `json:"adds_up,omitempty"`
	Rebuilt any   `json:"rebuilt,omitempty"`
}

// LineageMoment is a commit that gave a document a value: its version,
// its moment in seconds, who made it, of what kind and for whom, and
// the event: created, set, transition with the transition's name, or
// refresh with the trigger's, table:Rates for a table that changed. For
// a row of a table it is a change of the table's rows (ADR-0099, step
// 3), with no version, and the event is the change's kind: import,
// replace, delete, define, or the action of a rule, table.insert,
// table.update or table.delete.
type LineageMoment struct {
	Version int64  `json:"version,omitempty"`
	At      int64  `json:"at"`
	By      string `json:"by,omitempty"`
	ByKind  string `json:"by_kind,omitempty"`
	For     string `json:"for,omitempty"`
	Event   string `json:"event,omitempty"`
	Name    string `json:"name,omitempty"`
	// change is the number of a change of a table's rows while it is
	// read (rowMoments).
	change int64
}

// LineageVariable is a value of the document itself that a formula read;
// Hidden says the reader may not read it, and Value is then left out.
type LineageVariable struct {
	Variable string `json:"variable"`
	Value    any    `json:"value"`
	Hidden   bool   `json:"hidden,omitempty"`
}

// LineageLookup is what one lookup of a formula read: the documents of
// a class or the rows of a table, of which Count matched, and Value,
// what the lookup gave over them. Parts are the page of them the reader
// may read, the largest value first; Hidden how many the reader may not
// read, with what they add up to for a sum or a count; ChangedSince,
// for a kept value, the parts that have changed since it was worked
// out, and Now what the lookup gives now. TableChangedSince says a
// table's rows changed after the value was worked out. The rows listed
// are as they were at that moment, from the history of the rows; RowsNow
// says they are as they are now, since the table kept no history then
// and has changed since.
type LineageLookup struct {
	Class             string          `json:"class,omitempty"`
	Table             string          `json:"table,omitempty"`
	List              string          `json:"list,omitempty"`
	Aggregate         string          `json:"aggregate,omitempty"`
	Column            string          `json:"column,omitempty"`
	Value             any             `json:"value"`
	Count             int64           `json:"count"`
	Parts             []LineagePart   `json:"parts"`
	More              bool            `json:"more"`
	Hidden            *LineageHidden  `json:"hidden,omitempty"`
	ChangedSince      *LineageChanged `json:"changed_since,omitempty"`
	Now               any             `json:"now,omitempty"`
	TableChangedSince *bool           `json:"table_changed_since,omitempty"`
	RowsNow           bool            `json:"rows_now,omitempty"`
}

// LineagePart is one document a lookup read, at the version it read, or
// one row of a table by its key, with the value the lookup took of it.
// For a row, Since is the change that gave it the values read and Until
// the one that changed or removed it after (ADR-0099, step 3); Since is
// left out for a row older than the history of the rows.
type LineagePart struct {
	ID      string         `json:"id,omitempty"`
	Version int64          `json:"version,omitempty"`
	Key     Values         `json:"key,omitempty"`
	Value   any            `json:"value"`
	Since   *LineageMoment `json:"since,omitempty"`
	Until   *LineageMoment `json:"until,omitempty"`
}

// LineageHidden is how many parts the reader may not read, and for a sum
// or a count what they come to; for any other aggregate it is not told.
type LineageHidden struct {
	Count int64 `json:"count"`
	Value any   `json:"value,omitempty"`
}

// LineageChanged is how many parts have a version after the moment, and
// the page of them the reader may read, the latest change first.
type LineageChanged struct {
	Count int64           `json:"count"`
	Parts []LineageChange `json:"parts"`
}

// LineageChange is one document changed since: its version now, when
// and by whom it last changed.
type LineageChange struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	At      int64  `json:"at"`
	By      string `json:"by,omitempty"`
}

// Lineage answers where a value of a document comes from (ADR-0099,
// part 2), for its caller: one who may read the document and the value.
func (s *Store) Lineage(ctx context.Context, class, id, variable string, ask LineageAsk, opts ...Option) (*Lineage, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	if ask.Limit == 0 {
		ask.Limit = DefaultDocuments
	}
	if ask.Limit < 0 || ask.Limit > MaxDocuments {
		return nil, &Error{Code: CodeBadValue, Path: "/limit", Message: "limit is from 1 to 1,000, got " + strconv.Itoa(ask.Limit), Hint: "ask for fewer parts, and the next ones with offset"}
	}
	if ask.Offset < 0 {
		return nil, &Error{Code: CodeBadValue, Path: "/offset", Message: "offset is how many parts are skipped, 0 or more, got " + strconv.Itoa(ask.Offset), Hint: "leave it out for the first page"}
	}
	if ask.Version < 0 {
		return nil, &Error{Code: CodeBadValue, Path: "/version", Message: "version is 1 or more, got " + strconv.FormatInt(ask.Version, 10), Hint: "leave it out for the version the document is at"}
	}
	r := o.newRead("lineage", c)
	seen, err := s.lookID(ctx, c, id, o, r)
	if err != nil {
		return nil, err
	}
	if !seen {
		return nil, errf(CodeUnknownDocument, "", "no document %s of class %s", id, c.cls.Name)
	}
	var current int64
	if err := s.db.QueryRowContext(ctx, `SELECT "version" FROM `+quote(c.table)+` WHERE "id" = ?`, id).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil, errf(CodeUnknownDocument, "", "no document %s of class %s", id, c.cls.Name)
		}
		return nil, storageErr("read the document", err)
	}
	version := ask.Version
	if version == 0 {
		version = current
	}
	vc, row, err := s.version(ctx, c, id, version)
	if err != nil {
		return nil, err
	}
	cv := vc.byName[strings.ToUpper(variable)]
	if cv == nil {
		return nil, errf(CodeUnknownVariable, variable, "the class %s has no variable %s", c.cls.Name, variable)
	}
	v := cv.v
	out := &Lineage{Class: c.cls.Name, ID: id, Variable: v.Name, Version: version, Variables: []LineageVariable{}, Lookups: []LineageLookup{}}
	if v.Def != nil {
		out.Formula = v.Def.Formula
	}
	doc, err := s.projected(ctx, c, vc, row, o, r)
	if err != nil {
		return nil, err
	}
	switch {
	case v.Live():
		out.Kind = LineageLive
		if version != current {
			return nil, &Error{Code: CodeBadValue, Path: "/version", Message: v.Name + " is a live variable: it is worked out when it is read, and has no past", Hint: "leave version out"}
		}
	case !v.Computed:
		out.Kind = LineageTyped
	case v.Triggered():
		out.Kind = LineageKept
	default:
		out.Kind = LineageComputed
	}
	if out.Kind != LineageLive {
		val, readable := doc.Values[v.Name]
		if !readable {
			return nil, hintf(CodeForbidden, v.Name, "the caller may not read "+v.Name+" of "+c.cls.Name+" "+id, "ask about a value the caller may read")
		}
		out.Value = val
	}

	// The moment the value came, and the version whose values its
	// formula read.
	at, atRow, atDoc := version, row, doc
	event := ""
	switch out.Kind {
	case LineageKept:
		m, worked := doc.Computed[v.Name]
		if !worked || m.Version == 0 {
			break
		}
		at = m.Version
		if at != version {
			if _, atRow, err = s.version(ctx, c, id, at); err != nil {
				return nil, err
			}
			if atDoc, err = s.projected(ctx, c, vc, atRow, o, r); err != nil {
				return nil, err
			}
		}
		if out.Since, err = s.momentOf(ctx, c, id, atRow); err != nil {
			return nil, err
		}
		event = atRow.eventID
	case LineageTyped, LineageComputed:
		sinceRow, err := s.valueSince(ctx, c, id, v.Name, version, doc.Values[v.Name])
		if err != nil {
			return nil, err
		}
		if out.Since, err = s.momentOf(ctx, c, id, sinceRow); err != nil {
			return nil, err
		}
		event = sinceRow.eventID
	}
	if out.Kind == LineageKept && out.Since == nil {
		// Never worked out: it holds nothing it read.
		return out, s.logRead(ctx, o, r)
	}
	if out.Kind == LineageTyped || v.Formula == nil {
		if out.Kind == LineageLive {
			lv, err := s.Live(ctx, class, id, v.Name, opts...)
			if err != nil {
				return nil, err
			}
			out.Value = lv.Value
		}
		return out, s.logRead(ctx, o, r)
	}

	// The values of the document itself the formula read.
	for _, dep := range v.Deps {
		if strings.EqualFold(dep, v.Name) {
			continue
		}
		val, readable := atDoc.Values[dep]
		out.Variables = append(out.Variables, LineageVariable{Variable: dep, Value: val, Hidden: !readable})
	}

	// The formula worked out again over what it read: each lookup is
	// answered by the engine's own statement over the documents as they
	// were just before the commit of the moment (a kept value), or as
	// they are now for the reader (a live one), and lists its parts.
	x := &lineageLookups{s: s, ctx: ctx, o: o, kind: out.Kind, event: event, limit: ask.Limit, offset: ask.Offset}
	fctx := s.lineageContext(o, out, atRow, id)
	ev := s.reader()
	ev.vm.Executor = x
	rebuilt, runErr := ev.run(vc, v.Formula, atRow, &fctx)
	ev.vm.Executor = nil
	s.release(ev)
	if x.err != nil {
		return nil, x.err
	}
	out.Lookups = x.found
	logs := append([]*readLog{r}, x.logs...)
	if out.Kind == LineageLive {
		if runErr != nil {
			return nil, evalErr(v.Name, runErr)
		}
		out.Value = valueOut(rebuilt)
		return out, s.logRead(ctx, o, logs...)
	}
	adds := runErr == nil && reflect.DeepEqual(valueOut(rebuilt), out.Value)
	out.AddsUp = &adds
	if !adds && runErr == nil {
		out.Rebuilt = valueOut(rebuilt)
	}
	return out, s.logRead(ctx, o, logs...)
}

// projected is a version of a document as its caller sees it.
func (s *Store) projected(ctx context.Context, c, vc *class, row *docRow, o options, r *readLog) (*Document, error) {
	h, err := vc.historyRow(ctx, row)
	if err != nil {
		return nil, err
	}
	s.projectRead(c, vc, row, &h.Document, o, r)
	return &h.Document, nil
}

// valueSince finds the version since which a value is what it is at the
// version asked: the earliest of the versions before it, in a row, with
// the same value. Each version is read under the class version that
// wrote it.
func (s *Store) valueSince(ctx context.Context, c *class, id, name string, version int64, value any) (*docRow, error) {
	since := version
	for since > 1 {
		pc, prev, err := s.version(ctx, c, id, since-1)
		if err != nil {
			return nil, err
		}
		h, err := pc.historyRow(ctx, prev)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(h.Values[name], value) {
			break
		}
		since--
	}
	_, row, err := s.version(ctx, c, id, since)
	return row, err
}

// momentOf is the commit that wrote a version of a document: the
// version, when, who, and its event.
func (s *Store) momentOf(ctx context.Context, c *class, id string, row *docRow) (*LineageMoment, error) {
	m := &LineageMoment{Version: row.version, At: row.validFrom, By: row.actor}
	var kind, name, byKind, byFor sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT "kind", "name", "actor_kind", "actor_for" FROM "events" WHERE "event_id" = ?`, row.eventID).Scan(&kind, &name, &byKind, &byFor)
	if err != nil && err != sql.ErrNoRows {
		return nil, storageErr("read the event of "+c.cls.Name+" "+id, err)
	}
	m.Event, m.Name, m.ByKind, m.For = kind.String, name.String, byKind.String, byFor.String
	return m, nil
}

// lineageContext is what the formula ran under at the moment: the clock
// of the commit, its author, the state and the id of the document; a
// live value's is its reader's, now.
func (s *Store) lineageContext(o options, out *Lineage, row *docRow, id string) formula.Context {
	if out.Since == nil {
		f := contextOf(o.principal, s.clock(o))
		f.State, f.ID = row.state, id
		return f
	}
	f := contextOf(Principal{ID: out.Since.By, Kind: out.Since.ByKind, For: out.Since.For}, out.Since.At)
	f.State, f.ID = row.state, id
	return f
}

// lineageLookups is the store as the VM's QueryExecutor for one lineage:
// it answers each lookup of the formula from the documents as they were
// at the moment, and keeps what each read.
type lineageLookups struct {
	s             *Store
	ctx           context.Context
	o             options
	kind          string
	event         string
	limit, offset int
	found         []LineageLookup
	err           error
	// logs are the entries of the log of reads for the parts a content
	// administrator saw past the rules of their classes (ADR-0067).
	logs []*readLog
}

// Lookup implements formula.QueryExecutor.
func (x *lineageLookups) Lookup(q *formula.QuerySlot, args []formula.Value, fctx *formula.Context) (formula.Value, *formula.EvalError) {
	l, v, err := x.s.lineageLookup(x.ctx, q, args, fctx, x)
	if err != nil {
		if x.err == nil {
			x.err = err
		}
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	x.found = append(x.found, l)
	return v, nil
}

// lineageLookup answers one lookup and lists what it read.
func (s *Store) lineageLookup(ctx context.Context, q *formula.QuerySlot, args []formula.Value, fctx *formula.Context, x *lineageLookups) (LineageLookup, formula.Value, error) {
	l := LineageLookup{Aggregate: q.Agg, Column: q.Column, Parts: []LineagePart{}}
	if q.Value != nil && len(q.Value.Slots) == 1 && !strings.HasPrefix(q.Value.Slots[0].Name, "THIS.") {
		l.Column = q.Value.Slots[0].Name
	}
	if q.List != "" {
		// A figure over a list of the document: the list is read with
		// live, a page at a time; lineage names it.
		l.List = q.List
		return l, formula.Blank(), &Error{Code: CodeNotAllowed, Message: "lineage does not yet open a figure over the list " + q.List + ": read the list with live", Hint: "ask live for " + q.List}
	}
	d := formula.NewSQLite().WithLimits(s.limits)
	params := formula.NewParams(d)
	arg := func(i int, currency bool) string {
		if currency {
			return d.Param(fmt.Sprintf("la%dc", i))
		}
		return d.Param(fmt.Sprintf("la%d", i))
	}
	named := []any{}
	money := make([]bool, len(args))
	for _, prog := range []*formula.Program{q.Value, q.Cond} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if i := q.ArgIndex(sl.Name); i >= 0 && sl.Type.Kind == formula.KindMoney {
				money[i] = true
			}
		}
	}
	for i, a := range args {
		named = append(named, sql.Named(fmt.Sprintf("la%d", i), a.DriverValue()))
		if money[i] {
			var cur any
			if a.Kind() == formula.KindMoney {
				cur = a.Currency()
			}
			named = append(named, sql.Named(fmt.Sprintf("la%dc", i), cur))
		}
	}
	named = append(named, formula.NewSQLite().ContextArgs(fctx)...)
	if x.event != "" {
		named = append(named, sql.Named("lineage_event", x.event))
	}

	if t, e := s.table(q.Source); e == nil {
		// A table: its rows as they were just before the commit of the
		// moment, from the history of the rows (ADR-0099, step 3), each
		// with the change that gave it its values and the one after; or,
		// for a live value, as they are now.
		l.Table = t.def.Name
		rel := t.rowsNow
		if x.event != "" {
			var token sql.NullString
			if err := s.db.QueryRowContext(ctx, `SELECT "value" FROM "metadata" WHERE "key" = ?`, rowsKey+t.tableKey()).Scan(&token); err != nil && err != sql.ErrNoRows {
				return l, formula.Value{}, storageErr("read the change of "+t.def.Name, err)
			}
			changed := token.Valid && token.String > x.event
			l.TableChangedSince = &changed
			kept, err := s.historyAt(ctx, t, x.event)
			if err != nil {
				return l, formula.Value{}, err
			}
			if kept {
				rel = t.rowsAsOf
				before, err := s.changeBefore(ctx, x.event)
				if err != nil {
					return l, formula.Value{}, err
				}
				named = append(named, sql.Named("lineage_change", before))
			} else {
				l.RowsNow = changed
			}
		}
		v, err := s.lineageValue(ctx, q, d, arg, rel, params, named)
		if err != nil {
			return l, formula.Value{}, err
		}
		l.Value = valueOut(v)
		keys := append(append([]string{}, t.def.Key...), sinceColumn, untilColumn)
		if err := s.lineageParts(ctx, q, d, arg, rel, params, named, "", keys, x, &l); err != nil {
			return l, formula.Value{}, err
		}
		for i := range l.Parts {
			if err := s.rowMoments(ctx, &l.Parts[i]); err != nil {
				return l, formula.Value{}, err
			}
		}
		return l, v, nil
	}
	src, e := s.class(q.Source)
	if e != nil {
		return l, formula.Value{}, e
	}
	l.Class = src.cls.Name
	// The documents the lookup read: as they were just before the commit
	// of the moment, or, for a live value, as they are for the reader.
	read := query.On{}
	for _, prog := range []*formula.Program{q.Value, q.Cond} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if !strings.HasPrefix(sl.Name, "THIS.") {
				read.Reads = append(read.Reads, sl.Name)
			}
		}
	}
	plan, err := query.CompileOn(Filter{Class: src.cls.Name}, &read, src.cls, s.sources(), caller(x.o, src))
	visible := ""
	if err == nil {
		if plan.Where != "" {
			// The rule's own values are named, so that they are bound
			// wherever the statement holds it (SQLite numbers the values
			// of a statement in the order of its text).
			reader := contextOf(x.o.principal, s.clock(x.o))
			where, args := namedRule(plan.Where, plan.Args, formula.NewSQLite().ContextArgs(&reader))
			visible = `SELECT "id" FROM ` + quote(src.cls.Name) + ` WHERE ` + where
			named = append(named, args...)
		}
	} else {
		visible = `SELECT "id" FROM ` + quote(src.cls.Name) + ` WHERE 0`
	}
	var rel func(string) string
	if x.event == "" {
		// Now, for the reader, as a live lookup reads.
		rel = func(string) string {
			if visible == "" {
				return quote(src.cls.Name)
			}
			return `(SELECT * FROM ` + quote(src.cls.Name) + ` WHERE "id" IN (` + visible + `))`
		}
	} else {
		// Of the documents as they were, only those that can have been
		// read: a document that matched then matches now, or has changed
		// since. The store finds those by its indexes, and reads their
		// versions by their key.
		nowRel, err := s.lineageKeyed(ctx, q, d, arg, params, named, src)
		if err != nil {
			return l, formula.Value{}, err
		}
		nowFrom, _, err := formula.PartsSQL(q, d, arg, nowRel, params)
		if err != nil {
			return l, formula.Value{}, err
		}
		among := `SELECT ` + formula.LookupAlias + `."id"` + nowFrom + ` UNION ` + eventsAfter(src, ">=")
		rel = func(string) string { return asOfAmong(src, among) }
	}
	v, err := s.lineageValue(ctx, q, d, arg, rel, params, named)
	if err != nil {
		return l, formula.Value{}, err
	}
	l.Value = valueOut(v)
	// The parts the reader may read, and how many they may not.
	shown := ""
	if x.event != "" && visible != "" {
		shown = visible
	}
	if err := s.lineageParts(ctx, q, d, arg, rel, params, named, shown, []string{"id", "version"}, x, &l); err != nil {
		return l, formula.Value{}, err
	}
	// A content administrator past the rules: the entry of the parts'
	// class names those only the override showed (ADR-0067).
	if rr := x.o.newRead("lineage", src); rr != nil {
		for _, p := range l.Parts {
			sees, err := s.sees(ctx, src, p.ID, x.o)
			if err != nil {
				return l, formula.Value{}, err
			}
			if !sees {
				rr.saw(p.ID, "/access/read")
			}
		}
		x.logs = append(x.logs, rr)
	}
	if shown != "" {
		hidden := func(string) string {
			return `(SELECT * FROM ` + rel(src.cls.Name) + ` AS h0 WHERE h0."id" NOT IN (` + visible + `))`
		}
		from, _, err := formula.PartsSQL(q, d, arg, hidden, params)
		if err != nil {
			return l, formula.Value{}, err
		}
		var n int64
		if err := s.lineageRow(ctx, `SELECT count(*)`+from, named, &n); err != nil {
			return l, formula.Value{}, err
		}
		if n > 0 {
			l.Hidden = &LineageHidden{Count: n}
			// The page is of the parts the reader may read.
			l.More = int64(x.offset+len(l.Parts)) < l.Count-n
			if q.Agg == "SUM" || q.Agg == "COUNT" {
				hv, err := s.lineageValue(ctx, q, d, arg, hidden, params, named)
				if err != nil {
					return l, formula.Value{}, err
				}
				l.Hidden.Value = valueOut(hv)
			}
		}
	}
	if x.event != "" {
		// What has changed since, and what the lookup gives now, with the
		// engine's eyes, as the kept value would be worked out now.
		nowRel, err := s.lineageKeyed(ctx, q, d, arg, params, named, src)
		if err != nil {
			return l, formula.Value{}, err
		}
		now, err := s.lineageValue(ctx, q, d, arg, nowRel, params, named)
		if err != nil {
			return l, formula.Value{}, err
		}
		l.Now = valueOut(now)
		// A part that changed since has an event after the moment: the
		// documents of those events, of which the ones that matched then
		// or match now.
		after := eventsAfter(src, ">")
		then, _, err := formula.PartsSQL(q, d, arg, func(string) string { return asOfAmong(src, after) }, params)
		if err != nil {
			return l, formula.Value{}, err
		}
		nowFrom, _, err := formula.PartsSQL(q, d, arg, func(string) string {
			return `(SELECT * FROM ` + quote(src.cls.Name) + ` WHERE "id" IN (` + after + `))`
		}, params)
		if err != nil {
			return l, formula.Value{}, err
		}
		a := formula.LookupAlias
		changed := `SELECT p."id", v."version", v."updated_at", v."updated_by", count(*) OVER () FROM (SELECT ` + a + `."id"` + then + ` UNION SELECT ` + a + `."id"` + nowFrom + `) p` +
			` JOIN ` + quote(src.cls.Name) + ` v ON v."id" = p."id"`
		cargs := named
		if visible != "" {
			changed += ` WHERE p."id" IN (` + visible + `)`
		}
		changed += ` ORDER BY v."updated_at" DESC, p."id" LIMIT ` + strconv.Itoa(x.limit)
		st, err := s.filters.acquire(ctx, s.db, changed)
		if err != nil {
			return l, formula.Value{}, storageErr("prepare what changed in "+src.cls.Name, err)
		}
		rows, err := st.stmt.QueryContext(ctx, cargs...)
		if err != nil {
			s.filters.release(st)
			return l, formula.Value{}, storageErr("what changed in "+src.cls.Name, err)
		}
		l.ChangedSince = &LineageChanged{Parts: []LineageChange{}}
		for rows.Next() {
			var ch LineageChange
			var by sql.NullString
			if err := rows.Scan(&ch.ID, &ch.Version, &ch.At, &by, &l.ChangedSince.Count); err != nil {
				rows.Close()
				s.filters.release(st)
				return l, formula.Value{}, storageErr("what changed in "+src.cls.Name, err)
			}
			ch.By = by.String
			l.ChangedSince.Parts = append(l.ChangedSince.Parts, ch)
		}
		rows.Close()
		s.filters.release(st)
	}
	return l, v, nil
}

// namedRule writes a reader's rule so that it may stand anywhere in a
// statement beside a lookup's own condition: its plain values, each a ?
// in its text, become named ones, for SQLite numbers a ? after the named
// values before it; and the values it names about the caller, who reads
// and when, are given names of their own, for the condition of a kept
// value's lookup names the same ones about the commit that worked it
// out. A ? or a name inside a quoted text is left as it is.
func namedRule(where string, args []any, reader []any) (string, []any) {
	var b strings.Builder
	var named []any
	quoteOf := byte(0)
	for i := 0; i < len(where); i++ {
		ch := where[i]
		switch {
		case quoteOf != 0:
			if ch == quoteOf {
				quoteOf = 0
			}
		case ch == '\'' || ch == '"':
			quoteOf = ch
		case ch == '?' && len(named) < len(args):
			name := fmt.Sprintf("lineage_r%d", len(named))
			b.WriteString(":" + name)
			named = append(named, sql.Named(name, args[len(named)]))
			continue
		case ch == ':':
			j := i + 1
			for j < len(where) && (where[j] == '_' || where[j] >= 'a' && where[j] <= 'z' || where[j] >= 'A' && where[j] <= 'Z' || where[j] >= '0' && where[j] <= '9') {
				j++
			}
			if j > i+1 {
				b.WriteString(":lineage_rd_" + where[i+1:j])
				i = j - 1
				continue
			}
		}
		b.WriteByte(ch)
	}
	for _, a := range reader {
		if n, ok := a.(sql.NamedArg); ok {
			named = append(named, sql.Named("lineage_rd_"+n.Name, n.Value))
		}
	}
	return b.String(), named
}

// eventsAfter is the documents of a class with an event after the
// moment the statement binds as :lineage_event, or at it with ">=". The
// events are found by their own key, which is in the order the engine
// wrote them: the unary + keeps SQLite off the index by class, which
// would read every event of the class.
func eventsAfter(src *class, op string) string {
	return `SELECT DISTINCT "doc_id" AS "id" FROM "events" WHERE +"class" = '` + strings.ReplaceAll(src.cls.Name, "'", "''") + `' AND "event_id" ` + op + ` :lineage_event`
}

// lineageKeyed is the documents of a lookup's class as they are now,
// narrowed by the index of the lookup's key column when it has one and
// the key is not blank: the invoices of one customer, where the
// condition alone reads the class. A blank key narrows nothing, so the
// rows are the condition's in every case.
func (s *Store) lineageKeyed(ctx context.Context, q *formula.QuerySlot, d formula.Dialect, arg func(int, bool) string, params *formula.Params, named []any, src *class) (func(string) string, error) {
	whole := func(string) string { return quote(src.cls.Name) }
	key, err := formula.KeySQL(q, d, arg, whole, params)
	if err != nil || key == "" || !query.Indexed(src.cls, q.IndexColumn) {
		return whole, err
	}
	var v any
	st, err := s.filters.acquire(ctx, s.db, `SELECT `+key)
	if err != nil {
		return whole, nil
	}
	defer s.filters.release(st)
	if err := st.stmt.QueryRowContext(ctx, named...).Scan(&v); err != nil || v == nil || v == "" {
		return whole, nil
	}
	if b, ok := v.([]byte); ok && len(b) == 0 {
		return whole, nil
	}
	return func(string) string {
		return `(SELECT * FROM ` + quote(src.cls.Name) + ` WHERE ` + quote(q.IndexColumn) + ` = ` + key + `)`
	}, nil
}

// asOfAmong is the documents of a class among those of the statement
// among, as they were just before the commit whose event the statement
// binds as :lineage_event: of each, the last version written before it,
// unless it was deleted before it. The events are in the order the
// engine wrote them (ADR-0099).
func asOfAmong(src *class, among string) string {
	h := quote(src.cls.Name + "_history")
	// CROSS JOIN keeps SQLite on the candidates first, each read by its
	// key, where it would read the whole history and test each row.
	return `(SELECT h.* FROM (` + among + `) AS c CROSS JOIN ` + h + ` h ON h."id" = c."id" WHERE h."event_id" < :lineage_event` +
		` AND NOT EXISTS (SELECT 1 FROM ` + h + ` n WHERE n."id" = h."id" AND n."version" = h."version" + 1 AND n."event_id" < :lineage_event)` +
		` AND NOT EXISTS (SELECT 1 FROM "events" g WHERE g."class" = '` + strings.ReplaceAll(src.cls.Name, "'", "''") + `' AND g."doc_id" = h."id" AND g."kind" = '` + EventDeleted + `' AND g."event_id" < :lineage_event))`
}

// lineageValue is what a lookup gives over the rows of rel: the
// engine's own statement for it.
func (s *Store) lineageValue(ctx context.Context, q *formula.QuerySlot, d formula.Dialect, arg func(int, bool) string, rel func(string) string, params *formula.Params, args []any) (formula.Value, error) {
	var text string
	var err error
	if q.Agg != "" {
		text, err = formula.AggregateSQL(q, d, arg, rel, params)
	} else {
		var e formula.SQLExpr
		if e, err = formula.LookupSQL(q, d, arg, rel, params); err == nil {
			text = lookupSelect(e)
		}
	}
	if err != nil {
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	var v, c any
	var code sql.NullString
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return formula.Value{}, storageErr("prepare a lookup in "+q.Source, err)
	}
	defer s.filters.release(st)
	if err := st.stmt.QueryRowContext(ctx, args...).Scan(&v, &c, &code); err != nil {
		return formula.Value{}, storageErr("look in "+q.Source, err)
	}
	if code.Valid {
		return formula.Value{}, &formula.EvalError{Code: code.String, Pos: -1, Msg: q.Agg + " over " + q.Source + ": " + code.String}
	}
	return formula.ValueOfType(v, q.Type, c), nil
}

// lineageRow reads one number.
func (s *Store) lineageRow(ctx context.Context, text string, args []any, n *int64) error {
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return storageErr("prepare the parts of a lookup", err)
	}
	defer s.filters.release(st)
	if err := st.stmt.QueryRowContext(ctx, args...).Scan(n); err != nil {
		return storageErr("the parts of a lookup", err)
	}
	return nil
}

// lineageParts lists the page of the rows a lookup read, the largest
// value first, and counts them all: of a class the documents with the
// version read, of a table the rows by their key. shown is the reader's
// documents, when only those are listed; the count is of every row.
func (s *Store) lineageParts(ctx context.Context, q *formula.QuerySlot, d formula.Dialect, arg func(int, bool) string, rel func(string) string, params *formula.Params, named []any, shown string, keys []string, x *lineageLookups, l *LineageLookup) error {
	from, val, err := formula.PartsSQL(q, d, arg, rel, params)
	if err != nil {
		return &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	raw := val.Val
	if val.Kind.IsExact() {
		raw = d.RawMant(val)
	}
	cur := "NULL"
	if val.Cur != "" {
		cur = val.Cur
	}
	cols := make([]string, len(keys))
	for i, k := range keys {
		cols[i] = formula.LookupAlias + "." + quote(k)
	}
	// One statement: every row that matched, with how many there are,
	// whether the reader may read each, and the page of those they may,
	// in the order of the values.
	seen := "1"
	if shown != "" {
		seen = `(` + formula.LookupAlias + `."id" IN (` + shown + `))`
	}
	inner := `SELECT ` + strings.Join(cols, ", ") + `, ` + raw + ` AS "lineage_v", ` + cur + ` AS "lineage_c", ` + seen + ` AS "lineage_seen", count(*) OVER () AS "lineage_n"` + from
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = quote(k)
	}
	text := `SELECT ` + strings.Join(names, ", ") + `, "lineage_v", "lineage_c", "lineage_n" FROM (` + inner + `) WHERE "lineage_seen"` +
		` ORDER BY "lineage_v" DESC, ` + strings.Join(names, ", ") + ` LIMIT ` + strconv.Itoa(x.limit) + ` OFFSET ` + strconv.Itoa(x.offset)
	args := named
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return storageErr("prepare the parts of a lookup in "+q.Source, err)
	}
	defer s.filters.release(st)
	rows, err := st.stmt.QueryContext(ctx, args...)
	if err != nil {
		return storageErr("the parts of a lookup in "+q.Source, err)
	}
	defer rows.Close()
	t := formula.Type{Kind: val.Kind, Scale: val.Scale}
	listed := 0
	for rows.Next() {
		cells := make([]any, len(keys)+3)
		ptrs := make([]any, len(cells))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return storageErr("the parts of a lookup in "+q.Source, err)
		}
		p := LineagePart{Value: valueOut(formula.ValueOfType(cells[len(keys)], t, cells[len(keys)+1]))}
		l.Count = whole(cells[len(keys)+2])
		if l.Class != "" {
			p.ID, p.Version = text1(cells[0]), whole(cells[1])
		} else {
			p.Key = Values{}
			for i, k := range keys {
				switch k {
				case sinceColumn, untilColumn:
					// Kept as the change's number until rowMoments
					// reads it.
					if cells[i] != nil {
						m := &LineageMoment{change: whole(cells[i])}
						if k == sinceColumn {
							p.Since = m
						} else {
							p.Until = m
						}
					}
				default:
					p.Key[k] = text1Or(cells[i])
				}
			}
		}
		l.Parts = append(l.Parts, p)
		listed++
	}
	if err := rows.Err(); err != nil {
		return storageErr("the parts of a lookup in "+q.Source, err)
	}
	if listed == 0 {
		// No row of the page: the count is asked by itself.
		if err := s.lineageRow(ctx, `SELECT count(*)`+from, named, &l.Count); err != nil {
			return err
		}
	}
	l.More = int64(x.offset+listed) < l.Count
	return nil
}

// text1Or is a key's cell as the wire carries it: text as text, a
// number as it came.
func text1Or(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
