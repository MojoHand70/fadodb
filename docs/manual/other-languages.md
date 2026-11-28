# In a program of another language

A program in any language can hold a FadoDB database in its own
process, with no server beside it, through the library of C
([ADR-0101](../adr/0101-the-library-of-c.md)). It sends the database the
same commands a server takes at `POST /v1/<command>`, and gets the same
answers. So a program, or an agent, that learned to talk to a server has
nothing new to learn here, and can move from one to the other.

This chapter tells one short story five times: in C, Python, Go, Node
and C#. A test runs each program on each push and compares what it
prints with this page, and holds each program shown here to its file
under [`examples/languages`](../../examples/languages).

A Go program needs no library: it holds the engine itself, as
[opening a database](opening-a-database.md) and
[naming the caller](naming-the-caller.md) show.

## The story

Dan designs the invoice. Ann of sales makes one. Bob of accounting tries
to book it before it has an account, and is refused; he gives it the
account and books it. The class, in `invoice.json` beside each program:

<!-- examples/languages/invoice.json -->
```json
{
  "name": "Invoice",
  "variables": {
    "number":  {"type": "text", "required": true},
    "account": {"type": "text"}
  },
  "states": {"initial": "open", "list": ["open", "booked"], "final": ["booked"]},
  "transitions": [
    {"name": "book", "from": "open", "to": "booked", "guard": "NOT ISBLANK(account)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"accounting\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"accounting\")",
    "delete": "FALSE",
    "transition": {"book": "INGROUP(\"accounting\") AND USERKIND() = \"person\""}
  }
}
```

Every program prints:

<!-- what every program prints -->
```text
dan defines the class Invoice
ann makes FV/2026/11/0042: open
bob books it: refused, guard_false
bob gives the account 400-01
bob books it: booked
```

## The library

There are two, as there are two programs
([ADR-0102](../adr/0102-the-programs-own-actions-and-the-library-with-duckdb.md)):
`libfadodb`, the engine of `fadodb`, which keeps a copy of a database's
documents in DuckDB beside its file and answers the questions that read
everything from it, with DuckDB's own library beside it; and
`libfadodb-core`, the engine of `fadodb-core`, small and without it.

| System | `libfadodb`, with DuckDB's library | `libfadodb-core` |
|---|---|---|
| Linux, Intel and ARM | `libfadodb.so` and `libduckdb.so` | `libfadodb-core.so` |
| macOS, Apple and Intel | `libfadodb.dylib` and `libduckdb.dylib` | `libfadodb-core.dylib` |
| Windows, Intel | `fadodb.dll` and `duckdb.dll` | `fadodb-core.dll` |

A release has each in an archive with `fadodb.h`:
`fadodb-library_<version>_<os>_<arch>.tar.gz` and
`fadodb-core-library_<version>_<os>_<arch>.tar.gz`, `.zip` on Windows.
`scripts/library.sh build` builds both from the repository into
`bin/library/`, with Go and a C compiler, and `make library` the small
one into `bin/`. `libfadodb` takes the options of its engine,
`{"analytics": {"memory": "2GB", "threads": 8}}`, which
`libfadodb-core` refuses. [`cabi/fadodb.h`](../../cabi/fadodb.h) declares its functions;
these five are the ones every program uses:

```c
fado_db     fado_open(const char *path, const char *options, char **refusal);
char       *fado_exec(fado_db db, const char *caller, const char *command, const char *object);
int         fado_close(fado_db db, char **refusal);
void        fado_free(void *answer);
const char *fado_version(void);
```

- **`fado_open`** opens the file, and makes it when it does not exist.
  It answers a number, `0` when the file was not opened, and then the
  refusal is in `refusal`. Its options are a JSON object, or nothing:
  `{"timezone": "Europe/Warsaw"}`, named as the settings of
  [`fadodb serve`](serving-a-database.md) name the same things.
- **`fado_exec`** runs one command as the caller and answers one JSON
  object, never nothing. An answer with `error` at its top is a
  refusal, and its `code` is what a program branches on, as over HTTP.
- **The caller** is said beside the command, as a JSON object with the
  members a user has: `{"id": "ann", "kind": "person", "groups":
  ["sales"]}`, and `system_groups` and `today` when they are needed.
  The engine trusts it, as it trusts a Go program: the program has said
  who is calling. Nothing at all is nobody. Whom the caller acts for is
  the command's `for`, which the engine checks
  ([naming the caller](naming-the-caller.md)).
