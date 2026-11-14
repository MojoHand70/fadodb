# Give each state its own editors

A memo is written by its author, then reviewed. While it is being
written only the author changes it. While it is in review nobody
changes the text, and reviewers add their remarks.

## The class

```json
{
  "name": "Memo",
  "title": "subject",
  "variables": {
    "subject": {"type": "text", "required": true},
    "body":    {"type": "text"},
    "remarks": {"type": "text"}
  },
  "states": {"initial": "writing", "list": ["writing", "review", "published"], "final": ["published"]},
  "transitions": [
    {"name": "send",    "from": "writing", "to": "review"},
    {"name": "return",  "from": "review",  "to": "writing"},
    {"name": "publish", "from": "review",  "to": "published"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE",
    "transition": {"send": "CREATEDBY() = USER()", "return": "INGROUP(\"reviewers\")", "publish": "INGROUP(\"reviewers\")"}
  },
  "rights": {
    "writing": {"*": {"write": "CREATEDBY() = USER()"}},
    "review":  {"*": {"write": "FALSE"}, "remarks": {"write": ["reviewers"]}}
  }
}
```

The editors are in the `rights` block, one line per state:

- in `writing`, every variable (`*`) is written by whoever created the
  memo;
- in `review`, no variable is written by anybody, except `remarks`,
  which the group `reviewers` writes.

## What it does

Ann writes a memo. Rita is in the group `reviewers`.

| State | Who | Does | Answer |
|---|---|---|---|
| `writing` | Rita | changes the body | refused, `forbidden`, variable `body` |
| `writing` | Ann | changes the body | allowed |
| `writing` | Rita | sends it to review | refused, `forbidden` at `/access/transition/send` |
| `writing` | Ann | sends it to review | allowed |
| `review` | Ann | changes the body | refused, `forbidden`, variable `body` |
| `review` | Rita | changes the body | refused, `forbidden`, variable `body` |
| `review` | Rita | writes a remark | allowed |
| `review` | Rita | returns it | allowed, and Ann can change the body again |

## How it works

- **`rights` narrows `access`.** The `update` line lets everybody
  change a memo; `rights` then says who may write which variable in
  which state. A caller needs both.
- **`*` is every variable**, and a variable named beside it wins. That
  is how `remarks` gets its own editors in `review`.
- **A state with no line in `rights` has no extra limit.** `published`
  needs none: it is final, so the memo is frozen there.
- **Who moves the memo is separate.** Editing is `rights`; taking a
  transition is its line under `access.transition`.

## Other ways to say who

A right is a formula or a list of groups. Some that are often needed:

| The editor is | Written as |
|---|---|
| a group | `["reviewers"]` |
| any of several groups | `["reviewers", "editors"]` |
| the one who created the document | `"CREATEDBY() = USER()"` |
| a person named on the document, in a variable `owner` | `"owner = USER()"` |
| a group named on the document, in a variable `team` | `"INGROUP(team)"` |
| nobody | `"FALSE"` |

Several named people on one document cannot be written yet: the
function that asks whether the caller is in a list kept on the
document is not built. Until it is, a set of editors is a group.

See also: [The class definition, Rights](../class.md#rights), and
[Make a field read-only after approval](read-only-after-approval.md).
