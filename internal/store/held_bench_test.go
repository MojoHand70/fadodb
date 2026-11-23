package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// benchHeld commits a change of the lines of a bill of twenty lines.
// With figures the bill has a total and a count worked out of its lines
// in the same commit (ADR-0081, step 2); without, it is the same commit
// of the same lines, which says what the figures cost.
func benchHeld(b *testing.B, figures bool) {
	s, err := Open(filepath.Join(b.TempDir(), "held.fado"), Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	ctx := context.Background()
	more := ""
	if figures {
		more = `, "count": {"formula": "COUNT(lines)"}, "total": {"formula": "SUM(lines.qty * lines.price)"}`
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Bill", "title": "number", "variables": {"number": {"type": "text"},
  "lines": {"type": "rows", "schema": {"what": "text", "qty": "decimal(0)", "price": "money(2)"}}`+more+`},
  "states": {"initial": "draft", "list": ["draft"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), asSetup); err != nil {
		b.Fatal(err)
	}
	who := As(benchReader)
	lines := func(first int) []any {
		out := make([]any, 20)
		for i := range out {
			out[i] = map[string]any{"what": fmt.Sprintf("line %d", i), "qty": 1 + (first+i)%7, "price": "10.50 PLN"}
		}
		return out
	}
	// Two sets of lines, made before the clock starts, taken in turn so
	// that every commit changes the list.
	sets := [2]Values{{"lines": lines(0)}, {"lines": lines(1)}}
	d, err := s.Create(ctx, "Bill", Values{"number": "B-1", "lines": lines(1)}, who)
	if err != nil {
		b.Fatal(err)
	}
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := s.Set(ctx, "Bill", d.ID, sets[i%2], who)
		if err != nil {
			b.Fatal(err)
		}
		if figures && got.Values["count"] != 20.0 {
			b.Fatalf("the count of lines: %v", got.Values["count"])
		}
		i++
	}
}

// BenchmarkHeldFigure is the commit with the figures, and
// BenchmarkHeldFigureNone the one without.
func BenchmarkHeldFigure(b *testing.B) { benchHeld(b, true) }

func BenchmarkHeldFigureNone(b *testing.B) { benchHeld(b, false) }
