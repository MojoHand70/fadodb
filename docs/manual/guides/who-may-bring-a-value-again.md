# Say who may bring a value again

A purchase brings its limit from a table when it is filed. A manager
decides on that number. Nobody else should be able to bring the limit
again in between, and so change the number the manager is looking at.

A triggered variable is typed by nobody: it changes by being brought
again, with `refresh`. So its `write` right says the one thing it can:
who may bring its value again, and in which state.

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
    "number":  {"type": "text", "required": true},
    "amount":  {"type": "decimal", "scale": 2, "required": true},
    "grade":   {"type": "text", "required": true},
    "limit":   {"type": "decimal", "scale": 2,
                "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
                "mode": "triggered", "on": ["enter:review", "refresh"]},
    "checked": {"type": "datetime", "formula": "NOW()",
                "mode": "triggered", "on": ["refresh"]}
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

The two lines of `rights` say it: `limit` may be brought again by a
manager while the purchase is in review, and by nobody anywhere else.
`checked` lists `refresh` and has no line: whoever may read the
purchase may bring it again.

## What it does

The limit of grade B is 10,000.00. Ola files purchase P-7 of 9,000.00,
and it brings that limit. Then Tess, in treasury, lowers the limit to
5,000.00. Tomek is a manager, and Piotr a colleague with no part in
the purchase.

| Who | Does | Answer |
|---|---|---|
| Piotr | refreshes `limit` | refused, `forbidden`, variable `limit` |
| Piotr | refreshes the purchase, naming no variable | allowed: `checked` is brought, `limit` stays 10,000.00 |
| Tomek | refreshes `limit` | allowed: `limit` is 5,000.00 |
| Tomek | returns the purchase to draft, and refreshes `limit` | refused, `forbidden`, variable `limit` |

Piotr's refusal:

```json
{"error": {"code": "forbidden", "variable": "limit",
           "message": "piotr may not bring limit again while the document is in review",
           "hint": "the write right of limit, which says who may bring its value again, from the cell review / limit: the groups managers"}}
```

## How it works

- **The right is asked of whoever sends the `refresh`**, in the state
  the document is in, as the right of an entered value is asked of
  whoever sets it. It is one table for both: who may edit what is
  entered, who may bring again what is brought.
- **A refresh that names a variable** its caller may not bring is
  refused, and nothing is written.
- **A refresh that names none** brings every variable its caller may
  bring and leaves the others as they are. It is refused only when the
  caller may bring none of them.
- **A variable with no line is open** to whoever may read the
  document, as before. A class that says nothing behaves as it did.
- **`access.update` is not asked.** Bringing a value again is no edit;
  a class that wants only editors to do it says so in the line.
- **A section closes many at once**: a line for a section stands for
  every variable in it, as for entered values.
- **What the document's own move brings is not asked.** `limit` is
  brought when Ola files the purchase, though she is no manager:
  whoever may make the move brings what the move brings. A rule of the
  class that refreshes its own document is not asked either.
- **A rule that refreshes another document** is asked the right of the
  person whose command set the rule off.
- **A table that brings a value asks nobody.** A variable that lists
  `table:<name>` is brought when the table changes, whoever changed
  it and whatever the line says about people. One line stops it: a
  `write` right of `FALSE` in a state, which is nobody, and a table
  is nobody too; see
  [Following a table](../triggered-variables.md#following-a-table).
- **A content administrator** gets past the line with a reason, which
  the event keeps, as past any right.

See also: [Triggered variables](../triggered-variables.md#refreshing),
[Act only on the version you read](act-on-what-you-read.md),
[Give each state its own editors](editors-per-state.md).
