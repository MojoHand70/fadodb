# ADR-0058: The stream of changes: the log read forward, over plain HTTP

- Status: Proposed, with three questions open for the owner; amends ADR-0011 (the stream is carried by an HTTP reply that stays open, and the WebSocket is not built), ADR-0010 and ADR-0049 (how the read rule is asked for an event), ADR-0018 (an entry of the stream is an event and not a document), ADR-0055 (one command joins the table)
- Date: 2026-11-14

## Context

A caller that waits for something to happen has one way to learn of it
today: ask again and again. An agent that matches invoices would send
`query` every minute for the invoices in `review`, and a screen would
send it for every list it shows.

ADR-0011 named a command `subscribe` and a WebSocket to carry it.
ADR-0010 decided that the stream applies the read rule, so that a
subscriber never receives what a query would hide, and ADR-0049 moved
that filter to this phase, with the cases of `conformance/access` it
has to pass.

Facts this record was written from:

- **The log already holds everything a stream would say.** Every commit
  writes its events, in order: `created`, `set`, `transition`,
  `deleted`, `reset`, `refresh`, `migrated`, `action_run`,
  `action_failed` and `custom`, each with the class, the document, the
  states it left and entered, who acted, what they were and for whom,
  and the versions before and after. The log is never cut.
- **Go's standard library has no WebSocket.** The core takes no
  dependency beyond the standard library and the SQLite driver
  (ADR-0002). A WebSocket would be a library added by a record, or the
  protocol written and kept by this project.
- **A stream of changes goes one way**, from the server to the caller.
  Commands go the other way already, as requests.
- **A commit may not get slower.** The benchmark gate allows a
  transition no allocation more than it makes today.
- An action already tells the outside world of an event (ADR-0045): a
  designer writes in a class that a transition calls a service. The
  stream is the other direction. Nothing is written in a class; a
  caller with a key asks to be told. An agent behind a firewall cannot
  be called, and can hold a stream open.

The case used below: `ap-agent`, an agent in `finance`, works the
invoices of Acme that enter `review`.

## Decision

1. **The stream is the log, read forward.** A subscriber is told every
   event from a place in the log onward, in the order of the commits,
   as each is written. The server keeps one thing for a subscriber:
   the place it has reached. It keeps no queue. A subscriber that reads
   slowly is behind in the log and loses nothing, and a subscriber
   that comes back after a day continues where it stopped.

2. **`subscribe` is a command with one object, like every other**
   (ADR-0055), and its answer is a reply that stays open:

   ```
   POST /v1/subscribe
   Authorization: Bearer <the key of ap-agent>
   Content-Type: application/json

   {"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"]}
   ```

   | Field | Meaning |
   |---|---|
   | `classes` | the classes to hear of; left out, every class |
   | `kinds` | the kinds of event; left out, every kind |
   | `to_states` | for a `transition`, the states entered; left out, every state |
   | `after` | the id of an event: the stream begins after it. Left out, it begins now |

3. **An entry is an event, as the command `events` answers it.** The
   reply is `text/event-stream`, the form of server-sent events: every
   entry is a few lines of text, and its `data` is one JSON object.

   ```
   id: 01KA2F8Q7ZJ4M0X3T5V9B1C6DE
   event: change
   data: {"event_id": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE", "class": "Invoice",
   data:  "doc_id": "FV/2026/11/0042", "kind": "transition", "name": "submit",
   data:  "from_state": "draft", "to_state": "review",
   data:  "actor_id": "bob", "actor_kind": "person",
   data:  "before_version": 2, "after_version": 3,
   data:  "business_date": "2026-11-14", "ts": 1763107200}

   ```

   The server writes the object on one `data` line; it is broken here
   to be read. The agent reads it, sends `get` for the invoice and decides. The
   entry carries no values of the document: the agent reads the
   document as it stands when it acts, with the rights it has then.

4. **The read rule decides who hears of an event** (ADR-0010,
   ADR-0049). A subscriber is told of an event when it may read the
   document as the event left it. It is asked of the subscriber as it
   is at that moment: its groups are read again after a commit that
   changed them (ADR-0057).

   - A document that is created out of the subscriber's sight is never
     mentioned, and an event on it tells nothing, not even that it
     exists.
   - A document that an event takes out of the subscriber's sight: an
     invoice whose owner changes from Ann to Bob, under a rule
     `owner = USER()`. Ann is sent one bare entry, `hidden`, with the
     class and the id and nothing else, so that her list drops the row.
     She learns nothing she would not learn by asking for it.
   - A delete is told to whoever could read the document just before
     it.
   - A content administrator hears of every document, as they see every
     document (ADR-0052).

