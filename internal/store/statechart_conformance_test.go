package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The statechart conformance suite (ADR-0040) is the specification of the
// statechart's behaviour: a class, a script of commands with their
// principals, and the expected outcome of each. The rules suite
// (ADR-0043) is the specification of the rules inside the commit, in the
// same script format. The store runs both here; the server and the
// browser VM run them with their phases. The format is in
// conformance/README.md.

type statechartSuite struct {
	Suite       string `json:"suite"`
	Description string `json:"description,omitempty"`
	// Clock is the database's clock while the suite runs, as
	// YYYY-MM-DD HH:MM:SS in UTC; the runner's default when absent.
	Clock string `json:"clock,omitempty"`
	// Tables and Classes are defined before Class, in order: the tables
	// its rules write and the classes they name (ADR-0044).
	Tables  []json.RawMessage `json:"tables,omitempty"`
	Classes []json.RawMessage `json:"classes,omitempty"`
	// Together defines Classes and Class in one call (ADR-0046, item 12),
	// as classes that name each other must be.
	Together bool `json:"together,omitempty"`
	// Actions are the action definitions the rules name (ADR-0045),
	// defined before the classes. The runner implements each with a
	// function that records its calls and does what Functions says for
	// the action's name: by default it runs and returns nothing.
	Actions   []json.RawMessage         `json:"actions,omitempty"`
	Functions map[string]scriptFunction `json:"functions,omitempty"`
	// Outbound allows the suite's store to send the requests of declared
	// actions (ADR-0045, item 7), to a network the runner stands in for:
	// hosts are the hosts a request without a connection may go to, and
	// connections the hosts of each connection. Absent, the store sends
	// nothing. Secrets are the secrets the runner supplies, by connection
	// and name. Replies are what the network answers, each to the next
	// request to its url, or to the next request at all without one; a
	// request no reply is left for is answered 200 with no body.
	Outbound   *scriptOutbound              `json:"outbound,omitempty"`
	Secrets    map[string]map[string]string `json:"secrets,omitempty"`
	Replies    []*fakeReply                 `json:"replies,omitempty"`
	Class      json.RawMessage              `json:"class"`
	Principals map[string]scriptPrincipal   `json:"principals,omitempty"`
	Script     []scriptStep                 `json:"script"`
}

type scriptOutbound struct {
	Hosts       []string            `json:"hosts,omitempty"`
	Connections map[string][]string `json:"connections,omitempty"`
}

// scriptFunction is what the runner's function of an action does when it
// is called: it gets no answer at its first Fails attempts, then refuses
// with the words of Refuses when there are any, and else runs and
// returns Returns.
type scriptFunction struct {
	Fails   int    `json:"fails,omitempty"`
	Refuses string `json:"refuses,omitempty"`
	Returns Values `json:"returns,omitempty"`
}

type scriptPrincipal struct {
	ID     string   `json:"id"`
	Groups []string `json:"groups,omitempty"`
	// System lists the system groups the principal is in (ADR-0052).
	System []string `json:"system,omitempty"`
	// Kind is what the principal is and For whom it acts for (ADR-0050).
	Kind string `json:"kind,omitempty"`
	For  string `json:"for,omitempty"`
}

type scriptStep struct {
	Name string `json:"name,omitempty"`
	// Do is create, set, reset, refresh, transition, delete, preview,
	// transitions, visits, get, query, count, drain, retry, import, rows,
	// wait, actions, try or preview_action.
	Do string `json:"do"`
	// Doc labels the document a create makes, and names the document
	// another step acts on; the last created document when absent. ID is
	// the id a create gives its document, and the id of a document the
	// script did not create: one an action made.
	Doc        string `json:"doc,omitempty"`
	ID         string `json:"id,omitempty"`
	As         string `json:"as,omitempty"`
	Key        string `json:"key,omitempty"`
	Transition string `json:"transition,omitempty"`
	Values     Values `json:"values,omitempty"`
	// Variables are the variables a reset or a refresh names.
	Variables []string     `json:"variables,omitempty"`
	Expect    scriptExpect `json:"expect"`
	// At is the moment the step runs at (ADR-0042), as YYYY-MM-DD or
	// YYYY-MM-DD HH:MM:SS in UTC.
	At string `json:"at,omitempty"`
	// Where, Order and Fields are the filter of a query or a count, and
	// Class the class a step acts on when that is not the suite's own.
	Where  string   `json:"where,omitempty"`
	Order  []string `json:"order,omitempty"`
	Fields []string `json:"fields,omitempty"`
	Class  string   `json:"class,omitempty"`
	// Table is the table of an import or of a rows step, and Rows the rows
	// an import writes.
	Table string   `json:"table,omitempty"`
	Rows  []Values `json:"rows,omitempty"`
	// Row is, for a retry, the position of the action among the ones the
	// document queued.
	Row int `json:"row,omitempty"`
	// Seconds is how far a wait moves the database's clock on.
	Seconds int `json:"seconds,omitempty"`
	// Action and Args are the action a try or a preview_action names and
	// the values it gives it.
	Action string `json:"action,omitempty"`
	Args   Values `json:"args,omitempty"`
}

