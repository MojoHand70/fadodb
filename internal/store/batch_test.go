package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestCreateMany(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineInvoice(t, s)
	batch := make([]Values, 50)
	for i := range batch {
		v := sampleValues()
		v["number"] = fmt.Sprintf("B-%03d", i)
		batch[i] = v
	}
	docs, err := s.CreateMany(ctx, "Invoice", batch, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 50 || docs[0].Values["number"] != "B-000" || docs[49].Values["number"] != "B-049" || docs[7].Values["total"] != "100.00 PLN" || docs[7].Values["owner"] != "alice" {
		t.Errorf("batch: %d documents, %s", len(docs), dump(docs[7]))
	}
	seen := map[string]bool{}
	for _, d := range docs {
		if seen[d.ID] || d.Version != 1 {
			t.Errorf("document %s version %d", d.ID, d.Version)
		}
		seen[d.ID] = true
	}
	if n, _ := s.Count(ctx, Filter{Class: "Invoice"}); n != 50 {
		t.Errorf("%d documents stored", n)
	}
	var events int
	s.db.QueryRow("SELECT count(*) FROM events").Scan(&events)
	if events != 50 {
		t.Errorf("%d events, want one per document", events)
	}
	// All or none: one bad document and nothing is written.
	bad := []Values{{"number": "C-1"}, {"number": "C-2", "status": "lost"}, {"number": "C-3"}}
	_, err = s.CreateMany(ctx, "Invoice", bad)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeNotInValues || e.Variable != "[1].status" {
		t.Errorf("bad batch: %v", err)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Invoice"}); n != 50 {
		t.Errorf("a refused batch wrote documents: %d", n)
	}
	// No key, no id, an empty batch.
	if _, err := s.CreateMany(ctx, "Invoice", batch[:1], Key("k")); code(err) != CodeNotSupported {
		t.Errorf("batch with a key: %v", err)
	}
	if _, err := s.CreateMany(ctx, "Invoice", batch[:1], WithID("x")); code(err) != CodeBadValue {
		t.Errorf("batch with an id: %v", err)
	}
	if docs, err := s.CreateMany(ctx, "Invoice", nil); err != nil || len(docs) != 0 {
		t.Errorf("empty batch: %v %d", err, len(docs))
	}
	if _, err := s.CreateMany(ctx, "Nope", batch[:1]); code(err) != CodeUnknownClass {
		t.Errorf("unknown class: %v", err)
	}
}
