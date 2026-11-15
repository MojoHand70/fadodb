package httpdoor_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

// people is the host program's own directory in the examples: the token
// each caller sends, and who it is. The engine's own users and keys
// come with a later step; a program that has its people already keeps
// this function for good.
var people = map[string]fado.Principal{
	"token-of-dan":   {ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}},
	"token-of-bob":   {ID: "bob", Kind: fado.ActorPerson, Groups: []string{"sales"}},
	"token-of-agent": {ID: "ap-agent", Kind: fado.ActorAgent, Groups: []string{"finance"}},
}

func who(r *http.Request) (fado.Principal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	p, known := people[token]
	if !ok || !known {
		return fado.Principal{}, errors.New("no such token")
	}
	return p, nil
}

// open makes a database with the invoice class of the examples, and its
// door on a server that listens on this machine.
func open(dir string) (*fado.DB, *httptest.Server) {
	db, err := fado.Open(filepath.Join(dir, "example.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		panic(err)
	}
	door, err := httpdoor.New(db, httpdoor.Options{Who: who})
	if err != nil {
		panic(err)
	}
	return db, httptest.NewServer(door)
}

// send posts a command as the caller whose token it is given, and
// answers the status and the body of the reply.
func send(srv *httptest.Server, token, command, object string) (int, string) {
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/"+command, strings.NewReader(object))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	return resp.StatusCode, strings.TrimSuffix(string(body), "\n")
}

const invoice = `{"definition": {
  "name": "Invoice",
  "variables": {"customer": {"type": "text", "required": true}, "note": {"type": "text"}},
  "states": {"initial": "review", "list": ["review", "approved"]},
  "transitions": [{"name": "approve", "from": "review", "to": "approved"}],
  "access": {"read": "TRUE", "create": "INGROUP(\"sales\")", "update": "TRUE", "delete": "FALSE",
             "transition": {"approve": "INGROUP(\"finance\")"}}
}}`

// A program opens a database and lets it answer the command set over
// HTTP. The program says who sends each request; the door does the
// rest. Every command is POST /v1/<command> with its object as the
// body, and the answer is the body of the reply.
func ExampleNew() {
	dir, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	db, srv := open(dir)
	defer db.Close()
	defer srv.Close()

	// Dan, a designer, defines the class, and Bob creates an invoice
	// under its number. The id travels in the JSON, slashes and all.
	if status, body := send(srv, "token-of-dan", "define_class", invoice); status != 200 {
		panic(body)
	}
	if status, body := send(srv, "token-of-bob", "create", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"customer": "Customer:42"}}`); status != 200 {
		panic(body)
	}

	// Bob may not approve it. The refusal is the engine's, word for
	// word; the status is for the proxies in between.
	fmt.Println(send(srv, "token-of-bob", "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}`))

	// The agent of accounts payable approves it.
	fmt.Println(send(srv, "token-of-agent", "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve",
	  "values": {"note": "matches order PO-118"}, "key": "approve-FV/2026/11/0042"}`))

	// A request that does not say who sends it runs nothing.
	fmt.Println(send(srv, "", "get", `{"class": "Invoice", "id": "FV/2026/11/0042"}`))

	// Output:
	// 403 {"error":{"code":"forbidden","message":"bob may not take approve from review","hint":"the rule of approve: INGROUP(\"finance\")","path":"/access/transition/approve"}}
	// 200 {"document":{"class":"Invoice","id":"FV/2026/11/0042","title":"FV/2026/11/0042","state":"approved","version":2,"class_version":1,"created_at":1794562200,"updated_at":1794562200,"state_entered_at":1794562200,"created_by":"bob","updated_by":"ap-agent","state_entered_by":"ap-agent","state_entered_from":"review","values":{"customer":"Customer:42","note":"matches order PO-118"}}}
	// 401 {"error":{"code":"unauthenticated","message":"the request does not say who sends it, or says it with a credential the server does not know"}}
}

// A program that serves two customers makes a door for each
// (ADR-0056): the same command, sent with the same token, reaches the
// database of the address it was sent to and no other.
func TestADoorForEachDatabase(t *testing.T) {
	acmeDB, acme := open(t.TempDir())
	globexDB, globex := open(t.TempDir())
	t.Cleanup(func() { acme.Close(); globex.Close(); acmeDB.Close(); globexDB.Close() })

	for _, srv := range []*httptest.Server{acme, globex} {
		if status, body := send(srv, "token-of-dan", "define_class", invoice); status != 200 {
			t.Fatal(body)
		}
	}
	if status, body := send(acme, "token-of-bob", "create", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"customer": "Customer:42"}}`); status != 200 {
		t.Fatal(body)
	}
	get := `{"class": "Invoice", "id": "FV/2026/11/0042"}`
	if status, body := send(acme, "token-of-bob", "get", get); status != 200 || !strings.Contains(body, `"customer":"Customer:42"`) {
		t.Errorf("Acme answers %d %s", status, body)
	}
	if status, body := send(globex, "token-of-bob", "get", get); status != 404 || !strings.Contains(body, `"unknown_document"`) {
		t.Errorf("Globex answers for a document of Acme: %d %s", status, body)
	}
}

