package fado_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on views and SQL is a reference: the views of a class,
// their columns, how a value looks in SQL, what a statement may do and
// who may run one. TestViewsAndSQL takes the class out of the chapter,
// plays what the chapter says happened, runs every statement of the
// chapter and compares the answer, and holds every table to the engine.

func TestViewsAndSQL(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "views-and-sql.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The page is read with the line endings of every platform made one.
	page = bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	if _, err := db.DefineClass(ctx, notionClass(t, page), setup); err != nil {
		t.Fatal(err)
	}
	krzysztof, anna := person("krzysztof"), person("anna", "ACCOUNTING")
	ada := fado.As(fado.Principal{ID: "ada", Kind: fado.ActorPerson, System: []string{fado.GroupAnalysts}})

	// How a value looks in SQL: one variable for each row of the table,
	// in a class of the test, written as the chapter writes it.
	kinds := map[string]struct{ variable, definition string }{
		"text":     {"t", `{"type":"text"}`},
		"number":   {"n", `{"type":"number"}`},
		"int":      {"i", `{"type":"int"}`},
		"decimal":  {"vat_rate", `{"type":"decimal","scale":2}`},
		"money":    {"net", `{"type":"money","scale":2}`},
		"bool":     {"b", `{"type":"bool"}`},
		"date":     {"issued", `{"type":"date"}`},
		"datetime": {"seen_at", `{"type":"datetime"}`},
		"json":     {"j", `{"type":"json"}`},
		"ref":      {"r", `{"type":"ref","class":"Invoice"}`},
	}
	span := regexp.MustCompile("`([^`]*)`")
	var variables []string
	given := fado.Values{}
	type shown struct {
		kind, variable string
		stored         []string
		read           string
	}
	var looks []shown
	table := marked(t, page, "how a value looks in SQL")
	for i, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if i < 2 {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		kind := strings.Trim(strings.TrimSpace(cells[0]), "`")
		k, ok := kinds[kind]
		if !ok {
			t.Errorf("the chapter shows the type %s, and the test has no variable for it", kind)
			continue
		}
		delete(kinds, kind)
		variables = append(variables, `"`+k.variable+`":`+k.definition)
		var value any
		if err := json.Unmarshal([]byte(span.FindStringSubmatch(cells[1])[1]), &value); err != nil {
			t.Fatalf("the chapter writes a %s as %s: %v", kind, cells[1], err)
		}
		given[k.variable] = value
		look := shown{kind: kind, variable: k.variable}
		for _, m := range span.FindAllStringSubmatch(cells[2], -1) {
			look.stored = append(look.stored, m[1])
		}
		if m := span.FindStringSubmatch(cells[3]); m != nil {
			look.read = m[1]
		}
		looks = append(looks, look)
	}
	for kind := range kinds {
		t.Errorf("the test has a variable for the type %s, and the chapter does not show it", kind)
	}
	// Every class of the test is defined before the first statement
	// runs; the end of the test says why.
	other := `{"name":"Parcel","variables":{"tags":{"type":"array<text>"}},"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`
	for name, definition := range map[string]string{
		"Kinds":  scratch("Kinds", strings.Join(variables, ","), oneState),
		"Tagged": scratch("Tagged", `"tags":{"type":"array<text>"}`, oneState),
		"Holder": scratch("Holder", `"buyer":{"type":"object","schema":"Person"}`, oneState),
		"Parcel": other,
	} {
		if _, err := db.DefineClass(ctx, []byte(definition), setup); err != nil {
			t.Fatalf("the class %s of the test: %v", name, err)
		}
	}

	// What happened.
	if steps := rows(marked(t, page, "what happened")); len(steps) != 7 {
		t.Fatalf("the chapter tells %d steps, and the test plays seven", len(steps))
	}
	create := func(id string, values fado.Values) {
		t.Helper()
		values["issued"] = "2026-11-13"
		if _, err := db.Create(ctx, "Invoice", values, krzysztof, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	create("I-1", fado.Values{"number": "FV/1", "supplier": "ACME", "net": "1000.00 PLN",
		"lines": []any{map[string]any{"what": "taxi", "cost": "40.00 PLN"}, map[string]any{"what": "hotel", "cost": "300.00 PLN"}}})
	create("I-2", fado.Values{"number": "FV/2", "supplier": "Globex", "net": "500.00 EUR"})
	create("I-3", fado.Values{"number": "FV/3", "supplier": "Initech", "net": "70.00 PLN"})
	clock = clock.Add(2 * time.Hour)
	if _, err := db.Transition(ctx, "Invoice", "I-1", "approve", nil, krzysztof); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Set(ctx, "Invoice", "I-2", fado.Values{"net": "600.00 EUR"}, krzysztof); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	if _, err := db.Transition(ctx, "Invoice", "I-1", "book", nil, anna); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "Invoice", "I-3", krzysztof); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, "Kinds", given, krzysztof); err != nil {
		t.Fatal(err)
	}

	// ask runs a statement as the analyst and writes each value as a
	// table of the chapter does, a blank as an empty cell.
	ask := func(statement string, args ...any) ([]string, [][]string, error) {
		answer, err := db.SQL(ctx, statement, args, ada)
		if err != nil {
			return nil, nil, err
		}
		var out [][]string
		for _, row := range answer.Rows {
			var cells []string
			for _, value := range row {
				if value == nil {
					cells = append(cells, "")
				} else {
					cells = append(cells, fmt.Sprint(value))
				}
			}
			out = append(out, cells)
		}
		return answer.Columns, out, nil
	}
	columns := func(view string) []string {
		t.Helper()
		got, _, err := ask("select * from " + view + " limit 0")
		if err != nil {
			t.Fatalf("the view %s: %v", view, err)
		}
		return got
	}
	said := func(statement string, args ...any) string {
		if _, _, err := ask(statement, args...); err != nil {
			return fado.ErrorCode(err)
		}
		return "answered"
	}

	// The views of a class: each is there, and a name that is not a
	// view is not.
	for _, row := range rows(marked(t, page, "the views")) {
		if got := said("select * from " + row[0] + " limit 1"); got != "answered" {
			t.Errorf("the view %s is %s", row[0], got)
		}
	}
	if got := said("select * from Invoice_versions"); got != "bad_value" {
		t.Errorf("a view the class does not have is %s", got)
	}
	// A list has two views with the column value; a class without
	// states has no view of stays.
	if got := columns("Tagged_tags"); !reflect.DeepEqual(got, []string{"doc_id", "version", "idx", "value"}) || said("select * from Tagged_tags_history") != "answered" {
		t.Errorf("the views of a list have the columns %v", got)
	}
	if got := said("select id from Parcel_states"); got != "bad_value" {
		t.Errorf("the view of stays of a class without states is %s", got)
	}

	// The columns.
	split := func(marker string) []string {
		var names []string
		for _, row := range rows(marked(t, page, marker)) {
			names = append(names, strings.Split(row[0], ", ")...)
		}
		return names
	}
	ofDocument := columns("Invoice")
	engine := split("the columns of a document")
	if !reflect.DeepEqual(ofDocument[:len(engine)], engine) {
		t.Errorf("a document has the columns %v first, and the chapter lists %v", ofDocument[:len(engine)], engine)
	}
	if got := ofDocument[len(engine):]; !reflect.DeepEqual(got, []string{"number", "supplier", "net", "net_currency", "vat_rate", "gross", "gross_currency", "issued"}) {
		t.Errorf("after them a document has the columns %v", got)
	}
	ofVersion := columns("Invoice_history")
	if more := split("the columns of a version"); !reflect.DeepEqual(ofVersion[len(engine):len(engine)+len(more)], more) || len(ofVersion) != len(ofDocument)+len(more) {
		t.Errorf("a version has the columns %v, and the chapter adds %v", ofVersion, more)
	}
	sorted := func(names []string) []string {
		out := append([]string{}, names...)
		sort.Strings(out)
		return out
	}
	if got, want := sorted(columns("Invoice_states")), sorted(split("the columns of a stay")); !reflect.DeepEqual(got, want) {
		t.Errorf("a stay has the columns %v, and the chapter lists %v", got, want)
	}
	if got := columns("Invoice_lines"); !reflect.DeepEqual(got, []string{"doc_id", "version", "idx", "what", "cost", "cost_currency"}) {
		t.Errorf("a row of lines has the columns %v", got)
	}
	if got := columns("Invoice_events"); got[len(got)-1] != "qualifier" {
		t.Errorf("an event has the columns %v", got)
	}
	if _, got, err := ask("select distinct qualifier from Invoice_events order by qualifier"); err != nil || fmt.Sprint(got) != "[[changed] [created] [deleted] [transitioned]]" {
		t.Errorf("the qualifiers of the events: %v, %v", got, err)
	}
	if _, got, err := ask("select business_date from Invoice_events limit 1"); err != nil || got[0][0] != "20773" {
		t.Errorf("the business day of an event in SQL: %v, %v", got, err)
	}

	// How a value looks in SQL: the variables were made at the start.
	readable := map[string]string{"decimal": "0.23", "money": "1000", "date": "2026-11-13", "datetime": "2026-11-14 10:00:00"}
	for _, look := range looks {
		_, got, err := ask("select " + look.variable + " from Kinds")
		if err != nil || len(got) != 1 || got[0][0] != look.stored[0] {
			t.Errorf("a %s is %v in SQL, %v; the chapter says %s", look.kind, got, err, look.stored[0])
		}
		if len(look.stored) == 3 {
			// Money: the amount, the currency, and the column it is in.
			_, got, err := ask("select " + look.stored[2] + " from Kinds")
			if err != nil || len(got) != 1 || got[0][0] != look.stored[1] {
				t.Errorf("the currency of a %s is %v in the column %s, %v; the chapter says %s", look.kind, got, look.stored[2], err, look.stored[1])
			}
		}
		if (look.read != "") != (readable[look.kind] != "") {
			t.Errorf("the chapter says a %s is read with %q", look.kind, look.read)
		} else if look.read != "" {
			_, got, err := ask("select " + look.read + " from Kinds")
			if err != nil || len(got) != 1 || got[0][0] != readable[look.kind] {
				t.Errorf("%s gives %v, %v; a %s reads as %s", look.read, got, err, look.kind, readable[look.kind])
			}
		}
	}
	// A blank is NULL, and an object one column for each field.
	if _, got, err := ask("select count(*) from Invoice where issued is not null and state_entered_from is null"); err != nil || got[0][0] != "1" {
		t.Errorf("the invoices in their first state, by a blank: %v, %v", got, err)
	}
	if got := said("select buyer__name from Holder"); got != "answered" {
		t.Errorf("the column of a field of an object is %s", got)
	}

	// The questions: each statement of the chapter, with the values for
	// its marks, and the answer under it.
	questions := regexp.MustCompile("<!-- question(?:: ([^>]*?))? -->\n```sql\n((?s:.*?))\n```\n\n<!-- answer -->\n((?:\\|.*\n)+)").FindAllSubmatch(page, -1)
	if len(questions) != 7 {
		t.Fatalf("the chapter asks %d questions with their answers; the test knows seven", len(questions))
	}
	for i, q := range questions {
		var args []any
		for _, value := range strings.Split(string(q[1]), ",") {
			if value = strings.TrimSpace(value); value != "" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					t.Fatalf("question %d is given the value %q", i+1, value)
				}
				args = append(args, n)
			}
		}
		head, got, err := ask(string(q[2]), args...)
		if err != nil {
			t.Errorf("question %d is refused: %v", i+1, err)
			continue
		}
		answer := string(q[3])
		if !reflect.DeepEqual(head, heading(answer)) {
			t.Errorf("question %d answers the columns %v, and the chapter shows %v", i+1, head, heading(answer))
		}
		if want := rows(answer); !reflect.DeepEqual(got, want) {
			t.Errorf("question %d answers %v, and the chapter shows %v", i+1, got, want)
		}
	}

	// What a statement may do.
	checks(t, page, "what a statement may do", map[string]func(row []string){
		"reads the views, with joins, groups and with": func(row []string) {
			if got := said("with open as (select id from Invoice where state <> 'BOOKED') select count(o.id) from open o join Invoice_history h on h.id = o.id group by o.id"); got != row[1] {
				t.Errorf("a statement with a join, a group and with is %s", got)
			}
		},
		"takes values of the caller as ?": func(row []string) {
			if _, got, err := ask("select id from Invoice where state = ? and version >= ?", "BOOKED", 1); err != nil || fmt.Sprint(got) != "[[I-1]]" || row[1] != "answered" {
				t.Errorf("a statement with values of the caller: %v, %v", got, err)
			}
		},
		"uses the functions of SQLite, such as date and datetime": func(row []string) {
			if got := said("select date(issued * 86400, 'unixepoch'), datetime(created_at, 'unixepoch'), upper(number), length(supplier) from Invoice"); got != row[1] {
				t.Errorf("a statement with functions of SQLite is %s", got)
			}
		},
		"reads a table behind a view, such as doc_Invoice": func(row []string) {
			for _, statement := range []string{"select * from doc_Invoice", "select id from doc_Invoice_history"} {
				if got := said(statement); got != row[1] {
					t.Errorf("%s is %s, and the chapter says %s", statement, got, row[1])
				}
			}
		},
		"reads a table of the engine, such as events": func(row []string) {
			for _, statement := range []string{"select * from events", "select count(*) from events", "select * from outbox", "select * from metadata"} {
				if got := said(statement); got != row[1] {
					t.Errorf("%s is %s, and the chapter says %s", statement, got, row[1])
				}
			}
		},
		"asks how the database is laid out, with sqlite_master or a pragma": func(row []string) {
			for _, statement := range []string{"select name from sqlite_master", "pragma table_info(Invoice)", "attach database 'other.db' as other"} {
				if got := said(statement); got != row[1] {
					t.Errorf("%s is %s, and the chapter says %s", statement, got, row[1])
				}
			}
		},
		"inserts a row": func(row []string) {
			if got := said("insert into Invoice (id) values ('I-9')"); got != row[1] {
				t.Errorf("an insert is %s, and the chapter says %s", got, row[1])
			}
		},
		"changes or deletes rows": func(row []string) {
			for _, statement := range []string{"update Invoice set state = 'BOOKED'", "delete from Invoice"} {
				if got := said(statement); got != row[1] {
					t.Errorf("%s is %s, and the chapter says %s", statement, got, row[1])
				}
			}
		},
		"answers more than 10,000 rows": func(row []string) {
			numbers := "with recursive n(i) as (select 1 union all select i + 1 from n where i < %d) select i from n"
			if got := said(fmt.Sprintf(numbers, 10001)); got != row[1] {
				t.Errorf("a statement that answers 10,001 rows is %s, and the chapter says %s", got, row[1])
			}
			if got := said(fmt.Sprintf(numbers, 10000)); got != "answered" {
				t.Errorf("a statement that answers 10,000 rows is %s", got)
			}
		},
		"is not SQL, or names a view the database does not have": func(row []string) {
			for _, statement := range []string{"selec 1", "select * from Nowhere"} {
				if got := said(statement); got != row[1] {
					t.Errorf("%s is %s, and the chapter says %s", statement, got, row[1])
				}
			}
		},
	})
	if _, got, err := ask("select count(*) from Invoice"); err != nil || got[0][0] != "2" {
		t.Errorf("after the statements that were refused the invoices are %v, %v", got, err)
	}

	// count(*) over the stays is refused. When the engine answers it,
	// this fails and the box in the chapter goes.
	for _, statement := range []string{"select count(*) from Invoice_states", "select 1 from Invoice_states"} {
		if got := said(statement); got != "not_allowed" {
			t.Errorf("%s is %s; the chapter says it is refused, a fault not yet mended", statement, got)
		}
	}
	if _, got, err := ask("select count(id) from Invoice_states"); err != nil || got[0][0] != "5" {
		t.Errorf("the stays counted by a column: %v, %v", got, err)
	}
	for _, view := range []string{"Invoice", "Invoice_history", "Invoice_events", "Invoice_actions", "Invoice_lines", "Invoice_lines_history"} {
		if got := said("select count(*) from " + view); got != "answered" {
			t.Errorf("count(*) over %s is %s", view, got)
		}
	}
	// The last stay of a deleted document has no end. When the engine
	// ends it, this fails and the box in the chapter goes.
	if _, got, err := ask("select state, left_at from Invoice_states where id = 'I-3'"); err != nil || fmt.Sprint(got) != "[[INAPPROVAL ]]" {
		t.Errorf("the stays of the deleted invoice: %v, %v", got, err)
	}
	// A deleted document: not among the documents, and still in the
	// versions, with the moment of the delete as the end of the last.
	if _, got, err := ask("select count(*) from Invoice where id = 'I-3'"); err != nil || got[0][0] != "0" {
		t.Errorf("the deleted invoice among the documents: %v, %v", got, err)
	}
	if _, got, err := ask("select version, valid_to from Invoice_history where id = 'I-3'"); err != nil || fmt.Sprint(got) != fmt.Sprintf("[[1 %d]]", clock.Unix()) {
		t.Errorf("the versions of the deleted invoice: %v, %v", got, err)
	}

	// Who may.
	for who, as := range map[string][]fado.Option{"Krzysztof": {krzysztof}, "Anna": {anna}, "a designer": {fado.As(fado.Principal{ID: "d", System: []string{fado.GroupDesigners}})}, "nobody": nil} {
		_, err := db.SQL(ctx, "select count(*) from Invoice", nil, as...)
		want(t, who+" runs a statement", err, "forbidden at /system/analysts")
	}
	answered, err := db.Exec(ctx, fado.Principal{ID: "ada", Kind: fado.ActorPerson, System: []string{fado.GroupAnalysts}}, "sql",
		[]byte(`{"statement": "select id, state from Invoice where state = ? order by id", "args": ["BOOKED"]}`))
	if err != nil || string(answered) != `{"columns":["id","state"],"rows":[["I-1","BOOKED"]]}` {
		t.Errorf("the command sql: %s, %v", answered, err)
	}
	// A class or a table defined after the first statement of this
	// program is not read by SQL until the database is opened again.
	// When the engine learns of it at once, this fails and the box in
	// the chapter goes.
	if _, err := db.DefineClass(ctx, []byte(scratch("Late", `"t":{"type":"text"}`, oneState)), setup); err != nil {
		t.Fatal(err)
	}
	if got := said("select id from Late"); got != "not_allowed" {
		t.Errorf("a class defined after the first statement is %s; the chapter says it is refused until the database is opened again", got)
	}
	if got := said("select id from Invoice"); got != "answered" {
		t.Errorf("a class defined before the first statement is %s", got)
	}

	// An analyst reads what the class shows to no caller but its
	// creator and accounting.
	if _, got, err := ask("select supplier from Invoice order by id"); err != nil || fmt.Sprint(got) != "[[ACME] [Globex]]" {
		t.Errorf("an analyst reads every document: %v, %v", got, err)
	}
	if _, err := db.Get(ctx, "Invoice", "I-1", ada); fado.ErrorCode(err) != "unknown_document" {
		t.Errorf("an analyst reads a document by a call: %v", err)
	}
}
