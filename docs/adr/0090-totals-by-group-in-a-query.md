# ADR-0090: A total among the columns of a QUERY: one line for each group

- Status: Proposed; follows ADR-0089 (step 3, its third part); amends ADR-0016 (the `groupby` place of `QUERY` is not written: the plain columns group), ADR-0076 (item 9: a column of a `QUERY` may be a total, and the plain columns then group; item 8: such a line is no document and has no `id`), ADR-0089 (item 6: which engine answers it)
- Date: 2026-11-25

## Context

ADR-0076 built `QUERY` for a list of documents and left out, on
purpose, "grouping, and totals among the columns of a `QUERY`",
"planned with the reports of Phase 8, which read everything".
ADR-0089 keeps a copy of the documents for such questions, and its
step 3 sends them there. This record says how such a question is
written and what it answers.

Acme's class Invoice has `customer`, `net` and a state.

| What Ewa, in finance, wants | Today |
|---|---|
| the invoices of one customer, as a list | `QUERY(Invoice, {number, net, state}, customer = THIS.customer)` |
| one number: how much Globex was invoiced | `LOOKUP(Invoice, SUM(net), customer = "Globex")` |
| one line for each customer: the customer, how many invoices, their total | cannot be written; only an analyst can ask it, with `sql` |

The functions for totals exist. `COUNT`, `SUM`, `MIN`, `MAX`,
`AVERAGE`, `MEDIAN` and `COUNTUNIQUE` stand where a `LOOKUP` names
its column (ADR-0046, ADR-0081). What is missing is the list.

ADR-0016 wrote the signature of `QUERY` with a place for the groups,
`QUERY(source, columns, condition, sort, groupby, limit)`, and
ADR-0046 left how the grouping is written to "the record that builds
it: the first real use should shape the answer". This is that record.

Measured on 2026-11-25 for ADR-0089, among 3,400,000 invoices: the
list of totals by customer takes the store 1.35 seconds and the copy
0.006.

## Decision

1. **A column of a `QUERY` may be a total**, written as in a `LOOKUP`:

   ```
   QUERY(Invoice, {customer, COUNT(id), SUM(net)})
   ```

   The totals are `COUNT`, `SUM`, `MIN`, `MAX`, `AVERAGE` and
   `COUNTUNIQUE`, each over one variable of the source or one of its
   system columns.

   When one column is a total, the plain columns say what the lines
   are grouped by: one line for each customer. It is the rule of a
   pivot table. With no plain column the answer is one line, the
   totals over every document the condition takes.

   So the `groupby` place of ADR-0016 is never written. A group that
   is no column of the list would be a line nobody can tell from the
   next, and a group that is one would be written twice. A fifth
   argument stays refused, with `not_supported`.

2. **The rest of `QUERY` is as it was.** The condition says which
   documents are counted, and what the list is sorted by may be a
   group or a total, written as among the columns:

   ```
   QUERY(Invoice, {customer, SUM(net)}, state = "paid", {-SUM(net)})
   ```

   the paid invoices by customer, the largest total first. Without a
   sort the lines come in the order of their groups.

3. **A line of such a list is no document.** It has no `id` (ADR-0076,
   item 8, is for a line that is one document). A face that wants the
   invoices behind a line asks a second `QUERY`.

4. **What a line holds.** Each group under the name of its variable,
   with its type, label and format. Each total under the name of its
   function and its variable, in lower case with `_` between:
   `count_id`, `sum_net`. A total has the type its function gives in a
   `LOOKUP`: a count is a number, a sum of money is money. The same
   total written twice is refused.

5. **A total counts what the reader may read.** The documents are
   those the read rule of the class shows the reader, as for every
   list (ADR-0076, item 6). Of them a document is counted only where
   its state lets the reader read every variable the list names, a
   group or what a total runs over (ADR-0041): so every total of a
   line is over the same documents. A reader who may read one of
   those variables in no state is refused, with `forbidden`. A content
   administrator who passes the rules with a reason is written down
   with how many documents only that let them count (ADR-0067).

6. **Where it is answered.** A `QUERY` with a total in a live variable
   is read when it is asked. It goes where a count and a total of a
   `LOOKUP` go (ADR-0089, item 6). In a program that keeps the copy,
   to the copy at once when no index narrows the condition of the
   list, brought up to the database first (item 5); to the store for a
   moment when one does, and to the copy when the moment has passed.
   In a program without the copy, to the store. The answer is the
   same. In a triggered variable, which keeps the list, it is
   worked out inside the commit, by the store (ADR-0020, item 4).

