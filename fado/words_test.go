package fado_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// wordsIn returns the words a chapter of the manual shows for a class,
// with the line endings of a checkout on Windows made plain.
func wordsIn(t *testing.T, chapter, class string) []byte {
	t.Helper()
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", chapter))
	if err != nil {
		t.Fatal(err)
	}
	page = bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		if kindOf(m[1]) == "words" && strings.Contains(string(m[1]), `"class": "`+class+`"`) {
			return m[1]
		}
	}
	t.Fatalf("%s shows no words of %s", chapter, class)
	return nil
}

// What the chapter on the class says of words in other languages
// (ADR-0068, item 6): Ola's Polish words of the invoice are defined
// beside the class and make no new version of it; the language is
// written as BCP 47 writes it; a later version of the class that drops
// a variable leaves its words noted; and the words, their versions and
// who defined them are kept when the database is opened again.
func TestWords_BesideTheClass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "words.fado")
	db, err := fado.Open(path, fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	class := classIn(t, "class.md", "Invoice")
	polish := wordsIn(t, "class.md", "Invoice")
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	inv, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Acme", "number": "FV/9"}, person("jan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineWords(ctx, polish, person("ola")); codeOf(err) != "forbidden" {
		t.Errorf("Ola, who is no designer, defines words: %v", err)
	}
	info, err := db.DefineWords(ctx, polish, setup)
	if err != nil || info.Name != "class/Invoice/pl" || info.Version != 1 || info.Defined == nil || info.Defined.By != "setup" || len(info.Notes) != 0 {
		t.Fatalf("the Polish words: %v %+v", err, info)
	}
	if cls, _ := db.Class("Invoice"); cls.Version != 1 {
		t.Errorf("the words made version %d of the class", cls.Version)
	}
	if after, err := db.Get(ctx, "Invoice", inv.ID, person("jan")); err != nil || after.Version != inv.Version {
		t.Errorf("the document after the words: %v", err)
	}
	read, err := db.Definition(ctx, fado.DefinitionWords, "class/Invoice/pl", 0)
	if err != nil || !strings.Contains(string(read.Definition), `"label":"Faktura"`) {
		t.Errorf("the words read back: %v %s", err, read.Definition)
	}

	// The language as the standard writes it.
	_, err = db.DefineWords(ctx, []byte(strings.Replace(string(polish), `"language": "pl"`, `"language": "pt-br"`, 1)), setup)
	var found fado.Diagnostics
	if !errors.As(err, &found) || len(found) != 1 || found[0].Path != "/language" || !strings.Contains(found[0].Hint, `"pt-BR"`) {
		t.Errorf("the tag pt-br: %v", err)
	}

	// A version of the class without the notes leaves their words unused.
	smaller := strings.Replace(string(class), `,
    "notes":        {"type": "text",  "label": "Notes"}`, "", 1)
	if smaller == string(class) {
		t.Fatal("the chapter's class has no notes to drop")
	}
	if _, err := db.DefineClass(ctx, []byte(smaller), setup); err != nil {
		t.Fatal(err)
	}
	listed := db.ListWords(fado.WordsOf{Class: "Invoice"})
	if len(listed) != 1 || len(listed[0].Notes) != 1 || listed[0].Notes[0].Code != "unused_words" || listed[0].Notes[0].Path != "/variables/notes" {
		t.Errorf("the words of a class that dropped a variable: %+v", listed)
	}
	again := strings.Replace(string(polish), `,
    "notes":        {"label": "Uwagi"}`, "", 1)
	if _, err := db.DefineWords(ctx, []byte(again), setup); err != nil {
		t.Fatalf("the words again without the notes: %v", err)
	}
	db.Close()

	// Opened again, the words are there, with their versions and who
	// defined them; taken away, they leave the list and keep their
	// versions.
	db, err = fado.Open(path, fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	listed = db.ListWords(fado.WordsOf{Language: "pl"})
	if len(listed) != 1 || listed[0].Version != 2 || listed[0].Defined == nil || listed[0].Defined.By != "setup" || len(listed[0].Notes) != 0 {
		t.Errorf("the words when the database is opened again: %+v", listed)
	}
	if err := db.RemoveWords(ctx, fado.WordsOf{Class: "Invoice", Language: "pl"}, setup); err != nil {
		t.Fatal(err)
	}
	if listed := db.ListWords(fado.WordsOf{}); len(listed) != 0 {
		t.Errorf("the words after they were taken away: %+v", listed)
	}
	if versions, err := db.Definitions(ctx, fado.DefinitionWords, "class/Invoice/pl"); err != nil || len(versions) != 2 {
		t.Errorf("the versions of the words taken away: %v %+v", err, versions)
	}
}

// A table has words for its label, its description and its columns, and
// a schema for its fields, the engine's own among them.
func TestWords_OfATableAndASchema(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "words.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineTable(ctx, []byte(`{"name": "Rates", "label": "Rates", "columns": {"code": "text", "rate": "decimal(6)"}, "key": ["code"],
	  "access": {"read": "TRUE", "write": "FALSE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineWords(ctx, []byte(`{"table": "Rates", "language": "pl", "label": "Kursy", "columns": {"code": {"label": "Waluta"}, "rate": {"label": "Kurs"}}}`), setup); err != nil {
		t.Errorf("the words of a table: %v", err)
	}
	if _, err := db.DefineWords(ctx, []byte(`{"schema": "Address", "language": "pl", "fields": {"city": {"label": "Miasto"}, "postal_code": {"label": "Kod pocztowy"}}}`), setup); err != nil {
		t.Errorf("the words of the engine's schema Address: %v", err)
	}
	if _, err := db.DefineWords(ctx, []byte(`{"class": "Invoice", "language": "pl", "label": "Faktura"}`), setup); codeOf(err) != "unknown_class" {
		t.Errorf("the words of a class the database does not have: %v", err)
	}
	var names []string
	for _, w := range db.ListWords(fado.WordsOf{}) {
		names = append(names, w.Name)
	}
	if got := strings.Join(names, " "); got != "table/Rates/pl schema/Address/pl" {
		t.Errorf("the words listed: %s", got)
	}
}
