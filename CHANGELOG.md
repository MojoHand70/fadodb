# Changelog

All notable changes to FadoDB are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

Phase 8 started on 2026-11-25.

### Added

- A command says which version of a document its sender read
  (ADR-0078, item 2, the first step of the record). `set`, `reset`,
  `refresh`, `transition` and `delete` take the member `version`, in Go
  the option `Saw`; when the document is at another version the command
  is refused with the new code `stale_version`, status 409, which names
  the version the document is at, and nothing is written. `preview` and
  `preview_delete` take it and answer the refusal the command would
  get. `transition_many` and `preview_many` take `versions`, one for
  each id and `null` for none, in Go `SawEach`: a document that moved
  on is refused among the outcomes, and the others move. The event of a
  command that was taken keeps the version as `seen_version`, in the
  answer of `events`, on the stream of changes and in the view
  `<Class>_events`; what the engine does by itself names none. A
  command without the member acts on the document as it is, and a
  command sent again with its key is answered as the first was. The
  door for agents tells an agent to send it, and each tool describes
  the member. The guide is
  [act only on the version you read](docs/manual/guides/act-on-what-you-read.md).
- A table as the source of a `QUERY` (ADR-0076, item 9, which the
  list of documents left unbuilt). A live variable, or a triggered one
  that keeps the list, lists the rows of an explicit table:
  `QUERY(Prices, {sku, price}, band = THIS.band, {-price})`. A row has
  its columns and no id; left to itself the list is sorted by the key
  of the table; the reader of a live list is asked the `read` line of
  the table, as by `table_rows`; and the class is noted as a reader of
  the table. Not built: a figure over a live list of a table.
- A list that is kept (ADR-0078, item 5, the fourth step of the
  record). A triggered variable whose whole formula is a `QUERY` keeps
  the answer with the document and with each of its versions, as
  lines somebody entered: `"type": "rows"` with a `schema` that has
  the columns of the `QUERY` and their types. It is brought at the
  triggers the variable lists, `before:` a transition among them, with
  the engine's eyes, one list for everybody, and brought again by a
  `refresh`, by whoever its `write` right lets. A guard, a rule and a
  computed variable read it as they read entered lines:
  `ALL(settled, state = "paid")`, `SUM(settled.net)`. Its `read` right
  says who sees it; a refusal `guard_false` shows it under `brought`;
  a list longer than 10,000 rows is not kept and says
  `too_many_elements`. The guide is
  [keep the lines a decision stood on](docs/manual/guides/keep-the-lines-a-decision-stood-on.md).
- A guard reads its document, and what it needs from outside is
  brought first (ADR-0078, item 4, the third step of the record). A
  triggered variable takes a fifth trigger, `before:<transition>`: it
  is worked out when somebody asks for the transition, after the
  values the command sets and before the guard is asked, with
  `STATE()` the state the transition leaves. When the guard holds, the
  value is kept in the version the move makes, with that version as
  its moment, and it is not worked out again by an `exit:`, a
  `transition:` or an `enter:` of the same move. When the guard does
  not hold, nothing is written, and the refusal `guard_false` says
  under the new member `brought` what each such variable was, to a
  caller who may read it (in Go, `Error.Brought`). A value that cannot
  be brought refuses the transition with the code of the failure and
  the variable. `transitions` and `preview` bring it too and write
  nothing, and the bundle marks the guard of such a transition as the
  server's. A transition that fires by itself, by its guard or by its
  time, brings nothing. The guide is
  [bring what a condition needs](docs/manual/guides/bring-what-a-condition-needs.md).
  `BenchmarkTransitionBefore` joins the gate.

- The `write` right of a triggered variable says who may bring its
  value again (ADR-0078, item 3, the second step of the record). A
  `refresh` that names a variable asks the variable's `write` right of
  its caller, in the state the document is in, and is refused with
  `forbidden`, the variable and the cell when it does not hold; one
  that names none brings what its caller may bring, leaves the rest as
  it is, and is refused when the caller may bring none. A variable
  with no cell is open to whoever may read the document, as before,
  and `access.update` is not asked. What a move of the document brings
  and a rule's refresh of its own document ask nothing; a rule's
  refresh of another document asks the right of the principal whose
  command queued it. **A class that has a `write` right on a triggered
  variable, which asked nothing until now, closes its refresh to the
  callers the right does not let through.** The guide is
  [say who may bring a value again](docs/manual/guides/who-may-bring-a-value-again.md).
- The engine schema is 14, for `seen_version` on the events. **A
  database that v0.7.0 made, of schema 13, is brought to it when it is
  opened**, in the transaction that opens it: the column is added, the
  view of the events of every class is made again, and nothing the
  database holds changes. It is the first time a database is brought
  forward and not made again. v0.7.0 no longer opens the file
  afterwards: keep a copy if you may go back. A database of a release
  before v0.7.0 is refused as before.

- People sign in with a passkey, invited by a link (ADR-0088, its
  second step): their fingerprint, their face or their device's PIN,
  and nothing to type. An administrator makes an invitation, good once
  and for 7 days, with `create_invitation`, or `fadodb user invite` on
  the machine, the way back in when every administrator has lost every
  passkey; at `/auth/invite` the person names the device and adds a
  passkey on it, and is signed in. The sign-in page has one button,
  **Sign in with a passkey**: the passkey says who the person is. The
  account page lists a person's passkeys, adds one on the device in
  use and removes any but the last; an administrator removes anybody's
  with `remove_passkey`, and the sessions a passkey began end with it.
  `list_invitations`, `withdraw_invitation` and `list_passkeys` join
  them; `create_invitation` is no tool of the door for agents. The
  server checks each passkey with code of its own, `internal/webauthn`,
  Go's standard library alone: ES256, Ed25519 and RSA keys, the
  challenge it gave, good once and for 5 minutes, its address, the
  person verified, the signature, and a counter that must rise; a
  counter that did not is refused and recorded. The new refusals are
  `unknown_invitation` and `unknown_passkey` (404). Passwords and the
  second step still sign a person in, under **Sign in with a
  password**, until they go (ADR-0088, step 4).

### Fixed

- **A batch wrote the lists of its first document into the others.**
  `create_many` gave a document that set no lines, or no tags, the
  lines and the tags of the document before it in the batch, and
  `transition_many` wrote the rows and the array values of the first
  document it moved over those of every document after it, a list a
  rule had emptied among them. One map of a commit was not emptied
  between the documents of a batch. It came with the functions over
  the lines a document holds (ADR-0081, step 2), and is in v0.7.0. A
  single create or move was never affected. A database that was
  loaded or moved in batches by v0.7.0, with classes that hold rows or
  arrays, may hold wrong lists: the history has each document's lists
  at the version before the batch.
- A count, a sum or another function over the lines of a document was
  not worked out again when a `reset`, by a command or by a rule,
  emptied the lines: the document kept the old figure beside no lines.
  It is worked out again now. In v0.7.0 too.

### Changed

- **A `LOOKUP` or a `QUERY` written in a guard is refused when a class
  is defined**, with `not_allowed` at the guard and the mend in the
  hint (ADR-0078, item 4): a guard is not stored, so nothing kept what
  it saw. Give the lookup a triggered variable that lists
  `"before:<transition>"`, and let the guard read the variable. A
  class that a database already holds with such a guard still runs as
  it was written, and `due` still lists its documents; its next
  version is written the new way. The conformance scripts, the tests
  and the designer's skill that had such a guard are written again.
- The server's pages are built to Rhea's sign-in, one centred column,
  in FadoDB's colours, light and dark, with FadoDB's mark and the
  typefaces Schibsted Grotesk and Martian Mono, which the program holds
  with their licence (the SIL Open Font License). They load one script
  and one style of the server's own, and nothing from elsewhere: their
  policy has `script-src 'self'`, `style-src 'self'` (no longer
  `'unsafe-inline'`), `font-src 'self'` and `img-src 'self'`.

## [0.7.0] - 2026-11-24

### Added

- Phase 7, the browser VM and the reference frontend, is closed, with
  its measurements in `docs/perf/v0.7.0.md`: every budget of its
  records holds. Not in this release, by the owner's word of the same
  day: the phase's test, in which an agent builds an application from a
  story told in plain words and its people use it in a browser, which
  runs when the owner's face is finished; signing in with a passkey
  (ADR-0088), so that a person still signs in with a password, and an
  administrator or a designer with a code from an authenticator app as
  well; and lists sent by an action and taken from its answer
  (ADR-0085). Built and still proposed: ADR-0079 to ADR-0084 and
  ADR-0086. The engine schema is 13: a database made by v0.6.0 is
  refused, as before 1.0 every database of an earlier schema is, and
  there is no way to upgrade one yet.

- Files through the door for agents, from a folder (ADR-0064, ruling 4,
  items 31 to 38): `fadodb mcp --files FOLDER` offers `put_file`,
  which reads a file of the folder, puts its bytes and answers its
  facts, for `create` or `set` to give a variable of the schema File,
  and `get_file`, which writes a file a document holds into the
  folder, by its hash, under the rules of the document. Nothing
  outside the folder is read or written: `..`, a whole path elsewhere
  and a link that points out are refused. No file is written over, and
  a file is left only when its bytes have the hash asked for. The bytes
  never travel in a tool's arguments or answer, and the door at the
  server, which has no folder of the caller's, offers neither tool.

- The address of a sign-in behind a proxy (ADR-0088, item 8, the first
  of its five steps): the setting `proxies` names the proxies in front
  of `fadodb serve`, each an address or a range such as `10.0.0.0/8`.
  From a request of one of them, a sign-in is recorded, and failed
  sign-ins are counted, at the last address of `X-Forwarded-For` that
  is not a proxy; from any other address the header is not read.
  Before, every person behind Caddy or nginx had the proxy's address,
  and twenty failures of anybody made everybody wait.

- An agent waits for a change through the door for agents (ADR-0064,
  ruling 3, items 22 to 30): the command `wait_for_changes`, a tool at
  both doors, `POST /v1/wait_for_changes` and the Go call
  `WaitForChanges`. It takes what `subscribe` takes and `seconds`, up
  to 60, and `limit`; it answers the events after its place that the
  caller may hear of, or waits for the first, with `after`, the place
  to ask from next, and `more`. A caller who acts for its person, as
  Claude does at `/mcp`, hears of what it may read for them. At the
  server a wait holds the place of a stream and not a command's slot.
  `after` takes `start`, before the first event of the log, for a wait
  and for `subscribe`.

- A list narrowed, ordered and cut inside a function over it (ADR-0081,
  step 3, its second group): `FILTER`, `SORT`, `TOP` and `UNIQUE`. On
  a bill, `FIRST(SORT(lines, {-price}).what)` is the dearest thing,
  `SUM(TOP(SORT(lines, {-price}), 3).price)` the three dearest lines
  added up, `JOIN(FILTER(lines, NOT delivered).what, ", ")` what is
  still to deliver, and `JOIN(UNIQUE(lines.what), ", ")` the different
  things; the same over a list that is a `QUERY`, in a live variable.
  Such a list stands only inside a function over a list, so a formula
  always answers one value; written by itself it is refused. A column
  of what they give is written after the call,
  `FILTER(lines, delivered).price`, which is new in the grammar. Each
  is a part of the function's one statement, in SQLite and in DuckDB,
  and fifty-three cases prove one answer. `FILTER` takes a condition,
  as `COUNTIF` does, and no mask. With these every function of the
  catalogue is built.
- One value out of a list (ADR-0081, step 3, its first group):
  `FIRST`, `LAST`, `INDEX`, `JOIN` and `CONTAINS` work over a list of
  the document. On a bill, `FIRST(lines.what)` is what its first line
  is for, `INDEX(lines.what, 2)` the second, `JOIN(lines.what, ", ")`
  all of them as one text, and `CONTAINS(tags, "urgent")` whether it
  has the tag; over a list that is a `QUERY`, in a live variable,
  `FIRST(invoices.number)` and `JOIN(invoices.number, ", ")` follow
  the order the list has for its reader. `INDEX` at a place the list
  does not have is the error `out_of_range`. The functions were in
  the catalogue, written for arrays, and refused as not built; their
  entries now say what they are over a list. Each has its SQL for
  SQLite and for DuckDB, and forty cases prove one answer. They are
  not taken in the column position of a `LOOKUP`. Not built yet: the
  functions that give a list again, `FILTER`, `SORT`, `TOP` and
  `UNIQUE`.
- A group is a record of its own (ADR-0086). A database that keeps
  its users keeps its groups: a name, which `INGROUP` and a user's
  `groups` name and which never changes, a `label` a screen shows, and
  a `description` of what the group is for. Who is in a group stays on
  the user. Six commands, each a tool of the door for agents:
  `create_group` makes a group before anybody is in it, `set_group`
  changes its label and description, `get_group` and `list_groups`
  answer groups with how many users are in each, `remove_group` takes
  away a group nobody is in, and `group_changes` lists every change to
  one. A group a user is put in gets its record in the same commit, and
  a database made before gets a record for each group its users are in
  when it is opened. A new code, `unknown_group`, answered with 404.
  The Go calls are `CreateGroup`, `SetGroup`, `GetGroup`, `Groups`,
  `RemoveGroup` and `GroupChanges`. Not yet: the System Table of
  groups.

- Figures over the lines and the tags a document holds (ADR-0081,
  step 2). The functions over a list work over a `rows` or an `array`
  variable of the document: on a bill, `SUM(lines.qty * lines.price)`
  is its net, `COUNT(lines)` how many lines it has,
  `COUNTIF(lines, NOT delivered)` how many are not delivered, and the
  guard `COUNT(lines) > 0 AND ALL(lines, qty > 0)` lets a bill be sent
  only with lines that have a quantity. Such a figure reads its own
  document, so it is the same for every reader and stands in a
  computed variable, a triggered one, a guard, a rule and a live
  variable; it is worked out again in the commit that changes the
  list. The one argument may be an expression of the columns of the
  list, and in a condition an element of an array is `value`:
  `ANY(tags, value = "red")`. Before, a formula that named a list was
  refused with "composite values are not supported yet". Not yet, each
  refused in words: a figure over the lines in a read rule, a read
  right, a filter or a title; a page asks the server for one. The
  layout of a database and the format of a program do not change.
- The door for agents at the server itself (ADR-0064, ruling 1).
  `fadodb serve` offers every command as a tool at `/mcp`, in the
  Model Context Protocol over HTTP, for an agent that cannot start a
  program on a machine: Claude on claude.ai connects with the pass a
  person gives it (ADR-0071) and acts as `ann~claude.ai` for Ann, and
  an agent's program that sets a header sends a key. Both forms of the
  protocol are spoken, the revision of 2026-07-28 and the ones that
  open with a greeting; the door keeps no session and answers each
  message with one JSON object. A request that proves nobody is told
  where passes come from, at
  `/.well-known/oauth-protected-resource/mcp`; a pass for the commands
  is refused there, as one for `/mcp` is at `/v1/`; a page of another
  site is refused. A tool runs as a command sent to `/v1/` does, with
  the server's limits, and its request is a line of the log with
  `"door":"mcp"`. The server says where agents connect when it starts,
  and the counts gain the status 202. Settled while building, the
  owner's to strike: ADR-0064, items 11 to 21.
- The mean, the middle value and the different values of rows
  (ADR-0081). `AVERAGE(invoices.net)`, `MEDIAN(invoices.net)` and
  `COUNTUNIQUE(invoices.state)` are figures over a list in a live
  variable, beside `COUNT`, `SUM`, `MIN` and `MAX`, and
  `AVERAGEIF(invoices, state = "draft", net)` is the mean of the rows
  that meet a condition. The same three stand in the column position
  of a `LOOKUP`: `LOOKUP(Rates, AVERAGE(rate), currency = THIS.currency)`,
  which was refused before with the advice to divide a sum by a count.
  The mean of exact values is exact, with four places more than the
  column, as `AVERAGE` of single values has; the middle value has one
  place more, and of an even number of values is the mean of the two
  in the middle; `ROUND` cuts either. `COUNTUNIQUE` counts the values
  that differ as `=` compares them, and money in two currencies is two
  values. `MEDIAN` and `COUNTUNIQUE` work over rows alone and are
  refused over single values. Each has its SQL for SQLite and for
  DuckDB, and the lookup suite proves one answer on all the executors.
  Three functions are new in the catalogue. The layout of a database
  and the format of a program do not change.
- Who removed a table or words, and when (ADR-0083, proposed).
  `remove_table` and `remove_words` write a line in the log of
  definitions, in the transaction that removes: who removed it, what
  they were, for whom they acted, when, and the version it had. The
  words that go with a table write a line each. `definitions` answers
  that version with `removed`, in Go `Definition.Removed`, to database
  administrators and designers and to an agent working for an
  administrator the database keeps, the owner's ruling; anyone else
  gets the versions without it, and is not refused. `definitions` now
  takes its caller, and `for`; in Go, `Definitions` takes options. A
  refused removal writes nothing. The engine schema stays 13: a database made
  before opens as it is, and an engine made before reads one made now
  as it did, without the removals.
