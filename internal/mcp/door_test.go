package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
)

// database is a database as a door sees one: what it answers to each
// command, and the commands it was sent.
type database struct {
	mu   sync.Mutex
	sent []string
	// answers is what each command answers; refused marks the ones that
	// are refusals, and gone the ones no answer comes through for.
	answers map[string]string
	refused map[string]bool
	gone    map[string]error
	// waits holds a command until its channel closes, or until whoever
	// sent it stops waiting.
	waits map[string]chan struct{}
}

func (db *database) send(ctx context.Context, name string, object []byte) ([]byte, bool, error) {
	db.mu.Lock()
	db.sent = append(db.sent, name+" "+string(object))
	wait := db.waits[name]
	db.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	if err := db.gone[name]; err != nil {
		return nil, false, err
	}
	return []byte(db.answers[name] + "\n"), db.refused[name], nil
}

func (db *database) was(sent string) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, s := range db.sent {
		if s == sent {
			return true
		}
	}
	return false
}

// acme is the database of the cases: Ann, in finance, approves
// invoices, and Bob, in sales, may not.
func acme() *database {
	return &database{
		answers: map[string]string{
			"whoami":     `{"user":{"id":"ann","kind":"person","groups":["finance"]}}`,
			"get":        `{"document":{"class":"Invoice","id":"FV/2026/11/0042","state":"review","values":{"total":"1250.50 PLN"}}}`,
			"transition": `{"error":{"code":"forbidden","message":"bob may not take approve from review","hint":"the rule of approve: INGROUP(\"finance\")","path":"/access/transition/approve"}}`,
			"count":      `{"count":3}`,
			"query":      `{"documents":[]}`,
		},
		refused: map[string]bool{"transition": true},
		gone:    map[string]error{"set": errors.New("the server at https://fado.acme.example was not reached: connection refused")},
		waits:   map[string]chan struct{}{},
	}
}

func door(t *testing.T, db *database, o Options) *Door {
	t.Helper()
	if o.Version == "" {
		o.Version = "0.6.0"
	}
	d, err := New(db.send, o)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// modern is the _meta of a request of the modern revision.
const modern = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

// answered is what a door wrote: its lines in order, and each answer
// by the id of its request, as the text of the id.
type answered struct {
	lines []string
	by    map[string]map[string]any
}

// result is the result of the request with an id; it fails when the
// request was answered with an error or not at all.
func (a answered) result(t *testing.T, id string) map[string]any {
	t.Helper()
	r, ok := a.by[id]["result"].(map[string]any)
	if !ok {
		t.Fatalf("request %s has no result: %v\nall of it:\n%s", id, a.by[id], strings.Join(a.lines, "\n"))
	}
	return r
}

// failure is the error the request with an id was answered with.
func (a answered) failure(t *testing.T, id string) (code int, message string, data map[string]any) {
	t.Helper()
	e, ok := a.by[id]["error"].(map[string]any)
	if !ok {
		t.Fatalf("request %s has no error: %v\nall of it:\n%s", id, a.by[id], strings.Join(a.lines, "\n"))
	}
	data, _ = e["data"].(map[string]any)
	return int(e["code"].(float64)), e["message"].(string), data
}

// text is the text a tool answered, and whether it marked an error.
func (a answered) text(t *testing.T, id string) (string, bool) {
	t.Helper()
	r := a.result(t, id)
	content := r["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("request %s: %d parts of content, want 1", id, len(content))
	}
	part := content[0].(map[string]any)
	if part["type"] != "text" {
		t.Fatalf("request %s: content of type %v", id, part["type"])
	}
	return part["text"].(string), r["isError"].(bool)
}

// heard reads what a door wrote.
func heard(t *testing.T, written string) answered {
	t.Helper()
	a := answered{by: map[string]map[string]any{}}
	for _, line := range strings.Split(strings.TrimSuffix(written, "\n"), "\n") {
		if line == "" {
			continue
		}
		a.lines = append(a.lines, line)
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("the door wrote a line that is not JSON: %v\n%s", err, line)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("a line does not say jsonrpc 2.0: %s", line)
		}
		_, result := m["result"]
		_, failed := m["error"]
		if result == failed {
			t.Fatalf("a line has a result and an error, or neither: %s", line)
		}
		id, _ := json.Marshal(m["id"])
		a.by[string(id)] = m
	}
	return a
}

