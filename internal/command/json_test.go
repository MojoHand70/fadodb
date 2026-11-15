package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// An answer writes <, > and & as themselves (ADR-0055, amended
// 2026-11-15): in a definition read back, in a refusal with the rule
// that refused, and in a value of a document. So a formula is read at a
// terminal, and by an agent, as its author wrote it.
func TestAnAnswerWritesAFormulaAsItIsWritten(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	const class = `{
  "name": "Order",
  "title": "\"Order \" & number",
  "variables": {
    "number": {"type": "text", "required": true},
    "amount": {"type": "decimal", "scale": 2, "default": "0"},
    "note":   {"type": "text"}
  },
  "states": {"initial": "draft", "list": ["draft", "sent"]},
  "transitions": [{"name": "send", "from": "draft", "to": "sent"}],
  "access": {
    "read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE",
    "transition": {"send": "amount > 0 AND amount < 1000"}
  }
}`
	exec := func(name, object string) string {
		t.Helper()
		answer, _ := Exec(ctx, s, dan, name, []byte(object))
		if !json.Valid(answer) {
			t.Fatalf("%s answered what is not JSON: %s", name, answer)
		}
		return string(answer)
	}
	for what, c := range map[string]struct{ answer, holds string }{
		"a definition read back": {exec("define_class", `{"definition": `+class+`}`), `"title":"\"Order \" & number"`},
		"a rule read back":       {exec("get_class", `{"name": "Order"}`), `"send":"amount > 0 AND amount < 1000"`},
		"a value of a document":  {exec("create", `{"class": "Order", "id": "O-1", "values": {"number": "1", "note": "<b>R&D</b>"}}`), `"note":"<b>R&D</b>"`},
		"the hint of a refusal":  {exec("transition", `{"class": "Order", "id": "O-1", "transition": "send"}`), `amount > 0 AND amount < 1000`},
	} {
		if !strings.Contains(c.answer, c.holds) {
			t.Errorf("%s: the answer does not hold %s:\n%s", what, c.holds, c.answer)
		}
		if strings.Contains(c.answer, `\u003`) || strings.Contains(c.answer, `\u0026`) {
			t.Errorf("%s: the answer escapes a character a formula is written with:\n%s", what, c.answer)
		}
	}

	// What is escaped stays escaped: an answer is one line, and reads
	// back as the value it was.
	for _, text := range []string{"a \"quoted\" \\ text", "line\nbreak\ttab", "<>&", "\u2028\u2029", "zażółć gęślą jaźń"} {
		written, err := Marshal(text)
		if err != nil || strings.ContainsAny(string(written), "\n\u2028\u2029") {
			t.Errorf("Marshal(%q) = %s, %v", text, written, err)
		}
		var back string
		if err := json.Unmarshal(written, &back); err != nil || back != text {
			t.Errorf("Marshal(%q) reads back as %q, %v", text, back, err)
		}
	}
	if written, _ := Marshal("<>&"); string(written) != `"<>&"` {
		t.Errorf(`Marshal("<>&") = %s`, written)
	}
	// A value encoding/json cannot write is refused, and the next one
	// is written.
	if _, err := Marshal(func() {}); err == nil {
		t.Error("Marshal wrote a function")
	}
	if written, err := Marshal(map[string]int{"a": 1}); err != nil || string(written) != `{"a":1}` {
		t.Errorf("after a failure Marshal wrote %s, %v", written, err)
	}
}
