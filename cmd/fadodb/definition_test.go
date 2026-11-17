package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// samples is where the classes of the examples are.
var samples = filepath.Join("..", "..", "examples", "classes")

// bare takes the directory off the names of the files in what the tool
// wrote, so that it reads as the manual shows it.
func bare(text, dir string) string {
	return strings.ReplaceAll(text, dir+string(os.PathSeparator), "")
}

// page reads a page of the documentation.
func page(t *testing.T, path ...string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "docs"}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// The check of a class from a file, as the manual shows it: a class
// that names one the database does not have is refused with the place
// and the fix, and the two together are taken.
func TestTheCheckOfAClass(t *testing.T) {
	chapter := page(t, "manual", "class.md")
	invoice, receipt := filepath.Join(samples, "invoice.json"), filepath.Join(samples, "receipt.json")
	if got := blockOf(t, chapter, "the check of a class"); got != "fadodb class check invoice.json" {
		t.Errorf("the chapter checks a class with %q", got)
	}
	if got := blockOf(t, chapter, "the check of two classes"); got != "fadodb class check receipt.json invoice.json" {
		t.Errorf("the chapter checks two classes with %q", got)
	}

	// Alone, the invoice is refused: its rule makes a receipt.
	code, out, said := tool("class", "check", invoice)
	if want := blockOf(t, chapter, "what the check says") + "\n"; code != 1 || bare(out, samples) != want || said != "" {
		t.Errorf("the check of the invoice alone: exit %d\n%s%s\nand the chapter shows\n%s", code, bare(out, samples), said, want)
	}
	// Together, in either order of the files, both are taken.
	code, out, said = tool("class", "check", receipt, invoice)
	if want := blockOf(t, chapter, "what the check says of two") + "\n"; code != 0 || bare(out, samples) != want || said != "" {
		t.Errorf("the check of both: exit %d\n%s%s\nand the chapter shows\n%s", code, bare(out, samples), said, want)
	}
	if code, out, _ := tool("class", "check", invoice, receipt); code != 0 || strings.Count(out, "the engine takes it") != 2 {
		t.Errorf("the check of both, the invoice first: exit %d\n%s", code, out)
	}

	// For a program: what the command validate_class answers, and the
	// same exit.
	code, out, _ = tool("class", "check", invoice, "--json")
	if want := blockOf(t, chapter, "the findings for a program") + "\n"; code != 1 || out != want {
		t.Errorf("the findings for a program: exit %d\n%s\nand the chapter shows\n%s", code, out, want)
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "empty.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	definition, err := os.ReadFile(invoice)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := db.Exec(context.Background(), fado.Principal{}, "validate_class", []byte(`{"definition": `+string(definition)+`}`))
	if err != nil || string(answer)+"\n" != out {
		t.Errorf("validate_class answers %s (%v), and the tool prints %s", answer, err, out)
	}
	if code, out, _ := tool("class", "check", receipt, "--json"); code != 0 || out != `{"diagnostics":[]}`+"\n" {
		t.Errorf("a class the engine takes, for a program: exit %d %s", code, out)
	}

	// A place inside a formula, and a finding that refuses nothing.
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	bill := write("bill.json", `{"name": "Bill", "variables": {"net": {"type": "number"}, "total": {"formula": "net + nett"}},
	  "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE"}}`)
	code, out, _ = tool("class", "check", bill)
	if code != 1 || !strings.Contains(out, "bill.json: 1 finding refuses it\n  bad_formula at /variables/total/formula, offset 6: ") {
		t.Errorf("a formula that names no variable: exit %d\n%s", code, out)
	}
	memo := write("memo.json", `{"name": "Memo", "variables": {"text": {"type": "text", "description": "What the memo says."}},
	  "states": {"initial": "new", "list": ["new"], "descriptions": {"new": "Written, and read by anyone."}}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE"}}`)
	// A class that leaves a rule out is taken, and told so in a note,
	// with what to write.
	if code, out, _ := tool("class", "check", memo); code != 0 || !strings.Contains(out, "memo.json: the engine takes it, with 1 note\n  note: rule_left_out at /access/delete: ") || !strings.Contains(out, "\n    fix: add access.delete") {
		t.Errorf("a class with a note alone: exit %d\n%s", code, out)
	}
	// Two files, of which one is refused: each finding stands under its
	// file, with its place in that file.
	code, out, _ = tool("class", "check", memo, bill)
	if code != 1 || !strings.Contains(out, "memo.json: nothing refuses it\n") || !strings.Contains(out, "bill.json: 1 finding refuses it\n  bad_formula at /variables/total/formula, offset 6: ") || strings.Contains(out, " at /1/") {
		t.Errorf("two files, one refused: exit %d\n%s", code, out)
	}

	// Standard input, and what is not a definition.
	stdin = strings.NewReader(`{"name": `)
	if code, out, _ := tool("class", "check", "-"); code != 1 || !strings.Contains(out, "standard input: 1 finding refuses it\n  bad_json at the definition: ") {
		t.Errorf("standard input that is not JSON: exit %d\n%s", code, out)
	}
	stdin = os.Stdin
	for name, c := range map[string]struct {
		args []string
		code int
		says string
	}{
		"a file that is not there": {[]string{"class", "check", filepath.Join(dir, "nothing.json")}, 2, "nothing.json"},
		"no file":                  {[]string{"class", "check"}, 2, "give the file of the definition"},
		"no check":                 {[]string{"class"}, 2, "fadodb class check FILE..."},
		"another word":             {[]string{"class", "define", memo}, 2, "fadodb class check FILE..."},
		"help":                     {[]string{"class", "help"}, 0, "fadodb class check FILE..."},
		"help of the check":        {[]string{"class", "check", "--help"}, 0, "--json"},
		"a flag it does not have":  {[]string{"class", "check", memo, "--settings", "x"}, 2, "settings"},
	} {
		if code, out, said := tool(c.args...); code != c.code || !strings.Contains(said, c.says) || out != "" {
			t.Errorf("%s: exit %d, want %d\n%s%s", name, code, c.code, out, said)
		}
	}
	if !strings.Contains(usage, "fadodb class check FILE...") || !strings.Contains(usage, "fadodb action check FILE") {
		t.Error("the help of the tool does not name the checks")
	}
}

// The check against a database: what the database has is what a
// definition may name, and nothing is written to it.
func TestTheCheckAgainstADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	invoice, receipt := filepath.Join(samples, "invoice.json"), filepath.Join(samples, "receipt.json")
	if code, _, said := tool("class", "check", invoice, "--db", path); code != 1 || !strings.Contains(said, "there is no database at "+path) {
		t.Fatalf("a check against a database that is not there: exit %d\n%s", code, said)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the check made a database: %v", err)
	}
	tool("init", path)
	tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path)
	// The receipt is defined in the database; the invoice then passes
	// alone, and is not defined by the check.
	ctx := context.Background()
	db, err := fado.Open(path, fado.Options{Users: true})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, definition, fado.As(fado.Principal{ID: "olga", System: []string{fado.GroupDesigners}})); err != nil {
		t.Fatal(err)
	}
	had := len(db.Classes())
	db.Close()
	if code, out, said := tool("class", "check", invoice, "--db", path); code != 0 || !strings.Contains(out, "invoice.json: the engine takes it\n") {
		t.Errorf("the invoice against a database that has the receipt: exit %d\n%s%s", code, out, said)
	}
	if code, _, _ := tool("class", "check", invoice); code != 1 {
		t.Errorf("the invoice against an empty database: exit %d", code)
	}
	db, err = fado.Open(path, fado.Options{Users: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(db.Classes()); got != had {
		t.Errorf("the database has %d classes after the check, and had %d", got, had)
	}
	db.Close()

	// A database that a server holds is asked through the server.
	_, key, _ := tool("key", "new", "olga", "--db", path)
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0")
	code, _, said := tool("class", "check", invoice, "--db", path)
	if code != 1 || !strings.Contains(said, "is held by fadodb serve") || !strings.Contains(said, "send it the command validate_class instead") {
		t.Errorf("a check against a served database: exit %d\n%s", code, said)
	}
	definition, _ = os.ReadFile(invoice)
	status, body := ask(t, &http.Client{Timeout: 10 * time.Second}, "POST", "http://"+addr+"/v1/validate_class", strings.TrimSpace(key), `{"definition": `+string(definition)+`}`)
	if status != 200 || body != `{"diagnostics":[]}` {
		t.Errorf("validate_class at the server: %d %s", status, body)
	}
}

// The check of an action from a file, as the page on actions shows it:
// the tool answers what the server would with the same settings.
func TestTheCheckOfAnAction(t *testing.T) {
	actions := page(t, "actions.md")
	// The definition the page defines, and the settings it gives the
	// check.
	_, definition, found := strings.Cut(actions, "## An action for a REST API")
	_, definition, _ = strings.Cut(definition, "```json\n")
	definition, _, _ = strings.Cut(definition, "\n```")
	if !found || !strings.Contains(definition, `"name": "tickets.create"`) {
		t.Fatal("the page on actions does not define tickets.create")
	}
	dir := t.TempDir()
	tickets, settings := filepath.Join(dir, "tickets.json"), filepath.Join(dir, "settings.json")
	for path, text := range map[string]string{tickets: definition, settings: blockOf(t, actions, "the settings of the check")} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := blockOf(t, actions, "the check of an action"); got != "fadodb action check tickets.json --settings settings.json" {
		t.Errorf("the page checks an action with %q", got)
	}
	code, out, said := tool("action", "check", tickets, "--settings", settings)
	if want := blockOf(t, actions, "what the check says") + "\n"; code != 0 || bare(out, dir) != want || said != "" {
		t.Errorf("the check with the settings: exit %d\n%s%s\nand the page shows\n%s", code, bare(out, dir), said, want)
	}
	code, out, _ = tool("action", "check", tickets)
	if want := blockOf(t, actions, "what the check says without settings") + "\n"; code != 1 || bare(out, dir) != want {
		t.Errorf("the check without settings: exit %d\n%s\nand the page shows\n%s", code, bare(out, dir), want)
	}

	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// Settings that let the connection reach another host: the place
	// is the address of the request.
	elsewhere := write("elsewhere.json", `{"outbound": {"connections": {"tickets": ["other.example.com"]}}}`)
	if code, out, _ := tool("action", "check", tickets, "--settings", elsewhere); code != 1 || !strings.Contains(out, "  not_allowed at /request/url: ") {
		t.Errorf("settings that name another host: exit %d\n%s", code, out)
	}
	// A definition with no request is the contract of a Go function,
	// and the tool has none, as fadodb serve has none.
	mail := write("mail.json", `{"name": "mail.send", "params": {"to": "text"}}`)
	code, out, _ = tool("action", "check", mail, "--json")
	var got struct {
		Diagnostics []struct{ Code, Path string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 1 || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "not_registered" || got.Diagnostics[0].Path != "/name" {
		t.Errorf("an action of a Go function, for a program: exit %d %s", code, out)
	}
	for name, c := range map[string]struct {
		args []string
		code int
		says string
	}{
		"two files":                    {[]string{"action", "check", tickets, mail}, 2, "one at a time"},
		"settings that are not there":  {[]string{"action", "check", tickets, "--settings", filepath.Join(dir, "nothing.json")}, 2, "the settings"},
		"settings with a wrong member": {[]string{"action", "check", tickets, "--settings", write("wrong.json", `{"outbond": {}}`)}, 2, "outbond"},
		"help":                         {[]string{"action", "help"}, 0, "fadodb action check FILE"},
	} {
		if code, out, said := tool(c.args...); code != c.code || !strings.Contains(said, c.says) || out != "" {
			t.Errorf("%s: exit %d, want %d\n%s%s", name, code, c.code, out, said)
		}
	}
}
