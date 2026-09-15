# Self-host ownership metadata allocation

## Change

Identifier-frequency queries now fold an integer through the existing AST walk
instead of constructing a list of every identifier. They reuse the collector's
assignment-target and lambda-scope rules. The sole-occurrence analysis checks
parameter counts first and skips its repeating-scope inventory when none occurs
once. Field-append and dying-argument queries use the same count APIs.

Function lowering also calculates its top-level movement result once and derives
a separate view containing loop moves. Analysis consumers use the complete view;
the emitter retains the top-level view. Neither change alters ownership rules.

## Fixed-input measurement

Native Linux ARM64 in Docker, four CPUs, 12 GiB memory limit, compiler built by
the same Go bootstrap binary, and the same `coreutils/sha256sum.fern` inputs from
5c9c131ed. GNU time measured separate compiler processes emitting ARM64 assembly.
A small fixture first verified the measurement pipeline and exact output match.
No other local builds or tests ran during the full comparisons.

| Run order | Peak RSS, bytes | Wall seconds |
|---|---:|---:|
| Before | 5,631,819,776 | 3.36 |
| After | 4,103,081,984 | 1.36 |
| After | 4,102,926,336 | 1.32 |
| Before | 5,631,877,120 | 1.82 |

All four assembly outputs are byte-identical. Peak memory falls by about 1.53 GB.
Repeated earlier trials showed substantial process-order timing variation, so
these times do not establish a reliable speedup or predict whole-CI duration.
The compiler executables both occupy 16,626,897 bytes and have different hashes.

Movement-result reuse alone reduced peak memory by about 111 MB. Scalar counting
and the conditional repeating-scope scan provide most of the remaining reduction.
The additional expression/field-append count consumers do not provide a measurable
incremental reduction on this particular input. Compiler peak memory remains
about 4.10 GB; this is a partial reduction.

Temporary stage probes attributed about 1.4 GB of peak growth to the may-grow
registry's identifier inventories. Its graph fixpoint added no observed peak
growth. Those instrumented probes preserved assembly exactly and are memory
attribution evidence, not timing measurements.

## Validation

- Thirteen identifier-count cases compare both APIs to the existing list
  collectors and independently pinned counts, including assignments, loops,
  duplicates, tuple binders and nested lambda shadowing.
- All 29,472 self-host coreutils parity outcomes pass.
- Targeted field-append, ownership, retained-bracket and generic-fold cases pass
  across the available x86 QEMU, native ARM64 and Wasm paths. The generic-fold
  ARM64 test requires a native x86 driver and skips on this host.
- Eleven separately executed native ARM64 structural cases produce identical
  movement plans, IR, diagnostics and exit statuses before and after, including
  loop-local construction and a top-level binding shadowed inside a loop.
- Source checks, measured feature census, formatting, selectors, Go build and
  Linux module-wide vet pass.

The full current-head CI suite is still required before merging. Local broad
validation ran concurrently and supplies correctness evidence, not controlled
whole-suite performance measurements.
