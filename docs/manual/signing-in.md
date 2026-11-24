# Signing in

People sign in to `fadodb serve` on its own pages, with a password
and, for the people who define and keep the database, a second step
from an app on their phone (ADR-0071). The server gives them a
session, which their browser keeps, so that the pages of the server
send commands as them. A client a person allows, such as Claude, is
given a pass by OAuth. A program goes on calling with a key, as
[Serving a database](serving-a-database.md) says.

The story is Acme's. Olga is an administrator, and the first person
who signs in. Ann is in finance.

> **Status.** Passwords, the second step, sessions, a company's
> sign-on, the passes of clients, and the door for agents at `/mcp`,
> which takes Claude's pass, are built.

## The first password, on the machine

Olga makes the database and herself on the machine that holds it, as
before, and asks for a link to set her password. A password is never
typed at a terminal:

<!-- olga on the machine -->
```
fadodb init acme.fado
fadodb user add olga --kind person --system administrators --db acme.fado
fadodb key new olga --label laptop --db acme.fado
fadodb user password olga --db acme.fado
fadodb serve acme.fado
```

`fadodb user password` prints the link, good once and for a day:

```
http://localhost:8080/auth/password?link=fadol_7GQ2M4XK0P_3ZD9…
```

`--url` gives the address the server is reached at, when it is not
`http://localhost:8080`. The server says where people sign in, and
where agents connect, when it starts:

```
fadodb serve: people sign in at http://127.0.0.1:8080/auth/signin
fadodb serve: agents connect at http://127.0.0.1:8080/mcp
```

## Setting a password with a link

Olga opens the link. The page names whose password it sets, and takes
it twice:

- **At least 12 characters, and no other rule.** A sentence of four or
  five words is long enough and easy to remember.
- **The link holds once.** Used, or a day old, it says that it does
  not hold, and an administrator makes another.
- **Every session of the person ends** when a link sets the password.

The database keeps a hash of the password, PBKDF2 with SHA-256 at
600,000 rounds, and never the password.

## Signing in, and the second step

Olga signs in at `/auth/signin` with her id and her password. She is
an administrator, and the system groups `administrators` and
`designers` ask for a second step. The first time, the page shows her a
key to add to an authenticator app on her phone, as text and as an
`otpauth://` link, and asks for the six digits the app then shows.

<!-- the second step -->
| Olga | The page |
|---|---|
| gives her id and her password | shows the key for her app, and asks for a code |
| gives the six digits of the app | gives her the session, and shows ten codes for a lost phone, this once |
| signs in the next day | asks for the six digits after her password |
| gives a code she gave before | refuses it: a code holds once |
| gives one of her ten codes | lets her in; that code holds no more |

Ann is in no such group, and goes from her password to her session.
She may set a second step up for herself on her account page, and take
it away again. Dan, a designer, may not take his away: his group asks
for it. An administrator clears anybody's, for a person who lost the
phone and the codes, who then sets it up again at the next sign-in.

## The account page

`/auth/account` is Ann's: she changes her password, giving the one she
has; sets up or takes away her second step; sees her sessions, when
each began, from where and when it was last used, and ends any; and
signs out.

**A session holds 8 hours unused, and 30 days at most.** The settings
may make both shorter. It ends at once when Ann signs out, when an
administrator ends it, when a link sets her password, and when she is
disabled. When she changes her password, every session but the one she
is in ends.

## A session sends commands

The pages the server serves send commands with the session, as Ann:

<!-- ann asks who she is -->
```
POST /v1/whoami
Cookie: fado_session=fados_…
Origin: http://127.0.0.1:8080
```

answered `{"user": {"id": "ann", …}}`.

- **The cookie travels only to the server.** No script reads it, and a
  page of another site cannot make a browser send it.
- **A command with a session comes from a page of the server itself**,
  which the browser says in `Origin`. One from another site is refused
  with `unauthenticated`, as a request with no key is.
- **A key still wins.** A request with `Authorization` is the key's,
  whatever cookie it carries.

## Who may

<!-- the commands -->
| Command | What it does | Who may |
|---|---|---|
| `create_password_link` | makes a link that lets a person set their password, and answers its secret this once | an administrator |
| `list_sessions` | lists the sessions of a user that still hold | an administrator; a user, its own |
| `end_session` | ends a session by its id | an administrator; a user, its own |
| `clear_second_step` | takes away the second step of a user | an administrator; a user, its own when its groups do not require one |
| `sign_ins` | lists the log of sign-ins, refused ones among them | an administrator |
| `create_user`, `set_user` | take `email`, a person's company e-mail, which a first company sign-on finds | an administrator |
| `add_client` | adds a face, an app or an agent of the company that the server gives passes to | an administrator |
| `list_clients` | lists the clients an administrator added, the removed ones among them | an administrator |
| `remove_client` | removes a client: every grant of it ends | an administrator |
| `list_grants` | lists the clients a person allowed that still hold | an administrator; a user, its own |
| `end_grant` | ends a grant by its id: its passes are refused | an administrator; a person, their own |

