package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/server"
)

// aFace makes the folder of a small face under dir and answers where it
// is. It holds two names that are the server's own addresses.
func aFace(t *testing.T, dir string) string {
	t.Helper()
	face := filepath.Join(dir, "face")
	for name, body := range map[string]string{
		"index.html":       "<!doctype html><title>The face of Acme</title>",
		"app.js":           "export const here = true;\n",
		"v1/health":        "not the server's health",
		"auth/signin.html": "not the server's sign-in",
	} {
		at := filepath.Join(face, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return face
}

// fetch reads an address of a server and answers the status, the body
// and the headers.
func fetch(t *testing.T, method, url string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A redirect is answered as it is, not followed.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

// Olga starts the server with the folder of a face (ADR-0073). Its files
// answer at the root of the server's address, and the server's own
// addresses always come first.
func TestTheServerHandsOutThePagesOfAFace(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", db); code != 0 {
		t.Fatalf("init ended with %d: %s", code, said)
	}
	face := aFace(t, dir)
	addr, said, _ := serving(t, db, "--listen", "127.0.0.1:0", "--pages", face)
	at := "http://" + addr

	// The folder's files, at the root.
	code, body, h := fetch(t, http.MethodGet, at+"/")
	if code != http.StatusOK || !strings.Contains(body, "The face of Acme") {
		t.Errorf("the address of the server is answered %d:\n%s", code, body)
	}
	if h.Get("Content-Security-Policy") != server.FolderPolicy {
		t.Errorf("a page of the face carries the policy %q", h.Get("Content-Security-Policy"))
	}
	if code, body, h := fetch(t, http.MethodGet, at+"/app.js"); code != http.StatusOK || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") || !strings.Contains(body, "here = true") {
		t.Errorf("a script of the face is answered %d as %q:\n%s", code, h.Get("Content-Type"), body)
	}
	if code, _, h := fetch(t, http.MethodGet, at+"/nothing.html"); code != http.StatusNotFound || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") {
		t.Errorf("a page that is not there is answered %d as %q", code, h.Get("Content-Type"))
	}
	if code, _, _ := fetch(t, http.MethodPost, at+"/"); code != http.StatusMethodNotAllowed {
		t.Errorf("the folder is written to: POST / is answered %d", code)
	}

	// The server's own addresses come first, whatever the folder holds.
	if code, body, _ := fetch(t, http.MethodGet, at+"/v1/health"); code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Errorf("the health of the server is answered %d:\n%s", code, body)
	}
	code, body, h = fetch(t, http.MethodGet, at+"/auth/signin")
	if code != http.StatusOK || !strings.Contains(body, `action="/auth/signin"`) {
		t.Errorf("the page where people sign in is answered %d:\n%s", code, body)
	}
	// The server's own page runs the server's one script and takes forms
	// to the server alone; a face's policy is another.
	if policy := h.Get("Content-Security-Policy"); policy == server.FolderPolicy || !strings.Contains(policy, "script-src 'self';") || !strings.Contains(policy, "form-action 'self';") {
		t.Errorf("the server's own page carries the policy of a face: %q", policy)
	}
	if code, body, _ := fetch(t, http.MethodGet, at+"/auth/signin.html"); code == http.StatusOK || strings.Contains(body, "not the server's sign-in") {
		t.Errorf("a file of the folder under /auth/ is handed out: %d\n%s", code, body)
	}
	for _, own := range []string{"/v1/whoami", "/v1", "/mcp", "/mcp/"} {
		code, body, h := fetch(t, http.MethodPost, at+own)
		if h.Get("Content-Security-Policy") == server.FolderPolicy || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
			t.Errorf("POST %s is answered by the folder: %d %q\n%s", own, code, h.Get("Content-Type"), body)
		}
	}

	// What the server says when it starts.
	for _, line := range []string{
		"fadodb serve: it hands out the pages of " + face + " at " + at + "/\n",
		"fadodb serve: the folder of pages holds v1, which is an address of the server's own: it is never handed out\n",
		"fadodb serve: the folder of pages holds auth, which is an address of the server's own: it is never handed out\n",
	} {
		if !strings.Contains(said.String(), line) {
			t.Errorf("the server does not say %q:\n%s", line, said)
		}
	}
	// A page is told of in the log as every request is.
	if !strings.Contains(said.String(), `"command":"page /app.js","user":"","status":200`) {
		t.Errorf("the log has no line for a page:\n%s", said)
	}
}

