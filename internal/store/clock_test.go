package store

import (
	"context"
	"testing"
	"time"
)

// At chooses the moment of a preview or a read (ADR-0042): the formulas
// see it, the commit's timestamps do not, and a command that writes
// refuses it.
func TestAt_PreviewRunsAtTheMoment(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	cls := `{"name": "Stamp", "variables": {"at": {"type": "datetime", "default": "NOW()"}, "day": {"type": "date", "default": "TODAY()"}, "n": {"type": "number"}},
	  "states": {"initial": "s", "list": ["s"]}}`
	if _, err := s.DefineClass(ctx, []byte(cls), asSetup); err != nil {
		t.Fatal(err)
	}
	moment := time.Date(2027, 3, 4, 5, 6, 7, 0, time.UTC)
	pv, err := s.Preview(ctx, "Stamp", "", Values{"n": 1}, "", As(alice), At(moment))
	if err != nil {
		t.Fatal(err)
	}
	if pv.Now != moment.Unix() || pv.Document.Values["at"] != moment.Unix() || pv.Document.Values["day"] != "2027-03-04" {
		t.Errorf("preview at %v: now %d, values %v", moment, pv.Now, pv.Document.Values)
	}
	// The test clock ticks a second per call from start; the timestamps
	// come from it, not from the moment.
	if c := pv.Document.CreatedAt; c == moment.Unix() || c < start.Unix() || c > start.Unix()+60 {
		t.Errorf("the timestamps stay the database's clock: created_at %d, moment %d, start %d", c, moment.Unix(), start.Unix())
	}
	// Without At, and with a zero At, the database's clock: the moment
	// reported is the one the timestamps carry.
	for _, opts := range [][]Option{{As(alice)}, {As(alice), At(time.Time{})}} {
		pv, err := s.Preview(ctx, "Stamp", "", nil, "", opts...)
		if err != nil {
			t.Fatal(err)
		}
		if pv.Now != pv.Document.CreatedAt || pv.Document.Values["at"] != pv.Now || pv.Now < start.Unix() {
			t.Errorf("preview at the clock: now %d, at %v, created_at %d", pv.Now, pv.Document.Values["at"], pv.Document.CreatedAt)
		}
	}
	// A draft preview too.
	dp, err := s.PreviewDraft(ctx, []byte(cls), "", nil, "", At(moment), asSetup)
	if err != nil {
		t.Fatal(err)
	}
	if dp.Now != moment.Unix() || dp.Document.Values["at"] != moment.Unix() {
		t.Errorf("draft preview at %v: now %d, values %v", moment, dp.Now, dp.Document.Values)
	}
}

func TestAt_RefusedOnCommands(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "T", "variables": {"a": {"type": "number"}}, "states": {"initial": "s", "list": ["s", "u"]}, "transitions": [{"name": "go", "from": "s", "to": "u"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Fx", "columns": {"code": "text", "rate": "decimal(6)"}, "key": ["code"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "T", Values{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	at := At(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	refusals := map[string]func() error{
		"create":     func() error { _, err := s.Create(ctx, "T", Values{"a": 2}, at); return err },
		"batch":      func() error { _, err := s.CreateMany(ctx, "T", []Values{{"a": 2}}, at); return err },
		"set":        func() error { _, err := s.Set(ctx, "T", doc.ID, Values{"a": 2}, at); return err },
		"transition": func() error { _, err := s.Transition(ctx, "T", doc.ID, "go", nil, at); return err },
		"delete":     func() error { return s.Delete(ctx, "T", doc.ID, at) },
		"migration":  func() error { _, err := s.Migrate(ctx, "T", at, asSetup); return err },
		"row import": func() error {
			_, err := s.ImportRows(ctx, "Fx", []Values{{"code": "EUR", "rate": "4.3"}}, at)
			return err
		},
	}
	for name, call := range refusals {
		err := call()
		e, ok := err.(*Error)
		if !ok || e.Code != CodeNotAllowed {
			t.Errorf("%s with At: want not_allowed, got %v", name, err)
			continue
		}
		if e.Hint == "" || e.Message == "" {
			t.Errorf("%s: a refusal explains itself: %v", name, err)
		}
	}
	// Nothing was written.
	got, err := s.Get(ctx, "T", doc.ID)
	if err != nil || got.Version != 1 || got.State != "s" {
		t.Errorf("after the refusals: %v %v", got, err)
	}
	if rows, err := s.TableRows(ctx, "Fx"); err != nil || len(rows) != 0 {
		t.Errorf("rows imported under At: %v %v", rows, err)
	}
}