type scriptExpect struct {
	// Code is the refusal's code; the other fields are then not checked,
	// but Action, the path of the rule's action that refused the command.
	Code    string `json:"code,omitempty"`
	Action  string `json:"action,omitempty"`
	State   string `json:"state,omitempty"`
	Version int64  `json:"version,omitempty"`
	// Path is the preview's path; History the states of every history
	// row so far; Events the kind of every event so far, a transition
	// written kind:name.
	Path    []string `json:"path,omitempty"`
	History []string `json:"history,omitempty"`
	Events  []string `json:"events,omitempty"`
	// Values are checked against the document's, by name, in the wire
	// shape; absent names are not checked.
	Values map[string]json.RawMessage `json:"values,omitempty"`
	// Transitions is the listing, in order.
	Transitions []scriptTransition `json:"transitions,omitempty"`
	// Visits are the rows of the _states view: state, entry version, exit
	// version or null.
	Visits [][]any `json:"visits,omitempty"`
	// Stays are the rows of the _states view again, for who and how
	// (ADR-0047, item 3): state, then for the entry that began the stay
	// who made it, the state the document came from, the transition and
	// the origin, then for the move that ended it who, the transition and
	// the origin; null where there is none. Origins are the origins of
	// the events that enter a state, created and transition, in order.
	// Life is what the document keeps of its life, by name: created_by,
	// updated_by, state_entered_by, state_entered_from; "" for none.
	Stays   [][]any           `json:"stays,omitempty"`
	Origins []string          `json:"origins,omitempty"`
	Life    map[string]string `json:"life,omitempty"`
	// Actors are the actors of the document's events so far, in order
	// (ADR-0050): each the actor's id, then what it was and for whom it
	// acted where the host program said, as "agent-7 agent for ann".
	Actors []string `json:"actors,omitempty"`
	// Absent and Present are variables that must be missing from, or
	// found in, the values of the document, or of every document a query
	// returned (ADR-0041).
	Absent  []string `json:"absent,omitempty"`
	Present []string `json:"present,omitempty"`
	// Docs are the labels of the documents a query returned, in order;
	// Count the count.
	Docs  []string `json:"docs,omitempty"`
	Count *int64   `json:"count,omitempty"`
	// Due is what a due step lists: for each document its label in the
	// script, its state and the state it would move to, and for one a
	// timed transition would move the moment of its timer, in the
	// notation of At with its time.
	Due [][]string `json:"due,omitempty"`
	// Now is the moment a preview reports, in the notation of At.
	Now string `json:"now,omitempty"`
	// Rules are the paths of the rules that ran in a preview, in order
	// (ADR-0043); Skipped the paths of the rules whose trigger was raised
	// and that did not run; Queued the actions for the outbox, by name;
	// Errors what the preview recorded and went on from, each by code and,
	// where given, by variable and by the path of the action.
	Rules   []string       `json:"rules,omitempty"`
	Skipped []string       `json:"skipped,omitempty"`
	Queued  []scriptQueued `json:"queued,omitempty"`
	Errors  []scriptError  `json:"errors,omitempty"`
	// Computed are the moments of triggered variables, by name (ADR-0043):
	// the version whose commit computed the value, the error of a
	// recomputation that failed since, and the time in the notation of
	// At where given; null for a variable that has no moment. They are
	// checked on the document, or on every document a query returned.
	Computed map[string]*scriptMoment `json:"computed,omitempty"`
	// Drained is how many actions a drain attempted. Outbox are the
	// actions the document queued, in order, each by the fields given.
	// Found are the documents a query returned, in order, each by its
	// state and a subset of its values.
	Drained *int           `json:"drained,omitempty"`
	Outbox  []scriptAction `json:"outbox,omitempty"`
	Found   []scriptFound  `json:"found,omitempty"`
	// Rows are the rows of the step's table, in key order.
	Rows json.RawMessage `json:"rows,omitempty"`
	// Calls are the calls the functions of the suite's actions received
	// during the step, in order, each by the fields given: key, attempt,
	// action, args and event, and of the event and its document any part
	// (ADR-0045, item 4). An empty list says nothing was called.
	Calls json.RawMessage `json:"calls,omitempty"`
	// Outcome is what a try answered and Call what a preview_action would
	// call, by the fields given. Catalogue are entries of the listing of
	// actions, each found by its name and compared by the fields given.
	Outcome   json.RawMessage `json:"outcome,omitempty"`
	Call      json.RawMessage `json:"call,omitempty"`
	Catalogue json.RawMessage `json:"catalogue,omitempty"`
	// Requests are the requests the store sent during the step, in order,
	// each by the fields given: method, url, connection, headers by name,
	// and body, the JSON sent, or text for a body that is none. Request is
	// the request a preview_action would send, in the same shape.
	Requests json.RawMessage `json:"requests,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
}

// scriptTransition is one entry of a listing as a suite expects it: the
// store's, and Fires, the moment a timed transition fires, in the
// notation of At with its time; absent for a transition that is not
// timed or whose moment never comes.
type scriptTransition struct {
	TransitionInfo
	Fires string `json:"fires,omitempty"`
}

// stamp writes a moment in the notation of At, with its time.
func stamp(secs int64) string {
	return time.Unix(secs, 0).UTC().Format("2006-01-02 15:04:05")
}

type scriptAction struct {
	Action   string          `json:"action"`
	Status   string          `json:"status"`
	Code     string          `json:"code,omitempty"`
	Attempts *int            `json:"attempts,omitempty"`
	Depth    *int            `json:"depth,omitempty"`
	As       string          `json:"as,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	// IntoCode is why the action's result was not written back to the
	// document (ADR-0045, item 8), checked when given: "" says it was
	// stored, or that the action has no into.
	IntoCode *string `json:"into_code,omitempty"`
	// Due is when the row may next run, in the notation of At with its
	// time, checked when given: the moment of a wake-up (ADR-0047).
	Due string `json:"due,omitempty"`
}

