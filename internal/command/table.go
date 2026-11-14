package command

// The table of the commands (ADR-0055, item 2 and item 10): for each its
// name, what it does, its mark, the call of the Go API it is, the
// members of its object and the members of its answer. It is data, so
// that the HTTP addresses, the description for other languages, the help
// of the command-line tool and the tools of an agent's door all come
// from one place.

// Mark says what a command does to the database.
type Mark byte

const (
	// Reads marks a command that writes nothing.
	Reads Mark = 'R'
	// Writes marks a command that changes the database.
	Writes Mark = 'W'
	// Destroys marks a command that removes something.
	Destroys Mark = 'D'
)

// Kind is the JSON type of a member of a command's object.
type Kind int

const (
	// Text is a JSON string.
	Text Kind = iota
	// Integer is a whole number.
	Integer
	// Flag is true or false.
	Flag
	// Object is an object of values by name, in the wire shape of a
	// document (ADR-0018).
	Object
	// Texts is a list of texts.
	Texts
	// Objects is a list of objects.
	Objects
	// Scalars is a list of texts, numbers, true, false and null.
	Scalars
	// JSON is a definition: a JSON object carried as it is.
	JSON
	// JSONs is a list of definitions.
	JSONs
	// Moment is a day, a day and a time in UTC, or seconds since the
	// epoch.
	Moment
)

// String names the kind as the descriptions of the commands write it.
func (k Kind) String() string {
	switch k {
	case Text:
		return "text"
	case Integer:
		return "whole number"
	case Flag:
		return "true or false"
	case Object:
		return "object"
	case Texts:
		return "list of texts"
	case Objects:
		return "list of objects"
	case Scalars:
		return "list of values"
	case JSON:
		return "definition"
	case JSONs:
		return "list of definitions"
	case Moment:
		return "moment"
	}
	return "?"
}

// Field is one member of a command's object.
type Field struct {
	Name     string
	Kind     Kind
	Required bool
	// Does says what the member is, in a sentence.
	Does string
	// Option says the member is one of the five that mean the same in
	// every command that takes them (ADR-0055, item 3).
	Option bool
	// Refused says the command knows the member only so that the engine
	// can refuse it in its own words: at on a command that writes, which
	// is told to preview at that moment instead. A description of the
	// command leaves such a member out.
	Refused bool
}

// Member is one member of a command's answer.
type Member struct {
	Name string
	Does string
}

// Command is one row of the table.
type Command struct {
	Name string
	// Group is the part of the table the command is in: Documents,
	// Definitions or Operations.
	Group string
	Mark  Mark
	// Call is the method of fado.DB the command is.
	Call string
	// Does says what the command does, in a sentence.
	Does string
	// Fields are the members of the object, the command's own first and
	// then the options it takes.
	Fields []Field
	// Answer are the members of the answer; none for a command that
	// answers {}.
	Answer []Member

	run func(*call) error
	// takes says which of the five options the command takes.
	takes options
}

// options says which of the five options a command takes: the ones its
// call of the Go API acts on, and no other, so that a member that would
// change nothing is refused and not passed over.
type options int

const (
	takesKey options = 1 << iota
	takesBusinessDate
	takesReason
	takesAt
	takesFor
	// refusesAt and refusesKey: the engine refuses the option on this
	// command in words of its own, which say what to do instead. The
	// command knows the member so that those words are the answer.
	refusesAt
	refusesKey

	// asReader is a command that reads documents or rows.
	asReader = takesAt | takesFor
	// asCommitter is a command that commits a document.
	asCommitter = takesKey | takesBusinessDate | takesReason | takesFor | refusesAt
	// asBatch is a batch of commits that takes no key.
	asBatch = takesBusinessDate | takesReason | takesFor | refusesAt | refusesKey
	// asPreviewer is a preview of a commit.
	asPreviewer = takesAt | takesReason | takesFor
	// asWriter is a command that writes what is not one document's.
	asWriter = takesFor | refusesAt
	// asCaller is a command that needs to know who calls and no more.
	asCaller = takesFor
	// asNobody takes no option: its call takes no caller, since reading
	// a definition is open to everybody (ADR-0051, item 4).
	asNobody options = 0
)

