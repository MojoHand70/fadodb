package formula

import "strings"

// Source is an explicit table a LOOKUP or QUERY may read (ADR-0016,
// ADR-0020): its columns with their scalar types, the columns whose order
// is its key order, and the binding that says where its rows live.
type Source struct {
	Name    string
	Columns []Field
	// Key lists the columns that order the source. LOOKUP returns the
	// first matching row in this order (ADR-0020).
	Key []string
	// Binding is sqlite, duckdb or external (ADR-0016); "" is the
	// store's default.
	Binding string
	// Class says the source is the documents of a class, not an explicit
	// table (ADR-0046): an auto formula may not read it.
	Class bool
	// Indexed names the columns that lead an index of the source. Among
	// the equalities of a conjunction the binder takes one on such a
	// column for the key, so that the order the author wrote the terms in
	// does not decide what the lookup costs.
	Indexed []string
	// Held says the source is a list the document itself holds, its
	// lines or its tags (ADR-0081, step 2): Name is the variable, the
	// columns are the fields of a row, or the one column "value" of an
	// array, and the rows are in the order of the list.
	Held bool
}

// HeldValue names the one column of a list that is an array: its
// elements.
const HeldValue = "value"

// HeldOrder names what orders the rows of a list a document holds: the
// place of each in the list.
const HeldOrder = "idx"

// HeldTable is the name by which the SQL generator asks for the rows of
// a list the document holds: whoever translates a program gives, for it,
// the elements of that variable of the document the statement is about.
func HeldTable(list string) string { return "THIS." + list }

// indexed reports whether the column leads an index of the source.
func (s *Source) indexed(name string) bool {
	for _, c := range s.Indexed {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

// Column finds a column by name, case-insensitively.
func (s *Source) Column(name string) (Field, bool) {
	for _, f := range s.Columns {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return Field{}, false
}

// ColumnNames lists the columns in order.
func (s *Source) ColumnNames() []string {
	names := make([]string, len(s.Columns))
	for i, f := range s.Columns {
		names[i] = f.Name
	}
	return names
}

// Sources resolves the sources a formula may read, by name.
type Sources interface {
	Source(name string) (*Source, bool)
}

// SourceSet is a Sources over a list, resolved case-insensitively. A nil
// SourceSet has no sources.
type SourceSet struct {
	byKey map[string]*Source
	order []string
}

// NewSourceSet makes a set of the sources.
func NewSourceSet(srcs ...*Source) *SourceSet {
	s := &SourceSet{byKey: map[string]*Source{}}
	for _, src := range srcs {
		s.Add(src)
	}
	return s
}

// Add adds or replaces a source.
func (s *SourceSet) Add(src *Source) {
	key := strings.ToUpper(src.Name)
	if _, exists := s.byKey[key]; !exists {
		s.order = append(s.order, src.Name)
	}
	s.byKey[key] = src
}

// Source implements Sources.
func (s *SourceSet) Source(name string) (*Source, bool) {
	if s == nil {
		return nil, false
	}
	src, ok := s.byKey[strings.ToUpper(name)]
	return src, ok
}

// Names lists the sources in the order they were added.
func (s *SourceSet) Names() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.order...)
}
