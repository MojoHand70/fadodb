package formula

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// Bytecode format v1 (ADR-0003). A program is a stack machine over Values
// with a constant pool, a slot table of the variables it reads, the code,
// and metadata the engine and the browser need without executing it. The
// binary form is the interchange format; the JSON form is for debugging.

// Op is an opcode. The numbering is part of the format: append only.
type Op uint8

// The opcodes. Arg is the operand of the instruction.
const (
	OpNop       Op = iota
	OpConstNum     // push Nums[arg]
	OpConstText    // push Texts[arg]
	OpTrue         // push TRUE
	OpFalse        // push FALSE
	OpBlank        // push BLANK
	OpLoad         // push the variable in slot arg
	OpNeg          // -x
	OpPercent      // x%
	OpNot          // NOT x
	OpAdd
	OpSub
	OpMul
	OpDiv       // / and \ : by zero is an error (ADR-0024)
	OpDivStrict // kept for the numbering: the same as div
	OpPow
	OpConcat
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpJump        // pc = arg
	OpJumpIfFalse // pop; when FALSE or blank, pc = arg
	OpAndJump     // when the top is FALSE or blank, replace it by FALSE and pc = arg; else pop
	OpOrJump      // when the top is TRUE, pc = arg; else pop
	OpToBool      // replace a blank top by FALSE
	OpCall        // call builtin arg>>8 with arg&0xff arguments from the stack
	OpLookup      // LOOKUP through query slot arg (ADR-0020)
	OpQuery       // reserved: QUERY through query slot arg (ADR-0020)
	OpReturn      // the top is the result
	OpConstDec    // push Decs[arg] (ADR-0022)
	OpDecNeg      // -x on an exact value; arg is the result's scale and money flag
	OpDecPercent  // x% on an exact value; arg as dec.neg
	OpDecAdd      // exact +; arg as dec.neg
	OpDecSub      // exact -; arg as dec.neg
	OpDecMul      // exact *; arg as dec.neg
	OpDecDiv      // exact /; arg as dec.neg
	OpDecScale    // bring the exact top to the static scale and kind; arg as dec.neg
	OpTry         // on an error before the matching end.try, restore the stack and pc = arg (IFERROR)
	OpEndTry      // leave the innermost try
)

// The operand of the exact operations: the result's scale in the low byte
// and, in bit 8, whether the static result is money, so that the VM can
// finish a value whose operands were blank at run time.
const decMoneyFlag = 1 << 8

func decArg(t Type) int32 {
	arg := int32(t.Scale)
	if t.Kind == KindMoney {
		arg |= decMoneyFlag
	}
	return arg
}

var opNames = [...]string{
	OpNop: "nop", OpConstNum: "const.num", OpConstText: "const.text", OpTrue: "true",
	OpFalse: "false", OpBlank: "blank", OpLoad: "load", OpNeg: "neg", OpPercent: "percent",
	OpNot: "not", OpAdd: "add", OpSub: "sub", OpMul: "mul", OpDiv: "div",
	OpDivStrict: "div.strict", OpPow: "pow", OpConcat: "concat", OpEq: "eq", OpNe: "ne",
	OpLt: "lt", OpLe: "le", OpGt: "gt", OpGe: "ge", OpJump: "jump",
	OpJumpIfFalse: "jump.if.false", OpAndJump: "and.jump", OpOrJump: "or.jump",
	OpToBool: "to.bool", OpCall: "call", OpLookup: "lookup", OpQuery: "query", OpReturn: "return",
	OpConstDec: "const.dec", OpDecNeg: "dec.neg", OpDecPercent: "dec.percent", OpDecAdd: "dec.add",
	OpDecSub: "dec.sub", OpDecMul: "dec.mul", OpDecDiv: "dec.div", OpDecScale: "dec.scale", OpTry: "try", OpEndTry: "end.try",
}

func (o Op) String() string {
	if int(o) < len(opNames) && opNames[o] != "" {
		return opNames[o]
	}
	return fmt.Sprintf("op(%d)", o)
}

// Instr is one instruction.
type Instr struct {
	Op  Op
	Arg int32
}

// Slot is one variable the program reads. The engine supplies values in
// slot order.
type Slot struct {
	Name string
	Type Type
}

