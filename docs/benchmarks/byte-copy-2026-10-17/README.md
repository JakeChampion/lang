# Byte-copy loop measurements

These measurements cover the generic compiler transformation, without changes
to HTTP or networking code. They do not measure server throughput or establish
the TLS-profile acceptance condition in #12549.

The control compiler is the owned-call fixpoint, SHA-256
`e22746a45222120478e5cbb563bb761557d642840777abd8068169a109147ab1`.
The candidate is the byte-copy fixpoint at `f71565e6a`, SHA-256
`23ff3a54d1b4c70add94c360ef8f9faaae551261513f0daea88801768b67cdc6`.
Stage 2 and stage 3 are identical at 12,555,360 bytes.

`native-pairs.json` records seven alternating pairs on the same x86-64 Linux
host. Each microbenchmark sample runs three processes, each copying 49,152,000
bytes and checking the result. Median candidate/control elapsed-time ratios:

| Workload | Ratio |
| --- | ---: |
| Byte range writes | 0.02166 |
| Byte appends | 0.01065 |
| Compiler emitting x86-64 | 0.99004 |
| Compiler emitting ARM64 | 0.95035 |

Both compiler timing series execute on x86-64; the ARM64 row measures code
generation, not ARM64 execution. They compile the same frozen checker and
stdlib, using isolated disk certification caches warmed separately for each
compiler. Shared-host elapsed times carry noise; these are local comparisons.

The same report separately records three cold whole-compiler certification
pairs, with an empty memo directory for every process. The median enabled /
disabled ratio is 1.49809, and all six output hashes agree. This exceeds
#12181's 15% overhead target and is not evidence that that issue is resolved.

`benchmark-execution.json` records both new benchmarks on x86-64 Linux,
ARM64 Linux and WASI, in ordinary and sanitized modes. All 12 executions
produce checksum 2,088,960 with balanced allocations and frees: two allocations
for range writes and 3,001 for append. The scalar append control allocates
30,001 times for the same workload.

`debug-code-equivalence.json` records 114 exact native code comparisons with
and without debug information across x86-64 Linux, ARM64 Linux and ARM64 Darwin.
`whole-benchmark-equivalence.json` compares 114 executables with the control and
records 111 paired executions across Linux and WASI. Results agree throughout;
only the two new copy benchmarks and two UTF-8 ingestion benchmarks change
executable bytes on each target. `driver-sizes.txt` records all 11 compiler
drivers within their existing 5% limits.

The UTF-8 ingestion programs reach a copy loop through string padding. Their
assembly-line footprint increases by 128 on x86-64 and 159 on ARM64 for the
shared copy kernel. Those four costs and the eight new benchmark measurements
are explicitly recorded in the baseline, with no tolerance changes. The
standard performance workflow on `f71565e6a` passed all lanes:
https://github.com/JakeChampion/lang/actions/runs/39026648568.

## Main integration verification

After integrating main `38735b2b2` and the consuming-call alias fix, bootstrap
stages 2 and 3 agree: 12,576,400 bytes, SHA-256
`c7cc133ed8890d8d4d822e49e13f448de0116c1c09872e148362e59783404398`.
All twelve ordinary/sanitized executions across x86-64, ARM64 and WASI pass
again with the same allocation/free counts; see
[the current execution record](main-387-execution.json). Ten executables,
including all four x86-64 microbenchmark variants, are byte-identical to the
recorded candidate. Two ARM64 executables changed. The timings above retain
the original compiler hashes and are not new measurements of the refreshed
compiler executable. All 11 strict driver size budgets also pass after this
integration.
