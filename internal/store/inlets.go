package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// A way in for messages from outside (ADR-0065). An inlet is a
// definition kept in the database, the mirror of a declared action:
// that one says what request the engine sends, this one what the engine
// does with a request it is sent. A message proves who sent it with a
// secret of the inlet's connection, and becomes one command, create,
// set or transition, carried out as the service user the inlet names,
// with a key that makes a message sent twice one commit. An inlet is
// data: its formulas read the message and nothing else.

// InletAddress is where a server takes the messages of an inlet: the
// name of the inlet follows it.
const InletAddress = "/v1/in/"

// inletKeyPrefix begins the idempotency key of every command an inlet
// sends, in:<inlet>:<key of the message>. A caller may not use it, so
// that nobody takes the key of a message before the message comes.
const inletKeyPrefix = "in:"

// MaxMessageKey is the longest key a message may have.
const MaxMessageKey = 200

// proofTolerance is how far the timestamp of a signed message may be
// from the clock of the database (ADR-0065, item 3).
const proofTolerance = 5 * time.Minute

// ErrNotProven is what Receive answers for every message whose proof
// does not hold, whatever is wrong with it, so that a guess learns
// nothing. A door answers it as it answers a request without a key.
var ErrNotProven = errors.New("the message does not prove who sent it")

// Message is a message from outside as an inlet reads it.
type Message struct {
	// Headers are the headers of the message by name, in any case, and
	// Params the parameters of its address.
	Headers map[string]string `json:"headers,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	// Body is the body as it came. Form says that it holds the fields of
	// a form and no JSON: the formulas then read BODY as an object of
	// the fields.
	Body []byte `json:"-"`
	Form bool   `json:"form,omitempty"`
}

// Receipt is what became of a message.
type Receipt struct {
	Inlet string `json:"inlet"`
	// As is the user the command was carried out as.
	As string `json:"as"`
	// Ignored says that the inlet does not act on the message: its when
	// was not TRUE. Nothing was done.
	Ignored bool `json:"ignored,omitempty"`
	// Key is the idempotency key the command carried, and Document the
	// document after it, as the user of the inlet may read it.
	Key      string    `json:"key,omitempty"`
	Document *Document `json:"document,omitempty"`
}

// InletProof is the proof of an inlet as a list shows it.
type InletProof struct {
	Form     string `json:"form"`
	Header   string `json:"header"`
	Secret   string `json:"secret"`
	Prefix   string `json:"prefix,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

// InletInfo is one inlet of the database, as Inlets lists it and
// DefineInlet returns it.
type InletInfo struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Address is where a server takes the inlet's messages.
	Address string `json:"address"`
	// As is the user the inlet acts as.
	As         string     `json:"as"`
	Connection string     `json:"connection"`
	Proof      InletProof `json:"proof"`
	// Command is what a message becomes, on a document of Class, along
	// Transition when it is a transition.
	Command    string `json:"command"`
	Class      string `json:"class"`
	Transition string `json:"transition,omitempty"`
	// Available says whether this process acts on the inlet's messages
	// now, and Why, when it does not, what it lacks.
	Available bool   `json:"available"`
	Why       string `json:"why,omitempty"`
	// Defined says who defined this version, and when.
	Defined *Defined `json:"defined,omitempty"`
}

// InletTrial is what an inlet would do with a message (ADR-0065, item
// 9). Nothing was done.
type InletTrial struct {
	Inlet string `json:"inlet"`
	// As is the user the command would be carried out as.
	As string `json:"as"`
	// Acts says whether the inlet acts on the message: its when is TRUE,
	// or it has none.
	Acts bool `json:"acts"`
	// Command is the command the message would become and Object its
	// object, with the key the inlet gives it.
	Command string         `json:"command,omitempty"`
	Object  map[string]any `json:"object,omitempty"`
}

// inlet is an inlet of the database. removed marks one whose address
// was closed: its versions stay.
type inlet struct {
	spec    *model.InletSpec
	json    []byte
	defined Defined
	removed bool
}

