// Command fadodb, built here, is the FadoDB command-line tool and its
// server with the analytical engine (ADR-0017, ADR-0089): the program of
// cmd/fadodb, from the same code, and DuckDB linked in. A database it
// serves keeps a copy of its documents in a DuckDB file beside its own,
// and the questions that read everything are answered from the copy.
//
// It needs cgo and a C compiler, which is why it is built in this
// module and the program in pure Go is not.
package main

import (
	"os"

	"github.com/MojoHand70/fadodb/analytics"
	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/cli"
)

// Set by the release build through -ldflags. A plain "go build" leaves
// the defaults in place, so a development binary never claims to be a
// release.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, cli.Build{Version: version, Commit: commit, Date: date,
		Engine: func(memory string, threads int) fado.Analytics {
			return analytics.Engine{Memory: memory, Threads: threads}
		}}))
}
