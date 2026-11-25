# Act only on the version you read

A manager reads a purchase, thinks, and approves it. Between the
reading and the approval somebody corrects the amount. The approval
must not land on numbers the manager never saw.

A command that changes a document may say which version of it its
sender read. When the document has moved on, the command is refused
and nothing is written.

## The table and the class

Tess, in treasury, keeps what a buyer of each grade may spend.

```json
{
  "name": "Limits",
  "columns": {"grade": "text", "limit": "decimal(2)"},
  "key": ["grade"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}
```

```json
{
  "name": "Purchase",
  "title": "\"Purchase \" & number",
  "variables": {
    "number": {"type": "text", "required": true},
    "amount": {"type": "decimal", "scale": 2, "required": true},
    "grade":  {"type": "text", "required": true},
    "limit":  {"type": "decimal", "scale": 2,
               "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
               "mode": "triggered", "on": ["enter:review"]}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "approved"], "final": ["approved"]},
  "transitions": [
    {"name": "file",    "from": "draft",  "to": "review"},
    {"name": "return",  "from": "review", "to": "draft"},
    {"name": "approve", "from": "review", "to": "approved",
     "guard": "NOT ISBLANK(limit) AND amount <= limit"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE",
    "transition": {
      "file":    "CREATEDBY() = USER()",
      "return":  "INGROUP(\"managers\")",
      "approve": "INGROUP(\"managers\")"
    }
  }
}
```

Nothing in the class is there for this. Every document has a version,
1 when it is created and one more with each commit, and every command
that reads a document answers it.

## What it does

The limit of grade B is 10,000.00. Ola writes purchases and Tomek is
a manager.

| Who | Does | Answer |
|---|---|---|
| Ola | writes purchase P-7 of 9,000.00 and files it | allowed, version 2 |
| Tomek | reads P-7 | version 2: 9,000.00 against 10,000.00 |
| Ola | corrects the amount to 9,900.00 | allowed, version 3 |
| Tomek | approves, and says he read version 2 | refused, `stale_version` |
| Tomek | reads P-7 again | version 3: 9,900.00 against 10,000.00 |
| Tomek | approves, and says he read version 3 | allowed, version 4 |
| Tomek's program | sends the same approval again with its key | answered as the first was, version 4 |

Tomek's first approval, and its answer:

```jsonc
{"class": "Purchase", "id": "P-7", "transition": "approve", "version": 2}
```

```json
{"error": {"code": "stale_version",
           "message": "Purchase P-7 is at version 3, and the command saw version 2",
           "hint": "read the document again and decide on what it holds now"}}
```

The log then says of the approval that its sender read version 3: the
event carries `seen_version`. The events of Ola, who named no version,
carry none.

## How it works

- **`version` is the version the sender read.** `set`, `reset`,
  `refresh`, `transition` and `delete` take it, and `preview` and
  `preview_delete` answer the refusal the command would get. In a Go
  program it is the option `fado.Saw`.
- **A command without it acts on the document as it is.** Nothing is
  required. A face and an agent send it with every change to a
  document they read first; a program that only writes leaves it out.
- **The loop of an agent** is read, decide, send with the version; on
  `stale_version`, read again and decide again. It never acts on a
  document it did not see, and the log shows that it did not.
- **A command sent again with its key is not compared again.** It is
  answered as the first was, so a caller that lost the answer is not
  refused by its own success.
- **A caller who may not read the document learns nothing.** It is
  told `unknown_document`, whatever version it names.
- **What the engine does by itself names no version**: a transition
  that fires by itself, a timer, an action a rule queued. They read
  nothing.

## Many documents at once

`transition_many` and `preview_many` take `versions`, one for each id
and in their order, `null` for a document whose version is not named.
A document that has moved on is refused with `stale_version` among the
outcomes, and the others move. In Go it is `fado.SawEach`, with 0 for
none.

```jsonc
{"class": "Purchase", "transition": "approve", "ids": ["P-1", "P-2", "P-3"], "versions": [2, 2, null]}
```

## Two people at one document

Two people who change different values of one document at the same
time are no longer both taken when their faces send the version: the
second is refused and reads again. Putting the two changes together is
the work of the face.

See also: [Move many documents at once](move-many-documents.md),
[History and the log](../notions/history-and-the-log.md).