// Told nothing, the server hands out no page of a face: its address
// answers as it did before.
func TestAServerToldNoFolderHasNoFace(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", db); code != 0 {
		t.Fatalf("init ended with %d: %s", code, said)
	}
	addr, said, _ := serving(t, db, "--listen", "127.0.0.1:0")
	code, _, h := fetch(t, http.MethodGet, "http://"+addr+"/")
	if code != http.StatusNotFound || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Errorf("the address of a server with no face is answered %d as %q", code, h.Get("Content-Type"))
	}
	if strings.Contains(said.String(), "hands out the pages") {
		t.Errorf("a server with no face speaks of pages:\n%s", said)
	}
}

// A folder that cannot be handed out is found when the server starts,
// before the database is opened.
func TestAFolderOfPagesThatCannotBeHandedOut(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", db); code != 0 {
		t.Fatalf("init ended with %d: %s", code, said)
	}
	nowhere := filepath.Join(dir, "nowhere")
	if code, _, said := tool("serve", db, "--pages", nowhere); code != 2 || !strings.Contains(said, "fadodb serve: there is no folder of pages at "+nowhere) {
		t.Errorf("a folder that is not there ends with %d: %s", code, said)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, said := tool("serve", db, "--pages", empty); code != 2 || !strings.Contains(said, "has no index.html") {
		t.Errorf("a folder with no first page ends with %d: %s", code, said)
	}
}

// The chapter on serving shows the command, lists the kinds of file,
// shows the header of a page and what the server says when it starts.
func TestTheChapterOnThePagesOfAFace(t *testing.T) {
	if got := blockOfTheChapter(t, "the server with a face"); got != "fadodb serve acme.fado --pages ./workshop" {
		t.Errorf("the chapter starts the server with:\n%s", got)
	}
	var rows []string
	for _, k := range server.FolderKinds() {
		rows = append(rows, "| `"+k.Ending+"` | `"+k.Type+"` | "+k.What+" |")
	}
	table := blockAfter(t, "<!-- the kinds of file -->\n| A name that ends in | is sent as | It is |\n|---|---|---|\n", "\n\n")
	if table != strings.Join(rows, "\n") {
		t.Errorf("the chapter lists the kinds of file\n%s\n\nand the server hands out\n%s", table, strings.Join(rows, "\n"))
	}
	if got, want := blockOfTheChapter(t, "the header of a page"), "Content-Security-Policy: "+server.FolderPolicy; got != want {
		t.Errorf("the chapter shows the header\n%s\nand the server sends\n%s", got, want)
	}
	// The chapter names the server's own addresses as the code does.
	names := blockAfter(t, "**The server's own addresses always come first**: ", ". A file")
	for _, own := range server.FolderReserved() {
		if !strings.Contains(names, "`/"+own) {
			t.Errorf("the chapter does not name the address /%s among the server's own: %s", own, names)
		}
	}
	// What the server says of the pages, for the folder of the chapter.
	dir := t.TempDir()
	face := aFace(t, dir)
	folder, err := server.NewFolder(face, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer folder.Close()
	if got, want := blockOfTheChapter(t, "what it says of the pages"), "fadodb serve: "+handingOut(folder, "./workshop", "http://"+defaultListen)[0]; got != want {
		t.Errorf("the chapter shows a server that says\n%s\nand the server says\n%s", got, want)
	}
	// The help of the server names the flag.
	if !strings.Contains(serveUsage, "--pages DIR") {
		t.Error("the help of the server does not name --pages")
	}
}
