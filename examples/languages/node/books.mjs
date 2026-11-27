// The story of the chapter "In a program of another language", in
// Node, in the same process (docs/manual/other-languages.md). Node calls
// a library of C through a package, koffi; nothing else is needed. Run
// it in a folder with invoice.json and nothing else:
//
//     npm install koffi
//     FADODB_LIBRARY=/path/to/libfadodb-core.so node books.mjs
import { readFileSync } from "node:fs";
import koffi from "koffi";

const lib = koffi.load(process.env.FADODB_LIBRARY);
// An answer is kept as a pointer, so that it is given back with fado_free.
const fado_open = lib.func("uint64_t fado_open(const char *path, const char *options, _Out_ void **refusal)");
const fado_exec = lib.func("void *fado_exec(uint64_t db, const char *caller, const char *command, const char *object)");
const fado_close = lib.func("int fado_close(uint64_t db, void *refusal)");
const fado_free = lib.func("void fado_free(void *answer)");

// text reads an answer of the library and gives it back.
function text(pointer) {
  try {
    return koffi.decode(pointer, "char", -1);
  } finally {
    fado_free(pointer);
  }
}

// exec runs a command as the caller; a refusal is thrown with its code.
function exec(db, caller, command, object) {
  const answer = JSON.parse(text(fado_exec(db, JSON.stringify(caller), command, JSON.stringify(object))));
  if (answer.error) {
    throw Object.assign(new Error(answer.error.message), { code: answer.error.code });
  }
  return answer;
}

const invoice = JSON.parse(readFileSync("invoice.json", "utf8"));
const refusal = [null];
const db = fado_open("books.fado", "{}", refusal);
if (!db) {
  throw new Error(text(refusal[0]));
}
const dan = { id: "dan", kind: "person", system_groups: ["designers"] };
const ann = { id: "ann", kind: "person", groups: ["sales"] };
const bob = { id: "bob", kind: "person", groups: ["accounting"] };

exec(db, dan, "define_class", { definition: invoice });
console.log("dan defines the class Invoice");

const made = exec(db, ann, "create", { class: "Invoice", id: "FV/2026/11/0042", values: { number: "FV/2026/11/0042" } }).document;
console.log(`ann makes ${made.id}: ${made.state}`);

const book = { class: "Invoice", id: "FV/2026/11/0042", transition: "book" };
try {
  exec(db, bob, "transition", book);
} catch (refused) {
  console.log(`bob books it: refused, ${refused.code}`);
}

exec(db, bob, "set", { class: "Invoice", id: "FV/2026/11/0042", values: { account: "400-01" } });
console.log("bob gives the account 400-01");

const booked = exec(db, bob, "transition", book).document;
console.log(`bob books it: ${booked.state}`);

fado_close(db, null);
