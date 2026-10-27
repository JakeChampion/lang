# Bounded inverse matrix kernel

The `codecs/icc/matrix_solve` package solves an explicit signed 3x3 matrix for linear RGB.
This kernel is a prerequisite of #13659 and native child #13694.
It does not select profiles, evaluate inverse curves, clip channels, or change encoded admission.
It does not infer color meaning from metadata or apply adaptation, black-point compensation, or rendering intent.

## Public contract

`solve(coefficients, input, cancelled)` accepts nine raw signed `i32` coefficients in row order.
Each raw coefficient represents its integer value divided by 65536.
The rows correspond to X, Y, and Z.
The columns correspond to red, green, and blue.
The caller supplies an explicit XYZ value and the correct matrix convention.

Each raw coefficient must lie between -98304 and 98304, inclusive.
Each finite XYZ channel must lie between -2 and 2, inclusive.
The exact raw determinant must be nonzero.
There is no conditioning threshold, positive-determinant restriction, or output clipping.
The output can contain negative channels or channels above one.

These bounds define the initial numerical subset from issue #13694.
They are not ICC restrictions or conclusions about arbitrary profiles.
Shape errors, nonfinite inputs, unsupported bounds, and singular matrices produce distinct errors before callbacks.
A valid call polls before arithmetic and before success.
Either poll can return `Cancelled`.
A defensive `Numeric` error does not exempt an admitted finite corpus case from the success requirement.

The coefficient view retains COW isolation from callback aliases.
The scalar result borrows no coefficient storage.
A signed unit inverse row preserves the selected input bits, including the sign of zero.
Other computed zero results use positive zero.
No callback resides in a cell.

## Arithmetic and finite criterion

The coefficient bounds keep exact integer cofactors and the raw determinant within `i64`.
The determinant magnitude remains below 2^53, and each scaled cofactor remains below 2^51.
The implementation combines compensated products in decreasing magnitude order before a residual correction of the division.
The reference uses exact `Fraction` Gaussian elimination, with row pivots and no cofactor formula.
It uses the actual input binary64 values and raw fixed-point coefficients.

Issue #13694 declared the domain and criterion before the implementation.
Every admitted finite case must succeed within two representable binary64 steps of its correctly rounded exact solution.
Positive and negative zero share one numerical position for that distance.
Signed projections, permutations, exact-zero results, and declared dyadic controls also require exact bits.
The existing curve criterion and forward-matrix criterion remain unchanged.
This finite criterion is not a universal precision bound or an absolute-error tolerance for all output magnitudes.

The mandatory cancellation control uses the frozen sRGB matrix and a small exact red residual.
The ordinary inverse-and-dot computation must fail the numerical criterion.
The compensated kernel must preserve the residual within the declared criterion.
No seed, matrix, conditioning level, or failure receives an exemption.

## Reference and execution

The deterministic generator defines 2,224 vectors, with 2,095 numerical results and 129 expected errors.
They cover signed permutations, negative zero, subnormals, dyadic results, coefficient extremes, near-dependent rows, and determinants with magnitude one.
They also cover the frozen profile, input and coefficient bounds, admission order, and both cancellation polls.
The frozen profile identifies matrix coefficients only.
No CMM or Pillow transform supplies this inverse reference.

The private worker format contains `FMS1`, a cancellation index, a coefficient count, three signed input bit patterns, and raw coefficients.
This file protocol is test infrastructure, not an ICC or runtime API.
The collector starts a fresh process for each group of at most eight cases.
It requires exact protocol framing, errors, polls, and applicable output bits.
Every process must exit normally with one nonzero balanced heap census and zero live bytes.
The collector retains the failing case files and output on any failure.

Eight fault-control groups reject excess numerical error, wrong exact bits, nonfinite output, incorrect errors or polls, and malformed or reordered output.
They also reject bad references, nonzero process exits, sanitizer faults, and missing, empty, duplicate, or unbalanced heap censuses.
The scalar TAP suite also checks COW aliases, fresh calls, and exact projection bits.

## Limits and future work

The API returns unclipped linear values rather than an image or a destination-profile transform.
Inverse curves, their flat-interval policies, clipping before inverse curves, and destination composition require separate qualification.
JPEG ICC and Exif admission remain unchanged.
There is no compatibility, performance, physical/process bound, hard-deadline, or production claim.

The mathematical context is [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), Annex F.3, equation F.7.
That specification separately describes clipping and inverse-curve behavior after the matrix inverse.
This package implements only the explicit standalone matrix solve.
