package fado_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on lists is a reference: it says what a QUERY in a live
// variable is, what its condition reads, who gets which rows and how a
// page of it is asked for. TestLists takes the two classes out of the
// chapter, plays its story, and holds every table and every block of
// the chapter to what the engine answers.
func TestLists(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "lists.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows checks the chapter out with \r\n; the blocks are found by \n.
	page = bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
	ctx := context.Background()
	classes := map[string][]byte{}
	for _, m := range regexp.MustCompile("(?s)```json\n(.*?)\n```").FindAllSubmatch(page, -1) {
		if kindOf(m[1]) == "class" {
			classes[className(string(m[1]))] = m[1]
		}
	}
	db := notionDB(t, fado.Options{})
	// The class that holds the list is defined after its source.
	for _, name := range []string{"Invoice", "Order"} {
		if classes[name] == nil {
			t.Fatalf("the chapter shows the class %s", name)
		}
		if _, err := db.DefineClass(ctx, classes[name], setup); err != nil {
			t.Fatal(err)
		}
	}
	people := map[string]fado.Option{"Jan": person("jan", "sales"), "Piotr": person("piotr", "sales"), "Ewa": person("ewa", "finance")}
	for _, o := range []string{"ORD-17", "ORD-18"} {
		if _, err := db.Create(ctx, "Order", fado.Values{"number": o, "customer": "Globex"}, people["Jan"], fado.WithID(o)); err != nil {
			t.Fatal(err)
		}
	}
	// The story: Jan writes FV-1 and FV-2 on ORD-17, Piotr FV-3, and Jan
	// FV-4 on ORD-18.
	for _, inv := range []struct{ id, order, net, who string }{
		{"FV-1", "ORD-17", "250.00 PLN", "Jan"}, {"FV-2", "ORD-17", "1000.00 PLN", "Jan"},
		{"FV-3", "ORD-17", "70.00 PLN", "Piotr"}, {"FV-4", "ORD-18", "5.00 PLN", "Jan"},
	} {
		values := fado.Values{"number": inv.id, "order": "Order:" + inv.order, "net": inv.net, "margin": "10.00 PLN"}
		if _, err := db.Create(ctx, "Invoice", values, people[inv.who], fado.WithID(inv.id)); err != nil {
			t.Fatal(err)
		}
	}
	// listed are the ids of the rows, as the chapter writes them.
	listed := func(v *fado.LiveValue) string {
		var ids []string
		for _, r := range v.Rows {
			ids = append(ids, r.ID)
		}
		if len(ids) == 0 {
			return "none"
		}
		return strings.Join(ids, ", ")
	}
	for _, row := range rows(marked(t, page, "who reads what")) {
		who, order, want := row[0], row[1], row[2]
		got, err := db.Live(ctx, "Order", order, "invoices", people[who])
		if err != nil || listed(got) != want {
			t.Errorf("%s reads the invoices of %s: the engine answers %v, %v, and the chapter says %s", who, order, got, err, want)
			continue
		}
		// The margin is for finance alone.
		for _, r := range got.Rows {
			if _, has := r.Values["margin"]; has != (who == "Ewa") {
				t.Errorf("%s reads the margin of %s: %v, and the chapter says that finance alone does", who, r.ID, has)
			}
		}
	}

	// The command and its answer, as the chapter shows them.
	squeeze := func(text []byte) string { return strings.Join(strings.Fields(string(text)), "") }
	var asked fado.LiveRead
	if err := json.Unmarshal(jsoncAt(t, page, "the command"), &asked); err != nil {
		t.Fatal(err)
	}
	got, err := db.LivePage(ctx, asked, people["Jan"])
	if err != nil {
		t.Fatal(err)
	}
	answered, _ := json.Marshal(got)
	if want := squeeze(jsoncAt(t, page, "the answer")); squeeze(answered) != want {
		t.Errorf("the command answers %s, and the chapter shows %s", squeeze(answered), want)
	}

	// The page of the list.
	for _, row := range rows(marked(t, page, "the page")) {
		with, want, more := row[0], row[1], row[2]
		read := fado.LiveRead{}
		if with != "nothing more" {
			if err := json.Unmarshal([]byte("{"+with+"}"), &read); err != nil {
				t.Fatalf("the chapter asks with %s: %v", with, err)
			}
		}
		read.Class, read.ID, read.Variable = "Order", "ORD-17", "invoices"
		got, err := db.LivePage(ctx, read, people["Ewa"])
		if err != nil || listed(got) != want || got.More != (more == "yes") {
			t.Errorf("Ewa asks with %s: the engine answers %v, %v, and the chapter says %s, more %s", with, got, err, want, more)
		}
	}

	// Nothing is kept: the order does not carry its list, and one more
	// invoice is in it at once, with the order where it was.
	before, err := db.Get(ctx, "Order", "ORD-17", people["Jan"])
	if err != nil {
		t.Fatal(err)
	}
	if _, carried := before.Values["invoices"]; carried {
		t.Error("get answers the list of the order, and the chapter says it is kept nowhere")
	}
	if _, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV-5", "order": "Order:ORD-17"}, people["Jan"], fado.WithID("FV-5")); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Live(ctx, "Order", "ORD-17", "invoices", people["Jan"]); err != nil || listed(got) != "FV-1, FV-2, FV-5" {
		t.Errorf("after one more invoice Jan reads %v, %v; the chapter says the next reader sees it", got, err)
	}
	if after, err := db.Get(ctx, "Order", "ORD-17", people["Jan"]); err != nil || after.Version != before.Version {
		t.Errorf("the order is %v, %v; the chapter says it is still at version %d", after, err, before.Version)
	}

	// What is refused when the class is defined.
	if _, err := db.DefineTable(ctx, []byte(`{"name": "rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"]}`), setup); err != nil {
		t.Fatal(err)
	}
	list := func(variable string) string {
		return scratch("T", `"n": {"type": "text"}, "l": {`+variable+`}`, oneState)
	}
	live := func(formula string) string { return list(`"mode": "live", "formula": "` + formula + `"`) }
	heldBy(t, page, "refused", map[string][]string{
		"a QUERY in a variable that is not live":         {list(`"formula": "QUERY(Invoice, number)"`), list(`"mode": "triggered", "on": ["refresh"], "formula": "QUERY(Invoice, number)"`)},
		"a QUERY inside a larger formula":                {live(`IF(TRUE, QUERY(Invoice, number), BLANK)`)},
		"a source nobody defined":                        {live(`QUERY(Nothing, number)`)},
		"a table as the source":                          {live(`QUERY(rates, code)`)},
		"a column the source has not":                    {live(`QUERY(Invoice, {number, nothing})`)},
		"a count among the columns":                      {live(`QUERY(Invoice, {number, COUNT(net)})`)},
		"a condition that is no condition":               {live(`QUERY(Invoice, number, net)`)},
		"a pointer compared with a text":                 {live(`QUERY(Invoice, number, order = \"Order:ORD-17\")`)},
		"REF() compared with a pointer to another class": {live(`QUERY(Invoice, number, order = REF())`)},
		"a LOOKUP in the condition":                      {live(`QUERY(Invoice, number, LEN(number) > LOOKUP(rates, rate, code = \"PLN\"))`)},
		"a live variable of the source as a column":      {live(`QUERY(Order, {number, invoices})`)},
		"braces anywhere else":                           {live(`LEN({n, n})`)},
		"REF() anywhere else":                            {live(`TEXT(REF())`), list(`"formula": "TEXT(REF())"`)},
	}, func(definition string) string { return finding(db, definition) })

	// The source may not lose what a list names.
	without := strings.Replace(string(classes["Invoice"]), `"margin": {"type": "money", "scale": 2, "label": "Margin"},`, "", 1)
	without = regexp.MustCompile(`(?s),\s*"rights": \{.*\}\s*\}\s*$`).ReplaceAllString(without, "}")
	if without == string(classes["Invoice"]) || strings.Contains(without, "margin") {
		t.Fatal("the test takes the margin out of the invoice of the chapter")
	}
	if _, err := db.DefineClass(ctx, []byte(without), setup); err == nil || !strings.Contains(err.Error(), "margin") {
		t.Errorf("an invoice without the margin the order lists: %v; the chapter says it is refused", err)
	}
}