// The five members that mean the same in every command that takes them.
var (
	optionKey          = Field{Name: "key", Kind: Text, Option: true, Does: "the idempotency key: the same command sent again with the same key makes no second commit and is answered as the first was"}
	optionBusinessDate = Field{Name: "business_date", Kind: Text, Option: true, Does: `the business day of the commit, as "2026-11-13"; the server's day when left out`}
	optionReason       = Field{Name: "reason", Kind: Text, Option: true, Does: "why a content administrator acts past a rule of the class; kept on the event"}
	optionAt           = Field{Name: "at", Kind: Moment, Option: true, Does: `the moment the command reads at: "2026-11-13", "2026-11-13 09:30:00" in UTC, or seconds since the epoch; now when left out`}
	optionFor          = Field{Name: "for", Kind: Text, Option: true, Does: "the id of whoever the caller acts for; let through only while a substitution says so"}
)

func req(name string, kind Kind, does string) Field {
	return Field{Name: name, Kind: kind, Required: true, Does: does}
}

func opt(name string, kind Kind, does string) Field {
	return Field{Name: name, Kind: kind, Does: does}
}

func answer(pairs ...string) []Member {
	out := make([]Member, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Member{Name: pairs[i], Does: pairs[i+1]})
	}
	return out
}

// The parts of the table.
const (
	Documents   = "Documents"
	Definitions = "Definitions"
	Operations  = "Tables, actions and the queue"
)

// Words the table says many times.
const (
	theClass      = "the name of the class"
	theID         = "the id of the document"
	theValues     = "the values to assign, by variable"
	theDocument   = "the document after the command"
	theDefinition = "the definition, as JSON"
	thePreview    = "what the command would do: the document as it would be, the errors, the states visited, the transitions then open and the rules that would run"
	theStored     = "the definition as it was given, with its name, its version and who defined it"
)

