package model

import (
	"sort"
	"strconv"
)

// Sections are the outline of a class (ADR-0069): which variables belong
// together, a section inside a section as a subchapter inside a chapter.
// A section says what it is to people, a label and a description, and
// stands where a variable stands in the rights table, so that a designer
// gives the rights of a part in one line. A section is not a value:
// nothing of it is stored with a document, and no formula, filter,
// export or view reads it.

// MaxSectionDepth is how deep sections may nest, the top level counted
// as the first, as objects nest below a variable (ADR-0031).
const MaxSectionDepth = 4

// Section is one section of the outline, checked.
type Section struct {
	// Name is the name as written.
	Name        string
	Label       string
	Description string
	// Variables are the names of the variables it holds, as the class
	// declares them, in the written order.
	Variables []string
	// Sections are the sections inside it, in the written order.
	Sections []*Section
}

// Section finds a section of the outline by name, case-insensitively,
// however deep it is.
func (c *Class) Section(name string) (*Section, bool) {
	s, ok := c.sectionByName[upper(name)]
	return s, ok
}

// SectionsOf names the section that holds a variable and each section
// around it, outwards: the order a rule is looked for in (ADR-0069,
// item 4). A variable in no section has none.
func (c *Class) SectionsOf(variable string) []string {
	return c.sectionsOf[upper(variable)]
}

// compileSections checks the outline against the variables of the class
// and keeps, for each variable, the sections around it.
func compileSections(c *collector, cls *Class, def *Definition) {
	if len(def.Sections) == 0 {
		return
	}
	cls.sectionByName = map[string]*Section{}
	cls.sectionsOf = map[string][]string{}
	where := map[string]string{} // a variable listed, to the place it was listed at
	// A variable the class declares and refuses for its own fault is
	// not a finding of the outline too.
	declared := map[string]bool{}
	for _, name := range def.VariableOrder {
		declared[upper(name)] = true
	}
	var level func(path string, defs map[string]*SectionDef, order []string, depth int, around []string) []*Section
	level = func(path string, defs map[string]*SectionDef, order []string, depth int, around []string) []*Section {
		if order == nil {
			order = sortedSections(defs)
		}
		var out []*Section
		for _, name := range order {
			spath := path + "/" + name
			sd := defs[name]
			if !c.checkName(spath, name, nil) {
				continue
			}
			if _, dup := cls.sectionByName[upper(name)]; dup {
				c.addf(CodeDuplicate, spath, "section %s is declared twice in the outline; names differ in case only", quote(name))
				continue
			}
			if v, clash := cls.Variable(name); clash {
				c.hint(CodeDuplicate, spath, "section "+quote(name)+" has the name of variable "+quote(v.Name),
					"give the section another name: rights and the words name a section by its name alone")
				continue
			}
			if sd == nil {
				c.hint(CodeMissing, spath, "a section is an object with its variables, its sections or both",
					"write it as {\"label\": ..., \"variables\": [...]}")
				continue
			}
			if depth > MaxSectionDepth {
				c.hint(CodeTooDeep, spath, "sections nest at most "+strconv.Itoa(MaxSectionDepth)+" deep, the top level counted as the first",
					"list the variables of the fifth level in the section around it")
				continue
			}
			s := &Section{Name: name, Label: sd.Label, Description: sd.Description}
			cls.sectionByName[upper(name)] = s
			checkWords(c, spath, sd.Label, sd.Description)
			if len(sd.Variables) == 0 && len(sd.Sections) == 0 {
				c.hint(CodeMissing, spath, "section "+quote(name)+" holds no variable and no section",
					"list its variables under \"variables\", or leave the section out")
			}
			chain := append([]string{name}, around...)
			for i, vname := range sd.Variables {
				vpath := spath + "/variables/" + strconv.Itoa(i)
				v, ok := cls.Variable(vname)
				if !ok && declared[upper(vname)] {
					continue
				}
				if !ok {
					c.hint(CodeUnknownVariable, vpath, "section "+quote(name)+" lists "+quote(vname)+", and the class has no such variable",
						"take it out of the section, or declare it under \"variables\"")
					continue
				}
				if first, dup := where[upper(v.Name)]; dup {
					c.hint(CodeDuplicate, vpath, "variable "+v.Name+" is listed twice in the outline, first at "+first,
						"a variable belongs to one section: take it out of one of them")
					continue
				}
				where[upper(v.Name)] = vpath
				s.Variables = append(s.Variables, v.Name)
				cls.sectionsOf[upper(v.Name)] = chain
			}
			s.Sections = level(spath+"/sections", sd.Sections, sd.Order, depth+1, chain)
			out = append(out, s)
		}
		return out
	}
	cls.Sections = level("/sections", def.Sections, def.SectionOrder, 1, nil)
}

// sortedSections is the order of a level built in code, without a
// written order.
func sortedSections(defs map[string]*SectionDef) []string {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
