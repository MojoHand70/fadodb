package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The campaign of the library (docs/perf/v0.9.0.md): what one command
// costs on each way to a database, from the Go API to a server over
// HTTP, and what one action costs when the program's function carries
// it out. It runs when FADODB_BENCH names library, with the databases
// in FADODB_BENCH_DIR, a folder on a disk:
//
//	FADODB_BENCH=library FADODB_BENCH_DIR=/path/on/disk go test -count=1 -v -run TestLibraryCampaign .
//
// Each way runs three times on databases of its own, each run five
// rounds of 2,000 reads of one document and 2,000 documents made; a
// run's figure is the median of its rounds.
func TestLibraryCampaign(t *testing.T) {
	if !strings.Contains(os.Getenv("FADODB_BENCH"), "library") {
		t.Skip("FADODB_BENCH=library runs the campaign of the library")
	}
	base := os.Getenv("FADODB_BENCH_DIR")
	if base == "" {
		t.Fatal("FADODB_BENCH_DIR names no folder: the databases are kept on a disk, not in memory")
	}
	const n, rounds, runs = 2000, 5, 3
	cc := compiler(t)
	work := t.TempDir()
	lib := built(t, work)
	program := filepath.Join(work, "measure")
	args := append([]string{"-std=c11", "-O2", "-I.", "-o", program, filepath.Join("testdata", "measure.c")}, linkedTo(lib)...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("measure.c: %v\n%s", err, out)
	}
	core := filepath.Join(work, "fadodb-core")
	build := exec.Command("go", "build", "-o", core, "./cmd/fadodb-core")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fadodb-core: %v\n%s", err, out)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("no python3")
	}

	t.Logf("an fsync of 4 KB: %.2f ms at the median, when the campaign begins", fsync(t, base))
	figures := map[string][][]float64{}
	add := func(way string, run int, values []float64) {
		for len(figures[way]) <= run {
			figures[way] = append(figures[way], nil)
		}
		figures[way][run] = append(figures[way][run], median(values))
	}
	for run := range runs {
		dir, err := os.MkdirTemp(base, "library-campaign-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)

		get, create := goAPI(t, filepath.Join(dir, "go.fado"), n, rounds, false)
		add("Go API: get", run, get)
		add("Go API: create", run, create)
		get, create = goAPI(t, filepath.Join(dir, "exec.fado"), n, rounds, true)
		add("Go, the command set (Exec): get", run, get)
		add("Go, the command set (Exec): create", run, create)
		get, create = librarysGo(t, filepath.Join(dir, "library.fado"), n, rounds)
		add("Go, the library's own code without C: get", run, get)
		add("Go, the library's own code without C: create", run, create)

		out, err := exec.Command(program, filepath.Join(dir, "c.fado"), strconv.Itoa(n), strconv.Itoa(rounds)).Output()
		if err != nil {
			t.Fatalf("measure: %v", err)
		}
		var cget, ccreate, copen []float64
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			name, value, _ := strings.Cut(line, " ")
			v, _ := strconv.ParseFloat(value, 64)
			switch name {
			case "get":
				cget = append(cget, v)
			case "create":
				ccreate = append(ccreate, v)
			case "open":
				copen = append(copen, v)
			}
		}
		add("C, the library: get", run, cget)
		add("C, the library: create", run, ccreate)
		add("C, fado_open of a new file", run, copen)

		py := exec.Command(python, filepath.Join("..", "clients", "py", "measure.py"), dir, strconv.Itoa(n), strconv.Itoa(rounds))
		py.Env = append(os.Environ(), "FADODB_LIBRARY="+lib, "FADODB_PROGRAM="+core)
		out, err = py.Output()
		if err != nil {
			t.Fatalf("measure.py: %v", err)
		}
		var told struct {
			InProcess struct {
				Get, Create []float64
				Load, Open  float64
			} `json:"in_process"`
			Actions struct {
				Drain []float64 `json:"drain_per_action"`
			}
			HTTP struct{ Get, Create []float64 } `json:"http"`
			Kept struct{ Get, Create []float64 } `json:"kept"`
		}
		if err := json.Unmarshal(out, &told); err != nil {
			t.Fatalf("measure.py answered %s: %v", out, err)
		}
		add("Python, in the same process: get", run, told.InProcess.Get)
		add("Python, in the same process: create", run, told.InProcess.Create)
		add("Python, loading the library", run, []float64{told.InProcess.Load})
		add("Python, fadodb.open of a new file", run, []float64{told.InProcess.Open})
		add("Python, over HTTP to fadodb-core serve: get", run, told.HTTP.Get)
		add("Python, over HTTP to fadodb-core serve: create", run, told.HTTP.Create)
		add("Python, over HTTP, one connection kept (http.client): get", run, told.Kept.Get)
		add("Python, over HTTP, one connection kept (http.client): create", run, told.Kept.Create)
		add("An action carried out by a Python function, per action of a drain", run, told.Actions.Drain)
		add("An action carried out by a Go function, per action of a drain", run, goActions(t, filepath.Join(dir, "goactions.fado"), n, rounds))
	}
	t.Logf("an fsync of 4 KB: %.2f ms at the median, when the campaign ends", fsync(t, base))

	ways := make([]string, 0, len(figures))
	for way := range figures {
		ways = append(ways, way)
	}
	sort.Strings(ways)
	var b strings.Builder
	fmt.Fprintf(&b, "\n| Way | µs, three runs |\n|---|---|\n")
	for _, way := range ways {
		var parts []string
		for _, run := range figures[way] {
			parts = append(parts, fmt.Sprintf("%.1f", run[0]))
		}
		fmt.Fprintf(&b, "| %s | %s |\n", way, strings.Join(parts, ", "))
	}
	t.Log(b.String())
	t.Logf("%s %s/%s, %d rounds of %d", runtime.Version(), runtime.GOOS, runtime.GOARCH, rounds, n)
}

