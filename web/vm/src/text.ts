// Text as the Go VM sees it: a sequence of code points, compared code
// point by code point, with the simple case mappings of Unicode. A
// JavaScript string is UTF-16, so every count, cut and comparison here goes
// by code point and not by unit.

/** The number of code points of a text. */
export function runeCount(s: string): number {
  let n = s.length;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) {
      const d = s.charCodeAt(i + 1);
      if (d >= 0xdc00 && d <= 0xdfff) {
        n--;
        i++;
      }
    }
  }
  return n;
}

/** Whether a text has more code points than the limit, counted only when it may. */
export function longerThan(s: string, limit: number): boolean {
  return s.length > limit && runeCount(s) > limit;
}

/** The code points of a text. */
export function runes(s: string): string[] {
  return Array.from(s);
}

/**
 * Compares two texts by code point, as Go compares UTF-8 bytes: -1, 0 or
 * 1. UTF-16 order differs only where a unit of a pair meets a unit from
 * U+E000 up, which this puts right.
 */
export function compareText(a: string, b: string): number {
  if (a === b) {
    return 0;
  }
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    let ca = a.charCodeAt(i);
    let cb = b.charCodeAt(i);
    if (ca !== cb) {
      if (ca >= 0xd800 && cb >= 0xd800) {
        ca = ca >= 0xe000 ? ca - 0x800 : ca + 0x2000;
        cb = cb >= 0xe000 ? cb - 0x800 : cb + 0x2000;
      }
      return ca < cb ? -1 : 1;
    }
  }
  return a.length < b.length ? -1 : a.length > b.length ? 1 : 0;
}

// The simple case mappings that JavaScript's full mappings miss: where the
// full mapping of a code point is several code points and the simple one
// is another single code point (UnicodeData.txt).
const simpleLower: Record<number, number> = { 0x130: 0x69 };
const simpleUpper: Record<number, number> = (() => {
  const m: Record<number, number> = { 0x1fb3: 0x1fbc, 0x1fc3: 0x1fcc, 0x1ff3: 0x1ffc };
  for (const [from, to] of [
    [0x1f80, 0x1f88],
    [0x1f90, 0x1f98],
    [0x1fa0, 0x1fa8],
  ]) {
    for (let i = 0; i < 8; i++) {
      m[from! + i] = to! + i;
    }
  }
  return m;
})();

function mapRune(r: string, full: (s: string) => string, simple: Record<number, number>): string {
  const cp = r.codePointAt(0)!;
  const s = simple[cp];
  if (s !== undefined) {
    return String.fromCodePoint(s);
  }
  const m = full(r);
  // A mapping to several code points is a full mapping only: the simple
  // one keeps the code point.
  return m.length === 1 || (m.length === 2 && m.codePointAt(0)! > 0xffff) ? m : r;
}

const toUpper = (s: string): string => s.toUpperCase();
const toLower = (s: string): string => s.toLowerCase();

/** The upper case of each code point, as Go's strings.ToUpper. */
export function upper(s: string): string {
  let out = "";
  for (const r of s) {
    out += mapRune(r, toUpper, simpleUpper);
  }
  return out;
}

/** The lower case of each code point, as Go's strings.ToLower. */
export function lower(s: string): string {
  let out = "";
  for (const r of s) {
    out += mapRune(r, toLower, simpleLower);
  }
  return out;
}

const letter = /^\p{L}$/u;

/** Each word with its first letter upper case and the rest lower, as PROPER. */
export function proper(s: string): string {
  let out = "";
  let prev = false;
  for (const r of s) {
    if (letter.test(r)) {
      out += prev ? mapRune(r, toLower, simpleLower) : mapRune(r, toUpper, simpleUpper);
      prev = true;
    } else {
      out += r;
      prev = false;
    }
  }
  return out;
}

// The white space of Go's unicode.IsSpace, which strings.TrimSpace trims.
const space = "[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]";
const spaceAtEnds = new RegExp("^" + space + "+|" + space + "+$", "g");

/** A text without the white space at its ends, as Go's strings.TrimSpace. */
export function trimSpace(s: string): string {
  return s.replace(spaceAtEnds, "");
}

const utf8 = new TextEncoder();

/** The UTF-8 bytes of a text. */
export function utf8Bytes(s: string): Uint8Array {
  return utf8.encode(s);
}
