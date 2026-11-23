---
name: fadodb-face
description: Build a face for a FadoDB server, the screens people work on, so that it is beautiful and looks like every other face of that server. Read the server's look before you draw anything. When the server has none, define a direction with the person, show it to them drawn, and store it once they approve; from then on it is the visual identity of that server and every face follows it. Use this skill whenever an agent builds, restyles or reviews a user interface over FadoDB, such as a web app, a list, a form, a dashboard or a designer's tool, or is asked for colours, typefaces, a theme, a design system or a brand for one.
metadata:
  hat: face builder
  status: written
---

# Build a face for a FadoDB server

**Status: written, 2026-11-21.** This page says how to build what
people see of a FadoDB server so that it is good to look at and to
work in, and so that two agents building two apps on one server end
with one family of screens. A test on each push defines the class this
page shows, sends its commands and compares the answers with the ones
below.

A face is an application of its own. It calls the server's commands
and holds nothing the server does not; the database carries no face,
as Postgres carries none. So a face may be built with any tools, and
the faces of two servers may look nothing alike. The faces of **one**
server look like one thing. What makes them so is the server's
**look**: its colours, its type, its spacing and its components,
written down once, approved by a person, and kept on the server where
every agent finds it.

The work has three steps, and their order is the point:

1. **Define a direction.** Before any screen.
2. **Verify it with the person, by eye.** A drawn page, never a
   description.
3. **Stay consistent.** Once a look is approved it is the identity of
   the server. Every face follows it, unless the person asks in so
   many words for something different.

## Read first

