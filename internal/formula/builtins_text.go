package formula

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text functions. A blank text argument reads as "": Value.str is "" for a
// blank. Positions and lengths count code points and are 1-based, and a
// count past the end of the text reads to the end. Every text a formula
// produces is at most the database's MaxText code points (ADR-0021): a
// function whose result can be longer than its inputs works the length out
// first and stops with out_of_range instead of building it, so a formula
// can never ask for gigabytes. The functions that only shrink or recase
// their input need no check, since the inputs are within the limit. A
// length is checked in bytes first, which bound the code points, and in
// code points only when the bytes exceed the limit, so the usual short
// text costs nothing to check.

// countArg reads a count, length or width argument: truncated, and never
// negative. It stays a float64 so that a huge count cannot overflow an
// int; callers clamp it to what they can use.
func countArg(v Value, what string) (float64, *EvalError) {
	n := math.Trunc(v.num)
	if !(n >= 0) { // negative, or NaN
		return 0, outOfRange(what + " must not be negative")
	}
	return n, nil
}

// clamp converts a non-negative count to an int no larger than limit.
func clamp(n float64, limit int) int {
	if n > float64(limit) {
		return limit
	}
	return int(n)
}

func tooLong() *EvalError { return outOfRange("the result is too long") }

// start0 is the 0-based start for a 1-based position, which is 1 or more,
// clamped to one past the end of r.
func start0(pos float64, r []rune) int { return int(math.Min(pos, float64(len(r)+1))) - 1 }

// textBytes bounds the rendering of a scalar in bytes without rendering
// it: the text itself, or the 32 bytes a number, a bool, a date, a
// datetime, an exact number or a money never exceeds.
func textBytes(v Value) int {
	switch v.kind {
	case KindText, KindRef, KindJSON:
		return len(v.str)
	case KindNull:
		return 0
	}
	return 32
}

// runeLen is the length in code points of a scalar rendered as text.
func runeLen(v Value) int {
	switch v.kind {
	case KindText, KindRef, KindJSON:
		return utf8.RuneCountInString(v.str)
	case KindNull:
		return 0
	}
	return utf8.RuneCountInString(render(v))
}

func fnLeft(_ *VM, args []Value) (Value, *EvalError) {
	n, err := countArg(args[1], "the count")
	if err != nil {
		return Value{}, err
	}
	r := []rune(args[0].str)
	return Text(string(r[:clamp(n, len(r))])), nil
}

func fnRight(_ *VM, args []Value) (Value, *EvalError) {
	n, err := countArg(args[1], "the count")
	if err != nil {
		return Value{}, err
	}
	r := []rune(args[0].str)
	return Text(string(r[len(r)-clamp(n, len(r)):])), nil
}

func fnMid(_ *VM, args []Value) (Value, *EvalError) {
	pos := math.Trunc(args[1].num)
	if !(pos >= 1) {
		return Value{}, outOfRange("the position must be 1 or more")
	}
	r := []rune(args[0].str)
	start := start0(pos, r)
	end := len(r)
	if len(args) > 2 {
		n, err := countArg(args[2], "the length")
		if err != nil {
			return Value{}, err
		}
		end = start + clamp(n, len(r)-start)
	}
	return Text(string(r[start:end])), nil
}

func fnLen(_ *VM, args []Value) (Value, *EvalError) {
	return Number(float64(utf8.RuneCountInString(args[0].str))), nil
}

// fnTrim removes leading and trailing spaces and collapses inner runs of
// spaces to one, as Excel's TRIM does. Only the space character counts.
func fnTrim(_ *VM, args []Value) (Value, *EvalError) {
	var b strings.Builder
	space := true // drop leading spaces
	for _, r := range args[0].str {
		if r == ' ' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return Text(strings.TrimSuffix(b.String(), " ")), nil
}

func textFn(f func(string) string) func(*VM, []Value) (Value, *EvalError) {
	return func(_ *VM, args []Value) (Value, *EvalError) {
		return Text(f(args[0].str)), nil
	}
}

// proper upper-cases the first letter of every word and lower-cases the
// rest; a word starts at a letter that does not follow a letter.
func proper(s string) string {
	var b strings.Builder
	prevLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			if prevLetter {
				b.WriteRune(unicode.ToLower(r))
			} else {
				b.WriteRune(unicode.ToUpper(r))
			}
			prevLetter = true
		} else {
			b.WriteRune(r)
			prevLetter = false
		}
	}
	return b.String()
}

