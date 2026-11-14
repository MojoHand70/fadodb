package formula

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// The function catalog is catalog.json: every function a formula can call,
// with its evaluation class, its argument shapes and a one-line summary. The
// parser reads arities from it, the binder reads classes and types, the
// documentation and editor hints are generated from it, and the conformance
// suite covers every entry. A function is described once, here.

//go:embed catalog.json
var catalogJSON []byte

// Class is the evaluation class of a function (ADR-0015, ADR-0016). It
// decides where a formula using the function may appear.
type Class string

// The evaluation classes. ClassSecret is SECRET's alone: it reads a
// secret the host program supplies, and only the request of an action
// definition may use it (ADR-0045).
const (
	ClassPure    Class = "pure"
	ClassContext Class = "context"
	ClassLookup  Class = "lookup"
	ClassQuery   Class = "query"
	ClassSecret  Class = "secret"
)

// Param is one argument of a function.
type Param struct {
	Name string `json:"name"`
	// Type names the argument's static type, or a shape such as "numbers"
	// (a number or an array of numbers), "source" or "condition" for the
	// query functions.
	Type string `json:"type"`
	// Optional: this argument and every one after it may be left out.
	Optional bool `json:"optional,omitempty"`
	// Repeat: the argument may be given any number of times.
	Repeat bool `json:"repeat,omitempty"`
}

// Function is one catalog entry.
type Function struct {
	Name     string   `json:"name"`
	Aliases  []string `json:"aliases,omitempty"`
	Class    Class    `json:"class"`
	Category string   `json:"category"`
	Params   []Param  `json:"params"`
	Returns  string   `json:"returns"`
	Summary  string   `json:"summary"`
}

type catalogFile struct {
	Version   int              `json:"version"`
	Classes   map[Class]string `json:"classes"`
	Functions []*Function      `json:"functions"`
}

// catalog is the embedded catalog, loaded once. A malformed catalog is a
// programmer error caught by the catalog tests, so loading panics.
var catalog = mustLoadCatalog(catalogJSON)

type catalogIndex struct {
	functions []*Function
	byName    map[string]*Function
	classes   map[Class]string
}

func mustLoadCatalog(raw []byte) *catalogIndex {
	idx, err := loadCatalog(raw)
	if err != nil {
		panic("formula: " + err.Error())
	}
	return idx
}

func loadCatalog(raw []byte) (*catalogIndex, error) {
	var file catalogFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("catalog.json: %w", err)
	}
	if file.Version != 1 {
		return nil, fmt.Errorf("catalog.json: version %d, want 1", file.Version)
	}
	idx := &catalogIndex{byName: make(map[string]*Function, len(file.Functions)*2), classes: file.Classes}
	for _, fn := range file.Functions {
		if err := validateFunction(fn, file.Classes); err != nil {
			return nil, fmt.Errorf("catalog.json: %w", err)
		}
		for _, name := range append([]string{fn.Name}, fn.Aliases...) {
			if _, dup := idx.byName[name]; dup {
				return nil, fmt.Errorf("catalog.json: %s is listed twice", name)
			}
			idx.byName[name] = fn
		}
		idx.functions = append(idx.functions, fn)
	}
	return idx, nil
}

func validateFunction(fn *Function, classes map[Class]string) error {
	if fn.Name == "" || fn.Name != strings.ToUpper(fn.Name) {
		return fmt.Errorf("function %q: the name must be upper case", fn.Name)
	}
	for _, a := range fn.Aliases {
		if a == "" || a != strings.ToUpper(a) {
			return fmt.Errorf("function %s: alias %q must be upper case", fn.Name, a)
		}
	}
	if _, ok := classes[fn.Class]; !ok {
		return fmt.Errorf("function %s: unknown class %q", fn.Name, fn.Class)
	}
	if fn.Category == "" || fn.Returns == "" || fn.Summary == "" {
		return fmt.Errorf("function %s: category, returns and summary are required", fn.Name)
	}
	if fn.Params == nil {
		return fmt.Errorf("function %s: params must be a list, empty for no arguments", fn.Name)
	}
	optional := false
	for i, p := range fn.Params {
		if p.Name == "" || p.Type == "" {
			return fmt.Errorf("function %s: param %d needs a name and a type", fn.Name, i)
		}
		if optional && !p.Optional {
			return fmt.Errorf("function %s: param %s follows an optional param and must be optional", fn.Name, p.Name)
		}
		if p.Repeat && i != len(fn.Params)-1 {
			return fmt.Errorf("function %s: only the last param may repeat", fn.Name)
		}
		optional = optional || p.Optional
	}
	return nil
}

// LookupFunction returns the catalog entry for a function name or alias,
// case-insensitively, or nil when there is none.
func LookupFunction(name string) *Function {
	return catalog.byName[strings.ToUpper(name)]
}

// Implemented reports whether this build evaluates the catalog function
// of that name. The catalog lists every function of the language; one
// the engine does not have yet is refused where a formula calls it, with
// "not implemented yet".
func Implemented(name string) bool {
	switch name = strings.ToUpper(name); name {
	case "IF", "NOT", "AND", "OR", "IFERROR", "LOOKUP", "SECRET":
		// These are forms of the language that the parser and the binder
		// compile themselves, and no entry of the table of builtins.
		return true
	}
	_, ok := builtinByName[name]
	return ok
}

// Functions returns every catalog entry in catalog order.
func Functions() []*Function {
	out := make([]*Function, len(catalog.functions))
	copy(out, catalog.functions)
	return out
}

// Classes returns the evaluation classes with their descriptions.
func Classes() map[Class]string {
	out := make(map[Class]string, len(catalog.classes))
	for k, v := range catalog.classes {
		out[k] = v
	}
	return out
}

// MinArgs is the fewest arguments a call may pass.
func (fn *Function) MinArgs() int {
	n := 0
	for _, p := range fn.Params {
		if p.Optional {
			break
		}
		n++
	}
	return n
}

// MaxArgs is the most arguments a call may pass, or -1 when unbounded.
func (fn *Function) MaxArgs() int {
	if n := len(fn.Params); n > 0 && fn.Params[n-1].Repeat {
		return -1
	}
	return len(fn.Params)
}

// Signature renders the function under the given name as the editor shows
// it: ROUND(number[, digits]), MIN(value[, value, ...]).
func (fn *Function) Signature(name string) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('(')
	open := 0
	for i, p := range fn.Params {
		if p.Optional {
			b.WriteByte('[')
			open++
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		if p.Repeat {
			b.WriteString(", ...")
		}
	}
	b.WriteString(strings.Repeat("]", open))
	b.WriteByte(')')
	return b.String()
}

// checkArity reports an error naming the function as the author spelled it
// when got arguments do not fit the catalog entry.
func (fn *Function) checkArity(spelled string, got int) error {
	lo, hi := fn.MinArgs(), fn.MaxArgs()
	if got >= lo && (hi < 0 || got <= hi) {
		return nil
	}
	name := strings.ToUpper(spelled)
	switch {
	case hi == 0:
		return fmt.Errorf("%s takes no arguments, got %d", name, got)
	case hi < 0:
		return fmt.Errorf("%s takes at least %s, got %d", name, plural(lo, "argument"), got)
	case lo == hi:
		return fmt.Errorf("%s takes %s, got %d", name, plural(lo, "argument"), got)
	default:
		return fmt.Errorf("%s takes %d to %d arguments, got %d", name, lo, hi, got)
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
