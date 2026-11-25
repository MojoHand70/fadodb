package fado_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on opening a database is for the developer whose program
// holds the engine. Its program is examples/open, which has a test of
// its own. TestOpeningADatabase holds the tables of the chapter: what
// is on disk, the options, what is refused, and what the chapter says
// in passing.

func TestOpeningADatabase(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "opening-a-database.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	ola := person("ola")
	noteClass := scratch("Note", `"text":{"type":"text"}`, oneState)
	// open opens a database of the test, and closes it when the test
	// ends unless the test closed it before.
	open := func(name string, o fado.Options) (*fado.DB, error) {
		db, err := fado.Open(filepath.Join(dir, name), o)
		if err == nil {
			t.Cleanup(func() { db.Close() })
		}
		return db, err
	}
	must := func(db *fado.DB, err error) *fado.DB {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}

	// What opening does, and what is on disk.
	db := must(open("notes.fado", fado.Options{}))
	if got := db.Classes(); !reflect.DeepEqual(got, []string{"Substitution"}) || len(db.Schemas()) != 9 || len(db.Tables()) != 0 {
		t.Errorf("a new database has the classes %v, %d schemas and the tables %v", got, len(db.Schemas()), db.Tables())
	}
	if info, err := os.Stat(filepath.Join(dir, "notes.fado")); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("a new database has the permissions %o; the chapter says it belongs to the user that made it", info.Mode().Perm())
	}
	if _, err := db.DefineClass(ctx, []byte(noteClass), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, "Note", fado.Values{"text": "buy milk"}, ola); err != nil {
		t.Fatal(err)
	}
	withBlobs := must(open("files.fado", fado.Options{BlobStore: "dir"}))
	if _, err := withBlobs.PutBlob(ctx, "a.txt", "", strings.NewReader("bytes"), ola); err != nil {
		t.Fatal(err)
	}
	onDisk := rows(marked(t, page, "on disk"))
	there := map[string]string{}
	for _, row := range onDisk {
		there[row[0]] = row[2]
	}
	for name, when := range there {
		switch {
		case when == "always":
			if !exists(name) {
				t.Errorf("%s is not there while the database is open", name)
			}
		case when == "while a program has the database open":
			if !exists(name) {
				t.Errorf("%s is not there while the database is open", name)
			}
		case strings.HasPrefix(when, "when the database was created with the store dir"):
			if exists(name) || !exists("files.fado.blobs") {
				t.Errorf("the directory of bytes: %s is there for a database of the store sqlite, or missing for one of the store dir", name)
			}
		case when == "once a program has held the database alone":
			if exists(name) {
				t.Errorf("%s is there for a database that no program held alone", name)
			}
			must(open("held.fado", fado.Options{Alone: "the test"})).Close()
			if !exists("held.fado.lock") {
				t.Error("a database that a program held alone has no lock file")
			}
		default:
			t.Errorf("the test cannot read when %s is there: %q", name, when)
		}
	}
	if len(there) != 5 {
		t.Errorf("the chapter lists %d files, and the test knows five", len(there))
	}

	// One program holds a database: a second open is not refused unless
	// the first holds the database alone, and the first does not learn
	// what the second defines. When the engine keeps every second
	// program off the file, this fails and the box in the chapter goes.
	second, err := fado.Open(filepath.Join(dir, "notes.fado"), fado.Options{})
	if err != nil {
		t.Errorf("a second open of an open file is refused: %v", err)
	} else {
		if _, err := second.DefineClass(ctx, []byte(scratch("Other", `"text":{"type":"text"}`, oneState)), setup); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Create(ctx, "Other", fado.Values{"text": "x"}, ola); fado.ErrorCode(err) != "unknown_class" {
			t.Errorf("the first program creates a document of a class the second defined: %v", err)
		}
		second.Close()
	}

	// Closing.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
	if _, err := db.Create(ctx, "Note", fado.Values{"text": "late"}, ola); fado.ErrorCode(err) != "closed" {
		t.Errorf("a change after the close: %v", err)
	}
	if _, err := db.Query(ctx, fado.Filter{Class: "Note"}, ola); err == nil {
		t.Error("a read after the close is answered")
	}
	for name, when := range there {
		if when == "while a program has the database open" && exists(name) {
			t.Errorf("%s is still there after the close", name)
		}
		if when == "always" && !exists(name) {
			t.Errorf("%s is not there after the close", name)
		}
	}
	again := must(open("notes.fado", fado.Options{}))
	if got := again.Classes(); !reflect.DeepEqual(got, []string{"Substitution", "Note", "Other"}) {
		t.Errorf("opened again, the database has the classes %v", got)
	}
	again.Close()

	// Close waits for an action that is running.
	started, finished := make(chan struct{}), make(chan struct{})
	busy := must(open("busy.fado", fado.Options{Actions: map[string]fado.ActionFunc{
		"job.slow": func(context.Context, *fado.ActionCall) (fado.Values, error) {
			close(started)
			time.Sleep(50 * time.Millisecond)
			close(finished)
			return nil, nil
		},
	}}))
	if _, err := busy.DefineAction(ctx, []byte(`{"name":"job.slow"}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.DefineClass(ctx, []byte(scratch("Job", `"text":{"type":"text"}`, oneState+`,"rules":[{"on":"create","do":[{"action":"job.slow"}]}]`)), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Create(ctx, "Job", fado.Values{"text": "x"}, ola); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := busy.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Error("the close did not wait for the action that was running")
	}

	// What was queued and has not run stays in the file.
	queued := must(open("queued.fado", fado.Options{Outbox: fado.OutboxOptions{Manual: true}}))
	if _, err := queued.DefineClass(ctx, []byte(noteClass), setup); err != nil {
		t.Fatal(err)
	}
	maker := scratch("Maker", `"text":{"type":"text"}`, oneState+`,"rules":[{"on":"create","do":[{"action":"create","class":"Note","values":{"text":"text"}}]}]`)
	if _, err := queued.DefineClass(ctx, []byte(maker), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := queued.Create(ctx, "Maker", fado.Values{"text": "made later"}, ola); err != nil {
		t.Fatal(err)
	}
	queued.Close()
	later := must(open("queued.fado", fado.Options{Outbox: fado.OutboxOptions{Manual: true}}))
	if ran, err := later.Drain(ctx, setup); err != nil || ran != 1 {
		t.Errorf("opened again, the queue ran %d actions, %v", ran, err)
	}
	if n, err := later.Count(ctx, fado.Filter{Class: "Note"}, ola); err != nil || n != 1 {
		t.Errorf("the note the queued action makes: %d, %v", n, err)
	}
	later.Close()

	// The clock.
	clock := time.Date(2030, 5, 17, 9, 0, 0, 0, time.UTC)
	dated := must(open("dated.fado", fado.Options{Now: func() time.Time { return clock }}))
	if _, err := dated.DefineClass(ctx, []byte(scratch("Dated", `"day":{"type":"date","default":"TODAY()"}`, oneState)), setup); err != nil {
		t.Fatal(err)
	}
	if doc, err := dated.Create(ctx, "Dated", fado.Values{}, ola); err != nil || doc.Values["day"] != "2030-05-17" || doc.CreatedAt != clock.Unix() {
		t.Errorf("a database with a clock of its own: %v, %v", doc, err)
	}

	// The options: the table has every option of the package and no
	// other.
	options := rows(marked(t, page, "the options"))
	var listed, has []string
	whose := map[string][]string{}
	for _, row := range options {
		for _, name := range strings.Split(row[0], ", ") {
			listed = append(listed, name)
			whose[name] = row
		}
	}
	kind := reflect.TypeOf(fado.Options{})
	for i := 0; i < kind.NumField(); i++ {
		has = append(has, kind.Field(i).Name)
	}
	sort.Strings(listed)
	sort.Strings(has)
	if !reflect.DeepEqual(listed, has) {
		t.Errorf("the chapter lists the options %v, and the package has %v", listed, has)
	}
	// reopened says what a database that was created with the first
	// options answers when it is opened again with the second.
	n := 0
	reopened := func(created, with fado.Options) string {
		t.Helper()
		n++
		name := "option" + string(rune('a'+n)) + ".fado"
		must(open(name, created)).Close()
		db, err := open(name, with)
		if err != nil {
			return "refused, " + fado.ErrorCode(err)
		}
		db.Close()
		return "taken"
	}
	long := func(letters int) string { return strings.Repeat("a", letters) }
	ofDatabase := map[string]func(row []string){
		"Synchronous": func(row []string) {
			if got := reopened(fado.Options{}, fado.Options{Synchronous: "FULL"}); got != row[4] {
				t.Errorf("Synchronous, opened again with another value: %s, and the chapter says %s", got, row[4])
			}
			if got := reopened(fado.Options{}, fado.Options{Synchronous: row[3]}); got != "taken" {
				t.Errorf("a database created with no Synchronous is not %s: %s", row[3], got)
			}
		},
		"MaxText": func(row []string) {
			if got := reopened(fado.Options{}, fado.Options{MaxText: 1000}); got != row[4] {
				t.Errorf("MaxText, opened again with another value: %s, and the chapter says %s", got, row[4])
			}
			if row[3] != "65,536" {
				t.Errorf("the chapter gives %s as the text limit, and the test holds 65,536", row[3])
			}
			texts := must(open("texts.fado", fado.Options{}))
			if _, err := texts.DefineClass(ctx, []byte(noteClass), setup); err != nil {
				t.Fatal(err)
			}
			if _, err := texts.Create(ctx, "Note", fado.Values{"text": long(65536)}, ola); err != nil {
				t.Errorf("a text of 65,536 letters: %v", err)
			}
			if _, err := texts.Create(ctx, "Note", fado.Values{"text": long(65537)}, ola); fado.ErrorCode(err) != "out_of_range" {
				t.Errorf("a text of 65,537 letters: %v", err)
			}
		},
		"BlobStore": func(row []string) {
			if got := reopened(fado.Options{}, fado.Options{BlobStore: "dir"}); got != row[4] {
				t.Errorf("BlobStore, opened again with another value: %s, and the chapter says %s", got, row[4])
			}
			if got := reopened(fado.Options{}, fado.Options{BlobStore: row[3]}); got != "taken" {
				t.Errorf("a database created with no BlobStore is not %s: %s", row[3], got)
			}
		},
		"MaxBlob": func(row []string) {
			higher, lower := reopened(fado.Options{MaxBlob: 1000}, fado.Options{MaxBlob: 2000}), reopened(fado.Options{MaxBlob: 1000}, fado.Options{MaxBlob: 500})
			if got := "a higher one is " + higher + " and kept; a lower one is " + lower; got != row[4] {
				t.Errorf("MaxBlob, opened again with another value: %s, and the chapter says %s", got, row[4])
			}
			// The higher limit is kept: it holds when none is given.
			must(open("limit.fado", fado.Options{MaxBlob: 1000})).Close()
			must(open("limit.fado", fado.Options{MaxBlob: 2000})).Close()
			kept := must(open("limit.fado", fado.Options{}))
			if _, err := kept.PutBlob(ctx, "a.bin", "", strings.NewReader(long(1500)), ola); err != nil {
				t.Errorf("a raised limit is not kept: %v", err)
			}
		},
		"MaxIndexedRows": func(row []string) {
			if got := reopened(fado.Options{}, fado.Options{MaxIndexedRows: 5}) + " and kept"; got != row[4] {
				t.Errorf("MaxIndexedRows, opened again with another value: %s, and the chapter says %s", got, row[4])
			}
		},
		"MaxQuestion": func(row []string) {
			if got := reopened(fado.Options{}, fado.Options{MaxQuestion: time.Minute}) + " and kept"; got != row[4] {
				t.Errorf("MaxQuestion, opened again with another value: %s, and the chapter says %s", got, row[4])
			}
			// None given: five seconds. One given is kept when none is
			// given again.
			fresh := must(open("question.fado", fado.Options{}))
			if got := fresh.MaxQuestion(); got != 5*time.Second || row[3] != "5 seconds" {
				t.Errorf("a new database allows a question %v, and the chapter says %s", got, row[3])
			}
			fresh.Close()
			must(open("question.fado", fado.Options{MaxQuestion: time.Minute})).Close()
			kept := must(open("question.fado", fado.Options{}))
			if got := kept.MaxQuestion(); got != time.Minute {
				t.Errorf("a limit given once is not kept: %v", got)
			}
		},
	}
	for name, row := range whose {
		check, database := ofDatabase[name]
		switch {
		case database && row[2] == "the database's":
			check(row)
		case !database && row[2] == "the program's" && row[4] == "":
		default:
			t.Errorf("the chapter says %s is %s, opened again %q; the test holds otherwise", name, row[2], row[4])
		}
	}

	// What is refused.
	newer := func(by int) error {
		name := "newer.fado"
		if by < 0 {
			name = "earlier.fado"
		}
		must(open(name, fado.Options{})).Close()
		raw, err := sql.Open("sqlite", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		// A newer engine's file has a schema one above this engine's. An
		// earlier one's has schema 12, the one before v0.7.0: a database
		// of v0.7.0 or later, schema 13 and up, is brought forward when
		// it is opened and is not refused (ADR-0078).
		set := "UPDATE schema_version SET version = version + 1"
		if by < 0 {
			set = "UPDATE schema_version SET version = 12"
		}
		if _, err := raw.Exec(set); err != nil {
			t.Fatal(err)
		}
		raw.Close()
		_, err = open(name, fado.Options{})
		return err
	}
	opened := func(name string, o fado.Options) error {
		_, err := open(name, o)
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "letter.txt"), []byte(strings.Repeat("This is a letter, and not a database. ", 8)), 0o600); err != nil {
		t.Fatal(err)
	}
	must(open("plain.fado", fado.Options{MaxBlob: 1000})).Close()
	cases := map[string][]error{
		"gives no path": {func() error { _, err := fado.Open("", fado.Options{}); return err }()},
		"names a file in a directory that is not there":       {opened(filepath.Join("nowhere", "x.fado"), fado.Options{})},
		"names a file that is not a database":                 {opened("letter.txt", fado.Options{})},
		"gives a Synchronous that is neither NORMAL nor FULL": {opened("new1.fado", fado.Options{Synchronous: "OFF"})},
		"gives a MaxText under 64":                            {opened("new2.fado", fado.Options{MaxText: 63})},
		"gives a BlobStore the engine does not know":          {opened("new3.fado", fado.Options{BlobStore: "s3"})},
		"names the store external and gives no Blobs":         {opened("new4.fado", fado.Options{BlobStore: "external"})},
		"gives a function under Actions that is nil, or under a name no action can have": {
			opened("new5.fado", fado.Options{Actions: map[string]fado.ActionFunc{"mail.send": nil}}),
			opened("new6.fado", fado.Options{Actions: map[string]fado.ActionFunc{"not a name": func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil }}}),
		},
		"opens a database with another Synchronous, MaxText or BlobStore than it has": {
			opened("plain.fado", fado.Options{Synchronous: "FULL"}),
			opened("plain.fado", fado.Options{MaxText: 100}),
			opened("plain.fado", fado.Options{BlobStore: "dir"}),
		},
		"opens a database with a lower MaxBlob than it has": {opened("plain.fado", fado.Options{MaxBlob: 999})},
		"opens a database that another program holds alone": {func() error {
			held := must(open("alone.fado", fado.Options{Alone: "the test"}))
			defer held.Close()
			return opened("alone.fado", fado.Options{})
		}()},
		"opens a file that a newer engine made":          {newer(1)},
		"opens a file that an engine before v0.7.0 made": {newer(-1)},
	}
	listedCases := map[string]bool{}
	for _, row := range rows(marked(t, page, "what is refused")) {
		listedCases[row[0]] = true
		if len(cases[row[0]]) == 0 {
			t.Errorf("the chapter says a program that %q is refused, and the test has no such case", row[0])
		}
		for _, err := range cases[row[0]] {
			if got := fado.ErrorCode(err); got != row[1] {
				t.Errorf("a program that %s: the engine answers %q, and the chapter says %s", row[0], got, row[1])
			}
		}
	}
	for what := range cases {
		if !listedCases[what] {
			t.Errorf("the test has a program that %q, and the chapter has no such row", what)
		}
	}
	// A MaxText of 64 is taken.
	if err := opened("smallest.fado", fado.Options{MaxText: 64}); err != nil {
		t.Errorf("a MaxText of 64: %v", err)
	}
}
