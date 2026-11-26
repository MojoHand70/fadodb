# Architecture decision records

One file per decision. A record is never edited after acceptance; a change is
a new record that amends or supersedes the old one, and the old one's status
line says so. Copy `0000-template.md` to start.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-license-and-name.md) | License and name | Accepted, license superseded by 0061 |
| [0002](0002-pure-go-core-and-dependency-budget.md) | Pure-Go core and dependency budget | Accepted, amended by 0017, 0072 |
| [0003](0003-bytecode-as-interchange-format.md) | Bytecode as the interchange format | Accepted, amended by 0029, 0037, 0046, 0070 |
| [0004](0004-number-semantics.md) | Number semantics: float64 with Excel rules | Accepted, amended by 0019, 0022, 0024 |
| [0005](0005-blank-semantics.md) | Blank and null semantics | Accepted |
| [0006](0006-event-log-as-source-of-truth.md) | Event log as the source of truth | Accepted, amended by 0014, 0026, 0044 |
| [0007](0007-one-table-per-class.md) | One table per document class | Accepted, amended by 0014 |
| [0008](0008-statechart-semantics.md) | Statechart semantics, SCXML subset | Accepted, amended by 0039, 0040, 0047, 0068 |
| [0009](0009-action-execution-model.md) | Action execution model: synchronous versus outbox | Accepted, amended by 0027, 0044, 0045 |
| [0010](0010-security-rules-as-predicates.md) | Security rules compiled to SQL predicates | Accepted, amended by 0027, 0028, 0041, 0046, 0049, 0052 |
| [0011](0011-one-json-command-set.md) | One JSON command set over HTTP, WebSocket and C ABI | Accepted, amended by 0025, 0026, 0027, 0033, 0045, 0046 |
| [0012](0012-performance-gates.md) | Performance budgets and gates | Accepted, amended by 0030, 0072 |
| [0013](0013-document-variables-and-types.md) | Document variables and the type system | Accepted, amended by 0019, 0022, 0023, 0031, 0034, 0047, 0068, 0075 |
| [0014](0014-document-tables.md) | Document tables: current, history, child tables and views | Accepted, amended by 0026, 0031, 0039, 0040, 0043, 0047, 0075 |
| [0015](0015-commits-and-recalculation.md) | Commits, recalculation modes, defaults, reset and refresh | Accepted, amended by 0020, 0025, 0040, 0043, 0076, 0078 |
| [0016](0016-explicit-tables-and-query-functions.md) | Explicit tables, storage bindings, LOOKUP and QUERY | Accepted, amended by 0020, 0036, 0037, 0046, 0068, 0076, 0079 |
| [0017](0017-duckdb-as-the-analytical-engine.md) | DuckDB as the analytical engine, full and lite builds | Accepted, amended by 0035, 0072, 0089 |
| [0018](0018-class-metadata-layouts-and-wire-format.md) | JSON for definitions and interchange: class metadata, layouts and the wire format | Accepted, amended by 0022, 0023, 0028, 0031, 0032, 0033, 0034, 0039, 0043, 0047, 0068, 0069, 0070, 0077 |
| [0019](0019-formula-language-semantics.md) | Formula language semantics: grammar, types, coercion, comparison, dates, text, composites and errors | Accepted, amended by 0021, 0022, 0023, 0024, 0031, 0043, 0047, 0050, 0076 |
| [0020](0020-query-function-execution.md) | LOOKUP and QUERY execution: one engine call per invocation | Accepted, amended by 0036, 0037, 0046, 0076, 0089 |
| [0021](0021-size-limits.md) | Size limits: one text limit per database, enforced before building | Accepted, amended by 0033 |
| [0022](0022-exact-decimal-int-and-money.md) | Exact numbers: decimal, int and money | Accepted |
| [0023](0023-calendar-day-datetime-and-enum-values.md) | A date is a calendar day, a datetime an instant, an enum a text with a value set | Accepted |
| [0024](0024-division-by-zero-is-an-error-and-iferror.md) | Division by zero is an error, and IFERROR catches one | Accepted |
| [0025](0025-evaluate-without-committing.md) | Evaluate a class or a document without committing | Accepted, amended by 0042, 0043, 0048, 0049 |
| [0026](0026-idempotency-keys-business-dates-and-the-transition-event.md) | Idempotency keys on commands, a business date on events, and the transition event | Accepted, amended by 0040, 0044, 0045, 0047, 0048, 0049, 0050, 0078 |
| [0027](0027-principal-from-the-embedder-and-registered-executors.md) | The principal comes from the embedder; action executors are registered | Accepted, amended by 0044, 0045, 0049, 0050, 0051, 0052, 0053 |
| [0028](0028-field-level-rights-per-state.md) | Field-level rights per state | Accepted, amended by 0041, 0069, 0070, 0076, 0078 |
| [0029](0029-browser-vm-as-one-plain-javascript-file.md) | The browser VM ships as one plain JavaScript file | Accepted |
| [0030](0030-benchmark-gate-measures-the-minimum-over-placements.md) | The benchmark gate measures the minimum over code placements | Accepted |
| [0031](0031-named-schemas-and-object-values.md) | Named schemas and object values | Accepted, amended by 0068 |
| [0032](0032-predefined-vocabulary-and-object-presentation.md) | A predefined vocabulary of objects, and how an object is presented | Accepted, amended by 0069 |
| [0033](0033-attachments-as-file-values-behind-a-blob-store.md) | Attachments are File values; the bytes live behind a blob store | Accepted, amended by 0049, 0052, 0055, 0067 |
| [0034](0034-range-constraints-on-numeric-variables.md) | Range constraints on numeric variables | Accepted, amended by 0069 |
| [0035](0035-sqlite-is-the-transactional-store.md) | SQLite is the transactional store, by measurement | Accepted |
| [0036](0036-in-memory-index-for-lookups-in-commits.md) | An in-memory index serves LOOKUP inside commits, by measurement | Accepted |
| [0037](0037-explicit-tables-and-the-compiled-lookup.md) | Explicit tables bound to SQLite and the compiled LOOKUP | Accepted, amended by 0046, 0051, 0080, 0082, 0084 |
| [0038](0038-the-filter-and-the-read-only-sql-endpoint.md) | The filter and the read-only SQL endpoint | Accepted, amended by 0049, 0051, 0067, 0075, 0076 |
| [0039](0039-class-versions-terminal-states-and-migrations.md) | Class versions, terminal states and migrations | Accepted, amended by 0040, 0043, 0051 |
| [0040](0040-transitions-as-commits.md) | Transitions as commits: the command, eventless chains and state visits | Accepted, amended by 0043, 0047, 0048, 0078 |
| [0041](0041-field-rights-and-transition-rules-in-the-go-api.md) | Field rights and transition rules in the Go API | Accepted, amended by 0049, 0052, 0078 |
| [0042](0042-a-moment-the-caller-chooses.md) | A moment the caller chooses, for previews and reads | Accepted |
| [0043](0043-rules-inside-the-commit.md) | Rules inside the commit: triggers, synchronous actions and triggered variables | Accepted, amended by 0044, 0078 |
| [0044](0044-the-outbox.md) | The outbox: what a commit queues, the worker and the engine's own actions | Accepted, amended by 0047, 0049, 0050, 0051 |
| [0045](0045-actions-are-an-extension-point.md) | Actions are an extension point: definitions, declared HTTP actions, registered Go actions, connections and results | Accepted, amended by 0051, 0085 |
| [0046](0046-formulas-that-read-other-documents.md) | Formulas that read other documents: a class as the source of LOOKUP, with COUNT, SUM, MIN and MAX | Accepted, amended by 0051, 0075, 0076, 0078, 0081 |
| [0047](0047-time.md) | Time: a document's life in formulas, what is due, and timed transitions | Accepted, amended by 0052 |
| [0048](0048-moving-many-documents-in-one-command.md) | Moving many documents in one command: `TransitionMany`, the refused reported and the rest moved | Accepted, amended by 0078 |
| [0049](0049-who-may-see-create-change-and-delete-a-document.md) | Who may see, create, change and delete a document | Accepted, amended by 0052 |
| [0050](0050-person-agent-or-service-and-for-whom.md) | Person, agent or service, and for whom it acts | Accepted, amended by 0053 |
| [0051](0051-who-may-define-operate-and-query.md) | Who may define, operate and query, and the rules of a table | Accepted, amended by 0052, 0067, 0083 |
| [0052](0052-system-groups-and-the-content-administrator.md) | System groups: who runs the database, who designs, who reads everything and who may act on any document | Accepted, amended by 0057, 0059, 0067, 0083 |
| [0053](0053-acting-for-somebody.md) | Acting for somebody: a recorded substitution for people and assistants | Accepted, amended by 0071 |
| [0054](0054-the-screen-on-a-layout-component.md) | The screen on a layout component: `mobile` and `desktop` | Withdrawn by 0069 |
| [0055](0055-the-command-set-on-the-wire.md) | The command set on the wire: every command, its JSON, and one address each | Accepted, amended by 0057, 0058, 0059, 0063, 0065, 0067, 0070, 0074, 0077, 0076, 0078, 0079, 0082, 0083, 0086, 0088, 0064 |
| [0056](0056-one-database-for-each-customer.md) | One database for each customer, and a server that serves one database | Accepted |
| [0057](0057-users-groups-and-keys.md) | Who is calling: users, groups and keys, kept in the database | Accepted, amended by 0071, 0086, 0088 |
| [0058](0058-the-stream-of-changes.md) | The stream of changes: the log read forward, over plain HTTP | Accepted, amended by 0067, 0064 |
| [0059](0059-fadodb-serve.md) | `fadodb serve`: one database, what it is told when it starts, and what it refuses | Accepted, amended by 0065, 0071, 0088 |
| [0060](0060-the-organisations-rule-book.md) | The organisation's rule-book: the things it keeps and the look of its faces | Accepted |
| [0061](0061-apache-license.md) | License: Apache 2.0 | Accepted |
| [0062](0062-commands-from-a-terminal.md) | Commands from a terminal: `fadodb exec` | Accepted |
| [0063](0063-the-flow-of-a-class.md) | The flow of a class in one call | Accepted |
| [0064](0064-the-door-for-agents.md) | The door for agents: `fadodb mcp`, each command a tool | Accepted, amended by 0071 |
| [0065](0065-a-way-in-for-messages-from-outside.md) | A way in for messages from outside: an inlet, a definition the server runs | Accepted, amended by 0085 |
| [0066](0066-what-the-servers-budget-is-measured-beside.md) | What the server's budget is measured beside: the Go API called by a request | Accepted |
| [0067](0067-the-log-of-reads.md) | The log of reads: what a content administrator sees past a rule, and every SQL statement | Accepted |
| [0068](0068-what-a-class-says-to-people.md) | What a class says to people: labels, The List, formats, descriptions and other languages | Accepted, amended by 0069, 0077, 0083 |
| [0069](0069-the-screen-is-the-faces-and-a-class-has-sections.md) | The screen is the face's, and a class has sections | Accepted |
| [0070](0070-the-bundle.md) | The bundle: a class, its words and its compiled formulas in one call | Accepted, amended by 0076 |
| [0071](0071-signing-in.md) | Signing in: passwords, company sign-on, sessions and passes for agents | Accepted, amended by 0073, 0088 |
| [0072](0072-the-size-budget-of-the-core-program.md) | The size budget of the core program: 20 MB | Accepted |
| [0073](0073-the-server-hands-out-the-pages-of-a-face.md) | The server hands out the pages of a face from a folder, and has none of its own | Accepted |
| [0074](0074-the-engine-hands-out-its-list-of-functions.md) | The engine hands out its list of functions | Accepted |
| [0075](0075-the-engine-makes-its-own-indexes.md) | The engine makes its own indexes, and says which question it cannot serve | Accepted |
| [0076](0076-a-value-worked-out-when-it-is-read.md) | A value worked out when it is read: the mode `live`, the list of other documents, and what a `ref` compares with | Accepted, amended by 0078 |
| [0077](0077-the-app.md) | The app, a name for the classes that belong together | Accepted, amended by 0079 |
| [0078](0078-a-decision-stands-on-what-the-document-holds.md) | A decision stands on what the document holds: the version a command saw, who may bring a value again, and what a condition saw | Accepted |
| [0079](0079-a-table-belongs-to-an-app.md) | A table belongs to an app | Proposed |
| [0080](0080-a-column-added-to-a-table-in-use.md) | A column added to a table in use | Proposed, amended by 0084 |
| [0081](0081-functions-over-a-list.md) | Functions over a list, each with its SQL from the start | Proposed |
| [0082](0082-removing-rows-and-removing-a-table.md) | Removing rows and removing a table | Proposed, amended by 0083 |
| [0083](0083-who-removed-a-table-and-when.md) | Who removed a table or words, and when | Proposed |
| [0084](0084-changing-a-table-that-holds-rows.md) | Changing a table that holds rows | Proposed |
| [0085](0085-lists-in-and-out.md) | Lists in and out: the lines of a message, the lines of a request | Accepted |
| [0086](0086-a-group-is-a-record-of-its-own.md) | A group is a record of its own | Proposed |
| [0087](0087-a-database-made-with-a-starter.md) | A database made with a starter | Withdrawn by the owner |
| [0088](0088-people-sign-in-with-a-passkey.md) | People sign in with a passkey, invited by a link | Proposed |
| [0089](0089-the-copy-beside-the-documents.md) | The copy beside the documents: how the analytical engine is fed and asked | Proposed |
| [0090](0090-totals-by-group-in-a-query.md) | A total among the columns of a QUERY: one line for each group | Proposed |
| [0091](0091-a-kept-value-that-follows-its-table.md) | A kept value that follows its table | Proposed |
| [0092](0092-the-app-system-and-its-tables.md) | The app System and its tables: what the database holds, read as any table | Proposed |
| [0093](0093-a-kept-value-that-follows-the-clock.md) | A kept value that follows the clock, and the time zone of a database | Proposed |
| [0094](0094-the-rows-of-a-table-stay-in-the-store.md) | The rows of a table stay in the store, and the copy holds them too | Proposed |
| [0095](0095-how-the-stays-of-a-state-spread.md) | How the stays of a state spread: the middle stay and the slow tenth | Proposed |
