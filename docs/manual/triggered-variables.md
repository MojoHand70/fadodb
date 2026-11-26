# Triggered variables

A computed variable is worked out by its formula, and the engine works
it out again whenever a value it reads changes, as a spreadsheet does.
That is not always what a process wants. The exchange rate of an
invoice should be the rate of the moment accounting checked it, and
stay, though the table of rates moves on. Who checked it should be
whoever made that move, and stay.

A **triggered variable** is a computed variable that is worked out
only at the moments its class names, and keeps its value in between.
With the value it keeps the moment: which version of the document
worked it out, and when.

This chapter is the reference for triggered variables. A test defines
what stands here and plays it on each push.

## Two kinds of computed variable

<!-- two kinds -->
| | A computed variable | A triggered variable |
|---|---|---|
| Written with | `formula` | `formula`, `mode` and `on` |
| Worked out | whenever a value it reads changes | when one of its triggers fires |
| In between | it follows the values it reads | it keeps its value, whatever changes |
| Its formula may read | the values of its document, and a table | those, and the caller, the clock, the state, the life of the document, other documents and its own value |
| Says when it was worked out | no | yes |
| When working it out fails | the commit is refused | the commit goes on, the value stays, and the document says what failed |

The engine's word for the first kind is `auto`. A computed variable
that names no mode is one, and `"mode": "auto"` may be written.

## The syntax

```json
"checked_by": {"formula": "USER()", "mode": "triggered", "on": ["transition:check"]},
```

| Word | Meaning |
|---|---|
| `formula` | what the variable is worked out by, as for any computed variable |
| `mode` | `triggered` |
| `on` | the triggers: the moments at which it is worked out. At least one |
| `type` | the type of the value. It may be left out, and is then the formula's. It is needed when the formula reads other documents or the variable's own value, and for `min` and `max` to hold |

The triggers:

