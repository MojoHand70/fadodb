# The library of C

FadoDB in a program of any language, in its own process, with no server
([ADR-0101](../docs/adr/0101-the-library-of-c.md)). The library is the
engine of `fadodb-core`, without the analytical engine:

| System | File |
|---|---|
| Linux | `libfadodb-core.so` |
| macOS | `libfadodb-core.dylib` |
| Windows | `fadodb-core.dll` |

and [`fadodb.h`](fadodb.h) declares its five functions:

```c
fado_db     fado_open(const char *path, const char *options, char **refusal);
char       *fado_exec(fado_db db, const char *caller, const char *command, const char *object);
int         fado_close(fado_db db, char **refusal);
void        fado_free(char *answer);
const char *fado_version(void);
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
program branches on. Every function may be called from any thread. A
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

Releases do not carry the library yet; that is step D of Phase 9.
Python, over HTTP and in the same process, is step C.
