package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// answering is a server that is not FadoDB's: it answers every request
// as the test says, and keeps what it was sent.
type answering struct {
	*httptest.Server
	path, key, kind, agent, body string
}

func answeringWith(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *answering {
	t.Helper()
	a := &answering{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		a.path, a.key, a.kind, a.agent, a.body = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("User-Agent"), string(body)
		answer(w, r)
	}))
	t.Cleanup(a.Close)
	return a
}

// asOlga gives the tool a key and no server, as a test that calls a
// server of its own wants it.
func asOlga(t *testing.T) {
	t.Helper()
	t.Setenv("FADODB_KEY", "fado_0000000000_olga")
	t.Setenv("FADODB_URL", "")
}

// What the tool sends: the command at its address, its object as it
// was given, the key as a bearer, and nothing else of its own.
func TestExecSendsTheCommandAsItWasGiven(t *testing.T) {
	asOlga(t)
	server := answeringWith(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"document":{"id":"FV/2026/11/0042","total":1250.50,"note":"a \u0026 b"}}`)
	})
	const object = `{"class": "Invoice",  "id": "FV/2026/11/0042", "values": {"total": 1250.50}}`
	code, out, said := tool("exec", "get", object, "--url", server.URL)
	if code != exitDone || said != "" {
		t.Fatalf("exit %d, said %q", code, said)
	}
	if server.path != "/v1/get" || server.body != object || server.key != "Bearer fado_0000000000_olga" || server.kind != "application/json" {
		t.Errorf("sent %s %q with %q as %q", server.path, server.body, server.key, server.kind)
	}
	if !strings.HasPrefix(server.agent, "fadodb/") {
		t.Errorf("the tool names itself %q", server.agent)
	}
	// The answer is set out over lines, and every value in it is the
	// server's own: the number keeps its last zero, the text its
	// escape.
	const want = "{\n  \"document\": {\n    \"id\": \"FV/2026/11/0042\",\n    \"total\": 1250.50,\n    \"note\": \"a \\u0026 b\"\n  }\n}\n"
	if out != want {
		t.Errorf("printed\n%s\nwant\n%s", out, want)
	}

	// A server may be newer than the tool: a command the tool does not
	// know is sent, and the server judges it.
	if _, known := knownCommand("flow"); known {
		t.Fatal("the test wants a command the tool does not know")
	}
	if code, _, said := tool("exec", "flow", `{"class": "Invoice"}`, "--url", server.URL); code != exitDone || server.path != "/v1/flow" {
		t.Errorf("a command the tool does not know: exit %d, sent to %s, said %q", code, server.path, said)
	}
	// With no object, the command is sent {}.
	if tool("exec", "list_classes", "--url", server.URL); server.body != "{}" {
		t.Errorf("with no object the tool sent %q", server.body)
	}
	// A server behind a path keeps its path.
	if tool("exec", "list_classes", "--url", server.URL+"/acme/"); server.path != "/acme/v1/list_classes" {
		t.Errorf("a server behind a path was asked at %s", server.path)
	}
}

// closed is an output that takes nothing, as a pipe whose reader has
// gone.
type closed struct{}

func (closed) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// knownCommand finds a command in the table the tool holds.
func knownCommand(name string) (*fado.Command, bool) {
	for _, c := range fado.Commands() {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}

// Where the object comes from: the line, standard input, a file, and a
// member of it from a file of its own.
func TestExecReadsTheObject(t *testing.T) {
	asOlga(t)
	server := answeringWith(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{}`) })
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	whole := write("command.json", "{\"class\": \"Invoice\"}\n")
	// The definition as its author wrote it: its spaces, the order of
	// its members and its & are the author's, and reach the server so.
	const definition = `{"name": "Invoice",   "title": "\"Invoice for \" & supplier", "variables": {"supplier": {"type": "text"}}}`
	class := write("invoice.json", definition+"\n")
	list := write("both.json", "[1, 2]")

	old := stdin
	defer func() { stdin = old }()
	stdin = strings.NewReader(" {\"class\": \"Invoice\", \"id\": \"A\"}\n")
	if code, _, said := tool("exec", "get", "-", "--url", server.URL); code != exitDone || server.body != `{"class": "Invoice", "id": "A"}` {
		t.Errorf("from standard input: exit %d, sent %q, said %q", code, server.body, said)
	}
	if code, _, said := tool("exec", "count", "@"+whole, "--url", server.URL); code != exitDone || server.body != `{"class": "Invoice"}` {
		t.Errorf("from a file: exit %d, sent %q, said %q", code, server.body, said)
	}
	if code, _, said := tool("exec", "define_class", "--from", "definition="+class, "--url", server.URL); code != exitDone || server.body != `{"definition":`+definition+`}` {
		t.Errorf("a member from a file: exit %d, sent %q, said %q", code, server.body, said)
	}
	if code, _, said := tool("exec", "define_classes", `{"key": "k1"}`, "--from", "definitions="+list, "--from", "extra="+class, "--url", server.URL); code != exitDone || server.body != `{"key": "k1","definitions":[1, 2],"extra":`+definition+`}` {
		t.Errorf("two members from files: exit %d, sent %q, said %q", code, server.body, said)
	}

	// What is not an object is not sent.
	server.body = "nothing was sent"
	notJSON := write("notes.txt", "not JSON")
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"get", `["Invoice"]`}, "one JSON object"},
		{[]string{"get", `{"class": `}, "one JSON object"},
		{[]string{"get", ""}, "holds nothing"},
		{[]string{"get", "@" + filepath.Join(dir, "none.json")}, "reading the object"},
		{[]string{"get", "@" + notJSON}, "one JSON object"},
		{[]string{"get", "{}", "{}"}, "its object is one argument"},
		{[]string{"define_class", "--from", "definition"}, "a member and a file"},
		{[]string{"define_class", "--from", "definition=" + notJSON}, "does not hold JSON"},
		{[]string{"define_class", "--from", "definition=" + filepath.Join(dir, "none.json")}, "reading the member definition"},
		{[]string{"define_class", `{"definition": {}}`, "--from", "definition=" + class}, "gives it another"},
		{[]string{"Get", "{}"}, "is not the name of a command"},
		{[]string{"../health", "{}"}, "is not the name of a command"},
		{[]string{"get", "{}", "--csv"}, "--csv prints the documents of query and the rows of sql"},
		{[]string{}, "say which command to send"},
	} {
		code, out, said := tool(append([]string{"exec", "--url", server.URL}, c.args...)...)
		if code != exitUsage || out != "" || !strings.Contains(said, c.says) {
			t.Errorf("%v: exit %d, printed %q, said %q; want exit %d and %q", c.args, code, out, said, exitUsage, c.says)
		}
	}
	if server.body != "nothing was sent" {
		t.Errorf("a line that could not be read sent %q", server.body)
	}
}

