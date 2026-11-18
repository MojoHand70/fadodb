package model

import (
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// A format says how a value is shown to a person, in the pattern
// language of Excel's number formats (ADR-0068, item 5): "#,##0.00",
// "0%", "d mmm yyyy", "yyyy-mm-dd hh:mm". The engine checks a pattern
// when the class, the table or the schema is defined and never applies
// one: a page does, in the person's language, which gives the separators
// and the names of the months. Only the parts the record lists are read;
// anything else is refused with its place, so that what the engine takes
// is what every page can draw.

// formatLiterals are the characters Excel shows as they are without
// quotes, besides the digits' and the dates' own letters.
const formatLiterals = " $-+/():!^&'~{}<>="

// checkFormat checks the format at path against the type it is declared
// on. A finding's Pos is the byte offset in the pattern.
func checkFormat(c *collector, path, pattern string, t formula.Type) {
	if pattern == "" {
		return
	}
	switch t.Kind {
	case formula.KindNumber, formula.KindDecimal, formula.KindMoney:
		if pos, msg := numberFormat(pattern); msg != "" {
			c.list = append(c.list, Diagnostic{Code: CodeBadValue, Path: path, Pos: pos, Message: msg,
				Hint: `a number's format is made of 0, #, the separators , and ., %, text in quotes, and up to three sections split by ;, such as "#,##0.00"`})
		}
	case formula.KindDate, formula.KindDateTime:
		if pos, msg := dateFormat(pattern, t.Kind == formula.KindDateTime); msg != "" {
			c.list = append(c.list, Diagnostic{Code: CodeBadValue, Path: path, Pos: pos, Message: msg,
				Hint: `a date's format is made of d, m and y, a moment's also of h, mm, ss and AM/PM, with text in quotes, such as "d mmm yyyy" or "yyyy-mm-dd hh:mm"`})
		}
	case formula.KindNull:
		// The type is not known: a computed variable whose formula
		// failed, which has its own finding.
	default:
		c.hint(CodeNotAllowed, path, "a format is for a number, a decimal, an int, a money, a date or a datetime, and this is "+t.String(),
			"leave format out")
	}
}

// numberFormat reads a number's pattern: one to three sections, for the
// positive, the negative and the zero, each of digit places, the two
// separators, a percent sign, text in quotes and the characters Excel
// shows as they are. It returns the offset of the first thing it does not
// read and why, or "" when it reads the whole pattern.
func numberFormat(p string) (int, string) {
	section, start := 0, 0
	digits, point := false, false
	for i := 0; i < len(p); i++ {
		ch := p[i]
		switch {
		case ch == '"':
			end := strings.IndexByte(p[i+1:], '"')
			if end < 0 {
				return i, "the text in quotes is not closed"
			}
			i += end + 1
		case ch == '0' || ch == '#':
			digits = true
		case ch == ',':
			// Between digit places only: Excel reads a comma after the
			// last of them as "in thousands", which the engine does not.
			if !digits || i+1 == len(p) || (p[i+1] != '0' && p[i+1] != '#') {
				return i, "a thousands separator stands between digit places, as in #,##0"
			}
		case ch == '.':
			if point {
				return i, "a section has one decimal point"
			}
			point = true
		case ch == '%':
		case ch == ';':
			if section == 0 && !digits {
				return start, "the first section has no digit place, 0 or #"
			}
			section++
			if section > 2 {
				return i, "a format has at most three sections: positive, negative and zero"
			}
			start, digits, point = i+1, false, false
		case ch == '[':
			return i, "colours and conditions in brackets are not part of the formats the engine reads"
		case ch == 'E' || ch == 'e':
			return i, "the scientific notation is not part of the formats the engine reads"
		case strings.IndexByte(formatLiterals, ch) >= 0:
		default:
			return i, "the engine does not read " + quote(string(p[i:i+runeLen(p[i:])])) + " in a number's format; put text in quotes"
		}
	}
	if section == 0 && !digits {
		return start, "the format has no digit place, 0 or #"
	}
	return 0, ""
}

// dateFormat reads a date's or a moment's pattern: runs of d (one to
// four), m (one to four; minutes after an hour), yy or yyyy, and for a
// moment h or hh, ss and AM/PM, with text in quotes and the characters
// Excel shows as they are, the comma and the point among them.
func dateFormat(p string, moment bool) (int, string) {
	any := false
	afterHour := false
	for i := 0; i < len(p); {
		ch := p[i]
		lower := ch | 0x20
		n := 1
		for i+n < len(p) && p[i+n]|0x20 == lower && lower >= 'a' && lower <= 'z' {
			n++
		}
		switch {
		case ch == '"':
			end := strings.IndexByte(p[i+1:], '"')
			if end < 0 {
				return i, "the text in quotes is not closed"
			}
			i += end + 2
			continue
		case strings.EqualFold(p[i:min(len(p), i+5)], "AM/PM"):
			if !moment {
				return i, "a date has no time of day"
			}
			i += 5
			continue
		case lower == 'd':
			if n > 4 {
				return i, "a day is d, dd, ddd or dddd"
			}
			afterHour = false
		case lower == 'm':
			if afterHour {
				if n > 2 {
					return i, "minutes after an hour are m or mm"
				}
			} else if n > 4 {
				return i, "a month is m, mm, mmm or mmmm"
			}
		case lower == 'y':
			if n != 2 && n != 4 {
				return i, "a year is yy or yyyy"
			}
			afterHour = false
		case lower == 'h':
			if !moment {
				return i, "a date has no time of day"
			}
			if n > 2 {
				return i, "an hour is h or hh"
			}
			afterHour = true
		case lower == 's':
			if !moment {
				return i, "a date has no time of day"
			}
			if n != 2 {
				return i, "seconds are ss"
			}
		case ch == '.' || ch == ',' || strings.IndexByte(formatLiterals, ch) >= 0:
			i++
			continue
		case ch == '[':
			return i, "colours and conditions in brackets are not part of the formats the engine reads"
		default:
			return i, "the engine does not read " + quote(string(p[i:i+runeLen(p[i:])])) + " in a date's format; put text in quotes"
		}
		any = true
		i += n
	}
	if !any {
		return 0, "the format has no part of the date: d, m or y"
	}
	return 0, ""
}

// runeLen is the length in bytes of the first character of s.
func runeLen(s string) int {
	for i := range s {
		if i > 0 {
			return i
		}
	}
	return len(s)
}
