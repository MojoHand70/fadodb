package postgres

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// envAddress names the Postgres server the tests run against.
const envAddress = "FADODB_POSTGRES"

// address answers where the Postgres of the tests is. Without one the
// test is skipped; where the variable says "must", it fails, so that
// GitHub's checks cannot pass without having run.
func address(t testing.TB) string {
	t.Helper()
	a := os.Getenv(envAddress)
	switch a {
	case "":
		t.Skip(envAddress + " names no Postgres server")
	case "must":
		t.Fatal(envAddress + "=must, and no address of a Postgres server was given")
	}
	return a
}

// schemaOf makes a schema of its own for a test, with the functions of
// the dialect in it, and answers its name; it is dropped when the test
// ends, however it ends. Another run may use the same server.
func schemaOf(t testing.TB) (addr, schema string) {
	t.Helper()
	addr = address(t)
	admin, err := Open(addr, "")
	if err != nil {
		t.Fatal(err)
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema = "fado_t_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec("CREATE SCHEMA " + quoteIdent(schema)); err != nil {
		admin.Close()
		t.Fatalf("create the schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + quoteIdent(schema) + " CASCADE"); err != nil {
			t.Errorf("drop the schema: %v", err)
		}
		admin.Close()
	})
	db, err := Open(addr, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range formula.NewPostgres().Setup() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v\n%s", err, stmt)
		}
	}
	return addr, schema
}

// openPostgres opens a database of the test's own, with the functions
// of the dialect.
func openPostgres(t testing.TB) *sql.DB {
	t.Helper()
	addr, schema := schemaOf(t)
	db, err := Open(addr, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
