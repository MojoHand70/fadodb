# ADR-0101: FadoDB in a program of any language: the library of C

- Status: Proposed; carries out ADR-0011 (its three functions, with the caller beside the command and two more functions), ADR-0017 (the library is the build without DuckDB, named `fadodb-core` after the programs, ADR-0089) and, in step B, ADR-0033 item 6 (a put and an open of files)
- Date: 2026-11-27

## Context

Phase 9 of the plan is embedding: a C ABI, a Python client, Python in
the same process through the C ABI, and a guide for Go, Node, Python
and .NET. Its test is that the same client tests pass over HTTP and in
the same process. The owner opened the phase on 2026-11-27, beside
Phase 8, which is still running.

Today only a Go program can hold a database in its own process. Any
other program has to run `fadodb serve` beside it and talk to it over
HTTP, even when nothing but that one program ever uses the database.
An agent written in Python, which is how most agents are written, gets
the engine only with a server running next to it.

Facts this record was written from, each checked in the code on the
day:

- **Every door already runs a command the same way.** `fado.DB.Exec`
  takes the caller, the name of a command and its object, and answers
  one JSON object (ADR-0055, item 10). The HTTP door, `fadodb exec` and
  the door for agents all call it. So the library has nothing of its
  own to say about documents: it is a door like the others.
- **Who calls never travels inside a command** (ADR-0055, item 4). The
  HTTP door works it out from a key; a Go program gives a `Principal`.
  The library needs a third way.
- **A command that answers a stream is refused by `Exec`.**
  `subscribe` is answered `not_supported`, and `wait_for_changes`
  answers the same events one call at a time (ADR-0064).
- **Anything that needs cgo lives in a module of its own** (ADR-0002,
  Accepted). A library of C is built with `-buildmode=c-shared`, which
  needs cgo and a C compiler.
- **The library is the build without DuckDB** (ADR-0017, Accepted). The
  owner named the programs on 2026-11-25: `fadodb` is the one with
  DuckDB and `fadodb-core` the small one (ADR-0089).
- **A Go library loaded into a process brings the Go runtime with it.**
  A process can hold one such runtime: two Go libraries loaded into
  one program do not work together. This is Go's limit, not ours.
- **The settings of `fadodb serve` already name the things a database
  is opened with**: `timezone`, and the outbox and limits in words with
  their unit, such as `command_seconds` (ADR-0059).

## Decision

1. **The library has five functions**, declared in `fadodb.h`:

   ```c
   typedef uint64_t fado_db;

   fado_db     fado_open(const char *path, const char *options, char **refusal);
   char       *fado_exec(fado_db db, const char *caller, const char *command, const char *object);
   int         fado_close(fado_db db, char **refusal);
   void        fado_free(void *answer);
   const char *fado_version(void);
   ```

   The three of ADR-0011, and two that a library of C cannot do
   without: one that gives back the memory of what the library answered
   (an answer, a refusal, and from step B the bytes of a file, so it
   takes any pointer), and one that says which version the program
   loaded.

   Ann of sales and Bob of accounting, in a program of C:

   ```c
   #include <stdio.h>
   #include "fadodb.h"

   int main(void) {
       char *refusal = NULL;
       fado_db db = fado_open("books.fado", "{\"timezone\": \"Europe/Warsaw\"}", &refusal);
       if (db == 0) {
           fprintf(stderr, "%s\n", refusal);
           fado_free(refusal);
           return 1;
       }
       /* The class Invoice of the manual's chapter "Naming the caller" was
          defined by a designer, with the command define_class. */
       const char *ann = "{\"id\": \"ann\", \"kind\": \"person\", \"groups\": [\"sales\"]}";
       const char *bob = "{\"id\": \"bob\", \"kind\": \"person\", \"groups\": [\"accounting\"]}";

       char *answer = fado_exec(db, ann, "create",
           "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"number\": \"FV/2026/11/0042\"}}");
       puts(answer);   /* {"document":{"class":"Invoice","id":"FV/2026/11/0042",…,"state":"open",…}} */
       fado_free(answer);

       answer = fado_exec(db, bob, "transition",
           "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}");
       puts(answer);   /* {"error":{"code":"guard_false",…}}: the invoice has no account yet */
       fado_free(answer);

       fado_close(db, NULL);
       return 0;
   }
   ```

