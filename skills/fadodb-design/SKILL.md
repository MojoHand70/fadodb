---
name: fadodb-design
description: Draft, validate and refine a FadoDB document class from a description of a business process. A class is one JSON document with typed variables, Excel-style formulas, a statechart with guards, field rights per state, rules and access as formulas. Use this skill whenever someone wants to model, design, declare or set up a process, workflow, approval flow, form, register, ledger, permit, case or any record that has states in FadoDB, or asks to turn a spreadsheet, a policy, a procedure or a use-case card into a class, even if they never say "class" or "FadoDB". It begins by finding what the organisation already keeps, so that no thing is kept twice. Also use it to review, explain or repair an existing class definition or a diagnostic the engine returned.
metadata:
  hat: designer
  status: written
---

# Design a FadoDB class

**Status: written, 2026-11-14; its first step, look before you build,
2026-11-15.** This page says how to write a class the
engine accepts and a person can read. The syntax is in the manual, and
this page tells you what to read, in which order to work, and what
goes wrong most often.

## Read first

| Read | For |
|---|---|
| [`docs/manual/class.md`](../../docs/manual/class.md) | every block of a class and every word in it. Read it whole before you write a class |
| [`docs/manual/notions/`](../../docs/manual/notions/) | the seven ideas, one chapter each, with a class and a story of named people: classes and documents, variables and formulas, states and transitions, rules and actions, who may, history and the log, time |
| [`docs/manual/formula-language.md`](../../docs/manual/formula-language.md) | how a formula is written, what mixes with what, blanks, exact numbers, days, and what a failure means in each place |
| [`docs/manual/guides/`](../../docs/manual/guides/) | one task a page, each with a class a test runs. Take the pattern from the guide |
| [`docs/manual/tables.md`](../../docs/manual/tables.md) | reference data: the definition of a table, its rows, `LOOKUP`, the three actions of a rule that write a row, and who may. Read it before a class looks a value up or feeds a table |
| [`docs/manual/schemas-and-objects.md`](../../docs/manual/schemas-and-objects.md) | a value with named fields: defining a schema, the nine the engine brings, and the table of what a class can and cannot do with an object today. Read it before you give a class an object |
| [`docs/manual/files.md`](../../docs/manual/files.md) | a file on a document: the bytes are put first, the document then names their hash; who may open a file, and the guard `NOT ISBLANK(scan.hash)` |
| [`docs/manual/triggered-variables.md`](../../docs/manual/triggered-variables.md) | a computed variable worked out only at chosen moments and kept in between: the rate of the day an invoice was checked, who made a move, a count of other documents. Read it before you write `"mode": "triggered"`: the guard of a move does not see what the move works out, and whoever may see a document may refresh it |
| [`docs/manual/live-variables.md`](../../docs/manual/live-variables.md) | a computed variable worked out when it is read, for the reader, and kept nowhere: the days a document has waited, whether the reader wrote it. Read it before you write `"mode": "live"`: nothing that is kept or decided may read such a variable |
| [`docs/manual/lists.md`](../../docs/manual/lists.md) | a list of other documents on a document, such as the invoices of an order: a live variable whose whole formula is a `QUERY`. Read it before you write one: the class with the list is defined after its source, the condition reads the listed document by bare names and this one by `THIS.name` and `REF()`, and the pointer wants `"index": true` |
| [`docs/manual/functions.md`](../../docs/manual/functions.md) | every function, with the ones this engine does not have yet marked. Check a function here before you use it. The engine you talk to answers the same list, for its own version, with the command `list_functions` |
| [`docs/manual/refusals.md`](../../docs/manual/refusals.md) | every code the engine answers with, and what it means |
| [`docs/manual/glossary.md`](../../docs/manual/glossary.md) | the words, and which are the engine's and which are yours |

## Look before you build

A process names things: an employee, a department, a customer, a cost
centre. Each of them is born somewhere and kept by somebody, and the
organisation needs one list of each. A class that keeps its own list of
employees beside the one the organisation already has makes two lists
that drift apart, and from then on every report is wrong in one of
them. So before you draw a state, find out what the organisation
keeps, and never keep a thing twice.