func fnReplace(vm *VM, args []Value) (Value, *EvalError) {
	pos := math.Trunc(args[1].num)
	if !(pos >= 1) {
		return Value{}, outOfRange("the position must be 1 or more")
	}
	n, err := countArg(args[2], "the length")
	if err != nil {
		return Value{}, err
	}
	r := []rune(args[0].str)
	start := start0(pos, r)
	end := start + clamp(n, len(r)-start)
	if len(r)-(end-start)+utf8.RuneCountInString(args[3].str) > vm.maxText() {
		return Value{}, tooLong()
	}
	return Text(string(r[:start]) + args[3].str + string(r[end:])), nil
}

func fnSubstitute(vm *VM, args []Value) (Value, *EvalError) {
	t, old, repl := args[0].str, args[1].str, args[2].str
	if old == "" {
		return Text(t), nil
	}
	if count := strings.Count(t, old); count > 0 && len(t)+count*(len(repl)-len(old)) > vm.maxText() &&
		utf8.RuneCountInString(t)+count*(utf8.RuneCountInString(repl)-utf8.RuneCountInString(old)) > vm.maxText() {
		return Value{}, tooLong()
	}
	return Text(strings.ReplaceAll(t, old, repl)), nil
}

func fnConcat(vm *VM, args []Value) (Value, *EvalError) {
	n := 0
	for _, a := range args {
		n += textBytes(a)
	}
	if n > vm.maxText() {
		n = 0
		for _, a := range args {
			n += runeLen(a)
		}
		if n > vm.maxText() {
			return Value{}, tooLong()
		}
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString(render(a))
	}
	return Text(b.String()), nil
}

func fnRepeat(vm *VM, args []Value) (Value, *EvalError) {
	n, err := countArg(args[1], "the count")
	if err != nil {
		return Value{}, err
	}
	if args[0].str == "" || n == 0 {
		return Text(""), nil
	}
	if max := float64(vm.maxText()); n*float64(len(args[0].str)) > max && n*float64(utf8.RuneCountInString(args[0].str)) > max {
		return Value{}, tooLong()
	}
	return Text(strings.Repeat(args[0].str, int(n))), nil
}

// fnURLEncode makes a text safe for a URL (RFC 3986): the unreserved
// characters, which are the ASCII letters and digits and - . _ ~, stay,
// and every other character becomes its UTF-8 bytes as %XX with
// upper-case hex digits. The length is worked out before the result is
// built (ADR-0021).
func fnURLEncode(vm *VM, args []Value) (Value, *EvalError) {
	s := args[0].str
	n := 0
	for i := 0; i < len(s); i++ {
		if unreserved(s[i]) {
			n++
		} else {
			n += 3
		}
	}
	if n == len(s) {
		return Text(s), nil
	}
	if n > vm.maxText() {
		return Value{}, tooLong()
	}
	const hex = "0123456789ABCDEF"
	b := make([]byte, 0, n)
	for i := 0; i < len(s); i++ {
		if c := s[i]; unreserved(c) {
			b = append(b, c)
		} else {
			b = append(b, '%', hex[c>>4], hex[c&15])
		}
	}
	return Text(string(b)), nil
}

func unreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

func runePos(s string, byteIndex int) float64 {
	if byteIndex < 0 {
		return 0
	}
	return float64(utf8.RuneCountInString(s[:byteIndex]) + 1)
}

func fnFindFirst(_ *VM, args []Value) (Value, *EvalError) {
	return Number(runePos(args[1].str, strings.Index(args[1].str, args[0].str))), nil
}

func fnFindLast(_ *VM, args []Value) (Value, *EvalError) {
	return Number(runePos(args[1].str, strings.LastIndex(args[1].str, args[0].str))), nil
}

