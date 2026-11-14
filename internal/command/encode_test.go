package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/MojoHand70/fadodb/internal/store"
)

// The encoder written by hand is held to the bytes encoding/json writes
// for the same document (ADR-0055): an answer written here and an answer
// written by encoding/json, such as a preview with its document inside,
// must read the same.

func sameBytes(t *testing.T, d *store.Document) {
	t.Helper()
	want, wantErr := json.Marshal(d)
	got, gotErr := appendDocument(nil, d)
	if (wantErr != nil) != (gotErr != nil) {
		t.Fatalf("encoding/json fails with %v and the encoder with %v", wantErr, gotErr)
	}
	if wantErr == nil && !bytes.Equal(got, want) {
		t.Fatalf("the encoder wrote\n%s\nand encoding/json\n%s", got, want)
	}
}

func TestTheEncoderWritesWhatEncodingJSONWrites(t *testing.T) {
	texts := []string{"", "plain", `a "quoted" \ text`, "line\nbreak\ttab\r\b\f", "\x00\x01\x1f\x7f", "<script>&amp;</script>",
		"zażółć gęślą jaźń", "发票 🧾", "sep" + string(rune(0x2028)) + "and" + string(rune(0x2029)), "bad \xff\xfe utf8", "trailing \xc3", "FV/2026/11/0042"}
	numbers := []float64{0, math.Copysign(0, -1), 1, -1, 1.5, 0.1, 1230, 1e20, 1e21, 1.5e21, 1e-6, 1e-7, 9.99e-9, 1e-10, 1e100,
		123456789012345680, math.MaxFloat64, math.SmallestNonzeroFloat64, -2.5e-7, 3.0000000000000004}
	docs := []*store.Document{
		{},
		{Class: "Invoice", ID: "1", Title: "1", Version: 1, ClassVersion: 1, CreatedAt: 1763020800, UpdatedAt: 1763020800, Values: store.Values{}},
		{Class: "Invoice", ID: "FV/2026/11/0042", Title: "FV/2026/11/0042", State: "approved", Version: 4, ClassVersion: 1,
			CreatedAt: 1763020800, UpdatedAt: 1763035200, StateEnteredAt: 1763035200,
			CreatedBy: "bob", UpdatedBy: "ap-agent", StateEnteredBy: "ap-agent", StateEnteredFrom: "review",
			Values: store.Values{"customer": "Customer:42", "total": "1230.00 PLN", "note": "matches order PO-118", "paid": false, "blank": nil,
				"seen": int64(1763035200), "rate": 4.25,
				"address": map[string]any{"city": "Kraków", "geo": map[string]any{"lat": 50.06, "lon": 19.94}, "line2": nil},
				"lines":   []any{map[string]any{"sku": "A-1", "qty": float64(2)}, map[string]any{"sku": "B-2", "qty": float64(1)}},
				"tags":    []any{"urgent", "export", nil, 3.5, true},
				"none":    []any{},
				"payload": json.RawMessage(`{ "a" : [1, 2, {"b": "<x>"}] }`)},
			Computed: map[string]store.Moment{"rate": {Version: 3, At: 1763030000}, "check": {Error: "div_by_zero"},
				"both": {Version: 2, At: 1763020900, Error: "overflow"}, "at_only": {At: 5}}},
		{Class: "A", ID: "x", Values: store.Values{"absent": (map[string]any)(nil), "empty": ([]any)(nil), "typed": store.Values{"a": 1}, "ints": []int{1, 2}}},
		{Class: "A", ID: "x", Values: store.Values{"nan": math.NaN()}},
		{Class: "A", ID: "x", Values: store.Values{"inf": []any{math.Inf(1)}}},
	}
	for _, text := range texts {
		docs = append(docs, &store.Document{Class: text, ID: text, Title: text, State: text, CreatedBy: text,
			Values: store.Values{text: text, "in": map[string]any{text: []any{text}}}, Computed: map[string]store.Moment{text: {Error: text}}})
	}
	for _, n := range numbers {
		docs = append(docs, &store.Document{Class: "N", ID: "n", Values: store.Values{"n": n, "list": []any{n, -n}}})
	}
	for _, d := range docs {
		sameBytes(t, d)
	}
	// A document that is not there is null, as a pointer that is nil is.
	if got, _ := appendDocument(nil, nil); string(got) != "null" {
		t.Fatalf("no document was written as %s", got)
	}
}

