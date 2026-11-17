// The recalculation budget of ADR-0003, measured on the built file as
// ADR-0029 asks: a document of 200 formulas, each reading an input and
// the formula before it, recalculated whole, in the order of its
// dependencies, as a page recalculates it when a value changes. The same
// document with decimals and with money in place of numbers is measured
// beside it; it has no budget of its own, and its numbers are published.
//
//   node vm/test/bench.mjs cases.json
//
// Every recalculation is checked against the value of each formula as the
// Go VM works it out. The run fails when the document of plain numbers
// takes 1 ms or more at the median.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { performance } from "node:perf_hooks";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const vm = await import(join(here, "..", "dist", "fadodb-vm.js"));
const cases = JSON.parse(readFileSync(process.argv[2] ?? 0, "utf8"));

const BUDGET_US = 1000;
const BATCHES = 60;
const PER_BATCH = 100;

function valueOf(t) {
  switch (t.k) {
    case "number":
      return vm.number(t.n);
    case "decimal":
      return vm.decimal(BigInt(t.m), t.sc);
    case "money":
      return vm.money(BigInt(t.m), t.sc, t.c);
  }
  throw new Error("a value of kind " + t.k);
}

let bad = 0;
for (const doc of cases.bench) {
  // Every value of the document in one array: the inputs, then each
  // formula's result, so that a formula reads its slots by position.
  const names = Object.keys(doc.inputs);
  const values = names.map((n) => valueOf(doc.inputs[n]));
  const index = new Map(names.map((n, i) => [n, i]));
  const formulas = doc.formulas.map((f) => {
    index.set(f.name, values.length);
    values.push(vm.BLANK);
    const program = vm.decode(Buffer.from(f.program, "base64"));
    return { program, reads: f.reads.map((r) => index.get(r)), vars: new Array(f.reads.length), at: index.get(f.name), want: valueOf(f.value) };
  });
  const machine = new vm.VM();
  const recalc = () => {
    for (const f of formulas) {
      for (let i = 0; i < f.reads.length; i++) f.vars[i] = values[f.reads[i]];
      values[f.at] = machine.run(f.program, f.vars);
    }
  };

  recalc();
  for (const f of formulas) {
    if (!vm.same(values[f.at], f.want)) {
      console.error(`${doc.name}: ${vm.render(values[f.at])}, and the Go VM works out ${vm.render(f.want)}`);
      bad = 1;
    }
  }
  // Warm up, then batches of recalculations; the median of the batches.
  for (let i = 0; i < 2000; i++) recalc();
  const times = [];
  for (let b = 0; b < BATCHES; b++) {
    const start = performance.now();
    for (let i = 0; i < PER_BATCH; i++) recalc();
    times.push(((performance.now() - start) * 1000) / PER_BATCH);
  }
  times.sort((x, y) => x - y);
  const median = times[Math.floor(times.length / 2)];
  const slowest = times[times.length - 1];
  let line = `${doc.name.padEnd(8)} 200 formulas recalculated in ${median.toFixed(1)} µs at the median, ${slowest.toFixed(1)} µs in the slowest batch`;
  if (doc.name === "number") {
    line += `; the budget is ${BUDGET_US} µs (ADR-0003)`;
    if (median >= BUDGET_US) bad = 1;
  }
  console.log(line);
}
process.exit(bad);