// Which server, and which key: the flag before the environment, and
// the place fadodb serve listens at when nothing says.
func TestExecFindsTheServerAndTheKey(t *testing.T) {
	t.Setenv("FADODB_KEY", "")
	t.Setenv("FADODB_URL", "")
	for _, c := range []struct {
		given, env string
		plain      bool
		want, says string
	}{
		{want: "http://127.0.0.1:8080/v1/get"},
		{env: "https://fado.acme.example", want: "https://fado.acme.example/v1/get"},
		{given: "https://other.example:8443/", env: "https://fado.acme.example", want: "https://other.example:8443/v1/get"},
		{given: "http://localhost:9000", want: "http://localhost:9000/v1/get"},
		{given: "http://[::1]:9000", want: "http://[::1]:9000/v1/get"},
		{given: "http://fado.acme.example", says: "the key would travel in the clear"},
		{given: "http://fado.acme.example", plain: true, want: "http://fado.acme.example/v1/get"},
		{given: "fado.acme.example", says: "a server is given as"},
		{given: "ftp://fado.acme.example", says: "a server is given as"},
		{given: "https://fado.acme.example/?db=acme", says: "a server is given as"},
		{given: "https://olga:secret@fado.acme.example", says: "a server is given as"},
		{env: "nowhere", says: "FADODB_URL is"},
	} {
		t.Setenv("FADODB_URL", c.env)
		got, err := addressOf(c.given, "get", c.plain)
		switch {
		case c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)):
			t.Errorf("%+v: %q, %v; want an error with %q", c, got, err, c.says)
		case c.says == "" && (err != nil || got != c.want):
			t.Errorf("%+v: %q, %v; want %q", c, got, err, c.want)
		}
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "olga.key")
	if err := os.WriteFile(file, []byte("fado_0000000000_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.key")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file, env  string
		want, says string
	}{
		{env: "fado_0000000000_env", want: "fado_0000000000_env"},
		{env: " fado_0000000000_env\n", want: "fado_0000000000_env"},
		{file: file, env: "fado_0000000000_env", want: "fado_0000000000_file"},
		{says: "say whose key the command carries"},
		{file: empty, env: "fado_0000000000_env", says: "holds no key"},
		{file: filepath.Join(dir, "none.key"), says: "reading the key"},
		{env: "two words", says: "a key is one word"},
		{env: "fado_\r\nX-Other: 1", says: "a key is one word"},
	} {
		t.Setenv("FADODB_KEY", c.env)
		got, err := keyOf(c.file)
		switch {
		case c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)):
			t.Errorf("%+v: %q, %v; want an error with %q", c, got, err, c.says)
		case c.says == "" && (err != nil || got != c.want):
			t.Errorf("%+v: %q, %v; want %q", c, got, err, c.want)
		}
	}

	// The key is in no argument, so no line of a terminal holds one.
	if strings.Contains(execUsage, "--key ") {
		t.Error("the tool takes a key as an argument")
	}
	// A line that names no key sends nothing and says how one is made.
	t.Setenv("FADODB_KEY", "")
	server := answeringWith(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{}`) })
	if code, _, said := tool("exec", "list_classes", "--url", server.URL); code != exitUsage || server.path != "" || !strings.Contains(said, "fadodb key new USER --db FILE") {
		t.Errorf("with no key: exit %d, sent to %q, said %q", code, server.path, said)
	}
}

// How the tool ends: 0 for an answer, 1 for a refusal, which goes to
// standard error as the server gave it, and 3 when what answered is not
// a FadoDB server or nothing answered.
func TestExecEndsAsTheAnswerSays(t *testing.T) {
	asOlga(t)
	refusal := `{"error":{"code":"forbidden","message":"olga may not take approve from review","hint":"the rule of approve: INGROUP(\"finance\")","path":"/access/transition/approve"}}`
	server := answeringWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/transition":
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, refusal)
		case "/v1/busy":
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"code":"busy","message":"the server has as many commands as it takes"}}`)
		case "/v1/moved":
			http.Redirect(w, r, "/v1/elsewhere", http.StatusTemporaryRedirect)
		case "/v1/elsewhere":
			t.Error("the tool followed a redirect, and its key with it")
		case "/v1/page":
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "<html>\n<body>502 Bad Gateway</body>\n</html>")
		case "/v1/list":
			io.WriteString(w, `["not", "an", "object"]`)
		case "/v1/claims":
			// An answer that says it worked and holds a refusal's
			// shape is still an answer: the status decides.
			io.WriteString(w, `{"error": "a member of an answer"}`)
		}
	})
	code, out, said := tool("exec", "transition", `{"class": "Invoice"}`, "--url", server.URL)
	const want = "{\n  \"error\": {\n    \"code\": \"forbidden\",\n    \"message\": \"olga may not take approve from review\",\n    \"hint\": \"the rule of approve: INGROUP(\\\"finance\\\")\",\n    \"path\": \"/access/transition/approve\"\n  }\n}\n"
	if code != exitRefused || out != "" || said != want {
		t.Errorf("a refusal: exit %d, printed %q, said\n%s\nwant\n%s", code, out, said, want)
	}
	if code, out, said := tool("exec", "busy", "--url", server.URL); code != exitRefused || out != "" || !strings.Contains(said, `"code": "busy"`) {
		t.Errorf("a server that is busy: exit %d, printed %q, said %q", code, out, said)
	}
	for command, says := range map[string]string{
		"moved": "307 Temporary Redirect",
		"page":  "502 Bad Gateway\n  <html>\n  <body>502 Bad Gateway</body>",
		"list":  "is not a FadoDB server",
	} {
		if code, out, said := tool("exec", command, "--url", server.URL); code != exitNoAnswer || out != "" || !strings.Contains(said, says) {
			t.Errorf("%s: exit %d, printed %q, said %q; want exit %d and %q", command, code, out, said, exitNoAnswer, says)
		}
	}
	if code, out, _ := tool("exec", "claims", "--url", server.URL); code != exitDone || !strings.Contains(out, "a member of an answer") {
		t.Errorf("an answer with a member named error: exit %d, printed %q", code, out)
	}

	// An answer that cannot be written is no answer: the command was
	// carried out, and the tool says so.
	var told bytes.Buffer
	if code := runExec(context.Background(), []string{"claims", "--url", server.URL}, closed{}, &told); code != exitNoAnswer || !strings.Contains(told.String(), "was carried out, and its answer could not be written") {
		t.Errorf("an answer that cannot be written: exit %d, said %q", code, told.String())
	}

	// Told to stop while it waits, the tool has no answer, and does
	// not say that the command was carried out.
	waiting := make(chan struct{})
	slow := answeringWith(t, func(_ http.ResponseWriter, r *http.Request) {
		close(waiting)
		<-r.Context().Done()
	})
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	ended := make(chan int, 1)
	var printed, said2 buffer
	go func() { ended <- runExec(ctx, []string{"transition", "--url", slow.URL}, &printed, &said2) }()
	select {
	case <-waiting:
	case <-time.After(10 * time.Second):
		t.Fatal("the command was not sent")
	}
	interrupt()
	select {
	case code := <-ended:
		if code != exitNoAnswer || printed.String() != "" || !strings.Contains(said2.String(), "interrupted before the answer of transition came") {
			t.Errorf("interrupted while it waits: exit %d, printed %q, said %q", code, printed.String(), said2.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the tool did not end when it was interrupted")
	}

	// Nothing answers where the last server was.
	gone := answeringWith(t, func(http.ResponseWriter, *http.Request) {})
	gone.Close()
	if code, out, said := tool("exec", "list_classes", "--url", gone.URL); code != exitNoAnswer || out != "" || !strings.Contains(said, "no answer from "+gone.URL+"/v1/list_classes") || !strings.Contains(said, "fadodb serve FILE") {
		t.Errorf("no server: exit %d, printed %q, said %q", code, out, said)
	}
}

// A server with a certificate the machine does not know is known by
// the certificate the tool is given, and by no other.
func TestExecKnowsAServerByItsCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, key, _ := tool("key", "new", "olga", "--db", path)
	keyFile := filepath.Join(t.TempDir(), "olga.key")
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FADODB_KEY", "")
	certFile, certKey, _ := certificate(t)
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0", "--tls-cert", certFile, "--tls-key", certKey)
	t.Setenv("FADODB_URL", "https://"+addr)

	if code, _, said := tool("exec", "whoami", "--key-file", keyFile); code != exitNoAnswer || !strings.Contains(said, "certificate") {
		t.Errorf("a certificate the machine does not know: exit %d, said %q", code, said)
	}
	code, out, said := tool("exec", "whoami", "--key-file", keyFile, "--ca", certFile)
	if code != exitDone || !strings.Contains(out, `"id": "olga"`) {
		t.Errorf("with the certificate: exit %d, printed %q, said %q", code, out, said)
	}
	other, _, _ := certificate(t)
	if code, _, said := tool("exec", "whoami", "--key-file", keyFile, "--ca", other); code != exitNoAnswer || !strings.Contains(said, "certificate") {
		t.Errorf("with another certificate: exit %d, said %q", code, said)
	}
	if code, _, said := tool("exec", "whoami", "--key-file", keyFile, "--ca", keyFile); code != exitUsage || !strings.Contains(said, "holds no certificate") {
		t.Errorf("a file with no certificate: exit %d, said %q", code, said)
	}
}

