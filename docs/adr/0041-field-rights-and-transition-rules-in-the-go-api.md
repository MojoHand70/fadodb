# ADR-0041: Field rights and transition rules in the Go API

- Status: Proposed; amends ADR-0010 (when the per-transition rules are enforced), ADR-0028 (the row a right is evaluated over, the paths the projection covers, the filter)
- Date: 2026-11-08

## Context

ADR-0010 made the per-transition rules formulas the VM evaluates at
commit time, and ADR-0028 added the rights table: per state and variable,
a `read` and a `write` rule, enforced on commits and on reads, with the
write check "after the class-level and per-transition rules of ADR-0010"
and its budget measured in Phase 3. Phase 2 shipped the Go API with the
principal recorded as the actor and enforcing nothing, as its package
documentation says; the roadmap places the field-level rights in Phase 3
and the access rules compiled to SQL predicates in Phase 5. The first
embedder's test for the rights is plain: a variable hidden from a group
in a state is absent from that group's read, and a set on it is refused.

Three things the records leave open, and the filter of ADR-0038 forces:
which row a rule is evaluated over, since a right may read the document
(`owner = USER()`) and a command may change that very variable; what a
filter does with a right that depends on the row, since `where` and
`order` span documents in many states; and who a call without a principal
is, now that the principal decides something.

## Decision

1. **What Phase 3 enforces.** The per-transition access rules of ADR-0010
   and the rights table of ADR-0028, on every path of the Go API. The
   class-level `read`, `create`, `update` and `delete` rules, the SQL
   predicate of ADR-0010 and the change stream wait for Phase 5 with the
   server. Until then every principal reads every row of every class;
   what it sees of a row and what it may set are governed here.

2. **The rules run over the stored document.** On `Set` and `Transition`,
   the transition's access rule and the write rights are evaluated over
   the document as it is stored, before the assignments, in the state the
   document is in when the command arrives: the rules say who may change
   this document, not who may change the document the change would make.
   On `Create` and `CreateMany` the write rights are evaluated over the
   new document after its defaults, in the initial state. The order inside
   a commit is the transition's access rule when there is one, then the
   write right of every variable the caller sets, then the pipeline of
   ADR-0015. A rule that errors is FALSE (ADR-0019). An administrator
   bypasses both and never a guard. Defaults, computed variables and the
   engine's own writes are not the caller's and are not checked; `Migrate`
   sets nothing.

3. **The write check** refuses, with `forbidden` naming the variable, a
   value for a variable whose write rule in the document's state is not
   TRUE for the principal: the cell of the named state first, then the
   cell of `*`; a variable with no cell is governed by the class rules
   alone (ADR-0028). A commit reports the first forbidden variable in
   declaration order, as it reports the first missing required one; a
   preview reports every one under its errors.

4. **The read projection.** Every document the API returns, from `Get`,
   `Version`, `History`, `Query` and `Preview` and as the result of
   `Create`, `Set` and `Transition`, is projected for the principal: a
   variable whose read rule in the row's state is not TRUE for the
   principal is absent from `Values`, not null. The rule is evaluated over
   that row, so a history row is projected under the state and the values
   it had. The system variables are always present. The rights table of
   the current class version applies by variable name to every row; a
   variable the current version does not have has no cell and shows as
   before. The SQL endpoint and the views stay row-level, as ADR-0028
   decided, and `TableRows` reads an explicit table, whose rules come with
   Phase 5.

5. **The filter.** `where` and `order` of a `Query` or a `Count` may name a
   variable only when the principal may read it in every state without
   the row: no cell names it, or every cell that does is a group list
   naming a group of the principal or a formula that reads no variable
   and evaluates TRUE for the principal. Otherwise the filter is refused
   with `forbidden` naming the variable and the element. `fields` is
   projected as a document is: a hidden variable is absent from the
   result, with no refusal. The alternative, deciding per row and dropping
   the rows a principal may not filter on, would make the same filter
   return different rows for different people without saying so.

6. **A call without a principal runs as nobody**: no id, no groups, not an
   administrator. It sets what the rules allow nobody in particular and
   sees the same. An embedder that wants everything passes an
   administrator. A class without rights and without transition rules
   behaves as in Phase 2, so nothing that works today stops working.

7. **The listing and the preview** of ADR-0040 report a transition whose
   access rule is not TRUE for the principal as not enabled, with
   `forbidden`.

## Consequences

- The first embedder's test holds at the end of Phase 3 for the Go API,
  and again for the server in Phase 6 with the same code, since the
  projection is the store's and not the encoder's.
- A right that reads the document cannot be filtered or sorted on by a
  principal it does not decide without the row. A designer who wants a
  variable filterable by a group writes its right as that group's list,
  or as a formula over context functions alone. The documentation says so
  beside the rights table.
- Rules evaluated over the stored document mean a caller may set `owner`
  to someone else and lose the right to the document in the same commit;
  they also mean nobody grants themselves a right by the change that
  uses it.
- The package documentation of `fado`, the architecture's Querying section
  and the README change from "enforces nothing until Phase 5" to "rows in
  Phase 5; fields and transitions now".
- Tests and examples that define rights pass principals. The ones that
  define none are unchanged.

## Performance

Budgets from ADR-0028: the write check is one formula per set variable,
inside the commit budget of ADR-0006; the read projection under 5 µs per
document of fifty variables, measured in the store with
`BenchmarkProject` on a class with a rights cell per variable, group lists
and formulas alike, and reported in `docs/perf/v0.3.0.md`. The filter's
check is decided once per call when the filter compiles and costs nothing
per row. The encoder of ADR-0018, Phase 6, receives the projected document
and pays nothing more.
