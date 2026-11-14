package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The suites of the script format run through every door and must answer
// the same (ADR-0055, item 11). A door is how a step reaches the store:
// the Go API, as the suites have always run, or the command set, where
// each step is a name and one JSON object and its answer is read back
// from JSON. The door for requests to a server joins them with the
// server.

// stepCall is who a step runs as and with which options.
type stepCall struct {
	// as is the caller, nil for a step that names none.
	as     *Principal
	key    string
	reason string
	// at is the moment the step reads at, in the notation of the suites;
	// id is the id a create gives its document.
	at string
	id string
}

// by is a call as the principal, with no other option.
func by(p Principal) stepCall { return stepCall{as: &p} }

// opts are the options of the Go API for the call.
func (c stepCall) opts() []Option {
	var opts []Option
	if c.as != nil {
		opts = append(opts, As(*c.as))
	}
	if c.key != "" {
		opts = append(opts, Key(c.key))
	}
	if c.reason != "" {
		opts = append(opts, Reason(c.reason))
	}
	if c.at != "" {
		secs, err := formula.ParseCaseDate(c.at)
		if err != nil {
			panic(err)
		}
		opts = append(opts, At(time.Unix(secs, 0).UTC()))
	}
	if c.id != "" {
		opts = append(opts, WithID(c.id))
	}
	return opts
}

// door is what a suite needs of the store.
type door interface {
	name() string
	DefineAction(ctx context.Context, def []byte, c stepCall) error
	DefineTable(ctx context.Context, def []byte, c stepCall) error
	// DefineClass and DefineClasses answer the declared name of the
	// class, of the last one for several.
	DefineClass(ctx context.Context, def []byte, c stepCall) (string, error)
	DefineClasses(ctx context.Context, defs [][]byte, c stepCall) (string, error)
	Create(ctx context.Context, class string, values Values, c stepCall) (*Document, error)
	Set(ctx context.Context, class, id string, values Values, c stepCall) (*Document, error)
	Reset(ctx context.Context, class, id string, variables []string, c stepCall) (*Document, error)
	Refresh(ctx context.Context, class, id string, variables []string, c stepCall) (*Document, error)
	Transition(ctx context.Context, class, id, name string, values Values, c stepCall) (*Document, error)
	Delete(ctx context.Context, class, id string, c stepCall) error
	Get(ctx context.Context, class, id string, c stepCall) (*Document, error)
	Query(ctx context.Context, f Filter, c stepCall) ([]*Document, error)
	Count(ctx context.Context, f Filter, c stepCall) (int64, error)
	Due(ctx context.Context, class string, c stepCall) ([]DueDocument, error)
	Preview(ctx context.Context, class, id string, values Values, transition string, c stepCall) (*Preview, error)
	Transitions(ctx context.Context, class, id string, c stepCall) ([]TransitionInfo, error)
	History(ctx context.Context, class, id string, c stepCall) ([]*HistoryRow, error)
	Events(ctx context.Context, class, id string, c stepCall) ([]*Event, error)
	Drain(ctx context.Context, c stepCall) (int, error)
	Outbox(ctx context.Context, f OutboxFilter, c stepCall) ([]QueuedAction, error)
	RetryAction(ctx context.Context, key string, c stepCall) error
	ImportRows(ctx context.Context, table string, rows []Values, c stepCall) (int, error)
	TableRows(ctx context.Context, table string, c stepCall) ([]Values, error)
	TryAction(ctx context.Context, name string, args Values, c stepCall) (*ActionOutcome, error)
	PreviewAction(ctx context.Context, name string, args Values, c stepCall) (*ActionPreview, error)
	Action(name string) (*ActionInfo, bool)
}

// goDoor is the Go API.
type goDoor struct{ s *Store }

