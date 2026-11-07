# ADR-0029: The browser VM ships as one plain JavaScript file

- Status: Proposed; amends ADR-0003
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
