# ADR-0032: A predefined vocabulary of objects, and how an object is presented

- Status: Accepted; amends ADR-0018
- Date: 2026-11-07

## Context

ADR-0031 gives the model named schemas. Discussion 15 asks for a shipped
set of real-world things with a known look: a person as a chip with an
avatar, a group of people as a stack of coins, a place with its address, a
meeting with its time. Layouts in ADR-0018 bind components to variables and
know nothing about what a person looks like; without a shared vocabulary
every class author would define `Person` again, differently, and every
frontend would draw it differently. The vocabulary must be stable, because
class versions store its shape (ADR-0031), and small, because a field never
comes back out.

## Decision

1. **The vocabulary.** The engine ships these schemas. Their names are
   reserved; a user schema may not take them. They are versioned with the
   engine; a release may add a schema or add a field to one, and never
   removes or retypes a field.

   | Schema | Fields |
   |---|---|
   | `Person` | `id` text, `name` text, `email` text, `phone` text, `avatar` text |
   | `Organization` | `id` text, `name` text, `tax_id` text, `email` text, `phone` text, `address` Address |
   | `Address` | `line1` text, `line2` text, `city` text, `postal_code` text, `region` text, `country` text |
   | `GeoPoint` | `lat` number, `lon` number |
   | `Place` | `name` text, `address` Address, `geo` GeoPoint |
   | `Period` | `start` datetime, `end` datetime |
   | `Event` | `title` text, `start` datetime, `end` datetime, `all_day` bool, `place` Place, `organizer` Person |
   | `Quantity` | `value` decimal(6), `unit` text |
   | `File` | `hash` text, `name` text, `media_type` text, `size` int |

   `Person.id` is any text; when the embedder uses its principal ids,
   `assignee.id = USER()` is the "mine" predicate. `avatar` is a URL or a
   file hash (ADR-0033) that the frontend resolves; the engine never does.
   `country` is an ISO 3166-1 alpha-2 code by convention, which the engine
   does not check. `File` is given its meaning by ADR-0033.

2. **A presentation block.** A schema, predefined or user-defined, may
   declare `presentation`: `title` and `subtitle`, formulas over the
   schema's own fields restricted to pure functions as the class `title` of
   ADR-0018 is; `avatar`, the name of a text field holding an image URL or
   a file hash; `icon` and `color` as in the class header. The block is
   part of the schema JSON, travels with the resolved schema in the bundle
   with its formulas compiled, and nothing of it is stored per document.
   The predefined schemas ship with one: `Person` titles by `name` with
   `email` under it and `avatar` as the avatar; `Organization` by `name`;
   `Address` by `line1` with city and country under it; `Place` by `name`
   with the address under it; `Period` and `Event` by their dates and
   title; `Quantity` by value and unit; `File` by `name` with the size
   under it.

3. **Components for objects** (amends the layout format of ADR-0018). A
   layout may bind these components to a variable of object or rows type:
   - `chip`: one line, the title and the avatar or icon.
   - `card`: every field with its label; a nested object is a section.
   - `coins`: a collection of objects as overlapping avatars with an
     overflow count; each coin opens as a chip.
   - `list`: a collection as chips, one per line.

   `field` bound to an object variable renders its chip, and `field` may
   bind a path such as `contact.email` to render one leaf. `grid` keeps
   rendering a `rows` variable as a table, whatever its columns. The
   property formulas `visible`, `enabled`, `editable` and `style` apply as
   to any component. The reference renderer implements them in Phase 7; a
   frontend that lacks a component renders the card. How a frontend lets a
   person pick a `Person` or a `Place` is the frontend's and the
   embedder's, since the engine knows no people (ADR-0027); the engine
   stores what it is given.

4. **Extension.** A user schema may `extend` one schema. Its fields are the
   base's fields followed by its own, and it takes the base's presentation
   unless it declares one. Extension is resolved when the schema is saved,
   like a Go embedding: there is no subtyping, an `Employee` is not a
   `Person` in a formula or a binding, and a later change to the base
   reaches the extending schema when that schema is saved next.

5. **Rating is not an object.** A rating is a number within a range;
   ADR-0034 adds `min` and `max` to numeric variables and a `rating`
   component.

## Consequences

- The vocabulary is a public contract from Phase 2: names, fields and
  types are listed in the documentation and pinned by a definition test; a
  change to it is an ADR.
- Every frontend gets the same look for a person or a place across every
  class, and a user-defined schema gets it from one block.
- The engine never resolves an id, an avatar or a country; the frontend and
  the embedder do. ADR-0027 stands: the engine knows no people.
- `Event.organizer` and `Place.address` nest objects three deep below a
  variable, inside ADR-0031's limit; a user schema that embeds `Event`
  reaches four.
- Comments remain planned after 1.0 as side objects. Attachments do not, by
  ADR-0033.

## Performance

- Nothing in the commit path beyond ADR-0031.
- The bundle grows by the schemas a class uses and their compiled
  presentation formulas; it is cached by ETag.
- Presentation formulas evaluate in the browser VM inside the 1 ms for 200
  formulas of ADR-0012. A `coins` component over a collection of 1,000
  persons would evaluate 1,000 titles, so the renderer evaluates titles for
  what is on screen only.
