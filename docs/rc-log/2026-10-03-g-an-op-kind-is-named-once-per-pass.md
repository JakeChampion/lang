# 2026-10-03 — an op kind is named once per pass

`ssarc.aligned_rc`, `ssabytes.lower_reads`, `ir.licm_header_end` and the
expiry in `ssa.regalloc_linear`. Refs #8171. No emitted byte changes: the
stage0-built compiler before and after emits the fixed older tree
(`examples/self_host/fern.fern` at 1ae9cad, its bindings spelled `let`)
and that tree's `checker.fern` byte for byte.

## What the profile named

`ir.kind_id` turns a kind's name into its id through a dispatch on the
name's length and a string compare. It was 195 M of self cost in the
stage-2 compile of `checker.fern`, nearly all of it from two passes that
asked it for `"call_direct"` once per op: `aligned_rc` (1.05 M calls) and
`ssabytes.read_value` (1.03 M calls), which `lower_reads` runs over every
op three times. `licm_header_end` asked `is_terminator_kind`, which names
three kinds, for every op it scanned.

`regalloc_linear` rebuilt its three active-interval arrays for every value
it placed, whether or not any interval had ended.

## What changed

Each of those passes names the kinds it compares against once, before its
loop; `read_value` takes the id. The expiry rebuilds the active arrays only
when some interval ends before the value being placed.

`std/float`'s `expm1` spells its shifted literal `0x200000 as i64`, as
#11254 does: the pinned stage0 types `0x200000 >> k as i64` by its left
operand, `i32`, and refuses it as an `i64` argument, so every self-host
driver whose closure reaches `std/float` failed to build on main.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind, on
main at 1f3897a8c:

| | main | this change |
|---|--:|--:|
| total Ir | 20.826 G | 20.584 G (−1.16%) |
| `ir.kind_id` with its per-length helpers, self | 256 M | 72 M |
| `ssa.regalloc_linear`, self | 274 M | 247 M |

A candidate list built per value in `regalloc_linear` was measured too and
left alone: replacing it moved the total by 2.6 M.

## Witnessed

Both emit identities; the byte-view, view, SSA, reference-count and spill
tests of `internal/e2eselfhost` (`TestSelfHostStrViewBorrowRelease*`
among them, red on main before the `std/float` line); `make
check-sources` and the lint ratchet.
