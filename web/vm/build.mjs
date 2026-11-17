// Builds the browser VM as ADR-0029 says: one ES module, fadodb-vm.js, and
// one classic script that defines the global FadoDB, fadodb-vm.iife.js,
// each minified, with no runtime dependency, and each held to the size
// budget of 100 KB before compression. The files go to vm/dist, which is
// never committed.

import { build } from "esbuild";
import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
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

// The declarations, in one file beside the two (ADR-0029). TypeScript
// writes one file of declarations for each source; they are joined here,
// the public names exported, and of the rest only what a public one
// refers to kept, without export.
const types = join(dist, "types");
rmSync(types, { recursive: true, force: true });
execFileSync(join(here, "..", "node_modules", ".bin", "tsc"), ["-p", join(here, "tsconfig.json"), "--noEmit", "false", "--declaration", "--emitDeclarationOnly", "--rootDir", join(here, "src"), "--outDir", types], { stdio: "inherit" });
writeFileSync(join(dist, "fadodb-vm.d.ts"), joinDeclarations(types));
rmSync(types, { recursive: true, force: true });

/** One file of declarations out of the files TypeScript wrote. */
function joinDeclarations(dir) {
  const declaration = /^(?:export )?(?:declare )?(?:abstract )?(?:class|interface|type|const|function|enum) ([A-Za-z_$][\w$]*)/;
  const chunks = new Map();
  const order = [];
  const publicNames = new Set();
  for (const file of readdirSync(dir).sort()) {
    let doc = [];
    let inDoc = false;
    let current = null;
    for (const line of readFileSync(join(dir, file), "utf8").split("\n")) {
      if (inDoc || line.startsWith("/**")) {
        // A doc comment belongs to the declaration that follows it.
        doc.push(line);
        inDoc = !line.includes("*/");
        current = null;
        continue;
      }
      if (line.startsWith("import ") || line.startsWith("export {")) {
        // What index.ts exports from the other files is public.
        if (file === "index.d.ts") {
          const m = /^export \{([^}]*)\}/.exec(line);
          if (m) for (const part of m[1].split(",")) if (part.trim()) publicNames.add(part.trim().replace(/^type /, ""));
        }
        doc = [];
        current = null;
        continue;
      }
      const m = declaration.exec(line);
      if (m) {
        current = m[1];
        if (file === "index.d.ts") publicNames.add(current);
        if (!chunks.has(current)) {
          order.push(current);
          chunks.set(current, []);
        }
        chunks.get(current).push(...doc, line);
        doc = [];
        continue;
      }
      if (current !== null) chunks.get(current).push(line);
    }
  }
  // What the public names refer to, and what that refers to.
  const keep = new Set(publicNames);
  for (let grew = true; grew; ) {
    grew = false;
    for (const name of [...keep]) {
      const text = (chunks.get(name) ?? []).join("\n");
      for (const other of chunks.keys()) {
        if (!keep.has(other) && new RegExp("\\b" + other.replace(/\$/g, "\\$") + "\\b").test(text)) {
          keep.add(other);
          grew = true;
        }
      }
    }
  }
  for (const name of publicNames) {
    if (!chunks.has(name)) throw new Error("index.ts exports " + name + ", which no file of declarations declares");
  }
  let out = "// fadodb-vm: the formula machine of FadoDB for a page (ADR-0029).\n// Generated from the TypeScript sources by web/vm/build.mjs.\n\n";
  for (const name of order) {
    if (!keep.has(name)) continue;
    let text = chunks.get(name).join("\n").trimEnd();
    if (!publicNames.has(name)) text = text.replace(/^export /gm, "");
    out += text + "\n\n";
  }
  return out.trimEnd() + "\nexport {};\n";
}

// What npm publishes: the three files, with the package's description,
// a README for npmjs.com and the licence, written beside them. The
// package's major version is the bytecode format version (ADR-0029, the
// owner's ruling of 2026-11-17).
writeFileSync(join(dist, "package.json"), readFileSync(join(here, "package.npm.json")));
writeFileSync(join(dist, "README.md"), readFileSync(join(here, "npm-readme.md")));
writeFileSync(join(dist, "LICENSE"), readFileSync(join(here, "..", "..", "LICENSE")));

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
