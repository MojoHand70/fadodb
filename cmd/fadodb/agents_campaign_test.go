package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAgentsCampaign measures what Phase 7 gave agents, at fadodb serve
// as it is told nothing more than where its database is (ADR-0064,
// items 11 to 38). Each has no budget of its own, and each is measured
// beside what it stands on, in the same run:
//
//   - the door at the server itself, /mcp: a read and a move as tools,
//     beside the same commands at /v1/ (item 18 says a tool at /mcp runs
//     as a command at /v1/ does);
//   - waiting for a change: how long after a move is sent an agent that
//     waits with wait_for_changes has it, for one agent and for a
//     hundred, while a writer moves a document every millisecond, as
//     ADR-0058 measures the stream;
//   - files through the door started with --files: files of 1 MiB put
//     with put_file and written back with get_file, beside the same
//     files sent to POST /v1/blobs and read at GET /v1/file by sixteen
//     callers, as many as the door carries at once.
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real disk.
// FADODB_BENCH_COMMITS sizes the rows of /mcp, 100,000 calls a row by
// default; the waits are 5,000 moves, and the files 200 of 1 MiB. It
// prints Markdown tables with -v.
func TestAgentsCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the campaign of the door for agents")
	}
	n := 100_000
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_COMMITS")); err == nil && v > 0 {
		n = v
	}
	base := filepath.Join(dir, fmt.Sprintf("agents-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	path := filepath.Join(base, "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatalf("init: %s", said)
	}
	keys := map[string]string{}
	for id, flags := range map[string][]string{
		"olga":     {"--kind", "person", "--system", "administrators", "--system", "designers"},
		"ap-agent": {"--kind", "agent", "--group", "support"},
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
	addr, _, stop := serving(t, path, "--listen", "127.0.0.1:0")
	v1, mcpAt := "http://"+addr+"/v1/", "http://"+addr+"/mcp"
	agentKey := keys["ap-agent"]
	caller := func() *http.Client { return &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 4}} }
	// send makes a request and answers the body of a reply of 200.
	send := func(client *http.Client, method, url, key string, headers map[string]string, body io.Reader) ([]byte, error) {
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		answer, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %d: %.300s", url, resp.StatusCode, answer)
		}
		return answer, nil
	}
	setup := caller()
	// command sends a command as ap-agent, who works the tickets; the
	// class is defined by Olga, a designer.
	commandAs := func(key, name, object string) []byte {
		t.Helper()
		answer, err := send(setup, http.MethodPost, v1+name, key, nil, strings.NewReader(object))
		if err != nil {
			t.Fatal(err)
		}
		return answer
	}
	command := func(name, object string) []byte { return commandAs(agentKey, name, object) }
	commandAs(keys["olga"], "define_class", `{"definition": `+campaignScanned+`}`)
	const each = 1000
	tickets := func(row string) []string {
		ids := make([]string, each)
		for i := range ids {
			ids[i] = row + "-" + strconv.Itoa(i)
		}
		return ids
	}
	for _, row := range []string{"v1", "mcp", "wait"} {
		for _, id := range tickets(row) {
			command("create", `{"class": "Ticket", "id": "`+id+`", "values": {"subject": "the printer on the second floor"}}`)
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

	// The door at the server, beside the commands at /v1/. A tool at
	// /mcp is a message of the modern form with its headers; its answer
	// is checked for an error, which a reply of 200 may carry.
	client := caller()
	ids := tickets("v1")
	took, elapsed := one(func(i int) error {
		_, err := send(client, http.MethodPost, v1+"get", agentKey, nil, strings.NewReader(`{"class": "Ticket", "id": "`+ids[i%each]+`"}`))
		return err
	})
	add("read by id, a command at /v1/", took, elapsed)
	took, elapsed = one(func(i int) error {
		_, err := send(client, http.MethodPost, v1+"transition", agentKey, nil, strings.NewReader(`{"class": "Ticket", "id": "`+ids[i%each]+`", "transition": "`+move(i)+`"}`))
		return err
	})
	add("transition, a command at /v1/", took, elapsed)
	toolCall := func(name, arguments string) func(i int) error {
		headers := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": name}
		return func(i int) error {
			body := `{"jsonrpc":"2.0","id":` + strconv.Itoa(i) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `,"_meta":` + modernMeta + `}}`
			answer, err := send(client, http.MethodPost, mcpAt, agentKey, headers, strings.NewReader(body))
			if err == nil && !bytes.Contains(answer, []byte(`"isError":false`)) {
				err = fmt.Errorf("the tool %s answered %.300s", name, answer)
			}
			return err
		}
	}
	ids = tickets("mcp")
	took, elapsed = one(func(i int) error {
		return toolCall("get", `{"class":"Ticket","id":"`+ids[i%each]+`"}`)(i)
	})
	add("read by id, the tool get at /mcp", took, elapsed)
	took, elapsed = one(func(i int) error {
		return toolCall("transition", `{"class":"Ticket","id":"`+ids[i%each]+`","transition":"`+move(i)+`"}`)(i)
	})
	add("transition, the tool transition at /mcp", took, elapsed)
	client.CloseIdleConnections()

	// Waiting for a change: a writer sends a move every millisecond, and
	// each agent waits with wait_for_changes from the place it was given,
	// until it has had every move. A move is told by its document and the
	// version it made: the writer moves the documents of the row in turn,
	// so move c is document c%each at version 2+c/each.
	const moves = 5000
	waitRows := []string{}
	for _, agents := range []int{1, 100} {
		ids := tickets("wait")
		// Each round moves the documents from where the last left them.
		round := len(waitRows)
		var sent [moves]time.Time
		var mu sync.Mutex
		var heard []time.Duration
		// Each agent begins at the end of the log as it is now.
		var first struct {
			After string `json:"after"`
		}
		if err := json.Unmarshal(command("wait_for_changes", `{"classes": ["Ticket"]}`), &first); err != nil || first.After == "" {
			t.Fatalf("the place to wait from: %v", err)
		}
		var wg sync.WaitGroup
		for range agents {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client := caller()
				defer client.CloseIdleConnections()
				place, got := first.After, 0
				mine := make([]time.Duration, 0, moves)
				for got < moves {
					answer, err := send(client, http.MethodPost, v1+"wait_for_changes", agentKey, nil,
						strings.NewReader(`{"classes": ["Ticket"], "kinds": ["transition"], "after": "`+place+`", "seconds": 30, "limit": 1000}`))
					at := time.Now()
					if err != nil {
						t.Errorf("an agent waiting: %v", err)
						return
					}
					var waited struct {
						Events []struct {
							Doc     string `json:"doc_id"`
							Version int    `json:"after_version"`
						} `json:"events"`
						After string `json:"after"`
					}
					if err := json.Unmarshal(answer, &waited); err != nil {
						t.Errorf("an agent waiting: %v", err)
						return
					}
					for _, e := range waited.Events {
						d, _ := strconv.Atoi(strings.TrimPrefix(e.Doc, "wait-"))
						c := (e.Version-2-round*moves/each)*each + d
						if c < 0 || c >= moves {
							t.Errorf("an agent heard of a move it did not wait for: %s at %d", e.Doc, e.Version)
							return
						}
						mu.Lock()
						when := sent[c]
						mu.Unlock()
						mine = append(mine, at.Sub(when))
						got++
					}
					place = waited.After
				}
				mu.Lock()
				heard = append(heard, mine...)
				mu.Unlock()
			}()
		}
		writer := caller()
		tick := time.NewTicker(time.Millisecond)
		began := time.Now()
		for c := range moves {
			<-tick.C
			mu.Lock()
			sent[c] = time.Now()
			mu.Unlock()
			name := "hold"
			if (round*moves/each+c/each)%2 == 1 {
				name = "release"
			}
			if _, err := send(writer, http.MethodPost, v1+"transition", agentKey, nil, strings.NewReader(`{"class": "Ticket", "id": "`+ids[c%each]+`", "transition": "`+name+`"}`)); err != nil {
				t.Fatal(err)
			}
		}
		tick.Stop()
		wg.Wait()
		elapsed := time.Since(began)
		writer.CloseIdleConnections()
		if len(heard) != agents*moves {
			t.Fatalf("%d agents heard %d moves, where each should hear %d", agents, len(heard), moves)
		}
		who := "one agent"
		if agents > 1 {
			who = strconv.Itoa(agents) + " agents"
		}
		waitRows = append(waitRows, campaignRow("a move heard by "+who+" waiting with wait_for_changes, a move sent every millisecond", heard, elapsed))
	}

	// Files of 1 MiB, each other bytes. Beside the door: the server's
	// own two addresses, sixteen callers at once.
	const files, size, callers = 200, 1 << 20, 16
	bytesOf := func(set, i int) []byte {
		b := make([]byte, size)
		r := rand.New(rand.NewChaCha8([32]byte{byte(set), byte(i), byte(i >> 8)}))
		for j := 0; j < size; j += 8 {
			v := r.Uint64()
			for k := 0; k < 8; k++ {
				b[j+k] = byte(v >> (8 * k))
			}
		}
		return b
	}
	at16 := func(call func(client *http.Client, i int) error) time.Duration {
		var wg sync.WaitGroup
		began := time.Now()
		for who := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client := caller()
				defer client.CloseIdleConnections()
				for i := who; i < files; i += callers {
					if err := call(client, i); err != nil {
						t.Errorf("file %d: %v", i, err)
						return
					}
				}
			}()
		}
		wg.Wait()
		return time.Since(began)
	}
	// facts are the facts of each file as the server answered them, and
	// the hash of each.
	type facts struct {
		File json.RawMessage `json:"file"`
	}
	hashOf := func(f facts) string {
		var h struct{ Hash string }
		_ = json.Unmarshal(f.File, &h)
		return h.Hash
	}
	put := make([]facts, files)
	putAt := at16(func(client *http.Client, i int) error {
		answer, err := send(client, http.MethodPost, v1+"blobs?name=f-"+strconv.Itoa(i)+".bin", agentKey, map[string]string{"Content-Type": "application/octet-stream"}, bytes.NewReader(bytesOf(1, i)))
		if err == nil {
			err = json.Unmarshal(answer, &put[i])
		}
		return err
	})
	// The files on documents, so that they can be read through them.
	ids = tickets("v1")
	for i, f := range put {
		command("set", `{"class": "Ticket", "id": "`+ids[i]+`", "values": {"scan": `+string(f.File)+`}}`)
	}
	getAt := at16(func(client *http.Client, i int) error {
		_, err := send(client, http.MethodGet, v1+"file?class=Ticket&id="+ids[i]+"&hash="+hashOf(put[i]), agentKey, nil, nil)
		return err
	})

	t.Setenv("FADODB_URL", "http://"+addr)
	t.Setenv("FADODB_KEY", agentKey)
	from, into := filepath.Join(base, "from"), filepath.Join(base, "into")
	for _, d := range []string{from, into} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	lines := make([]string, files)
	for i := range lines {
		if err := os.WriteFile(filepath.Join(from, "d-"+strconv.Itoa(i)+".bin"), bytesOf(2, i), 0o600); err != nil {
			t.Fatal(err)
		}
		lines[i] = `{"jsonrpc":"2.0","id":` + strconv.Itoa(i) + `,"method":"tools/call","params":{"name":"put_file","arguments":{"path":"d-` + strconv.Itoa(i) + `.bin"},"_meta":` + modernMeta + `}}`
	}
	began := time.Now()
	code, wrote, said := door([]string{"--files", from}, lines...)
	doorPut := time.Since(began)
	doorFacts := make([]facts, files)
	if code != exitDone || len(wrote) != files {
		t.Fatalf("put_file through the door: exit %d, %d answers\n%s", code, len(wrote), said)
	}
	for _, line := range wrote {
		var reply struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
		}
		if json.Unmarshal([]byte(line), &reply) != nil || reply.Result.IsError || len(reply.Result.Content) != 1 || reply.ID < 0 || reply.ID >= files ||
			json.Unmarshal([]byte(reply.Result.Content[0].Text), &doorFacts[reply.ID]) != nil {
			t.Fatalf("put_file through the door answered %.300s", line)
		}
	}
	ids = tickets("mcp")
	for i, f := range doorFacts {
		hash := hashOf(f)
		command("set", `{"class": "Ticket", "id": "`+ids[i]+`", "values": {"scan": `+string(f.File)+`}}`)
		lines[i] = `{"jsonrpc":"2.0","id":` + strconv.Itoa(i) + `,"method":"tools/call","params":{"name":"get_file","arguments":{"class":"Ticket","id":"` + ids[i] + `","hash":"` + hash + `","path":"g-` + strconv.Itoa(i) + `.bin"},"_meta":` + modernMeta + `}}`
	}
	began = time.Now()
	code, wrote, said = door([]string{"--files", into}, lines...)
	doorGet := time.Since(began)
	if code != exitDone || len(wrote) != files || strings.Contains(strings.Join(wrote, ""), `"isError":true`) {
		t.Fatalf("get_file through the door: exit %d, %d answers\n%s", code, len(wrote), said)
	}
	for i := range files {
		if got, err := os.ReadFile(filepath.Join(into, "g-"+strconv.Itoa(i)+".bin")); err != nil || !bytes.Equal(got, bytesOf(2, i)) {
			t.Fatalf("the file %d the door wrote is not the one put (%v)", i, err)
		}
	}
	if code := stop(); code != 0 {
		t.Errorf("the server ended with %d", code)
	}
	rate := func(d time.Duration) string {
		return fmt.Sprintf("%s, %.0f MiB/s", d.Round(time.Millisecond), float64(files)/d.Seconds())
	}
	t.Logf("\n%d calls a row, one caller on one connection.\n\n| Work | Calls | Throughput | p50 | p90 | p99 | max |\n|---|---|---|---|---|---|---|\n%s\n\n"+
		"Waiting for a change, %d moves a row:\n\n| Work | Heard | Throughput | p50 | p90 | p99 | max |\n|---|---|---|---|---|---|---|\n%s\n\n"+
		"Files, %d of 1 MiB, sixteen at once:\n\n| Work | Took |\n|---|---|\n"+
		"| put, POST /v1/blobs, sixteen callers | %s |\n| put_file, through one door started with --files | %s |\n"+
		"| read, GET /v1/file, sixteen callers | %s |\n| get_file, through one door started with --files | %s |",
		n, strings.Join(rows, "\n"), moves, strings.Join(waitRows, "\n"), files, rate(putAt), rate(doorPut), rate(getAt), rate(doorGet))
}

// campaignScanned is the ticket of the campaign of the door for agents:
// the ticket of the campaign of the server, with a scan.
const campaignScanned = `{
  "name": "Ticket",
  "variables": {
    "subject":  {"type": "text", "required": true},
    "priority": {"type": "number", "default": "3"},
    "scan":     {"type": "object", "schema": "File"}
  },
  "states": {"initial": "open", "list": ["open", "held"]},
  "transitions": [
    {"name": "hold",    "from": "open", "to": "held"},
    {"name": "release", "from": "held", "to": "open"}
  ],
  "access": {"read": "INGROUP(\"support\")", "create": "INGROUP(\"support\")", "update": "INGROUP(\"support\")", "delete": "FALSE"}
}`
