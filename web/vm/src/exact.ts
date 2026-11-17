// Exact numbers and money (ADR-0022) as the Go VM works them out
// (internal/formula/decimal.go, vm.go): a mantissa at a scale of 0 to 18
// places. Go keeps the mantissa in an int64 and refuses with overflow what
// does not fit; here it is a BigInt held to the same range after every
// step, so that what overflows on the server overflows on the page too.

import { EvalError, ErrorCode, errDivByZero, errModByZero, errNotANumber } from "./errors.js";
import { pow10, roundHalfAway, roundTo } from "./numbers.js";
import { trimSpace } from "./text.js";
import { BLANK, Kind, type Value, decimal, money } from "./value.js";

/** The largest scale. */
export const MAX_SCALE = 18;
/** The places a division adds to its dividend's scale. */
const DIV_SCALE_STEP = 4;

const MIN64 = -(2n ** 63n);
const MAX64 = 2n ** 63n - 1n;
const U64 = 2n ** 64n;

/** The error of a result too large for an exact number. */
export const errOverflow = new EvalError(ErrorCode.Overflow, "the exact result does not fit 18 digits");
/** The error of money of two currencies in one operation. */
export const errCurrencyMix = new EvalError(ErrorCode.CurrencyMismatch, "the currencies of one operation must agree");

const fits = (m: bigint): boolean => m >= MIN64 && m <= MAX64;
const abs = (m: bigint): bigint => (m < 0n ? -m : m);
const sign = (m: bigint): bigint => (m > 0n ? 1n : m < 0n ? -1n : 0n);
const pow10i = (k: number): bigint => 10n ** BigInt(k);

/** The sum, or null when it does not fit. */
export function addInt(a: bigint, b: bigint): bigint | null {
  const r = a + b;
  return fits(r) ? r : null;
}

/** The product, or null when it does not fit. */
export function mulInt(a: bigint, b: bigint): bigint | null {
  const r = a * b;
  return fits(r) ? r : null;
}

/** m times 10^k for a positive k; m itself for any other. Null on overflow. */
export function scaleBy(m: bigint, k: number): bigint | null {
  if (k <= 0) return m;
  if (k > MAX_SCALE) return m === 0n ? 0n : null;
  return mulInt(m, pow10i(k));
}

/** x / y rounded half away from zero. Null on overflow. */
function divRound(x: bigint, y: bigint): bigint | null {
  if (y === -1n) {
    return x === MIN64 ? null : -x;
  }
  let q = x / y;
  const r = x % y;
  if (r !== 0n && 2n * abs(r) >= abs(y)) {
    q += (x < 0n) === (y < 0n) ? 1n : -1n;
  }
  return q;
}

/** a at scale sa divided by b at scale sb, at the dividend's scale and four places more. */
export function decDiv(a: bigint, sa: number, b: bigint, sb: number): [bigint, number] | EvalError {
  if (b === 0n) return errDivByZero;
  const r = Math.min(sa + DIV_SCALE_STEP, MAX_SCALE);
  const x = scaleBy(a, r - sa + sb);
  if (x === null) return errOverflow;
  const q = divRound(x, b);
  if (q === null) return errOverflow;
  return [q, r];
}

/** a mod b with the sign of b. */
export function decMod(a: bigint, b: bigint): bigint | EvalError {
  if (b === 0n) return errModByZero;
  if (b === -1n) return 0n;
  let r = a % b;
  if (r !== 0n && r < 0n !== b < 0n) r += b;
  return r;
}

/** The six ways of rounding (ADR-0022). */
export const RoundMethod = { HalfUp: 0, HalfEven: 1, Down: 2, Up: 3, Floor: 4, Ceiling: 5 } as const;
type RoundMethodCode = (typeof RoundMethod)[keyof typeof RoundMethod];

const methods: Record<string, RoundMethodCode> = {
  half_up: RoundMethod.HalfUp,
  half_even: RoundMethod.HalfEven,
  down: RoundMethod.Down,
  up: RoundMethod.Up,
  floor: RoundMethod.Floor,
  ceiling: RoundMethod.Ceiling,
};

