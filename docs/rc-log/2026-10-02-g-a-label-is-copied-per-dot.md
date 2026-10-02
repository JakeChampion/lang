# 2026-10-02 — a symbol name is copied per dot, not per byte

`asmcore.sanitize_label`. Refs #8171.

Every call and every function label the x86-64 SSA emitter writes goes
through `sanitize_label`, which turns `module.name` into `module__name`. It
rebuilt the whole name one byte at a time, a concatenation per character: 1.3
million calls on the stage-2 compile of `checker.fern`, at about 25 characters
each. It now hands a name without a dot back unchanged, and otherwise finds
each dot with `__memchr` and copies the run before it.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
is the compiler the self-host compiler builds from the same commit.

| | main (00ffd2c) | this change |
|---|--:|--:|
| stage 2, total Ir | 37.85 G | 37.72 G (−0.36%) |
| stage 2, `sanitize_label` self Ir | 55.5 M | 7.9 M |
| stage 2, `__fern_str_concat` self Ir | 614.7 M | 603.2 M |

Byte-identical against a compiler built from main: the `checker.fern` binary
and all 1,965 `selfhost-emit-hashes` rows.

The PR first carried a rewrite of `x86_gas_prepare` over views as well;
`2026-10-02-f-the-assembler-stops-scanning-by-byte.md` landed the same change
first, and this one keeps main's.
