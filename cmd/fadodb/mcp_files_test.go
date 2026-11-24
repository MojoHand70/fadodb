package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scan0045 are the bytes of the scan the chapter's section on files
// puts: a PDF as small as one can be, whose hash and size the chapter
// shows.
const scan0045 = "%PDF-1.7\n% the scan of invoice FV/2026/11/0045\n%%EOF\n"

// The chapter's section on files from a folder, played against a real
// server through "fadodb mcp --files": Bob's assistant puts the scan of
// an invoice from his folder and sets it on the invoice, and ap-agent
// writes it into its own (ADR-0064, ruling 4). Then every way out of a
// folder is tried, and refused.
func TestTheChapterOnFiles(t *testing.T) {
	chapter, terminal := doorChapter(t), terminalChapter(t)
	path := filepath.Join(t.TempDir(), "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--db", path)
	olga = strings.TrimSpace(olga)
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0")
	base := "http://" + addr
	web := &http.Client{Timeout: 10 * time.Second}
	send := func(key, name, object string) string {
		t.Helper()
		status, body := ask(t, web, "POST", base+"/v1/"+name, key, object)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", name, status, body)
		}
		return body
	}
	send(olga, "define_class", `{"definition": `+blockOf(t, terminal, "invoice.json")+`}`)
	keys := map[string]string{}
	for _, u := range []string{`{"id": "bob", "kind": "person", "groups": ["sales"]}`, `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`} {
		var made struct {
			User struct{ ID string } `json:"user"`
		}
		json.Unmarshal([]byte(send(olga, "create_user", u)), &made)
		var key struct {
			Key struct{ Secret string } `json:"key"`
		}
		json.Unmarshal([]byte(send(olga, "create_key", `{"user": "`+made.User.ID+`", "label": "chapter"}`)), &key)
		keys[made.User.ID] = key.Key.Secret
	}
	send(keys["bob"], "create", `{"class": "Invoice", "id": "FV/2026/11/0045", "values": {"supplier": "Hurtownia Kowalski", "total": "980.00"}}`)

	// Bob's program is told to start the door with his folder.
	var told struct {
		MCPServers map[string]struct{ Args []string } `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(blockOf(t, chapter, "files: bob's program is told")), &told); err != nil || len(told.MCPServers["acme"].Args) != 3 || told.MCPServers["acme"].Args[1] != "--files" {
		t.Fatalf("the chapter tells Bob's program %+v (%v)", told, err)
	}
	bob := t.TempDir()
	if err := os.WriteFile(filepath.Join(bob, "scan-0045.pdf"), []byte(scan0045), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FADODB_URL", base)
	t.Setenv("FADODB_KEY", keys["bob"])
	bobs := []string{"--files", bob}
	put := callTool(t, bobs, blockOf(t, chapter, "files: put"))
	if put.isError || put.refused != "" {
		t.Fatalf("put_file: %+v", put)
	}
	if got, want := asTheAgentReads(t, put.text), blockOf(t, chapter, "files: put, the answer"); got != want {
		t.Errorf("the agent reads\n%s\nand the chapter shows\n%s", got, want)
	}
	set := callTool(t, bobs, blockOf(t, chapter, "files: set"))
	var setOn struct {
		Document struct {
			Values struct {
				Scan struct{ Hash string }
			}
		}
	}
	if set.isError || json.Unmarshal([]byte(set.text), &setOn) != nil || !strings.HasPrefix(setOn.Document.Values.Scan.Hash, "09ac2f15") {
		t.Fatalf("set with the facts: %+v", set)
	}

	// ap-agent writes the scan into its own folder.
	agent := t.TempDir()
	t.Setenv("FADODB_KEY", keys["ap-agent"])
	agents := []string{"--files", agent}
	got := callTool(t, agents, blockOf(t, chapter, "files: get"))
	if got.isError || got.refused != "" {
		t.Fatalf("get_file: %+v", got)
	}
	if laid, want := asTheAgentReads(t, got.text), blockOf(t, chapter, "files: get, the answer"); laid != want {
		t.Errorf("the agent reads\n%s\nand the chapter shows\n%s", laid, want)
	}
	if written, err := os.ReadFile(filepath.Join(agent, "scan-0045.pdf")); err != nil || string(written) != scan0045 {
		t.Fatalf("the agent's folder holds %q (%v)", written, err)
	}

	// The folder, and nowhere else.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not for the agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(bob, "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	linked := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(bob, "link.txt")) == nil
	refusedAt := func(c called) bool {
		var r struct {
			Error struct{ Code, Path, Message string }
		}
		return c.isError && json.Unmarshal([]byte(c.text), &r) == nil && r.Error.Code == "bad_value" && r.Error.Path == "/path"
	}
	putOf := func(p string) string {
		text, _ := json.Marshal(map[string]any{"name": "put_file", "arguments": map[string]string{"path": p}})
		return string(text)
	}
	for _, c := range []struct {
		why, path, says string
		skip            bool
	}{
		{"out with ..", "../secret.txt", "not in the folder", false},
		{"a whole path elsewhere", filepath.Join(outside, "secret.txt"), "not in the folder", false},
		{"a link that points out", "link.txt", "cannot read", !linked},
		{"a file that is not there", "missing.pdf", "no such file", false},
		{"a folder", "2026", "is not a file", false},
	} {
		if c.skip {
			t.Logf("%s: links cannot be made here", c.why)
			continue
		}
		t.Setenv("FADODB_KEY", keys["bob"])
		if r := callTool(t, bobs, putOf(c.path)); !refusedAt(r) || !strings.Contains(r.text, c.says) {
			t.Errorf("put_file, %s: %+v", c.why, r)
		}
	}
	t.Setenv("FADODB_KEY", keys["bob"])
	if r := callTool(t, bobs, putOf(filepath.Join(bob, "scan-0045.pdf"))); r.isError || !strings.Contains(r.text, `"hash":"09ac2f15`) {
		t.Errorf("put_file, a whole path in the folder: %+v", r)
	}
	t.Setenv("FADODB_KEY", keys["ap-agent"])
	getOf := func(p, hash string) string {
		arguments := map[string]string{"class": "Invoice", "id": "FV/2026/11/0045", "hash": hash}
		if p != "" {
			arguments["path"] = p
		}
		text, _ := json.Marshal(map[string]any{"name": "get_file", "arguments": arguments})
		return string(text)
	}
	hash := "09ac2f156d98803f6a577eaef61f7d4c178d040c6a14967330564523bb95d3e5"
	if err := os.WriteFile(filepath.Join(agent, "kept.pdf"), []byte("the agent's own"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ why, path, says string }{
		{"over the file it wrote", "", "already a file"},
		{"over a file of the folder's", "kept.pdf", "already a file"},
		{"out with ..", "../scan.pdf", "not in the folder"},
		{"into a folder that is not there", "2027/scan.pdf", "cannot write"},
	} {
		if r := callTool(t, agents, getOf(c.path, hash)); !refusedAt(r) || !strings.Contains(r.text, c.says) {
			t.Errorf("get_file, %s: %+v", c.why, r)
		}
	}
	if kept, _ := os.ReadFile(filepath.Join(agent, "kept.pdf")); string(kept) != "the agent's own" {
		t.Errorf("a file of the folder was written over: %q", kept)
	}
	if _, err := os.Stat(filepath.Join(agent, "2027")); err == nil {
		t.Error("get_file made a folder")
	}
	if err := os.Mkdir(filepath.Join(agent, "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := callTool(t, agents, getOf("2026/scan.pdf", hash)); r.isError || !strings.Contains(r.text, `"path":"2026/scan.pdf"`) {
		t.Errorf("get_file into a folder of the folder: %+v", r)
	}
	// A file the document does not hold is refused by the server, as at
	// GET /v1/file, and nothing is left.
	other := strings.Repeat("0", 64)
	if r := callTool(t, agents, getOf("other.pdf", other)); !r.isError || !strings.Contains(r.text, `"unknown_blob"`) {
		t.Errorf("get_file of a hash the document does not hold: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(agent, "other.pdf")); err == nil {
		t.Error("a refused get_file left a file")
	}

	// A door given no folder offers no tool for files, and one given a
	// folder that is not there does not start.
	if r := callTool(t, nil, putOf("scan-0045.pdf")); !strings.Contains(r.refused, "--files FOLDER") {
		t.Errorf("put_file at a door with no folder: %+v", r)
	}
	if code, _, said := door([]string{"--files", filepath.Join(outside, "nowhere")}); code != exitUsage || !strings.Contains(said, "--files") {
		t.Errorf("a door with a folder that is not there ended %d and said %q", code, said)
	}
}
