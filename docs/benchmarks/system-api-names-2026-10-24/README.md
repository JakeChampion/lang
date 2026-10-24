# System API names and empty WASI host results

Source: `82b6647259bed976062bc7ab427baed119be1372`.
Base: the codec integration source `e8742d205`.

The path, CLI, log and fuzz modules use qualified names without a repeated
module prefix. Log levels and the default fuzz iteration count are public
constants. Callers and reference pages use the same names. The log IR tests
import the actual standard module.

The CLI validation exposed #13122. Empty canonical ABI host results have a
pointer but do not allocate a heap block. Cleanup now frees a returned buffer
only when its length is positive. Cleanup still releases every return area.
The preview 1 allocator is unchanged.

The old source reproduces the count mismatch: CLI reports 1053 allocations and
1065 frees, with zero live bytes. A missing environment variable reproduces the
same fault with the published stage0: two allocations and three frees. These
failed results are retained as diagnostic evidence.

The final matrix passes all 66 commands. Both checkers and interpreters pass six
programs: `path`, `cli`, `log`, `fuzz_example`, `fuzz_shrink` and `fuzz_corpus`.

All four target builds pass. Native x86, ARM64 under qemu and WASI executions
have identical TAP output. All 18 allocation checks balance with zero live
bytes. Darwin is a compile check only.

The corrected WASI CLI counts are
1053 allocations and 1053 frees.

Separate source tests pass for empty environment lists, empty values,
and empty arguments in default WASI components. The preview 1 scratch tests
also pass. The final cleanup, component, feature census and complexity suites
run 25 test cases with no failures or skips. Earlier API suites run 41 and 38
cases with no failures or skips. The manifest records their source revisions.

Bootstrap stages 2 and 3 are byte identical:
`0a3dbe4b9b9682f28b3945fc8d267a0128b261f6475bcfcab37dbbca73a6e18b`.
All 11 strict driver size checks pass against the unchanged main baselines.
The largest increase is 0.1 percent. Go vet, Go format and the canonical Fern
format checks pass.

`artifacts.json` records the source inventory and evidence hashes. The patch
applies in reverse to the source revision. The matrix archive has all 132
standard output and error logs. The size script recorded each driver hash before removal.
The final bootstrap stage and oracle remain in the workspace.