- **`fado_free`** gives back every answer, refusal and file the library
  hands out, once. What a program gives the library is copied before
  the call returns.
- **Files** travel beside the commands: `fado_put_file`,
  `fado_get_file` and `fado_get_blob`, as in [files](files.md).

## C

<!-- examples/languages/c/books.c -->
```c
/*
 * The story of the chapter "In a program of another language", in C,
 * in the same process (docs/manual/other-languages.md). Run it in a
 * folder with invoice.json and nothing else:
 *
 *     cc -I bin -o books books.c -L bin -lfadodb-core
 *     ./books
 *
 * with fadodb.h and the library in bin, as make library leaves them;
 * the system finds the library when it runs as it finds any other.
 *
 * A real program reads the answers with a library for JSON; this one
 * finds the two members it prints by hand, so that it needs nothing.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "fadodb.h"

/* member copies the text of the first member "name":"…" of an answer
 * into out. */
static const char *member(const char *answer, const char *name, char *out, size_t size) {
    char key[64];
    snprintf(key, sizeof key, "\"%s\":\"", name);
    const char *at = strstr(answer, key);
    out[0] = '\0';
    if (at != NULL) {
        at += strlen(key);
        size_t n = strcspn(at, "\"");
        if (n >= size) {
            n = size - 1;
        }
        memcpy(out, at, n);
        out[n] = '\0';
    }
    return out;
}

/* run sends a command and answers its answer, or stops the program when
 * the library gave none. */
static char *run(fado_db db, const char *caller, const char *command, const char *object) {
    char *answer = fado_exec(db, caller, command, object);
    if (answer == NULL) {
        fprintf(stderr, "no answer to %s\n", command);
        exit(1);
    }
    return answer;
}

int main(void) {
    FILE *f = fopen("invoice.json", "rb");
    if (f == NULL) {
        fprintf(stderr, "no invoice.json here\n");
        return 1;
    }
    char invoice[4096];
    size_t n = fread(invoice, 1, sizeof invoice - 1, f);
    invoice[n] = '\0';
    fclose(f);

    char *refusal = NULL;
    fado_db db = fado_open("books.fado", NULL, &refusal);
    if (db == 0) {
        fprintf(stderr, "%s\n", refusal);
        fado_free(refusal);
        return 1;
    }
    const char *dan = "{\"id\": \"dan\", \"kind\": \"person\", \"system_groups\": [\"designers\"]}";
    const char *ann = "{\"id\": \"ann\", \"kind\": \"person\", \"groups\": [\"sales\"]}";
    const char *bob = "{\"id\": \"bob\", \"kind\": \"person\", \"groups\": [\"accounting\"]}";
    char object[8192], id[64], state[64], code[64];

    snprintf(object, sizeof object, "{\"definition\": %s}", invoice);
    char *answer = run(db, dan, "define_class", object);
    if (strncmp(answer, "{\"error\":", 9) == 0) {
        fprintf(stderr, "%s\n", answer);
        return 1;
    }
    fado_free(answer);
    printf("dan defines the class Invoice\n");

    answer = run(db, ann, "create",
                 "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"number\": \"FV/2026/11/0042\"}}");
    printf("ann makes %s: %s\n", member(answer, "id", id, sizeof id), member(answer, "state", state, sizeof state));
    fado_free(answer);

    answer = run(db, bob, "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}");
    if (strncmp(answer, "{\"error\":", 9) == 0) {
        printf("bob books it: refused, %s\n", member(answer, "code", code, sizeof code));
    }
    fado_free(answer);

    fado_free(run(db, bob, "set", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"account\": \"400-01\"}}"));
    printf("bob gives the account 400-01\n");

    answer = run(db, bob, "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}");
    printf("bob books it: %s\n", member(answer, "state", state, sizeof state));
    fado_free(answer);

    fado_close(db, NULL);
    return 0;
}
```

## Python

The package `fadodb` is in [`clients/py`](../../clients/py/README.md). It
needs nothing but Python's own library. From v0.9.0 on, a release carries a wheel
of it for each system with the library inside, which `fadodb.open` finds by
itself; `pip install` takes the file. Here `FADODB_LIBRARY` names the
library instead.

