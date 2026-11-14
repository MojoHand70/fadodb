# Who may

FadoDB keeps no users. The program that embeds it says, on every
call, who is calling. The class says what that caller may do, and the
engine asks the class each time, for a person, an agent and a service
alike. This chapter says what a caller is, which questions a class
answers, how to give each state its own editors, and what the people
who run a database may do that a class does not give.

## The caller

| What the host program says | Example | What it is for |
|---|---|---|
| an id | `krzysztof` | `USER()` in a formula; the name in every event |
| its groups | `ACCOUNTING` | `INGROUP("ACCOUNTING")`, and the lists of groups under `rights` |
| what it is | a person, an agent or a service | `USERKIND()`; kept on every event |
| for whom it acts | `ann` | `ACTINGFOR()`; checked against a substitution; kept on every event |
| its system groups | `designers` | the work that is not about one document; see below |

The engine trusts the host program for who the caller is, and decides
everything else itself.

Every caller that has an id is in the group `everyone`. A call that
names nobody is nobody: it gets through only where a line of the class
says `TRUE`.

## The questions a class answers

| The question | Answered by | Asked |
|---|---|---|
| May this caller see the document? | `access.read` | on every call that names the document, and for every document of a filter or a count |
| May they create one? | `access.create` | over the new document, with its defaults |
| May they change its values? | `access.update` | over the document as it is stored, before the change |
| Which values? | `rights`, by state and by variable | for each value the call sets |
| May they delete it? | `access.delete` | over the document as it is stored |
| May they take this transition? | its line under `access.transition` | over the document as it is stored; a transition with no line asks `update` |
| Which values may they see? | `rights`, the `read` right | for each value the answer carries |

Each answer is a formula. It reads the caller, with `USER()`,
`INGROUP()` and `USERKIND()`, and the document, with its variables,
`STATE()` and `CREATEDBY()`. So "the approver named on this invoice"
and "anybody in accounting while it is in accounting" are both one
line.

**A line left out lets nobody through.** A class says who may, or
nobody may. The engine notes each line a class leaves out when the
class is defined.

## The class

An invoice again, with a few people more. Ola registers it, Krzysztof
approves it, accounting books it. The accounting team has an agent,
`ledger-bot`, that prepares invoices for booking.

```json
{
  "name": "Invoice",
  "title": "\"Invoice \" & number",
  "variables": {
    "number":     {"type": "text", "required": true},
    "supplier":   {"type": "text", "required": true},
    "net":        {"type": "money", "scale": 2},
    "approver":   {"type": "text", "required": true},
    "account":    {"type": "text"},
    "audit_note": {"type": "text"}
  },
  "states": {"initial": "INAPPROVAL", "list": ["INAPPROVAL", "INACCOUNTING", "BOOKED"], "final": ["BOOKED"]},
  "transitions": [
    {"name": "approve", "from": "INAPPROVAL",   "to": "INACCOUNTING", "guard": "AMOUNT(net) > 0"},
    {"name": "book",    "from": "INACCOUNTING", "to": "BOOKED",       "guard": "NOT ISBLANK(account)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR approver = USER() OR INGROUP(\"ACCOUNTING\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() OR approver = USER() OR INGROUP(\"ACCOUNTING\")",
    "delete": "FALSE",
    "transition": {
      "approve": "approver = USER()",
      "book":    "INGROUP(\"ACCOUNTING\") AND USERKIND() = \"person\""
    }
  },
  "rights": {
    "INAPPROVAL":   {
      "*":          {"write": "CREATEDBY() = USER() OR approver = USER()"},
      "audit_note": {"write": ["ACCOUNTING"]}
    },
    "INACCOUNTING": {"*": {"write": ["ACCOUNTING"]}},
    "*":            {"audit_note": {"read": ["ACCOUNTING"]}}
  }
}
```

## What it does

Piotr is a colleague with no part in this invoice. Celina is a content
administrator.

| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | a call that names nobody | creates an invoice | refused, `forbidden` at `/access/create` |
| 2 | Ola | creates invoice FV/2026/11/021, with Krzysztof as approver | allowed |
| 3 | Piotr | reads it | refused, `unknown_document` |
| 4 | Anna | changes the net amount | refused, `forbidden`, variable `net` |
| 5 | Krzysztof | writes an audit note | refused, `forbidden`, variable `audit_note` |
| 6 | Ola | changes the net amount | allowed |
| 7 | Ola | approves | refused, `forbidden` at `/access/transition/approve` |
| 8 | Krzysztof | approves | allowed, state `INACCOUNTING` |
| 9 | Krzysztof | changes the net amount | refused, `forbidden`, variable `net` |
| 10 | ledger-bot | sets the account and writes an audit note | allowed |
| 11 | Krzysztof | reads the invoice | allowed; `audit_note` is not in what he gets |
| 12 | ledger-bot | books | refused, `forbidden` at `/access/transition/book` |
| 13 | Celina | corrects the supplier's name, giving no reason | refused, `reason_required` |
| 14 | Celina | corrects it, with the reason "the supplier was renamed, ticket 4711" | allowed |
| 15 | Anna | books | allowed, state `BOOKED` |
| 16 | Celina | changes the booked invoice, with a reason | refused, `terminal` |

## Who edits in which state

The question a designer asks most: who is the editor of the document
in this state? In FadoDB that is two things together. `update` says
who may change the document at all, and `rights` narrows it, state by
state and value by value. Read from the class above:

| State | Who may change values | Which values |
|---|---|---|
| `INAPPROVAL` | whoever created it, and its approver | all but `audit_note` |
| `INAPPROVAL` | anybody in `ACCOUNTING` | `audit_note` only |
| `INACCOUNTING` | anybody in `ACCOUNTING` | all |
| `BOOKED` | nobody | none: the state is final |

To give each state its own editors, write one line per state under
`rights`, with `*` for "every variable", as this class does. The guide
[Give each state its own editors](../guides/editors-per-state.md) has
the short form of it.

## How a right is found

`rights` is a table: a state, then a variable, then `write` or `read`.
`*` stands for every state, or every variable. For one variable in one
state, several places of the table could speak. The engine looks in
this order and takes the first place that says something about the
right it is asking for:

1. the state of the document, and the variable by name
2. the state of the document, and `*`
3. the state `*`, and the variable by name
4. the state `*`, and `*`

`write` and `read` are looked up separately. A place that gives only
`write` says nothing about `read`, and the search for `read` goes on.

**A line for a state wins over the line for `*`, even for a variable
that `*` names.** The class above has to say twice who writes
`audit_note`. Under `*` alone, place 3, the line of `INAPPROVAL` for
every variable, place 2, would be found first, and the approver could
write the audit note while the invoice is in approval. So when a state
has its own line with `*`, name in that state every variable that
somebody else must keep.

A value with no place in the table is governed by `access` alone.
`rights` only narrows: it never lets a caller change a document that
`update` does not let them change. The one case where `update` is not
asked is a transition with a line of its own, in
[States and transitions](states-and-transitions.md).

## What a caller who may not see gets

Piotr is told that there is no such document, in the same words as
for an id that was never used. He cannot tell an invoice he may not
see from one that does not exist. A filter and a count leave it out:
Piotr counts no invoices, Krzysztof counts one.

A value a caller may not see is left out of the document they get,
as in step 11. The document is otherwise whole.

## A step kept for a person

`ledger-bot` is in the group `ACCOUNTING`, so it may prepare the
invoice in step 10. Booking is kept for a person:
`USERKIND() = "person"` in the line of `book`. An agent works within
the same rules as a person, and a class can say where a person must
decide.

The kind is in the log too. The event of step 10 says an agent made
it.

## Acting for somebody

