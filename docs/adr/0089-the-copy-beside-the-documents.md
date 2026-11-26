# ADR-0089: The copy beside the documents: how the analytical engine is fed and asked

- Status: Proposed; amends ADR-0017 (who feeds the copy, what it holds, how fresh an answer is, how it is made again), ADR-0020 (item 3: which question the copy answers); amended by ADR-0094 (Proposed: the copy holds the rows of the tables too)
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
   | the moves of the documents that exist: each time one was made or moved along a transition, with the moment, and for a move the moment the state it left was entered | how documents moved, as one sum over one table |

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
   | a count or a total that no index narrows: the command `count`, and the total or the first row that a `LOOKUP` or a figure over a list asks for when it is read | the copy |
   | a page of a list that the store does not find in a moment: the copy says which documents, in which order, and the store hands them over | the copy |
   | groups with their totals among the columns of a `QUERY` (ADR-0090), when no index narrows what they count | the copy |

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
    3. what a class asks, sent to the copy when no index narrows it:
       first what it already asks, its counts and totals and then the
       pages of its lists; then counts, sums and groups among the
       columns of a `QUERY`;
    4. a value that follows a table or a schedule, for many documents
       in one statement (ADR-0078, item 6);
    5. tables that DuckDB holds or attaches;
    6. the full build for each system, and the page for analysts.

    Step 3 gained its first two parts on 2026-11-25, when the owner
    said what comes first: what a class already asks, its lookups and
    its lists, takes milliseconds over a large class, and so does the
    question of how documents moved. The first writing of this record
    went from the flow straight to a new way of writing totals, and
    nothing a class already asked was any faster for it.

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
- **Step 2, again: the moves kept ready, on 2026-11-25.** The copy
  keeps the moves of the documents that exist, and the flow is one sum
  over them: 19 ms over the same ten million moves, for a reader who
  reads every document. An existing copy is given its moves in 47
  seconds, once, and is 0.62 GB where it was 0.54.
- **The first half of step 6, the program with the engine, on
  2026-11-25**, ahead of steps 3 to 5: the copy answers nothing for a
  person until a server has it. The code of the program `fadodb` is
  the package `internal/cli`; `cmd/fadodb` builds it in pure Go, as
  before, and `analytics/cmd/fadodb` builds the same program with
  DuckDB. `make full` makes it, as `bin/fadodb-full`. A database it
  serves keeps its copy, and the settings of the server say how much
  memory the engine may take. Not built then: the program for each
  operating system, released, which came on 2026-11-26 with the names
  (see "The names" below); an image for containers with the engine,
  which came the same day; the page for analysts, which came that day
  too, as the manual's chapter "Reading a database with DuckDB".
- **Step 3, its first part: the counts and totals a class already
  asks, on 2026-11-25.** The command `count`, and the total or the
  first row that a `LOOKUP` in a live variable or a figure over a list
  asks for, are answered from the copy when no index narrows them.
  Measured through the engine among 3,400,000 invoices, for a reader
  who reads them all, three runs that agree: the sum of every invoice
  in a `LOOKUP` takes a program without the copy 460 ms and one with
  it 7; a count over an amount 275 ms and 2; a count by a variable
  with no index 240 ms and 1.5; the number of different owners 285 ms
  and 1.5. A count by the state, which has an index, stays with the
  store, in 4 ms, and so does the sum of the 170,000 invoices in
  review that a figure over a list asks for, in 36 ms.
- **Step 3, its second part: the pages of lists, on 2026-11-25.** A
  page of the command `query` or of a live list is found in the copy
  when the store would read and sort the class for it, or has not
  found it in five milliseconds; its documents are then read from the
  store. Measured through the engine among 3,400,000 invoices, three
  runs that agree: the fifty dearest invoices take a program without
  the copy 318 ms and one with it 8; a live list of the fifty dearest
  417 ms and 14; the fifty dearest of one owner, whom an index narrows
  to a million invoices, 278 ms and 11. Fifty invoices over an amount,
  as they come, and the newest fifty, which the store hands over at
  once, stay with the store, in under a millisecond.
