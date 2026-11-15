package main

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

// The counts of the server (ADR-0059, item 9), as it writes them for
// requests it was told of.
func TestTheCountsOfTheServer(t *testing.T) {
	started := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	m := newMetrics(`v0.6.0 "dev"`, started)
	told := func(command string, status int, took time.Duration) {
		m.record(httpdoor.Request{Command: command, Status: status, Took: took})
	}
	told("transition", 200, 60*time.Microsecond)
	told("transition", 200, 40*time.Microsecond)
	told("transition", 409, 30*time.Microsecond)
	told("get", 200, 3*time.Millisecond)
	told("file", 304, 200*time.Microsecond)
	// A name that is no command, and a status the server does not answer:
	// both are counted, under other, and make no line of their own.
	told("drop_everything", 404, 10*time.Microsecond)
	told(strings.Repeat("x", 500), 404, 10*time.Microsecond)
	told("get", 418, 45*time.Second)

	var out bytes.Buffer
	queue := fado.OutboxCounts{Due: 2, Waiting: 5, Dead: 1, OldestDue: started.Add(30 * time.Second)}
	m.write(&out, 3, queue, 4096, 1024, started.Add(90*time.Second))
	got := out.String()
	for _, want := range []string{
		"# HELP fadodb_requests_total Requests answered, by the command they were sent to and the status of the reply.",
		"# TYPE fadodb_requests_total counter\n",
		`fadodb_build_info{version="v0.6.0 \"dev\""} 1` + "\n",
		"fadodb_start_time_seconds " + strconv.FormatInt(started.Unix(), 10) + "\n",
		`fadodb_requests_total{command="transition",status="200"} 2` + "\n",
		`fadodb_requests_total{command="transition",status="409"} 1` + "\n",
		`fadodb_requests_total{command="get",status="200"} 1` + "\n",
		`fadodb_requests_total{command="get",status="other"} 1` + "\n",
		`fadodb_requests_total{command="file",status="304"} 1` + "\n",
		`fadodb_requests_total{command="other",status="404"} 2` + "\n",
		`fadodb_command_seconds_sum{command="transition"} 0.00013` + "\n",
		`fadodb_command_seconds_count{command="transition"} 3` + "\n",
		`fadodb_command_seconds_sum{command="get"} 45.003` + "\n",
		`fadodb_command_seconds_count{command="get"} 2` + "\n",
		// Within a bound are the requests that took that long or less.
		`fadodb_request_seconds_bucket{le="0.0001"} 5` + "\n",
		`fadodb_request_seconds_bucket{le="0.00025"} 6` + "\n",
		`fadodb_request_seconds_bucket{le="0.0025"} 6` + "\n",
		`fadodb_request_seconds_bucket{le="0.005"} 7` + "\n",
		`fadodb_request_seconds_bucket{le="30"} 7` + "\n",
		`fadodb_request_seconds_bucket{le="+Inf"} 8` + "\n",
		"fadodb_request_seconds_count 8\n",
		"fadodb_streams 3\n",
		`fadodb_actions{state="due"} 2` + "\n",
		`fadodb_actions{state="waiting"} 5` + "\n",
		`fadodb_actions{state="dead"} 1` + "\n",
		"fadodb_actions_oldest_due_seconds 60\n",
		"fadodb_database_bytes 4096\n",
		"fadodb_log_bytes 1024\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the counts have no %q", want)
		}
	}
	if strings.Contains(got, "drop_everything") || strings.Contains(got, "xxxx") {
		t.Error("a name that is no command made a line of its own")
	}
	// A command nobody sent has no line: the page holds what happened.
	if strings.Contains(got, `command="create"`) {
		t.Error("a command that was not sent has a line")
	}
	// Every family is announced once, in the order of the list, and
	// every line belongs to a family that was announced.
	var announced []string
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		if name, found := strings.CutPrefix(l, "# TYPE "); found {
			announced = append(announced, strings.Fields(name)[0])
			continue
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		family := announced[len(announced)-1]
		if !strings.HasPrefix(l, family) {
			t.Errorf("the line %q stands under the family %s", l, family)
		}
	}
	if len(announced) != len(families) {
		t.Fatalf("%d families are announced, of %d", len(announced), len(families))
	}
	for i, f := range families {
		if announced[i] != f.name {
			t.Errorf("family %d is %s, want %s", i, announced[i], f.name)
		}
	}
	if !strings.HasSuffix(got, "\n") || strings.Contains(got, "\n\n") {
		t.Error("the counts do not end each line once")
	}

	// Nothing is due: no action has been due for any time.
	out.Reset()
	m.write(&out, 0, fado.OutboxCounts{}, 0, 0, started)
	if !strings.Contains(out.String(), "fadodb_actions_oldest_due_seconds 0\n") {
		t.Errorf("with nothing due:\n%s", out.String())
	}

	// Counting a request allocates nothing: it stands on the way of
	// every command.
	r := httpdoor.Request{Command: "transition", Status: 200, Took: 50 * time.Microsecond}
	if n := testing.AllocsPerRun(1000, func() { m.record(r) }); n != 0 {
		t.Errorf("counting a request allocates %v times", n)
	}
	stranger := httpdoor.Request{Command: "no_such_command", Status: 599, Took: time.Hour}
	if n := testing.AllocsPerRun(1000, func() { m.record(stranger) }); n != 0 {
		t.Errorf("counting a request to a name that is no command allocates %v times", n)
	}
	// Every command has its place, and so have the files and the counts.
	for _, c := range fado.Commands() {
		if _, known := m.place[c.Name]; !known {
			t.Errorf("the command %s has no place among the counts", c.Name)
		}
	}
	for _, name := range []string{"blobs", "file", "blob", metricsName} {
		if _, known := m.place[name]; !known {
			t.Errorf("%s has no place among the counts", name)
		}
	}
}