// goAPI measures reads and makes with the Go API, or with the command
// set of Exec when byCommand.
func goAPI(t *testing.T, path string, n, rounds int, byCommand bool) (get, create []float64) {
	t.Helper()
	ctx := context.Background()
	db, err := fado.Open(path, fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dan := fado.Principal{ID: "dan", Kind: "person", System: []string{"designers"}}
	ann := fado.Principal{ID: "ann", Kind: "person"}
	note := `{"name": "Note", "variables": {"text": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`
	if _, err := db.Exec(ctx, dan, "define_class", []byte(`{"definition": `+note+`}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, ann, "create", []byte(`{"class": "Note", "id": "N-0", "values": {"text": "the one read"}}`)); err != nil {
		t.Fatal(err)
	}
	made := 0
	for range rounds {
		began := time.Now()
		for range n {
			if byCommand {
				_, err = db.Exec(ctx, ann, "get", []byte(`{"class": "Note", "id": "N-0"}`))
			} else {
				_, err = db.Get(ctx, "Note", "N-0", fado.As(ann))
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		get = append(get, float64(time.Since(began).Microseconds())/float64(n))
		began = time.Now()
		for range n {
			id := "M-" + strconv.Itoa(made)
			made++
			if byCommand {
				_, err = db.Exec(ctx, ann, "create", []byte(`{"class": "Note", "id": "`+id+`", "values": {"text": "a note"}}`))
			} else {
				_, err = db.Create(ctx, "Note", fado.Values{"text": "a note"}, fado.As(ann), fado.WithID(id))
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		create = append(create, float64(time.Since(began).Microseconds())/float64(n))
	}
	return get, create
}

// librarysGo measures the library's own code, the functions fado_open
// and fado_exec call, without C: what the library adds to the command
// set before C is crossed.
func librarysGo(t *testing.T, path string, n, rounds int) (get, create []float64) {
	t.Helper()
	db, refusal := openDatabase(path, "", nil)
	if db == 0 {
		t.Fatal(string(refusal))
	}
	defer closeDatabase(db)
	const dan, ann = `{"id": "dan", "kind": "person", "system_groups": ["designers"]}`, `{"id": "ann", "kind": "person"}`
	execCommand(db, dan, "define_class", `{"definition": {"name": "Note", "variables": {"text": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}}`)
	execCommand(db, ann, "create", `{"class": "Note", "id": "N-0", "values": {"text": "the one read"}}`)
	made := 0
	for range rounds {
		began := time.Now()
		for range n {
			execCommand(db, ann, "get", `{"class": "Note", "id": "N-0"}`)
		}
		get = append(get, float64(time.Since(began).Microseconds())/float64(n))
		began = time.Now()
		for range n {
			execCommand(db, ann, "create", `{"class": "Note", "id": "M-`+strconv.Itoa(made)+`", "values": {"text": "a note"}}`)
			made++
		}
		create = append(create, float64(time.Since(began).Microseconds())/float64(n))
	}
	return get, create
}

// goActions measures an action a Go function carries out, as measure.py
// does one of Python: n documents sent, then the queue run.
func goActions(t *testing.T, path string, n, rounds int) []float64 {
	t.Helper()
	ctx := context.Background()
	db, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Actions: map[string]fado.ActionFunc{
		"books.post": func(_ context.Context, call *fado.ActionCall) (fado.Values, error) {
			return fado.Values{"entry": "JE-" + call.Key[len(call.Key)-6:]}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dan := fado.Principal{ID: "dan", Kind: "person", System: []string{"designers"}}
	ada := fado.Principal{ID: "ada", Kind: "person", System: []string{"administrators"}}
	ann := fado.Principal{ID: "ann", Kind: "person"}
	for _, c := range []struct{ command, object string }{
		{"define_action", `{"definition": {"name": "books.post", "returns": {"entry": "text"}}}`},
		{"define_class", `{"definition": {"name": "Sent", "variables": {"entry": {"type": "text"}}, "states": {"initial": "new", "list": ["new", "sent"]}, "transitions": [{"name": "send", "from": "new", "to": "sent"}], "rules": [{"on": "transition", "name": "send", "do": [{"action": "books.post", "into": {"entry": "entry"}}]}], "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE", "transition": {"send": "TRUE"}}}}`},
	} {
		if _, err := db.Exec(ctx, dan, c.command, []byte(c.object)); err != nil {
			t.Fatalf("%s: %v", c.command, err)
		}
	}
	var out []float64
	made := 0
	for range rounds {
		for range n {
			id := "S-" + strconv.Itoa(made)
			made++
			if _, err := db.Exec(ctx, ann, "create", []byte(`{"class": "Sent", "id": "`+id+`"}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, ann, "transition", []byte(`{"class": "Sent", "id": "`+id+`", "transition": "send"}`)); err != nil {
				t.Fatal(err)
			}
		}
		began := time.Now()
		ran, err := db.Drain(ctx, fado.As(ada))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, float64(time.Since(began).Microseconds())/float64(max(ran, 1)))
	}
	return out
}

// fsync is the median of 200 writes of 4 KB, each made to disk, in ms.
func fsync(t *testing.T, dir string) float64 {
	t.Helper()
	f, err := os.CreateTemp(dir, "fsync-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	block := make([]byte, 4096)
	var took []float64
	for range 200 {
		began := time.Now()
		if _, err := f.Write(block); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		took = append(took, float64(time.Since(began).Microseconds())/1000)
	}
	return median(took)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}
