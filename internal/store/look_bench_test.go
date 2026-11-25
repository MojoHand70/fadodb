package store

import (
	"fmt"
	"testing"
)

// BenchmarkQueryLook asks a free question that two indexes fit, the
// invoices of one order for one customer: the engine looks into both
// indexes, at most a thousand documents each, and asks through the one
// that ends first (ADR-0075, item 10). It is the whole call of the
// command `query`, which returns the forty invoices the question finds.
// BenchmarkQueryOneIndex asks the same documents through the one
// comparison of the pointer, and nobody looks: the difference between
// the two is what the looks cost.
func BenchmarkQueryLook(b *testing.B) {
	benchLook(b, `order = REF("Order", "ORD-%d") AND customer = "Globex"`, 40)
}

// BenchmarkQueryOneIndex is the control of BenchmarkQueryLook.
func BenchmarkQueryOneIndex(b *testing.B) {
	benchLook(b, `order = REF("Order", "ORD-%d") AND customer & "" = "Globex"`, 40)
}

func benchLook(b *testing.B, where string, want int) {
	s, ctx := openLook(b)
	who := As(liveEwa)
	ask := func(i int) error {
		docs, err := s.Query(ctx, Filter{Class: "Invoice", Where: fmt.Sprintf(where, i%30), Limit: 100}, who)
		if err == nil && len(docs) < want {
			err = fmt.Errorf("%d documents, want %d", len(docs), want)
		}
		return err
	}
	for i := 0; i < 60; i++ {
		if err := ask(i); err != nil {
			b.Fatal(err)
		}
	}
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := ask(i); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