// The door is made of a *fado.DB, and not without the function that
// says who is calling.
func TestNew(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "test.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := httpdoor.New(db, httpdoor.Options{}); err == nil || !strings.Contains(err.Error(), "Who") {
		t.Errorf("a door without Who: %v", err)
	}
	if _, err := httpdoor.New(nil, httpdoor.Options{Who: who}); err == nil {
		t.Error("a door of no database was made")
	}
	door, err := httpdoor.New(db, httpdoor.Options{Who: who, Limits: httpdoor.Limits{Commands: 8}})
	if err != nil {
		t.Fatal(err)
	}
	want := httpdoor.Limits{RequestBytes: httpdoor.DefaultRequestBytes, CommandTime: httpdoor.DefaultCommandTime, Commands: 8, Streams: httpdoor.DefaultStreams}
	if got := door.Limits(); got != want {
		t.Errorf("the limits are %+v, want %+v", got, want)
	}
	if httpdoor.Status(httpdoor.CodeBusy) != 503 || httpdoor.Status("") != 200 {
		t.Error("Status does not answer the table of ADR-0055")
	}
}

// The door answers the stream of changes of a *fado.DB (ADR-0058): a
// reply that stays open, an entry for each event the caller may read
// of, and a last entry when the program ends its streams.
func TestTheStreamOfChanges(t *testing.T) {
	db, srv := open(t.TempDir())
	defer db.Close()
	defer srv.Close()
	door := srv.Config.Handler.(*httpdoor.Door)
	if status, body := send(srv, "token-of-dan", "define_class", invoice); status != 200 {
		t.Fatalf("define_class: %d %s", status, body)
	}
	listen := func(token string) *bufio.Reader {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/subscribe", strings.NewReader(`{"classes": ["Invoice"]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("the stream was answered with %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		return bufio.NewReader(resp.Body)
	}
	// entry reads the lines of one entry.
	entry := func(in *bufio.Reader) string {
		var b strings.Builder
		for {
			line, err := in.ReadString('\n')
			if err != nil || line == "\n" {
				return b.String()
			}
			b.WriteString(line)
		}
	}
	bob := listen("token-of-bob")
	if got := entry(bob); got != ": the stream is open\n" {
		t.Fatalf("the stream begins with %q", got)
	}
	if n := door.Streams(); n != 1 {
		t.Errorf("the door holds %d streams, want 1", n)
	}
	if status, body := send(srv, "token-of-bob", "create", `{"class": "Invoice", "id": "FV/1", "values": {"customer": "Acme"}}`); status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	got := entry(bob)
	if !strings.HasPrefix(got, "id: ") || !strings.Contains(got, "\nevent: change\ndata: {") || !strings.Contains(got, `"doc_id":"FV/1","kind":"created"`) || !strings.Contains(got, `"actor_id":"bob"`) {
		t.Errorf("the entry of a create is %q", got)
	}
	// The program stops: the door ends the stream, and says why.
	door.EndStreams()
	if got := entry(bob); !strings.HasPrefix(got, "event: end\ndata: {\"error\":{\"code\":\"closed\"") {
		t.Errorf("the last entry is %q", got)
	}
	if got := entry(bob); got != "" {
		t.Errorf("the stream went on after its end: %q", got)
	}
}

