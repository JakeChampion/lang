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

The [larger comparison](paired-controls/summary.json) uses the second group, with 32 records and 127,126 decoded bytes.
Three pairs alternate compiler order, and all six commands exit zero with identical TAP output.
The CPU reductions are 61.76%, 61.93%, and 60.72%.
Median wall time decreases from 80.264019253 seconds to 31.044851410 seconds.
The shortcut range is 77.844565593–81.066830437 seconds, and the index range is 29.624515362–31.558555311 seconds.
Median peak RSS increases from 150,856 KiB to 150,984 KiB.

Separate counter builds remove 92,717 allocation events on the first group and 5,937,677 events on the second group.
The reductions are 0.3233% and 8.2267%, respectively, relative to the shortcut version.
Each run frees every allocation and reports zero live bytes.
The [allocation comparison](allocation-controls/allocation-summary.json) retains exact totals and the complete records for both binaries.
The fresh shortcut control reproduces its earlier first-group total of 28,676,955 events.
The counter executables are separate from the normal timing executables.

One [instruction sample](instruction-control/instruction-summary.json) records 23,717,071,286 guest instructions for the shortcut and 18,366,256,684 for the index.
This removes 5,350,814,602 instructions, or 22.56%, on the first diagnostic group.
Both use the same Valgrind 3.24.0 binary and policy, with child tracing, cache simulation, and branch simulation disabled.
The normal executables produce identical TAP output and exit zero.
The raw Callgrind data uses lossless gzip storage, with original byte hashes in [the raw-data identities](instruction-control/raw-data-identities.json).
This work-count comparison makes no wall-time or external-rank claim.

## Correctness controls

All eight targeted Go suites pass with exit zero in 270.880 seconds.
They report 385 passing test and subtest results with zero skips.
The [test log](tests.log) includes x86-64, ARM64 under QEMU, and WebAssembly controls.
The new REPL regression covers a same-count replacement that changes declaration order.
It verifies the new function-value binding and the earlier session snapshot on all three targets.

The other suites cover callable values, both native driver suites, generic calls, associated constants, dynamic providers, and map key equality.
The five positive CLI controls also pass.
Five negative CLI controls produce identical failures and diagnostics through both normal binaries.
Source check, Fern format, Go format, and diff checks pass.
Go vet and the CI self-tests also pass on the index source.

The qualified full rebuild produces the same executable as the initial prototype after a whitespace-only correction.
The [agreement record](qualified-binary-agreement.json) retains both source identities and the executable hash.
The archive also retains the first source-check failure and formatter diff.

The indexed normal interpreter completes the unchanged full 1,896-record corpus with exit zero in 527.216713180 seconds.
It reports 525.259982 seconds of user CPU time, 0.382929 seconds of system CPU time, and 222,664 KiB peak RSS.
The [full control](full-corpus-control/full.json) retains the original source hash, command, output hashes, and child resource measurements.
The TAP output reports one passing reference test and zero failures.
The original and shortcut controls both reached their diagnostic deadlines without completion.
Different concurrent workloads prevent a paired full-corpus timing claim.

The test corpus and CI package budget stay unchanged.
The direct full-corpus control retains its 1,980-second diagnostic limit.

The [Go oracle/native subtest](go-oracle-control/full-go-control-result.json) also completes with exit zero under the unchanged 33-minute package timeout.
`TestSelfHostStdTestInterp/opus_delimited` passes in 439.89 seconds.
The complete filtered Go command reports 443.161 seconds, with zero skips.
Other subtests remain outside this filtered control.
Exact-head CI remains incomplete at this archive revision.

## Complete compilation controls

The final [compilation controls](compilation-controls/summary.json) compare original source `ebb50c78` with index source `db4308a1`.
They compile the same fixed checker and imports to the same native executable in all 16 commands.
The output SHA-256 is `ca913adb4c4164b7b49e1b2c73e62739c178d9bc05978b3ccdc30138fdbca068`.
Three pairs alternate compiler order in each cache mode.
Each binary primes its own cache, and both probes record 2,410 hits and zero misses.
The measured commands use normal verifier defaults without the reporting flag.

| Measurement | Original baseline | Index candidate |
| --- | ---: | ---: |
| Cold median wall time | 15.039303504 s | 14.176383716 s |
| Cold wall time range | 14.906041274–15.538241930 s | 13.787340586–14.742470354 s |
| Cold median CPU time | 15.037078 s | 14.174824 s |
| Cold median peak RSS | 585,604 KiB | 585,480 KiB |
| Warm median wall time | 11.514601125 s | 11.401592027 s |
| Warm wall time range | 10.826896589–12.600782931 s | 11.257115651–11.589937346 s |
| Warm median CPU time | 11.513360 s | 11.384784 s |
| Warm median peak RSS | 582,788 KiB | 582,792 KiB |

These controls run concurrently with Go tests and instruction collectors.
They establish output correctness on this input but do not establish a complete-compilation performance gain.
The archive retains every sample and all raw logs.

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
