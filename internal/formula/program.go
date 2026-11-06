package formula

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	OpDiv       // / : by zero yields 0
	OpDivStrict // \ : by zero is an error
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
	OpLookup      // reserved: LOOKUP through query slot arg (ADR-0020)
	OpQuery       // reserved: QUERY through query slot arg (ADR-0020)
	OpReturn      // the top is the result
)

var opNames = [...]string{
	OpNop: "nop", OpConstNum: "const.num", OpConstText: "const.text", OpTrue: "true",
	OpFalse: "false", OpBlank: "blank", OpLoad: "load", OpNeg: "neg", OpPercent: "percent",
	OpNot: "not", OpAdd: "add", OpSub: "sub", OpMul: "mul", OpDiv: "div",
	OpDivStrict: "div.strict", OpPow: "pow", OpConcat: "concat", OpEq: "eq", OpNe: "ne",
	OpLt: "lt", OpLe: "le", OpGt: "gt", OpGe: "ge", OpJump: "jump",
	OpJumpIfFalse: "jump.if.false", OpAndJump: "and.jump", OpOrJump: "or.jump",
	OpToBool: "to.bool", OpCall: "call", OpLookup: "lookup", OpQuery: "query", OpReturn: "return",
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

// QuerySlot is a compiled LOOKUP or QUERY call (ADR-0020). Reserved: the
// binder does not emit them yet.
type QuerySlot struct {
	Source string
	Args   []int32
}

// Program is a compiled formula.
type Program struct {
	Nums    []float64
	Texts   []string
	Slots   []Slot
	Code    []Instr
	Pos     []int32 // byte offset in the source per instruction, for messages
	Result  Type
	Classes []Class // function classes used, in catalog order
	Queries []QuerySlot
	// MaxStack is the stack depth the program needs.
	MaxStack int
}

// Format constants.
const (
	formatMagic   = "FADO"
	formatVersion = 1
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
		w.u32(uint32(len(q.Args)))
		for _, a := range q.Args {
			w.i32(a)
		}
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
		q := QuerySlot{Source: r.str()}
		m := r.count()
		for j := uint32(0); j < m; j++ {
			q.Args = append(q.Args, r.i32())
		}
		p.Queries = append(p.Queries, q)
	}
	p.MaxStack = int(r.u32())
	if r.err != nil {
		return nil, fmt.Errorf("bytecode: %w", r.err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("bytecode: %w", err)
	}
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
		case OpLoad:
			if in.Arg < 0 || int(in.Arg) >= len(p.Slots) {
				return fmt.Errorf("instruction %d: slot %d out of range", i, in.Arg)
			}
		case OpJump, OpJumpIfFalse, OpAndJump, OpOrJump:
			if in.Arg < 0 || int(in.Arg) >= len(p.Code) {
				return fmt.Errorf("instruction %d: jump target %d out of range", i, in.Arg)
			}
		case OpCall:
			if id := int(in.Arg >> 8); id < 0 || id >= len(builtins) {
				return fmt.Errorf("instruction %d: unknown builtin %d", i, id)
			}
		case OpLookup, OpQuery:
			if in.Arg < 0 || int(in.Arg) >= len(p.Queries) {
				return fmt.Errorf("instruction %d: query slot %d out of range", i, in.Arg)
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
	case OpLoad:
		return fmt.Sprintf("%03d %s %d (%s)", i, in.Op, in.Arg, p.Slots[in.Arg].Name)
	case OpCall:
		return fmt.Sprintf("%03d %s %d (%s/%d)", i, in.Op, in.Arg, builtins[in.Arg>>8].name, in.Arg&0xff)
	case OpJump, OpJumpIfFalse, OpAndJump, OpOrJump, OpLookup, OpQuery:
		return fmt.Sprintf("%03d %s %d", i, in.Op, in.Arg)
	}
	return fmt.Sprintf("%03d %s", i, in.Op)
}
