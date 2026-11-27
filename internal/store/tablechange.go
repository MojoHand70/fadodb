package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Changing a table that holds rows, or that a class reads (ADR-0084). The
// owner, 2026-11-23: "Changing types and removing columns should check two
// things 1. is data present in the table and there is a way to convert the
// data and 2. is any formula using this column."

// reshape checks a version of a table in use that changes its columns, and
// answers the rows it holds as that version takes them. Its key stays: the
// same columns of the same types. No formula and no rule of a class uses a
// column it changes or removes. Every value of a column whose type it
// changes converts to the new type, as an import of that value into the
// new column would take it: exactly, or not at all.
//
// The history of the rows is carried the same way (ADR-0099, step 3):
// each row of it converts as a row now would, and with each row its
// moment goes too.
func (s *Store) reshape(ctx context.Context, old *table, def *model.Table) ([]carriedRow, model.Diagnostics) {
	var diags model.Diagnostics
	same := len(old.def.Key) == len(def.Key)
	for i := 0; same && i < len(def.Key); i++ {
		of, _ := old.def.Field(old.def.Key[i])
		nf, ok := def.Field(def.Key[i])
		same = ok && strings.EqualFold(of.Name, nf.Name) && of.Type.String() == nf.Type.String()
	}
	if !same {
		return nil, model.Diagnostics{{Code: model.CodeNotSupported, Path: "/key", Pos: -1,
			Message: "the key of a table that holds rows, or that a class reads, does not change: the same columns, of the same types",
			Hint:    "keep the key; a table with another key is a table of another name"}}
	}
	var touched []string
	changed := map[string]formula.Type{}
	for _, f := range old.def.Fields {
		nf, ok := def.Field(f.Name)
		switch {
		case !ok:
			touched = append(touched, f.Name)
		case nf.Type.String() != f.Type.String():
			touched = append(touched, f.Name)
			changed[strings.ToUpper(f.Name)] = f.Type
		}
	}
	// Check 2: what a class finds missing in the table without these
	// columns, it uses.
	if len(touched) > 0 {
		if diags = s.usesOf(def, touched); len(diags) > 0 {
			return nil, diags
		}
	}
	// Check 1: the rows, each as the new version takes it, and the rows
	// of the history.
	rows, err := s.carriedRows(ctx, old)
	if err != nil {
		return nil, model.Diagnostics{{Code: CodeStorage, Path: "/name", Pos: -1, Message: err.Error()}}
	}
	tmp := newTable(def, nil)
	vals := make([]formula.Value, len(tmp.leaves))
	out := make([]carriedRow, 0, len(rows))
	type miss struct {
		n     int
		first string
		past  bool // the first is a row of the history
	}
	bad := map[string]*miss{}
	var badOrder []string
	for _, row := range rows {
		was, _ := getFields(row.leaves, old.leafIndex, "", old.def.Fields)
		next := Values{}
		for _, f := range def.Fields {
			x, had := field(was, f.Name)
			if !had {
				continue
			}
			if from, ok := changed[strings.ToUpper(f.Name)]; ok {
				x = carry(x, from, f.Type)
			}
			next[f.Name] = x
		}
		for j := range vals {
			vals[j] = formula.Blank()
		}
		if e := setFields(vals, tmp.leafIndex, "", def.Fields, next, "", s.limits); e != nil {
			col := e.Variable
			m := bad[strings.ToUpper(col)]
			if m == nil {
				m = &miss{}
				bad[strings.ToUpper(col)] = m
				badOrder = append(badOrder, col)
			}
			if m.n++; m.n == 1 {
				v, _ := field(was, col)
				m.first = fmt.Sprintf("the row %s holds %s", rowKeyText(was, old.def.Key), quoteValue(v))
				if row.until.Valid {
					m.first, m.past = fmt.Sprintf("the row %s held %s, in the history of the rows", rowKeyText(was, old.def.Key), quoteValue(v)), true
				}
			}
			continue
		}
		out = append(out, carriedRow{values: next, since: row.since, until: row.until})
	}
	for _, col := range badOrder {
		m := bad[strings.ToUpper(col)]
		nf, _ := def.Field(col)
		rows := "1 row does not convert"
		if m.n > 1 {
			rows = strconv.Itoa(m.n) + " rows do not convert"
		}
		hint := "change those rows first, or keep the type (ADR-0084)"
		if m.past {
			hint = "the history of the rows keeps what a row held (ADR-0099): keep the type, or define a table of another name"
		}
		diags = append(diags, model.Diagnostic{Code: model.CodeBadValue, Path: "/columns/" + col, Pos: -1,
			Message: fmt.Sprintf("%s to %s: %s, the first: %s", rows, nf.Type, "the values would change or be lost", m.first),
			Hint:    hint})
	}
	return out, diags
}

