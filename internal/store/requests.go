package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// A declared action is a definition with a request, which the engine
// sends (ADR-0045, items 3 to 7). The store builds the request from the
// arguments the commit stored: the address, the headers and the body are
// pure formulas over them and over the secrets of the definition's
// connection, which the host program supplies at that moment and the
// engine never stores. It hands the request to the Outbound the host
// program opened the database with, and reads the reply: 2xx and ok is
// the action run; no reply, 408, 429 and 5xx are no answer, asked again;
// anything else is a refusal. The store itself never touches the
// network: without an Outbound nothing is sent.

// Outbound sends the HTTP requests of declared actions (ADR-0045, item
// 7). Whether a process may reach the network is its host program's
// decision, so the engine sends through what the host gives it and
// through nothing else: package fado/outbound makes one that calls only
// the hosts the program lists.
type Outbound interface {
	// Check says whether a request of a definition with the connection, ""
	// for none, may go to the address: nil, or an *Error with the code
	// unknown_connection or host_not_allowed. An empty address checks the
	// connection alone.
	Check(connection, url string) error
	// Send sends the request and returns the reply, whatever its status.
	// An *Error is a refusal to send, which asking again would not change,
	// such as host_not_allowed; any other error is no reply. Neither names
	// the address beyond its host: an address may carry a secret.
	Send(ctx context.Context, req *OutboundRequest) (*OutboundReply, error)
}

// OutboundRequest is one request of a declared action, as the engine
// built it.
type OutboundRequest struct {
	Method string
	URL    string
	// Headers are the headers in the order they are sent.
	Headers []Header
	Body    []byte
	// Connection is the connection of the action's definition, "" for
	// none. A request with a connection may carry its secrets and goes
	// only to the hosts the host program names for that connection; one
	// without carries none.
	Connection string
}

// Header is one header of a request.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// OutboundReply is the reply to a request.
type OutboundReply struct {
	Status int
	Body   []byte
	// RetryAfter is how long the reply asks the sender to wait, from its
	// Retry-After header; 0 when it does not say.
	RetryAfter time.Duration
}

// RequestPreview is the request a declared action would send, as a
// preview shows it (ADR-0045, item 9): every secret is masked, and
// nothing was sent.
type RequestPreview struct {
	Method     string   `json:"method"`
	URL        string   `json:"url"`
	Headers    []Header `json:"headers"`
	Body       string   `json:"body,omitempty"`
	Connection string   `json:"connection,omitempty"`
}

// The engine's own headers on a request (ADR-0045, item 4), and the mask
// a preview shows in the place of a secret.
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderEvent          = "FadoDB-Event"
	HeaderAttempt        = "FadoDB-Attempt"
	HeaderSignature      = "FadoDB-Signature"
	secretMask           = "***"
	// maxRetryAfter bounds how far a reply's Retry-After may move the next
	// attempt.
	maxRetryAfter = 24 * time.Hour
	// replySnippet is how much of a reply a failure's message quotes.
	replySnippet = 200
)

// outcome is what became of one run of an action that leaves the
// database: what it returned, or the failure, whether the failure is no
// answer, to be asked again, and how long the receiver asked to wait.
// badResult says the action ran and what it returned does not fit its
// definition.
type outcome struct {
	result    Values
	failure   *Error
	retry     bool
	after     time.Duration
	badResult *Error
}

func refusal(e *Error) outcome { return outcome{failure: e} }
func noReply(e *Error) outcome { return outcome{failure: e, retry: true} }
func (o outcome) failed() bool { return o.failure != nil }
func outboundOff(name string) *Error {
	return hintf(CodeOutboundOff, "", "this process sends no requests, and "+name+" is sent as one", "the host program allows outbound calls when it opens the database, with Options.Outbound (ADR-0045)")
}

// sending is one request being built: the action, the call, the values of
// the parameters and of the secrets it reads.
type sending struct {
	s      *Store
	spec   *model.ActionSpec
	call   *ActionCall
	params []formula.Value
	// secrets are the values of the connection's secrets by upper-cased
	// name, and tainted every text built from one, which no message may
	// repeat.
	secrets map[string]string
	tainted []string
	e       *evaluator
}

