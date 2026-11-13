# ADR-0053: Acting for somebody: a recorded substitution for people and assistants

- Status: Accepted; amends ADR-0027 (the engine keeps who may act for whom and checks it, where ADR-0027 left it to the host), ADR-0050 (the claim of its item 1 is checked)
- Date: 2026-11-13

## Context

ADR-0050 lets a caller say for whom it acts: `agent-7`, an agent, for
`ann`; `bob`, a person, for `ann`. The engine writes it on every event
and a formula reads it with `ACTINGFOR()`. The engine checks nothing:
"the host says so and answers for it" (ADR-0050, item 8), as ADR-0027
decided when the first embedder kept its own substitutions.

So the log can say that Bob approved for Ann, and nothing in the
database says who allowed Bob to, from when, or until when. A careless
host, or an agent that names whoever suits it, acts for anybody.

On 2026-11-13 the owner said that `ACTINGFOR()` is needed, and asked
for "a notion of substitute user in case a user is not available",
which "can be time limited".

Seen from the engine, a substitute and an assistant are the same thing:
somebody acts, for somebody else, because they were allowed to. They
differ in what the one who acts is, a person or an agent, and in how
long it lasts.

The owner read the record with its questions on 2026-11-13, said that
the answers recommended are his own, and accepted it. His rulings are
at the end.

## Decision

1. **A substitution is a record in the database.** It says who may act
   (`by`), for whom (`for`), from when, until when, on which classes,
   who gave it and why.

   - "Bob may act for Ann from 1 December to 14 December."
   - "`agent-7` may act for Ann on `Expense`, until it is withdrawn."

   `until` may be left out, and the substitution then holds until it is
   withdrawn. The classes may be left out, and it then holds for every
   class.

2. **The engine checks the claim on every call.** A caller that says it
   acts for `ann` is let through only while a substitution says so, by
   the engine's clock at the time of the call and for the class the
   call names. Otherwise the call is refused with `forbidden`, a read
   as a command, and the hint says what is missing: no substitution
   lets `bob` act for `ann` on `Invoice` today, and who may give one. A
   caller that acts for nobody is asked nothing.

   - What a commit queued runs as the caller the commit had (ADR-0044,
     item 11; ADR-0047, item 12), with its "for". The substitution is
     asked when the command is given and not again when its action or
     its timer runs: the step was decided while it held.
   - A preview at a moment in the future asks the substitution as of
     that moment.

3. **`USER()` is the one who acts.** This is question B of ADR-0050
   as the owner ruled it, and a substitute gives a second reason for
   it. Bob files an expense. Ann is its approver, with four eyes on
   `approve`: `CREATEDBY() <> USER()`. Ann goes on leave and Bob
   substitutes for her.

   - If `USER()` were `ann`, the rule would see Ann approve what Bob
     filed, and Bob would have approved his own expense.
   - With `USER()` as `bob` and `ACTINGFOR()` as `ann`, the same rule
     refuses him.

   A substitution never hides who acts.

4. **A class says where a substitute is accepted.**

   ```json
   "transition": {
     "approve": "(approver = USER() OR approver = ACTINGFOR()) AND CREATEDBY() <> USER()",
     "sign":    "approver = USER() AND USERKIND() = \"person\""
   }
   ```

   Whoever validly acts for the approver may approve, and not their own
   document. The signature stays personal, and stays a person's. The
   engine has checked the claim (item 2), so the first line is safe to
   write.

5. **Groups do not pass.** Bob acting for Ann has Bob's groups. A rule
   that says `INGROUP("managers")` waits for a manager, and any other
   manager may act. The engine does not know Ann's groups: the host
   gives it the caller's. This may be asked again when the engine keeps
   its own group lists in Phase 6.

6. **Who gives a substitution, and who withdraws it.**

   - The person: Ann gives a substitution for Ann and withdraws it at
     any time.
   - When Ann cannot: a database administrator, never to themselves
     (question I).
   - Nobody passes one on. Bob, acting for Ann, cannot let Carol act
     for Ann, and nobody acts for somebody who acts for somebody
     (ADR-0050, item 8).
   - Ann sees every substitution for her, and Bob every one he holds.

7. **Its form: a document of a class the engine brings.** A
   substitution is a document. It has states, `planned`, `active`,
   `ended` and `withdrawn`, and moves by itself at its `from` and its
   `until`, with the timed transitions of ADR-0047. Its history and its
   events say who gave it and who withdrew it. It is created, listed,
   previewed and withdrawn with the calls every document has, so
   nothing new is added to the command set.

   The class is the engine's. It exists in every database under a
   reserved name, `Substitution`, and a designer cannot redefine it. It
   is the first class the engine brings.

   The check of item 2 reads the dates and not the state, so a
   substitution ends at its `until` whether or not the worker has moved
   the document yet.

8. **A host that keeps its own.** The reason of ADR-0027 holds for an
   embedder with its own directory of substitutions: it would keep them
   twice. Such a host says at `Open` that it answers for "for" itself,
   and the engine records what it is told, as ADR-0050 built. Unless
   the host says so, the engine checks.

9. **What this record leaves out.**

   - A shorter way to write the first line of item 4, one function for
     "is, or validly acts for". It is added when classes show the need.
   - A substitution for some states or transitions only, or up to an
     amount. A class writes that with `ACTINGFOR()` and `USERKIND()`.
   - Which substitution let a step happen. An event names who acted and
     for whom (ADR-0050); the substitution is found from those two ids
     and the time.
   - Absence. The engine knows no calendar. The substitution is the
     fact.
   - Telling Bob that he now acts for Ann. A rule on the class can, as
     on any class, once the owner wants it.

## The owner's rulings

All of 2026-11-13.

**B. When Bob or an assistant acts for Ann, who is `USER()`? The one
who acts** (item 3, and ADR-0050).

**H. Who checks that Bob may act for Ann? The engine, against a
recorded substitution, in this phase** (items 1, 2, 7 and 8). FadoDB
gives the authority to act and keeps the evidence, and "for whom" was
the one fact in the log that nobody checked. The answers not taken:
the host, as before, where the database cannot say who allowed it and
what an agent says of "for" is as good as its host; and the engine
with its own users in Phase 6, which closes Phase 5 sooner.

**I. Who gives a substitution when the person cannot? A database
administrator, never to themselves.** They will keep the users in Phase
6. The substitution says who gave it, and the person sees it and can
withdraw it. The answers not taken: a content administrator, who
already acts past the rules; and nobody, where each document that is
stuck needs a content administrator's override (ADR-0052).

## Consequences

- "For whom" in the log is checked and not only claimed. Who allowed
  it, from when and until when, is a document with a history.
- An assistant needs a substitution before its first act for a person.
  An agent asked to act for Ann asks first with a preview and is told
  what is missing.
- A planned absence is the person's to arrange, an unplanned one a
  database administrator's, and where neither did, a content
  administrator's override frees one document at a time (ADR-0052).
- A class decides where a substitute is accepted. The sample classes
  gain `ACTINGFOR()` where one should be.
- The first embedder keeps its own substitutions, says so at `Open`,
  and nothing changes for it.
- The engine brings a class for the first time: a reserved name, a
  definition that travels with the engine and not with the database's
  designers, and its documents in every database.
- The class format and the catalogue of functions do not change.

## Performance

A call that acts for nobody tests one empty text. A call that acts for
somebody looks the substitution up in memory; the engine keeps the ones
in force there and refreshes them when a commit touches the class. The
benchmark gate allows no allocation more for a caller that acts for
nobody (ADR-0030), and a call with a "for" is measured when this is
built.
