package formula

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is the kind of a value, static or at run time (ADR-0013, ADR-0022,
// ADR-0023).
type Kind uint8

// The kinds. KindNull is the blank: the static type of the BLANK literal
// and the run-time kind of a missing value. The scalar kinds come first so
// that IsScalar is one comparison.
const (
	KindNull Kind = iota
	KindBool
	KindNumber   // float64, Excel arithmetic (ADR-0004)
	KindDecimal  // an exact number: an int64 mantissa at a scale (ADR-0022)
	KindMoney    // a decimal with a currency (ADR-0022)
	KindText     // UTF-8 text
	KindDate     // a calendar day: days since 1970-01-01 (ADR-0023)
	KindDateTime // an instant: seconds since 1970-01-01 00:00:00 UTC
	KindRef      // a document reference in its Class:id form
	KindJSON     // opaque JSON text
	KindArray
	KindRows
	KindObject // one record of a named schema; static only, never a run-time value (ADR-0031)
)

var kindNames = [...]string{"blank", "bool", "number", "decimal", "money", "text", "date", "datetime", "ref", "json", "array", "rows", "object"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("kind(%d)", k)
}

// IsScalar reports whether the kind is a scalar, blank included.
func (k Kind) IsScalar() bool { return k <= KindJSON }

// IsExact reports whether the kind is an exact number: decimal or money.
func (k Kind) IsExact() bool { return k == KindDecimal || k == KindMoney }

// IsDateLike reports whether the kind is a calendar day or an instant.
func (k Kind) IsDateLike() bool { return k == KindDate || k == KindDateTime }

// Type is a static type: a scalar kind with its scale when it is exact, an
// array of a scalar kind, rows with a schema, or an object of a named
// schema.
type Type struct {
	Kind Kind
	// Scale is the number of digits after the point of a decimal or money
	// type, 0 to MaxScale. For an array it is the element's scale.
	Scale int8
	// Elem is the element kind of an array.
	Elem Kind
	// Cols is the schema of rows; nil when unknown.
	Cols []Column
	// Schema names the schema of an object, for messages and for telling
	// two object types apart; Fields are its fields in declared order,
	// scalars or objects (ADR-0031). The binder resolves a field path to
	// one scalar slot, so no evaluator ever sees an object value.
	Schema string
	Fields []Field
}

// Column is one column of a rows type.
type Column struct {
	Name  string
	Kind  Kind
	Scale int8
}

// Field is one field of an object type.
type Field struct {
	Name string
	Type Type
}

// ObjectType makes the type of an object of a named schema.
func ObjectType(schema string, fields []Field) Type {
	return Type{Kind: KindObject, Schema: schema, Fields: fields}
}

// Field finds a field by name, case-insensitively.
func (t Type) Field(name string) (Field, bool) {
	for _, f := range t.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return Field{}, false
}

// FieldNames lists the field names of an object type, for messages.
func (t Type) FieldNames() []string {
	names := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		names[i] = f.Name
	}
	return names
}

// Scalar makes the type of a scalar kind. A decimal or money kind gets
// scale 0; DecimalType and MoneyType take a scale.
func Scalar(k Kind) Type { return Type{Kind: k} }

// DecimalType makes an exact decimal type at the scale.
func DecimalType(scale int) Type { return Type{Kind: KindDecimal, Scale: int8(scale)} }

// MoneyType makes a money type at the scale.
func MoneyType(scale int) Type { return Type{Kind: KindMoney, Scale: int8(scale)} }

// ArrayOf makes the type of an array of a scalar kind.
func ArrayOf(elem Kind) Type { return Type{Kind: KindArray, Elem: elem} }

// IsScalar reports whether the type is a scalar, blank included.
func (t Type) IsScalar() bool { return t.Kind.IsScalar() }

// IsExact reports whether the type is a decimal or money scalar.
func (t Type) IsExact() bool { return t.Kind.IsExact() }

func (t Type) String() string {
	switch t.Kind {
	case KindDecimal:
		if t.Scale == 0 {
			return "int"
		}
		return "decimal(" + strconv.Itoa(int(t.Scale)) + ")"
	case KindMoney:
		return "money(" + strconv.Itoa(int(t.Scale)) + ")"
	case KindArray:
		return "array<" + Type{Kind: t.Elem, Scale: t.Scale}.String() + ">"
	case KindRows:
		if t.Cols == nil {
			return "rows"
		}
		parts := make([]string, len(t.Cols))
		for i, c := range t.Cols {
			parts[i] = c.Name + " " + Type{Kind: c.Kind, Scale: c.Scale}.String()
		}
		return "rows<" + strings.Join(parts, ", ") + ">"
	case KindObject:
		if t.Schema == "" {
			return "object"
		}
		return "object<" + t.Schema + ">"
	}
	return t.Kind.String()
}

// Equal reports whether two types are the same. Exact types compare by
// scale too; rows compare by schema; objects by schema name and fields.
func (t Type) Equal(o Type) bool {
	if t.Kind != o.Kind || t.Scale != o.Scale || t.Elem != o.Elem || len(t.Cols) != len(o.Cols) ||
		t.Schema != o.Schema || len(t.Fields) != len(o.Fields) {
		return false
	}
	for i := range t.Cols {
		if t.Cols[i] != o.Cols[i] {
			return false
		}
	}
	for i := range t.Fields {
		if t.Fields[i].Name != o.Fields[i].Name || !t.Fields[i].Type.Equal(o.Fields[i].Type) {
			return false
		}
	}
	return true
}

var scalarKinds = map[string]Kind{
	"bool": KindBool, "number": KindNumber, "text": KindText, "date": KindDate, "datetime": KindDateTime,
	"ref": KindRef, "json": KindJSON,
}

// ParseType reads a type as written in class definitions and in the
// conformance suite: a scalar name, `int`, `decimal(s)`, `money(s)`,
// `array<scalar>`, or `rows`. `decimal` alone is `decimal(2)` and `money`
// alone is `money(2)`. It also reads "blank", the static type of a formula
// that is BLANK, so that every type String writes decodes again; a
// variable is never declared blank, which is the declaring side's check.
func ParseType(s string) (Type, error) {
	if k, ok := scalarKinds[s]; ok {
		return Scalar(k), nil
	}
	switch s {
	case "blank":
		return Scalar(KindNull), nil
	case "rows":
		return Type{Kind: KindRows}, nil
	case "int":
		return DecimalType(0), nil
	case "decimal":
		return DecimalType(2), nil
	case "money":
		return MoneyType(2), nil
	}
	if strings.HasPrefix(s, "array<") && strings.HasSuffix(s, ">") {
		elem, err := ParseType(s[6 : len(s)-1])
		if err == nil && elem.IsScalar() && elem.Kind != KindNull {
			return Type{Kind: KindArray, Elem: elem.Kind, Scale: elem.Scale}, nil
		}
		return Type{}, fmt.Errorf("unknown type %q", s)
	}
	for _, p := range []struct {
		prefix string
		kind   Kind
	}{{"decimal(", KindDecimal}, {"money(", KindMoney}} {
		if strings.HasPrefix(s, p.prefix) && strings.HasSuffix(s, ")") {
			n, err := strconv.Atoi(s[len(p.prefix) : len(s)-1])
			if err != nil || n < 0 || n > MaxScale {
				return Type{}, fmt.Errorf("type %q: the scale must be 0 to %d", s, MaxScale)
			}
			return Type{Kind: p.kind, Scale: int8(n)}, nil
		}
	}
	return Type{}, fmt.Errorf("unknown type %q", s)
}