// send sends the request of a declared action for a call and reads the
// reply (ADR-0045, item 5).
func (s *Store) send(ctx context.Context, a *action, call *ActionCall) outcome {
	if s.outbound == nil {
		return noReply(outboundOff(a.spec.Name))
	}
	x := &sending{s: s, spec: a.spec, call: call, e: s.reader()}
	defer s.release(x.e)
	if fail, retry := x.fetchSecrets(ctx, a.reads, false); fail != nil {
		return outcome{failure: fail, retry: retry}
	}
	req, fail := x.request(false)
	if fail != nil {
		return refusal(fail)
	}
	reply, err := s.outbound.Send(ctx, req)
	if err != nil {
		var refused *Error
		if errors.As(err, &refused) {
			out := refusal(&Error{Code: refused.Code, Message: x.redact(refused.Message), Hint: refused.Hint})
			// A connection this process was not given is something it lacks,
			// as a function or a secret is: another process may have it.
			out.retry = refused.Code == CodeUnknownConnection
			return out
		}
		return noReply(&Error{Code: CodeNoReply, Message: x.redact("no reply from " + hostOf(req.URL) + ": " + err.Error())})
	}
	status := reply.Status
	said := "the receiver answered " + strconv.Itoa(status) + x.quote(reply.Body)
	switch {
	case status == 408 || status == 429 || status >= 500:
		// A reply that says try again: later, and no sooner than it asks.
		return outcome{failure: &Error{Code: CodeTryLater, Message: said}, retry: true, after: min(max(reply.RetryAfter, 0), maxRetryAfter)}
	case status < 200 || status > 299:
		return refusal(&Error{Code: CodeRefused, Message: said})
	}
	body := formula.Blank()
	if trimmed := bytes.TrimSpace(reply.Body); len(trimmed) > 0 && json.Valid(trimmed) {
		body = formula.JSON(string(trimmed))
	}
	over := []formula.Value{formula.Number(float64(status)), body}
	if a.spec.OK != nil {
		if val, err := x.run(a.spec.OK, over, replySlots); err != nil || !val.Truth() {
			return refusal(&Error{Code: CodeNotOK, Message: "the receiver answered " + strconv.Itoa(status) + " and the definition's ok is not TRUE for the reply" + x.quote(reply.Body)})
		}
	}
	// What the action returns: status and body for http.call, and for a
	// definition what its result formulas read out of the reply.
	out := outcome{}
	if a.spec.Request.Dynamic {
		out.result = Values{"status": float64(status), "body": valueOut(body)}
		return out
	}
	if len(a.spec.Returns) > 0 {
		out.result = make(Values, len(a.spec.Returns))
		for _, r := range a.spec.Returns {
			out.result[r.Name] = nil
		}
	}
	for _, rv := range a.spec.Result {
		val, err := x.run(rv.Formula, over, replySlots)
		if err != nil {
			out.badResult = &Error{Code: CodeBadResult, Variable: rv.Name, Message: "the formula of " + rv.Name + " failed on the reply: " + err.Message}
			continue
		}
		out.result[rv.Name] = valueOut(val)
	}
	return out
}

// replySlots names the values the formulas over a reply read, in the
// order send gives them.
var replySlots = []string{model.ReplyStatus, model.ReplyBody}

// fetchSecrets asks the host program for the secrets the request reads,
// at every attempt (ADR-0045, item 6): a provider that renews a token
// serves it as it serves a fixed one. A provider that fails, or has no
// value, is no answer. A preview asks for none and shows the mask.
func (x *sending) fetchSecrets(ctx context.Context, names []string, preview bool) (*Error, bool) {
	x.secrets = make(map[string]string, len(names))
	for _, name := range names {
		if preview {
			x.secrets[strings.ToUpper(name)] = secretMask
			continue
		}
		if x.s.secretFn == nil {
			return hintf(CodeSecretUnavailable, name, "this process supplies no secrets, and "+x.spec.Name+" reads "+name+" of the connection "+x.spec.Connection,
				"the host program gives Open a function for them, Options.Secrets"), true
		}
		value, err := x.s.secretFn(ctx, x.spec.Connection, name)
		if err == nil && value == "" {
			err = errors.New("it has no value")
		}
		if err != nil {
			return &Error{Code: CodeSecretUnavailable, Variable: name, Message: "the host program did not supply the secret " + name + " of the connection " + x.spec.Connection + ": " + err.Error()}, true
		}
		x.secrets[strings.ToUpper(name)] = value
		x.tainted = append(x.tainted, value)
	}
	return nil, false
}