// QuerySlot is a compiled LOOKUP call (ADR-0020): the source and the
// column it returns; the condition as a program of its own over the
// source's columns, named by column, and the document variables it reads
// as THIS.name, named "THIS.<variable>"; the slots of the enclosing
// program those variables live in; and, when the condition is or holds
// an equality of a column with an expression of the document alone, that
// expression as a program, so that an index on the column can serve the
// lookup (ADR-0036, ADR-0046). In place of the column the slot may carry
// an aggregate over an expression (ADR-0046). QUERY gets a slot shape of
// its own when it is built.
type QuerySlot struct {
	Source string
	Column string
	// Type is the column's type, which is the type of the lookup.
	Type Type
	// Order lists the source's key columns: "first row" is first in this
	// order.
	Order []string
	// Args are the slots of the enclosing program whose values the
	// condition reads as THIS.name, and ArgNames those variables' names,
	// so that a slot "THIS.<name>" of Cond or Key maps to Args[i].
	Args     []int32
	ArgNames []string
	// Cond is the condition; a row matches when it is TRUE, and a row on
	// which it stops with an error does not match.
	Cond *Program
	// Key is the compared expression of an equality of a column with an
	// expression of the document alone, nil when the condition has none,
	// and IndexColumn the column it is compared with. The equality is the
	// whole condition, or, when KeyPart is set, the first such term of a
	// conjunction (ADR-0046): the rows whose column equals the key are
	// then the only candidates, and the condition still decides.
	Key         *Program
	IndexColumn string
	KeyPart     bool
	// Agg names the aggregate that stands in the column position, COUNT,
	// SUM, MIN or MAX, and Value is the expression it runs over, a program
	// over the source's columns and the THIS arguments like Cond
	// (ADR-0046). Column is then empty and Type the aggregate's type.
	Agg   string
	Value *Program
}

// ArgIndex returns the position in Args of a "THIS.<name>" slot of Cond or
// Key, or -1 when the slot is a column.
func (q *QuerySlot) ArgIndex(slotName string) int {
	name, ok := strings.CutPrefix(slotName, "THIS.")
	if !ok {
		return -1
	}
	for i, a := range q.ArgNames {
		if strings.EqualFold(a, name) {
			return i
		}
	}
	return -1
}

// DecConst is an exact constant: a mantissa at a scale (ADR-0022).
type DecConst struct {
	Mant  int64
	Scale int8
}

// Program is a compiled formula.
type Program struct {
	Nums    []float64
	Decs    []DecConst
	Texts   []string
	Slots   []Slot
	Code    []Instr
	Pos     []int32 // byte offset in the source per instruction, for messages
	Result  Type
	Classes []Class // function classes used, in catalog order
	Queries []QuerySlot
	// ReadsLife says the program, or a program nested in one of its
	// lookups, calls a function of the document's life (ADR-0047), so
	// that whoever runs it knows to give the context that life. It is
	// worked out when the program is bound or decoded, not stored.
	ReadsLife bool
	// MaxStack is the stack depth the program needs.
	MaxStack int
}

// ClaimExact makes a program that is one number constant into the exact
// constant of the type, when the number is exactly a value at the type's
// scale: a default of "0" for a decimal(2) variable is 0.00, as a literal
// meeting an exact operand is read exactly (ADR-0022). It reports whether
// it did; a program that is not a lone constant, a type that is not a
// decimal, or a number that does not fit the scale leave the program as
// it is.
func (p *Program) ClaimExact(t Type) bool {
	if t.Kind != KindDecimal || len(p.Code) != 2 || p.Code[0].Op != OpConstNum || p.Code[1].Op != OpReturn {
		return false
	}
	f := p.Nums[p.Code[0].Arg]
	m, err := decFromFloat(f, int(t.Scale), rmHalfUp)
	if err != nil || decToFloat(m, int(t.Scale)) != f {
		return false
	}
	p.Decs = append(p.Decs, DecConst{Mant: m, Scale: t.Scale})
	p.Code[0] = Instr{Op: OpConstDec, Arg: int32(len(p.Decs) - 1)}
	p.Result = t
	return true
}

// readsLife reports whether the program or a program nested in one of
// its lookups calls a function of the document's life (ADR-0047).
func (p *Program) readsLife() bool {
	for _, in := range p.Code {
		if in.Op == OpCall && LifeColumns[builtins[in.Arg>>8].name] != "" {
			return true
		}
	}
	for i := range p.Queries {
		for _, nested := range []*Program{p.Queries[i].Cond, p.Queries[i].Key, p.Queries[i].Value} {
			if nested != nil && nested.readsLife() {
				return true
			}
		}
	}
	return false
}

// Format constants.
const (
	formatMagic   = "FADO"
	formatVersion = 4
)

