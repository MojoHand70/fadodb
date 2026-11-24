package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/MojoHand70/fadodb/internal/command"
)

// Send carries a command to a database and brings back what it
// answered: the answer of the command, or, with refused set, its
// refusal as the database wrote it. An error says that no answer came
// through, so that nobody knows whether the command was carried out.
//
// The door that an agent's program starts has one Send, for the user
// whose key it was given. The door at the server itself is given the
// Send of each request, for whoever that request proves to be.
type Send func(ctx context.Context, name string, object []byte) (answer []byte, refused bool, err error)

// Options say what a door offers.
type Options struct {
	// Version is the version of the program that holds the door, which
	// the door tells an agent's program.
	Version string
	// Database says where the commands go, such as the address of a
	// server, for the words the door says of itself.
	Database string
	// ReadOnly offers only the tools that read.
	ReadOnly bool
	// Only names the commands to offer; every command when it is empty.
	Only []string
	// AtServer says the door stands at the server itself, at /mcp, where
	// each request proves who sends it, and not in a program that was
	// given the key of one user.
	AtServer bool
	// Files is the folder the door's files come from and go to, when
	// whoever starts it names one; with none the door offers no tool for
	// files. The door at the server is given none.
	Files Files
}

// Withheld answers why the door offers a command as no tool, and "" for
// a command it offers.
func Withheld(name string) string {
	switch name {
	case "subscribe":
		return "its answer is a stream, and a tool is one question and one answer: an agent waits with wait_for_changes"
	case "create_key":
		return "its answer holds the secret of a key, which would stay in the conversation of an agent; a key is made at a terminal"
	case "create_password_link":
		return "its answer holds the secret of a link that sets a password, which would stay in the conversation of an agent; a link is made at a terminal and given to the person"
	case "create_invitation":
		return "its answer holds the secret of an invitation, which lets a person in and would stay in the conversation of an agent; an invitation is made at a terminal and given to the person"
	}
	return ""
}

// reachesOut says a command itself sends a request to a host outside
// the database: the two that run an action now. Every other command
// reads or changes the database and nothing else.
func reachesOut(name string) bool { return name == "try_action" || name == "drain" }

// builds says a command makes a thing the database keeps one list of: a
// class, a table or a schema. An agent looks at what is already kept
// before it calls one, so that nothing is kept twice. The door says so
// of itself and on each such tool, since it is the one place every
// agent passes; the whole procedure is the first step of the
// designer's skill.
func builds(name string) bool {
	switch name {
	case "define_app", "define_class", "define_classes", "define_table", "define_schema":
		return true
	}
	return false
}

// lists names the tools offered that answer what the database keeps.
func lists(tools map[string]bool) []string {
	var names []string
	for _, name := range []string{"list_apps", "list_classes", "list_tables", "list_schemas"} {
		if tools[name] {
			names = append(names, name)
		}
	}
	return names
}

// CodeNoAnswer is the code of the door's own for a command that no
// answer came through for: the database was not reached, or its answer
// was cut off. It is no refusal of the database, which never saw the
// command or was not heard. The command may have been carried out.
const CodeNoAnswer = "no_answer"

// tool is a command as the protocol lists a tool.
type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations hints          `json:"annotations"`
}

// hints are the marks of a tool. Each is always written: the protocol
// takes a tool with no mark for one that destroys and reaches out.
type hints struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

// Door offers the commands of a database as tools.
type Door struct {
	send Send
	// files carries the bytes of the tools for files, when the door was
	// given a folder.
	files Files
	// info is the door as it names itself to an agent's program.
	info map[string]any
	// says is what the door says of itself to an agent.
	says string
	// tools are the names of the tools offered, and listed the tools as
	// the protocol lists them, written once.
	tools  map[string]bool
	listed json.RawMessage
	// readOnly and narrowed say why a command is no tool of this door.
	readOnly, narrowed bool
	// atServer says the door stands at the server, where there is no
	// folder for files.
	atServer bool

	// running are the calls of tools under way that came over HTTP, by
	// who sent them and the id of their request, each with what stops
	// it: a notice that stops a call names only the id.
	mu      sync.Mutex
	running map[string]*underway
}

