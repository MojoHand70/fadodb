package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The people of the tests. Olga runs the database and is in every system
// group; Dan designs; Ann is in finance; Bob is in sales.
var (
	olga = store.Principal{ID: "olga", Kind: store.ActorPerson, System: []string{store.GroupAdministrators, store.GroupDesigners, store.GroupContentAdministrators, store.GroupAnalysts}}
	dan  = store.Principal{ID: "dan", Kind: store.ActorPerson, System: []string{store.GroupDesigners}}
	ann  = store.Principal{ID: "ann", Kind: store.ActorPerson, Groups: []string{"finance"}}
	bob  = store.Principal{ID: "bob", Kind: store.ActorPerson, Groups: []string{"sales"}}
)

const invoiceClass = `{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "net":    {"type": "money", "scale": 2},
    "note":   {"type": "text"},
    "total":  {"formula": "net"}
  },
  "states": {"initial": "draft", "list": ["draft", "approved", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "approve", "from": "draft", "to": "approved"},
    {"name": "pay", "from": "approved", "to": "paid"}
  ],
  "access": {
    "read": "TRUE", "create": "INGROUP(\"sales\")", "update": "INGROUP(\"sales\")", "delete": "INGROUP(\"sales\")",
    "transition": {"approve": "INGROUP(\"finance\")", "pay": "INGROUP(\"finance\")"}
  }
}`

// clock is the database's clock in the tests: a fixed moment.
var clock = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

func open(t testing.TB) *store.Store {
	t.Helper()
	o := store.Options{Now: func() time.Time { return clock }}
	o.Outbox.Manual = true
	o.Actions = map[string]store.ActionFunc{
		"books.post": func(context.Context, *store.ActionCall) (store.Values, error) {
			return store.Values{"entry": "E-1"}, nil
		},
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "test.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// used counts the commands the tests of this package ran, so that one
// can say none was left out.
var used = map[string]int{}

// run sends a command that must run, and answers its answer.
func run(t testing.TB, s *store.Store, p store.Principal, name, object string) map[string]any {
	t.Helper()
	used[name]++
	answer, err := Exec(context.Background(), s, p, name, []byte(object))
	if err != nil {
		t.Fatalf("%s %s: %v", name, object, err)
	}
	var out map[string]any
	if err := json.Unmarshal(answer, &out); err != nil {
		t.Fatalf("%s answered what is not a JSON object: %s", name, answer)
	}
	if _, refused := out["error"]; refused {
		t.Fatalf("%s returned no error and answered a refusal: %s", name, answer)
	}
	return out
}

// refused sends a command that must be refused, and answers the refusal
// as the answer carries it, having held it to the error Exec returned.
func refused(t testing.TB, s *store.Store, p store.Principal, name, object string) *store.Error {
	t.Helper()
	answer, err := Exec(context.Background(), s, p, name, []byte(object))
	if err == nil {
		t.Fatalf("%s %s: want a refusal, got %s", name, object, answer)
	}
	var out struct {
		Error *store.Error `json:"error"`
	}
	if jerr := json.Unmarshal(answer, &out); jerr != nil || out.Error == nil {
		t.Fatalf("%s: the answer of a refusal has no error: %s", name, answer)
	}
	if out.Error.Code != Code(err) {
		t.Fatalf("%s: the answer says %s and the error %s", name, out.Error.Code, Code(err))
	}
	if se, ok := err.(*store.Error); ok && !reflect.DeepEqual(se, out.Error) {
		t.Fatalf("%s: the answer carries %+v and the error is %+v", name, out.Error, se)
	}
	return out.Error
}

// at reads a member of an answer by its path, such as document.state.
func at(t testing.TB, answer map[string]any, path string) any {
	t.Helper()
	var v any = answer
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("no %s in %v", path, answer)
		}
		v = m[part]
	}
	return v
}

func define(t testing.TB, s *store.Store) {
	t.Helper()
	run(t, s, dan, "define_class", `{"definition": `+invoiceClass+`}`)
}

