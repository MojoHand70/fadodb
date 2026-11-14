# Change a class that already has documents

A class of tickets is in use. Now every ticket should have a priority.
The class changes; the tickets that exist must not be disturbed.

## The class, before and after

The class in use:

```json
{
  "name": "Ticket",
  "variables": {
    "subject": {"type": "text", "required": true}
  },
  "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
  "transitions": [
    {"name": "close", "from": "open", "to": "closed"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  }
}
```

The same class with a priority, and a computed variable that reads it:

```json
{
  "name": "Ticket",
  "variables": {
    "subject":  {"type": "text", "required": true},
    "priority": {"type": "text", "values": ["low", "high"], "default": "\"low\""},
    "urgent":   {"formula": "priority = \"high\""}
  },
  "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
  "transitions": [
    {"name": "close", "from": "open", "to": "closed"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  }
}
```

Defining a class again under the same name makes its next **version**.
There is nothing else to do.

## What it does

Three tickets exist under version 1: A and B are open, C is closed.
Then version 2 is defined.

| Ticket | What happens | It is then |
|---|---|---|
| A | nothing: nobody touches it | still as version 1 made it, with no priority |
| A | somebody changes its subject | of version 2: it has `priority`, blank, and `urgent`, FALSE |
| B | the designer migrates the class | of version 2, without anybody changing it |
| C | it is closed, so it is frozen | of version 1 for ever |
| a new ticket | is created | of version 2, with `priority` "low" from the default |

## How it works

- **A new version touches no document**, however many the class has.
- **A document takes the new version at its next change**, or all at
  once when a designer migrates the class. Until then it is read
  exactly as the version that made it left it.
- **A default is for new documents.** A ticket that existed gets the
  new variable blank, not its default. Write the formulas of the new
  version so that a blank is handled, or set the value on the old
  tickets.
- **A document in a final state never changes**, its version of the
  class included. What was closed under version 1 stays as it was
  closed.
- **Every version is recorded**: who defined it, and when.

## What a new version may not do

The engine checks a new version against the documents that exist and
refuses one that would lose something, naming what and how many
documents are in the way.

| Not allowed | The engine says |
|---|---|
| removing a variable that documents hold a value for | `not_allowed`, with the variable and how many documents hold a value for it |

Blank the values first, or keep the variable.

See also: [The class definition](../class.md#checking-a-class).
