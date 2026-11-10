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
	"github.com/MojoHand70/fadodb/internal/query"
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
	// upper-cased name, exact by its name as declared, without folding.
	vars   []*cvar
	byName map[string]*cvar
	exact  map[string]*cvar
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
	// files are the File objects among the leaves (ADR-0033).
	files []filePair
	// rights is the rights table resolved per state (ADR-0041): for each
	// state of the class, and "" for the "*" row alone, one cell per
	// variable in declared order; nil when the class declares no rights.
	rights map[string][]rightCell
	// rules are the class's rules by trigger (ADR-0043); nil when it has
	// none, so that a commit of such a class does not look.
	rules *ruleSet
	// triggered are the triggered variables in declaration order, the
	// order they recompute in (ADR-0043); nil when the class has none.
	triggered []*cvar

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
	// asof and errLeaf are the moment leaves of a triggered variable
	// (ADR-0043) and tidx its place among the triggered ones; -1 for any
	// other variable.
	asof, errLeaf, tidx int
}

// leaf is one stored scalar of a row.
type leaf struct {
	path   string // the dotted field path, which is the slot name the binder gives it
	column string
	typ    formula.Type
	v      *model.Variable
	// owner is the variable the column belongs to, by name; moment marks a
	// moment column of a triggered variable, which is no leaf of its
	// value.
	owner, moment string
}

// child is the per-version table of a composite variable.
type child struct {
	v *model.Variable
	// idx is the variable's place among the class's variables.
	idx       int
	def       model.ChildTable
	table     string
	leaves    []leaf
	leafIndex map[string]int
	// fields are the columns of a rows value, or one field "value" for an
	// array.
	fields []formula.Field
	// files are the File objects among a row's leaves (ADR-0033).
	files []filePair

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
	classVersion                       int
	createdAt, updatedAt, stateEntered int64
	validFrom, validTo                 int64
	actor, eventID                     string
	vals                               []formula.Value
	// ats are the times the class's triggered variables were last
	// computed, one per variable of class.triggered and 0 for none: the
	// valid_from of the history row at the version the moment leaf names.
	// It is read with the row and never stored (ADR-0043). nil for a
	// class without triggered variables.
	ats []int64
}