// New makes a door that sends its commands through send. A door that is
// asked only over HTTP, with Exchange, which is given the Send of each
// request, is made with none.
func New(send Send, o Options) (*Door, error) {
	if o.AtServer && o.Files != nil {
		return nil, fmt.Errorf("the door at the server is given no folder: files come through the door an agent's program starts")
	}
	only := map[string]bool{}
	for _, name := range o.Only {
		if _, ok := fileTool(name); ok {
			switch {
			case o.Files == nil:
				return nil, fmt.Errorf("%s is offered only when the door is given a folder for files, with --files FOLDER", name)
			case o.ReadOnly && name == ToolPutFile:
				return nil, fmt.Errorf("%s does not only read, and the door was told to offer only the tools that read", name)
			}
			only[name] = true
			continue
		}
		c, known := command.Lookup(name)
		switch {
		case !known:
			return nil, fmt.Errorf("no command is named %q", name)
		case Withheld(name) != "":
			return nil, fmt.Errorf("%s is offered as no tool: %s", name, Withheld(name))
		case o.ReadOnly && c.Mark != command.Reads:
			return nil, fmt.Errorf("%s does not only read, and the door was told to offer only the tools that read", name)
		}
		only[name] = true
	}
	d := &Door{
		send:     send,
		files:    o.Files,
		running:  map[string]*underway{},
		tools:    map[string]bool{},
		readOnly: o.ReadOnly,
		atServer: o.AtServer,
		narrowed: len(only) > 0,
		info:     map[string]any{"name": "fadodb", "title": "FadoDB", "version": o.Version},
	}
	var offered []*command.Command
	for _, c := range command.Commands() {
		switch {
		case c.Stream, Withheld(c.Name) != "":
			continue
		case o.ReadOnly && c.Mark != command.Reads:
			continue
		case len(only) > 0 && !only[c.Name]:
			continue
		}
		d.tools[c.Name] = true
		offered = append(offered, c)
	}
	// The tools for files come after the commands, when there is a
	// folder. get_file reads the database, so a door that offers only
	// the tools that read offers it.
	if o.Files != nil {
		for _, c := range []*command.Command{putFile, getFile} {
			switch {
			case o.ReadOnly && c.Mark != command.Reads:
				continue
			case len(only) > 0 && !only[c.Name]:
				continue
			}
			d.tools[c.Name] = true
			offered = append(offered, c)
		}
	}
	var tools []tool
	for _, c := range offered {
		_, forFiles := fileTool(c.Name)
		tools = append(tools, tool{
			Name:        c.Name,
			Title:       title(c.Name),
			Description: describe(c, d.tools),
			InputSchema: c.Schema(),
			Annotations: hints{
				// A tool for files changes the folder or the database:
				// neither leaves everything as it was.
				ReadOnly:    c.Mark == command.Reads && !forFiles,
				Destructive: c.Mark == command.Destroys,
				OpenWorld:   reachesOut(c.Name),
			},
		})
	}
	listed, err := command.Marshal(tools)
	if err != nil {
		return nil, err
	}
	d.listed = listed
	d.says = says(o, d.tools)
	return d, nil
}

