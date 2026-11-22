# ADR-0075: The engine makes its own indexes, and says which question it cannot serve

- Status: Proposed; amends ADR-0013 (what `index` on a variable means), ADR-0014 (the indexes of a document table), ADR-0038 (a filter that no index serves), ADR-0046 (item 10: the index of a key is the engine's to make)
- Date: 2026-11-20

## Context

On 2026-11-20 the owner asked for a list on a document: an order shows
its invoices, a click on one shows its details. Looking at what the
engine gives a face for that, I told him that one way of asking would be
slow "with hundreds of thousands" of invoices. I had not measured it.
His answer: "This would be a catastrophe. We have to support hundreds
of millions of rows and more, and not be worried. We need an
architectural solution for that. One which will differentiate FadoDB
from other databases." He named two ways: the engine checks for an
index whenever a `QUERY` or a `LOOKUP` is defined, or DuckDB.

So it was measured the same day, on the machine of ADR-0035, on its
disk, through the Go API. A class `Invoice` with five invoices to an
order; Ewa, in finance, may read every invoice. Three runs of each
question; the table gives where they lay.

| Invoices in the class | One order's invoices, through an index | The same, past the index | The newest 50 of the class | The count in one state |
|---|---|---|---|---|
| 100,000 | 0.06 to 0.08 ms | 7 to 10 ms | 55 ms | 5 ms |
| 1,000,000 | 0.06 to 0.07 ms | 73 to 79 ms | 0.56 s | 58 ms |
| 10,000,000 | 0.05 to 0.08 ms | 0.74 to 0.82 s | 5.7 s | 0.56 s |
| 100,000,000 | 0.3 to 0.9 ms | 2.5 min to over 4 min | over 4 min | 2 to 2.6 min |

At a hundred million the file is 92 GB and the run was held to 12 GB
of memory, so what it reads comes from the disk. A question was stopped
when it had run four minutes.

Two things are in the table.

- **A question that goes through an index stays under a thousandth of
  a second at every size.** It takes 0.07 ms while the file fits in
  memory, and 0.3 to 0.9 ms when the file is eight times the memory
  and each answer costs a few reads from the disk. That is what an
  index is, and it is why a hundred million documents are no worry for
  such a question.
- **A question that goes past its index takes ten times as long for
  ten times the documents, and far longer once the file no longer fits
  in memory: minutes.** Three of them were found that day, in what a
  face asks most:
  - the invoices of one order, where the invoice names its order by a
    `ref`: a formula can write nothing a `ref` compares with, so the
    filter was written `TEXT(order) = "Order:ORD-17"`, and a function
    around a variable keeps the index out (ADR-0038, item 3);
  - the newest documents of a class, which is what The List of a face
    shows first: nothing indexes the moment a document was made;
  - the count of the documents in a state: nothing indexes the state.

A fourth finding is about what the engine says. A class whose formula
is `LOOKUP(Invoice, COUNT(id), order_id = ID())` is taken without a
word when `order_id` has no index, though ADR-0046, item 10, knows that
"without an index the statement reads every document of the class".
The index is the designer's to remember: `"index": true`.

The hundred million were then asked again with indexes added to the
test file by hand: first one on the moment a document was made and one
on the state, then a third. The read rule of the class is `owner =
USER() OR INGROUP("finance")`, and `owner` had its index from the
start. Jan wrote all the invoices but 100,000, which are Piotr's.

| The newest 50, among 100,000,000 | As the class was | With the two indexes | And with one on `owner`, then the moment made |
|---|---|---|---|
| Ewa, who may read all | over 4 min | 0.2 to 0.3 ms | 0.2 to 0.3 ms |
| Piotr, who owns 100,000 | about 70 ms | about 70 ms | 0.3 ms |
| Jan, who owns the rest | 3 to 4 min | 3 min 40 s to over 4 min | 0.3 ms |

The count in one state came down from two minutes to 0.2 ms for Ewa,
and from over four minutes to 3 ms for Jan.

Jan's row is the fifth finding. The store took the index on `owner`,
found his 99.9 million invoices and sorted them to answer fifty. An
index on a variable alone answers "which are mine". It does not answer
"my newest", which is what a person asks of a list. An index on the
variable and then on the moment a document was made answers both.

The sixth finding came from the new indexes themselves. With the index
on the state in place, a question that had taken under a thousandth of
a second took seven to ten seconds: the drafts of one order, `order_id
= "ORD-17" AND STATE() = "draft"`. Two indexes fit it. The one on
`order_id` narrows to five invoices, the one on the state to 99.99
million. The store has no numbers on the data, holds each index for as
good as the other, and took the one on the state. Told which to take,
it answered in 0.2 to 0.6 ms. The store's own sampled count of the data
(`ANALYZE` under a limit) took four and a half minutes on the hundred
million and changed nothing: it gave the order, the owner and the state
the same number, 1,001 documents to a value.

So an index added to make one question fast made another slow. ADR-0049
had seen the cause: "the store runs no `ANALYZE`, so SQLite chooses
between two indexes blind". In a database whose questions come from
outside, a person finds such a case in production. The engine knows
what the store has to guess: the state of `Invoice` has three values,
the pointer of an order names a few invoices, and a declared question
was given its index for a reason.

That is how every database works, and it is where FadoDB can differ.
In a relational database the questions live in the code of an
application. The database first sees a question when it runs, and a
person adds an index after it was slow. In FadoDB the questions are
written in the class: every `LOOKUP`, every `QUERY`, every `ref`, the
rule that says who may read. The engine has read them all before the
first document exists.

On DuckDB two measurements stand. ADR-0035: as the place where
documents are written, a commit costs DuckDB 10.6 ms at the median
against 0.06 ms on SQLite, some 100 commits a second against 3,400 and
more. ADR-0036: one small lookup costs it about 220 µs whatever the
size of the table, against 2 to 4 µs. DuckDB is built for the other
kind of question, the one that reads everything: the total invoiced by
customer and month. ADR-0017 gave it that place, beside the documents
and fed from the commits, and the roadmap builds it in Phase 8.

The case is Acme with a hundred million invoices. Jan, in sales, opens
order ORD-17. Ewa, in finance, opens the list of invoices. Olga designs
the classes, and an agent of Acme's designs some of them too.

## Decision

1. **Two kinds of question, and the engine chooses the way.**

   | The question | Example | Answered by | Its time |
   |---|---|---|---|
   | names what it wants | the invoices of order ORD-17; Jan's newest fifty | an index, in the store that holds the documents | does not grow with the class |
   | reads everything | the total invoiced by customer and month | the analytical engine (ADR-0017), beside the documents | grows with the class, on an engine built for that |

   Neither a designer nor an agent chooses. They write a formula, and
   the compiler, which writes SQL for both engines already (ADR-0016),
   sends it where it belongs. The second row is Phase 8 and is not
   built; until it is, such a question is answered by the store, in a
   time that grows with the class, and item 5 says what the engine
   tells about it.

2. **The engine makes the index of every question a class declares**,
   in the transaction that takes the definition.

   | What the class says | The index the engine makes |
   |---|---|
   | a variable of type `ref` | on the variable: a pointer is there to be followed back |
   | the key of a `LOOKUP` or a `QUERY` over a class or a table: the first comparison of the condition, in its top-level `AND`, of a column for equality with something of the reading document (ADR-0046, item 10) | on that column of the source |
   | a text variable the read rule compares with the caller, `owner = USER()` (ADR-0049) | on the variable |
   | nothing: every class has them | on the state, on the moment a document was made, and on the moment it last changed |

   The last row is for what every face asks of every class: the newest
   documents, the newest in a state, how many wait in a state, what
   changed last.

   **Every index on a variable or on the state goes on to the moment
   the document was made.** The newest of whatever the index narrows
   to are then read off its end, and nothing is sorted: Jan's newest
   fifty, the newest fifty in review, the newest fifty invoices of a
   customer with ten million of them. That is the fifth finding of the
   Context, and it holds for an index a designer asks for with
   `"index": true` as well. The id of the document is not in the
   index. Measured with it: the newest fifty come in 0.2 ms where they
   otherwise take 4 to 9 ms when 45,000 documents were made in the
   same second, and the file is a fifth larger again.

   With Acme's classes: `Invoice` has `"order": {"type": "ref",
   "class": "Order"}`, so its table has an index on `order` from the
   day Olga defines it. When her agent later defines `Order` with a
   count of its invoices, the engine finds that the index is there.
   Nobody wrote `"index": true`, and nobody could forget it.

3. **`"index": true` stays, for a question the class does not
   declare.** A face lets people search invoices by `customer`. That
   question is in no class, so the engine cannot know it. The designer
   says it with `"index": true` on `customer`, as today.

4. **An index belongs to the questions that need it.** The engine keeps
   which definitions asked for an index. When a new version of a class
   no longer declares the question, and no other definition and no
   `"index": true` needs the index, it is dropped. A question in an
   older version of a class is no reason to keep one: only the current
   version is asked (ADR-0039).

5. **The engine says, when a class is defined, which declared question
   no index can serve.** It is a note, code `reads_all`, at the place
   of the formula, with the reason and the mend, as `no_description` is
   a note (ADR-0068). The class is taken: on a class of a thousand
   documents such a question is harmless.

   ```text
   note: reads_all at /variables/open_invoices/formula: this LOOKUP
     reads every document of Invoice each time it is worked out:
     LEFT(number, 2) = "FV" is no comparison of a variable with a value
     fix: begin the condition with a variable compared for equality,
     such as order = REF(), and the engine keeps an index for it
   ```

   A question that reads everything by its nature, a total over the
   whole class, gets the same note until Phase 8 gives it its engine.

6. **A free question that no index serves is stopped, and told what
   would serve it.** A free question is one a caller writes itself: the
   `where` and the `order` of `query` and `count`. The engine
   cannot have an index ready for it. It runs for at most the time the
   database allows a free question, a limit of the database as the
   text limit is (ADR-0021), 5 seconds when nobody set it. Past that it
   is stopped and refused with `too_wide`, and the hint names the
   variable an index would be needed on, read from the store's own plan
   of the statement. Ewa, searching a hundred million invoices by a
   customer nobody indexed, is told after five seconds: "customer has
   no index; say `"index": true` on it". The question of an analyst,
   `sql`, keeps its own limits (ADR-0038).

   The owner ruled so on 2026-11-22; it was question 1 of this record.

7. **An index on a class that already holds documents is built when
   the definition is taken**, and the commits of the database wait
   meanwhile, for the store has one writer (ADR-0035). Measured: on a
   hundred million invoices one index took between half a minute and
   six minutes, by how much of the file the memory held; on ten
   million, two to four seconds. `validate_class` and the check of a
   class from a terminal
   say before anything is written what a definition would build: "an
   index on `customer` over 100,000,000 documents". A designer chooses
   the hour.

8. **The layout of a database changes**: every class table gains the
   indexes of item 2, and the number of the engine's schema rises. A
   database made by an earlier version is refused by the release that
   builds this, as every database of an earlier schema has been before
   1.0 (ADR-0067). Were an older database brought over one day, the
   indexes would be built then, in the time item 7 names.

9. **An index is not free, so the engine makes only what a declared
   question needs.** Each one is written with every commit that
   changes its variable, and takes room. Measured at 2,000,000
   invoices, with the six indexes this class comes to, three on its
   variables and three of every class: loading is 8% slower, 45,300
   invoices a second against 49,000; a single commit takes 10 to 30 µs
   longer, a create 63 to 92 µs at the median against 55 to 64; the
   file is 8% larger.

10. **An index never makes another question slow** (the sixth
    finding). Two indexes may fit one question, and the store, which
    has no numbers on the data, takes either.

    - **A declared question is answered through the index made for
      it.** The statement the compiler writes names the index, and the
      store has no choice to make.
    - **A free question that two indexes fit is the open part.** The
      drafts of one order fit the index on the order and the index on
      the state. How the engine chooses is not decided in this record;
      three ways are known and none is measured far enough:

      | The way | What is known |
      |---|---|
      | the store counts the data itself (`ANALYZE`) | sampled, it took four and a half minutes on a hundred million and did not tell an order from a state; a full count was not measured; the numbers age as the class grows |
      | a rule from the class: the index on the state is taken only when nothing else fits | mends the measured case, 0.01 to 0.05 ms; wrong for a state that holds few documents beside a value that holds millions |
      | the engine looks into each index that fits, for at most a thousand documents, and takes the one that ends first | not measured |

      One page of analysis comes before any of it is built.
    - **The test is decided, whichever way is taken.** No index of
      item 2 is added to a class by a release whose gate does not ask,
      in a class of 1,000,000 with every index of item 2 in place, the
      drafts of one order, and get them in under a thousandth of a
      second. Today, with no index on the state, they take 0.1 ms
      among 2,000,000.

## Questions for the owner

None is open. Question 1, whether a free question that no index serves
on a large class is stopped or answered however long it takes, is
answered: item 6, and the rulings below.

## The owner's rulings

Of 2026-11-20, in his words: "We have to support hundreds of millions
of rows and more, and not be worried. We need an architectural solution
for that. One which will differentiate FadoDB from other databases."

To the question put the same day, "the engine makes its own indexes
and names what it cannot serve, with DuckDB for questions over
everything in Phase 8; the other way: the designer writes
`"index": true` as today and the engine only warns", he answered
"yes".

Of 2026-11-22, to the question as it was put to him that day, "a
search no index serves: stopped after five seconds with the mend named
(my proposal), or left to run": **"1. yes"**. That is item 6. The case:
Ewa types a customer's name into the search of a face, over a hundred
million invoices, and nobody indexed `customer`. She is refused after
five seconds, and the refusal names the mend, `"index": true` on
`customer`; a database with many such searches at once still answers
everybody else. The way not taken: the search is answered however long
it takes, two and a half minutes and more here. Nothing would be
refused that used to work; ten such searches at once would keep ten
readers busy for minutes each, and while they read, the log the
commits write to could not be emptied (ADR-0035).

Settled by me, his to veto: which questions count as declared, and the
four rows of item 2; that every index goes on to the moment a document
was made (item 2); that a declared question names its index, and the
test of item 10; that the engine's word on a question it cannot
serve is a note and no refusal (item 5); that an index is dropped when
nothing needs it (item 4); that an index is built with the definition
and the commits wait (item 7).

## Consequences

- A designer and an agent write what a document needs to know, and
  never tune the database. An agent that forgets an index cannot make a
  slow database, because there is nothing to forget.
- The list on a document, the reason this record was written, is a
  declared question once the class can write it. ADR-0076 gives the
  class the words: a value worked out when it is read, and something a
  `ref` compares with.
- Every class table gains three indexes, and the indexes of its
  variables grow by the moment a document was made. The file of the
  measured class was 8% larger for both, its loading 8% slower.
- A definition can now take time and hold the commits: the one that
  first asks a question of a class that is already full.
- A caller that sent free questions with no thought for an index gets
  `too_wide` on a large class, where before it got an answer late.
- An index is no longer something that can only help. The release
  that adds the first of them carries the test of item 10.

## What this record leaves out

- **The analytical engine**, DuckDB beside the documents, for the
  question that reads everything. Planned, Phase 8, not built.
- **An index on more than what item 2 names**: on two variables of a
  class together, or for a comparison with `<`. Not decided. The gate
  of this record's Performance is where a need for one shows.
- **An index for every variable of The List.** A variable that says
  `"list": true` (ADR-0068) is a column a person may sort by. Whether
  that alone is a declared question is not decided: each index costs
  every commit, and `"index": true` says it today.
- **How the engine chooses between two indexes that fit one free
  question** (item 10). Not decided; a page of analysis first. Open
  since ADR-0049, which found that the store chooses blind.
- **A count of very many documents.** A count reads what it counts:
  100,000,000 invoices took Ewa 0.2 s through the smallest index, and
  Jan's 99,900,000 took 2.5 s. It is a question that reads everything
  (item 1). What a face shows above a list of a hundred million is the
  face's to decide; not decided here.
- **Building an index beside the commits**, without making them wait.
  The store of this build cannot. Not planned before the Postgres store
  of Phase 10.
- **More than one database file for one company.** A hundred million
  invoices are 92 GB in one file, and 106 GB with the four indexes of
  the measurement. Splitting a class over files is not planned.

## Performance

The budget this record adds: **a declared question takes no longer on
1,000,000 documents than twice what it takes on 100,000.** A benchmark
in the `store` package asks one order's invoices at both sizes and
joins the gate (ADR-0012). The budget of ADR-0012 for an indexed filter
over 1,000,000 documents, under 10 ms, stands; the measurement above is
at 0.07 ms.

### Measured on 2026-11-20

On an Intel Core Ultra 9 275HX, 24 threads, 31 GB, WSL2, the database
on ext4 on a VHD on NVMe, through the Go API, the engine at commit
`1ff9991`. The run was held to 12 GB of memory, so that a mistake could
not take the machine down, and the machine was not idle: test runs of
the same day ran beside parts of it. Every figure is of three runs.
The program was written for the day and is not in the repository; the
benchmark of the gate replaces it when the indexes are built.

**The class.** `Invoice` with `number`, `order` (a `ref`, indexed),
`order_id` (a text, indexed), `net` (money) and `owner` (a text,
indexed, `USER()` by default); the read rule `owner = USER() OR
INGROUP("finance")`. Five invoices to an order, made in batches of
1,000 by `CreateMany`. Jan made them all but every thousandth batch,
which is Piotr's. Ewa is in finance.

**Loading.** 10,000,000 in 3 min 13 s, 51,700 a second, a file of
8.8 GB. 100,000,000 in 37 min 53 s, 44,000 a second, between 37,600
and 48,900 by the ten million, a file of 91.9 GB. Opening the file of
a hundred million took 3 to 10 ms.

**The questions, as the class was.** The first table of the Context.
One order's invoices through the index, among 100,000,000: 0.29 to
0.86 ms at the median of 21 askings, six runs, and 0.3 to 2.4 ms for
the first asking of a run. And at 100,000,000:

| Who asks | The newest 50 | The count of all they may read | The count in review |
|---|---|---|---|
| Ewa, 100,000,000 | over 4 min | 43 s | 2 to 2.6 min |
| Piotr, 100,000 | 67 ms | 2.6 to 2.8 ms | 9 ms |
| Jan, 99,900,000 | 3 to 4 min | 2.5 to 6 s | over 4 min |

**With indexes added by hand**, in this order: on `created_at, id`; on
`state`; on `owner, created_at, id`; on `updated_at, id`.

| | Built in | The file after |
|---|---|---|
| `created_at, id` | 171 s | |
| `state` | 344 s | 97.5 GB |
| `owner, created_at, id` | 174 s | 102.1 GB |
| `updated_at, id` | 36 s, the table being in memory | 106.2 GB |

On 10,000,000 the first two were built in 3.4 s and 1.8 s, and the file
grew from 8.83 to 9.37 GB.

| Who asks, among 100,000,000 | The newest 50 | The count of all | The count in review |
|---|---|---|---|
| Ewa, with the first two | 0.2 to 0.3 ms | 0.2 s | 0.15 to 0.6 ms |
| Piotr, with the first two | 73 to 75 ms | 2.8 to 4.9 ms | 2 to 3 ms |
| Jan, with the first two | 3 min 40 s to over 4 min | 2.7 to 3.2 s | 2.8 to 4.3 ms |
| Piotr, with the third | 0.27 to 0.35 ms | 2.8 to 3.1 ms | 2 to 3 ms |
| Jan, with the third | 0.26 to 0.35 ms | 2.6 to 3 s | 2.7 to 3.4 ms |

The first asking after the file is opened is slower, up to 3 ms for a
list and once 19 s for Jan's count.

**The drafts of one order**, `order_id = "ORD-n" AND STATE() =
"draft"`, among 100,000,000 with the four indexes: 10.4 to 11.6 s
through the engine, for Jan and for Ewa, six askings. The store's plan:
the index on `state`. The same statement in the store with the index
on `order_id` named: 0.15 to 0.6 ms; with the state kept out of the
choice of the index: 0.01 to 0.05 ms, the pages by then in memory. The
store's sampled count (`ANALYZE`, `analysis_limit` 1,000) took 272 s
and wrote 1,001 documents to a value for `order_id`, `owner` and
`state` alike; the question still took 10.4 s.

**Single commits among 100,000,000**, a thousand creates and a thousand
transitions of documents spread over the class:

| | Creates a second | A create, median | A transition, median | A transition, 99th of a hundred |
|---|---|---|---|---|
| as the class was | 5,400 to 7,000 | 0.12 to 0.17 ms | 0.19 to 0.22 ms | 6.3 to 7.7 ms |
| with three indexes more | 8,000 to 8,100 | 0.08 to 0.09 ms | 0.11 ms | 2.5 to 4.1 ms |
| with four | 8,800 to 12,300 | 0.07 to 0.09 ms | 0.09 ms | 2.3 to 2.6 ms |

The rows are not a fair comparison: the first was measured straight
after questions that had read the whole file, the others with more of
it in memory. They show that what the memory holds moved a commit more
than four indexes did. The fair comparison is the next one.

**At 2,000,000, the class as it is against the class with the indexes
of item 2**, and against the same indexes with the id of the document
in each as well, which is the shape the hundred million were asked
with. Three rounds of each, every round a new file loaded alike; the
indexes were made by hand in the empty table.

| | As it is | The indexes of item 2 | The same with the id |
|---|---|---|---|
| Loading, invoices a second | 48,100 to 49,900 | 45,200 to 45,400 | 43,600 to 44,000 |
| Creates a second, one commit each | 13,700 to 16,400 | 9,900 to 13,500 | 11,100 to 13,300 |
| A create, median | 55 to 64 µs | 63 to 92 µs | 67 to 79 µs |
| A transition, median | 60 to 67 µs | 71 to 97 µs | 72 to 87 µs |
| A transition, 99th of a hundred | 0.23 to 0.31 ms | 0.22 to 0.36 ms | 0.23 to 0.30 ms |
| The newest 50, Ewa | 1.1 s | 3.9 to 9.4 ms | 0.2 to 0.9 ms |
| The newest 50, Jan | 1.2 s | 3.6 to 9.3 ms | 0.14 to 0.22 ms |
| The newest 50, Piotr | 1.3 to 1.7 ms | 0.6 to 0.9 ms | 0.15 to 0.5 ms |
| The count in review, Ewa | 122 to 127 ms | 0.03 to 0.11 ms | 0.06 to 0.18 ms |
| The count in review, Jan | 187 to 200 ms | 0.05 to 0.13 ms | 0.07 to 0.11 ms |
| The count of all, Ewa | 7 to 10 ms | 7 to 8 ms | 17 to 25 ms |
| The count of all, Jan | 50 to 52 ms | 51 to 55 ms | 67 to 74 ms |
| The drafts of one order | 0.08 to 0.25 ms | 204 to 215 ms | 232 to 246 ms |
| The file | 1.77 GB | 1.90 GB | 2.27 GB |

The invoices were made 45,000 to a second, so the newest second holds
that many, and the index without the id leaves them to be sorted: that
is the 4 to 9 ms. The row of the drafts is the sixth finding again, in
both shapes: it is what the test of item 10 guards.

**The budget of a transition.** ADR-0012 holds a transition commit to
under 2 ms at the 99th of a hundred. Among 2,000,000 it took 0.2 to
0.4 ms with every index. Among 100,000,000 on 12 GB of memory it took
2.3 to 7.7 ms, with the new indexes and without: a document picked from
anywhere in a file eight times the memory is read from the disk first.
The budget was set for a file the memory holds, and this record does
not change it.
