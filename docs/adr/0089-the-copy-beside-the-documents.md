# ADR-0089: The copy beside the documents: how the analytical engine is fed and asked

- Status: Proposed; amends ADR-0017 (who feeds the copy, what it holds, how fresh an answer is, how it is made again), ADR-0020 (item 3: which question the copy answers)
- Date: 2026-11-25

## Context

ADR-0017 makes DuckDB the analytical engine of the full build. It says
the full build "keeps a projection of every document table fed
append-only from the history and event tables", and that this copy
"lags the transactional store by its feed delay". ADR-0075 (item 1)
sends there every question that reads everything, and says that
neither a designer nor an agent chooses.

Neither record says who feeds the copy, how old an answer may be, what
the copy holds, or what happens when it is lost. Phase 8 builds it, so
this record says it. It was measured first.

**The case.** Acme has 3,400,000 invoices. Each was made as a draft,
most were filed for review and most of those were paid: ten million
visits of a state.

- Ewa, in finance, asks how much is invoiced by customer. That reads
  every invoice.
- Tomek, who owns the process, asks where invoices wait, and how long.
  That reads every visit of every state.

**Measured on 2026-11-25**, on the machine of ADR-0035 and its disk. The
database was made through the engine: 3,400,000 invoices, 9,965,400
versions, 7.4 GB. Each question was asked five times, and three times
of the store, whose answers take longer; the number is the middle one.
The whole run was made three times, and the three agree within 5
percent. The three ways agree on their answers.

| The question | The store, as today | DuckDB reading the store's file | DuckDB over a copy the engine keeps |
|---|---|---|---|
| invoiced by customer | 1.35 s | 0.076 s | 0.006 s |
| invoiced by owner and month | 1.46 s | 0.068 s | 0.005 s |
| how long invoices wait in each state | 11.4 s; 26.1 s through the view `Invoice_states` | 0.54 s | 0.36 s |
| which state follows which, with the middle wait and the slow tenth | no function for those two; without them 12.2 s | 0.64 s | 0.47 s |

What the copy costs, with the engine's process reading the database
and handing the rows to DuckDB:

| | |
|---|---|
| making it, once | 38 s for the ten million versions, 261,000 a second |
| its size | 0.38 GB, a twentieth of the database |
| bringing it up to date after 10 new versions | 17 ms |
| after 1,000 new versions | 28 ms |
| after 100,000 new versions | 0.73 s |

Two findings decide what follows.

1. **The copy is worth keeping.** The questions of the case take the
   store 1.4 to 26 seconds, and the copy 0.006 to 0.47.
2. **DuckDB may not read the store's file from inside the engine.** It
   would be the simplest way, with nothing to copy, and it is fast. But
   DuckDB reads such a file with a SQLite library of its own, and the
   engine has one already. Two copies of that library on one file in
   one program is a case of SQLite's own page on how a database is
   corrupted (sqlite.org/howtocorrupt.html, section 2.3): the locks on
   a file belong to the whole program, and when one library closes the
   file, the locks of the other are gone. From another program it is
   safe, and that is how an analyst reads a database (item 10).

## Decision

1. **The engine feeds the copy itself.** In the full build the engine
   keeps a DuckDB database beside the documents: the copy. The engine
   reads what was committed with the library that writes the database,
   and hands the rows to DuckDB. DuckDB never opens the store's file.

2. **What the copy holds**, for every class, and nobody chooses which:

   | In the copy | For |
   |---|---|
   | every version of every document, with the columns of its class, who made it and when | how documents moved, and what they held at a past moment |
   | the documents as they are now | totals over a whole class |
   | the events | what happened, by whom and how |

   Not in the copy: reference tables, until the step that gives DuckDB
   tables of its own; users, groups, keys and sessions; definitions;
   secrets; attached files; the log of reads.

