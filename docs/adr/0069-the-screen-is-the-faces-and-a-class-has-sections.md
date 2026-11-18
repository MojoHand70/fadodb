# ADR-0069: The screen is the face's, and a class has sections

- Status: Accepted; amends ADR-0018 (the engine keeps no layouts, and the bundle carries none), ADR-0028 (a section stands where a variable stands in `rights`), ADR-0032 (the four components for objects are no longer the engine's), ADR-0034 (the `rating` component is no longer the engine's), ADR-0068 (a section has a label, a description and words in other languages); withdraws ADR-0054
- Date: 2026-11-17

## Context

ADR-0018 gave a class any number of named layouts: JSON documents the
engine stores beside the class, which bind components to variables and
carry the formulas `visible`, `enabled`, `editable` and `style`,
compiled with the class and run in the browser VM. ADR-0032 and
ADR-0034 named components for them (`chip`, `card`, `coins`, `list`,
`rating`), and ADR-0054, still Proposed, two switches for the phone and
the desk. None of it was built. No layout has ever been stored.

All of it was decided in planning, before two directions of the owner.
On 2026-11-13: every face is built by Claude or another agent, and
every face is different; FadoFace is a product of its own, and a kit
of plain pages ships with the engine. On 2026-11-17, at step 3 of
Phase 7: the engine does not enter the race of components, "a never
ending race"; and then, asking who is responsible for the screen, he
answered it himself: "You want a headless document on the data side
and layout is imposed by who writes the face, nothing to do with
engine."

What a layout was to hold falls into three kinds.

- **What the document says to people**: the caption of a field, its
  help, the columns of The List, how a number is shown. ADR-0068 put
  them in the class.
- **Conditions**: when a part of the document matters, and who may see
  and set it. A condition written as a formula is a computed variable
  of the class, `has_delivery`, which the engine and the browser VM work
  out alike. Who may see and set is the rights table (ADR-0028), which
  protects a value, where a layout's `visible` never did.
- **Arrangement and look**: where a field goes, in what order, drawn as
  what, on which screen. That is the face's.

One thing of the first kind had no home: which variables belong
together. An invoice has a supplier, amounts and a delivery. A form
draws them as parts, an agent reads them as the outline of the
document, and a designer gives rights to a part at once instead of
to each variable in it. In the owner's words: "if ever it is a human
to define rights on a hundred of single variables that will kill us."

The case is Acme's invoice. Jan files it, Ewa approves it, Marta is in
finance, and Ola works in Polish.

## Decision

1. **The engine has no screen.** A class has no layouts. The engine
   stores none, checks none and serves none. A face, FadoFace, the kit
   of Phase 7 or a page an agent writes, decides what goes where, drawn
   as what and on which screen, from the class and the commands. A face
   that keeps screens of its own keeps them as documents of a class it
   defines, as it keeps the views of The List (ADR-0068, item 4).

2. **What a face reads.** The class, with what it says to people
   (ADR-0068) and its sections (item 3); the words of the person's
   language; the rights table; and the class's formulas, compiled, so
   that a page recalculates a document and greys out a field without
   asking the server (the bundle, the record that follows this one). A
   condition a face needs and the class does not have is a computed
   variable added to the class, where every face and every agent reads
   the same answer.

