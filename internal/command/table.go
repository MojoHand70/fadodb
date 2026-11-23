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

	// Stream says the answer of the command is a stream and not one
	// object (ADR-0058): a reply that stays open and carries an entry
	// for each event. Exec does not run such a command. A Go program
	// makes the call, and a door answers the command at its address.
	Stream bool

	// Users says the command keeps the users, groups and keys of a
	// database (ADR-0057). A database that keeps none, because the
	// program that opened it says itself who is calling, does not offer
	// the command: it answers unknown_command.
	Users bool

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
	// refusesFor: the stream of changes is heard as oneself, which the
	// engine says in its own words to a caller that names somebody.
	refusesFor

	// asReader is a command that reads documents or rows. A content
	// administrator reads past a rule with a reason (ADR-0067).
	asReader = takesAt | takesReason | takesFor
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

// TakesKey says the command takes the idempotency key: the same command
// sent again with the same key makes no second commit (ADR-0055, item
// 3). It is the commands that commit a document, and no other.
func (c *Command) TakesKey() bool { return c.takes&takesKey != 0 }

// The five members that mean the same in every command that takes them.
var (
	optionKey          = Field{Name: "key", Kind: Text, Option: true, Does: "the idempotency key: the same command sent again with the same key makes no second commit and is answered as the first was"}
	optionBusinessDate = Field{Name: "business_date", Kind: Text, Option: true, Does: `the business day of the commit, as "2026-11-13"; the server's day when left out`}
	optionReason       = Field{Name: "reason", Kind: Text, Option: true, Does: "why a content administrator acts or reads past a rule; a change keeps it on its event, a read in the log of reads"}
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
	Users       = "Users, groups and keys"
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
	theUser       = "the user: its id, its kind, its name, its groups, its system groups and whether it is disabled"
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
	{Group: Documents, Name: "live", Mark: Reads, Call: "Live", Does: "Works a live variable of a document out for the caller, now: one value, or the list a QUERY gives. Nothing is kept.",
		Fields: []Field{req("class", Text, theClass), req("id", Text, theID), req("variable", Text, `the live variable: one that says "mode": "live" in its class`),
			opt("order", Texts, `for a list: the columns to sort by, a leading "-" for descending; the order of the formula when left out`),
			opt("limit", Integer, "for a list: the most rows to answer; 1,000 when left out, 10,000 at most"), opt("offset", Integer, "for a list: how many rows to skip"),
			opt("fields", Texts, "for a list: the columns to answer; every one when left out")},
		Answer: answer("value", "for one value: what the variable is for this caller at this moment",
			"columns", "for a list: its columns in the order of the formula, each with its name, its type and the label the source gives it",
			"rows", "for a list: the documents the caller may read, each with its id and the columns the caller may read in it",
			"more", "for a list: true when rows follow the last one answered"), takes: asReader, run: runLive},
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
	{Group: Documents, Name: "subscribe", Mark: Reads, Call: "Subscribe", Stream: true,
		Does: "Opens the stream of changes: the caller is told of every event it may read of, in the order of the commits, as each is written.",
		Fields: []Field{opt("classes", Texts, "the classes to hear of; every class when left out"),
			opt("kinds", Texts, "the kinds of event to hear of, such as created, set and transition; every kind when left out"),
			opt("to_states", Texts, "for a transition, the states entered; every state when left out"),
			opt("after", Text, "the id of an event: the stream begins after it, so a caller that was away gives the last one it handled; now when left out")},
		takes: refusesFor | takesReason, run: runSubscribe},
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
	{Group: Documents, Name: "flow", Mark: Reads, Call: "Flow", Does: "Answers the flow of a class: the documents the caller may read in each state now, and their moves along each transition.",
		Fields: []Field{req("class", Text, theClass),
			opt("since", Moment, "the first moment of the period the moves and the stays are counted in; since the class began when left out"),
			opt("until", Moment, "the moment the period ends before; now when left out")},
		Answer: answer("flow", "the states of the class, each with its documents now, since when the oldest is there, and how many stays ended in the period and how long they lasted; and its transitions, each with its moves in the period"),
		takes:  asCaller | takesReason, run: runFlow},
	{Group: Documents, Name: "sql", Mark: Reads, Call: "SQL", Does: "Runs one read-only SELECT over the views, for an analyst.",
		Fields: []Field{req("statement", Text, "the SELECT, with ? for each argument"), opt("args", Scalars, "the arguments, in order")},
		Answer: answer("columns", "the names of the columns", "rows", "the rows, each a list of values in the order of the columns"), takes: asCaller | takesReason, run: runSQL},

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
	{Group: Definitions, Name: "remove_table", Mark: Writes, Call: "RemoveTable", Does: "Takes a table away with its rows, its definition and its words; refused while a class reads it or a rule writes it. The log of definitions says who removed it.",
		Fields: []Field{req("name", Text, "the name of the table")}, takes: asCaller, run: runRemoveTable},
	{Group: Definitions, Name: "define_action", Mark: Writes, Call: "DefineAction", Does: "Defines an action a rule may name.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("action", theStored), takes: asCaller, run: runDefineAction},
	{Group: Definitions, Name: "define_app", Mark: Writes, Call: "DefineApp", Does: "Defines an app, a name for the classes and tables that belong together, or the next version of one.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("app", theStored), takes: asCaller, run: runDefineApp},
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
		Answer: answer("class", theStored+", the notes of the class, and under pointed_at_by the ref variables of the classes defined now that point at it"), run: runGetClass},
	{Group: Definitions, Name: "get_bundle", Mark: Reads, Call: "GetBundle", Does: "Reads the bundle of a class, for a page: the class in the person's words and its formulas compiled, so that the page works a document out as the server does.", takes: asNobody,
		Fields: []Field{req("class", Text, theClass), opt("language", Text, "the person's language, a tag of BCP 47 in any case: pl, pt-BR; the class's own words when left out"),
			opt("version", Integer, "an earlier version of the class that computed a document; the current one when left out"),
			opt("tag", Text, "the tag of the bundle the page holds; when it is still the bundle's, the answer is unchanged and nothing more")},
		Answer: answer("class", "the class", "version", "its version", "bytecode", "the format of the compiled formulas",
			"language", "the language asked for", "languages", "the languages the class has words in", "words", "the words put in place, by name and version",
			"tag", "what the page sends back", "definition", "the class, the words put in place", "schemas", "the schemas it uses",
			"compiled", "the formulas a page runs, compiled or marked as the server's", "unchanged", "true, with the tag alone, when the page holds the bundle"), run: runGetBundle},
	{Group: Definitions, Name: "get_schema", Mark: Reads, Call: "Definition", Does: "Reads a schema back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the schema")},
		Answer: answer("schema", theStored), run: runGetSchema},
	{Group: Definitions, Name: "get_table", Mark: Reads, Call: "Definition", Does: "Reads a table's definition back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the table")},
		Answer: answer("table", theStored), run: runGetTable},
	{Group: Definitions, Name: "get_action", Mark: Reads, Call: "Definition", Does: "Reads an action's definition back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the action"), opt("version", Integer, "an earlier version; the current one when left out")},
		Answer: answer("action", theStored), run: runGetAction},
	{Group: Definitions, Name: "get_app", Mark: Reads, Call: "Definition", Does: "Reads an app back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the app")},
		Answer: answer("app", theStored), run: runGetApp},
	{Group: Definitions, Name: "list_classes", Mark: Reads, Call: "Classes", Does: "Lists the classes: all of them, or those of one app.", takes: asNobody,
		Fields: []Field{opt("app", Text, "the name of an app, for the classes that belong to it; every class when left out")},
		Answer: answer("classes", "the names, in the order they were defined"), run: runListClasses},
	{Group: Definitions, Name: "list_apps", Mark: Reads, Call: "Apps", Does: "Lists the apps.", takes: asNobody,
		Answer: answer("apps", "the names, in the order they were defined"), run: runListApps},
	{Group: Definitions, Name: "list_schemas", Mark: Reads, Call: "Schemas", Does: "Lists the schemas.", takes: asNobody,
		Answer: answer("schemas", "the names, the engine's own first"), run: runListSchemas},
	{Group: Definitions, Name: "list_tables", Mark: Reads, Call: "Tables", Does: "Lists the tables: all of them, or those of one app.", takes: asNobody,
		Fields: []Field{opt("app", Text, "the name of an app, for the tables that belong to it; every table when left out")},
		Answer: answer("tables", "the names, in the order they were defined"), run: runListTables},
	{Group: Definitions, Name: "list_actions", Mark: Reads, Call: "Actions", Does: "Lists every action a rule may name, the engine's own first.", takes: asNobody,
		Answer: answer("actions", "the actions, each with its parameters, what it returns and whether this server can run it"), run: runListActions},
	{Group: Definitions, Name: "list_functions", Mark: Reads, Call: "Functions", Does: "Lists every function a formula can call, for whoever writes one: a formula editor, or an agent before it defines a class.", takes: asNobody,
		Answer: answer("functions", "the functions in the order of the manual, each with its other names, its kind, its category, the call as the manual writes it, its arguments, what it returns, a sentence on what it answers, and not_built where this version of the engine refuses it",
			"kinds", "the kinds of function, each with what it means for where a function may stand"), run: runListFunctions},
	{Group: Definitions, Name: "definitions", Mark: Reads, Call: "Definitions", Does: "Lists the versions of one class, schema, table, action, inlet, app or words, with who defined each and when; to administrators and designers, and to an agent working for an administrator, also who removed a table or words and when.", takes: asCaller,
		Fields: []Field{req("what", Text, "class, schema, table, action, inlet, app or words"), req("name", Text, "its name; for words, what they translate, its name and the language: class/Invoice/pl")},
		Answer: answer("versions", "the versions, oldest first; for a caller who may see it, the one a table or words had when they were removed says who removed them and when, under removed"), run: runDefinitions},
	{Group: Definitions, Name: "preview_draft", Mark: Reads, Call: "PreviewDraft", Does: "Tries a class definition that is not stored on a create, or on a document of the stored class of the same name, writing nothing.",
		Fields: []Field{req("definition", JSON, "the draft, as JSON"), opt("id", Text, "the id of a document of the stored class; left out, the preview is of a create"),
			opt("values", Object, theValues), opt("transition", Text, "the name of a transition to take")},
		Answer: answer("preview", thePreview), takes: asPreviewer, run: runPreviewDraft},
	{Group: Definitions, Name: "migrate", Mark: Writes, Call: "Migrate", Does: "Brings every live document of a class that an earlier version computed to the current version.",
		Fields: []Field{req("class", Text, theClass)},
		Answer: answer("migrated", "how many documents it moved"), takes: asWriter | refusesKey, run: runMigrate},
	// The words in other languages (ADR-0068, item 6).
	{Group: Definitions, Name: "define_words", Mark: Writes, Call: "DefineWords", Does: "Defines the words of a class, a table or a schema in another language: the label and the description of each thing, by its path. It makes no new version of what it translates.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("words", theStored+", and a note for each path the class no longer has"), takes: asCaller, run: runDefineWords},
	{Group: Definitions, Name: "get_words", Mark: Reads, Call: "Definition", Does: "Reads the words of a class, a table or a schema in one language back as they were defined.", takes: asNobody,
		Fields: []Field{opt("class", Text, "the class they translate"), opt("table", Text, "the table, in place of a class"), opt("schema", Text, "the schema, in place of a class"),
			req("language", Text, "the language, a tag of BCP 47: pl, pt-BR"), opt("version", Integer, "an earlier version; the current one when left out")},
		Answer: answer("words", theStored+", and a note for each path the class no longer has"), run: runGetWords},
	{Group: Definitions, Name: "list_words", Mark: Reads, Call: "ListWords", Does: "Lists the words in other languages: of every class, table and schema, or of the one named, in every language or in the one named.", takes: asNobody,
		Fields: []Field{opt("class", Text, "only the words of this class"), opt("table", Text, "only the words of this table"), opt("schema", Text, "only the words of this schema"),
			opt("language", Text, "only the words in this language")},
		Answer: answer("words", "the words, in the order first defined, each with its name, its version, who defined it and a note for each path the class no longer has"), run: runListWords},
	{Group: Definitions, Name: "remove_words", Mark: Writes, Call: "RemoveWords", Does: "Takes away the words of a class, a table or a schema in one language: a page shows it in its own words from then on. Their versions stay, and the log of definitions says who took them away.",
		Fields: []Field{opt("class", Text, "the class they translate"), opt("table", Text, "the table, in place of a class"), opt("schema", Text, "the schema, in place of a class"),
			req("language", Text, "the language")}, takes: asCaller, run: runRemoveWords},

	// The inlets (ADR-0065): a way in for messages from outside.
	{Group: Definitions, Name: "validate_inlet", Mark: Reads, Call: "ValidateInlet", Does: "Reports what define_inlet would refuse, storing nothing.", takes: asNobody,
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("diagnostics", "the findings; an empty list when nothing is wrong"), run: runValidateInlet},
	{Group: Definitions, Name: "try_inlet", Mark: Reads, Call: "TryInlet", Does: "Answers what an inlet would do with a message, and does nothing: whether it acts on it, and the command the message would become. The proof is not asked for.",
		Fields: []Field{opt("inlet", Text, "the name of an inlet of the database"), opt("definition", JSON, "a definition that is not stored, in place of a name"),
			opt("message", Object, "the message: headers and params, each an object of texts; body, the body as JSON or as one text; and form, true when the body holds the fields of a form")},
		Answer: answer("trial", "whether the inlet acts on the message, the user it would act as, and the command with its object and its key"), takes: asCaller, run: runTryInlet},
	{Group: Definitions, Name: "define_inlet", Mark: Writes, Call: "DefineInlet", Does: "Defines an inlet: how a message from outside proves who sent it, and the one command it becomes, carried out as a service user. For a database administrator.",
		Fields: []Field{req("definition", JSON, theDefinition)},
		Answer: answer("inlet", theStored), takes: asCaller, run: runDefineInlet},
	{Group: Definitions, Name: "remove_inlet", Mark: Writes, Call: "RemoveInlet", Does: "Closes an inlet: its messages are answered as those of a name that is no inlet. Its versions stay. For a database administrator.",
		Fields: []Field{req("name", Text, "the name of the inlet")}, takes: asCaller, run: runRemoveInlet},
	{Group: Definitions, Name: "get_inlet", Mark: Reads, Call: "Definition", Does: "Reads an inlet back as it was defined.", takes: asNobody,
		Fields: []Field{req("name", Text, "the name of the inlet"), opt("version", Integer, "an earlier version; the current one when left out")},
		Answer: answer("inlet", theStored), run: runGetInlet},
	{Group: Definitions, Name: "list_inlets", Mark: Reads, Call: "Inlets", Does: "Lists the inlets.", takes: asNobody,
		Answer: answer("inlets", "the inlets, each with its address, the user it acts as, the command its messages become and whether this server acts on them"), run: runListInlets},

	// Tables, actions and the queue.
	{Group: Operations, Name: "table_rows", Mark: Reads, Call: "TableRows", Does: "Reads every row of a table, in the order of its key.",
		Fields: []Field{req("table", Text, "the name of the table")},
		Answer: answer("rows", "the rows, each an object of values by column"), takes: asReader, run: runTableRows},
	{Group: Operations, Name: "import_rows", Mark: Writes, Call: "ImportRows", Does: "Writes rows into a table; a row whose key exists is replaced.",
		Fields: []Field{req("table", Text, "the name of the table"), req("rows", Objects, "the rows, each an object of values by column"),
			opt("replace", Flag, "true empties the table first, so that the rows given are all its rows")},
		Answer: answer("imported", "how many rows it wrote"), takes: asWriter | takesReason, run: runImportRows},
	{Group: Operations, Name: "delete_rows", Mark: Writes, Call: "DeleteRows", Does: "Removes the rows of a table that have the keys given; a key the table does not have removes nothing.",
		Fields: []Field{req("table", Text, "the name of the table"), req("keys", Objects, "the keys of the rows, each an object with a value for every column of the table's key and for no other")},
		Answer: answer("deleted", "how many rows it removed"), takes: asWriter | takesReason, run: runDeleteRows},
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
	{Group: Operations, Name: "check_blobs", Mark: Reads, Call: "CheckBlobs", Does: "Recomputes the hash of every file the database holds, and looks for every file a document holds.",
		Answer: answer("checked", "how many files it checked", "mismatched", "the hashes of the files whose bytes do not match",
			"missing", `the files a document holds, in any version, that the database has lost, each {"hash", "class", "id", "version", "variable"}`), takes: asCaller, run: runCheckBlobs},
	{Group: Operations, Name: "reads", Mark: Reads, Call: "Reads", Does: "Lists the log of reads: what a content administrator saw past a rule, with the reason, and every SQL statement. It is a database administrator's.",
		Fields: []Field{opt("by", Text, "only what this caller read"), opt("class", Text, "only reads of this class"),
			opt("id", Text, "only reads that showed this document; needs class"), opt("table", Text, "only reads of this table"),
			opt("command", Text, "only reads by this command, such as get, query or sql"),
			opt("since", Moment, "the first moment of the period"), opt("until", Moment, "the moment the period ends before"),
			opt("after", Integer, "the seq of the last entry already read, to read on from"),
			opt("limit", Integer, "the most entries to answer: 1,000 when left out, at most 10,000")},
		Answer: answer("reads", "the entries, oldest first: seq, when, who, of what kind and for whom, the command, the class or the table, the documents it showed past a rule and the rules, the reason; for a count how many it counted past a rule; for a table how many rows; for SQL the statement, its arguments and how many rows it returned or the code it failed with"),
		takes:  asCaller, run: runReads},

	// Users, groups and keys (ADR-0057).
	{Group: Users, Users: true, Name: "create_user", Mark: Writes, Call: "CreateUser", Does: "Makes a user: a person, an agent or a service the database knows by its id. It is a database administrator's.",
		Fields: []Field{req("id", Text, "the id every event will record; it never changes and is never given to somebody else"),
			req("kind", Text, "person, agent or service"), opt("name", Text, "how a screen shows the user"),
			opt("email", Text, "a person's company e-mail, by which a first sign-in with the company's sign-on finds them"),
			opt("groups", Texts, "the groups of the user, which INGROUP answers"), opt("system_groups", Texts, "the system groups of the user: administrators, designers, content-administrators, analysts")},
		Answer: answer("user", theUser), takes: asWriter, run: runCreateUser},
	{Group: Users, Users: true, Name: "set_user", Mark: Writes, Call: "SetUser", Does: "Changes a user: the members given, and no other. It is a database administrator's, who may change their own groups too.",
		Fields: []Field{req("id", Text, "the id of the user"), opt("name", Text, "how a screen shows the user"),
			opt("email", Text, "a person's company e-mail, in place of the one they have; empty takes it away"),
			opt("groups", Texts, "the groups of the user, in place of the ones it has"), opt("system_groups", Texts, "the system groups of the user, in place of the ones it has"),
			opt("disabled", Flag, "true stops every key of the user at the next command; false lets them through again")},
		Answer: answer("user", theUser+", after the change"), takes: asWriter, run: runSetUser},
	{Group: Users, Users: true, Name: "get_user", Mark: Reads, Call: "GetUser", Does: "Reads one user. Every user may ask.",
		Fields: []Field{req("id", Text, "the id of the user")},
		Answer: answer("user", theUser), takes: asCaller, run: runGetUser},
	{Group: Users, Users: true, Name: "list_users", Mark: Reads, Call: "Users", Does: "Lists the users, in the order of their ids. Every user may ask.",
		Fields: []Field{opt("group", Text, "only the members of this group, of either kind"), opt("kind", Text, "only the users of this kind")},
		Answer: answer("users", "the users, the disabled ones among them"), takes: asCaller, run: runListUsers},
	{Group: Users, Users: true, Name: "user_changes", Mark: Reads, Call: "UserChanges", Does: "Lists every change to a user and to its keys. A database administrator may ask of any user, and a user of itself.",
		Fields: []Field{req("id", Text, "the id of the user")},
		Answer: answer("changes", "the changes, oldest first: what was done, by whom, of what kind, for whom and when, with the user or the key as the change left it"), takes: asCaller, run: runUserChanges},
	{Group: Users, Users: true, Name: "create_key", Mark: Writes, Call: "CreateKey", Does: "Makes a key for a user and answers it with its secret, this once. It is a database administrator's.",
		Fields: []Field{req("user", Text, "the id of the user the key is for"), opt("label", Text, "where the key is used, such as laptop or nightly import"),
			opt("expires", Text, "the last day the key holds, as YYYY-MM-DD in UTC; left out, the key holds until it is revoked")},
		Answer: answer("key", "the key: its id, its user, its label, when and by whom it was made, its last day, and under secret the key itself, which is shown this once and kept nowhere"), takes: asWriter, run: runCreateKey},
	{Group: Users, Users: true, Name: "list_keys", Mark: Reads, Call: "Keys", Does: "Lists the keys of a user, never a secret. A database administrator may ask of any user, and a user of itself.",
		Fields: []Field{opt("user", Text, "the id of the user; left out, the caller")},
		Answer: answer("keys", "the keys, oldest first, the revoked ones among them"), takes: asCaller, run: runListKeys},
	{Group: Users, Users: true, Name: "revoke_key", Mark: Writes, Call: "RevokeKey", Does: "Ends a key: the next command sent with it is refused. A database administrator may revoke any key, and a user its own.",
		Fields: []Field{req("key", Text, "the id of the key, as list_keys shows it, and not the key itself")}, takes: asWriter, run: runRevokeKey},
	{Group: Users, Users: true, Name: "create_password_link", Mark: Writes, Call: "CreatePasswordLink", Does: "Makes a link that lets a person set their password, good once and for a day, and answers its secret, this once. Nobody sets another's password. It is a database administrator's.",
		Fields: []Field{req("user", Text, "the id of the person the link is for")},
		Answer: answer("link", "the link: its user, the moment it ends, who made it, and under token the secret the server's page /auth/password takes, shown this once"), takes: asWriter, run: runCreatePasswordLink},
	{Group: Users, Users: true, Name: "list_sessions", Mark: Reads, Call: "Sessions", Does: "Lists the sessions of a user that still hold, never a secret. A database administrator may ask of any user, and a user of itself.",
		Fields: []Field{opt("user", Text, "the id of the user; left out, the caller")},
		Answer: answer("sessions", "the sessions, oldest first: their id, the way the user signed in, where from, when they began and were last used, and when they end"), takes: asCaller, run: runListSessions},
	{Group: Users, Users: true, Name: "end_session", Mark: Writes, Call: "EndSession", Does: "Ends a session: the next request with it is refused. A database administrator may end any, and a user its own.",
		Fields: []Field{req("session", Text, "the id of the session, as list_sessions shows it")}, takes: asWriter, run: runEndSession},
	{Group: Users, Users: true, Name: "clear_second_step", Mark: Writes, Call: "ClearSecondStep", Does: "Takes away the second step of a user, for a person who lost the phone and its codes; it is set up again at the next sign-in when the user's system groups require one. A database administrator clears anybody's, and a user its own when its groups do not require one.",
		Fields: []Field{opt("user", Text, "the id of the user; left out, the caller")}, takes: asWriter, run: runClearSecondStep},
	{Group: Users, Users: true, Name: "sign_ins", Mark: Reads, Call: "SignIns", Does: "Lists the log of sign-ins, refused ones among them, oldest first. It is a database administrator's.",
		Fields: []Field{opt("user", Text, "only the sign-ins of this user"), opt("since", Moment, "the first moment of the period"),
			opt("after", Integer, "the seq of the last entry already read, to read on from"), opt("limit", Integer, "the most entries to answer, at most 1,000")},
		Answer: answer("sign_ins", "the sign-ins: their seq, when, the user named, the way, the address they came from and whether they were let through"), takes: asCaller, run: runSignIns},
	{Group: Users, Users: true, Name: "add_client", Mark: Writes, Call: "AddClient", Does: "Adds a client the server gives passes to by OAuth: a face or an app of the company, or an agent. A client known by its published document, as Claude is, is never added. For a database administrator.",
		Fields: []Field{req("id", Text, "the client_id it sends: letters, digits, dots, dashes and underscores"),
			req("name", Text, "the name the person is shown"),
			req("redirect_uris", Texts, "where its codes go: over https, or over http on its own machine"),
			req("address", Text, "/v1, the commands, as the person; or /mcp, the door for agents, as the person's agent")},
		Answer: answer("client", "the client as added"), takes: asWriter, run: runAddClient},
	{Group: Users, Users: true, Name: "list_clients", Mark: Reads, Call: "Clients", Does: "Lists the clients an administrator added, removed ones too. For a database administrator.",
		Answer: answer("clients", "the clients, by id"), takes: asCaller, run: runListClients},
	{Group: Users, Users: true, Name: "remove_client", Mark: Writes, Call: "RemoveClient", Does: "Removes a client: every grant of it ends. For a database administrator.",
		Fields: []Field{req("client", Text, "the id of the client")}, takes: asWriter, run: runRemoveClient},
	{Group: Users, Users: true, Name: "list_grants", Mark: Reads, Call: "Grants", Does: "Lists the clients a person allowed that still hold, never a pass. An administrator asks of anybody, a user of itself.",
		Fields: []Field{opt("user", Text, "the id of the user; left out, the caller")},
		Answer: answer("grants", "the grants, oldest first: the person, the agent, the client, the address, the substitution, when allowed and last renewed, and when it ends unless renewed"), takes: asCaller, run: runListGrants},
	{Group: Users, Users: true, Name: "end_grant", Mark: Writes, Call: "EndGrant", Does: "Ends a grant: its passes are refused, and its agent's substitution is withdrawn. An administrator ends any, a person their own.",
		Fields: []Field{req("grant", Text, "the id of the grant, as list_grants shows it")}, takes: asWriter, run: runEndGrant},
	{Group: Users, Users: true, Name: "whoami", Mark: Reads, Call: "Whoami", Does: "Answers who the database takes the caller for.",
		Answer: answer("user", theUser, "for", "the id of whoever the caller acts for, when the command names one"), takes: asCaller, run: runWhoami},
}

