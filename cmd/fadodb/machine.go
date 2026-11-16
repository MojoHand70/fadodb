package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MojoHand70/fadodb/fado"
)

// The commands that work on a database on the machine that holds it
// (ADR-0057, item 7; ADR-0059, item 1): making it, and making the first
// user and its key. Whoever holds the database on their own machine
// already holds everything in it, so these commands ask for no key. The
// database records what they change as made on the machine, by no user.
// While a server serves the database they are refused: the server holds
// it alone, and is reached with a key.

const initUsage = `fadodb init - make a database

Usage:
  fadodb init FILE [flags]

Makes an empty database at FILE, which must not exist, closed to every
user of the machine but the one who made it. What is fixed when a
database is made is said here:

  --max-text N     the longest text a variable holds, in characters
  --blobs WHERE    where the bytes of files are kept: sqlite, in the
                   database, or dir, in a directory beside it
  --max-blob N     the largest file, in bytes

Then make the first administrator and serve the database:

  fadodb user add olga --kind person --system administrators --db FILE
  fadodb key new olga --label laptop --db FILE
  fadodb serve FILE
`

const userUsage = `fadodb user add - make a user, on the machine that holds the database

Usage:
  fadodb user add ID --kind KIND --db FILE [flags]

  --kind KIND      person, agent or service
  --name NAME      how a screen shows the user
  --group GROUP    a group of the user; may be given several times
  --system GROUP   a system group of the user: administrators, designers,
                   content-administrators or analysts; may be given
                   several times
  --db FILE        the database

This is how the first administrator of a database is made, and the way
back in when every administrator's key is lost. While a server serves
the database, users are kept through it, with the command create_user.
`

const keyUsage = `fadodb key new - make a key for a user, on the machine that holds the database

Usage:
  fadodb key new USER --db FILE [flags]

  --label LABEL    where the key is used, such as laptop
  --expires DAY    the last day the key holds, as YYYY-MM-DD in UTC
  --db FILE        the database

Prints the key, once: the database keeps a hash of it and cannot show
it again. A key that is lost is replaced with a new one. While a server
serves the database, keys are made through it, with the command
create_key.
`

// leavesTheQueue is how every command of the tool but serve opens a
// database. The tool works on a database and is not its program: it has
// none of the program's Go functions and may call no host. A worker of
// its own would ask for the actions that are due, get no answer, and
// use up an attempt of each. So the tool starts none, and what is
// queued waits for the program, or the server, that runs it.
var leavesTheQueue = fado.OutboxOptions{Manual: true}

// onTheMachine is who the commands of this file act as: no user, with
// the work of a database administrator.
var onTheMachine = fado.As(fado.Principal{System: []string{fado.GroupAdministrators}})

// several collects a flag that may be given several times.
type several []string

func (s *several) String() string     { return strings.Join(*s, ",") }
func (s *several) Set(v string) error { *s = append(*s, v); return nil }

// parse reads the flags of a command wherever they stand among its
// arguments, and answers the arguments that are not flags.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// flags makes the flag set of a command. It prints nothing of its own:
// what a command says of a line it could not read, and its help, is
// said once, by asked.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// asked ends a command whose arguments could not be read: 0 when the
// user asked for help, 2 for a mistake.
func asked(name, usage string, err error, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stderr, usage)
		return 0
	}
	fmt.Fprintf(stderr, "%s: %v\n\n%s", name, err, usage)
	return 2
}

// exists reports whether there is a database at the path.
func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// noDatabase is what a command says when it is given a path with no
// database: a database is made by init and by nothing else.
func noDatabase(name, path string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s: there is no database at %s\n  make one with: fadodb init %s\n", name, path, path)
	return 1
}

