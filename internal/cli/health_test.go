package cli

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// "fadodb health" asks a server for its health (ADR-0059, item 9), with
// no key, and ends as the answer says: 0 for a server that can read its
// database, 1 for one that cannot, 3 when no server answered.
func TestHealth(t *testing.T) {
	t.Setenv("FADODB_URL", "")
	t.Setenv("FADODB_KEY", "")
	path := filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, said)
	}
	addr, log, _ := serving(t, path, "--listen", "127.0.0.1:0")

	// A server that answers. No key was made and none is sent.
	const healthy = `{"ok":true,"version":"dev"}` + "\n"
	if code, out, said := tool("health", "--url", "http://"+addr); code != exitDone || out != healthy || said != "" {
		t.Errorf("the health of a server: exit %d\n%q\n%s", code, out, said)
	}
	// The server is told by the environment as by the flag.
	t.Setenv("FADODB_URL", "http://"+addr)
	if code, out, _ := tool("health"); code != exitDone || out != healthy {
		t.Errorf("the health of the server of FADODB_URL: exit %d\n%q", code, out)
	}
	t.Setenv("FADODB_URL", "")
	// Asking leaves no line in the log of the server.
	if strings.Contains(log.String(), `"command"`) {
		t.Errorf("asking for the health wrote a line:\n%s", log)
	}

	// A server that answers that it cannot read its database.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/health" || r.Header.Get("Authorization") != "" {
			t.Errorf("the tool asked %s %s with the key %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"ok":false,"version":"dev"}`+"\n")
	}))
	defer broken.Close()
	if code, out, said := tool("health", "--url", broken.URL); code != exitRefused || out != `{"ok":false,"version":"dev"}`+"\n" ||
		!strings.Contains(said, "cannot read its database") {
		t.Errorf("a server that cannot read its database: exit %d\n%q\n%s", code, out, said)
	}

	// What answers and is no FadoDB server: another program at the
	// address, or one that says ok with a status that says otherwise.
	for name, answer := range map[string]http.HandlerFunc{
		"another program": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"words":           func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hello") },
		"ok and 503": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"ok":true}`)
		},
		"not ok and 200": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"ok":false}`) },
		"not ok and 500": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"ok":false}`)
		},
	} {
		other := httptest.NewServer(answer)
		if code, out, said := tool("health", "--url", other.URL); code != exitNoAnswer || out != "" || !strings.Contains(said, "is not a FadoDB server") {
			t.Errorf("%s: exit %d\n%q\n%s", name, code, out, said)
		}
		other.Close()
	}

	// Nothing answers: an address where nothing listens.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nobody := ln.Addr().String()
	ln.Close()
	if code, out, said := tool("health", "--url", "http://"+nobody); code != exitNoAnswer || out != "" || !strings.Contains(said, "no answer from http://"+nobody+"/v1/health") {
		t.Errorf("no server: exit %d\n%q\n%s", code, out, said)
	}

	// A line the tool cannot read, and its help.
	if code, _, said := tool("health", "acme.fado"); code != exitUsage || !strings.Contains(said, "flags only") {
		t.Errorf("an argument: exit %d\n%s", code, said)
	}
	if code, _, said := tool("health", "--url", "fado.acme.example"); code != exitUsage || !strings.Contains(said, "https://HOST") {
		t.Errorf("an address that is none: exit %d\n%s", code, said)
	}
	if code, _, said := tool("health", "--help"); code != 0 || said != healthUsage {
		t.Errorf("the help: exit %d\n%s", code, said)
	}

	// The chapter on serving shows the command, and says how it ends.
	chapter := blockAfter(t, "<!-- the tool asks -->\n", "\n## ")
	for _, want := range []string{"```sh\nfadodb health\n```", "with 0 when `ok` is true", "with 1 when", "with 3 when no"} {
		if !strings.Contains(chapter, want) {
			t.Errorf("the chapter on serving does not say of fadodb health: %s", want)
		}
	}
}
