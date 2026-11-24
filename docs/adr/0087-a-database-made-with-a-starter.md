# ADR-0087: A database made with a starter

- Status: Proposed; amends ADR-0059 (item 1: `fadodb init` takes a starter). The owner chose the way of making it on 2026-11-24 ("Agreed with your proposal, --starter expenses"); every item below is for him to veto.
- Date: 2026-11-24

## Context

Phase 7 holds one starter, the approval of an expense by the
employee's manager (the scope ruled on 2026-11-17). A database that
`fadodb init` has just made is empty: no class, no table, nobody to
work in it. Tomek, the person who looks after the computers of a firm
of forty, takes FadoDB to try it, and wants a colleague approving an
expense the same evening.

Tried before anything was written, on a server built from main: the
engine already does all an expense approved by the manager needs. A
table names each person's manager; the class looks the manager up when
the expense is submitted; the access rules let the manager, and only
the manager, see and approve it. So the starter needs no change to the
engine, only a way into a new database.

Two ways were put to the owner, with that case:

- A. `fadodb init acme.fado --starter expenses`: the program carries
  the starter and makes it with the database.
- B. Nothing new in the program: the starter's definitions as files in
  the repository, and a page of the manual with the commands that send
  them, about eight.

His answer: A. The way an app is exported from one database and
brought into another, which he asked for on 2026-11-18, is not built
and not in the steps left of Phase 7, so it was not a third way.

## Decision

1. **`fadodb init FILE --starter NAME`** makes the database and, in
   it, the starter named. Only `init` takes a starter: no command
   sends one to a database that exists. A database that exists is
   given the same app by a designer or an agent, through the commands
   that make it.

2. **A starter is the commands that make it**, in the order they are
   sent: commands of the command set (ADR-0055), as `Exec` takes them,
   with nothing that only a starter can say. The program carries its
   starters; it takes none from outside. Somebody who has a starter of
   their own sends its commands with `fadodb exec`, or has an agent
   send them.

3. **It is made as the first user is**, on the machine, by no user,
   with the work of an administrator and of a designer (ADR-0059,
   item 1; ADR-0052). What it makes records no user as its author.
   From then on it is the database's own and is changed as anything
   else is.

4. **All or nothing.** When the database refuses a command of the
   starter, `init` removes every file it made, says which command was
   refused and why, and touches no file it did not make. A name the
   program has no starter under is refused before any file is made,
   and the refusal names the starters there are.

5. **`init` says what it made**, one line on standard output, and
   what to do next on standard error, as it does without a starter:

   ```
   made the database acme.fado with the starter expenses: the app Expenses, the group hr, the group finance, the table Employees, the class Expense
   ```

6. **The one starter, `expenses`**: what an employee paid for the
   company, approved by their manager and paid back by finance.

   | It makes | Name | |
   |---|---|---|
   | the app | `Expenses` | ADR-0077 |
   | the groups | `hr`, `finance` | with their labels and what they are for, and nobody in them (ADR-0086) |
   | the table | `Employees` | `person` and `manager`, user ids; read by everyone, written by `hr`; it belongs to no app, since who works for whom is the company's |
   | the class | `Expense` | in the app `Expenses` |

   The class, in the states `draft`, `submitted`, `approved`,
   `rejected` and `paid`:

   - **The manager is looked up at `submit`**, from `Employees`, by a
     triggered variable (ADR-0043), and kept: a person who moves to
     another team before submitting is decided by the new manager, and
     an expense already waiting stays with the one it was sent to.
     Nobody writes it by hand. An employee the table names no manager
     for cannot submit.
   - **A draft is the employee's alone.** The manager sees an expense
     from `submitted` on, `finance` sees every expense.
   - **Nobody approves their own expense**, even as their own
     manager, and nobody approves one for the person who filed it.
   - **Somebody acts for another while a substitution lasts**
     (ADR-0053): the manager's substitute reads and approves, an
     assistant, such as an agent, files and submits the employee's
     expenses, which are then the employee's.
   - **Once submitted, it is not changed**; `finance` pays it back.

   Its full definitions are in the manual's chapter on starters, which
   a test plays against a real server.

7. **A starter makes no people.** The users are the company's, and its
   administrator makes them. Whether a starter may bring people and
   documents to try it out with is the owner's question, open.

## Consequences

- From one command to an app the people of a firm can use: the four
  commands of ADR-0059, the first with `--starter expenses`, then the
  people.
- A starter is what an agent copies. Its definitions are held to the
  same checks as any, and a test makes every starter the program
  carries on a new database and fails on a note against it.
- The program carries the starter's text, about six kilobytes; built
  as main is on 2026-11-24, it measured the same size as main,
  16,089,250 bytes.
- A starter cannot be added to a database that exists, and an app is
  not exported and imported. When that is built, a starter may become
  an exported app the program carries.
- Building it found a trap for every designer: `ACTINGFOR()` is blank
  for a caller who acts for nobody, and a blank equals a blank, so
  `manager = ACTINGFOR()` lets every caller through while `manager` is
  empty. The class asks for a manager first, and the designer's skill
  says so.

## What this record leaves out

- More starters: with the example applications of Phase 12.
- A face: which face the people of a starter use in a browser is the
  owner's to name.
- The organisation's rule-book, the classes `Entity` and `Look` a
  starter is to give a new database (ADR-0060, item 4): not in this
  starter.

## Performance

No budget changes. Nothing runs on any path but `init` with
`--starter`; the starter's five commands take a few milliseconds.
