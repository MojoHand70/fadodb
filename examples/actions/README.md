# Sample action definitions

Action definitions in JSON, the same document the Go API's `DefineAction`
takes (ADR-0045). `TestExampleActions` in `fado/outbound` defines every
file and sends it to a local receiver on each push, so a sample that no
longer holds against the engine fails CI.

**Status on 2026-11-10:** one sample, the generic webhook. By the
owner's ruling of that day the repository carries no definitions for
particular services: those come with real needs, and
[`docs/actions.md`](../../docs/actions.md) says how to write one for any
REST API. A format for publishing several definitions together, a
package, is planned and waits for the first real integration.

## `webhook.json`

`webhook.send` sends the event that fired a rule to one receiver, as the
signed call of [`api/call.schema.json`](../../api/call.schema.json). It
names no host: the address and the signing key are secrets of the
connection `webhook`, which the host program supplies and whose hosts it
lists.

```go
out, err := outbound.Allow(outbound.Options{
    Connections: map[string][]string{"webhook": {"hooks.example.com"}},
})
db, err := fado.Open(path, fado.Options{
    Outbound: out,
    Secrets: func(ctx context.Context, connection, name string) (string, error) {
        return vault.Get(ctx, connection, name) // "webhook", "url" or "signing_key"
    },
})
```

A rule then says when to send, and what to keep of the answer:

```json
{"on": "enter", "state": "approved",
 "do": [{"action": "webhook.send",
         "args": {"topic": "\"invoice.approved\""},
         "into": {"sent": "OK", "send_error": "ERROR"}}]}
```

A second receiver is a second definition: copy the file, give it another
name and another connection.
