# Tables

A table is reference data: a list of rows with a key, kept beside the
documents. Exchange rates, price lists, cost centres and postal codes
are tables. One team keeps a table up to date, and the formulas of
every class may look values up in it. This chapter is the reference
for tables: how one is defined, how rows get into it, how a formula
reads it, how a rule writes it, and who may do each. A test defines
what stands here and plays it on each push.

The short way in is the guide
[Look a value up in a reference table](guides/look-a-value-up.md).

## A table or a class

| | A table | A class |
|---|---|---|
| Holds | rows | documents |
| One of them is found by | its key: the values of the key columns | its id |
| A change | replaces the row, and nothing is kept of what it was | makes a version and an event |
| States, transitions, rules | no | yes |
| Who may | two lines, each about the whole table | lines for each document, by state and by variable |
| A formula reads it with | `LOOKUP` | `LOOKUP` |

Keep in a table what is looked up and loaded in bulk. Keep as
documents whatever has a life of its own, or needs a record of who
changed it. The two work together: in this chapter a class of
documents feeds a table, so that every row has an approval behind it.

## The definition

A table is written once, as one JSON document, like a class.

```json
{
  "name": "Rates",
  "label": "Exchange rates",
  "description": "The rate of a currency in PLN on a day",
  "columns": {
    "currency": "text",
    "day":      "date",
    "rate":     {"type": "decimal(4)", "label": "Rate", "format": "0.0000",
                 "description": "How many PLN one unit of the currency was worth that day."},
    "source":   "text"
  },
  "key": ["currency", "day"],
  "indexes": [["day"]],
  "access": {
    "read":  "INGROUP(\"everyone\")",
    "write": "INGROUP(\"treasury\")"
  }
}
```