// run evaluates one formula of the request or of the reply. A slot is a
// secret, a parameter of the action, or with over one of the values
// named.
func (x *sending) run(prog *formula.Program, over []formula.Value, names []string) (formula.Value, *Error) {
	if cap(x.e.vars) < len(prog.Slots) {
		x.e.vars = make([]formula.Value, len(prog.Slots))
	}
	vars := x.e.vars[:len(prog.Slots)]
	secret := false
slots:
	for i, slot := range prog.Slots {
		vars[i] = formula.Blank()
		if name, ok := strings.CutPrefix(slot.Name, formula.SecretSlot); ok {
			vars[i], secret = formula.Text(x.secrets[strings.ToUpper(name)]), true
			continue
		}
		for j, name := range names {
			if strings.EqualFold(name, slot.Name) {
				vars[i] = over[j]
				continue slots
			}
		}
		if over != nil {
			continue
		}
		for j := range x.spec.Params {
			if strings.EqualFold(x.spec.Params[j].Name, slot.Name) {
				vars[i] = x.params[j]
			}
		}
	}
	val, err := x.e.vm.Run(prog, vars, &x.e.ctx)
	if err != nil {
		return formula.Value{}, evalErr("", err)
	}
	if secret && val.Kind() == formula.KindText && val.Str() != "" {
		x.tainted = append(x.tainted, val.Str())
	}
	return val, nil
}

// secretAddress reports whether the address of the request reads a
// secret: the host program keeps the address itself, as it does for a
// receiver whose address is its credential.
func (x *sending) secretAddress() bool {
	rd := x.spec.Request
	if rd.Dynamic || rd.URL == nil {
		return false
	}
	for _, slot := range rd.URL.Slots {
		if strings.HasPrefix(slot.Name, formula.SecretSlot) {
			return true
		}
	}
	return false
}

// template makes the JSON value of a body template over the parameters.
func (x *sending) template(t *model.Template) (any, *Error) {
	switch {
	case t.Formula != nil:
		val, err := x.run(t.Formula, nil, nil)
		if err != nil {
			return nil, err
		}
		return valueOut(val), nil
	case t.Object:
		out := make(map[string]any, len(t.Fields))
		for _, f := range t.Fields {
			val, err := x.template(f.Value)
			if err != nil {
				return nil, err
			}
			out[f.Name] = val
		}
		return out, nil
	case t.Array:
		out := make([]any, len(t.Items))
		for i, item := range t.Items {
			val, err := x.template(item)
			if err != nil {
				return nil, err
			}
			out[i] = val
		}
		return out, nil
	}
	return t.Literal, nil
}

