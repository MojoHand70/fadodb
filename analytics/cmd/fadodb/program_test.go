package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The program with the analytical engine, built and run as an operator
// runs it (ADR-0089): a database it serves keeps a copy of its
// documents beside its own file, and the flow of a class is answered
// from the copy. The test builds the program once; or it is given one,
// in FADODB_PROGRAM, and tries that: the very file a release publishes
// (scripts/program.sh try).

var served = regexp.MustCompile(`serving \S+ at (http://\S+)`)

func built(t *testing.T) string {
	t.Helper()
	if given := os.Getenv("FADODB_PROGRAM"); given != "" {
		if _, err := os.Stat(given); err != nil {
			t.Fatalf("the program to try, FADODB_PROGRAM: %v", err)
		}
		return given
	}
	bin := filepath.Join(t.TempDir(), "fadodb")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("the program does not build: %v\n%s", err, out)
	}
	return bin
}

// tool runs the program to its end and answers what it printed.
func tool(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("fadodb %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestTheProgramWithTheEngine(t *testing.T) {
	bin := built(t)
	if got := tool(t, bin, "version"); !strings.Contains(got, "with the analytical engine") {
		t.Errorf("the program says of itself: %s", got)
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "acme.fado")
	tool(t, bin, "init", db)
	tool(t, bin, "user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", db)
	key := ""
	for _, word := range strings.Fields(tool(t, bin, "key", "new", "olga", "--label", "test", "--db", db)) {
		if strings.HasPrefix(word, "fado_") {
			key = word
		}
	}
	if key == "" {
		t.Fatal("the program printed no key")
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"analytics": {"memory": "512MB", "threads": 2}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	server := exec.CommandContext(ctx, bin, "serve", db, "--listen", "127.0.0.1:0", "--settings", settings)
	server.Cancel = func() error {
		// Windows has no interrupt to send to another process: the
		// server is ended there, and the database is the file it left.
		if runtime.GOOS == "windows" {
			return server.Process.Kill()
		}
		return server.Process.Signal(os.Interrupt)
	}
	server.WaitDelay = 10 * time.Second
	said, err := server.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	// What the server says when it starts: where it serves, and that it
	// keeps the copy.
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(said)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	base, keeps := "", false
	deadline := time.After(30 * time.Second)
	for base == "" || !keeps {
		select {
		case line, open := <-lines:
			if !open {
				t.Fatalf("the server ended before it served; it keeps the copy: %v", keeps)
			}
			if m := served.FindStringSubmatch(line); m != nil {
				base = m[1]
			}
			keeps = keeps || strings.Contains(line, "it keeps a copy of the documents")
		case <-deadline:
			t.Fatalf("the server did not say where it serves and that it keeps a copy: %q, %v", base, keeps)
		}
	}
	go func() {
		for range lines {
		}
	}()

	ask := func(command, body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest("POST", base+"/v1/"+command, bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %s\n%s", command, res.Status, raw)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s answered what is no JSON: %s", command, raw)
		}
		return out
	}
	ask("define_class", `{"definition": {"name": "Invoice", "title": "number",
 "variables": {"number": {"type": "text", "required": true}},
 "states": {"initial": "draft", "list": ["draft", "review"]},
 "transitions": [{"name": "file", "from": "draft", "to": "review"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}}`)
	for i := 1; i <= 5; i++ {
		ask("create", fmt.Sprintf(`{"class": "Invoice", "id": "i-%d", "values": {"number": "FV-%d"}}`, i, i))
	}
	ask("transition", `{"class": "Invoice", "id": "i-1", "transition": "file"}`)
	ask("transition", `{"class": "Invoice", "id": "i-2", "transition": "file"}`)
	flow, _ := ask("flow", `{"class": "Invoice"}`)["flow"].(map[string]any)
	in := map[string]float64{}
	states, _ := flow["states"].([]any)
	for _, st := range states {
		m, _ := st.(map[string]any)
		name, _ := m["name"].(string)
		in[name], _ = m["documents"].(float64)
	}
	if in["draft"] != 3 || in["review"] != 2 {
		t.Errorf("the flow the server answered: %v", flow)
	}
	// The copy stands beside the database.
	if st, err := os.Stat(db + ".duckdb"); err != nil || st.Size() == 0 {
		t.Errorf("the copy beside the database: %v", err)
	}
	stop()
	if err := server.Wait(); err != nil && ctx.Err() == nil {
		t.Errorf("the server ended with %v", err)
	}
}
