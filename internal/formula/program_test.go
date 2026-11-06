package formula

import (
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
	for _, want := range []string{`"format":"FADO v1"`, `"result":"number"`, `load 0 (qty)`, `call`, `(ROUND/2)`, `jump.if.false`, `return`, `"classes":["pure"]`} {
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
