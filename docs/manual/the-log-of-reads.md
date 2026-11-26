# The log of reads

Most callers see only what the rules of a class give them. Two may see
more. A **content administrator** may see any document past its rules,
so that a document that waits for a person who left is never stuck. An
**analyst** runs SQL, which has no rules at all. This chapter is about
what that costs them: a reason, and an entry in a log that somebody
else reads. It is for whoever runs a database, whoever audits one, and
whoever builds a face for a content administrator. A test plays the
chapter's story and every table of it on each push.

The decision is [ADR-0067](../adr/0067-the-log-of-reads.md). Changes
past a rule are older: the event of the commit keeps the rule and the
reason, as [Who may](notions/who-may.md) shows.

## The case

At Acme, Jan has a salary review, and Ewa is his manager. Hana of HR
writes the reviews. Olga is a content administrator, Marek an analyst,
and Tomek a database administrator: he runs the database and reads
none of its documents.

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

## What it does

<!-- what it does -->
| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | Hana | writes Jan's review, SR-017, and Ann's, SR-018, both managed by Ewa | allowed |
| 2 | Ewa | reads Jan's review | allowed |
| 3 | Olga | reads Jan's review, giving no reason | refused, `reason_required` at `/access/read` |
| 4 | Olga | lists the reviews, giving no reason | 0 documents |
| 5 | Olga | reads Jan's review, with the reason "Jan asked HR to correct his manager" | allowed |
| 6 | Olga | lists the reviews, with the reason "the yearly check" | 2 documents |
| 7 | Olga | counts them, with the same reason | 2 |
| 8 | Marek | runs `SELECT employee, salary FROM SalaryReview WHERE salary > ?` with 20000 | 2 rows |
| 9 | Olga | reads the log of reads | refused, `forbidden` at `/system/administrators` |
| 10 | Tomek | reads the log of reads | the four entries below |

Ewa's read in step 2 and Olga's in step 4 are what the rules give them,
and leave no entry. Without a reason Olga is held by the rules as
anybody in her groups: a review only the override would show her is
refused, so that she knows it is there, and a list leaves it out.

What Tomek reads:

<!-- the log -->
| Seq | By | Command | Documents | Rules | Counted or rows | Reason |
|---|---|---|---|---|---|---|
| 1 | olga | get | SR-017 | /access/read | | Jan asked HR to correct his manager |
| 2 | olga | query | SR-017 SR-018 | /access/read | | the yearly check |
| 3 | olga | count | | | counted 2 | the yearly check |
| 4 | marek | sql | | | rows 2 | |

- **"Who has seen Jan's review?" has one answer.** Every call that
  showed it past a rule names it, a list too.
- **A count names no document.** It says how many only the override
  counted.
- **A SQL statement keeps its words, never its rows**: the statement,
  its arguments, and how many rows it returned. A statement that fails
  is kept too, with the code it failed with. An analyst may give a
  reason, and it is kept; none is asked.
- **Each entry says when**, who, what kind of caller it was (a person,
  an agent or a service), for whom it acted, and the moment the call
  read at, when it was not now.

## What is recorded

- **Only what the override showed.** A content administrator is asked
  the rules first, as everybody is. What the rules let them see anyway
  is their ordinary work.
- **One entry for each call.** A list holds at most 10,000 documents,
  so an entry is bounded.
- **One document**: `get`, `version`, `history`, `events`,
  `transitions`, `preview` and `preview_delete`; a file the document
  holds.
- **Many**: `query`, `count`, `flow`, `due` and `preview_many`.
- **The stream of changes**: one entry for each batch it sends that
  holds an event past the rule, naming the documents of that batch.
- **A variable a right hides** is past a rule too: its entry names
  `/rights/<state>/<variable>/read`.
- **Bytes opened by their hash alone** get past the rules of every
  document that holds them, so they need a reason every time, and the
  entry keeps the hash. Over HTTP the reason is a parameter of the
  address, `reason=`.
- **Tables.** A table keeps no history of its own, so a content
  administrator's import past its write rule, or read past its read
  rule, is kept here with the table and the number of rows. So is a
  row that a rule's action writes past the table's rule, with the
  reason of the commit that queued it.
- **The answer of a change.** A change past a rule keeps its rule and
  its reason on its event. Whatever its answer shows past another rule,
  a variable the rights hide, is kept here, in the same transaction.

Not recorded: a read the rules allow; a formula that reads other
documents, since a formula sees every document; the worker's own work;
a program that reads the database file itself. Whoever can copy the
file reads everything in it.

## No trace, no answer

The entry is written before the answer leaves. If it cannot be written,
the call fails with `storage` and shows nothing. A read that is
recorded therefore waits for the database's one writer, as a small
change does. A read nobody records takes the path it always took.

Nothing deletes an entry.

## Reading the log

The command `reads` lists the log, oldest first. Every member is
optional: `by`, `class`, `id` (with `class`), `table`, `command`,
`since`, `until`, `after` and `limit`. [The commands](commands.md#reads)
lists them. Tomek asks who has seen Jan's review:

<!-- who saw SR-017 -->
```jsonc
{"class": "SalaryReview", "id": "SR-017"}
```

and is answered:

<!-- the answer -->
```jsonc
{"reads": [{"seq": 1, "at": 1794992400, "by": "olga", "kind": "person", "command": "get", "class": "SalaryReview", "documents": ["SR-017"], "rules": ["/access/read"], "reason": "Jan asked HR to correct his manager"},
           {"seq": 2, "at": 1794992400, "by": "olga", "kind": "person", "command": "query", "class": "SalaryReview", "documents": ["SR-017", "SR-018"], "rules": ["/access/read"], "reason": "the yearly check"}]}
```

To read on, he sends the `seq` of the last entry he has as `after`.

## Who reads it

- **Database administrators**, the system group `administrators`, and
  nobody else. SQL cannot read the log: it is not a view.
- **Whoever reads past the rules is not whoever checks the log.** A
  content administrator or an analyst who is not also a database
  administrator cannot read the log of their own reads. A host program
  that puts one person in both groups gives them that power.
- **The log names documents, rules and statements, and holds no value
  of a document.** So whoever runs the database still reads none of
  its documents.

## A face, and an agent

A page that shows a content administrator what the rules hide asks
them why, once, and sends the same reason with each call. Each call is
an entry. Without a reason the page shows what the rules give anybody
in their groups, which is often all they need. An agent in the group
`content-administrators` gives its own reason in the member `reason`.

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| reads one document past its rule as a content administrator, and gives no reason | `reason_required` | `/access/read` |
| imports into a table past its write rule as a content administrator, and gives no reason | `reason_required` | `/access/write` |
| opens bytes by their hash alone as a content administrator, and gives no reason | `reason_required` | |
| reads the log, and is no database administrator | `forbidden` | `/system/administrators` |
| names a document of the log without its class | `bad_value` | |
| asks for more than 10,000 entries at once | `bad_value` | |

## Not here yet

- Telling Jan who has read his review: a person reading the log of
  their own documents.
- A list that says how many documents it left out for want of a
  reason.
- Trimming the log after some years.
- What an analyst reads with a program of their own over the file of
  the database, such as DuckDB
  ([Reading a database with DuckDB](reading-with-duckdb.md)). The
  engine does not see it, so nothing is written here: whoever has the
  file reads all of it.
