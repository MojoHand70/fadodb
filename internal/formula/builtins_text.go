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
// blank. Positions and lengths count code points and are 1-based.

func countArg(v Value, what string) (int, *EvalError) {
	n := math.Trunc(v.num)
	if n < 0 {
		return 0, outOfRange(what + " must not be negative")
	}
	if n > 1e9 {
		return 0, outOfRange(what + " is too large")
	}
	return int(n), nil
}

func fnLeft(_ *VM, args []Value) (Value, *EvalError) {
	n, err := countArg(args[1], "the count")
	if err != nil {
		return Value{}, err
	}
	r := []rune(args[0].str)
	if n > len(r) {
		n = len(r)
	}
	return Text(string(r[:n])), nil
}

func fnRight(_ *VM, args []Value) (Value, *EvalError) {
	n, err := countArg(args[1], "the count")
	if err != nil {
		return Value{}, err
	}
	r := []rune(args[0].str)
	if n > len(r) {
		n = len(r)
	}
	return Text(string(r[len(r)-n:])), nil
}

func fnMid(_ *VM, args []Value) (Value, *EvalError) {
	pos := math.Trunc(args[1].num)
	if pos < 1 {
		return Value{}, outOfRange("the position must be 1 or more")
	}
	r := []rune(args[0].str)
	start := int(math.Min(pos, float64(len(r)+1))) - 1
	end := len(r)
	if len(args) > 2 {
		n, err := countArg(args[2], "the length")
		if err != nil {
			return Value{}, err
		}
		if start+n < end {
			end = start + n
		}
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

func fnReplace(_ *VM, args []Value) (Value, *EvalError) {
	pos := math.Trunc(args[1].num)
	if pos < 1 {
		return Value{}, outOfRange("the position must be 1 or more")
	}
	n, err := countArg(args[2], "the length")
	if err != nil {
		return Value{}, err
	}
	r := []rune(args[0].str)
	start := int(math.Min(pos, float64(len(r)+1))) - 1
	end := start + n
	if end > len(r) {
		end = len(r)
	}
	return Text(string(r[:start]) + args[3].str + string(r[end:])), nil
}

func fnSubstitute(_ *VM, args []Value) (Value, *EvalError) {
	if args[1].str == "" {
		return Text(args[0].str), nil
	}
	return Text(strings.ReplaceAll(args[0].str, args[1].str, args[2].str)), nil
}

func fnConcat(_ *VM, args []Value) (Value, *EvalError) {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(render(a))
	}
	return Text(b.String()), nil
}

func fnRepeat(_ *VM, args []Value) (Value, *EvalError) {
	n, err := countArg(args[1], "the count")
	if err != nil {
		return Value{}, err
	}
	if n*len(args[0].str) > 1<<20 {
		return Value{}, outOfRange("the result is too long")
	}
	return Text(strings.Repeat(args[0].str, n)), nil
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

func fnPad(_ *VM, args []Value) (Value, *EvalError) {
	width, err := countArg(args[1], "the width")
	if err != nil {
		return Value{}, err
	}
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

func fnRegexReplace(_ *VM, args []Value) (Value, *EvalError) {
	re, err := regexp.Compile(args[1].str)
	if err != nil {
		return Value{}, &EvalError{Code: ErrBadText, Msg: "invalid pattern: " + err.Error()}
	}
	return Text(re.ReplaceAllString(args[0].str, args[2].str)), nil
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
				i, err := strconv.Atoi(seg)
				if err != nil || i < 0 || i >= len(c) {
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
