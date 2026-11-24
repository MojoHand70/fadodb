package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/starter"
)

// startersChapter reads the chapter of the manual on starters.
func startersChapter(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "starters.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// sameJSON fails the test when two JSON texts do not hold the same
// value.
func sameJSON(t *testing.T, what, written string, held json.RawMessage) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(written), &a); err != nil {
		t.Fatalf("%s, as the chapter writes it, is not JSON: %v", what, err)
	}
	if err := json.Unmarshal(held, &b); err != nil {
		t.Fatalf("%s, as the starter holds it, is not JSON: %v", what, err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s of the chapter is not the one the starter makes:\n%s", what, held)
	}
}

// definitionOf is the definition a command of a starter sends, or the
// object itself for a command that takes no definition.
func definitionOf(c starter.Command) json.RawMessage {
	var o struct {
		Definition json.RawMessage `json:"definition"`
	}
	if json.Unmarshal(c.Object, &o) == nil && len(o.Definition) > 0 {
		return o.Definition
	}
	return c.Object
}

// execLines reads the lines of a block that send a command, as
// fadodb exec NAME 'OBJECT', into their names and objects.
func execLines(t *testing.T, block string) (names, objects []string) {
	t.Helper()
	for _, line := range strings.Split(block, "\n") {
		rest, ok := strings.CutPrefix(line, "fadodb exec ")
		name, object, quoted := strings.Cut(rest, " '")
		if !ok || !quoted || !strings.HasSuffix(object, "'") {
			t.Fatalf("the line %q sends no command", line)
		}
		names, objects = append(names, name), append(objects, strings.TrimSuffix(object, "'"))
	}
	return names, objects
}

// answered is what the test reads of an answer of the server: the
// document it answers, or the refusal.
type answered struct {
	Document struct {
		State  string         `json:"state"`
		Values map[string]any `json:"values"`
	} `json:"document"`
	Rows  []map[string]any `json:"rows"`
	Error struct {
		Code string `json:"code"`
		Path string `json:"path"`
	} `json:"error"`
}

