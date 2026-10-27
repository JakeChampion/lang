# Standalone ICC function-zero inverses

The `codecs/icc/power_inverse` package inverts the pure power from ICC.1:2022 Table 68 function zero.
Native child #13700 declared its domain and finite criterion before implementation.
Affine and piecewise inverse forms remain separate work.
This initial scope does not prohibit those forms in ICC.

## Public contract

`invert(data, input, cancelled)` accepts explicit `tag_data.ParametricData`.
Function zero requires exactly one positive raw i32 exponent at scale 65536.
All raw values from 1 through 2147483647 are in scope.
The normalized input must be finite and lie from zero through one, including negative zero.
The result uses the reciprocal exponent `65536 / raw_exponent`.

Input, function, shape, and exponent admission precede callbacks.
The result reuses `parametric_eval.EvalError` variants.
Unknown function types return `Function`, and types one through four return `Unsupported`.
An incorrect coefficient count returns `Shape`.
A nonpositive exponent returns `Domain`.
Invalid input returns `Input` before the other checks.

Successful calls poll twice: after full admission and before return.
Either poll can return `Cancelled`.
Callback aliases cannot change the admitted exponent or borrowed coefficient data through COW.
The scalar result retains no coefficient storage.
Exponent one and the declared endpoints preserve input bits, including negative zero.

## Qualification policy

The predeclared finite absolute-error criterion is `1e-12`.
Independent 90-digit Decimal mathematics evaluates the reciprocal power over exact binary64 inputs and exact fixed-point exponents.
The corpus contains 640 vectors with exponent extremes, identity neighbours, subnormals, input neighbours, dyadic values, cancellation, admission errors, and pinned random values.
Declared identities and endpoints also require exact bits.
All existing curve and matrix criteria remain unchanged.

The private `FPI1` worker format stores function type, cancellation index, coefficient count, input bits, and signed coefficients.
It is test infrastructure, not an ICC encoding or runtime API.
The strict collector requires normal exit, exact protocol rows, expected errors and poll counts, and one nonzero balanced zero-live census per worker.
It preserves failed input files and output.
Ten protocol tests contain a valid control and nine fault groups.
They reject numerical errors, incorrect bits, incorrect admission priority, malformed references or rows, process failures, sanitizer faults, and invalid heap reports.

Each immutable three-target campaign requires a prior source commit.
No numerical error waiver, seed exclusion, domain reduction, or larger tolerance can follow a failure.
Finite observations do not establish universal precision, compatibility, performance, physical/process bounds, hard deadlines, or production readiness.

The current forward evaluators, sampled inverses, model selection, and encoded admission remain unchanged.
No external runtime CMM accompanies this standalone kernel.
Affine and piecewise inverses, destination composition, adaptation, intents, and encoded integration require separate qualification.
The [official ICC specification](https://www.color.org/specifications/ICC.1-2022-05.pdf) gives the current pure-power formula in Table 68 and inverse-curve context in Annex F.1.
