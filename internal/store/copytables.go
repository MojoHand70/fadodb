package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// The tables in the copy (ADR-0094). The rows of a table live in the
// store, where a commit finds one of them by its key in a few millionths
// of a second. A question that reads the whole table, a sum over it or a
// page of it in an order no index holds, is what the analytical engine
// is for: a program that keeps the copy keeps the rows of every table in
// it too, and asks it such a question.
//
// A table has no log of its changes, as the documents have their
// events. So the store keeps, for each table, a token that every
// transaction which changes its rows writes anew, and the copy keeps the
// token it was made at: where the two differ the copy of the table is
// made again, whole. A small table is copied by whichever feed finds it
// changed, the feed of a question too. A large one is copied by the feed
// that runs by itself, and the store answers its questions meanwhile.

const (
	// rowsKey leads the line of the database's metadata that keeps the
	// token of a table's rows, under the table's name in upper case.
	rowsKey = "rows:"
	// copyMarkTable leads the mark of a table in the copy, under the
	// table's name in upper case: the version of its definition, a
	// space, and the token of its rows, copyNoRows for a table whose
	// rows no transaction of this record has changed.
	copyMarkTable = "table:"
	copyNoRows    = "-"
)

// copySmallTable and copyTableRest are variables so that a test reaches
// them with few rows. A table of at most copySmallTable rows is copied
// by whichever feed finds it changed: that is a moment. A larger one is
// copied by the feed that runs by itself, and is then left alone for
// copyTableRest times as long as its copy took, so that a large table
// which changes all the time does not keep the engine copying it.
var (
	copySmallTable int64 = 10000
	copyTableRest        = 10
)

// errTableBehind is why the store answered a question over a table that
// the copy could have: the table changed, and its copy is not made
// again yet.
var errTableBehind = errors.New("the copy of the table is not made again yet")

// copiedTable is what the last feed found of one table: whether the
// copy holds its rows as they are, and how many rows its last copy
// took.
type copiedTable struct {
	current bool
	rows    int64
}

// feedKind says who runs a feed, which decides what it does with a
// large table that changed.
type feedKind int

const (
	// feedAsked is a feed somebody asked for: it copies every table
	// that changed.
	feedAsked feedKind = iota
	// feedSelf is the feed that runs by itself: it leaves a large table
	// alone for a while after it copied it.
	feedSelf
	// feedClosing is the feed of a store that closes: it has a few
	// seconds, and begins no large table.
	feedClosing
)

// tableLayout is one table as the copy holds it: its rows under the name
// of the table of the store in lower case, since the copy tells names
// apart by their letters alone, and a table may be defined again in
// another case.
type tableLayout struct {
	name, upper string
	store, copy string
	version     int
	laid        CopyTable
	sel         string
	bools       []int
	// want is the mark the copy has when it holds the rows as they are.
	want string
}

func tableLayoutOf(t *table) *tableLayout {
	l := &tableLayout{name: t.def.Name, upper: strings.ToUpper(t.def.Name), store: t.name, copy: strings.ToLower(t.name), version: t.def.Version}
	cols := make([]CopyColumn, len(t.def.Columns))
	for i, col := range t.def.Columns {
		cols[i] = CopyColumn{Name: col.Name, Type: copyColumnType(col)}
		if cols[i].Type == CopyBool {
			l.bools = append(l.bools, i)
		}
	}
	l.laid = CopyTable{Name: l.copy, Columns: cols}
	l.sel = "SELECT " + strings.Join(quoteAll(columnNames(t.def.Columns)), ", ") + " FROM " + quote(t.name)
	return l
}

// rowsChanged writes the token of a table's rows anew, in the
// transaction that changes them (ADR-0094, item 3). A program without an
// analytical engine writes it too: the next one to open the database may
// keep a copy.
func (s *Store) rowsChanged(ctx context.Context, t *table, now time.Time) error {
	var err error
	if t.noteRows != nil {
		_, err = t.noteRows.ExecContext(ctx, s.ids.next(now))
	} else {
		// A table that is being defined again has its statements when
		// its transaction is over.
		_, err = s.writer.ExecContext(ctx, noteRowsSQL(t.def.Name), s.ids.next(now))
	}
	if err != nil {
		return storageErr("note the change of "+t.def.Name, err)
	}
	return nil
}

// noteRowsSQL is the statement that writes the token of a table's rows:
// the name is in its text, and the token its one value.
func noteRowsSQL(name string) string {
	return `INSERT INTO metadata (key, value) VALUES ('` + strings.ReplaceAll(rowsKey+strings.ToUpper(name), "'", "''") + `', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`
}