- Changing a table that holds rows (ADR-0084, proposed), with the
  owner's two checks. A version of a table that holds rows, or that a
  class reads, is taken when it keeps its key, every value of a changed
  column converts to the new type exactly, and no formula or rule of a
  class uses a column it changes or removes. Its rows are carried over
  in the same transaction. A value that does not convert is refused
  with `bad_value`, naming the first row by its key; a use with
  `not_allowed`, naming the class and the place.
- Figures over a list, the first step of ADR-0081: a live variable
  may count a list of its document or add up a column of it. In the
  class Order, beside the list `invoices`, `COUNT(invoices)` is how
  many invoices the order has and `SUM(invoices.net)` what they add up
  to; `MIN` and `MAX` give the smallest and the largest value of a
  column; `COUNTIF`, `SUMIF`, `MINIF` and `MAXIF` do the same over the
  rows that meet a condition, and `ANY` and `ALL` say whether some
  row, or every row, meets one. A figure is one statement to the
  listed class, asked for whoever reads the variable: it counts the
  rows the list has for that reader, a column they may not read gives
  a blank, and no row is read to be counted. It may stand inside a
  formula, and stands in a live variable alone: a kept value, a guard
  and a rule are refused, and count with `LOOKUP` as before. Six
  functions are new in the catalogue. Not built yet: `AVERAGE`,
  `MEDIAN` and `COUNTUNIQUE` over a list, the same functions over the
  lines and tags a document holds, and the functions that give a list
  again. `BenchmarkLiveFigure` joined the benchmark gate. The layout
  of a database does not change.
- Removing rows and removing a table (ADR-0082, proposed).
  `delete_rows` removes the rows of a table that have the keys given,
  in one transaction, as the table's write rule lets; a key the table
  does not have removes nothing. `remove_table` takes a table away with
  its rows and its words, for a designer, and is refused with
  `not_allowed` while a class reads the table or a rule writes it. A
  table defined again under its name takes the next version after the
  removed one's. In Go, `DB.DeleteRows` and `DB.RemoveTable`.
- A column added to a table in use (ADR-0080, proposed). A version of a
  table that adds columns and changes nothing else is taken while the
  table holds rows or a class reads it; its rows are blank in each new
  column. A new column of a table in use is not part of its key. Any
  other change of a table in use is refused as before, and the refusal
  says that columns can be added.
- A table belongs to an app, as a class does (ADR-0079, proposed). A
  table names its app in its definition, `"app": "Expenses"`, and an
  app the database does not have is refused with `unknown_app` at
  `/app`. `list_tables` takes `app` and answers the tables of one app;
  in Go, `DB.TablesOf`. A table moves to another app by its next
  version even while it holds rows or a class reads it: nothing of its
  columns or rows changes. Any other change of a table in use is
  refused as before. A database in which a table names an app is not
  opened by an earlier engine.
- A list of other documents on a document, the next part of ADR-0076:
  a live variable whose whole formula is a `QUERY`. In the class Order,
  `QUERY(Invoice, {number, net, state}, order = REF(), {number})` lists
  the invoices that point at the order. A list is one question to the
  documents of its source, the one the command `query` answers, with
  the id of the document it is read on in its condition: so its reader
  gets the documents the source's read rule gives them, and in each
  the columns they may read. Several columns, and several to sort by,
  are written in braces. Inside the condition a bare name is a column
  of the listed document, `THIS.name` a variable of the document the
  list belongs to, and `REF()` the pointer to it. The command `live`
  takes `order`, `limit`, `offset` and `fields` for a list and answers
  `columns`, `rows` and `more`; in Go the page is chosen with
  `LivePage`. A list that names what its source has not is refused when
  its class is defined, and the source may not lose what a list names.
  Not built yet: a table as the source, a count or a sum among the
  columns, grouping, a `QUERY` in a triggered variable, `REF()`
  anywhere else and `REF(class, id)`. The engine does not index a
  pointer by itself yet (ADR-0075): a list on a large class wants
  `"index": true` on its pointer. The layout of a database does not
  change. New chapter of the manual: Lists of other documents.
- `BenchmarkLiveList` joined the benchmark gate, as ADR-0076 says: the
  hundred invoices of one order, read through the index of their
  pointer with the whole call of the command `live`. The gate watches
  it among 10,000 invoices. The budget of the record, under 1 ms among
  1,000,000, was measured on disk on 2026-11-23 at 0.52 to 0.55 ms;
  `BenchmarkLiveListPage`, the first twenty of the hundred, at 0.25 to
  0.29 ms.
- A `ref` is checked when it is set, and a class says what points at
  it: two more parts of ADR-0076. A commit that sets a `ref` to a
  document which is not there, or which its caller may not read, is
  refused with `unknown_document`, the same answer either way; a `ref`
  the document held already is not asked about again. **This refuses
  what was taken before**: load the documents pointed at before the
  ones that point at them. `get_class` answers under `pointed_at_by`
  the `ref` variables of the classes defined now that point at the
  class. The layout of a database does not change.
- A live variable, the first part of ADR-0076: a computed variable may
  say `"mode": "live"`. It is worked out when somebody reads it, for
  that reader, and kept nowhere: it has no column, no history and no
  event, and no read of a document carries it. A new command reads it,
  `live`, in Go the call `Live`: whoever may read the document asks,
  and the variable by its read right. What speaks of a kept value is
  refused on it (`on`, `default`, `required`, `values`, `min`, `max`,
  `index`, `list`), and nothing that is kept or decided may read it: no
  other formula of the class, no guard, no access rule, no filter. In
  the bundle it is the server's. A live formula reads its own document,
  the caller, the clock, the state and the life of the document; one
  that looks a value up, with `LOOKUP`, is refused as not built yet,
  and a `LOOKUP` of another class cannot name a live variable of its
  source. The layout of a database does not change. New chapter of the
  manual: Live variables.
- ADR-0075 and ADR-0076: the owner ruled on 2026-11-22 on the three
  questions they had put, and none is open. A search that no index
  serves on a large class is stopped after five seconds and told which
  index would serve it; `QUERY` writes several columns in braces,
  `{number, net, state}`; a `ref` that names a document which is not
  there is refused when it is set. Both records stay proposed, and
  nothing of them is built.
- A sixth skill for agents, `skills/fadodb-face/`, at the owner's
  direction of 2026-11-21: how to build what people see of a server
  so that it is good to look at and looks like every other face of
  that server. An agent reads the server's look before it draws;
  with none, it defines a direction with the person, shows it to them
  drawn, and stores it; once a person approves, the look is the visual
  identity of the server and every face follows it. A test on each
  push sends the commands of the page and compares the answers.
- A fifth sample class, `examples/classes/look.json`: the look of a
  server's faces as a document, written in the DESIGN.md format. A
  designer drafts it, an agent among them; only a person of the group
  `brand` approves it; an approved look is not changed, and of two
  the one approved last holds. It is an ordinary class: the engine is
  unchanged, and no database has to be made again.
- ADR-0060, the organisation's rule-book, accepted by the owner on
  2026-11-21, after its part on the look was rewritten as he asked
  that day, with his answer to where a look lives: on the server. The
  look and the face builder's skill are built; the things the
  organisation keeps, and a starter that gives a new database both
  classes, are decided and not built yet.
- ADR-0075, ADR-0076 and ADR-0078 accepted by the owner on 2026-11-22,
  and the records they amend say so. Nothing of the three is built yet;
  what each one decides is in the three lines below.
- ADR-0075, proposed, at the owner's direction of 2026-11-20, and
  nothing of it built: the engine makes the index of every question a
  class declares, a pointer to be followed back, the key of a `LOOKUP`,
  the variable the read rule compares with the caller, and three of
  every class, and says when a class is defined which declared question
  no index can serve. Measured that day on 100,000,000 invoices in one
  file of 92 GB: a question through an index takes 0.3 to 0.9 ms, a
  question past its index minutes, and an index added without care made
  another question slow. The record has the figures, what the indexes
  cost, what stays open and one question for the owner.
- ADR-0076, proposed, with the owner's rulings of 2026-11-20, and
  nothing of it built: a value worked out when it is read. A computed
  variable gains a third mode, `"mode": "live"`: worked out for the
  reader, with the reader's rights, and kept nowhere. `QUERY` is built
  for it, so that an order lists its invoices; `REF()` gives a `ref`
  something to compare with; a command `live` reads one page of such a
  list. Two questions for the owner.
- ADR-0078, proposed, with the owner's rulings of 2026-11-21 and
  2026-11-22 on its three questions, and nothing of it built: a
  decision stands on what the document holds. A command that changes
  a document may say which version its sender saw, and is refused
  with `stale_version` when the document has moved on; the event
  keeps the version. The `write`
  right of a triggered variable says who may bring its value again,
  and in which state. A trigger `before:<transition>` brings a value
  before the condition of the transition is asked, and a guard no
  longer looks in a table or at other documents itself, so that what
  a condition saw is on the document.
- Apps (ADR-0077): an app is a name for the classes that belong
  together, such as Expenses or a help desk, with a label and a
  description for people. `define_app` stores one, for a designer;
  `get_app` and `list_apps` read them back, for everybody. A class
  says which app it belongs to with `"app": "Expenses"` in its own
  definition; a class of an app the database does not have is refused
  with `unknown_app` at `/app`, and a class may belong to none.
  `list_classes` takes `app` and answers the classes of one. A class
  moves to another app by its next version. The door for agents says
  that there are apps, and `define_app` tells an agent to look before
  it builds. In Go: `DB.DefineApp`, `DB.Apps`, `DB.App`,
  `DB.ClassesOf`. The engine schema is 13: a database made by an
  earlier pre-release engine is recreated.
- The list of functions as a command (ADR-0074):
  `list_functions` answers every function a formula can call, each
  with its other names, its kind, which decides where it may stand, its
  arguments, what it returns, the call as the manual writes it and a
  sentence on what it answers, and marks the ones this version of the
  engine has not built. It is the catalogue the parser itself reads, so
  a formula editor needs no copy of its own, and an agent reads it
  through the door before it writes a formula; the sign above the door
  says so. In Go: `DB.Functions()`.
- The pages of a face (ADR-0073): `fadodb serve FILE --pages
  DIR` hands out the files of a folder at the root of the server's
  address, `index.html` first, so that a face made of plain files, as
  FadoDB Workshop is, needs no server of its own and signs people in
  with the server's own pages. The server's own addresses, `/v1/`,
  `/auth/`, `/mcp` and `/.well-known/`, always come first. The folder
  is only read, a fixed list of kinds of file is handed out, and
  nothing whose name begins with a dot or that a link points to
  outside the folder. Every file is sent with one header, not the
  operator's to change: a page loads script, style, pictures and fonts
  from the server alone and calls the server alone. A script of such a
  page acts as whoever is signed in, so the folder is the operator's to
  answer for. The server has no pages of a face of its own: the kit
  once planned inside it is FadoDB Workshop, in a repository of its
  own.
- Signing in, part 3 (ADR-0071, items 6 to 8): passes for clients, by
  OAuth 2.1. A client sends the person to `/auth/authorize`; the
  server's page `/auth/allow` asks "Claude (claude.ai) wants to act for
  you. Allow?"; the client exchanges its code, proven with PKCE (S256),
  at `/auth/token` for a pass of one hour, `fadoat_`, and a renewing
  pass that changes each time it is used: one used twice ends the
  grant. A pass is for one address. Claude, known by the document it
  publishes, is given passes for the door for agents, as an agent of
  the person's own, `ann~claude.ai`, which the server makes with a
  substitution the person gives, and which has the person's groups and
  system groups while it acts for them. A face or an app that an
  administrator adds with `add_client` is given passes for the
  commands, as the person. New commands `add_client`, `list_clients`,
  `remove_client`, `list_grants`, `end_grant`; codes `unknown_client`
  and `unknown_grant`; the setting `grants.idle_days`; the account page
  lists the clients a person allowed. The server publishes
  `/.well-known/oauth-authorization-server` and
  `/.well-known/oauth-protected-resource/v1`, and gives back passes at
  `/auth/revoke`. It reads a client's document over HTTPS only, at most
  5 KB, past no redirect and never from an address inside the network.
  `create_user` refuses an id with `~`. No new dependency. Three tables
  join the engine's own.
- Signing in, part 2 (ADR-0071, item 4): a company's sign-on. The
  settings name each provider of OpenID Connect under `signon`, with
  its `issuer`, `client_id`, `label` and `company_emails`; its client
  secret is found as a connection's is. The sign-in page offers a
  button for each; the server sends the person to the provider with
  PKCE, a state and a nonce, and checks the provider's signed note:
  RS256, PS256 or ES256 by a key the provider publishes, the issuer,
  the audience, the time and the nonce. A user gains `email`
  (`create_user`, `set_user`): the first sign-on finds the person by
  the e-mail the provider vouches for and ties the account to the
  user, and every later one finds them by the provider's id. An account
  with no user is refused as a wrong password is. No new dependency:
  the code is the standard library's, in a package only the server
  links. Two tables join the engine's own; a new database is about
  310 KB.
- ADR-0072, accepted, with the owner's ruling of 2026-11-18: the core
  program may be 20 MB, where it was 15. The size gate holds every pull
  request to it. `text/template`, `html/template` and calls of methods
  by their names stay out of the core: they keep every method of the
  program.
- Signing in, part 1 (ADR-0071): passwords, the second step and
  sessions. People sign in to `fadodb serve` on its own pages under
  `/auth/`: a password, kept as a PBKDF2-SHA256 hash of the standard
  library at 600,000 rounds, set through a link good once and for a
  day, which `fadodb user password` makes on the machine and the
  command `create_password_link` through the server; a code from an
  authenticator app (TOTP) after the password of an administrator or a
  designer, set up at the first sign-in, with ten codes for a lost
  phone; and a session the browser keeps, 8 hours unused and 30 days at
  most, with which the server's own pages send commands. The account
  page changes the password, sets up or takes away the second step and
  ends sessions. Every sign-in is logged, refused ones too; sign-ins
  that keep failing wait. New commands `list_sessions`, `end_session`,
  `clear_second_step` and `sign_ins`; new codes `sign_in_refused`
  (401), `too_many_attempts` (429) and `unknown_session` (404). The
  settings gain `url` and `sessions`. Five tables join the engine's
  own; a database of the same engine schema opens and gains them. The
  chapter [Signing in](docs/manual/signing-in.md) is played against a
  server.
- ADR-0071, proposed, with the owner's rulings of 2026-11-18: signing
  in. `fadodb serve` signs people in, with a password (PBKDF2 of the
  standard library) and a second step from an authenticator app,
  required for administrators and designers, or with the company's
  sign-on (OpenID Connect: Microsoft, Google and others), and gives out
  passes: a session for its own pages, and OAuth passes for every other
  client, an agent at `/mcp` among them. An administrator adds a person
  first; a person's Claude is an agent of its own that acts for them
  with their groups. No new dependency.
- The bundle (ADR-0070), built. The command `get_bundle` answers a
  class in one call: its definition with the words of the person's
  language put in place, looked for as a browser's tag falls back
  (`pl-PL` finds `pl`), the schemas it uses, and every formula a page
  runs to show a document and check it before it is sent, compiled for
  the browser VM: the title, the defaults, the computed variables in
  their order with what each reads, the access rules but the read
  rule, the guards, and the rights worked out for each state and
  variable with the cell that decides. A formula that reads other
  documents, a table or the history of states is marked as the
  server's. A page that sends its `tag` is answered `unchanged`;
  `GET /v1/bundle?class=…&language=…` answers the same with the tag as
  the `ETag` and 304 to a browser that holds it. It carries no document
  and no row of a table, and every caller may read it. `fado.DB` has
  `GetBundle`. The chapter [The bundle](docs/manual/the-bundle.md) is
  played by a test, and `make vmtest` works out every answer of two
  stories in the browser VM from the bundle alone and compares each
  with the server's.
- ADR-0070, proposed, with the owner's rulings of 2026-11-18: the
  bundle, a class, its words and its compiled formulas in one call, so
  that a page works a document out as the server does without asking it
  at each key press. One command, `get_bundle`, with a `tag` that
  answers `unchanged`, and a `GET` form for the browser's cache. The
  words of the person's language are put in place in the class; the
  rights come worked out for each state and variable; a formula that
  reads other documents, a table or the history of states is marked as
  the server's, and the page asks `preview`. No document and no row of a
  table is ever in it, and it is the same for everyone.
- Words in other languages (ADR-0068, item 6; ADR-0069, item 6),
  built. The words of a class, a table or a schema in one language are
  a definition of their own beside it: its label and description, and
  those of each variable, column (`lines.qty`), section, state and
  transition by its path, or of a table's columns and a schema's fields.
  The commands `define_words` and `remove_words`, for designers, and
  `get_words` and `list_words`, for every caller; `fado.DB` has
  `DefineWords`, `ListWords` and `RemoveWords`, and `Definition` and
  `Definitions` read them under `words` and the name
  `class/Invoice/pl`. The language is a tag of BCP 47 written as the
  standard writes it. A path the current version does not have is
  refused with `bad_value`; a later version of the class that drops one
  leaves the words noted, `unused_words`. Words make no new version of
  what they translate and touch no document; each of their versions is
  kept with who defined it. Two tables, `words` and `words_versions`,
  join the engine's own; a database of the same engine schema opens and
  gains them. A new database is about 250 KB.
