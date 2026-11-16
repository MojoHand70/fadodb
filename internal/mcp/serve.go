package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/MojoHand70/fadodb/internal/command"
)

// The conversation of the door with one agent's program: JSON-RPC 2.0,
// a message to a line (the protocol's transport over standard input and
// output). The door answers requests and sends none of its own.

// The versions of the protocol the door speaks.
const (
	// Modern is the revision in which every request says its version
	// and nothing is remembered between two requests.
	Modern = "2026-07-28"
	// newestGreeted is the newest of the revisions that open with a
	// greeting, which the door answers to a greeting that names a
	// revision it does not know.
	newestGreeted = "2025-11-25"
)

// greeted are the revisions that open with a greeting, newest first.
func greeted() []string { return []string{newestGreeted, "2025-06-18", "2025-03-26", "2024-11-05"} }

// Versions lists the revisions of the protocol the door speaks, newest
// first.
func Versions() []string { return append([]string{Modern}, greeted()...) }

// The members of _meta that a request of the modern revision carries,
// and the one its answers carry.
const (
	metaVersion      = "io.modelcontextprotocol/protocolVersion"
	metaCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServer       = "io.modelcontextprotocol/serverInfo"
)

// The codes of the errors of the protocol itself: a message the door
// cannot take as a call of a tool. What a command answers, a refusal
// among it, is no such error: it is the answer of a tool.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeNoMethod       = -32601
	codeInvalidParams  = -32602
	codeVersion        = -32022
)

const (
	// largestMessage is the longest line the door reads. A server reads
	// 16 MiB of a command unless it was told otherwise, and says so in
	// its own words; the door reads more, so that those words are the
	// answer.
	largestMessage = 64 << 20
	// atOnce is how many calls of tools the door carries at once. The
	// next waits, and with it everything the agent's program sends.
	atOnce = 16
	// listedFor is how long an agent's program may keep the list of the
	// tools, in milliseconds: the list of a door does not change while
	// the door lives.
	listedFor = 3_600_000
)

// message is one message of the conversation as it is read.
type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	// Result and Error are of an answer to a request. The door sends no
	// request, so it reads them only to pass such a message over.
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// params are the members of the params the door reads, of every method.
type params struct {
	Meta map[string]json.RawMessage `json:"_meta"`
	// Of tools/call.
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	// Of tools/list. The door lists its tools on one page, so a place
	// to go on from is one it never gave.
	Cursor *string `json:"cursor"`
	// Of initialize.
	ProtocolVersion string `json:"protocolVersion"`
	// Of notifications/cancelled.
	RequestID json.RawMessage `json:"requestId"`
}

// failure is an error of the protocol.
type failure struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// reply is an answer of the door as it is written.
type reply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	// Result is nil in an answer that is an error, and a map otherwise:
	// an empty one is written, as {}.
	Result any      `json:"result,omitempty"`
	Error  *failure `json:"error,omitempty"`
}

// served is one conversation.
type served struct {
	door *Door
	// greeting is the revision agreed in a greeting, and "" while there
	// was none. Only the loop that reads the messages touches it.
	greeting string

	write sync.Mutex
	out   io.Writer
	// broken is why the last answer could not be written.
	broken error
	stop   context.CancelFunc

	mu sync.Mutex
	// running are the calls of tools under way, by the id of their
	// request, each with what stops it.
	running map[string]context.CancelFunc
	calls   sync.WaitGroup
	slots   chan struct{}
}

// Serve holds the conversation with one agent's program: it reads its
// messages from in, a message to a line, and writes the answers to out,
// until in ends or ctx does. The calls under way are then stopped or
// awaited, and it answers why an answer could not be written, if one
// could not.
func (d *Door) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	s := &served{door: d, out: out, stop: stop, running: map[string]context.CancelFunc{}, slots: make(chan struct{}, atOnce)}
	lines := make(chan line)
	go read(in, lines, ctx.Done())
	for open := true; open; {
		select {
		case <-ctx.Done():
			open = false
		case l, more := <-lines:
			if !more {
				open = false
				break
			}
			s.take(ctx, l)
		}
	}
	// What was asked before the end is still answered: a command that
	// was sent is one its agent wants to hear of.
	s.calls.Wait()
	s.write.Lock()
	defer s.write.Unlock()
	return s.broken
}

