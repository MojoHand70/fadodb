# ADR-0078: A decision stands on what the document holds: the version a command saw, who may bring a value again, and what a condition saw

- Status: Accepted; amends ADR-0015 (the order inside a commit; a trigger before a condition), ADR-0026 (the event keeps the version a command saw), ADR-0028 and ADR-0041 (the write right of a triggered variable), ADR-0040 (one more refusal; what a guard may read), ADR-0043 (what a `Refresh` asks; a triggered variable that fails before a condition), ADR-0046 (item 6: where a lookup may be written), ADR-0048 (`versions` in `transition_many`), ADR-0055 (a sixth member that means the same), ADR-0076 (the list that is kept); amended by ADR-0099 ("a history of a table", left out here, is built: every table keeps the history of its rows)
- Date: 2026-11-21

## Context

On 2026-11-18 the owner recalled the requirement the project began
with: "we talked about snapshotting document data at state change. The
reason for that was that state transition condition must survive the
underlying data change. Tomek's eyes or Agent's eyes and the rule saw
some numbers based on which they made a decision." He proposed to
"decide, maybe by formula, when variables are allowed to change by
edit or by rebind", his word for bringing data into a document again.
His earlier product let the tabular data of a document be refreshed by
hand or automatically. He called the subject "the CORE system logic -
how and when numbers are calculated on an active document".

**What stands.** The engine does more than keep a snapshot at a state
change.

- Every commit makes a version, kept as a typed row that is never
  rewritten (ADR-0014). The event of a transition names the version
  before it and the version it made (ADR-0040).
- No value of a document changes because data outside it moved
  (ADR-0015; ADR-0046, item 8).
- A document in a final state works nothing out again (ADR-0039).
- Who may edit which value in which state is a formula already: the
  `rights` of the class (ADR-0028).

**Three kinds of value.** Only the third has the question of a rebind.

| A value is | It changes | Who decides when |
|---|---|---|
| entered by somebody | by an edit | `rights`: a formula for each state and variable |
| worked out from the document's own values | at once, in the same commit | nobody: it cannot drift |
| brought from outside: a table, other documents, the clock, the caller | at the moments the class names | the table below |

| A value is brought | Written | |
|---|---|---|
| once, when the document is created | `default` | built |
| when a value of the document that it reads changes | a computed variable that looks in a table; a rule on `values_change` | built |
| at a state change | `enter:`, `exit:`, `transition:` | built |
| when somebody asks, the owner's manual rebind | `refresh` | built |
| whenever it is read, and kept nowhere | `"mode": "live"` | accepted, not built (ADR-0076) |
| when its source changes, and kept | `table:` | planned (ADR-0015); refused today with `not_supported` |
| at a time | `schedule:` | planned (ADR-0015); refused today |

**Where the requirement does not hold.** The case is Acme. Tess, in
treasury, keeps the table `Limits`: what a buyer of each grade may
spend. Ola writes purchases. Tomek is a manager and approves them.
Piotr is a colleague with no part in it. The purchase, as today's
engine takes it, brings its limit when it is filed, lists `refresh`,
and has the condition `amount <= limit` on `approve`. Played on
2026-11-21 against the engine at 1f75b59:

| | Who | Does | Today |
|---|---|---|---|
| 1 | Ola | writes purchase P-7 of 9,000.00 and files it | version 2, `limit` 10,000.00 |
| 2 | Tomek | reads P-7 | version 2: 9,000.00 against 10,000.00 |
| 3 | Ola | corrects the amount to 9,900.00 | version 3 |
| 4 | Piotr | refreshes `limit`, which the class's `rights` close to all but managers | allowed, version 4 |
| 5 | Tess | raises the limit to 20,000.00 | allowed |
| 6 | Piotr | refreshes the purchase | allowed, version 5, `limit` 20,000.00 |
| 7 | Tomek | approves | allowed, version 6: 9,900.00 against 20,000.00 |

Three things went wrong, and each is of a different kind.

1. **The eyes and the commit were different versions.** Tomek decided
   on version 2 and approved version 5. No command can say which
   version its sender saw. No record speaks of it: this is new work.
