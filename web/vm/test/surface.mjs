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

// The package's major version is the bytecode format the VM reads.
const { FORMAT_VERSION } = await import(join(dist, "fadodb-vm.js"));
const version = JSON.parse(readFileSync(join(dist, "package.json"), "utf8")).version;
if (Number(version.split(".")[0]) !== FORMAT_VERSION) {
  console.error(`the package is version ${version}, and the VM reads bytecode format ${FORMAT_VERSION}: the major version is the format`);
  process.exit(1);
}
console.log(`package      fadodb-vm ${version}, for bytecode format ${FORMAT_VERSION}`);
