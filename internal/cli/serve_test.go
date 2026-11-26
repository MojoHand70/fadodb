package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
	"github.com/MojoHand70/fadodb/internal/server"
)

// requestOfTheChapter is the request whose line the chapter on serving
// shows.
var requestOfTheChapter = httpdoor.Request{
	At: time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC), Took: 412 * time.Microsecond,
	Command: "transition", User: "ap-agent", Status: 409, Code: "guard_false",
}

// buffer is an output that a server writes while the test reads it.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// tool runs one command of the tool and answers its exit code and what
// it wrote.
func tool(args ...string) (code int, out, said string) {
	var stdout, stderr bytes.Buffer
	code = run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// serving starts "fadodb serve" with the arguments, and answers where
// it listens, what it writes and how to stop it; stopping answers its
// exit code.
func serving(t *testing.T, args ...string) (addr string, said *buffer, stop func() int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	said = &buffer{}
	ready := make(chan string, 1)
	done := make(chan int, 1)
	go func() {
		done <- runServe(ctx, args, io.Discard, said, func(addr string) { ready <- addr })
	}()
	var once sync.Once
	code := -1
	stop = func() int {
		once.Do(func() {
			cancel()
			code = <-done
		})
		return code
	}
	t.Cleanup(func() { stop() })
	select {
	case addr = <-ready:
	case code := <-done:
		done <- code
		t.Fatalf("the server ended with %d before it listened:\n%s", code, said)
	case <-time.After(10 * time.Second):
		t.Fatalf("the server did not listen:\n%s", said)
	}
	return addr, said, stop
}

// ask sends a request to a server and answers the status and the body.
func ask(t *testing.T, client *http.Client, method, url, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(text))
}

