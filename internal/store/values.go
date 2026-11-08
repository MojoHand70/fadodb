package store

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// MaxElements is the hard cap on the elements of a composite variable
// (ADR-0013).
const MaxElements = 10000

// assign applies values to a row: inputs by name, an object as a whole,
// and the composites into kids. It marks every assigned variable dirty
// and reports whether a stored scalar changed; a composite's change is
// decided by the caller, which holds the previous rows. Values are
// applied in name order, so that the first error is always the same one.
func (c *class) assign(row *docRow, kids map[*child][]childRow, values Values, dirty []bool, limits formula.Limits) (changed bool, err *Error) {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		x := values[name]
		cv, ok := c.byName[strings.ToUpper(name)]
		if !ok {
			return false, errf(CodeUnknownVariable, name, "the class %s has no variable %s", c.cls.Name, name)
		}
		if dirty[cv.idx] {
			return false, errf(CodeDuplicate, name, "the value is given twice; names differ in case only")
		}
		v := cv.v
		if v.Computed {
			return false, hintf(CodeReadOnly, v.Name, "a computed variable takes its value from its formula", "leave it out of the values")
		}
		switch v.Type.Kind {
		case formula.KindRows, formula.KindArray:
			rows, err := cv.child.convert(x, limits)
			if err != nil {
				return false, err
			}
			kids[cv.child] = rows
		case formula.KindObject:
			before := make([]formula.Value, len(cv.leaves))
			for i, li := range cv.leaves {
				before[i] = row.vals[li]
				row.vals[li] = formula.Blank()
			}
			if err := setFields(row.vals, c.leafIndex, v.Name, v.Type.Fields(), x, "", limits); err != nil {
				return false, err
			}
			for i, li := range cv.leaves {
				if !formula.Equal(before[i], row.vals[li]) {
					changed = true
				}
			}
		default:
			val, err := toValue(x, v.Type, v.RefClass, v.Name, limits)
			if err != nil {
				return false, err
			}
			if err := checkStored(v, val); err != nil {
				return false, err
			}
			if !formula.Equal(row.vals[cv.leaf], val) {
				changed = true
			}
			row.vals[cv.leaf] = val
		}
		dirty[cv.idx] = true
	}
	return changed, nil
}

// setFields writes an object value, a map of fields or nil, into the
// leaves below prefix. Nested objects recurse; an unknown field is
// refused. The prefix is the variable for an object variable and "" for
// the row of a rows value.
func setFields(dst []formula.Value, index map[string]int, prefix string, fields []formula.Field, x any, refClass string, limits formula.Limits) *Error {
	if x == nil {
		return nil
	}
	m, ok := asMap(x)
	if !ok {
		return errf(CodeBadValue, prefix, "an object is a map of its fields, got %T", x)
	}
	byName := make(map[string]any, len(m))
	for name, fx := range m {
		byName[strings.ToUpper(name)] = fx
	}
	for _, f := range fields {
		key := strings.ToUpper(f.Name)
		fx, ok := byName[key]
		if !ok {
			continue
		}
		delete(byName, key)
		path := f.Name
		if prefix != "" {
			path = prefix + "." + f.Name
		}
		if f.Type.Kind == formula.KindObject {
			if err := setFields(dst, index, path, f.Type.Fields(), fx, "", limits); err != nil {
				return err
			}
			continue
		}
		val, err := toValue(fx, f.Type, refClass, path, limits)
		if err != nil {
			return err
		}
		dst[index[strings.ToUpper(path)]] = val
	}
	if len(byName) > 0 {
		names := make([]string, 0, len(byName))
		for k := range byName {
			names = append(names, k)
		}
		sort.Strings(names)
		for name := range m {
			if strings.ToUpper(name) == names[0] {
				where := "the schema"
				if prefix != "" {
					where = prefix
				}
				return errf(CodeUnknownVariable, strings.TrimPrefix(prefix+"."+name, "."), "%s has no field %s", where, name)
			}
		}
	}
	return nil
}

