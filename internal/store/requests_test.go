package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

const (
	slackPost = `{"name": "slack.post", "label": "Post a message to Slack",
 "params": {"channel": {"type": "text", "required": true}, "text": {"type": "text", "required": true}},
 "returns": {"ts": "text"},
 "connection": {"name": "slack", "secrets": ["token"]},
 "request": {"method": "POST", "url": "\"https://slack.com/api/chat.postMessage\"",
             "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
             "body": {"channel": "channel", "text": "text"}},
 "ok": "JSONVALUE(BODY, \"ok\") = \"TRUE\"",
 "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}`
	slackURL = "https://slack.com/api/chat.postMessage"
	// A memo whose filing posts to Slack.
	memoClass = `{"name": "Memo", "variables": {"subject": {"type": "text"}, "to": {"type": "text", "default": "\"#general\""}},
 "states": {"initial": "new", "list": ["new", "filed"]}, "transitions": [{"name": "file", "from": "new", "to": "filed"}],
 "rules": [{"on": "enter", "state": "filed", "do": [{"action": "%s", "args": %s}]}]}`
)

// network is a stand-in for the network that allows Slack over its
// connection and one open host.
func network() *fakeOutbound {
	return &fakeOutbound{hosts: []string{"hooks.example"}, connections: map[string][]string{"slack": {"slack.com"}, "books": {"books.example"}}}
}

func fixedSecrets(values map[string]string) func(context.Context, string, string) (string, error) {
	return func(_ context.Context, connection, name string) (string, error) {
		if v, ok := values[connection+"/"+name]; ok {
			return v, nil
		}
		return "", errors.New("the vault has no such secret")
	}
}

// file creates a memo and files it.
func file(t testing.TB, s *Store, values Values) string {
	t.Helper()
	ctx := context.Background()
	memo, err := s.Create(ctx, "Memo", values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Memo", memo.ID, "file", nil); err != nil {
		t.Fatal(err)
	}
	return memo.ID
}

