package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Explicit tables (ADR-0016, ADR-0037): reference data a formula reads
// through LOOKUP, defined in JSON, stored in the database file under
// tbl_<Name> with a view <Name>, filled by ImportRows and served to auto
// formulas from an in-memory index while small and from a prepared
// statement otherwise (ADR-0036).

// table is a stored explicit table with its layout, statements and the
// cache that serves indexed lookups.
type table struct {
	def       *model.Table
	json      []byte
	name      string // the physical table, tbl_<Name>
	leaves    []leaf
	leafIndex map[string]int
	src       *formula.Source

	upsert, insert, deleteAll, selectAll *sql.Stmt // on the writer
	readAll                              *sql.Stmt // on the pool

	// cache holds the rows while the table is small enough (ADR-0036);
	// nil until a lookup needs it, dropped by an import and by a rule's
	// table action. tooBig says the last build found more rows than the
	// limit.
	cache  *tableCache
	tooBig bool
}

type tableCache struct {
	rows  [][]formula.Value       // row-major, in key order, in leaf order
	byCol map[string]map[ikey]int // upper-cased column: canonical key to the first row
}

// ikey is a value as the equality operator sees it (ADR-0005, ADR-0019):
// a text, ref or json column by its text with blank as ""; a number,
// date, datetime or bool column by its number with blank as 0; a decimal
// column by its mantissa with trailing zeros stripped, so that 1.5 and
// 1.50 are one key and blank is 0. Money columns are not indexed, since
// a money compares with a decimal by its amount and with money of
// another currency not at all.
type ikey struct {
	kind  formula.Kind
	num   uint64
	scale int8
	str   string
}

func canonKey(v formula.Value, col formula.Type) ikey {
	switch col.Kind {
	case formula.KindText, formula.KindRef, formula.KindJSON:
		return ikey{kind: formula.KindText, str: v.Str()}
	case formula.KindDecimal:
		m, s := v.Mant(), int8(v.Scale())
		if v.IsBlank() {
			m, s = 0, 0
		}
		for m != 0 && m%10 == 0 && s > 0 {
			m /= 10
			s--
		}
		if m == 0 {
			s = 0
		}
		return ikey{kind: formula.KindDecimal, num: uint64(m), scale: s}
	}
	f := v.Num()
	if f == 0 {
		f = 0 // one key for -0 and 0
	}
	return ikey{kind: formula.KindNumber, num: math.Float64bits(f)}
}

func newTable(def *model.Table, src []byte) *table {
	t := &table{def: def, json: src, name: "tbl_" + def.Name, src: def.Source()}
	t.leaves = leavesOf(def.Columns)
	t.leafIndex = indexOf(t.leaves)
	return t
}

func (t *table) createDDL() []string {
	ddl := []string{
		"CREATE TABLE " + quote(t.name) + " (" + columnDDL(t.def.Columns) + ", PRIMARY KEY (" + strings.Join(quoteAll(t.def.Key), ", ") + "))",
	}
	for _, idx := range t.def.Indexes {
		ddl = append(ddl, "CREATE INDEX "+quote(t.name+"_"+strings.Join(idx, "_"))+" ON "+quote(t.name)+" ("+strings.Join(quoteAll(idx), ", ")+")")
	}
	return append(ddl, t.viewDDL())
}

// viewDDL makes the view of the table that SQL reads, under the table's
// own name, with every column.
func (t *table) viewDDL() string {
	return "CREATE VIEW " + quote(t.def.Name) + " AS SELECT " + strings.Join(quoteAll(columnNames(t.def.Columns)), ", ") + " FROM " + quote(t.name)
}

func (t *table) dropDDL() []string {
	return []string{"DROP VIEW IF EXISTS " + quote(t.def.Name), "DROP TABLE IF EXISTS " + quote(t.name)}
}

