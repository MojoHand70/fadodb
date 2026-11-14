package command

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/store"
)

// maxFields is the most members the object of a command has.
const maxFields = 12

// call is one command while it runs: who sent it, its members as they
// were given, the options made of them and the answer being written.
type call struct {
	ctx  context.Context
	s    *store.Store
	cmd  *Command
	raw  [maxFields][]byte
	opts []store.Option
	out  []byte
}

// given is the value of a member, nil when it was not given.
func (c *call) given(name string) []byte {
	for i := range c.cmd.Fields {
		if c.cmd.Fields[i].Name == name {
			return c.raw[i]
		}
	}
	return nil
}

// Exec runs one command (ADR-0055, item 10): it takes the caller, the
// name of the command and its object, makes the call of the engine the
// command is, and returns the answer, one JSON object.
//
// A command that was refused answers {"error": …}, and the error says
// so to the door that carries the answer: a *store.Error, whose code the
// caller branches on; model.Diagnostics for a definition that was
// refused, whose findings are in the answer; or, for a failure that is
// no refusal, any other error, whose words are for the server's log and
// are not in the answer. Code gives the code of each.
//
// The caller is the door's to know (item 4). The one thing the object
// says about it is for, whom it acts for, which the engine checks.
func Exec(ctx context.Context, s *store.Store, p store.Principal, name string, object []byte) ([]byte, error) {
	cmd, ok := byName[name]
	if !ok {
		return refuse(unknownCommand(name))
	}
	c := &call{ctx: ctx, s: s, cmd: cmd}
	if e := c.read(object); e != nil {
		return refuse(e)
	}
	if e := c.options(p); e != nil {
		return refuse(e)
	}
	c.out = make([]byte, 0, 512)
	if err := cmd.run(c); err != nil {
		return refuse(err)
	}
	return c.out, nil
}

// Code is the code of an error Exec returned, as a door needs it to pick
// a status: the code of a refusal, bad_definition for a definition that
// was refused, and internal for anything else.
func Code(err error) string {
	switch e := err.(type) {
	case nil:
		return ""
	case *store.Error:
		return e.Code
	case model.Diagnostics:
		return CodeBadDefinition
	}
	return CodeInternal
}

// refuse writes the answer of a command that did not run.
func refuse(err error) ([]byte, error) {
	switch e := err.(type) {
	case *store.Error:
		text, _ := json.Marshal(e)
		out := make([]byte, 0, len(text)+12)
		out = append(out, `{"error":`...)
		out = append(out, text...)
		return append(out, '}'), e
	case model.Diagnostics:
		findings := 0
		for _, d := range e {
			if !d.Note {
				findings++
			}
		}
		message := strconv.Itoa(findings) + " findings refuse the definition"
		if findings == 1 {
			message = "1 finding refuses the definition"
		}
		text, _ := json.Marshal(struct {
			Error       *store.Error      `json:"error"`
			Diagnostics model.Diagnostics `json:"diagnostics"`
		}{&store.Error{Code: CodeBadDefinition, Message: message}, e})
		return text, e
	}
	return []byte(`{"error":{"code":"internal","message":"the command failed in the server; its log says how"}}`), err
}

func unknownCommand(name string) *store.Error {
	e := &store.Error{Code: CodeUnknownCommand, Message: "no command is named " + strconv.Quote(name)}
	// A name that is a call of the Go API, or a command in another case,
	// is one edit away.
	lower := strings.ToLower(name)
	for _, c := range commands {
		if c.Name == lower || strings.EqualFold(c.Call, name) || strings.ReplaceAll(c.Name, "_", "") == strings.ReplaceAll(lower, "-", "") {
			e.Hint = "the command is " + c.Name
			return e
		}
	}
	e.Hint = "a command is a call of the Go API in lower case with underscores, such as create, transition or define_class"
	return e
}