// What a process must have been given to take a definition with a request
// (ADR-0045, items 6 and 7): leave to send requests, the definition's
// connection, the hosts of that connection for an address that is known,
// and a supplier of secrets when the request reads one.
func TestRequests_DefinitionNeedsTheHostsLeave(t *testing.T) {
	ctx := context.Background()
	secrets := fixedSecrets(map[string]string{"slack/token": "xoxb-1"})
	diag := func(err error) string {
		diags, _ := err.(model.Diagnostics)
		if len(diags) != 1 {
			return fmt.Sprint(err)
		}
		return diags[0].Code + " " + diags[0].Path
	}

	// No leave to send: no definition with a request, whatever it says.
	closed, _ := openStore(t, Options{Secrets: secrets})
	if _, err := closed.DefineAction(ctx, []byte(slackPost)); diag(err) != "not_supported /request" {
		t.Errorf("a definition with a request in a process that sends none: %v", err)
	}
	if diags := closed.ValidateAction([]byte(slackPost)); len(diags) != 1 || diags[0].Code != model.CodeNotSupported {
		t.Errorf("a validation says so too: %v", diags)
	}

	s, path := openStore(t, Options{Outbound: network(), Secrets: secrets, Actions: map[string]ActionFunc{"books.post": (&recorder{}).fn}})
	info, err := s.DefineAction(ctx, []byte(slackPost))
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != ActionDeclared || !info.Available || info.Connection != "slack" || strings.Join(info.Secrets, " ") != "token" || info.Version != 1 {
		t.Errorf("the definition: %s", dump(info))
	}
	for name, tc := range map[string]struct{ def, want string }{
		"a connection the host did not name":            {strings.Replace(slackPost, `"name": "slack", "secrets"`, `"name": "mail", "secrets"`, 1), "unknown_connection /connection/name"},
		"an address outside the connection's hosts":     {strings.Replace(slackPost, "https://slack.com/api", "https://hooks.example/api", 1), "not_allowed /request/url"},
		"an address on no list":                         {strings.Replace(slackPost, "https://slack.com/api", "https://evil.example/api", 1), "not_allowed /request/url"},
		"an address not over https":                     {strings.Replace(slackPost, "https://slack.com/api", "http://slack.com/api", 1), "not_allowed /request/url"},
		"no connection, and a host that needs one":      {`{"name": "slack.raw", "request": {"url": "\"https://slack.com/api/x\""}}`, "not_allowed /request/url"},
		"a request, and a Go function of the same name": {`{"name": "books.post", "request": {"url": "\"https://hooks.example/books\""}}`, "not_allowed /request"},
	} {
		if _, err := s.DefineAction(ctx, []byte(tc.def)); diag(err) != tc.want {
			t.Errorf("%s: want %s, got %v", name, tc.want, err)
		}
	}
	// Without a connection a request goes to the open list, and reads no
	// secret: the model refuses SECRET there.
	if a, err := s.DefineAction(ctx, []byte(`{"name": "hook.send", "request": {"url": "\"https://hooks.example/in\""}}`)); err != nil || a.Connection != "" || a.Secrets != nil || !a.Available {
		t.Errorf("a definition without a connection: %v, %s", err, dump(a))
	}
	// An address that depends on an argument is judged when it is sent.
	if _, err := s.DefineAction(ctx, []byte(`{"name": "slack.any", "params": {"host": "text"}, "connection": {"name": "slack", "secrets": ["token"]},
 "request": {"url": "\"https://\" & host & \"/api\"", "headers": {"Authorization": "SECRET(\"token\")"}}}`)); err != nil {
		t.Fatal(err)
	}
	// A process that supplies no secrets takes only what reads none.
	s.Close()
	bare, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Outbound: network()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.DefineAction(ctx, []byte(strings.Replace(slackPost, `"label"`, `"description": "x", "label"`, 1))); diag(err) != "not_supported /connection/secrets" {
		t.Errorf("a request that reads a secret in a process that supplies none: %v", err)
	}
	if a, _ := bare.Action("slack.post"); a.Available || a.Kind != ActionDeclared {
		t.Errorf("the stored definition there: %s", dump(a))
	}
	if a, _ := bare.Action("hook.send"); !a.Available {
		t.Errorf("a definition that reads no secret runs there: %s", dump(a))
	}
	bare.Close()
	// And one that sends nothing lists what the file defines and runs none
	// of it.
	off, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	for _, name := range []string{"slack.post", "hook.send", "http.call"} {
		if a, ok := off.Action(name); !ok || a.Available || a.Kind != ActionDeclared {
			t.Errorf("%s in a process that sends nothing: %s", name, dump(a))
		}
	}
	if _, err := off.TryAction(ctx, "hook.send", nil); code(err) != CodeOutboundOff {
		t.Errorf("a trial there: %v", err)
	}
}