func (t *table) prepare(ctx context.Context, writer *sql.Conn, db *sql.DB) error {
	cols := quoteAll(columnNames(t.def.Columns))
	keys := map[string]bool{}
	for _, k := range t.def.Key {
		keys[strings.ToUpper(k)] = true
	}
	var sets []string
	for _, c := range t.def.Columns {
		if !keys[strings.ToUpper(c.Variable)] {
			sets = append(sets, quote(c.Name)+" = excluded."+quote(c.Name))
		}
	}
	conflict := " DO NOTHING"
	if len(sets) > 0 {
		conflict = " DO UPDATE SET " + strings.Join(sets, ", ")
	}
	order := " ORDER BY " + strings.Join(quoteAll(t.def.Key), ", ")
	selectAll := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(t.name) + order
	var err error
	prep := func(dst **sql.Stmt, on interface {
		PrepareContext(context.Context, string) (*sql.Stmt, error)
	}, text string) {
		if err == nil {
			*dst, err = on.PrepareContext(ctx, text)
		}
	}
	prep(&t.upsert, writer, "INSERT INTO "+quote(t.name)+" ("+strings.Join(cols, ", ")+") VALUES ("+marks(len(cols))+") ON CONFLICT ("+strings.Join(quoteAll(t.def.Key), ", ")+")"+conflict)
	// The insert of a rule's table.insert, which an existing key refuses.
	prep(&t.insert, writer, "INSERT INTO "+quote(t.name)+" ("+strings.Join(cols, ", ")+") VALUES ("+marks(len(cols))+") ON CONFLICT ("+strings.Join(quoteAll(t.def.Key), ", ")+") DO NOTHING")
	prep(&t.deleteAll, writer, "DELETE FROM "+quote(t.name))
	prep(&t.selectAll, writer, selectAll)
	prep(&t.readAll, db, selectAll)
	if err != nil {
		t.close()
		return storageErr("prepare the statements of "+t.def.Name, err)
	}
	return nil
}

func (t *table) close() {
	for _, st := range []*sql.Stmt{t.upsert, t.insert, t.deleteAll, t.selectAll, t.readAll} {
		if st != nil {
			st.Close()
		}
	}
}

// keyFields are the columns of the key, or with key false the others.
func (t *table) keyFields(key bool) []formula.Field {
	var out []formula.Field
	for _, f := range t.def.Fields {
		isKey := false
		for _, k := range t.def.Key {
			isKey = isKey || strings.EqualFold(k, f.Name)
		}
		if isKey == key {
			out = append(out, f)
		}
	}
	return out
}

// scanRows reads every row of the table in key order with the statement.
func (t *table) scanRows(ctx context.Context, st *sql.Stmt, limit int) ([][]formula.Value, bool, error) {
	rows, err := st.QueryContext(ctx)
	if err != nil {
		return nil, false, storageErr("read "+t.def.Name, err)
	}
	defer rows.Close()
	n := len(t.def.Columns)
	raw := make([]any, n)
	dest := make([]any, n)
	for i := range raw {
		dest[i] = &raw[i]
	}
	var out [][]formula.Value
	for rows.Next() {
		if limit > 0 && len(out) >= limit {
			return nil, true, nil
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, false, storageErr("read "+t.def.Name, err)
		}
		row := make([]formula.Value, len(t.leaves))
		rest := raw
		for i, l := range t.leaves {
			row[i], rest = decode(l, rest)
		}
		out = append(out, row)
	}
	return out, false, storageErr("read "+t.def.Name, rows.Err())
}

func (s *Store) registerTable(t *table) {
	key := strings.ToUpper(t.def.Name)
	if _, exists := s.tables[key]; !exists {
		s.tableOrder = append(s.tableOrder, t.def.Name)
	}
	s.tables[key] = t
}

func (s *Store) table(name string) (*table, *Error) {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	t, ok := s.tables[strings.ToUpper(name)]
	if !ok {
		return nil, errf(CodeUnknownTable, "", "unknown table %q", name)
	}
	return t, nil
}

// Tables lists the explicit tables in definition order.
func (s *Store) Tables() []string {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return append([]string(nil), s.tableOrder...)
}