// The counts are answered to a database administrator, with a key, and
// to nobody else (ADR-0059, item 9).
func TestWhoReadsTheCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	// A program of the company's own left an action that is due: the
	// server cannot run it, and counts it.
	db, err := fado.Open(path, fado.Options{Users: true, Outbox: leavesTheQueue, Actions: map[string]fado.ActionFunc{
		"mail.send": func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	setup := fado.As(fado.Principal{ID: "setup", System: []string{fado.GroupDesigners}})
	if _, err := db.DefineAction(ctx, []byte(`{"name": "mail.send", "params": {"to": "text"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, []byte(`{"name": "Order", "variables": {"customer": {"type": "text"}},
 "states": {"initial": "new", "list": ["new", "sent"]}, "transitions": [{"name": "send", "from": "new", "to": "sent"}],
 "rules": [{"on": "transition", "name": "send", "do": [{"action": "mail.send", "args": {"to": "customer"}}]}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, "Order", fado.Values{"customer": "ann@acme.example"}, fado.As(fado.Principal{ID: "ann"}), fado.WithID("o-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Order", "o-1", "send", nil, fado.As(fado.Principal{ID: "ann"})); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path},
		{"user", "add", "ap-agent", "--kind", "agent", "--group", "finance", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatal(said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--db", path)
	_, agent, _ := tool("key", "new", "ap-agent", "--db", path)
	olga, agent = strings.TrimSpace(olga), strings.TrimSpace(agent)

	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	base := "http://" + addr
	client := &http.Client{Timeout: 10 * time.Second}
	if status, _ := ask(t, client, "POST", base+"/v1/whoami", olga, "{}"); status != 200 {
		t.Fatalf("whoami: %d", status)
	}
	if status, _ := ask(t, client, "POST", base+"/v1/whoami", agent, "{}"); status != 200 {
		t.Fatalf("whoami: %d", status)
	}
	if status, _ := ask(t, client, "POST", base+"/v1/get", agent, `{"class": "Order", "id": "none"}`); status != 404 {
		t.Fatalf("get of a document that is not there: %d", status)
	}

	// Without a key, and with the key of a user who is no administrator.
	if status, body := ask(t, client, "GET", base+metricsPath, "", ""); status != 401 || !strings.Contains(body, `"code":"unauthenticated"`) || strings.Contains(body, "fadodb_") {
		t.Errorf("the counts, asked without a key: %d %s", status, body)
	}
	if status, body := ask(t, client, "GET", base+metricsPath, agent, ""); status != 403 || !strings.Contains(body, `"code":"forbidden"`) || strings.Contains(body, "fadodb_") {
		t.Errorf("the counts, asked by a user who is no administrator: %d %s", status, body)
	}
	if status, body := ask(t, client, "POST", base+metricsPath, olga, "{}"); status != 405 || !strings.Contains(body, "read with GET") {
		t.Errorf("the counts, asked with POST: %d %s", status, body)
	}

	// An administrator reads them.
	req, err := http.NewRequest("GET", base+metricsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+olga)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var page bytes.Buffer
	_, _ = page.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the counts: %d, %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	got := page.String()
	for _, want := range []string{
		`fadodb_build_info{version="dev"} 1` + "\n",
		`fadodb_requests_total{command="whoami",status="200"} 2` + "\n",
		`fadodb_requests_total{command="get",status="404"} 1` + "\n",
		// The three requests for the counts that were refused are counted
		// too; this one is counted when it is answered.
		`fadodb_requests_total{command="metrics",status="401"} 1` + "\n",
		`fadodb_requests_total{command="metrics",status="403"} 1` + "\n",
		`fadodb_requests_total{command="metrics",status="405"} 1` + "\n",
		`fadodb_command_seconds_count{command="whoami"} 2` + "\n",
		"fadodb_request_seconds_count 6\n",
		`fadodb_actions{state="dead"} 0` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the counts have no %q:\n%s", want, got)
		}
	}
	// The action of the Go function is due, or waits for its next
	// attempt: the server asked for it and has no function.
	if !strings.Contains(got, `fadodb_actions{state="due"} 1`+"\n") && !strings.Contains(got, `fadodb_actions{state="waiting"} 1`+"\n") {
		t.Errorf("the action the server cannot run is not counted:\n%s", got)
	}
	if strings.Contains(got, "fadodb_database_bytes 0\n") {
		t.Errorf("the database has no size:\n%s", got)
	}
	// A key, a user and a value of a document are in no line of the page.
	for _, secret := range []string{olga, agent, "ann@acme.example", "ap-agent", "o-1"} {
		if strings.Contains(got, secret) {
			t.Errorf("the counts hold %q", secret)
		}
	}
	stop()
	// Who asked for the counts is in the log, as for a command.
	for _, want := range []string{
		`"command":"metrics","user":"","status":401,"code":"unauthenticated"`,
		`"command":"metrics","user":"ap-agent","status":403,"code":"forbidden"`,
		`"command":"metrics","user":"olga","status":200,"ms":`,
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the log has no line with %s:\n%s", want, log)
		}
	}
}