// receipt is the class of the manual's chapter on files: a receipt with
// its scan, and a working paper that only accounting writes and sees.
const receipt = `{"definition": {
  "name": "Receipt",
  "title": "\"Receipt for \" & what",
  "variables": {
    "what":  {"type": "text", "required": true},
    "scan":  {"type": "object", "schema": "File"},
    "audit": {"type": "object", "schema": "File"}
  },
  "states": {"initial": "open", "list": ["open", "filed"], "final": ["filed"]},
  "transitions": [{"name": "file", "from": "open", "to": "filed", "guard": "NOT ISBLANK(scan.hash)"}],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "delete": "CREATEDBY() = USER()",
    "transition": {"file": "CREATEDBY() = USER()"}
  },
  "rights": {
    "*": {
      "*":     {"write": "CREATEDBY() = USER()"},
      "audit": {"write": ["ACCOUNTING"], "read": ["ACCOUNTING"]}
    }
  }
}}`

// call sends one request beside the commands and answers the reply.
func call(t *testing.T, srv *httptest.Server, method, token, address, contentType, body string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(text)
}

// The files travel beside the commands (ADR-0055, item 9). Ola puts the
// scan of a taxi receipt and names it on a receipt; whoever may read the
// receipt reads the scan through it, and nobody else learns that it
// exists.
func TestFilesBesideTheCommands(t *testing.T) {
	people["token-of-ola"] = fado.Principal{ID: "ola", Kind: fado.ActorPerson}
	people["token-of-anna"] = fado.Principal{ID: "anna", Kind: fado.ActorPerson, Groups: []string{"ACCOUNTING"}}
	people["token-of-piotr"] = fado.Principal{ID: "piotr", Kind: fado.ActorPerson}
	people["token-of-celina"] = fado.Principal{ID: "celina", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}}
	db, srv := open(t.TempDir())
	t.Cleanup(func() { srv.Close(); db.Close() })
	if status, body := send(srv, "token-of-dan", "define_class", receipt); status != 200 {
		t.Fatal(body)
	}

	// 1. Ola puts the bytes, under a name, and is answered with the
	// facts of the file.
	const scan = "%PDF-1.4 a taxi receipt"
	resp, body := call(t, srv, "POST", "token-of-ola", "/v1/blobs?name=taxi.pdf", "application/pdf", scan)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("the put is answered %d %s", resp.StatusCode, body)
	}
	facts := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), `{"file":`), "}")
	if !strings.HasPrefix(facts, `{"hash":"`) || !strings.HasSuffix(facts, `,"name":"taxi.pdf","media_type":"application/pdf","size":23}`) {
		t.Fatalf("the facts of the file are %s", facts)
	}
	// The hash is the SHA-256 of the bytes, as 64 letters and digits.
	sum := facts[len(`{"hash":"`) : len(`{"hash":"`)+64]

	// 2. She names it on a receipt: the facts go into a command as they
	// came.
	if status, body := send(srv, "token-of-ola", "create", `{"class": "Receipt", "id": "R-7", "values": {"what": "taxi", "scan": `+facts+`}}`); status != 200 {
		t.Fatalf("the create: %d %s", status, body)
	}

	// 3. She reads the scan back through the receipt. A browser is told
	// to save it, whatever kind it was said to be.
	file := "/v1/file?class=Receipt&id=R-7&hash=" + sum
	resp, body = call(t, srv, "GET", "token-of-ola", file, "", "")
	if resp.StatusCode != 200 || body != scan {
		t.Fatalf("the file is answered %d %q", resp.StatusCode, body)
	}
	for header, want := range map[string]string{
		"Content-Type":            "application/pdf",
		"Content-Length":          "23",
		"ETag":                    `"` + sum + `"`,
		"Content-Disposition":     "attachment; filename=taxi.pdf",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-cache",
		"Content-Security-Policy": "sandbox; default-src 'none'",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s is %q, want %q", header, got, want)
		}
	}

	// 4. A caller that holds the bytes already is told they are the
	// same, and is sent nothing.
	resp, body = call(t, srv, "GET", "token-of-ola", file, "", "", "If-None-Match", `"`+sum+`"`)
	if resp.StatusCode != 304 || body != "" {
		t.Errorf("a caller that holds the file is answered %d %q", resp.StatusCode, body)
	}

	// 5. Anna, of accounting, may read the receipt and so its scan.
	// Piotr may not: for him there is no such receipt, with or without
	// the tag of the bytes.
	if resp, body := call(t, srv, "GET", "token-of-anna", file, "", ""); resp.StatusCode != 200 || body != scan {
		t.Errorf("Anna is answered %d %q", resp.StatusCode, body)
	}
	for _, tag := range []string{"", `"` + sum + `"`} {
		resp, body := call(t, srv, "GET", "token-of-piotr", file, "", "", "If-None-Match", tag)
		if resp.StatusCode != 404 || !strings.Contains(body, `"unknown_document"`) {
			t.Errorf("Piotr is answered %d %s", resp.StatusCode, body)
		}
	}
	// Nobody at all learns anything.
	if resp, body := call(t, srv, "GET", "", file, "", ""); resp.StatusCode != 401 || !strings.Contains(body, `"unauthenticated"`) {
		t.Errorf("a caller without a token is answered %d %s", resp.StatusCode, body)
	}

	// A file in a variable the caller may not read is not held, for
	// that caller: Anna attaches a working paper that only accounting
	// sees, and Ola, who reads the receipt, is told there is no such
	// file on it.
	resp, body = call(t, srv, "POST", "token-of-anna", "/v1/blobs?name=audit.txt", "text/plain", "checked against the log")
	if resp.StatusCode != 200 {
		t.Fatalf("Anna's put: %d %s", resp.StatusCode, body)
	}
	paper := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), `{"file":`), "}")
	if status, body := send(srv, "token-of-anna", "set", `{"class": "Receipt", "id": "R-7", "values": {"audit": `+paper+`}}`); status != 200 {
		t.Fatalf("Anna's set: %d %s", status, body)
	}
	audit := "/v1/file?class=Receipt&id=R-7&hash=" + paper[len(`{"hash":"`):len(`{"hash":"`)+64]
	if resp, body := call(t, srv, "GET", "token-of-anna", audit, "", ""); resp.StatusCode != 200 || body != "checked against the log" {
		t.Errorf("Anna, for her working paper, is answered %d %q", resp.StatusCode, body)
	}
	if resp, body := call(t, srv, "GET", "token-of-ola", audit, "", ""); resp.StatusCode != 404 || !strings.Contains(body, `"unknown_blob"`) {
		t.Errorf("Ola, for the working paper she may not read, is answered %d %s", resp.StatusCode, body)
	}

	// 6. The bytes by their hash alone are a content administrator's.
	blob := "/v1/blob?hash=" + sum
	if resp, body := call(t, srv, "GET", "token-of-celina", blob, "", ""); resp.StatusCode != 200 || body != scan {
		t.Errorf("Celina is answered %d %q", resp.StatusCode, body)
	}
	if resp, body := call(t, srv, "GET", "token-of-ola", blob, "", ""); resp.StatusCode != 422 || !strings.Contains(body, `"not_allowed"`) {
		t.Errorf("Ola, by the hash alone, is answered %d %s", resp.StatusCode, body)
	}

	// 7. An earlier version: Ola takes the scan off, and the first
	// version still holds it.
	if status, body := send(srv, "token-of-ola", "set", `{"class": "Receipt", "id": "R-7", "values": {"scan": null}}`); status != 200 {
		t.Fatalf("taking the scan off: %d %s", status, body)
	}
	if resp, body := call(t, srv, "GET", "token-of-ola", file, "", ""); resp.StatusCode != 404 || !strings.Contains(body, `"unknown_blob"`) {
		t.Errorf("the file of a receipt that no longer holds it is answered %d %s", resp.StatusCode, body)
	}
	if resp, body := call(t, srv, "GET", "token-of-ola", file+"&version=1", "", ""); resp.StatusCode != 200 || body != scan {
		t.Errorf("the file of the first version is answered %d %q", resp.StatusCode, body)
	}

	// What is not a request for a file is told so.
	for _, c := range []struct {
		name, method, token, address, contentType, body string
		status                                          int
		says                                            string
	}{
		{"a parameter the address does not take", "GET", "token-of-ola", file + "&inline=1", "", "", 400, `file takes no \"inline\"`},
		{"a parameter given twice", "GET", "token-of-ola", file + "&id=R-8", "", "", 400, "id is given 2 times"},
		{"no hash", "GET", "token-of-ola", "/v1/file?class=Receipt&id=R-7", "", "", 400, "file needs hash"},
		{"a version that is no number", "GET", "token-of-ola", file + "&version=first", "", "", 400, "a whole number from 1"},
		{"a file read with POST", "POST", "token-of-ola", file, "", "", 405, "a file is read with GET"},
		{"bytes put with PUT", "PUT", "token-of-ola", "/v1/blobs?name=taxi.pdf", "application/pdf", scan, 405, "a file is put with POST"},
		{"a form", "POST", "token-of-ola", "/v1/blobs?name=taxi.pdf", "multipart/form-data; boundary=x", "--x--", 400, "the bytes of the file"},
		{"a put with a parameter it does not take", "POST", "token-of-ola", "/v1/blobs?filename=taxi.pdf", "application/pdf", scan, 400, `blobs takes no \"filename\"`},
		{"a put by nobody", "POST", "", "/v1/blobs?name=taxi.pdf", "application/pdf", scan, 401, "unauthenticated"},
		{"bytes nobody put", "GET", "token-of-celina", "/v1/blob?hash=" + strings.Repeat("0", 64), "", "", 404, "unknown_blob"},
	} {
		resp, body := call(t, srv, c.method, c.token, c.address, c.contentType, c.body)
		if resp.StatusCode != c.status || !strings.Contains(body, c.says) || !strings.Contains(body, `{"error":`) {
			t.Errorf("%s: %d %s, want %d and %q", c.name, resp.StatusCode, body, c.status, c.says)
		}
	}
}