3. **Sections: the outline of a class.** A class may declare
   `sections`. A section has a `label`, a `description`, the
   `variables` it holds, and `sections` inside it. The outline is
   written once, in one place, and reads as it nests.

   ```json
   {
     "name": "Invoice",
     "label": "Invoice",
     "description": "A supplier's invoice, from receipt to payment.",
     "variables": {
       "customer":     {"type": "text",  "label": "Supplier",   "list": true},
       "number":       {"type": "text",  "label": "Number",     "list": true, "required": true},
       "net":          {"type": "money", "label": "Net amount", "list": true},
       "vat":          {"type": "money", "label": "VAT"},
       "total":        {"type": "money", "label": "Total",      "formula": "net + vat"},
       "has_delivery": {"type": "bool",  "label": "Delivered to us"},
       "street":       {"type": "text",  "label": "Street"},
       "city":         {"type": "text",  "label": "City"},
       "notes":        {"type": "text",  "label": "Notes"}
     },
     "sections": {
       "supplier": {"label": "Supplier", "description": "Who sent the invoice.",
                    "variables": ["customer", "number"]},
       "amounts":  {"label": "Amounts",  "description": "What the invoice asks us to pay.",
                    "variables": ["net", "vat", "total"]},
       "delivery": {"label": "Delivery", "description": "Where the goods came to.",
                    "variables": ["has_delivery"],
                    "sections": {
                      "address": {"label": "Address", "description": "The address on the delivery note.",
                                  "variables": ["street", "city"]}
                    }}
     },
     "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
     "transitions": [
       {"name": "file",    "from": "draft",  "to": "review", "label": "Send for approval"},
       {"name": "approve", "from": "review", "to": "paid",   "label": "Approve"}
     ],
     "access": {
       "read":   "CREATEDBY() = USER() OR INGROUP(\"finance\") OR INGROUP(\"approvers\")",
       "create": "INGROUP(\"everyone\")",
       "update": "TRUE",
       "transition": {"file": "CREATEDBY() = USER()", "approve": "INGROUP(\"approvers\")"}
     },
     "rights": {
       "review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}
     }
   }
   ```

   - **One section per variable.** A variable listed twice, in one
     section or in two, is refused with `duplicate` and the place of
     the second.
   - **A variable in no section** belongs to the document as a whole,
     as `notes` does.
   - **The order is the written order**: of the sections, and of the
     variables in a section. A section's own variables come before the
     sections inside it, as a chapter's text comes before its
     subchapters. The order of `variables` is unchanged and still
     decides the order The List offers its columns in (ADR-0068).
   - **Names.** A section is named as a variable is, an identifier
     compared without regard to case. The name is unique in the whole
     outline however deep, and is not the name of a variable, because
     `rights` and the words name a section by its name alone. A clash
     is refused with `duplicate`.
   - **Depth.** Sections nest at most four deep, the top level
     counted as the first, as objects nest four deep below a variable
     (ADR-0031). A fifth level is refused with `bad_value`.
   - **A variable the class does not have** is refused with
     `unknown_variable` and its place in the outline. A version of the
     class that drops a variable takes it out of its section in the same
     definition: the price of an outline kept in one place.
   - **An empty section**, with neither variables nor sections, is
     refused with `missing`.
   - A column of a `rows` variable, a field of an object and a column
     of an explicit table belong to their variable and are not listed;
     a `rows` or an object variable is listed whole.
   - **A section has no condition.** Whether it is shown, folded or
     left out is the face's. A face that hides Delivery when nothing was
     delivered reads `has_delivery`.

4. **Rights by section** (amends ADR-0028). In `rights`, the name of a
   section may stand where the name of a variable stands. The read rule
   and the write rule of a variable in a state are each the first one
   declared, in this order:

   1. the state's cell for the variable;
   2. the state's cell for its section, then for each section around
      that one, outwards;
   3. the state's cell `*`;
   4. the same three in the state `*`.

   **The most specific name wins**: a variable may narrow the rule of
   its section, or widen it. With the invoice above, in review:

   | Who | Sets | Decided by | Answer |
   |---|---|---|---|
   | Ewa, an approver | `net` | `review` / `amounts`: finance only | refused, `forbidden` |
   | Marta, in finance | `net` | `review` / `amounts`: finance only | set |
   | Marta, in finance | `vat` | `review` / `vat`: `FALSE` | refused, `forbidden` |
   | Marta, in finance | `street` | no cell: the class rules alone | set |

   A refusal names the variable, as today, and its hint names the cell
   that decided, `review / amounts`, so that a designer and an agent
   see which line to change.

5. **A missing description is a note** for a section, code
   `no_description`, as for a variable (ADR-0068, item 3).

6. **Words in other languages** (amends ADR-0068, item 6). A definition
   of words gains `sections`, each named by its name alone:

   ```json
   {
     "class": "Invoice",
     "language": "pl",
     "sections": {
       "supplier": {"label": "Dostawca"},
       "amounts":  {"label": "Kwoty"},
       "delivery": {"label": "Dostawa"},
       "address":  {"label": "Adres", "description": "Adres z dokumentu dostawy."}
     }
   }
   ```

   A name the class has no section for is refused with `bad_value`, as
   an unknown path is.

7. **A section changes no document.** Sections are part of the class
   definition: changing them makes a new version of the class
   (ADR-0039) and touches no document, and nothing of them is stored
   with a document. A section is not a value. No formula, filter,
   export or view reads it; `get_class` answers it as it was written,
   and the bundle carries it.

8. **What leaves the engine.**
   - ADR-0018: the paragraph on layouts. The bundle carries the class,
     its words and its compiled formulas, and no layout; its tag
     follows the class and the words.
   - ADR-0028: the sentence "The layout's `visible` and `editable`
     formulas remain advisory on top." A face greys out a field from
     the rights the bundle carries.
   - ADR-0032, item 3: `chip`, `card`, `coins` and `list` are not the
     engine's. The kit draws objects that way, and another face as it
     likes. Items 1, 2, 4 and 5 stand: the vocabulary, and the
     presentation block that says how an object is titled, are what an
     object says to people, as a label is.
   - ADR-0034: `min` and `max` stand; the `rating` component is not
     the engine's.
   - ADR-0054 is withdrawn. Its need, that a phone shows less than a
     desk, is the face's, and FadoFace and the kit meet it.

