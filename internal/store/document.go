package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

type scanner interface {
	Scan(dest ...any) error
}

// scanRow reads a current row, or a history row with its four extra
// columns, as the statements of prepare select them.
func (c *class) scanRow(sc scanner, history bool) (*docRow, error) {
	return c.scan(sc, history, false)
}

// scan reads a row. lean says the statement left out who made the
// document and where it came from, the last four system columns
// (ADR-0047, item 4): the row then has them blank.
func (c *class) scan(sc scanner, history, lean bool) (*docRow, error) {
	sys := len(systemColumns)
	if lean {
		sys -= len(whoColumns)
	}
	n := sys + len(c.cls.Columns) + len(c.triggered)
	if history {
		n += len(historyColumns)
	}
	raw := make([]any, n)
	dest := make([]any, n)
	for i := range raw {
		dest[i] = &raw[i]
	}
	return c.scanInto(sc, history, lean, sys, raw, dest)
}

// scanW reads a current row through a statement of the writer. The
// writer reads one row at a time, so it keeps the two lists a scan needs
// between rows instead of making them for each.
func (c *class) scanW(sc scanner, lean bool) (*docRow, error) {
	sys := len(systemColumns)
	if lean {
		sys -= len(whoColumns)
	}
	n := sys + len(c.cls.Columns) + len(c.triggered)
	if len(c.wraw) < n {
		c.wraw, c.wdest = make([]any, n), make([]any, n)
		for i := range c.wraw {
			c.wdest[i] = &c.wraw[i]
		}
	}
	return c.scanInto(sc, false, lean, sys, c.wraw[:n], c.wdest[:n])
}

// scanInto scans a row into raw through dest, whose elements point at
// raw's, and reads the row out of it.
func (c *class) scanInto(sc scanner, history, lean bool, sys int, raw, dest []any) (*docRow, error) {
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	r := &docRow{id: toString(raw[0]), title: toString(raw[1]), state: toString(raw[2]), version: toInt64(raw[3]), classVersion: int(toInt64(raw[4])),
		life: formula.Life{Version: toInt64(raw[3]), CreatedAt: toInt64(raw[5]), UpdatedAt: toInt64(raw[6]), EnteredAt: toInt64(raw[7])}}
	r.logged = r.version
	if !lean {
		r.life.CreatedBy, r.life.UpdatedBy, r.life.EnteredBy, r.life.EnteredFrom = toString(raw[8]), toString(raw[9]), toString(raw[10]), toString(raw[11])
	}
	rest := raw[sys:]
	if history {
		r.validFrom, r.validTo, r.actor, r.eventID = toInt64(rest[0]), toInt64(rest[1]), toString(rest[2]), toString(rest[3])
		rest = rest[len(historyColumns):]
	}
	r.vals = make([]formula.Value, len(c.leaves))
	for i, l := range c.leaves {
		r.vals[i], rest = decode(l, rest)
	}
	if len(c.triggered) > 0 {
		r.ats = make([]int64, len(c.triggered))
		for i := range r.ats {
			r.ats[i] = toInt64(rest[i])
		}
	}
	return r, nil
}

// load reads the rows of one version of a composite with the statement,
// the writer's or the pool's.
func (ch *child) load(ctx context.Context, st *sql.Stmt, id string, version int64) ([]childRow, error) {
	rows, err := st.QueryContext(ctx, id, version)
	if err != nil {
		return nil, storageErr("read "+ch.v.Name, err)
	}
	defer rows.Close()
	n := len(ch.def.Columns)
	raw := make([]any, n)
	dest := make([]any, n)
	for i := range raw {
		dest[i] = &raw[i]
	}
	var out []childRow
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, storageErr("read "+ch.v.Name, err)
		}
		row := make(childRow, len(ch.leaves))
		rest := raw
		for i, l := range ch.leaves {
			row[i], rest = decode(l, rest)
		}
		out = append(out, row)
	}
	return out, storageErr("read "+ch.v.Name, rows.Err())
}

// loadChildren completes have with the rows of every other composite at
// the version, on the writer's connection or the pool's.
func (c *class) loadChildren(ctx context.Context, id string, version int64, have map[*child][]childRow, writer bool) (map[*child][]childRow, error) {
	if have == nil {
		have = map[*child][]childRow{}
	}
	for _, ch := range c.children {
		if _, ok := have[ch]; ok {
			continue
		}
		st := ch.readRows
		if writer {
			st = ch.selectRows
		}
		rows, err := ch.load(ctx, st, id, version)
		if err != nil {
			return nil, err
		}
		have[ch] = rows
	}
	return have, nil
}

