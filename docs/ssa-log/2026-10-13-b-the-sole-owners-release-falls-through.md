# 2026-10-13 — the sole owner's release falls through

`asm_ir.emit_ir_runtime` and `asm_arm64_ir`: `__fn___fern_arr_dec` and
`__fn___fern_str_free`. Refs #8171. Emitted bytes change in the two runtime
helpers only.

## The shape

`__fern_arr_dec` releases one array box. On the stage-2 compile of
`checker.fern` it runs 19.6 M times, and 19.3 M of those find a count of 1
and free the box. The helper tested the rare cases first: the immortal
sentinel (a sign test and branch), then a compare against 1 with two
branches off it, before reaching the free. The free then compared the class
against the large tier's 65,536 words before the compare against 256 that
sends a block to `__fern_capw`, so every small block paid both compares.
Twenty instructions on the common path.

## What changed

- One compare against 1 and a `jne` send everything but the sole owner's
  release to a side path, which sorts out the immortal sentinel, the shared
  decrement and the double free there.
- The compare against the large tier moves onto the over-256-word path,
  beside the class call that path already makes, so a small block pays one
  compare. `freelist_push_classed` is the push without the class step.
- `__fern_str_free` takes the same two changes: its count test, and on the
  fused block (nearly every heap string) the large-tier compare.
- arm64 mirrors all of it, with the compare's flags carried past the `tbnz`.

Sixteen instructions on the common path of `arr_dec` on x86-64, from twenty.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo. The base is the shape-immediate entry
(the one before this).

| | Ir |
|---|--:|
| base | 14.582 G |
| the sole owner's release falls through | 14.496 G (−0.59%) |

The text machines that pin the two helpers' bodies (`docs/TEST-GATES.md`,
`TestSelfHostRetainedNativeBodies`) follow the new order: the ISA controls
re-pin the instruction-kind sequences, the controls that mutated the shared
path's `testl` or the retired `.L*_dec` label mutate the count-one compare
and `.L*_shared` instead, and the large-array machine expects the block base
the sole-owner path now computes before the tier branch. This slice first
landed without those and #12312 pulled it.

A trap, recorded because the gates let it through: the first draft of
`str_free`'s side path tested the zero count with `jl` after a `testl`,
which can never be taken, so a double free would have decremented the dead
count to −1 and marked the block immortal. Nothing in the sanitizer suite
reads the plain build's underflow counter for a string, so
`TestSelfHostUnderflowCounted` now does, for an array and a string on both
register targets.
