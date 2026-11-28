# Glossary

The words of FadoDB, one line each. Words in `code` are written
exactly so in a class or a formula.

## The main things

| Word | Meaning |
|---|---|
| class | one kind of document, such as an invoice. Written once, as one JSON document; see [The class definition](class.md) |
| document | one thing of a class: one invoice. It has an id, a state, values and a history |
| variable | one named value that every document of a class holds, such as `amount` |
| input | a variable that a caller sets, or that takes its default |
| computed variable | a variable with a `formula`; the engine works it out and nobody sets it |
| triggered variable | a computed variable that is worked out only at the moments its class names, such as the entry into a state, and keeps its value in between; see [Triggered variables](triggered-variables.md) |
| kept value | the value of a triggered variable: kept with the document and each of its versions until a trigger brings it again, with the version and the moment it was worked out; see [the moment](triggered-variables.md#the-moment) |
| `table:` | a trigger that follows a table: when the rows of the table it names change, the variable is worked out again in every document of its class that is not finished; see [Following a table](triggered-variables.md#following-a-table) |
| `schedule:` | a trigger that follows the clock: at the moments its words name, such as `daily at 03:00` or `every 4h`, the variable is worked out again in every document of its class that is not finished; see [Following the clock](triggered-variables.md#following-the-clock) |
| time zone | the database's own, such as `Europe/Warsaw`: its schedules go by it, and `throughput` begins its days by it. A database never given one is in UTC, and `TODAY()` and `NOW()` are in UTC always; see [Following the clock](triggered-variables.md#following-the-clock) |
| live variable | a computed variable that is worked out when somebody reads it, for that reader, and kept nowhere; the command `live` reads it; see [Live variables](live-variables.md) |
| list | a variable whose whole formula is a `QUERY`: some documents of a class, or the rows of a table, with some of their columns. In a live variable it is read for the reader and kept nowhere; in a triggered variable it is a kept list; with a total among its columns it is totals by group; see [Lists of other documents](lists.md) |
| kept list | a list in a triggered variable: kept with the document and each of its versions, as lines somebody entered, one list for everybody, brought at the triggers the variable lists; see [A list that is kept](lists.md#a-list-that-is-kept) |
| totals by group | a live list whose `QUERY` has a total among its columns, as `QUERY(Invoice, {owner, COUNT(id), SUM(net)})`: one line for each group, as in a pivot table. A line is no document and has no id; see [Totals by group](lists.md#totals-by-group) |
| figure | one value worked out of a list of the document, in a live variable: `COUNT(invoices)`, `SUM(invoices.net)`, `MEDIAN(invoices.net)`, `FIRST(invoices.number)`; it counts what its reader may read of the list; see [Lists of other documents](lists.md) |
| refresh | a commit that works triggered variables out again |
| formula | an expression in the style of a spreadsheet, such as `ROUND(net * (1 + vat_rate), 2)` |
| state | where a document is in its process, such as `INAPPROVAL` |
| transition | a named move from one state to another, such as `approve` |
| guard | a condition on the document that must hold for a transition |
| `before:` | a trigger of a triggered variable, as `before:approve`: the variable is worked out when somebody asks for the transition, before the guard is asked, and kept in the version the move makes when the guard holds. A guard reads its document alone, and this brings onto it what the guard needs from outside; see [Before a transition](triggered-variables.md#before-a-transition) |
| automatic transition | a transition the engine takes by itself, by time or by a condition; also called silent |
| final state | a state in which a document is finished and frozen |
| rule | "when this happens, do that": it runs by itself when a document is created, changed or moved |
| action | one thing a rule does: set a value, create another document, call another system |
| inlet | the way in for a system that speaks only its own language, such as a bank: a definition, like an action's, that says how a message proves who sent it and which one command it becomes; see [A way in for messages](a-way-in-for-messages.md) |
| door for agents | the commands offered to an agent as tools, in the Model Context Protocol: `fadodb mcp`, which an agent's program starts, or `/mcp` at the server itself. What an agent does is judged by the rules of the classes and kept in the log; see [The door for agents](the-door-for-agents.md) |
| table | a list of reference data, such as exchange rates, that formulas look values up in; see [Tables](tables.md) |
| app | a name for the classes and the tables that belong together, such as the expenses of a company. It holds no document and decides nothing; see [Apps](apps.md) |
| system table | one of the four tables of the app `System`, the engine's own: `SystemApps`, `SystemClasses`, `SystemUsers` and `SystemGroups`, what the database holds at the moment they are read. A formula reads them as any table, and nobody writes them; see [The system tables](system-tables.md) |
| schema | a named set of fields, defined on its own, for a variable that holds an object such as an address; see [Schemas and objects](schemas-and-objects.md) |
| file | a file attached to a document: the document holds its facts, the hash, the name, the kind and the size, in an object of the schema `File`, and the bytes are kept beside under the hash; see [Files](files.md) |
| object | one value with named fields, by a schema. It has no id and no life of its own, and a formula reads its fields one at a time, as `depot.address.city` |
| `label` | what a person reads in place of a name: of a class, a variable, a state, a transition, a column or a field. Without one, the name is shown; see [what a class says to people](class.md#what-a-class-says-to-people) |
| `description` | what a thing of a class is for, for people and for agents; the engine notes each variable, state and transition that has none |
| The List | the table of the documents of a class that a page shows, one document to a row. The variables marked `"list": true` are the columns it may show |
| `format` | how a page shows a number or a date, in the patterns of Excel's number formats; the person's language gives the separators and the names of the months |
| words | what a class, a table or a schema says to people in another language: the labels and the descriptions of its things by their paths, kept beside it as a definition of their own, under a tag of BCP 47 such as `pl`; see [words in other languages](class.md#words-in-other-languages) |
| bundle | a class in a person's words with its formulas compiled, in one call, `get_bundle`, so that a page works a document out as the server does; see [The bundle](the-bundle.md) |
| section | a part of a class's outline: the variables that belong together, such as the amounts of an invoice, with a label and a description, and sections inside it. `rights` may name a section for every variable in it; see [sections](class.md#sections) |

## Who may

| Word | Meaning |
|---|---|
| caller | whoever makes a call: a person, an agent or a service |
| principal | the caller as the engine is told of it: an id, its groups, what it is, and for whom it acts |
| host program | the program that embeds FadoDB. It says who the caller is on every call: from its own users, or from the users, groups and keys it lets the database keep |
| user | a caller that a database knows by its id, when the database keeps its own users; see [the commands](commands.md#users-groups-and-keys) |
| key | how such a user proves who it is: one line of text that begins `fado_`, shown once when it is made |
| session | how a person's browser stays signed in to `fadodb serve`: a cookie the server's own pages send, 8 hours unused and 30 days at most; see [Signing in](signing-in.md) |
| company sign-on | signing in with the company's own provider, such as Microsoft or Google, by OpenID Connect: the provider vouches for the person, and the server ties their account to the user an administrator added; see [Signing in](signing-in.md#a-companys-sign-on) |
| pass | how a client a person allowed calls the server: a line that begins `fadoat_`, good for one hour and for one address, the commands or the door for agents, and renewed by a pass of its own; see [Signing in](signing-in.md#connecting-a-client) |
| grant | a client a person allowed, such as Claude: its passes hold until the person or an administrator ends it, or no pass was renewed for 30 days; see [Signing in](signing-in.md#connecting-a-client) |
| passkey | how a person signs in to `fadodb serve`: a key their phone or computer makes for the server's address and opens with their fingerprint, their face or the device's PIN; the server keeps its public half; see [Signing in](signing-in.md#signing-in-with-a-passkey) |
| invitation | a link an administrator makes for a person, good once and for 7 days, with which they add their first passkey; see [Signing in](signing-in.md#an-invitation) |
| code for the phone | the QR code the page of an invitation, and the account page, show on a computer: a phone's camera reads it and the person adds a passkey on the phone, which signs in the phone and the computer that showed it; good once and for 10 minutes; see [Signing in](signing-in.md#the-code-for-the-phone) |
| second step | a code of six digits from an authenticator app, asked after the password of an administrator or a designer, until passwords go; see [Signing in](signing-in.md) |
| group | a named set of callers, as the host program knows them, such as `ACCOUNTING` |
| system group | one of four groups that hold work which is not about one document: `administrators`, `designers`, `content-administrators`, `analysts` |
| `everyone` | every caller that has an id |
| `access` | the block of a class that says who may see, create, change, delete and move a document |
| `rights` | the block of a class that says who may write or see single variables, state by state |
| editor | not a word of the engine: whoever `access` and `rights` let change a document in its state |
| content administrator | a caller who may see and act on any document past its `access` and `rights`, with a reason: the log keeps the reason of a change, and the log of reads that of a read |
| substitution | a document that says one caller may act for another, from when and until when |

## What the engine keeps

| Word | Meaning |
|---|---|
| commit | one change to one document, made whole or not at all: a create, a set, a transition, a delete |
| version | the document as one commit left it. Every version is kept |
| history | all the versions of a document, oldest first |
| history of the rows | each row of a table as it was until a change replaced or removed it, and who made each change and when, kept unless the table says `"history": false`; `row_history` reads the history of one row; see [The history of the rows](tables.md#the-history-of-the-rows) |
| lineage | where a value comes from, one level a call: how it is made, when, and what it read. For a kept value it is worked out again from the history and says whether it adds up; the command `lineage`; see [Where a value comes from](guides/where-a-value-comes-from.md) |
| event | the record of one commit: what happened, who did it, when, and for a move from which state to which |
| comment | words the engine keeps beside a document, with who wrote them, when and on which version. A comment makes no version of the document; its event is of the kind `commented`; see [Comments](comments.md) |
| log | all the events, in order. Nothing in it is ever changed |
| log of reads | what a content administrator saw past a rule, and every SQL statement, each with who and why. Database administrators read it. [The log of reads](the-log-of-reads.md) |
| preview | a commit tried and not made: what it would do, and whether it would be refused |
| refusal | the engine's answer to a call it does not carry out. It has a code, a place and often a fix |
| note | a finding in a definition that refuses nothing, such as `reads_all`, a lookup or a list that no index serves, or `builds_index`, an index a definition would make on a large class; see [Findings in a definition](refusals.md#findings-in-a-definition) |
| outbox | where the actions that reach outside a commit wait until the engine runs them |
| stream of changes | the log read forward for one caller: it is told of every event it may read of, as each is written, and need not ask again and again. [The stream of changes](the-stream-of-changes.md) |

## Questions over a whole class

| Word | Meaning |
|---|---|
| analytical engine | DuckDB, in the program `fadodb`: it answers the questions that read a whole class, a total, a count, a sorted page, the flow, from the copy, in milliseconds; see [An analytical engine](opening-a-database.md#an-analytical-engine) |
| copy | the file `<database>.duckdb` beside a database that `fadodb` opens: the documents, their versions and events, the moves and the rows of the tables, which the database feeds itself. It is never the truth and needs no backup: the engine makes it again when it is lost; see [An analytical engine](opening-a-database.md#an-analytical-engine) |
| `fadodb`, `fadodb-core` | the two programs from the same code: `fadodb` with the analytical engine, and `fadodb-core` in pure Go without it, which answers every question by the store. Every command and every answer is the same in both; see [A server with the analytical engine](serving-a-database.md#a-server-with-the-analytical-engine) |
| flow | the command `flow`: of a class, how many documents are in each state now, how long the stays in it lasted and how many moved along each transition, among the documents the caller may read; a chart of the process is drawn from it; see [See where documents wait](guides/see-where-documents-wait.md) |
| stay | the time one document was in a state, from the move that brought it there to the one that took it out; a document that left a state and came back has two stays in it; see [What a number says](guides/see-where-documents-wait.md#what-a-number-says) |
| spread | how the stays of a state spread, asked with `"spread": true`: the middle stay, which half of the stays did not outlast, and the slow tenth, which nine stays in ten did not outlast; see [How the stays spread](guides/see-where-documents-wait.md#how-the-stays-spread) |
| throughput | the command `throughput`: how many documents were made and how many finished in each day, week or month, so whether the pile grows; see [Whether the pile grows](guides/see-where-documents-wait.md#whether-the-pile-grows) |
| path | one way documents went through the states, in the order they came into them; the command `paths` lists them, the most taken first; see [Which way documents went](guides/see-where-documents-wait.md#which-way-documents-went) |
| documents behind a number | the documents a number of `flow`, `throughput` or `paths` counts, asked with the member `documents`, a page at a time; see [The documents behind a number](guides/see-where-documents-wait.md#the-documents-behind-a-number) |

## The codes of a refusal you meet first

The whole list is in [The refusals](refusals.md).

| Code | Meaning |
|---|---|
| `forbidden` | the caller may not do this. The refusal names the line of `access` or the variable |
| `guard_false` | the document does not meet the guard of the transition |
| `unknown_document` | there is no such document, or the caller may not see it |
| `required` | a required variable has no value |
| `terminal` | the document is in a final state and cannot change |
| `wrong_state` | the transition does not leave the state the document is in |
| `stale_version` | the command said which version of the document its sender read, and the document has moved on; nothing was written. See [Act only on the version you read](guides/act-on-what-you-read.md) |
| `too_wide` | a question that no index serves read the documents of its class one by one for as long as the database allows, and was stopped; the hint names the variable an index is needed on. See [A large class](lists.md#a-large-class) |

## Whose word is it

In a class, the words on the left of a colon are FadoDB's: `variables`,
`states`, `transitions`, `rules`, `access`, `rights`, and the words
inside them such as `type`, `from`, `guard`, `read`, `write`. So are
the functions, which are written in capitals with brackets, such as
`USER()`, and `TRUE`, `FALSE` and `*`.

The names are yours: of the class, of each variable, state and
transition, and of the users and groups, which come from your own
program.
