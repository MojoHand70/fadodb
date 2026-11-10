package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// ActionDef is an action definition as written (ADR-0045, items 1 and
// 3): the contract of an action a rule may name. It has a name of a
// namespace and a verb, its parameters and what it returns, each by
// scalar type, and what implements it: nothing for a Go function the host
// program registers under the name, or a request the engine sends.
type ActionDef struct {
	Name        string `json:"name"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Params and Returns map a name to its type as text, or to an object
	// of type, required and description.
	Params  map[string]json.RawMessage `json:"params,omitempty"`
	Returns map[string]json.RawMessage `json:"returns,omitempty"`
	// Request makes a declared action: the HTTP request the engine sends.
	// Connection names the connection the request goes over and the
	// secrets of it the request may read. OK is the condition over the
	// reply that says the action ran, and Result maps a returned value to
	// a formula over the reply.
	Connection *ConnectionDef  `json:"connection,omitempty"`
	Request    *RequestDef     `json:"request,omitempty"`
	OK         string          `json:"ok,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	// ParamOrder and ReturnOrder list the names in the order written.
	ParamOrder  []string `json:"-"`
	ReturnOrder []string `json:"-"`
}

// ConnectionDef is the connection of a declared action as written: its
// name, by which the host program knows the hosts it reaches and its
// secrets, and the names of the secrets the request may read.
type ConnectionDef struct {
	Name    string   `json:"name"`
	Secrets []string `json:"secrets,omitempty"`
}

// RequestDef is the request of a declared action as written. URL and the
// values of Headers are formulas over the action's parameters; Body is a
// template whose every string is one; Sign names the secret that signs
// the request.
type RequestDef struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
	Sign    string            `json:"sign,omitempty"`
	// headerOrder lists the headers in the order written.
	headerOrder []string
}

