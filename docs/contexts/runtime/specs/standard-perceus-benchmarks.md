# Standard Perceus benchmarks

**Status:** In progress
**Contexts:** Runtime ownership, compiler validation, performance measurement
**Date:** 2026-10-07

## Problem

Fern's reuse implementation lacks a comparison against the standard Perceus
programs on the same machine as Koka and Lean4. Application-specific results
do not establish how it handles the same allocation and sharing patterns.

## Solution

Port the five standard algorithms using ordinary algebraic data types and
pattern matching. Check results through interpreters and compiled targets,
gate small cases in CI, and publish reproducible full-size comparisons.

## User stories

1. Compiler contributors can detect instruction-count and code-size regressions.
2. Runtime contributors can distinguish unique reuse from checkpoint sharing.
3. Readers can reproduce correct answers, timings, memory and allocation counts
   with exact source and toolchain versions, including results where Fern loses.

## Implementation decisions

The kernels preserve Koka's algorithms and sharing lifetimes. Small CI drivers
return work-dependent checksums; native comparison drivers expose full results.
The comparison harness owns toolchain provenance, process measurements and
result validation. Instrument allocation separately to avoid charging its
formatting or export work to only one timed implementation.

Adapt the Lean programs to the same algorithms where upstream versions differ.
Document each adaptation and preserve source licenses. Keep numeric operations
equivalent by proving exercised ranges or using matching arbitrary precision.
Retain ordinary immutable value semantics without hand-tuned storage or reuse
annotations. Compiler defects discovered by faithful ports get focused fixes.

## Testing strategy

Use independent small-case references and full-value interpreter checks, then
strict IR, sanitizer and allocation-census checks on all required targets.
Test tree invariants and checkpoint preservation, solution-list content,
derivative simplification and constant-fold evaluation. Measure native small
pilots before changing scale. Retain raw per-run data, source and binary hashes,
versions and commands; QEMU is correctness evidence, not native timing evidence.

## Out of scope

Networking, alternate faster algorithms, weakening performance gates, changing
existing baselines to hide failures, or replacing negative results with guesses.

## Open questions

Numeric ranges, safe full-scale resource use and the causes of losing rows need
measurement. They do not require a product decision from the user.

The arbitrary-precision derivative range audit reaches ten iterations with
40,230,090 expanded leaves and exercised integer values between -1 and 1.
The default workload therefore fits i64. Its port retains ordinary recursion;
memoization belongs only to the audit. Constant folding's generated depth 20
tree uses addition and constants at most 21, also within i64. Neither kernel
claims unbounded arithmetic for arbitrary external expressions. Full-scale
resource use and comparative performance still need measurement.
