# 2026-10-03 — a user function lends only what it lends on

`ownership.lends`, `semsource.infer_rows`. Refs #11204.

```fern
function seen(c: C, v: i64): i32 { ... reads c ... }
function accept(c: C, v: i64): C {
  if (seen(c, v) > 0) { return c; }
  return add(c, v);
}
```

`accept`'s `c` stayed borrowed. A slot the callee leaves uncounted is LENT
when the callee's row `lends` (`lent_values`), and a lent parameter is never
counted. Every produced function's row was seeded `lends: true`, so a reader
whose result holds no reference at all, here `seen`, lent `c` as much as one
returning a view of it.

## The rule

A produced row's `lends` is inferred in the same rounds as its bits, starting
from false and only ever turning true. A function lends when one of its
parameters is lent to a callee that lends (builtins included, through their
contract rows), or when it returns a view, which counts nothing it reads.

## Measured

`TestSelfHostHeldConnectionsHeapBoundX86_64`, bump bytes per held
connection, second batch of 64:

| shape | before | after |
| --- | --: | --: |
| accepted, never used | 643 | 568 |
| kept alive after one request | 10,122 | 2,483 |

The kept figure came from `__serve_accept`: `__conns_from_peer(c, peer)` (an
`i32`) lent the connection table, so `__serve_accept`'s `c` stayed borrowed
and its call to `__conns_add` was bracketed field by field. Once a served
request has left the table's box shared, each bracket retains all fifteen
arrays and every append copies one at its exact length. With the inference
the bracket is gone. The kept figure is still over #9854's 1 KiB; the next
lead is the `.with` copies `__serve_read` and `__body_deadline` make on a
record they rebuild from a borrowed parameter.

## Why the seed was `true`

The 2026-09-21 entry's fourth rung: counting `semsource.stmt`'s `st` crashed
the compiler when its arms handed payloads to lent slots. With every user
row inferred `false`, the whole-compiler gate now passes. That crash's
mechanism was the take family #11212 belongs to, and #11212 is fixed. The
inference keeps the conservative half of that rule anyway: a parameter lent
onward to anything that lends still makes its function lend.

## Witnessed

- `TestSelfHostOwnershipInference/modes-in-the-emitted-code`: `checked_keep`
  is counted (it fails on the seeded rows), and `view_keep`, which reads its
  record through a function returning a view of it, stays borrowed.
- `TestSelfHostSemanticSourceRC`: `views_kept` reads three views returned
  through user functions, directly, through another user function, and
  through `slice_unchecked`, of strings that are dead at the call. It runs on
  x86-64, under the sanitizer, on arm64 and on wasm.
- `TestSelfHostSemanticWholeCompilerX86_64`: gen1 coverage, byte-identical
  output, fixpoint.