// The engine stores no secret (ADR-0045, item 6): none in a row, an
// event, a preview or an error, even when the receiver sends it back.
// The secret is asked for at every attempt, and reaches only the hosts of
// its connection.
func TestRequests_NoSecretIsStored(t *testing.T) {
	const first, second = "xoxb-FIRST-s3cr3t", "xoxb-SECOND-s3cr3t"
	var mu sync.Mutex
	asked := 0
	net := network()
	now := start
	s, path := openStore(t, Options{Now: func() time.Time { return now }, Outbound: net,
		Secrets: func(_ context.Context, connection, name string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			if connection != "slack" || name != "token" {
				return "", fmt.Errorf("asked for %s of %s", name, connection)
			}
			if asked++; asked == 1 {
				return first, nil
			}
			return second, nil
		}})
	ctx := context.Background()
	for _, def := range []string{slackPost,
		// An address the rule's argument decides, over Slack's connection.
		`{"name": "slack.any", "params": {"host": {"type": "text", "required": true}}, "connection": {"name": "slack", "secrets": ["token"]},
 "request": {"url": "\"https://\" & host & \"/api?token=\" & URLENCODE(SECRET(\"token\"))", "body": {"auth": "\"t:\" & SECRET(\"token\")"}}}`} {
		if _, err := s.DefineAction(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(memoClass, "slack.post", `{"channel": "to", "text": "subject"}`))); err != nil {
		t.Fatal(err)
	}
	// The receiver fails and sends the request's own header back; then it
	// refuses, again with the secret in what it says.
	net.script(&fakeReply{Status: 503, Text: "bad gateway for Authorization: Bearer " + first},
		&fakeReply{Status: 403, Body: json.RawMessage(`{"error": "invalid_auth", "token": "` + second + `"}`)})
	id := file(t, s, Values{"subject": "the budget"})
	if n, err := s.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	sent := net.take()
	if len(sent) != 1 || sent[0].Headers[0] != (Header{Name: "Authorization", Value: "Bearer " + first}) || sent[0].Connection != "slack" {
		t.Fatalf("the request: %s", dump(sent))
	}
	row := mustRows(t, s, id)[0]
	if row.Status != ActionPending || row.ErrorCode != CodeTryLater || !strings.Contains(row.ErrorMessage, "the receiver answered 503: bad gateway for Authorization: Bearer ***") {
		t.Errorf("the row after a reply that repeats the secret: %s", dump(row))
	}
	// The second attempt asks for the secret again and sends the new one.
	now = now.Add(time.Second)
	if n, _ := s.Drain(ctx); n != 1 {
		t.Fatalf("the second attempt was not made")
	}
	if sent = net.take(); len(sent) != 1 || sent[0].Headers[0].Value != "Bearer "+second {
		t.Errorf("the second request: %s", dump(sent))
	}
	row = mustRows(t, s, id)[0]
	if row.Status != ActionDead || row.ErrorCode != CodeRefused || !strings.Contains(row.ErrorMessage, `"token": "***"`) || strings.Contains(row.ErrorMessage, "s3cr3t") {
		t.Errorf("the row after a refusal that repeats the secret: %s", dump(row))
	}
	// A secret that is part of the address and of the body, sent to a host
	// outside its connection: refused before anything leaves, and the
	// message names the host and nothing else of the address.
	out, err := s.TryAction(ctx, "slack.any", Values{"host": "hooks.example"})
	if err != nil || out.OK || out.Code != CodeHostNotAllowed || out.Retry || strings.Contains(out.Message, "s3cr3t") || len(net.take()) != 0 {
		t.Errorf("a secret pointed at another host: %v, %s", err, dump(out))
	}
	// To a host of its connection it goes, and a failure to reach it says
	// the host and not the address.
	net.script(&fakeReply{NoReply: `Post "https://slack.com/api?token=` + second + `": connection reset`})
	out, _ = s.TryAction(ctx, "slack.any", Values{"host": "slack.com"})
	if out.OK || out.Code != CodeNoReply || !out.Retry || strings.Contains(out.Message, "s3cr3t") || !strings.Contains(out.Message, "no reply from slack.com") {
		t.Errorf("a request that got no reply: %s", dump(out))
	}
	if sent = net.take(); len(sent) != 1 || !strings.Contains(sent[0].URL, "token="+second) || !strings.Contains(string(sent[0].Body), "t:"+second) {
		t.Errorf("the request with the secret in its address and its body: %s", dump(sent))
	}
	// A preview masks, and asks the host for nothing.
	before := asked
	pv, err := s.PreviewAction(ctx, "slack.any", Values{"host": "slack.com"})
	if err != nil || pv.Request.URL != "https://slack.com/api?token=%2A%2A%2A" || pv.Request.Body != `{"auth":"t:***"}` || asked != before {
		t.Errorf("a preview: %v, %s", err, dump(pv))
	}
	if pv, _ := s.Preview(ctx, "Memo", id, nil, ""); strings.Contains(dump(pv), "s3cr3t") {
		t.Error("a preview of the document holds the secret")
	}
	// Nothing the engine wrote holds either secret: not the rows, not the
	// events, not a byte of the file or of its log.
	rows, _ := s.Outbox(ctx, OutboxFilter{})
	events, _ := s.Events(ctx, "Memo", id)
	if text := dump(rows) + dump(events); strings.Contains(text, "s3cr3t") {
		t.Errorf("a row or an event holds the secret: %s", text)
	}
	s.Close()
	for _, file := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("s3cr3t")) {
			t.Errorf("%s holds a secret", file)
		}
	}
}

