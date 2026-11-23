package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
)

// The door over HTTP, the protocol's Streamable HTTP transport: an
// agent's program posts one message to the door's address and is
// answered in the reply to that request. The door answers with one JSON
// object and never with a stream, which both forms of the protocol
// allow. It keeps no session, so each request carries all the door
// needs: one of the modern form says its version in its params and in
// the header MCP-Protocol-Version, one of the greeted form in the
// header alone, and a greeting is answered and kept nowhere.

const (
	// codeHeaderMismatch is the code of a request whose headers do not
	// say what its body says, or that lacks one the modern revision
	// asks for.
	codeHeaderMismatch = -32020
	// codeInternal is the code of an answer the door could not write.
	codeInternal = -32603
)

// assumedVersion is the revision of a request of the greeted form that
// names none in its header: the protocol says to take it for the first
// revision that spoke over HTTP this way.
const assumedVersion = "2025-03-26"

// Posted is one message an agent's program posted to the door, with the
// headers of the protocol that came with it.
type Posted struct {
	// Body is the body of the request: one message of the protocol.
	Body []byte
	// Version, Method and Name are the headers MCP-Protocol-Version,
	// Mcp-Method and Mcp-Name, as they were sent.
	Version, Method, Name string
	// Caller says who posted it, as the server knows them, so that a
	// notice that stops a call stops only a call of theirs.
	Caller string
}

// Answered is the door's answer to a posted message: the HTTP status,
// and the body, of which an answer of 202 has none.
type Answered struct {
	Status int
	Body   []byte
	// Tool names the tool a call was for, and is empty for every other
	// message, so that a server counts a call with its command.
	Tool string
}

// underway is a call of a tool that came over HTTP, with what stops it.
type underway struct{ stop context.CancelFunc }

// Exchange answers one message posted to the door. send carries the
// command of a call of a tool as whoever posted the message; the server
// that holds the door has already asked who that is.
//
// A request that is not one message of the protocol, or whose version
// or headers are wrong, is answered 400. A method the door does not
// have is answered 404 in the modern form, as that revision asks, and
// 200 in the greeted form, to which a 404 would say that its session
// ended. A notice, and an answer to a request, are taken with 202. Every
// other answer, a refused command among them, is 200.
func (d *Door) Exchange(ctx context.Context, send Send, m Posted) Answered {
	text := bytes.TrimSpace(m.Body)
	switch {
	case len(text) == 0:
		return refused(http.StatusBadRequest, nil, codeInvalidRequest, "the request has no body: the body is one message of the protocol", nil)
	case text[0] == '[':
		return refused(http.StatusBadRequest, nil, codeInvalidRequest, "the door takes one message to a request, and no list of messages", nil)
	}
	var msg message
	if err := json.Unmarshal(text, &msg); err != nil {
		return refused(http.StatusBadRequest, nil, codeParse, "the body is not one JSON object: "+err.Error(), nil)
	}
	if msg.Method == "" {
		if len(msg.Result) > 0 || len(msg.Error) > 0 {
			// An answer to a request. The door sends none, so nothing
			// waits for it.
			return Answered{Status: http.StatusAccepted}
		}
		return refused(http.StatusBadRequest, msg.ID, codeInvalidRequest, "the message names no method", nil)
	}
	var p params
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return refused(http.StatusBadRequest, msg.ID, codeInvalidParams, "the params of "+msg.Method+" are not read: "+err.Error(), nil)
		}
	}
	if len(msg.ID) == 0 {
		// A notice: taken, and never answered. One of them stops a call.
		if msg.Method == "notifications/cancelled" {
			d.stopCall(m.Caller, string(p.RequestID))
		}
		return Answered{Status: http.StatusAccepted}
	}
	if c := msg.ID[0]; c != '"' && c != '-' && (c < '0' || c > '9') {
		return refused(http.StatusBadRequest, nil, codeInvalidRequest, "the id of a request is a text or a number", nil)
	}
	if msg.Method == "initialize" {
		// The greeting is answered, and nothing of it is kept: the
		// requests after it say their revision in the header.
		_, result := d.greet(p.ProtocolVersion)
		return d.answered(msg.ID, false, result, "")
	}
	modern, wrong := overHTTP(&p, msg.Method, m)
	if wrong != nil {
		return refused(http.StatusBadRequest, msg.ID, wrong.Code, wrong.Message, wrong.Data)
	}
	if msg.Method == "tools/call" {
		return d.callOver(ctx, send, msg.ID, modern, &p, m.Caller)
	}
	result, f := d.answerOf(msg.Method, modern, &p)
	if f != nil {
		status := http.StatusOK
		if f.Code == codeNoMethod && modern {
			status = http.StatusNotFound
		}
		return refused(status, msg.ID, f.Code, f.Message, f.Data)
	}
	return d.answered(msg.ID, modern, result, "")
}