<!-- examples/languages/python/books.py -->
```python
# The story of the chapter "In a program of another language", in
# Python, in the same process (docs/manual/other-languages.md). Run it in
# a folder with invoice.json and nothing else:
#
#     python3 books.py
import json

import fadodb

with open("invoice.json", encoding="utf-8") as f:
    invoice = json.load(f)

db = fadodb.open("books.fado")
dan = db.as_caller({"id": "dan", "kind": "person", "system_groups": ["designers"]})
ann = db.as_caller({"id": "ann", "kind": "person", "groups": ["sales"]})
bob = db.as_caller({"id": "bob", "kind": "person", "groups": ["accounting"]})

dan.exec("define_class", {"definition": invoice})
print("dan defines the class Invoice")

made = ann.exec("create", {"class": "Invoice", "id": "FV/2026/11/0042",
                           "values": {"number": "FV/2026/11/0042"}})["document"]
print(f"ann makes {made['id']}: {made['state']}")

try:
    bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
except fadodb.Refused as refusal:
    print(f"bob books it: refused, {refusal.code}")

bob.exec("set", {"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}})
print("bob gives the account 400-01")

booked = bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042",
                                 "transition": "book"})["document"]
print(f"bob books it: {booked['state']}")

db.close()
```

A refusal is raised as `fadodb.Refused`, with `code`, `message`, `hint`,
`path` and the whole `answer`. Over HTTP the same program connects to a
server instead of opening the file, and each person connects with their
own key; nothing else changes:

```python
ann = fadodb.connect("https://fado.acme.example", key="fado_…")
```

## Go

A Go program holds the engine itself, so it needs no library of C. It
may send the same commands with `db.Exec`, as here, or call
`db.Create`, `db.Transition` and the rest.

<!-- examples/languages/golang/main.go -->
```go
// Command golang is the story of the chapter "In a program of another
// language", in Go, with no library of C: a Go program holds the engine
// itself (docs/manual/other-languages.md). Run it in a folder with
// invoice.json and nothing else:
//
//	go run github.com/MojoHand70/fadodb/examples/languages/golang
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MojoHand70/fadodb/fado"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	invoice, err := os.ReadFile("invoice.json")
	if err != nil {
		return err
	}
	db, err := fado.Open("books.fado", fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	dan := fado.Principal{ID: "dan", Kind: "person", System: []string{"designers"}}
	ann := fado.Principal{ID: "ann", Kind: "person", Groups: []string{"sales"}}
	bob := fado.Principal{ID: "bob", Kind: "person", Groups: []string{"accounting"}}

	// The same commands as from any other language, with db.Exec; a Go
	// program may call db.DefineClass, db.Create and the rest as well.
	if _, err := db.Exec(ctx, dan, "define_class", []byte(`{"definition": `+string(invoice)+`}`)); err != nil {
		return err
	}
	fmt.Println("dan defines the class Invoice")

	var made struct {
		Document struct{ ID, State string } `json:"document"`
	}
	answer, err := db.Exec(ctx, ann, "create", []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042"}}`))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(answer, &made); err != nil {
		return err
	}
	fmt.Printf("ann makes %s: %s\n", made.Document.ID, made.Document.State)

	book := []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"}`)
	if _, err := db.Exec(ctx, bob, "transition", book); err != nil {
		fmt.Printf("bob books it: refused, %s\n", fado.ErrorCode(err))
	}

	if _, err := db.Exec(ctx, bob, "set", []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}}`)); err != nil {
		return err
	}
	fmt.Println("bob gives the account 400-01")

	answer, err = db.Exec(ctx, bob, "transition", book)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(answer, &made); err != nil {
		return err
	}
	fmt.Printf("bob books it: %s\n", made.Document.State)
	return nil
}
```

## Node

