# Exact ICC XYZ and curve wire data

`packages/codecs/icc/tag_data.fern` reads standalone XYZ triplets and curve tag payloads into owned values.
It preserves integer wire values without floating-point conversion, interpolation, power operations, clipping, or color conversion.
The broader [ICC decision](icc-feasibility.md) and [envelope inspector](icc-envelope.md) remain separate stages.
The existing JFIF/RGB admission does not change.

## API and exact values

`read_xyz_triplet(input, cancelled)` accepts exactly one 20-byte XYZ tag payload.
It returns three signed i32 numerators in `XyzNumerators`.
Their fixed-point denominator is 65,536.
The general ICC type can contain more triplets, but this explicit API admits one.

`read_curve(input, limits, cancelled)` returns one of four `CurveData` variants:

| Variant | Stored wire values |
| --- | --- |
| `Identity` | A zero-entry `curv` payload |
| `Gamma` | One unsigned 16-bit numerator with denominator 256 |
| `Samples` | An owned i32 array of unsigned 16-bit values, each with denominator 65,535 |
| `Parametric` | Function type and owned signed i32 coefficient numerators, each with denominator 65,536 |

Parametric function types 0–4 require exactly 1/3/4/5/7 coefficients in wire order.
Other function types return `TagError.Function`.
All signed extremes remain exact integers.
The reader does not normalize samples or invert gamma values.

Zero, negative, nonmonotone, or out-of-domain parameters can remain valid raw wire data for this reader.
Read success does not authorize numerical evaluation.
A later admission stage must check numerical domains, finite results, branch points, and the requested model before a transform.
These functions do not supply a profile class, rendering intent, destination, or required-tag policy.

## Admission and failure behavior

Both readers check the exact payload length, type signature, and common zero reserved fields.
Parametric data also requires its two extra reserved bytes to be zero.
Payload slices exclude the profile's external alignment padding.
Trailing bytes, incomplete fields, and incorrect function shapes return explicit errors.

Curve limits require positive caller byte and sample allowances.
Defaults are 1 MiB and 4,096 samples, with absolute logical ceilings of 1 MiB and 65,536 samples.
Allowances above those ceilings do not raise them.
Unsigned counts and computed payload sizes use i64 before access, allocation, or conversion.
Admission and wire-shape errors occur before callbacks and output arrays.

The sample array stores i32 values, so its logical element storage is four bytes per sample.
These limits exclude capacity, metadata, compiler temporaries, allocator overhead, and extra caller retention.
They do not establish physical/process bounds, a CPU bound, or a hard deadline.
The runtime allocator does not return a recoverable allocation error through these APIs.

## Cancellation and ownership

Readers borrow input views and return owned scalar values or arrays.
They do not retain the input bytes.
Callbacks can update another alias through copy-on-write without changing the observed input.
Result updates do not change another result, and each call uses fresh state.
The callback remains outside every Cell.

XYZ, identity, and gamma success each use two cancellation polls.
A sampled curve with `n` entries uses `2 + ceil(n/64)` polls.
A parametric curve with `p` coefficients uses `2 + p` polls.
Polls precede initial work, each copy group, and success.
Cancellation during a partial array copy releases the private output through ordinary ownership.

## Qualification

Seven TAP groups check signed extremes, all wire forms, frozen-profile fields, prefixes, trailing bytes, reserved fields, limits, ownership, and every unit cancellation poll.
They pass on native, Wasm, ARM64/QEMU10, and the Go interpreter.
The Go checker also accepts the same test source.
All compiled unit runs report nonzero balanced heaps and zero live blocks.

The independent generator constructs declared values with Python `struct` and checks their exact integer fields.
Its vectors include all parametric function shapes, random signed coefficients, nonmonotone samples, 65,536-entry boundaries, and strict malformed-input policies.
Seven frozen-profile tag observations use independent signed unpack operations on the existing offline profile fixture.
No CMM transform or numerical tolerance participates in this wire-data campaign.

`scripts/test-icc-tag-data.sh` runs eight inputs per fresh worker.
The collector requires exact values, variants, function types, errors, and poll counts.
Every worker must exit zero without a sanitizer finding and report a nonzero balanced heap census with zero live blocks.
Failures preserve complete prefixes and failed process output, then return nonzero.
Eight protocol fault controls check metadata changes, row identity, missing/extra output, strict errors, process failures, and heap failures.

Final records require committed source, repeatable input identity, and immutable selected source/tool hashes on three compiled targets.
This finite evidence does not establish curve-domain validity, numerical accuracy, profile conformance, color accuracy, compatibility, or production acceptance.
Numerical evaluation and a restricted RGB matrix/TRC model remain open under #13659.

### Recorded finite observations

Final records use committed source `9d5a4447ef6db31b963d9da135cc6ce28ebf4b11`.
Each target checks 742 cases: 383 accepted payloads and 359 expected errors.
The successful cases contain 146,383 wire values per target, all exact without tolerance.
All variants, function types, values, errors, and poll counts match, and the three target sample records are equal.
The bundle reproduces byte-for-byte with SHA-256 `08fa6d6039dd86b68a0d81326b39885c561b874b4d92d9e2602236aa5981e3b8`.

The campaigns use 93 fresh workers per target, for 279 total workers.
Every worker reports nonzero balanced allocations and frees with zero live blocks.
The native record preserves 1,245 selected source/tool hashes, while each runner record preserves 1,246.
These include all compiler and 163 stdlib sources, worker binaries, the reference bundle, generator/collector sources, selected Python modules, and frozen profile inputs.
All selected hashes remain unchanged throughout the campaigns.

The clean compiler source is `933843468f0bf6ef3d782c85b9e4d348ca312182`.
Its SHA-256 is `6602687e6c7ed8e67633bbc3b627b4ab61a9a461359d31bb090f248782b08610`.
Compiler and stdlib sources match the campaign checkout.
Full native/Wasm scripts and 34 standalone ARM64 executables pass 242 TAP cases per target.
Their 73/49/34 heap censuses are balanced with zero live blocks.
Standalone ARM execution does not establish completion of the whole Go ARM harness.

Both unchanged compiler/stdlib complexity checks pass without skips.
All 2,045 eligible Fern format checks, build, vet, lint, CI self-tests, runtime bundle, protocol controls, and STE checks pass.
The records are [native](../benchmarks/results/icc-tag-data-native.json),
[Wasm](../benchmarks/results/icc-tag-data-wasm.json), and
[ARM64](../benchmarks/results/icc-tag-data-arm64.json).
Historical datasets remain unchanged.

## References

The wire layouts derive from [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), sections 4.6, 4.8, 10.6, 10.18, and 10.31.
The official document was read through the public web source.
Executor PDF access returned HTTP 403, so this evidence contains no local PDF digest.
No credentials, headers, proxy, or network policy changed for access.