// overHTTP says which form of the protocol a request posted over HTTP
// is in, from what it says of its version in its params and in its
// headers, or why it is refused.
func overHTTP(p *params, method string, m Posted) (modern bool, wrong *failure) {
	raw, said := p.Meta[metaVersion]
	if !said {
		version := m.Version
		if version == "" {
			version = assumedVersion
		}
		switch {
		case version == Modern:
			return false, &failure{Code: codeInvalidParams,
				Message: "the request says " + Modern + " in its header and not in params, in _meta, as " + metaVersion + ": a request of that revision says it in both"}
		case !isGreeted(version):
			return false, &failure{Code: codeVersion, Message: "Unsupported protocol version",
				Data: map[string]any{"supported": Versions(), "requested": version}}
		}
		return false, nil
	}
	var version string
	if json.Unmarshal(raw, &version) != nil || version != Modern {
		return false, &failure{Code: codeVersion, Message: "Unsupported protocol version",
			Data: map[string]any{"supported": Versions(), "requested": json.RawMessage(raw)}}
	}
	if _, said := p.Meta[metaCapabilities]; !said {
		return false, &failure{Code: codeInvalidParams,
			Message: "the request does not say what its sender can do: say it in params, in _meta, as " + metaCapabilities + ", which may be {}"}
	}
	switch {
	case m.Version != version:
		return false, mismatch("MCP-Protocol-Version", m.Version, version)
	case m.Method != method:
		return false, mismatch("Mcp-Method", m.Method, method)
	case method == "tools/call" && headerText(m.Name) != p.Name:
		return false, mismatch("Mcp-Name", m.Name, p.Name)
	}
	return true, nil
}

// isGreeted reports whether a revision is one of those that open with a
// greeting.
func isGreeted(version string) bool {
	for _, v := range greeted() {
		if v == version {
			return true
		}
	}
	return false
}

// mismatch is the failure of a request of the modern form whose header
// is missing or says something else than its body.
func mismatch(header, sent, body string) *failure {
	if sent == "" {
		return &failure{Code: codeHeaderMismatch,
			Message: "Header mismatch: the request has no " + header + " header, which every request of " + Modern + " carries; its body says " + strconv.Quote(body)}
	}
	return &failure{Code: codeHeaderMismatch,
		Message: "Header mismatch: " + header + " header value " + strconv.Quote(sent) + " does not match body value " + strconv.Quote(body)}
}

// headerText is the text a header of the protocol carries: a value that
// is no plain ASCII travels as =?base64?…?=.
func headerText(v string) string {
	if inner, ok := strings.CutPrefix(v, "=?base64?"); ok {
		if inner, ok = strings.CutSuffix(inner, "?="); ok {
			if b, err := base64.StdEncoding.DecodeString(inner); err == nil {
				return string(b)
			}
		}
	}
	return v
}

// callOver runs a tool for a request that came over HTTP and answers
// what the database answered. The call is stopped when the request
// ends, and when a notice of the same caller names its id.
func (d *Door) callOver(ctx context.Context, send Send, id json.RawMessage, modern bool, p *params, caller string) Answered {
	if !d.tools[p.Name] {
		return refused(http.StatusOK, id, codeInvalidParams, d.unknownTool(p.Name), nil)
	}
	object, f := arguments(p)
	if f != nil {
		return refused(http.StatusOK, id, f.Code, f.Message, nil)
	}
	running, stop := context.WithCancel(ctx)
	defer stop()
	key, call := caller+"\x00"+string(id), &underway{stop: stop}
	d.mu.Lock()
	d.running[key] = call
	d.mu.Unlock()
	text, failed := d.call(running, send, p.Name, object)
	d.mu.Lock()
	if d.running[key] == call {
		delete(d.running, key)
	}
	d.mu.Unlock()
	return d.answered(id, modern, called(text, failed), p.Name)
}

// stopCall stops the call of a caller's request, when it is still under
// way.
func (d *Door) stopCall(caller, id string) {
	d.mu.Lock()
	call := d.running[caller+"\x00"+id]
	d.mu.Unlock()
	if call != nil {
		call.stop()
	}
}

// answered is the answer 200 to a request, with its result.
func (d *Door) answered(id json.RawMessage, modern bool, result map[string]any, tool string) Answered {
	text, err := command.Marshal(d.result(id, modern, result))
	if err != nil {
		return refused(http.StatusInternalServerError, id, codeInternal, "the answer could not be written: "+err.Error(), nil)
	}
	return Answered{Status: http.StatusOK, Body: text, Tool: tool}
}

// refused is an error of the protocol as an answer, with its status.
func refused(status int, id json.RawMessage, code int, message string, data any) Answered {
	text, err := command.Marshal(protocolError(id, &failure{Code: code, Message: message, Data: data}))
	if err != nil {
		return Answered{Status: http.StatusInternalServerError,
			Body: []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"the answer could not be written"}}`)}
	}
	return Answered{Status: status, Body: text}
}
