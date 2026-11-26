# ADR-0095: How the stays of a state spread: the middle stay and the slow tenth

- Status: Proposed; amends ADR-0063 (the flow says how the stays of each state spread, for a caller who asks), ADR-0055 (the command `flow` takes one more word, `spread`), ADR-0089 (the first of what it calls the mining views is built)
- Date: 2026-11-26

## Context

The flow of a class (ADR-0063) says of each state how many stays in it
ended in a period, how long they lasted on average, and how long the
longest lasted. A chart of a process draws that, and an agent reads it
before it says where documents wait.

An average does not say where documents wait. In the case of the
manual, three invoices leave `draft` after an hour and one lies there
28 hours. The average is 7 hours and 45 minutes: a time no invoice
took.

ADR-0063 knew it. Its third question, of 2026-11-17, was what the flow
says about waiting. The flow was accepted with the average and the
longest stay; the other way was "the median as well, which is truer
when one invoice waited a year, and costs a sort of the stays of each
state". The plan of the first days names the same among its mining
views: the median and the ninetieth percentile of a stay, and the
states ranked by the latter. ADR-0089 lists them as not built.

Since ADR-0089 the copy keeps the moves of the documents ready, each
with the moment the state it left was entered. The sort that the
median costs is 70 ms there among ten million moves, where the store
takes seconds for the flow alone.

## Decision

1. **A flow says how the stays spread when its caller asks.** The
   command `flow` takes one more word, `"spread": true`; a Go program
   sets `FlowPeriod.Spread`. A flow that does not ask is the flow of
   ADR-0063, in every member and at its cost.

2. **Each state then has a `spread`**, with two numbers:

   ```jsonc
   {
     "name": "draft",
     "initial": true,
     "documents": 0,
     "stays": 4,
     "average_seconds": 27900,
     "longest_seconds": 100800,
     "spread": {
       "middle_seconds": 3600,
       "slow_tenth_seconds": 100800
     }
   }
   ```

   - **The middle stay**: half of the stays lasted no longer.
   - **The slow tenth**: nine stays in ten lasted no longer.

3. **Each is the length of one of the stays.** The timed stays in the
   state that ended in the period, of the documents the caller may
   read, are put in the order of their lengths: the stays the average
   is taken over. Of `n` stays the middle one stands at place
   `(n+1)/2` and the slow tenth at place `(9n+9)/10`, both rounded
   down. Nothing is worked out between two stays, so the number is a
   time some document took, and the two programs cannot differ by a
   rounding. A state no stay ended in says zero for both.

4. **Both programs answer it, and the same.** The store counts the
   stays of each state in the order of their lengths and takes the
   one at each place. The copy asks DuckDB's discrete quantile, with
   the share written as an exact decimal, which takes the value at
   the same place. A test holds the two, and an answer sorted by
   hand, to the same numbers at every number of stays from one to
   forty-five.

5. **It is not given unasked**, for its cost: see Performance. It
   reads every stay of the period and puts them in order.

## Questions for the owner

One, asked on 2026-11-26 on a page of its own, and not answered when
this was written:

1. **Which of the questions about how documents moved the engine
   answers in this phase.** The plan names three that the flow does
   not answer: how the stays spread, the paths documents took, and
   the documents made and finished in each period. Proposed: all
   three, this one first. The other way: this one only, and the other
   two when a screen asks for them. This record builds what both
   answers hold.

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **The names**: the word `spread` in the command and in the answer,
   and `middle_seconds` and `slow_tenth_seconds` in it. The plan's
   words were the median and the ninetieth percentile.
2. **Two fixed places, the half and nine tenths**, and no share that
   the caller chooses.
3. **The place is counted with whole numbers**, the nearest rank, and
   nothing is worked out between two stays.
4. **Every state says it when it is asked for**, with zeros where no
   stay ended, as a state says its average.
5. **A transition says nothing of it.** A stay ends with whichever
   transition takes the document on; how long documents waited before
   one transition and before another of the same state is not told
   apart.

## What is built

- The word `spread` of the command `flow`, and `FlowPeriod.Spread`.
- `FlowSpread`, in the answer of a state.
- The third statement of a flow, for the store and for the copy.

## Consequences

- A chart can show what usually happens in a state and what the
  slowest wait, beside the average, with one more word in the command
  it already sends.
- A caller who does not ask pays nothing and is answered as before.
- `fadodb-core` answers it from the store, where the flow of a large
  class takes seconds already and the spread takes as long again.

## What this record leaves out

- **The paths documents took**, and **the documents made and finished
  in each period**: the other two of question 1.
- **The states ranked by their slow tenth.** The flow lists the states
  in the order of the class; whoever draws it ranks them.
- **A share of the caller's own choosing**, such as the slowest
  hundredth.
- **The flow of a moment in the past** (ADR-0063).

## Performance

Measured on 2026-11-26 through the engine, on disk: 3,400,000
invoices that made ten million moves, 6,565,400 timed stays in two
states. Each flow was asked five times of the program with the copy
and three of the one without, and the middle one is given; three runs
with the copy, which agree.

| The flow | `fadodb`, from the copy | `fadodb-core`, from the store |
|---|---|---|
| as ADR-0063 has it | 12 ms | 18 seconds |
| with how the stays spread | 80 to 87 ms | 40 seconds |

The gate's benchmarks ask no spread, and the statements of a flow that
does not ask are the ones they were.
