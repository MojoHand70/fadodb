# ADR-0058: The stream of changes: the log read forward, over plain HTTP

- Status: Accepted; amends ADR-0011 (the stream is carried by an HTTP reply that stays open, and the WebSocket is not built), ADR-0010 and ADR-0049 (how the read rule is asked for an event), ADR-0018 (an entry of the stream is an event and not a document), ADR-0055 (one command joins the table); amended by ADR-0067 (a content administrator hears past the read rule only with a reason, and is recorded); and by ADR-0064 (`after` takes `start`, the place before the first event of the log; a caller that cannot hold a stream open waits with `wait_for_changes`)
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
     expense waits for Ann's approval, under a `read` rule that names
     its owner and its approver, and the approver is changed to Celina
     before Ann touched it. Ann is told nothing, by the owner's
     ruling 3. It is for her as if she had never had a right to the
     document, which is what ADR-0049, item 3, says of a document a
     caller may not read: hidden means absent. So the one rule above
     has no exception. A list Ann holds keeps the row until she asks
     again, and then the expense is absent for her.
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
   ever does, and the owner expects that 2.0 will add it (ruling 1).

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

    - The document inside an entry (ruling 2).
    - A condition on the values of a document, such as invoices over
      10,000. The subscriber reads the document and decides.
    - Changes of definitions and of users. They are not events of the
      log. A frontend that keeps a class in memory will want to hear
      that it changed; that is Phase 7's.
    - How a tool of the door for agents hears of changes. That is the
      record of that door.

## Settled while building the stream

The stream is built in two parts. The first is the engine's: the Go
call of item 10, with the place of items 1 and 5, the read rule of
item 4, the mark of item 9, and the suite of item 11 through the Go
API. The address on the server, the form on the wire, the sign of
life, how a stream ends and the limit on their number (items 2, 3, 6
and 7) follow with the door.

- **The order of the stream is the order in which the events were
  written**, which is the order of the commits. The id of an event is
  not used for it: a clock that is set back would put a later event
  first.
- **A stream that begins now begins after the last event of the log**
  as the stream is opened. `after` names an event by its id, and an id
  the log does not have is refused with `bad_value`.
- **The filter is checked when the stream is opened.** A class the
  database does not have is `unknown_class`. A kind that is none, and
  a state that no class the stream hears of has, are `bad_value`: a
  stream that can never say anything would stay silent, and its caller
  would not know why.
- **Which version the read rule is asked over.** The version the event
  left; for a delete, the version before it; for an event that names
  no version, the document as it is now. The rule is the class's as it
  is now, as for the read of a version (ADR-0049).
- **A commit wakes the streams and does nothing else for them.** Every
  commit of the store ends in one function, which tells each open
  stream without waiting for any. A test holds that the statement
  stands in that one place, and another that a commit allocates the
  same with eight streams open as with none.
- **A stream is heard as oneself.** A caller that names somebody it
  acts for (ADR-0053) is refused with `not_supported`: who may act for
  whom is asked class by class when a command is sent, and a stream is
  of every class and of no command.
- **A stream follows its caller.** A stream can be given a function
  that says who its caller is now (`Follow`). It asks the function
  when the log has events its filter lets through, after it has read
  them and before it judges who hears of them, so that a stream
  follows the groups of its user as item 4 says and no event is judged
  for a caller as it was before the event. The door gives the function
  that asks whose the key is; a program that names its callers itself
  gives its own. The first part had a call that told a stream its
  caller "from now on" (`As`). It was replaced with the door: a caller
  told that way was judged too early for the events already read.
- **Until the door serves it, `Subscribe` is a call and no command.**
  It joins the table of ADR-0055 with its address.
- **The suite asks what a caller heard** with a step of its own,
  `heard`: the kind and the document of each event the caller's stream
  was told, in order. `conformance/access/rules.json` asks it of the
  owner of a claim, of another owner, of HR, of an administrator, of a
  stranger and of nobody.

## Settled while building the door of the stream

The second part, of 2026-11-15: the address on the server, the form on
the wire, the sign of life, how a stream ends and the limit on their
number (items 2, 3, 6 and 7).

