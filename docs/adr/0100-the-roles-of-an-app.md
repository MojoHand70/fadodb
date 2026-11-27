# ADR-0100: The roles of an app

- Status: Proposed; amends ADR-0077 (an app's definition gains `roles`), ADR-0049 (a formula may ask for a role besides a group), ADR-0055 (one command joins the table, two answer more), ADR-0057 (`whoami` answers the caller's roles)
- Date: 2026-11-27

## Context

A class says with formulas who may see, create, change, delete and
move its documents (ADR-0049), and who may write or see a variable in
each state (ADR-0028). Those formulas name the groups of the server
they run on: `INGROUP("approvers")`.

An app is written once and runs on many servers. Apps reach a server
from the marketplace (the owner, 2026-11-24), and every client has a
server of its own (ADR-0056). An app cannot know the groups of a
server it has never seen, so today a class moved to another server is
rewritten, or that server makes groups under the names the class
happens to use.

On 2026-11-27, with the first testers on a server open to the
internet, the owner asked where a designer says who may create a
document and who sees and edits it in each state, and how the entry
"Roles and permissions" of the designer's tree relates to states and
transitions. The answer was that it relates to nothing yet: the engine
has no roles of an app. The only roles it knows are the five of
ADR-0052 (database administrator, designer, content administrator,
analyst, user), which are system groups and belong to the server, not
to an app.

Two ways were put to him: roles per app, which each server gives to
its own groups and people; or the server's groups named directly, with
nothing new in the engine. His words: "yes, roles per app and a table
per class".

This record is the engine's half, the role. The table per class is the
face's: it shows and writes the lines a class has already, `access`
and `rights`, with the function this record adds.

## Decision

1. **An app names its roles in its definition.**

   ```json
   {
     "name": "Expenses",
     "label": "Expenses",
     "description": "What employees paid for the company, approved and paid back.",
     "roles": {
       "employee": {"label": "Employee", "description": "Files expenses and is paid back."},
       "approver": {"label": "Approver", "description": "Decides the expenses of the people they manage."},
       "finance":  {"label": "Finance",  "description": "Pays the approved expenses."}
     }
   }
   ```

   A role's name follows the rule of every name: letters, digits and
   underscores. The name is the role's identity; its label may change.
   A role says nothing of who holds it. That is each server's.

2. **A formula asks for a role with `INROLE("approver")`.** It is TRUE
   when the caller holds that role of the app the class belongs to. It
   stands wherever `INGROUP` may: every line of `access`, the read rule
   among them, since like `INGROUP` it asks the caller and not the
   document; `rights`; a transition's condition; a variable's formula.
   A table that belongs to an app asks its roles the same way in its
   read and write rules.

   - The name is a text written in the formula, never worked out, so
     that the definition is checked: a role the app does not name is
     refused with `unknown_role` at that place, and the fix says to add
     it to the app's roles.
   - A class or table of no app has no roles. `INROLE` there is
     refused the same way, and the fix says to put it in an app.
   - A class asks the roles of its own app only.

3. **Each server says who holds each role: groups, people, or both.**

   ```json
   {"app": "Expenses", "role": "approver", "groups": ["managers"], "users": ["marek"]}
   ```

   The command `set_role` gives a role its holders. It replaces both
   lists at once, so sending the same twice changes nothing, and an
   agent can repeat it safely. The holders are kept in the database
   beside the app, never in its definition. So an app taken to another
   server carries its roles and none of this server's people. Every
   change is kept with who made it, for whom, and when, as the changes
   of a group are (ADR-0086).

4. **An administrator gives roles to people; a designer defines them.**
   `set_role` is an administrator's, as keeping users and their groups
   is (ADR-0052, ADR-0057). Defining or changing the roles of an app is
   a designer's, as defining the app is (ADR-0077).

5. **The caller holds a role when one of their groups, or their id, is
   among its holders.** Their groups are their own, their system
   groups, and `everyone` (ADR-0052), as `INGROUP` answers them.
   - A role that nobody holds lets nobody through, as a line left out
     does.
   - A group named as a holder that no user is in yet is kept: a group
     is a name on a user (ADR-0057).
   - An agent holds a role as a person does, through its groups or its
     id. A class that keeps a step for people still says so with
     `USERKIND()`.
   - Whoever acts for somebody holds their own roles, not that
     person's, as with groups (ADR-0057).

6. **A role that a class or table asks for cannot leave the app.** A
   new version of the app without it is refused, naming each class and
   table that asks. A role nobody asks may leave. Its holders go with
   it, and the change is kept.

7. **Who holds what is visible.**
   - `get_app` answers each role with its holders, to every caller, as
     `list_users` shows everybody's groups (ADR-0057, ruling 4).
   - `whoami` answers the caller's roles, app by app:
     `"roles": {"Expenses": ["approver"]}`. A page uses that to answer
     `INROLE` as the engine does, and an agent can see what it holds
     before it acts.

## Not here

- **The table per class** is the face's. It writes `access` and
  `rights` with `INROLE`, and a designer's tool records it in its own
  place.
- **A role of another app.** A class asks for its own app's roles.
- **Roles inside roles, and a role among a role's holders.** No group
  holds a group either (ADR-0057).
- **The role of one document**, such as the approver of this expense.
  That is a variable that holds a person, `approver = USER()`, as
  today.
- **A system table of roles** beside the four of ADR-0092. One can
  follow when asked.
- **Roles of the app System.** It has none.

## Consequences

- A class written with `INROLE` runs on any server once an
  administrator there says who holds its roles. Nothing in the class
  changes.
- `INGROUP` stays. A class may still name a server's group, and the
  system groups are still groups.
- `INROLE` is answered on the Go VM, in SQL (SQLite and DuckDB) and on
  the browser VM, with cases under `conformance/` played on all three.
- The database gains two tables, the holders and their changes. The
  schema moves on by one, so going back to an earlier engine means the
  backup taken before.

## Performance

- A database whose apps name no roles pays nothing: no call works out
  roles.
- With roles, the holders live in memory, made again when a holder
  changes. A caller's roles are worked out once per call, and only when
  a formula of that call asks `INROLE`. The benchmarks of the gate name
  no roles and must show no change.
