# ADR-0070: The bundle: a class, its words and its compiled formulas in one call

- Status: Accepted; amends ADR-0003 (the dependency graph is the order of the formulas and what each reads), ADR-0018 (the bundle's command, address, contents and tag), ADR-0028 (the rights travel worked out for each state and variable), ADR-0055 (item 8: the bundle is the first read with a `GET` form); amended by ADR-0076 (a live variable in the bundle)
- Date: 2026-11-18

## Context

Ola works in Polish and opens an invoice in her browser. As she types
the net amount, the total should change at once. A field she may not
set should be greyed out, and "Zatwierdź" offered only when she may
approve. None of it should need a call to the server at each key
press. The server still decides when she saves.

The browser VM of step 2 does that arithmetic with the server's
answers (ADR-0003, ADR-0029), and it is published as `fadodb-vm`. It
runs compiled formulas only, and only the engine compiles: the browser
never parses a formula. Something must hand a page the class and the
formulas compiled from it.

Without that, a page can still work today. It reads the class with
`get_class` and its words with `get_words`, asks `preview` at every
change and `transitions` for its buttons. That is a call to the server
at each key press, and the browser VM has nothing to run. ADR-0003
chose to work a document out in the page. ADR-0018 named the call that
makes it possible, the bundle, at `GET /v1/classes/{name}/bundle`.
That was before ADR-0055 gave every command one address, and before
ADR-0069 took layouts out of the engine. Its address and its contents
are this record's.

An agent does not need the bundle: it reads `get_class` and asks
`preview`. The bundle is for pages, the kit of step 5, FadoFace, and
any page an agent writes.

The case is Acme's invoice from the chapter on the class: Jan files
it, Ewa approves it, Marta is in finance, and Ola reads it in Polish.

## Decision

1. **One command, `get_bundle`**, which reads. It takes `class`, and
   may take `language`, `version` and `tag`:

   | Field | What it is |
   |---|---|
   | `class` | the class, required |
   | `language` | the person's language, a tag of BCP 47; left out, the class's own words |
   | `version` | an earlier version of the class; the current one when left out |
   | `tag` | the tag of a bundle the page holds; when it is still the bundle's, the answer is `{"unchanged": true, "tag": "…"}` and nothing more |

   Reading a definition is open to every caller (ADR-0051, item 4), and
   the bundle says nothing `get_class` does not, so it is open to every
   caller too. A class the database does not have is refused with
   `unknown_class`, a language that is not a tag with `bad_value` at
   `/language`.

2. **A `GET` form for the browser's cache** (ADR-0055, item 8):
   `GET /v1/bundle?class=Invoice&language=pl` answers the same bundle,
   with its tag as the `ETag`. A browser that sends the tag back in
   `If-None-Match` is answered `304 Not Modified` and no body.
   `Cache-Control: private, no-cache` lets the browser keep it and ask
   each time whether it changed. The bundle is behind the person's
   key or sign-in, so no shared cache keeps it.

3. **What Ola's page receives.** The whole bundle of the invoice:

   ```json
   {
     "class": "Invoice",
     "version": 1,
     "bytecode": 4,
     "language": "pl",
     "languages": ["pl"],
     "words": {"class/Invoice/pl": 1},
     "tag": "5d0c9a7e31b2f468",
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
         "amounts":  {"label": "Kwoty",    "description": "What the invoice asks us to pay.",
                      "variables": ["net", "vat", "total"]},
         "delivery": {"label": "Dostawa",  "description": "Where the goods came to.",
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
         {"variable": "total", "type": "money", "reads": ["net", "vat"], "formula": "<compiled>"}
       ],
       "access": {
         "create": "<compiled>", "update": "<compiled>", "delete": "<compiled>",
         "transition": {"file": "<compiled>", "approve": "<compiled>"}
       },
       "guards": {},
       "rights": {
         "review": {
           "net":   {"write": {"groups": ["finance"], "cell": "review / amounts"}},
           "vat":   {"write": {"formula": "<compiled>", "cell": "review / vat"}},
           "total": {"write": {"groups": ["finance"], "cell": "review / amounts"}}
         }
       },
       "schemas": {}
     }
   }
   ```

   Each `<compiled>` is one formula as the engine compiled it: its
   bytecode in base64, a few hundred letters, which `decode` of
   `fadodb-vm` reads.

   | Member | What it is |
   |---|---|
   | `class`, `version` | the class and the version the bundle is of |
   | `bytecode` | the format of the compiled formulas; a VM of another format refuses the bundle (ADR-0029) |
   | `language` | the language asked for, in its canonical form; absent when none was |
   | `languages` | every language the class has words in, so that a page offers a choice |
   | `words` | the words put in place, each by its name and version |
   | `tag` | what the page sends back to learn whether the bundle changed |
   | `definition` | the class as it was defined, with the words of the language put in place (item 4) |
   | `schemas` | every named schema the class uses, the engine's own among them, as defined, with their words put in place |
   | `compiled` | the formulas a page runs (item 5) |

4. **The words are put in place** (the owner's ruling 1). Where the
   language has a word for something, its `label` or its `description`
   stands in the class in place of the class's own. Where it has none,
   the class's own stays: Ola reads "VAT", and the description of
   Amounts in English. A page reads every caption from one place, and
   the class as written is still read with `get_class`.
   - A column of a `rows` variable written in the short form,
     `"qty": "int"`, is written in the long form where a word is put in
     it: `"qty": {"type": "int", "label": "Ilość"}`.
   - The labels and descriptions of states go in the `labels` and
     `descriptions` of the states block, as ADR-0068 writes them.
   - Words the current version no longer uses, noted `unused_words`,
     are left out.
   - **Which words.** The words of the class and of each schema are
     looked for each on its own, by the lookup of RFC 4647: `pt-BR`,
     then `pt`, then the class's own. The tag is read in any case,
     `pt-br` as `pt-BR`: a browser sends it either way, and only a
     definition of words must be written in the canonical case.

5. **What is compiled.** Every formula a page needs to show a document
   and check it before it is sent:

   | Member | The formulas |
   |---|---|
   | `title` | the title, or `null` when the title is the id |
   | `defaults` | the defaults of the inputs, in the order they are worked out |
   | `computed` | the computed variables that work themselves out, in the order the engine works them out (ADR-0015), each with its `type` |
   | `access` | `create`, `update`, `delete`, and the rule of each transition |
   | `guards` | the guard of each transition that has one |
   | `rights` | the rule of each variable in each state (item 6) |
   | `schemas` | the title and the subtitle of each schema's presentation (ADR-0032) |

   Each formula of `title`, `defaults` and `computed` names its
   `variable` and the variables it `reads`, which are ADR-0003's
   dependency graph: when Ola changes `net`, the page works out again
   what reads `net`, and what reads that, in order. A compiled formula
   names the values it reads as the formula does, `net`,
   `shipping.city`, `lines`, and the page gives them from the document
   as the wire carries it. The page gives the context from `whoami`
   (the person, her groups, her system groups, for whom she acts), the
   document (its id, its state, its system variables), and its own
   clock.

6. **The rights are worked out by the engine.** For each state of the
   class and each variable, `compiled.rights` carries the read rule and
   the write rule that decide, as `rights` looks for them (ADR-0028,
   ADR-0069, item 4): `{"groups": [...]}` for a list of groups, or
   `{"formula": "<compiled>"}`, and the `cell` it was written in. A
   variable and a state with no rule are left out, and the class rules
   alone decide them. A page never repeats the search for the most
   specific cell: it would be the same rule written again in every
   face.

7. **What the page cannot work out is the server's** (the owner's
   ruling 2). A formula that reads other documents or a table, with
   `LOOKUP` or an aggregate (ADR-0020, ADR-0046), or that reads the
   history of a document's states (ADR-0047, item 6), whose stays are
   not on the wire, carries `"server": true` in place of its formula:

   ```json
   {"variable": "rate", "type": "decimal(6)", "reads": ["currency"], "server": true}
   ```

   The page shows the value the server last gave. When Ola changes the
   currency, a variable `rate` reads, the page asks `preview` once and
   shows its answer; for a guard or the rule of a transition it asks
   `transitions`. What reads `rate`, `total_eur`, the page works out
   again from that answer. A rule of the rights that is the server's
   leaves the field as the server last said, and the commit's answer
   counts.

8. **What the bundle never carries.**
   - No document and no row of a table. A bundle that is the same for
     every reader cannot carry a row that a table's read rule hides
     from one of them.
   - No rule, action, triggered variable or timer (`after`, `at`):
     they are the server's, after a commit.
   - No `read` rule compiled: the server never sends Ola a document she
     may not read. It stays, as written, in the definition.
   - Nothing of a screen (ADR-0069).

9. **The same bundle for everyone.** The rights are formulas over the
   document, `CREATEDBY() = USER()`, so a bundle made for Ewa alone
   would still carry formulas, and would be one more thing to cache
   for each person. One bundle of a class in a language is built once.

10. **The tag** is the first sixteen hexadecimal digits of the SHA-256
    of the bundle as answered without it. It changes when, and only
    when, what the bundle says changes: a version of the class, the
    words, a schema, or an engine that compiles otherwise.

11. **The bundle follows the class.** A live document computed by an
    earlier version takes the current one with its next commit
    (ADR-0039), so the bundle of the current version runs what the
    server will run. A page that reads a document whose
    `class_version` is newer than its bundle's asks for the bundle
    again. A document in a final state is frozen and computed by the
    version it ended under; a page that shows it may ask for that
    `version`.

12. **The proof.** A test in CI hands the bundle of the chapter's
    invoice, in Ola's words, to `fadodb-vm` in Node. Ola types a set of
    values; at each one, every computed value, every right and every
    guard the page works out is compared with the server's `preview`
    for the same values and the same person.

13. **What this record leaves out.**
    - Several classes in one call. A page with an invoice and its
      supplier asks for two bundles, each cached.
    - The stays of a document on the wire, which would let a page work
      out the functions of a state itself.
    - The tables a class reads. A page that offers the codes of
      `Rates` reads them with `table_rows`, under the table's own read
      rule.
    - The kit's use of the bundle (step 5).

## The owner's rulings

Of 2026-11-18, put in the chat on one page with the invoice above.

1. **The words are put in place** in the class (item 4). The other way
   was the class as written with the words beside it, which every face
   would match by path, `lines.qty` into the columns of a `rows`
   variable among them.
2. **What reads other documents, a table or the history of states is
   the server's** (items 7 and 8): the bundle marks it, and the page
   asks `preview` or `transitions`. The other way was the rows of the
   small tables a class reads carried in the bundle, so that the page
   works out `rate` itself; the bundle would then depend on who reads
   it.

On 2026-11-17 he agreed to the command, its tag, the `GET` form for
the browser and the word "bundle" (items 1 and 2), before record B
was reopened and became ADR-0069. Settled by me, his to veto: the same
bundle for everyone (item 9), open to every caller as `get_class` is,
the current version with an earlier one on request (item 11), the
lookup of a language (item 4), the tag a hash of the content (item
10), the rights worked out by the engine (item 6), and the proof
(item 12).

## Settled while building

Every item is built. Settled by me while building, each the owner's to
strike:

1. **The answer is the bundle itself**, its members at the top, and not
   wrapped in a member `bundle` as most answers are, so that the
   command and `GET /v1/bundle` answer the same bytes. A page that
   holds it is answered `{"unchanged": true, "tag": …}`.
2. **A compiled access rule or guard is its bytecode alone**, a text;
   one that is the server's is `{"server": true, "reads": [...]}`. A
   right carries `formula` or `groups` beside its `cell`, or `server`
   and `reads`. A right of a list of groups holds when the person is in
   one of them; the system groups are not among them, as at the server.
3. **The `type` of a computed variable and of a default** is the type
   as the engine resolved it, `money(2)` for a `money` written without a
   scale, which `parseType` of the browser VM reads.
4. **The schemas** are those the class uses, and the schemas they
   extend and their object fields name, in the order the database
   lists them.
5. **A guard of a transition without a name** is left out: nobody takes
   such a transition by a command, and the words leave it out too.
6. **The tag** is the first sixteen hexadecimal digits of the SHA-256
   of the bundle without it, as item 10 says. The store keeps at most
   256 bundles built and starts again past that, so a caller asking in
   every tag there is costs time and not memory.
7. **An earlier version** is answered while the store keeps it
   compiled: while a document it computed is live. A version that
   computed no document is refused with `unknown_class`, its current
   version in the hint.
8. **In Go the call is `GetBundle`**, so that the command keeps the
   name ADR-0055 gives the command of a call, `get_bundle`, as the
   owner agreed it; the store's call is `Bundle`.
9. **The list of the door's tools** grows by the tool `get_bundle`, to
   about 55,000 characters, which the chapter on the door now says.
10. **The proof.** `make vmtest` runs `go run ./web/vm/bundle`, which
   plays two stories against the engine: the chapter's invoice in
   Ola's words, and the sample invoice, which has a title, defaults,
   guards and rights in every state. For each person and step it
   writes the document as that person reads it and the server's
   answers: the values a set works out (`preview`), a new document's
   defaults at two moments, and what each person may set, read, move
   and delete. `web/vm/test/bundle.mjs` works every answer out again
   from the bundle, the person and the document alone: 394 answers,
   each the server's. A page in a final state sets nothing, as the
   server refuses it.

## Consequences

- A page runs a document as the server does, from one call, and keeps
  it in the browser's cache. A face does not repeat the engine's rules:
  the order of the formulas, the cell that decides a right and the
  lookup of a language come worked out.
- The server still decides. A page that misreads the bundle greys out
  the wrong field, and the commit refuses what the rules refuse.
- Every caller may read the rights and the access rules of a class,
  the names of its groups among them, as `get_class` lets them today.
  A class that must hide its rules from the people who use it needs a
  rule on reading definitions, which no record has.
- The bundle is a command like any other, so it is also a tool of the
  door for agents (ADR-0064). The skills say it is for pages.
- The bundle is the engine's contract with every face. Its shape gets
  the promise of the bytecode at 1.0 (ADR-0029).
- The manual gains a part on the bundle, played by a test, in the pull
  request that builds it.

## Performance

No budget changes. A compiled formula is about 170 bytes in base64:
the invoice under `examples/classes` compiles to twelve formulas in
about 2 KB, and a class of fifty variables to some tens of KB, fetched
once for each version and language and kept by the browser. The engine
builds a bundle the first time it is asked for after a definition
changes and keeps it in memory, never in the database, so an answer
`unchanged` costs what a read of a class costs. The browser VM's budget
stands: 200 formulas worked out in under a millisecond (ADR-0003).
