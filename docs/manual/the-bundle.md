# The bundle

For the developer of a page: FadoDB Workshop, FadoFace, or a page an
agent writes. Ola works in Polish and opens an invoice in her browser. As
she types the net amount, the total changes at once; a field she may
not set is greyed out; "Zatwierdź" is offered only when she may
approve. Her page asks the server none of it while she types. It asks
once, for the bundle: the class in Ola's words and its formulas
compiled. The browser VM, [`fadodb-vm`](../../web/vm/README.md) on
npm, works them out with the server's answers. When she saves, the
server decides, as it always does.

The invoice is the one of [the chapter on the class](class.md#sections),
with its [Polish words](class.md#words-in-other-languages). Jan files
it, Ewa approves invoices, and Marta and Ola are in finance.

## Asking for it

The page sends the command `get_bundle`:

<!-- the call -->
```jsonc
{"class": "Invoice", "language": "pl"}
```

| Field | What it is |
|---|---|
| `class` | the class, required |
| `language` | the person's language, a tag of BCP 47 in any case: `pl`, `pt-BR`; left out, the class's own words |
| `version` | an earlier version of the class that computed a document; the current one when left out |
| `tag` | the tag of the bundle the page holds |

It goes where every command goes: `POST /v1/get_bundle` to a server,
`Exec` or the call `GetBundle` in a Go program, `fadodb exec get_bundle` from a terminal, or
the tool `get_bundle` of the door for agents. Every caller may send
it, as every caller may read a class.

**A page that holds the bundle** sends its tag. When nothing changed,
the answer is that and nothing more:

<!-- unchanged -->
```jsonc
{"unchanged": true, "tag": "2eb799b2aacc9b91"}
```

**A browser keeps it in its cache.** `GET /v1/bundle?class=Invoice&language=pl`
answers the same bundle, with its tag as the `ETag` and `Cache-Control:
private, no-cache`. The browser sends the tag back in `If-None-Match`
and is answered `304 Not Modified`, and no body, until something
changes. The address takes `class`, `language` and `version`.

## What it answers

Ola's page is answered this. Each `<compiled>` is a formula as the
engine compiled it, its bytecode in base64, which `decode` of
`fadodb-vm` reads:

<!-- Ola's bundle -->
```jsonc
{
  "class": "Invoice",
  "version": 1,
  "bytecode": 4,
  "language": "pl",
  "languages": ["pl"],
  "words": {"class/Invoice/pl": 1},
  "tag": "2eb799b2aacc9b91",
  "definition": {
    "name": "Invoice",
    "label": "Faktura",
    "description": "Faktura dostawcy, od przyjęcia do zapłaty.",
    "variables": {
      "customer":     {"type": "text",  "label": "Dostawca",    "list": true},
      "number":       {"type": "text",  "label": "Numer",       "list": true, "required": true},
      "net":          {"type": "money", "label": "Kwota netto", "list": true},
      "vat":          {"type": "money", "label": "VAT"},
      "total":        {"type": "money", "label": "Razem",       "formula": "net + vat"},
      "has_delivery": {"type": "bool",  "label": "Dostarczono do nas"},
      "street":       {"type": "text",  "label": "Ulica"},
      "city":         {"type": "text",  "label": "Miasto"},
      "notes":        {"type": "text",  "label": "Uwagi"}
    },
    "sections": {
      "supplier": {"label": "Dostawca", "description": "Kto przysłał fakturę.",
                   "variables": ["customer", "number"]},
      "amounts":  {"label": "Kwoty", "description": "What the invoice asks us to pay.",
                   "variables": ["net", "vat", "total"]},
      "delivery": {"label": "Dostawa", "description": "Where the goods came to.",
                   "variables": ["has_delivery"],
                   "sections": {
                     "address": {"label": "Adres", "description": "Adres z dokumentu dostawy.",
                                 "variables": ["street", "city"]}
                   }}
    },
    "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"],
               "labels": {"draft": "Szkic", "review": "Do zatwierdzenia", "paid": "Zapłacona"}},
    "transitions": [
      {"name": "file",    "from": "draft",  "to": "review", "label": "Wyślij do zatwierdzenia"},
      {"name": "approve", "from": "review", "to": "paid",   "label": "Zatwierdź"}
    ],
    "access": {
      "read":   "CREATEDBY() = USER() OR INGROUP(\"finance\") OR INGROUP(\"approvers\")",
      "create": "INGROUP(\"everyone\")",
      "update": "TRUE",
      "delete": "FALSE",
      "transition": {"file": "CREATEDBY() = USER()", "approve": "INGROUP(\"approvers\")"}
    },
    "rights": {
      "review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}
    }
  },
  "schemas": {},
  "compiled": {
    "title": null,
    "defaults": [],
    "computed": [
      {"variable": "total", "type": "money(2)", "reads": ["net", "vat"], "formula": "<compiled>"}
    ],
    "access": {
      "create": "<compiled>", "update": "<compiled>", "delete": "<compiled>",
      "transition": {"approve": "<compiled>", "file": "<compiled>"}
    },
    "guards": {},
    "rights": {
      "review": {
        "net":   {"write": {"groups": ["finance"], "cell": "review / amounts"}},
        "total": {"write": {"groups": ["finance"], "cell": "review / amounts"}},
        "vat":   {"write": {"formula": "<compiled>", "cell": "review / vat"}}
      }
    },
    "schemas": {}
  }
}
```

| Member | What it is |
|---|---|
| `class`, `version` | the class and the version the bundle is of |
| `bytecode` | the format of the compiled formulas; a browser VM of another format refuses them |
| `language` | the language asked for, written as BCP 47 writes it; absent when none was |
| `languages` | every language the class has words in, so that a page offers a choice |
| `words` | the words put in place, each by its name and version |
| `tag` | what the page sends back to learn whether the bundle changed |
| `definition` | the class as it was defined, with the words of the language put in place |
| `schemas` | every named schema the class uses, the engine's own among them, with their words put in place |
| `compiled` | the formulas a page runs |

The bundle of the invoice is about 3 KB. It is the same for every
reader, and the server builds it once for each version and language
and keeps it until a class, a schema or words are defined again.

## Ola's words

**The words of her language are put in place** in the class: its
`label` reads "Kwota netto". A word Polish does not give stays the
class's own: Ola reads "VAT", and the description of Amounts in
English. The labels of the states go in the `labels` of the states
block. A column of a `rows` variable written in the short form,
`"qty": "int"`, is written in the long form where a word is put in
it. Words the class no longer uses are left out.

**The language is looked for** as a browser's tag falls back: `pl-PL`
finds the words in `pl`, `pt-BR` those in `pt`, and a language with
no words leaves the class's own. A tag is read in any case.

<!-- which words -->
| Asked | Words put in place | `language` |
|---|---|---|
| `pl` | `class/Invoice/pl` | `pl` |
| `pl-PL` | `class/Invoice/pl` | `pl-PL` |
| `PL` | `class/Invoice/pl` | `pl` |
| `de` | none: the class's own | `de` |
| nothing | none: the class's own | absent |

## The formulas

`compiled` holds every formula a page needs to show a document and
check it before it is sent:

| Member | The formulas |
|---|---|
| `title` | the title, or `null` when the title is the id |
| `defaults` | the defaults of the inputs, for a new document, in the order they are worked out |
| `computed` | the computed variables that work themselves out, in the order the engine works them out, each with its `type` |
| `access` | `create`, `update`, `delete`, and the rule of each transition |
| `guards` | the guard of each transition that has one |
| `rights` | for each state and variable, the rule that decides reading it and setting it, and the `cell` it is written in |
| `schemas` | the title and the subtitle of each schema's presentation |

Each formula of `title`, `defaults` and `computed` names its
`variable` and the variables it `reads`. When Ola changes `net`, the
page works out again what reads `net`, and what reads that, in order.

**What a formula is given.** A compiled formula names the values it
reads as the formula does: `net`, `shipping.city`. The page gives them
from the document as [the wire](commands.md) carries it. The rest is
the context:

| A function | The page gives it from |
|---|---|
| `USER()`, `INGROUP()`, `USERKIND()`, `ACTINGFOR()` | `whoami`: the user, the groups, the system groups, the kind, for whom the user acts |
| `INROLE()` | `whoami`'s `roles`, each written app and role with a dot between, `Expenses.approver`, in the context's `roles` ([the roles of an app](apps.md#the-roles-of-an-app)) |
| `STATE()`, `ID()`, `VERSION()` | the document |
| `CREATEDBY()`, `UPDATEDAT()`, `ENTEREDAT()` and the others of the document's row | the document's system variables |
| `FIRSTENTERED()`, `DAYSIN()` and the others of a state's history | nothing: they are the server's (below) |
| `NOW()`, `TODAY()` | the page's own clock |

**How the page works it out**, as the server does:

- **May Ola change a value?** The `update` rule holds, and the right
  of the variable in the document's state holds when it has one. A
  right is a list of groups, which holds when she is in one of them,
  or a formula.
- **May she take a transition?** Its rule holds, or `update` when it
  has none, and its guard holds.
- **May she create one, or delete it?** `create` and `delete`.

With the invoice in `review`, Ola's page greys out what the server
would refuse:

<!-- what Ola's page greys out -->
| Who | Sets | The rule the page reads | The page | The server |
|---|---|---|---|---|
| Ewa, an approver | `net` | `review / amounts`: the groups finance | greyed out | refused, `forbidden` |
| Marta, in finance | `net` | `review / amounts`: the groups finance | open | set |
| Marta, in finance | `vat` | `review / vat`: `FALSE` | greyed out | refused, `forbidden` |
| Ola, in finance | `street` | `update` alone | open | set |

**The page may be wrong, and the server is not.** A page that misreads
the bundle greys out the wrong field. The commit still refuses what
the rules refuse.

## What only the server works out

A formula that reads other documents or a table, with `LOOKUP` or an
aggregate, or that reads the history of the document's states, is the
server's: those are not on the page. Ann's quote, from the guide
[Look a value up](guides/look-a-value-up.md), reads its rate from a
table:

```json
{
  "name": "Rates",
  "columns": {"code": "text", "rate": "decimal(4)"},
  "key": ["code"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}
```

```json
{
  "name": "Quote",
  "title": "\"Quote in \" & currency",
  "variables": {
    "currency": {"type": "text", "required": true},
    "amount":   {"type": "decimal", "scale": 2, "required": true},
    "rate":     {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"},
    "in_pln":   {"formula": "ROUND(amount * rate, 2)"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  }
}
```

Its bundle marks the rate as the server's, in place of a formula:

<!-- the rate -->
```jsonc
{"variable": "rate", "type": "decimal(4)", "reads": ["currency"], "server": true}
```

The page shows the rate the server last gave. When Ann changes the
currency, which the rate reads, the page asks `preview` once and shows
its answer; `in_pln`, which reads the rate, it works out again itself.
A guard or a rule that is the server's is marked the same way, with
what it reads, and the page asks `transitions` for the buttons. The
guard of a transition that a value is brought before, by a variable
that lists `before:` it, is the server's too: the page holds the
value of an earlier version, and the move decides on the one it
brings.

## What it never carries

- **No document and no row of a table.** The bundle is the same for
  every reader, so it never holds a row that a table's rule hides from
  one of them. A page that offers the codes of `Rates` reads them with
  `table_rows`, under the table's own rule.
- **No rule, action, triggered variable or timer.** They are the
  server's, after a commit.
- **No `read` rule compiled.** The server never sends Ola a document
  she may not read. The rule stays, as written, in the definition.
- **Nothing of a screen.** Where a field goes and how it is drawn is
  the page's.

## The bundle follows the class

A document computed by an earlier version of the class takes the
current one when it is next saved, so the bundle of the current
version runs what the server will run. A document's `class_version`
says which version computed it: a page whose bundle is older asks for
it again. A document in a final state keeps the version it ended
under, and a page that shows it may ask for that `version`.

## What is refused

<!-- refused -->
| The page asks | Answer |
|---|---|
| the bundle of a class the database does not have | refused, `unknown_class` |
| the bundle in the language `polish` | refused, `bad_value` at `/language` |
| version 7 of a class at version 1 | refused, `unknown_class` |
| `POST /v1/bundle` | refused, `bad_json`, with `Allow: GET` |
| `GET /v1/bundle` without a class | refused, `bad_json` at `/class` |
