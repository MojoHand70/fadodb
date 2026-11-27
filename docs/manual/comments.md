# Comments

People and agents who work on one document talk about it: an approver
asks why a taxi was taken, the author answers, an agent that read the
receipt says what it saw. In FadoDB a comment is kept by the engine
beside its document: its words, who wrote them, when, and the version
of the document they were written on. A comment makes **no version** of
the document: no rule runs, no formula is worked out, and the values,
the state and the version stay as they are. Each comment is an event,
so the log and the stream of changes hold it with who and when.

This chapter is the reference for comments. A test plays what stands
here on each push. The design is
[ADR-0098](../adr/0098-comments-on-a-document.md).

## The class

A trip its author files and accounting pays. The author and approvers
read it; nobody else does.

```json
{
  "name": "Trip",
  "title": "what",
  "variables": {
    "what": {"type": "text", "required": true},
    "cost": {"type": "money", "scale": 2}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file", "from": "draft", "to": "filed"},
    {"name": "pay", "from": "filed", "to": "paid"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR CREATEDBY() = ACTINGFOR() OR INGROUP(\"approvers\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "CREATEDBY() = USER()"
  }
}
```

Nothing in the class speaks of comments: every class takes them.

## What it does

Ola makes a trip. Anna is in the group `approvers`. Piotr is a
colleague with no part in it. The reader is an agent that reads
receipts for Ola, as her substitution lets it.

<!-- what it does -->
| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | Ola | makes a trip, "Taxi to the airport", 120.00 PLN | allowed, version 1 |
| 2 | Anna | comments "Why a taxi? The train is cheaper." | allowed, on version 1 |
| 3 | Ola | comments "The last train had left." | allowed, on version 1 |
| 4 | Piotr | comments "Nice trip." | refused, unknown_document |
| 5 | Piotr | reads the comments | none |
| 6 | Ola | reads the trip | version 1 |
| 7 | the reader, for Ola | comments "The receipt says 23:40." | allowed, on version 1 |
| 8 | Ola | reads the comments | anna, ola, reader for ola |
| 9 | Ola | comments "Booked by phone." with the key `ola-9` | allowed, on version 1 |
| 10 | Ola | sends step 9 again | the same comment |
| 11 | Ola | comments "Booked online." with the key `ola-9` | refused, key_reused |
| 12 | Ola | files the trip | allowed, version 2 |
| 13 | Ola | comments, saying she read version 1 | refused, stale_version |
| 14 | Ola | pays the trip | allowed, version 3 |
| 15 | Anna | comments "Paid back on 28 November." on the paid trip | allowed, on version 3 |
| 16 | Ola | reads the events of the trip | created, commented, commented, commented, commented, transition, transition, commented |

- **A comment is the writer's.** The engine writes who it was, what
  they are, person, agent or service, and whom they acted for, as the
  reader's comment of step 7 says; the moment and the business day are
  the commit's. The caller says only the words.
- **A comment makes no version** (step 6). Its event, `commented`, has
  the version the comment was written on as its `before_version`, and
  no `after_version`. `history` lists versions, and so shows no
  comment; `events` and the stream of changes show every one.
- **A final state takes comments** (step 15): a note after the fact is
  part of the document's story.
- **Sent twice with a key**, a comment is answered as it was the first
  time, and nothing is written again (step 10); the same key with
  other words is refused (step 11). An agent that may lose an answer
  gives a key to every comment.
- **A comment may say which version its writer read** (step 13): when
  the document has moved on, it is refused and nothing is written.

## A comment

The answer of `comment`, and each element of `comments`:

<!-- a comment -->
```jsonc
{
  "comment_id": "01M8D2A9K3",
  "class": "Trip",
  "doc_id": "01M8D2A6R0",
  "text": "The receipt says 23:40.",
  "actor_id": "reader",
  "actor_kind": "agent",
  "actor_for": "ola",
  "version": 1,
  "business_date": "2026-11-26",
  "ts": 1795725296
}
```

<!-- the members -->
| Member | What it is |
|---|---|
| `comment_id` | the comment's id, and the id of its event |
| `class`, `doc_id` | the document it is on |
| `text` | the words |
| `actor_id` | who wrote them |
| `actor_kind` | what they are: `person`, `agent` or `service`; left out when the caller did not say |
| `actor_for` | whom they acted for; left out when they acted for themselves |
| `version` | the version of the document the comment was written on |
| `business_date` | the business day of the commit |
| `ts` | the moment, in seconds since the epoch |

## The words

- **Plain text.** Nothing in it is read as markup; a page shows it as
  it is.
- **Line breaks are kept**, and a carriage return before one is
  dropped. A tab is kept. Any other control character is refused.
- **Words that are empty once trimmed are refused**, and so are words
  longer than the database's limit for a text, 65,536 characters
  unless the database was made with another (see
  [Opening a database](opening-a-database.md)).
- **A comment is never changed and never removed.** A correction is a
  new comment.

## Who may

<!-- who may -->
| To | A caller needs |
|---|---|
| comment | to see the document as it is stored now |
| read the comments | to see the document, as it is stored or as it was last when it was deleted |
| read the comments past the read rule | the system group `content-administrators`, and a reason, which the log of reads keeps |

- **Seeing is enough.** A comment changes nothing of the document, so
  the update rule and the rights of the variables do not apply to it.
- **A caller who may not see the document** is answered as if there
  were no such document (step 4), and reads no comment (step 5), as
  `events` answers them.
- **A content administrator comments only where the rules let them**:
  `comment` takes no reason.

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| comments on a document they may not see, or one that was deleted | `unknown_document` | |
| gives no words, or only spaces and line breaks | `required` | `/text` |
| gives a control character other than a line break or a tab | `bad_value` | `/text` |
| gives more words than the database's limit for a text | `out_of_range` | `/text` |
| says they read a version the document is no longer at | `stale_version` | |
| sends a key another command used | `key_reused` | |
| comments at another moment than now, with `at` | `not_allowed` | |

## Not here yet

- Who may comment, narrower than who may read: a rule of the class for
  it.
- Replies to a comment, mentions of a person, files on a comment,
  reactions.
- A rule or an action that runs on a comment, and a formula that reads
  comments.
- The words of comments in a SQL view and in the copy for analysts;
  their events are there.
- Taking a comment out, for a reason the law gives.
