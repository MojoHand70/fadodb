package command

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/store"
)

// A document is written by hand (ADR-0018, ADR-0055): no reflection, one
// buffer. The bytes are the ones encoding/json writes for the same
// document, which a test holds it to, so that an answer written here and
// an answer written by encoding/json, such as a preview with its
// document inside, read the same.

// appendDocument writes a document. It fails as encoding/json does, on a
// number that is not finite or a value of a type JSON has no form for.
func appendDocument(b []byte, d *store.Document) ([]byte, error) {
	if d == nil {
		return append(b, "null"...), nil
	}
	b = append(b, `{"class":`...)
	b = appendString(b, d.Class)
	b = append(b, `,"id":`...)
	b = appendString(b, d.ID)
	b = append(b, `,"title":`...)
	b = appendString(b, d.Title)
	if d.State != "" {
		b = append(b, `,"state":`...)
		b = appendString(b, d.State)
	}
	b = append(b, `,"version":`...)
	b = strconv.AppendInt(b, d.Version, 10)
	b = append(b, `,"class_version":`...)
	b = strconv.AppendInt(b, int64(d.ClassVersion), 10)
	b = append(b, `,"created_at":`...)
	b = strconv.AppendInt(b, d.CreatedAt, 10)
	b = append(b, `,"updated_at":`...)
	b = strconv.AppendInt(b, d.UpdatedAt, 10)
	if d.StateEnteredAt != 0 {
		b = append(b, `,"state_entered_at":`...)
		b = strconv.AppendInt(b, d.StateEnteredAt, 10)
	}
	if d.CreatedBy != "" {
		b = append(b, `,"created_by":`...)
		b = appendString(b, d.CreatedBy)
	}
	if d.UpdatedBy != "" {
		b = append(b, `,"updated_by":`...)
		b = appendString(b, d.UpdatedBy)
	}
	if d.StateEnteredBy != "" {
		b = append(b, `,"state_entered_by":`...)
		b = appendString(b, d.StateEnteredBy)
	}
	if d.StateEnteredFrom != "" {
		b = append(b, `,"state_entered_from":`...)
		b = appendString(b, d.StateEnteredFrom)
	}
	b = append(b, `,"values":`...)
	var err error
	if d.Values == nil {
		b = append(b, "null"...)
	} else if b, err = appendObject(b, d.Values); err != nil {
		return b, err
	}
	if len(d.Computed) > 0 {
		b = append(b, `,"computed":{`...)
		names := takeNames()
		for name := range d.Computed {
			*names = append(*names, name)
		}
		slices.Sort(*names)
		for i, name := range *names {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, name)
			b = append(b, ':', '{')
			m, first := d.Computed[name], true
			if m.Version != 0 {
				b = append(b, `"version":`...)
				b = strconv.AppendInt(b, m.Version, 10)
				first = false
			}
			if m.At != 0 {
				if !first {
					b = append(b, ',')
				}
				b = append(b, `"at":`...)
				b = strconv.AppendInt(b, m.At, 10)
				first = false
			}
			if m.Error != "" {
				if !first {
					b = append(b, ',')
				}
				b = append(b, `"error":`...)
				b = appendString(b, m.Error)
			}
			b = append(b, '}')
		}
		giveNames(names)
		b = append(b, '}')
	}
	return append(b, '}'), nil
}

// names are the keys of an object while they are sorted: encoding/json
// writes the members of a map in the order of their names.
var namesPool = sync.Pool{New: func() any { s := make([]string, 0, 64); return &s }}

func takeNames() *[]string { return namesPool.Get().(*[]string) }

func giveNames(names *[]string) {
	clear(*names)
	*names = (*names)[:0]
	namesPool.Put(names)
}

func appendObject(b []byte, m map[string]any) ([]byte, error) {
	b = append(b, '{')
	names := takeNames()
	defer giveNames(names)
	for name := range m {
		*names = append(*names, name)
	}
	slices.Sort(*names)
	var err error
	for i, name := range *names {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendString(b, name)
		b = append(b, ':')
		if b, err = appendValue(b, m[name]); err != nil {
			return b, err
		}
	}
	return append(b, '}'), nil
}

// appendValue writes a value of a document in the wire shape: the types
// the store answers, and through encoding/json any other.
func appendValue(b []byte, v any) ([]byte, error) {
	switch v := v.(type) {
	case nil:
		return append(b, "null"...), nil
	case string:
		return appendString(b, v), nil
	case float64:
		return appendFloat(b, v)
	case int64:
		return strconv.AppendInt(b, v, 10), nil
	case bool:
		return strconv.AppendBool(b, v), nil
	case map[string]any:
		if v == nil {
			return append(b, "null"...), nil
		}
		return appendObject(b, v)
	case []any:
		if v == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '[')
		var err error
		for i, element := range v {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendValue(b, element); err != nil {
				return b, err
			}
		}
		return append(b, ']'), nil
	}
	// A json value, which encoding/json compacts and escapes, and anything
	// a host program's action put into a document's values.
	text, err := json.Marshal(v)
	if err != nil {
		return b, err
	}
	return append(b, text...), nil
}

// appendFloat writes a number as encoding/json does: the shortest text
// that reads back the same, in exponent form below 1e-6 and from 1e21.
func appendFloat(b []byte, f float64) ([]byte, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return b, &json.UnsupportedValueError{Str: strconv.FormatFloat(f, 'g', -1, 64)}
	}
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	b = strconv.AppendFloat(b, f, format, -1, 64)
	if format == 'e' {
		// e-09 is written e-9.
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b, nil
}

const hex = "0123456789abcdef"

// appendString writes a text as encoding/json does with its HTML
// escaping on: the quote, the backslash and the control characters
// escaped, <, > and & as <, > and &, U+2028 and U+2029 as
// escapes, and a byte that is not UTF-8 as the replacement character, escaped.
func appendString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			if c >= ' ' && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '\\', '"':
				b = append(b, '\\', c)
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', 'f', 'f', 'f', 'd')
			i += size
			start = i
			continue
		}
		if r == 0x2028 || r == 0x2029 {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hex[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}
