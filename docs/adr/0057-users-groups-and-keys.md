# ADR-0057: Who is calling: users, groups and keys, kept in the database

- Status: Proposed, with four questions open for the owner; amends ADR-0027 (the optional package of users, groups and keys is built, with keys and without passwords or sessions; its commands are part of the command set and not routes of their own), ADR-0052 (who keeps the members of a group, and what an administrator may not give themselves), ADR-0055 (nine commands join the table)
- Date: 2026-11-14

## Context

Until now a host program has told the engine who is calling, with every
call (ADR-0027). A server has no host program in front of it: a request
arrives from the network, and the server has to work out who sent it.
Five records left that to this phase:

- ADR-0027: one optional package turns a credential into a principal
  for `fadodb serve`, and the server takes the function that does it.
- ADR-0049 moved the engine's own users, groups and keys here.
- ADR-0050: the package holds a kind for each: person, agent or
  service.
- ADR-0052: with the server the engine keeps its own users and group
  lists, the five system groups exist in every database from its first
  day, database administrators keep users and keys, "and who keeps
  their members is that phase's record".
- ADR-0053, item 5, said that its answer on groups may be asked again
  when the engine knows everybody's groups.

Two rulings of the owner shape this record. On 2026-11-14 he ruled that
the engine knows keys only: a person, an agent and a service each call
with a key, and passwords and a company's sign-on come with the
frontend in Phase 7. And ADR-0056, if he accepts it, keeps everything
that is a customer's in that customer's database.

The case used below is the database of Acme:

| Id | What | Groups | System groups |
|---|---|---|---|
| `olga` | a person | | `administrators` |
| `dan` | a person | | `designers` |
| `ann` | a person | `finance`, `managers` | |
| `ap-agent` | an agent | `finance` | |
| `importer` | a service | `imports` | |

## Decision

1. **A user is a person, an agent or a service that the database knows
   by its id.** `USER()` has always answered an id; a user is who has
   it.

   ```json
   {"id": "ap-agent", "kind": "agent", "name": "Accounts payable agent",
    "groups": ["finance"], "system_groups": [], "disabled": false}
   ```

   - `id` is what every event records. It is any text but a control
     character, it never changes, and it is never given to somebody
     else. The id `fadodb` is the engine's own (ADR-0053) and no user
     has it.
   - `kind` is `person`, `agent` or `service` (ADR-0050), and it is
     required. A host program may leave the kind unsaid; a database
     that keeps its users always knows.
   - `name` is how a screen shows the user. It may be left out.

2. **A group is a name on a user.** A user's groups are a list, and
   `INGROUP("finance")` answers from it as it does today. There is no
   record of a group apart from its members, and no group inside a
   group. The four system groups a host program gives (ADR-0052) are in
   a list of their own, `system_groups`, as they are in a principal; a
   system group's name among `groups` is refused, as it is today.
   `everyone` is in neither list: the engine gives it to every user.

3. **A key is how a user proves who it is.** A user has any number of
   keys, each with a label that says where it is used: "laptop",
   "nightly import". A key is one line of text:

   ```
   fado_7GQ2M4XK0P_3ZD9…
   ```

   It begins `fado_`, so that a tool that looks for secrets in code
   finds it. Then comes the key's id, which names it in lists and is no
   secret, and then 32 random bytes written as text, which are the
   secret. The database keeps the id and a SHA-256 hash of the secret.
   It never keeps the key: the key is shown once, when it is made, and
   a lost key is replaced, not found again.

   Over HTTP a request carries it in one place:

   ```
   POST /v1/transition
   Authorization: Bearer fado_7GQ2M4XK0P_3ZD9…
   ```

   Nothing else is a credential: no cookie, no key in the address, no
   name and password. A key may be given an end, `expires`; without one
   it holds until it is revoked.

   A request with no key, with a key the database does not know, with a
   key that was revoked or has ended, or with the key of a disabled
   user, is refused with `unauthenticated`, in the same words each
   time, so that a guess learns nothing.

4. **No password, no session, no sign-on**, by the owner's ruling. A
   person calls with a key as an agent does, from the command-line
   tool, a script or a notebook. Signing in on a screen comes with the
   frontend in Phase 7, and a company's sign-on with it. A host program
   that has its own sign-in gives the door its own function (item 9)
   and uses nothing of this record.

5. **Database administrators keep users and keys**, with nine commands
   that join the command set under the rules of ADR-0055:

   | Command | Takes | Answers | Who may send it |
   |---|---|---|---|
   | `create_user` | `id`, `kind`, `name`, `groups`, `system_groups` | `user` | an administrator |
   | `set_user` | `id`, and any of `name`, `groups`, `system_groups`, `disabled` | `user` | an administrator |
   | `get_user` | `id` | `user` | every user (question 4) |
   | `list_users` | `group`, `kind` | `users` | every user (question 4) |
   | `user_changes` | `id` | `changes`, oldest first | an administrator; a user, of itself |
   | `create_key` | `user`, `label`, `expires` | `key`, with `secret` this once | an administrator |
   | `list_keys` | `user` | `keys`, never a secret | an administrator; a user, its own |
   | `revoke_key` | `key`, the key's id | nothing, `{}` | an administrator; a user, its own |
   | `whoami` | nothing | `user`, and `for` when the command names one | every user |

   Olga gives the agent a key:

   ```json
   {"user": "ap-agent", "label": "invoice matching", "expires": "2027-11-14"}
   ```

   sent as `create_key`, and answered

   ```json
   {"key": {"id": "7GQ2M4XK0P", "user": "ap-agent", "label": "invoice matching",
            "created_at": 1763107200, "created_by": "olga",
            "expires": "2027-11-14",
            "secret": "fado_7GQ2M4XK0P_3ZD9…"}}
   ```

   `whoami` is for an agent that starts with nothing but a key: it
   answers who the database takes it for, of what kind and in which
   groups, before the agent sends anything that matters.

   The Go API gains the same nine, so that nothing is on the wire that
   a Go program cannot do.

