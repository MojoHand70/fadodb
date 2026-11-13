package model

import "strings"

// SubstitutionClass is the name of the class the engine brings to every
// database (ADR-0053, item 7): a document of it says who may act for
// whom, from when, until when and on which classes. The name is the
// engine's, and no definition a designer sends may take it.
const SubstitutionClass = "Substitution"

// The states of a substitution, and the transition that withdraws one.
const (
	SubstitutionPlanned   = "planned"
	SubstitutionActive    = "active"
	SubstitutionEnded     = "ended"
	SubstitutionWithdrawn = "withdrawn"
	SubstitutionWithdraw  = "withdraw"
)

// engineClass reports whether the name is that of a class the engine
// brings, in any case: class names differ in more than case.
func engineClass(name string) bool { return strings.EqualFold(name, SubstitutionClass) }

// SubstitutionDefinition is the definition of the class as the engine
// defines it when it opens a database (ADR-0053).
//
//   - by may act for for, from from until until; until left out holds
//     until the substitution is withdrawn, and classes left out holds for
//     every class.
//   - The document is planned until its from, active until its until and
//     ended after it, by the timed transitions of ADR-0047; the check of
//     a call reads the dates, so nothing waits for the worker.
//   - The person gives a substitution for themselves, and a database
//     administrator for somebody else with a reason, never to themselves
//     (item 6). Nobody gives one while acting for somebody, to
//     themselves, or one that ends before it begins.
//   - The person and a database administrator withdraw it; both see it,
//     and so does the one who holds it.
//   - Nothing changes it and nothing deletes it: it is the evidence of
//     who was allowed.
func SubstitutionDefinition() []byte { return []byte(substitutionJSON) }

const substitutionJSON = `{
  "name": "Substitution",
  "label": "Substitution",
  "plural": "Substitutions",
  "description": "Who may act for whom, from when, until when and on which classes (ADR-0053). The engine brings this class and asks it of every call that names somebody the caller acts for.",
  "title": "by & \" for \" & for",
  "variables": {
    "by":      {"type": "text", "required": true, "index": true},
    "for":     {"type": "text", "required": true, "default": "USER()", "index": true},
    "from":    {"type": "datetime", "required": true, "default": "NOW()"},
    "until":   {"type": "datetime"},
    "classes": {"type": "array<text>"},
    "reason":  {"type": "text"}
  },
  "states": {"initial": "planned", "list": ["planned", "active", "ended", "withdrawn"], "final": ["ended", "withdrawn"]},
  "transitions": [
    {"from": "planned", "to": "active", "at": "from"},
    {"from": "active", "to": "ended", "at": "until"},
    {"name": "withdraw", "from": ["planned", "active"], "to": "withdrawn"}
  ],
  "access": {
    "read":   "for = USER() OR by = USER() OR INGROUP(\"administrators\")",
    "create": "ISBLANK(ACTINGFOR()) AND by <> for AND (ISBLANK(until) OR until > from) AND (for = USER() OR (INGROUP(\"administrators\") AND by <> USER() AND NOT ISBLANK(reason)))",
    "update": "FALSE",
    "delete": "FALSE",
    "transition": {"withdraw": "ISBLANK(ACTINGFOR()) AND (for = USER() OR INGROUP(\"administrators\"))"}
  }
}`
