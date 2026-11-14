package httpdoor_test

import (
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
	want := httpdoor.Limits{RequestBytes: httpdoor.DefaultRequestBytes, CommandTime: httpdoor.DefaultCommandTime, Commands: 8}
	if got := door.Limits(); got != want {
		t.Errorf("the limits are %+v, want %+v", got, want)
	}
	if httpdoor.Status(httpdoor.CodeBusy) != 503 || httpdoor.Status("") != 200 {
		t.Error("Status does not answer the table of ADR-0055")
	}
}