// read takes the object apart, member by member.
func (c *call) read(object []byte) *store.Error {
	if !json.Valid(object) {
		return badJSON("", c.cmd.Name+" was sent something that is not JSON", "send one JSON object; {} for a command that takes nothing")
	}
	var seen [maxFields]bool
	isObject, e := members(object, func(name string, value []byte) *store.Error {
		for i := range c.cmd.Fields {
			f := &c.cmd.Fields[i]
			if f.Name != name {
				continue
			}
			if seen[i] {
				return badJSON(pointer(name), name+" is given twice", "")
			}
			seen[i] = true
			if isNull(value) {
				return nil
			}
			if !f.Kind.fits(value) {
				return badJSON(pointer(name), name+" is a "+f.Kind.String()+", got "+short(value), "")
			}
			c.raw[i] = value
			return nil
		}
		return badJSON(pointer(name), c.cmd.Name+" takes no "+strconv.Quote(name), c.cmd.takesText())
	})
	if !isObject {
		return badJSON("", c.cmd.Name+" was sent JSON that is not an object", "send one JSON object; {} for a command that takes nothing")
	}
	if e != nil {
		return e
	}
	for i := range c.cmd.Fields {
		if f := &c.cmd.Fields[i]; f.Required && c.raw[i] == nil {
			return badJSON(pointer(f.Name), c.cmd.Name+" needs "+f.Name+": "+f.Does, c.cmd.takesText())
		}
	}
	return nil
}

// takesText lists the members of the command's object, for a hint.
func (cmd *Command) takesText() string {
	if len(cmd.Fields) == 0 {
		return cmd.Name + " takes nothing: send {}"
	}
	names := make([]string, 0, len(cmd.Fields))
	for i := range cmd.Fields {
		if !cmd.Fields[i].Refused {
			names = append(names, cmd.Fields[i].Name)
		}
	}
	return cmd.Name + " takes " + strings.Join(names, ", ")
}

// short is a value as a refusal quotes it: whole when it is short.
func short(value []byte) string {
	if len(value) <= 40 {
		return string(value)
	}
	return string(value[:37]) + "…"
}

// options makes the options of the call from the caller and the five
// members that mean the same in every command (item 3).
func (c *call) options(p store.Principal) *store.Error {
	if c.cmd.takes == asNobody {
		return nil
	}
	if value := c.given("for"); value != nil {
		p.For = unquote(value)
	}
	c.opts = make([]store.Option, 1, 5)
	c.opts[0] = store.As(p)
	if value := c.given("key"); value != nil {
		c.opts = append(c.opts, store.Key(unquote(value)))
	}
	if value := c.given("business_date"); value != nil {
		d, e := day(value)
		if e != nil {
			return e
		}
		c.opts = append(c.opts, store.BusinessDate(d))
	}
	if value := c.given("reason"); value != nil {
		c.opts = append(c.opts, store.Reason(unquote(value)))
	}
	if value := c.given("at"); value != nil {
		t, e := moment(value)
		if e != nil {
			return e
		}
		c.opts = append(c.opts, store.At(t))
	}
	return nil
}

// The answers. A member is written under its name; a list that is empty
// is written [], never null.

func (c *call) open(member string) {
	c.out = append(c.out, '{', '"')
	c.out = append(c.out, member...)
	c.out = append(c.out, '"', ':')
}

func (c *call) next(member string) {
	c.out = append(c.out, ',', '"')
	c.out = append(c.out, member...)
	c.out = append(c.out, '"', ':')
}

func (c *call) close() { c.out = append(c.out, '}') }

// document answers {"document": …}.
func (c *call) document(d *store.Document, err error) error {
	if err != nil {
		return err
	}
	c.open("document")
	if c.out, err = appendDocument(c.out, d); err != nil {
		return err
	}
	c.close()
	return nil
}

// answerDocuments answers {"documents": […]}.
func (c *call) answerDocuments(docs []*store.Document, err error) error {
	if err != nil {
		return err
	}
	c.open("documents")
	c.out = append(c.out, '[')
	for i, d := range docs {
		if i > 0 {
			c.out = append(c.out, ',')
		}
		if c.out, err = appendDocument(c.out, d); err != nil {
			return err
		}
	}
	c.out = append(c.out, ']')
	c.close()
	return nil
}

