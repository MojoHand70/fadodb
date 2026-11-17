// Values, as the Go VM keeps them (internal/formula/value.go). Every value
// has the same five fields, so that the engine of the page sees one shape:
// the kind; the number of a number, the 1 or 0 of a bool, the days of a
// date and the seconds of a datetime; the text of a text, a ref or a json
// value and the currency of a money; and the mantissa and scale of an
// exact number (ADR-0022, ADR-0029). The zero of each field is what a
// blank holds.

/** The kinds of a value, numbered as the engine numbers them. */
export const Kind = {
  Blank: 0,
  Bool: 1,
  Number: 2,
  Decimal: 3,
  Money: 4,
  Text: 5,
  Date: 6,
  DateTime: 7,
  Ref: 8,
  JSON: 9,
  Array: 10,
  Rows: 11,
  Object: 12,
} as const;

/** One of the numbers of Kind. */
export type KindCode = (typeof Kind)[keyof typeof Kind];

/** The names of the kinds, as the engine writes them. */
export const kindNames: readonly string[] = ["blank", "bool", "number", "decimal", "money", "text", "date", "datetime", "ref", "json", "array", "rows", "object"];

/** A value of a formula. */
export class Value {
  constructor(
    /** What the value is, one of Kind. */
    readonly kind: KindCode,
    /** A number; 1 or 0 for a bool; days for a date; seconds for a datetime. */
    readonly num: number,
    /** A text, a ref or a json value; the currency of a money. */
    readonly str: string,
    /** The mantissa of a decimal or a money. */
    readonly mant: bigint,
    /** The digits after the point of a decimal or a money. */
    readonly scale: number,
  ) {}

  /** Whether the value is blank. */
  get isBlank(): boolean {
    return this.kind === Kind.Blank;
  }

  /** The value as a condition: TRUE is true, FALSE and blank are false. */
  get truth(): boolean {
    return this.kind === Kind.Bool && this.num !== 0;
  }
}

/** The blank value. */
export const BLANK = new Value(Kind.Blank, 0, "", 0n, 0);
/** TRUE. */
export const TRUE = new Value(Kind.Bool, 1, "", 0n, 0);
/** FALSE. */
export const FALSE = new Value(Kind.Bool, 0, "", 0n, 0);

/** TRUE or FALSE. */
export function bool(b: boolean): Value {
  return b ? TRUE : FALSE;
}

/** A number. */
export function number(n: number): Value {
  return new Value(Kind.Number, n, "", 0n, 0);
}

/** A text. */
export function text(s: string): Value {
  return new Value(Kind.Text, 0, s, 0n, 0);
}

/** A date, as days since 1970-01-01. */
export function date(days: number): Value {
  return new Value(Kind.Date, days, "", 0n, 0);
}

/** A datetime, as seconds since 1970-01-01 00:00:00 UTC. */
export function datetime(secs: number): Value {
  return new Value(Kind.DateTime, secs, "", 0n, 0);
}

/** A reference to a document, in its Class:id form. */
export function ref(s: string): Value {
  return new Value(Kind.Ref, 0, s, 0n, 0);
}

/** A json value, as its text. */
export function json(s: string): Value {
  return new Value(Kind.JSON, 0, s, 0n, 0);
}

/** An exact number: decimal(1230n, 2) is 12.30. */
export function decimal(mant: bigint, scale: number): Value {
  return new Value(Kind.Decimal, 0, "", mant, scale);
}

/** Money: a mantissa, a scale and a currency code. */
export function money(mant: bigint, scale: number, currency: string): Value {
  return new Value(Kind.Money, 0, currency, mant, scale);
}

/** Whether a kind is an exact number. */
export function isExact(k: number): boolean {
  return k === Kind.Decimal || k === Kind.Money;
}

/**
 * Whether two values are the same value of the same kind. An exact value
 * compares by mantissa and scale, so 0.3 and 0.30 differ. It is the
 * identity the tests use, not the = of the language.
 */
export function same(a: Value, b: Value): boolean {
  if (a.kind !== b.kind) {
    return false;
  }
  switch (a.kind) {
    case Kind.Blank:
      return true;
    case Kind.Bool:
    case Kind.Number:
    case Kind.Date:
    case Kind.DateTime:
      return a.num === b.num;
    case Kind.Decimal:
      return a.scale === b.scale && a.mant === b.mant;
    case Kind.Money:
      return a.scale === b.scale && a.mant === b.mant && a.str === b.str;
    default:
      return a.str === b.str;
  }
}