<!-- the triggers -->
| Trigger | Fires in | `STATE()` in the formula is |
|---|---|---|
| `enter:<state>` | a commit that brings the document into the state; for the first state that is the create | the state entered |
| `exit:<state>` | a commit that takes the document out of the state | the state left |
| `transition:<name>` | a commit that makes the transition | the state left |
| `before:<name>` | a command that asks for the transition, before its guard is asked; see [Before a transition](#before-a-transition) | the state left |
| `refresh` | a refresh of the document; see [Refreshing](#refreshing) | the state the document is in |
| `table:<name>` | a change of the rows of the table, for every document of the class that is not finished; see [Following a table](#following-a-table) | the state the document is in |

- **A variable is worked out once in a commit**, however many of its
  triggers the commit fires. A move from a state into the same state
  leaves it and enters it, and fires both.
- **A transition that time makes fires the triggers too.** `USER()`
  is then the caller whose commit began the wait.
- **There is no trigger for a change of values.** A rule does that;
  the class below has one.

## The class

The rates are a table that the treasury keeps:

```json
{
  "name": "Rates",
  "columns": {"code": "text", "rate": "decimal(4)"},
  "key": ["code"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}
```

An invoice in a foreign currency is entered, checked by accounting and
booked:

```json
{
  "name": "Invoice",
  "title": "\"Invoice \" & number",
  "variables": {
    "number":     {"type": "text", "required": true},
    "amount":     {"type": "decimal", "scale": 2, "required": true},
    "currency":   {"type": "text", "required": true},
    "rate":       {"type": "decimal", "scale": 4, "max": 10,
                   "formula": "LOOKUP(Rates, rate, code = THIS.currency)",
                   "mode": "triggered", "on": ["enter:checked", "refresh"]},
    "in_pln":     {"formula": "ROUND(amount * rate, 2)"},
    "checked_by": {"formula": "USER()", "mode": "triggered", "on": ["transition:check"]},
    "as_entered": {"formula": "amount", "mode": "triggered", "on": ["exit:entered"]}
  },
  "states": {"initial": "entered", "list": ["entered", "checked", "booked"], "final": ["booked"]},
  "transitions": [
    {"name": "check",  "from": "entered", "to": "checked"},
    {"name": "return", "from": "checked", "to": "entered"},
    {"name": "book",   "from": "checked", "to": "booked", "guard": "NOT ISBLANK(rate)"}
  ],
  "rules": [
    {"on": "values_change", "variables": ["currency"], "do": [{"action": "refresh", "variables": ["rate"]}]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "delete": "FALSE",
    "transition": {
      "check":  "INGROUP(\"ACCOUNTING\")",
      "return": "INGROUP(\"ACCOUNTING\")",
      "book":   "INGROUP(\"ACCOUNTING\")"
    }
  }
}
```

Three variables are triggered, one for each kind of moment.

| Variable | Is worked out | And holds |
|---|---|---|
| `rate` | when the invoice comes into `checked`, and on a refresh | the rate the table had at that moment |
| `checked_by` | when somebody makes the transition `check` | who that was |
| `as_entered` | when the invoice leaves `entered` | the amount it left with |

`in_pln` is an ordinary computed variable that reads `rate`.

## What it does

Ola enters invoices. Anna and Adam are in the group `ACCOUNTING`.
Tess is in the group `treasury`. Piotr is a colleague with no part in
it. The table has EUR at 4.3000 and USD at 3.9000. The invoice has the
id `I-1`. The story starts on 1 December 2026 at 09:00 UTC, and each
step comes ten minutes after the one before.

<!-- what it does -->
| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | Ola | enters the invoice FV/1 of 100.00 in EUR | allowed, version 1 |
| 2 | Anna | checks it | allowed, version 2 |
| 3 | Tess | changes the EUR rate to 4.4000 | allowed |
| 4 | Anna | corrects the amount to 120.00 | allowed, version 3 |
| 5 | Piotr | refreshes the invoice | allowed, version 4 |
| 6 | Anna | refreshes `checked_by` | refused, `not_allowed`, variable `checked_by` |
| 7 | Anna | sets `rate` to 4.5000 | refused, `read_only`, variable `rate` |
| 8 | Tess | mistypes the EUR rate: 44.0000 | allowed |
| 9 | Anna | refreshes the invoice | allowed, version 5 |
| 10 | Tess | puts the EUR rate right: 4.4500 | allowed |
| 11 | Anna | refreshes `rate` | allowed, version 6 |
| 12 | Anna | corrects the currency to USD | allowed, version 7 |
| 13 | Anna | returns the invoice | allowed, version 8 |
| 14 | Adam | checks it | allowed, version 9 |
| 15 | Adam | books it | allowed, version 10 |
| 16 | Tess | changes the USD rate to 4.0000 | allowed |
| 17 | Adam | refreshes the invoice | refused, `terminal` |
| 18 | Ola | reads the invoice | `rate` 3.9000, `in_pln` 468.00 |

What the invoice holds after each step that made a version:

<!-- what the invoice holds -->
| After step | `rate` | `in_pln` | `checked_by` | `as_entered` | `rate` was worked out |
|---|---|---|---|---|---|
| 1 | blank | 0.00 | blank | blank | never |
| 2 | 4.3000 | 430.00 | anna | 100.00 | at version 2 |
| 4 | 4.3000 | 516.00 | anna | 100.00 | at version 2 |
| 5 | 4.4000 | 528.00 | anna | 100.00 | at version 4 |
| 9 | 4.4000 | 528.00 | anna | 100.00 | at version 4, and failed since with `out_of_range` |
| 11 | 4.4500 | 534.00 | anna | 100.00 | at version 6 |
| 12 | 3.9000 | 468.00 | anna | 100.00 | at version 7 |
| 13 | 3.9000 | 468.00 | anna | 100.00 | at version 7 |
| 14 | 3.9000 | 468.00 | adam | 120.00 | at version 9 |
| 15 | 3.9000 | 468.00 | adam | 120.00 | at version 9 |

## How it works

- **It is worked out in the commit that fires its trigger**, after
  the document has moved and before the rules of the move run.
- **In between it keeps its value.** Tess changes the rate in step 3
  and Anna the amount in step 4, and the invoice keeps 4.3000.
- **A computed variable that reads it follows at once.** `in_pln` is
  516.00 in step 4, by the new amount and the rate that was kept, and
  528.00 in step 5, by the new rate.
- **A change of a value its formula reads does not work it out
  again.** `rate` reads `currency`, and a change of the currency alone
  would leave a rate for euros on an invoice in dollars. The rule of
  the class says what an ordinary computed variable does by itself:
  when the currency changes, refresh the rate. That is step 12.
- **Each time a trigger fires it is worked out again**, and what it
  held before is in the history. After step 14 `checked_by` is adam
  and `as_entered` is 120.00.
- **Nobody sets it.** A caller who tries is refused with `read_only`,
  as in step 7, and a rule that sets one is refused when the class is
  defined.
- **Several in one commit are worked out in the order the class lists
  them.** Each reads the ones above it as just worked out, and the
  ones below it as they were.
- **A preview shows what it would be**, and writes nothing.

> **The guard of a move does not see what the move works out.** A
> guard is asked before the document moves. Had `check` the guard
> `NOT ISBLANK(rate)`, it would see the blank from before and refuse
> every invoice. So the guard stands on the next move, `book`. A rule
> of the move does see the new value. One kind of variable a guard of
> the move does see: one that lists `before:` the move, which is
> worked out before the guard is asked; see
> [Before a transition](#before-a-transition).

## The moment

Beside its values a document carries, under `computed`, when each
triggered variable was worked out. After step 9 the invoice has:

<!-- the moments after step 9 -->
```jsonc
{
  "as_entered": {
    "version": 2,
    "at": 1796116200
  },
  "checked_by": {
    "version": 2,
    "at": 1796116200
  },
  "rate": {
    "version": 4,
    "at": 1796118000,
    "error": "out_of_range"
  }
}
```

<!-- the parts of a moment -->
| Part | Holds |
|---|---|
| `version` | the version of the document whose commit worked the value out |
| `at` | the time of that commit, in seconds since 1970, as `created_at` of a document is. 1796116200 is 1 December 2026, 09:10 UTC |
| `error` | the code of a failure since then. It is absent when the last working out went well |

- **A variable that was never worked out is blank and has no moment**,
  as all three are after step 1.
- **Working it out to the same value moves the moment.** In step 14
  the rate is 3.9000 again, and it is now the rate of version 9.
- **The history keeps the moments** as they were at each version.
- **A caller who may not read the variable** sees neither its value
  nor its moment.

In SQL each triggered variable has three columns beside its own:

<!-- the columns -->
| Column | Holds |
|---|---|
| `rate` | the value |
| `rate__asof` | the version at which it was worked out |
| `rate__at` | the time of that version, in seconds since 1970 |
| `rate__error` | the code of a failure since then, or null |

An analyst asks for the history of the rate around the mistake:

<!-- the statement -->
```sql
select version, rate, rate__asof, rate__error
from Invoice_history
where number = 'FV/1' and version between 4 and 6
order by version
```

<!-- its answer -->
| `version` | `rate` | `rate__asof` | `rate__error` |
|---|---|---|---|
| 4 | 44000 | 4 | null |
| 5 | 44000 | 4 | out_of_range |
| 6 | 44500 | 6 | null |

The rate is shown as it is stored, 4.4000 as 44000; see
[Views and SQL](views-and-sql.md#how-a-value-looks-in-sql). The
condition `rate__error is not null` over the view `Invoice` finds the
invoices whose rate could not be worked out the last time.

## When working it out fails

- **The commit is not refused.** In step 8 Tess mistypes the rate,
  and 44.0000 is past the `max` of the variable. The refresh of step 9
  is a version like any other: the invoice keeps 4.4000, the moment
  still names version 4, and `error` says `out_of_range`.
- **The next working out that goes well clears the error**, as in
  step 11.
- **A variable that has only ever failed** is blank, with an `error`
  and no `version`.
- **A lookup that finds nothing is no failure.** The value is blank,
  and the moment moves. That is why `book` has the guard
  `NOT ISBLANK(rate)`.

<!-- what fails -->
| Working it out fails when | Code |
|---|---|
| the formula fails, as a division by zero does | `div_by_zero` |
| the value is outside the `min` and `max` of the variable | `out_of_range` |
| the value is a text that is not among the `values` of the variable | `not_in_values` |

An ordinary computed variable that fails in one of these ways refuses
the commit.

> **A range needs the type.** On a computed variable that names no
> `type`, triggered or not, `min` and `max` are accepted and not
> applied. `rate` names its type for that reason.

## Before a transition

A guard reads its document and nothing outside it: a `LOOKUP` or a
`QUERY` written in a guard is refused when the class is defined, with
`not_allowed`. A decision stands on what the document holds, and what
a guard needs from a table or from other documents is brought onto
the document first. The trigger `before:<name>` does that: the
variable is worked out when somebody asks for the transition, and the
guard then reads it.

```jsonc
"limit": {"type": "decimal", "scale": 2,
          "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
          "mode": "triggered", "on": ["enter:review", "before:approve"]}
```

```jsonc
{"name": "approve", "from": "review", "to": "approved", "guard": "NOT ISBLANK(limit) AND amount <= limit"}
```

What a command that asks for a transition does, in order:

| | Step |
|---|---|
| 1 | the version the command saw is compared, when it names one |
| 2 | who may: the line of `access` for the transition, and the rights of the values the command sets |
| 3 | the values the command sets, and what is worked out from them |
| 4 | the variables that list `before:` this transition, in the order of the class, and what is worked out from each |
| 5 | the guard |
| 6 | the move; the variables that list `exit:`, `transition:` and `enter:`; the rules; the writes |

- **When the guard holds**, the value is kept in the version the move
  makes, and its moment names that version. So the log holds the
  number the guard decided on. It is not worked out again by an
  `enter:`, an `exit:` or a `transition:` of the same move.
- **When the guard does not hold**, nothing is written, and the
  refusal says under `brought` what each such variable was, to a
  caller who may read it. The document on the caller's screen may
  still show the value of an earlier version.
- **When the value cannot be brought**, because its formula fails or
  the value is not one the variable accepts, the transition is
  refused with that code and the variable. Another trigger would keep
  the old value and go on; a guard is not asked over a number that
  could not be worked out.
- **The listing of transitions and a preview bring it too**, and
  write nothing. A transition is offered, or not, by the number the
  move would see.
- **`STATE()` in its formula is the state the transition leaves.**
- **It names a transition that has a name.** A transition that fires
  by itself, by its guard or by its time, has nobody asking for it:
  nothing is brought before it, and its guard reads the document as it
  is stored. Bring what such a guard needs at the entry of the state,
  or by a refresh.

Two ways of using it, both right:

| The designer wants | Writes | The guard decides on |
|---|---|---|
| the limit of the day the purchase was filed | `"on": ["enter:review"]` | the number the approver saw |
| the limit of the moment of approval | `"on": ["enter:review", "before:approve"]` | a number brought in the same commit, and kept by it |
| that an invoice number is free | `"on": ["before:send"]`, a count over the class | a count nobody can have changed since: the commits come one after another |

The guide
[Bring what a condition needs](guides/bring-what-a-condition-needs.md)
plays the purchase from its first draft to its approval. A class that
was defined with a lookup in a guard before this was so still runs as
it was written; its next version brings the value first.

## Refreshing

A refresh works triggered variables out again. It is a commit: it
makes a version, and an event of the kind `refresh`.

- **It names variables, or none.** Each one named must list `refresh`
  among its triggers; step 6 is refused because `checked_by` does not.
  With none named, every variable that lists `refresh` is worked out.
- **Whoever may see a document may refresh it, unless the class says
  who may.** A refresh asks `access.read`, so Piotr, who may not
  change the invoice, refreshes it in step 5. A refresh sets no value:
  it brings the variables to what their formulas say now. A class
  that wants to say who may bring a value again, and in which state,
  gives the variable a `write` right; see
  [Say who may bring a value again](guides/who-may-bring-a-value-again.md).
- **Do not list `refresh` on a variable that says who did
  something.** Its `USER()` would be whoever refreshed. `checked_by`
  lists the transition alone.
- **It makes a version though nothing changed**, and though the class
  has no variable that lists `refresh`.
- **A rule may refresh.** Inside the commit, for its own document, as
  the rule of the class does. For another document the refresh runs
  after the commit, through the outbox; see
  [Rules and actions](notions/rules-and-actions.md), and the Purchase
  and Approval samples under
  [examples/classes](../../examples/classes/README.md), where each
  approval refreshes the purchase that counts them.
- **A finished document is not refreshed**: step 17 is refused with
  `terminal`.

In Go it is the call `Refresh`. As a command, the refresh of step 11:

<!-- command: refresh -->
```jsonc
{"class": "Invoice", "id": "I-1", "variables": ["rate"]}
```

## Following a table

A triggered variable keeps its value between its triggers. Sometimes
the wish is the other one: a value that is kept, so that it has a
history and costs nothing to read, and that still does not go stale
while its document is open. The trigger `table:<name>` does that.
When the rows of the table change, the engine works the variable out
again in every document of the class that is not finished, and keeps
the new value where it came out different.

Acme's estimates in a foreign currency follow the table of rates
while they are drafts. Once an estimate is sent its rate stands: the
customer has it in hand.

```json
{
  "name": "Estimate",
  "title": "\"Estimate \" & number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "amount":   {"type": "decimal", "scale": 2, "required": true},
    "currency": {"type": "text", "required": true},
    "rate":     {"type": "decimal", "scale": 4,
                 "formula": "LOOKUP(Rates, rate, code = THIS.currency)",
                 "mode": "triggered", "on": ["enter:draft", "table:Rates"]},
    "in_pln":   {"formula": "ROUND(amount * rate, 2)"}
  },
  "states": {"initial": "draft", "list": ["draft", "sent", "accepted"], "final": ["accepted"]},
  "transitions": [
    {"name": "send",   "from": "draft", "to": "sent"},
    {"name": "accept", "from": "sent",  "to": "accepted"}
  ],
  "rights": {
    "sent": {"rate": {"write": "FALSE"}}
  },
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"sales-lead\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE",
    "transition": {
      "send":   "CREATEDBY() = USER()",
      "accept": "INGROUP(\"sales-lead\")"
    }
  }
}
```

Ola writes estimates. Marek is in the group `sales-lead`. Tess, in
the treasury, keeps the table, and may not see an estimate. The table
has EUR at 4.2500 and USD at 3.9000. Ola has four estimates of 100.00
each. Then Tess changes the EUR rate to 4.3100.

<!-- what follows the table -->
| Estimate | In | Before Tess's change | After it |
|---|---|---|---|
| E-1, in EUR | `draft` | version 1, rate 4.2500, 425.00 in PLN | version 2, rate 4.3100, 431.00 in PLN |
| E-2, in EUR | `sent` | version 2, rate 4.2500, 425.00 in PLN | the same |
| E-3, in USD | `draft` | version 1, rate 3.9000, 390.00 in PLN | the same |
| E-4, in EUR | `accepted` | version 3, rate 4.2500, 425.00 in PLN | the same |

- **E-1 follows.** It gets a version of its own, and `in_pln` follows
  `rate` as after any change. The event of that version is of the
  kind `refresh`, its name is `table:Rates` and its actor is Tess: the
  history says which table moved the rate, and whose change it was.
- **E-2 stands, because the class says so.** A `write` right of
  `FALSE` on the variable in a state means nobody brings it again
  there, and a table is nobody too. Any other `write` right is about
  the people who refresh by hand, and does not stop a table.
- **E-3 is left alone.** Its rate came out the same, so it gets no
  version and no event.
- **E-4 is finished**, and a finished document is frozen.

What holds for it:

- **Nobody's right is asked.** Tess needs the right to change the
  table, which is the table's own `write` rule, and nothing more.
  What follows in the documents is what the class says, and the
  engine writes it, as it writes `in_pln`. Tess may not so much as
  read an estimate.
- **It is a commit like any other.** The rules of the class fire, a
  transition whose guard now holds fires, and a command that says it
  saw the version before is refused with `stale_version`: nobody acts
  on a number that changed under their eyes.
- **It is not instant.** The change of the table queues the work in
  its own transaction, as a row of the
  [outbox](the-outbox-and-the-worker.md) whose action is `follow`,
  and the worker does it afterwards: a thousand documents in a
  transaction, so that other people's commits go in between. A
  reader who looks in that moment may still see the old value. The
  moment of the value says which version brought it.
- **What changes a table**: `import_rows`, `delete_rows`, the actions
  `table.insert`, `table.update` and `table.delete` of a rule, and a
  new definition of a table that holds rows.
- **A value that cannot be worked out, or that the variable does not
  accept**, leaves the value as it was and says what failed, as on
  any trigger; see [When working it out fails](#when-working-it-out-fails).
- **The table is one the variable's own formula looks into.** A
  variable whose formula reads two tables may list both.

## What a triggered formula may read

A formula that is worked out at a known moment may read what holds at
that moment. The values of an offer that a list shows and a filter
asks for are kept this way:

```json
{
  "name": "Offer",
  "title": "\"Offer for \" & customer",
  "variables": {
    "customer":  {"type": "text", "required": true},
    "sent_on":   {"formula": "TODAY()", "mode": "triggered", "on": ["transition:send"]},
    "rounds":    {"type": "int", "formula": "rounds + 1", "mode": "triggered", "on": ["enter:review"]},
    "ahead":     {"type": "number",
                  "formula": "LOOKUP(Offer, COUNT(id), state = \"review\")",
                  "mode": "triggered", "on": ["enter:review"]},
    "in_review": {"formula": "DAYSIN(\"review\")", "mode": "triggered", "on": ["exit:review"]}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "accepted"], "final": ["accepted"]},
  "transitions": [
    {"name": "send",   "from": "draft",  "to": "review"},
    {"name": "return", "from": "review", "to": "draft"},
    {"name": "accept", "from": "review", "to": "accepted"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {
      "send":   "CREATEDBY() = USER()",
      "return": "INGROUP(\"sales-lead\")",
      "accept": "INGROUP(\"sales-lead\")"
    }
  }
}
```

Ola and Iza write offers. Marek is in the group `sales-lead`.

<!-- the offer -->
| Step | Day | Who | Does | `sent_on` | `rounds` | `ahead` | `in_review` |
|---|---|---|---|---|---|---|---|
| 1 | 1 December | Ola | writes an offer for ACME | blank | blank | blank | blank |
| 2 | 1 December | Ola | sends it | 2026-12-01 | 1 | 0 | blank |
| 3 | 4 December | Marek | returns it | 2026-12-01 | 1 | 0 | 3 |
| 4 | 5 December | Iza | writes an offer for Borg and sends it | 2026-12-01 | 1 | 0 | 3 |
| 5 | 5 December | Ola | sends hers again | 2026-12-05 | 2 | 1 | 3 |
| 6 | 8 December | Marek | accepts it | 2026-12-05 | 2 | 1 | 6 |

The columns are Ola's offer. `sent_on` is the day it was last sent,
`rounds` how many times it came into review, `ahead` how many other
offers were in review when it came in, and `in_review` the days it had
spent there whenever it left.

<!-- what its formula may read -->
| A triggered formula may read | As in | The same formula in a variable that is not triggered |
|---|---|---|
| the caller | `USER()` | `bad_formula` |
| the clock | `TODAY()` | `bad_formula` |
| the state of the document | `STATE()` | `bad_formula` |
| the life of the document | `DAYSIN("review")` | `bad_formula` |
| other documents | `LOOKUP(Offer, COUNT(id), state = "review")` | `bad_formula` |
| the value it holds | `rounds + 1` | `cycle` |

- **A formula that reads other documents names its `type`**, and so
  does one that reads its own value. The others may leave it out.
- **A formula that reads other documents sees every document**,
  whoever the caller is; see
  [Look a value up](guides/look-a-value-up.md).
- **It sees them as they were stored before the commit began.** When
  Ola's offer comes into review it is still stored as a draft, so
  `ahead` does not count the offer itself.
- **A filter can ask for these values**, where it cannot ask the
  function itself: Marek finds the offers that came back at least
  once with the filter `rounds >= 2`, and `ENTRIES("review") >= 2` is
  refused in a filter.

## A finished document

A document in a final state is frozen, and nothing in it is worked
out again.

- **The move into a final state still works out**: `enter:booked`
  would hold the rate of the booking.
- **A refresh is refused** with `terminal`.
- **A trigger that could fire only on a frozen document is refused
  when the class is defined**: the exit of a final state, a
  transition that leaves one, and the entry of a state that a final
  state leads to.

## Changing a class

- **A computed variable that a new version of the class makes
  triggered** keeps the value it has, and has no moment until one of
  its triggers fires.
- **A triggered variable that a new version adds** is blank on the
  documents that exist, until a trigger fires for each. A refresh of
  such a document fills it, when the variable lists `refresh`.

See [Change a class that already has
documents](guides/change-a-class.md).

## The commands

<!-- the commands -->
| To | Command |
|---|---|
| work triggered variables out again | `refresh` |
| read a document with its moments | `get` |
| see what a move would work out, and write nothing | `preview` |

What each takes and what it answers is in
[The commands](commands.md#documents).

## What is checked when the class is defined

<!-- checked when the class is defined -->
| A class is refused when | Code |
|---|---|
| an input has a `mode` or an `on` | `not_allowed` |
| a computed variable has an `on` and is not triggered | `not_allowed` |
| a triggered variable has no `on`, or an empty one | `missing` |
| the `mode` is neither `auto` nor `triggered` | `bad_value` |
| a trigger is none of the six, or is written wrongly, as `enter` with no state | `bad_trigger` |
| a trigger names a state the class does not have | `unknown_state` |
| a trigger names a transition the class does not have | `unknown_transition` |
| a trigger is `schedule:<when>` | `not_supported` |
| `table:<name>` names a table the variable's formula does not look into | `not_allowed` |
| `table:<name>` names a class, or stands on a list that is kept | `not_supported` |
| a trigger could fire only on a frozen document | `not_allowed` |
| a triggered variable has a `default` or is `required` | `not_allowed` |
| a triggered formula reads other documents and names no `type` | `missing` |
| a triggered formula reads its own value and names no `type` | `cycle` |
| the `type` is not the type of the formula | `type_mismatch` |
| a rule refreshes a variable that is not triggered, or does not list `refresh` | `not_allowed` |
| a rule refreshes, and no variable of the class lists `refresh` | `not_allowed` |
| a rule sets a triggered variable | `not_allowed` |

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| refreshes a variable that does not list `refresh` | `not_allowed` | the variable |
| refreshes a variable that is not triggered | `not_allowed` | the variable |
| refreshes a variable the class does not have | `unknown_variable` | the variable |
| names a variable twice in a refresh | `duplicate` | the variable |
| sets a triggered variable | `read_only` | the variable |
| resets a triggered variable | `read_only` | the variable |
| refreshes a document in a final state | `terminal` | |
| refreshes a document they may not see | `unknown_document` | |

## Not here yet

- A trigger for a time, `schedule:<when>`: every night, every hour. A
  class that names it is refused with `not_supported`. Until then a
  program sends the refresh when the time comes.
- A value that follows the documents of another class, as `table:`
  follows a table. Today a rule of that class sends the refresh.
- A list that is kept does not follow a table.
- Working a variable out again for every document of a class in one
  call. A refresh is one call for each document; only the change of a
  table brings many.
