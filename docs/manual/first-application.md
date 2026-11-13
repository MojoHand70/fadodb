# Your first application

In this chapter you build a small application from nothing: an invoice
that one person approves and the accounting team books. It takes one
file of JSON and one command. At the end you will have seen every part
of a class at work, and the engine refusing what the class does not
allow.

You need Go 1.26 or later and a copy of this repository.

## The process

An incoming invoice goes through three states:

| State | What happens there | Who may edit the invoice |
|---|---|---|
| `INAPPROVAL` | the invoice is checked and approved | Krzysztof |
| `INACCOUNTING` | accounting gives it an account and books it | anybody in the group `ACCOUNTING` |
| `BOOKED` | nothing more: it is final | nobody |

Two moves connect them: `approve`, which only Krzysztof may make, and
`book`, which only accounting may make and only once the invoice has
an account.

## The class

In FadoDB a kind of document is a **class**, and a class is one JSON
document. This is the whole application,
[`examples/first/invoice.json`](../../examples/first/invoice.json):

```json
{
  "name": "Invoice",
  "title": "\"Invoice \" & number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "supplier": {"type": "text", "required": true},
    "net":      {"type": "money", "scale": 2},
    "vat_rate": {"type": "decimal", "scale": 2, "default": "0.23"},
    "gross":    {"formula": "ROUND(net * (1 + vat_rate), 2)"},
    "account":  {"type": "text"},
    "note":     {"type": "text"}
  },
  "states": {"initial": "INAPPROVAL", "list": ["INAPPROVAL", "INACCOUNTING", "BOOKED"], "final": ["BOOKED"]},
  "transitions": [
    {"name": "approve", "from": "INAPPROVAL",   "to": "INACCOUNTING", "guard": "gross > 0"},
    {"name": "book",    "from": "INACCOUNTING", "to": "BOOKED",       "guard": "NOT ISBLANK(account)"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE",
    "transition": {"approve": "USER() = \"krzysztof\"", "book": "INGROUP(\"ACCOUNTING\")"}
  },
  "rights": {
    "INAPPROVAL":   {"*": {"write": "USER() = \"krzysztof\""}},
    "INACCOUNTING": {"*": {"write": ["ACCOUNTING"]}}
  }
}
```

Read it block by block.

**`variables`** is what every invoice holds.

- `number`, `supplier`, `account` and `note` are texts. The first two
  are required: an invoice without them is refused.
- `net` is money, kept exactly to two places with its currency.
- `vat_rate` has a default, so a new invoice starts at 0.23.
- `gross` has a formula and no type of its own. Nobody sets it: the
  engine computes it whenever `net` or `vat_rate` changes.

**`title`** is a formula too. It gives each invoice a name people can
read, such as "Invoice FV/2026/11/017".

**`states`** lists the states, says which one a new invoice starts in
and which are final. A document in a final state is frozen.

**`transitions`** are the moves. Each has a name, the state it leaves
and the state it enters. A `guard` is a condition on the document that
must hold for the move: an invoice with no amount cannot be approved,
and one with no account cannot be booked.

**`access`** says who may do what with an invoice as a whole. Here
everybody who is known to the system may see, create and change
invoices, nobody deletes one, and each transition names who may take
it. A line left out would let nobody through.

**`rights`** narrows that, state by state: who may write the variables
while the invoice is in that state. `*` means every variable. A cell
is a formula, as in the first line, or a list of groups, as in the
second.

The words on the left of each colon in this file are FadoDB's, except
the names you chose: the names of the class, the variables, the states
and the transitions. `krzysztof` and `ACCOUNTING` are a user and a
group as your own program knows them. The [glossary](glossary.md) has
every word.

## Run it

From the root of the repository:

```
go run ./examples/first
```

The program is [`examples/first/main.go`](../../examples/first/main.go),
about a hundred lines. It creates a new database, defines the class,
and then plays a short story as two people: Krzysztof, and Anna, who is
in the group `ACCOUNTING`. This is what it prints:

