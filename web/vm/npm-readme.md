# fadodb-vm

The formula machine of [FadoDB](https://fadodb.org) for a page. A page
that shows a document works its formulas out itself, as the person
types, with the same answers the server gives. It is given each formula
compiled by the engine, the bytecode, and runs it; it never parses a
formula.

```js
import { decode, VM, number } from "fadodb-vm";

const program = decode(bytes); // the bytecode, a Uint8Array, from the server
const value = new VM().run(program, [number(12), number(3)], { user: "ann" });
```

Or with a script tag, which defines the global `FadoDB`:

```html
<script src="https://cdn.jsdelivr.net/npm/fadodb-vm@4/fadodb-vm.iife.js"></script>
```

- **One file, no dependency**: an ES module, a classic script and the
  declarations for TypeScript, each well under 100 KB.
- **The server's answers**: plain numbers, text, logic, dates and
  moments, exact numbers and money on `BigInt`. Every case of the
  engine's conformance suite that a page runs passes, on every push.
- **Fast**: a document of 200 formulas recalculates in about 13
  microseconds.
- **The version follows the bytecode**: 4.x runs the programs of
  bytecode format 4. A page pins the major version to the format its
  server writes, and the VM refuses a program of another format.

A formula that reads other documents is worked out by the server, and
the page is given its last value.

Apache License 2.0. The source, the decisions and the tests are at
[github.com/MojoHand70/fadodb](https://github.com/MojoHand70/fadodb/tree/main/web/vm).
