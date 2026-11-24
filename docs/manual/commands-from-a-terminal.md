# Commands from a terminal

`fadodb exec` sends a command to a server that serves a database, and
prints the answer. It is for a person at a terminal, for a script, and
for an agent that works through a shell. It also writes an export, and
`fadodb file` puts a file on a document and reads one.

```sh
fadodb exec transition '{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}'
```

After `exec` comes the name of a command, and then its object: the
same name and the same object that [the commands](commands.md) lists,
and that a program sends over HTTP. So all the commands are there, and
a command added to the engine is there with it.

The tool is a caller as any other. It holds no database. It sends the
command to the server with the key of a user, so what is typed at a
terminal is judged by the rules of the classes, and written to the log
under that user, as a command sent by a program is.

The story of this chapter: Acme keeps its invoices in a database that
[`fadodb serve` serves](serving-a-database.md). Olga keeps the database
and designs its classes. Bob, in sales, files invoices. Ann, in
finance, approves them. A test plays every line of the chapter against
a server and compares what the tool prints.

## The server and the key

The tool is told two things: which server, and whose key.

```sh
export FADODB_URL=https://fado.acme.example
export FADODB_KEY=fado_…
```

<!-- what the tool is told -->
| What | Flag | Environment | Told nothing |
|---|---|---|---|
| the server | `--url` | `FADODB_URL` | `http://127.0.0.1:8080`, where `fadodb serve` listens when it is told nothing |
| the key | `--key-file`, a file that holds it | `FADODB_KEY` | the tool sends nothing, and says how a key is made |
| a certificate to know the server by | `--ca`, a file in PEM | | the certificates the machine knows |
| that the key may travel in the clear | `--plain-http` | | it may not, beyond this machine |

A flag counts before the environment.

- **The key is never an argument.** A line of a terminal is kept in its
  history and shown to the other users of the machine. The key is in
  the environment of the terminal, or in a file that only its owner
  reads.
- **The key does not travel in the clear by accident.** An address
  that begins with `http://` is taken for this machine only. Beyond it
  the tool wants `https://`, or `--plain-http`, which says that
  something between the tool and the server encrypts.
- **The tool follows no redirect.** A command goes to the server it was
  addressed to, and its key to nobody else.

Olga's key was made on the machine that holds the database, by
`fadodb key new`, as [Serving a database](serving-a-database.md)
shows. Every other key is made through the server, below.

## A class from a file

Olga has the class of an invoice in a file, `invoice.json`:

<!-- invoice.json -->
```json
{
  "name": "Invoice",
  "title": "supplier",
  "variables": {
    "supplier": {"type": "text", "required": true},
    "total":    {"type": "decimal", "scale": 2, "required": true},
    "scan":     {"type": "object", "schema": "File"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "approved"]},
  "transitions": [
    {"name": "submit",  "from": "draft",  "to": "review"},
    {"name": "approve", "from": "review", "to": "approved"}
  ],
  "access": {
    "read":   "INGROUP(\"sales\") OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"sales\") AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {"submit": "INGROUP(\"sales\")", "approve": "INGROUP(\"finance\")"}
  }
}
```

The tool checks it, which needs no server, and then defines it in the
database:

<!-- olga: the class -->
```sh
fadodb class check invoice.json
fadodb exec define_class --from definition=invoice.json
```

The object of `define_class` has one member, `definition`. `--from`
takes that member from a file: `--from definition=invoice.json` puts
the JSON of `invoice.json` under `definition`. The bytes of the file
go as they are, so the definition reaches the database as its author
wrote it.

## Users and their keys

Olga makes Bob and Ann, and a key for each:

<!-- olga: the users -->
```sh
fadodb exec create_user '{"id": "bob", "kind": "person", "groups": ["sales"]}'
fadodb exec create_user '{"id": "ann", "kind": "person", "groups": ["finance"]}'
fadodb exec create_key '{"user": "bob", "label": "laptop"}'
fadodb exec create_key '{"user": "ann", "label": "laptop"}'
```

`create_key` prints the key once, under `key` as `secret`. Olga hands
each key to its user, who puts it in `FADODB_KEY`.

## A command and its answer

Bob, with his key, files three invoices and submits them:

<!-- bob: three invoices -->
```sh
fadodb exec create '{"class": "Invoice", "id": "FV/2026/11/0041", "values": {"supplier": "Paper Mill", "total": "840.00"}}'
fadodb exec create '{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"supplier": "Ink and Son", "total": "1250.50"}}'
fadodb exec create '{"class": "Invoice", "id": "FV/2026/11/0043", "values": {"supplier": "Paper Mill", "total": "2400.00"}}'
fadodb exec transition_many '{"class": "Invoice", "transition": "submit", "ids": ["FV/2026/11/0041", "FV/2026/11/0042", "FV/2026/11/0043"]}'
```

Ann, with hers, approves one:

<!-- ann: approve -->
```sh
fadodb exec transition '{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}'
```

The tool prints the answer of the server, set out over lines, to
standard output, and ends with 0:

<!-- ann: approve, the answer -->
```jsonc
{
  "document": {
    "class": "Invoice",
    "id": "FV/2026/11/0042",
    "title": "Ink and Son",
    "state": "approved",
    "version": 3,
    "class_version": 1,
    "created_at": 1794733200,
    "updated_at": 1794733200,
    "state_entered_at": 1794733200,
    "created_by": "bob",
    "updated_by": "ann",
    "state_entered_by": "ann",
    "state_entered_from": "review",
    "values": {
      "scan": null,
      "supplier": "Ink and Son",
      "total": "1250.50"
    }
  }
}
```

Only the space between the values is the tool's. Every number and
every text is printed as the server wrote it.

## A refusal

Bob is in sales, and not in finance. He sends the same command for
the next invoice:

<!-- bob: approve -->
```sh
fadodb exec transition '{"class": "Invoice", "id": "FV/2026/11/0043", "transition": "approve"}'
```

The server refuses it. The tool prints the refusal to standard error,
as the server gave it, prints nothing to standard output, and ends
with 1:

<!-- bob: approve, the refusal -->
```jsonc
{
  "error": {
    "code": "forbidden",
    "message": "bob may not take approve from review",
    "hint": "the rule of approve: INGROUP(\"finance\")",
    "path": "/access/transition/approve"
  }
}
```

So a script stops at a refusal, and an agent reads its code.
[The refusals](refusals.md) lists every code.

## The object from somewhere else

A long object is easier kept in a file than typed between quotes.

| Given as | The object is |
|---|---|
| `'{…}'` | the argument, in single quotes so that the shell leaves it alone |
| `-` | what is read from standard input |
| `@FILE` | what the file holds |
| nothing | `{}`, for a command that needs no member, such as `list_classes` |
| `--from MEMBER=FILE`, beside any of them | the same object with one more member, the JSON the file holds; it may be given several times |

Ann has the command for the last invoice in a file,
`approve-0043.json`:

<!-- approve-0043.json -->
```jsonc
{"class": "Invoice", "id": "FV/2026/11/0043", "transition": "approve"}
```

<!-- ann: approve from a file -->
```sh
fadodb exec transition @approve-0043.json
```

## An export

Ann sends the accountant every approved invoice over 1000, as an Excel
file:

<!-- ann: the export -->
```sh
fadodb exec query '{"class": "Invoice", "where": "state = \"approved\" AND total > 1000", "order": ["id"], "fields": ["supplier", "total"]}' --xlsx --out approved.xlsx
```

The sheet of the file, named `Invoice`, with its first line kept in
sight:

<!-- ann: the export, the sheet -->
| id | state | supplier | total |
|---|---|---|---|
| FV/2026/11/0042 | approved | Ink and Son | 1250.50 |
| FV/2026/11/0043 | approved | Paper Mill | 2400.00 |

The two totals are numbers in the file, and not texts that look like
numbers. That is the point of the Excel file. A file of text has to
choose a mark between its values and a mark inside a number, and the
reader's spreadsheet expects the marks of the reader's language: a
comma in a number and a semicolon between values in Polish, a point and
a comma in English. Whoever writes such a file guesses the reader. An
Excel file has no such marks. It keeps the number, and the program that
opens it shows the number its reader's way: `1250,50` on a machine set
to Polish, `1250.50` on one set to English, and both add it up. So
nothing in the export is written in a locale, neither the server's nor
the tool's.

What a cell holds follows the type the class gives the variable, which
the tool asks the server for:

