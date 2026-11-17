// The bytecode, format 4, as the Go engine writes it (internal/formula/
// program.go, ADR-0003): a program is a stack machine over values with
// its constants, the variables it reads, its code and what whoever runs it
// needs to know. Every number is big-endian, every text a length and its
// UTF-8 bytes. The browser never parses a formula: it is given this.

import { Kind, type KindCode } from "./value.js";

/** The version of the bytecode this file reads. */
export const FORMAT_VERSION = 4;

/** The opcodes, numbered as the format numbers them. */
export const Op = {
  Nop: 0,
  ConstNum: 1,
  ConstText: 2,
  True: 3,
  False: 4,
  Blank: 5,
  Load: 6,
  Neg: 7,
  Percent: 8,
  Not: 9,
  Add: 10,
  Sub: 11,
  Mul: 12,
  Div: 13,
  DivStrict: 14,
  Pow: 15,
  Concat: 16,
  Eq: 17,
  Ne: 18,
  Lt: 19,
  Le: 20,
  Gt: 21,
  Ge: 22,
  Jump: 23,
  JumpIfFalse: 24,
  AndJump: 25,
  OrJump: 26,
  ToBool: 27,
  Call: 28,
  Lookup: 29,
  Query: 30,
  Return: 31,
  ConstDec: 32,
  DecNeg: 33,
  DecPercent: 34,
  DecAdd: 35,
  DecSub: 36,
  DecMul: 37,
  DecDiv: 38,
  DecScale: 39,
  Try: 40,
  EndTry: 41,
} as const;

/** The largest scale of an exact number (ADR-0022). */
export const MAX_SCALE = 18;
/** The bit of an exact operation's operand that says the result is money. */
export const DEC_MONEY_FLAG = 1 << 8;

/** A static type: a kind, the scale of an exact one, the element of an array. */
export interface Type {
  readonly kind: KindCode;
  readonly scale: number;
  readonly elem: KindCode;
}

const scalarKinds: Record<string, KindCode> = {
  bool: Kind.Bool,
  number: Kind.Number,
  text: Kind.Text,
  date: Kind.Date,
  datetime: Kind.DateTime,
  ref: Kind.Ref,
  json: Kind.JSON,
};

/** Reads a type as the engine writes it: number, decimal(2), array<text>. */
export function parseType(s: string): Type {
  const scalar = scalarKinds[s];
  if (scalar !== undefined) {
    return { kind: scalar, scale: 0, elem: Kind.Blank };
  }
  switch (s) {
    case "blank":
      return { kind: Kind.Blank, scale: 0, elem: Kind.Blank };
    case "rows":
      return { kind: Kind.Rows, scale: 0, elem: Kind.Blank };
    case "int":
      return { kind: Kind.Decimal, scale: 0, elem: Kind.Blank };
    case "decimal":
      return { kind: Kind.Decimal, scale: 2, elem: Kind.Blank };
    case "money":
      return { kind: Kind.Money, scale: 2, elem: Kind.Blank };
  }
  if (s.startsWith("array<") && s.endsWith(">")) {
    const elem = parseType(s.slice(6, -1));
    if (elem.kind === Kind.Blank || elem.kind >= Kind.Array) {
      throw new Error(`unknown type "${s}"`);
    }
    return { kind: Kind.Array, scale: elem.scale, elem: elem.kind };
  }
  for (const [prefix, kind] of [
    ["decimal(", Kind.Decimal],
    ["money(", Kind.Money],
  ] as const) {
    if (s.startsWith(prefix) && s.endsWith(")")) {
      const digits = s.slice(prefix.length, -1);
      const n = Number(digits);
      if (!/^[0-9]+$/.test(digits) || n > MAX_SCALE) {
        throw new Error(`type "${s}": the scale must be 0 to ${MAX_SCALE}`);
      }
      return { kind, scale: n, elem: Kind.Blank };
    }
  }
  throw new Error(`unknown type "${s}"`);
}

/** One variable a program reads; values are given in slot order. */
export interface Slot {
  readonly name: string;
  readonly type: Type;
}

/** An exact constant: a mantissa at a scale. */
export interface DecConst {
  readonly mant: bigint;
  readonly scale: number;
}