// convert reads a composite value: a list of maps for rows, a list of
// scalars for an array, nil for none.
func (ch *child) convert(x any, limits formula.Limits) ([]childRow, *Error) {
	if x == nil {
		return nil, nil
	}
	list, ok := asList(x)
	if !ok {
		return nil, errf(CodeBadValue, ch.v.Name, "%s is a list, got %T", ch.v.Type, x)
	}
	if len(list) > MaxElements {
		return nil, errf(CodeTooManyElements, ch.v.Name, "%d elements; a composite holds at most %d (ADR-0013)", len(list), MaxElements)
	}
	rows := make([]childRow, len(list))
	for i, el := range list {
		row := make(childRow, len(ch.leaves))
		if ch.v.Type.Kind == formula.KindArray {
			val, err := toValue(el, ch.fields[0].Type, "", fmt.Sprintf("%s[%d]", ch.v.Name, i), limits)
			if err != nil {
				return nil, err
			}
			row[0] = val
		} else if err := setFields(row, ch.leafIndex, "", ch.fields, el, "", limits); err != nil {
			err.Variable = fmt.Sprintf("%s[%d].%s", ch.v.Name, i, err.Variable)
			return nil, err
		}
		rows[i] = row
	}
	return rows, nil
}

func asMap(x any) (map[string]any, bool) {
	switch m := x.(type) {
	case map[string]any:
		return m, true
	case Values:
		return m, true
	}
	return nil, false
}

func asList(x any) ([]any, bool) {
	switch l := x.(type) {
	case []any:
		return l, true
	case []map[string]any:
		out := make([]any, len(l))
		for i, m := range l {
			out[i] = m
		}
		return out, true
	case []Values:
		out := make([]any, len(l))
		for i, m := range l {
			out[i] = m
		}
		return out, true
	case []string:
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out, true
	case []float64:
		out := make([]any, len(l))
		for i, f := range l {
			out[i] = f
		}
		return out, true
	}
	return nil, false
}