// talk sends the lines to a door and reads what it wrote when its
// input ended.
func talk(t *testing.T, d *Door, lines ...string) answered {
	t.Helper()
	var out strings.Builder
	if err := d.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	return heard(t, out.String())
}

// Every command of the table is a tool, but the ones the door says why
// it withholds; and a tool says of itself what the table says of its
// command (ADR-0064, items 2 and 3).
func TestEveryCommandIsATool(t *testing.T) {
	d := door(t, acme(), Options{})
	var listed []tool
	if err := json.Unmarshal(d.listed, &listed); err != nil {
		t.Fatal(err)
	}
	tools := map[string]tool{}
	for _, tl := range listed {
		tools[tl.Name] = tl
	}
	name := regexp.MustCompile(`^[a-z][a-z_]{0,63}$`)
	for _, c := range command.Commands() {
		tl, offered := tools[c.Name]
		if why := Withheld(c.Name); why != "" {
			if offered {
				t.Errorf("%s is withheld (%s) and is offered", c.Name, why)
			}
			continue
		}
		if c.Stream {
			t.Errorf("%s answers a stream, and the door does not say why it is no tool", c.Name)
			continue
		}
		if !offered {
			t.Errorf("%s is a command and no tool", c.Name)
			continue
		}
		if !name.MatchString(tl.Name) {
			t.Errorf("%s is not a name a tool may have", tl.Name)
		}
		if tl.Title == "" || !strings.HasPrefix(tl.Description, c.Does) || !strings.HasSuffix(tl.Description, ".") {
			t.Errorf("%s: title %q, description %q", c.Name, tl.Title, tl.Description)
		}
		if tl.InputSchema["type"] != "object" || tl.InputSchema["additionalProperties"] != false {
			t.Errorf("%s: the schema of its arguments is %v", c.Name, tl.InputSchema)
		}
		properties := tl.InputSchema["properties"].(map[string]any)
		for _, f := range c.Fields {
			if _, has := properties[f.Name]; has == f.Refused {
				t.Errorf("%s: member %s, refused %v, in the schema %v", c.Name, f.Name, f.Refused, has)
			}
		}
		want := hints{ReadOnly: c.Mark == command.Reads, Destructive: c.Mark == command.Destroys, OpenWorld: c.Name == "try_action" || c.Name == "drain"}
		if tl.Annotations != want {
			t.Errorf("%s: marks %+v, want %+v", c.Name, tl.Annotations, want)
		}
	}
	if len(tools) != len(command.Commands())-2 {
		t.Errorf("%d tools for %d commands, two of them withheld", len(tools), len(command.Commands()))
	}
	// The names the door knows by heart are commands still.
	for _, known := range []string{"subscribe", "create_key", "try_action", "drain", "whoami", "list_classes", "preview", "sql"} {
		if _, is := command.Lookup(known); !is {
			t.Errorf("the door names %s, which is no command", known)
		}
	}
	for cmd, want := range map[string]string{"transition": "Transition", "create_many": "Create many", "sql": "SQL", "whoami": "Who am I"} {
		if got := tools[cmd].Title; got != want {
			t.Errorf("the title of %s is %q, want %q", cmd, got, want)
		}
	}
	if got, want := tools["count"].Description, "Counts the documents of a class that match a filter. It answers count: how many documents match."; got != want {
		t.Errorf("count says %q, want %q", got, want)
	}
	if got := tools["revoke_key"].Description; !strings.HasSuffix(got, " It answers {}.") {
		t.Errorf("revoke_key says %q", got)
	}
}