// TablesOf lists the tables that belong to an app, in the order they
// were defined (ADR-0079), as ClassesOf lists its classes. An app no
// table belongs to answers an empty list; a name that is no app is
// refused.
func (s *Store) TablesOf(name string) ([]string, error) {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	if _, ok := s.apps[strings.ToUpper(name)]; !ok {
		return nil, s.noSuchApp(name)
	}
	out := []string{}
	for _, n := range s.tableOrder {
		if t, ok := s.tables[strings.ToUpper(n)]; ok && strings.EqualFold(t.def.App, name) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Table returns a compiled table definition by name.
func (s *Store) Table(name string) (*model.Table, bool) {
	t, err := s.table(name)
	if err != nil {
		return nil, false
	}
	return t.def, true
}

// DefineTable compiles an explicit table definition, creates the table
// and its view and stores the definition. The same definition again is a
// no-op. A table with no rows and no class reading it can be redefined;
// one with rows, or one a class reads, cannot in this build.
func (s *Store) DefineTable(ctx context.Context, data []byte, opts ...Option) (*model.Table, error) {
	o, e := s.worker(ctx, workDesign, opts)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	compact, err := compactJSON(data)
	if err != nil {
		return nil, model.Diagnostics{{Code: model.CodeBadJSON, Pos: -1, Message: err.Error()}}
	}
	old := s.tables[upperName(data)]
	if old != nil && bytes.Equal(old.json, compact) {
		return old.def, nil
	}
	def, diags := model.DefineTable(data)
	if def == nil {
		return nil, diags
	}
	version := def.Version
	// The app the table belongs to is one the database has (ADR-0079,
	// item 2), as for a class (ADR-0077, item 3).
	if def.App != "" {
		if _, ok := s.apps[strings.ToUpper(def.App)]; !ok {
			diags = append(diags, model.Diagnostic{Code: model.CodeUnknownApp, Path: "/app", Pos: -1, Message: "unknown app " + strconv.Quote(def.App),
				Hint: "define the app first, with define_app, or leave app out: a table may belong to none"})
		}
	}
	// A table moves to another app by its next version, whatever it holds
	// and whoever reads it (ADR-0079, item 4): its columns, key and
	// indexes stay as they are, and so do its rows and its storage.
	moving := false
	var adding []string
	if old != nil {
		if old.def.Name != def.Name {
			diags = append(diags, model.Diagnostic{Code: model.CodeDuplicate, Path: "/name", Pos: -1, Message: "a table " + old.def.Name + " exists; names differ in case only"})
		}
		version = old.def.Version + 1
		if def.Def.Version != 0 && def.Def.Version != version {
			diags = append(diags, model.Diagnostic{Code: model.CodeBadValue, Path: "/version", Pos: -1,
				Message: "the stored table is at version " + itoa(old.def.Version) + "; a redefinition is version " + itoa(version), Hint: "omit version, or write " + itoa(version)})
		}
		moving = model.OnlyTheApp(old.def.Def, def.Def)
		if !moving {
			adding, _ = model.AddsColumns(old.def.Def, def.Def)
		}
	}
	// A table removed before under this name left its versions in the log
	// of definitions: this one is the next after them (ADR-0082, item 3).
	if old == nil {
		var last sql.NullInt64
		if err := s.writer.QueryRowContext(ctx, "SELECT max(version) FROM definitions WHERE what = ? AND name = ?", DefinitionTable, def.Name).Scan(&last); err == nil && last.Valid {
			next := int(last.Int64) + 1
			if def.Def.Version != 0 && def.Def.Version != next {
				diags = append(diags, model.Diagnostic{Code: model.CodeBadValue, Path: "/version", Pos: -1,
					Message: "a table of this name was removed at version " + itoa(int(last.Int64)) + "; this one is version " + itoa(next), Hint: "omit version, or write " + itoa(next)})
			}
			version = next
		}
	}
	// A version that adds columns and changes nothing else is taken
	// whatever the table holds and whoever reads it (ADR-0080): the rows
	// it holds are blank in each new column. A rule of a class that writes
	// the table is held to it as to any version.
	if len(adding) > 0 {
		diags = append(diags, s.brokenRules(nil, def, nil)...)
	}
	// Any other version of a table in use, one that holds rows or that a
	// class reads, is taken when it keeps its key, uses of its columns
	// stay whole and its rows convert (ADR-0084); its rows are carried
	// over, as it takes them.
	var carried []Values
	if old != nil && !moving && len(adding) == 0 {
		inUse := false
		for _, name := range s.order {
			if s.classes[strings.ToUpper(name)].sources[strings.ToUpper(def.Name)] {
				inUse = true
				break
			}
		}
		var n int64
		if err := s.writer.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(old.name)).Scan(&n); err == nil && n > 0 {
			inUse = true
		}
		if inUse {
			rows, d := s.reshape(ctx, old, def)
			carried, diags = rows, append(diags, d...)
		}
		// A rule of a class may write the table (ADR-0044): a new shape
		// may not break it.
		diags = append(diags, s.brokenRules(nil, def, nil)...)
	}
	if len(diags) > 0 {
		return nil, diags
	}
	if _, isClass := s.classes[strings.ToUpper(def.Name)]; isClass {
		return nil, model.Diagnostics{{Code: model.CodeDuplicate, Path: "/name", Pos: -1, Message: "a class " + def.Name + " exists"}}
	}
	def.Version = version
	t := newTable(def, compact)
	now := s.now().Unix()
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	rollback := func(err error) (*model.Table, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return nil, err
	}
	// A move to another app keeps the storage as it is; columns added are
	// added to it, and its view is made again with them; any other version
	// is the table made again, empty.
	if len(adding) > 0 {
		added := make(map[string]bool, len(adding))
		for _, name := range adding {
			added[strings.ToUpper(name)] = true
		}
		for _, c := range t.def.Columns {
			if !added[strings.ToUpper(c.Variable)] {
				continue
			}
			if _, err := w.ExecContext(ctx, "ALTER TABLE "+quote(t.name)+" ADD COLUMN "+quote(c.Name)+" "+sqlType(c.Type.Kind)); err != nil {
				return rollback(storageErr("add the column "+c.Name+" to "+def.Name, err))
			}
		}
		for _, ddl := range []string{"DROP VIEW IF EXISTS " + quote(def.Name), t.viewDDL()} {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return rollback(storageErr("make the view of "+def.Name+" again", err))
			}
		}
	} else if !moving {
		if old != nil {
			for _, ddl := range old.dropDDL() {
				if _, err := w.ExecContext(ctx, ddl); err != nil {
					return rollback(storageErr("drop the table "+old.def.Name, err))
				}
			}
		}
		for _, name := range []string{t.name, def.Name} {
			var n int64
			if err := w.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = ? COLLATE NOCASE", name).Scan(&n); err != nil {
				return rollback(storageErr("check the names", err))
			}
			if n > 0 {
				return rollback(hintf(CodeDuplicate, "", "the name "+name+" is taken by another table or view", "rename the table"))
			}
		}
		for _, ddl := range t.createDDL() {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return rollback(storageErr("create the table "+def.Name, err))
			}
		}
		// The rows the old shape held, as the new one takes them, in the
		// same transaction (ADR-0084).
		if len(carried) > 0 {
			cols := quoteAll(columnNames(t.def.Columns))
			ins := "INSERT INTO " + quote(t.name) + " (" + strings.Join(cols, ", ") + ") VALUES (" + marks(len(cols)) + ")"
			vals := make([]formula.Value, len(t.leaves))
			args := make([]any, 0, len(cols))
			for _, row := range carried {
				for j := range vals {
					vals[j] = formula.Blank()
				}
				if e := setFields(vals, t.leafIndex, "", t.def.Fields, row, "", s.limits); e != nil {
					return rollback(e)
				}
				args = args[:0]
				for j, l := range t.leaves {
					args = encode(args, l, vals[j])
				}
				if _, err := w.ExecContext(ctx, ins, args...); err != nil {
					return rollback(storageErr("carry the rows of "+def.Name, err))
				}
			}
		}
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO tables_registry (name, version, binding, json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)"+
		" ON CONFLICT (name) DO UPDATE SET name = excluded.name, version = excluded.version, binding = excluded.binding, json = excluded.json, updated_at = excluded.updated_at",
		def.Name, version, def.Binding, string(compact), now, now); err != nil {
		return rollback(storageErr("store the table", err))
	}
	defined, derr := recordDefinition(ctx, w, DefinitionTable, def.Name, version, o.principal, now)
	if derr != nil {
		return rollback(derr)
	}
	def.Defined = defined
	// A table that held rows may hold them otherwise under its new
	// definition (ADR-0084): the values that follow it are looked at
	// again (ADR-0091), as the designer who changed it.
	followed := 0
	if old != nil && !moving {
		var ferr error
		if followed, ferr = s.queueFollowsOf(ctx, t, s.followersOf(def.Name), o.principal, o.reason, 0, s.now()); ferr != nil {
			return rollback(ferr)
		}
	}
	if err := s.endTransaction(ctx); err != nil {
		return nil, storageErr("commit", err)
	}
	if followed > 0 {
		s.wakeWorker()
	}
	if moving {
		// The same storage, the same statements and the same rows in
		// memory, under the new definition. A reader that looked the
		// table up before goes on with the old one, which names the old
		// app and nothing else that differs; its statements stay open,
		// since the new one holds them too. A lookup's plan holds the
		// table it was made for, so the plans are made again, against
		// this one, as after any other version: an import empties the
		// rows in memory of the table the database holds now.
		moved := *old
		moved.def, moved.json = def, compact
		s.closePlans()
		s.registerTable(&moved)
		return def, nil
	}
	if old != nil {
		old.close()
		s.closePlans()
	}
	if err := t.prepare(ctx, w, s.db); err != nil {
		delete(s.tables, strings.ToUpper(def.Name))
		return nil, err
	}
	s.registerTable(t)
	return def, nil
}

