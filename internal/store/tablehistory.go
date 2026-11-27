package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The history of a table's rows (ADR-0099, step 3; the owner's "1. B",
// 2026-11-26): every table keeps every row as it was, until a change
// replaced or removed it, and who made each change, unless its
// definition says "history": false.
//
// The layout, beside tbl_<Name>:
//
//   - fado_table_changes: one row a change, by its number, seq, with who
//     made it and of what kind; its event_id is an event id, so it
//     orders with the commits of documents, and the token of the
//     table's rows (ADR-0094) is it;
//   - the column __since of every row: the number of the change that
//     gave the row the values it has, blank for a row older than the
//     history (schema 16), a few bytes where an id would be 26;
//   - tbl_<Name>__history: each row as it was, from __since to __until,
//     the change that replaced or removed it.
//
// SQLite writes the history itself, by two triggers on the table, so
// that every way of writing a row keeps it, and the engine binds nothing
// more to a statement than it did: a change is written first in its
// transaction, and the triggers and the statements that insert read its
// number as the last of fado_table_changes. A row written again with the values it
// has is no change and keeps no history. A name with "__" in it is none
// a table or a column may have (model.PathSeparator), so these never
// meet a name of the database's own.

const (
	sinceColumn   = "__since"
	untilColumn   = "__until"
	historySuffix = "__history"
	// historyKey, in metadata, with the table's name in upper case: the
	// moment its history began, for a table that began to keep it after
	// it held rows, brought from schema 15 or turned on by a version.
	// Before it, what a row held is not known. A table that kept it from
	// its first version has none.
	historyKey = "history:"
)

// The kinds of change of a table's rows that fado_table_changes keeps. A
// rule's write is kept under the name of its action: table.insert,
// table.update or table.delete.
const (
	ChangeImport  = "import"
	ChangeReplace = "replace"
	ChangeDelete  = "delete"
	ChangeDefine  = "define"
)

// fado_table_changes is read by its number, from a row or a row of a
// history: one page in a new database, with no index beside it. Its
// numbers and its ids rise together, since one writer writes them
// (changeBefore).
var tableChangesDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_table_changes (seq INTEGER PRIMARY KEY, event_id TEXT NOT NULL, table_key TEXT NOT NULL, kind TEXT NOT NULL, actor_id TEXT, actor_kind TEXT, actor_for TEXT)`,
	// The token of the table's rows is the change (ADR-0094).
	`CREATE TRIGGER IF NOT EXISTS fado_table_changes_token AFTER INSERT ON fado_table_changes BEGIN ` +
		`INSERT OR REPLACE INTO metadata (key, value) VALUES ('` + rowsKey + `' || NEW.table_key, NEW.event_id); END`,
}

// historyName is the table of the history of the rows.
func (t *table) historyName() string { return t.name + historySuffix }

// tableKey is the table's name as the token and the changes keep it.
func (t *table) tableKey() string { return strings.ToUpper(t.def.Name) }

// changeKinds are the kinds of change as changeSQL binds them, by their
// place; actorKinds the kinds of caller (ADR-0050).
var (
	changeKinds = []string{"", ChangeImport, ChangeReplace, ChangeDelete, ChangeDefine, model.ActionTableInsert, model.ActionTableUpdate, model.ActionTableDelete}
	actorKinds  = []string{"", ActorPerson, ActorAgent, ActorService}
)

// changeSQL writes a change of the table. The name is in the text. A
// change binds one text, its id with who made it after (idGen.change),
// and two small numbers, its kind and the caller's (changeKinds,
// actorKinds), each once and by no name: the driver keeps a buffer for
// each text it binds and makes the name of a named parameter anew on
// every execution, and a change is on the path of every value that
// follows a table (BenchmarkFollow), which the gate holds to its
// allocations.
func changeSQL(key string) string {
	codes := func(col string, names []string) string {
		var b strings.Builder
		b.WriteString("CASE " + col)
		for i, n := range names {
			if n != "" {
				b.WriteString(" WHEN " + strconv.Itoa(i) + " THEN '" + n + "'")
			}
		}
		b.WriteString(" END")
		return b.String()
	}
	n := "CAST(substr(v, 27, 6) AS INTEGER)"
	return `INSERT INTO fado_table_changes (event_id, table_key, kind, actor_id, actor_kind, actor_for) SELECT substr(v, 1, 26), '` +
		strings.ReplaceAll(key, "'", "''") + `', ` + codes("k", changeKinds) + `, nullif(substr(v, 33, ` + n + `), ''), ` + codes("a", actorKinds) +
		`, nullif(substr(v, 33 + ` + n + `), '') FROM (SELECT ? AS v, ? AS k, ? AS a)`
}