// The stream: an event to a line as the server wrote it, comments
// passed over, and three ends.
func TestExecFollowsAStream(t *testing.T) {
	asOlga(t)
	const (
		first  = `{"event_id":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE","class":"Invoice","kind":"created"}`
		second = `{"event_id":"01KA2F8Q7ZJ4M0X3T5V9B1C6DF","class":"Invoice","kind":"transition","name":"submit"}`
	)
	entry := func(w http.ResponseWriter, kind, data string) {
		if kind == "change" {
			id := strings.Split(data, `"`)[3]
			fmt.Fprintf(w, "id: %s\n", id)
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		w.(http.Flusher).Flush()
	}
	open := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": the stream is open\n\n")
		w.(http.Flusher).Flush()
	}
	sent := make(chan struct{})
	server := answeringWith(t, func(w http.ResponseWriter, r *http.Request) {
		open(w)
		entry(w, "change", first)
		io.WriteString(w, ": nothing new\n\n")
		entry(w, "change", second)
		switch r.URL.Path {
		case "/v1/subscribe":
			// The server ends the stream and says why.
			entry(w, "end", `{"error":{"code":"closed","message":"the server is stopping"}}`)
		case "/v1/breaks":
			// The connection breaks in the middle of an entry.
			io.WriteString(w, "id: 01KA2F8Q7ZJ4M0X3T5V9B1C6DG\nevent: change\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		case "/v1/stays":
			sent <- struct{}{}
			<-r.Context().Done()
		}
	})

	const isOpen = "fadodb exec: the stream is open\n"
	code, out, said := tool("exec", "subscribe", `{"classes": ["Invoice"]}`, "--url", server.URL)
	if code != exitRefused || out != first+"\n"+second+"\n" || !strings.HasPrefix(said, isOpen+"{\n") || !strings.Contains(said, `"code": "closed"`) {
		t.Errorf("a stream the server ends: exit %d, printed %q, said %q", code, out, said)
	}
	code, out, said = tool("exec", "breaks", "--url", server.URL)
	if code != exitNoAnswer || out != first+"\n"+second+"\n" || !strings.Contains(said, "the stream broke off") || !strings.Contains(said, `"after"`) {
		t.Errorf("a stream that breaks: exit %d, printed %q, said %q", code, out, said)
	}

	// The caller closes a stream by interrupting the tool, and that is
	// no failure.
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	var printed, told buffer
	ended := make(chan int, 1)
	go func() { ended <- runExec(ctx, []string{"stays", "--url", server.URL}, &printed, &told) }()
	select {
	case <-sent:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream was not opened")
	}
	// Each event is printed as it comes, and not when the stream ends.
	for start := time.Now(); printed.String() != first+"\n"+second+"\n"; time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			t.Fatalf("an open stream printed %q", printed.String())
		}
	}
	interrupt()
	select {
	case code := <-ended:
		if code != exitDone || told.String() != isOpen {
			t.Errorf("a stream its caller closes: exit %d, said %q", code, told.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the tool did not end when it was interrupted")
	}
}

// An export: the documents of query and the rows of sql as CSV.
func TestExecPrintsCSV(t *testing.T) {
	asOlga(t)
	server := answeringWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/query":
			io.WriteString(w, `{"documents":[`+
				`{"class":"Invoice","id":"FV/2026/11/0041","state":"approved","version":3,"values":{"supplier":"Paper Mill, \"North\"","total":1250.50,"paid":true,"note":null,"lines":[{"sku":"A4", "qty":2}]}},`+
				`{"class":"Invoice","id":"FV/2026/11/0042","state":"review","version":2,"values":{"supplier":"Ink & Son","total":840.00,"due":"2026-12-15","net":"840.00 PLN","where":{"city":"Gdańsk"}}}`+
				`]}`)
		case "/v1/sql":
			io.WriteString(w, `{"columns":["id","total","note"],"rows":[["FV/2026/11/0041",125050,null],["FV/2026/11/0042",84000,"two\nlines"]]}`)
		case "/v1/count":
			io.WriteString(w, `{"documents":[]}`)
		}
	})
	// With no fields asked for: every variable, in the order met.
	code, out, said := tool("exec", "query", `{"class": "Invoice"}`, "--csv", "--url", server.URL)
	want := "id,state,supplier,total,paid,note,lines,due,net,where\n" +
		"FV/2026/11/0041,approved,\"Paper Mill, \"\"North\"\"\",1250.50,true,,\"[{\"\"sku\"\":\"\"A4\"\",\"\"qty\"\":2}]\",,,\n" +
		"FV/2026/11/0042,review,Ink & Son,840.00,,,,2026-12-15,840.00 PLN,\"{\"\"city\"\":\"\"Gdańsk\"\"}\"\n"
	if code != exitDone || out != want || said != "" {
		t.Errorf("query as CSV: exit %d, said %q, printed\n%s\nwant\n%s", code, said, out, want)
	}
	// With fields: those, in the order asked.
	code, out, _ = tool("exec", "query", `{"class": "Invoice", "fields": ["total", "supplier", "due"]}`, "--csv", "--url", server.URL)
	want = "id,state,total,supplier,due\n" +
		"FV/2026/11/0041,approved,1250.50,\"Paper Mill, \"\"North\"\"\",\n" +
		"FV/2026/11/0042,review,840.00,Ink & Son,2026-12-15\n"
	if code != exitDone || out != want {
		t.Errorf("query as CSV with fields: exit %d, printed\n%s\nwant\n%s", code, out, want)
	}
	code, out, _ = tool("exec", "sql", `{"statement": "SELECT id, total, note FROM Invoice"}`, "--csv", "--url", server.URL)
	want = "id,total,note\nFV/2026/11/0041,125050,\nFV/2026/11/0042,84000,\"two\nlines\"\n"
	if code != exitDone || out != want {
		t.Errorf("sql as CSV: exit %d, printed\n%s\nwant\n%s", code, out, want)
	}
	// A refusal is no line of an export.
	refusing := answeringWith(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":{"code":"forbidden","message":"olga may not run SQL"}}`)
	})
	if code, out, said := tool("exec", "sql", `{"statement": "SELECT 1"}`, "--csv", "--url", refusing.URL); code != exitRefused || out != "" || !strings.Contains(said, "forbidden") {
		t.Errorf("a refused export: exit %d, printed %q, said %q", code, out, said)
	}
}

