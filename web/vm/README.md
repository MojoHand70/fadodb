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

**Built.** Every instruction of the bytecode and every function a page
runs: plain numbers, text and logic, dates and moments, the functions
of a document's life, and exact numbers and money on `BigInt`, 95 of
the 104 functions of the language; the other nine are the server's or
not built in the engine either. Each release carries the three files.

**Fast.** A document of 200 formulas, each reading the one before,
recalculates whole in about 13 µs of the 1,000 µs budget of ADR-0003,
and in about 42 µs with decimals or money in place of numbers. The
numbers, three runs each, are at the end of ADR-0029.

On every push, CI builds the file and runs every case of the
conformance suite through it, compiled by the engine
(`conformance/formula`): every one a page runs passes. Beside the
suite, 3,000 numbers are rendered as text as Go renders them, 4,200
calls of the functions of dates are drawn at random over year 1 to year
9999 with texts that are almost a date, and 4,800 calls of exact numbers
and money at every scale, with mantissas next to the 64-bit limit, the
six methods of rounding on halves and two currencies in one operation:
each answers what the Go VM answers.

## Building and testing

Node 24 and npm, from the root of the repository:

```sh
make vmtest     # npm ci, type check, build into web/vm/dist, every case through the built file
```

The build writes `dist/fadodb-vm.js`, an ES module,
`dist/fadodb-vm.iife.js`, a classic script that defines the global
`FadoDB`, both minified and held to 100 KB, and `dist/fadodb-vm.d.ts`,
the declarations of both, one file joined from the sources' own. None
is committed: a release builds them, checks them and attaches them,
with their checksums beside the binaries'.

`make vmtest` then checks the declarations, as a page written in
TypeScript would use them and against what the module exports, runs
every case, and measures the speed.

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
| `src/exact.ts` | exact numbers and money: a BigInt mantissa held to Go's 64 bits |
| `src/render.ts` | a value as text, the TEXT rule |
| `src/errors.ts` | the codes a formula stops with |
| `cases/main.go` | writes the conformance cases, compiled, for the runner, and the documents of the benchmark |
| `test/run.mjs` | runs them through the built file |
| `test/bench.mjs` | the recalculation budget, on the built file |
| `test/consumer.ts`, `test/surface.mjs` | the declarations, as a page uses them and against the module |
| `build.mjs` | the build, the declarations and the size budget |
