package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
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
	Clock      string                     `json:"clock,omitempty"`
	Class      json.RawMessage            `json:"class"`
	Principals map[string]scriptPrincipal `json:"principals,omitempty"`
	Script     []scriptStep               `json:"script"`
}

type scriptPrincipal struct {
	ID     string   `json:"id"`
	Groups []string `json:"groups,omitempty"`
	Admin  bool     `json:"admin,omitempty"`
}

type scriptStep struct {
	Name string `json:"name,omitempty"`
	// Do is create, set, transition, delete, preview, transitions, visits,
	// get, query or count.
	Do string `json:"do"`
	// Doc labels the document a create makes, and names the document
	// another step acts on; the last created document when absent.
	Doc        string       `json:"doc,omitempty"`
	As         string       `json:"as,omitempty"`
	Key        string       `json:"key,omitempty"`
	Transition string       `json:"transition,omitempty"`
	Values     Values       `json:"values,omitempty"`
	Expect     scriptExpect `json:"expect"`
	// At is the moment the step runs at (ADR-0042), as YYYY-MM-DD or
	// YYYY-MM-DD HH:MM:SS in UTC.
	At string `json:"at,omitempty"`
	// Where, Order and Fields are the filter of a query or a count.
	Where  string   `json:"where,omitempty"`
	Order  []string `json:"order,omitempty"`
	Fields []string `json:"fields,omitempty"`
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
	Transitions []TransitionInfo `json:"transitions,omitempty"`
	// Visits are the rows of the _states view: state, entry version, exit
	// version or null.
	Visits [][]any `json:"visits,omitempty"`
	// Absent and Present are variables that must be missing from, or
	// found in, the values of the document, or of every document a query
	// returned (ADR-0041).
	Absent  []string `json:"absent,omitempty"`
	Present []string `json:"present,omitempty"`
	// Docs are the labels of the documents a query returned, in order;
	// Count the count.
	Docs  []string `json:"docs,omitempty"`
	Count *int64   `json:"count,omitempty"`
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
}

type scriptQueued struct {
	Action string `json:"action"`
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
	if suite.Clock != "" {
		at := moment(t, suite.Clock)
		o.Now = func() time.Time { return at }
	}
	s, _ := openStore(t, o)
	ctx := context.Background()
	cls, err := s.DefineClass(ctx, suite.Class)
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
			opts = append(opts, As(Principal{ID: p.ID, Groups: p.Groups, Admin: p.Admin}))
		}
		if step.Key != "" {
			opts = append(opts, Key(step.Key))
		}
		if step.At != "" {
			opts = append(opts, At(moment(t, step.At)))
		}
		id := last
		if step.Do != "create" && step.Doc != "" {
			id = docs[step.Doc]
		}
		var doc *Document
		var pv *Preview
		var infos []TransitionInfo
		var visitsGot [][]any
		var found []*Document
		var count int64
		filter := Filter{Class: cls.Name, Where: step.Where, Order: step.Order, Fields: step.Fields}
		switch step.Do {
		case "get":
			doc, err = s.Get(ctx, cls.Name, id, opts...)
		case "query":
			found, err = s.Query(ctx, filter, opts...)
		case "count":
			count, err = s.Count(ctx, filter, opts...)
		case "create":
			doc, err = s.Create(ctx, cls.Name, step.Values, opts...)
			if err == nil {
				last, id = doc.ID, doc.ID
				if step.Doc != "" {
					docs[step.Doc] = doc.ID
				}
			}
		case "set":
			doc, err = s.Set(ctx, cls.Name, id, step.Values, opts...)
		case "transition":
			doc, err = s.Transition(ctx, cls.Name, id, step.Transition, step.Values, opts...)
		case "delete":
			err = s.Delete(ctx, cls.Name, id, opts...)
		case "preview":
			pv, err = s.Preview(ctx, cls.Name, id, step.Values, step.Transition, opts...)
			if err == nil {
				doc = pv.Document
			}
		case "transitions":
			infos, err = s.Transitions(ctx, cls.Name, id, opts...)
		case "visits":
			visitsGot, err = readVisits(s, cls.Name, id)
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
		if want.Count != nil && count != *want.Count {
			t.Errorf("%s: count %d, want %d", name, count, *want.Count)
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
						if a.Queued {
							queued = append(queued, scriptQueued{Action: a.Action})
						}
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
					t.Errorf("%s: queued %v, want %v", name, queued, want.Queued)
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
			rows, err := s.History(ctx, cls.Name, id)
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
		if want.Events != nil {
			events, err := s.Events(ctx, cls.Name, id)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range events {
				if e.Kind == EventTransition || e.Kind == EventCustom {
					got = append(got, e.Kind+":"+e.Name)
				} else {
					got = append(got, e.Kind)
				}
			}
			if !reflect.DeepEqual(got, want.Events) {
				t.Errorf("%s: events %v, want %v", name, got, want.Events)
			}
		}
		if want.Visits != nil {
			if step.Do != "visits" {
				visitsGot, err = readVisits(s, cls.Name, id)
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

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// sameListing compares two listings on name, target, enabled and code;
// the message is for people.
func sameListing(got, want []TransitionInfo) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Name != w.Name || g.Enabled != w.Enabled || g.Code != w.Code || (w.To != "" && g.To != w.To) {
			return false
		}
	}
	return true
}

// TestStatechartSuitesAreWellFormed keeps the suite files of the script
// format tidy: every step has a command, and the files sort by name.
func TestStatechartSuitesAreWellFormed(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "conformance", "statechart", "*.json"))
	more, _ := filepath.Glob(filepath.Join("..", "..", "conformance", "rules", "*.json"))
	files = append(files, more...)
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
