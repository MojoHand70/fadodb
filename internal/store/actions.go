package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Actions are an extension point (ADR-0045). An action a rule names is
// either the engine's own or has a definition: JSON, like a class, with a
// name of a namespace and a verb, typed parameters and what it returns.
// The definition is data and lives in the database; what implements it
// lives in the process: a Go function the host program registers under
// the action's name (ADR-0027), or for a definition with a request the
// Outbound the host program allows requests through (requests.go). The
// commit evaluates the arguments and queues a row, and the worker calls
// the function, or sends the request, with them and with the event that
// fired the rule, outside any transaction, at least once.

// ActionFunc is a Go function that implements an action (ADR-0027,
// ADR-0045). The outbox worker calls it once for each queued action, on
// its own goroutine and outside any transaction, so it may take its time
// and may call the database. It ran when it returns no error, and what it
// returns are the values its definition declares under returns. An error
// is no answer: the action is tried again later, up to MaxAttempts. An
// error wrapped with Permanent is a refusal: the action is dead at once.
// A crash between its effect and the mark of its row repeats the call
// with the same Key, so the function makes twice once by that key. The
// context ends when the database closes. A command the function sends is
// the function's own, under its own principal: it starts a chain of its
// own, which the bound on chains of actions does not follow. It must not
// call Drain.
type ActionFunc func(ctx context.Context, call *ActionCall) (Values, error)

// ActionCall is what an action is called with (ADR-0045, item 4). Its
// JSON is the published shape of a call, api/call.schema.json.
type ActionCall struct {
	// Key identifies the queued action and is the same at every attempt:
	// "<event_id>:<idx>" (ADR-0044). The key of a trial starts with "try:".
	Key string `json:"key"`
	// Attempt counts the calls of this action from 1.
	Attempt int    `json:"attempt"`
	Action  string `json:"action"`
	// Args are the arguments as the commit evaluated them, one for each
	// parameter of the definition by its declared name, in the wire shape
	// a document's values have on the way out; a parameter not given is
	// nil.
	Args Values `json:"args"`
	// Event is the event that fired the rule; nil in a trial, which
	// belongs to no document.
	Event *DeliveredEvent `json:"event,omitempty"`
}

// DeliveredEvent is an event as an action receives it (ADR-0026,
// ADR-0045): the event of the commit whose rule queued the action, with
// the document at the version that commit made, so that the receiver
// needs no second request. No principal's read rights apply to the
// document: it is what the class's author chose to hand to the action.
// Its JSON is api/event.schema.json.
type DeliveredEvent struct {
	ID   string `json:"event_id"`
	Kind string `json:"kind"`
	// Class and ClassVersion are the class of the document and the version
	// of it whose rule queued the action.
	Class        string `json:"class"`
	ClassVersion int    `json:"class_version"`
	DocID        string `json:"doc_id"`
	// Name is the transition of a transition event; FromState and ToState
	// are the states it left and entered.
	Name      string `json:"name,omitempty"`
	FromState string `json:"from_state,omitempty"`
	ToState   string `json:"to_state,omitempty"`
	// Version is the version the commit made, and Document the document
	// at it; for a delete, the last version there was.
	Version  int64     `json:"version"`
	Document *Document `json:"document"`
	ActorID  string    `json:"actor_id,omitempty"`
	// ActorKind is what the actor was, person, agent or service, and
	// ActorFor the id it acted for (ADR-0050); each absent when the host
	// program did not say.
	ActorKind string `json:"actor_kind,omitempty"`
	ActorFor  string `json:"actor_for,omitempty"`
	// BusinessDate is the calendar day as YYYY-MM-DD.
	BusinessDate string `json:"business_date"`
	// CommandKey is the idempotency key of the command that caused the
	// event, when it had one.
	CommandKey string `json:"command_key,omitempty"`
	TS         int64  `json:"ts"`
	// Origin says, on an event that enters a state, how the move came
	// about (ADR-0047, item 3): command, action, guard or timer.
	Origin string `json:"origin,omitempty"`
}

// permanent marks the error of an action as a refusal.
type permanent struct{ err error }

func (p *permanent) Error() string { return p.err.Error() }
func (p *permanent) Unwrap() error { return p.err }

// Permanent marks an error a Go action returns as a refusal (ADR-0045,
// item 5): an answer, which asking again would not change. The action is
// dead at once with the error, where any other error is tried again. A
// nil error stays nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanent{err: err}
}