func TestACommandIsTheCallOfTheGoAPI(t *testing.T) {
	s := open(t)
	define(t, s)
	ctx := context.Background()

	// Bob creates the invoice of the record, with an id that has slashes.
	created := run(t, s, bob, "create", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042", "net": "1230.00 PLN"},
	  "key": "create-0042", "business_date": "2026-11-12"}`)
	if at(t, created, "document.state") != "draft" || at(t, created, "document.version") != float64(1) || at(t, created, "document.created_by") != "bob" {
		t.Fatalf("create answered %v", created)
	}
	// The same document, read by the Go API: the answer is that document.
	doc, err := s.Get(ctx, "Invoice", "FV/2026/11/0042", store.As(bob))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(doc)
	got := run(t, s, bob, "get", `{"class": "Invoice", "id": "FV/2026/11/0042"}`)
	var wantDoc any
	_ = json.Unmarshal(want, &wantDoc)
	if !reflect.DeepEqual(got["document"], wantDoc) {
		t.Fatalf("get answered %v, the Go API %s", got["document"], want)
	}

	// The key makes twice once.
	again := run(t, s, bob, "create", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042", "net": "1230.00 PLN"},
	  "key": "create-0042", "business_date": "2026-11-12"}`)
	if !reflect.DeepEqual(again, created) {
		t.Fatalf("the same command with the same key answered %v, the first time %v", again, created)
	}
	if e := refused(t, s, bob, "set", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"note": "x"}, "key": "create-0042"}`); e.Code != store.CodeKeyReused {
		t.Fatalf("a key used by another command: %v", e)
	}

	// The business date is on the event.
	events := run(t, s, bob, "events", `{"class": "Invoice", "id": "FV/2026/11/0042"}`)
	first := events["events"].([]any)[0].(map[string]any)
	if first["business_date"] != "2026-11-12" || first["command_key"] != "create-0042" || first["actor_id"] != "bob" || first["actor_kind"] != "person" {
		t.Fatalf("the event of the create is %v", first)
	}

	set := run(t, s, bob, "set", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"note": "matches order PO-118"}}`)
	if at(t, set, "document.version") != float64(2) || at(t, set, "document.values.note") != "matches order PO-118" {
		t.Fatalf("set answered %v", set)
	}

	// The refusal is the Go API's, word for word.
	_, goErr := s.Transition(ctx, "Invoice", "FV/2026/11/0042", "approve", nil, store.As(bob))
	e := refused(t, s, bob, "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}`)
	if !reflect.DeepEqual(e, goErr) || e.Code != store.CodeForbidden || e.Path != "/access/transition/approve" {
		t.Fatalf("the command was refused with %+v, the call with %+v", e, goErr)
	}

	moved := run(t, s, ann, "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve", "values": null}`)
	if at(t, moved, "document.state") != "approved" || at(t, moved, "document.state_entered_by") != "ann" || at(t, moved, "document.state_entered_from") != "draft" {
		t.Fatalf("transition answered %v", moved)
	}

	listed := run(t, s, ann, "transitions", `{"class": "Invoice", "id": "FV/2026/11/0042"}`)
	if got := listed["transitions"].([]any); len(got) != 1 || got[0].(map[string]any)["name"] != "pay" || got[0].(map[string]any)["enabled"] != true {
		t.Fatalf("transitions answered %v", listed)
	}

	history := run(t, s, ann, "history", `{"class": "Invoice", "id": "FV/2026/11/0042"}`)
	if got := history["versions"].([]any); len(got) != 3 || got[2].(map[string]any)["state"] != "approved" {
		t.Fatalf("history answered %v", history)
	}
	version := run(t, s, ann, "version", `{"class": "Invoice", "id": "FV/2026/11/0042", "version": 1}`)
	if at(t, version, "version.state") != "draft" || at(t, version, "version.actor_id") != "bob" {
		t.Fatalf("version answered %v", version)
	}
	if e := refused(t, s, ann, "version", `{"class": "Invoice", "id": "FV/2026/11/0042", "version": 9}`); e.Code != store.CodeUnknownDocument {
		t.Fatalf("a version that does not exist: %v", e)
	}

	// A preview holds its document where a command does.
	preview := run(t, s, ann, "preview", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "pay"}`)
	if at(t, preview, "preview.document.state") != "paid" || at(t, preview, "preview.now") != float64(clock.Unix()) {
		t.Fatalf("preview answered %v", preview)
	}
	if got := run(t, s, ann, "get", `{"class": "Invoice", "id": "FV/2026/11/0042"}`); at(t, got, "document.state") != "approved" {
		t.Fatalf("a preview wrote: %v", got)
	}
	creating := run(t, s, bob, "preview", `{"class": "Invoice", "values": {"number": "FV/2026/11/0043"}, "at": "2026-12-01 08:00:00"}`)
	if at(t, creating, "preview.document.state") != "draft" || at(t, creating, "preview.now") != float64(time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC).Unix()) {
		t.Fatalf("the preview of a create answered %v", creating)
	}
	deleting := run(t, s, ann, "preview_delete", `{"class": "Invoice", "id": "FV/2026/11/0042"}`)
	if errs := at(t, deleting, "preview.errors").([]any); len(errs) != 1 || errs[0].(map[string]any)["code"] != store.CodeForbidden {
		t.Fatalf("the preview of a delete Ann may not make answered %v", deleting)
	}

	// A filter, a count and the SQL of an analyst.
	run(t, s, bob, "create", `{"class": "Invoice", "id": "FV/2026/11/0043", "values": {"number": "FV/2026/11/0043", "net": "10.00 PLN"}}`)
	found := run(t, s, ann, "query", `{"class": "Invoice", "where": "STATE() = \"draft\"", "order": ["-number"], "limit": 5, "offset": 0, "fields": ["number"]}`)
	if got := found["documents"].([]any); len(got) != 1 || at(t, got[0].(map[string]any), "values.number") != "FV/2026/11/0043" {
		t.Fatalf("query answered %v", found)
	}
	if none := run(t, s, ann, "query", `{"class": "Invoice", "where": "number = \"none\""}`); string(mustJSON(none["documents"])) != "[]" {
		t.Fatalf("a query that finds nothing answered %v, want an empty list", none)
	}
	if n := run(t, s, ann, "count", `{"class": "Invoice"}`); n["count"] != float64(2) {
		t.Fatalf("count answered %v", n)
	}
	rows := run(t, s, olga, "sql", `{"statement": "SELECT id, state FROM Invoice WHERE version >= ? ORDER BY id LIMIT ?", "args": [1, 1]}`)
	if !reflect.DeepEqual(rows["columns"], []any{"id", "state"}) || len(rows["rows"].([]any)) != 1 {
		t.Fatalf("sql answered %v", rows)
	}
	if e := refused(t, s, ann, "sql", `{"statement": "SELECT 1"}`); e.Code != store.CodeForbidden {
		t.Fatalf("SQL by somebody who is no analyst: %v", e)
	}

	// Several at once: the one that cannot move is reported, the other moves.
	many := run(t, s, ann, "preview_many", `{"class": "Invoice", "transition": "pay", "ids": ["FV/2026/11/0042", "nobody"]}`)
	outcomes := many["outcomes"].([]any)
	if len(outcomes) != 2 || at(t, outcomes[0].(map[string]any), "document.state") != "paid" || at(t, outcomes[1].(map[string]any), "refusal.code") != store.CodeUnknownDocument {
		t.Fatalf("preview_many answered %v", many)
	}
	many = run(t, s, ann, "transition_many", `{"class": "Invoice", "transition": "pay", "ids": ["FV/2026/11/0042", "nobody"], "key": "pay-run-1"}`)
	outcomes = many["outcomes"].([]any)
	if len(outcomes) != 2 || at(t, outcomes[0].(map[string]any), "document.state") != "paid" || at(t, outcomes[1].(map[string]any), "refusal.code") != store.CodeUnknownDocument {
		t.Fatalf("transition_many answered %v", many)
	}

	batch := run(t, s, bob, "create_many", `{"class": "Invoice", "documents": [{"values": {"number": "A"}}, {"values": {"number": "B"}}]}`)
	if got := batch["documents"].([]any); len(got) != 2 || at(t, got[1].(map[string]any), "values.number") != "B" {
		t.Fatalf("create_many answered %v", batch)
	}
	if e := refused(t, s, bob, "create_many", `{"class": "Invoice", "documents": [{"values": {"number": "C"}, "id": "c"}]}`); e.Code != CodeBadJSON || e.Path != "/documents/0/id" {
		t.Fatalf("a document of a batch with an id: %+v", e)
	}

	// Reset and refresh are commits of their own kind.
	reset := run(t, s, bob, "reset", `{"class": "Invoice", "id": "FV/2026/11/0043", "variables": ["net"]}`)
	if at(t, reset, "document.values.net") != nil {
		t.Fatalf("reset answered %v", reset)
	}
	refreshed := run(t, s, bob, "refresh", `{"class": "Invoice", "id": "FV/2026/11/0043"}`)
	if at(t, refreshed, "document.version") != float64(3) {
		t.Fatalf("refresh answered %v", refreshed)
	}
	if due := run(t, s, ann, "due", `{"class": "Invoice"}`); string(mustJSON(due["due"])) != "[]" {
		t.Fatalf("due answered %v, want an empty list", due)
	}

	// Delete answers {} and the document is gone.
	if gone := run(t, s, bob, "delete", `{"class": "Invoice", "id": "FV/2026/11/0043", "reason": "entered twice"}`); len(gone) != 0 {
		t.Fatalf("delete answered %v, want {}", gone)
	}
	if e := refused(t, s, bob, "get", `{"class": "Invoice", "id": "FV/2026/11/0043"}`); e.Code != store.CodeUnknownDocument {
		t.Fatalf("the deleted document: %v", e)
	}
}

