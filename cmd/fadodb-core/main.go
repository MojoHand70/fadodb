// Command fadodb-core is the FadoDB command-line tool and its server,
// in pure Go and without the analytical engine. Its code is the package
// internal/cli; the module analytics builds the same program with the
// engine, DuckDB, as fadodb (ADR-0017, ADR-0089). The commands and their
// answers are the same in both.
package main

import (
	"os"

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
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, cli.Build{Version: version, Commit: commit, Date: date}))
}
