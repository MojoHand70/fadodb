// fadodb-vm: the formula machine of FadoDB for a page (ADR-0003, ADR-0029).
// A page is given a document's formulas compiled by the engine, the
// bytecode, and works them out itself as the person types, with the same
// answers as the server. It never parses a formula.
//
//   import { decode, VM, number } from "./fadodb-vm.js";
//   const program = decode(bytes);
//   const total = new VM().run(program, [number(12), number(3)]);
//
// What this file exports is a public surface: it keeps the compatibility
// promise of the bytecode from 1.0 on.

import { builtins } from "./builtins.js";

export { decode, parseType, FORMAT_VERSION, type Program, type Slot, type Type } from "./bytecode.js";
export { EvalError, ErrorCode } from "./errors.js";
export { render } from "./render.js";
export { VM, DEFAULT_MAX_TEXT, type Context, type Life, type Options, type StateLife } from "./vm.js";
export { BLANK, FALSE, Kind, TRUE, Value, bool, date, datetime, decimal, json, kindNames, money, number, ref, same, text, type KindCode } from "./value.js";

/** The names of the VM's functions, in the order of the engine's table. */
export function builtinNames(): string[] {
  return builtins.map((b) => b.name);
}
