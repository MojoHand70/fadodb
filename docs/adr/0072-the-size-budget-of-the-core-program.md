# ADR-0072: The size budget of the core program: 20 MB

- Status: Proposed; amends ADR-0002 and ADR-0012 (the core program may be 20 MB, where it was 15), ADR-0017 (the budget of the lite build)
- Date: 2026-11-18

## Context

ADR-0002 and ADR-0012 gave the core program, the lite build of
`fadodb` without DuckDB, a budget of 15 MB, and `scripts/sizegate.sh`
holds every pull request to it. The budget was set in planning, on
2026-11-05, before the server, the door for agents and the pages of
Phase 7 were designed.

The program grew with the phases: 14.8 MB at v0.6.0, then the words in
other languages, sections, the bundle and the first part of signing in.
On 2026-11-18, after the first part of signing in (#174), it is
15,331,490 bytes, and about 400 KB of the budget is left. Still to come
in Phase 7 are a company's sign-on, the passes of OAuth, the door for
agents at the server and the kit's pages, which the server serves from
inside the program; then five phases.

Writing the first part of signing in also showed what costs most. The
pages drawn with `html/template` made the program 17.1 MB: a template
calls methods by their names, so the linker keeps every method of the
program. The pages written by hand cost about 300 KB with all of
signing in.

## Decision

1. **The core program may be 20 MB**, 20 × 1,048,576 bytes, built as
   `scripts/sizegate.sh` builds it: without cgo, with `-trimpath` and
   `-ldflags '-s -w'`. The gate holds every pull request to it, as it
   held them to 15.
2. **What it measures does not change.** The DuckDB build of Phase 8
   has no size budget, as ADR-0002 and ADR-0017 say; its cost is
   reported with each release.
3. **No package that calls methods by their names in the core**:
   `text/template`, `html/template`, and code of our own that uses
   `reflect.Value.Method` or `MethodByName`. They keep every method of
   the program, whatever it calls. `scripts/depgate.sh` refuses the
   two template packages in the program's dependencies.

## The owner's ruling

Of 2026-11-18: "I think we can raise the budget from 15MB to 20MB. We
are in phase 7, there still is 5 phases to go." The other way was to
keep 15 MB and make room by taking things out.

Settled by me, his to veto: the megabyte of the gate, which counts
1,048,576 bytes as the script always has; and item 3, the lesson of the
first part of signing in.

## Consequences

- About 5.4 MB of room for the rest of Phase 7 and the five phases
  after it. A pull request still says what it adds when it adds much.
- A program of 20 MB downloads in a moment and is smaller than most of
  what a server runs beside it. The image of the container grows with
  it, and is still the tool alone.
- The pages of the performance campaigns before v0.7.0 keep the budget
  they were measured against, 15 MB.

## Performance

The budget itself. The current size, 15,331,490 bytes, is measured by
the gate on every pull request.
