package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

// streamChapter reads the chapter of the manual on the stream of
// changes.
func streamChapter(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "the-stream-of-changes.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// blockOf is the block of the chapter that follows a mark.
func blockOf(t *testing.T, chapter, mark string) string {
	t.Helper()
	_, after, found := strings.Cut(chapter, "<!-- "+mark+" -->\n```")
	if !found {
		t.Fatalf("the chapter has no block marked %q", mark)
	}
	_, after, _ = strings.Cut(after, "\n")
	block, _, found := strings.Cut(after, "\n```")
	if !found {
		t.Fatalf("the block marked %q does not end", mark)
	}
	return block
}

// rowsOf are the rows of the table of the chapter that follows a mark,
// each as its cells, without the two lines of its head.
func rowsOf(t *testing.T, chapter, mark string) [][]string {
	t.Helper()
	_, after, found := strings.Cut(chapter, "<!-- "+mark+" -->\n")
	if !found {
		t.Fatalf("the chapter has no table marked %q", mark)
	}
	table, _, _ := strings.Cut(after, "\n\n")
	var rows [][]string
	for i, line := range strings.Split(table, "\n") {
		if i < 2 {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		rows = append(rows, cells)
	}
	return rows
}

// subscriber is a caller that reads a stream of a server.
type subscriber struct {
	t     *testing.T
	resp  *http.Response
	in    *bufio.Reader
	close context.CancelFunc
}

// subscribe opens a stream at a server with a key.
func subscribe(t *testing.T, base, key, object string, header ...string) *subscriber {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/subscribe", strings.NewReader(object))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &subscriber{t: t, resp: resp, in: bufio.NewReader(resp.Body), close: cancel}
}

// entry reads the next entry of the stream as it was written, with the
// empty line that ends it; "" when the stream ended.
func (s *subscriber) entry() string {
	s.t.Helper()
	var b strings.Builder
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return ""
		}
		b.WriteString(line)
		if line == "\n" {
			return b.String()
		}
	}
}

// event reads the next entry that is an event, and answers it with the
// entry as it was written. It fails on an entry of another kind.
func (s *subscriber) event() (ev map[string]any, written string) {
	s.t.Helper()
	for {
		written = s.entry()
		if strings.HasPrefix(written, ":") {
			continue
		}
		lines := strings.Split(written, "\n")
		if len(lines) != 5 || !strings.HasPrefix(lines[0], "id: ") || lines[1] != "event: change" || !strings.HasPrefix(lines[2], "data: ") {
			s.t.Fatalf("the stream wrote, where an event was awaited: %q", written)
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[2], "data: ")), &ev); err != nil {
			s.t.Fatalf("the data of an entry is not JSON: %q", written)
		}
		if ev["event_id"] != strings.TrimPrefix(lines[0], "id: ") {
			s.t.Fatalf("an entry has an id that is not its event's: %q", written)
		}
		return ev, written
	}
}

// The ids and the moments the chapter shows, which another run does
// not repeat: an event read from a server is held to the chapter with
// these in their place.
var (
	eventIDs = regexp.MustCompile(`[0-9A-HJKMNP-TV-Z]{26}`)
	moments  = regexp.MustCompile(`"business_date":"[0-9-]+","ts":[0-9]+`)
)

const (
	idOfTheChapter     = "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"
	momentOfTheChapter = `"business_date":"2026-11-15","ts":1794733200`
)

func asTheChapterShows(written string) string {
	return moments.ReplaceAllString(eventIDs.ReplaceAllString(written, idOfTheChapter), momentOfTheChapter)
}