type scriptFound struct {
	State  string                     `json:"state,omitempty"`
	Values map[string]json.RawMessage `json:"values,omitempty"`
}

type scriptMoment struct {
	Version int64  `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
	At      string `json:"at,omitempty"`
}

type scriptQueued struct {
	Action string `json:"action"`
	// Args are the arguments as the preview evaluated them, checked when
	// given; Into the variables the action's result will be written to,
	// checked when given.
	Args json.RawMessage `json:"args,omitempty"`
	Into []string        `json:"into,omitempty"`
}

type scriptError struct {
	Code     string `json:"code"`
	Variable string `json:"variable,omitempty"`
	Action   string `json:"action,omitempty"`
}

// moment reads a step's or a suite's instant.
func moment(t *testing.T, s string) time.Time {
	t.Helper()
	secs, err := formula.ParseCaseDate(s)
	if err != nil {
		t.Fatalf("%q: want YYYY-MM-DD or YYYY-MM-DD HH:MM:SS, %v", s, err)
	}
	return time.Unix(secs, 0).UTC()
}

func TestStatechartConformance(t *testing.T) { runScriptSuites(t, "statechart") }

// TestRulesConformance runs conformance/rules, the specification of the
// rules inside the commit (ADR-0043).
func TestRulesConformance(t *testing.T) { runScriptSuites(t, "rules") }

// TestAccessConformance runs conformance/access, the specification of the
// four rules of a class on every path (ADR-0049).
func TestAccessConformance(t *testing.T) { runScriptSuites(t, "access") }

func runScriptSuites(t *testing.T, dir string) {
	files, err := filepath.Glob(filepath.Join("..", "..", "conformance", dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no %s suites found", dir)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var suite statechartSuite
		if err := json.Unmarshal(raw, &suite); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		t.Run(suite.Suite, func(t *testing.T) { runStatechartSuite(t, &suite) })
	}
}

func runStatechartSuite(t *testing.T, suite *statechartSuite) {
	var o Options
	var now time.Time
	if suite.Clock != "" {
		now = moment(t, suite.Clock)
		o.Now = func() time.Time { return now }
	}
	// The functions of the suite's actions: each records the call it
	// received, as its JSON, and does what the suite says.
	var mu sync.Mutex
	var calls []json.RawMessage
	o.Actions = map[string]ActionFunc{}
	for i, def := range suite.Actions {
		var head struct {
			Name    string          `json:"name"`
			Request json.RawMessage `json:"request"`
		}
		if err := json.Unmarshal(def, &head); err != nil || head.Name == "" {
			t.Fatalf("action %d of the suite's actions has no name: %v", i, err)
		}
		if head.Request != nil {
			// A declared action is sent, not called.
			continue
		}
		does := suite.Functions[head.Name]
		o.Actions[head.Name] = func(_ context.Context, call *ActionCall) (Values, error) {
			mu.Lock()
			calls = append(calls, mustJSON(call))
			mu.Unlock()
			switch {
			case call.Attempt <= does.Fails:
				return nil, fmt.Errorf("no answer at attempt %d", call.Attempt)
			case does.Refuses != "":
				return nil, Permanent(fmt.Errorf("%s", does.Refuses))
			}
			return does.Returns, nil
		}
	}
	var network *fakeOutbound
	if suite.Outbound != nil {
		network = &fakeOutbound{hosts: suite.Outbound.Hosts, connections: suite.Outbound.Connections, replies: suite.Replies}
		o.Outbound = network
	}
	if suite.Secrets != nil {
		o.Secrets = func(_ context.Context, connection, name string) (string, error) {
			if value, ok := suite.Secrets[connection][name]; ok {
				return value, nil
			}
			return "", fmt.Errorf("the suite has no secret %s of %s", name, connection)
		}
	}
	s, _ := openStore(t, o)
	ctx := context.Background()
	for i, def := range suite.Actions {
		if _, err := s.DefineAction(ctx, def, asSetup); err != nil {
			t.Fatalf("action %d of the suite's actions: %v", i, err)
		}
	}
	for i, table := range suite.Tables {
		if _, err := s.DefineTable(ctx, table, asSetup); err != nil {
			t.Fatalf("table %d of the suite's tables: %v", i, err)
		}
	}
	var cls *model.Class
	var err error
	if suite.Together {
		set := make([][]byte, 0, len(suite.Classes)+1)
		for _, other := range suite.Classes {
			set = append(set, other)
		}
		defined, derr := s.DefineClasses(ctx, append(set, suite.Class), asSetup)
		if derr != nil {
			t.Fatalf("the classes, together: %v", derr)
		}
		cls = defined[len(defined)-1]
	} else {
		for i, other := range suite.Classes {
			if _, err := s.DefineClass(ctx, other, asSetup); err != nil {
				t.Fatalf("class %d of the suite's classes: %v", i, err)
			}
		}
		cls, err = s.DefineClass(ctx, suite.Class, asSetup)
	}
	if err != nil {
		t.Fatalf("the class: %v", err)
	}
	docs := map[string]string{}
	last := ""
	for i, step := range suite.Script {
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step %d: %s %s", i, step.Do, step.Transition)
		}
		var opts []Option
		if step.As != "" {
			p, ok := suite.Principals[step.As]
			if !ok {
				t.Fatalf("%s: unknown principal %s", name, step.As)
			}
			opts = append(opts, As(Principal{ID: p.ID, Groups: p.Groups, System: p.System, Kind: p.Kind, For: p.For}))
		}
		if step.Key != "" {
			opts = append(opts, Key(step.Key))
		}
		if step.At != "" {
			opts = append(opts, At(moment(t, step.At)))
		}
		id := last
		switch {
		case step.Do == "create":
			if step.ID != "" {
				opts = append(opts, WithID(step.ID))
			}
		case step.ID != "":
			id = step.ID
		case step.Doc != "":
			id = docs[step.Doc]
		}
		// The class a step acts on, the suite's own unless it names one.
		class := cls.Name
		if step.Class != "" {
			class = step.Class
		}
		var doc *Document
		var pv *Preview
		var infos []TransitionInfo
		var visitsGot [][]any
		var found []*Document
		var due []DueDocument
		var count int64
		drained := -1
		filter := Filter{Class: class, Where: step.Where, Order: step.Order, Fields: step.Fields}
		var tableRows []Values
		var outcome *ActionOutcome
		var called *ActionPreview
		mu.Lock()
		calls = []json.RawMessage{}
		mu.Unlock()
		if network != nil {
			network.take()
		}
		switch step.Do {
		case "wait":
			if suite.Clock == "" {
				t.Fatalf("%s: a wait moves the suite's clock, and the suite has none", name)
			}
			now = now.Add(time.Duration(step.Seconds) * time.Second)
		case "actions":
		case "try":
			outcome, err = s.TryAction(ctx, step.Action, step.Args, asSetup)
		case "preview_action":
			called, err = s.PreviewAction(ctx, step.Action, step.Args, asSetup)
		case "get":
			doc, err = s.Get(ctx, class, id, opts...)
		case "query":
			found, err = s.Query(ctx, filter, opts...)
		case "count":
			count, err = s.Count(ctx, filter, opts...)
		case "due":
			due, err = s.Due(ctx, class, opts...)
		case "create":
			doc, err = s.Create(ctx, class, step.Values, opts...)
			if err == nil {
				last, id = doc.ID, doc.ID
				if step.Doc != "" {
					docs[step.Doc] = doc.ID
				}
			}
		case "set":
			doc, err = s.Set(ctx, class, id, step.Values, opts...)
		case "reset":
			doc, err = s.Reset(ctx, class, id, step.Variables, opts...)
		case "refresh":
			doc, err = s.Refresh(ctx, class, id, step.Variables, opts...)
		case "transition":
			doc, err = s.Transition(ctx, class, id, step.Transition, step.Values, opts...)
		case "delete":
			err = s.Delete(ctx, class, id, opts...)
		case "drain":
			drained, err = s.Drain(ctx, asSetup)
		case "retry":
			var rows []QueuedAction
			if rows, err = s.Outbox(ctx, OutboxFilter{Class: class, DocID: id}, asSetup); err == nil {
				if step.Row >= len(rows) {
					t.Fatalf("%s: the document queued %d actions; no row %d", name, len(rows), step.Row)
				}
				err = s.RetryAction(ctx, rows[step.Row].Key, asSetup)
			}
		case "import":
			_, err = s.ImportRows(ctx, step.Table, step.Rows)
		case "rows":
			tableRows, err = s.TableRows(ctx, step.Table)
		case "preview":
			pv, err = s.Preview(ctx, class, id, step.Values, step.Transition, opts...)
			if err == nil {
				doc = pv.Document
			}
		case "transitions":
			infos, err = s.Transitions(ctx, class, id, opts...)
		case "visits":
			visitsGot, err = readVisits(s, class, id)
		default:
			t.Fatalf("%s: unknown command %q", name, step.Do)
		}
		want := step.Expect
		if want.Code != "" {
			if code(err) != want.Code {
				t.Errorf("%s: want the refusal %s, got %v", name, want.Code, err)
			} else if se, _ := err.(*Error); want.Action != "" && (se == nil || se.Path != want.Action) {
				t.Errorf("%s: want the refusal in %s, got %v", name, want.Action, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if doc != nil {
			if want.State != "" && doc.State != want.State {
				t.Errorf("%s: state %s, want %s", name, doc.State, want.State)
			}
			if want.Version != 0 && doc.Version != want.Version {
				t.Errorf("%s: version %d, want %d", name, doc.Version, want.Version)
			}
			for field, w := range want.Life {
				got, known := map[string]string{"created_by": doc.CreatedBy, "updated_by": doc.UpdatedBy,
					"state_entered_by": doc.StateEnteredBy, "state_entered_from": doc.StateEnteredFrom}[field]
				if !known || got != w {
					t.Errorf("%s: %s is %q, want %q", name, field, got, w)
				}
			}
			for vname, raw := range want.Values {
				if !sameWire(doc.Values[vname], raw) {
					t.Errorf("%s: %s = %s, want %s", name, vname, dump(doc.Values[vname]), raw)
				}
			}
		}
		for _, d := range append(found, doc) {
			if d == nil {
				continue
			}
			for _, v := range want.Absent {
				if _, ok := d.Values[v]; ok {
					t.Errorf("%s: %s is present in %s, want it absent", name, v, d.ID)
				}
				if _, ok := d.Computed[v]; ok {
					t.Errorf("%s: the moment of %s is present in %s, want it absent with the variable", name, v, d.ID)
				}
			}
			for v, m := range want.Computed {
				got, ok := d.Computed[v]
				switch {
				case m == nil:
					if ok {
						t.Errorf("%s: %s has the moment %+v, want none", name, v, got)
					}
				case !ok:
					t.Errorf("%s: %s has no moment, want %+v", name, v, *m)
				case got.Version != m.Version || got.Error != m.Error:
					t.Errorf("%s: the moment of %s is %+v, want %+v", name, v, got, *m)
				case m.At != "" && got.At != moment(t, m.At).Unix():
					t.Errorf("%s: %s was computed at %d, want %s", name, v, got.At, m.At)
				}
			}
			for _, v := range want.Present {
				if _, ok := d.Values[v]; !ok {
					t.Errorf("%s: %s is absent from %s, want it present", name, v, d.ID)
				}
			}
		}
		if want.Docs != nil {
			labels := map[string]string{}
			for label, docID := range docs {
				labels[docID] = label
			}
			got := []string{}
			for _, d := range found {
				got = append(got, labels[d.ID])
			}
			if !reflect.DeepEqual(got, want.Docs) {
				t.Errorf("%s: documents %v, want %v", name, got, want.Docs)
			}
		}
		if want.Due != nil {
			labels := map[string]string{}
			for label, docID := range docs {
				labels[docID] = label
			}
			got := [][]string{}
			for _, d := range due {
				row := []string{labels[d.ID], d.State, d.To}
				if d.Since != 0 {
					row = append(row, stamp(d.Since))
				}
				got = append(got, row)
			}
			if !reflect.DeepEqual(got, want.Due) {
				t.Errorf("%s: due %v, want %v", name, got, want.Due)
			}
		}
		if want.Count != nil && count != *want.Count {
			t.Errorf("%s: count %d, want %d", name, count, *want.Count)
		}
		if want.Drained != nil && drained != *want.Drained {
			t.Errorf("%s: drained %d actions, want %d", name, drained, *want.Drained)
		}
		if want.Found != nil {
			if len(found) != len(want.Found) {
				t.Errorf("%s: found %d documents, want %d", name, len(found), len(want.Found))
			} else {
				for i, w := range want.Found {
					if w.State != "" && found[i].State != w.State {
						t.Errorf("%s: document %d is in %s, want %s", name, i, found[i].State, w.State)
					}
					for vname, raw := range w.Values {
						if !sameWire(found[i].Values[vname], raw) {
							t.Errorf("%s: document %d has %s = %s, want %s", name, i, vname, dump(found[i].Values[vname]), raw)
						}
					}
				}
			}
		}
		if want.Calls != nil && !subsetOf(want.Calls, mustJSON(calls)) {
			t.Errorf("%s: the calls were %s, want %s", name, dump(calls), want.Calls)
		}
		if want.Outcome != nil && !subsetOf(want.Outcome, mustJSON(outcome)) {
			t.Errorf("%s: the trial answered %s, want %s", name, dump(outcome), want.Outcome)
		}
		if want.Requests != nil {
			if network == nil {
				t.Fatalf("%s: requests are expected of a suite with outbound", name)
			}
			if sent := requestsJSON(network.take()); !subsetOf(want.Requests, sent) {
				t.Errorf("%s: the requests sent were %s, want %s", name, sent, want.Requests)
			}
		}
		if want.Request != nil {
			var got json.RawMessage
			if called != nil && called.Request != nil {
				r := called.Request
				got = requestJSON(r.Method, r.URL, r.Connection, r.Headers, []byte(r.Body))
			}
			if got == nil || !subsetOf(want.Request, got) {
				t.Errorf("%s: the action would send %s, want %s", name, got, want.Request)
			}
		}
		if want.Call != nil && (called == nil || !subsetOf(want.Call, mustJSON(called.Call))) {
			t.Errorf("%s: the action would be called with %s, want %s", name, dump(called), want.Call)
		}
		if want.Catalogue != nil {
			var entries []json.RawMessage
			if err := json.Unmarshal(want.Catalogue, &entries); err != nil {
				t.Fatalf("%s: the catalogue is a list of entries: %v", name, err)
			}
			for _, entry := range entries {
				var head struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(entry, &head)
				info, ok := s.Action(head.Name)
				if !ok || !subsetOf(entry, mustJSON(info)) {
					t.Errorf("%s: the catalogue has %s, want %s", name, dump(info), entry)
				}
			}
		}
		if want.Rows != nil && !sameWire(tableRows, want.Rows) {
			t.Errorf("%s: the rows of %s are %s, want %s", name, step.Table, dump(tableRows), want.Rows)
		}
		if want.Outbox != nil {
			rows, err := s.Outbox(ctx, OutboxFilter{Class: class, DocID: id}, asSetup)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(want.Outbox) {
				t.Errorf("%s: the document queued %s, want %d actions", name, dump(rows), len(want.Outbox))
			} else {
				for i, w := range want.Outbox {
					g := rows[i]
					if g.Action != w.Action || g.Status != w.Status || g.ErrorCode != w.Code ||
						w.Attempts != nil && g.Attempts != *w.Attempts || w.Depth != nil && g.Depth != *w.Depth ||
						w.As != "" && g.PrincipalID != suite.Principals[w.As].ID || w.Args != nil && !sameWire(g.Args, w.Args) ||
						w.IntoCode != nil && g.IntoCode != *w.IntoCode || w.Due != "" && stamp(g.DueAt) != w.Due {
						t.Errorf("%s: action %d is %s, want %s", name, i, dump(g), dump(w))
					}
				}
			}
		}
		if want.Now != "" {
			if pv == nil {
				t.Errorf("%s: a moment is expected of a preview only", name)
			} else if at := moment(t, want.Now).Unix(); pv.Now != at {
				t.Errorf("%s: now %d, want %d", name, pv.Now, at)
			}
		}
		if want.Rules != nil || want.Skipped != nil || want.Queued != nil || want.Errors != nil {
			if pv == nil {
				t.Errorf("%s: rules, queued actions and errors are expected of a preview only", name)
			} else {
				ran, skipped, queued, errs := []string{}, []string{}, []scriptQueued{}, []scriptError{}
				for _, r := range pv.Rules {
					if !r.Ran {
						skipped = append(skipped, r.Path)
						continue
					}
					ran = append(ran, r.Path)
					for _, a := range r.Actions {
						if !a.Queued {
							continue
						}
						got := scriptQueued{Action: a.Action}
						// The arguments are compared where the suite gives them.
						if n := len(queued); n < len(want.Queued) && want.Queued[n].Args != nil {
							got.Args = want.Queued[n].Args
							if !sameWire(a.Args, want.Queued[n].Args) {
								got.Args = mustJSON(a.Args)
							}
						}
						if n := len(queued); n < len(want.Queued) && want.Queued[n].Into != nil {
							got.Into = a.Into
						}
						queued = append(queued, got)
					}
				}
				for i, e := range pv.Errors {
					got := scriptError{Code: e.Code}
					if i < len(want.Errors) && want.Errors[i].Variable != "" {
						got.Variable = e.Variable
					}
					if i < len(want.Errors) && want.Errors[i].Action != "" {
						got.Action = e.Path
					}
					errs = append(errs, got)
				}
				if want.Rules != nil && !reflect.DeepEqual(ran, want.Rules) {
					t.Errorf("%s: the rules that ran %v, want %v", name, ran, want.Rules)
				}
				if want.Skipped != nil && !reflect.DeepEqual(skipped, want.Skipped) {
					t.Errorf("%s: the rules that did not run %v, want %v", name, skipped, want.Skipped)
				}
				if want.Queued != nil && !reflect.DeepEqual(queued, want.Queued) {
					t.Errorf("%s: queued %s, want %s", name, dump(queued), dump(want.Queued))
				}
				if want.Errors != nil && !reflect.DeepEqual(errs, want.Errors) {
					t.Errorf("%s: errors %v, want %v", name, dump(pv.Errors), dump(want.Errors))
				}
			}
		}
		if want.Path != nil {
			if pv == nil {
				t.Errorf("%s: a path is expected of a preview only", name)
			} else if !reflect.DeepEqual(pv.Path, want.Path) {
				t.Errorf("%s: path %v, want %v", name, pv.Path, want.Path)
			}
		}
		if want.Transitions != nil {
			got := infos
			if pv != nil {
				got = pv.Transitions
			}
			if !sameListing(got, want.Transitions) {
				t.Errorf("%s: transitions %s, want %s", name, dump(got), dump(want.Transitions))
			}
		}
		if want.History != nil {
			rows, err := s.History(ctx, class, id)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r.State)
			}
			if !reflect.DeepEqual(got, want.History) {
				t.Errorf("%s: history %v, want %v", name, got, want.History)
			}
		}
		if want.Actors != nil {
			events, err := s.Events(ctx, class, id, As(root))
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			got := []string{}
			for _, e := range events {
				who := e.ActorID
				if e.ActorKind != "" {
					who += " " + e.ActorKind
				}
				if e.ActorFor != "" {
					who += " for " + e.ActorFor
				}
				got = append(got, who)
			}
			if !reflect.DeepEqual(got, want.Actors) {
				t.Errorf("%s: actors %v, want %v", name, got, want.Actors)
			}
		}
		if want.Events != nil {
			events, err := s.Events(ctx, class, id)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range events {
				if e.Kind == EventTransition || e.Kind == EventCustom || e.Kind == EventActionRun || e.Kind == EventActionFailed {
					got = append(got, e.Kind+":"+e.Name)
				} else {
					got = append(got, e.Kind)
				}
			}
			if !reflect.DeepEqual(got, want.Events) {
				t.Errorf("%s: events %v, want %v", name, got, want.Events)
			}
		}
		if want.Origins != nil {
			events, err := s.Events(ctx, class, id)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, e := range events {
				if e.Kind == EventCreated || e.Kind == EventTransition {
					got = append(got, e.Origin)
				}
			}
			if !reflect.DeepEqual(got, want.Origins) {
				t.Errorf("%s: origins %v, want %v", name, got, want.Origins)
			}
		}
		if want.Stays != nil {
			got, err := readStays(s, class, id)
			if err != nil {
				t.Fatal(err)
			}
			if !sameWire(got, mustJSON(want.Stays)) {
				t.Errorf("%s: stays %s, want %s", name, dump(got), dump(want.Stays))
			}
		}
		if want.Visits != nil {
			if step.Do != "visits" {
				visitsGot, err = readVisits(s, class, id)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !sameWire(visitsGot, mustJSON(want.Visits)) {
				t.Errorf("%s: visits %s, want %s", name, dump(visitsGot), dump(want.Visits))
			}
		}
	}
}

// readVisits reads the _states view of a document: state, entry version
// and exit version, the last null while the visit is current.
func readVisits(s *Store, class, id string) ([][]any, error) {
	rows, err := s.db.Query(`SELECT state, entry_version, exit_version FROM `+quote(class+"_states")+` WHERE id = ? ORDER BY entry_version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]any{}
	for rows.Next() {
		var state string
		var entry int64
		var exit *int64
		if err := rows.Scan(&state, &entry, &exit); err != nil {
			return nil, err
		}
		var x any
		if exit != nil {
			x = *exit
		}
		out = append(out, []any{state, entry, x})
	}
	return out, rows.Err()
}

