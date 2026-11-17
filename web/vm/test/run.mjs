// Runs the evaluation cases of the conformance suite through the built
// browser VM, in Node, as ADR-0029 asks: the same cases the Go VM and the
// SQL generator pass. The cases come compiled by the engine, written by
// go run ./web/vm/cases, so the file under test is given bytecode as a
// page is.
//
//   node vm/test/run.mjs cases.json
//
// A case passes when the VM answers the value the engine answers, of the
// same kind and the same exactness, or stops with the same code. One
// whose program reads other documents is the server's (ADR-0003).

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const vm = await import(join(here, "..", "dist", "fadodb-vm.js"));
const { VM, decode, builtinNames, same, render } = vm;

const files = process.argv.slice(2);
const input = files.length > 0 ? readFileSync(files[0], "utf8") : readFileSync(0, "utf8");
const cases = JSON.parse(input);

let bad = 0;
const table = builtinNames();
if (JSON.stringify(table) !== JSON.stringify(cases.builtins)) {
  console.error("the table of functions differs from the engine's:");
  console.error("  the engine: " + cases.builtins.join(" "));
  console.error("  the VM:     " + table.join(" "));
  process.exit(1);
}

/** A value of the typed form the cases are written in. */
function valueOf(t) {
  switch (t.k) {
    case "blank":
      return vm.BLANK;
    case "bool":
      return vm.bool(t.n !== 0);
    case "number":
      return vm.number(t.n);
    case "date":
      return vm.date(t.n);
    case "datetime":
      return vm.datetime(t.n);
    case "text":
      return vm.text(t.s);
    case "ref":
      return vm.ref(t.s);
    case "json":
      return vm.json(t.s);
    case "decimal":
      return vm.decimal(BigInt(t.m), t.sc);
    case "money":
      return vm.money(BigInt(t.m), t.sc, t.c);
  }
  throw new Error("a value of kind " + t.k);
}

function shown(v) {
  return vm.kindNames[v.kind] + " " + JSON.stringify(render(v));
}

const machines = new Map();
const totals = { passed: 0, failed: 0, server: 0 };
const failures = [];

for (const suite of cases.suites) {
  const counts = { passed: 0, failed: 0, server: 0 };
  for (const c of suite.cases) {
    if (c.server) {
      counts.server++;
      continue;
    }
    let machine = machines.get(c.max_text);
    if (!machine) {
      machine = new VM({ maxText: c.max_text });
      machines.set(c.max_text, machine);
    }
    const program = decode(Buffer.from(c.program, "base64"));
    const inputs = (c.inputs ?? []).map(valueOf);
    let got;
    let stopped;
    try {
      got = machine.run(program, inputs, c.context);
    } catch (e) {
      if (!(e instanceof vm.EvalError)) {
        throw e;
      }
      stopped = e;
    }
    let wrong = null;
    if (c.error) {
      if (!stopped) wrong = `got ${shown(got)}, want the error ${c.error}`;
      else if (stopped.code !== c.error) wrong = `got the error ${stopped.code} (${stopped.detail}), want ${c.error}`;
    } else if (stopped) {
      wrong = `got the error ${stopped.code} (${stopped.detail}), want ${shown(valueOf(c.expected))}`;
    } else if (!same(got, valueOf(c.expected))) {
      wrong = `got ${shown(got)}, want ${shown(valueOf(c.expected))}`;
    }
    if (wrong) {
      counts.failed++;
      failures.push(`${c.label}\n    ${wrong}`);
    } else {
      counts.passed++;
    }
  }
  for (const k of Object.keys(totals)) totals[k] += counts[k];
  console.log(`${suite.suite.padEnd(12)} passed ${String(counts.passed).padStart(5)}   failed ${String(counts.failed).padStart(3)}   server ${String(counts.server).padStart(3)}`);
}

// Every number the engine rendered, rendered here.
let renderedWrong = 0;
for (const r of cases.renders) {
  const got = render(vm.number(r.n));
  if (got !== r.s) {
    if (renderedWrong < 20) failures.push(`the number ${r.n} renders as ${JSON.stringify(got)}, and the engine renders it as ${JSON.stringify(r.s)}`);
    renderedWrong++;
  }
}
console.log(`${"renders".padEnd(12)} passed ${String(cases.renders.length - renderedWrong).padStart(5)}   failed ${String(renderedWrong).padStart(3)}`);

console.log(`${"all".padEnd(12)} passed ${String(totals.passed).padStart(5)}   failed ${String(totals.failed).padStart(3)}   server ${String(totals.server).padStart(3)}`);
if (failures.length > 0) {
  console.error(`\n${failures.length} cases fail:`);
  for (const f of failures.slice(0, 80)) console.error(f);
  bad = 1;
}
process.exit(bad);
