package analytics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/MojoHand70/fadodb/fado"
)

// Engine is FadoDB's analytical engine, made with DuckDB (ADR-0017,
// ADR-0089). A program gives it to the database it opens, in
// fado.Options.Analytics: the database then keeps a copy of its
// documents in a DuckDB file beside its own, and feeds that copy
// itself. DuckDB never opens the file of the database.
type Engine struct {
	// Memory is the most memory DuckDB may take, such as "4GB"; "" is
	// DuckDB's own limit, four fifths of the machine's.
	Memory string
	// Threads is how many threads DuckDB uses; 0 is one for each core.
	Threads int
}

var _ fado.Analytics = Engine{}

// CopyPath is the file of the copy that stands beside a database.
func CopyPath(database string) string { return database + ".duckdb" }

// The table of the copy that keeps how far it was fed, and the table of
// a feed's own, which lives no longer than the connection, for the ids
// of many rows that are taken out at once.
const (
	marksTable = "fado_copy_marks"
	goneTable  = "fado_copy_gone"
)

// OpenCopy opens the copy beside the database, and makes an empty one
// when none is there or the one there does not open: a file DuckDB
// cannot read, or one a later DuckDB wrote. Nothing is lost with it:
// the database feeds an empty copy from the beginning.
func (e Engine) OpenCopy(ctx context.Context, database string) (fado.Copy, error) {
	path := CopyPath(database)
	// One opening feeds a copy. DuckDB keeps a second program off the
	// file; inside one program it would hand the same copy to a second
	// opening, and two feeds would write the same rows twice.
	key := path
	if abs, err := filepath.Abs(path); err == nil {
		key = abs
	}
	if _, taken := openCopies.LoadOrStore(key, true); taken {
		return nil, fmt.Errorf("the copy at %s is open in this program already: one opening of a database feeds its copy", path)
	}
	c, err := e.openOrMake(ctx, path)
	if err != nil {
		openCopies.Delete(key)
		return nil, err
	}
	c.key = key
	return c, nil
}

// openCopies are the copies this program has open, by their paths.
var openCopies sync.Map