2. **The caller is the second argument of `fado_exec`**, a JSON object
   with the members a user has in the command set:

   ```json
   {"id": "ann", "kind": "person", "groups": ["sales"],
    "system_groups": ["designers"], "today": "2026-11-27"}
   ```

   Each member may be left out. `NULL`, `""` and `{}` are nobody, as a
   `Principal` left empty is in Go, and the rules of each class decide
   what nobody may do. The engine trusts the caller as it trusts a Go
   program's `Principal`: the program that loaded the library has
   already said who it is (ADR-0027). `today` is the caller's business
   day, the business date of a commit that gives none (ADR-0026). Whom
   the caller acts for is not here: it is the command's `for`, which the
   engine checks (ADR-0053). A member the caller does not take is
   refused with `bad_json` and its place, `/name`, and so is one of the
   wrong JSON type.

3. **A command and its answer are the command set's, byte for byte.**
   The same name, the same object and the same answer as `POST
   /v1/<command>` on a server. An answer with `error` at its top is a
   refusal, and its `code` is what a program branches on. `NULL` and
   `""` for the object are `{}`. `subscribe` is refused with
   `not_supported`; a program hears changes with `wait_for_changes`,
   which waits on its own thread up to the seconds it is given.

4. **The options of `fado_open` are a JSON object, named as the
   settings of `fadodb serve` name the same things.** Every member may
   be left out, and `NULL` or `""` is `{}`:

   ```json
   {"timezone": "Europe/Warsaw", "synchronous": "FULL",
    "max_question_seconds": 5, "max_text": 100000,
    "max_indexed_rows": 100000, "max_blob_bytes": 33554432,
    "blob_store": "sqlite", "host_answers_for": false,
    "outbox": {"manual": false, "workers": 4, "max_attempts": 10,
               "backoff_seconds": 1}}
   ```

   They are the fields of `fado.Options` that are data. A blob store of
   the embedder's own (`"external"`) is a Go value and is refused with
   `bad_value`. A member the options do not take is refused with
   `bad_json` and its place, `/outbox/retries`. More members may be
   added later, as `/v1` grows (ADR-0055, item 12); none is renamed.

5. **A database is a number, never a pointer.** `0` is none. A number
   is given once in the life of a process, so a number kept after its
   database was closed reaches no other database: a command sent to it
   is refused with `closed`. A program that confuses two numbers gets a
   refusal, never a crash.

6. **Every function may be called from any thread, at the same time as
   any other**, on one database or on several. Commands on one database
   run side by side, as they do in a server. `fado_close` first answers
   the commands that wait, so a `wait_for_changes` does not hold it for
   its sixty seconds; then it lets the running ones finish; then it
   closes the file.

7. **Text in and out is UTF-8, ended by a zero byte.** JSON never
   holds a zero byte outside an escape, so nothing is cut short. What a
   program gives is copied before the call returns. What the library
   answers belongs to the program, which gives it back with `fado_free`
   once. `fado_version` answers the library's own memory, never freed.

8. **A failure that is no refusal is answered `internal` with its
   words.** Over HTTP the words stay in the server's log and the caller
   reads only that the engine failed (ADR-0055, item 7). Here the
   program that loaded the library is the one that runs the engine, and
   it has no other log, so the words are in the message.

9. **A panic in the engine ends the call, not the program.** It is
   answered `internal` with its words and the first lines of the stack.
   A library that takes its host down on a fault would be worse than
   the fault.

10. **The library is built in a module of its own, `cabi/`, without
    DuckDB** (ADR-0002, ADR-0017), with `go build -buildmode=c-shared`.
    Its name follows the programs (ADR-0089): `libfadodb-core.so` on
    Linux, `libfadodb-core.dylib` on macOS and `fadodb-core.dll` on
    Windows. `make library` puts it in `bin/` with `fadodb.h` beside it.
    About 16 MB on Linux, with the sender of the requests of actions
    (step B). `fadodb.h` is written by hand, for people to
    read, and a test holds it to the functions Go says the library
    exports.