// document renders a row and its composites in the wire shape.
func (c *class) document(r *docRow, kids map[*child][]childRow) *Document {
	d := &Document{Class: c.cls.Name, ID: r.id, Title: r.title, State: r.state, Version: r.version, ClassVersion: r.classVersion,
		CreatedAt: r.life.CreatedAt, UpdatedAt: r.life.UpdatedAt, StateEnteredAt: r.life.EnteredAt,
		CreatedBy: r.life.CreatedBy, UpdatedBy: r.life.UpdatedBy, StateEnteredBy: r.life.EnteredBy, StateEnteredFrom: r.life.EnteredFrom,
		Values: make(Values, len(c.cls.Variables))}
	if d.Title == "" {
		d.Title = r.id
	}
	for _, cv := range c.vars {
		v := cv.v
		switch v.Type.Kind {
		case formula.KindRows, formula.KindArray:
			d.Values[v.Name] = cv.child.out(kids[cv.child])
		case formula.KindObject:
			m, any := getFields(r.vals, c.leafIndex, v.Name, v.Type.Fields())
			if any {
				d.Values[v.Name] = m
			} else {
				d.Values[v.Name] = nil
			}
		default:
			d.Values[v.Name] = valueOut(r.vals[cv.leaf])
		}
	}
	// The moment of every triggered variable that has one (ADR-0043).
	for i, cv := range c.triggered {
		asof, failed := r.vals[cv.asof], r.vals[cv.errLeaf]
		if asof.IsBlank() && failed.IsBlank() {
			continue
		}
		if d.Computed == nil {
			d.Computed = make(map[string]Moment, len(c.triggered))
		}
		m := Moment{Error: failed.Str()}
		if !asof.IsBlank() {
			m.Version, m.At = asof.Mant(), r.ats[i]
		}
		d.Computed[cv.v.Name] = m
	}
	return d
}

// out renders the rows of a composite: a list of maps, or of scalars for
// an array; an empty list when there are none.
func (ch *child) out(rows []childRow) []any {
	out := make([]any, len(rows))
	for i, row := range rows {
		if ch.v.Type.Kind == formula.KindArray {
			out[i] = valueOut(row[0])
			continue
		}
		m, _ := getFields(row, ch.leafIndex, "", ch.fields)
		out[i] = m
	}
	return out
}

// getFields renders the fields below prefix as a map and says whether any
// leaf is set, so that a blank object renders as null (ADR-0018).
func getFields(src []formula.Value, index map[string]int, prefix string, fields []formula.Field) (map[string]any, bool) {
	m := make(map[string]any, len(fields))
	any := false
	for _, f := range fields {
		path := f.Name
		if prefix != "" {
			path = prefix + "." + f.Name
		}
		if f.Type.Kind == formula.KindObject {
			sub, set := getFields(src, index, path, f.Type.Fields())
			if set {
				m[f.Name] = sub
				any = true
			} else {
				m[f.Name] = nil
			}
			continue
		}
		v := src[index[strings.ToUpper(path)]]
		if !v.IsBlank() {
			any = true
		}
		m[f.Name] = valueOut(v)
	}
	return m, any
}

// valueOut renders a scalar in the wire shape: nil, bool, float64, text
// for exact values, dates, texts and refs, int64 seconds for a datetime,
// json.RawMessage for a json value.
func valueOut(v formula.Value) any {
	switch v.Kind() {
	case formula.KindNull:
		return nil
	case formula.KindBool:
		return v.Truth()
	case formula.KindNumber:
		return v.Num()
	case formula.KindDateTime:
		return v.Secs()
	case formula.KindJSON:
		return json.RawMessage(v.Str())
	case formula.KindText, formula.KindRef:
		return v.Str()
	}
	return formula.Render(v)
}

// Get reads the current version of a document, projected for the
// principal (ADR-0041).
func (s *Store) Get(ctx context.Context, class, id string, opts ...Option) (*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	row, err := c.scanRow(c.r.getCurrent.QueryRowContext(ctx, id), false)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, noDocument(c, id)
		}
		return nil, storageErr("read the document", err)
	}
	vc, row, err := s.underVersion(ctx, c, row, false)
	if err != nil {
		return nil, err
	}
	// A document the caller may not see does not exist for them
	// (ADR-0049, item 3).
	if !s.seesRow(c, vc, row, o) {
		return nil, noDocument(c, id)
	}
	kids, err := vc.loadChildren(ctx, id, row.version, nil, false)
	if err != nil {
		return nil, err
	}
	d := vc.document(row, kids)
	s.projectRead(c, vc, row, d, o)
	return d, nil
}

// projectRead projects a document on a read path with a borrowed
// evaluator (ADR-0041), at the call's clock (ADR-0042).
func (s *Store) projectRead(c, vc *class, row *docRow, d *Document, o options) {
	if c.rights == nil || o.principal.Admin {
		return
	}
	ev := s.reader()
	s.project(ev, c, vc, row, d, o.principal, s.clock(o))
	s.release(ev)
}

// underVersion returns the class that computed a row and the row read
// under that class's layout, which is the row itself when the class is
// the current one (ADR-0039). A document is read under the version that
// computed it, so a later definition never changes what it shows.
func (s *Store) underVersion(ctx context.Context, c *class, row *docRow, history bool) (*class, *docRow, error) {
	if row.classVersion == c.cls.Version || row.classVersion == 0 {
		return c, row, nil
	}
	old := s.classAt(c.cls.Name, row.classVersion)
	if old == nil {
		return nil, nil, errf(CodeStorage, "", "the document %s was computed by version %d of %s, which the store does not have", row.id, row.classVersion, c.cls.Name)
	}
	var err error
	if history {
		row, err = old.scanRow(old.r.getVersion.QueryRowContext(ctx, row.id, row.version), true)
	} else {
		row, err = old.scanRow(old.r.getCurrent.QueryRowContext(ctx, row.id), false)
	}
	if err != nil {
		return nil, nil, storageErr("read the document under its class version", err)
	}
	return old, row, nil
}