2. **A value was brought again after it was looked at, by somebody
   with no part.** Whoever may read a document may refresh it, in any
   state but a final one, and a `write` right on a triggered variable
   is accepted and asks nothing. Decided so in ADR-0043, item 10, by
   me; with the owner's requirement beside it, it is not right.
3. **A condition may look outside, and then nothing keeps what it
   saw.** Had the condition been `amount <= LOOKUP(Limits, limit,
   grade = THIS.grade)`, the 20,000.00 would be nowhere: a guard is not
   stored (ADR-0015), and a table keeps nothing of what a row was
   (ADR-0037). Decided so in ADR-0015, before the first line of code;
   it is the one place that goes against the requirement itself.

The caller this is written for is an agent that reads a document,
thinks for a minute, and acts, with no person watching.

## Decision

1. **The rule.** A decision stands on what the document holds. Whoever
   decides, a person, an agent or a condition, decides on values that
   are on the document, in a version the log can name afterwards. What
   comes from outside is first brought onto the document, at a moment
   the class names, and then decided on. Items 2 to 4 mend the three
   places above. Items 5 and 6 say what the rule means for a list and
   for a value that follows its source.

   The two definitions of the case, whole, as this record would have
   them:

   ```json
   {
     "name": "Limits",
     "columns": {"grade": "text", "limit": "decimal(2)"},
     "key": ["grade"],
     "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
   }
   ```

   ```json
   {
     "name": "Purchase",
     "title": "\"Purchase \" & number",
     "variables": {
       "number": {"type": "text", "required": true},
       "amount": {"type": "decimal", "scale": 2, "required": true},
       "grade":  {"type": "text", "required": true},
       "limit":  {"type": "decimal", "scale": 2,
                  "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
                  "mode": "triggered", "on": ["enter:review", "before:approve", "refresh"]}
     },
     "states": {"initial": "draft", "list": ["draft", "review", "approved"], "final": ["approved"]},
     "transitions": [
       {"name": "file",    "from": "draft",  "to": "review"},
       {"name": "return",  "from": "review", "to": "draft"},
       {"name": "approve", "from": "review", "to": "approved",
        "guard": "NOT ISBLANK(limit) AND amount <= limit"}
     ],
     "access": {
       "read":   "INGROUP(\"everyone\")",
       "create": "INGROUP(\"everyone\")",
       "update": "CREATEDBY() = USER()",
       "delete": "FALSE",
       "transition": {
         "file":    "CREATEDBY() = USER()",
         "return":  "INGROUP(\"managers\")",
         "approve": "INGROUP(\"managers\")"
       }
     },
     "rights": {
       "*":      {"limit": {"write": "FALSE"}},
       "review": {"limit": {"write": ["managers"]}}
     }
   }
   ```

   Two things in it are new. `before:approve` brings the limit when
   somebody asks to approve, before the condition is asked (item 4).
   The `write` right of `limit` says who may bring it again, and in
   which state (item 3). The same people, under this record:

   | | Who | Does | Answer |
   |---|---|---|---|
   | 1 | Ola | writes purchase P-7 of 9,000.00, grade B | version 1 |
   | 2 | Ola | files it | version 2 |
   | 3 | Tomek | reads P-7 | version 2: 9,000.00 against 10,000.00 |
   | 4 | Ola | corrects the amount to 9,900.00 | version 3 |
   | 5 | Tomek | approves, and says he saw version 2 | refused, `stale_version`: the purchase is at version 3 |
   | 6 | Piotr | refreshes `limit` | refused, `forbidden`, variable `limit` |
   | 7 | Tess | lowers the limit of grade B to 5,000.00 | allowed |
   | 8 | Tomek | reads version 3 and approves it | refused, `guard_false`; brought: `limit` 5,000.00 |
   | 9 | Tomek | refreshes `limit` | version 4 |
   | 10 | Ola | corrects the amount to 4,900.00 | version 5 |
   | 11 | Tomek | reads version 5 and approves it | version 6, approved |
   | 12 | Tess | raises the limit of grade B to 20,000.00 | allowed |
   | 13 | Piotr | reads P-7 | approved: 4,900.00 against 5,000.00 |

   | Version | State | `amount` | `limit` | `limit` was worked out |
   |---|---|---|---|---|
   | 1 | draft | 9,000.00 | blank | never |
   | 2 | review | 9,000.00 | 10,000.00 | at version 2 |
   | 3 | review | 9,900.00 | 10,000.00 | at version 2 |
   | 4 | review | 9,900.00 | 5,000.00 | at version 4 |
   | 5 | review | 4,900.00 | 5,000.00 | at version 4 |
   | 6 | approved | 4,900.00 | 5,000.00 | at version 6 |

   The event of version 6 says that Tomek saw version 5, and version 6
   holds the limit the condition saw. Nothing Tess does afterwards
   changes either.