11. **One library in a process.** A program loads the library once and
    opens as many databases through it as it needs. It may not load a
    second library built in Go beside it (Go's limit, see Context). The
    guide of the phase says so.

12. **How it is tried.** The Go tests of the module try every function
    without a C compiler. A program of C, `cabi/testdata/try.c`, is
    compiled against `fadodb.h`, linked to the library and run, on
    Linux, macOS and Windows, on every pull request: Ann's invoice,
    Bob's account, the agent who may not book, and sixteen threads that
    make invoices at once. The tests of the Python client, over HTTP and
    in the same process, are the test of the phase (step C).

13. **The steps of the phase**, one pull request each:

    - **A**, this record and the library: the five functions, the job
      on GitHub.
    - **B**, files and requests: a put and an open of a file (ADR-0033,
      item 6), and the hosts the declared actions of a database may
      call, with their secrets, in the same words as `fadodb serve`
      (ADR-0059, items 4 and 5).
    - **C**, Python: one client that talks to a server over HTTP or to
      the library in the same process, with no package to install
      beside it, and one set of tests run both ways.
    - **D**, releases: the library for each system and the Python
      package in every release.
    - **E**, the guide: embedding FadoDB in Go, Node, Python and .NET.

    Letters and not numbers, so that no step of any phase is numbered
    thirteen.

## Settled while building

Mine, while building step B, each for the owner to strike:

- **Three functions for files**, one for each address of ADR-0055,
  item 9, named as the door for agents names its tools:

  ```c
  char *fado_put_file(fado_db db, const char *caller, const char *object, const void *bytes, size_t length);
  char *fado_get_file(fado_db db, const char *caller, const char *object, void **bytes, size_t *length);
  char *fado_get_blob(fado_db db, const char *caller, const char *object, void **bytes, size_t *length);
  ```

  Each takes the database, the caller and one JSON object, as
  `fado_exec` does, and the bytes beside them, and answers one JSON
  object. Ann puts the scan of a taxi receipt:
  `fado_put_file(db, ann, "{\"name\": \"taxi.pdf\", \"media_type\":
  \"application/pdf\"}", scan, length)` answers `{"file": {"hash":
  "d694…", "name": "taxi.pdf", "media_type": "application/pdf",
  "size": 48213}}`, the facts she then commits on her claim with `set`.
  Bob reads it with `{"class": "Claim", "id": "C-7", "hash": "d694…"}`,
  under the rules of the claim, and may give `version`, `for` and
  `reason` as over HTTP. The bytes are the program's to give back with
  `fado_free`, which is why it takes any pointer. A file is read whole
  into memory: a file is at most the database's `max_blob_bytes`, 32 MiB
  unless raised.
- **The hosts an action may call, and its secrets**, as `fadodb serve`
  has them (ADR-0059, items 4 and 5): the option `outbound` in the
  words of its settings, and `secrets_dir`. Without `outbound` the
  database sends nothing, and a definition with a request is refused,
  as in a Go program that gives no `Outbound`. A secret is the file of
  the directory or the variable `FADODB_SECRET_<CONNECTION>__<NAME>` of
  the program's environment, read again for every request. The two
  share one piece of Go, `internal/secret`, taken out of the server, so
  that they cannot drift apart; its words say "the program" where the
  server's say "the server".

Mine, while building step C:

- **One package, `fadodb`, in `clients/py/`, with nothing but Python's
  own library.** `fadodb.connect(url, key)` is a server, as the user
  whose key it is; `fadodb.open(path, caller=…, options=…)` is the file
  in the same process, through the library. Both give a `Database` with
  the same calls:

  ```python
  ann = fadodb.open("books.fado", caller={"id": "ann", "kind": "person", "groups": ["sales"]})
  bob = ann.as_caller({"id": "bob", "kind": "person", "groups": ["accounting"]})
  ann.exec("create", {"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042"}})
  try:
      bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
  except fadodb.Refused as refusal:
      print(refusal.code)    # guard_false
  ```

- **`exec` with the name of the command is the whole of it**, as
  `fado_exec` and the tools of the door for agents are. The plan said
  the clients are generated from the OpenAPI file; a method for each
  command would add nothing an agent needs and one more thing to keep
  in step with the table, so none is generated. A refusal is raised as
  `fadodb.Refused` with `code`, `message`, `hint`, `path` and the whole
  `answer`; no answer at all is `fadodb.NoAnswer`.
- **`as_caller` only in the same process.** Over HTTP the key says who
  calls, and `as_caller` raises `TypeError`.
- **`subscribe` is refused the same both ways**, `not_supported`,
  before anything is sent: over HTTP its answer would be a stream that
  `exec` cannot return. `wait_for_changes` answers the same events.
- **Files**: `put_file(bytes, name=…, media_type=…)` answers the facts;
  `get_file(class_, id, hash, version=…, for_=…, reason=…)` and
  `get_blob(hash, reason=…)` answer the facts and the bytes. Over HTTP
  the facts come from the headers of the reply. A file of no known type
  is `application/octet-stream` both ways, as a server serves it.
- **Where the library is found**: the path given, the variable
  `FADODB_LIBRARY`, `fadodb/lib` beside the package (where a release
  will put it, step D), or the one the system finds. A process loads
  one; asking for another is refused.
- **The test of the phase** is `clients/py/tests/test_both_ways.py`:
  one set of tests run as a server's callers, against a `fadodb-core
  serve` the tests start with a user and a key for each person, and in
  the same process with the same people. GitHub runs it on Linux,
  macOS and Windows with the machine's own Python.

Mine, while building step D:

- **The library carries the database of time zones**, as both
  programs do (ADR-0093, item 3). Found on Windows by the first release
  build of step D: Windows has no such database, a library built
  without one opened no database whose zone is `Europe/Warsaw`, and the
  module's own tests had passed only because Go was on the machine.
- **The library for the five systems the program `fadodb` is built
  for**: Linux for Intel and ARM, built on Ubuntu 22.04 so that it
  needs no newer C library than 2.35; macOS for Apple and Intel; and
  Windows for Intel, where it may need no library of the compiler it
  was built with. Windows on ARM has none: its C compiler is not on
  GitHub's machines. `scripts/library.sh` builds, tries and packs it on
  a machine of each system, as `scripts/program.sh` does the program.
- **What a release carries**: for each system
  `fadodb-core-library_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows)
  with the library, `fadodb.h`, the licence and the README of `cabi/`;
  and the Python package as wheels, one for each system with the
  library inside, in `fadodb/lib`, where `fadodb.open` finds it, and one
  for every system without it, for a program that only talks to a
  server. A wheel for Linux is named after the C library the library
  needs (`manylinux_2_35_x86_64`), one for macOS after the oldest macOS
  Go builds for (`macosx_12_0_arm64`).