// runInit is "fadodb init FILE".
func runInit(args []string, stdout, stderr io.Writer) int {
	const name = "fadodb init"
	fs := flags(name)
	maxText := fs.Int("max-text", 0, "")
	blobs := fs.String("blobs", "", "")
	maxBlob := fs.Int64("max-blob", 0, "")
	rest, err := parse(fs, args)
	if err != nil {
		return asked(name, initUsage, err, stderr)
	}
	if len(rest) != 1 {
		return asked(name, initUsage, errors.New("give the file of the database to make"), stderr)
	}
	path := rest[0]
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stderr, "%s: %s exists; a database is made where there is none\n", name, path)
		return 1
	}
	db, err := fado.Open(path, fado.Options{Users: true, Alone: name, Outbox: leavesTheQueue, MaxText: *maxText, BlobStore: *blobs, MaxBlob: *maxBlob})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "made the database %s\n", path)
	fmt.Fprintf(stderr, "next, its first administrator and a key:\n  fadodb user add olga --kind person --system administrators --db %s\n  fadodb key new olga --label laptop --db %s\nthen serve it:\n  fadodb serve %s\n", path, path, path)
	return 0
}

// openOnTheMachine opens a database that exists for a command of this
// file, alone.
func openOnTheMachine(name, path string, stderr io.Writer) (*fado.DB, int) {
	if path == "" {
		fmt.Fprintf(stderr, "%s: say which database, with --db FILE\n", name)
		return nil, 2
	}
	if !exists(path) {
		return nil, noDatabase(name, path, stderr)
	}
	db, err := fado.Open(path, fado.Options{Users: true, Alone: name, Outbox: leavesTheQueue})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		if fado.ErrorCode(err) == "in_use" {
			fmt.Fprintf(stderr, "  while a server serves the database, send it the command instead, with an administrator's key\n")
		}
		return nil, 1
	}
	return db, 0
}

// runUser is "fadodb user add ID …".
func runUser(args []string, stdout, stderr io.Writer) int {
	const name = "fadodb user add"
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprint(stderr, userUsage)
		if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	fs := flags(name)
	kind := fs.String("kind", "", "")
	shown := fs.String("name", "", "")
	path := fs.String("db", "", "")
	var groups, system several
	fs.Var(&groups, "group", "")
	fs.Var(&system, "system", "")
	rest, err := parse(fs, args[1:])
	if err != nil {
		return asked(name, userUsage, err, stderr)
	}
	if len(rest) != 1 {
		return asked(name, userUsage, errors.New("give the id of the user to make"), stderr)
	}
	db, code := openOnTheMachine(name, *path, stderr)
	if db == nil {
		return code
	}
	defer db.Close()
	u, err := db.CreateUser(context.Background(), fado.User{ID: rest[0], Kind: *kind, Name: *shown, Groups: groups, System: system}, onTheMachine)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "made the user %s, a %s", u.ID, u.Kind)
	if len(u.Groups) > 0 {
		fmt.Fprintf(stdout, ", in %s", strings.Join(u.Groups, ", "))
	}
	if len(u.System) > 0 {
		fmt.Fprintf(stdout, ", of the system groups %s", strings.Join(u.System, ", "))
	}
	fmt.Fprintln(stdout)
	return 0
}

// runKey is "fadodb key new USER …".
func runKey(args []string, stdout, stderr io.Writer) int {
	const name = "fadodb key new"
	if len(args) == 0 || args[0] != "new" {
		fmt.Fprint(stderr, keyUsage)
		if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	fs := flags(name)
	label := fs.String("label", "", "")
	expires := fs.String("expires", "", "")
	path := fs.String("db", "", "")
	rest, err := parse(fs, args[1:])
	if err != nil {
		return asked(name, keyUsage, err, stderr)
	}
	if len(rest) != 1 {
		return asked(name, keyUsage, errors.New("give the id of the user the key is for"), stderr)
	}
	db, code := openOnTheMachine(name, *path, stderr)
	if db == nil {
		return code
	}
	defer db.Close()
	key, err := db.CreateKey(context.Background(), rest[0], *label, *expires, onTheMachine)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	// The key alone goes to standard output, so that a script takes it;
	// what is said about it goes beside.
	fmt.Fprintln(stdout, key.Secret)
	fmt.Fprintf(stderr, "this is the key %s of %s, shown this once: the database keeps a hash of it and cannot show it again\n", key.ID, key.User)
	return 0
}
