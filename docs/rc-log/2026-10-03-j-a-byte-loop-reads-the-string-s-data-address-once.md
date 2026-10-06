# 2026-10-03 — a byte loop reads the string's data address once

`ssarc`'s unchecked string byte read and `ir.hoist_loop_invariants`. Refs
#8171. The emitted code changes, so there is no emit identity. The compiler
this tree builds through the pinned stage0 builds itself, and that build
reproduces itself byte for byte.

## What the profile named

`util.hash_bucket` was 419 M of self cost in the stage-2 compile of
`checker.fern`, about seven instructions for each byte it hashes. One of the
seven reloaded the string's data address from its box: an unchecked `s[i]`
lowered to the single `str_index` op, and the register backends turn that op
into a load of the address followed by the byte load, on every read.

## What changed

- **The read is two ops.** An unchecked byte read lowers to the string's
  `str_data`, the index and `raw_load8`, which the register backends emit as
  one `movzbl (base,index)`.
- **The address is hoisted with the length.** `hoist_loop_invariants`
  already lifts a header's `str_len` of a string the loop never stores to.
  It now also lifts that string's `str_data`, from anywhere in the loop,
  into a slot of its own. The header reads the length on every path into
  the loop, so the box is valid there and the hoisted read is one the
  program makes anyway. A string the header does not read keeps its address
  read in the loop.
- `op_str_index_nc` has no caller left and is deleted.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | main at 26b82ea7c | this change |
|---|--:|--:|
| total Ir | 20.087 G | 20.025 G (−0.31%) |
| `util.hash_bucket`, self | 419 M | 382 M |

The thirty `bench` programs: +0.0014% in total, with every exit
status unchanged. Only `http_hello` and the seed-dependent `map_string` and
`utf8_ingest` rows move.

## Witnessed

`TestSelfHostIRLICM` gains three cases: the address hoisted beside the
length, and refused for a string the header does not read and for one the
body stores to. `TestSelfHostTypedLICMDataAddress` checks that the x86-64
loop reads each byte with one base+index load, and runs the program on
x86-64, arm64 and wasm. The other LICM, typed-fold and string tests of
`internal/e2eselfhost` pass, and stage 2 == stage 3.
