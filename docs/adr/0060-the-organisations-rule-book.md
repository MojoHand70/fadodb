# ADR-0060: The organisation's rule-book: the things it keeps and the look of its faces

- Status: Accepted by the owner on 2026-11-21 ("accept adr-0060"). Item 3, the class `Look`, and the face builder's skill of item 4 withdrawn by the owner on 2026-11-27: a face and its look are the tool's that builds faces, not the engine's. Decided and not built yet: the things the organisation keeps, item 2, the designer's skill reading them, and the starter of question 4.
- Date: 2026-11-15; the look rewritten 2026-11-21

## Context

FadoDB is built so that a person can tell an agent the story of a
process and the agent builds it: the class with its approval paths and
its checks, and a face for the people in it. On 2026-11-15 the owner
added two rules for that agent.

**Never keep a thing twice.** Before it builds, the agent finds which
classes and tables already hold the organisation's business objects,
tries to understand the organisation's ontology, and never makes a
second list of a thing that has one. It confirms with the person which
list to use. For a thing it does not know it asks where the thing is
born, who manages it and how to get the list. If no answer exists, it
proposes a process, and a face, to keep that list.

**Keep the faces coherent.** The same holds for how faces look: the
company's branding, its colours, its look and feel, discussed with a
person, confirmed, and kept in a rule-book, "maybe even in Fado".

Agents make the first rule matter more than it did for people. A team
that builds applications by hand makes a duplicate list of employees
once a year; an agent that builds a process in an afternoon can make
ten in a week, and every report after that is wrong in one of them.

What there is today:

- `list_classes`, `list_tables` and `list_schemas` answer the names a
  database keeps, and `get_class`, `get_table` and `get_schema` each
  definition with its `label` and its `description`, words for people
  that the engine keeps and does nothing with.
- A table's rows are kept in the database file. The binding `external`,
  rows read where another system keeps them, is planned (ADR-0016), so a
  thing born in another system is a table that an import fills.
- A class has an `icon` and a `color` (ADR-0018). Layouts and faces come
  with Phase 7, and nothing keeps a company's look.
- Since 2026-11-15 the designer's skill begins with "look before you
  build" and asks an agent to write `Born: ... Owner: ... List: ...` in
  the description of every class and table it makes.

Descriptions are free text. They hold no other words for the same
thing ("staff", "worker"), nobody approves them, an agent cannot ask
for every thing born in the HR system, and a face's look has no place
in them at all.

## Decision

1. **The rule-book is kept as documents in the database.** Two
   classes, one for the things the organisation keeps and one for the
   look of its faces. Being documents, they have what an answer given
   once to an agent needs: an owner who approves a change, a history
   of who changed what and when, rules for who may read and who may
   change, and one way to read them for every agent and every face,
   the command set.

2. **`Entity`: one document for each thing the organisation keeps.**

   | Variable | Holds | Example |
   |---|---|---|
   | `name` | the word people use | employee |
   | `other_words` | other words for the same thing | staff, worker |
   | `meaning` | one sentence | a person employed by the company under a contract |
   | `born` | where a new one comes into being: `here` or `elsewhere` | elsewhere |
   | `source` | the system it is born in, when it is born elsewhere | the HR system |
   | `kept_in` | the class or table of this database that holds it, or its copy | `People` |
   | `owner` | the person or group who answers for it | Anna Wolska, HR |
   | `list` | how to get the list | the table `People`, copied every night at 02:00 |

   Its states are `proposed`, `confirmed` and `retired`. An agent
   proposes an entity after the person it works with has said yes in
   the conversation; the entity's owner, or the group `data-owners`,
   confirms it; an entity no longer kept is retired. Every caller with
   an id may read the entities.

3. **`Look`: one document for each look of the organisation's faces.**
   **Withdrawn by the owner on 2026-11-27**, with its sample class and
   its test: the look of faces is kept by the tool that builds them.
   Rewritten on 2026-11-21. The owner saw the faces an agent had built
   without a rule and said what was missing, in three steps: "Define a
   direction. Verify that direction with user. Stay consistent in
   styling, the look-and-feel becomes this FadoDB server visual
   identity and unless [the] designer asks [explicitly] to create
   something different, once identity is created, all apps should
   follow." And of every face that will be built on FadoDB: "each can
   be different", but "if we don't teach them and their apps will look
   [bad] we will not win. We have to guide them by hand."

   | Variable | Holds |
   |---|---|
   | `name` | whose look it is: the organisation, or one brand of it |
   | `design` | the look itself, as a DESIGN.md: the exact colours, type, corners and spacing, and in words why each is so and where it is used |
   | `direction` | what the person asked for, and what they said when they saw it drawn |
   | `tone` | how a face speaks to people: formal or plain, "you" or "we" |
   | `approved_by`, `approved_on` | who saw it and said yes, and when; the engine writes both at the approval |

   - **The look is written in the DESIGN.md format** (Google Labs,
     Apache License 2.0): exact values an agent uses, and prose that
     says why. It is a format the agents of every vendor read, and it
     comes with a check for contrast and for a value that names
     nothing. The colours, the fonts and the rules that this item
     first kept as three variables are inside it.
   - **Its states are `draft`, `approved` and `replaced`.** A designer,
     an agent among them, writes a draft and may change it. **Only a
     person of the group `brand` approves**: the rule of the
     transition is `INGROUP("brand") AND USERKIND() = "person"`, so
     the engine refuses an agent, whoever gave it its rights.
   - **An approved look is not changed.** A change is a new draft,
     shown and approved as the first was. Of two approved looks of
     one name the one approved last holds; a person of `brand`
     replaces the earlier one, which is kept.
   - **A face that is to look different** is given a look of its own,
     under its own `name`, drafted and approved the same way. Nothing
     else makes a face of the server look different.
   - The class is `examples/classes/look.json`, and a test defines it
     and walks it on each push.

   Not in the class yet: the organisation's mark, as a file. The
   languages faces are shown in are the classes' own words in other
   languages (ADR-0068), not the look's.

