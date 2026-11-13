# ADR-0051: Who may define, operate and query, and the rules of a table

- Status: Proposed; amends ADR-0046 (who may define and preview a class), ADR-0045 (who may define and try an action), ADR-0044 (who may operate the queue; a table action asks the table's rule), ADR-0038 (who may use the SQL endpoint), ADR-0037 (a table has rules), ADR-0039 (who may migrate, and a version records who defined it), ADR-0027 (the host names three lists of groups)
- Date: 2026-11-13

## Context

The rules of ADR-0049 guard documents. Some calls are not about one
document, and today nothing guards them:

- `DefineClass`, `DefineClasses`, `DefineSchema`, `DefineTable` and
  `DefineAction` take no caller at all. Whoever holds the open database
  defines.
- A formula reads every document (ADR-0046, item 2), so whoever may
  define a class, or preview a draft of one, can read any document
  through it. ADR-0046 left to Phase 5 who that is.
- A redefinition can rewrite the rules of ADR-0049 themselves.
- `Outbox`, `RetryAction` and `TryAction`, which sends a real request,
  take no caller. `SQL` reads every row for any caller.
- A table has no rules (ADR-0044, item 11): `TableRows` and
  `ImportRows` serve every caller.

In a program that embeds the engine this is the program's own business:
it decides which of its code reaches which call. With the server of
Phase 6 these calls are commands on the wire, and the engine has to
know who may send them. The agents that work with a database come in
the same kinds: one designs classes, one operates the queue, one mines
the log, and most do their own work on documents (the skills under
`skills/`).

The owner agreed on 2026-11-13 that who may define, and the rules of
tables, are in Phase 5. Everything below is proposed here and is his to
change.

## Decision

1. **Three kinds of work beside documents, each for the groups the
   host names.** `Open` takes three lists of group names:

   | Option | Its members may |
   |---|---|
   | `Designers` | define and redefine classes, schemas, tables and actions; preview a draft class on real documents; migrate the documents of a class |
   | `Operators` | list the outbox, retry a dead action, drain the queue, preview and try an action, check the blobs |
   | `Analysts` | run `SQL`, which reads every row and every variable (ADR-0049, item 12) |

   An administrator may do all three. A list left empty leaves that
   work to administrators. Any other caller is refused with
   `forbidden`, and the hint names the option.

2. **Why the host names them, and not the database.** Who may rewrite
   the rules cannot be decided by a rule that they may rewrite. So the
   lists are given when the database is opened, as the hosts a database
   may call and its secrets are (ADR-0045), and nothing stored in the
   file can widen them.

3. **The calls take a caller.** `DefineClass`, `DefineClasses`,
   `DefineSchema`, `DefineTable`, `DefineAction`, `Outbox`,
   `RetryAction`, `Drain`, `PreviewAction`, `TryAction`, `CheckBlobs`
   and `TableRows` gain the options every other call has, and with them
   `As`. `Migrate` and `PreviewDraft` have them already. A call without
   a principal is nobody (ADR-0041, item 6) and is refused.

4. **What stays open to every caller.** Reading a definition: `Class`,
   `Classes`, `Table`, `Tables`, `Action`, `Actions`. An agent has to
   read a class to work with its documents, and a class holds no
   secret. `ValidateClass`, `ValidateClasses` and `ValidateAction`,
   which store nothing and read no document.

5. **Every version says who defined it.** A version of a class, a
   schema, a table and an action records the id of whoever defined it,
   with the kind and the id acted for of ADR-0050, and when. Under
   schema 6, with the columns of ADR-0050. A redefinition is the
   strongest act there is in a database, and the record of it is the
   answer to what a designer can do: anything, and in sight.

6. **A designer can read through any class**, and this record says so
   plainly. A formula sees every document (ADR-0046), so a class
   written to look up salaries and show the sum will do it. Designers
   are therefore named with the care administrators are. What separates
   them from administrators is that a designer's own commands on
   documents still ask the rules of ADR-0049, and that what a designer
   changes is a version with their name on it.

7. **A table has two rules.** A table definition takes `access` with
   `read` and `write`, each a condition over the caller and the clock:

   ```json
   {"name": "rates", "key": ["currency", "day"],
    "columns": {"currency": "text", "day": "date", "rate": "decimal(6)"},
    "access": {"read": "TRUE", "write": "INGROUP(\"treasury\")"}}
   ```

   `TableRows` asks `read`. `ImportRows` asks `write`, and so do the
   `table.insert`, `table.update` and `table.delete` actions of a rule,
   as the person who set the rule off (ADR-0044, item 11). The rules
   are about the table as a whole and may not read a column. A rule
   left out is answered as question A of ADR-0049 is ruled: under its
   first answer, only an administrator reads the rows or writes them.
   A formula goes on reading a table with the engine's eyes: a `LOOKUP`
   asks no rule (ADR-0046).

8. **What this record leaves out.**

   - The right to define one class and not another. Until a case needs
     it, a designer designs everything.
   - A class that names the classes allowed to read it, which ADR-0046
     said Phase 5 may add. A designer may redefine the class that holds
     the list, so the list would stop nobody who can define; it is
     worth having only together with the item above.
   - Rules for single rows of a table.
   - Who may open a database, copy its file or read a blob store from
     outside. That is the host's and the operating system's.

## Details settled while building

Built on 2026-11-13, on the record as proposed, as the fourth step of
the phase, but for the two things below.

- **A host program that names no list is asked nothing.** Item 1 says
  a list left empty leaves its work to administrators. Built: a list
  that is empty and not nil does that, and a host program that gives
  no list at all is asked nothing, as before these options. One
  constant holds that answer. The other answer refuses every
  definition made without an administrator, which is every sample and
  nearly every test, so it waits for the owner's word with question A
  of ADR-0049, which has the same price.
- **Item 5 is not built**: a version does not yet record who defined
  it. It needs a way to read the record back, which no call has, and
  that is a decision on the command set.
- **The names.** `Options.Designers`, `Options.Operators` and
  `Options.Analysts`. The refusal is `forbidden`, with
  `/options/Designers` and the like as its path.
- **`TableRows` is governed by the table's `read` rule**, not by a
  list: it reads one table, and the table says who may.
- **A rule of a table is checked when the table is defined.** One that
  reads a column, another table or a document, or that is no
  condition, refuses the definition.
- **A table gains rules when it is defined.** This build redefines a
  table only while it has no rows and no class reads it (ADR-0037), so
  a table that is in use keeps the rules it was given.
- **A rule a table leaves out** answers as a class's does: it lets
  every caller through until question A of ADR-0049 is ruled.

## Consequences

- A host that serves several people has to say who its designers,
  operators and analysts are, or only administrators define, operate
  and run SQL. A program that embeds the engine for itself names an
  administrator for its own setup code.
- The definition calls and the queue calls change their signatures in
  the Go API: they take options. The command set of Phase 6 carries the
  caller for them as for every command.
- An agent that designs is not an administrator: it cannot approve a
  payment by its flag, only by redefining a rule, and the version says
  that it did.
- Reference data has an owner: treasury writes the rates, everybody
  reads them, and a rule that updates a table does so with the rights
  of the person who set it off.
- Every test and sample that defines a class names a caller who may.
- The class format does not change. The table format gains `access`.
  The engine's schema gains who defined each version.

## Performance

A test of the caller's groups against a short list on calls that are
not hot paths. A table's rule is one evaluation for an `ImportRows` or
a table action, inside the outbox's budget (ADR-0044), and
`BenchmarkOutbox` holds.
