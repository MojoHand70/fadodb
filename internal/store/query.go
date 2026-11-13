package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"modernc.org/sqlite"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Filter is a query over a class (ADR-0038).
type Filter = query.Filter

// Rows is the result of a SQL statement: the column names and the rows,
// each value as the database returns it, an integer, a float, text, bytes
// or nil.
type Rows struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// MaxSQLRows is the most rows a SQL statement may return.
const MaxSQLRows = 10000

// Query reads the documents of a class that match a filter, in its order,
// at most its limit, with the fields it names (ADR-0038), each projected
// for the principal (ADR-0041): a variable the principal may not read in
// a row's state is absent from that document, and a variable the where
// or the order names matches and sorts only where the principal may read
// it.
func (s *Store) Query(ctx context.Context, f Filter, opts ...Option) ([]*Document, error) {
	c, e := s.class(f.Class)
	if e != nil {
		return nil, e
	}
	o, e := asked(opts)
	if e != nil {
		return nil, e
	}
	plan, err := query.Compile(f, c.cls, s.sources(), caller(o))
	if err != nil {
		return nil, filterErr(err)
	}
	cols := append(quoteAll(systemColumns), quoteAll(columnNames(c.cls.Columns))...)
	cols = append(cols, quoteAll(c.atNames())...)
	text := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(c.cls.Name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	text += " ORDER BY " + plan.Order + " LIMIT " + fmt.Sprint(plan.Limit) + " OFFSET " + fmt.Sprint(plan.Offset)
	args := append(plan.Args, s.contextArgs(o)...)
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, storageErr("prepare the query of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	rows, err := st.stmt.QueryContext(ctx, args...)
	if err != nil {
		return nil, storageErr("query "+c.cls.Name, err)
	}
	var found []*docRow
	for rows.Next() {
		row, err := c.scanRow(rows, false)
		if err != nil {
			rows.Close()
			return nil, storageErr("query "+c.cls.Name, err)
		}
		found = append(found, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, storageErr("query "+c.cls.Name, err)
	}
	// The composites of every document in one statement per child table,
	// for the fields asked for.
	wanted := map[*child]bool{}
	for _, ch := range c.children {
		if plan.Fields == nil {
			wanted[ch] = true
			continue
		}
		for _, f := range plan.Fields {
			if strings.EqualFold(f, ch.v.Name) {
				wanted[ch] = true
			}
		}
	}
	kids := map[*child]map[string][]childRow{}
	for ch := range wanted {
		byDoc, err := ch.loadCurrent(ctx, s.db, c, found)
		if err != nil {
			return nil, err
		}
		kids[ch] = byDoc
	}
	ev := s.reader()
	defer s.release(ev)
	now := s.clock(o)
	docs := make([]*Document, len(found))
	for i, row := range found {
		var d *Document
		if row.classVersion != c.cls.Version && row.classVersion != 0 {
			// A document computed by an older version is read under it.
			vc, vrow, err := s.underVersion(ctx, c, row, false)
			if err != nil {
				return nil, err
			}
			vkids, err := vc.loadChildren(ctx, vrow.id, vrow.version, nil, false)
			if err != nil {
				return nil, err
			}
			d = vc.document(vrow, vkids)
			s.project(ev, c, vc, vrow, d, o.principal, now)
		} else {
			have := map[*child][]childRow{}
			for ch := range wanted {
				have[ch] = kids[ch][row.id]
			}
			d = c.document(row, have)
			s.project(ev, c, c, row, d, o.principal, now)
		}
		if plan.Fields != nil {
			// A hidden variable is absent from the fields as from the
			// document, with no refusal.
			values := make(Values, len(plan.Fields))
			var computed map[string]Moment
			for _, f := range plan.Fields {
				if v, ok := d.Values[f]; ok {
					values[f] = v
				}
				if m, ok := d.Computed[f]; ok {
					if computed == nil {
						computed = map[string]Moment{}
					}
					computed[f] = m
				}
			}
			d.Values, d.Computed = values, computed
		}
		docs[i] = d
	}
	return docs, nil
}

// caller is the principal as the filter compiler sees it.
func caller(o options) query.Principal {
	return query.Principal{ID: o.principal.ID, Groups: o.principal.Groups, Admin: o.principal.Admin}
}

// Count counts the documents of a class that match a filter's where.
func (s *Store) Count(ctx context.Context, f Filter, opts ...Option) (int64, error) {
	c, e := s.class(f.Class)
	if e != nil {
		return 0, e
	}
	o, e := asked(opts)
	if e != nil {
		return 0, e
	}
	f.Order, f.Fields, f.Limit, f.Offset = nil, nil, 0, 0
	plan, err := query.Compile(f, c.cls, s.sources(), caller(o))
	if err != nil {
		return 0, filterErr(err)
	}
	text := "SELECT count(*) FROM " + quote(c.cls.Name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return 0, storageErr("prepare the count of "+c.cls.Name, err)
	}
	defer s.filters.release(st)
	var n int64
	if err := st.stmt.QueryRowContext(ctx, append(plan.Args, s.contextArgs(o)...)...).Scan(&n); err != nil {
		return 0, storageErr("count "+c.cls.Name, err)
	}
	return n, nil
}

// loadCurrent reads the current rows of a composite for the documents
// found, in batches, one statement per batch.
func (ch *child) loadCurrent(ctx context.Context, db *sql.DB, c *class, docs []*docRow) (map[string][]childRow, error) {
	out := make(map[string][]childRow, len(docs))
	cols := quoteAll(columnNames(ch.def.Columns))
	const batch = 500
	for start := 0; start < len(docs); start += batch {
		end := min(start+batch, len(docs))
		args := make([]any, 0, end-start)
		for _, d := range docs[start:end] {
			args = append(args, d.id)
		}
		text := `SELECT c."doc_id", ` + prefixed("c", strings.Join(cols, ", ")) + " FROM " + quote(ch.table) + " c JOIN " + quote(c.table) +
			` d ON d."id" = c."doc_id" AND d."version" = c."version" WHERE c."doc_id" IN (` + marks(len(args)) + `) ORDER BY c."doc_id", c."idx"`
		rows, err := db.QueryContext(ctx, text, args...)
		if err != nil {
			return nil, storageErr("read "+ch.v.Name, err)
		}
		n := len(ch.def.Columns) + 1
		raw := make([]any, n)
		dest := make([]any, n)
		for i := range raw {
			dest[i] = &raw[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, storageErr("read "+ch.v.Name, err)
			}
			id := toString(raw[0])
			row := make(childRow, len(ch.leaves))
			rest := raw[1:]
			for i, l := range ch.leaves {
				row[i], rest = decode(l, rest)
			}
			out[id] = append(out[id], row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, storageErr("read "+ch.v.Name, err)
		}
	}
	return out, nil
}

// contextArgs binds the context parameters a filter's formula may read:
// the principal, and the clock, the moment chosen with At or the
// database's (ADR-0042).
func (s *Store) contextArgs(o options) []any {
	ctx := contextOf(o.principal, s.clock(o))
	return formula.NewSQLite().ContextArgs(&ctx)
}

func filterErr(err error) error {
	var qe *query.Error
	if errors.As(err, &qe) {
		variable := qe.Element
		if qe.Variable != "" {
			variable = qe.Variable
		}
		return &Error{Code: qe.Code, Variable: variable, Pos: qe.Pos, Message: qe.Message, Hint: qe.Hint}
	}
	return err
}

// SQL runs one read-only statement over the views (ADR-0038): SELECT
// only, the views and the functions, nothing physical. Positional
// parameters bind args. At most MaxSQLRows rows come back; more is the
// error too_many_rows.
func (s *Store) SQL(ctx context.Context, text string, args []any, opts ...Option) (*Rows, error) {
	_ = apply(opts)
	s.sqlMu.Lock()
	defer s.sqlMu.Unlock()
	if s.sqlConn == nil {
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return nil, storageErr("open the SQL connection", err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA query_only = 1"); err != nil {
			conn.Close()
			return nil, storageErr("set query_only", err)
		}
		if err := conn.Raw(func(driverConn any) error {
			r, ok := driverConn.(sqlite.AuthorizerRegisterer)
			if !ok {
				return fmt.Errorf("the driver has no authorizer")
			}
			return r.RegisterAuthorizer(query.Authorizer(s.viewNames()))
		}); err != nil {
			conn.Close()
			return nil, storageErr("install the authorizer", err)
		}
		s.sqlConn = conn
	}
	rows, err := s.sqlConn.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, sqlErr(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, sqlErr(err)
	}
	out := &Rows{Columns: cols}
	for rows.Next() {
		if len(out.Rows) >= MaxSQLRows {
			return nil, hintf(CodeTooManyRows, "", fmt.Sprintf("the statement returns more than %d rows", MaxSQLRows), "add a LIMIT, or filter further")
		}
		vals := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, sqlErr(err)
		}
		out.Rows = append(out.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, sqlErr(err)
	}
	return out, nil
}

// sources are the store's tables as the binder sees them.
func (s *Store) sources() formula.Sources {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.env().Sources
}

// viewNames lists, upper-cased, the views a SQL statement may read: the
// classes' views and the tables'.
func (s *Store) viewNames() map[string]bool {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	views := map[string]bool{}
	for _, c := range s.classes {
		for _, v := range c.viewNames() {
			views[strings.ToUpper(v)] = true
		}
	}
	for _, t := range s.tables {
		views[strings.ToUpper(t.def.Name)] = true
	}
	return views
}

// sqlErr turns the database's refusal into the store's: a statement the
// authorizer refused is not_allowed, anything else is bad_value with the
// engine's words.
func sqlErr(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "authorization denied") || strings.Contains(msg, "not authorized") || strings.Contains(msg, "prohibited") ||
		strings.Contains(msg, "query_only") || strings.Contains(msg, "readonly") {
		return hintf(CodeNotAllowed, "", "the statement is not allowed: "+msg, "the endpoint runs SELECT over the views; physical tables, writes and pragmas are refused")
	}
	return &Error{Code: CodeBadValue, Message: msg}
}
