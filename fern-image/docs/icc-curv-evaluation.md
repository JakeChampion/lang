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

## References

The `curv` layout and semantics derive from [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), section 10.6.
The [ICC decision](icc-feasibility.md) records the official source access and the separate future acceptance gates.
This scalar API does not imply profile conformance or broad compatibility.
