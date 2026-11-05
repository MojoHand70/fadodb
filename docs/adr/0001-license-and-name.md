# ADR-0001: License and name

- Status: Accepted
- Date: 2026-11-05

## Context

FadoDB is meant to be embedded in other people's products, including
commercial ones, and to attract contributors. Several nearby projects moved
to source-available licenses in 2025 and 2026, which left users of
"open source" low-code data tools uncertain about what they may build.

The name had to be free of conflicts with databases and with programming
languages, pronounceable, and available as a package name in the ecosystems
where clients will ship.

## Decision

- License: MIT, for the whole repository, with no contributor license
  agreement. Contributions are accepted under the same license.
- Name: FadoDB. On 2026-11-05 the name was free on npm, PyPI and crates.io,
  no other GitHub repository used it, and the .com, .io, .dev and .org
  domains were unregistered. No conflicting trademark was found in a search
  of public registers; this is a search, not legal advice.
- Separate modules (`analytics/`, `postgres/`) carry the same license. Their
  third-party dependencies keep their own licenses, listed in
  `docs/dependencies.md`.

## Consequences

- Anyone can embed FadoDB in closed products. That is intended.
- A later move to a more restrictive license would require every contributor's
  consent, which is a feature: it makes such a move impractical.
- Package names and domains should be registered before the first client ships.

## Performance

Not applicable.
