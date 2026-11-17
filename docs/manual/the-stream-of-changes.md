# The stream of changes

A caller that waits for something to happen can ask again and again.
An agent that approves invoices would send `query` every minute for
the invoices in `review`. The stream of changes is the other way: the
caller asks once to be told, and the database tells it of every event
it may read of, as each is written. Nothing is asked twice, and the
agent learns of an invoice within milliseconds of the commit.

The stream is the log, read forward. This chapter says how a stream is
opened, in a Go program and over HTTP, what it tells, who hears of
what, how a caller that was away comes back, and how a stream ends. A
test runs the program of this chapter, and another plays every table
and every reply of it against a server, on each push.

What the log is, and what an event carries, is in
[History and the log](notions/history-and-the-log.md).

## The program

An agent asks to be told of every invoice that enters `review`. Bob
writes two invoices and submits them, and the agent reads and approves
each as it is told. It is `examples/stream`; run it with
`go run ./examples/stream`.

<!-- the program -->
```go
// Command stream is the program of the manual's chapter "The stream of
// changes" (docs/manual/the-stream-of-changes.md): an agent asks to be
// told of every invoice that enters review, is told of each as it
// happens, reads it and approves it.
//
//	go run ./examples/stream
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

// An invoice that sales writes and finance approves. Its owner and
// finance may read it.
const invoice = `{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "net":    {"type": "money", "scale": 2},
    "owner":  {"type": "text", "default": "USER()"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "approved"]},
  "transitions": [
    {"name": "submit", "from": "draft", "to": "review"},
    {"name": "approve", "from": "review", "to": "approved"}
  ],
  "access": {
    "read":   "owner = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"submit": "owner = USER()", "approve": "INGROUP(\"finance\")"}
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-stream")
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
	db, err := fado.Open(filepath.Join(dir, "acme.fado"), fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	olga := fado.As(fado.Principal{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	bob := fado.As(fado.Principal{ID: "bob", Kind: fado.ActorPerson, Groups: []string{"sales"}})
	agent := fado.As(fado.Principal{ID: "ap-agent", Kind: fado.ActorAgent, Groups: []string{"finance"}})
	if _, err := db.DefineClass(ctx, []byte(invoice), olga); err != nil {
		return err
	}

	// The agent asks to be told of every invoice that enters review.
	// The stream begins now: it says what happens from here on.
	changes, err := db.Subscribe(ctx, fado.ChangeFilter{
		Classes:  []string{"Invoice"},
		Kinds:    []string{"transition"},
		ToStates: []string{"review"},
	}, agent)
	if err != nil {
		return err
	}
	defer changes.Close()

	// The agent waits. Next answers when there is an event to tell, and
	// not before: nobody asks again and again.
	done := make(chan error, 1)
	go func() {
		for handled := 0; handled < 2; handled++ {
			ev, err := changes.Next(ctx)
			if err != nil {
				done <- err
				return
			}
			fmt.Fprintf(w, "the agent is told: %s of %s by %s, from %s to %s\n", ev.Name, ev.DocID, ev.ActorID, ev.FromState, ev.ToState)
			// The event says what happened to which document, and
			// carries no value of it. The agent reads the document as
			// it stands now, with the rights it has now, and decides.
			doc, err := db.Get(ctx, ev.Class, ev.DocID, agent)
			if err != nil {
				done <- err
				return
			}
			if _, err := db.Transition(ctx, ev.Class, ev.DocID, "approve", nil, agent); err != nil {
				done <- err
				return
			}
			fmt.Fprintf(w, "the agent reads %s, net %v, and approves it\n", doc.ID, doc.Values["net"])
		}
		done <- nil
	}()

	// Bob writes two invoices and submits each. Writing one is no move
	// into review, so the agent is not told of it.
	for _, number := range []string{"FV/2026/11/0042", "FV/2026/11/0043"} {
		if _, err := db.Create(ctx, "Invoice", fado.Values{"number": number, "net": "1200.00 PLN"}, bob, fado.WithID(number)); err != nil {
			return err
		}
		if _, err := db.Transition(ctx, "Invoice", number, "submit", nil, bob); err != nil {
			return err
		}
	}
	if err := <-done; err != nil {
		return err
	}
	for _, number := range []string{"FV/2026/11/0042", "FV/2026/11/0043"} {
		doc, err := db.Get(ctx, "Invoice", number, bob)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s is %s, by %s\n", doc.ID, doc.State, doc.UpdatedBy)
	}
	return nil
}
```

It prints:

<!-- output of examples/stream -->
```text
the agent is told: submit of FV/2026/11/0042 by bob, from draft to review
the agent reads FV/2026/11/0042, net 1200.00 PLN, and approves it
the agent is told: submit of FV/2026/11/0043 by bob, from draft to review
the agent reads FV/2026/11/0043, net 1200.00 PLN, and approves it
FV/2026/11/0042 is approved, by ap-agent
FV/2026/11/0043 is approved, by ap-agent
```

Three calls make a stream in Go:

| Call | What it does |
|---|---|
| `db.Subscribe(ctx, filter, fado.As(caller))` | opens the stream for the caller, and answers it, a `*fado.Changes` |
| `changes.Next(ctx)` | waits for the next event the caller may hear of and answers it. It ends with the error of the context when the context ends, and with `closed` when the stream or the database was closed |
| `changes.Close()` | ends the stream |

## What a stream is asked for

The filter says which events the caller wants. What is left out lets
everything through.

| Member | In Go | What it is |
|---|---|---|
| `classes` | `Classes` | the classes to hear of; every class when left out |
| `kinds` | `Kinds` | the kinds of event: `created`, `set`, `transition`, `deleted`, `reset`, `refresh`, `migrated`, `action_run`, `action_failed`, `custom`; every kind when left out |
| `to_states` | `ToStates` | for a transition, the states entered; every state when left out. With it, only transitions are told |
| `after` | `After` | the id of an event: the stream begins after it. Left out, the stream begins now |

A stream that begins now says nothing of what happened before it was
opened. A filter on the values of a document, such as invoices over
10,000, is not there: the caller reads the document and decides.

## What an event says

An event of the stream is an event of the log, as the command `events`
answers it. This is the first one the agent was told:

<!-- an event -->
```jsonc
{"event_id":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE","class":"Invoice","doc_id":"FV/2026/11/0042","kind":"transition","name":"submit","from_state":"draft","to_state":"review","actor_id":"bob","actor_kind":"person","before_version":1,"after_version":2,"business_date":"2026-11-15","ts":1794733200,"origin":"command"}
```

It says what happened, to which document, who did it and when, and the
versions of the document before and after. It carries no value of the
document. The caller reads the document when it acts, as it stands
then and with the rights the caller has then: three commits may have
followed before the first event was read.

## Who hears of what

The read rule decides. **A caller is told of an event when it may read
the document as the event left it.** So a stream never tells what a
`get` would hide.

The invoice of this chapter is read by its owner and by finance. Four
callers each hold a stream of every event: Bob and Dan, who are in
sales; the agent, which is in finance; and Eve, who is in no group.

<!-- who hears -->
| What happens | Bob | Dan | The agent | Eve |
|---|---|---|---|---|
| Bob writes FV/2026/11/0044 | told | nothing | told | nothing |
| Bob submits it | told | nothing | told | nothing |
| The agent approves it | told | nothing | told | nothing |
| Dan writes FV/2026/11/0045 | nothing | told | told | nothing |
| Olga puts Eve in finance | nothing | nothing | nothing | nothing |
| Dan submits FV/2026/11/0045 | nothing | told | told | told |

- **A document a caller may not read is absent for it.** Eve is told
  nothing of the first four events, not even that an invoice exists.
- **A stream follows its caller.** Who the caller is, and in which
  groups, is asked again when there is an event to tell. Eve hears of
  what happens after she joined finance, and of nothing that passed.
- **Changing a user is no event of the log**, so nobody is told that
  Eve joined finance. Nor is defining a class.
- **A document that a change takes out of a caller's sight is not
  mentioned to it.** The change that hid it is not told, and nothing
  says that it went. For that caller it is as if it had never had a
  right to the document. A list that is kept up to date from the
  stream keeps such a row until the list is asked for again.
- **A delete is told to whoever could read the document just before
  it.**
- **A content administrator hears of every document when they give a
  reason** in the member `reason`, as they see every document with one;
  each batch told past the read rule is kept in the
  [log of reads](the-log-of-reads.md). Without a reason they hear what
  anybody in their groups hears.
- **A stream is heard as oneself.** A caller that says it acts for
  somebody is refused: who may act for whom is asked class by class,
  for a command.

## Over HTTP

A server answers the stream as a command, `subscribe`, whose reply
stays open. With the database of this chapter served by
[`fadodb serve`](serving-a-database.md), the agent sends, with its key:

<!-- the stream is asked for -->
```
POST /v1/subscribe
Authorization: Bearer fado_M4VA8BGP0Z_CDQ5…
Content-Type: application/json

{"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"]}
```

The reply has the status 200 and the kind `text/event-stream`, the form
of server-sent events, and does not end. Its first line says that the
stream is open. Bob then writes an invoice and submits it, and the
reply goes on:

<!-- the reply -->
```
: the stream is open

id: 01KA2F8Q7ZJ4M0X3T5V9B1C6DE
event: change
data: {"event_id":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE","class":"Invoice","doc_id":"FV/2026/11/0042","kind":"transition","name":"submit","from_state":"draft","to_state":"review","actor_id":"bob","actor_kind":"person","before_version":1,"after_version":2,"business_date":"2026-11-15","ts":1794733200,"origin":"command"}

```

| Line | What it is |
|---|---|
| a line that begins with `:` | a comment, which a reader of server-sent events passes over: the stream is open, or it is alive |
| `id:` | the id of the event. It is what a caller that comes back gives |
| `event: change` | the kind of the entry. Every event of the log is a `change`; the one other kind is `end`, the last entry of a stream the server ends |
| `data:` | the event, one JSON object on one line |
| an empty line | the end of the entry |

With `curl`, which shows each entry as it comes:

```
curl -N http://127.0.0.1:8080/v1/subscribe \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"]}'
```

The agent then reads the invoice and approves it, with two commands
like any other: `get` and `transition`.

## Coming back

A stream keeps one thing for its caller: the place in the log it has
reached. The server keeps no list of what a caller missed. A caller
that was away, because its connection broke or because it was stopped
for a night, says the last event it handled and is told everything
since, in order:

<!-- coming back -->
```
POST /v1/subscribe
Authorization: Bearer fado_M4VA8BGP0Z_CDQ5…
Content-Type: application/json

{"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"],
 "after": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"}
```

- **An event is told at least once, and in order.** A caller that
  records the id of the last event it handled, and gives it when it
  comes back, handles each event once.
- **The header `Last-Event-ID` says the same** as `after`. It is what a
  reader of server-sent events sends by itself when it connects again.
  When a request has both, the header counts.
- **The order is the order in which the events were written**, which
  is the order of the commits.
- **In Go** the place is `ChangeFilter.After`.

## A stream that says nothing

Every fifteen seconds without an event the server writes one comment
line, `: nothing new`, so that a proxy does not cut a quiet connection
and the caller knows a silent stream from a dead one.

## How a stream ends

The caller closes the connection, and that is all it has to do. When
the server ends a stream, it says why in a last entry of the kind
`end`, whose data is a refusal as a command answers one:

<!-- how a stream ends -->
| What happened | The code of the last entry | What the caller does |
|---|---|---|
| the key of the caller was revoked, or its user disabled | `unauthenticated` | asks whoever keeps the users for a key |
| the server stops | `closed` | opens the stream again when the server answers, with the last event it handled as `after`: nothing is lost |
| the stream failed in the engine | `internal` | opens the stream again, with `after`; the words of the failure are in the server's log |

The agent's key is revoked. Its stream ends before the next event,
which it is not told:

<!-- the key was revoked -->
```
event: end
data: {"error":{"code":"unauthenticated","message":"the request does not say who sends it, or says it with a credential the server does not know"}}

```

And the last entry of every stream when the server is told to stop:

<!-- the server stops -->
```
event: end
data: {"error":{"code":"closed","message":"the server stops, and its streams of changes end with it","hint":"open the stream again when the server answers, with the id of the last event handled as after: nothing is lost"}}

```

A key that is revoked ends a stream before its next event, and a
silent stream within fifteen seconds, when it would have shown that it
is alive.

## What is refused

A stream that is not opened is refused as a command is, with one JSON
object and no stream.

<!-- what is refused -->
| The object | Code | Why |
|---|---|---|
| `{"classes": ["Order"]}` | `unknown_class` | the database has no class Order |
| `{"kinds": ["approved"]}` | `bad_value` | `approved` is no kind of event |
| `{"to_states": ["shipped"]}` | `bad_value` | no class has a state `shipped`, so the stream could never say anything |
| `{"after": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"}` | `bad_value` | the log has no event with this id |
| `{"class": "Invoice"}` | `bad_json` | the member is `classes`, a list |
| `{"for": "olga"}` | `not_supported` | a stream is heard as oneself |

The command `subscribe` sent to `Exec`, which answers one object, is
refused with `not_supported`: a Go program calls `Subscribe`.

## What a server takes on

- **256 streams at once**, unless
  [the settings](serving-a-database.md#the-settings) say otherwise
  with `limits.streams`. One beyond them is refused with `busy`, at
  once: a stream stays open for as long as its caller listens, so
  there is nothing to wait for.
- **A stream holds one connection**, and takes no place among the
  commands that run.
- **A caller that takes nothing of what it is sent is cut** after
  thirty seconds, so that one that went away without a word holds no
  stream.
- **A proxy must pass the reply on as it comes.** The reply says so in
  the header `X-Accel-Buffering: no`, which nginx reads; another
  proxy is told in its own settings not to collect the reply.
- **How many streams are open** is among
  [the counts](serving-a-database.md#its-counts), as `fadodb_streams`.
  A stream has its line in the log of the server when it ended:

<!-- the line of a stream -->
```jsonc
{"time":"2026-11-15T09:00:00.000Z","command":"subscribe","user":"ap-agent","status":200,"code":"closed","ms":0.072,"open_s":3600.000,"events":2}
```

  `ms` is the time until the stream was open, `open_s` for how many
  seconds it stayed open and `events` how many events it told. `code`
  is the code the server ended it with, and is not there when the
  caller closed it.

## In a server of your own

A program that mounts the door, `fado/httpdoor`, answers the stream at
the same address. Two things are the program's:

- **Ending the streams when it stops.** A stream does not end by
  itself, so a program that waits for its requests to end would wait
  for the streams. It tells the door to end them first, each with its
  last entry:

  ```go
  srv := &http.Server{Handler: door}
  srv.RegisterOnShutdown(door.EndStreams)
  ```

- **Who the caller is now.** The door asks the program's `Who` again
  when there is an event to tell, as it asked when the stream was
  opened. A `Who` that answers an error ends the stream with
  `unauthenticated`, and one that answers other groups is believed
  from that event on.

A Go program that reads a stream itself for callers of its own does
the same with `changes.Follow`: it gives the stream the function that
says who the caller is now.

## What it costs

- **A commit does nothing for the streams** but leave a mark that the
  log grew. It allocates the same with streams open as with none.
- **The streams read the log once, together.** After a commit the
  first stream to wake reads the new events, and the others take them
  from it, so a hundred streams ask the database what one asks.
- **How soon.** On one machine, with a writer that commits every
  millisecond, a subscriber had read the entry of a commit 0.25 ms
  after the commit ended at the median and 0.84 ms at p99; with a
  hundred subscribers, 1.09 ms and 3.12 ms. The budget is 5 ms at
  p99. [ADR-0058](../adr/0058-the-stream-of-changes.md) has the
  measurement, and what a writer that never pauses makes of it.

## What the stream does not say

- **The document.** An event says which document changed and carries
  none of its values.
- **Changes of definitions and of users.** They are not events of the
  log.
- **That a document went out of sight.**