// ImportRows writes rows into a table in one transaction, each a map of
// column to value in the wire shape, inserting a row whose key is new and
// replacing one whose key exists; with Replace, the table is emptied
// first. It returns the number of rows written and drops the table's
// index, which the next lookup rebuilds (ADR-0036).
func (s *Store) ImportRows(ctx context.Context, name string, rows []Values, opts ...Option) (int, error) {
	t, e := s.table(name)
	if e != nil {
		return 0, e
	}
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return 0, e
	}
	if err := refuseAt(o, "row import"); err != nil {
		return 0, err
	}
	// The write rule of the table (ADR-0051, item 7). A content
	// administrator's import past it is written down with the rows, in
	// the same transaction (ADR-0067, item 7).
	past, e := s.tableRefusal(t, true, o, s.clock(o))
	if e != nil {
		return 0, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errf(CodeClosed, "", "the store is closed")
	}
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, storageErr("begin", err)
	}
	rollback := func(err error) (int, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return 0, err
	}
	if o.replace {
		if _, err := t.deleteAll.ExecContext(ctx); err != nil {
			return rollback(storageErr("empty "+t.def.Name, err))
		}
	}
	vals := make([]formula.Value, len(t.leaves))
	args := make([]any, 0, len(t.def.Columns))
	for i, row := range rows {
		for j := range vals {
			vals[j] = formula.Blank()
		}
		if err := setFields(vals, t.leafIndex, "", t.def.Fields, row, "", s.limits); err != nil {
			err.Variable = fmt.Sprintf("%s[%d].%s", t.def.Name, i, err.Variable)
			return rollback(err)
		}
		for _, k := range t.def.Key {
			if vals[t.leafIndex[strings.ToUpper(k)]].IsBlank() {
				return rollback(errf(CodeRequired, fmt.Sprintf("%s[%d].%s", t.def.Name, i, k), "a key column is not blank"))
			}
		}
		args = args[:0]
		for j, l := range t.leaves {
			args = encode(args, l, vals[j])
		}
		if _, err := t.upsert.ExecContext(ctx, args...); err != nil {
			return rollback(storageErr(fmt.Sprintf("write row %d of %s", i, t.def.Name), err))
		}
	}
	if past {
		r := &readLog{command: "import_rows", table: t.def.Name, rules: []string{"/access/write"}, rows: int64(len(rows)), hasRows: true, past: true}
		if err := s.insertRead(ctx, w, o, r); err != nil {
			return rollback(err)
		}
	}
	// The values that follow the table (ADR-0091), queued with the rows.
	followed, err := s.queueFollows(ctx, t, o.principal, o.reason, 0, s.now())
	if err != nil {
		return rollback(err)
	}
	if err := s.endTransaction(ctx); err != nil {
		return rollback(storageErr("commit", err))
	}
	t.cache, t.tooBig = nil, false
	if followed > 0 {
		s.wakeWorker()
	}
	return len(rows), nil
}

