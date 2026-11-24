package server

import (
	"context"
	"net/http"
	"strconv"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/mcp"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The door for agents at the server itself (ADR-0064, item 1): the
// commands as tools, in the Model Context Protocol over HTTP, at /mcp.
// A request proves who sends it as one at /v1/ does, with a key or a
// pass in its header, and each tool it calls runs as that caller, with
// the door's limits and in its log. A pass for /mcp is given by OAuth
// (ADR-0071); one for /v1 is refused here, and the other way round.

// AddressAgents is the address of the door for agents.
const AddressAgents = store.AddressAgents

// agentsName is the name a request to the door for agents is counted
// and logged under when it called no tool.
const agentsName = "mcp"

// serveAgents answers one request to the door for agents.
func (d *Door) serveAgents(out *reply, r *http.Request, told *Request) {
	told.Agents, told.Command = true, agentsName
	if r.Method != http.MethodPost {
		// The door opens no stream, which the modern revision has no
		// GET for, and keeps no session to end with DELETE.
		told.Code = command.CodeBadJSON
		out.w.Header().Set("Allow", http.MethodPost)
		d.agentsRefuse(out, http.StatusMethodNotAllowed, "the door for agents takes one message to a POST, and this request is a "+r.Method)
		return
	}
	// A page of another site may not send its visitor's agent's
	// requests: the protocol asks a door at an address to refuse them.
	if sent := r.Header.Get("Origin"); sent != "" {
		if own, known := d.originOf(r); !known || sent != own {
			told.Code = store.CodeForbidden
			d.agentsRefuse(out, http.StatusForbidden, "the door for agents takes no request from a page of "+strconv.Quote(sent))
			return
		}
	}
	// A key or a pass in the header, and nothing else: the session of a
	// browser is for the server's own pages, and is not asked here.
	var p store.Principal
	err := store.ErrUnknownKey
	if r.Header.Get("Authorization") != "" {
		p, err = d.who(r)
	}
	if err != nil {
		told.Code = CodeUnauthenticated
		challenge := "Bearer"
		if origin, known := d.originOf(r); known {
			challenge += ` resource_metadata="` + origin + WellKnownResource + store.AddressAgents + `"`
		}
		out.w.Header().Set("WWW-Authenticate", challenge)
		out.refusal(Status(told.Code), unauthenticated())
		return
	}
	told.User = p.ID
	if !isJSON(r.Header.Get("Content-Type")) {
		told.Code = command.CodeBadJSON
		d.agentsRefuse(out, http.StatusUnsupportedMediaType, "the body of a request to the door for agents is JSON, and this request does not say its body is: send the header Content-Type: application/json")
		return
	}
	if !d.slot(out, r, told) {
		return
	}
	held := &seat{d: d, slot: true}
	defer held.give()
	body, refusal := d.body(out.w, r)
	if refusal != nil {
		told.Code = refusal.Code
		d.agentsRefuse(out, Status(refusal.Code), refusal.Message)
		return
	}
	answered := d.agents.Exchange(r.Context(), d.sendAs(p, told, held), mcp.Posted{
		Body:    body,
		Version: r.Header.Get("MCP-Protocol-Version"),
		Method:  r.Header.Get("Mcp-Method"),
		Name:    r.Header.Get("Mcp-Name"),
		Caller:  p.ID,
	})
	if answered.Tool != "" {
		told.Command = answered.Tool
	}
	if answered.Status == http.StatusAccepted {
		out.status, out.wrote = answered.Status, true
		out.w.WriteHeader(answered.Status)
		return
	}
	out.send(answered.Status, answered.Body)
}

// sendAs carries the command of a tool as p, as a command sent to /v1/
// is carried: within the door's time, its refusal as the database wrote
// it. told is told the command's code. The tool that waits for changes
// gives the request's slot back for the place of a stream, as at /v1/,
// and runs for as long as it may wait.
func (d *Door) sendAs(p store.Principal, told *Request, held *seat) mcp.Send {
	return func(ctx context.Context, name string, object []byte) ([]byte, bool, error) {
		if name != addressWait {
			status, answer := d.run(ctx, p, name, object, told)
			return answer, status != http.StatusOK, nil
		}
		ctx, end := context.WithCancel(ctx)
		defer end()
		place, refusal := d.hold(end)
		if refusal != nil {
			told.Code = refusal.Code
			return refusalText(refusal), true, nil
		}
		held.give()
		held.place = place
		status, answer := d.runFor(ctx, d.waitTime(), p, name, object, told)
		return answer, status != http.StatusOK, nil
	}
}

// seat is what a request to the door for agents holds while it runs:
// the slot of a command, or the place of a stream while its tool waits
// for changes.
type seat struct {
	d     *Door
	slot  bool
	place *subscription
}

// give gives back what the request holds.
func (s *seat) give() {
	if s.slot {
		s.d.leave()
		s.slot = false
	}
	if s.place != nil {
		s.d.release(s.place)
		s.place = nil
	}
}

// originOf is the address the server is reached at, when it knows it.
func (d *Door) originOf(r *http.Request) (string, bool) {
	if d.origin == nil {
		return "", false
	}
	return d.origin(r)
}

// agentsRefuse writes a refusal of the door for agents before any
// message was read, as the protocol writes an error with no id.
func (d *Door) agentsRefuse(out *reply, status int, message string) {
	text, err := command.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      nil,
		"error":   map[string]any{"code": -32600, "message": message},
	})
	if err != nil {
		text = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"the request is refused"}}`)
	}
	out.send(status, text)
}
