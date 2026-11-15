# ADR-0059: `fadodb serve`: one database, what it is told when it starts, and what it refuses

- Status: Accepted; amends ADR-0011 (what the server is and what it is told), ADR-0027 and ADR-0045 (an action implemented by a Go function does not run under `fadodb serve`), ADR-0052 (which settings are an administrator's), ADR-0055 (two codes, `timeout` and `busy`, join the codes of the door)
- Date: 2026-11-14

## Context

`fadodb serve` is the program a company starts to reach its database
over the network: the door of ADR-0055 with the users and keys of
ADR-0057, in front of one database (ADR-0056). This record says what
the person who starts it tells it, what it does when it is told
nothing, and what it will not do.

That person is called the operator here: whoever has the machine. The
operator is not a user of the database. A database administrator
(ADR-0052) is a user and keeps users and keys through commands; the
operator starts and stops the program and holds the database itself.

Facts checked on the day:

- **A database is made readable by every user of the machine.** The
  store creates it, and its log beside it, as `rw-r--r--`, and the
  directory of attachments as `rwxr-xr-x`. The owner ruled on
  2026-11-14 that a database is closed to everybody but its owner.
- **Nothing stops two programs from opening one database today.**
  Each keeps the compiled classes, and from this phase the users and
  keys, in its own memory. What one defines or revokes, the other does
  not learn.
- **A database sends nothing out unless the host program allows it**
  (ADR-0045): the hosts are listed when the database is opened, and the
  secrets of connections are asked of the host program at every
  request.
- **An action may be implemented by a Go function** the host program
  gives at `Open` (ADR-0027). `fadodb serve` is a finished program and
  has none. Today a definition without its function is accepted, and
  its action dies with `not_registered` when it first runs.
- A database that is opened and does not exist is created. A mistyped
  name makes an empty database.

## Decision

1. **`fadodb serve` serves one database that exists.**

   ```
   fadodb init acme.fado
   fadodb user add olga --kind person --system administrators --db acme.fado
   fadodb key new olga --label laptop --db acme.fado
   fadodb serve acme.fado
   ```

   `fadodb init` makes a database and is where what is fixed at its
   making is said: the limit of a text, where attachments are kept and
   their largest size. `fadodb serve` refuses a name under which there
   is no database, and says to run `init`.

2. **Told nothing, it is safe.** `fadodb serve acme.fado` with nothing
   else:

   | | Told nothing | How it is told otherwise |
   |---|---|---|
   | Where it listens | `127.0.0.1:8080`: this machine only | `--listen` |
   | Encryption on the wire | none, which is safe on this machine only | `--tls-cert` and `--tls-key` |
   | What actions may call | nothing: an action that sends a request cannot be defined | the settings, item 4 |
   | The largest request | 16 MiB (ADR-0055) | the settings |
   | The time one command may take | 30 seconds | the settings |
   | Commands at once | 64 | the settings |
   | Streams of changes open at once | 256 | the settings |

   A command that runs out of time is stopped and refused with
   `timeout`, status 504, and writes nothing. A request beyond the
   count of commands or of streams waits a short time and is then
   refused with `busy`, status 503. Both codes join the five of
   ADR-0055, item 7.

3. **A key never travels in the clear by accident.** Listening on any
   address but this machine's without a certificate is refused at
   start, unless the operator says that something in front encrypts:
   `--plain-http`, for a server behind a proxy or inside a container.

4. **The settings are one JSON file the operator writes**, given with
   `--settings`. It holds what does not fit a flag: the hosts actions
   may call and the limits of item 2.

   ```json
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

   The lists are the ones of `fado/outbound`, with its rules: a
   connection's secrets go only to that connection's hosts, by the
   owner's ruling of 2026-11-10. A field the file should not have is
   refused at start, as in a command.

5. **A secret is never in the settings, in a definition or on the
   command line.** `fadodb serve` finds the secret `token` of the
   connection `slack` in one of two places, and reads it again for
   every request, so that a secret is changed without a restart:

   - a file, `slack/token`, in the directory given with
     `--secrets-dir`;
   - or the environment, as `FADODB_SECRET_SLACK_TOKEN`.

6. **An action implemented by a Go function does not run under
   `fadodb serve`.** The actions of the engine, and the actions that
   send an HTTP request, do. A definition that needs a Go function is
   refused when it is defined, with `not_registered` and the fix in the
   hint, so that an agent learns it at once and not from a dead action.
   A company that needs its own Go function writes a small host program
   that imports the door and gives the function at `Open`; it is the
   same server with one thing added.

7. **`fadodb serve` holds its database alone.** A second program that
   opens the same database, a second `fadodb serve` or the
   command-line tool, is refused while the first runs, and told which
   program holds it. The command-line tool then reaches the database
   through the server, with a key. How the hold is kept is settled
   while building; it must end by itself when the program dies.

8. **A database is closed to everybody but its owner.** The engine
   creates a database, the files beside it and the directory of
   attachments readable and writable by the user that made them and by
   nobody else. This is the engine's, for every program that opens a
   database, and it is built first, in a pull request of its own. A
   database that exists keeps the permissions it has; the release
   notes say how to close one.

9. **What it writes about itself.** One line for each request, as
   JSON, to standard error: the time, the command, the user, the code
   of the refusal if there was one, and how long it took. Never a body,
   a value of a document or a key. Two addresses beside the commands:

   | Address | Answers | Who |
   |---|---|---|
   | `GET /v1/health` | that the server answers and can read its database, and its version | anybody |
   | `GET /v1/metrics` | counts and times of commands, the queue of actions and the streams, in the text form Prometheus reads | a database administrator |

10. **How it stops.** Asked to stop, it takes no new request, lets the
    commands that are running end, for up to ten seconds, closes the
    streams with `closed` (ADR-0058) and closes the database.

11. **The settings of this record are the operator's, and none is kept
    in the database.** ADR-0052 gave database administrators "users,
    keys and settings". Where the server listens, what it may call and
    how much it takes on are decided by whoever runs the machine, and a
    user of the database changes none of them through a command.

12. **What this record leaves out.**

    - Many databases behind one program (ADR-0056, item 5).
    - A program image for containers, and the description of the
      commands for other languages. They are steps of the phase and
      change no contract.
    - Changing the settings without a restart.
    - Limits for each user, such as commands a minute. The limits of
      item 2 are for the whole database.

## Settled while building the door

The limits of item 2 that are about commands are the door's
(`fado/httpdoor`), so that a host program has them as `fadodb serve`
does. Built with the door, ahead of `fadodb serve`:

- **A command takes its slot before its body is read.** So a door
  never holds more bodies than it runs commands: 64 of at most 16 MiB
  unless told otherwise. A body is read after the door knows who sends
  it.
- **The short time a request waits for a slot is one second**, and the
  refusal `busy` carries `Retry-After: 1`.
- **What `timeout` leaves behind.** A command that is one commit wrote
  nothing. `migrate` and `drain` commit as they go, and keep what they
  had done when they were stopped; both can be sent again. The refusal
  says so in its hint.
- **A refusal that comes back after the time is up is still the
  refusal.** `timeout` is answered only for a command that the clock
  stopped: one that failed in the engine or in its storage once its
  time had run out.
- **A file in transfer counts among the commands at once** and is not
  stopped by the time one command may run: a large file over a slow
  line takes longer than a command should. The server's own limits on
  a connection that sends nothing are `fadodb serve`'s to set.
- **The door tells of every request** through a function the program
  gives it: when, which command, who, the status, the code and how
  long. The line of item 9 is written from it by `fadodb serve`.

## Settled while building `fadodb serve`

The program is built in parts. The first built items 1, 3, 7 and 10,
the defaults of item 2, and of item 9 the line for each request and
the health. The settings (items 4, 5, 6 and 11) and the metrics follow.

- **How the hold is kept (item 7).** A program that opens a database
  with `Options.Alone` takes a lock of the operating system on a file
  beside it, `acme.fado.lock`, and writes into it the name it goes by,
  its process and the moment. The system drops the lock when the
  program ends, however it ends; a test kills a program that holds a
  database and opens the database after it. Every `Open` asks whether
  the database is held alone, and is refused with the new code
  `in_use` and the words of the holder: "the database acme.fado is
  held by fadodb serve (process 4711) since 2026-11-14 09:30:00 UTC".
  The lock file stays when the hold is gone, since a lock file that is
  removed can be locked twice.
- **The tool on the machine holds the database alone too**, for the
  moment it works: `fadodb init`, `fadodb user add` and
  `fadodb key new`. `fadodb check` holds nothing and is refused a
  served database like any program.
- **What the hold does not reach.** A program that opens a database
  without `Alone` holds nothing. It is refused a database that is held
  alone, and a server that starts while such a program has the
  database open does not see it. Whether every program that opens a
  database should hold it alone is a question for the owner: it would
  end two programs on one file for good, and it would refuse what the
  engine lets through today.
- **`fadodb init` takes what is fixed at the making** as `--max-text`,
  `--blobs` and `--max-blob`, and refuses a file that exists.
  `fadodb serve`, `fadodb user add` and `fadodb key new` refuse a file
  that does not, and say to run `init`.
- **`fadodb key new` prints the key alone** on standard output, so
  that a script takes it, and what it says about the key on standard
  error.
- **Half a certificate is refused**, and so is `--plain-http` with a
  certificate. An address is this machine's when its host is
  `localhost` or a loopback address; `:8080`, which is every address
  of the machine, is not.
- **The line of a request** is `time`, `command`, `user`, `status`,
  `code` when the request was refused, `ms`, and `error` with the
  words of a failure that the caller is not given.
- **The health** answers `{"ok": true, "version": "…"}` with 200, and
  `ok` false with 503 when the database cannot be read.
- **A server of a database with no user says so** when it starts, with
  the two commands that make the first.
- **A caller that connects and sends no request is dropped after ten
  seconds.** A body and a reply have no limit of time, since a file
  takes as long as its line allows.

The second part built items 4, 5, 6 and 11: the settings, the secrets
and the refusal of a Go function.

- **The file of settings is read once, when the server starts**, by a
  reader as strict as a command's: it refuses a member the file should
  not have, a member written twice and a value that does not fit, and
  names the file and the place. `limits.command_seconds` may have a
  fraction and is at most a day. `limits.streams` is read and kept for
  the stream of ADR-0058, which is not built, so it limits nothing
  yet. The time of a call and the size of its reply, ten seconds and
  one megabyte (ADR-0045, item 7), are not settings.
- **A connection of the settings has a name a definition can give
  it**, and no two differ in case only: their secrets are kept under
  the name without regard to case, so two such connections would share
  them.
- **A secret in the environment is `FADODB_SECRET_SLACK__TOKEN`, with
  two underscores** between the connection and the secret, and not
  with one, as item 5 wrote it. A name may hold an underscore. With
  one between them, the secret `c` of a connection `a_b` and the
  secret `b_c` of a connection `a` are one variable, and a definition
  could name the second to send the first to the hosts of another
  connection, against the owner's ruling of 2026-11-10. No name holds
  two underscores (ADR-0031), so the variable of one connection is
  never that of another.
- **The file is looked for first, then the environment.** The
  directory and the file are in small letters, the variable in
  capitals. A file is read without the end of its last line and holds
  at most 64 KiB. A file that is there and empty is no secret, and the
  environment is not asked in its place. The directory given with
  `--secrets-dir` must be there when the server starts.
- **Why a secret is missing is said without the directory of the
  machine.** The words stay with the action that was not sent, where
  users of the database read them: they name the file as `slack/token`
  and the variable.
- **What item 6 found already there.** The Context says that a
  definition without its function is accepted. It is not: since
  ADR-0045 was built, a program with no function under the name
  refuses the definition, with `not_supported`. What is there, and is
  tried again with `not_registered` until it is dead, is an action of
  a database that another program kept with the function. So this part changed the code of the
  refusal to `not_registered`, in every program, and its hint to the
  two ways out: a request, or a program of one's own. A class whose
  rule names such an action is taken as in any program, and the action
  does not run.
- **The server says what it cannot run, when it starts**: an action of
  a Go function, an action whose connection the settings do not have,
  and a secret that is in neither place, each by name. It also says in
  a line what its actions may call.
- **`examples/server` is the program of item 6**: the door in front of
  a database opened with a Go function, in about fifty lines, played
  by a test.
- **The gate of ADR-0045, item 7, moved.** `fadodb` is now a program
  that allows outbound calls when its settings list a host, so it
  imports `fado/outbound` and links the HTTP client, as ADR-0045 said
  it would when the server arrived. The dependency gate holds the rule
  on `examples/server` instead: a program with the engine and the door
  links no HTTP client, and neither package imports `fado/outbound`.
  The tool is 14.1 MB against its budget of 15.

The third part built the counts of item 9.

- **The door tells of each request, and the program counts.**
  `fadodb serve` counts in the function the door reports to, the one
  that writes the line of the log. A program of one's own around the
  door counts what it likes from the same reports.
- **What is on the page.** The requests by command and status; the
  time the requests of each command took, added up, and how many they
  were; all requests by how long they took; the queue of the database,
  as the actions that are due, the ones that wait for their moment and
  the dead ones, with how long the oldest due action has been due; the
  sizes of the database and of its log; the version and the moment the
  server started. The count of open streams comes with the stream.
- **A name that is no command is counted as `other`.** A caller may
  send any name, and a page with a line for each would have no bound.
  Only what happened has a line.
- **The counts are kept in memory** and begin at zero with each start.
  No user, no document and no value is in them.
- **Who reads them** is a database administrator, as item 9 says:
  without a key the request is refused with `unauthenticated`, and the
  queue is counted as the caller, so the engine itself refuses
  everybody else with `forbidden`. A watcher such as Prometheus
  therefore holds the key of an administrator, a key that can also
  make users and keys. A lesser right, for watching alone, would be a
  change to this item and is the owner's to decide; it is not built.
- **`OutboxCounts` is the Go call behind the counts of the queue.** It
  reads three counts from an index and no row, and is no command:
  `outbox` lists the actions themselves.
- **A request for the counts has its line in the log and is counted;
  a request for the health has neither**, since a health check comes
  every few seconds from a machine and says nothing of who asks.
- **Counting a request allocates nothing**, which a test holds: it
  stands on the way of every command.

## The owner's rulings

All of 2026-11-14. The owner read the three questions, each with a
case, and agreed to the answer proposed for each.

**1. Is a database made only by `fadodb init`, and never by `fadodb
serve`? By `init` only** (item 1). An operator who types
`fadodb serve acm.fado` for `acme.fado` is refused and told to run
`init`. The answer not taken: `serve` makes an empty database under
the mistyped name and serves it, with no administrator in it.

**2. Is it right that an action written as a Go function needs the
company's own host program? Yes** (item 6). The owner: "I don't think
we can allow stranger code run in our server, can we?" It cannot, and
under this record it does not: `fadodb serve` runs the engine's own
actions and the actions that send an HTTP request, whose code runs in
the service they call. A Go function runs only in the program of the
company that wrote it. The answer not taken was a way to load a
company's code into `fadodb serve`.

**3. Are all the settings the operator's? Yes**, in this phase
(item 11). Olga, a database administrator, changes none of them
through a command. The answer not taken: an administrator changes
some, such as the hosts an action may call, which would let a user of
the database widen what the owner of the machine allowed.

## Consequences

- The shortest way to a working server is four lines, and it listens
  to its own machine only until somebody decides otherwise.
- A company that puts the server on the network has to give it a
  certificate or say that a proxy does. It cannot happen by leaving a
  flag out.
- The operator and the database administrator are two roles. In a
  small company they are one person; in a service they are the service
  and its customer.
- Two programs can no longer disagree about one database.
- A database made before this change stays readable by the users of
  its machine until its operator closes it.
- `fadodb serve` is the door with one database, so a host program that
  serves many, or adds a Go function, starts from a program of a few
  lines.

## Performance

The budget of ADR-0011 stands and is measured against `fadodb serve` as
it is told nothing: under 200 µs added at p99 to a transition, over a
connection that is kept open. The line written for each request is
inside that budget and allocates nothing beyond its buffer. The limits
of item 2 are counted without a lock that commands wait on.
