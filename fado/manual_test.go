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
		var classes, pieces []string
		for _, m := range block.FindAllSubmatch(data, -1) {
			var whole struct {
				Name      string          `json:"name"`
				Variables json.RawMessage `json:"variables"`
			}
			if json.Unmarshal(m[1], &whole) == nil && whole.Name != "" && whole.Variables != nil {
				classes = append(classes, string(m[1]))
			} else {
				pieces = append(pieces, string(m[1]))
			}
		}
		name := filepath.Base(page)
		for i, class := range classes {
			db, err := fado.Open(filepath.Join(t.TempDir(), "manual.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
			if err != nil {
				t.Fatal(err)
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
		t.Logf("%s: %d classes defined, %d pieces found in them", name, len(classes), len(pieces))
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

// manualPages lists the chapters and the guides of the manual.
func manualPages(t *testing.T) []string {
	t.Helper()
	var pages []string
	for _, pattern := range []string{"*.md", filepath.Join("guides", "*.md")} {
		found, err := filepath.Glob(filepath.Join("..", "docs", "manual", pattern))
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, found...)
	}
	if len(pages) == 0 {
		t.Fatal("no chapters found")
	}
	return pages
}
