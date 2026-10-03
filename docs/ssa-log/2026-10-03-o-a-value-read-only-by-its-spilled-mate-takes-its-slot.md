# 2026-10-03 — a value read only by its spilled mate takes its slot

`ssa.regalloc_linear` and `ssa.sole_readers`, both native targets. Refs
#8171.

## The shape

A loop that carries more values than the callee-saved registers hold
spills some of its phis. When one of them changes on only some
iterations, the merge after the change is a second phi, and its only
reader is the loop phi on the back edge. That merge still took a
register. The path that left the value alone loaded the loop phi's slot
into it, and the back edge stored it straight back:

```
.Lssa_keep_5:
    movq -64(%rbp), %r9
.Lssa_keep_4:
    ...
    movq %r9, -64(%rbp)
```

On the stage-2 compile of `checker.fern`, loads that fall through to a
store back into the same slot ran 77 M instructions. A quarter of that was
in `ssa_lift.lift_impl`, whose block-exit loop carries the slot table, the
pending instructions and the value counter through iterations that mostly
change none of them.

## What changed

Before a value is placed, the scan checks its phi mate. When the mate is
already spilled, the value has no reader but the mate, and the two never
live at once (`mate_interferes`, the test the slot hints already use), the
value is spilled too. The slot hints then give it the mate's slot. The
unchanged path moves nothing, and the changing path stores into the slot
where the back edge used to. `sole_readers` gives each value the one
instruction that reads it, or marks it as read by several or by a
terminator.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 624126a6 and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.667 G | 24.614 G (−0.21%) |

`std/crypto`'s five digests over 4 MiB: 1.720 G to 1.719 G, digests
unchanged. Static load-then-store-back sites in the stage-2 binary fall
from 1,003 to 406.

Emitted bytes change on 327 of the 1,968 `selfhost-emit-hashes` rows: 255
x86-64 and 72 arm64. wasm does not use this allocator.

## Witnessed

`TestSelfHostSpilledMateSlot` is new. A loop carries six values through
calls, and a seventh changes on one iteration in five. The listing must
spill something and must hold no load of a slot that falls through to a
store back into it. The program must exit 42 on every host target. Also
run: the phi, regalloc, physical-rc, lea, leaf-frame and spill suites in
`internal/e2eselfhost`.
