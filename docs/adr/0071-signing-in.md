# ADR-0071: Signing in: passwords, company sign-on, sessions and passes for agents

- Status: Accepted; amends ADR-0057 (a password, a session and a pass join the key as ways to say who calls; a user gains an e-mail and a second step), ADR-0053 (item 5: an agent a person connects has that person's groups while it acts for them), ADR-0059 (the settings gain the address of the server, the company sign-on and the lengths of sessions), ADR-0064 (an agent signs in at `/mcp` with a pass)
- Date: 2026-11-18

## Context

ADR-0057 gave the database its own users, groups and keys, and left
"passwords, sessions and sign-on" to Phase 7 by the owner's ruling. A
person calls today as an agent does, with a key from a terminal. The
pages of Phase 7 need a person to sign in on a screen, and ADR-0064
put the door for agents at the server's own address, `/mcp`, with the
signing in of this phase: the protocol's way to sign in at an address
is OAuth.

On 2026-11-17 the owner ruled the scope of this record: people sign in
to `fadodb serve` itself; a password is kept with the standard
library's hash; a browser keeps a session; Microsoft and Google
sign-on; OAuth for `/mcp`.

The words of this record, each with one example:

- **Company sign-on** (OpenID Connect). Ewa clicks "Sign in with
  Microsoft", and Microsoft hands the server a signed note: this is
  Ewa. The server never sees her Microsoft password.
- **A session.** After Ewa signs in, her browser holds a pass, a
  cookie, so that the next page does not ask again.
- **OAuth.** How Claude gets a pass to act for Ann without ever seeing
  her password. A window opens on Acme's server; Ann signs in and is
  asked "Claude wants to act for you. Allow?"; Claude gets a pass that
  ends within an hour, is renewed, and is taken back when Ann wants.

Three premises were checked before the owner ruled.

- **Passwords are needed beside company sign-on.** A firm may have no
  Microsoft or Google; the first ten minutes with a starter work
  without setting either up; and the first administrator is made on
  the machine.
- **Signing in belongs to the server, though screens are the face's**
  (ADR-0069). The people are kept in the customer's database
  (ADR-0056, ADR-0057), and a face is a client of the server as an
  agent is. So there is one place where a person proves who they are,
  and passes for each kind of client.
- **The page where a person signs in, and the page that asks "Allow?",
  are the server's own.** A password is typed only where it is
  checked, and the protocol has the server that gives a pass ask for
  it.

What the protocol asks was read on 2026-11-18 from its revisions of
2025-11-25 and 2026-07-28, and from Claude's pages on connectors: a
server publishes where its passes come from (RFC 9728) and the
authority that gives them publishes how (RFC 8414); a client proves
the code it was given with PKCE (S256); a pass is asked for one
address (RFC 8707) and accepted only there; a client names itself by a
document it publishes, its Client ID Metadata Document, and the
revision of 2026-07-28 deprecates registering clients on the fly.
Claude uses its published document when the authority accepts clients
without a secret, and sends people back to
`https://claude.ai/api/mcp/auth_callback`, or to an address on the
machine for Claude Code.

The case is Acme: Olga, an administrator; Dan, a designer; Ann, in
finance and an approver; Ewa, who joins Acme, which uses Microsoft
365; and Ann's Claude.

## Decision

1. **`fadodb serve` signs people in and gives out passes.** A person
   proves who they are with a password or with the company's sign-on,
   and is given one of three passes:

   | The client | Its pass | How it carries it |
   |---|---|---|
   | the pages `fadodb serve` itself serves, the kit of step 5 | a session | a cookie |
   | every other client: an agent at `/mcp`, FadoFace or a page served from elsewhere, an app on a phone | an OAuth pass | `Authorization: Bearer fadoat_…` |
   | a program or a service | a key, as ADR-0057 | `Authorization: Bearer fado_…` |

   Every one is kept in the database as a hash, never as itself, and
   every one ends when its user is disabled.

2. **A password.** The database keeps its hash, PBKDF2 with SHA-256 of
   Go's standard library, 600,000 rounds as OWASP advises, a salt of 16
   random bytes, with the rounds kept beside it so that they can rise.
   A check takes 60 ms on one core, measured three times on the
   development machine on 2026-11-18, and runs outside the writer's
   turn.

   - At least 12 characters and at most 256, and no other rule, as
     NIST advises.
   - A wrong name or password is answered in the same words, whatever
     was wrong, and sign-ins that keep failing for one user or from
     one address are slowed.
   - Ann changes her own, giving the old one. Nobody sets another
     person's password: Olga makes a link that lets Ann set hers, good
     once and for a day. On the machine, `fadodb user password olga`
     sets the first one, and is the way back in when every
     administrator's is lost (ADR-0057, item 7).

3. **A second step for a password** (the owner's ruling 3). A code of
   six digits from an authenticator app (TOTP, RFC 6238), which the
   standard library makes.

   - Required for the system groups `administrators` and `designers`,
     and offered to everyone else. Company sign-on brings the company's
     own second step, and the server asks none after it.
   - Ann sets it up on the server's own page, which shows the secret
     once, with ten codes for when the phone is lost, each good once.
     The secret never travels in a command, so that it never stays in
     an agent's conversation.
   - Olga clears Ann's second step when Ann has lost her phone and her
     codes; Ann then sets it up again at her next sign-in.

4. **Company sign-on** (OpenID Connect). The operator's settings name
   each provider; its secret is found as every secret is (ADR-0059,
   item 5):

   ```json
   {
     "url": "https://fado.acme.example",
     "signon": {
       "microsoft": {
         "label": "Sign in with Microsoft",
         "issuer": "https://login.microsoftonline.com/7d1e…/v2.0",
         "client_id": "3f2a…"
       }
     }
   }
   ```

   - **An administrator adds a person first** (the owner's ruling 2):
     Ewa's id, her groups and her company e-mail. The first time she
     signs in with Microsoft and Microsoft says the e-mail is hers, the
     server ties her Microsoft account to her user, and from then on
     knows her by Microsoft's id for her and not by the e-mail, which
     can pass to somebody else. Nobody gets in for being in Acme's
     Microsoft: an account without a user is answered in the same
     words as a wrong password.
   - **Groups stay the database's own** (ADR-0057, item 6). Groups read
     from Microsoft wait.
   - The server checks the signed note as the standard says: the
     signature against the provider's published keys, the issuer, that
     the note is for this server, that it has not ended, and that it
     answers the sign-in the server began.

5. **A session**, for the pages the server serves. The cookie
   `fado_session` is sent back only to the server and only over HTTPS,
   no script on a page reads it, and a page of another site cannot make
   a browser send it. A command sent with it must come from a page of
   the server itself, which the browser says in the request.

   - It ends after 8 hours unused, or after 30 days, whichever comes
     first; the settings may make both shorter.
   - Ann sees her sessions and ends any; Olga ends anybody's. Signing
     out ends the one in use.

6. **OAuth: the server gives passes for its own database.** It follows
   OAuth 2.1 as the protocol asks:

   - **How Ann connects Claude.** Claude asks `/mcp` and is told where
     passes come from. It sends Ann's browser to the server, where she
     signs in and reads: "Claude (claude.ai) wants to act for you in
     this database. Allow?" She allows it, and Claude is given a pass
     for `/mcp`, good for one hour, and a pass that renews it.
   - **A pass is for one address.** A pass for `/mcp` is refused at
     `/v1/`, and the other way: an agent's pass does not open what a
     face's does.
   - **The renewing pass changes each time it is used.** One used twice
     means it was stolen: every pass of that connection ends. A
     connection unused for 30 days ends.
   - **A client is known by the document it publishes**, its Client ID
     Metadata Document, which is how Claude names itself; or it is
     added by an administrator, as FadoFace or an app of Acme's would
     be. A client is never registered on the fly. The server fetches a
     client's document only over HTTPS, small and quickly, and never
     from an address inside the network.
   - **Only the code with its proof** (PKCE, S256) is taken. No other
     way to get a pass is, and a pass never travels in an address.
   - Ann sees her connections and ends any; Olga ends anybody's.

7. **Who Ann's Claude is** (the owner's ruling 1). An agent of its
   own, acting for Ann.

   - When Ann allows it the first time, the server makes the user
     `ann~claude.ai`, an agent named "Claude for Ann", and a
     substitution (ADR-0053) that lets it act for Ann, given by Ann.
     Withdrawing the substitution, or ending the connection, ends both.
   - `USER()` is the agent and `ACTINGFOR()` is Ann. The log says
     "Claude, for Ann", and a substitution never hides who acts
     (ADR-0053, item 3).
   - **While it acts for Ann it has Ann's groups**, as they are at each
     command, and never more. This amends ADR-0053, item 5, for an
     agent a person connects. Bob, who substitutes for Ann, keeps his
     own groups, as ruled.
   - A rule that names a person lets it through only where the class
     accepts a substitute, as ADR-0053, item 4, has it:

     ```json
     "transition": {
       "approve": "INGROUP(\"approvers\") AND CREATEDBY() <> USER()",
       "sign":    "(approver = USER() OR approver = ACTINGFOR()) AND USERKIND() = \"person\""
     }
     ```

     Ann's Claude approves what Ann may approve, through her group,
     and never signs: the signature is a person's.

8. **Who calls, as the door works it out** (amends ADR-0057, items 3
   and 9). A request carries a key, a pass or, from the server's own
   pages, a session, and nothing else. The function of ADR-0057, item
   9, finds whichever it carries and builds the caller from the user.
   Without one, or with one that ended, was revoked or belongs to a
   disabled user, the request is refused with `unauthenticated`, in
   the same words each time; at `/mcp` the refusal also says where
   passes come from, as the protocol asks.

9. **The server's own addresses**, beside the commands, as `/v1/health`
   is:

   | Address | What it is |
   |---|---|
   | `/.well-known/oauth-protected-resource/mcp`, and the same for `/v1` | where passes for that address come from (RFC 9728) |
   | `/.well-known/oauth-authorization-server` | how passes are given (RFC 8414) |
   | `/auth/signin`, `/auth/signout` | the page where a person signs in, and signing out |
   | `/auth/callback` | where the company's sign-on sends a person back |
   | `/auth/authorize`, `/auth/token`, `/auth/revoke` | OAuth's own |
   | `/auth/account` | Ann's page: her password, her second step, her sessions and her connections |

   The pages are plain HTML, without a script, in English; their words
   in other languages are a later record's.

10. **What an administrator does through commands.** Make a link to
    set a password; clear a second step; list and end a user's sessions
    and connections; add, list and remove the clients the database
    knows; list the sign-ins. A user's e-mail joins `create_user` and
    `set_user`. The commands are named when they are built, by the rule
    of ADR-0055, item 2, and join the table. The door for agents does
    not offer the link to set a password, as it does not offer
    `create_key` (ADR-0064, ruling 2).

11. **Everything is recorded.** Each sign-in, and each one refused,
    with the user, the way, the moment and the address it came from;
    each session, connection and pass made and ended; each password and
    second step set or cleared, with who did it. Nothing is deleted, as
    a key is never deleted (ADR-0057, item 8).

12. **The settings** (amends ADR-0059, item 4) gain `url`, the address
    people and clients reach the server at, from which every page, pass
    and cookie is named; `signon`, the providers of item 4; and the
    lengths of sessions and passes. Without `url` the server signs in
    nobody but on the machine itself, at `http://localhost`. A
    provider's name and a connection's name are one list of names, so
    that a secret is never found for the wrong one.

13. **No new dependency.** The hash, the codes, the check of a
    provider's signature, random bytes and HTTP are Go's standard
    library.

14. **Built in three pull requests, each after the one before it is
    merged:** passwords, the second step and sessions, with the pages;
    company sign-on; OAuth, with the agent of item 7. The door at
    `/mcp` (step 6) is built on the third.

15. **What this record leaves out.**
    - Groups read from the company's directory.
    - A user made by a first sign-on.
    - Passkeys.
    - A connection that may do less than its person, such as reading
      only; an agent that must do less is a user of its own, as
      ADR-0057, item 12, has it.
    - Claude connecting a company's people without the "Allow?" page
      (its Enterprise Managed Auth).
    - Registering clients on the fly.
    - The words of the server's pages in other languages.
    - An agent that calls with a key and acts for Ann through a
      substitution keeps its own groups, as before: item 7 is for an
      agent Ann connected herself.

## The owner's rulings

Of 2026-11-18, put in the chat on one page with Acme's case; he
answered "agreed".

1. **Ann's Claude is an agent of its own, acting for Ann** (item 7),
   with Ann's groups while it acts for her. The other way was Claude as
   Ann, `USER()` being `ann` with `USERKIND()` `agent`: simpler, and
   every rule written for Ann, the personal ones too, worked for her
   Claude.
2. **An administrator adds a person first** (item 4), and the first
   sign-in ties the company account to the user. The other way was a
   user made for anyone of acme.com at their first sign-in, with no
   groups.
3. **The second step for a password comes in Phase 7** (item 3),
   required for administrators and designers. The other way was
   passwords alone in Phase 7, administrators advised to use company
   sign-on.

Settled by me, each his to veto: at least 12 characters and no other
rule; the same words for every failed sign-in, and slowing; the first
password on the machine or through a link; sessions of 8 hours unused
and 30 days at most; passes of one hour, renewed by a pass that
changes; no registration on the fly; Claude's Enterprise Managed Auth
left out. Settled while writing, his to veto too: the agent of item 7
has Ann's system groups as well as her groups, so that Dan's Claude
designs the class of the north star as Dan may; and its id is the
person's, a `~` and the client's host.

## Settled while building

The first of the three pull requests built passwords, the second step,
sessions and the pages (items 2, 3, 5, 9 and 11, and the part of items
8, 10 and 12 that is theirs). Settled by me while building, each the
owner's to strike:

1. **The first password is set through a link, on the machine too.**
   `fadodb user password olga` prints a link to the server's page,
   good once and for a day, so that a password is typed only where it
   is checked and never at a terminal. `--url` gives the server's
   address in the link.
2. **Sign-ins that keep failing wait** from the fifth failure for one
   user and the twentieth from one address, within a quarter of an
   hour, which many people of one office share: one second, then twice
   as long after each failure, up to a quarter of an hour. A wait is
   refused with `too_many_attempts`, which says how long.
3. **A person signs in with the id of their user.** The e-mail comes
   with the company's sign-on, the next pull request.
4. **The page shows the key of the second step as text and as an
   `otpauth://` link**, which authenticator apps take; a picture to
   scan waits, since the standard library draws none.
5. **The codes.** Ten codes for a lost phone, of ten characters each,
   kept as hashes; a code from the app holds once and one step either
   side of the moment; five wrong codes end the sign-in under way.
6. **An administrator clears anybody's second step, her own among
   them**, and sets it up again at her next sign-in; a person whose
   groups require none clears their own on the account page.
7. **Who is refused, and how.** A person only signs in with a
   password: an agent and a service are refused in the same words as a
   wrong password, and no link is made for them. A link that sets a
   password ends every session of the person; changing the password on
   the account page ends every session but the one it is changed in.
8. **The tokens** of a session and of a link are written as a key is,
   `fados_` and `fadol_`, an id and a secret of which the database
   keeps a hash. The moment a session was last used is kept in memory
   and written at most every five minutes, so that a request is no
   write.
9. **A form of the pages, and a command sent with a session, are taken
   only from the server's own origin**, as the browser says in `Origin`,
   or in `Sec-Fetch-Site` when it sends no `Origin`. The pages forbid
   being framed, send no referrer to another site (item 36), and allow
   no script.
10. **The commands** are `create_password_link`, `list_sessions`,
    `end_session`, `clear_second_step` and `sign_ins`, named by the rule
    of ADR-0055, item 2. A password, a code and a session's token
    travel in none: the calls that take them are the pages', and
    `create_password_link` is no tool of the door for agents.
11. **The codes and their statuses** join the table of ADR-0055, item
    8: `sign_in_refused` 401, `too_many_attempts` 429, `unknown_session`
    404.
12. **The settings** take `sessions.idle_hours`, 1 to 8, and
    `sessions.days`, 1 to 30: shorter than the usual, never longer.

The second pull request built the company's sign-on (item 4, and the
part of items 9 and 12 that is its). Settled by me, each the owner's
to strike:

13. **`company_emails`** in the settings of a provider says its e-mails
    are the company's own, kept by its administrators. Microsoft's
    directory of one company sends no word on an e-mail; without the
    setting, an e-mail is taken only with the provider's
    `email_verified`. With it, Microsoft's `preferred_username` stands
    for an e-mail it does not send.
14. **A user is tied to one account of an issuer.** Another account of
    the same issuer with the user's e-mail is refused: the e-mail passed
    to somebody else. The same person's account of another issuer, a
    second provider, is tied too.
15. **An e-mail is a person's**, in small letters, one user's at a time;
    an empty one takes it away. It is kept in a table of its own, so a
    database of the same engine schema opens and gains it.
16. **The note is checked** for its signature (RS256, PS256 or ES256, an
    RSA key of 2048 bits or more or a key of P-256), its issuer, that it
    is for the server's client (and `azp` when it has more audiences),
    its end and its start, with a minute either way, and its nonce. The
    provider's addresses and keys are read again after a day, and the
    keys at once when a note names one not read yet, at most once a
    minute.
17. **The state of a sign-on is kept in the browser that began it**, in
    a cookie of its own, `fado_signon`, sent back on the provider's
    redirect; the answer is taken only with it. The page after the
    callback sends the person on by itself, so that the browser sends
    the session's cookie, which it holds back at the end of redirects
    that began on another site.
18. **The code lives in `internal/signon`**, which only `fadodb serve`
    links: the engine and the door still send no request of their own,
    as the dependency gate checks. A provider of the tests,
    `internal/signon/signontest`, is linked only in tests.
19. **The secret is `client_secret`** of the sign-on's name, found as a
    connection's secret is; a sign-on and a connection never share a
    name. The server says when it starts that a sign-on has no secret.

The third pull request built the passes of clients and the agent a
person connects (items 6, 7 and 8, and the part of items 9, 10 and 12
that is theirs). Settled by me, each the owner's to strike:

20. **What the record calls a connection is a grant** in the commands
    and the code: `list_grants`, `end_grant`, `unknown_grant`. A
    connection is already the outbound connection of an action
    (ADR-0045), with its own code `unknown_connection`. The account page
    says "Clients you allowed".
21. **A client is for one address.** A client known by its document is
    given passes for `/mcp` alone, as an agent; a client an
    administrator adds names `/v1`, for a face or an app, whose pass is
    the person, or `/mcp`, for an agent. A request that names no
    `resource` is for the client's address.
22. **A client holds no secret.** Every client proves each code with
    PKCE, and the server says `none` alone at its token address, which
    is what makes Claude name itself by its document. A request with a
    client secret is refused with `invalid_client`.
23. **A client's document is read each time a person is asked**, and
    kept nowhere: over HTTPS, at most 5 KB, within five seconds, past no
    redirect and through no proxy, never from an address of RFC 6890
    (asked of the address the connection is made to, after the name is
    looked up), at most eight at once. Its `client_id` is the address it
    was read from; a document with a client secret, or whose client
    proves itself otherwise than with PKCE, is refused; its addresses
    that are neither https nor http on the client's machine are left
    out; without a name, its host names it.
24. **The page that asks is `/auth/allow`.** `/auth/authorize` checks
    the request and keeps it a quarter of an hour, and its page sends
    the browser on by itself, so that the browser sends the session's
    cookie, which it holds back on a request another site began. A
    person with no session signs in on the way and comes back. The page
    shows the client's name and host, the agent's id, the person's
    groups and system groups, and where the pass goes, with a warning
    when that is a program on the person's own computer; its form may
    go to that address alone. The person is asked every time.
25. **Where a refusal goes.** A client the server does not know, an
    address the client is not sent codes at, and a member given twice
    are told to the person on the page, and nothing is sent to the
    client; every other refusal goes back to the client, with its state
    and the server's own name (RFC 9207).
26. **A code holds once and for five minutes.** A code that failed once
    is spent; one shown a second time ends the grant it gave. Codes and
    requests are kept in memory, at most 10,000 of each: a server that
    starts again forgets them, and the client asks again.
27. **The passes** are `fadoat_` for an address and `fadort_` to renew
    it, each an id and a secret of which the database keeps the hash.
    The pass for an address is held in memory and checked with no
    read; renewing reads the database. A pass of an earlier renewal
    holds to the end of its hour.
28. **A grant ends** when its person or an administrator ends it; when
    its client gives either pass back (RFC 7009); when a renewing pass
    or a code is shown twice; when an administrator removes its client;
    when its substitution is withdrawn, found at the next pass; and when
    no pass was renewed for 30 days, found when a pass is renewed or
    grants are listed, so that the server needs no clock of its own for
    it. A disabled person or agent is refused and the grant kept, as a
    session is.
29. **The agent and its substitution are made when the first code is
    exchanged**, not when the person allows, so that a code never
    exchanged leaves nothing. The agent is named after the client and
    the person, "Claude for Ann", and the change is recorded as
    `agent_connected`, by the person. A substitution for every class
    that stands is used again; otherwise the person gives a new one. It
    is withdrawn when the agent's last grant ends, as the engine's own
    command, recorded as by whoever ended the grant. An agent an
    administrator disabled stays disabled: connecting again is refused.
30. **An id with a `~` is the server's.** `create_user` refuses one, so
    that no user takes the id of an agent a person may connect.
31. **The agent acts for its person alone.** A command of it that names
    somebody else under `for` is refused with `forbidden`. A stream of
    changes is heard as oneself (ADR-0058) and the agent always acts
    for its person, so it opens none; the tool that waits, later in
    this phase, settles how an agent waits.
32. **The commands** are `add_client`, `list_clients`, `remove_client`,
    `list_grants` and `end_grant`, with the codes `unknown_client` and
    `unknown_grant`, 404. The door for agents offers all five: their
    answers hold no secret. A client's id is 1 to 64 letters, digits,
    dots, dashes and underscores, never given again; its addresses are
    over https, or over http on the client's machine, whose port may
    then be any (RFC 8252).
33. **The settings** take `grants.idle_days`, 1 to 30. Without `url`,
    clients are given passes on the machine itself only, as people sign
    in there.
34. **The server publishes** `/.well-known/oauth-authorization-server`
    and `/.well-known/oauth-protected-resource/v1`; the one of `/mcp`
    comes with the door at `/mcp`. The addresses a client calls answer
    a page of any site, since they take no cookie; the commands that
    answer a page of another site wait for the kit.
35. **The test against Claude's real connector** needs the door at
    `/mcp` and a server Claude reaches: it comes with step 6. This pull
    request is tested against the protocol's rules, with a client's
    document published over HTTPS on the test machine.
36. **A form of the pages was refused in a real browser.** With no
    referrer at all, Chromium sends the `Origin` of a form as `null`,
    which item 9 took for another site: every form of the pages,
    signing in among them, was refused with 403. The tests had sent
    `Origin` themselves. The pages now send a referrer to the server
    itself alone (`same-origin`), never to another site, and a form
    whose `Origin` is `null` is taken when the browser says, in
    `Sec-Fetch-Site`, that it comes from the same origin. Headless
    Chromium found it, and then showed a connection working: the
    session's cookie held back on a request another site began, sent
    after the page that sends on, and the code reaching the client
    after Allow.

## Consequences

- A person signs in on a screen, an agent connects as the protocol
  asks, and a program keeps its key. One database holds all of them,
  so a customer's people stay in the customer's database (ADR-0056).
- A cookie is a credential, from the server's own pages only. Every
  other client carries its pass in the header, as a key is carried.
- The server becomes the authority that gives passes for its database.
  That is code where a mistake is a hole: each pull request is tested
  against the protocol's rules and against Claude's real connector
  before it is offered for merging.
- An agent a person connects can do what the person's groups allow,
  and nothing personal unless a class says so. A designer who wants an
  agent to do less makes it a user of its own with its own groups.
- `fadodb serve` needs its address to give passes. Behind a proxy that
  holds the certificate, the operator writes the address people use.
- The program grows. About 700 KB of the 15 MB budget is left, and the
  kit's pages come at step 5; the budget's record comes with that
  step, as noted at step 1.

## Performance

A password costs 60 ms of one core, outside the writer's turn, once per
sign-in; a hundred people signing in at nine cost six seconds of one
core between them. A pass and a session are checked as a key is: one
hash and one lookup, measured with the first pull request beside the
key's. No budget changes.