5. **A broken connection loses nothing.** Every entry carries the id of
   its event. A subscriber that connects again gives the last one it
   handled, as `after` or in the header `Last-Event-ID`, which is how
   server-sent events resume, and receives everything since. An event
   is sent at least once, and in order. A subscriber that records the
   last id it handled handles each once.

6. **A stream that says nothing shows it is alive.** Every fifteen
   seconds without an entry the server sends a line that is a comment,
   so that a proxy does not cut the connection and a subscriber knows
   a silent stream from a dead one.

7. **How a stream ends.** The subscriber closes it. Or the server
   does, with a last entry that says why, as a refusal of ADR-0055:
   `unauthenticated` when the key was revoked or the user disabled
   (ADR-0057, item 10), `closed` when the server stops.

8. **No WebSocket.** The stream needs one direction, and an HTTP reply
   that stays open gives it with what the standard library has. It
   passes the proxies that pass HTTP, `curl` shows it, and a browser
   reads it. The WebSocket of ADR-0011 would also carry commands on the
   same connection; a command is a request already, and nothing needs
   both on one connection. It can be added by a record if something
   ever does.

9. **A commit pays nothing for the stream.** A commit that ends leaves
   a mark that the log grew, without allocating and without waiting for
   anybody. The streams read the log after it, on their own
   connections. With no subscriber a commit does what it does today.

10. **The Go API gains the same call**, so that a host program hears
    of changes without a server, and the door uses it. The architecture
    page drew it as `Subscribe`.

11. **The suite.** `conformance/access` gains, for each rule and each
    caller, the events that reach the caller's stream, as ADR-0049,
    item 14, asked. The cases run through the Go API and through the
    server (ADR-0055, item 11).

12. **What this record leaves out.**

    - The document inside an entry (question 2).
    - A condition on the values of a document, such as invoices over
      10,000. The subscriber reads the document and decides.
    - Changes of definitions and of users. They are not events of the
      log. A frontend that keeps a class in memory will want to hear
      that it changed; that is Phase 7's.
    - How a tool of the door for agents hears of changes. That is the
      record of that door.

## Questions for the owner

1. **A plain HTTP stream and no WebSocket (item 8)?** Proposed: yes.
   The roadmap's words for the phase change from "over HTTP and
   WebSocket" to "over HTTP". The other answer: a WebSocket, by a
   library taken into the core or by code of our own.

2. **Is an entry the event alone (item 3), or the event with the
   document after it?** Proposed: the event alone. The other answer
   saves the subscriber one `get`, and has to say which document is
   meant when three commits follow each other before the first entry
   is read, and to apply the rights on each variable for each
   subscriber. It can be added later as a field of `subscribe` without
   changing what is built now.

3. **A document that leaves a subscriber's sight: one bare `hidden`
   entry (item 4), or silence?** Proposed: the bare entry. With
   silence, the row stays on Ann's screen until she asks again.

## Consequences

- An agent that waits costs one open connection and no polling. It is
  told within milliseconds of the commit.
- An agent that was stopped for a night does not have to work out what
  it missed: it gives its last id and the log tells it.
- The roadmap and the architecture page stop promising a WebSocket.
  ADR-0011's title keeps the word; its status line says what changed.
- A server holds one connection for each stream, and the limits of
  ADR-0059 bound their number.
- The read rule is asked once for each event and each subscriber that
  the filter of item 2 lets through. A stream on a busy class with
  many subscribers is work in proportion to both; it is done after
  the commit and never inside it.
- The frontend of Phase 7 reads the same stream in a browser.

## Performance

A commit: no allocation and no measurable time added, held by the
benchmark gate as it stands (`BenchmarkTransition`).

The stream: the time from the end of a commit to its entry on the
wire, with one subscriber and with a hundred, is measured in the
campaign of the phase's last step and published in
`docs/perf/v0.6.0.md`. Its budget is set from the first measurement, in
the pull request that builds the stream, and is expected under 5 ms at
p99 on one machine.
