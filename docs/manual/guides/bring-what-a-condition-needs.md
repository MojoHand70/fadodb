# Bring what a condition needs

A manager may approve a purchase up to the limit of its grade. The
limits are in a table that the treasury keeps and changes. The
condition of the approval must stand on a number the document holds,
so that the log can show afterwards what the manager and the
condition saw, whatever the table says by then.

A guard reads its document and nothing outside it. What it needs from
the table is brought onto the document first, at a moment the class
names, and then decided on.

## The table and the class

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
               "mode": "triggered", "on": ["enter:review", "before:approve", "refresh"]}
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
  },
  "rights": {
    "*":      {"limit": {"write": "FALSE"}},
    "review": {"limit": {"write": ["managers"]}}
  }
}
```

Three things in the class do the work:

- `limit` is a triggered variable. It is brought when the purchase
  enters `review`, and again `before:approve`: when somebody asks to
  approve, before the guard is asked.
- The guard of `approve` reads `limit` and `amount`, two values of the
  document.
- The `write` right of `limit` says who may bring it again with a
  `refresh`: a manager, while the purchase is in review.

## What it does

The limit of grade B is 10,000.00. Tess is in treasury, Ola writes
purchases, Tomek is a manager, and Piotr has no part in this one.

| | Who | Does | Answer |
|---|---|---|---|
| 1 | Ola | writes purchase P-7 of 9,000.00, grade B | version 1 |
| 2 | Ola | files it | version 2 |
| 3 | Tomek | reads P-7 | version 2: 9,000.00 against 10,000.00 |
| 4 | Ola | corrects the amount to 9,900.00 | version 3 |
| 5 | Tomek | approves, and says he read version 2 | refused, `stale_version` |
| 6 | Piotr | refreshes `limit` | refused, `forbidden`, variable `limit` |
| 7 | Tess | lowers the limit of grade B to 5,000.00 | allowed |
| 8 | Tomek | reads version 3 and approves it | refused, `guard_false`; brought: `limit` 5,000.00 |
| 9 | Tomek | refreshes `limit` | version 4 |
| 10 | Ola | corrects the amount to 4,900.00 | version 5 |
| 11 | Tomek | reads version 5 and approves it | version 6, approved |
| 12 | Tess | raises the limit of grade B to 20,000.00 | allowed |
| 13 | Piotr | reads P-7 | approved: 4,900.00 against 5,000.00 |

The refusal of step 8 says what the guard saw. Tomek's screen still
showed 10,000.00, the limit of version 2:

```json
{"error": {"code": "guard_false",
           "message": "the guard of approve is not TRUE for the document in review",
           "hint": "the guard: NOT ISBLANK(limit) AND amount <= limit",
           "brought": {"limit": "5000.00"}}}
```

What the purchase held, version by version:

| Version | State | `amount` | `limit` | `limit` was worked out |
|---|---|---|---|---|
| 1 | draft | 9,000.00 | blank | never |
| 2 | review | 9,000.00 | 10,000.00 | at version 2 |
| 3 | review | 9,900.00 | 10,000.00 | at version 2 |
| 4 | review | 9,900.00 | 5,000.00 | at version 4 |
| 5 | review | 4,900.00 | 5,000.00 | at version 4 |
| 6 | approved | 4,900.00 | 5,000.00 | at version 6 |

The event of version 6 says that Tomek read version 5, and version 6
holds the limit the guard saw. Nothing Tess does afterwards changes
either.

## How it works

- **A guard may not look outside.** Written as
  `amount <= LOOKUP(Limits, limit, grade = THIS.grade)`, the guard is
  refused when the class is defined, with `not_allowed`: a guard is
  not stored, and a table keeps nothing of what a row was, so nothing
  would hold the number it saw.
- **`before:approve` brings the value in the same commit as the
  move.** The value is worked out after the values the command sets
  and before the guard. When the guard holds, the version the move
  makes keeps it.
- **A refused move writes nothing**, and says what was brought. In
  step 8 the purchase stays at version 3 with the limit of version 2.
- **The listing of transitions brings it too.** After step 7 `approve`
  is listed as closed with `guard_false`, though the purchase still
  holds 10,000.00: a button is offered by the number the move would
  see.
- **A value that cannot be brought refuses the move**, with the code
  of the failure and the variable.

## Which number the condition decides on

| The designer wants | Writes | The guard decides on |
|---|---|---|
| the limit of the day the purchase was filed | `"on": ["enter:review"]` | the number Tomek saw |
| the limit of the moment of approval | `"on": ["enter:review", "before:approve"]` | a number brought in the same commit, and kept by it |
| that an invoice number is free | `"on": ["before:send"]`, a count over the class | a count nobody can have changed since: the commits come one after another |

A value brought `before:` a move may differ from the one the person
saw, when its source moved in between. Both are kept: the version the
person saw, and the version the move made. A designer who wants them
to be the same number leaves `before:` out.

See also: [Triggered variables](../triggered-variables.md#before-a-transition),
[Act only on the version you read](act-on-what-you-read.md),
[Say who may bring a value again](who-may-bring-a-value-again.md).
