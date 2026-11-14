# ADR-0059: `fadodb serve`: one database, what it is told when it starts, and what it refuses

- Status: Proposed, with three questions open for the owner; amends ADR-0011 (what the server is and what it is told), ADR-0027 and ADR-0045 (an action implemented by a Go function does not run under `fadodb serve`), ADR-0052 (which settings are an administrator's)
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
   `timeout`, and writes nothing. A request beyond the count of
   commands or of streams waits a short time and is then refused with
   `busy`, with status 503. Both codes join the five of ADR-0055,
   item 7.

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

## Questions for the owner

1. **Is a database made only by `fadodb init`, and never by
   `fadodb serve` (item 1)?** Proposed: yes.

2. **Is it right that an action written as a Go function needs the
   company's own host program (item 6)?** Proposed: yes. The actions
   that call a service over HTTP are the ones a server runs, and they
   are the ones an agent can add without anybody building a program.

3. **Are all the settings the operator's (item 11)?** Proposed: yes,
   in this phase. The other answer: a database administrator changes
   some through commands, such as the hosts an action may call. That
   would let a user of the database widen what the machine's owner
   allowed.

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