func TestDefinitionsAreReadBackAsTheyWereGiven(t *testing.T) {
	s := open(t)

	// Nothing is wrong with the class, and a validation says so with an
	// empty list; a class with a mistake is not refused by a validation,
	// it is reported.
	if ok := run(t, s, dan, "validate_class", `{"definition": `+invoiceClass+`}`); string(mustJSON(ok["diagnostics"])) != "[]" {
		t.Fatalf("validate_class answered %v", ok)
	}
	broken := `{"name": "Broken", "variables": {"a": {"formula": "b + 1"}, "c": {"formula": "1 +"}}}`
	report := run(t, s, dan, "validate_class", `{"definition": `+broken+`}`)
	if found := report["diagnostics"].([]any); len(found) < 2 {
		t.Fatalf("validate_class of a class with two mistakes answered %v", report)
	}
	// Defining it is refused with every finding beside the error.
	answer, err := Exec(context.Background(), s, dan, "define_class", []byte(`{"definition": `+broken+`}`))
	if _, ok := err.(model.Diagnostics); !ok || Code(err) != CodeBadDefinition {
		t.Fatalf("define_class of a broken class returned %v", err)
	}
	var refusal struct {
		Error       store.Error        `json:"error"`
		Diagnostics []model.Diagnostic `json:"diagnostics"`
	}
	if jerr := json.Unmarshal(answer, &refusal); jerr != nil || refusal.Error.Code != CodeBadDefinition || len(refusal.Diagnostics) < 2 || refusal.Diagnostics[0].Path == "" {
		t.Fatalf("define_class of a broken class answered %s", answer)
	}
	if !strings.Contains(refusal.Error.Message, "findings refuse the definition") {
		t.Fatalf("the refusal says %q", refusal.Error.Message)
	}

	// Only a designer defines.
	if e := refused(t, s, ann, "define_class", `{"definition": `+invoiceClass+`}`); e.Code != store.CodeForbidden {
		t.Fatalf("a class defined by somebody who is no designer: %v", e)
	}

	// The class comes back as Dan wrote it, with what the database added
	// beside it: a definition read back can be sent again as it is.
	defined := run(t, s, dan, "define_class", `{"definition": `+invoiceClass+`}`)
	var compact any
	_ = json.Unmarshal([]byte(invoiceClass), &compact)
	if !reflect.DeepEqual(at(t, defined, "class.definition"), compact) || at(t, defined, "class.version") != float64(1) ||
		at(t, defined, "class.name") != "Invoice" || at(t, defined, "class.defined.by") != "dan" || at(t, defined, "class.defined.kind") != "person" {
		t.Fatalf("define_class answered %v", defined)
	}
	read := run(t, s, ann, "get_class", `{"name": "invoice"}`)
	if !reflect.DeepEqual(read, defined) {
		t.Fatalf("get_class answered %v, define_class %v", read, defined)
	}
	// Sent again byte for byte, it is the same definition and no new
	// version.
	answer, err = Exec(context.Background(), s, ann, "get_class", []byte(`{"name": "Invoice"}`))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Class struct {
			Definition json.RawMessage `json:"definition"`
		} `json:"class"`
	}
	if err := json.Unmarshal(answer, &raw); err != nil {
		t.Fatal(err)
	}
	again := run(t, s, dan, "define_class", `{"definition": `+string(raw.Class.Definition)+`}`)
	if at(t, again, "class.version") != float64(1) {
		t.Fatalf("the definition read back and sent again made %v", again)
	}

	// The next version, and the first one still read back.
	next := strings.Replace(invoiceClass, `"note":   {"type": "text"},`, `"note":   {"type": "text"}, "po": {"type": "text"},`, 1)
	if second := run(t, s, dan, "define_class", `{"definition": `+next+`}`); at(t, second, "class.version") != float64(2) {
		t.Fatalf("the second definition answered %v", second)
	}
	if first := run(t, s, ann, "get_class", `{"name": "Invoice", "version": 1}`); !reflect.DeepEqual(at(t, first, "class.definition"), compact) {
		t.Fatalf("version 1 read back as %v", first)
	}
	if e := refused(t, s, ann, "get_class", `{"name": "Invoice", "version": 7}`); e.Code != store.CodeUnknownClass {
		t.Fatalf("a version that does not exist: %v", e)
	}
	versions := run(t, s, ann, "definitions", `{"what": "class", "name": "Invoice"}`)
	if got := versions["versions"].([]any); len(got) != 2 || got[1].(map[string]any)["by"] != "dan" || got[1].(map[string]any)["version"] != float64(2) {
		t.Fatalf("definitions answered %v", versions)
	}
	if got := run(t, s, ann, "list_classes", `{}`)["classes"].([]any); len(got) != 2 || got[0] != "Substitution" || got[1] != "Invoice" {
		t.Fatalf("list_classes answered %v", got)
	}
	if moved := run(t, s, dan, "migrate", `{"class": "Invoice"}`); moved["migrated"] != float64(0) {
		t.Fatalf("migrate answered %v", moved)
	}

	// Two classes that name each other, in one call.
	purchase, approval := sample(t, "purchase"), sample(t, "approval")
	two := run(t, s, dan, "define_classes", `{"definitions": [`+purchase+`, `+approval+`]}`)
	if got := two["classes"].([]any); len(got) != 2 || got[0].(map[string]any)["name"] != "Purchase" || got[1].(map[string]any)["name"] != "Approval" {
		t.Fatalf("define_classes answered %v", two)
	}
	if ok := run(t, s, dan, "validate_classes", `{"definitions": [{"name": "Solo", "variables": {"a": {"type": "text"}}}]}`); ok["diagnostics"] == nil {
		t.Fatalf("validate_classes answered %v", ok)
	}

	// A draft tried on a create.
	draft := run(t, s, dan, "preview_draft", `{"definition": `+strings.Replace(invoiceClass, `"total":  {"formula": "net"}`, `"total":  {"formula": "net * 2"}`, 1)+`,
	  "values": {"number": "D-1", "net": "10.00 PLN"}}`)
	if at(t, draft, "preview.document.values.total") != "20.00 PLN" {
		t.Fatalf("preview_draft answered %v", draft)
	}

	// A schema: one of the engine's and one of Dan's.
	if got := run(t, s, ann, "list_schemas", `{}`)["schemas"].([]any); len(got) == 0 || got[0] != "Person" {
		t.Fatalf("list_schemas answered %v", got)
	}
	person := run(t, s, ann, "get_schema", `{"name": "Person"}`)
	if at(t, person, "schema.predefined") != true || at(t, person, "schema.definition.fields.email") != "text" {
		t.Fatalf("get_schema of the engine's Person answered %v", person)
	}
	contact := run(t, s, dan, "define_schema", `{"definition": {"name": "Contact", "fields": {"name": "text", "phone": "text"}}}`)
	if at(t, contact, "schema.version") != float64(1) || at(t, contact, "schema.definition.fields.phone") != "text" || at(t, contact, "schema.defined.by") != "dan" {
		t.Fatalf("define_schema answered %v", contact)
	}
	if got := run(t, s, ann, "get_schema", `{"name": "Contact"}`); !reflect.DeepEqual(got, contact) {
		t.Fatalf("get_schema answered %v", got)
	}
	if e := refused(t, s, ann, "get_schema", `{"name": "Nothing"}`); e.Code != store.CodeUnknownSchema {
		t.Fatalf("a schema that does not exist: %v", e)
	}
}