/**
 * A compiled LOOKUP, or an aggregate over a class (ADR-0020, ADR-0046). The
 * browser reads it to know the program reads other documents; the server
 * works it out.
 */
export interface QuerySlot {
  readonly source: string;
  readonly column: string;
  readonly type: Type;
  readonly order: readonly string[];
  readonly args: readonly number[];
  readonly argNames: readonly string[];
  readonly cond: Program | null;
  readonly key: Program | null;
  readonly indexColumn: string;
  readonly keyPart: boolean;
  readonly agg: string;
  readonly value: Program | null;
}

/** A compiled formula. */
export interface Program {
  readonly nums: Float64Array;
  readonly decs: readonly DecConst[];
  readonly texts: readonly string[];
  readonly slots: readonly Slot[];
  /** The opcode of each instruction. */
  readonly ops: Uint8Array;
  /** The operand of each instruction. */
  readonly args: Int32Array;
  /** The offset in the formula's text of each instruction, or null. */
  readonly pos: Int32Array | null;
  readonly result: Type;
  /** The classes of the functions it calls: pure, context, lookup. */
  readonly classes: readonly string[];
  readonly queries: readonly QuerySlot[];
  /** The stack depth it needs. */
  readonly maxStack: number;
}

const utf8 = new TextDecoder("utf-8");

class Reader {
  private at = 0;
  private readonly view: DataView;

  constructor(private readonly bytes: Uint8Array) {
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }

  private need(n: number): void {
    if (this.at + n > this.bytes.length) {
      throw new Error("bytecode: the program ends early");
    }
  }

  u8(): number {
    this.need(1);
    return this.bytes[this.at++]!;
  }

  u16(): number {
    this.need(2);
    const v = this.view.getUint16(this.at);
    this.at += 2;
    return v;
  }

  u32(): number {
    this.need(4);
    const v = this.view.getUint32(this.at);
    this.at += 4;
    return v;
  }

  i32(): number {
    this.need(4);
    const v = this.view.getInt32(this.at);
    this.at += 4;
    return v;
  }

  f64(): number {
    this.need(8);
    const v = this.view.getFloat64(this.at);
    this.at += 8;
    return v;
  }

  i64(): bigint {
    this.need(8);
    const v = this.view.getBigInt64(this.at);
    this.at += 8;
    return v;
  }

  /** A count, refused when larger than the bytes left, as the Go decoder does. */
  count(): number {
    const n = this.u32();
    if (n > this.bytes.length - this.at) {
      throw new Error("bytecode: the program ends early");
    }
    return n;
  }

  str(): string {
    const n = this.count();
    const s = utf8.decode(this.bytes.subarray(this.at, this.at + n));
    this.at += n;
    return s;
  }

  program(): Program | null {
    const n = this.count();
    if (n === 0) {
      return null;
    }
    const p = decode(this.bytes.subarray(this.at, this.at + n));
    this.at += n;
    return p;
  }
}

/**
 * Reads a program in the binary format of the engine. It refuses a program
 * of another format version, one that ends early, and one whose operands
 * are out of range, so that the VM can trust its tables.
 */
