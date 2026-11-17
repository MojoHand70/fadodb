// The errors a formula stops with: the closed list of ADR-0019, ADR-0022
// and ADR-0024, with the codes the Go VM gives (internal/formula/vm.go).

/** The codes a formula may stop with. */
export const ErrorCode = {
  DivByZero: "div_by_zero",
  NotANumber: "not_a_number",
  BadText: "bad_text",
  OutOfRange: "out_of_range",
  LengthMismatch: "length_mismatch",
  TooManyElements: "too_many_elements",
  QueryFailed: "query_failed",
  Overflow: "overflow",
  Inexact: "inexact",
  CurrencyMismatch: "currency_mismatch",
} as const;

/**
 * The error a formula stopped with: its code, a message, and the offset in
 * the formula's text of the operation that failed, -1 when not known.
 */
export class EvalError extends Error {
  constructor(
    /** The code, one of ErrorCode. */
    readonly code: string,
    /** What went wrong, in words. */
    readonly detail: string,
    /** The offset in the formula's text of the operation that failed, -1 when not known. */
    readonly pos: number = -1,
  ) {
    super(pos >= 0 ? `${code}: ${detail} at offset ${pos}` : `${code}: ${detail}`);
    this.name = "EvalError";
  }
}

// The errors that carry no detail, shared as the Go VM shares them.
export const errDivByZero = new EvalError(ErrorCode.DivByZero, "/ by zero");
export const errNotANumber = new EvalError(ErrorCode.NotANumber, "the result is not a finite number");
export const errModByZero = new EvalError(ErrorCode.DivByZero, "MOD by zero");
export const errServer = new EvalError(ErrorCode.QueryFailed, "the formula reads a table or other documents, which the server works out; the page is given its last value");

/** An error of the range check of a function. */
export function outOfRange(what: string): EvalError {
  return new EvalError(ErrorCode.OutOfRange, what);
}

/** The error of a text past the limit of the database. */
export function tooLong(): EvalError {
  return outOfRange("the result is too long");
}