// The kinds of action: what implements the contract.
const (
	// ActionEngine is an action the engine implements itself.
	ActionEngine = "engine"
	// ActionRegistered is an action a Go function of the host program
	// implements.
	ActionRegistered = "registered"
	// ActionDeclared is an action the engine sends as an HTTP request: a
	// definition with a request, and the engine's own http.call.
	ActionDeclared = "declared"
)

// ActionInfo is one action a rule may name, as the catalogue lists it
// (ADR-0045, item 9), and what DefineAction returns.
type ActionInfo struct {
	Name string `json:"name"`
	// Kind is engine, registered or declared.
	Kind string `json:"kind"`
	// Version is the version of the definition; an engine action has none.
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Params are what a rule gives the action and Returns what it answers,
	// each in the order declared.
	Params  []ActionParam `json:"params,omitempty"`
	Returns []ActionParam `json:"returns,omitempty"`
	// Connection is the connection the request of a declared action goes
	// over, and Secrets the secrets of it the request reads; both absent
	// when it has none.
	Connection string   `json:"connection,omitempty"`
	Secrets    []string `json:"secrets,omitempty"`
	// Sync is true for an action that runs inside the commit, on the
	// rule's own document; every other action is queued and runs after it.
	Sync bool `json:"sync,omitempty"`
	// Available says whether this process can run the action: always for
	// an engine action; for a registered one when its function was given
	// to Open; for a declared one when the host program allows outbound
	// calls, knows its connection and supplies secrets if it reads any.
	Available bool `json:"available"`
	// Defined says who defined this version of the definition, and when
	// (ADR-0052, item 7); nil for an action of the engine's own.
	Defined *Defined `json:"defined,omitempty"`
}

// ActionParam is one parameter of an action, or one value it returns. For
// a defined action Type is a scalar type of ADR-0013, and a rule gives the
// parameter a formula of that type under args. For an engine action Type
// is the shape of the property the action is written with: name, names,
// formula, or formulas for an object of formulas.
type ActionParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// ActionPreview is what an action would be called with or would send
// (ADR-0045, item 9); nothing was called and nothing sent.
type ActionPreview struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	// Call is the call: what a registered action would receive, and what
	// the request of a declared one is made from.
	Call *ActionCall `json:"call,omitempty"`
	// Request is the request a declared action would send, with every
	// secret masked.
	Request *RequestPreview `json:"request,omitempty"`
}

// ActionOutcome is what became of one run of an action outside any
// document (ADR-0045, item 9).
type ActionOutcome struct {
	Action string `json:"action"`
	// OK says the action ran, and Result is what it returned, by the names
	// its definition declares.
	OK     bool   `json:"ok"`
	Result Values `json:"result,omitempty"`
	// Code and Message say why it did not run, or with OK why what it
	// returned does not fit its definition (bad_result). Retry is true
	// when the failure is no answer, which the outbox would ask again, and
	// false for a refusal.
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Retry   bool   `json:"retry,omitempty"`
}

// action is an action definition, with what this process runs it with:
// the function of a registered action, and for a declared one the secrets
// its request reads.
type action struct {
	spec  *model.ActionSpec
	json  []byte
	fn    ActionFunc
	reads []string
	// defined says who defined this version (ADR-0052, item 7).
	defined Defined
}

// kind says what implements the action.
func (a *action) kind() string {
	if a.spec.Declared() {
		return ActionDeclared
	}
	return ActionRegistered
}

// info is the action as the catalogue lists it.
func (s *Store) info(a *action) *ActionInfo {
	spec := a.spec
	info := &ActionInfo{Name: spec.Name, Kind: a.kind(), Version: spec.Version, Label: spec.Label, Description: spec.Description,
		Params: paramsOut(spec.Params), Returns: paramsOut(spec.Returns), Connection: spec.Connection, Secrets: a.reads, Available: a.fn != nil}
	if spec.Declared() {
		info.Available = s.outbound != nil && s.outbound.Check(spec.Connection, "") == nil && (len(a.reads) == 0 || s.secretFn != nil)
	}
	if spec.Name == model.ActionHTTPCall {
		// The engine's own: it has no version of a definition.
		info.Version = 0
	}
	if a.defined != (Defined{}) {
		d := a.defined
		info.Defined = &d
	}
	return info
}

func paramsOut(list []model.Param) []ActionParam {
	if len(list) == 0 {
		return nil
	}
	out := make([]ActionParam, len(list))
	for i, p := range list {
		out[i] = ActionParam{Name: p.Name, Type: p.Type.String(), Required: p.Required, Description: p.Description}
	}
	return out
}