3. **The copy is never the truth.** The database is. Nothing is kept
   only in the copy, so it needs no backup and no care. The engine
   makes it again, by itself, when it is lost, when it does not open,
   or when it does not belong to the database it stands beside, such
   as after the database was put back from a backup. While the copy is
   being made, a question it would answer is answered by the store.

4. **The copy knows how far it was fed.** It keeps a mark: the last
   event it holds. Feeding it is one read of the database, which sees
   one moment of it, and one transaction of the copy, which takes the
   rows and the new mark together or nothing. A program stopped in the
   middle leaves the copy as it was, and the next feed starts from the
   mark. When the engine opens, it asks the database for the event the
   mark names; when the database does not have it, the copy is made
   again.

5. **An answer is never older than its question.** Before a question
   is answered from the copy, the engine feeds the copy everything
   committed since the mark. Ewa approves invoice FV-2041 and asks for
   the total a second later: FV-2041 is in it. Between questions the
   engine also feeds the copy on its own, so that a question after a
   long load does not wait for all of it. This is question 1 for the
   owner.

6. **Which question the copy answers.**

   | The question | Answered by |
   |---|---|
   | names what it wants: a document, the lines of a document, the invoices of an order, a `LOOKUP` by its key | the store, by an index, as today |
   | anything inside a commit | the store, always (ADR-0020, item 4) |
   | how documents moved: which state follows which, how long they wait | the copy |
   | a total over a whole class: counts, sums and groups among the columns of a `QUERY`, with no key the store has an index for | the copy |

   No formula is written differently for it. The compiler sends each
   question where it belongs, as ADR-0075 says.

7. **The reader's rights hold in the copy.** The read rule of a class
   and the rights on its variables are compiled into the statement for
   DuckDB as they are into the statement for the store; the compiler
   writes both dialects already (ADR-0016). A reader gets from the copy
   what the store would give them, and the tests ask both the same
   questions as the same people.

8. **When a class changes**, the copy follows. A new version that adds
   a variable adds its column to the copy. A class that had no document
   and was made again has its copy made again. A variable that was
   taken out keeps its column, as in the store.

9. **The small build has no copy.** It is pure Go and has no DuckDB
   (ADR-0017). It answers every question by the store: the same
   formulas, the same answers, in the times of today. Nothing is
   refused for the lack of DuckDB but a table that says it is DuckDB's.

10. **An analyst's own program reads the database itself.** DuckDB,
    Python and R open the file of a database with DuckDB's SQLite
    scanner, from a program of their own: the middle column of the
    table above. No read rule holds there: whoever has the file reads
    all of it. The page that says how is the last step of the phase.

11. **The core gains nothing.** The core says what it asks of an
    analytical engine, as an interface in pure Go, and has none of its
    own. The module `analytics/` gives one, made with DuckDB. The full
    build is a program built in that module. The gates that hold the
    core's dependencies and the size of the small build stay as they
    are.

12. **The steps**, each a pull request:

    1. the copy of a class and its feed, with the mark, and the copy
       made again when it is lost;
    2. the questions of how documents moved, with the test of ADR-0017:
       under 2 seconds over ten million visits;
    3. counts, sums and groups among the columns of a `QUERY`, sent to
       the copy;
    4. a value that follows a table or a schedule, for many documents
       in one statement (ADR-0078, item 6);
    5. tables that DuckDB holds or attaches;
    6. the full build for each system, and the page for analysts.

## Questions for the owner

1. **How fresh is an answer from the copy?** (item 5)

   The case: Ewa approves invoice FV-2041 and, a second later, looks at
   the total invoiced by customer.

   - **Proposed: the copy is brought up to date before each answer.**
     FV-2041 is in the total. Each such question costs 17 to 28 ms
     more when little was written since the last one.
   - **The other way: the copy follows by itself, every few seconds.**
     The answer comes 17 ms sooner and may miss the last seconds; it
     says as of which moment it is. FV-2041 may be missing when she
     looks. This is the "feed delay" ADR-0017 speaks of.

## What is built