// toValue reads one scalar in the wire shape (ADR-0018, ADR-0022,
// ADR-0023) as a value of the type.
func toValue(x any, t formula.Type, refClass, name string, limits formula.Limits) (formula.Value, *Error) {
	if x == nil {
		return formula.Blank(), nil
	}
	bad := func(want string) (formula.Value, *Error) {
		return formula.Value{}, errf(CodeBadValue, name, "want %s, got %s", want, describe(x))
	}
	switch t.Kind {
	case formula.KindBool:
		b, ok := x.(bool)
		if !ok {
			return bad("a bool")
		}
		return formula.Bool(b), nil
	case formula.KindNumber:
		f, ok := asFloat(x)
		if !ok {
			return bad("a number")
		}
		return formula.Number(f), nil
	case formula.KindDecimal:
		switch n := x.(type) {
		case string:
			v, e := formula.ParseExact(strings.TrimSpace(n), t)
			if e != nil {
				return formula.Value{}, &Error{Code: CodeBadValue, Variable: name, Message: fmt.Sprintf("want %s as text such as \"12.30\": %s", t, e.Msg)}
			}
			return v, nil
		case json.Number:
			v, e := formula.ParseExact(n.String(), t)
			if e != nil {
				return formula.Value{}, &Error{Code: CodeBadValue, Variable: name, Message: fmt.Sprintf("%s does not fit %s: %s", n, t, e.Msg)}
			}
			return v, nil
		}
		if i, ok := asInt(x); ok {
			v, e := formula.ParseExact(strconv.FormatInt(i, 10), t)
			if e != nil {
				return formula.Value{}, &Error{Code: CodeBadValue, Variable: name, Message: fmt.Sprintf("%d does not fit %s: %s", i, t, e.Msg)}
			}
			return v, nil
		}
		if f, ok := asFloat(x); ok {
			if v, ok := formula.ExactFromFloat(f, t); ok {
				return v, nil
			}
			return formula.Value{}, hintf(CodeBadValue, name, fmt.Sprintf("%v is not exact at %d places", f, t.Scale), "write the value as text, \"12.30\"")
		}
		return bad(t.String() + " as text such as \"12.30\"")
	case formula.KindMoney:
		s, ok := x.(string)
		if !ok {
			return bad(t.String() + ` as text such as "12.30 PLN"`)
		}
		v, e := formula.ParseExact(strings.TrimSpace(s), t)
		if e != nil {
			return formula.Value{}, &Error{Code: CodeBadValue, Variable: name, Message: fmt.Sprintf("want %s as text such as \"12.30 PLN\": %s", t, e.Msg)}
		}
		return v, nil
	case formula.KindText:
		s, ok := x.(string)
		if !ok {
			return bad("text")
		}
		if n := utf8.RuneCountInString(s); n > limits.MaxText {
			return formula.Value{}, errf(CodeOutOfRange, name, "the text has %d code points; the database's limit is %d (ADR-0021)", n, limits.MaxText)
		}
		return formula.Text(s), nil
	case formula.KindDate:
		switch d := x.(type) {
		case string:
			days, err := formula.ParseCaseDay(strings.TrimSpace(d))
			if err != nil {
				return bad("a date as YYYY-MM-DD")
			}
			return formula.Date(days), nil
		case time.Time:
			y, m, dd := d.Date()
			return formula.Day(y, int(m), dd), nil
		}
		return bad("a date as YYYY-MM-DD")
	case formula.KindDateTime:
		switch d := x.(type) {
		case string:
			secs, err := formula.ParseCaseDate(strings.TrimSpace(d))
			if err != nil {
				return bad("a datetime as seconds or YYYY-MM-DD HH:MM:SS")
			}
			return formula.DateTime(secs), nil
		case time.Time:
			return formula.DateTime(d.Unix()), nil
		}
		if i, ok := asInt(x); ok {
			return formula.DateTime(i), nil
		}
		if f, ok := asFloat(x); ok && f == math.Trunc(f) {
			return formula.DateTime(int64(f)), nil
		}
		return bad("a datetime as seconds or YYYY-MM-DD HH:MM:SS")
	case formula.KindRef:
		s, ok := x.(string)
		if !ok {
			return bad(`a ref as "Class:id"`)
		}
		class, id, found := strings.Cut(s, ":")
		if !found || class == "" || id == "" {
			return bad(`a ref as "Class:id"`)
		}
		if refClass != "" && !strings.EqualFold(class, refClass) {
			return formula.Value{}, errf(CodeBadValue, name, "the ref points at %s, not %s", class, refClass)
		}
		return formula.Ref(s), nil
	case formula.KindJSON:
		switch j := x.(type) {
		case string:
			if !json.Valid([]byte(j)) {
				return bad("a json value as its text")
			}
			return formula.JSON(j), nil
		case json.RawMessage:
			if !json.Valid(j) {
				return bad("a json value")
			}
			return formula.JSON(string(j)), nil
		}
		b, err := json.Marshal(x)
		if err != nil {
			return bad("a json value")
		}
		return formula.JSON(string(b)), nil
	}
	return formula.Value{}, errf(CodeNotSupported, name, "values of type %s are not supported", t)
}

func asFloat(x any) (float64, bool) {
	switch n := x.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	if i, ok := asInt(x); ok {
		return float64(i), true
	}
	return 0, false
}

func asInt(x any) (int64, bool) {
	switch n := x.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	}
	return 0, false
}

func describe(x any) string {
	switch v := x.(type) {
	case string:
		return strconv.Quote(v)
	case json.RawMessage:
		return string(v)
	}
	return fmt.Sprintf("%v (%T)", x, x)
}

