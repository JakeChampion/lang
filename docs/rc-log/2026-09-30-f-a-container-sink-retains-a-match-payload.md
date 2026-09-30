# 2026-09-30 — a container sink retains a match payload (#10696)

Native (`internal/ir`: `sinkRetainsArg`, `mapSetValueCountedBy`,
`mapSetKeyCountedBy`).

`borrowedCallArg` refuses `__method_Array_push` and `__method_Map_set` through
`calleeRetainsAnyArg`. That refusal is right for a borrowing callee, but it
left the payload's own count with no releaser. Both sinks store an aliased
argument counted (`emitArrayPush`'s alias inc, `emitMapSetRetains`), so the
arm owes that release exactly where the sink retains. The map predicates take
the alias-inc fact as a parameter, because a match binding is out of
`exprType`'s scope when the analysis asks.

## Measured (100 matches, allocs / frees)

| arm body | x86-64 before | x86-64 after | wasm after |
|---|---|---|---|
| `xs = xs.append(v)` | 210 / 110 | 210 / 210 | 310 / 310 |
| `m = m.insert("k", v)` | 204 / 104 | 204 / 204 | 504 / 504 |
| `m = m.insert(v, i)` | leaked | 202 / 202 | 402 / 402 |

`__rc_underflow_count()` is 0 in every row, and the programs fold it into
their exit. `TestLeakCheckPairPayloadSink{X86_64,Wasm}`; all six subtests fail
without the change.
