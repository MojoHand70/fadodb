# ADR-0060: The organisation's rule-book: the things it keeps and the look of its faces

- Status: Proposed
- Date: 2026-11-15

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

   | Variable | Holds |
   |---|---|
   | `name` | whose look it is: the company, or one brand of it |
   | `colours` | the main colour, the accent, the background and the text, each a colour code |
   | `fonts` | the fonts of headings and of text |
   | `logo` | a file |
   | `tone` | how a face speaks to people: formal or plain, "you" or "we" |
   | `languages` | the languages faces are shown in, the first the default |
   | `rules` | what else holds: buttons named by their verb, how a date is written |

   Its states are `draft`, `approved` and `replaced`. The group
   `brand` approves a draft; approving one replaces the look that was
   approved before, so one look of a name holds at a time.

4. **What agents do with it**, taught by the skills:
   - The designer's skill reads the confirmed entities before it draws
     a state, matches the words of the story against `name` and
     `other_words`, and asks the person which entity each word means.
     It never defines a class or a table for a thing an entity keeps
     elsewhere. It proposes an entity for a thing it finds nowhere,
     and a class to keep it when it is born nowhere.
   - The face skill of Phase 7 reads the approved look before it draws
     a face, and asks for one when there is none. A class's `icon` and
     `color` are taken from it.
   - Neither skill writes to the rule-book without a person's yes, and
     an agent never confirms or approves its own proposal.

5. **The engine does not change.** Both are ordinary classes: no
   command, no storage and no formula is added, and a database without
   them works as it does today. Until they exist, the descriptions of
   classes and tables are the rule-book, as the designer's skill says.

## Questions for the owner

1. **A copy of a thing born elsewhere.** Employees are born in the HR
   system, and Ola's training request needs them. Proposed: a table in
   this database may copy them, when its entity names the source, an
   import refreshes it, and nobody edits it here. The other way: no
   copies, ever; a process reads the source where it lives, through
   the binding `external` once it is built. That is purer, slower, and
   the process stops whenever the HR system is down.
2. **Where the rule-book lives.** Proposed: in the database, as in
   items 1 to 3. The other way: a file beside the faces, simpler to
   start with, with no owner, no approval and no history but the
   repository's, and invisible to an agent outside that repository.
3. **Who changes it.** While an agent builds a face, Marek says "our
   green is darker now". Proposed: the agent writes a new draft of the
   look and the group `brand` approves it. The other way: the agent
   changes the approved look itself once Marek says yes in the
   conversation.
4. **The engine's own classes, or a starter.** Proposed: a starter,
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
