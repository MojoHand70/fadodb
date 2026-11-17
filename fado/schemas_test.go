package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on schemas and objects is a reference: it says how a
// schema is defined, which schemas the engine brings, how a class holds
// an object, and what a class can and cannot do with one today.
// TestSchemasAndObjects takes the schemas, the class and the values out
// of the chapter, plays its story step by step, and holds every table
// of the chapter to what the engine answers.

// schemaFinding says what the engine finds in a schema definition. No
// call checks a schema without storing it, so the schema is defined.
func schemaFinding(db *fado.DB, definition string) string {
	_, err := db.DefineSchema(context.Background(), []byte(definition), setup)
	return codeOf(err)
}

func TestSchemasAndObjects(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "schemas-and-objects.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var schemas [][]byte
	var class []byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		switch kindOf(m[1]) {
		case "schema":
			schemas = append(schemas, m[1])
		case "class":
			class = m[1]
		}
	}
	if len(schemas) != 2 || class == nil {
		t.Fatal("the chapter shows two schemas and a class")
	}
	var given fado.Values
	if err := json.Unmarshal([]byte(fenced(t, page, "the values Ola gives")), &given); err != nil {
		t.Fatal(err)
	}

	dan := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	ola, marek := person("ola"), person("marek", "dispatch")
	says := func(doc *fado.Document, err error) string {
		if err != nil {
			return told(err)
		}
		return fmt.Sprintf("title %q, city %s, roomy %s", doc.Title, shown(doc.Values["city"]), strings.ToUpper(shown(doc.Values["roomy"])))
	}

	// The story.
	db := notionDB(t, fado.Options{})
	story := taleOf(t, page)
	step := story.step
	// A schema that another one names is defined first.
	if got := codeOf(func() error { _, err := db.DefineSchema(ctx, schemas[1], dan); return err }()); got != "unknown_schema" {
		t.Errorf("a schema defined before the one it names is found %s", got)
	}
	contact, err := db.DefineSchema(ctx, schemas[0], dan)
	if err != nil {
		t.Fatal(err)
	}
	depot, err := db.DefineSchema(ctx, schemas[1], dan)
	if err != nil {
		t.Fatal(err)
	}
	step(1, fmt.Sprintf("defined, version %d and version %d", contact.Version, depot.Version))
	defined, err := db.DefineClass(ctx, class, dan)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := db.Create(ctx, "Delivery", given, ola)
	step(2, says(delivery, err))
	if err != nil {
		t.Fatal(err)
	}
	answered, err := json.MarshalIndent(delivery.Values, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(answered) != fenced(t, page, "the values as they are answered") {
		t.Errorf("the values of the chapter are not what the engine answers:\n%s", answered)
	}
	set := func(values fado.Values, as fado.Option) (*fado.Document, error) {
		return db.Set(ctx, "Delivery", delivery.ID, values, as)
	}
	_, err = set(fado.Values{"depot": map[string]any{"code": "GDA", "floor": 2}}, ola)
	step(3, told(err))
	_, err = set(fado.Values{"depot": map[string]any{"code": "GDA", "capacity": "many"}}, ola)
	step(4, told(err))
	changed, err := set(fado.Values{"depot": map[string]any{"code": "WAW", "address": map[string]any{"city": "Warszawa"}}}, ola)
	step(5, says(changed, err))
	read, err := db.Get(ctx, "Delivery", delivery.ID, ola)
	if err != nil {
		t.Fatal(err)
	}
	held, _ := read.Values["depot"].(map[string]any)
	step(6, shown(held["contact"]))
	if held["capacity"] != nil || held["code"] != "WAW" {
		t.Errorf("after step 5 the depot is %v; the chapter says an object is replaced whole", held)
	}
	_, err = db.Transition(ctx, "Delivery", delivery.ID, "send", nil, ola)
	step(7, told(err))
	_, err = set(fado.Values{"driver": map[string]any{"name": "Jan"}}, ola)
	step(8, told(err))
	_, err = set(fado.Values{"driver": map[string]any{"name": "Jan"}}, marek)
	step(9, told(err))
	_, err = db.Transition(ctx, "Delivery", delivery.ID, "send", nil, ola)
	step(10, told(err))
	found, err := db.Count(ctx, fado.Filter{Class: "Delivery", Where: `depot.address.city = "Warszawa"`}, ola)
	if err != nil {
		t.Fatal(err)
	}
	step(11, fmt.Sprint(found))
	story.done()

	// The history shows the depot as it was written down at each version.
	history, err := db.History(ctx, "Delivery", delivery.ID, ola)
	if err != nil || len(history) < 2 {
		t.Fatal(err)
	}
	if first, _ := history[0].Values["depot"].(map[string]any); first["code"] != "GDA" {
		t.Errorf("the first version holds the depot %v", first)
	}

	// The schemas the engine brings: the fields of each, and its name
	// is taken.
	brought := rows(marked(t, page, "the schemas the engine brings"))
	if len(brought) != 9 {
		t.Errorf("the chapter lists %d schemas, and says nine", len(brought))
	}
	// The engine lists its own schemas first: the chapter has each of
	// them, in that order, and the caller's two follow.
	var names []string
	for _, row := range brought {
		names = append(names, row[0])
	}
	if got := db.Schemas(); !reflect.DeepEqual(got, append(names, "Contact", "Depot")) {
		t.Errorf("the schemas of the database are %v, and the chapter lists %v as the engine's", got, names)
	}
	for i, row := range brought {
		cls, err := db.DefineClass(ctx, []byte(scratch(fmt.Sprintf("Holds%d", i), `"o":{"type":"object","schema":"`+row[0]+`"}`, oneState)), setup)
		if err != nil {
			t.Errorf("a class with an object of the schema %s: %v", row[0], err)
			continue
		}
		var fields []string
		for _, f := range cls.Variables[0].Type.Fields() {
			kind := f.Type.String()
			if name, ok := strings.CutPrefix(kind, "object<"); ok {
				kind = strings.TrimSuffix(name, ">")
			}
			fields = append(fields, f.Name+" "+kind)
		}
		if got := strings.Join(fields, ", "); got != row[1] {
			t.Errorf("the schema %s has the fields %s, and the chapter says %s", row[0], got, row[1])
		}
		if got := schemaFinding(db, `{"name":"`+row[0]+`","fields":{"a":"text"}}`); got != "reserved_name" {
			t.Errorf("a schema of the caller's with the name %s is found %s", row[0], got)
		}
	}
	// The caller's id in a Person.
	if got := finding(db, `{"name":"Mine","variables":{"driver":{"type":"object","schema":"Person"}},"states":{"initial":"a","list":["a"]},`+
		`"access":{"read":"driver.id = USER()","create":"TRUE","update":"driver.id = USER()","delete":"FALSE"}}`); got != "accepted" {
		t.Errorf("a class that asks driver.id = USER() is found %s", got)
	}

	// What a class can do with an object. each says "yes" when the
	// engine accepts every definition, and "no" with the codes it finds
	// when it refuses every one of them.
	two := `"a":{"type":"object","schema":"Contact"},"b":{"type":"object","schema":"Contact"},"note":{"type":"text"}`
	chart := `"states":{"initial":"s","list":["s","t"]},"transitions":[{"name":"go","from":"s","to":"t"}]`
	with := func(variables, more string) string { return scratch("Able", variables, chart+more) }
	each := func(definitions ...string) string {
		var codes []string
		refused := 0
		for _, definition := range definitions {
			got := finding(db, definition)
			if got == "accepted" {
				continue
			}
			refused++
			if !strings.Contains(strings.Join(codes, " "), got) {
				codes = append(codes, got)
			}
		}
		switch refused {
		case 0:
			return "yes"
		case len(definitions):
			return "no, " + strings.Join(codes, ", ")
		}
		return fmt.Sprintf("%d of %d refused: %s", refused, len(definitions), strings.Join(codes, ", "))
	}
	holds := func(what string, got string) func(row []string) {
		return func(row []string) {
			t.Helper()
			if got != row[1] {
				t.Errorf("a class can %s: the engine says %s, and the chapter says %s", what, got, row[1])
			}
		}
	}
	guarded, err := db.DefineClass(ctx, []byte(`{"name":"Guarded","variables":{"a":{"type":"object","schema":"Contact"}},"states":{"initial":"s","list":["s"]},`+
		`"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"},"rights":{"*":{"a":{"read":["hr"]}}}}`), setup)
	if err != nil || len(withoutDescriptions(guarded.Notes)) != 0 {
		t.Fatalf("a class with a read right on an object: %v", err)
	}
	secret, err := db.Create(ctx, "Guarded", fado.Values{"a": map[string]any{"name": "Ewa"}}, ola)
	if err != nil {
		t.Fatal(err)
	}
	forOla, _ := db.Get(ctx, "Guarded", secret.ID, ola)
	forHR, _ := db.Get(ctx, "Guarded", secret.ID, person("hanna", "hr"))
	if _, sees := forOla.Values["a"]; sees || forHR.Values["a"] == nil {
		t.Errorf("a read right on an object: Ola gets %v, and somebody of hr %v", forOla.Values, forHR.Values)
	}
	checks(t, page, "what a class can do with an object", map[string]func(row []string){
		"read one field in a formula, at any depth: depot.address.city": holds("read a field", each(
			string(class),
			with(`"d":{"type":"object","schema":"Depot"},"r":{"formula":"d.contact.email"},"q":{"formula":"d.rent * 2"}`, ""),
			with(`"e":{"type":"object","schema":"Event"},"r":{"formula":"e.place.address.city"}`, ""),
		)),
		"use a field in a title, a guard, a line of access, a right, a rule": holds("use a field", each(
			`{"name":"Able","title":"\"For \" & a.name","variables":{`+two+`},"states":{"initial":"s","list":["s","t"]},`+
				`"transitions":[{"name":"go","from":"s","to":"t","guard":"NOT ISBLANK(a.email)"}],`+
				`"rules":[{"on":"transition","name":"go","if":"NOT ISBLANK(b.name)","do":[{"action":"set","values":{"note":"a.name"}}]}],`+
				`"access":{"read":"a.name = USER()","create":"TRUE","update":"a.name = USER()","delete":"FALSE","transition":{"go":"b.name = USER()"}},`+
				`"rights":{"*":{"note":{"write":"a.name = USER()","read":"b.name = USER()"}}}}`,
		)),
		`find documents by a field: depot.address.city = "Warszawa"`: func(row []string) {
			if found != 1 || row[1] != "yes" {
				t.Errorf("the deliveries found by a field are %d, and the chapter says %s", found, row[1])
			}
			other, err := db.Count(ctx, fado.Filter{Class: "Delivery", Where: `depot.address.city = "Gdańsk"`}, ola)
			if err != nil || other != 0 {
				t.Errorf("the deliveries whose depot is in Gdańsk: %d, %v", other, err)
			}
		},
		"say who may write or see the object as a whole": holds("give rights on an object", each(
			with(two, `,"rights":{"*":{"a":{"write":["dispatch"],"read":["hr"]}}}`),
		)),
		"say who may write or see one field of it": holds("give rights on a field", each(
			with(two, `,"rights":{"*":{"a.name":{"write":"FALSE"}}}`),
			with(two, `,"rights":{"*":{"a.name":{"read":["hr"]}}}`),
		)),
		"name the object in a rule on values_change, or reset it in a rule": holds("name an object in a rule", each(
			with(two, `,"rules":[{"on":"values_change","variables":["a"],"do":[{"action":"emit","event":"contact.changed"}]}]`),
			with(two, `,"rules":[{"on":"transition","name":"go","do":[{"action":"reset","variables":["a"]}]}]`),
		)),
		"set an object, or one field of it, in a rule": holds("set an object in a rule", each(
			with(two, `,"rules":[{"on":"transition","name":"go","do":[{"action":"set","values":{"a":"b"}}]}]`),
			with(two, `,"rules":[{"on":"transition","name":"go","do":[{"action":"set","values":{"a.name":"\"x\""}}]}]`),
		)),
		"test or compare a whole object: ISBLANK(depot), a = b": holds("test a whole object", each(
			with(two+`,"r":{"formula":"ISBLANK(a)"}`, ""),
			with(two+`,"r":{"formula":"a = b"}`, ""),
			`{"name":"Able","variables":{`+two+`},"states":{"initial":"s","list":["s","t"]},`+
				`"transitions":[{"name":"go","from":"s","to":"t","guard":"NOT ISBLANK(a)"}],`+
				`"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`,
		)),
		"compute an object": holds("compute an object", each(
			with(two+`,"r":{"formula":"a"}`, ""),
			with(two+`,"r":{"formula":"IF(ISBLANK(a.name), b, a)"}`, ""),
			with(`"d":{"type":"object","schema":"Depot"},"r":{"formula":"d.contact"}`, ""),
		)),
		"give an object a default": holds("give a default", each(
			with(`"a":{"type":"object","schema":"Contact","default":"BLANK"}`, ""),
		)),
		"keep an index on an object": holds("keep an index", each(
			with(`"a":{"type":"object","schema":"Contact","index":true}`, ""),
		)),
		"read a field of rows in a formula: stops.name": holds("read a field of rows", each(
			with(`"stops":{"type":"rows","schema":"Contact"},"r":{"formula":"COUNT(stops.name)"}`, ""),
		)),
	})

	// required does not hold for an object, a list or rows. When the
	// engine is changed to enforce it, or to refuse the word, this fails
	// and the box in the chapter goes.
	if _, err := db.DefineClass(ctx, []byte(scratch("Wanting",
		`"who":{"type":"object","schema":"Contact","required":true},"tags":{"type":"array<text>","required":true},"lines":{"type":"rows","schema":{"what":"text"},"required":true}`, oneState)), setup); err != nil {
		t.Errorf("required on an object, a list and rows is refused when the class is defined: %v", err)
	} else if _, err := db.Create(ctx, "Wanting", fado.Values{}, ola); err != nil {
		t.Errorf("required on an object, a list or rows is enforced: %v", err)
	}

	// How an object is stored.
	view, err := db.SQL(ctx, "select * from Delivery", nil, setup)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, column := range view.Columns {
		has[column] = true
	}
	named := regexp.MustCompile(`[a-z]+(__[a-z_]+)+`)
	for _, row := range rows(marked(t, page, "the columns")) {
		columns := named.FindAllString(row[1], -1)
		if len(columns) == 0 || columns[0] != strings.ReplaceAll(row[0], ".", "__") {
			t.Errorf("the chapter gives the field %s the columns %v", row[0], columns)
		}
		for _, column := range columns {
			if !has[column] {
				t.Errorf("the view Delivery has no column %s; it has %v", column, view.Columns)
			}
		}
	}
	stops, err := db.SQL(ctx, "select * from Delivery_stops", nil, setup)
	if err != nil || !reflect.DeepEqual(stops.Columns, []string{"doc_id", "version", "idx", "name", "email", "phone"}) {
		t.Errorf("the view of the stops: %v, %v", stops, err)
	}
	for _, v := range defined.Variables {
		if v.Name == "stops" && v.Type.String() != "rows<Contact>" {
			t.Errorf("stops is %s", v.Type)
		}
	}
	// Two underscores in a row part the names of a path, so no name has
	// them.
	if got := schemaFinding(db, `{"name":"A__B","fields":{"a":"text"}}`); got != "bad_name" {
		t.Errorf("a schema with two underscores in its name is found %s", got)
	}
	if got := finding(db, scratch("Named", `"a__b":{"type":"text"}`, oneState)); got != "bad_name" {
		t.Errorf("a variable with two underscores in its name is found %s", got)
	}

	// Changing a schema.
	if again, err := db.DefineSchema(ctx, schemas[0], dan); err != nil || again.Version != 1 {
		t.Errorf("the same definition again: %v, %v", again, err)
	}
	checks(t, page, "changing a schema", map[string]func(row []string){
		"is used by no class": func(row []string) {
			if got := schemaFinding(db, `{"name":"Spare","fields":{"a":"text"}}`); got != "accepted" {
				t.Fatalf("a new schema is found %s", got)
			}
			next, err := db.DefineSchema(ctx, []byte(`{"name":"Spare","fields":{"a":"text","b":"int"}}`), dan)
			if err != nil || next.Version != 2 || row[1] != "is taken, as the next version" {
				t.Errorf("a changed definition of a schema no class uses: %v, %v; the chapter says it %s", next, err, row[1])
			}
			versions, err := db.Definitions(ctx, fado.DefinitionSchema, "Spare")
			if err != nil || len(versions) != 2 || versions[1].By != "dan" || versions[1].At == 0 {
				t.Errorf("the versions of the schema: %v, %v", versions, err)
			}
		},
		"is used by a class": func(row []string) {
			if got := "is refused, " + schemaFinding(db, `{"name":"Contact","fields":{"name":"text"}}`); got != row[1] {
				t.Errorf("a changed definition of a schema in use %s, and the chapter says it %s", got, row[1])
			}
		},
		"is one of the engine's": func(row []string) {
			if got := "is refused, " + schemaFinding(db, `{"name":"Address","fields":{"street":"text"}}`); got != row[1] {
				t.Errorf("a definition under the name of one of the engine's schemas %s, and the chapter says it %s", got, row[1])
			}
		},
	})

	// The words of a schema: extends, and a class under the same name.
	if got := schemaFinding(db, `{"name":"Employee","extends":"Person","fields":{"badge":"text"}}`); got != "accepted" {
		t.Fatalf("a schema that extends another is found %s", got)
	}
	employee, err := db.DefineClass(ctx, []byte(scratch("Staff", `"e":{"type":"object","schema":"employee"}`, oneState)), setup)
	if err != nil {
		t.Fatalf("a class names a schema in another case of letters: %v", err)
	}
	var fields []string
	for _, f := range employee.Variables[0].Type.Fields() {
		fields = append(fields, f.Name)
	}
	if got := strings.Join(fields, " "); got != "id name email phone avatar badge" {
		t.Errorf("a schema that extends Person has the fields %s", got)
	}
	if _, err := db.DefineClass(ctx, []byte(scratch("Contact", `"c":{"type":"text"}`, oneState)), setup); err != nil {
		t.Errorf("a class with the name of a schema: %v", err)
	}

	// The commands: each row by the command it names, on a database of
	// its own.
	db2 := notionDB(t, fado.Options{})
	exec := func(as fado.Principal, name, object string) map[string]json.RawMessage {
		t.Helper()
		got, err := db2.Exec(ctx, as, name, []byte(object))
		if err != nil {
			t.Fatalf("the command %s: %v", name, err)
		}
		var answer map[string]json.RawMessage
		if err := json.Unmarshal(got, &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}
	designer := fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}}
	reader := fado.Principal{ID: "ola", Kind: fado.ActorPerson}
	commands := []struct {
		to, name string
		holds    func() bool
	}{
		{"define a schema", "define_schema", func() bool {
			return exec(designer, "define_schema", `{"definition":`+string(schemas[0])+`}`)["schema"] != nil
		}},
		{"read its definition back", "get_schema", func() bool {
			return strings.Contains(string(exec(reader, "get_schema", `{"name":"Contact"}`)["schema"]), `"subtitle"`)
		}},
		{"list the schemas, the engine's first", "list_schemas", func() bool {
			var got []string
			json.Unmarshal(exec(reader, "list_schemas", `{}`)["schemas"], &got)
			return reflect.DeepEqual(got, append(append([]string{}, names...), "Contact"))
		}},
	}
	listed := map[string]string{}
	for _, row := range rows(marked(t, page, "the commands")) {
		listed[row[0]] = row[1]
	}
	if len(listed) != len(commands) {
		t.Errorf("the chapter lists %d commands, and the test plays %d", len(listed), len(commands))
	}
	for _, c := range commands {
		if listed[c.to] != c.name {
			t.Errorf("to %s the chapter names the command %q, and the test plays %s", c.to, listed[c.to], c.name)
		}
		if !c.holds() {
			t.Errorf("the command %s does not %s", c.name, c.to)
		}
	}

	// What is checked when the schema is defined.
	schema := func(name, fields, more string) string {
		return `{"name":"` + name + `","fields":{` + fields + `}` + more + `}`
	}
	heldBy(t, page, "checked when the schema is defined", map[string][]string{
		"has no name, or a name that is not letters, digits and underscores": {`{"fields":{"a":"text"}}`, schema("my schema", `"a":"text"`, "")},
		"has the name of one of the engine's schemas":                        {schema("Person", `"a":"text"`, "")},
		"has the name of another schema in another case of letters":          {schema("spare", `"a":"text"`, "")},
		"has no fields": {schema("S", "", "")},
		"has two fields whose names differ only in the case of letters": {schema("S", `"a":"text","A":"text"`, "")},
		"has a field of a type the engine does not know":                {schema("S", `"a":"string"`, ""), schema("S", `"a":"object"`, "")},
		"has a json field":                                   {schema("S", `"a":"json"`, "")},
		"has a field that is a list or rows":                 {schema("S", `"a":"array<text>"`, ""), schema("S", `"a":"rows"`, "")},
		"has a field of a schema the database does not have": {schema("S", `"a":"object<Nowhere>"`, "")},
		"contains itself":                                    {schema("S", `"a":"object<S>"`, "")},
		"nests more than four deep":                          {schema("S", `"a":"object<Deep>"`, "")},
		"has a field with a name the engine's tables use: doc_id, version, idx": {
			schema("S", `"doc_id":"text"`, ""), schema("S", `"version":"text"`, ""), schema("S", `"idx":"text"`, ""),
		},
		"has a field whose name contains two underscores in a row, or ends in _currency": {schema("S", `"a__b":"text"`, ""), schema("S", `"a_currency":"text"`, "")},
		"extends a schema the database does not have":                                    {schema("S", `"a":"text"`, `,"extends":"Nowhere"`)},
		"gives a field the name of a field of the schema it extends":                     {schema("S", `"name":"text"`, `,"extends":"Person"`)},
		"has a title or a subtitle that reads a field it does not have, or the caller or the clock": {
			schema("S", `"a":"text"`, `,"presentation":{"title":"b"}`),
			schema("S", `"a":"text"`, `,"presentation":{"subtitle":"USER()"}`),
			schema("S", `"a":"text"`, `,"presentation":{"title":"TEXT(TODAY())"}`),
		},
		"has a title or a subtitle that does not give text": {schema("S", `"a":"int"`, `,"presentation":{"title":"a"}`)},
		"names an avatar that is no field of it":            {schema("S", `"a":"text"`, `,"presentation":{"avatar":"picture"}`)},
		"has a word the engine does not know":               {schema("S", `"a":"text"`, `,"colour":"red"`)},
	}, func(definition string) string {
		// Deep is a schema that nests four deep, the most there is.
		if strings.Contains(definition, "object<Deep>") {
			if got := schemaFinding(db, `{"name":"Deep","fields":{"e":"object<Event>"}}`); got != "accepted" {
				return "the schema that nests four deep is found " + got
			}
		}
		return schemaFinding(db, definition)
	})
	if got := schemaFinding(db, schema("Free", `"id":"text","state":"text","title":"text"`, "")); got != "accepted" {
		t.Errorf("a schema whose fields have the names a variable may not have is found %s", got)
	}

	// What is checked in a class.
	one := func(variables string) string { return scratch("Checked", variables, oneState) }
	heldBy(t, page, "checked when the class is defined", map[string][]string{
		"has an object variable that names no schema":                                            {one(`"a":{"type":"object"}`)},
		"names a schema the database does not have":                                              {one(`"a":{"type":"object","schema":"Nowhere"}`), one(`"a":{"type":"rows","schema":"Nowhere"}`)},
		"writes the fields of an object into the variable, in the place of the name of a schema": {one(`"a":{"type":"object","schema":{"x":"text"}}`)},
		"reads a field the schema does not have":                                                 {one(`"a":{"type":"object","schema":"Contact"},"r":{"formula":"a.fax"}`)},
	}, func(definition string) string { return finding(db, definition) })

	// What is refused, each row by its own call.
	fresh, err := db.Create(ctx, "Delivery", given, ola)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"gives a field the schema does not have": func() error {
			_, err := db.Set(ctx, "Delivery", fresh.ID, fado.Values{"depot": map[string]any{"code": "GDA", "floor": 2}}, ola)
			return err
		},
		"gives a value that does not fit its field": func() error {
			_, err := db.Set(ctx, "Delivery", fresh.ID, fado.Values{"depot": map[string]any{"capacity": "many"}}, ola)
			return err
		},
		"gives something that is not a map of fields": func() error {
			_, err := db.Set(ctx, "Delivery", fresh.ID, fado.Values{"depot": "GDA"}, ola)
			return err
		},
		"names a field as if it were a variable": func() error {
			_, err := db.Set(ctx, "Delivery", fresh.ID, fado.Values{"depot.code": "WAW"}, ola)
			return err
		},
		"defines a schema and is no designer": func() error {
			_, err := db.DefineSchema(ctx, []byte(`{"name":"Mine","fields":{"a":"text"}}`), ola)
			return err
		},
	}
	by := map[string]func(row []string){}
	for what, call := range calls {
		by[what] = func(row []string) {
			var got *fado.Error
			if err := call(); !errors.As(err, &got) {
				t.Errorf("a caller who %s is answered %v", what, err)
				return
			}
			if got.Code != row[1] {
				t.Errorf("a caller who %s: the engine answers %s, and the chapter says %s", what, got.Code, row[1])
			}
			names := row[2]
			if _, example, ok := strings.Cut(names, ", as "); ok {
				names = example
			}
			if place := got.Variable + got.Path; place != names {
				t.Errorf("a caller who %s: the refusal names %q, and the chapter says %s", what, place, names)
			}
		}
	}
	checks(t, page, "what is refused", by)
	if after, _ := db.Get(ctx, "Delivery", fresh.ID, ola); after == nil || after.Version != 1 {
		t.Error("a refused change made a version")
	}
}