Olga makes Ann a link with her key:

```
fadodb exec create_password_link '{"user": "ann"}'
```

The link is the server's address, `/auth/password?link=` and the
`token` of the answer. The door for agents does not offer
`create_password_link`: its answer would stay in an agent's
conversation. A password, a code and a session never travel in a
command: the pages take them.

**Every sign-in is kept**, refused ones too: when, the user it named,
the way, `password`, `second_step`, `link` or `signon`, the address it
came from and whether it was let through. `sign_ins` lists them.

## A company's sign-on

Acme uses Microsoft 365. Its people sign in with Microsoft: the
server sends them to Microsoft, Microsoft hands back a signed note that
says who they are, and the server checks the note: its signature by a
key Microsoft publishes, that it is for this server, that it has not
ended, and that it answers the sign-in the server began. The server
never sees a Microsoft password, and asks no second step after it:
Microsoft brings Acme's own.

The operator registers the server with Microsoft as an application,
with the address `https://fado.acme.example/auth/callback` to come back
to, and writes what Microsoft gives into the settings:

<!-- a company's sign-on -->
```jsonc
{
  "url": "https://fado.acme.example",
  "signon": {
    "microsoft": {
      "label": "Sign in with Microsoft",
      "issuer": "https://login.microsoftonline.com/7d1e3c52-0000-4000-8000-000000000000/v2.0",
      "client_id": "3f2a7c90-0000-4000-8000-000000000000",
      "company_emails": true
    }
  }
}
```

- **`issuer`** is the address of the company's directory, the one of
  Acme's own tenant, never Microsoft's common one: everybody with a
  Microsoft account would be its people.
- **The client secret is found as every secret is**: the file
  `microsoft/client_secret` in the directory of secrets, or
  `FADODB_SECRET_MICROSOFT__CLIENT_SECRET`. The server says when it
  starts that a sign-on has none.
- **`company_emails`** says the provider's e-mails are the company's
  own, kept by its administrators. Microsoft says no word on an e-mail
  and Google does: without it, an e-mail is taken only when the
  provider vouches for it.
- **Google, Okta and any provider of OpenID Connect** are written the
  same way, each under its own name. Each is a button on the page.

**Olga adds Ewa first**, with her company e-mail:

```
fadodb exec create_user '{"id": "ewa", "kind": "person", "email": "ewa@acme.example", "groups": ["approvers"]}'
```

The first time Ewa signs in with Microsoft, the e-mail Microsoft
vouches for finds her user, and the server ties her Microsoft account
to it. From then on Microsoft's id for her finds her, whatever her
e-mail becomes: an e-mail can pass to somebody else, the id never does.

<!-- signing on -->
| What happens | Answer |
|---|---|
| Ewa signs in with Microsoft the first time | she is signed in, and her Microsoft account is tied to her user |
| Ewa signs in again, her e-mail at Microsoft changed | she is signed in: her account finds her |
| Zed, who has no user, signs in with Microsoft | refused, `sign_in_refused` |
| another Microsoft account with Ewa's e-mail signs in | refused, `sign_in_refused`: her user is tied to her own |
| Microsoft's answer comes to another browser than the one that began | refused, `sign_in_refused` |

Nobody gets in for being in Acme's directory: an account with no user
is refused in the same words as a wrong password. Groups stay the
database's own: Olga gives them, as before. Every sign-on is in the
log of sign-ins, as `signon`.

## Connecting a client

A client is a program that calls the server for a person: Claude,
which Ann connects to act for her, or FadoFace, a face of Acme's. It is
given a pass by OAuth, as the protocol of agents asks, and never sees
a password.

**How Ann connects Claude.** Ann adds the server to Claude as a
connector, at `https://fado.acme.example/mcp`. Claude reads where
passes come from, and sends Ann's browser to the server's page:

1. Ann signs in, if her browser has no session.
2. She reads: *"**Claude** (claude.ai) wants to act for you, **ann**,
   in this database, as the agent ann~claude.ai. It may do what your
   groups let you do, in finance, until you end it on your account
   page. Where a class asks for a person, it is refused."* She allows
   it.
3. Claude is sent a code, which it exchanges, with the proof it made
   the request with, for a pass of one hour and a pass that renews it.

The first time, the server makes the user `ann~claude.ai`, an agent
named "Claude for Ann", and a substitution that lets it act for Ann,
given by Ann. `USER()` is the agent and `ACTINGFOR()` is Ann. The agent
has Ann's groups and system groups, as they are at each request, and
never more:

- **What Ann's groups let her do, her Claude does**: a rule such as
  `INGROUP("approvers")` lets it through.
- **A rule that asks for a person refuses it**, as
  `USERKIND() = "person"` does in the class of
  [Who may](notions/who-may.md).
- **A rule that names Ann lets it through only where the class takes
  whoever acts for her**, with `ACTINGFOR()`, as in
  [Acting for somebody](guides/acting-for-somebody.md).

It acts for Ann alone: a command of it that names somebody else under
`for` is refused with `forbidden`.

