package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// class is a compiled class with its storage layout and its prepared
// statements.
type class struct {
	cls            *model.Class
	json           []byte
	table, history string
	// leaves are the stored scalars of a current or history row, in
	// column order: one per column, a money's two columns one leaf.
	leaves    []leaf
	leafIndex map[string]int // upper-cased dotted path
	// vars are the variables in declared order; byName finds one by its
	// upper-cased name.
	vars   []*cvar
	byName map[string]*cvar
	// order are the auto variables, and nil for the title, in evaluation
	// order; defaults the inputs with defaults in dependency order.
	order     []*cvar
	defaults  []*cvar
	titleDeps []int
	children  []*child
	// slots maps a program to the leaf of each of its slots.
	slots    map[*formula.Program][]int
	maxSlots int
	// sources are the explicit tables the class's formulas read, by
	// upper-cased name.
	sources map[string]bool

	w writeStmts
	r readStmts
}

// cvar is one variable's place in the layout.
type cvar struct {
	v   *model.Variable
	idx int
	// leaf is the stored scalar of a scalar variable, -1 otherwise;
	// leaves every leaf of a scalar or an object.
	leaf   int
	leaves []int
	// child is the table of a composite variable.
	child *child
	// deps are the variables the formula or the default reads, by index.
	deps []int
}

// leaf is one stored scalar of a row.
type leaf struct {
	path   string // the dotted field path, which is the slot name the binder gives it
	column string
	typ    formula.Type
	v      *model.Variable
}

// child is the per-version table of a composite variable.
type child struct {
	v         *model.Variable
	def       model.ChildTable
	table     string
	leaves    []leaf
	leafIndex map[string]int
	// fields are the columns of a rows value, or one field "value" for an
	// array.
	fields []formula.Field

	insert, copyRows, selectRows *sql.Stmt // on the writer
	readRows                     *sql.Stmt // on the pool
}

type writeStmts struct {
	selectCurrent, upsert, closeHistory, insertHistory, deleteCurrent *sql.Stmt
}

type readStmts struct {
	getCurrent, getVersion, listHistory *sql.Stmt
}

// docRow is a current or history row in memory.
type docRow struct {
	id, title, state                   string
	version                            int64
	createdAt, updatedAt, stateEntered int64
	validFrom, validTo                 int64
	actor, eventID                     string
	vals                               []formula.Value
}

func (r *docRow) clone() *docRow {
	c := *r
	c.vals = append([]formula.Value(nil), r.vals...)
	return &c
}

// childRow is one element of a composite: its leaves in order.
type childRow []formula.Value

// leavesOf turns storage columns into leaves, folding a money's currency
// column into the money leaf before it.
func leavesOf(cols []model.Column) []leaf {
	var out []leaf
	for i, col := range cols {
		if i > 0 && col.Path == cols[i-1].Path && cols[i-1].Type.Kind == formula.KindMoney && strings.HasSuffix(col.Name, formula.CurrencySuffix) {
			continue
		}
		out = append(out, leaf{path: col.Path, column: col.Name, typ: col.Type})
	}
	return out
}

func indexOf(leaves []leaf) map[string]int {
	idx := make(map[string]int, len(leaves))
	for i, l := range leaves {
		idx[strings.ToUpper(l.path)] = i
	}
	return idx
}