// engineCatalogue are the engine's own actions as the catalogue lists
// them (ADR-0043, ADR-0044): they are written with properties of their
// own, not with args.
var engineCatalogue = []ActionInfo{
	{Name: model.ActionSet, Kind: ActionEngine, Sync: true, Available: true, Label: "Set variables",
		Description: "Assigns input variables of the rule's own document, inside the commit; what depends on them recalculates.",
		Params:      []ActionParam{{Name: "values", Type: "formulas", Required: true, Description: "A formula for each input variable to assign."}}},
	{Name: model.ActionReset, Kind: ActionEngine, Sync: true, Available: true, Label: "Reset variables",
		Description: "Puts input variables of the rule's own document back to their defaults, inside the commit.",
		Params:      []ActionParam{{Name: "variables", Type: "names", Required: true, Description: "The input variables to reset."}}},
	{Name: model.ActionRefresh, Kind: ActionEngine, Sync: true, Available: true, Label: "Refresh triggered variables",
		Description: "Recomputes triggered variables that list refresh: of the rule's own document inside the commit, or with an id of another document, queued, which then moves by its own conditions.",
		Params: []ActionParam{{Name: "variables", Type: "names", Description: "The triggered variables to recompute; every one that lists refresh when absent."},
			{Name: "class", Type: "name", Description: "The class of the other document; the rule's own when absent."},
			{Name: "id", Type: "formula", Description: "The id of the other document. With it the action is queued."}}},
	{Name: model.ActionEmit, Kind: ActionEngine, Sync: true, Available: true, Label: "Emit an event",
		Description: "Appends a custom event to the log of the rule's own document.",
		Params:      []ActionParam{{Name: "event", Type: "name", Required: true, Description: "The name of the event, identifiers joined by dots."}}},
	{Name: model.ActionCreate, Kind: ActionEngine, Available: true, Label: "Create a document",
		Description: "Creates a document of a class, or one for each element of a list, as the person whose command fired the rule.",
		Params: []ActionParam{{Name: "class", Type: "name", Required: true, Description: "The class of the new document."},
			{Name: "values", Type: "formulas", Description: "A formula for each input variable of the new document."},
			{Name: "id", Type: "formula", Description: "The id of the new document; the engine gives one when absent."},
			{Name: "each", Type: "name", Description: "An array or rows variable: one document for each element."},
			{Name: "as", Type: "name", Description: "The name the formulas read the element by; required with each."}}},
	{Name: model.ActionTransition, Kind: ActionEngine, Available: true, Label: "Take a transition",
		Description: "Takes a named transition of a document, as the person whose command fired the rule.",
		Params: []ActionParam{{Name: "class", Type: "name", Description: "The class of the document; the rule's own when absent."},
			{Name: "id", Type: "formula", Required: true, Description: "The id of the document."},
			{Name: "name", Type: "name", Required: true, Description: "The transition to take."},
			{Name: "values", Type: "formulas", Description: "A formula for each input variable to assign in the same commit."}}},
	{Name: model.ActionTableInsert, Kind: ActionEngine, Available: true, Label: "Insert a table row",
		Description: "Inserts one row into an explicit table; a key the table has is refused with duplicate.",
		Params: []ActionParam{{Name: "table", Type: "name", Required: true, Description: "The table."},
			{Name: "row", Type: "formulas", Required: true, Description: "A formula for each column, every key column among them."}}},
	{Name: model.ActionTableUpdate, Kind: ActionEngine, Available: true, Label: "Update a table row",
		Description: "Changes the row of an explicit table with a key; a key the table lacks is refused with unknown_row.",
		Params: []ActionParam{{Name: "table", Type: "name", Required: true, Description: "The table."},
			{Name: "key", Type: "formulas", Required: true, Description: "A formula for each key column."},
			{Name: "set", Type: "formulas", Required: true, Description: "A formula for each column to change; no key column."}}},
	{Name: model.ActionTableDelete, Kind: ActionEngine, Available: true, Label: "Delete a table row",
		Description: "Deletes the row of an explicit table with a key; a key the table lacks deletes nothing.",
		Params: []ActionParam{{Name: "table", Type: "name", Required: true, Description: "The table."},
			{Name: "key", Type: "formulas", Required: true, Description: "A formula for each key column."}}},
}

// registerAction makes a definition the current one of its name. The
// caller holds cmu.
func (s *Store) registerAction(a *action) {
	key := strings.ToUpper(a.spec.Name)
	if _, exists := s.actions[key]; !exists {
		s.actionOrder = append(s.actionOrder, a.spec.Name)
	}
	a.fn, a.reads = s.funcs[key], secretsRead(a.spec)
	s.actions[key] = a
}

