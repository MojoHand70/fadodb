// Package command is the command set of FadoDB (ADR-0055): every call of
// the Go API that reads or changes a database, as a name and one JSON
// object, answered by one JSON object.
//
// Exec turns a command into the call. Every door uses it: the Go API
// itself (fado.DB.Exec), and from Phase 6 the HTTP server, the
// command-line tool and the door for agents. The table of the commands
// is data (Commands), from which the doors make their addresses, their
// descriptions and their help, so that a call cannot be on one door and
// missing from another.
package command