- **The wheel is written by `clients/py/wheel.py`** with Python's own
  `zipfile`, from what `pyproject.toml` says: a wheel is a zip and three
  files that say what it is, so a release fetches no build tool from
  the network to make one.
- **Every pull request builds, tries and packs all five**, as it does
  the program, so that a release cannot be the first to find a system
  it does not build on. Each wheel is installed with pip and its tests
  run in the same process through the library inside it. A release
  keeps the files and attaches them.
- **Published on PyPI as `fadodb`, on the owner's word of
  2026-11-27** ("Yes, publish it on PyPI as fadodb"): every release
  publishes its wheels there, so that `pip install fadodb` finds them,
  from a job of its own that the release neither waits for nor fails
  with, as the browser VM's on npm. PyPI trusts the release workflow by
  a trusted publisher setting for the environment `pypi`, which the
  owner makes on PyPI; no token is kept anywhere. A tag of a version
  before its release, `v1.0.0-rc.1`, is `1.0.0rc1` to Python.

## Left out

Each of these is named so that it is not mistaken for decided:

- **Actions that the host program implements itself**, in its own
  language, as a Go program gives `Options.Actions`. A database opened
  through the library runs its own rules and the engine's actions; an
  action whose function would be the host's dies saying it has none,
  as under `fadodb serve` (ADR-0059). A record of its own takes up a
  function of C called back by the library, when a host asks for it.
- **The stream of changes as a function called back.**
  `wait_for_changes` answers the same events.
- **A library with DuckDB**, `libfadodb`. ADR-0017 makes the library the
  build without it. The names leave room for it.
- **The DuckDB extension** that the plan keeps as an option for this
  phase. It waits for what the plan says it waits for.

## Consequences

- A program in any language that can call C can hold a FadoDB database
  in its own process: Python with `ctypes`, Node with an FFI, .NET with
  P/Invoke, Rust, Java with Panama. Each binding is a few lines over
  five functions, since everything else is the JSON of the command set.
- An agent talks to a database in its own process with exactly what it
  sends to a server, so it can move from one to the other without
  learning anything new.
- The repository has a third Go module. Like `analytics/go.mod`,
  `cabi/go.mod` is tidied when Dependabot moves a version in the core
  `go.mod`.
- GitHub runs three more jobs on every pull request, one for each
  system.
