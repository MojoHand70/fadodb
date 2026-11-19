# ADR-0073: The server hands out the pages of a face from a folder, and has none of its own

- Status: Proposed
- Date: 2026-11-19

## Context

The plan of Phase 7 had a kit of plain pages built into `fadodb serve`,
with `--pages` for a face of one's own in their place, and the roadmap
says so. ADR-0071 gave those pages, the pages the server itself serves,
a session.

On 2026-11-18 the owner took the kit out of the server: a database
carries no application, as PostgreSQL and MariaDB carry none, and what
people see is a face, a program of its own. The open face,
[FadoDB Workshop](https://github.com/MojoHand70/fadodb-workshop), is in
a repository of its own and uses only the commands every caller has.

A face made of plain files has no server of its own. To sign people in
with what is built it has to answer at the server's address: a page of
another site cannot send a command with a session (ADR-0071, items 5
and 9), and the commands do not answer a page of another site yet
(item 34). On 2026-11-19 Workshop signs a person in, lists a class and
reads, changes, makes and moves a document, in front of a small helper
of its own that hands out its files and passes everything else to the
server. Nobody but its developers can run it that way.

The case is Acme: Olga, who runs the server, and Ann, who works in
Workshop.

## Decision

1. **The server has no pages of a face of its own.** Its own pages are
   the ones where people sign in and keep their account (ADR-0071).
   Told nothing, its address answers as before: not found.

2. **`fadodb serve FILE --pages DIR` hands out the files of a folder at
   the root of the server's address**, `index.html` first:

   ```
   fadodb serve acme.fado --pages ./workshop
   ```

   Ann opens `https://fado.acme.example/` and is in Workshop. A folder
   inside the folder answers with its own `index.html`, and is asked for
   with a slash at its end.

3. **The server's own addresses always come first**: `/v1/`, `/auth/`,
   `/mcp` and `/.well-known/`. A file of the folder under one of those
   names is never handed out, and the server says so when it starts.

4. **The folder is only read**, with `GET` and `HEAD`. Nothing reaches
   it through the server.

5. **Only a fixed list of kinds of file is handed out**, by the ending
   of the name: pages, style sheets, scripts, data, texts, pictures and
   fonts. The type a file is sent as comes from the server's own table
   and never from the machine's, which differs from one machine to the
   next. The list is in the chapter on serving, held to the code by a
   test.

6. **Nothing else in the folder is there for a browser**: a file of
   another kind, a file or a folder whose name begins with a dot, and
   whatever a link in the folder points to outside it. A path is taken
   as it is written and never cleaned into another: one with `..`, an
   empty part or a backslash names nothing.

7. **Every file is sent with one header, which is not the operator's
   to change:**

   ```
   Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'
   ```

   A page loads script, style, pictures and fonts from the server alone,
   calls the server alone, and is shown inside no other page. No script
   is written in a page. A style may be: a text editor that a page
   loads sets its own, and a style runs nothing.

8. **The folder is the operator's to answer for**, as the program and
   its settings are. A script of such a page runs at the server's own
   address, so it sends commands with the session of whoever is signed
   in, and can do what that person may do. It can do nothing that
   person may not: every rule of a class is checked by the engine, on
   every command, whoever sends it.

9. **A changed file is seen at the next load.** The browser asks each
   time whether its copy is still the file, and is told so without it.

10. **A folder that cannot be handed out is found when the server
    starts**: one that is not there, or has no `index.html`. The server
    then does not start. When it starts, it says where the pages
    answer.

11. **A page is a request like any other**: it has its line in the log,
    as `page /list.html`, and is counted with the rest.

## The owner's ruling

Of 2026-11-18, on the kit inside the server: "The whole 'app' process
does not belong to database. Does MariaDB offer applications. Does
Postgres offer applications." And, made exact the same day: "I meant
'frontend app building process' does not belong to engine." To the
proposal that the server be pointed at a folder of pages: "Agreed with
your proposal, FadoDB server pointed at a folder". On the two ways to
run a face: "Can we have both options? Like an embedded small http
server for less technical users and as a folder for more advanced
users who know how to set a server up."

Of 2026-11-19, two questions:

- Where the pages answer: "At the root". The other way was under a
  path, `/app/`, which leaves the root free and makes every page know
  the path it lives under.
- Which header the pages are sent with: "One fixed header". The other
  way was a header the operator sets, so that a face of Acme's own
  could load a map from another site, and one wrong line could open
  the door wider.

Settled by me, his to veto: items 4 to 6 and 9 to 11; the list of
kinds; that the header lets a style be written in a page; and the name
`--pages`, which stood in the roadmap.

## Consequences

- One program, one address, one certificate. Olga downloads a folder
  and names it; nothing else is installed.
- Signing in works as it is built, and every way the server learns to
  sign people in works in a face the same day.
- The engine stays an engine. Workshop is not in the program and does
  not count against its size.
- Whoever can write to the folder can act as everybody who signs in,
  within what each may do. The folder belongs with the program, not
  with the data.
- A face that needs another site, for a map or a font, cannot be
  handed out this way. It waits for a face at an address of its own.

## What this record leaves out

Each is planned and needs a record of its own:

- **A face at an address of its own**, the second of the two ways the
  owner asked for on 2026-11-18: the commands answering a page of
  another site, and the face carrying passes (ADR-0071, items 21 and
  34).
- **A room for a page that carries code somebody else wrote**, such as
  a face an agent builds. The header of item 7 lets a page show no
  other page inside it; that record decides what it opens.
- **How Workshop reaches a person who installs FadoDB**: beside the
  program, in the image for containers, or in one bundle with a
  starter.

## Performance

No budget changes. A command passes one more comparison on its way to
the door, and no allocation. A file is read from the disk at each
request that the browser's copy does not answer.

The program grows by 49,152 bytes, from 15,630,498 to 15,679,650 of
the 20,971,520 of ADR-0072, measured as `scripts/sizegate.sh` builds
it, on 2026-11-19. Sending a file with the standard library's
`http.ServeContent`, which also sends a part of a file, measured some
53,000 bytes more; a file is small and is sent whole, by hand.
