package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// An inlet is a way in for messages from outside (ADR-0065): the mirror
// of a declared action. That one says what request the engine sends;
// this one says what the engine does with a request it is sent: how the
// message proves who sent it, which messages it acts on, what makes two
// messages the same message, and the one command a message becomes. It
// is data, as a class is: its formulas read the message and nothing
// else.

// InletDef is an inlet as written.
type InletDef struct {
	Name        string `json:"name"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// As is the id of the user the command is carried out as: a service.
	As string `json:"as"`
	// Connection names the connection whose secret the proof reads.
	Connection *ConnectionDef `json:"connection,omitempty"`
	// Proof says how a message proves who sent it.
	Proof *ProofDef `json:"proof,omitempty"`
	// When is the condition over the message that says the inlet acts on
	// it; without one it acts on every message. Key gives what makes two
	// messages the same; without one the bytes of the body do.
	When string `json:"when,omitempty"`
	Key  string `json:"key,omitempty"`
	// Command is create, set or transition, and Object the object of
	// that command: the class and the transition as they are, every other
	// text a formula over the message.
	Command string          `json:"command"`
	Object  json.RawMessage `json:"object,omitempty"`
}

// ProofDef is the proof of an inlet as written: the form, the header of
// the message that carries it, the secret of the connection it is made
// with, and for the forms that take them what stands before it in the
// header and how a signature is written.
type ProofDef struct {
	Form     string `json:"form"`
	Header   string `json:"header"`
	Secret   string `json:"secret"`
	Prefix   string `json:"prefix,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

// The forms of a proof (ADR-0065, item 3).
const (
	// ProofShown: the header holds the secret itself.
	ProofShown = "shown"
	// ProofBody: the header holds the HMAC-SHA256 of the body, keyed
	// with the secret.
	ProofBody = "body"
	// ProofTimestampBody: the header is t=<timestamp>,v1=<signature>, the
	// signature the HMAC-SHA256 of the timestamp, a dot and the body. It
	// is what the engine's own requests carry (ADR-0045, item 4).
	ProofTimestampBody = "timestamp.body"
)

// How a signature is written in the header.
const (
	EncodingHex    = "hex"
	EncodingBase64 = "base64"
)

// The commands a message may become (ADR-0065, item 5).
const (
	InletCreate     = "create"
	InletSet        = "set"
	InletTransition = "transition"
)

// What the formulas of an inlet read (ADR-0065, item 4): the body as a
// json value, the headers and the parameters of the address as objects
// of texts, and the body as text.
const (
	MessageBody    = "BODY"
	MessageHeaders = "HEADERS"
	MessageParams  = "PARAMS"
	MessageRaw     = "RAW"
)

// messageVariables are what a message gives the formulas of an inlet.
var messageVariables = []formula.Variable{
	{Name: MessageBody, Type: formula.Scalar(formula.KindJSON)},
	{Name: MessageHeaders, Type: formula.Scalar(formula.KindJSON)},
	{Name: MessageParams, Type: formula.Scalar(formula.KindJSON)},
	{Name: MessageRaw, Type: formula.Scalar(formula.KindText)},
}

// MaxInletName is the longest name an inlet may have.
const MaxInletName = 100

// Proof is the compiled proof of an inlet. Header is in small letters,
// as the headers of a message are read.
type Proof struct {
	Form     string
	Header   string
	Secret   string
	Prefix   string
	Encoding string
}

// InletSpec is a compiled inlet.
type InletSpec struct {
	Name        string
	Version     int
	Label       string
	Description string
	As          string
	Connection  string
	Secrets     []string
	Proof       Proof
	// When and Key are nil when the definition gives none.
	When *formula.Program
	Key  *formula.Program
	// Command is the command a message becomes, Class the class of its
	// document as the class declares its name, and Transition the
	// transition, for a transition.
	Command    string
	Class      string
	Transition string
	// ID gives the id of the document; nil for a create that leaves the
	// id to the engine. Sets are the values, in the order written.
	ID   *formula.Program
	Sets []Assignment
	Def  *InletDef
}

// ParseInlet decodes an inlet. Unknown properties are findings.
func ParseInlet(data []byte) (*InletDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def InletDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	return &def, nil
}

// DefineInlet parses and compiles an inlet against an environment, which
// has the classes its command may name.
func DefineInlet(data []byte, env Env) (*InletSpec, Diagnostics) {
	def, diags := ParseInlet(data)
	if def == nil {
		return nil, diags
	}
	return CompileInlet(def, env)
}

// inletNameReason says why a text is not the name of an inlet, with the
// code of the finding, or "" when it is: identifiers joined by dots, the
// sender before what it says, as an action has a namespace and a verb.
// The name is the end of the inlet's address, so it holds nothing an
// address would have to escape.
func inletNameReason(name string) (code, reason string) {
	if name == "" {
		return CodeMissing, "an inlet has a name"
	}
	if len(name) > MaxInletName {
		return CodeBadName, fmt.Sprintf("an inlet name has at most %d characters", MaxInletName)
	}
	if r := strings.Replace(eventNameReason(name), "an event name", "an inlet name", 1); r != "" {
		return CodeBadName, r
	}
	if !strings.Contains(name, ".") {
		return CodeBadName, "an inlet name says who sends and what it says, joined by a dot"
	}
	return "", ""
}

// proofHeaderReason says why a text is not the name of a header a proof
// may be read from, or "" when it is.
func proofHeaderReason(name string) string {
	if name == "" {
		return "the proof names the header of the message that carries it"
	}
	for _, r := range name {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune(`()<>@,;:\"/[]?={}`, r) {
			return "a header name is letters, digits and the punctuation of an HTTP token"
		}
	}
	return ""
}

// CompileInlet checks an inlet and compiles it.
func CompileInlet(def *InletDef, env Env) (*InletSpec, Diagnostics) {
	c := &collector{env: env, pure: true}
	in := &InletSpec{Name: def.Name, Version: def.Version, Label: def.Label, Description: def.Description, Def: def}
	if code, reason := inletNameReason(def.Name); reason != "" {
		c.hint(code, "/name", reason+": "+quote(def.Name), `the sender and what it says, joined by a dot, such as "bank.paid"`)
	}
	if in.Version == 0 {
		in.Version = 1
	} else if in.Version < 0 {
		c.add(CodeBadValue, "/version", "the version is a positive number")
	}
	if in.As = strings.TrimSpace(def.As); in.As == "" {
		c.hint(CodeMissing, "/as", "an inlet names the user its command is carried out as",
			`"as": "bank-in", a user of the kind service, with the rights the inlet needs and no more`)
	}

	// The connection and the proof: a message proves who sent it with a
	// secret, and a secret is a connection's (ADR-0065, item 3).
	secrets := []string{}
	if conn := def.Connection; conn != nil {
		if c.checkName("/connection/name", conn.Name, nil) {
			in.Connection = conn.Name
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
		in.Secrets = secrets
	}
	compileProof(c, in, def, secrets)

	// The formulas of an inlet read the message and nothing else.
	scope, _ := formula.NewScope(messageVariables...)
	if def.When != "" {
		prog, err := c.bind(def.When, scope, formula.ModePure)
		switch {
		case err != nil:
			c.formulaErr("/when", err)
		case prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull:
			c.hint(CodeNotACondition, "/when", "when must be a condition over the message, got "+prog.Result.String(), `such as JSONVALUE(BODY, "type") = "payment.received"`)
		default:
			in.When = prog
		}
	}
	if def.Key != "" {
		prog, err := c.bind(def.Key, scope, formula.ModePure)
		switch {
		case err != nil:
			c.formulaErr("/key", err)
		case prog.Result.Kind != formula.KindText && prog.Result.Kind != formula.KindNull:
			c.hint(CodeTypeMismatch, "/key", "the key is a text but the formula is "+prog.Result.String(), `the sender's own number of the message, such as JSONVALUE(BODY, "id"); TEXT(x) renders a value as text`)
		default:
			in.Key = prog
		}
	}
	compileInletCommand(c, in, def, scope)
	// A formula that may not be used here is refused by the binder in the
	// words of a title; say what it is refused in.
	for i := range c.list {
		c.list[i].Message = strings.Replace(c.list[i].Message, formula.ModePure.Where(), "a formula of an inlet, which reads the message and nothing else", 1)
	}
	if c.failed() {
		return nil, c.list
	}
	return in, nil
}

// compileProof reads the proof of an inlet. An inlet without one is
// refused: there is no address the whole world may write to.
func compileProof(c *collector, in *InletSpec, def *InletDef, secrets []string) {
	const whole = `"connection": {"name": "bank", "secrets": ["signing_key"]}, "proof": {"form": "timestamp.body", "header": "Bank-Signature", "secret": "signing_key"}`
	if def.Proof == nil {
		c.hint(CodeMissing, "/proof", "an inlet says how a message proves who sent it; without a proof anybody could send one", whole)
		return
	}
	p := def.Proof
	proof := Proof{Form: p.Form, Prefix: p.Prefix, Encoding: p.Encoding}
	switch p.Form {
	case ProofShown, ProofBody, ProofTimestampBody:
	case "":
		c.hint(CodeMissing, "/proof/form", "a proof has a form", "one of "+ProofShown+", "+ProofBody+", "+ProofTimestampBody)
	default:
		c.hint(CodeBadValue, "/proof/form", "unknown form "+quote(p.Form), "one of "+ProofShown+" (the header holds the secret), "+ProofBody+" (the header holds a signature of the body), "+ProofTimestampBody+" (the header is t=<timestamp>,v1=<signature>)")
	}
	if reason := proofHeaderReason(p.Header); reason != "" {
		code := CodeBadName
		if p.Header == "" {
			code = CodeMissing
		}
		c.hint(code, "/proof/header", reason, `such as "Bank-Signature"`)
	} else {
		proof.Header = strings.ToLower(p.Header)
	}
	switch {
	case p.Secret == "":
		c.hint(CodeMissing, "/proof/secret", "a proof names the secret it is made with", whole)
	case def.Connection == nil:
		c.hint(CodeMissing, "/connection", "the secret of a proof is a secret of a connection, and the inlet names none", whole)
	default:
		found := false
		for _, name := range secrets {
			if strings.EqualFold(name, p.Secret) {
				proof.Secret, found = name, true
			}
		}
		if !found {
			c.hint(CodeUnknownSecret, "/proof/secret", "the connection declares no secret "+quote(p.Secret), whole)
		}
	}
	if p.Encoding != "" {
		switch {
		case p.Form != ProofBody && p.Form != "":
			c.hint(CodeNotAllowed, "/proof/encoding", "only the form "+ProofBody+" says how its signature is written", "leave encoding out")
		case p.Encoding != EncodingHex && p.Encoding != EncodingBase64:
			c.hint(CodeBadValue, "/proof/encoding", "unknown encoding "+quote(p.Encoding), EncodingHex+", which it is when left out, or "+EncodingBase64)
		}
	}
	if proof.Encoding == "" && p.Form == ProofBody {
		proof.Encoding = EncodingHex
	}
	if p.Prefix != "" && p.Form == ProofTimestampBody {
		c.hint(CodeNotAllowed, "/proof/prefix", "the form "+ProofTimestampBody+" has its own shape, t=<timestamp>,v1=<signature>, and nothing stands before it", "leave prefix out")
	}
	in.Proof = proof
}

// compileInletCommand reads the command of an inlet and its object: the
// class and the transition are names, checked now against the class as
// it is, and the id and the values are formulas over the message, typed
// against what they fill.
func compileInletCommand(c *collector, in *InletSpec, def *InletDef, scope *formula.Scope) {
	const commands = InletCreate + ", " + InletSet + " or " + InletTransition
	switch def.Command {
	case InletCreate, InletSet, InletTransition:
		in.Command = def.Command
	case "":
		c.hint(CodeMissing, "/command", "an inlet names the command a message becomes", commands)
		return
	default:
		c.hint(CodeBadValue, "/command", "a message does not become "+quote(def.Command), commands+": one command on one document")
		return
	}
	if len(bytes.TrimSpace(def.Object)) == 0 {
		c.hint(CodeMissing, "/object", "an inlet gives the object of its command", `"object": {"class": "Invoice", "id": "JSONVALUE(BODY, \"reference\")", "transition": "pay"}`)
		return
	}
	var obj struct {
		Class      string          `json:"class"`
		ID         string          `json:"id"`
		Transition *string         `json:"transition"`
		Values     json.RawMessage `json:"values"`
	}
	dec := json.NewDecoder(bytes.NewReader(def.Object))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		c.hint(CodeBadJSON, "/object", jsonMessage(err), "the object of "+in.Command+" has class, id and values"+map[bool]string{true: ", and transition"}[in.Command == InletTransition]+"; who sends the command, and its key, are the inlet's own")
		return
	}
	if obj.Class == "" {
		c.hint(CodeMissing, "/object/class", "the object names the class of its document, as it is and not as a formula", `"class": "Invoice"`)
		return
	}
	var target *Class
	ok := false
	if c.env.Classes != nil {
		target, ok = c.env.Classes(obj.Class)
	}
	switch {
	case !ok && !c.env.Lenient:
		c.hint(CodeUnknownClass, "/object/class", "unknown class "+quote(obj.Class), "the name of a class is written as it is, and is no formula; define the class before the inlet that names it")
		return
	case ok && target.States == nil && !c.env.Lenient:
		c.hint(CodeNotAllowed, "/object/class", target.Name+" declares no states: it is a stateless application and stores no documents (ADR-0040)", "name a class that has a states block")
	}
	in.Class = targetName(obj.Class, target)

	switch {
	case in.Command != InletTransition:
		if obj.Transition != nil {
			c.hint(CodeNotAllowed, "/object/transition", "only the command transition names one", `"command": "transition"`)
		}
	case obj.Transition == nil || *obj.Transition == "":
		c.hint(CodeMissing, "/object/transition", "the object names the transition the document takes, as it is and not as a formula", `"transition": "pay"`)
	default:
		in.Transition = *obj.Transition
		if ok && !c.env.Lenient && !hasTransition(target, in.Transition) {
			c.addf(CodeUnknownTransition, "/object/transition", "the class %s has no transition %s", target.Name, quote(in.Transition))
		}
	}

	switch {
	case obj.ID != "":
		in.ID = bindFill(c, "/object/id", obj.ID, scope, &textType)
	case in.Command != InletCreate:
		c.hint(CodeMissing, "/object/id", "the object names the document, by a formula over the message", `"id": "JSONVALUE(BODY, \"payment.reference\")"`)
	}

	sets, given, read := fillValuesFrom(c, "/object/values", obj.Values, target, scope, in.Command, messageVariables)
	if !read {
		return
	}
	in.Sets = sets
	switch {
	case in.Command == InletSet && len(sets) == 0 && !c.failed():
		c.hint(CodeMissing, "/object/values", "a set gives the values it assigns", `"values": {"paid_on": "DATEVALUE(JSONVALUE(BODY, \"booked\"))"}`)
	case in.Command == InletCreate && ok && !c.env.Lenient:
		// A required input the create does not give and the class does
		// not default would refuse every message: say so now.
		var missing []string
		for _, v := range target.Variables {
			if !v.Computed && v.Required && v.Default == nil && !given[upper(v.Name)] && v.Type.IsScalar() {
				missing = append(missing, v.Name)
			}
		}
		if len(missing) > 0 {
			c.hint(CodeMissing, "/object/values", "the create gives no value for "+strings.Join(missing, ", ")+", which "+target.Name+" requires and has no default for", "give each a formula over the message under values")
		}
	}
}
