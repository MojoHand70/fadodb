# ADR-0066: What the server's budget is measured beside

- Status: Accepted; amends ADR-0011 (what its budget is measured beside)
- Date: 2026-11-17

## Context

ADR-0011 gives the server one budget: it adds under 200 µs at p99 to a
transition over the Go API. Phase 6 built the server, and its campaign
measured the budget for the first time
([docs/perf/v0.6.0.md](../perf/v0.6.0.md)), on the machine every
campaign of this project runs on, which is a virtual machine. One
caller moves one document after another, on one connection that is
kept open:

| How the transition is called | Median | p99 |
|---|---|---|
| The Go API, one call after the other in a loop | 0.055 ms | 0.214 ms |
| `fadodb serve` | 0.318 ms | 0.546 ms |
| For comparison, a request that does nothing, sent to an HTTP server of Go's standard library | 0.149 ms | 0.321 ms |

Beside the loop, the server adds 0.332 ms at p99, and the budget does
not hold. But the third row has no FadoDB in it and is over the budget
already. On this machine no server could hold a budget read so, and on
another machine the same words would measure that machine's loopback
and Go's HTTP as much as they measure FadoDB.

A budget is there to tell when FadoDB has become slower (ADR-0012). It
has to measure what FadoDB does.

So the same transition was made a third way: through the Go API, by a
program that was sent a request for it. The program is an HTTP server
of Go's standard library with nothing of FadoDB's server in it. Three
runs of 200,000 calls a row, on 2026-11-17:

| Run | The Go API, called by a request, median and p99 | `fadodb serve`, median and p99 | What the server adds, median and p99 |
|---|---|---|---|
| 1 | 0.336 ms, 0.581 ms | 0.310 ms, 0.523 ms | -0.026 ms, -0.057 ms |
| 2 | 0.353 ms, 0.628 ms | 0.296 ms, 0.547 ms | -0.057 ms, -0.080 ms |
| 3 | 0.351 ms, 0.625 ms | 0.340 ms, 0.601 ms | -0.012 ms, -0.024 ms |

Two things follow.

- **A transition costs more inside a request than in a loop, whoever
  makes it.** The request alone takes about 0.15 ms and the transition
  in a loop 0.055 ms, and the two together take 0.34 ms. This record
  does not say where the rest goes. It is there without the server.
- **What `fadodb serve` adds to it cannot be told from the noise of
  this machine.** The server does all that the plain program does and
  more, and in the three runs it still came out quicker, by up to
  0.08 ms at p99. That much is the noise between two rows of one run,
  which are measured minutes apart. In two shorter runs the server
  added 0.016 ms and -0.012 ms at the median.

## Decision

1. **The number stands.** 200 µs at p99.
2. **It is measured beside the Go API called by a request.** What the
   server adds is the p99 of a transition sent to `fadodb serve`, less
   the p99 of the same transition made through the Go API by an HTTP
   server of Go's standard library that was sent a request for it. That
   server reads the request, calls `Transition` with the context of the
   request, and answers `{}`. Both are measured in the same run, on the
   same machine and the same database, by one caller on one connection
   that is kept open, with the same body and no certificate.
3. **The campaign of the server measures both and prints the
   difference** (`TestServeCampaign`). Each release publishes it with
   its measurements under `docs/perf/`.
4. **What the machine and Go's HTTP cost is published beside it and
   has no budget**: the row of a request that does nothing. The engine
   cannot change it.
5. **The part of the server that needs no network stays in the
   benchmark gate**, where every pull request is held to it:
   `BenchmarkDoorTransition` beside `BenchmarkExecTransition`.

## The owner's ruling

On 2026-11-17 the owner was shown two readings on one case, a caller
that moves an invoice through the server: to count what FadoDB adds
and leave out what the machine takes for a request, or to leave the
words as they are and judge the budget only on a machine that is not
virtual. He chose the first.

What FadoDB's part is measured beside was settled after his ruling, by
the measurements of this record. He was first shown a figure of
0.155 ms, taken another way, which did not repeat (see the last of the
alternatives). He accepted item 2 with the record, on the same
day.

## Consequences

- The budget judges what FadoDB puts between a request and the Go API:
  the key, the command read from its JSON, the answer written, the line
  of the log and the counts. A change that makes one of them slower
  shows on any machine.
- It promises a caller no time. A caller waits what its machine takes
  for a request with a commit in it; the page of each release says how
  long that is.
- The difference of two p99s is not the p99 of what the server adds to
  each request, since no request is made both ways. It is the measure
  that can be taken. On the machine of the measurements it moves by
  up to 0.07 ms from run to run, so it tells a server that has become
  0.2 ms slower, and does not tell one that has become 0.02 ms slower.
  The benchmark gate tells that one (item 5).
- A certificate is not in it. The campaign serves without one, as it
  did.
- ADR-0011's sentence stays as it is written, and its status line names
  this record.

## Alternatives considered

- **The words as they are, judged on a machine that is not virtual.**
  The budget could then be judged only where such a machine is at hand,
  and it would still count that machine's loopback with the server. Not
  chosen, by the owner's ruling.
- **A larger number, the one this machine measures.** A number bound to
  one machine: it would let FadoDB's own part double unseen.
- **Beside a request that does nothing.** It charges the server with
  the transition itself, and with what a transition costs more inside
  a request.
- **Beside a request that keeps Go's server busy for as long as a
  transition takes in a loop.** This was tried first. Its handler spun
  for 0.053 ms and answered. Three runs gave 0.155 ms, 0.227 ms and
  0.195 ms at p99 for the server, once inside the budget, once outside
  and once at its edge. The figure was not the server's: a handler that
  spins does not cost what a commit costs inside a request, and the
  difference was charged to the server. A plain server that makes the
  transition itself takes as long as `fadodb serve`.

## Performance

The three runs of the table above, on main at `bc4de71` with the
campaign of this record, 200,000 calls a row: the server adds -0.057 ms,
-0.080 ms and -0.024 ms at p99, against a budget of 0.200 ms. The same
runs measured a request that does nothing at 0.148 ms, 0.164 ms and
0.165 ms at the median, and a transition through the Go API in a loop
at 0.054 ms, 0.055 ms and 0.055 ms.

```
FADODB_BENCH_DIR=/path/on/disk FADODB_BENCH_COMMITS=200000 go test -count=1 -timeout 2h -v -run TestServeCampaign ./cmd/fadodb/
```