// inletNamed finds an inlet that was not removed.
func (s *Store) inletNamed(name string) *inlet {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	if in := s.inlets[strings.ToUpper(name)]; in != nil && !in.removed {
		return in
	}
	return nil
}

// unknownInlet is the refusal of a name that is no inlet.
func unknownInlet(name string) *Error {
	return hintf(CodeUnknownInlet, "", "the database has no inlet "+strconv.Quote(name), "list_inlets lists them; an administrator defines one with define_inlet")
}

// actor finds the user an inlet acts as, and says why it cannot act as
// that user when it cannot: an inlet acts as a service that the
// database knows and has not disabled.
func (s *Store) actor(id string) (Principal, string) {
	if !s.people.on {
		return Principal{}, "this database keeps no users of its own, and an inlet acts as one"
	}
	s.people.mu.RLock()
	u := s.people.users[id]
	s.people.mu.RUnlock()
	switch {
	case u == nil:
		return Principal{}, "the database has no user " + strconv.Quote(id)
	case u.Kind != ActorService:
		return Principal{}, id + " is " + article(u.Kind) + ", and an inlet acts as a service"
	case u.Disabled:
		return Principal{}, "the user " + id + " is disabled"
	}
	return Principal{ID: u.ID, Kind: u.Kind, Groups: u.Groups, System: u.System}, ""
}

// article puts a or an before a word.
func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an " + word
	}
	return "a " + word
}

// inletInfo is the inlet as a list shows it.
func (s *Store) inletInfo(in *inlet) *InletInfo {
	spec := in.spec
	info := &InletInfo{Name: spec.Name, Version: spec.Version, Label: spec.Label, Description: spec.Description,
		Address: InletAddress + spec.Name, As: spec.As, Connection: spec.Connection,
		Proof:   InletProof{Form: spec.Proof.Form, Header: spec.Def.Proof.Header, Secret: spec.Proof.Secret, Prefix: spec.Proof.Prefix, Encoding: spec.Proof.Encoding},
		Command: spec.Command, Class: spec.Class, Transition: spec.Transition}
	switch _, why := s.actor(spec.As); {
	case !s.inbound:
		info.Why = "this process takes no messages from outside"
	case s.secretFn == nil:
		info.Why = "this process supplies no secrets, and the proof is made with one"
	case why != "":
		info.Why = why
	default:
		info.Available = true
	}
	if in.defined != (Defined{}) {
		d := in.defined
		info.Defined = &d
	}
	return info
}

// Inlets lists the inlets of the database in the order they were
// defined, each with its address, the user it acts as, the command its
// messages become and whether this process acts on them. It is open to
// every caller, as reading a definition is; an inlet holds no secret,
// only the name of one.
func (s *Store) Inlets() []InletInfo {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	out := make([]InletInfo, 0, len(s.inletOrder))
	for _, name := range s.inletOrder {
		if in := s.inlets[strings.ToUpper(name)]; !in.removed {
			out = append(out, *s.inletInfo(in))
		}
	}
	return out
}

// Inlet returns one inlet by name.
func (s *Store) Inlet(name string) (*InletInfo, bool) {
	if in := s.inletNamed(name); in != nil {
		return s.inletInfo(in), true
	}
	return nil, false
}

// ValidateInlet compiles an inlet and reports what DefineInlet would
// refuse, without storing anything.
func (s *Store) ValidateInlet(data []byte) model.Diagnostics {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	_, diags, _ := s.compileInlet(data)
	return diags
}

