# Let somebody act for a person

Ann approves expenses. When she is on leave, Bob approves for her.
Her assistant, an agent, files expenses in her name all year. In both
cases the engine must know that Ann agreed, and the log must say who
really acted.

## The class

```json
{
  "name": "Expense",
  "title": "\"Expense of \" & owner",
  "variables": {
    "owner":    {"type": "text", "default": "IF(ACTINGFOR() <> \"\", ACTINGFOR(), USER())", "index": true},
    "approver": {"type": "text", "required": true},
    "amount":   {"type": "money", "scale": 2, "required": true}
  },
  "states": {"initial": "filed", "list": ["filed", "approved"], "final": ["approved"]},
  "transitions": [
    {"name": "approve", "from": "filed", "to": "approved"}
  ],
  "access": {
    "read":   "owner = USER() OR owner = ACTINGFOR() OR approver = USER() OR approver = ACTINGFOR()",
    "create": "INGROUP(\"everyone\")",
    "update": "FALSE",
    "delete": "FALSE",
    "transition": {"approve": "(approver = USER() OR approver = ACTINGFOR()) AND CREATEDBY() <> USER()"}
  }
}
```

The class says where somebody who acts for another is accepted, with
`ACTINGFOR()`: the approver approves, or whoever acts for the
approver.

## What it does

A caller who acts for somebody says so on the call: "I am Bob, and I
act for Ann". The engine lets such a call through only while a
**substitution** says Ann agreed. A substitution is a document of the
class `Substitution`, which every database has.

Carol and Bob have each filed an expense that Ann approves.

| Who | Does | Answer |
|---|---|---|
| Bob, for Ann | approves Carol's expense | refused, `forbidden` at `/principal/for`: no substitution |
| Ann | creates a substitution: `by` Bob, `until` 15 December | allowed; it is `active` |
| Bob, for Ann | approves Carol's expense | allowed |
| Bob, for Ann | approves his own expense | refused, `forbidden` at `/access/transition/approve` |
| Ann | withdraws the substitution | allowed; it is `withdrawn` |
| Bob, for Ann | reads Carol's expense | refused, `forbidden` at `/principal/for` |

The log of Carol's expense says: approved by Bob, for Ann.

## How it works

- **`USER()` stays the one who acts.** Bob is Bob, whoever he acts
  for, and `ACTINGFOR()` is Ann. That is why Bob cannot approve his
  own expense in Ann's name: the class still sees that he created it.
- **Ann gives the substitution herself** and takes it back with the
  transition `withdraw`. When she cannot, a database administrator
  gives one for her, with a reason, and never to themselves.
- **It holds by its dates.** `from` is now when left out, and `until`
  may be left out for "until withdrawn". It can be kept to some
  classes with `classes`.
- **A group does not pass over.** Bob acting for Ann has Bob's groups.
- **Without a "for" nothing is asked.** A caller who acts for nobody
  needs no substitution.

## An assistant

An agent that files expenses for Ann is the same thing with other
values: Ann creates a substitution with `by` the agent's id and
`classes` set to `["Expense"]`, with no `until`. The agent then calls
as itself, for Ann: the expense it files has Ann as its `owner`, and
the log says an agent made it, for Ann.

## A program that keeps its own substitutions

A host program with its own list of who stands in for whom opens the
database with the option `HostAnswersFor`. The engine then writes down
the "for" it is told and asks for no substitution.

See also: [Four eyes](four-eyes.md).