2. **A command says which version it saw** (the owner's ruling of
   2026-11-21). The commands that change one stored document, `set`,
   `reset`, `refresh`, `transition` and `delete`, take a member
   `version`: the version of the document its sender read. In Go it is
   the option `Saw`.

   ```json
   {"class": "Purchase", "id": "P-7", "transition": "approve", "version": 2}
   ```

   ```json
   {"error": {"code": "stale_version",
              "message": "Purchase P-7 is at version 3, and the command saw version 2",
              "hint": "read the document again and decide on what it holds now"}}
   ```

   - **Given, and the document is at that version**: the command goes
     on as it does today.
   - **Given, and the document is at another version**: it is refused
     with `stale_version`, which names the version the document is at,
     and nothing is written. A version the document never had is
     refused the same way.
   - **Left out**: the command acts on the document as it is, as today.
   - **The event keeps it.** `events` gains `seen_version`, shown in
     the view `<Class>_events`: the version the command named, null
     when it named none. So the log says of every change whether its
     sender had read what it changed.
   - **When it is compared.** After the caller is known to read the
     document, so that a caller who may not learns nothing
     (`unknown_document`, as today), and before anything is asked or
     brought. A command sent again with its key is answered as the
     first was (ADR-0026) and is not compared again: a caller that
     repeats a command whose answer was lost must not be refused by
     its own success.
   - **`transition_many`** takes `versions`, a list as long as `ids`
     and in their order, a `null` for a document whose version is not
     named. A document at another version is reported with
     `stale_version` among those that did not move, and the others
     move (ADR-0048).
   - **`preview`** takes `version` and answers the refusal a command
     would get.
   - **What ships sends it.** The tools of the door for agents
     (ADR-0064) say so in their descriptions, the operator's skill
     teaches read, decide, send the version, and the face that ships
     with the engine sends it with every change. An action that a rule queued (ADR-0044), a
     timer and a wake-up (ADR-0047) have read nothing and name none.

3. **Who may bring a value again is the variable's `write` right**
   (question 2). Nobody types a triggered variable: a caller who sets
   one is refused with `read_only`. It changes by being brought again.
   So its `write` right, which the class format takes today and which
   asks nothing, gets the one meaning it can have: who may bring the
   value again, in which state. It is the cell a designer already
   knows, resolved as for any variable: the variable, its section, the
   sections around it, `*`, then the same in the `*` state (ADR-0069).

   | A value is brought again by | Its `write` right is |
   |---|---|
   | a `refresh` that names it | asked of the caller, in the state the document is in; `forbidden`, with the variable and the cell, as for an edit |
   | a `refresh` that names no variable | asked for each variable that lists `refresh`; one the caller may not bring is left as it is; `forbidden` when the class has such variables and the caller may bring none |
   | a `refresh` of another document, which a rule queued | asked of the person whose command queued it (ADR-0044, item 11) |
   | a rule of its own class, inside the commit | not asked: it is the engine's write (ADR-0043, item 4) |
   | a trigger of the document's own move: `enter:`, `exit:`, `transition:`, `before:` | not asked: whoever may make the move brings what the move brings |

   - A variable with no cell behaves as today: whoever may read the
     document may refresh it.
   - `access.update` is not asked. Bringing a value again is no edit;
     a class that wants only editors to do it says so in the cell.
   - A content administrator who gives a reason gets past the cell, as
     past any right (ADR-0052).
   - The bundle carries the rights (ADR-0070), so a face knows whether
     to offer the refresh.

   In the case, `limit` may be brought again by a manager while the
   purchase is in review, and by nobody anywhere else. Step 6 is
   refused; step 9 is taken.