// placeOf is the place of a name among the names, 0 for none.
func placeOf(names []string, name string) int64 {
	for i, n := range names {
		if n == name {
			return int64(i)
		}
	}
	return 0
}

// tokenSQL reads the number of the change being written, inside its
// transaction: the last one, since the writer writes it first.
func (t *table) tokenSQL() string {
	return `(SELECT max(seq) FROM fado_table_changes)`
}

// historyDDL makes the table of the history and its index, for a table
// that keeps it.
func (t *table) historyDDL() []string {
	if !t.def.History {
		return nil
	}
	return []string{
		"CREATE TABLE " + quote(t.historyName()) + " (" + columnDDL(t.def.Columns) + ", " + quote(sinceColumn) + " INTEGER, " + quote(untilColumn) + " INTEGER NOT NULL)",
		"CREATE INDEX " + quote(t.historyName()+"_key") + " ON " + quote(t.historyName()) + " (" + strings.Join(quoteAll(t.def.Key), ", ") + ", " + quote(untilColumn) + ")",
	}
}

// triggerDDL makes the triggers of the table. A row whose values change
// is put into the history as it was, and its __since becomes the change
// when the statement did not make it so itself, as the import does in
// the same write; a row removed is put into the history as it was. A
// table that keeps no history still keeps __since, so that a row
// unchanged since a moment is known to be as it was then.
func (t *table) triggerDDL() []string {
	keys := map[string]bool{}
	for _, k := range t.def.Key {
		keys[strings.ToUpper(k)] = true
	}
	var differ []string
	for _, c := range t.def.Columns {
		if !keys[strings.ToUpper(c.Variable)] {
			differ = append(differ, "OLD."+quote(c.Name)+" IS NOT NEW."+quote(c.Name))
		}
	}
	cols := quoteAll(columnNames(t.def.Columns))
	old := make([]string, len(cols))
	for i, c := range cols {
		old[i] = "OLD." + c
	}
	into := "INSERT INTO " + quote(t.historyName()) + " (" + strings.Join(cols, ", ") + ", " + quote(sinceColumn) + ", " + quote(untilColumn) + ") VALUES (" +
		strings.Join(old, ", ") + ", OLD." + quote(sinceColumn) + ", " + t.tokenSQL() + ");"
	var ddl []string
	if len(differ) > 0 {
		when := "(" + strings.Join(differ, " OR ") + ")"
		if t.def.History {
			ddl = append(ddl, "CREATE TRIGGER "+quote(t.name+"__changed")+" AFTER UPDATE ON "+quote(t.name)+" WHEN "+when+" BEGIN "+into+" END")
		}
		ddl = append(ddl, "CREATE TRIGGER "+quote(t.name+"__since")+" AFTER UPDATE ON "+quote(t.name)+" WHEN "+when+" AND OLD."+quote(sinceColumn)+" IS NEW."+quote(sinceColumn)+
			" BEGIN UPDATE "+quote(t.name)+" SET "+quote(sinceColumn)+" = "+t.tokenSQL()+" WHERE rowid = NEW.rowid; END")
	}
	if t.def.History {
		ddl = append(ddl, "CREATE TRIGGER "+quote(t.name+"__removed")+" AFTER DELETE ON "+quote(t.name)+" BEGIN "+into+" END")
	}
	return ddl
}

// triggerNames are the triggers triggerDDL may make.
func (t *table) triggerNames() []string {
	return []string{t.name + "__changed", t.name + "__since", t.name + "__removed"}
}

