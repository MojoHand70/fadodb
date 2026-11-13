# ADR-0049: Who may see, create, change and delete a document

- Status: Proposed, with one question open for the owner; amends ADR-0010 (the paths the read rule covers, what a transition without a rule asks, the change stream waits for the server), ADR-0027 (the optional package of users and keys waits for the server), ADR-0041 (the class-level rules are enforced), ADR-0038 (the read rule joins the filter, and the SQL endpoint is not for every caller), ADR-0033 (`OpenFile` asks the read rule and the right), ADR-0044 (what a refresh asks), ADR-0026 (the reply to a repeated key), ADR-0025 (the preview of a delete)
- Date: 2026-11-13

## Context

A class can say four things about its documents, each a formula that is
TRUE or FALSE for whoever calls (ADR-0010), beside the rule of each
transition:

```json
"access": {
  "read":   "owner = USER() OR INGROUP(\"finance\")",
  "create": "INGROUP(\"sales\")",
  "update": "owner = USER() AND state = \"draft\"",
  "delete": "FALSE",
  "transition": {"approve": "INGROUP(\"managers\") AND CREATEDBY() <> USER()"}
}
```

The class format takes all of it and the compiler checks it. Since
Phase 3 the engine enforces the rule of a transition and the rights
table, which says who sees and who sets each variable in each state
(ADR-0028, ADR-0041). The four rules of the class are checked and not
enforced: every caller reads every document of every class, and may
create, change and delete any of them.

Earlier records left pieces of this to Phase 5: the filter keeps the
principal for the read rule (ADR-0038), `OpenFile` waits for it
(ADR-0033), a rule's action on another document is to ask it of the
person who set the rule off (ADR-0044, item 11), and a refresh asks
nothing yet, so anybody's child may wake its parent (ADR-0044).

The owner opened Phase 5 on 2026-11-13 and agreed its scope. In it:
these four rules; the kind of actor and whom it acts for (ADR-0050);
who may define, and the rules of tables (ADR-0051). Moved to Phase 6,
with the server they need: the engine's own users, groups, API keys and
sessions, and the filter on the change stream.

The caller is as often an agent as a person. A refusal therefore has to
name a stable code, the rule that refused and what would change the
answer, and an agent has to be able to ask before it acts.

Items 1, 8, 9 and 10 build on accepted records and name them. What they
add to those records, and every other item, is proposed here and is the
owner's to change. The question at the end is his to rule.

## Decision

1. **Four rules, enforced.** `read`, `create`, `update` and `delete`
   are conditions, as the rule of a transition is. TRUE lets the caller
   through. FALSE, a blank, a value of another type and an error do not
   (ADR-0019). An administrator passes every rule and never a guard.

2. **What each command asks, and over which document.**

   | Command | Asks | Over |
   |---|---|---|
   | `Get`, `Version`, `History`, `Events`, `Transitions`, `OpenFile` | `read` | the document as it is stored now |
   | `Query`, `Count`, `Due` | `read`, for every document | each document as it is stored now |
   | `Create`, `CreateMany` | `create` | the new document, with its defaults and its computed values |
   | `Set`, `Reset` | `read`, then `update` | the stored document, before the change |
   | `Transition`, `TransitionMany` | `read`, then the rule of the transition; `update` when the transition has no rule | the stored document, before the change |
   | `Delete` | `read`, then `delete` | the stored document |
   | `Refresh` | `read` | the stored document |

   The write rights of the variables a command sets are asked after
   these, as today (ADR-0041). A rule is asked over the stored document
   because it says who may change this document, not who may change the
   document the change would make (ADR-0041, item 2). So Ann may hand
   an invoice to Bob and lose it in the same commit, and nobody gives
   themselves a right by the change that uses it.

   A transition with a rule of its own is not asked `update`: a manager
   approves an invoice she may not edit. A transition without one asks
   `update`: who may change the document may move it, until the
   transition says who. `CreateMany` stays all or none (ADR-0035): one
   document its caller may not create refuses the batch.