- Sections (ADR-0069), built. A class may declare `sections`, the
  outline of its variables: a section has a `label`, a `description`,
  the `variables` it holds and `sections` inside it, at most four deep.
  A variable is in one section at most; a name is unique in the outline
  and apart from the variables'. A variable listed twice, a section
  named like a variable or like another section, an unknown variable and
  an empty section are refused with their place, a fifth level with
  `too_deep`; a section without a description gets the note
  `no_description`. In `rights` a section stands where a variable
  stands: the rule of a variable is looked for in its own cell, then in
  its section's and each section around it, then in `*`, in the state
  and then in the state `*`, so the most specific name wins. The rule is
  resolved once when the class is laid out. A refusal of a write names
  the cell that decided in its hint, `review / amounts`. The chapter on
  the class shows the outline and plays the record's table; the sample
  invoice and purchase have sections, and `docs/architecture.md` no
  longer describes layouts.
- What a class says to people (ADR-0068), built. A variable takes a
  `label`, a `description`, `"list": true` for a column The List may
  show, and a `format` in Excel's patterns; the states block takes
  `labels` and `descriptions` by state; a transition takes a `label` and
  a `description`; a column of a `rows` variable or of a table and a
  field of a schema may be written in a long form,
  `{"type": "int", "label": "Quantity"}`, beside the short one. The
  engine checks a format when the class is defined and refuses one it
  does not read, with the offset in the pattern; it never applies one,
  and a value on the wire is as it was. Each variable, state and
  transition without a description gets the note `no_description`, which
  refuses nothing. The engine's own class `Substitution` and the four
  sample classes describe everything. The words in other languages are
  not built yet.
- ADR-0069, accepted by the owner, with his rulings of 2026-11-17: the
  screen is the face's, and a class has sections. The engine keeps no
  layouts: a face draws a document from the class, its words and its
  compiled formulas, and a condition it needs is a computed variable of
  the class. A class may declare `sections`, an outline of its
  variables with a label, a description and words in other languages,
  nested at most four deep, one section per variable; `rights` may name
  a section, and the most specific name wins. ADR-0054 is withdrawn.
- ADR-0068, accepted, with the owner's rulings of 2026-11-17: what a
  class says to people. A `label` and a `description` on a variable, a
  state and a transition, and on a column and a field in a long form;
  `"list": true` for the columns The List may show; `format` in Excel's
  patterns, applied in the person's language; the words of a class in
  other languages as definitions beside it; a note for each missing
  description. Nothing of it is built yet.
- The browser VM, part 1 of 4 (ADR-0029): the engine's formulas run in a
  page, from the bytecode the engine compiles, with the same answers as
  the server. TypeScript under `web/vm/`, built into one ES module and
  one classic script that defines the global `FadoDB`, 24.7 KB each of
  the 100 KB budget. Part 1 is the machine with every instruction but
  those of exact numbers, plain numbers worked out to the last bit as Go
  works them out, text by code point, logic, IFERROR and the context of
  the caller. On every push, `make vmtest` builds it and runs every case
  of the conformance suite through the built file: 748 pass, none fails,
  640 wait for the dates, the exact numbers and money of parts 2 and 3,
  and the 86 that read other documents are the server's. 3,000 numbers
  are rendered as text as Go renders them. Not yet published: the files
  go with a release when the four parts are built.
- The browser VM, part 2 of 4: dates and moments, and the functions of
  a document's life (ADR-0023, ADR-0047). `DATE`, `DATEVALUE`,
  `DATETIMEVALUE`, the parts of a date, the distances between two and
  the moves of a date by days, months and years run as the server runs
  them, on whole days with Go's carrying of a month that runs over, so
  that year 1 to year 9999 come out alike; a document's life comes in
  the context in the form the engine writes it. 1,009 cases of the suite
  pass, none fails, and the 379 that wait are all exact numbers and
  money; 4,200 calls of the functions of dates drawn at random answer
  what the Go VM answers. 27.8 KB.
- The browser VM, part 3 of 4: exact numbers and money (ADR-0022), with
  the mantissa a `BigInt` held to the 64 bits of the engine's, so that
  what overflows on the server overflows on the page: the exact
  operators and comparisons, the six methods of rounding, `DECIMAL`,
  `NUMBER`, `MONEY`, `MONEYVALUE`, `DECIMALVALUE`, `AMOUNT`, `CURRENCY`,
  and the exact paths of `ROUND`, `FLOOR`, `CEILING`, `TRUNC`, `ABS`,
  `SIGN`, `MIN`, `MAX`, `SUM`, `AVERAGE` and `MOD`. Every instruction
  and every function a page runs is built: every case of the suite a
  page runs passes, 1,388, and 4,800 calls of exact numbers and money
  drawn at random answer what the Go VM answers. 34.7 KB.
- The browser VM, part 4 of 4, so it is built (ADR-0003, ADR-0029): a
  document of 200 formulas recalculates whole in about 13 µs on the
  built file, against the budget of 1,000 µs, and in about 42 µs with
  decimals or money, which ADR-0029 asked to be measured and published;
  `make vmtest` fails when the budget is reached. `fadodb-vm.d.ts`, the
  declarations, one file beside the two, checked as a page in
  TypeScript uses them and against what the module exports. Every
  release attaches the three files, with their checksums beside the
  binaries'.
- The browser VM on npm as `fadodb-vm`, version 4.0.0: its major
  version is the bytecode format version, by the owner's ruling of
  2026-11-17. The release workflow publishes it by itself through npm's
  trusted publishing, once the owner has published 4.0.0 by hand, which
  npm asks of a new package, and named the workflow its trusted
  publisher.
- Phase 7, the browser VM and the reference frontend, has started;
  the roadmap says what the owner ruled it holds.
- The log of reads (ADR-0067, accepted). A content administrator who
  gives a reason sees past the rules, and every document, row,
  variable and file only the override showed is written down before
  the answer leaves, with the rules it got past and the reason: one
  entry for each call, naming every document; a count says how many
  it counted past a rule. Every SQL statement is written down too, with
  its arguments and how many rows it returned, or the code it failed
  with, never the rows. So is a content administrator's import into a
  table past its write rule, and a row a rule's action writes past it.
  Database administrators, and nobody else, read the log with the new
  command `reads` (in Go `Reads`), narrowed by caller, class and
  document, table, command and period; SQL cannot read it, and nothing
  deletes an entry. If the entry cannot be written, the read fails and
  shows nothing. The chapter
  [The log of reads](docs/manual/the-log-of-reads.md) plays the case.
- The member `reason` on every command that reads a document or a
  table, on `flow`, `subscribe`, `sql` and `import_rows`, and the
  parameter `reason=` on the addresses of files.

### Changed

- **A content administrator reads past a rule only with a reason.**
  Without one they read as anybody in their groups: a document only
  the override would show them is refused with `reason_required` and
  the place of the rule, a list and a count leave it out, the stream
  does not tell of it, and a variable a right hides is hidden. This
  holds for a host program that lists `due` as a content
  administrator, and for the answer of a change. A class the engine
  brings, `Substitution`, is seen as before.
- **Opening bytes by their hash alone needs a reason**, every time.
- **A content administrator's import into a table, or read of its
  rows, past the table's rule needs a reason**, and an action a rule
  queued writes a table past its rule only with the reason of the
  commit that queued it; without one it is refused with
  `reason_required`.
- The engine schema is 12, for the log of reads. A database made by
  v0.6.0 is refused by this release, as before 1.0 every database of
  an earlier schema is: there is no upgrade path yet.

- The chapter on containers says where the image is pulled from,
  `ghcr.io/mojohand70/fadodb:0.6.0`, now that v0.6.0 has published it.

### Fixed

- The designer's skill warns that `ACTINGFOR()` is blank for a caller
  who acts for nobody, and a blank equals a blank: compared with a
  value that is still empty, such as an approver filled in later, it
  lets every caller through, so the skill says to ask for the value
  first. The operator's skill no longer says that the door for agents
  at the server, `/mcp`, is not built.

- **The README, the roadmap, the manual and two skills said things of
  Phase 7 that were no longer true.** The README named nothing Phase 7
  built and still called the log of reads not built; the roadmap and
  the manual called the open face in development, after work on it
  stopped on 2026-11-21; the manual said FadoDB checks no passwords,
  that the browser will be the third place a formula is worked out, and
  that it stood at release v0.5.0, with the server still to come. Each
  now says what works on main, what is left of the phase in its order,
  and which records wait for the owner's acceptance.
- **The pages where people sign in made Firefox write a refusal in its
  console.** They named no icon, so Firefox asked the server for
  `/favicon.ico`, and the header of those pages lets nothing load. The
  pages now name an empty icon, written in the page itself, and their
  header lets a picture written in a page be (`img-src data:`). Nothing
  is loaded from anywhere, as before, and nothing a person saw was
  wrong. Found by opening FadoDB Workshop in Firefox.
- **The sign-in pages took no form from a real browser.** They sent no
  referrer at all, so Chromium sent the `Origin` of every form as
  `null`, which the server took for another site and refused with 403:
  setting a password and signing in among them. The tests had sent
  `Origin` themselves. The pages now send a referrer to the server
  itself alone, and a form whose `Origin` is `null` is taken when the
  browser says, in `Sec-Fetch-Site`, that it is the server's own.
  Found with headless Chromium, which then went through a whole
  connection of a client.

### Security

- A read past a rule leaves a trace. A content administrator's read of
  what the rules hide, and every statement of an analyst's SQL, are in
  the log of reads with who, when and why; v0.6.0 said that a read
  left none.

## [0.6.0] - 2026-11-17

### Added

- Phase 6, the server, is closed. The owner accepted the records that
  were built as they proposed, with his rulings on their questions:
  ADR-0062, commands from a terminal; ADR-0063, the flow of a class;
  ADR-0064, the door for agents; ADR-0065, the way in for messages
  from outside; and ADR-0066, what the budget of the server is
  measured beside. `BenchmarkReceive` and `BenchmarkTransitionKeyed`
  join the benchmark gate, as ADR-0065 says. Not in this release, and
  wanted, each as its own step: the door for agents at the server
  itself, which comes with the signing in of the frontend; a tool
  with which an agent waits for a change; and files through the door.
- The release publishes the image for containers at
  `ghcr.io/mojohand70/fadodb`, under its version and under no other
  name, so that a server never changes its engine by itself.

- The manual, under `docs/`: its contents page with every chapter
  planned, and the first three written: your first application, an
  invoice with its own editors in each state, from an empty file to a
  working process; the class definition, block by block and word by
  word; and the glossary. Two more follow them: what FadoDB is, and
  how it works, a commit followed step by step from the call to the
  answer. A test runs every example of a chapter
  against the engine on each push, and another follows every link.
- `examples/first`, the program of the first application:
  `go run ./examples/first`.
- Five guides in the manual, one task each, under `docs/manual/guides`:
  give each state its own editors, make a field read-only after
  approval, four eyes, a reminder every seven days with a deadline,
  and let somebody act for a person. A test defines the class of each
  guide and plays its table of who does what against the engine.
- Two chapters of the manual are generated from the engine: every
  function of the formula language, with the ones this version does
  not have yet marked, and every code of a refusal with its meaning.
  `make docs` writes them again, and a test fails when they fall
  behind the engine.
- Four more guides, each played by a test: look a value up in a
  reference table, move many documents at once, change a class that
  already has documents, and do something outside when a document
  changes.
- The first three chapters on the notions, under
  `docs/manual/notions`: classes and documents, variables and
  formulas, states and transitions. Each explains the idea with one
  class and a story of named people, and a test plays the story and
  every table of the chapter against the engine, the document it
  shows and the chart it draws included.
- Two more chapters on the notions: rules and actions, with what a
  rule does inside a commit set apart from what the engine runs after
  it; and who may, with the caller, the questions a class answers, who
  edits in which state, how a right is found in the table, the four
  system groups and the content administrator.
- The last two chapters on the notions: history and the log, with
  what an event carries, every kind of event, the key of a call sent
  twice and the business day; and time, with a transition that carries
  its time, a timer that was missed while no program ran, and reads as
  of another day. All seven notions are now written.
- The reference chapter on the formula language: how a formula is
  written, the operators, what mixes with what, blanks, text, plain
  and exact numbers, days and moments, objects, what a failure means
  and what a formula may read in each place. A test adds each of its
  120 examples to the chapter's class as a computed variable and
  compares the engine's answer and the type of the result with the
  chapter.
- The reference chapter on tables: the definition and the types of a
  column, rows and imports, a formula that reads a table, the three
  actions of a rule that write a row, who may, changing a table, what
  is checked and what is refused. It shows a table of exchange rates,
  a class that reads it and a class that feeds it; a test plays the
  story of 21 steps and holds every table of the chapter to the
  engine's answers.
- The reference chapter on schemas and objects: how a schema is
  defined, the nine schemas the engine brings, an object in a class,
  the values as they are given and answered, how an object is stored,
  and a table of what a class can and cannot do with an object today.
  A test plays the chapter's story and holds every table of it to the
  engine's answers. The chapter and the class definition now say that
  `required` on an object, a list or rows is accepted and not
  enforced.
- The reference chapter on files: the facts of a file and its bytes,
  attaching, replacing and taking off as versions of the document,
  opening a file under the rules of the document, who may, the three
  stores for the bytes, the limit of a file and the check. A test
  plays the chapter's story and holds every table of it to the
  engine's answers, the three stores and a file changed on disk
  included.
- The first chapter for the developer who embeds the engine, opening
  a database: a whole small program, `examples/open`, what opening
  does, what is on disk, every option and whether it belongs to the
  database or to the program, closing, and what is refused. A test
  runs the program and plays every table of the chapter; the table of
  options is held to the options the package has.
- The chapter on naming the caller, for the developer who embeds the
  engine: a whole small program, `examples/caller`, with four callers
  on one invoice; every part of a caller, what the engine uses it for
  and what it checks; a call with no caller; why the program is the
  only door; the system groups, the business day and the options of a
  call. A test runs the program and plays every table; the tables of
  the caller's parts and of the options are held to what the package
  has.
- The chapter on the outbox and the worker, for the developer who
  runs a database: a whole small program, `examples/outbox`, with an
  action that gets no answer twice and one that is refused until an
  administrator sends it again; a row of the queue, how the worker
  runs it, what happens when an action fails, a dead action, running
  the queue by hand, and who may. A test runs the program and plays
  every table of the chapter.
- The operator's skill is written, and no longer a stub: what to read
  in the manual, one piece of work command by command with the
  engine's own answers, and the eleven things that go wrong most
  often. A test defines the class the skill shows, sends its commands
  and compares the answers, and holds each of the eleven points.
- The reference chapter on views and SQL: the views of a class and
  their columns, how a value looks in SQL, seven questions an analyst
  asks with their statements and the engine's answers, what a
  statement may do, and who may run one. A test runs every statement
  of the chapter and compares the answer. The chapter states three
  faults of the engine as they are, each pinned by the test.
- The three remaining skills for agents are written, so all five are:
  the analyst's (a week of a process and seven statements over it),
  the simulator's (six commands that answer "what would happen if" and
  write nothing, a draft of a class tried on a real document among
  them), and the skill for an agent's own work (a task the agent
  proposes, a person decides and the agent carries out, with a
  timeout and a count of attempts). Each shows the engine's own
  answers and lists what goes wrong most often; a test plays each on
  every push.
- The reference chapter on triggered variables, the computed variables
  that are worked out only at chosen moments: the four triggers, an
  invoice that takes its exchange rate when accounting checks it, the
  moment a value carries, what happens when working it out fails,
  refreshing and who may, and what such a formula may read. A test
  plays the chapter's two stories and every table of it. The chapter
  states one fault of the engine as it is, pinned by the test: `min`
  and `max` on a computed variable that names no type are accepted
  and not applied.
- The manual's chapter "Running it", for whoever keeps a database in
  service: a backup and putting it back, with what is gone and what
  runs twice; what a copy is, and a copy of a database that does not
  stop; what a program that died leaves; what `fadodb check` finds;
  how large the file gets; and how fast the engine is, from the
  performance pages. A test plays the chapter's story and every table
  of it. The chapter states three faults as they are: the check does
  not find a file that is missing; the command-line tool runs the
  queue of a database it opens; and the file of latest commits grows
  far past its limit while a caller writes without pause.
- The designer's skill teaches the syntax: what to read in the manual,
  a small class that is complete, and the ten things that go wrong
  most often. It is no longer a stub. The classes the skills show are
  defined by the engine in the manual's test.
- The designer's skill begins with "look before you build": list what
  the database keeps, read each label and description, ask the person
  which list a process uses, and never keep a thing twice; for a thing
  nothing holds, ask where it is born, who keeps it and how to get the
  list, and write the answer in the description. A training request,
  for an employee or a whole department, shows it with the engine's
  own answers, and a test plays it. ADR-0060, Proposed, keeps the
  organisation's rule-book, its things and the look of its faces, as
  documents.

- The command set (ADR-0055): every call of the Go API that
  reads or changes a database is also a command, a name and one JSON
  object answered by one JSON object. `DB.Exec` runs one, for a caller
  that is not a Go program; `Commands` lists the 47 of them with their
  members and their answers, and `ErrorCode` gives the code of any
  error. A refusal is the Go API's, as JSON under `error`. A member a
  command does not take, one given twice, one of the wrong JSON type
  and a missing one the command needs are refused with `bad_json` and
  the place.