func fnPad(vm *VM, args []Value) (Value, *EvalError) {
	w, err := countArg(args[1], "the width")
	if err != nil {
		return Value{}, err
	}
	if w > float64(vm.maxText()) {
		return Value{}, tooLong()
	}
	width := int(w)
	fill := " "
	if len(args) > 2 && args[2].str != "" {
		r, _ := utf8.DecodeRuneInString(args[2].str)
		fill = string(r)
	}
	n := utf8.RuneCountInString(args[0].str)
	if n >= width {
		return Text(args[0].str), nil
	}
	return Text(strings.Repeat(fill, width-n) + args[0].str), nil
}

// fnRegexReplace replaces every match with the replacement taken
// literally. The matches are found first, so that the length of the result
// is known before it is built; Go's successive non-overlapping matches
// are those of RE2, which the DuckDB dialect runs.
func fnRegexReplace(vm *VM, args []Value) (Value, *EvalError) {
	re, err := regexp.Compile(args[1].str)
	if err != nil {
		return Value{}, &EvalError{Code: ErrBadText, Msg: "invalid pattern: " + err.Error()}
	}
	t, repl := args[0].str, args[2].str
	matches := re.FindAllStringIndex(t, -1)
	if len(matches) == 0 {
		return Text(t), nil
	}
	matched := 0
	for _, m := range matches {
		matched += utf8.RuneCountInString(t[m[0]:m[1]])
	}
	if utf8.RuneCountInString(t)-matched+len(matches)*utf8.RuneCountInString(repl) > vm.maxText() {
		return Value{}, tooLong()
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(t[last:m[0]])
		b.WriteString(repl)
		last = m[1]
	}
	b.WriteString(t[last:])
	return Text(b.String()), nil
}

func fnText(_ *VM, args []Value) (Value, *EvalError) { return Text(render(args[0])), nil }

// fnValue reads a decimal number: optional sign, digits with an optional
// fraction, an optional exponent, a dot as the decimal point and nothing
// else. Empty or whitespace-only text is blank.
func fnValue(_ *VM, args []Value) (Value, *EvalError) {
	s := strings.TrimSpace(args[0].str)
	if s == "" {
		return Blank(), nil
	}
	if !numberSyntax(s) {
		return Value{}, &EvalError{Code: ErrBadText, Msg: "not a number: " + s}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange) && !math.IsInf(f, 0) {
			return Number(f), nil // underflow to zero or a denormal is a number
		}
		return Value{}, notANumber()
	}
	return finite(f)
}

func numberSyntax(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && isDigit(rune(s[i])) {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(rune(s[i])) {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for i < len(s) && isDigit(rune(s[i])) {
			i++
			exp++
		}
		if exp == 0 {
			return false
		}
	}
	return i == len(s)
}

// arrayIndex reads a path segment as an array index the way a JSON
// pointer does (RFC 6901), which is what the DuckDB dialect's json_extract
// accepts: "0" or digits without a leading zero, no sign.
func arrayIndex(seg string) (int, bool) {
	if seg == "" || (len(seg) > 1 && seg[0] == '0') {
		return 0, false
	}
	for _, c := range seg {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	i, err := strconv.Atoi(seg)
	return i, err == nil
}

// fnJSONValue reads one value at a dotted path and renders it as text.
func fnJSONValue(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull {
		return Blank(), nil
	}
	var doc any
	if err := json.Unmarshal([]byte(args[0].str), &doc); err != nil {
		return Value{}, &EvalError{Code: ErrBadText, Msg: "invalid json: " + err.Error()}
	}
	cur := doc
	if p := args[1].str; p != "" {
		for _, seg := range strings.Split(p, ".") {
			switch c := cur.(type) {
			case map[string]any:
				v, ok := c[seg]
				if !ok {
					return Blank(), nil
				}
				cur = v
			case []any:
				i, ok := arrayIndex(seg)
				if !ok || i >= len(c) {
					return Blank(), nil
				}
				cur = c[i]
			default:
				return Blank(), nil
			}
		}
	}
	switch v := cur.(type) {
	case nil:
		return Blank(), nil
	case string:
		return Text(v), nil
	case float64:
		return Text(renderNumber(v)), nil
	case bool:
		return Text(render(Bool(v))), nil
	}
	return Value{}, &EvalError{Code: ErrBadText, Msg: "the path does not lead to a scalar"}
}
