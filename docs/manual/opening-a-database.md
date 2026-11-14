# Opening a database

A database is one file. A program opens it, works with it through the
calls of the Go package `fado`, and closes it. This chapter is for the
developer whose program holds the engine: what opening does, what is
on disk, which options there are, and which of them belong to the
database and which to the program. A test runs the program of this
chapter and plays every table of it on each push.

## The program

The whole of it: it makes a database, writes a note, closes the
database, opens it again and reads the note back. It is
`examples/open`; run it with `go run ./examples/open`.

<!-- the program -->
```go
// Command open is the program of the manual's chapter "Opening a
// database" (docs/manual/opening-a-database.md): it makes a database
// file, writes a note into it, closes it, opens it again and reads the
// note back.
//
//	go run ./examples/open
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

// The class of the notes this program keeps.
const note = `{
  "name": "Note",
  "variables": {"text": {"type": "text", "required": true}},
  "states": {"initial": "kept", "list": ["kept"]},
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE"
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-open")
	if err == nil {
		defer os.RemoveAll(dir)
		err = run(os.Stdout, dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run keeps a note in a database under dir and reads it back after the
// database was closed and opened again.
func run(w io.Writer, dir string) error {
	ctx := context.Background()
	path := filepath.Join(dir, "notes.fado")

	// Open makes the file when it is not there.
	db, err := fado.Open(path, fado.Options{})
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "a new database has the classes", db.Classes())

	// Every call says who is calling. The program knows its callers; the
	// engine does not.
	dan := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	ola := fado.As(fado.Principal{ID: "ola", Kind: fado.ActorPerson})

	if _, err := db.DefineClass(ctx, []byte(note), dan); err != nil {
		return err
	}
	made, err := db.Create(ctx, "Note", fado.Values{"text": "buy milk"}, ola)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "ola wrote %q, version %d\n", made.Values["text"], made.Version)

	// Close finishes what is in progress and frees the file.
	if err := db.Close(); err != nil {
		return err
	}

	// The class and the note are in the file, and a second Open finds
	// them.
	db, err = fado.Open(path, fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Fprintln(w, "opened again, it has the classes", db.Classes())
	again, err := db.Get(ctx, "Note", made.ID, ola)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "ola reads %q, version %d\n", again.Values["text"], again.Version)
	return nil
}
```

It prints:

<!-- output of examples/open -->
```text
a new database has the classes [Substitution]
ola wrote "buy milk", version 1
opened again, it has the classes [Substitution Note]
ola reads "buy milk", version 1
```

## What opening does

- **It makes the file when it is not there.** A new file belongs to
  the user that made it: nobody else on the machine may read or write
  it. A file that exists keeps the permissions it has.
- **A new database is not empty.** It has the nine schemas the engine
  brings and one class, `Substitution`, which says who may act for
  whom; see [Let somebody act for a
  person](guides/acting-for-somebody.md). It has no tables.
- **It reads the definitions into memory**: every class, schema, table
  and action of the file.
- **It starts the worker**, which runs the actions that commits queued
  and takes the transitions whose time has come. What was queued while
  no program ran is still in the file, and runs now.
- **It names no caller.** A database is opened by a program, and each
  call says who is calling; see [Naming the caller](naming-the-caller.md).

## What is on disk

<!-- on disk -->
| File | What it is | It is there |
|---|---|---|
| `notes.fado` | the database | always |
| `notes.fado-wal` | the latest commits, before they are folded into the database | while a program has the database open |
| `notes.fado-shm` | an index of the file above | while a program has the database open |
| `notes.fado.blobs` | a directory with the bytes of attached files | when the database was created with the store `dir`; see [Files](files.md) |

The database is a SQLite file, and the two files beside it are
SQLite's. When the database is closed they are folded in and gone, and
the database is the one file.

## The options

An option is one of two kinds.

- **An option of the database** is written into the file when the
  database is created, and holds wherever the file is opened. A
  database behaves the same in every program.
- **An option of the program** is given at every open, and kept
  nowhere. It says what this program does for the database.

<!-- the options -->
| Option | What it sets | Whose | When none is given | Opened again with another value |
|---|---|---|---|---|
| `Synchronous` | how sure a commit is on disk: `NORMAL` or `FULL` | the database's | `NORMAL` | refused, `bad_value` |
| `MaxText` | the longest text a value may hold, in characters | the database's | 65,536 | refused, `bad_value` |
| `BlobStore` | where the bytes of files are kept: `sqlite`, `dir` or `external` | the database's | `sqlite` | refused, `bad_value` |
| `MaxBlob` | the largest file, in bytes | the database's | 32 MiB | a higher one is taken and kept; a lower one is refused, `bad_value` |
| `MaxIndexedRows` | up to how many rows a table is kept in memory for lookups | the database's | 100,000 | taken and kept |
| `Blobs` | the program's own store for the bytes of files | the program's | none | |
| `Outbox` | the worker: whether it runs, how many actions at once, how often one is tried again | the program's | it runs, 4 at once, 10 attempts | |
| `Actions` | the Go functions that carry out actions | the program's | none | |
| `Outbound` | which hosts an action may call | the program's | none: no call leaves the program | |
| `Secrets` | a function that gives the secret of a connection when a call needs it | the program's | none | |
| `HostAnswersFor` | the program itself keeps who may act for whom | the program's | the engine asks its substitutions | |
| `Now` | the clock of the database | the program's | the clock of the machine | |
| `WALLimit`, `CheckpointEvery` | when the latest commits are folded into the database | the program's | at 64 MiB, and every 100 ms | |

Where each is explained: `MaxIndexedRows` in
[Tables](tables.md#how-fast-a-lookup-is), `BlobStore`, `Blobs` and
`MaxBlob` in [Files](files.md), `Actions`, `Outbound` and `Secrets` in
[Actions](../actions.md), `HostAnswersFor` in [Let somebody act for a
person](guides/acting-for-somebody.md), `Now` in
[Time](notions/time.md).

### How sure a commit is

| `Synchronous` | A commit that was answered |
|---|---|
| `NORMAL` | is there after the program crashes. The last commits may be lost when the power fails or the machine stops |
| `FULL` | is on disk before it is answered. Commits are slower, as slow as the disk confirms a write |

The measurements behind the choice are in
[ADR-0035](../adr/0035-sqlite-is-the-transactional-store.md).

## One program holds a database

The engine is built for one program on a file. That program keeps the
classes, the tables and the actions in memory from the moment it
opened the database.

> **A second open is not refused.** Nothing stops another program, or
> the same program a second time, from opening a file that is open.
> Each then works from what it read when it opened: a class that one
> defines is unknown to the other until that one opens the database
> again. The engine promises nothing about two programs on one file.
> Let one program hold a database, and let the others ask that one.

That one program for many callers is the server, which is in
progress.

## Closing

- **Close waits for an action that is running**, stops the worker and
  frees the file.
- **What was queued and has not run stays in the file**, and runs when
  a program opens the database again.
- **Closing twice does no harm.**
- **A call after the close is refused.** A change is refused with
  `closed`.

## The clock

`Now` gives the database its clock. Every commit is stamped by it, and
`NOW()` and `TODAY()` read it in every formula. A program gives none,
and the clock is the machine's. A test gives its own, and so does a
program that rehearses next month in a copy of the file; see
[Time](notions/time.md#looking-at-another-day).

## What is refused

<!-- what is refused -->
| The program | Code |
|---|---|
| gives no path | `bad_value` |
| names a file in a directory that is not there | `storage` |
| names a file that is not a database | `storage` |
| gives a `Synchronous` that is neither `NORMAL` nor `FULL` | `bad_value` |
| gives a `MaxText` under 64 | `bad_value` |
| gives a `BlobStore` the engine does not know | `bad_value` |
| names the store `external` and gives no `Blobs` | `bad_value` |
| gives a function under `Actions` that is nil, or under a name no action can have | `bad_value` |
| opens a database with another `Synchronous`, `MaxText` or `BlobStore` than it has | `bad_value` |
| opens a database with a lower `MaxBlob` than it has | `bad_value` |
| opens a file that a newer engine made | `not_supported` |
| opens a file that an earlier engine made | `not_supported` |

Before version 1.0 a release may change how the file is laid out, and
there is no way to bring a file of an earlier release forward: it is
made again.

## Not here yet

- A lock that keeps a second program off an open file.
- A way to bring a file of an earlier release forward.
- The server, `fadodb serve`, which holds databases for many callers.
- Copying a database that is open. The chapter on running a database
  is planned.