- **`subscribe` is a row of the table of commands**, with the mark
  that its answer is a stream. Its object is read as every object is:
  a member it does not have is refused with `bad_json` and its place.
  `Exec`, which answers one object, refuses the command with
  `not_supported` and says to call `Subscribe`. A caller that says for
  whom it acts is passed to the engine, which refuses it in its own
  words.
- **The reply** has the status 200 and the kind `text/event-stream`.
  Its first line is a comment, `: the stream is open`, written before
  anything happened: a caller that has read it knows that every event
  written from then on reaches it. An entry is three lines and an
  empty one: `id:` with the id of the event, `event: change`, and
  `data:` with the event as one JSON object on one line.
- **`after` and `Last-Event-ID` (item 5).** When a request has both,
  the header counts. A reader of server-sent events that connects
  again sends the request it sent the first time, with the object it
  had then, and adds the header.
- **The sign of life (item 6)** is the comment `: nothing new`, every
  fifteen seconds without an entry.
- **The last entry (item 7)** is of the kind `end`, and its data is a
  refusal: `{"error": {"code": …, "message": …}}`. It has no id, so
  that the last id a reader holds is still that of the last event. It
  is not named `error`: a browser's reader of server-sent events uses
  that name for a connection that broke. Three codes end a stream:
  `unauthenticated`, `closed`, and `internal` for a stream that failed
  in the engine, whose words go to the server's log.
- **When the key is asked again.** When there is an event to tell, as
  the engine's part settled, and at each sign of life. So a key that
  was revoked ends a stream before its next event, which is not told,
  and a silent stream within fifteen seconds.
- **The limit.** A door holds 256 streams, `limits.streams` of
  ADR-0059. One beyond them is refused with `busy` at once, and not
  after the second a command waits: a stream stays open for as long
  as its caller listens. A stream takes no place among the commands
  that run, and the time one command may run does not end it.
- **A caller that takes nothing of what it is sent for thirty seconds
  is cut**, so that one that went away without a word holds no
  stream.
- **Ending the streams is the program's.** A stream does not end by
  itself, so a program that waits for its requests to end would wait
  for its streams. The door ends them when it is told to
  (`EndStreams`), each with the last entry `closed`, and refuses the
  ones asked for after that. `fadodb serve` tells it when it is told
  to stop, before it waits for the commands.
- **A stream is one request.** The door tells of it once, when it
  ended: the status 200, the code the server ended it with, the time
  until it was open, how long it stayed open and how many events it
  told. `fadodb serve` writes that as the line of the stream and
  counts the streams that are open, `fadodb_streams`.
- **The suite through the server (item 11).** The step `heard` runs
  through the HTTP door too. A stream on the wire arrives when it
  arrives, so the runner writes a mark that every caller reads and
  takes what the stream told before it.

### What the measurement found, and what was changed in the engine

The first part read the log for each stream: after a commit every
stream sent its own query for the new events, and one more for the
version of the document the read rule is asked over. The commit did
nothing for them, and allocated the same, which is what its test
held. But a hundred streams reading at once stood in the way of the
next commit inside the storage: a commit took 11 ms at the median
with a hundred streams open, where it takes 0.12 ms with none.

The log is now read once for all the streams.

- **A tail of the log.** The engine holds the last 1,024 events of
  the log in memory while a stream is open. After a commit the first
  stream to wake reads the new events into the tail, and the others
  take them from it. The version of the document is read once for
  each event, when a caller that is no content administrator first
  asks, and serves every stream.
- **A commit leaves a count** beside the mark of item 9: one number
  that goes up, without allocating. A stream at the end of the log
  asks the database again only after the count moved.
- **A stream that is behind the tail reads the log itself**, as every
  stream did before, until it has caught up. So a caller that comes
  back after a day costs what it cost, and holds nobody up.
- **What a caller is given is its own.** An event of the tail is
  every stream's, so `Next` answers a copy.

Measured in the engine on the machine below, with a writer that
commits every millisecond, a commit took at the median 140 µs with no
stream, 141 µs with one, 145 µs with ten and 151 µs with a hundred. A
test holds that fifty streams read the log from the database once for
each commit, and not at all when no commit ended.

