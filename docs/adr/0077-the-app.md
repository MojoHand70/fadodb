# ADR-0077: The app, a name for the classes that belong together

- Status: Proposed
- Date: 2026-11-20

## Context

A company does not think in classes. It has its expenses, its help
desk, its purchases, and each of them is several classes that belong
together: the expense and the list of cost centres, the ticket and its
categories. Until now the database knew classes only, one beside the
other.

Three callers need the group.

- A person opens "Expenses" and finds the lists that belong to it. A
  face that shows every class of a database in one row shows an
  inventory, and nobody's place of work.
- A designer makes a class inside something: the owner's words of
  2026-11-20, looking at the first page of FadoDB Workshop: "Where do
  we create apps? We need to create an app first."
- An agent that is told a story of the help desk has to find what the
  help desk already keeps before it builds beside it.

The owner said on 2026-11-13 that the database is to know the app: "I
would add notion of 'App'", and "Class has a home, the others can
refer to it". ADR-0068, item 8, left it out with the words "the face's
for now", which was never his ruling. An app has to be the database's,
because it is to be exported from one database and brought into
another, with or without its documents.

The case is Acme. Dan, a designer, builds Expenses: the class Expense
and the class CostCentre. Ann approves expenses. An assistant of
Acme's, an agent, is asked through the door to add travel requests.

## Decision

1. **An app is a definition of its own**, a name with what it says of
   itself to people:

   ```json
   {
     "name": "Expenses",
     "label": "Expenses",
     "description": "What employees paid for the company, approved and paid back."
   }
   ```

   | Member | What it says | Needed |
   |---|---|---|
   | `name` | the name, as every name of a definition: letters, digits and underscores | always |
   | `label` | what people read in its place | no |
   | `description` | what the app is for | no |
   | `icon`, `color` | how a face shows it, as in the header of a class | no |

   A member it does not have is a finding, as in every definition.

2. **A class says which app it belongs to, in its own definition:**

   ```json
   {
     "name": "Expense",
     "app": "Expenses",
     "label": "Expense",
     "plural": "Expenses",
     "description": "What an employee paid for the company and asks to be paid back.",
     "variables": {
       "what":  {"type": "text", "required": true, "description": "A few words a colleague would understand."},
       "total": {"type": "money", "scale": 2, "description": "What the employee is paid back."}
     },
     "states": {"initial": "draft", "list": ["draft", "paid"], "final": ["paid"]}
   }
   ```

   The home is written on the thing, as the owner ruled on 2026-11-18.
   So a class an agent writes, or one pasted from anywhere, says where
   it belongs; `get_class` and the bundle carry it; and the app does
   not list its classes.

3. **The app is there first.** A class that names an app the database
   does not have is refused with the finding `unknown_app` at `/app`,
   with the hint to define the app or to leave `app` out.
   `validate_class` reports the same.

4. **A class belongs to one app, or to none.** A class without `app`
   is what every class was until now. A database made before apps
   keeps working, and the engine's own class Substitution belongs to
   none.

5. **A class moves to another app by its next version**, as it changes
   in any other way (ADR-0039). No document is touched.

6. **Three commands, and one more member:**

   | Command | Who | What it does |
   |---|---|---|
   | `define_app` | a designer | stores the app, or its next version; the same definition again changes nothing |
   | `get_app` | everybody | answers one app as it was defined, with its version and who defined it |
   | `list_apps` | everybody | answers the names of the apps, in the order they were defined |
   | `list_classes` with `app` | everybody | answers the names of the classes of one app; without `app`, every class as before |

   A name that is no app is refused with `unknown_app`, 404 over HTTP.
   In Go: `DB.DefineApp`, `DB.Apps`, `DB.App`, `DB.ClassesOf`.

7. **Names stay the database's**, as the owner ruled on 2026-11-18. A
   class is `Expense` in every formula, view and command, whichever
   app it belongs to. Two apps cannot each have a class `Category`:
   the cure is a good name, `ExpenseCategory`.

8. **The database keeps the current version of an app**, as of a
   schema, and who defined every version (ADR-0052, item 7):
   `definitions` takes `"what": "app"`.

9. **An app decides nothing.** No right and no rule hangs on it. Who
   may see, make, change and move a document is said by its class.

10. **The door for agents says that there are apps**, on its sign, and
    `define_app` carries "Look before you build" as the tools that
    define a class, a table or a schema do: an agent looks at
    `list_apps` before it makes an app, and puts a class into the app
    it belongs to.

## Questions for the owner

Three things are built as I proposed them on 2026-11-20, so that the
first page of Workshop is not held up. Each is the owner's to turn
round.

1. **Who makes an app: a designer.** Dan makes classes; an app is a
   name and a few words. The other way: an administrator only, and
   Dan waits for Olga before he can begin.
2. **A class with no app is allowed.** The other way: every class
   must have one, and a database made before apps is repaired before
   it opens. A face is free to make a class only inside an app, as
   Workshop does.
3. **Everybody reads every app**, as everybody reads every definition
   (ADR-0051, item 4). I had proposed that a person sees only the apps
   in which they may read a list. The engine cannot say that cheaply:
   whether Ann may read an expense is a formula over each document. A
   face shows every app, and how many documents the person may see in
   each.

## Consequences

- A face has a first page that is somebody's place of work: the apps,
  and in each its lists.
- An agent finds what belongs together, and says where a new class
  belongs.
- The class format has one more word, the command set three more
  commands and one more member (ADR-0055), the store one more table,
  `apps`, whose name no class may take. The engine schema is 13: a
  database made by an earlier pre-release engine is recreated.
- ADR-0068, item 8, said of an app "the face's for now". When this
  record is accepted, that line no longer holds, and ADR-0068 and
  ADR-0055 are marked as amended by it.

## What this record leaves out

- **Removing and renaming an app.** A record of its own, with what
  becomes of the classes that name it.
- **The rest of what the owner asked of an app on 2026-11-18**: the
  tables, schemas, actions and inlets that belong to it; groups of its
  own, which travel with it empty; who keeps an app; exporting an app
  and bringing it into another database, with or without its
  documents, with the numbered copy and the clasp.
- **Words in other languages** for an app's label and description.
- **The order of the classes inside an app**: they come in the order
  they were defined.
- **What refers to what.** Nothing answers which classes of other apps
  a class points at.

## Performance

No hot path is touched. An app is read from memory, as every
definition is; `list_classes` with `app` walks the classes once.
Whether a class's app is there is asked once, when the class is
defined. A new database is 8 KB larger, a table and its index.