// The shortest way to a working server (ADR-0059, item 1): four
// commands, and a database that answers a key over HTTP.
func TestFromNothingToAServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")

	// A database is made by init, and by nothing else.
	if code, _, said := tool("serve", path); code != 1 || !strings.Contains(said, "there is no database at "+path) || !strings.Contains(said, "fadodb init "+path) {
		t.Fatalf("serving a database that is not there: exit %d\n%s", code, said)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("serve made a database: %v", err)
	}
	if code, _, said := tool("user", "add", "olga", "--kind", "person", "--db", path); code != 1 || !strings.Contains(said, "fadodb init") {
		t.Errorf("a user in a database that is not there: exit %d\n%s", code, said)
	}
	if code, out, said := tool("init", path); code != 0 || !strings.Contains(out, "made the database "+path) || !strings.Contains(said, "fadodb user add") {
		t.Fatalf("init: exit %d\n%s%s", code, out, said)
	}
	if code, _, said := tool("init", path); code != 1 || !strings.Contains(said, "exists") {
		t.Errorf("init where a database is: exit %d\n%s", code, said)
	}

	// The first administrator, and her key, made on the machine. The
	// flags stand before or after the id.
	if code, out, said := tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--name", "Olga Nowak", "--db", path); code != 0 ||
		!strings.Contains(out, "made the user olga, a person, of the system groups administrators") {
		t.Fatalf("user add: exit %d\n%s%s", code, out, said)
	}
	if code, _, said := tool("user", "add", "--db", path, "--kind", "robot", "r2"); code != 1 || !strings.Contains(said, "person, agent or service") {
		t.Errorf("a user of a kind that is none: exit %d\n%s", code, said)
	}
	code, key, said := tool("key", "new", "olga", "--label", "laptop", "--db", path)
	key = strings.TrimSpace(key)
	if code != 0 || !strings.HasPrefix(key, "fado_") || strings.ContainsAny(key, " \n") || !strings.Contains(said, "shown this once") || strings.Contains(said, key) {
		t.Fatalf("key new: exit %d\n%q\n%s", code, key, said)
	}
	if code, _, said := tool("key", "new", "eve", "--db", path); code != 1 || !strings.Contains(said, "no user") {
		t.Errorf("a key for a user that is none: exit %d\n%s", code, said)
	}

	// Served, told nothing but a free port: on this machine only.
	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	base := "http://" + addr
	if !strings.Contains(log.String(), "serving "+path+" at http://127.0.0.1:") {
		t.Errorf("the server says:\n%s", log)
	}
	client := &http.Client{Timeout: 10 * time.Second}

	// Its health, for anybody.
	if status, body := ask(t, client, "GET", base+"/v1/health", "", ""); status != 200 || body != `{"ok":true,"version":"dev"}` {
		t.Errorf("health: %d %s", status, body)
	}
	if status, _ := ask(t, client, "POST", base+"/v1/health", "", "{}"); status != 405 {
		t.Errorf("health asked with POST: %d", status)
	}

	// Olga, with her key, asks who she is, makes the agent and gives it
	// a key.
	if status, body := ask(t, client, "POST", base+"/v1/whoami", key, "{}"); status != 200 || !strings.Contains(body, `"id":"olga"`) || !strings.Contains(body, `"name":"Olga Nowak"`) {
		t.Fatalf("whoami: %d %s", status, body)
	}
	if status, body := ask(t, client, "POST", base+"/v1/create_user", key, `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`); status != 200 {
		t.Fatalf("create_user: %d %s", status, body)
	}
	if status, body := ask(t, client, "POST", base+"/v1/whoami", "", "{}"); status != 401 || !strings.Contains(body, "unauthenticated") {
		t.Errorf("a request without a key: %d %s", status, body)
	}

	// While it serves, the server holds the database alone (item 7):
	// the tool on the machine is refused and told who holds it.
	if code, _, said := tool("user", "add", "eve", "--kind", "person", "--db", path); code != 1 || !strings.Contains(said, "is held by fadodb serve (process ") || !strings.Contains(said, "send it the command instead") {
		t.Errorf("the tool on a served database: exit %d\n%s", code, said)
	}
	if code, _, said := tool("serve", path, "--listen", "127.0.0.1:0"); code != 1 || !strings.Contains(said, "is held by fadodb serve") {
		t.Errorf("a second server on the database: exit %d\n%s", code, said)
	}
	if code, _, said := tool("check", path); code != 1 || !strings.Contains(said, "is held by fadodb serve") {
		t.Errorf("the check of a served database: exit %d\n%s", code, said)
	}

	// Told to stop, it ends cleanly and gives the database back.
	if code := stop(); code != 0 {
		t.Errorf("the server ended with %d:\n%s", code, log)
	}
	said = log.String()
	if !strings.Contains(said, "stopping") || !strings.Contains(said, "stopped") {
		t.Errorf("the server did not say that it stopped:\n%s", said)
	}
	if code, out, said := tool("user", "add", "eve", "--kind", "person", "--db", path); code != 0 {
		t.Errorf("the tool, after the server stopped: exit %d\n%s%s", code, out, said)
	}

	// One line for each request (item 9): the command, the user, the
	// status, the code of a refusal and how long. Never a key.
	for _, want := range []string{
		`"command":"whoami","user":"olga","status":200,"ms":`,
		`"command":"create_user","user":"olga","status":200,"ms":`,
		`"command":"whoami","user":"","status":401,"code":"unauthenticated","ms":`,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the log has no line with %s:\n%s", want, said)
		}
	}
	if strings.Contains(said, key) || strings.Contains(said, "finance") {
		t.Errorf("the log holds a key or a value:\n%s", said)
	}
}