// compileInlet compiles an inlet against the classes of the database
// and checks it against the stored one of the same name, the user it
// names and what this process was opened with. It returns the version
// the definition would get. The caller holds cmu.
func (s *Store) compileInlet(data []byte) (*model.InletSpec, model.Diagnostics, int) {
	spec, diags := model.DefineInlet(data, s.env())
	if spec == nil {
		return nil, diags, 0
	}
	add := func(code, path, message, hint string) {
		diags = append(diags, model.Diagnostic{Code: code, Path: path, Pos: -1, Message: message, Hint: hint})
	}
	version := spec.Version
	if old := s.inlets[strings.ToUpper(spec.Name)]; old != nil {
		if old.spec.Name != spec.Name {
			add(model.CodeDuplicate, "/name", "an inlet "+old.spec.Name+" exists; names differ in case only", "")
		}
		version = old.spec.Version + 1
		if spec.Def.Version != 0 && spec.Def.Version != version {
			add(model.CodeBadValue, "/version", "the stored inlet is at version "+itoa(old.spec.Version)+"; a redefinition is version "+itoa(version),
				"omit version, or write "+itoa(version))
		}
	}
	// Whether this process takes messages from outside is its host
	// program's decision, as whether it sends requests is (ADR-0065,
	// item 8): told here, an agent learns it at once.
	if !s.inbound {
		add(model.CodeNotSupported, "", "this process takes no messages from outside, so it cannot take an inlet",
			"the host program says that it does when it opens the database, with Options.Inbound; fadodb serve does when its settings have inbound")
	}
	if s.secretFn == nil {
		add(model.CodeNotSupported, "/connection/secrets", "this process supplies no secrets, and the proof of an inlet is made with one",
			"the host program gives Open a function for them, Options.Secrets")
	}
	// An inlet says whom the database takes a stranger for: a service
	// that it knows (item 6).
	switch _, why := s.actor(spec.As); {
	case why == "":
	case !s.people.on:
		add(model.CodeNotSupported, "/as", why, "a database keeps users when it is opened with Options.Users, as fadodb serve opens it")
	case strings.HasPrefix(why, "the database has no user"):
		add(CodeUnknownUser, "/as", why, `an administrator makes the user first: create_user {"id": "`+spec.As+`", "kind": "service", "groups": […]}, with the groups the inlet needs and no more`)
	default:
		add(model.CodeNotAllowed, "/as", why, "name a user of the kind service, made for this inlet, with the groups it needs and no more")
	}
	return spec, diags, version
}

// DefineInlet compiles an inlet and stores it with its version
// (ADR-0065): a server that takes messages takes them at the inlet's
// address from then on. Defining the same definition again is a no-op,
// and a redefinition is the next version. It is for a database
// administrator, since an inlet says whom the database takes a stranger
// for (item 6), in a database that keeps its users, opened by a program
// that takes messages (Options.Inbound) and supplies secrets.
func (s *Store) DefineInlet(ctx context.Context, data []byte, opts ...Option) (*InletInfo, error) {
	o, e := s.keeper(ctx, opts, "define an inlet")
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	compact, err := compactJSON(data)
	if err != nil {
		return nil, model.Diagnostics{{Code: model.CodeBadJSON, Pos: -1, Message: err.Error()}}
	}
	if old := s.inlets[upperName(data)]; old != nil && !old.removed && bytes.Equal(old.json, compact) {
		return s.inletInfo(old), nil
	}
	spec, diags, version := s.compileInlet(data)
	if len(diags) > 0 {
		return nil, diags
	}
	spec.Version = version
	now := s.now().Unix()
	w := s.writer
	if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	rollback := func(err error) (*InletInfo, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return nil, err
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO inlets (name, version, json, removed, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)"+
		" ON CONFLICT (name) DO UPDATE SET name = excluded.name, version = excluded.version, json = excluded.json, removed = 0, updated_at = excluded.updated_at",
		spec.Name, version, string(compact), now, now); err != nil {
		return rollback(storageErr("store the inlet", err))
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO inlet_versions (inlet, version, json, created_at) VALUES (?, ?, ?, ?)", spec.Name, version, string(compact), now); err != nil {
		return rollback(storageErr("store the inlet version", err))
	}
	defined, derr := recordDefinition(ctx, w, DefinitionInlet, spec.Name, version, o.principal, now)
	if derr != nil {
		return rollback(derr)
	}
	if err := s.endTransaction(ctx); err != nil {
		return nil, storageErr("commit", err)
	}
	in := &inlet{spec: spec, json: compact, defined: defined}
	s.registerInlet(in)
	return s.inletInfo(in), nil
}

// registerInlet makes a definition the current one of its name. The
// caller holds cmu.
func (s *Store) registerInlet(in *inlet) {
	key := strings.ToUpper(in.spec.Name)
	if _, exists := s.inlets[key]; !exists {
		s.inletOrder = append(s.inletOrder, in.spec.Name)
	}
	s.inlets[key] = in
}