// A file larger than the database keeps is refused, and none of it is
// kept.
func TestAFileThatIsTooLarge(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "test.fado"), fado.Options{MaxBlob: 1024})
	if err != nil {
		t.Fatal(err)
	}
	door, err := httpdoor.New(db, httpdoor.Options{Who: who})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(door)
	t.Cleanup(func() { srv.Close(); db.Close() })
	resp, body := call(t, srv, "POST", "token-of-bob", "/v1/blobs?name=big.bin", "application/octet-stream", strings.Repeat("x", 2000))
	if resp.StatusCode != 413 || !strings.Contains(body, `"blob_too_large"`) {
		t.Errorf("a file of 2000 bytes in a database that keeps 1024 is answered %d %s", resp.StatusCode, body)
	}
	if resp, body := call(t, srv, "POST", "token-of-bob", "/v1/blobs?name=small.bin", "application/octet-stream", strings.Repeat("x", 1024)); resp.StatusCode != 200 {
		t.Errorf("a file of the largest size is answered %d %s", resp.StatusCode, body)
	}
}

// service opens a database that keeps its own users and puts its door,
// which asks for a key, on a server that listens on this machine. The
// first administrator, Olga, is made on the machine, by no user
// (ADR-0057, item 7), and her key is answered.
func service(t *testing.T, name string) (*fado.DB, *httptest.Server, string) {
	t.Helper()
	db, err := fado.Open(filepath.Join(t.TempDir(), name+".fado"), fado.Options{Users: true})
	if err != nil {
		t.Fatal(err)
	}
	door, err := httpdoor.New(db, httpdoor.Options{Who: httpdoor.Keys(db)})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(door)
	t.Cleanup(func() { srv.Close(); db.Close() })
	ctx := context.Background()
	machine := fado.As(fado.Principal{System: []string{fado.GroupAdministrators}})
	if _, err := db.CreateUser(ctx, fado.User{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}}, machine); err != nil {
		t.Fatal(err)
	}
	key, err := db.CreateKey(ctx, "olga", "laptop", "", machine)
	if err != nil {
		t.Fatal(err)
	}
	return db, srv, key.Secret
}

