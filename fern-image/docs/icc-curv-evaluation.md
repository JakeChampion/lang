# Scalar ICC curv evaluation

`packages/codecs/icc/curv_eval.fern` evaluates one scalar `CurveData` value from the [wire-data API](icc-tag-data.md).
Identity, positive gamma, and sampled `curv` forms are supported.
Parametric forms return `EvalError.Unsupported`.
This API does not interpret a complete profile, select an intent, construct a transform, or change JPEG admission.

## API and admission

`evaluate(curve, input, limits, cancelled)` returns a binary64 value or an explicit `EvalError`.
Input must be finite and within the inclusive interval `[0, 1]`.
Public gamma numerators must be within `[1, 65535]`, with the wire denominator 256.
Sample arrays need at least two entries, and every entry must be within `[0, 65535]`.
The evaluator admits every sample even when the input is an endpoint.
Nonmonotone sampled curves remain valid forward functions.

`EvalLimits.max_samples` must be positive.
Its default is 4,096, and the absolute ceiling is 65,536 samples.
Larger caller allowances do not raise that ceiling.
Sample storage already belongs to the caller, and the evaluator borrows it without an output array.
These counts do not establish physical/process bounds, a CPU bound, or a hard deadline.

`Identity` returns the input bits exactly, including negative zero.
`Gamma(g)` evaluates `input^(g/256)` through `std/f64.pow`.
Positive-gamma endpoints and exponent one preserve the input bits exactly.
`Samples` uses linear interpolation on the uniform grid with `n - 1` intervals, then divides by 65,535.
It does not clamp, resample a profile, invert a curve, or apply a transfer-function label.
Nonfinite or out-of-range results return `EvalError.Numeric`.

## Sample position and cancellation

A binary64 product can round a sample position onto the wrong interval boundary.
The evaluator retains the product's low part with an input split before interval selection and interpolation.
A 65,536-entry alternating table at binary64 one-third exposes the lost part in a naive product.
Its independent Fraction result is approximately `0.9999999999987874`.
The naive result differs by approximately `1.21264e-12` and fails the predeclared finite criterion.
The dedicated regression requires the corrected result within `1e-15`.

Identity and gamma success each use two cancellation polls.
A sampled curve with `n` entries uses `2 + ceil(n/64)` polls.
Polls precede work, each validation group, and success.
Cancellation remains cooperative, with no preemption or deadline guarantee.
Callbacks remain outside Cells, and a callback can update another alias without changing the borrowed samples.

## Finite qualification

Issue #13665 declares an absolute error criterion of `1e-12` before implementation.
This criterion applies to the finite corpus, not every valid input or a complete transform.
Failures require a cause fix rather than a larger tolerance.
Identity and gamma endpoint observations additionally require exact IEEE bits.

The independent generator uses Fraction arithmetic on the original IEEE input for sampled curves.
Its gamma reference uses Decimal power with 90-digit precision.
The 1,049 cases cover subnormals, endpoints, adjacent grid boundaries, gamma extremes, random curves, nonmonotone samples, large tables, public-record errors, and cancellation.
The frozen reference bundle records the generator identity and its seed.
No runtime Python or external CMM dependency participates in evaluation.

Six TAP groups pass on native, Wasm, ARM64/QEMU10, and the Go interpreter.
The Go checker accepts the same test source.
All compiled unit runs report nonzero balanced heaps and zero live blocks.
Compiler prerequisite #13667 preserves the original test syntax and fixes nested empty-array inference before these runtime observations.

`scripts/test-icc-curv-eval.sh` checks eight inputs per fresh worker.
The collector requires exact errors, poll counts, row identities, finite results, and the declared numerical criterion.
Every worker must exit zero without a sanitizer finding and report a nonzero balanced heap census with zero live blocks.
Failures preserve complete prefixes and failed process output, then return nonzero.
Eight fault-control groups cover numerical/exact-bit changes, errors/polls, missing or duplicate rows, process failures, sanitizer findings, heap failures, and reference corruption.

Final records require committed source and immutable selected source/tool hashes across three targets.
The initial native smoke record uses uncommitted source and does not qualify the final campaign.
Parametric curves, profile models, rendering intents, full transforms, encoded integration, color accuracy, and production acceptance remain open under #13659.

## Recorded finite observations

Final records use committed source `7508059b2dfc41da27717c6a0d6ef415579bba96`.
Each target checks 1,049 cases: 977 successful evaluations and 72 expected errors.

The largest observed absolute error is `1.327041356519031e-16`, within the predeclared `1e-12` finite criterion.
All required identity and endpoint bits, errors, and poll counts match exactly.
The three target sample records are equal.
The bundle reproduces byte-for-byte with SHA-256 `b42ce9c4de12f4a17b13c7b48d406a474aec71cd55d4fef3bd9b94388e98ae14`.

The campaigns use 132 fresh workers per target, for 396 total workers.
Every worker reports nonzero balanced allocations and frees with zero live blocks.
The native record preserves 1,246 selected source/tool hashes, while each runner record preserves 1,247.
These include all compiler and 163 stdlib sources, worker binaries, the reference bundle, and generator/collector sources.
All selected hashes remain unchanged throughout the campaigns.

The clean compiler source is `b38cca91520de044f7ebf81f7ea6523d1af38a12`.
Its SHA-256 is `d12b97d792d5d66d6bd312cd6723100ecb85ec2609d9430705032e504e5c090b`.
Compiler and stdlib sources match the campaign checkout.
Full native/Wasm scripts and 35 standalone ARM64 executables pass 248 TAP cases per target.
Their 74/50/35 heap censuses are balanced with zero live blocks.
Standalone ARM execution does not establish completion of the whole Go ARM harness.

Both unchanged compiler/stdlib complexity checks pass without skips.
All 2,048 eligible Fern format checks, build, vet, lint, test names, CI self-tests, runtime bundle, protocol controls, and STE checks pass.
The committed-source Go checker and interpreter runs also pass all six unit groups after format changes.

Their recorder retained a hardcoded uncommitted flag, which the separate provenance correction explains.
That repeat accidentally reused an earlier local Go log path.
The evidence preserves the earlier metadata recovered from its tool response and identifies the unavailable original stdout log.

The raw records are [native](../benchmarks/results/icc-curv-eval-native.json),
[Wasm](../benchmarks/results/icc-curv-eval-wasm.json), and
[ARM64](../benchmarks/results/icc-curv-eval-arm64.json).
[The evidence directory](../benchmarks/icc-curv-eval-2026-10-27/) preserves regressions, auxiliary checks, and the explicit recorder correction.
Published historical datasets remain unchanged.

## References

The `curv` layout and semantics derive from [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), section 10.6.
The [ICC decision](icc-feasibility.md) records the official source access and the separate future acceptance gates.
This scalar API does not imply profile conformance or broad compatibility.