// Tools lists the names of the tools the door offers, in the order of
// their names.
func (d *Door) Tools() []string {
	names := make([]string, 0, len(d.tools))
	for name := range d.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// title is how a screen shows a tool: the name of its command in words.
func title(name string) string {
	switch name {
	case "sql":
		return "SQL"
	case "whoami":
		return "Who am I"
	}
	words := strings.ReplaceAll(name, "_", " ")
	return strings.ToUpper(words[:1]) + words[1:]
}

// describe says what a tool does and what it answers, in the words of
// the table. A tool that builds also says to look first, naming the
// tools of this door that answer what the database keeps: an agent's
// program may show its agent the tools and not what the door says of
// itself.
func describe(c *command.Command, tools map[string]bool) string {
	var b strings.Builder
	b.WriteString(c.Does)
	if len(c.Answer) == 0 {
		b.WriteString(" It answers {}.")
	} else {
		b.WriteString(" It answers ")
		for i, m := range c.Answer {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(m.Name + ": " + m.Does)
		}
		b.WriteString(".")
	}
	if builds(c.Name) {
		b.WriteString(" Look before you build:")
		switch lists := lists(tools); len(lists) {
		case 0:
		case 1:
			b.WriteString(" " + lists[0] + " answers what the database already keeps, and")
		default:
			b.WriteString(" " + anded(lists) + " answer what the database already keeps, and")
		}
		b.WriteString(" a thing is never kept twice.")
	}
	return b.String()
}

// anded writes names as a sentence lists them: a, b and c.
func anded(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// says is what the door tells an agent of itself when it is asked what
// it is, the sign above the door: what the tools are, what each first
// step answers, and to look before it builds.
func says(o Options, tools map[string]bool) string {
	var b strings.Builder
	b.WriteString("FadoDB is a database of documents that move through the states of their class. Each tool is one command of the database")
	if o.Database != "" {
		b.WriteString(" at " + o.Database)
	}
	if o.AtServer {
		b.WriteString(", sent as whoever each request proves to be: the agent a person connected acts for that person, with that person's groups, and a key is its user's own.")
	} else {
		b.WriteString(", sent as the user whose key this door was given.")
	}
	if tools["whoami"] {
		b.WriteString(" whoami answers who that user is.")
	}
	if tools["list_classes"] {
		b.WriteString(" list_classes answers which classes there are, and get_class how one is defined: its variables, its states, its transitions and who may do what.")
	}
	if tools["list_apps"] {
		b.WriteString(" list_apps answers which apps there are: an app is a name for the classes and the tables that belong together, and a class or a table says in its definition which app it belongs to.")
	}
	if tools["list_functions"] {
		b.WriteString(" list_functions answers every function a formula can call, with its arguments; read it before you write a formula.")
	}
	for name := range tools {
		if !builds(name) {
			continue
		}
		b.WriteString(" Look before you build: before you define an app, a class, a table or a schema, list what the database has")
		if lists := lists(tools); len(lists) > 0 {
			b.WriteString(" (" + strings.Join(lists, ", ") + ")")
		}
		b.WriteString(" and read each one that may be the same thing; match by meaning, not by name. Ask the person you work for which one to use, and never keep a thing twice.")
		break
	}
	if tools["preview"] {
		b.WriteString(" preview answers what a create, a set or a transition would do, and writes nothing.")
	}
	if o.Files != nil && (tools[ToolPutFile] || tools[ToolGetFile]) {
		b.WriteString(" Files go through the folder " + o.Files.Folder() + ", and nothing outside it is read or written:")
		if tools[ToolPutFile] {
			b.WriteString(" put_file puts the bytes of a file of the folder and answers its facts, which create or set give a variable of the schema File;")
		}
		if tools[ToolGetFile] {
			b.WriteString(" get_file writes a file a document holds into the folder, by the hash the document holds, and never over another file;")
		}
		b.WriteString(" the bytes never travel in a tool's arguments or answer.")
	}
	b.WriteString(" A command the database refuses is answered with the refusal under error: a code, a message, the place in the command and a hint.")
	if keyed := keyed(tools); len(keyed) > 0 {
		b.WriteString(" The tools that commit a document take a key, and one sent again with the same key makes no second commit: " + strings.Join(keyed, ", ") + ". No other tool takes one.")
	}
	if o.ReadOnly {
		b.WriteString(" This door offers only the tools that read.")
	}
	return b.String()
}

// keyed lists the tools offered that take the idempotency key, in the
// order of the table.
func keyed(tools map[string]bool) []string {
	var names []string
	for _, c := range command.Commands() {
		if tools[c.Name] && c.TakesKey() {
			names = append(names, c.Name)
		}
	}
	return names
}

// call sends a command and answers the text of a tool's answer, and
// whether it is an error.
func (d *Door) call(ctx context.Context, send Send, name string, object []byte) (string, bool) {
	if c, ok := fileTool(name); ok && d.files != nil {
		return d.callFile(ctx, c, object)
	}
	answer, refused, err := send(ctx, name, object)
	if err != nil {
		return noAnswer(name, err), true
	}
	return string(bytes.TrimSpace(answer)), refused
}

// noAnswer is what a tool answers when no answer came through, in the
// form of a refusal, so that an agent reads one form. Its hint says
// what is safe for the command that was sent: a command that reads is
// asked again, one that takes a key is sent again with its key, and
// any other may have been carried out and has no key to send it again
// with.
func noAnswer(name string, err error) string {
	hint := "the command may have been carried out, and it takes no key: read what it would have changed before sending it again"
	switch name {
	case ToolPutFile:
		hint = "the bytes may have been kept: put the file again, and the same bytes are kept once"
	case ToolGetFile:
		hint = "the database was not changed, and no file was left in the folder: ask again"
	}
	if c, known := command.Lookup(name); known {
		switch {
		case c.Mark == command.Reads:
			hint = "the command only reads, so nothing was changed: ask again"
		case c.TakesKey():
			hint = "the command may have been carried out: sent again with the same key, it makes no second commit; sent with no key, read the document before sending it again"
		}
	}
	text, _ := command.Marshal(map[string]any{"error": map[string]string{
		"code":    CodeNoAnswer,
		"message": "no answer came through: " + err.Error(),
		"hint":    hint,
	}})
	return string(text)
}

// unknownTool says why a name is no tool of this door.
func (d *Door) unknownTool(name string) string {
	if c, ok := fileTool(name); ok {
		switch {
		case d.atServer:
			return "Unknown tool: " + name + ". Files come through the door an agent's program starts, from a folder whoever starts it names with --files FOLDER: at the server there is no folder of the caller's, and the bytes of a file do not pass through a conversation"
		case d.files == nil:
			return "Unknown tool: " + name + ". This door was started with no folder for files: whoever starts it names one, fadodb mcp --files FOLDER"
		case d.readOnly && c.Mark != command.Reads:
			return "Unknown tool: " + name + ". This door was started to offer only the tools that read"
		case d.narrowed:
			return "Unknown tool: " + name + ". This door was started with a list of tools, and " + name + " is not in it"
		}
	}
	c, known := command.Lookup(name)
	switch {
	case !known:
		return "Unknown tool: " + name
	case Withheld(name) != "":
		return "Unknown tool: " + name + ". The command is offered as no tool: " + Withheld(name)
	case d.readOnly && c.Mark != command.Reads:
		return "Unknown tool: " + name + ". This door was started to offer only the tools that read"
	case d.narrowed:
		return "Unknown tool: " + name + ". This door was started with a list of tools, and " + name + " is not in it"
	}
	return "Unknown tool: " + name
}
