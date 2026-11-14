# Rules and actions

A rule says: when this happens to a document, do that. Nobody calls a
rule. The engine runs it as part of the commit that set it off, and
the caller cannot leave it out. This chapter says when a rule runs,
what its actions may do, and the difference between what happens
inside a commit and what happens after it.

## A rule

A rule has three parts.

| Part | Written as | Says |
|---|---|---|
| when | `on`, with `state`, `name` or `variables` | what sets the rule off: a document is created, a value changes, a state is entered or left, a transition is taken, a document is deleted |
| only if | `if` | a condition over the document; left out, the rule always runs |
| what | `do` | the actions to run, in order |

## Two kinds of action

This is the one thing to carry away from the chapter.

| | Inside the commit | After the commit |
|---|---|---|
| The actions | `set`, `reset`, `refresh`, `emit` | `create`, `transition`, the `table` actions, and every action that calls another system |
| What they touch | the document of the commit | another document, a table, the world outside |
| When they happen | before the document is written: they are part of the same version | they are queued in the same write, and the engine runs them afterwards |
| When one fails | the whole commit is refused, and nothing is written | the commit stands; the action is tried again, or kept as dead with the reason |

## The classes

A purchase is requested, checked by a controller and approved. When it
is approved an order is created. The order comes first, because the
purchase names it.

```json
{
  "name": "Order",
  "title": "\"Order for \" & item",
  "variables": {
    "purchase":   {"type": "text", "required": true},
    "item":       {"type": "text", "required": true},
    "amount":     {"type": "money", "scale": 2},
    "ordered_by": {"type": "text"}
  },
  "states": {"initial": "open", "list": ["open", "delivered"], "final": ["delivered"]},
  "transitions": [
    {"name": "deliver", "from": "open", "to": "delivered"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"managers\")",
    "update": "INGROUP(\"purchasing\")",
    "delete": "FALSE"
  }
}
```

```json
{
  "name": "Purchase",
  "title": "\"Purchase of \" & item",
  "variables": {
    "item":        {"type": "text", "required": true},
    "amount":      {"type": "money", "scale": 2, "required": true},
    "requester":   {"type": "text", "default": "USER()"},
    "approver":    {"type": "text", "required": true},
    "checked":     {"type": "bool", "default": "FALSE"},
    "approved_by": {"type": "text"},
    "approved_at": {"type": "datetime"}
  },
  "states": {"initial": "draft", "list": ["draft", "approved"], "final": ["approved"]},
  "transitions": [
    {"name": "approve", "from": "draft", "to": "approved", "guard": "checked"}
  ],
  "rules": [
    {"on": "values_change", "variables": ["amount"],
     "do": [{"action": "reset", "variables": ["checked"]}]},
    {"on": "transition", "name": "approve",
     "do": [{"action": "set", "values": {"approved_by": "USER()", "approved_at": "NOW()"}}]},
    {"on": "enter", "state": "approved", "if": "AMOUNT(amount) > 10000",
     "do": [{"action": "emit", "event": "purchase.large"}]},
    {"on": "enter", "state": "approved",
     "do": [{"action": "create", "class": "Order", "values": {"purchase": "ID()", "item": "item", "amount": "amount", "ordered_by": "USER()"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "requester = USER() OR INGROUP(\"controllers\")",
    "delete": "FALSE",
    "transition": {"approve": "approver = USER()"}
  },
  "rights": {
    "*": {
      "*":           {"write": "requester = USER()"},
      "checked":     {"write": ["controllers"]},
      "approved_by": {"write": "FALSE"},
      "approved_at": {"write": "FALSE"}
    }
  }
}
```

The four rules, read aloud:

1. When the amount changes, the check is taken back.
2. When the purchase is approved, write down who approved it and when.
3. When it becomes approved and is above 10 000, put an event of our
   own into the log.
4. When it becomes approved, create an order.

The first three act inside the commit. The fourth acts after it.

## What it does

Ola asks for a laptop. Iza is a controller, Marek a manager and the
approver Ola names.

| Step | Who | Does | Answer | What the rules did |
|---|---|---|---|---|
| 1 | Ola | creates a purchase of a laptop for 8000.00 PLN | allowed | nothing |
| 2 | Ola | ticks `checked` herself | refused, `forbidden`, variable `checked` | |
| 3 | Iza | ticks `checked` | allowed | nothing |
| 4 | Ola | changes the amount to 12000.00 PLN | allowed | rule 1: `checked` is FALSE again, in the same version |
| 5 | Marek | approves | refused, `guard_false` | |
| 6 | Iza | ticks `checked` again | allowed | nothing |
| 7 | Marek | approves | allowed | rule 2: `approved_by` is marek; rule 3: the event; rule 4: an order is queued |
| 8 | the engine | runs the queue | one action ran | the order exists, created as Marek |
| 9 | the engine | runs the queue again | nothing to run | |

The purchase went through five versions, one for each allowed step of
a person. The rules made none of their own.

## What the log keeps