// The modern form of the protocol: every request says its version, the
// door is asked what it is, and nothing is remembered between two
// requests (ADR-0064, item 8).
func TestTheModernForm(t *testing.T) {
	db := acme()
	a := talk(t, door(t, db, Options{Database: "https://fado.acme.example"}),
		`{"jsonrpc":"2.0","id":"discover-1","method":"server/discover","params":{`+modern+`}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+modern+`}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get","arguments":{"class":"Invoice","id":"FV/2026/11/0042"},`+modern+`}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami",`+modern+`}}`,
	)
	who := map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{"name": "fadodb", "title": "FadoDB", "version": "0.6.0"}}
	discovered := a.result(t, `"discover-1"`)
	if discovered["resultType"] != "complete" || discovered["cacheScope"] != "private" || discovered["ttlMs"] != float64(3_600_000) {
		t.Errorf("the door says of itself: %v", discovered)
	}
	if got, _ := json.Marshal(discovered["supportedVersions"]); string(got) != `["2026-07-28","2025-11-25","2025-06-18","2025-03-26","2024-11-05"]` {
		t.Errorf("the versions the door speaks: %s", got)
	}
	if got, _ := json.Marshal(discovered["capabilities"]); string(got) != `{"tools":{}}` {
		t.Errorf("what the door can do: %s", got)
	}
	if got, _ := json.Marshal(discovered["_meta"]); !jsonEqual(t, got, who) {
		t.Errorf("who answers: %s", got)
	}
	says := discovered["instructions"].(string)
	for _, part := range []string{"at https://fado.acme.example", "whoami answers", "preview answers", "the refusal under error",
		"take a key, and one sent again with the same key makes no second commit: create, set, reset, refresh, transition, transition_many, delete. No other tool takes one."} {
		if !strings.Contains(says, part) {
			t.Errorf("the door does not say %q of itself: %s", part, says)
		}
	}

	list := a.result(t, "1")
	if list["resultType"] != "complete" || list["cacheScope"] != "private" || list["ttlMs"] != float64(3_600_000) || list["nextCursor"] != nil {
		t.Errorf("the list of the tools carries %v", keysOf(list))
	}
	if n := len(list["tools"].([]any)); n != len(command.Commands())-2 {
		t.Errorf("%d tools listed", n)
	}

	text, failed := a.text(t, "2")
	if failed || text != db.answers["get"] {
		t.Errorf("get answered %q, error %v", text, failed)
	}
	if r := a.result(t, "2"); r["resultType"] != "complete" || r["_meta"] == nil {
		t.Errorf("the answer of a tool carries %v", keysOf(r))
	}
	if !db.was(`get {"class":"Invoice","id":"FV/2026/11/0042"}`) {
		t.Errorf("the object of get did not reach the database as it was given: %v", db.sent)
	}
	// A tool called with no arguments sends the empty object.
	if text, failed := a.text(t, "3"); failed || text != db.answers["whoami"] || !db.was("whoami {}") {
		t.Errorf("whoami answered %q, error %v, sent %v", text, failed, db.sent)
	}
}

// The form of the revisions before: a greeting opens the conversation
// and settles the version, and the answers carry nothing of the modern
// form.
func TestTheGreetedForm(t *testing.T) {
	for asked, agreed := range map[string]string{
		"2025-11-25": "2025-11-25",
		"2025-06-18": "2025-06-18",
		"2024-11-05": "2024-11-05",
		"2023-01-01": "2025-11-25",
		"2026-07-28": "2025-11-25",
	} {
		db := acme()
		a := talk(t, door(t, db, Options{}),
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+asked+`","capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"ExampleClient","version":"1.0.0"}}}`,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"count","arguments":{"class":"Invoice"}}}`,
			`{"jsonrpc":"2.0","id":"123","method":"ping"}`,
		)
		greeting := a.result(t, "1")
		if greeting["protocolVersion"] != agreed {
			t.Errorf("asked for %s, the door agreed %v, want %s", asked, greeting["protocolVersion"], agreed)
		}
		if got, _ := json.Marshal(greeting["serverInfo"]); !jsonEqual(t, got, map[string]any{"name": "fadodb", "title": "FadoDB", "version": "0.6.0"}) {
			t.Errorf("who answers: %s", got)
		}
		if got, _ := json.Marshal(greeting["capabilities"]); string(got) != `{"tools":{}}` || greeting["instructions"] == "" {
			t.Errorf("the greeting: %v", greeting)
		}
		if len(a.lines) != 4 {
			t.Fatalf("four requests and a notice were answered with %d lines:\n%s", len(a.lines), strings.Join(a.lines, "\n"))
		}
		list := a.result(t, "2")
		if got := keysOf(list); got != "tools" {
			t.Errorf("the list of a greeted conversation carries %s", got)
		}
		if text, failed := a.text(t, "3"); failed || text != `{"count":3}` {
			t.Errorf("count answered %q, error %v", text, failed)
		}
		if got := keysOf(a.result(t, "3")); got != "content,isError" {
			t.Errorf("the answer of a tool in a greeted conversation carries %s", got)
		}
		// A ping is answered with the empty result, written out.
		found := false
		for _, line := range a.lines {
			found = found || line == `{"jsonrpc":"2.0","id":"123","result":{}}`
		}
		if !found {
			t.Errorf("the ping was not answered with {}:\n%s", strings.Join(a.lines, "\n"))
		}
	}
}

