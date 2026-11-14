package fado_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The operator's skill shows one piece of work command by command, with
// the engine's own answers, and a list of what goes wrong most often.
// TestOperatorSkill defines the class the skill shows, makes the
// situation it describes, sends each command as the agent of the skill
// and compares the answer with the one on the page; then it holds each
// point of the list.
func TestOperatorSkill(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "skills", "fadodb-operate", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	if _, err := db.DefineClass(ctx, notionClass(t, page), setup); err != nil {
		t.Fatal(err)
	}
	ola := fado.Principal{ID: "ola", Kind: fado.ActorPerson}
	agent := fado.Principal{ID: "claims-agent", Kind: fado.ActorAgent}
	// The situation: Ola filed three expenses at 09:00.
	for id, values := range map[string]string{
		"E-1": `{"approver":"claims-agent","what":"taxi to the airport","amount":"120.00 PLN"}`,
		"E-2": `{"approver":"claims-agent","what":"conference in Lisbon","amount":"8000.00 PLN"}`,
		"E-3": `{"approver":"marek","what":"lunch","amount":"60.00 PLN"}`,
	} {
		if _, err := db.Exec(ctx, ola, "create", []byte(`{"class":"Expense","id":"`+id+`","values":`+values+`}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, ola, "transition", []byte(`{"class":"Expense","id":"`+id+`","transition":"file"}`)); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(time.Hour)

	// same compares two answers as JSON, with every id the engine makes
	// taken for the same.
	made := regexp.MustCompile(`"[0-9A-HJKMNP-TV-Z]{26}"`)
	same := func(got, want []byte) bool {
		var g, w any
		if json.Unmarshal(made.ReplaceAll(got, []byte(`"an id"`)), &g) != nil || json.Unmarshal(made.ReplaceAll(want, []byte(`"an id"`)), &w) != nil {
			return false
		}
		return reflect.DeepEqual(g, w)
	}
	// The exchange: each command of the page, in its order, and the
	// answer under it.
	exchange := regexp.MustCompile("(?s)<!-- command: (\\w+) -->\n```jsonc\n(.*?)\n```\n\n<!-- answer -->\n```jsonc\n(.*?)\n```").FindAllSubmatch(page, -1)
	if len(exchange) != 6 {
		t.Fatalf("the skill shows %d commands with their answers; the test knows six", len(exchange))
	}
	var rejected []byte
	for _, m := range exchange {
		name, sent, shown := string(m[1]), m[2], m[3]
		got, _ := db.Exec(ctx, agent, name, sent)
		if !same(got, shown) {
			t.Errorf("the command %s %s is answered\n%s\nand the skill shows\n%s", name, sent, got, shown)
		}
		if name == "transition" {
			rejected = sent
			// Step 5: the same command again is answered as the first
			// time, and writes nothing.
			again, err := db.Exec(ctx, agent, name, sent)
			if err != nil || string(again) != string(got) {
				t.Errorf("the same command under the same key is answered\n%s, %v", again, err)
			}
			if history, err := db.History(ctx, "Expense", "E-2", fado.As(agent)); err != nil || len(history) != 3 {
				t.Errorf("after the command was sent twice the expense has %d versions, %v", len(history), err)
			}
		}
	}
	if rejected == nil {
		t.Fatal("the skill shows no transition")
	}

	// What goes wrong most often: the list has eleven points, and each
	// is held here by its number.
	points := regexp.MustCompile(`(?m)^(\d+)\. \*\*`).FindAllSubmatch(page[strings.Index(string(page), "## What goes wrong most often"):strings.Index(string(page), "## The hat")], -1)
	if len(points) != 11 {
		t.Errorf("the list has %d points, and the test holds eleven", len(points))
	}
	code := func(name, object string) string {
		t.Helper()
		_, err := db.Exec(ctx, agent, name, []byte(object))
		return fado.ErrorCode(err)
	}
	// 1. A refusal sent again is refused again.
	for i := 0; i < 2; i++ {
		if got := code("transition", `{"class":"Expense","id":"E-2","transition":"approve"}`); got != "wrong_state" {
			t.Errorf("1: a refusal sent again: %s", got)
		}
	}
	// 2. A change with no key, sent twice, is made twice or refused the
	// second time; with a key it is made once (step 5 above).
	if got := code("transition", `{"class":"Expense","id":"E-1","transition":"approve"}`); got != "" {
		t.Errorf("2: an approval with no key: %s", got)
	}
	if got := code("transition", `{"class":"Expense","id":"E-1","transition":"approve"}`); got != "wrong_state" {
		t.Errorf("2: the same approval with no key, sent again: %s", got)
	}
	// 3. One key for two commands.
	if got := code("transition", `{"class":"Expense","id":"E-1","transition":"approve","key":"reject-E-2"}`); got != "key_reused" {
		t.Errorf("3: a different command under a key that was used: %s", got)
	}
	// 4. enabled: false is not final: the exchange shows reject blocked
	// in step 2 and taken in step 4.
	if !strings.Contains(string(exchange[1][3]), `"name": "reject", "to": "rejected", "enabled": false`) {
		t.Error("4: the skill no longer shows reject as blocked before its values are given")
	}
	// 5. A document that is hidden and one that does not exist are
	// answered alike.
	hidden, _ := db.Exec(ctx, agent, "get", []byte(`{"class":"Expense","id":"E-3"}`))
	never, _ := db.Exec(ctx, agent, "get", []byte(`{"class":"Expense","id":"E-9"}`))
	if strings.ReplaceAll(string(never), "E-9", "E-3") != string(hidden) {
		t.Errorf("5: a hidden document is answered %s, and one that does not exist %s", hidden, never)
	}
	// 6. A filter leaves out what the caller may not see, and USER() is
	// the caller.
	all, err := db.Exec(ctx, agent, "query", []byte(`{"class":"Expense","fields":["what"]}`))
	if err != nil || strings.Contains(string(all), "lunch") || !strings.Contains(string(all), "taxi") {
		t.Errorf("6: a query by the agent: %s, %v", all, err)
	}
	mine, err := db.Exec(ctx, agent, "count", []byte(`{"class":"Expense","where":"approver = USER()"}`))
	if err != nil || string(mine) != `{"count":2}` {
		t.Errorf("6: the expenses whose approver is the caller: %s, %v", mine, err)
	}
	// 7. A finished document keeps its values.
	if got := code("set", `{"class":"Expense","id":"E-1","values":{"note":"paid"}}`); got != "terminal" {
		t.Errorf("7: a set on a finished document: %s", got)
	}
	// 8. A batch answers for each id, and gives no error when a
	// document does not move.
	for _, id := range []string{"E-4", "E-5"} {
		amount := "100.00 PLN"
		if id == "E-5" {
			amount = "9000.00 PLN"
		}
		if _, err := db.Exec(ctx, ola, "create", []byte(`{"class":"Expense","id":"`+id+`","values":{"approver":"claims-agent","what":"hotel","amount":"`+amount+`"}}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, ola, "transition", []byte(`{"class":"Expense","id":"`+id+`","transition":"file"}`)); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := db.Exec(ctx, agent, "transition_many", []byte(`{"class":"Expense","transition":"approve","ids":["E-4","E-5"],"key":"approve-batch-1"}`))
	if err != nil || !strings.Contains(string(batch), `"guard_false"`) || !strings.Contains(string(batch), `"approved"`) {
		t.Errorf("8: a batch of one that moves and one that does not: %s, %v", batch, err)
	}
	// 9. A command does not say who sends it; it may say for whom, and
	// that needs a substitution.
	if got := code("get", `{"class":"Expense","id":"E-1","as":"ola"}`); got != "bad_json" {
		t.Errorf("9: a command that says who sends it: %s", got)
	}
	if got := code("get", `{"class":"Expense","id":"E-1","for":"ola"}`); got != "forbidden" {
		t.Errorf("9: a command for somebody, with no substitution: %s", got)
	}
	// 10. A member the command does not take, and the hint that lists
	// the members it takes.
	guessed, err := db.Exec(ctx, agent, "transition", []byte(`{"class":"Expense","id":"E-1","move":"approve"}`))
	if fado.ErrorCode(err) != "bad_json" || !strings.Contains(string(guessed), `transition takes class, id, transition`) {
		t.Errorf("10: a member the command does not take: %s", guessed)
	}
	// 11. A guard that reads the clock does not move a document; due
	// lists it and refresh touches it.
	late := `{"name":"Loan","variables":{"due":{"type":"date"}},"states":{"initial":"out","list":["out","overdue"]},` +
		`"transitions":[{"from":"out","to":"overdue","guard":"TODAY() > due"}],` +
		`"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`
	if _, err := db.DefineClass(ctx, []byte(late), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, agent, "create", []byte(`{"class":"Loan","id":"L-1","values":{"due":"2026-11-20"}}`)); err != nil {
		t.Fatal(err)
	}
	clock = clock.AddDate(0, 0, 10)
	still, _ := db.Exec(ctx, agent, "get", []byte(`{"class":"Loan","id":"L-1"}`))
	due, err := db.Exec(ctx, agent, "due", []byte(`{"class":"Loan"}`))
	if !strings.Contains(string(still), `"state":"out"`) || err != nil || !strings.Contains(string(due), `"L-1"`) {
		t.Errorf("11: a loan whose date has passed: %s; due answers %s, %v", still, due, err)
	}
	moved, err := db.Exec(ctx, agent, "refresh", []byte(`{"class":"Loan","id":"L-1"}`))
	if err != nil || !strings.Contains(string(moved), `"state":"overdue"`) {
		t.Errorf("11: a refresh of a loan that is due: %s, %v", moved, err)
	}
}