<!-- the events -->
| Event | Name | From | To | By |
|---|---|---|---|---|
| `created` | | | `draft` | ola |
| `set` | | | | iza |
| `set` | | | | ola |
| `set` | | | | iza |
| `transition` | `approve` | `draft` | `approved` | marek |
| `custom` | `purchase.large` | | | marek |
| `action_run` | `create` | | | marek |

- The `set` of rule 2 and the `reset` of rule 1 have no event of their
  own: they are part of the commit that set them off.
- `emit` writes an event of the kind `custom` under the name the rule
  gives. A program or an agent that watches the log can wait for it.
- `action_run` says the queued action was carried out. It names the
  event that caused it, so every consequence leads back to its cause.

## When a rule runs

Each kind of commit sets off its rules in a fixed order.

<!-- the order of the triggers -->
| The commit | Sets off, in this order |
|---|---|
| a create | `create`, then `enter` of the first state |
| a change of values | `values_change` of each variable that changed |
| a transition | `values_change` when the call carried values, `exit` of the state left, `transition` of its name, `enter` of the state entered |
| a delete | `delete` |

Rules of the same kind run in the order the class lists them. A rule
with an `if` runs when the condition is TRUE. A condition that cannot
be worked out counts as FALSE, and the rule does not run.

A rule sees the document as the commit has made it so far: the values
of the call applied, the formulas worked out, the state changed, and
what the rules before it did. `USER()` is the caller of the commit and
`NOW()` its time.

## Inside the commit

- **What a rule writes is the engine's, not the caller's.** The
  `rights` of the class are not asked. Ola may not tick `checked`, and
  her change of the amount still takes the check back. Nobody may write
  `approved_by` by hand, and rule 2 writes it. This is how a class
  keeps a value that only the process may set.
- **What a rule writes is checked like any value**: its type, its
  range, its list of values, `required`.
- **Rules do not set each other off.** A value that a rule sets does
  not run a `values_change` rule. So a commit runs each rule at most
  once, and which rules a call sets off can be read from the class.
- **An action that fails refuses the commit.** If rule 2 could not
  work out a value, the approval would not happen. The refusal carries
  the code of the failure and the place of the action in the class,
  such as `/rules/1/do/0`.

## After the commit

- **The action is queued in the same write as the commit.** Either the
  approval and the queued order are both there, or neither is.
- **Its values are those of the moment that caused it.** The order
  gets the amount the purchase had when it was approved.
- **The engine runs it afterwards, once.** A worker of the engine takes
  the queue. Steps 8 and 9 show it by hand: the second run finds
  nothing to do.
- **It runs as the person whose commit queued it.** The order is
  created as Marek, with his groups, under the rules of the class
  `Order`. A rule never does what that person may not do.

That last point decides what happens when Ola names Piotr as approver.
Piotr is not in the group `managers`, and only managers create orders.

<!-- an action that is refused -->
| What happens | Answer |
|---|---|
| Piotr approves the purchase | allowed: he is the approver |
| the engine runs the queue | the order is refused, `forbidden`; no order exists |
| the queue afterwards | the action is kept as `dead`, with the code and who it ran as |
| the log of the purchase | `action_failed`, `create`, with the code `forbidden` |

The approval stands, and the failure is on record where an
administrator finds it. An action ends in one of three ways:

| Outcome | When | Then |
|---|---|---|
| it ran | the action was carried out | the event `action_run` |
| no answer | another system did not reply, or said try again | tried again, a little later each time, ten times in all by default |
| a refusal | the engine or the other system said no | `dead` at once, with the event `action_failed` and the code |

A dead action is never tried again by itself and never removed. A
caller in the system group `administrators` lists the queue and may
send a dead action again.

What an action does to another document may set off rules there, and
those may queue actions in turn. The engine follows such a chain
sixteen deep and then stops it, with a record of where.

Actions that call another system, yours or over HTTP, are in the guide
[Do something outside when a document
changes](../guides/act-outside.md) and in [Actions](../../actions.md).

## Looking before

A preview of a call lists the rules it would set off, in order, what
each would write, and what would be queued. Nothing is written and
nothing is queued. For Marek's approval it lists rules 2, 3 and 4, the
last one marked as queued.

## A finished document

A document in a final state is frozen, and a rule cannot change it. So
a `set`, a `reset` or a `refresh` is refused when the class is defined
if it could only run on a document that is already finished: on leaving
a final state, or on entering a state that a final one leads to.
`emit` and the actions after the commit may run there.

## What is checked when the class is defined

<!-- checked when the class is defined -->
| The rule | Code |
|---|---|
| names a state the class does not have | `unknown_state` |
| names a transition the class does not have | `unknown_transition` |
| sets a variable the class does not have | `unknown_variable` |
| sets a computed variable | `not_allowed` |
| sets a value of another type than the variable | `type_mismatch` |
| is on `values_change` and names no variables | `missing` |
| has an `if` that is not a condition | `not_a_condition` |
| names an action the database does not have | `unknown_action` |
| creates a document of a class the database does not have | `unknown_class` |
| changes a document that is already finished | `not_allowed` |

A class without states stores no documents, so rules in it are refused
too.

Next: [Who may](who-may.md).
