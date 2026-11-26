# Apps

An app is a name for the classes and the tables that belong together:
the expenses of a company, its help desk. A person sees the app as one place of
work; a designer keeps its classes together; an agent that is asked to
build something for the help desk finds what is there already.

An app holds no document and decides nothing. Who may do what is said
by each class, as before.

## Defining an app

Dan is a designer at Acme. He makes the app for expenses:

<!-- an app -->
```json
{
  "name": "Expenses",
  "label": "Expenses",
  "description": "What employees paid for the company, approved and paid back."
}
```

| Member | What it says | Needed |
|---|---|---|
| `name` | the name of the app, as a class writes it: letters, digits and underscores | always |
| `label` | what people read in its place | no; the name, where it is left out |
| `description` | what the app is for, in a sentence | no |
| `icon`, `color` | how a face shows it, as in the header of a class; nothing to the engine | no |

`define_app` takes the definition and answers it as it was stored,
with its version and who defined it. It is for a designer, as
`define_class` is. The same definition sent again changes nothing.
Another definition under the same name is the next version of the
app, and the database keeps the current one.

## A class says which app it belongs to

The class names its app in its own definition, with `app`:

```json
{
  "name": "Expense",
  "app": "Expenses",
  "label": "Expense",
  "plural": "Expenses",
  "description": "What an employee paid for the company and asks to be paid back.",
  "title": "what",
  "variables": {
    "what":  {"type": "text", "required": true, "description": "A few words a colleague would understand."},
    "total": {"type": "money", "scale": 2, "description": "What the employee is paid back."}
  },
  "states": {"initial": "draft", "list": ["draft", "paid"], "final": ["paid"],
             "descriptions": {"draft": "Being filled in.", "paid": "Paid back, and kept."}},
  "transitions": [
    {"name": "pay", "from": "draft", "to": "paid", "description": "Finance pays the employee back."}
  ],
  "access": {"read": "TRUE", "create": "TRUE", "update": "CREATEDBY() = USER()", "delete": "FALSE",
             "transition": {"pay": "INGROUP(\"finance\")"}}
}
```

- **The app is there first.** A class that names an app the database
  does not have is refused with the finding `unknown_app` at `/app`;
  `validate_class` reports the same and stores nothing.
- **A class belongs to one app, or to none.** A class without `app`
  works as every class did before there were apps.
- **A class moves to another app by its next version**, as it changes
  in any other way: Dan writes another name after `app` and defines
  the class again. No document is touched.
- **The app does not list its classes.** `list_classes` with `app`
  answers them.

## A table says which app it belongs to

A [table](tables.md) names its app the same way. Rates are kept for
the expenses, so Dan puts them into Expenses:

```json
{
  "name": "Rates",
  "app": "Expenses",
  "label": "Exchange rates",
  "description": "The rate of a currency in PLN on a day.",
  "columns": {"currency": "text", "day": "date", "rate": "decimal(4)"},
  "key": ["currency", "day"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}
```

- **The app is there first**, as for a class: a table that names an
  app the database does not have is refused with `unknown_app` at
  `/app`.
- **A table belongs to one app, or to none.** A table without `app`
  works as every table did before.
- **A table moves to another app by its next version**, and that
  change is taken whatever the table holds and whoever reads it: it
  touches neither its columns nor its rows. Any other change of a table
  that has rows, or that a class reads, is refused as before
  ([changing a table](tables.md#changing-a-table)).
- **A class of one app reads a table of another**, as it reads one of
  none. The app decides nothing.
- **`list_tables` with `app`** answers the tables of the app.

## Reading what there is

| Command | What it answers |
|---|---|
| `list_apps` | the names of the apps, in the order they were defined |
| `get_app` | one app as it was defined, with its version and who defined it |
| `list_classes` with `"app": "Expenses"` | the names of the classes that belong to the app; an empty list for an app that has none yet |
| `list_tables` with `"app": "Expenses"` | the names of the tables that belong to the app, in the same way |
| `get_class`, `get_bundle`, `get_table` | the class or the table with its `app`, as it was defined |
| `definitions` with `"what": "app"` | every version of an app, with who defined it and when |

They are open to everybody, as reading any definition is. A name that
is no app is refused with `unknown_app`, and the hint names the apps
there are.

## The app System

Every database has the app `System`, the engine's own. It holds the
[system tables](system-tables.md), which say what the database holds:
its apps, its classes, its users and its groups. The engine defines it
when it opens a database that has none, so `list_apps` names it in
every database, first in a new one. A designer may give it other words
with `define_app`, as any app; a database where a designer made an
app `System` before keeps it with its words. No class of a designer's
needs to belong to it.

## What an app does not do yet

- An app cannot be removed or renamed.
- An app has no groups, schemas or actions of its own: a class and a
  table name their app, nothing else does today.
- An app is not exported from one database and brought into another.
- An app has no words in other languages.
