# Bounded standalone ICC affine inverses

The `codecs/icc/affine_inverse` package inverts a bounded continuous subset of ICC.1:2022 Table 68 functions one and two.
Native child #13702 declared the domain, policies, and finite criterion before implementation.
These initial bounds are a numerical implementation scope, not ICC prohibitions on other coefficients.
The standalone API does not select a profile or authorize a transform.

## Public contract

`invert(data, input, cancelled)` accepts explicit `tag_data.ParametricData`.
Function one requires three raw s15Fixed16 coefficients `(g, a, b)`.
Function two requires four coefficients `(g, a, b, c)`.
The admitted bounds are `1 <= g <= 4`, `1/4 <= a <= 4`, `b <= 0`, and `0 < a+b <= 1`.
Function one uses offset zero, and function two requires `0 <= c <= 1/2`.
The inverse input must be finite from zero through one, with negative zero permitted.

Input, function, shape, positive-domain values, numeric bounds, and invertibility receive checks before callbacks.
Unknown function types return `Function`.
Types zero, three, and four return `Unsupported` in this kernel.
Incorrect coefficient counts return `Shape`, and nonpositive `g` or `a` return `Domain`.
Otherwise-defined coefficients outside the initial bounds return `Unsupported`.
After the other bounded checks, `a+b <= 0` returns `NonInvertible` because the original normalized function is constant.

The original function includes its specified output clipping before the inverse policy.
An initial flat interval selects its greatest coordinate.
A terminal flat interval selects its least coordinate.
An input outside the actual forward range selects the closest range value and the same inverse policy.
These continuous monotonic curves require no discontinuity policy.

Selected algebraic identity branches preserve input bits, including negative zero.
Other computed zeros use positive zero.
The output coordinate remains within the normalized domain.
Successful calls poll twice, after full admission and before return.
Either poll can return `Cancelled`.
All admitted coefficients are scalar copies before callbacks, and COW isolates borrowed coefficient arrays from callback aliases.

## Numerical design

The kernel retains the offset-free target difference and its low component.
It compares that difference with the positive power range before an offset can erase the range through binary64 rounding.
It then applies the inverse root and affine coordinate conversion.
A low-component root correction preserves the compensated target.
No callback resides in a cell.

The mandatory control uses raw coefficients `[262144, 16384, -16383, 32768]` and input `nextafter(0.5, 1)`.
The exact upper range is `1/2 + 2^-64`, which rounds to `0.5` in binary64.
The input exceeds the exact upper range, so its inverse is exactly one.
A rounded-range clamp returns `0.99993896484375`, with error `6.103515625e-05`.
The ordinary approach must fail the unchanged finite criterion, and the actual kernel must preserve the correct range phase.
This mathematical counterexample preceded the Fern implementation.

The finite absolute-error criterion is `1e-12`, as declared in #13702 before code.
Independent 90-digit Decimal mathematics evaluates the clipped forward function and performs 112 monotonic bisection steps.
Exact Fraction flat coordinates, selected identities, and declared endpoint or dyadic controls supplement that forward reference.
The reference does not repeat the kernel's closed-form inverse root calculation.
All existing curve and matrix criteria remain unchanged.

The expanded corpus contains 1,581 cases.
It includes 224 additional coefficient-neighbour cases and retains all original 1,357 cases unchanged.
The earlier native results remain provisional and separate from the final source-pinned campaigns.
There was no numerical failure, seed exclusion, domain reduction, numerical error waiver, or larger tolerance.

## Protocol and scope

The private `FAI1` worker format stores function type, cancellation index, coefficient count, input bits, and signed coefficients.
This protocol is test infrastructure, not an ICC encoding or runtime API.
Each fresh process handles at most eight cases.
The strict collector requires normal exit, exact rows, expected errors and polls, and one nonzero balanced zero-live heap census.
It preserves failed inputs, output, and the completed prefix.
Eleven protocol tests contain one valid control and ten fault groups, including flat-policy and erased-range faults.

The current forward evaluators, other inverse APIs, model selection, and encoded admission remain unchanged.
Wider affine domains, piecewise inverse forms, destination composition, adaptation, intents, and encoded integration require separate qualification.
No external runtime CMM accompanies this standalone kernel.
Finite observations do not establish universal precision, compatibility, performance, physical/process bounds, hard deadlines, or production readiness.
The [official ICC specification](https://www.color.org/specifications/ICC.1-2022-05.pdf) gives the current formulas in Table 68 and inverse-curve policies in Annex F.1.

## Final observations

Each target passed 1,581 committed-source cases through 198 fresh workers.
All 594 workers exited normally with nonzero balanced zero-live heaps.
Target sample records match exactly.

The largest observed absolute error is `1.7056711454934297e-16`, within the unchanged finite criterion.
All three full regression runs passed 310 TAP tests.
The [qualification record](../../docs/benchmarks/icc-affine-inverse-2026-10-27/README.md) retains source identities, raw evidence, and limitations.