// keepHistory brings the history of a table to what its definition says,
// when the database is opened: the column __since, the table of the
// history and the triggers. A table that has them as they should be is
// not written to. A table that says "history": false keeps the history it
// had until a version of it says so (DefineTable drops it).
func (s *Store) keepHistory(ctx context.Context, t *table) error {
	w := s.writer
	var has int
	if err := w.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info(?) WHERE name = ?", t.name, sinceColumn).Scan(&has); err != nil {
		return storageErr("read the columns of "+t.def.Name, err)
	}
	if has == 0 {
		if _, err := w.ExecContext(ctx, "ALTER TABLE "+quote(t.name)+" ADD COLUMN "+quote(sinceColumn)+" INTEGER"); err != nil {
			return storageErr("add the moment of its rows to "+t.def.Name, err)
		}
	}
	have := map[string]string{}
	rows, err := w.QueryContext(ctx, "SELECT name, sql FROM sqlite_master WHERE tbl_name IN (?, ?) AND type IN ('table', 'index', 'trigger')", t.name, t.historyName())
	if err != nil {
		return storageErr("read the history of "+t.def.Name, err)
	}
	for rows.Next() {
		var name string
		var ddl sql.NullString
		if err := rows.Scan(&name, &ddl); err != nil {
			rows.Close()
			return storageErr("read the history of "+t.def.Name, err)
		}
		have[name] = ddl.String
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the history of "+t.def.Name, err)
	}
	if _, ok := have[t.historyName()]; !ok && t.def.History {
		for _, ddl := range t.historyDDL() {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return storageErr("make the history of "+t.def.Name, err)
			}
		}
		if _, err := w.ExecContext(ctx, t.historyBegins(), s.ids.next(s.now())); err != nil {
			return storageErr("make the history of "+t.def.Name, err)
		}
	}
	want := map[string]string{}
	for _, ddl := range t.triggerDDL() {
		want[triggerName(ddl)] = ddl
	}
	for _, name := range t.triggerNames() {
		ddl, wanted := want[name]
		had, exists := have[name]
		if exists && (!wanted || had != ddl) {
			if _, err := w.ExecContext(ctx, "DROP TRIGGER "+quote(name)); err != nil {
				return storageErr("make the history of "+t.def.Name+" again", err)
			}
			exists = false
		}
		if wanted && !exists {
			if _, err := w.ExecContext(ctx, ddl); err != nil {
				return storageErr("make the history of "+t.def.Name, err)
			}
		}
	}
	return nil
}

// triggerName is the name a CREATE TRIGGER of triggerDDL gives.
func triggerName(ddl string) string {
	rest := strings.TrimPrefix(ddl, `CREATE TRIGGER "`)
	if i := strings.Index(rest, `" `); i >= 0 {
		return strings.ReplaceAll(rest[:i], `""`, `"`)
	}
	return ""
}

// tableChange writes a change of the table's rows, before the rows are
// written in the same transaction: its id becomes the token of the rows,
// which the triggers and the statements that insert read. The caller
// holds the writer.
func (s *Store) tableChange(ctx context.Context, t *table, kind string, p Principal, now time.Time) error {
	var err error
	text, kc, ac := s.ids.change(now, p.ID, p.For), placeOf(changeKinds, kind), placeOf(actorKinds, p.Kind)
	if t.noteRows != nil {
		_, err = t.noteRows.ExecContext(ctx, text, kc, ac)
	} else {
		// A table that is being defined again has its statements when
		// its transaction is over.
		_, err = s.writer.ExecContext(ctx, changeSQL(t.tableKey()), text, kc, ac)
	}
	if err != nil {
		return storageErr("note the change of "+t.def.Name, err)
	}
	return nil
}

// keyColumns are the storage columns of the table's key, in the order of
// the columns, and where each stands among them.
func (t *table) keyColumns() ([]model.Column, []int) {
	keys := map[string]bool{}
	for _, k := range t.def.Key {
		keys[strings.ToUpper(k)] = true
	}
	var cols []model.Column
	var at []int
	for i, c := range t.def.Columns {
		if keys[strings.ToUpper(c.Variable)] {
			cols, at = append(cols, c), append(at, i)
		}
	}
	return cols, at
}

