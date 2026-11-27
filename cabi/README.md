# The library of C

FadoDB in a program of any language, in its own process, with no server
([ADR-0101](../docs/adr/0101-the-library-of-c.md)). The library is the
engine of `fadodb-core`, without the analytical engine:

| System | File |
|---|---|
| Linux | `libfadodb-core.so` |
| macOS | `libfadodb-core.dylib` |
| Windows | `fadodb-core.dll` |

and [`fadodb.h`](fadodb.h) declares its functions:

```c
fado_db     fado_open(const char *path, const char *options, char **refusal);
char       *fado_exec(fado_db db, const char *caller, const char *command, const char *object);
int         fado_close(fado_db db, char **refusal);
void        fado_free(void *answer);
const char *fado_version(void);

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
`fadodb serve` does. Every function may be called from any thread. A
process loads the library once.

## Building it

It needs Go and a C compiler:

```sh
make library        # bin/libfadodb-core.so (or .dylib, .dll) and bin/fadodb.h
make cabi           # its tests, which compile testdata/try.c against it and run it
```

A program of C is compiled with the header and linked to the library:

```sh
cc -I bin -o books books.c -L bin -lfadodb-core
```

Every release carries the library for Linux (Intel and ARM), macOS
(Apple and Intel) and Windows (Intel), as
`fadodb-core-library_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows),
with `fadodb.h`. `scripts/library.sh build`, `try` and `pack` do on one
machine what a release does on each. Python, over HTTP and in the same
process, is in [clients/py](../clients/py/README.md).
