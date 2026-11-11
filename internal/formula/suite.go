package formula

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	// bool, number, text, date, ref, json, array<scalar>, rows, or
	// object<Name> for a schema of Schemas.
	Variables map[string]string `json:"variables,omitempty"`
	// Schemas declares object schemas by name: field name to type, where
	// a type may be object<Name> of another schema (ADR-0031). A case sets
	// an object's leaf by its path, "shipping.city".
	Schemas map[string]map[string]string `json:"schemas,omitempty"`
	// Sources declares the explicit tables LOOKUP may read (ADR-0020),
	// with their rows.
	Sources map[string]*SuiteSource `json:"sources,omitempty"`
	// Limits sets the size limits the cases evaluate under (ADR-0021);
	// absent, the defaults. A suite at a small limit reaches the boundary
	// with short texts.
	Limits *SuiteLimits `json:"limits,omitempty"`
	Cases  []Case       `json:"cases"`
	// File is where the suite was loaded from, for messages.
	File string `json:"-"`
}

// SuiteSource declares one table a suite's LOOKUP cases read: its columns
// by scalar type, its key columns, and its rows as objects in the input
// notation, an absent column blank. Columns are in name order.
type SuiteSource struct {
	Columns map[string]string            `json:"columns"`
	Key     []string                     `json:"key"`
	Rows    []map[string]json.RawMessage `json:"rows,omitempty"`
	// Class says the source stands for the documents of a class, which
	// an auto formula may not read, and Indexed names the columns that
	// lead an index (ADR-0046).
	Class   bool     `json:"class,omitempty"`
	Indexed []string `json:"indexed,omitempty"`
}

// SourceSet resolves the suite's sources.
func (s *Suite) SourceSet() (*SourceSet, error) { return sourcesOf(s.Sources) }

// SourceRows parses the rows of every source, row-major in column order.
func (s *Suite) SourceRows() (map[*Source][][]Value, error) {
	set, err := sourcesOf(s.Sources)
	if err != nil {
		return nil, err
	}
	out := map[*Source][][]Value{}
	for name, ss := range s.Sources {
		src, _ := set.Source(name)
		rows := make([][]Value, 0, len(ss.Rows))
		for ri, raw := range ss.Rows {
			row := make([]Value, len(src.Columns))
			for i, f := range src.Columns {
				v, err := ParseCaseValue(raw[f.Name], f.Type)
				if err != nil {
					return nil, fmt.Errorf("source %s, row %d, %s: %w", name, ri, f.Name, err)
				}
				row[i] = v
			}
			rows = append(rows, row)
		}
		out[src] = rows
	}
	return out, nil
}

// Executor makes the in-memory executor over the suite's sources.
func (s *Suite) Executor() (*MemoryExecutor, error) {
	rows, err := s.SourceRows()
	if err != nil {
		return nil, err
	}
	m := NewMemoryExecutor()
	for src, data := range rows {
		m.AddTable(src, data)
	}
	return m, nil
}

