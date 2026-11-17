// Plain numbers as the Go VM works them out (internal/formula/builtins.go):
// float64 arithmetic with Excel's rules (ADR-0004), where a power, a
// rounding and a logarithm come out to the same last bit as Go's. Go
// rounds half away from zero, has a fused multiply-add and its own
// math.Pow; this file does the same with what JavaScript has.

/** The smallest normal float64. */
const MIN_NORMAL = 2.2250738585072014e-308;

/** Half away from zero, as Go's math.Round. */
export function roundHalfAway(x: number): number {
  const t = Math.trunc(x);
  if (Math.abs(x - t) >= 0.5) {
    return t + Math.sign(x);
  }
  return t;
}

const pow10Table: Float64Array = (() => {
  const t = new Float64Array(632);
  for (let i = 0; i < t.length; i++) {
    t[i] = Number("1e" + (i - 323));
  }
  return t;
})();

/** 10 to an integer power, exactly as the nearest float64 to it. */
export function pow10(d: number): number {
  if (d < -323) return 0;
  if (d > 308) return Infinity;
  return pow10Table[d + 323]!;
}

/** The natural logarithm, with subnormal arguments scaled first as Go does. */
export function ln(x: number): number {
  if (x > 0 && x < MIN_NORMAL) {
    return Math.log(x * 18446744073709551616) - 64 * Math.LN2;
  }
  return Math.log(x);
}

/** The binary exponent and the fraction in [0.5, 1) of x, as Go's math.Frexp. */
export function frexp(x: number): [number, number] {
  if (x === 0 || !Number.isFinite(x)) {
    return [x, 0];
  }
  let e = 0;
  if (Math.abs(x) < MIN_NORMAL) {
    x *= 4503599627370496; // 2^52
    e = -52;
  }
  const view = new DataView(new ArrayBuffer(8));
  view.setFloat64(0, x);
  const hi = view.getUint32(0);
  e += ((hi >>> 20) & 0x7ff) - 1022;
  view.setUint32(0, (hi & 0x800fffff) | (1022 << 20));
  return [view.getFloat64(0), e];
}

/** x times 2^e with one rounding, as Go's math.Ldexp. */
export function ldexp(x: number, e: number): number {
  if (x === 0 || !Number.isFinite(x)) {
    return x;
  }
  while (e > 1023) {
    x *= 2 ** 1023;
    e -= 1023;
    if (!Number.isFinite(x)) return x;
  }
  if (e < -1022) {
    // Scale while the value stays normal, then round once into the
    // subnormals, as Go does.
    while (e < -1022 - 53) {
      x *= 2 ** -1022;
      e += 1022;
      if (x === 0) return x;
    }
    return x * 2 ** (e + 53) * 2 ** -53;
  }
  return x * 2 ** e;
}

/** Go's math.Pow, the fallback of pow for what it does not handle itself. */
export function goPow(x: number, y: number): number {
  if (y === 0 || x === 1) return 1;
  if (y === 1) return x;
  if (Number.isNaN(x) || Number.isNaN(y)) return NaN;
  if (x === 0) {
    const odd = isOddInt(y);
    if (y < 0) return Object.is(x, -0) && odd ? -Infinity : Infinity;
    if (y > 0) return Object.is(x, -0) && odd ? x : 0;
  }
  if (!Number.isFinite(y)) {
    if (x === -1) return 1;
    return (Math.abs(x) < 1) === (y > 0) ? 0 : Infinity;
  }
  if (!Number.isFinite(x)) {
    if (x < 0) return goPow(1 / x, -y);
    return y < 0 ? 0 : Infinity;
  }
  if (y === 0.5) return Math.sqrt(x);
  if (y === -0.5) return 1 / Math.sqrt(x);
  const ay = Math.abs(y);
  let yi = Math.trunc(ay);
  let yf = ay - yi;
  if (yf !== 0 && x < 0) return NaN;
  if (yi >= 2 ** 63) {
    if (x === -1) return 1;
    return (Math.abs(x) < 1) === (y > 0) ? 0 : Infinity;
  }
  let a1 = 1;
  let ae = 0;
  if (yf !== 0) {
    if (yf > 0.5) {
      yf--;
      yi++;
    }
    a1 = Math.exp(yf * Math.log(x));
  }
  let [x1, xe] = frexp(x);
  for (let i = BigInt(yi); i !== 0n; i >>= 1n) {
    if (xe < -(1 << 12) || 1 << 12 < xe) {
      ae += xe;
      break;
    }
    if ((i & 1n) === 1n) {
      a1 *= x1;
      ae += xe;
    }
    x1 *= x1;
    xe <<= 1;
    if (x1 < 0.5) {
      x1 += x1;
      xe--;
    }
  }
  if (y < 0) {
    a1 = 1 / a1;
    ae = -ae;
  }
  return ldexp(a1, ae);
}