func (d goDoor) name() string { return "the Go API" }
func (d goDoor) DefineAction(ctx context.Context, def []byte, c stepCall) error {
	_, err := d.s.DefineAction(ctx, def, c.opts()...)
	return err
}
func (d goDoor) DefineTable(ctx context.Context, def []byte, c stepCall) error {
	_, err := d.s.DefineTable(ctx, def, c.opts()...)
	return err
}
func (d goDoor) DefineClass(ctx context.Context, def []byte, c stepCall) (string, error) {
	cls, err := d.s.DefineClass(ctx, def, c.opts()...)
	if err != nil {
		return "", err
	}
	return cls.Name, nil
}
func (d goDoor) DefineClasses(ctx context.Context, defs [][]byte, c stepCall) (string, error) {
	classes, err := d.s.DefineClasses(ctx, defs, c.opts()...)
	if err != nil {
		return "", err
	}
	return classes[len(classes)-1].Name, nil
}
func (d goDoor) Create(ctx context.Context, class string, values Values, c stepCall) (*Document, error) {
	return d.s.Create(ctx, class, values, c.opts()...)
}
func (d goDoor) Set(ctx context.Context, class, id string, values Values, c stepCall) (*Document, error) {
	return d.s.Set(ctx, class, id, values, c.opts()...)
}
func (d goDoor) Reset(ctx context.Context, class, id string, variables []string, c stepCall) (*Document, error) {
	return d.s.Reset(ctx, class, id, variables, c.opts()...)
}
func (d goDoor) Refresh(ctx context.Context, class, id string, variables []string, c stepCall) (*Document, error) {
	return d.s.Refresh(ctx, class, id, variables, c.opts()...)
}
func (d goDoor) Transition(ctx context.Context, class, id, name string, values Values, c stepCall) (*Document, error) {
	return d.s.Transition(ctx, class, id, name, values, c.opts()...)
}
func (d goDoor) Delete(ctx context.Context, class, id string, c stepCall) error {
	return d.s.Delete(ctx, class, id, c.opts()...)
}
func (d goDoor) Get(ctx context.Context, class, id string, c stepCall) (*Document, error) {
	return d.s.Get(ctx, class, id, c.opts()...)
}
func (d goDoor) Query(ctx context.Context, f Filter, c stepCall) ([]*Document, error) {
	return d.s.Query(ctx, f, c.opts()...)
}
func (d goDoor) Count(ctx context.Context, f Filter, c stepCall) (int64, error) {
	return d.s.Count(ctx, f, c.opts()...)
}
func (d goDoor) Due(ctx context.Context, class string, c stepCall) ([]DueDocument, error) {
	return d.s.Due(ctx, class, c.opts()...)
}
func (d goDoor) Preview(ctx context.Context, class, id string, values Values, transition string, c stepCall) (*Preview, error) {
	return d.s.Preview(ctx, class, id, values, transition, c.opts()...)
}
func (d goDoor) Transitions(ctx context.Context, class, id string, c stepCall) ([]TransitionInfo, error) {
	return d.s.Transitions(ctx, class, id, c.opts()...)
}
func (d goDoor) History(ctx context.Context, class, id string, c stepCall) ([]*HistoryRow, error) {
	return d.s.History(ctx, class, id, c.opts()...)
}
func (d goDoor) Events(ctx context.Context, class, id string, c stepCall) ([]*Event, error) {
	return d.s.Events(ctx, class, id, c.opts()...)
}
func (d goDoor) Drain(ctx context.Context, c stepCall) (int, error) {
	return d.s.Drain(ctx, c.opts()...)
}
func (d goDoor) Outbox(ctx context.Context, f OutboxFilter, c stepCall) ([]QueuedAction, error) {
	return d.s.Outbox(ctx, f, c.opts()...)
}
func (d goDoor) RetryAction(ctx context.Context, key string, c stepCall) error {
	return d.s.RetryAction(ctx, key, c.opts()...)
}
func (d goDoor) ImportRows(ctx context.Context, table string, rows []Values, c stepCall) (int, error) {
	return d.s.ImportRows(ctx, table, rows, c.opts()...)
}
func (d goDoor) TableRows(ctx context.Context, table string, c stepCall) ([]Values, error) {
	return d.s.TableRows(ctx, table, c.opts()...)
}
func (d goDoor) TryAction(ctx context.Context, name string, args Values, c stepCall) (*ActionOutcome, error) {
	return d.s.TryAction(ctx, name, args, c.opts()...)
}
func (d goDoor) PreviewAction(ctx context.Context, name string, args Values, c stepCall) (*ActionPreview, error) {
	return d.s.PreviewAction(ctx, name, args, c.opts()...)
}
func (d goDoor) Action(name string) (*ActionInfo, bool) { return d.s.Action(name) }