// NotCommands are the methods of fado.DB that are not commands, each
// with why (ADR-0055, item 2).
var NotCommands = map[string]string{
	"Close":        "closing a database is for the program that opened it",
	"Exec":         "it is the door the commands come through",
	"PutBlob":      "the bytes of a file are not JSON: they travel beside the commands, with the server",
	"OpenFile":     "the bytes of a file are not JSON: they travel beside the commands, with the server",
	"OpenBlob":     "the bytes of a file are not JSON: they travel beside the commands, with the server",
	"Ping":         "it asks whether the database answers; a server answers it at GET /v1/health, for anybody",
	"OutboxCounts": "it counts what of the queue is still to do, for whoever watches a server, which answers it at GET /v1/metrics; outbox lists the actions themselves",
	"Authenticate": "it answers who a key belongs to, for the door that is given the key; a caller that wants to know who it is sends whoami",
	"Class":        "it answers the engine's compiled form to a Go program; get_class answers the definition",
	"Schema":       "it answers the engine's compiled form to a Go program; get_schema answers the definition",
	"App":          "it answers the engine's parsed form to a Go program; get_app answers the definition",
	"ClassesOf":    "it is list_classes with its app given, for a Go program",
	"TablesOf":     "it is list_tables with its app given, for a Go program",
	"LivePage":     "it is live with the page of a list given, for a Go program: the command takes the page in the same call",
	"Table":        "it answers the engine's compiled form to a Go program; get_table answers the definition",
	"Action":       "it answers one entry of the catalogue that list_actions answers whole",
	"Inlet":        "it answers one entry of the list that list_inlets answers whole",
	"Receive":      "it takes a message from outside, which proves itself and carries no key of a user: a server answers it at POST /v1/in/<inlet>",
	// Signing in (ADR-0071): a password, a code and a session's token
	// never travel in a command, where they would stay in a log or in an
	// agent's conversation. The server's own pages take them.
	"BeginSignIn":         "a password never travels in a command: the server's page /auth/signin takes it",
	"SetUpSecondStep":     "it belongs to a sign-in under way on the server's page /auth/signin",
	"FinishSignIn":        "it belongs to a sign-in under way on the server's page /auth/signin",
	"SessionCaller":       "it answers who a session belongs to, for the door that is given the session; a caller that wants to know who it is sends whoami",
	"SignOut":             "it ends the session it is sent with, on the server's page /auth/signout; end_session ends one by its id",
	"ChangePassword":      "a password never travels in a command: the server's page /auth/account takes it",
	"PasswordLinkUser":    "it belongs to the server's page /auth/password, which a link opens",
	"UsePasswordLink":     "a password never travels in a command: the server's page /auth/password takes it",
	"SetUpMySecondStep":   "its secret never travels in a command: the server's page /auth/account shows it",
	"ConfirmMySecondStep": "its codes never travel in a command: the server's page /auth/account shows them",
	"HasSecondStep":       "the server's page /auth/account shows it to the person",
	"SignOn":              "it takes who a company's provider vouched for, which the server checked on its page /auth/callback; no command carries a provider's word",
	// The passes the server gives clients (ADR-0071, item 6): a code and
	// a pass never travel in a command. The server's addresses of OAuth
	// take them.
	"KnownClient": "it answers a client to the server's page /auth/authorize; list_clients lists them",
	"AskConsent":  "it keeps a client's request, which the server's page /auth/authorize took",
	"Consent":     "it belongs to the server's page /auth/allow, which asks the person",
	"Allow":       "a code never travels in a command: the server's page /auth/allow sends it to the client",
	"Deny":        "it belongs to the server's page /auth/allow, which asks the person",
	"Exchange":    "a code and a pass never travel in a command: the server's address /auth/token takes them",
	"Renew":       "a pass never travels in a command: the server's address /auth/token takes it",
	"Revoke":      "a pass never travels in a command: the server's address /auth/revoke takes it; end_grant ends a grant by its id",
	"PassCaller":  "it answers who a pass belongs to, for the door that is given the pass; a caller that wants to know who it is sends whoami",
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
			{takesFor, refusesFor, optionFor},
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