// RemoveInlet closes an inlet: its messages are answered as those of a
// name that is no inlet, and it leaves the list. Its versions stay, and
// defining it again opens it as its next version. It is for a database
// administrator.
func (s *Store) RemoveInlet(ctx context.Context, name string, opts ...Option) error {
	if _, e := s.keeper(ctx, opts, "remove an inlet"); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	old := s.inlets[strings.ToUpper(name)]
	if old == nil || old.removed {
		return unknownInlet(name)
	}
	if _, err := s.writer.ExecContext(ctx, "UPDATE inlets SET removed = 1, updated_at = ? WHERE name = ?", s.now().Unix(), old.spec.Name); err != nil {
		return storageErr("remove the inlet", err)
	}
	gone := *old
	gone.removed = true
	s.inlets[strings.ToUpper(name)] = &gone
	return nil
}

// loadInlets reads the stored inlets in the order they were defined.
// A stored inlet was checked when it was defined and loads leniently:
// what it says of a class that has since changed is found when a
// message comes. The caller is Open, after the classes are loaded.
func (s *Store) loadInlets(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT name, version, json, removed FROM inlets ORDER BY rowid")
	if err != nil {
		return storageErr("read the inlets", err)
	}
	defer rows.Close()
	env := s.env()
	env.Lenient = true
	for rows.Next() {
		var name string
		var version int
		var src []byte
		var removed bool
		if err := rows.Scan(&name, &version, &src, &removed); err != nil {
			return storageErr("read the inlets", err)
		}
		spec, diags := model.DefineInlet(src, env)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored inlet " + name + " does not compile: " + diags.Error()}
		}
		spec.Version = version
		s.registerInlet(&inlet{spec: spec, json: src, removed: removed})
	}
	return storageErr("read the inlets", rows.Err())
}

// storedInlet finds the text and the version of an inlet for
// Definition: the current one from memory, an earlier one from its
// table. A removed inlet is read like any other: its versions stay.
// The caller holds cmu.
func (s *Store) storedInlet(ctx context.Context, name string, version int) (*StoredDefinition, *Error) {
	in, ok := s.inlets[strings.ToUpper(name)]
	if !ok {
		return nil, unknownInlet(name)
	}
	if version == 0 || version == in.spec.Version {
		return &StoredDefinition{Name: in.spec.Name, Version: in.spec.Version, Definition: copyJSON(in.json)}, nil
	}
	var text string
	err := s.db.QueryRowContext(ctx, "SELECT json FROM inlet_versions WHERE inlet = ? AND version = ?", in.spec.Name, version).Scan(&text)
	if err == sql.ErrNoRows {
		return nil, hintf(CodeUnknownInlet, "", "the inlet "+in.spec.Name+" has no version "+strconv.Itoa(version),
			"its current version is "+strconv.Itoa(in.spec.Version)+"; definitions lists every version with who defined it")
	}
	if err != nil {
		return nil, asError(storageErr("read the version of the inlet", err))
	}
	return &StoredDefinition{Name: in.spec.Name, Version: version, Definition: json.RawMessage(text)}, nil
}

// read is a message as the formulas of an inlet read it: the four
// values of ADR-0065, item 4, and, while a repeat goes over a list of
// the message, one element of it under the name the repeat gives it
// (ADR-0085, item 1).
type read struct {
	body, headers, params, raw formula.Value
	as                         string
	item                       formula.Value
}

