# ADR-0088: People sign in with a passkey, invited by a link

- Status: Proposed; amends ADR-0071 (a person signs in with a passkey or the company's sign-on, and nothing else: passwords and the six-digit second step go; an invitation replaces the link that set a password; the server's pages carry a script and typefaces of their own), ADR-0059 (the settings gain the proxies whose word on a caller's address is taken), ADR-0057 (item 7: the way back in on the machine prints an invitation), ADR-0055 (five commands join the table and two leave it). The owner ruled its premises on 2026-11-24; every item settled by me is for him to veto.
- Date: 2026-11-24

## Context

ADR-0071 signs a person in with a password, plus a code of six digits
from an authenticator app for administrators and designers. Its item
15 left passkeys out, and its settled item 4 shows the key of the
second step as text, since the standard library draws no picture to
scan.

On 2026-11-24 the owner set up the first test server on a machine of
his own and could not get through: "I cannot retype worms from PC
screen to a phone." He asked for the sign-in of Rhea, the first
program built on FadoDB, which has used passkeys only since
2026-10-19: a person is invited by a link, adds a passkey on a device,
and signs in with a fingerprint, a face or the device's PIN. And of the
page: "it doesn't look like a professional system."

The words of this record, each with one example:

- **A passkey.** A key that Ann's phone or computer makes for one
  address, keeps, and opens with her fingerprint, her face or the
  device's PIN. The server keeps only its public half. It works on the
  address it was made for and nowhere else, so a page that only looks
  like Acme's gets nothing from it. Browsers implement it as the W3C's
  Web Authentication (WebAuthn).
- **An invitation.** A link Olga makes for Ann, good once and for 7
  days, with which Ann adds her first passkey.
- **The code for the phone.** On the invitation page the computer
  shows a picture to scan, a QR code, holding a link of its own. Ann
  points her phone's camera at it and adds the passkey on the phone.

The owner's rulings, put in the chat one at a time on 2026-11-24, each
with the other way and its cost:

1. **Passkeys only for people**, or the company's Microsoft or Google
   account; no passwords anywhere ("5. yes"). The other way was to keep
   passwords, draw the code of the second step, and add passkeys beside
   them: two ways in to keep, a password that can be phished, and a
   code to type at every sign-in.
2. **A passkey is checked by our own code**, Go's standard library only
   ("6. yes"). The other way was Rhea's library, go-webauthn, which
   brings about ten more: CBOR, TPM, JWT and others.
3. **The screen is built to Rhea's sign-in**, with the server's look
   and Rhea's typefaces, shown on a test server in small shots before
   any merge; Rhea's layout becomes public in the engine ("7. yes").
4. **The passkey page shows a QR code for the phone** (his words, of a
   mockup of the purchase of a server).
5. **This record first, then the pull requests one at a time**
   ("2. yes").

Five premises were checked.

- **Every person has a browser that makes passkeys.** Chrome, Edge,
  Safari and Firefox have them on Windows, macOS, iOS and Android, and
  a phone's passkeys follow its owner to a new phone through iCloud
  Keychain or Google Password Manager.
- **A passkey belongs to one address**, the host it was made on. A
  server for each customer (ADR-0056) has passkeys of its own: none of
  one server opens another, nor a program that made the server.
- **What the server checks is small.** When a passkey is added, it
  reads the public key once, from a structure in CBOR. At each
  sign-in, it checks one signature with `crypto/ecdsa`, `crypto/rsa`
  or `crypto/ed25519`, the challenge it gave, the address and two
  flags. Which maker made the device, its attestation, is not asked,
  as for any passkey a person keeps; there is nothing to check there.
- **WebAuthn is called from a script.** The server's pages allow none
  today (ADR-0071, settled item 9). They need one: the server's own.
- **Behind a proxy, every caller has the proxy's address.** On the
  test server Caddy stands in front of `fadodb serve`, and the server
  takes the address of a request from the connection alone: the
  slowing of failed sign-ins (ADR-0071, settled item 2) counts every
  person as one address, and each sign-in is recorded as from
  127.0.0.1. Found on 2026-11-24.

The case is Acme's, as in ADR-0071: Olga, an administrator; Dan, a
designer; Ann, in finance; Ewa, who signs in with Microsoft.

## Decision

1. **A person signs in with a passkey, or with the company's
   sign-on.** Nothing else. A password and the six-digit second step
   go, with their pages, commands and settings. An agent and a service
   keep their keys and passes, as ADR-0057 and ADR-0071 have them; a
   passkey is a person's alone.

2. **An invitation lets a person add their first passkey.**
   - Olga makes one for Ann: good once, for 7 days, for one passkey,
     at `https://fado.acme.example/auth/invite?t=fadoi_…`. The
     database keeps its hash, as of every token.
   - Ann opens it and reads: **Welcome, Ann.** Olga invited you to
     Acme. Add a passkey to sign in: your fingerprint, your face or
     this device's PIN. No password. She names the device, which the
     page proposes from the browser ("Ann's iPhone"), and presses
     **Add a passkey and enter**. She is signed in.
   - An invitation ends when it is used, after its 7 days, when Olga
     withdraws it, or when a new one is made for Ann.
   - Ewa, who signs in with Microsoft, needs none: her first sign-in
     ties her Microsoft account to her user, as ADR-0071, item 4, has
     it.
   - **The first administrator of a new server** is invited on the
     machine: `fadodb user invite olga --url https://fado.acme.example`
     prints the link. It is also the way back in when every
     administrator has lost every passkey (ADR-0057, item 7), and it
     replaces `fadodb user password`. A program that makes servers
     calls `create_invitation` with its key and hands the person the
     link.

3. **The code for the phone.**
   - The invitation page shows the code first: "Point your phone's
     camera at the code. Your phone keeps the key." It holds a link of
     the same invitation, good once and for 10 minutes. **Use this
     computer instead** makes the passkey on the computer.
   - The phone opens the link, Ann adds the passkey there with her
     face, and the phone is signed in.
   - **The computer that showed the code signs Ann in too**, once the
     phone has added its passkey: only that browser, which holds a
     cookie of its own for the code, and only within those 10 minutes.
     A computer that holds the invitation could have added a passkey
     itself, so this gives it nothing more.
   - Ann's account page shows a code of the same kind, for a link that
     adds a passkey of hers on another device.
   - **The server draws the code itself**, in Go, as an SVG inside the
     page: no library and no outside service.

4. **Signing in.** The sign-in page has one button, **Sign in with a
   passkey**. The browser offers the passkeys it holds for the address;
   Ann touches the sensor and is in. She types no name: the passkey
   says who she is. On a computer that holds none of hers, the
   browser's own window offers "use a phone", and the passkey on her
   phone lets her in; the phone and the computer prove they are near
   each other, which the browser and the phone do without the server.
   The buttons of the company's sign-on stand below, when it is set.

5. **What the server asks of a passkey, and checks.**
   - It is made for the host of `url` (ADR-0071, item 12), and used
     only on that origin.
   - It is discoverable (the device keeps who it is for), and the
     person is verified, by the fingerprint, the face or the PIN, every
     time.
   - Attestation `none`: the server does not ask who made the device.
   - Its key is ES256 (P-256), EdDSA (Ed25519), or RS256 (RSA of 2048
     bits or more): what Windows Hello, Apple, Google, Android and
     security keys make.
   - At each sign-in the server checks the challenge it gave (good
     once, for 5 minutes), the origin, the hash of the host, that the
     person was present and verified, and the signature over the
     device's data and the hash of what the browser sent. When the
     device keeps a counter, the counter must rise: one that does not
     is refused and recorded, as a copied key would show.
   - The device knows Ann by 16 random bytes the server gives her,
     never by her id or her e-mail.
   - **The code is `internal/webauthn`**: a reader of the part of CBOR
     that WebAuthn uses, the three kinds of key, and the checks. Its
     tests carry an authenticator of their own for each kind of key,
     and refuse a wrong challenge, origin, host, flag, counter and
     signature each. The pages are played in headless Chromium with
     its virtual authenticator. The security review of Phase 10 reads
     it.

6. **A person's passkeys.** Each is kept with the name of its device,
   when it was added and last used, and its public key; never its
   private half, which the server never sees.
   - Ann's account page lists her passkeys, adds one, by the code or on
     this device, and removes one. Her last cannot be removed, unless
     she signs in with the company's sign-on too.
   - Olga sees Ann's passkeys and removes any, when a phone is lost. A
     new invitation is how Ann then gets back in.
   - **An administrator is asked to keep two passkeys**: the account
     page says so while they have one.
   - Removing a passkey ends the sessions it began.

7. **The pages are built to Rhea's sign-in.** Centred: the server's
   name above one heading, one sentence of what happens, one button,
   and one small line for whoever has no passkey yet.
   - In Rhea's typefaces, Schibsted Grotesk and Martian Mono, under the
     SIL Open Font License, handed out by the server from the program;
     in FadoDB's own colours, light and dark, with its signet.
   - They are shown on a test server in small shots, and merged after
     the owner's yes.
   - The policy of the pages gains `script-src 'self'`, for one script
     of the server's own, and `font-src 'self'`. Still no script from
     elsewhere, none written into a page, and no framing.
   - The words stay English (ADR-0071, item 9).
   - **The server's own look on these pages**, its mark and its
     colours, is a step of its own (item 11, step 5). Where the engine
     takes them from is open: the class `Look` (ADR-0060, item 3) was
     withdrawn on 2026-11-27. Until then the pages carry the server's
     name and FadoDB's look.

8. **The address a request came from, behind a proxy** (amends
   ADR-0059). The settings gain `proxies`, the addresses of the
   proxies in front of the server:

   ```json
   {
     "url": "https://fado.acme.example",
     "proxies": ["127.0.0.1"]
   }
   ```

   From a request that comes from one of them, the server takes the
   caller's address from `X-Forwarded-For`: the last entry that is not
   a proxy. From any other, it ignores the header. Without the setting
   nothing changes. The slowing of failed sign-ins and the record of
   each sign-in use this address.

9. **What an administrator does through commands.**
   `create_invitation`, `list_invitations`, `withdraw_invitation`,
   `list_passkeys` and `remove_passkey`, named by the rule of ADR-0055,
   item 2, when they are built. `create_password_link` and
   `clear_second_step` go. The door for agents does not offer
   `create_invitation`, as it does not offer `create_key` (ADR-0064,
   ruling 2): a link that lets somebody in never stays in an agent's
   conversation.

10. **Everything is recorded**, in the record of sign-ins of ADR-0071,
    item 11: an invitation made, used, withdrawn or ended, and by whom;
    a passkey added, with its device's name, or removed, and by whom;
    each sign-in and each refusal, with the way (`passkey`, `phone` or
    `signon`), the moment and the address of item 8; a counter that
    did not rise.

11. **Built in five pull requests, each after the one before is
    merged:**
    1. the address behind a proxy (item 8);
    2. invitations and passkeys on the device in use: the check, the
       tables, the commands, `fadodb user invite`, and the invitation,
       sign-in and account pages in Rhea's layout;
    3. the code for the phone (item 3);
    4. passwords and the second step go: their pages, commands,
       settings and sections of the manual. A database made before
       keeps their tables, and nothing reads them;
    5. the server's own look on its pages, its mark and its colours,
       from a place still to be decided (`Look` was withdrawn on
       2026-11-27).

    Each moves the manual's chapter on signing in, and the marks in
    `skills/`, in the same pull request.

12. **No new dependency.** The part of CBOR, the keys, the signatures,
    the QR code and random bytes are Go's standard library and code of
    our own. The typefaces are files, with their licences, named in
    `docs/dependencies.md`.

13. **What this record leaves out.**
    - Passkeys for an agent or a service.
    - Asking which maker made a device, and lists of the devices
      allowed.
    - A passkey that opens several servers of one customer.
    - Signing in by a link sent by e-mail: the server sends no e-mail.
    - The words of the pages in other languages.
    - Moving the people who have passwords today: before 1.0 only test
      servers have them, and each person gets an invitation.

## Settled by me, each the owner's to veto

The invitation's 7 days, as Rhea's; the code's 10 minutes; the
computer signed in once the phone has added its passkey; the challenge
of 5 minutes; the counter that must rise; two passkeys asked of an
administrator, and a last one kept; 16 random bytes by which a device
knows a person; the three kinds of key, and no attestation; the
setting `proxies` and the last entry not a proxy; the tables of
passwords left unread in an older database; the server's own look as
the last step.

## Consequences

- Nothing to type, nothing to remember, and nothing a fake page can
  use. The device and the finger are two steps in one touch.
- Losing every device means a new invitation from an administrator;
  every administrator losing every one means `fadodb user invite` on
  the machine.
- The server's pages carry a script and typefaces, and their policy
  allows only the server's own.
- Our own WebAuthn code is security code: each refusal has its test,
  and the security review reads it.
- The owner's demo moves: its people get invitations and add
  passkeys, and the checks that sign in with a password and a code sign
  in with Chromium's virtual authenticator.
- The program grows by the typefaces, about 130 KB, and the code; the
  size gate measures it (ADR-0072).

## Performance

A sign-in checks one signature, well under a millisecond, where a
password took 60 ms of PBKDF2. No hot path is touched: the VM,
recalculation and the commit stay as they are. The size gate measures
the program.

## Settled while building

The first pull request built item 8. Settled by me, each the owner's
to strike:

1. **A proxy is an address or a range**, such as `10.0.0.0/8`, since a
   cloud's load balancers come from one; each is kept as a range.
2. **An entry of `X-Forwarded-For` may carry its port, or an IPv6
   address in brackets**, as some proxies write it; an IPv4 address
   written as IPv6 is IPv4.
3. **An entry that is no address stops the reading** at the proxy that
   passed it on, and the sign-in is recorded at that proxy's address.
   When every entry is a proxy, the first of them is the caller.
4. **The address is read for the pages of signing in alone**: nothing
   else of the server records where a request came from. The server's
   help and both chapters of the manual show the setting.

The second pull request built items 1 to 7, 9 and 10 for the device in
use: the check, the tables, the commands, `fadodb user invite`, and the
invitation, sign-in and account pages. Settled by me, each the owner's
to strike:

5. **A challenge holds what it needs, signed**: what it is for (a
   sign-in, an invitation, or a passkey added on the account page),
   when it ends, the 16 bytes the device will know the person by, and
   random bytes, signed with a key the server makes when it opens the
   database. A page that asks for one writes nothing; only a challenge
   that was used is remembered, until it ends. A server that starts
   again holds no question it asked before.
6. **The statement of a device's maker is not read, whatever its
   format.** Asked for none, some devices still send one (`packed`);
   it is taken and not read, as if it said nothing.
7. **A person's passkeys share the 16 bytes of their first**, so that
   every device of theirs knows them alike; each passkey keeps the
   bytes it was made with, and a use is checked against them.
8. **A passkey removed is kept**, with when and by whom, and opens
   nothing; a removed passkey is never listed.
9. **Nobody removes their own last passkey, an administrator
   included**, unless they sign in with the company's sign-on too. An
   administrator removes anybody else's, the last too.
10. **A counter that did not rise** is refused as every other wrong
    passkey is, in the same words, and recorded among the person's
    changes as `passkey_counter_fell`, with the passkey's id.
11. **A device is named with 1 to 60 characters**, which the page
    proposes from the browser: "Ann's iPhone", "Ann's Windows PC".
12. **Until step 4, a password still signs a person in**: the sign-in
    page offers it under its button, as a small link, **Sign in with a
    password**, and the account page shows the password and the second
    step under the passkeys to a session begun with a password alone.
    A person signed in with a passkey or the company's sign-on is asked
    for no password (the owner's first try, before the merge).
13. **The server's name on its pages is its address**, the host of
    `url` (or `localhost:8080` on the machine), under FadoDB's mark,
    until step 5 gives a name and a mark.
14. **The pages load their style from a file of the server's too**, so
    their policy is `style-src 'self'`, and no longer
    `'unsafe-inline'`: nothing written in a page runs or styles it.
15. **The files of the pages are in a package of their own**,
    `internal/server/web`, since the server's package keeps no
    variable (ADR-0056, item 4). Each is served at an address that
    holds the start of its hash, and kept by a browser for a year.
16. **Two refusals are new**, `unknown_invitation` and
    `unknown_passkey`, both 404. `list_invitations` lists the
    invitations that hold, and `withdraw_invitation` names the person,
    who has one at most. A session lists the passkey it began with
    (`passkey` in `list_sessions`).
17. **After an invitation, the person goes to the account page**, where
    their passkey is listed: the server knows no other home of theirs.
18. **The tests play the pages with a device of their own**
    (`internal/webauthn/webauthntest`), which makes and uses passkeys
    as a phone or a computer does, in the browser's place. Chromium's
    virtual authenticator played the pages before the pull request was
    opened; it is not among the gates, which run no browser.

The third pull request built item 3, the code for the phone. Settled
by me, each the owner's to strike:

19. **A code is kept in the server's memory alone**, as a challenge
    is: no table is added, a server that starts again holds none, and
    the page shows a new one when it is loaded.
20. **A new code of the same invitation, or of the same person, ends
    the one before**, so that a page loaded again leaves one code that
    holds.
21. **A phone is shown no code.** The page of an invitation opened on
    a phone, an iPhone or an Android phone by what its browser says of
    itself, offers **Add a passkey and enter** at once, and the account
    page opened on one shows no code either.
22. **The computer's page asks the server every two seconds how its
    code stands**, at `/auth/phone/state`, and the answer is one word:
    waiting, done or ended. Only a page that shows a code may ask the
    server anything, and the server alone (`connect-src 'self'` in its
    policy). Once the phone is done, the page sends its form to
    `/auth/phone/enter`, which signs that browser in by the secret of
    its cookie; the account page shows itself again with the new
    passkey.
23. **The computer's session is the phone's passkey's**: removing the
    passkey ends it, as it ends the phone's. It is recorded with the
    way `phone`.
24. **The phone's passkey is added as the invitation's, or as the
    account's**, as on the computer, under the name the phone's page
    proposes from the phone's browser ("Ann's iPhone").
25. **The code is drawn at the level of error correction M**, which
    restores about 15% of it, in the smallest version that holds the
    link: 6 or 7 for a server's address. It is dark on white with its
    quiet zone in either colour scheme, since a camera reads that best.
    The tests read every code back with a reader written apart from the
    encoder, `internal/qrcode/qrcodetest`, and hold two blocks and the
    format and version words to the standard's published values.
26. **A device that holds the person's passkey already signs in with
    it.** A synced passkey, which a password manager keeps on each of
    the person's devices, is on their phone once they added it on their
    computer, and a device makes no second passkey of one person for one
    server: it refuses with `InvalidStateError`. The owner met it on the
    test server on 2026-11-25, as a page that said so in red and offered
    nothing more. His ruling that day: "let the phone page offer sign in
    with it". When the device refuses, the phone's page and the page of
    an invitation give way to **Sign in with it**, a form drawn hidden
    for a person who holds a passkey. It uses the passkey the device
    holds, which must be the person's, with a challenge bound to the
    code or the invitation. The computer that showed the code enters
    with that passkey, as when the phone adds one.
27. **The invitation ends when the person signs in with it** (the
    owner's ruling, 2026-11-25, in his words): signing in with a passkey
    the device holds uses the invitation, as adding a passkey does.
28. **A synced passkey is named after its password manager** (the
    owner's ruling, 2026-11-25: "name it after the password manager").
    His synced passkey was listed as "Krzysztof's Windows PC", the
    computer he made it on, though his password manager kept it on his
    phone too. When a passkey is made, the browser says its maker's
    AAGUID, which Chrome does under attestation `none`. A synced passkey
    whose AAGUID the table of makers holds is named after the maker,
    whatever name was given. The table is the engine's own copy of the
    community list `passkeydeveloper/passkey-authenticator-aaguids`, as
    it stood on 2026-11-25, 67 makers: the list takes nothing new since
    2026-11-01 and answers nothing from 2027-01-06. The AAGUID itself is
    not kept. Safari says sixteen zeros. A synced passkey whose maker
    is not known, by those zeros or by an AAGUID the table does not
    hold, keeps the name given with ", synced" after it: "Krzysztof's
    Mac, synced" (the owner's ruling, 2026-11-26: "device name plus
    synced"). The account page then says "synced" once, in the name,
    and not again after the dates (settled while building). A passkey
    that stays on its device keeps the name given alone. Not ruled: the
    passkeys made before, which are named as they were.
