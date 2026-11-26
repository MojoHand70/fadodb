package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The program built with an analytical engine gives it to the database
// it serves (ADR-0089). The engine itself is DuckDB's, in the module
// analytics; here a stand-in says what the server asked of it.

type standInEngine struct {
	mu              sync.Mutex
	memory          string
	threads, opened int
	fail            error
}

func (e *standInEngine) OpenCopy(context.Context, string) (fado.Copy, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail != nil {
		return nil, e.fail
	}
	e.opened++
	return standInCopy{}, nil
}

// standInCopy takes every feed and holds nothing.
type standInCopy struct{}

func (standInCopy) Marks(context.Context) (map[string]string, error) { return nil, nil }
func (standInCopy) Lay(context.Context, []fado.CopyTable) error      { return nil }
func (standInCopy) Feed(_ context.Context, give func(fado.CopyWriter) error, _ map[string]string) error {
	return give(standInWriter{})
}
func (standInCopy) Ask(context.Context, string, ...any) ([]string, [][]any, error) {
	return nil, nil, errors.New("the stand-in answers no statement")
}
func (standInCopy) Close() error { return nil }

type standInWriter struct{}

func (standInWriter) Append(string, []any) error    { return nil }
func (standInWriter) Remove(string, []string) error { return nil }
func (standInWriter) Empty(string) error            { return nil }

// withEngine makes the program one that was built with the stand-in,
// for the length of a test.
func withEngine(t *testing.T, e *standInEngine) {
	t.Helper()
	old := makeEngine
	makeEngine = func(memory string, threads int) fado.Analytics {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.memory, e.threads = memory, threads
		return e
	}
	t.Cleanup(func() { makeEngine = old })
}

func servedDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("fadodb %s: %d\n%s", strings.Join(args, " "), code, said)
		}
	}
	return path
}

func TestAServerWithTheAnalyticalEngine(t *testing.T) {
	path := servedDatabase(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(settings, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The program built without the engine says nothing of a copy, and
	// refuses settings that name the engine.
	_, said, stop := serving(t, "--listen", "127.0.0.1:0", path)
	if strings.Contains(said.String(), "copy of the documents") {
		t.Errorf("a server without the engine speaks of a copy:\n%s", said)
	}
	stop()
	write(`{"analytics": {"memory": "1GB"}}`)
	if code, _, said := tool("serve", "--listen", "127.0.0.1:0", "--settings", settings, path); code != 2 || !strings.Contains(said, "this program is built without it") {
		t.Errorf("settings that name the engine, for a program without it: %d\n%s", code, said)
	}
	if code, _, said := tool("version"); code != 0 || strings.Contains(said, "analytical engine") {
		t.Errorf("the version of the program without the engine: %d %s", code, said)
	}

	// The program built with it: the database it serves is given the
	// engine, with the memory the server keeps for it.
	e := &standInEngine{}
	withEngine(t, e)
	if code, out, _ := tool("version"); code != 0 || !strings.Contains(out, ", with the analytical engine") {
		t.Errorf("the version of the program with the engine: %d %s", code, out)
	}
	_, said, stop = serving(t, "--listen", "127.0.0.1:0", path)
	if !strings.Contains(said.String(), "it keeps a copy of the documents for the questions that read everything") {
		t.Errorf("a server with the engine does not say it keeps a copy:\n%s", said)
	}
	stop()
	if e.opened != 1 || e.memory != defaultEngineMemory || e.threads != 0 {
		t.Errorf("the engine was opened %d times, with %q and %d threads; want once, with %s and its own threads", e.opened, e.memory, e.threads, defaultEngineMemory)
	}
	// The settings say how much memory it may take and how many threads.
	write(`{"analytics": {"memory": "512MB", "threads": 3}}`)
	_, _, stop = serving(t, "--listen", "127.0.0.1:0", "--settings", settings, path)
	stop()
	if e.memory != "512MB" || e.threads != 3 {
		t.Errorf("the engine was made with %q and %d threads; the settings say 512MB and 3", e.memory, e.threads)
	}
	// An engine that cannot open its copy stops nothing: the server
	// serves, and says that it keeps none and why.
	e.fail = errors.New("the disk of the copy is full")
	_, said, stop = serving(t, "--listen", "127.0.0.1:0", path)
	if !strings.Contains(said.String(), "it keeps no copy of the documents") || !strings.Contains(said.String(), "the disk of the copy is full") {
		t.Errorf("a server whose engine cannot open its copy:\n%s", said)
	}
	stop()

	// What the settings refuse.
	for name, text := range map[string]string{
		"memory that is no size":    `{"analytics": {"memory": "plenty"}}`,
		"memory as a number":        `{"analytics": {"memory": 4}}`,
		"no thread":                 `{"analytics": {"threads": 0}}`,
		"too many threads":          `{"analytics": {"threads": 4096}}`,
		"a member it does not have": `{"analytics": {"every": 5}}`,
		"what is no object":         `{"analytics": true}`,
	} {
		if _, err := parseSettings([]byte(text)); err == nil || !strings.Contains(err.Error(), "analytics") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if s, err := parseSettings([]byte(`{"analytics": {}}`)); err != nil || !s.analytics || s.engineMemory != "" || s.engineThreads != 0 {
		t.Errorf("settings that name the engine and say nothing of it: %v, %+v", err, s)
	}
}

// The time zone of the database in the settings (ADR-0093): the server
// gives it to the database it opens, which keeps it.
func TestSettings_TheTimeZone(t *testing.T) {
	s, err := parseSettings([]byte(`{"timezone": "Europe/Warsaw"}`))
	if err != nil || s.timezone != "Europe/Warsaw" {
		t.Fatalf("the time zone of the settings: %v, %+v", err, s)
	}
	if s, err := parseSettings([]byte(`{}`)); err != nil || s.timezone != "" {
		t.Errorf("settings that name no zone: %v, %q", err, s.timezone)
	}
	for name, text := range map[string]string{
		"a zone nobody knows":       `{"timezone": "Europe/Atlantis"}`,
		"the zone of the machine":   `{"timezone": "Local"}`,
		"an offset in place of one": `{"timezone": 2}`,
		"no zone":                   `{"timezone": ""}`,
	} {
		if _, err := parseSettings([]byte(text)); err == nil || !strings.Contains(err.Error(), "timezone") {
			t.Errorf("%s: %v", name, err)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"timezone": "Europe/Warsaw"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, stop := serving(t, "--listen", "127.0.0.1:0", "--settings", settings, path)
	stop()
	// Served again with no settings, the database is where it was put.
	db, err := fado.Open(path, fado.Options{Users: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.TimeZone(); got != "Europe/Warsaw" {
		t.Errorf("the zone the database kept: %s", got)
	}
}
