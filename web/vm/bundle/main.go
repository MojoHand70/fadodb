// Command bundle writes the proof of the bundle (ADR-0070, item 12): two
// classes played against the engine, each with its bundle, the people
// who use it, and at each step the document as each person reads it and
// what the server answers: the values a set works out, the defaults of
// a new document, and what each person may set, read, move and delete.
// The browser VM's bundle check, web/vm/test/bundle.mjs, works every
// answer out again from the bundle alone and compares. Nothing it writes
// is kept in the repository: make vmtest makes it each time.
//
// The first class is the invoice of the chapter on the class, read in
// Ola's Polish words; the second the sample invoice under
// examples/classes, which has a title, defaults, guards and rights in
// every state.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MojoHand70/fadodb/fado"
)

// person is a principal as whoami answers it and a page gives it to the
// browser VM.
type person struct {
	User   string   `json:"user"`
	Groups []string `json:"groups"`
	System []string `json:"system"`
	Kind   string   `json:"kind"`
}

// compute is a set the page works out: the document before, the values
// set, and what the server's preview answers.
type compute struct {
	Person string          `json:"person"`
	Doc    json.RawMessage `json:"doc"`
	Set    map[string]any  `json:"set"`
	Expect json.RawMessage `json:"expect"`
}

// defaults is a new document: the values given, the moment, and the
// document the server's preview of the create answers.
type defaults struct {
	Person string          `json:"person"`
	Given  map[string]any  `json:"given"`
	Now    int64           `json:"now"`
	Expect json.RawMessage `json:"expect"`
}

// may is what a person may do with a document in a state, as the server
// answers it.
type may struct {
	Person      string          `json:"person"`
	Doc         json.RawMessage `json:"doc"`
	Set         map[string]bool `json:"set"`
	Read        map[string]bool `json:"read"`
	Transitions map[string]bool `json:"transitions"`
	Delete      bool            `json:"delete"`
}

type story struct {
	Name     string            `json:"name"`
	Bundle   json.RawMessage   `json:"bundle"`
	People   map[string]person `json:"people"`
	Compute  []compute         `json:"compute"`
	Defaults []defaults        `json:"defaults"`
	May      []may             `json:"may"`
}

var setup = fado.Principal{ID: "setup", Kind: fado.ActorService, System: []string{fado.GroupDesigners, fado.GroupAdministrators}}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bundle:", err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "fadodb-bundle")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var stories []story
	for i, play := range []func(*fado.DB) (story, error){olasInvoice, sampleInvoice} {
		db, err := fado.Open(filepath.Join(dir, fmt.Sprintf("story%d.fado", i)), fado.Options{})
		if err != nil {
			return err
		}
		s, err := play(db)
		db.Close()
		if err != nil {
			return err
		}
		stories = append(stories, s)
	}
	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)
	return out.Encode(map[string]any{"stories": stories})
}

// exec sends a command as a page does, and answers its JSON.
func exec(db *fado.DB, p fado.Principal, name string, object any) (map[string]any, error) {
	text, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	answer, err := db.Exec(context.Background(), p, name, text)
	var out map[string]any
	if jerr := json.Unmarshal(answer, &out); jerr != nil {
		return nil, fmt.Errorf("%s answered %s: %v", name, answer, jerr)
	}
	return out, err
}

func principal(p person) fado.Principal {
	return fado.Principal{ID: p.User, Groups: p.Groups, System: p.System, Kind: p.Kind}
}

// player keeps what a story writes down.
type player struct {
	db    *fado.DB
	class string
	s     story
}

func (pl *player) bundle(language string) error {
	b, err := pl.db.GetBundle(context.Background(), fado.BundleOf{Class: pl.class, Language: language})
	if err != nil {
		return err
	}
	pl.s.Bundle = b.JSON
	return nil
}

// doc is the document as a person reads it, with get.
func (pl *player) doc(who, id string) (json.RawMessage, map[string]any, error) {
	got, err := exec(pl.db, principal(pl.s.People[who]), "get", map[string]any{"class": pl.class, "id": id})
	if err != nil {
		return nil, nil, fmt.Errorf("%s reads %s: %v", who, id, err)
	}
	text, _ := json.Marshal(got["document"])
	values, _ := got["document"].(map[string]any)["values"].(map[string]any)
	return text, values, nil
}

