package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// TableDef is an explicit table as written (ADR-0016, ADR-0037): a name,
// its columns by scalar type, the key columns that order it and identify
// a row, optional secondary indexes, and the storage binding its rows
// live in.
type TableDef struct {
	Name string `json:"name"`
	// App is the app the table belongs to, or none (ADR-0079), as a
	// class names its app (ADR-0077). The store checks that the database
	// has it.
	App         string `json:"app,omitempty"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Columns maps a column name to its type: a scalar type other than
	// object; money is stored as two columns.
	Columns map[string]FieldDef `json:"columns"`
	// Key lists the columns that identify a row and order the table; a
	// LOOKUP returns the first matching row in this order (ADR-0020).
	Key []string `json:"key"`
	// Indexes lists further column sets to index.
	Indexes [][]string `json:"indexes,omitempty"`
	// Binding is sqlite (the default), duckdb or external (ADR-0016).
	Binding string `json:"binding,omitempty"`
	// Access holds the table's two rules (ADR-0051): who may read its
	// rows and who may write them.
	Access *TableAccessDef `json:"access,omitempty"`
	// History says whether the table keeps the history of its rows
	// (ADR-0099, step 3): every row as it was, until a change replaced
	// or removed it. Left out it does; false keeps none, for a table
	// brought in whole that no decision reads.
	History *bool `json:"history,omitempty"`
	// ColumnOrder lists the columns in the order written.
	ColumnOrder []string `json:"-"`
}

// TableAccessDef is the rules of a table: each a condition over the
// caller and the clock, about the table as a whole.
type TableAccessDef struct {
	Read  string `json:"read,omitempty"`
	Write string `json:"write,omitempty"`
}

// Table is a compiled table definition.
type Table struct {
	// Defined says who defined this version of the table, and when
	// (ADR-0052, item 7); the store sets it.
	Defined Defined
	Name    string
	App     string // the app it belongs to, or none (ADR-0079)
	// Roles are the roles of that app its rules ask for with INROLE
	// (ADR-0100).
	Roles       []string
	Version     int
	Label       string
	Description string
	Binding     string
	// History says the table keeps the history of its rows (ADR-0099,
	// step 3): true unless the definition says "history": false.
	History bool
	// Fields are the logical columns in order; Columns the storage
	// columns, a money field as two.
	Fields  []formula.Field
	Columns []Column
	Key     []string
	Indexes [][]string
	// Read and Write are the compiled rules of the table (ADR-0051), nil
	// for a rule the definition leaves out.
	Read, Write *formula.Program
	Def         *TableDef
}

// Source is the table as the binder sees it.
func (t *Table) Source() *formula.Source {
	src := &formula.Source{Name: t.Name, Columns: t.Fields, Key: t.Key, Binding: t.Binding}
	// The key and every index are led by their first column.
	if len(t.Key) > 0 {
		src.Indexed = append(src.Indexed, t.Key[0])
	}
	for _, idx := range t.Indexes {
		if len(idx) > 0 {
			src.Indexed = append(src.Indexed, idx[0])
		}
	}
	return src
}

// Field finds a logical column by name, case-insensitively.
func (t *Table) Field(name string) (formula.Field, bool) {
	for _, f := range t.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return formula.Field{}, false
}

// ParseTable decodes a table definition. Unknown properties are findings.
func ParseTable(data []byte) (*TableDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def TableDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	if raw, ok := top["columns"]; ok {
		var err error
		if def.ColumnOrder, err = orderedKeys(raw); err != nil {
			c.add(CodeBadJSON, "/columns", err.Error())
		}
	}
	return &def, c.list
}

// The bindings the engine knows by name, and the one it stores rows in.
// The rows of every table live in the store (ADR-0094): a program with
// the analytical engine keeps a copy of them by itself, and a table of
// another database is brought in from outside.
var (
	knownBindings     = map[string]bool{"sqlite": true, "duckdb": true, "external": true}
	supportedBindings = map[string]bool{"sqlite": true}
)

// CompileTable checks a table definition and lays it out.
func CompileTable(def *TableDef) (*Table, Diagnostics) {
	c := &collector{}
	t := &Table{Name: def.Name, App: def.App, Version: def.Version, Label: def.Label, Description: def.Description, Binding: def.Binding, History: def.History == nil || *def.History, Def: def}
	if c.checkName("/name", def.Name, reservedClassNames) {
		if lower := strings.ToLower(def.Name); strings.HasPrefix(lower, "doc_") || strings.HasPrefix(lower, "tbl_") {
			c.add(CodeReservedName, "/name", "a table name may not start with doc_ or tbl_, the prefixes of the generated tables")
		}
	}
	if t.Version == 0 {
		t.Version = 1
	} else if t.Version < 0 {
		c.add(CodeBadValue, "/version", "the version is a positive number")
	}
	switch t.Binding {
	case "":
		t.Binding = "sqlite"
	default:
		if !knownBindings[t.Binding] {
			c.hint(CodeBadValue, "/binding", "unknown binding "+quote(t.Binding), "one of sqlite, duckdb, external")
		} else if t.Binding == "duckdb" {
			c.hint(CodeNotSupported, "/binding", "the duckdb binding is not built: the rows of a table are kept in the database itself", "leave binding out; a program with the analytical engine keeps a copy of the rows by itself")
		} else if !supportedBindings[t.Binding] {
			c.hint(CodeNotSupported, "/binding", "the "+t.Binding+" binding is not built: the engine reads no table of another database", "leave binding out, and bring the rows in with import_rows")
		}
	}
	checkWords(c, "", def.Label, def.Description)
	order := def.ColumnOrder
	if order == nil {
		for name := range def.Columns {
			order = append(order, name)
		}
	}
	if len(order) == 0 {
		c.add(CodeMissing, "/columns", "a table has at least one column")
	}
	seen := map[string]bool{}
	textType := formula.Scalar(formula.KindText)
	for _, name := range order {
		path := "/columns/" + name
		if !c.checkName(path, name, nil) {
			continue
		}
		if seen[upper(name)] {
			c.addf(CodeDuplicate, path, "column %s is declared twice", quote(name))
			continue
		}
		seen[upper(name)] = true
		typ := def.Columns[name].Type
		if typ == "object" || strings.HasPrefix(typ, "object<") {
			c.add(CodeNotSupported, path, "object columns in tables are not supported yet; one column per field")
			continue
		}
		ft, err := formula.ParseType(typ)
		switch {
		case err != nil:
			c.add(CodeUnknownType, path, err.Error())
			continue
		case ft.Kind == formula.KindNull:
			c.add(CodeUnknownType, path, "a column is never blank by type")
			continue
		case !ft.IsScalar():
			c.add(CodeNotAllowed, path, "a table column is a scalar; "+typ+" is a composite")
			continue
		}
		checkField(c, path, def.Columns[name], ft)
		t.Fields = append(t.Fields, formula.Field{Name: name, Type: ft})
		t.Columns = append(t.Columns, Column{Name: name, Type: ft, Variable: name, Path: name})
		if ft.Kind == formula.KindMoney {
			t.Columns = append(t.Columns, Column{Name: name + formula.CurrencySuffix, Type: textType, Variable: name, Path: name})
		}
	}
	if len(def.Key) == 0 {
		c.hint(CodeMissing, "/key", "a table names the columns of its key", `"key": ["code"]`)
	}
	keySeen := map[string]bool{}
	for i, k := range def.Key {
		path := fmt.Sprintf("/key/%d", i)
		f, ok := t.Field(k)
		switch {
		case !ok:
			c.addf(CodeUnknownVariable, path, "unknown column %s", quote(k))
		case keySeen[upper(k)]:
			c.addf(CodeDuplicate, path, "column %s is in the key twice", quote(k))
		case f.Type.Kind == formula.KindJSON:
			c.add(CodeNotAllowed, path, "a json column cannot be a key")
		default:
			t.Key = append(t.Key, f.Name)
		}
		keySeen[upper(k)] = true
	}
	for i, idx := range def.Indexes {
		path := fmt.Sprintf("/indexes/%d", i)
		if len(idx) == 0 {
			c.add(CodeMissing, path, "an index names at least one column")
			continue
		}
		var cols []string
		ok := true
		for j, k := range idx {
			f, found := t.Field(k)
			if !found {
				c.addf(CodeUnknownVariable, fmt.Sprintf("%s/%d", path, j), "unknown column %s", quote(k))
				ok = false
				continue
			}
			cols = append(cols, f.Name)
		}
		if ok {
			t.Indexes = append(t.Indexes, cols)
		}
	}
	if len(t.Columns) > MaxColumns {
		c.addf(CodeTooManyColumns, "/columns", "the table would have %d columns; at most %d", len(t.Columns), MaxColumns)
	}
	// The rules of the table (ADR-0051): conditions over the caller and
	// the clock. They are about the table as a whole, so they read no
	// column, and they read no other table or document.
	if def.Access != nil {
		t.Read = tableRule(c, def.App, "/access/read", def.Access.Read, "a read rule")
		t.Write = tableRule(c, def.App, "/access/write", def.Access.Write, "a write rule")
		for _, prog := range []*formula.Program{t.Read, t.Write} {
			if prog != nil {
				for _, role := range prog.Roles {
					if !slices.Contains(t.Roles, role) {
						t.Roles = append(t.Roles, role)
					}
				}
			}
		}
	}
	if c.failed() {
		return nil, c.list
	}
	return t, nil
}

// noColumns is what a rule of a table binds against: nothing of a row.
var noColumns = func() *formula.Scope {
	scope, _ := formula.NewScope()
	return scope
}()

// tableRule binds a rule of a table. A table of an app may ask for the
// app's roles (ADR-0100); the store holds each name to the roles the app
// names, as it holds the app to the apps the database has.
func tableRule(c *collector, app, path, src, what string) *formula.Program {
	if src == "" {
		return nil
	}
	prog, err := formula.Bind(src, noColumns, formula.BindOptions{Mode: formula.ModeAny, App: app})
	var fe *formula.Error
	if errors.As(err, &fe) && fe.Role {
		c.hint(CodeUnknownRole, path, what+" of a table: "+fe.Msg, "put the table in an app, whose roles its rules may ask for")
		return nil
	}
	if err != nil {
		c.hint(CodeBadFormula, path, what+" of a table: "+err.Error(),
			"it is a condition over the caller and the clock, such as INGROUP(\"treasury\"); it is about the table as a whole and reads no column, no other table and no document")
		return nil
	}
	if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
		c.hint(CodeNotACondition, path, what+" must be a condition, got "+prog.Result.String(), "compare it, for instance <> 0 or <> \"\"")
		return nil
	}
	return prog
}

// OnlyTheApp says whether two definitions of a table differ in nothing
// but the app they name, whether they keep the history of the rows,
// and the version they write: the columns in their order, the key, the
// indexes, the binding, the rules and the words are the same. A table
// moves to another app so (ADR-0079, item 4), whatever it holds, and
// starts or stops keeping its history so (ADR-0099, step 3).
func OnlyTheApp(a, b *TableDef) bool {
	if a == nil || b == nil || !slices.Equal(a.ColumnOrder, b.ColumnOrder) {
		return false
	}
	x, y := *a, *b
	x.App, y.App, x.Version, y.Version = "", "", 0, 0
	x.History, y.History = nil, nil
	l, errL := json.Marshal(x)
	r, errR := json.Marshal(y)
	return errL == nil && errR == nil && bytes.Equal(l, r)
}

// AddsColumns says whether the second definition of a table is the first
// with columns added and nothing else changed (ADR-0080): every column of
// the first is in the second, written the same, the old ones in the same
// order among themselves; the key, the indexes, the binding, the rules
// and the words are the same, so no new column is in the key. The app may
// differ (ADR-0079, item 4). It answers the names of the columns added.
func AddsColumns(a, b *TableDef) ([]string, bool) {
	if a == nil || b == nil || len(b.ColumnOrder) <= len(a.ColumnOrder) {
		return nil, false
	}
	had := make(map[string]bool, len(a.ColumnOrder))
	for _, name := range a.ColumnOrder {
		had[name] = true
	}
	var kept, added []string
	for _, name := range b.ColumnOrder {
		if had[name] {
			kept = append(kept, name)
		} else {
			added = append(added, name)
		}
	}
	if !slices.Equal(kept, a.ColumnOrder) {
		return nil, false
	}
	for _, name := range a.ColumnOrder {
		l, errL := json.Marshal(a.Columns[name])
		r, errR := json.Marshal(b.Columns[name])
		if errL != nil || errR != nil || !bytes.Equal(l, r) {
			return nil, false
		}
	}
	x, y := *a, *b
	x.App, y.App, x.Version, y.Version = "", "", 0, 0
	x.History, y.History = nil, nil
	x.Columns, y.Columns, x.ColumnOrder, y.ColumnOrder = nil, nil, nil, nil
	l, errL := json.Marshal(x)
	r, errR := json.Marshal(y)
	if errL != nil || errR != nil || !bytes.Equal(l, r) {
		return nil, false
	}
	return added, true
}

// DefineTable parses and compiles a table definition in one step.
func DefineTable(data []byte) (*Table, Diagnostics) {
	def, diags := ParseTable(data)
	if def == nil {
		return nil, diags
	}
	t, more := CompileTable(def)
	return t, append(diags, more...)
}
