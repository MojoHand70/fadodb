# ADR-0102: The program's own actions through the library, and the library with DuckDB

- Status: Proposed; amends ADR-0101 (two of what it left out: actions that the host program carries out itself, and a library with DuckDB; the library gains two functions)
- Date: 2026-11-27

## Context

On 2026-11-27, with the five steps of Phase 9 built (ADR-0101), the
owner asked about the steps the plan and that record left optional,
and was told which of them were worth doing: the program's own actions
first, the library with DuckDB second; not the DuckDB extension and
not the stream of changes as a function called back. He answered:
"yes, go ahead with 1 and 2". They are steps F and G of the phase.

Facts this record was written from, each checked on the day:

- **A Go program carries out actions itself.** It gives
  `Options.Actions`, a Go function by the name of each action, at every
  Open (ADR-0027, ADR-0045). An action whose definition has no
  `request` is such an action. The function is called with an
  `ActionCall`, whose JSON is the published `api/call.schema.json`:
  the very body a webhook receives. It answers values in the wire
  shape of a document's values, which the engine checks against the
  action's `returns` (`bad_result`), or an error, tried again with
  backoff, or one marked `Permanent`, dead at once.
- **Through the library no such function can be given.** A function
  is no JSON, so `fado_open` cannot take one, and an action of the kind
  dies saying it has no function, as under `fadodb serve` (ADR-0059).
  So an agent in Python can be the caller of its process, and not yet
  the one who carries out its steps: sends the e-mail, asks a model,
  books the payment.
- **The worker calls actions from threads of its own**, as many at
  once as `outbox.workers` says, four when it says nothing, and a
  `drain` runs them on the thread that sent it. Go calls a function of
  C from any of them; Python, through `ctypes`, takes its own lock
  for each call.