// TestTheEncoderOnRandomDocuments holds the encoder to encoding/json on
// documents made of random bytes and random numbers.
func TestTheEncoderOnRandomDocuments(t *testing.T) {
	r := rand.New(rand.NewSource(55))
	text := func() string {
		b := make([]byte, r.Intn(24))
		for i := range b {
			switch r.Intn(4) {
			case 0:
				b[i] = byte(r.Intn(256))
			case 1:
				b[i] = byte(r.Intn(0x30))
			default:
				b[i] = byte('a' + r.Intn(26))
			}
		}
		if r.Intn(3) == 0 {
			return string(b) + string(rune(r.Intn(0x3000)))
		}
		return string(b)
	}
	number := func() float64 {
		switch r.Intn(4) {
		case 0:
			return float64(r.Int63n(1_000_000)) / 100
		case 1:
			return math.Float64frombits(r.Uint64())
		case 2:
			return float64(r.Int63())
		}
		return r.NormFloat64() * math.Pow(10, float64(r.Intn(60)-30))
	}
	var value func(depth int) any
	value = func(depth int) any {
		switch n := r.Intn(9); {
		case n == 0:
			return nil
		case n == 1:
			return r.Intn(2) == 0
		case n == 2:
			return r.Int63() - r.Int63()
		case n <= 4:
			return number()
		case n <= 6 || depth > 2:
			return text()
		case n == 7:
			m := map[string]any{}
			for i := r.Intn(5); i > 0; i-- {
				m[text()] = value(depth + 1)
			}
			return m
		}
		list := make([]any, r.Intn(5))
		for i := range list {
			list[i] = value(depth + 1)
		}
		return list
	}
	for i := 0; i < 3000; i++ {
		d := &store.Document{Class: text(), ID: text(), Title: text(), State: text(), Version: r.Int63n(9), ClassVersion: r.Intn(3),
			CreatedAt: r.Int63n(2e9), UpdatedAt: r.Int63n(2e9), StateEnteredAt: r.Int63n(2), CreatedBy: text(), UpdatedBy: text(),
			StateEnteredBy: text(), StateEnteredFrom: text(), Values: store.Values{}}
		for j := r.Intn(12); j > 0; j-- {
			d.Values[text()] = value(0)
		}
		if r.Intn(3) == 0 {
			d.Computed = map[string]store.Moment{}
			for j := r.Intn(4); j > 0; j-- {
				d.Computed[text()] = store.Moment{Version: r.Int63n(3), At: r.Int63n(3), Error: []string{"", "overflow"}[r.Intn(2)]}
			}
		}
		sameBytes(t, d)
	}
}

// fifty is a document of fifty variables, as the budget of ADR-0018
// measures one: texts, numbers, a money, a date, a datetime, a ref, a
// blank and two objects.
func fifty() *store.Document {
	d := &store.Document{Class: "Invoice", ID: "FV/2026/11/0042", Title: "Invoice for Customer 42", State: "approved", Version: 4, ClassVersion: 1,
		CreatedAt: 1763020800, UpdatedAt: 1763035200, StateEnteredAt: 1763035200,
		CreatedBy: "bob", UpdatedBy: "ap-agent", StateEnteredBy: "ap-agent", StateEnteredFrom: "review", Values: store.Values{}}
	for i := 0; i < 48; i++ {
		name := fmt.Sprintf("variable_%02d", i)
		switch i % 8 {
		case 0:
			d.Values[name] = "a text of ordinary length, number " + fmt.Sprint(i)
		case 1:
			d.Values[name] = float64(i) * 12.5
		case 2:
			d.Values[name] = "1230.00 PLN"
		case 3:
			d.Values[name] = "2026-11-13"
		case 4:
			d.Values[name] = int64(1763035200 + i)
		case 5:
			d.Values[name] = "Customer:42"
		case 6:
			d.Values[name] = nil
		case 7:
			d.Values[name] = true
		}
	}
	d.Values["address"] = map[string]any{"line1": "ul. Floriańska 3", "city": "Kraków", "postal_code": "31-019", "country": "PL"}
	d.Values["contact"] = map[string]any{"name": "Anna Nowak", "email": "anna@example.com", "phone": nil}
	return d
}

func BenchmarkEncodeDocument50(b *testing.B) {
	d := fifty()
	buf := make([]byte, 0, 4096)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var err error
		if buf, err = appendDocument(buf[:0], d); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncodeDocument50Reflect is the same document through
// encoding/json, for the comparison.
func BenchmarkEncodeDocument50Reflect(b *testing.B) {
	d := fifty()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(d); err != nil {
			b.Fatal(err)
		}
	}
}
