// Command fadodb is the FadoDB command-line tool and, later, its server.
//
// At this stage of the project only "fadodb version" does anything.
// See ROADMAP.md for what comes next.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
)

// Set by the release build through -ldflags. A plain "go build" leaves the
// defaults in place, so a development binary never claims to be a release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

const usage = `fadodb - an embeddable active document database (pre-release)

Usage:
  fadodb version   print version, commit and build date
  fadodb help      print this help

Nothing else is implemented yet.
Roadmap: https://github.com/MojoHand70/fadodb/blob/main/ROADMAP.md
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, versionString())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "fadodb: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// versionString reports the version, the commit, the build date and the Go
// toolchain. The commit falls back to the VCS revision embedded by the Go
// tool when the release build did not set one.
func versionString() string {
	c := commit
	if c == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					c = s.Value[:7]
				}
			}
		}
	}
	s := "fadodb " + version
	if c != "" {
		s += " (" + c
		if date != "" {
			s += ", " + date
		}
		s += ")"
	}
	return s + " " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}
