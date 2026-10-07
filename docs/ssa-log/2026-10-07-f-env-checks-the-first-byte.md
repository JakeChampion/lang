# 2026-10-07 — env() checks an entry's first byte first

`asmcore.rt_src_env`, the native `__fern_env` runtime both native emitters
compile into a program that calls `env`. Refs #8171.

## What changed

`env(name)` walks the process environment for a `name=` entry. It compared
each entry against the name byte by byte, indexing the name with a bounds
check each time, before moving to the next. Most entries differ in their first
byte. The walk now compares that byte, `=` for an empty name, before it starts
the full compare.

The compiler reads its debug switches (`FERN_IR_VERIFY`, `FERN_SSA_REPORT`,
`FERN_SELFHOST_NO_REUSE`, `FERN_RC_TRACE`) through `env`, about 14 k times on
a `checker.fern` compile. Each read walks the whole environment and misses.

`TestSelfHostEnvX86_64` gains two cases. In one, entries that share the name's
first byte, or all of it but the `=`, come before the name. In the other, only
those entries are present.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built by its own stage 2,
in an environment of 160 variables. The baseline is main at 11a717394. The
stage 3 rebuilds itself byte for byte, and the two compile `checker.fern` for
x86-64, arm64 and wasm to the same bytes. `fern.fern` differs only by this
runtime, since it calls `env`.

| | before | this change |
|---|--:|--:|
| total Ir | 14.824 G | 14.812 G (−0.08%) |
| stage 3 size | 10,780,136 | 10,780,328 |

A read now costs about 3.2 k instructions rather than 4 k, so the share left
is the number of reads, not their cost.
