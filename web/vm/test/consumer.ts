// A page's code, compiled against the declarations alone: it holds
// dist/fadodb-vm.d.ts to being whole and usable, as a TypeScript page
// would use it. It is type-checked, never run.

import { BLANK, EvalError, Kind, VM, decimal, decode, money, number, render, same, text, type Context, type Program, type Value } from "../dist/fadodb-vm.js";

declare const bytes: Uint8Array;

const program: Program = decode(bytes);
const ctx: Context = { user: "ann", groups: ["finance"], now: 1794992400, doc: { version: 3, states: [{ state: "draft", n: 1, here: true }] } };
const inputs: Value[] = [number(12), text("taxi"), decimal(1230n, 2), money(1230n, 2, "PLN"), BLANK];
try {
  const v: Value = new VM({ maxText: 65536 }).run(program, inputs, ctx);
  const shown: string = render(v);
  const isMoney: boolean = v.kind === Kind.Money && same(v, inputs[3]!);
  void shown;
  void isMoney;
} catch (e) {
  if (e instanceof EvalError) {
    const code: string = e.code;
    const at: number = e.pos;
    void code;
    void at;
  }
}
