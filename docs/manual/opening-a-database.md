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
| `notes.fado.lock` | says which program holds the database alone, and is empty when none does | once a program has held the database alone |
| `notes.fado.duckdb` | a copy of the documents, kept for the questions that read everything | when the program gave an analytical engine; see [An analytical engine](#an-analytical-engine) |

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
| `MaxQuestion` | how long a `query` or a `count` that no index serves may run before it is stopped | the database's | 5 seconds | taken and kept |
| `Blobs` | the program's own store for the bytes of files | the program's | none | |
| `Outbox` | the worker: whether it runs, how many actions at once, how often one is tried again; see [The outbox and the worker](the-outbox-and-the-worker.md#the-options-of-the-worker) | the program's | it runs, 4 at once, 10 attempts | |
| `Actions` | the Go functions that carry out actions | the program's | none | |
| `Outbound` | which hosts an action may call | the program's | none: no call leaves the program | |
| `Secrets` | a function that gives the secret of a connection when a call needs it | the program's | none | |
| `HostAnswersFor` | the program itself keeps who may act for whom | the program's | the engine asks its substitutions | |
| `Users` | the database keeps its own users, groups and keys, and answers who a key belongs to | the program's | the program says itself who is calling, and the database keeps no users | |
| `Inbound` | the program takes messages from outside at the inlets of the database (ADR-0065) | the program's | it takes none: an inlet cannot be defined, and a message is answered as one for a name that is no inlet | |
| `Alone` | the program holds the database alone until it closes it, under the name it gives here | the program's | the program holds nothing | |
| `Analytics` | the analytical engine of the program, which keeps a copy of the documents; see [An analytical engine](#an-analytical-engine) | the program's | none: the database keeps no copy | |
| `CopyEvery` | how often that copy is fed without being asked | the program's | every 5 seconds | |
| `Now` | the clock of the database | the program's | the clock of the machine | |
| `WALLimit`, `CheckpointEvery` | when the latest commits are folded into the database | the program's | at 64 MiB, and every 100 ms | |

Where each is explained: `MaxIndexedRows` in
[Tables](tables.md#how-fast-a-lookup-is), `MaxQuestion` in
[Lists of other documents](lists.md#a-large-class), `BlobStore`, `Blobs` and
`MaxBlob` in [Files](files.md), `Actions`, `Outbound` and `Secrets` in
[Actions](../actions.md), `HostAnswersFor` in [Let somebody act for a
person](guides/acting-for-somebody.md), `Users` in
[The commands](commands.md#users-groups-and-keys), `Alone` in
[One program holds a database](#one-program-holds-a-database), `Now`
in [Time](notions/time.md).

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

> **A second open is not refused, unless a program holds the database
> alone.** Nothing else stops another program, or the same program a
> second time, from opening a file that is open. Each then works from
> what it read when it opened: a class that one defines is unknown to
> the other until that one opens the database again. The engine
> promises nothing about two programs on one file. Let one program hold
> a database, and let the others ask that one.

A program holds a database alone by opening it with `Alone`, which is
the name the program goes by, such as `"fadodb serve"`. While it has
the database open, every other program that opens it is refused with
`in_use` and told that name. The hold ends when the program closes
the database, and by itself when the program ends, however it ends.

A program that opens a database without `Alone` holds nothing. It is
refused a database that another holds alone, and a program that takes
the database alone after it does not see it.

That one program for many callers is the server:
[Serving a database](serving-a-database.md).

## An analytical engine

A question that names what it wants, the invoices of one order, is
answered by an index. A question that reads everything, how much is
invoiced by customer, is slow in a database built for the first kind.
For that the engine can keep a copy of the documents in DuckDB, an
engine built for counting (ADR-0017, ADR-0089).

The core is pure Go and has no DuckDB. A program that wants the copy is
built with the module `analytics`, which needs a C compiler, and gives
its engine when it opens the database:

```go
db, err := fado.Open("notes.fado", fado.Options{Analytics: analytics.Engine{}})
```

- **The database feeds the copy itself**, every `CopyEvery` and when it
  closes. `FeedCopy` feeds it at once.
- **The copy holds**, for every class, the documents as they are now
  and every version of them, the events, and the moves of the
  documents that exist: each time one was made or moved along a
  transition. The lines of a document are not in it.
- **The copy is never the truth.** It is the file `notes.fado.duckdb`
  beside the database. It needs no backup: when it is lost, or does not
  open, or belongs to another database, the engine makes it again by
  itself.
- **A copy that cannot be kept stops nothing.** The database works
  without it, and `CopyState` says why the last feed failed.
- **One opening feeds a copy.** DuckDB lets one program write a file,
  and inside one program the engine gives the copy to the first
  opening of the database. A second opening works without a copy, and
  `CopyState` says so.

**What the copy answers.** Nothing is written differently for it: the
engine sends a question to the copy by itself, and neither a designer
nor a caller chooses.

| The question | The copy answers it |
|---|---|
| the flow of a class, the command [`flow`](commands.md#flow) and the call `Flow` | always |
| how many documents a condition takes, the command [`count`](commands.md#count) and the call `Count` | at once when no index narrows it; otherwise when the store has not answered in a moment |
| a total, or the first document, that a `LOOKUP` in a live variable asks for: `LOOKUP(Invoice, SUM(net), urgent)` | the same |
| a figure over a list, `SUM(invoices.net)`, `COUNT(invoices)` | the same |
| a page of a list, the command [`query`](commands.md#query) and a live list | at once when the store would read and sort the class for it; otherwise when the store has not found the page in a moment |

**An index narrows a question** when its condition compares an indexed
variable with a value: the invoices of one order, the documents in one
state, the lookup keyed by a customer. So does the read rule of a
reader who reads their own, `owner = USER()`. Such a question names
what it wants, and the store answers it in a moment, as ever. A
question that no index narrows reads the class, and that is what the
copy is for.

An index may narrow a question to very many documents all the same:
the invoices in one state may be most of the class. So a question an
index narrows is the store's for a moment, five milliseconds, and the
copy's when the moment has passed.

- **An answer is never older than its question.** Before it answers,
  the copy is brought up to the database. When nothing was written
  since the last look, the look costs nothing to speak of; after a
  write the new rows are copied first, some twenty milliseconds, once.
- **The answer is the store's**, for the same reader. The statement is
  the same one, written by the same compiler in the copy's dialect,
  with the read rule and the read rights in it.
- **When the copy does not answer, the store does**, and `CopyState`
  says why: the copy cannot run the statement, or it is still being
  made, or it is a long load behind. A question never waits for a copy
  that is being made.

Measured among 3,400,000 invoices, with a reader who reads them all:

| The question | A program without the copy | With the copy |
|---|---|---|
| the sum of every invoice, in a `LOOKUP` of a live variable | 460 ms | 7 ms |
| how many invoices are over an amount, the command `count` | 275 ms | 2 ms |
| how many invoices one customer has, where `customer` has no index | 240 ms | 1.5 ms |
| how many different owners the invoices have | 285 ms | 1.5 ms |
| how many invoices are in review: the state has an index | 4 ms | 4 ms, from the store |

The first of such questions after the file was last read from the
disk takes a program without the copy seconds, and a question that no
index serves is stopped after five (see
[A large class](lists.md#a-large-class)). The flow of that class, ten
million moves, takes the store 18 seconds and the copy 19 ms: the copy
keeps the moves ready, and the flow is one sum over them.

**A page of a list** is found in the copy and read from the store.
The copy says which documents the question takes, in its order, and
the store hands over those documents, as many as the page has. A page
the store can fill at once, the invoices of one order or the first
fifty that meet a condition most documents meet, is the store's: it
is given a moment, five milliseconds, and the copy is asked only when
the moment has passed. A page that no index serves and that is sorted
by a variable without an index would have the store read and sort
every document it takes, so the copy is asked for it at once.

Measured among the same 3,400,000 invoices:

| The page | A program without the copy | With the copy |
|---|---|---|
| `query`: the fifty dearest invoices | 318 ms | 8 ms |
| `query`: the fifty dearest invoices of one customer, where `customer` has no index | 309 ms | 6 ms |
| a live list: the fifty dearest invoices | 417 ms | 14 ms |
| a live list: the fifty dearest invoices of one owner, whom an index narrows to a million | 278 ms | 11 ms |
| `query`: fifty invoices over an amount, as they come | 0.2 ms | 0.2 ms, from the store |
| `query`: the newest fifty invoices; an index holds the order | 0.9 ms | 0.9 ms, from the store |

At ten times that size, 34,000,000 invoices that made a hundred
million moves, the copy answers each of these questions in under a
tenth of a second, and the flow of the class in 158 ms, where the
store takes seconds to minutes.

**What stays with the store:** everything inside a commit, so a value
that a triggered variable keeps is worked out by the store.

The program `fadodb-core` is built without the module; the same
program built with it, `fadodb`, serves a database with its copy
([A server with the analytical engine](serving-a-database.md#a-server-with-the-analytical-engine)).

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
| opens a database that another program holds alone | `in_use` |
| opens a file that a newer engine made | `not_supported` |
| opens a file that an engine before v0.7.0 made | `not_supported` |

Before version 1.0 a release may change how the file is laid out. A
database that v0.7.0 or a later release made is brought to the layout
of the engine that opens it, in the one transaction that opens it:
what it holds stays as it was. The engine that made it no longer opens
it afterwards, so keep a copy of the file if you may go back. A file of
a release before v0.7.0 cannot be brought forward: it is made again.

A newer engine may keep indexes the earlier one did not. They are made
right after that transaction, before the database answers anything, so
the first opening of a large database takes longer than the others. A
program that is stopped meanwhile loses nothing: the next opening
makes what is missing.

## Not here yet

- A hold for every program. One that opens a database without `Alone`
  holds nothing, so two such programs on one file are not kept apart.
- A way to bring a file of an earlier release forward.
- A backup of an open database that the engine makes itself.
  [Running it](running-it.md#a-database-that-does-not-stop) says what
  can be done today.