| Read | For |
|---|---|
| [`docs/manual/class.md`](../../docs/manual/class.md#what-a-class-says-to-people) | what a class already says to people: labels, descriptions, The List, formats, sections and the words in other languages. A face shows these and never writes its own |
| [`docs/manual/the-bundle.md`](../../docs/manual/the-bundle.md) | the class in the person's words with its formulas compiled, so that a page works a document out as the server does |
| [`docs/manual/apps.md`](../../docs/manual/apps.md) | an app: the name for the classes and tables that belong together, and what a face groups its screens by |
| [`docs/manual/serving-a-database.md`](../../docs/manual/serving-a-database.md) | the server, and how it hands out the pages of a face at its own address |
| [`docs/manual/signing-in.md`](../../docs/manual/signing-in.md) | how a person signs in: on the server's own page, never in the face |
| [`docs/manual/commands.md`](../../docs/manual/commands.md) | every command a face may send |
| [The DESIGN.md format](https://github.com/google-labs-code/design.md) | how a look is written: exact values an agent uses, and in words why each is so. The `design` of a look is one such file |

## The look of a server

A look is a document of the class `Look`. It is an ordinary class:
the engine does not bring it, and a server that has none works as
before. Until a starter gives it to a new database, define it once,
as it stands here, with `define_class`.

```json
{
  "name": "Look",
  "label": "Look",
  "plural": "Looks",
  "description": "How the faces of this server look: its visual identity. One document is one look, written as a DESIGN.md. A person approves it, and from then on every face is built from the approved look of its name that was approved last. Born: here. Owner: the group brand. List: the documents of this class in the state approved.",
  "title": "name",
  "variables": {
    "name":        {"type": "text", "required": true, "index": true, "label": "Name", "list": true,
                    "description": "Whose look it is: the organisation, or one brand of it."},
    "design":      {"type": "text", "required": true, "label": "Design",
                    "description": "The look itself, as a DESIGN.md: the exact colours, type, corners and spacing, and in words why each is so and where it is used."},
    "direction":   {"type": "text", "label": "Direction",
                    "description": "What the person asked for, and what they said when they saw it drawn. Kept so that the next change starts from their words."},
    "tone":        {"type": "text", "label": "Tone",
                    "description": "How a face speaks to people: formal or plain, \"you\" or \"we\"."},
    "approved_by": {"formula": "USER()", "mode": "triggered", "on": ["transition:approve"], "label": "Approved by", "list": true,
                    "description": "The person who saw the look and said yes."},
    "approved_on": {"formula": "NOW()", "mode": "triggered", "on": ["transition:approve"], "label": "Approved on", "list": true,
                    "description": "When they said yes. Of two approved looks of one name, the later one holds."}
  },
  "states": {"initial": "draft", "list": ["draft", "approved", "replaced"], "final": ["replaced"],
             "labels": {"draft": "Draft", "approved": "Approved", "replaced": "Replaced"},
             "descriptions": {
               "draft":    "Proposed, and shown to a person. No face is built from it.",
               "approved": "A person saw it and said yes. Faces are built from it.",
               "replaced": "A later look took its place. Kept as the record of how the faces looked."}},
  "transitions": [
    {"name": "approve", "from": "draft", "to": "approved", "label": "Approve",
     "description": "A person of the group brand has seen the look drawn and says yes. An agent does not approve a look."},
    {"name": "replace", "from": "approved", "to": "replaced", "label": "Replace",
     "description": "A person of the group brand takes the look out of use, when a later one has taken its place."}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"designers\") OR INGROUP(\"brand\")",
    "update": "STATE() = \"draft\" AND (CREATEDBY() = USER() OR INGROUP(\"brand\"))",
    "delete": "STATE() = \"draft\" AND (CREATEDBY() = USER() OR INGROUP(\"brand\"))",
    "transition": {
      "approve": "INGROUP(\"brand\") AND USERKIND() = \"person\"",
      "replace": "INGROUP(\"brand\") AND USERKIND() = \"person\""
    }
  }
}
```

What the class holds, in words:

- **One document is one look.** Its `design` is a DESIGN.md. Its
  `direction` keeps what the person asked for and what they said when
  they saw it.
- **A draft is nobody's look yet.** Any designer, an agent among
  them, may write one and change it.
- **Only a person approves**, and only one in the group `brand`. The
  engine refuses an agent, whoever gave it its rights.
- **An approved look is not changed.** A change is a new draft, shown
  and approved as the first was. The document's history, and the
  looks before it, are the record of how the faces looked and who
  said yes.
- **The look that holds** is the approved one of its name with the
  latest `approved_on`.

## A piece of work, command by command

Acme's server has classes and no face. Marta, who keeps Acme's brand,
asks an agent for a screen where people file their expenses. The
agent is a designer on this server; Marta is in the group `brand`.

**1. Read the look before you draw.** Always the first command.

<!-- command: query -->
```jsonc
{"class": "Look", "where": "STATE() = \"approved\" AND name = \"Acme\"", "order": ["-approved_on"], "limit": 1}
```

<!-- answer -->
```jsonc
{"documents": []}
```

No look. Had the answer been `unknown_class`, the server would have
no class `Look` either: define it from this page first. Had it held a
document, steps 2 to 5 would fall away, and the work would begin at
step 6.

**2. Define a direction**, as [the section below](#define-a-direction)
says, and write it as a DESIGN.md. This is Acme's:

<!-- design -->
```md
---
name: Acme
colors:
  ground: "#F4F5F7"
  surface: "#FFFFFF"
  ink: "#16202E"
  ink-quiet: "#5A6577"
  line: "#D9DEE6"
  accent: "#0B5CAD"
  on-accent: "#FFFFFF"
  wrong: "#A4281B"
typography:
  title:  {fontFamily: "Public Sans", fontSize: 20px, fontWeight: 600}
  body:   {fontFamily: "Public Sans", fontSize: 14px, fontWeight: 400}
  label:  {fontFamily: "Public Sans", fontSize: 12px, fontWeight: 600}
  figure: {fontFamily: "IBM Plex Mono", fontSize: 13px, fontWeight: 500}
rounded: {control: 6px, surface: 10px}
spacing: {xs: 4px, sm: 8px, md: 12px, lg: 16px, xl: 24px}
components:
  control: {height: 32px}
---
## Overview
Acme's people file and approve all day, at a desk. The faces are calm
and dense: a quiet grey ground, white surfaces, one blue.

## Colors
- **Accent (#0B5CAD)** marks what is pressed and where the person is.
  Nothing else is blue: no blue headings, no blue borders at rest.
- **Wrong (#A4281B)** is for what the server refused, and nothing else.

## Typography
Four sizes, no more. A figure, an id and a date are in the mono face:
they are facts from the documents, and they line up in a column.

## Layout
Everything sits on a 4 px step. Fields and buttons are one height.
A list is a table with hairlines, not a pile of cards.
```

**3. Show it drawn, and ask what the person sees.** Not this text: a
page, as [Verify it by eye](#verify-it-with-the-person-by-eye) says.
Marta looked at the drawn page and said: "Calmer than I expected.
Good. Make the blue a little darker." The agent darkened the blue,
showed the page again, and she said yes.

**4. Store it as a draft**, with her words in `direction`.

<!-- command: create -->
```jsonc
{"class": "Look", "id": "acme-1", "key": "look-acme-1",
 "values": {"name": "Acme", "design": "(the DESIGN.md above)", "tone": "Plain. \"You\", never \"the user\".",
            "direction": "Marta, 2026-11-21: people file and approve all day at a desk; calm and dense; our blue. On the drawn page: \"Calmer than I expected. Good. Make the blue a little darker.\" Darkened; she said yes."}}
```

<!-- answer -->
```jsonc
{
  "document": {
    "class": "Look", "id": "acme-1", "title": "Acme", "state": "draft", "version": 1, "class_version": 1,
    "created_at": 1795251600, "updated_at": 1795251600, "state_entered_at": 1795251600,
    "created_by": "face-agent", "updated_by": "face-agent", "state_entered_by": "face-agent",
    "values": {
      "approved_by": null, "approved_on": null,
      "design": "(the DESIGN.md above)",
      "direction": "Marta, 2026-11-21: people file and approve all day at a desk; calm and dense; our blue. On the drawn page: \"Calmer than I expected. Good. Make the blue a little darker.\" Darkened; she said yes.",
      "name": "Acme",
      "tone": "Plain. \"You\", never \"the user\"."
    }
  }
}
```

**5. Do not approve it yourself.** The yes in the conversation is not
the approval. The person approves the document, and the engine holds
that:

<!-- command: transition -->
```jsonc
{"class": "Look", "id": "acme-1", "transition": "approve"}
```

<!-- answer -->
```jsonc
{
  "error": {
    "code": "forbidden",
    "message": "face-agent may not take approve from draft",
    "hint": "the rule of approve: INGROUP(\"brand\") AND USERKIND() = \"person\"",
    "path": "/access/transition/approve"
  }
}
```

Tell the person the draft is waiting, and where. Marta approved it
two hours later, from her own screen.

**6. Build from the look that holds.** This is where every later
piece of work on this server begins, for this agent and for any
other:

<!-- command: query -->
```jsonc
{"class": "Look", "where": "STATE() = \"approved\" AND name = \"Acme\"", "order": ["-approved_on"], "limit": 1,
 "fields": ["name", "design", "tone", "approved_by", "approved_on"]}
```

<!-- answer -->
```jsonc
{
  "documents": [
    {
      "class": "Look", "id": "acme-1", "title": "Acme", "state": "approved", "version": 2, "class_version": 1,
      "created_at": 1795251600, "updated_at": 1795258800, "state_entered_at": 1795258800,
      "created_by": "face-agent", "updated_by": "marta", "state_entered_by": "marta", "state_entered_from": "draft",
      "values": {
        "approved_by": "marta", "approved_on": 1795258800,
        "design": "(the DESIGN.md above)",
        "name": "Acme",
        "tone": "Plain. \"You\", never \"the user\"."
      },
      "computed": {
        "approved_by": {"version": 2, "at": 1795258800},
        "approved_on": {"version": 2, "at": 1795258800}
      }
    }
  ]
}
```

## Define a direction

A direction is decided before a screen is drawn, and it is one
thing: a small set of exact values and the reasons for them. Without
it every screen invents its own, and the faces of a server end with
fourteen sizes of type and three different search boxes.

1. **Find the subject before you ask.** The server tells much:
   `list_apps`, `list_classes` and the descriptions say what the
   organisation does and who works here. Ask the person only what the
   server cannot say: who sits at these screens and for how long, at
   a desk or on a phone, and what they must get done there.
2. **Ask for what exists.** A brand has colours, a typeface and a
   mark already; a person has a product they find beautiful. Ask for
   both by name. A direction that starts from them is theirs; one
   that starts from your defaults is yours, and looks like every
   other.
3. **Write one direction, as a DESIGN.md.** One, not three to choose
   from: a choice between three you did not believe in tells the
   person nothing. It holds:
   - **Grounds and ink.** A ground, a surface, the ink, a quieter
     ink, a line. Neutral. Five values carry nine tenths of a screen.
   - **One accent.** It marks what is pressed and where the person
     is, and nothing else. A second accent needs a reason written
     beside it.
   - **Type.** One family, or two that are clearly different, each
     with its job. A scale of sizes you can count on two hands.
   - **Steps.** One step of spacing and its multiples; two or three
     corner radii; one height for a field and a button.
   - **Components.** The few every face needs, each named once: a
     field, a button and its quiet form, the box that finds, a tick,
     a table, tabs, the way from page to page. Each in its states:
     at rest, under the pointer, in focus, pressed, disabled, wrong.
   - **Light and dark**, decided together. A dark shade added later
     is a second look.
   - **Why.** Under each choice, a sentence. The next agent has only
     this file, and the values alone do not say what the blue is for.
4. **Do not spend it on a default.** Some looks are what an agent
   draws when nobody decided anything: content cut into identical
   rounded cards with one soft shadow under each; a tint of the
   accent on every ground; a pill for every control; capitals with
   wide spacing over every heading; the mono face for small labels; a
   gradient as decoration. Each is right for some brief. Use one when
   the person's direction asks for it, never because it came first.

The method follows Anthropic's `frontend-design` skill, and the
format of `design` is Google Labs' DESIGN.md; both are under the
Apache License 2.0.

## Verify it with the person, by eye

A person cannot approve a list of colour codes, and a description of
a look is not the look. They judge what they see.

1. **Draw one page that shows the look whole**: the colours, each on
   the ground it is read on; the sizes of type; every component in
   every state; light and dark side by side. Then **one real screen
   of their own**: a list and a form of a class they know, with
   their own words in it. A look that holds on a specimen and breaks
   on a real form is not ready.
2. **Ask what they see**, not whether they like it. "What is the
   first thing you notice?" finds what "is this fine?" hides.
3. **Change it and show it again.** Their words go into `direction`
   as they said them. Two rounds are usual.
4. **Nothing is built before the yes**, and the yes is theirs to
   give on the document. Until then there is a draft, and a draft is
   nobody's look.

## Stay consistent

Once a look is approved it is the identity of the server.

1. **Values from the look only.** No colour, size, corner or gap in
   a face that the DESIGN.md does not name. Put them in one place at
   the top of the face's styles, under the names the look gives
   them, and let a check fail on any value written anywhere else.
2. **A gallery first.** Every component is drawn once, in all its
   states, light and dark, on one page of the face. A new screen is
   built from the gallery. A component the gallery lacks is added to
   the gallery first, and then used; it is never drawn on the screen
   that happened to need it.
3. **A check, not a hope.** Before a face is called done:
   `npx @google/design.md lint` on the look, for broken references
   and contrast; the face's own check for values outside the look
   and for a style no gallery exhibit shows; every screen at a
   phone's width and by the keyboard alone.
4. **What the look lacks, ask for.** A face that needs a colour for
   a warning, when the look has none, proposes a change to the look:
   a new draft with the one addition, shown and approved. It does
   not invent one locally.
5. **Different on purpose, and said.** A face may look different
   only when the person asks for it in so many words: another brand
   of the company, a public page. Then it is a look of its own, under
   its own `name`, drafted and approved as the first was. Never by
   drift.
6. **The class's words are the class's.** Labels, descriptions, the
   columns of a list, formats and the words in other languages come
   from `get_bundle`. A face that writes its own has two versions of
   them the day the class changes.

## What goes wrong most often

1. **Drawing before reading.** The first screen is built in the
   colours that came to mind, and the look is read afterwards, or
   never. Step 1 of every piece of work is the query of step 6
   above.
2. **Asking for a yes to a description.** "A calm palette with a
   blue accent, shall I go on?" The person says yes to words and no
   to the screen. Draw first.
3. **Approving your own look.** The engine refuses it, `forbidden`,
   with the rule in the hint. Do not look for another way round: a
   look no person approved is a draft.
4. **Changing an approved look in place.** `set` on it is refused,
   `forbidden`. A change is a new draft under the same `name`.
5. **Taking any approved look.** A server may hold two approved
   looks of one name, the old one not yet replaced. The one that
   holds was approved last: order by `-approved_on` and take one.
6. **A second accent.** A green for "new", an orange for "pending",
   a purple for a chart, each added where it was first wanted. The
   look says what colours mean; a face does not add meanings.
7. **A control drawn where it was first needed.** Three boxes that
   find things, each a little different, are three components. The
   gallery has one.
8. **Sizes without a scale.** A size, a gap or a corner chosen by
   eye on each screen. Count them: more than ten sizes of type is a
   face without a rule.
9. **A sentence that explains the screen.** A paragraph under a
   heading saying what the page is for. If the screen needs it, the
   screen is wrong.
10. **Dark as an afterthought.** Colours inverted at the end, with
    contrast nobody measured. Decide both shades in the direction.
11. **A look lifted from somebody's files.** Another product's style
    sheet is its owner's. A reference is looked at; the look is
    written anew.

## The hat

The face builder draws what people see of a FadoDB server. It reads
the server's look before anything else, defines one with a person
when there is none, and then holds to it against its own taste. It
never approves a look, and never changes one that is approved.

## What works today

| Surface | Mark |
|---|---|
| the look as documents of the class `Look`, defined from this page: drafted by a designer, approved by a person of the group `brand`, read by every caller | works today, with no change to the engine |
| a starter that gives a new database the class `Look`, so that nobody has to define it | decided, not built (ADR-0060) |
| the things the organisation keeps, the other half of its rule-book (`Entity`) | decided, not built (ADR-0060) |
| the server handing out a face's pages at its own address, `fadodb serve --pages` | shipped (ADR-0073) |
| the class in the person's words with its formulas compiled, `get_bundle`, and the formula machine for a page, `fadodb-vm` | shipped (ADR-0070, ADR-0029) |
| the server handing this skill, and the other five, to an agent that connects | planned |
| checking a face against the look by itself | the face's own check; the engine has none |

## Procedure, in outline

1. Query the approved look of the server. With one, go to 6.
2. With no class `Look`, define it from this page.
3. Find the subject on the server; ask the person for what it cannot
   say, and for their brand and a product they find beautiful.
4. Write one direction as a DESIGN.md. Draw it: the specimen and one
   real screen of theirs. Ask what they see. Change it. Show it
   again.
5. Store the draft with their words. Tell them it waits for their
   approval. Do not build.
6. Put the look's values in one place in the face. Build the gallery.
   Build the screens from the gallery.
7. Check: the look, values outside it, the gallery, a phone, the
   keyboard. Show the person the face running before calling it done.

## Where the rest is

The decision records behind this page are
[ADR-0060](../../docs/adr/0060-the-organisations-rule-book.md), the
organisation's rule-book and the look of its faces, and
[ADR-0073](../../docs/adr/0073-the-server-hands-out-the-pages-of-a-face.md).
The other hats are in [`../README.md`](../README.md).
