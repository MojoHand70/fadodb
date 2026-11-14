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
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// A written skill shows one piece of work as commands, each with the
// engine's own answer under it. These helpers read a skill and play its
// commands.

// skill reads a skill, with the line endings of every platform made
// one.
func skill(t *testing.T, name string) []byte {
	t.Helper()
	page, err := os.ReadFile(filepath.Join("..", "skills", name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
}

// exchange is one command of a skill, with the answer the skill shows
// under it.
type exchange struct {
	name        string
	sent, shown []byte
}

// exchangesOf returns the commands of a skill in the order of the page.
func exchangesOf(page []byte) []exchange {
	var out []exchange
	for _, m := range regexp.MustCompile("(?s)<!-- command: (\\w+) -->\n```jsonc\n(.*?)\n```\n\n<!-- answer -->\n```jsonc\n(.*?)\n```").FindAllSubmatch(page, -1) {
		out = append(out, exchange{name: string(m[1]), sent: m[2], shown: m[3]})
	}
	return out
}

// sameAnswer compares two answers as JSON, with every id the engine
// makes taken for the same.
func sameAnswer(got, want []byte) bool {
	made := regexp.MustCompile(`"[0-9A-HJKMNP-TV-Z]{26}"`)
	var g, w any
	if json.Unmarshal(made.ReplaceAll(got, []byte(`"an id"`)), &g) != nil || json.Unmarshal(made.ReplaceAll(want, []byte(`"an id"`)), &w) != nil {
		return false
	}
	return reflect.DeepEqual(g, w)
}

// points returns how many numbered points the list of a skill under
// "What goes wrong most often" has.
func points(t *testing.T, page []byte) int {
	t.Helper()
	text := string(page)
	from, to := strings.Index(text, "## What goes wrong most often"), strings.Index(text, "## The hat")
	if from < 0 || to < from {
		t.Fatal("the skill has no list of what goes wrong most often before its hat")
	}
	return len(regexp.MustCompile(`(?m)^\d+\. \*\*`).FindAllString(text[from:to], -1))
}

// The operator's skill shows one piece of work command by command, with
// the engine's own answers, and a list of what goes wrong most often.
// TestOperatorSkill defines the class the skill shows, makes the
// situation it describes, sends each command as the agent of the skill
// and compares the answer with the one on the page; then it holds each
// point of the list.
func TestOperatorSkill(t *testing.T) {
	page := skill(t, "fadodb-operate")
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

	// The exchange: each command of the page, in its order, and the
	// answer under it.
	exchange := exchangesOf(page)
	if len(exchange) != 6 {
		t.Fatalf("the skill shows %d commands with their answers; the test knows six", len(exchange))
	}
	var rejected []byte
	for _, m := range exchange {
		name, sent, shown := m.name, m.sent, m.shown
		got, _ := db.Exec(ctx, agent, name, sent)
		if !sameAnswer(got, shown) {
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
	if n := points(t, page); n != 11 {
		t.Errorf("the list has %d points, and the test holds eleven", n)
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
	if !strings.Contains(string(exchange[1].shown), `"name": "reject", "to": "rejected", "enabled": false`) {
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

// The analyst's skill shows a process and seven statements over it, with
// the engine's own answers. TestAnalystSkill defines the class the skill
// shows, plays what the skill says happened under a clock of its own,
// runs each statement as an analyst and compares the answer with the
// page; then it holds each point of the list.
func TestAnalystSkill(t *testing.T) {
	page := skill(t, "fadodb-mine")
	ctx := context.Background()
	clock := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	// Every class of the test is defined before the first statement
	// runs: point 9 of the list says why.
	for _, class := range []string{
		string(notionClass(t, page)),
		scratch("Gone", `"t":{"type":"text"}`, oneState),
		`{"name":"Private","variables":{"t":{"type":"text"}},"states":{"initial":"a","list":["a"]},` +
			`"access":{"read":"CREATEDBY() = USER()","create":"TRUE","update":"FALSE","delete":"FALSE"}}`,
	} {
		if _, err := db.DefineClass(ctx, []byte(class), setup); err != nil {
			t.Fatal(err)
		}
	}
	who := map[string]fado.Principal{
		"ola": {ID: "ola", Kind: fado.ActorPerson}, "piotr": {ID: "piotr", Kind: fado.ActorPerson},
		"marek": {ID: "marek", Kind: fado.ActorPerson}, "iza": {ID: "iza", Kind: fado.ActorPerson},
		"intake-agent": {ID: "intake-agent", Kind: fado.ActorAgent},
	}
	analyst := fado.Principal{ID: "ada", Kind: fado.ActorAgent, System: []string{fado.GroupAnalysts}}
	at := func(day, hour int) { clock = time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }
	do := func(as, name, object string) {
		t.Helper()
		if _, err := db.Exec(ctx, who[as], name, []byte(object)); err != nil {
			t.Fatalf("%s: %s %s: %v", as, name, object, err)
		}
	}
	file := func(as, id, what, amount, approver string) {
		t.Helper()
		do(as, "create", `{"class":"Request","id":"`+id+`","values":{"what":"`+what+`","amount":"`+amount+`","approver":"`+approver+`"}}`)
		do(as, "transition", `{"class":"Request","id":"`+id+`","transition":"file"}`)
	}
	move := func(as, id, transition, note string) {
		t.Helper()
		values := ""
		if note != "" {
			values = `,"values":{"note":"` + note + `"}`
		}
		do(as, "transition", `{"class":"Request","id":"`+id+`","transition":"`+transition+`"`+values+`}`)
	}

	// What happened: the thirteen steps of the page.
	if steps := rows(marked(t, page, "what happened")); len(steps) != 13 {
		t.Fatalf("the skill tells %d steps, and the test plays thirteen", len(steps))
	}
	at(2, 9)
	file("ola", "R-1", "a laptop", "6000.00 PLN", "marek")
	file("ola", "R-2", "a monitor", "1500.00 PLN", "iza")
	at(2, 10)
	file("piotr", "R-3", "a trip to Lisbon", "8000.00 PLN", "iza")
	at(2, 13)
	move("marek", "R-1", "approve", "")
	at(3, 9)
	move("iza", "R-2", "return", "which monitor?")
	file("intake-agent", "R-4", "paper", "200.00 PLN", "marek")
	at(3, 10)
	do("ola", "set", `{"class":"Request","id":"R-2","values":{"what":"a 27 inch monitor"}}`)
	move("ola", "R-2", "file", "")
	at(3, 11)
	move("marek", "R-4", "approve", "")
	at(4, 9)
	file("piotr", "R-5", "a chair", "900.00 PLN", "iza")
	at(4, 10)
	move("iza", "R-3", "reject", "no budget this year")
	at(5, 9)
	file("ola", "R-6", "a desk", "1200.00 PLN", "marek")
	at(5, 10)
	move("iza", "R-2", "approve", "")
	// Monday 9 November, 09:00: the engine takes expire of R-5.
	at(9, 9)
	if _, err := db.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	// For the points of the list: a document that is deleted, and one
	// that only its creator may see.
	if _, err := db.Exec(ctx, who["ola"], "create", []byte(`{"class":"Gone","id":"G-1","values":{"t":"x"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, who["ola"], "delete", []byte(`{"class":"Gone","id":"G-1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, who["ola"], "create", []byte(`{"class":"Private","id":"P-1","values":{"t":"for Ola only"}}`)); err != nil {
		t.Fatal(err)
	}
	at(9, 12)

	// The statements: each command of the page, in its order, and the
	// answer under it.
	statements := exchangesOf(page)
	if len(statements) != 7 {
		t.Fatalf("the skill shows %d statements with their answers; the test knows seven", len(statements))
	}
	for i, m := range statements {
		if m.name != "sql" {
			t.Errorf("question %d is the command %s", i+1, m.name)
		}
		got, err := db.Exec(ctx, analyst, m.name, m.sent)
		if err != nil || !sameAnswer(got, m.shown) {
			t.Errorf("question %d, %s, is answered\n%s, %v\nand the skill shows\n%s", i+1, m.sent, got, err, m.shown)
		}
	}

	// What goes wrong most often: eleven points, each held by its
	// number.
	if n := points(t, page); n != 11 {
		t.Errorf("the list has %d points, and the test holds eleven", n)
	}
	sql := func(statement string, args ...any) string {
		t.Helper()
		object, _ := json.Marshal(map[string]any{"statement": statement, "args": args})
		got, err := db.Exec(ctx, analyst, "sql", object)
		if err != nil {
			return fado.ErrorCode(err)
		}
		var answer struct{ Rows [][]any }
		if err := json.Unmarshal(got, &answer); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(answer.Rows)
	}
	// 1. An amount as it is stored.
	if got := sql("select amount, amount_currency, amount / 100.0 from Request where id = 'R-1'"); got != "[[600000 PLN 6000]]" {
		t.Errorf("1: the amount of R-1 in SQL: %s", got)
	}
	// 2. A day and a moment as numbers.
	if got := sql("select business_date, date(business_date * 86400, 'unixepoch'), datetime(ts, 'unixepoch') from Request_events order by ts limit 1"); got != "[[20759 2026-11-02 2026-11-02 09:00:00]]" {
		t.Errorf("2: the day of the first event: %s", got)
	}
	// 3. The last stay of a deleted document has no end, and the
	// document is not among the documents.
	if got := sql("select left_at from Gone_states where id = 'G-1'"); got != "[[<nil>]]" {
		t.Errorf("3: the stay of a deleted document: %s", got)
	}
	if got := sql("select count(*) from Gone"); got != "[[0]]" {
		t.Errorf("3: the deleted document among the documents: %s", got)
	}
	// 4. An average leaves out the stays that have not ended.
	if got := sql("select count(id), count(duration) from Request_states where state = 'filed'"); got != "[[7 6]]" {
		t.Errorf("4: the stays in filed, and those with a length: %s", got)
	}
	// 5. count(*) over the stays.
	if got := sql("select count(*) from Request_states"); got != "not_allowed" {
		t.Errorf("5: count(*) over the stays: %s", got)
	}
	if got := sql("select count(id) from Request_states"); got != "[[19]]" {
		t.Errorf("5: the stays counted by a column: %s", got)
	}
	// 6. A refusal writes nothing.
	before := sql("select count(event_id) from Request_events")
	if _, err := db.Exec(ctx, who["ola"], "transition", []byte(`{"class":"Request","id":"R-6","transition":"approve"}`)); fado.ErrorCode(err) != "forbidden" {
		t.Errorf("6: Ola approves her own request: %v", err)
	}
	if after := sql("select count(event_id) from Request_events"); after != before {
		t.Errorf("6: a refusal wrote an event: %s before, %s after", before, after)
	}
	// 7. The versions are more than the documents.
	if got := sql("select (select count(*) from Request), (select count(*) from Request_history)"); got != "[[6 20]]" {
		t.Errorf("7: the documents and their versions: %s", got)
	}
	// 8. More than 10,000 rows.
	if got := sql("with recursive n(i) as (select 1 union all select i + 1 from n where i < 10001) select i from n"); got != "too_many_rows" {
		t.Errorf("8: a statement that answers 10,001 rows: %s", got)
	}
	// 10. The steps of an agent are those of the caller the program
	// called an agent.
	if got := sql("select distinct actor_id from Request_events where actor_kind = 'agent'"); got != "[[intake-agent]]" {
		t.Errorf("10: the callers that were agents: %s", got)
	}
	// 11. An analyst reads what a class shows to its creator alone, and
	// only through SQL.
	if got := sql("select t from Private"); got != "[[for Ola only]]" {
		t.Errorf("11: an analyst reads a document of another: %s", got)
	}
	if _, err := db.Exec(ctx, analyst, "get", []byte(`{"class":"Private","id":"P-1"}`)); fado.ErrorCode(err) != "unknown_document" {
		t.Errorf("11: an analyst reads a document of another by a command: %v", err)
	}
	// 9. A class defined after the first statement. It is held last,
	// since it defines one.
	if _, err := db.DefineClass(ctx, []byte(scratch("Fresh", `"t":{"type":"text"}`, oneState)), setup); err != nil {
		t.Fatal(err)
	}
	if got := sql("select id from Fresh"); got != "not_allowed" {
		t.Errorf("9: a class defined after the first statement: %s", got)
	}
}