function isOddInt(x: number): boolean {
  if (Math.abs(x) >= 2 ** 53) return false;
  return Number.isInteger(x) && Math.abs(x % 2) === 1;
}

const SPLIT = 134217729; // 2^27 + 1

/** a*b - p exactly, for p the rounded product, as a fused multiply-add gives. */
function productError(a: number, b: number, p: number): number {
  let c = SPLIT * a;
  const ah = c - (c - a);
  const al = a - ah;
  c = SPLIT * b;
  const bh = c - (c - b);
  const bl = b - bh;
  const e = ah * bh - p + ah * bl + al * bh + al * bl;
  if (Number.isFinite(e)) {
    return e;
  }
  // A factor too large to split: work two to the 600 lower.
  const s = 2 ** -600;
  return productError(a * s, b, p * s) / s;
}

/** The double-double product of (ah, al) and (bh, bl). */
function ddMul(ah: number, al: number, bh: number, bl: number): [number, number] {
  const p = ah * bh;
  if (!Number.isFinite(p)) {
    return [p, 0];
  }
  const e = productError(ah, bh, p) + ah * bl + al * bh;
  const hi = p + e;
  return [hi, e - (hi - p)];
}

/**
 * x to the power y, as the formula language works it out: an integer
 * power by repeated squaring in double-double, so that 1.1^2 is 1.21, a
 * half power by a square root, and Go's math.Pow for the rest.
 */
export function pow(x: number, y: number): number {
  if (y === 0 || x === 1) {
    return 1;
  }
  const yi = Math.trunc(y);
  const yf = y - yi;
  if (x <= 0 || !Number.isFinite(x) || Number.isNaN(y) || !Number.isFinite(y) || Math.abs(yi) > 2 ** 62) {
    return goPow(x, y);
  }
  let frac = 1;
  if (yf === 0.5) {
    frac = Math.sqrt(x);
  } else if (yf !== 0) {
    frac = Math.exp(yf * ln(x));
  }
  let n = BigInt(yi);
  const neg = n < 0n;
  if (neg) n = -n;
  let hi = 1;
  let lo = 0;
  let bh = x;
  let bl = 0;
  while (n > 0n) {
    if ((n & 1n) === 1n) {
      [hi, lo] = ddMul(hi, lo, bh, bl);
    }
    n >>= 1n;
    if (n > 0n) {
      [bh, bl] = ddMul(bh, bl, bh, bl);
    }
    if (!Number.isFinite(hi) || !Number.isFinite(bh) || hi === 0) {
      return goPow(x, y);
    }
  }
  let r = hi + lo;
  if (neg) {
    r = 1 / r;
  }
  return r * frac;
}

// The constants Go folds exactly at compile time: ln 2 / ln 10 and 1 / ln 2.
const LN2_OVER_LN10 = 0.3010299956639812;
const ONE_OVER_LN2 = 1.4426950408889634;

/** Go's math.Log2: the exponent of x plus the logarithm of its fraction. */
function goLog2(x: number): number {
  const [frac, exp] = frexp(x);
  if (frac === 0.5) {
    return exp - 1;
  }
  return Math.log(frac) * ONE_OVER_LN2 + exp;
}

/** The base-10 logarithm, as Go's math.Log10, exact for an exact power of ten. */
export function log10(x: number): number {
  let r = goLog2(x) * LN2_OVER_LN10;
  if (x < MIN_NORMAL) {
    r = ln(x) / Math.LN10;
  }
  const n = roundHalfAway(r);
  if (n !== r && Math.abs(n) <= 323 && pow10(n) === x) {
    return n;
  }
  return r;
}

/** v rounded to digits places, half away from zero. */
export function roundTo(v: number, digits: number): number {
  const d = Math.trunc(digits);
  if (d > 300) return v;
  if (d < -300) return 0;
  const p = pow10(d);
  const scaled = v * p;
  if (Math.abs(scaled) >= 2 ** 52) {
    return v;
  }
  return roundHalfAway(scaled) / p;
}

/** A step of FLOOR, CEILING or TRUNC at digits places. */
export function scaledStep(v: number, digits: number, step: (x: number) => number): number {
  const d = Math.max(-300, Math.min(300, Math.trunc(digits)));
  const p = pow10(d);
  const scaled = v * p;
  if (Math.abs(scaled) >= 2 ** 52) {
    return v;
  }
  return step(roundTo(scaled, 9)) / p;
}
