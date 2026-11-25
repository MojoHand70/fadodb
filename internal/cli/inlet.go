package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/command"
)

const inletUsage = `fadodb inlet check - ask whether the engine would take an inlet

Usage:
  fadodb inlet check FILE [flags]

Reads an inlet, one JSON object, from FILE and reports what the engine
would refuse, storing nothing. An inlet is a way in for messages from
outside: how a message proves who sent it, and the one command it
becomes. With a message, the check also shows that command. A FILE of
- is standard input.

  --class FILE    a class the command of the inlet names, in a file; it
                  may be given several times. The inlet is checked
                  against an empty database that has these classes, and
                  the user the inlet acts as, as a service
  --db FILE       the database the inlet is meant for, in place of
                  --class: its classes and its users are what the inlet
                  may name
  --message FILE  a message, as the command try_inlet takes it:
                  {"headers": {...}, "params": {...}, "body": ...}. The
                  check answers whether the inlet acts on it and the
                  command it would become. The proof is not asked for
  --json          print what the commands validate_inlet and try_inlet
                  answer, {"diagnostics": [...]} and {"trial": {...}},
                  each on a line, for a program to read

The check answers what a server that takes messages would answer. It
reads no secret, and does not say whether one is there.

Exits 0 when the engine would take the inlet and can read the message,
1 when a finding refuses the inlet or the message is not one it reads,
2 on a usage mistake or a file that cannot be read. A database that a
server holds is asked through the server, with the commands
validate_inlet and try_inlet.
`

// designing is who the tool checks a definition as, on the machine:
// nobody by name, with the rights to define and to keep users.
var designing = fado.As(fado.Principal{System: []string{"administrators", "designers"}})

