package store

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkReceiveLines is the part of a message with a hundred lines
// that comes before its command (ADR-0085, Performance): the inlet's
// formulas over a body of about 20 KiB, the key, the id, two values and
// a repeat of three columns over the hundred elements, each handed to
// its formulas as a small json value of its own. The budget is under
// 1 ms. BenchmarkReceive is a message without a list, with its command.
func BenchmarkReceiveLines(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Users: true, Inbound: true, Outbox: OutboxOptions{Manual: true},
		Secrets: func(context.Context, string, string) (string, error) { return "the key of commercetools", nil }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupDesigners}, Groups: []string{"orders"}},
		{ID: "ct-in", Kind: ActorService, Groups: []string{"commerce"}},
	} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(ctOrder), olgaBoth); err != nil {
		b.Fatal(err)
	}
	// The inlet of the record: every formula of a row reads its element.
	inlet := strings.Replace(ctInlet, `JSONVALUE(BODY, \"order.currency\"))`, `JSONVALUE(item, \"price.value.currencyCode\"))`, 1)
	if _, err := s.DefineInlet(ctx, []byte(inlet), olgaBoth); err != nil {
		b.Fatal(err)
	}
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"id": "line-`+strconv.Itoa(i)+`", "name": {"en": "A sofa with three seats, in grey"}, "variant": {"id": 1, "sku": "SOFA-`+strconv.Itoa(i)+
			`"}, "quantity": `+strconv.Itoa(1+i%4)+`, "price": {"value": {"type": "centPrecision", "centAmount": 240000, "currencyCode": "PLN", "fractionDigits": 2}}}`)
	}
	body := []byte(`{"id": "evt-1", "type": "OrderCreated", "order": {"id": "ct-1001", "orderNumber": "1001", "customerEmail": "buyer@globex.example", "lineItems": [` + strings.Join(lines, ",") + `]}}`)
	m := &Message{Headers: map[string]string{"x-api-key": "the key of commercetools", "Content-Type": "application/json"}, Body: body}
	spec := s.inletNamed("ct.order").spec
	if made, e := s.makeOf(spec, m); e != nil || len(made.values["lines"].([]any)) != 100 {
		b.Fatalf("the message of the benchmark: %v, %v", made, e)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, e := s.makeOf(spec, m); e != nil {
			b.Fatal(e)
		}
	}
}
