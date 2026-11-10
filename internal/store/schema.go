package store

import (
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// engineDDL makes the tables every database has (docs/architecture.md,
// ADR-0006, ADR-0014, ADR-0026). Every statement is idempotent, since Open
// runs them on every start.
var engineDDL = []string{
	`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS schemas (name TEXT PRIMARY KEY COLLATE NOCASE, version INTEGER NOT NULL, json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS classes (name TEXT PRIMARY KEY COLLATE NOCASE, version INTEGER NOT NULL, json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS class_versions (class TEXT NOT NULL COLLATE NOCASE, version INTEGER NOT NULL, json TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY (class, version))`,
	`CREATE TABLE IF NOT EXISTS tables_registry (name TEXT PRIMARY KEY COLLATE NOCASE, version INTEGER NOT NULL, binding TEXT NOT NULL, json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS events (event_id TEXT PRIMARY KEY, class TEXT NOT NULL, doc_id TEXT NOT NULL, kind TEXT NOT NULL, name TEXT, from_state TEXT, to_state TEXT,
		actor_id TEXT, before_version INTEGER, after_version INTEGER, cause_event_id TEXT, command_key TEXT, command_hash TEXT, business_date INTEGER NOT NULL, ts INTEGER NOT NULL)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS events_command_key ON events (command_key)`,
	`CREATE INDEX IF NOT EXISTS events_class_doc ON events (class, doc_id, ts)`,
	`CREATE INDEX IF NOT EXISTS events_business_date ON events (business_date)`,
	`CREATE TABLE IF NOT EXISTS event_docs (event_id TEXT NOT NULL, class TEXT NOT NULL, doc_id TEXT NOT NULL, qualifier TEXT NOT NULL, PRIMARY KEY (event_id, class, doc_id))`,
	`CREATE TABLE IF NOT EXISTS blobs (hash TEXT PRIMARY KEY, size INTEGER NOT NULL, bytes BLOB NOT NULL, put_at INTEGER NOT NULL)`,
}

// The system columns of a current row (ADR-0013, ADR-0018) and the ones a
// history row adds (ADR-0014).
var (
	systemColumns  = []string{"id", "title", "state", "version", "class_version", "created_at", "updated_at", "state_entered_at"}
	historyColumns = []string{"valid_from", "valid_to", "actor_id", "event_id"}
	childKey       = []string{"doc_id", "version", "idx"}
)

const (
	currentSystemDDL = `"id" TEXT NOT NULL, "title" TEXT, "state" TEXT, "version" INTEGER NOT NULL, "class_version" INTEGER NOT NULL, "created_at" INTEGER NOT NULL, "updated_at" INTEGER NOT NULL, "state_entered_at" INTEGER`
	historySystemDDL = currentSystemDDL + `, "valid_from" INTEGER NOT NULL, "valid_to" INTEGER, "actor_id" TEXT, "event_id" TEXT NOT NULL`
	childKeyDDL      = `"doc_id" TEXT NOT NULL, "version" INTEGER NOT NULL, "idx" INTEGER NOT NULL`
)

// sqlType is SQLite's storage class for values of the kind: a date as
// INTEGER days and a datetime as INTEGER seconds (ADR-0023), an exact
// value as its INTEGER mantissa (ADR-0022), a bool as 0 or 1.
func sqlType(k formula.Kind) string {
	switch k {
	case formula.KindNumber:
		return "REAL"
	case formula.KindBool, formula.KindDecimal, formula.KindMoney, formula.KindDate, formula.KindDateTime:
		return "INTEGER"
	}
	return "TEXT"
}

// quote quotes an identifier. Names are identifiers already; quoting
// keeps a class called Order or a variable called Group out of SQL's way.
func quote(ident string) string { return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"` }

// lit quotes a text literal.
func lit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = quote(n)
	}
	return out
}

func columnDDL(cols []model.Column) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = quote(c.Name) + " " + sqlType(c.Type.Kind)
	}
	return strings.Join(parts, ", ")
}