// replacing makes the list of the rows a replace brings, empty, and
// answers the statement that adds one, a row of the same key replacing
// the one before as in the table; the caller closes it.
func (t *table) replacing(ctx context.Context, w *sql.Conn) (*sql.Stmt, error) {
	keys, _ := t.keyColumns()
	cols := quoteAll(columnNames(t.def.Columns))
	for _, ddl := range []string{"DROP TABLE IF EXISTS temp.fado_replace",
		"CREATE TEMP TABLE fado_replace (" + columnDDL(t.def.Columns) + ", PRIMARY KEY (" + strings.Join(quoteAll(columnNames(keys)), ", ") + ")) WITHOUT ROWID"} {
		if _, err := w.ExecContext(ctx, ddl); err != nil {
			return nil, storageErr("replace the rows of "+t.def.Name, err)
		}
	}
	st, err := w.PrepareContext(ctx, "INSERT OR REPLACE INTO temp.fado_replace ("+strings.Join(cols, ", ")+") VALUES ("+marks(len(cols))+")")
	if err != nil {
		return nil, storageErr("replace the rows of "+t.def.Name, err)
	}
	return st, nil
}

// replaced writes the rows a replace brought into the table, in the order
// of the key, as an import writes each, and removes the rows it did not
// bring, each kept in the history as it was; then the list goes.
func (t *table) replaced(ctx context.Context, w *sql.Conn) error {
	keys, _ := t.keyColumns()
	cols := strings.Join(quoteAll(columnNames(t.def.Columns)), ", ")
	same := make([]string, len(keys))
	for i, c := range keys {
		same[i] = "r." + quote(c.Name) + " = " + quote(t.name) + "." + quote(c.Name)
	}
	for _, text := range []string{
		"INSERT INTO " + quote(t.name) + " (" + cols + ", " + quote(sinceColumn) + ") SELECT " + cols + ", " + t.tokenSQL() + " FROM temp.fado_replace WHERE true" +
			" ON CONFLICT (" + strings.Join(quoteAll(t.def.Key), ", ") + ")" + t.conflictSQL(),
		"DELETE FROM " + quote(t.name) + " WHERE NOT EXISTS (SELECT 1 FROM temp.fado_replace r WHERE " + strings.Join(same, " AND ") + ")",
		"DROP TABLE temp.fado_replace",
	} {
		if _, err := w.ExecContext(ctx, text); err != nil {
			return storageErr("replace the rows of "+t.def.Name, err)
		}
	}
	return nil
}

// pastRow is a row of a table or of its history as a version of the
// table reads it to carry it over: its values in leaf order and its
// moments; until is set for a row of the history.
type pastRow struct {
	leaves       []formula.Value
	since, until sql.NullInt64
}

// carriedRow is a row a version of a table carries over, in the wire
// shape, with its moments.
type carriedRow struct {
	values       Values
	since, until sql.NullInt64
}

// rowQuerier is the writer or the pool of readers.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// hasHistory says whether the database holds the history of the table's
// rows, asked on the writer (whose lock the caller holds) or the pool.
func hasHistory(ctx context.Context, on rowQuerier, t *table) (bool, error) {
	var n int
	if err := on.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", t.historyName()).Scan(&n); err != nil {
		return false, storageErr("read the history of "+t.def.Name, err)
	}
	return n > 0, nil
}

// carriedRows reads the rows of a table in key order with their moments,
// then the rows of its history in the order they were kept.
func (s *Store) carriedRows(ctx context.Context, t *table) ([]pastRow, error) {
	cols := strings.Join(quoteAll(columnNames(t.def.Columns)), ", ")
	texts := []string{"SELECT " + cols + ", " + quote(sinceColumn) + ", NULL FROM " + quote(t.name) + " ORDER BY " + strings.Join(quoteAll(t.def.Key), ", ")}
	has, err := hasHistory(ctx, s.writer, t)
	if err != nil {
		return nil, err
	}
	if has {
		texts = append(texts, "SELECT "+cols+", "+quote(sinceColumn)+", "+quote(untilColumn)+" FROM "+quote(t.historyName())+" ORDER BY rowid")
	}
	n := len(t.def.Columns)
	raw := make([]any, n)
	dest := make([]any, n+2)
	for i := range raw {
		dest[i] = &raw[i]
	}
	var out []pastRow
	for _, text := range texts {
		rows, err := s.writer.QueryContext(ctx, text)
		if err != nil {
			return nil, storageErr("read "+t.def.Name, err)
		}
		for rows.Next() {
			var r pastRow
			dest[n], dest[n+1] = &r.since, &r.until
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, storageErr("read "+t.def.Name, err)
			}
			r.leaves = make([]formula.Value, len(t.leaves))
			rest := raw
			for i, l := range t.leaves {
				r.leaves[i], rest = decode(l, rest)
			}
			out = append(out, r)
		}
		if err := rows.Close(); err != nil {
			return nil, storageErr("read "+t.def.Name, err)
		}
	}
	return out, nil
}

