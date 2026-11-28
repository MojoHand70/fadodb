package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter "Reading a database with DuckDB" is for an analyst who
// opens the file of a database with DuckDB's own extension for SQLite,
// from a program of their own (ADR-0089, item 10). The test takes the
// class out of the chapter, plays what the chapter says happened,
// closes the database, attaches the file with DuckDB as the chapter
// says, runs every statement of the chapter and compares the answers.
//
// The extension is DuckDB's, fetched from DuckDB's site. The test loads
// it when the machine has it. Where it has not, FADODB_DUCKDB_SQLITE
// says what to do: "must", as the checks on GitHub set it, fetches the
// extension and fails when it cannot be had, so that the chapter is
// played there or the checks say why not; "install" fetches it and
// skips the test when it cannot; and with nothing set the test is
// skipped, and says that the chapter was not played.
//
// DuckDB brings its own SQLite. Two SQLites in one program that hold
// one file can lose each other's locks, so the engine has closed the
// database before DuckDB opens it here; the chapter says the same to
// its reader.
func TestReadingWithDuckDB(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "docs", "manual", "reading-with-duckdb.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := strings.ReplaceAll(string(text), "\r\n", "\n")
	ctx := context.Background()

	duck, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer duck.Close()
	// One connection: an extension and an attached file are its own.
	duck.SetMaxOpenConns(1)
	// DuckDB fetches an extension it knows by itself when it is loaded.
	// The test fetches nothing it was not told to.
	if _, err := duck.ExecContext(ctx, "SET autoinstall_known_extensions = false"); err != nil {
		t.Fatal(err)
	}
	if _, err := duck.ExecContext(ctx, "LOAD sqlite"); err != nil {
		mode := os.Getenv("FADODB_DUCKDB_SQLITE")
		if mode != "install" && mode != "must" {
			t.Skipf("the chapter is not played: DuckDB's extension for SQLite is not on this machine (%v); set FADODB_DUCKDB_SQLITE=install to fetch it", firstLine(err))
		}
		if _, err := duck.ExecContext(ctx, "INSTALL sqlite"); err != nil {
			if mode == "must" {
				t.Fatalf("the chapter is not played: DuckDB's extension for SQLite could not be fetched: %v", err)
			}
			t.Skipf("the chapter is not played: DuckDB's extension for SQLite could not be fetched: %v", firstLine(err))
		}
		if _, err := duck.ExecContext(ctx, "LOAD sqlite"); err != nil {
			t.Fatalf("the extension was fetched and does not load: %v", err)
		}
	}

	// The class of the chapter and what happened.
	class := regexp.MustCompile("(?s)```json\n(.*?)\n```").FindStringSubmatch(page)
	if class == nil {
		t.Fatal("the chapter shows no class")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "books.fado")
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, []byte(class[1]), olga); err != nil {
		db.Close()
		t.Fatalf("the class of the chapter: %v", err)
	}
	krzysztof, anna := person("krzysztof"), person("anna", "ACCOUNTING")
	must := func(_ *fado.Document, err error) {
		t.Helper()
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	must(db.Create(ctx, "Invoice", fado.Values{"number": "FV/1", "supplier": "ACME", "net": "1000.00 PLN", "issued": "2026-11-13",
		"lines": []any{map[string]any{"what": "taxi", "cost": "40.00 PLN"}, map[string]any{"what": "hotel", "cost": "300.00 PLN"}}}, krzysztof, fado.WithID("I-1")))
	must(db.Create(ctx, "Invoice", fado.Values{"number": "FV/2", "supplier": "Globex", "net": "500.00 EUR", "issued": "2026-11-13"}, krzysztof, fado.WithID("I-2")))
	must(db.Create(ctx, "Invoice", fado.Values{"number": "FV/3", "supplier": "Initech", "net": "70.00 PLN", "issued": "2026-11-13"}, krzysztof, fado.WithID("I-3")))
	clock = clock.Add(2 * time.Hour)
	must(db.Transition(ctx, "Invoice", "I-1", "approve", nil, krzysztof))
	must(db.Set(ctx, "Invoice", "I-2", fado.Values{"net": "600.00 EUR"}, krzysztof))
	clock = clock.Add(time.Hour)
	must(db.Transition(ctx, "Invoice", "I-1", "book", nil, anna))
	if err := db.Delete(ctx, "Invoice", "I-3", krzysztof); err != nil {
		db.Close()
		t.Fatal(err)
	}
	// The story of the chapter has as many steps as were played.
	if steps := len(tableAfter(t, page, "what happened")); steps != 7 {
		t.Errorf("the chapter tells %d steps, and the test plays 7", steps)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Opening it, as the chapter says, with the file where it is.
	if !strings.Contains(page, "INSTALL sqlite;\nLOAD sqlite;\nATTACH 'books.fado' AS books (TYPE sqlite, READ_ONLY);") {
		t.Fatal("the chapter no longer opens the database with the three statements the test runs")
	}
	here := func(statement string) string {
		statement = strings.ReplaceAll(statement, "'books.fado'", "'"+filepath.ToSlash(path)+"'")
		return strings.ReplaceAll(statement, "'invoices.parquet'", "'"+filepath.ToSlash(filepath.Join(dir, "invoices.parquet"))+"'")
	}
	if _, err := duck.ExecContext(ctx, here("ATTACH 'books.fado' AS books (TYPE sqlite, READ_ONLY)")); err != nil {
		t.Fatalf("DuckDB does not attach the database: %v", err)
	}
	// ask runs a statement and answers its rows as the chapter writes
	// them: an exact number with its places, a date as a day, a
	// timestamp as a day and a time.
	ask := func(statement string) ([]string, [][]string) {
		t.Helper()
		rows, err := duck.QueryContext(ctx, here(statement))
		if err != nil {
			t.Fatalf("%s\n%v", statement, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		types, _ := rows.ColumnTypes()
		var out [][]string
		for rows.Next() {
			vals, dest := make([]any, len(cols)), make([]any, len(cols))
			for i := range vals {
				dest[i] = &vals[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatalf("%s\n%v", statement, err)
			}
			line := make([]string, len(cols))
			for i, v := range vals {
				line[i] = duckShown(v, types[i].DatabaseTypeName())
			}
			out = append(out, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s\n%v", statement, err)
		}
		return cols, out
	}

	// The views DuckDB sees of the class are the ones the chapter lists.
	_, views := ask(`SELECT table_name FROM information_schema.tables WHERE table_catalog = 'books' AND table_type = 'VIEW' AND table_name LIKE 'Invoice%' ORDER BY table_name`)
	listed := tableAfter(t, page, "the views DuckDB sees")
	if len(views) != len(listed) {
		t.Errorf("DuckDB sees %d views of the class, and the chapter lists %d", len(views), len(listed))
	}
	for i := 0; i < len(views) && i < len(listed); i++ {
		if name := strings.ReplaceAll(listed[i][0], "`", ""); views[i][0] != name {
			t.Errorf("DuckDB sees the view %s where the chapter lists %s", views[i][0], name)
		}
	}
	// And the tables behind them, and the engine's own.
	for _, table := range []string{"doc_Invoice", "events", "outbox", "fado_users"} {
		if _, n := ask(`SELECT count(*) FROM information_schema.tables WHERE table_catalog = 'books' AND table_type = 'BASE TABLE' AND table_name = '` + table + `'`); n[0][0] != "1" {
			t.Errorf("DuckDB does not show the table %s, which the chapter says it does", table)
		}
	}

	// How DuckDB reads a value: what the file holds, and what the
	// chapter's expression makes of it, for the first invoice.
	column := regexp.MustCompile(`\b(vat_rate|net|issued|created_at)\b`)
	first := func(cell string) string {
		// What a cell writes in its first pair of backticks.
		_, rest, _ := strings.Cut(cell, "`")
		span, _, _ := strings.Cut(rest, "`")
		return span
	}
	reads := tableAfter(t, page, "how DuckDB reads a value")
	if len(reads) != 4 {
		t.Errorf("the chapter reads %d kinds of value, and the test expects 4", len(reads))
	}
	for _, row := range reads {
		kind, stored, read, become := strings.ReplaceAll(row[0], "`", ""), first(row[1]), first(row[2]), first(row[3])
		name := column.FindString(read)
		if name == "" {
			t.Errorf("the chapter reads %s with %q, which names no column the test knows", kind, read)
			continue
		}
		_, got := ask(`SELECT ` + name + `, ` + read + ` FROM books.Invoice WHERE id = 'I-1'`)
		if got[0][0] != stored || got[0][1] != become {
			t.Errorf("%s is %s in the file and %s read with %s; the chapter says %s and %s", kind, got[0][0], got[0][1], read, stored, become)
		}
	}

	// The questions, each with the answer the chapter shows; one that
	// writes a file has none.
	parts := regexp.MustCompile("(?s)<!-- question -->\n```sql\n(.*?)\n```\n\n(<!-- answer -->\n(.*?)\n\n)?").FindAllStringSubmatch(page, -1)
	if len(parts) != 6 {
		t.Fatalf("the chapter has %d statements, and the test expects 6", len(parts))
	}
	for _, part := range parts {
		cols, got := ask(part[1])
		if part[2] == "" {
			continue
		}
		lines := strings.Split(strings.TrimSpace(part[3]), "\n")
		heads := cells(strings.ReplaceAll(lines[0], "`", ""))
		if strings.Join(heads, ",") != strings.Join(cols, ",") {
			t.Errorf("%s\nanswers the columns %v, and the chapter shows %v", part[1], cols, heads)
		}
		var want [][]string
		for _, line := range lines[2:] {
			want = append(want, cells(line))
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s\nanswers %v, and the chapter shows %v", part[1], got, want)
		}
	}

	// A statement that would write is refused by DuckDB itself.
	if _, err := duck.ExecContext(ctx, `DELETE FROM books.doc_Invoice`); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("a statement that writes: %v", err)
	}
	// DuckDB has read a database nobody has open, and left SQLite's two
	// small files beside it, the first of them empty.
	wal, err := os.Stat(path + "-wal")
	if err != nil || wal.Size() != 0 {
		t.Errorf("beside the database: %v, %v", wal, err)
	}
	if _, err := os.Stat(path + "-shm"); err != nil {
		t.Errorf("beside the database: %v", err)
	}
	if _, err := duck.ExecContext(ctx, `DETACH books`); err != nil {
		t.Fatal(err)
	}
	// A program that opens the database after finds every commit, and
	// clears the two files when it closes.
	again, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := again.Count(ctx, fado.Filter{Class: "Invoice"}, anna); err != nil || n != 2 {
		t.Errorf("the database after DuckDB read it has %d invoices, %v", n, err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Errorf("after the engine closed the database, %s is still there: %v", suffix, err)
		}
	}
}

// tableAfter answers the rows of the table that follows a marker of the
// chapter.
func tableAfter(t *testing.T, page, mark string) [][]string {
	t.Helper()
	_, after, found := strings.Cut(page, "<!-- "+mark+" -->\n")
	if !found {
		t.Fatalf("the chapter has nothing marked %q", mark)
	}
	table, _, _ := strings.Cut(after, "\n\n")
	var out [][]string
	for i, line := range strings.Split(table, "\n") {
		if i >= 2 {
			out = append(out, cells(line))
		}
	}
	return out
}

// cells answers the cells of a line of a table, as they are written.
func cells(line string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

// duckShown writes a value DuckDB answered as the chapter writes it.
func duckShown(v any, kind string) string {
	switch x := v.(type) {
	case nil:
		return ""
	case duckdb.Decimal:
		text := new(big.Int).Abs(x.Value).String()
		if scale := int(x.Scale); scale > 0 {
			for len(text) <= scale {
				text = "0" + text
			}
			text = text[:len(text)-scale] + "." + text[len(text)-scale:]
		}
		if x.Value.Sign() < 0 {
			text = "-" + text
		}
		return text
	case time.Time:
		if kind == "DATE" {
			return x.UTC().Format("2006-01-02")
		}
		return x.UTC().Format("2006-01-02 15:04:05")
	case []byte:
		return string(x)
	}
	return fmt.Sprint(v)
}
