# Scalar ICC matrix/TRC composition

The `codecs/icc/matrix_trc_eval` package composes the existing curve and matrix kernels.
It accepts owned `matrix_model.ForwardData` and explicit normalized device RGB.
It returns the actual linear RGB trace and unclipped relative D50 XYZ.
This scalar foundation addresses native child #13683 under #13659.

## Public contract

`evaluate(data, input, limits, cancelled)` requires explicit `Relative` intent, nine matrix coefficients, and finite input channels from zero through one.
The default sample limit is 4,096.
Caller limits cannot widen the existing hard sample limit of 65,536.
Each channel uses its existing identity, positive gamma, sampled, or bounded parametric evaluator without wider domains.
Curve errors retain their stage, and cancellation has one common error.

The matrix columns already describe relative D50 XYZ.
The composition applies no additional adaptation, media-white scaling, black-point compensation, clipping, or integer encoding.
Header intent preference does not override the explicit request.
Optional adaptation and text metadata remain opaque.
Model metadata does not certify conformance or transform eligibility.

Cheap admission precedes the first callback.
Curve admission and work then proceed in channel order, followed by the matrix and final cancellation poll.
An identity call polls ten times.
Sampled curves add one poll for each group of at most 64 samples.
Later invalid curves can follow earlier callbacks.
There is no complete-model preflight claim.

Callbacks remain outside cells.
Borrowed coefficient and curve views retain COW isolation from callback aliases.
Returned scalar records retain no borrowed storage.
Exact identity and projection paths preserve selected bits, including negative zero.
This API is not a prepared image plan or a complete profile transform.

## Predeclared finite criteria

Issue #13683 fixes these criteria before implementation.
Each actual linear channel must differ from its independent ideal value by at most `1e-12`.
The matrix result must lie within two representable binary64 steps of the correctly rounded exact dot product of the actual trace.
Identity, projection, and declared exactly representable controls also require exact bits.
These criteria retain the separately qualified curve and matrix requirements.

The full ideal XYZ comparison uses `row_L1 * 1e-12 + B(A)`.
Here, `A` is the exact rational matrix dot product of the reported trace.
`B(A)` is the greatest exact distance from `A` to the values two representable steps around its correctly rounded result.
This term includes the rounding residual and changes in spacing at exponent boundaries.
The criterion concerns the finite corpus, not a universal numerical bound.

The primary reference uses exact Python `Fraction` branches and 90-digit `Decimal` powers.
It covers all existing curve forms, signed matrices, raw IEEE boundaries, cancellation, and public model changes.
The worker reports both the linear trace and XYZ bits.
The collector checks each component criterion and the derived full comparison independently.

## Secondary offline CMM observations

Only the frozen 588-byte profile participates in CMM comparisons.
Its SHA256 is `3bccbfedf06978c3035b255167afcf462a50b27805096cee038ff4dd69c650a6`.
The reference uses the existing Pillow 12.3.0 wheel and LittleCMS 2.19 public API.
It converts `RGB_DBL` to `XYZ_DBL` with relative intent and flags 324.
These flags disable optimization, caching, and the white-on-white fixup.
Black-point compensation remains disabled.

The predeclared secondary absolute XYZ criterion is `1e-6`.
This criterion accounts for the reference pipeline's Float32 stages and does not replace the stricter component checks.
The fixed grid, neutral levels, branch neighbors, and seeded triples yield 1,264 distinct CMM observations.
Other models still receive all primary mathematical checks.
Their CMM inapplicability is explicit, rather than a skipped Fern test.

The host reference reruns all applicable observations before each target campaign.
This does not execute LittleCMS inside Wasm or ARM workers.
The generated profile has a variable creation timestamp, so reproduction uses the frozen fixture.
No runtime CMM, Pillow, or Python dependency enters the Fern API.

## Protocol and qualification

The private `FTR1` file format carries model inputs and cancellation controls for tests only.
Each fresh worker handles at most eight cases.
The collector requires exact row identity, order, errors, polls, successful exits, and one nonzero balanced zero-live heap census.
It rejects sanitizer findings and preserves failed batches with their validated prefixes.
Its process timeout limits the experiment, not the public API.

Nine protocol tests include eight fault groups for numerical drift, exact bits, CMM observations, rows, errors, polls, exits, heaps, and reference identity.
A clean source commit must precede the final three-target campaign.
Selected source, reference, compiler, Python, Pillow, library, worker, and runner hashes must remain unchanged throughout each campaign.
The earlier uncommitted native campaign remains separate development evidence.

There is no encoded JPEG admission, destination RGB, alpha, resampling, or metadata integration change.
Existing strict JPEG entry points still reject ICC and Exif metadata.
Wider models, intents, adaptation, photographs, performance, security, and production acceptance require separate work.
No broad compatibility, exact CMM parity, physical/process bound, or hard-deadline claim follows from these observations.

## References

[ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), sections 8.3.3, 8.4.3, Table 68, and Annex F.3, defines the matrix/TRC sequence.
Annex E.4 describes adaptation metadata in relation to the PCS.
The official web reader supplied the document, and this record makes no local PDF digest claim.

[LittleCMS 2.19 public header](https://github.com/mm2/Little-CMS/blob/lcms2.19/include/lcms2.h) defines the reference formats, flags, and public functions.
The test reference uses the existing offline wheel library only.
The API restrictions and finite criteria above are project policies.

## Completed finite campaign

Source `581469fb562de3c41890722a986b65e49726f644` precedes all final campaigns.
Each target checks 2,824 cases: 2,709 numeric results and 115 expected errors.
The three targets have exactly equal sample records.
All 1,059 fresh Fern workers exit successfully with nonzero balanced heaps and zero live bytes.
Each campaign reruns all 1,264 applicable offline CMM observations on the host.

The largest observed linear-channel error is approximately `1.1102230246251565e-16`.
Every matrix result has zero representable-step distance from the correctly rounded exact dot of its actual linear trace.
The largest observed difference from full ideal XYZ is approximately `3.735297468793823e-12`.
Every full comparison satisfies its unchanged derived criterion.

The largest observed CMM difference is approximately `6.090074364810578e-7`, within the predeclared `1e-6` secondary criterion.
This observation does not establish exact CMM parity or a universal error bound.
All 1,279 native and 1,280 runner hashes remain unchanged during their campaigns, including all 163 stdlib files.
The reference bundle reproduces exactly with SHA256 `86bc90068249b58c6ce76e1f109fcbec93031993068d96e7ec020ea14662243a`.

All 282 TAP cases pass on native, Wasm, and standalone ARM64/QEMU10.
The complete runs report 79 native, 55 Wasm, and 40 ARM balanced zero-live heap censuses.
The 40 standalone ARM executables do not qualify the whole Go ARM harness.
All 1,334 selected full-run source hashes and the compiler hash remain unchanged.

The committed Go source check and interpreter pass all six groups.
Both unchanged complexity gates pass without skips in 8.956 seconds.
All 2,063 selected Fern files pass the format check.
Go build/vet, workflow lint, test names, CI selftests, runtime bundles, Go formatting, nine protocol tests, and prose checks pass.

The new records are `benchmarks/results/icc-matrix-trc-{native,wasm,arm64}.json`.
Development failures and qualification evidence remain in `benchmarks/icc-matrix-trc-2026-10-27/`.
The initial Wasm unit run failed because its runner omitted the fixture preopen.
The corrected runner passes with the same compiled module, and both records remain available.
No numerical tolerance, domain, baseline, validation, or runtime dependency changed after a failure.