// A request that names a sign secret is signed (ADR-0045, item 4): the
// receiver checks, with the secret the two sides share, that the body is
// the one the engine sent and that it was sent now. Without a body of its
// own the request carries the call, which is what a developer's own
// service receives.
func TestRequests_SignedEnvelope(t *testing.T) {
	const key = "whsec-shared"
	net := network()
	now := start
	s, _ := openStore(t, Options{Now: func() time.Time { return now }, Outbound: net, Secrets: fixedSecrets(map[string]string{"books/signing_key": key})})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "books.post", "params": {"memo": {"type": "text", "required": true}, "note": "text"},
 "connection": {"name": "books", "secrets": ["signing_key"]},
 "request": {"url": "\"https://books.example/hooks/fadodb\"", "sign": "signing_key"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(memoClass, "books.post", `{"memo": "ID()", "note": "subject"}`))); err != nil {
		t.Fatal(err)
	}
	id := file(t, s, Values{"subject": "the budget"})
	if n, err := s.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	sent := net.take()
	if len(sent) != 1 {
		t.Fatalf("%d requests", len(sent))
	}
	req := sent[0]
	header := func(name string) string {
		for _, h := range req.Headers {
			if h.Name == name {
				return h.Value
			}
		}
		return ""
	}
	// The body is the call, as a registered function would receive it.
	var call ActionCall
	if err := json.Unmarshal(req.Body, &call); err != nil {
		t.Fatal(err)
	}
	row := mustRows(t, s, id)[0]
	if call.Key != row.Key || call.Attempt != 1 || call.Action != "books.post" || call.Args["memo"] != id || call.Args["note"] != "the budget" ||
		call.Event == nil || call.Event.Kind != EventTransition || call.Event.Name != "file" || call.Event.Document.State != "filed" || call.Event.Document.Values["subject"] != "the budget" {
		t.Errorf("the body of a request without one of its own: %s", req.Body)
	}
	// It is the published shape of a call.
	if findings := loadSchema(t, "call.schema.json").validate(t, req.Body); len(findings) > 0 {
		t.Errorf("the body does not fit the schema of a call: %v", findings)
	}
	// The engine's headers say which action this is, at which attempt.
	if header(HeaderIdempotencyKey) != row.Key || header(HeaderEvent) != call.Event.ID || header(HeaderAttempt) != "1" || header("Content-Type") != "application/json" || req.Method != "POST" {
		t.Errorf("the headers: %s", dump(req.Headers))
	}
	signature := header(HeaderSignature)
	if !strings.HasPrefix(signature, fmt.Sprintf("t=%d,v1=", start.Unix())) || len(signature) != len(fmt.Sprintf("t=%d,v1=", start.Unix()))+64 {
		t.Fatalf("the signature header: %q", signature)
	}
	if err := VerifySignature(key, signature, req.Body, now, 5*time.Minute); err != nil {
		t.Errorf("the receiver's check: %v", err)
	}
	for name, check := range map[string]error{
		"another secret":      VerifySignature("whsec-other", signature, req.Body, now, 5*time.Minute),
		"a body that changed": VerifySignature(key, signature, append([]byte(" "), req.Body...), now, 5*time.Minute),
		"a timestamp changed": VerifySignature(key, strings.Replace(signature, "t=", "t=1", 1), req.Body, now, 5*time.Minute),
		"a request replayed":  VerifySignature(key, signature, req.Body, now.Add(time.Hour), 5*time.Minute),
		"a clock far behind":  VerifySignature(key, signature, req.Body, now.Add(-time.Hour), 5*time.Minute),
		"no signature":        VerifySignature(key, "", req.Body, now, 5*time.Minute),
		"half a signature":    VerifySignature(key, "t=1", req.Body, now, 5*time.Minute),
	} {
		if check == nil {
			t.Errorf("%s passes the receiver's check", name)
		}
	}
	// The signature is the HMAC-SHA256 of the timestamp, a dot and the
	// body, in hex: what a receiver in any language computes.
	at := fmt.Sprint(start.Unix())
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(at + "."))
	mac.Write(req.Body)
	if want := hex.EncodeToString(mac.Sum(nil)); signature != "t="+at+",v1="+want || Sign(key, at, req.Body) != want {
		t.Errorf("the signature is %s, want v1=%s", signature, want)
	}
}

