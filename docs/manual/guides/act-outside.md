# Do something outside when a document changes

When an invoice is approved, the finance team should be told. Telling
them is not a change to the invoice: it is something done outside the
database, by your program or by another system. A rule asks for it,
and the engine makes sure it is done once.

## The action and the class

An **action** is something a rule can ask for by name. It is defined
once, with the arguments it takes:

```json
{
  "name": "team.notify",
  "label": "Notify the team",
  "params": {
    "channel": {"type": "text", "required": true},
    "text":    {"type": "text", "required": true}
  }
}
```

The class asks for it in a rule:

```json
{
  "name": "Invoice",
  "variables": {
    "number": {"type": "text", "required": true}
  },
  "states": {"initial": "draft", "list": ["draft", "approved"]},
  "transitions": [
    {"name": "approve", "from": "draft", "to": "approved"}
  ],
  "rules": [
    {"on": "enter", "state": "approved",
     "do": [{"action": "team.notify", "args": {"channel": "\"#finance\"", "text": "\"Approved: \" & number"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  }
}
```

What the action does is a function of your program, given to the
engine when the database is opened:

```go
notify := func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
	return nil, post(call.Args["channel"], call.Args["text"])
}
db, err := fado.Open(path, fado.Options{Actions: map[string]fado.ActionFunc{"team.notify": notify}})
```

## What it does

| What happens | The function is called |
|---|---|
| Ann approves invoice FV-17 | not yet: the approval is written, and the call is queued with it |
| the engine's worker runs, a moment later | once, with `channel` "#finance" and `text` "Approved: FV-17" |
| the worker runs again | not again |

The log of the invoice then has two events: the approval, and that
the action ran.

## How it works

- **The call is queued in the same write as the approval.** Either
  both are there or neither is. An approval is never written without
  its notification, and a notification never goes out for an approval
  that was not written.
- **It is made after the commit, exactly once**, as the person whose
  change asked for it.
- **If the function fails, the engine tries again**, a little later
  each time, and after several attempts marks the action as dead and
  keeps it for a database administrator to look at.
- **The arguments are formulas**, worked out from the document as the
  commit left it, and checked against the definition when the class is
  defined.

## Variations

| To | Do |
|---|---|
| call another system over HTTP with no code of your own | define the action with a `request`; see [Actions](../../actions.md) |
| write what the action returned back to the document | give the rule's action an `into`, and declare `returns` in the definition |
| try an action outside any document | `TryAction`, as a database administrator |

See also: [The class definition, Rules](../class.md#rules).