// The help comes from the table of the commands, so it has every
// command, each member of each, and nothing a command only refuses.
func TestExecHelpIsTheTable(t *testing.T) {
	code, out, said := tool("exec", "help")
	if code != exitDone || said != "" {
		t.Fatalf("exit %d, said %q", code, said)
	}
	for _, c := range fado.Commands() {
		if !strings.Contains(out, "\n  "+c.Name+" ") || !strings.Contains(out, c.Does) {
			t.Errorf("the list of the commands lacks %s", c.Name)
		}
		if !commandName(c.Name) {
			t.Errorf("the tool would not send %s: it does not take its name for one of a command", c.Name)
		}
		code, one, said := tool("exec", "help", c.Name)
		if code != exitDone || said != "" {
			t.Errorf("help %s: exit %d, said %q", c.Name, code, said)
			continue
		}
		if !strings.HasPrefix(one, c.Name+" - "+c.Does+"\n") || !strings.Contains(one, "the call "+c.Call+".") {
			t.Errorf("help %s begins\n%s", c.Name, one)
		}
		for _, f := range c.Fields {
			has := strings.Contains(one, "\n  "+f.Name+" ") && strings.Contains(one, f.Does)
			if has == f.Refused {
				t.Errorf("help %s and its member %s, which the command only refuses: %v", c.Name, f.Name, f.Refused)
			}
			if f.Required && !f.Refused && !strings.Contains(one, "needed  "+f.Does) {
				t.Errorf("help %s does not say that %s is needed", c.Name, f.Name)
			}
		}
		for _, m := range c.Answer {
			if !strings.Contains(one, m.Does) {
				t.Errorf("help %s lacks the member %s of its answer", c.Name, m.Name)
			}
		}
		if c.Stream != strings.Contains(one, "Its answer is a stream") {
			t.Errorf("help %s and the stream", c.Name)
		}
	}
	if code, out, said := tool("exec", "help", "flow"); code != exitUsage || out != "" || !strings.Contains(said, `knows no command "flow"`) {
		t.Errorf("help of a command there is none of: exit %d, printed %q, said %q", code, out, said)
	}
	if code, _, said := tool("exec", "--help"); code != 0 || !strings.Contains(said, "fadodb exec COMMAND [OBJECT] [flags]") {
		t.Errorf("--help: exit %d, said %q", code, said)
	}
	// The tool's own help names the command.
	if _, out, _ := tool("help"); !strings.Contains(out, "fadodb exec COMMAND") {
		t.Error("the help of the tool does not name exec")
	}
}

// A cell of an export holds a value as the server wrote it.
func TestACellOfAnExport(t *testing.T) {
	for value, want := range map[string]string{
		`"Paper Mill"`:                   "Paper Mill",
		`"a \"b\" \n c"`:                 "a \"b\" \n c",
		`"=SUM(A1:A9)"`:                  "=SUM(A1:A9)",
		`1250.50`:                        "1250.50",
		`12345678901234567890.123456789`: "12345678901234567890.123456789",
		`true`:                           "true",
		`false`:                          "false",
		`null`:                           "",
		``:                               "",
		`[1, 2,  3]`:                     "[1,2,3]",
		`{"a": {"b": 1}}`:                `{"a":{"b":1}}`,
	} {
		if got := cell([]byte(value)); got != want {
			t.Errorf("cell(%s) = %q, want %q", value, got, want)
		}
	}
	var out bytes.Buffer
	if err := writeCSV(&out, "query", []byte(`{}`), []byte(`{"documents":[]}`)); err != nil || out.String() != "id,state\n" {
		t.Errorf("no documents: %q, %v", out.String(), err)
	}
}