// The chapter of the manual on the stream of changes is played against
// a server: the class of its program, the stream the agent asks for,
// the reply, who hears of what, coming back, how a stream ends and what
// is refused.
func TestTheChapterOnTheStream(t *testing.T) {
	chapter := streamChapter(t)
	path := filepath.Join(t.TempDir(), "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--label", "laptop", "--db", path)
	olga = strings.TrimSpace(olga)
	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	base := "http://" + addr
	client := &http.Client{Timeout: 10 * time.Second}
	// send sends a command that must run.
	send := func(key, command, object string) string {
		t.Helper()
		status, body := ask(t, client, "POST", base+"/v1/"+command, key, object)
		if status != 200 {
			t.Fatalf("%s %s: %d %s", command, object, status, body)
		}
		return body
	}

	// The people of the chapter, each with a key, and the class of its
	// program.
	keys, keyIDs := map[string]string{}, map[string]string{}
	for id, user := range map[string]string{
		"bob":      `{"id": "bob", "kind": "person", "groups": ["sales"]}`,
		"dan":      `{"id": "dan", "kind": "person", "groups": ["sales"]}`,
		"ap-agent": `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`,
		"eve":      `{"id": "eve", "kind": "person"}`,
	} {
		send(olga, "create_user", user)
		var made struct {
			Key struct{ ID, Secret string }
		}
		if err := json.Unmarshal([]byte(send(olga, "create_key", `{"user": "`+id+`", "label": "the chapter"}`)), &made); err != nil {
			t.Fatal(err)
		}
		keys[id], keyIDs[id] = made.Key.Secret, made.Key.ID
	}
	_, class, found := strings.Cut(blockOf(t, chapter, "the program"), "const invoice = `")
	class, _, _ = strings.Cut(class, "`")
	if !found {
		t.Fatal("the program of the chapter has no class")
	}
	send(olga, "define_class", `{"definition": `+class+`}`)
	// A class of marks, which everybody reads: an entry of it says how
	// far a stream has come, so that the test knows what a stream was
	// not told.
	send(olga, "define_class", `{"definition": {"name": "Mark", "variables": {"note": {"type": "text"}},
	  "states": {"initial": "made", "list": ["made"]}, "access": {"read": "TRUE", "create": "TRUE"}}}`)
	invoiceOf := func(key, number string) {
		t.Helper()
		send(key, "create", `{"class": "Invoice", "id": "`+number+`", "values": {"number": "`+number+`", "net": "1200.00 PLN"}}`)
	}
	move := func(key, number, transition string) {
		t.Helper()
		send(key, "transition", `{"class": "Invoice", "id": "`+number+`", "transition": "`+transition+`"}`)
	}

	// The stream the agent asks for, as the chapter writes it.
	asked := strings.Split(blockOf(t, chapter, "the stream is asked for"), "\n")
	if asked[0] != "POST /v1/subscribe" || asked[2] != "Content-Type: application/json" || asked[3] != "" {
		t.Fatalf("the chapter asks for the stream with:\n%s", strings.Join(asked, "\n"))
	}
	object := asked[len(asked)-1]
	agent := subscribe(t, base, keys["ap-agent"], object)
	if agent.resp.StatusCode != 200 || agent.resp.Header.Get("Content-Type") != "text/event-stream" || agent.resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("the stream was answered with %d and %v", agent.resp.StatusCode, agent.resp.Header)
	}
	reply := agent.entry()
	// Bob writes an invoice and submits it: the agent is told of the
	// move into review, and not of the writing.
	invoiceOf(keys["bob"], "FV/2026/11/0042")
	move(keys["bob"], "FV/2026/11/0042", "submit")
	first, written := agent.event()
	reply += written
	if want := blockOf(t, chapter, "the reply") + "\n"; asTheChapterShows(reply) != want {
		t.Errorf("the server replied\n%s\nand the chapter shows\n%s", asTheChapterShows(reply), want)
	}
	if want := "data: " + blockOf(t, chapter, "an event") + "\n"; !strings.Contains(asTheChapterShows(written), want) {
		t.Errorf("the chapter shows an event that is not the one the agent was told:\n%s", written)
	}
	// The agent reads the invoice and approves it.
	if body := send(keys["ap-agent"], "get", `{"class": "Invoice", "id": "FV/2026/11/0042"}`); !strings.Contains(body, `"net":"1200.00 PLN"`) {
		t.Errorf("the agent reads %s", body)
	}
	move(keys["ap-agent"], "FV/2026/11/0042", "approve")
	agent.close()

	// Who hears of what: four callers, each with a stream of every
	// event, and the six rows of the chapter.
	callers := []string{"bob", "dan", "ap-agent", "eve"}
	streams := map[string]*subscriber{}
	for _, who := range callers {
		streams[who] = subscribe(t, base, keys[who], `{}`)
		if got := streams[who].entry(); got != ": the stream is open\n\n" {
			t.Fatalf("the stream of %s begins with %q", who, got)
		}
	}
	happens := map[string]func(){
		"Bob writes FV/2026/11/0044":  func() { invoiceOf(keys["bob"], "FV/2026/11/0044") },
		"Bob submits it":              func() { move(keys["bob"], "FV/2026/11/0044", "submit") },
		"The agent approves it":       func() { move(keys["ap-agent"], "FV/2026/11/0044", "approve") },
		"Dan writes FV/2026/11/0045":  func() { invoiceOf(keys["dan"], "FV/2026/11/0045") },
		"Olga puts Eve in finance":    func() { send(olga, "set_user", `{"id": "eve", "groups": ["finance"]}`) },
		"Dan submits FV/2026/11/0045": func() { move(keys["dan"], "FV/2026/11/0045", "submit") },
	}
	table := rowsOf(t, chapter, "who hears")
	if len(table) != len(happens) {
		t.Fatalf("the chapter has %d rows of who hears, and the test plays %d", len(table), len(happens))
	}
	for i, row := range table {
		play, known := happens[row[0]]
		if !known || len(row) != 1+len(callers) {
			t.Fatalf("the test does not know the row %q of who hears", row)
		}
		play()
		// The mark of the row: every stream is told of it, after what
		// it was told of the row.
		mark := "mark-" + strconv.Itoa(i)
		send(keys["bob"], "create", `{"class": "Mark", "id": "`+mark+`"}`)
		for j, who := range callers {
			told := "nothing"
			for {
				ev, _ := streams[who].event()
				if ev["doc_id"] == mark {
					break
				}
				told = "told"
			}
			if told != row[1+j] {
				t.Errorf("%s: %s is %s, and the chapter says %s", row[0], who, told, row[1+j])
			}
		}
	}

	// Coming back: the agent gives the last event it handled, in the
	// object the chapter shows or in the header, and is told what
	// entered review since, in order.
	back := strings.Split(blockOf(t, chapter, "coming back"), "\n")
	again := strings.ReplaceAll(strings.Join(back[4:], "\n"), idOfTheChapter, first["event_id"].(string))
	if !strings.Contains(again, `"after": "`) {
		t.Fatalf("the chapter comes back with:\n%s", again)
	}
	for how, s := range map[string]*subscriber{
		"after":         subscribe(t, base, keys["ap-agent"], again),
		"Last-Event-ID": subscribe(t, base, keys["ap-agent"], object, "Last-Event-ID", first["event_id"].(string)),
		"both":          subscribe(t, base, keys["ap-agent"], strings.ReplaceAll(again, first["event_id"].(string), "01KA2F8Q7ZJ4M0X3T5V9B1C6ZZ"), "Last-Event-ID", first["event_id"].(string)),
	} {
		if s.resp.StatusCode != 200 {
			t.Fatalf("coming back with %s: %d", how, s.resp.StatusCode)
		}
		for _, number := range []string{"FV/2026/11/0044", "FV/2026/11/0045"} {
			if ev, _ := s.event(); ev["doc_id"] != number || ev["to_state"] != "review" {
				t.Errorf("coming back with %s: told of %v, want the submit of %s", how, ev, number)
			}
		}
		s.close()
	}

	// What is refused, with one JSON object and no stream.
	for _, row := range rowsOf(t, chapter, "what is refused") {
		status, body := ask(t, client, "POST", base+"/v1/subscribe", keys["bob"], strings.Trim(row[0], "`"))
		var got struct{ Error struct{ Code string } }
		if err := json.Unmarshal([]byte(body), &got); err != nil || status == 200 || "`"+got.Error.Code+"`" != row[1] {
			t.Errorf("%s: answered %d %s, and the chapter says %s", row[0], status, body, row[1])
		}
	}

	// The streams that are open are counted.
	if _, page := ask(t, client, "GET", base+metricsPath, olga, ""); !strings.Contains(page, "\nfadodb_streams 4\n") {
		t.Errorf("four streams are open, and the counts say:\n%s", between(page, "# TYPE fadodb_streams gauge\n", "\n"))
	}

	// The agent's key is revoked. Its stream ends before the next event,
	// which it is not told.
	send(olga, "revoke_key", `{"key": "`+keyIDs["ap-agent"]+`"}`)
	invoiceOf(keys["bob"], "FV/2026/11/0046")
	if got, want := streams["ap-agent"].entry(), blockOf(t, chapter, "the key was revoked")+"\n"; got != want {
		t.Errorf("after its key was revoked the stream of the agent says\n%s\nand the chapter shows\n%s", got, want)
	}
	if more := streams["ap-agent"].entry(); more != "" {
		t.Errorf("the stream of the agent went on after its end: %q", more)
	}
	if status, _ := ask(t, client, "POST", base+"/v1/subscribe", keys["ap-agent"], `{}`); status != 401 {
		t.Errorf("a stream asked for with a revoked key: %d", status)
	}

	// The server is told to stop: every stream is told why it ends.
	stopped := make(chan int, 1)
	go func() { stopped <- stop() }()
	for _, who := range []string{"bob", "dan", "eve"} {
		var last string
		for {
			entry := streams[who].entry()
			if entry == "" {
				break
			}
			last = entry
		}
		if want := blockOf(t, chapter, "the server stops") + "\n"; last != want {
			t.Errorf("when the server stops the stream of %s ends with\n%s\nand the chapter shows\n%s", who, last, want)
		}
	}
	select {
	case code := <-stopped:
		if code != 0 {
			t.Errorf("the server ended with %d:\n%s", code, log)
		}
	case <-time.After(stopWithin):
		t.Fatalf("the server did not stop while streams were open:\n%s", log)
	}
	// Each stream has its line in the log, written when it ended.
	said := log.String()
	for _, want := range []string{
		`"command":"subscribe","user":"ap-agent","status":200,"code":"unauthenticated","ms":`,
		`"command":"subscribe","user":"bob","status":200,"code":"closed","ms":`,
		`"command":"subscribe","user":"bob","status":404,"code":"unknown_class","ms":`,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the log has no line with %s:\n%s", want, said)
		}
	}
	if strings.Contains(said, "fado_") || strings.Contains(said, "1200.00") {
		t.Errorf("the log holds a key or a value:\n%s", said)
	}

	// The line of a stream the chapter shows is one the server writes.
	var out bytes.Buffer
	(&requestLog{w: &out}).write(httpdoor.Request{
		At: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC), Took: 72 * time.Microsecond,
		Command: "subscribe", User: "ap-agent", Status: 200, Code: "closed",
		Stream: true, Lasted: time.Hour, Entries: 2,
	})
	if line := strings.TrimSpace(out.String()); line != blockOf(t, chapter, "the line of a stream") {
		t.Errorf("the server writes the line\n%s\nand the chapter shows\n%s", line, blockOf(t, chapter, "the line of a stream"))
	}
	// The codes a stream ends with, and what the server takes on.
	ends := rowsOf(t, chapter, "how a stream ends")
	if len(ends) != 3 || ends[0][1] != "`"+httpdoor.CodeUnauthenticated+"`" || ends[1][1] != "`closed`" || ends[2][1] != "`internal`" {
		t.Errorf("the chapter says a stream ends with %v", ends)
	}
	if !strings.Contains(chapter, "**"+strconv.Itoa(httpdoor.DefaultStreams)+" streams at once**") {
		t.Errorf("the chapter does not say that a server holds %d streams", httpdoor.DefaultStreams)
	}
	if !strings.Contains(serveUsage, "POST /v1/subscribe") {
		t.Error("the help of the server does not name the address of the stream")
	}
}