// newClass lays a compiled class out for storage.
func newClass(cls *model.Class, src []byte) (*class, error) {
	c := &class{cls: cls, json: src, table: "doc_" + cls.Name, history: "doc_" + cls.Name + "_history",
		byName: map[string]*cvar{}, slots: map[*formula.Program][]int{}, sources: map[string]bool{}}
	for _, v := range cls.Variables {
		for _, name := range v.Sources {
			c.sources[strings.ToUpper(name)] = true
		}
	}
	c.leaves = leavesOf(cls.Columns)
	c.leafIndex = indexOf(c.leaves)
	for i, v := range cls.Variables {
		cv := &cvar{v: v, idx: i, leaf: -1}
		c.vars = append(c.vars, cv)
		c.byName[strings.ToUpper(v.Name)] = cv
	}
	for i := range c.leaves {
		l := &c.leaves[i]
		name := l.path
		if j := strings.IndexByte(name, '.'); j >= 0 {
			name = name[:j]
		}
		cv := c.byName[strings.ToUpper(name)]
		l.v = cv.v
		cv.leaves = append(cv.leaves, i)
		if cv.v.Type.Kind != formula.KindObject {
			cv.leaf = i
		}
	}
	for _, ct := range cls.Children {
		cv := c.byName[strings.ToUpper(ct.Variable)]
		ch := &child{v: cv.v, def: ct, table: "doc_" + cls.Name + "__" + ct.Variable}
		ch.leaves = leavesOf(ct.Columns)
		for i := range ch.leaves {
			ch.leaves[i].v = cv.v
		}
		ch.leafIndex = indexOf(ch.leaves)
		if cv.v.Type.Kind == formula.KindArray {
			ch.fields = []formula.Field{{Name: "value", Type: formula.Type{Kind: cv.v.Type.Elem, Scale: cv.v.Type.Scale}}}
		} else {
			ch.fields = cv.v.Type.Fields()
		}
		cv.child = ch
		c.children = append(c.children, ch)
	}
	for _, cv := range c.vars {
		for _, d := range cv.v.Deps {
			cv.deps = append(cv.deps, c.byName[strings.ToUpper(d)].idx)
		}
		for _, p := range []*formula.Program{cv.v.Default, cv.v.Formula} {
			if p != nil {
				if err := c.bindSlots(p, cv.v.Name); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, name := range cls.Order {
		if name == "title" {
			c.order = append(c.order, nil)
			continue
		}
		c.order = append(c.order, c.byName[strings.ToUpper(name)])
	}
	for _, name := range cls.DefaultOrder {
		c.defaults = append(c.defaults, c.byName[strings.ToUpper(name)])
	}
	if cls.Title != nil {
		if err := c.bindSlots(cls.Title, "title"); err != nil {
			return nil, err
		}
		seen := map[int]bool{}
		for _, s := range cls.Title.Slots {
			name := s.Name
			if j := strings.IndexByte(name, '.'); j >= 0 {
				name = name[:j]
			}
			idx := c.byName[strings.ToUpper(name)].idx
			if !seen[idx] {
				seen[idx] = true
				c.titleDeps = append(c.titleDeps, idx)
			}
		}
	}
	return c, nil
}

// bindSlots resolves a program's slots to leaves.
func (c *class) bindSlots(p *formula.Program, owner string) error {
	idx := make([]int, len(p.Slots))
	for i, s := range p.Slots {
		li, ok := c.leafIndex[strings.ToUpper(s.Name)]
		if !ok {
			return hintf(CodeNotSupported, owner, "the formula reads "+s.Name+", which is not a stored scalar", "only scalar variables and object fields can be read in this build")
		}
		idx[i] = li
	}
	c.slots[p] = idx
	c.maxSlots = max(c.maxSlots, len(idx))
	return nil
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// prepare prepares the class's statements: the writer's on its
// connection, the readers' on the pool.
func (c *class) prepare(ctx context.Context, writer *sql.Conn, db *sql.DB) error {
	cols := quoteAll(columnNames(c.cls.Columns))
	current := append(quoteAll(systemColumns), cols...)
	history := append(quoteAll(append(append([]string{}, systemColumns...), historyColumns...)), cols...)
	sets := make([]string, 0, len(current))
	for _, col := range current[1:] {
		sets = append(sets, col+" = excluded."+col)
	}
	var err error
	prepW := func(dst **sql.Stmt, text string) {
		if err == nil {
			*dst, err = writer.PrepareContext(ctx, text)
		}
	}
	prepR := func(dst **sql.Stmt, text string) {
		if err == nil {
			*dst, err = db.PrepareContext(ctx, text)
		}
	}
	selectCurrent := "SELECT " + strings.Join(current, ", ") + " FROM " + quote(c.table) + ` WHERE "id" = ?`
	prepW(&c.w.selectCurrent, selectCurrent)
	prepW(&c.w.upsert, "INSERT INTO "+quote(c.table)+" ("+strings.Join(current, ", ")+") VALUES ("+marks(len(current))+`) ON CONFLICT ("id") DO UPDATE SET `+strings.Join(sets, ", "))
	prepW(&c.w.closeHistory, "UPDATE "+quote(c.history)+` SET "valid_to" = ? WHERE "id" = ? AND "version" = ?`)
	prepW(&c.w.insertHistory, "INSERT INTO "+quote(c.history)+" ("+strings.Join(history, ", ")+") VALUES ("+marks(len(history))+")")
	prepW(&c.w.deleteCurrent, "DELETE FROM "+quote(c.table)+` WHERE "id" = ?`)
	prepR(&c.r.getCurrent, selectCurrent)
	prepR(&c.r.getVersion, "SELECT "+strings.Join(history, ", ")+" FROM "+quote(c.history)+` WHERE "id" = ? AND "version" = ?`)
	prepR(&c.r.listHistory, "SELECT "+strings.Join(history, ", ")+" FROM "+quote(c.history)+` WHERE "id" = ? ORDER BY "version"`)
	for _, ch := range c.children {
		ccols := quoteAll(columnNames(ch.def.Columns))
		all := append(quoteAll(childKey), ccols...)
		prepW(&ch.insert, "INSERT INTO "+quote(ch.table)+" ("+strings.Join(all, ", ")+") VALUES ("+marks(len(all))+")")
		prepW(&ch.copyRows, "INSERT INTO "+quote(ch.table)+" ("+strings.Join(all, ", ")+`) SELECT "doc_id", ?, "idx"`+joinWithPrefix(ccols)+" FROM "+quote(ch.table)+` WHERE "doc_id" = ? AND "version" = ?`)
		selectRows := "SELECT " + strings.Join(ccols, ", ") + " FROM " + quote(ch.table) + ` WHERE "doc_id" = ? AND "version" = ? ORDER BY "idx"`
		prepW(&ch.selectRows, selectRows)
		prepR(&ch.readRows, selectRows)
	}
	if err != nil {
		c.close()
		return storageErr("prepare the statements of "+c.cls.Name, err)
	}
	return nil
}

func joinWithPrefix(cols []string) string {
	if len(cols) == 0 {
		return ""
	}
	return ", " + strings.Join(cols, ", ")
}

func (c *class) close() {
	for _, st := range []*sql.Stmt{c.w.selectCurrent, c.w.upsert, c.w.closeHistory, c.w.insertHistory, c.w.deleteCurrent, c.r.getCurrent, c.r.getVersion, c.r.listHistory} {
		if st != nil {
			st.Close()
		}
	}
	for _, ch := range c.children {
		for _, st := range []*sql.Stmt{ch.insert, ch.copyRows, ch.selectRows, ch.readRows} {
			if st != nil {
				st.Close()
			}
		}
	}
}

// Validate compiles a class definition and reports what DefineClass would
// refuse, without storing anything (ADR-0025).
func (s *Store) Validate(data []byte) model.Diagnostics {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	_, diags, _ := s.compileClass(context.Background(), data)
	return diags
}

// compileClass compiles a definition against the stored schemas and
// checks it against the stored class of the same name. It returns the
// version the class would get. The document count reads through the
// pool, so that Validate needs no writer.
func (s *Store) compileClass(ctx context.Context, data []byte) (*model.Class, model.Diagnostics, int) {
	cls, diags := model.DefineIn(data, s.env())
	if cls == nil {
		return nil, diags, 0
	}
	version := cls.Version
	if old, ok := s.classes[strings.ToUpper(cls.Name)]; ok {
		if old.cls.Name != cls.Name {
			diags = append(diags, model.Diagnostic{Code: model.CodeDuplicate, Path: "/name", Pos: -1,
				Message: "a class " + old.cls.Name + " exists; names differ in case only"})
		}
		version = old.cls.Version + 1
		if cls.Def.Version != 0 && cls.Def.Version != version {
			diags = append(diags, model.Diagnostic{Code: model.CodeBadValue, Path: "/version", Pos: -1,
				Message: "the stored class is at version " + itoa(old.cls.Version) + "; a redefinition is version " + itoa(version),
				Hint:    "omit version, or write " + itoa(version)})
		}
		var n int64
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(old.history)).Scan(&n); err == nil && n > 0 {
			diags = append(diags, model.Diagnostic{Code: model.CodeNotSupported, Path: "/name", Pos: -1,
				Message: "the class has documents; redefining a class under live documents is not supported yet",
				Hint:    "a class with no documents can be redefined; migrations are a later decision"})
		}
	}
	return cls, diags, version
}

// DefineClass compiles a class definition, creates its tables and views
// and stores the definition. Defining the same definition again is a
// no-op. A class with no documents can be redefined, which drops its empty
// tables and makes the next version; a class with documents cannot be
// redefined in this build.
func (s *Store) DefineClass(ctx context.Context, data []byte) (*model.Class, error) {
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
	if old, ok := s.classes[upperName(data)]; ok && bytes.Equal(old.json, compact) {
		return old.cls, nil
	}
	cls, diags, version := s.compileClass(ctx, data)
	if len(diags) > 0 {
		return nil, diags
	}
	cls.Version = version
	c, err := newClass(cls, compact)
	if err != nil {
		return nil, err
	}
	old := s.classes[strings.ToUpper(cls.Name)]
	now := s.now().Unix()
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	rollback := func(err error) (*model.Class, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return nil, err
	}
	if old != nil {
		for _, ddl := range old.dropDDL() {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return rollback(storageErr("drop the tables of "+old.cls.Name, err))
			}
		}
	}
	for _, name := range append(c.tableNames(), c.viewNames()...) {
		var n int64
		if err := w.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = ? COLLATE NOCASE", name).Scan(&n); err != nil {
			return rollback(storageErr("check the names", err))
		}
		if n > 0 {
			return rollback(hintf(CodeDuplicate, "", "the name "+name+" is taken by another table or view", "rename the class or the variable"))
		}
	}
	for _, ddl := range c.createDDL() {
		if _, err := w.ExecContext(ctx, ddl); err != nil {
			return rollback(storageErr("create the tables of "+cls.Name, err))
		}
	}
	if old == nil {
		_, err = w.ExecContext(ctx, "INSERT INTO classes (name, version, json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", cls.Name, version, string(compact), now, now)
	} else {
		_, err = w.ExecContext(ctx, "UPDATE classes SET name = ?, version = ?, json = ?, updated_at = ? WHERE name = ?", cls.Name, version, string(compact), now, old.cls.Name)
	}
	if err != nil {
		return rollback(storageErr("store the class", err))
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO class_versions (class, version, json, created_at) VALUES (?, ?, ?, ?)", cls.Name, version, string(compact), now); err != nil {
		return rollback(storageErr("store the class version", err))
	}
	if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, storageErr("commit", err)
	}
	if old != nil {
		old.close()
		s.closePlans()
	}
	if err := c.prepare(ctx, w, s.db); err != nil {
		// The tables exist and the class is stored; the statements failed,
		// which only a driver fault explains. Leave the class out of the
		// map so that the error is visible on the next definition.
		delete(s.classes, strings.ToUpper(cls.Name))
		return nil, err
	}
	s.register(c)
	return cls, nil
}