// Encode serializes the program in the binary format.
func (p *Program) Encode() []byte {
	var b bytes.Buffer
	w := &encoder{w: &b}
	b.WriteString(formatMagic)
	w.u16(formatVersion)
	w.u16(0) // flags
	w.u32(uint32(len(p.Nums)))
	for _, f := range p.Nums {
		w.u64(math.Float64bits(f))
	}
	w.u32(uint32(len(p.Decs)))
	for _, c := range p.Decs {
		w.u64(uint64(c.Mant))
		w.u8(uint8(c.Scale))
	}
	w.u32(uint32(len(p.Texts)))
	for _, s := range p.Texts {
		w.str(s)
	}
	w.u32(uint32(len(p.Slots)))
	for _, s := range p.Slots {
		w.str(s.Name)
		w.str(s.Type.String())
	}
	w.u32(uint32(len(p.Code)))
	for _, in := range p.Code {
		w.u8(uint8(in.Op))
		w.i32(in.Arg)
	}
	w.u32(uint32(len(p.Pos)))
	for _, pos := range p.Pos {
		w.i32(pos)
	}
	w.str(p.Result.String())
	w.u32(uint32(len(p.Classes)))
	for _, c := range p.Classes {
		w.str(string(c))
	}
	w.u32(uint32(len(p.Queries)))
	for _, q := range p.Queries {
		w.str(q.Source)
		w.str(q.Column)
		w.str(q.Type.String())
		w.u32(uint32(len(q.Order)))
		for _, o := range q.Order {
			w.str(o)
		}
		w.u32(uint32(len(q.Args)))
		for i, a := range q.Args {
			w.i32(a)
			w.str(q.ArgNames[i])
		}
		w.program(q.Cond)
		w.program(q.Key)
		w.str(q.IndexColumn)
		part := uint8(0)
		if q.KeyPart {
			part = 1
		}
		w.u8(part)
		w.str(q.Agg)
		w.program(q.Value)
	}
	w.u32(uint32(p.MaxStack))
	return b.Bytes()
}

