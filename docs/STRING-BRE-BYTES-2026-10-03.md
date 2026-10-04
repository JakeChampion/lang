# Byte-safe BRE input and literal prefixes

A valid UTF-8 BRE pattern can quantify the final byte of a scalar. For
example, the required prefix of `é*` is the first byte of `é`. Compiled
prefixes and alternation filters retain byte arrays so that this partial
encoding never becomes text.

The `exec_bytes`, `search_bytes`, `search_range_bytes`, `search_from_bytes`
and `search_back_bytes` methods accept raw arrays. A private tagged input
retains the original string or array while both APIs share the matching
engine. Captures remain byte offsets, and backreferences compare byte
ranges without constructing partial string views. Existing text APIs keep
their signatures and matching rules.

The matching engine retains the existing repetition and capture behavior.

The final stage-2 compiler passes both prefix and raw-input fixtures on
Darwin, core WASM and the interpreter. Native and core WASM allocations
balance. Coverage includes incomplete scalar prefixes, malformed bytes,
NUL, captures, backreferences, bounded searches and retained inputs.

The same final compiler builds text-based `tac` with the old and new BRE
engines. The new engine adds 1,632 bytes of code and 432 bytes of unwind
data, with unchanged static data and a 199,025-byte file in both cases.
This isolates the engine change from `tac`'s raw-input migration.

Linux target checks, GNU and primary consumer corpora, full units and lint
pass on the combined record-processing snapshot.

Native measurements use text-based `tac` with each BRE engine, isolating
this change from raw input handling. All outputs match GNU 9.12 and uutils
0.12.0 in the C locale. A 16-repeat pilot precedes the same workloads at
65,536 repeats. Two warmups precede seven alternating samples with output
directed to the null device. This workstream runs no other compiler or test
job during measurement; other macOS services remain active.

The literal and partial-prefix medians increase, but all before/after
sample ranges overlap. These results do not establish a speedup. Both Fern
versions are slower than GNU and uutils on the branch-prefix workload.

| Workload | Implementation | Median ms | Sample range ms |
| --- | --- | ---: | ---: |
| literal | Previous BRE | 22.215250 | 21.177459-25.342625 |
| literal | Byte-safe BRE | 24.359250 | 23.600084-24.988792 |
| literal | GNU 9.12 | 28.458709 | 28.021750-31.567583 |
| literal | uutils 0.12.0 | 10.556959 | 10.319042-11.220042 |
| partial prefix | Previous BRE | 35.849459 | 35.316667-36.630500 |
| partial prefix | Byte-safe BRE | 36.627458 | 36.460417-37.049583 |
| partial prefix | GNU 9.12 | 29.099125 | 28.610833-29.656917 |
| partial prefix | uutils 0.12.0 | 21.999292 | 21.828541-22.684000 |
| branch prefixes | Previous BRE | 82.143375 | 81.605042-83.884792 |
| branch prefixes | Byte-safe BRE | 82.034542 | 79.264333-83.519000 |
| branch prefixes | GNU 9.12 | 41.300000 | 40.488833-42.265000 |
| branch prefixes | uutils 0.12.0 | 31.462083 | 31.286292-34.600917 |
