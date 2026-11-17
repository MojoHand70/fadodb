# fadodb-vm: the formula machine for a page

A page that shows a document works its formulas out itself, as the
person types, with the same answers the server gives. It is given each
formula compiled by the engine, the bytecode, and runs it here; it never
parses a formula ([ADR-0003](../../docs/adr/0003-bytecode-as-interchange-format.md),
[ADR-0029](../../docs/adr/0029-browser-vm-as-one-plain-javascript-file.md)).

```js
import { decode, VM, number } from "./fadodb-vm.js";

const program = decode(bytes);             // the bytecode, a Uint8Array
const value = new VM().run(program, [number(12), number(3)], { user: "ann" });
```

A formula that reads other documents, `LOOKUP` and its counts and sums,
is worked out by the server, and the page is given its last value.

## Where it stands

Phase 7 builds it in four parts. **Parts 1 and 2 are built: the
machine, plain numbers, text and logic; dates and moments, and the
functions of a document's life.** Every instruction runs but those of
exact numbers, and every function but those of exact numbers and
money, which wait for part 3; part 4 adds what is left and measures the
speed. What waits says so: it throws an error named `NotBuilt` and
never answers a value.

On every push, CI builds the file and runs every case of the
conformance suite through it, compiled by the engine
(`conformance/formula`): what parts 1 and 2 run passes, case for case.
Beside the suite, 3,000 numbers are rendered as text as Go renders
them, and 4,200 calls of the functions of dates, drawn at random over
year 1 to year 9999 with texts that are almost a date, answer what the
Go VM answers.

## Building and testing

Node 24 and npm, from the root of the repository:

```sh
make vmtest     # npm ci, type check, build into web/vm/dist, every case through the built file
```

The build writes `dist/fadodb-vm.js`, an ES module, and
`dist/fadodb-vm.iife.js`, a classic script that defines the global
`FadoDB`, both minified and held to 100 KB. Neither is committed.

## The files

| File | What it holds |
|---|---|
| `src/index.ts` | the public surface: what a page imports |
| `src/bytecode.ts` | reading the bytecode, format 4, and checking it |
| `src/vm.ts` | the machine: the loop over the instructions, IFERROR |
| `src/builtins.ts` | the functions, in the order of the engine's table |
| `src/value.ts` | values, as the Go VM keeps them |
| `src/numbers.ts` | plain numbers worked out to the last bit as Go does |
| `src/text.ts` | text by code point, Unicode's simple case mappings |
| `src/dates.ts` | calendar days and instants, as Go's time.Date carries them |
| `src/render.ts` | a value as text, the TEXT rule |
| `src/errors.ts` | the codes a formula stops with |
| `cases/main.go` | writes the conformance cases, compiled, for the runner |
| `test/run.mjs` | runs them through the built file |
| `build.mjs` | the build, and the size budget |
