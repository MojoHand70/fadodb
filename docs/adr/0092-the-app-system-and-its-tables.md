# ADR-0092: The app System and its tables

- Status: Proposed; builds what ADR-0077 and ADR-0079 left out (the app System, which every database is to have, and the system tables in it) and item 11 of ADR-0086 (the system table of groups); amends ADR-0036 (a system table is never kept in memory) and ADR-0091 (`table:` on a system table is refused)
- Date: 2026-11-26

## Context

What a database holds besides its documents is read by a command of
its own for each kind: `list_apps` and `get_app`, `list_classes` and
`get_class`, `list_users`, `list_groups`. A formula reads none of
them. So an expense that names its approver can only hold the id
`marek`, and a start page that shows the apps of the server as tiles
needs code outside the engine to ask `list_apps` and draw them.

The owner's words, each with its day:

- 2026-11-21, proposing it: "Maybe it is a good idea to actually
  introduce a notion of System Tables which hold: Apps, Classes,
  Users, Groups etc." (ADR-0086).
- 2026-11-23: "I think Engine by default should have one App called
  System. This is where system tables belong to." (ADR-0079).
- 2026-11-25, on the screens a designer works in: "Move system tables
  to Server navigation panel, add a node Tables and let it expand into
  system tables. This should be created any time a new server is
  open."
- 2026-11-26: "start building the tables".

A table is reference data a formula reads (ADR-0016, ADR-0037): a
definition with columns, a key and the rules of who may read and write
it (ADR-0051), rows the database file holds under `tbl_<Name>`, and a
view under the table's own name that every statement reads. The
engine keeps its apps, classes, users and groups in tables of its own:
`apps`, `classes`, `fado_users` with `fado_user_emails`, and
`fado_groups` (ADR-0086).

## Decision

1. **Every database has the app `System`, the engine's own.** The
   engine defines it, version 1, when it opens a database that has
   none, and writes down that `fadodb` defined it, as for any app. A
   database that has an app of that name, made by a designer before,
   keeps it with its words.
2. **Four tables belong to it**, each with one row for each thing of
   its kind that the database holds:

   | Table | Label | One row for each | Key | Columns |
   |---|---|---|---|---|
   | `SystemApps` | Apps | app | `name` | `name`, `label`, `description`, `icon`, `color`, `version`, `created_at`, `updated_at` |
   | `SystemClasses` | Classes | class | `name` | `name`, `app`, `label`, `description`, `version`, `created_at`, `updated_at` |
   | `SystemUsers` | Users | user | `id` | `id`, `kind`, `name`, `email`, `groups`, `system_groups`, `disabled`, `created_at`, `updated_at` |
   | `SystemGroups` | Groups | group | `name` | `name`, `label`, `description`, `members`, `created_at`, `updated_at` |

   `groups` and `system_groups` are `json`, a list of names;
   `disabled` is `bool`; `version` and `members` are `int`; the
   moments are `datetime`; the rest is `text`.
3. **A system table is a view over the engine's own records**, under
   the table's name. Nothing is copied, so nothing waits to be
   brought up to date: its rows are what the database holds at the
   moment it is read. The engine makes the view when the database has
   none of the name, makes it again when its text differs from this
   engine's, and otherwise writes nothing.
4. **It is read as any table.** `LOOKUP`, `QUERY` with a table as its
   source, `table_rows`, `get_table`, `definitions` and `list_tables`
   take it. Its definition says `"app": "System"` and the read rule
   `TRUE`; the engine writes down once that `fadodb` defined its
   version, so `get_table` says so in `defined`.
5. **Everybody may read the four**, as `list_users` and `list_groups`
   answer every user today and every definition is open to
   everybody.
6. **Nobody writes one.** `import_rows`, `delete_rows` and
   `remove_table` are refused with `read_only`, a content
   administrator's too: there are no rows of its own to write. A
   rule's `table.insert`, `table.update` or `table.delete` that names
   one is refused with `not_allowed` when the class is defined. What
   a system table says is changed by the command of what it shows.
7. **Its name is the engine's.** A table and a class named
   `SystemApps`, `SystemClasses`, `SystemUsers` or `SystemGroups` are
   refused with `reserved_name`. A database whose own table or class
   took one of the names before this engine keeps it, and that system
   table is left out of that database.
8. **It is never kept in memory** for the formulas that read it
   (amends ADR-0036): every lookup of one is a statement, so a
   formula never reads a user who is gone or a name that changed.
9. **`table:` on a system table is refused** with `not_supported`
   when the class is defined (amends ADR-0091): nothing brings a kept
   value again when the engine changes the rows of a system table,
   and a value that follows nothing would go stale with nobody told.
10. **A later engine only adds columns to a system table**, never
    takes one away or changes its type, since a class may read it and
    a database opens only if every class it keeps compiles.

## The case

At Acme, Olga administers the server, Dan designs the classes, Ann
files expenses and Marek, in the group `approvers`, approves them.
Dan's expense names its approver as a person, and his start page
lists the apps:

```json
{
  "name": "Expense",
  "label": "Expense",
  "variables": {
    "amount":        {"type": "money(2)", "required": true, "label": "Amount"},
    "approver":      {"type": "text", "required": true, "label": "Approver",
                      "description": "The id of the user who approves the expense."},
    "approver_name": {"type": "text", "mode": "live", "label": "Approver's name",
                      "formula": "LOOKUP(SystemUsers, name, id = THIS.approver)"},
    "approvers":     {"type": "int", "mode": "live", "label": "Approvers in the company",
                      "formula": "LOOKUP(SystemGroups, members, name = \"approvers\")"}
  },
  "states": {"initial": "draft", "list": ["draft", "submitted", "approved"]},
  "transitions": [
    {"name": "submit", "from": "draft", "to": "submitted"},
    {"name": "approve", "from": "submitted", "to": "approved", "guard": "USER() = approver"}
  ],
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}
```

