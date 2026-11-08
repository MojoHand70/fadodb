package model

import (
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The columns the generated tables and views add, reserved for variables
// (ADR-0013), and the subset a rows child table adds, reserved for schema
// fields (ADR-0031). ADR-0031 also lists value, the one column an
// array's child table has; a rows child table never has it, and the
// Quantity schema of ADR-0032 has a field of that name, so value is not
// reserved for fields here. The two records disagree and the owner
// decides which one gives.
var (
	reservedVariableNames = map[string]bool{
		"id": true, "title": true, "state": true, "version": true, "created_at": true,
		"updated_at": true, "state_entered_at": true, "valid_from": true, "valid_to": true,
		"actor_id": true, "event_id": true, "doc_id": true, "idx": true, "value": true,
	}
	reservedFieldNames = map[string]bool{"doc_id": true, "version": true, "idx": true}
)

// PathSeparator joins the parts of a field path in a column name
// (ADR-0031); it is reserved in every name.
const PathSeparator = "__"

// identReason says why a name is not an identifier, or "" when it is:
// a letter or underscore followed by letters, digits and underscores, not
// containing the path separator and not ending in the currency suffix.
func identReason(name string) string {
	if name == "" {
		return "a name is required"
	}
	for i, r := range name {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return "a name may not start with a digit"
			}
		default:
			return "a name is letters, digits and underscores"
		}
	}
	if strings.Contains(name, PathSeparator) {
		return "a name may not contain " + PathSeparator + ", the separator of field paths in column names"
	}
	if strings.HasSuffix(strings.ToLower(name), formula.CurrencySuffix) {
		return "a name may not end in " + formula.CurrencySuffix + ", the suffix of a money variable's currency column"
	}
	return ""
}

// checkName validates a variable, state, transition, class or schema
// name at path and records what is wrong. reserved is the set of names
// the generated tables take, or nil.
func (c *collector) checkName(path, name string, reserved map[string]bool) bool {
	if reason := identReason(name); reason != "" {
		c.add(CodeBadName, path, reason+": "+quote(name))
		return false
	}
	if formula.IsKeyword(name) {
		c.addf(CodeReservedName, path, "%s is a keyword of the formula language", quote(name))
		return false
	}
	if reserved[strings.ToLower(name)] {
		c.addf(CodeReservedName, path, "%s is a column the generated tables add", quote(name))
		return false
	}
	return true
}

func quote(s string) string { return "\"" + s + "\"" }

// upper is the key names are compared by: case-insensitively (ADR-0013).
func upper(s string) string { return strings.ToUpper(s) }
