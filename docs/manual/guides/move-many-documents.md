# Move many documents at once

At the end of the month every approved invoice is booked. There are
three thousand of them, and a few cannot be booked because something
is missing. One call moves all that can move and reports the rest.

## The class

```json
{
  "name": "Invoice",
  "variables": {
    "cost_centre": {"type": "text"}
  },
  "states": {"initial": "approved", "list": ["approved", "booked"], "final": ["booked"]},
  "transitions": [
    {"name": "book", "from": "approved", "to": "booked", "guard": "NOT ISBLANK(cost_centre)"}
  ],
  "access": {
    "read":   "INGROUP(\"accounting\")",
    "create": "INGROUP(\"accounting\")",
    "update": "INGROUP(\"accounting\")",
    "delete": "FALSE"
  }
}
```

Nothing in the class is special. Moving many documents is a call, and
it works on any transition of any class.

## The call

```go
// Tried first: what would happen, and nothing written.
would, err := db.PreviewMany(ctx, "Invoice", "book", ids, nil, as)

// The call itself. The key makes it safe to send again.
moved, err := db.TransitionMany(ctx, "Invoice", "book", ids, nil, as, fado.Key("booking-2026-11"))
```

Both answer with one outcome for every id, in the order given: the
document as it now is, or the refusal with its code.

## What it does

Three invoices are approved. INV-417 has no cost centre.

| Call | INV-416 | INV-417 | INV-418 |
|---|---|---|---|
| the preview | would move | would be refused, `guard_false` | would move |
| the call | `booked` | refused, `guard_false`, still `approved` | `booked` |
| the same call sent again, with the same key | `booked` | refused, `guard_false` | `booked` |

After the cost centre of INV-417 is filled in, a new call with only
that id books it.

## How it works

- **Each document is still a commit of its own**: its guard, who may,
  its rules, its version and its event in the log, exactly as if it
  had been moved alone.
- **The ones that cannot move are reported and the others move.** No
  error from the call does not mean that all moved: read the outcomes.
- **It is one write to the disk**, which is why it is fast: about
  27,000 documents a second, against 12,000 moved one at a time.
- **One call takes up to 10,000 ids**; 1,000 is a good size. While a
  call runs, other changes wait for it, so leave a pause between calls
  when people are working.
- **A key makes the call safe to repeat.** A program that is not sure
  its call arrived sends it again and gets the first answer; nothing
  is moved twice.

See also: [the performance pages](../../perf/README.md).
