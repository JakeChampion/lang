# Optimizer slot bound

The LICM change removes 7,953 full scans from the fixed checker compile.
It reduces instructions by 1.781725%, with byte-identical output on all three targets.
The allocation count is unchanged.

The subject is a full `checker.fern` compile to x86-64 assembly text.
The command uses `-O -target x86-64-linux -emit asm`.
Both stage-2 compilers use one stage-1 compiler as their producer.
The stage-1 compiler comes from the published stage-0 pin and baseline sources.

The baseline commit is `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
The candidate starts at `231becae0394b07f7bdbc5cf24922b6e9db67aed`.
Those commits have identical compiler and standard-library sources.
The candidate changes only `compiler/ir.fern` in those source trees.
The frozen baseline tree supplies the subject and its standard library for both compilers.
The source manifest records all 1,215 files.

The baseline profile records 25,719,653,483 instructions and 20,526,860 calls to `__fern_arr_box`.
The `cp_max_slot` scan has a self cost of 515,372,921 instructions, or 2.00% of the total.
The first prototype shares the bound only inside `propagate_fold`.
It records 25,699,210,738 instructions, a decrease of 0.079483%, with the same box count.
The raw files retain this prototype as candidate A.

Candidate B shares one bound through `optimize_ops` before LICM.
It records 25,671,755,888 instructions, a decrease of 0.186230%.
The bound crosses more passes but requires four private helper functions.
Its raw files and source patch remain available as a superseded prototype.

Candidate C changes five lines in the LICM implementation.
`hoist_loop_invariants` already computes a bound and extends it after each new local slot.
The change passes that current bound into `licm_hoist_pure` and `licm_slot_use`.
Each loop therefore avoids another full scan to find the maximum local slot.
The store and load analyses still inspect the current operations for each loop.
The public pass functions retain their interfaces.

| Metric | Baseline | Candidate C |
|---|---:|---:|
| Instructions | 25,719,653,483 | 25,261,399,930 |
| Maximum-slot scans | 17,152 | 9,199 |
| Instructions in the maximum-slot scan | 515,372,921 | 56,940,213 |
| Calls to `__fern_arr_box` | 20,526,860 | 20,526,860 |
| Best native CPU time, seconds | 5.640790 | 5.663143 |
| Median native CPU time, seconds | 5.787134 | 5.737805 |

The native sample gives mixed results and does not establish a latency change.
The instruction count supplies the measured benefit for this subject.

The private differential driver covers 350 cases with five slot bounds and ten constant values.
The cases include empty input, no locals, dead tees, constant chains, branches and loops.
Each run serializes all nine fields of each operation and the retained input alias.
Baseline and all three candidates produce the same 1,400 lines on x86-64, ARM64 and Wasm.
Each run exits with status zero and no diagnostic output.
The output SHA-256 is `cd5c9feafa6f7026b5e97a48c6d6b791df0b27a9f3e43e523a5dccc0472c9397`.

Callgrind 3.24.0 supplies instruction counts.
Each compiler first completes a native compile into a private certifier memo directory.
The measured compile retains the certifier and uses that warm directory.
The native sample alternates baseline and candidate order for five pairs.
Callgrind elapsed time does not measure native compiler latency.

The baseline release and debug binaries have identical loaded code and data, apart from ELF section-table header fields.
The segment hashes and addresses establish this fact for this exact pair.
The debug binary supplies symbols for address resolution after that check.
The release profile supplies the performance totals.
This result does not establish equivalence for other release and debug builds.

The candidate C release and debug binaries also pass the segment comparison.
The proof checks identical addresses, segment sizes, permissions and bytes.
Only ELF section-table fields differ in the header.

The original profile record retains its initial generic debug caveat.
The corrected record references the measured segment equivalence.

The earlier profile from October 6 used different sources.
Its 17.09 billion instructions are not an A/B baseline for this change.
This slice does not complete the architecture roadmap in issue #4412.
It does not establish a general compiler speed increase or a decrease in allocation count.

## Repeat the primary comparison

The script needs Valgrind 3.24.0 and the repository toolchain.
Run these commands from the candidate checkout:

```sh
stage0_binary=$(bootstrap/bootstrap.sh stage0)
bash docs/benchmarks/optimizer-slot-bound-2026-10-27/reproduce.sh "$stage0_binary" /tmp/optimizer-slot-bound
```

The script creates a separate baseline worktree and builds both stage-2 compilers with one stage-1 compiler.
It warms a private certifier memo for each measured compiler.
It compares complete output bytes and retains both raw profiles.
The raw Python scripts retain the original absolute workspace paths for provenance.

`native-summary.json` retains every native sample and its order.
`output-identity.json` records the output size and SHA-256 for each target.
The differential driver and its JSON records retain all three execution targets.
The source manifest and binary identities fix the measured inputs and producers.

Gzip archives preserve the raw Callgrind files and symbol maps without a text diff.
`raw-archives.json` records the original and compressed hashes.
Decompress an archive with `gzip -dk FILE.gz` before use with Callgrind tools.
Raw tool logs and patches retain their original whitespace.
A directory attribute exempts these logs and patches from the Git whitespace check.