- **Step 3, its third part: groups with their totals, on 2026-11-26.**
  A live variable whose `QUERY` has a total among its columns gives
  one line for each group (ADR-0090, proposed). It is answered by the
  copy when no index narrows what it counts, and by the store
  otherwise, with the same lines. Measured through the engine among 3,400,000 invoices, three
  runs that agree: one line for each customer takes a program without
  the copy 675 ms and one with it 12; a list by owner and state, the
  largest sum first, 3.4 seconds and 15 ms; one line of the whole
  class 980 ms and 10; the invoices in review by customer, which the
  index of the state narrows to 170,000, 72 ms and 13.
- **Measured at ten times the size, on 2026-11-26**, with all of the
  above: 34,000,000 invoices that made a hundred million moves, a
  database of 75 GB, whose copy is 6.1 GB. One run, the copy asked
  five times and the store once. The flow takes 158 ms from the copy
  and 7 minutes 40 seconds from the store; before the moves were kept
  ready it took the copy 7.2 seconds. The sum of every invoice in a
  `LOOKUP` takes 47 ms where the store takes 4.5 seconds; a count by a
  variable with no index 6 ms and 2.4 seconds; the count of the paid
  invoices, most of the class, 12 ms and 16.6 seconds; the fifty
  dearest invoices 59 ms and 3 seconds; one line for each customer 76
  ms and 110 seconds. A page the store fills at once stays with the
  store, under a millisecond.

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

## Settled while building the program with the engine

Each of these is settled by me while building, and is the owner's to
strike.

- **One program, built twice.** The program with the engine has every
  command, flag and setting of the other, because it is the same
  code. Only what opens a served database differs: it is given the
  engine.
- **The names.** ADR-0017 calls the program with DuckDB `fadodb` and
  the one in pure Go `fadodb-lite`. Today `fadodb` is the program in
  pure Go everywhere: the releases, the image, the manual. So the new
  one is built as `fadodb-full`, and nothing that exists is renamed.
  Which program keeps the name `fadodb` is the owner's to say.

  The owner said it on 2026-11-25: the program with DuckDB is
  `fadodb`, and the one in pure Go is `fadodb-core`. The names move
  with step 6, when the program with DuckDB is built and released for
  each system: on the day `fadodb` means the program with the engine,
  every release has one. Until then the two are built as
  `fadodb-full` and `fadodb`.

  **The names moved on 2026-11-26**, with the builds: `cmd/fadodb-core`
  is the program in pure Go, `make build` makes it as
  `bin/fadodb-core`, and `version` begins with that name;
  `analytics/cmd/fadodb` is the program with the engine, which
  `make full` makes as `bin/fadodb`. `fadodb` is built and tried, as
  an operator runs it, on a machine of each of the five systems DuckDB
  is built for, on every pull request, and a release is made only when
  every one of them is there, attached beside `fadodb-core` under one
  list of checksums. Windows on ARM has `fadodb-core` alone. The
  owner's word of that day: both programs are offered for every
  system, with advice on up to what size `fadodb-core` is enough,
  which the README and the manual carry with its measurements.
  Settled by me while building, the owner's to strike: the Linux
  programs are built on Ubuntu 22.04, so that they run on Debian 12,
  Ubuntu 22.04 and what is as new, and not on the Red Hat 9 family or
  Alpine, where `fadodb-core` runs; `fadodb-core` writes `fadodb` in
  its help, as the manual does, and says at its end which program it
  is; `go install` gives `fadodb-core`, since Go does not install a
  module that names the repository beside it.

  **The image followed the same day.** By the owner's words, "the
  full one is just FadoDB" and "we will make both available", the
  image under the plain name, `fadodb:<version>`, holds the program
  with the engine, and `fadodb:<version>-core` holds the other. The
  image of `fadodb` stands on Google's distroless base of Debian 12,
  which has the C and C++ libraries DuckDB needs and no shell; it is
  about 118 MB where the small one is 17. Settled by me while
  building, the owner's to strike: one Dockerfile builds both, on the
  machine that builds and with a compiler for the other kind of
  machine, so nothing is emulated; the chapter of the manual is played
  against the image with the engine, and the small one is held to
  what the chapter says of it; before a release publishes them, the
  chapter is played on an Intel machine and on an ARM one.
