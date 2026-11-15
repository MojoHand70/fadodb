package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/outbound"
	"github.com/MojoHand70/fadodb/internal/command"
)

const classUsage = `fadodb class check - ask whether the engine would take a class

Usage:
  fadodb class check FILE... [flags]

Reads a class definition, one JSON object, from each FILE and reports
what the engine would refuse, storing nothing. Several files are
checked together, as the command define_classes takes them, so that
they may name each other. A FILE of - is standard input.

  --db FILE   the database the classes are meant for: the classes,
              tables, schemas and actions it has are what a definition
              may name. Left out, the definitions are checked against
              an empty database, which has only what the engine brings.
  --json      print the findings as the command validate_class answers
              them, {"diagnostics": [...]}, for a program to read

A finding has a code, the place in the definition, what is wrong and,
where the engine knows, what to write instead. A note is a finding
that refuses nothing.

Exits 0 when the engine would take the definitions, 1 when a finding
refuses one, 2 on a usage mistake or a file that cannot be read. With
--db the database is opened and nothing is written to it. A database
that a server holds is asked through the server, with the command
validate_class.
`

const actionUsage = `fadodb action check - ask whether the engine would take an action

Usage:
  fadodb action check FILE [flags]

Reads an action definition, one JSON object, from FILE and reports
what the engine would refuse, storing nothing. A FILE of - is standard
input.

  --db FILE        the database the action is meant for. Left out, the
                   definition is checked against an empty database.
  --settings FILE  the settings the server will run with, as fadodb
                   serve takes them: the hosts an action may call and
                   the connections it may name. Left out, the action
                   may call no host, as under a server told nothing.
  --json           print the findings as the command validate_action
                   answers them, {"diagnostics": [...]}, for a program
                   to read

The check answers what fadodb serve would answer with the same
settings. That server runs no Go function, so a definition with no
request is refused here as it is there; a program of your own that has
the function takes it.

Exits 0 when the engine would take the definition, 1 when a finding
refuses it, 2 on a usage mistake or a file that cannot be read.
`

// stdin is where a FILE of - is read from.
var stdin io.Reader = os.Stdin

// runDefinition is "fadodb class check" and "fadodb action check": it
// asks the engine what it would refuse of the definitions in the files,
// and stores nothing. what is "class" or "action".
func runDefinition(what string, args []string, stdout, stderr io.Writer) int {
	usage, name := classUsage, "fadodb class check"
	if what == "action" {
		usage, name = actionUsage, "fadodb action check"
	}
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, usage)
		if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	fs := flags(name, usage, stderr)
	path := fs.String("db", "", "")
	asJSON := fs.Bool("json", false, "")
	settingsFile := new(string)
	if what == "action" {
		settingsFile = fs.String("settings", "", "")
	}
	files, err := parse(fs, args[1:])
	if err != nil {
		return asked(name, usage, err, stderr)
	}
	switch {
	case len(files) == 0:
		return asked(name, usage, errors.New("give the file of the definition"), stderr)
	case what == "action" && len(files) != 1:
		return asked(name, usage, errors.New("an action is checked one at a time"), stderr)
	}
	// The definitions, as they stand in the files. A file that is not
	// one JSON object is the engine's to refuse, with its place.
	definitions := make([][]byte, len(files))
	for i, file := range files {
		if file == "-" {
			definitions[i], err = io.ReadAll(stdin)
		} else {
			definitions[i], err = os.ReadFile(file)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 2
		}
	}

	// The database the definitions are checked against: the one named,
	// or an empty one, made for the check and removed after it.
	against := *path
	opened := fado.Options{Users: true, Alone: name, Outbox: leavesTheQueue}
	if against == "" {
		dir, err := os.MkdirTemp("", "fadodb-check")
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		defer os.RemoveAll(dir)
		against, opened = filepath.Join(dir, "empty.fado"), fado.Options{Outbox: leavesTheQueue}
	} else if !exists(against) {
		return noDatabase(name, against, stderr)
	}
	// An action is checked as the server would take it: with the hosts
	// its settings let an action call, and none when it is told nothing
	// (ADR-0059, item 4).
	if *settingsFile != "" {
		told, err := readSettings(*settingsFile)
		if err != nil {
			fmt.Fprintf(stderr, "%s: the settings %s: %v\n", name, *settingsFile, err)
			return 2
		}
		if told.calls() {
			if opened.Outbound, err = outbound.Allow(outbound.Options{Hosts: told.hosts, Connections: told.connections}); err != nil {
				fmt.Fprintf(stderr, "%s: the settings %s: %v\n", name, *settingsFile, err)
				return 2
			}
			// The server finds the secrets of a connection when it sends
			// a request. A definition is checked without one: the check
			// reads no secret, and does not say whether one is there.
			opened.Secrets = (&secrets{env: func(string) (string, bool) { return "", false }}).find
		}
	}
	db, err := fado.Open(against, opened)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		if fado.ErrorCode(err) == "in_use" {
			fmt.Fprintf(stderr, "  while a server serves the database, send it the command validate_%s instead, with a key\n", what)
		}
		return 1
	}
	defer db.Close()

	var found fado.Diagnostics
	switch {
	case what == "action":
		found = db.ValidateAction(definitions[0])
	case len(definitions) == 1:
		found = db.ValidateClass(definitions[0])
	default:
		found = db.ValidateClasses(definitions)
	}
	code := 0
	if found.Refuses() {
		code = 1
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
		return code
	}

	// For a person: the findings under the file each is about. Checked
	// together, a finding begins with the place of its definition among
	// them, /1/variables/total for the second file.
	byFile := make([]fado.Diagnostics, len(files))
	for _, d := range found {
		i := 0
		if len(files) > 1 {
			first, rest, _ := strings.Cut(strings.TrimPrefix(d.Path, "/"), "/")
			if n, err := strconv.Atoi(first); err == nil && n >= 0 && n < len(files) {
				i, d.Path = n, "/"+rest
			}
		}
		byFile[i] = append(byFile[i], d)
	}
	for i, file := range files {
		if file == "-" {
			file = "standard input"
		}
		refusing, notes := 0, 0
		for _, d := range byFile[i] {
			if d.Note {
				notes++
			} else {
				refusing++
			}
		}
		switch {
		case refusing == 0 && code == 0:
			fmt.Fprintf(stdout, "%s: the engine takes it", file)
		case refusing == 0:
			fmt.Fprintf(stdout, "%s: nothing refuses it", file)
		case refusing == 1:
			fmt.Fprintf(stdout, "%s: 1 finding refuses it", file)
		default:
			fmt.Fprintf(stdout, "%s: %d findings refuse it", file, refusing)
		}
		switch notes {
		case 0:
			fmt.Fprintln(stdout)
		case 1:
			fmt.Fprintln(stdout, ", with 1 note")
		default:
			fmt.Fprintf(stdout, ", with %d notes\n", notes)
		}
		for _, d := range byFile[i] {
			kind := ""
			if d.Note {
				kind = "note: "
			}
			place := d.Path
			if place == "" {
				place = "the definition"
			}
			if d.Pos >= 0 {
				place += ", offset " + strconv.Itoa(d.Pos)
			}
			fmt.Fprintf(stdout, "  %s%s at %s: %s\n", kind, d.Code, place, d.Message)
			if d.Hint != "" {
				fmt.Fprintf(stdout, "    fix: %s\n", d.Hint)
			}
			if what == "action" && *settingsFile == "" && d.Code == "not_supported" && d.Path == "/request" {
				fmt.Fprintf(stdout, "    here: give the settings the server will run with, --settings FILE\n")
			}
		}
	}
	return code
}
