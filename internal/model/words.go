package model

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// The words of a class, a table or a schema in another language
// (ADR-0068, item 6; ADR-0069, item 6): a definition of their own, beside
// the definition they translate, holding the label and the description of
// each thing that has one, named by its path. A name is never translated.
// A translation makes no new version of what it translates, and touches
// no document.

// The kinds of thing a definition of words is for.
const (
	WordsOfClass  = "class"
	WordsOfTable  = "table"
	WordsOfSchema = "schema"
)

// WordsDef is a definition of words as written. One of Class, Table and
// Schema names what it translates.
type WordsDef struct {
	Class       string `json:"class,omitempty"`
	Table       string `json:"table,omitempty"`
	Schema      string `json:"schema,omitempty"`
	Language    string `json:"language"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Of a class: its variables, a column of a rows variable as
	// lines.qty, its sections, its states and its transitions by name.
	Variables   map[string]*Words `json:"variables,omitempty"`
	Sections    map[string]*Words `json:"sections,omitempty"`
	States      map[string]*Words `json:"states,omitempty"`
	Transitions map[string]*Words `json:"transitions,omitempty"`
	// Of a table, its columns; of a schema, its fields.
	Columns map[string]*Words `json:"columns,omitempty"`
	Fields  map[string]*Words `json:"fields,omitempty"`
}

// Words is the label and the description of one thing, in the language
// of the definition.
type Words struct {
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// Kind says what the definition translates, and Target its name.
func (d *WordsDef) Kind() (kind, target string) {
	switch {
	case d.Class != "":
		return WordsOfClass, d.Class
	case d.Table != "":
		return WordsOfTable, d.Table
	case d.Schema != "":
		return WordsOfSchema, d.Schema
	}
	return "", ""
}

// WordsName is the name a definition of words is kept and recorded
// under: what it translates, its name and the language,
// class/Invoice/pl. A class and a schema may share a name, so the kind
// is part of it.
func WordsName(kind, target, language string) string {
	return kind + "/" + target + "/" + language
}

// ParseWords decodes a definition of words. Unknown properties are
// findings, as in every definition.
func ParseWords(data []byte) (*WordsDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def WordsDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	named := 0
	for _, n := range []string{def.Class, def.Table, def.Schema} {
		if n != "" {
			named++
		}
	}
	switch {
	case named == 0:
		c.hint(CodeMissing, "/class", "a definition of words names what it translates",
			`write "class": "Invoice", or "table" or "schema" with its name`)
	case named > 1:
		c.hint(CodeBadValue, "/class", "a definition of words translates one class, one table or one schema",
			"write one definition for each")
	}
	if def.Version < 0 {
		c.add(CodeBadValue, "/version", "the version is a positive number")
	}
	if def.Language == "" {
		c.hint(CodeMissing, "/language", "a definition of words names its language",
			`write "language": "pl", a tag of BCP 47`)
	} else if canonical, why := languageTag(def.Language); why != "" {
		c.hint(CodeBadValue, "/language", quote(def.Language)+" is not a tag of BCP 47: "+why,
			"write a language of two or three letters, then a script or a region if it needs one: pl, de, pt-BR, zh-Hant")
	} else if canonical != def.Language {
		c.hint(CodeBadValue, "/language", "a tag of BCP 47 is written "+quote(canonical),
			"write "+quote(canonical)+": the language in small letters, a script with a capital, a region in capitals")
	}
	if c.failed() {
		return nil, c.list
	}
	return &def, nil
}

// WordsTarget is what a definition of words is checked against: the
// class, the table or the schema it translates, in its current version.
type WordsTarget struct {
	Class  *Class
	Table  *Table
	Schema *Schema
}

// CheckWords checks a definition of words against what it translates:
// every path is one the thing has, every entry says something, and no
// text is past the limit of a label (ADR-0068, item 6).
func CheckWords(def *WordsDef, t WordsTarget) Diagnostics {
	var c collector
	checkWords(&c, "", def.Label, def.Description)
	block := func(name string, entries map[string]*Words, has func(path string) (bool, string)) {
		for _, path := range sortedWords(entries) {
			where := "/" + name + "/" + path
			w := entries[path]
			if ok, hint := has(path); !ok {
				if hint == "" {
					hint = "take it out, or name one of the " + name + " the definition has"
				}
				c.hint(CodeBadValue, where, quote(path)+" is not one of the "+name+" of "+wordsTarget(t), hint)
				continue
			}
			if w == nil || w.Label == "" && w.Description == "" {
				c.hint(CodeMissing, where, "the words of "+quote(path)+" say nothing", "give a label, a description or both, or leave it out")
				continue
			}
			checkWords(&c, where, w.Label, w.Description)
		}
	}
	none := func(name string, entries map[string]*Words) {
		if len(entries) > 0 {
			c.hint(CodeNotAllowed, "/"+name, "a "+kindOf(t)+" has no "+name, "leave "+quote(name)+" out")
		}
	}
	switch {
	case t.Class != nil:
		cls := t.Class
		block("variables", def.Variables, func(path string) (bool, string) { return classPath(cls, path) })
		block("sections", def.Sections, func(path string) (bool, string) {
			_, ok := cls.Section(path)
			return ok, ""
		})
		block("states", def.States, func(path string) (bool, string) {
			return cls.States != nil && cls.States.Has(path), ""
		})
		block("transitions", def.Transitions, func(path string) (bool, string) {
			for _, tr := range cls.Transitions {
				if tr.Name != "" && strings.EqualFold(tr.Name, path) {
					return true, ""
				}
			}
			return false, "name a transition by its name; a transition without one has no words"
		})
		none("columns", def.Columns)
		none("fields", def.Fields)
	case t.Table != nil:
		tbl := t.Table
		block("columns", def.Columns, func(path string) (bool, string) {
			for _, f := range tbl.Fields {
				if strings.EqualFold(f.Name, path) {
					return true, ""
				}
			}
			return false, ""
		})
		for name, entries := range map[string]map[string]*Words{"variables": def.Variables, "sections": def.Sections, "states": def.States, "transitions": def.Transitions, "fields": def.Fields} {
			none(name, entries)
		}
	case t.Schema != nil:
		sch := t.Schema
		if def.Label != "" || def.Description != "" {
			c.hint(CodeNotAllowed, "/label", "a schema has no label and no description of its own", "give the words of its fields")
		}
		block("fields", def.Fields, func(path string) (bool, string) {
			for _, f := range sch.Type.Fields() {
				if strings.EqualFold(f.Name, path) {
					return true, ""
				}
			}
			return false, ""
		})
		for name, entries := range map[string]map[string]*Words{"variables": def.Variables, "sections": def.Sections, "states": def.States, "transitions": def.Transitions, "columns": def.Columns} {
			none(name, entries)
		}
	}
	return c.list
}

// UnusedWords notes each path of a definition of words that the current
// version of what it translates no longer has (ADR-0068, item 6): a
// version that drops a variable leaves its words in place, unused. A
// note refuses nothing.
func UnusedWords(def *WordsDef, t WordsTarget) Diagnostics {
	var notes Diagnostics
	for _, d := range CheckWords(def, t) {
		if d.Code == CodeBadValue && strings.Count(d.Path, "/") == 2 {
			notes = append(notes, Diagnostic{Code: CodeUnusedWords, Path: d.Path, Pos: -1, Note: true,
				Message: d.Message + ", so its words are not used",
				Hint:    "define the words again without it"})
		}
	}
	return notes
}

// classPath says whether a class has the variable, or the column of a
// rows variable written as lines.qty.
func classPath(cls *Class, path string) (bool, string) {
	name, column, nested := strings.Cut(path, ".")
	v, ok := cls.Variable(name)
	if !ok {
		return false, ""
	}
	if !nested {
		return true, ""
	}
	raw := bytes.TrimSpace(v.Def.Schema)
	if len(raw) == 0 || raw[0] != '{' {
		if len(raw) > 0 {
			var schema string
			_ = json.Unmarshal(raw, &schema)
			return false, "the fields of " + name + " are those of the schema " + schema + ": give their words with \"schema\": " + quote(schema)
		}
		return false, name + " has no columns"
	}
	var columns map[string]FieldDef
	if json.Unmarshal(raw, &columns) != nil {
		return false, ""
	}
	for col := range columns {
		if strings.EqualFold(col, column) {
			return true, ""
		}
	}
	return false, ""
}

func wordsTarget(t WordsTarget) string {
	switch {
	case t.Class != nil:
		return "the class " + t.Class.Name
	case t.Table != nil:
		return "the table " + t.Table.Name
	case t.Schema != nil:
		return "the schema " + t.Schema.Name
	}
	return ""
}

func kindOf(t WordsTarget) string {
	switch {
	case t.Table != nil:
		return WordsOfTable
	case t.Schema != nil:
		return WordsOfSchema
	}
	return WordsOfClass
}

func sortedWords(m map[string]*Words) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// languageTag reads a language tag of BCP 47 (RFC 5646) and answers it
// in its canonical case, or why it is not one. It checks the shape of
// the tag, not the registry: pl, pt-BR, zh-Hant-TW, de-CH-1901 and
// en-x-acme are tags, and a tag nobody registered reads as one too.
func languageTag(tag string) (string, string) {
	if tag == "" || utf8.RuneCountInString(tag) > 64 {
		return "", "it is empty or too long"
	}
	parts := strings.Split(tag, "-")
	out := make([]string, 0, len(parts))
	alpha := func(s string) bool {
		for _, r := range s {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
		}
		return s != ""
	}
	digits := func(s string) bool {
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return s != ""
	}
	alnum := func(s string) bool {
		for _, r := range s {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				return false
			}
		}
		return s != ""
	}
	i := 0
	if !alpha(parts[0]) || len(parts[0]) < 2 || len(parts[0]) > 3 {
		return "", "it begins with a language of two or three letters"
	}
	out = append(out, strings.ToLower(parts[0]))
	i++
	// Up to three extended languages of three letters.
	for n := 0; n < 3 && i < len(parts) && len(parts[i]) == 3 && alpha(parts[i]); n++ {
		out = append(out, strings.ToLower(parts[i]))
		i++
	}
	if i < len(parts) && len(parts[i]) == 4 && alpha(parts[i]) {
		p := strings.ToLower(parts[i])
		out = append(out, strings.ToUpper(p[:1])+p[1:])
		i++
	}
	if i < len(parts) && (len(parts[i]) == 2 && alpha(parts[i]) || len(parts[i]) == 3 && digits(parts[i])) {
		out = append(out, strings.ToUpper(parts[i]))
		i++
	}
	for i < len(parts) && alnum(parts[i]) && (len(parts[i]) >= 5 && len(parts[i]) <= 8 || len(parts[i]) == 4 && digits(parts[i][:1])) {
		out = append(out, strings.ToLower(parts[i]))
		i++
	}
	// Extensions, a singleton then subtags of two to eight, and a private
	// use, x then subtags of one to eight.
	for i < len(parts) {
		single := strings.ToLower(parts[i])
		if len(single) != 1 || !alnum(single) {
			return "", quote(parts[i]) + " is not a part of a tag where it stands"
		}
		out = append(out, single)
		i++
		least := 2
		if single == "x" {
			least = 1
		}
		n := 0
		for i < len(parts) && alnum(parts[i]) && len(parts[i]) >= least && len(parts[i]) <= 8 {
			out = append(out, strings.ToLower(parts[i]))
			i++
			n++
		}
		if n == 0 {
			return "", quote(single) + " is followed by nothing"
		}
		if single == "x" && i < len(parts) {
			return "", quote(parts[i]) + " is not a part of a tag where it stands"
		}
	}
	return strings.Join(out, "-"), ""
}
