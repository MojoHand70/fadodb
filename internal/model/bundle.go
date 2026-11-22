package model

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The bundle (ADR-0070) hands a page a class and the formulas compiled
// from it, so that the page works a document out as the server does.
// This file holds the two parts of it the model knows: the class with
// the words of a language put in place, and the formulas a page runs.

// LanguageTag reads a language tag of BCP 47 in any case and answers it
// in its canonical case, or why it is not a tag. A definition of words
// must be written in the canonical case; a page that asks for a bundle
// may send the tag either way, as browsers do.
func LanguageTag(tag string) (canonical, why string) { return languageTag(tag) }

// LanguageLookup lists the tags the words of a language are looked for
// under, most specific first, as the lookup of RFC 4647 truncates them:
// pt-BR, then pt; en-US-u-ca-gregory, then en-US-u-ca, en-US, en. A
// subtag of one letter or digit is never left last.
func LanguageLookup(tag string) []string {
	var out []string
	for tag != "" {
		out = append(out, tag)
		i := strings.LastIndexByte(tag, '-')
		if i < 0 {
			break
		}
		tag = tag[:i]
		if j := strings.LastIndexByte(tag, '-'); j >= 0 && len(tag)-j == 2 {
			tag = tag[:j]
		}
	}
	return out
}

// PutClassWords answers a class as it was defined with the words of a
// language put in place (ADR-0070, item 4): the label and the description
// of the class, of each variable, of each column of a rows variable, of
// each section, state and transition the words give, in place of the
// class's own. What the words do not give stays as it was written, and
// words of a path the class no longer has are left out. The members keep
// the order they were written in. A column written in the short form,
// "qty": "int", is written in the long form where a word is put in it.
func PutClassWords(def []byte, cls *Class, w *WordsDef) ([]byte, error) {
	if w == nil {
		return def, nil
	}
	top, err := members(def)
	if err != nil {
		return nil, err
	}
	top = putWords(top, &Words{Label: w.Label, Description: w.Description})

	if i := find(top, "variables"); i >= 0 && len(w.Variables) > 0 {
		vars, err := members(top[i].val)
		if err != nil {
			return nil, err
		}
		for _, path := range sortedWords(w.Variables) {
			name, column, nested := strings.Cut(path, ".")
			v, ok := cls.Variable(name)
			if !ok {
				continue
			}
			at := findExact(vars, v.Name)
			if at < 0 {
				continue
			}
			if !nested {
				if vars[at].val, err = putIn(vars[at].val, w.Variables[path]); err != nil {
					return nil, err
				}
				continue
			}
			// A column of a rows variable, in its inline schema.
			one, err := members(vars[at].val)
			if err != nil {
				continue
			}
			si := find(one, "schema")
			if si < 0 || len(one[si].val) == 0 || one[si].val[0] != '{' {
				continue
			}
			cols, err := members(one[si].val)
			if err != nil {
				return nil, err
			}
			ci := findFold(cols, column)
			if ci < 0 {
				continue
			}
			if cols[ci].val, err = putIn(cols[ci].val, w.Variables[path]); err != nil {
				return nil, err
			}
			one[si].val = object(cols)
			vars[at].val = object(one)
		}
		top[i].val = object(vars)
	}

	if i := find(top, "sections"); i >= 0 && len(w.Sections) > 0 {
		folded := make(map[string]*Words, len(w.Sections))
		for name, words := range w.Sections {
			folded[upper(name)] = words
		}
		if top[i].val, err = putSectionWords(top[i].val, folded); err != nil {
			return nil, err
		}
	}

	if i := find(top, "states"); i >= 0 && len(w.States) > 0 && cls.States != nil {
		block, err := members(top[i].val)
		if err != nil {
			return nil, err
		}
		for _, which := range []struct {
			key   string
			pick  func(*Words) string
			given bool
		}{
			{"labels", func(x *Words) string { return x.Label }, false},
			{"descriptions", func(x *Words) string { return x.Description }, false},
		} {
			var texts []member
			at := find(block, which.key)
			if at >= 0 {
				if texts, err = members(block[at].val); err != nil {
					return nil, err
				}
			}
			changed := false
			for _, st := range cls.States.List {
				words := w.States[st]
				if words == nil || which.pick(words) == "" {
					continue
				}
				texts = set(texts, st, text(which.pick(words)))
				changed = true
			}
			if !changed {
				continue
			}
			if at >= 0 {
				block[at].val = object(texts)
			} else {
				block = append(block, member{key: which.key, val: object(texts)})
			}
		}
		top[i].val = object(block)
	}

	if i := find(top, "transitions"); i >= 0 && len(w.Transitions) > 0 {
		var list []json.RawMessage
		if err := json.Unmarshal(top[i].val, &list); err != nil {
			return nil, err
		}
		for k, raw := range list {
			one, err := members(raw)
			if err != nil {
				continue
			}
			ni := find(one, "name")
			if ni < 0 {
				continue
			}
			var name string
			if json.Unmarshal(one[ni].val, &name) != nil || name == "" {
				continue
			}
			for path, words := range w.Transitions {
				if strings.EqualFold(path, name) {
					list[k] = object(putWords(one, words))
					break
				}
			}
		}
		top[i].val = array(list)
	}
	return object(top), nil
}

