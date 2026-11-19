# ADR-0074: The engine hands out its list of functions

- Status: Proposed; adds one command to the set of ADR-0055
- Date: 2026-11-19

## Context

A formula calls functions: `ROUND`, `TODAY`, `INGROUP`. The engine
keeps the list of them in one place, its catalogue: 104 functions,
each with its arguments, what it returns, where it may stand and a
sentence on what it answers. The parser reads the catalogue, and the
manual's page [The functions](../manual/functions.md) is written from
it.

Whoever helps somebody write a formula needs the same list.

- A formula editor in a face offers the names as a person types, and
  shows the arguments of the function the cursor is in. The owner asked
  for one on 2026-11-19, for
  [FadoDB Workshop](https://github.com/MojoHand70/fadodb-workshop).
- An agent that designs a class through the door for agents has to
  know what a formula may call before it writes one.

Neither can ask today. A face would have to carry a copy of the list,
which is wrong on the day the engine gains a function. An agent has
the list only when somebody gave it the skills of this repository.

The engine hands out one such list already: `list_actions`, every
action a rule may name.

The case is Acme. Olga, a designer, writes the formula of `vat` in the
class Expense, in Workshop. An assistant of Acme's, an agent, is asked
to design a class for travel requests through the door.

## Decision

1. **A command `list_functions` answers the engine's list of
   functions, whole.** It takes nothing. In Go it is `DB.Functions()`.

2. **Each function is told with what somebody who writes a formula
   needs:**

   | Member | What it is |
   |---|---|
   | `name` | the name, in capitals |
   | `aliases` | the other names it is written by; left out when it has none |
   | `kind` | where it may stand: `pure`, `context`, `lookup`, `query` or `secret` |
   | `category` | what it is for: `logic`, `numeric`, `text`, `date` and so on, as the manual groups them |
   | `signature` | the call as the manual writes it: `ROUND(number[, digits[, method]])` |
   | `params` | the arguments in their order, each with its `name`, its `type`, `optional` when it and those after it may be left out, and `repeat` when it may be given many times |
   | `returns` | the type of what it answers |
   | `description` | a sentence or two on what it answers |
   | `not_built` | true for a function that is part of the language and that this version of the engine refuses; left out otherwise |

   For Olga, typing `CEI` in the formula of `vat`:

   ```json
   {
     "name": "CEILING",
     "aliases": ["CEIL"],
     "kind": "pure",
     "category": "numeric",
     "signature": "CEILING(number[, digits])",
     "params": [
       {"name": "number", "type": "number"},
       {"name": "digits", "type": "number", "optional": true}
     ],
     "returns": "number",
     "description": "Rounds up, toward plus infinity, at the given decimal places. On an exact value the digits give the result's scale."
   }
   ```

3. **The answer tells the kinds too**, under `kinds`: each of the five
   with what it means for where a function may stand. A function of the
   kind `context`, such as `TODAY`, reads who calls and what time it
   is, and may not stand in an auto formula: one the engine works out
   again whenever a value it reads changes.

4. **Every caller may ask.** The list is the engine's and the same for
   every database. It is in the manual, for anybody.

5. **The list is the one the engine itself reads.** The answer is made
   from the catalogue the parser reads, at each call. There is no
   second copy to fall behind, and the list changes only with the
   version of the engine.

6. **The check stays the engine's.** The list is help for writing.
   Whether a formula is right is answered by `validate_class`, with the
   place of each finding, and by nothing else.

## The owner's ruling

None yet. Of 2026-11-19, on what the open face is to have: "a formula
editor with intellisense for calculated variables".

Settled by me, his to veto: everything in this record. Nothing in it
was put to him as a question. The names of the members follow
`list_actions` (`name`, `kind`, `params`, `returns`) and the words of
a class (`description`).

## Consequences

- A face needs no copy of the list, and offers a function on the day
  the engine has it.
- An agent that comes through the door reads the list before it writes
  a formula. The sign above the door says so.
- The catalogue's words are now read by programs. A function's name,
  kind and arguments were a contract already, held by the conformance
  suite; its sentence is written for people and may be reworded.

## What this record leaves out

- **The words of the language that are no functions**: the operators,
  `IF ... THEN ... ELSE`, `AND`, `OR`, `NOT`, `THIS`. A face that
  colours a formula takes them from the chapter
  [The formula language](../manual/formula-language.md). Not planned;
  it comes when a face cannot do without.
- **The list in another language than English.** The engine's own
  words are English everywhere. Not planned.
- **One function by its name**, as a command. The whole list is some
  33,000 bytes, and a program asks for it once.

## Performance

No budget changes. The command reads nothing of the database: it
writes 104 entries from memory.

The program grows by 4,096 bytes, from 15,683,746 to 15,687,842 of the
20,971,520 of ADR-0072, measured as `scripts/sizegate.sh` builds it,
on 2026-11-19.
