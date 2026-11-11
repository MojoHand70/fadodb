package formula

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProgram_BinaryRoundTrip(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)}, Variable{Name: "name", Type: Scalar(KindText)},
		Variable{Name: "due", Type: Scalar(KindDate)})
	p := mustBind(t, `IF(qty > 1 AND name <> "", ROUND(qty * 1.5, 2) & " " & TEXT(due), BLANK)`, scope)
	data := p.Encode()
	if string(data[:4]) != "FADO" {
		t.Fatalf("magic = %q", data[:4])
	}
	q, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(q)
	if string(a) != string(b) {
		t.Errorf("round trip changed the program:\n%s\n%s", a, b)
	}
}

func TestProgram_DecodeRejectsGarbage(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)})
	good := mustBind(t, "qty + 1", scope).Encode()
	bad := map[string][]byte{
		"empty":     {},
		"magic":     []byte("NOPE" + string(good[4:])),
		"version":   append([]byte("FADO\x00\x09"), good[6:]...),
		"truncated": good[:len(good)-3],
		"no return": func() []byte {
			p := mustBind(t, "qty + 1", scope)
			p.Code = p.Code[:len(p.Code)-1]
			p.Pos = p.Pos[:len(p.Pos)-1]
			return p.Encode()
		}(),
		"bad slot": func() []byte { p := mustBind(t, "qty + 1", scope); p.Code[0].Arg = 7; return p.Encode() }(),
		"bad call": func() []byte { p := mustBind(t, "ROUND(qty)", scope); p.Code[1].Arg = 9999 << 8; return p.Encode() }(),
	}
	for name, data := range bad {
		if _, err := Decode(data); err == nil {
			t.Errorf("%s: decoded without error", name)
		} else if !strings.HasPrefix(err.Error(), "bytecode:") {
			t.Errorf("%s: error %q should start with bytecode:", name, err)
		}
	}
}