// A reply's Retry-After moves the next attempt later, and no further than
// a day; a receiver that keeps failing leaves the action dead after
// MaxAttempts, with one event.
func TestRequests_RetriesAndTheirBounds(t *testing.T) {
	net := network()
	now := start
	s, _ := openStore(t, Options{Now: func() time.Time { return now }, Outbound: net, Outbox: OutboxOptions{MaxAttempts: 3}})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(memoClass, "http.call", `{"url": "\"https://hooks.example/memos\"", "body": {"subject": "subject"}}`))); err != nil {
		t.Fatal(err)
	}
	net.script(&fakeReply{Status: 503, RetryAfter: 10 * 24 * 3600}, &fakeReply{Status: 408}, &fakeReply{Status: 502, Text: "still down"})
	id := file(t, s, Values{"subject": "x"})
	if n, _ := s.Drain(ctx); n != 1 {
		t.Fatal("the first attempt was not made")
	}
	if row := mustRows(t, s, id)[0]; row.Status != ActionPending || row.DueAt != start.Add(24*time.Hour).Unix() || row.ErrorCode != CodeTryLater {
		t.Errorf("a receiver that asks for ten days gets one: %s", dump(row))
	}
	now = now.Add(24 * time.Hour)
	if n, _ := s.Drain(ctx); n != 1 {
		t.Fatal("the second attempt was not made")
	}
	if row := mustRows(t, s, id)[0]; row.Attempts != 2 || row.DueAt != now.Unix()+2 {
		t.Errorf("after a 408 the engine's own wait: %s", dump(row))
	}
	now = now.Add(2 * time.Second)
	if n, _ := s.Drain(ctx); n != 1 {
		t.Fatal("the third attempt was not made")
	}
	row := mustRows(t, s, id)[0]
	if row.Status != ActionDead || row.Attempts != 3 || row.ErrorCode != CodeTryLater || row.ErrorMessage != "the receiver answered 502: still down" {
		t.Errorf("after the last attempt: %s", dump(row))
	}
	events, _ := s.Events(ctx, "Memo", id)
	if last := events[len(events)-1]; last.Kind != EventActionFailed || last.Code != CodeTryLater || last.Name != "http.call" || len(events) != 3 {
		t.Errorf("the events: %s", dump(events))
	}
	if len(net.take()) != 3 {
		t.Error("three attempts are three requests")
	}
	// A long reply is quoted to its start, on one line.
	net.script(&fakeReply{Status: 400, Text: "line one\nline two " + strings.Repeat("x", 500)})
	out, _ := s.TryAction(ctx, "http.call", Values{"url": "https://hooks.example/memos"})
	if out.Code != CodeRefused || strings.Contains(out.Message, "\n") || !strings.HasSuffix(out.Message, "…") || len([]rune(out.Message)) > 260 {
		t.Errorf("the message of a long reply: %q", out.Message)
	}
	// What http.call returns is the status and the reply when it is JSON.
	net.script(&fakeReply{Status: 201, Body: json.RawMessage(`{"id": 7}`)}, &fakeReply{Status: 204})
	if out, _ := s.TryAction(ctx, "http.call", Values{"url": "https://hooks.example/memos"}); !out.OK || out.Result["status"] != float64(201) || string(out.Result["body"].(json.RawMessage)) != `{"id": 7}` {
		t.Errorf("what http.call returns: %s", dump(out))
	}
	if out, _ := s.TryAction(ctx, "http.call", Values{"url": "https://hooks.example/memos", "method": "DELETE"}); !out.OK || out.Result["status"] != float64(204) || out.Result["body"] != nil {
		t.Errorf("a reply without a body: %s", dump(out))
	}
	if sent := net.take(); len(sent) != 3 || sent[2].Method != "DELETE" || sent[2].Body != nil {
		t.Errorf("a DELETE carries no body: %s", dump(sent))
	}
}