- **DuckDB's prebuilt library cannot be loaded by a running program.**
  The DuckDB module links DuckDB's prebuilt static library, as
  `fadodb` does. A library built so loads into a program of C linked to
  it, and into nothing that loads it later: Python refused it ("cannot
  allocate memory in static TLS block"), and with the room raised by
  hand it crashed. Python, Node and .NET all load a library later.
- **DuckDB's own shared library can.** DuckDB publishes it with every
  release, `duckdb-shared-libs-<system>.tar.gz`, and the Go driver
  links it instead with the build tag `duckdb_use_lib`. The driver
  records, in `duckdb-release.json`, the release it is built from
  (v2.0.0 today) and the checksum of each system's file. A library
  built so on Linux, 20 MB, with DuckDB's `libduckdb.so`, 96 MB, beside
  it, loaded into Python, opened a database, kept its copy and answered
  `flow` from the copy. That release has files for Linux on Intel and
  ARM, macOS (one file for both machines) and Windows on Intel and ARM.
- **The owner's rulings on the programs.** The one with DuckDB is
  plain `fadodb`, and the small one `fadodb-core` (2026-11-25); "the
  price of DuckDB is worth being paid". And the Python package is
  published on PyPI as `fadodb` (2026-11-27).

## Decision

### The program's own actions (step F)

1. **A database opened with a function of the program carries out its
   actions through it:**

   ```c
   typedef void (*fado_action)(void *context, uint64_t call, const char *action, const char *object);

   fado_db fado_open_with(const char *path, const char *options,
                          fado_action actions, void *context, char **refusal);
   int     fado_answer(uint64_t call, const char *answer);
   ```

   The options say which actions are the program's, by name:
   `{"actions": ["mail.send", "model.ask"]}`. `fado_open` is
   `fado_open_with` with no function; options that name actions without
   a function are refused with `bad_value`. A name that no definition
   has yet waits for `define_action`, as in Go.

2. **The function is called with the call as a webhook gets it**, the
   JSON of `api/call.schema.json`: the key, the attempt, the action,
   its arguments and the event that fired the rule, with the document.
   `context` is what the program gave `fado_open_with`, and `call` the
   number of this call.

3. **It answers before it returns, with `fado_answer`**, one JSON
   object: `{"result": {...}}`, what the action returns, by the names
   of its `returns`; or `{"error": {"message": "...", "permanent":
   true}}`, a refusal that asking again would not change, dead at once,
   and with `"permanent": false`, or none, a failure tried again. The
   library copies the answer, so the program owns no memory of it after
   the call. A function that returns without answering has failed, and
   the action is tried again. `fado_answer` answers `-1` for a call that
   was answered already or has returned.

4. **The function is called from any thread**, as many at once as
   `outbox.workers` says, and from the thread that sends `drain`. It
   may send commands to the database itself, as a Go function may.
   `fado_close` waits for the actions that run.

5. **Python gives a function by name:**

   ```python
   def send(call):
       mail.send(to=call["args"]["to"], subject=call["args"]["subject"])
       return {"sent": True}

   db = fadodb.open("books.fado", actions={"mail.send": send})
   ```

   The function takes the call as a `dict` and returns the action's
   result as one. `raise fadodb.Permanent("…")` is a refusal; any
   other exception is a failure tried again, with its words. Over HTTP
   the server carries out the actions, so `fadodb.connect` takes none.

### The library with DuckDB (step G)

6. **`libfadodb`, the engine of `fadodb`, beside `libfadodb-core`.**
   The same functions and `fadodb.h`; a database it opens keeps a copy
   of its documents in DuckDB beside its file, as `fadodb serve` does,
   and the questions that read everything are answered from it
   (ADR-0089). The options take `"analytics": {"memory": "2GB",
   "threads": 8}`, the words of the settings of `fadodb serve`.

7. **It is linked to DuckDB's own shared library**, which travels
   beside it: `libfadodb.so` and `libduckdb.so` on Linux,
   `libfadodb.dylib` and `libduckdb.dylib` on macOS, `fadodb.dll` and
   `duckdb.dll` on Windows. Each finds the other in its own folder. The
   build takes DuckDB's file from DuckDB's release for the version the
   Go driver is built from, and refuses one whose checksum is not the
   one the driver records.

8. **The same code, one build tag.** The module `cabi` builds both: the
   tag `duckdb` adds the analytical engine. The release builds and tries
   both on each system, as it does the two programs.

9. **The Python package `fadodb` carries the library with DuckDB**, by
   the owner's rule that the full one is plain `fadodb`: `pip install
   fadodb` gives a program in Python the fast answers. Its wheel for a
   system grows from about 7 MB to about 35 MB. The small library stays
   in the release's archives, for a program of C and for a Python
   program that names it with `FADODB_LIBRARY`.

10. **The systems are the five of `fadodb`**: Linux on Intel and ARM,
    macOS on Apple and Intel, Windows on Intel. DuckDB has a file for
    Windows on ARM, and GitHub's machines have no C compiler for it, so
    neither program nor library is built there.

## Settled while building

Mine, while building step G, each for the owner to strike:

- **Both libraries are built by every run of `scripts/library.sh
  build`**, on every pull request, and tried there: the program of C
  and the Python client in the same process against each, the tests
  only `libfadodb` has (a copy beside the database, its options), and
  the wheel installed with pip and tried with nothing named, which must
  find `libfadodb`.
- **Measured on Linux for Intel, 2026-11-27**: `libfadodb.so` 17.7 MB
  and DuckDB's `libduckdb.so` 96 MB; the archive 34.6 MB and the wheel
  34.6 MB; `libfadodb-core.so` 16.8 MB, its archive 6.7 MB.
- **The DuckDB module's own tests pass linked to DuckDB's shared
  library**, the test that only extensions built into the library are
  loaded among them (ADR-0089): the shared library carries what the
  copy needs.
- **DuckDB's notice travels with DuckDB**: the archive and the wheel of
  `libfadodb` carry `LICENSE-duckdb`, the MIT notice of the Stichting
  DuckDB Foundation, taken from the Go driver, which carries the same
  words.
- **`fadodb.open` looks for `libfadodb` before `libfadodb-core`**,
  beside the package and on the system; `FADODB_LIBRARY` names either.

## Left out

- **A second package on PyPI, `fadodb-core`**, for a Python program
  that wants the small library. It takes a second publisher on PyPI;
  a program names the small library with `FADODB_LIBRARY` meanwhile.
- **The stream of changes as a function called back**, and the DuckDB
  extension of the plan, as the owner was told on 2026-11-27.

## Consequences

- An agent in Python, Node or C# can be the one who carries out the
  steps of its process, under the same queue, retries and dead actions
  as a Go program's, and with the same call a webhook gets.
- A program in any language gets the answers of the analytical engine
  in its own process, at the size of DuckDB.
- A release carries twice as many libraries and Python wheels about
  five times larger.
