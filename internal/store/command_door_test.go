package store_test

import (
	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The command set imports the store, so the store's own tests cannot
// import it. This file is outside the package and in its test binary:
// it hands the command set to the suites, which then run through it as
// they run through the Go API (ADR-0055, item 11).
func init() { store.SetExecCommand(command.Exec) }
