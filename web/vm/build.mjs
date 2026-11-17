// Builds the browser VM as ADR-0029 says: one ES module, fadodb-vm.js, and
// one classic script that defines the global FadoDB, fadodb-vm.iife.js,
// each minified, with no runtime dependency, and each held to the size
// budget of 100 KB before compression. The files go to vm/dist, which is
// never committed.

import { build } from "esbuild";
import { statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const dist = join(here, "dist");
const budget = 100 * 1024;

const common = {
  entryPoints: [join(here, "src", "index.ts")],
  bundle: true,
  minify: true,
  target: "es2020",
  legalComments: "none",
  logLevel: "warning",
};

await build({ ...common, format: "esm", outfile: join(dist, "fadodb-vm.js") });
await build({ ...common, format: "iife", globalName: "FadoDB", outfile: join(dist, "fadodb-vm.iife.js") });

let over = false;
for (const name of ["fadodb-vm.js", "fadodb-vm.iife.js"]) {
  const size = statSync(join(dist, name)).size;
  const line = `${name}: ${size} bytes of the ${budget} the budget allows (ADR-0029)`;
  if (size > budget) {
    console.error("vm build: " + line);
    over = true;
  } else {
    console.log("vm build: " + line);
  }
}
if (over) {
  process.exit(1);
}
