package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOpenCampaign measures what it takes to open a database of fifty
// classes and what such a database holds open (ADR-0056, Performance):
// a program that serves a database for each customer opens them as they
// are called, and holds many at once.
//
// The database has fifty classes, each with twelve variables, four
// states, three transitions, a rule and its access rules, and 200
// documents a class. It is opened twenty times, one after the other,
// and then 50 copies of it are held open at once: the memory, the open
// files and the goroutines of each are what the process holds after
// the collector ran, less what it held before.
//
// What a database holds grows with its classes, so the campaign takes
// its size from the environment: FADODB_BENCH_CLASSES classes, fifty
// by default, and FADODB_BENCH_HELD copies held open at once, fifty by
// default. ADR-0056 measured 300 databases of four classes.
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real disk.
// It prints a Markdown table with -v.
func TestOpenCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the campaign of opening")
	}
	const documents, opens = 200, 20
	classes, held := 50, 50
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_CLASSES")); err == nil && v > 0 {
		classes = v
	}
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_HELD")); err == nil && v > 0 {
		held = v
	}
	base := filepath.Join(dir, fmt.Sprintf("open-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	ctx := context.Background()
	setup := As(Principal{ID: "setup", Groups: []string{"sales", "finance"}, System: []string{GroupDesigners, GroupAdministrators}})
	options := Options{Outbox: OutboxOptions{Manual: true}}

	// The database: fifty classes, defined one at a time as a designer
	// defines them, and their documents.
	first := filepath.Join(base, "c0.fado")
	s, err := Open(first, options)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	for c := range classes {
		if _, err := s.DefineClass(ctx, []byte(openClass(c)), setup); err != nil {
			t.Fatal(err)
		}
	}
	defined := time.Since(began)
	for c := range classes {
		docs := make([]Values, documents)
		for d := range docs {
			docs[d] = Values{"number": "N-" + strconv.Itoa(d), "customer": "Globex", "net": "100.00 PLN", "qty": d%7 + 1, "note": "as agreed"}
		}
		if _, err := s.CreateMany(ctx, fmt.Sprintf("Class%02d", c), docs, setup); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}

	// Opened and closed, one after the other.
	took, closed := make([]time.Duration, opens), make([]time.Duration, opens)
	for i := range took {
		began := time.Now()
		s, err := Open(first, options)
		if err != nil {
			t.Fatal(err)
		}
		took[i] = time.Since(began)
		// The engine brings a class of its own beside the fifty.
		if n := len(s.Classes()); n < classes {
			t.Fatalf("the database has %d classes", n)
		}
		began = time.Now()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		closed[i] = time.Since(began)
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	sort.Slice(closed, func(i, j int) bool { return closed[i] < closed[j] })

	// Fifty copies of it, held open at once.
	paths := []string{first}
	for i := 1; i < held; i++ {
		to := filepath.Join(base, "c"+strconv.Itoa(i)+".fado")
		if err := copyFile(first, to); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, to)
	}
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	files := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			return -1
		}
		return len(entries)
	}
	// resident is the memory of the process that is in use, as the
	// system counts it: SQLite's own memory is in it, and not in what
	// Go counts as its heap.
	resident := func() uint64 {
		// What Go has freed is given back first, so that what is left
		// is what is held.
		debug.FreeOSMemory()
		text, err := os.ReadFile("/proc/self/statm")
		if err != nil {
			return 0
		}
		fields := strings.Fields(string(text))
		if len(fields) < 2 {
			return 0
		}
		pages, _ := strconv.ParseUint(fields[1], 10, 64)
		return pages * uint64(os.Getpagesize())
	}
	heapBefore, residentBefore, filesBefore, routinesBefore := heap(), resident(), files(), runtime.NumGoroutine()
	stores := make([]*Store, 0, held)
	began = time.Now()
	for _, path := range paths {
		s, err := Open(path, options)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, s)
	}
	openedAll := time.Since(began)
	// Each is used once, as a caller would: a document read, and one
	// moved.
	ids := make([]string, len(stores))
	for i, s := range stores {
		docs, err := s.Query(ctx, Filter{Class: "Class00", Limit: 1}, setup)
		if err != nil || len(docs) != 1 {
			t.Fatalf("a document of an open database: %v %v", docs, err)
		}
		ids[i] = docs[0].ID
		if _, err := s.Transition(ctx, "Class00", docs[0].ID, "submit", nil, setup); err != nil {
			t.Fatal(err)
		}
	}
	heapAfter, residentAfter, filesAfter, routinesAfter := heap(), resident(), files(), runtime.NumGoroutine()

	// Ten of them are read by sixteen callers at once: each reader has
	// a connection of its own, which the database keeps ready for a
	// quarter of a minute after.
	const readers = 16
	busy := min(10, held)
	var reading sync.WaitGroup
	for i := range busy {
		for range readers {
			reading.Add(1)
			go func() {
				defer reading.Done()
				for range 200 {
					if _, err := stores[i].Get(ctx, "Class00", ids[i], setup); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
	}
	reading.Wait()
	heapBusy, residentBusy, filesBusy := heap(), resident(), files()
	connectionsBusy := 0
	for _, s := range stores[:busy] {
		connectionsBusy += s.db.Stats().OpenConnections
	}
	// And once nobody has read for a while: the connections that were
	// kept ready are closed.
	time.Sleep(2*readerIdle + 5*time.Second)
	_, residentQuiet, filesQuiet := heap(), resident(), files()
	connectionsQuiet := 0
	for _, s := range stores[:busy] {
		connectionsQuiet += s.db.Stats().OpenConnections
	}
	began = time.Now()
	for _, s := range stores {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	closedAll := time.Since(began)

	ms := func(d time.Duration) string {
		return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 2, 64) + " ms"
	}
	fileCount := "not counted on this system"
	if filesBefore >= 0 {
		fileCount = strconv.FormatFloat(float64(filesAfter-filesBefore)/float64(held), 'f', 1, 64)
	}
	rows := []string{
		fmt.Sprintf("| the database | %d classes, %d documents, %.1f MB; defining the classes took %s |", classes, classes*documents, float64(info.Size())/1e6, ms(defined)),
		fmt.Sprintf("| opening it, one after the other, %d times | %s at the median, %s the longest |", opens, ms(took[opens/2]), ms(took[opens-1])),
		fmt.Sprintf("| closing it | %s at the median, %s the longest |", ms(closed[opens/2]), ms(closed[opens-1])),
		fmt.Sprintf("| opening %d copies and holding them | %s for all, %s each |", held, ms(openedAll), ms(openedAll/time.Duration(held))),
		fmt.Sprintf("| memory an open database holds, after a read and a commit | %.2f MB of Go's own, %.2f MB as the system counts it |", float64(heapAfter-heapBefore)/float64(held)/1e6, float64(residentAfter-residentBefore)/float64(held)/1e6),
		fmt.Sprintf("| open files an open database holds | %s |", fileCount),
		fmt.Sprintf("| what %d callers reading at once add to one, as they finish | %.2f MB of Go's own, %.2f MB as the system counts it, %.1f open files; it has %.1f connections open |", readers,
			float64(int64(heapBusy)-int64(heapAfter))/float64(busy)/1e6, float64(int64(residentBusy)-int64(residentAfter))/float64(busy)/1e6, float64(filesBusy-filesAfter)/float64(busy), float64(connectionsBusy)/float64(busy)),
		fmt.Sprintf("| the same, %s after the last read | %.2f MB as the system counts it, %.1f open files; it has %.1f connections open |", (2*readerIdle + 5*time.Second).String(),
			float64(int64(residentQuiet)-int64(residentAfter))/float64(busy)/1e6, float64(filesQuiet-filesAfter)/float64(busy), float64(connectionsQuiet)/float64(busy)),
		fmt.Sprintf("| goroutines an open database holds | %.1f |", float64(routinesAfter-routinesBefore)/float64(held)),
		fmt.Sprintf("| closing the %d | %s for all, %s each |", held, ms(closedAll), ms(closedAll/time.Duration(held))),
	}
	t.Logf("\n| What | Measured |\n|---|---|\n%s", strings.Join(rows, "\n"))
}

// openClass is one of the fifty classes of the campaign: an order with
// twelve variables, four states, three transitions, a rule and its
// access rules.
func openClass(n int) string {
	return fmt.Sprintf(`{
  "name": "Class%02d",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "customer": {"type": "text", "required": true},
    "net":      {"type": "money", "scale": 2},
    "qty":      {"type": "number", "default": "1"},
    "unit":     {"type": "number", "default": "10"},
    "note":     {"type": "text"},
    "checked":  {"type": "bool", "default": "FALSE"},
    "decided":  {"type": "text"},
    "gross":    {"formula": "AMOUNT(net) * 1.23"},
    "each":     {"formula": "IF(qty > 0, unit / qty, 0)"},
    "long":     {"formula": "LEN(note) > 100"},
    "label":    {"formula": "number & \" for \" & customer"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "approved", "closed"], "final": ["closed"]},
  "transitions": [
    {"name": "submit",  "from": "draft",    "to": "review",   "guard": "qty > 0"},
    {"name": "approve", "from": "review",   "to": "approved", "guard": "checked OR AMOUNT(net) < 1000"},
    {"name": "close",   "from": "approved", "to": "closed"}
  ],
  "rules": [
    {"on": "transition", "name": "approve", "do": [{"action": "set", "values": {"decided": "USER()"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"sales\") OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"sales\")",
    "delete": "FALSE",
    "transition": {"approve": "INGROUP(\"finance\")"}
  }
}`, n)
}

// copyFile copies a database that no program has open.
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