// rowTokens reads the token of every table's rows, by the table's name
// in upper case, at the moment of the database the connection reads.
func rowTokens(ctx context.Context, conn *sql.Conn) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT key, value FROM metadata WHERE key >= 'rows:' AND key < 'rows;'`)
	if err != nil {
		return nil, storageErr("read the tokens of the tables for the copy", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, storageErr("read the tokens of the tables for the copy", err)
		}
		out[strings.TrimPrefix(key, rowsKey)] = value
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the tokens of the tables for the copy", err)
	}
	return out, nil
}

// moreRowsThan reports whether a table of the store has more than n
// rows, and reads no more than n of them to say so.
func moreRowsThan(ctx context.Context, conn *sql.Conn, table string, n int64) (bool, error) {
	var one int
	switch err := conn.QueryRowContext(ctx, "SELECT 1 FROM "+quote(table)+" LIMIT 1 OFFSET ?", n).Scan(&one); err {
	case nil:
		return true, nil
	case sql.ErrNoRows:
		return false, nil
	default:
		return false, storageErr("read "+table+" for the copy", err)
	}
}

// tablesToCopy looks at every table of the database at the moment the
// connection reads (ADR-0094, items 3 and 4), and answers which of them
// this feed copies, the marks of tables the database no longer has, and
// what holds of each table when the feed is done but for those it
// copies. kick says a large table waits for the feed that runs by
// itself.
func (f *feeder) tablesToCopy(ctx context.Context, conn *sql.Conn, tabs []*tableLayout, marks map[string]string, whole bool, most int64, kind feedKind) (copyNow []*tableLayout, gone []string, state map[string]copiedTable, kick bool, err error) {
	state = make(map[string]copiedTable, len(tabs))
	marked := false
	for name := range marks {
		if strings.HasPrefix(name, copyMarkTable) {
			marked = true
			break
		}
	}
	if len(tabs) == 0 && !marked {
		return nil, nil, state, false, nil
	}
	tokens, err := rowTokens(ctx, conn)
	if err != nil {
		return nil, nil, nil, false, err
	}
	was := f.tablesNow()
	have := make(map[string]bool, len(tabs))
	for _, l := range tabs {
		key := copyMarkTable + l.upper
		have[key] = true
		token := tokens[l.upper]
		if token == "" {
			token = copyNoRows
		}
		l.want = strconv.Itoa(l.version) + " " + token
		state[l.upper] = copiedTable{rows: was[l.upper].rows}
		if !whole && marks[key] == l.want {
			state[l.upper] = copiedTable{current: true, rows: was[l.upper].rows}
			continue
		}
		large, err := moreRowsThan(ctx, conn, l.store, copySmallTable)
		if err != nil {
			return nil, nil, nil, false, err
		}
		if large {
			switch {
			case most > 0:
				// A question does not wait for it.
				kick = true
				continue
			case kind == feedClosing:
				continue
			case kind == feedSelf && time.Since(f.tableAt[l.upper]) < time.Duration(copyTableRest)*f.tableTook[l.upper]:
				continue
			}
			// Nobody waits for this feed: the store answers meanwhile.
			f.stands.Store(copyBehind)
		}
		copyNow = append(copyNow, l)
	}
	for name, value := range marks {
		if strings.HasPrefix(name, copyMarkTable) && value != "" && !have[name] {
			gone = append(gone, name)
		}
	}
	return copyNow, gone, state, kick, nil
}

// tablesNow answers what the last feed found of the tables; a map nobody
// writes to.
func (f *feeder) tablesNow() map[string]copiedTable {
	if m := f.tables.Load(); m != nil {
		return *m
	}
	return nil
}

// feedTable hands the copy every row of a table as the store holds it,
// and answers how many they were.
func feedTable(ctx context.Context, conn *sql.Conn, w CopyWriter, l *tableLayout) (int64, error) {
	rows, err := conn.QueryContext(ctx, l.sel)
	if err != nil {
		return 0, storageErr("read "+l.name+" for the copy", err)
	}
	defer rows.Close()
	row, dest := scanInto(len(l.laid.Columns))
	n := int64(0)
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return n, storageErr("read "+l.name+" for the copy", err)
		}
		bools(row, l.bools)
		if err := w.Append(l.copy, row); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, storageErr("read "+l.name+" for the copy", err)
	}
	return n, nil
}

// leads reports whether a column leads the key of the table or one of
// its indexes: a condition that names its value is then answered by the
// store in a moment, whatever the size of the table.
func (t *table) leads(column string) bool {
	if len(t.def.Key) > 0 && strings.EqualFold(t.def.Key[0], column) {
		return true
	}
	for _, idx := range t.def.Indexes {
		if len(idx) > 0 && strings.EqualFold(idx[0], column) {
			return true
		}
	}
	return false
}

// largeInCopy reports whether the copy holds the table and its last
// copy took more rows than the store keeps in memory (ADR-0036): a
// question that reads such a table whole is asked of the copy at once,
// and of the store for a moment first when the table is smaller.
func (s *Store) largeInCopy(t *table) bool {
	if s.feeder == nil || t.system {
		return false
	}
	return s.feeder.tablesNow()[strings.ToUpper(t.def.Name)].rows > int64(s.maxIndexed)
}

// copyReadyTable is copyReady for a question over a table: the copy is
// brought up to the database, and it answers only when it then holds
// the rows of the table as they are (ADR-0094, item 5).
func (s *Store) copyReadyTable(ctx context.Context, t *table) (*feeder, bool) {
	if t.system {
		return nil, false
	}
	fd, ready := s.copyReady(ctx)
	if !ready {
		return nil, false
	}
	if !fd.tablesNow()[strings.ToUpper(t.def.Name)].current {
		fd.notAnswered(errTableBehind)
		return nil, false
	}
	return fd, true
}

// tableFigureFromCopy asks the copy for what a LOOKUP of a live formula
// asks of a table: the aggregate of the slot, or the column of its first
// row, with the currency and the error code of the one row, as the
// store's statement answers them. Who may read the table was asked
// before.
func (s *Store) tableFigureFromCopy(ctx context.Context, t *table, q *formula.QuerySlot, args []formula.Value, o options, of *formula.Context) (v, c any, code sql.NullString, ok bool) {
	if s.feeder == nil || t.system {
		return nil, nil, code, false
	}
	d := formula.NewDuckDB().WithLimits(s.limits)
	params := formula.NewParams(d)
	arg := func(i int, currency bool) string {
		if currency {
			return d.Param(fmt.Sprintf("fa%dc", i))
		}
		return d.Param(fmt.Sprintf("fa%d", i))
	}
	table := func(string) string { return quote(t.name) }
	var text string
	var err error
	if q.Agg != "" {
		text, err = formula.AggregateSQL(q, d, arg, table, params)
	} else {
		var e formula.SQLExpr
		if e, err = formula.LookupSQL(q, d, arg, table, params); err == nil {
			text = lookupSelect(e)
		}
	}
	if err != nil {
		return nil, nil, code, false
	}
	evalCtx := contextOf(o.principal, s.clock(o))
	if of != nil {
		evalCtx = *of
	}
	named := d.ContextArgs(&evalCtx)
	money := make([]bool, len(args))
	for _, prog := range []*formula.Program{q.Value, q.Cond, q.Key} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if i := q.ArgIndex(sl.Name); i >= 0 && sl.Type.Kind == formula.KindMoney {
				money[i] = true
			}
		}
	}
	for i, a := range args {
		named = append(named, sql.Named(fmt.Sprintf("fa%d", i), a.DriverValue()))
		if money[i] {
			var cur any
			if a.Kind() == formula.KindMoney {
				cur = a.Currency()
			}
			named = append(named, sql.Named(fmt.Sprintf("fa%dc", i), cur))
		}
	}
	fd, ready := s.copyReadyTable(ctx, t)
	if !ready {
		return nil, nil, code, false
	}
	_, rows, err := fd.copy.Ask(ctx, text, named...)
	if err == nil && (len(rows) != 1 || len(rows[0]) != 3) {
		err = errors.New("the copy answered a lookup with a row of another shape")
	}
	if err != nil {
		fd.notAnswered(err)
		return nil, nil, code, false
	}
	if e := rows[0][2]; e != nil {
		code = sql.NullString{String: fmt.Sprint(e), Valid: true}
	}
	fd.answered()
	return copyCell(rows[0][0]), copyCell(rows[0][1]), code, true
}

// tablePageWithCopy answers the rows of a page of a list over a table
// in a program that keeps the copy (ADR-0094, item 6). A page of a large
// table in an order no index holds is found in the copy at once. Any
// other page is the store's for a moment, copyStoreFirst, and the copy's
// when the moment has passed. Whichever finds the page, its rows are
// read from the store.
func (s *Store) tablePageWithCopy(ctx context.Context, t *table, plan *query.Plan, f query.TableFilter, on *query.On, o options, text string, args []any) ([][]formula.Value, error) {
	sorts := false
	if len(f.Order) > 0 {
		sorts = !t.leads(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(f.Order[0]), "-+")))
	}
	if !(sorts && s.largeInCopy(t)) && copyStoreFirst != 0 {
		moment, done := context.WithTimeout(ctx, copyStoreFirst)
		rows, err := t.tableRows(moment, s.db, text, args)
		passed := err != nil && moment.Err() != nil && ctx.Err() == nil
		done()
		if !passed {
			return rows, err
		}
	}
	if rows, found := s.tablePageFromCopy(ctx, t, plan, f, on, o); found {
		return rows, nil
	}
	// The copy did not answer: the store does, for as long as it takes.
	return t.tableRows(ctx, s.db, text, args)
}

// tablePageFromCopy finds a page of a list over a table in the copy:
// which rows the question takes, in its order. That is the part that
// reads the table, and the copy is asked for the keys of the rows
// alone. The rows themselves are then read from the store by their
// keys, still under the question's own condition, so that a row changed
// since the copy looked is never shown where it no longer belongs. It
// answers false when the store has to answer the whole question.
func (s *Store) tablePageFromCopy(ctx context.Context, t *table, plan *query.Plan, f query.TableFilter, on *query.On, o options) ([][]formula.Value, bool) {
	d := formula.NewDuckDB().WithLimits(s.limits)
	asked, err := query.CompileTableFor(d, f, on, t.src, s.sources())
	if err != nil {
		// The store says what is wrong with the question.
		return nil, false
	}
	keys := quoteAll(t.def.Key)
	text := "SELECT " + strings.Join(keys, ", ") + " FROM " + quote(t.name)
	if asked.Where != "" {
		text += " WHERE " + asked.Where
	}
	text += " ORDER BY " + asked.Order + " LIMIT " + fmt.Sprint(plan.Limit+1) + " OFFSET " + fmt.Sprint(plan.Offset)
	evalCtx := contextOf(o.principal, s.clock(o))
	fd, ready := s.copyReadyTable(ctx, t)
	if !ready {
		return nil, false
	}
	_, found, err := fd.copy.Ask(ctx, text, append(asked.Args[:len(asked.Args):len(asked.Args)], d.ContextArgs(&evalCtx)...)...)
	if err != nil {
		fd.notAnswered(err)
		return nil, false
	}
	if len(found) == 0 {
		fd.answered()
		return nil, true
	}
	// The keys, as the store compares them: a true or a false is its 1
	// or 0 there.
	page := make([][]any, len(found))
	for i, row := range found {
		if len(row) != len(keys) {
			fd.notAnswered(errors.New("the copy answered the keys of a page with a row of another shape"))
			return nil, false
		}
		page[i] = make([]any, len(row))
		for j, v := range row {
			switch x := copyCell(v).(type) {
			case bool:
				page[i][j] = int64(0)
				if x {
					page[i][j] = int64(1)
				}
			case []byte:
				page[i][j] = string(x)
			default:
				page[i][j] = x
			}
		}
	}
	listed, err := json.Marshal(page)
	if err != nil {
		fd.notAnswered(err)
		return nil, false
	}
	// The rows, from the store: the keys lead, each row is found by the
	// key of the table, and the question's condition is asked of it.
	read := `SELECT ` + strings.Join(quoteAll(columnNames(t.def.Columns)), ", ") + ` FROM (SELECT "key" AS "fado_page_at"`
	joined := make([]string, len(keys))
	for j, k := range keys {
		col := quote("fado_page_k" + strconv.Itoa(j))
		read += `, json_extract("value", '$[` + strconv.Itoa(j) + `]') AS ` + col
		joined[j] = quote(t.name) + "." + k + " = p." + col
	}
	read += ` FROM json_each(:page_keys)) AS p CROSS JOIN ` + quote(t.name) + ` ON ` + strings.Join(joined, " AND ")
	if plan.Where != "" {
		read += " WHERE " + plan.Where
	}
	read += ` ORDER BY p."fado_page_at"`
	args := append(plan.Args[:len(plan.Args):len(plan.Args)], s.contextArgs(o)...)
	args = append(args, sql.Named("page_keys", string(listed)))
	rows, err := t.tableRows(ctx, s.db, read, args)
	if err != nil {
		fd.notAnswered(err)
		return nil, false
	}
	fd.answered()
	return rows, true
}
