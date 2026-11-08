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
// at most its limit, with the fields it names (ADR-0038).
func (s *Store) Query(ctx context.Context, f Filter, opts ...Option) ([]*Document, error) {
	c, e := s.class(f.Class)
	if e != nil {
		return nil, e
	}
	_ = apply(opts)
	plan, err := query.Compile(f, c.cls, s.sources())
	if err != nil {
		return nil, filterErr(err)
	}
	cols := append(quoteAll(systemColumns), quoteAll(columnNames(c.cls.Columns))...)
	text := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(c.cls.Name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	text += " ORDER BY " + plan.Order + " LIMIT " + fmt.Sprint(plan.Limit) + " OFFSET " + fmt.Sprint(plan.Offset)
	args := append(plan.Args, contextArgs(opts)...)
	rows, err := s.db.QueryContext(ctx, text, args...)
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
	docs := make([]*Document, len(found))
	for i, row := range found {
		have := map[*child][]childRow{}
		for ch := range wanted {
			have[ch] = kids[ch][row.id]
		}
		d := c.document(row, have)
		if plan.Fields != nil {
			values := make(Values, len(plan.Fields))
			for _, f := range plan.Fields {
				values[f] = d.Values[f]
			}
			d.Values = values
		}
		docs[i] = d
	}
	return docs, nil
}

// Count counts the documents of a class that match a filter's where.
func (s *Store) Count(ctx context.Context, f Filter, opts ...Option) (int64, error) {
	c, e := s.class(f.Class)
	if e != nil {
		return 0, e
	}
	f.Order, f.Fields, f.Limit, f.Offset = nil, nil, 0, 0
	plan, err := query.Compile(f, c.cls, s.sources())
	if err != nil {
		return 0, filterErr(err)
	}
	text := "SELECT count(*) FROM " + quote(c.cls.Name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, text, append(plan.Args, contextArgs(opts)...)...).Scan(&n); err != nil {
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
// the clock and the principal.
func contextArgs(opts []Option) []any {
	o := apply(opts)
	return formula.NewSQLite().ContextArgs(&formula.Context{User: o.principal.ID, Groups: o.principal.Groups})
}

func filterErr(err error) error {
	var qe *query.Error
	if errors.As(err, &qe) {
		return &Error{Code: qe.Code, Variable: qe.Element, Pos: qe.Pos, Message: qe.Message, Hint: qe.Hint}
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
