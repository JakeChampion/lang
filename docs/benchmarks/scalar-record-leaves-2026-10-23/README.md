# Scalar record leaves

Issue: #12931. Base: `8207edece5bcf7bb94bc5f4192024abc94c367d4`.

The original SIMD evaluator benchmark allocates 21 records per element for `Dual[f64]` and 323 for `DoubleDouble[f64]`.
This compiler change reduces both counts to zero. The `f64` evaluator also allocates zero.
The benchmark source and the standard library stay the same.

The compiler admits constructions with scalar fields to its small leaf rule.
It compares function indices to distinguish methods with the same short name.
After each splice, it splits constructions and removes dead copies before it classifies new leaves.
A small record wrapper can keep a scalar call without the wrapper's box.

Larger scalar record builders use the existing budget of 400 instructions.
They also keep the existing block and caller limits.
A caller must project the result's fields, including through copies and loop phis, or meet the shared splice rule.
Records with references or finalizers do not qualify for this larger rule.
The pass retains scalar calls and their effects, including calls marked `@noinline`.

## Results

Four pairs alternate baseline and candidate execution order on one x86-64 host.
Each execution uses three warmups and twenty timed iterations over 20,000 elements.
The table gives the median p50 across the four executions of each variant.
Timings describe this host. They do not establish a portable speed guarantee.

| Evaluator | Baseline allocations per element | Candidate allocations per element | Baseline p50 | Candidate p50 |
| --- | ---: | ---: | ---: | ---: |
| `f64` | 0 | 0 | 242,562.5 ns | 252,412.5 ns |
| `Dual[f64]` | 21 | 0 | 2,820,684 ns | 412,471 ns |
| `DoubleDouble[f64]` | 323 | 0 | 53,578,852 ns | 26,719,405.5 ns |

`paired-kernel.json` records every execution and the executable hashes.
`measure-kernel.py` records the exact measurement commands and paths.
The compressed inputs preserve the exact source bytes.

## Validation

- Bootstrap stages 2 and 3 match byte for byte: 14,268,096 bytes, SHA-256 `249ac984201e7eba37250232c138e120e5c28b706cc9ef97dbd467c90e150828`.
- All 11 driver sizes pass the strict gate with the unchanged baselines.
- The broad regression group passes in 130.507 seconds. It covers record leaves, scalar and array leaves, constructors, finalizers, the interpreter SIMD suite, and the complexity gate.
- The new regression covers distinct methods with one short name, a larger arithmetic builder, loop phis, immutable arrays, and a scalar call marked `@noinline`.
- The semantic regression runs on x86-64, ARM64, and Wasm. It also runs with the x86-64 sanitizer and checks balanced allocations and frees.
- All 18 SIMD suite checks pass on x86-64, ARM64, and Wasm with sanitizer and leak checks. Both interpreters pass the same suite.
- All 1,176 tracked Fern sources in the formatter gate pass. `go vet ./...` passes.

The compiler and standard library source hash is `2eaf09fcbb10cb6a5d9188e103204f2c4368da4545d151274699d361253bcedb`.
`bootstrap.json` gives the exact hash procedure's output. Its `source_git` identifies the base before the source commit.
`manifest.json` gives the evidence file hashes.
