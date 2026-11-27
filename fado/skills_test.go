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

// The simulator's skill answers "what would happen if" with commands
// that write nothing. TestSimulatorSkill defines the class the skill
// shows, makes the situation it describes, sends each command as the
// agent of the skill and compares the answer with the page, checks that
// nothing was written, and holds each point of the list.
func TestSimulatorSkill(t *testing.T) {
	page := skill(t, "fadodb-simulate")
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	class := notionClass(t, page)
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	ola := fado.Principal{ID: "ola", Kind: fado.ActorPerson}
	agent := fado.Principal{ID: "claims-agent", Kind: fado.ActorAgent, System: []string{fado.GroupDesigners}}
	// The situation: Ola filed three claims at 09:00.
	for _, claim := range [][2]string{
		{"C-1", `"what":"taxi","amount":"120.00 PLN"`},
		{"C-2", `"what":"conference","amount":"8000.00 PLN"`},
		{"C-3", `"what":"hotel","amount":"300.00 PLN"`},
	} {
		if _, err := db.Exec(ctx, ola, "create", []byte(`{"class":"Claim","id":"`+claim[0]+`","values":{"approver":"claims-agent",`+claim[1]+`}}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, ola, "transition", []byte(`{"class":"Claim","id":"`+claim[0]+`","transition":"file"}`)); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(time.Hour)
	// stored says what the database holds, so that the test can tell
	// whether a command wrote.
	stored := func() string {
		t.Helper()
		var parts []string
		for _, id := range []string{"C-1", "C-2", "C-3"} {
			history, err := db.History(ctx, "Claim", id, fado.As(ola))
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, fmt.Sprintf("%s: %d versions, %s", id, len(history), history[len(history)-1].State))
		}
		stored, _ := db.Class("Claim")
		return strings.Join(parts, "; ") + fmt.Sprintf("; classes %v, Claim version %d", db.Classes(), stored.Version)
	}
	before := stored()

	// The exchange: each command of the page, in its order, and the
	// answer under it.
	exchange := exchangesOf(page)
	if len(exchange) != 6 {
		t.Fatalf("the skill shows %d commands with their answers; the test knows six", len(exchange))
	}
	for i, m := range exchange {
		got, _ := db.Exec(ctx, agent, m.name, m.sent)
		if !sameAnswer(got, m.shown) {
			t.Errorf("step %d, the command %s, is answered\n%s\nand the skill shows\n%s", i+1, m.name, got, m.shown)
		}
	}
	// The draft of step 2 is the class of the page with one line
	// changed.
	var draft struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(exchange[1].sent, &draft); err != nil {
		t.Fatal(err)
	}
	var shown, tried bytes.Buffer
	json.Compact(&shown, class)
	json.Compact(&tried, draft.Definition)
	if got := strings.Replace(shown.String(), "AMOUNT(amount) <= 5000", "AMOUNT(amount) <= 10000", 1); got != tried.String() {
		t.Error("the draft of step 2 differs from the class of the page by more than the limit")
	}
	if after := stored(); after != before {
		t.Errorf("the commands of the skill wrote: before %s, after %s", before, after)
	}

	// What goes wrong most often: ten points, each held by its number.
	if n := points(t, page); n != 10 {
		t.Errorf("the list has %d points, and the test holds ten", n)
	}
	answer := func(as fado.Principal, name, object string) (string, string) {
		got, err := db.Exec(ctx, as, name, []byte(object))
		return fado.ErrorCode(err), string(got)
	}
	definition := string(draft.Definition)
	// 2. A second preview does not see what the first would have done.
	for i := 0; i < 2; i++ {
		if code, got := answer(agent, "preview", `{"class":"Claim","id":"C-1","transition":"approve"}`); code != "" || !strings.Contains(got, `"path":["filed","approved"]`) {
			t.Errorf("2: preview %d of the same approval: %s %s", i+1, code, got)
		}
	}
	// 3. A preview of a transition at a later moment does not first
	// take the timers that will have come by then.
	if code, got := answer(agent, "preview", `{"class":"Claim","id":"C-1","transition":"approve","at":"2026-12-01"}`); code != "" || !strings.Contains(got, `"state":"approved"`) {
		t.Errorf("3: approve of C-1 previewed on 1 December: %s %s", code, got)
	}
	if code, got := answer(agent, "preview", `{"class":"Claim","id":"C-1","at":"2026-12-01"}`); code != "" || !strings.Contains(got, `"state":"expired"`) {
		t.Errorf("3: C-1 by itself on 1 December: %s %s", code, got)
	}
	// 4. A change as of another moment.
	if code, _ := answer(agent, "transition", `{"class":"Claim","id":"C-1","transition":"approve","at":"2026-12-01"}`); code != "not_allowed" {
		t.Errorf("4: a transition as of another moment: %s", code)
	}
	// 5. A draft tried by a caller who is no designer.
	plain := fado.Principal{ID: "claims-agent", Kind: fado.ActorAgent}
	if code, _ := answer(plain, "preview_draft", `{"id":"C-2","transition":"approve","definition":`+definition+`}`); code != "forbidden" {
		t.Errorf("5: a draft tried by a caller who is no designer: %s", code)
	}
	// 6. A draft under another name: not on a document, but on a create.
	renamed := strings.Replace(definition, `"name": "Claim"`, `"name": "Claim2"`, 1)
	if code, _ := answer(agent, "preview_draft", `{"id":"C-2","transition":"approve","definition":`+renamed+`}`); code != "unknown_class" {
		t.Errorf("6: a draft under another name, on a document: %s", code)
	}
	if code, got := answer(agent, "preview_draft", `{"values":{"approver":"marek","what":"a pen","amount":"3.00 PLN"},"definition":`+renamed+`}`); code != "" || !strings.Contains(got, `"class":"Claim2"`) {
		t.Errorf("6: a draft under another name, on a create: %s %s", code, got)
	}
	// 7. A draft with a mistake.
	if code, got := answer(agent, "preview_draft", `{"id":"C-2","transition":"approve","definition":`+strings.Replace(definition, "AMOUNT(amount) <= 10000", "AMOUNT(amont) <= 10000", 1)+`}`); code != "bad_definition" || !strings.Contains(got, `"/transitions/1/guard"`) {
		t.Errorf("7: a draft with a mistake: %s %s", code, got)
	}
	// 8. A batch that would not move every document gives no error.
	if code, got := answer(agent, "preview_many", `{"class":"Claim","transition":"approve","ids":["C-1","C-2"]}`); code != "" || !strings.Contains(got, `"refusal"`) {
		t.Errorf("8: a batch with a document that would not move: %s %s", code, got)
	}
	// 9. A claim that waits to be approved is not due.
	if code, got := answer(agent, "due", `{"class":"Claim"}`); code != "" || got != `{"due":[]}` {
		t.Errorf("9: what is due now: %s %s", code, got)
	}
	// 10. A preview answers for its caller.
	if code, _ := answer(ola, "preview", `{"class":"Claim","id":"C-1","transition":"approve"}`); code != "forbidden" {
		t.Errorf("10: Ola previews an approval: %s", code)
	}
	// 1. Nothing above wrote, and a real change does.
	if after := stored(); after != before {
		t.Errorf("1: the previews wrote: before %s, after %s", before, after)
	}
	if code, _ := answer(agent, "transition", `{"class":"Claim","id":"C-1","transition":"approve","key":"approve-C-1"}`); code != "" {
		t.Errorf("1: a real approval: %s", code)
	}
	if after := stored(); after == before {
		t.Error("1: a real change left the database as it was")
	}
}

// The skill for an agent's own work shows a task that the agent
// proposes, a person decides and the agent carries out. TestOwnWorkSkill
// defines the class the skill shows, plays the piece of work with what
// the page says happens between its commands, compares each answer and
// the log with the page, and holds each point of the list.
func TestOwnWorkSkill(t *testing.T) {
	page := skill(t, "fadodb-own-work")
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})
	if _, err := db.DefineClass(ctx, notionClass(t, page), setup); err != nil {
		t.Fatal(err)
	}
	marek := fado.Principal{ID: "marek", Kind: fado.ActorPerson}
	agent := fado.Principal{ID: "ops-agent", Kind: fado.ActorAgent}
	code := func(as fado.Principal, name, object string) string {
		t.Helper()
		_, err := db.Exec(ctx, as, name, []byte(object))
		return fado.ErrorCode(err)
	}
	must := func(as fado.Principal, name, object string) {
		t.Helper()
		if got := code(as, name, object); got != "" {
			t.Fatalf("%s: %s %s: %s", as.ID, name, object, got)
		}
	}

	// The exchange, with what the page says happens between its
	// commands: after step 2 Marek approves at 09:30; steps 3 and 4 are
	// at 10:00; the engine takes stall at 11:00; step 5 is at 12:00, and
	// Marek resumes; step 6 follows.
	exchange := exchangesOf(page)
	if len(exchange) != 6 {
		t.Fatalf("the skill shows %d commands with their answers; the test knows six", len(exchange))
	}
	between := map[int]func(){
		3: func() {
			clock = clock.Add(30 * time.Minute)
			must(marek, "transition", `{"class":"Task","id":"T-1","transition":"approve"}`)
			clock = clock.Add(30 * time.Minute)
		},
		5: func() {
			clock = clock.Add(time.Hour)
			if ran, err := db.Drain(ctx, setup); err != nil || ran != 1 {
				t.Fatalf("at 11:00 the engine ran %d actions, %v", ran, err)
			}
			clock = clock.Add(time.Hour)
		},
		6: func() {
			// 8. After the timeout the agent's finish is refused, and
			// so is its resume.
			if got := code(agent, "transition", `{"class":"Task","id":"T-1","transition":"finish","values":{"result":"x"}}`); got != "wrong_state" {
				t.Errorf("8: a finish after the timeout: %s", got)
			}
			if got := code(agent, "transition", `{"class":"Task","id":"T-1","transition":"resume"}`); got != "forbidden" {
				t.Errorf("1: the agent resumes its own task: %s", got)
			}
			must(marek, "transition", `{"class":"Task","id":"T-1","transition":"resume"}`)
			clock = clock.Add(10 * time.Minute)
			// 10. A finish with no result.
			if got := code(agent, "transition", `{"class":"Task","id":"T-1","transition":"finish"}`); got != "guard_false" {
				t.Errorf("10: a finish with no result: %s", got)
			}
			// 6. The agent does not write the count of attempts.
			if got := code(agent, "set", `{"class":"Task","id":"T-1","values":{"attempts":0}}`); got != "forbidden" {
				t.Errorf("6: the agent writes the count of attempts: %s", got)
			}
		},
	}
	var first []byte
	for i, m := range exchange {
		if before := between[i+1]; before != nil {
			before()
		}
		got, _ := db.Exec(ctx, agent, m.name, m.sent)
		if !sameAnswer(got, m.shown) {
			t.Errorf("step %d, the command %s, is answered\n%s\nand the skill shows\n%s", i+1, m.name, got, m.shown)
		}
		if i == 0 {
			first = got
		}
	}

	// The log of the task.
	events, err := db.Events(ctx, "Task", "T-1", fado.As(agent))
	if err != nil {
		t.Fatal(err)
	}
	var log [][]string
	for _, e := range events {
		log = append(log, []string{e.Kind, e.Name, e.ActorID, e.ActorKind, e.Origin})
	}
	if want := rows(marked(t, page, "the log of the task")); !reflect.DeepEqual(log, want) {
		t.Errorf("the log of the task is %v, and the skill shows %v", log, want)
	}

	// What goes wrong most often: ten points, each held by its number.
	// Points 6, 8 and 10 are held above, where the task is in the state
	// they speak of.
	if n := points(t, page); n != 10 {
		t.Errorf("the list has %d points, and the test holds ten", n)
	}
	// 1. The agent does not decide its own proposal, also when it names
	// itself as reviewer; and a person does not propose.
	must(agent, "create", `{"class":"Task","id":"T-2","values":{"what":"restart the server","reviewer":"ops-agent"}}`)
	if got := code(agent, "transition", `{"class":"Task","id":"T-2","transition":"approve"}`); got != "forbidden" {
		t.Errorf("1: the agent approves a task it reviews itself: %s", got)
	}
	// 2. Acting for a person needs a substitution.
	got, err := db.Exec(ctx, agent, "get", []byte(`{"class":"Task","id":"T-1","for":"marek"}`))
	if fado.ErrorCode(err) != "forbidden" || !strings.Contains(string(got), `"path":"/principal/for"`) {
		t.Errorf("2: the agent acts for Marek with no substitution: %s", got)
	}
	// 3 and 4. A second create under the same id is a duplicate; under
	// the same key it is answered as the first time, though the task is
	// done by now.
	if got := code(agent, "create", `{"class":"Task","id":"T-1","values":{"what":"delete the logs older than 90 days","reviewer":"marek"}}`); got != "duplicate" {
		t.Errorf("3: a second create under the same id, with no key: %s", got)
	}
	again, err := db.Exec(ctx, agent, exchange[0].name, exchange[0].sent)
	if err != nil || string(again) != string(first) || !strings.Contains(string(again), `"state":"proposed"`) {
		t.Errorf("4: the first command again is answered\n%s, %v", again, err)
	}
	now, _ := db.Exec(ctx, agent, "get", []byte(`{"class":"Task","id":"T-1"}`))
	if !strings.Contains(string(now), `"state":"done"`) {
		t.Errorf("4: the task as it is now: %s", now)
	}
	// 5. After a restart the open tasks are found by a query: the
	// exchange shows it in step 5.
	if !strings.Contains(string(exchange[4].sent), "owner = USER()") {
		t.Error("5: the skill no longer asks for the agent's own tasks after a restart")
	}
	// 7. The timeout is a transition with a time, and the engine took
	// it as the agent, marked as a timer.
	if log[3][1] != "stall" || log[3][4] != "timer" {
		t.Errorf("7: the timeout in the log: %v", log[3])
	}
	// 9. The third retry is refused.
	must(agent, "create", `{"class":"Task","id":"T-3","values":{"what":"rotate the keys","reviewer":"marek"}}`)
	must(marek, "transition", `{"class":"Task","id":"T-3","transition":"approve"}`)
	must(agent, "transition", `{"class":"Task","id":"T-3","transition":"start"}`)
	for attempt := 1; attempt <= 3; attempt++ {
		must(agent, "transition", `{"class":"Task","id":"T-3","transition":"fail","values":{"problem":"the disk is read-only"}}`)
		want := ""
		if attempt == 3 {
			want = "guard_false"
		}
		if got := code(agent, "transition", `{"class":"Task","id":"T-3","transition":"retry"}`); got != want {
			t.Errorf("9: the retry after attempt %d: %q", attempt, got)
		}
	}
}

// The designer's skill begins by looking at what the organisation
// already keeps. TestDesignerSkill makes the database the skill
// describes, sends each command of its story as the agent and compares
// the answer with the page; then it defines every class the page shows,
// the small one of "The shape of a class" with them, and plays the
// story the page tells, so that the classes do what the page says.
func TestDesignerSkill(t *testing.T) {
	page := skill(t, "fadodb-design")
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	db := notionDB(t, fado.Options{Now: func() time.Time { return clock }})

	// The database the story finds: a leave process and two tables.
	if _, err := db.DefineClass(ctx, []byte(`{"name":"Leave","label":"Leave request",`+
		`"description":"A day off asked for and approved. Born: here. Owner: HR. List: this class.",`+
		`"variables":{"from":{"type":"date"},"to":{"type":"date"}},`+oneState+
		`,"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"FALSE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	// The two tables are those the page shows in the answers to
	// get_table.
	tables := shownTables(page)
	if len(tables) != 2 {
		t.Fatalf("the skill shows %d tables; the test knows two", len(tables))
	}
	for _, table := range tables {
		if _, err := db.DefineTable(ctx, table, setup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ImportRows(ctx, "People", []fado.Values{
		{"login": "ola", "name": "Ola Nowak", "department": "SAL"},
		{"login": "marek", "name": "Marek Lis", "department": "SAL"},
		{"login": "anna", "name": "Anna Wolska", "department": "HR"},
		{"login": "ewa", "name": "Ewa Mazur", "department": "FIN"},
	}, fado.As(fado.Principal{ID: "hr-import", Kind: fado.ActorService, Groups: []string{"hr"}})); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ImportRows(ctx, "Departments", []fado.Values{
		{"code": "SAL", "name": "Sales", "head": "marek"},
		{"code": "HR", "name": "Human resources", "head": "anna"},
		{"code": "FIN", "name": "Finance", "head": "ewa"},
	}, fado.As(fado.Principal{ID: "ewa", Kind: fado.ActorPerson, Groups: []string{"finance"}})); err != nil {
		t.Fatal(err)
	}

	// The exchange: each command of the page, in its order, and the
	// answer under it.
	agent := fado.Principal{ID: "builder-agent", Kind: fado.ActorAgent, System: []string{fado.GroupDesigners}}
	exchange := exchangesOf(page)
	if len(exchange) != 4 {
		t.Fatalf("the skill shows %d commands with their answers; the test knows four", len(exchange))
	}
	for i, m := range exchange {
		got, _ := db.Exec(ctx, agent, m.name, m.sent)
		if !sameAnswer(got, m.shown) {
			t.Errorf("step %d, the command %s %s, is answered\n%s\nand the skill shows\n%s", i+1, m.name, m.sent, got, m.shown)
		}
	}
	// What was asked names the two tables the commands found.
	asked := rows(marked(t, page, "what was asked"))
	if len(asked) != 3 || !strings.Contains(asked[0][1], "People") || !strings.Contains(asked[1][1], "Departments") {
		t.Errorf("the table of what was asked is %v", asked)
	}

	// Every class the page shows is accepted as it stands, by the agent.
	classes := notionClasses(t, page)
	if len(classes) != 3 {
		t.Fatalf("the skill shows %d classes; the test knows three", len(classes))
	}
	for _, class := range classes {
		if _, err := db.Exec(ctx, agent, "define_class", append(append([]byte(`{"definition":`), class...), '}')); err != nil {
			t.Fatalf("the class %.40s... is refused: %v", class, err)
		}
	}
	// Each class the agent made says where it is born, who owns it and
	// where its list is.
	for _, name := range []string{"CostCentre", "TrainingRequest"} {
		got, _ := db.Exec(ctx, agent, "get_class", []byte(`{"name":"`+name+`"}`))
		for _, word := range []string{"Born: ", "Owner: ", "List: "} {
			if !strings.Contains(string(got), word) {
				t.Errorf("the description of %s does not say %q", name, word)
			}
		}
	}

	// The story the page tells.
	who := func(id string, groups ...string) fado.Principal {
		return fado.Principal{ID: id, Kind: fado.ActorPerson, Groups: groups}
	}
	ola, marek, ewa, anna := who("ola"), who("marek"), who("ewa", "finance"), who("anna", "hr")
	code := func(as fado.Principal, name, object string) string {
		t.Helper()
		_, err := db.Exec(ctx, as, name, []byte(object))
		return fado.ErrorCode(err)
	}
	must := func(as fado.Principal, name, object string) {
		t.Helper()
		if got := code(as, name, object); got != "" {
			t.Fatalf("%s: %s %s: %s", as.ID, name, object, got)
		}
	}
	must(ola, "create", `{"class":"TrainingRequest","id":"T-1","values":{"training":"a negotiation course","for_whom":"employee","cost_centre":"CC-410","cost":"1200.00 PLN"}}`)
	if got := code(ola, "transition", `{"class":"TrainingRequest","id":"T-1","transition":"ask"}`); got != "guard_false" {
		t.Errorf("Ola asks before the cost centre is approved: %q", got)
	}
	must(marek, "create", `{"class":"CostCentre","id":"CC-410","values":{"name":"Sales training","owner":"marek"}}`)
	if got := code(marek, "transition", `{"class":"CostCentre","id":"CC-410","transition":"approve"}`); got != "forbidden" {
		t.Errorf("Marek approves the cost centre he proposed: %q", got)
	}
	must(ewa, "transition", `{"class":"CostCentre","id":"CC-410","transition":"approve"}`)
	must(ola, "transition", `{"class":"TrainingRequest","id":"T-1","transition":"ask"}`)
	got, err := db.Exec(ctx, marek, "get", []byte(`{"class":"TrainingRequest","id":"T-1"}`))
	if err != nil || !strings.Contains(string(got), `"department":"SAL"`) || !strings.Contains(string(got), `"head":"marek"`) {
		t.Errorf("the request as its head reads it: %s, %v", got, err)
	}
	if got := code(ola, "transition", `{"class":"TrainingRequest","id":"T-1","transition":"approve"}`); got != "forbidden" {
		t.Errorf("Ola approves her own request: %q", got)
	}
	must(marek, "transition", `{"class":"TrainingRequest","id":"T-1","transition":"approve"}`)
	must(anna, "transition", `{"class":"TrainingRequest","id":"T-1","transition":"book"}`)
	got, _ = db.Exec(ctx, anna, "get", []byte(`{"class":"TrainingRequest","id":"T-1"}`))
	if !strings.Contains(string(got), `"state":"booked"`) {
		t.Errorf("the request after Anna booked it: %s", got)
	}
	// A training for a whole department takes the department chosen.
	must(ola, "create", `{"class":"TrainingRequest","id":"T-2","values":{"training":"first aid","for_whom":"department","chosen":"HR","cost_centre":"CC-410","cost":"3000.00 PLN"}}`)
	got, _ = db.Exec(ctx, ola, "get", []byte(`{"class":"TrainingRequest","id":"T-2"}`))
	if !strings.Contains(string(got), `"head":"anna"`) {
		t.Errorf("a training for the department HR: %s", got)
	}
	// Nothing the story names was made twice: the tables are the two
	// the agent found, and the one new class is the list nobody kept.
	if tables := db.Tables(); !reflect.DeepEqual(tables, append([]string{"People", "Departments"}, systemTables...)) {
		t.Errorf("the tables after the story: %v", tables)
	}
}