// actionNamed finds a stored definition by name.
func (s *Store) actionNamed(name string) *action {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.actions[strings.ToUpper(name)]
}

// Actions lists every action a rule may name in this database and this
// process (ADR-0045, item 9): the engine's own first, http.call last
// among them, then the defined ones in the order they were defined, each
// with its parameters, what it returns and whether this process can run
// it.
func (s *Store) Actions() []ActionInfo {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	out := make([]ActionInfo, 0, len(engineCatalogue)+1+len(s.actionOrder))
	out = append(out, engineCatalogue...)
	out = append(out, *s.info(s.actions[strings.ToUpper(model.ActionHTTPCall)]))
	for _, name := range s.actionOrder {
		out = append(out, *s.info(s.actions[strings.ToUpper(name)]))
	}
	return out
}

// Action returns one action of the catalogue by name.
func (s *Store) Action(name string) (*ActionInfo, bool) {
	if model.EngineAction(name) {
		for i := range engineCatalogue {
			if engineCatalogue[i].Name == name {
				info := engineCatalogue[i]
				return &info, true
			}
		}
	}
	if a := s.actionNamed(name); a != nil {
		return s.info(a), true
	}
	return nil, false
}

// ValidateAction compiles an action definition and reports what
// DefineAction would refuse, without storing anything.
func (s *Store) ValidateAction(data []byte) model.Diagnostics {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	_, diags, _ := s.compileAction(data)
	return diags
}

// compileAction compiles a definition and checks it against the stored
// one of the same name, the rules that use it and the functions of this
// process. It returns the version the definition would get. The caller
// holds cmu.
func (s *Store) compileAction(data []byte) (*model.ActionSpec, model.Diagnostics, int) {
	spec, diags := model.DefineAction(data)
	if spec == nil {
		return nil, diags, 0
	}
	version := spec.Version
	if old := s.actions[strings.ToUpper(spec.Name)]; old != nil {
		if old.spec.Name != spec.Name {
			diags = append(diags, model.Diagnostic{Code: model.CodeDuplicate, Path: "/name", Pos: -1,
				Message: "an action " + old.spec.Name + " exists; names differ in case only"})
		}
		version = old.spec.Version + 1
		if spec.Def.Version != 0 && spec.Def.Version != version {
			diags = append(diags, model.Diagnostic{Code: model.CodeBadValue, Path: "/version", Pos: -1,
				Message: "the stored action is at version " + itoa(old.spec.Version) + "; a redefinition is version " + itoa(version),
				Hint:    "omit version, or write " + itoa(version)})
		}
		// A rule of a class may call the action: a new shape may not break
		// it (ADR-0044, item 7).
		diags = append(diags, s.brokenRules(nil, nil, spec)...)
	}
	add := func(code, path, message, hint string) {
		diags = append(diags, model.Diagnostic{Code: code, Path: path, Pos: -1, Message: message, Hint: hint})
	}
	fn := s.funcs[strings.ToUpper(spec.Name)]
	switch {
	case !spec.Declared():
		// A definition without a request is the contract of a Go function,
		// and the process that defines it has the function (ADR-0027).
		if fn == nil {
			add(model.CodeNotSupported, "/name", "nothing in this process implements "+spec.Name+": the definition has no request, so it is the contract of a Go function, and none is registered under that name",
				"the host program gives the function to Open, in Options.Actions; or the definition gives a request for the engine to send")
		}
	case s.outbound == nil:
		// Whether this process may reach the network is its host program's
		// decision (ADR-0045, item 7).
		add(model.CodeNotSupported, "/request", "this process sends no requests, so it cannot take a definition that has one",
			"the host program allows outbound calls when it opens the database, with Options.Outbound")
	default:
		if fn != nil {
			add(model.CodeNotAllowed, "/request", "a Go function is registered under "+spec.Name+" in this process, and the definition has a request: an action is one or the other",
				"drop the request, or the function")
		}
		// The request goes over the definition's connection, to the hosts
		// the host program names for it, and reads its secrets only.
		if refused := s.outbound.Check(spec.Connection, spec.Request.ConstURL); refused != nil {
			switch err := asError(refused); err.Code {
			case CodeUnknownConnection:
				add(model.CodeUnknownConnection, "/connection/name", err.Message, err.Hint)
			default:
				add(model.CodeNotAllowed, "/request/url", err.Message, err.Hint)
			}
		}
		if len(secretsRead(spec)) > 0 && s.secretFn == nil {
			add(model.CodeNotSupported, "/connection/secrets", "this process supplies no secrets, and the request reads "+strings.Join(secretsRead(spec), ", "),
				"the host program gives Open a function for them, Options.Secrets")
		}
	}
	return spec, diags, version
}