func (r *docRow) clone() *docRow {
	c := *r
	c.vals = append([]formula.Value(nil), r.vals...)
	c.ats = append([]int64(nil), r.ats...)
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
		out = append(out, leaf{path: col.Path, column: col.Name, typ: col.Type, owner: col.Variable, moment: col.Moment})
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
		byName: map[string]*cvar{}, exact: map[string]*cvar{}, slots: map[*formula.Program][]int{}, sources: map[string]bool{}}
	for _, v := range cls.Variables {
		for _, name := range v.Sources {
			c.sources[strings.ToUpper(name)] = true
		}
	}
	c.leaves = leavesOf(cls.Columns)
	c.leafIndex = indexOf(c.leaves)
	for i, v := range cls.Variables {
		cv := &cvar{v: v, idx: i, leaf: -1, asof: -1, errLeaf: -1, tidx: -1}
		c.vars = append(c.vars, cv)
		c.byName[strings.ToUpper(v.Name)] = cv
		c.exact[v.Name] = cv
		if v.Triggered() {
			cv.tidx = len(c.triggered)
			c.triggered = append(c.triggered, cv)
		}
	}
	for i := range c.leaves {
		l := &c.leaves[i]
		// The moment of a triggered variable is stored beside its value
		// and is no part of it.
		switch l.moment {
		case model.MomentAsOf:
			cv := c.byName[strings.ToUpper(l.owner)]
			l.v, cv.asof = cv.v, i
			continue
		case model.MomentError:
			cv := c.byName[strings.ToUpper(l.owner)]
			l.v, cv.errLeaf = cv.v, i
			continue
		}
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
	for _, v := range cls.Variables {
		if v.Type.Kind == formula.KindObject {
			c.files = append(c.files, filePairs(v.Name, v.Type, c.leafIndex)...)
		}
	}
	for _, ct := range cls.Children {
		cv := c.byName[strings.ToUpper(ct.Variable)]
		ch := &child{v: cv.v, idx: cv.idx, def: ct, table: "doc_" + cls.Name + "__" + ct.Variable}
		ch.leaves = leavesOf(ct.Columns)
		for i := range ch.leaves {
			ch.leaves[i].v = cv.v
		}
		ch.leafIndex = indexOf(ch.leaves)
		if cv.v.Type.Kind == formula.KindArray {
			ch.fields = []formula.Field{{Name: "value", Type: formula.Type{Kind: cv.v.Type.Elem, Scale: cv.v.Type.Scale}}}
		} else {
			ch.fields = cv.v.Type.Fields()
			ch.files = filePairs("", cv.v.Type, ch.leafIndex)
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
	for i, t := range cls.Transitions {
		if t.Guard == nil {
			continue
		}
		owner := "transition " + t.Name
		if t.Name == "" {
			owner = "transitions/" + itoa(i)
		}
		if err := c.bindSlots(t.Guard, owner); err != nil {
			return nil, err
		}
	}
	// The transition rules and the rights (ADR-0041): their slots bound,
	// and the rights table resolved per state once, so that a projection
	// and a write check look nothing up.
	if cls.Access != nil {
		for name, prog := range cls.Access.Transition {
			if err := c.bindSlots(prog, "access/transition/"+name); err != nil {
				return nil, err
			}
		}
	}
	if cls.Rights != nil {
		for st, row := range cls.Rights {
			for name, r := range row {
				for kind, rule := range map[string]*model.RightRule{"read": r.Read, "write": r.Write} {
					if rule != nil && rule.Formula != nil {
						if err := c.bindSlots(rule.Formula, "rights/"+st+"/"+name+"/"+kind); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		c.rights = map[string][]rightCell{}
		states := []string{""}
		if cls.States != nil {
			states = append(states, cls.States.List...)
		}
		for _, st := range states {
			cells := make([]rightCell, len(c.vars))
			for i, cv := range c.vars {
				cells[i] = rightCell{read: cls.ReadRule(st, cv.v.Name), write: cls.WriteRule(st, cv.v.Name)}
			}
			c.rights[st] = cells
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
	if err := c.layoutRules(); err != nil {
		return nil, err
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
	// The reads take the time of each triggered variable's moment with the
	// row; the writes store the two moment columns alone.
	historySelect := strings.Join(append(append([]string{}, history...), c.atExprs(c.history)...), ", ")
	selectCurrent := "SELECT " + strings.Join(append(append([]string{}, current...), c.atExprs(c.table)...), ", ") + " FROM " + quote(c.table) + ` WHERE "id" = ?`
	prepW(&c.w.selectCurrent, selectCurrent)
	prepW(&c.w.upsert, "INSERT INTO "+quote(c.table)+" ("+strings.Join(current, ", ")+") VALUES ("+marks(len(current))+`) ON CONFLICT ("id") DO UPDATE SET `+strings.Join(sets, ", "))
	prepW(&c.w.closeHistory, "UPDATE "+quote(c.history)+` SET "valid_to" = ? WHERE "id" = ? AND "version" = ?`)
	prepW(&c.w.insertHistory, "INSERT INTO "+quote(c.history)+" ("+strings.Join(history, ", ")+") VALUES ("+marks(len(history))+")")
	prepW(&c.w.deleteCurrent, "DELETE FROM "+quote(c.table)+` WHERE "id" = ?`)
	prepR(&c.r.getCurrent, selectCurrent)
	prepR(&c.r.getVersion, "SELECT "+historySelect+" FROM "+quote(c.history)+` WHERE "id" = ? AND "version" = ?`)
	prepR(&c.r.listHistory, "SELECT "+historySelect+" FROM "+quote(c.history)+` WHERE "id" = ? ORDER BY "version"`)
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

// atExprs are, for each triggered variable in order, the expression that
// gives the time of its moment over a row of the table, the current or
// the history one: the valid_from of the history row at the version the
// value was computed in, null when it never was (ADR-0043).
func (c *class) atExprs(table string) []string {
	out := make([]string, len(c.triggered))
	for i, cv := range c.triggered {
		out[i] = `(SELECT m."valid_from" FROM ` + quote(c.history) + ` m WHERE m."id" = ` + quote(table) + `."id" AND m."version" = ` + quote(table) + "." + quote(c.leaves[cv.asof].column) + ")"
	}
	return out
}

// atNames are the columns the views give those times under:
// <variable>__at.
func (c *class) atNames() []string {
	out := make([]string, len(c.triggered))
	for i, cv := range c.triggered {
		out[i] = model.MomentColumn(cv.v.Name, "at")
	}
	return out
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
		diags = append(diags, s.redefinitionDiagnostics(ctx, old, cls)...)
		diags = append(diags, s.brokenRules(cls, nil)...)
	}
	return cls, diags, version
}

// DefineClass compiles a class definition, creates its tables and views
// and stores the definition. Defining the same definition again is a
// no-op. A redefinition is the next version of the class (ADR-0039): it
// adds the columns and tables of new variables, drops and remakes the
// columns of a variable that changed type with no value anywhere, keeps
// the columns of removed variables for the history, remakes the views,
// and touches no document; every earlier version stays compiled so that
// a document is read under the version that computed it. What a
// redefinition may not do against the documents is in
// redefinitionDiagnostics. A class with no history at all is dropped
// and made again.
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
	fresh := old == nil
	if old != nil {
		var n int64
		if err := w.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(old.history)).Scan(&n); err != nil {
			return rollback(storageErr("count the history", err))
		}
		fresh = n == 0
	}
	// The names the new version needs must be free: every table and view
	// of a fresh class, the views and the new child tables otherwise.
	names := c.viewNames()
	if fresh {
		names = append(c.tableNames(), names...)
	} else {
		for _, ch := range c.children {
			if _, had := old.childOfName(ch.v.Name); !had {
				names = append(names, ch.table)
			}
		}
	}
	if old != nil {
		for _, ddl := range old.dropViewDDL() {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return rollback(storageErr("drop the views of "+old.cls.Name, err))
			}
		}
	}
	for _, name := range names {
		var n int64
		if err := w.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = ? COLLATE NOCASE", name).Scan(&n); err != nil {
			return rollback(storageErr("check the names", err))
		}
		if n > 0 && !(fresh && old != nil && isOwn(old, name)) {
			return rollback(hintf(CodeDuplicate, "", "the name "+name+" is taken by another table or view", "rename the class or the variable"))
		}
	}
	switch {
	case fresh && old != nil:
		for _, ddl := range old.dropDDL() {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return rollback(storageErr("drop the tables of "+old.cls.Name, err))
			}
		}
	case old != nil:
		for _, ddl := range alterDDL(old, c) {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return rollback(storageErr("alter the tables of "+old.cls.Name, err))
			}
		}
	}
	ddl := c.viewDDL()
	if fresh {
		ddl = c.createDDL()
	}
	for _, d := range ddl {
		if _, err := w.ExecContext(ctx, d); err != nil {
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
		s.closePlans()
		s.filters.clear()
		query.Forget(old.cls)
		if fresh {
			// Nothing was ever written under the old versions: forget them.
			for v, oc := range s.versions[strings.ToUpper(cls.Name)] {
				oc.close()
				delete(s.versions[strings.ToUpper(cls.Name)], v)
			}
		} else {
			// The old version's statements may name columns a type change
			// dropped; it is prepared again and stays as the reader of
			// the documents it computed.
			old.close()
			if err := old.prepare(ctx, w, s.db); err != nil {
				return nil, err
			}
		}
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

// isOwn reports whether a name is one of the class's own tables, which a
// fresh redefinition drops before it creates them again.
func isOwn(c *class, name string) bool {
	for _, t := range c.tableNames() {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// childOfName finds a composite's table by variable name.
func (c *class) childOfName(name string) (*child, bool) {
	for _, ch := range c.children {
		if strings.EqualFold(ch.v.Name, name) {
			return ch, true
		}
	}
	return nil, false
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
