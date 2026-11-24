package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// folderOf is a folder as a door sees one: what it answers, and the
// calls it was given.
type folderOf struct {
	mu      sync.Mutex
	puts    []PutFile
	gets    []GetFile
	answer  string
	refused bool
	err     error
}

func (f *folderOf) Folder() string { return "/home/bob/invoices" }

func (f *folderOf) Put(ctx context.Context, p PutFile) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, p)
	return []byte(f.answer), f.refused, f.err
}

func (f *folderOf) Get(ctx context.Context, g GetFile) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, g)
	return []byte(f.answer), f.refused, f.err
}

// callOf is the line of a call of a tool, in the modern form.
func callOf(id, name, arguments string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `,` + modern + `}}`
}

// listedOf are the tools a door lists, by name.
func listedOf(t *testing.T, d *Door) map[string]tool {
	t.Helper()
	var listed []tool
	if err := json.Unmarshal(d.listed, &listed); err != nil {
		t.Fatal(err)
	}
	tools := map[string]tool{}
	for _, tl := range listed {
		tools[tl.Name] = tl
	}
	return tools
}

// Files come only through a folder that whoever starts the door names:
// a door given none offers no tool for files, and says how one is given;
// the door at the server is given none (ADR-0064, ruling 4).
func TestFilesOnlyThroughAFolder(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		says string
	}{
		{"a door with no folder", Options{}, "This door was started with no folder for files: whoever starts it names one, fadodb mcp --files FOLDER"},
		{"the door at the server", Options{AtServer: true}, "at the server there is no folder of the caller's, and the bytes of a file do not pass through a conversation"},
	} {
		d := door(t, acme(), c.o)
		for _, name := range []string{ToolPutFile, ToolGetFile} {
			if d.tools[name] {
				t.Errorf("%s offers %s", c.name, name)
			}
		}
		if strings.Contains(d.says, "put_file") || strings.Contains(d.says, "get_file") {
			t.Errorf("%s speaks of files: %s", c.name, d.says)
		}
		a := talk(t, d, callOf("1", ToolPutFile, `{"path":"scan-0045.pdf"}`))
		if _, message, _ := a.failure(t, "1"); !strings.Contains(message, c.says) {
			t.Errorf("%s: put_file is answered %q", c.name, message)
		}
	}
	if _, err := New(acme().send, Options{AtServer: true, Files: &folderOf{}}); err == nil {
		t.Error("the door at the server took a folder")
	}
	if _, err := New(acme().send, Options{Only: []string{"get", ToolGetFile}}); err == nil || !strings.Contains(err.Error(), "--files FOLDER") {
		t.Errorf("a door with no folder, told to offer get_file: %v", err)
	}
}

// With a folder the door offers the two tools, described as commands
// are, and says where its files are.
func TestTheToolsForFiles(t *testing.T) {
	d := door(t, acme(), Options{Files: &folderOf{}})
	tools := listedOf(t, d)
	for name, want := range map[string]struct {
		title    string
		required []string
	}{
		ToolPutFile: {"Put file", []string{"path"}},
		ToolGetFile: {"Get file", []string{"class", "id", "hash"}},
	} {
		tl, offered := tools[name]
		if !offered {
			t.Errorf("a door with a folder does not offer %s", name)
			continue
		}
		c, _ := fileTool(name)
		if tl.Title != want.title || !strings.HasPrefix(tl.Description, c.Does) || !strings.HasSuffix(tl.Description, ".") {
			t.Errorf("%s: title %q, description %q", name, tl.Title, tl.Description)
		}
		if tl.Annotations != (hints{}) {
			t.Errorf("%s: marks %+v; a tool for files changes the folder or the database, and destroys nothing", name, tl.Annotations)
		}
		required, _ := json.Marshal(tl.InputSchema["required"])
		if wanted, _ := json.Marshal(want.required); string(required) != string(wanted) || tl.InputSchema["additionalProperties"] != false {
			t.Errorf("%s: the schema of its arguments is %v", name, tl.InputSchema)
		}
	}
	for _, said := range []string{"Files go through the folder /home/bob/invoices", "put_file puts", "get_file writes", "never over another file"} {
		if !strings.Contains(d.says, said) {
			t.Errorf("the door does not say %q: %s", said, d.says)
		}
	}
	// A door that offers only the tools that read offers get_file, which
	// reads the database, and not put_file.
	read := door(t, acme(), Options{ReadOnly: true, Files: &folderOf{}})
	if !read.tools[ToolGetFile] || read.tools[ToolPutFile] {
		t.Errorf("a door that only reads offers %v", read.Tools())
	}
	a := talk(t, read, callOf("1", ToolPutFile, `{"path":"scan-0045.pdf"}`))
	if _, message, _ := a.failure(t, "1"); !strings.Contains(message, "only the tools that read") {
		t.Errorf("put_file at a door that only reads: %q", message)
	}
	if _, err := New(acme().send, Options{ReadOnly: true, Only: []string{ToolPutFile}, Files: &folderOf{}}); err == nil {
		t.Error("a door that only reads took put_file in its list")
	}
	// A list names the tools for files as it names commands.
	narrow := door(t, acme(), Options{Only: []string{"get", ToolGetFile}, Files: &folderOf{}})
	if got := strings.Join(narrow.Tools(), ","); got != "get,get_file" {
		t.Errorf("a door told to offer get and get_file offers %s", got)
	}
}

