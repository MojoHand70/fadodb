# ADR-0031: Named schemas and object values

- Status: Accepted; amends ADR-0013, ADR-0014, ADR-0018, ADR-0019
- Date: 2026-11-07

## Context

ADR-0013 gives a class scalar variables, `array<scalar>` and `rows<schema>`,
where a schema is a list of named scalar columns written inline in the
variable, and nothing nests. Documents describe things of the world: the
person who asked, the place a delivery goes to, the meeting, the file that
came with the request. Today such a thing is either spread over loose
scalars (`assignee_id`, `assignee_name`, `assignee_email`) that nothing
holds together, or hidden in a `json` variable that formulas reach only
through `JSONVALUE` and that rules and indexes cannot use. Discussion 15 on
the repository asks for predefined objects reflecting physical notions,
which an author adds to a document and a layout renders with a known look,
and for objects the author defines in the same way. The owner's model is a
struct in Go: flat typed fields, value semantics, no identity of its own.

Four facts shape the decision. `rows<schema>` already defines a record
shape; a struct is that shape with exactly one row. The grammar of ADR-0019
already has `x.col`, and the binder's member-access node refuses composites
until Phase 2, so field access costs no new syntax. The hot path must not
allocate, so an object must not become a boxed value in the VM. And history
is typed rows (ADR-0014), so whatever an object is, it must be columns.

## Decision

1. **Schemas are named and versioned.** A schema is a named list of
   fields. A field has a name and a type: any scalar type of ADR-0013 and
   its amendments except `json`, or `object<S>` for another schema. A
   schema holds no collection: `array` and `rows` exist only as document
   variables. A schema may not contain itself, directly or through another
   schema, and objects nest at most four deep below a variable. User
   schemas live in a `schemas` table (`name`, `version`, `json`) at the
   database level and are created and changed with a `define schema`
   command; the engine ships the predefined ones of ADR-0032, whose names
   are reserved. An inline schema on a `rows` variable stays allowed and is
   anonymous; its columns follow the same field rules and may be objects.

2. **`object<S>` is a variable type.** It holds one record of the schema
   `S`, or a blank. `rows<S>` takes a named schema as well as an inline one
   and is the collection of objects; `array` holds scalars only, as before.
   Values stay immutable: an object is replaced whole, in a commit, as
   ADR-0013 says of composites.

3. **Storage flattens an object to columns** (amends ADR-0014). Each leaf
   field becomes one typed column named by its path joined with `__`:
   `shipping__city`, `meeting__place__geo__lat`. The columns live in
   `doc_<Class>` and `doc_<Class>_history` like any variable, and in the
   child table when the object is a field of a `rows` element, where the
   path starts at the element. A money leaf keeps its two columns. No child
   table is created for an object. Views expose the same prefixed names.
   `"index": true` may be set on a leaf path. `__` is reserved in schema,
   variable and field names, as it was already the separator of child
   table names, and a definition is rejected when a generated table would
   exceed 1,000 columns.

   A field name may not be `doc_id`, `version`, `idx` or `value`, the
   columns a child table adds, and may not end in `_currency`. The other
   names ADR-0013 reserves for variables (`id`, `title`, `state` and the
   rest) are free for fields, because a field never appears unprefixed in a
   document table; `Person.id` is a field.

4. **Field paths in formulas** (amends ADR-0019). `v.f` reads field `f` of
   the object `v`, and paths chain: `v.f.g`. On a `rows` variable `lines.f`
   is an array as before, and `lines.o.f` is the array of a nested leaf.
   The static type of a path is the leaf's type, or `object<S>` when the
   path ends on an object field. Names resolve case-insensitively as
   variables do. The binder resolves a leaf path to one column slot, so a
   leaf read costs what a scalar variable costs on every evaluator.