- **The engine of a server takes at most 2 GB of memory** unless its
  settings say otherwise, `analytics.memory`. DuckDB's own limit is
  four fifths of the machine, which a server shares with its database
  and with everything else on it.
- **Settings that name the engine are refused by the program built
  without it**, when it starts: it could not do what they ask.
- **Only a served database gets the engine.** The commands of the
  program that open a database for a moment, such as `fadodb user
  add`, keep no copy.

## Settled while building the moves the copy keeps ready

Each of these is settled by me while building, and is the owner's to
strike.

- **The flow was not in milliseconds, and the copy now keeps what
  makes it so.** As first built, the flow joined the events with the
  versions each time it was asked, ten million rows with ten million:
  a third of a second on the day it was built, and half a second when
  it was measured again beside this. The copy now keeps the moves
  ready, in a table `<Class>_moves` for each class, and the flow is
  one sum over it: 19 ms over the same ten million moves, measured
  through the engine in three runs that agree, where the store takes
  18.2 seconds. The answer is the same.
- **When the state a move left was entered is read while the copy is
  fed**, from the version the event left, which the store finds by its
  key inside the one statement that reads the new events. Nothing is
  read row by row from outside the store.
- **A deleted document has no moves in the copy.** The flow is of the
  documents that exist, as the store answers it, and the id of a
  deleted document is never given again. So a reader who reads every
  document is asked for no more than the sum; the reader's documents
  are named only for a reader whose rule leaves some out. Naming them
  costs about a tenth of a second when the rule picks millions of
  documents, measured on the statement alone.
- **A copy that was fed before it kept the moves is given them**: it
  carries a mark of what it holds, and a copy with another mark has
  its events read again from the first, once. Its versions and its
  documents stay.

## Settled while building the pages of lists from the copy

Each of these is settled by me while building, and is the owner's to
strike.

- **The store is given a moment first: five milliseconds.** A first
  build sent every page that no index serves to the copy. Measured, a
  page the store fills at once, the first fifty invoices over an
  amount, took the store 0.3 ms and the copy 4; and a list that an
  index narrows to a million invoices and then sorts stayed with the
  store, at 296 ms. So the rule looks at what the store does and not
  at how the question is written: the store finds the page in the
  moment, or the copy is asked. Whichever finds it, the page is the
  same.
- **A page that needs the class read and sorted is the copy's at
  once**: no index serves its condition, and the first thing it is
  sorted by has no index. The store has no way to hand that over in a
  moment, and the moment would be lost.
- **The copy says which documents, and the store hands them over.**
  The copy is asked for the ids of the page alone. The documents are
  read from the store by those ids, with their lists and under the
  question's own condition still, so a document changed since the
  copy looked is never shown where it no longer belongs, and what the
  reader may see of each document is decided by the code that decides
  it for every read.
- **A blank sorts where it sorts in the store**: before every value.
  DuckDB's own way is the other, so the order is written out for it.
- **The moment is spent only in a program that keeps the copy.** A
  program without one asks the store as before, and a question no
  index serves is still stopped after five seconds there.

## Settled while building the counts and totals from the copy

Each of these is settled by me while building, and is the owner's to
strike.

- **Which question goes to the copy: one that no index narrows.** The
  filter compiler knows it already: the condition, or the read rule
  for that reader, compares an indexed variable with a value, or it
  does not. A `LOOKUP` goes by its key. Nothing looks at how many
  documents there are.
