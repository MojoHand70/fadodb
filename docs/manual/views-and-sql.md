# Views and SQL

A call of the engine works on one document, or on the documents of one
class that a filter picks. A report asks other questions: how many
invoices wait in each state, how long they wait there, who moved them.
For those the engine keeps **views**, named after each class, and one
call that runs a `SELECT` over them. This chapter is the reference for
both: the views of a class, their columns, how a value looks in SQL,
what a statement may do, and who may run one. A test runs every
statement of this chapter and compares the answer on each push.

SQL reads every document and every value, past `access` and `rights`.
It is for the system group `analysts`, and for nobody else.

## The class and what happened

```json
{
  "name": "Invoice",
  "title": "\"Invoice \" & number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "supplier": {"type": "text", "required": true},
    "net":      {"type": "money", "scale": 2, "required": true},
    "vat_rate": {"type": "decimal", "scale": 2, "default": "0.23"},
    "gross":    {"formula": "ROUND(net * (1 + vat_rate), 2)"},
    "issued":   {"type": "date"},
    "lines":    {"type": "rows", "schema": {"what": "text", "cost": "money"}}
  },
  "states": {"initial": "INAPPROVAL", "list": ["INAPPROVAL", "INACCOUNTING", "BOOKED"], "final": ["BOOKED"]},
  "transitions": [
    {"name": "approve", "from": "INAPPROVAL",   "to": "INACCOUNTING"},
    {"name": "book",    "from": "INACCOUNTING", "to": "BOOKED"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "CREATEDBY() = USER()",
    "transition": {
      "approve": "CREATEDBY() = USER()",
      "book":    "INGROUP(\"ACCOUNTING\")"
    }
  }
}
```

Monday 16 November 2026, in UTC. Anna is in the group `ACCOUNTING`.

<!-- what happened -->
| At | Who | Does |
|---|---|---|
| 09:00 | Krzysztof | creates `I-1`: FV/1, ACME, 1000.00 PLN, issued 13 November, with two lines: a taxi for 40.00 PLN and a hotel for 300.00 PLN |
| 09:00 | Krzysztof | creates `I-2`: FV/2, Globex, 500.00 EUR, issued 13 November |
| 09:00 | Krzysztof | creates `I-3`: FV/3, Initech, 70.00 PLN, issued 13 November |
| 11:00 | Krzysztof | approves `I-1` |
| 11:00 | Krzysztof | changes the net amount of `I-2` to 600.00 EUR |
| 12:00 | Anna | books `I-1` |
| 12:00 | Krzysztof | deletes `I-3` |

## The views of a class

<!-- the views -->
| View | It has one row for |
|---|---|
| `Invoice` | each document, as it is now |
| `Invoice_history` | each version of each document |
| `Invoice_events` | each event of a document |
| `Invoice_states` | each stay of a document in a state |
| `Invoice_actions` | each action that a commit of a document queued |
| `Invoice_lines` | each row of `lines`, of each document as it is now |
| `Invoice_lines_history` | each row of `lines`, of each version |

- **A view is named after its class.** A variable of rows has two
  views of its own, named after the class and the variable. So has a
  list, with its values in the column `value`.