// usesOf answers a refusal for each class whose formula or rule uses one of
// the columns: compiled against the table without them, the class finds
// something missing it did not miss before.
func (s *Store) usesOf(def *model.Table, touched []string) model.Diagnostics {
	gone := map[string]bool{}
	for _, n := range touched {
		gone[strings.ToUpper(n)] = true
	}
	// the table without them; its indexes are of no use to a formula, and
	// one may name a column that is gone
	probe := *def.Def
	probe.Columns, probe.ColumnOrder, probe.Indexes = map[string]model.FieldDef{}, nil, nil
	for _, n := range def.Def.ColumnOrder {
		if !gone[strings.ToUpper(n)] {
			probe.Columns[n] = def.Def.Columns[n]
			probe.ColumnOrder = append(probe.ColumnOrder, n)
		}
	}
	pt, _ := model.CompileTable(&probe)
	if pt == nil {
		return nil
	}
	env := s.env()
	next := env
	set := formula.NewSourceSet()
	for _, name := range s.tableOrder {
		if strings.EqualFold(name, def.Name) {
			set.Add(pt.Source())
		} else {
			set.Add(s.tables[strings.ToUpper(name)].src)
		}
	}
	next.Sources = set
	key := strings.ToUpper(def.Name)
	var out model.Diagnostics
	for _, name := range s.order {
		c := s.classes[strings.ToUpper(name)]
		if !c.sources[key] && !actsOn(c.cls, def.Name, true) {
			continue
		}
		_, before := model.DefineIn(c.json, env)
		_, after := model.DefineIn(c.json, next)
		had := map[string]bool{}
		for _, d := range before {
			had[d.Code+" "+d.Path] = true
		}
		for _, d := range after {
			if d.Note || had[d.Code+" "+d.Path] {
				continue
			}
			out = append(out, model.Diagnostic{Code: model.CodeNotAllowed, Path: "/columns", Pos: -1,
				Message: "the class " + name + " uses a column this version changes or removes, at " + d.Path + ": " + d.Message,
				Hint:    "change the class first: a column that a formula or a rule uses keeps its type and stays (ADR-0084)"})
			break
		}
	}
	return out
}

// carry turns a value as a table reads it back into what an import into a
// column of another type takes, where the two kinds write it differently:
// a figure as text, text as a figure or a yes or no, anything as text or
// JSON. Whether the result fits the new column is the import's to say.
func carry(x any, from, to formula.Type) any {
	if x == nil {
		return nil
	}
	switch to.Kind {
	case formula.KindText:
		return wireText(x, from)
	case formula.KindJSON:
		b, err := json.Marshal(x)
		if err != nil {
			return x
		}
		return json.RawMessage(b)
	}
	s, isText := x.(string)
	if !isText {
		return x
	}
	switch to.Kind {
	case formula.KindNumber:
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f
		}
	case formula.KindBool:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return s
}

// wireText writes a value as text: a figure as its digits, a moment in UTC
// as YYYY-MM-DD HH:MM:SS, a yes or no as true or false, JSON as its text.
func wireText(x any, from formula.Type) string {
	switch v := x.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		if from.Kind == formula.KindDateTime {
			return time.Unix(int64(v), 0).UTC().Format("2006-01-02 15:04:05")
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		if from.Kind == formula.KindDateTime {
			return time.Unix(v, 0).UTC().Format("2006-01-02 15:04:05")
		}
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	}
	b, err := json.Marshal(x)
	if err != nil {
		return fmt.Sprint(x)
	}
	return string(b)
}

// field finds a value of a row by its column's name, in any case of letters.
func field(row Values, name string) (any, bool) {
	if v, ok := row[name]; ok {
		return v, true
	}
	for k, v := range row {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

// rowKeyText names a row by its key, as "EUR · 2026-11-13".
func rowKeyText(row Values, key []string) string {
	parts := make([]string, len(key))
	for i, k := range key {
		v, _ := field(row, k)
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, " · ")
}

func quoteValue(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
