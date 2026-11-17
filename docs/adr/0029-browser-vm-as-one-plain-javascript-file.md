# ADR-0029: The browser VM ships as one plain JavaScript file

- Status: Accepted; amends ADR-0003
- Date: 2026-11-06

## Context

ADR-0003 makes the browser VM one of the three consumers of the bytecode,
written in TypeScript, and says what it executes. It does not say how it
reaches a page. A TypeScript package that needs a bundler to use is of no
help to a frontend that has none.

The first embedder's frontend is vanilla JavaScript with no framework and no
build step, and it is not alone: many internal tools, admin pages and
embedded widgets are a page and a script tag. The promise of the bundle, that
a frontend needs nothing else to render a document and evaluate it live,
is only kept when the VM loads as easily as the bundle.

ADR-0022 adds `decimal`, `int` and `money` as exact types, and ADR-0023 makes
`date` a calendar day. A JavaScript `number` is a float64 and cannot carry
them. The browser VM must pick its representations before the first release
that ships it, because they are part of the conformance suite.

## Decision

- Phase 7 ships the browser VM built: `fadodb-vm.js` as an ES module and
  `fadodb-vm.iife.js` as a classic script that defines a global `FadoDB`,
  with `fadodb-vm.d.ts` beside them. They are built from the TypeScript
  sources with no runtime dependency, and they load from a script tag with
  no bundler and no build step.
- The files are attached to every GitHub release and published on npm as
  `fadodb-vm`. The version follows the bytecode format version.
- `decimal`, `int` and `money` values of ADR-0022 use `BigInt` in the
  browser VM. `date` is an integer day and `datetime` is integer seconds,
  as ADR-0023 defines them.
- The conformance suite runs on the built file in Node in CI. The suite's
  cases are published so that a frontend author can run them in a browser.
- Size budget: 100 KB minified before compression. The size gate of
  ADR-0012 gains this file.

## Consequences

- No runtime dependency means the VM carries its own exact arithmetic over
  `BigInt`, including rounding with the six methods of ADR-0022. That is
  code to write and to keep inside the size budget; it is also the only way
  the file stays one file.
- `BigInt` is slower than `number`. The recalculation budget of ADR-0003
  stands for `number` formulas, and the exact types are measured against it
  separately. A page that mixes the two pays for the exact part only.
- `BigInt` needs a browser from 2020 or later. Older browsers are not
  supported, and the documentation names the floor.
- A VM release per bytecode version means a frontend pins the file to the
  bytecode version its server emits. The bundle carries that version, and
  the VM refuses a bundle it cannot execute instead of guessing.
- The global `FadoDB` and the exports of the module are a public surface.
  They get the same compatibility promise as the bytecode at 1.0.
- The release pipeline gains an npm publish step with its own credential.
  The package has no dependencies, so an audit of it is an audit of the one
  file.
- The published cases let a frontend author prove a port or a patched build
  in the browser they ship to, which is the same proof CI runs in Node.
- The TypeScript client generated from OpenAPI is unchanged by this record.
  It talks to the server; the VM evaluates bytecode. A page may use either
  without the other.

## Performance

Budget: 100 KB minified before compression, enforced by the size gate of
ADR-0012 on every pull request. The browser VM recalculation budget of
ADR-0003, 200 formulas in under 1 ms, stands for `number` formulas and is
measured on the built file, not on the sources. The `BigInt` path is
benchmarked in Phase 7 on the same 200-formula document with `decimal` and
`money` in place of `number`, and the result is published with the Phase 7
release.

## Weighed on 2026-11-17: the Go VM compiled to WebAssembly

The owner asked, as Phase 7 opened, whether WebAssembly matters to it.
One engine in place of two would spare writing the functions again, so
it was measured that day with Go 1.26.11 (`GOOS=js GOARCH=wasm`, built
with `-s -w`): `internal/formula` with `Decode` and `VM.Run` came to
4.5 MB, 1.2 MB compressed with gzip; an empty Go program alone to
1.8 MB, 0.54 MB compressed, which is Go's runtime. The budget above is
100 KB. TinyGo was not measured: the formula package carries the
compiler, the SQL generator, `database/sql`, `regexp` and
`encoding/json`, and the VM would first have to be split out of it.
WebAssembly also needs a loader where a script tag would do, a page
with a strict content security policy must allow `'wasm-unsafe-eval'`,
and values are copied across the boundary.

The owner ruled the same day: keep TypeScript. This record stands as
written; the first part of the browser VM, built on 2026-11-17, is
24.7 KB.

## Measured on 2026-11-17

The four parts of the browser VM built, the budgets above were measured
on the built file, `fadodb-vm.js`, in Node 24 on the machine every
campaign of this project runs on (`web/vm/test/bench.mjs`, run by
`make vmtest`). The document is the one of the Go recalculation
benchmark grown to 200 formulas, each reading an input and the formula
before it, recalculated whole in the order of its dependencies; the
exact documents round each formula to two places, so that the chain
keeps its scale. Every recalculation is held to the values the Go VM
works out. Three runs, the median of 60 batches of 100
recalculations:

| The document of 200 formulas | Run 1 | Run 2 | Run 3 | The budget |
|---|---|---|---|---|
| plain numbers | 13.0 µs | 13.3 µs | 12.8 µs | 1,000 µs (ADR-0003) |
| decimals, on `BigInt` | 41.0 µs | 42.0 µs | 41.5 µs | none; measured as this record asks |
| money, on `BigInt` | 43.4 µs | 44.8 µs | 43.6 µs | none; measured as this record asks |

The exact path costs a little more than three times the plain one, and
both are far inside the budget. The size: 34.7 KB for the ES module and
35.2 KB for the classic script, of 100 KB. `make vmtest` fails when the
plain document reaches the budget or a file the size.

## The owner's rulings of 2026-11-17: npm

The name `fadodb-vm` was free on npm. The owner ruled:

1. **Published on npm, as this record says.** The owner's npm account
   holds the name, and the release workflow publishes each new version
   by itself, through npm's trusted publishing, so that no token is
   kept anywhere. npm asks that a new package be published once by
   hand, which the owner does for 4.0.0.
2. **The version follows the bytecode format version** as this record
   says, read as the major version: the first is 4.0.0, for format 4.
   The other way was the engine's version, 0.7.0, which says nothing
   of which programs the VM runs.
