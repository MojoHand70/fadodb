# Naming the caller

FadoDB keeps no users and checks no passwords. The program that holds
the engine knows who is calling, from its own login, a token or a
queue, and says so on every call. The engine trusts the program for
that, and decides everything else itself. This chapter is for the
developer of that program: what it says about a caller, what the
engine does with each part, what the engine checks, and what stays
the program's to guard. A test runs the program of this chapter and
plays every table of it on each push.

What a class makes of the caller is in [Who may](notions/who-may.md).

## The program

Four callers work on one invoice. It is `examples/caller`; run it with
`go run ./examples/caller`.

<!-- the program -->
```go
// Command caller is the program of the manual's chapter "Naming the
// caller" (docs/manual/naming-the-caller.md): four callers work on one
// invoice, and the program prints what the engine answered each of them
// and what the log kept.
//
//	go run ./examples/caller
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

// An invoice that sales creates and accounting books. Booking is kept
// for a person.
const invoice = `{
  "name": "Invoice",
  "variables": {
    "number":  {"type": "text", "required": true},
    "account": {"type": "text"}
  },
  "states": {"initial": "open", "list": ["open", "booked"], "final": ["booked"]},
  "transitions": [
    {"name": "book", "from": "open", "to": "booked", "guard": "NOT ISBLANK(account)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"accounting\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"accounting\")",
    "delete": "FALSE",
    "transition": {"book": "INGROUP(\"accounting\") AND USERKIND() = \"person\""}
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-caller")
	if err == nil {
		defer os.RemoveAll(dir)
		err = run(os.Stdout, dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run plays the story of the chapter in a database under dir.
func run(w io.Writer, dir string) error {
	ctx := context.Background()
	db, err := fado.Open(filepath.Join(dir, "books.fado"), fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	// The program knows its callers: from its own login, a token, a
	// queue. Here they are written down.
	dan := fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}}
	ola := fado.Principal{ID: "ola", Kind: fado.ActorPerson, Groups: []string{"sales"}}
	bot := fado.Principal{ID: "ledger-bot", Kind: fado.ActorAgent, Groups: []string{"accounting"}}
	anna := fado.Principal{ID: "anna", Kind: fado.ActorPerson, Groups: []string{"accounting"}}

	// Defining is work of the system group designers.
	if _, err := db.DefineClass(ctx, []byte(invoice), fado.As(dan)); err != nil {
		return err
	}

	// said prints what the engine answered a call.
	said := func(what string, err error) {
		answer := "allowed"
		if err != nil {
			answer = "refused, " + fado.ErrorCode(err)
		}
		fmt.Fprintf(w, "%-36s %s\n", what+":", answer)
	}

	// Each call says who is calling, with As.
	made, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/031"}, fado.As(ola))
	said("ola creates an invoice", err)
	if err != nil {
		return err
	}
	id := made.ID

	// A call with no As is nobody's.
	_, err = db.Get(ctx, "Invoice", id)
	said("a call that names nobody reads it", err)

	_, err = db.Set(ctx, "Invoice", id, fado.Values{"account": "4010"}, fado.As(bot))
	said("ledger-bot sets the account", err)
	_, err = db.Transition(ctx, "Invoice", id, "book", nil, fado.As(bot))
	said("ledger-bot books it", err)
	_, err = db.Transition(ctx, "Invoice", id, "book", nil, fado.As(anna))
	said("anna books it", err)

	// The log keeps who did each thing, and what they were.
	events, err := db.Events(ctx, "Invoice", id, fado.As(anna))
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "the log:")
	for _, event := range events {
		fmt.Fprintf(w, "  %-10s by %s, %s\n", event.Kind, event.ActorID, event.ActorKind)
	}
	return nil
}
```

It prints:

<!-- output of examples/caller -->
```text
ola creates an invoice:              allowed
a call that names nobody reads it:   refused, unknown_document
ledger-bot sets the account:         allowed
ledger-bot books it:                 refused, forbidden
anna books it:                       allowed
the log:
  created    by ola, person
  set        by ledger-bot, agent
  transition by anna, person
```

## What the program says about a caller

A caller is a `Principal`.

<!-- the principal -->
| Part | The program puts there | The engine uses it for | The engine checks |
|---|---|---|---|
| `ID` | the caller's id, as the program knows it | `USER()` and `CREATEDBY()` in a formula; the name on every event and on every version | nothing: an id is any text |
| `Groups` | the caller's groups, as the program knows them | `INGROUP("accounting")`, and the lists of groups under `rights` | that none has the name of a system group, or is `everyone` |
| `System` | the system groups the caller is in | the work no class gives: defining, running the queue, SQL, acting past a rule | that each is one of the four |
| `Kind` | `person`, `agent` or `service`, or nothing | `USERKIND()`; kept on every event | that it is one of the three, or empty |
| `For` | the id of whoever the caller acts for, or nothing | `ACTINGFOR()`; kept on every event | that a substitution of the database says so |
| `Today` | the caller's business day, or nothing | the business day of the caller's commits | nothing |

- **An id is any text**, and the engine never looks inside it. Use the
  id your program already has for the caller, and keep it stable: it
  is what the log shows for ever.
- **Groups are compared as they are written.** `SALES` is not `sales`.
- **Every caller with an id is in the group `everyone`.** The engine
  adds it; the program does not name it.
- **The kind is the program's word.** The engine cannot tell a person
  from an agent. A class can keep a step for a person, as `book` does,
  and that holds as far as the program says truthfully what a caller
  is.

## How a call names its caller

Every call of the package takes options after its own arguments, and
`fado.As(principal)` is the one that names the caller. A command, run
with `Exec`, takes the principal as an argument of its own.

- **A call with no `As` is nobody's.**
- **Nobody passes only a line that says `TRUE`.** Nobody has no id, is
  in no group, and is not in `everyone`.
- **The last `As` of a call counts**, when a call is given two.

> **Give every caller an id.** The engine takes a caller that has
> groups and no id. Such a caller is in its groups and passes the
> lines that name them, and the log has no name for what it did. The
> engine does not refuse it today.

## The program is the only door

The engine has no login. Whoever can call the package can name any
caller, a designer and a content administrator included. So:

- **Establish the caller yourself**, from what your program trusts: a
  session, a verified token, the account a job runs under.
- **Never pass on what the caller sent as who the caller is.** An id,
  a group or a kind taken from the body of a request lets the sender
  choose who they are.
- **Name the system groups with care.** Who is a designer or a content
  administrator is said by the program on each call and stored
  nowhere in the file. Keep that list as you keep the list of your own
  administrators.
- **Guard the file.** A new database file is readable by the user that
  made it and by nobody else. Whoever can read the file can read
  everything in it, past every rule.

The engine's own users and keys come with the server, which is in
progress.

## The system groups

<!-- the system groups -->
| Constant | Name | Its members |
|---|---|---|
| `GroupDesigners` | `designers` | define classes, schemas, tables and actions |
| `GroupAdministrators` | `administrators` | run the queue of actions and check the files |
| `GroupAnalysts` | `analysts` | run SQL, which reads every document |
| `GroupContentAdministrators` | `content-administrators` | see every document and act on it past its rules, with a reason |

The program gives them in `System`, apart from `Groups`. A group of
the program's own that happens to be called `administrators` then
gives nothing: under `Groups` the name is refused. What each group may
and may not do is in [Who may](notions/who-may.md#the-people-who-run-the-database).

## Acting for somebody

A caller that acts for another person says so in `For`. The engine
lets the call through only while a substitution of the database says
that person agreed, and writes both names on the event. A program that
keeps such agreements itself opens the database with the option
`HostAnswersFor`, and the engine then takes `For` as told. The guide
[Let somebody act for a person](guides/acting-for-somebody.md) plays
both.

## The business day

Every commit has a business day beside its time. It is, in this order:

<!-- the business day -->
| The business day is | When |
|---|---|
| the day the call names, with the option `BusinessDate` | the call names one |
| the caller's `Today` | the call names none, and the caller has one |
| the day of the database's clock | neither is given |

## The options of a call

<!-- the options of a call -->
| Option | Says | For |
|---|---|---|
| `As` | who is calling | every call |
| `Key` | the key of a call that may be sent twice; see [History and the log](notions/history-and-the-log.md#sending-a-call-twice) | a call that changes something |
| `Reason` | why a content administrator acts past a rule | a call that changes something |
| `BusinessDate` | the business day of the commit | a call that changes something |
| `WithID` | the id of a new document, when the caller gives one | a create |
| `At` | the moment a read or a preview is worked out for; see [Time](notions/time.md#looking-at-another-day) | a read or a preview |
| `AtVersion` | the version of the document a file is opened from; see [Files](files.md#opening) | opening a file |
| `Replace` | that a table is emptied before the rows are written; see [Tables](tables.md#rows) | an import |

## What is refused

<!-- what is refused -->
| The program | Code | The refusal names |
|---|---|---|
| gives a kind that is none of the three | `bad_value` | |
| puts a name under `System` that is none of the four, `everyone` included | `bad_value` | |
| puts the name of a system group, or `everyone`, under `Groups` | `bad_value` | |
| names somebody under `For`, and no substitution says so | `forbidden` | `/principal/for` |

A caller that is refused this way is refused on every call, a read
included, before the engine looks at the document.

## Not here yet

- The engine's own users, groups and keys. They come with the server.
- A refusal for a caller that has groups and no id.
- A record of what a caller read. No read is written down today.
