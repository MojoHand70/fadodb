// Holds the declarations to the module: every value dist/fadodb-vm.d.ts
// says is exported, the built module exports, and nothing more.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dist = join(dirname(fileURLToPath(import.meta.url)), "..", "dist");
const exported = Object.keys(await import(join(dist, "fadodb-vm.js"))).sort();
const declared = [...readFileSync(join(dist, "fadodb-vm.d.ts"), "utf8").matchAll(/^export declare (?:const|function|class) ([A-Za-z_$][\w$]*)/gm)].map((m) => m[1]).sort();
if (JSON.stringify(exported) !== JSON.stringify(declared)) {
  console.error("the module exports " + exported.join(" "));
  console.error("the declarations   " + declared.join(" "));
  process.exit(1);
}
console.log(`surface      the declarations and the module export the same ${exported.length} values`);
