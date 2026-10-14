# 2026-10-13 — an owned first append skips the frame

Both native targets. `asm_ir` / `asm_arm64_ir`: the `__fern_arr_push_owned`
runtime body. Refs #8171.

## The shape

`__fern_arr_push_owned` is the append whose receiver dies at the call, so a
grown copy frees the old buffer. Once its inline fast path declined, its
slow path:
- built a frame;
- called the shared `.Larr_push_slow`;
- checked whether the old box needed freeing;
- took the frame down.

On a stage-2 compile of `checker.fern`, 2.78 M of its 4.95 M slow calls were
the first append to the static empty array, `[]`. `.Larr_push_slow` already
gives that case a frameless path that pops a four-slot block, and the old box,
a static literal, is never freed. The frame, the call and the free test cost
about fifteen instructions each time and did nothing.

## What changed

Outside the debug build, the slow path first tests for an empty receiver with
no capacity and a negative count, the static sentinel. That receiver leaves
through a jump to `.Larr_push_slow`, which returns to the caller itself.

- An empty heap array with a count of one still takes the frame path, because
  its box must be freed after the copy. That is why the count is tested and
  not only the length and capacity.
- arm64 mirrors it, reaching `.Larr_push_slow` with a plain `b` so the branch
  range never matters.

## Measured

The stage-2 compile of `checker.fern` under callgrind, without `-g`, warm
certifier memo, both compilers built from the same main:

| | Ir |
|---|--:|
| main (with #12349) | 14.311 G |
| the owned first append | 14.287 G (−0.17%) |

The stage-2 compiler recompiles itself byte for byte.

`scripts/perf-bench` x86-64 re-banks `struct_drop.ir`, which falls 2.55%.
The six-instruction test in every runtime's owned append grows the `.text`
of the programs that append by under 0.6%.

## Witnessed

`TestSelfHostArrPushFastPath` now requires `.Lapo_slow` to reach
`.Larr_push_slow` before it builds a frame, on both targets, and still
requires the debug build to frame first. The append, append-reclaim and
alloc fast-path suites pass on every target.
