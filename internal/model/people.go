package model

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// What a class says to people (ADR-0068): the label and the description
// of the class, its variables, its states and its transitions, the
// columns of its tables and the fields of its schemas, whether a variable
// is a column of The List, and how a value is shown. None of it is a
// value: nothing of it is stored with a document, and no formula, command
// or export reads it. It is checked here, when the definition is, and
// answered as it was written.

// maxWords is the longest a label or a description may be, in code
// points: the default text limit of a database (ADR-0021). A definition
// is checked without a database too, by fadodb class check, so the limit
// is the default and not a database's own.
const maxWords = formula.DefaultMaxText

// checkWords checks the label and the description of the thing at path.
func checkWords(c *collector, path, label, description string) {
	if n := utf8.RuneCountInString(label); n > maxWords {
		c.addf(CodeBadValue, path+"/label", "the label has %d code points, and the limit is %d (ADR-0021)", n, maxWords)
	}
	if n := utf8.RuneCountInString(description); n > maxWords {
		c.addf(CodeBadValue, path+"/description", "the description has %d code points, and the limit is %d (ADR-0021)", n, maxWords)
	}
}

// checkField checks what a column or a field of the type says to
// people.
func checkField(c *collector, path string, f FieldDef, t formula.Type) {
	checkWords(c, path, f.Label, f.Description)
	checkFormat(c, path+"/format", f.Format, t)
}

// compileWords checks what the class says to people. The variables have
// their types by now, a computed one its formula's, so that a format is
// checked against the type the value has.
func compileWords(c *collector, cls *Class, def *Definition) {
	checkWords(c, "", def.Label, def.Description)
	for _, v := range cls.Variables {
		path := "/variables/" + v.Name
		checkWords(c, path, v.Def.Label, v.Def.Description)
		checkFormat(c, path+"/format", v.Def.Format, v.Type)
	}
	if def.States != nil {
		for _, block := range []struct {
			name  string
			words map[string]string
		}{{"labels", def.States.Labels}, {"descriptions", def.States.Descriptions}} {
			for _, name := range sortedKeys(block.words) {
				path := "/states/" + block.name + "/" + name
				if cls.States == nil || !cls.States.Has(name) {
					c.addf(CodeUnknownState, path, "unknown state %s", quote(name))
					continue
				}
				if n := utf8.RuneCountInString(block.words[name]); n > maxWords {
					c.addf(CodeBadValue, path, "the text has %d code points, and the limit is %d (ADR-0021)", n, maxWords)
				}
			}
		}
	}
	for i, t := range def.Transitions {
		checkWords(c, fmt.Sprintf("/transitions/%d", i), t.Label, t.Description)
	}
}

// noDescription notes each variable, section, state and transition that has no
// description (ADR-0068, item 3). A note refuses nothing: a person may
// define a class quickly, and an agent loops until no note is left, as a
// programmer writes a comment for each thing a reader needs.
func noDescription(def *Definition) Diagnostics {
	var notes Diagnostics
	note := func(path, message, hint string) {
		notes = append(notes, Diagnostic{Code: CodeNoDescription, Path: path, Pos: -1, Note: true, Message: message, Hint: hint})
	}
	for _, name := range def.VariableOrder {
		if v := def.Variables[name]; v != nil && v.Description == "" {
			note("/variables/"+name+"/description", "variable "+name+" has no description: a person sees no help beside it, and an agent knows it by its name alone",
				"add \"description\": what it holds and where it comes from, as a programmer writes a comment")
		}
	}
	var outline func(path string, defs map[string]*SectionDef, order []string)
	outline = func(path string, defs map[string]*SectionDef, order []string) {
		if order == nil {
			order = sortedSections(defs)
		}
		for _, name := range order {
			sd := defs[name]
			if sd == nil {
				continue
			}
			if sd.Description == "" {
				note(path+"/"+name+"/description", "section "+name+" has no description: nobody is told what this part of the document is for",
					"add \"description\": what the part holds and who fills it in")
			}
			outline(path+"/"+name+"/sections", sd.Sections, sd.Order)
		}
	}
	outline("/sections", def.Sections, def.SectionOrder)
	if def.States != nil {
		for _, name := range def.States.List {
			if def.States.Descriptions[name] == "" {
				note("/states/descriptions/"+name, "state "+name+" has no description: nobody is told what a document waits for in it",
					"add it under states.descriptions: \""+name+"\": \"what happens while a document is here\"")
			}
		}
	}
	for i, t := range def.Transitions {
		if t.Description != "" {
			continue
		}
		what := "transition " + t.Name
		if t.Name == "" {
			what = "the transition to " + t.To
		}
		note(fmt.Sprintf("/transitions/%d/description", i), what+" has no description: nobody is told what taking it means",
			"add \"description\": what it does and who takes it")
	}
	return notes
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
