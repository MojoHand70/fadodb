package fado_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// TestManualExamples keeps the manual honest. Every block of JSON in a
// chapter under docs/manual is either a whole class, which the engine
// must define as it is written, or a piece quoted from a whole class of
// the same chapter, which must be found there word for word. So a
// chapter cannot show a class the engine refuses, nor a line the class
// beside it does not have.
func TestManualExamples(t *testing.T) {
	pages := manualPages(t)
	block := regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```")
	space := regexp.MustCompile(`\s+`)
	squeeze := func(text string) string { return space.ReplaceAllString(text, "") }
	ctx := context.Background()
	for _, page := range pages {
		data, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		var classes, tables, actions, pieces []string
		for _, m := range block.FindAllSubmatch(data, -1) {
			switch kindOf(m[1]) {
			case "class":
				classes = append(classes, string(m[1]))
			case "table":
				tables = append(tables, string(m[1]))
			case "action":
				actions = append(actions, string(m[1]))
			case "document", "answer":
				// A document as the engine answers it, or the answer of a
				// command. The test of its chapter compares it with the
				// engine's answer.
			default:
				pieces = append(pieces, string(m[1]))
			}
		}
		name := filepath.Base(page)
		// An action a page defines with no request is the contract of a
		// function of the host program, so the test's database is opened
		// with one that does nothing under each such name.
		funcs := map[string]fado.ActionFunc{}
		for _, action := range actions {
			var def struct {
				Name    string          `json:"name"`
				Request json.RawMessage `json:"request"`
			}
			if json.Unmarshal([]byte(action), &def) == nil && def.Request == nil {
				funcs[def.Name] = func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil }
			}
		}
		for i, class := range classes {
			db, err := fado.Open(filepath.Join(t.TempDir(), "manual.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Actions: funcs})
			if err != nil {
				t.Fatal(err)
			}
			// What the classes of the page name is defined before them.
			for _, table := range tables {
				if _, err := db.DefineTable(ctx, []byte(table), setup); err != nil {
					t.Errorf("%s: a table is refused: %v", name, err)
				}
			}
			for _, action := range actions {
				if _, err := db.DefineAction(ctx, []byte(action), setup); err != nil {
					t.Errorf("%s: an action is refused: %v", name, err)
				}
			}
			cls, err := db.DefineClass(ctx, []byte(class), setup)
			if err != nil {
				t.Errorf("%s: class %d is refused: %v", name, i+1, err)
			} else if len(cls.Notes) > 0 {
				t.Errorf("%s: the class %s leaves a rule out: %v", name, cls.Name, cls.Notes)
			}
			db.Close()
		}
		for _, piece := range pieces {
			found := false
			for _, class := range classes {
				if strings.Contains(squeeze(class), squeeze(piece)) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: this piece is in no class of the chapter:\n%s", name, piece)
			}
		}
		if len(classes) == 0 && len(tables)+len(actions) > 0 {
			t.Errorf("%s shows a table or an action and no class that uses it", name)
		}
		t.Logf("%s: %d classes, %d tables and %d actions defined, %d pieces found in them", name, len(classes), len(tables), len(actions), len(pieces))
	}
}

// TestManualLinks follows every link of the manual that points into the
// repository: the file it names must exist.
func TestManualLinks(t *testing.T) {
	pages := append(manualPages(t), filepath.Join("..", "docs", "README.md"))
	link := regexp.MustCompile(`\]\(([^)#\s]+)(#[^)\s]*)?\)`)
	for _, page := range pages {
		data, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range link.FindAllSubmatch(data, -1) {
			target := string(m[1])
			if strings.Contains(target, "://") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(page), filepath.FromSlash(target))); err != nil {
				t.Errorf("%s links to %s, which is not there", filepath.Base(page), target)
			}
		}
	}
}

// manualPages lists the chapters, the guides and the notions of the
// manual, and the skills, which teach an agent from the same examples.
func manualPages(t *testing.T) []string {
	t.Helper()
	var pages []string
	for _, pattern := range []string{"*.md", filepath.Join("guides", "*.md"), filepath.Join("notions", "*.md")} {
		found, err := filepath.Glob(filepath.Join("..", "docs", "manual", pattern))
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, found...)
	}
	if len(pages) == 0 {
		t.Fatal("no chapters found")
	}
	// The skills show classes to agents, and are held to the same.
	skills, err := filepath.Glob(filepath.Join("..", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return append(pages, skills...)
}

// kindOf says what a block of JSON in the manual is: a whole class, a
// whole table, a whole action definition, a document as the engine
// answers it, the answer of a command, or a piece of one of them.
func kindOf(block []byte) string {
	var whole struct {
		Name      string          `json:"name"`
		Variables json.RawMessage `json:"variables"`
		Columns   json.RawMessage `json:"columns"`
		Params    json.RawMessage `json:"params"`
		Request   json.RawMessage `json:"request"`
		Class     string          `json:"class"`
		Values    json.RawMessage `json:"values"`
		Document  json.RawMessage `json:"document"`
		Error     json.RawMessage `json:"error"`
	}
	if json.Unmarshal(block, &whole) != nil {
		return "piece"
	}
	if whole.Document != nil || whole.Error != nil {
		return "answer"
	}
	if whole.Class != "" && whole.Values != nil {
		return "document"
	}
	if whole.Name == "" {
		return "piece"
	}
	switch {
	case whole.Variables != nil:
		return "class"
	case whole.Columns != nil:
		return "table"
	case whole.Params != nil || whole.Request != nil:
		return "action"
	}
	return "piece"
}
