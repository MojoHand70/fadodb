# Starters

A database that has just been made is empty: it has no classes, no
tables and nobody to work in it. A **starter** is an app ready to
use, made in the database by `fadodb init` at the moment the database
is made. The people come next, made by the database's administrator,
and they can start work the same evening.

The program carries one starter today, `expenses`: an employee files
what they paid for the company, their manager approves it, and
finance pays it back. This chapter is its reference. A test plays
what stands here on each push.

## Making a database with a starter

The four commands of [serving a database](serving-a-database.md),
with the starter named to the first:

```
fadodb init acme.fado --starter expenses
fadodb user add olga --kind person --system administrators --db acme.fado
fadodb key new olga --label laptop --db acme.fado
fadodb serve acme.fado
```

`init` says what it made:

<!-- what init says -->
```
made the database acme.fado with the starter expenses: the app Expenses, the group hr, the group finance, the table Employees, the class Expense
```

- **A starter is made with the database, and only then.** `init`
  takes one; no command sends a starter to a database that exists.
  A database that exists is given the same app by a designer, or an
  agent, through the commands that make it.
- **It is made as the first user is**: on the machine, by no user.
  The definitions it makes say that nobody defined them, and from
  then on they are the database's own, changed as any other.
- **All or nothing.** When the database refuses a command of the
  starter, `init` takes the database back, says which command was
  refused, and leaves no file behind.
- **A name the program does not have** is refused before anything is
  made, and `init` names the starters it has.

## What `expenses` makes

<!-- what expenses makes -->
| What | Name | What it is for |
|---|---|---|
| the app | `Expenses` | What employees paid for the company: approved by their manager, paid back by finance. |
| the group | `hr` | Keeps who works here and who each person's manager is, in the table Employees. |
| the group | `finance` | Reads every expense, and pays back the approved ones. |
| the table | `Employees` | Who each person's manager is. A person is a user of the database, by their id; their name is the user's own. |
| the class | `Expense` | What an employee paid for the company and asks to be paid back. The employee's manager decides it; finance pays it. |

The groups have nobody in them and the table has no rows. The
starter makes no people: the users are the company's, and its
administrator makes them.

The app, the class's home:

<!-- an app -->
```json
{
  "name": "Expenses",
  "label": "Expenses",
  "description": "What employees paid for the company: approved by their manager, paid back by finance."
}
```

The table belongs to no app. Who works here, and for whom, is the
company's, and another app reads it as this one does
([apps](apps.md)).

<!-- the table Employees -->
```json
{
  "name": "Employees",
  "label": "Employees",
  "description": "Who each person's manager is. A person is a user of the database, by their id; their name is the user's own.",
  "columns": {
    "person":  {"type": "text", "label": "Person", "description": "The id of the user who works here."},
    "manager": {"type": "text", "label": "Manager", "description": "The id of the user who decides their expenses."}
  },
  "key": ["person"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"hr\")"}
}
```