// historyAs brings the history of a table whose version keeps its
// storage to what the version says: made when it starts keeping it,
// dropped when it stops, and the triggers made again for the columns the
// version has.
func (s *Store) historyAs(ctx context.Context, old, t *table) error {
	w := s.writer
	has, err := hasHistory(ctx, w, old)
	if err != nil {
		return err
	}
	var ddl []string
	switch {
	case t.def.History && !has:
		if _, err := w.ExecContext(ctx, t.historyBegins(), s.ids.next(s.now())); err != nil {
			return storageErr("make the history of "+t.def.Name, err)
		}
		ddl = t.historyDDL()
	case !t.def.History && has:
		if _, err := w.ExecContext(ctx, "DELETE FROM metadata WHERE key = ?", historyKey+t.tableKey()); err != nil {
			return storageErr("drop the history of "+t.def.Name, err)
		}
		ddl = t.dropHistoryDDL()
	}
	for _, name := range t.triggerNames() {
		ddl = append(ddl, "DROP TRIGGER IF EXISTS "+quote(name))
	}
	for _, text := range append(ddl, t.triggerDDL()...) {
		if _, err := w.ExecContext(ctx, text); err != nil {
			return storageErr("make the history of "+t.def.Name+" again", err)
		}
	}
	return nil
}

// historyBegins writes the moment the history of the table begins, its
// one value.
func (t *table) historyBegins() string {
	return `INSERT OR REPLACE INTO metadata (key, value) VALUES ('` + strings.ReplaceAll(historyKey+t.tableKey(), "'", "''") + `', ?)`
}

// rowsNow is the table as lineage reads it now: every row with the
// change that gave it its values.
func (t *table) rowsNow(string) string {
	return "(SELECT " + strings.Join(quoteAll(columnNames(t.def.Columns)), ", ") + ", " + quote(sinceColumn) + ", NULL AS " + quote(untilColumn) + " FROM " + quote(t.name) + ")"
}

// rowsAsOf is the table as it was just before the commit of the moment
// began, :lineage_change being the last change of a table before it:
// the rows given their values by then and not changed since, and the
// rows of the history that held their values then. A row older than the
// history has no __since, and was there before it.
func (t *table) rowsAsOf(string) string {
	cols := strings.Join(quoteAll(columnNames(t.def.Columns)), ", ")
	since := "(" + quote(sinceColumn) + " IS NULL OR " + quote(sinceColumn) + " <= :lineage_change)"
	return "(SELECT " + cols + ", " + quote(sinceColumn) + ", NULL AS " + quote(untilColumn) + " FROM " + quote(t.name) + " WHERE " + since +
		" UNION ALL SELECT " + cols + ", " + quote(sinceColumn) + ", " + quote(untilColumn) + " FROM " + quote(t.historyName()) + " WHERE " + since + " AND " + quote(untilColumn) + " > :lineage_change)"
}

// changeBefore is the number of the last change of a table's rows made
// before an event, 0 for none. The numbers and the ids of the changes
// rise together, so it is found by halves, a row read by its number at
// each step: some twenty for a million changes.
func (s *Store) changeBefore(ctx context.Context, event string) (int64, error) {
	var hi sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT max(seq) FROM fado_table_changes").Scan(&hi); err != nil {
		return 0, storageErr("read the changes of the tables", err)
	}
	lo, top := int64(0), hi.Int64
	for lo < top {
		mid := lo + (top-lo+1)/2
		var seq int64
		var id string
		err := s.db.QueryRowContext(ctx, "SELECT seq, event_id FROM fado_table_changes WHERE seq >= ? ORDER BY seq LIMIT 1", mid).Scan(&seq, &id)
		if err != nil && err != sql.ErrNoRows {
			return 0, storageErr("read the changes of the tables", err)
		}
		if err == nil && id < event {
			lo = seq
		} else {
			top = mid - 1
		}
	}
	return lo, nil
}