- **Step 1, the copy and its feed, on 2026-11-25.** A program gives
  the database an analytical engine when it opens it, in the option
  `Analytics`; the module `analytics/` has the one made with DuckDB,
  `analytics.Engine`. The database then keeps the copy in the file
  `<database>.duckdb` and feeds it itself: for every class the
  documents as they are now, in a table of the class's name, and every
  version of them, in `<Class>_versions`; and the events. The copy is
  found again when the database is opened again, and made again when
  it is lost, does not open, or names an event the database does not
  have. `CopyState` says how it stands, and `FeedCopy` feeds it at
  once.
- **Step 2, how documents moved, on 2026-11-25.** The flow of a class,
  the command `flow`, is answered from the copy when the program keeps
  one: how many documents the reader may read are in each state, since
  when the oldest, how many stays ended in the period and how long
  they lasted, and the moves along each transition. The copy is
  brought up to the database before it answers (item 5), and the
  reader's rule is written into its statements by the compiler, in
  DuckDB's dialect (item 7). Measured on the database of the Context,
  3,400,000 invoices and ten million visits, three runs: 18.6 seconds
  from the store, 0.33 from the copy, the same answer. The budget of
  ADR-0017 is 2 seconds.
- **Every other question is still the store's.** Step 3 sends the
  totals of a `QUERY` to the copy. The program `fadodb` is built
  without the module, so a server keeps no copy.

## Settled while building the copy and its feed

Each of these is settled by me while building, and is the owner's to
strike.

- **The feed goes by the place of a row in the store's table, and not
  by the id of an event.** The id of an event begins with its moment.
  After a clock was set back, a new event sorts before an older one,
  and a feed that asked for the events after the last id would never
  see it. A test found this before anything read the copy.
- **A mark is the place of the last row the copy holds and the id of
  its event.** The place says where the next feed begins. The id tells
  a database that was put back from a backup, and a class that was
  made again, from the one the copy was fed: the copy of that class,
  or the whole copy, is then made again.
- **A long load is copied in pieces**, of at most 1,000,000 versions
  of a class and as many events, each kept when it is done. No reading
  of the database is held for the whole of a long load, which would
  keep the store from folding its latest commits into the database
  meanwhile.
- **The documents as they are now follow piece by piece**: the ones a
  piece changed are taken out of the copy and put back as they are.
  They are read whole, with the last piece, when the copy holds none
  of the class yet, and when most of them changed: more than 100,000
  new versions, and more than a quarter of the documents the class
  holds.
- **The feed never counts what is left to copy.** A version is never
  taken out of a history, so the place of the last row says how many
  are new. A first run on a hundred million versions showed that
  counting them read the table again at every piece.
- **Opening a database does not wait for its copy.** The first feed
  runs beside the program, and so does the one every `CopyEvery`, five
  seconds unless the program says otherwise.
- **Closing feeds the copy once more, for at most five seconds.** What
  is left is fed at the next opening.
- **A copy that cannot be opened or fed stops nothing.** The database
  works without it, and `CopyState` says why. A commit never waits
  for the copy and never fails for it.
- **One opening feeds a copy.** DuckDB lets one program write a file;
  inside one program the engine gives the copy to the first opening,
  and a second works without one.
- **A column of the copy has the type the formula compiler writes its
  statements for** in DuckDB's dialect (ADR-0016): a text, a whole
  number for an exact value's mantissa and for a moment's seconds, a
  number with a fraction, a true or a false, and a day for a date. So
  the reader's rule, written by the compiler, runs on the copy as it
  is.
- **When a version ended is not copied.** The store writes that into
  the row of a version later, and the copy only ever takes rows; the
  next version of the document says it.
- **The lines of a document and its arrays are not in the copy**, as
  this record leaves out. The fields of an object are columns, as in
  the store.
- **Two calls for the program that opened the database, and no
  command**: `FeedCopy` and `CopyState`. A caller of a server never
  sees the copy.

## Settled while building the flow from the copy

Each of these is settled by me while building, and is the owner's to
strike.