// line is one line of what an agent's program sent.
type line struct {
	text []byte
	// long says the line was longer than the door reads, and text is
	// empty.
	long bool
}

// read reads the lines of in into to, until in ends or done closes.
func read(in io.Reader, to chan<- line, done <-chan struct{}) {
	defer close(to)
	r := bufio.NewReaderSize(in, 64<<10)
	for {
		l, err := readLine(r, largestMessage)
		if len(l.text) > 0 || l.long {
			select {
			case to <- l:
			case <-done:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// readLine reads one line, of at most most bytes. A longer line is read
// to its end and kept no part of.
func readLine(r *bufio.Reader, most int) (line, error) {
	var l line
	for {
		part, err := r.ReadSlice('\n')
		switch {
		case l.long:
		case len(l.text)+len(part) > most:
			l.long, l.text = true, nil
		default:
			l.text = append(l.text, part...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		l.text = bytes.TrimSpace(l.text)
		return l, err
	}
}

// take reads one message and answers it. A call of a tool is carried
// beside the loop, so that a slow command holds up no other message.
func (s *served) take(ctx context.Context, l line) {
	switch {
	case l.long:
		s.fail(nil, codeInvalidRequest, "the message is longer than the door reads, which is 64 MiB", nil)
		return
	case l.text[0] == '[':
		s.fail(nil, codeInvalidRequest, "the door takes one message to a line, and no list of messages", nil)
		return
	}
	var m message
	if err := json.Unmarshal(l.text, &m); err != nil {
		s.fail(nil, codeParse, "the line is not one JSON object: "+err.Error(), nil)
		return
	}
	if m.Method == "" {
		// An answer to a request. The door sends none, so there is
		// nothing it waits for; anything else with no method is no
		// message of the protocol.
		if len(m.Result) == 0 && len(m.Error) == 0 {
			s.fail(m.ID, codeInvalidRequest, "the message names no method", nil)
		}
		return
	}
	var p params
	if len(m.Params) > 0 {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			if len(m.ID) > 0 {
				s.fail(m.ID, codeInvalidParams, "the params of "+m.Method+" are not read: "+err.Error(), nil)
			}
			return
		}
	}
	if len(m.ID) == 0 {
		// A notice: it is never answered. One of them stops a call.
		if m.Method == "notifications/cancelled" {
			s.cancel(string(p.RequestID))
		}
		return
	}
	if c := m.ID[0]; c != '"' && c != '-' && (c < '0' || c > '9') {
		s.fail(nil, codeInvalidRequest, "the id of a request is a text or a number", nil)
		return
	}

	if m.Method == "initialize" {
		// The greeting of the revisions before the modern one. The door
		// answers the revision asked for when it speaks it, and the
		// newest greeted one when it does not.
		s.greeting = newestGreeted
		for _, v := range greeted() {
			if v == p.ProtocolVersion {
				s.greeting = v
			}
		}
		s.answer(m.ID, false, map[string]any{
			"protocolVersion": s.greeting,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      s.door.info,
			"instructions":    s.door.says,
		})
		return
	}
	modern, wrong := s.era(&p)
	if wrong != nil {
		s.fail(m.ID, wrong.Code, wrong.Message, wrong.Data)
		return
	}
	switch {
	case m.Method == "server/discover" && modern:
		s.answer(m.ID, true, map[string]any{
			"supportedVersions": Versions(),
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      s.door.says,
			"ttlMs":             listedFor,
			"cacheScope":        "private",
		})
	case m.Method == "ping":
		s.answer(m.ID, modern, map[string]any{})
	case m.Method == "tools/list":
		if p.Cursor != nil {
			s.fail(m.ID, codeInvalidParams, "the door lists its tools on one page, and gave no place to go on from", nil)
			return
		}
		result := map[string]any{"tools": s.door.listed}
		if modern {
			result["ttlMs"], result["cacheScope"] = listedFor, "private"
		}
		s.answer(m.ID, modern, result)
	case m.Method == "tools/call":
		s.call(ctx, m.ID, modern, &p)
	default:
		s.fail(m.ID, codeNoMethod, "the door has no method "+m.Method+": it lists its tools at tools/list and runs one at tools/call", nil)
	}
}

// era says which form of the protocol a request is in: the modern one,
// when it says its version, or the greeted one, when a greeting came
// before it. A request that is in neither is told what to send.
func (s *served) era(p *params) (modern bool, wrong *failure) {
	raw, said := p.Meta[metaVersion]
	if !said {
		if s.greeting != "" {
			return false, nil
		}
		return false, &failure{Code: codeInvalidParams,
			Message: "the request does not say its version of the protocol: say it in params, in _meta, as " + metaVersion + ", or open with initialize"}
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
	return true, nil
}

// call runs a tool: it sends the command and answers what the database
// answered. A refusal is an answer of the tool, marked as an error.
func (s *served) call(ctx context.Context, id json.RawMessage, modern bool, p *params) {
	if !s.door.tools[p.Name] {
		s.fail(id, codeInvalidParams, s.door.unknownTool(p.Name), nil)
		return
	}
	object := bytes.TrimSpace(p.Arguments)
	switch {
	case len(object) == 0, string(object) == "null":
		object = []byte("{}")
	case object[0] != '{':
		s.fail(id, codeInvalidParams, "the arguments of a tool are one JSON object: the object of the command", nil)
		return
	}
	// The loop waits here while as many calls run as the door carries.
	s.slots <- struct{}{}
	running, stop := context.WithCancel(ctx)
	key := string(id)
	s.mu.Lock()
	s.running[key] = stop
	s.mu.Unlock()
	s.calls.Add(1)
	go func() {
		defer s.calls.Done()
		defer func() { <-s.slots }()
		text, failed := s.door.call(running, p.Name, object)
		s.mu.Lock()
		delete(s.running, key)
		s.mu.Unlock()
		// A call its agent's program stopped is given no answer: the
		// program waits for none.
		stopped := running.Err() != nil && ctx.Err() == nil
		stop()
		if stopped {
			return
		}
		s.answer(id, modern, map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
			"isError": failed,
		})
	}()
}

// cancel stops the call of a request, when it is still under way.
func (s *served) cancel(id string) {
	s.mu.Lock()
	stop := s.running[id]
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// answer writes the result of a request. A result of the modern
// revision says that it is whole and who answers.
func (s *served) answer(id json.RawMessage, modern bool, result map[string]any) {
	if modern {
		result["resultType"] = "complete"
		result["_meta"] = map[string]any{metaServer: s.door.info}
	}
	s.send(reply{JSONRPC: "2.0", ID: id, Result: result})
}

// fail writes an error of the protocol. A message whose id could not be
// read is answered with no id.
func (s *served) fail(id json.RawMessage, code int, message string, data any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	s.send(reply{JSONRPC: "2.0", ID: id, Error: &failure{Code: code, Message: message, Data: data}})
}

// send writes one message on a line of its own. When it cannot be
// written, the agent's program is gone and the conversation ends.
func (s *served) send(r reply) {
	text, err := command.Marshal(r)
	s.write.Lock()
	defer s.write.Unlock()
	if err == nil && s.broken == nil {
		_, err = s.out.Write(append(text, '\n'))
	}
	if err != nil && s.broken == nil {
		s.broken = err
		s.stop()
	}
}
