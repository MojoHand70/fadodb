package formula

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The conformance suite under conformance/ is the specification of the
// formula language (ADR-0003). This file loads and validates it; each
// target has its own runner. Evaluation cases live in conformance/formula,
// definition-time acceptance cases in conformance/syntax (the parser) and
// conformance/binding (the binder).

// Suite is one file of evaluation cases.
type Suite struct {
	// Suite is the short name, unique across files.
	Suite       string `json:"suite"`
	Description string `json:"description,omitempty"`
	// Variables declares the input variables the cases may set, by type:
	// bool, number, text, date, ref, json, array<scalar>, rows.
	Variables map[string]string `json:"variables,omitempty"`
	Cases     []Case            `json:"cases"`
	// File is where the suite was loaded from, for messages.
	File string `json:"-"`
}

// Case is one evaluation case: a formula, its inputs and context, and the
// value or the error it must produce on every target.
type Case struct {
	Name    string `json:"name,omitempty"`
	Formula string `json:"formula"`
	// Inputs are values for declared variables; an absent variable is blank.
	// Dates are written as YYYY-MM-DD or YYYY-MM-DD HH:MM:SS.
	Inputs map[string]json.RawMessage `json:"inputs,omitempty"`
	// Context sets the context functions: user, groups, now (a date).
	Context *CaseContext `json:"context,omitempty"`
	// Type is the static type the formula must have, when the case pins it.
	// A date result must declare it so that Expected can be written as text.
	Type string `json:"type,omitempty"`
	// Expected is the value, in the same notation as Inputs. Exactly one of
	// Expected and Error is set; Expected may be null for a blank result.
	Expected json.RawMessage `json:"expected,omitempty"`
	// Error is the error code the evaluation must stop with.
	Error string `json:"error,omitempty"`
}

// CaseContext is the context a case runs in.
type CaseContext struct {
	User   string   `json:"user,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Now    string   `json:"now,omitempty"`
}

// SyntaxSuite is one file of definition-time cases: formulas the parser
// must accept and formulas it must reject.
type SyntaxSuite struct {
	Suite       string            `json:"suite"`
	Description string            `json:"description,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	Accept      []string          `json:"accept"`
	Reject      []RejectCase      `json:"reject"`
	File        string            `json:"-"`
}

// RejectCase is a formula that must be refused, with the reason for people.
type RejectCase struct {
	Formula string `json:"formula"`
	Reason  string `json:"reason,omitempty"`
}

// BindingSuite is one file of definition-time cases for the binder:
// formulas that must bind and formulas it must refuse, in a mode.
type BindingSuite struct {
	Suite       string            `json:"suite"`
	Description string            `json:"description,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	// Mode is "any" (the default) or "auto" (ADR-0015).
	Mode   string       `json:"mode,omitempty"`
	Accept []string     `json:"accept"`
	Reject []RejectCase `json:"reject"`
	File   string       `json:"-"`
}

// ErrorCodes is the closed list of evaluation error codes (ADR-0019,
// ADR-0020). A case may only expect one of these.
var ErrorCodes = map[string]bool{
	"div_by_zero": true, "not_a_number": true, "bad_text": true, "out_of_range": true,
	"length_mismatch": true, "too_many_elements": true,
	"unknown_source": true, "unknown_column": true, "query_failed": true,
}

var scalarTypes = map[string]bool{
	"bool": true, "number": true, "text": true, "date": true, "ref": true, "json": true,
}

// LoadSuites loads every evaluation suite from dir, sorted by file name.
func LoadSuites(dir string) ([]*Suite, error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return nil, err
	}
	var suites []*Suite
	names := map[string]string{}
	for _, f := range files {
		s, err := loadSuite(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := names[s.Suite]; dup {
			return nil, fmt.Errorf("%s: suite %q is also %s", f, s.Suite, prev)
		}
		names[s.Suite] = f
		suites = append(suites, s)
	}
	return suites, nil
}

// LoadSyntaxSuites loads every syntax suite from dir, sorted by file name.
func LoadSyntaxSuites(dir string) ([]*SyntaxSuite, error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return nil, err
	}
	var suites []*SyntaxSuite
	for _, f := range files {
		s, err := loadSyntaxSuite(f)
		if err != nil {
			return nil, err
		}
		suites = append(suites, s)
	}
	return suites, nil
}

// LoadBindingSuites loads every binding suite from dir, sorted by file name.
func LoadBindingSuites(dir string) ([]*BindingSuite, error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return nil, err
	}
	var suites []*BindingSuite
	for _, f := range files {
		var s BindingSuite
		if err := decodeStrict(f, &s); err != nil {
			return nil, err
		}
		s.File = f
		if s.Suite == "" {
			return nil, fmt.Errorf("%s: suite name is required", f)
		}
		if err := validateVariables(s.Variables); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if s.Mode != "" && s.Mode != "any" && s.Mode != "auto" {
			return nil, fmt.Errorf("%s: mode must be any or auto, got %q", f, s.Mode)
		}
		if len(s.Accept)+len(s.Reject) == 0 {
			return nil, fmt.Errorf("%s: no cases", f)
		}
		suites = append(suites, &s)
	}
	return suites, nil
}

func jsonFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func decodeStrict(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func loadSuite(path string) (*Suite, error) {
	var s Suite
	if err := decodeStrict(path, &s); err != nil {
		return nil, err
	}
	s.File = path
	if s.Suite == "" {
		return nil, fmt.Errorf("%s: suite name is required", path)
	}
	if err := validateVariables(s.Variables); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(s.Cases) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	for i := range s.Cases {
		c := &s.Cases[i]
		where := fmt.Sprintf("%s: case %d", path, i)
		if c.Name != "" {
			where = fmt.Sprintf("%s: case %q", path, c.Name)
		}
		if c.Formula == "" {
			return nil, fmt.Errorf("%s: formula is required", where)
		}
		if (c.Expected == nil) == (c.Error == "") {
			return nil, fmt.Errorf("%s: exactly one of expected and error is required", where)
		}
		if c.Error != "" && !ErrorCodes[c.Error] {
			return nil, fmt.Errorf("%s: unknown error code %q", where, c.Error)
		}
		if c.Type != "" && !scalarTypes[c.Type] && !strings.HasPrefix(c.Type, "array<") && c.Type != "rows" && c.Type != "blank" {
			return nil, fmt.Errorf("%s: unknown type %q", where, c.Type)
		}
		for name := range c.Inputs {
			if _, ok := s.Variables[name]; !ok {
				return nil, fmt.Errorf("%s: input %q is not a declared variable", where, name)
			}
		}
	}
	return &s, nil
}

func loadSyntaxSuite(path string) (*SyntaxSuite, error) {
	var s SyntaxSuite
	if err := decodeStrict(path, &s); err != nil {
		return nil, err
	}
	s.File = path
	if s.Suite == "" {
		return nil, fmt.Errorf("%s: suite name is required", path)
	}
	if err := validateVariables(s.Variables); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(s.Accept)+len(s.Reject) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	// A reject case may be the empty formula: refusing it is a case too.
	return &s, nil
}

func validateVariables(vars map[string]string) error {
	for name, typ := range vars {
		if name == "" {
			return fmt.Errorf("a variable has no name")
		}
		if scalarTypes[typ] {
			continue
		}
		if typ == "rows" || (strings.HasPrefix(typ, "array<") && strings.HasSuffix(typ, ">") && scalarTypes[typ[6:len(typ)-1]]) {
			continue
		}
		return fmt.Errorf("variable %s: unknown type %q", name, typ)
	}
	return nil
}
