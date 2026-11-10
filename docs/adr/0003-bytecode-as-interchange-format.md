# ADR-0003: Bytecode as the interchange format

- Status: Accepted; amended by ADR-0029 (the browser VM ships as one plain JavaScript file), ADR-0037 (format v3: the query slot carries the LOOKUP condition), ADR-0046 (format v4: the query slot carries an aggregate)
- Date: 2026-11-05

## Context

Formulas run in three places: the Go VM on the server when a value changes, the
SQL engine when a view or query reads computed columns, and the browser while
a user edits a document. Projects that kept two hand-written evaluators of the
same language, such as Camunda with FEEL Scala on the server and feelin in the
browser, document feature drift between them. The predecessor engine in sfmt
avoided drift between its VM and SQL paths with differential tests, but its
VM carried floats only and left text to the SQL path.

## Decision

- One compiler, in Go, turns formula text into a versioned typed bytecode.
- Three consumers execute or translate that bytecode: the Go VM, the SQL
  generator, and the browser VM written in TypeScript.
- The browser never parses formula text. It receives bytecode, a constant
  pool and a dependency graph in the class bundle.
- The bytecode format is a public contract from release 0.4 onward, with its
  own version number and a compatibility promise after 1.0.
- A conformance suite of `{formula, inputs, context, expected}` cases runs on
  all three consumers in CI, plus property-based tests that generate random
  formulas and assert the targets agree.

## Consequences

- Adding a function means implementing it three times, with the suite proving
  equivalence. That is the cost of a single behaviour.
- The SQL target cannot express everything; functions are classed as pure,
  context or data, and the data class runs on the server only, with the
  browser receiving the last computed value.
- Third parties can port the VM to another language by passing the suite.

## Performance

Budget: a five-operation arithmetic formula executes in under 100 ns with
zero allocations in the Go VM; 200 formulas recalculate in under 1 ms in the
browser VM. Benchmarks live next to the VM packages.
