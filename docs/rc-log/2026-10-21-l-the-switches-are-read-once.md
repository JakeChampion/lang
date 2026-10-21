# 2026-10-21 — the switches are read once

`optbisect.switches`, `ssaunits.Reuse.disabled`, `irverifygate.Gate.verify`
and `.certify`. Refs #8171.

## The shape

Reading an environment variable walks the process environment comparing
names, about 1,500 instructions a read on this box, and the compiler asked
per function: `optbisect.allowed` read `FERN_OPT_TRACE` and
`FERN_OPT_BISECT` for every decision in every pass that asks one,
`reuse_layer_disabled` read those two and `FERN_SELFHOST_NO_REUSE` from four
places in the lowering, and the IR gate read `FERN_IR_VERIFY` and the
certify mode per function. 276 M instructions of `__fern_env` on the whole
compiler's self-compile, 175 k reads.

## What changed

A loop over the module's functions reads `optbisect.switches()` once and
asks `allowed_by` per function; `with_reuse` reads the reuse switch once per
function and records it on the plan's `Reuse`, where the lowering's three
readers find it; the gate reads its two switches when it is built. The
analyses' own `verify_on` reads (`ssadeps.analyze`, `ssasem.schema_error`,
`ssarc.validate`), 45 k of the reads, stay: they have no record to ride.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the live-type-names slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.486 G | 74.377 G (−0.15%), then 74.351 G (−0.03%) with the gate's |

The whole-compiler binary is byte-identical to the base compiler's after
each commit.

## Measured and not landed: the reachability walk as a worklist

`semreach.walk` was 832 M on the same compile, flat across its loop: each
round walks every executable block until a round changes nothing, and the
instruction loop ran 8.3 million times, about eight visits per instruction.
A use-driven worklist (a block walked again when a value it reads moves or
an edge into it is taken, with a per-walk table of each value's reading
blocks) reaches the same fixed point and measured 74.351 G → 74.455 G
(+0.14%), binary identical: the rounds converge in two or three, and the
table and queue cost more than the visits they save. Dropped.