// historyAt says whether the history of the table's rows reaches back to
// the moment of an event: the table keeps it, and began to before then.
func (s *Store) historyAt(ctx context.Context, t *table, event string) (bool, error) {
	if has, err := hasHistory(ctx, s.db, t); err != nil || !has {
		return false, err
	}
	var began string
	switch err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key = ?", historyKey+t.tableKey()).Scan(&began); err {
	case nil:
		return began < event, nil
	case sql.ErrNoRows:
		return true, nil
	default:
		return false, storageErr("read the history of "+t.def.Name, err)
	}
}

// rowMoments reads the changes a part of a table names, by the numbers
// lineageParts left in them: when, who, of what kind and for whom, and
// the kind of change.
func (s *Store) rowMoments(ctx context.Context, p *LineagePart) error {
	for _, m := range []*LineageMoment{p.Since, p.Until} {
		if m == nil {
			continue
		}
		var id, kind, by, byKind, byFor sql.NullString
		err := s.db.QueryRowContext(ctx, "SELECT event_id, kind, actor_id, actor_kind, actor_for FROM fado_table_changes WHERE seq = ?", m.change).Scan(&id, &kind, &by, &byKind, &byFor)
		if err != nil && err != sql.ErrNoRows {
			return storageErr("read a change of a table", err)
		}
		*m = LineageMoment{At: idMillis(id.String) / 1000, By: by.String, ByKind: byKind.String, For: byFor.String, Event: kind.String}
	}
	return nil
}

// idMillis is the moment of an id (ids.go): its first ten characters,
// in Crockford's base32, are the milliseconds since the epoch.
func idMillis(id string) int64 {
	var ms int64
	for i := 0; i < 10 && i < len(id); i++ {
		d := strings.IndexByte(crockford, id[i])
		if d < 0 {
			return 0
		}
		ms = ms<<5 | int64(d)
	}
	return ms
}

// RowHistoryAsk asks for a page of the history of a row: Limit versions,
// DefaultDocuments when 0 and MaxDocuments at most, after Offset.
type RowHistoryAsk struct {
	Limit, Offset int
}

// RowHistory is the history of one row of a table (ADR-0099, step 3):
// the row as it is now, then as it was before each change, the newest
// first, Count in all. History says the table keeps it; a table whose
// definition says "history": false answers the row as it is now, if it
// has one.
type RowHistory struct {
	Table   string       `json:"table"`
	Key     Values       `json:"key"`
	History bool         `json:"history"`
	Count   int64        `json:"count"`
	Rows    []RowVersion `json:"rows"`
	More    bool         `json:"more"`
}

// RowVersion is a row as it was: its values, the change that gave them,
// and the one that changed or removed them, left out for the row as it
// is now. Since is left out for a row older than the history.
type RowVersion struct {
	Values Values         `json:"values"`
	Since  *LineageMoment `json:"since,omitempty"`
	Until  *LineageMoment `json:"until,omitempty"`
}