// reading makes the four values of a message. The header that carries
// the proof is not among the headers the formulas read: with the form
// shown it holds the secret itself, which no formula may copy into a
// document.
func reading(spec *model.InletSpec, m *Message) read {
	r := read{body: formula.Blank(), raw: formula.Blank()}
	if utf8.Valid(m.Body) {
		r.raw = formula.Text(string(m.Body))
	}
	switch trimmed := bytes.TrimSpace(m.Body); {
	case m.Form:
		fields := map[string]string{}
		if form, err := url.ParseQuery(string(m.Body)); err == nil {
			for name, values := range form {
				if len(values) > 0 {
					fields[name] = values[0]
				}
			}
		}
		r.body = formula.JSON(string(mustMarshal(fields)))
	case len(trimmed) > 0 && json.Valid(trimmed):
		r.body = formula.JSON(string(trimmed))
	}
	headers := make(map[string]string, len(m.Headers))
	for name, value := range m.Headers {
		if name = strings.ToLower(name); name != spec.Proof.Header {
			headers[name] = value
		}
	}
	r.headers = formula.JSON(string(mustMarshal(headers)))
	params := m.Params
	if params == nil {
		params = map[string]string{}
	}
	r.params = formula.JSON(string(mustMarshal(params)))
	return r
}

// run evaluates a formula of an inlet over a message.
func (r *read) run(e *evaluator, prog *formula.Program) (formula.Value, error) {
	if cap(e.vars) < len(prog.Slots) {
		e.vars = make([]formula.Value, len(prog.Slots))
	}
	vars := e.vars[:len(prog.Slots)]
	for i, slot := range prog.Slots {
		switch strings.ToUpper(slot.Name) {
		case model.MessageBody:
			vars[i] = r.body
		case model.MessageHeaders:
			vars[i] = r.headers
		case model.MessageParams:
			vars[i] = r.params
		case model.MessageRaw:
			vars[i] = r.raw
		default:
			vars[i] = formula.Blank()
			// One element of the list a repeat goes over, by its name
			// (ADR-0085, item 1).
			if r.as != "" && strings.EqualFold(slot.Name, r.as) {
				vars[i] = r.item
			}
		}
	}
	return e.vm.Run(prog, vars, &formula.Context{})
}

