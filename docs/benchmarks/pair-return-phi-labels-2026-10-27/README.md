# Tail-parameter labels and pair returns

The pair-return pass treated a tail-parameter label as a function reference.
`ssasem.phis` stores the function name in `str` on a parameter phi, whose kind is 8.
`sempair.refs_of` looked up that name and marked the function as an indirect-call target.
That false reference prevented pair returns for `type_holds_view` and then `any_holds_view`.
Both functions allocate return boxes in the measured checker compile.

The change classifies phi labels as data in `names_data`.
Actual function-reference and closure instructions retain their reference checks.
The pass can use its current pair-return representation without a new escape analysis or return convention.

The new fixture calls a direct tail-recursive function and an indirect tail-recursive function 50 times each.
The direct function forwards another pair result.
The indirect function remains an actual function-pointer target.
Before the fix, both calls allocate a box and the fixture records 100 allocations.
After the fix, only the indirect call allocates and the fixture records 50 allocations.

The regression fails before the fix and passes after it on x86-64, ARM64 and Wasm.
The default, optimized and sanitizer-enabled modes give 18 complete runs across the before and after versions.
All runs end with zero live bytes.
The full pair-return suite passes all 27 cases in 39.697 seconds.
Its source fixture exactly matches the private regression fixture.

The primary correctness gate completed before the compiler self-reproduction check.
The private candidate compiles itself twice and produces byte-identical stage-3 and stage-4 binaries.
Their SHA-256 is `bddb8e23b04acf6db660c5217d7472405c1898b80b20853fbf12c7e5762c19ff`.
The two complete builds take 42.495 and 42.529 seconds.
These build times are validation telemetry, not a performance comparison.

The common stage-1 compiler produced both the earlier LICM candidate C and the pair-return candidate D.
The primary profile uses those stage-2 compilers and the same frozen checker and standard-library inputs.
The fixture allocation comparison also uses compilers from that common producer.
The source manifest and compiler hashes identify the inputs.

| Primary metric | Prior LICM candidate C | Pair-return candidate D |
|---|---:|---:|
| Instructions | 25,261,399,930 | 25,239,904,272 |
| Calls to `__fern_arr_box` | 20,526,860 | 20,516,468 |
| Generated assembly bytes | 26,865,028 | 26,675,438 |

Instructions decrease by 0.085093%, and the compiler makes 10,392 fewer box calls.
These counts describe the frozen comparison, not a general latency gain.
The candidate source starts at `fa5546746c4801c29d2670b175b192715895686d` and applies `source-change.patch`.
The common producer SHA-256 is `238621600593496f4c333c3661bc09464155c13d0796cec03939166b16cad4db`.
The earlier [LICM evidence](../optimizer-slot-bound-2026-10-27/README.md) retains the baseline profile and frozen subject manifest.

The candidate release and debug binaries contain identical loaded code and data.
The proof checks addresses, segment sizes, permissions and all bytes outside the ELF header.
Only section-table fields differ in that header.
The release profile supplies totals, and the verified debug layout supplies symbols for address resolution.
The box count sums edge call counts from all callers of the nonrecursive `__fern_arr_box` function.

This change affects the generated private ABI.
Its correctness gates are the pair-return suite and the compiler self-reproduction check.
The earlier LICM slice has its separate byte-identity proof.
This slice does not complete the architecture roadmap in issue #4412.

## Main integration

The publication branch merges main at `50e121fbab40c3ffe7f0172cf0ac2f51723e04e4`.
That merge preserves another Fern automation's allocator-guard proof corrections.
The combined source passes the pair-return, scalar-record return, CLI refusal and source suites.
The merged source produces the same stage-2 compiler bytes as the measured candidate D.

That compiler emits byte-identical checker output to candidate D.
It also produces byte-identical stage-3 and stage-4 compilers.
`post-merge/qualification.json` records these results and their exact source and binary identities.

The A/B instruction and allocation counts remain measurements of the pre-merge snapshots.
The main integration does not establish new performance counts for the combined compiler.

## Repeat the primary comparison

The script needs Valgrind 3.24.0 and the repository toolchain.
Run these commands from the candidate checkout:

```sh
stage0_binary=$(bootstrap/bootstrap.sh stage0)
bash docs/benchmarks/pair-return-phi-labels-2026-10-27/reproduce.sh "$stage0_binary" /tmp/pair-return-phi-labels
go test ./internal/testing/e2ecompiler -run '^TestSelfHostPairReturn$' -count=1 -v
```

The script reconstructs the frozen candidate with `source-change.patch` in a separate worktree.
It builds both stage-2 compilers with one stage-1 producer and retains their complete profiles.
Later main commits therefore do not change this comparison.
The generated output can differ because the private ABI changes.
The e2e test checks behavior, allocation counts and release of all memory.

Gzip archives preserve the raw Callgrind file and symbol map.
`raw-archives.json` records original and compressed hashes.
Decompress an archive with `gzip -dk FILE.gz` before use with Callgrind tools.
Raw tool logs and source patches retain their original whitespace.
A directory attribute exempts those raw files from the Git whitespace check.
The raw Python scripts retain original absolute paths for provenance.
