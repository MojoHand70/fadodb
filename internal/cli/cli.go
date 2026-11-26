// Package cli is the FadoDB command-line tool and its server: the
// program fadodb, and the program with the analytical engine that the
// module analytics builds from the same code (ADR-0017, ADR-0089).
//
// "fadodb init" makes a database, "fadodb user add" and "fadodb key new"
// make its first administrator on the machine that holds it, and
// "fadodb serve" serves it over HTTP to callers who send a key
// (ADR-0059). "fadodb exec" is such a caller: it sends a command to a
// server with the key of a user and prints the answer, and "fadodb
// file" puts a file on a document or reads one (ADR-0062). "fadodb mcp"
// is the door for agents: it offers the commands of a server as tools
// to an agent's program (ADR-0064). "fadodb health" asks a server
// whether it answers.
// "fadodb class check", "fadodb action check" and "fadodb inlet check"
// ask the engine whether it would take a definition in a file. "fadodb formula eval" runs the
// formula engine, "fadodb check" verifies a database file and "fadodb
// version" prints the build. See ROADMAP.md for what comes next.
package cli

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/MojoHand70/fadodb/fado"

	// The time zones, so that a database has its zone (ADR-0093) on a
	// machine that has no database of them, as the image for containers
	// has none.
	_ "time/tzdata"
)

// What the program says of itself: its release, set by the release
// build through -ldflags on the program's own package and handed to
// Main. A plain "go build" leaves the defaults in place, so a
// development binary never claims to be a release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// makeEngine makes the analytical engine of the program for a server
// (ADR-0089), with the most memory it may take and how many threads it
// uses, as the server's settings say; nil in the program built without
// one. A server then keeps no copy of its documents and answers every
// question by the store.
var makeEngine func(memory string, threads int) fado.Analytics

// Build is what a program built from this package hands to Main: its
// release, and what makes its analytical engine when it has one.
type Build struct {
	Version, Commit, Date string
	Engine                func(memory string, threads int) fado.Analytics
}

// coreNote ends the help of the program without the analytical engine:
// its commands are written as fadodb's, which they are.
const coreNote = `
This program is fadodb-core: FadoDB without the analytical engine. Its
commands and their answers are those of fadodb, as written above. The
program fadodb answers the questions that read a whole class in
milliseconds on a large database; see the README, "Which program".
`

const usage = `fadodb - an embeddable active document database (pre-release)

Usage:
  fadodb init FILE                      make a database
  fadodb user add ID --kind KIND --db FILE
                                        make a user, on the machine that holds the database
  fadodb key new USER --db FILE         make a key for a user, and print it once
  fadodb serve FILE                     serve a database over HTTP, on this machine only
                                        unless told otherwise
  fadodb exec COMMAND [OBJECT]          send a command to a server, with the key of a user,
                                        and print the answer; fadodb exec help lists the commands
  fadodb file put FILE                  put a file on a document, through a server
  fadodb file get                       read the file a document holds, through a server
  fadodb mcp                            the door for agents: offer the commands of a server
                                        as tools to an agent's program, with the key of a user
  fadodb health                         ask a server whether it answers, with no key
  fadodb class check FILE...            ask whether the engine would take a class, storing nothing
  fadodb action check FILE              ask whether the engine would take an action, storing nothing
  fadodb inlet check FILE               ask whether the engine would take an inlet, a way in for
                                        messages from outside, and what a message would become
  fadodb formula eval [flags] FORMULA   evaluate a formula, or print its bytecode or SQL
  fadodb check FILE                     verify a database file: every blob's hash
  fadodb version                        print version, commit and build date
  fadodb help                           print this help

Run a command with --help for its flags. A database that is served
answers the commands of docs/manual/commands.md, each sent as
POST /v1/<command> with a key of a user, which fadodb exec does.
Roadmap: https://github.com/MojoHand70/fadodb/blob/main/ROADMAP.md
`

// Main runs the program for the arguments after its name and answers
// its exit code.
func Main(args []string, stdout, stderr io.Writer, b Build) int {
	if b.Version != "" {
		version = b.Version
	}
	commit, date, makeEngine = b.Commit, b.Date, b.Engine
	return run(args, stdout, stderr)
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "user":
		return runUser(args[1:], stdout, stderr)
	case "key":
		return runKey(args[1:], stdout, stderr)
	case "serve":
		ctx, stop := interrupted()
		defer stop()
		return runServe(ctx, args[1:], stdout, stderr, nil)
	case "class", "action":
		return runDefinition(args[0], args[1:], stdout, stderr)
	case "inlet":
		return runInlet(args[1:], stdout, stderr)
	case "exec":
		ctx, stop := interrupted()
		defer stop()
		return runExec(ctx, args[1:], stdout, stderr)
	case "file":
		ctx, stop := interrupted()
		defer stop()
		return runFile(ctx, args[1:], stdout, stderr)
	case "mcp":
		ctx, stop := interrupted()
		defer stop()
		return runMCP(ctx, args[1:], stdin, stdout, stderr)
	case "health":
		ctx, stop := interrupted()
		defer stop()
		return runHealth(ctx, args[1:], stdout, stderr)
	case "formula":
		return runFormula(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, versionString())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		if makeEngine == nil {
			fmt.Fprint(stdout, coreNote)
		}
		return 0
	}
	fmt.Fprintf(stderr, "fadodb: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// versionString reports the version, the commit, the build date and the Go
// toolchain, from the values the release build sets and, where it did not,
// from the build information the Go tool embeds.
func versionString() string {
	info, _ := debug.ReadBuildInfo()
	return versionStringFrom(version, commit, date, info)
}

// versionStringFrom builds the version line. A version of "dev" falls back
// to the module version in info, which "go install module@version" sets
// and a build from a checkout leaves as "(devel)"; an empty commit falls
// back to the VCS revision a build from a checkout embeds.
func versionStringFrom(ver, rev, built string, info *debug.BuildInfo) string {
	v, c := ver, rev
	if info != nil {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = strings.TrimPrefix(info.Main.Version, "v")
		}
		if c == "" {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					c = s.Value[:7]
				}
			}
		}
	}
	// The program says which of the two it is (ADR-0089): fadodb has
	// the analytical engine, fadodb-core is the same program without.
	s := "fadodb-core " + v
	if makeEngine != nil {
		s = "fadodb " + v
	}
	if c != "" {
		s += " (" + c
		if built != "" {
			s += ", " + built
		}
		s += ")"
	}
	s += " " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
	if makeEngine != nil {
		s += ", with the analytical engine"
	}
	return s
}
