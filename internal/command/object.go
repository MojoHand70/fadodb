package command

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/store"
)

// A command's object is read strictly (ADR-0055, item 5): a member the
// command does not know, a member given twice, a member of the wrong
// JSON type and a missing member the command needs are each refused with
// bad_json and the place, so that a caller that misspells is told at
// once instead of having sent a command that does something else.

// The codes the command set adds to the engine's own (ADR-0055, item 7),
// for what cannot happen to a call of the Go API. A door adds its own
// when it is built: no credential, and a request larger than it reads.
const (
	// CodeBadJSON: the object of a command is not JSON or is not an
	// object; or it has a member the command does not take, a member
	// given twice or a member of the wrong JSON type; or it lacks a member
	// the command needs. The path names the member.
	CodeBadJSON = "bad_json"
	// CodeUnknownCommand: no command has that name.
	CodeUnknownCommand = "unknown_command"
	// CodeBadDefinition: a definition was refused. Every finding is
	// beside the error, under diagnostics, each with its own code and its
	// place in the definition.
	CodeBadDefinition = "bad_definition"
	// CodeInternal: the engine failed in a way that is no refusal. The
	// words of the failure stay with the program that runs the engine and
	// are not in the answer.
	CodeInternal = "internal"
)

func badJSON(path, message, hint string) *store.Error {
	return &store.Error{Code: CodeBadJSON, Path: path, Message: message, Hint: hint}
}

// pointer is the JSON pointer of a member of the command's object.
func pointer(name string) string {
	if strings.ContainsAny(name, "~/") {
		name = strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
	}
	return "/" + name
}

// members walks the members of a JSON object that json.Valid accepted,
// in the order written, and hands each name and the bytes of its value,
// which are a part of the object and are not copied. It answers false
// for JSON that is not an object.
func members(object []byte, each func(name string, value []byte) *store.Error) (bool, *store.Error) {
	i := space(object, 0)
	if i >= len(object) || object[i] != '{' {
		return false, nil
	}
	i = space(object, i+1)
	if object[i] == '}' {
		return true, nil
	}
	for {
		end := stringEnd(object, i)
		name := unquote(object[i:end])
		i = space(object, end)
		i = space(object, i+1) // past the colon
		end = valueEnd(object, i)
		if e := each(name, object[i:end]); e != nil {
			return true, e
		}
		i = space(object, end)
		if object[i] == '}' {
			return true, nil
		}
		i = space(object, i+1) // past the comma
	}
}

// elements walks the elements of a JSON array that json.Valid accepted.
func elements(list []byte, each func(i int, value []byte) *store.Error) *store.Error {
	i := space(list, 0)
	i = space(list, i+1) // past the bracket
	if list[i] == ']' {
		return nil
	}
	for n := 0; ; n++ {
		end := valueEnd(list, i)
		if e := each(n, list[i:end]); e != nil {
			return e
		}
		i = space(list, end)
		if list[i] == ']' {
			return nil
		}
		i = space(list, i+1)
	}
}

func space(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	return i
}

// stringEnd is the index past the closing quote of the string at i.
func stringEnd(b []byte, i int) int {
	for i++; b[i] != '"'; i++ {
		if b[i] == '\\' {
			i++
		}
	}
	return i + 1
}

