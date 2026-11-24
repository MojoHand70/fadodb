package fado_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

	// The figures over the list, for each reader (ADR-0081): one value
	// each, which counts what that reader may read of the list.
	shown := func(v any) string {
		switch v := v.(type) {
		case nil:
			return "blank"
		case bool:
			return strings.ToUpper(strconv.FormatBool(v))
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case string:
			return v
		}
		return fmt.Sprint(v)
	}
	for mark, many := range map[string]int{"figures": 6, "more figures": 3, "picked figures": 3, "shaped figures": 2} {
		figures := marked(t, page, mark)
		names := regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(strings.SplitN(figures, "\n", 2)[0], -1)
		if len(names) != many {
			t.Fatalf("the chapter's table of %s names %d figures in its heading", mark, len(names))
		}
		for _, row := range rows(figures) {
			who, order := row[0], row[1]
			for i, name := range names {
				got, err := db.Live(ctx, "Order", order, name[1], people[who])
				if err != nil || got.List || shown(got.Value) != row[2+i] {
					t.Errorf("%s reads %s of %s: the engine answers %v, %v, and the chapter says %s", who, name[1], order, got, err, row[2+i])
				}
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

	// What is refused of a figure when the class is defined.
	figure := func(variable string) string {
		return strings.Replace(string(classes["Order"]), `"all_paid":`, variable+`, "all_paid":`, 1)
	}
	fig := func(formula string) string { return figure(`"x": {"mode": "live", "formula": "` + formula + `"}`) }
	if fig("COUNT(invoices)") == string(classes["Order"]) {
		t.Fatal("the test adds a variable to the order of the chapter")
	}
	if got := finding(db, fig("COUNT(invoices)")); got != "accepted" {
		t.Fatalf("the order of the chapter with one more figure is refused: %s", got)
	}
	heldBy(t, page, "figures refused", map[string][]string{
		"a figure in a variable that is not live": {figure(`"x": {"type": "number", "formula": "COUNT(invoices)"}`), figure(`"x": {"type": "number", "mode": "triggered", "on": ["refresh"], "formula": "COUNT(invoices)"}`)},
		"a sum of a list and no column":           {fig(`SUM(invoices)`)},
		"a column the listed class has not":       {fig(`SUM(invoices.nothing)`)},
		"a sum of a text":                         {fig(`SUM(invoices.number)`)},
		"a condition that is no condition":        {fig(`COUNTIF(invoices, net)`)},
		"STATE() in the condition of a figure":    {fig(`COUNTIF(invoices, STATE() = \"paid\")`)},
		"a figure inside a figure":                {fig(`COUNTIF(invoices, net > MAX(invoices.net))`)},
		"a middle value of a text":                {fig(`MEDIAN(invoices.number)`)},
		"MEDIAN of single values":                 {fig(`MEDIAN(number)`), fig(`COUNTUNIQUE(customer)`)},
	}, func(definition string) string { return finding(db, definition) })

	// The figures over the lines a document holds (ADR-0081, step 2):
	// the bill of the chapter, step by step.
	if classes["Bill"] == nil {
		t.Fatal("the chapter shows the class Bill")
	}
	if _, err := db.DefineClass(ctx, classes["Bill"], setup); err != nil {
		t.Fatal(err)
	}
	ewa := people["Ewa"]
	line := func(what string, qty int, price string, delivered bool) map[string]any {
		return map[string]any{"what": what, "qty": qty, "price": price, "delivered": delivered}
	}
	paper, pens := line("paper", 2, "10.50 PLN", true), line("pens", 1, "0.25 PLN", true)
	steps := map[string]func() (*fado.Document, error){
		"writes B-1 with its three lines": func() (*fado.Document, error) {
			return db.Create(ctx, "Bill", fado.Values{"number": "B-1", "tags": []any{"red", "urgent", "red"},
				"lines": []any{paper, pens, line("ink", 3, "100.00 PLN", false)}}, ewa, fado.WithID("B-1"))
		},
		"marks the ink as delivered": func() (*fado.Document, error) {
			return db.Set(ctx, "Bill", "B-1", fado.Values{"lines": []any{paper, pens, line("ink", 3, "100.00 PLN", true)}}, ewa)
		},
		"takes the ink off the bill": func() (*fado.Document, error) {
			return db.Set(ctx, "Bill", "B-1", fado.Values{"lines": []any{paper, pens}}, ewa)
		},
		"changes the number alone": func() (*fado.Document, error) {
			return db.Set(ctx, "Bill", "B-1", fado.Values{"number": "B-1/A"}, ewa)
		},
	}
	held := marked(t, page, "held figures")
	heldNames := regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(strings.SplitN(held, "\n", 2)[0], -1)
	if len(heldNames) != 7 || len(rows(held)) != len(steps) {
		t.Fatalf("the chapter's table of the bill names %d figures in %d steps", len(heldNames), len(rows(held)))
	}
	after := map[string]*fado.Document{}
	for _, row := range rows(held) {
		do := steps[row[0]]
		if do == nil {
			t.Fatalf("the chapter has a step the test does not play: %s", row[0])
		}
		d, err := do()
		if err != nil {
			t.Fatalf("Ewa %s: %v", row[0], err)
		}
		after[row[0]] = d
		for i, name := range heldNames {
			if got := shown(d.Values[name[1]]); got != row[1+i] {
				t.Errorf("Ewa %s: %s is %s, and the chapter says %s", row[0], name[1], got, row[1+i])
			}
		}
	}
	// The same steps, read for the figures over the lines narrowed,
	// ordered and cut (ADR-0081, step 3).
	heldShaped := marked(t, page, "held shaped")
	shapedNames := regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(strings.SplitN(heldShaped, "\n", 2)[0], -1)
	if len(shapedNames) != 3 || len(rows(heldShaped)) != len(steps) {
		t.Fatalf("the chapter's second table of the bill names %d figures in %d steps", len(shapedNames), len(rows(heldShaped)))
	}
	for _, row := range rows(heldShaped) {
		d := after[row[0]]
		if d == nil {
			t.Fatalf("the chapter's second table has a step the first has not: %s", row[0])
		}
		for i, name := range shapedNames {
			if got := shown(d.Values[name[1]]); got != row[1+i] {
				t.Errorf("Ewa %s: %s is %s, and the chapter says %s", row[0], name[1], got, row[1+i])
			}
		}
	}
	// The guard counts the lines: a bill with none is not sent, one with
	// a line of no quantity neither, and B-1 is.
	for id, lines := range map[string][]any{"B-2": nil, "B-3": {map[string]any{"what": "ink", "price": "1.00 PLN"}}} {
		values := fado.Values{"number": id}
		if lines != nil {
			values["lines"] = lines
		}
		if _, err := db.Create(ctx, "Bill", values, ewa, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Transition(ctx, "Bill", id, "send", nil, ewa); err == nil {
			t.Errorf("the bill %s was sent", id)
		}
	}
	if sent, err := db.Transition(ctx, "Bill", "B-1", "send", nil, ewa); err != nil || sent.State != "sent" {
		t.Errorf("B-1 is sent: %v, %v", sent, err)
	}
	// A filter reads the computed figure, and does not fold by itself.
	if docs, err := db.Query(ctx, fado.Filter{Class: "Bill", Where: "count > 1"}, ewa); err != nil || len(docs) != 1 || docs[0].ID != "B-1" {
		t.Errorf("a filter on the count of lines: %v, %v", docs, err)
	}
	bill := func(old, with string) string {
		changed := strings.Replace(string(classes["Bill"]), old, with, 1)
		if changed == string(classes["Bill"]) {
			t.Fatalf("the test changes %s of the bill of the chapter", old)
		}
		return changed
	}
	heldBy(t, page, "held refused", map[string][]string{
		"a figure over the lines in a read rule": {bill(`"read": "INGROUP(\"finance\")"`, `"read": "COUNT(lines) > 0"`)},
		"a figure over the lines in a filter":    {"a filter"},
		"a figure over the lines in a title":     {bill(`"title": "number"`, `"title": "TEXT(COUNT(lines))"`)},
		"the list as a value":                    {bill(`"formula": "COUNT(lines)"`, `"formula": "lines"`), bill(`"formula": "MAX(lines.price)"`, `"formula": "lines.price"`)},
	}, func(definition string) string {
		if definition != "a filter" {
			return finding(db, definition)
		}
		_, err := db.Query(ctx, fado.Filter{Class: "Bill", Where: "COUNT(lines) > 1"}, ewa)
		var fe *fado.Error
		if !errors.As(err, &fe) {
			return fmt.Sprint(err)
		}
		return fe.Code
	})

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