3. **Hidden means absent.** For a caller the `read` rule does not let
   through, a document does not exist. Every command that names it
   answers `unknown_document`, in the words it uses for an id that was
   never given. A filter, a count and `Due` leave it out. In
   `TransitionMany` its entry is `unknown_document` (ADR-0048, item 3).
   One case shows through: `Create` with `WithID` of an id that is taken
   answers `duplicate`, since two documents cannot share an id.

4. **A refusal names the rule.** A caller who sees a document and may
   not do what they ask gets `forbidden`, with the place of the rule in
   the class as its element (`/access/update`), a message that names
   the caller, the command and the document, and the text of the rule
   as the hint, as a refused transition has today. Where the class has
   no such rule, the hint says so and what to add.

5. **The read rule in a filter.** The rule joins the `WHERE` of every
   `Query`, `Count` and `Due` as one predicate, written by the SQL
   generator from the same formula the Go VM runs for `Get`. This is
   why a read rule may use only the document's own variables, the
   functions of its row such as `CREATEDBY()`, the caller and the
   clock, which the compiler already enforces (ADR-0010, ADR-0047).

   The parts of a rule that read only the caller are decided before the
   statement is written. For Ann, who is not in finance, the rule above
   is `owner = USER()`, and an index on `owner` serves it. For a member
   of finance it is TRUE, and nothing is added. For a caller it lets
   through for no document, the answer is empty and the table is not
   read. A statement is kept for each class and each outcome of those
   parts, not for each caller.

6. **The past is read under the rule of today.** Who may see a document
   now may read its versions, its history and its events. Who may not
   sees none of them, whatever the rule would have said of an earlier
   version. For a deleted document the rule is asked over its last
   version. The rights table goes on deciding, row by row, which
   variables of each version show (ADR-0041, item 4).

7. **A commit whose caller cannot see its result.** Ann files a
   complaint that only HR may read. The command succeeds, and its reply
   is the document with its id, its version and its state, and no
   values. A repeat of a command key (ADR-0026) is answered the same
   way: the document if whoever repeats may see it, that bare reply if
   not.

8. **What the engine does by itself asks nothing.** Eventless and timed
   transitions, the wake-up of a timer (ADR-0047, item 12), what a rule
   sets on its own document, the result an action writes back, defaults
   and recalculation are the document's own and no rule is asked. An
   access rule of FALSE on a timed transition still leaves it to time
   alone.

