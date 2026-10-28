# Zero-slot guard scan evidence

The pass now tracks loop depth in its guard scan.
It removes `zg_in_loop` and its temporary boolean array.
The maximum-slot and first-write analyses are unchanged.
This is a pure compiler refactor, with complete checker output identity on three targets.

The comparison uses two stage-2 compilers from one stage-1 producer.
The common producer has SHA-256 `64f876ff80e4db91d1eb614605a7fe567bbad13ecb061dce204defb5c0b975a0`.
That producer already reproduces itself from the baseline source.
The baseline stage-2 artifact therefore has the same bytes as the producer.
The retained profile of those bytes is valid for this comparison.
The [baseline record](baseline-provenance.json) links the build, raw profile, symbols and load proof.

The baseline compiler sources match commit `b0958981729ecb84c9fc91b1f49e2ed7ff1171ec`.
The [source manifest](source-hashes.json) compares that tree with the frozen baseline and candidate sources.
The [patch](source-change.patch) contains the measured compiler change.
The permanent test additions do not enter the compiler import graph.
Both compilers use the frozen checker and stdlib from commit `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.

| Metric | Baseline | Candidate |
|---|---:|---:|
| Instructions | 25,031,956,216 | 25,020,122,061 |
| Calls to `__fern_arr_box` | 17,926,218 | 17,924,193 |

The full-run [metrics](metrics.json) record 11,834,155 fewer instructions and 2,025 fewer boxes.
The instruction decrease is 0.047276% on this subject.
The [caller records](selected-callers.json) show 2,025 fewer calls from `ir.optimize_ops` to the byte-array allocator.

The candidate profile exits with status zero and uses a warm private certifier memo.
Callgrind elapsed time is telemetry, not a native latency comparison.
These figures use a different producer from the prior LICM comparison.
Do not add their percentages.

The [output records](output-identities.json) prove complete checker output identity on x86-64, ARM64 and Wasm.
They reuse the preserved baseline outputs from the earlier private edge-check experiment.
That experiment used this same baseline compiler, subject and stdlib.
The edge-check source change remains parked and does not enter this candidate.
The [ELF proof](stage2-elf-load-equivalence.json) compares loaded code and data between the release and symbol binaries.
Only ELF section fields differ in their headers, so the symbol addresses describe the measured release binary.

The [differential records](contract-validation.json) cover 1,350 cases per target before and after the change.
The [raw fixture](zero-guard-contract.fern.txt) compares all nine operation fields, retained input aliases and repeated application.
Its cases cover nested loops, conditionals, adjacent guards, parameters, writes and frame slots.
All six sanitizer runs exit with status zero, the same 4,050 output lines and zero live bytes.
The `.fern.txt` suffix preserves the measured fixture text outside the source formatter.

The archives preserve complete raw profiles, symbol tables and differential output.
The [archive manifest](raw-archives.json) records decompressed hashes and byte counts.
The Python coordinators preserve the original workspace paths for provenance.
The [reproduction script](reproduce.sh) accepts the named producer binary and a new artifact directory.
It reconstructs both source trees, builds both stage-2 compilers and measures the same frozen subject.

The permanent nested-region and adjacent-guard cases pass in `TestSelfHostIRStrengthPeephole`.
The [primary log](primary-gates.log) records the native driver result.
The private differential provides ARM64 and Wasm execution coverage.
The [self-reproduction record](fixpoint.json) confirms byte-identical stage-2 and stage-3 compilers after the primary test.
The source and repository gates have separate logs in this directory.
Issue #4412 remains incomplete, and the PR remains in draft.