// compute writes down what the server works out when a person sets
// values.
func (pl *player) compute(who, id string, set map[string]any) error {
	doc, _, err := pl.doc(who, id)
	if err != nil {
		return err
	}
	got, err := exec(pl.db, principal(pl.s.People[who]), "preview", map[string]any{"class": pl.class, "id": id, "values": set})
	if err != nil {
		return fmt.Errorf("%s previews %v: %v", who, set, err)
	}
	preview := got["preview"].(map[string]any)
	if preview["errors"] != nil {
		return fmt.Errorf("%s previews %v: %v", who, set, preview["errors"])
	}
	expect, _ := json.Marshal(preview["document"])
	pl.s.Compute = append(pl.s.Compute, compute{Person: who, Doc: doc, Set: set, Expect: expect})
	return nil
}

// defaults writes down the document the server makes of the values a
// person gives a new one, at a moment.
func (pl *player) defaults(who string, given map[string]any, now int64) error {
	got, err := exec(pl.db, principal(pl.s.People[who]), "preview", map[string]any{"class": pl.class, "values": given, "at": now})
	if err != nil {
		return fmt.Errorf("%s previews a new document: %v", who, err)
	}
	expect, _ := json.Marshal(got["preview"].(map[string]any)["document"])
	pl.s.Defaults = append(pl.s.Defaults, defaults{Person: who, Given: given, Now: now, Expect: expect})
	return nil
}

// may writes down, for every person, what the server lets them do with
// the document in its state.
func (pl *player) may(id string, variables []string) error {
	for who, p := range pl.s.People {
		as := principal(p)
		doc, values, err := pl.doc(who, id)
		if err != nil {
			if strings.Contains(err.Error(), "unknown_document") {
				continue
			}
			return err
		}
		m := may{Person: who, Doc: doc, Set: map[string]bool{}, Read: map[string]bool{}, Transitions: map[string]bool{}}
		for _, v := range variables {
			value, read := values[v]
			m.Read[v] = read
			got, err := exec(pl.db, as, "preview", map[string]any{"class": pl.class, "id": id, "values": map[string]any{v: value}})
			m.Set[v] = err == nil && got["preview"].(map[string]any)["errors"] == nil
		}
		got, err := exec(pl.db, as, "transitions", map[string]any{"class": pl.class, "id": id})
		if err != nil {
			return err
		}
		for _, t := range got["transitions"].([]any) {
			t := t.(map[string]any)
			m.Transitions[t["name"].(string)] = t["enabled"] == true
		}
		got, err = exec(pl.db, as, "preview_delete", map[string]any{"class": pl.class, "id": id})
		m.Delete = err == nil && got["preview"].(map[string]any)["errors"] == nil
		pl.s.May = append(pl.s.May, m)
	}
	return nil
}

func (pl *player) create(who string, values map[string]any) (string, error) {
	got, err := exec(pl.db, principal(pl.s.People[who]), "create", map[string]any{"class": pl.class, "values": values})
	if err != nil {
		return "", fmt.Errorf("%s creates: %v %v", who, err, got)
	}
	return got["document"].(map[string]any)["id"].(string), nil
}

func (pl *player) move(who, id, transition string) error {
	got, err := exec(pl.db, principal(pl.s.People[who]), "transition", map[string]any{"class": pl.class, "id": id, "transition": transition})
	if err != nil {
		return fmt.Errorf("%s takes %s: %v %v", who, transition, err, got)
	}
	return nil
}

// fromManual reads the first JSON block of a chapter that holds every
// one of the marks.
func fromManual(chapter string, marks ...string) ([]byte, error) {
	page, err := os.ReadFile(filepath.Join("docs", "manual", chapter))
	if err != nil {
		return nil, err
	}
	page = []byte(strings.ReplaceAll(string(page), "\r\n", "\n"))
	for _, m := range regexp.MustCompile("(?s)```json\n(.*?)\n```").FindAllSubmatch(page, -1) {
		all := true
		for _, mark := range marks {
			all = all && strings.Contains(string(m[1]), mark)
		}
		if all {
			return m[1], nil
		}
	}
	return nil, fmt.Errorf("%s shows no block with %v", chapter, marks)
}