// A refusal of the database is the answer of a tool, as the database
// wrote it and marked as an error; so is a command no answer came
// through for, in the same form and with the door's own code
// (ADR-0064, item 4).
func TestARefusalIsAnAnswer(t *testing.T) {
	db := acme()
	a := talk(t, door(t, db, Options{}),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"transition","arguments":{"class":"Invoice","id":"FV/2026/11/0042","transition":"approve"},`+modern+`}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"set","arguments":{"class":"Invoice","id":"FV/2026/11/0042","values":{"total":"1300.00 PLN"},"key":"raise-0042"},`+modern+`}}`,
	)
	text, failed := a.text(t, "1")
	if !failed || text != db.answers["transition"] {
		t.Errorf("the refusal reached the agent as %q, error %v", text, failed)
	}
	text, failed = a.text(t, "2")
	var none struct {
		Error struct{ Code, Message, Hint string }
	}
	if err := json.Unmarshal([]byte(text), &none); err != nil || !failed {
		t.Fatalf("no answer reached the agent as %q, error %v: %v", text, failed, err)
	}
	if none.Error.Code != CodeNoAnswer || !strings.Contains(none.Error.Message, "was not reached: connection refused") || !strings.Contains(none.Error.Hint, "may have been carried out") || !strings.Contains(none.Error.Hint, "same key") {
		t.Errorf("no answer reached the agent as %+v", none.Error)
	}
}

// When no answer comes through, the hint says what is safe for the
// command that was sent: a command that reads is asked again, one that
// commits a document is sent again with its key, and any other has no
// key to be sent again with (ADR-0064, item 4).
func TestNoAnswerSaysWhatIsSafe(t *testing.T) {
	db := acme()
	lost := errors.New("the answer of the server broke off: unexpected EOF")
	db.gone = map[string]error{"get": lost, "transition": lost, "delete": lost, "set_user": lost, "try_action": lost, "define_class": lost}
	hint := func(name string) string {
		t.Helper()
		a := talk(t, door(t, db, Options{}), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":{},`+modern+`}}`)
		text, failed := a.text(t, "1")
		var none struct {
			Error struct{ Code, Hint string }
		}
		if err := json.Unmarshal([]byte(text), &none); err != nil || !failed || none.Error.Code != CodeNoAnswer {
			t.Fatalf("no answer of %s reached the agent as %q, error %v: %v", name, text, failed, err)
		}
		return none.Error.Hint
	}
	for name, want := range map[string]string{
		"get":          "the command only reads, so nothing was changed: ask again",
		"transition":   "the command may have been carried out: sent again with the same key, it makes no second commit; sent with no key, read the document before sending it again",
		"delete":       "the command may have been carried out: sent again with the same key, it makes no second commit; sent with no key, read the document before sending it again",
		"set_user":     "the command may have been carried out, and it takes no key: read what it would have changed before sending it again",
		"try_action":   "the command may have been carried out, and it takes no key: read what it would have changed before sending it again",
		"define_class": "the command may have been carried out, and it takes no key: read what it would have changed before sending it again",
	} {
		if got := hint(name); got != want {
			t.Errorf("no answer of %s is hinted\n%s\nand not\n%s", name, got, want)
		}
	}
}

