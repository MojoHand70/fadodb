package postgres

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// spelled is a statement as Postgres takes it: its text with every ?
// and @name written as $1, $2 and so on, and where each of those gets
// its argument from.
type spelled struct {
	text string
	// from says, for $1, $2 and so on, which argument it is: the one
	// given under a name, or the one at a place among those given
	// without one. It is empty for a statement that has no ? and no
	// @name, whose arguments go on as they are.
	from []source
	// places is how many ? the statement has.
	places int
}

type source struct {
	name  string
	place int
}

// spellings keeps the statements already spelled, by their text: the
// engine sends the same statements over and over. It keeps no more
// than spellingsKept of them and none longer than spellingMax bytes,
// so that a caller who sends statements that are never the same twice
// cannot fill the memory with them: past the count it is emptied and
// begins again.
var (
	spellings sync.Map
	spelt     atomic.Int64
)

const (
	spellingsKept = 2048
	spellingMax   = 16 << 10
)

// spell answers the statement as Postgres takes it.
func spell(query string) *spelled {
	if s, ok := spellings.Load(query); ok {
		return s.(*spelled)
	}
	s := rewrite(query)
	if len(query) <= spellingMax {
		if spelt.Add(1) > spellingsKept {
			spellings.Clear()
			spelt.Store(1)
		}
		spellings.Store(query, s)
	}
	return s
}

// rewrite spells a statement Postgres's way. A ? is the next argument
// given without a name, and an @name the argument of that name: each
// becomes $n, in the order they first stand in the text, and a name
// that stands twice is the same $n both times. What stands in a quoted
// text, a quoted name, a comment or a body between dollar quotes is
// left as it is, and so is a $n the statement has already.
//
// An @ is the start of a name only before a letter or an underscore
// and after something that is no part of a name or of an operator, so
// that the operators @>, <@ and @@ stay operators. Every ? is a
// parameter: the operators of jsonb that are written with one are not
// used by the engine, which calls their functions.
func rewrite(q string) *spelled {
	if !strings.ContainsAny(q, "?@") {
		return &spelled{text: q}
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	s := &spelled{}
	named := map[string]int{}
	param := func(src source) {
		s.from = append(s.from, src)
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(len(s.from)))
	}
	i := 0
	for i < len(q) {
		c := q[i]
		switch {
		case c == '\'':
			// A quoted text; one with E before it takes a backslash
			// as an escape.
			escapes := i > 0 && (q[i-1] == 'E' || q[i-1] == 'e') && (i < 2 || !nameByte(q[i-2]))
			j := i + 1
			for j < len(q) {
				if escapes && q[j] == '\\' && j+1 < len(q) {
					j += 2
					continue
				}
				if q[j] == '\'' {
					if j+1 < len(q) && q[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j+1, len(q))
			b.WriteString(q[i:j])
			i = j
		case c == '"':
			j := i + 1
			for j < len(q) {
				if q[j] == '"' {
					if j+1 < len(q) && q[j+1] == '"' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j+1, len(q))
			b.WriteString(q[i:j])
			i = j
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			j := strings.IndexByte(q[i:], '\n')
			if j < 0 {
				j = len(q)
			} else {
				j += i
			}
			b.WriteString(q[i:j])
			i = j
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			// A comment, which may hold comments.
			depth, j := 1, i+2
			for j < len(q) && depth > 0 {
				switch {
				case q[j] == '/' && j+1 < len(q) && q[j+1] == '*':
					depth++
					j += 2
				case q[j] == '*' && j+1 < len(q) && q[j+1] == '/':
					depth--
					j += 2
				default:
					j++
				}
			}
			b.WriteString(q[i:j])
			i = j
		case c == '$':
			// A body between dollar quotes, $$ or $tag$, to its end. A $
			// inside a name, and one before a digit, is no quote.
			j := i
			if i == 0 || !nameByte(q[i-1]) {
				if tag := dollarTag(q[i:]); tag != "" {
					if end := strings.Index(q[i+len(tag):], tag); end >= 0 {
						j = i + len(tag) + end + len(tag)
					} else {
						j = len(q)
					}
				}
			}
			if j == i {
				j = i + 1
			}
			b.WriteString(q[i:j])
			i = j
		case c == '?':
			param(source{place: s.places})
			s.places++
			i++
		case c == '@' && i+1 < len(q) && nameStart(q[i+1]) && (i == 0 || !nameByte(q[i-1]) && !strings.ContainsRune("+-*/<>=~!@#%^&|`", rune(q[i-1]))):
			j := i + 1
			for j < len(q) && nameByte(q[j]) {
				j++
			}
			name := q[i+1 : j]
			if n, seen := named[name]; seen {
				b.WriteByte('$')
				b.WriteString(strconv.Itoa(n))
			} else {
				param(source{name: name})
				named[name] = len(s.from)
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	s.text = b.String()
	return s
}

func nameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func nameByte(c byte) bool {
	return nameStart(c) || c >= '0' && c <= '9' || c == '$' || c >= 0x80
}

// dollarTag reads the tag that opens a dollar quote at the start of q,
// $$ or $name$, and answers "" where there is none.
func dollarTag(q string) string {
	if len(q) < 2 || q[0] != '$' {
		return ""
	}
	if q[1] == '$' {
		return "$$"
	}
	if !nameStart(q[1]) && q[1] < 0x80 {
		return ""
	}
	for j := 1; j < len(q); j++ {
		if q[j] == '$' {
			return q[:j+1]
		}
		if !nameStart(q[j]) && !(q[j] >= '0' && q[j] <= '9') && q[j] < 0x80 {
			return ""
		}
	}
	return ""
}

// order puts the arguments a caller gave in the places the statement
// has for them. An argument with a name the statement does not use is
// left out; one the statement names and the caller did not give, and a
// wrong number of the ones without a name, is an error.
func (s *spelled) order(args []driver.NamedValue) ([]driver.NamedValue, error) {
	if len(s.from) == 0 {
		// Nothing was spelled: the arguments without a name go on as
		// they are, for a statement written with $n, and the named ones
		// are left out.
		for i, a := range args {
			if a.Name == "" {
				continue
			}
			kept := append([]driver.NamedValue(nil), args[:i]...)
			for _, b := range args[i+1:] {
				if b.Name == "" {
					b.Ordinal = len(kept) + 1
					kept = append(kept, b)
				}
			}
			return kept, nil
		}
		return args, nil
	}
	out := make([]driver.NamedValue, len(s.from))
	unnamed := 0
	for _, a := range args {
		if a.Name == "" {
			unnamed++
		}
	}
	if unnamed != s.places {
		return nil, fmt.Errorf("postgres: the statement has %d ? and was given %d arguments without a name", s.places, unnamed)
	}
	for i, src := range s.from {
		found := false
		place := 0
		for _, a := range args {
			if src.name != "" {
				if a.Name == src.name {
					out[i], found = a, true
					break
				}
				continue
			}
			if a.Name != "" {
				continue
			}
			if place == src.place {
				out[i], found = a, true
				break
			}
			place++
		}
		if !found {
			return nil, fmt.Errorf("postgres: the statement names @%s and was given no argument of that name", src.name)
		}
		out[i].Ordinal = i + 1
		out[i].Name = ""
	}
	return out, nil
}