// Two customers, each with a database, a door and users of its own
// (ADR-0056, item 4): a key opens the one database it was made in, and
// a user of one is nobody in the other.
func TestAKeyOpensTheDatabaseItWasMadeIn(t *testing.T) {
	_, acme, olgaAtAcme := service(t, "acme")
	_, globex, olgaAtGlobex := service(t, "globex")

	// Olga of Acme asks who she is, with her key.
	status, body := send(acme, olgaAtAcme, "whoami", `{}`)
	if status != 200 || body != `{"user":{"id":"olga","kind":"person","groups":[],"system_groups":["administrators"],"disabled":false}}` {
		t.Fatalf("whoami at Acme: %d %s", status, body)
	}
	// Her key is nobody's at Globex, whose own Olga is another user.
	if status, body := send(globex, olgaAtAcme, "whoami", `{}`); status != 401 || !strings.Contains(body, `"unauthenticated"`) {
		t.Errorf("a key of Acme at the door of Globex: %d %s", status, body)
	}
	if status, _ := send(globex, olgaAtGlobex, "whoami", `{}`); status != 200 {
		t.Errorf("the key of Globex at the door of Globex: %d", status)
	}
	// Nothing else is a credential.
	for what, token := range map[string]string{"no key": "", "a key cut short": olgaAtAcme[:40], "the id of the key alone": olgaAtAcme[:15]} {
		if status, body := send(acme, token, "whoami", `{}`); status != 401 || !strings.Contains(body, `"unauthenticated"`) {
			t.Errorf("%s: %d %s", what, status, body)
		}
	}
	if resp, body := call(t, acme, "POST", "", "/v1/whoami?key="+olgaAtAcme, "application/json", `{}`); resp.StatusCode != 400 && resp.StatusCode != 401 {
		t.Errorf("a key in the address: %d %s", resp.StatusCode, body)
	}

	// Olga makes the agent of accounts payable and gives it a key, over
	// HTTP. The key is in the answer, this once.
	if status, body := send(acme, olgaAtAcme, "create_user", `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`); status != 200 {
		t.Fatalf("create_user: %d %s", status, body)
	}
	status, body = send(acme, olgaAtAcme, "create_key", `{"user": "ap-agent", "label": "invoice matching"}`)
	at := strings.Index(body, `"secret":"`)
	if status != 200 || at < 0 {
		t.Fatalf("create_key: %d %s", status, body)
	}
	agentKey := body[at+len(`"secret":"`):]
	agentKey = agentKey[:strings.Index(agentKey, `"`)]
	if status, body := send(acme, agentKey, "whoami", `{}`); status != 200 || !strings.Contains(body, `"id":"ap-agent","kind":"agent","groups":["finance"]`) {
		t.Errorf("the agent asks who it is: %d %s", status, body)
	}

	// The agent keeps no users: that is an administrator's.
	if status, body := send(acme, agentKey, "create_user", `{"id": "eve", "kind": "person"}`); status != 403 || !strings.Contains(body, `"forbidden"`) {
		t.Errorf("the agent makes a user: %d %s", status, body)
	}
	if status, body := send(acme, agentKey, "get_user", `{"id": "eve"}`); status != 404 || !strings.Contains(body, `"unknown_user"`) {
		t.Errorf("a user that is none: %d %s", status, body)
	}

	// Olga puts the agent among the designers. The key it holds answers
	// with the new groups at the next command, and nothing is sent anew.
	if status, body := send(acme, olgaAtAcme, "set_user", `{"id": "ap-agent", "system_groups": ["designers"]}`); status != 200 {
		t.Fatalf("set_user: %d %s", status, body)
	}
	if status, body := send(acme, agentKey, "define_class", invoice); status != 200 {
		t.Errorf("the agent, now a designer, defines a class: %d %s", status, body)
	}

	// A disabled user is stopped at the next command, and so is a key
	// that was revoked.
	if status, body := send(acme, olgaAtAcme, "set_user", `{"id": "ap-agent", "disabled": true}`); status != 200 {
		t.Fatalf("disabling the agent: %d %s", status, body)
	}
	if status, _ := send(acme, agentKey, "whoami", `{}`); status != 401 {
		t.Errorf("a disabled user is answered %d", status)
	}
	if status, body := send(acme, olgaAtAcme, "set_user", `{"id": "ap-agent", "disabled": false}`); status != 200 {
		t.Fatalf("enabling the agent: %d %s", status, body)
	}
	status, body = send(acme, agentKey, "list_keys", `{}`)
	at = strings.Index(body, `"id":"`)
	if status != 200 || at < 0 {
		t.Fatalf("list_keys: %d %s", status, body)
	}
	id := body[at+len(`"id":"`):]
	id = id[:strings.Index(id, `"`)]
	if status, body := send(acme, agentKey, "revoke_key", `{"key": "`+id+`"}`); status != 200 || body != `{}` {
		t.Errorf("the agent revokes its key: %d %s", status, body)
	}
	if status, _ := send(acme, agentKey, "whoami", `{}`); status != 401 {
		t.Errorf("a revoked key is answered %d", status)
	}
}

