# x86-64 meets every flip condition too, and the default is taken

`2026-09-17-arm64-meets-every-flip-condition.md` took the flip on arm64 and
left x86-64 on the stack machine, failing the size condition at 1.25x the
instructions and 1.26x the binary. #9683 folded the trivial phis and took that
to 0.92x. Re-measured after it, on `3af57f6e`, x86-64-linux native:

## Smaller output

Whole compiler, `-emit asm` and the linked binary, self-host driver compiling
`fern.fern`:

| | flat | ssa | ratio |
|---|---|---|---|
| instructions | 3,258,709 | 2,982,469 | **0.92x** |
| linked binary | 14,506,120 | 11,631,040 | **0.80x** |

The condition is "at or under flat's". Both are under.

## Faster output

The compiler built with `-backend ssa` against the one built with
`-backend flat`, each compiling `checker.fern` **with the same backend** so the
only difference is the compiler's own code. Best of three:

| | time |
|---|---|
| flat-built | 18,879 ms |
| ssa-built | 12,296 ms |
| | **0.65x — 35% faster** |

## Faster compile

Self-build, one run each: 158.6 s on `-backend flat`, 170.7 s on
`-backend ssa`. **1.08x** against a stated ceiling of 1.5x with parity the aim.
arm64 measured 1.04x.

## The corpus

Swept here, natively rather than under qemu: every conformance case built with
the self-host driver on both backends for x86-64-linux and run.

| | cases |
|---|---|
| agree on exit status and stdout | **519** |
| diverge | **0** |
| skipped | 73 |

The skips are cases whose `backends` file does not list `x86_64`, plus the few
the stack machine itself does not compile — neither says anything about the
register path. #9683 reported 522 agreeing under qemu on the same code, which
is the same result through a different skip rule.

The two gates that bracket it are green on the flipped tree: the backend gate
(`TestSelfHostSSABackend*`) and the stage-2 fixpoint
(`TestSelfHostStage2Compiler`, `TestSelfHostStage2Bootstrap`,
`TestSelfHostPerModuleEmitAllFixpointX86_64`) — the fixpoint being the one the
flip actually moves, since stage 2 is now built by the register path.

## What changed

`run()` computes the register path's availability once and uses it for both the
refusal and the default, so the two cannot drift: the default is the register
path wherever there is one, which is exactly the pair the refusal names. Before
this, the availability test and the default test were separate expressions over
the same ISA names.

`TestSelfHostSSABackendRefusesOtherTargets` pinned the per-target rule by
branching on an `arm64-` prefix. Both native ISAs now take the same side, so
the branch is gone — and the half it could never reach is now covered: wasm's
default must be the stack machine, which the test asserts directly rather than
leaving implied by the `-backend ssa` refusal above it.

## The trap

The size condition reads as a property of the register allocator and is not.
`2026-09-17-spill-the-longer-interval.md` blamed x86-64's gap on the register
budget; `2026-09-17-the-register-budget-is-not-the-x86-gap.md` disproved that
with one experiment, and #9683 then found the cause — surplus phis the lift
emits per live local at every merge, which no budget was going to absorb. The
two register backends had agreed to within half a percent the whole time. A
condition that looks like a backend's can belong to the layer above it.