- The HTTP door, the package `fado/httpdoor` (ADR-0055, items 8 and
  10): a program that has opened a database lets it answer the command
  set over HTTP, each command as `POST /v1/<command>` with its object
  as the body. `httpdoor.New` makes the door, an `http.Handler`, from
  the database and the function that says who sends a request; a
  request whose sender it cannot tell is refused with
  `unauthenticated` before its body is read. A door serves one
  database and shares nothing with another (ADR-0056). It holds a
  command to its limits (ADR-0059): a request of at most 16 MiB
  (`too_large`), 30 seconds for one command (`timeout`) and 64 commands
  at once (`busy`, after a wait of one second), each of which the
  program may set. The status of a reply follows its code; the caller
  reads the code. The server `fadodb serve`, the engine's own users and
  keys and the stream of changes are not built yet.
- `fadodb serve`, the server, in its first part (ADR-0059). Four
  commands lead from nothing to a database that answers a key over
  HTTP: `fadodb init FILE` makes a database; `fadodb user add ID
  --kind … --system administrators --db FILE` and `fadodb key new USER
  --db FILE` make its first administrator and print her key, once, on
  the machine that holds the database; `fadodb serve FILE` serves it.
  Told nothing, the server listens at `127.0.0.1:8080`. It refuses to
  listen beyond this machine without `--tls-cert` and `--tls-key`,
  unless `--plain-http` says that something in front encrypts. It
  writes one line of JSON for each request to standard error, answers
  `GET /v1/health` for anybody, and on an interrupt lets the commands
  that run end before it closes the database.
  `docs/manual/serving-a-database.md` is its chapter.
- The counts of `fadodb serve` (ADR-0059, item 9). `GET /v1/metrics`
  answers, to a database administrator who sends a key, what the
  server has counted since it started, in the text form Prometheus
  reads: the requests by command and status, the time they took, what
  the queue of the database still has to do (due, waiting for its
  moment, dead), how long the oldest due action has been due, and the
  sizes of the database and of its log. A name that is no command is
  counted as `other`; no user, document or value is in the counts.
  Counting a request allocates nothing. `OutboxCounts` is the Go call
  behind the counts of the queue.
- The stream of changes, in the Go API (ADR-0058). `Subscribe` opens a
  stream for a caller: the events of the log from now on, or from
  after the event its filter names, in the order of the commits.
  `Next` waits for the next event and `Close` ends the stream. The
  filter narrows it to classes, to kinds of event and to the states a
  transition enters. A caller is told of an event when it may read the
  document as the event left it, and of a delete when it could read
  the document just before; a document that leaves a caller's sight
  is passed over in silence. A stream that was away gives the id of
  the last event it handled and misses nothing. A stream follows its
  caller: given a function that says who the caller is now (`Follow`),
  it asks when there is an event to tell, so a group that was given or
  taken counts from the next event on. A commit pays nothing for the
  streams: it allocates the same with streams open as with none, and
  the streams read the log once, together, so a hundred of them ask
  the database what one asks.
- The stream of changes on the server (ADR-0058): `POST /v1/subscribe`,
  the command `subscribe`, whose reply stays open. The reply is in the
  form of server-sent events, `text/event-stream`: an entry for each
  event, with its id, the kind `change` and the event as one line of
  JSON; a first line that says the stream is open; and a comment
  every fifteen seconds without an event. A caller that was away
  gives the last event it handled, as `after` or in the header
  `Last-Event-ID`, and is told everything since. When the server ends
  a stream it says why in a last entry of the kind `end`:
  `unauthenticated` when the key was revoked or its user disabled,
  `closed` when the server stops. A door holds 256 streams at once,
  `limits.streams` in the settings of `fadodb serve`, and refuses one
  beyond them with `busy`; a caller that takes nothing for thirty
  seconds is cut. `httpdoor.Door.EndStreams` ends the streams of a
  program that stops; `fadodb serve` calls it, and so does
  `examples/server`, the server of one's own. The server counts
  the open streams as `fadodb_streams`, and writes a line for a stream
  when it ended, with how long it stayed open and how many events it
  told. Measured on one machine with a writer that commits every
  millisecond: an entry is read 0.84 ms after its commit at p99 with
  one subscriber and 3.12 ms with a hundred, against a budget of 5 ms.
- `docs/perf/v0.6.0.md` has the measurements of the phase against its
  budgets, with the rows of Phases 2 to 5 run again on the same build.
  Every budget holds, the 200 µs that ADR-0011 gives the server
  beyond the Go API as ADR-0066 measures it: beside the
  same transition made through the Go API by a plain server that was
  sent a request for it, on the same machine. Measured so, three
  times, the server adds nothing that can be told from the noise. The
  page first gave the server 0.155 ms of its own, from a comparison
  that did not repeat; it is corrected. So is what it said of the
  memory of an open database: 23 MB is a database of fifty classes,
  and one of four holds 3 MB, as ADR-0056 measured. Three
  campaigns are new: `TestServeCampaign`, which measures `fadodb serve`
  on a connection that is kept open, the way in and the door for
  agents; `TestOpenCampaign`, which measures what it takes to open a
  database of fifty classes and what it holds open; and the benchmarks
  `BenchmarkReceive` and `BenchmarkTransitionKeyed`, a message from
  outside beside the same move sent with a key.
- The engine's part of a way in for messages from outside (ADR-0065):
  an inlet. It is a definition kept in the database, the
  mirror of a declared action: how a message of another system proves
  who sent it, by a secret shown or a signature made with one, in one
  of three forms; which messages the inlet acts on; what makes two
  messages the same; and the one command a message becomes, `create`,
  `set` or `transition`, whose values are formulas over the message,
  typed against the class when the inlet is defined. The engine
  carries the command out as the service user the inlet names, so the
  rules of the class judge it and the log names it, with the key of
  the message, so a message sent twice makes one commit. Six commands:
  `validate_inlet`, `try_inlet`, which answers the command a message
  would become and does nothing, `define_inlet` and `remove_inlet`,
  which are a database administrator's, `get_inlet` and `list_inlets`.
  In Go: `DefineInlet`, `ValidateInlet`, `TryInlet`, `RemoveInlet`,
  `Inlets`, `Inlet`, and `Receive`, which takes a message. A program
  says that it takes messages with `Options.Inbound`. New refusals:
  `unknown_inlet` and `bad_message`.
- The server's part of the way in (ADR-0065): `fadodb serve`
  takes a message from outside at `POST /v1/in/<inlet>`, with no key
  of a user, once the operator's settings have the member `inbound`;
  told nothing, it takes none, and an inlet cannot be defined. The
  sender is answered a status and a code and no word of the database:
  `{"ok":true}`, `{"ok":true,"ignored":true}` for a message the inlet
  does not act on, `unauthenticated` for a proof that does not hold,
  `unknown_inlet`, `bad_message`, or the code of what the database
  refused. The words of a refusal are in the line the server writes,
  under `in/<inlet>` and the user the inlet acts as, and the counts
  have the messages of each inlet by status. A message has places of
  its own, 16 unless `inbound.messages` says otherwise, and is at most
  1 MiB, `inbound.message_bytes`, so a stranger who floods an inlet
  takes no place from a caller with a key. When it starts, the server
  says which inlets it takes messages at and which it cannot act on. A
  program that mounts the door sets `Inbound` in its options.
- `fadodb inlet check FILE --class FILE --message FILE` asks the
  engine whether it would take an inlet in a file, with the class it
  names, and shows the command a sample message would become, storing
  nothing and needing no server.
- A chapter of the manual, A way in for messages, played step by step
  against a server by a test; the address is in `api/openapi.json`.
- The image for containers. `docker build -t fadodb .` builds it from
  a checkout: the tool, the certificates an action's request knows
  another service by, and nothing else, about 15 MB. It runs as a user
  that is not root, keeps a database in the volume at `/data`, and is
  the tool: what follows its name is what follows `fadodb` at a
  terminal, so the four commands from nothing to a server are four
  containers, and the image is a caller and the door for agents as
  well. `examples/container/compose.yaml` keeps a served database
  running, with a health check, a file system it cannot write to and
  none of the rights of the machine. A release publishes the image at
  `ghcr.io/mojohand70/fadodb` under its version, for `linux/amd64` and
  `linux/arm64`, and under no other name; v0.6.0 is the first. The manual has
  a chapter, In a container, and a test plays its commands against the
  image, on GitHub with every push (`make imagecheck`).
- `fadodb health` asks a server whether it answers, `GET /v1/health`,
  with no key, and prints the answer. It ends with 0 when the server
  can read its database, with 1 when it answers that it cannot and
  with 3 when no answer came. It is the health check of a container,
  whose image holds no other program to ask with, and how a script
  waits for a server it has started.
- The door for agents: `fadodb mcp` offers the commands of a server as
  tools to an agent's program, in the Model Context Protocol
  (ADR-0064). The agent's program starts it and talks to it
  over standard input and output; the door sends each call to the
  server with the key of a user, from `FADODB_KEY` or `--key-file`, so
  what an agent does is judged by the rules of the classes and written
  to the log under that user. A tool is a command: its name, its
  arguments and its answer are the command's, written from the
  engine's table, with a mark that says whether it reads, writes or
  removes. A refusal reaches the agent as the database wrote it, with
  its code and its hint; when no answer comes through, the tool says so
  with the code `no_answer`. The door tells every agent that opens it
  to look before it builds: to list what the database keeps before it
  defines a class, a table or a schema, and never to keep a thing
  twice. `--read-only` offers only the tools that
  read and `--tools` only the ones named. `subscribe` and `create_key`
  are no tool, and the files are not in the door. It speaks the
  revision of 2026-07-28 of the protocol and the ones that open with a
  greeting, back to 2024-11-05. The manual has a chapter, "The door for
  agents".
- The flow of a class in one call (ADR-0063): the command
  `flow`, in Go `Flow`, answers the states of a class, each with the
  documents that are in it now, the moment the oldest entered it, and
  how many stays in it ended in a period, with their average and their
  longest; and its transitions, each with the moves along it in the
  period, and how many documents were created. `since` and `until` say
  the period; left out, it is since the class began. The flow counts
  the documents its caller may read, by the read rule of the class, so
  two callers see two flows. A state or a transition the class no
  longer has is marked `retired`. The manual has a guide, "See where
  documents wait". Measured on disk: 361 ms for a class of 100,000
  documents asked by somebody who reads them all, 15 ms by somebody
  who reads a hundredth.
- Commands from a terminal: `fadodb exec COMMAND OBJECT` sends any
  command to a server, with the key of a user, and prints the answer
  (ADR-0062). The object is the JSON the command takes over
  HTTP, given on the line, on standard input (`-`) or in a file
  (`@FILE`); `--from MEMBER=FILE` takes one member from a file, so a
  class is defined from the file it was checked in. The server comes
  from `--url` or `FADODB_URL` and the key from `--key-file` or
  `FADODB_KEY`, never from an argument; the key does not travel in the
  clear beyond the machine unless `--plain-http` says so, and the tool
  follows no redirect. A refusal goes to standard error as the server
  gave it. The tool ends with 0 when the command was carried out, 1
  when the server refused it, 2 when the line could not be read and 3
  when no answer came through. `subscribe` prints one event to a line,
  and the tool says when the stream is open; `fadodb exec help` lists
  the commands and `fadodb exec help COMMAND` the members of one, from
  the engine's table. `--out FILE` writes what the tool would print to
  a file. The manual has a chapter,
  `docs/manual/commands-from-a-terminal.md`, and a test types every
  line of it against a server.
- Exports from a terminal, written in no locale: `fadodb exec query …
  --xlsx --out FILE` writes the documents of `query`, or the rows of
  `sql`, as an Excel file in which a number is a number and a day a
  day, by the type the class gives each variable, so the program that
  opens it shows them its reader's way; an amount is shown with its
  currency. The writer is the tool's own, on the standard library.
  `--csv` writes the same for a program, in one form on every machine:
  commas, a point in a number, UTF-8.
- A file on a document from a terminal: `fadodb file put FILE --class
  … --id … --variable …` sends the bytes to a server and writes the
  file into the variable; `fadodb file get … --out FILE` reads the file
  a variable holds, or one by its hash, through the document and under
  its rules.
- A check of a definition from the command line: `fadodb class check
  FILE...` and `fadodb action check FILE` ask the engine whether it
  would take the class or the action in a file, and store nothing.
  Each finding is printed with its code, its place in the definition,
  what is wrong and what to write instead; the tool ends with 0 when
  the engine would take the definition and with 1 when a finding
  refuses it, so an agent loops until it passes. Several classes that
  name each other are checked together; `--db FILE` checks against a
  database, which is not changed; `--json` prints the findings as the
  commands `validate_class` and `validate_action` answer them. An
  action is checked as `fadodb serve` would take it, with the settings
  given by `--settings FILE`.
- The description of the commands for other languages (ADR-0055, item
  10): `api/openapi.json`, an OpenAPI 3.1 document with every command
  as `POST /v1/<command>`, the members of its object with their types,
  the members of its answer and the shape of a refusal, the three
  addresses of the files, and the health and the counts of
  `fadodb serve`. It is written from the engine's table of the
  commands by `make docs`, a test fails when it falls behind, and
  another holds every address of it to a door. An answer is described
  by the names of its members and not yet by their types.
- The manual's chapter on the stream of changes,
  `docs/manual/the-stream-of-changes.md`, with `examples/stream`, the
  program of an agent that waits for invoices to enter review. A test
  runs the program, and another plays every table and every reply of
  the chapter against a server.
- The settings of `fadodb serve` (ADR-0059, items 4, 5, 6 and 11).
  `--settings FILE` gives the server one JSON file of the operator's:
  under `outbound`, the hosts the actions of the database may call,
  and for each connection the hosts its requests and its secrets may
  go to; under `limits`, the largest request, the time of a command,
  the commands at once and the streams at once. A member the file
  should not have, a member written twice or a value that does not
  fit is refused when the server starts. Told nothing, the server
  calls no host. A secret is never in the settings: the secret `token`
  of the connection `slack` is the file `slack/token` in the directory
  given with `--secrets-dir`, or the variable
  `FADODB_SECRET_SLACK__TOKEN` of the environment, and it is read
  again for every request, so a secret is changed without a restart.
  When it starts, the server says what its actions may call, and
  names every action of the database it cannot run as it was started.
  No setting is kept in the database, and no command changes one.
- **Breaking for a program that calls `CheckBlobs`:** the check of the
  files now also finds a file that is missing. It read every blob the
  store held and named the ones whose bytes had changed, and never
  asked which files the documents hold: a backup that left a file out,
  or the whole directory of the `dir` store, passed. It now reads the
  hash of every file a document holds, in every version, and names
  the ones the store does not have, each with its document: the class,
  the id, the first version that held it and the variable. `CheckBlobs`
  answers a `BlobCheck` with `Checked`, `Mismatched` and `Missing`
  where it answered two values; the command `check_blobs` gains the
  member `missing`; `fadodb check` lists the missing files and ends
  with 1 for them (ADR-0033).
- The command-line tool leaves the queue of a database alone. `fadodb
  check`, `fadodb user add` and `fadodb key new` opened a database as
  a program does, with a worker for its queued actions; the tool has
  none of the program's Go functions, so an action that was due got no
  answer, `not_registered`, and lost one of its ten attempts. A check
  of a database with files to read ran long enough for that. The tool
  now starts no worker. `fadodb check` also refuses a name under which
  there is no database, where it made an empty one.
- `fadodb serve` runs no Go function (ADR-0059, item 6). A definition
  of an action with no request is refused when it is defined, in
  every program that has no function under its name, with
  `not_registered` (it was `not_supported`), and the refusal says the
  ways out. `examples/server` is the program a company starts from
  when it needs its own Go function: the door in front of a database
  it opens with the function.
- A program holds a database alone with `Options.Alone` (ADR-0059,
  item 7), as the server does: while it has the database open, every
  other program that opens it is refused with the new code `in_use`
  and told who holds it. The hold is a lock of the operating system on
  `FILE.lock` and ends with the program, however it ends.
- `DB.Ping` reads from the database, to say that it answers.
- A database keeps its own users, groups and keys (ADR-0057) when it
  is opened with `Options.Users`. A user is a person, an agent or a
  service known by its id; a group is a name on a user; a key is one
  line of text, `fado_…`, shown once when it is made, of which the
  database keeps a hash. Nine calls and their commands keep them:
  `CreateUser`, `SetUser`, `GetUser`, `Users`, `UserChanges`,
  `CreateKey`, `Keys`, `RevokeKey` and `Whoami` (`create_user` to
  `whoami`). Database administrators make users and keys; every user
  sees the users and lists and revokes its own keys; a change that
  would leave no administrator is refused; every change is kept with
  who made it. `DB.Authenticate` answers who a key belongs to, in
  0.1 µs and without a read, and `httpdoor.Keys` is the door's
  function for such a database. A disabled user and a revoked key are
  stopped at the next command. A database opened without `Users` keeps
  none, and does not offer the nine commands. Two codes are new,
  `unknown_user` and `unknown_key`.
