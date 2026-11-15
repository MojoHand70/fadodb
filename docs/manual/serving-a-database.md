# Serving a database

A program of your own reaches a database by opening it. Everybody
else reaches it through a server: `fadodb serve` holds one database
and answers many callers over HTTP, each of whom proves who it is with
a key. This chapter is the reference for the server and for the three
commands that come before it. A test plays what stands here on each
push.

The server is being built in parts. What is here works; what is not
is listed at the end.

## From nothing to a server

Four commands, on the machine that will hold the database.

```
fadodb init acme.fado
fadodb user add olga --kind person --system administrators --db acme.fado
fadodb key new olga --label laptop --db acme.fado
fadodb serve acme.fado
```

| Command | What it does |
|---|---|
| `fadodb init acme.fado` | makes an empty database, closed to every user of the machine but the one who made it. A database is made by this command and by no other: the server refuses a file that is not there |
| `fadodb user add olga …` | makes the first user, Olga, a person among the administrators |
| `fadodb key new olga …` | makes a key for Olga and prints it, once. The database keeps a hash of the key and cannot show it again; a key that is lost is replaced |
| `fadodb serve acme.fado` | serves the database, on this machine only, at `127.0.0.1:8080` |

The second and the third need no key. Whoever holds the database on
their own machine already holds everything in it, and the database
records what they change as made on the machine, by no user. They are
how the first administrator comes to be, and the way back in when
every administrator has lost their key.

