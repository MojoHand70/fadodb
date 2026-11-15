package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file on a document, from a terminal: put, read back by its
// variable and by its hash, under the rules of the document.
func TestAFileFromATerminal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--db", path)
	addr, _, stop := serving(t, path, "--listen", "127.0.0.1:0")
	t.Setenv("FADODB_URL", "http://"+addr)
	t.Setenv("FADODB_KEY", strings.TrimSpace(olga))
	// sent sends a command that must be carried out.
	sent := func(args ...string) string {
		t.Helper()
		code, out, said := tool(args...)
		if code != exitDone {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
		return out
	}
	sent("exec", "define_class", `{"definition": {
  "name": "Receipt",
  "variables": {
    "what":  {"type": "text", "required": true},
    "scan":  {"type": "object", "schema": "File"},
    "pages": {"type": "rows", "schema": "File"}
  },
  "states": {"initial": "filed", "list": ["filed"]},
  "access": {"read": "INGROUP(\"sales\")", "create": "INGROUP(\"sales\")", "update": "INGROUP(\"sales\")", "delete": "FALSE"}
}}`)
	keys := map[string]string{}
	for user, groups := range map[string]string{"bob": `["sales"]`, "eve": `[]`} {
		sent("exec", "create_user", `{"id": "`+user+`", "kind": "person", "groups": `+groups+`}`)
		var made struct {
			Key struct{ Secret string }
		}
		if err := json.Unmarshal([]byte(sent("exec", "create_key", `{"user": "`+user+`"}`)), &made); err != nil {
			t.Fatal(err)
		}
		keys[user] = made.Key.Secret
	}
	t.Setenv("FADODB_KEY", keys["bob"])
	sent("exec", "create", `{"class": "Receipt", "id": "R/7", "values": {"what": "taxi"}}`)

	taxi, other := []byte("%PDF-1.4 a taxi receipt\n"), []byte("a second scan, as plain text\n")
	scan, second := filepath.Join(dir, "taxi.pdf"), filepath.Join(dir, "second.txt")
	for file, content := range map[string][]byte{scan: taxi, second: other} {
		if err := os.WriteFile(file, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Put on a document: the bytes go to the server and the file into
	// the variable, and the tool prints the document after the change.
	var after struct {
		Document struct {
			Version int
			Values  struct {
				Scan struct {
					Hash, Name string
					MediaType  string `json:"media_type"`
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(sent("file", "put", scan, "--class", "Receipt", "--id", "R/7", "--variable", "scan")), &after); err != nil {
		t.Fatal(err)
	}
	first := after.Document.Values.Scan
	if after.Document.Version != 2 || len(first.Hash) != 64 || first.Name != "taxi.pdf" || first.MediaType != "application/pdf" {
		t.Errorf("after the put the document is %+v", after.Document)
	}

	// Read back by the variable: to a file, and to standard output.
	back := filepath.Join(dir, "back.pdf")
	if out := sent("file", "get", "--class", "Receipt", "--id", "R/7", "--variable", "scan", "--out", back); out != "" {
		t.Errorf("a file written with --out is also printed: %q", out)
	}
	if read, _ := os.ReadFile(back); !bytes.Equal(read, taxi) {
		t.Errorf("the file read back is %q", read)
	}
	if out := sent("file", "get", "--class", "Receipt", "--id", "R/7", "--variable", "scan"); out != string(taxi) {
		t.Errorf("the file on standard output is %q", out)
	}

	// A second file takes its place; the first is read by its hash, from
	// the version that held it.
	sent("file", "put", second, "--class", "Receipt", "--id", "R/7", "--variable", "scan", "--name", "drugi skan.txt", "--type", "text/plain")
	if out := sent("file", "get", "--class", "Receipt", "--id", "R/7", "--variable", "scan"); out != string(other) {
		t.Errorf("after the second put the variable holds %q", out)
	}
	if out := sent("file", "get", "--class", "Receipt", "--id", "R/7", "--hash", first.Hash, "--version", "2"); out != string(taxi) {
		t.Errorf("the file of version 2, by its hash, is %q", out)
	}

	// Put with no document: the bytes go to the server, and the tool
	// prints the facts of the file, the value to give a variable.
	var put struct {
		File struct {
			Hash, Name string
			MediaType  string `json:"media_type"`
		}
	}
	if err := json.Unmarshal([]byte(sent("file", "put", second, "--name", "drugi skan.txt", "--type", "text/plain")), &put); err != nil {
		t.Fatal(err)
	}
	if len(put.File.Hash) != 64 || put.File.Name != "drugi skan.txt" || put.File.MediaType != "text/plain" {
		t.Errorf("a put with no document answers %+v", put.File)
	}
	sent("exec", "create", `{"class": "Receipt", "id": "R/8", "values": {"what": "train", "pages": [{"hash": "`+put.File.Hash+`", "name": "drugi skan.txt", "media_type": "text/plain", "size": `+"29"+`}]}}`)
	// A file of a list is read by its hash.
	if out := sent("file", "get", "--class", "Receipt", "--id", "R/8", "--hash", put.File.Hash); out != string(other) {
		t.Errorf("a file of a list is %q", out)
	}

	// A variable that holds no file: nothing is written.
	none := filepath.Join(dir, "none.pdf")
	if code, _, said := tool("file", "get", "--class", "Receipt", "--id", "R/8", "--variable", "scan", "--out", none); code != exitUsage || !strings.Contains(said, "the variable scan of Receipt R/8 holds no file") {
		t.Errorf("a variable with no file: exit %d, said %q", code, said)
	}
	// The rules of the document hold: Eve, who is not in sales, reads no
	// file of it and puts none on it.
	t.Setenv("FADODB_KEY", keys["eve"])
	for _, args := range [][]string{
		{"file", "get", "--class", "Receipt", "--id", "R/7", "--variable", "scan", "--out", none},
		{"file", "get", "--class", "Receipt", "--id", "R/7", "--hash", first.Hash, "--out", none},
		{"file", "put", scan, "--class", "Receipt", "--id", "R/7", "--variable", "scan"},
	} {
		if code, out, said := tool(args...); code != exitRefused || out != "" || !strings.Contains(said, `"error"`) {
			t.Errorf("%v as Eve: exit %d, printed %q, said %q", args[:2], code, out, said)
		}
	}
	if _, err := os.Stat(none); err == nil {
		t.Error("a file that was refused was written")
	}

	// A line that cannot be read sends nothing.
	t.Setenv("FADODB_KEY", keys["bob"])
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{}, "say put or get"},
		{[]string{"list"}, "say put or get"},
		{[]string{"put"}, "put takes one file"},
		{[]string{"put", scan, second}, "put takes one file"},
		{[]string{"put", scan, "--class", "Receipt"}, "all three"},
		{[]string{"put", scan, "--out", none}, "are for get"},
		{[]string{"put", filepath.Join(dir, "missing.pdf")}, "missing.pdf"},
		{[]string{"put", dir}, "is not a file"},
		{[]string{"get", scan, "--class", "Receipt", "--id", "R/7", "--variable", "scan"}, "get takes no file"},
		{[]string{"get", "--variable", "scan"}, "with --class and --id"},
		{[]string{"get", "--class", "Receipt", "--id", "R/7"}, "with --variable or with --hash"},
		{[]string{"get", "--class", "Receipt", "--id", "R/7", "--variable", "scan", "--hash", first.Hash}, "with --variable or with --hash"},
		{[]string{"get", "--class", "Receipt", "--id", "R/7", "--variable", "scan", "--version", "2"}, "--version goes with --hash"},
		{[]string{"get", "--class", "Receipt", "--id", "R/7", "--variable", "scan", "--name", "x"}, "are for put"},
	} {
		code, out, said := tool(append([]string{"file"}, c.args...)...)
		if code != exitUsage || out != "" || !strings.Contains(said, c.says) {
			t.Errorf("%v: exit %d, printed %q, said %q; want exit %d and %q", c.args, code, out, said, exitUsage, c.says)
		}
	}
	if code, _, said := tool("file", "--help"); code != 0 || !strings.Contains(said, "fadodb file put FILE") {
		t.Errorf("--help: exit %d, said %q", code, said)
	}
	if _, out, _ := tool("help"); !strings.Contains(out, "fadodb file put FILE") || !strings.Contains(out, "fadodb file get") {
		t.Error("the help of the tool does not name the file words")
	}

	// With no server, no answer comes.
	stop()
	if code, _, _ := tool("file", "get", "--class", "Receipt", "--id", "R/7", "--variable", "scan"); code != exitNoAnswer {
		t.Errorf("with no server the tool ends with %d", code)
	}
}
