# ADR-0056: One database for each customer, and a server that serves one database

- Status: Proposed, with three questions open for the owner; amends ADR-0011 (a server serves one database, and the address says which), ADR-0027 (the users and keys of the optional package are kept in the database they belong to)
- Date: 2026-11-14

## Context

The owner asked on 2026-11-14, as Phase 6 opened, how FadoDB keeps many
customers apart when one service runs it for all of them: "I think it
touches server." It does: in what the server keeps and where it keeps
it, and in what an address names. Both are fixed by the first lines of
the server, so the answer is needed now.

This record is about the engine and its server. A service that runs
databases for customers also needs sign-up, invoices, backups and
machines. None of that is the engine's, and none of it is decided here.

The case used below: a service runs FadoDB for two companies, Acme and
Globex. Each has a class `Invoice`, with different variables. Ann works
at Acme. An agent, `ap-agent`, approves invoices at Globex.

There are three ways to keep Acme and Globex apart:

| | How | What keeps them apart |
|---|---|---|
| A | One database for all. Every document, class, table and event carries the name of its customer | a condition on the customer in every statement the engine runs |
| B | One database for each customer, many of them held by one server program | the database: each is a file of its own |
| C | One server for each customer, with its one database | the process and, if wished, the machine |

The architecture page says of A, in one line of what is out of scope
for 1.0: "multi-tenancy inside one database file (run one file per
tenant)". That line came from the first plan and was never put to the
owner as a question. This record puts it.

Facts checked in the engine on the day:

- **A class belongs to its database.** `Invoice` is defined once in a
  database, and its tables, views and indexes carry its name. Under A,
  Acme's `Invoice` and Globex's are one class, or every name in the
  engine gains a customer.
