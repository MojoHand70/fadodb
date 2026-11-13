package fado_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// TestExampleClasses keeps examples/classes honest: every sample class
// the skills draw on validates and defines against the engine as it is,
// so a change that breaks a sample fails here before a reader meets it.
// A sample may name another, as the Invoice's rule names the Receipt it
// creates: the test defines what it can and comes back to the rest, so
// the order of the files does not matter.
func TestExampleClasses(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "examples", "classes", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no sample classes under examples/classes")
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "examples.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for len(files) > 0 {
		var later []string
		for _, f := range files {
			def, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if diags := db.ValidateClass(def); len(diags) != 0 {
				later = append(later, f)
				continue
			}
			if _, err := db.DefineClass(ctx, def, setup); err != nil {
				t.Errorf("%s does not define: %v", filepath.Base(f), err)
			}
		}
		if len(later) == len(files) {
			// Nothing more defines alone. Samples that name each other, as
			// the Purchase and the Approval do, are defined together; what
			// does not hold even so are the samples that are wrong.
			var set [][]byte
			for _, f := range later {
				def, _ := os.ReadFile(f)
				set = append(set, def)
			}
			if diags := db.ValidateClasses(set); len(diags) != 0 {
				for i, f := range later {
					t.Errorf("%s does not validate, alone or with the others (definition %d of the set):\n%v", filepath.Base(f), i, diags)
				}
				return
			}
			if _, err := db.DefineClasses(ctx, set, setup); err != nil {
				t.Errorf("the samples that name each other do not define together: %v", err)
			}
			return
		}
		files = later
	}
}

// TestExampleApprovals walks the Purchase and Approval samples through
// what their descriptions promise: submitting a purchase asks every
// approver, each answers their own, and the purchase moves when all have
// granted, or at the first refusal.
func TestExampleApprovals(t *testing.T) {
	var set [][]byte
	for _, name := range []string{"purchase.json", "approval.json"} {
		def, err := os.ReadFile(filepath.Join("..", "examples", "classes", name))
		if err != nil {
			t.Fatal(err)
		}
		set = append(set, def)
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "approvals.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// Neither sample validates alone; the two do together.
	for i, def := range set {
		if diags := db.ValidateClass(def); len(diags) == 0 {
			t.Errorf("sample %d validates alone; the README says the two name each other", i)
		}
	}
	if _, err := db.DefineClasses(ctx, set, setup); err != nil {
		t.Fatal(err)
	}
	ann, bob := fado.As(fado.Principal{ID: "ann"}), fado.As(fado.Principal{ID: "bob"})
	state := func(id string) string {
		t.Helper()
		doc, err := db.Get(ctx, "Purchase", id)
		if err != nil {
			t.Fatal(err)
		}
		return doc.State
	}
	answer := func(id, transition string, as fado.Option) {
		t.Helper()
		if _, err := db.Transition(ctx, "Approval", id, transition, nil, as); err != nil {
			t.Fatalf("%s %s: %v", transition, id, err)
		}
		if _, err := db.Drain(ctx, setup); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"P-1", "P-2"} {
		if _, err := db.Create(ctx, "Purchase", fado.Values{"number": id, "approvers": []any{"ann", "bob"}}, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Transition(ctx, "Purchase", id, "submit", nil); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.Drain(ctx, setup); err != nil || n != 4 {
		t.Fatalf("submitting two purchases asked %d approvers: %v", n, err)
	}
	// Only the approver answers their own approval.
	if _, err := db.Transition(ctx, "Approval", "P-1-ann", "grant", nil, bob); err == nil {
		t.Error("bob granted ann's approval")
	}
	answer("P-1-ann", "grant", ann)
	if got := state("P-1"); got != "waiting" {
		t.Errorf("after one of two approvals the purchase is %s", got)
	}
	answer("P-1-bob", "grant", bob)
	if got := state("P-1"); got != "approved" {
		t.Errorf("after both approvals the purchase is %s", got)
	}
	// One refusal rejects the other purchase, whatever the rest say.
	answer("P-2-bob", "refuse", bob)
	if got := state("P-2"); got != "rejected" {
		t.Errorf("after a refusal the purchase is %s", got)
	}
}