**Claude is known by the document it publishes**, at the address it
gives as its client id, which says its name and where its codes go.
The server reads it when Ann is asked: over HTTPS only, at most 5 KB,
never past a redirect and never from an address inside the network.
Nobody registers Claude, and the page shows Ann the host the document
came from, and where the pass goes.

**FadoFace is added by Olga**, as a face or an app of Acme's would be,
with the addresses its codes are sent to and the address it is given
passes for:

```
fadodb exec add_client '{"id": "fadoface", "name": "FadoFace", "redirect_uris": ["https://face.acme.example/callback"], "address": "/v1"}'
```

A face's pass is Ann herself at the commands, `/v1`, as her session is
on the server's own pages. An agent's pass is for the door for agents,
`/mcp`. A pass is refused at the other address, and a client known by
its document is given passes for `/mcp` alone.

**What the server publishes**, as the protocol asks:

<!-- how passes are given -->
| Address | What it is |
|---|---|
| `/.well-known/oauth-authorization-server` | how passes are given: a code proven with PKCE (S256), clients that hold no secret, clients known by their document |
| `/.well-known/oauth-protected-resource/v1` | where passes for the commands come from |
| `/.well-known/oauth-protected-resource/mcp` | where passes for the door for agents come from |
| `/auth/authorize` | where a client sends the person |
| `/auth/allow` | the page that asks the person |
| `/auth/token` | where a client exchanges its code for passes, and renews them |
| `/auth/revoke` | where a client gives its pass back |

<!-- connecting a client -->
| What happens | Answer |
|---|---|
| Ann allows Claude | Claude is given a pass for `/mcp`, as `ann~claude.ai` acting for Ann |
| FadoFace sends `whoami` with its pass | answered `ann` |
| Claude's pass is sent to the commands | refused, `unauthenticated`: a pass is for one address |
| Claude calls `whoami` at `/mcp` with its pass | answered `ann~claude.ai`, acting for `ann` |
| FadoFace's pass is sent to `/mcp` | refused, `unauthenticated`: a pass is for one address |
| a code is exchanged without the proof it was asked with | refused, `invalid_grant` |
| a code is exchanged a second time | refused, `invalid_grant`, and the grant ends |
| a renewing pass is used a second time | refused, `invalid_grant`, and the grant ends: it was stolen |
| Ann does not allow it | the client is told `access_denied` |
| a client the server does not know | the page says so, and nothing is sent to the client |
| Ann ends the grant on her account page | its passes are refused, and the substitution is withdrawn |

**A grant holds until it is ended.** It ends by itself when no pass
was renewed for 30 days, and the settings may make that shorter. A
pass holds one hour. Ann's account page lists the clients she allowed,
and she ends any; Olga ends anybody's, with `end_grant`. Withdrawing
the substitution ends the grant too, and disabling Ann or her agent
refuses its passes.

## The settings

<!-- the settings -->
```jsonc
{
  "url": "https://fado.acme.example",
  "proxies": ["127.0.0.1"],
  "sessions": {"idle_hours": 4, "days": 7},
  "grants": {"idle_days": 14}
}
```

- **`url` is the address people reach the server at.** The pages and
  the commands of a session take requests from it alone, and over
  HTTPS the cookie travels only encrypted. Behind a proxy that holds
  the certificate, it is the proxy's address.
- **`proxies` names the proxies in front of the server**, such as
  Caddy or nginx on the same machine: an address, or a range such as
  `10.0.0.0/8`. From a request of one of them, a sign-in is recorded,
  and failures are counted, at the address the proxy read in
  `X-Forwarded-For`: the last one that is not a proxy, since a caller
  may write the header too. From any other address the header is not
  read. Without `proxies`, every person behind a proxy has the proxy's
  address, and twenty failures of anybody make everybody wait.
- **Without `url`, people sign in on the machine itself only**, at
  `http://localhost`. A server reached from other machines without one
  shows no page, and says so when it starts.
- **`sessions`** shortens how long a session holds: `idle_hours` 1 to
  8, `days` 1 to 30.
- **`grants`** shortens how long a client a person allowed holds with
  no pass renewed: `idle_days` 1 to 30.
- **Without `url`, clients are given passes on the machine itself
  only**, as people sign in there.

## What is refused

<!-- refused -->
| What happens | Answer |
|---|---|
| a wrong password | refused, `sign_in_refused` |
| a user the database does not have | refused, `sign_in_refused`, in the same words |
| an agent signs in with a password | refused, `sign_in_refused`, in the same words: an agent calls with a key |
| a sixth wrong password within a quarter of an hour | refused, `too_many_attempts`, for a while that grows |
| a link used a second time | the page says the link does not hold |
| a password of 11 characters | refused, `bad_value` |
| `create_password_link` for an agent | refused, `bad_value` |
| a command with a session, from another site | refused, `unauthenticated` |
| a form of the pages sent from another site | refused, with the status 403 |

Twenty failures from one address within a quarter of an hour make
that address wait too, whoever they were for: many people of one
office share one.
