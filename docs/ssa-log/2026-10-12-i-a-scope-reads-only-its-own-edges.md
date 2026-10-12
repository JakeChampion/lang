# 2026-10-12 — a scope reads only its own edges

`ssa_lift.lift_impl`. Refs #8171. No emitted byte changes.

## The shape

The lift records an edge for every `br` and `brif`: the block it leaves and
the slot values it carries, tagged with the uid of the scope it targets. At
the scope's `end`, a block or a loop gathers its edges for the exit merge or
the header phis. It found them by scanning every edge recorded since the
scope opened and keeping those with its uid.

That scan passed over every edge of every scope nested inside, already
consumed, and over every edge to a scope outside. Nested scopes made it
quadratic in the nesting depth. The typed lowering opens a block per match arm
and per statement scope, so this scan was the hottest loop in `lift_impl`.

## What changed

Each scope chains its own edges. `sc_edge_last` holds a scope's latest edge
by its position on the scope stack, which does not change while the scope is
open. `edge_link` points each edge at the one before it for the same scope.
`scope_edges` walks the chain back and returns the edges in the order they
were recorded, so predecessors and phi operands come out in the same order.

The scope uid existed only to tag edges, so `sc_uid` and `next_uid` are gone,
and `sc_edge_from` with them.

## Measured

The whole compiler under callgrind. Both compilers were built from the same
tree and compiled the same frozen copy of it, each on a warm memo of its own.

| | Ir |
|---|--:|
| before | 69.339 G |
| a scope's own edges | 69.216 G (−0.18%) |

`lift_impl` −117 M, `put_at` −10 M, against `scope_edges` +6 M.

The changed compiler compiles `checker.fern` and `fern.fern` to the same bytes
for x86-64 and arm64, and `checker.fern` for wasm. It also rebuilds itself
byte for byte.