var commands = []*Command{
	// Documents.
	{Group: Documents, Name: "create", Mark: Writes, Call: "Create", Does: "Makes a document.",
		Fields: []Field{req("class", Text, theClass), opt("values", Object, theValues),
			opt("id", Text, "the id of the new document; generated when left out")},
		Answer: answer("document", "the new document"), takes: asCommitter, run: runCreate},
	{Group: Documents, Name: "create_many", Mark: Writes, Call: "CreateMany", Does: "Makes several documents of one class in one transaction, all or none.",
		Fields: []Field{req("class", Text, theClass), req("documents", Objects, `the documents, each {"values": {…}}`)},
		Answer: answer("documents", "the new documents, in the order given"), takes: asBatch, run: runCreateMany},
	{Group: Documents, Name: "get", Mark: Reads, Call: "Get", Does: "Reads a document.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID)},
		Answer: answer("document", "the document"), takes: asReader, run: runGet},
	{Group: Documents, Name: "set", Mark: Writes, Call: "Set", Does: "Assigns values to a document and recalculates what depends on them.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID), opt("values", Object, theValues)},
		Answer: answer("document", theDocument), takes: asCommitter, run: runSet},
	{Group: Documents, Name: "reset", Mark: Writes, Call: "Reset", Does: "Puts input variables of a document back to their defaults.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID), opt("variables", Texts, "the variables to reset")},
		Answer: answer("document", theDocument), takes: asCommitter, run: runReset},
	{Group: Documents, Name: "refresh", Mark: Writes, Call: "Refresh", Does: "Recomputes triggered variables of a document, and lets it move if a guard on time now holds.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID), opt("variables", Texts, "the variables to recompute; every one that lists refresh when left out")},
		Answer: answer("document", theDocument), takes: asCommitter, run: runRefresh},
	{Group: Documents, Name: "transition", Mark: Writes, Call: "Transition", Does: "Moves a document along a named transition of its class.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID), req("transition", Text, "the name of the transition"),
			opt("values", Object, "values to assign in the same commit, by variable")},
		Answer: answer("document", theDocument), takes: asCommitter, run: runTransition},
	{Group: Documents, Name: "transition_many", Mark: Writes, Call: "TransitionMany", Does: "Moves several documents of one class along one transition; a document that cannot move is reported and the others move.",
		Fields: []Field{req("class", Text, theClass), req("transition", Text, "the name of the transition"), req("ids", Texts, "the ids of the documents, in order"),
			opt("values", Object, "values to assign to each document in its commit, by variable")},
		Answer: answer("outcomes", "one for each id, in the order given: the id, and the document after its move or the refusal"), takes: asCommitter, run: runTransitionMany},
	{Group: Documents, Name: "delete", Mark: Destroys, Call: "Delete", Does: "Removes a document; its history and its events stay.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID)}, takes: asCommitter, run: runDelete},
	{Group: Documents, Name: "transitions", Mark: Reads, Call: "Transitions", Does: "Lists the transitions leaving a document's state, each with whether the caller may take it now.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID)},
		Answer: answer("transitions", "the transitions, in the order of the class"), takes: asReader, run: runTransitions},
	{Group: Documents, Name: "due", Mark: Reads, Call: "Due", Does: "Lists the documents of a class that a commit would move now: a guard on time holds, or a timer has come.",
		Fields: []Field{req("class", Text, theClass)},
		Answer: answer("due", "the documents, each with its state and the state it would move to"), takes: asReader, run: runDue},
	{Group: Documents, Name: "history", Mark: Reads, Call: "History", Does: "Reads every committed version of a document, oldest first.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID)},
		Answer: answer("versions", "the versions, each the document as it was, when it was current and who committed it"), takes: asReader, run: runHistory},
	{Group: Documents, Name: "version", Mark: Reads, Call: "Version", Does: "Reads one committed version of a document.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID), req("version", Integer, "the version to read")},
		Answer: answer("version", "the document as it was at that version, when it was current and who committed it"), takes: asReader, run: runVersion},
	{Group: Documents, Name: "events", Mark: Reads, Call: "Events", Does: "Reads the events that touched a document, oldest first.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID)},
		Answer: answer("events", "the events"), takes: asReader, run: runEvents},
	{Group: Documents, Name: "preview", Mark: Reads, Call: "Preview", Does: "Runs a create, a set or a transition without writing anything.",
		Fields: []Field{req("class", Text, theClass), opt("id", Text, "the id of the document; left out, the preview is of a create"),
			opt("values", Object, theValues), opt("transition", Text, "the name of a transition to take")},
		Answer: answer("preview", thePreview), takes: asPreviewer, run: runPreview},
	{Group: Documents, Name: "preview_delete", Mark: Reads, Call: "PreviewDelete", Does: "Answers what a delete of a document would do, without deleting it.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID)},
		Answer: answer("preview", thePreview), takes: asPreviewer, run: runPreviewDelete},
	{Group: Documents, Name: "preview_many", Mark: Reads, Call: "PreviewMany", Does: "Runs the batch transition_many would run, without writing anything.",
		Fields: []Field{req("class", Text, theClass), req("transition", Text, "the name of the transition"), req("ids", Texts, "the ids of the documents, in order"),
			opt("values", Object, "values to assign to each document in its commit, by variable")},
		Answer: answer("outcomes", "one for each id, in the order given: the id, and the document as it would be or the refusal"), takes: asPreviewer, run: runPreviewMany},
	{Group: Documents, Name: "query", Mark: Reads, Call: "Query", Does: "Reads the documents of a class that match a filter.",
		Fields: []Field{req("class", Text, theClass), opt("where", Text, "a formula over the variables of the class; every document when left out"),
			opt("order", Texts, `the variables to sort by, a leading "-" for descending; by id when left out`),
			opt("limit", Integer, "the most documents to answer"), opt("offset", Integer, "how many documents to skip"),
			opt("fields", Texts, "the variables to answer; every one when left out")},
		Answer: answer("documents", "the documents, in the order asked"), takes: asReader, run: runQuery},
	{Group: Documents, Name: "count", Mark: Reads, Call: "Count", Does: "Counts the documents of a class that match a filter.",
		Fields: []Field{req("class", Text, theClass), opt("where", Text, "a formula over the variables of the class; every document when left out")},
		Answer: answer("count", "how many documents match"), takes: asReader, run: runCount},
	{Group: Documents, Name: "sql", Mark: Reads, Call: "SQL", Does: "Runs one read-only SELECT over the views, for an analyst.",
		Fields: []Field{req("statement", Text, "the SELECT, with ? for each argument"), opt("args", Scalars, "the arguments, in order")},
		Answer: answer("columns", "the names of the columns", "rows", "the rows, each a list of values in the order of the columns"), takes: asCaller, run: runSQL},

	// Definitions.
	{Group: Definitions, Name: "define_class", Mark: Writes, Call: "DefineClass", Does: "Defines a class, or the next version of one.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("class", theStored+", and the notes of the class"), takes: asCaller, run: runDefineClass},
	{Group: Definitions, Name: "define_classes", Mark: Writes, Call: "DefineClasses", Does: "Defines several classes in one call, all or none: how classes that name each other are defined.",
		Fields: []Field{req("definitions", JSONs, "the definitions, as JSON")},
		Answer: answer("classes", "for each class, in the order given, "+theStored), takes: asCaller, run: runDefineClasses},
	{Group: Definitions, Name: "define_schema", Mark: Writes, Call: "DefineSchema", Does: "Defines a named schema of an object.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("schema", theStored), takes: asCaller, run: runDefineSchema},
	{Group: Definitions, Name: "define_table", Mark: Writes, Call: "DefineTable", Does: "Defines a table of reference data.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("table", theStored), takes: asCaller, run: runDefineTable},
	{Group: Definitions, Name: "define_action", Mark: Writes, Call: "DefineAction", Does: "Defines an action a rule may name.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("action", theStored), takes: asCaller, run: runDefineAction},
	{Group: Definitions, Name: "validate_class", Mark: Reads, Call: "ValidateClass", Does: "Reports what define_class would refuse, storing nothing.", takes: asNobody,
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("diagnostics", "the findings; an empty list when nothing is wrong"), run: runValidateClass},
	{Group: Definitions, Name: "validate_classes", Mark: Reads, Call: "ValidateClasses", Does: "Reports what define_classes would refuse, storing nothing.", takes: asNobody,
		Fields: []Field{req("definitions", JSONs, "the definitions, as JSON")},
		Answer: answer("diagnostics", "the findings; an empty list when nothing is wrong"), run: runValidateClasses},
	{Group: Definitions, Name: "validate_action", Mark: Reads, Call: "ValidateAction", Does: "Reports what define_action would refuse, storing nothing.", takes: asNobody,
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("diagnostics", "the findings; an empty list when nothing is wrong"), run: runValidateAction},
	{Group: Definitions, Name: "get_class", Mark: Reads, Call: "Definition", Does: "Reads a class back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, theClass), opt("version", Integer, "an earlier version; the current one when left out")},
		Answer: answer("class", theStored+", and the notes of the class"), run: runGetClass},
	{Group: Definitions, Name: "get_schema", Mark: Reads, Call: "Definition", Does: "Reads a schema back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the schema")},
		Answer: answer("schema", theStored), run: runGetSchema},
	{Group: Definitions, Name: "get_table", Mark: Reads, Call: "Definition", Does: "Reads a table's definition back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the table")},
		Answer: answer("table", theStored), run: runGetTable},
	{Group: Definitions, Name: "get_action", Mark: Reads, Call: "Definition", Does: "Reads an action's definition back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the action"), opt("version", Integer, "an earlier version; the current one when left out")},
		Answer: answer("action", theStored), run: runGetAction},
	{Group: Definitions, Name: "list_classes", Mark: Reads, Call: "Classes", Does: "Lists the classes.", takes: asNobody,
		Answer: answer("classes", "the names, in the order they were defined"), run: runListClasses},
	{Group: Definitions, Name: "list_schemas", Mark: Reads, Call: "Schemas", Does: "Lists the schemas.", takes: asNobody,
		Answer: answer("schemas", "the names, the engine's own first"), run: runListSchemas},
	{Group: Definitions, Name: "list_tables", Mark: Reads, Call: "Tables", Does: "Lists the tables.", takes: asNobody,
		Answer: answer("tables", "the names, in the order they were defined"), run: runListTables},
	{Group: Definitions, Name: "list_actions", Mark: Reads, Call: "Actions", Does: "Lists every action a rule may name, the engine's own first.", takes: asNobody,
		Answer: answer("actions", "the actions, each with its parameters, what it returns and whether this server can run it"), run: runListActions},
	{Group: Definitions, Name: "definitions", Mark: Reads, Call: "Definitions", Does: "Lists the versions of one class, schema, table or action, with who defined each and when.", takes: asNobody,
		Fields: []Field{req("what", Text, "class, schema, table or action"), req("name", Text, "its name")},
		Answer: answer("versions", "the versions, oldest first"), run: runDefinitions},
	{Group: Definitions, Name: "preview_draft", Mark: Reads, Call: "PreviewDraft", Does: "Tries a class definition that is not stored on a create, or on a document of the stored class of the same name, writing nothing.",
		Fields: []Field{req("definition", JSON, "the draft, as JSON"), opt("id", Text, "the id of a document of the stored class; left out, the preview is of a create"),
			opt("values", Object, theValues), opt("transition", Text, "the name of a transition to take")},
		Answer: answer("preview", thePreview), takes: asPreviewer, run: runPreviewDraft},
	{Group: Definitions, Name: "migrate", Mark: Writes, Call: "Migrate", Does: "Brings every live document of a class that an earlier version computed to the current version.",
		Fields: []Field{req("class", Text, theClass)},
		Answer: answer("migrated", "how many documents it moved"), takes: asWriter | refusesKey, run: runMigrate},

	// Tables, actions and the queue.
	{Group: Operations, Name: "table_rows", Mark: Reads, Call: "TableRows", Does: "Reads every row of a table, in the order of its key.",
		Fields: []Field{req("table", Text, "the name of the table")},
		Answer: answer("rows", "the rows, each an object of values by column"), takes: asReader, run: runTableRows},
	{Group: Operations, Name: "import_rows", Mark: Writes, Call: "ImportRows", Does: "Writes rows into a table; a row whose key exists is replaced.",
		Fields: []Field{req("table", Text, "the name of the table"), req("rows", Objects, "the rows, each an object of values by column"),
			opt("replace", Flag, "true empties the table first, so that the rows given are all its rows")},
		Answer: answer("imported", "how many rows it wrote"), takes: asWriter, run: runImportRows},
	{Group: Operations, Name: "preview_action", Mark: Reads, Call: "PreviewAction", Does: "Answers what an action would be called with, or the request it would send, with every secret masked; nothing is called or sent.",
		Fields: []Field{req("action", Text, "the name of the action"), opt("args", Object, "its arguments, by parameter")},
		Answer: answer("preview", "the call, or the request"), takes: asCaller, run: runPreviewAction},
	{Group: Operations, Name: "try_action", Mark: Writes, Call: "TryAction", Does: "Runs an action once, outside any document; a request is sent for real.",
		Fields: []Field{req("action", Text, "the name of the action"), opt("args", Object, "its arguments, by parameter")},
		Answer: answer("outcome", "what the action returned, or how it failed"), takes: asCaller, run: runTryAction},
	{Group: Operations, Name: "outbox", Mark: Reads, Call: "Outbox", Does: "Lists the actions commits queued: what waits, what ran and what is dead.",
		Fields: []Field{opt("status", Text, "pending, done or dead; every status when left out"), opt("class", Text, "only the actions of documents of this class"),
			opt("id", Text, "only the actions of this document"), opt("limit", Integer, "the most actions to answer")},
		Answer: answer("actions", "the actions, in the order they were queued"), takes: asCaller, run: runOutbox},
	{Group: Operations, Name: "retry_action", Mark: Writes, Call: "RetryAction", Does: "Makes a dead action pending again.",
		Fields: []Field{req("action_key", Text, "the key of the queued action, as outbox lists it")}, takes: asCaller, run: runRetryAction},
	{Group: Operations, Name: "drain", Mark: Writes, Call: "Drain", Does: "Runs every queued action that is due, now.",
		Answer: answer("attempted", "how many actions it attempted"), takes: asCaller, run: runDrain},
	{Group: Operations, Name: "check_blobs", Mark: Reads, Call: "CheckBlobs", Does: "Recomputes the hash of every file the database holds.",
		Answer: answer("checked", "how many files it checked", "mismatched", "the hashes of the files whose bytes do not match"), takes: asCaller, run: runCheckBlobs},
}

