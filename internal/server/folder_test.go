package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aFolder makes the folder of a small face, and beside it a file that
// is not in it.
func aFolder(t *testing.T) (dir string) {
	t.Helper()
	top := t.TempDir()
	dir = filepath.Join(top, "face")
	files := map[string]string{
		"index.html":        "<!doctype html><title>The face</title>",
		"app.js":            "export const here = true;\n",
		"style.css":         "body { margin: 0 }\n",
		"data.json":         `{"ok": true}`,
		"pic.png":           "\x89PNG\r\n\x1a\n",
		"NOTICE.TXT":        "A notice.\n",
		"notes.md":          "# Not handed out\n",
		"LICENSE":           "No ending, not handed out.\n",
		".env":              "SECRET=1\n",
		".git/config":       "[core]\n",
		"sub/index.html":    "<!doctype html><title>Inside</title>",
		"sub/page.html":     "<!doctype html><title>A page inside</title>",
		"empty/readme.txt":  "A folder with no first page.\n",
		"v1/health":         "not the server's health",
		"auth/signin.html":  "not the server's sign-in",
		"../outside.txt":    "outside the folder\n",
		"sub/.hidden.html":  "<!doctype html>",
		"deep/a/b/far.html": "<!doctype html><title>Far</title>",
	}
	for name, body := range files {
		at := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// openedFolder opens a folder for a test and lets go of it when the test ends,
// so that the folder can be thrown away on every system.
func openedFolder(t *testing.T, dir string, report func(Request)) *Folder {
	t.Helper()
	f, err := NewFolder(dir, report)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// askFolder asks the folder for a path, taken as written.
func askFolder(f *Folder, method, path string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/", nil)
	r.URL.Path = path
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

func TestAFolderHandsOutItsFiles(t *testing.T) {
	f := openedFolder(t, aFolder(t), nil)
	for _, c := range []struct{ path, kind, holds string }{
		{"/", "text/html; charset=utf-8", "<title>The face</title>"},
		{"/index.html", "text/html; charset=utf-8", "<title>The face</title>"},
		{"/app.js", "text/javascript; charset=utf-8", "export const here"},
		{"/style.css", "text/css; charset=utf-8", "margin: 0"},
		{"/data.json", "application/json; charset=utf-8", `"ok": true`},
		{"/pic.png", "image/png", "PNG"},
		{"/NOTICE.TXT", "text/plain; charset=utf-8", "A notice."},
		{"/sub/", "text/html; charset=utf-8", "<title>Inside</title>"},
		{"/sub/page.html", "text/html; charset=utf-8", "A page inside"},
		{"/deep/a/b/far.html", "text/html; charset=utf-8", "<title>Far</title>"},
	} {
		w := askFolder(f, http.MethodGet, c.path)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s is answered %d", c.path, w.Code)
			continue
		}
		if got := w.Header().Get("Content-Type"); got != c.kind {
			t.Errorf("GET %s is sent as %q, not %q", c.path, got, c.kind)
		}
		if !strings.Contains(w.Body.String(), c.holds) {
			t.Errorf("GET %s does not hold %q:\n%s", c.path, c.holds, w.Body.String())
		}
	}
}

// Every answer, a refusal too, carries the header that says what a page
// may load and call, and it is the same for every folder.
func TestAFolderSendsOneHeader(t *testing.T) {
	f := openedFolder(t, aFolder(t), nil)
	for _, path := range []string{"/", "/app.js", "/pic.png", "/nothing.html", "/.env"} {
		h := askFolder(f, http.MethodGet, path).Header()
		if got := h.Get("Content-Security-Policy"); got != FolderPolicy {
			t.Errorf("GET %s carries the policy %q", path, got)
		}
		for name, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "same-origin",
			"Cache-Control":          "no-cache",
		} {
			if got := h.Get(name); got != want {
				t.Errorf("GET %s carries %s: %q, not %q", path, name, got, want)
			}
		}
	}
	// What the policy lets a page do, part by part.
	for _, part := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'self'"} {
		if !strings.Contains(FolderPolicy, part) {
			t.Errorf("the policy does not say %s", part)
		}
	}
	if script := partBetween(FolderPolicy, "script-src", ";"); strings.Contains(script, "unsafe") || strings.Contains(script, "http") {
		t.Errorf("the policy lets a script come from elsewhere or be written in a page: %s", script)
	}
}

// partBetween is the part of s after from and before to.
func partBetween(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	part, _, _ := strings.Cut(rest, to)
	return part
}

func TestAFolderHandsOutNothingElse(t *testing.T) {
	dir := aFolder(t)
	// A link in the folder that points outside it.
	linked := os.Symlink(filepath.Join(filepath.Dir(dir), "outside.txt"), filepath.Join(dir, "link.txt")) == nil
	f := openedFolder(t, dir, nil)
	paths := []string{
		"/nothing.html",     // not there
		"/notes.md",         // a kind that is not handed out
		"/LICENSE",          // no ending
		"/.env",             // begins with a dot
		"/.git/config",      // inside a folder that begins with a dot
		"/sub/.hidden.html", // a dot further down
		"/empty/",           // a folder with no first page
		"/empty",            // the same, without the slash
		"/../outside.txt",   // up and out
		"/sub/../../outside.txt",
		"/sub/../index.html", // up, even to a file that is in the folder
		"/sub//page.html",    // an empty part
		"//index.html",
		`/sub\page.html`,      // the other slash
		"/index.html\x00.png", // a zero
		"index.html",          // no slash at the start
		"",
	}
	if linked {
		paths = append(paths, "/link.txt")
	}
	for _, path := range paths {
		w := askFolder(f, http.MethodGet, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %q is answered %d:\n%s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "outside") || strings.Contains(w.Body.String(), "SECRET") {
			t.Errorf("GET %q hands out what is not the folder's:\n%s", path, w.Body.String())
		}
	}
}

// A folder inside the folder is asked for with a slash at its end, so
// that what its page links to is found beside it.
func TestAFolderInsideIsSentOn(t *testing.T) {
	f := openedFolder(t, aFolder(t), nil)
	w := askFolder(f, http.MethodGet, "/sub")
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/sub/" {
		t.Errorf("GET /sub is answered %d and sent to %q", w.Code, w.Header().Get("Location"))
	}
}