A caller may say "I am Bob, and I act for Ann". The engine lets such a
call through only while a substitution says Ann agreed, and the log
says both names. In the formulas `USER()` stays Bob, and
`ACTINGFOR()` is Ann, so a class says where somebody who acts for
another is accepted. Bob keeps his own groups. The guide
[Let somebody act for a person](../guides/acting-for-somebody.md)
plays it.

## The people who run the database

Some work is not about one document: defining a class, running the
queue, reading everything for a report. No class gives it. Four
system groups do, and the host program names their members apart from
its own groups.

| System group | Its members may | By this group alone they may not |
|---|---|---|
| `designers` | define classes, tables and actions; try a draft; migrate | see or change a document |
| `administrators` | list and run the queue of actions, and send a dead one again | see or change a document; define anything |
| `analysts` | run SQL, which reads every document and every value | change anything |
| `content-administrators` | see every document, and act on it past `access` and `rights`, with a reason | define anything; run the queue; run SQL |

- **Nobody is in a system group unless the host program says so.**
- **A group of your own with one of these names gives nothing.** A
  caller whose ordinary groups include `administrators` or `everyone`
  is refused with `bad_value`.
- **A designer does not see documents.** Who may rewrite the rules is
  kept apart from who may see what the rules protect.

## The content administrator

Sometimes a document must be corrected by somebody its class does not
name: the approver left, a supplier was renamed. That is the content
administrator.

- **It lifts who may, and nothing else.** `access` and `rights` let a
  content administrator through. A guard, a type, a required value and
  a final state hold for them as for everybody: step 16 is refused.
- **The class is asked first.** When the class itself lets them
  through, it is an ordinary act. It is an act past a rule only when
  the group alone lets them through.
- **An act past a rule needs a reason.** Without one it is refused
  with `reason_required`, as in step 13.
- **It is written down.** The event of step 14 names the lines Celina
  got past and keeps her reason:

<!-- the event of step 14 -->
| Event | By | Past | Reason |
|---|---|---|---|
| `set` | celina | `/access/read` `/access/update` `/rights/INACCOUNTING/supplier/write` | the supplier was renamed, ticket 4711 |

- **Asking first says so.** The list of open transitions tells a
  content administrator which of them they would take past a rule.
- **A read leaves no trace.** No read is logged today, a content
  administrator's included.

## What a rule does, and for whom

Two things from [Rules and actions](rules-and-actions.md) belong
here. What a rule writes inside a commit is the engine's write, and
`rights` is not asked. What a rule queues runs afterwards as the
person whose commit queued it, under the rules of the document it
touches.

## What is checked when the class is defined

<!-- checked when the class is defined -->
| The class | Code |
|---|---|
| has a line of `access` or a right that is not a condition | `not_a_condition` |
| reads a variable it does not have in a line of `access` | `bad_formula` |
| has a line under `access.transition` for a transition it does not have | `unknown_transition` |
| names a state under `rights` that it does not have | `unknown_state` |
| names a variable under `rights` that it does not have | `unknown_variable` |
| has a word under `access` or `rights` that the engine does not know | `bad_json` |

A line of `access` that is left out is not refused. The class is
defined, and the engine notes the line with `rule_left_out` and what
to add.

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| may not see the document | `unknown_document` | nothing more |
| may not create, change, delete or move it | `forbidden` | the line, such as `/access/update` |
| may not set a value | `forbidden` | the variable |
| acts for somebody with no substitution | `forbidden` | `/principal/for` |
| does work of a system group they are not in | `forbidden` | the group, such as `/system/designers` |
| acts past a rule and gives no reason | `reason_required` | the lines they would get past |
| has an ordinary group with the name of a system group | `bad_value` | the group |

## Not here yet

The engine's own users, groups and keys come with the server of
Phase 6. Until then the host program is the only one who knows who a
caller is.

Next: [History and the log](history-and-the-log.md).
