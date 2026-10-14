# 2026-10-14 — a closure call reads its target from the environment

Both native backends. `ssa.slot_calls` in `register_form`, and `ssa_call_ind`
in `asm_ir.fern` and `asm_arm64_ir.fern`. Refs #8171.

## The shape

A call through a function value passes the environment box first and reads
the body's address out of its slot 0 (`ssarc.body_address`). Once a constant
index became an immediate (2026-10-14-d), that read was one load, but into a
register of its own, so x86-64 still copied it into `%rdx` for `call *%rdx`.
The register also had to be allocated, and kept across whatever came between
the read and the call.

## What changed

`slot_calls` finds each call whose target is the unchecked read of slot 0 of
the environment it passes, when nothing else reads that target. It marks the
call (`str` "slot", `args[0]` repeating the environment) and drops the read.
The emitter moves the arguments, so the environment is in the first pool
register, and then calls through it: `call *8(%rax)` on x86-64, and
`ldr x6, [x0, #8]` before the `blr x6` on arm64.

## Measured

Stage-2 compile of `checker.fern`, callgrind, warm memo:

| | Ir |
|---|--:|
| main (e9fab346) | 13.997 G |
| this | 13.978 G (−0.14%) |

The compile makes 19.6 M calls through function values. The stage-2
compiler reproduces itself. In the self-host size bench, 7 programs shrink
on each of x86-64 and arm64, and none grows.
