package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

// BenchmarkRequestLines is the building of a request that carries a
// hundred lines of four values (ADR-0085, Performance): the lines of the
// call typed column by column, the address, the header that reads the
// secret, and a body whose repeat runs four formulas for each line, as
// send does before anything goes to the network. The budget is under
// 1 ms.
func BenchmarkRequestLines(b *testing.B) {
	net := &fakeOutbound{connections: map[string][]string{"bc": {"bc.example"}}}
	s, err := Open(filepath.Join(b.TempDir(), "bench.fado"), Options{Outbox: OutboxOptions{Manual: true}, Outbound: net,
		Secrets: fixedSecrets(map[string]string{"bc/token": "bc-s3cr3t"})})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{
  "name": "bc.sales_order",
  "params": {
    "customer": {"type": "text", "required": true},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)", "note": "text"}, "required": true}
  },
  "connection": {"name": "bc", "secrets": ["token"]},
  "request": {
    "method": "POST",
    "url": "\"https://bc.example/api/v2.0/salesOrders\"",
    "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
    "body": {
      "customerNumber": "customer",
      "salesOrderLines": {
        "each": "lines",
        "as": "line",
        "item": {
          "lineObjectNumber": "line.sku",
          "quantity": "VALUE(TEXT(line.qty))",
          "unitPrice": "VALUE(TEXT(AMOUNT(line.price)))",
          "description": "line.note"
        }
      }
    }
  }
}`), asSetup); err != nil {
		b.Fatal(err)
	}
	lines := make([]any, 100)
	for i := range lines {
		lines[i] = map[string]any{"sku": "SOFA-" + strconv.Itoa(i), "qty": 1 + i%4, "price": "2400.00 PLN", "note": "A sofa with three seats, in grey"}
	}
	a, args, e := s.trial("bc.sales_order", Values{"customer": "WEB-PL", "lines": lines})
	if e != nil {
		b.Fatal(e)
	}
	call := &ActionCall{Key: "evt-1:0", Attempt: 1, Action: a.spec.Name, Args: args}
	build := func() (*OutboundRequest, *Error) {
		x := &sending{s: s, spec: a.spec, call: call, e: s.reader()}
		defer s.release(x.e)
		if fail, _ := x.fetchSecrets(ctx, a.reads, false); fail != nil {
			return nil, fail
		}
		return x.request(false)
	}
	req, e := build()
	if e != nil {
		b.Fatal(e)
	}
	b.SetBytes(int64(len(req.Body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, e := build(); e != nil {
			b.Fatal(e)
		}
	}
}
