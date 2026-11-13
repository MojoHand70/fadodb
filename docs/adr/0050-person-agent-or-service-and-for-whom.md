# ADR-0050: Person, agent or service, and for whom it acts

- Status: Proposed, with one question open for the owner; amends ADR-0027 (two more facts of a principal), ADR-0026 (the delivered event carries them), ADR-0044 (the outbox row carries them), ADR-0019 (two context functions)
- Date: 2026-11-13

## Context

The host program names the caller on every call: an id, groups, the
administrator's flag and a business day (ADR-0027). The engine writes
the id on every event as the actor, and nothing else about who that
was.

FadoDB is built for agents that run business processes, some with no
person at a screen. The first question anyone asks of such a process is
which of its steps an agent took, and for whom. The log cannot answer:
`agent-7` and `ann` are two ids, and nothing says that one of them is a
program that decides for itself and the other a person, or that the
first was working for the second.

A group does not answer it either. A host can put its agents in a group
`agents` and a rule can say `NOT INGROUP("agents")`, but the log does
not keep groups, and a caller whose host forgot the group passes as a
person.

The same gap exists between people. The first embedder keeps mandates
and substitutions: Bob signs for Ann while she is away. ADR-0027 leaves
that to the host and records the one id it is given, so the log says
`bob` and not for whom.

The owner agreed on 2026-11-11 that this belongs to Phase 5 (ADR-0047,
item 16) and on 2026-11-13 that it is in the phase. Everything below is
proposed here and is his to change. The question at the end is his to
rule.

## Decision

1. **Two more facts of a principal.** The host gives them with the id,
   and the engine trusts them as it trusts the id (ADR-0027):

   - `Kind`: what the caller is. One of `person`, `agent`, `service`,
     or empty when the host does not say. A person is a human who sent
     the command. An agent is a program that decided to send it. A
     service is a program that decides nothing: an import, a schedule,
     an integration. Any other word is refused with `bad_value`.
   - `For`: the id of whoever the caller acts for. Empty when it acts
     for itself.

   So an assistant that files Ann's expenses is the id `agent-7`, the
   kind `agent`, for `ann`. Bob signing for Ann is the id `bob`, the
   kind `person`, for `ann`. An agent that matches invoices for the
   whole company is the id `matcher`, the kind `agent`, for nobody.

2. **A formula can ask.** Two functions join `USER()` and `INGROUP()`,
   and stand wherever `USER()` may: `USERKIND()` gives the kind as
   text, and `ACTINGFOR()` the id acted for, blank when there is none.
   The Go VM and both SQL dialects answer them alike (the browser VM
   when it is built), with cases in `conformance/formula/context.json`.

   ```json
   "access": {
     "read": "owner = USER() OR owner = ACTINGFOR() OR INGROUP(\"finance\")",
     "transition": {
       "approve": "INGROUP(\"managers\") AND (total <= 10000.00 OR USERKIND() = \"person\")"
     }
   }
   ```

   Ann's assistant sees Ann's invoices. A manager's agent approves up
   to 10,000, and above that a person does.

   A step kept for people is written `USERKIND() = "person"`, never
   `USERKIND() <> "agent"`: a caller whose host said nothing has a blank
   kind, and a blank must not pass for a person.

3. **`USER()` is the one who acts.** It stays the id the host gives,
   which is `agent-7` in the first example and not `ann`. The rules are
   asked of that id and those groups. The engine adds nothing: an agent
   has the groups the host gave it, and what it may do for Ann is what
   the class says of `ACTINGFOR()`. Authority reaches an agent because
   a rule or a group says so, and that can be read in the class.

4. **The log keeps both.** Every event gains `actor_kind` and
   `actor_for` beside `actor_id`. A class's `_events` and `_states`
   views show them, and the delivered event of ADR-0026 carries them,
   in the schema under `api/`. The engine's schema becomes 6. So this
   is one statement over a view: which steps of the invoice process an
   agent took, for whom, and which ones a person took.

5. **How a step came about stays a separate fact.** An event already
   says whether a command, a rule's action or a timer made the move
   (ADR-0047). What a rule or a timer does is done as whoever set it
   off (ADR-0044, item 11; ADR-0047, item 12), so the outbox row and
   the wake-up carry that caller's kind and `For` with the id and the
   groups, and their events show them. A step an agent decided is then
   an event whose actor is an agent and whose origin is a command.

