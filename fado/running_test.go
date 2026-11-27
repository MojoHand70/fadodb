package fado_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter "Running it" is for whoever keeps a database in service:
// how a backup is made and put back, what a program that died leaves,
// what the check finds, how large the file gets and how fast the engine
// is. TestRunningIt takes the class and the action out of the chapter,
// plays its story, and holds every table of it to what the engine, the
// files on disk and the performance pages say.

// copyFile copies one file, as an administrator's copy does.
func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// copyDir copies a directory with everything under it.
func copyDir(t *testing.T, from, to string) {
	t.Helper()
	if err := os.CopyFS(to, os.DirFS(from)); err != nil {
		t.Fatal(err)
	}
}

// onDisk says whether a file is there.
func onDisk(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRunningIt(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "running-it.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(page), "\r\n", "\n")
	ctx := context.Background()
	var mail, receipt []byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		switch kindOf(m[1]) {
		case "action":
			mail = m[1]
		case "class":
			receipt = m[1]
		}
	}
	if mail == nil || receipt == nil {
		t.Fatal("the chapter shows the action mail.send and the class Receipt")
	}

	// The world outside: a mail server that gives no answer at first.
	// What it was sent is kept by the key of each call.
	answers := false
	var sent []string
	actions := map[string]fado.ActionFunc{
		"mail.send": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
			if !answers {
				return nil, errors.New("the mail server gives no answer")
			}
			sent = append(sent, call.Key)
			return nil, nil
		},
	}
	clock := time.Date(2026, 3, 2, 17, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	// The program of the story runs its queue by hand, so that the test
	// says when the worker works. A program that only looks opens a
	// database the same way and does not run it.
	byHand := fado.OutboxOptions{Manual: true}
	program := fado.Options{BlobStore: "dir", Actions: actions, Now: now, Outbox: byHand}
	looking := fado.Options{Actions: actions, Now: now, Outbox: byHand}
	open := func(path string, o fado.Options) *fado.DB {
		t.Helper()
		db, err := fado.Open(path, o)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	define := func(db *fado.DB) {
		t.Helper()
		if _, err := db.DefineAction(ctx, mail, setup); err != nil {
			t.Fatal(err)
		}
		if _, err := db.DefineClass(ctx, receipt, setup); err != nil {
			t.Fatal(err)
		}
	}
	ola := person("ola", "sales")
	adam := fado.As(fado.Principal{ID: "adam", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}})
	receipts := func(db *fado.DB) string {
		n, err := db.Count(ctx, fado.Filter{Class: "Receipt"}, ola)
		if err != nil {
			return codeOf(err)
		}
		return fmt.Sprint(n)
	}
	waiting := func(db *fado.DB) []fado.QueuedAction {
		t.Helper()
		queued, err := db.Outbox(ctx, fado.OutboxFilter{}, adam)
		if err != nil {
			t.Fatal(err)
		}
		var out []fado.QueuedAction
		for _, row := range queued {
			if row.Status == "pending" {
				out = append(out, row)
			}
		}
		return out
	}
	// state writes what a database holds as the story does.
	state := func(db *fado.DB) string {
		t.Helper()
		found, err := db.CheckBlobs(ctx, adam)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("receipts: %s, files: %d, mails sent: %d, waiting: %d", receipts(db), found.Checked, len(sent), len(waiting(db)))
	}
	// made creates a receipt with no scan.
	made := func(db *fado.DB, id string) {
		t.Helper()
		if _, err := db.Create(ctx, "Receipt", fado.Values{"what": "a taxi"}, ola, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	// filed creates a receipt with its scan, files it, lets the worker
	// run, and answers the hash of the scan.
	filed := func(db *fado.DB, id, what string) string {
		t.Helper()
		scan, err := db.PutBlob(ctx, "scan-"+id+".txt", "text/plain", strings.NewReader("the scan of "+id), ola)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Create(ctx, "Receipt", fado.Values{"what": what, "scan": scan}, ola, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Transition(ctx, "Receipt", id, "file", nil, ola); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Drain(ctx, adam); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(scan["hash"])
	}

	root := t.TempDir()
	books := filepath.Join(root, "acme", "books.fado")
	backup := filepath.Join(root, "backup", "books.fado")
	if err := os.MkdirAll(filepath.Dir(books), 0o700); err != nil {
		t.Fatal(err)
	}

	// The story.
	story := taleOf(t, page)
	db := open(books, program)
	define(db)
	scan := filed(db, "R-1", "a taxi")
	evening := state(db)
	story.step(1, evening)

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if onDisk(books+"-wal") || onDisk(books+"-shm") {
		t.Error("a database that is closed has a file beside it that the chapter does not name")
	}
	copyFile(t, books, backup)
	copyDir(t, books+".blobs", backup+".blobs")
	look := open(backup, looking)
	copied := state(look)
	story.step(2, copied)
	look.Close()

	clock = clock.Add(15 * time.Hour)
	answers = true
	db = open(books, program)
	if _, err := db.Drain(ctx, adam); err != nil {
		t.Fatal(err)
	}
	story.step(3, state(db))

	filed(db, "R-2", "a hotel")
	story.step(4, state(db))

	db.Close()
	copyFile(t, backup, books)
	if err := os.RemoveAll(books + ".blobs"); err != nil {
		t.Fatal(err)
	}
	copyDir(t, backup+".blobs", books+".blobs")
	db = open(books, program)
	if _, err := db.Drain(ctx, adam); err != nil {
		t.Fatal(err)
	}
	story.step(5, state(db))
	story.done()
	if _, err := db.Get(ctx, "Receipt", "R-2", ola); codeOf(err) != "unknown_document" {
		t.Errorf("after the copy is put back, reading R-2 answers %v", err)
	}
	if len(sent) != 3 || sent[0] != sent[2] || sent[1] == sent[0] {
		t.Errorf("the mail of R-1 goes out twice with one key, and the mail of R-2 with another: %v", sent)
	}
	db.Close()

	// What a copy is.
	checks(t, page, "a copy", map[string]func([]string){
		"the database file, and the directory of its files": func(row []string) {
			if copied != evening || !strings.Contains(row[2], "the whole database") {
				t.Errorf("the copy holds %q, and the database held %q", copied, evening)
			}
		},
		"the database file without the directory": func(row []string) {
			bare := filepath.Join(root, "bare", "books.fado")
			copyFile(t, backup, bare)
			db := open(bare, looking)
			defer db.Close()
			if got := receipts(db); got != "1" {
				t.Errorf("the copy without its directory holds %s receipts", got)
			}
			_, _, err := db.OpenFile(ctx, "Receipt", "R-1", scan, ola)
			if code := codeOf(err); code != "unknown_blob" || !strings.Contains(row[2], code) {
				t.Errorf("opening a file of the copy without its directory answers %q, and the chapter says %q", code, row[2])
			}
			found, err := db.CheckBlobs(ctx, adam)
			want := []fado.MissingFile{{Hash: scan, Class: "Receipt", ID: "R-1", Version: 1, Variable: "scan"}}
			if err != nil || found.Checked != 0 || len(found.Mismatched) != 0 || !reflect.DeepEqual(found.Missing, want) ||
				!strings.Contains(row[2], "The check names every receipt that lost its file") {
				t.Errorf("the check of the copy without its directory: %+v, %v", found, err)
			}
		},
		"the database file alone": func(row []string) {
			// The engine folds nothing in for an hour, so that what the
			// copy lacks does not depend on how fast the test runs.
			path := filepath.Join(root, "open", "books.fado")
			os.MkdirAll(filepath.Dir(path), 0o700)
			o := looking
			o.CheckpointEvery = time.Hour
			db := open(path, o)
			defer db.Close()
			define(db)
			made(db, "R-1")
			if !onDisk(path + "-wal") {
				t.Fatal("a database that is open has no -wal file beside it")
			}
			alone := filepath.Join(root, "alone", "books.fado")
			copyFile(t, path, alone)
			lone, err := fado.Open(alone, looking)
			if err != nil {
				t.Fatalf("the copy of the database file alone does not open: %v", err)
			}
			defer lone.Close()
			if got := receipts(lone); got != "unknown_class" || !strings.Contains(row[2], "a database with no class") {
				t.Errorf("the copy of the database file alone answers %q for its receipts", got)
			}
		},
		"a copy that SQLite makes, with VACUUM INTO": func(row []string) {
			path := filepath.Join(root, "busy", "books.fado")
			os.MkdirAll(filepath.Dir(path), 0o700)
			db := open(path, looking)
			define(db)
			// One caller creates a receipt and changes it, again and
			// again, without pause.
			stop := make(chan struct{})
			var pairs atomic.Int64
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					id := fmt.Sprintf("W-%d", i)
					if _, err := db.Create(ctx, "Receipt", fado.Values{"what": "a taxi"}, ola, fado.WithID(id)); err != nil {
						t.Errorf("create: %v", err)
						return
					}
					if _, err := db.Set(ctx, "Receipt", id, fado.Values{"what": "a hotel"}, ola); err != nil {
						t.Errorf("set: %v", err)
						return
					}
					pairs.Add(1)
				}
			}()
			for deadline := time.Now().Add(20 * time.Second); pairs.Load() < 50 && time.Now().Before(deadline); {
				time.Sleep(time.Millisecond)
			}
			copyPath := filepath.Join(root, "busy", "copy.fado")
			second, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatal(err)
			}
			_, err = second.Exec("VACUUM INTO ?", copyPath)
			second.Close()
			if err != nil {
				t.Fatalf("SQLite does not copy the open database: %v", err)
			}
			for after := pairs.Load() + 20; pairs.Load() < after && !t.Failed(); {
				time.Sleep(time.Millisecond)
			}
			close(stop)
			wg.Wait()
			db.Close()

			if onDisk(copyPath+"-wal") || onDisk(copyPath+"-shm") {
				t.Error("the copy SQLite makes has a file beside it")
			}
			if runtime.GOOS != "windows" {
				// The engine closes its own file; SQLite makes the copy
				// as open as any new file of this machine.
				probe := filepath.Join(root, "busy", "probe")
				os.WriteFile(probe, nil, 0o666)
				usual, _ := os.Stat(probe)
				own, _ := os.Stat(path)
				made, _ := os.Stat(copyPath)
				if own.Mode().Perm() != 0o600 || made.Mode().Perm() != usual.Mode().Perm() {
					t.Errorf("the database is %v, the copy %v, and a new file of this machine %v", own.Mode().Perm(), made.Mode().Perm(), usual.Mode().Perm())
				}
			}
			whole := open(copyPath, looking)
			defer whole.Close()
			analyst := fado.As(fado.Principal{ID: "ann", Kind: fado.ActorPerson, System: []string{fado.GroupAnalysts}})
			answered, err := whole.SQL(ctx, `SELECT count(*), sum(version) FROM "Receipt"`, nil, analyst)
			if err != nil {
				t.Fatal(err)
			}
			var count, versions int64
			fmt.Sscan(fmt.Sprint(answered.Rows[0][0]), &count)
			fmt.Sscan(fmt.Sprint(answered.Rows[0][1]), &versions)
			// A receipt of the copy has both its versions, apart from
			// the last one, which may have been copied between the two.
			if count < 50 || (versions != 2*count && versions != 2*count-1) || !strings.Contains(row[2], "one moment between two commits") {
				t.Errorf("the copy holds %d receipts with %d versions", count, versions)
			}
		},
	})

	// A database with three receipts, copied; then a fourth, and the
	// database is closed; then a fifth by a program that dies: its files
	// are copied as it left them.
	died := filepath.Join(root, "died", "books.fado")
	older := filepath.Join(root, "older", "books.fado")
	os.MkdirAll(filepath.Dir(died), 0o700)
	slow := looking
	slow.CheckpointEvery = time.Hour
	db = open(died, slow)
	define(db)
	made(db, "R-1")
	made(db, "R-2")
	made(db, "R-3")
	db.Close()
	copyFile(t, died, older)
	db = open(died, slow)
	made(db, "R-4")
	db.Close()
	db = open(died, slow)
	made(db, "R-5")
	left := func(name string, wal bool, file string) string {
		t.Helper()
		path := filepath.Join(root, name, "books.fado")
		copyFile(t, file, path)
		if wal {
			copyFile(t, died+"-wal", path+"-wal")
		}
		return path
	}
	withWAL, withoutWAL := left("left", true, died), left("removed", false, died)
	under, clean := left("under", true, older), left("clean", false, older)
	db.Close()
	words := map[string]string{"4": "four receipts", "5": "five receipts"}
	holds := func(path string) string {
		t.Helper()
		db, err := fado.Open(path, looking)
		if err != nil {
			return codeOf(err)
		}
		defer db.Close()
		return receipts(db)
	}
	checks(t, page, "after the program died", map[string]func([]string){
		"the -wal file the program left": func(row []string) {
			if got := holds(withWAL); got != "5" || !strings.Contains(row[1], words[got]) {
				t.Errorf("with the -wal file the program left, the database holds %s receipts", got)
			}
		},
		"nothing: the -wal file was removed": func(row []string) {
			if got := holds(withoutWAL); got != "4" || !strings.Contains(row[1], words[got]) {
				t.Errorf("without the -wal file the program left, the database holds %s receipts", got)
			}
		},
	})
	checks(t, page, "putting a copy back", map[string]func([]string){
		"where no -wal file is": func(row []string) {
			if got := holds(clean); got != "3" || row[1] != "the copy" {
				t.Errorf("the copy put where no -wal file is holds %s receipts", got)
			}
		},
		"beside the -wal file of the program that died": func(row []string) {
			got := holds(under)
			if got != "5" || !strings.Contains(row[1], "holds three receipts") || !strings.Contains(row[1], "holds five") {
				t.Errorf("the copy of three receipts, put beside the -wal file of a program that died, holds %s", got)
			}
		},
	})

	// What the check finds, in a database with three scans.
	checked := filepath.Join(root, "checked", "books.fado")
	os.MkdirAll(filepath.Dir(checked), 0o700)
	db = open(checked, program)
	define(db)
	hashes := []string{filed(db, "R-1", "a taxi"), filed(db, "R-2", "a hotel"), filed(db, "R-3", "a dinner")}
	onDiskAs := func(hash string) string { return filepath.Join(checked+".blobs", hash[:2], hash) }
	checks(t, page, "what the check finds", map[string]func([]string){
		"every file as it was put": func(row []string) {
			if found, err := db.CheckBlobs(ctx, adam); err != nil || len(found.Mismatched) != 0 || len(found.Missing) != 0 || found.Checked != 3 || row[1] != "names nothing" {
				t.Errorf("the check of files as they were put: %+v, %v", found, err)
			}
		},
		"a file whose bytes were changed": func(row []string) {
			if err := os.WriteFile(onDiskAs(hashes[0]), []byte("the scan of another"), 0o600); err != nil {
				t.Fatal(err)
			}
			if found, err := db.CheckBlobs(ctx, adam); err != nil || len(found.Mismatched) != 1 || found.Mismatched[0] != hashes[0] || len(found.Missing) != 0 || found.Checked != 3 || row[1] != "names its hash" {
				t.Errorf("the check of a file whose bytes were changed: %+v, %v", found, err)
			}
		},
		"a file that is missing": func(row []string) {
			if err := os.Remove(onDiskAs(hashes[1])); err != nil {
				t.Fatal(err)
			}
			found, err := db.CheckBlobs(ctx, adam)
			want := []fado.MissingFile{{Hash: hashes[1], Class: "Receipt", ID: "R-2", Version: 1, Variable: "scan"}}
			if err != nil || found.Checked != 2 || !reflect.DeepEqual(found.Missing, want) || row[1] != "names the document that holds it" {
				t.Errorf("the check of a file that is missing: %+v, %v", found, err)
			}
		},
	})
	db.Close()

	// A program with none of the functions opens Adam's copy with the
	// worker running: the mail that waits there is tried. The
	// command-line tool starts no worker, which its own test holds.
	tool := filepath.Join(root, "tool", "books.fado")
	copyFile(t, backup, tool)
	copyDir(t, backup+".blobs", tool+".blobs")
	db = open(tool, fado.Options{})
	var tried fado.QueuedAction
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if rows := waiting(db); len(rows) == 1 && rows[0].Attempts >= 2 {
			tried = rows[0]
			break
		}
	}
	db.Close()
	if tried.ErrorCode != "not_registered" || !strings.Contains(text, "tried and fails with `not_registered`") {
		t.Errorf("the mail that waits in a database opened with no functions: %+v", tried)
	}

	// How large the file is.
	sized := filepath.Join(root, "sized", "books.fado")
	os.MkdirAll(filepath.Dir(sized), 0o700)
	size := func() int64 {
		t.Helper()
		info, err := os.Stat(sized)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	const many = 500
	var empty, defined, created, changed int64
	checks(t, page, "the size", map[string]func([]string){
		"a new database, with no class": func(row []string) {
			open(sized, looking).Close()
			if empty = size(); empty < 410<<10 || empty > 490<<10 || row[1] != "about 450 KB" {
				t.Errorf("a new database is %d bytes", empty)
			}
		},
		"a receipt created, or changed": func(row []string) {
			db := open(sized, looking)
			define(db)
			db.Close()
			defined = size()
			db = open(sized, looking)
			for i := range many {
				made(db, fmt.Sprintf("S-%d", i))
			}
			db.Close()
			created = size()
			db = open(sized, looking)
			for i := range many {
				if _, err := db.Set(ctx, "Receipt", fmt.Sprintf("S-%d", i), fado.Values{"what": "a hotel"}, ola); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			changed = size()
			t.Logf("a new database is %d bytes, and %d with the class; %d creates add %d and %d sets %d", empty, defined, many, created-defined, many, changed-created)
			for what, grew := range map[string]int64{"create": (created - defined) / many, "set": (changed - created) / many} {
				if grew < 300 || grew > 800 || row[1] != "about half a kilobyte more each time" {
					t.Errorf("a %s makes the file %d bytes larger", what, grew)
				}
			}
		},
		"a receipt deleted": func(row []string) {
			db := open(sized, looking)
			defer db.Close()
			for i := range many {
				if err := db.Delete(ctx, "Receipt", fmt.Sprintf("S-%d", i), ola); err != nil {
					t.Fatal(err)
				}
			}
			past := fado.As(fado.Principal{ID: "cora", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}})
			events, err := db.Events(ctx, "Receipt", "S-7", past)
			if err != nil || len(events) != 3 {
				t.Errorf("a deleted receipt has %d events: %v", len(events), err)
			}
			if got := receipts(db); got != "0" {
				t.Errorf("after every receipt is deleted the database holds %s", got)
			}
			db.Close()
			if size() < changed || !strings.HasPrefix(row[1], "not smaller.") {
				t.Errorf("deleting every receipt made the file %d bytes, from %d", size(), changed)
			}
		},
	})

	// The figures the chapter takes from the performance pages are
	// there, as it writes them.
	perf := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "docs", "perf", name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(data)), " ")
	}
	v5, v2 := perf("v0.5.0.md"), perf("v0.2.0.md")
	// quoted holds a row to the page it is taken from: the figures the
	// row gives, and the words of the page that give them.
	quoted := func(in string, figures []string, source string) func([]string) {
		return func(row []string) {
			t.Helper()
			for _, figure := range figures {
				if !strings.Contains(row[1], figure) {
					t.Errorf("%s: the chapter says %q, and the test looks for %q", row[0], row[1], figure)
				}
			}
			if !strings.Contains(in, source) {
				t.Errorf("%s: the performance page does not say %q", row[0], source)
			}
		}
	}
	checks(t, page, "measured", map[string]func([]string){
		"one document created":                                      quoted(v5, []string{"0.20 ms"}, "0.20 ms for a create, 0.27 ms for a set"),
		"one document changed":                                      quoted(v5, []string{"0.27 ms"}, "0.20 ms for a create, 0.27 ms for a set"),
		"one document moved by a transition":                        quoted(v5, []string{"0.17 ms", "12,740 a second"}, "0.17 ms / 12,740 /"),
		"many documents moved by one call":                          quoted(v5, []string{"27,488 a second"}, "27,488 over 1,000,000 documents"),
		"many documents created in one transaction":                 quoted(v5, []string{"25,220 a second"}, "/ 25,220 /"),
		"one document read by its id":                               quoted(v5, []string{"0.005 ms at the median"}, "0.005 ms at the median"),
		"a filter on an indexed variable, over 1,000,000 documents": quoted(v5, []string{"15.45 µs"}, "15.45 µs against 15.35 µs over 1,000,000 documents"),
		"opening a database of 11.2 GB":                             quoted(v5, []string{"4.7 ms"}, "4.7 ms with an 11.2 GB file"),
	})
	checks(t, page, "what is slow", map[string]func([]string){
		"a commit made while another caller moves documents in batches of 1,000": quoted(v5, []string{"69 ms"}, "waited 69 ms at p90"),
		"the first ten of a caller's own 10,000 documents, sorted":               quoted(v5, []string{"10.7 ms"}, "10,000 tickets take 10.7 ms"),
		"a commit in a database created with FULL": func(row []string) {
			db, err := fado.Open(filepath.Join(root, "full.fado"), fado.Options{Synchronous: "FULL", Outbox: byHand})
			if err != nil {
				t.Fatalf("a database is not created with FULL: %v", err)
			}
			db.Close()
		},
		"a lookup in a table with no index for it": func(row []string) {
			tables, err := os.ReadFile(filepath.Join("..", "docs", "manual", "tables.md"))
			if err != nil || !strings.Contains(string(tables), "## How fast a lookup is") {
				t.Errorf("the chapter on tables has no section on how fast a lookup is: %v", err)
			}
		},
	})
	if !strings.Contains(text, "made a file of 6.7 GB") || !strings.Contains(v2, "2,000,000 documents with 3,000,000 history rows, the file is 6.7 GB") {
		t.Error("the chapter and the performance page of v0.2.0 differ on the size of the campaign's file")
	}
}