// A key never travels in the clear by accident (ADR-0059, item 3).
func TestWhereTheServerListens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	for _, c := range []struct {
		listen string
		only   bool
	}{
		{"127.0.0.1:8080", true}, {"localhost:8080", true}, {"[::1]:8080", true}, {"127.8.9.10:1", true},
		{":8080", false}, {"0.0.0.0:8080", false}, {"[::]:8080", false}, {"192.168.1.10:8080", false}, {"example.com:8080", false}, {"8080", false},
	} {
		if got := thisMachineOnly(c.listen); got != c.only {
			t.Errorf("%s is reached from this machine only: %v, want %v", c.listen, got, c.only)
		}
	}

	// Beyond this machine without a certificate: refused before
	// anything listens.
	code, _, said := tool("serve", path, "--listen", "0.0.0.0:0")
	if code != 2 || !strings.Contains(said, "would send every key in the clear") || !strings.Contains(said, "--tls-cert") || !strings.Contains(said, "--plain-http") {
		t.Errorf("listening beyond the machine in the clear: exit %d\n%s", code, said)
	}
	if code, _, said := tool("serve", path, "--tls-cert", "cert.pem"); code != 2 || !strings.Contains(said, "both --tls-cert and --tls-key") {
		t.Errorf("half a certificate: exit %d\n%s", code, said)
	}
	if code, _, said := tool("serve", path, "--plain-http", "--tls-cert", "c", "--tls-key", "k"); code != 2 {
		t.Errorf("in the clear and with a certificate: exit %d\n%s", code, said)
	}
	if code, _, said := tool("serve"); code != 2 || !strings.Contains(said, "give the file of the database") {
		t.Errorf("serve of nothing: exit %d\n%s", code, said)
	}
	if code, _, said := tool("serve", "--help"); code != 0 || !strings.Contains(said, "fadodb serve - serve a database over HTTP") {
		t.Errorf("serve --help: exit %d\n%s", code, said)
	}

	// With the operator's word that something in front encrypts, it
	// listens beyond the machine in the clear.
	addr, said2, stop := serving(t, path, "--listen", "0.0.0.0:0", "--plain-http")
	_, port, _ := net.SplitHostPort(addr)
	client := &http.Client{Timeout: 10 * time.Second}
	if status, _ := ask(t, client, "GET", "http://127.0.0.1:"+port+"/v1/health", "", ""); status != 200 {
		t.Errorf("health of a server that listens beyond the machine: %d", status)
	}
	// A database with no user says so, and how to make the first.
	if !strings.Contains(said2.String(), "has no user yet") {
		t.Errorf("a server of a database without users says:\n%s", said2)
	}
	stop()

	// With a certificate it listens beyond the machine, encrypted.
	certFile, keyFile, pool := certificate(t)
	addr, _, stop = serving(t, path, "--listen", "0.0.0.0:0", "--tls-cert", certFile, "--tls-key", keyFile)
	_, port, _ = net.SplitHostPort(addr)
	secure := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	if status, body := ask(t, secure, "GET", "https://127.0.0.1:"+port+"/v1/health", "", ""); status != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("health over TLS: %d %s", status, body)
	}
	stop()
}