- Files through the HTTP door (ADR-0055, item 9; ADR-0033): `POST
  /v1/blobs?name=…` keeps the bytes of the body and answers the facts
  of the file, to set on a document in a command;
  `GET /v1/file?class=…&id=…&hash=…` streams a file under the rules of
  the document that holds it, at its current version or the one
  `version=` names; `GET /v1/blob?hash=…` streams bytes by their hash
  alone, for a content administrator. A file is sent so that a browser
  saves it and does not show it, with its hash as its `ETag`. A
  parameter an address does not take is refused, as a member of a
  command is.
- `DB.Definition` reads a class, a schema, a table or an action back as
  it was given, with its version and who defined it; `DB.Schema` and
  `DB.Schemas` read the schemas, which could not be read back.
- `docs/manual/commands.md`, generated from the table of the commands,
  and the refusals of a command in `docs/manual/refusals.md`.
- The three script suites, `conformance/statechart`, `rules` and
  `access`, run through the command set as they run through the Go
  API, and a third way, as requests to the HTTP door of a server that
  listens on the machine, and must answer the same each way.

### Changed

- The layout of a database is 11 (it was 10): the tables of the inlets
  join the engine's, and `inlets` and `inlet_versions` are names no
  class may take. A database made by an earlier engine is refused with
  `not_supported`, as before 1.0 there is no way to bring one forward.
- An idempotency key may not start with `in:`, the prefix of the
  commands the inlets send, as it may not start with `outbox:`.
- An answer writes `<`, `>` and `&` as themselves, and no longer as
  `\u003c`, `\u003e` and `\u0026`: a guard reads `"total > 0"` at a
  terminal and to an agent. This holds for the answer of a command, a
  refusal, an entry of the stream and the facts of a file, in a Go
  program and over HTTP. The values are the same; a program that reads
  JSON sees no change (ADR-0055, amended).

- **The license is the Apache License 2.0** (ADR-0061), in place of
  MIT. Releases up to v0.5.0 stay under MIT.
- **The engine schema is version 10**, for the three tables of the
  users, their keys and the changes to them (ADR-0057). Every database
  has them; a database that keeps no users has them empty. A database
  made by an earlier pre-release is made again: before 1.0 there is no
  way to bring one forward.
- A document is no longer created under an id that holds a control
  character, such as a line break, a tab or a zero byte: the create is
  refused with `bad_value` (ADR-0055, the owner's ruling of
  2026-11-14). Slashes, spaces and the letters of any alphabet stay,
  and a document that has such an id is still read and changed.
- The benchmark gate runs on a pull request only when it changes a file
  that goes into the programs the gate measures: a Go file of a package
  linked into the benchmarks of `internal/formula` and `internal/store`,
  a test file of those two packages, a file they embed, `go.mod`,
  `go.sum`, the Makefile, a script or the CI workflow. Until now any
  `.go` file started it, a test of another package included, and it
  takes half an hour. `scripts/benchgate.sh --needed` gives the answer
  and the file that decided it; when the go tool cannot list the
  packages the gate runs.

### Fixed

- Callers that read at once had their connections to the database
  opened and closed over and over: every reader reads on a connection
  of its own, and only two were kept ready, which is what Go's database
  library does unless it is told otherwise. Sixteen callers together
  read fewer documents a second than one caller alone, and through a
  server one read in a hundred took 18 ms. Eight connections are now
  kept ready, and one that nobody has used for a quarter of a minute is
  closed, all but one, which is kept for good, so that a quiet database
  holds what it held and a caller that reads once a minute does not pay
  for a connection each time: a connection that reads takes about
  3.5 MB and two open files, and milliseconds to open. Sixteen callers at
  once take 4.3 µs a read, where they took 11.9 µs.
- A command sent again with its key searched the whole log of the
  database for the versions its commit had made, and did so while it
  held the writer's turn: with fifty thousand events of other documents
  it took 2.4 ms, where a first command takes 0.05 ms, and it grew with
  the log. It is what a caller does that got no answer the first time,
  and what a sender of messages does with every notice it repeats. The
  answer is now read from the events of the command's own document:
  0.024 ms, whatever the database holds.
- A command of the tool said its help twice: `fadodb serve --help`
  printed the whole text two times, and a flag it does not know
  printed it before the mistake and after. Each command now says it
  once.