7. **What is refused when the class is defined.**

   | Written | Refused with |
   |---|---|
   | a total over what is no variable of the source | `bad_formula`, as a column the source has not |
   | a total its function cannot run over, a sum over a text | `bad_formula`, with what a `LOOKUP` says of it |
   | a total over a live variable of the source, or over a list | `not_allowed` |
   | the same total twice | `duplicate` |
   | a total over what is worked out, such as `SUM(net * 2)` | `not_supported`: a computed variable of the source gives the value, and the total is over it |
   | a function that is no total where a column stands | `not_allowed`, naming the six |
   | `MEDIAN` among the columns | `not_supported`: not built by this record |
   | a sort by what is neither a group nor a total of the list | `not_allowed` |
   | a total over a table (ADR-0076, item 9) | `not_supported`: not built by this record |
   | such a list in a triggered variable | `not_supported` until it is built |
   | a figure over such a list, `COUNT(by_customer)` | `bad_formula`: a figure runs over a list of documents; not built |

8. **What is refused when it is asked.** A sum of money in two
   currencies within one group, as in a `LOOKUP` (ADR-0046). The
   caller's page, `order`, `limit` and `offset`, is of the lines, at
   most 10,000 of them (ADR-0013), and `more` says that lines follow.
   `fields` is refused: the columns say what the lines are grouped by,
   so a reader cannot take some of them. A blank group is a line of
   its own, and stands first.

## Questions for the owner

1. **How is a list of totals by group written?** (item 1)

   The case: Ewa wants one line for each customer, with the number of
   invoices and their total.

   - **Proposed: a total among the columns of `QUERY`**,
     `QUERY(Invoice, {customer, COUNT(id), SUM(net)})`. Nothing new to
     learn; the plain columns group, as in a pivot table.
   - **Another way: the fifth place of ADR-0016**,
     `QUERY(Invoice, {customer, SUM(net)}, TRUE, {customer}, {customer})`.
     The groups are said outright; each is written twice, and the
     condition and the sort must be written to reach the place.
   - **Another way: a function of its own**,
     `TOTALS(Invoice, {customer}, {COUNT(id), SUM(net)})`, with the
     groups and the totals in two places. One more function for what
     `QUERY` nearly does.

Items 2 to 8 are settled by me while writing and building, and each
is the owner's to strike. The ones that could have gone another way:

- **The name of a total in a line** is its function and its variable,
  `sum_net` (item 4). A name of the designer's can come later.
- **A document is counted only where the reader may read every
  variable the list names**, and a reader who may read one of them in
  no state is refused (item 5). A figure over a list gives a blank
  there (ADR-0081); a line of totals whose count and sum ran over
  different documents would say what is not so.
- **`fields` is refused** for such a list (item 8).
- **`MEDIAN`, a total over what is worked out, and the largest date
  are left out** (item 7): the first two as this record says below,
  the third because `MAX` over a date is refused in a `LOOKUP` too.

## What is built

- **Step 1, on 2026-11-26: such a list in a live variable**, over the
  documents of a class, answered by the store, and by the copy when
  the program keeps one and no index narrows what the list counts
  (ADR-0089, step 3, its third part). A list an index narrows is
  the store's for a moment and the copy's after it, as a count is.
  Measured through the engine among 3,400,000 invoices, three
  runs that agree: one line for each customer takes a program without
  the copy 675 ms and one with it 12; a list by owner and state, the
  largest sum first, 3.4 seconds and 15 ms; one line of the whole
  class 980 ms and 10; the invoices in review by customer, which the
  index of the state narrows to 170,000, 72 ms and 13.
- **Not built:** such a list kept by a triggered variable; `MEDIAN`
  among the columns; a total over what is worked out; a table as the
  source; a figure over such a list. Each is refused when the class is
  defined, and says so.

## Consequences

- A report that an analyst had to write in SQL is a formula of a
  class, read by a face with the command it reads every list with.
- `QUERY` has two meanings for a plain column, told apart by whether a
  total stands beside it.
- In a program without the analytical engine such a list reads every
  document the condition takes, each time it is asked, and is noted
  with `reads_all` as a total in a `LOOKUP` is (ADR-0075, item 5).

## What this record leaves out

- **The middle value of each group**, `MEDIAN` among the columns. The
  store has no middle value of its own, and the statement that finds
  one for a `LOOKUP` reads the rows in a way that does not group. The
  middle of all, one number, is a `LOOKUP`.
- **A total over what is worked out**, `SUM(net * 2)`.
- **Totals over the states documents visited**, such as the middle
  wait in review. That reads the visits and not the documents.
- **A name of the designer's for a total.**
- **A total over the lines of the documents**, units sold by product:
  the copy does not hold lines (ADR-0089).
- **Groups by a part of a value**, such as the month of a date. A
  computed variable of the class gives the part, and the list groups
  by it.
- **A total over a table.**

## Performance

| Budget | The benchmark that proves it |
|---|---|
| a list of totals by one group over 3,400,000 documents, from the copy: under 0.1 s | measured by hand on a database of that size on 2026-11-26: 12 ms from the copy, 675 from the store |
| a list of totals over 10,000 documents in 100 groups, from the store | `BenchmarkLiveTotals` in `internal/store`, not in the gate |
| a `QUERY` without a total is no slower | the benchmark gate |