<!-- output of examples/first -->
```text
1. krzysztof creates it:               Invoice FV/2026/11/017, state INAPPROVAL, gross 1230.00 PLN
2. anna writes a note:                 refused, forbidden, variable note
3. krzysztof changes the net:          allowed, gross 1476.00 PLN, version 2
4. anna approves:                      refused, forbidden at /access/transition/approve
5. krzysztof approves:                 allowed, state INACCOUNTING
6. krzysztof writes a note:            refused, forbidden, variable note
7. anna books it with no account:      refused, guard_false
8. anna sets the account and books:    allowed, state BOOKED
9. anna writes a note:                 refused, terminal

The log of the invoice:
  version 1  created    by krzysztof
  version 2  set        by krzysztof
  version 3  transition by krzysztof, approve: INAPPROVAL -> INACCOUNTING
  version 4  set        by anna
  version 5  transition by anna, book: INACCOUNTING -> BOOKED
```

## What happened, step by step

1. **Krzysztof creates the invoice** with a number, a supplier and a
   net amount. The engine fills in the default VAT rate, computes the
   gross and the title, and puts the invoice in its first state.
2. **Anna is refused.** The invoice is in `INAPPROVAL`, where the
   `rights` block lets only Krzysztof write. The refusal has a code,
   `forbidden`, and names the variable she tried to set.
3. **Krzysztof changes the net amount.** The gross follows by itself.
   The change is a new version of the invoice; the first one is kept.
4. **Anna may not approve.** The `access` block gives `approve` to
   Krzysztof, and the refusal names that line of the class.
5. **Krzysztof approves.** The guard holds, since the gross is above
   zero, and the invoice moves to `INACCOUNTING`.
6. **Now Krzysztof is refused.** The same person, the same variable,
   another state: in `INACCOUNTING` the editors are the accounting
   group.
7. **Anna cannot book yet.** She may take the transition, but its
   guard does not hold: the invoice has no account. The code is
   `guard_false`, not `forbidden`: this is about the document, not
   about her.
8. **Anna sets the account and books.** The invoice is `BOOKED`.
9. **Nobody changes a booked invoice.** `BOOKED` is final.

The log at the end is kept by the engine, with nothing to configure.
Every change is a version with its event: what happened, who did it,
and for a move from which state to which.

## Try a change

Open `examples/first/invoice.json`, change something, and run the
program again. Three to try:

- **Let accounting write notes during approval.** In `rights`, under
  `INAPPROVAL`, add a line for one variable:
  `"note": {"write": ["ACCOUNTING"]}`. A named variable wins over `*`,
  and step 2 becomes allowed.
- **Let anybody in a group approve.** Replace
  `"USER() = \"krzysztof\""` under `transition` with
  `"INGROUP(\"MANAGERS\")"`. Now the program stops at step 5: the
  engine refuses Krzysztof, who is in no group, and its message names
  the line of the class that refused him.
- **Make a mistake on purpose.** Misspell `gross` in the guard of
  `approve`. The program stops when it defines the class, and the
  engine names the place of the mistake, `/transitions/0/guard`.

## What you used

| Part | What it is for | More |
|---|---|---|
| a class | one kind of document, written once | [The class definition](class.md) |
| variables and formulas | what a document holds and what is computed | [The class definition](class.md#variables) |
| states and transitions | where a document is and how it moves | [The class definition](class.md#states) |
| guards | what must hold for a move | [The class definition](class.md#transitions) |
| access and rights | who may see, change and move a document | [The class definition](class.md#access) |
| versions and events | what happened, kept by the engine | [Glossary](glossary.md) |

The program calls the engine through its Go package, `fado`: `Open`,
`DefineClass`, `Create`, `Set`, `Transition` and `Events`. A server
that takes the same commands over the network comes with Phase 6.
