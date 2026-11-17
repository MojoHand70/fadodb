// The functions of the VM, in the order of the Go VM's table
// (internal/formula/builtins.go). The operand of a call is the position
// in this table, so the order is part of the bytecode: an entry is never
// moved or removed, and the test holds this table to the engine's list.
//
// A function reads its arguments from the stack, s[a] to s[a+n-1], and
// answers a value or the error the formula stops with. The binder has
// checked every argument's type, so a function trusts what it is given.

import { EvalError, ErrorCode, NotBuilt, errModByZero, errNotANumber, outOfRange, tooLong } from "./errors.js";
import { log10, ln, pow, roundTo, scaledStep } from "./numbers.js";
import { render, renderNumber } from "./render.js";
import { compareText, longerThan, lower, proper, runeCount, runes, trimSpace, upper, utf8Bytes } from "./text.js";
import { BLANK, Kind, Value, bool, date, datetime, isExact, number, text } from "./value.js";
import type { VM } from "./vm.js";

/** A function: the VM, the stack, where its arguments begin and how many. */
export type Fn = (vm: VM, s: readonly Value[], a: number, n: number) => Value | EvalError;

/** One entry of the table: its name, and what runs it, or the part of Phase 7 that builds it. */
export interface Builtin {
  readonly name: string;
  readonly fn: Fn;
}

/** A finite number, or the error not_a_number. */
function finite(f: number): Value | EvalError {
  return Number.isFinite(f) ? number(f) : errNotANumber;
}

/** A function that a later part of Phase 7 builds. */
function later(name: string, what: string): Builtin {
  return {
    name,
    fn: () => {
      throw new NotBuilt(`${name}, ${what},`);
    },
  };
}

/** Whether any argument is an exact number; the exact paths come later. */
function anyExact(s: readonly Value[], a: number, n: number): boolean {
  for (let i = a; i < a + n; i++) {
    if (isExact(s[i]!.kind)) {
      return true;
    }
  }
  return false;
}

function plainOnly(name: string, s: readonly Value[], a: number, n: number): void {
  if (anyExact(s, a, n)) {
    throw new NotBuilt(`${name} of an exact number`);
  }
}

// ---- Numbers

const digitsArg = (s: readonly Value[], a: number, n: number): number => (n > 1 ? s[a + 1]!.num : 0);

function stepFn(name: string, step: (x: number) => number): Builtin {
  return {
    name,
    fn: (_vm, s, a, n) => {
      plainOnly(name, s, a, n);
      return finite(scaledStep(s[a]!.num, digitsArg(s, a, n), step));
    },
  };
}

function fold(name: string, s: readonly Value[], a: number, n: number, pick: (m: number, x: number) => boolean): Value {
  plainOnly(name, s, a, n);
  let m = 0;
  let any = false;
  for (let i = a; i < a + n; i++) {
    const v = s[i]!;
    if (v.kind === Kind.Blank) continue;
    if (!any || pick(m, v.num)) m = v.num;
    any = true;
  }
  return any ? number(m) : BLANK;
}

function sum(s: readonly Value[], a: number, n: number): [number, number] {
  let total = 0;
  let count = 0;
  for (let i = a; i < a + n; i++) {
    const v = s[i]!;
    if (v.kind === Kind.Blank) continue;
    count++;
    total += v.num;
  }
  return [total, count];
}

// ---- Text

/** A count argument: its whole part, refused when negative or not a number. */
function countArg(v: Value, what: string): number | EvalError {
  const n = Math.trunc(v.num);
  if (!(n >= 0)) {
    return outOfRange(what + " must not be negative");
  }
  return n;
}

const clamp = (n: number, limit: number): number => (n > limit ? limit : n);
const start0 = (pos: number, r: string[]): number => Math.min(pos, r.length + 1) - 1;

/** The position, from 1, of a match found at a UTF-16 index; 0 for none. */
function runePos(s: string, index: number): number {
  return index < 0 ? 0 : runeCount(s.slice(0, index)) + 1;
}

const numberSyntax = /^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$/;

