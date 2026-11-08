package main

import (
	"context"
	"fmt"
	"io"

	"github.com/MojoHand70/fadodb/internal/store"
)

const checkUsage = `fadodb check - verify a database file

Usage:
  fadodb check FILE

Opens the database, reads every blob the attachment store holds,
recomputes its hash and reports the blobs whose bytes do not match
(ADR-0033), so that a restore from an altered backup is found. Exits 0
when every blob matches, 1 when one does not or the file cannot be
opened, 2 on a usage mistake. A database whose blob store is the
embedder's cannot be checked here.
`

// runCheck is "fadodb check FILE".
func runCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stderr, checkUsage)
		if len(args) == 1 {
			return 0
		}
		return 2
	}
	s, err := store.Open(args[0], store.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "fadodb check: %v\n", err)
		return 1
	}
	defer s.Close()
	mismatched, checked, err := s.CheckBlobs(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "fadodb check: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %d classes, %d tables, %d blobs checked", args[0], len(s.Classes()), len(s.Tables()), checked)
	if len(mismatched) == 0 {
		fmt.Fprintln(stdout, ", all match")
		return 0
	}
	fmt.Fprintf(stdout, ", %d do not match:\n", len(mismatched))
	for _, h := range mismatched {
		fmt.Fprintln(stdout, "  "+h)
	}
	return 1
}
