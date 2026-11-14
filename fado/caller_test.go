package fado_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on naming the caller is for the developer whose program
// holds the engine. Its program is examples/caller, which has a test of
// its own. TestNamingTheCaller holds the tables of the chapter and what
// it says in passing.

func TestNamingTheCaller(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "naming-the-caller.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	// A memo that anybody may write, and that writes down what the
	// engine made of its caller.
	memo := `{"name":"Memo","variables":{"text":{"type":"text"},
	  "who":{"type":"text","default":"USER()"},"kind":{"type":"text","default":"USERKIND()"},"for":{"type":"text","default":"ACTINGFOR()"},
	  "sales":{"type":"bool","default":"INGROUP(\"sales\")"},"all":{"type":"bool","default":"INGROUP(\"everyone\")"}},
	  "states":{"initial":"a","list":["a"]},
	  "access":{"read":"TRUE","create":"TRUE","update":"INGROUP(\"sales\")","delete":"INGROUP(\"everyone\")"}}`
	if _, err := db.DefineClass(ctx, []byte(memo), setup); err != nil {
		t.Fatal(err)
	}
	write := func(opts ...fado.Option) (*fado.Document, *fado.Event, error) {
		t.Helper()
		doc, err := db.Create(ctx, "Memo", fado.Values{"text": "x"}, opts...)
		if err != nil {
			return nil, nil, err
		}
		events, err := db.Events(ctx, "Memo", doc.ID)
		if err != nil || len(events) != 1 {
			t.Fatalf("the events of a new memo: %v, %v", events, err)
		}
		return doc, events[0], nil
	}
	written := func(p fado.Principal, more ...fado.Option) (*fado.Document, *fado.Event) {
		t.Helper()
		doc, event, err := write(append(more, fado.As(p))...)
		if err != nil {
			t.Fatal(err)
		}
		return doc, event
	}

	// The parts of a caller: the table names every part of a Principal
	// and no other.
	var listed, has []string
	for _, row := range rows(marked(t, page, "the principal")) {
		listed = append(listed, row[0])
	}
	kind := reflect.TypeOf(fado.Principal{})
	for i := 0; i < kind.NumField(); i++ {
		has = append(has, kind.Field(i).Name)
	}
	sort.Strings(listed)
	sort.Strings(has)
	if !reflect.DeepEqual(listed, has) {
		t.Errorf("the chapter lists the parts %v, and a Principal has %v", listed, has)
	}
	// What the engine uses each for.
	doc, event := written(fado.Principal{ID: "Ola Nowak <ola@example.com>", Kind: fado.ActorAgent, Groups: []string{"sales"}})
	if doc.Values["who"] != "Ola Nowak <ola@example.com>" || doc.CreatedBy != doc.Values["who"] || event.ActorID != doc.Values["who"] {
		t.Errorf("an id of any text: USER() is %v, created by %q, the event by %q", doc.Values["who"], doc.CreatedBy, event.ActorID)
	}
	if doc.Values["kind"] != "agent" || event.ActorKind != "agent" || doc.Values["sales"] != true || doc.Values["all"] != true {
		t.Errorf("the kind and the groups of a caller: %v, the event says %q", doc.Values, event.ActorKind)
	}
	if doc, _ := written(fado.Principal{ID: "ola", Groups: []string{"SALES"}}); doc.Values["sales"] != false {
		t.Error("a group in another case of letters is taken for the same group")
	}
	if doc, event := written(fado.Principal{ID: "ola"}); doc.Values["kind"] != nil || event.ActorKind != "" {
		t.Errorf("a caller whose kind the program does not say: USERKIND() is %v, the event says %q", doc.Values["kind"], event.ActorKind)
	}

	// How a call names its caller.
	nobody, event, err := write()
	if err != nil || nobody.CreatedBy != "" || event.ActorID != "" || nobody.Values["all"] != false || nobody.Values["sales"] != false {
		t.Errorf("a call with no As: %v, %v", nobody, err)
	}
	_, err = db.Set(ctx, "Memo", nobody.ID, fado.Values{"text": "y"})
	want(t, "nobody changes a memo that sales may change", err, "forbidden at /access/update")
	want(t, "nobody deletes a memo that everyone may delete", db.Delete(ctx, "Memo", nobody.ID), "forbidden at /access/delete")
	if _, err := db.Get(ctx, "Memo", nobody.ID); err != nil {
		t.Errorf("nobody reads a memo whose read line says TRUE: %v", err)
	}
	if _, _, err := write(fado.As(fado.Principal{})); err != nil {
		t.Errorf("a caller with nothing in it: %v", err)
	}
	if doc, _, err := write(fado.As(fado.Principal{ID: "first"}), fado.As(fado.Principal{ID: "second"})); err != nil || doc.CreatedBy != "second" {
		t.Errorf("a call with two As: %v, %v", doc, err)
	}
	got, err := db.Exec(ctx, fado.Principal{ID: "ola"}, "create", []byte(`{"class":"Memo","values":{"text":"by command"}}`))
	if err != nil || !strings.Contains(string(got), `"created_by":"ola"`) {
		t.Errorf("a command takes the principal as an argument: %s, %v", got, err)
	}
	// Give every caller an id: a caller with groups and no id passes
	// the lines that name its groups, and the log has no name for it.
	// When the engine refuses such a caller, this fails and the box in
	// the chapter goes.
	nameless := fado.As(fado.Principal{Groups: []string{"sales"}})
	changed, err := db.Set(ctx, "Memo", nobody.ID, fado.Values{"text": "changed by no name"}, nameless)
	if err != nil {
		t.Errorf("a caller with groups and no id is refused: %v", err)
	} else if changed.UpdatedBy != "" {
		t.Errorf("the log names a caller with no id: %q", changed.UpdatedBy)
	}

	// The system groups.
	constants := map[string]string{
		"GroupDesigners":             fado.GroupDesigners,
		"GroupAdministrators":        fado.GroupAdministrators,
		"GroupAnalysts":              fado.GroupAnalysts,
		"GroupContentAdministrators": fado.GroupContentAdministrators,
	}
	groups := rows(marked(t, page, "the system groups"))
	if len(groups) != len(constants) {
		t.Errorf("the chapter lists %d system groups, and the package has %d", len(groups), len(constants))
	}
	for _, row := range groups {
		if constants[row[0]] != row[1] {
			t.Errorf("the chapter says %s is %q, and the package says %q", row[0], row[1], constants[row[0]])
		}
		// The name is taken under System, and refused under Groups.
		if _, _, err := write(fado.As(fado.Principal{ID: "x", System: []string{row[1]}})); err != nil {
			t.Errorf("a caller in the system group %s: %v", row[1], err)
		}
		if _, _, err := write(fado.As(fado.Principal{ID: "x", Groups: []string{row[1]}})); fado.ErrorCode(err) != "bad_value" {
			t.Errorf("a group of the program's own with the name %s: %v", row[1], err)
		}
	}
	// What each group may do, by one call each.
	plain := fado.As(fado.Principal{ID: "x"})
	in := func(group string) fado.Option { return fado.As(fado.Principal{ID: "x", System: []string{group}}) }
	work := map[string]func(as fado.Option) error{
		fado.GroupDesigners: func(as fado.Option) error {
			_, err := db.DefineSchema(ctx, []byte(`{"name":"Thing","fields":{"a":"text"}}`), as)
			return err
		},
		fado.GroupAdministrators: func(as fado.Option) error {
			_, err := db.Drain(ctx, as)
			return err
		},
		fado.GroupAnalysts: func(as fado.Option) error {
			_, err := db.SQL(ctx, "select id from Memo", nil, as)
			return err
		},
	}
	for group, do := range work {
		if err := do(plain); fado.ErrorCode(err) != "forbidden" {
			t.Errorf("a caller in no system group does the work of %s: %v", group, err)
		}
		if err := do(in(group)); err != nil {
			t.Errorf("a caller in %s does its work: %v", group, err)
		}
	}
	// A content administrator acts past a rule, with a reason.
	celina := in(fado.GroupContentAdministrators)
	_, err = db.Set(ctx, "Memo", nobody.ID, fado.Values{"text": "z"}, celina)
	want(t, "a content administrator acts past a rule and gives no reason", err, "reason_required at /access/update")
	if _, err := db.Set(ctx, "Memo", nobody.ID, fado.Values{"text": "z"}, celina, fado.Reason("ticket 4711")); err != nil {
		t.Errorf("a content administrator acts past a rule with a reason: %v", err)
	}

	// Acting for somebody.
	bob := fado.Principal{ID: "bob", For: "ann"}
	_, _, err = write(fado.As(bob))
	want(t, "Bob acts for Ann with no substitution", err, "forbidden at /principal/for")
	host := notionDB(t, fado.Options{HostAnswersFor: true})
	if _, err := host.DefineClass(ctx, []byte(memo), setup); err != nil {
		t.Fatal(err)
	}
	if made, err := host.Create(ctx, "Memo", fado.Values{"text": "x"}, fado.As(bob)); err != nil || made.Values["for"] != "ann" || made.Values["who"] != "bob" {
		t.Errorf("a program that answers for it itself: %v, %v", made, err)
	} else if events, _ := host.Events(ctx, "Memo", made.ID); len(events) != 1 || events[0].ActorFor != "ann" || events[0].ActorID != "bob" {
		t.Errorf("the event of a call for somebody: %v", events)
	}

	// The business day.
	october, september := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	day := func(opts ...fado.Option) string {
		t.Helper()
		_, event, err := write(opts...)
		if err != nil {
			t.Fatal(err)
		}
		return event.BusinessDate
	}
	checks(t, page, "the business day", map[string]func(row []string){
		"the day the call names, with the option BusinessDate": func([]string) {
			if got := day(fado.As(fado.Principal{ID: "ola", Today: october}), fado.BusinessDate(september)); got != "2026-09-30" {
				t.Errorf("a call that names a business day, of a caller who has one: %s", got)
			}
		},
		"the caller's Today": func([]string) {
			if got := day(fado.As(fado.Principal{ID: "ola", Today: october})); got != "2026-10-31" {
				t.Errorf("a caller with a business day: %s", got)
			}
		},
		"the day of the database's clock": func([]string) {
			if got := day(fado.As(fado.Principal{ID: "ola"})); got != "2026-11-16" {
				t.Errorf("a call with no business day: %s", got)
			}
		},
	})

	// The options of a call: the table has every option of the package
	// and no other.
	source, err := os.ReadFile("fado.go")
	if err != nil {
		t.Fatal(err)
	}
	var options, shown []string
	for _, m := range regexp.MustCompile(`(?m)^func (\w+)\([^)]*\) Option \{`).FindAllSubmatch(source, -1) {
		options = append(options, string(m[1]))
	}
	for _, row := range rows(marked(t, page, "the options of a call")) {
		shown = append(shown, row[0])
	}
	sort.Strings(options)
	sort.Strings(shown)
	if len(options) == 0 || !reflect.DeepEqual(options, shown) {
		t.Errorf("the chapter lists the options %v, and the package has %v", shown, options)
	}
	if made, err := db.Create(ctx, "Memo", fado.Values{"text": "x"}, plain, fado.WithID("M-1")); err != nil || made.ID != "M-1" {
		t.Errorf("a create with an id of the caller's: %v, %v", made, err)
	}
	if _, err := db.Create(ctx, "Memo", fado.Values{"text": "x"}, plain, fado.At(clock.Add(time.Hour))); fado.ErrorCode(err) != "not_allowed" {
		t.Errorf("a change as of another moment: %v", err)
	}

	// What is refused, each row by its own caller, on a change and on a
	// read.
	refused := map[string][]fado.Principal{
		"gives a kind that is none of the three": {{ID: "x", Kind: "robot"}},
		"puts a name under System that is none of the four, everyone included": {
			{ID: "x", System: []string{"sales"}}, {ID: "x", System: []string{"everyone"}},
		},
		"puts the name of a system group, or everyone, under Groups": {
			{ID: "x", Groups: []string{"designers"}}, {ID: "x", Groups: []string{"everyone"}},
		},
		"names somebody under For, and no substitution says so": {bob},
	}
	listedRows := map[string]bool{}
	for _, row := range rows(marked(t, page, "what is refused")) {
		listedRows[row[0]] = true
		if len(refused[row[0]]) == 0 {
			t.Errorf("the chapter says a program that %q is refused, and the test has no such caller", row[0])
		}
		for _, p := range refused[row[0]] {
			_, _, change := write(fado.As(p))
			_, read := db.Get(ctx, "Memo", nobody.ID, fado.As(p))
			for what, err := range map[string]error{"a change": change, "a read": read} {
				var got *fado.Error
				if !errors.As(err, &got) || got.Code != row[1] || got.Path != row[2] {
					t.Errorf("a program that %s, on %s: the engine answers %v, and the chapter says %s %s", row[0], what, err, row[1], row[2])
				}
			}
		}
	}
	for what := range refused {
		if !listedRows[what] {
			t.Errorf("the test has a program that %q, and the chapter has no such row", what)
		}
	}
}
