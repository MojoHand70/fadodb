package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter the program belongs to.
const chapter = "../../docs/manual/serving-a-database.md"

// unix reads a file with the line endings of every platform made one.
func unix(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// printed is what the program prints while the test reads it.
type printed struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (p *printed) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.b.Write(b)
}

func (p *printed) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.b.String()
}

// The chapter shows the program, and the program does what the chapter
// says: an action with no request is defined through it, and its Go
// function runs.
func TestTheChapterShowsTheProgram(t *testing.T) {
	page := unix(t, chapter)
	shown := regexp.MustCompile("(?s)<!-- the program -->\n```go\n(.*?)\n```\n").FindStringSubmatch(page)
	if shown == nil {
		t.Fatal("the chapter has no block for the program")
	}
	if got := strings.TrimRight(unix(t, "main.go"), "\n"); got != shown[1] {
		t.Error("main.go is not the program the chapter shows")
	}

	// The database, its first user and her key, as the tool makes them
	// on the machine.
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	onTheMachine := fado.As(fado.Principal{System: []string{fado.GroupAdministrators}})
	db, err := fado.Open(path, fado.Options{Users: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(ctx, fado.User{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators, fado.GroupDesigners}}, onTheMachine); err != nil {
		t.Fatal(err)
	}
	key, err := db.CreateKey(ctx, "olga", "laptop", "", onTheMachine)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(ctx)
	out, ended := &printed{}, make(chan error, 1)
	go func() { ended <- serve(ctx, path, ln, out) }()
	defer func() {
		stop()
		if err := <-ended; err != nil {
			t.Errorf("the program ended with %v", err)
		}
		// Stopped, it gave the database back.
		db, err := fado.Open(path, fado.Options{Users: true, Alone: "the test"})
		if err != nil {
			t.Fatalf("the database after the program: %v", err)
		}
		db.Close()
	}()

	client := &http.Client{Timeout: 10 * time.Second}
	send := func(command, object string) (int, string) {
		t.Helper()
		req, err := http.NewRequest("POST", "http://"+ln.Addr().String()+"/v1/"+command, strings.NewReader(object))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(body))
	}
	// The definition has no request: it is the contract of the Go
	// function, which this program has.
	if status, body := send("define_action", `{"definition": {"name": "mail.send", "params": {"to": "text"}}}`); status != 200 {
		t.Fatalf("define_action: %d %s", status, body)
	}
	if status, body := send("try_action", `{"action": "mail.send", "args": {"to": "olga@acme.example"}}`); status != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("try_action: %d %s", status, body)
	}
	if got := out.String(); got != "a mail goes to olga@acme.example\n" {
		t.Errorf("the program printed %q", got)
	}
	// A function the program does not have is refused, as everywhere.
	if status, body := send("define_action", `{"definition": {"name": "fax.send", "params": {"to": "text"}}}`); status != 422 || !strings.Contains(body, "not_registered") {
		t.Errorf("an action of a function the program does not have: %d %s", status, body)
	}
	// While it serves, the program holds the database alone.
	if _, err := fado.Open(path, fado.Options{}); fado.ErrorCode(err) != "in_use" || !strings.Contains(err.Error(), "the server of Acme") {
		t.Errorf("a second program on the database: %v", err)
	}
}
