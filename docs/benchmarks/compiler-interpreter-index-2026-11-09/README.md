# Interpreter function-name index

Status: [experiment] initial completed controls from 2026-11-09.

The [unused-search shortcut](../compiler-interpreter-targets-2026-11-09/README.md) improves two subsets but does not complete the full corpus within its diagnostic deadline.
Its candidate CPU profile still attributes 49.43% of exclusive samples to `call_target`.
The index addresses this residual declaration search.
It does not change the packet codec, corpus, test exclusions, CI budget, ownership certification, or proof rules.

## Change and contracts

`FuncTable` stores the unchanged declarations, a function-name index, and a map from index entries to declaration positions.
The table includes both raw names and runtime method aliases where they differ.
Index entries preserve declaration order, including duplicate names and aliases.
Lookup retains the existing name and receiver predicates.

Module evaluation builds one table after all module transformations finish.
Function and closure environments share that immutable table.
Every REPL declaration update builds a new table, even when the declaration count stays unchanged.
Earlier sessions retain their earlier table and function bodies.

Free and method argument-target searches use the index.
Constant and function-value resolution also use the index.
Other helpers receive the table and retain their declaration scans.
The parser dispatch helper still receives the raw declaration array.
The [patch](implementation.patch) retains the complete change.

## Completed comparison

The baseline is the shortcut version, `424fe24f4cf2bd1c0c49060215f1fe622ef4d5b2`.
The index candidate is `db4308a127fa82a0a6cf45f1c86851a709f010d4`.
Both use producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The fixed input and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
The builds omit debug variables and embedded source files.
Normal measurements disable profiling and allocation counters and retain ownership certification.

Five pairs alternate compiler order on the first 32 reference records.
All ten commands exit zero with identical TAP output.
The [raw samples](first-samples.json) retain every command, binary hash, input hash, and child resource measurement.
Concurrent Go tests and Callgrind runs can affect wall and CPU time.
This comparison does not pool results with the earlier shortcut comparison.

| Measurement | Shortcut baseline | Index candidate |
| --- | ---: | ---: |
| Median wall time | 8.772472410 s | 7.031007564 s |
| Wall time range | 5.972868690–9.068611213 s | 4.428853477–7.214205093 s |
| Median CPU time | 8.598952 s | 6.959663 s |
| Median peak RSS | 148,168 KiB | 148,296 KiB |

CPU time decreases in every pair, by 25.60%, 20.12%, 17.05%, 20.21%, and 16.83%.
The candidate uses 128 KiB more median peak RSS on this subset.
The [summary](first-summary.json) retains the exact values.
These results establish an additional benefit on this subset only.
They do not establish full-corpus completion, a complete-compilation gain, or an external rank.

## Correctness controls

All eight targeted Go suites pass with exit zero in 270.880 seconds.
They report 385 passing test and subtest results with zero skips.
The [test log](tests.log) includes x86-64, ARM64 under QEMU, and WebAssembly controls.
The new REPL regression covers a same-count replacement that changes declaration order.
It verifies the new function-value binding and the earlier session snapshot on all three targets.

The other suites cover callable values, both native driver suites, generic calls, associated constants, dynamic providers, and map key equality.
The five positive CLI controls also pass.
Source check, Fern format, Go format, and diff checks pass.
The qualified full rebuild produces the same executable as the initial prototype after a whitespace-only correction.
The [agreement record](qualified-binary-agreement.json) retains both source identities and the executable hash.
The archive also retains the first source-check failure and formatter diff.

The full 1,896-record control, larger repeated comparison, and allocation controls remain active at this archive revision.
Their later records must state each completed outcome explicitly.
The test corpus and CI package budget stay unchanged.
The direct full-corpus control retains its 1,980-second diagnostic limit.

## Reproduction

The [identity record](identities.json) retains the measured source revisions and the integration source with identical compiler and test code.
The [qualified build](qualified-build.json) retains the producer hash, complete command, environment, and normal executable hash.
The [collector](measure.py) runs each binary against the same input tree and standard library.
Its paths refer to the original isolated worktrees.
Use equivalent isolated worktrees for a new reproduction.
Preserve the exact producer and flags in the build record.

The [manifest](manifest.json) hashes each data file.
The README and manifest itself are outside that hash list.
Executable files remain outside this archive.
