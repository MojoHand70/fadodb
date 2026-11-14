package main

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado/httpdoor"
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
	chapter := string(text)

	// Every flag of the server is in the chapter.
	for _, flag := range []string{"--listen", "--tls-cert", "--tls-key", "--plain-http"} {
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
}
