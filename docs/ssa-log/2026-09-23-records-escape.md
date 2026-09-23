# Scalar replacement and stack allocation have almost nothing to take

No slice landed from this. It is here so nobody builds it twice.

Before building scalar replacement of aggregates (a record or tuple built,
read field by field and dropped, becomes its fields) or stack allocation of
a box that never escapes, the typed semantic path's functions were counted
for candidates, 2026-09-23 — every `record_new` / `tuple_new` in a produced
body, and which of them are used only by field reads (SROA), or only by
field reads and as an argument to a call (an upper bound for stack
allocation, taking every callee to borrow without retaining):

| subject | constructions | read-only | read or lent |
|---|---|---|---|
| `examples/self_host/checker.fern` | 1,205 | 1 | 17 |
| `examples/bench/record_update.fern` | 2 | 0 | 0 |
| `examples/bench/struct_drop.fern` | 4 | 0 | 0 |
| `examples/bench/pvec_with.fern` | 11 | 3 | 3 |

A record in this compiler is built to be returned or stored into another
(`LowerState { ...s, ops: … }` threaded through every call), so it escapes
by construction; even with a perfect interprocedural escape analysis the
bound is 1.4% of constructions. The allocation that costs is the functional
update's copy, which is Perceus reuse's to remove (roadmap goal 2), not an
escape analysis's. The one shape the table misses — a record passed to a
small callee that only reads it — becomes local only after inlining, and
inline_tiny_leaves splices only call-free bodies, so the construction's
drop is still a call there.