// certificate makes a certificate for 127.0.0.1 that signs itself, in
// two files, and the pool a client trusts it by.
func certificate(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fadodb test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

// The chapter of the manual on serving a database says what the tool
// does: its flags, where it listens when told nothing, a line of its
// log and the answer of its health.
func TestTheChapterOnServing(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "serving-a-database.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	chapter := strings.ReplaceAll(string(text), "\r\n", "\n")

	// Every flag of the server is in the chapter.
	for _, flag := range []string{"--listen", "--tls-cert", "--tls-key", "--plain-http", "--settings", "--secrets-dir"} {
		if !strings.Contains(serveUsage, flag) {
			t.Errorf("the test names the flag %s, which the server does not have", flag)
		}
		if !strings.Contains(chapter, "`"+flag) {
			t.Errorf("the chapter does not have the flag %s", flag)
		}
	}
	if !strings.Contains(chapter, "`"+defaultListen+"`") {
		t.Errorf("the chapter does not say that the server listens at %s when told nothing", defaultListen)
	}
	if !strings.Contains(chapter, "up to ten seconds") || stopWithin != 10*time.Second {
		t.Errorf("the chapter and the server differ on how long a stop waits: %s", stopWithin)
	}
	// The four commands, as the chapter writes them, are commands of
	// the tool.
	for _, line := range []string{
		"fadodb init acme.fado",
		"fadodb user add olga --kind person --system administrators --db acme.fado",
		"fadodb key new olga --label laptop --db acme.fado",
		"fadodb serve acme.fado",
	} {
		if !strings.Contains(chapter, line+"\n") {
			t.Errorf("the chapter does not have the command %q", line)
		}
	}

	// A line of the log, as the server writes it.
	var out bytes.Buffer
	log := &requestLog{w: &out}
	log.write(requestOfTheChapter)
	if line := strings.TrimSpace(out.String()); !strings.Contains(chapter, "<!-- a line of the log -->\n```jsonc\n"+line+"\n```") {
		t.Errorf("the chapter does not show the line of the log the server writes:\n%s", line)
	}
	// A failure in the engine is said in the log, with its words.
	out.Reset()
	failed := requestOfTheChapter
	failed.Status, failed.Code, failed.Err = 500, "internal", io.ErrUnexpectedEOF
	log.write(failed)
	if !strings.Contains(out.String(), `"code":"internal","ms":0.412,"error":"unexpected EOF"}`) {
		t.Errorf("the line of a failure is %s", out.String())
	}

	// The answer of the health, which TestFromNothingToAServer holds to
	// the server.
	if !strings.Contains(chapter, "```jsonc\n{\"ok\":true,\"version\":\"dev\"}\n```") {
		t.Error("the chapter does not show the answer of the health")
	}

	// The settings the chapter shows are those of the record, and the
	// server reads them.
	if got := blockOfTheChapter(t, "the settings"); got != settingsOfTheRecord {
		t.Errorf("the settings of the chapter are not those of ADR-0059:\n%s", got)
	}
	// Every member a file of settings may have has its row, with what
	// the server does when the member is not given.
	rows := between(chapter, "<!-- the members of the settings -->\n", "\n\n")
	usual := map[string]string{
		"limits.request_bytes":       strconv.Itoa(server.DefaultRequestBytes),
		"limits.command_seconds":     strconv.Itoa(int(server.DefaultCommandTime.Seconds())),
		"limits.commands":            strconv.Itoa(server.DefaultCommands),
		"limits.streams":             "256",
		"outbound.hosts":             "none",
		"outbound.connections":       "none",
		"inbound.message_bytes":      strconv.Itoa(server.DefaultMessageBytes),
		"inbound.messages":           strconv.Itoa(server.DefaultMessages),
		"url":                        "people sign in on this machine only",
		"proxies":                    "none: the caller is the address of the connection",
		"sessions.idle_hours":        "8",
		"sessions.days":              "30",
		"grants.idle_days":           "30",
		"signon.NAME.issuer":         "no company sign-on",
		"signon.NAME.client_id":      "none",
		"signon.NAME.label":          "Sign in with NAME",
		"signon.NAME.company_emails": "false",
		"analytics.memory":           defaultEngineMemory,
		"analytics.threads":          "one for each core",
		"timezone":                   "the database keeps the one it has, UTC when it was never given one",
	}
	var all []string
	for _, m := range outboundMembers {
		all = append(all, "outbound."+m)
	}
	for _, m := range limitMembers {
		all = append(all, "limits."+m)
	}
	for _, m := range inboundMembers {
		all = append(all, "inbound."+m)
	}
	all = append(all, "url", "proxies")
	for _, m := range sessionMembers {
		all = append(all, "sessions."+m)
	}
	for _, m := range grantMembers {
		all = append(all, "grants."+m)
	}
	for _, m := range signonMembers {
		all = append(all, "signon.NAME."+m)
	}
	for _, m := range engineMembers {
		all = append(all, "analytics."+m)
	}
	all = append(all, "timezone")
	if len(all) != len(usual) {
		t.Errorf("the test knows what is usual for %d members, and a file of settings may have %d", len(usual), len(all))
	}
	for _, member := range all {
		found := false
		for _, row := range strings.Split(rows, "\n") {
			if cells := strings.Split(row, "|"); len(cells) >= 5 && strings.TrimSpace(cells[1]) == "`"+member+"`" {
				found = true
				if !strings.HasPrefix(strings.TrimSpace(cells[3]), usual[member]) {
					t.Errorf("the chapter says of %s, when it is not given: %s; the server has %s", member, strings.TrimSpace(cells[3]), usual[member])
				}
			}
		}
		if !found {
			t.Errorf("the chapter has no row for the setting %s", member)
		}
		name := member[strings.LastIndexByte(member, '.')+1:]
		if !strings.Contains(serveUsage, `"`+name+`"`) {
			t.Errorf("the help of the server does not show the setting %s", member)
		}
	}
	// The settings the help of the server shows are read too.
	if _, after, found := strings.Cut(serveUsage, "\n  {\n"); !found {
		t.Error("the help of the server shows no settings")
	} else if example, _, _ := strings.Cut(after, "\n  }\n"); true {
		if _, err := parseSettings([]byte("{" + example + "}")); err != nil {
			t.Errorf("the settings the help of the server shows: %v", err)
		}
	}

	// Where a secret is, as the server looks for it.
	where := between(chapter, "<!-- where a secret is -->\n", "\n\n")
	if !strings.Contains(where, "`"+secretFile("books", "token")+"`") || !strings.Contains(where, "`"+secretVariable("books", "token")+"`") {
		t.Errorf("the chapter does not say where the secret token of books is: %s and %s\n%s", secretFile("books", "token"), secretVariable("books", "token"), where)
	}
	if !strings.Contains(serveUsage, secretVariable("slack", "token")) || !strings.Contains(serveUsage, "DIR/"+secretFile("slack", "token")) {
		t.Error("the help of the server does not say where the secret token of slack is")
	}
	if !strings.Contains(chapter, "at\n  most 64 KiB") || maxSecret != 64<<10 {
		t.Errorf("the chapter and the server differ on the longest secret: %d", maxSecret)
	}

	// What the server says when it starts, for the settings of the
	// chapter's case.
	told, err := parseSettings([]byte(blockOfTheChapter(t, "the settings of the case")))
	if err != nil {
		t.Fatal(err)
	}
	if said := blockOfTheChapter(t, "what it says when it starts"); said != "fadodb serve: serving acme.fado at http://"+defaultListen+"\nfadodb serve: people sign in at http://"+defaultListen+"/auth/signin\nfadodb serve: agents connect at http://"+defaultListen+"/mcp\nfadodb serve: "+told.mayCall() {
		t.Errorf("the chapter shows a server that says:\n%s\nand the server says of the same settings:\n%s", said, told.mayCall())
	}
}

// The chapter lists the counts the server writes, each with its kind and
// what it says, and shows a part of the page as the server writes it.
func TestTheChapterOnTheCounts(t *testing.T) {
	var rows []string
	for _, f := range families {
		rows = append(rows, "| `"+f.name+"` | "+f.kind+" | "+f.help+" |")
	}
	table := strings.Split(blockAfter(t, "<!-- the counts -->\n| Count | Kind | What it says |\n|---|---|---|\n", "\n\n"), "\n")
	if strings.Join(table, "\n") != strings.Join(rows, "\n") {
		t.Errorf("the chapter lists the counts\n%s\n\nand the server writes\n%s", strings.Join(table, "\n"), strings.Join(rows, "\n"))
	}
	if got := blockOfTheChapter(t, "the counts are read"); !strings.HasPrefix(got, "GET "+metricsPath+"\nAuthorization: Bearer fado_") {
		t.Errorf("the chapter asks for the counts with:\n%s", got)
	}
	// The part of the page the chapter shows, for the requests and the
	// queue it names.
	started := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	m := newMetrics("dev", started)
	m.record(httpdoor.Request{Command: "transition", Status: 200, Took: 60 * time.Microsecond})
	m.record(httpdoor.Request{Command: "transition", Status: 200, Took: 40 * time.Microsecond})
	m.record(httpdoor.Request{Command: "transition", Status: 409, Took: 30 * time.Microsecond})
	var page bytes.Buffer
	m.write(&page, 3, fado.OutboxCounts{Due: 2, Waiting: 5, Dead: 1, OldestDue: started}, 0, 0, started.Add(time.Minute))
	shown := strings.Split(blockOfTheChapter(t, "a part of the counts"), "\n")
	if len(shown) < 8 {
		t.Errorf("the chapter shows %d lines of the counts", len(shown))
	}
	for _, line := range shown {
		if !strings.Contains(page.String(), line+"\n") {
			t.Errorf("the chapter shows the line %q, which the server does not write", line)
		}
	}
	// The help of the server names the address.
	if !strings.Contains(serveUsage, "GET  "+metricsPath) {
		t.Error("the help of the server does not name the address of the counts")
	}
}

// blockAfter is what stands in the chapter on serving after a mark, up
// to an end.
func blockAfter(t *testing.T, from, to string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "serving-a-database.md"))
	if err != nil {
		t.Fatal(err)
	}
	chapter := strings.ReplaceAll(string(text), "\r\n", "\n")
	if !strings.Contains(chapter, from) {
		t.Fatalf("the chapter has no %q", from)
	}
	return between(chapter, from, to)
}