// PutSchemaWords answers a schema as it was defined with the words of
// its fields in a language put in place, as PutClassWords does for a
// class.
func PutSchemaWords(def []byte, w *WordsDef) ([]byte, error) {
	if w == nil || len(w.Fields) == 0 {
		return def, nil
	}
	top, err := members(def)
	if err != nil {
		return nil, err
	}
	i := find(top, "fields")
	if i < 0 {
		return def, nil
	}
	fields, err := members(top[i].val)
	if err != nil {
		return nil, err
	}
	for path, words := range w.Fields {
		if fi := findFold(fields, path); fi >= 0 {
			if fields[fi].val, err = putIn(fields[fi].val, words); err != nil {
				return nil, err
			}
		}
	}
	top[i].val = object(fields)
	return object(top), nil
}

// putSectionWords puts the words of each section in place, however deep
// it is in the outline: a section is named by its name alone.
func putSectionWords(raw json.RawMessage, words map[string]*Words) (json.RawMessage, error) {
	sections, err := members(raw)
	if err != nil {
		return nil, err
	}
	for k := range sections {
		one, err := members(sections[k].val)
		if err != nil {
			continue
		}
		if w := words[upper(sections[k].key)]; w != nil {
			one = putWords(one, w)
		}
		if inner := find(one, "sections"); inner >= 0 {
			if one[inner].val, err = putSectionWords(one[inner].val, words); err != nil {
				return nil, err
			}
		}
		sections[k].val = object(one)
	}
	return object(sections), nil
}

// putIn puts words in a value that is an object, or a column or field of
// the short form, a type alone, which it writes in the long form.
func putIn(raw json.RawMessage, w *Words) (json.RawMessage, error) {
	if w == nil {
		return raw, nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		raw = object([]member{{key: "type", val: raw}})
	}
	one, err := members(raw)
	if err != nil {
		return nil, err
	}
	return object(putWords(one, w)), nil
}

// putWords sets the label and the description the words give.
func putWords(ms []member, w *Words) []member {
	if w == nil {
		return ms
	}
	if w.Label != "" {
		ms = set(ms, "label", text(w.Label))
	}
	if w.Description != "" {
		ms = set(ms, "description", text(w.Description))
	}
	return ms
}

// member is one member of a JSON object, in the order it was written.
type member struct {
	key string
	val json.RawMessage
}

// members reads a JSON object into its members, in order.
func members(raw []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		out = append(out, member{key: key, val: val})
	}
	return out, nil
}