// DefineAction compiles an action definition and stores it with its
// version (ADR-0045, item 1); a rule may name the action from then on.
// Defining the same definition again is a no-op. A redefinition is the
// next version: it may add a parameter or a returned value, and may not
// take away or retype what a rule of a class gives, which is refused with
// not_allowed naming the rule. A definition without a request is the
// contract of a Go function, so the process that defines it must have
// been given the function, in Options.Actions. A definition with a
// request is sent by the engine, so the process must allow outbound
// calls, with Options.Outbound, know the definition's connection and, if
// the request reads a secret, supply secrets.
func (s *Store) DefineAction(ctx context.Context, data []byte, opts ...Option) (*ActionInfo, error) {
	o, e := s.worker(ctx, workDesign, opts)
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
	if old := s.actions[upperName(data)]; old != nil && bytes.Equal(old.json, compact) {
		return s.info(old), nil
	}
	spec, diags, version := s.compileAction(data)
	if len(diags) > 0 {
		return nil, diags
	}
	spec.Version = version
	now := s.now().Unix()
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	rollback := func(err error) (*ActionInfo, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return nil, err
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO actions (name, version, json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)"+
		" ON CONFLICT (name) DO UPDATE SET name = excluded.name, version = excluded.version, json = excluded.json, updated_at = excluded.updated_at",
		spec.Name, version, string(compact), now, now); err != nil {
		return rollback(storageErr("store the action", err))
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO action_versions (action, version, json, created_at) VALUES (?, ?, ?, ?)", spec.Name, version, string(compact), now); err != nil {
		return rollback(storageErr("store the action version", err))
	}
	defined, derr := recordDefinition(ctx, w, DefinitionAction, spec.Name, version, o.principal, now)
	if derr != nil {
		return rollback(derr)
	}
	if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, storageErr("commit", err)
	}
	a := &action{spec: spec, json: compact, defined: defined}
	s.registerAction(a)
	return s.info(a), nil
}

// loadActions reads the stored action definitions in the order they were
// defined. The caller is Open.
func (s *Store) loadActions(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT name, version, json FROM actions ORDER BY rowid")
	if err != nil {
		return storageErr("read the actions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var version int
		var src []byte
		if err := rows.Scan(&name, &version, &src); err != nil {
			return storageErr("read the actions", err)
		}
		spec, diags := model.DefineAction(src)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored action " + name + " does not compile: " + diags.Error()}
		}
		spec.Version = version
		s.registerAction(&action{spec: spec, json: src})
	}
	return storageErr("read the actions", rows.Err())
}

// checkFuncs reads the functions Open was given: each under the name of
// an action a definition may have.
func checkFuncs(given map[string]ActionFunc) (map[string]ActionFunc, error) {
	funcs := make(map[string]ActionFunc, len(given))
	for name, fn := range given {
		key := strings.ToUpper(name)
		// A definition of the name alone says whether the name can be one.
		if _, diags := model.DefineAction([]byte(`{"name": ` + strconv.Quote(name) + `}`)); len(diags) > 0 {
			return nil, hintf(CodeBadValue, "", "Options.Actions: "+diags[0].Message, diags[0].Hint)
		}
		if fn == nil {
			return nil, errf(CodeBadValue, "", "Options.Actions: the function of %s is nil", name)
		}
		if _, dup := funcs[key]; dup {
			return nil, errf(CodeBadValue, "", "Options.Actions: %s is given twice; names differ in case only", name)
		}
		funcs[key] = fn
	}
	return funcs, nil
}

