package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/MojoHand70/fadodb/internal/command"
)

// Files through the door (ADR-0064, ruling 4 and items 31 to 38). The
// bytes of a file do not pass through an agent's conversation: the door
// reads them from a folder on the disk of whoever starts it, and writes
// them into it, and nowhere else. There is a folder only when whoever
// starts the door names one, so a door that is given none offers no
// tool for files, and the door at the server itself, where there is no
// disk of the caller's, offers none.
//
// Two tools, each one transfer, as the server has two addresses for the
// bytes (ADR-0055, item 9). put_file answers the facts of the file, and
// the document is written with create or set, so that the rules, the
// version and the answer are the command's. get_file opens the file
// through the document that holds it, by its hash, as GET /v1/file.

// The names of the two tools.
const (
	ToolPutFile = "put_file"
	ToolGetFile = "get_file"
)

// Files carries the bytes of files between the folder a door was given
// and the database. Each method answers as Send does: what the database
// or the door answered, or, with refused set, a refusal; an error says
// that no answer came through.
type Files interface {
	// Folder names the folder, as the door tells an agent where its
	// files are.
	Folder() string
	// Put reads a file from the folder and puts its bytes in the
	// database, and answers the facts of the file.
	Put(ctx context.Context, f PutFile) (answer []byte, refused bool, err error)
	// Get writes a file a document holds into the folder, and answers
	// where and the facts of the file.
	Get(ctx context.Context, f GetFile) (answer []byte, refused bool, err error)
}

// PutFile is a call of put_file.
type PutFile struct {
	// Path is the file, by its path in the folder.
	Path string
	// Name is the name the file is kept under; the last part of Path
	// when empty.
	Name string
	// MediaType is the kind of file; the database reads it from the
	// first bytes when empty.
	MediaType string
}

// GetFile is a call of get_file.
type GetFile struct {
	Class, ID, Hash string
	// Version is the version of the document that held the file; 0 for
	// the version it is in now.
	Version int64
	// For and Reason are as in a command.
	For, Reason string
	// Path is where in the folder the file is written; the name of the
	// file when empty.
	Path string
}

// The two tools, described as a command of the table is, so that an
// agent reads them as it reads the others.
var (
	putFile = &command.Command{Name: ToolPutFile, Mark: command.Writes,
		Does: "Reads a file from the folder this door was given and puts its bytes in the database, as the user whose key the door was given; nothing outside the folder is read. The bytes do nothing until a document names them: give the facts it answers as the value of a variable of the schema File, in create or in set.",
		Fields: []command.Field{
			{Name: "path", Kind: command.Text, Required: true, Does: "the file, by its path in the folder, as scan-0045.pdf or 2026/scan-0045.pdf; a whole path that lies in the folder is taken too"},
			{Name: "name", Kind: command.Text, Does: "the name the file is kept under; the last part of path when left out"},
			{Name: "media_type", Kind: command.Text, Does: "the kind of file, such as application/pdf; the database reads it from the first bytes when left out"},
		},
		Answer: []command.Member{{Name: "file", Does: "the facts of the file: its hash, its name, its kind and its size, which is the value to give the variable; the same bytes put twice are kept once"}},
	}
	getFile = &command.Command{Name: ToolGetFile, Mark: command.Reads,
		Does: "Writes a file a document holds into the folder this door was given, under the rules of the document: whoever may read the variable that holds the file may read the file. It never writes over a file already in the folder, and it leaves the file there only when its bytes have the hash asked for.",
		Fields: []command.Field{
			{Name: "class", Kind: command.Text, Required: true, Does: "the class of the document"},
			{Name: "id", Kind: command.Text, Required: true, Does: "the id of the document"},
			{Name: "hash", Kind: command.Text, Required: true, Does: "the hash of the file, as the document holds it in its variable: get answers it"},
			{Name: "version", Kind: command.Integer, Does: "the version of the document that held the file, for a file it holds no more; the version it is in now when left out"},
			{Name: "path", Kind: command.Text, Does: "where in the folder the file is written, as scan-0045.pdf or 2026/scan-0045.pdf, in a folder that is there; the name of the file when left out"},
			option("for"),
			option("reason"),
		},
		Answer: []command.Member{
			{Name: "path", Does: "where in the folder the file was written"},
			{Name: "file", Does: "its facts: its hash, its name, its kind and its size"},
		},
	}
)

