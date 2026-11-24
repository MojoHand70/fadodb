package starter_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/starter"
)

// Every starter the program carries is read, and made on a database
// that has just been made, command after command, with no note against
// what it defines: a starter is what a person or an agent copies.
func TestEveryStarterIsMade(t *testing.T) {
	names := starter.Names()
	if len(names) == 0 {
		t.Fatal("the program carries no starter")
	}
	machine := fado.Principal{System: []string{fado.GroupAdministrators, fado.GroupDesigners}}
	for _, name := range names {
		s, err := starter.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if s.Label == "" || s.Description == "" || s.Next == "" || len(s.Commands) == 0 {
			t.Errorf("the starter %s does not say what it is, what to do next, or what it makes: %+v", name, s)
		}
		db, err := fado.Open(filepath.Join(t.TempDir(), name+".fado"), fado.Options{Users: true})
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range s.Commands {
			answer, err := db.Exec(context.Background(), machine, c.Command, c.Object)
			if err != nil {
				t.Fatalf("%s, command %d, %s: %v\n%s", name, i+1, c.Made(), err, answer)
			}
			var noted struct {
				Class struct {
					Notes []json.RawMessage `json:"notes"`
				} `json:"class"`
			}
			if json.Unmarshal(answer, &noted); len(noted.Class.Notes) > 0 {
				t.Errorf("%s: %s is defined with notes:\n%s", name, c.Made(), answer)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// A name the program carries no starter under is refused, and the
// refusal names the starters there are; a name is never a path.
func TestAStarterThatIsNone(t *testing.T) {
	for _, name := range []string{"payroll", "", "../expenses", "expenses.json", `..\expenses`} {
		if _, err := starter.Get(name); err == nil || !strings.Contains(err.Error(), "the starters are expenses") {
			t.Errorf("the starter %q: %v", name, err)
		}
	}
}

// What a command of a starter makes, as init says it.
func TestWhatACommandMakes(t *testing.T) {
	for _, c := range []struct{ command, object, makes string }{
		{"define_app", `{"definition": {"name": "Expenses"}}`, "the app Expenses"},
		{"create_group", `{"name": "hr"}`, "the group hr"},
		{"define_class", `{"definition": {"name": "Expense"}}`, "the class Expense"},
	} {
		if got := (starter.Command{Command: c.command, Object: json.RawMessage(c.object)}).Made(); got != c.makes {
			t.Errorf("%s makes %q", c.command, got)
		}
	}
}
