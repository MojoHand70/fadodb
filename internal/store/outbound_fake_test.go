package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"time"
)

// fakeOutbound stands for the network in the store's tests: it applies
// the one rule the store relies on, that a request goes to a host of its
// connection or, without one, to a host on the open list; records what it
// was asked to send; and answers with the replies a test scripted. The
// rules of the real one, with the addresses a host resolves to, are
// tested in package fado/outbound.
type fakeOutbound struct {
	mu          sync.Mutex
	hosts       []string
	connections map[string][]string
	replies     []*fakeReply
	sent        []*OutboundRequest
}

// fakeReply is one scripted reply: to the next request to URL, or to the
// next request at all when URL is empty. Without a status it is 200.
type fakeReply struct {
	URL    string          `json:"url,omitempty"`
	Status int             `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	// Text is a body that is not JSON.
	Text string `json:"text,omitempty"`
	// RetryAfter is the reply's Retry-After, in seconds.
	RetryAfter int `json:"retry_after,omitempty"`
	// NoReply makes the request fail without a reply, with these words.
	NoReply string `json:"no_reply,omitempty"`
	used    bool
}

func (f *fakeOutbound) Check(connection, raw string) error {
	allowed := f.hosts
	if connection != "" {
		hosts, known := f.connections[connection]
		if !known {
			return hintf(CodeUnknownConnection, "", "the host program names no connection "+connection, "it lists the connections a definition may use when it allows outbound calls")
		}
		allowed = hosts
	}
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return errf(CodeHostNotAllowed, "", "a request goes over https")
	}
	for _, host := range allowed {
		if host == u.Host {
			return nil
		}
	}
	if connection != "" {
		return errf(CodeHostNotAllowed, "", "the host %s is not one of the connection %s", u.Host, connection)
	}
	return errf(CodeHostNotAllowed, "", "the host %s is not one this process may call", u.Host)
}

func (f *fakeOutbound) Send(_ context.Context, req *OutboundRequest) (*OutboundReply, error) {
	if err := f.Check(req.Connection, req.URL); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	for _, r := range f.replies {
		if r.used || r.URL != "" && r.URL != req.URL {
			continue
		}
		r.used = true
		if r.NoReply != "" {
			return nil, errors.New(r.NoReply)
		}
		reply := &OutboundReply{Status: r.Status, Body: r.Body, RetryAfter: time.Duration(r.RetryAfter) * time.Second}
		if reply.Status == 0 {
			reply.Status = 200
		}
		if r.Text != "" {
			reply.Body = []byte(r.Text)
		}
		return reply, nil
	}
	return &OutboundReply{Status: 200}, nil
}

// take returns the requests sent since the last take.
func (f *fakeOutbound) take() []*OutboundRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

// script queues replies.
func (f *fakeOutbound) script(replies ...*fakeReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, replies...)
}

// requestJSON is a request, or the preview of one, in the shape the
// suites and the tests compare: the headers as an object by name, and the
// body as the JSON it is, or under text when it is none.
func requestJSON(method, address, connection string, headers []Header, body []byte) json.RawMessage {
	out := map[string]any{"method": method, "url": address, "connection": connection}
	hs := map[string]string{}
	for _, h := range headers {
		hs[h.Name] = h.Value
	}
	out["headers"] = hs
	switch {
	case len(body) == 0:
	case json.Valid(body):
		out["body"] = json.RawMessage(body)
	default:
		out["text"] = string(body)
	}
	return mustJSON(out)
}

func requestsJSON(reqs []*OutboundRequest) json.RawMessage {
	out := make([]json.RawMessage, len(reqs))
	for i, r := range reqs {
		out[i] = requestJSON(r.Method, r.URL, r.Connection, r.Headers, r.Body)
	}
	return mustJSON(out)
}