// checkStored applies the commit-time rules to a scalar variable's value:
// a required input is not blank, an enum value is in the list
// (ADR-0023), a number is within min and max (ADR-0034).
func checkStored(v *model.Variable, val formula.Value) *Error {
	if val.IsBlank() {
		if v.Required {
			return errf(CodeRequired, v.Name, "%s is required", v.Name)
		}
		return nil
	}
	if len(v.Values) > 0 {
		found := false
		for _, allowed := range v.Values {
			if allowed == val.Str() {
				found = true
				break
			}
		}
		if !found {
			return hintf(CodeNotInValues, v.Name, strconv.Quote(val.Str())+" is not one of the values of "+v.Name, "one of "+strings.Join(v.Values, ", "))
		}
	}
	if v.HasMin && compare(val, v.Min) < 0 {
		return errf(CodeOutOfRange, v.Name, "%s is below the minimum %s", formula.Render(val), formula.Render(v.Min))
	}
	if v.HasMax && compare(val, v.Max) > 0 {
		return errf(CodeOutOfRange, v.Name, "%s is above the maximum %s", formula.Render(val), formula.Render(v.Max))
	}
	return nil
}

// compare orders a numeric value against a bound of its type. A money's
// bound is a decimal at the money's scale.
func compare(a, b formula.Value) int {
	if a.Kind().IsExact() {
		switch {
		case a.Mant() < b.Mant():
			return -1
		case a.Mant() > b.Mant():
			return 1
		}
		return 0
	}
	switch {
	case a.Num() < b.Num():
		return -1
	case a.Num() > b.Num():
		return 1
	}
	return 0
}

// encode appends the SQL parameters of a value: nil for blank, an int64
// for a bool, a date, a datetime or a mantissa, a float64 for a number,
// text otherwise; a money is its mantissa and its currency.
func encode(args []any, l leaf, v formula.Value) []any {
	if v.IsBlank() {
		if l.typ.Kind == formula.KindMoney {
			return append(args, nil, nil)
		}
		return append(args, nil)
	}
	switch l.typ.Kind {
	case formula.KindBool:
		if v.Truth() {
			return append(args, int64(1))
		}
		return append(args, int64(0))
	case formula.KindNumber:
		return append(args, v.Num())
	case formula.KindDecimal:
		return append(args, v.Mant())
	case formula.KindMoney:
		return append(args, v.Mant(), v.Currency())
	case formula.KindDate:
		return append(args, v.Days())
	case formula.KindDateTime:
		return append(args, v.Secs())
	}
	return append(args, v.Str())
}

// decode reads a value back from its SQL columns; a money reads two.
func decode(l leaf, raw []any) (formula.Value, []any) {
	x := raw[0]
	rest := raw[1:]
	var cur any
	if l.typ.Kind == formula.KindMoney {
		cur = raw[1]
		rest = raw[2:]
	}
	if x == nil {
		return formula.Blank(), rest
	}
	switch l.typ.Kind {
	case formula.KindBool:
		return formula.Bool(toInt64(x) != 0), rest
	case formula.KindNumber:
		if f, ok := x.(float64); ok {
			return formula.Number(f), rest
		}
		return formula.Number(float64(toInt64(x))), rest
	case formula.KindDecimal:
		return formula.Decimal(toInt64(x), int(l.typ.Scale)), rest
	case formula.KindMoney:
		return formula.Money(toInt64(x), int(l.typ.Scale), toString(cur)), rest
	case formula.KindDate:
		return formula.Date(toInt64(x)), rest
	case formula.KindDateTime:
		return formula.DateTime(toInt64(x)), rest
	case formula.KindRef:
		return formula.Ref(toString(x)), rest
	case formula.KindJSON:
		return formula.JSON(toString(x)), rest
	}
	return formula.Text(toString(x)), rest
}

func toInt64(x any) int64 {
	switch n := x.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	case []byte:
		i, _ := strconv.ParseInt(string(n), 10, 64)
		return i
	}
	return 0
}

func toString(x any) string {
	switch s := x.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case nil:
		return ""
	}
	return fmt.Sprint(x)
}