/** A method by its name, half_up for any other. */
export function methodNamed(name: string | undefined): RoundMethodCode {
  return (name !== undefined ? methods[name] : undefined) ?? RoundMethod.HalfUp;
}

/** Go's uint64 product 2*u, which wraps. */
const twice64 = (u: bigint): bigint => (2n * u) % U64;

/** m at scale s rounded to places, as the scale it ends at. Null on overflow. */
export function roundMant(m: bigint, s: number, places: number, rm: RoundMethodCode): [bigint, number] | null {
  if (places >= s) {
    const r = scaleBy(m, places - s);
    return r === null ? null : [r, places];
  }
  const drop = s - places;
  let q: bigint;
  let r: bigint;
  let p: bigint;
  if (drop > MAX_SCALE) {
    q = 0n;
    r = m;
    p = U64 - 1n;
  } else {
    const pp = pow10i(drop);
    q = m / pp;
    r = m % pp;
    p = pp;
  }
  const sg = sign(m);
  switch (rm) {
    case RoundMethod.HalfUp:
      if (r !== 0n && twice64(abs(r)) >= p) q += sg;
      break;
    case RoundMethod.HalfEven: {
      const t = twice64(abs(r));
      if (r !== 0n && (t > p || (t === p && (q & 1n) !== 0n))) q += sg;
      break;
    }
    case RoundMethod.Down:
      break;
    case RoundMethod.Up:
      if (r !== 0n) q += sg;
      break;
    case RoundMethod.Floor:
      if (r !== 0n && m < 0n) q--;
      break;
    case RoundMethod.Ceiling:
      if (r !== 0n && m > 0n) q++;
      break;
  }
  if (places < 0) {
    const scaled = scaleBy(q, -places);
    return scaled === null ? null : [scaled, 0];
  }
  return [q, places];
}

/** m at scale s as the same value at scale to, or inexact when places would be lost. */
export function rescaleExact(m: bigint, s: number, to: number): bigint | EvalError {
  if (to >= s) {
    const r = scaleBy(m, to - s);
    return r === null ? errOverflow : r;
  }
  const p = pow10i(s - to);
  if (m % p !== 0n) {
    return new EvalError(ErrorCode.Inexact, `the value has more decimal places than ${to}`);
  }
  return m / p;
}

/**
 * A number written exactly: an optional sign, digits with an optional
 * fraction, an optional exponent. The scale is the places written after
 * the point, moved by the exponent, trailing zeros kept: "0.30" is 30 at
 * scale 2, "1e3" is 1000 at scale 0, and a zero is zero at scale 0.
 */
export function parseDecimal(s: string): [bigint, number] | EvalError {
  const bad = (): EvalError => new EvalError(ErrorCode.BadText, "not a decimal: " + s);
  let text = s;
  let neg = false;
  if (text !== "" && (text[0] === "+" || text[0] === "-")) {
    neg = text[0] === "-";
    text = text.slice(1);
  }
  let expText = "";
  const e = text.search(/[eE]/);
  if (e >= 0) {
    expText = text.slice(e + 1);
    text = text.slice(0, e);
    if (expText === "" || ((expText[0] === "+" || expText[0] === "-") && expText.length === 1)) return bad();
    if (!/^[+\-0-9][0-9]*$/.test(expText)) return bad();
  }
  const dot = text.indexOf(".");
  const intPart = dot >= 0 ? text.slice(0, dot) : text;
  const fracPart = dot >= 0 ? text.slice(dot + 1) : "";
  if (intPart === "" && fracPart === "") return bad();
  if (!/^[0-9]*$/.test(intPart + fracPart)) return bad();
  let digits = (intPart + fracPart).replace(/^0+/, "");
  if (digits === "") return [0n, 0];
  let exp = 0;
  if (expText !== "") {
    const x = Number(expText);
    if (!Number.isFinite(x) || x > 1e6 || x < -1e6) return errOverflow;
    exp = x;
  }
  let scale = fracPart.length - exp;
  if (scale < 0) {
    if (-scale > 19) return errOverflow;
    digits += "0".repeat(-scale);
    scale = 0;
  }
  if (scale > MAX_SCALE || digits.length > 19) return errOverflow;
  const u = BigInt(digits);
  if (u > 2n ** 63n || (u === 2n ** 63n && !neg)) return errOverflow;
  return [neg ? -u : u, scale];
}