5. **Whole objects.** An expression of type `object<S>` is a variable, a
   path ending on an object field, an `IF` or `IFERROR` with object
   branches, or `BLANK`. There is no constructor: a formula chooses an
   existing object and never builds one from scalars. A computed variable
   may be of type `object<S>` when its formula has that type. Three
   operations take a whole object:
   - `ISBLANK(v)` is TRUE when every leaf of `v` is blank. `ISBLANK(v.f)`
     tests one leaf.
   - `=` and `<>` between two objects of the same schema compare leaf by
     leaf under the scalar rules of ADR-0019 and are equal when every leaf
     is equal. Two different schemas are a definition-time error.
   - `IF` and `IFERROR` choose between two objects of the same schema.

   Everything else is a definition-time error: `<` and the other
   orderings, arithmetic, `&`, `TEXT`, aggregates and array functions. A
   collection of objects is still never compared whole.

6. **The compiler scalarizes.** Every object operation compiles to
   operations on leaves: `ISBLANK(v)` to a conjunction of leaf tests,
   `a = b` to a conjunction of leaf comparisons, `IF(c, a, b)` to one `IF`
   per leaf with the condition evaluated once, and a computed object
   variable to one computed column per leaf. The Go VM, the SQL dialects
   and the browser VM never see an object value, and bytecode format v2
   gains no opcode.

7. **Objects on the wire** (amends ADR-0018). An object is a nested JSON
   object keyed by field names; a blank leaf is `null`; an object whose
   every leaf is blank is written as `null` and may be sent as `null`. On
   input a field absent from an object is blank, because the object is
   replaced whole; on `PATCH .../values` the variable is still the unit, so
   an absent variable is unchanged and a present object replaces the stored
   one. The bundle carries the resolved schemas the class uses.

8. **Schema versions are inlined into class versions.** A class names a
   schema by name. Saving the class resolves the schema's current version
   and stores the resolved shape in the class version, as a Go program
   compiles a type in. Changing a schema makes a new schema version and
   leaves every class as it is; a class takes the new shape when it is
   saved next, and that save is an in-place change or a migration under
   ADR-0014's rules, leaf by leaf. A schema may be deleted only when no
   current class definition names it.

9. **Objects carry no identity.** A `Person` in two documents is two
   values. A field such as `id` is the link to whatever the embedder knows,
   and it is any text; a `ref` remains the way to point at a document. A
   document's history therefore shows the person's name as it was at each
   version.

## Consequences

- A person, a place or a file on a document is typed columns in the
  current, history and child tables. "As of" queries, the projection of
  Phase 8 and process mining see them without JSON.
- Read rules (ADR-0010) compile on leaves: `assignee.id = USER()` is an
  indexed predicate, which is the "my documents" rule. Field rights
  (ADR-0028) name the object variable and cover all its leaves.
- "No nesting" becomes "no collection inside a schema or a collection".
  Objects nest freely because flattening makes depth free.
- The `json` variable is needed less often; it keeps data with no shape.
- The binder's `composite values are not supported yet` is lifted in Phase
  2 together with the rows work already planned there.
- A class definition gains `object` and named schemas; the class JSON
  schema, the OpenAPI document and the conformance suite grow with them.
  The suite gains cases for field paths, object blankness and equality on
  all three evaluators.
- Rows gain more columns per document; the per-table column cap is a
  product rule like the element caps.
- The first embedder's file fact, `{hash, name, media_type, size}`, is a
  schema, which ADR-0033 uses.

## Performance

- A leaf read is a scalar slot read: the same budget and the same zero
  allocations as a scalar variable under ADR-0012.
- Equality of two objects with `k` leaves is `k` scalar comparisons; a
  `Person` has five.
- A commit writes more columns per row. The Phase 2 commit benchmark adds a
  class with `Person`, `Place` and `Event` objects and a `rows<Person>`
  collection and must stay under the 2 ms p99 of ADR-0006.
- The bundle grows by the schema JSON, cached by ETag as before.
- Opening a database reads the `schemas` table once, inside the 50 ms of
  ADR-0012.