func TestTablesActionsAndTheQueue(t *testing.T) {
	s := open(t)

	rates := run(t, s, dan, "define_table", `{"definition": {"name": "Rates", "columns": {"currency": "text", "rate": "number"}, "key": ["currency"],
	  "access": {"read": "TRUE", "write": "INGROUP(\"finance\")"}}}`)
	if at(t, rates, "table.version") != float64(1) || at(t, rates, "table.definition.key").([]any)[0] != "currency" {
		t.Fatalf("define_table answered %v", rates)
	}
	if got := run(t, s, ann, "get_table", `{"name": "Rates"}`); !reflect.DeepEqual(got, rates) {
		t.Fatalf("get_table answered %v", got)
	}
	if got := run(t, s, ann, "list_tables", `{}`)["tables"].([]any); len(got) != 1 || got[0] != "Rates" {
		t.Fatalf("list_tables answered %v", got)
	}
	if n := run(t, s, ann, "import_rows", `{"table": "Rates", "rows": [{"currency": "EUR", "rate": 4.25}, {"currency": "USD", "rate": 3.9}]}`); n["imported"] != float64(2) {
		t.Fatalf("import_rows answered %v", n)
	}
	if n := run(t, s, ann, "import_rows", `{"table": "Rates", "rows": [{"currency": "CHF", "rate": 4.6}], "replace": true}`); n["imported"] != float64(1) {
		t.Fatalf("import_rows with replace answered %v", n)
	}
	if e := refused(t, s, bob, "import_rows", `{"table": "Rates", "rows": []}`); e.Code != store.CodeForbidden {
		t.Fatalf("rows written by somebody the table does not let: %v", e)
	}
	rows := run(t, s, bob, "table_rows", `{"table": "Rates"}`)
	if got := rows["rows"].([]any); len(got) != 1 || got[0].(map[string]any)["currency"] != "CHF" {
		t.Fatalf("table_rows answered %v", rows)
	}

	// An action of the host program, and a class whose rule calls it.
	posted := run(t, s, dan, "define_action", `{"definition": {"name": "books.post", "params": {"amount": {"type": "number"}}, "returns": {"entry": {"type": "text"}}}}`)
	if at(t, posted, "action.version") != float64(1) || at(t, posted, "action.definition.name") != "books.post" {
		t.Fatalf("define_action answered %v", posted)
	}
	if got := run(t, s, ann, "get_action", `{"name": "books.post"}`); !reflect.DeepEqual(got, posted) {
		t.Fatalf("get_action answered %v", got)
	}
	if e := refused(t, s, ann, "get_action", `{"name": "set"}`); e.Code != store.CodeUnknownAction || !strings.Contains(e.Message, "engine's own") {
		t.Fatalf("the definition of an action of the engine: %+v", e)
	}
	if ok := run(t, s, dan, "validate_action", `{"definition": {"name": "books.post", "params": {"amount": {"type": "number"}}}}`); string(mustJSON(ok["diagnostics"])) != "[]" {
		t.Fatalf("validate_action answered %v", ok)
	}
	catalogue := run(t, s, ann, "list_actions", `{}`)["actions"].([]any)
	last := catalogue[len(catalogue)-1].(map[string]any)
	if last["name"] != "books.post" || last["available"] != true {
		t.Fatalf("list_actions ends with %v", last)
	}

	would := run(t, s, olga, "preview_action", `{"action": "books.post", "args": {"amount": 12.5}}`)
	if at(t, would, "preview.call.args.amount") != 12.5 {
		t.Fatalf("preview_action answered %v", would)
	}
	tried := run(t, s, olga, "try_action", `{"action": "books.post", "args": {"amount": 12.5}}`)
	if at(t, tried, "outcome.ok") != true || at(t, tried, "outcome.result.entry") != "E-1" {
		t.Fatalf("try_action answered %v", tried)
	}

	run(t, s, dan, "define_class", `{"definition": {"name": "Bill", "variables": {"amount": {"type": "number"}},
	  "states": {"initial": "open", "list": ["open"]},
	  "rules": [{"on": "create", "do": [{"action": "books.post", "args": {"amount": "amount"}}]}],
	  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}}`)
	bill := run(t, s, bob, "create", `{"class": "Bill", "values": {"amount": 7}}`)
	id := at(t, bill, "document.id").(string)
	queue := run(t, s, olga, "outbox", `{"status": "pending", "class": "Bill", "id": `+string(mustJSON(id))+`, "limit": 10}`)
	queued := queue["actions"].([]any)
	if len(queued) != 1 || queued[0].(map[string]any)["action"] != "books.post" {
		t.Fatalf("outbox answered %v", queue)
	}
	if e := refused(t, s, ann, "outbox", `{}`); e.Code != store.CodeForbidden {
		t.Fatalf("the queue read by somebody who is no administrator: %v", e)
	}
	if n := run(t, s, olga, "drain", `{}`); n["attempted"] != float64(1) {
		t.Fatalf("drain answered %v", n)
	}
	key := queued[0].(map[string]any)["key"].(string)
	// An action that ran is not dead: there is nothing to retry.
	if e := refused(t, s, olga, "retry_action", `{"action_key": `+string(mustJSON(key))+`}`); e.Code == "" || e.Code == CodeBadJSON {
		t.Fatalf("a retry of an action that ran: %+v", e)
	}
	used["retry_action"]++

	checked := run(t, s, olga, "check_blobs", `{}`)
	if checked["checked"] != float64(0) || string(mustJSON(checked["mismatched"])) != "[]" {
		t.Fatalf("check_blobs answered %v", checked)
	}
}