4. **A transition's condition reads the document, and what it needs
   from outside is brought first** (question 3). Two parts.

   **A fifth trigger, `before:<transition>`.** A triggered variable
   that lists it is worked out when somebody asks for that transition:
   after the values the command sets and the recalculation, and before
   the condition is asked. The order inside the commit of a transition
   (amends ADR-0015 and ADR-0040, item 1):

   | | Step |
   |---|---|
   | 1 | the version the command saw (item 2) |
   | 2 | who may: the line of `access` for the transition, and the rights of the values the command sets, over the document as stored |
   | 3 | the values the command sets, and the recalculation |
   | 4 | **the variables that list `before:` this transition**, in the order of the class, and the recalculation after each |
   | 5 | the condition of the transition |
   | 6 | the move; the variables that list `exit:`, `transition:` and `enter:`; the rules; the transitions that fire by themselves; the writes |

   - **When the condition is not TRUE**, nothing is written, as today.
     The refusal carries, under `brought`, each variable brought for
     the condition and what it was, for a caller who may read it:

     ```json
     {"error": {"code": "guard_false",
                "message": "the guard of approve is not TRUE for the document in review",
                "hint": "the guard: NOT ISBLANK(limit) AND amount <= limit",
                "brought": {"limit": "5000.00"}}}
     ```

     The purchase on Tomek's screen still shows 10,000.00, the limit
     of version 2, so the refusal has to say what the condition saw.
   - **When it is TRUE**, the value is kept in the version the move
     makes, and its moment names that version. A variable is worked
     out once in a commit (ADR-0043), so one that lists `before:` is
     not worked out again by an `enter:` of the same move.
   - **When it cannot be brought**, because its formula fails or the
     value is outside its range, the transition is refused with that
     code and the variable. Another trigger leaves the old value and
     goes on (ADR-0043, item 9); a condition must not be asked over a
     number that could not be brought.
   - **`transitions` and `preview` bring it too**, and write nothing.
     A button is offered, or not, by the number the move would see.
   - `STATE()` in its formula is the state the transition leaves.
   - It names a transition that has a name. A transition that fires by
     itself has nobody asking, and its condition reads the document as
     it is stored.
   - A transition that leaves a final state brings nothing, and a
     `before:` that names one is refused when the class is defined
     (ADR-0043, item 8).

   **A `guard` may not look outside.** A query function, `LOOKUP` and
   `QUERY`, written in a guard is refused when the class is defined,
   with `not_allowed` and the hint to give the lookup a variable that
   lists `before:` the transition. A guard reads the values of the
   document, the caller, the clock, the state and the life of the
   document. The caller and the time are on the event; everything else
   the condition read is in a version: the values brought for it in
   the version the move made, all others in the version before, with
   the values the command set.

   Two ways of using it, both right:

   | The designer wants | Writes | The condition decides on |
   |---|---|---|
   | the limit of the day the purchase was filed | `"on": ["enter:review"]` | the number Tomek saw |
   | the limit of the moment of approval | `"on": ["enter:review", "before:approve"]` | a number brought in the same commit, and kept by it |
   | that an invoice number is free | `"on": ["before:file"]`, a count over the class | a count nobody can have changed since: the commits come one after another |