6. **Nobody changes their own groups, and the last administrator
   stays.** Olga, an administrator, puts Dan among the designers and
   Ann in `finance`. She cannot put herself in either: her own lists
   are changed by another administrator. It is the rule the owner gave
   for substitutions (ADR-0053, ruling I: "never to themselves"). A
   command that would leave the database without an active
   administrator is refused.

   What the rule does not stop: Olga can make a user `olga-2`, put it
   in every group and give it a key. The database records that she
   did (item 8). As with a designer who rewrites a rule (ADR-0052,
   item 7), the split does not make it impossible; it makes it a
   deliberate act that is in sight.

7. **The first administrator is made on the machine.** A new database
   has no user. Whoever holds the database on their own machine already
   holds everything in it, since a program that opens it says itself
   who is calling (ADR-0027). So the first user is made there, by the
   command-line tool, on the database and not through a server:

   ```
   fadodb user add olga --kind person --system administrators --db acme.fado
   fadodb key new olga --label laptop --db acme.fado
   ```

   The second line prints the key, once. This is also the way back in
   when every administrator's key is lost. The database records such a
   change as made on the machine, by no user.

8. **Every change to a user or a key is kept.** Who made it, what they
   were, for whom they acted and when, as for a version of a definition
   (ADR-0052, item 7). A user is disabled and never deleted, and a key
   is revoked and never deleted, so that the id in an old event always
   has its user. `user_changes` lists them.

9. **The door takes the function that says who is calling**, as
   ADR-0027 decided: from a request to a principal, or to the refusal
   `unauthenticated`. `fadodb serve` gives it the function of this
   record, which finds the key and builds the principal from the user:
   its id, its kind, its groups and its system groups. The `for` of a
   command (ADR-0055, item 3) is added to it and checked against the
   substitutions as before.

   A host program with its own people gives its own function. The nine
   commands of item 5 are then not offered, and answer
   `unknown_command`: the lists they keep would be read by nobody.

10. **A disabled user and a revoked key are stopped at the next
    command.** Nothing is remembered past the commit that disabled or
    revoked. An open stream of changes of that user is closed
    (ADR-0058). What the user's earlier commits had queued still runs,
    as ADR-0044 decided for every queued action: the step was allowed
    when it was taken.

11. **Where they are kept.** In the database (ADR-0056), in tables of
    the engine's own, which no formula and no `SQL` statement reads,
    like the engine's other tables. A database opened by a host program
    that says who is calling has them empty.

12. **What this record leaves as it is, and what it leaves out.**

    - Groups do not pass with a substitution (ADR-0053, item 5). The
      engine now knows Ann's groups, and Bob acting for Ann still has
      Bob's. The ruling stands until the owner reopens it.
    - Passwords, sessions and sign-on: Phase 7.
    - A key that may do less than its user, such as read only, or some
      classes only. An agent that must do less is a user of its own
      with its own groups, which is what ADR-0050 gave it an id for.
    - Groups inside groups.
    - When a key was last used. It would be a write for every request.
    - A formula that asks about another user, such as whether the
      approver named on a document is a manager.

## Questions for the owner

1. **The word.** Proposed: **user**, for a person, an agent and a
   service alike, since `USER()` has always answered the id of any of
   them. The other words: "caller", which ADR-0049 to ADR-0053 use for
   whoever sends a call, or "account".

2. **May an administrator change their own groups (item 6)?**
   Proposed: no, neither the system groups nor the ordinary ones, with
   the limit item 6 states. The other answer: yes, and the record of
   the change is the guard.

3. **Who makes a key (item 5)?** Proposed: an administrator only; a
   user sees its own keys and may revoke them, so that a key that
   leaked is dead at once. The other answer: a user also makes keys for
   itself, which lets an agent change its own key on a schedule, and
   lets whoever stole a key make more of them.

4. **Who sees the list of users (item 5)?** Proposed: every user sees
   every user's id, name, kind and groups, so that a screen shows a
   name for an id and an agent finds the manager to ask; the keys of a
   user are seen by that user and by administrators. The other answer:
   administrators only, and a class that needs names keeps them in
   documents of its own.

## Consequences

- An agent is set up in two commands, `create_user` and `create_key`,
  and from then on every event it causes says `ap-agent`, an agent.
- `kind` in the log is as true as the administrator who set it. The
  engine still vouches for none of it (ADR-0050).
- A database carries its people with it. Moved to another machine, it
  answers the same keys.
- The command-line tool on a database is the way in for whoever holds
  the machine, so the machine and the database on it are what an
  operator has to guard. ADR-0059 closes the database to the other
  users of the machine.
- Nine commands are added to the table of ADR-0055 and to the Go API.
- An embedder with its own directory is not touched: no table is read
  and no command is offered.

## Performance

A key is checked on every request, inside the 200 µs that ADR-0011
gives the server. The check is one SHA-256 of a short text, compared in
constant time, and one look into what the database holds in memory of
its users and keys, read again after a commit that changed them, as the
substitutions are (ADR-0053). It allocates nothing. `BenchmarkWhoIsCalling`
measures it and joins the benchmark gate; its budget is set from its
first measurement, in the pull request that builds it, and is expected
under 2 µs.