// readArgs reads the arguments of a call against the parameters of its
// action: every name given is a parameter, every value is of its
// parameter's type, and a required one is not blank. It answers them by
// parameter as declared, in the wire shape of a document's values on the
// way out, a parameter not given as nil.
func (s *Store) readArgs(spec *model.ActionSpec, given map[string]any) (Values, *Error) {
	names := make([]string, 0, len(given))
	for name := range given {
		names = append(names, name)
	}
	sort.Strings(names)
	by := make(map[string]any, len(given))
	for _, name := range names {
		p, ok := spec.Param(name)
		if !ok {
			return nil, hintf(CodeUnknownParameter, name, "the action "+spec.Name+" has no parameter "+strconv.Quote(name), "Actions lists its parameters")
		}
		if _, dup := by[p.Name]; dup {
			return nil, errf(CodeBadValue, p.Name, "%s is given twice; names differ in case only", p.Name)
		}
		by[p.Name] = given[name]
	}
	out := make(Values, len(spec.Params))
	for i := range spec.Params {
		p := &spec.Params[i]
		val, err := toValue(by[p.Name], p.Type, "", p.Name, s.limits)
		if err != nil {
			return nil, err
		}
		if p.Required && val.IsBlank() {
			return nil, errf(CodeRequired, p.Name, "%s requires %s, and it is blank", spec.Name, p.Name)
		}
		out[p.Name] = valueOut(val)
	}
	return out, nil
}

// rowArgs reads the arguments a commit stored with a row of the outbox.
func (s *Store) rowArgs(spec *model.ActionSpec, raw []byte) (Values, *Error) {
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, errf(CodeBadValue, "", "the arguments of the action do not read: %v", err)
	}
	given := make(map[string]any, len(stored))
	for name, value := range stored {
		// A json argument is the JSON as it stands; any other is a scalar
		// in the wire shape.
		if p, ok := spec.Param(name); ok && p.Type.Kind == formula.KindJSON {
			if string(bytes.TrimSpace(value)) != "null" {
				given[name] = value
			}
			continue
		}
		var x any
		if err := json.Unmarshal(value, &x); err != nil {
			return nil, errf(CodeBadValue, name, "the argument does not read: %v", err)
		}
		given[name] = x
	}
	return s.readArgs(spec, given)
}

// readResult reads what an action returned against what its definition
// declares it returns, by declared name, a value not returned as nil.
func (s *Store) readResult(spec *model.ActionSpec, result Values) (Values, *Error) {
	names := make([]string, 0, len(result))
	for name := range result {
		names = append(names, name)
	}
	sort.Strings(names)
	by := make(map[string]any, len(result))
	for _, name := range names {
		r, ok := spec.Return(name)
		if !ok {
			return nil, hintf(CodeBadResult, name, "the action returned "+strconv.Quote(name)+", which the definition of "+spec.Name+" does not declare", "declare it under returns, or do not return it")
		}
		by[r.Name] = result[name]
	}
	if len(spec.Returns) == 0 {
		return nil, nil
	}
	out := make(Values, len(spec.Returns))
	for i := range spec.Returns {
		r := &spec.Returns[i]
		val, err := toValue(by[r.Name], r.Type, "", r.Name, s.limits)
		if err != nil {
			return nil, &Error{Code: CodeBadResult, Variable: r.Name, Message: "what the action returned as " + r.Name + " is no " + r.Type.String() + ": " + err.Message}
		}
		out[r.Name] = valueOut(val)
	}
	return out, nil
}

// trial finds the action of a preview or a trial and reads its
// arguments.
func (s *Store) trial(name string, args Values) (*action, Values, *Error) {
	if model.EngineAction(name) {
		return nil, nil, hintf(CodeNotAllowed, "", "the engine's own action "+name+" acts on a document and is not run alone", "Preview shows what the rules of a command would do")
	}
	a := s.actionNamed(name)
	if a == nil {
		return nil, nil, hintf(CodeUnknownAction, "", "unknown action "+strconv.Quote(name), "Actions lists what this database has")
	}
	vals, err := s.readArgs(a.spec, args)
	if err != nil {
		return nil, nil, err
	}
	return a, vals, nil
}

// PreviewAction answers what an action would be called with for the
// arguments, given as values in the wire shape, and for a declared action
// the request it would send, with secrets masked; it calls and sends
// nothing (ADR-0045, item 9). The arguments are checked as the worker
// checks them: unknown_parameter, bad_value, required.
func (s *Store) PreviewAction(ctx context.Context, name string, args Values, opts ...Option) (*ActionPreview, error) {
	if _, e := s.worker(ctx, workOperate, opts); e != nil {
		return nil, e
	}
	a, vals, err := s.trial(name, args)
	if err != nil {
		return nil, err
	}
	pv := &ActionPreview{Action: a.spec.Name, Kind: a.kind(), Call: &ActionCall{Attempt: 1, Action: a.spec.Name, Args: vals}}
	if a.spec.Declared() {
		// The request as it would be sent, every secret masked and none
		// asked for; a request the outbound rules would refuse is refused
		// here.
		if pv.Request, err = s.previewRequest(a, pv.Call); err != nil {
			return nil, err
		}
	}
	return pv, nil
}

