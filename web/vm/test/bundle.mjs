// The proof of the bundle (ADR-0070, item 12): a page given the bundle
// of a class, the person from whoami and the document from get works out
// what the server works out, with the built browser VM, in Node. The
// stories come from go run ./web/vm/bundle, which played them against
// the engine: each set a person makes and the values the server's
// preview answers, each new document and its defaults, and what each
// person may set, read, move and delete in each state.
//
//   node vm/test/bundle.mjs bundle.json
//
// The page here reads nothing but the bundle, the person and the
// document; it is the smallest page that follows the chapter on the
// bundle, and every face's logic is held to it.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const vm = await import(join(here, "..", "dist", "fadodb-vm.js"));
const { VM, decode, Kind, render, parseType, FORMAT_VERSION } = vm;

const files = process.argv.slice(2);
const input = files.length > 0 ? readFileSync(files[0], "utf8") : readFileSync(0, "utf8");
const { stories } = JSON.parse(input);

let checks = 0;
let bad = 0;
function fail(story, what, got, want) {
  bad++;
  console.error(`${story}: ${what}: the page works out ${JSON.stringify(got)}, the server ${JSON.stringify(want)}`);
}

// --- Values as the wire carries them, and as the VM holds them.

const DAY = 86400000;

function exact(text) {
  const s = String(text);
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(s);
  if (!m) throw new Error(`not an exact value: ${s}`);
  const frac = m[3] ?? "";
  return { mant: BigInt(m[1] + m[2] + frac), scale: frac.length };
}

/** A value of the wire as the VM reads it, by the type of the slot. */
function fromWire(w, type) {
  if (w === null || w === undefined) return vm.BLANK;
  switch (type.kind) {
    case Kind.Bool:
      return vm.bool(w);
    case Kind.Number:
      return vm.number(w);
    case Kind.Text:
      return vm.text(w);
    case Kind.Ref:
      return vm.ref(w);
    case Kind.JSON:
      return vm.json(JSON.stringify(w));
    case Kind.Date: {
      const [y, m, d] = w.split("-").map(Number);
      return vm.date(Date.UTC(y, m - 1, d) / DAY);
    }
    case Kind.DateTime:
      return vm.datetime(w);
    case Kind.Decimal: {
      const e = exact(w);
      return vm.decimal(e.mant, e.scale);
    }
    case Kind.Money: {
      const [amount, currency] = String(w).split(" ");
      const e = exact(amount);
      return vm.money(e.mant, e.scale, currency);
    }
  }
  throw new Error(`a value of kind ${type.kind} is not read by this page`);
}

function digits(mant, scale) {
  const neg = mant < 0n;
  let s = (neg ? -mant : mant).toString().padStart(scale + 1, "0");
  if (scale > 0) s = s.slice(0, s.length - scale) + "." + s.slice(s.length - scale);
  return (neg ? "-" : "") + s;
}

/** A value of the VM as the wire carries it. */
function toWire(v) {
  switch (v.kind) {
    case Kind.Blank:
      return null;
    case Kind.Bool:
      return v.num !== 0;
    case Kind.Number:
    case Kind.DateTime:
      return v.num;
    case Kind.Text:
    case Kind.Ref:
      return v.str;
    case Kind.Date:
      return render(v);
    case Kind.Decimal:
      return digits(v.mant, v.scale);
    case Kind.Money:
      return digits(v.mant, v.scale) + " " + v.str;
  }
  throw new Error(`a value of kind ${v.kind} is not written by this page`);
}

/** A worked-out value at the scale of its variable, as the server keeps it. */
function fit(v, typeText) {
  if (!typeText || (v.kind !== Kind.Decimal && v.kind !== Kind.Money)) return v;
  const t = parseType(typeText);
  if (v.scale === t.scale) return v;
  if (v.scale > t.scale) throw new Error(`${toWire(v)} needs rounding to ${typeText}, which this page does not do`);
  const mant = v.mant * 10n ** BigInt(t.scale - v.scale);
  return v.kind === Kind.Money ? vm.money(mant, t.scale, v.str) : vm.decimal(mant, t.scale);
}

// --- The page.

class Page {
  constructor(bundle, person) {
    if (bundle.bytecode !== FORMAT_VERSION) {
      throw new Error(`the bundle is of bytecode ${bundle.bytecode}, and this VM runs ${FORMAT_VERSION}`);
    }
    this.b = bundle;
    this.person = person;
    this.vm = new VM();
    this.programs = new Map();
  }

  program(b64) {
    let p = this.programs.get(b64);
    if (!p) {
      p = decode(new Uint8Array(Buffer.from(b64, "base64")));
      this.programs.set(b64, p);
    }
    return p;
  }

  context(doc, now) {
    const p = this.person;
    const ctx = { user: p.user, groups: p.groups, system: p.system, kind: p.kind, now: now ?? Math.floor(Date.now() / 1000) };
    if (doc) {
      ctx.state = doc.state;
      ctx.id = doc.id;
      ctx.doc = {
        version: doc.version,
        created_at: doc.created_at, created_by: doc.created_by,
        updated_at: doc.updated_at, updated_by: doc.updated_by,
        entered_at: doc.state_entered_at, entered_by: doc.state_entered_by, entered_from: doc.state_entered_from,
      };
    }
    return ctx;
  }