// ParamDef is one parameter, or one returned value, written in full.
type ParamDef struct {
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// ActionSpec is a compiled action definition.
type ActionSpec struct {
	Name        string
	Version     int
	Label       string
	Description string
	// Params and Returns are in the order written.
	Params  []Param
	Returns []Param
	// Request is what the engine sends for a declared action; nil for an
	// action a Go function implements.
	Request *Request
	// Connection is the connection a declared action's request goes over,
	// "" for none, and Secrets the secrets of it the request may read, as
	// declared. A request with a connection is sent only to the hosts the
	// host program names for that connection, and a request without one
	// reads no secret.
	Connection string
	Secrets    []string
	// OK says whether a reply means the action ran: a condition over
	// STATUS and BODY, nil when any 2xx reply does. Result gives the
	// returned values, each a formula over STATUS and BODY, in the order
	// written.
	OK     *formula.Program
	Result []ResultValue
	Def    *ActionDef
}

// Request is the compiled request of a declared action (ADR-0045, item
// 3). Its formulas are pure and read the action's parameters and the
// secrets of its connection, so every attempt sends the same request.
type Request struct {
	// Method is GET, POST, PUT, PATCH or DELETE.
	Method string
	// URL gives the address, a text. ConstURL is the address itself when
	// the formula reads nothing, and "" otherwise.
	URL      *formula.Program
	ConstURL string
	// Headers are in the order written; a header whose formula gives a
	// blank is not sent.
	Headers []Header
	// Body makes the body. Without one a POST, a PUT and a PATCH send the
	// call itself as JSON (item 4), and a GET and a DELETE send none.
	Body *Template
	// Sign is the secret that signs the timestamp and the body, "" for no
	// signature.
	Sign string
	// Dynamic marks http.call, whose method, headers and body are
	// parameters of the call and not parts of the definition.
	Dynamic bool
}

// Header is one header of a request: its name and the formula of its
// value.
type Header struct {
	Name  string
	Value *formula.Program
}

// ResultValue is one value a declared action returns: the returned value
// it gives, as declared, and its formula over the reply.
type ResultValue struct {
	Name    string
	Formula *formula.Program
}

// Declared reports whether the engine sends the action as a request.
func (a *ActionSpec) Declared() bool { return a.Request != nil }

// The methods a request may have, and the ones that carry a body.
var requestMethods = map[string]bool{"GET": false, "POST": true, "PUT": true, "PATCH": true, "DELETE": false}

// RequestMethod reads a method as written: its canonical form, whether
// such a request carries a body, and whether it is a method at all.
func RequestMethod(m string) (method string, body, ok bool) {
	method = strings.ToUpper(m)
	body, ok = requestMethods[method]
	return method, body, ok
}

// engineHeaders are the headers the engine sets on every request it
// sends, or that the transport owns; a definition may not give them.
var engineHeaders = map[string]bool{
	"idempotency-key": true, "fadodb-event": true, "fadodb-attempt": true, "fadodb-signature": true,
	"host": true, "content-length": true, "connection": true, "transfer-encoding": true,
}

// HeaderNameReason says why a text is not a header a definition, or a
// call of http.call, may give, or "" when it is.
func HeaderNameReason(name string) string {
	if name == "" {
		return "a header has a name"
	}
	for _, r := range name {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune(`()<>@,;:\"/[]?={}`, r) {
			return "a header name is letters, digits and the punctuation of an HTTP token"
		}
	}
	if engineHeaders[strings.ToLower(name)] {
		return "the engine sets this header itself"
	}
	return ""
}

// The variables the formulas over a reply read (ADR-0045, item 3): the
// status of the reply, a number, and its body as a json value, blank when
// the reply is not JSON.
const (
	ReplyStatus = "STATUS"
	ReplyBody   = "BODY"
)

// Param is one parameter of an action, or one value it returns: a name, a
// scalar type, whether a call must give it, and what it is for.
type Param struct {
	Name        string
	Type        formula.Type
	Required    bool
	Description string
}

// Param finds a parameter by name, case-insensitively.
func (a *ActionSpec) Param(name string) (*Param, bool) { return findParam(a.Params, name) }

// Return finds a returned value by name, case-insensitively.
func (a *ActionSpec) Return(name string) (*Param, bool) { return findParam(a.Returns, name) }

func findParam(list []Param, name string) (*Param, bool) {
	for i := range list {
		if strings.EqualFold(list[i].Name, name) {
			return &list[i], true
		}
	}
	return nil, false
}

// MaxActionName is the longest name an action may have.
const MaxActionName = 100

// engineActions are the actions the engine implements, by name, with
// whether each runs inside the commit.
var engineActions = map[string]bool{
	ActionSet: true, ActionReset: true, ActionRefresh: true, ActionEmit: true,
	ActionCreate: false, ActionTransition: false,
	ActionTableInsert: false, ActionTableUpdate: false, ActionTableDelete: false,
}

// EngineAction reports whether the engine implements the action of that
// name itself. Every other action a rule names has a definition.
func EngineAction(name string) bool {
	_, ok := engineActions[name]
	return ok
}

// ActionHTTPCall is the declared action the engine defines for a call
// nobody named (ADR-0045, item 3); HTTPCall is its definition.
const ActionHTTPCall = "http.call"

// ParseAction decodes an action definition. Unknown properties are
// findings.
func ParseAction(data []byte) (*ActionDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def ActionDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	var err error
	if def.Params != nil {
		if def.ParamOrder, err = orderedKeys(mustField(data, "params")); err != nil {
			c.add(CodeBadJSON, "/params", err.Error())
		}
	}
	if def.Returns != nil {
		if def.ReturnOrder, err = orderedKeys(mustField(data, "returns")); err != nil {
			c.add(CodeBadJSON, "/returns", err.Error())
		}
	}
	if def.Request != nil && def.Request.Headers != nil {
		if def.Request.headerOrder, err = orderedKeys(mustField(mustField(data, "request"), "headers")); err != nil {
			c.add(CodeBadJSON, "/request/headers", err.Error())
		}
	}
	return &def, c.list
}

// mustField is one property of a JSON object that has decoded already.
func mustField(data []byte, name string) json.RawMessage {
	var top map[string]json.RawMessage
	_ = json.Unmarshal(data, &top)
	return top[name]
}

// CompileAction checks an action definition and compiles it.
func CompileAction(def *ActionDef) (*ActionSpec, Diagnostics) {
	c := &collector{}
	a := &ActionSpec{Name: def.Name, Version: def.Version, Label: def.Label, Description: def.Description, Def: def}
	if code, reason := actionNameReason(def.Name); reason != "" {
		c.hint(code, "/name", reason+": "+quote(def.Name), `a namespace and a verb joined by a dot, such as "slack.post"`)
	}
	if a.Version == 0 {
		a.Version = 1
	} else if a.Version < 0 {
		c.add(CodeBadValue, "/version", "the version is a positive number")
	}
	a.Params = compileParams(c, "/params", def.Params, def.ParamOrder, true)
	a.Returns = compileParams(c, "/returns", def.Returns, def.ReturnOrder, false)
	compileRequest(c, a, def)
	if c.failed() {
		return nil, c.list
	}
	return a, nil
}

// compileRequest reads what makes an action a declared one (ADR-0045,
// items 3, 4 and 6): the connection with its secrets, the request, and
// the formulas over the reply. Without a request the definition is the
// contract of a Go function and has none of them.
func compileRequest(c *collector, a *ActionSpec, def *ActionDef) {
	if def.Request == nil {
		const hint = "a definition without a request is the contract of a Go function the host program registers under its name"
		if def.Connection != nil {
			c.hint(CodeNotAllowed, "/connection", "a connection belongs to a definition with a request", hint)
		}
		if def.OK != "" {
			c.hint(CodeNotAllowed, "/ok", "ok reads the reply to a request, and the definition has no request", hint)
		}
		if len(def.Result) > 0 {
			c.hint(CodeNotAllowed, "/result", "result reads the reply to a request, and the definition has no request", hint)
		}
		return
	}
	// The connection: a name, and the secrets the request may read.
	secrets := []string{}
	if conn := def.Connection; conn != nil {
		if c.checkName("/connection/name", conn.Name, nil) {
			a.Connection = conn.Name
		}
		seen := map[string]bool{}
		for i, name := range conn.Secrets {
			path := fmt.Sprintf("/connection/secrets/%d", i)
			switch {
			case !c.checkName(path, name, nil):
			case seen[upper(name)]:
				c.addf(CodeDuplicate, path, "the secret %s is declared twice; names differ in case only", quote(name))
			default:
				seen[upper(name)] = true
				secrets = append(secrets, name)
			}
		}
		a.Secrets = secrets
	}
	vars := make([]formula.Variable, len(a.Params))
	for i, p := range a.Params {
		vars[i] = formula.Variable{Name: p.Name, Type: p.Type}
	}
	scope, err := formula.NewScope(vars...)
	if err != nil {
		c.add(CodeBadName, "/params", err.Error())
		return
	}
	// A request is pure, so that every attempt sends the same one, and
	// reads the secrets of its connection.
	bind := func(src string) (*formula.Program, error) {
		return formula.Bind(src, scope, formula.BindOptions{Mode: formula.ModePure, Secrets: secrets})
	}
	text := func(path, src, what string) *formula.Program {
		prog, err := bind(src)
		if err != nil {
			c.formulaErr(path, err)
			return nil
		}
		if k := prog.Result.Kind; k != formula.KindText && k != formula.KindNull {
			c.hint(CodeTypeMismatch, path, what+" is a text but the formula is "+prog.Result.String(), "TEXT(x) renders a value as text")
			return nil
		}
		return prog
	}
	rd := def.Request
	req := &Request{Sign: rd.Sign}
	method, takesBody, ok := RequestMethod(rd.Method)
	switch {
	case rd.Method == "":
		method, takesBody = "POST", true
	case !ok:
		c.hint(CodeBadValue, "/request/method", "unknown method "+quote(rd.Method), "one of GET, POST, PUT, PATCH, DELETE; POST when absent")
	}
	req.Method = method
	if rd.URL == "" {
		c.hint(CodeMissing, "/request/url", "a request has a url", `a formula that gives the address, such as "\"https://api.example.com/v1/messages\""`)
	} else if req.URL = text("/request/url", rd.URL, "the url"); req.URL != nil && len(req.URL.Slots) == 0 {
		// An address that reads nothing is known now: say now what is wrong
		// with it.
		if val, err := formula.NewVM().Run(req.URL, nil, &formula.Context{}); err == nil {
			if reason := URLReason(val.Str()); reason != "" {
				c.hint(CodeBadValue, "/request/url", reason, `an address is "https://host/path"`)
			} else {
				req.ConstURL = val.Str()
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range rd.headerOrder {
		path := "/request/headers/" + name
		switch reason := HeaderNameReason(name); {
		case reason != "":
			code := CodeBadName
			if engineHeaders[strings.ToLower(name)] {
				code = CodeReservedName
			}
			c.add(code, path, reason+": "+quote(name))
		case seen[strings.ToLower(name)]:
			c.addf(CodeDuplicate, path, "the header %s is given twice; header names differ in case only", quote(name))
		default:
			seen[strings.ToLower(name)] = true
			if prog := text(path, rd.Headers[name], "a header"); prog != nil {
				req.Headers = append(req.Headers, Header{Name: name, Value: prog})
			}
		}
	}
	if len(bytes.TrimSpace(rd.Body)) > 0 {
		if !takesBody && ok {
			c.hint(CodeNotAllowed, "/request/body", "a "+method+" request carries no body", "put what it says into the url, with URLENCODE, or use POST")
		} else {
			req.Body = compileTemplate(c, "/request/body", rd.Body, bind, 1)
		}
	}
	if rd.Sign != "" {
		found := false
		for _, name := range secrets {
			if strings.EqualFold(name, rd.Sign) {
				req.Sign, found = name, true
			}
		}
		if !found {
			c.hint(CodeUnknownSecret, "/request/sign", "the connection declares no secret "+quote(rd.Sign), `sign names a secret of the definition's connection: "connection": {"name": "books", "secrets": ["signing_key"]}`)
		}
	}
	a.Request = req

	// The reply: a status and a body, read by pure formulas.
	reply, _ := formula.NewScope(formula.Variable{Name: ReplyStatus, Type: formula.Scalar(formula.KindNumber)},
		formula.Variable{Name: ReplyBody, Type: formula.Scalar(formula.KindJSON)})
	over := func(src string) (*formula.Program, error) {
		return formula.Bind(src, reply, formula.BindOptions{Mode: formula.ModePure})
	}
	if def.OK != "" {
		prog, err := over(def.OK)
		switch {
		case err != nil:
			c.formulaErr("/ok", err)
		case prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull:
			c.hint(CodeNotACondition, "/ok", "ok must be a condition over STATUS and BODY, got "+prog.Result.String(), `such as STATUS = 200, or JSONVALUE(BODY, "ok") = "TRUE"`)
		default:
			a.OK = prog
		}
	}
	if len(def.Result) > 0 {
		names, err := orderedKeys(def.Result)
		var formulas map[string]string
		if err == nil {
			err = json.Unmarshal(def.Result, &formulas)
		}
		if err != nil {
			c.hint(CodeBadValue, "/result", "result is an object of formulas by returned value", `"result": {"ts": "JSONVALUE(BODY, \"ts\")"}`)
			return
		}
		given := map[string]bool{}
		for _, name := range names {
			path := "/result/" + name
			r, ok := a.Return(name)
			switch {
			case !ok:
				c.hint(CodeUnknownParameter, path, "the action returns no "+quote(name), "declare it under returns, with its type")
				continue
			case given[upper(name)]:
				c.addf(CodeDuplicate, path, "%s is given twice; names differ in case only", quote(name))
				continue
			}
			given[upper(name)] = true
			prog, err := over(formulas[name])
			if err != nil {
				c.formulaErr(path, err)
				continue
			}
			prog.ClaimExact(r.Type)
			if msg, hint := typeFits(r.Type, prog.Result); msg != "" {
				c.hint(CodeTypeMismatch, path, strings.Replace(msg, "the variable is", "the returned value "+r.Name+" is", 1), hint)
				continue
			}
			a.Result = append(a.Result, ResultValue{Name: r.Name, Formula: prog})
		}
	}
}

// URLReason says why a text is not an address a request may go to, or ""
// when it is: http or https, a host, and no user or password, which
// belong in a header.
func URLReason(raw string) string {
	u, err := url.Parse(raw)
	switch {
	case raw == "":
		return "the url is blank"
	case err != nil:
		return "the url does not read as an address"
	case u.Scheme != "https" && u.Scheme != "http":
		return "the url is http or https"
	case u.Hostname() == "":
		return "the url names no host"
	case u.User != nil:
		return "the url carries a user or a password; a credential belongs in a header, as a secret"
	}
	return ""
}

// HTTPCall is the declared action the engine defines for a call nobody
// named (ADR-0045, item 3): the address, the method, the headers and the
// body are arguments of the call. It has no connection and so reads no
// secret, and it goes only to the hosts the host program allows for
// requests without one.
func HTTPCall() *ActionSpec { return httpCall() }

var httpCall = sync.OnceValue(func() *ActionSpec {
	text, js := formula.Scalar(formula.KindText), formula.Scalar(formula.KindJSON)
	return &ActionSpec{Name: ActionHTTPCall, Version: 1, Label: "Call a URL",
		Description: "Sends one HTTP request to an address the rule gives. A reply of 2xx is success; no reply, 408, 429 and 5xx are asked again; any other reply is a refusal.",
		Params: []Param{
			{Name: "url", Type: text, Required: true, Description: "The address, http or https."},
			{Name: "method", Type: text, Description: "GET, POST, PUT, PATCH or DELETE; POST when blank."},
			{Name: "headers", Type: js, Description: "An object of header names to their values."},
			{Name: "body", Type: js, Description: "The JSON to send. When blank, a POST, a PUT and a PATCH send the call itself: the arguments and the event that fired the rule."},
		},
		Returns: []Param{
			{Name: "status", Type: formula.Scalar(formula.KindNumber), Description: "The status of the reply."},
			{Name: "body", Type: js, Description: "The reply, when it is JSON."},
		},
		Request: &Request{Dynamic: true},
	}
})

// DefineAction parses and compiles an action definition in one step.
func DefineAction(data []byte) (*ActionSpec, Diagnostics) {
	def, diags := ParseAction(data)
	if def == nil {
		return nil, diags
	}
	a, more := CompileAction(def)
	return a, append(diags, more...)
}

// compileParams reads the parameters of an action, or with params false
// what it returns: each a name and a scalar type.
func compileParams(c *collector, path string, defs map[string]json.RawMessage, order []string, params bool) []Param {
	what := "a parameter"
	if !params {
		what = "a returned value"
	}
	var out []Param
	seen := map[string]bool{}
	for _, name := range order {
		ppath := path + "/" + name
		if !c.checkName(ppath, name, nil) {
			continue
		}
		if seen[upper(name)] {
			c.addf(CodeDuplicate, ppath, "%s is declared twice; names differ in case only", quote(name))
			continue
		}
		if !params && (upper(name) == ResultOK || upper(name) == ResultError) {
			c.hint(CodeReservedName, ppath, quote(name)+" is what an into reads beside the returned values: whether the action ran, and the code it failed with", "give the returned value another name")
			continue
		}
		seen[upper(name)] = true
		raw := bytes.TrimSpace(defs[name])
		var pd ParamDef
		switch {
		case len(raw) > 0 && raw[0] == '"':
			_ = json.Unmarshal(raw, &pd.Type)
		default:
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&pd); err != nil {
				c.hint(CodeBadJSON, ppath, jsonMessage(err), what+` is its type as text, or an object: {"type": "text", "required": true, "description": "..."}`)
				continue
			}
		}
		if pd.Type == "" {
			c.hint(CodeMissing, ppath+"/type", what+" has a type", "one of bool, number, int, decimal(s), money(s), text, date, datetime, ref, json")
			continue
		}
		t, err := formula.ParseType(pd.Type)
		switch {
		case err != nil:
			c.add(CodeUnknownType, ppath, err.Error())
			continue
		case t.Kind == formula.KindNull:
			c.add(CodeUnknownType, ppath, what+" is never blank by type")
			continue
		case !t.IsScalar():
			c.hint(CodeNotAllowed, ppath, what+" is a scalar; "+pd.Type+" is a composite", "a json parameter carries a structure")
			continue
		}
		if pd.Required && !params {
			c.hint(CodeNotAllowed, ppath+"/required", "required belongs to a parameter; what an action returns it may leave blank", "drop it")
			continue
		}
		out = append(out, Param{Name: name, Type: t, Required: pd.Required, Description: pd.Description})
	}
	return out
}

// actionNameReason says why a text is not the name of a defined action,
// with the code of the finding, or "" when it is: identifiers joined by
// dots, a namespace before a verb. A name without a dot, and the
// namespaces table and http, are the engine's (ADR-0045, item 1).
func actionNameReason(name string) (code, reason string) {
	if name == "" {
		return CodeMissing, "an action has a name"
	}
	if len(name) > MaxActionName {
		return CodeBadName, fmt.Sprintf("an action name has at most %d characters", MaxActionName)
	}
	if r := strings.Replace(eventNameReason(name), "an event name", "an action name", 1); r != "" {
		return CodeBadName, r
	}
	namespace, _, dotted := strings.Cut(name, ".")
	switch {
	case !dotted:
		return CodeReservedName, "a name without a dot is the engine's, as set and create are"
	case strings.EqualFold(namespace, "table") || strings.EqualFold(namespace, "http"):
		return CodeReservedName, "the namespace " + strings.ToLower(namespace) + " is the engine's"
	}
	return "", ""
}
