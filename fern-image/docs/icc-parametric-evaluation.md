# Bounded scalar ICC parametric evaluation

`packages/codecs/icc/parametric_eval.fern` evaluates a public `tag_data.ParametricData` record.
It implements the five functions from ICC.1:2022 Table 68 within the initial numerical subset below.
It does not admit a profile model, select an intent, construct a transform, or change JPEG admission.
The separate `curv_eval` API continues to return Unsupported for parametric data.

## Formulas and initial domain

All coefficient numerators are signed i32 values with denominator 65,536.
The input must be finite and within the inclusive interval `[0,1]`.
The exact coefficient counts for function types 0–4 are 1/3/4/5/7.
Unknown functions return Function, incorrect counts return Shape, and invalid inputs return Input before callbacks.

| Function | Coefficients | Formula before final clipping |
| --- | --- | --- |
| 0 | g | `x^g` |
| 1 | g,a,b | `(a*x+b)^g` above the zero-base threshold, otherwise zero |
| 2 | g,a,b,c | `(a*x+b)^g+c` above that threshold, otherwise c |
| 3 | g,a,b,c,d | `(a*x+b)^g` at `x>=d`, otherwise `c*x` |
| 4 | g,a,b,c,d,e,f | `(a*x+b)^g+e` at `x>=d`, otherwise `c*x+f` |

The current table corrects older formulas, so the two type-4 offsets remain distinct.
After a finite result, the evaluator clips to `[0,1]` as the standard requires.
NaN or infinity returns Numeric rather than a numeric fallback.

Function 0 admits every positive signed fixed-point gamma numerator.
Affine-power functions require positive a and gamma at least one.
For functions 1/2, `a+b` must lie within `[0,1]`.
For functions 3/4, d must lie within `[0,1]`, `a*d+b` must be nonnegative, and `a+b` must not exceed one.
These conditions keep every active power base within `[0,1]`.

Zero/negative gamma or a and a negative active base return Domain.
Otherwise valid coefficients outside this subset return Unsupported.
This includes affine powers with gamma below one, out-of-subset d, and power bases above one.
The affine gamma restriction avoids initial admission of fractional powers at an ill-conditioned computed zero.
It is an implementation scope, not an ICC prohibition.
A later slice needs separate numerical qualification before it expands this domain.

The lower-base check uses i64 products of exact wire numerators.
It does not infer validity from a rounded division threshold.
Scalar linear branches and offsets can leave `[0,1]`, but their finite results receive the declared standard clipping.
The API does not require continuity, monotonicity, or profile-model validity from arbitrary public coefficients.
Such model rules remain separate admission work.

## Numerical work and identity

Affine operations retain their high and low binary64 parts through product and sum compensation.
The power calculation includes a first-order correction from the retained low base part.
The finite campaign checks this calculation against independent exact affine expressions and 90-digit Decimal powers.
It does not establish correct rounding or a universal accuracy bound.

A declared large-slope control has an ideal result near `0.999999880792536599`.
An ordinary rounded-base power differs by approximately `1.81899e-12` and fails the predeclared `1e-12` criterion.
The dedicated corrected regression requires error at most `1e-15`.
Selected power or linear formulas that are algebraic identities preserve input bits, independent of unused branch coefficients.
That identity check follows full shape and domain admission.
It does not bypass invalid public values.

The expanded identity control first exposed a negative-zero sign loss in the new implementation.
Its failed native observation and source remain separate from later passes.
The general identity path corrects that behavior without an input-specific exception.
An additional four-case probe then exposed selected-branch identities with different unused coefficients.
Its exit four remains a failure despite the earlier corpus pass.
Branch-specific algebraic checks correct those paths after full public admission.

## Ownership, cancellation, and scope

The function borrows coefficient storage and returns an owned scalar result.
Callbacks remain outside Cells, and alias updates through callbacks preserve the observed coefficients through COW.
Successful calls use two cancellation polls, before numerical work and before success.
Invalid public records return before callbacks.
The coefficient count is at most seven, with no output array.
These counts do not establish physical/process bounds, a CPU bound, or a hard deadline.