// Decode reads a program in the binary format.
func Decode(data []byte) (*Program, error) {
	if len(data) < 8 || string(data[:4]) != formatMagic {
		return nil, errors.New("bytecode: not a FadoDB program")
	}
	r := &decoder{r: bytes.NewReader(data[4:])}
	if v := r.u16(); v != formatVersion {
		return nil, fmt.Errorf("bytecode: format version %d, this build reads %d", v, formatVersion)
	}
	r.u16() // flags
	p := &Program{}
	n := r.count()
	p.Nums = make([]float64, n)
	for i := range p.Nums {
		p.Nums[i] = math.Float64frombits(r.u64())
	}
	n = r.count()
	if n > 0 {
		p.Decs = make([]DecConst, n)
		for i := range p.Decs {
			p.Decs[i].Mant = int64(r.u64())
			p.Decs[i].Scale = int8(r.u8())
			if p.Decs[i].Scale < 0 || p.Decs[i].Scale > MaxScale {
				r.err = fmt.Errorf("exact constant %d has scale %d", i, p.Decs[i].Scale)
			}
		}
	}
	n = r.count()
	p.Texts = make([]string, n)
	for i := range p.Texts {
		p.Texts[i] = r.str()
	}
	n = r.count()
	p.Slots = make([]Slot, n)
	for i := range p.Slots {
		p.Slots[i].Name = r.str()
		t, err := ParseType(r.str())
		if err != nil && r.err == nil {
			r.err = err
		}
		p.Slots[i].Type = t
	}
	n = r.count()
	p.Code = make([]Instr, n)
	for i := range p.Code {
		p.Code[i].Op = Op(r.u8())
		p.Code[i].Arg = r.i32()
	}
	n = r.count()
	if n > 0 {
		p.Pos = make([]int32, n)
		for i := range p.Pos {
			p.Pos[i] = r.i32()
		}
	}
	t, err := ParseType(r.str())
	if err != nil && r.err == nil {
		r.err = err
	}
	p.Result = t
	n = r.count()
	for i := uint32(0); i < n; i++ {
		p.Classes = append(p.Classes, Class(r.str()))
	}
	n = r.count()
	for i := uint32(0); i < n; i++ {
		q := QuerySlot{Source: r.str(), Column: r.str()}
		t, err := ParseType(r.str())
		if err != nil && r.err == nil {
			r.err = err
		}
		q.Type = t
		m := r.count()
		for j := uint32(0); j < m; j++ {
			q.Order = append(q.Order, r.str())
		}
		m = r.count()
		for j := uint32(0); j < m; j++ {
			q.Args = append(q.Args, r.i32())
			q.ArgNames = append(q.ArgNames, r.str())
		}
		q.Cond = r.program()
		q.Key = r.program()
		q.IndexColumn = r.str()
		q.KeyPart = r.u8() != 0
		q.Agg = r.str()
		q.Value = r.program()
		p.Queries = append(p.Queries, q)
	}
	p.MaxStack = int(r.u32())
	if r.err != nil {
		return nil, fmt.Errorf("bytecode: %w", r.err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("bytecode: %w", err)
	}
	p.ReadsLife = p.readsLife()
	return p, nil
}

// validate checks that every operand is in range, so that the VM can trust
// the program without bounds checks on its own tables.
func (p *Program) validate() error {
	if len(p.Code) == 0 || p.Code[len(p.Code)-1].Op != OpReturn {
		return errors.New("code must end with return")
	}
	if p.Pos != nil && len(p.Pos) != len(p.Code) {
		return errors.New("positions do not match the code")
	}
	for i, in := range p.Code {
		switch in.Op {
		case OpConstNum:
			if in.Arg < 0 || int(in.Arg) >= len(p.Nums) {
				return fmt.Errorf("instruction %d: number constant %d out of range", i, in.Arg)
			}
		case OpConstText:
			if in.Arg < 0 || int(in.Arg) >= len(p.Texts) {
				return fmt.Errorf("instruction %d: text constant %d out of range", i, in.Arg)
			}
		case OpConstDec:
			if in.Arg < 0 || int(in.Arg) >= len(p.Decs) {
				return fmt.Errorf("instruction %d: exact constant %d out of range", i, in.Arg)
			}
		case OpLoad:
			if in.Arg < 0 || int(in.Arg) >= len(p.Slots) {
				return fmt.Errorf("instruction %d: slot %d out of range", i, in.Arg)
			}
		case OpJump, OpJumpIfFalse, OpAndJump, OpOrJump, OpTry:
			if in.Arg < 0 || int(in.Arg) >= len(p.Code) {
				return fmt.Errorf("instruction %d: jump target %d out of range", i, in.Arg)
			}
		case OpDecNeg, OpDecPercent, OpDecAdd, OpDecSub, OpDecMul, OpDecDiv, OpDecScale:
			if in.Arg < 0 || in.Arg&0xff > MaxScale || in.Arg&^(0xff|decMoneyFlag) != 0 {
				return fmt.Errorf("instruction %d: scale %d out of range", i, in.Arg)
			}
		case OpCall:
			if id := int(in.Arg >> 8); id < 0 || id >= len(builtins) {
				return fmt.Errorf("instruction %d: unknown builtin %d", i, id)
			}
		case OpLookup, OpQuery:
			if in.Arg < 0 || int(in.Arg) >= len(p.Queries) {
				return fmt.Errorf("instruction %d: query slot %d out of range", i, in.Arg)
			}
			q := &p.Queries[in.Arg]
			if q.Cond == nil || len(q.Args) != len(q.ArgNames) || q.Key != nil && q.IndexColumn == "" || q.Key == nil && q.KeyPart ||
				(q.Agg == "") != (q.Value == nil) || (q.Agg == "") == (q.Column == "") || q.Agg != "" && !rowAggregates[q.Agg] {
				return fmt.Errorf("instruction %d: query slot %d is incomplete", i, in.Arg)
			}
			for _, a := range q.Args {
				if a < 0 || int(a) >= len(p.Slots) {
					return fmt.Errorf("instruction %d: query argument slot %d out of range", i, a)
				}
			}
		default:
			if int(in.Op) >= len(opNames) {
				return fmt.Errorf("instruction %d: unknown opcode %d", i, in.Op)
			}
		}
	}
	return nil
}

type encoder struct{ w *bytes.Buffer }

// program writes a nested program as a length and its encoding, a zero
// length for nil.
func (e *encoder) program(p *Program) {
	if p == nil {
		e.u32(0)
		return
	}
	b := p.Encode()
	e.u32(uint32(len(b)))
	e.w.Write(b)
}

func (e *encoder) u8(v uint8)   { e.w.WriteByte(v) }
func (e *encoder) u16(v uint16) { _ = binary.Write(e.w, binary.BigEndian, v) }
func (e *encoder) u32(v uint32) { _ = binary.Write(e.w, binary.BigEndian, v) }
func (e *encoder) u64(v uint64) { _ = binary.Write(e.w, binary.BigEndian, v) }
func (e *encoder) i32(v int32)  { _ = binary.Write(e.w, binary.BigEndian, v) }
func (e *encoder) str(s string) {
	e.u32(uint32(len(s)))
	e.w.WriteString(s)
}

type decoder struct {
	r   *bytes.Reader
	err error
}

func (d *decoder) read(v any) {
	if d.err == nil {
		d.err = binary.Read(d.r, binary.BigEndian, v)
	}
}

// program reads a nested program written by encoder.program: nil for a
// zero length.
func (d *decoder) program() *Program {
	n := d.count()
	if n == 0 || d.err != nil {
		return nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		d.err = err
		return nil
	}
	p, err := Decode(buf)
	if err != nil {
		d.err = err
		return nil
	}
	return p
}

func (d *decoder) u8() uint8   { var v uint8; d.read(&v); return v }
func (d *decoder) u16() uint16 { var v uint16; d.read(&v); return v }
func (d *decoder) u32() uint32 { var v uint32; d.read(&v); return v }

// count reads an element count and refuses one larger than the bytes left,
// so corrupt input cannot drive a huge allocation.
func (d *decoder) count() uint32 {
	v := d.u32()
	if d.err == nil && int64(v) > int64(d.r.Len()) {
		d.err = io.ErrUnexpectedEOF
	}
	return v
}
func (d *decoder) u64() uint64 { var v uint64; d.read(&v); return v }
func (d *decoder) i32() int32  { var v int32; d.read(&v); return v }
func (d *decoder) str() string {
	n := d.count()
	if d.err != nil {
		return ""
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		d.err = err
		return ""
	}
	return string(buf)
}

// debugProgram is the JSON debug form.
type debugProgram struct {
	Format   string      `json:"format"`
	Nums     []float64   `json:"nums"`
	Decs     []string    `json:"decs"`
	Texts    []string    `json:"texts"`
	Slots    []debugSlot `json:"slots"`
	Code     []string    `json:"code"`
	Result   string      `json:"result"`
	Classes  []Class     `json:"classes"`
	MaxStack int         `json:"max_stack"`
	Queries  []QuerySlot `json:"queries,omitempty"`
}

type debugSlot struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// MarshalJSON renders the debug form: one line per instruction with the
// opcode name, the operand, and what the operand refers to.
func (p *Program) MarshalJSON() ([]byte, error) {
	d := debugProgram{Format: fmt.Sprintf("%s v%d", formatMagic, formatVersion), Nums: p.Nums, Texts: p.Texts,
		Result: p.Result.String(), Classes: p.Classes, MaxStack: p.MaxStack, Queries: p.Queries}
	if d.Nums == nil {
		d.Nums = []float64{}
	}
	d.Decs = make([]string, len(p.Decs))
	for i, c := range p.Decs {
		d.Decs[i] = formatDecimal(c.Mant, int(c.Scale))
	}
	if d.Texts == nil {
		d.Texts = []string{}
	}
	if d.Classes == nil {
		d.Classes = []Class{}
	}
	d.Slots = make([]debugSlot, len(p.Slots))
	for i, s := range p.Slots {
		d.Slots[i] = debugSlot{Name: s.Name, Type: s.Type.String()}
	}
	d.Code = make([]string, len(p.Code))
	for i, in := range p.Code {
		d.Code[i] = p.describe(i, in)
	}
	return json.Marshal(d)
}

func (p *Program) describe(i int, in Instr) string {
	switch in.Op {
	case OpConstNum:
		return fmt.Sprintf("%03d %s %d (%s)", i, in.Op, in.Arg, renderNumber(p.Nums[in.Arg]))
	case OpConstText:
		return fmt.Sprintf("%03d %s %d (%q)", i, in.Op, in.Arg, p.Texts[in.Arg])
	case OpConstDec:
		return fmt.Sprintf("%03d %s %d (%s)", i, in.Op, in.Arg, formatDecimal(p.Decs[in.Arg].Mant, int(p.Decs[in.Arg].Scale)))
	case OpLoad:
		return fmt.Sprintf("%03d %s %d (%s)", i, in.Op, in.Arg, p.Slots[in.Arg].Name)
	case OpCall:
		return fmt.Sprintf("%03d %s %d (%s/%d)", i, in.Op, in.Arg, builtins[in.Arg>>8].name, in.Arg&0xff)
	case OpJump, OpJumpIfFalse, OpAndJump, OpOrJump, OpLookup, OpQuery, OpTry:
		return fmt.Sprintf("%03d %s %d", i, in.Op, in.Arg)
	case OpDecNeg, OpDecPercent, OpDecAdd, OpDecSub, OpDecMul, OpDecDiv, OpDecScale:
		kind := "decimal"
		if in.Arg&decMoneyFlag != 0 {
			kind = "money"
		}
		return fmt.Sprintf("%03d %s %d (%s scale %d)", i, in.Op, in.Arg, kind, in.Arg&0xff)
	}
	return fmt.Sprintf("%03d %s", i, in.Op)
}