- **No new command and no new view.** The record's step 2 spoke of
  "the questions of how documents moved". The engine has that question
  already: `flow`, which a face draws its flowchart from. It is the
  first question the copy answers, and its answer does not change.
- **When the copy does not answer, the store does.** The feed failed,
  the copy cannot run the statement, or the program keeps none: the
  caller gets the flow from the store, in the store's time, and
  `CopyState` keeps why. A question is never refused for the copy.
- **The copy is asked in DuckDB's dialect, by name.** The engine's own
  statements are written for the copy by the compiler that writes them
  for the store. DuckDB takes the values of a statement by name or by
  place and not both, so the copy's statements name every value.
- **The copy has the functions the compiler's statements call**: they
  are made in it when it is opened.
- **The whole copy takes longer to make than the Context says.** The
  38 seconds and 0.38 GB there were of the versions and the documents
  alone. With the events, the engine makes the copy of that database
  in 84 seconds, in ten pieces, and it is 0.54 GB, a fourteenth of the
  database. Found again at the next opening, it is checked in 21 ms.
- **Not built:** the middle wait and the slow tenth, the paths
  documents took and the documents made and finished in each period,
  which the roadmap calls the mining views. Each is a question the
  copy can answer when a face asks for it.

## Consequences

- A question that reads everything is answered in the time of an
  engine built for it, and nobody learns a second language for it.
- The full build has a second file beside the database, a twentieth of
  its size, which nobody backs up.
- The first question after a long load waits for the copy to catch up:
  0.73 seconds for 100,000 versions, 38 seconds when the copy is made
  from nothing for ten million.
- The feed reads the database as any reader does, and a reader slows
  the writer. Measured with the first step, on 2026-11-25: a thousand
  moves in one call take 39.8 ms in a database that keeps no copy and
  41.9 ms beside a copy that is fed ten times a second, 5% more
  (`BenchmarkCommitsBesideTheFeed`, three runs of each, within 1%).
  It is not measured at the period of five seconds a copy is fed at
  unless the program says otherwise.
- Two programs are built and released, as ADR-0017 says. The one with
  DuckDB is about 100 MB; the one of today is 16 MB.

## What this record leaves out

- **Lines.** The measured invoices have no lines. Whether the copy
  holds the lines of every version or the lines documents hold now is
  decided with the step that sends a total over lines to the copy.
- **A flow under 2 seconds over a hundred million visits.** Measured
  with step 2, on 2026-11-25, in a database of 34,000,000 invoices
  that visited a hundred million states, 75.4 GB. The copy is made in
  29 minutes, in a hundred pieces, and is 5.4 GB. The flow takes 7.2
  seconds from the copy, the middle of five askings between 7.0 and
  8.2, and 7 minutes 40 seconds from the store; the two answers are
  the same. The copy was made once and the store asked once, on a
  machine that ran tests beside them, so the numbers say seconds
  against minutes and no more. Ten times the visits took the copy 22
  times as long: its time grows faster than the class, and at this
  size it is past the 2 seconds ADR-0017 sets for ten million. Three
  hundred million visits are not measured.
- **The statement of an analyst**, the command `sql`. It stays with the
  store, in the store's dialect.
- **A copy on another machine.**
- **The Postgres store** of Phase 10, where the analytical engine may
  read the store in another way.

## Performance

| Budget | The benchmark that proves it |
|---|---|
| which state follows which, with the middle wait and the slow tenth, over 10,000,000 visits: under 2 s (ADR-0017) | with step 2, in the module `analytics/` |
| bringing the copy up to date after up to 1,000 new versions: under 50 ms | `BenchmarkFeed` in the module `analytics/`: 31.7 ms, measured with step 1 on 2026-11-25, in a class of 20,000 documents |
| a commit is no slower for the copy, and allocates nothing more: the feed is not on the path of a commit | the benchmark gate, with step 1 |
| the small build is no larger (ADR-0072) | the size gate |
