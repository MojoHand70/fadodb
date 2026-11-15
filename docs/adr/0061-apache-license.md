# ADR-0061: License: Apache 2.0

- Status: Accepted
- Date: 2026-11-15

## Context

ADR-0001 chose MIT so that anyone could embed FadoDB, in commercial
products too, and so that contributors would come. Both aims stand. Two
things MIT leaves unsaid matter for a database that runs other
organisations' processes:

- **Patents.** MIT grants no patent license in so many words. A company
  that reviews a dependency before it holds its processes asks for one.
- **Contributions.** MIT says nothing about the terms of a contribution;
  ADR-0001 relied on a line in `CONTRIBUTING.md`.

On 2026-11-15 the repository had one author, apart from automated
dependency updates, and no outside contributions, so the owner could
change the license alone. Pull request #135 replaced `LICENSE` with the
Apache License 2.0 that day.

## Decision

- License: Apache License 2.0, for the whole repository, from #135 on.
  It replaces the license part of ADR-0001; the name part stands.
- No contributor license agreement. Section 5 of the license accepts
  contributions under its own terms.
- Separate modules (`analytics/`, `postgres/`) carry the same license.
  Third-party dependencies keep their own licenses, listed in
  `docs/dependencies.md`; the BSD and MIT licenses there are compatible
  with it.
- Releases up to v0.5.0 stay under MIT: a license once granted is not
  taken back. Apache 2.0 starts with the next tag.

## Consequences

- Anyone can still embed FadoDB in closed products.
- Every contributor grants users a patent license for their
  contribution. It ends for anyone who sues claiming the work infringes
  a patent (section 3).
- The license grants no right to the name FadoDB (section 6).
- Whoever redistributes FadoDB passes on the license text, keeps the
  notices in the source and a `NOTICE` file if the project adds one, and
  marks the files they changed (section 4).
- Apache 2.0 code can go into a project under GPL version 3, but not
  into one under GPL version 2 only.
- A move to a more restrictive license would still need every
  contributor's consent.

## Performance

Not applicable.