4. **What agents do with it**, taught by the skills:
   - The designer's skill reads the confirmed entities before it draws
     a state, matches the words of the story against `name` and
     `other_words`, and asks the person which entity each word means.
     It never defines a class or a table for a thing an entity keeps
     elsewhere. It proposes an entity for a thing it finds nowhere,
     and a class to keep it when it is born nowhere.
   - **Withdrawn by the owner on 2026-11-27**, with item 3: the face
     builder's skill left the engine for the tool that builds faces.
     As it was: the face builder's skill, `skills/fadodb-face/`, written on
     2026-11-21 as the sixth hat, reads the approved look before it
     draws anything. With none, it defines a direction with the
     person, shows it to them drawn, never described, and stores the
     draft with their words; it builds only once a person has
     approved. From then on it takes every value from the look, draws
     each component once, in a gallery, and checks the face against
     both. Its method of defining a direction follows Anthropic's
     `frontend-design` skill (Apache License 2.0). A class's `icon`
     and `color` are taken from the look.
   - Neither skill writes to the rule-book without a person's yes, and
     an agent never confirms or approves its own proposal.

5. **The engine does not change.** Both are ordinary classes: no
   command, no storage and no formula is added, and a database without
   them works as it does today. Until they exist, the descriptions of
   classes and tables are the rule-book, as the designer's skill says.

## The owner's ruling

Accepted by him on 2026-11-21: "accept adr-0060". The four questions
below stood in this record from 2026-11-15. He answered the second in
his own words that day, for the look; of the other three he turned
none round, so each stands as it was proposed. They are kept here as
they were asked, with the other way, so that he can turn one round
later.

1. **A copy of a thing born elsewhere.** Employees are born in the HR
   system, and Ola's training request needs them. Stands as proposed: a table in
   this database may copy them, when its entity names the source, an
   import refreshes it, and nobody edits it here. The other way: no
   copies, ever; a process reads the source where it lives, through
   the binding `external` once it is built. That is purer, slower, and
   the process stops whenever the HR system is down.
2. **Where the rule-book lives.** **Answered for the look on
   2026-11-21: in the database.** The owner: "Yes, it definitely needs
   to be on our server, served as other skills." The skills are not
   served by the server yet: they are files an agent's program copies,
   and the server handing them to an agent that connects is a record
   of its own, to come. With the record accepted the same holds for
   the things the organisation keeps: in the database, as in
   items 1 to 3. The other way: a file beside the faces, simpler to
   start with, with no owner, no approval and no history but the
   repository's, and invisible to an agent outside that repository.
3. **Who changes it.** While an agent builds a face, Marek says "our
   green is darker now". Stands as proposed, and built so on
   2026-11-21: the agent writes a new draft of the
   look and a person of the group `brand` approves it. The other way: the agent
   changes the approved look itself once Marek says yes in the
   conversation.
4. **The engine's own classes, or a starter.** Stands as proposed,
   and not built yet: until a starter exists, an agent defines the
   class of the look from the skill's page on a server that has
   none. The proposal: a starter,
   two ordinary classes a new database is given and the organisation
   may extend, with a field for a data steward or a privacy class, say.
   The skills find them by their names. The other way: the engine's
   own, as `Substitution` is (ADR-0053): in every database and never
   removed, but then no organisation could name a class of its own
   `Entity` or `Look`.

## Consequences

- An agent's first questions to a person are about what the
  organisation keeps. They take time at first and are answered once,
  for every agent after.
- The rule-book is only as good as the people who confirm it. An
  entity nobody confirmed is a proposal, and an agent says so when it
  relies on one.
- The engine does not stop a second list of a thing: a person or an
  agent that ignores the rule-book can still make one. The skills, and
  the person who approves the agent's class, prevent it.
- A face that ignores the look still works; it only looks wrong.
- The look is data, so a company changes its colours once, and every
  face that reads it follows.

## Performance

None. Two ordinary classes with a few dozen documents each, read when
an agent starts a piece of work and when a face opens.