// The operator says how many streams the server holds, and one beyond
// them is refused at once (ADR-0059, item 2).
func TestTheServerHoldsSoManyStreams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"limits": {"streams": 2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tool("init", path)
	tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path)
	_, key, _ := tool("key", "new", "olga", "--db", path)
	key = strings.TrimSpace(key)
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0", "--settings", settings)
	base := "http://" + addr
	client := &http.Client{Timeout: 10 * time.Second}
	first, second := subscribe(t, base, key, `{}`), subscribe(t, base, key, `{}`)
	first.entry()
	second.entry()
	status, body := ask(t, client, "POST", base+"/v1/subscribe", key, `{}`)
	if status != 503 || !strings.Contains(body, `"code":"busy"`) || !strings.Contains(body, "2 streams of changes") {
		t.Errorf("a third stream: %d %s", status, body)
	}
	// A command is answered all the same.
	if status, body := ask(t, client, "POST", base+"/v1/whoami", key, `{}`); status != 200 {
		t.Errorf("a command while the streams are full: %d %s", status, body)
	}
	// One is closed, and there is room for another.
	first.close()
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, page := ask(t, client, "GET", base+metricsPath, key, ""); strings.Contains(page, "\nfadodb_streams 1\n") {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the place of a stream that was closed was not given back")
		}
	}
	if third := subscribe(t, base, key, `{}`); third.resp.StatusCode != 200 {
		t.Errorf("a stream after one was closed: %d", third.resp.StatusCode)
	}
}
