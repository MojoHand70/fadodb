# ADR-0067: The log of reads: what a content administrator sees past a rule, and every SQL statement

- Status: Accepted; amends ADR-0052 (question G is answered: a content administrator's read past a rule needs a reason and is recorded, and so is an import into a table past its write rule), ADR-0033 (a file opened by its hash alone needs a reason), ADR-0038 (every SQL statement is recorded), ADR-0051 (a table's rules: the content administrator's pass needs a reason), ADR-0058 (what a content administrator hears without a reason), ADR-0055 (one command joins the table, and the reading commands take `reason`); amended by ADR-0099 (Proposed: a table keeps the history of its rows, which says who changed a row; the log of reads still keeps a write or a read past a rule, and `row_history` past one)
- Date: 2026-11-17

## Context

ADR-0052 made the content administrator: the one caller who may see,
change and move any document past its rules, so that a document that
waits for a person who left is not stuck for good (ruling F). Every
change such a caller makes past a rule needs a reason, and the event
of the commit keeps the rule and the reason. Reads were left out. Its
question G said that a log of such reads, together with the statements
of SQL, gets a record of its own, and the owner ruled on 2026-11-17
that it is the first step after v0.6.0. The release notes of v0.6.0
say plainly that a read leaves no trace.

What the engine does today, read from the code on 2026-11-17:

- **A content administrator's read does not ask the rule at all.** The
  checks that decide whether a caller sees a document, a row of a
  list, an event of the stream or a variable under the rights of the
  class answer yes for this group before they read anything. It costs
  nothing, and it leaves nothing.
- **An analyst's SQL has no rule.** `sql` runs one SELECT over the
  views and reads every row and every variable (ADR-0038, ADR-0049
  item 12). Nothing is kept of the statement.
- **A table keeps no log.** A content administrator reads and writes
  every table past its rules, asked for no reason (ADR-0051 item 7,
  ADR-0052). The manual says so in its chapter on tables.
- **A file opened by its hash alone** is for a content administrator
  only (ADR-0033), and leaves nothing either.

Other systems that give somebody the power to see everything make the
same two demands this record makes: say why, and leave a trace that
somebody else reads. A hospital's record system lets a doctor "break
the glass" to open a patient's file in an emergency, asks for the
reason and audits the opening; SAP's emergency access gives a
"firefighter" account for a while, with a reason and every step
logged for review.

The case used below: at Acme, Jan has a salary review, and Ewa is his
manager. Olga is a content administrator, Marek an analyst, and Tomek
a database administrator.

```json
{
  "name": "SalaryReview",
  "variables": {
    "employee": {"type": "text", "required": true},
    "manager":  {"type": "text", "required": true},
    "salary":   {"type": "money", "scale": 2}
  },
  "states": {"initial": "draft", "list": ["draft", "agreed"], "final": ["agreed"]},
  "transitions": [{"name": "agree", "from": "draft", "to": "agreed"}],
  "access": {
    "read":   "employee = USER() OR manager = USER()",
    "create": "INGROUP(\"hr\")",
    "update": "manager = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {"agree": "manager = USER()"}
  }
}
```

## Decision

1. **Two readers are recorded, and only for what the rules would not
   show them.**
   - A content administrator, for every document, row, event, file
     and variable that only the override lets them see. The engine now
     asks the rules of a content administrator as it asks them of
     everybody else; a read the rules allow anyway is not recorded.
     Olga reads invoice 1042, which her group lets her read, and
     nothing is kept. She opens Jan's review, and that is kept.
   - An analyst, for every SQL statement.

2. **A content administrator who gives no reason reads as anybody
   does.** Without a reason, every command below answers Olga as it
   answers a caller with her id and her groups who is not in the
   system group: a list leaves out what the rules hide, a count does
   not count it, the stream does not tell of it, and a variable the
   rights hide is hidden. A command about one document that only the
   override would show her is refused with `reason_required`, the
   code a change past a rule gets today; its path is the place of the
   rule, `/access/read`, or `/rights/<state>/<variable>/read` for a
   variable. With a reason she sees everything, as before this record,
   and the read is recorded.

   ```json
   {"class": "SalaryReview", "id": "SR-017", "reason": "Jan asked HR to correct his manager"}
   ```

   The commands: about one document `get`, `version`, `history`,
   `events`, `transitions`, `preview`, `preview_delete`; about many
   `query`, `count`, `flow`, `due`, `preview_many` and `subscribe`; a
   file of a document; a table's `table_rows`. Each of them gains the
   member `reason`, which the commands that change a document take
   today (ADR-0055), and `sql` gains it too; in the Go API it is the
   option `Reason`, which these calls now heed. A file travels beside
   the commands, at the server's address for files, which takes the
   reason as a parameter of the address. A file opened by its hash
   alone, which only a content administrator may, needs a reason every
   time and is recorded every time.

3. **One entry for each call.** It names every document the call
   showed past a rule, and the places of the rules it got past. A list
   holds at most 10,000 documents (ADR-0038), so an entry is bounded.
   The question "who has seen Jan's review?" has one exact answer.

   ```json
   {"seq": 412, "at": 1794994200, "by": "olga", "kind": "person",
    "command": "query", "class": "SalaryReview",
    "documents": ["SR-001", "SR-002", "SR-017"],
    "rules": ["/access/read"],
    "reason": "yearly check of the reviews"}
   ```

   - `count` and `flow` show no document, only numbers. Their entry
     holds `counted`, how many documents only the override counted,
     and names none.
   - The stream (`subscribe`) is one call that lasts. Its entry is
     written for each batch it sends that holds an event past a rule,
     and names the documents of that batch.
   - `table_rows` names the table and holds `rows`, how many rows it
     returned.
   - A file opened by its hash names the hash under `blob`.
   - `for` names whoever the caller acted for (ADR-0053), and
     `read_at` the moment the command read at when it was not now
     (`at`, ADR-0042).

4. **Every SQL statement is recorded:** who ran it, when, its text,
   its arguments and how many rows it returned; never the rows. A
   statement that fails is recorded with the code of its failure in
   place of the count. A reason may be given and is then kept; none is
   asked.

   ```json
   {"seq": 413, "at": 1794995520, "by": "marek", "kind": "person",
    "command": "sql",
    "statement": "SELECT employee, salary FROM SalaryReview WHERE salary > ?",
    "args": [20000], "rows": 14}
   ```

5. **Database administrators read the log, through a new command,
   `reads`.** Members, each optional: `by`, `class`, `id` (with
   `class`), `table`, `command`, `since` and `until` (moments, as
   `at` takes them), `after` (a `seq`, to read on from) and `limit`
   (1,000 when left out, at most 10,000). It answers
   `{"reads": [...]}`, oldest first. Only the system group
   `administrators` may ask; anybody else is refused with `forbidden`
   and the path `/system/administrators`. The log is not a view, so
   SQL cannot read it, and a content administrator or an analyst who
   is not also a database administrator cannot read the log of their
   own reads. Whoever reads past the rules is not whoever checks the
   log. The log names documents, rules and statements, and holds no
   value of a document, which keeps ADR-0052's line that whoever runs
   the database does not read its documents. A statement's arguments
   are the analyst's words, not a document's values, and are kept.

   The Go API gains `Reads`, the door for agents gains the tool
   `reads` (ADR-0064: every command is a tool), and `fadodb exec`
   takes it like any command.

6. **No trace, no answer.** The entry is committed, in a transaction
   of its own through the database's one writer, before the answer is
   returned. If it cannot be written, the call fails with `storage`
   and shows nothing. A read that is recorded therefore waits for the
   writer as a small change does. A read nobody records takes the path
   it takes today.

7. **Tables too.** A content administrator who writes into a table
   past its write rule, by `import_rows`, needs a reason, which the
   command gains as a member like the reading ones, and the import is
   recorded with the table, the number of rows and the rule
   `/access/write`. A table action a rule queued (ADR-0044) carries
   the reason of the commit that queued it, as it does since ADR-0052,
   and is recorded the same way when it gets past the table's rule.
   Without a reason the import is refused with `reason_required`.

8. **Nothing deletes an entry**, as nothing deletes an event
   (ADR-0006). The log lives in a table of the engine's own, written
   only by the engine and only by adding a row. Adding it raises the
   engine schema from 11 to 12.

9. **What is not recorded, and why.**
   - A read the rules allow: it is anybody's ordinary work.
   - A change past a rule, and the document its answer shows: the
     event of the commit keeps the rule and the reason already
     (ADR-0052).
   - A formula that reads other documents: a formula sees every
     document (ADR-0046, the owner's ruling of 2026-11-10); what it
     shows a person is under that person's rules.
   - The engine's own work: the worker, a wake-up, a migration.
   - The queue of the outbox shown to a database administrator, which
     ADR-0052 already says holds the values actions send.
   - A program that reads the database file itself. Whoever can copy
     the file reads everything in it (ADR-0052).

10. **The suite.** `conformance/access` gains cases for each command
    of item 2: a content administrator with no reason sees what the
    rules give anybody; with a reason she sees everything and leaves
    exactly one entry; a read the rules allow leaves none; and an
    analyst's statement leaves one, the failing one too.

11. **What this record leaves out.**
    - Telling Jan who has read his review. A person who reads the log
      of their own documents is a record of its own, if it is wanted.
    - A list that says how many documents it left out for want of a
      reason.
    - Trimming the log after some years.
    - The analytical engine of Phase 8. Its SQL is SQL, and this
      record holds for it when it is built.

## The owner's rulings

All of 2026-11-17, put in the chat before this record was written,
with the case above; the owner answered "Agreed on all seven".

1. **What is recorded: only what the override shows** (item 1). The
   other way was every read a content administrator makes, rules
   unasked, which fills the log with reads anybody in her groups could
   make.
2. **A read past a rule needs a reason; without one a content
   administrator reads as anybody** (item 2). The other way was a
   reason left optional, the read recorded either way.
3. **One entry for each call, naming every document it showed past a
   rule** (item 3). The other way was the filter and a count, which
   leaves "who saw Jan's review?" to a guess.
4. **Every SQL statement is recorded, a reason optional** (item 4).
   The other way was a reason for every statement.
5. **Database administrators read the log, through `reads`** (item
   5). The other way was analysts through SQL, which would have Marek
   audit himself.
6. **The entry is written before the answer leaves** (item 6). The
   other way was entries written in batches shortly after, which loses
   the last reads to a crash.
7. **An import into a table past its write rule needs a reason and is
   recorded** (item 7). The other way was tables left as they are.

The same message said that nothing deletes an entry (item 8), and
listed what is not recorded (item 9).

The owner accepted the record the same day ("accept ADR-0067"), and
merged it.

## Settled while building

Each of these is mine, decided while building on 2026-11-17, and the
owner's to overrule.

1. **A class the engine brings is seen as before.** A content
   administrator reads `Substitution` with no reason and leaves no
   entry, since ADR-0052 has them see such a class as any member does.
2. **The answer of a change.** With a reason, the document a change
   answers with is shown whole. What it shows past a read rule or a
   read right that its event does not name already is written to the
   log in the same transaction as the commit: had the class a right
   that hides the salary from Olga, her change past the update rule of
   Jan's review would name `/access/read` and `/access/update` on its
   event, and that right in the log. Without a reason the answer is
   projected and veiled as for anybody. A preview, a change that wrote nothing and a preview of
   many keep no event, so their entry names every rule the answer got
   past; a preview of many writes one entry for the batch once it is
   taken back.
3. **A count and the flow** count twice, as the content administrator
   passes the rules and as anybody would, and keep the difference as
   `counted`. The flow's is of the documents in its states now.
4. **The stream** writes its entries, one for each class of a batch,
   before it tells the batch; if they cannot be written it tells
   nothing and stays where it was. `subscribe` takes `reason`, and the
   server's door passes it to the engine.
5. **Files.** An entry for a file names the document and, under `blob`,
   the hash; a file in a variable a right hides names that right. Over
   HTTP both addresses of files take `reason=`; bytes opened by their
   hash alone always need it, and that refusal names no path, since
   no rule of a class is in play.
6. **Tables.** A content administrator without a reason is refused
   with `reason_required` at `/access/read` or `/access/write`, and not
   `forbidden`, so that they know a reason would do. A rule's table
   action carries the reason of the commit that queued it; a commit
   that gave none queues an action the table refuses with
   `reason_required`, and it fails as any refused action does. Its
   entry is named after the action, `table.insert`.
7. **SQL.** A statement refused before it runs, because its caller is
   no analyst, is not recorded: it read nothing. One that fails while
   it runs is recorded with its code, and so is one whose caller left
   before the answer.
8. **The log wakes no stream.** Its transaction adds no event, so it
   ends without telling the streams of changes, which would read the
   log of events for nothing: fifty streams of content administrators
   would otherwise wake one another at every batch.
9. **The names.** The table is `fado_reads`. An entry names its call
   as the command set does, `get`, `query`, `sql`; a file is
   `open_file` or `open_blob`. `reads` counts `since` in and `until`
   out, as `flow` does.
10. **An entry costs an ordinary read nothing.** It is made only when
    a content administrator gives a reason, so every other read
    allocates no more than before.

## Consequences

- An act past a rule is now one kind of act, whether it reads or
  changes: it needs a reason and leaves a mark. A change keeps its
  mark on the event of its commit, a read in the log of reads.
- A face, and an agent in the group `content-administrators`, must ask
  its person for a reason, or give its own, before it shows what the
  rules hide. Without one it sees what anybody in the same groups sees,
  which is often all it needs. The kit of Phase 7 asks once and sends
  the same reason with each call; each call is an entry.
- A host program that lists `due` as a content administrator, to touch
  what is due, gives a reason, or sees only what the rules let it see.
  The engine's own wake-up is not such a caller (ADR-0047).
- A content administrator's read that is recorded waits for the
  writer. It is the exception it is meant to be.
- Separation holds only if the host keeps the groups apart. A host
  that puts one person in both `content-administrators` and
  `administrators` gives that person the power to read the log of
  their own reads. The manual says so where it names the groups.
- The chapter on tables, the page of the system groups and the guide
  to naming the caller change with the code, and the release notes of
  v0.6.0 are answered by the next release's.
- Engine schema 12: a database made by v0.6.0 is refused by the
  release that builds this, as every database of an earlier schema has
  been before 1.0 ("there is no upgrade path before 1.0; recreate the
  file"). Whether an upgrade path begins earlier than 1.0 is not this
  record's question.

## Performance

- **A read nobody records** asks one thing more: whether the caller
  is a content administrator who gave a reason. An ordinary caller's
  path is unchanged, and the benchmarks of the gate read as ordinary
  callers, so they keep their allocations.
- **A content administrator's read** now asks the rules, which every
  other caller already pays, and with a reason adds one row written
  through the writer. It is measured in the step that builds this
  record, beside the same read by an ordinary caller, and the numbers
  are quoted in its pull request and added here.
- **A SQL statement** adds one row written through the writer. A
  statement takes milliseconds and the row is small; it is measured in
  the same step.
- No budget of ADR-0012 changes.

### Measured on 2026-11-17

On disk, on the machine every campaign of this project runs on, three
runs each, with the same file of benchmarks run on main, where nothing
is written down, as the control
(`internal/store/reads_bench_test.go`; logs under
`~/.cache/fadodb-bench/v0.7.0/adr-0067/`):

| One call | Main | With the log | What the entry costs |
|---|---|---|---|
| Ewa reads the review she manages, `BenchmarkReadByRule` | 6.75, 6.72, 6.69 µs | 6.75, 7.11, 6.78 µs | nothing: 95 allocations either way |
| Olga reads it past the rule, with a reason, `BenchmarkReadPastRule` | 6.59, 6.63, 6.52 µs | 36.9, 37.1, 36.8 µs | about 30 µs and 42 allocations |
| Marek's statement, `BenchmarkSQLStatement` | 8.83, 8.86, 8.87 µs | 40.8, 38.8, 38.5 µs | about 30 µs and 38 allocations |

The 30 µs are the small transaction the entry is written in. The full
comparison of the gate (`make benchgate`, five placements of five
rounds) holds every budgeted benchmark to main: no allocation more,
and the time of all of them together +0.05%.
