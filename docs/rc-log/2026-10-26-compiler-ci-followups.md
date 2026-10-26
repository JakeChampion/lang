# Compiler CI follow-ups

Date: 2026-10-26. PR: #13561.

The source commit is `d92821ddb32b4477795019dad63aa374ca68b06f`.
It includes main at `7b457eae4405762bcbe66a558a6060366276b8fe`.
The official stage0 pin is `stage0-20261025-433d93a`.

The IR verifier now uses physical ABI argument counts for imported calls.
The Wasm shard driver retains the ABI metadata for each function range.

The ABI repair passed both native whole-compiler tests.
Those tests also exercised self-compilation.
The ABI repair passed the Wasm whole-compiler shard link.
The native compiler reached a fixed point across two complete module builds.
These tests precede the reachability repair.

Reachability retains constant facts across scalar parameters.
Roots and function values keep unknown parameter facts.
An early pass preserves record facts before scalar layout removes their source operations.
The constant, mixed, unknown, and function-value controls pass on all three targets.
The previous false-branch and closure-returned record regressions also pass.

The remaining heap fixtures now retain actual heap objects.
The retained producer fixture keeps its original payload width through whole-record observations.
Every malformed graph and metadata assertion remains in place.

The static-box test now records zero allocations for 100 rebuilds.
Its computed value remains 5450.
The library now supplies the error and display interfaces for `InflateReadError`.
The IR specification also includes the `count_below_u64` stack effects.

## Bootstrap and driver sizes

All three bootstrap stages use the same source tree.
The final two stages have identical bytes.
Their SHA-256 is `f3be8832b47eae41078e693ba6bee169a5aed62bd739772b97cf7aa58ea8cfa8`.
Each of the ten drivers uses the official stage0 pin.
The strict size gate passed for all 11 rows.

| Driver | Previous bytes | Measured bytes | Change |
| --- | ---: | ---: | ---: |
| `fern.fern/stage2` | 14885376 | 14885344 | -32 |
| `fern.fern` | 14976176 | 14980272 | +4096 |
| `drivers/asm_load_run.fern` | 11956096 | 11964288 | +8192 |
| `drivers/asm_modload_run.fern` | 10604576 | 10608672 | +4096 |
| `drivers/asm_ir_run.fern` | 10201088 | 10205184 | +4096 |
| `drivers/asm_run.fern` | 9359760 | 9363856 | +4096 |
| `drivers/wasm_ir_run.fern` | 9303008 | 9307104 | +4096 |
| `drivers/wasm_run.fern` | 9307264 | 9311360 | +4096 |
| `drivers/wasm_runio_run.fern` | 9288640 | 9288640 | +0 |
| `drivers/asm_pathprobe_run.fern` | 6199936 | 6204032 | +4096 |
| `drivers/checker_modload_run.fern` | 3295600 | 3295600 | +0 |

## Worker qualification

The final compiler produced a different worker binary after the reachability repair.
Its SHA-256 is `33e30c6a6cffbad68db72916788206f72a8aca6433a6fe3f384f5ef8bc2cda78`.
The local cgroupfs run passed all 27 cases, with three kernel OOM kills.
Every memory, CPU, process, cleanup, and deadline assertion remains unchanged.
The source hashes did not change during the run.

The earlier record retains the binary identity control for the original systemd failure.
The current record separately tests the worker from this compiler.
These records do not qualify production workers or aggregate memory limits.

## Validation state

The 290 targeted compiler cases pass, with no skips.
All 48 follow-up cases pass, with no skips.
All seven retained-body cases pass across both drivers and all three targets.
Source lint passes all 201 cases, with no skips.
Vet and format checks pass.

The broader compiler and whole-compiler tests remain in flight on this source.
The debug-variable repair remains separate and incomplete.
Full CI must pass on the final head before merge.