<!-- a cell of an Excel file -->
| The variable is | The answer writes | The cell holds |
|---|---|---|
| `text` | `"10.50"` | the text 10.50, whatever it looks like |
| `decimal` | `"1250.50"` | the number 1250.50, shown with its two digits after the point |
| `money` | `"1250.50 PLN"` | the number 1250.50, shown with its two digits and with PLN |
| `int` | `"12"` | the number 12 |
| `number` | `3.75` | the number 3.75 |
| `date` | `"2026-12-15"` | the day 15 December 2026 |
| `datetime` | `1794778200` | the moment 15 November 2026 at 21:30, in UTC |
| `bool` | `true` | true |
| `rows` | `[{"sku": "A4"}]` | the text `[{"sku":"A4"}]`, its JSON |
| computed, and its definition names no `type` | `"1250.50 PLN"` | the number 1250.50 with PLN: such a value is told by its form |
| computed, and its definition names no `type` | `"0042"` | the text 0042: digits that begin with a zero are no number |

- **A computed variable says its type** when its definition has a
  `type`, as [the class definition](class.md) allows. Without one the
  tool goes by the form the engine writes a value in: an amount, an
  exact number, a whole number, a day. A designer who means a text
  writes `"type": "text"`.
- **A number with more than fifteen digits is written as a text.** A
  number of an Excel file keeps fifteen, and the tool does not drop a
  digit.
- **A text is written as it is.** One that begins with `=` is a text in
  the file, and not a formula.
- **`--xlsx` goes with `--out`.** An Excel file is not printed to a
  terminal.
- **`--xlsx` is for `query` and `sql`.** With `sql` the columns are the
  ones of the statement, for a caller in the system group `analysts`
  ([Views and SQL](views-and-sql.md)), and a number is a number and a
  text a text. Any other command is refused with it, and nothing is
  sent.
- **A refusal writes no file**, and leaves the one that was there.

### For a program: CSV

`--csv` writes the same documents as text, for a program that reads
them:

<!-- ann: the export as text -->
```sh
fadodb exec query '{"class": "Invoice", "where": "state = \"approved\" AND total > 1000", "order": ["id"], "fields": ["supplier", "total"]}' --csv
```

<!-- ann: the export as text, printed -->
```text
id,state,supplier,total
FV/2026/11/0042,approved,Ink and Son,1250.50
FV/2026/11/0043,approved,Paper Mill,2400.00
```

It has one form on every machine: commas between the values, a point
in a number, UTF-8, each value as the server wrote it, a list or an
object as its JSON. That is what a program wants, and it is why this
file is not for a person's spreadsheet: one set to Polish, opened by a
double click, puts each line into a single cell.

### Into a file: `--out`

`--out FILE` writes what the tool would print to a file: an answer, an
export, the events of a stream. A shell does the same with `>`, but
some shells change what passes through them, Windows PowerShell among
them, and `--out` does not.

## The stream of changes

`subscribe` is answered with [a stream](the-stream-of-changes.md). The
tool prints each event on a line of its own, as it comes. Ann waits for
invoices that enter review:

<!-- ann: wait -->
```sh
fadodb exec subscribe '{"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"]}'
```

Once the server has opened the stream, the tool says
`fadodb exec: the stream is open` on standard error. From then on no
event is missed, so a script that listens and then acts waits for
those words first. Bob files one more invoice and submits it:

<!-- bob: one more -->
```sh
fadodb exec create '{"class": "Invoice", "id": "FV/2026/11/0044", "values": {"supplier": "Ink and Son", "total": "310.00"}}'
fadodb exec transition '{"class": "Invoice", "id": "FV/2026/11/0044", "transition": "submit"}'
```

and Ann's terminal prints a line:

<!-- ann: wait, a line -->
```jsonc
{"event_id":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE","class":"Invoice","doc_id":"FV/2026/11/0044","kind":"transition","name":"submit","from_state":"draft","to_state":"review","actor_id":"bob","actor_kind":"person","before_version":1,"after_version":2,"business_date":"2026-11-15","ts":1794733200,"origin":"command"}
```

How it ends:

