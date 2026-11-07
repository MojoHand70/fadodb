# ADR-0018: JSON for definitions and interchange: class metadata, layouts and the wire format

- Status: Accepted; amended by ADR-0022 (exact values and money on the wire), ADR-0023 (dates and datetimes on the wire, enum values), ADR-0028 (the rights table in the bundle), ADR-0031 (objects on the wire and schemas in the bundle), ADR-0032 (presentation block and object components), ADR-0033 (attachments are File values, not side objects), ADR-0034 (min and max in the bundle)
- Date: 2026-11-06

## Context

ADR-0014 removed JSON from document storage: values are typed columns,
history is a table, events reference versions. That raised the question of
what is still JSON, and where everything that is not a variable lives: the
class's display metadata, the visuals, data without a typed shape, and the
document as a client sees it. Earlier drafts had a single "UI schema" with
no stated storage, versioning or validation, and no canonical shape for a
document on the wire.

## Decision

- **JSON is the format for definitions and interchange, not for storage.**
  Class definitions, layouts, the function catalog and the bytecode debug
  form are JSON documents and are stored as JSON. Commands, documents on the
  wire and the bundle are JSON. Document values are typed columns
  (ADR-0014); the only JSON inside a document table is the opaque `json`
  variable type (ADR-0013).
- **Class header.** Besides `name` and `version`, a class may declare
  `label`, `plural`, `description`, `icon`, `color` and `title`. `title` is a
  formula restricted to pure functions and the document's own variables. It
  is computed like an auto variable, stored in the current and history
  tables, and exposed as the system variable `TITLE`; without a `title` the
  system variable equals `ID`.
- **Layouts.** A class has any number of named layouts, each a JSON document
  with `class`, `name` and `components`. Component properties `visible`,
  `enabled`, `editable`, `style` and templates are formulas compiled with the
  class and evaluated in the browser VM. Layouts live in the `layouts` table
  (class, name, version, json), are versioned independently of the class, and
  never cause a class version or a migration. Saving a layout validates every
  binding and formula against the class; a class change that removes a
  variable a layout binds to is rejected until the layout changes. Nothing
  visual is stored per document.
- **Bundle.** `GET /v1/classes/{name}/bundle` returns the class definition,
  its layouts, the compiled bytecode of every formula including layout
  bindings, and the slice of the function catalog they use, with an ETag
  derived from the class and layout versions. A frontend needs nothing else
  to render a document and evaluate it live.
- **Wire shape.** A document is one JSON object with the system variables at
  the top level (`class`, `id`, `title`, `state`, `version`, `created_at`,
  `updated_at`, `state_entered_at`) and the declared variables under
  `values`, composites inline as arrays of scalars or objects. Dates are
  integer seconds, refs are `"Class:id"` text, blanks are `null`, `json`
  variables are embedded as they are. Reads, the change stream, create
  (`values`) and `PATCH .../values` (changed variables only; absent means
  unchanged) share this shape.
- **Side objects.** Attachments and comments are neither variables nor
  layouts. They are planned after 1.0 as side tables keyed by class and
  document id with their own events. The core model does not depend on them.

## Consequences

- Every non-variable property has one home: display metadata in the class
  header, visuals in layouts, shapeless data in a `json` variable, files and
  discussion in future side objects. Nothing needs a per-document blob.
- A layout designer and a class author work independently; a layout change
  ships without touching documents.
- `TITLE` is one more column per current and history row, and `title`
  formulas join the auto dependency graph.
- The wire shape is a public contract from the first server release, so the
  OpenAPI document and the conformance suite carry it.
- A `json` variable is deliberately inconvenient in formulas so that typed
  variables stay the default.

## Performance

- Encoding a 50-variable document to JSON: under 5 µs with a hand-written
  encoder and no reflection, measured in Phase 6 with the server budget.
- The bundle is fetched once per class and layout version and cached by
  ETag, so it adds no per-document cost.
- `TITLE` adds one text column per history row; included in the Phase 2
  commit benchmark.
