# The class definition

A class is one kind of document: an invoice, an expense, a permit. It
is written once, as one JSON document, and every document of the class
follows it. This chapter goes through the definition block by block.
If you have not built [your first application](first-application.md)
yet, start there.

## The blocks

| Block | What it says | Needed |
|---|---|---|
| `name` | the name of the class | always |
| `label`, `plural`, `description`, `icon`, `color` | how the class is shown to people | no |
| `title` | a formula that names each document | no; without it the title is the document's id |
| [`variables`](#variables) | what each document holds, and what is computed | always |
| [`states`](#states) | the states a document can be in | for a class that stores documents |
| [`transitions`](#transitions) | the moves between states | no |
| [`rules`](#rules) | what happens by itself when something changes | no |
| [`access`](#access) | who may see, create, change, delete and move a document | in practice always: a line left out lets nobody through |
| [`rights`](#rights) | who may write or see single variables, state by state | no |

The words in the first column are FadoDB's and are written exactly so.
A word the engine does not know is refused when the class is defined,
so a misspelt block never passes as an absent one.

## An example with every block

An expense that an employee files and a named approver decides. The
rest of the chapter takes its examples from it.

```json
{
  "name": "Expense",
  "label": "Expense",
  "plural": "Expenses",
  "description": "A cost that an employee files and their approver decides.",
  "title": "\"Expense of \" & owner",
  "variables": {
    "owner":      {"type": "text", "default": "USER()", "index": true},
    "approver":   {"type": "text", "required": true},
    "kind":       {"type": "text", "values": ["travel", "hotel", "meal", "other"], "default": "\"other\""},
    "amount":     {"type": "money", "scale": 2, "required": true},
    "spent_on":   {"type": "date", "default": "TODAY()"},
    "nights":     {"type": "int", "min": 0, "max": 60, "default": "0"},
    "paid":       {"type": "bool", "default": "FALSE"},
    "lines":      {"type": "rows", "schema": {"what": "text", "cost": "money"}},
    "tags":       {"type": "array<text>"},
    "large":      {"formula": "amount > 1000"},
    "decided_by": {"type": "text"}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"]},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed", "guard": "amount > 0"},
    {"name": "recall",  "from": "filed", "to": "draft"},
    {"name": "approve", "from": "filed", "to": "approved"},
    {"name": "reject",  "from": "filed", "to": "rejected"},
    {"name": "expire",  "from": "filed", "to": "expired", "after": "30d"}
  ],
  "rules": [
    {"on": "transition", "name": "approve", "do": [{"action": "set", "values": {"decided_by": "USER()"}}]},
    {"on": "transition", "name": "reject",  "do": [{"action": "set", "values": {"decided_by": "USER()"}}]}
  ],
  "access": {
    "read":   "owner = USER() OR approver = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"everyone\")",
    "update": "(owner = USER() AND STATE() = \"draft\") OR INGROUP(\"finance\")",
    "delete": "owner = USER() AND STATE() = \"draft\"",
    "transition": {
      "file":    "owner = USER()",
      "recall":  "owner = USER()",
      "approve": "approver = USER() AND CREATEDBY() <> USER()",
      "reject":  "approver = USER()",
      "expire":  "FALSE"
    }
  },
  "rights": {
    "*":     {"decided_by": {"write": "FALSE"}, "paid": {"write": ["finance"]}},
    "filed": {"tags": {"write": ["finance"]}}
  }
}
```

## Variables

Each entry is one variable: its name, and what it is.

```json
"amount":     {"type": "money", "scale": 2, "required": true},
```

| Word | Meaning |
|---|---|
| `type` | what kind of value it holds; see the table below |
| `required` | a document without a value for it is refused |
| `default` | a formula for the value a new document gets when the caller gives none |
| `formula` | the variable is computed, and nobody sets it |
| `values` | for a text: the list of values it may take |
| `min`, `max` | for a number: the range it must stay in |
| `scale` | for `decimal` and `money`: the number of decimal places, two when left out |
| `index` | keeps an index on the variable, so that finding documents by it is fast |
| `class` | for a `ref`: the class it points at |
| `schema` | for `object` and `rows`: the fields of the value |

The types:

| `type` | Holds | Written in a document as |
|---|---|---|
| `text` | text | `"ACME"` |
| `number` | a number, as a spreadsheet keeps it | `12.5` |
| `int` | a whole number | `3` |
| `decimal` | an exact number with a fixed scale | `"0.23"` |
| `money` | an exact amount with its currency | `"1000.00 PLN"` |
| `bool` | true or false | `true` |
| `date` | a calendar day | `"2026-12-01"` |
| `datetime` | a moment, in UTC | `"2026-12-01 09:30:00"` |
| `json` | any JSON value, kept as it is | `{"a": 1}` |
| `ref` | a pointer to a document of another class | `"Customer:C-17"` |
| `array<text>`, `array<number>` and so on | a list of values of one simple type | `["a", "b"]` |
| `object` | one value with named fields, by a schema | `{"city": "Gdańsk"}` |
| `rows` | a table inside the document, by a schema | `[{"what": "taxi", "cost": "40.00 PLN"}]` |

A variable is one of two kinds.

- **An input** has a `type`. A caller sets it, or its `default` does.
- **A computed variable** has a `formula`. The engine works it out
  again whenever a variable it reads changes, as a spreadsheet does.
  Its type follows from the formula.

```json
"large":      {"formula": "amount > 1000"},
```

A computed variable can also be worked out only at chosen moments,
such as the entry into a state, and kept in between: that is
`"mode": "triggered"`, an advanced technique with a chapter of its own
to come.

Some names are the engine's and cannot be used for a variable, among
them `id`, `title`, `state`, `version`, `created_at`, `created_by`,
`updated_at` and `updated_by`. The engine keeps these for every
document by itself.

## States

```json
"states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"]},
```

| Word | Meaning |
|---|---|
| `list` | every state, by name |
| `initial` | the state a new document starts in |
| `final` | the states in which a document is finished |

A document in a final state is frozen: its values cannot change, and
it can only move to another final state.

A class with no `states` block stores no documents. It can still be
evaluated, as a calculator: give it values and it answers with its
computed variables.

## Transitions

A transition is a move from one state to another.

```json
{"name": "file",    "from": "draft", "to": "filed", "guard": "amount > 0"},
```

| Word | Meaning |
|---|---|
| `name` | the name of the move; a caller takes the transition by this name |
| `from` | the state it leaves, or a list of states |
| `to` | the state it enters |
| `guard` | a formula over the document that must be TRUE for the move |
| `after` | the move is made by time: so long after the document entered the state, as `"30d"`, `"12h"`, `"90m"` or `"45s"` |
| `at` | the move is made by time: at a date or a moment the document holds, as `"ADDDAYS(spent_on, 60)"` |

A guard is about the document: is the amount above zero, is the
account filled in. Who may take the transition is a different question
and is answered under [`access`](#access). The engine tells the two
apart in its refusals: `guard_false` for the first, `forbidden` for the
second.

**Give every transition a name.** The history, the log and every
refusal identify a move by it.

**Automatic transitions.** A move the engine makes by itself, with
nobody asking, is called automatic, or silent: a silent rejection
after thirty days, for example.

```json
{"name": "expire",  "from": "filed", "to": "expired", "after": "30d"}
```

This one is made by time. Its line under `access.transition` is
`FALSE`, so nobody can take it by hand, and the engine takes it when
thirty days in `filed` have passed.

> **Status.** The engine can also make a move as soon as a guard comes
> to hold, with no time involved. Today that form is written without a
> name. The owner ruled on 2026-11-13 that every transition has a name
> and an automatic one carries a mark; that is not built yet, and this
> chapter will show the form when it is.

## Rules

A rule is something that happens by itself when something else
happens: when this, do that.

```json
{"on": "transition", "name": "approve", "do": [{"action": "set", "values": {"decided_by": "USER()"}}]},
```

| Word | Meaning |
|---|---|
| `on` | what sets the rule off; see the table below |
| `state`, `name`, `variables` | which state, which transition or which variables `on` is about |
| `if` | a formula; the rule runs only when it is TRUE |
| `do` | the list of actions to run, in order |

What can set a rule off:

| `on` | Runs when | Also give |
|---|---|---|
| `create` | a document is created | |
| `values_change` | one of the named variables changes | `variables` |
| `enter` | a document enters a state | `state` |
| `exit` | a document leaves a state | `state` |
| `transition` | a named transition is taken | `name` |
| `delete` | a document is deleted | |

The actions:

| `action` | What it does |
|---|---|
| `set` | sets variables of this document, each to the result of a formula |
| `reset` | puts variables of this document back to their defaults |
| `refresh` | works triggered variables out again |
| `emit` | writes an event of your own into the log |
| `create`, `transition` | creates a document of another class, or moves one |
| `table.insert`, `table.update`, `table.delete` | changes a row of a reference table |
| `http.call`, or the name of a defined action | calls another system; see [Actions](../actions.md) |

The first four change the document inside the same commit. The others
reach outside it, so the engine queues them in that commit and runs
them afterwards, exactly once, as the person whose change set the rule
off.

## Access

Who may do what with a document as a whole. Each line is a formula
that is asked for the caller and the document; TRUE lets the caller
through.

```json
"access": {
  "read":   "owner = USER() OR approver = USER() OR INGROUP(\"finance\")",
  "create": "INGROUP(\"everyone\")",
  "update": "(owner = USER() AND STATE() = \"draft\") OR INGROUP(\"finance\")",
  "delete": "owner = USER() AND STATE() = \"draft\"",
  "transition": {
    "file":    "owner = USER()",
    "recall":  "owner = USER()",
    "approve": "approver = USER() AND CREATEDBY() <> USER()",
    "reject":  "approver = USER()",
    "expire":  "FALSE"
  }
},
```

| Line | Who may | Asked |
|---|---|---|
| `read` | see the document | on every call; a caller it refuses is told the document does not exist |
| `create` | create one | over the new document, with its defaults |
| `update` | change its values | over the document as it is stored |
| `delete` | delete it | over the document as it is stored |
| `transition`, by name | take that transition | over the document as it is stored; a transition with no line of its own asks `update` |

**A line left out lets nobody through.** A class says who may, or
nobody may. Checking a class tells you about each line it leaves out.

**A transition with a line of its own is not asked `update`.** A
manager approves a request she may not edit. The values she gives with
the transition are checked by [`rights`](#rights) alone, so say there
what she may write. [States and
transitions](notions/states-and-transitions.md) plays it through.

The functions these formulas use most, of the whole list in
[The functions](functions.md):

| Function | Answers |
|---|---|
| `USER()` | the id of the caller |
| `INGROUP("finance")` | whether the caller is in that group |
| `INGROUP("everyone")` | TRUE for every caller that has an id |
| `STATE()` | the state the document is in |
| `CREATEDBY()` | the id of whoever created the document |
| `ACTINGFOR()` | the id of whoever the caller acts for, when they act for somebody |
| `USERKIND()` | whether the caller is a `person`, an `agent` or a `service` |
| `TRUE`, `FALSE` | everybody, nobody |

The users and the groups are not kept by FadoDB. The program that
embeds it says on every call who the caller is and which groups they
are in.

## Rights

Who may write or see single variables, state by state. It narrows
what `access` allows; it never widens it.

```json
"rights": {
  "*":     {"decided_by": {"write": "FALSE"}, "paid": {"write": ["finance"]}},
  "filed": {"tags": {"write": ["finance"]}}
}
```

The table has three levels: a state, then a variable, then `write` or
`read`.

| Level | Written as | `*` means |
|---|---|---|
| the state | the name of a state | every state |
| the variable | the name of a variable | every variable |
| the right | `write`, who may set it; `read`, who may see it | |

A right is a formula, or a list of groups, which is the short way to
write "anybody in one of these groups".

When several lines could apply to a variable, one of them is asked:
the first of these that the table has.

1. the document's state, and the variable by name
2. the document's state, and `*`
3. the state `*`, and the variable by name
4. the state `*`, and `*`

So what a state says wins over what `*` says for every state, and
within a state a named variable wins over `*`.

Read aloud, the example says: nobody ever sets `decided_by` by hand
(the rule above sets it), only finance marks an expense as paid, and
while an expense is filed only finance changes its tags.

To give each state its own editors, write one line per state:

```json
{
  "name": "Memo",
  "variables": {"text": {"type": "text"}},
  "states": {"initial": "writing", "list": ["writing", "review"]},
  "transitions": [{"name": "send", "from": "writing", "to": "review"}],
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"},
  "rights": {
    "writing": {"*": {"write": "CREATEDBY() = USER()"}},
    "review":  {"*": {"write": ["reviewers"]}}
  }
}
```

A variable the caller may not `read` is left out of the document they
get, as if it were not there.

## Checking a class

Before a class is defined the engine checks all of it and reports
every finding at once, each with a code, the exact place in the
definition and often a hint for the fix. A class with a finding is not
defined, and nothing is half done.

Defining a class again with a change makes the next version of the
class. Existing documents are not touched; each takes the new version
at its next change.