## The owner's rulings

All of 2026-11-14. The owner read the three questions, each with a
case, and ruled on each. The first two are the answers the record
proposed. The third is the other answer.

**1. A plain HTTP stream and no WebSocket? Yes** (item 8), with his
words: "I can agree, but I think we will want to add Websocket in
2.0." The roadmap's words for the phase change from "over HTTP and
WebSocket" to "over HTTP". A WebSocket in 2.0 is what he expects. It
is not planned work, and a record of its own comes before it.

**2. Is an entry the event alone, or the event with the document after
it? The event alone** (item 3). The agent reads the document when it
acts, as it stands then and with the rights the agent has then. The
answer not taken saves the subscriber one `get`, and has to say which
document is meant when three commits follow each other before the
first entry is read, and to apply the rights on each variable for each
subscriber. It can be added later as a field of `subscribe` without
changing what is built now.

**3. A document that leaves a subscriber's sight: one bare `hidden`
entry, or silence? Silence** (item 4). The record had proposed the
bare entry, so that a list drops the row. The owner: "My answer is
'nothing'. Just as if she didn't have rights to the document." So no
entry of the stream says that a document went out of sight, and the
stream has one kind of entry, `change`.

The record's first case for this question was an invoice whose owner
changes from Ann to Bob. The owner answered that whoever was once the
editor of a document sees it in every later state, so a class written
well does not take that invoice from Ann. The `read` rule of a class
says so today by naming whoever has had a part: who created the
document, its owner, its approver. The case of item 4 is therefore of
somebody who never had a part. Whether the engine should hold his rule
without the designer writing it is a question of rights that he has
not ruled, and a record of its own.

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
- A list that is kept up to date from the stream can show a document
  its holder may no longer read, until the list is asked again
  (ruling 3). The frontend asks again when it shows a list; an agent
  that works a document reads it first and finds it absent.

## Performance

A commit: no allocation and no measurable time added, held by the
benchmark gate as it stands (`BenchmarkTransition`).

The stream: the time from the end of a commit to its entry on the
wire, with one subscriber and with a hundred, is measured in the
campaign of the phase's last step and published in
`docs/perf/v0.6.0.md`. Its budget is set from the first measurement, in
the pull request that builds the stream, and is expected under 5 ms at
p99 on one machine.

### Measured on 2026-11-15, and the budget

`TestStreamCampaign` measures it: a server on this machine, each
subscriber on a connection of its own with a filter that lets every
event through and a read rule that is asked for every event, and the
time from the moment a commit returned to its caller to the moment
the subscriber had read the entry. 5,000 commits for each row, four
runs, the slowest of them here. The machine is an Intel Core Ultra 9
275HX, 24 threads, under WSL2 with kernel 6.18, the database on ext4
on a VHD on NVMe.

| Subscribers | The writer | Commits/s | p50 | p99 | Longest |
|---|---|---|---|---|---|
| 1 | commits every millisecond | 650 to 680 | 0.25 ms | 0.84 ms | 1.7 ms |
| 100 | commits every millisecond | 760 to 770 | 1.09 ms | 3.12 ms | 7.2 ms |
| 1 | never pauses | 10,400 to 11,100 | 0.53 ms | 1.69 ms | 4.1 ms |
| 100 | never pauses | 4,700 to 6,300 | 3.72 ms | 12.81 ms | 25.2 ms |

**The budget of the stream is 5 ms at p99**, from the end of a commit
to the entry read by a subscriber on the same machine, with one
subscriber and with a hundred, under a writer that commits every
millisecond. Both rows hold it. The campaign of the phase's last step
measures it again for `docs/perf/v0.6.0.md`.

The two rows of a writer that never pauses are outside the budget and
are here to be known. A hundred subscribers are then sent over half a
million entries a second, and the hundred readers of the test run on
the same machine as the server: the entries wait behind each other,
and the writer has less of the machine. In the engine alone, with no
server, 20,000 commits of such a writer took 1.6 s with no stream and
1.9 to 2.2 s with one stream as with a hundred. A commit does nothing
for a stream, but reading the log while it is written takes part of
the machine, as any reader of the database does.