/** A currency code: three letters from A to Z. */
export function isCurrency(s: string): boolean {
  return /^[A-Z]{3}$/.test(s);
}

/** An amount with its currency, "12.30 PLN". */
function parseMoney(s: string): [bigint, number, string] | EvalError {
  const sp = s.indexOf(" ");
  if (sp < 0 || !isCurrency(s.slice(sp + 1))) {
    return new EvalError(ErrorCode.BadText, "not an amount with a currency: " + s);
  }
  const d = parseDecimal(s.slice(0, sp));
  if (d instanceof EvalError) return d;
  return [d[0], d[1], s.slice(sp + 1)];
}

/** Rounds half to even, as Go's math.RoundToEven. */
function roundHalfEven(x: number): number {
  const t = Math.trunc(x);
  const d = Math.abs(x - t);
  if (d > 0.5 || (d === 0.5 && t % 2 !== 0)) return t + Math.sign(x);
  return t;
}

/** A plain number as a mantissa at places, rounded by the method. */
export function decFromFloat(f: number, places: number, rm: RoundMethodCode): bigint | EvalError {
  if (!Number.isFinite(f)) return errNotANumber;
  const scaled = f * pow10(places);
  let stepped: number;
  switch (rm) {
    case RoundMethod.HalfUp:
      stepped = roundHalfAway(scaled);
      break;
    case RoundMethod.HalfEven:
      stepped = roundHalfEven(scaled);
      break;
    default: {
      const guarded = roundTo(scaled, 9);
      switch (rm) {
        case RoundMethod.Down:
          stepped = Math.trunc(guarded);
          break;
        case RoundMethod.Up:
          stepped = guarded < 0 ? Math.floor(guarded) : Math.ceil(guarded);
          break;
        case RoundMethod.Floor:
          stepped = Math.floor(guarded);
          break;
        default:
          stepped = Math.ceil(guarded);
      }
    }
  }
  if (Math.abs(stepped) >= 2 ** 63) return errOverflow;
  return BigInt(stepped);
}

/** A mantissa at a scale as the nearest plain number. */
export function decToFloat(m: bigint, s: number): number {
  return Number(m) / pow10(s);
}

/** The mantissa and the scale of an exact operand; a bool is 1 or 0, anything else 0. */
export function exactOperand(v: Value): [bigint, number] {
  switch (v.kind) {
    case Kind.Decimal:
    case Kind.Money:
      return [v.mant, v.scale];
    case Kind.Bool:
      return [BigInt(v.num), 0];
  }
  return [0n, 0];
}

/** A decimal, or money when the value given is money. */
export function sameKind(x: Value, m: bigint, s: number): Value {
  return x.kind === Kind.Money ? money(m, s, x.str) : decimal(m, s);
}

/** Money of the currency, or a decimal when there is none. */
export function exactResult(m: bigint, s: number, cur: string): Value {
  return cur !== "" ? money(m, s, cur) : decimal(m, s);
}

/** DECIMALVALUE and MONEYVALUE: the text at places, exactly, or inexact. */
export function exactOfText(t: string, places: number, withCurrency: boolean): Value | EvalError {
  const s = trimSpace(t);
  if (s === "") return BLANK;
  let m: bigint;
  let scale: number;
  let cur = "";
  if (withCurrency) {
    const p = parseMoney(s);
    if (p instanceof EvalError) return p;
    [m, scale, cur] = p;
  } else {
    const p = parseDecimal(s);
    if (p instanceof EvalError) return p;
    [m, scale] = p;
  }
  const r = rescaleExact(m, scale, places);
  if (r instanceof EvalError) return r;
  return withCurrency ? money(r, places, cur) : decimal(r, places);
}