Node calls a library of C through a package; this program uses
[koffi](https://koffi.dev), with `npm install koffi`. `FADODB_LIBRARY`
names the library.

<!-- examples/languages/node/books.mjs -->
```js
// The story of the chapter "In a program of another language", in
// Node, in the same process (docs/manual/other-languages.md). Node calls
// a library of C through a package, koffi; nothing else is needed. Run
// it in a folder with invoice.json and nothing else:
//
//     npm install koffi
//     FADODB_LIBRARY=/path/to/libfadodb-core.so node books.mjs
import { readFileSync } from "node:fs";
import koffi from "koffi";

const lib = koffi.load(process.env.FADODB_LIBRARY);
// An answer is kept as a pointer, so that it is given back with fado_free.
const fado_open = lib.func("uint64_t fado_open(const char *path, const char *options, _Out_ void **refusal)");
const fado_exec = lib.func("void *fado_exec(uint64_t db, const char *caller, const char *command, const char *object)");
const fado_close = lib.func("int fado_close(uint64_t db, void *refusal)");
const fado_free = lib.func("void fado_free(void *answer)");

// text reads an answer of the library and gives it back.
function text(pointer) {
  try {
    return koffi.decode(pointer, "char", -1);
  } finally {
    fado_free(pointer);
  }
}

// exec runs a command as the caller; a refusal is thrown with its code.
function exec(db, caller, command, object) {
  const answer = JSON.parse(text(fado_exec(db, JSON.stringify(caller), command, JSON.stringify(object))));
  if (answer.error) {
    throw Object.assign(new Error(answer.error.message), { code: answer.error.code });
  }
  return answer;
}

const invoice = JSON.parse(readFileSync("invoice.json", "utf8"));
const refusal = [null];
const db = fado_open("books.fado", "{}", refusal);
if (!db) {
  throw new Error(text(refusal[0]));
}
const dan = { id: "dan", kind: "person", system_groups: ["designers"] };
const ann = { id: "ann", kind: "person", groups: ["sales"] };
const bob = { id: "bob", kind: "person", groups: ["accounting"] };

exec(db, dan, "define_class", { definition: invoice });
console.log("dan defines the class Invoice");

const made = exec(db, ann, "create", { class: "Invoice", id: "FV/2026/11/0042", values: { number: "FV/2026/11/0042" } }).document;
console.log(`ann makes ${made.id}: ${made.state}`);

const book = { class: "Invoice", id: "FV/2026/11/0042", transition: "book" };
try {
  exec(db, bob, "transition", book);
} catch (refused) {
  console.log(`bob books it: refused, ${refused.code}`);
}

exec(db, bob, "set", { class: "Invoice", id: "FV/2026/11/0042", values: { account: "400-01" } });
console.log("bob gives the account 400-01");

const booked = exec(db, bob, "transition", book).document;
console.log(`bob books it: ${booked.state}`);

fado_close(db, null);
```

## C# and .NET

.NET calls the library with P/Invoke and needs no package. The project:

<!-- examples/languages/dotnet/books.csproj -->
```xml
<Project Sdk="Microsoft.NET.Sdk">
  <!-- The story of the chapter "In a program of another language", in C#
       (docs/manual/other-languages.md): dotnet run, in a folder with
       invoice.json, with FADODB_LIBRARY naming the library. -->
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net10.0</TargetFramework>
    <Nullable>enable</Nullable>
    <ImplicitUsings>enable</ImplicitUsings>
    <InvariantGlobalization>true</InvariantGlobalization>
  </PropertyGroup>
</Project>
```

And the program, run with `dotnet run`; `FADODB_LIBRARY` names the
library:

<!-- examples/languages/dotnet/Program.cs -->
```csharp
// The story of the chapter "In a program of another language", in C#,
// in the same process (docs/manual/other-languages.md). .NET calls the
// library of C with P/Invoke; nothing else is needed. Run it in a folder
// with invoice.json and nothing else:
//
//     FADODB_LIBRARY=/path/to/libfadodb-core.so dotnet run --project /path/to/books.csproj
using System.Runtime.InteropServices;
using System.Text.Json.Nodes;

// The library is the file FADODB_LIBRARY names, or the one the system
// finds under its name.
NativeLibrary.SetDllImportResolver(typeof(Native).Assembly, (name, assembly, path) =>
    name == Native.Library && Environment.GetEnvironmentVariable("FADODB_LIBRARY") is { Length: > 0 } file
        ? NativeLibrary.Load(file)
        : IntPtr.Zero);

var invoice = JsonNode.Parse(File.ReadAllText("invoice.json"));
using var db = new Database("books.fado");
var dan = new JsonObject { ["id"] = "dan", ["kind"] = "person", ["system_groups"] = new JsonArray("designers") };
var ann = new JsonObject { ["id"] = "ann", ["kind"] = "person", ["groups"] = new JsonArray("sales") };
var bob = new JsonObject { ["id"] = "bob", ["kind"] = "person", ["groups"] = new JsonArray("accounting") };

db.Exec(dan, "define_class", new JsonObject { ["definition"] = invoice });
Console.WriteLine("dan defines the class Invoice");

var made = db.Exec(ann, "create", new JsonObject
{
    ["class"] = "Invoice", ["id"] = "FV/2026/11/0042",
    ["values"] = new JsonObject { ["number"] = "FV/2026/11/0042" },
})["document"]!;
Console.WriteLine($"ann makes {made["id"]}: {made["state"]}");

JsonObject Book() => new() { ["class"] = "Invoice", ["id"] = "FV/2026/11/0042", ["transition"] = "book" };
try
{
    db.Exec(bob, "transition", Book());
}
catch (Refused refused)
{
    Console.WriteLine($"bob books it: refused, {refused.Code}");
}

db.Exec(bob, "set", new JsonObject
{
    ["class"] = "Invoice", ["id"] = "FV/2026/11/0042",
    ["values"] = new JsonObject { ["account"] = "400-01" },
});
Console.WriteLine("bob gives the account 400-01");

var booked = db.Exec(bob, "transition", Book())["document"]!;
Console.WriteLine($"bob books it: {booked["state"]}");

// The five functions of fadodb.h this program uses. Text is UTF-8; an
// answer is kept as a pointer, so that it is given back with fado_free.
static class Native
{
    public const string Library = "fadodb-core";

    [DllImport(Library)]
    public static extern ulong fado_open([MarshalAs(UnmanagedType.LPUTF8Str)] string path,
        [MarshalAs(UnmanagedType.LPUTF8Str)] string options, out IntPtr refusal);

    [DllImport(Library)]
    public static extern IntPtr fado_exec(ulong db, [MarshalAs(UnmanagedType.LPUTF8Str)] string caller,
        [MarshalAs(UnmanagedType.LPUTF8Str)] string command, [MarshalAs(UnmanagedType.LPUTF8Str)] string obj);

    [DllImport(Library)]
    public static extern int fado_close(ulong db, IntPtr refusal);

    [DllImport(Library)]
    public static extern void fado_free(IntPtr answer);
}

// A database the library opened, closed when it is disposed.
sealed class Database : IDisposable
{
    readonly ulong n;

    public Database(string path)
    {
        n = Native.fado_open(path, "{}", out var refusal);
        if (n == 0)
        {
            throw new InvalidOperationException(Take(refusal));
        }
    }

    // Runs a command as the caller; a refusal is thrown with its code.
    public JsonObject Exec(JsonObject caller, string command, JsonObject obj)
    {
        var answer = JsonNode.Parse(Take(Native.fado_exec(n, caller.ToJsonString(), command, obj.ToJsonString())))!.AsObject();
        if (answer["error"] is JsonObject error)
        {
            throw new Refused((string)error["code"]!, (string)error["message"]!);
        }
        return answer;
    }

    static string Take(IntPtr answer)
    {
        try
        {
            return Marshal.PtrToStringUTF8(answer) ?? "";
        }
        finally
        {
            Native.fado_free(answer);
        }
    }

    public void Dispose() => Native.fado_close(n, IntPtr.Zero);
}

sealed class Refused(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}
```

## What every program minds

- **One library in a process.** The library brings Go's runtime with
  it, and a process holds one. A program loads the library once and
  opens as many databases through it as it needs; it does not load a
  second library built in Go beside it.
- **Any thread.** Every function may be called from any thread at the
  same time. Python lets go of its lock while the library works, so
  threads of Python run their commands side by side.
- **A database is a number.** A number is given once in the life of a
  process. A command sent to a number after `fado_close` is refused
  with `closed`, never a crash.
- **Waiting for a change** is `wait_for_changes`, which waits on its
  own thread up to the seconds it is given. `subscribe`, whose answer
  is a stream, is refused with `not_supported`.
- **A failure that is no refusal** is answered `internal` with its
  words, since the program that loaded the library has no other place
  to read them. A panic in the engine is answered the same way and
  leaves the program running.