- **A formula sees every document** (ADR-0046, the owner's ruling), and
  `SQL` reads every row (ADR-0051). Inside one database nothing stands
  between two customers but the rules each class writes. Under A, one
  rule written without the customer shows Acme's invoices to Globex.
- **A database has one writer** (ADR-0035). Under A, every commit of
  every customer waits for every other's.
- **Nothing in a database reaches outside it.** The SQL endpoint
  refuses `ATTACH`, every table that is not a view and every function
  that reads a file (`internal/query/authorizer.go`). An action calls
  only the hosts it was allowed, and a host admitted by a suffix is
  refused when it resolves to an address inside the server's own
  network (`fado/outbound`). Attachments are kept in the database or in
  a directory beside it, and in a store shared with another database
  only if the host program gives one.
- **Two open databases share nothing that holds data.** What they share
  in one process is the engine's SQL functions, registered with the
  driver once, and two caches keyed by the compiled class, where each
  database's classes are keys of their own.
- **What an open database costs**, measured on the development machine,
  on disk: 300 databases of four classes and 50 documents each, all
  held open by one process.

  | | |
  |---|---|
  | Open a database that exists | 5.9 ms median, 29 ms p99 |
  | The first read after opening | 50 µs median |
  | Held open | 2.9 MB of memory, 3 goroutines, 7 open files |
  | Close | 0.85 ms median |
  | The database on disk | 332 KB |

  A thousand open databases are about 3 GB, and a database nobody is
  calling can be closed and opened again at the next call for a few
  milliseconds. Opening compiles every class, so it grows with their
  number.
- **A closed database does nothing.** Its timers do not fire and its
  queued actions are not tried again until it is opened: the worker
  lives in the open database (ADR-0044, ADR-0047).

## Decision

1. **A customer is a database.** Nothing in the engine marks a
   document, a class or an event with a customer, and nothing will.
   Whoever runs FadoDB for Acme and Globex gives each a database of its
   own. This is B, and C is B with one database for each server.

2. **Everything that is a customer's is in its database**, and the
   server keeps nothing of a customer beside it: the definitions, the
   documents with their history, the log, the queue of actions, the
   attachments, the substitutions and, from this phase, its users,
   groups and keys (ADR-0057). So:

   - a copy of Acme's database is the whole of Acme;
   - moving it to Acme's own machine moves everything, and Acme changes
     only the address its people and agents call;
   - deleting it leaves nothing of Acme behind.

   Two things stay the host program's and are not in the database, as
   before: the Go functions of actions and the secrets of connections
   (ADR-0045). A service keeps each customer's secrets apart and gives
   the engine the ones of the database that asks.

3. **A user of one database is nobody in another.** `ann` at Acme and
   `ann` at Globex are two users with nothing in common. A key opens
   the one database it was made in.

4. **The door serves one database.** The package that turns HTTP
   requests into commands (ADR-0055, item 10) is given one open
   database. It keeps nothing outside it: no variable of the package,
   no cache, no count and no limit that two doors share. A host program
   that serves many databases makes one door for each. A test holds the
   package to this, with two doors in one process: a key of Acme is
   `unauthenticated` at the door of Globex, and a commit at Acme never
   appears in the stream of changes of Globex.

5. **`fadodb serve` serves one database.** It is the door of item 4
   with one database behind it. Many databases behind one program is a
   host program that imports the door. It is not built in this phase.

6. **The address says which database, and nothing else does**: not the
   command, not its JSON and not the key. The command that `ap-agent`
   sends to Globex is, to the letter, the one an agent of Acme sends to
   Acme:

   ```
   POST https://globex.example.com/v1/transition
   Authorization: Bearer <a key made in the database of Globex>

   {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}
   ```

   A host program chooses the database by the host name, as here, or by
   the start of the path, which it takes off before the door sees the
   request. The door serves `/v1/…` and knows no customer. A key sent
   to the wrong address is `unauthenticated`, like any key a database
   does not know.

   So a script, a client and an agent's tool are the same against a
   service and against one's own `fadodb serve`.

7. **Every limit belongs to a door.** The largest request, the time a
   command may take and the number of commands at once are given when
   the door is made and count for its database only, so that the load
   of one customer is never the refusal of another. ADR-0059 names
   them.

8. **No command names a place on the server.** No field of a command
   is the path of a file or a directory. A file reaches a database as
   bytes in a request (ADR-0033) and rows as JSON. This holds for every
   command added later.

9. **What this record leaves out.**

   - Several customers in one database. It stays out of scope, as the
     architecture page said.
   - A formula or a query of one database that reads another. Nothing
     does today.
   - A call that tells a host program when a closed database next has
     work, so that it is opened in time for a timer. A program that
     closes quiet databases needs it. It is added to the Go API when
     such a program is built.
   - A copy of a database taken while it is in use, as one command.
   - Everything of a service that is not the engine: who signs up, an
     account that has several databases, such as one for tests and one
     for production, invoices, and which machine holds which database.

## Questions for the owner

1. **Is a customer a database (item 1)?** Proposed: yes. The answer not
   taken is A, one database for all with a customer on every row. It
   would put a customer into every table, view, index, formula and
   statement the engine has built in five phases; it would make every
   customer wait for the one writer; and one rule written wrongly would
   show one customer's documents to another.

2. **Does `fadodb serve` serve one database (item 5)?** Proposed: yes,
   and a program that serves many is a host program. The other answer:
   `fadodb serve`, given a directory, serves every database in it, each
   under its name in the address. That makes the open engine a ready
   service for many customers. It adds to this phase the opening of a
   database at its first call, its closing when it is quiet, and the
   call of item 9 that wakes it for a timer.

3. **Does the address alone say which database (item 6)?** Proposed:
   yes. The other answer: one address for all, and the key says which
   database. A key would then have to be found before its database is
   known, so keys would be kept outside the databases, against item 2.

## Consequences

- Customers are apart by how the engine is built and not by a
  condition somebody has to remember. No statement can leave the
  customer out, because no statement has one.
- Acme's long query and Acme's commits never hold Globex's writer. The
  two share the processor and the disk of the machine and nothing
  else. A customer that outgrows the machine is moved, database and
  all, to one of its own.
- Each customer names its classes as it likes, and defines `Invoice`
  its own way.
- ADR-0057 keeps users, groups and keys in the database, and ADR-0059
  gives `fadodb serve` one database to serve.
- A question asked of all customers at once, such as how many invoices
  the service processed this month, opens every database in turn. That
  is the price, and it is the one a customer expects a service to pay.
- A service with many small customers holds many open databases or
  opens them as they are called. The figures above say what each
  costs.

## Performance

No budget changes. The campaign of the phase's last step measures what
it takes to open a database of fifty classes and what such a database
holds open, and publishes both in `docs/perf/v0.6.0.md`. No budget is
set on either until a program exists that opens databases as they are
called.
