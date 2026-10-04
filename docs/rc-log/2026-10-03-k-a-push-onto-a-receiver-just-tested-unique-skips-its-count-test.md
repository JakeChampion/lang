# 2026-10-03 — a push onto a receiver just tested unique skips its count test

`ssarc.append_push`, the lift of an owned push, and the x86-64 and arm64
inline pushes. Refs #8171. The emitted code changes, so there is no emit
identity. The compiler this tree builds through the pinned stage0 builds
itself, and that build reproduces itself byte for byte.

## What the profile named

An append onto an array the frame owns tests `__fern_rc_is_unique` and, on
the unique arm, makes the consuming push. That push's inline fast path then
tested the count again — load it, compare with 1, test the immortal bit —
before writing in place. A loop growing an array one element at a time ran
about 27 instructions an element, five of them that second test.

## What changed

- **The unique arm's push is marked.** `append_push` sets `i32_imm` 1 on the
  owned push it makes on the unique arm, and the lift carries the mark in
  the instruction's `imm`.
- **The marked push tests capacity only.** The x86-64 and arm64 inline
  pushes leave the count test out for a marked push: `__fern_rc_is_unique`
  returned true, so the count is 1 and the box is not immortal. The shared
  arm's push keeps its test.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | after `2026-10-03-j` | this change |
|---|--:|--:|
| total Ir | 20.025 G | 19.835 G (−0.95%) |

A loop appending 403 elements to a fresh array, 10,000 times: 110.8 M to
99.0 M (−10.7%).

## Witnessed

`TestSelfHostUniquePushSkipsCountTest` checks that the x86-64 and arm64
listings of such a loop keep exactly one push count test, the shared arm's,
and runs the program on x86-64, arm64 and wasm. The 305 append, push,
array, reference-count, sanitizer, leak and reuse tests of
`internal/e2eselfhost` pass, and stage 2 == stage 3.