6. **The document's own columns stay ids.** `created_by`, `updated_by`
   and `state_entered_by` and the functions that read them (ADR-0047)
   answer ids, as today. A class that needs the kind or the person
   later keeps it in a variable when it happens: an input with the
   default `USERKIND()`, or a rule on the transition that sets
   `approved_for` to `ACTINGFOR()`. Strict four eyes between people,
   where Ann may not approve what her own assistant drafted, is then a
   rule over such a variable.

7. **An administrator** passes these rules as every rule, and the log
   says what it was all the same.

8. **What this record leaves out.**

   - Checking the claim. The engine does not know whether `agent-7` is
     an agent or whether Ann asked it to act. The host says so and
     answers for it, as for the id.
   - What an agent was: its model, its version, the run the command
     belonged to. The command key (ADR-0026) ties an event to the
     caller's own record of the run.
   - A chain: an agent acting for an agent acting for Ann. `For` names
     one id, and the host chooses which.
   - Functions that read the kind of a past step, such as the kind of
     whoever created the document. Item 6 covers the need with what
     exists.

## Details settled while building

Built on 2026-11-13, on the record as proposed, as the third step of
the phase. Question B is assumed as recommended; the other answer
changes `For` into the id of the assistant and `ACTINGFOR()` into a
function that names it, and leaves the kind as built.

- **The names.** `Principal.Kind` and `Principal.For`; the constants
  `ActorPerson`, `ActorAgent` and `ActorService`; the functions
  `USERKIND()` and `ACTINGFOR()`; the columns `actor_kind` and
  `actor_for` on an event, and `principal_kind` and `principal_for` on
  an outbox row.
- **The views.** `_events` shows `actor_kind` and `actor_for`.
  `_states` shows, for the move that began a stay and the one that
  ended it, `entered_by_kind`, `entered_for`, `left_by_kind` and
  `left_for`.
- **An unknown kind is refused everywhere**, by a read as by a command,
  with `bad_value`, so that no formula sees a kind the log would not
  take.
- **An `at` may not read them**, as it may not read `USER()`: the
  moment of a timer is the document's, the same for everyone.
- **No upgrade.** The schema is 6, and a database of schema 5, which is
  what v0.4.0 makes, is refused when opened, as every schema change
  before 1.0 has been.

Measured, in memory: `BenchmarkTransition` keeps its 155 allocations
and takes 128 bytes more a commit, 1.5%, for the two texts in the
context of its formulas. An action through the outbox takes 469
allocations where it took 459, and a timer 404 where it took 394: the
driver allocates for every column a statement returns, and the
statement that reads the due rows returns two more.

## Open question

This one is the owner's to rule. The record assumes the first answer
until he does.

**B. When Ann's assistant acts for Ann, who has acted: the assistant or
Ann?** Take a class written for people: `"read": "owner = USER()"`, and
four eyes on `approve`, `CREATEDBY() <> USER()`.

- *The assistant acts* (items 1 and 3). `USER()` is `agent-7`. The
  class above does not let it in until its author adds `ACTINGFOR()`.
  The log reads "agent-7, an agent, for ann". Ann may approve what her
  assistant drafted, since two different ids are on the document; a
  class that forbids it says so (item 6).
- *Ann acts, through an assistant.* `USER()` is `ann`, the kind is
  `agent`, and a third fact names the assistant. Every class written
  for people works for their assistants at once, with everything Ann
  may do, approvals included; a class keeps a step for people by saying
  `USERKIND() = "person"`. The log reads "ann, through agent-7". Ann
  may not approve what her assistant drafted, since both are `ann`.

Recommended: the assistant acts. What an agent may do is then written
in the class, where it can be read and checked, and nothing passes to
an agent because its author forgot a line. It is also what ADR-0027
says of people: the engine records the id that acted.

## Consequences

- The log says which steps an agent took, for whom, and which a person
  took, from the day a host says so. Events written before, and by a
  host that says nothing, have a blank kind.
- A class can keep a decision for a person and give the preparation to
  an agent, in the formulas it already has.
- A substitution between people is recorded as one: `bob`, for `ann`.
- A host has two more things to state for every caller. The server of
  Phase 6 takes them from its authenticator, and the package of keys
  will hold a kind for each key.
- The engine vouches for none of it. A log that says "a person approved
  this" is as true as the host that said so.
- The catalogue of functions gains two, on three evaluators. The event
  row gains two columns. The class format does not change.

## Performance

Two more values in the context of a formula and two more columns on the
event row. The functions read the context without an allocation, as
`USER()` does. A commit stays inside its budget (ADR-0006), and the
benchmarks of the gate that commit, the outbox among them, hold with a
kind and a `For` given.
