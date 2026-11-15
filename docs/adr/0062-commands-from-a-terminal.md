# ADR-0062: Commands from a terminal: `fadodb exec`

- Status: Proposed
- Date: 2026-11-15

## Context

The server of Phase 6 answers the command set over HTTP (ADR-0055), to
callers who send the key of a user (ADR-0057). The command-line tool
makes a database, its first administrator and a key, serves the
database, and checks a class or an action in a file. It could not send
a command. A person at a terminal, a script and an agent that works
through a shell reached a served database with `curl`: the address, the
key in a header, the kind of the body, and a refusal that ends the
program with 0, so that a script goes on after it.

The roadmap lists the command line for classes, documents, queries and
exports in this phase, and on 2026-11-15 the owner said it stays in.

Three accepted decisions already say most of what the tool does:

- **The tool reaches a served database through its server, with a
  key** (ADR-0059, item 7). The server holds the database alone.
- **A person calls with a key as an agent does, from the command-line
  tool** (ADR-0057, item 4). Only the first administrator and its key
  are made on the machine, by no user (item 7).
- **The help of the command-line tool comes from the table of the
  commands** (ADR-0055, item 10), as the HTTP addresses and the
  description for other languages do.

What they leave open is the line a person types, how the tool is told
the server and the key, what it prints, and how it ends.

## Decision

1. **One word sends every command: `fadodb exec COMMAND OBJECT`.** The
   command is a name of the table and the object is the JSON the
   command takes over HTTP, unchanged:

   ```
   fadodb exec transition '{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}'
   ```

   The tool sends it to `POST /v1/<command>` of the server and prints
   the answer. There is no word of its own for a class, a document or
   a query: that would be a second set of commands, to be kept in step
   with the first by hand, and an agent that has learned the commands
   would learn them again.

2. **The tool is a caller as any other.** It holds no database and
   opens none. A command it sends is judged by the rules of the
   database and written to the log under the user whose key it
   carries. It has no way past them: there is no flag that runs a
   command on a database on the machine as a user who did not prove to
   be that user.

3. **The server and the key come from the environment or from a flag,
   and the key is never an argument.**

   | What | Flag | Environment | Told nothing |
   |---|---|---|---|
   | the server | `--url` | `FADODB_URL` | `http://127.0.0.1:8080`, where `fadodb serve` listens when it is told nothing |
   | the key | `--key-file`, a file that holds it | `FADODB_KEY` | nothing is sent |
   | a certificate to know the server by | `--ca` | | the certificates the machine knows |
   | that the key may travel in the clear | `--plain-http` | | it may not, beyond this machine |

   A line of a terminal is kept in its history and shown to the other
   users of the machine, so no flag takes the key itself. The rule of
   `fadodb serve` for listening (ADR-0059, item 2) holds for calling:
   an address that begins with `http://` is taken for this machine
   only, unless `--plain-http` says that something between encrypts.
   The tool follows no redirect, so a key goes to the server it was
   addressed to and to nobody else. There is no flag that stops the
   tool from checking the server's certificate.

4. **The object is an argument, standard input, or a file.** `'{…}'`
   on the line, `-` for standard input, `@FILE` for a file, and `{}`
   when nothing is given. `--from MEMBER=FILE` adds one member whose
   value is the JSON a file holds, so that a class checked in its file
   is defined from the same file:

   ```
   fadodb class check invoice.json
   fadodb exec define_class --from definition=invoice.json
   ```

   The bytes of the file go into the object as they are. The tool
   checks that the object is one JSON object, and nothing more: what
   its members are is for the server to judge, in the engine's own
   words.

5. **The answer goes to standard output and a refusal to standard
   error, each as the server gave it**, set out over lines. Only the
   space between the values is the tool's: a number keeps its digits
   and a text its escapes.

6. **The tool ends with 0, 1, 2 or 3.**

   | It ends with | When |
   |---|---|
   | 0 | the command was carried out |
   | 1 | the server refused it |
   | 2 | the line could not be read, and nothing was sent |
   | 3 | no answer came through: the server was not reached, what answered is not a FadoDB server, or the answer was cut off |

   A refusal and an answer that did not come through are told apart,
   because a script sends a command again after the second and not
   after the first. After a 3 the command may have been carried out,
   so a command that writes is sent with a `key` (ADR-0055, item 3)
   and is then safely sent again. Whether an answer is a refusal is
   read from the status of the reply, and not from a member named
   `error`.

