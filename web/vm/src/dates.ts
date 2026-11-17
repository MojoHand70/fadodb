// Calendar days and instants as the Go VM works them out (ADR-0023,
// internal/formula/builtins.go): a date is a day, counted from
// 1970-01-01; a datetime is an instant, in seconds from 1970-01-01
// 00:00:00 UTC. Everything is arithmetic on whole days with the
// proleptic Gregorian calendar, as Go's time.Date normalizes it, so that
// year 1 to year 9999 and a month that runs over come out as the server's.

import { EvalError, ErrorCode } from "./errors.js";
import { civil } from "./render.js";
import { trimSpace } from "./text.js";
import { BLANK, Kind, type Value, date, datetime } from "./value.js";

/** Seconds in a day. */
export const DAY = 86400;

// The range of ADR-0019 and ADR-0023: year 1 to year 9999.
const MIN_DATE_SECS = -62135596800;
const MAX_DATE_SECS = 253402300799;
const MIN_DATE_DAYS = -719162;
const MAX_DATE_DAYS = 2932896;

/** The error of a date outside year 1 to year 9999. */
export const errDateRange = new EvalError(ErrorCode.OutOfRange, "the date is outside year 1 to year 9999");

/** Days since 1970-01-01 of a calendar day; month and day already in range. */
export function daysFromCivil(y: number, m: number, d: number): number {
  // Howard Hinnant's days_from_civil.
  y -= m <= 2 ? 1 : 0;
  const era = Math.floor(y / 400);
  const yoe = y - era * 400;
  const doy = Math.floor((153 * (m + (m > 2 ? -3 : 9)) + 2) / 5) + d - 1;
  const doe = yoe * 365 + Math.floor(yoe / 4) - Math.floor(yoe / 100) + doy;
  return era * 146097 + doe - 719468;
}

/** The year and the month of a month counted from one in a year, carried as Go carries it. */
function normMonth(y: number, m: number): [number, number] {
  const m0 = m - 1;
  const carry = Math.floor(m0 / 12);
  return [y + carry, m0 - carry * 12 + 1];
}

/** The days of a month. */
export function daysIn(y: number, m: number): number {
  if (m === 2) {
    return (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0 ? 29 : 28;
  }
  return m === 4 || m === 6 || m === 9 || m === 11 ? 30 : 31;
}

/** A date of a number of days, floored, or the range error. */
export function dateValue(days: number): Value | EvalError {
  days = Math.floor(days);
  if (!(days >= MIN_DATE_DAYS && days <= MAX_DATE_DAYS)) {
    return errDateRange;
  }
  return date(days);
}

/** A datetime of a number of seconds, floored, or the range error. */
export function dateTimeValue(secs: number): Value | EvalError {
  secs = Math.floor(secs);
  if (!(secs >= MIN_DATE_SECS && secs <= MAX_DATE_SECS)) {
    return errDateRange;
  }
  return datetime(secs);
}

/** The day of a date, or of the instant of a datetime. */
export function dayNumber(v: Value): number {
  return v.kind === Kind.Date ? v.num : Math.floor(v.num / DAY);
}

/** The calendar day of a date or a datetime, as year, month and day. */
export function civilOf(v: Value): [number, number, number] {
  return civil(dayNumber(v));
}

/** DATE(year, month, day): the month and the day run over as Go's time.Date runs them. */
export function makeDate(y: number, m: number, d: number): Value | EvalError {
  y = Math.trunc(y);
  m = Math.trunc(m);
  d = Math.trunc(d);
  if (Math.abs(y) > 1e6 || Math.abs(m) > 1e7 || Math.abs(d) > 1e9) {
    return errDateRange;
  }
  const [yy, mm] = normMonth(y, m);
  return dateValue(daysFromCivil(yy, mm, 1) + d - 1);
}

/** The date or the datetime months later, the day kept unless the month is shorter. */
export function addMonths(v: Value, months: number): Value | EvalError {
  if (v.kind === Kind.Blank) {
    return BLANK;
  }
  const n = Math.trunc(months);
  if (Math.abs(n) > 1e7) {
    return errDateRange;
  }
  const day = dayNumber(v);
  const [y, m, d] = civil(day);
  const [yy, mm] = normMonth(y, m + n);
  const days = daysFromCivil(yy, mm, Math.min(d, daysIn(yy, mm)));
  if (v.kind === Kind.Date) {
    return dateValue(days);
  }
  return dateTimeValue(days * DAY + (v.num - day * DAY));
}

/** The digits of s from at, n of them exactly, or fewer than min when they run out. */
function digits(s: string, at: number, min: number, max: number): [number, number] | null {
  let end = at;
  while (end < s.length && end - at < max && s.charCodeAt(end) >= 0x30 && s.charCodeAt(end) <= 0x39) {
    end++;
  }
  if (end - at < min) {
    return null;
  }
  return [Number(s.slice(at, end)), end];
}

/**
 * Reads YYYY-MM-DD, and with time YYYY-MM-DD HH:MM:SS, as Go's time.Parse
 * reads the two layouts: four digits of year, two of month,
 * day, minute and second, one or two of hour, a fraction of a second
 * allowed and dropped, every field in its range. The seconds since the
 * epoch, or null for a text that is not one.
 */
function parseMoment(s: string, withTime: boolean): number | null {
  if (!/^[0-9]{4}/.test(s)) return null;
  const year = Number(s.slice(0, 4));
  let at = 4;
  const lit = (c: string): boolean => {
    if (s[at] !== c) return false;
    at++;
    return true;
  };
  const num = (min: number, max: number): number | null => {
    const r = digits(s, at, min, max);
    if (r === null) return null;
    at = r[1];
    return r[0];
  };
  if (!lit("-")) return null;
  const month = num(2, 2);
  if (month === null || !lit("-")) return null;
  const day = num(2, 2);
  if (day === null) return null;
  let secs = 0;
  if (withTime) {
    if (!lit(" ")) return null;
    const hour = num(1, 2);
    if (hour === null || !lit(":")) return null;
    const minute = num(2, 2);
    if (minute === null || !lit(":")) return null;
    const second = num(2, 2);
    if (second === null) return null;
    if ((s[at] === "." || s[at] === ",") && at + 1 < s.length && s.charCodeAt(at + 1) >= 0x30 && s.charCodeAt(at + 1) <= 0x39) {
      at++;
      while (at < s.length && s.charCodeAt(at) >= 0x30 && s.charCodeAt(at) <= 0x39) at++;
    }
    if (hour > 23 || minute > 59 || second > 59) return null;
    secs = hour * 3600 + minute * 60 + second;
  }
  if (at !== s.length) return null;
  if (month < 1 || month > 12 || day < 1 || day > daysIn(year, month)) return null;
  return daysFromCivil(year, month, day) * DAY + secs;
}

/** DATEVALUE: a date written YYYY-MM-DD; blank for an empty text. */
export function dateOfText(t: string): Value | EvalError {
  const s = trimSpace(t);
  if (s === "") return BLANK;
  const secs = parseMoment(s, false);
  if (secs === null) {
    return new EvalError(ErrorCode.BadText, "not a date: " + s);
  }
  return dateValue(Math.floor(secs / DAY));
}

/** DATETIMEVALUE: an instant written YYYY-MM-DD HH:MM:SS, or a day at midnight. */
export function dateTimeOfText(t: string): Value | EvalError {
  const s = trimSpace(t);
  if (s === "") return BLANK;
  const secs = parseMoment(s, s.length > 10);
  if (secs === null) {
    return new EvalError(ErrorCode.BadText, "not a datetime: " + s);
  }
  return dateTimeValue(secs);
}
