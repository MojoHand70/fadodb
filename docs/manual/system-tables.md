# The system tables

Every database has the app `System`, the engine's own, and four tables
in it that say what the database holds: its apps, its classes, its
users and its groups. A formula, a page and an agent read them as they
read any [table](tables.md). Nobody writes them: the engine keeps
their rows itself, and they are what the database holds at the moment
they are read.

They are for what needs to know what the server has: a start page that
shows the apps as tiles, an expense that names its approver as a
person, an agent that looks for the people of a group before it asks
one of them.

## What there is

<!-- the tables -->
| Table | Label | One row for each | Its key |
|---|---|---|---|
| `SystemApps` | Apps | app | `name` |
| `SystemClasses` | Classes | class | `name` |
| `SystemUsers` | Users | user | `id` |
| `SystemGroups` | Groups | group | `name` |

`list_tables` lists them after the database's own tables, and
`list_tables` with `"app": "System"` lists them alone. `get_table`
reads the definition of one back; the engine defined it, and
`defined.by` says `fadodb`.

## The columns

<!-- the columns -->
| Table | Column | Type | What it holds |
|---|---|---|---|
| `SystemApps` | `name` | text | the name of the app |
| `SystemApps` | `label` | text | its label |
| `SystemApps` | `description` | text | its description |
| `SystemApps` | `icon` | text | its icon, a face's to draw |
| `SystemApps` | `color` | text | its colour, a face's to draw |
| `SystemApps` | `version` | int | the version of its definition |
| `SystemApps` | `created_at` | datetime | when it was first defined |
| `SystemApps` | `updated_at` | datetime | when its last version was defined |
| `SystemClasses` | `name` | text | the name of the class |
| `SystemClasses` | `app` | text | the app it belongs to; blank for none |
| `SystemClasses` | `label` | text | its label |
| `SystemClasses` | `description` | text | its description |
| `SystemClasses` | `version` | int | the version of its definition |
| `SystemClasses` | `created_at` | datetime | when it was first defined |
| `SystemClasses` | `updated_at` | datetime | when its last version was defined |
| `SystemUsers` | `id` | text | the id of the user, which `USER()` answers |
| `SystemUsers` | `kind` | text | `person`, `agent` or `service` |
| `SystemUsers` | `name` | text | how a screen shows the user |
| `SystemUsers` | `email` | text | a person's company e-mail; blank for none |
| `SystemUsers` | `groups` | json | the user's groups, a list of names |
| `SystemUsers` | `system_groups` | json | the system groups the user is in |
| `SystemUsers` | `disabled` | bool | `TRUE` for a user who may no longer call |
| `SystemUsers` | `created_at` | datetime | when the user was made |
| `SystemUsers` | `updated_at` | datetime | when the user was last changed |
| `SystemGroups` | `name` | text | the name `INGROUP` names |
| `SystemGroups` | `label` | text | its label |
| `SystemGroups` | `description` | text | what it is for |
| `SystemGroups` | `members` | int | how many users are in it, disabled ones too |
| `SystemGroups` | `created_at` | datetime | when its record was made |
| `SystemGroups` | `updated_at` | datetime | when it was last changed |

The rows are the ones the commands answer: `list_apps` and `get_app`,
`list_classes` and `get_class`, `list_users`, `list_groups`. A user
who is disabled is a row of `SystemUsers`, as `list_users` lists them.
A group is a row of `SystemGroups` once it has its record (see
[Naming the caller](naming-the-caller.md)); the system groups are
not.

## Reading them

At Acme, Olga administers the server, Dan designs the classes, Ann
files expenses and Marek approves them. Marek is in the group
`approvers`. Dan wants each expense to say who approves it by name,
not by an id, and the start page to show the apps.

<!-- the expense -->
```json
{
  "name": "Expense",
  "label": "Expense",
  "variables": {
    "amount":        {"type": "money(2)", "required": true, "label": "Amount"},
    "approver":      {"type": "text", "required": true, "label": "Approver",
                      "description": "The id of the user who approves the expense."},
    "approver_name": {"type": "text", "mode": "live", "label": "Approver's name",
                      "formula": "LOOKUP(SystemUsers, name, id = THIS.approver)"},
    "approvers":     {"type": "int", "mode": "live", "label": "Approvers in the company",
                      "formula": "LOOKUP(SystemGroups, members, name = \"approvers\")"}
  },
  "states": {"initial": "draft", "list": ["draft", "submitted", "approved"]},
  "transitions": [
    {"name": "submit", "from": "draft", "to": "submitted"},
    {"name": "approve", "from": "submitted", "to": "approved", "guard": "USER() = approver"}
  ],
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}
```

<!-- the start page -->
```json
{
  "name": "Start",
  "label": "Start page",
  "variables": {
    "apps": {"mode": "live", "label": "Apps",
             "formula": "QUERY(SystemApps, {name, label, icon}, name <> \"System\", {label})"}
  },
  "states": {"initial": "shown", "list": ["shown"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "FALSE", "delete": "FALSE"}
}
```

What happens:

<!-- what happens -->
| Step | Who | What | What the database answers |
|---|---|---|---|
| 1 | Olga | makes Ann (`clerks`) and Marek Nowak (`approvers`) | two rows of `SystemUsers`; `SystemGroups` has `approvers` with 1 member and `clerks` with 1 |
| 2 | Ann | files an expense of 120.00 PLN with `approver` `marek` | `approver_name` is `Marek Nowak`, `approvers` is 1 |
| 3 | Olga | changes Marek's name to Marek Nowak-Lis | the next reading of the expense says `Marek Nowak-Lis` |
| 4 | Olga | puts Ann into `approvers` as well | `approvers` is 2 |
| 5 | Dan | defines the apps Expenses and Helpdesk | the start page lists Expenses and Helpdesk, by their labels, without System |

- **A value is read when the formula is worked out.** The two
  variables of the expense are `live`, so they are worked out each
  time the document is read: step 3 shows the new name at once. A
  variable worked out in a commit keeps what the table said at that
  commit, as a value from any table does; the next commit of the
  document reads the table again.
- **No row is kept from before.** A small table of the database's own
  is kept in memory for the formulas that read it (see
  [how fast a lookup is](tables.md#how-fast-a-lookup-is)); a system
  table never is, so a formula never reads a user who is gone or a
  name that was changed.
- **Everybody may read them**, as `list_users` and `list_groups`
  answer everybody: the read rule of each is `TRUE`.

## What is refused

<!-- what is refused -->
| What | Refused with |
|---|---|
| `import_rows` or `delete_rows` on a system table, a content administrator's too | `read_only` |
| `remove_table` of a system table | `read_only` |
| a rule whose `table.insert`, `table.update` or `table.delete` names a system table | `not_allowed`, when the class is defined |
| a triggered variable that lists `table:SystemUsers`, or any system table | `not_supported`, when the class is defined |
| a table or a class named `SystemApps`, `SystemClasses`, `SystemUsers` or `SystemGroups` | `reserved_name` |

To change what a system table says, change what it shows: `define_app`,
`define_class`, `create_user`, `set_user`, `create_group`,
`set_group`. The app `System` is defined as any app is: a designer may
give it other words with `define_app`; its name and its tables stay
the engine's.

A database made before the engine had system tables gets them the
first time it is opened. One where a designer made an app `System`
keeps it with its words. One whose own table or class took the name of
a system table keeps it, and that system table is left out of it.

## Not here yet

- A value kept by a triggered variable that follows a system table,
  `table:SystemUsers`: refused for now.
- Tables of the other things a database holds: schemas, tables,
  actions, inlets.
- Whether an app is `hidden`, shown to designers only.