9. **What this record leaves out.**
   - The bundle: what it carries and the command that serves it, the
     record that follows this one.
   - The form the kit draws from a class: sections as headings, the
     labels, the formats and the rights greyed out (Phase 7, step 5).
   - A variable in two sections, "at least for now" in the owner's
     words; sections of the columns of a table; a section as a column
     of The List.

## The owner's rulings

All of 2026-11-17, put in the chat on one page with the invoice above.

1. **The screen is the face's, and the engine keeps no layouts** (items
   1, 2 and 8). The other way was ADR-0018 as written: layouts stored
   and checked by the engine, with a shared description of a form.
2. **Grouping in the class**, his own addition: "What I would add, is
   some kind of variables grouping." A section carries a label, a
   description, words and rights; one section per variable, "at least
   for now"; sections inside sections left to me ("maybe agents will
   want to create groups in groups"); and no condition: "Aren't we
   again entering face jurisdiction? ... No, I don't think so."
3. **The word is `sections`.** The other way was `groups`, his first
   word, which already names groups of people: `INGROUP`, the system
   groups, and the lists of groups in `rights`.
4. **A section lists its variables** (item 3), in his words: "Outline
   shows sections visually." The other way was each variable naming its
   section, which keeps one section per variable without a check and
   leaves nothing behind when a variable goes.
5. **The most specific rule wins** (item 4). The other way was a
   section's rule binding all its variables, with a variable able only
   to narrow it.

Settled by me, his to veto: sections nested in the outline itself, a
section's variables before the sections inside it, four levels, names
unique across the outline and apart from the variables', an empty
section refused, the hint naming the cell that decided.

## Settled while building

Items 3, 4, 5, 6 and 7 are built, item 6 with the words of ADR-0068;
the bundle of item 2 comes with the record that follows. Settled
by me while building, each the owner's to strike:

1. **A fifth level is refused with `too_deep`**, not `bad_value` as
   item 3 says: `too_deep` is the engine's code for anything nested past
   its limit, objects (ADR-0031) and the templates of an action, and an
   agent that knows it for one knows it for all.
2. **The hint of a refused write** reads `the write right of net, from
   the cell review / amounts: the groups finance`: the variable, the
   cell that decided, and the rule written in it.
3. **An override is recorded as ADR-0052 has it**, with the document's
   state and the variable, `/rights/review/net/write`, and not with the
   cell that decided. The record says what was set past a rule; the hint
   says which line to change. Two variables of one section set past it
   are two places, as they are without sections.
4. **A section's name** is checked as an identifier and against the
   keywords of the formula language, and not against the names of the
   system columns, `id` and `state` among them, which a section never
   meets.
5. **A variable refused for its own fault**, a type the engine does not
   know, is not refused again by the section that lists it.
6. **A name in `rights` that is neither a variable nor a section** is
   refused with `unknown_variable`, as an unknown variable was, and the
   message says it looked for both.
7. **The samples.** The invoice and the purchase under
   `examples/classes`, the expense of the chapter on the class and the
   request of the designer's skill have sections. The receipt and the
   approval, with two and three variables, have none.

## Consequences

- The engine's part ends at the document. A face needs the class, its
  words, the bundle and the commands, and nothing of a screen is kept
  where every face must follow it.
- An agent that builds a class writes its outline with it, and one
  that fills a document reads the outline as the parts of the document.
  A designer gives the rights of a part in one line.
- The engine no longer refuses a class change that would break a
  screen, because it knows no screen. A face learns of a new version of
  the class from the bundle; the kit draws its form from the class and
  follows it by itself. The rights still protect every value on every
  path, whatever a face draws.
- No description of a form is shared among faces. Each face draws its
  own, as the owner's direction of 2026-11-13 has it.
- Step 3 of Phase 7 becomes sections and the bundle. The test of the
  phase changes with it: the agent told the story builds the table and
  the class, and the people of the story use them through the kit.
  ROADMAP, `docs/architecture.md`, the chapter on the class and the
  designer's skill follow in the pull request that builds sections.
- The class format grows by one block. Every class written before stays
  valid: `sections` is optional.

## Performance

No budget changes. The rule of each variable in each state, through
its sections, is worked out once when the class is defined, so a write
checked at commit and a value hidden by the wire encoder cost what they
cost today (ADR-0028). Sections are read when a class is defined and
when it is answered, never in a commit or a recalculation.
