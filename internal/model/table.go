package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// TableDef is an explicit table as written (ADR-0016, ADR-0037): a name,
// its columns by scalar type, the key columns that order it and identify
// a row, optional secondary indexes, and the storage binding its rows
// live in.
type TableDef struct {
	Name        string `json:"name"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Columns maps a column name to its type: a scalar type other than
	// object; money is stored as two columns.
	Columns map[string]string `json:"columns"`
	// Key lists the columns that identify a row and order the table; a
	// LOOKUP returns the first matching row in this order (ADR-0020).
	Key []string `json:"key"`
	// Indexes lists further column sets to index.
	Indexes [][]string `json:"indexes,omitempty"`
	// Binding is sqlite (the default), duckdb or external (ADR-0016).
	Binding string `json:"binding,omitempty"`
	// ColumnOrder lists the columns in the order written.
	ColumnOrder []string `json:"-"`
}

// Table is a compiled table definition.
type Table struct {
	Name        string
	Version     int
	Label       string
	Description string
	Binding     string
	// Fields are the logical columns in order; Columns the storage
	// columns, a money field as two.
	Fields  []formula.Field
	Columns []Column
	Key     []string
	Indexes [][]string
	Def     *TableDef
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

// The bindings this build knows, and the ones it stores rows in.
var (
	knownBindings     = map[string]bool{"sqlite": true, "duckdb": true, "external": true}
	supportedBindings = map[string]bool{"sqlite": true}
)

// CompileTable checks a table definition and lays it out.
func CompileTable(def *TableDef) (*Table, Diagnostics) {
	c := &collector{}
	t := &Table{Name: def.Name, Version: def.Version, Label: def.Label, Description: def.Description, Binding: def.Binding, Def: def}
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
		} else if !supportedBindings[t.Binding] {
			c.hint(CodeNotSupported, "/binding", "the "+t.Binding+" binding is not implemented yet", "sqlite is the binding of this build")
		}
	}
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
		typ := def.Columns[name]
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
	if c.failed() {
		return nil, c.list
	}
	return t, nil
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