// ---- The instructions of exact numbers (internal/formula/vm.go)

/** The bit of an operand that says the result is money. */
const MONEY_FLAG = 1 << 8;

/** dec.neg, dec.percent and dec.scale over the top of the stack. */
export function decUnary(scaleOnly: boolean, neg: boolean, v: Value, arg: number): Value | EvalError {
  const scale = arg & 0xff;
  const isMoney = (arg & MONEY_FLAG) !== 0;
  if (scaleOnly) {
    if (isMoney && (v.kind === Kind.Decimal || v.kind === Kind.Number)) {
      return BLANK;
    }
    if (v.kind === Kind.Number) {
      const m = decFromFloat(v.num, scale, RoundMethod.HalfUp);
      return m instanceof EvalError ? m : decimal(m, scale);
    }
    if ((v.kind === Kind.Decimal || v.kind === Kind.Money) && v.scale !== scale) {
      const m = rescaleExact(v.mant, v.scale, scale);
      return m instanceof EvalError ? m : sameKind(v, m, scale);
    }
    return v;
  }
  if (v.kind === Kind.Blank || v.kind === Kind.Bool) {
    if (isMoney) return BLANK;
    const m = BigInt(v.num);
    return decimal(neg ? -m : m, scale);
  }
  let m = v.mant;
  if (neg) {
    if (m === MIN64) return errOverflow;
    m = -m;
  }
  return v.kind === Kind.Money ? money(m, scale, v.str) : decimal(m, scale);
}

/** Compares two operands of which one or both are exact, aligned at scale. */
export function compareExact(a: Value, b: Value, scale: number): number | EvalError {
  if (a.kind === Kind.Money && b.kind === Kind.Money && a.str !== b.str) return errCurrencyMix;
  const [ma, sa] = exactOperand(a);
  const [mb, sb] = exactOperand(b);
  const x = scaleBy(ma, scale - sa);
  const y = scaleBy(mb, scale - sb);
  if (x === null || y === null) return errOverflow;
  return x < y ? -1 : x > y ? 1 : 0;
}

/** The exact operators: 0 add, 1 sub, 2 mul, 3 div; the result at the static scale. */
export function decBinary(op: 0 | 1 | 2 | 3, a: Value, b: Value, arg: number): Value | EvalError {
  const scale = arg & 0xff;
  const isMoney = (arg & MONEY_FLAG) !== 0;
  let cur = "";
  if (a.kind === Kind.Money) cur = a.str;
  if (b.kind === Kind.Money) {
    if (cur !== "" && b.str !== cur) return errCurrencyMix;
    cur = b.str;
  }
  const [ma, sa] = exactOperand(a);
  const [mb, sb] = exactOperand(b);
  let m: bigint | null;
  let s: number;
  if (op <= 1) {
    // Both at the larger scale.
    const x = sa < sb ? scaleBy(ma, sb - sa) : ma;
    let y = sb < sa ? scaleBy(mb, sa - sb) : mb;
    if (x === null || y === null) return errOverflow;
    s = Math.max(sa, sb);
    if (op === 1) {
      if (y === MIN64) return errOverflow;
      y = -y;
    }
    m = addInt(x, y);
  } else if (op === 2) {
    m = mulInt(ma, mb);
    s = sa + sb;
  } else {
    const q = decDiv(ma, sa, mb, sb);
    if (q instanceof EvalError) return q;
    [m, s] = q;
  }
  if (m === null) return errOverflow;
  if (s !== scale) {
    m = scaleBy(m, scale - s);
    if (m === null) return errOverflow;
  }
  if (isMoney) {
    return cur === "" ? BLANK : money(m, scale, cur);
  }
  return decimal(m, scale);
}
