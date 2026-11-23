package command

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
    "number": {"type": "text", "required": true, "label": "Number", "list": true, "description": "The supplier's number."},
    "net":    {"type": "money", "scale": 2, "label": "Net amount", "list": true, "format": "#,##0.00", "description": "The amount before VAT."},
    "note":   {"type": "text", "description": "Anything the next person should know."},
    "total":  {"formula": "net", "description": "What is paid."}
  },
  "states": {"initial": "draft", "list": ["draft", "approved", "paid"], "final": ["paid"],
             "labels": {"approved": "Approved"},
             "descriptions": {"draft": "Being filled in.", "approved": "Waiting for payment.", "paid": "Paid, and kept."}},
  "transitions": [
    {"name": "approve", "from": "draft", "to": "approved", "label": "Approve", "description": "Finance accepts it for payment."},
    {"name": "pay", "from": "approved", "to": "paid", "description": "Finance records the payment."}
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
	next := strings.Replace(invoiceClass, `"note":   {"type": "text", "description": "Anything the next person should know."},`,
		`"note":   {"type": "text", "description": "Anything the next person should know."}, "po": {"type": "text", "description": "The order it answers."},`, 1)
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
	draft := run(t, s, dan, "preview_draft", `{"definition": `+strings.Replace(invoiceClass, `"total":  {"formula": "net",`, `"total":  {"formula": "net * 2",`, 1)+`,
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
	// The next version of the action, and the first one still read back.
	second := run(t, s, dan, "define_action", `{"definition": {"name": "books.post", "params": {"amount": {"type": "number"}, "memo": {"type": "text"}}, "returns": {"entry": {"type": "text"}}}}`)
	if at(t, second, "action.version") != float64(2) {
		t.Fatalf("the second definition of the action answered %v", second)
	}
	if first := run(t, s, ann, "get_action", `{"name": "books.post", "version": 1}`); !reflect.DeepEqual(first, posted) {
		t.Fatalf("version 1 of the action read back as %v", first)
	}
	if e := refused(t, s, ann, "get_action", `{"name": "books.post", "version": 9}`); e.Code != store.CodeUnknownAction {
		t.Fatalf("a version of an action that does not exist: %v", e)
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
	if checked["checked"] != float64(0) || string(mustJSON(checked["mismatched"])) != "[]" || string(mustJSON(checked["missing"])) != "[]" {
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

// The stream of changes is a command whose answer is no object
// (ADR-0058, item 2): Exec says so, and the door that answers it with a
// stream reads its object as every object is read.
func TestTheStreamIsACommandWithItsObject(t *testing.T) {
	s := open(t)
	define(t, s)
	used["subscribe"]++
	e := refused(t, s, bob, "subscribe", `{"classes": ["Invoice"]}`)
	if e.Code != store.CodeNotSupported || !strings.Contains(e.Hint, "POST /v1/subscribe") || !strings.Contains(e.Hint, "Subscribe") {
		t.Errorf("subscribe sent to Exec is refused with %+v", e)
	}
	cmd, _ := Lookup("subscribe")
	if !cmd.Stream || cmd.Call != "Subscribe" || len(cmd.Answer) != 0 {
		t.Errorf("the row of subscribe: %+v", cmd)
	}
	for _, c := range Commands() {
		if c.Stream != (c.Name == "subscribe") {
			t.Errorf("%s: its answer is a stream: %v", c.Name, c.Stream)
		}
	}

	// The object: the four members, each left out or given.
	f, p, _, err := Subscription(bob, []byte(`{"classes": ["Invoice", "Memo"], "kinds": ["transition"], "to_states": ["approved"], "after": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"}`))
	want := store.ChangeFilter{Classes: []string{"Invoice", "Memo"}, Kinds: []string{"transition"}, ToStates: []string{"approved"}, After: "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"}
	if err != nil || !reflect.DeepEqual(f, want) || !reflect.DeepEqual(p, bob) {
		t.Errorf("the object of subscribe read as %+v for %+v: %v", f, p, err)
	}
	if f, _, _, err := Subscription(bob, []byte(`{}`)); err != nil || !reflect.DeepEqual(f, store.ChangeFilter{}) {
		t.Errorf("an empty object read as %+v: %v", f, err)
	}
	if f, _, _, err := Subscription(bob, []byte(`{"classes": null, "after": null}`)); err != nil || !reflect.DeepEqual(f, store.ChangeFilter{}) {
		t.Errorf("members that are null read as %+v: %v", f, err)
	}
	// For whom the caller acts is passed on, and the engine refuses it
	// in its own words.
	_, p, _, err = Subscription(bob, []byte(`{"for": "ann"}`))
	if err != nil || p.For != "ann" || p.ID != bob.ID {
		t.Errorf("an object with for read for %+v: %v", p, err)
	}
	if _, err := s.Subscribe(context.Background(), store.ChangeFilter{}, store.As(p)); Code(err) != store.CodeNotSupported {
		t.Errorf("a stream for somebody else: %v", err)
	}
	// What is not the object of subscribe.
	for _, c := range []struct{ object, path, says string }{
		{`{"class": "Invoice"}`, "/class", `takes no "class"`},
		{`{"classes": "Invoice"}`, "/classes", "list of texts"},
		{`{"kinds": ["set", 3]}`, "/kinds/1", "list of texts"},
		{`{"after": 7}`, "/after", "after is a text"},
		{`{"at": "2026-11-13"}`, "/at", `takes no "at"`},
		{`{"after": "a", "after": "b"}`, "/after", "given twice"},
		{`["Invoice"]`, "", "not an object"},
		{``, "", "not JSON"},
	} {
		_, _, _, err := Subscription(bob, []byte(c.object))
		e, _ := err.(*store.Error)
		if e == nil || e.Code != CodeBadJSON || e.Path != c.path || !strings.Contains(e.Message, c.says) {
			t.Errorf("subscribe %s: refused with %+v, want %s at %q saying %q", c.object, err, CodeBadJSON, c.path, c.says)
		}
	}
	_, _, _, err = Subscription(bob, []byte(`{"states": ["approved"]}`))
	if e, _ := err.(*store.Error); e == nil || e.Hint != "subscribe takes classes, kinds, to_states, after, reason" {
		t.Errorf("the hint of an unknown member is %v", err)
	}
}

// The flow of a class is a command (ADR-0063): the answer of the Go
// call under "flow", a period given as two moments, and the documents
// the caller may read.
// A live variable is worked out when the command live asks for it, for
// the caller, and no other command answers it (ADR-0076, item 7).
func TestALiveValueIsACommand(t *testing.T) {
	s := open(t)
	run(t, s, dan, "define_class", `{"definition": {
  "name": "Note", "title": "subject",
  "variables": {
    "subject": {"type": "text", "required": true, "description": "What it is about."},
    "owner":   {"type": "text", "default": "USER()", "description": "Who wrote it."},
    "mine":    {"type": "bool", "mode": "live", "formula": "owner = USER()", "label": "Mine", "description": "Whether the reader wrote it."},
    "shout":   {"formula": "UPPER(subject)", "description": "The subject, loud."}
  },
  "states": {"initial": "open", "list": ["open"], "descriptions": {"open": "It is read."}},
  "access": {"read": "TRUE", "create": "TRUE", "update": "owner = USER()", "delete": "FALSE"}
}}`)
	made := run(t, s, bob, "create", `{"class": "Note", "id": "N-1", "values": {"subject": "Lunch"}}`)
	if doc := made["document"].(map[string]any)["values"].(map[string]any); doc["shout"] != "LUNCH" {
		t.Errorf("create answered %v, want the kept value shout", doc)
	} else if _, carried := doc["mine"]; carried {
		t.Errorf("create answered the live value mine: %v", doc)
	}
	// For the caller: Bob wrote it, Ann did not.
	if got := run(t, s, bob, "live", `{"class": "Note", "id": "N-1", "variable": "mine"}`); got["value"] != true {
		t.Errorf("live answered Bob %v, want true", got)
	}
	if got := run(t, s, ann, "live", `{"class": "Note", "id": "N-1", "variable": "mine"}`); got["value"] != false {
		t.Errorf("live answered Ann %v, want false", got)
	}
	want, err := s.Live(context.Background(), "Note", "N-1", "mine", store.As(bob))
	if err != nil || want.Value != true {
		t.Errorf("the Go call answered %v, %v", want, err)
	}
	// What it refuses: a kept variable, an unknown one, a document that is
	// not there, and a member left out.
	for _, tc := range []struct{ object, code string }{
		{`{"class": "Note", "id": "N-1", "variable": "shout"}`, store.CodeNotAllowed},
		{`{"class": "Note", "id": "N-1", "variable": "nothing"}`, store.CodeUnknownVariable},
		{`{"class": "Note", "id": "N-9", "variable": "mine"}`, store.CodeUnknownDocument},
		{`{"class": "Note", "id": "N-1"}`, CodeBadJSON},
	} {
		if e := refused(t, s, bob, "live", tc.object); e.Code != tc.code {
			t.Errorf("live %s was refused with %s, want %s", tc.object, e.Code, tc.code)
		}
	}
	// A live value cannot be set, and no filter reads it.
	if e := refused(t, s, bob, "set", `{"class": "Note", "id": "N-1", "values": {"mine": true}}`); e.Code != store.CodeReadOnly || e.Variable != "mine" {
		t.Errorf("setting a live value was refused with %+v, want read_only about mine", e)
	}
	if e := refused(t, s, bob, "query", `{"class": "Note", "where": "mine"}`); e.Code != store.CodeNotAllowed {
		t.Errorf("a filter on a live value was refused with %+v, want not_allowed", e)
	}
	if e := refused(t, s, bob, "query", `{"class": "Note", "fields": ["mine"]}`); e.Code != store.CodeNotAllowed {
		t.Errorf("a live value among the fields was refused with %+v, want not_allowed", e)
	}
}

// A ref is checked when a command sets it, and get_class says what
// points at a class (ADR-0076, items 11 and 12).
func TestAPointerIsCheckedAndListed(t *testing.T) {
	s := open(t)
	open := `"states": {"initial": "open", "list": ["open"], "descriptions": {"open": "It is there."}}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}`
	run(t, s, dan, "define_class", `{"definition": {"name": "Order", "title": "number", "variables": {"number": {"type": "text", "description": "Its number."}}, `+open+`}}`)
	run(t, s, dan, "define_class", `{"definition": {"name": "Parcel", "variables": {"of": {"type": "ref", "class": "Order", "description": "The order it carries."}}, `+open+`}}`)
	run(t, s, bob, "create", `{"class": "Order", "id": "ORD-17", "values": {"number": "ORD-17"}}`)
	made := run(t, s, bob, "create", `{"class": "Parcel", "id": "P-1", "values": {"of": "Order:ORD-17"}}`)
	if at(t, made, "document.values.of") != "Order:ORD-17" {
		t.Errorf("create answered %v", made)
	}
	if e := refused(t, s, bob, "create", `{"class": "Parcel", "values": {"of": "Order:ORD-71"}}`); e.Code != store.CodeUnknownDocument || e.Variable != "of" {
		t.Errorf("a pointer at an order that is not there was refused with %+v, want unknown_document about of", e)
	}
	if e := refused(t, s, bob, "set", `{"class": "Parcel", "id": "P-1", "values": {"of": "Order:ORD-71"}}`); e.Code != store.CodeUnknownDocument || e.Variable != "of" {
		t.Errorf("a set to an order that is not there was refused with %+v, want unknown_document about of", e)
	}
	got := run(t, s, ann, "get_class", `{"name": "Order"}`)
	pointers, ok := at(t, got, "class.pointed_at_by").([]any)
	if !ok || len(pointers) != 1 || !reflect.DeepEqual(pointers[0], map[string]any{"class": "Parcel", "variable": "of"}) {
		t.Errorf("get_class answered pointed_at_by %v, want the variable of of Parcel", at(t, got, "class.pointed_at_by"))
	}
	if _, has := run(t, s, ann, "get_class", `{"name": "Parcel"}`)["class"].(map[string]any)["pointed_at_by"]; has {
		t.Errorf("get_class says something points at Parcel")
	}
}

func TestTheFlowIsACommand(t *testing.T) {
	s := open(t)
	define(t, s)
	ctx := context.Background()
	run(t, s, bob, "create", `{"class": "Invoice", "id": "I-1", "values": {"number": "I-1"}}`)
	run(t, s, bob, "create", `{"class": "Invoice", "id": "I-2", "values": {"number": "I-2"}}`)
	run(t, s, ann, "transition", `{"class": "Invoice", "id": "I-1", "transition": "approve"}`)

	got := run(t, s, ann, "flow", `{"class": "Invoice"}`)
	want, err := s.Flow(ctx, "Invoice", store.FlowPeriod{}, store.As(ann))
	if err != nil {
		t.Fatal(err)
	}
	var wantFlow any
	_ = json.Unmarshal(mustJSON(want), &wantFlow)
	if !reflect.DeepEqual(got["flow"], wantFlow) {
		t.Errorf("flow answered %v, and the Go call %v", got["flow"], wantFlow)
	}
	if at(t, got, "flow.class") != "Invoice" || at(t, got, "flow.created") != float64(2) {
		t.Errorf("flow answered %v", got)
	}
	states, transitions := at(t, got, "flow.states").([]any), at(t, got, "flow.transitions").([]any)
	if len(states) != 3 || len(transitions) != 2 {
		t.Fatalf("flow answered %d states and %d transitions", len(states), len(transitions))
	}
	draft, approve := states[0].(map[string]any), transitions[0].(map[string]any)
	if draft["name"] != "draft" || draft["initial"] != true || draft["documents"] != float64(1) || draft["stays"] != float64(1) ||
		approve["name"] != "approve" || approve["from"] != "draft" || approve["to"] != "approved" || approve["moves"] != float64(1) {
		t.Errorf("flow answered the state %v and the transition %v", draft, approve)
	}
	// A period, in each form a moment has: the clock of the tests stands
	// at 2026-11-13 09:30:00.
	for object, moves := range map[string]float64{
		`{"class": "Invoice", "since": "2026-11-13"}`:                                          1,
		`{"class": "Invoice", "since": "2026-11-14"}`:                                          0,
		`{"class": "Invoice", "until": "2026-11-13"}`:                                          0,
		`{"class": "Invoice", "since": "2026-11-13 09:30:00", "until": "2026-11-13 09:30:01"}`: 1,
		`{"class": "Invoice", "since": 1794562200, "until": 1794562201}`:                       1,
		`{"class": "Invoice", "since": null, "until": null}`:                                   1,
	} {
		if got := run(t, s, ann, "flow", object); at(t, got, "flow.transitions").([]any)[0].(map[string]any)["moves"] != moves {
			t.Errorf("flow %s answered %v moves, want %v", object, at(t, got, "flow.transitions").([]any)[0].(map[string]any)["moves"], moves)
		}
	}
	// Somebody who is in no group of the class reads the chart and no
	// document.
	eve := store.Principal{ID: "eve", Kind: store.ActorPerson}
	if got := run(t, s, eve, "flow", `{"class": "Invoice"}`); at(t, got, "flow.created") != float64(2) {
		// The class of these tests lets everybody read.
		t.Errorf("flow answered Eve %v", got)
	}
	// What is refused, and where.
	for object, want := range map[string][2]string{
		`{}`:                                     {CodeBadJSON, "/class"},
		`{"class": "Receipt"}`:                   {store.CodeUnknownClass, ""},
		`{"class": "Invoice", "since": "soon"}`:  {store.CodeBadValue, "/since"},
		`{"class": "Invoice", "until": "never"}`: {store.CodeBadValue, "/until"},
		`{"class": "Invoice", "since": "2026-11-14", "until": "2026-11-13"}`: {store.CodeBadValue, ""},
		`{"class": "Invoice", "at": "2026-11-13"}`:                           {CodeBadJSON, "/at"},
		`{"class": "Invoice", "where": "net > 0"}`:                           {CodeBadJSON, "/where"},
	} {
		if e := refused(t, s, ann, "flow", object); e.Code != want[0] || e.Path != want[1] {
			t.Errorf("flow %s: %s at %q (%s), want %s at %q", object, e.Code, e.Path, e.Message, want[0], want[1])
		}
	}
	if e := refused(t, s, ann, "flow", `{"class": "Invoice", "since": "soon"}`); !strings.HasPrefix(e.Message, "since is a day") {
		t.Errorf("a moment that is none is refused with: %s", e.Message)
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

// The users, groups and keys of a database that keeps its own
// (ADR-0057), command by command: Olga, the first administrator, made
// on the machine; Ann of finance; and the agent of accounts payable.
func TestTheCommandsOfUsersAndKeys(t *testing.T) {
	o := store.Options{Users: true, Now: func() time.Time { return clock }}
	o.Outbox.Manual = true
	s, err := store.Open(filepath.Join(t.TempDir(), "acme.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	machine := store.Principal{System: []string{store.GroupAdministrators}}
	olga := store.Principal{ID: "olga", Kind: store.ActorPerson, System: []string{store.GroupAdministrators}}
	ann := store.Principal{ID: "ann", Kind: store.ActorPerson, Groups: []string{"finance"}}

	made := run(t, s, machine, "create_user", `{"id": "olga", "kind": "person", "name": "Olga Nowak", "system_groups": ["administrators"]}`)
	if got, want := string(mustJSON(made)), `{"user":{"disabled":false,"groups":[],"id":"olga","kind":"person","name":"Olga Nowak","system_groups":["administrators"]}}`; got != want {
		t.Errorf("create_user answers\n%s\nwant\n%s", got, want)
	}
	run(t, s, olga, "create_user", `{"id": "ann", "kind": "person", "groups": ["finance"]}`)
	run(t, s, olga, "create_user", `{"id": "ap-agent", "kind": "agent", "name": "Accounts payable agent", "groups": ["finance"]}`)

	// Olga gives the agent a key. The secret is in the answer, this
	// once.
	answer := run(t, s, olga, "create_key", `{"user": "ap-agent", "label": "invoice matching", "expires": "2027-11-14"}`)
	key := answer["key"].(map[string]any)
	secret, _ := key["secret"].(string)
	if key["user"] != "ap-agent" || key["label"] != "invoice matching" || key["expires"] != "2027-11-14" || key["created_by"] != "olga" ||
		key["created_at"] != float64(clock.Unix()) || !strings.HasPrefix(secret, "fado_"+key["id"].(string)+"_") {
		t.Errorf("create_key answers %v", answer)
	}
	agent, err := s.Authenticate(secret)
	if err != nil || agent.ID != "ap-agent" || agent.Kind != store.ActorAgent {
		t.Fatalf("the key of the answer is let through as %+v, %v", agent, err)
	}

	// The agent, which starts with nothing but its key, asks who it is.
	if got, want := string(mustJSON(run(t, s, agent, "whoami", `{}`))), `{"user":{"disabled":false,"groups":["finance"],"id":"ap-agent","kind":"agent","name":"Accounts payable agent","system_groups":[]}}`; got != want {
		t.Errorf("whoami answers\n%s\nwant\n%s", got, want)
	}

	// Every user sees the users; a list narrows by group and by kind.
	if users := run(t, s, ann, "list_users", `{}`)["users"].([]any); len(users) != 3 || users[0].(map[string]any)["id"] != "ann" {
		t.Errorf("list_users answers %v", users)
	}
	if users := run(t, s, ann, "list_users", `{"group": "finance", "kind": "agent"}`)["users"].([]any); len(users) != 1 {
		t.Errorf("the agents of finance are %v", users)
	}
	if u := run(t, s, agent, "get_user", `{"id": "ann"}`)["user"].(map[string]any); u["kind"] != "person" {
		t.Errorf("get_user answers %v", u)
	}

	// set_user changes the members given and no other; a list given as
	// [] empties it, and one left out stays.
	u := run(t, s, olga, "set_user", `{"id": "ann", "name": "Ann Kowalska"}`)["user"].(map[string]any)
	if u["name"] != "Ann Kowalska" || len(u["groups"].([]any)) != 1 {
		t.Errorf("set_user with a name answers %v", u)
	}
	u = run(t, s, olga, "set_user", `{"id": "ann", "groups": [], "disabled": true}`)["user"].(map[string]any)
	if u["name"] != "Ann Kowalska" || len(u["groups"].([]any)) != 0 || u["disabled"] != true {
		t.Errorf("set_user with an empty list answers %v", u)
	}
	u = run(t, s, olga, "set_user", `{"id": "ann", "groups": ["finance"], "disabled": false, "name": null}`)["user"].(map[string]any)
	if u["name"] != "Ann Kowalska" || u["disabled"] != false {
		t.Errorf("set_user with a null answers %v", u)
	}
	// Olga may put herself in a group (ruling 2).
	run(t, s, olga, "set_user", `{"id": "olga", "groups": ["finance"]}`)

	// The keys of a user, and the end of one.
	keys := run(t, s, olga, "list_keys", `{"user": "ap-agent"}`)["keys"].([]any)
	if len(keys) != 1 || keys[0].(map[string]any)["secret"] != nil || keys[0].(map[string]any)["id"] != key["id"] {
		t.Errorf("list_keys answers %v", keys)
	}
	if own := run(t, s, agent, "list_keys", `{}`)["keys"].([]any); len(own) != 1 {
		t.Errorf("the agent's own keys are %v", own)
	}
	if got := string(mustJSON(run(t, s, agent, "revoke_key", `{"key": "`+key["id"].(string)+`"}`))); got != `{}` {
		t.Errorf("revoke_key answers %s", got)
	}
	if _, err := s.Authenticate(secret); err != store.ErrUnknownKey {
		t.Errorf("the revoked key answers %v", err)
	}

	// The changes of a user, oldest first.
	changes := run(t, s, olga, "user_changes", `{"id": "ap-agent"}`)["changes"].([]any)
	var whats []string
	for _, c := range changes {
		whats = append(whats, c.(map[string]any)["what"].(string))
	}
	if got := strings.Join(whats, " "); got != "created key_created key_revoked" {
		t.Errorf("the changes of the agent are %s", got)
	}
	if strings.Contains(string(mustJSON(changes)), "fado_") {
		t.Errorf("a change holds a key: %s", mustJSON(changes))
	}

	// The refusals come as the Go API gives them.
	for _, c := range []struct {
		p                        store.Principal
		name, object, code, path string
	}{
		{ann, "create_user", `{"id": "eve", "kind": "person"}`, store.CodeForbidden, "/system/administrators"},
		{ann, "create_key", `{"user": "ann"}`, store.CodeForbidden, "/system/administrators"},
		{olga, "create_user", `{"id": "ann", "kind": "person"}`, store.CodeDuplicate, "/id"},
		{olga, "create_user", `{"id": "eve"}`, CodeBadJSON, "/kind"},
		{olga, "create_user", `{"id": "eve", "kind": "robot"}`, store.CodeBadValue, "/kind"},
		{olga, "create_user", `{"id": "eve", "kind": "person", "groups": "finance"}`, CodeBadJSON, "/groups"},
		{olga, "create_user", `{"id": "eve", "kind": "person", "admin": true}`, CodeBadJSON, "/admin"},
		{olga, "set_user", `{"id": "eve", "name": "Eve"}`, store.CodeUnknownUser, "/id"},
		{olga, "set_user", `{"id": "ann"}`, store.CodeBadValue, ""},
		{olga, "set_user", `{"id": "olga", "system_groups": []}`, store.CodeNotAllowed, "/system_groups"},
		{olga, "get_user", `{"id": "eve"}`, store.CodeUnknownUser, "/id"},
		{olga, "create_key", `{"user": "eve"}`, store.CodeUnknownUser, "/user"},
		{olga, "create_key", `{"user": "ann", "expires": "next year"}`, store.CodeBadValue, "/expires"},
		{ann, "list_keys", `{"user": "ap-agent"}`, store.CodeForbidden, "/system/administrators"},
		{ann, "revoke_key", `{"key": "` + key["id"].(string) + `"}`, store.CodeUnknownKey, "/key"},
		{ann, "user_changes", `{"id": "olga"}`, store.CodeForbidden, "/system/administrators"},
		{olga, "create_user", `{"id": "eve", "kind": "person", "at": "2026-01-01"}`, store.CodeNotAllowed, ""},
		{store.Principal{}, "list_users", `{}`, store.CodeForbidden, ""},
	} {
		if e := refused(t, s, c.p, c.name, c.object); e.Code != c.code || e.Path != c.path {
			t.Errorf("%s %s as %s: refused with %s at %q, want %s at %q: %s", c.name, c.object, c.p.ID, e.Code, e.Path, c.code, c.path, e.Message)
		}
	}

	// A database whose program says itself who is calling does not
	// offer these commands (item 9).
	plain := open(t)
	for _, c := range Commands() {
		if !c.Users {
			continue
		}
		e := refused(t, plain, olga, c.Name, `{}`)
		if e.Code != CodeUnknownCommand || !strings.Contains(e.Message, "keeps no users") {
			t.Errorf("%s in a database that keeps no users is refused with %+v", c.Name, e)
		}
	}
	// And the commands that are not about users do not carry the mark.
	marked := 0
	for _, c := range Commands() {
		if c.Users {
			marked++
		}
	}
	if marked != 19 {
		t.Errorf("%d commands are of the users, want the nine of ADR-0057 and the ten of ADR-0071", marked)
	}
}

// The inlets of a database (ADR-0065), command by command: Dan writes
// the inlet of the bank, has the engine check it and tries it with a
// message; Olga, an administrator, defines it and removes it.
func TestTheCommandsOfTheInlets(t *testing.T) {
	o := store.Options{Users: true, Inbound: true, Now: func() time.Time { return clock },
		Secrets: func(context.Context, string, string) (string, error) { return "the secret of the bank", nil }}
	o.Outbox.Manual = true
	s, err := store.Open(filepath.Join(t.TempDir(), "acme.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	machine := store.Principal{System: []string{store.GroupAdministrators}}
	run(t, s, machine, "create_user", `{"id": "bank-in", "kind": "service", "groups": ["finance"]}`)
	run(t, s, olga, "define_class", `{"definition": `+invoiceClass+`}`)
	run(t, s, bob, "create", `{"class": "Invoice", "id": "i-1", "values": {"number": "FV/1"}}`)
	const inlet = `{"name": "bank.approved", "label": "The bank approves", "as": "bank-in",
 "connection": {"name": "bank", "secrets": ["signing_key"]},
 "proof": {"form": "timestamp.body", "header": "Bank-Signature", "secret": "signing_key"},
 "when": "JSONVALUE(BODY, \"type\") = \"approved\"",
 "key": "JSONVALUE(BODY, \"id\")",
 "command": "transition",
 "object": {"class": "Invoice", "id": "JSONVALUE(BODY, \"invoice\")", "transition": "approve", "values": {"note": "JSONVALUE(HEADERS, \"x-note\")"}}}`
	const message = `{"headers": {"X-Note": "by the bank"}, "body": {"id": "n-1", "type": "approved", "invoice": "i-1"}}`

	// validate_inlet answers the findings, and none for a sound one.
	if got := run(t, s, dan, "validate_inlet", `{"definition": `+inlet+`}`); len(got["diagnostics"].([]any)) != 0 {
		t.Errorf("validate_inlet of a sound inlet answers %v", got)
	}
	found := run(t, s, dan, "validate_inlet", `{"definition": `+strings.Replace(inlet, `"transition": "approve"`, `"transition": "settle"`, 1)+`}`)["diagnostics"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["code"] != model.CodeUnknownTransition || found[0].(map[string]any)["path"] != "/object/transition" {
		t.Errorf("validate_inlet of an inlet with a transition the class has not answers %v", found)
	}
	// try_inlet answers the command a message would become, for a
	// definition that is not stored, and does nothing.
	trial := run(t, s, dan, "try_inlet", `{"definition": `+inlet+`, "message": `+message+`}`)
	if got, want := string(mustJSON(trial)), `{"trial":{"acts":true,"as":"bank-in","command":"transition","inlet":"bank.approved",`+
		`"object":{"class":"Invoice","id":"i-1","key":"in:bank.approved:n-1","transition":"approve","values":{"note":"by the bank"}}}}`; got != want {
		t.Errorf("try_inlet answers\n%s\nwant\n%s", got, want)
	}
	if e := refused(t, s, ann, "try_inlet", `{"definition": `+inlet+`, "message": `+message+`}`); e.Code != store.CodeForbidden {
		t.Errorf("try_inlet by somebody who designs nothing: %+v", e)
	}
	if e := refused(t, s, dan, "try_inlet", `{"definition": `+inlet+`, "message": {"heads": {}}}`); e.Code != CodeBadJSON || e.Path != "/message" {
		t.Errorf("try_inlet with a message it cannot read: %+v", e)
	}
	// A body given as one text is the body itself: a form.
	form := run(t, s, dan, "try_inlet", `{"definition": `+strings.Replace(inlet, `"when": "JSONVALUE(BODY, \"type\") = \"approved\"",`, ``, 1)+`, "message": {"form": true, "body": "id=n-2&invoice=i-1"}}`)
	if object := form["trial"].(map[string]any)["object"].(map[string]any); object["id"] != "i-1" || object["key"] != "in:bank.approved:n-2" {
		t.Errorf("try_inlet of a form answers %v", form)
	}

	// define_inlet is an administrator's, and answers the definition as
	// it was given, with who defined it.
	if e := refused(t, s, dan, "define_inlet", `{"definition": `+inlet+`}`); e.Code != store.CodeForbidden || e.Path != "/system/administrators" {
		t.Errorf("define_inlet by a designer: %+v", e)
	}
	defined := run(t, s, olga, "define_inlet", `{"definition": `+inlet+`}`)["inlet"].(map[string]any)
	if defined["name"] != "bank.approved" || defined["version"] != float64(1) || defined["defined"].(map[string]any)["by"] != "olga" ||
		defined["definition"].(map[string]any)["as"] != "bank-in" {
		t.Errorf("define_inlet answers %v", defined)
	}
	if e := refused(t, s, olga, "define_inlet", `{"definition": `+strings.Replace(inlet, `"as": "bank-in"`, `"as": "nobody"`, 1)+`}`); e.Code != CodeBadDefinition {
		t.Errorf("define_inlet of an inlet that acts as nobody: %+v", e)
	}
	// get_inlet reads it back, list_inlets lists it with its address,
	// and definitions says who defined each version.
	if got := run(t, s, ann, "get_inlet", `{"name": "bank.approved"}`)["inlet"].(map[string]any); got["version"] != float64(1) || got["definition"].(map[string]any)["command"] != "transition" {
		t.Errorf("get_inlet answers %v", got)
	}
	listed := run(t, s, ann, "list_inlets", `{}`)["inlets"].([]any)
	if got, want := string(mustJSON(listed)), `[{"address":"/v1/in/bank.approved","as":"bank-in","available":true,"class":"Invoice","command":"transition","connection":"bank",`+
		`"defined":{"at":`+fmt.Sprint(clock.Unix())+`,"by":"olga","kind":"person"},"label":"The bank approves","name":"bank.approved",`+
		`"proof":{"form":"timestamp.body","header":"Bank-Signature","secret":"signing_key"},"transition":"approve","version":1}]`; got != want {
		t.Errorf("list_inlets answers\n%s\nwant\n%s", got, want)
	}
	if versions := run(t, s, ann, "definitions", `{"what": "inlet", "name": "bank.approved"}`)["versions"].([]any); len(versions) != 1 {
		t.Errorf("definitions of an inlet answers %v", versions)
	}
	// A trial of the stored inlet, by its name.
	if trial := run(t, s, dan, "try_inlet", `{"inlet": "bank.approved", "message": {"body": {"type": "other"}}}`)["trial"].(map[string]any); trial["acts"] != false || trial["command"] != nil {
		t.Errorf("try_inlet of a message of another kind answers %v", trial)
	}
	// The message itself is no command: it comes with a proof and no
	// key of a user, through Receive. Here it moves the invoice.
	ts := fmt.Sprint(clock.Unix())
	body := []byte(`{"id": "n-1", "type": "approved", "invoice": "i-1"}`)
	receipt, err := s.Receive(context.Background(), "bank.approved", &store.Message{Body: body,
		Headers: map[string]string{"Bank-Signature": "t=" + ts + ",v1=" + store.Sign("the secret of the bank", ts, body), "X-Note": "by the bank"}})
	if err != nil || receipt.Document.State != "approved" || receipt.Document.Values["note"] != "by the bank" || receipt.Document.UpdatedBy != "bank-in" {
		t.Errorf("the message of the bank: %+v %v", receipt, err)
	}
	// remove_inlet closes it, and is an administrator's.
	if e := refused(t, s, dan, "remove_inlet", `{"name": "bank.approved"}`); e.Code != store.CodeForbidden {
		t.Errorf("remove_inlet by a designer: %+v", e)
	}
	if got := run(t, s, olga, "remove_inlet", `{"name": "bank.approved"}`); len(got) != 0 {
		t.Errorf("remove_inlet answers %v", got)
	}
	if e := refused(t, s, olga, "remove_inlet", `{"name": "bank.approved"}`); e.Code != store.CodeUnknownInlet {
		t.Errorf("remove_inlet of an inlet that was removed: %+v", e)
	}
	if listed := run(t, s, ann, "list_inlets", `{}`)["inlets"].([]any); len(listed) != 0 {
		t.Errorf("list_inlets after the removal answers %v", listed)
	}
	if e := refused(t, s, ann, "get_inlet", `{"name": "bank.nothing"}`); e.Code != store.CodeUnknownInlet {
		t.Errorf("get_inlet of a name that is no inlet: %+v", e)
	}
}

// The log of reads is a command like the rest (ADR-0067): a read past a
// rule takes the member reason, and a database administrator lists what
// was read, by whom and why.
func TestTheLogOfReads(t *testing.T) {
	s := open(t)
	run(t, s, olga, "define_class", `{"definition": {"name": "Review", "variables": {"employee": {"type": "text"}},
	  "states": {"initial": "draft", "list": ["draft"]},
	  "access": {"read": "employee = USER()", "create": "TRUE", "update": "FALSE", "delete": "FALSE"}}}`)
	review := run(t, s, ann, "create", `{"class": "Review", "values": {"employee": "ann"}}`)
	id := at(t, review, "document.id").(string)
	cora := store.Principal{ID: "cora", Kind: store.ActorPerson, System: []string{store.GroupContentAdministrators}}
	if e := refused(t, s, cora, "get", `{"class": "Review", "id": `+string(mustJSON(id))+`}`); e.Code != store.CodeReasonRequired || e.Path != "/access/read" {
		t.Fatalf("a read past the rule with no reason: %+v", e)
	}
	run(t, s, cora, "get", `{"class": "Review", "id": `+string(mustJSON(id))+`, "reason": "the manager left"}`)
	run(t, s, olga, "sql", `{"statement": "SELECT count(*) FROM Review", "reason": "how many reviews"}`)
	reads := run(t, s, olga, "reads", `{"class": "Review", "id": `+string(mustJSON(id))+`}`)["reads"].([]any)
	if len(reads) != 1 || at(t, reads[0].(map[string]any), "by") != "cora" || at(t, reads[0].(map[string]any), "reason") != "the manager left" {
		t.Fatalf("reads answered %v", reads)
	}
	all := run(t, s, olga, "reads", `{"after": 0, "limit": 10}`)["reads"].([]any)
	if len(all) != 2 || at(t, all[1].(map[string]any), "command") != "sql" || at(t, all[1].(map[string]any), "rows") != float64(1) {
		t.Fatalf("reads answered %v", all)
	}
	if e := refused(t, s, cora, "reads", `{}`); e.Code != store.CodeForbidden || e.Path != "/system/administrators" {
		t.Fatalf("the log read by a content administrator: %+v", e)
	}
}

// The words of a class in another language (ADR-0068, item 6): a
// designer defines them, every caller reads and lists them, a path the
// class does not have is refused, and a later version of the class that
// drops a variable leaves its words noted as unused.
func TestTheCommandsOfTheWords(t *testing.T) {
	s := open(t)
	define(t, s)
	const polish = `{"class": "Invoice", "language": "pl", "label": "Faktura",
	  "variables": {"number": {"label": "Numer"}, "net": {"label": "Kwota netto", "description": "Kwota przed VAT."}},
	  "states": {"approved": {"label": "Zatwierdzona"}},
	  "transitions": {"approve": {"label": "Zatwierdź"}}}`
	if e := refused(t, s, bob, "define_words", `{"definition": `+polish+`}`); e.Code != store.CodeForbidden {
		t.Errorf("words defined by somebody who is no designer: %+v", e)
	}
	defined := run(t, s, dan, "define_words", `{"definition": `+polish+`}`)
	if at(t, defined, "words.name") != "class/Invoice/pl" || at(t, defined, "words.version") != 1.0 || at(t, defined, "words.defined.by") != "dan" {
		t.Errorf("define_words answered %v", defined)
	}
	if got := run(t, s, dan, "define_words", `{"definition": `+polish+`}`); at(t, got, "words.version") != 1.0 {
		t.Errorf("the same words again: %v", got)
	}
	// A path the class does not have is refused with its place.
	answer, err := Exec(context.Background(), s, dan, "define_words", []byte(`{"definition": {"class": "Invoice", "language": "pl", "variables": {"vat": {"label": "VAT"}}}}`))
	if err == nil || !strings.Contains(string(answer), `"path":"/variables/vat"`) || !strings.Contains(string(answer), `"code":"bad_value"`) {
		t.Errorf("words of a variable the class does not have: %v %s", err, answer)
	}
	// Every caller reads them, as a page of every person does.
	got := run(t, s, bob, "get_words", `{"class": "invoice", "language": "pl"}`)
	if at(t, got, "words.name") != "class/Invoice/pl" || at(t, got, "words.definition.label") != "Faktura" {
		t.Errorf("get_words answered %v", got)
	}
	if e := refused(t, s, bob, "get_words", `{"class": "Invoice", "language": "de"}`); e.Code != store.CodeUnknownWords {
		t.Errorf("words in a language nobody defined: %+v", e)
	}
	// A version of the class without net leaves the words of net unused.
	withoutNet := strings.Replace(invoiceClass, `"total":  {"formula": "net", "description": "What is paid."}`, `"total":  {"formula": "0", "description": "What is paid."}`, 1)
	withoutNet = strings.Replace(withoutNet, `    "net":    {"type": "money", "scale": 2, "label": "Net amount", "list": true, "format": "#,##0.00", "description": "The amount before VAT."},
`, "", 1)
	run(t, s, dan, "define_class", `{"definition": `+withoutNet+`}`)
	listed := run(t, s, bob, "list_words", `{"language": "pl"}`)["words"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["class"] != "Invoice" {
		t.Fatalf("list_words answered %v", listed)
	}
	if notes := listed[0].(map[string]any)["notes"].([]any); len(notes) != 1 || notes[0].(map[string]any)["code"] != model.CodeUnusedWords || notes[0].(map[string]any)["path"] != "/variables/net" {
		t.Errorf("the notes of words the class no longer uses: %v", notes)
	}
	again := strings.Replace(polish, `"label": "Faktura"`, `"label": "Faktura dostawcy"`, 1)
	again = strings.Replace(again, `, "net": {"label": "Kwota netto", "description": "Kwota przed VAT."}`, "", 1)
	run(t, s, dan, "define_words", `{"definition": `+again+`}`)
	if versions := run(t, s, bob, "definitions", `{"what": "words", "name": "class/Invoice/pl"}`)["versions"].([]any); len(versions) != 2 {
		t.Errorf("the versions of the words: %v", versions)
	}
	if got := run(t, s, bob, "get_words", `{"class": "Invoice", "language": "pl", "version": 1}`); at(t, got, "words.definition.label") != "Faktura" {
		t.Errorf("the first version of the words: %v", got)
	}
	if got := run(t, s, dan, "remove_words", `{"class": "Invoice", "language": "pl"}`); len(got) != 0 {
		t.Errorf("remove_words answered %v", got)
	}
	if listed := run(t, s, bob, "list_words", `{}`)["words"].([]any); len(listed) != 0 {
		t.Errorf("the words after they were taken away: %v", listed)
	}
	if e := refused(t, s, dan, "remove_words", `{"class": "Invoice", "language": "pl"}`); e.Code != store.CodeUnknownWords {
		t.Errorf("words taken away twice: %+v", e)
	}
}

// TestEveryCommandIsRun fails when a command of the table was run by no
// test of this package. It runs last: the tests of a package run in the
// order of their files and, in a file, as written. When the tests that
// run the commands were left out, by -run or by -short, it has nothing
// to say.
// The bundle of a class (ADR-0070): every caller reads it, in the
// words of a language; a page that holds it is answered that it has not
// changed; a language that is no tag is refused at its place.
func TestTheBundleCommand(t *testing.T) {
	s := open(t)
	define(t, s)
	run(t, s, dan, "define_words", `{"definition": {"class": "Invoice", "language": "pl", "label": "Faktura", "variables": {"net": {"label": "Kwota netto"}}}}`)
	got := run(t, s, bob, "get_bundle", `{"class": "invoice", "language": "PL"}`)
	if at(t, got, "class") != "Invoice" || at(t, got, "language") != "pl" || at(t, got, "definition.label") != "Faktura" ||
		at(t, got, "definition.variables.net.label") != "Kwota netto" || at(t, got, "words.class/Invoice/pl") != 1.0 {
		t.Errorf("get_bundle answered %v", got)
	}
	tag, _ := got["tag"].(string)
	if same := run(t, s, bob, "get_bundle", `{"class": "Invoice", "language": "pl", "tag": "`+tag+`"}`); len(same) != 2 || same["unchanged"] != true || same["tag"] != tag {
		t.Errorf("a page that holds the bundle is answered %v", same)
	}
	if e := refused(t, s, bob, "get_bundle", `{"class": "Invoice", "language": "polish"}`); e.Code != store.CodeBadValue || e.Path != "/language" {
		t.Errorf("a language that is no tag: %+v", e)
	}
	if e := refused(t, s, bob, "get_bundle", `{"class": "Nothing"}`); e.Code != store.CodeUnknownClass {
		t.Errorf("a class the database does not have: %+v", e)
	}
}

// The list of functions is a command (ADR-0074): the engine's own
// catalogue, whole, for whoever writes a formula, with nothing taken
// and nothing kept from any caller.
func TestTheListOfFunctionsIsACommand(t *testing.T) {
	s := open(t)
	got := run(t, s, ann, "list_functions", `{}`)
	if len(got) != 2 {
		t.Fatalf("list_functions answers the members %v", got)
	}
	functions := got["functions"].([]any)
	byName := map[string]map[string]any{}
	for _, f := range functions {
		entry := f.(map[string]any)
		byName[entry["name"].(string)] = entry
		for _, member := range []string{"name", "kind", "category", "signature", "params", "returns", "description"} {
			if _, ok := entry[member]; !ok {
				t.Errorf("%v has no %s", entry["name"], member)
			}
		}
	}
	// The catalogue is the one the manual's page of functions is written
	// from; the test of the generated pages holds the two together.
	if len(functions) < 100 || len(byName) != len(functions) {
		t.Fatalf("list_functions answers %d functions, %d by name", len(functions), len(byName))
	}
	if round := byName["ROUND"]; round["signature"] != "ROUND(number[, digits[, method]])" {
		t.Errorf("ROUND is told as %v", round)
	}
	ceiling := byName["CEILING"]
	if ceiling["signature"] != "CEILING(number[, digits])" || ceiling["kind"] != "pure" || ceiling["category"] != "numeric" || ceiling["returns"] != "number" {
		t.Errorf("CEILING is told as %v", ceiling)
	}
	if params := ceiling["params"].([]any); len(params) != 2 || !reflect.DeepEqual(params[0], map[string]any{"name": "number", "type": "number"}) ||
		!reflect.DeepEqual(params[1], map[string]any{"name": "digits", "type": "number", "optional": true}) {
		t.Errorf("the arguments of CEILING are told as %v", ceiling["params"])
	}
	if _, marked := ceiling["not_built"]; marked {
		t.Errorf("CEILING is built, and is told as %v", ceiling)
	}
	if also := ceiling["aliases"]; !reflect.DeepEqual(also, []any{"CEIL"}) {
		t.Errorf("the other names of CEILING are told as %v", also)
	}
	if today := byName["TODAY"]; today["kind"] != "context" || len(today["params"].([]any)) != 0 || today["signature"] != "TODAY()" {
		t.Errorf("TODAY is told as %v", today)
	}
	if first := byName["FIRST"]; first["not_built"] != true {
		t.Errorf("FIRST is not built yet, and is told as %v", first)
	}
	if lookup := byName["LOOKUP"]; lookup["kind"] != "lookup" {
		t.Errorf("LOOKUP is told as %v", lookup)
	}
	var kinds []string
	for _, k := range got["kinds"].([]any) {
		kind := k.(map[string]any)
		if text, _ := kind["description"].(string); text == "" {
			t.Errorf("the kind %v says nothing", kind["name"])
		}
		kinds = append(kinds, kind["name"].(string))
	}
	if strings.Join(kinds, " ") != "pure context lookup query secret" {
		t.Errorf("the kinds are told as %v", kinds)
	}
	// Every kind a function has is one the answer explains.
	for name, entry := range byName {
		if !strings.Contains(" "+strings.Join(kinds, " ")+" ", " "+entry["kind"].(string)+" ") {
			t.Errorf("%s is of the kind %v, which the answer does not explain", name, entry["kind"])
		}
	}
	if e := refused(t, s, ann, "list_functions", `{"name": "ROUND"}`); e.Code != CodeBadJSON || e.Path != "/name" {
		t.Errorf("list_functions takes nothing: %+v", e)
	}
}

// The commands of signing in (ADR-0071): an administrator makes a link,
// lists and ends a person's sessions, clears a second step and reads the
// log of sign-ins; a person lists and ends their own sessions.
func TestTheCommandsOfSigningIn(t *testing.T) {
	o := store.Options{Users: true, Now: func() time.Time { return clock }}
	o.Outbox.Manual = true
	s, err := store.Open(filepath.Join(t.TempDir(), "acme.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	machine := store.Principal{System: []string{store.GroupAdministrators}}
	olga := store.Principal{ID: "olga", Kind: store.ActorPerson, System: []string{store.GroupAdministrators}}
	ann := store.Principal{ID: "ann", Kind: store.ActorPerson, Groups: []string{"finance"}}
	run(t, s, machine, "create_user", `{"id": "olga", "kind": "person", "system_groups": ["administrators"]}`)
	run(t, s, olga, "create_user", `{"id": "ann", "kind": "person", "groups": ["finance"]}`)
	ctx := context.Background()

	link := run(t, s, olga, "create_password_link", `{"user": "ann"}`)["link"].(map[string]any)
	token, _ := link["token"].(string)
	if link["user"] != "ann" || link["made_by"] != "olga" || !strings.HasPrefix(token, "fadol_") {
		t.Fatalf("create_password_link answers %v", link)
	}
	if e := refused(t, s, ann, "create_password_link", `{"user": "olga"}`); e.Code != store.CodeForbidden {
		t.Errorf("Ann makes a link: %+v", e)
	}
	if err := s.UsePasswordLink(ctx, token, "ann's long password", "10.0.0.8"); err != nil {
		t.Fatal(err)
	}
	in, err := s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.8")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.FinishSignIn(ctx, in.ID, "", "10.0.0.8", store.SessionLengths{})
	if err != nil {
		t.Fatal(err)
	}
	listed := run(t, s, ann, "list_sessions", `{}`)["sessions"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["id"] != sess.ID || listed[0].(map[string]any)["token"] != nil {
		t.Errorf("Ann's sessions: %v", listed)
	}
	if e := refused(t, s, ann, "list_sessions", `{"user": "olga"}`); e.Code != store.CodeForbidden {
		t.Errorf("Ann lists Olga's sessions: %+v", e)
	}
	run(t, s, olga, "end_session", `{"session": "`+sess.ID+`"}`)
	if got := run(t, s, ann, "list_sessions", `{}`)["sessions"].([]any); len(got) != 0 {
		t.Errorf("the sessions after one was ended: %v", got)
	}
	if e := refused(t, s, ann, "end_session", `{"session": "NOSUCH0000"}`); e.Code != store.CodeUnknownSession || e.Path != "/session" {
		t.Errorf("a session the database does not have: %+v", e)
	}
	run(t, s, olga, "clear_second_step", `{"user": "ann"}`)
	entries := run(t, s, olga, "sign_ins", `{"user": "ann"}`)["sign_ins"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["way"] != "link" || entries[1].(map[string]any)["way"] != "password" {
		t.Errorf("Ann's sign-ins: %v", entries)
	}
	if e := refused(t, s, ann, "sign_ins", `{}`); e.Code != store.CodeForbidden {
		t.Errorf("Ann reads the log of sign-ins: %+v", e)
	}
}

// The clients and the grants (ADR-0071, items 6 and 10): Olga adds
// FadoFace, Ann connects Claude, and the commands list them and end
// them. Ann's Claude acts for Ann alone: a command of it that names
// somebody else is refused.
func TestTheCommandsOfClientsAndGrants(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "acme.fado"), store.Options{Users: true, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	machine := store.Principal{System: []string{store.GroupAdministrators}}
	olga := store.Principal{ID: "olga", Kind: store.ActorPerson, System: []string{store.GroupAdministrators}}
	ann := store.Principal{ID: "ann", Kind: store.ActorPerson, Groups: []string{"finance"}}
	run(t, s, machine, "create_user", `{"id": "olga", "kind": "person", "system_groups": ["administrators"]}`)
	run(t, s, olga, "create_user", `{"id": "ann", "kind": "person", "groups": ["finance"]}`)
	run(t, s, olga, "create_user", `{"id": "bob", "kind": "person"}`)

	added := run(t, s, olga, "add_client", `{"id": "fadoface", "name": "FadoFace", "redirect_uris": ["https://face.acme.example/callback"], "address": "/v1"}`)["client"].(map[string]any)
	if added["id"] != "fadoface" || added["added_by"] != "olga" || added["address"] != "/v1" {
		t.Errorf("add_client answers %v", added)
	}
	if e := refused(t, s, ann, "add_client", `{"id": "x", "name": "X", "redirect_uris": ["https://x.example/cb"], "address": "/v1"}`); e.Code != store.CodeForbidden {
		t.Errorf("Ann adds a client: %+v", e)
	}
	if e := refused(t, s, olga, "add_client", `{"id": "x", "name": "X", "redirect_uris": ["http://x.example/cb"], "address": "/v1"}`); e.Code != store.CodeBadValue || e.Path != "/redirect_uris" {
		t.Errorf("a code sent over http: %+v", e)
	}
	if clients := run(t, s, olga, "list_clients", `{}`)["clients"].([]any); len(clients) != 1 {
		t.Errorf("the clients: %v", clients)
	}

	// Ann connects Claude, as the server's pages do it.
	ctx := context.Background()
	claude := store.Client{ID: "https://claude.ai/oauth/mcp-oauth-client-metadata", Name: "Claude", RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}, Address: store.AddressAgents}
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	id, err := s.AskConsent(ctx, store.Asked{Client: claude, RedirectURI: claude.RedirectURIs[0], Challenge: base64.RawURLEncoding.EncodeToString(sum[:]), Resource: "https://fado.acme.example/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := s.Allow(ctx, id, store.As(ann))
	if err != nil {
		t.Fatal(err)
	}
	passes, err := s.Exchange(ctx, store.CodeExchange{Client: claude.ID, Code: code, RedirectURI: claude.RedirectURIs[0], Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.PassCaller(ctx, passes.Pass, store.AddressAgents)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, s, agent, "whoami", `{}`); got["for"] != "ann" || got["user"].(map[string]any)["id"] != "ann~claude.ai" {
		t.Errorf("Ann's Claude is %v", got)
	}
	if e := refused(t, s, agent, "whoami", `{"for": "bob"}`); e.Code != store.CodeForbidden || e.Path != "/for" {
		t.Errorf("Ann's Claude acts for Bob: %+v", e)
	}
	grants := run(t, s, ann, "list_grants", `{}`)["grants"].([]any)
	if len(grants) != 1 || grants[0].(map[string]any)["agent"] != "ann~claude.ai" || grants[0].(map[string]any)["address"] != "/mcp" {
		t.Fatalf("Ann's grants: %v", grants)
	}
	if e := refused(t, s, ann, "list_grants", `{"user": "olga"}`); e.Code != store.CodeForbidden {
		t.Errorf("Ann lists Olga's grants: %+v", e)
	}
	if e := refused(t, s, ann, "end_grant", `{"grant": "NOSUCH0000"}`); e.Code != store.CodeUnknownGrant || e.Path != "/grant" {
		t.Errorf("a grant the database does not have: %+v", e)
	}
	run(t, s, olga, "end_grant", `{"grant": "`+grants[0].(map[string]any)["id"].(string)+`"}`)
	if got := run(t, s, ann, "list_grants", `{}`)["grants"].([]any); len(got) != 0 {
		t.Errorf("the grants after one was ended: %v", got)
	}
	run(t, s, olga, "remove_client", `{"client": "fadoface"}`)
	if e := refused(t, s, olga, "remove_client", `{"client": "nobody"}`); e.Code != store.CodeUnknownClient {
		t.Errorf("a client the database does not have: %+v", e)
	}
}

// TestTheCommandsOfTheApps follows Acme (ADR-0077): Dan, a designer,
// makes the app Expenses and puts two classes in it; Ann, who is no
// designer, reads what there is.
func TestTheCommandsOfTheApps(t *testing.T) {
	s := open(t)

	// A database has no app until somebody makes one.
	if got := run(t, s, ann, "list_apps", `{}`)["apps"].([]any); len(got) != 0 {
		t.Fatalf("list_apps of a new database answered %v", got)
	}
	if e := refused(t, s, ann, "get_app", `{"name": "Expenses"}`); e.Code != store.CodeUnknownApp || !strings.Contains(e.Hint, "define_app") {
		t.Fatalf("an app that does not exist: %+v", e)
	}

	// Making one is for a designer.
	expenses := `{"name": "Expenses", "label": "Expenses", "description": "What employees paid for the company, approved and paid back."}`
	if e := refused(t, s, ann, "define_app", `{"definition": `+expenses+`}`); e.Code != store.CodeForbidden {
		t.Fatalf("Ann, who is no designer, defined an app: %+v", e)
	}
	made := run(t, s, dan, "define_app", `{"definition": `+expenses+`}`)
	if at(t, made, "app.name") != "Expenses" || at(t, made, "app.version") != float64(1) || at(t, made, "app.defined.by") != "dan" ||
		at(t, made, "app.definition.description") != "What employees paid for the company, approved and paid back." {
		t.Fatalf("define_app answered %v", made)
	}
	// The same definition again changes nothing; another one is the next
	// version, and the database keeps the current one.
	if again := run(t, s, dan, "define_app", `{"definition": `+expenses+`}`); !reflect.DeepEqual(again, made) {
		t.Fatalf("the same app defined again answered %v", again)
	}
	next := run(t, s, dan, "define_app", `{"definition": {"name": "Expenses", "label": "Expenses", "description": "What employees paid for the company.", "icon": "receipt", "color": "blue"}}`)
	if at(t, next, "app.version") != float64(2) || at(t, next, "app.definition.icon") != "receipt" {
		t.Fatalf("the next version of an app answered %v", next)
	}
	// Anybody reads an app back, as anybody reads a class.
	if got := run(t, s, ann, "get_app", `{"name": "expenses"}`); !reflect.DeepEqual(got, next) {
		t.Fatalf("get_app answered %v", got)
	}
	run(t, s, dan, "define_app", `{"definition": {"name": "Helpdesk", "label": "Help desk"}}`)
	if got := run(t, s, ann, "list_apps", `{}`)["apps"]; !reflect.DeepEqual(got, []any{"Expenses", "Helpdesk"}) {
		t.Fatalf("list_apps answered %v", got)
	}

	// What is wrong with a definition is found, each thing at its place.
	for wrong, path := range map[string]string{
		`{"label": "No name"}`:                  "/name",
		`{"name": "2fast"}`:                     "/name",
		`{"name": "EXPENSES", "label": "Loud"}`: "/name",
		`{"name": "Trips", "colour": "blue"}`:   "",
	} {
		answer, err := Exec(context.Background(), s, dan, "define_app", []byte(`{"definition": `+wrong+`}`))
		var refusal struct {
			Diagnostics []model.Diagnostic `json:"diagnostics"`
		}
		if _, ok := err.(model.Diagnostics); !ok || json.Unmarshal(answer, &refusal) != nil || len(refusal.Diagnostics) != 1 || refusal.Diagnostics[0].Path != path {
			t.Fatalf("define_app of %s answered %s", wrong, answer)
		}
	}

	// A class says which app it belongs to, in its own definition. An
	// app the database does not have is a finding at that line, before
	// anything is stored.
	expense := func(app string) string {
		return `{"name": "Expense", "label": "Expense", "plural": "Expenses", "app": "` + app + `", "description": "What an employee paid for the company.",
		  "variables": {"what": {"type": "text", "description": "What was bought."}}}`
	}
	report := run(t, s, dan, "validate_class", `{"definition": `+expense("Travel")+`}`)
	found := report["diagnostics"].([]any)
	if len(found) != 1 || at(t, found[0].(map[string]any), "code") != model.CodeUnknownApp || at(t, found[0].(map[string]any), "path") != "/app" {
		t.Fatalf("a class of an app that does not exist: %v", report)
	}
	if _, err := Exec(context.Background(), s, dan, "define_class", []byte(`{"definition": `+expense("Travel")+`}`)); Code(err) != CodeBadDefinition {
		t.Fatalf("define_class of a class of an app that does not exist returned %v", err)
	}
	defined := run(t, s, dan, "define_class", `{"definition": `+expense("Expenses")+`}`)
	if at(t, defined, "class.definition.app") != "Expenses" {
		t.Fatalf("define_class answered %v", defined)
	}
	run(t, s, dan, "define_class", `{"definition": {"name": "CostCentre", "app": "Expenses", "description": "The part of the company that pays.", "variables": {"name": {"type": "text", "description": "Its name."}}}}`)
	define(t, s) // Invoice, which belongs to no app

	// The classes of an app, in the order they were defined; every class
	// when no app is named; and an app nothing belongs to yet.
	if got := run(t, s, ann, "list_classes", `{"app": "Expenses"}`)["classes"]; !reflect.DeepEqual(got, []any{"Expense", "CostCentre"}) {
		t.Fatalf("the classes of Expenses: %v", got)
	}
	if got := run(t, s, ann, "list_classes", `{}`)["classes"].([]any); len(got) != 4 {
		t.Fatalf("every class, the engine's own Substitution among them: %v", got)
	}
	if got := run(t, s, ann, "list_classes", `{"app": "Helpdesk"}`)["classes"].([]any); len(got) != 0 {
		t.Fatalf("the classes of an app that has none: %v", got)
	}
	if e := refused(t, s, ann, "list_classes", `{"app": "Travel"}`); e.Code != store.CodeUnknownApp || !strings.Contains(e.Hint, "Expenses, Helpdesk") {
		t.Fatalf("the classes of an app that does not exist: %+v", e)
	}

	// A class moves to another app as it changes in any way: by its next
	// version. No document is touched.
	moved := run(t, s, dan, "define_class", `{"definition": `+expense("Helpdesk")+`}`)
	if at(t, moved, "class.version") != float64(2) {
		t.Fatalf("a class moved to another app answered %v", moved)
	}
	if got := run(t, s, ann, "list_classes", `{"app": "Helpdesk"}`)["classes"]; !reflect.DeepEqual(got, []any{"Expense"}) {
		t.Fatalf("the classes of Helpdesk after the move: %v", got)
	}
	// The page of a face reads the app of a class with the rest of it.
	if bundle := run(t, s, ann, "get_bundle", `{"class": "Expense"}`); at(t, bundle, "definition.app") != "Helpdesk" {
		t.Fatalf("the bundle of a class does not say its app: %v", bundle["definition"])
	}
	// Who defined each version of an app is kept, as for every definition.
	if got := run(t, s, ann, "definitions", `{"what": "app", "name": "Expenses"}`)["versions"].([]any); len(got) != 2 {
		t.Fatalf("the versions of the app Expenses: %v", got)
	}

	// A table says which app it belongs to as a class does (ADR-0079); the
	// tables of an app are listed as its classes are; and a table moves to
	// another app by its next version while it holds rows.
	rates := func(app string) string {
		return `{"name": "Rates", "app": "` + app + `", "columns": {"currency": "text", "rate": "decimal(4)"}, "key": ["currency"], "access": {"read": "TRUE", "write": "TRUE"}}`
	}
	if _, err := Exec(context.Background(), s, dan, "define_table", []byte(`{"definition": `+rates("Travel")+`}`)); Code(err) != CodeBadDefinition {
		t.Fatalf("define_table of a table of an app that does not exist returned %v", err)
	}
	if table := run(t, s, dan, "define_table", `{"definition": `+rates("Expenses")+`}`); at(t, table, "table.definition.app") != "Expenses" {
		t.Fatalf("define_table answered %v", table)
	}
	if got := run(t, s, ann, "list_tables", `{"app": "Expenses"}`)["tables"]; !reflect.DeepEqual(got, []any{"Rates"}) {
		t.Fatalf("the tables of Expenses: %v", got)
	}
	if got := run(t, s, ann, "list_tables", `{"app": "Helpdesk"}`)["tables"].([]any); len(got) != 0 {
		t.Fatalf("the tables of an app that has none: %v", got)
	}
	if got := run(t, s, ann, "list_tables", `{}`)["tables"]; !reflect.DeepEqual(got, []any{"Rates"}) {
		t.Fatalf("every table: %v", got)
	}
	if e := refused(t, s, ann, "list_tables", `{"app": "Travel"}`); e.Code != store.CodeUnknownApp {
		t.Fatalf("the tables of an app that does not exist: %+v", e)
	}
	run(t, s, dan, "import_rows", `{"table": "Rates", "rows": [{"currency": "EUR", "rate": "4.27"}]}`)
	if moved := run(t, s, dan, "define_table", `{"definition": `+rates("Helpdesk")+`}`); at(t, moved, "table.version") != float64(2) {
		t.Fatalf("a table moved to another app answered %v", moved)
	}
	if got := run(t, s, ann, "list_tables", `{"app": "Helpdesk"}`)["tables"]; !reflect.DeepEqual(got, []any{"Rates"}) {
		t.Fatalf("the tables of Helpdesk after the move: %v", got)
	}
	if got := run(t, s, ann, "table_rows", `{"table": "Rates"}`)["rows"].([]any); len(got) != 1 {
		t.Fatalf("the rows after the move: %v", got)
	}
	// A row goes by its key, and the table goes with what is left of it
	// (ADR-0082); a designer removes a table, and nobody else.
	if got := run(t, s, dan, "delete_rows", `{"table": "Rates", "keys": [{"currency": "EUR"}, {"currency": "GBP"}]}`)["deleted"]; got != float64(1) {
		t.Fatalf("delete_rows answered %v", got)
	}
	if e := refused(t, s, ann, "remove_table", `{"name": "Rates"}`); e.Code != store.CodeForbidden {
		t.Fatalf("somebody who is no designer removed a table: %+v", e)
	}
	run(t, s, dan, "remove_table", `{"name": "Rates"}`)
	if got := run(t, s, ann, "list_tables", `{}`)["tables"].([]any); len(got) != 0 {
		t.Fatalf("the tables after the removal: %v", got)
	}
	// Who removed it, and when, is on the version it had (ADR-0083), and
	// anybody reads it, as anybody reads who defined a version.
	versions := run(t, s, ann, "definitions", `{"what": "table", "name": "Rates"}`)["versions"].([]any)
	last, _ := versions[len(versions)-1].(map[string]any)
	if removed, _ := last["removed"].(map[string]any); removed["by"] != "dan" || removed["kind"] != store.ActorPerson || removed["at"] == nil {
		t.Fatalf("definitions of a removed table answered %v", versions)
	}
	for _, v := range versions[:len(versions)-1] {
		if _, removed := v.(map[string]any)["removed"]; removed {
			t.Fatalf("a version that was not removed says it was: %v", versions)
		}
	}
}

func TestEveryCommandIsRun(t *testing.T) {
	if used["create"] == 0 || used["get_class"] == 0 || used["drain"] == 0 {
		t.Skip("the tests that run the commands did not all run")
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
