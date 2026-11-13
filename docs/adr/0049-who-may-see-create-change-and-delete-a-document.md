# ADR-0049: Who may see, create, change and delete a document

- Status: Accepted; amends ADR-0010 (the paths the read rule covers, what a transition without a rule asks, the change stream waits for the server), ADR-0027 (the optional package of users and keys waits for the server), ADR-0041 (the class-level rules are enforced), ADR-0038 (the read rule joins the filter, and the SQL endpoint is not for every caller), ADR-0033 (`OpenFile` asks the read rule and the right), ADR-0044 (what a refresh asks), ADR-0026 (the reply to a repeated key), ADR-0025 (the preview of a delete); amended by ADR-0052 (a content administrator passes the rules where an administrator did, with a reason the event keeps; a rule left out; who opens a blob by its hash)
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

Items 1, 8, 9 and 10 build on accepted records and name them. The
owner accepted the record on 2026-11-13 and ruled on the question it
put to him. The ruling is at the end.

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

## Details settled while building

The first step was built on 2026-11-13, on the record as proposed.

- **The ruling on question A is not in the code yet.** A rule a class
  leaves out lets every caller through, as the engine has always
  answered, and one constant holds that answer for the store and for
  the filter compiler (`model.LeftOutAllows`). The owner has since
  ruled the other answer. It is that constant and, with it, rules and
  named callers for the samples and the tests, and it is built with
  ADR-0052.
- **A set that names no value asks only `read`.** It changes nothing
  and answers with the document, which is what a read does. `update` is
  asked of a set that names a value and of every reset.
- **Where the refusal names the rule.** `Error.Path`, which until now
  held the place of a rule's action: `/access/create`,
  `/access/update`, `/access/delete`, `/access/transition/approve`. A
  transition refused by `update` says so in its message and carries
  `/access/update`.
- **`PreviewDelete`** is the preview of item 13 for a delete, a command
  of its own: the document as it is, the refusal under the errors, the
  rules the delete would run.
- **`History` and `Events` of a hidden document** answer an empty list
  and no error, which is what they answer for an id no document has.
- **A file in a hidden variable** is answered `unknown_blob`: for that
  caller the document does not hold it.
- **`Due` reads each document it found** for a caller who is not an
  administrator and whose class has a read rule, and leaves out the
  ones the rule hides. The host program that touches what is due asks
  as an administrator and pays nothing. The page of `Due` may then come
  back shorter than its bound while more documents are due.
- **Not built yet:** the SQL endpoint, which waits for the analysts of
  ADR-0051 (item 12).

Measured on the reference machine, in memory: a transition on a class
with a read rule and an update rule takes the time and the allocations
of the same class without them, 51 µs and 163 allocations
(`BenchmarkTransitionAccess` against `BenchmarkTransitionOwned`), and
`BenchmarkTransition` is unchanged at 155 allocations.

The second step, the same day, built the second paragraph of item 5.

- **What the caller decides.** An `INGROUP` of a text written in the
  rule, standing under `AND`, `OR` and `NOT` alone. Each is answered
  for the caller, and the rule is folded around the answers by the
  rules the evaluators follow: `AND` and `OR` stop at the operand that
  decides them, left to right. So `x OR TRUE` is TRUE only when `x`
  cannot stop with an error, which the SQL generator knows; where it
  can, `x` stays and is asked first, as on the Go VM. An `INGROUP` of a
  computed text is left to the statement.
- **Whether the caller has an id** is part of the outcome. For a caller
  with one, `owner = USER()` is written as the bare column against a
  value, since no blank owner equals it. For a call without a principal
  the comparison keeps its care for blanks.
- **A text compared with the caller is an index term in every filter**,
  in a `where` as in a read rule. Before, `owner = USER()` in a `where`
  read every document of the class.
- **The caller's condition leads.** The rule offers its comparisons to
  an index only when the `where` compares no indexed variable itself.
  SQLite keeps no statistics in a database of ours, so of two
  comparisons with an index each it takes one without knowing which
  finds fewer rows. With both offered, a ticket asked for by its number
  was found through the owner's index: every ticket of the caller read
  to find one, 818 µs where 15 µs were due.
- **Every path agrees, tested.** Beside the suite, a test takes fifteen
  rules, some of which stop with an error on some rows, eight callers
  and forty-eight documents, and asks for each whether a read by id, a
  filter and a count show the same documents.

Measured on disk, 100,000 documents a class, the median of six runs
(`BenchmarkFilter`):

| Filter | Without a read rule | With one | |
|---|---|---|---|
| one ticket by its indexed number, the caller alone decides the rule | 14.65 µs | 14.81 µs | +1.1%, budget 5% |
| the same, the rule reads the row | 14.65 µs | 15.11 µs | +3.1%, budget 15% |
| a caller's own tickets, the first 10 of 1,000 | 351 µs, by `owner = "u7"` | 346 µs, by the rule alone | |
| a count of a caller's own tickets | 30.7 µs, by `owner = "u7"` | 27.4 µs, by the rule alone | |

One finding for the owner's eye: the store runs no `ANALYZE`, so
SQLite chooses between two indexes blind, in any filter and not only
here. Keeping statistics is a decision about every database and is not
taken in this record.

## The owner's ruling

**A. What does a class allow when it says nothing?** A class is defined
with no `access` block, or with a `read` rule and no `update` rule.

**Ruled on 2026-11-13: nobody.** In the owner's words, "a document with
no access block - nobody sees by default". It is what ADR-0010 says.

- A rule that is left out is FALSE. `ValidateClass` reports each rule a
  class leaves out as a note, a finding that refuses nothing, the first
  of its kind; and the refusal of item 4 names the missing rule and
  what to add. An agent that defines a class and forgets its rules is
  stopped at its first command and told why.
- Who still reaches such a document is ADR-0052: a content
  administrator, with a reason that the event keeps. The owner saw that
  the answer is safe and can leave a process stuck, and asked for that
  role with it.
- The answer he did not take was everybody, as the engine behaved
  until then: nothing that works stops working, and a class whose
  author forgot the rules is open to every caller without a sign.

The ruling has a price. Every class that people use needs its four
lines, and the samples, the README and the tests gain them. A call
without a principal, which is nobody (ADR-0041, item 6), sees and does
nothing, so a first program names its callers and writes rules.

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
