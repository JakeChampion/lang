# Primitive protocols and default display

This record covers #12704 and the five defects found during its implementation: #13081 through #13085.
The base commit is `671c1109abd94f82e590ddbcd5e07f726c127006`.
The feature commit is `578d51988b8d548490603a323ee0699cfaf5d8f7`.
The compiler source commit is `f9a715b02f483e0b9177655a91b0b26cc3ce3a80`.
The later feature commit only adds the final measured census row.

## Behavior

All eleven primitives support default display without an import.
Explicit user methods retain precedence.
Runtime dependencies add no source import edges, receiver declarations, or unrelated module names.
The compiler and the public library share the existing formatting kernels.
Public floating-point precision and cached-power utilities retain their APIs.

The comparison traits cover every primitive.
Boolean order places `false` before `true`.
Float hashes follow the existing total equality of the comparison traits.
Both zero signs share a hash, and all NaN signs and payloads share a hash.
The numeric traits include `u8` and `usize`, with the existing wrapping arithmetic rules.

The interpreter retains byte identity through casts, bindings, arrays, views, generic calls, captures, and cells.
The Go checker recognizes `char` and `usize` associated-function namespaces.
Both implementations resolve contextual `char` and `str` type arguments while preserving value shadowing.
Builtin method argument errors report E004.
Formatting import hints require an actual receiver and method pairing.

## Validation

| Gate | Result |
| --- | --- |
| Three-stage bootstrap | Stages 2 and 3 are byte-identical |
| Driver sizes | All 11 rows pass the unchanged strict baseline; maximum increase is 2.7% |
| Go parser, checker, loader, and interpreter suites | Pass; three existing synthetic AST cases are intentionally skipped |
| Self-host checker, display, cell, and Unicode regressions | All 11 selected parent tests pass |
| Tree-shaking regressions | All six selected parent tests pass |
| Library standalone imports and formatting regressions | Pass |
| Trigonometry regressions | Pass, including the two callers of the extracted multiplication helper |
| Official fixture runners | Eight runnable cases on six runners, plus six negative checker cases: 54 selected cases |
| Public API matrix | 144 commands pass |
| Negative diagnostic matrix | Six checks pass with exactly the required diagnostic codes |
| Allocation traces | All eight runnable fixtures have zero unmatched allocations across 188 trace events |
| Embedded release CLI | 26 commands pass from `/tmp`, using copied projects and embedded library sources |
| Source format check | All 1,183 standard source files pass |
| Source lint, feature census, and Go vet | Pass |

The native, ARM64, and WebAssembly matrix uses `FERN_STRICT_IR`, `FERN_SANITIZE`, and `FERN_LEAKCHECK`.
Every executable must produce the expected output and report balanced allocations with zero live bytes.
The negative cases preserve method and module scope and reject wrong formatter arity on all eleven primitives.

The Go frontend suites used the source committed as `91d705e9c725bc4a18d1f31f90e067e517d86f32`.
The Go frontend and library sources did not change after that commit.
The three skipped interpreter cases are synthetic AST nodes: `EnumLit`, `CaptureRef`, and `MakeClosure`.
The other recorded suites contain no skipped tests.
The final Go oracle records the compiler source commit and a clean working tree.
The embedded CLI records that same commit in its version output.

## Kernel cost

The benchmark performs 10,000 iterations of shortest and fixed-precision float formatting and two public Unicode scalar encoders.
The baseline uses the previous implementation's compiler and matching library tree.
The candidate uses the final stage 3 compiler and its library tree.
Both produce checksum `321202`, report `142986` allocations, and emit 29,824-byte executables.

| Measurement | Baseline | Candidate |
| --- | --- | --- |
| Callgrind instructions | 31,197,760 | 30,927,965 |
| Candidate / baseline | 1.000000 | 0.991352 |

This is a 0.86% instruction reduction for this workload.
It is not a claim about all formatting workloads.
The record includes the benchmark source, executable identities, and profiler logs.

The formatted source copy has one additional blank line after its imports.
The compiler produces the same executable bytes from this copy.
`kernel-cost-measured.fern.gz` retains the exact measured source bytes.
The manifest records both source identities and this format correction.

## Artifacts

`manifest.json` records source identities, binary hashes, test summaries, and artifact hashes.
`source-inventory.json` records every tracked compiler and library file.
The source hash includes each sorted path and its file contents, with NUL delimiters after both.
The compressed source patch restores the feature from its base commit.
Its reverse application was checked against the validated tree.
The baseline records include both compiler disagreements and the repaired diagnostics.

The compiler and library source SHA-256 is:

```
bba57fdbc445673f2e9dd4e8c9857d5a8bb64b7359536491566507fc32785513
```

The stage 2 and stage 3 SHA-256 is:

```
5298d78bac0c6e0ba34b716d7c9388cc5f41224777a94d584597cf54f1f0e7f8
```
