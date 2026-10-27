# Standalone ICC curve inverses

The `codecs/icc/curv_inverse` package inverts explicit identity, gamma, and sampled curves.
It is one prerequisite of #13659 and native child #13698.
It returns one normalized scalar value.
Profile selection, inverse parametric curves, destination composition, and encoded admission remain separate work.

## Public contract

`invert(curve, input, limits, cancelled)` accepts public `tag_data.CurveData` values and the existing `curv_eval.EvalLimits`.
The finite input must lie from zero through one, including negative zero.
Identity and positive u8.8 gamma numerators from 1 through 65535 remain supported.
Sampled curves require two through 65536 u16 values within the caller's sample limit.
The default sample limit remains 4096.
Caller-constructed values receive fresh admission.

Sampled curves must be monotonic and nonconstant.
Both directions and flat intervals are supported.
Constant and nonmonotonic tables return `NonInvertible`.
Parametric curves return `Unsupported` in this first inverse kernel.
The forward evaluators, tag readers, and model selector remain unchanged.
This standalone kernel does not authorize a profile or establish profile conformance.

The inverse uses the exact piecewise-linear forward coordinates `i / (sample_count - 1)`.
A nonterminal flat interval selects its greatest input coordinate.
A terminal flat interval selects its least input coordinate.
Values outside the curve's range select its nearest range value and the same inverse policy.
These policies follow ICC.1:2022, Annex F.1.
They apply within this API's normalized input domain.

Limits, input, variant, gamma value, and table shape receive checks before callbacks.
Sample entry checks poll at each block of 64 entries after one initial poll.
Cancellation can precede a later entry-value or monotonicity error.
Successful calls poll once more before return.
Identity and gamma calls use two polls.
Successful sample calls use `2 + ceil(sample_count / 64)` polls.

The callback is outside cells.
COW isolates borrowed sample data from callback aliases.
The scalar result borrows no table storage.
`Limit`, `Input`, `Shape`, `Value`, `Unsupported`, `NonInvertible`, `Numeric`, and `Cancelled` remain distinct.
A defensive numerical error does not exempt an admitted finite case from the success requirement.

## Arithmetic and reference policy

The sample search compares a compensated product with each raw sample value.
It retains the low product component when the rounded high product equals a sample value.
General selected identity segments preserve input bits after complete admission.
Identity, gamma-one, and declared gamma endpoint controls also preserve their specified bits.
The gamma inverse uses the reciprocal of the raw u8.8 exponent.

Issue #13698 declared the domain and criterion before implementation.
The finite absolute-error criterion remains `1e-12`.
The reference uses independent 90-digit Decimal mathematics for inverse gamma.
For sampled curves, exact Fraction arithmetic enumerates forward segments and applies the inverse selection policy.
It does not repeat the implementation's binary search.
Declared identity, endpoint, and dyadic controls also require exact bits.

The mandatory phase control uses `[0, 21845, 21845, 65535]` with binary64 input `1/3`.
Its rounded product with 65535 equals 21845, while its exact product lies below that value.
An ordinary search on the rounded product returns approximately two thirds.
The exact inverse equals the original input.
The ordinary search must fail the criterion, and the kernel must select the correct branch.

The generator covers 1,974 vectors, including both monotonic directions, flat intervals, nearest range values, and knot neighbours.
It also covers subnormals, gamma extremes, sample limits, all selected cancellation boundaries, and error precedence.
The maximum-table controls reach the final cancellation poll at 65536 entries.
The earlier 1,964-vector native smoke result remains provisional and separate from the expanded corpus.
There is no seed exclusion, domain reduction, numerical error waiver, or larger tolerance after a failure.

## Protocol and scope

The private worker format contains `FCI1`, curve mode, sample limit, cancellation index, gamma parameter, sample count, and input bits.
Raw signed sample values follow that header.
This protocol is test infrastructure, not an ICC encoding or runtime API.
The collector uses at most eight cases in each fresh process.
It requires normal exit, exact protocol rows, expected errors and polls, and one nonzero balanced zero-live heap census.
It preserves the failing input files and output on failure.

Ten protocol tests include a valid control and nine fault-control groups.
They reject numerical and exact-bit errors, incorrect flat policy, malformed rows, bad references, process failures, sanitizer faults, and invalid heap reports.
The reference bundle reproduces byte-for-byte with the recorded generator.
The source commit precedes the final immutable campaigns on all three targets.

These finite observations do not establish a universal precision, compatibility, performance, physical/process bound, hard-deadline, or production claim.
No external runtime CMM accompanies this kernel.
Clipping before inverse curves and complete profile transforms require separate qualification.
The [official specification](https://www.color.org/specifications/ICC.1-2022-05.pdf) describes the inverse-curve policies in Annex F.1 and subsequent matrix/TRC composition in Annex F.3.

## Final observations

The committed source campaign completed 1,974 cases on each of three targets.
All 741 fresh workers exited normally with nonzero balanced zero-live heaps.
The target sample records match exactly.
The largest observed absolute error is `1.1102230246251565e-16`, within the unchanged finite criterion.
All three full regression runs passed 296 TAP tests.
The [qualification record](../../docs/benchmarks/icc-curv-inverse-2026-10-27/README.md) retains source identities, raw records, and limitations.