// The arguments of a tool for files are read as strictly as the object
// of a command, and what is taken is handed to the folder.
func TestTheArgumentsOfAToolForFiles(t *testing.T) {
	for _, c := range []struct {
		tool, arguments, path string
	}{
		{ToolPutFile, `{}`, "/path"},
		{ToolPutFile, `{"path":""}`, "/path"},
		{ToolPutFile, `{"path":"scan.pdf","folder":"/etc"}`, "/folder"},
		{ToolPutFile, `{"path":"scan.pdf","path":"other.pdf"}`, "/path"},
		{ToolPutFile, `{"path":42}`, "/path"},
		{ToolGetFile, `{"class":"Invoice","id":"FV/2026/11/0042"}`, "/hash"},
		{ToolGetFile, `{"class":"Invoice","id":"FV/2026/11/0042","hash":"d694","version":0}`, "/version"},
		{ToolGetFile, `{"class":"Invoice","id":"FV/2026/11/0042","hash":"d694","version":"2"}`, "/version"},
		{ToolGetFile, `{"class":"Invoice","id":"FV/2026/11/0042","hash":"d694","version":1.5}`, "/version"},
	} {
		f := &folderOf{answer: `{}`}
		a := talk(t, door(t, acme(), Options{Files: f}), callOf("1", c.tool, c.arguments))
		text, failed := a.text(t, "1")
		var refusal struct {
			Error struct{ Code, Path string }
		}
		if json.Unmarshal([]byte(text), &refusal) != nil || !failed || refusal.Error.Code != "bad_json" || refusal.Error.Path != c.path {
			t.Errorf("%s %s: answered %s, error %v; want bad_json at %s", c.tool, c.arguments, text, failed, c.path)
		}
		if len(f.puts)+len(f.gets) != 0 {
			t.Errorf("%s %s reached the folder", c.tool, c.arguments)
		}
	}
	f := &folderOf{answer: `{"file":{"hash":"d694"}}`}
	d := door(t, acme(), Options{Files: f})
	a := talk(t, d,
		callOf("1", ToolPutFile, `{"path":"2026/scan-0045.pdf","name":"FV-0042.pdf","media_type":"application/pdf"}`),
		callOf("2", ToolGetFile, `{"class":"Invoice","id":"FV/2026/11/0042","hash":"d694","version":3,"path":"in/FV-0042.pdf","for":"ann","reason":null}`))
	for _, id := range []string{"1", "2"} {
		if text, failed := a.text(t, id); failed || text != `{"file":{"hash":"d694"}}` {
			t.Errorf("call %s answered %s, error %v", id, text, failed)
		}
	}
	if len(f.puts) != 1 || f.puts[0] != (PutFile{Path: "2026/scan-0045.pdf", Name: "FV-0042.pdf", MediaType: "application/pdf"}) {
		t.Errorf("the folder was given the puts %+v", f.puts)
	}
	if len(f.gets) != 1 || f.gets[0] != (GetFile{Class: "Invoice", ID: "FV/2026/11/0042", Hash: "d694", Version: 3, For: "ann", Path: "in/FV-0042.pdf"}) {
		t.Errorf("the folder was given the gets %+v", f.gets)
	}
}

// What the folder refuses is the tool's answer, marked as an error; when
// no answer came through, the tool says what is safe to do.
func TestAToolForFilesAnswers(t *testing.T) {
	refusing := &folderOf{answer: `{"error":{"code":"bad_value","message":"there is already a file at \"scan.pdf\"","path":"/path"}}`, refused: true}
	a := talk(t, door(t, acme(), Options{Files: refusing}), callOf("1", ToolGetFile, `{"class":"Invoice","id":"FV/2026/11/0042","hash":"d694","path":"scan.pdf"}`))
	if text, failed := a.text(t, "1"); !failed || !strings.Contains(text, "already a file") {
		t.Errorf("a refusal of the folder: %s, error %v", text, failed)
	}
	for name, hint := range map[string]string{
		ToolPutFile: "the same bytes are kept once",
		ToolGetFile: "no file was left in the folder",
	} {
		gone := &folderOf{err: errors.New("the server at https://fado.acme.example was not reached: connection refused")}
		arguments := `{"path":"scan.pdf"}`
		if name == ToolGetFile {
			arguments = `{"class":"Invoice","id":"FV/2026/11/0042","hash":"d694"}`
		}
		a := talk(t, door(t, acme(), Options{Files: gone}), callOf("1", name, arguments))
		text, failed := a.text(t, "1")
		if !failed || !strings.Contains(text, `"no_answer"`) || !strings.Contains(text, hint) {
			t.Errorf("%s with no answer: %s, error %v", name, text, failed)
		}
	}
}