```json
{
  "name": "Start",
  "label": "Start page",
  "variables": {
    "apps": {"mode": "live", "label": "Apps",
             "formula": "QUERY(SystemApps, {name, label, icon}, name <> \"System\", {label})"}
  },
  "states": {"initial": "shown", "list": ["shown"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "FALSE", "delete": "FALSE"}
}
```

Ann files an expense of 120.00 PLN for `marek`: it says
`Marek Nowak`. Olga changes his name to Marek Nowak-Lis, and the next
reading of the expense says so. Olga puts Ann into `approvers` as
well, and `approvers` is 2. Dan defines Expenses and Helpdesk, and the
start page lists them by their labels, without System. The chapter
`docs/manual/system-tables.md` tells this story, and a test plays it.

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **The names begin with `System`**: `SystemApps`, not `Apps`. The
   database already has SQL tables named `apps` and `classes`, which a
   view of the same name cannot stand beside, since SQL does not tell
   names apart by capitals; and `Users` and `Groups` are names a
   designer is likely to want for a table or a class of their own.
   The labels are Apps, Classes, Users and Groups, which is what a
   screen shows. The other way: the four plain names, with a second
   name for the view of each, and `Users` and `Groups` taken from
   designers.
2. **Everybody reads them**, with the rule `TRUE`, as the commands
   answer everybody now. The other way: users and groups for
   administrators only, which would also change `list_users` and
   `list_groups`.
3. **`SystemUsers` holds the e-mail**, which `list_users` answers
   everybody today. A person's e-mail is personal data; if it should
   be read by administrators only, both change together.
4. **`SystemGroups` holds how many users are in each**, disabled ones
   too, as `list_groups` counts them. It is the same for every reader,
   so it is no statistic that depends on who asks. Counts of
   documents are never columns of a system table: a count of the
   documents of a class depends on what the reader may read, and
   `count` and `flow` answer it for the caller.
5. **`list_tables` and `list_apps` name them.** `list_tables` lists
   the four after the database's own tables, in a fixed order, so the
   list is the same after the database is opened again; with
   `"app": "System"` it lists them alone. `list_apps` names `System`,
   first in a new database. A face that shows the apps leaves System
   out by its name. The other way: leave them out of both lists
   unless the app is asked for.
6. **The app `System` may be given other words** with `define_app`,
   as any app; its name and its tables stay the engine's. A database
   where a designer made an app `System` keeps its words, and is not
   given a version of the engine's.
7. **`fadodb check` counts the tables of the database's own**: the
   four views hold no rows to check.
8. **A class does not need to belong to System** to read its tables,
   and no class of a designer's belongs to it unless the designer
   says so.

## What is built

- The model: `internal/model/systemtables.go` (the names, the app's
  definition, each table's definition); `reserved_name` for a class
  under a system table's name, `not_supported` for `table:` on one,
  `not_allowed` for a rule's table action that names one.
- The store: `internal/store/systemtables.go` (`keepSystem`,
  `keepView`, `bringSystemApp`), called when a database is opened,
  after its own tables are read and before its classes are, so that a
  class that reads a system table compiles; a system table's
  statements read only; the refusals of `ImportRows`, `DeleteRows`,
  `RemoveTable` and `DefineTable`.
- Tests: `internal/store/systemtables_test.go` (what each table holds,
  a value worked out in a commit and a live one after a name changes,
  every refusal, a reopening with the views unchanged, and a database
  from before with its own app System and its own table
  `SystemGroups`); `fado/systemtables_test.go` plays the chapter.

## Consequences

- A formula names a person, a group's size, an app or a class from
  the database itself, and a start page lists the apps with a `QUERY`
  and no code of its own.
- An agent finds the people of a group, and who is disabled, with a
  `QUERY` it already knows how to write.
- `list_apps` and `list_tables` answer more than before in every
  database. A program that took an empty list for a new database
  sees System and its four tables.
- The four names are taken from designers in every new database.

## What this record leaves out

- **A value kept by a triggered variable that follows a system
  table**, `table:SystemUsers`: refused. To build it the engine would
  queue the follows of ADR-0091 when it changes an app, a class, a
  user or a group.
- **Tables of the other things a database holds**: schemas, tables,
  actions, inlets, the owner's "etc.".
- **`hidden` on an app**, the owner's words of 2026-11-22: "This
  marks an app as technical and app is never shown to people only to
  designers." The engine refuses the member today; whether System is
  hidden waits for it.
- **The memberships as a table of their own**, one row for each user
  and group: `groups` is a list in the user's row.
- **The engine's class Substitution** stays in no app (ADR-0077,
  item 4); whether it moves into System is still for later.

## Performance

No hot path changes for a class that reads no system table: a lookup
asks one more field of the table before it builds its rows in memory.

The bench gate of the first push of this record found twelve more
allocations for each list, figure, draft and look a formula is
compiled for (`DraftsOfOneOrder`, `LiveList`, `LiveFigure`,
`QueryLook`, and `Follow` by a hundredth of a percent): the store made
the set of the tables a formula may read anew at each compilation, and
every database now has four tables more. The store now keeps one set,
made again whole when a table is defined or removed and never changed
once made, so a compilation reads it and allocates nothing for it.
A lookup of a system table is a statement every time, as a lookup of a
large table is (ADR-0036); `SystemUsers` by `id` and the others by
`name` are found by the primary key of the engine's own table. Opening
a database reads four views' text and four rows of `definitions`, and
writes nothing when they are this engine's; the first opening writes
the four views, one row in `definitions` for each table, and, in a
database that has no app System, one row in `apps` and one more in
`definitions`. The engine schema stays 15.