<!-- how a stream ends -->
| What happens | The tool ends with |
|---|---|
| it is interrupted, which is how a caller closes a stream | 0 |
| the server ends the stream, and says why in a last entry, a refusal, which the tool prints to standard error | 1 |
| the connection breaks | 3 |

The tool does not open a broken stream again by itself. A script does:
it keeps the `event_id` of the last line, and gives it as `after` when
it sends `subscribe` again, so that no event is lost.

## A file on a document

The bytes of a file are no JSON, so they are not sent by `exec`. Two
words of the tool send them, with the same key and under the same rules
of the document: `fadodb file put` and `fadodb file get`. The class of
this chapter keeps the scan of an invoice in its variable `scan`
([Files](files.md) says what a file on a document is).

Bob files an invoice and puts its scan on it:

<!-- bob: the scan -->
```sh
fadodb exec create '{"class": "Invoice", "id": "FV/2026/11/0045", "values": {"supplier": "Paper Mill", "total": "96.00"}}'
fadodb file put scan-0045.pdf --class Invoice --id FV/2026/11/0045 --variable scan
```

`put` sends the bytes to the server, writes the file into the variable,
and prints the document after the change. Ann reads the scan:

<!-- ann: the scan -->
```sh
fadodb file get --class Invoice --id FV/2026/11/0045 --variable scan --out scan.pdf
```

| To | The line |
|---|---|
| put a file on a document | `fadodb file put FILE --class CLASS --id ID --variable VARIABLE` |
| send the bytes only, and be told the facts of the file, to give a variable in a command of your own | `fadodb file put FILE` |
| read the file a variable holds | `fadodb file get --class CLASS --id ID --variable VARIABLE --out FILE` |
| read one file of a list, or one an earlier version of the document held, by its hash | `fadodb file get --class CLASS --id ID --hash HASH --out FILE`, with `--version N` for an earlier version |

A file is read through the document that holds it, by a caller who may
read that variable; somebody who may not see the document is refused,
and nothing is written. `--name` and `--type` say the name a file is
kept under and its kind; left out, they are the name of the file, and
what the server reads from its first bytes.

## The help

`fadodb exec help` lists the commands, and `fadodb exec help` with a
name the members of one command and of its answer. Both come from the
engine's own table of the commands, and ask no server:

<!-- the help of a command -->
```text
transition - Moves a document along a named transition of its class.

It changes the database. In a Go program it is the call Transition.

The members of its object:
  class       text    needed  the name of the class
  id          text    needed  the id of the document
  transition  text    needed  the name of the transition
  values      object          values to assign in the same commit, by variable

And the members that mean the same in every command:
  key            text                  the idempotency key: the same command sent again with the same key makes no second commit and is answered as the first was
  business_date  text                  the business day of the commit, as "2026-11-13"; the server's day when left out
  reason         text                  why a content administrator acts or reads past a rule; a change keeps it on its event, a read in the log of reads
  for            text                  the id of whoever the caller acts for; let through only while a substitution says so
  version        whole number          the version of the document its sender read before deciding; when the document is at another, the command is refused with stale_version and nothing is written

The members of its answer:
  document  the document after the command
```

The help says what this build of the tool knows. A server may be newer
than the tool: a command the tool has not heard of is sent all the
same, and the server judges it.

## How the tool ends

`fadodb exec` and `fadodb file` end the same way:

<!-- how the tool ends -->
| It ends with | When |
|---|---|
| 0 | the command was carried out; the answer is on standard output |
| 1 | the server refused the command; the refusal is on standard error |
| 2 | the line could not be read: no key, an object that is not JSON, a name that is not a command's; nothing was sent. `fadodb file get` also ends so when the variable it names holds no file |
| 3 | no answer came through: the server was not reached, what answered is not a FadoDB server, or the answer was cut off |

After a 3 the tool cannot say whether the command was carried out: the
answer may have been lost on its way back. So a script gives every
command that writes a `key`, as [the commands](commands.md) says, and
sends it again after a 3: the same command sent again with the same
key makes no second commit, and is answered as the first was.

## Not here yet

- **CSV in another form.** `--csv` has one form. A program that wants
  semicolons, or a comma in a number, is not served yet.
- **A server with a sign-in of its own.** The tool sends a key, as
  `fadodb serve` wants it. A program that mounts the door with its own
  way of knowing a caller is called by its own means.