func TestProgram_DebugForm(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)})
	p := mustBind(t, "IF(qty > 1, ROUND(qty, 2), 0)", scope)
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"format":"FADO v4"`, `"result":"number"`, `load 0 (qty)`, `call`, `(ROUND/2)`, `jump.if.false`, `return`, `"classes":["pure"]`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("debug form lacks %s:\n%s", want, out)
		}
	}
}

func TestOp_Names(t *testing.T) {
	if OpReturn.String() != "return" || Op(200).String() != "op(200)" {
		t.Error("opcode names")
	}
}

func TestProgram_BlankResultRoundTrips(t *testing.T) {
	scope := mustScope(t, numVars("a")...)
	for _, src := range []string{"BLANK", "IF(a > 0, BLANK, BLANK)"} {
		p := mustBind(t, src, scope)
		if p.Result.Kind != KindNull {
			t.Fatalf("%s: result type %s, want blank", src, p.Result)
		}
		back, err := Decode(p.Encode())
		if err != nil {
			t.Fatalf("%s: decode: %v", src, err)
		}
		if !back.Result.Equal(p.Result) {
			t.Errorf("%s: decoded result type %s, want %s", src, back.Result, p.Result)
		}
	}
}

func TestProgram_LookupRoundTrip(t *testing.T) {
	scope := mustScope(t, Variable{Name: "code", Type: Scalar(KindText)}, Variable{Name: "qty", Type: Scalar(KindNumber)})
	rates := &Source{Name: "Rates", Key: []string{"code"}, Columns: []Field{{Name: "code", Type: Scalar(KindText)}, {Name: "rate", Type: DecimalType(6)}, {Name: "min_qty", Type: Scalar(KindNumber)}}}
	opts := BindOptions{Sources: NewSourceSet(rates)}
	indexed, err := Bind("LOOKUP(Rates, rate, code = UPPER(THIS.code))", scope, opts)
	if err != nil {
		t.Fatal(err)
	}
	q := indexed.Queries[0]
	if q.Source != "Rates" || q.Column != "rate" || q.Type.String() != "decimal(6)" || q.IndexColumn != "code" || q.Key == nil ||
		len(q.Args) != 1 || indexed.Slots[q.Args[0]].Name != "code" || q.ArgNames[0] != "code" || q.Cond == nil || len(q.Order) != 1 {
		t.Errorf("indexed slot: %+v", q)
	}
	if q.ArgIndex("THIS.code") != 0 || q.ArgIndex("THIS.CODE") != 0 || q.ArgIndex("code") != -1 {
		t.Errorf("ArgIndex")
	}
	if len(indexed.Slots) != 1 || len(indexed.Classes) != 2 || indexed.Classes[1] != ClassLookup {
		t.Errorf("program: slots %v classes %v", indexed.Slots, indexed.Classes)
	}
	if q.KeyPart || q.Agg != "" || q.Value != nil {
		t.Errorf("a whole equality is not a part, and a column is no aggregate: %+v", q)
	}
	// The first equality of a conjunction with an expression of the
	// document alone is the key, and the slot says the condition still
	// decides (ADR-0046).
	narrowed, err := Bind("LOOKUP(Rates, rate, min_qty <= THIS.qty AND code = THIS.code AND rate = DECIMAL(THIS.qty, 6))", scope, opts)
	if err != nil {
		t.Fatal(err)
	}
	if q := narrowed.Queries[0]; q.Key == nil || q.IndexColumn != "code" || !q.KeyPart || len(q.Args) != 2 || len(q.Cond.Slots) != 5 {
		t.Errorf("narrowed slot: %+v", q)
	}
	scan, err := Bind("LOOKUP(Rates, rate, code = THIS.code OR min_qty <= THIS.qty)", scope, opts)
	if err != nil {
		t.Fatal(err)
	}
	if q := scan.Queries[0]; q.Key != nil || q.IndexColumn != "" || q.KeyPart || len(q.Args) != 2 || len(q.Cond.Slots) != 4 {
		t.Errorf("statement slot: %+v", q)
	}
	// An aggregate in the column position: no column, the function and
	// the expression it runs over, and the function's type.
	agg, err := Bind("LOOKUP(Rates, SUM(rate + rate), code = THIS.code AND min_qty > 0)", scope, opts)
	if err != nil {
		t.Fatal(err)
	}
	if q := agg.Queries[0]; q.Column != "" || q.Agg != "SUM" || q.Value == nil || q.Type.String() != "decimal(6)" || !agg.Result.Equal(q.Type) ||
		len(q.Value.Slots) != 1 || q.Key == nil || !q.KeyPart {
		t.Errorf("aggregate slot: %+v", q)
	}
	count, err := Bind("LOOKUP(Rates, COUNT(code), TRUE)", scope, opts)
	if err != nil {
		t.Fatal(err)
	}
	if q := count.Queries[0]; q.Agg != "COUNT" || q.Type.String() != "number" || q.Key != nil {
		t.Errorf("count slot: %+v", q)
	}
	// Among the equalities of a conjunction the one on a column that leads
	// an index is the key, wherever the author wrote it; with no index the
	// first is. A class is refused in an auto formula.
	approvals := &Source{Name: "Approval", Class: true, Key: []string{"id"}, Indexed: []string{"id", "purchase"},
		Columns: []Field{{Name: "id", Type: Scalar(KindText)}, {Name: "state", Type: Scalar(KindText)}, {Name: "purchase", Type: Scalar(KindText)}, {Name: "kind", Type: Scalar(KindText)}}}
	classes := BindOptions{Sources: NewSourceSet(approvals)}
	for cond, key := range map[string]string{
		`state = "granted" AND purchase = THIS.code`:                 "purchase",
		`purchase = THIS.code AND state = "granted"`:                 "purchase",
		`state = "granted" AND kind = "a" AND id = THIS.code`:        "id",
		`state = "granted" AND kind = "a"`:                           "state",
		`kind = "a" AND state = "granted"`:                           "kind",
		`state = "granted" AND purchase = THIS.code AND id = "A-1"`:  "purchase",
		`state = "granted" AND (purchase = THIS.code OR kind = "a")`: "state",
		`state <> "granted" AND purchase = id AND kind = THIS.code`:  "kind",
		`purchase = THIS.code`:                                       "purchase",
		`state <> "granted"`:                                         "",
	} {
		p, err := Bind("LOOKUP(Approval, COUNT(id), "+cond+")", scope, classes)
		if err != nil {
			t.Fatalf("%s: %v", cond, err)
		}
		if q := p.Queries[0]; q.IndexColumn != key || (q.Key != nil) != (key != "") || q.KeyPart != strings.Contains(cond, " AND ") && key != "" {
			t.Errorf("%s: key %q part %v, want %q", cond, q.IndexColumn, q.KeyPart, key)
		}
	}
	if _, err := Bind("LOOKUP(Approval, COUNT(id), TRUE)", scope, BindOptions{Sources: NewSourceSet(approvals), Mode: ModeAuto}); err == nil || !strings.Contains(err.Error(), "an auto formula may not read other documents") {
		t.Errorf("a class in an auto formula: %v", err)
	}
	// A slot that is neither a column nor an aggregate, or both, or whose
	// key is a part of nothing, is not a program.
	for name, spoil := range map[string]func(q *QuerySlot){
		"an aggregate with a column":   func(q *QuerySlot) { q.Column = "rate" },
		"an aggregate nobody knows":    func(q *QuerySlot) { q.Agg = "AVERAGE" },
		"an aggregate over nothing":    func(q *QuerySlot) { q.Value = nil },
		"neither column nor aggregate": func(q *QuerySlot) { q.Agg, q.Value = "", nil },
		"a part of no key":             func(q *QuerySlot) { q.Key, q.IndexColumn = nil, "" },
	} {
		bad := *agg
		bad.Queries = append([]QuerySlot(nil), agg.Queries...)
		spoil(&bad.Queries[0])
		if _, err := Decode(bad.Encode()); err == nil {
			t.Errorf("%s was decoded", name)
		}
	}
	for _, p := range []*Program{indexed, narrowed, scan, agg, count} {
		back, err := Decode(p.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(back.Encode(), p.Encode()) {
			t.Errorf("round trip changed the encoding of %+v", p)
		}
		j1, _ := json.Marshal(p)
		j2, _ := json.Marshal(back)
		if !bytes.Equal(j1, j2) {
			t.Errorf("round trip:\n%s\nwant\n%s", j2, j1)
		}
	}
	// A slot that does not fit its program is refused.
	bad := indexed.Encode()
	broken, _ := Decode(bad)
	broken.Queries[0].Args[0] = 7
	if _, err := Decode(broken.Encode()); err == nil {
		t.Error("an argument slot out of range decoded")
	}
}

func TestVM_LookupWithoutExecutor(t *testing.T) {
	scope := mustScope(t, Variable{Name: "code", Type: Scalar(KindText)})
	rates := &Source{Name: "Rates", Key: []string{"code"}, Columns: []Field{{Name: "code", Type: Scalar(KindText)}, {Name: "rate", Type: Scalar(KindNumber)}}}
	p, err := Bind("LOOKUP(Rates, rate, code = THIS.code)", scope, BindOptions{Sources: NewSourceSet(rates)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewVM().Run(p, []Value{Text("EUR")}, nil)
	if ee, ok := err.(*EvalError); !ok || ee.Code != ErrQueryFailed {
		t.Errorf("without an executor: %v", err)
	}
	m := NewMemoryExecutor()
	m.AddTable(rates, [][]Value{{Text("USD"), Number(4)}, {Text("EUR"), Number(4.5)}})
	vm := NewVM()
	vm.Executor = m
	if v, err := vm.Run(p, []Value{Text("EUR")}, nil); err != nil || v.Num() != 4.5 {
		t.Errorf("with the memory executor: %v %v", v, err)
	}
}