// between is what stands in the chapter between two marks.
func between(chapter, from, to string) string {
	_, after, _ := strings.Cut(chapter, from)
	block, _, _ := strings.Cut(after, to)
	return block
}

// fourInvoices makes a database with four invoices and Olga, who may
// run SQL and read the counts, and answers where it is and her key. A
// statement over the four, joined sixteen times, reads four billion
// rows: slowStatement, which does not end unless it is stopped.
func fourInvoices(t *testing.T) (path, key string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	db, err := fado.Open(path, fado.Options{Users: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.DefineClass(ctx, []byte(`{"name": "Invoice", "variables": {"supplier": {"type": "text"}},
 "states": {"initial": "draft", "list": ["draft"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), fado.As(fado.Principal{ID: "setup", System: []string{fado.GroupDesigners}})); err != nil {
		t.Fatal(err)
	}
	for _, supplier := range []string{"Paper Mill", "Ink and Son", "Zażółć", "Staples"} {
		if _, err := db.Create(ctx, "Invoice", fado.Values{"supplier": supplier}, fado.As(fado.Principal{ID: "bob"})); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if code, _, said := tool("user", "add", "olga", "--kind", "person", "--system", "analysts", "--system", "administrators", "--db", path); code != 0 {
		t.Fatal(said)
	}
	_, key, _ = tool("key", "new", "olga", "--db", path)
	return path, strings.TrimSpace(key)
}

func slowStatement() string {
	var from []string
	for i := 0; i < 16; i++ {
		from = append(from, "Invoice t"+strconv.Itoa(i))
	}
	return `{"statement": "SELECT count(*) FROM ` + strings.Join(from, ", ") + `"}`
}

// A statement that runs longer than the server lets a command run is
// stopped and answered timeout, as any other command is, and the log
// says the same: nothing is wrong with what the caller sent (ADR-0059,
// item 2).
func TestAStatementThatRunsTooLong(t *testing.T) {
	path, key := fourInvoices(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"limits": {"command_seconds": 0.05}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0", "--settings", settings)
	client := &http.Client{Timeout: 30 * time.Second}
	status, body := ask(t, client, "POST", "http://"+addr+"/v1/sql", key, slowStatement())
	if status != http.StatusGatewayTimeout || !strings.Contains(body, `"code":"timeout"`) || !strings.Contains(body, "50ms") {
		t.Errorf("a statement that runs longer than the server lets one run: %d %s", status, body)
	}
	// The next statement runs, and one that is wrong is still the
	// caller's to mend.
	if status, body := ask(t, client, "POST", "http://"+addr+"/v1/sql", key, `{"statement": "SELECT count(*) AS n FROM Invoice"}`); status != 200 || !strings.Contains(body, `"rows":[[4]]`) {
		t.Errorf("the statement after one that was stopped: %d %s", status, body)
	}
	if status, body := ask(t, client, "POST", "http://"+addr+"/v1/sql", key, `{"statement": "SELECT nothing FROM Invoice"}`); status != 422 || !strings.Contains(body, `"code":"bad_value"`) {
		t.Errorf("a statement that names no column: %d %s", status, body)
	}
	stop()
	if !strings.Contains(log.String(), `"command":"sql","user":"olga","status":504,"code":"timeout"`) {
		t.Errorf("the log of a statement that ran too long:\n%s", log)
	}
}

// A statement whose caller leaves before it ends is stopped, and the
// log and the counts say that the caller left, with the status 499: the
// storage did not fail (ADR-0059, item 2).
func TestAStatementWhoseCallerLeft(t *testing.T) {
	path, key := fourInvoices(t)
	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	base := "http://" + addr

	// Ann's assistant waits a fifth of a second for its answer and
	// leaves.
	req, err := http.NewRequest("POST", base+"/v1/sql", strings.NewReader(slowStatement()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if resp, err := (&http.Client{Timeout: 200 * time.Millisecond}).Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("a statement over four billion rows was answered: %d", resp.StatusCode)
	}

	// The server stops the statement and writes its line; the next
	// statement runs.
	client := &http.Client{Timeout: 30 * time.Second}
	const line = `"command":"sql","user":"olga","status":499,"code":"caller_left"`
	for until := time.Now().Add(20 * time.Second); !strings.Contains(log.String(), line); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(until) {
			t.Fatalf("the log has no line of a caller that left:\n%s", log)
		}
	}
	if status, body := ask(t, client, "POST", base+"/v1/sql", key, `{"statement": "SELECT count(*) AS n FROM Invoice"}`); status != 200 || !strings.Contains(body, `"rows":[[4]]`) {
		t.Errorf("the statement after one whose caller left: %d %s", status, body)
	}
	// It is counted under its own status, and no failure is.
	status, page := ask(t, client, "GET", base+metricsPath, key, "")
	if status != 200 || !strings.Contains(page, `fadodb_requests_total{command="sql",status="499"} 1`+"\n") || strings.Contains(page, `command="sql",status="500"`) {
		t.Errorf("the counts after a caller left: %d\n%s", status, page)
	}
	stop()
	if strings.Contains(log.String(), `"code":"storage"`) || strings.Contains(log.String(), `"status":500`) {
		t.Errorf("the log names a failure where a caller left:\n%s", log)
	}
	// The chapter on serving shows such a line.
	if shown := blockAfter(t, "<!-- a caller that left -->\n```jsonc\n", "\n```"); !strings.Contains(shown, line) {
		t.Errorf("the chapter shows the line of a caller that left as %s", shown)
	}
}
