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

- **A key never travels in the clear by accident.** A key goes with
  every request. So the server refuses to start when it would listen
  beyond this machine without a certificate, unless `--plain-http`
  says that it may. Leaving a flag out never puts a key on the
  network.
- **Told nothing, it is safe**: this machine only.

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
and 64 commands at once; [the commands over HTTP](commands.md#over-http)
says what a caller is answered beyond them.

## Not here yet

- **The settings.** The limits above cannot be changed yet, and there
  is no file of settings.
- **Actions that call a service.** A served database may call no
  host, so an action that sends a request cannot be defined on it.
  The hosts it may call and the secrets of its connections come with
  the settings.
- **Refusing an action that needs a Go function when it is defined.**
  Today such an action can be defined on a served database and does
  not run: it answers `not_registered`. A server runs no code from
  outside; a company that needs its own function builds its own
  program around the door.
- **Counts for whoever watches the server** (`GET /v1/metrics`).
- **The stream of changes.**
- **The command-line tool as a caller** of a server, with a key.
- **Several databases behind one program.** `fadodb serve` serves
  one; a program that serves several makes one door for each
  ([the commands over HTTP](commands.md#over-http)).