function unreserved(c: number): boolean {
  return (c >= 0x61 && c <= 0x7a) || (c >= 0x41 && c <= 0x5a) || (c >= 0x30 && c <= 0x39) || c === 0x2d || c === 0x2e || c === 0x5f || c === 0x7e;
}

function arrayIndex(seg: string): number {
  if (!/^(0|[1-9][0-9]*)$/.test(seg)) return -1;
  const i = Number(seg);
  return Number.isSafeInteger(i) ? i : -1;
}

/**
 * A pattern of Go's RE2 syntax as a JavaScript expression: the leading
 * flags (?i), (?s) and (?m) become flags of the expression. Every match is
 * found from the start, as Go's FindAllStringIndex finds them, which skips
 * an empty match that touches the one before it.
 */
function regexOf(pattern: string): RegExp {
  let flags = "gu";
  let p = pattern;
  for (;;) {
    const m = /^\(\?([ims]+)\)/.exec(p);
    if (m === null) break;
    for (const f of m[1]!) {
      if (!flags.includes(f)) flags += f;
    }
    p = p.slice(m[0].length);
  }
  return new RegExp(p, flags);
}

// ---- Context

const ctxText = (v: string | undefined): Value => (v ? text(v) : BLANK);

/** The table, in the engine's order. */
export const builtins: readonly Builtin[] = [
  // Logic
  { name: "ISBLANK", fn: (_vm, s, a) => bool(s[a]!.kind === Kind.Blank) },
  { name: "ISNOTBLANK", fn: (_vm, s, a) => bool(s[a]!.kind !== Kind.Blank) },
  // Numeric, over either number family
  {
    name: "ROUND",
    fn: (_vm, s, a, n) => {
      if (isExact(s[a]!.kind) || n > 2) {
        throw new NotBuilt(n > 2 ? "ROUND with a method" : "ROUND of an exact number");
      }
      return finite(roundTo(s[a]!.num, digitsArg(s, a, n)));
    },
  },
  stepFn("FLOOR", Math.floor),
  stepFn("CEILING", Math.ceil),
  stepFn("TRUNC", Math.trunc),
  {
    name: "ABS",
    fn: (_vm, s, a, n) => {
      plainOnly("ABS", s, a, n);
      return number(Math.abs(s[a]!.num));
    },
  },
  {
    name: "SIGN",
    fn: (_vm, s, a, n) => {
      plainOnly("SIGN", s, a, n);
      const x = s[a]!.num;
      return number(x > 0 ? 1 : x < 0 ? -1 : 0);
    },
  },
  { name: "MIN", fn: (_vm, s, a, n) => fold("MIN", s, a, n, (m, x) => x < m) },
  { name: "MAX", fn: (_vm, s, a, n) => fold("MAX", s, a, n, (m, x) => x > m) },
  {
    name: "SUM",
    fn: (_vm, s, a, n) => {
      plainOnly("SUM", s, a, n);
      return finite(sum(s, a, n)[0]);
    },
  },
  {
    name: "AVERAGE",
    fn: (_vm, s, a, n) => {
      plainOnly("AVERAGE", s, a, n);
      const [total, count] = sum(s, a, n);
      return count === 0 ? BLANK : finite(total / count);
    },
  },
  {
    name: "COUNT",
    fn: (_vm, s, a, n) => {
      let c = 0;
      for (let i = a; i < a + n; i++) if (s[i]!.kind !== Kind.Blank) c++;
      return number(c);
    },
  },
  { name: "POWER", fn: (_vm, s, a) => finite(pow(s[a]!.num, s[a + 1]!.num)) },
  { name: "SQRT", fn: (_vm, s, a) => (s[a]!.num < 0 ? errNotANumber : number(Math.sqrt(s[a]!.num))) },
  { name: "EXP", fn: (_vm, s, a) => finite(Math.exp(s[a]!.num)) },
  { name: "LN", fn: (_vm, s, a) => (s[a]!.num <= 0 ? errNotANumber : number(ln(s[a]!.num))) },
  {
    name: "LOG",
    fn: (_vm, s, a, n) => {
      const x = s[a]!.num;
      const base = n > 1 ? s[a + 1]!.num : 10;
      if (x <= 0 || base <= 0 || base === 1) return errNotANumber;
      return base === 10 ? finite(log10(x)) : finite(ln(x) / ln(base));
    },
  },
  {
    name: "MOD",
    fn: (_vm, s, a, n) => {
      plainOnly("MOD", s, a, n);
      const x = s[a]!.num;
      const y = s[a + 1]!.num;
      if (y === 0) return errModByZero;
      return finite(x - y * Math.floor(x / y));
    },
  },
  // Dates: a date is a calendar day, a datetime an instant (ADR-0023)
  later("DATE", "a function of dates"),
  later("DATEVALUE", "a function of dates"),
  later("YEAR", "a function of dates"),
  later("MONTH", "a function of dates"),
  later("DAY", "a function of dates"),
  later("QUARTER", "a function of dates"),
  later("DAYS", "a function of dates"),
  later("DAYSBETWEEN", "a function of dates"),
  later("MONTHSBETWEEN", "a function of dates"),
  later("YEARSBETWEEN", "a function of dates"),
  later("ADDDAYS", "a function of dates"),
  later("ADDMONTHS", "a function of dates"),
  later("ADDYEARS", "a function of dates"),
  // Context
  { name: "NOW", fn: (vm) => datetime(vm.now()) },
  { name: "TODAY", fn: (vm) => date(Math.floor(vm.now() / 86400)) },
  { name: "USER", fn: (vm) => ctxText(vm.ctx?.user) },
  {
    name: "INGROUP",
    fn: (vm, s, a) => {
      const c = vm.ctx;
      const g = s[a]!.str;
      if (!c) return bool(false);
      return bool((c.groups?.includes(g) ?? false) || (c.system?.includes(g) ?? false) || (!!c.user && g === "everyone"));
    },
  },
  { name: "USERKIND", fn: (vm) => ctxText(vm.ctx?.kind) },
  { name: "ACTINGFOR", fn: (vm) => ctxText(vm.ctx?.for) },
  { name: "STATE", fn: (vm) => ctxText(vm.ctx?.state) },
  { name: "ID", fn: (vm) => ctxText(vm.ctx?.id) },
  // Text
  {
    name: "LEFT",
    fn: (_vm, s, a) => {
      const n = countArg(s[a + 1]!, "the count");
      if (n instanceof EvalError) return n;
      const r = runes(s[a]!.str);
      return text(r.slice(0, clamp(n, r.length)).join(""));
    },
  },
  {
    name: "RIGHT",
    fn: (_vm, s, a) => {
      const n = countArg(s[a + 1]!, "the count");
      if (n instanceof EvalError) return n;
      const r = runes(s[a]!.str);
      return text(r.slice(r.length - clamp(n, r.length)).join(""));
    },
  },
  {
    name: "MID",
    fn: (_vm, s, a, n) => {
      const pos = Math.trunc(s[a + 1]!.num);
      if (!(pos >= 1)) return outOfRange("the position must be 1 or more");
      const r = runes(s[a]!.str);
      const start = start0(pos, r);
      let end = r.length;
      if (n > 2) {
        const len = countArg(s[a + 2]!, "the length");
        if (len instanceof EvalError) return len;
        end = start + clamp(len, r.length - start);
      }
      return text(r.slice(start, end).join(""));
    },
  },
  { name: "LEN", fn: (_vm, s, a) => number(runeCount(s[a]!.str)) },
  {
    name: "TRIM",
    fn: (_vm, s, a) => {
      // Spaces only: none at the ends, one between words.
      let out = "";
      let space = true;
      for (const r of s[a]!.str) {
        if (r === " ") {
          if (!space) {
            out += " ";
            space = true;
          }
          continue;
        }
        out += r;
        space = false;
      }
      return text(out.endsWith(" ") ? out.slice(0, -1) : out);
    },
  },
  { name: "UPPER", fn: (_vm, s, a) => text(upper(s[a]!.str)) },
  { name: "LOWER", fn: (_vm, s, a) => text(lower(s[a]!.str)) },
  { name: "PROPER", fn: (_vm, s, a) => text(proper(s[a]!.str)) },
  {
    name: "REPLACE",
    fn: (vm, s, a) => {
      const pos = Math.trunc(s[a + 1]!.num);
      if (!(pos >= 1)) return outOfRange("the position must be 1 or more");
      const n = countArg(s[a + 2]!, "the length");
      if (n instanceof EvalError) return n;
      const r = runes(s[a]!.str);
      const start = start0(pos, r);
      const end = start + clamp(n, r.length - start);
      const repl = s[a + 3]!.str;
      if (r.length - (end - start) + runeCount(repl) > vm.maxText) return tooLong();
      return text(r.slice(0, start).join("") + repl + r.slice(end).join(""));
    },
  },
  {
    name: "SUBSTITUTE",
    fn: (vm, s, a) => {
      const t = s[a]!.str;
      const old = s[a + 1]!.str;
      const repl = s[a + 2]!.str;
      if (old === "") return text(t);
      const out = t.split(old).join(repl);
      if (longerThan(out, vm.maxText)) return tooLong();
      return text(out);
    },
  },
  {
    name: "CONCAT",
    fn: (vm, s, a, n) => {
      let out = "";
      for (let i = a; i < a + n; i++) out += render(s[i]!);
      if (longerThan(out, vm.maxText)) return tooLong();
      return text(out);
    },
  },
  {
    name: "REPEAT",
    fn: (vm, s, a) => {
      const n = countArg(s[a + 1]!, "the count");
      if (n instanceof EvalError) return n;
      const t = s[a]!.str;
      if (t === "" || n === 0) return text("");
      if (n * t.length > vm.maxText && n * runeCount(t) > vm.maxText) return tooLong();
      return text(t.repeat(n));
    },
  },
  { name: "FINDFIRST", fn: (_vm, s, a) => number(runePos(s[a + 1]!.str, s[a + 1]!.str.indexOf(s[a]!.str))) },
  { name: "FINDLAST", fn: (_vm, s, a) => number(runePos(s[a + 1]!.str, s[a + 1]!.str.lastIndexOf(s[a]!.str))) },
  {
    name: "PAD",
    fn: (vm, s, a, n) => {
      const w = countArg(s[a + 1]!, "the width");
      if (w instanceof EvalError) return w;
      if (w > vm.maxText) return tooLong();
      let fill = " ";
      if (n > 2 && s[a + 2]!.str !== "") fill = runes(s[a + 2]!.str)[0]!;
      const t = s[a]!.str;
      const have = runeCount(t);
      return text(have >= w ? t : fill.repeat(w - have) + t);
    },
  },
  {
    name: "REGEXREPLACE",
    fn: (vm, s, a) => {
      let re: RegExp;
      try {
        re = regexOf(s[a + 1]!.str);
      } catch (e) {
        return new EvalError(ErrorCode.BadText, "invalid pattern: " + (e as Error).message);
      }
      const t = s[a]!.str;
      const repl = s[a + 2]!.str;
      let out = "";
      let last = 0;
      let prevEnd = -1;
      let matched = 0;
      let count = 0;
      for (let m = re.exec(t); m !== null; m = re.exec(t)) {
        const start = m.index;
        const end = start + m[0].length;
        if (m[0].length === 0) {
          re.lastIndex = start + (t.codePointAt(start)! > 0xffff ? 2 : 1);
          if (start === prevEnd) continue;
        }
        out += t.slice(last, start) + repl;
        matched += runeCount(m[0]);
        count++;
        last = end;
        prevEnd = end;
        if (start >= t.length) break;
      }
      if (count === 0) return text(t);
      if (runeCount(t) - matched + count * runeCount(repl) > vm.maxText) return tooLong();
      return text(out + t.slice(last));
    },
  },
  { name: "TEXT", fn: (_vm, s, a) => text(render(s[a]!)) },
  {
    name: "VALUE",
    fn: (_vm, s, a) => {
      const t = trimSpace(s[a]!.str);
      if (t === "") return BLANK;
      if (!numberSyntax.test(t)) return new EvalError(ErrorCode.BadText, "not a number: " + t);
      return finite(Number(t));
    },
  },
  {
    name: "JSONVALUE",
    fn: (_vm, s, a) => {
      if (s[a]!.kind === Kind.Blank) return BLANK;
      let cur: unknown;
      try {
        cur = JSON.parse(s[a]!.str);
      } catch (e) {
        return new EvalError(ErrorCode.BadText, "invalid json: " + (e as Error).message);
      }
      const path = s[a + 1]!.str;
      if (path !== "") {
        for (const seg of path.split(".")) {
          if (Array.isArray(cur)) {
            const i = arrayIndex(seg);
            if (i < 0 || i >= cur.length) return BLANK;
            cur = cur[i];
          } else if (cur !== null && typeof cur === "object") {
            if (!Object.prototype.hasOwnProperty.call(cur, seg)) return BLANK;
            cur = (cur as Record<string, unknown>)[seg];
          } else {
            return BLANK;
          }
        }
      }
      switch (typeof cur) {
        case "string":
          return text(cur);
        case "number":
          return text(renderNumber(cur));
        case "boolean":
          return text(cur ? "TRUE" : "FALSE");
      }
      if (cur === null) return BLANK;
      return new EvalError(ErrorCode.BadText, "the path does not lead to a scalar");
    },
  },
  // Exact numbers and money (ADR-0022)
  later("DECIMAL", "a function of exact numbers"),
  later("NUMBER", "a function of exact numbers"),
  later("MONEY", "a function of money"),
  later("MONEYVALUE", "a function of money"),
  later("DECIMALVALUE", "a function of exact numbers"),
  later("AMOUNT", "a function of money"),
  later("CURRENCY", "a function of money"),
  // Calendar days and instants (ADR-0023)
  later("DATETIMEVALUE", "a function of dates"),
  later("DATEOF", "a function of dates"),
  later("DATETIME", "a function of dates"),
  // Text made safe for a URL (ADR-0045)
  {
    name: "URLENCODE",
    fn: (vm, s, a) => {
      const bytes = utf8Bytes(s[a]!.str);
      let out = "";
      const hex = "0123456789ABCDEF";
      for (const c of bytes) {
        out += unreserved(c) ? String.fromCharCode(c) : "%" + hex[c >> 4] + hex[c & 15];
      }
      if (out.length > vm.maxText && out.length !== bytes.length) return tooLong();
      return text(out);
    },
  },
  // The life of the document, from its row (ADR-0047, item 5)
  later("CREATEDAT", "a function of the document's life"),
  later("CREATEDBY", "a function of the document's life"),
  later("UPDATEDAT", "a function of the document's life"),
  later("UPDATEDBY", "a function of the document's life"),
  later("ENTEREDAT", "a function of the document's life"),
  later("ENTEREDBY", "a function of the document's life"),
  later("ENTEREDFROM", "a function of the document's life"),
  later("VERSION", "a function of the document's life"),
  // The life of the document by state, from its events (ADR-0047, item 6)
  later("FIRSTENTERED", "a function of a state"),
  later("FIRSTENTEREDBY", "a function of a state"),
  later("LASTENTERED", "a function of a state"),
  later("LASTENTEREDBY", "a function of a state"),
  later("FIRSTLEFT", "a function of a state"),
  later("FIRSTLEFTBY", "a function of a state"),
  later("LASTLEFT", "a function of a state"),
  later("LASTLEFTBY", "a function of a state"),
  later("ENTRIES", "a function of a state"),
  later("DAYSIN", "a function of a state"),
  later("FIRSTEDITOR", "a function of a state"),
  later("LASTEDITOR", "a function of a state"),
];

/** Compares two scalar values that are neither exact: -1, 0 or 1. */
export function compare(a: Value, b: Value): number {
  if (a.kind === Kind.Blank && b.kind === Kind.Blank) {
    return 0;
  }
  const k = a.kind === Kind.Blank ? b.kind : a.kind;
  if (k === Kind.Text || k === Kind.Ref || k === Kind.JSON) {
    return compareText(a.str, b.str);
  }
  return a.num < b.num ? -1 : a.num > b.num ? 1 : 0;
}