From here on Olga keeps the users through the server, with her key:
`create_user`, `create_key` and the other commands of
[Users, groups and keys](commands.md#users-groups-and-keys).

## Calling it

A caller sends a command to the address that ends in its name, with
its key:

```
POST http://127.0.0.1:8080/v1/whoami
Authorization: Bearer fado_7GQ2M4XK0P_3ZD9…
Content-Type: application/json

{}
```

```jsonc
{"user": {"id": "olga", "kind": "person", "name": "Olga Nowak",
          "groups": [], "system_groups": ["administrators"], "disabled": false}}
```

[The commands](commands.md) lists every command, how a command is sent
[over HTTP](commands.md#over-http), and the addresses of
[the files](commands.md#files-over-http). A request without a key the
database knows is refused with `unauthenticated`, and nothing of it is
read.

## Where it listens

| Flag | What it says | When it is not given |
|---|---|---|
| `--listen ADDR` | where the server listens, such as `127.0.0.1:8080` or `:8443` | `127.0.0.1:8080`, which only this machine reaches |
| `--tls-cert FILE`, `--tls-key FILE` | the certificate the server shows, and its key | the server sends in the clear |
| `--plain-http` | the server may listen beyond this machine without a certificate, because something in front of it encrypts: a proxy, or the network of a container | it may not |
| `--settings FILE` | [the settings](#the-settings): what the actions of the database may call, and how much the server takes on | the actions call no host, and the limits are the usual ones |
| `--secrets-dir DIR` | the directory that holds [the secrets](#the-secrets) of the connections | a secret is read from the environment |

- **A key never travels in the clear by accident.** A key goes with
  every request. So the server refuses to start when it would listen
  beyond this machine without a certificate, unless `--plain-http`
  says that it may. Leaving a flag out never puts a key on the
  network.
- **Told nothing, it is safe**: this machine only.

## The settings

What does not fit a flag is in one JSON file that the operator writes,
the person who runs the machine. The server is given it with
`--settings`:

```
fadodb serve acme.fado --settings settings.json --secrets-dir /run/secrets/fadodb
```

<!-- the settings -->
```jsonc
{
  "outbound": {
    "hosts": ["hooks.acme.example"],
    "connections": {
      "slack": ["slack.com"],
      "books": ["books.acme.internal:8443"]
    }
  },
  "limits": {
    "request_bytes": 16777216,
    "command_seconds": 30,
    "commands": 64,
    "streams": 256
  }
}
```

<!-- the members of the settings -->
| Member | What it says | When it is not given |
|---|---|---|
| `outbound.hosts` | the hosts a request without a connection may go to | none |
| `outbound.connections` | the connections a definition may name, each with the hosts its requests, and so its secrets, may go to | none |
| `limits.request_bytes` | the largest request, in bytes | 16777216, which is 16 MiB |
| `limits.command_seconds` | how long one command may run, in seconds; it may have a fraction | 30 |
| `limits.commands` | how many commands run at once | 64 |
| `limits.streams` | how many streams of changes are open at once. The stream is not built yet, so it is read and limits nothing today | 256 |

- **The settings are the operator's.** Where the server listens, what
  it may call and how much it takes on are decided by whoever runs the
  machine. None of it is kept in the database and no command changes
  it: Olga, an administrator of the database, cannot widen what the
  owner of the machine allowed.
- **A mistake is found when the server starts.** A member the file
  should not have, a member written twice, a limit that is no number
  above zero, a host that is none: the server names the file and what
  is wrong in it, and does not start.
- **The file is read once.** A change to it takes a restart. A secret
  does not.
- **A secret is never in the settings.**

## What its actions may call

Told nothing, the actions of a served database call no host. An action
that sends a request cannot be defined. One that came with the
database is not sent: it is tried again later, and after the last
attempt it is dead, with `outbound_off`.

The settings list the hosts. An entry is one of:

| Entry | What it allows |
|---|---|
| `slack.com` | that host, over https, on port 443 |
| `.slack.com` | every host under `slack.com`, and not `slack.com` itself |
| `books.acme.internal:8443` | that host on that port |
| `http://localhost:8080` | that host over http, not https |

- **A connection's secrets go only to that connection's hosts.** A
  definition that names the connection `books` sends its request, and
  the secrets of `books`, to the hosts listed for `books` and to no
  other. A request without a connection reads no secret and goes only
  to `outbound.hosts`.
- **A host inside the network is listed by its own name.** A host that
  only an entry such as `.slack.com` admits is refused when its name
  leads to an address inside the network.
- **A redirect is not followed**: it is the reply. A call has ten
  seconds, and its reply one megabyte.
- **What the settings do not list is refused when it is defined**, so
  an agent learns it at once: `unknown_connection` for a connection the
  settings do not have, `not_allowed` for an address outside the lists,
  and `not_supported` on a server whose settings list no host at all.

### An action that calls the books

Acme keeps its books in a service of its own, at
`books.acme.internal:8443`. When an order is sent, the database should
post an entry there and keep the number the books answer.

The operator lists the host of the books for a connection called
`books`:

<!-- the settings of the case -->
```jsonc
{
  "outbound": {
    "connections": {
      "books": ["books.acme.internal:8443"]
    }
  }
}
```

and puts the token of the books into the directory of secrets, as the
file `books/token`. Then Olga, with her key, defines the action. This
is the `definition` she sends with the command `define_action`:

<!-- the action -->
```jsonc
{
  "name": "books.post",
  "label": "Post an entry to the books",
  "params": {
    "amount": {"type": "number", "required": true}
  },
  "returns": {
    "entry": "text"
  },
  "connection": {"name": "books", "secrets": ["token"]},
  "request": {
    "method": "POST",
    "url": "\"https://books.acme.internal:8443/entries\"",
    "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
    "body": {"amount": "amount"}
  },
  "result": {"entry": "JSONVALUE(BODY, \"entry\")"}
}
```

The definition names the connection and the secret, and never the
token itself. The class asks for the action in a rule:

<!-- the class -->
```jsonc
{
  "name": "Order",
  "variables": {
    "amount": {"type": "number", "required": true},
    "entry":  {"type": "text"}
  },
  "states": {"initial": "new", "list": ["new", "sent"]},
  "transitions": [
    {"name": "send", "from": "new", "to": "sent"}
  ],
  "rules": [
    {"on": "transition", "name": "send", "do": [
      {"action": "books.post", "args": {"amount": "amount"}, "into": {"entry": "entry"}}
    ]}
  ],
  "access": {
    "read":   "INGROUP(\"sales\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"sales\")",
    "delete": "FALSE"
  }
}
```

The agent `sales-agent`, of the group `sales`, makes the order `o-1`,
of 250, and sends it. The commit queues the action,
the server sends `{"amount":250}` to the books with the header
`Authorization: Bearer` and the token, the books answer
`{"entry": "E-1"}`, and the server writes `E-1` into the order's
`entry`. [The outbox and the worker](the-outbox-and-the-worker.md) says
what happens when the books give no answer;
[docs/actions.md](../actions.md) says how a definition is written for
any service.

## The secrets

A secret, such as the token of the books, is never in the settings, in
a definition or on the command line. The server finds the secret
`token` of the connection `books` in one of two places:

<!-- where a secret is -->
| Place | Where |
|---|---|
| a file | `books/token` in the directory given with `--secrets-dir` |
| the environment | the variable `FADODB_SECRET_BOOKS__TOKEN` |

- **The file is looked for first**, then the environment.
- **A secret is read again for every request.** The operator replaces
  the file, and the next request carries the new token. The server is
  not restarted.
- **The names are those of the definition**, in small letters for the
  directory and the file, in capitals for the variable. The connection
  and the secret are joined by two underscores, which no name holds,
  so the variable of one connection is never the variable of another.
- **A file is read without the end of its last line**, and holds at
  most 64 KiB.
- **A secret that is in neither place is no answer.** The action is
  tried again later, like one whose service gives no answer, with the
  code `secret_unavailable` and words that name the file and the
  variable. After the last attempt it is dead, and an administrator
  sends it again once the secret is there.
- **A secret is in no line the server writes**, and in no answer: a
  preview of a request shows a mask where a secret goes.

## Actions that need a Go function

An action is the engine's own, a request the server sends, or a Go
function of a program. `fadodb serve` is a finished program and runs
no Go function.

- **A definition with no request is refused when it is defined**, with
  `not_registered`: it is the contract of a Go function, and the server
  has none. The refusal says the two ways out: give the definition a
  request, or serve the database from a program of your own.
- **A database that such a program kept** may hold actions of its Go
  functions. The server says which they are when it starts. They do
  not run on it: each is tried again later with `not_registered`, and
  is dead after the last attempt.

### A server of your own

A company that needs its own Go function writes a small program: the
door, in front of a database it opens with the function. It is
`examples/server`, and it is what `fadodb serve` does with one thing
added.

<!-- the program -->
```go
// Command server is the program of the manual's chapter "Serving a
// database" (docs/manual/serving-a-database.md), section "A server of
// your own": what fadodb serve does, with one thing added, an action
// that a Go function of the company runs. fadodb serve runs no Go
// function; a program like this one does.
//
//	fadodb init acme.fado
//	fadodb user add olga --kind person --system administrators --system designers --db acme.fado
//	fadodb key new olga --label laptop --db acme.fado
//	go run ./examples/server acme.fado
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: server FILE, the database that fadodb init made")
		os.Exit(2)
	}
	// On this machine only: a key travels with every request, and this
	// program has no certificate.
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		err = serve(ctx, os.Args[1], ln, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve answers the callers of the database at the path, each by the
// key of a user of the database, until ctx ends.
func serve(ctx context.Context, path string, ln net.Listener, w io.Writer) error {
	db, err := fado.Open(path, fado.Options{
		// The database keeps its users and their keys, and this program
		// holds it alone, as a server must.
		Users: true,
		Alone: "the server of Acme",
		// The one thing added: the Go function behind the action
		// mail.send. A definition of that name, with no request, is
		// taken here and refused by fadodb serve.
		Actions: map[string]fado.ActionFunc{
			"mail.send": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
				fmt.Fprintf(w, "a mail goes to %v\n", call.Args["to"])
				return nil, nil
			},
		},
	})
	if err != nil {
		return err
	}
	defer db.Close()

	// The door: every command of the database over HTTP, for whoever
	// sends a key.
	door, err := httpdoor.New(db, httpdoor.Options{Who: httpdoor.Keys(db)})
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: door, ReadHeaderTimeout: 10 * time.Second}
	ended := make(chan error, 1)
	go func() { ended <- srv.Serve(ln) }()
	select {
	case err := <-ended:
		return err
	case <-ctx.Done():
	}
	// Told to stop: the commands that run end first, then the database
	// is closed.
	within, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(within); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
```

The command `define_action` with the definition
`{"name": "mail.send", "params": {"to": "text"}}` is taken by this
program and refused by `fadodb serve`.
[The commands over HTTP](commands.md#over-http) says what the door
answers; [Opening a database](opening-a-database.md) lists what a
program may say when it opens one.

## What it says when it starts

To standard error, before the first request:

<!-- what it says when it starts -->
```
fadodb serve: serving acme.fado at http://127.0.0.1:8080
fadodb serve: its actions may call over the connection books, books.acme.internal:8443
```

and a line for everything the database needs that this server does not
have as it was started: an action of a Go function, an action whose
connection the settings do not have, a secret that is in neither
place, a database that has no user yet.

## One program holds the database

While it serves, the server holds its database alone. It keeps in
memory what it has read of the database, its classes and its users,
so a second program that wrote to the same file would change it behind
the server's back.

- A second `fadodb serve`, `fadodb user add`, `fadodb key new` and
  `fadodb check` on a served database are refused, with `in_use`, and
  told which program holds it and since when.
- The hold ends when the server stops, and by itself when the server
  dies.
- A served database is reached through its server, with a key.

[Opening a database](opening-a-database.md#one-program-holds-a-database)
says how a program of your own holds a database alone.

## What it writes

To standard error, one line for each request, as JSON:

<!-- a line of the log -->
```jsonc
{"time":"2026-11-14T09:30:00.000Z","command":"transition","user":"ap-agent","status":409,"code":"guard_false","ms":0.412}
```

| Member | What it is |
|---|---|
| `time` | when the request came, in UTC |
| `command` | the name the request was sent to: a command, or `blobs`, `file` or `blob` for a file |
| `user` | the id of who sent it, empty when the server could not tell |
| `status` | the status of the reply |
| `code` | the code of the refusal; left out when the command ran |
| `ms` | how long it took, in milliseconds |
| `error` | for a command that failed in the engine, with the code `internal`: what failed. The caller is not told |

A line never holds the body of a request, a value of a document or a
key.

## Its health

<!-- the health -->
```
GET /v1/health
```

```jsonc
{"ok":true,"version":"dev"}
```

Anybody may ask; it needs no key and says nothing of what the database
holds. `ok` is true when the server answers and can read its database,
and the status is then 200; otherwise it is 503.

## Stopping

Told to stop, by an interrupt, the server takes no new request, lets
the commands that are running end, for up to ten seconds, closes the
database and gives it back. What a commit had queued and has not run
stays in the database and runs when it is served again.

## What it takes on

A request of at most 16 MiB, a command that runs at most 30 seconds,
and 64 commands at once, unless [the settings](#the-settings) say
otherwise; [the commands over HTTP](commands.md#over-http) says what a
caller is answered beyond them.

## Not here yet

- **Counts for whoever watches the server** (`GET /v1/metrics`).
- **The stream of changes.**
- **The command-line tool as a caller** of a server, with a key.
- **Changing the settings without a restart.**
- **Limits for each user**, such as commands a minute. The limits are
  for the whole database.
- **Several databases behind one program.** `fadodb serve` serves
  one; a program that serves several makes one door for each
  ([the commands over HTTP](commands.md#over-http)).