// The door tells an agent to look before it builds: on its sign, and on
// each tool that makes a class, a table or a schema, since a program
// may show its agent the tools and not the sign. A door with no such
// tool says nothing of it, and a door names only the tools it offers.
func TestLookBeforeYouBuild(t *testing.T) {
	const sign = " Look before you build: before you define a class, a table or a schema, list what the database has (list_classes, list_tables, list_schemas) and read each one that may be the same thing; match by meaning, not by name. Ask the person you work for which one to use, and never keep a thing twice."
	const onATool = " Look before you build: list_classes, list_tables and list_schemas answer what the database already keeps, and a thing is never kept twice."
	described := func(d *Door) map[string]string {
		t.Helper()
		var tools []struct{ Name, Description string }
		if err := json.Unmarshal(d.listed, &tools); err != nil {
			t.Fatal(err)
		}
		says := map[string]string{}
		for _, tool := range tools {
			says[tool.Name] = tool.Description
		}
		return says
	}

	all := door(t, acme(), Options{})
	if !strings.Contains(all.says, sign) {
		t.Errorf("a door with every tool says of itself: %s", all.says)
	}
	var building []string
	for name, says := range described(all) {
		if strings.Contains(says, "Look before you build") != builds(name) {
			t.Errorf("%s says: %s", name, says)
		}
		if builds(name) {
			building = append(building, name)
			if !strings.HasSuffix(says, onATool) {
				t.Errorf("%s, a tool that builds, says: %s", name, says)
			}
		}
	}
	sort.Strings(building)
	if got := strings.Join(building, ","); got != "define_class,define_classes,define_schema,define_table" {
		t.Errorf("the tools that build are %s", got)
	}

	// A door that builds nothing says nothing of it.
	for how, o := range map[string]Options{"only reads": {ReadOnly: true}, "has no tool that builds": {Only: []string{"get", "create", "transition"}}} {
		if d := door(t, acme(), o); strings.Contains(d.says, "Look before") {
			t.Errorf("a door that %s says of itself: %s", how, d.says)
		}
	}
	// A door names the tools it offers and no other.
	some := door(t, acme(), Options{Only: []string{"define_class", "list_classes", "get_class"}})
	if !strings.Contains(some.says, "list what the database has (list_classes) and read each one") ||
		!strings.HasSuffix(described(some)["define_class"], " Look before you build: list_classes answers what the database already keeps, and a thing is never kept twice.") {
		t.Errorf("a door with one tool that lists says %s\nand its define_class %s", some.says, described(some)["define_class"])
	}
	none := door(t, acme(), Options{Only: []string{"define_table"}})
	if !strings.Contains(none.says, "list what the database has and read each one") ||
		!strings.HasSuffix(described(none)["define_table"], " Look before you build: a thing is never kept twice.") {
		t.Errorf("a door with no tool that lists says %s\nand its define_table %s", none.says, described(none)["define_table"])
	}
}

// The door says of itself which tools take a key, and they are the
// ones whose arguments have one: what the door says and what a tool
// lists cannot come apart.
func TestTheToolsThatTakeAKey(t *testing.T) {
	d := door(t, acme(), Options{})
	var tools []struct {
		Name        string
		InputSchema struct {
			Properties map[string]struct{ Description string }
		}
	}
	if err := json.Unmarshal(d.listed, &tools); err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, tool := range tools {
		if strings.HasPrefix(tool.InputSchema.Properties["key"].Description, "the idempotency key") {
			listed = append(listed, tool.Name)
		}
	}
	if got := keyed(d.tools); strings.Join(got, ",") != strings.Join(listed, ",") || len(got) != 7 {
		t.Errorf("the door says these take a key: %v, and the tools that list one are %v", got, listed)
	}
	// A door with one such tool names that one, and a door with none says
	// nothing of keys.
	one := door(t, acme(), Options{Only: []string{"get", "transition", "set_user"}})
	if !strings.Contains(one.says, "makes no second commit: transition. No other tool takes one.") {
		t.Errorf("a door with one tool that takes a key says of itself: %s", one.says)
	}
	if none := door(t, acme(), Options{Only: []string{"get", "set_user"}}); strings.Contains(none.says, "a key") {
		t.Errorf("a door with no tool that takes a key says of itself: %s", none.says)
	}
}

