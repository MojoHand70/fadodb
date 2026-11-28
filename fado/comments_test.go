package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on comments is a reference (ADR-0098): who may comment, what
// a comment keeps, and what is refused. TestCommentsChapter takes the
// class out of the chapter, plays its story step by step, and holds every
// table of the chapter to what the engine answers.
func TestCommentsChapter(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "comments.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ola, anna, piotr := person("ola"), person("anna", "approvers"), person("piotr")
	reader := fado.As(fado.Principal{ID: "reader", Kind: fado.ActorAgent, For: "ola"})
	db := notionDB(t, fado.Options{})
	if _, err := db.DefineClass(ctx, notionClass(t, page), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, fado.SubstitutionClass, fado.Values{"by": "reader"}, ola); err != nil {
		t.Fatal(err)
	}
	on := func(c *fado.Comment, err error) string {
		if err != nil {
			return told(err)
		}
		return fmt.Sprintf("allowed, on version %d", c.Version)
	}
	versioned := func(doc *fado.Document, err error) string {
		if err != nil {
			return told(err)
		}
		return fmt.Sprintf("allowed, version %d", doc.Version)
	}

	story := taleOf(t, page)
	step := story.step
	trip, err := db.Create(ctx, "Trip", fado.Values{"what": "Taxi to the airport", "cost": "120.00 PLN"}, ola)
	step(1, versioned(trip, err))
	if err != nil {
		t.FailNow()
	}
	step(2, on(db.Comment(ctx, "Trip", trip.ID, "Why a taxi? The train is cheaper.", anna)))
	step(3, on(db.Comment(ctx, "Trip", trip.ID, "The last train had left.", ola)))
	step(4, on(db.Comment(ctx, "Trip", trip.ID, "Nice trip.", piotr)))
	if none, err := db.Comments(ctx, "Trip", trip.ID, piotr); err == nil && len(none) == 0 {
		step(5, "none")
	} else {
		step(5, fmt.Sprint(len(none), err))
	}
	if read, err := db.Get(ctx, "Trip", trip.ID, ola); err == nil {
		step(6, fmt.Sprintf("version %d", read.Version))
	} else {
		step(6, told(err))
	}
	byReader, err := db.Comment(ctx, "Trip", trip.ID, "The receipt says 23:40.", reader)
	step(7, on(byReader, err))
	all, err := db.Comments(ctx, "Trip", trip.ID, ola)
	var who []string
	for _, c := range all {
		w := c.ActorID
		if c.ActorFor != "" {
			w += " for " + c.ActorFor
		}
		who = append(who, w)
	}
	step(8, strings.Join(who, ", ")+fmt.Sprint(map[bool]string{true: "", false: " " + fmt.Sprint(err)}[err == nil]))
	keyed, err := db.Comment(ctx, "Trip", trip.ID, "Booked by phone.", ola, fado.Key("ola-9"))
	step(9, on(keyed, err))
	if again, err := db.Comment(ctx, "Trip", trip.ID, "Booked by phone.", ola, fado.Key("ola-9")); err == nil && again.ID == keyed.ID && again.TS == keyed.TS {
		step(10, "the same comment")
	} else {
		step(10, fmt.Sprint(again, err))
	}
	step(11, on(db.Comment(ctx, "Trip", trip.ID, "Booked online.", ola, fado.Key("ola-9"))))
	step(12, versioned(db.Transition(ctx, "Trip", trip.ID, "file", nil, ola)))
	step(13, on(db.Comment(ctx, "Trip", trip.ID, "As I read it.", ola, fado.Saw(1))))
	step(14, versioned(db.Transition(ctx, "Trip", trip.ID, "pay", nil, ola)))
	step(15, on(db.Comment(ctx, "Trip", trip.ID, "Paid back on 28 November.", anna)))
	evs, err := db.Events(ctx, "Trip", trip.ID, ola)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if err != nil {
		kinds = append(kinds, err.Error())
	}
	step(16, strings.Join(kinds, ", "))
	story.done()

	// The reader's comment, as the chapter shows it: the members the engine answers.
	var shown map[string]any
	if err := json.Unmarshal([]byte(fenced(t, page, "a comment")), &shown); err != nil {
		t.Fatal(err)
	}
	answered := map[string]any{}
	if b, err := json.Marshal(byReader); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &answered); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(keys(shown), keys(answered)) || answered["actor_kind"] != "agent" || answered["actor_for"] != "ola" || answered["text"] != shown["text"] {
		t.Errorf("the chapter shows a comment with %v, and the engine answers %v", keys(shown), answered)
	}
	var members []string
	for _, row := range rows(marked(t, page, "the members")) {
		for _, m := range strings.Split(row[0], ",") {
			members = append(members, strings.TrimSpace(m))
		}
	}
	sort.Strings(members)
	if !reflect.DeepEqual(members, keys(answered)) {
		t.Errorf("the chapter lists the members %v, and the engine answers %v", members, keys(answered))
	}

	// What is refused: each row, as the engine refuses it.
	other, err := db.Create(ctx, "Trip", fado.Values{"what": "Hotel"}, ola)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := db.Create(ctx, "Trip", fado.Values{"what": "Entered twice"}, ola)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "Trip", gone.ID, ola); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Set(ctx, "Trip", other.ID, fado.Values{"cost": "300.00 PLN"}, ola, fado.Key("ola-set")); err != nil {
		t.Fatal(err)
	}
	refusal := func(err error) (string, string) {
		var e *fado.Error
		if !errors.As(err, &e) {
			return fmt.Sprint(err), ""
		}
		return e.Code, e.Path
	}
	tries := []func() error{
		func() error {
			_, e1 := db.Comment(ctx, "Trip", other.ID, "Seen.", piotr)
			_, e2 := db.Comment(ctx, "Trip", gone.ID, "Seen.", ola)
			if c1, _ := refusal(e1); c1 != "unknown_document" {
				return e1
			}
			return e2
		},
		func() error { _, err := db.Comment(ctx, "Trip", other.ID, " \n ", ola); return err },
		func() error { _, err := db.Comment(ctx, "Trip", other.ID, "a\x07b", ola); return err },
		func() error {
			_, err := db.Comment(ctx, "Trip", other.ID, strings.Repeat("x", 65537), ola)
			return err
		},
		func() error { _, err := db.Comment(ctx, "Trip", other.ID, "Read.", ola, fado.Saw(1)); return err },
		func() error {
			_, err := db.Comment(ctx, "Trip", other.ID, "Read.", ola, fado.Key("ola-set"))
			return err
		},
		func() error {
			_, err := db.Comment(ctx, "Trip", other.ID, "Then.", ola, fado.At(time.Now().Add(-time.Hour)))
			return err
		},
	}
	table := rows(marked(t, page, "what is refused"))
	if len(table) != len(tries) {
		t.Fatalf("the chapter refuses %d things, and the test tries %d", len(table), len(tries))
	}
	for i, row := range table {
		code, path := refusal(tries[i]())
		if code != row[1] || path != row[2] {
			t.Errorf("%s: the engine refuses with %q at %q, and the chapter says %q at %q", row[0], code, path, row[1], row[2])
		}
	}
}