7. **An export is written in no locale. For a person it is an Excel
   file, `--xlsx`; for a program it is CSV, `--csv`.** Both are for
   `query` and `sql`. For `query` the columns are the id and the state
   of each document and then its variables: the ones asked for with
   `fields`, in that order, or every variable the documents hold. For
   `sql` they are the columns of the statement.

   A file of text has to choose a mark between its values and a mark
   inside a number, and a spreadsheet expects the marks of its
   reader's language: Excel set to Polish wants a semicolon and a
   comma, Excel set to English a comma and a point. Whoever writes the
   text guesses the reader, and neither the server nor the tool knows
   who will open the file. So for a person the tool writes no text. An
   Excel file keeps a number as a number and a day as a day, and the
   program that opens it shows them its reader's way. Checked on
   2026-11-15 in Excel 16 set to Polish: the file opens by a double
   click, an amount shows as `1 250,50 PLN`, a day as `15.12.2026`,
   and the amounts add up; the same documents as CSV land in one
   column.

   - **What a cell holds follows the type of the variable**, which the
     tool reads from the definition of the class, in each version the
     documents are of (`get_class`, open to every caller): a decimal,
     an amount and a whole number are numbers, an amount with its
     currency in how it is shown; a date is a day and a datetime a
     moment in UTC; a text is a text whatever it looks like; a list or
     an object is its JSON.
   - **A computed variable whose definition names no type** is told by
     the form the engine writes its value in. A designer who means a
     text names the type. The type the engine worked out is not in an
     answer today; when the class bundle of Phase 7 carries it, the
     tool reads it there.
   - **A number with more than fifteen digits is a text** in the file:
     a number of an Excel file keeps fifteen, and no digit is dropped.
   - **The writer is the tool's own**, on the standard library: a zip
     of six XML files (ADR-0002). The same answer makes the same file,
     byte for byte.
   - **CSV has one form on every machine**: commas, a point in a
     number, UTF-8, each value as the server wrote it. It is what a
     program reads. The tool reads no locale from the machine it runs
     on: the same line would then write different files on two
     machines.
   - **`--out FILE`** writes what the tool would print to a file. An
     Excel file goes nowhere else. Some shells change what passes
     through `>`, Windows PowerShell among them.

8. **`subscribe` prints one event to a line**, as the server wrote it,
   until the tool is interrupted, which is how a caller closes a
   stream (ADR-0058) and ends the tool with 0. When the server ends
   the stream, its last entry is a refusal, and the tool ends with 1;
   when the connection breaks, with 3. The tool does not open a broken
   stream again: a script gives the id of the last event as `after`.
   The tool knows a stream by the kind of the reply, and not by the
   name of the command. It says on standard error when the stream is
   open, so that a script which listens and then acts knows from when
   on no event is missed.

9. **`fadodb exec help` is the table of the commands**: the list of
   them, and for one command the members of its object and of its
   answer. It asks no server, so it says what the build of the tool
   knows. A name the tool does not know is sent all the same, because
   a server may be newer than the tool.

10. **A file on a document has two words of the tool: `fadodb file
    put` and `fadodb file get`.** Putting a file and reading one travel
    beside the commands (ADR-0055, item 9) and are not commands, so
    `exec` does not send them. `put` sends the bytes and, when a
    document and a variable are named, writes the file into the
    variable with `set`; without them it prints the facts of the file.
    `get` reads the file a variable holds, or one by its hash, through
    the document. They are told the server and the key as `exec` is,
    end as it does, and stand under the same rules: a caller who may
    not read the variable reads no file.

## Questions for the owner

1. **One word for every command, or a word for each thing.** Olga
   approves an invoice from a terminal. Proposed: `fadodb exec
   transition '{…}'`, as in item 1. The other way: words written by
   hand, such as `fadodb invoice approve FV/2026/11/0042`. They are
   shorter to type, and they are a second set of commands that an
   agent learns beside the first and that lags behind it.
2. **What an export is: ruled.** On 2026-11-15 the owner said of the
   first form of item 7, which wrote CSV only: "assuming server locale
   is wrong, it must be client locale set", and recalled the trouble
   with the separators of Excel in an earlier engine. Item 7 was
   rewritten on that word: no export is written in a locale; a person
   gets an Excel file, which the reader's own program shows the
   reader's way.
3. **CSV in another form.** A program of the accountant's wants
   semicolons and a comma in a number. Proposed: not built until one
   is named; then the caller says the two marks on the line, and the
   tool still reads no locale by itself. The other way: the tool takes
   the marks from the settings of the machine it runs on, which is
   right for Ann at her desk and wrong for an agent on a server that
   exports for her.

## Consequences

- A person, a script and an agent with a shell work on a served
  database with the commands they already know, and a command added
  to the table is there without a line of code in the tool.
- A command sent from a terminal leaves the same evidence as any
  other: the user of the key, on the event.
- A database that no server serves is not reached by `exec`. Serving
  it on the machine takes one line, and the tool's first three
  commands make its first administrator.
- JSON between single quotes is awkward in the shells of Windows.
  `@FILE`, `-` and `--from` are the way there.
- An accountant opens an export by a double click, in any language
  Excel is set to, and adds its numbers up. A text that begins with
  `=` is a text in the file, and not a formula.
- A computed variable with no type in its definition is told by the
  form of its value. The manual says so, and how a designer says it
  otherwise.
- The tool grows by the Excel writer and the file words: 0.2 MB, to
  14.5 MB of its budget of 15. The door for agents is still to come.

## Performance

The tool is a client, and the server's part of a command is measured
where the door is (ADR-0055, ADR-0059). The tool
adds the start of a process and a connection to each command, so a
program that sends many commands keeps a connection open and calls the
server itself.
