# Running it

This chapter is for whoever keeps a database in service: the developer
whose program holds the engine, and the administrator of a database
that `fadodb serve` serves. It says how to make a backup and how to
put one back, what a program that died leaves behind, what the check
finds, how large the file gets and how fast the engine is.

A test plays the story of this chapter and every table of it on each
push. One measurement is not played, and is marked.

## What is on disk

[Opening a database](opening-a-database.md#what-is-on-disk) lists the
files. Three things matter here:

- **A database that is closed is one file**, `books.fado`.
- **While a program has it open, its latest commits are in a second
  file**, `books.fado-wal`. The engine folds them into the database
  as it goes, and all of them when the program closes it.
- **The files that documents hold are in the database**, or, with the
  store `dir`, in the directory `books.fado.blobs` beside it; see
  [Files](files.md#where-the-bytes-are-kept).

## The database of the story

Acme keeps its receipts in `books.fado`, with the store `dir`. A
receipt holds its scan, and filing it sends a mail to accounting. The
mail is a Go function of Acme's program.

```json
{"name": "mail.send", "params": {"to": "text", "subject": "text"}}
```

```json
{
  "name": "Receipt",
  "title": "\"Receipt for \" & what",
  "variables": {
    "what": {"type": "text", "required": true},
    "scan": {"type": "object", "schema": "File"}
  },
  "states": {"initial": "open", "list": ["open", "filed"], "final": ["filed"]},
  "transitions": [
    {"name": "file", "from": "open", "to": "filed", "guard": "NOT ISBLANK(scan.hash)"}
  ],
  "rules": [
    {"on": "transition", "name": "file", "do": [
      {"action": "mail.send", "args": {"to": "\"accounting@acme.example\"", "subject": "\"Filed: \" & what"}}
    ]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "CREATEDBY() = USER()",
    "transition": {"file": "CREATEDBY() = USER()"}
  }
}
```

## A backup, and putting it back

Ola works in sales. Adam looks after the machine.

<!-- what it does -->
| Step | Who | Does what | What is there afterwards |
|---|---|---|---|
| 1 | Ola | creates the receipt `R-1` with its scan and files it. The mail server gives no answer, so the mail waits in the queue | receipts: 1, files: 1, mails sent: 0, waiting: 1 |
| 2 | Adam | stops the program in the evening, and copies `books.fado` and `books.fado.blobs`. He opens the copy to look at it | receipts: 1, files: 1, mails sent: 0, waiting: 1 |
| 3 | Adam | starts the program in the morning. The mail that waited goes out | receipts: 1, files: 1, mails sent: 1, waiting: 0 |
| 4 | Ola | creates the receipt `R-2` with its scan and files it. Its mail goes out | receipts: 2, files: 2, mails sent: 2, waiting: 0 |
| 5 | Adam | stops the program, puts the copy of the evening where the database was, and starts the program | receipts: 1, files: 1, mails sent: 3, waiting: 0 |

What the story shows:

- **A backup is a copy made while no program has the database open**:
  the file, and the directory of the store `dir`. Nothing else is
  needed. The file `books.fado.lock` need not be copied.
- **Putting a copy back brings back everything as it was**: the
  documents with their versions, the log, the classes, the users and
  their keys, and the queue.
- **What was done after the copy is gone.** After step 5 the database
  does not know `R-2`.
- **What waited in the queue runs again.** The mail of `R-1` waited
  when Adam made the copy. It went out in the morning, and it went out
  a second time after step 5, since in the copy it still waits. Both
  times the function was given the same key, so a receiver that keeps
  the keys it has seen can tell the second from the first; for a
  request the engine sends the key as `Idempotency-Key`
  ([Actions](../actions.md)).
- **The world outside is not put back.** Accounting has the mail about
  `R-2`, and the database has no such receipt.

A served database is stopped by stopping its server, which closes it;
see [Serving a database](serving-a-database.md#stopping).

## What a copy is

<!-- a copy -->
| What is copied | When | What the copy is |
|---|---|---|
| the database file, and the directory of its files | no program has the database open | the whole database |
| the database file without the directory | no program has the database open | every document, and no file: opening one is refused with `unknown_blob`. The check does not notice |
| the database file alone | a program has it open | not a backup. It lacks the commits that are still in the `-wal` file. In the test that was all of them: the copy opened, with no complaint, as a database with no class |
| a copy that SQLite makes, with `VACUUM INTO` | a program has it open and writes to it | the whole database, as it was at one moment between two commits |

- **Copying the `-wal` file as well does not make a backup of an open
  database.** The two files change while they are copied.
- **With the store `external`** the bytes are where the host program
  keeps them, and their backup is the host program's.
- **A copy is as secret as the database.** Whoever can read it can
  read every document in it, past every rule. It holds no key: the
  database keeps a hash of each key and not the key.

### Looking into a copy

Opening a copy is opening a database: the worker starts, and runs what
waits in the queue of the copy, for real. A program that only looks
opens the copy with the queue run by hand, and does not run it:

```go
db, err := fado.Open("/backup/books.fado", fado.Options{
	Outbox: fado.OutboxOptions{Manual: true},
})
```

That is how Adam looked at his copy in step 2, and no mail went out.

### A database that does not stop

The database is a SQLite file, and SQLite can copy a database that is
open and written to. FadoDB has no command of its own for it yet. With
SQLite's own command-line program:

```
sqlite3 books.fado "VACUUM INTO '/backup/books.fado'"
```

- **The copy is whole.** It is the database as it was at one moment
  between two commits. The test runs the same statement through a
  second connection of SQLite while a caller creates receipts and
  changes each of them, without pause. It opens the copy and finds
  every commit up to one moment and none after it: each receipt has
  both its versions, apart from the last one, which the copy may have
  caught between the two.
- **The copy is one file with nothing beside it**, and is opened like
  any database.
- **SQLite makes the copy as open as any new file.** The engine closes
  a database it makes to every user of the machine but its owner.
  SQLite does not: on a Linux machine with the usual settings every
  user can read the copy. Make it in a directory that is closed to
  others, or close it: `chmod 600 /backup/books.fado`.
- **With the store `dir`, copy the directory after the database.**
  The bytes of a file are written before a document names them, and
  are never removed, so a directory copied afterwards holds every file
  the copy names.

## When the program dies

A program that is killed, or a machine that stops, does not close the
database. In the test a database holds three receipts and is copied.
A fourth is made, and the program closes the database. A fifth is
made, and the program dies.

<!-- after the program died -->
| Beside the database | What opens |
|---|---|
| the `-wal` file the program left | everything the program was answered: five receipts |
| nothing: the `-wal` file was removed | the database without its latest commits: four receipts |

- **Leave the two files where they are.** The latest commits are in
  the `-wal` file, and the next program that opens the database folds
  them in.
- **The hold ends by itself.** A database that the dead program held
  alone is free; see
  [Opening a database](opening-a-database.md#one-program-holds-a-database).
- **What a commit was answered is there** after the program died. When
  the machine stops or the power fails, the last commits may be lost
  unless the database was created with `FULL`; see
  [How sure a commit is](opening-a-database.md#how-sure-a-commit-is).
- **The queue goes on** when the database is opened again; see
  [The outbox and the worker](the-outbox-and-the-worker.md).

## Putting a copy back

1. Stop the program, or the server.
2. Put the copy where the database was, and with the store `dir` the
   directory too.
3. **Remove the files `-wal` and `-shm` beside it**, when they are
   there. They are there when the program died instead of closing the
   database.
4. Start the program.

The copy of three receipts, put where the program of the last section
died:

<!-- putting a copy back -->
| The copy is put | What opens |
|---|---|
| where no `-wal` file is | the copy |
| beside the `-wal` file of the program that died | not the copy. The copy holds three receipts, and the database that opens holds five |

The `-wal` file belongs to the database file it was written beside.
Under another file it is still read, and what opens is a mixture of
the two that nobody made.

## Checking

`fadodb check` reads the bytes of every file the database keeps, works
out the hash of each again, and names the ones that do not match. It
is how to see that a copy has its files as they were put.

```
fadodb check books.fado
```

<!-- the check -->
```text
books.fado: 2 classes, 0 tables, 2 blobs checked, all match
```

The two classes are `Receipt` and `Substitution`, which every database
has. A blob is the bytes of one file: here the scans of `R-1` and
`R-2`. When one byte of the scan of `R-1` is changed on disk:

<!-- the check, a file altered -->
```text
books.fado: 2 classes, 0 tables, 2 blobs checked, 1 do not match:
  7a67c33675777c40c57af1be207dd2a250dee13c3e5e8b6668371477b80b0e21
```

<!-- what the check finds -->
| On disk | The check |
|---|---|
| every file as it was put | names nothing |
| a file whose bytes were changed | names its hash |
| a file that is missing | names nothing, and counts one file less |

<!-- how the check ends -->
| The check | It ends with |
|---|---|
| finds every file as it was put | 0 |
| names a file | 1 |
| cannot open the database, because another program holds it alone | 1 |
| is called with no database | 2 |

- **A file that is missing is not found.** The check reads what the
  store holds. It does not ask which files the documents name. After a
  copy, compare the count of blobs with the one before.
- **The database file itself is not checked.** The check opens it, and
  says how many classes and tables it read.
- **A served database is checked through its server**, by an
  administrator, with the command
  [`check_blobs`](commands.md#check_blobs). `fadodb check` is refused
  with `in_use` while the server holds the database.

> **`fadodb check` runs the queue of the database it checks.** It opens
> the database as a program does, so the worker starts. The tool has
> none of the program's functions and may call no host, so an action
> that is due fails, with `not_registered` or `outbound_off`, and
> waits for its next attempt. An action is dead after ten attempts.
> `fadodb user add` and `fadodb key new` open the database the same
> way. In the test a program with no functions opens Adam's copy, and
> the mail that waits there is tried and fails with `not_registered`.
> The tool does not leave the queue alone today. Until it does, check
> a database whose queue is empty, or a second copy that is thrown
> away afterwards.

## How large the file is

<!-- the size -->
| What | The file |
|---|---|
| a new database, with no class | about 200 KB |
| a receipt created, or changed | about half a kilobyte more each time |
| a receipt deleted | not smaller. Its versions and its events stay |

- **Every commit adds to the file**: a version of the document and an
  event. Nothing takes them out again.
- **A larger class costs more.** In the campaign of
  [v0.2.0](../perf/v0.2.0.md), two million invoices of twenty-one
  variables with three million versions made a file of 6.7 GB.
- **The bytes of files** are in the database with the store `sqlite`,
  and are never removed, whatever the store.
- **The file never gets smaller.** No call makes it so.

### Room for the latest commits

The `-wal` file is emptied once it passes 64 MiB, and the engine does
that in a moment between two commits. The option `WALLimit` changes
the size; see [Opening a database](opening-a-database.md#the-options).

> **Not played by the test.** Measured on 2026-11-15 on the machine of
> the [performance pages](../perf/README.md). One caller created
> receipts for four seconds without any pause, over 50,000 of them. The
> engine found a moment to empty the `-wal` file only every second or
> two, and the file reached between 1.4 and 2.3 GiB in three runs,
> beside a database of 27 MB. With a pause of about a millisecond
> between two commits it never passed 66 MiB.

So leave room on the disk for it, and let a program that writes for a
long time without pause, such as an import, stop for a moment now and
then.

## How fast it is

Measured for [v0.5.0](../perf/v0.5.0.md), on one machine, which that
page describes. Yours is another:
[the page](../perf/v0.5.0.md#how-to-run-it) says how to run the same
measurements.

<!-- measured -->
| Work | Measured |
|---|---|
| one document created | 99 of 100 take less than 0.20 ms |
| one document changed | 99 of 100 take less than 0.27 ms |
| one document moved by a transition | 99 of 100 take less than 0.17 ms; 12,740 a second |
| many documents moved by one call | 27,488 a second |
| many documents created in one transaction | 25,220 a second |
| one document read by its id | 0.005 ms at the median |
| a filter on an indexed variable, over 1,000,000 documents | 15.45 µs |
| opening a database of 11.2 GB | 4.7 ms |

What is slower than a reader might expect:

<!-- what is slow -->
| What | Measured | Why |
|---|---|---|
| a commit made while another caller moves documents in batches of 1,000 | 9 of 10 waited less than 69 ms | there is one writer at a time, and a batch keeps it until all its documents are moved |
| the first ten of a caller's own 10,000 documents, sorted | 10.7 ms | the engine finds the 10,000 and sorts them before it answers ten |
| a commit in a database created with `FULL` | as slow as the disk confirms a write | every commit waits for the disk; see [How sure a commit is](opening-a-database.md#how-sure-a-commit-is) |
| a lookup in a table with no index for it | grows with the table | see [How fast a lookup is](tables.md#how-fast-a-lookup-is) |

## Another release

Before version 1.0 a release may change how the file is laid out. A
database is opened only by an engine that lays it out the same way,
and any other is refused with `not_supported`; see
[Opening a database](opening-a-database.md#what-is-refused). There is
no way yet to bring a file forward. So keep with each backup the
release that made it: `fadodb version` says it, and the
[changelog](../../CHANGELOG.md) says which release changes the file.

## Not here yet

- A backup that the engine makes itself, of a database that is open.
- A check that finds a file that is missing, and one that checks the
  database file itself.
- A tool that leaves the queue alone.
- Making the file smaller: nothing removes old versions, events or
  bytes.
- A way to bring a file of an earlier release forward.