- **A class without states has no view of stays.**
- **A table of reference data has one view**, under its own name; see
  [Tables](tables.md#with-sql).
- **A deleted document is not in the view of the documents as they are
  now.** Its versions, its events and its stays are still in theirs.

## The columns

A document, in `Invoice`: the columns below, and then one for each
variable, in the order of the class.

<!-- the columns of a document -->
| Column | What it is |
|---|---|
| `id` | the id of the document |
| `title` | its title |
| `state` | the state it is in |
| `version` | the number of its version |
| `class_version` | the version of the class it follows |
| `created_at`, `updated_at`, `state_entered_at` | when it was created, last changed, and when it entered its state |
| `created_by`, `updated_by`, `state_entered_by` | who did each of the three |
| `state_entered_from` | the state it came from; blank in the first state |

A version, in `Invoice_history`: the same, and four more.

<!-- the columns of a version -->
| Column | What it is |
|---|---|
| `valid_from` | the moment the version became the document |
| `valid_to` | the moment the next version took over; blank while it is the document, and the moment of the delete for the last version of a deleted document |
| `actor_id` | who made the version |
| `event_id` | the event that made it |

A stay, in `Invoice_states`:

<!-- the columns of a stay -->
| Column | What it is |
|---|---|
| `id` | the id of the document |
| `state` | the state |
| `entered_at`, `left_at` | when the document entered the state and when it left; `left_at` is blank while it is there |
| `duration` | the length of the stay in seconds; blank while it is there |
| `entry_version`, `exit_version` | the version that entered the state, and the last version in it |
| `entered_by`, `left_by` | who made the move in, and who made the move out |
| `entered_from` | the state it came from |
| `entered_via`, `left_via` | the name of the transition in, and of the transition out |
| `entered_how`, `left_how` | how each move came about: `command`, `action`, `guard` or `timer` |
| `entered_by_kind`, `left_by_kind` | what the caller of each move was: a person, an agent or a service |
| `entered_for`, `left_for` | for whom the caller of each move acted |

An event, in `Invoice_events`, has what
[History and the log](notions/history-and-the-log.md#what-an-event-carries)
lists, and `qualifier`, which says what the event did to the document:
`created`, `changed`, `transitioned`, `deleted` and so on. An action,
in `Invoice_actions`, has the parts of
[a row of the queue](the-outbox-and-the-worker.md#a-row-of-the-queue).
A row of `lines` has `doc_id`, `version`, `idx`, its place among the
rows from 0, and one column for each column of the rows.

A variable that is worked out only at chosen moments has three more
columns, which say when that was; see
[Triggered variables](triggered-variables.md#the-moment).

## How a value looks in SQL

SQL gives a value as the engine stores it, which is not always how a
document shows it.

<!-- how a value looks in SQL -->
| Type | In a document | In SQL | To read it |
|---|---|---|---|
| `text` | `"ACME"` | `ACME` | |
| `number` | `1.5` | `1.5` | |
| `int` | `"2"` | `2` | |
| `decimal` | `"0.23"` | `23` | `vat_rate / 100.0` |
| `money` | `"1000.00 PLN"` | `100000`, and `PLN` in `net_currency` | `net / 100.0` |
| `bool` | `true` | `1` | |
| `date` | `"2026-11-13"` | `20770` | `date(issued * 86400, 'unixepoch')` |
| `datetime` | `1794650400` | `1794650400` | `datetime(seen_at, 'unixepoch')` |
| `json` | `{"a": 1}` | `{"a":1}` | |
| `ref` | `"Invoice:I-1"` | `Invoice:I-1` | |

- **An exact number is a whole number of its smallest unit.** With two
  decimal places 0.23 is 23 and 1000.00 is 100000: divide by 100. With
  four places, divide by 10000.
- **Money is two columns**: the amount, and the currency in a column
  named after the first with `_currency` after it.
- **A day is the number of days since 1 January 1970**, and a moment
  the number of seconds since then. The business day of an event is a
  day too.
- **An object is one column for each field**, named by the path with
  two underscores between the parts, as `buyer__name`; see
  [Schemas and objects](schemas-and-objects.md#how-an-object-is-stored).
- **A blank is `NULL`.**

## Seven questions

The answers are those of the story above, asked at 12:00.

**1. How many invoices are in each state now?**

<!-- question -->
```sql
select state, count(*) as invoices from Invoice group by state order by state
```

<!-- answer -->
| `state` | `invoices` |
|---|---|
| BOOKED | 1 |
| INAPPROVAL | 1 |

The deleted invoice is not counted.

**2. How long does an invoice stay in each state?**

<!-- question -->
```sql
select state, count(id) as stays, avg(duration) as seconds
from Invoice_states where left_at is not null group by state order by state
```

<!-- answer -->
| `state` | `stays` | `seconds` |
|---|---|---|
| INACCOUNTING | 1 | 3600 |
| INAPPROVAL | 1 | 7200 |

**3. Who moved invoice I-1, by which transition, and after how long?**

<!-- question -->
```sql
select state, entered_by, left_by, left_via, duration
from Invoice_states where id = 'I-1' order by entered_at
```

<!-- answer -->
| `state` | `entered_by` | `left_by` | `left_via` | `duration` |
|---|---|---|---|---|
| INAPPROVAL | krzysztof | krzysztof | approve | 7200 |
| INACCOUNTING | krzysztof | anna | book | 3600 |
| BOOKED | anna | | | |

**4. What was the net amount of I-2 at 10:00?** A value of the caller
goes into the statement as `?`, and is given beside it: here the
moment 10:00, 1794823200, twice.

<!-- question: 1794823200, 1794823200 -->
```sql
select id, net / 100.0 as net, net_currency from Invoice_history
where id = 'I-2' and valid_from <= ? and (valid_to is null or valid_to > ?)
```

<!-- answer -->
| `id` | `net` | `net_currency` |
|---|---|---|
| I-2 | 500 | EUR |

**5. What do the lines of each invoice add up to?**

<!-- question -->
```sql
select i.number, count(l.idx) as lines, sum(l.cost) / 100.0 as cost
from Invoice i join Invoice_lines l on l.doc_id = i.id
group by i.number order by i.number
```

<!-- answer -->
| `number` | `lines` | `cost` |
|---|---|---|
| FV/1 | 2 | 340 |

**6. What happened, by kind of event?**

<!-- question -->
```sql
select kind, count(event_id) as events from Invoice_events group by kind order by kind
```

<!-- answer -->
| `kind` | `events` |
|---|---|
| created | 3 |
| deleted | 1 |
| set | 1 |
| transition | 2 |

**7. The invoices as a person reads them.**

<!-- question -->
```sql
select number, net / 100.0 as net, net_currency, vat_rate / 100.0 as vat_rate,
       date(issued * 86400, 'unixepoch') as issued,
       datetime(created_at, 'unixepoch') as created
from Invoice order by number
```

<!-- answer -->
| `number` | `net` | `net_currency` | `vat_rate` | `issued` | `created` |
|---|---|---|---|---|---|
| FV/1 | 1000 | PLN | 0.23 | 2026-11-13 | 2026-11-16 09:00:00 |
| FV/2 | 600 | EUR | 0.23 | 2026-11-13 | 2026-11-16 09:00:00 |

## What a statement may do

A statement is one `SELECT`. The database is SQLite, and the statement
is written in its SQL.

<!-- what a statement may do -->
| The statement | Answer |
|---|---|
| reads the views, with joins, groups and `with` | answered |
| takes values of the caller as `?` | answered |
| uses the functions of SQLite, such as `date` and `datetime` | answered |
| reads a table behind a view, such as `doc_Invoice` | `not_allowed` |
| reads a table of the engine, such as `events` | `not_allowed` |
| asks how the database is laid out, with `sqlite_master` or a pragma | `not_allowed` |
| inserts a row | `not_allowed` |
| changes or deletes rows | `bad_value` |
| answers more than 10,000 rows | `too_many_rows` |
| is not SQL, or names a view the database does not have | `bad_value` |

A statement that would answer more than 10,000 rows is refused whole.
Give it a `LIMIT`, or ask for less.

> **`count(*)` over the stays is refused.** `select count(*) from
> Invoice_states`, and `select 1 from Invoice_states`, are answered
> with `not_allowed`, though they read nothing they may not. Name a
> column: `count(id)`. Every other view counts with `count(*)`. This
> is a fault of the engine, and not yet mended.

> **A class defined while the program runs is not read by SQL at
> once.** The engine learns which views there are when a program runs
> its first statement. A class or a table that is defined after that
> is refused with `not_allowed` when a statement names one of its
> columns, until the database is opened again. This is a fault of the
> engine, and not yet mended.

> **The last stay of a deleted document has no end.** `I-3` was
> deleted at 12:00, and `Invoice_states` still shows it in
> `INAPPROVAL` with no `left_at`. To count what waits now, read
> `Invoice`, as question 1 does, and not the stays.

## Who may

- **A statement is run by a caller in the system group `analysts`.**
  Anybody else is refused with `forbidden`, naming the group.
- **It reads everything.** No rule of a class is asked: an analyst
  sees every document, every value, every version and every event.
  Name the analysts as you name those who may read the file itself.
- **It changes nothing.**
- **The command is `sql`**, with the statement under `statement` and
  the values for its `?` under `args`; see
  [The commands](commands.md#sql). In Go it is the call `SQL`.

## Not here yet

- A statement that answers only what its caller may see.
- `count(*)` over the stays.
- SQL over a class that was defined after the program's first
  statement, without opening the database again.
- An end for the last stay of a deleted document.
- DuckDB over the same views through the engine, for a caller with a
  name. An analyst who has the file itself reads it with DuckDB from
  a program of their own, past every rule; see
  [Reading a database with DuckDB](reading-with-duckdb.md).