  /** Runs a compiled formula over the values, as the wire carries them. */
  run(b64, values, ctx) {
    if (typeof b64 !== "string") throw new Error("the formula is the server's");
    const p = this.program(b64);
    const vars = p.slots.map((slot) => {
      let w = values;
      for (const part of slot.name.split(".")) w = w?.[part];
      return fromWire(w, slot.type);
    });
    return this.vm.run(p, vars, ctx);
  }

  holds(b64, values, ctx) {
    return b64 !== undefined && this.run(b64, values, ctx).truth;
  }

  /** Works out the computed variables and the title, in order. */
  recalc(values, ctx, id) {
    for (const f of this.b.compiled.computed) {
      if (f.server) continue;
      values[f.variable] = toWire(fit(this.run(f.formula, values, ctx), f.type));
    }
    const t = this.b.compiled.title;
    return t ? render(this.run(t.formula, values, ctx)) : id;
  }

  /** The rule of a right, in a state, for the person; null when none. */
  right(state, variable, which) {
    return this.b.compiled.rights[state]?.[variable]?.[which] ?? null;
  }

  allowed(rule, values, ctx) {
    if (rule.groups) return rule.groups.some((g) => this.person.groups.includes(g));
    return this.holds(rule.formula, values, ctx);
  }
}

function same(a, b) {
  return JSON.stringify(a) === JSON.stringify(b);
}

for (const story of stories) {
  const b = JSON.parse(JSON.stringify(story.bundle));
  const def = b.definition;
  const finals = def.states?.final ?? [];
  const page = (who) => new Page(b, story.people[who]);

  // A set: the page works out the computed variables and the title.
  for (const c of story.compute ?? []) {
    const p = page(c.person);
    const values = { ...c.doc.values, ...c.set };
    const title = p.recalc(values, p.context(c.doc), c.doc.id);
    for (const f of b.compiled.computed) {
      checks++;
      if (!same(values[f.variable], c.expect.values[f.variable])) fail(story.name, `${c.person} sets ${JSON.stringify(c.set)}: ${f.variable}`, values[f.variable], c.expect.values[f.variable]);
    }
    checks++;
    if (title !== c.expect.title) fail(story.name, `${c.person} sets ${JSON.stringify(c.set)}: the title`, title, c.expect.title);
  }

  // A new document: the defaults, then the computed variables.
  for (const d of story.defaults ?? []) {
    const p = page(d.person);
    const values = { ...d.given };
    const ctx = p.context(null, d.now);
    ctx.state = def.states?.initial;
    for (const f of b.compiled.defaults) {
      if (f.variable in d.given || f.server) continue;
      values[f.variable] = toWire(fit(p.run(f.formula, values, ctx), f.type));
    }
    const title = p.recalc(values, ctx, "");
    for (const name of [...b.compiled.defaults, ...b.compiled.computed].map((f) => f.variable)) {
      checks++;
      if (!same(values[name], d.expect.values[name])) fail(story.name, `a new document at ${d.now}: ${name}`, values[name], d.expect.values[name]);
    }
    checks++;
    if (title !== d.expect.title) fail(story.name, `a new document at ${d.now}: the title`, title, d.expect.title);
  }

  // What each person may do, in each state.
  for (const m of story.may ?? []) {
    const p = page(m.person);
    const doc = m.doc;
    const ctx = p.context(doc);
    const access = b.compiled.access ?? {};
    const final = finals.includes(doc.state);
    const update = p.holds(access.update, doc.values, ctx);
    const where = `${m.person} in ${doc.state}`;
    for (const v of Object.keys(m.set)) {
      const write = p.right(doc.state, v, "write");
      const set = !final && update && (write === null || p.allowed(write, doc.values, ctx));
      checks++;
      if (set !== m.set[v]) fail(story.name, `${where} sets ${v}`, set, m.set[v]);
      const read = p.right(doc.state, v, "read");
      const reads = read === null || p.allowed(read, doc.values, ctx);
      checks++;
      if (reads !== m.read[v]) fail(story.name, `${where} reads ${v}`, reads, m.read[v]);
    }
    const moves = {};
    for (const t of def.transitions ?? []) {
      const from = Array.isArray(t.from) ? t.from : [t.from];
      if (!t.name || !(from.includes(doc.state) || from.includes("*"))) continue;
      const rule = access.transition?.[t.name] ?? access.update;
      const guard = b.compiled.guards[t.name];
      moves[t.name] = p.holds(rule, doc.values, ctx) && (guard === undefined || p.holds(guard, doc.values, ctx));
    }
    checks++;
    if (!same(moves, m.transitions)) fail(story.name, `${where}: the transitions`, moves, m.transitions);
    const del = p.holds(access.delete, doc.values, ctx);
    checks++;
    if (del !== m.delete) fail(story.name, `${where} deletes`, del, m.delete);
  }
}

if (bad > 0) {
  console.error(`the bundle: ${bad} of ${checks} answers differ from the server's`);
  process.exit(1);
}
console.log(`the bundle: ${checks} answers of ${stories.length} stories worked out in the page, each the server's`);