// outcomes answers the outcomes of a batch, each with its id and the
// document or the refusal.
func (c *call) outcomes(moves []store.MoveOutcome, err error) error {
	if err != nil {
		return err
	}
	c.open("outcomes")
	c.out = append(c.out, '[')
	for i := range moves {
		if i > 0 {
			c.out = append(c.out, ',')
		}
		m := &moves[i]
		c.out = append(c.out, `{"id":`...)
		c.out = appendString(c.out, m.ID)
		if m.Document != nil {
			c.out = append(c.out, `,"document":`...)
			if c.out, err = appendDocument(c.out, m.Document); err != nil {
				return err
			}
		}
		if m.Refusal != nil {
			text, _ := json.Marshal(m.Refusal)
			c.out = append(c.out, `,"refusal":`...)
			c.out = append(c.out, text...)
		}
		c.out = append(c.out, '}')
	}
	c.out = append(c.out, ']')
	c.close()
	return nil
}

// answer writes {"member": value} through encoding/json.
func (c *call) answer(member string, value any, err error) error {
	if err != nil {
		return err
	}
	text, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.open(member)
	c.out = append(c.out, text...)
	c.close()
	return nil
}

// empty answers {}.
func (c *call) empty(err error) error {
	if err != nil {
		return err
	}
	c.out = append(c.out, '{', '}')
	return nil
}