func columnNames(cols []model.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// createDDL is the DDL of a class: the current, history and child tables
// with their indexes, and the views a user sees (ADR-0014).
func (c *class) createDDL() []string {
	return append(c.tableDDL(), c.viewDDL()...)
}

// tableDDL makes the tables and indexes of a class.
func (c *class) tableDDL() []string {
	cls := c.cls
	vars := columnDDL(cls.Columns)
	if vars != "" {
		vars = ", " + vars
	}
	ddl := []string{
		"CREATE TABLE " + quote(c.table) + " (" + currentSystemDDL + vars + `, PRIMARY KEY ("id"))`,
		"CREATE TABLE " + quote(c.history) + " (" + historySystemDDL + vars + `, PRIMARY KEY ("id", "version"))`,
		"CREATE INDEX " + quote(c.history+"_valid") + " ON " + quote(c.history) + ` ("id", "valid_from")`,
	}
	for _, col := range cls.Columns {
		if col.Index {
			ddl = append(ddl, "CREATE INDEX "+quote(c.table+"_"+col.Name)+" ON "+quote(c.table)+" ("+quote(col.Name)+")")
		}
	}
	for _, ch := range c.children {
		ddl = append(ddl, ch.tableDDL())
	}
	return ddl
}

// tableDDL makes a child table.
func (ch *child) tableDDL() string {
	cd := columnDDL(ch.def.Columns)
	if cd != "" {
		cd = ", " + cd
	}
	return "CREATE TABLE " + quote(ch.table) + " (" + childKeyDDL + cd + `, PRIMARY KEY ("doc_id", "version", "idx"))`
}

// viewDDL makes the views of a class: the current and history documents,
// the events, and each composite current and per version.
func (c *class) viewDDL() []string {
	cls := c.cls
	cols := quoteAll(columnNames(cls.Columns))
	// A view shows the time of a triggered variable's moment as
	// <name>__at beside the stored <name>__asof and <name>__error.
	all := func(prefix []string, table string) string {
		list := append(quoteAll(prefix), cols...)
		names := c.atNames()
		for i, expr := range c.atExprs(table) {
			list = append(list, expr+" AS "+quote(names[i]))
		}
		return strings.Join(list, ", ")
	}
	ddl := []string{
		"CREATE VIEW " + quote(cls.Name) + " AS SELECT " + all(systemColumns, c.table) + " FROM " + quote(c.table),
		"CREATE VIEW " + quote(cls.Name+"_history") + " AS SELECT " + all(append(append([]string{}, systemColumns...), historyColumns...), c.history) + " FROM " + quote(c.history),
		"CREATE VIEW " + quote(cls.Name+"_events") + ` AS SELECT e.*, d."qualifier" FROM "events" e JOIN "event_docs" d ON d."event_id" = e."event_id" WHERE d."class" = ` + lit(cls.Name),
	}
	if cls.States != nil {
		ddl = append(ddl, c.statesViewDDL())
	}
	for _, ch := range c.children {
		ccols := strings.Join(append(quoteAll(childKey), quoteAll(columnNames(ch.def.Columns))...), ", ")
		ddl = append(ddl,
			"CREATE VIEW "+quote(cls.Name+"_"+ch.v.Name)+" AS SELECT "+prefixed("c", ccols)+" FROM "+quote(ch.table)+" c JOIN "+quote(c.table)+` d ON d."id" = c."doc_id" AND d."version" = c."version"`,
			"CREATE VIEW "+quote(cls.Name+"_"+ch.v.Name+"_history")+" AS SELECT "+ccols+" FROM "+quote(ch.table),
		)
	}
	return ddl
}

// statesViewDDL makes the <Class>_states view (ADR-0014, ADR-0040): one
// row per visit of a document to a state, over the history table with
// window functions. A visit begins at a history row whose state differs
// from the previous version's; it ends where the next visit begins, so
// left_at, duration and exit_version are null while the visit is current.
// The entry version is the entry snapshot and the exit version the row
// the leaving transition's guard saw. A visit of zero duration inside a
// chain is a row like any other.
func (c *class) statesViewDDL() string {
	window := `OVER (PARTITION BY "id" ORDER BY "version")`
	return "CREATE VIEW " + quote(c.cls.Name+"_states") + " AS WITH h AS (" +
		`SELECT "id", "state", "version", "valid_from", lag("state") ` + window + ` AS prev_state FROM ` + quote(c.history) +
		`), e AS (SELECT "id", "state", "version", "valid_from", lead("version") ` + window + ` AS next_version, lead("valid_from") ` + window + ` AS next_from` +
		` FROM h WHERE prev_state IS NULL OR prev_state IS NOT "state")` +
		` SELECT "id", "state", "valid_from" AS "entered_at", next_from AS "left_at", next_from - "valid_from" AS "duration", "version" AS "entry_version", next_version - 1 AS "exit_version" FROM e`
}

// prefixed qualifies every column of a comma-separated quoted list.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ", ")
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return strings.Join(parts, ", ")
}

// dropDDL removes everything createDDL made.
func (c *class) dropDDL() []string {
	ddl := c.dropViewDDL()
	for _, t := range c.tableNames() {
		ddl = append(ddl, "DROP TABLE IF EXISTS "+quote(t))
	}
	return ddl
}

// dropViewDDL removes the views of a class.
func (c *class) dropViewDDL() []string {
	var ddl []string
	for _, v := range c.viewNames() {
		ddl = append(ddl, "DROP VIEW IF EXISTS "+quote(v))
	}
	return ddl
}

func (c *class) tableNames() []string {
	names := []string{c.table, c.history}
	for _, ch := range c.children {
		names = append(names, ch.table)
	}
	return names
}

func (c *class) viewNames() []string {
	names := []string{c.cls.Name, c.cls.Name + "_history", c.cls.Name + "_events"}
	if c.cls.States != nil {
		names = append(names, c.cls.Name+"_states")
	}
	for _, ch := range c.children {
		names = append(names, c.cls.Name+"_"+ch.v.Name, c.cls.Name+"_"+ch.v.Name+"_history")
	}
	return names
}