// option is a member that means the same in every command that takes it,
// in the words of the table.
func option(name string) command.Field {
	get, _ := command.Lookup("get")
	for _, f := range get.Fields {
		if f.Name == name {
			return f
		}
	}
	panic("mcp: the command get takes no " + name)
}

// fileTool says a name is one of the tools for files, and answers it.
func fileTool(name string) (*command.Command, bool) {
	switch name {
	case ToolPutFile:
		return putFile, true
	case ToolGetFile:
		return getFile, true
	}
	return nil, false
}

// Refusal is a refusal in the form the database writes one, for what the
// door itself refuses: a code, a message, the place in the arguments and
// a hint.
func Refusal(code, path, message, hint string) []byte {
	e := map[string]string{"code": code, "message": message}
	if path != "" {
		e["path"] = path
	}
	if hint != "" {
		e["hint"] = hint
	}
	text, _ := command.Marshal(map[string]any{"error": e})
	return text
}

// callFile runs a tool for files: it reads its arguments as strictly as
// a command's object is read, and carries the bytes.
func (d *Door) callFile(ctx context.Context, c *command.Command, object []byte) (string, bool) {
	given, refusal := takeArguments(c, object)
	if refusal != nil {
		return string(refusal), true
	}
	var answer []byte
	var refused bool
	var err error
	switch c.Name {
	case ToolPutFile:
		answer, refused, err = d.files.Put(ctx, PutFile{Path: given.text("path"), Name: given.text("name"), MediaType: given.text("media_type")})
	case ToolGetFile:
		answer, refused, err = d.files.Get(ctx, GetFile{Class: given.text("class"), ID: given.text("id"), Hash: given.text("hash"),
			Version: given.version, For: given.text("for"), Reason: given.text("reason"), Path: given.text("path")})
	}
	if err != nil {
		return noAnswer(c.Name, err), true
	}
	return string(bytes.TrimSpace(answer)), refused
}

// given are the arguments of a tool for files, as it took them.
type given struct {
	texts   map[string]string
	version int64
}

func (g given) text(name string) string { return g.texts[name] }

// takeArguments reads the arguments of a tool for files: a member the
// tool does not take, a member given twice, a member of the wrong JSON
// type and a missing member it needs are each refused with bad_json and
// the place, as in a command (ADR-0055, item 5).
func takeArguments(c *command.Command, object []byte) (given, []byte) {
	g := given{texts: map[string]string{}}
	fields := map[string]command.Field{}
	for _, f := range c.Fields {
		fields[f.Name] = f
	}
	bad := func(path, message, hint string) (given, []byte) {
		return given{}, Refusal(command.CodeBadJSON, path, message, hint)
	}
	dec := json.NewDecoder(bytes.NewReader(object))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return bad("", "the arguments of "+c.Name+" are one JSON object", "")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return bad("", "the arguments of "+c.Name+" are not JSON: "+err.Error(), "")
		}
		name, _ := t.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return bad("/"+name, "the value of "+name+" is not JSON: "+err.Error(), "")
		}
		f, known := fields[name]
		switch {
		case !known:
			return bad("/"+name, c.Name+" takes no member "+strconv.Quote(name), "it takes "+memberNames(c))
		case seen[name]:
			return bad("/"+name, name+" is given twice", "give each member once")
		}
		seen[name] = true
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		switch f.Kind {
		case command.Integer:
			var v int64
			if json.Unmarshal(value, &v) != nil {
				return bad("/"+name, name+" is a whole number, and "+string(bytes.TrimSpace(value))+" is not one", "")
			}
			g.version = v
		default:
			var s string
			if json.Unmarshal(value, &s) != nil {
				return bad("/"+name, name+" is text", "")
			}
			g.texts[name] = s
		}
	}
	if _, err := dec.Token(); err != nil {
		return bad("", "the arguments of "+c.Name+" are not JSON: "+err.Error(), "")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return bad("", "the arguments of "+c.Name+" are one JSON object, and something follows it", "")
	}
	for _, f := range c.Fields {
		if f.Required && g.texts[f.Name] == "" {
			return bad("/"+f.Name, c.Name+" needs "+f.Name+": "+f.Does, "")
		}
	}
	if seen["version"] && g.version < 1 {
		return bad("/version", "version is "+strconv.FormatInt(g.version, 10)+", and the version of a document is a whole number from 1", "")
	}
	return g, nil
}

// memberNames lists the members a tool takes, as a sentence does.
func memberNames(c *command.Command) string {
	names := make([]string, 0, len(c.Fields))
	for _, f := range c.Fields {
		names = append(names, f.Name)
	}
	return anded(names)
}