An agent that comes through `fadodb mcp` reads the short of this on
the door itself, and again on each tool that defines an app, a class,
a table or a schema
([the door for agents](../../docs/manual/the-door-for-agents.md#what-the-door-says-of-itself)).
This section is the whole of it.

1. **List what the database has**: `list_apps`, `list_classes`,
   `list_tables` and `list_schemas`. They answer names only. An app is
   a name for the classes and tables that belong together;
   `list_classes` and `list_tables` with `"app"` answer those of one.
2. **Read every one that may be a thing the story names**:
   `get_app`, `get_class`, `get_table` and `get_schema` answer the definition,
   with its `label` and its `description`. Match by meaning, not by
   name: the story says "employee", and the table may be called
   `People`. A rule names groups: on a server, `list_groups` answers
   each group with its `label` and its `description`, what it is for
   (ADR-0086). Read them before a rule names a group with `INGROUP`,
   and ask before you name one that is not there.
3. **Ask the person, once for each thing, naming what you found.**
   Never choose in silence. Never make a second list because the first
   is awkward to use: when it lacks a column you need, ask its owner
   for the column.
4. **For a thing nothing here holds, ask three questions**: where is
   it born, who keeps it, and how do you get the list? Then:

   | The answer | What you do |
   |---|---|
   | in a class or a table of this database | read it with `LOOKUP` |
   | in another system | a table filled by an import from that system and never edited here, its `description` naming the system. Rows read where they live, the binding `external`, are planned, and whether such a copy stays the way is question 1 of ADR-0060 |
   | nowhere: a spreadsheet, somebody's memory | propose a class that keeps it, with its owner's approval as its process, and define it only on the person's yes. A face for it is built with [`fadodb-face`](../fadodb-face/SKILL.md) and handed out by `fadodb serve` from a folder; without one, its owner works it through the commands |

5. **Write the answer down where the next agent looks**: in the
   `description` of the class or the table, as `Born: ... Owner: ...
   List: ...`. The next agent reads it in step 2 and asks nobody
   again.

The same holds for how a class looks: its `icon` and `color` belong to
the organisation, so ask before you choose them. A rule-book of the
organisation, the things it keeps and the look of its faces, kept as
documents in FadoDB, is proposed in
[ADR-0060](../../docs/adr/0060-the-organisations-rule-book.md); until
it is decided, the descriptions are the rule-book.

### A story, looked at

The story, as a person told it: "A training request. First it asks
whether the training is for one employee or for a whole department.
The head of that department approves it, HR books it, and a cost
centre pays for it."

The database already serves a leave process. The program that runs
you tells the engine that you are `builder-agent`, an agent, and a
designer.

<!-- command: list_classes -->
```jsonc
{}
```

<!-- answer -->
```jsonc
{"classes": ["Substitution", "Leave"]}
```

`Substitution` is the engine's own. `Leave` is a process, not a list
of anything the story names. The tables:

<!-- command: list_tables -->
```jsonc
{}
```

<!-- answer -->
```jsonc
{"tables": ["People", "Departments"]}
```

Nothing is called "employee". Read both: the label and the description
say what a table holds, where it is born and who owns it.

<!-- command: get_table -->
```jsonc
{"name": "People"}
```

<!-- answer -->
```jsonc
{
  "table": {
    "name": "People",
    "version": 1,
    "defined": {"by": "setup", "kind": "service", "at": 1794819600},
    "definition": {
      "name": "People",
      "label": "Employees",
      "description": "Everyone employed here, one row a person. Born: the HR system; the import copies it here every night at 02:00, and nobody edits it here. Owner: Anna Wolska, HR. List: this table.",
      "columns": {"login": "text", "name": "text", "department": "text"},
      "key": ["login"],
      "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"hr\")"}
    }
  }
}
```

<!-- command: get_table -->
```jsonc
{"name": "Departments"}
```

<!-- answer -->
```jsonc
{
  "table": {
    "name": "Departments",
    "version": 1,
    "defined": {"by": "setup", "kind": "service", "at": 1794819600},
    "definition": {
      "name": "Departments",
      "label": "Departments",
      "description": "The departments and the head of each. Born: here; finance writes the rows. Owner: Ewa Mazur, finance. List: this table.",
      "columns": {"code": "text", "name": "text", "head": "text"},
      "key": ["code"],
      "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"finance\")"}
    }
  }
}
```

What you ask, and what you are told:

<!-- what was asked -->
| The story says | You ask | The answer |
|---|---|---|
| an employee | The table People, labelled Employees, is copied every night from the HR system. Shall the request take its employees from it? | Yes. |
| a department and its head | The table Departments, kept by finance, names the head of each. Shall the request take the department and its head from it? | Yes. |
| a cost centre | Nothing here holds cost centres. Where is one born, who keeps them, and how do I get the list? | Ewa Mazur, finance: "In my spreadsheet." |

So you propose a class that keeps cost centres: anybody may propose
one, somebody from finance other than whoever proposed it approves
it, and finance closes it. Ewa says yes. Its description says where a
cost centre is born from now on.

```json
{
  "name": "CostCentre",
  "label": "Cost centre",
  "description": "Born: here, since 15 November 2026; before, in a spreadsheet of finance. Owner: finance. List: the documents in the state active.",
  "title": "name",
  "variables": {
    "name":  {"type": "text", "required": true},
    "owner": {"type": "text", "required": true}
  },
  "states": {"initial": "proposed", "list": ["proposed", "active", "closed"], "final": ["closed"]},
  "transitions": [
    {"name": "approve", "from": "proposed", "to": "active"},
    {"name": "close",   "from": "active",   "to": "closed"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() AND STATE() = \"proposed\"",
    "delete": "FALSE",
    "transition": {
      "approve": "INGROUP(\"finance\") AND CREATEDBY() <> USER()",
      "close":   "INGROUP(\"finance\")"
    }
  }
}
```

The request reads each thing from where it is kept: the employee's
department from `People`, the head from `Departments`, and brings,
when somebody asks for `ask`, how many active cost centres have its
number. The guard of `ask` reads that count: a guard reads its
document and never looks outside it.

```json
{
  "name": "TrainingRequest",
  "label": "Training request",
  "description": "A training for one employee or a whole department. The head of the department approves it, HR books it, a cost centre pays. Born: here. Owner: HR. List: this class.",
  "title": "training",
  "variables": {
    "training":    {"type": "text", "required": true},
    "for_whom":    {"type": "text", "values": ["employee", "department"], "required": true},
    "employee":    {"type": "text", "default": "USER()"},
    "chosen":      {"type": "text"},
    "department":  {"formula": "IF(for_whom = \"employee\", LOOKUP(People, department, login = THIS.employee), chosen)"},
    "head":        {"formula": "LOOKUP(Departments, head, code = THIS.department)"},
    "cost_centre": {"type": "text", "required": true},
    "centre_active": {"type": "number", "mode": "triggered", "on": ["before:ask"],
                    "formula": "LOOKUP(CostCentre, COUNT(id), id = THIS.cost_centre AND state = \"active\")"},
    "cost":        {"type": "money", "scale": 2, "required": true}
  },
  "states": {"initial": "draft", "list": ["draft", "asked", "approved", "booked", "refused"], "final": ["booked", "refused"]},
  "transitions": [
    {"name": "ask",     "from": "draft",    "to": "asked",
     "guard": "NOT ISBLANK(head) AND centre_active = 1"},
    {"name": "approve", "from": "asked",    "to": "approved"},
    {"name": "refuse",  "from": "asked",    "to": "refused"},
    {"name": "book",    "from": "approved", "to": "booked"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR head = USER() OR INGROUP(\"hr\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {
      "ask":     "CREATEDBY() = USER()",
      "approve": "head = USER() AND CREATEDBY() <> USER()",
      "refuse":  "head = USER()",
      "book":    "INGROUP(\"hr\")"
    }
  },
  "rights": {
    "asked":    {"*": {"write": "FALSE"}},
    "approved": {"*": {"write": "FALSE"}}
  }
}
```

How it went, as a test plays it: Ola asked for a negotiation course
for herself, paid by `CC-410`, and was refused with `guard_false`,
since nobody had approved that cost centre yet. Marek proposed it, Ewa
approved it, and Ola asked again. The request found her department,
Sales, in `People` and its head, Marek, in `Departments`. Marek
approved and Anna from HR booked it. No list of employees,
departments or cost centres was made twice.

## The shape of a class

A class is one JSON document with these blocks. This one is small and
complete; the engine accepts it as it stands, with no note.

```json
{
  "name": "Request",
  "label": "Request",
  "description": "A request for money that an employee files and a named approver decides.",
  "title": "subject",
  "variables": {
    "subject":  {"type": "text", "required": true, "label": "Subject", "list": true,
                 "description": "What the money is for, in the employee's words."},
    "amount":   {"type": "money", "scale": 2, "label": "Amount", "list": true, "format": "#,##0.00",
                 "description": "How much is asked for, before tax."},
    "approver": {"type": "text", "required": true, "label": "Approver",
                 "description": "The id of the person who decides the request."},
    "with_tax": {"formula": "ROUND(amount * 1.23, 2)", "label": "With tax", "format": "#,##0.00",
                 "description": "The amount with 23% VAT, to the cent."}
  },
  "sections": {
    "money":    {"label": "Money", "description": "What is asked for, with and without tax.",
                 "variables": ["subject", "amount", "with_tax"]},
    "decision": {"label": "Decision", "description": "Who decides the request.",
                 "variables": ["approver"]}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved"], "final": ["approved"],
             "labels": {"draft": "Draft", "filed": "Waiting for approval", "approved": "Approved"},
             "descriptions": {
               "draft":    "The employee writes the request.",
               "filed":    "Waiting for the approver's decision.",
               "approved": "Approved; the request changes no more."}},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed", "guard": "amount > 0", "label": "File",
     "description": "The employee sends the request to the approver."},
    {"name": "approve", "from": "filed", "to": "approved", "label": "Approve",
     "description": "The approver accepts the request."}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR approver = USER()",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE",
    "transition": {"file": "CREATEDBY() = USER()", "approve": "approver = USER() AND CREATEDBY() <> USER()"}
  },
  "rights": {
    "filed": {"*": {"write": "FALSE"}}
  }
}
```

## What goes wrong most often

Each of these costs a round with the engine, or passes and surprises a
person later.

1. **A line of `access` left out lets nobody through.** Write all
   four, `read`, `create`, `update` and `delete`, and a line under
   `transition` for every transition that not everybody who may
   `update` should take.
2. **A guard is about the document; who may is `access`.** Put
   "the amount is above zero" in the `guard` of a transition and "only
   finance approves" under `access.transition`. The engine tells them
   apart in its refusals, `guard_false` and `forbidden`, and so does
   everybody who reads the class.
3. **Give every transition a name.** One that only time takes gets
   `FALSE` under `access.transition`.
4. **`rights` narrows `access` and never widens it.** A caller needs
   `update` to let them through and the right of each variable they
   set. The line of the document's state is asked before the line of
   `*`, and a named variable before `*`. So a variable closed under
   the state `*` is open again in a state that has its own line for
   `*`: name it in that state too.
5. **A list is no value of a formula.** `QUERY` stands only as the
   whole formula of a live variable. `FILTER`, `SORT`, `TOP` and
   `UNIQUE` give a list, and stand only inside a function over a list:
   `SUM(FILTER(lines, delivered).price)`, never by themselves. `FIRST`,
   `LAST`, `INDEX`, `JOIN` and `CONTAINS` work over a list of the
   document and over nothing else.
   Several named editors on one document must be a group.
6. **A lookup that finds nothing is blank, and a blank counts as zero**
   in arithmetic. Guard the next step with `NOT ISBLANK(...)`.
7. **A looked-up value stays with the document** until a value the
   lookup reads changes. Use a triggered variable and a refresh for a
   value that must follow its source.
8. **A default is for new documents.** A variable added in a new
   version is blank on the documents that exist.
9. **Money is exact.** Declare `money` or `decimal` with a `scale`;
   never compute an amount yourself, write the formula.
10. **Never write a secret into a class or an action.** A request
    reads it with `SECRET("name")`, and the host program supplies it.
11. **A transition with its own line under `access.transition` is not
    asked `update`.** Whoever may take it may set, in the same call,
    every variable that `rights` does not keep from them. When a
    transition goes to somebody who may not edit the document, write
    in `rights` what they may set, or they can change any value while
    they approve.
12. **`required` holds for a simple value only.** On an object, a
    list or `rows` the engine accepts the word and does not enforce
    it. Guard the transition that needs the object with a test of one
    of its fields, such as `NOT ISBLANK(depot.code)`. A formula reads
    the fields of an object one at a time; it cannot test or compare
    the object as a whole.

## The hat

The designer turns a process somebody described into a class the engine
accepts: the fields with their types, the formulas that compute, the
states and the transitions between them, who may move what and when, and
what happens on a change. The engine validates the draft and names the
exact place of every problem, so the designer's loop is draft, validate,
read the diagnostics, fix, preview against live data, define.

## What works today

| Part of a class | Mark |
|---|---|
| variables, types, defaults, ranges, named schemas, objects, attachments, tables and `LOOKUP` | shipped; a table that holds rows, or that a class reads, gains columns by its next version, none of them in its key (ADR-0080, proposed), and changes or removes a column when every value converts and no formula or rule of a class uses it (ADR-0084, proposed); its key stays. `delete_rows` removes rows by their keys, as the write line lets; `remove_table` takes a table away, for a designer, once no class reads or writes it (ADR-0082, proposed), and `definitions` says on the version it had who removed it and when, to administrators and designers and to an agent working for an administrator (ADR-0083, proposed) |
| `COUNT`, `SUM`, `MIN` and `MAX` in a `LOOKUP` of a table, as in `LOOKUP(Stock, SUM(qty), sku = THIS.sku)` | shipped with Phase 4. `AVERAGE`, `MEDIAN` and `COUNTUNIQUE` stand there too (ADR-0081, proposed): the mean of exact values is exact with four places more than the column, the middle value with one more; `ROUND` cuts either |
| a formula that reads the documents of another class, `LOOKUP(Approval, COUNT(id), purchase = ID() AND state = "granted")` | shipped with Phase 4: in a triggered variable, a default or a rule, not in an auto formula and, since ADR-0078, not in a guard, which reads its document alone: bring what a guard needs with a triggered variable that lists `before:` the transition, as `centre_active` above; the variable declares its `type`; it sees every document and is computed at its trigger, so the other class's rule refreshes this document; put `"index": true` on the variable the condition compares. Two classes that name each other, a purchase and its approvals, are defined in one call with `DefineClasses`; `examples/classes/purchase.json` and `approval.json` are the worked pair |
| formulas, exact money, calendar days, `IFERROR` | shipped |
| states, transitions, guards, eventless chains, `STATE()` and `ID()` | shipped |
| a pointer to another document, `"type": "ref"` with `"class"` | shipped. A `ref` is checked when it is set: the document must be there and the caller must be able to read it, or the change is refused with `unknown_document`. Make the documents pointed at before the ones that point at them. The pointer is kept with the class written as its definition writes it, `"Customer:C-17"`, in whatever case it was given; the id is kept to the letter. `get_class` lists under `pointed_at_by` what points at a class. A `ref` compares with a pointer that `REF` makes, never with a text (ADR-0076, shipped after v0.7.0): `REF()` is the pointer to the document the formula belongs to and stands where `ID()` may, so not in an auto formula; `REF("Order", "ORD-17")` is the pointer to a named document and stands anywhere, a filter among them. `LOOKUP(Invoice, COUNT(id), order = REF())` counts the invoices of an order. A class that compares pointers to two classes is refused with `type_mismatch` |
| a kept value that follows its table: a triggered variable that lists `table:<name>`, as `"rate": {"type": "decimal", "scale": 4, "formula": "LOOKUP(Rates, rate, code = THIS.currency)", "mode": "triggered", "on": ["enter:draft", "table:Rates"]}` | shipped after v0.7.0 (ADR-0091, proposed): when the rows of the table change, the variable is worked out again in every document of the class that is not in a final state, and kept where it comes out different, each in a commit of its own, so the rules fire and a command that saw the version before gets `stale_version`. The table is one the variable's own formula looks into. Nobody's right is asked: whoever may change the table moves the values, whatever the `write` right of the variable says about people. To make the value stand in a state, give the variable `"write": "FALSE"` there: `"rights": {"sent": {"rate": {"write": "FALSE"}}}`. It is not instant: the worker of the outbox brings the documents after the change, a thousand in a transaction. Use it for a value that must not go stale while its document is open; for a value a decision stood on, which must stay, name the move instead (`enter:`, `before:`). Not built: following the documents of a class (a rule of that class sends a `refresh`), and a kept list that follows a table |
| a kept value that follows the clock: a triggered variable that lists `schedule:<when>`, as `"days_late": {"type": "number", "formula": "MAX(DAYS(TODAY(), due), 0)", "mode": "triggered", "on": ["enter:sent", "schedule:daily at 03:00"]}` | shipped after v0.7.0 (ADR-0093, proposed): at the moments the words name, the variable is worked out again in every document of the class that is not in a final state, and kept where it comes out different, each in a commit of its own with no actor, since the engine itself brings it. The words: `hourly`; `daily` or `daily at 03:00`; `weekly` or `weekly on friday at 18:00`; `monthly`, `monthly on 15` (1 to 28), `monthly on last day`, `monthly on 3rd monday at 06:00` (`1st` to `4th`, `last`); `every 15m`, `every 4h` (minutes or hours that divide a day, five minutes at the least). Twice a day is two triggers. The times are those of the database's time zone, which its operator gives (`timezone` in the server's settings, `Options.TimeZone`); UTC when none was given. `TODAY()` and `NOW()` stay in UTC, so work a value that counts days out at three in the night and not at midnight. To make the value stand in a state, give the variable `"write": "FALSE"` there. Not built: working days and holidays, "every second week", a schedule on a rule or on a kept list |
| a value worked out when it is read, `"mode": "live"` on a computed variable, read with the command `live` | shipped: one that reads its own document, the caller, the clock, the state and the life of the document. A live variable may look one value up, with `LOOKUP`, in a table or among the documents of a class (ADR-0076, shipped after v0.7.0): it is asked for its reader, so two readers may be answered two numbers, and a reader the `read` line of a table does not let through is refused with `forbidden`; a number a decision stands on is a triggered variable instead |
| a list of other documents, a live variable whose whole formula is `QUERY(source, columns, condition, sort)`, read with the command `live` for the reader, a page at a time | shipped: several columns in braces, `THIS.name` and `REF()` in the condition; the source is a class, or a table, whose rows have no `id`. A triggered variable of type `rows` keeps such a list with the document (see below). The engine keeps an index on every pointer by itself, on the state and the two moments of every class, and on a text the read rule compares with the caller (ADR-0075, shipped after v0.7.0); and on the variable a `LOOKUP` or a list is keyed by, the one its condition compares for equality with a value of its own document; write `"index": true` only on a variable people search by that is none of these. Where a condition compares two indexed variables, the engine looks into both indexes and asks through the one that finds fewer documents (ADR-0075, shipped after v0.7.0), so a second `"index": true` never makes a question slow; a list is asked through the pointer it compares with its document |
| one line for each group, with its totals: a live variable whose `QUERY` has a total among its columns, `QUERY(Invoice, {customer, COUNT(id), SUM(net)})` | shipped after v0.7.0 (ADR-0090, proposed), over a class. The plain columns say what the lines are grouped by, as in a pivot table; with none the list is one line. The totals are `COUNT`, `SUM`, `MIN`, `MAX`, `AVERAGE` and `COUNTUNIQUE`, each over one variable of the source, typed as in a `LOOKUP`. A line is no document and has no `id`; a total stands under its function and its variable in lower case, `count_id`, `sum_net`. Sort by a group or a total, `{-SUM(net)}` in the formula, `"order": ["-sum_net"]` in the command; `fields` is refused. It counts the documents its reader may read, and of them only those whose state lets the reader read every variable the list names; a reader who may read one of them in no state gets `forbidden`. It reads every document its condition takes each time it is read, and the engine notes `reads_all`; a program that keeps the copy of the analytical engine answers it from the copy, in milliseconds over a large class, so on a server built without the engine keep it for small classes. Not built: `MEDIAN` among the columns, a total over what is worked out (`SUM(net * 2)`: total a computed variable), such a list in a triggered variable, a figure over one, a table as its source |
| a figure over such a list: `COUNT(invoices)`, `SUM(invoices.net)`, `MIN`, `MAX`, `AVERAGE(invoices.net)`, `MEDIAN(invoices.net)`, `COUNTUNIQUE(invoices.state)`, `COUNTIF(invoices, state = "draft")`, `SUMIF(invoices, condition, net)`, `MINIF`, `MAXIF`, `AVERAGEIF`, `ANY(invoices, condition)`, `ALL(invoices, condition)` | shipped in a live variable alone (ADR-0081): one value, worked out with one statement for whoever reads it, so two readers may get two figures; a column the reader may not read gives a blank. It may stand inside a formula: `ROUND(AVERAGE(invoices.net), 2)`, `MAX(invoices.net) - MIN(invoices.net)`. The mean of money with two places has six and the middle value three, so round what a person reads. A guard, a rule and a kept value are refused: there a `LOOKUP` in a triggered variable counts and sums. Decided, not built: `FILTER`, `SORT`, `TOP`, `UNIQUE` |
| a figure over the lines or the tags the document itself holds: `SUM(lines.qty * lines.price)`, `COUNT(lines)`, `MAX(lines.price)`, `COUNTIF(lines, NOT delivered)`, `ALL(lines, qty > 0)`, `COUNTUNIQUE(tags)`, `ANY(tags, value = "red")` | shipped (ADR-0081, step 2): the same functions, in a computed variable, a triggered one, a guard, a rule and a live variable; the same for every reader, and worked out again in the commit that changes the list. A column is `lines.price`; an expression of the columns stands in the one argument, and a bare name beside them is a variable of the document. In a form with a condition the names are the row's and the document's are `THIS.name`. Not yet: in a read rule, a read right, a filter or a title (keep the figure in a computed variable and test that); the list itself as a value |
| one value out of a list of the document: `FIRST(lines.what)`, `LAST(lines.what)`, `INDEX(lines.what, 2)`, `JOIN(lines.what, ", ")`, `CONTAINS(tags, "urgent")`; over a list that is a `QUERY`, `FIRST(invoices.number)`, `JOIN(invoices.number, ", ")` | shipped (ADR-0081, step 3, its first group): where the figures above stand, over the lines and tags in any formula of the document, over a `QUERY` list in a live variable alone. They follow the order of the list. `INDEX` at a place the list does not have is the error `out_of_range`, which refuses a commit when it stands in a computed variable: write `IFERROR(INDEX(lines.what, 2), "")`. The place, the separator and the value looked for are values of the document; a figure may not stand there (the last row is `LAST`, not `INDEX(..., COUNT(...))`). Not in the column position of a `LOOKUP` |
| a list narrowed, ordered and cut inside a function over it: `SUM(FILTER(lines, delivered).price)`, `FIRST(SORT(lines, {-price}).what)`, `SUM(TOP(SORT(lines, {-price}), 3).price)`, `JOIN(UNIQUE(lines.what), ", ")`, `JOIN(FILTER(invoices, state = "draft").number, ", ")` | shipped (ADR-0081, step 3, its second group): where the figures stand. Written from the inside out in one order, `FILTER` nearest the list: `TOP(SORT(FILTER(list, condition), {columns}), count)`; a column after the call; `UNIQUE` round a column. Each is a part of the one question. A blank comes before every value in a `SORT`; a blank count of `TOP` is 0. `UNIQUE` stands inside `COUNT`, `JOIN`, `FIRST`, `LAST`, `INDEX`, `CONTAINS`. Decided not now (the owner, 2026-11-24): such a list as a variable of its own; for a `QUERY` list, write a second `QUERY` |
| the life of the document in a formula: `CREATEDAT()`, `CREATEDBY()`, `UPDATEDAT()`, `UPDATEDBY()`, `ENTEREDAT()`, `ENTEREDBY()`, `ENTEREDFROM()`, `VERSION()` | shipped with Phase 4: in a default, a triggered variable, a guard, a rule, an access rule and a right, not in an auto formula. "Seven days in this state" is `NOW() >= ADDDAYS(ENTEREDAT(), 7)`; four eyes is `CREATEDBY() <> USER()` as the access rule of the transition. A transition back into the same state is an entry and restarts `ENTEREDAT()` |
| the life of the document by state: `FIRSTENTERED("approved")`, `LASTENTERED`, `FIRSTLEFT`, `LASTLEFT`, each with a `...BY` for who; `ENTRIES("sent")`; `DAYSIN("review")`; `FIRSTEDITOR("draft")`, `LASTEDITOR("draft")` | shipped with Phase 4: where the functions above may stand, and not in a read rule, a read right or a filter, which run for every row (keep the answer in a triggered variable, or query the `_states` view). The state is written as text and checked when the class is defined, or computed, as in `DAYSIN(STATE())`. A transition back into the same state is an entry. An editor created the document in the state or changed it there; who moved it in or out is `LASTENTEREDBY` and `LASTLEFTBY`. "Back to whoever wrote the draft" is `LASTEDITOR("draft")`; "who first sent it to review may not approve it" is `FIRSTENTEREDBY("review") <> USER()`; "after three rounds" is `ENTRIES("review") >= 3` |
| the list of documents that are due, `Due` | shipped with Phase 4: the documents of a class that a commit would move now, or at a moment, each with its state and the state it would move to. A document becomes due when a guard of an eventless transition comes to hold with no commit of its own: the clock passed a date. (A class defined before a guard was held to its document may look a table up in a guard, and a change of the table then makes its documents due too.) The engine does not move it by itself yet: the host program touches it, with a refresh. Index the variable a guard compares with the clock, as in `"due": {"type": "date", "index": true}` for `TODAY() > due` |
| a transition that carries its time: `"after": "30d"`, a time in the state as a whole number and `d`, `h`, `m` or `s`, or `"at": "ADDDAYS(due, 1)"`, a date or a datetime the document holds | shipped with Phase 4: it needs neither a name nor a guard, and a guard beside it is still asked. It fires in the first commit that touches the document once its time has come, in the order of the class with the eventless transitions; its event has the origin `timer`. A reminder is a named transition back into its own state with an `after` and a rule on its name; an escalation that reminders must not push back is `"at": "ADDDAYS(FIRSTENTERED(\"waiting\"), 14)"`. With a name a person may take it before its time; give it the access rule `FALSE` to leave it to time alone. An `at` may not read `NOW()`, `TODAY()`, `USER()`, another document or a table, and a blank `at` never comes. Put `"index": true` on the date an `at` names |
| the engine moving a document by itself when its timer comes | shipped with Phase 4: the move is made as the person whose commit set the timer, who moved the document into the state or who last changed the date, so `USER()` in its rules is theirs and its actions run with their rights. A reminder missed while the engine was stopped is made once, not once for every period missed. A guard beside a timer is asked when the time comes; if it does not hold then, the document waits for a commit to touch it, and `Due` lists it once the guard holds |
| what the database keeps: `list_classes`, `list_tables` and `list_schemas` answer the names; `get_class`, `get_table` and `get_schema` the definition with its `label` and `description` | shipped |
| apps, a name for the classes and tables that belong together: `define_app`, `get_app`, `list_apps`, and `"app": "Expenses"` in a class or a table | shipped (ADR-0077, ADR-0079 proposed): define the app first, then each class and table with `app`; one of an app the database does not have is refused with `unknown_app` at `/app`; `list_classes` and `list_tables` with `"app"` answer what belongs to one. A table in use moves to another app by its next version, its rows kept. Before you make an app, look whether the story belongs in one that is there. An app has no groups, schemas or actions of its own yet, the app System is planned, and an app is not exported |
| field rights per state, per-transition rules | shipped with Phase 3 |
| rules and actions | shipped with Phase 4: rules fire inside the commit with `set`, `reset`, `refresh` and `emit`, a preview lists them, and triggered variables are computed at their triggers; what a rule does beyond its own document runs through the outbox, exactly once and as the person who caused it: `create` (with `each`, one document per element of a list), `transition` and `refresh` of another document, `table.insert`, `table.update`, `table.delete`; a rule may also call an action that has a definition, with typed `args`, whether a Go function of the host program implements it or the engine sends it as an HTTP request, and `http.call` for one request written in the rule; `Actions` lists every action a rule may name and whether this process can run it (`available`); on a database served by `fadodb serve` an action of your own is always a request, to a host its operator listed, since the server runs no Go function: a definition with no request is refused there with `not_registered`; `into` writes what an action returned, or the code it failed with, back to the document, and the eventless transitions follow |
| inlets, a way in for the messages of a system that speaks only its own language: `validate_inlet`, `try_inlet`, `define_inlet` (an administrator's), `list_inlets`; from a file, `fadodb inlet check` | shipped (ADR-0065): a definition says how a message proves who sent it, with a secret of a connection, and the one command it becomes, `create`, `set` or `transition`, whose values are formulas over `BODY`, `HEADERS`, `PARAMS` and `RAW`, typed against the class. A list in the message becomes the lines of the document (ADR-0085, shipped after v0.7.0): a `rows` variable takes, in place of a formula, `{"each": "order.lineItems", "as": "item", "row": {"sku": "JSONVALUE(item, \"variant.sku\")"}}`, a path to the list, a name for one element and a formula for each column. It is carried out as a service user. `docs/manual/a-way-in-for-messages.md` |
| action definitions: `DefineAction`, `ValidateAction`, `Actions`, `PreviewAction`, `TryAction` | shipped with Phase 4: a definition with a `request` needs no code and reads secrets with `SECRET("name")`; it can be defined only where the host program allows outbound calls and names the definition's connection; a list goes out in a request (ADR-0085, shipped after v0.7.0): a parameter `"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}` takes the name of a `rows` variable of the class, `"lines": "lines"`, and the body repeats a part for each row, `{"each": "lines", "as": "line", "item": {"sku": "line.sku"}}`; the same repeat stands in the JSON a rule gives a `json` parameter, such as the `body` of `http.call`, over a `rows` variable of its class; a list comes back in an answer: `"returns": {"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}}` with `"result": {"lines": {"each": "value", "as": "l", "row": {"sku": "JSONVALUE(l, \"lineObjectNumber\")"}}}`, and a rule writes it whole with `"into": {"lines": "lines"}`; a pull that fails leaves the lines as they are; `docs/actions.md` is the guide and `examples/actions/webhook.json` a generic webhook; definitions for particular services come with real needs, and a format for packages of them is planned |
| the class-level access rules: `"access": {"read": ..., "create": ..., "update": ..., "delete": ...}` | shipped with Phase 5: each is a condition over the document for the caller, such as `owner = USER() OR INGROUP("finance")`. A document its caller may not `read` does not exist for them: `unknown_document` from every call that names it, and absent from a filter, a count and `Due`. `create` is asked over the new document with its defaults and computed values; `update` over the stored document, by a set, a reset and a named transition that has no rule of its own; `delete` over the stored document. A refusal is `forbidden`, with the place of the rule as its path, `/access/update`. A `read` rule uses the document's own variables, the functions of its row, the caller and the clock, and no lookup. Put `"index": true` on the variable a read rule compares with the caller, as in `"owner": {"type": "text", "default": "USER()", "index": true}`: a caller's own documents are then listed through the index, and a group that sees everything, `OR INGROUP("finance")`, costs its members nothing. A rule left out lets every caller through today; a ruling on that is pending (ADR-0049), so write all four **Write all four.** A rule a class leaves out lets nobody through but a content administrator: a class with no `access` block holds documents nobody creates or sees. `ValidateClass` says so with a note for each rule left out, code `rule_left_out` and `note: true`, which refuses nothing; a definition is refused only when `Refuses()` is true. `INGROUP("everyone")` opens a rule to every caller that has an id, and `TRUE` to a call without a principal too |
| what a class says to people: `label`, `description`, `"list": true` and `format` on a variable, `labels` and `descriptions` in the states block, a `label` and a `description` on a transition, and the long form of a column or a field | shipped with Phase 7 (ADR-0068): checked when the class is defined, a `no_description` note for each variable, state and transition with none; the same words in other languages are definitions beside the class, two rows below |
| sections, the outline of a class: `"sections": {"amounts": {"label": ..., "description": ..., "variables": [...], "sections": {...}}}`, and a section's name in `rights` | shipped with Phase 7 (ADR-0069): one section per variable, four levels, names apart from the variables'; a section without a description is noted; in `rights` the most specific name wins, and a refusal's hint names the cell that decided. A section's words in another language are given with the class's, under `sections` |
| words in other languages: `define_words`, `get_words`, `list_words`, `remove_words`, for a class, a table or a schema | shipped with Phase 7 (ADR-0068, item 6): checked against the current version, a path it does not have refused with `bad_value`, the language a tag of BCP 47 written as the standard writes it (`pt-BR`); no new version of the class; a page reads them with `get_words` until the bundle carries them; `definitions` says on the version they had who took them away and when, to the same people (ADR-0083, proposed) |
| class versions, terminal states, migrations | shipped: a redefinition is a new version and touches no document; a document adopts it at its next commit, or all at once with `Migrate`; a terminal state freezes the document |

## Procedure

1. **Look before you build.** Find what the organisation already keeps,
   ask the person which of it the process uses, and keep nothing twice:
   [Look before you build](#look-before-you-build) above.
2. **Find the shape.** Every process in the catalogue is one of six
   shapes or two together: the approval chain, the lifecycle register,
   the ledger and the period, matching and reconciliation, the case with
   a clock, the schedule of obligations. Name the shape first; the states
   and the guards follow from it.
3. **List the fields and type each one.** Money is exact, with a scale
   and a currency; a date is a calendar day; a value from a fixed list is
   an enum; a repeated line is a `rows` variable with a schema. Mark what
   is required and write defaults as formulas. **Give each a `label` and
   a `description`.** The label is what a person reads in place of the
   name; the description is what an agent reads before it fills the
   field and a person reads as help. Write it as a programmer writes a
   comment: what the field holds and where it comes from. Mark `"list":
   true` on the fields The List should offer as columns, in the order
   you want them, and give a number or a date a person reads a `format`
   in Excel's patterns, `#,##0.00` or `d mmm yyyy`.
   **Group the fields in sections**, the outline of the document a page
   draws and an agent reads: `"sections": {"amounts": {"label":
   "Amounts", "description": "What the invoice asks us to pay.",
   "variables": ["net", "vat", "total"]}}`. A field is in one section at
   most, and one in none belongs to the document as a whole; a section
   may hold sections, four deep, and its name is not a field's. A
   section has no condition: a part shown only when something holds
   reads a computed variable, `has_delivery`, which every page and every
   agent reads alike. Where the screen puts a section is the page's.
4. **Write the formulas you would write in the spreadsheet.** The engine
   computes; the agent never does arithmetic. Division by zero is an
   error unless the formula says what to do with `IFERROR`.
5. **Draw the states, then the transitions, then the guards.** Give
   every transition a name, and each state and transition a label and a
   description: the states' under `labels` and `descriptions` in the
   states block, by name. A guard says what must hold for the
   document (`total > 0`); who may take the transition goes under
   `access.transition` in step 7. A terminal state freezes the
   document.
6. **Write the rights per state.** Which fields are read-only once
   approved, which group may write a discount, who the editors are in
   each state. The table is state, then variable, then `write` or
   `read`; `*` stands for every state or every variable; a cell is a
   formula or a list of groups. "In review only reviewers edit" is
   `"rights": {"review": {"*": {"write": ["reviewers"]}}}`. The line of
   the document's state is asked before the line of `*`, and within a
   state the most specific name wins. **A section stands where a
   variable stands**: `"review": {"amounts": {"write": ["finance"]}}`
   gives every field of the amounts, and of the sections inside it, to
   finance in review, and `"vat": {"write": "FALSE"}` beside it still
   closes one of them. The order is the variable's own line, its
   section's, each section around that one, then `*`. A refusal's hint
   names the line that decided, `review / amounts`: mend that line.
   **What a guard needs from outside the document is brought first**
   (ADR-0078). A guard reads the document, the caller, the clock, the
   state and the life of the document; `LOOKUP` and `QUERY` in a guard
   are refused with `not_allowed`. Give the lookup a triggered variable
   that lists `"before:<transition>"`: it is worked out when somebody
   asks for the transition, before the guard is asked, and kept in the
   version the move makes, so the log holds the number the condition
   decided on. `"on": ["enter:review"]` keeps the number of the day
   the document was filed; `"on": ["enter:review", "before:approve"]`
   takes the number of the moment of approval. A refusal says what
   was brought, under `brought`. A transition without a name has
   nobody asking, so nothing is brought before its guard: bring its
   number at the entry of the state, or by a refresh.
   **A decision over other documents keeps the lines it stood on**
   (ADR-0078). "Close the order when every invoice is paid" reads the
   invoices; make them a list the order keeps, a triggered variable
   whose whole formula is a `QUERY`, with `"type": "rows"` and a
   `schema` of the QUERY's columns:
   `"settled": {"type": "rows", "schema": {"number": "text", "state": "text"}, "formula": "QUERY(Invoice, {number, state}, order = REF())", "mode": "triggered", "on": ["before:close"]}`,
   and the guard `COUNT(settled) > 0 AND ALL(settled, state = "paid")`.
   The version the closing makes holds those lines. A list to browse
   is `"mode": "live"` instead: asked at every read, kept nowhere.
   **For a triggered variable `write` says who may bring its value
   again** with `refresh` (ADR-0078): a value a decision stands on,
   such as a limit brought from a table, gets
   `"*": {"limit": {"write": "FALSE"}}, "review": {"limit": {"write": ["managers"]}}`,
   so that nobody without a part in the document changes the number
   somebody is deciding on. With no line, whoever may read the
   document may refresh it.
   Rights narrow what `access` allows and never widen it.
7. **Write the rules.** They run. Read `Actions` first: a rule names an
   engine action or one the catalogue lists, and gives a defined action
   its arguments under `args`, a formula for each parameter. To reach
   an API that has no action yet, write a definition with a `request`,
   check it with `ValidateAction`, look at what it would send with
   `PreviewAction`, and run it once with `TryAction` before a document
   depends on it. Never write a token into a definition or a class: a
   request reads it with `SECRET("name")`, and the host program
   supplies it. To go on from the answer, give the action an `into`
   and guard the next transition on the variable it fills; guard
   another on the error variable, so that a failure is a state and not
   a document that waits for ever.
   **Write the access rules**, all four: who sees a document, who
   creates one, who changes it and who deletes it. They are enforced.
   A transition that only some may take gets a rule of its own under
   `access.transition`; one without is open to whoever may `update`.
   What a rule does to another document is done as the person who set
   it off, so that person needs the other class's rules to let them.
   **A table has two rules too**: `"access": {"read": "TRUE", "write":
   "INGROUP(\"treasury\")"}`, each about the table as a whole. A rule's
   `table.update` is refused when whoever set the rule off may not
   write the table. Whether you may define at all is the host
   program's to say: defining is for the system group `designers`,
   which it gives a caller in `Principal.System`. Without it expect
   `forbidden`, naming the group. Every version you define is recorded
   with your id, what you are and for whom you act, and
   `Definitions(ctx, "class", name)` lists them for every caller. A class may speak of a system group
   as of any group, and `INGROUP("everyone")` is true for every caller
   that has an id.
   **Say what agents may do.** A caller is a person, an agent or a
   service, and may act for somebody: `USERKIND()` and `ACTINGFOR()`
   say so. Keep a decision for a person with `USERKIND() = "person"`,
   never with `<> "agent"`, which a caller of unknown kind would pass.
   Let an assistant in with `owner = USER() OR owner = ACTINGFOR()`.
   `USER()` is always the one who acts, the assistant and not the
   person it works for.
   **Say where a substitute is accepted.** The engine checks every
   "for" against a substitution before a formula runs (ADR-0053), so
   `ACTINGFOR()` is safe to write. A step the approver or their
   substitute takes, and never the one who filed the document, is
   `(approver = USER() OR approver = ACTINGFOR()) AND CREATEDBY() <> USER()`.
   Compare `ACTINGFOR()` only with a value that is never blank:
   `ACTINGFOR()` is blank for a caller who acts for nobody, and a blank
   equals a blank, so `manager = ACTINGFOR()` lets every caller through
   while `manager` is still empty. Write
   `NOT ISBLANK(manager) AND (manager = USER() OR manager = ACTINGFOR())`.
   A step that stays personal names `USER()` alone. A group does not
   pass to a substitute: `INGROUP("managers")` waits for a manager.
   The class `Substitution` is the engine's; a class of yours may read
   it, and may not take its name (`reserved_name`).
8. **Validate.** Write the class to a file and ask the tool:
   `fadodb class check invoice.json`. It ends with 0 when the engine
   would take the class and with 1 when a finding refuses it; `--json`
   prints the findings as one object, `{"diagnostics": [...]}`. Classes
   that name each other go in one call, `fadodb class check
   receipt.json invoice.json`, and `--db FILE` checks against the
   database the class is meant for, writing nothing. Loop: check, mend
   the place the finding names, check again, until it ends with 0 and
   no `no_description` note is left. A `reads_all` note says that a
   `LOOKUP` or a list of the class reads every document of the class it
   looks in: begin its condition with a comparison of a variable for
   equality with a value of this document, and the engine keeps the
   index; or leave it when that class stays small or the question is a
   total over all of it. A `builds_index` note is no fault to mend: it
   says that taking the definition makes an index over a large class,
   so tell the person and let them choose the hour. The
   same check is the command `validate_class` on a server, and
   `ValidateClass` and `ValidateClasses` on the Go API, whose findings
   for several classes start with the position of the definition,
   `/1/variables/total`. Each diagnostic carries a stable code, a JSON
   pointer to the element, an offset inside the formula, a message and
   often a hint. Fix the place it names; do not guess around it. An
   action in a file is checked the same way, with `fadodb action check
   tickets.json --settings settings.json`.
9. **Preview the draft against live data** with `PreviewDraft` before
   defining it: it runs the draft through the commit pipeline over an
   existing document or as a create and writes nothing.
10. **Define.** A redefinition is a new version that touches no document
   (ADR-0039). On a server, from the file that was checked:
   `fadodb exec define_class --from definition=invoice.json`, with the
   key of a designer in `FADODB_KEY`
   ([commands from a terminal](../../docs/manual/commands-from-a-terminal.md)).
11. **Give the words of each language the people use**, once the class
   is defined: `define_words` with `{"class": "Invoice", "language":
   "pl", "label": "Faktura", "variables": {"net": {"label": "Kwota
   netto"}}, "sections": {...}, "states": {...}, "transitions": {...}}`,
   each thing by its path (a column as `lines.qty`). Never translate a
   name. The words make no new version of the class. After a class
   version that drops a variable, `list_words` notes its words as
   `unused_words`: define the words again without it.

## Why the order matters

An agent that writes the whole class in one pass and reads the
diagnostics once gets a class that compiles. An agent that follows the
shape gets a class a person can read in two minutes, with the states a
process owner recognises. Both matter: the second is why the class is a
document and not code.

## Worked examples

- [`../../examples/first/invoice.json`](../../examples/first/invoice.json),
  the invoice of the manual's first application: two states with their
  own editors, a guard, and a program that walks it through,
  `go run ./examples/first`.
- [`../../examples/classes/invoice.json`](../../examples/classes/invoice.json),
  the Invoice from the README: an approval chain with a `rows` variable,
  a rule and rights per state.
- The guides under [`../../docs/manual/guides/`](../../docs/manual/guides/),
  one task each with a class that a test runs: editors per state, a
  field read-only after approval, four eyes, reminders and a deadline,
  acting for somebody. Take the pattern from the guide before writing
  your own.
- The six samples the front page of fadodb.org links to, planned.

## Where the rest is

The reference this page once planned is the manual now: the chapters
under "Read first" above. The formula language is
[`../../docs/manual/formula-language.md`](../../docs/manual/formula-language.md),
and the statechart is the chapter on states and transitions under
[`../../docs/manual/notions/`](../../docs/manual/notions/). The list of
every function and the list of every code are generated from the
engine. The decision records behind them are listed in
[`../reference/README.md`](../reference/README.md), and the contents of
the whole manual in [`../../docs/README.md`](../../docs/README.md).