// DefineSchema compiles a schema definition and stores it (ADR-0031).
// Defining the same definition again is a no-op; a schema a stored class
// uses cannot be redefined in this build.
func (s *Store) DefineSchema(ctx context.Context, data []byte) (*model.Schema, error) {
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
	def, diags := model.ParseSchema(data)
	if len(diags) > 0 {
		return nil, diags
	}
	key := strings.ToUpper(def.Name)
	if old, ok := s.schemas.Lookup(def.Name); ok && !old.Predefined {
		if bytes.Equal(s.schemaJSON[key], compact) {
			return old, nil
		}
		for _, name := range s.order {
			for _, used := range s.classes[strings.ToUpper(name)].cls.Schemas {
				if strings.ToUpper(used.Name) == key {
					return nil, model.Diagnostics{{Code: model.CodeNotSupported, Path: "/name", Pos: -1,
						Message: "the class " + name + " uses the schema " + old.Name + "; redefining a schema in use is not supported yet",
						Hint:    "define the new shape under another name"}}
				}
			}
		}
	}
	sch, diags := s.schemas.Define(def)
	if len(diags) > 0 {
		return nil, diags
	}
	now := s.now().Unix()
	if _, err := s.writer.ExecContext(ctx, "INSERT INTO schemas (name, version, json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)"+
		" ON CONFLICT (name) DO UPDATE SET name = excluded.name, version = excluded.version, json = excluded.json, updated_at = excluded.updated_at",
		sch.Name, sch.Version, string(compact), now, now); err != nil {
		return nil, storageErr("store the schema", err)
	}
	s.schemaJSON[key] = compact
	return sch, nil
}

func compactJSON(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// upperName reads the name of a definition, upper-cased, without parsing
// the rest.
func upperName(data []byte) string {
	var head struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &head)
	return strings.ToUpper(head.Name)
}

func itoa(i int) string { return strconv.Itoa(i) }
