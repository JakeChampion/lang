# Isolated ICC matrix evaluation

The `codecs/icc/matrix_eval` package applies a signed 3x3 matrix to explicit linear RGB values.
This kernel is one prerequisite of #13659 and native child #13672.
It does not select a profile, tags, rendering intent, destination, or white-point adaptation.
It does not evaluate curves, invert matrices, encode PCS values, or change JPEG admission.

## Public contract

`evaluate(coefficients, input, cancelled)` accepts nine raw signed `i32` coefficients in row order.
Each coefficient represents its integer value divided by 65536.
`LinearRGB` has finite `r`, `g`, and `b` values in the closed unit interval.
`XYZ` contains three scalar results without clipping.
Negative values, values above one, singular matrices, and zero matrices remain valid kernel inputs or outputs.

The rows contain the X, Y, and Z coordinates respectively.
The columns correspond to red, green, and blue respectively.
Thus the first row contains `rXYZ.X`, `gXYZ.X`, and `bXYZ.X` from separately checked tags.
The caller must supply linear values and the correct matrix convention.

The kernel checks the exact shape and all three input channels before any callback.
A valid call polls once before arithmetic and once before success.
Either poll can return `Cancelled`.
`Shape`, `Input`, and defensive `Numeric` errors remain distinct.
The callback is outside cells, and the coefficient view retains COW isolation from callback aliases.
The scalar result owns no borrowed coefficient storage.

A projection row with one positive unit coefficient and two zeros preserves the selected input bits, including negative zero.
Other computed zero outputs use positive zero.
Products retain a low component before compensated sums combine terms in decreasing magnitude order.
The final fixed-point division follows the sum, which preserves small terms after large terms cancel.
There is no SIMD, timing, allocation-performance, physical/process bound, or hard-deadline claim.

## Finite reference policy

Issue #13672 declared the domain and criterion before implementation.
The reference uses exact Python `Fraction` products of the integer coefficients and the actual binary64 inputs.
Each reported result must lie within two representable binary64 steps of the correctly rounded exact dot product.
Positive and negative zero share a numerical position for this distance.
Projection bits, exact-zero results, and specified exactly representable dyadic values have additional exact checks.
This criterion concerns this matrix domain and does not replace either existing curve criterion.

The bundle covers projections, signed coefficient extremes, adjacent inputs, subnormals, cancellation, deterministic random cases, and a frozen profile.
The frozen profile supplies column values only, without profile selection or transform qualification.
The generator uses no Fern arithmetic and no runtime CMM.
Its rational reference includes every admitted coefficient and every actual input bit.
The earlier native provisional campaign remains a separate development observation.

The test includes a cancellation vector whose ordinary three-product sum loses 74 representable steps.
Its exact rational value fixes the expected result before the Fern comparison.
The naive control must remain outside the predeclared two-step criterion.
This test demonstrates the need for compensation in this finite example, without a universal accuracy claim.

## Collector and future gates

The private worker reads files with the `FME1` test packet identifier.
It reports three signed IEEE bit patterns, an exact error, and callback counts.
The collector requires exact row identity and order, nonzero balanced heap counts, zero live bytes, and no sanitizer finding.
It preserves a failed batch and its validated prefix, and refuses an existing output record.
Its test timeout limits the experiment, not the public API.

The protocol tests reject numerical drift, changed exact bits, nonfinite output, wrong errors, incorrect polls, missing rows, and duplicate rows.
They also reject nonzero exits, sanitizer findings, invalid references, and missing or unbalanced heap counts.
A source commit must precede the final three-target campaign.
Every source, compiler, reference, selected Python module, and runner hash must remain unchanged during that campaign.

Complete model admission, tag precedence, destinations, adaptation, wider curve domains, photographs, and complete transforms require separate issues and evidence.
Existing strict JFIF and RGB entry points still reject ICC and Exif metadata.
No broad ICC compatibility, standards conformance, physical/process bound, or production acceptance follows from this kernel.

## Reference

[ICC.1:2022, profile version 4.4](https://www.color.org/specifications/ICC.1-2022-05.pdf), sections 8.3.3, 8.4.3, and Annex F.3.
The official document defines the column convention after the channel curves.
The numerical criterion and isolated API restrictions here are project policies.
The public web reader supplied the document.
This record makes no local PDF digest claim.

## Completed campaign

Source `039bf0c92e747740c0808456543d2aa3ca268687` precedes every final campaign.
Each target checks 2,015 cases: 1,990 numeric results and 25 expected errors.
The three targets have exactly equal sample records.
Every one of the 756 fresh workers has a nonzero balanced allocation count and zero live bytes.
All 1,250 native and 1,251 runner source/tool hashes remain unchanged during their respective campaigns.

The largest observed difference is one representable step, within the unchanged two-step finite criterion.
The maximum observed absolute difference is approximately `7.275846595269095e-12`.
The step criterion reflects the different magnitudes of these XYZ outputs.
Neither this observation nor the finite criterion establishes a universal bound or changes the existing curve criteria.

The bundle reproduces exactly with SHA256 `78b48df3bb075c022e31eb2e2be082dc66e27cc9c5030c6aedb22be667867a89`.
The three new records are `benchmarks/results/icc-matrix-eval-{native,wasm,arm64}.json`.
The source, protocol, cancellation, and ownership evidence remains in `benchmarks/icc-matrix-eval-2026-10-27/`.

All 268 TAP cases pass on native, Wasm, and standalone ARM64/QEMU10.
The complete scripts report 77 native, 53 Wasm, and 38 ARM balanced zero-live heap censuses.
The 38 standalone ARM executables do not qualify the whole Go ARM harness.
The Go source check and interpreter pass all six matrix groups after the source commit.

Both unchanged compiler and stdlib complexity gates pass without skips in 7.738 seconds.
All 2,057 selected Fern files pass the format check.
Go build/vet, workflow lint, test names, CI selftests, runtime bundles, Go formatting, eight protocol tests, and prose checks pass.
The full scripts preserve all 1,305 selected source hashes and the compiler hash.
No baseline, tolerance, exemption, or runtime dependency changes accompany this evidence.
