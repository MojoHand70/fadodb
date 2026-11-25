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
| `app` | the [app](apps.md) the class belongs to, by its name | no; a class may belong to none |
| `title` | a formula that names each document | no; without it the title is the document's id |
| [`variables`](#variables) | what each document holds, and what is computed | always |
| [`sections`](#sections) | which variables belong together: the outline of the document | no |
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
    "approver":   {"type": "text", "required": true, "label": "Approver", "list": true,
                   "description": "The id of the person who decides the expense: the employee's manager."},
    "kind":       {"type": "text", "values": ["travel", "hotel", "meal", "other"], "default": "\"other\""},
    "amount":     {"type": "money", "scale": 2, "required": true},
    "spent_on":   {"type": "date", "default": "TODAY()", "label": "Spent on", "list": true, "format": "d mmm yyyy"},
    "nights":     {"type": "int", "min": 0, "max": 60, "default": "0"},
    "paid":       {"type": "bool", "default": "FALSE"},
    "lines":      {"type": "rows", "schema": {"what": "text", "cost": {"type": "money", "label": "Cost", "format": "#,##0.00"}}},
    "tags":       {"type": "array<text>"},
    "large":      {"formula": "amount > 1000"},
    "decided_by": {"type": "text"}
  },
  "sections": {
    "spending": {"label": "Spending", "description": "What was spent, when, and on what.",
                 "variables": ["kind", "amount", "spent_on", "nights", "lines", "tags"]},
    "decision": {"label": "Decision", "description": "Who decides the expense, what they decided, and whether it is paid.",
                 "variables": ["approver", "decided_by", "paid"]}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"],
             "labels": {"draft": "Draft", "filed": "Waiting for approval", "approved": "Approved", "rejected": "Rejected", "expired": "Expired"},
             "descriptions": {"filed": "The approver has thirty days to decide; after them the expense expires."}},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed", "guard": "amount > 0"},
    {"name": "recall",  "from": "filed", "to": "draft"},
    {"name": "approve", "from": "filed", "to": "approved", "label": "Approve",
     "description": "The approver accepts the expense for payment."},
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
| `required` | a document without a value for it is refused. This holds for a simple value; on an object, a list or rows the word is accepted and not enforced today |
| `default` | a formula for the value a new document gets when the caller gives none |
| `formula` | the variable is computed, and nobody sets it |
| `values` | for a text: the list of values it may take |
| `min`, `max` | for a number: the range it must stay in. On a computed variable they hold only when it names its `type` |
| `mode`, `on` | for a computed variable: `"mode": "triggered"` and the list of moments at which it is worked out, see [Triggered variables](triggered-variables.md); or `"mode": "live"`, worked out when it is read and kept nowhere, see [Live variables](live-variables.md) |
| `scale` | for `decimal` and `money`: the number of decimal places, two when left out |
| `index` | keeps an index on the variable, so that finding documents by it is fast |
| `class` | for a `ref`: the class it points at |
| `schema` | for `rows`: the columns of the table, each with its type, or the name of a schema. For `object`: the name of a schema, which is defined on its own before the class; see [Schemas and objects](schemas-and-objects.md) |
| `label` | what a person reads in place of the name; see [what a class says to people](#what-a-class-says-to-people) |
| `description` | what the variable is for, for people and for agents |
| `list` | `true`: The List of the class may show the variable as a column |
| `format` | for a number, an int, a decimal, a money, a date or a datetime: how a page shows the value, in Excel's patterns |

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

**A pointer is checked when it is set.** A `ref` names a document of
the class it points at, as `"Customer:C-17"`. The document must be
there, and whoever sets the pointer must be able to read it; otherwise
the change is refused with `unknown_document`, in the same words for a
document that is not there and for one the caller may not read. So
documents are loaded in the order they point: the customers before the
invoices that name them. A pointer a document holds already is not
asked about again when its other values change. The engine keeps the
pointer with the class written as its definition writes it:
`"customer:C-17"` is kept as `"Customer:C-17"`. The id is kept to the
letter.

**A pointer compares with a pointer.** A formula makes one with
`REF`:

<!-- pointers -->
| Written | Is | May stand |
|---|---|---|
| `REF()` | the pointer to the document the formula belongs to | where `ID()` may |
| `REF("Customer", "C-17")` | the pointer to that document. The class is written as a text, and the id is any text; a blank id gives a blank pointer | anywhere |

So `customer = REF("Customer", "C-17")` is a condition, in a formula
and in a filter. `customer = "Customer:C-17"` is refused, because a
pointer never compares with a text. The engine knows the class each
pointer points at. A class that compares pointers to two classes is
refused with `type_mismatch` when it is defined: `customer = REF()`
written in an Invoice says "customer points at Customer, and this
document is an Invoice".

**A class says what points at it.** `get_class` answers, under
`pointed_at_by`, the `ref` variables of the classes defined now that
point at the class, each as its class and its name there:
`[{"class": "Invoice", "variable": "customer"}]`. Nobody writes that
list; the engine works it out from the definitions.

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
`"mode": "triggered"`, an advanced technique with a chapter of its
own, [Triggered variables](triggered-variables.md).

And it can be worked out when somebody reads it, for that reader, and
kept nowhere: that is `"mode": "live"`, for a value such as the days a
document has waited, which is wrong the day after it is stored. It has
a chapter too, [Live variables](live-variables.md). A live variable
whose formula is a `QUERY` is a list of other documents, such as the
invoices of an order, and another live variable may count that list or
add up a column of it: see [Lists of other documents](lists.md).

Some names are the engine's and cannot be used for a variable, among
them `id`, `title`, `state`, `version`, `created_at`, `created_by`,
`updated_at` and `updated_by`. The engine keeps these for every
document by itself.

## States

```json
"states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"],
           "labels": {"draft": "Draft", "filed": "Waiting for approval", "approved": "Approved", "rejected": "Rejected", "expired": "Expired"},
           "descriptions": {"filed": "The approver has thirty days to decide; after them the expense expires."}},
```

| Word | Meaning |
|---|---|
| `list` | every state, by name |
| `initial` | the state a new document starts in |
| `final` | the states in which a document is finished |
| `labels` | what a person reads in place of each state's name, by the name |
| `descriptions` | what a document waits for in each state, by the name, for people and for agents |

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
| `label` | the caption of its button |
| `description` | what taking it means, for people and for agents |

A guard is about the document: is the amount above zero, is the
account filled in. Who may take the transition is a different question
and is answered under [`access`](#access). The engine tells the two
apart in its refusals: `guard_false` for the first, `forbidden` for the
second.

A guard reads its document, the caller, the clock, the state and the
life of the document. It does not look in a table or at other
documents: what it needs from there is brought onto the document
first, by a triggered variable that lists `before:` the transition;
see [Before a transition](triggered-variables.md#before-a-transition).

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

## What a class says to people

A name is for formulas, commands and the log, and never changes for
people. What a person reads is written beside it: a label, a
description, whether The List shows it, and a format. None of it is a
value. Nothing of it is stored with a document, no formula, command or
export reads it, and the engine answers it as it was written, to every
page and every agent that asks for the class.

```json
"approver":   {"type": "text", "required": true, "label": "Approver", "list": true,
               "description": "The id of the person who decides the expense: the employee's manager."},
```

- **`label`** is what a person reads in place of the name: the heading
  of a column, the caption of a field, the word on a button. A
  variable, a transition, a column, a field and the class itself have
  one; a state's is under `labels` in the states block. Without one, a
  page shows the name.
- **`description`** says what the thing is for, to people and to
  agents. A page shows it as help beside a field; an agent reads it
  before it fills the variable. It may be long, several paragraphs,
  and may use Markdown. **A variable, a state or a transition without
  one is noted**, code `no_description`, each with its place: a note
  refuses nothing, and the class is defined all the same. Write one for
  each, as a programmer writes a comment.
- **`"list": true`** lets The List of the class, the table of its
  documents a page shows, have the variable as a column. The columns
  are offered in the order the variables are written, and all of them
  are shown at first. Which of them a person hides, shows and reorders,
  and the views they save by name, are the page's; the engine keeps no
  view. A variable that is not on The List is still in every answer
  that holds the document.
- **`format`** says how a page shows a number, an int, a decimal, a
  money, a date or a datetime, in the patterns of Excel's number
  formats. The person's language gives the separators and the names of
  the months, so one pattern serves every language.

```json
"spent_on":   {"type": "date", "default": "TODAY()", "label": "Spent on", "list": true, "format": "d mmm yyyy"},
```

| Pattern | Ola reads, in Polish | Ann reads, in English |
|---|---|---|
| `d mmm yyyy` on 2026-12-05 | 5 gru 2026 | 5 Dec 2026 |
| `#,##0.00` on 12345.67 | 12 345,67 | 12,345.67 |
| `0%` on 0.15 | 15% | 15% |

The engine checks a pattern when the class is defined and never applies
it: on the wire, in an export and in a formula the value is as it always
was, `"2026-12-05"`. It reads these parts of Excel's language:

| For | The parts |
|---|---|
| a number, an int, a decimal, a money | `0` a digit, `#` a digit when there is one, `,` the thousands separator between digit places, `.` the decimal point, `%` the value times a hundred, and up to three sections split by `;`, for the positive, the negative and the zero |
| a date | `d` and `dd` the day, `ddd` and `dddd` its name, `m` and `mm` the month, `mmm` and `mmmm` its name, `yy` and `yyyy` the year |
| a datetime | the same, and `h` and `hh` the hour, `m` and `mm` the minutes after an hour, `ss` the seconds, `AM/PM` |

Text in quotes stands as it is, as do a space and `$ - + / ( ) : ! ^ &
' ~ { } < > =`, and in a date also `.` and `,`: `dd.mm.yyyy`. Anything
else is refused with `bad_value` and its place in the pattern: colours
and conditions in brackets, the scientific notation, `General`. A format
on a text, a bool, a ref, a list, an object or rows is refused with
`not_allowed`.

**A column or a field says the same.** A column of a `rows` variable,
written as a type, may be written as an object with the type, a
`label`, a `description` and a `format`; the short form stays and means
the long one with a type alone.

```json
"lines":      {"type": "rows", "schema": {"what": "text", "cost": {"type": "money", "label": "Cost", "format": "#,##0.00"}}},
```

A column of an explicit table and a field of a schema take the same
long form ([tables](tables.md), [schemas and objects](schemas-and-objects.md)).

Changing a label, a description, `list` or a format is a change of the
class like any other: it makes a new version, and moves no document
([change a class](guides/change-a-class.md)). The same words in
another language are kept beside the class:
[words in other languages](#words-in-other-languages).

## Sections

Which variables belong together. An invoice has a supplier, amounts
and a delivery. A page draws them as parts of its form, an agent reads
them as the outline of the document, and a designer gives the rights
of a part in one line, not one line for each variable in it.

```json
{
  "name": "Invoice",
  "label": "Invoice",
  "description": "A supplier's invoice, from receipt to payment.",
  "variables": {
    "customer":     {"type": "text",  "label": "Supplier",   "list": true},
    "number":       {"type": "text",  "label": "Number",     "list": true, "required": true},
    "net":          {"type": "money", "label": "Net amount", "list": true},
    "vat":          {"type": "money", "label": "VAT"},
    "total":        {"type": "money", "label": "Total",      "formula": "net + vat"},
    "has_delivery": {"type": "bool",  "label": "Delivered to us"},
    "street":       {"type": "text",  "label": "Street"},
    "city":         {"type": "text",  "label": "City"},
    "notes":        {"type": "text",  "label": "Notes"}
  },
  "sections": {
    "supplier": {"label": "Supplier", "description": "Who sent the invoice.",
                 "variables": ["customer", "number"]},
    "amounts":  {"label": "Amounts",  "description": "What the invoice asks us to pay.",
                 "variables": ["net", "vat", "total"]},
    "delivery": {"label": "Delivery", "description": "Where the goods came to.",
                 "variables": ["has_delivery"],
                 "sections": {
                   "address": {"label": "Address", "description": "The address on the delivery note.",
                               "variables": ["street", "city"]}
                 }}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file",    "from": "draft",  "to": "review", "label": "Send for approval"},
    {"name": "approve", "from": "review", "to": "paid",   "label": "Approve"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"finance\") OR INGROUP(\"approvers\")",
    "create": "INGROUP(\"everyone\")",
    "update": "TRUE",
    "delete": "FALSE",
    "transition": {"file": "CREATEDBY() = USER()", "approve": "INGROUP(\"approvers\")"}
  },
  "rights": {
    "review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}
  }
}
```

A section has a `label`, a `description`, the `variables` it holds and
the `sections` inside it, so the outline reads as it nests: Delivery
holds `has_delivery`, and the Address inside it holds the street and
the city.

- **A variable is in one section at most.** A variable listed twice,
  in one section or in two, is refused with `duplicate` at the place of
  the second. A variable in no section, as `notes` here, belongs to the
  document as a whole.
- **The order is the written order**, of the sections and of the
  variables in each. A section's own variables come before the sections
  inside it, as a chapter's text comes before its subchapters. The
  order of `variables` still decides the order The List offers its
  columns in.
- **A section is named as a variable is**, without regard to case. The
  name is unique in the whole outline, however deep, and is not the
  name of a variable, because `rights` names a section by its name
  alone. A clash is refused with `duplicate`.
- **Sections nest at most four deep**, the top level counted as the
  first. A fifth level is refused with `too_deep`.
- **A section lists the class's own variables.** A name the class does
  not have is refused with `unknown_variable`, so a version of the
  class that drops a variable takes it out of its section too. A
  section with neither variables nor sections is refused with
  `missing`.
- A `rows` or an object variable is listed whole: its columns and its
  fields are its own.
- **A section without a description is noted**, code
  `no_description`, as a variable is.
- **A section has no condition.** Whether a page shows it, folds it or
  leaves it out is the page's. A page that hides the delivery when
  nothing was delivered reads `has_delivery`, a variable of the class,
  so that every page and every agent reads the same answer.

A section is not a value. Nothing of it is stored with a document, no
formula, filter or export reads it, and the engine answers it as it
was written. Changing the outline makes a new version of the class and
moves no document.

**Rights by section.** In `rights`, the name of a section stands where
the name of a variable stands, and the line holds for every variable
in the section and in the sections inside it:

```json
"rights": {
  "review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}
}
```

The most specific name wins: a variable may narrow the line of its
section, as `vat` does here, or widen it. Jan files the invoice, Ewa
approves invoices and Marta is in finance. With the invoice in
`review`:

<!-- who sets what in review -->
| Who | Sets | Decided by | Answer |
|---|---|---|---|
| Ewa, an approver | `net` | `review / amounts`: finance only | refused, `forbidden` |
| Marta, in finance | `net` | `review / amounts`: finance only | set |
| Marta, in finance | `vat` | `review / vat`: nobody | refused, `forbidden` |
| Marta, in finance | `street` | no line: `access` alone | set |

A refusal names the variable, and its hint names the line that
decided, `review / amounts`, so that a designer and an agent see which
line to change. [Rights](#rights) gives the whole order the lines are
looked in.

## Words in other languages

A name is never translated: `net` is `net` in every formula, command
and log. What a person reads is, and the words of a class in another
language are a definition of their own, kept beside the class. Ola
works in Polish, and reads the invoice above in these words:

```json
{
  "class": "Invoice",
  "language": "pl",
  "label": "Faktura",
  "description": "Faktura dostawcy, od przyjęcia do zapłaty.",
  "variables": {
    "customer":     {"label": "Dostawca"},
    "number":       {"label": "Numer"},
    "net":          {"label": "Kwota netto"},
    "total":        {"label": "Razem"},
    "has_delivery": {"label": "Dostarczono do nas"},
    "street":       {"label": "Ulica"},
    "city":         {"label": "Miasto"},
    "notes":        {"label": "Uwagi"}
  },
  "sections": {
    "supplier": {"label": "Dostawca", "description": "Kto przysłał fakturę."},
    "amounts":  {"label": "Kwoty"},
    "delivery": {"label": "Dostawa"},
    "address":  {"label": "Adres", "description": "Adres z dokumentu dostawy."}
  },
  "states":      {"draft": {"label": "Szkic"}, "review": {"label": "Do zatwierdzenia"}, "paid": {"label": "Zapłacona"}},
  "transitions": {"file": {"label": "Wyślij do zatwierdzenia"}, "approve": {"label": "Zatwierdź"}}
}
```

- **Each thing is named by its path**: a variable by its name, a
  column of a `rows` variable as `lines.qty`, a section, a state and a
  transition by their names. Each has a `label`, a `description` or
  both. A path the class does not have is refused with `bad_value` and
  its place, and words that say nothing with `missing`.
- **The language is a tag of BCP 47**, written as the standard writes
  it: `pl`, `de`, `pt-BR`, `zh-Hant`. `pt-br` is refused, and the hint
  gives `pt-BR`.
- **A word left out is the class's own.** Ola reads `VAT` beside the
  Polish words, because the words above do not give one.
- **The words make no new version of the class**, and touch no
  document. Defining them again makes their next version, and each
  version is kept with who defined it: `definitions` with `"what":
  "words"` and the name `class/Invoice/pl` lists them. Words taken
  away with `remove_words` say there, on the version they had, who
  took them away and when, under `removed`, to database administrators
  and designers (see [Removing a table](tables.md#removing-a-table)).
- **A later version of the class that drops a variable** leaves its
  words in place, unused. `list_words` and `get_words` note each such
  path, code `unused_words`; the words defined again without it clear
  the note.
- **A table and a schema have words too**, with `"table"` or
  `"schema"` in place of `"class"`: a table its `label`, its
  `description` and its `columns`, a schema its `fields`. The fields of
  an object variable are its schema's, and are given in the schema's
  words.

| Command | What it does | Who may |
|---|---|---|
| `define_words` | defines the words of one class, table or schema in one language, or their next version | a designer |
| `get_words` | reads them back: `{"class": "Invoice", "language": "pl"}` | every caller |
| `list_words` | lists them: all, of one thing, or in one language | every caller |
| `remove_words` | takes them away; their versions stay, with who took them away | a designer |

A page asks for the words of its person's language with the class in
one call, the bundle, and finds them put in place:
[The bundle](the-bundle.md).

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
| `refresh` | works [triggered variables](triggered-variables.md#refreshing) out again |
| `emit` | writes an event of your own into the log |
| `create`, `transition` | creates a document of another class, or moves one |
| `table.insert`, `table.update`, `table.delete` | changes a row of a reference table; see [Tables](tables.md#a-rule-that-writes-a-table) |
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
| the variable | the name of a variable, or of a [section](#sections) | every variable |
| the right | `write`, who may set it, or, for a [triggered variable](triggered-variables.md), who may bring its value again with `refresh`; `read`, who may see it | |

A right is a formula, or a list of groups, which is the short way to
write "anybody in one of these groups".

When several lines could apply to a variable, one of them is asked:
the first of these that says something about the right in question,
`write` or `read`.

1. the document's state, and the variable by name
2. the document's state, and the variable's section, then each section
   around that one, outwards
3. the document's state, and `*`
4. the state `*`, and the variable by name
5. the state `*`, and the variable's sections, as in 2
6. the state `*`, and `*`

So what a state says wins over what `*` says for every state, and
within a state the most specific name wins: a variable over its
section, a section over the one around it, and any of them over `*`.

That holds even for a variable that the state `*` names. A state with
a line of its own for `*` opens such a variable to whoever that line
names, so name the variable in that state as well.
[Who may](notions/who-may.md) shows it.

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

### From the command line

A class that stands in a file is checked before it reaches a database,
with the tool:

<!-- the check of a class -->
```
fadodb class check invoice.json
```

The file here is the invoice of [examples/classes](../../examples/classes/README.md),
whose rule makes a receipt. Checked alone, it is refused, and the tool
says where and what to do:

<!-- what the check says -->
```text
invoice.json: 1 finding refuses it
  unknown_class at /rules/0/do/0/class: unknown class "Receipt"
    fix: define it before the class whose rule creates its documents
```

Classes that name each other are checked together, as they are
defined together:

<!-- the check of two classes -->
```
fadodb class check receipt.json invoice.json
```

<!-- what the check says of two -->
```text
receipt.json: the engine takes it
invoice.json: the engine takes it
```

| | |
|---|---|
| `fadodb class check FILE...` | each file holds one class, a JSON object; `-` is standard input |
| `--db FILE` | the database the classes are meant for: what it has is what a definition may name. Left out, the check is against an empty database. Nothing is written to the database |
| `--json` | the findings as the command `validate_class` answers them, for a program |
| it ends with 0 | the engine would take the classes; a note refuses nothing |
| it ends with 1 | a finding refuses one of them |
| it ends with 2 | a file could not be read, or the tool was not told what to check |

An agent that writes a class works in a loop: write the file, check
it, mend the place the finding names, and check again until the tool
ends with 0. For a program the findings are one JSON object:

<!-- the findings for a program -->
```jsonc
{"diagnostics":[{"code":"unknown_class","path":"/rules/0/do/0/class","pos":-1,"message":"unknown class \"Receipt\"","hint":"define it before the class whose rule creates its documents"}]}
```

A database that a server holds is not opened by the tool. It is asked
through the server, with the command
[`validate_class`](commands.md#validate_class), which answers the same
findings. An action is checked the same way, with
`fadodb action check`: [Actions](../actions.md#checking-an-action-from-the-command-line).

Defining a class again with a change makes the next version of the
class. Existing documents are not touched; each takes the new version
at its next change.
