package fado_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on the log of reads (ADR-0067): TestTheLogOfReads takes
// the class out of the chapter, plays its story step by step, and holds
// the log Tomek reads, the command and its answer, and the table of
// refusals to what the engine answers.
func TestTheLogOfReads(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "the-log-of-reads.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := notionDB(t, fado.Options{Now: func() time.Time { return time.Unix(1794992400, 0).UTC() }})
	if _, err := db.DefineClass(ctx, notionClass(t, page), setup); err != nil {
		t.Fatal(err)
	}
	system := func(id, group string) fado.Option {
		return fado.As(fado.Principal{ID: id, Kind: fado.ActorPerson, System: []string{group}})
	}
	hana, ewa := person("hana", "hr"), person("ewa")
	olga, marek, tomek := system("olga", fado.GroupContentAdministrators), system("marek", fado.GroupAnalysts), system("tomek", fado.GroupAdministrators)
	correct, yearly := fado.Reason("Jan asked HR to correct his manager"), fado.Reason("the yearly check")

	// The story.
	steps := map[string]string{}
	for _, row := range rows(marked(t, page, "what it does")) {
		steps[row[0]] = row[3]
	}
	step := func(n int, got string) {
		t.Helper()
		if w := steps[fmt.Sprint(n)]; w != got {
			t.Errorf("step %d: the engine answers %q, and the chapter says %q", n, got, w)
		}
		delete(steps, fmt.Sprint(n))
	}
	refused := func(err error) string {
		if got := answer(err); got != "allowed" {
			return "refused, " + got
		}
		return "allowed"
	}
	var errs []error
	for _, review := range []struct{ id, employee string }{{"SR-017", "jan"}, {"SR-018", "ann"}} {
		_, err := db.Create(ctx, "SalaryReview", fado.Values{"employee": review.employee, "manager": "ewa", "salary": "21000.00 PLN"}, hana, fado.WithID(review.id))
		errs = append(errs, err)
	}
	step(1, refused(firstOf(errs)))
	_, err = db.Get(ctx, "SalaryReview", "SR-017", ewa)
	step(2, refused(err))
	_, err = db.Get(ctx, "SalaryReview", "SR-017", olga)
	step(3, refused(err))
	docs, err := db.Query(ctx, fado.Filter{Class: "SalaryReview"}, olga)
	if err != nil {
		t.Fatal(err)
	}
	step(4, fmt.Sprintf("%d documents", len(docs)))
	_, err = db.Get(ctx, "SalaryReview", "SR-017", olga, correct)
	step(5, refused(err))
	docs, err = db.Query(ctx, fado.Filter{Class: "SalaryReview"}, olga, yearly)
	if err != nil {
		t.Fatal(err)
	}
	step(6, fmt.Sprintf("%d documents", len(docs)))
	n, err := db.Count(ctx, fado.Filter{Class: "SalaryReview"}, olga, yearly)
	if err != nil {
		t.Fatal(err)
	}
	step(7, fmt.Sprint(n))
	out, err := db.SQL(ctx, "SELECT employee, salary FROM SalaryReview WHERE salary > ?", []any{20000}, marek)
	if err != nil {
		t.Fatal(err)
	}
	step(8, fmt.Sprintf("%d rows", len(out.Rows)))
	_, err = db.Reads(ctx, fado.ReadsFilter{}, olga)
	step(9, refused(err))
	log, err := db.Reads(ctx, fado.ReadsFilter{}, tomek)
	if err != nil {
		t.Fatal(err)
	}
	step(10, map[bool]string{true: "the four entries below"}[len(log) == 4])
	for n := range steps {
		t.Errorf("step %s of the chapter is not played", n)
	}

	// What Tomek reads.
	var got [][]string
	for _, r := range log {
		more := ""
		switch {
		case r.Counted != nil:
			more = fmt.Sprintf("counted %d", *r.Counted)
		case r.Rows != nil:
			more = fmt.Sprintf("rows %d", *r.Rows)
		}
		got = append(got, []string{fmt.Sprint(r.Seq), r.By, r.Command, strings.Join(r.Documents, " "), strings.Join(r.Rules, " "), more, r.Reason})
	}
	if want := rows(marked(t, page, "the log")); !reflect.DeepEqual(got, want) {
		t.Errorf("the log is\n%q\nand the chapter says\n%q", got, want)
	}

	// The command and its answer, as Tomek sends it.
	tomekAs := fado.Principal{ID: "tomek", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}}
	answered, err := db.Exec(ctx, tomekAs, "reads", []byte(fenced(t, page, "who saw SR-017")))
	if err != nil {
		t.Fatal(err)
	}
	var have, want any
	if err := json.Unmarshal(answered, &have); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fenced(t, page, "the answer")), &want); err != nil {
		t.Fatalf("the answer the chapter shows: %v", err)
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("reads answers\n%s\nand the chapter shows\n%s", answered, fenced(t, page, "the answer"))
	}

	// What is refused.
	if _, err := db.DefineTable(ctx, []byte(`{"name": "Grades", "columns": {"employee": "text", "grade": "number"}, "key": ["employee"], "access": {"read": "INGROUP(\"hr\")", "write": "INGROUP(\"hr\")"}}`), setup); err != nil {
		t.Fatal(err)
	}
	checks(t, page, "what is refused", map[string]func(row []string){
		"reads one document past its rule as a content administrator, and gives no reason": func(row []string) {
			_, err := db.Get(ctx, "SalaryReview", "SR-018", olga)
			wantRefusal(t, row, err)
		},
		"imports into a table past its write rule as a content administrator, and gives no reason": func(row []string) {
			_, err := db.ImportRows(ctx, "Grades", []fado.Values{{"employee": "jan", "grade": 3}}, olga)
			wantRefusal(t, row, err)
		},
		"opens bytes by their hash alone as a content administrator, and gives no reason": func(row []string) {
			_, _, err := db.OpenBlob(ctx, strings.Repeat("0", 64), olga)
			wantRefusal(t, row, err)
		},
		"reads the log, and is no database administrator": func(row []string) {
			_, err := db.Reads(ctx, fado.ReadsFilter{}, marek)
			wantRefusal(t, row, err)
		},
		"names a document of the log without its class": func(row []string) {
			_, err := db.Reads(ctx, fado.ReadsFilter{ID: "SR-017"}, tomek)
			wantRefusal(t, row, err)
		},
		"asks for more than 10,000 entries at once": func(row []string) {
			_, err := db.Reads(ctx, fado.ReadsFilter{Limit: 10_001}, tomek)
			wantRefusal(t, row, err)
		},
	})
}

// firstOf is the first error of a list, nil when there is none.
func firstOf(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// wantRefusal holds a refusal to a row of a table of refusals: the
// code, and the place when the row names one.
func wantRefusal(t *testing.T, row []string, err error) {
	t.Helper()
	got := answer(err)
	want := row[1]
	if row[2] != "" {
		want += " at " + row[2]
	}
	if got != want {
		t.Errorf("%s: answered %q, and the chapter says %q", row[0], got, want)
	}
}
