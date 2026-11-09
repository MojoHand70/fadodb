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
	n := len(systemColumns) + len(c.cls.Columns)
	if history {
		n += len(historyColumns)
	}
	raw := make([]any, n)
	dest := make([]any, n)
	for i := range raw {
		dest[i] = &raw[i]
	}
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	r := &docRow{id: toString(raw[0]), title: toString(raw[1]), state: toString(raw[2]), version: toInt64(raw[3]), classVersion: int(toInt64(raw[4])),
		createdAt: toInt64(raw[5]), updatedAt: toInt64(raw[6]), stateEntered: toInt64(raw[7])}
	rest := raw[len(systemColumns):]
	if history {
		r.validFrom, r.validTo, r.actor, r.eventID = toInt64(rest[0]), toInt64(rest[1]), toString(rest[2]), toString(rest[3])
		rest = rest[len(historyColumns):]
	}
	r.vals = make([]formula.Value, len(c.leaves))
	for i, l := range c.leaves {
		r.vals[i], rest = decode(l, rest)
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
		CreatedAt: r.createdAt, UpdatedAt: r.updatedAt, StateEnteredAt: r.stateEntered, Values: make(Values, len(c.cls.Variables))}
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
			return nil, errf(CodeUnknownDocument, "", "no document %s of class %s", id, c.cls.Name)
		}
		return nil, storageErr("read the document", err)
	}
	vc, row, err := s.underVersion(ctx, c, row, false)
	if err != nil {
		return nil, err
	}
	kids, err := vc.loadChildren(ctx, id, row.version, nil, false)
	if err != nil {
		return nil, err
	}
	d := vc.document(row, kids)
	s.projectRead(c, vc, row, d, o.principal)
	return d, nil
}

// projectRead projects a document on a read path with a borrowed
// evaluator (ADR-0041).
func (s *Store) projectRead(c, vc *class, row *docRow, d *Document, p Principal) {
	if c.rights == nil || p.Admin {
		return
	}
	ev := s.reader()
	s.project(ev, c, vc, row, d, p, s.now().Unix())
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
	vc, row, err := s.version(ctx, c, id, version)
	if err != nil {
		return nil, err
	}
	h, err := vc.historyRow(ctx, row)
	if err != nil {
		return nil, err
	}
	s.projectRead(c, vc, row, &h.Document, o.principal)
	return h, nil
}

// version reads one history row of a document under the class version
// that computed it, through the pool.
func (s *Store) version(ctx context.Context, c *class, id string, version int64) (*class, *docRow, error) {
	row, err := c.scanRow(c.r.getVersion.QueryRowContext(ctx, id, version), true)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, errf(CodeUnknownDocument, "", "no version %d of document %s of class %s", version, id, c.cls.Name)
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
	now := s.now().Unix()
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

// Events reads the events that touched a document, oldest first.
func (s *Store) Events(ctx context.Context, class, id string, opts ...Option) ([]*Event, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	_ = apply(opts)
	rows, err := s.stmts.listEvents.QueryContext(ctx, c.cls.Name, id)
	if err != nil {
		return nil, storageErr("read the events", err)
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var ev Event
		var name, from, to, actor, cause, key sql.NullString
		var before, after sql.NullInt64
		var day int64
		if err := rows.Scan(&ev.ID, &ev.Class, &ev.DocID, &ev.Kind, &name, &from, &to, &actor, &before, &after, &cause, &key, &day, &ev.TS); err != nil {
			return nil, storageErr("read the events", err)
		}
		ev.Name, ev.FromState, ev.ToState, ev.ActorID, ev.CauseEventID, ev.CommandKey = name.String, from.String, to.String, actor.String, cause.String, key.String
		ev.BeforeVersion, ev.AfterVersion = before.Int64, after.Int64
		ev.BusinessDate = formula.Render(formula.Date(day))
		out = append(out, &ev)
	}
	return out, storageErr("read the events", rows.Err())
}
