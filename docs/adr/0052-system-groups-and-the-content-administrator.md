# ADR-0052: System groups: who runs the database, who designs, who reads everything and who may act on any document

- Status: Accepted; amends ADR-0010 and ADR-0027 (the administrator's flag gives way to four system groups), ADR-0041 (who passes the rights on variables), ADR-0033 (who opens a blob by its hash), ADR-0047 (an event says which rule was lifted, and why), ADR-0049 (who passes the rules of a class; a rule left out), ADR-0051 (the three lists the host names become system groups; work nobody was given; a table's rule left out; a version is read back with who defined it)
- Date: 2026-11-13

## Context

One flag does everything today. A caller whose principal has `Admin`
set passes every rule of every class and every right on a variable
(ADR-0010, ADR-0027, ADR-0041), may define, operate the queue and run
SQL (ADR-0051), and opens any blob by its hash (ADR-0033). So whoever
runs a database reads every salary in it, and whoever may free a
document that is stuck may also rewrite the rule that stuck it.

ADR-0051 took three kinds of work out of that flag and gave each to
groups the host names. It left the flag itself whole.

On 2026-11-13 the owner asked that the roles be set out before any open
question of the phase is answered, "we have to be very careful about
security", and named them: a database administrator, a designer, users,
user groups with system groups among them ("administrators, designers
and all users"), and a content super admin, "one which can deal with
documents but cannot change system settings nor design". He said that a
class with no `access` block is seen by nobody, that this is safe and
will jam processes, and that a dedicated role should be able to act on
such a document.

A document can be stuck for two reasons, and they have different cures:

- **The class says nothing, or the wrong thing.** The cure is design.
  The rules asked of a document are those of its class as it is defined
  now, whatever version computed the document (ADR-0039, and ADR-0049
  as it is built), so a redefinition with the missing rule opens its
  documents at once.
- **The one person a document waits for is not there.** The cure is a
  substitute when the absence was planned (ADR-0053), and somebody who
  may act past the rules when it was not.

This record is about that somebody, and about keeping the other kinds
of work away from them. The owner read it with its questions on
2026-11-13, said that the answers recommended are his own, and accepted
it. His rulings are at the end.

## Decision

1. **Four system groups hold the work a class does not give, and a
   fifth names every caller.**

   | Role | System group | Its members may | By this group alone they may not |
   |---|---|---|---|
   | Database administrator | `administrators` | list the outbox, retry a dead action, drain the queue, preview and try an action, check the blobs; with the server of Phase 6, keep users, keys and settings | see or change a document, define anything |
   | Designer | `designers` | define and redefine classes, schemas, tables and actions; preview a draft on real documents; migrate | see or change a document past its rules, operate the queue |
   | Content administrator | `content-administrators` | see every document and act on it past the rules of its class, as item 4 says | define anything, operate the queue, run SQL |
   | Analyst | `analysts` | run `SQL`, which reads every row and every variable | change anything |
   | User | `everyone` | what the formulas of a class give them | |

   A caller may be in several of the first four, and is in none unless
   the host put it there. The names are the owner's to change.

2. **A system group has a fixed name, and the host gives it apart from
   its own groups.** With a caller's id the host states which system
   groups the caller is in, in a place of their own in the principal.
   A group of the host's directory that happens to be called
   `administrators` therefore gives nothing, and one of these five
   names among a caller's ordinary groups is refused with `bad_value`,
   with the fix in the hint. `INGROUP("designers")` answers from the
   system groups, so a class can speak of them as of any group. The
   engine itself puts every caller that has an id in `everyone`.

   The reason of ADR-0051, item 2, stands: who may rewrite the rules is
   not decided by a rule that they may rewrite. No formula, class or
   table gives a system group. With the server of Phase 6 the engine
   keeps its own users and group lists, and these five exist in every
   database from its first day; who keeps their members is that
   phase's record.

3. **The administrator's flag goes.** `Principal.Admin` is removed. A
   caller in all four groups may do what the flag allowed, with the
   differences of item 4. The setup code of a host names itself as a
   caller, a service, in the groups it needs.

4. **What a content administrator may do, and what it leaves behind.**

   - **It lifts who may, and nothing else.** The four rules of a class,
     the rules of its transitions, the rights on its variables
     (ADR-0041), the two rules of a table (ADR-0051) and the read of a
     blob by its hash alone (`OpenBlob`, ADR-0033) let a content
     administrator through. A guard, the statechart, a type, a required
     value and a terminal state hold for them as for everyone. ADR-0049
     says an administrator "passes every rule and never a guard", and
     that stays.
   - **The rule is asked first.** When the rule itself lets the caller
     through, the act is an ordinary act. It is an override only when
     the group alone lets them through.
   - **An override of a command needs a reason and is written down.**
     The command carries a text that says why. Without one it is
     refused with `reason_required`, a new code, and the refusal names
     the rule that would be lifted. The event of the commit records
     that rule, `/access/update` or `/access/transition/approve`, and
     the reason, and the `_events` view of the class shows both. Every
     override, by whom, of which rule and why, is then one statement
     over a view.
   - **Asking first says so.** `Preview` and `Transitions` answer that
     the act is open to this caller by override, and of which rule, so
     an agent knows before it acts that it will leave that mark.
   - **A read by override leaves no trace**, as no read does today
     (question G).

5. **A rule left out lets nobody through.** This is question A of
   ADR-0049, in the owner's words of 2026-11-13: "a document with no
   access block - nobody sees by default". It is also what ADR-0010
   says. A class with no `access` block holds documents that only an
   override reaches. The same holds for one rule left out, and for a
   rule a table leaves out. `ValidateClass` and `DefineClass` report
   each rule a class leaves out as a note that refuses nothing, and
   the refusal a caller gets names the missing rule and what to add. A
   class opens itself in one line: `"read": "INGROUP(\"everyone\")"`
   for every caller who has an id, or `"read": "TRUE"` for a call
   without a principal as well.

6. **Work nobody was given is done by nobody.** This is question C,
   raised while ADR-0051 was built. A database whose host puts nobody
   in `designers` has no one who may define, and a call without a
   principal is nobody (ADR-0041, item 6) and is refused. The three
   options of ADR-0051, `Designers`, `Operators` and `Analysts`, are
   not needed any more and go.

7. **What a designer does is in sight.** Item 5 of ADR-0051 is built
   with this record: every version of a class, a schema, a table and an
   action records who defined it, what they were and for whom they
   acted (ADR-0050), and when. `Class`, `Table` and `Action` return it
   with the definition, and one new call lists the versions of a class.
   A designer can still read through a class they write (ADR-0051,
   item 6) and can still rewrite a rule. The split of this record does
   not stop that; it makes it visible.

8. **What this record leaves out.**

   - Who is in a group. The host says so until Phase 6.
   - A content administrator for some classes only, a class that shuts
     the override out, a designer for one schema. Each narrows a group.
     None is needed to split the flag, and each can be added later
     without changing what is stored.
   - A record of reads (question G).
   - SQL under the rules. An analyst reads every document (ADR-0049,
     item 12) and is named with the care a content administrator is.
   - What the queue shows. An outbox row carries the arguments of its
     action as the commit computed them, so a database administrator
     sees the values that actions send, and no others.
   - The database itself. Whoever can copy a database, or change the
     host program, reads everything in it. These groups guard the
     calls. Encrypting a database is not decided here.

## The owner's rulings

All of 2026-11-13. Questions A and C came from ADR-0049 and ADR-0051.

**A. What a class allows when it leaves a rule out: nobody** (item 5).

**C. Work the host gave to nobody: nobody does it** (item 6).

**D. Is the administrator split? In four** (items 1 and 3). Who runs
the database does not read it, who designs does not act on documents
past their rules, and who frees a stuck document cannot change a rule.
The answer not taken was one administrator, as before, beside the three
lists of ADR-0051: one flag for a host's own code, no content
administrator as a role of its own, and no mark on an act past the
rules.

**E. Fixed system groups, or groups the host names? Fixed names** (item
2), which was the owner's suggestion. The same five words in every
database, class, sample and skill, and a class can say
`INGROUP("designers")`. The answer not taken was ADR-0051 as built,
`Options.Designers` and the like: a host maps its directory without
code of its own, and no class can speak of a role in words that hold in
another database.

**F. How far does a content administrator reach? Everything a rule
could allow anybody** (item 4): see, create, change values, delete and
every transition, each with a reason and a mark. The mark is the
control. The answer not taken was to see and to move only, which sends
a document that waits for a person who left to a designer, for a
transition that names somebody else.

**G. Is a read by override recorded? Not in this step.** A command by
override is recorded and a read is not, as no read is today. A content
administrator can open any document, and an analyst can read any
through SQL, and the log does not say which. A log of such reads, SQL
statements and reads by override together, gets a record of its own,
before the phase is called done or with the server, as the owner
chooses then.

## Consequences

- An agent that operates the queue reads no document. One that designs
  cannot approve a payment. One that frees stuck documents cannot
  change a rule, and each thing it did past a rule is in the log with
  its reason.
- Every program names its callers and their system groups, and the
  smallest program names one for its own setup. Every class says who
  may see, create, change and delete. The samples, the README and the
  tests gain both. This is the price of items 5 and 6, and a pull
  request of its own.
- A service that loads documents is given a rule by the class, such as
  `USERKIND() = "service"` with a group, or acts by override with a
  reason on each commit.
- `Admin` leaves the Go API and the three options of ADR-0051 leave
  `Open`. Both break every host, before 1.0.
- The event row gains the rule lifted and the reason, and the event
  schema under `api/` gains both. The engine's schema moves again. The
  command set gains a reason on every command, the code
  `reason_required`, and a call that lists the versions of a class.
- The five skills under `skills/` say which system group each hat
  needs.

## Performance

For a caller in no system group nothing changes: the test is of a short
list carried with the principal. A content administrator's command asks
the rule as anybody's does, where the flag skipped it, so it costs what
a user's costs.

The event row gains two values that are empty on an ordinary commit.
The benchmark gate allows no allocation more on a transition or an
outbox action (ADR-0030), and that is measured when this is built. If
it cannot be held, the two facts go to a row of their own, written only
for an override.
