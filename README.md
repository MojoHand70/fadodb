# FadoDB

An embeddable, MIT-licensed active document database.

A document class declares typed named values, Excel-like formulas, a statechart,
and rules that fire actions on state changes. FadoDB keeps an append-only event
log, evaluates formulas on the server and in the browser from one compiled
bytecode, exposes every document class as a queryable table, and enforces user
and group rules written as formulas.

Pure Go core, one small binary, a protocol any frontend can attach to.

**Status:** early development. Nothing to run yet.

## License

MIT, see [LICENSE](LICENSE).