func TestWhatIsNotACommandIsRefused(t *testing.T) {
	s := open(t)
	define(t, s)
	cases := []struct {
		name, object string
		code, path   string
		says         string
	}{
		{"set", `{"class": "Invoice", "id": "7", "valeus": {"note": "x"}}`, CodeBadJSON, "/valeus", `takes no "valeus"`},
		{"set", `{"class": "Invoice", "id": 7}`, CodeBadJSON, "/id", "id is a text, got 7"},
		{"set", `{"class": "Invoice", "id": "7", "values": ["note"]}`, CodeBadJSON, "/values", "values is a object"},
		{"set", `{"class": "Invoice", "id": "7", "id": "8"}`, CodeBadJSON, "/id", "given twice"},
		{"set", `{"class": "Invoice"}`, CodeBadJSON, "/id", "needs id"},
		{"set", `{"class": null, "id": "7"}`, CodeBadJSON, "/class", "needs class"},
		{"set", `["Invoice", "7"]`, CodeBadJSON, "", "not an object"},
		{"set", `{"class": "Invoice", "id": "7"`, CodeBadJSON, "", "not JSON"},
		{"set", ``, CodeBadJSON, "", "not JSON"},
		// The engine refuses a moment on a command that writes, and a key
		// on a batch, in its own words: they say what to do instead.
		{"set", `{"class": "Invoice", "id": "7", "at": "2026-11-13"}`, store.CodeNotAllowed, "", "runs at the database's clock"},
		{"create_many", `{"class": "Invoice", "documents": [], "key": "k"}`, store.CodeNotSupported, "", "a batch takes no idempotency key"},
		{"drain", `{"at": "2026-11-13"}`, CodeBadJSON, "/at", `takes no "at"`},
		{"get", `{"class": "Invoice", "id": "7", "key": "k"}`, CodeBadJSON, "/key", `takes no "key"`},
		{"get", `{"class": "Invoice", "id": "7", "at": "yesterday"}`, store.CodeBadValue, "/at", "at is a day"},
		{"get", `{"class": "Invoice", "id": "7", "at": true}`, CodeBadJSON, "/at", "at is a moment"},
		{"set", `{"class": "Invoice", "id": "7", "business_date": "13.11.2026"}`, store.CodeBadValue, "/business_date", "calendar day"},
		{"version", `{"class": "Invoice", "id": "7", "version": 1.5}`, CodeBadJSON, "/version", "whole number"},
		{"query", `{"class": "Invoice", "order": ["number", 3]}`, CodeBadJSON, "/order/1", "list of texts"},
		{"transition_many", `{"class": "Invoice", "transition": "pay", "ids": "7"}`, CodeBadJSON, "/ids", "list of texts"},
		{"import_rows", `{"table": "Rates", "rows": [{"currency": "EUR"}, 5]}`, CodeBadJSON, "/rows/1", "list of objects"},
		{"sql", `{"statement": "SELECT ?", "args": [[1]]}`, CodeBadJSON, "/args/0", "argument"},
		{"define_class", `{"definition": "Invoice"}`, CodeBadJSON, "/definition", "definition is a definition"},
		{"define_classes", `{"definitions": [{"name": "A"}, "B"]}`, CodeBadJSON, "/definitions/1", "JSON object"},
		{"list_classes", `{"class": "Invoice"}`, CodeBadJSON, "/class", "takes no"},
		{"get_class", `{"name": "Invoice", "for": "ann"}`, CodeBadJSON, "/for", `takes no "for"`},
		{"approve", `{}`, CodeUnknownCommand, "", "no command is named"},
		{"Transition", `{}`, CodeUnknownCommand, "", "no command is named"},
		{"transition-many", `{}`, CodeUnknownCommand, "", "no command is named"},
	}
	for _, c := range cases {
		e := refused(t, s, bob, c.name, c.object)
		if e.Code != c.code || e.Path != c.path || !strings.Contains(e.Message, c.says) {
			t.Errorf("%s %s: refused with %+v, want %s at %q saying %q", c.name, c.object, e, c.code, c.path, c.says)
		}
	}
	// The hint of an unknown member lists what the command takes, and of
	// a name that is nearly a command says which.
	if e := refused(t, s, bob, "set", `{"class": "Invoice", "id": "7", "valeus": {}}`); e.Hint != "set takes class, id, values, key, business_date, reason, for" {
		t.Errorf("the hint of an unknown member is %q", e.Hint)
	}
	if e := refused(t, s, bob, "TransitionMany", `{}`); e.Hint != "the command is transition_many" {
		t.Errorf("the hint of a call's name is %q", e.Hint)
	}
	// A refusal of the wire wrote nothing.
	if n := run(t, s, bob, "count", `{"class": "Invoice"}`); n["count"] != float64(0) {
		t.Errorf("a refused command wrote: %v", n)
	}
}