9. **A rule's action on another document asks as the person**
   (ADR-0044, item 11, the owner's ruling of 2026-11-10). When paying
   an invoice makes a receipt, the `create` rule of `Receipt` is asked
   of whoever paid. A `refresh` of another document asks its `read`
   rule, so a child wakes only a parent that the person who moved the
   child may see. What the person may not do is a dead row with
   `forbidden` and the rule: the row is read by whoever operates the
   queue, so it says the true reason and not `unknown_document`.

10. **A formula still reads with the engine's eyes** (ADR-0046, item 2,
    the owner's ruling). A `LOOKUP` over a class adds no rule. `create`,
    `update`, `delete` and the rules of transitions may themselves read
    other documents and tables; `read` may not, since it runs for every
    row.

11. **Files.** `OpenFile` asks the `read` rule of the document and then
    the read right of the variable that holds the file (ADR-0033).
    `OpenBlob` stays the administrator's.

12. **The SQL endpoint adds no rule, and is not for every caller.** A
    statement the caller writes cannot be given a rule per class without
    rewriting it. So `SQL` is for an administrator and for the analysts
    the host names (ADR-0051), who read every row and every variable, as
    ADR-0028 says of this endpoint. Any other caller is refused with
    `forbidden`. Today every caller may use it.

13. **Asking first.** `Preview` runs the checks of this record and
    answers the refusals the command would get, all of them and not the
    first. It takes a delete too. `Transitions` reports a transition
    that `update` refuses as it reports one its own rule refuses. An
    agent that wants to know whether it may act asks, and nothing is
    written.

14. **The suite.** `conformance/access/` holds classes, documents and
    callers, and for each rule and each caller the documents every path
    must show: `Get` on the Go VM, `Query`, `Count` and `Due` in SQL,
    the history, the files, and each command with the refusal it gets.
    The phase is done when every path agrees on every case. The change
    stream joins the suite with the server.

15. **What this record leaves out.**

    - Rules inside a SQL statement the caller writes (item 12).
    - A `read` rule that reads other documents. The answer is kept in a
      variable of the document and the rule tests that variable.
    - Sharing a document with a list of people. It needs a list in a
      predicate, which the SQL generator does not write yet.
    - The filter on the change stream, the authenticator and the
      package of users, groups and API keys: Phase 6, with the server.

## Open question

This one is the owner's to rule. The record assumes the first answer
until he does, since it is what ADR-0010 says today.

**A. What does a class allow when it says nothing?** A class is defined
with no `access` block, or with a `read` rule and no `update` rule.

- *Nobody but an administrator* (ADR-0010 as accepted). A rule that is
  left out is FALSE. A class with no `access` block stores documents
  that only an administrator sees and changes. `ValidateClass` reports
  each rule a class leaves out as a note, a finding that refuses
  nothing, the first of its kind; and the refusal of item 4 names the
  missing rule and what to add. An agent that defines a class and
  forgets its rules is stopped at its first command and told why.
- *Everybody*, as the engine behaves today. A rule that is left out is
  TRUE. Nothing that works today stops working, and a class whose
  author forgot the rules is open to every caller without a sign.

The first answer has a price. Every class that people use needs its
four lines, and the samples, the README and the tests that run as
somebody other than an administrator gain them. A call without a
principal, which is nobody (ADR-0041, item 6), sees and does nothing,
so a first program either names an administrator or writes rules.

Recommended: nobody but an administrator. A database that gives the
authority to act should give none that was not written down.

## Consequences

- A person and an agent see the same documents through `Get`, a filter,
  a count, the history and a file, because one formula answers on every
  path and the suite proves it.
- Whoever cannot see a document cannot learn that it exists, except
  through an id they chose themselves.
- A listing with no `where` is narrowed by the rule: Ann's listing of
  invoices reads her invoices through the index on `owner`, not the
  table.
- A child wakes its parent only if the person who moved the child may
  see the parent. The purchase and its approvals (ADR-0046) keep
  working where an approver may read the purchase they approve.
- The SQL endpoint closes to callers who are neither administrators nor
  analysts. A host that gave people SQL has to name them analysts or
  give them filters.
- `Refresh`, `Transitions` and `Preview` refuse a caller they answered
  before, where the class has a `read` rule that caller fails.
- The class format and the bytecode do not change. The Go API gains the
  preview of a delete. The engine's schema does not change with this
  record.

## Performance

Budgets, on the reference machine:

- On the indexed filter of ADR-0038, a read rule adds under 5% when the
  caller alone decides it, as `INGROUP("finance")` does, and under 15%
  when it reads the row and the caller, as `owner = USER()` does. These
  are the budgets ADR-0041 set for a read right, which is the same
  predicate on one variable, after measuring 3.4% and 10 to 12%.
  ADR-0010's single figure of 5% was not reachable there, for the
  reason that record gives.
- A filter with no `where`, by a caller who sees only their own
  documents through an indexed variable, reads through the index. Its
  time is measured against the same filter with the condition written
  out, and published.
- A read by id and a commit each gain one evaluation of one rule, with
  no allocation, inside the budgets they have (ADR-0006, ADR-0012).
  The benchmarks of the gate that commit and read run on a class with
  rules too, and hold.
