# Four eyes: the one who filed it may not approve it

A payment is prepared by one person in finance and approved by
another. Nobody approves a payment they created themselves.

## The class

```json
{
  "name": "Payment",
  "title": "\"Payment to \" & payee",
  "variables": {
    "payee":  {"type": "text", "required": true},
    "amount": {"type": "money", "scale": 2, "required": true}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected"], "final": ["approved", "rejected"]},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed", "guard": "amount > 0"},
    {"name": "approve", "from": "filed", "to": "approved"},
    {"name": "reject",  "from": "filed", "to": "rejected"}
  ],
  "access": {
    "read":   "INGROUP(\"finance\")",
    "create": "INGROUP(\"finance\")",
    "update": "INGROUP(\"finance\") AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {
      "file":    "CREATEDBY() = USER()",
      "approve": "INGROUP(\"finance\") AND CREATEDBY() <> USER()",
      "reject":  "INGROUP(\"finance\")"
    }
  }
}
```

The second pair of eyes is one condition on `approve`:
`CREATEDBY() <> USER()`, the caller is not the one who created the
payment.

## What it does

Fay and Fred are both in the group `finance`. Fay prepares a payment
and files it.

| Who | Does | Answer |
|---|---|---|
| Fay | approves her own payment | refused, `forbidden` at `/access/transition/approve` |
| Fay | asks what she may do with it | `approve`: not allowed, `forbidden`; `reject`: allowed |
| Fred | approves it | allowed |

The log then says: created by Fay, filed by Fay, approved by Fred.

## How it works

- **`CREATEDBY()` is kept by the engine.** Nobody can set it, so the
  condition cannot be talked around by changing a value.
- **The listing says so beforehand.** A screen, or an agent, asks
  which transitions a caller may take and shows `approve` as closed to
  Fay without her having to try.
- **`USER()` is the one who acts, whoever they act for.** A substitute
  who filed a payment may not approve it in the name of somebody else
  either. See [Let somebody act for a person](acting-for-somebody.md).

## Variations

| The rule | The condition on `approve` |
|---|---|
| the one who last changed the draft may not approve | `LASTEDITOR("draft") <> USER()` |
| the one who filed it may not approve | `LASTENTEREDBY("filed") <> USER()` |
| an agent may prepare a payment, and only a person approves | `USERKIND() = "person" AND CREATEDBY() <> USER()` |
| above a limit a manager approves | `CREATEDBY() <> USER() AND (amount <= 10000 OR INGROUP("managers"))` |

See also: [The class definition, Access](../class.md#access).