func TestWhoCallsIsNeverInTheCommand(t *testing.T) {
	s := open(t)
	define(t, s)
	// No member names the caller.
	for _, member := range []string{"as", "principal", "user", "groups", "kind", "actor"} {
		object := fmt.Sprintf(`{"class": "Invoice", "values": {"number": "1"}, %q: "olga"}`, member)
		if e := refused(t, s, bob, "create", object); e.Code != CodeBadJSON || e.Path != "/"+member {
			t.Errorf("a create that names its caller under %s: %+v", member, e)
		}
	}
	// The one thing a caller says about itself is for whom it acts, and
	// the engine checks it: without a substitution it is refused.
	e := refused(t, s, bob, "create", `{"class": "Invoice", "values": {"number": "1"}, "for": "ann"}`)
	if e.Code != store.CodeForbidden || e.Path != "/principal/for" {
		t.Fatalf("bob acting for ann with no substitution: %+v", e)
	}
	// Ann lets Bob act for her, through a command like any other.
	run(t, s, ann, "create", `{"class": "Substitution", "values": {"by": "bob"}}`)
	doc := run(t, s, bob, "create", `{"class": "Invoice", "values": {"number": "1"}, "for": "ann"}`)
	events := run(t, s, bob, "events", `{"class": "Invoice", "id": `+string(mustJSON(at(t, doc, "document.id")))+`}`)
	if first := events["events"].([]any)[0].(map[string]any); first["actor_id"] != "bob" || first["actor_for"] != "ann" {
		t.Fatalf("the event of a create bob made for ann is %v", first)
	}
}

