# The library of C

FadoDB in a program of any language, in its own process, with no server
([ADR-0101](../docs/adr/0101-the-library-of-c.md)). There are two
libraries, as there are two programs:

| System | `libfadodb`, with DuckDB | `libfadodb-core` |
|---|---|---|
| Linux | `libfadodb.so` and `libduckdb.so` | `libfadodb-core.so` |
| macOS | `libfadodb.dylib` and `libduckdb.dylib` | `libfadodb-core.dylib` |
| Windows | `fadodb.dll` and `duckdb.dll` | `fadodb-core.dll` |

`libfadodb` is the engine of `fadodb`: a database it opens keeps a copy
of its documents in DuckDB beside its file, and the questions that read
everything are answered from it. It is linked to DuckDB's own library,
which travels beside it ([ADR-0102](../docs/adr/0102-the-programs-own-actions-and-the-library-with-duckdb.md)).
`libfadodb-core` is the engine of `fadodb-core`, without it.

and [`fadodb.h`](fadodb.h) declares its functions:

```c
fado_db     fado_open(const char *path, const char *options, char **refusal);
char       *fado_exec(fado_db db, const char *caller, const char *command, const char *object);
int         fado_close(fado_db db, char **refusal);
void        fado_free(void *answer);
const char *fado_version(void);

typedef void (*fado_action)(void *context, uint64_t call, const char *action, const char *object);
fado_db fado_open_with(const char *path, const char *options, fado_action actions, void *context, char **refusal);
int     fado_answer(uint64_t call, const char *answer);

char *fado_put_file(fado_db db, const char *caller, const char *object, const void *bytes, size_t length);
char *fado_get_file(fado_db db, const char *caller, const char *object, void **bytes, size_t *length);
char *fado_get_blob(fado_db db, const char *caller, const char *object, void **bytes, size_t *length);
```

A command is a name and one JSON object, and its answer is one JSON
object: the commands of the [command set](../docs/manual/commands.md),
the same a server takes at `POST /v1/<command>`, answered the same. Who
calls is said beside the command, as a JSON object:

```c
char *answer = fado_exec(db,
    "{\"id\": \"bob\", \"kind\": \"person\", \"groups\": [\"accounting\"]}",
    "transition",
    "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}");
/* {"document":{"class":"Invoice","id":"FV/2026/11/0042",…,"state":"booked",…}} */
fado_free(answer);
```

An answer with `error` at its top is a refusal, and its `code` is what a
program branches on. The bytes of a file travel beside the commands, as
over HTTP: `fado_put_file` keeps them and answers the facts a command
commits on a document, and `fado_get_file` reads them back under the
rules of the document. A database sends the requests of its actions
only to the hosts the option `outbound` lists, with the secrets of the
directory `secrets_dir` or of the program's environment, as
`fadodb serve` does. An action whose definition has no request is the
program's own: `fado_open_with` gives the library a function of the
program, which it calls with the call, as a webhook gets it, and which
answers with `fado_answer` ([ADR-0102](../docs/adr/0102-the-programs-own-actions-and-the-library-with-duckdb.md)). Every function may be called from any thread. A
process loads the library once.

## Building it

It needs Go and a C compiler:

```sh
make library                 # bin/libfadodb-core.so (or .dylib, .dll) and bin/fadodb.h
scripts/library.sh build     # both, in bin/library, DuckDB's library fetched from DuckDB's release
make cabi           # its tests, which compile testdata/try.c against it and run it
```

A program of C is compiled with the header and linked to the library:

```sh
cc -I bin -o books books.c -L bin -lfadodb-core
```

From v0.9.0 on, every release carries the library for Linux (Intel and ARM), macOS
(Apple and Intel) and Windows (Intel), as
`fadodb-core-library_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows),
with `fadodb.h`. `scripts/library.sh build`, `try` and `pack` do on one
machine what a release does on each. Python, over HTTP and in the same
process, is in [clients/py](../clients/py/README.md).