// The chapter on starters, played against a real server: the database
// made with the starter expenses, what it made, the people of the
// chapter, and its expenses row by row.
func TestTheChapterOnStarters(t *testing.T) {
	chapter := startersChapter(t)
	expenses, err := starter.Get("expenses")
	if err != nil {
		t.Fatal(err)
	}

	// The four commands, as the chapter writes them.
	for _, line := range []string{
		"fadodb init acme.fado --starter expenses",
		"fadodb user add olga --kind person --system administrators --db acme.fado",
		"fadodb key new olga --label laptop --db acme.fado",
		"fadodb serve acme.fado",
	} {
		if !strings.Contains(chapter, line+"\n") {
			t.Errorf("the chapter does not have the command %q", line)
		}
	}
	if !strings.Contains(initUsage, "--starter NAME") || !strings.Contains(initUsage, "expenses: ") {
		t.Errorf("the help of init does not name the starter:\n%s", initUsage)
	}

	// Made with the starter, init says what it made.
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	code, out, said := tool("init", path, "--starter", "expenses")
	if code != 0 {
		t.Fatalf("init with the starter: exit %d\n%s%s", code, out, said)
	}
	if got := strings.ReplaceAll(strings.TrimSpace(out), path, "acme.fado"); got != blockOf(t, chapter, "what init says") {
		t.Errorf("init says:\n%s", got)
	}
	if !strings.Contains(said, "then, for the starter expenses: "+expenses.Next) {
		t.Errorf("init does not say what to do next with the starter:\n%s", said)
	}

	// What the starter makes is what the chapter lists, and the
	// definitions the chapter shows are those it sends.
	rows := rowsOf(t, chapter, "what expenses makes")
	if len(rows) != len(expenses.Commands) {
		t.Fatalf("the chapter lists %d things, the starter makes %d", len(rows), len(expenses.Commands))
	}
	for i, c := range expenses.Commands {
		var words struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(definitionOf(c), &words); err != nil {
			t.Fatal(err)
		}
		if got := rows[i][0] + " " + strings.Trim(rows[i][1], "`"); got != c.Made() || rows[i][2] != words.Description {
			t.Errorf("row %d of what the starter makes: %q, %q; the starter makes %s: %q", i+1, got, rows[i][2], c.Made(), words.Description)
		}
		switch c.Command {
		case "define_app":
			sameJSON(t, "the app Expenses", blockOf(t, chapter, "an app"), definitionOf(c))
		case "define_table":
			sameJSON(t, "the table Employees", blockOf(t, chapter, "the table Employees"), definitionOf(c))
		case "define_class":
			sameJSON(t, "the class Expense", blockOf(t, chapter, "the class Expense"), definitionOf(c))
		}
	}

	// The first administrator, and the server.
	if code, out, said := tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path); code != 0 {
		t.Fatalf("user add: exit %d\n%s%s", code, out, said)
	}
	code, olga, said := tool("key", "new", "olga", "--label", "laptop", "--db", path)
	if code != 0 {
		t.Fatalf("key new: exit %d\n%s", code, said)
	}
	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	defer stop()
	base := "http://" + addr
	client := &http.Client{Timeout: 10 * time.Second}
	keys := map[string]string{"olga": strings.TrimSpace(olga)}
	send := func(who, name, object string) (int, answered) {
		t.Helper()
		status, body := ask(t, client, "POST", base+"/v1/"+name, keys[who], object)
		var a answered
		if err := json.Unmarshal([]byte(body), &a); err != nil {
			t.Fatalf("%s %s answered %d, no JSON: %s", who, name, status, body)
		}
		return status, a
	}

	// The definitions were made on the machine, by no user.
	status, body := ask(t, client, "POST", base+"/v1/get_class", keys["olga"], `{"name": "Expense"}`)
	var class struct {
		Class struct {
			Defined map[string]any `json:"defined"`
		} `json:"class"`
	}
	if json.Unmarshal([]byte(body), &class); status != 200 || class.Class.Defined["at"] == nil || len(class.Class.Defined) != 1 {
		t.Errorf("the class of the starter says who defined it: %d %s", status, body)
	}
	if status, body := ask(t, client, "POST", base+"/v1/list_groups", keys["olga"], `{}`); status != 200 ||
		!strings.Contains(body, `"name":"finance","label":"Finance"`) || !strings.Contains(body, `"members":0`) {
		t.Errorf("the groups of the starter: %d %s", status, body)
	}

	// The people, made by Olga as the chapter writes them, each with a
	// key.
	names, objects := execLines(t, blockOf(t, chapter, "the people"))
	for i := range names {
		if status, a := send("olga", names[i], objects[i]); status != 200 {
			t.Fatalf("%s %s: %d %+v", names[i], objects[i], status, a.Error)
		}
		var person struct {
			ID string `json:"id"`
		}
		json.Unmarshal([]byte(objects[i]), &person)
		status, body := ask(t, client, "POST", base+"/v1/create_key", keys["olga"], `{"user": "`+person.ID+`", "label": "chapter"}`)
		var made struct {
			Key struct {
				Secret string `json:"secret"`
			} `json:"key"`
		}
		if json.Unmarshal([]byte(body), &made); status != 200 || made.Key.Secret == "" {
			t.Fatalf("a key for %s: %d %s", person.ID, status, body)
		}
		keys[person.ID] = made.Key.Secret
	}
	names, objects = execLines(t, blockOf(t, chapter, "the managers"))
	for i := range names {
		if status, a := send("hanna", names[i], objects[i]); status != 200 {
			t.Fatalf("Hanna writes the managers: %d %+v", status, a.Error)
		}
	}

	// The expenses, row by row.
	expense := func(id string) string { return `"class": "Expense", "id": "` + id + `"` }
	file := func(who, id, what, amount string) answered {
		t.Helper()
		_, a := send(who, "create", `{`+expense(id)+`, "values": {"what": "`+what+`", "amount": "`+amount+`"}}`)
		return a
	}
	move := func(who, id, transition string, forWhom ...string) answered {
		t.Helper()
		object := `{` + expense(id) + `, "transition": "` + transition + `"`
		if len(forWhom) > 0 {
			object += `, "for": "` + forWhom[0] + `"`
		}
		_, a := send(who, "transition", object+`}`)
		return a
	}
	is := func(a answered, state string, values ...string) bool {
		if a.Error.Code != "" || a.Document.State != state {
			return false
		}
		for i := 0; i+1 < len(values); i += 2 {
			if a.Document.Values[values[i]] != values[i+1] {
				return false
			}
		}
		return true
	}
	refused := func(a answered, code string) bool { return a.Error.Code == code }
	substitute := func(who, by string) answered {
		t.Helper()
		_, a := send(who, "create", `{"class": "Substitution", "id": "`+who+`-`+by+`", "values": {"by": "`+by+`", "classes": ["Expense"]}}`)
		return a
	}
	steps := []struct {
		who, does, answer string
		holds             func() (bool, any)
	}{
		{"Jan", `files "Hotel in Gdańsk", 410.00 PLN`, "a draft", func() (bool, any) {
			a := file("jan", "EXP-1", "Hotel in Gdańsk", "410.00 PLN")
			return is(a, "draft", "employee", "jan"), a
		}},
		{"Ewa", "reads it", "`unknown_document`: a draft is Jan's alone", func() (bool, any) {
			_, a := send("ewa", "get", `{`+expense("EXP-1")+`}`)
			return refused(a, "unknown_document"), a
		}},
		{"Jan", "writes Marek as its manager", "`read_only`: the manager is looked up", func() (bool, any) {
			_, a := send("jan", "set", `{`+expense("EXP-1")+`, "values": {"manager": "marek"}}`)
			return refused(a, "read_only"), a
		}},
		{"Jan", "submits it", "waiting for the manager, `ewa`", func() (bool, any) {
			a := move("jan", "EXP-1", "submit")
			return is(a, "submitted", "manager", "ewa"), a
		}},
		{"Marek", "approves it", "`unknown_document`: he is not Jan's manager", func() (bool, any) {
			a := move("marek", "EXP-1", "approve")
			return refused(a, "unknown_document"), a
		}},
		{"Jan", "approves it himself", "`forbidden`", func() (bool, any) {
			a := move("jan", "EXP-1", "approve")
			return refused(a, "forbidden"), a
		}},
		{"Ewa", "approves it", "approved, decided by `ewa`", func() (bool, any) {
			a := move("ewa", "EXP-1", "approve")
			return is(a, "approved", "decided_by", "ewa"), a
		}},
		{"Jan", "changes its amount to 4100.00 PLN", "`forbidden`: it is no longer a draft", func() (bool, any) {
			_, a := send("jan", "set", `{`+expense("EXP-1")+`, "values": {"amount": "4100.00 PLN"}}`)
			return refused(a, "forbidden"), a
		}},
		{"Fiona", "pays it back", "paid back", func() (bool, any) {
			a := move("fiona", "EXP-1", "pay")
			return is(a, "paid", "amount", "410.00 PLN"), a
		}},
		{"Jan", `files "Taxi to the airport", 84.00 PLN`, "a draft", func() (bool, any) {
			a := file("jan", "EXP-2", "Taxi to the airport", "84.00 PLN")
			return is(a, "draft"), a
		}},
		{"Hanna", "moves Jan to Marek's team", "the row of `jan` names `marek`", func() (bool, any) {
			if status, a := send("hanna", "import_rows", `{"table": "Employees", "rows": [{"person": "jan", "manager": "marek"}]}`); status != 200 {
				return false, a
			}
			_, a := send("hanna", "table_rows", `{"table": "Employees"}`)
			for _, row := range a.Rows {
				if row["person"] == "jan" {
					return row["manager"] == "marek", a.Rows
				}
			}
			return false, a.Rows
		}},
		{"Jan", "submits the taxi", "waiting for the manager, `marek`", func() (bool, any) {
			a := move("jan", "EXP-2", "submit")
			return is(a, "submitted", "manager", "marek"), a
		}},
		{"Ewa", "approves the taxi", "`unknown_document`: she is not Jan's manager any more", func() (bool, any) {
			a := move("ewa", "EXP-2", "approve")
			return refused(a, "unknown_document"), a
		}},
		{"Marek", "approves the taxi", "approved, decided by `marek`", func() (bool, any) {
			a := move("marek", "EXP-2", "approve")
			return is(a, "approved", "decided_by", "marek"), a
		}},
		{"Marek", `files "Train to Kraków", 150.00 PLN, and submits it`, "waiting for the manager, `ewa`", func() (bool, any) {
			if a := file("marek", "EXP-3", "Train to Kraków", "150.00 PLN"); !is(a, "draft") {
				return false, a
			}
			a := move("marek", "EXP-3", "submit")
			return is(a, "submitted", "manager", "ewa"), a
		}},
		{"Jan, for Ewa", "approves the train", "`forbidden`: Ewa has let nobody act for her", func() (bool, any) {
			a := move("jan", "EXP-3", "approve", "ewa")
			return refused(a, "forbidden") && a.Error.Path == "/principal/for", a
		}},
		{"Ewa", "lets Jan act for her on expenses", "a substitution, `active`", func() (bool, any) {
			a := substitute("ewa", "jan")
			return is(a, "active"), a
		}},
		{"Jan, for Ewa", "approves the train", "approved, decided by `jan`", func() (bool, any) {
			a := move("jan", "EXP-3", "approve", "ewa")
			return is(a, "approved", "decided_by", "jan"), a
		}},
		{"Jan", "lets his assistant act for him on expenses", "a substitution, `active`", func() (bool, any) {
			a := substitute("jan", "jan-assistant")
			return is(a, "active"), a
		}},
		{"Jan's assistant, for Jan", `files "Parking at the airport", 36.00 PLN, and submits it`, "waiting for the manager, `marek`; the employee is `jan`", func() (bool, any) {
			_, a := send("jan-assistant", "create", `{`+expense("EXP-4")+`, "for": "jan", "values": {"what": "Parking at the airport", "amount": "36.00 PLN"}}`)
			if !is(a, "draft", "employee", "jan") {
				return false, a
			}
			a = move("jan-assistant", "EXP-4", "submit", "jan")
			return is(a, "submitted", "manager", "marek", "employee", "jan"), a
		}},
		{"Fiona", `files "Paper for the printer", 12.00 PLN, and submits it`, "`guard_false`: Employees names no manager of hers", func() (bool, any) {
			if a := file("fiona", "EXP-5", "Paper for the printer", "12.00 PLN"); !is(a, "draft") {
				return false, a
			}
			a := move("fiona", "EXP-5", "submit")
			return refused(a, "guard_false"), a
		}},
	}
	rows = rowsOf(t, chapter, "the expenses")
	if len(rows) != len(steps) {
		t.Fatalf("the chapter has %d rows of expenses, the test %d", len(rows), len(steps))
	}
	for i, s := range steps {
		if row := rows[i]; row[0] != s.who || row[1] != s.does || row[2] != s.answer {
			t.Fatalf("row %d of the expenses is %q, the test plays %q", i+1, row, []string{s.who, s.does, s.answer})
		}
		if holds, got := s.holds(); !holds {
			t.Fatalf("row %d, %s %s: the answer is not %q: %+v\n%s", i+1, s.who, s.does, s.answer, got, log)
		}
	}
}

