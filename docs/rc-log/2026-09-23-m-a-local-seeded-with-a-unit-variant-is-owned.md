# A local seeded with a unit variant is owned

2026-09-23. Native. #10103.

```
var c: Pick = Pick.First;
c = Pick.Second(1);
```

On x86-64 and arm64 `-sanitize` the `Second` box was never freed. The
same shape seeded with a payload variant was freed.

## Cause

`rhsTainted` decides whether a local's initialiser could alias a borrowed
value. A local whose initialiser might alias is not `freeEligible`, so its
overwrite and its exit sweep fall back to the flat `__fern_rc_dec`, which
never frees. The qualified form `Pick.First` is an `ast.FieldAccess` whose
target is the enum's name. `rhsTainted` read it as a field read out of
something it could not place, and took the conservative taint. The bare
forms (`First`, `None`) are identifiers bound to nothing tainted, so they
were already clean.

A payload-less variant is the shared static sentinel, so it aliases
nothing, exactly like a string literal. `rhsTainted` now says so.
`builder.unitVariantRef` recognises the qualified form, and the lowering
and `exprType` read it through the same helper instead of spelling the
test out twice.

## Measured

Six trips per shape, `-sanitize`:

| shape | before (x86-64) | now (x86-64 and arm64) |
|---|---|---|
| scalar payload, `Pick.First` then `Pick.Second(i)` | leak | balanced |
| the same, handed to a call | leak | balanced |
| string payload | leak | balanced |
| `Option.None` then `Some(…)` | leak | balanced |
| reassigned in a callee and returned | balanced | balanced |

A probe running all five in one loop went from 3 of 21 blocks freed
(480 B live) to 21 of 21. wasm was already balanced on every shape.
`TestEnumLocalReassignedFromAUnitVariant` holds the rows on x86-64 and
arm64 under the sanitizer and the answers on wasm. The returned row pins
that the local, now eligible, still moves on return.