export function decode(bytes: Uint8Array): Program {
  if (bytes.length < 8 || bytes[0] !== 0x46 || bytes[1] !== 0x41 || bytes[2] !== 0x44 || bytes[3] !== 0x4f) {
    throw new Error("bytecode: not a FadoDB program");
  }
  const r = new Reader(bytes.subarray(4));
  const version = r.u16();
  if (version !== FORMAT_VERSION) {
    throw new Error(`bytecode: format version ${version}, this build reads ${FORMAT_VERSION}`);
  }
  r.u16(); // flags
  const nums = new Float64Array(r.count());
  for (let i = 0; i < nums.length; i++) {
    nums[i] = r.f64();
  }
  const decs: DecConst[] = [];
  for (let n = r.count(), i = 0; i < n; i++) {
    const mant = r.i64();
    const scale = r.u8();
    if (scale > MAX_SCALE) {
      throw new Error(`bytecode: exact constant ${i} has scale ${scale}`);
    }
    decs.push({ mant, scale });
  }
  const texts: string[] = [];
  for (let n = r.count(), i = 0; i < n; i++) {
    texts.push(r.str());
  }
  const slots: Slot[] = [];
  for (let n = r.count(), i = 0; i < n; i++) {
    const name = r.str();
    slots.push({ name, type: parseType(r.str()) });
  }
  const length = r.count();
  const ops = new Uint8Array(length);
  const args = new Int32Array(length);
  for (let i = 0; i < length; i++) {
    ops[i] = r.u8();
    args[i] = r.i32();
  }
  let pos: Int32Array | null = null;
  const npos = r.count();
  if (npos > 0) {
    pos = new Int32Array(npos);
    for (let i = 0; i < npos; i++) {
      pos[i] = r.i32();
    }
  }
  const result = parseType(r.str());
  const classes: string[] = [];
  for (let n = r.count(), i = 0; i < n; i++) {
    classes.push(r.str());
  }
  const queries: QuerySlot[] = [];
  for (let n = r.count(), i = 0; i < n; i++) {
    const source = r.str();
    const column = r.str();
    const type = parseType(r.str());
    const order: string[] = [];
    for (let m = r.count(), j = 0; j < m; j++) {
      order.push(r.str());
    }
    const qargs: number[] = [];
    const argNames: string[] = [];
    for (let m = r.count(), j = 0; j < m; j++) {
      qargs.push(r.i32());
      argNames.push(r.str());
    }
    const cond = r.program();
    const key = r.program();
    const indexColumn = r.str();
    const keyPart = r.u8() !== 0;
    const agg = r.str();
    const value = r.program();
    queries.push({ source, column, type, order, args: qargs, argNames, cond, key, indexColumn, keyPart, agg, value });
  }
  const maxStack = r.u32();
  const p: Program = { nums, decs, texts, slots, ops, args, pos, result, classes, queries, maxStack };
  validate(p);
  return p;
}

/** The number of functions the table of this build has; set by the VM. */
let builtinCount = 0;

/** Tells the decoder how many functions the VM's table has. */
export function setBuiltinCount(n: number): void {
  builtinCount = n;
}

function validate(p: Program): void {
  const n = p.ops.length;
  if (n === 0 || p.ops[n - 1] !== Op.Return) {
    throw new Error("bytecode: code must end with return");
  }
  if (p.pos !== null && p.pos.length !== n) {
    throw new Error("bytecode: positions do not match the code");
  }
  for (let i = 0; i < n; i++) {
    const arg = p.args[i]!;
    const bad = (what: string): never => {
      throw new Error(`bytecode: instruction ${i}: ${what}`);
    };
    switch (p.ops[i]) {
      case Op.ConstNum:
        if (arg < 0 || arg >= p.nums.length) bad(`number constant ${arg} out of range`);
        break;
      case Op.ConstText:
        if (arg < 0 || arg >= p.texts.length) bad(`text constant ${arg} out of range`);
        break;
      case Op.ConstDec:
        if (arg < 0 || arg >= p.decs.length) bad(`exact constant ${arg} out of range`);
        break;
      case Op.Load:
        if (arg < 0 || arg >= p.slots.length) bad(`slot ${arg} out of range`);
        break;
      case Op.Jump:
      case Op.JumpIfFalse:
      case Op.AndJump:
      case Op.OrJump:
      case Op.Try:
        if (arg < 0 || arg >= n) bad(`jump target ${arg} out of range`);
        break;
      case Op.DecNeg:
      case Op.DecPercent:
      case Op.DecAdd:
      case Op.DecSub:
      case Op.DecMul:
      case Op.DecDiv:
      case Op.DecScale:
        if (arg < 0 || (arg & 0xff) > MAX_SCALE || (arg & ~(0xff | DEC_MONEY_FLAG)) !== 0) bad(`scale ${arg} out of range`);
        break;
      case Op.Call:
        if (arg >> 8 >= builtinCount || arg < 0) bad(`unknown builtin ${arg >> 8}`);
        break;
      case Op.Lookup:
      case Op.Query:
        if (arg < 0 || arg >= p.queries.length) bad(`query slot ${arg} out of range`);
        break;
      default:
        if (p.ops[i]! > Op.EndTry) bad(`unknown opcode ${p.ops[i]}`);
    }
  }
}