// TryAction runs an action once outside any document, for the arguments
// given as values in the wire shape (ADR-0045, item 9): it queues
// nothing, writes no event and answers what the action returned, or how
// it failed. The call carries no event and a key of its own. The error is
// a refusal of the trial itself: an unknown action, arguments that do not
// fit, or an action this process cannot run. The request of a declared
// action is sent for real, to its receiver.
func (s *Store) TryAction(ctx context.Context, name string, args Values, opts ...Option) (*ActionOutcome, error) {
	if _, e := s.worker(ctx, workOperate, opts); e != nil {
		return nil, e
	}
	a, vals, e := s.trial(name, args)
	if e != nil {
		return nil, e
	}
	switch {
	case a.spec.Declared() && s.outbound == nil:
		return nil, outboundOff(a.spec.Name)
	case !a.spec.Declared() && a.fn == nil:
		return nil, notRegistered(a.spec.Name)
	}
	did := s.act(ctx, a, &ActionCall{Key: "try:" + s.ids.next(s.now()), Attempt: 1, Action: a.spec.Name, Args: vals})
	out := &ActionOutcome{Action: a.spec.Name}
	if did.failed() {
		out.Code, out.Message, out.Retry = did.failure.Code, did.failure.Message, did.retry
		return out, nil
	}
	out.OK, out.Result = true, did.result
	if did.badResult != nil {
		out.Result, out.Code, out.Message = nil, did.badResult.Code, did.badResult.Message
	}
	return out, nil
}

// act does what an action stands for, for one call: the request of a
// declared action is sent, the function of a registered one is called,
// and what either returns is read against the definition.
func (s *Store) act(ctx context.Context, a *action, call *ActionCall) outcome {
	if a.spec.Declared() {
		return s.send(ctx, a, call)
	}
	result, failure, retry := invoke(ctx, a.fn, call)
	if failure != nil {
		return outcome{failure: failure, retry: retry}
	}
	out := outcome{}
	out.result, out.badResult = s.readResult(a.spec, result)
	return out
}

func notRegistered(name string) *Error {
	return hintf(CodeNotRegistered, "", "nothing in this process implements the action "+name, "the host program gives its function to Open, in Options.Actions")
}

// invoke calls the function of an action and reads its answer (ADR-0045,
// item 5): it ran when the function returns no error; an error is no
// answer, to be asked again, and an error wrapped with Permanent a
// refusal. A function that panics failed as if it had returned the panic.
func invoke(ctx context.Context, fn ActionFunc, call *ActionCall) (result Values, failure *Error, retry bool) {
	defer func() {
		if r := recover(); r != nil {
			result, failure, retry = nil, &Error{Code: CodeActionError, Message: fmt.Sprintf("the action panicked: %v", r)}, true
		}
	}()
	result, err := fn(ctx, call)
	if err == nil {
		return result, nil, false
	}
	var perm *permanent
	if errors.As(err, &perm) {
		return nil, actionError(perm.err), false
	}
	return nil, actionError(err), true
}

// actionError is the error of an action's function as the row records
// it: the store's own error when the function returned one, with its
// code, else action_error with the error's words.
func actionError(err error) *Error {
	var se *Error
	if errors.As(err, &se) {
		return &Error{Code: se.Code, Variable: se.Variable, Message: se.Message, Hint: se.Hint}
	}
	return &Error{Code: CodeActionError, Message: err.Error()}
}

// calling is what the worker reads of a row and of its event before it
// calls the action.
type calling struct {
	status                string
	attempts              int
	version               int64
	classVersion          int
	kind                  string
	name, from, to, actor sql.NullString
	commandKey            sql.NullString
	origin                sql.NullInt64
	day, ts               int64
}