// NotCommands are the methods of fado.DB that are not commands, each
// with why (ADR-0055, item 2).
var NotCommands = map[string]string{
	"Close":    "closing the database is the server's",
	"Exec":     "it is the door the commands come through",
	"PutBlob":  "the bytes of a file travel beside the commands (item 9)",
	"OpenFile": "the bytes of a file travel beside the commands (item 9)",
	"OpenBlob": "the bytes of a file travel beside the commands (item 9)",
	"Class":    "it answers the engine's compiled form to a Go program; get_class answers the definition",
	"Schema":   "it answers the engine's compiled form to a Go program; get_schema answers the definition",
	"Table":    "it answers the engine's compiled form to a Go program; get_table answers the definition",
	"Action":   "it answers one entry of the catalogue list_actions answers whole",
}

var byName = map[string]*Command{}

func init() {
	for _, c := range commands {
		for _, o := range []struct {
			takes, refuses options
			field          Field
		}{
			{takesKey, refusesKey, optionKey},
			{takesBusinessDate, 0, optionBusinessDate},
			{takesReason, 0, optionReason},
			{takesAt, refusesAt, optionAt},
			{takesFor, 0, optionFor},
		} {
			switch {
			case c.takes&o.takes != 0:
				c.Fields = append(c.Fields, o.field)
			case c.takes&o.refuses != 0:
				o.field.Refused = true
				c.Fields = append(c.Fields, o.field)
			}
		}
		if len(c.Fields) > maxFields {
			panic("command: " + c.Name + " has more members than a call holds")
		}
		byName[c.Name] = c
	}
}

// Commands lists the table, in its order. The rows are the table's own:
// a caller reads them and changes nothing.
func Commands() []*Command { return commands }

// Lookup finds a command by its name.
func Lookup(name string) (*Command, bool) {
	c, ok := byName[name]
	return c, ok
}