<!-- the class Expense -->
```json
{
  "name": "Expense",
  "app": "Expenses",
  "label": "Expense",
  "plural": "Expenses",
  "description": "What an employee paid for the company and asks to be paid back. The employee's manager decides it; finance pays it.",
  "title": "what",
  "variables": {
    "what":       {"type": "text", "required": true, "label": "What", "list": true,
                   "description": "A few words a colleague would understand."},
    "amount":     {"type": "money", "scale": 2, "required": true, "label": "Amount", "list": true,
                   "description": "What the employee is paid back."},
    "employee":   {"type": "text", "default": "IF(ACTINGFOR() <> \"\", ACTINGFOR(), USER())", "label": "Employee", "list": true,
                   "description": "Who paid: the user who filed the expense, or the one an assistant filed it for."},
    "manager":    {"type": "text", "formula": "LOOKUP(Employees, manager, person = THIS.employee)",
                   "mode": "triggered", "on": ["transition:submit"], "label": "Manager", "list": true,
                   "description": "Who decides: the employee's manager in Employees at the moment the expense is submitted."},
    "decided_by": {"type": "text", "label": "Decided by",
                   "description": "Who approved or rejected the expense."}
  },
  "states": {"initial": "draft", "list": ["draft", "submitted", "approved", "rejected", "paid"], "final": ["rejected", "paid"],
             "labels": {"draft": "Draft", "submitted": "Waiting for the manager", "approved": "Approved", "rejected": "Rejected", "paid": "Paid back"},
             "descriptions": {"draft": "Being filled in; only the employee sees it.",
                              "submitted": "The manager decides it.",
                              "approved": "Finance pays it back.",
                              "rejected": "The manager turned it down; it is kept.",
                              "paid": "Paid back, and kept."}},
  "transitions": [
    {"name": "submit",  "from": "draft",     "to": "submitted", "label": "Submit",
     "description": "The employee sends the expense to their manager, who must be in Employees.",
     "guard": "amount > 0 AND NOT ISBLANK(LOOKUP(Employees, manager, person = THIS.employee))"},
    {"name": "approve", "from": "submitted", "to": "approved",  "label": "Approve",
     "description": "The manager accepts the expense for payment."},
    {"name": "reject",  "from": "submitted", "to": "rejected",  "label": "Reject",
     "description": "The manager turns the expense down."},
    {"name": "pay",     "from": "approved",  "to": "paid",      "label": "Pay back",
     "description": "Finance has paid the employee back."}
  ],
  "rules": [
    {"on": "transition", "name": "approve", "do": [{"action": "set", "values": {"decided_by": "USER()"}}]},
    {"on": "transition", "name": "reject",  "do": [{"action": "set", "values": {"decided_by": "USER()"}}]}
  ],
  "access": {
    "read":   "employee = USER() OR employee = ACTINGFOR() OR (NOT ISBLANK(manager) AND (manager = USER() OR manager = ACTINGFOR())) OR INGROUP(\"finance\")",
    "create": "INGROUP(\"everyone\")",
    "update": "(employee = USER() OR employee = ACTINGFOR()) AND STATE() = \"draft\"",
    "delete": "(employee = USER() OR employee = ACTINGFOR()) AND STATE() = \"draft\"",
    "transition": {
      "submit":  "employee = USER() OR employee = ACTINGFOR()",
      "approve": "(manager = USER() OR manager = ACTINGFOR()) AND employee <> USER() AND employee <> ACTINGFOR()",
      "reject":  "manager = USER() OR manager = ACTINGFOR()",
      "pay":     "INGROUP(\"finance\")"
    }
  },
  "rights": {"*": {"employee": {"write": "FALSE"}, "decided_by": {"write": "FALSE"}}}
}
```

How it decides:

- **The manager is looked up when the expense is submitted**, from
  Employees, and kept: `manager` is a
  [triggered variable](triggered-variables.md) worked out at
  `submit`. A person who moves to another team before submitting is
  decided by the new manager; an expense already waiting stays with
  the manager it was sent to.
- **Nobody writes the manager by hand.** It is computed, and a value
  given for it is refused with `read_only`.
- **An employee Employees names no manager for cannot submit**: the
  guard of `submit` is false, and the expense stays a draft.
- **A draft is the employee's alone.** The manager sees an expense
  from the moment it waits for them, finance sees every expense, and
  nobody else sees any. The `read` line asks for a manager before it
  compares one with `ACTINGFOR()`: a draft has no manager yet, and a
  blank equals the blank `ACTINGFOR()` of a caller who acts for
  nobody, so without it every caller would read every draft.
- **Nobody approves their own expense**, not even as their own
  manager, and nobody approves one for the person who filed it.
- **Somebody may act for another**, while a substitution the other
  gave lasts ([acting for somebody](guides/acting-for-somebody.md)):
  a manager's substitute reads and approves for the manager, and an
  assistant, such as an agent, files and submits an employee's
  expenses, which are then the employee's.
- **Once submitted, an expense is not changed**, by the employee or
  anyone: `update` lets the employee through in `draft` only.

## The first evening