// TableRows reads every row of a table in key order, in the wire shape,
// for a principal the table's read rule lets through (ADR-0051, item 7).
func (s *Store) TableRows(ctx context.Context, name string, opts ...Option) ([]Values, error) {
	t, e := s.table(name)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, e
	}
	past, e := s.tableRefusal(t, false, o, s.clock(o))
	if e != nil {
		return nil, e
	}
	rows, _, err := t.scanRows(ctx, t.readAll, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Values, len(rows))
	for i, row := range rows {
		m, _ := getFields(row, t.leafIndex, "", t.def.Fields)
		out[i] = m
	}
	if past {
		r := &readLog{command: "table_rows", table: t.def.Name, rules: []string{"/access/read"}, rows: int64(len(rows)), hasRows: true, past: true}
		if err := s.logRead(ctx, o, r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// lookupPlan is what one query slot needs at run time: its table and the
// result column, the argument of each slot of the key expression, and
// the prepared statement of the statement path, made on first use.
type lookupPlan struct {
	// tbl is the explicit table the slot reads, nil when it reads the
	// documents of a class (ADR-0046), and name the physical table of
	// either.
	tbl  *table
	name string
	// col is the result column, -1 for an aggregate. rows says the slot
	// needs every row of its key, not the first, so the index of first
	// rows cannot answer it; the documents of a class have no such index.
	col  int
	rows bool
	// empty says the source has no rows at all: see plan.
	empty   bool
	keyArgs []int
	keyVars []formula.Value
	// idxCol is the upper-cased index column and idxType its type, for
	// the equality shape.
	idxCol  string
	idxType formula.Type
	// stmt is the statement of the condition alone; keyed the same with
	// the index column pinned to the key, which lets SQLite use the
	// table's index (ADR-0036). params are the context parameters the
	// condition reads, which both statements bind.
	stmt, keyed *sql.Stmt
	// keyedSQL is the text of keyed, kept so that a test can ask SQLite
	// how it runs it.
	keyedSQL string
	params   []string
	nargs    int
	money    []bool // per argument, whether it is a money value with a currency parameter
}

// executor is the store as the VM's QueryExecutor (ADR-0020, ADR-0036).
// It runs under the writer's lock, inside the transaction of the commit
// or preview that evaluates the formula.
type executor Store

// Lookup implements formula.QueryExecutor.
func (e *executor) Lookup(q *formula.QuerySlot, args []formula.Value, ctx *formula.Context) (formula.Value, *formula.EvalError) {
	s := (*Store)(e)
	plan, err := s.plan(q)
	if err != nil {
		return formula.Value{}, err
	}
	if plan.empty {
		return plan.none(q)
	}
	var key formula.Value
	if q.Key != nil {
		for i, a := range plan.keyArgs {
			plan.keyVars[i] = args[a]
		}
		var kerr error
		if key, kerr = s.qvm.Run(q.Key, plan.keyVars, ctx); kerr != nil {
			// The key expression fails on every row, so no row matches.
			return plan.none(q)
		}
		// The index holds the first row of each key, which answers the
		// lookup when the equality is the whole condition. A key that is
		// one term of a conjunction, and an aggregate, need every row of
		// the key: the statement reads them (ADR-0046).
		if !plan.rows {
			if cache, ok := plan.tbl.cacheFor(s); ok {
				pos, found := cache.index(plan.tbl, plan.idxCol)[canonKey(key, plan.idxType)]
				if !found {
					return formula.Blank(), nil
				}
				return cache.rows[pos][plan.col], nil
			}
		}
	}
	return s.lookupStatement(plan, q, args, key, ctx)
}

// none is the answer of a slot no row matches: blank for a column, and
// what the aggregate gives no values.
func (p *lookupPlan) none(q *formula.QuerySlot) (formula.Value, *formula.EvalError) {
	if q.Agg == "" {
		return formula.Blank(), nil
	}
	return q.Aggregate(nil)
}

// plan finds or makes the plan of a slot.
func (s *Store) plan(q *formula.QuerySlot) (*lookupPlan, *formula.EvalError) {
	if p, ok := s.plans[q]; ok {
		return p, nil
	}
	p := &lookupPlan{col: -1, nargs: len(q.Args), money: make([]bool, len(q.Args))}
	// typeOf gives the type of a column of the source.
	var typeOf func(column string) (formula.Type, bool)
	if t, ok := s.tables[strings.ToUpper(q.Source)]; ok {
		p.tbl, p.name, p.rows = t, t.name, q.KeyPart || q.Agg != ""
		typeOf = func(column string) (formula.Type, bool) {
			li, ok := t.leafIndex[strings.ToUpper(column)]
			if !ok {
				return formula.Type{}, false
			}
			if q.Agg == "" && strings.EqualFold(column, q.Column) {
				p.col = li
			}
			return t.leaves[li].typ, true
		}
	} else if c, ok := s.classes[strings.ToUpper(q.Source)]; ok {
		// The documents of a class: its current table, read by a statement
		// on the writer, inside the transaction of the commit or preview
		// that evaluates the formula (ADR-0046).
		src := c.cls.Source()
		p.name, p.rows = c.table, true
		typeOf = func(column string) (formula.Type, bool) {
			f, ok := src.Column(column)
			return f.Type, ok
		}
	} else {
		// The binder knew the source, and the store does not: the draft of
		// a class nobody has defined yet reads its own documents, and
		// there are none (ADR-0025, ADR-0046).
		p.empty = true
		s.plans[q] = p
		return p, nil
	}
	// A draft may read what the stored class does not have yet (ADR-0025).
	for _, prog := range []*formula.Program{q.Cond, q.Value} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if _, ok := typeOf(sl.Name); !ok && q.ArgIndex(sl.Name) < 0 && !strings.HasPrefix(sl.Name, "THIS.") {
				return nil, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: "unknown column " + sl.Name + " of " + q.Source + "; a draft reads the documents of the stored class"}
			}
		}
	}
	if q.Agg == "" {
		if _, ok := typeOf(q.Column); !ok {
			return nil, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: "unknown column " + q.Column + " of " + q.Source}
		}
	}
	if q.Key != nil {
		p.keyArgs = make([]int, len(q.Key.Slots))
		p.keyVars = make([]formula.Value, len(q.Key.Slots))
		for i, sl := range q.Key.Slots {
			p.keyArgs[i] = q.ArgIndex(sl.Name)
		}
		p.idxCol = strings.ToUpper(q.IndexColumn)
		var ok bool
		if p.idxType, ok = typeOf(q.IndexColumn); !ok {
			return nil, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: "unknown column " + q.IndexColumn + " of " + q.Source}
		}
	}
	for _, prog := range []*formula.Program{q.Cond, q.Value} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if i := q.ArgIndex(sl.Name); i >= 0 && sl.Type.Kind == formula.KindMoney {
				p.money[i] = true
			}
		}
	}
	s.plans[q] = p
	return p, nil
}