// What a definition's result formula cannot read is a bad result, not a
// failed action: the request was sent and answered.
func TestRequests_AResultThatDoesNotRead(t *testing.T) {
	net := network()
	s, _ := openStore(t, Options{Outbound: net})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "rates.get", "params": {"pair": "text"}, "returns": {"rate": "decimal(4)", "day": "date", "raw": "json", "code": "number"},
 "request": {"method": "GET", "url": "\"https://hooks.example/rate?pair=\" & URLENCODE(pair)"},
 "ok": "STATUS = 200",
 "result": {"rate": "DECIMALVALUE(JSONVALUE(BODY, \"rate\"), 4)", "day": "DATEVALUE(JSONVALUE(BODY, \"day\"))", "raw": "BODY", "code": "STATUS"}}`)); err != nil {
		t.Fatal(err)
	}
	net.script(&fakeReply{Body: json.RawMessage(`{"rate": "4.25", "day": "2026-03-01"}`)},
		&fakeReply{Body: json.RawMessage(`{"rate": "about four", "day": "2026-03-01"}`)},
		&fakeReply{Text: "<html>not json</html>"},
		&fakeReply{Status: 202})
	out, err := s.TryAction(ctx, "rates.get", Values{"pair": "EUR/PLN"})
	if got, _ := json.Marshal(out); err != nil || string(got) != `{"action":"rates.get","ok":true,"result":{"code":200,"day":"2026-03-01","rate":"4.2500","raw":{"rate":"4.25","day":"2026-03-01"}}}` {
		t.Errorf("a result: %v, %s", err, got)
	}
	if out, _ := s.TryAction(ctx, "rates.get", Values{"pair": "EUR/PLN"}); !out.OK || out.Code != CodeBadResult || !strings.Contains(out.Message, "rate") || out.Result != nil {
		t.Errorf("a reply the result cannot read: %s", dump(out))
	}
	// A reply that is not JSON has a blank body: what reads it is blank.
	if out, _ := s.TryAction(ctx, "rates.get", Values{"pair": "EUR/PLN"}); !out.OK || out.Code != "" || out.Result["rate"] != nil || out.Result["raw"] != nil || out.Result["code"] != float64(200) {
		t.Errorf("a reply that is not JSON: %s", dump(out))
	}
	// ok decides, whatever the status: 202 is a success for the transport
	// and not for this definition.
	if out, _ := s.TryAction(ctx, "rates.get", Values{"pair": "EUR/PLN"}); out.OK || out.Code != CodeNotOK || out.Retry {
		t.Errorf("a 2xx the definition does not take: %s", dump(out))
	}
}

// What a process lacks to send a request is no answer, with the usual
// retries, so that a process that has it can still run the row
// (ADR-0027, ADR-0045 item 7): the leave to send, the connection, a
// secret. A host that is not allowed is an answer: dead at once.
func TestRequests_WhatAProcessLacksIsNoAnswer(t *testing.T) {
	now := start
	clock := func() time.Time { return now }
	full := network()
	s, path := openStore(t, Options{Now: clock, Outbound: full, Secrets: fixedSecrets(map[string]string{"slack/token": "xoxb-1"})})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(slackPost)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(memoClass, "slack.post", `{"channel": "to", "text": "subject"}`))); err != nil {
		t.Fatal(err)
	}
	id := file(t, s, Values{"subject": "x"})
	s.Close()
	attempt := 0
	for _, tc := range []struct {
		name string
		o    Options
		want string
	}{
		{"no leave to send", Options{}, CodeOutboundOff},
		{"a sender that does not know the connection", Options{Outbound: &fakeOutbound{hosts: []string{"slack.com"}}, Secrets: fixedSecrets(map[string]string{"slack/token": "xoxb-1"})}, CodeUnknownConnection},
		{"no supplier of secrets", Options{Outbound: network()}, CodeSecretUnavailable},
		{"a supplier without the secret", Options{Outbound: network(), Secrets: fixedSecrets(nil)}, CodeSecretUnavailable},
	} {
		tc.o.Now, tc.o.Outbox.Manual = clock, true
		other, err := Open(path, tc.o)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := other.Drain(ctx); err != nil || n != 1 {
			t.Fatalf("%s: drain %d, %v", tc.name, n, err)
		}
		attempt++
		if row := mustRows(t, other, id)[0]; row.Status != ActionPending || row.Attempts != attempt || row.ErrorCode != tc.want {
			t.Errorf("%s: %s", tc.name, dump(row))
		}
		// The catalogue knows what the process was given, not what its
		// supplier of secrets holds: it asks for no secret.
		if a, _ := other.Action("slack.post"); a.Available != (tc.name == "a supplier without the secret") {
			t.Errorf("%s: the catalogue says available %v", tc.name, a.Available)
		}
		other.Close()
		now = now.Add(time.Duration(1<<attempt) * time.Second)
	}
	// The process that has all three sends it.
	again, err := Open(path, Options{Now: clock, Outbox: OutboxOptions{Manual: true}, Outbound: full, Secrets: fixedSecrets(map[string]string{"slack/token": "xoxb-1"})})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	full.script(&fakeReply{Body: json.RawMessage(`{"ok": true}`)})
	if n, err := again.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if row := mustRows(t, again, id)[0]; row.Status != ActionDone || row.Attempts != 5 || len(full.take()) != 1 {
		t.Errorf("the row in the process that can send: %s", dump(row))
	}
}