- **A count or a total that an index narrows is the store's for a
  moment**, the five milliseconds a page has, and the copy's when the
  moment has passed. An index narrows a question to the invoices of
  one order, or to the documents of one state, which may be millions;
  how the question is written does not say which. As first built such
  a count stayed with the store whatever it counted. Measured through
  the engine among 3,400,000 invoices, three runs that agree: the
  count of the paid invoices, most of the class, takes 8 ms where the
  store took 82; the sum of the 170,000 invoices in review that a
  figure over a list asks for, 10 ms where it took 34; the count of
  the drafts, a few, stays with the store, under a millisecond. A
  count the store answers in about the moment, 4 ms, goes one way or
  the other, and takes 4 to 5 ms either way.
- **A question never waits for a copy that is being made.** While the
  copy is being made, or is more than one piece of a long load behind,
  the store answers, and `CopyState` says so. This holds for the flow
  too, which waited before.
- **One statement, two dialects.** The copy is asked the statement the
  store is asked, written by the same compiler for DuckDB, and its one
  row is read by the same code. Nothing is written twice by hand.
- **A value that is kept is the store's.** A triggered variable is
  worked out inside the commit that changes it (item 6), and the copy
  does not hold that commit yet.
- **What a content administrator is written down with**, how many
  documents only the override let them count (ADR-0067), is counted
  by the store, whichever engine answered.

## Settled while writing the page for analysts

Item 10 says that an analyst's own program reads the database itself,
and that a page says how. The page is the manual's chapter "Reading a
database with DuckDB", of 2026-11-26. Each of these is settled by me
while writing it, and is the owner's to strike.

- **The page is played by a test, with DuckDB's own extension.** The
  extension for SQLite files is not in the library the program is
  built with; an analyst fetches it once, and so does the test, on
  GitHub, where `FADODB_DUCKDB_SQLITE` is `must`: the chapter is
  played there, or the check fails and says the extension could not
  be had. On another machine the test loads the extension when it is
  there and is skipped when it is not, saying that the chapter was
  not played. No program of the project loads the extension.
- **The test closes the database before DuckDB opens it.** DuckDB
  brings its own SQLite, and two in one program that hold one file is
  what item 2 of the Context forbids. That DuckDB reads beside a
  running server was tried by hand, with two programs.
- **The views are what the page tells an analyst to ask.** DuckDB shows
  the tables behind them and the engine's own as well, the users and
  the records of sign-ins among them; the page says so, and that a
  release may lay those out otherwise.
- **The programs for Python and R are written and not run.** Neither
  package was on the machine the page was written on.
- **What DuckDB needs beside the file** was found by trying: it makes
  SQLite's two small files to read a database nobody has open, is
  refused where it may not write in the directory, and the engine
  clears the two files when it next closes the database.

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
- **Three hundred million visits.** A hundred million are measured:
  see "What is built". Three times that is not.
- **The statement of an analyst**, the command `sql`. It stays with the
  store, in the store's dialect.
- **A copy on another machine.**
- **The Postgres store** of Phase 10, where the analytical engine may
  read the store in another way.

## Performance

| Budget | The benchmark that proves it |
|---|---|
| which state follows which, with the middle wait and the slow tenth, over 10,000,000 visits: under 2 s (ADR-0017) | with step 2, in the module `analytics/`; the flow, which has neither the middle wait nor the slow tenth, takes 19 ms over 10,000,000 moves, measured by hand on 2026-11-25 |
| bringing the copy up to date after up to 1,000 new versions: under 50 ms | `BenchmarkFeed` in the module `analytics/`: 31.7 ms, measured with step 1 on 2026-11-25, in a class of 20,000 documents; 28.5 ms with the moves kept, where it took 24.7 the same hour without them |
| a commit is no slower for the copy, and allocates nothing more: the feed is not on the path of a commit | the benchmark gate, with step 1 |
| the small build is no larger (ADR-0072) | the size gate |
