package fado_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/command"
)

// TestEveryCallIsACommand walks the methods of fado.DB (ADR-0055, item
// 10): each is the call of a command, or is on the short list of what is
// not a command, with the reason. A call added to the Go API fails here
// until it has its row in the table, so that it cannot be forgotten on
// the wire.
func TestEveryCallIsACommand(t *testing.T) {
	db := reflect.TypeOf(&fado.DB{})
	methods := map[string]bool{}
	for i := 0; i < db.NumMethod(); i++ {
		methods[db.Method(i).Name] = true
	}
	calls := map[string]bool{}
	for _, c := range fado.Commands() {
		calls[c.Call] = true
		if !methods[c.Call] {
			t.Errorf("the command %s is the call %s, which fado.DB does not have", c.Name, c.Call)
		}
	}
	for name := range methods {
		_, not := command.NotCommands[name]
		switch {
		case calls[name] && not:
			t.Errorf("%s is the call of a command and is listed as not a command", name)
		case !calls[name] && !not:
			t.Errorf("fado.DB.%s is neither the call of a command nor listed as not a command: give it a row in the table of internal/command, or a line in NotCommands with the reason", name)
		}
	}
	for name := range command.NotCommands {
		if !methods[name] {
			t.Errorf("NotCommands lists %s, which fado.DB does not have", name)
		}
	}
}

// TestACommandIsNamedAfterItsCall holds the names to the rule of
// ADR-0055, item 2: the call's name in lower case with underscores.
// Reading back is the one family that is named for what it answers:
// list_ before a call that lists, and get_ for the four kinds of
// definition, which are one call.
func TestACommandIsNamedAfterItsCall(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range fado.Commands() {
		if seen[c.Name] {
			t.Errorf("two commands are named %s", c.Name)
		}
		seen[c.Name] = true
		snake := snakeCase(c.Call)
		switch {
		case c.Name == snake:
		case c.Name == "list_"+snake:
		case c.Call == "Definition" && strings.HasPrefix(c.Name, "get_"):
		default:
			t.Errorf("the command %s is the call %s, whose name is %s", c.Name, c.Call, snake)
		}
		if c.Does == "" || !strings.HasSuffix(c.Does, ".") {
			t.Errorf("the command %s does not say what it does, in a sentence", c.Name)
		}
		for _, f := range c.Fields {
			if f.Does == "" {
				t.Errorf("the member %s of %s does not say what it is", f.Name, c.Name)
			}
		}
		for _, m := range c.Answer {
			if m.Does == "" {
				t.Errorf("the member %s of the answer of %s does not say what it is", m.Name, c.Name)
			}
		}
	}
}

// snakeCase writes a Go name in lower case with underscores: CreateMany
// is create_many and SQL is sql.
func snakeCase(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// TestCommandsChapter plays the worked case of docs/manual/commands.md:
// the command the chapter shows is sent as it is written, by Bob and
// then by the agent, and the two answers the chapter shows are the
// engine's.
func TestCommandsChapter(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1)
	if len(blocks) != 3 {
		t.Fatalf("the chapter has %d blocks of JSON; this test plays three: the command, its answer and the refusal", len(blocks))
	}
	sent, answered, refusal := blocks[0][1], blocks[1][1], blocks[2][1]

	db, err := fado.Open(filepath.Join(t.TempDir(), "chapter.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	dan := fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}}
	bob := fado.Principal{ID: "bob", Kind: fado.ActorPerson, Groups: []string{"sales"}}
	agent := fado.Principal{ID: "ap-agent", Kind: fado.ActorAgent, Groups: []string{"finance"}}
	if _, err := db.Exec(ctx, dan, "define_class", []byte(`{"definition": {
	  "name": "Invoice",
	  "variables": {"customer": {"type": "text", "required": true}, "note": {"type": "text"}},
	  "states": {"initial": "review", "list": ["review", "approved"]},
	  "transitions": [{"name": "approve", "from": "review", "to": "approved"}],
	  "access": {"read": "TRUE", "create": "INGROUP(\"sales\")", "update": "TRUE", "delete": "FALSE",
	             "transition": {"approve": "INGROUP(\"finance\")"}}
	}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, bob, "create", []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"customer": "Customer:42"}}`)); err != nil {
		t.Fatal(err)
	}
	same := func(what string, got, want []byte) {
		t.Helper()
		var g, w any
		if err := json.Unmarshal(got, &g); err != nil {
			t.Fatalf("%s: the engine answered what is not JSON: %s", what, got)
		}
		if err := json.Unmarshal(want, &w); err != nil {
			t.Fatalf("%s: the chapter shows what is not JSON: %s", what, want)
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s: the chapter shows\n%s\nand the engine answers\n%s", what, want, got)
		}
	}
	got, err := db.Exec(ctx, bob, "transition", sent)
	if fado.ErrorCode(err) != "forbidden" {
		t.Fatalf("Bob's command: %v", err)
	}
	same("the refusal", got, refusal)
	got, err = db.Exec(ctx, agent, "transition", sent)
	if err != nil {
		t.Fatalf("the agent's command: %v", err)
	}
	same("the answer", got, answered)
}