func TestAFolderIsOnlyRead(t *testing.T) {
	f := openedFolder(t, aFolder(t), nil)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		w := askFolder(f, method, "/index.html")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s is answered %d, and allows %q", method, w.Code, w.Header().Get("Allow"))
		}
	}
	w := askFolder(f, http.MethodHead, "/")
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD / is answered %d with %d bytes and the length %q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}
}

// A browser that holds a file asks whether it is still the file, and is
// told so without the file. A file is always sent whole.
func TestAFolderAnswersWhatABrowserHolds(t *testing.T) {
	f := openedFolder(t, aFolder(t), nil)
	first := askFolder(f, http.MethodGet, "/app.js")
	changed := first.Header().Get("Last-Modified")
	if changed == "" {
		t.Fatal("a file is sent without the moment it last changed")
	}
	if w := askFolder(f, http.MethodGet, "/app.js", "If-Modified-Since", changed); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("a file the browser holds is answered %d with %d bytes", w.Code, w.Body.Len())
	}
	if w := askFolder(f, http.MethodGet, "/app.js", "If-Modified-Since", "Mon, 02 Jan 2006 15:04:05 GMT"); w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Errorf("a file that changed since the browser's copy is answered %d with %d bytes", w.Code, w.Body.Len())
	}
	if w := askFolder(f, http.MethodGet, "/app.js", "Range", "bytes=0-5"); w.Code != http.StatusOK || w.Body.String() != "export const here = true;\n" {
		t.Errorf("a file asked for in part is answered %d: %q", w.Code, w.Body.String())
	}
}

func TestAFolderTellsOfEachRequest(t *testing.T) {
	var told []Request
	f := openedFolder(t, aFolder(t), func(r Request) { told = append(told, r) })
	askFolder(f, http.MethodGet, "/app.js")
	askFolder(f, http.MethodGet, "/nothing.html")
	askFolder(f, http.MethodPost, "/")
	want := []struct {
		command string
		status  int
	}{{"page /app.js", 200}, {"page /nothing.html", 404}, {"page /", 405}}
	if len(told) != len(want) {
		t.Fatalf("%d requests were told of, not %d", len(told), len(want))
	}
	for i, w := range want {
		if told[i].Command != w.command || told[i].Status != w.status || told[i].At.IsZero() {
			t.Errorf("request %d was told as %q with %d", i, told[i].Command, told[i].Status)
		}
	}
}

func TestAFolderThatCannotBeHandedOut(t *testing.T) {
	top := t.TempDir()
	if _, err := NewFolder(filepath.Join(top, "nowhere"), nil); err == nil || !strings.Contains(err.Error(), "there is no folder of pages at") {
		t.Errorf("a folder that is not there: %v", err)
	}
	file := filepath.Join(top, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFolder(file, nil); err == nil || !strings.Contains(err.Error(), "there is no folder of pages at") {
		t.Errorf("a file where a folder is asked for: %v", err)
	}
	empty := filepath.Join(top, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFolder(empty, nil); err == nil || !strings.Contains(err.Error(), "has no index.html") {
		t.Errorf("a folder with no first page: %v", err)
	}
}

// The names of the server's own addresses, when a folder holds them, are
// named so that the operator is told they are never handed out.
func TestAFolderNamesWhatTheServerKeeps(t *testing.T) {
	f := openedFolder(t, aFolder(t), nil)
	if got := strings.Join(f.Shadowed(), " "); got != "v1 auth" {
		t.Errorf("the folder holds v1 and auth, and names %q", got)
	}
}

// Every kind of file has an ending in small letters and a type, and none
// is named twice.
func TestTheKindsOfFile(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range FolderKinds() {
		if !strings.HasPrefix(k.Ending, ".") || k.Ending != strings.ToLower(k.Ending) || k.Type == "" || k.What == "" {
			t.Errorf("the kind %+v is not well said", k)
		}
		if seen[k.Ending] {
			t.Errorf("the ending %s is named twice", k.Ending)
		}
		seen[k.Ending] = true
		if sent, handed := folderKind("a-file" + strings.ToUpper(k.Ending)); !handed || sent != k.Type {
			t.Errorf("a file that ends in %s is sent as %q", k.Ending, sent)
		}
	}
	// And no ending is handed out that the list does not name.
	for _, other := range []string{".md", ".map", ".wasm", ".pdf", ".zip", ".exe", ".php", ".htm", ""} {
		if _, handed := folderKind("a-file" + other); handed {
			t.Errorf("a file that ends in %q is handed out, and the list does not name it", other)
		}
	}
}