// olasInvoice plays the invoice of the chapter on the class, in Ola's
// Polish: Jan files it, Ewa approves it, Marta and Ola are in finance.
func olasInvoice(db *fado.DB) (story, error) {
	pl := &player{db: db, class: "Invoice", s: story{Name: "the chapter's invoice, in Ola's words", People: map[string]person{
		"jan":   {User: "jan", Groups: []string{}, System: []string{}, Kind: "person"},
		"ewa":   {User: "ewa", Groups: []string{"approvers"}, System: []string{}, Kind: "person"},
		"marta": {User: "marta", Groups: []string{"finance"}, System: []string{}, Kind: "person"},
		"ola":   {User: "ola", Groups: []string{"finance"}, System: []string{}, Kind: "person"},
	}}}
	class, err := fromManual("class.md", `"name": "Invoice"`, `"sections"`)
	if err != nil {
		return story{}, err
	}
	words, err := fromManual("class.md", `"class": "Invoice"`, `"language": "pl"`)
	if err != nil {
		return story{}, err
	}
	ctx := context.Background()
	if _, err := db.DefineClass(ctx, class, fado.As(setup)); err != nil {
		return story{}, err
	}
	if _, err := db.DefineWords(ctx, words, fado.As(setup)); err != nil {
		return story{}, err
	}
	if err := pl.bundle("pl"); err != nil {
		return story{}, err
	}
	inputs := []string{"customer", "number", "net", "vat", "has_delivery", "street", "city", "notes"}
	id, err := pl.create("jan", map[string]any{"customer": "Acme", "number": "FV/9", "net": "100.00 PLN", "vat": "23.00 PLN"})
	if err != nil {
		return story{}, err
	}
	for _, set := range []map[string]any{
		{"net": "120.00 PLN"}, {"vat": "27.60 PLN"}, {"net": nil}, {"net": "0.01 PLN", "vat": "0.00 PLN"}, {"street": "Długa 1", "has_delivery": true},
	} {
		if err := pl.compute("ola", id, set); err != nil {
			return story{}, err
		}
	}
	if err := pl.may(id, inputs); err != nil {
		return story{}, err
	}
	if err := pl.move("jan", id, "file"); err != nil {
		return story{}, err
	}
	if err := pl.compute("marta", id, map[string]any{"net": "200.00 PLN"}); err != nil {
		return story{}, err
	}
	if err := pl.may(id, inputs); err != nil {
		return story{}, err
	}
	if err := pl.move("ewa", id, "approve"); err != nil {
		return story{}, err
	}
	return pl.s, pl.may(id, inputs)
}

// sampleInvoice plays the sample invoice: Sam in sales writes it for
// Cara, the customer, and Fin in finance approves it and records the
// payment.
func sampleInvoice(db *fado.DB) (story, error) {
	pl := &player{db: db, class: "Invoice", s: story{Name: "the sample invoice", People: map[string]person{
		"sam":  {User: "sam", Groups: []string{"sales"}, System: []string{}, Kind: "person"},
		"fin":  {User: "fin", Groups: []string{"finance"}, System: []string{}, Kind: "person"},
		"cara": {User: "cara", Groups: []string{}, System: []string{}, Kind: "person"},
	}}}
	ctx := context.Background()
	for _, name := range []string{"receipt.json", "invoice.json"} {
		data, err := os.ReadFile(filepath.Join("examples", "classes", name))
		if err != nil {
			return story{}, err
		}
		if _, err := db.DefineClass(ctx, data, fado.As(setup)); err != nil {
			return story{}, err
		}
	}
	if err := pl.bundle(""); err != nil {
		return story{}, err
	}
	// 2026-11-18 at noon, and the last day of a month.
	for _, now := range []int64{1795003200, 1798675200} {
		if err := pl.defaults("sam", map[string]any{"customer": "cara", "net": "100.00 PLN"}, now); err != nil {
			return story{}, err
		}
	}
	inputs := []string{"customer", "due", "discount", "net"}
	id, err := pl.create("sam", map[string]any{"customer": "cara", "net": "100.00 PLN", "discount": "0.10"})
	if err != nil {
		return story{}, err
	}
	for _, set := range []map[string]any{{"discount": "0.25"}, {"net": "80.00 PLN"}, {"customer": "dan"}, {"discount": "0.5", "net": "0.03 PLN"}} {
		if err := pl.compute("sam", id, set); err != nil {
			return story{}, err
		}
	}
	for _, step := range []struct{ who, transition string }{{"", ""}, {"sam", "submit"}, {"fin", "approve"}, {"fin", "pay"}} {
		if step.who != "" {
			if err := pl.move(step.who, id, step.transition); err != nil {
				return story{}, err
			}
		}
		if err := pl.may(id, inputs); err != nil {
			return story{}, err
		}
	}
	// A draft whose total is nothing cannot be submitted.
	empty, err := pl.create("sam", map[string]any{"customer": "cara", "net": "0.00 PLN"})
	if err != nil {
		return story{}, err
	}
	return pl.s, pl.may(empty, inputs)
}