// list is a list as an answer writes it: [] when it has nothing.
func list[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// Documents.

func runCreate(c *call) error {
	values, e := c.values("values")
	if e != nil {
		return e
	}
	if id := c.given("id"); id != nil {
		c.opts = append(c.opts, store.WithID(unquote(id)))
	}
	return c.document(c.s.Create(c.ctx, c.text("class"), values, c.opts...))
}

func runCreateMany(c *call) error {
	docs, e := c.documentValues("documents")
	if e != nil {
		return e
	}
	return c.answerDocuments(c.s.CreateMany(c.ctx, c.text("class"), docs, c.opts...))
}

func runGet(c *call) error {
	return c.document(c.s.Get(c.ctx, c.text("class"), c.text("id"), c.opts...))
}

func runSet(c *call) error {
	values, e := c.values("values")
	if e != nil {
		return e
	}
	return c.document(c.s.Set(c.ctx, c.text("class"), c.text("id"), values, c.opts...))
}

func runReset(c *call) error {
	variables, e := c.texts("variables")
	if e != nil {
		return e
	}
	return c.document(c.s.Reset(c.ctx, c.text("class"), c.text("id"), variables, c.opts...))
}

func runRefresh(c *call) error {
	variables, e := c.texts("variables")
	if e != nil {
		return e
	}
	return c.document(c.s.Refresh(c.ctx, c.text("class"), c.text("id"), variables, c.opts...))
}

func runTransition(c *call) error {
	values, e := c.values("values")
	if e != nil {
		return e
	}
	return c.document(c.s.Transition(c.ctx, c.text("class"), c.text("id"), c.text("transition"), values, c.opts...))
}

func runTransitionMany(c *call) error {
	ids, values, e := c.batch()
	if e != nil {
		return e
	}
	return c.outcomes(c.s.TransitionMany(c.ctx, c.text("class"), c.text("transition"), ids, values, c.opts...))
}

func runPreviewMany(c *call) error {
	ids, values, e := c.batch()
	if e != nil {
		return e
	}
	return c.outcomes(c.s.PreviewMany(c.ctx, c.text("class"), c.text("transition"), ids, values, c.opts...))
}

func (c *call) batch() ([]string, store.Values, *store.Error) {
	ids, e := c.texts("ids")
	if e != nil {
		return nil, nil, e
	}
	values, e := c.values("values")
	return ids, values, e
}

func runDelete(c *call) error {
	return c.empty(c.s.Delete(c.ctx, c.text("class"), c.text("id"), c.opts...))
}

func runTransitions(c *call) error {
	infos, err := c.s.Transitions(c.ctx, c.text("class"), c.text("id"), c.opts...)
	return c.answer("transitions", list(infos), err)
}

func runDue(c *call) error {
	due, err := c.s.Due(c.ctx, c.text("class"), c.opts...)
	return c.answer("due", list(due), err)
}

func runHistory(c *call) error {
	rows, err := c.s.History(c.ctx, c.text("class"), c.text("id"), c.opts...)
	return c.answer("versions", list(rows), err)
}

func runVersion(c *call) error {
	version, e := c.integer("version")
	if e != nil {
		return e
	}
	row, err := c.s.Version(c.ctx, c.text("class"), c.text("id"), version, c.opts...)
	return c.answer("version", row, err)
}

func runEvents(c *call) error {
	events, err := c.s.Events(c.ctx, c.text("class"), c.text("id"), c.opts...)
	return c.answer("events", list(events), err)
}

func runPreview(c *call) error {
	values, e := c.values("values")
	if e != nil {
		return e
	}
	pv, err := c.s.Preview(c.ctx, c.text("class"), c.text("id"), values, c.text("transition"), c.opts...)
	return c.answer("preview", pv, err)
}

func runPreviewDelete(c *call) error {
	pv, err := c.s.PreviewDelete(c.ctx, c.text("class"), c.text("id"), c.opts...)
	return c.answer("preview", pv, err)
}

func (c *call) filter() (store.Filter, *store.Error) {
	f := store.Filter{Class: c.text("class"), Where: c.text("where")}
	var e *store.Error
	if f.Order, e = c.texts("order"); e != nil {
		return f, e
	}
	if f.Fields, e = c.texts("fields"); e != nil {
		return f, e
	}
	limit, e := c.integer("limit")
	if e != nil {
		return f, e
	}
	offset, e := c.integer("offset")
	f.Limit, f.Offset = int(limit), int(offset)
	return f, e
}

func runQuery(c *call) error {
	f, e := c.filter()
	if e != nil {
		return e
	}
	return c.answerDocuments(c.s.Query(c.ctx, f, c.opts...))
}

func runCount(c *call) error {
	n, err := c.s.Count(c.ctx, store.Filter{Class: c.text("class"), Where: c.text("where")}, c.opts...)
	return c.answer("count", n, err)
}

func runSQL(c *call) error {
	args, e := c.scalars("args")
	if e != nil {
		return e
	}
	rows, err := c.s.SQL(c.ctx, c.text("statement"), args, c.opts...)
	if err != nil {
		return err
	}
	columns, _ := json.Marshal(list(rows.Columns))
	data, err := json.Marshal(list(rows.Rows))
	if err != nil {
		return err
	}
	c.open("columns")
	c.out = append(c.out, columns...)
	c.next("rows")
	c.out = append(c.out, data...)
	c.close()
	return nil
}

// Definitions.

// stored answers a definition as it was given, under the member.
func (c *call) stored(member, what, name string, version int, err error) error {
	if err != nil {
		return err
	}
	def, err := c.s.Definition(c.ctx, what, name, version)
	return c.answer(member, def, err)
}

func runDefineClass(c *call) error {
	cls, err := c.s.DefineClass(c.ctx, c.given("definition"), c.opts...)
	if err != nil {
		return err
	}
	return c.stored("class", store.DefinitionClass, cls.Name, cls.Version, nil)
}

func runDefineClasses(c *call) error {
	definitions, e := c.definitions("definitions")
	if e != nil {
		return e
	}
	classes, err := c.s.DefineClasses(c.ctx, definitions, c.opts...)
	if err != nil {
		return err
	}
	out := make([]*store.StoredDefinition, len(classes))
	for i, cls := range classes {
		if out[i], err = c.s.Definition(c.ctx, store.DefinitionClass, cls.Name, cls.Version); err != nil {
			return err
		}
	}
	return c.answer("classes", out, nil)
}

func runDefineSchema(c *call) error {
	sch, err := c.s.DefineSchema(c.ctx, c.given("definition"), c.opts...)
	if err != nil {
		return err
	}
	return c.stored("schema", store.DefinitionSchema, sch.Name, sch.Version, nil)
}

func runDefineTable(c *call) error {
	t, err := c.s.DefineTable(c.ctx, c.given("definition"), c.opts...)
	if err != nil {
		return err
	}
	return c.stored("table", store.DefinitionTable, t.Name, t.Version, nil)
}

func runDefineAction(c *call) error {
	a, err := c.s.DefineAction(c.ctx, c.given("definition"), c.opts...)
	if err != nil {
		return err
	}
	return c.stored("action", store.DefinitionAction, a.Name, a.Version, nil)
}

// diagnostics answers the findings of a validation, [] when there are
// none: a validation that found something ran, and is no refusal.
func (c *call) diagnostics(found model.Diagnostics) error {
	if found == nil {
		found = model.Diagnostics{}
	}
	return c.answer("diagnostics", found, nil)
}

func runValidateClass(c *call) error { return c.diagnostics(c.s.Validate(c.given("definition"))) }

func runValidateClasses(c *call) error {
	definitions, e := c.definitions("definitions")
	if e != nil {
		return e
	}
	return c.diagnostics(c.s.ValidateClasses(definitions))
}

func runValidateAction(c *call) error {
	return c.diagnostics(c.s.ValidateAction(c.given("definition")))
}

func runGetClass(c *call) error {
	version, e := c.integer("version")
	if e != nil {
		return e
	}
	return c.stored("class", store.DefinitionClass, c.text("name"), int(version), nil)
}

func runGetSchema(c *call) error {
	return c.stored("schema", store.DefinitionSchema, c.text("name"), 0, nil)
}

func runGetTable(c *call) error {
	return c.stored("table", store.DefinitionTable, c.text("name"), 0, nil)
}

func runGetAction(c *call) error {
	version, e := c.integer("version")
	if e != nil {
		return e
	}
	return c.stored("action", store.DefinitionAction, c.text("name"), int(version), nil)
}

func runListClasses(c *call) error { return c.answer("classes", list(c.s.Classes()), nil) }
func runListSchemas(c *call) error { return c.answer("schemas", list(c.s.SchemaNames()), nil) }
func runListTables(c *call) error  { return c.answer("tables", list(c.s.Tables()), nil) }
func runListActions(c *call) error { return c.answer("actions", list(c.s.Actions()), nil) }

func runDefinitions(c *call) error {
	versions, err := c.s.Definitions(c.ctx, c.text("what"), c.text("name"))
	return c.answer("versions", list(versions), err)
}

func runPreviewDraft(c *call) error {
	values, e := c.values("values")
	if e != nil {
		return e
	}
	pv, err := c.s.PreviewDraft(c.ctx, c.given("definition"), c.text("id"), values, c.text("transition"), c.opts...)
	return c.answer("preview", pv, err)
}

func runMigrate(c *call) error {
	n, err := c.s.Migrate(c.ctx, c.text("class"), c.opts...)
	return c.answer("migrated", n, err)
}

// Tables, actions and the queue.

func runTableRows(c *call) error {
	rows, err := c.s.TableRows(c.ctx, c.text("table"), c.opts...)
	return c.answer("rows", list(rows), err)
}

func runImportRows(c *call) error {
	rows, e := c.objects("rows")
	if e != nil {
		return e
	}
	if c.flag("replace") {
		c.opts = append(c.opts, store.Replace())
	}
	n, err := c.s.ImportRows(c.ctx, c.text("table"), rows, c.opts...)
	return c.answer("imported", n, err)
}

func runPreviewAction(c *call) error {
	args, e := c.values("args")
	if e != nil {
		return e
	}
	pv, err := c.s.PreviewAction(c.ctx, c.text("action"), args, c.opts...)
	return c.answer("preview", pv, err)
}

func runTryAction(c *call) error {
	args, e := c.values("args")
	if e != nil {
		return e
	}
	outcome, err := c.s.TryAction(c.ctx, c.text("action"), args, c.opts...)
	return c.answer("outcome", outcome, err)
}

func runOutbox(c *call) error {
	limit, e := c.integer("limit")
	if e != nil {
		return e
	}
	rows, err := c.s.Outbox(c.ctx, store.OutboxFilter{Status: c.text("status"), Class: c.text("class"), DocID: c.text("id"), Limit: int(limit)}, c.opts...)
	return c.answer("actions", list(rows), err)
}

func runRetryAction(c *call) error {
	return c.empty(c.s.RetryAction(c.ctx, c.text("action_key"), c.opts...))
}

func runDrain(c *call) error {
	n, err := c.s.Drain(c.ctx, c.opts...)
	return c.answer("attempted", n, err)
}

func runCheckBlobs(c *call) error {
	mismatched, checked, err := c.s.CheckBlobs(c.ctx, c.opts...)
	if err != nil {
		return err
	}
	text, _ := json.Marshal(list(mismatched))
	c.open("checked")
	c.out = strconv.AppendInt(c.out, int64(checked), 10)
	c.next("mismatched")
	c.out = append(c.out, text...)
	c.close()
	return nil
}