5. **A list that is kept** (built after ADR-0076, and written as that
   record's `QUERY` is). A triggered variable may be worked out by a
   `QUERY`. Its value is a list, kept with the document and with each
   of its versions, as an entered list is (ADR-0014). It is brought at
   the triggers the variable lists, `before:` among them, and brought
   again by whoever its `write` right allows. It is read with the
   engine's eyes (ADR-0046, item 2): one list for everybody, and the
   `read` right of the variable says who sees it. What reads an
   entered list may read a kept one, a condition included.

   The owner's three ways of rebinding tabular data then have these
   homes:

   | In the owner's earlier product | Here |
   |---|---|
   | refreshed automatically | `"mode": "live"`: worked out whenever it is read, kept nowhere (ADR-0076) |
   | refreshed by hand | a triggered variable that lists `refresh`: kept, and brought again by whoever may |
   | the third, which he did not recall | perhaps one of two: brought once (`default`), or brought by the process at a state change or `before:` a move |

   A kept list is copied with every version of its document, and the
   cap of a composite holds for it (ADR-0013). It is for the lines a
   decision stood on. A list to browse is `live`.

6. **A kept value that follows its source** is the trigger `table:` of
   ADR-0015, and its like for a class. It is planned and not built,
   and this record does not build it. Three things about it are fixed
   here, because the rule fixes them.

   - Each document it reaches gets a commit and a version of its own.
     So a command that saw the version before is refused (item 2), and
     nobody approves a number that changed under their eyes.
   - It brings a value only where the variable's `write` right lets it
     be brought again, and never into a document in a final state.
     Whose right is asked is decided with its build; proposed: of
     whoever changed the source, as an action runs as the person whose
     command queued it (ADR-0044, item 11).
   - It is one statement for all the documents it reaches, never one
     for each (ADR-0015, ADR-0020).

## Questions for the owner

All three are answered, each as it was proposed; the owner's words are
in the rulings below. Question 1, whether a command may or must say
which version it saw, is item 2. The other two are kept here as they
were asked, with the other way, so that he can turn one round later.

2. **Who may bring a value again** (item 3). **Answered on
   2026-11-22: the rights of the class.** The case: Piotr refreshes
   the limit of a purchase in review, in step 6.
   - Proposed, the rights of the class: the `write` right of `limit`.
     Edit and rebind are said in one table, by state and by formula,
     which a designer and an agent already read; it says who and when
     together; nothing is added to the class format; a section closes
     many brought values in one cell.
   - The other way, a condition of its own on the variable:
     `"while": "STATE() = \"review\""`. The variable is brought at its
     triggers only while the condition is TRUE. It holds for the
     triggers of a move too, which the rights do not ask. It is one
     more word in the class, and a second place, beside `rights`,
     where "in which state" is said.

3. **A condition that looks outside** (item 4). **Answered on
   2026-11-22: refused when the class is defined.** The case: `approve`
   written with `amount <= LOOKUP(Limits, limit, grade = THIS.grade)`.
   - Proposed, refused when the class is defined, with the mend named.
     Then what a condition saw is on the document by construction, and
     the person who decides sees the number the rule uses. The price:
     a variable for every outside look, and the classes, guides and
     scripts that have such a guard today are written again.
   - The other way, taken as today, and `class check` says in a note
     that what the condition reads is not kept. Nothing written today
     changes; the evidence then depends on a designer heeding a note.

## The owner's rulings

Accepted by him on 2026-11-22: "accept all three and start building
QUERY." Nothing of the record was built when he accepted it.

Of 2026-11-18: the requirement and the direction quoted in the
Context.

Of 2026-11-21, to the question "a command that changes a document may
say which version its sender saw; if the document has moved on, the
command is refused and writes nothing; the event of a move keeps the
version its sender saw, or that it named none; what we ship always
sends it. The other way: every transition must name it, and a caller
that did not look has to say so": **"1. agreed"**. That is item 2.

Of 2026-11-22, to questions 2 and 3 as they stand above, each put to
him with its case, the proposal and the other way: **"2. agreed, 3.
agreed"**. Those are items 3 and 4: the `write` right of a triggered
variable says who may bring its value again, and a guard that looks
in a table or at other documents is refused when the class is defined.

He accepted the record by name on 2026-11-22.

Settled by me, his to veto: the name of the member, `version`, as it
stood in the question put to him, and `versions` for many documents;
the code `stale_version`; that a repeated key is answered before the
version is compared; the column `seen_version`; that a rule's refresh
of its own document asks no right; that a variable with no cell stays
open to whoever may read; the name `before:`; that a value which
cannot be brought before a condition refuses the transition; `brought`
on the refusal; the three fixed points of item 6.

## What is built

Phase 8 builds the record first, in four steps, one pull request each.

| Step | Item | State |
|---|---|---|
| 1 | the version a command saw (item 2) | built |
| 2 | who may bring a value again (item 3) | built |
| 3 | `before:` a transition, and a guard that reads the document alone (item 4) | built |
| 4 | a list that is kept (item 5) | built |

Item 6, a kept value that follows its source, is built for a table by
ADR-0091, which also decides whose right is asked: nobody's. A value
that follows the documents of a class, and one that follows the
clock, are not built.

## Settled while building the version a command saw

Built on 2026-11-25. Each of these is settled by me while building,
working alone on the owner's word of that day, and is his to strike.

1. **A database of v0.7.0 is brought forward, not made again.** The
   Consequences below said that a file of the schema before is made
   again, as at every change so far. With v0.7.0 released there are
   databases that hold what people keep, the owner's own among them.
   So the engine that reads schema 14 opens a database of schema 13
   by adding the column and making the `_events` view of every class
   again, in the transaction that opens it; the documents, the events
   and the definitions are not touched. The engine of v0.7.0 no longer
   opens the file afterwards. A database older than v0.7.0 is refused
   as before. This promises nothing for later changes of the layout:
   each is its own step, and one that cannot be made in place will say
   so.
2. **A version below 1 is no version.** `"version": 0` is refused with
   `bad_value` at `/version`, as a member that cannot be right, before
   the document is looked for. A version the document never had, 99
   when it is at 3, is refused with `stale_version`, as item 2 says.
3. **A create that names a version is refused**, with `not_allowed`:
   a new document has no version anybody read. Only the Go option can
   say it; the command `create` takes no such member.
4. **A batch names each version or none.** `versions` shorter or
   longer than `ids` is refused with `bad_value`, and so is the one
   `version` of a single command given to a batch in Go. In Go the
   list is `SawEach`, with 0 where JSON has `null`.
5. **`preview_delete` takes the version** as `preview` does, and
   `preview_many` takes `versions`: a preview answers what the command
   would be answered. `preview_draft` and `migrate` take none: a draft
   is tried on a document, not decided on, and a migration moves many
   documents and read no one of them.
6. **The status is 409**, with the other refusals that say the
   document is not as the command expects: `wrong_state`, `terminal`,
   `guard_false`.
7. **The key is not part of the comparison, and the version is not
   part of the key's command.** A command sent again with its key is
   answered as the first was even when it names another version: the
   key names the command (ADR-0026).
8. **The event of an action a rule queued, of a timer and of a move
   the document makes by itself carries no `seen_version`**, and
   neither does an event of the caller's own (`emit`): only the event
   of the command itself does.
9. **No face ships with the engine**, so "the face that ships with the
   engine sends it" has nothing to change here: the pages of a face
   come from a folder (ADR-0073). The door for agents says it to every
   agent that connects, each tool describes the member, and the
   operator's skill teaches read, decide, send the version.
10. **The options of a call are no larger for it**, which the gate
    shows as bytes on every path: they are made for every call. The
    version a command saw is held where the version of a file's
    document is held for `OpenFile` (`AtVersion`), since no call takes
    both; the versions of a batch stand behind a pointer where a flag
    stood, which moved beside the two other flags. With eight bytes
    more a commit took 16 bytes more and the listing of transitions was
    1.9% slower, measured; with none, nothing moves.

## Settled while building who may bring a value again

Built on 2026-11-25. Each of these is settled by me while building,
and is the owner's to strike.

11. **A refresh that names none and may bring none says which.** With
    one such variable the refusal is the one a refresh that named it
    would get, with the variable. With several it names them all in
    its message, "piotr may bring none of buy, sell again while the
    document is in kept", and no single variable; the hint is the cell
    of the first.
12. **The words of the refusal are of bringing, not of setting**: "may
    not bring limit again", and the hint says of the write right that
    it "says who may bring its value again", so that a designer who
    reads it knows which meaning of the cell refused.
13. **A content administrator's override is at the place of the
    cell**, `/rights/<state>/<variable>/write`, the place an edit past
    the same cell would name (ADR-0052).
14. **A refresh a rule queued for another document that its
    principal may not bring ends as any refused action does**: the row
    of the outbox dies with `forbidden` and is there to be read. The
    designer gives the right to the people whose commands set the rule
    off, or leaves the variable without a cell.
15. **Nothing is said when a class is defined** about a `write` right
    on a triggered variable that does not list `refresh`: the cell
    then closes nothing, as it did before. Not planned.

## Settled while building what a condition saw

Built on 2026-11-25. Each of these is settled by me while building,
and is the owner's to strike.

16. **A class stored before this was so still runs as it was
    written.** A guard that looks in a table or at other documents is
    refused when a class is defined. A definition that a database
    already holds is compiled as it was taken, as every stored
    definition is, so a database of v0.7.0 opens, its documents move,
    and `due` still lists what a change of a table made due. The next
    version of such a class brings the value first. The other way,
    refusing to open a database until its classes are rewritten, would
    stop a server for a change of its engine.
17. **The hint of the refusal says the mend**, with the transition's
    own name in it: a triggered variable that lists `"before:approve"`.
    For a transition without a name it says to bring the value at the
    entry of the state, or by a refresh, since nothing is brought
    before a transition nobody asks for.
18. **`brought` is behind a pointer in Go**, `*Values` on `Error`, so
    that two refusals still compare with `==`, which programs and this
    repository's own tests do. On the wire it is the object of the
    record.
19. **`brought` names every variable that was brought for the
    transition**, whether the guard reads it or not, in the wire shape
    of its type, and leaves out one whose `read` right does not hold
    for the caller in the state the document is in.
20. **A value that cannot be brought is said in the words of the
    failure**, "limit could not be brought before approve: …", with
    the code of the failure, `div_by_zero`, `out_of_range` and the
    like, and the variable. The listing of transitions shows that code
    for the transition, in the place of `guard_false`.
21. **A command of the outbox that asks for a transition brings too.**
    A rule of another document that moves this one is somebody asking,
    as a caller is. A timer and a transition that fires by its guard
    are not: for a timed transition that has a name, the variables
    that list `before:` it are brought when a caller takes it, and not
    when its time makes the move.
22. **The page does not work such a guard out.** In the bundle
    (ADR-0070) the guard of a transition that anything is brought
    before is marked as the server's, as a guard that looked in a
    table was: the page holds the value of an earlier version. It asks
    `transitions`, or a preview.
23. **The benchmark of the gate** is `BenchmarkTransitionBefore`: a
    transition with a count over 10,000 documents, through an index,
    brought before its guard. Measured on the maintainer's machine:
    62 to 65 µs and 176 allocations, beside 50 to 52 µs and 155 for
    the transition alone, the same number of allocations at 2,000,
    20,000, 25,000 and 60,000 iterations.

## Settled while building a list that is kept

Built on 2026-11-25. Each of these is settled by me while building,
and is the owner's to strike.

24. **A list that is kept says its columns.** The variable is written
    `"type": "rows"` with a `schema`, the columns of its `QUERY` with
    the types they have in the source, and the class is refused when
    the two differ: a column the schema lacks, one of another type,
    one the `QUERY` does not list. The reason is the one a triggered
    variable that reads other documents says its type for (ADR-0046):
    the tables of a class are laid out when a database opens, before
    the class it lists is at hand. The other way, the engine taking
    the columns from the source by itself, would make the layout of
    one class depend on the definition of another.
25. **Its columns are single values.** A column of the source that is
    a list or an object is refused: a list of lists is not kept.
26. **It is asked on the writer, inside the commit**, so it sees what
    a `LOOKUP` of the same commit sees: the documents as stored, and
    in a batch the moves made before this one. It is one statement,
    prepared each time it is asked.
27. **A list longer than a list may be is not kept.** The cap of a
    composite is 10,000 (ADR-0013). An answer longer than that fails
    with `too_many_elements`: a trigger leaves the list as it was and
    records the code as its moment's error, and `before:` a
    transition refuses the transition, as for any value that cannot
    be brought.
28. **`brought` shows the list** on a refusal `guard_false`, as its
    rows, to a caller who may read the variable.
29. **A system column may be listed**, `id`, `title`, `state`,
    `class_version`, `created_at`, `updated_at`, `state_entered_at`,
    with the type a filter gives it; not `version`, whose name the
    table of a list uses for the version of the document that holds
    the list. A row does not carry the id of its document unless the
    `QUERY` lists `id`: a kept list is lines of the document that
    holds it, not pointers.
30. **A transition that fires by itself brings the list too**, when
    the variable lists the entry or the exit of a state: the move's
    own version keeps it.
31. **The moment of a list** stands in the document's row, the columns
    `<name>__asof` and `<name>__error`, as for every triggered value;
    its rows stand in the child table.
32. **What a kept list costs, measured** on the maintainer's machine,
    three runs: a transition that brings a list of 100 rows before its
    guard, found through an index among 10,000 documents, and keeps it,
    takes 0.50 ms and 3,271 allocations
    (`BenchmarkTransitionKeptList`, no benchmark of the gate), beside
    0.06 ms and 176 for a transition that brings one count. The cost is
    the reading of each listed document, whole, as a live list reads
    it, and the writing of the 100 rows with the new version. It is
    inside the 2 ms of a transition for a list of this size, and it
    grows with the list: a list of thousands of rows is brought in
    milliseconds and written again with every version of its document.
    Reading only the columns the list keeps is not built.

## Consequences

- The loop of an agent is read, decide, send with the version; on
  `stale_version`, read again. It never acts on a document it did not
  see, and the log shows that it did not.
- Two people who change different values of one document at once are
  no longer both taken when their faces send the version: the second
  is refused and reads again. Merging the two is the face's work.
- The rights of a class now mean something for every kind of
  variable: who may edit an entered value, who may bring a brought
  value again. The designer's lists show both in the table they have.
- What a condition saw can always be shown, without the designer
  having thought of it. A class says more: one variable for each
  thing a condition needs from outside. That variable is also what a
  person sees beside the button.
- A value brought `before:` a move may differ from the one the person
  saw, when its source moved in between. Both are kept: the version
  the person saw, and the version the move made. A designer who wants
  them to be the same number leaves `before:` out.
- The classes that ship with the engine, the chapters of the manual
  and the scripts of the conformance suite that write a lookup in a
  guard are written again with the step that builds item 4, and the
  designer's skill teaches the new form in the same pull request.
- The engine schema gains a version, for `seen_version`, and a file of
  the schema before is made again, as at every change so far. (Changed
  while building: a database of v0.7.0 is brought forward when it is
  opened; see "Settled while building the version a command saw",
  item 1.)

## What this record leaves out

- **Who may make a move, when the line of `access` looks outside.** A
  line of `access` and a right may still look in a table, and
  `INGROUP` asks for groups, which are outside the document by their
  nature. The event keeps who the caller was, of what kind, and for
  whom (ADR-0050), and not why they were allowed. It is the question
  of item 4 asked about the authority instead of the condition. Not
  decided.
- **The condition of a rule** may look outside, and what it read is
  not kept; the arguments of an action are, in its row of the outbox
  (ADR-0044). Not planned.
- **A history of a table.** A table keeps nothing of what a row was
  (ADR-0037). After item 4 no condition depends on it. Not planned.
- **`before:` for a transition that fires by itself.** Such a
  transition has no name today. The owner ruled on 2026-11-13 that
  every transition has one; the engine record of that ruling is not
  written, and this record does not write it.
- **A computed variable that looks in a table** follows the values of
  its document and not the table, and says no moment (ADR-0015).
  Decided so, and unchanged: a designer who needs the moment makes the
  variable triggered.
- **Which values changed** between the version a command saw and the
  one the document is at. The refusal does not say; the caller reads
  the document, and the history has both rows. Not planned.
- **`schedule:`.** Planned in ADR-0015, and unchanged by this record.

## Performance

No budget changes.

- **The version.** One comparison with the row the commit has read
  already. `seen_version` is one more column of the event's row; the
  allocations of `BenchmarkTransition` are held to what they are
  (ADR-0012), and the full comparison is run before the code is
  pushed.
- **The right of a refresh.** One evaluation of a cell for each
  variable a refresh brings, tens of nanoseconds, and none where the
  class has no cell.
- **`before:`.** A transition of a class with no variable that lists
  it evaluates nothing new: the triggers are indexed when the class is
  compiled (ADR-0043). A variable brought before a condition costs
  what the same lookup costs in a guard today: 10 µs through an index
  among 100,000 documents (ADR-0046). The listing of the transitions
  pays it for each transition that has one, as it pays for a guard
  that looks up today (ADR-0040).
- A benchmark of a transition with one variable brought `before:` it
  joins the gate with the step that builds item 4.