// sourcesOf resolves suite sources into a SourceSet, columns in name
// order, and validates them.
func sourcesOf(decl map[string]*SuiteSource) (*SourceSet, error) {
	set := NewSourceSet()
	names := make([]string, 0, len(decl))
	for name := range decl {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ss := decl[name]
		if name == "" || ss == nil {
			return nil, fmt.Errorf("a source has no name or no body")
		}
		if len(ss.Columns) == 0 {
			return nil, fmt.Errorf("source %s: no columns", name)
		}
		src := &Source{Name: name, Key: ss.Key, Class: ss.Class, Indexed: ss.Indexed}
		cols := make([]string, 0, len(ss.Columns))
		for c := range ss.Columns {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		for _, c := range cols {
			t, err := ParseType(ss.Columns[c])
			if err != nil || !t.IsScalar() || t.Kind == KindNull {
				return nil, fmt.Errorf("source %s: column %s has type %q; a column is a scalar", name, c, ss.Columns[c])
			}
			src.Columns = append(src.Columns, Field{Name: c, Type: t})
		}
		if len(ss.Key) == 0 {
			return nil, fmt.Errorf("source %s: no key", name)
		}
		for _, k := range ss.Key {
			if _, ok := src.Column(k); !ok {
				return nil, fmt.Errorf("source %s: key column %s is not a column", name, k)
			}
		}
		for ri, row := range ss.Rows {
			for c := range row {
				if _, ok := src.Column(c); !ok {
					return nil, fmt.Errorf("source %s: row %d sets %s, which is not a column", name, ri, c)
				}
			}
		}
		set.Add(src)
	}
	return set, nil
}

// SuiteLimits is the limits field of a suite.
type SuiteLimits struct {
	MaxText int `json:"max_text"`
}

// Scope builds the scope of the suite's variables, in name order.
func (s *Suite) Scope() (*Scope, error) { return ScopeOf(s.Variables, s.Schemas) }

// declares reports whether an input name is a declared variable or, as
// "variable.field.field", a scalar leaf of a declared object variable.
func (s *Suite) declares(name string) bool {
	parts := strings.Split(name, ".")
	typ, ok := s.Variables[parts[0]]
	if !ok {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	t, err := parseTypeIn(typ, s.Schemas, map[string]bool{})
	if err != nil {
		return false
	}
	for _, step := range parts[1:] {
		if t.Kind != KindObject {
			return false
		}
		f, ok := t.Field(step)
		if !ok {
			return false
		}
		t = f.Type
	}
	return t.IsScalar()
}

// EvalLimits are the limits the suite's cases evaluate under.
func (s *Suite) EvalLimits() Limits {
	l := DefaultLimits()
	if s.Limits != nil {
		l.MaxText = s.Limits.MaxText
	}
	return l
}

// Case is one evaluation case: a formula, its inputs and context, and the
// value or the error it must produce on every target.
type Case struct {
	Name    string `json:"name,omitempty"`
	Formula string `json:"formula"`
	// Inputs are values for declared variables; an absent variable is blank.
	// Dates are written as YYYY-MM-DD or YYYY-MM-DD HH:MM:SS.
	Inputs map[string]json.RawMessage `json:"inputs,omitempty"`
	// Context sets the context functions: user, groups, now (a date),
	// state and id.
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
	State  string   `json:"state,omitempty"`
	ID     string   `json:"id,omitempty"`
	// The life of the document (ADR-0047): its version, the moments it
	// was created, last changed and entered its state, written as Now is,
	// who did each, and the state it came from.
	Version     int64  `json:"version,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	UpdatedBy   string `json:"updated_by,omitempty"`
	EnteredAt   string `json:"entered_at,omitempty"`
	EnteredBy   string `json:"entered_by,omitempty"`
	EnteredFrom string `json:"entered_from,omitempty"`
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
	Suite       string                       `json:"suite"`
	Description string                       `json:"description,omitempty"`
	Variables   map[string]string            `json:"variables,omitempty"`
	Schemas     map[string]map[string]string `json:"schemas,omitempty"`
	// Sources declares the tables LOOKUP may read, as in an evaluation
	// suite; their rows are not needed to bind.
	Sources map[string]*SuiteSource `json:"sources,omitempty"`
	// Mode is "any" (the default), "auto" (ADR-0015) or "pure" (ADR-0018).
	Mode string `json:"mode,omitempty"`
	// Secrets are the secrets SECRET may name (ADR-0045): present, even
	// empty, for the formulas of the request of an action definition, and
	// absent everywhere else, where SECRET is refused.
	Secrets []string     `json:"secrets,omitempty"`
	Accept  []string     `json:"accept"`
	Reject  []RejectCase `json:"reject"`
	File    string       `json:"-"`
}

// SourceSet resolves the suite's sources.
func (s *BindingSuite) SourceSet() (*SourceSet, error) { return sourcesOf(s.Sources) }

// ErrorCodes is the closed list of evaluation error codes (ADR-0019,
// ADR-0020, ADR-0022, ADR-0024). A case may only expect one of these.
var ErrorCodes = map[string]bool{
	"div_by_zero": true, "not_a_number": true, "bad_text": true, "out_of_range": true,
	"length_mismatch": true, "too_many_elements": true,
	"unknown_source": true, "unknown_column": true, "query_failed": true,
	"overflow": true, "inexact": true, "currency_mismatch": true,
}

// Scope builds the scope of the suite's variables, in name order.
func (s *BindingSuite) Scope() (*Scope, error) { return ScopeOf(s.Variables, s.Schemas) }

// BindMode is the binding mode the suite names.
func (s *BindingSuite) BindMode() Mode {
	switch s.Mode {
	case "auto":
		return ModeAuto
	case "pure":
		return ModePure
	}
	return ModeAny
}

// parseTypeIn reads a type as written in a suite, resolving object<Name>
// against the suite's schemas. visiting guards against a schema that
// contains itself.
func parseTypeIn(s string, schemas map[string]map[string]string, visiting map[string]bool) (Type, error) {
	if strings.HasPrefix(s, "object<") && strings.HasSuffix(s, ">") {
		name := s[7 : len(s)-1]
		fields, ok := schemas[name]
		if !ok {
			return Type{}, fmt.Errorf("unknown schema %q", name)
		}
		if visiting[name] {
			return Type{}, fmt.Errorf("schema %s contains itself", name)
		}
		visiting[name] = true
		defer delete(visiting, name)
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fs := make([]Field, 0, len(keys))
		for _, k := range keys {
			ft, err := parseTypeIn(fields[k], schemas, visiting)
			if err != nil {
				return Type{}, fmt.Errorf("schema %s, field %s: %w", name, k, err)
			}
			if !ft.IsScalar() && ft.Kind != KindObject || ft.Kind == KindNull {
				return Type{}, fmt.Errorf("schema %s, field %s: a field is a scalar or an object, got %s", name, k, ft)
			}
			fs = append(fs, Field{Name: k, Type: ft})
		}
		return ObjectType(name, fs), nil
	}
	return ParseType(s)
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
		if err := validateVariables(s.Variables, s.Schemas); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if _, err := sourcesOf(s.Sources); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if s.Mode != "" && s.Mode != "any" && s.Mode != "auto" && s.Mode != "pure" {
			return nil, fmt.Errorf("%s: mode must be any, auto or pure, got %q", f, s.Mode)
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
	if err := validateVariables(s.Variables, s.Schemas); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := sourcesOf(s.Sources); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Limits != nil && s.Limits.MaxText < MinMaxText {
		return nil, fmt.Errorf("%s: limits.max_text must be at least %d", path, MinMaxText)
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
		if _, err := ParseType(c.Type); c.Type != "" && err != nil {
			return nil, fmt.Errorf("%s: unknown type %q", where, c.Type)
		}
		for name := range c.Inputs {
			if !s.declares(name) {
				return nil, fmt.Errorf("%s: input %q is not a declared variable or object leaf", where, name)
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
	if err := validateVariables(s.Variables, nil); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(s.Accept)+len(s.Reject) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	// A reject case may be the empty formula: refusing it is a case too.
	return &s, nil
}

func validateVariables(vars map[string]string, schemas map[string]map[string]string) error {
	for name, typ := range vars {
		if name == "" {
			return fmt.Errorf("a variable has no name")
		}
		if _, err := parseTypeIn(typ, schemas, map[string]bool{}); err != nil || typ == "blank" {
			return fmt.Errorf("variable %s: unknown type %q", name, typ)
		}
	}
	return nil
}

// ScopeOf builds the scope of a suite's variables, in name order, with
// object<Name> types resolved against the schemas.
func ScopeOf(vars map[string]string, schemas map[string]map[string]string) (*Scope, error) {
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	vs := make([]Variable, 0, len(names))
	for _, n := range names {
		t, err := parseTypeIn(vars[n], schemas, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("variable %s: %w", n, err)
		}
		vs = append(vs, Variable{Name: n, Type: t})
	}
	return NewScope(vs...)
}

// ParseCaseDate reads an instant as the suite writes it: YYYY-MM-DD or
// YYYY-MM-DD HH:MM:SS, UTC, as seconds since the epoch.
func ParseCaseDate(s string) (int64, error) {
	layout := dateLayout
	if len(s) > len(dateLayout) {
		layout = dateTimeLayout
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// ParseCaseDay reads a calendar day as the suite writes it, YYYY-MM-DD, as
// days since 1970-01-01.
func ParseCaseDay(s string) (int64, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return 0, err
	}
	return floorDiv(t.Unix(), secondsPerDay), nil
}

// ParseCaseValue reads a case value written in the suite notation as a
// value of the given type. An exact value is written as a JSON number or
// as text, read exactly and brought to the type's scale, which must not
// drop a digit; a money value is text such as "12.30 PLN".
func ParseCaseValue(raw json.RawMessage, t Type) (Value, error) {
	if raw == nil || string(raw) == "null" {
		return Blank(), nil
	}
	switch t.Kind {
	case KindNull:
		return Value{}, fmt.Errorf("a blank-typed result cannot be %s", raw)
	case KindNumber:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return Value{}, fmt.Errorf("want a number, got %s", raw)
		}
		return Number(f), nil
	case KindBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return Value{}, fmt.Errorf("want a bool, got %s", raw)
		}
		return Bool(b), nil
	case KindDecimal:
		text := string(raw)
		if strings.HasPrefix(text, `"`) {
			if err := json.Unmarshal(raw, &text); err != nil {
				return Value{}, fmt.Errorf("want an exact number, got %s", raw)
			}
		}
		m, s, e := parseDecimal(strings.TrimSpace(text))
		if e != nil {
			return Value{}, fmt.Errorf("want an exact number, got %s: %s", raw, e.Msg)
		}
		m, e = rescaleExact(m, s, int(t.Scale))
		if e != nil {
			return Value{}, fmt.Errorf("%s does not fit %s: %s", raw, t, e.Msg)
		}
		return Decimal(m, int(t.Scale)), nil
	case KindText, KindRef, KindJSON, KindDate, KindDateTime, KindMoney:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return Value{}, fmt.Errorf("want text, got %s", raw)
		}
		switch t.Kind {
		case KindText:
			return Text(s), nil
		case KindRef:
			return Ref(s), nil
		case KindJSON:
			return JSON(s), nil
		case KindMoney:
			m, scale, cur, e := parseMoney(s)
			if e == nil {
				m, e = rescaleExact(m, scale, int(t.Scale))
			}
			if e != nil {
				return Value{}, fmt.Errorf("want money as \"12.30 PLN\" fitting %s, got %s: %s", t, raw, e.Msg)
			}
			return Money(m, int(t.Scale), cur), nil
		case KindDate:
			days, err := ParseCaseDay(s)
			if err != nil {
				return Value{}, fmt.Errorf("want a date as YYYY-MM-DD, got %s", raw)
			}
			return Date(days), nil
		}
		secs, err := ParseCaseDate(s)
		if err != nil {
			return Value{}, fmt.Errorf("want a datetime, got %s", raw)
		}
		return DateTime(secs), nil
	}
	return Value{}, fmt.Errorf("values of type %s are not supported by the runners yet", t)
}

// Context converts the case context to a VM context; nil for none.
func (c *CaseContext) Context() (*Context, error) {
	if c == nil {
		return nil, nil
	}
	ctx := &Context{User: c.User, Groups: c.Groups, State: c.State, ID: c.ID}
	if c.Now != "" {
		secs, err := ParseCaseDate(c.Now)
		if err != nil {
			return nil, fmt.Errorf("context now: %w", err)
		}
		ctx.Now = secs
	}
	life := Life{Version: c.Version, CreatedBy: c.CreatedBy, UpdatedBy: c.UpdatedBy, EnteredBy: c.EnteredBy, EnteredFrom: c.EnteredFrom}
	for _, m := range []struct {
		text string
		to   *int64
		name string
	}{{c.CreatedAt, &life.CreatedAt, "created_at"}, {c.UpdatedAt, &life.UpdatedAt, "updated_at"}, {c.EnteredAt, &life.EnteredAt, "entered_at"}} {
		if m.text == "" {
			continue
		}
		secs, err := ParseCaseDate(m.text)
		if err != nil {
			return nil, fmt.Errorf("context %s: %w", m.name, err)
		}
		*m.to = secs
	}
	if life != (Life{}) {
		ctx.Doc = &life
	}
	return ctx, nil
}