// valueEnd is the index past the value that begins at i.
func valueEnd(b []byte, i int) int {
	switch b[i] {
	case '"':
		return stringEnd(b, i)
	case '{', '[':
		depth := 0
		for ; ; i++ {
			switch b[i] {
			case '"':
				i = stringEnd(b, i) - 1
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
	}
	for i < len(b) && b[i] != ',' && b[i] != '}' && b[i] != ']' && b[i] != ' ' && b[i] != '\n' && b[i] != '\t' && b[i] != '\r' {
		i++
	}
	return i
}

// unquote reads a JSON string. One without an escape and of valid UTF-8
// is its bytes; any other is left to encoding/json.
func unquote(quoted []byte) string {
	inner := quoted[1 : len(quoted)-1]
	for _, c := range inner {
		if c == '\\' {
			return unquoteSlow(quoted)
		}
	}
	if !utf8.Valid(inner) {
		return unquoteSlow(quoted)
	}
	return string(inner)
}

func unquoteSlow(quoted []byte) string {
	var text string
	_ = json.Unmarshal(quoted, &text)
	return text
}

// isNull reports a member given as null, which says nothing: a client
// that writes null for what it leaves out is read as leaving it out.
func isNull(value []byte) bool { return len(value) == 4 && value[0] == 'n' }

// fits reports whether the value is of the JSON type the field takes.
func (k Kind) fits(value []byte) bool {
	switch first := value[0]; k {
	case Text:
		return first == '"'
	case Integer:
		return first == '-' || first >= '0' && first <= '9'
	case Flag:
		return first == 't' || first == 'f'
	case Object, JSON:
		return first == '{'
	case Texts, Objects, Scalars, JSONs:
		return first == '['
	case Moment:
		return first == '"' || first >= '0' && first <= '9'
	}
	return false
}

// text reads a member that is a text, "" when it was not given.
func (c *call) text(name string) string {
	value := c.given(name)
	if value == nil {
		return ""
	}
	return unquote(value)
}

// integer reads a member that is a whole number, 0 when it was not
// given.
func (c *call) integer(name string) (int64, *store.Error) {
	value := c.given(name)
	if value == nil {
		return 0, nil
	}
	n, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return 0, badJSON(pointer(name), name+" is a whole number, got "+string(value), "")
	}
	return n, nil
}

func (c *call) flag(name string) bool {
	value := c.given(name)
	return value != nil && value[0] == 't'
}

// values reads a member that is values by name, in the wire shape of a
// document (ADR-0018); nil when it was not given.
func (c *call) values(name string) (store.Values, *store.Error) {
	value := c.given(name)
	if value == nil {
		return nil, nil
	}
	var out store.Values
	if err := json.Unmarshal(value, &out); err != nil {
		return nil, badJSON(pointer(name), name+" is an object of values by name: "+err.Error(), "")
	}
	return out, nil
}

// texts reads a member that is a list of texts.
func (c *call) texts(name string) ([]string, *store.Error) {
	value := c.given(name)
	if value == nil {
		return nil, nil
	}
	out := []string{}
	e := elements(value, func(i int, element []byte) *store.Error {
		if element[0] != '"' {
			return badJSON(pointer(name)+"/"+strconv.Itoa(i), name+" is a list of texts, and element "+strconv.Itoa(i)+" is "+string(element), "")
		}
		out = append(out, unquote(element))
		return nil
	})
	return out, e
}

// objects reads a member that is a list of objects of values by name.
func (c *call) objects(name string) ([]store.Values, *store.Error) {
	value := c.given(name)
	if value == nil {
		return nil, nil
	}
	out := []store.Values{}
	e := elements(value, func(i int, element []byte) *store.Error {
		var one store.Values
		if element[0] != '{' || json.Unmarshal(element, &one) != nil {
			return badJSON(pointer(name)+"/"+strconv.Itoa(i), name+" is a list of objects, and element "+strconv.Itoa(i)+" is not one", "")
		}
		out = append(out, one)
		return nil
	})
	return out, e
}

// documentValues reads the documents of a create_many: a list of
// objects, each with values and nothing else.
func (c *call) documentValues(name string) ([]store.Values, *store.Error) {
	value := c.given(name)
	if value == nil {
		return nil, nil
	}
	out := []store.Values{}
	e := elements(value, func(i int, element []byte) *store.Error {
		at := pointer(name) + "/" + strconv.Itoa(i)
		var one store.Values
		isObject, e := members(element, func(member string, raw []byte) *store.Error {
			if member != "values" {
				return badJSON(at+pointer(member), "a document of "+name+" has values and nothing else, got "+strconv.Quote(member), `write each as {"values": {…}}`)
			}
			if isNull(raw) {
				return nil
			}
			if raw[0] != '{' || json.Unmarshal(raw, &one) != nil {
				return badJSON(at+"/values", "the values of a document are an object of values by name", "")
			}
			return nil
		})
		if !isObject {
			return badJSON(at, name+" is a list of objects, and element "+strconv.Itoa(i)+" is not one", `write each as {"values": {…}}`)
		}
		if e != nil {
			return e
		}
		out = append(out, one)
		return nil
	})
	return out, e
}

// scalars reads the arguments of a SQL statement: texts, numbers, true,
// false and null. A number written without a fraction or an exponent is
// an integer, so that it can be a LIMIT or be compared with a version.
func (c *call) scalars(name string) ([]any, *store.Error) {
	value := c.given(name)
	if value == nil {
		return nil, nil
	}
	out := []any{}
	e := elements(value, func(i int, element []byte) *store.Error {
		switch first := element[0]; {
		case first == '"':
			out = append(out, unquote(element))
		case first == 't', first == 'f':
			out = append(out, first == 't')
		case first == 'n':
			out = append(out, nil)
		case first == '{', first == '[':
			return badJSON(pointer(name)+"/"+strconv.Itoa(i), "an argument is a text, a number, true, false or null, and argument "+strconv.Itoa(i)+" is neither", "")
		default:
			if n, err := strconv.ParseInt(string(element), 10, 64); err == nil {
				out = append(out, n)
			} else {
				f, _ := strconv.ParseFloat(string(element), 64)
				out = append(out, f)
			}
		}
		return nil
	})
	return out, e
}

// definitions reads a member that is a list of definitions, each a JSON
// object carried as it is.
func (c *call) definitions(name string) ([][]byte, *store.Error) {
	value := c.given(name)
	if value == nil {
		return nil, nil
	}
	out := [][]byte{}
	e := elements(value, func(i int, element []byte) *store.Error {
		if element[0] != '{' {
			return badJSON(pointer(name)+"/"+strconv.Itoa(i), "a definition is a JSON object, and element "+strconv.Itoa(i)+" of "+name+" is not one", "")
		}
		out = append(out, element)
		return nil
	})
	return out, e
}

// moment reads at (ADR-0042): a day, a day and a time in UTC, or seconds
// since the epoch.
func moment(value []byte) (time.Time, *store.Error) {
	if value[0] != '"' {
		secs, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return time.Time{}, badMoment(string(value))
		}
		return time.Unix(secs, 0).UTC(), nil
	}
	text := unquote(value)
	for _, layout := range []string{time.DateOnly, time.DateTime} {
		if t, err := time.Parse(layout, text); err == nil {
			return t, nil
		}
	}
	return time.Time{}, badMoment(strconv.Quote(text))
}

func badMoment(got string) *store.Error {
	return &store.Error{Code: store.CodeBadValue, Path: "/at", Message: "at is a day, a day and a time in UTC, or seconds since the epoch, got " + got,
		Hint: `write "2026-11-13", "2026-11-13 09:30:00" or 1763026200`}
}

// day reads business_date, a calendar day.
func day(value []byte) (time.Time, *store.Error) {
	text := unquote(value)
	t, err := time.Parse(time.DateOnly, text)
	if err != nil {
		return time.Time{}, &store.Error{Code: store.CodeBadValue, Path: "/business_date", Message: "business_date is a calendar day, got " + strconv.Quote(text),
			Hint: `write it as "2026-11-13"`}
	}
	return t, nil
}
