# Security policy

## Supported versions

FadoDB has no release suitable for production use yet. Security fixes land on
`main` and in the next tagged release. Once 1.0 ships, this section will list
the supported minor versions.

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Use GitHub's private vulnerability reporting for this repository:
https://github.com/MojoHand70/fadodb/security/advisories/new

You will get an acknowledgement within 72 hours and a plan, or a request for
more detail, within 7 days. Credit is given in the advisory and the changelog
unless you prefer to stay anonymous. There is no bug bounty.

## Scope

Anything in this repository: the engine, the command-line tool, the protocol,
the build and release pipeline. Reports about the formula language leaking
data across access rules are especially welcome; that boundary is the one
FadoDB promises to hold.