// What the door does not take is answered with an error of the
// protocol that says what to send, and never reaches the database.
func TestWhatTheDoorDoesNotTake(t *testing.T) {
	db := acme()
	a := talk(t, door(t, db, Options{}),
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list","params":{`+modern+`}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"approve_everything","arguments":{},`+modern+`}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"create_key","arguments":{"user":"ap-agent"},`+modern+`}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"subscribe","arguments":{},`+modern+`}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get","arguments":["Invoice"],`+modern+`}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{"cursor":"page-2",`+modern+`}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":"get"}`,
		`{"jsonrpc":"2.0","id":11}`,
		`{"jsonrpc":"2.0","id":null,"method":"tools/list","params":{`+modern+`}}`,
		`{"jsonrpc":"2.0","id":12,"method":"server/discover"}`,
		// Passed over without a word: a notice, an answer to a request
		// the door never sent, and an empty line.
		`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":77,"result":{}}`,
		``,
	)
	for id, want := range map[string]struct {
		code int
		says string
	}{
		"1":  {-32602, "does not say its version of the protocol"},
		"2":  {-32022, "Unsupported protocol version"},
		"3":  {-32602, "io.modelcontextprotocol/clientCapabilities"},
		"4":  {-32601, "no method resources/list"},
		"5":  {-32602, "Unknown tool: approve_everything"},
		"6":  {-32602, "Unknown tool: create_key. The command is offered as no tool: its answer holds the secret of a key"},
		"7":  {-32602, "Unknown tool: subscribe. The command is offered as no tool: its answer is a stream"},
		"8":  {-32602, "the arguments of a tool are one JSON object"},
		"9":  {-32602, "one page"},
		"10": {-32602, "the params of tools/call are not read"},
		"11": {-32600, "names no method"},
		"12": {-32602, "does not say its version of the protocol"},
	} {
		code, message, _ := a.failure(t, id)
		if code != want.code || !strings.Contains(message, want.says) {
			t.Errorf("request %s: %d %q, want %d and %q", id, code, message, want.code, want.says)
		}
	}
	// The one of the wrong version is told the ones the door speaks.
	_, _, data := a.failure(t, "2")
	if got, _ := json.Marshal(data); string(got) != `{"requested":"1900-01-01","supported":["2026-07-28","2025-11-25","2025-06-18","2025-03-26","2024-11-05"]}` {
		t.Errorf("the versions told: %s", got)
	}
	// The request whose id is null is answered with no id.
	if code, message, _ := a.failure(t, "null"); code != -32600 || !strings.Contains(message, "a text or a number") {
		t.Errorf("an id of null: %d %q", code, message)
	}
	if len(a.lines) != 13 {
		t.Errorf("%d lines for 13 requests:\n%s", len(a.lines), strings.Join(a.lines, "\n"))
	}
	if len(db.sent) != 0 {
		t.Errorf("the database was sent %v", db.sent)
	}

	// A line that is not JSON, and a list of messages.
	a = talk(t, door(t, db, Options{}), `{"jsonrpc":"2.0","id":1,"method":`)
	if code, message, _ := a.failure(t, "null"); code != -32700 || !strings.Contains(message, "not one JSON object") {
		t.Errorf("a line that is not JSON: %d %q", code, message)
	}
	a = talk(t, door(t, db, Options{}), `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	if code, message, _ := a.failure(t, "null"); code != -32600 || !strings.Contains(message, "one message to a line") {
		t.Errorf("a list of messages: %d %q", code, message)
	}
}

// A line longer than the door reads is read to its end and answered
// with an error; the line after it is read as any other.
func TestALineTooLong(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 300)+"\n  short \r\nlast"), 16)
	l, err := readLine(r, 100)
	if !l.long || len(l.text) != 0 || err != nil {
		t.Errorf("the long line: %+v, %v", l, err)
	}
	l, err = readLine(r, 100)
	if l.long || string(l.text) != "short" || err != nil {
		t.Errorf("the line after it: %+v, %v", l, err)
	}
	l, err = readLine(r, 100)
	if l.long || string(l.text) != "last" || err != io.EOF {
		t.Errorf("the last line, which no line break ends: %+v, %v", l, err)
	}

	d := door(t, acme(), Options{})
	s := &served{door: d, out: &strings.Builder{}, stop: func() {}}
	s.take(context.Background(), line{long: true})
	a := heard(t, s.out.(*strings.Builder).String())
	if code, message, _ := a.failure(t, "null"); code != -32600 || !strings.Contains(message, "64 MiB") {
		t.Errorf("a line too long: %d %q", code, message)
	}
}

// conversation is a door with an agent's program that writes a line at
// a time and reads the answers as they come.
type conversation struct {
	t     *testing.T
	to    *io.PipeWriter
	from  *bufio.Reader
	ended chan error
}

func converse(t *testing.T, d *Door) *conversation {
	t.Helper()
	in, to := io.Pipe()
	from, out := io.Pipe()
	c := &conversation{t: t, to: to, from: bufio.NewReader(from), ended: make(chan error, 1)}
	go func() {
		err := d.Serve(context.Background(), in, out)
		out.Close()
		c.ended <- err
	}()
	t.Cleanup(func() { to.Close() })
	return c
}

func (c *conversation) say(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.to, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// hear reads the next answer and answers the id of its request.
func (c *conversation) hear() (id string, line string) {
	c.t.Helper()
	got := make(chan string, 1)
	go func() {
		text, _ := c.from.ReadString('\n')
		got <- text
	}()
	select {
	case line = <-got:
	case <-time.After(10 * time.Second):
		c.t.Fatal("the door answered nothing")
	}
	var m struct{ ID json.RawMessage }
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		c.t.Fatalf("the door wrote %q: %v", line, err)
	}
	return string(m.ID), line
}

// A slow command holds up no other: the door carries its calls side by
// side, and answers each when it is done.
func TestCallsRunSideBySide(t *testing.T) {
	db := acme()
	slow := make(chan struct{})
	db.waits["query"] = slow
	c := converse(t, door(t, db, Options{}))
	c.say(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query","arguments":{"class":"Invoice"},` + modern + `}}`)
	c.say(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"count","arguments":{"class":"Invoice"},` + modern + `}}`)
	if id, line := c.hear(); id != "2" {
		t.Fatalf("the quick command was not answered while the slow one ran: %s", line)
	}
	close(slow)
	if id, line := c.hear(); id != "1" {
		t.Fatalf("the slow command was not answered: %s", line)
	}
}

// A call its agent's program stops is stopped and given no answer; the
// door goes on answering.
func TestACallIsStopped(t *testing.T) {
	db := acme()
	db.waits["query"] = make(chan struct{})
	c := converse(t, door(t, db, Options{}))
	c.say(`{"jsonrpc":"2.0","id":"q-1","method":"tools/call","params":{"name":"query","arguments":{"class":"Invoice"},` + modern + `}}`)
	for !db.was(`query {"class":"Invoice"}`) {
		time.Sleep(time.Millisecond)
	}
	c.say(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"q-1","reason":"the user stopped it"}}`)
	// A notice that names a request the door does not run changes nothing.
	c.say(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":404}}`)
	c.say(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"count","arguments":{"class":"Invoice"},` + modern + `}}`)
	if id, line := c.hear(); id != "2" {
		t.Fatalf("after a call was stopped the door answered: %s", line)
	}
	c.to.Close()
	if err := <-c.ended; err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(c.from); len(rest) != 0 {
		t.Errorf("the stopped call was answered: %s", rest)
	}
}

// What was asked before the input ended is still answered.
func TestTheEndOfTheInput(t *testing.T) {
	db := acme()
	slow := make(chan struct{})
	db.waits["count"] = slow
	c := converse(t, door(t, db, Options{}))
	c.say(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"count","arguments":{"class":"Invoice"},` + modern + `}}`)
	for !db.was(`count {"class":"Invoice"}`) {
		time.Sleep(time.Millisecond)
	}
	c.to.Close()
	select {
	case err := <-c.ended:
		t.Fatalf("the door ended with a call under way: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(slow)
	if id, line := c.hear(); id != "1" {
		t.Fatalf("the call under way was not answered: %s", line)
	}
	if err := <-c.ended; err != nil {
		t.Fatal(err)
	}
}

// A door that cannot write an answer ends, and says why; one that is
// told to stop ends too.
func TestTheDoorEnds(t *testing.T) {
	d := door(t, acme(), Options{})
	in, to := io.Pipe()
	defer to.Close()
	from, out := io.Pipe()
	from.CloseWithError(errors.New("the agent's program is gone"))
	ended := make(chan error, 1)
	go func() { ended <- d.Serve(context.Background(), in, out) }()
	if _, err := io.WriteString(to, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{`+modern+`}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err == nil || !strings.Contains(err.Error(), "the agent's program is gone") {
			t.Errorf("the door ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the door did not end when it could not write")
	}

	ctx, stop := context.WithCancel(context.Background())
	in2, to2 := io.Pipe()
	defer to2.Close()
	go func() { ended <- d.Serve(ctx, in2, io.Discard) }()
	stop()
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("the door that was told to stop ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the door did not end when it was told to")
	}
}

// Whoever starts a door may keep tools from its agent: every tool that
// does not only read, or all but the ones named (ADR-0064, item 6).
func TestToolsKeptFromAnAgent(t *testing.T) {
	db := acme()
	reads := door(t, db, Options{ReadOnly: true})
	for _, name := range reads.Tools() {
		if c, _ := command.Lookup(name); c.Mark != command.Reads {
			t.Errorf("a door that only reads offers %s", name)
		}
	}
	if n := len(reads.Tools()); n < 20 {
		t.Errorf("a door that only reads offers %d tools", n)
	}
	if !strings.Contains(reads.says, "only the tools that read") || strings.Contains(reads.says, "a key") {
		t.Errorf("a door that only reads says of itself: %s", reads.says)
	}
	a := talk(t, reads, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"transition","arguments":{},`+modern+`}}`)
	if code, message, _ := a.failure(t, "1"); code != -32602 || !strings.Contains(message, "only the tools that read") {
		t.Errorf("transition at a door that only reads: %d %q", code, message)
	}

	few := door(t, db, Options{Only: []string{"transition", "get", "whoami"}})
	if got := strings.Join(few.Tools(), ","); got != "get,transition,whoami" {
		t.Errorf("a door of three tools offers %s", got)
	}
	if strings.Contains(few.says, "list_classes") || strings.Contains(few.says, "preview answers") {
		t.Errorf("a door names tools it does not offer: %s", few.says)
	}
	a = talk(t, few, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete","arguments":{},`+modern+`}}`)
	if code, message, _ := a.failure(t, "1"); code != -32602 || !strings.Contains(message, "delete is not in it") {
		t.Errorf("delete at a door of three tools: %d %q", code, message)
	}
	if len(db.sent) != 0 {
		t.Errorf("the database was sent %v", db.sent)
	}

	for _, wrong := range []struct {
		o    Options
		says string
	}{
		{Options{Only: []string{"get", "approve"}}, `no command is named "approve"`},
		{Options{Only: []string{"create_key"}}, "create_key is offered as no tool"},
		{Options{Only: []string{"subscribe"}}, "subscribe is offered as no tool"},
		{Options{ReadOnly: true, Only: []string{"get", "transition"}}, "transition does not only read"},
	} {
		if _, err := New(db.send, wrong.o); err == nil || !strings.Contains(err.Error(), wrong.says) {
			t.Errorf("%+v: %v, want %q", wrong.o, err, wrong.says)
		}
	}
}

func keysOf(m map[string]any) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	// encoding/json wrote them in order; a map does not keep it.
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return strings.Join(keys, ",")
}

func jsonEqual(t *testing.T, got []byte, want any) bool {
	t.Helper()
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	return string(got) == string(w)
}