// readStays reads the _states view of a document for who and how: state,
// entered_by, entered_from, entered_via, entered_how, left_by, left_via
// and left_how, null where there is none.
func readStays(s *Store, class, id string) ([][]any, error) {
	rows, err := s.db.Query(`SELECT state, entered_by, entered_from, entered_via, entered_how, left_by, left_via, left_how FROM `+quote(class+"_states")+` WHERE id = ? ORDER BY entry_version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]any{}
	for rows.Next() {
		cols := make([]sql.NullString, 8)
		dest := make([]any, 8)
		for i := range cols {
			dest[i] = &cols[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make([]any, 8)
		for i, c := range cols {
			if c.Valid {
				row[i] = c.String
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// sameWire compares a Go value with a JSON value through their JSON text.
func sameWire(got any, want json.RawMessage) bool {
	var g, w any
	gb, err := json.Marshal(got)
	if err != nil {
		return false
	}
	if json.Unmarshal(gb, &g) != nil || json.Unmarshal(want, &w) != nil {
		return false
	}
	return reflect.DeepEqual(g, w)
}

// subsetOf reports whether got has everything want gives: an object every
// property of want's, compared the same way, a list the same number of
// elements, each compared the same way, and anything else an equal value.
func subsetOf(want, got json.RawMessage) bool {
	var w, g any
	if json.Unmarshal(want, &w) != nil || json.Unmarshal(got, &g) != nil {
		return false
	}
	var within func(w, g any) bool
	within = func(w, g any) bool {
		switch w := w.(type) {
		case map[string]any:
			g, ok := g.(map[string]any)
			if !ok {
				return false
			}
			for k, v := range w {
				gv, has := g[k]
				if !has && v != nil || !within(v, gv) {
					return false
				}
			}
			return true
		case []any:
			g, ok := g.([]any)
			if !ok || len(g) != len(w) {
				return false
			}
			for i := range w {
				if !within(w[i], g[i]) {
					return false
				}
			}
			return true
		}
		return reflect.DeepEqual(w, g)
	}
	return within(w, g)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// sameListing compares two listings on name, target, enabled, code and
// the moment a timed transition fires; the message is for people.
func sameListing(got []TransitionInfo, want []scriptTransition) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Name != w.Name || g.Enabled != w.Enabled || g.Code != w.Code || (w.To != "" && g.To != w.To) {
			return false
		}
		fires := ""
		if g.FiresAt != 0 {
			fires = stamp(g.FiresAt)
		}
		if fires != w.Fires {
			return false
		}
	}
	return true
}

// TestStatechartSuitesAreWellFormed keeps the suite files of the script
// format tidy: every step has a command, and the files sort by name.
func TestStatechartSuitesAreWellFormed(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "conformance", "statechart", "*.json"))
	for _, dir := range []string{"rules", "access"} {
		more, _ := filepath.Glob(filepath.Join("..", "..", "conformance", dir, "*.json"))
		files = append(files, more...)
	}
	sort.Strings(files)
	for _, file := range files {
		raw, _ := os.ReadFile(file)
		var suite statechartSuite
		if err := json.Unmarshal(raw, &suite); err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if suite.Suite == "" || !strings.HasSuffix(file, suite.Suite+".json") {
			t.Errorf("%s: the suite is named %q; the file is named after the suite", file, suite.Suite)
		}
		for i, step := range suite.Script {
			if step.Do == "" {
				t.Errorf("%s: step %d has no command", file, i)
			}
		}
	}
}
