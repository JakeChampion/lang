# 2026-10-03 — a named type's unique release frees without a frame

`asm_ir`'s `ssa_release_entry` and `ircore.nominal_release_drop`, x86-64
only. Refs #8171.

## The shape

`__sem_release_<T>` releases a value of a named type. Its register entry
already settles three cases in a frameless head: a pointer below 0x10000
or a static cell is kept, and a count above one is decremented. A count of
one or zero fell through to the body, which builds a frame, reloads the
argument, calls `__fern_rc_is_unique`, branches on the answer, calls the
type's `__sem_drop_<T>` and then the free.

Those slow entries are 13.8 M across every `__sem_release_*` on the
stage-2 compile of `checker.fern`. Nearly all of them are a sole owner
freeing its box.

## What changed

When the release body is the plain counted shape (`is_unique`, `if`,
drop, `end`, then the free), `ircore.nominal_release_drop` names its drop
helper. The head then tests for a count of exactly one and, holding the
box on the stack, calls the drop and then `__fern_arr_dec`. The one push
also aligns both calls. A count of zero still takes the body, whose helper
reports the underflow, and a body of any other shape keeps the old head.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 9b7dc74d and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.444 G | 25.202 G (−0.95%) |
| every `__sem_release_*`, self Ir | 967 M | 718 M |
| `__fern_arr_dec` and `__sem_drop_ast__Expr`, self Ir | unchanged | unchanged |

Emitted bytes change on 301 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64. Both sides refuse the same 252.

## Witnessed

`TestSelfHostReleaseUniqueTail` is new. A struct owning a string and an
array is built and dropped in a loop. Its release helper must call the
drop and then `__fern_arr_dec` before any `pushq %rbp`, and the program
must exit 42 on every host target. Also run: `TestSelfHostSemanticProduction`,
`TestSelfHostSemanticSourceRC`, `TestSelfHostSSA*`, `TestSelfHostMap*` and
`TestSelfHostRcTrace*`, the stage-2 build and its compile of
`checker.fern`, the emit-hash sweep, the lint ratchet and `make fmt-check`.
