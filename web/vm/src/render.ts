// How a value becomes text: the TEXT rule of ADR-0019, ADR-0022 and
// ADR-0023, the one place a scalar is rendered, as the Go VM renders it
// (internal/formula/value.go). A page shows what the server would show.

import { Kind, type Value } from "./value.js";

/**
 * A number with 15 significant digits by the C %.15g rule, as Go's
 * strconv.FormatFloat(f, 'g', 15, 64) writes it: trailing zeros removed,
 * an exponent below -4 or from 15 up written as e-05 or e+15. The digits
 * are rounded half to even on the exact binary value.
 */
export function renderNumber(f: number): string {
  if (f === 0) {
    return "0";
  }
  if (!Number.isFinite(f)) {
    return f > 0 ? "+Inf" : f < 0 ? "-Inf" : "NaN";
  }
  let [digits, exp] = fifteen(Math.abs(f));
  const sign = f < 0 ? "-" : "";
  digits = digits.replace(/0+$/, "");
  if (exp < -4 || exp >= 15) {
    const mant = digits.length > 1 ? digits[0] + "." + digits.slice(1) : digits;
    const e = Math.abs(exp);
    return sign + mant + "e" + (exp < 0 ? "-" : "+") + (e < 10 ? "0" + e : String(e));
  }
  if (exp < 0) {
    return sign + "0." + "0".repeat(-exp - 1) + digits;
  }
  if (digits.length <= exp + 1) {
    return sign + digits + "0".repeat(exp + 1 - digits.length);
  }
  return sign + digits.slice(0, exp + 1) + "." + digits.slice(exp + 1);
}

/**
 * The 15 significant digits of a positive finite number and the decimal
 * exponent of the first. JavaScript rounds a tie away from zero where Go
 * rounds it to even, so a number whose 16th digit on is exactly 5 is
 * worked out on its exact value.
 */
function fifteen(f: number): [string, number] {
  const wide = f.toExponential(20);
  const at = wide.indexOf("e");
  const rest = wide.slice(16, at);
  if (rest.startsWith("5") && /^50*$/.test(rest)) {
    return exactFifteen(f);
  }
  const s = f.toExponential(14);
  const e = s.indexOf("e");
  return [s[0] + s.slice(2, e), Number(s.slice(e + 1))];
}

/** fifteen, worked out on the exact value with integers. */
function exactFifteen(f: number): [string, number] {
  // f = m * 2^e exactly, with m an integer of at most 53 bits.
  const view = new DataView(new ArrayBuffer(8));
  view.setFloat64(0, f);
  const bits = view.getBigUint64(0);
  const biased = Number((bits >> 52n) & 0x7ffn);
  let m = bits & 0xfffffffffffffn;
  let e: number;
  if (biased === 0) {
    e = -1074;
  } else {
    m |= 1n << 52n;
    e = biased - 1075;
  }
  // The exact decimal digits of m * 2^e, as num / den.
  let num = m;
  let den = 1n;
  if (e >= 0) {
    num <<= BigInt(e);
  } else {
    den <<= BigInt(-e);
  }
  // The decimal exponent: 10^exp <= f < 10^(exp+1).
  let exp = Math.floor(Math.log10(f));
  const pow = (k: number): bigint => 10n ** BigInt(k);
  const fits = (k: number): boolean => (k >= 0 ? num >= den * pow(k) : num * pow(-k) >= den);
  while (!fits(exp)) exp--;
  while (fits(exp + 1)) exp++;
  // Scale to 15 digits before the point: q = f * 10^(14-exp).
  const shift = 14 - exp;
  let n = shift >= 0 ? num * pow(shift) : num;
  let d = shift >= 0 ? den : den * pow(-shift);
  let q = n / d;
  const r = n - q * d;
  if (r * 2n > d || (r * 2n === d && q % 2n === 1n)) {
    q += 1n;
  }
  let digits = q.toString();
  if (digits.length > 15) {
    digits = digits.slice(0, 15);
    exp++;
  }
  return [digits, exp];
}

const pad2 = (n: number): string => (n < 10 ? "0" + n : String(n));

/** The calendar day of days since 1970-01-01, as year, month and day. */
export function civil(days: number): [number, number, number] {
  // Howard Hinnant's days_from_civil, read backwards.
  const z = days + 719468;
  const era = Math.floor(z / 146097);
  const doe = z - era * 146097;
  const yoe = Math.floor((doe - Math.floor(doe / 1460) + Math.floor(doe / 36524) - Math.floor(doe / 146096)) / 365);
  const doy = doe - (365 * yoe + Math.floor(yoe / 4) - Math.floor(yoe / 100));
  const mp = Math.floor((5 * doy + 2) / 153);
  const d = doy - Math.floor((153 * mp + 2) / 5) + 1;
  const m = mp < 10 ? mp + 3 : mp - 9;
  return [yoe + era * 400 + (m <= 2 ? 1 : 0), m, d];
}

/** A date as YYYY-MM-DD. */
export function renderDate(days: number): string {
  const [y, m, d] = civil(days);
  return String(y).padStart(4, "0") + "-" + pad2(m) + "-" + pad2(d);
}

/** A datetime as YYYY-MM-DD HH:MM:SS, in UTC. */
export function renderDateTime(secs: number): string {
  const days = Math.floor(secs / 86400);
  const s = secs - days * 86400;
  return renderDate(days) + " " + pad2(Math.floor(s / 3600)) + ":" + pad2(Math.floor((s % 3600) / 60)) + ":" + pad2(s % 60);
}

/** An exact number as its digits, with as many after the point as its scale. */
export function formatDecimal(mant: bigint, scale: number): string {
  let digits = (mant < 0n ? -mant : mant).toString();
  if (scale > 0) {
    if (digits.length <= scale) {
      digits = "0".repeat(scale - digits.length + 1) + digits;
    }
    digits = digits.slice(0, digits.length - scale) + "." + digits.slice(digits.length - scale);
  }
  return mant < 0n ? "-" + digits : digits;
}

/** The value as text, as TEXT renders it. A blank is the empty text. */
export function render(v: Value): string {
  switch (v.kind) {
    case Kind.Text:
    case Kind.Ref:
    case Kind.JSON:
      return v.str;
    case Kind.Blank:
      return "";
    case Kind.Bool:
      return v.num !== 0 ? "TRUE" : "FALSE";
    case Kind.Number:
      return renderNumber(v.num);
    case Kind.Decimal:
      return formatDecimal(v.mant, v.scale);
    case Kind.Money:
      return formatDecimal(v.mant, v.scale) + " " + v.str;
    case Kind.Date:
      return renderDate(v.num);
    case Kind.DateTime:
      return renderDateTime(v.num);
  }
  return "";
}