Six unit groups cover all formulas, branch selection, clipping, domains, identity, gamma extremes, compensation, COW, cancellation, and an existing frozen sRGB wire curve.
They pass on native, Wasm, ARM64/QEMU10, and the Go interpreter.
The Go checker accepts the same source.
All compiled unit heaps are nonzero and balanced with zero live blocks.

Issue #13669 declares the initial domain and absolute error criterion of `1e-12` before implementation.
The independent generator uses Fraction expressions for exact branches and affine terms from the original IEEE input.
It then uses 90-digit Decimal powers with an explicit wide exponent range and standard clipping.
The 1,497 vectors include 1,369 successful evaluations and 128 expected errors.
They cover adjacent branches, subnormals, wire extremes, large slopes, output clipping, identities, frozen curve values, unsupported domains, and cancellation.

The collector requires exact errors, poll counts, row identities, finite output, exact identity bits, and the declared finite numerical criterion.
Each fresh process handles at most eight inputs and must report one nonzero balanced heap census with zero live blocks.
Eight fault-control groups preserve numerical, exact-bit, error/poll, row, process, sanitizer, heap, and reference requirements.
Failures preserve the complete prefix and failed output, then return nonzero.
The initial native smoke uses uncommitted source and does not qualify the final immutable campaign.

## Recorded finite observations

Final records use committed source `103243525a4987f48607dd0e7406c02370d6b1c3`.
Each target checks 1,497 cases: 1,369 successful evaluations and 128 expected errors.
All required identity bits, errors, and poll counts match exactly.
The three target sample records are equal.

The largest observed absolute error is `1.1102145542946743e-16`, within the predeclared `1e-12` finite criterion.
The bundle reproduces byte-for-byte with SHA-256 `0cdc457b127704084d974587500658f423c154026b64ae356569120d41135104`.
Admission restrictions, the finite criterion, and existing assertions remain unchanged through the identity corrections.

The campaigns use 188 fresh workers per target, for 564 total workers.
Every worker reports nonzero balanced allocations and frees with zero live blocks.
The native record preserves 1,249 selected source/tool hashes, while each runner record preserves 1,250.
These include all compiler and 163 stdlib sources, worker binaries, the reference bundle, frozen profile inputs, and generator/collector sources.
All selected hashes remain unchanged throughout the campaigns.

The clean compiler source is `b38cca91520de044f7ebf81f7ea6523d1af38a12`.
Its SHA-256 is `d12b97d792d5d66d6bd312cd6723100ecb85ec2609d9430705032e504e5c090b`.
Compiler and stdlib sources match the campaign checkout.
Full native/Wasm scripts and 36 standalone ARM64 executables pass 254 TAP cases per target.
Their 75/51/36 heap censuses are balanced with zero live blocks.
Standalone ARM execution does not establish completion of the whole Go ARM harness.

Both unchanged compiler/stdlib complexity checks pass without skips.
All 2,051 eligible Fern format checks, build, vet, lint, test names, CI self-tests, runtime bundle, protocol controls, and STE checks pass.
A fresh Go oracle built from the same clean source passes checker and interpreter checks for all six unit groups.
Its binary and selected source hashes remain unchanged through those checks.

The raw records are [native](../benchmarks/results/icc-parametric-eval-native.json),
[Wasm](../benchmarks/results/icc-parametric-eval-wasm.json), and
[ARM64](../benchmarks/results/icc-parametric-eval-arm64.json).
[The evidence directory](../benchmarks/icc-parametric-eval-2026-10-27/) preserves current qualification and earlier failed observations.
Earlier passes do not waive either failed identity probe or qualify source after the later correction.
Published historical datasets remain unchanged.

## References and open work

The functions and clipping rule derive from [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), section 10.18 and corrected Table 68.
The official web document was read directly.
Executor PDF access previously returned HTTP 403, so this evidence contains no local PDF digest or access bypass.

The [ICC decision](icc-feasibility.md) and parent #13659 retain profile-model admission, destination/adaptation policy, transforms, and encoded-image integration as separate work.
Photographs, color accuracy, compatibility, performance, security, physical/process bounds, and production acceptance remain unqualified.
No external runtime CMM dependency forms part of this API.
