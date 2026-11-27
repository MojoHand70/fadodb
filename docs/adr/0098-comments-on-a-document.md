# ADR-0098: Comments on a document

- Status: Proposed; amends ADR-0018 (the side object of comments is built), ADR-0033 (its line "Comments stay planned after 1.0" goes), ADR-0026 (a new kind of event, `commented`), ADR-0055 (two commands, `comment` and `comments`, join the table)
- Date: 2026-11-26

## Context

People and agents who work on one document talk about it: an approver
asks why a hotel cost more than the policy allows, the author answers,
an agent that read the receipt says what it could not read. ADR-0018
named comments a side object of a document, beside its variables, and
ADR-0033 left them planned after 1.0.

A face that shows a conversation needs a place to keep it. Three were
weighed on 2026-11-26:

- **A variable of the class**, rows of a text, a person and a moment.
  It is read under the document's rules and needs no engine change. But
  the page writes who said it and when, so a person could write a
  comment in another's name; every comment is a new version of the
  document; and every class has to declare it.
- **A class of comments**, each comment a document pointing at the one
  it is about. Its read rule cannot say "whoever may read that
  document": no function of the formula language asks another
  document's rule. A comment on a document somebody may not read would
  be read by them.
- **The engine keeps them**, beside the document.

The owner's words that day: "yes, the engine keeps comments".

The north star decides the rest: agents are the first callers. An agent
that comments in a loop, with nobody watching, needs to repeat a
command safely, to be told in a code that never changes why it was
refused, and to see its words kept as its own, marked as an agent's.

## Decision

1. **A comment is the engine's, kept beside its document**: its words,
   who wrote them, when, and the version of the document they were
   written on. The engine keeps them in a table of its own,
   `fado_comments`. A comment makes no version of the document: no
   rule runs, no formula is worked out, no action is queued, and the
   document's values, state and version stay as they are.

2. **Two commands.** `comment` writes one; in Go the call `Comment`:

   ```jsonc
   {"class": "Expense", "id": "EXP-104", "text": "Why two nights? The trip was one day."}
   ```

   It answers the comment as it is kept:

   ```jsonc
   {
     "comment": {
       "comment_id": "01M8D2…", "class": "Expense", "doc_id": "EXP-104",
       "text": "Why two nights? The trip was one day.",
       "actor_id": "ann", "actor_kind": "person",
       "version": 4, "business_date": "2026-11-26", "ts": 1795725296
     }
   }
   ```

   `comments` reads those of a document, the oldest first; in Go the
   call `Comments`. It answers `{"comments": [...]}`.

3. **Who reads a comment** is who may read its document: the class's
   read rule over the document as it is stored, or as it was last when
   it was deleted, as `events` reads it (ADR-0049, item 6). Whoever may
   not see the document is answered as `events` answers them: with
   nothing. A content administrator reads past the rule with a reason,
   which the log of reads keeps (ADR-0067).

4. **Who writes a comment** is who may read the document as it is
   stored now. A comment does not change the document, so the update
   rule and the rights of its variables do not apply to it, and a
   document in a final state takes comments too: a note of an auditor
   on an expense that was paid is part of its story. A deleted document
   takes none (`unknown_document`), and nor does one the caller may not
   see. A content administrator comments only where the rules let them:
   `comment` takes no reason.

5. **Who and when are the engine's.** The comment keeps the caller's id,
   the kind of its principal, person, agent or service (ADR-0050), and
   whom it acts for, let through only while a substitution says so
   (ADR-0053); the moment of the commit, and its business day, the one
   given or the server's (ADR-0026). The caller says only the words.

6. **The words** are plain text. Line breaks are kept, and a carriage
   return before one is dropped; other control characters are refused
   with `bad_value`. Words that are empty once trimmed are refused with
   `required`, and words longer than the database's limit for a text
   (ADR-0021) with `out_of_range`. Nothing in them is read as markup.

7. **A comment is never changed and never removed.** A correction is a
   new comment. Taking one out, for a reason the law gives, waits for
   the retention command of ADR-0033, item 7, which is designed and not
   built.

8. **Every comment is an event**, of the new kind `commented`, written
   with it in one transaction: its id is the comment's, its document the
   one commented on, its `before_version` the version the comment was
   written on, and it has no `after_version`. So `events`, the stream of
   changes and its filter by kind (ADR-0058), the view `<Class>_events`
   and the analytical copy (ADR-0089) all hold it, with who and when;
   the words are read with `comments`. `history` lists versions and so
   shows none.

9. **Repeated safely.** `comment` takes the idempotency key of ADR-0026:
   the same key with the same class, document and words is answered
   with the comment that key wrote, and nothing is written again; the
   same key with anything else is refused with `key_reused`. It takes
   `version` too (ADR-0078, item 2): an agent that says which version
   it read is refused with `stale_version` when the document moved on,
   and comments on nothing it did not see. And `for` and
   `business_date`, as every change does. Like every command that
   writes, it refuses `at` (ADR-0042).

10. **The door for agents** offers both, as it offers every command;
    `comment` is a tool that writes, `comments` one that reads.

11. **The table is made when a database is opened**, as any table of the
    engine's is, by an older database too; the schema keeps its number,
    15. An older engine that opens a newer database passes over the
    table and the new kind of event, as it passes over what it does not
    know. `fado_comments` is a name no class or table may take.

## Not here

- Who may comment, narrower than who may read: a rule `access.comment`
  in the class. Today every reader may comment.
- Replies to a comment, mentions of a person, files on a comment,
  reactions.
- A rule or an action that fires on a comment, and a formula that reads
  comments.
- The words of comments in a SQL view and in the analytical copy; the
  events are there.
- Taking a comment out (item 7).

## Consequences

- A conversation about a document is kept by the engine, under its
  document's rules, with who said what and when written by the engine,
  and with an agent's words marked as an agent's.
- The log of events has eleven kinds of event. Whatever counts edits
  (the life of a document, the flow, the paths, throughput) counts only
  the kinds it names, and a comment is none of them.
- One more table in every database, empty until somebody comments.

## Performance

- A comment is one transaction of its own on the writer: the document
  is read once, and one row is written to `fado_comments`, one to
  `events` and one to `event_docs`. It does not pass through a
  document's commit, so the commit's hot path is unchanged and
  allocates nothing more.
- `comments` reads one index, `(class, doc_id, ts)`.
