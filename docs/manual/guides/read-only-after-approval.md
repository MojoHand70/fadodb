# Make a field read-only after approval

A sales offer is drafted, approved by a manager and sent. Once it is
approved its price and its customer must not change any more, while a
comment and the date it is valid until still may.

## The class

```json
{
  "name": "Offer",
  "title": "\"Offer for \" & customer",
  "variables": {
    "customer":    {"type": "text", "required": true},
    "price":       {"type": "money", "scale": 2, "required": true},
    "valid_until": {"type": "date"},
    "comment":     {"type": "text"}
  },
  "states": {"initial": "draft", "list": ["draft", "approved", "sent"], "final": ["sent"]},
  "transitions": [
    {"name": "approve", "from": "draft",    "to": "approved", "guard": "price > 0"},
    {"name": "reopen",  "from": "approved", "to": "draft"},
    {"name": "send",    "from": "approved", "to": "sent"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"sales\")",
    "delete": "FALSE",
    "transition": {"approve": "INGROUP(\"managers\")", "reopen": "INGROUP(\"managers\")", "send": "INGROUP(\"sales\")"}
  },
  "rights": {
    "approved": {"price": {"write": "FALSE"}, "customer": {"write": "FALSE"}}
  }
}
```

The one line of `rights` does it: while an offer is `approved`, nobody
writes `price` or `customer`.

## What it does

Sam is in the group `sales`, Mia in `managers`.

| State | Who | Does | Answer |
|---|---|---|---|
| `draft` | Sam | changes the price | allowed |
| `draft` | Mia | approves | allowed |
| `approved` | Sam | changes the price | refused, `forbidden`, variable `price` |
| `approved` | Sam | changes the comment | allowed |
| `approved` | Mia | reopens it | allowed, and Sam can change the price again |
| `sent` | Sam | changes the comment | refused, `terminal` |

## How it works

- **A right of `FALSE` is for everybody.** Nobody sets the variable in
  that state, whatever `access` allows them.
- **The other variables are untouched.** `comment` and `valid_until`
  have no line, so `update` alone decides, and sales may change them.
- **To change a locked value, move the document back.** `reopen`
  returns the offer to `draft`, where the price can be changed and the
  manager approves again. The log shows the whole round.
- **A final state locks everything by itself.** `sent` needs no line.

## Variations

| To | Write under `"approved"` |
|---|---|
| lock every variable but one | `"*": {"write": "FALSE"}, "comment": {"write": ["sales"]}` |
| let one group still change the price | `"price": {"write": ["managers"]}`, and let managers through `update` as well |
| hide the price from everybody but sales and managers | `"price": {"read": ["sales", "managers"]}` |

A variable a caller may not read is left out of the document they
get.

See also: [Give each state its own editors](editors-per-state.md).