// lines makes the lines of a rows variable from a list in the message
// (ADR-0085, item 1): one line for each element of the list the path
// leads to, each column by its formula over the element. A message
// without the list, or with an empty one, gives no lines.
func (r *read) lines(e *evaluator, set model.Assignment) ([]any, *Error) {
	each, at := set.Each, "/object/values/"+set.Name
	lines := []any{}
	if r.body.IsBlank() {
		return lines, nil
	}
	elements, found, err := e.vm.JSONList(r.body.Str(), each.Path)
	switch {
	case err != nil:
		return nil, &Error{Code: CodeBadMessage, Path: at + "/each", Message: "the message does not give the lines of " + set.Name + ": " + each.Path + " leads to something that is no list",
			Hint: "the place names the path of the inlet that this message has no list at"}
	case !found:
		return lines, nil
	case len(elements) > MaxElements:
		return nil, &Error{Code: CodeBadMessage, Path: at + "/each", Message: "the message gives " + strconv.Itoa(len(elements)) + " lines of " + set.Name + " at " + each.Path + "; a document holds at most " + strconv.Itoa(MaxElements) + " (ADR-0013)",
			Hint: "send the lines in several documents"}
	}
	r.as = each.As
	defer func() { r.as, r.item = "", formula.Blank() }()
	for i, element := range elements {
		// One element, as a json value of its own: the formulas of the
		// row read it and not the whole message again, and the VM is
		// told what it parses to, so that none of them parses it.
		text, err := json.Marshal(element)
		if err != nil {
			return nil, badMessage(at+"/each", "the element "+strconv.Itoa(i+1)+" of "+each.Path, err)
		}
		r.item = formula.JSON(string(text))
		e.vm.SeedJSON(r.item.Str(), element)
		line := make(map[string]any, len(each.Row))
		for _, col := range each.Row {
			val, err := r.run(e, col.Formula)
			if err != nil {
				return nil, badMessage(at+"/row/"+col.Name, col.Name+" of the element "+strconv.Itoa(i+1)+" of "+each.Path, err)
			}
			line[col.Name] = valueOut(val)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// badMessage is the refusal of a message the inlet cannot read: what it
// could not work out, and why.
func badMessage(path, what string, err error) *Error {
	msg := "the message does not give " + what
	if err != nil {
		msg += ": " + evalErr("", err).Message
	}
	return &Error{Code: CodeBadMessage, Path: path, Message: msg, Hint: "the place names the formula of the inlet that this message has no answer for"}
}

// worked is what an inlet makes of a message: whether it acts on it,
// and then the key, the id and the values of its command.
type worked struct {
	acts   bool
	key    string
	id     string
	values Values
}

// makeOf evaluates the formulas of an inlet over a message.
func (s *Store) makeOf(spec *model.InletSpec, m *Message) (worked, *Error) {
	e := s.reader()
	defer s.release(e)
	// The formulas of an inlet read one body, which the VM parses once
	// and lets go of with the message.
	defer e.vm.ForgetJSON()
	r := reading(spec, m)
	var out worked
	if spec.When != nil {
		val, err := r.run(e, spec.When)
		if err != nil {
			return out, badMessage("/when", "whether the inlet acts on it", err)
		}
		if !val.Truth() {
			return out, nil
		}
	}
	out.acts = true
	// What makes two messages the same: the key the inlet reads from the
	// message, or the bytes of its body.
	if spec.Key != nil {
		val, err := r.run(e, spec.Key)
		switch {
		case err != nil:
			return out, badMessage("/key", "its key", err)
		case val.IsBlank() || val.Str() == "":
			return out, badMessage("/key", "its key: the formula of the key gives a blank", nil)
		case len(val.Str()) > MaxMessageKey:
			return out, badMessage("/key", "a key of at most "+strconv.Itoa(MaxMessageKey)+" characters", nil)
		}
		out.key = val.Str()
	} else {
		sum := sha256.Sum256(m.Body)
		out.key = hex.EncodeToString(sum[:])
	}
	out.key = inletKeyPrefix + spec.Name + ":" + out.key
	if spec.ID != nil {
		val, err := r.run(e, spec.ID)
		switch {
		case err != nil:
			return out, badMessage("/object/id", "the id of its document", err)
		case val.IsBlank() || val.Str() == "":
			if spec.Command != model.InletCreate {
				return out, badMessage("/object/id", "the id of its document: the formula of the id gives a blank", nil)
			}
		default:
			out.id = val.Str()
		}
	}
	out.values = make(Values, len(spec.Sets))
	for _, set := range spec.Sets {
		if set.Each != nil {
			lines, refused := r.lines(e, set)
			if refused != nil {
				return out, refused
			}
			out.values[set.Name] = lines
			continue
		}
		val, err := r.run(e, set.Formula)
		if err != nil {
			return out, badMessage("/object/values/"+set.Name, set.Name, err)
		}
		out.values[set.Name] = valueOut(val)
	}
	return out, nil
}

// proven checks the proof of a message (ADR-0065, item 3) with the
// secret, in a time that does not depend on where the two differ.
func proven(p model.Proof, secret string, m *Message, now time.Time) bool {
	var given string
	found := false
	for name, value := range m.Headers {
		if strings.ToLower(name) == p.Header {
			given, found = value, true
		}
	}
	if !found {
		return false
	}
	if p.Form == model.ProofTimestampBody {
		return VerifySignature(secret, given, m.Body, now, proofTolerance) == nil
	}
	given, found = strings.CutPrefix(given, p.Prefix)
	if !found {
		return false
	}
	want := secret
	if p.Form == model.ProofBody {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(m.Body)
		if sum := mac.Sum(nil); p.Encoding == model.EncodingBase64 {
			want = base64.StdEncoding.EncodeToString(sum)
		} else {
			want, given = hex.EncodeToString(sum), strings.ToLower(given)
		}
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
}

// Receive takes a message for an inlet (ADR-0065): it checks the proof,
// and then carries out the one command the message becomes, as the user
// the inlet names, with the key of the message, so that a message sent
// twice makes one commit and is answered the second time as the first.
//
// It asks for no caller: the proof is the message's own. A message
// whose proof does not hold is answered ErrNotProven and nothing of it
// is read. A name that is no inlet, and any name in a process that
// takes no messages, is unknown_inlet. A message the inlet cannot read
// is bad_message, and a command the database refuses is refused with
// the database's own error. Once the inlet is known the receipt names
// it and its user, whatever the error.
func (s *Store) Receive(ctx context.Context, name string, m *Message) (*Receipt, error) {
	in := s.inletNamed(name)
	if in == nil || !s.inbound {
		return nil, unknownInlet(name)
	}
	spec := in.spec
	receipt := &Receipt{Inlet: spec.Name, As: spec.As}
	if s.secretFn == nil {
		return receipt, hintf(CodeSecretUnavailable, "", "this process supplies no secrets, and the proof of "+spec.Name+" is made with one", "the host program gives Open a function for them, Options.Secrets")
	}
	secret, err := s.secretFn(ctx, spec.Connection, spec.Proof.Secret)
	if err != nil || secret == "" {
		why := "it is empty"
		if err != nil {
			why = err.Error()
		}
		return receipt, &Error{Code: CodeSecretUnavailable, Message: "the secret " + spec.Proof.Secret + " of the connection " + spec.Connection + ", which proves the messages of " + spec.Name + ": " + why}
	}
	if !proven(spec.Proof, secret, m, s.now()) {
		return receipt, ErrNotProven
	}
	p, why := s.actor(spec.As)
	if why != "" {
		e := unknownInlet(name)
		e.Message = "the inlet " + spec.Name + " acts as nobody: " + why
		e.Hint = "an administrator enables the user, or defines the inlet again with a service it may act as"
		return receipt, e
	}
	made, e := s.makeOf(spec, m)
	if e != nil {
		return receipt, e
	}
	if !made.acts {
		receipt.Ignored = true
		return receipt, nil
	}
	receipt.Key = made.key
	opts := []Option{As(p), Key(made.key), received()}
	var doc *Document
	switch spec.Command {
	case model.InletCreate:
		if made.id != "" {
			opts = append(opts, WithID(made.id))
		}
		doc, err = s.Create(ctx, spec.Class, made.values, opts...)
	case model.InletSet:
		doc, err = s.Set(ctx, spec.Class, made.id, made.values, opts...)
	default:
		doc, err = s.Transition(ctx, spec.Class, made.id, spec.Transition, made.values, opts...)
	}
	if err != nil {
		return receipt, err
	}
	receipt.Document = doc
	return receipt, nil
}

// received marks the command of an inlet, whose key may start with the
// prefix a caller may not use.
func received() Option { return func(o *options) { o.received = true } }

// TryInlet answers what an inlet would do with a message (ADR-0065,
// item 9), and does nothing: whether it acts on it, and the command
// with its object as it would be sent. The inlet is one of the
// database, by name, or a definition that is not stored, which must
// compile. The proof is not asked for: whoever tries is a caller the
// database knows. It is for a designer.
func (s *Store) TryInlet(ctx context.Context, name string, definition []byte, m *Message, opts ...Option) (*InletTrial, error) {
	if _, e := s.worker(ctx, workDesign, opts); e != nil {
		return nil, e
	}
	var spec *model.InletSpec
	switch {
	case len(definition) > 0 && name != "":
		return nil, hintf(CodeBadValue, "", "a trial names an inlet of the database or gives a definition, and this one does both", "give one of the two")
	case len(definition) > 0:
		s.cmu.RLock()
		compiled, diags := model.DefineInlet(definition, s.env())
		s.cmu.RUnlock()
		if len(diags) > 0 {
			return nil, diags
		}
		spec = compiled
	default:
		in := s.inletNamed(name)
		if in == nil {
			return nil, unknownInlet(name)
		}
		spec = in.spec
	}
	if m == nil {
		m = &Message{}
	}
	made, e := s.makeOf(spec, m)
	if e != nil {
		return nil, e
	}
	out := &InletTrial{Inlet: spec.Name, As: spec.As, Acts: made.acts}
	if !made.acts {
		return out, nil
	}
	out.Command = spec.Command
	out.Object = map[string]any{"class": spec.Class, "key": made.key}
	if made.id != "" {
		out.Object["id"] = made.id
	}
	if spec.Command == model.InletTransition {
		out.Object["transition"] = spec.Transition
	}
	if len(made.values) > 0 {
		out.Object["values"] = made.values
	}
	return out, nil
}