| Word | Meaning |
|---|---|
| `name` | the name of the table: letters, digits and underscores. A formula names it, in any case of letters. A class may not have the same name |
| `app` | the app the table belongs to, as a class names its own ([Apps](apps.md)): one the database has, or left out for none. The name stays the database's, whatever the app |
| `label`, `description` | words for people. The engine keeps them and does nothing with them. In another language they are given beside the table, with its columns' ([words in other languages](class.md#words-in-other-languages)) |
| `columns` | each column with its type; the types are below. A column may also be written in the long form, an object with its `type`, a `label`, a `description` and a `format`, as `rate` is: the words a person reads in place of its name, and how a page shows its value ([what a class says to people](class.md#what-a-class-says-to-people)) |
| `key` | the columns that identify a row. No two rows have the same values in them, none of them is ever blank, and the table is ordered by them |
| `indexes` | further sets of columns to keep an index on, for lookups that do not begin with the first column of the key |
| `access` | who may read the rows and who may write them; see [Who may](#who-may) |
| `binding` | where the rows are kept. `sqlite`, the database file itself, is the only binding that is built, and the one taken when the word is left out |
| `version` | may be left out: the engine counts the versions itself |

## The types of a column

A column holds one simple value.

<!-- the types -->
| Type | Holds | Written in a row as | Read back as |
|---|---|---|---|
| `text` | text | `"EUR"` | `"EUR"` |
| `number` | a number, as a spreadsheet keeps it | `1.5` | `1.5` |
| `int` | a whole number | `7` | `"7"` |
| `decimal(4)` | an exact number with four decimal places; `decimal` alone has two | `"4.27"` | `"4.2700"` |
| `money(2)` | an exact amount with its currency; `money` alone has two places | `"10.00 PLN"` | `"10.00 PLN"` |
| `date` | a calendar day | `"2026-11-13"` | `"2026-11-13"` |
| `datetime` | a moment, in UTC | `"2026-11-14 10:00:00"` | `1794650400` |
| `bool` | true or false | `true` | `true` |
| `ref` | a pointer to a document | `"Invoice:FV-17"` | `"Invoice:FV-17"` |
| `json` | any JSON value, kept as it is | `{"a": 1}` | `{"a": 1}` |

Values are read back as a document gives them: an exact number as
text, so that no digit is lost, and a moment as seconds since 1970.

A column cannot hold a list, rows or an object. An object goes into a
table as one column for each of its fields.

## Rows

Rows are written by an import: a list of rows, each with a value for
its columns.

```jsonc
[
  {"currency": "USD", "day": "2026-11-13", "rate": "3.9000", "source": "NBP"},
  {"currency": "EUR", "day": "2026-11-13", "rate": "4.2700", "source": "NBP"},
  {"currency": "EUR", "day": "2026-11-12", "rate": "4.2500", "source": "NBP"}
]
```

- **An import is all or nothing.** When the table refuses one row,
  none is written.
- **A row whose key is new is added.**
- **A row whose key the table has replaces that row, whole.** A column
  the new row leaves out is blank afterwards.
- **An import can replace the table.** With that option the table is
  emptied first, so that it holds the rows of this import and no
  others. An import of no rows with that option empties the table.
- **No call removes a single row.** A rule removes one, as below;
  otherwise the table is imported again, replaced.
- **An import is not an event.** The rows of a table have no versions
  and no log. Nothing says who wrote a row, or what it was before.

Rows are read back in the order of the key, whatever order they were
written in:

<!-- the rows as they are read -->
| `currency` | `day` | `rate` | `source` |
|---|---|---|---|
| EUR | 2026-11-12 | 4.2500 | NBP |
| EUR | 2026-11-13 | 4.2700 | NBP |
| USD | 2026-11-13 | 3.9000 | NBP |

## Two classes beside it

An expense in a foreign currency reads the table: its value in PLN
follows from the rate of the day it was spent on.

```json
{
  "name": "Expense",
  "title": "\"Expense in \" & currency",
  "variables": {
    "currency": {"type": "text", "required": true},
    "spent_on": {"type": "date", "required": true},
    "amount":   {"type": "decimal", "scale": 2, "required": true},
    "rate":     {"formula": "LOOKUP(Rates, rate, currency = THIS.currency AND day = THIS.spent_on)"},
    "in_pln":   {"formula": "ROUND(amount * rate, 2)"}
  },
  "states": {"initial": "open", "list": ["open", "filed"], "final": ["filed"]},
  "transitions": [
    {"name": "file", "from": "open", "to": "filed", "guard": "NOT ISBLANK(rate)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"treasury\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE"
  }
}
```

A rate entry writes the table. One person of the treasury enters a
rate, another publishes it, and only then is it a row of `Rates`.

```json
{
  "name": "RateEntry",
  "title": "\"Rate of \" & currency",
  "variables": {
    "currency": {"type": "text", "required": true},
    "day":      {"type": "date", "required": true},
    "rate":     {"type": "decimal", "scale": 4, "required": true}
  },
  "states": {"initial": "draft", "list": ["draft", "published", "withdrawn"], "final": ["withdrawn"]},
  "transitions": [
    {"name": "publish",  "from": "draft",     "to": "published"},
    {"name": "withdraw", "from": "published", "to": "withdrawn"}
  ],
  "rules": [
    {"on": "transition", "name": "publish",
     "do": [{"action": "table.insert", "table": "Rates",
             "row": {"currency": "currency", "day": "day", "rate": "rate", "source": "ID()"}}]},
    {"on": "values_change", "variables": ["rate"], "if": "STATE() = \"published\"",
     "do": [{"action": "table.update", "table": "Rates",
             "key": {"currency": "currency", "day": "day"},
             "set": {"rate": "rate"}}]},
    {"on": "transition", "name": "withdraw",
     "do": [{"action": "table.delete", "table": "Rates",
             "key": {"currency": "currency", "day": "day"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"treasury\")",
    "update": "INGROUP(\"treasury\")",
    "delete": "FALSE",
    "transition": {
      "publish":  "INGROUP(\"treasury\") AND CREATEDBY() <> USER()",
      "withdraw": "INGROUP(\"treasury\")"
    }
  },
  "rights": {
    "published": {
      "*":    {"write": "FALSE"},
      "rate": {"write": ["treasury"]}
    }
  }
}
```

## What it does

Dan is a designer. Tess and Olga are in the group `treasury`. Ann is
an employee.

<!-- what it does -->
| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | Dan | defines the table | defined, version 1 |
| 2 | Tess | imports the three rows above | 3 rows written |
| 3 | Ann | imports a row | refused, `forbidden` at `/access/write` |
| 4 | Ann | reads the rows | 3 rows |
| 5 | Ann | creates an expense of 100.00 EUR spent on 13 November | `rate` 4.2700, `in_pln` 427.00 |
| 6 | Tess | imports the row of EUR on 13 November again, with 4.3000 | 1 row written |
| 7 | Ann | reads her expense | `rate` 4.2700, `in_pln` 427.00 |
| 8 | Ann | creates an expense of 50.00 EUR spent on 16 November | `rate` blank, `in_pln` 0.00 |
| 9 | Ann | files it | refused, `guard_false` |
| 10 | Tess | enters the rate of EUR on 16 November, 4.3100 | allowed |
| 11 | Tess | publishes it herself | refused, `forbidden` at `/access/transition/publish` |
| 12 | Olga | publishes it | allowed |
| 13 | the engine | runs the queue | 1 action ran; the table has 4 rows |
| 14 | Ann | reads the expense of step 8 | `rate` blank, `in_pln` 0.00 |
| 15 | Ann | sets its day to 13 November, and back to 16 November | `rate` 4.3100, `in_pln` 215.50 |
| 16 | Ann | files it | allowed |
| 17 | Olga | changes the rate of the entry to 4.3200 | allowed |
| 18 | the engine | runs the queue | 1 action ran; the rate of the row is 4.3200 |
| 19 | Ann | reads the expense she filed | `rate` 4.3100, `in_pln` 215.50 |
| 20 | Olga | withdraws the entry | allowed |
| 21 | the engine | runs the queue | 1 action ran; the table has 3 rows |

The table after step 13:

<!-- the rows after step 13 -->
| `currency` | `day` | `rate` | `source` |
|---|---|---|---|
| EUR | 2026-11-12 | 4.2500 | NBP |
| EUR | 2026-11-13 | 4.3000 | NBP |
| EUR | 2026-11-16 | 4.3100 | the id of the rate entry |
| USD | 2026-11-13 | 3.9000 | NBP |

## A formula that reads a table

```
LOOKUP(Rates, rate, currency = THIS.currency AND day = THIS.spent_on)
```

reads: from the table `Rates`, the column `rate`, of the first row
whose `currency` is this document's `currency` and whose `day` is this
document's `spent_on`.

- **Inside the condition a bare name is a column of the table**, and
  `THIS.` reaches the document.
- **Names are found in any case of letters; values are compared as
  they are.** `LOOKUP(rates, RATE, CURRENCY = THIS.currency)` is the
  same lookup, and an expense in `eur` finds no row of `EUR`.
- **Several rows may match, and the first in the order of the key is
  taken.** For `Rates` that is the oldest day of the currency.
- **No row gives a blank**, and a blank counts as zero in arithmetic:
  step 8 has `in_pln` 0.00. Where that matters, guard the next
  transition with `NOT ISBLANK(rate)`, as `file` does.
- **A row for which the condition cannot be worked out does not
  match.** A price in EUR is not "less than 8.00 PLN": the row is
  passed over. A lookup itself never fails.
- **The answer has the type of the column.** `rate` of the expense is
  an exact number with four places, as the column is.

### One value from many rows

In the place of the column may stand `COUNT`, `SUM`, `MIN` or `MAX`.
The answer is then worked out from every row that matches. With the
three rows Tess imported:

<!-- one value from many rows -->
| Formula | Gives | For EUR | For a currency with no row |
|---|---|---|---|
| `LOOKUP(Rates, rate, currency = THIS.currency)` | the column, of the first row | 4.2500 | blank |
| `LOOKUP(Rates, COUNT(day), currency = THIS.currency)` | how many rows match | 2 | 0 |
| `LOOKUP(Rates, SUM(rate), currency = THIS.currency)` | the sum of the column | 8.5200 | 0.0000 |
| `LOOKUP(Rates, MIN(rate), currency = THIS.currency)` | the smallest | 4.2500 | blank |
| `LOOKUP(Rates, MAX(rate), currency = THIS.currency)` | the largest | 4.2700 | blank |

- `COUNT` counts the rows that have a value in the column. Count a key
  column to count the rows.
- `SUM`, `MIN` and `MAX` take numbers and amounts. Over a day or a
  text they are refused when the class is defined.
- One lookup may not stand inside another. Two may stand side by side
  in one formula. To feed one into another, give the first a variable
  of its own.

### When a lookup is worked out

<!-- when a lookup is worked out -->
| The lookup stands in | It is worked out |
|---|---|
| a computed variable | when the document is created, and again when a value the lookup reads changes; not when the table changes |
| a default | once, when the document is created |
| a guard, a line of `access`, a right to write, a rule | each time it is asked, over the table as it is then |

So a document keeps the value it looked up. Ann's first expense keeps
4.2700 after Tess corrected the row, in step 7, and her filed expense
keeps 4.3100 in step 19. That is usually what a business wants: a
document does not change because the reference data moved.

The price of it is step 14. An expense that looked before the row was
there keeps its blank when the row arrives. It looks again only when
`currency` or `spent_on` changes, as in step 15. Where a document must
take a value at a moment of its own, let a rule set it at that moment:
a `set` whose value is the lookup, on the transition that needs it.

### Where a lookup may stand

<!-- where a lookup may stand -->
| Place | A lookup of a table |
|---|---|
| a computed variable, a default | yes |
| a guard | yes |
| `create`, `update`, `delete` and the line of a transition under `access`; a right to write | yes |
| the `if` of a rule, a value a rule sets | yes |
| `access.read`, a right to read | no, `not_allowed` |
| the `at` of a transition | no, `not_allowed` |
| a line of `access` of a table | no, `bad_formula` |

What decides who sees a document or a value is asked of many documents
at once, in a filter and in a count, so it reads the document and the
caller only. Look the value up in a computed variable, and test that
variable there.

### A formula reads with the engine's eyes

The `read` line of a table is not asked when a formula looks a value
up. A class that reads a table shows what it read to whoever may see
the document, whether or not they may read the table. The `read` line
guards the list as a whole, and the designer of a class decides what
of it a document shows.

A formula reads the documents of a class with the same function, the
name of the class in the place of the table, in every place but a
computed variable; see the variations of the
[guide](guides/look-a-value-up.md).

## A rule that writes a table

Three actions of a rule change one row each.

<!-- the three actions -->
| `action` | Takes | Does | When the row |
|---|---|---|---|
| `table.insert` | `table`; `row`, a formula for each column, the key columns among them | adds one row | is there already: the action is refused, `duplicate` |
| `table.update` | `table`; `key`, a formula for each key column; `set`, a formula for each column to change | changes the row with that key | is not there: the action is refused, `unknown_row` |
| `table.delete` | `table`; `key`, a formula for each key column | removes the row with that key | is not there: nothing happens, and the action is done |

- **They run after the commit.** The action is queued in the same
  write as the commit, and the engine runs it afterwards, once; see
  [Rules and actions](notions/rules-and-actions.md). Between steps 12
  and 13 the entry is published and the row is not there yet.
- **The row gets the values of the moment that caused it**: the
  formulas are worked out in the commit.
- **It runs as the person whose commit set the rule off**, and the
  `write` line of the table is asked of that person. Step 13 writes
  the row as Olga.
- **One row each.** Many rows are an import.
- **An update changes no key column.** To move a row, delete it and
  insert another.
- **The record is the document's.** The table keeps no log, and the
  rate entry does: who entered the rate, who published it, when, and
  the event `action_run` for the row. The column `source` holds the id
  of the entry, so every row leads back to it.

<!-- the log of the rate entry -->
| Event | Name | By |
|---|---|---|
| `created` | | tess |
| `transition` | `publish` | olga |
| `action_run` | `table.insert` | olga |
| `set` | | olga |
| `action_run` | `table.update` | olga |
| `transition` | `withdraw` | olga |
| `action_run` | `table.delete` | olga |

An action that is refused does not undo the commit that queued it.

<!-- an action that is refused -->
| What happens | The action |
|---|---|
| a second entry for EUR on 16 November is published | is refused, `duplicate`; the row of the first entry stays |
| the rate of a published entry is changed after its row was removed from the table | is refused, `unknown_row` |
| an entry is withdrawn after its row was removed from the table | is done; nothing was there to remove |
| a class lets somebody outside `treasury` take a transition whose rule writes `Rates` | is refused, `forbidden`; the transition stands |

A refused action is kept as dead, with the event `action_failed` and
the code on the document, where an administrator finds it.

What is checked when a class with such a rule is defined:

<!-- checked when the class is defined -->
| The rule | Code |
|---|---|
| names a table the database does not have | `unknown_table` |
| names no table | `missing` |
| inserts a row with no value for a key column | `missing` |
| addresses a row with no value for a key column | `missing` |
| names a column the table does not have | `unknown_variable` |
| gives a column a value of another type | `type_mismatch` |
| changes a key column | `not_allowed` |
| addresses a row by a column that is not in the key | `not_allowed` |
| has a part its action does not take | `not_allowed` |

A formula that names a table or a column that is not there, reads the
document without `THIS.`, or puts one lookup inside another is refused
with `bad_formula`, and the message says which.

## Who may

<!-- who may -->
| To | A caller needs |
|---|---|
| define a table | the system group `designers` |
| read the definition of a table | nothing: a definition is open to every caller |
| read the rows | the `read` line of the table |
| write rows, by an import or through a rule | the `write` line of the table |
| read the rows with SQL | the system group `analysts` |

- **The two lines are about the table as a whole.** Each is a
  condition over the caller and the clock, such as
  `INGROUP("treasury")` or `USERKIND() = "person"`. It cannot read a
  column, so a table cannot show a caller some rows and not others.
- **A line left out lets nobody through**, as in a class. The engine
  does not note it when the table is defined; the refusal says which
  line is missing.
- **`TRUE` lets a call through that names nobody.**
  `INGROUP("everyone")` asks for a caller with an id.
- **A content administrator reads and writes every table, with a
  reason.** Without one the lines apply to them as to anybody, and
  what they would get past is refused with `reason_required`. A table
  keeps no history of its own, so the [log of reads](the-log-of-reads.md)
  keeps the import or the read: the table, how many rows, the line it
  got past, who and why. An action a rule queued writes a table as the
  person whose commit queued it, with the reason that commit gave.
- **A formula asks neither line**, as said above.

The system groups are in [Who may](notions/who-may.md).

## Changing a table

<!-- changing a table -->
| The table | A changed definition |
|---|---|
| has no rows, and no class reads it | is taken, as the next version |
| has rows, and the change is more than new columns | is refused, `not_supported` |
| is read by a formula of a class, and the change is more than new columns | is refused, `not_supported` |
| would break a rule of a class that writes it | is refused, `not_allowed` |
| gains columns, none of them in its key, and nothing else changes | is taken, as the next version, whatever rows it holds and whoever reads it; its rows are blank in each new column |
| names another app, or none, and nothing else changes | is taken, as the next version, whatever rows it holds and whoever reads it |

- **The same definition again changes nothing**, and the version
  stays.
- **Every version says who defined it and when**, and every caller may
  read that.
- **No call removes a table.**

For any other change of a table that is in use, the engine's own
advice is to define the new shape under another name. A class moves over to the new table with
a new version of the class, and the old table can be changed once no
class reads it; see [Change a class that already has
documents](guides/change-a-class.md).

## With SQL

For SQL the engine keeps a view under the name of the table. A caller
in the system group `analysts` reads it. SQL only reads.

```sql
select currency, day, rate from Rates where currency = 'USD'
```

<!-- as SQL gives it -->
| `currency` | `day` | `rate` |
|---|---|---|
| USD | 20770 | 39000 |

SQL gives the values as the engine stores them: a day as the number of
days since 1 January 1970, and an exact number as a whole number of
its smallest unit, so 3.9000 with four places is 39000. A money column
is two columns, the amount and the currency, the second under the name
of the first with `_currency` after it. The rest is in
[Views and SQL](views-and-sql.md).

## How fast a lookup is

What a lookup costs follows from its condition.

| The condition | Is answered | Measured |
|---|---|---|
| one equality of a column with a value of the document, such as `code = THIS.currency`, alone and with no `COUNT`, `SUM`, `MIN` or `MAX` | from an index in memory | 88 ns |
| such an equality as one of several terms joined by `AND`, or with `COUNT`, `SUM`, `MIN` or `MAX`; and the first kind over a table above the limit | by a statement over the rows that have that value | 2.4 µs |
| anything else: an `OR`, a comparison such as `<=` with no equality beside it, an equality with a money column | by a statement over every row | 35 µs for each thousand rows |

- **The limit** is 100,000 rows. Up to it the engine keeps the table
  in memory once a lookup of the first kind has needed it, and drops
  that copy when the table is written. The host program may set
  another limit when it opens the database, with the option
  `MaxIndexedRows`; see [Opening a database](opening-a-database.md).
  The limit changes the speed of a lookup and never its answer.
- **Help the statement.** Let the column a lookup compares with the
  document be the first column of the key, or the first of an index.
- **The measurements** are those of the release 0.2.0, in
  [Performance](../perf/v0.2.0.md): one lookup inside a recalculation,
  over a table of 1,000 rows.

## The commands

Everything a caller does with a table is a command, for a program or
an agent that speaks to the engine in JSON.

<!-- the commands -->
| To | Command |
|---|---|
| define a table | `define_table` |
| read its definition back | `get_table` |
| list the tables, or those of one app | `list_tables` |
| write rows | `import_rows` |
| read the rows | `table_rows` |

What each takes and what it answers is in
[The commands](commands.md#definitions).

## What is checked when the table is defined

<!-- checked when the table is defined -->
| The table | Code |
|---|---|
| has no name, or a name that is not letters, digits and underscores | `bad_name` |
| has a name that begins with `doc_` or `tbl_` | `reserved_name` |
| names an app the database does not have | `unknown_app` |
| has the name of a class | `duplicate` |
| has the name of another table in another case of letters | `duplicate` |
| has no columns | `missing` |
| has no key | `missing` |
| has two columns whose names differ only in the case of letters | `duplicate` |
| has a column of a type the engine does not know | `unknown_type` |
| has a column that is a list or rows | `not_allowed` |
| has a column that is an object | `not_supported` |
| names a column in its key that it does not have | `unknown_variable` |
| names a column twice in its key | `duplicate` |
| has a `json` column in its key | `not_allowed` |
| has an index that names no column | `missing` |
| has an index that names a column it does not have | `unknown_variable` |
| has a binding that is not built yet, `duckdb` or `external` | `not_supported` |
| has a binding the engine does not know | `bad_value` |
| has a line of `access` that reads a column, another table or a document | `bad_formula` |
| has a line of `access` that is not a condition | `not_a_condition` |
| has a word the engine does not know | `bad_json` |

A class with the name of a table is refused the same way, with
`duplicate`.

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| names a table the database does not have | `unknown_table` | |
| may not read the rows | `forbidden` | `/access/read` |
| may not write the rows | `forbidden` | `/access/write` |
| defines a table and is no designer | `forbidden` | `/system/designers` |
| gives a row with a blank in a key column | `required` | the row and the column, as `Rates[1].day` |
| gives a value that does not fit its column | `bad_value` | the row and the column |
| gives a column the table does not have | `unknown_variable` | the row and the column |
| imports as of another moment | `not_allowed` | |

Rows are counted from zero: `Rates[1]` is the second row of the
import.

## Not here yet

- Changing or removing a column of a table that has rows, or that a
  class reads, and a new column in its key.
- Removing a table.
- The last row in the order of the key, and `MIN` and `MAX` of days.
  So "the newest rate on or before a day" has no short form today: a
  table of rates needs a row for every day that is asked for.
- A column that holds an object or a list.
- Rows kept outside the database file: the bindings `duckdb` and
  `external`.
- An import from a file through the command line.
- A rule that a change of a table sets off.
- Rights for single rows or single columns.
- A record of who changed a row. Where that matters today, let
  documents feed the table, as the rate entry does.