func TestAnIDHoldsNoControlCharacter(t *testing.T) {
	s := open(t)
	define(t, s)
	for _, id := range []string{"a\nb", "a\tb", "a\u0000b", "a\u0085b", "\u007f"} {
		e := refused(t, s, bob, "create", `{"class": "Invoice", "id": `+string(mustJSON(id))+`, "values": {"number": "1"}}`)
		if e.Code != store.CodeBadValue || !strings.Contains(e.Message, "control character") {
			t.Errorf("a create under the id %q: %+v", id, e)
		}
	}
	// Every other character stays: slashes, spaces, any alphabet.
	for _, id := range []string{"FV/2026/11/0042", "a b", "zażółć", "a?b#c", "..", "due", "发票-7", `a"b\c`} {
		object := `{"class": "Invoice", "id": ` + string(mustJSON(id)) + `, "values": {"number": "1"}}`
		doc := run(t, s, bob, "create", object)
		if at(t, doc, "document.id") != id {
			t.Errorf("the id %q came back as %v", id, at(t, doc, "document.id"))
		}
		got := run(t, s, bob, "get", `{"class": "Invoice", "id": `+string(mustJSON(id))+`}`)
		if at(t, got, "document.id") != id {
			t.Errorf("the document %q was read back as %v", id, at(t, got, "document.id"))
		}
	}
}

func TestAFailureThatIsNoRefusalKeepsItsWords(t *testing.T) {
	answer, err := refuse(fmt.Errorf("disk on fire at /var/lib/acme.fado"))
	if Code(err) != CodeInternal || strings.Contains(string(answer), "acme") {
		t.Fatalf("a failure answered %s with %v", answer, err)
	}
	var out struct {
		Error store.Error `json:"error"`
	}
	if json.Unmarshal(answer, &out) != nil || out.Error.Code != CodeInternal {
		t.Fatalf("a failure answered %s", answer)
	}
	if Code(nil) != "" {
		t.Fatal("no error has a code")
	}
}

// TestEveryCommandIsRun fails when a command of the table was run by no
// test of this package. It runs last: the tests of a package run in the
// order of their files and, in a file, as written.
func TestEveryCommandIsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("needs the other tests to have run")
	}
	for _, c := range Commands() {
		if used[c.Name] == 0 {
			t.Errorf("no test of this package runs %s", c.Name)
		}
	}
}

// sample reads one of the classes under examples/classes.
func sample(t testing.TB, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "examples", "classes", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func mustJSON(v any) []byte {
	text, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return text
}
