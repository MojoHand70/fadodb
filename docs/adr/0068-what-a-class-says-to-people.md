# ADR-0068: What a class says to people: labels, The List, formats, descriptions and other languages

- Status: Accepted; amends ADR-0013 (a variable gains `label`, `description`, `list` and `format`), ADR-0008 (a state and a transition gain a label and a description), ADR-0016 and ADR-0031 (a column of a table and a field of a schema may be written in a long form), ADR-0018 (the class header's `label` is the word for every caption)
- Date: 2026-11-17

## Context

A variable is named for the formulas: `net`, `due`, `lines`. Nothing
in a class says what a person reads in its place. A variable has a
type, rules and a formula; a state and a transition have a name and no
more; a column of a table or a field of a schema is a name and a type.
Only the class header (ADR-0018) and a table's header carry a `label`
and a `description`.

Phase 7 builds the pages that show documents to people, and the owner
named the two places where a variable's own caption is needed
(2026-11-17):

- **The List**, a table with a document on each row and some of its
  variables in the columns. The heading of a column must not be a
  technical name.
- **A screen builder**, where a designer places a variable on a form,
  with its caption or without.

He added that the words a person reads beside a field on a form are
often not the variable's caption at all, but a text written for that
form or the value of another variable. That belongs to the layout, the
record that follows this one.

People work in their own language. On 2026-11-13 the owner agreed that
a name is never translated, and that the words in other languages are
kept beside the definitions, not in them.

And the caller is an agent as often as a person, the north star of
the project: agents managing processes, some with no person in them. An agent that fills an invoice
decides what to put in `net` from the class alone. A description of
each variable is to an agent what a comment is to a programmer, and an
agent that builds a class should write them as a programmer writes
comments.

The case below is Acme's invoice, worked in Polish by Ola.

## Decision

1. **`label`: the caption of a variable, a state and a transition**, as
   the class header's `label` is the caption of the class. A name is
   what formulas, commands and the log use, and never changes for
   people; a label is what a person reads. A thing with no label is
   shown by its name.

   ```json
   {
     "name": "Invoice",
     "label": "Invoice",
     "description": "A supplier's invoice, from receipt to payment.",
     "variables": {
       "number":   {"type": "text", "required": true, "label": "Number", "list": true,
                    "description": "The supplier's own number, as printed on the invoice."},
       "customer": {"type": "text", "label": "Supplier", "list": true},
       "net":      {"type": "money", "scale": 2, "label": "Net amount", "list": true, "format": "#,##0.00",
                    "description": "The amount before VAT, as on the supplier's invoice."},
       "due":      {"type": "date", "label": "Due date", "list": true, "format": "d mmm yyyy"},
       "lines":    {"type": "rows", "label": "Lines", "schema": {
                      "sku":   {"type": "text",  "label": "Product code"},
                      "qty":   {"type": "int",   "label": "Quantity"},
                      "price": {"type": "money", "label": "Unit price", "format": "#,##0.00"}}}
     },
     "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"],
                "labels": {"draft": "Draft", "review": "In review", "paid": "Paid"},
                "descriptions": {"review": "Waiting for the approver named on the invoice."}},
     "transitions": [{"name": "approve", "from": "review", "to": "paid", "label": "Approve",
                      "description": "The approver accepts the invoice for payment."}]
   }
   ```

   A state's label and description are in the maps `labels` and
   `descriptions` of the states block, so that the list of states stays
   a list of names.

2. **The long form of a column and a field.** Wherever a column of a
   `rows` variable, a column of an explicit table or a field of a
   schema is written in the short form, `"qty": "int"`, it may also be
   written as an object with the type and the same three properties:
   `"qty": {"type": "int", "label": "Quantity", "description": "…",
   "format": "0"}`. The short form stays, and means the same as the long
   form with only a type. A table keeps its own `label` and
   `description` (ADR-0016).

3. **`description`, for people and for agents**, on the class, a
   variable, a state, a transition, a column and a field. It may be
   long, several paragraphs, and may use Markdown, which a page may
   render and an agent reads as text; it is held to the text limit of
   the database (ADR-0021). A page shows it as help; the door for
   agents passes it on with the class (ADR-0064).

   **A missing description is a note**, code `no_description`, for each
   variable, state and transition that has none, given by
   `validate_class`, `define_class` and `fadodb class check`. A note
   refuses nothing, like the note for a rule left out (ADR-0052): a
   person may define a class quickly, and an agent loops until no note
   is left. The designer's skill tells an agent to write a description
   for each of them, as a programmer writes a comment.

4. **`"list": true`: available to The List.** A variable that says so
   is a column The List of the class may show; the order the variables
   are written in is the order the columns are offered in. The List
   shows every such column at first. Which of them a person hides,
   shows and reorders, and the views they save by name, are the face's
   (a face may keep them as documents of a class of its own; the engine
   knows nothing of views). The system columns of a document, its state
   and who and when, are on every document the server sends, and a face
   gives them headings of its own. A variable that is not on The List
   is still in every answer that holds the document; `list` chooses
   columns and hides nothing.

5. **`format`: Excel's patterns.** A number, a decimal, a money, a date
   or a datetime may declare how it is shown, in the pattern language
   of Excel's number formats: `#,##0.00`, `0%`, `d mmm yyyy`,
   `yyyy-mm-dd hh:mm`. The person's language gives the separators and
   the names of the months: Ola reads `12 345,67` and `5 gru 2026`, and
   the server assumes no language, as for exports (2026-11-15). A format
   is for people only: no command, no export for a program and no
   formula sees it, and a value on the wire is as it always was. The
   engine checks a pattern when the class is defined, against the parts
   of Excel's language the record lists below, and refuses one it does
   not read with `bad_value` and the place of the pattern. A format on
   a text, a bool or a ref is refused.

   The parts: for numbers `0`, `#`, `,` and `.`, `%`, text in quotes,
   and two or three sections separated by `;` for positive, negative
   and zero; for days and moments `d`, `dd`, `ddd`, `dddd`, `m`, `mm`,
   `mmm`, `mmmm`, `yy`, `yyyy`, `h`, `hh`, `mm` and `ss` after an hour,
   `AM/PM`, and text in quotes.

6. **Other languages, beside the class.** The words of a class in
   another language are a definition of their own: the class, the
   language, and the label and the description of each thing that has
   one, named by its path. A path is a variable, `lines.qty` for a
   column, a state or a transition, in a section of its own:

   ```json
   {
     "class": "Invoice",
     "language": "pl",
     "label": "Faktura",
     "description": "Faktura dostawcy, od przyjęcia do zapłaty.",
     "variables": {
       "number":    {"label": "Numer"},
       "customer":  {"label": "Dostawca"},
       "net":       {"label": "Kwota netto", "description": "Kwota przed VAT, jak na fakturze dostawcy."},
       "due":       {"label": "Termin płatności"},
       "lines":     {"label": "Pozycje"},
       "lines.qty": {"label": "Ilość"}
     },
     "states":      {"draft": {"label": "Szkic"}, "review": {"label": "Do zatwierdzenia"}, "paid": {"label": "Zapłacona"}},
     "transitions": {"approve": {"label": "Zatwierdź"}}
   }
   ```

   - The language is a tag of BCP 47: `pl`, `de`, `pt-BR`.
   - The same for an explicit table and a schema, with `table` or
     `schema` in place of `class`.
   - Commands: `define_words`, `get_words`, `list_words` and
     `remove_words`, for designers, as a class is. Each definition has
     versions and who defined it, as every definition does
     (ADR-0052).
   - A path the class does not have is refused when the words are
     defined, with `bad_value` and the path. A later version of the
     class that drops the variable leaves its words in place, unused,
     and `list_words` notes them.
   - A word missing in a language is shown in the class's own.
   - **A translation never makes a new version of the class**, and
     touches no document. A page asks for the words of its person's
     language with the class (the record that follows this one).

7. **A label, a description, `list` and `format` are part of the class
   definition.** Changing them makes a new version of the class as any
   change does (ADR-0039), and moves no document: nothing of them is
   stored with a document. `get_class` answers them as they were
   written.

8. **What this record leaves out.**
   - The layout of a form, and the words a form shows in place of a
     label: the record that follows this one.
   - The views of The List a person saves: the face's.
   - An "app", a group of classes a person works in: the face's for
     now.
   - Plural forms, writing from right to left, and a format chosen by
     the person rather than the designer.

## The owner's rulings

All of 2026-11-17, put in the chat with the case above.

1. **The word is `label`**, the class header's, for every caption. The
   other way was `caption`, his word of 2026-11-13, which would have
   renamed the header's `label`.
2. **Other languages are definitions beside the class** (item 6). The
   other way was every language inside the class, where each new
   translation would be a new version of it.
3. **`"list": true` makes a variable available to The List** (item 4),
   in his words: "this variable is available for display in the list of
   class instances". The List shows them all at first; a person hides,
   shows, reorders and saves views, which a face keeps as documents of
   a class of its own, in the customer's database. He added that the
   columns of a `rows` variable and of an explicit table need a label
   too, which item 2 gives.
4. **`format` is Excel's patterns**, the separators and month names
   the person's language's (item 5).
5. **Descriptions, for people and for agents** (item 3), in his words:
   "descriptions might sometimes be a considerable chunk. Additionally,
   Agent, when building a class should fill descriptions, it's like a
   comment in programming." The note for a missing one was proposed
   with his answer, and taken as agreed with his "Yes, agreed".

## Consequences

- A face shows a class in a person's language with nothing but the
  class and its words: The List's headings, a form's captions, the
  buttons of the transitions, the states, and help beside each field.
- An agent reads what each variable is for before it fills it, and one
  that builds a class is told by notes where it left a comment out.
- The class format grows by four properties on a variable, two maps in
  the states block, two on a transition, and the long form of a column
  and a field. Every class written before stays valid: each is
  optional, and the short form stays.
- A designer translates without touching the class, and a class
  changed later keeps its words where its paths still hold.
- The format is checked once, when the class is defined; the page that
  draws a value applies it. The server and the wire are unchanged, so
  no export and no agent is told a value in a person's style.

## Performance

No budget changes. Labels, descriptions, `list` and `format` are read
when a class is defined and when it is answered, never in a commit or a
recalculation. A definition of words is read with its class, once per
language, and cached by the page with the class (the record that
follows).
