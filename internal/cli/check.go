package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/store"
)

const checkUsage = `fadodb check - verify a database file

Usage:
  fadodb check FILE

Opens the database and checks its files both ways (ADR-0033). It
reads every blob the attachment store holds, recomputes its hash and
reports the blobs whose bytes do not match. And it reads the hash of
every file a document holds, in every version, and reports the ones
the store does not have, each with its document. So a restore from a
backup that was altered, or that left files out, is found.

And it checks the seal of the log (ADR-0103). It works the seal of
every event out again from what the event wrote and the seal of the
event before, and reports every event whose seal does not match, every
row of a history or a list that no event wrote, and every document
whose current row is not its last version. So a change made to the
file behind the engine's back, with any SQLite tool, is found.

Exits 0 when every blob and every seal matches and no file is missing,
1 when one does not, one is missing or the file cannot be opened, 2 on
a usage mistake. A database whose blob store is the embedder's cannot
be checked here. The check runs none of the database's queued actions.
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
	// A database is made by init and by nothing else (ADR-0059, item 1).
	if !exists(args[0]) {
		return noDatabase("fadodb check", args[0], stderr)
	}
	s, err := store.Open(args[0], store.Options{Outbox: leavesTheQueue})
	if err != nil {
		fmt.Fprintf(stderr, "fadodb check: %v\n", err)
		return 1
	}
	defer s.Close()
	// Whoever runs this tool on a database operates it: the check is the
	// work of a database administrator (ADR-0052).
	operator := store.As(store.Principal{ID: "fadodb", Kind: store.ActorService, System: []string{store.GroupAdministrators}})
	check, err := s.CheckBlobs(context.Background(), operator)
	if err != nil {
		fmt.Fprintf(stderr, "fadodb check: %v\n", err)
		return 1
	}
	// The system tables are views over the engine's records (ADR-0092),
	// and hold no rows of their own to count.
	tables := 0
	for _, name := range s.Tables() {
		if !model.IsSystemTable(name) {
			tables++
		}
	}
	fmt.Fprintf(stdout, "%s: %d classes, %d tables, %d blobs checked", args[0], len(s.Classes()), tables, check.Checked)
	if len(check.Mismatched) == 0 {
		fmt.Fprint(stdout, ", all match")
	} else {
		fmt.Fprintf(stdout, ", %d do not match", len(check.Mismatched))
	}
	switch len(check.Missing) {
	case 0:
		fmt.Fprintln(stdout, ", no file of a document is missing")
	case 1:
		fmt.Fprintln(stdout, ", 1 file of a document is missing")
	default:
		fmt.Fprintf(stdout, ", %d files of documents are missing\n", len(check.Missing))
	}
	if len(check.Mismatched) > 0 {
		fmt.Fprintln(stdout, "the bytes of these blobs are not the bytes that were put:")
		for _, h := range check.Mismatched {
			fmt.Fprintln(stdout, "  "+h)
		}
	}
	if len(check.Missing) > 0 {
		fmt.Fprintln(stdout, "these documents hold a file that the store does not have:")
		for _, m := range check.Missing {
			fmt.Fprintf(stdout, "  %s %s, %s, since version %d: %s\n", m.Class, m.ID, m.Variable, m.Version, m.Hash)
		}
	}
	logCheck, err := s.CheckLog(context.Background(), operator)
	if err != nil {
		fmt.Fprintf(stderr, "fadodb check: %v\n", err)
		return 1
	}
	printLog(stdout, logCheck)
	if len(check.Mismatched) > 0 || len(check.Missing) > 0 || len(logCheck.Faults) > 0 {
		return 1
	}
	return 0
}

// printLog prints what the check of the log found: how many events,
// from when they are sealed, and each place that does not match.
func printLog(stdout io.Writer, c *store.LogCheck) {
	if c.Events == 0 {
		fmt.Fprint(stdout, "the log: no events")
	} else {
		fmt.Fprintf(stdout, "the log: %d events", c.Events)
		switch {
		case c.Sealed == c.Events:
			fmt.Fprint(stdout, ", all sealed")
		case c.Sealed == 0:
			fmt.Fprint(stdout, ", none sealed")
		default:
			fmt.Fprintf(stdout, ", sealed from %s on, %d before that are not", moment(c.SealedFrom), c.Events-c.Sealed)
		}
	}
	switch len(c.Faults) {
	case 0:
		fmt.Fprintln(stdout, ", every seal matches")
		return
	case 1:
		fmt.Fprintln(stdout, ", 1 place does not match its seal:")
	default:
		fmt.Fprintf(stdout, ", %d places do not match their seals:\n", len(c.Faults))
	}
	for _, f := range c.Faults {
		fmt.Fprintf(stdout, "  %s %s, version %d", f.Class, f.ID, f.Version)
		if f.Event != "" && f.At != 0 {
			what := f.Kind
			if f.Name != "" {
				what = f.Name
			}
			fmt.Fprintf(stdout, ", %s at %s", what, moment(f.At))
		}
		fmt.Fprintf(stdout, ": %s\n", f.Message)
	}
}

// moment says a moment in seconds since 1970 in UTC.
func moment(secs int64) string {
	return time.Unix(secs, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}