// request builds the request of the call (ADR-0045, items 3 and 4): the
// address, the headers and the body from the definition's formulas, or
// for http.call from the arguments themselves; the call itself as the
// body of a request that has none to give; the engine's headers; and the
// signature. What cannot be a request is a refusal. preview masks the
// signature, since the secret that makes it was not fetched.
func (x *sending) request(preview bool) (*OutboundRequest, *Error) {
	spec, rd := x.spec, x.spec.Request
	// The parameters, typed, by the order the definition declares them.
	x.params = make([]formula.Value, len(spec.Params))
	for i := range spec.Params {
		p := &spec.Params[i]
		val, err := toValue(x.call.Args[p.Name], p.Type, "", p.Name, x.s.limits)
		if err != nil {
			return nil, err
		}
		x.params[i] = val
	}
	req := &OutboundRequest{Method: rd.Method, Connection: spec.Connection}
	var body []byte
	contentType, hasBody := "", false
	bad := func(variable, message string) (*OutboundRequest, *Error) {
		return nil, &Error{Code: CodeBadValue, Variable: variable, Message: message}
	}
	if rd.Dynamic {
		// http.call: the address, the method, the headers and the body are
		// the arguments.
		req.URL = x.params[0].Str()
		if m := x.params[1]; m.IsBlank() {
			req.Method = "POST"
		} else if method, _, ok := model.RequestMethod(m.Str()); ok {
			req.Method = method
		} else {
			return bad("method", "unknown method "+strconv.Quote(m.Str())+"; one of GET, POST, PUT, PATCH, DELETE")
		}
		if h := x.params[2]; !h.IsBlank() {
			var given map[string]any
			dec := json.NewDecoder(strings.NewReader(h.Str()))
			dec.UseNumber()
			if err := dec.Decode(&given); err != nil {
				return bad("headers", "the headers are an object of header names to their values")
			}
			names := make([]string, 0, len(given))
			for name := range given {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if reason := model.HeaderNameReason(name); reason != "" {
					return bad("headers", reason+": "+strconv.Quote(name))
				}
				switch v := given[name].(type) {
				case nil:
				case string:
					req.Headers = append(req.Headers, Header{Name: name, Value: v})
				case json.Number:
					req.Headers = append(req.Headers, Header{Name: name, Value: v.String()})
				case bool:
					req.Headers = append(req.Headers, Header{Name: name, Value: strings.ToUpper(strconv.FormatBool(v))})
				default:
					return bad("headers", "the value of the header "+name+" is a text, a number or a bool")
				}
			}
		}
		if b := x.params[3]; !b.IsBlank() {
			body, contentType, hasBody = []byte(b.Str()), "application/json", true
		}
	} else {
		val, err := x.run(rd.URL, nil, nil)
		if err != nil {
			err.Variable = "url"
			return nil, err
		}
		req.URL = val.Str()
		for _, h := range rd.Headers {
			val, err := x.run(h.Value, nil, nil)
			if err != nil {
				err.Variable = h.Name
				return nil, err
			}
			if !val.IsBlank() {
				req.Headers = append(req.Headers, Header{Name: h.Name, Value: val.Str()})
			}
		}
		if rd.Body != nil {
			made, err := x.template(rd.Body)
			if err != nil {
				err.Variable = "body"
				return nil, err
			}
			// A body that is one formula giving a text is sent as that text,
			// for a receiver that does not read JSON; any other is JSON.
			if text, isText := made.(string); isText && rd.Body.Formula != nil {
				body, contentType, hasBody = []byte(text), "text/plain; charset=utf-8", true
			} else {
				raw, jerr := json.Marshal(made)
				if jerr != nil {
					return bad("body", "the body does not encode: "+jerr.Error())
				}
				body, contentType, hasBody = raw, "application/json", true
			}
		}
	}
	// An address that is a secret of the connection is masked in a
	// preview, which cannot judge it.
	if reason := model.URLReason(req.URL); reason != "" && !(preview && x.secretAddress()) {
		return bad("url", reason)
	}
	for _, h := range req.Headers {
		if strings.ContainsAny(h.Value, "\r\n") {
			return bad(h.Name, "the value of a header is one line")
		}
	}
	// A request with nothing of its own to say carries the call itself:
	// the arguments and the event that fired the rule (item 4).
	if _, takes, _ := model.RequestMethod(req.Method); !hasBody && takes {
		raw, jerr := json.Marshal(x.call)
		if jerr != nil {
			return bad("", "the call does not encode: "+jerr.Error())
		}
		body, contentType, hasBody = raw, "application/json", true
	}
	req.Body = body
	given := func(name string) bool {
		for _, h := range req.Headers {
			if strings.EqualFold(h.Name, name) {
				return true
			}
		}
		return false
	}
	if hasBody && !given("Content-Type") {
		req.Headers = append(req.Headers, Header{Name: "Content-Type", Value: contentType})
	}
	if !given("User-Agent") {
		req.Headers = append(req.Headers, Header{Name: "User-Agent", Value: "FadoDB"})
	}
	req.Headers = append(req.Headers, Header{Name: HeaderIdempotencyKey, Value: x.call.Key})
	if x.call.Event != nil {
		req.Headers = append(req.Headers, Header{Name: HeaderEvent, Value: x.call.Event.ID})
	}
	req.Headers = append(req.Headers, Header{Name: HeaderAttempt, Value: strconv.Itoa(x.call.Attempt)})
	if rd.Sign != "" {
		at := strconv.FormatInt(x.s.now().Unix(), 10)
		signature := secretMask
		if !preview {
			signature = Sign(x.secrets[strings.ToUpper(rd.Sign)], at, body)
		}
		req.Headers = append(req.Headers, Header{Name: HeaderSignature, Value: "t=" + at + ",v1=" + signature})
	}
	return req, nil
}

