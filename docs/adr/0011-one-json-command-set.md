# ADR-0011: One JSON command set over HTTP, WebSocket and C ABI

- Status: Accepted; amended by ADR-0025 (preview and validate), ADR-0026 (idempotency keys), ADR-0027 (the principal on every call), ADR-0033 (blobs travel outside the JSON command set)
- Date: 2026-11-05

## Context

FadoDB must be usable from any language and any frontend without writing Go.
Maintaining separate REST, RPC and FFI surfaces would triple the work and
drift apart.

## Decision

- The engine exposes one set of JSON commands: define class, get bundle,
  create, set, transition, get, delete, query, sql, subscribe, and the
  administration commands.
- HTTP routes map one to one onto commands; the WebSocket carries the same
  commands plus subscription events; the C ABI exposes `fado_open`,
  `fado_exec(json) → json` and `fado_close`.
- The OpenAPI description is generated from the Go route table and committed
  under `api/`; official clients are generated from it.
- The command set is versioned (`/v1`) and gets a compatibility promise at 1.0.

## Consequences

- A client library written against HTTP works in-process through the C ABI by
  swapping the transport.
- Protocol conformance tests run against the Go API and the HTTP server with
  identical results.
- Binary efficiency is not a goal of the protocol; the hot paths are inside
  the engine, not on the wire. If a use case needs it, a binary encoding can
  be added as a second transport without changing the commands.

## Performance

Budget: the server adds under 200 µs at p99 to a transition over the Go API.