// object writes members back as a JSON object.
func object(ms []member) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(text(m.key))
		b.WriteByte(':')
		b.Write(m.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// array writes values back as a JSON array.
func array(list []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(v)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// text writes a string as JSON, leaving <, > and & as they are.
func text(s string) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// find finds a member a definition names, as the definition is read:
// without regard to case, the last of two winning.
func find(ms []member, key string) int {
	at := -1
	for i, m := range ms {
		if strings.EqualFold(m.key, key) {
			at = i
		}
	}
	return at
}

// findExact finds a member by its exact key, then without regard to
// case.
func findExact(ms []member, key string) int {
	for i, m := range ms {
		if m.key == key {
			return i
		}
	}
	return findFold(ms, key)
}

// findFold finds the first member whose key is the name without regard
// to case.
func findFold(ms []member, key string) int {
	for i, m := range ms {
		if strings.EqualFold(m.key, key) {
			return i
		}
	}
	return -1
}

// set gives a member its value in place, or adds it at the end.
func set(ms []member, key string, val json.RawMessage) []member {
	if i := find(ms, key); i >= 0 {
		ms[i].val = val
		return ms
	}
	return append(ms, member{key: key, val: val})
}

// BundleFormula is one formula of the bundle a page works out: a
// computed variable, a default or the title, with the variables it
// reads. Formula is its bytecode in base64; a formula the page cannot
// work out carries Server in its place (ADR-0070, item 7).
type BundleFormula struct {
	Variable string   `json:"variable"`
	Type     string   `json:"type,omitempty"`
	Reads    []string `json:"reads"`
	Formula  string   `json:"formula,omitempty"`
	Server   bool     `json:"server,omitempty"`
}

// BundleRule is a formula of a rule the page checks: an access rule or a
// guard, compiled, or the server's, with what it reads.
type BundleRule struct {
	Formula string
	Server  bool
	Reads   []string
}

// MarshalJSON writes a compiled rule as its bytecode alone, and one that
// is the server's as {"server": true, "reads": [...]}.
func (r BundleRule) MarshalJSON() ([]byte, error) {
	if !r.Server {
		return text(r.Formula), nil
	}
	return json.Marshal(struct {
		Server bool     `json:"server"`
		Reads  []string `json:"reads"`
	}{true, nonNil(r.Reads)})
}

// BundleRight is the rule of a variable in a state that decides, as the
// rights table is looked in (ADR-0028; ADR-0069, item 4), and the cell
// it was written in.
type BundleRight struct {
	Groups  *[]string `json:"groups,omitempty"`
	Formula string    `json:"formula,omitempty"`
	Server  bool      `json:"server,omitempty"`
	Reads   []string  `json:"reads,omitempty"`
	Cell    string    `json:"cell"`
}

// BundleRights is the read and the write rule of one variable in one
// state.
type BundleRights struct {
	Read  *BundleRight `json:"read,omitempty"`
	Write *BundleRight `json:"write,omitempty"`
}

// BundleAccess is the class-level rules a page checks: create, update,
// delete and the rule of each transition. The read rule is not among
// them: the server never sends a document its reader may not read.
type BundleAccess struct {
	Create     *BundleRule            `json:"create,omitempty"`
	Update     *BundleRule            `json:"update,omitempty"`
	Delete     *BundleRule            `json:"delete,omitempty"`
	Transition map[string]*BundleRule `json:"transition,omitempty"`
}

// BundlePresentation is the compiled title and subtitle of a schema's
// presentation (ADR-0032).
type BundlePresentation struct {
	Title    *BundleRule `json:"title,omitempty"`
	Subtitle *BundleRule `json:"subtitle,omitempty"`
}

// BundleCompiled is every formula a page needs to show a document and
// check it before it is sent (ADR-0070, item 5).
type BundleCompiled struct {
	Title    *BundleFormula                     `json:"title"`
	Defaults []BundleFormula                    `json:"defaults"`
	Computed []BundleFormula                    `json:"computed"`
	Access   *BundleAccess                      `json:"access,omitempty"`
	Guards   map[string]*BundleRule             `json:"guards"`
	Rights   map[string]map[string]BundleRights `json:"rights"`
	Schemas  map[string]BundlePresentation      `json:"schemas"`
}

// Compiled answers the formulas of a class a page runs: the title, the
// defaults and the computed variables that work themselves out, in the
// order the engine works them out; the access rules but the read rule;
// the guards; the rights of each variable in each state, worked out; and
// the presentation of the schemas the class uses. A live variable is
// listed among the computed ones as the server's. Triggered variables,
// timers, rules and actions are the server's and are left out.
func Compiled(cls *Class) *BundleCompiled {
	out := &BundleCompiled{
		Defaults: []BundleFormula{},
		Computed: []BundleFormula{},
		Guards:   map[string]*BundleRule{},
		Rights:   map[string]map[string]BundleRights{},
		Schemas:  map[string]BundlePresentation{},
	}
	if cls.Title != nil {
		f := bundleFormula("title", cls.Title, depsOf(cls.Title))
		out.Title = &f
	}
	for _, name := range cls.DefaultOrder {
		if v, ok := cls.Variable(name); ok && v.Default != nil {
			f := bundleFormula(v.Name, v.Default, v.Deps)
			f.Type = v.Type.String()
			out.Defaults = append(out.Defaults, f)
		}
	}
	for _, name := range cls.Order {
		// The order holds "title" too, which is not a variable.
		v, ok := cls.Variable(name)
		if !ok || v.Formula == nil {
			continue
		}
		f := bundleFormula(v.Name, v.Formula, v.Deps)
		f.Type = v.Type.String()
		out.Computed = append(out.Computed, f)
	}
	// A live variable is the server's (ADR-0076, item 13): it is worked
	// out for one reader at one moment, so the page asks the command live
	// for it and never works it out itself.
	for _, v := range cls.Variables {
		if v.Live() && v.Formula != nil {
			out.Computed = append(out.Computed, BundleFormula{Variable: v.Name, Type: v.Type.String(), Reads: nonNil(v.Deps), Server: true})
		}
	}
	if a := cls.Access; a != nil {
		out.Access = &BundleAccess{Create: bundleRule(a.Create), Update: bundleRule(a.Update), Delete: bundleRule(a.Delete)}
		if len(a.Transition) > 0 {
			out.Access.Transition = make(map[string]*BundleRule, len(a.Transition))
			for name, p := range a.Transition {
				out.Access.Transition[name] = bundleRule(p)
			}
		}
	}
	for _, t := range cls.Transitions {
		if t.Name != "" && t.Guard != nil {
			out.Guards[t.Name] = bundleRule(t.Guard)
		}
	}
	if cls.Rights != nil && cls.States != nil {
		for _, st := range cls.States.List {
			for _, v := range cls.Variables {
				r := BundleRights{Read: bundleRight(cls.ReadRule(st, v.Name)), Write: bundleRight(cls.WriteRule(st, v.Name))}
				if r.Read == nil && r.Write == nil {
					continue
				}
				if out.Rights[st] == nil {
					out.Rights[st] = map[string]BundleRights{}
				}
				out.Rights[st][v.Name] = r
			}
		}
	}
	for _, s := range cls.Schemas {
		if p := s.Presentation; p != nil && (p.Title != nil || p.Subtitle != nil) {
			out.Schemas[s.Name] = BundlePresentation{Title: bundleRule(p.Title), Subtitle: bundleRule(p.Subtitle)}
		}
	}
	return out
}

// ByServer reports whether a page cannot work a program out itself: it
// reads other documents or a table, with LOOKUP or an aggregate (ADR-0020,
// ADR-0046), or the history of the document's states (ADR-0047, item 6),
// whose stays are not on the wire.
func ByServer(p *formula.Program) bool {
	if len(p.Queries) > 0 || p.ReadsStates {
		return true
	}
	for _, c := range p.Classes {
		if c == formula.ClassLookup || c == formula.ClassQuery || c == formula.ClassSecret {
			return true
		}
	}
	return false
}

func bundleFormula(variable string, p *formula.Program, reads []string) BundleFormula {
	f := BundleFormula{Variable: variable, Reads: nonNil(reads)}
	if ByServer(p) {
		f.Server = true
	} else {
		f.Formula = base64.StdEncoding.EncodeToString(p.Encode())
	}
	return f
}

func bundleRule(p *formula.Program) *BundleRule {
	if p == nil {
		return nil
	}
	if ByServer(p) {
		return &BundleRule{Server: true, Reads: depsOf(p)}
	}
	return &BundleRule{Formula: base64.StdEncoding.EncodeToString(p.Encode())}
}

func bundleRight(r *RightRule) *BundleRight {
	if r == nil {
		return nil
	}
	out := &BundleRight{Cell: r.Cell()}
	switch {
	case r.Formula == nil:
		groups := append([]string{}, r.Groups...)
		out.Groups = &groups
	case ByServer(r.Formula):
		out.Server, out.Reads = true, nonNil(depsOf(r.Formula))
	default:
		out.Formula = base64.StdEncoding.EncodeToString(r.Formula.Encode())
	}
	return out
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