// Sign is the signature of a request (ADR-0045, item 4): the HMAC-SHA256,
// in hex, of the timestamp, a dot and the body, keyed with the secret.
// The engine sends it as "FadoDB-Signature: t=<timestamp>,v1=<signature>",
// the timestamp in seconds since the epoch. A receiver computes it over
// the body as received and compares in constant time, and refuses a
// timestamp too far from its own clock.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks the FadoDB-Signature header of a request a
// receiver was sent, against the body as received and the secret the
// two sides share: the signature is the one Sign gives, and the timestamp
// is within the tolerance of now. It is what a receiver written in Go
// calls; one in another language does the same three steps.
func VerifySignature(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var at, signature string
	for _, part := range strings.Split(header, ",") {
		switch name, value, _ := strings.Cut(strings.TrimSpace(part), "="); name {
		case "t":
			at = value
		case "v1":
			signature = value
		}
	}
	secs, err := strconv.ParseInt(at, 10, 64)
	if err != nil || signature == "" {
		return errors.New("the signature header is not t=<timestamp>,v1=<signature>")
	}
	if !hmac.Equal([]byte(signature), []byte(Sign(secret, at, body))) {
		return errors.New("the signature does not match the body")
	}
	if age := now.Sub(time.Unix(secs, 0)); age > tolerance || age < -tolerance {
		return errors.New("the signature is too old or too new")
	}
	return nil
}

// redact takes every secret, and every text built from one, out of a
// message: the engine stores no secret, in an error as anywhere else
// (ADR-0045, item 6).
func (x *sending) redact(message string) string {
	for _, secret := range x.tainted {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, secretMask)
		}
	}
	return message
}

// quote is the start of a reply for a failure's message, on one line and
// with no secret in it; "" for an empty reply.
func (x *sending) quote(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	// The secrets come out before the cut, so that none survives as a
	// fragment at the end.
	text := x.redact(strings.ToValidUTF8(string(body), "?"))
	text = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	if utf8.RuneCountInString(text) > replySnippet {
		text = string([]rune(text)[:replySnippet]) + "…"
	}
	return ": " + text
}

// hostOf is the host of an address, for a message: the rest of an address
// may carry a secret.
func hostOf(raw string) string {
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	return rest
}

// previewRequest is the request a declared action would send for a call,
// with every secret masked and none fetched, and whether the outbound
// rules would let it go.
func (s *Store) previewRequest(a *action, call *ActionCall) (*RequestPreview, *Error) {
	x := &sending{s: s, spec: a.spec, call: call, e: s.reader()}
	defer s.release(x.e)
	_, _ = x.fetchSecrets(context.Background(), a.reads, true)
	req, fail := x.request(true)
	if fail != nil {
		return nil, fail
	}
	if s.outbound != nil && !x.secretAddress() {
		if err := s.outbound.Check(req.Connection, req.URL); err != nil {
			return nil, asError(err)
		}
	}
	return &RequestPreview{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: string(req.Body), Connection: req.Connection}, nil
}

// secretsRead lists the secrets the request of a definition reads, as its
// connection declares them: in its address, its headers, its body and its
// signature.
func secretsRead(spec *model.ActionSpec) []string {
	if spec.Request == nil {
		return nil
	}
	read := map[string]bool{}
	note := func(prog *formula.Program) {
		if prog == nil {
			return
		}
		for _, slot := range prog.Slots {
			if name, ok := strings.CutPrefix(slot.Name, formula.SecretSlot); ok {
				read[strings.ToUpper(name)] = true
			}
		}
	}
	var walk func(t *model.Template)
	walk = func(t *model.Template) {
		if t == nil {
			return
		}
		note(t.Formula)
		for _, f := range t.Fields {
			walk(f.Value)
		}
		for _, item := range t.Items {
			walk(item)
		}
	}
	rd := spec.Request
	note(rd.URL)
	for _, h := range rd.Headers {
		note(h.Value)
	}
	walk(rd.Body)
	if rd.Sign != "" {
		read[strings.ToUpper(rd.Sign)] = true
	}
	var out []string
	for _, name := range spec.Secrets {
		if read[strings.ToUpper(name)] {
			out = append(out, name)
		}
	}
	return out
}