func (s *Store) closePlans() {
	for q, p := range s.plans {
		for _, st := range []*sql.Stmt{p.stmt, p.keyed} {
			if st != nil {
				st.Close()
			}
		}
		delete(s.plans, q)
	}
}

// lookupStatement runs the lookup as a prepared statement on the writer
// (ADR-0036): the subquery of LookupSQL with the arguments as parameters,
// or the statement of AggregateSQL for an aggregate (ADR-0046).
// For the equality shape with a key that is neither blank nor the zero of
// its kind, the table is first narrowed to the rows whose index column
// equals the key, which is exact, since a non-zero key equals no blank
// row, and lets SQLite use the table's index; the condition itself still
// decides. A blank or zero key reads as every blank row too, and runs the
// condition over the whole table.
func (s *Store) lookupStatement(p *lookupPlan, q *formula.QuerySlot, args []formula.Value, key formula.Value, ctx *formula.Context) (formula.Value, *formula.EvalError) {
	var keyArg any
	keyed := false
	if q.Key != nil && !key.IsBlank() {
		switch {
		case p.idxType.Kind == formula.KindDecimal:
			k, err := formula.Rescale(key, int(p.idxType.Scale))
			if err != nil {
				return p.none(q) // more places than the column keeps: equal to no row
			}
			keyed, keyArg = k.Mant() != 0, k.Mant()
		case p.idxType.Kind == formula.KindText || p.idxType.Kind == formula.KindRef || p.idxType.Kind == formula.KindJSON:
			keyed, keyArg = key.Str() != "", key.Str()
		default:
			keyed, keyArg = key.Num() != 0, key.DriverValue()
		}
	}
	stmt := &p.stmt
	if keyed {
		stmt = &p.keyed
	}
	if *stmt == nil {
		d := formula.NewSQLite().WithLimits(s.limits)
		params := formula.NewParams(d)
		arg := func(i int, currency bool) string {
			if currency {
				return d.Param(fmt.Sprintf("a%dc", i))
			}
			return d.Param(fmt.Sprintf("a%d", i))
		}
		table := func(string) string { return quote(p.name) }
		if keyed {
			table = func(string) string {
				return "(SELECT * FROM " + quote(p.name) + " WHERE " + quote(q.IndexColumn) + " = " + d.Param("k") + ")"
			}
		}
		// A column is the subquery of LookupSQL; an aggregate is one
		// statement over the matching rows, with its error beside it.
		var text string
		if q.Agg != "" {
			var err error
			if text, err = formula.AggregateSQL(q, d, arg, table, params); err != nil {
				return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
			}
		} else {
			e, err := formula.LookupSQL(q, d, arg, table, params)
			if err != nil {
				return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
			}
			cur := "NULL"
			if e.Cur != "" {
				cur = e.Cur
			}
			text = "SELECT (" + e.Val + ") AS v, (" + cur + ") AS c"
		}
		p.params = params.Names()
		if keyed {
			p.keyedSQL = text
		}
		prepared, err := s.writer.PrepareContext(context.Background(), text)
		if err != nil {
			return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: "prepare the lookup: " + err.Error()}
		}
		*stmt = prepared
	}
	named := make([]any, 0, 2*len(args)+4)
	for i, a := range args {
		named = append(named, sql.Named(fmt.Sprintf("a%d", i), a.DriverValue()))
		if p.money[i] {
			var cur any
			if a.Kind() == formula.KindMoney {
				cur = a.Currency()
			}
			named = append(named, sql.Named(fmt.Sprintf("a%dc", i), cur))
		}
	}
	if keyed {
		named = append(named, sql.Named("k", keyArg))
	}
	named = formula.NewSQLite().ContextArgsFor(ctx, p.params, named)
	var v, c any
	if q.Agg != "" {
		var code sql.NullString
		if err := (*stmt).QueryRowContext(context.Background(), named...).Scan(&v, &c, &code); err != nil {
			return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
		}
		if code.Valid {
			return formula.Value{}, &formula.EvalError{Code: code.String, Pos: -1, Msg: q.Agg + " over " + q.Source + ": " + code.String}
		}
		return formula.ValueOfType(v, q.Type, c), nil
	}
	if err := (*stmt).QueryRowContext(context.Background(), named...).Scan(&v, &c); err != nil {
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	return formula.ValueOfType(v, q.Type, c), nil
}

// cacheFor returns the table's rows in memory, building them when the
// table has at most MaxIndexedRows rows; false when it has more.
func (t *table) cacheFor(s *Store) (*tableCache, bool) {
	if t.cache != nil {
		return t.cache, true
	}
	if t.tooBig {
		return nil, false
	}
	rows, over, err := t.scanRows(context.Background(), t.selectAll, s.maxIndexed)
	if err != nil || over {
		t.tooBig = true
		return nil, false
	}
	t.cache = &tableCache{rows: rows, byCol: map[string]map[ikey]int{}}
	return t.cache, true
}

// index returns the hash index of an upper-cased column, built on first
// use: the first row in key order per key.
func (c *tableCache) index(t *table, key string) map[ikey]int {
	if idx, ok := c.byCol[key]; ok {
		return idx
	}
	li := t.leafIndex[key]
	col := t.leaves[li].typ
	idx := make(map[ikey]int, len(c.rows))
	for i := len(c.rows) - 1; i >= 0; i-- {
		idx[canonKey(c.rows[i][li], col)] = i
	}
	c.byCol[key] = idx
	return idx
}
