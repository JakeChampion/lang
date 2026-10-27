# Standalone ICC inverse selection

`codecs/icc/inverse.invert` accepts public `tag_data.CurveData`, a finite normalized input, caller sample limits, and a cooperative cancellation callback.
Native child #13706 declared the routing, common admission order, and finite criteria before implementation.
The function selects existing qualified inverse kernels without new numerical arithmetic.
It does not select profiles or authorize a complete destination transform.

## Selection and admission

The caller supplies `curv_eval.EvalLimits`.
The existing default is 4096 samples, with an unchanged hard table limit of 65536.
A nonpositive caller limit returns `Limit` before input or curve checks.
A nonfinite input or a value outside `[0,1]` returns `Input` before selection.
Negative zero remains admitted.

| Public curve | Selected kernel |
| --- | --- |
| Identity, gamma, samples | `curv_inverse` |
| Parametric function 0 | `power_inverse` |
| Parametric functions 1 and 2 | `affine_inverse` |
| Parametric functions 3 and 4 | `quadratic_inverse` |
| Unknown parametric function | `Function` error without callbacks |

Every selected kernel retains its current domain and fresh admission.
The entry point does not retry another kernel after an error.
The existing component and forward APIs retain their original unsupported forms.
A public owned record is not an inverse-eligibility or complete-conformance token.

The public errors are `Limit`, `Input`, `Function`, `Shape`, `Value`, `Domain`, `Unsupported`, `NonInvertible`, `Numeric`, and `Cancelled`.
The entry point normalizes the component errors without a numerical error waiver.
Sampled tables retain their value checks, monotonicity checks, and original cancellation precedence during the scan.
The function adds no callback polls or storage of callbacks in cells.

## Retained numerical domains

Identity, positive u8.8 gamma, and monotonic nonconstant sampled curves retain the current curv inverse policies.
Sampled curves support both directions and the qualified flat intervals.
Function zero retains the full positive signed s15Fixed16 exponent domain.
Functions one and two retain their declared continuous affine subset.
Functions three and four retain their declared exact continuous quadratic subset.
Wider affine and piecewise domains remain explicitly unsupported.

The reference is [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), Table 68 and Annex F.1/F.3.
The entry point preserves the qualified flat, closest-range, endpoint, join, and selected identity-bit policies.
The frozen sRGB profile's independently observed upward join gap remains outside the current piecewise inverse domain.
That restriction does not establish profile invalidity, a downward step, or CMM parity.

## Independent reference and paired worker

The new reference retains all 9,975 vectors from the four published inverse reference bundles.
It records every original vector hash and original standalone expectation without modification.
All successful mathematical observations retain the existing absolute `1e-12` finite criterion and every declared exact-bit control.
The original references use exact Fraction forward segments and 90-digit Decimal mathematics.

A standalone API's deliberate cross-function `Unsupported` result belongs to that original API.
The new dispatcher can select a different kernel for the same parametric record.
Its expectation follows the declared route, while provenance retains the original expectation separately.
The reference adds 112 common-admission and identity controls without removal of an original vector.

The private worker evaluates the dispatcher and the selected standalone kernel with separate fresh callback counters.
Both actual results must match their independent expected errors, polls, and numerical criteria.
Successful values must agree bit for bit.
A nonpositive common limit can reject the dispatcher before a scalar standalone kernel checks its input.
Both actual observations remain explicit in that case.

The collector rejects missing, extra, reordered, duplicate, nonfinite, divergent, or out-of-criterion results.
It requires normal process exits and one nonzero balanced zero-live heap census per worker.
It binds the complete original catalogs and rejects a changed original mathematical expectation.
Twelve protocol tests include positive observations and meaningful fault controls.

The private file header is `FDI1` with five signed little-endian i32 fields and input binary64 bits.
The fields select curve kind, sample limit, cancellation poll, parameter, and value count.
Signed i32 values follow the 32-byte header.
This fixture protocol is not a runtime image format or public library API.

No profile selection, matrix composition, adaptation, BPC, encoded admission, or runtime CMM dependency changes accompany this entry point.
Finite observations do not establish universal precision, compatibility, physical/process bounds, hard deadlines, or production readiness.
All actual M5 and epic acceptance gates remain open.