// execCommand runs one command of the command set. The package that
// holds the command set imports this one, so a test of this package
// cannot import it: a test file of the directory that is outside the
// package does, and sets this.
var execCommand func(ctx context.Context, s *Store, p Principal, name string, object []byte) ([]byte, error)

// SetExecCommand gives the tests of this package the command set.
func SetExecCommand(exec func(ctx context.Context, s *Store, p Principal, name string, object []byte) ([]byte, error)) {
	execCommand = exec
}

// commandDoor is the command set: every call is a name and one JSON
// object, and its answer is read back from JSON.
type commandDoor struct{ s *Store }

func (d commandDoor) name() string { return "the command set" }

// object is the JSON object of a command: its members, then the options
// of the call.
func object(c stepCall, members ...any) []byte {
	m := map[string]any{}
	for i := 0; i+1 < len(members); i += 2 {
		switch v := members[i+1].(type) {
		case string:
			if v == "" {
				continue
			}
		case Values:
			if v == nil {
				continue
			}
		case []string:
			if v == nil {
				continue
			}
		case int:
			if v == 0 {
				continue
			}
		}
		m[members[i].(string)] = members[i+1]
	}
	if c.key != "" {
		m["key"] = c.key
	}
	if c.reason != "" {
		m["reason"] = c.reason
	}
	if c.at != "" {
		m["at"] = c.at
	}
	if c.as != nil && c.as.For != "" {
		m["for"] = c.as.For
	}
	text, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return text
}

// send runs a command and reads its answer into out. A refusal comes
// back as the error the Go API would have returned: the store's, read
// from the answer and held to the one the command set returned.
func (d commandDoor) send(ctx context.Context, c stepCall, name string, object []byte, out any) error {
	var p Principal
	if c.as != nil {
		p = *c.as
		// For whom the caller acts is said in the command.
		p.For = ""
	}
	answer, err := execCommand(ctx, d.s, p, name, object)
	var got struct {
		Error       *Error            `json:"error"`
		Diagnostics model.Diagnostics `json:"diagnostics"`
	}
	if jerr := json.Unmarshal(answer, &got); jerr != nil {
		return fmt.Errorf("the command %s answered what is not a JSON object: %s", name, answer)
	}
	if err != nil {
		if got.Error == nil {
			return fmt.Errorf("the command %s was refused with %v and its answer has no error: %s", name, err, answer)
		}
		if se, ok := err.(*Error); ok {
			if *se != *got.Error {
				return fmt.Errorf("the command %s was refused with %+v and its answer says %+v", name, se, got.Error)
			}
			return got.Error
		}
		if _, ok := err.(model.Diagnostics); ok {
			return got.Diagnostics
		}
		return err
	}
	if got.Error != nil {
		return fmt.Errorf("the command %s returned no error and answered a refusal: %s", name, answer)
	}
	if out == nil {
		return nil
	}
	if jerr := json.Unmarshal(answer, out); jerr != nil {
		return fmt.Errorf("the answer of %s does not read back: %v: %s", name, jerr, answer)
	}
	return nil
}