// runCall runs one row of an action that leaves the database (ADR-0045):
// it calls the function outside any transaction, so that a slow action
// delays the queue and never a commit (ADR-0027), then marks the row and
// appends the event of the outcome under the writer. The call happens at
// least once: a crash between its effect and the mark repeats it with the
// same key (ADR-0044, item 5). It reports whether the row was attempted.
func (s *Store) runCall(ctx context.Context, row *outboxRow) (bool, error) {
	var c calling
	err := s.stmts.callOutbox.QueryRowContext(ctx, row.eventID, row.idx).Scan(&c.status, &c.attempts, &c.version, &c.classVersion,
		&c.kind, &c.name, &c.from, &c.to, &c.actor, &c.commandKey, &c.day, &c.ts, &c.origin)
	if err == sql.ErrNoRows || err == nil && c.status != ActionPending {
		return false, nil
	}
	if err != nil {
		return false, storageErr("read the action", err)
	}
	did := outcome{failure: s.at("perform", row)}
	if did.failure == nil {
		did = s.call(ctx, row, &c)
	} else {
		did.retry = noAnswer(did.failure)
	}
	if did.failure == nil {
		if did.failure = s.at("performed", row); did.failure != nil {
			did.retry = noAnswer(did.failure)
		}
	}
	failure := did.failure
	if ctx.Err() != nil {
		// The caller gave up; the row is as it was.
		return false, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, storageErr("begin", err)
	}
	// The row is still as it was read: its document is claimed.
	var status string
	var attempts int
	err = w.QueryRowContext(ctx, `SELECT status, attempts FROM outbox WHERE event_id = ? AND idx = ?`, row.eventID, row.idx).Scan(&status, &attempts)
	if err != nil || status != ActionPending {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		if err != nil && err != sql.ErrNoRows {
			return false, storageErr("read the action", err)
		}
		return false, nil
	}
	// The mark, the event of the outcome and, when the action has ended,
	// its result written back to the document: one transaction.
	var ended string
	if failure == nil {
		ended, err = s.finish(ctx, row, ActionDone, attempts+1, nil, c.day, now)
	} else {
		ended, err = s.failed(ctx, row, attempts, failure, did.retry, did.after, c.day, now)
	}
	queuedMore := false
	if err == nil && ended != "" {
		queuedMore, err = s.applyInto(ctx, row, ended, did, nil, c.day, now)
	}
	if e := s.at("marked", row); err == nil && e != nil {
		err = e
	}
	if err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, err
	}
	if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return false, storageErr("commit", err)
	}
	if failure == nil {
		_ = s.at("committed", row)
	}
	s.wakeQueued(queuedMore, false)
	return true, nil
}

// call makes the call of a row: the definition of its action, its
// arguments read against the definition as it is now, the event that
// fired the rule, and then the function or the request. What the row
// cannot be called with is a refusal; what this process lacks to run the
// action, its function or the leave to send requests, and a store that
// fails are no answer. What the action returns is written back to the
// document by the row's into, when it has one.
func (s *Store) call(ctx context.Context, row *outboxRow, c *calling) outcome {
	a := s.actionNamed(row.action)
	if a == nil {
		return refusal(hintf(CodeUnknownAction, "", "the database has no action "+row.action, "the row was queued when it had one; define the action again and retry the row"))
	}
	args, e := s.rowArgs(a.spec, row.args)
	if e != nil {
		return refusal(e)
	}
	switch {
	case a.spec.Declared() && s.outbound == nil:
		return noReply(outboundOff(a.spec.Name))
	case !a.spec.Declared() && a.fn == nil:
		return noReply(notRegistered(a.spec.Name))
	}
	ev, err := s.delivered(ctx, row, c)
	if err != nil {
		failure := asError(err)
		return outcome{failure: failure, retry: noAnswer(failure)}
	}
	return s.act(ctx, a, &ActionCall{Key: row.key(), Attempt: c.attempts + 1, Action: a.spec.Name, Args: args, Event: ev})
}

// delivered builds the event a row's action receives (ADR-0026,
// ADR-0045, item 4): the event of the commit that queued the row, and the
// document at the version that commit made, whole.
func (s *Store) delivered(ctx context.Context, row *outboxRow, c *calling) (*DeliveredEvent, error) {
	cls, e := s.class(row.class)
	if e != nil {
		return nil, e
	}
	vc, drow, err := s.version(ctx, cls, row.docID, c.version)
	if err != nil {
		return nil, err
	}
	kids, err := vc.loadChildren(ctx, row.docID, c.version, nil, false)
	if err != nil {
		return nil, err
	}
	return &DeliveredEvent{ID: row.eventID, Kind: c.kind, Class: cls.cls.Name, ClassVersion: c.classVersion, DocID: row.docID,
		Name: c.name.String, FromState: c.from.String, ToState: c.to.String, Version: c.version, Document: vc.document(drow, kids),
		// The row's principal is the actor of the event that queued it,
		// with what it was and for whom it acted (ADR-0050).
		ActorID: c.actor.String, ActorKind: row.principal.Kind, ActorFor: row.principal.For, BusinessDate: formula.Render(formula.Date(c.day)), CommandKey: c.commandKey.String, TS: c.ts, Origin: originOf(c.origin.Int64)}, nil
}