// runInlet is "fadodb inlet check": it asks the engine what it would
// refuse of the inlet in the file, shows the command a message would
// become, and stores nothing.
func runInlet(args []string, stdout, stderr io.Writer) int {
	const name = "fadodb inlet check"
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, inletUsage)
		if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	fs := flags(name)
	path := fs.String("db", "", "")
	var classes several
	fs.Var(&classes, "class", "")
	messageFile := fs.String("message", "", "")
	asJSON := fs.Bool("json", false, "")
	files, err := parse(fs, args[1:])
	if err != nil {
		return asked(name, inletUsage, err, stderr)
	}
	switch {
	case len(files) != 1:
		return asked(name, inletUsage, errors.New("give the file of the inlet, one at a time"), stderr)
	case *path != "" && len(classes) > 0:
		return asked(name, inletUsage, errors.New("--db names the database the inlet is meant for, and --class a class for an empty one: give one of the two"), stderr)
	}
	read := func(file string) ([]byte, bool) {
		var data []byte
		var err error
		if file == "-" {
			data, err = io.ReadAll(stdin)
		} else {
			data, err = os.ReadFile(file)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return nil, false
		}
		return data, true
	}
	definition, ok := read(files[0])
	if !ok {
		return 2
	}
	var message []byte
	if *messageFile != "" {
		if message, ok = read(*messageFile); !ok {
			return 2
		}
	}
	definitions := make([][]byte, len(classes))
	for i, file := range classes {
		if definitions[i], ok = read(file); !ok {
			return 2
		}
	}

	// The database the inlet is checked against, opened as a server
	// that takes messages opens it. A check reads no secret.
	ctx := context.Background()
	noSecret := func(context.Context, string, string) (string, error) {
		return "", errors.New("a check reads no secret")
	}
	against := *path
	opened := fado.Options{Users: true, Inbound: true, Secrets: noSecret, Alone: name, Outbox: leavesTheQueue}
	if against == "" {
		dir, err := os.MkdirTemp("", "fadodb-check")
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		defer os.RemoveAll(dir)
		against, opened.Alone = filepath.Join(dir, "empty.fado"), ""
	} else if !exists(against) {
		return noDatabase(name, against, stderr)
	}
	db, err := fado.Open(against, opened)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		if fado.ErrorCode(err) == "in_use" {
			fmt.Fprintln(stderr, "  while a server serves the database, send it the commands validate_inlet and try_inlet instead, with a key")
		}
		return 1
	}
	defer db.Close()
	if *path == "" {
		// An empty database, made for the check: the classes that were
		// given, and the user the inlet names, as a service.
		if len(definitions) > 0 {
			if _, err := db.DefineClasses(ctx, definitions, designing); err != nil {
				fmt.Fprintf(stderr, "%s: the classes given with --class are not taken: %v\n  fadodb class check says what is wrong with a class\n", name, err)
				return 1
			}
		}
		var head struct {
			As string `json:"as"`
		}
		if json.Unmarshal(definition, &head) == nil && head.As != "" {
			// A user the database refuses is the inlet's to be told of.
			_, _ = db.CreateUser(ctx, fado.User{ID: head.As, Kind: "service"}, designing)
		}
	}

	found := db.ValidateInlet(definition)
	code := 0
	if found.Refuses() {
		code = 1
	}
	file := files[0]
	if file == "-" {
		file = "standard input"
	}
	if *asJSON {
		if found == nil {
			found = fado.Diagnostics{}
		}
		text, err := command.Marshal(struct {
			Diagnostics fado.Diagnostics `json:"diagnostics"`
		}{found})
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", text)
	} else {
		switch refusing := len(found); {
		case code == 0:
			fmt.Fprintf(stdout, "%s: the engine takes it\n", file)
		case refusing == 1:
			fmt.Fprintf(stdout, "%s: 1 finding refuses it\n", file)
		default:
			fmt.Fprintf(stdout, "%s: %d findings refuse it\n", file, refusing)
		}
		for _, d := range found {
			place := d.Path
			if place == "" {
				place = "the definition"
			}
			if d.Pos >= 0 {
				place += ", offset " + strconv.Itoa(d.Pos)
			}
			fmt.Fprintf(stdout, "  %s at %s: %s\n", d.Code, place, d.Message)
			if d.Hint != "" {
				fmt.Fprintf(stdout, "    fix: %s\n", d.Hint)
			}
			if d.Code == "unknown_class" && *path == "" {
				fmt.Fprintf(stdout, "    here: give the class in a file, --class FILE, or the database the inlet is meant for, --db FILE\n")
			}
		}
	}
	if code != 0 || message == nil {
		return code
	}

	// The message: what the inlet would do with it. It is the command
	// try_inlet, so that the file of the message is the one a caller
	// sends a server.
	object, err := command.Marshal(struct {
		Definition json.RawMessage `json:"definition"`
		Message    json.RawMessage `json:"message"`
	}{definition, message})
	if err != nil {
		fmt.Fprintf(stderr, "%s: the message %s: %v\n", name, *messageFile, err)
		return 2
	}
	answer, err := db.Exec(ctx, fado.Principal{System: []string{"administrators", "designers"}}, "try_inlet", object)
	if *asJSON {
		fmt.Fprintf(stdout, "%s\n", answer)
		if err != nil {
			return 1
		}
		return 0
	}
	if err != nil {
		fmt.Fprintf(stdout, "the message %s: the inlet cannot read it\n", *messageFile)
		var e *fado.Error
		if errors.As(err, &e) {
			place := e.Path
			if place == "" {
				place = "the message"
			}
			fmt.Fprintf(stdout, "  %s at %s: %s\n", e.Code, place, e.Message)
			if e.Hint != "" {
				fmt.Fprintf(stdout, "    fix: %s\n", e.Hint)
			}
		} else {
			fmt.Fprintf(stdout, "  %v\n", err)
		}
		return 1
	}
	var out struct {
		Trial struct {
			As      string          `json:"as"`
			Acts    bool            `json:"acts"`
			Command string          `json:"command"`
			Object  json.RawMessage `json:"object"`
		} `json:"trial"`
	}
	if err := json.Unmarshal(answer, &out); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	if !out.Trial.Acts {
		fmt.Fprintf(stdout, "the message %s: the inlet does not act on it: its when is not TRUE\n", *messageFile)
		return 0
	}
	fmt.Fprintf(stdout, "the message %s: as %s, it becomes the command %s:\n", *messageFile, out.Trial.As, out.Trial.Command)
	_ = printJSON(stdout, out.Trial.Object)
	return 0
}
