# Apps

An app is a name for the classes that belong together: the expenses
of a company, its help desk. A person sees the app as one place of
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

## Reading what there is

| Command | What it answers |
|---|---|
| `list_apps` | the names of the apps, in the order they were defined |
| `get_app` | one app as it was defined, with its version and who defined it |
| `list_classes` with `"app": "Expenses"` | the names of the classes that belong to the app; an empty list for an app that has none yet |
| `get_class`, `get_bundle` | the class with its `app`, as it was defined |
| `definitions` with `"what": "app"` | every version of an app, with who defined it and when |

They are open to everybody, as reading any definition is. A name that
is no app is refused with `unknown_app`, and the hint names the apps
there are.

## What an app does not do yet

- An app cannot be removed or renamed.
- An app has no groups, tables, schemas or actions of its own: only a
  class names its app today.
- An app is not exported from one database and brought into another.
- An app has no words in other languages.