// A starter the program does not have is refused before anything is
// made, and the starters are named.
func TestAStarterThatIsNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	code, _, said := tool("init", path, "--starter", "payroll")
	if code != 2 || !strings.Contains(said, `there is no starter "payroll": the starters are expenses`) {
		t.Errorf("init with a starter that is none: exit %d\n%s", code, said)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 0 {
		t.Errorf("init made %d files", len(entries))
	}
}

// A starter whose command the database refuses leaves no database: init
// takes back every file it made, and says which command was refused.
func TestAStarterThatFailsLeavesNothing(t *testing.T) {
	defer func(was func(string) (*starter.Starter, error)) { findStarter = was }(findStarter)
	findStarter = func(name string) (*starter.Starter, error) {
		good, err := starter.Get("expenses")
		if err != nil {
			return nil, err
		}
		broken := *good
		broken.Commands = append(append([]starter.Command{}, good.Commands...),
			starter.Command{Command: "define_class", Object: json.RawMessage(`{"definition": {"name": "Broken", "variables": {"x": {"formula": "NOSUCH()"}}}}`)})
		return &broken, nil
	}
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("Olga's"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, said := tool("init", filepath.Join(dir, "acme.fado"), "--starter", "expenses")
	if code != 1 || !strings.Contains(said, "the starter expenses was not made, and neither was the database: command 6 of 6, define_class, which makes the class Broken") {
		t.Errorf("init with a starter that fails: exit %d\n%s", code, said)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "notes.txt" {
		var left []string
		for _, e := range entries {
			left = append(left, e.Name())
		}
		t.Errorf("init left %v", left)
	}
	if _, err := os.Stat(keep); errors.Is(err, os.ErrNotExist) {
		t.Error("init took away a file it did not make")
	}
}
