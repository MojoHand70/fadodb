package formula

import (
	"fmt"
	"strings"
)

// Kind is the kind of a value, static or at run time (ADR-0013).
type Kind uint8

// The kinds. KindNull is the blank: the static type of the BLANK literal
// and the run-time kind of a missing value.
const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindText
	KindDate
	KindRef
	KindJSON
	KindArray
	KindRows
)

var kindNames = [...]string{"blank", "bool", "number", "text", "date", "ref", "json", "array", "rows"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("kind(%d)", k)
}

// IsScalar reports whether the kind is a scalar, blank included.
func (k Kind) IsScalar() bool { return k <= KindJSON }

// Type is a static type: a scalar kind, an array of a scalar kind, or rows
// with a schema.
type Type struct {
	Kind Kind
	// Elem is the element kind of an array.
	Elem Kind
	// Cols is the schema of rows; nil when unknown.
	Cols []Column
}

// Column is one column of a rows type.
type Column struct {
	Name string
	Kind Kind
}

// Scalar makes the type of a scalar kind.
func Scalar(k Kind) Type { return Type{Kind: k} }

// ArrayOf makes the type of an array of a scalar kind.
func ArrayOf(elem Kind) Type { return Type{Kind: KindArray, Elem: elem} }

// IsScalar reports whether the type is a scalar, blank included.
func (t Type) IsScalar() bool { return t.Kind.IsScalar() }

func (t Type) String() string {
	switch t.Kind {
	case KindArray:
		return "array<" + t.Elem.String() + ">"
	case KindRows:
		if t.Cols == nil {
			return "rows"
		}
		parts := make([]string, len(t.Cols))
		for i, c := range t.Cols {
			parts[i] = c.Name + " " + c.Kind.String()
		}
		return "rows<" + strings.Join(parts, ", ") + ">"
	}
	return t.Kind.String()
}

// Equal reports whether two types are the same. Rows compare by schema.
func (t Type) Equal(o Type) bool {
	if t.Kind != o.Kind || t.Elem != o.Elem || len(t.Cols) != len(o.Cols) {
		return false
	}
	for i := range t.Cols {
		if t.Cols[i] != o.Cols[i] {
			return false
		}
	}
	return true
}

var scalarKinds = map[string]Kind{
	"bool": KindBool, "number": KindNumber, "text": KindText, "date": KindDate,
	"ref": KindRef, "json": KindJSON,
}

// ParseType reads a type as written in class definitions and in the
// conformance suite: a scalar name, array<scalar>, or rows. It also reads
// "blank", the static type of a formula that is BLANK, so that every type
// String writes decodes again; a variable is never declared blank, which
// is the declaring side's check.
func ParseType(s string) (Type, error) {
	if k, ok := scalarKinds[s]; ok {
		return Scalar(k), nil
	}
	if s == "blank" {
		return Scalar(KindNull), nil
	}
	if s == "rows" {
		return Type{Kind: KindRows}, nil
	}
	if strings.HasPrefix(s, "array<") && strings.HasSuffix(s, ">") {
		if k, ok := scalarKinds[s[6:len(s)-1]]; ok {
			return ArrayOf(k), nil
		}
	}
	return Type{}, fmt.Errorf("unknown type %q", s)
}