// Version reads one committed version of a document from its history,
// projected for the principal under the state and the values it had
// (ADR-0041).
func (s *Store) Version(ctx context.Context, class, id string, version int64, opts ...Option) (*HistoryRow, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	// The past is read under the rule of today (ADR-0049, item 6): who may
	// not see the document now is told there is no such version.
	if seen, err := s.sees(ctx, c, id, o); err != nil {
		return nil, err
	} else if !seen {
		return nil, noVersion(c, id, version)
	}
	vc, row, err := s.version(ctx, c, id, version)
	if err != nil {
		return nil, err
	}
	h, err := vc.historyRow(ctx, row)
	if err != nil {
		return nil, err
	}
	s.projectRead(c, vc, row, &h.Document, o)
	return h, nil
}

// noVersion is the answer for a version a document does not have, and
// for any version of a document the caller may not see.
func noVersion(c *class, id string, version int64) *Error {
	return errf(CodeUnknownDocument, "", "no version %d of document %s of class %s", version, id, c.cls.Name)
}

// version reads one history row of a document under the class version
// that computed it, through the pool.
func (s *Store) version(ctx context.Context, c *class, id string, version int64) (*class, *docRow, error) {
	row, err := c.scanRow(c.r.getVersion.QueryRowContext(ctx, id, version), true)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, noVersion(c, id, version)
		}
		return nil, nil, storageErr("read the version", err)
	}
	return s.underVersion(ctx, c, row, true)
}

func (c *class) historyRow(ctx context.Context, row *docRow) (*HistoryRow, error) {
	kids, err := c.loadChildren(ctx, row.id, row.version, nil, false)
	if err != nil {
		return nil, err
	}
	return &HistoryRow{Document: *c.document(row, kids), ValidFrom: row.validFrom, ValidTo: row.validTo, ActorID: row.actor, EventID: row.eventID}, nil
}

// History reads every committed version of a document, oldest first,
// each projected for the principal under its own state and values
// (ADR-0041). A deleted document keeps its history.
func (s *Store) History(ctx context.Context, class, id string, opts ...Option) ([]*HistoryRow, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	// A document the caller may not see has no history for them, as an id
	// no document has (ADR-0049, item 6).
	if seen, err := s.sees(ctx, c, id, o); err != nil {
		return nil, err
	} else if !seen {
		return []*HistoryRow{}, nil
	}
	rows, err := c.r.listHistory.QueryContext(ctx, id)
	if err != nil {
		return nil, storageErr("read the history", err)
	}
	var raw []*docRow
	for rows.Next() {
		row, err := c.scanRow(rows, true)
		if err != nil {
			rows.Close()
			return nil, storageErr("read the history", err)
		}
		raw = append(raw, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the history", err)
	}
	ev := s.reader()
	defer s.release(ev)
	now := s.clock(o)
	out := make([]*HistoryRow, 0, len(raw))
	for _, row := range raw {
		vc, row, err := s.underVersion(ctx, c, row, true)
		if err != nil {
			return nil, err
		}
		h, err := vc.historyRow(ctx, row)
		if err != nil {
			return nil, err
		}
		s.project(ev, c, vc, row, &h.Document, o.principal, now)
		out = append(out, h)
	}
	return out, nil
}

// Events reads the events that touched a document, oldest first; none
// for a document the principal may not see (ADR-0049, item 6).
func (s *Store) Events(ctx context.Context, class, id string, opts ...Option) ([]*Event, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	if seen, err := s.sees(ctx, c, id, apply(opts)); err != nil {
		return nil, err
	} else if !seen {
		return nil, nil
	}
	rows, err := s.stmts.listEvents.QueryContext(ctx, c.cls.Name, id)
	if err != nil {
		return nil, storageErr("read the events", err)
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var ev Event
		var name, from, to, actor, cause, key, code sql.NullString
		var before, after, origin sql.NullInt64
		var day int64
		if err := rows.Scan(&ev.ID, &ev.Class, &ev.DocID, &ev.Kind, &name, &from, &to, &actor, &before, &after, &cause, &key, &day, &ev.TS, &code, &origin); err != nil {
			return nil, storageErr("read the events", err)
		}
		ev.Name, ev.FromState, ev.ToState, ev.ActorID, ev.CauseEventID, ev.CommandKey = name.String, from.String, to.String, actor.String, cause.String, key.String
		ev.Origin = originOf(origin.Int64)
		if ev.Kind == EventActionFailed {
			ev.Code = code.String
		}
		ev.BeforeVersion, ev.AfterVersion = before.Int64, after.Int64
		ev.BusinessDate = formula.Render(formula.Date(day))
		out = append(out, &ev)
	}
	return out, storageErr("read the events", rows.Err())
}
