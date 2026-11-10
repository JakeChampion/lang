# Checked value types

Status: [policy] explicit value declarations.

`@value` declares a record whose fields contain only immutable copyable data.
The checker validates every field, including nested records and enum payloads.
A consuming receiver method does not make this record a resource handle.
For example, a cleanup method can release one holder of a byte array.

```fern
@value
struct Key { bytes: u8[] }
```

Numbers, strings, owned arrays, tuples, and enums can contain value data.
Recursive data remains valid when every other field satisfies the same rule.
Resource handles, mutable cells, callbacks, borrowed views, and consumption obligations cannot occur inside a value record.
A field with an unconstrained type parameter also fails this proof.
The declaration gives no ownership to a resource that another value retains.

A borrowed value record can supply an independent holder to an `own` parameter.
The ordinary reference-count and copy-on-write rules preserve its other holders.
An explicit `own` binding still follows the move rules after a transfer.
The declaration does not remove E050 or permit a second close of a resource handle.

Copyability does not mean that cleanup erases every copy of a secret.
`crypto.erase` clears uniquely held storage before release.
Shared storage retains its bytes for the other holders.
See [the cryptographic audit](security/CRYPTO-AUDIT.md) for the secret-erasure limits.

| Claim | Contract |
| --- | --- |
| VT-01 | A value cleanup preserves a retained COW alias. |
| VT-02 | A value declaration refuses a resource handle, including a nested handle. |
| VT-03 | A value declaration refuses mutable cells and callbacks. |
| VT-04 | A value declaration refuses views and unconstrained field types. |
| VT-05 | A value declaration refuses consumption obligations. |
