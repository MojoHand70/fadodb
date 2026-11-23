# ADR-0086: A group is a record of its own

- Status: Proposed; amends ADR-0057 (item 2: a group is no longer only a name on a user) and ADR-0055 (six commands join the table). The owner chose it on 2026-11-24 and asked for it built; every item below is for him to veto.
- Date: 2026-11-24

## Context

ADR-0057, item 2, made a group a name on a user: "A user's groups
are a list, and `INGROUP("finance")` answers from it as it does
today. There is no record of a group apart from its members." So the
engine has no list of groups. A screen that shows them, as the Groups
page of FadoDB Studio does (its ADR-0014), reads every user and
gathers the names; and a group cannot be there before somebody is in
it, nor say what it is for.

On 2026-11-21 the owner proposed System Tables, tables the engine
keeps itself and every reader reads as any table: "Maybe it is a good
idea to actually introduce a notion of System Tables which hold:
Apps, Classes, Users, Groups etc." On 2026-11-24, looking at the
Groups page, he asked: "Group is first class engine object, isn't
it?" It is not. Two ways were put to him, with a case: an `auditors`
group, "may read every expense, changes nothing", that a designer
names in a rule (`INGROUP("auditors")`) before anybody is put in it.

- A. The engine lists the names it finds on the users. Nobody writes
  the list. `auditors` is not there until somebody is in it, and has
  no place for what it is for.
- B. A group is made on its own: a name that never changes, which
  `INGROUP` names, a label and a description that may change.
  Membership stays on the user.

His answer: "B, write the engine ADR and implement this autonomous."

An agent is the first caller of the engine. An agent that puts
somebody in a group, or writes a rule that names one, should read
what the group is for, as it reads the description of a class or a
table before it uses it (the designer's skill, "Look before you
build").

## Decision

1. **A group is a record**: its `name`, its `label`, its
   `description`, and when and by whom it was made. The name is what
   a user's `groups` and `INGROUP` name. It never changes: a rule
   that names the group keeps naming it. The label is how a screen
   shows it, and the description says what it is for; both change.
   A group's name follows the rule a user's group has followed since
   ADR-0057: a text without a control character, not empty, and not
   the name of a system group or `everyone`.

2. **Membership stays on the user.** A user's `groups` is the list it
   is today, `INGROUP` answers from it as before, and `list_users`
   narrows to a group as before. There is still no group inside a
   group.

3. **Every group a user is in has a record.** A group that
   `create_user` or `set_user` puts somebody in, and that has no
   record, gets one in the same commit: no label, no description, made
   by the same caller. So nothing that works today stops working, and
   the list of groups never misses one a user is in. A database made
   before this record gets a record for every group its users are in
   when it is opened, made by nobody.

4. **A group may have nobody in it.** `create_group` makes one before
   anybody is put in it.

5. **The commands.** Making, changing and removing a group are a
   database administrator's, as making and changing a user are.
   Reading groups is every user's, as reading users is.

   | Command | Takes | Answers | Who may |
   |---|---|---|---|
   | `create_group` | `name`, and `label`, `description` | `group` | an administrator |
   | `set_group` | `name`, and any of `label`, `description` | `group` | an administrator |
   | `get_group` | `name` | `group` | every user |
   | `list_groups` | | `groups` | every user |
   | `remove_group` | `name` | | an administrator |
   | `group_changes` | `name` | `changes` | an administrator |

   A group is answered with how many users are in it, the disabled
   ones among them, under `members`; who they are is `list_users` with
   the group. `list_groups` answers the groups in the order of their
   names.

6. **A group is removed only when nobody is in it.** `remove_group`
   refuses a group somebody is in, with `not_allowed`, and says to take
   its members off first. A rule that names a removed group is not
   changed: `INGROUP` answers false for it, as it does for any name
   nobody is in. A group of that name may be made again.

7. **Every change to a group is kept**: made, changed or removed, by
   whom, of what kind, for whom and when, with the group as the change
   left it, as every change to a user is (ADR-0057, item 8).
   `group_changes` lists them, oldest first.

8. **A code is new**, `unknown_group`: the database has no group of
   that name. It is answered with 404, as `unknown_user` is.

9. **The system groups and `everyone` are not records.** They are the
   engine's own (ADR-0052), the same in every database, and
   `list_groups` does not answer them.

10. **The door for agents offers the six commands** as it offers the
    commands of users.

11. **System Tables are not built here.** When the engine has them
    (the owner's proposal of 2026-11-21), the System Table of groups
    reads these records. Until then `list_groups` is how a screen, a
    program and an agent read them.

## Consequences

- A screen and an agent read the groups of a server in one call, with
  what each is for, and can make one before anybody is in it.
- The engine keeps two tables more, `fado_groups` and
  `fado_group_changes`, made when a database is opened, as the tables
  of the users are.
- A group cannot be renamed: its name is in the rules that name it,
  and the engine does not rewrite a rule. Its label changes instead.
- A program that never calls the new commands sees nothing change,
  but the records the engine now makes when a user is put in a group.