// document sends a command that answers a document.
func (d commandDoor) document(ctx context.Context, c stepCall, name string, object []byte) (*Document, error) {
	var out struct {
		Document *Document `json:"document"`
	}
	if err := d.send(ctx, c, name, object, &out); err != nil {
		return nil, err
	}
	if out.Document == nil {
		return nil, fmt.Errorf("the answer of %s has no document", name)
	}
	return out.Document, nil
}

func definition(def []byte) []byte {
	return append(append([]byte(`{"definition":`), def...), '}')
}

func (d commandDoor) DefineAction(ctx context.Context, def []byte, c stepCall) error {
	return d.send(ctx, c, "define_action", definition(def), nil)
}
func (d commandDoor) DefineTable(ctx context.Context, def []byte, c stepCall) error {
	return d.send(ctx, c, "define_table", definition(def), nil)
}
func (d commandDoor) DefineClass(ctx context.Context, def []byte, c stepCall) (string, error) {
	var out struct {
		Class StoredDefinition `json:"class"`
	}
	err := d.send(ctx, c, "define_class", definition(def), &out)
	return out.Class.Name, err
}
func (d commandDoor) DefineClasses(ctx context.Context, defs [][]byte, c stepCall) (string, error) {
	list := []byte(`{"definitions":[`)
	for i, def := range defs {
		if i > 0 {
			list = append(list, ',')
		}
		list = append(list, def...)
	}
	var out struct {
		Classes []StoredDefinition `json:"classes"`
	}
	if err := d.send(ctx, c, "define_classes", append(list, ']', '}'), &out); err != nil {
		return "", err
	}
	return out.Classes[len(out.Classes)-1].Name, nil
}
func (d commandDoor) Create(ctx context.Context, class string, values Values, c stepCall) (*Document, error) {
	return d.document(ctx, c, "create", object(c, "class", class, "values", values, "id", c.id))
}
func (d commandDoor) Set(ctx context.Context, class, id string, values Values, c stepCall) (*Document, error) {
	return d.document(ctx, c, "set", object(c, "class", class, "id", id, "values", values))
}
func (d commandDoor) Reset(ctx context.Context, class, id string, variables []string, c stepCall) (*Document, error) {
	return d.document(ctx, c, "reset", object(c, "class", class, "id", id, "variables", variables))
}
func (d commandDoor) Refresh(ctx context.Context, class, id string, variables []string, c stepCall) (*Document, error) {
	return d.document(ctx, c, "refresh", object(c, "class", class, "id", id, "variables", variables))
}
func (d commandDoor) Transition(ctx context.Context, class, id, name string, values Values, c stepCall) (*Document, error) {
	return d.document(ctx, c, "transition", object(c, "class", class, "id", id, "transition", name, "values", values))
}
func (d commandDoor) Delete(ctx context.Context, class, id string, c stepCall) error {
	return d.send(ctx, c, "delete", object(c, "class", class, "id", id), nil)
}
func (d commandDoor) Get(ctx context.Context, class, id string, c stepCall) (*Document, error) {
	return d.document(ctx, c, "get", object(c, "class", class, "id", id))
}
func (d commandDoor) Query(ctx context.Context, f Filter, c stepCall) ([]*Document, error) {
	var out struct {
		Documents []*Document `json:"documents"`
	}
	err := d.send(ctx, c, "query", object(c, "class", f.Class, "where", f.Where, "order", f.Order, "fields", f.Fields, "limit", f.Limit, "offset", f.Offset), &out)
	return out.Documents, err
}
func (d commandDoor) Count(ctx context.Context, f Filter, c stepCall) (int64, error) {
	var out struct {
		Count int64 `json:"count"`
	}
	err := d.send(ctx, c, "count", object(c, "class", f.Class, "where", f.Where), &out)
	return out.Count, err
}
func (d commandDoor) Due(ctx context.Context, class string, c stepCall) ([]DueDocument, error) {
	var out struct {
		Due []DueDocument `json:"due"`
	}
	err := d.send(ctx, c, "due", object(c, "class", class), &out)
	return out.Due, err
}
func (d commandDoor) Preview(ctx context.Context, class, id string, values Values, transition string, c stepCall) (*Preview, error) {
	var out struct {
		Preview *Preview `json:"preview"`
	}
	if err := d.send(ctx, c, "preview", object(c, "class", class, "id", id, "values", values, "transition", transition), &out); err != nil {
		return nil, err
	}
	return out.Preview, nil
}
func (d commandDoor) Transitions(ctx context.Context, class, id string, c stepCall) ([]TransitionInfo, error) {
	var out struct {
		Transitions []TransitionInfo `json:"transitions"`
	}
	err := d.send(ctx, c, "transitions", object(c, "class", class, "id", id), &out)
	return out.Transitions, err
}
func (d commandDoor) History(ctx context.Context, class, id string, c stepCall) ([]*HistoryRow, error) {
	var out struct {
		Versions []*HistoryRow `json:"versions"`
	}
	err := d.send(ctx, c, "history", object(c, "class", class, "id", id), &out)
	return out.Versions, err
}
func (d commandDoor) Events(ctx context.Context, class, id string, c stepCall) ([]*Event, error) {
	var out struct {
		Events []*Event `json:"events"`
	}
	err := d.send(ctx, c, "events", object(c, "class", class, "id", id), &out)
	return out.Events, err
}
func (d commandDoor) Drain(ctx context.Context, c stepCall) (int, error) {
	var out struct {
		Attempted int `json:"attempted"`
	}
	err := d.send(ctx, c, "drain", object(c), &out)
	return out.Attempted, err
}
func (d commandDoor) Outbox(ctx context.Context, f OutboxFilter, c stepCall) ([]QueuedAction, error) {
	var out struct {
		Actions []QueuedAction `json:"actions"`
	}
	err := d.send(ctx, c, "outbox", object(c, "status", f.Status, "class", f.Class, "id", f.DocID, "limit", f.Limit), &out)
	return out.Actions, err
}
func (d commandDoor) RetryAction(ctx context.Context, key string, c stepCall) error {
	return d.send(ctx, c, "retry_action", object(c, "action_key", key), nil)
}
func (d commandDoor) ImportRows(ctx context.Context, table string, rows []Values, c stepCall) (int, error) {
	var out struct {
		Imported int `json:"imported"`
	}
	if rows == nil {
		rows = []Values{}
	}
	err := d.send(ctx, c, "import_rows", object(c, "table", table, "rows", rows), &out)
	return out.Imported, err
}
func (d commandDoor) TableRows(ctx context.Context, table string, c stepCall) ([]Values, error) {
	var out struct {
		Rows []Values `json:"rows"`
	}
	err := d.send(ctx, c, "table_rows", object(c, "table", table), &out)
	return out.Rows, err
}
func (d commandDoor) TryAction(ctx context.Context, name string, args Values, c stepCall) (*ActionOutcome, error) {
	var out struct {
		Outcome *ActionOutcome `json:"outcome"`
	}
	if err := d.send(ctx, c, "try_action", object(c, "action", name, "args", args), &out); err != nil {
		return nil, err
	}
	return out.Outcome, nil
}
func (d commandDoor) PreviewAction(ctx context.Context, name string, args Values, c stepCall) (*ActionPreview, error) {
	var out struct {
		Preview *ActionPreview `json:"preview"`
	}
	if err := d.send(ctx, c, "preview_action", object(c, "action", name, "args", args), &out); err != nil {
		return nil, err
	}
	return out.Preview, nil
}
func (d commandDoor) Action(name string) (*ActionInfo, bool) {
	var out struct {
		Actions []ActionInfo `json:"actions"`
	}
	if err := d.send(context.Background(), stepCall{}, "list_actions", []byte(`{}`), &out); err != nil {
		return nil, false
	}
	for i := range out.Actions {
		if out.Actions[i].Name == name {
			return &out.Actions[i], true
		}
	}
	return nil, false
}
