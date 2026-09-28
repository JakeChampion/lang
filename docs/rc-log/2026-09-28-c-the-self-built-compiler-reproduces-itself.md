# The self-built compiler reproduces itself (`make distcheck`, #6644)

The measurement roadmap goal 2 was waiting on. `make distcheck` — the
compiler `make bootstrap` built recompiling the compiler — was OOM-killed at
13.9 GB on 2026-09-02 (`2026-09-02-param-strarr-elem-counted-share.md`) and at
13.3 GiB on 2026-09-05 (`2026-09-05-rcenum-strarr-payload-and-fresh-registry.md`),
and had not been run since. Run on this tree, with the typed lowering the
default, it completes, and the compiler it produces reproduces itself.

## Measured (4-core, 16 GB x86-64 container, pin `stage0-20260925-f81d8d7`)

| step | compiler | wall | peak RSS | binary |
|---|---|---|---|---|
| stage1 | the pin (native-built) compiling `fern.fern` | 307 s | 6.1 GB | 13,519,395 B |
| stage2 | stage1 compiling `fern.fern` | 209 s | not sampled | 12,321,496 B |
| stage3 | stage2 compiling `fern.fern` | 137 s | 5.7 GB | 12,321,496 B, `cmp` clean against stage2 |
| (reference) | `bin/fern`, native, compiling `fern.fern` | 38 s | 1.4 GB | 14,678,361 B |

stage2 runs the smoke (a one-liner and `coreutils/tr`) like stage1 does. Peak
RSS is `VmHWM` polled at 200 ms, so a transient above it is possible; the host
has no swap, so anything past 15 GB would have been a kill, not a sample.

## The script was measuring the wrong pair

`bootstrap.sh distcheck` asserted stage1 == stage2. stage1's code is what the
PIN generates for the current source; stage2's is what the current source
generates for itself. They can only match while no code generator change has
landed since the pin's `source` commit — three days and several rc-log entries
ago here — and on this run they differed from byte 25 and by 1.2 MB. That is
not nondeterminism and not a miscompile. The fixed point is stage2 == stage3,
which is what the script asserts now, printing the stage1/stage2 verdict as
information. `internal/bootstrap` pins both: a fake pin with an older code
generator passes, a drifting compiler fails at stage3.

## Where the memory went

The pin (native-built) compiling the compiler took 6.1 GB where the same step
measured 4.0 GB on 2026-09-02, and 307 s where it measured 79 s. That is the
typed semantic lowering being the default now, not a regression in the
runtime: `SELFHOST-SEMANTIC-SOURCE.md` measured the semantic self-build at
9.5 min / 8.4 GB against the AST self-build's 59 s / 5.5 GB, and the compiler
it produces at a twelfth of the memory. stage3, built by a self-built
compiler, then runs the same build in 137 s at 5.7 GB — under the native-built
pin on both counts. Goal 2's RECLAIM side, measured as this bootstrap, is at
parity.

## Witnessed

- `make distcheck` on x86-64 Linux, this tree, as above.
- `go test ./internal/bootstrap` (the script, with fake compilers).
- `tools/freeze_gate.sh` reads the CI wiring and prints precondition 1 GREEN;
  every precondition is green, and `NATIVE-FREEZE.md` records the state.

## Not covered

- arm64-linux: the same chain under `qemu-aarch64` was still in stage1 when
  this was written, and #10448 reports gen2 of the opt-in whole-compiler
  fixpoint segfaulting under qemu on 2026-09-27. The `verify-arm64-linux` CI
  lane on the arm64 runner is the authoritative run.
- arm64-darwin: stage2 exhausts the arena (#8479); the darwin pin stays
  native-built.
