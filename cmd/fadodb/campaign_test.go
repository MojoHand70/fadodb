package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// TestServeCampaign measures fadodb serve as it is told nothing more
// than where its database is (ADR-0011, ADR-0055, ADR-0059): a
// transition and a read sent to it over a connection that is kept
// open, by one caller and by sixteen at once, beside the same calls
// made on the same database through the Go API and as commands, and
// beside a request that does nothing, which is what this machine's
// loopback and Go's HTTP cost with no FadoDB in them. The budget is
// that the server adds under 200 µs at p99 to a transition over the Go
// API (ADR-0011), and ADR-0066 says beside what: the same transition
// made through the Go API by a server of Go's own that was sent a
// request for it, on the same machine and in the same run. The
// campaign prints what the server adds.
//
// It measures the way in as well (ADR-0065): a message of 2 KiB with
// its signature, taken by an inlet and made a document, through the
// Go call and through the server, and the same message sent again.
// And the door for agents (ADR-0064): two thousand reads through one
// door, and the time the door takes to start, list its tools and end.
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real
// disk; FADODB_BENCH_COMMITS sizes it, 100,000 calls a row by default.
// It prints a Markdown table with -v.
func TestServeCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the campaign of the server")
	}
	n := 100_000
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_COMMITS")); err == nil && v > 0 {
		n = v
	}
	base := filepath.Join(dir, fmt.Sprintf("serve-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(filepath.Join(base, "secrets", "bank"), 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	path := filepath.Join(base, "acme.fado")
	const secret = "whsec_the_campaign_and_its_bank"
	settings := filepath.Join(base, "settings.json")
	for file, text := range map[string]string{settings: `{"inbound": {}}`, filepath.Join(base, "secrets", "bank", "signing_key"): secret} {
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The database, made as the manual makes one: an administrator, an
	// agent that works the tickets and the service the inlet acts as.
	if code, _, said := tool("init", path); code != 0 {
		t.Fatalf("init: %s", said)
	}
	keys := map[string]string{}
	for id, flags := range map[string][]string{
		"olga":     {"--kind", "person", "--system", "administrators", "--system", "designers"},
		"ap-agent": {"--kind", "agent", "--group", "support"},
		"bank-in":  {"--kind", "service", "--group", "payments"},
	} {
		if code, _, said := tool(append([]string{"user", "add", id, "--db", path}, flags...)...); code != 0 {
			t.Fatalf("user add %s: %s", id, said)
		}
		code, key, said := tool("key", "new", id, "--db", path)
		if code != 0 {
			t.Fatalf("key new %s: %s", id, said)
		}
		keys[id] = strings.TrimSpace(key)
	}
	ctx := context.Background()
	olga := fado.As(fado.Principal{ID: "olga", Kind: "person", System: []string{"administrators", "designers"}})
	agent := fado.Principal{ID: "ap-agent", Kind: "agent", Groups: []string{"support"}}
	opened := fado.Options{Users: true, Inbound: true, Secrets: func(context.Context, string, string) (string, error) { return secret, nil }}
	opened.Outbox.Manual = true

	// tickets are the documents of a row: each row moves its own, hold
	// and release in turn, so that every move is one named transition.
	const each = 1000
	tickets := func(row string) []string {
		ids := make([]string, each)
		for i := range ids {
			ids[i] = row + "-" + strconv.Itoa(i)
		}
		return ids
	}
	rowsWithTickets := []string{"api", "exec", "plain", "http1", "http16", "mcp"}
	db, err := fado.Open(path, opened)
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{campaignTicket, campaignNotice} {
		if _, err := db.DefineClass(ctx, []byte(class), olga); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DefineInlet(ctx, []byte(campaignInlet), olga); err != nil {
		t.Fatal(err)
	}
	for _, row := range rowsWithTickets {
		for _, id := range tickets(row) {
			if _, err := db.Create(ctx, "Ticket", fado.Values{"subject": "the printer on the second floor", "priority": 3}, fado.As(agent), fado.WithID(id)); err != nil {
				t.Fatal(err)
			}
		}
	}
	move := func(i int) string {
		if (i/each)%2 == 0 {
			return "hold"
		}
		return "release"
	}

	var rows []string
	add := func(work string, took []time.Duration, elapsed time.Duration) {
		rows = append(rows, campaignRow(work, took, elapsed))
	}
	// one runs a row by one caller: n calls, one after the other.
	one := func(call func(i int) error) ([]time.Duration, time.Duration) {
		took := make([]time.Duration, n)
		began := time.Now()
		for i := range took {
			at := time.Now()
			if err := call(i); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
			took[i] = time.Since(at)
		}
		return took, time.Since(began)
	}

	// The Go API, and the same calls as commands, on the database.
	ids := tickets("api")
	took, elapsed := one(func(i int) error {
		_, err := db.Transition(ctx, "Ticket", ids[i%each], move(i), nil, fado.As(agent))
		return err
	})
	add("transition, the Go API", took, elapsed)
	took, elapsed = one(func(i int) error {
		_, err := db.Get(ctx, "Ticket", ids[i%each], fado.As(agent))
		return err
	})
	add("read by id, the Go API", took, elapsed)
	ids = tickets("exec")
	took, elapsed = one(func(i int) error {
		_, err := db.Exec(ctx, agent, "transition", []byte(`{"class": "Ticket", "id": "`+ids[i%each]+`", "transition": "`+move(i)+`"}`))
		return err
	})
	add("transition, as a command", took, elapsed)
	took, elapsed = one(func(i int) error {
		_, err := db.Exec(ctx, agent, "get", []byte(`{"class": "Ticket", "id": "`+ids[i%each]+`"}`))
		return err
	})
	add("read by id, as a command", took, elapsed)

	// The way in, through the Go call: a message of 2 KiB, signed, that
	// becomes a document; beside the same document made by the Go call
	// with a key, which is the command of the message without what
	// stands before it; and the message sent again.
	message := func(row string, i int) []byte {
		text := `{"id": "` + row + `-` + strconv.Itoa(i) + `", "type": "notice", "sender": "the bank", "amount": "12.50", "text": "`
		return []byte(text + strings.Repeat("x", 2048-len(text)-2) + `"}`)
	}
	// A signature holds for five minutes, and a row of the campaign may
	// come later than that after the one before it: the messages of a
	// row are signed when the row begins, as a sender signs when it
	// sends.
	bodies := make([][]byte, n)
	proofs := make([]string, n)
	sign := func(row string) {
		at := strconv.FormatInt(time.Now().Unix(), 10)
		for i := range bodies {
			bodies[i] = message(row, i)
			proofs[i] = "t=" + at + ",v1=" + fado.Sign(secret, at, bodies[i])
		}
	}
	bank := fado.Principal{ID: "bank-in", Kind: "service", Groups: []string{"payments"}}
	note := strings.Repeat("x", 1950)
	took, elapsed = one(func(i int) error {
		_, err := db.Create(ctx, "Notice", fado.Values{"sender": "the bank", "amount": 12.5, "note": note}, fado.As(bank), fado.Key("made-"+strconv.Itoa(i)))
		return err
	})
	add("a document made with a key, the Go API", took, elapsed)
	receive := func(i int) error {
		_, err := db.Receive(ctx, "bank.notice", &fado.Message{Body: bodies[i], Headers: map[string]string{"Bank-Signature": proofs[i]}})
		return err
	}
	sign("call")
	took, elapsed = one(receive)
	add("a message of 2 KiB that makes that document, the Go call", took, elapsed)
	sign("call")
	took, elapsed = one(receive)
	add("the same messages again, the Go call", took, elapsed)
	// A server of Go's own on this machine: one connection kept open, a
	// body sent and one answered. It has two addresses. One does
	// nothing, which is what this machine's loopback and Go's HTTP cost
	// with no FadoDB in them. The other makes the transition through
	// the Go API and answers nothing of it: the transition over the Go
	// API as a program makes it that was sent a request for it. What
	// fadodb serve adds is measured beside that one (ADR-0066): the
	// same machine, the same request, the same commit.
	plain := http.NewServeMux()
	plain.HandleFunc("/v1/nothing", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}\n")
	})
	moved := tickets("plain")
	var turn atomic.Int64
	plain.HandleFunc("/v1/move", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		i := int(turn.Add(1) - 1)
		if _, err := db.Transition(r.Context(), "Ticket", moved[i%each], move(i), nil, fado.As(agent)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}\n")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bare := &http.Server{Handler: plain}
	go func() { _ = bare.Serve(ln) }()
	defer bare.Close()
	caller := func() *http.Client { return &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 4}} }
	post := func(client *http.Client, url, key string, headers map[string]string, body []byte) error {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s answered %d", url, resp.StatusCode)
		}
		return nil
	}
	client := caller()
	object := []byte(`{"class": "Ticket", "id": "api-0", "transition": "hold"}`)
	took, elapsed = one(func(int) error { return post(client, "http://"+ln.Addr().String()+"/v1/nothing", "", nil, object) })
	add("a request that does nothing, Go's own server on this machine", took, elapsed)
	took, elapsed = one(func(int) error { return post(client, "http://"+ln.Addr().String()+"/v1/move", "", nil, object) })
	add("transition, the Go API called by a request to Go's own server", took, elapsed)
	besideMedian, beside := quantile(took, 0.50), quantile(took, 0.99)
	client.CloseIdleConnections()
	if err := bare.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// The server, as fadodb serve runs it.
	addr, _, stop := serving(t, path, "--listen", "127.0.0.1:0", "--settings", settings, "--secrets-dir", filepath.Join(base, "secrets"))
	url := "http://" + addr + "/v1/"
	client = caller()
	took, elapsed = one(func(int) error { return post(client, url+"whoami", keys["ap-agent"], nil, []byte(`{}`)) })
	add("whoami, through the server", took, elapsed)
	ids = tickets("http1")
	took, elapsed = one(func(i int) error {
		return post(client, url+"get", keys["ap-agent"], nil, []byte(`{"class": "Ticket", "id": "`+ids[i%each]+`"}`))
	})
	add("read by id, through the server", took, elapsed)
	took, elapsed = one(func(i int) error {
		return post(client, url+"transition", keys["ap-agent"], nil, []byte(`{"class": "Ticket", "id": "`+ids[i%each]+`", "transition": "`+move(i)+`"}`))
	})
	add("transition, through the server", took, elapsed)
	servedMedian, served := quantile(took, 0.50), quantile(took, 0.99)

	// Sixteen callers at once, each on a connection of its own and on
	// documents of its own.
	const callers = 16
	many := func(call func(client *http.Client, who, i int) error) ([]time.Duration, time.Duration) {
		took := make([]time.Duration, n)
		var wg sync.WaitGroup
		began := time.Now()
		for who := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client := caller()
				defer client.CloseIdleConnections()
				for i := who; i < n; i += callers {
					at := time.Now()
					if err := call(client, who, i/callers); err != nil {
						t.Errorf("caller %d, call %d: %v", who, i, err)
						return
					}
					took[i] = time.Since(at)
				}
			}()
		}
		wg.Wait()
		return took, time.Since(began)
	}
	ids = tickets("http16")
	const mine = each / callers
	took, elapsed = many(func(client *http.Client, who, i int) error {
		return post(client, url+"get", keys["ap-agent"], nil, []byte(`{"class": "Ticket", "id": "`+ids[who*mine+i%mine]+`"}`))
	})
	add("read by id, through the server, sixteen callers", took, elapsed)
	took, elapsed = many(func(client *http.Client, who, i int) error {
		name := "hold"
		if (i/mine)%2 == 1 {
			name = "release"
		}
		return post(client, url+"transition", keys["ap-agent"], nil, []byte(`{"class": "Ticket", "id": "`+ids[who*mine+i%mine]+`", "transition": "`+name+`"}`))
	})
	add("transition, through the server, sixteen callers", took, elapsed)

	// The way in, through the server: the message carries no key.
	in := "http://" + addr + "/v1/in/bank.notice"
	send := func(i int) error {
		return post(client, in, "", map[string]string{"Bank-Signature": proofs[i]}, bodies[i])
	}
	sign("sent")
	took, elapsed = one(send)
	add("a message of 2 KiB that makes a document, through the server", took, elapsed)
	sign("sent")
	took, elapsed = one(send)
	add("the same messages again, through the server", took, elapsed)

	// The door for agents: two thousand reads through one door, which
	// carries sixteen at once, and the door started, asked for its
	// tools and ended.
	t.Setenv("FADODB_URL", "http://"+addr)
	t.Setenv("FADODB_KEY", keys["ap-agent"])
	ids = tickets("mcp")
	const reads = 2000
	lines := make([]string, reads)
	for i := range lines {
		lines[i] = `{"jsonrpc":"2.0","id":` + strconv.Itoa(i) + `,"method":"tools/call","params":{"name":"get","arguments":{"class":"Ticket","id":"` + ids[i%each] + `"},"_meta":` + modernMeta + `}}`
	}
	began := time.Now()
	code, wrote, said := door(nil, lines...)
	through := time.Since(began)
	if code != exitDone || len(wrote) != reads || strings.Contains(strings.Join(wrote, ""), `"isError":true`) {
		t.Errorf("the reads through the door for agents: exit %d, %d answers\n%s", code, len(wrote), said)
	}
	starts := make([]time.Duration, 20)
	for i := range starts {
		at := time.Now()
		if code, wrote, _ := door(nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":`+modernMeta+`}}`); code != exitDone || len(wrote) != 1 {
			t.Fatalf("the door asked for its tools: exit %d", code)
		}
		starts[i] = time.Since(at)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	if code := stop(); code != 0 {
		t.Errorf("the server ended with %d", code)
	}

	t.Logf("\n%d calls a row, one caller on one connection unless the row says otherwise.\n\n| Work | Calls | Throughput | p50 | p90 | p99 | max |\n|---|---|---|---|---|---|---|\n%s\n\n"+
		"The budget (ADR-0011, as ADR-0066 reads it): the server adds under 0.200 ms at p99 to a transition over the Go API, both called by a request on this machine. "+
		"It adds %s at p99 and %s at the median: a transition through the server takes %s and %s, and one made through the Go API by Go's own server %s and %s.\n\n"+
		"The door for agents: %d reads through one door in %s, %.0f a second. Started, asked for its tools and ended: %s at the median of %d, %s the longest.",
		n, strings.Join(rows, "\n"), milliseconds(served-beside), milliseconds(servedMedian-besideMedian), milliseconds(served), milliseconds(servedMedian), milliseconds(beside), milliseconds(besideMedian),
		reads, through.Round(time.Millisecond), float64(reads)/through.Seconds(),
		starts[len(starts)/2].Round(10*time.Microsecond), len(starts), starts[len(starts)-1].Round(10*time.Microsecond))
}