// A database whose program says itself who is calling does not offer
// the commands of the users.
func TestADatabaseWithoutUsersOffersNoCommandOfThem(t *testing.T) {
	db, srv := open(t.TempDir())
	t.Cleanup(func() { srv.Close(); db.Close() })
	if status, body := send(srv, "token-of-bob", "whoami", `{}`); status != 404 || !strings.Contains(body, `"unknown_command"`) || !strings.Contains(body, "keeps no users") {
		t.Errorf("whoami: %d %s", status, body)
	}
}

// The description of the commands for other languages, api/openapi.json,
// is held to a door: every address it lists is answered there, with
// the method it names; a member it does not list is refused; and what
// it says a command needs is what the door asks for (ADR-0055, item 10).
func TestTheDescriptionAndTheDoor(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Tags        []string
			Stream      bool `json:"x-fado-stream"`
			Parameters  []struct{ Name string }
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Properties map[string]any
						Required   []string
					}
				}
			} `json:"requestBody"`
		}
	}
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}
	_, srv, key := service(t, "acme")
	// ask sends a request with Olga's key and answers the status and
	// the refusal the reply carries.
	ask := func(method, address, contentType, body string) (int, fado.Error) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+address, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got struct{ Error fado.Error }
		answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = json.Unmarshal(answer, &got)
		return resp.StatusCode, got.Error
	}
	commands := 0
	for address, methods := range doc.Paths {
		for method, op := range methods {
			if len(op.Tags) == 1 && op.Tags[0] == "The server" {
				// Answered by fadodb serve, whose tests hold it.
				continue
			}
			upper := strings.ToUpper(method)
			body, described := op.RequestBody.Content["application/json"]
			if !described {
				// One of the three addresses of the files: it is there,
				// and it takes the parameters the description lists.
				status, e := ask(upper, address+"?no_such_parameter=1", "application/octet-stream", "x")
				if e.Code != "bad_json" || status != 400 || !strings.Contains(e.Message, "no_such_parameter") {
					t.Errorf("%s %s with a parameter it does not have: %d %+v", upper, address, status, e)
				}
				for _, p := range op.Parameters {
					if status, e := ask(upper, address+"?"+p.Name+"=x&"+p.Name+"=y", "application/octet-stream", "x"); e.Code != "bad_json" || !strings.Contains(e.Message, p.Name) {
						t.Errorf("%s %s with %s given twice: %d %+v", upper, address, p.Name, status, e)
					}
				}
				continue
			}
			commands++
			// A command: its address answers, a member the description
			// does not list is refused with its place, and an empty
			// object is refused for the first member it needs.
			if status, e := ask(upper, address, "application/json", `{"no_such_member": 1}`); e.Code != "bad_json" || e.Path != "/no_such_member" || status != 400 {
				t.Errorf("%s %s with a member it does not have: %d %+v", upper, address, status, e)
			}
			for member := range body.Schema.Properties {
				if _, e := ask(upper, address, "application/json", `{"`+member+`": null, "no_such_member": 1}`); e.Path != "/no_such_member" {
					t.Errorf("%s %s does not know the member %s the description lists: %+v", upper, address, member, e)
				}
			}
			if op.Stream {
				continue
			}
			status, e := ask(upper, address, "application/json", `{}`)
			needs := body.Schema.Required
			switch {
			case len(needs) > 0 && (e.Code != "bad_json" || e.Path != "/"+needs[0]):
				t.Errorf("%s %s sent {}: %d %+v, and the description says it needs %v", upper, address, status, e, needs)
			case len(needs) == 0 && (e.Code == "bad_json" || e.Code == "unknown_command"):
				t.Errorf("%s %s sent {}: %d %+v, and the description says it needs nothing", upper, address, status, e)
			}
		}
	}
	if want := len(fado.Commands()); commands != want {
		t.Errorf("the description has %d commands, and the engine %d", commands, want)
	}
}
