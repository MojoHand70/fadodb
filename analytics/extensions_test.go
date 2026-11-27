package analytics

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The engine takes no extension from outside the program (ADR-0089,
// "Settled while building the program with the engine"): DuckDB is told
// neither to fetch one nor to load one it finds on the machine, every
// extension it has loaded is one the library carries, and a statement
// that needs another is refused at once instead of sending DuckDB to the
// network. It runs on GitHub with the module's other tests, where the
// network is there to be used, so a fetch would show as a statement
// that works.
func TestCopy_TakesNoExtensionFromOutside(t *testing.T) {
	ctx := context.Background()
	c, err := Engine{Threads: 2}.openOrMake(ctx, filepath.Join(t.TempDir(), "books.fado.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Each connection of the pool opens with the settings: the feed's
	// own, held for as long as the copy is open, and another.
	other, err := c.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, setting := range []string{"autoinstall_known_extensions", "autoload_known_extensions"} {
		var feed, plain bool
		if err := c.feeder.QueryRowContext(ctx, "SELECT current_setting('"+setting+"')").Scan(&feed); err != nil {
			t.Fatal(err)
		}
		if err := other.QueryRowContext(ctx, "SELECT current_setting('"+setting+"')").Scan(&plain); err != nil {
			t.Fatal(err)
		}
		if feed || plain {
			t.Errorf("%s is %v on the feed's connection and %v on another", setting, feed, plain)
		}
	}

	// What is loaded is what the library carries.
	rows, err := other.QueryContext(ctx, `SELECT extension_name, coalesce(install_mode, '') FROM duckdb_extensions() WHERE loaded`)
	if err != nil {
		t.Fatal(err)
	}
	loaded := 0
	for rows.Next() {
		var name, mode string
		if err := rows.Scan(&name, &mode); err != nil {
			t.Fatal(err)
		}
		loaded++
		if mode != "STATICALLY_LINKED" {
			t.Errorf("the extension %s is loaded, installed as %q", name, mode)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if loaded == 0 {
		t.Error("no extension is loaded: the library carries core_functions, icu, json and parquet")
	}

	// A function of an extension the library does not carry is refused,
	// and DuckDB says which extension it would have needed. With the
	// settings DuckDB starts with, it would fetch the extension and
	// answer.
	var point string
	err = other.QueryRowContext(ctx, `SELECT ST_AsText(ST_Point(1, 2))`).Scan(&point)
	if err == nil || !strings.Contains(err.Error(), "spatial") {
		t.Errorf("a function of the extension spatial: %q, %v", point, err)
	}
}
