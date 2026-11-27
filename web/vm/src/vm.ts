// The browser VM: it runs a program of the engine over a stack of values,
// as the Go VM does (internal/formula/vm.go), so that a page works out a
// document's formulas as the server would, without asking it (ADR-0003,
// ADR-0029). Errors are the closed list of the engine; IFERROR catches
// them as the Go VM does.

import { compare, builtins } from "./builtins.js";
import { Op, setBuiltinCount, type Program } from "./bytecode.js";
import { EvalError, errDivByZero, errNotANumber, errServer, tooLong } from "./errors.js";
import { compareExact, decBinary, decUnary } from "./exact.js";
import { pow } from "./numbers.js";
import { render } from "./render.js";
import { longerThan } from "./text.js";
import { BLANK, FALSE, Kind, TRUE, Value, bool, decimal, isExact, number, text } from "./value.js";

setBuiltinCount(builtins.length);

/** The default text limit of a database: 65,536 code points (ADR-0021). */
export const DEFAULT_MAX_TEXT = 65536;

/** The deepest nesting of IFERROR, as the binder allows it. */
const MAX_TRIES = 16;

/**
 * What a document's life says of one state it has been in (ADR-0047, item
 * 6), in the form the engine writes it: the moments in seconds, who made
 * each move, how many times it entered, how long its stays that ended
 * lasted, whether it is there now, and who first and last changed it
 * there.
 */
export interface StateLife {
  state: string;
  /** First entered, and by whom. */
  fe?: number;
  feb?: string;
  /** Last entered, and by whom. */
  le?: number;
  leb?: string;
  /** First left, and by whom. */
  fl?: number;
  flb?: string;
  /** Last left, and by whom. */
  ll?: number;
  llb?: string;
  /** How many times it entered. */
  n?: number;
  /** The seconds of the stays that ended. */
  secs?: number;
  /** Whether the document is in the state now. */
  here?: boolean;
  /** Who first and last changed it in the state. */
  fed?: string;
  led?: string;
}

/** What a document keeps of its life (ADR-0047), for the functions that read it. */
export interface Life {
  version?: number;
  /** Moments in seconds since the epoch, and who. */
  created_at?: number;
  created_by?: string;
  updated_at?: number;
  updated_by?: string;
  entered_at?: number;
  entered_by?: string;
  entered_from?: string;
  /** Each state it has been in, in the order it first entered them. */
  states?: readonly StateLife[];
}

/** What the context functions see (ADR-0015, ADR-0050, ADR-0052). */
export interface Context {
  /** The id of the user the formula runs for. */
  user?: string;
  /** The groups the user is in. */
  groups?: readonly string[];
  /** The system groups the user is in. */
  system?: readonly string[];
  /** The roles the user holds, each written app.role (ADR-0100). */
  roles?: readonly string[];
  /** person, agent or service. */
  kind?: string;
  /** The id of whoever the user acts for. */
  for?: string;
  /** The moment, in seconds since the epoch, UTC; the clock when left out. */
  now?: number;
  /** The state of the document. */
  state?: string;
  /** The id of the document. */
  id?: string;
  /** The document's life. */
  doc?: Life;
}

/** The options of a VM. */
export interface Options {
  /** The longest text a formula may make, in code points (ADR-0021). */
  maxText?: number;
}

/** The text of the first instruction an error stopped at, with its offset. */
function located(err: EvalError, p: Program, pc: number): EvalError {
  const pos = p.pos !== null ? p.pos[pc]! : -1;
  return new EvalError(err.code, err.detail, pos);
}

/** Runs programs. One VM may run any number of programs, one at a time. */
export class VM {
  /** The text limit, in code points. */
  readonly maxText: number;
  /** The context of the program being run. */
  ctx: Context | undefined;
  private stack: Value[] = [];
  private readonly tryPc = new Int32Array(MAX_TRIES);
  private readonly trySp = new Int32Array(MAX_TRIES);

  constructor(options: Options = {}) {
    this.maxText = options.maxText && options.maxText > 0 ? options.maxText : DEFAULT_MAX_TEXT;
  }

  /** The moment of the context in seconds, the clock when it has none. */
  now(): number {
    return this.ctx?.now ? this.ctx.now : Math.floor(Date.now() / 1000);
  }

