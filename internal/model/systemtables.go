package model

import "strings"

// The app System and its tables (ADR-0092). Every database has the app
// System, the engine's own, and four tables in it whose rows are what the
// database holds now: its apps, its classes, its users and its groups. A
// formula, a page and an agent read them as they read any table; nobody
// writes them, since the engine keeps their rows itself.

// SystemApp is the name of the app the engine brings to every database.
const SystemApp = "System"

// The names of the system tables. A table and a class a designer defines
// may not take them.
const (
	SystemApps    = "SystemApps"
	SystemClasses = "SystemClasses"
	SystemUsers   = "SystemUsers"
	SystemGroups  = "SystemGroups"
)

// SystemTableNames lists the system tables in the order the engine
// defines them.
func SystemTableNames() []string {
	return []string{SystemApps, SystemClasses, SystemUsers, SystemGroups}
}

// IsSystemTable reports whether the name is that of a system table, in
// any case: table names differ in more than case.
func IsSystemTable(name string) bool {
	for _, n := range SystemTableNames() {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// SystemAppDefinition is the app as the engine defines it in a database
// that has no app of the name. One that has it keeps its own words.
func SystemAppDefinition() []byte { return []byte(systemAppJSON) }

const systemAppJSON = `{"name":"System","label":"System","description":"The engine's own app: the tables that say what the database holds, its apps, its classes, its users and its groups."}`

// SystemTableDefinition is the definition of a system table as get_table
// answers it, nil for a name that is none. Every user may read the rows;
// the rule says so as any table's would.
func SystemTableDefinition(name string) []byte {
	for _, t := range systemTables {
		if strings.EqualFold(t.name, name) {
			return []byte(t.json)
		}
	}
	return nil
}

var systemTables = []struct{ name, json string }{
	{SystemApps, `{"name":"SystemApps","app":"System","label":"Apps","description":"The apps of the database, one row each, with the words of their current version.",` +
		`"columns":{"name":{"type":"text"},"label":{"type":"text"},"description":{"type":"text"},"icon":{"type":"text"},"color":{"type":"text"},` +
		`"version":{"type":"int"},"created_at":{"type":"datetime"},"updated_at":{"type":"datetime"}},"key":["name"],"access":{"read":"TRUE"}}`},
	{SystemClasses, `{"name":"SystemClasses","app":"System","label":"Classes","description":"The classes of the database, one row each, with the app and the words of their current version.",` +
		`"columns":{"name":{"type":"text"},"app":{"type":"text"},"label":{"type":"text"},"description":{"type":"text"},` +
		`"version":{"type":"int"},"created_at":{"type":"datetime"},"updated_at":{"type":"datetime"}},"key":["name"],"access":{"read":"TRUE"}}`},
	{SystemUsers, `{"name":"SystemUsers","app":"System","label":"Users","description":"The users of the database, one row each, the disabled ones among them: people, agents and services, with their groups.",` +
		`"columns":{"id":{"type":"text"},"kind":{"type":"text"},"name":{"type":"text"},"email":{"type":"text"},"groups":{"type":"json"},"system_groups":{"type":"json"},` +
		`"disabled":{"type":"bool"},"created_at":{"type":"datetime"},"updated_at":{"type":"datetime"}},"key":["id"],"access":{"read":"TRUE"}}`},
	{SystemGroups, `{"name":"SystemGroups","app":"System","label":"Groups","description":"The groups of the database, one row each, with how many users are in it.",` +
		`"columns":{"name":{"type":"text"},"label":{"type":"text"},"description":{"type":"text"},"members":{"type":"int"},` +
		`"created_at":{"type":"datetime"},"updated_at":{"type":"datetime"}},"key":["name"],"access":{"read":"TRUE"}}`},
}
