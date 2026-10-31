# Keep transitive resource owners through semantic splices

Refs #4412 and PR #13897. The PR remains draft.

A parent record can own a child with a custom finalizer. The compiler can release the parent after a word read. The word can then become invalid before its consumer call. The common splice guard now follows finalizers through record fields, variants, closure captures, and concrete dynamic values.

Numeric loads from managed byte arrays and strings are treated as data copies. Unknown raw addresses and opaque getters that can return handles keep the conservative rule. Shadowed intrinsics also keep that rule. The analysis uses the return summary of a known closure when it has no captures.

New regressions cover pointer-width and wide integer fields. They also cover captured child finalizers and unused arguments with finalizers. Every published runtime program remains unchanged. The new IR controls cover opaque callbacks, forged references, managed loads, shadowed intrinsics, and callable aliases.

## Local validation

- Primary tests: 179 pass results on stage 2 and stable stage 3, with zero skips.
- Current-library primary tests: 179 pass results on the stable compiler, with zero skips.
- Extra inliner tests: 61 pass results on the stable compiler, with zero skips.
- The `TestSelfHostFramingAllocs` test passes on x86-64, ARM64, and Wasm at the zero-allocation pin.
- Thirty array and ordered-map reuse programs pass across both compiler generations and all three targets. The census assertions remain in place.
- Constructor and previous main-failure controls produce 24 pass results, with zero skips.
- Stages 3 and 4 are byte-identical. ARM64 execution uses QEMU. macOS requires CI.

## Fixed compiler comparisons

One stage-1 built both release stage-2 compilers. They compile the same fixed checker subject and library. The runs disable memoization. The compiler tests above validate this IR change.

| Measure | Before | After |
|---|---:|---:|
| Callgrind instructions | 39,967,273,663 | 38,592,782,682 |
| Allocations on the separate current-checker subject | 35,580,747 | 35,449,536 |

The instruction count falls by 3.44%. The allocation count falls by 0.37%. Both allocation runs balance all allocations and frees, with zero live bytes. The allocation probe uses compiler images built with leakcheck instrumentation.

Stage-1 SHA-256: `1a9e3d8e2d6fd9f2572a7d6f561a21ddd0085e5baf479b7f541e429c66021a87`.

Cost-subject SHA-256: `5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68`.

Allocation-subject SHA-256: `13c20e40215d37626ed08b26a145b29febeb7cfbf52874a19494e49ad2b4dd1e`.

All six fixed HTTP and UTF-8 assembly outputs match the baseline. The three x86-64 ELF files and runtime instruction counts also match. All eight HTTP profile assembly and ELF outputs match. Raw input manifests, profiles, modules, logs, and rejected prototypes remain in the qualification archives.

## Remaining hold

The serve probe still reports three allocations per hello request. Its pin is zero, and the test fails. No allocation limit was raised. CI must run on the updated PR head before review readiness. Native latency and full tracker acceptance remain untested. Issue #4412 stays open.