  /**
   * Runs the program over the values of its variables, one for each of
   * its slots, in slot order, and answers its value. It throws the
   * EvalError the formula stopped with.
   */
  run(p: Program, vars: readonly Value[], ctx?: Context): Value {
    if (vars.length < p.slots.length) {
      throw new EvalError("query_failed", `program reads ${p.slots.length} variables, got ${vars.length}`);
    }
    const stack = this.stack;
    while (stack.length < p.maxStack) {
      stack.push(BLANK);
    }
    this.ctx = ctx;
    const ops = p.ops;
    const args = p.args;
    let nt = 0;
    let sp = 0;
    let pc = 0;
    for (;;) {
      const op = ops[pc]!;
      const arg = args[pc]!;
      pc++;
      let err: EvalError | null = null;
      switch (op) {
        case Op.Nop:
          break;
        case Op.ConstNum:
          stack[sp++] = number(p.nums[arg]!);
          break;
        case Op.ConstText:
          stack[sp++] = text(p.texts[arg]!);
          break;
        case Op.True:
          stack[sp++] = TRUE;
          break;
        case Op.False:
          stack[sp++] = FALSE;
          break;
        case Op.Blank:
          stack[sp++] = BLANK;
          break;
        case Op.Load:
          stack[sp++] = vars[arg]!;
          break;
        case Op.Neg:
          stack[sp - 1] = number(-stack[sp - 1]!.num);
          break;
        case Op.Percent:
          stack[sp - 1] = number(stack[sp - 1]!.num / 100);
          break;
        case Op.Not:
          stack[sp - 1] = bool(!stack[sp - 1]!.truth);
          break;
        case Op.Add:
        case Op.Sub:
        case Op.Mul:
        case Op.Pow: {
          const a = stack[sp - 2]!.num;
          const b = stack[sp - 1]!.num;
          const r = op === Op.Add ? a + b : op === Op.Sub ? a - b : op === Op.Mul ? a * b : pow(a, b);
          if (!Number.isFinite(r)) {
            err = errNotANumber;
            break;
          }
          sp--;
          stack[sp - 1] = number(r);
          break;
        }
        case Op.Div:
        case Op.DivStrict: {
          const a = stack[sp - 2]!.num;
          const b = stack[sp - 1]!.num;
          if (b === 0) {
            err = errDivByZero;
            break;
          }
          const r = a / b;
          if (!Number.isFinite(r)) {
            err = errNotANumber;
            break;
          }
          sp--;
          stack[sp - 1] = number(r);
          break;
        }
        case Op.ConstDec: {
          const c = p.decs[arg]!;
          stack[sp++] = decimal(c.mant, c.scale);
          break;
        }
        case Op.DecNeg:
        case Op.DecPercent:
        case Op.DecScale: {
          const r = decUnary(op === Op.DecScale, op === Op.DecNeg, stack[sp - 1]!, arg);
          if (r instanceof EvalError) {
            err = r;
            break;
          }
          stack[sp - 1] = r;
          break;
        }
        case Op.DecAdd:
        case Op.DecSub:
        case Op.DecMul:
        case Op.DecDiv: {
          const r = decBinary((op - Op.DecAdd) as 0 | 1 | 2 | 3, stack[sp - 2]!, stack[sp - 1]!, arg);
          if (r instanceof EvalError) {
            err = r;
            break;
          }
          sp--;
          stack[sp - 1] = r;
          break;
        }
        case Op.Concat: {
          const s = render(stack[sp - 2]!) + render(stack[sp - 1]!);
          if (longerThan(s, this.maxText)) {
            err = tooLong();
            break;
          }
          sp--;
          stack[sp - 1] = text(s);
          break;
        }
        case Op.Eq:
        case Op.Ne:
        case Op.Lt:
        case Op.Le:
        case Op.Gt:
        case Op.Ge: {
          const a = stack[sp - 2]!;
          const b = stack[sp - 1]!;
          let c: number;
          if (isExact(a.kind) || isExact(b.kind)) {
            const r = compareExact(a, b, arg);
            if (r instanceof EvalError) {
              err = r;
              break;
            }
            c = r;
          } else {
            c = compare(a, b);
          }
          sp--;
          stack[sp - 1] = bool(op === Op.Eq ? c === 0 : op === Op.Ne ? c !== 0 : op === Op.Lt ? c < 0 : op === Op.Le ? c <= 0 : op === Op.Gt ? c > 0 : c >= 0);
          break;
        }
        case Op.Jump:
          pc = arg;
          break;
        case Op.JumpIfFalse:
          sp--;
          if (!stack[sp]!.truth) {
            pc = arg;
          }
          break;
        case Op.AndJump:
          if (!stack[sp - 1]!.truth) {
            stack[sp - 1] = FALSE;
            pc = arg;
          } else {
            sp--;
          }
          break;
        case Op.OrJump:
          if (stack[sp - 1]!.truth) {
            pc = arg;
          } else {
            sp--;
          }
          break;
        case Op.ToBool:
          if (stack[sp - 1]!.kind === Kind.Blank) {
            stack[sp - 1] = FALSE;
          }
          break;
        case Op.Try:
          this.tryPc[nt] = arg;
          this.trySp[nt] = sp;
          nt++;
          break;
        case Op.EndTry:
          nt--;
          break;
        case Op.Call: {
          const argc = arg & 0xff;
          const r = builtins[arg >> 8]!.fn(this, stack, sp - argc, argc);
          if (r instanceof EvalError) {
            err = r;
            break;
          }
          sp -= argc;
          stack[sp++] = r;
          break;
        }
        case Op.Lookup:
        case Op.Query:
          err = errServer;
          break;
        case Op.Return:
          return stack[sp - 1]!;
        default:
          err = new EvalError("query_failed", `unknown opcode ${op}`);
      }
      if (err !== null) {
        if (nt > 0) {
          // IFERROR: drop what the failed value left and run the fallback.
          nt--;
          sp = this.trySp[nt]!;
          pc = this.tryPc[nt]!;
          continue;
        }
        throw located(err, p, pc - 1);
      }
    }
  }
}