// campaignRow is a row of the campaign's table: how many calls, how
// many a second, and how long one took.
func campaignRow(work string, took []time.Duration, elapsed time.Duration) string {
	at := func(q float64) string { return milliseconds(quantile(took, q)) }
	return fmt.Sprintf("| %s | %d | %.0f/s | %s | %s | %s | %s |", work, len(took), float64(len(took))/elapsed.Seconds(), at(0.50), at(0.90), at(0.99), at(1))
}

// quantile is how long the call took that a part q of the calls were
// quicker than: 0.50 is the median, 0.99 is p99.
func quantile(took []time.Duration, q float64) time.Duration {
	sorted := append([]time.Duration(nil), took...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[min(len(sorted)-1, int(q*float64(len(sorted))))]
}

// milliseconds writes a time as the campaign's table does.
func milliseconds(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Nanoseconds())/1e6, 'f', 3, 64) + " ms"
}

// The classes and the inlet of the campaign: a ticket that support
// holds and releases, and a notice that the bank's message makes.
const (
	campaignTicket = `{
  "name": "Ticket",
  "variables": {
    "subject":  {"type": "text", "required": true},
    "priority": {"type": "number", "default": "3"},
    "urgent":   {"formula": "priority < 2"}
  },
  "states": {"initial": "open", "list": ["open", "held"]},
  "transitions": [
    {"name": "hold",    "from": "open", "to": "held"},
    {"name": "release", "from": "held", "to": "open"}
  ],
  "access": {"read": "INGROUP(\"support\")", "create": "INGROUP(\"support\")", "update": "INGROUP(\"support\")", "delete": "FALSE"}
}`
	campaignNotice = `{
  "name": "Notice",
  "variables": {
    "sender": {"type": "text", "required": true},
    "amount": {"type": "number"},
    "note":   {"type": "text"}
  },
  "states": {"initial": "new", "list": ["new"]},
  "access": {"read": "INGROUP(\"payments\")", "create": "INGROUP(\"payments\")", "update": "FALSE", "delete": "FALSE"}
}`
	campaignInlet = `{
  "name": "bank.notice",
  "as": "bank-in",
  "connection": {"name": "bank", "secrets": ["signing_key"]},
  "proof": {"form": "timestamp.body", "header": "Bank-Signature", "secret": "signing_key"},
  "when": "JSONVALUE(BODY, \"type\") = \"notice\"",
  "key": "JSONVALUE(BODY, \"id\")",
  "command": "create",
  "object": {"class": "Notice", "values": {
    "sender": "JSONVALUE(BODY, \"sender\")",
    "amount": "VALUE(JSONVALUE(BODY, \"amount\"))",
    "note": "JSONVALUE(BODY, \"text\")"
  }}
}`
)