func (e Engine) openOrMake(ctx context.Context, path string) (*duckCopy, error) {
	c, err := e.open(ctx, path)
	if err != nil {
		if _, gone := os.Stat(path); gone != nil {
			return nil, err
		}
		// The file is there and does not open: it is made again.
		for _, name := range []string{path, path + ".wal"} {
			if rerr := os.Remove(name); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				return nil, fmt.Errorf("the copy at %s does not open (%v) and cannot be taken away: %w", path, err, rerr)
			}
		}
		if c, err = e.open(ctx, path); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (e Engine) open(ctx context.Context, path string) (*duckCopy, error) {
	connector, err := duckdb.NewConnector(path, func(execer driver.ExecerContext) error {
		var settings []string
		if e.Memory != "" {
			settings = append(settings, "SET memory_limit = '"+strings.ReplaceAll(e.Memory, "'", "")+"'")
		}
		if e.Threads > 0 {
			settings = append(settings, fmt.Sprintf("SET threads = %d", e.Threads))
		}
		for _, stmt := range settings {
			if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("open the copy at %s: %w", path, err)
	}
	db := sql.OpenDB(connector)
	c := &duckCopy{path: path, db: db}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+quote(marksTable)+` ("name" VARCHAR PRIMARY KEY, "value" VARCHAR NOT NULL)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("open the copy at %s: %w", path, err)
	}
	// One connection is the feed's for as long as the copy is open: its
	// transaction holds the rows and the marks of a feed together.
	if c.feeder, err = db.Conn(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open the copy at %s: %w", path, err)
	}
	return c, nil
}

// duckCopy is the copy of one database in a DuckDB file.
type duckCopy struct {
	path, key string
	db        *sql.DB
	// feeder is the connection of the feeds, and mu lets one feed or
	// one laying of tables run at a time.
	feeder *sql.Conn
	mu     sync.Mutex
	// laid are the columns of each table as the database last laid
	// them, in the order a row of a feed gives its values, and days the
	// places among them of the columns that hold a day.
	laid map[string][]string
	days map[string][]int
}

func quote(ident string) string { return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"` }

// duckType is the DuckDB type of what a column of the copy holds.
func duckType(kind string) (string, error) {
	switch kind {
	case fado.CopyText:
		return "VARCHAR", nil
	case fado.CopyWhole:
		return "BIGINT", nil
	case fado.CopyNumber:
		return "DOUBLE", nil
	case fado.CopyBool:
		return "BOOLEAN", nil
	case fado.CopyDay:
		return "INTEGER", nil
	}
	return "", fmt.Errorf("the copy knows no column of the kind %q", kind)
}

func (c *duckCopy) Marks(ctx context.Context) (map[string]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT "name", "value" FROM `+quote(marksTable))
	if err != nil {
		return nil, fmt.Errorf("read the marks of the copy: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("read the marks of the copy: %w", err)
		}
		out[name] = value
	}
	return out, rows.Err()
}

// Lay makes each table the copy lacks, adds each column a table lacks,
// and makes again a column whose type changed. A column the database no
// longer names stays, with what it holds.
func (c *duckCopy) Lay(ctx context.Context, tables []fado.CopyTable) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.laid == nil {
		c.laid, c.days = map[string][]string{}, map[string][]int{}
	}
	for _, t := range tables {
		have := map[string]string{}
		rows, err := c.db.QueryContext(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = 'main' AND table_name = ?`, t.Name)
		if err != nil {
			return fmt.Errorf("read the columns of %s in the copy: %w", t.Name, err)
		}
		for rows.Next() {
			var name, kind string
			if err := rows.Scan(&name, &kind); err != nil {
				rows.Close()
				return fmt.Errorf("read the columns of %s in the copy: %w", t.Name, err)
			}
			have[name] = kind
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read the columns of %s in the copy: %w", t.Name, err)
		}
		rows.Close()
		var stmts, names []string
		var defs []string
		var days []int
		for i, col := range t.Columns {
			kind, err := duckType(col.Type)
			if err != nil {
				return err
			}
			if col.Type == fado.CopyDay {
				days = append(days, i)
			}
			names = append(names, col.Name)
			defs = append(defs, quote(col.Name)+" "+kind)
			switch was, there := have[col.Name]; {
			case len(have) == 0:
			case !there:
				stmts = append(stmts, `ALTER TABLE `+quote(t.Name)+` ADD COLUMN `+quote(col.Name)+` `+kind)
			case was != kind:
				stmts = append(stmts, `ALTER TABLE `+quote(t.Name)+` DROP COLUMN `+quote(col.Name), `ALTER TABLE `+quote(t.Name)+` ADD COLUMN `+quote(col.Name)+` `+kind)
			}
		}
		if len(have) == 0 {
			stmts = []string{`CREATE TABLE ` + quote(t.Name) + ` (` + strings.Join(defs, ", ") + `)`}
		}
		for _, stmt := range stmts {
			if _, err := c.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("lay %s in the copy: %w", t.Name, err)
			}
		}
		c.laid[t.Name], c.days[t.Name] = names, days
	}
	return nil
}

// duckWriter hands the rows of one feed to DuckDB, on the feed's
// connection and inside its transaction: an appender for each table,
// made when the table gets its first row.
type duckWriter struct {
	ctx       context.Context
	c         *duckCopy
	conn      driver.Conn
	appenders map[string]*duckdb.Appender
	row       []driver.Value
}

func (w *duckWriter) exec(stmt string, args ...any) error {
	named := make([]driver.NamedValue, len(args))
	for i, a := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: a}
	}
	_, err := w.conn.(driver.ExecerContext).ExecContext(w.ctx, stmt, named)
	return err
}

func (w *duckWriter) Append(table string, row []any) error {
	app, ok := w.appenders[table]
	if !ok {
		columns, laid := w.c.laid[table]
		if !laid {
			return fmt.Errorf("the copy was given a row of %s before the table was laid", table)
		}
		var err error
		if app, err = duckdb.NewAppenderWithColumns(w.conn, "", "", table, columns); err != nil {
			return fmt.Errorf("append to %s in the copy: %w", table, err)
		}
		w.appenders[table] = app
	}
	w.row = w.row[:0]
	for _, v := range row {
		w.row = append(w.row, v)
	}
	// A day is DuckDB's INTEGER, which takes an int32.
	for _, i := range w.c.days[table] {
		if day, ok := w.row[i].(int64); ok {
			w.row[i] = int32(day)
		}
	}
	if err := app.AppendRow(w.row...); err != nil {
		return fmt.Errorf("append to %s in the copy: %w", table, err)
	}
	return nil
}