- A command whose caller left before it ended was logged and counted
  by the server as a failure of the storage, with the status 500,
  which is what the engine says of a command it was told to stop. An
  agent that stopped waiting for a slow statement could so wake
  whoever watches for a failing disk. Such a request now has a code of
  its own in the log and the counts, `caller_left`, with the status
  499, the one proxies write for a request its caller closed
  (ADR-0059, item 2, amended on the owner's word). Nobody is answered
  with it. `httpdoor.CodeCallerLeft` and `httpdoor.StatusCallerLeft`
  are what a Go program's `Report` reads.
- A statement of `sql` that ran longer than the server lets a command
  run was refused with `bad_value` and the words `context deadline
  exceeded`, as if the statement were wrong. It is now refused with
  `timeout`, status 504, as every other command is. A Go program that
  gives `SQL` a context that ends reads the error of the storage, and
  no longer `bad_value`.
- The hint of a moment that is none showed seconds a year off:
  `1763026200` beside `"2026-11-13 09:30:00"`, which is `1794562200`.
- The log of latest commits, the `-wal` file beside a database, had no
  bound under a caller that commits without a pause: it grew to 2.6 to
  4.4 GiB beside a database of 200 MiB, measured. A log can only be
  emptied when nobody writes to it, and such a caller leaves no such
  moment; when one came by chance, every commit waited more than two
  seconds for the whole log to be copied. The log now has a bound.
  Past its limit of 64 MiB it is emptied whenever that costs nothing,
  as before, so a caller that pauses sees no difference. A burst grows
  it freely. At eight times the limit the engine empties it in the
  writer's turn, between two commits. Under callers that never pause
  the log stays near 650 MiB, the longest wait of a commit fell from
  2.2 s to about 70 ms, and commits a second rose by a quarter. Such
  callers now and then wait some tens of milliseconds for the log to
  be emptied. ADR-0035 has the measurements.

### Security

- A new database is closed to everybody but its owner. `Open` creates
  the file readable and writable by the user that made it and by nobody
  else, where it was readable by every user of the machine; the log
  SQLite keeps beside it follows, and so does the directory of
  attachments of a database that keeps them outside the file. A
  database that exists keeps its permissions: `chmod 600` on the file
  and `chmod -R go-rwx` on its `.blobs` directory close one made by an
  earlier version. On Windows nothing changes: a file takes the access
  list of its directory.
- A read leaves no trace in the database. A content administrator who
  opens a document past its rules is not recorded, and neither is an
  analyst who reads through SQL; a change past a rule is, with its
  reason. The log of the server has a line for each request, with the
  user and the command, and not the document or the statement. A log
  of such reads gets its own record as the first step after this
  release (ADR-0052, and the owner's ruling of 2026-11-17).

## [0.5.0] - 2026-11-13

### Added

- The four rules of a class are enforced (ADR-0049): `read`,
  `create`, `update` and `delete` under `access`, each a condition over
  the document for the caller. A document its caller may not see is
  answered as one that does not exist, `unknown_document` in the same
  words, by `Get`, `Version`, `Transitions`, `Preview`, `OpenFile` and
  every command that names it; `History` and `Events` answer nothing,
  and `Query`, `Count` and `Due` leave it out, the rule being one more
  predicate of the filter. `TransitionMany` reports it as
  `unknown_document`. The history of a document is read under the rule
  as the document is now, and over its last version once it is
  deleted.
- A caller who sees a document and may not do what they ask is refused
  with `forbidden`, and `Error.Path` names the rule: `/access/create`,
  `/access/update`, `/access/delete`, `/access/transition/<name>`. The
  hint is the rule. `create` is asked over the new document with its
  defaults and its computed values, `update` and `delete` over the
  stored one. `Set` and `Reset` ask `update`; `Refresh`, and a set that
  names no value, ask only to see the document.
- A command whose caller may not see what it left is answered with the
  document's id, version and state and no values, and a repeat of its
  key the same way.
- `PreviewDelete` says whether a delete would be refused and which
  rules it would run, and writes nothing. `Preview` reports a refusal
  of `create` or `update` under its errors and goes on.
- `OpenFile` asks the read rule of the document and the read right of
  the variable that holds the file.
- `conformance/access/` is the suite of the rules: every path, for every
  caller. `BenchmarkTransitionAccess` is in the benchmark gate: a
  transition on a class with rules allocates what it does without them.
- A filter decides the caller's part of a read rule before it writes
  its statement (ADR-0049, item 5). `INGROUP("finance")` has one answer
  for the whole statement, so `owner = USER() OR INGROUP("finance")` is
  nothing at all for a member of finance and `owner = USER()` for
  anybody else, and a statement is kept for each outcome, not for each
  caller. A caller who sees only their own documents lists and counts
  them through the index on the owner, at the cost of the same
  condition written out.
- A text variable compared with the caller, `owner = USER()`, is
  answered from the variable's index in any filter, in a `where` as in
  a read rule. Before, such a filter read every document of the class.
- A principal says what it is and for whom it acts (ADR-0050):
  `Principal.Kind` is `ActorPerson`, `ActorAgent` or
  `ActorService`, or empty when the host program does not know, and
  `Principal.For` is the id of whoever the caller acts for. The engine
  trusts the kind as it trusts the id, and checks the "for" against a
  substitution (ADR-0053, below). Any other kind is refused with
  `bad_value`.
- Two functions answer them to a formula, wherever `USER()` may stand:
  `USERKIND()` and `ACTINGFOR()`, each blank when the host program said
  nothing. `USER()` stays the one who acts. A step kept for people is
  `USERKIND() = "person"`; an assistant sees what its principal owns
  where the rule says `owner = ACTINGFOR()`. They evaluate alike on the
  Go VM, SQLite and DuckDB, with cases in
  `conformance/formula/context.json`.
- Every event records `actor_kind` and `actor_for` beside `actor_id`:
  `Event.ActorKind` and `Event.ActorFor`, the `_events` view, the
  `_states` view as `entered_by_kind`, `entered_for`, `left_by_kind`
  and `left_for`, and the event an action receives, in
  `api/event.schema.json`. An action and a timer run as whoever set
  them off, with what that caller was; `QueuedAction` shows it.
  `conformance/access/actors.json` is the suite.
- Four system groups hold the work that is not about one document
  (ADR-0051, ADR-0052), with fixed names: `designers` define classes,
  schemas, tables and actions, preview a draft and migrate;
  `administrators` operate the outbox, `Drain`, `PreviewAction`,
  `TryAction` and `CheckBlobs`; `analysts` run `SQL`, which reads every
  row and every variable; and `content-administrators` see every
  document and act on it past the rules of its class, never past a
  guard. The host program gives a caller its system groups in
  `Principal.System`, apart from `Principal.Groups`: a group of its own
  that has one of these names is refused with `bad_value`, so that no
  directory group gives such work by its name. Anybody else is refused
  with `forbidden`, and the refusal names the group, as
  `/system/designers`. Nobody is in a system group unless the host
  program says so, and a call without a principal does none of this
  work. The constants are `GroupAdministrators`, `GroupDesigners`,
  `GroupContentAdministrators`, `GroupAnalysts` and `GroupEveryone`.
- `INGROUP` answers a system group as it answers any group, and answers
  `everyone` for every caller that has an id, on the Go VM, SQLite and
  DuckDB. A class opens to every named caller with
  `INGROUP("everyone")`. `conformance/access/groups.json` is the suite,
  with ten cases in `conformance/formula/context.json`.
- `DefineClass`, `DefineClasses`, `DefineSchema`, `DefineTable`,
  `DefineAction`, `Outbox`, `RetryAction`, `Drain`, `PreviewAction`,
  `TryAction`, `CheckBlobs` and `TableRows` take the options of a
  call, and with them `As`.
- A table has two rules (ADR-0051, item 7): `"access": {"read": ...,
  "write": ...}` in its definition, each a condition over the caller
  and the clock, about the table as a whole. `TableRows` asks `read`;
  `ImportRows` asks `write`, and so do the `table.insert`,
  `table.update` and `table.delete` actions of a rule, as the person
  who set the rule off. A formula goes on reading a table with the
  engine's eyes.
- A content administrator's act past a rule is an override, with a
  reason and a mark (ADR-0052, item 4). The rule is asked first: where
  it lets the caller through, the command is an ordinary one. Where
  only the group does, the command needs `Reason("...")` or is refused
  with `reason_required` and the place of the rule, and the event of
  its commit keeps the rules lifted and the reason: `Event.Override`,
  as `/access/read /access/update`, and `Event.Reason`, both columns of
  the `_events` view. A write past a right is one too, as
  `/rights/draft/secret/write`. `Preview.Override` lists the rules a
  command would lift and `TransitionInfo.Override` the rule a
  transition would, so a caller knows before it acts. What the commit
  queued acts on another document for the same reason. A guard, the
  statechart and a required value hold as for everyone, and a read by
  override asks no reason and leaves no trace.
  `conformance/access/override.json` is the suite.
- Every version of a class, a schema, a table and an action records who
  defined it (ADR-0052, item 7; ADR-0051, item 5): the caller's id,
  what it was, a person, an agent or a service, the id it acted for,
  and when. `Definitions(ctx, what, name)` lists the versions of one,
  oldest first, and is open to every caller; `Class.Defined`,
  `Table.Defined` and `ActionInfo.Defined` say it of the current
  version. A definition that is the stored one is no new version and
  changes no record.
- Checking a class notes each access rule it leaves out (ADR-0052, item
  5): `ValidateClass` and `ValidateClasses` answer a finding with the
  code `rule_left_out`, the place of the rule, what it means and what
  to add, and `Note` set. A note refuses nothing: `Diagnostics.Refuses`
  tells whether a definition is refused, `Diagnostics.Err` is nil when
  it is not, and the class a `DefineClass` returns carries its `Notes`.
  `conformance/access/left_out.json` and `left_out_partly.json` are the
  suites.

- Acting for somebody is checked (ADR-0053). A caller that names
  somebody in `Principal.For` is let through only while a substitution
  says so, on every call, a read as a command; otherwise the call is
  refused with `forbidden`, the path `/principal/for`, and a message
  that says what is missing: no substitution, one that begins later,
  one that has ended, or one for other classes. A caller that acts for
  nobody is asked nothing.
- A substitution is a document of the class `Substitution`, which the
  engine brings to every database: `by` may act for `for`, from `from`
  until `until`, on `classes` or on every class. The person gives one
  for themselves and withdraws it at any time; a database
  administrator gives one for somebody who cannot, with a `reason` and
  never to themselves. Nobody passes one on, nothing changes one and
  nothing deletes one. It is `planned`, `active`, `ended` or
  `withdrawn` and moves by itself at its dates, while the check reads
  the dates and waits for no worker. `fado.SubstitutionClass` and the
  names of its states are constants.
- The name `Substitution` is the engine's: a definition of that name is
  refused with `reserved_name`. A content administrator sees every
  substitution and acts past no rule of the class.
- `Options.HostAnswersFor` is for a host program that keeps its own
  substitutions: the engine records the "for" it is told and checks
  nothing, as before.
- `conformance/access/substitution.json` is the suite, and
  `BenchmarkTransitionFor` measures a transition by a substitute: the
  check is one lookup in memory and allocates nothing.
- The Approval sample accepts whoever validly acts for the approver,
  with `approver = USER() OR approver = ACTINGFOR()`.
- `docs/perf/v0.5.0.md` has the measurements of the phase against the
  budgets of ADR-0049 to ADR-0053, with the campaigns of the earlier
  phases run again. `TestAccessCampaign` is the campaign of access,
  and `BenchmarkTransitionOverride` measures a content administrator's
  transition past a rule.

### Changed

- **Breaking for a host that sets `Principal.For`:** the engine now
  checks it. Give the caller a substitution, or open the database with
  `Options.HostAnswersFor` when the host program keeps its own.
- Every database has the class `Substitution`, listed first by
  `Classes`. `fadodb check` counts it.

- A named transition without a rule of its own asks the `update` rule
  of its class, where the class has one. Before, it was open to every
  caller. A transition with a rule of its own is asked that rule alone.
- A rule's action on another document is refused by that document's
  class rules as it is by its rights (ADR-0044, item 11): the row is
  dead with `forbidden`. A `refresh` of another document asks its
  `read` rule. The wake-up of a timer, an eventless transition and the
  result an action writes back ask nothing.
- **`Principal.Admin` is gone**, and with it the one caller who did
  everything (ADR-0052). What it did is split among the four system
  groups above, and a caller that needs all of it is in all four. A
  program's own setup code names itself as a caller in the groups it
  needs; `fadodb check` acts as a database administrator.
- **The engine schema is version 9.** Version 6 added what the actor of
  an event was and for whom it acted, two columns of the event; version
  7 drops the administrator's flag from the outbox row, which keeps the
  system groups of whoever queued it with the other groups; version 8
  adds the rules an override lifted and its reason to the event; version
  9 adds the table of who defined each version. A
  database made by an earlier engine, v0.4.1 included, is refused when
  opened, as before 1.0 there is no upgrade path.
- A class that declared `read`, `create`, `update` or `delete` had them
  checked and not enforced; they are enforced now.
- **A rule a class leaves out lets nobody through** (ADR-0052, item 5,
  the owner's ruling). A class with no `access` block holds documents
  that nobody but a content administrator creates, sees, changes or
  deletes; a class that writes `read` and leaves out `update` is seen
  and not changed. The same holds for the `read` and the `write` rule
  of a table. The refusal names the rule that is missing and what to
  add. `"read": "INGROUP(\"everyone\")"` opens a rule to every caller
  that has an id, and `"TRUE"` to a call without a principal as well.
  A class without states is asked `read` and `create` by its previews.
  Every sample writes its rules: an invoice is created and changed by
  sales while it is a draft, approved and paid by finance, seen by
  both and by its customer, and deleted by nobody.

## [0.4.1] - 2026-11-13

### Added

- `TransitionMany` moves many documents by one call (ADR-0048): up to
  10,000 documents of one class, named by their ids, along one
  transition, in the order given and in one transaction. Each move is
  the commit `Transition` makes, with its own guard, rights, rules,
  version, history row and event. A document that cannot move does not
  stop the others: the reply has one `MoveOutcome` for each id, the
  document after its move or the refusal with its code, and a refused
  document is left as it was. The call's own error is for the call:
  `unknown_class`, `unknown_transition`, `too_many_elements`,
  `bad_value` for an id named twice, `not_allowed` for `At`, or a
  failure of the database, after which nothing has moved. `Key` is held
  for each document, as the key, `#` and the id, so the call sent again
  moves no document twice. The values are the same for every document,
  and a guard that counts other documents sees the moves made earlier
  in the same batch.
- `PreviewMany` runs the same batch and rolls it back: what the call
  would answer for every document, now or at a moment given with `At`,
  with nothing written and no action queued.
- Measured on disk at 1,000,000 documents: 27,257 documents a second in
  batches of 1,000, against 12,427 a second by `Transition`, a command
  each; a million documents in 37 seconds where they took 80. The
  budget of ADR-0048 is 15,000. `BenchmarkTransitionMany` is in the
  benchmark gate, and the campaign of commits has two more rows: the
  batch, and single commits made while batches run. The page is
  `docs/perf/v0.4.1.md`.

## [0.4.0] - 2026-11-12

### Added

- A transition may carry its time (ADR-0047, items 9, 13 and 14):
  `"after": "30d"`, a length of time in its state as a whole number and
  `d`, `h`, `m` or `s`, or `"at"`, a formula over the document that
  gives a date or a datetime. It needs neither a name nor a guard, and
  a guard beside the timer is still asked. Such a transition fires in
  the chain of the first commit that touches the document once its time
  has come, in the order of the class with the eventless ones; its
  event has the origin `timer`, and with a name it raises the rules and
  the triggered variables of that name. A transition back into its own
  state with an `after` repeats, which is how a reminder is written. A
  named one may be taken by a command before its time. Refused when the
  class is defined: both on one transition, an `after` that is no
  length of time, an `at` that reads the clock, the user, another
  document or a table, an `at` on a transition into its own state, and
  a timed transition out of a terminal state.
- The engine moves a document by itself when its timer comes
  (ADR-0047, items 10 to 12). A commit that leaves a document in a
  state queues a wake-up, a row of the outbox, for each timed
  transition leaving it, due at its moment; a commit that changes a
  date an `at` reads queues one for the new moment. When a wake-up is
  due the worker touches the document: what fires in the chain is
  written, each transition a version with the origin `timer`, and when
  nothing is to do, because the document has left the state or the
  stay, the moment has moved to later or a guard does not hold, the row
  is done and nothing is written. The touch and the mark are one
  transaction, so a timer fires exactly once, and a timer the worker
  missed fires at the next commit, so a stopped worker loses nothing. A
  wake-up acts as the person whose commit queued it: `USER()` in the
  rules of the move is theirs, and the actions those rules queue run
  with their rights. `Outbox` and `<Class>_actions` list wake-ups as
  the action `timer`, with their moment as `due_at`. `Drain` runs what
  is due by the database's clock, so a copy of a database opened with
  `Options.Now` plays its timers at the moment a simulation chooses.
- `Transitions` shows a timed transition with `fires_at`, the moment
  its time comes, one without a name too. `Due` lists the documents a
  timed transition would move, each with `since`, the moment since
  which it has been due.
- `Due` lists the documents of a class that a commit would move if it
  touched them now, or at the moment given with `At` (ADR-0047, item
  8): each with its state and the state it would move to, the target of
  the first eventless transition leaving its state whose guard is TRUE.
  A document becomes due when a guard comes to hold with no commit of
  its own: the clock passed a date, or a document or a table the guard
  looks up changed. The list is in the order of the class's states and
  then of the ids, at most 10,000 documents in one call, and it writes
  nothing: a commit that touches the document moves it, and a refresh
  is one. A guard that translates to SQL runs as one filter over the
  documents of its state; a guard that looks another document up or
  calls a function of a state is evaluated document by document, as is
  a document an older version of the class computed. A terminal
  document is never due.
- The functions of a state (ADR-0047, item 6). Each takes the name of a
  state: `FIRSTENTERED` and `FIRSTENTEREDBY`, `LASTENTERED` and
  `LASTENTEREDBY`, `FIRSTLEFT` and `FIRSTLEFTBY`, `LASTLEFT` and
  `LASTLEFTBY`, when and by whom the document first and last entered
  the state and left it; `ENTRIES`, how many times it entered it;
  `DAYSIN`, the days it spent there in all, up to now when it is there;
  `FIRSTEDITOR` and `LASTEDITOR`, who first and last created the
  document there or changed it there with a set or a reset; who moved
  it in or out is not an editor. They are context functions, on
  the Go VM, SQLite and DuckDB, for a default, a triggered variable, a
  guard, a rule, a transition rule and a write right. A transition back
  into the same state is an entry. A state written as text is checked
  against the class's states when the class is defined; a computed one,
  as in `DAYSIN(STATE())`, is looked up when the formula runs. They
  read the document's own events once for a command, with the step the
  command is making. A read rule, a read right and a filter are refused
  them: the `_states` view has the same facts for every document.
- The event of a creation names the state the document was created in,
  under `to_state`, as the event of a transition names the one it
  entered.

- The document keeps who, and where from (ADR-0047, items 3 and 4):
  `created_by`, `updated_by`, `state_entered_by` and
  `state_entered_from` join the system variables, on the document, on
  every version in the history, in the views, as columns a `LOOKUP`
  over the class reads, and in the document on the wire. Each is the
  principal of the command, blank when it named none. The names are
  reserved.
- A transition back into the same state is an entry like any other, and
  the `_states` view has a row for every entry. It gains `entered_by`,
  `entered_from`, `entered_via` and `entered_how` for the move that
  began the stay, and `left_by`, `left_via` and `left_how` for the one
  that ended it.
- Every event that enters a state says how the move came about, under
  `origin`: `command` when somebody asked for it, `action` when a rule
  of a document did through the outbox, `guard` when an eventless
  transition fired. `Events`, the event an action receives and the
  `_events` view carry it.
- The functions of the row (ADR-0047, item 5): `CREATEDAT()`,
  `CREATEDBY()`, `UPDATEDAT()`, `UPDATEDBY()`, `ENTEREDAT()`,
  `ENTEREDBY()`, `ENTEREDFROM()` and `VERSION()`, context functions
  like `STATE()` and `ID()`, on the Go VM, SQLite and DuckDB. "Seven
  days in this state" is `NOW() >= ADDDAYS(ENTEREDAT(), 7)`; four eyes
  is `CREATEDBY() <> USER()`. In a rule or a triggered variable of an
  exit or of a transition they tell of the stay that ends, as `STATE()`
  does of the state left.

- `COUNT`, `SUM`, `MIN` and `MAX` in the column position of a `LOOKUP`
  (ADR-0046), over an explicit table: `LOOKUP(Stock, SUM(qty), sku =
  THIS.sku)`. The function runs over one expression of the table's
  columns, for every matching row, and answers what it answers over the
  same values inside one document: blanks skipped, exact values exact
  and of their type, money within one currency. With no matching row
  `COUNT` is 0, `SUM` is 0, or blank for money, and `MIN` and `MAX` are
  blank. Numbers are added one by one in key order; an exact sum fails
  with `overflow` only when the total does not fit. An aggregate fails
  where its function fails, and with the error of the first matching row
  on which its expression fails. `AVERAGE` is refused with the hint to
  divide a `SUM` by a `COUNT`. A class as the source comes with the next
  part of the step.
- `DefineClasses` and `ValidateClasses` (ADR-0046, item 12): several
  class definitions in one call, each checked against the others and
  against what is stored, all stored or none. Classes that name each
  other, an approval whose rule refreshes its purchase and a purchase
  whose formula counts its approvals, could not be defined before,
  since neither could be first. The path of a finding starts with the
  position of its definition, `/1/variables/total`; when a definition is
  wrong by itself only its own findings are reported; a definition that
  is the stored one is left as it is. `DefineClass` behaves as it did.
- `examples/classes/purchase.json` and `approval.json`: parallel
  approvals written out, walked through by `TestExampleApprovals`.
- A formula reads the documents of a class (ADR-0046):
  `LOOKUP(Approval, COUNT(id), purchase = ID() AND state = "granted")`.
  The source is the class's documents as they are now, those in a
  terminal state included and deleted ones not: the system columns `id`,
  `title`, `state`, `version`, `created_at`, `updated_at` and
  `state_entered_at`, and every scalar variable, ordered by `id`. `ID()`
  and `STATE()` inside the call are the reading document's own. The
  formula sees every document, whoever caused it to be computed and
  whatever the rights say; the rights on the variable that holds the
  result decide who sees it. It may stand in a triggered variable, a
  default, a guard, a rule and an access rule other than `read`, and is
  refused in an auto formula. It reads what was stored before the
  command began and is recomputed at its own triggers only, so the other
  class's rule refreshes the reader. It runs as one statement on the
  writer inside the commit, narrowed through the index of an indexed
  variable: 10.7 µs to count the approvals of one purchase among
  100,000, and 80 µs for the whole refresh that counts and commits.
- A variable computed from the documents of a class declares its `type`
  (`missing` at `/variables/<name>/type`, with the type in the hint):
  the layout of its documents must not follow another class. A
  redefinition of a class that would break a formula of another class
  which reads it is refused with `not_allowed`, naming the formula.
- Among the equalities of an `AND`, a lookup takes for its key the one
  on a column that leads an index, wherever it stands in the condition.
- `conformance/rules/reads.json`, `conformance/binding/lookup-class.json`,
  and the formula suite's lookups run against document tables too.
  `BenchmarkLookupClass` joined the benchmark gate.
- A lookup is narrowed by its key when the equality of a column with an
  expression of the document is one term of an `AND`, not only when it
  is the whole condition: `sku = THIS.sku AND min_qty <= THIS.qty` reads
  the rows of that `sku` through the table's index, 2.2 µs where the
  whole table of 1,000 rows took 33 µs.
- `conformance/formula/lookup.json` gained the aggregates and runs on
  the Go VM, SQLite and DuckDB, and now against the store's own tables
  too, with the index and without.

- Rules run inside the commit (ADR-0043). Every commit raises a fixed
  list of triggers: `create` and `enter` of the initial state on a
  create; `values_change` on a set; `values_change`, `exit`,
  `transition`, `enter` on a transition; `exit` and `enter` on each
  eventless transition, which is a commit of its own; `delete` on a
  delete. Rules run trigger by trigger and in declaration order, each
  over the document as computed so far; a condition that errors is
  FALSE. Rules do not cascade inside a commit.
- The synchronous actions `set`, `reset` and `emit`. A `set` evaluates
  every formula before it assigns any and is the engine's write, so no
  write right is consulted; a `reset` re-evaluates defaults and blanks an
  input without one; `emit` appends an event of kind `custom` with the
  name the rule gives, caused by the commit's event. An action that fails
  refuses the whole command; `Error` gained `Path`, the place of the
  action in the definition, such as `/rules/2/do/0`.
- `Preview` and `PreviewDraft` report `Rules`: every rule whose trigger
  the command raised, the commit it belongs to, whether it ran, and its
  actions with their evaluated arguments.
- `conformance/rules/`, suites in the script format of the statechart
  suites, and `BenchmarkRules`, which joins the benchmark gate: the
  rules of a commit do not allocate.

- Triggered variables are computed (ADR-0043). A variable with
  `"mode": "triggered"` recomputes in the commit that raises one of its
  triggers, `enter:`, `exit:` or `transition:`, after the state change
  and before the rules, and when the `refresh` action names it; it keeps
  its value in between. Its moment is stored beside it in two columns,
  `<name>__asof` and `<name>__error`; the views add the time as
  `<name>__at`, and `Document` gained `Computed`, the version, the time
  and the error per variable, hidden with the variable by its read
  right. A recomputation that errors leaves the value as it was, records
  the code and refuses nothing.
- The `refresh` action of a rule, which recomputes the triggered
  variables it names, or every one that lists `refresh`.
- `Reset` and `Refresh` as commands of the Go API (ADR-0043), each one
  commit with a key, a principal, a business date and an event of kind
  `reset` or `refresh`. A `Reset` puts the named inputs back to their
  defaults and is the caller's write, checked against the write rights;
  one that changes nothing commits nothing. A `Refresh` recomputes the
  triggered variables it names, or every one that lists `refresh`, always
  commits, and is followed by the eventless transitions: it is how a
  caller makes a document look at the clock. Both raise `values_change`
  of what changed and are refused on a terminal document.

- The outbox (ADR-0044), with `create` as its first action. A rule's
  `create` of a document of another class is evaluated by the commit and
  written as a row of the `outbox` table in the commit's transaction. A
  worker, started by `Open` unless `Options.Outbox.Manual` is set, or
  `Drain` on the caller's goroutine, makes the new document's commit,
  marks the row done and appends `action_run` to the causing document in
  one transaction: the action happens exactly once, which
  `TestOutbox_SurvivesACrashAtEveryStep` shows by stopping the process
  after the commit, inside the action and after it. The action runs as
  the person whose command queued it, on the business day of the event
  that caused it; what the engine refuses is a dead row with the code
  and an `action_failed` event; a failure of the store is tried again
  with a doubling wait, `Options.Outbox.MaxAttempts` times. A chain of
  actions stops sixteen deep with `too_deep`.
- `Outbox` lists the queued actions, `RetryAction` makes a dead one
  pending again, and every class has the view `<Class>_actions`.
  `Preview` reports what a command would queue with the evaluated
  arguments. `Event` gained `Code`, the code an action died with.
- `examples/classes/receipt.json`, the class the README's Invoice
  creates a document of.

- The rest of the engine's own outbox actions (ADR-0044, items 7, 12
  and 13), each run exactly once and as the person whose command queued
  it:
  - `transition` takes a named transition of the document its `id`
    names, of the rule's own class unless it names another, with
    `values`. The access rule of the transition and the write rights of
    the target apply to the person.
  - `refresh` with an `id` sends the `Refresh` command to another
    document: the triggered variables it names recompute, and the
    document then moves by its own guards. A child wakes its parent
    this way.
  - `create` with `each` and `as` makes one document for each element of
    an array or a rows variable: `"each": "approvers", "as": "approver"`.
    The formulas under `values` and `id` read the element by that name,
    a rows element by field, `line.sku`. Every child is a row of the
    outbox of its own, with its own outcome event.
  - `table.insert`, `table.update` and `table.delete` change one row of
    an explicit table, addressed by its key. An insert of an existing
    key is refused with `duplicate`, an update of a missing one with the
    new code `unknown_row`.
- Three conformance suites under `conformance/rules/`: `each`, `others`
  and `tables`. `TestOutbox_EveryActionSurvivesACrash` stops the process
  inside and after each of the five actions one commit queued and counts
  that each happened once.

- Actions are an extension point (ADR-0045, items 1, 4, 5 and 9): an
  action a rule names is the engine's own or has a definition, and the
  host program implements a definition with a Go function.
  - `DefineAction` stores an action definition, JSON like a class: a
    name of a namespace and a verb, such as `books.post`, with `params`
    and `returns` by scalar type, `required` and a `description`.
    `ValidateAction` reports what it would refuse. A redefinition is the
    next version of the action.
  - `Options.Actions` gives `Open` the Go functions of the process, each
    under the name of its action. A function receives a
    `fado.ActionCall`: the key of the queued action, the same at every
    attempt; the attempt; the arguments by parameter; and the event that
    fired the rule, with the document at that version.
  - A rule calls a defined action with `"args"`, a formula for each
    parameter, or for a `json` parameter a template with a formula in
    the place of every string. The commit evaluates them and queues a
    row; the worker calls the function outside any transaction, at
    least once. No error is `action_run`; an error is tried again with
    the usual wait; an error wrapped with `fado.Permanent` is a refusal,
    dead at once. A function may call the database.
  - `Actions` lists every action a rule may name, the engine's own and
    the defined ones, with their parameters; `Action` returns one.
    `PreviewAction` shows the call an action would receive for given
    values and calls nothing; `TryAction` runs an action once outside
    any document and answers what it returned or how it failed.
  - `api/`, with the JSON schemas of an action definition, of a call
    and of the delivered event, held to the engine by `TestAPISchemas`.
  - The conformance suite `conformance/rules/calls.json`, and
    `BenchmarkOutboxRegistered`.
  - New codes: `unknown_parameter`, `not_registered`, `action_error` and
    `bad_result` for the store; `unknown_action` and `unknown_parameter`
    for a definition.

- Actions the engine sends as HTTP requests (ADR-0045, items 3 to 7).
  - A definition may carry a `request`: `method`, `url`, `headers` and
    `body`, each a pure formula over the parameters, or a template for
    the body. `ok` is a condition over the reply and `result` gives the
    returned values, over `STATUS` and `BODY`. `DefineAction` stores it
    like any definition, and a rule calls it with `args`.
  - `http.call`, the engine's own declared action, for a request written
    in the rule: `url`, `method`, `headers` and `body` under `args`.
  - `Options.Outbound` is what sends. Package `fado/outbound` makes it:
    `outbound.Allow` takes the hosts a database may call and, for every
    connection a definition may name, that connection's hosts. A
    database opened without it sends nothing. Only https, unless a host
    is listed with `http://`; no redirect is followed; an address inside
    the network is refused unless its host is listed by name; ten
    seconds for a call and one megabyte of a reply.
  - `Options.Secrets` supplies the secrets of a connection when a
    request is sent. A definition reads one with `SECRET("name")`, a new
    function that binds only in a request. No secret is stored: not in a
    definition, a row, an event, a preview or an error. A connection's
    secret goes only to the hosts of that connection.
  - A request without a body of its own carries the call as JSON, the
    arguments and the event that fired the rule, with `Idempotency-Key`,
    `FadoDB-Event` and `FadoDB-Attempt` as headers. With `sign` it also
    carries `FadoDB-Signature`, which `fado.VerifySignature` checks.
  - No reply, 408, 429 and 5xx are tried again, no sooner than
    `Retry-After` asks; any other reply that is no success, and a 2xx
    for which `ok` is not TRUE, is a refusal.
  - `PreviewAction` shows the request with secrets masked; `TryAction`
    sends it for real.
  - `URLENCODE(text)`, a pure function, on the Go VM, SQLite and DuckDB.
  - The conformance suites `requests` and `outbound-off` under
    `conformance/rules/`, `conformance/binding/secret.json`,
    `BenchmarkOutboxDeclared` and `BenchmarkDeclaredAction`.
  - New codes: `outbound_off`, `unknown_connection`, `host_not_allowed`,
    `secret_unavailable`, `no_reply`, `try_later`, `refused`, `not_ok`
    and `reply_too_large` for the store; `unknown_secret` and
    `unknown_connection` for a definition.

- The result of an action comes back to the document (ADR-0045, item
  8). An action of the outbox may carry `into`, a map from input
  variables of the rule's own document to formulas over what the action
  returns, with `OK` and `ERROR`. When the action is done or dead the
  engine writes the values as a set of its own, in the transaction that
  marks the action; the eventless transitions follow it, so a process
  goes on from the answer and from a failure. A `create` returns `id`
  and a `transition` returns `state` and `version`. A document that is
  frozen or gone, or that refuses a value, keeps what it has, and
  `QueuedAction` says why under `IntoCode` and `IntoMessage`. A preview
  lists the variables a result will go to.
- `examples/actions/webhook.json`, a generic webhook definition whose
  address and signing key are the connection's secrets, and
  `docs/actions.md`, a guide to writing an action for any REST API. Both
  are run against the engine by the tests of `fado/outbound`.
- The conformance suite `conformance/rules/into.json`.
- `docs/perf/v0.4.0.md`, the Phase 4 measurements against the budgets of
  ADR-0043 to ADR-0047, with the rows of Phases 2 and 3 run again; and
  `TestRulesCampaign` in the store package, the campaign of rules and
  actions that measured them on disk: a transition that fires rules, a
  refresh, the outbox queued and drained, a count over the documents of
  another class with and without an index, a formula that reads the
  life of a document with a long history, and timers.

### Changed

- A filter that compares a date, a datetime or a number variable with
  an operand that reads no document, as in `due < TODAY()` or
  `seen >= ADDDAYS(NOW(), -7)`, now lets SQLite seek the variable's
  index, as a comparison with a literal already did. The answer is the
  same; a `Count` over 100,000 documents and the list of what is due
  read the hundred that match and not every row.
- The engine schema is 5, for the four system columns and the origin of
  an event. A database made before does not open: before 1.0 there is
  no upgrade path, and no database exists yet.
- The write step of a commit costs less, which pays for the four
  columns: an existing document is updated in place, a commit that left
  every value as it was writes its system columns alone, and every
  value is boxed once for the current row, the history row and the
  event. A transition allocates 159 times where it allocated 168.
- The write step boxes the name of a state once and keeps it. A
  transition allocates 155 times where it allocated 159.

- The bytecode format is version 4: a query slot may carry an aggregate
  and the expression it runs over, and says when its key is one term of
  a conjunction (ADR-0046). Classes are compiled from their JSON when a
  database opens, so no file changes.

- **The engine schema is version 4**, for the two columns of the outbox
  that say why a result was not stored. A file made by an earlier
  pre-release engine is refused and recreated.
- A returned value of an action definition may not be named `OK` or
  `ERROR`, which `into` reads beside the returned values.
- `http.call` is a call like any other and takes its arguments under
  `args`. Before it was accepted in any shape and not run; the old
  shape, with `url` beside `action`, is refused with `bad_json`. In a
  process that sends nothing its row gets no answer, `outbound_off`.
- The dependency gate checks that the `fadodb` binary links no HTTP
  client. The placeholder package `internal/rules` is gone; the sender
  is `fado/outbound`.
- A rule that names an action nobody defined is refused with
  `unknown_action`, a parameter the action lacks with
  `unknown_parameter`, a required one left out with `missing` and a
  formula of another type with `type_mismatch`, all when the class is
  defined. Before, any action name was accepted and not run.
- A redefinition of an action that would break a rule of a class is
  refused with `not_allowed` naming that rule, as a redefinition of a
  class or of a table is.
- The worker runs the engine's own actions one at a time and up to
  `Options.Outbox.Workers` actions that leave the database at once, and
  a call that takes long holds back neither a commit nor the engine's
  actions of other documents. `Drain` still runs every action one at a
  time on the caller's goroutine.
- `actions` and `action_versions` are names of the database's own tables
  and no class may take them.
- A `create` is checked when the rule's class is defined: the class it
  names must exist and have states, its values must be scalar inputs of
  that class with formulas of their types, and an input the class
  requires without a default must be given. A class that names another
  is defined after it. The same holds for what the other actions name:
  the class and the transition of a `transition`, the class and the
  variables of a `refresh` of another document, the table and the
  columns of a table action.
- A redefinition of a class or of a table that would break a rule of
  another class is refused with `not_allowed` naming that rule: a
  variable or a column the rule fills is removed or takes a type the
  formula does not have, the transition it takes is removed, a variable
  it refreshes no longer lists `refresh`. The fix is to change that rule
  first. A new required input is not refused; the create that does not
  give it is refused with `required` when it runs.
- A command key may not start with `outbox:`, the prefix of the commits
  the outbox makes.
- The engine schema went to version 3 for the moment columns, before it
  went to 4; there is no upgrade path before 1.0.
- A triggered variable's `table:` and `schedule:` triggers are refused
  with `not_supported` until the phase that runs them; before, they were
  accepted and never fired. A trigger that can fire on a move out of a
  terminal state is refused, since a frozen document computes nothing.
- A class definition is checked for what its rules do, where before only
  their structure was. Refused now: `rules` in a class without states; a
  `set`, `reset` or `refresh` on a trigger that can fire over a frozen
  document or on `delete`; a rule on `schedule`, with `not_supported`
  until it is built; a misspelt property of a synchronous action. An
  `emit` names its event under `event`.

### Fixed

- The outbox worker could leave a row that had fallen due waiting for a
  minute. It read the clock once to look at what is due and again to
  ask when the next row falls due; a row whose second began between the
  two readings was due for neither, and the worker waited out its bound.
  It now asks with one reading. It also wakes when the second of the
  next row begins and not up to a second later, and waits a second, not
  a minute, when the store fails while it asks what falls due next
  (ADR-0044).
- The listing of open transitions read a document without who created
  it, changed it and moved it when the class asked for that only inside
  the condition of a `LOOKUP`, as in
  `LOOKUP(Claim, COUNT(id), created_by = CREATEDBY())`: the function
  answered blank there, and the listing could call a transition closed
  that the commit allowed. It reads them now.
- A rule of an exit that creates a document for each element of a list
  read `ENTEREDAT()`, `ENTEREDBY()` and `ENTEREDFROM()` of the stay that
  begins, where every other rule of an exit reads the stay that ends,
  when no other formula of the class called a function of the
  document's life. It reads the stay that ends.

- An `Open` that refuses a file, because a stored class does not
  compile for one, left the file open: the classes and tables loaded
  before the failure kept their prepared statements. On Windows the file
  could then not be removed or replaced until the process ended. `Open`
  releases them now.
- A `LOOKUP` of a table by a money column, `price = THIS.amount`,
  answered from the in-memory index by the amount alone, so money of
  another currency found a row. ADR-0037 keeps money columns out of the
  index; the binder now records no key for one, and the statement
  compares the currencies. Found by running the formula suite's lookups
  against the store.

## [0.3.0] - 2026-11-09

### Added

- `skills/`, five agent skills in the SKILL.md format, one per hat an
  agent wears with FadoDB: `fadodb-design`, `fadodb-operate`,
  `fadodb-simulate`, `fadodb-mine` and `fadodb-own-work`. Stubs: each
  says what works today with the project's marks and outlines its
  procedure; the shared reference set under `skills/reference/` is to be
  generated from the catalogue and the decision records.
- `examples/classes/`, the sample classes the skills draw on, starting
  with the README's Invoice; `TestExampleClasses` validates and defines
  every sample so the corpus cannot drift from the engine.

### Added

- `docs/perf/v0.3.0.md`, the Phase 3 measurements against the budgets of
  ADR-0040, ADR-0041 and ADR-0042; the commit campaign gained the rows a
  transition, a transition with values, a transition starting a chain of
  three and the listing of ten candidates, and `BenchmarkTransitions10`
  measures that listing alone.
- `At(t)`, a moment the caller chooses for a call that reads or previews
  and writes nothing (ADR-0042): on `Preview`, `PreviewDraft`,
  `Transitions`, `Query`, `Count`, `Get`, `Version` and `History`, `NOW()`
  is the instant and `TODAY()` its UTC day in every formula the call
  evaluates, defaults, guards, eventless guards, access rules and rights
  alike. A preview reports the moment its formulas saw as `Now`, chosen
  or not. A zero moment is the database's clock. A commit runs at the
  database's clock: `At` on `Create`, `CreateMany`, `Set`, `Transition`,
  `Delete`, `Migrate` or `ImportRows` is refused with `not_allowed`, and
  the business date is untouched. `conformance/statechart/clock.json`
  is the specification; a suite may declare its `clock` and a step its
  `at`.
- The rights table and the per-transition rules are enforced in the Go
  API (ADR-0041). A value for a variable whose `write` right is not TRUE
  for the principal in the document's state is refused with `forbidden`
  naming the variable, the first in declaration order on a commit and
  every one under a preview's errors; the right is evaluated over the
  stored document before the assignments, in the state the command
  arrives in, and over a new document after its defaults. A transition
  whose access rule is not TRUE is refused with `forbidden` before its
  guard, and `Transitions` and a preview list it so. Every document the
  API returns, from `Get`, `Version`, `History`, `Query`, `Preview` and
  the commits, is projected for the principal: a variable whose `read`
  right is not TRUE in the row's state is absent from `Values`, a history
  row under the state and the values it had, the rights of the current
  class version applying by name to every row. A filter compiles the read
  right of each variable its `where` or `order` names into its predicate,
  so a row whose state hides the variable neither matches nor sorts, and
  is refused with `forbidden` only when no state lets the principal read
  it; `fields` are projected as a document is. A read right may use pure
  and context functions over the document's own variables only, refused
  at definition with `not_allowed` otherwise; a write right keeps every
  function. An administrator bypasses the rules and the rights and never
  a guard; a call without a principal is nobody. A class without rights
  and transition rules behaves as before. `conformance/statechart/rights.json`
  is the specification; the runner gained `get`, `query` and `count`
  steps with `absent`, `present`, `docs` and `count` expectations.
- The statechart runs (ADR-0040). `Transition(ctx, class, id, name,
  values, opts...)` moves a document along a named transition with the
  assignments applied in the same commit: one version, one history row
  and one event of kind `transition` with the name, the states and the
  versions before and after. The guard sees the recalculated document
  with `STATE()` the state it leaves. Every eventless transition that
  then fires is a further commit in the same transaction, a version and
  a history row of its own with the same values and the new state, and a
  `transition` event without a name caused by the command's event; more
  than eight refuse the command with `too_many_transitions` and write
  nothing. Eventless transitions are evaluated after `Create`,
  `CreateMany`, `Set` and `Transition`, not after `Migrate`. A refusal
  names its code and the fix: `unknown_transition`, `wrong_state`,
  `guard_false` (an erroring guard is FALSE, the message carries its
  code and offset), `too_many_transitions`, and `terminal` for values
  given with a move out of a terminal state. A keyed command that made a
  chain replays to the last version it made.
- `Transitions(ctx, class, id, opts...)` lists the named transitions
  leaving the document's state with whether the principal may take each
  one now, and the code when not. `Preview` and `PreviewDraft` take a
  transition, and a preview of a class with states reports `Path`, the
  states the command visited, and `Transitions`, the listing from the
  resulting state.
- The `<Class>_states` view (ADR-0014, ADR-0040): one row per visit of a
  document to a state, with `entered_at`, `left_at`, `duration`,
  `entry_version` and `exit_version`, over the history table.
- `conformance/statechart/*.json`: scripted suites the store runs in CI,
  the specification of the statechart's behaviour; the format is in
  `conformance/README.md`.

- `STATE()` and `ID()` (ADR-0040), the first of Phase 3: context
  functions returning the state and the id of the document a formula
  runs over, blank for a class without states and when there is no
  document. The VM reads them from its context; in a filter's `where`
  they are the row's `state` and `id` columns, the index term included,
  so `STATE() = "draft"` and `state = "draft"` spell the same predicate;
  elsewhere the SQL binds them as parameters, as `USER()`. Like the
  other context functions they are refused in auto formulas and the
  title. The `context` conformance suite has their cases, and `fadodb
  formula eval` takes `-state` and `-id`.
- `PreviewDraft` (ADR-0025): a class definition compiled for one call
  and run through the commit pipeline over an existing document of the
  stored class, or as a create, writing and storing nothing, so that a
  designer tries a draft on real documents, terminal ones included.

### Changed

- `NOW()` and `TODAY()` in a filter's `where` read the database's clock,
  `Options.Now`, as every other formula does; they read the wall clock
  before.
- A preview of a set that changes nothing no longer stops short: it
  reports `Path`, the states a commit at that moment would visit, and the
  transitions open from the resulting state, with the version as it is,
  so that a preview at a chosen moment answers whether an eventless
  transition would fire.
- `Query` and `Count` keep their prepared statements, at most 64 by SQL
  text, the least recently used one making room, dropped when a class is
  redefined: a repeated filter pays SQLite's parse once, which was half
  of an indexed filter's time, and the read right compiled into a filter
  (ADR-0041) stays within its budget.
- A class without states stores no documents (ADR-0040): it is a
  stateless application, evaluated with `Preview` and `PreviewDraft`.
  `Create` and `CreateMany` on such a class are refused with
  `stateless`. Every stored document is created into its class's
  initial state, and a class with documents keeps its states block.

### Fixed

- A filter whose `where` calls a function the engine implements in Go,
  `UPPER`, `TEXT`, `ROUND` and the others behind `fado_` functions,
  failed with `no such function`: the store never registered them with
  the SQLite driver. `Open` registers them once per process.

## [0.2.0] - 2026-11-08

### Added

- Class versions, terminal states and migrations (ADR-0039 proposed). A
  redefinition is a new class version and touches no document: every
  current and history row records the class version that computed it,
  and a document is read under that version. A document in a terminal
  state, one of `states.final`, is frozen: set, delete, preview and
  migration are refused with `terminal`, and a transition out of it must
  lead to a terminal state. A live document adopts the current version
  at its next commit; `Migrate` adopts every live document of a class in
  batches, as `migrated` events. `DefineClass` and `Validate` refuse,
  with the count of documents in the way, removing a state documents are
  in, thawing a terminal state with documents in it, removing a variable
  that holds values and retyping one that ever did; everything else is
  allowed, and the tables are altered in place. The engine schema is
  version 2; a pre-release file of schema 1 is refused.
- `CreateMany`, the bulk load path of ADR-0035: documents of one class in
  one transaction, one commit each with its history row and event, all
  or none; a batch takes no key and no id. The Phase 2 commit campaign,
  `TestCommitCampaign` in the store package, measures the commit with
  its compute at p99, keyed beside unkeyed commits (ADR-0026), a preview
  beside its commit (ADR-0025), batched creates and commits with lookups,
  on disk with `FADODB_BENCH_DIR`; `BenchmarkRecalculate50` measures the
  full recalculation of a fifty-formula document. The results are
  published under `docs/perf/`.
- Attachments (ADR-0033): a `BlobStore` interface with the `sqlite`
  store, a table in the database file and the default, the `dir` store,
  a directory beside it, and the embedder's own given on open; the kind
  and `MaxBlob`, 32 MiB by default and raisable, in the metadata.
  `PutBlob` hashes a stream while it writes, stops at the limit before
  anything is stored, sniffs the media type when none is given and
  returns the `File` fact to commit. A commit that sets a `File` whose
  hash the store does not hold, or whose size differs, is refused with
  `unknown_blob` naming the variable or the row. `OpenFile` serves a blob
  through a document that holds its hash, at the current version or one
  `AtVersion` names; an administrator may `OpenBlob` by hash. `CheckBlobs`
  recomputes every held blob's hash, and `fadodb check FILE` reports the
  mismatches.
- The filter and the read-only SQL endpoint in the Go API (ADR-0038
  proposed). `Query` takes a class, a `where` formula over the class's
  variables and the system columns, an `order`, a `limit`, an `offset`
  and the `fields` to return, and answers documents in the wire shape
  with their composites loaded in one statement per child table; `Count`
  counts. The formula is bound and translated by the engine, so a filter
  means what a formula means, a row on which it errors does not match,
  and beside the exact predicate every comparison of a column with a
  literal is added on the bare column so that SQLite uses the index.
  `SQL` runs one `SELECT` over the views on a connection whose authorizer
  refuses the physical tables, the schema, writes, pragmas, attachments
  and transactions, with positional parameters and at most 10,000 rows.
  Refusals carry a stable code, the element of the filter and the offset
  inside `where`.
- Explicit tables and `LOOKUP` (ADR-0016, ADR-0020, ADR-0036, ADR-0037
  proposed). A table is defined in JSON with its columns, key and
  indexes, stored as `tbl_<Name>` with the view `<Name>` and filled by
  `ImportRows`, which replaces rows by key or the whole table. The binder
  compiles `LOOKUP(source, column, condition)` into one query slot: the
  condition is a program of its own over the source's columns and the
  document's `THIS.name` variables, a row on which it errors does not
  match, and an equality of a column with an expression of the document
  alone is recorded as the key the index serves. The VM hands the slot to
  a `QueryExecutor`; the store answers from an in-memory index while the
  table has at most `MaxIndexedRows` rows, a database limit kept in the
  metadata, and from a prepared statement narrowed by the table's index
  above that; the SQL generator writes a correlated subquery. The
  conformance suite `lookup` declares its tables inline and runs on the
  VM, SQLite and DuckDB; three binding suites fix what is refused. The
  bytecode format is v3: the query slot carries the condition.
- The store and the Go API, `internal/store` and `fado`: a database file
  opened as ADR-0035 configures it, WAL mode with the checkpointer on its
  own connection and every statement prepared once on one pinned writer,
  `synchronous` and the text limit kept in its metadata. `DefineClass`
  compiles a definition and creates the current, history and child
  tables of ADR-0014 with the views a user sees; `DefineSchema` stores a
  named schema. `Create`, `Set` and `Delete` are commits in the sense of
  ADR-0015, computed first and written last in one transaction: the
  values in the wire shape of ADR-0018 checked against the type, the
  enum list, the range and the text limit, the defaults of the inputs
  not given, the auto variables and the title recalculated over the
  dirty part of the dependency graph, the history row closed and
  written, the child rows carried forward in one statement, the event
  appended with its idempotency key and business date (ADR-0026), and
  the principal recorded as the actor (ADR-0027). A repeated key returns
  the first commit's result; a set that changes nothing commits nothing.
  `Preview` runs the same pipeline and rolls back (ADR-0025), reporting
  the error of every variable whose formula failed. `Get`, `Version`,
  `History` and `Events` read a document, one version, every version
  and the log. Every refusal is an error with a stable code and the
  variable it names. A class with no documents can be redefined; one
  with documents cannot until class migration is decided.
- `formula.ParseExact`, `ExactFromFloat`, `Rescale`, `Render` and `Day`:
  the exact-number and date helpers the store reads and writes values
  with.
- The class model, `internal/model`: a class definition is parsed from
  JSON in written order and compiled with every finding reported at once
  as a list of diagnostics, each a stable code, the JSON pointer of the
  element, the position inside a formula, a message and a fix where there
  is one. It checks names against the reserved columns and keywords,
  types with their scales, enum values, ranges, refs, the properties an
  input and a computed variable may have, binds defaults, formulas, the
  title, guards, rule conditions, access rules and rights with the
  function classes each allows, infers a computed variable's type from
  its formula when none is declared, reads a bare number literal as an
  exact constant for a decimal variable, builds the dependency graph of
  the auto variables and the defaults and refuses a cycle, resolves
  named schemas with the predefined vocabulary of ADR-0032 and user
  schemas that extend them, and lays the variables out as the typed
  columns and child tables of ADR-0014. The statechart, rules, access
  rules and rights are checked for structure and bound now and run in
  their phases.
- Object field paths in formulas (ADR-0031): a variable of type
  `object<Schema>` is read by its leaves, `shipping.city` or
  `venue.address.city`, each a scalar slot of the field's type that
  binds, evaluates and translates to SQL like a scalar variable. The
  conformance suites declare schemas and set leaves by path; the binding
  suite `objects` fixes what is refused for now: a whole object in a
  formula, a path that stops on an object, and rows columns. A `pure`
  binding mode allows pure functions only, for class titles and
  presentation formulas.
- The `bench` package: the end-to-end scenarios behind the budgets of
  ADR-0012, on database/sql so that one scenario runs on every engine. It
  opens Phase 2 with the primary-store benchmark of ADR-0017, commits in
  the shape of the document tables into SQLite and into DuckDB with their
  latency, throughput and whether a second process can read the live
  file, and the point-lookup benchmark of ADR-0020, a keyed lookup on a Go
  map against a prepared statement at five table sizes. The DuckDB target
  lives in the `analytics` module. The results are proposed as ADR-0035,
  the transactional store, and ADR-0036, the in-memory index for `LOOKUP`
  inside commits.
- Exact numbers (ADR-0022): the types `decimal(s)`, `int` and `money(s)`,
  an int64 mantissa at a scale of up to 18 places, with exact `+`, `-` and
  `*`, division at the dividend's scale plus four, the error `overflow`
  where a result does not fit, currencies that must agree or raise
  `currency_mismatch`, and a number constant read exactly when it meets an
  exact operand. New functions `DECIMAL`, `NUMBER`, `MONEY`, `MONEYVALUE`,
  `DECIMALVALUE`, `AMOUNT` and `CURRENCY`; `ROUND` takes a method,
  `half_up`, `half_even`, `down`, `up`, `floor` or `ceiling`; `MIN`, `MAX`,
  `SUM`, `AVERAGE`, `MOD`, `ABS`, `SIGN`, `FLOOR`, `CEILING` and `TRUNC`
  run on either family. A money variable is two columns in SQL, the
  mantissa and the currency.
- A `date` is a calendar day, days since 1970-01-01, and `datetime` is the
  instant the old `date` was (ADR-0023). `DATEVALUE` reads days only; new
  `DATETIMEVALUE`, `DATEOF` and `DATETIME`; the date functions take either
  kind and the two never compare without a conversion; `TEXT` of a
  datetime always shows the clock. Enum values for text variables are
  recorded for Phase 2.
- `IFERROR(value, fallback)` catches an evaluation error in the value
  (ADR-0024), on the VM through a handler and in SQL through the error
  expression.
- `fadodb formula eval` declares the new types and reads money as
  `"12.30 PLN"`.
- The conformance suites `decimal`, `money`, `datetime`, `iferror` and the
  binding suite `exact`; the property generator produces exact, money,
  date and datetime formulas, and found nine divergences between the VM
  and the SQL dialects before they shipped.
- ADR-0025 to ADR-0029 record what the first embedder needs
  from Phases 2 to 7: preview and validate without writing, idempotency
  keys on commands and a business date on events with a published
  transition event, the principal from the embedder and registered action
  executors, field-level rights per state, and the browser VM as one plain
  JavaScript file.
- ADR-0031 to ADR-0034 record, for Phases 2 and 7: named schemas and
  `object<Schema>` values stored flat as one typed column per field, with
  field paths, object blankness and equality in formulas; a predefined
  vocabulary (`Person`, `Organization`, `Address`, `GeoPoint`, `Place`,
  `Period`, `Event`, `Quantity`, `File`) with a presentation block and the
  `chip`, `card`, `coins` and `list` components; attachments as `File`
  values with the bytes behind a blob store, in the database file, a
  directory beside it or the embedder's own store; and `min` and `max` on
  numeric variables with `rating` and `slider` components.

### Changed

- Division by zero is the error `div_by_zero` for `/` as it was for `\`,
  which is now the same operator (ADR-0024). A formula that relied on the
  silent zero guards the divisor with `IF` or `IFERROR`.
- Bytecode format v2: an exact constant pool and the opcodes `const.dec`,
  `dec.neg`, `dec.percent`, `dec.add`, `dec.sub`, `dec.mul`, `dec.div`,
  `dec.scale`, `try` and `end.try`. A constant expression of literals folds
  at definition time.

### Fixed

- `fadodb version` built with `go install .../cmd/fadodb@v0.1.0` reports
  the module version instead of `dev`.

## [0.1.0] - 2026-11-06

Phase 1, the formula engine: the language of ADR-0019, compiled once and
evaluated three ways, on the Go VM, on SQLite and on DuckDB, with the
conformance suite as the specification and property-based campaigns
checking that the three agree. `fadodb formula eval` runs it from the
command line. Documents, storage and the server are not there yet.

### Added

- `fadodb formula eval`: evaluates a formula on the Go VM with variables,
  a user, groups and a clock given as flags, and prints the result in the
  notation of the conformance suite; `-ast`, `-bytecode` and
  `-sql sqlite|duckdb` show instead what the compiler makes of it.
- The release's benchmarks under `docs/perf/v0.1.0.md`: the VM against
  the budgets of ADR-0012, the compiler, and the throughput and the result
  of the acceptance campaigns of one million generated formulas on each
  SQL target.
- Size limits (ADR-0021). Every text a formula produces is at most the
  database's text limit, 65,536 code points by default, and every builder,
  `&`, `CONCAT`, `REPEAT`, `PAD`, `SUBSTITUTE`, `REPLACE` and `REGEXREPLACE`,
  decides `out_of_range` before it builds, on the VM, SQLite and DuckDB
  alike. A count past the end of a text now reads to the end instead of
  failing above a billion. The limit is a property of the database,
  carried by `formula.Limits`, read by the VM and passed to the dialects;
  the `limits` conformance suite runs at 64 code points and the property
  campaigns at 1,024 (`FADODB_FUZZ_MAXTEXT`).
- Property-based testing of the engine. `internal/formula/fuzz` generates
  random well-typed formulas with inputs and context and runs each on the
  Go VM and on a SQL dialect; the SQLite and DuckDB property tests run
  2,000 formulas in CI and a campaign of any size locally (`FADODB_FUZZ_N`,
  `FADODB_FUZZ_SEED`, `FADODB_FUZZ_WORKERS`). The campaigns found, and the
  branch fixes, with conformance cases for each: a blank-typed program did
  not survive encode and decode; `NOT(a) <= b` parsed as `NOT (a <= b)`,
  and `NOT(` is now the function form; `/`, `\` and `MOD` could overflow
  to infinity without `not_a_number`; `POWER` with a large integer
  exponent was hundreds of ulps off the C math libraries and is computed
  in double-double arithmetic; `LOG` was inexact at the powers of ten and
  wrong for a base below 1 on both dialects; `LN` and `LOG` of a subnormal
  number were the logarithm of the smallest normal number instead; and
  `ROUND`, `FLOOR`, `CEILING` and `TRUNC` beyond 22 digits scaled by a
  power of ten that differed by an ulp between Go and C. They now scale by
  the correctly rounded power on every evaluator and leave a value alone
  once its scaled form is already an integer. On SQLite, `^`, `POWER`, `LN`
  and `LOG` run through the engine's own functions, since SQLite's are the
  platform's and differ in the last bit between Linux, macOS and Windows,
  and so does the rendering of a number as text, since SQLite's `printf`
  rounds the fifteenth digit its own way; and the VM rounds every product
  explicitly, so that arm64's fused multiply-add cannot change a result.
  The million-formula acceptance campaigns found four more: `SUM` and
  `AVERAGE` now add left to right in plain arithmetic on every evaluator,
  since the compensated sum of the VM and SQLite differed from DuckDB's
  inline additions by far more than the tolerance when large terms cancel;
  a JSON array index in a `JSONVALUE` path is a JSON pointer index, digits
  without a leading zero or a sign, as DuckDB reads it; `DATE` on DuckDB
  carries months and days by civil-calendar arithmetic instead of clamped
  timestamp intervals, which could cancel into a date in range where the
  real one was not; and the property runner compares two noisy numbers
  glued into one text by trying every split.
- DuckDB dialect of the SQL generator and the `analytics/` module. The
  dialect computes in DOUBLE with every literal cast, builds dates as naive
  timestamps so the session time zone never leaks in, and ships its engine
  functions as `CREATE MACRO` statements, inlined SQL rather than Go
  callbacks, under the same `fado_<name>` names as SQLite's registered
  functions. The module is a separate Go module with cgo and holds only the
  DuckDB conformance runner; CI builds it on Ubuntu, and `make analytics`
  runs it locally. All evaluation cases now agree across the VM, SQLite and
  DuckDB, aggregates to the relative 1e-12 of ADR-0019.
- `REGEXREPLACE` takes its replacement literally, with no backreferences,
  so the three targets agree; the catalog and the suite say so.
- SQL generator with the SQLite dialect. A program becomes a value
  expression and an error expression over the columns holding its
  variables, with the short-circuit rules of ADR-0019 kept in both. The
  dialect uses native SQL where SQLite agrees with the VM and calls the
  engine's own Go functions, registered by `internal/formula/sqlitefn`,
  where it does not. The differential test runs every evaluation case on
  SQLite through `modernc.org/sqlite`, now a dependency of the core module.
- Binder, bytecode v1 and the Go VM. The binder gives every expression a
  static type by ADR-0019 and rejects type errors, unknown names and
  context functions in auto mode at definition time, with a column in every
  message. Bytecode v1 (`FADO`, versioned, binary and JSON debug forms)
  reserves the `lookup` and `query` opcodes of ADR-0020. The VM executes
  the scalar language with zero allocations on the scalar path and the
  closed list of error codes. 59 of the 67 catalog functions run; the
  composite and query functions are refused as not implemented yet.
- The conformance runner executes every evaluation case on the VM, and a
  binding suite pins the static typing rules and the auto-mode restriction.
- Formula lexer and parser for the grammar of ADR-0019, with positions in
  every error, and the function catalog (`internal/formula/catalog.json`,
  67 functions with their evaluation class, arguments and summary) that the
  parser checks arities against.
- The conformance suite under `conformance/`: 579 evaluation cases in seven
  suites (blank, logic, math, numeric, arithmetic, text, date) and a syntax
  suite of 220 accept and reject cases, with the loader that validates them.
  Until the VM lands, evaluation cases are checked to parse.
- ADR-0019: formula language semantics: grammar, static types, coercion,
  comparison, dates, text, composites and errors.
- ADR-0020: `LOOKUP` and `QUERY` execution: one engine call per invocation,
  point lookups off the analytical engine, set-wise recomputes as one
  statement per class, DuckDB dialect tested from Phase 1.

### Changed

- The benchmark gate warms both trees up and interleaves the measured
  rounds, so a shared runner's drift lands on the base and the change
  alike instead of on whichever tree ran first.
- ROADMAP and README mark Phase 1 as in progress.
- `modernc.org/sqlite` is scheduled for Phase 1 instead of Phase 2, for the
  SQL conformance runner.

## [0.0.1] - 2026-11-05

Project scaffold. Nothing to run beyond `fadodb version`.

### Added

- Go module, `cmd/fadodb` with `version` and `help`, package layout with
  documented placeholders.
- Quality gates: gofmt, vet, staticcheck, govulncheck, race tests on three
  platforms, dependency budget gate, binary size gate, benchmark regression gate.
- Release pipeline with GoReleaser: binaries for Linux, macOS and Windows on
  amd64 and arm64, with checksums.
- Public architecture overview and twelve architecture decision records.
- Contributing guide, code of conduct, security policy, issue and pull request
  templates, roadmap.

[Unreleased]: https://github.com/MojoHand70/fadodb/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.7.0
[0.6.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.6.0
[0.5.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.5.0
[0.4.1]: https://github.com/MojoHand70/fadodb/releases/tag/v0.4.1
[0.4.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.4.0
[0.3.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.3.0
[0.2.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.2.0
[0.1.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.1.0
[0.0.1]: https://github.com/MojoHand70/fadodb/releases/tag/v0.0.1
