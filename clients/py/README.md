# FadoDB for Python

One package, two ways to the same database, and one way to use it
([ADR-0101](../../docs/adr/0101-the-library-of-c.md)). Nothing but
Python's own library is needed.

```python
import fadodb

# A server, as the user whose key it is:
db = fadodb.connect("https://fado.acme.example", key="fado_Q329CE63C9_…")

# Or the database file itself, in this process, through the library of
# C; the program says who is calling:
db = fadodb.open("books.fado", caller={"id": "ann", "kind": "person", "groups": ["sales"]})
```

A command is a name and one JSON object, and its answer is one JSON
object, a `dict`: the [commands](../../docs/manual/commands.md) a server
takes at `POST /v1/<command>`, answered the same. Ann of sales makes an
invoice, and Bob of accounting books it:

```python
ann = db                                  # opened as Ann above
bob = db.as_caller({"id": "bob", "kind": "person", "groups": ["accounting"]})

ann.exec("create", {"class": "Invoice", "id": "FV/2026/11/0042",
                    "values": {"number": "FV/2026/11/0042"}})
try:
    bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
except fadodb.Refused as refusal:
    print(refusal.code, refusal.hint)    # guard_false  the guard: NOT ISBLANK(account)

bob.exec("set", {"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}})
bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
```

A refusal is raised as `fadodb.Refused`: `code` is what a program
branches on, `message` and `hint` say why and what to send instead,
`path` names the member it is about, and `answer` is the whole answer,
with the findings of a refused definition under `diagnostics`.
`fadodb.NoAnswer` is raised when no answer came through; a command that
carries a `key` is then safely sent again.

`as_caller` is for a database in this process. Over HTTP the key says
who calls, so each user connects with their own key.

## Files

The bytes of a file travel beside the commands, and its facts in them:

```python
facts = ann.put_file(scan, name="taxi.pdf", media_type="application/pdf")
ann.exec("set", {"class": "Claim", "id": "C-7", "values": {"scan": facts}})
facts, data = bob.get_file("Claim", "C-7", facts["hash"])
```

## The program's own actions

An action whose definition has no request is carried out by the
program, in the same process: a function by the name of the action.
It is called with the call, as a webhook gets it, from a thread of the
worker, and returns what the action returns:

```python
def post(call):
    entry = books.post(amount=call["args"]["amount"], key=call["key"])
    return {"entry": entry}

shop = fadodb.open("shop.fado", actions={"books.post": post})
```

`raise fadodb.Permanent("the period 2026-11 is closed")` refuses the
call, and the action is dead at once; any other exception is a failure,
tried again later. The call's `key` is the same at every attempt, so
the function makes twice once by it. Over HTTP the server carries out
the actions, and `fadodb.connect` takes none.

## Waiting for a change

```python
after = bob.exec("wait_for_changes", {"classes": ["Invoice"]})["after"]
while True:
    heard = bob.exec("wait_for_changes", {"classes": ["Invoice"], "after": after, "seconds": 60})
    for event in heard["events"]:
        ...
    after = heard["after"]
```

`subscribe`, whose answer is a stream, is refused with `not_supported`:
`wait_for_changes` answers the same events one call at a time.

## The library

`fadodb.open` loads the library of C: the path given as `library`, or
the one the variable `FADODB_LIBRARY` names, or the one beside the
package in `fadodb/lib`, or the one the system finds: `libfadodb`, with
DuckDB, before `libfadodb-core`. A database opened through `libfadodb`
keeps a copy of its documents in DuckDB beside its file, and the
questions that read everything, such as `flow`, are answered from it;
`fadodb.open(path, options={"analytics": {"memory": "2GB"}})` tells it
how much it may take. `scripts/library.sh build` builds both into
`bin/library/`. A process holds one library, and opens
as many databases through it as it needs. Every call may be made from
any thread: the library lets go of Python's lock while it works.

From v0.9.0 on, every release carries the package as wheels: one for each system with
`libfadodb` and DuckDB's library inside, which `fadodb.open` finds with
nothing named, about 35 MB, and
`fadodb-<version>-py3-none-any.whl` for every system without it, for a
program that talks to a server. `pip install` takes the file of the
release. A release also publishes them on PyPI, so from v0.9.0 on
`pip install fadodb` takes the right one for the machine.

## Its tests

The same tests run both ways: over HTTP against a `fadodb-core serve`
they start, with a user and a key for each caller, and in the same
process with the same callers.

```sh
make build library
FADODB_PROGRAM=bin/fadodb-core FADODB_LIBRARY=bin/libfadodb-core.so \
    python3 -m unittest discover -s clients/py/tests
```