Olga is the administrator. She makes the people with her key, five
persons and an agent that works for Jan, and puts two of them into
the groups of the starter:

<!-- the people -->
```
fadodb exec create_user '{"id": "hanna", "kind": "person", "name": "Hanna Lis", "groups": ["hr"]}'
fadodb exec create_user '{"id": "fiona", "kind": "person", "name": "Fiona Kaczmarek", "groups": ["finance"]}'
fadodb exec create_user '{"id": "ewa", "kind": "person", "name": "Ewa Wiśniewska"}'
fadodb exec create_user '{"id": "jan", "kind": "person", "name": "Jan Kowalski"}'
fadodb exec create_user '{"id": "marek", "kind": "person", "name": "Marek Nowak"}'
fadodb exec create_user '{"id": "jan-assistant", "kind": "agent", "name": "Assistant to Jan Kowalski"}'
```

Each of them signs in with a password ([signing in](signing-in.md)),
or is given a key. Hanna, of `hr`, writes who reports to whom:

<!-- the managers -->
```
fadodb exec import_rows '{"table": "Employees", "rows": [{"person": "jan", "manager": "ewa"}, {"person": "marek", "manager": "ewa"}, {"person": "ewa", "manager": "olga"}]}'
```

Then the expenses. Each line is one command, sent by the person who
does it: a face sends it with their session, an agent through
[the door for agents](the-door-for-agents.md), and a terminal with
their key.

<!-- the expenses -->
| Who | Does | Answer |
|---|---|---|
| Jan | files "Hotel in Gdańsk", 410.00 PLN | a draft |
| Ewa | reads it | `unknown_document`: a draft is Jan's alone |
| Jan | writes Marek as its manager | `read_only`: the manager is looked up |
| Jan | submits it | waiting for the manager, `ewa` |
| Marek | approves it | `unknown_document`: he is not Jan's manager |
| Jan | approves it himself | `forbidden` |
| Ewa | approves it | approved, decided by `ewa` |
| Jan | changes its amount to 4100.00 PLN | `forbidden`: it is no longer a draft |
| Fiona | pays it back | paid back |
| Jan | files "Taxi to the airport", 84.00 PLN | a draft |
| Hanna | moves Jan to Marek's team | the row of `jan` names `marek` |
| Jan | submits the taxi | waiting for the manager, `marek` |
| Ewa | approves the taxi | `unknown_document`: she is not Jan's manager any more |
| Marek | approves the taxi | approved, decided by `marek` |
| Marek | files "Train to Kraków", 150.00 PLN, and submits it | waiting for the manager, `ewa` |
| Jan, for Ewa | approves the train | `forbidden`: Ewa has let nobody act for her |
| Ewa | lets Jan act for her on expenses | a substitution, `active` |
| Jan, for Ewa | approves the train | approved, decided by `jan` |
| Jan | lets his assistant act for him on expenses | a substitution, `active` |
| Jan's assistant, for Jan | files "Parking at the airport", 36.00 PLN, and submits it | waiting for the manager, `marek`; the employee is `jan` |
| Fiona | files "Paper for the printer", 12.00 PLN, and submits it | `guard_false`: Employees names no manager of hers |

## Making it your own

The starter's app, table and class are ordinary ones. A designer
changes the class as any other, with its next version
([changing a class](guides/change-a-class.md)): a limit above which
finance approves too, a category, the receipts as
[files](files.md). The documents already filed keep what they hold.

## Not here yet

- **One starter.** More come with the example applications of a later
  phase.
- **No people and no documents in a starter.** Whether a starter may
  bring people and documents to try it out with is not decided.
- **No face.** The people of this chapter work through the commands;
  the pages they use in a browser come from a face, and which face
  goes with a starter is not decided.
- **A starter cannot be added to a database that exists**, and an
  app is not yet exported from one database and brought into another
  ([apps](apps.md#what-an-app-does-not-do-yet)).
- **The organisation's rule-book** (ADR-0060): the classes `Entity`
  and `Look`, which a starter is to give a new database, are not in
  this one.