// RowHistory reads the history of the row of a table that has the key,
// for a caller its read rule lets through (ADR-0051, item 7); a content
// administrator's read past it is kept in the log of reads. A key the
// table never had answers no versions.
func (s *Store) RowHistory(ctx context.Context, name string, key Values, ask RowHistoryAsk, opts ...Option) (*RowHistory, error) {
	t, e := s.table(name)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, e
	}
	if ask.Limit == 0 {
		ask.Limit = DefaultDocuments
	}
	if ask.Limit < 0 || ask.Limit > MaxDocuments {
		return nil, &Error{Code: CodeBadValue, Path: "/limit", Message: "limit is from 1 to 1,000, got " + strconv.Itoa(ask.Limit), Hint: "ask for fewer versions, and the next ones with offset"}
	}
	if ask.Offset < 0 {
		return nil, &Error{Code: CodeBadValue, Path: "/offset", Message: "offset is how many versions are skipped, 0 or more, got " + strconv.Itoa(ask.Offset), Hint: "leave it out for the first page"}
	}
	if t.system {
		return nil, hintf(CodeNotAllowed, "", "the system table "+t.def.Name+" keeps no history of its rows: they are the engine's records", "read the records themselves; table_rows reads the table as it is")
	}
	past, e := s.tableRefusal(t, false, o, s.clock(o))
	if e != nil {
		return nil, e
	}
	// The key names every column of the table's key, and no other.
	vals := make([]formula.Value, len(t.leaves))
	for j := range vals {
		vals[j] = formula.Blank()
	}
	if err := setFields(vals, t.leafIndex, "", t.keyFields(true), key, "", s.limits); err != nil {
		err.Path = "/key/" + err.Variable
		if err.Code == CodeUnknownVariable {
			err.Message += "; a key names the columns of the table's key, and no other"
		}
		return nil, err
	}
	where := make([]string, len(t.def.Key))
	var args []any
	for i, k := range t.def.Key {
		li := t.leafIndex[strings.ToUpper(k)]
		if vals[li].IsBlank() {
			return nil, &Error{Code: CodeRequired, Path: "/key/" + k, Message: "a key names every column of the table's key", Hint: "give " + k}
		}
		where[i] = quote(t.leaves[li].column) + " = ?"
		args = append(args, encode(nil, t.leaves[li], vals[li])[0])
	}
	has, err := hasHistory(ctx, s.db, t)
	if err != nil {
		return nil, err
	}
	cols := strings.Join(quoteAll(columnNames(t.def.Columns)), ", ")
	versions := "SELECT " + cols + ", " + quote(sinceColumn) + ", NULL AS " + quote(untilColumn) + " FROM " + quote(t.name) + " WHERE " + strings.Join(where, " AND ")
	bound := append([]any(nil), args...)
	if has {
		versions += " UNION ALL SELECT " + cols + ", " + quote(sinceColumn) + ", " + quote(untilColumn) + " FROM " + quote(t.historyName()) + " WHERE " + strings.Join(where, " AND ")
		bound = append(bound, args...)
	}
	text := "SELECT *, count(*) OVER () FROM (" + versions + ") ORDER BY " + quote(untilColumn) + " IS NOT NULL, " + quote(untilColumn) + " DESC LIMIT " + strconv.Itoa(ask.Limit) + " OFFSET " + strconv.Itoa(ask.Offset)
	rows, err := s.db.QueryContext(ctx, text, bound...)
	if err != nil {
		return nil, storageErr("read the history of "+t.def.Name, err)
	}
	defer rows.Close()
	out := &RowHistory{Table: t.def.Name, Key: Values{}, History: has, Rows: []RowVersion{}}
	for _, f := range t.keyFields(true) {
		out.Key[f.Name], _ = field(key, f.Name)
	}
	n := len(t.def.Columns)
	raw := make([]any, n)
	dest := make([]any, n+3)
	for i := range raw {
		dest[i] = &raw[i]
	}
	for rows.Next() {
		var since, until sql.NullInt64
		dest[n], dest[n+1], dest[n+2] = &since, &until, &out.Count
		if err := rows.Scan(dest...); err != nil {
			return nil, storageErr("read the history of "+t.def.Name, err)
		}
		leaves := make([]formula.Value, len(t.leaves))
		rest := raw
		for i, l := range t.leaves {
			leaves[i], rest = decode(l, rest)
		}
		v := RowVersion{}
		v.Values, _ = getFields(leaves, t.leafIndex, "", t.def.Fields)
		if since.Valid {
			v.Since = &LineageMoment{change: since.Int64}
		}
		if until.Valid {
			v.Until = &LineageMoment{change: until.Int64}
		}
		if err := s.rowMoments(ctx, &LineagePart{Since: v.Since, Until: v.Until}); err != nil {
			return nil, err
		}
		out.Rows = append(out.Rows, v)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the history of "+t.def.Name, err)
	}
	if len(out.Rows) == 0 && ask.Offset > 0 {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM ("+versions+")", bound...).Scan(&out.Count); err != nil {
			return nil, storageErr("read the history of "+t.def.Name, err)
		}
	}
	out.More = int64(ask.Offset+len(out.Rows)) < out.Count
	if past {
		r := &readLog{command: "row_history", table: t.def.Name, rules: []string{"/access/read"}, rows: int64(len(out.Rows)), hasRows: true, past: true}
		if err := s.logRead(ctx, o, r); err != nil {
			return nil, err
		}
	}
	return out, nil
}