// flush hands DuckDB the rows an appender still holds, so that a
// statement on the same table sees them.
func (w *duckWriter) flush(table string) error {
	if app, ok := w.appenders[table]; ok {
		if err := app.Flush(); err != nil {
			return fmt.Errorf("append to %s in the copy: %w", table, err)
		}
	}
	return nil
}

// Remove takes the rows of the ids out of a table. A few are named in
// the statement. Many are put into a table of their own first and taken
// out in one statement, which reads the table once: a statement for
// every few hundred would read it once each.
func (w *duckWriter) Remove(table string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := w.flush(table); err != nil {
		return err
	}
	fail := func(err error) error { return fmt.Errorf("take rows out of %s in the copy: %w", table, err) }
	const few = 500
	if len(ids) <= few {
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
		if err := w.exec(`DELETE FROM `+quote(table)+` WHERE "id" IN (`+marks+`)`, args...); err != nil {
			return fail(err)
		}
		return nil
	}
	if err := w.exec(`CREATE TEMP TABLE IF NOT EXISTS ` + quote(goneTable) + ` ("id" VARCHAR)`); err != nil {
		return fail(err)
	}
	if err := w.exec(`DELETE FROM ` + quote(goneTable)); err != nil {
		return fail(err)
	}
	app, err := duckdb.NewAppender(w.conn, "temp", "", goneTable)
	if err != nil {
		return fail(err)
	}
	for _, id := range ids {
		if err := app.AppendRow(id); err != nil {
			app.Close()
			return fail(err)
		}
	}
	if err := app.Close(); err != nil {
		return fail(err)
	}
	if err := w.exec(`DELETE FROM ` + quote(table) + ` USING ` + quote(goneTable) + ` g WHERE ` + quote(table) + `."id" = g."id"`); err != nil {
		return fail(err)
	}
	return nil
}

func (w *duckWriter) Empty(table string) error {
	if err := w.flush(table); err != nil {
		return err
	}
	if err := w.exec(`DELETE FROM ` + quote(table)); err != nil {
		return fmt.Errorf("empty %s in the copy: %w", table, err)
	}
	return nil
}

// Feed runs give and keeps what it handed over with the marks, in one
// transaction of DuckDB: all of it or nothing.
func (c *duckCopy) Feed(ctx context.Context, give func(fado.CopyWriter) error, marks map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.feeder.Raw(func(dc any) error {
		w := &duckWriter{ctx: ctx, c: c, conn: dc.(driver.Conn), appenders: map[string]*duckdb.Appender{}}
		if err := w.exec("BEGIN TRANSACTION"); err != nil {
			return fmt.Errorf("begin a feed of the copy: %w", err)
		}
		err := give(w)
		for table, app := range w.appenders {
			// An appender is closed whether the feed is kept or not; its
			// rows are the transaction's.
			if cerr := app.Close(); cerr != nil && err == nil {
				err = fmt.Errorf("append to %s in the copy: %w", table, cerr)
			}
		}
		for name, value := range marks {
			if err != nil {
				break
			}
			if value == "" {
				err = w.exec(`DELETE FROM `+quote(marksTable)+` WHERE "name" = ?`, name)
			} else {
				err = w.exec(`INSERT INTO `+quote(marksTable)+` ("name", "value") VALUES (?, ?) ON CONFLICT ("name") DO UPDATE SET "value" = excluded."value"`, name, value)
			}
		}
		if err == nil {
			if err = w.exec("COMMIT"); err == nil {
				return nil
			}
		}
		// The context of the feed may be over: the undoing is not its.
		w.ctx = context.Background()
		_ = w.exec("ROLLBACK")
		return err
	})
}

// Ask runs one statement of DuckDB and answers its columns and rows.
func (c *duckCopy) Ask(ctx context.Context, statement string, args ...any) ([]string, [][]any, error) {
	rows, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var out [][]any
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		out = append(out, row)
	}
	return columns, out, rows.Err()
}

func (c *duckCopy) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.feeder.Close()
	if cerr := c.db.Close(); err == nil {
		err = cerr
	}
	openCopies.Delete(c.key)
	return err
}
