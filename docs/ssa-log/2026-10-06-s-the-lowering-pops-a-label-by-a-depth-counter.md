# 2026-10-06 — the lowering pops a label by a depth counter

`ssarc.pop`, the close of every `block`, `loop` and `if` scope the
register lowering emits. Refs #8171. No emitted byte changes: the
compiler before and after builds `checker.fern` for x86-64, arm64 and
wasm, and `fern.fern` for x86-64, byte for byte.

## What changed

The lowering's emit state carried the open labels as an array, innermost
last. A push appended; a pop built a fresh array of every label but the
last, one append per open scope, and the search a branch makes for its
target's depth started from the array's end. On the compiler's own
functions a scope closes inside twenty or thirty open ones, so each of
the 42 k pops on a `checker.fern` compile copied that many entries.

The state now carries a depth beside the array: a push writes at the
depth (overwriting what an earlier scope left there) or appends when the
array has no room, a pop is a decrement, and the search starts from the
depth. The entries past it are stale and nothing reads them.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.777 G (−0.51%) |
| `ssarc.pop`, self | 71.2 M | 2.8 M |
| `__fern_arr_push`, self | 290.6 M | 281.2 M |

## What is left

`ssarc.emit` and `ssarc.lower` build the op list one append at a time
through `irtables.LowerResult`, a record rebuilt per op; the record
spreads are what `__fern_arr_box` sees from this pass.
