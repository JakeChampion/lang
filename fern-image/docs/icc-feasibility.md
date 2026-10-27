# Native ICC feasibility decision

Status: a restricted native path is feasible enough for staged implementation and qualification.
This decision does not implement ICC parsing or color transforms.
The engine continues to reject ICC metadata at the strict JFIF/RGB interfaces.
No external runtime color-management dependency forms part of this decision.
M5 and production acceptance remain open.

## Evidence and current limits

The existing color module implements fixed sRGB transfer helpers.
It clamps their input range and has no profile model, tag parser, PCS adaptation, or rendering-intent selector.
Those helpers cannot directly interpret arbitrary profile RGB or XYZ values.
The JPEG plane decoder returns component samples without an ICC color interpretation.
The explicit orientation operation only permutes channel bytes.

The first candidate is an ICC.1 v4.4 RGB matrix/TRC path for input or display profiles with an XYZ PCS.
The transform will initially support an explicit media-relative colorimetric request to a declared sRGB destination.
This is a proposed admission subset, not a statement that other profiles are invalid.
Unsupported profiles must return an explicit error.
The engine must not discard a profile and reinterpret its samples as sRGB.

| Profile or operation | Initial decision |
| --- | --- |
| v4.4 structural envelope and tag directory | Implement first, without a color transform |
| RGB input/display matrix and curves, XYZ PCS | Implement and qualify in separate stages |
| Sampled/identity/gamma `curv` forms | Isolated curve stage before profile application |
| Parametric `para` functions 0 through 4 | Separate domain and numerical tests before admission |
| v2, earlier v4 rules, or alternate PCS | Defer until their distinct requirements have tests |
| LUT, Lab, CMYK, DeviceLink, named color, or iccMAX | Explicitly unsupported in the first transform |
| Perceptual, saturation, absolute intent, black-point compensation, or HDR | Separate decisions and qualification |
| JPEG metadata extraction or automatic color selection | Separate integration after profile qualification |

## Format facts that change the design

ICC.1:2022 defines a 128-byte header and a tag directory with big-endian counts, signatures, offsets, and sizes.
Tag order is unrestricted.
Tag signatures are unique, while exact offset-and-size aliases can share data.
Partial overlap is forbidden.
The v4.4 layout also requires aligned, contiguous unique data elements with permitted padding.
These rules differ from the sorted Exif IFD policy.

Matrix/TRC input profiles use XYZ PCS data and the required colorant and curve tags.
The ICC.1 PCS uses D50, so a native sRGB destination needs explicit adaptation and encoding rules.
Transform-tag precedence depends on the requested intent.
A future matrix path must not silently ignore a higher-precedence LUT tag.
The initial implementation will reject such combinations until their selection rules have tests.

JPEG ICC embedding uses APP2 payloads with `ICC_PROFILE` plus a null byte, sequence number, and total count.
Reassembly follows sequence numbers rather than physical marker order.
Each chunk can carry 65,519 profile bytes, with at most 255 chunks.
The theoretical container maximum is 16,707,345 profile bytes.
That container maximum is not a physical/process memory bound.

## Implementation sequence

1. Add a standalone v4.4 envelope inspector with owned metadata, checked directory extents, exact-alias support, and explicit scope errors.
2. Add isolated XYZ, sampled-curve, and parametric-curve readers with checked storage and function domains.
3. Add a restricted RGB matrix/TRC model after class, required-tag, PCS, and intent checks.
4. Qualify numeric source-to-destination conversion against independent math and offline LittleCMS with matching intent and destination definitions.
5. Add JPEG APP2 assembly and container/profile consistency tests.
6. Add an explicit managed decode path after component meaning, chroma positions, orientation, and filtering order have a declared policy.

Existing decode entry points retain their behavior throughout these stages.
No stage can imply completion of the next one.
Parser metadata alone cannot authorize a pixel transform.

Tracked implementation work is [the envelope inspector](https://github.com/JakeChampion/lang/issues/13658),
[curve/model qualification](https://github.com/JakeChampion/lang/issues/13659), and
[APP2 assembly](https://github.com/JakeChampion/lang/issues/13660).
These issues remain open and contain no implementation acceptance claim.

## Resource and failure policy for implementation

The initial standalone inspector will use positive caller byte and tag allowances.
Proposed defaults are 1 MiB and 128 tags, with logical ceilings of 16 MiB and 1,024 tags.
The implementation must check unsigned wire arithmetic in i64 before allocation or access.
Curve counts and APP2 chunks require separate positive allowances and aggregate checks.
Source ownership, exact tag aliasing, output ownership, and cancellation require independent tests.

Logical limits exclude capacity, metadata, compiler temporaries, allocator overhead, and extra caller retention.
They do not establish physical/process bounds or a hard deadline.
Future worker/admission work must measure aggregate retained state and actual failure cleanup separately.
A profile identifier is not a trust or authenticity signal.
Unsupported executable or LUT models cannot enter the first numeric path.

## Capability observations

The committed 588-byte fixture comes from offline Pillow 12.3 and LittleCMS 2.19 `createProfile("sRGB")`.
Its copyright field says `No copyright, use freely`.
The file is frozen for repeatable input identity.
A fresh generator can set a different creation time, so no byte-identical regeneration claim follows.
The fixture SHA-256 is `3bccbfedf06978c3035b255167afcf462a50b27805096cee038ff4dd69c650a6`.

The profile declares v4.4, display class, RGB input, XYZ PCS, and 11 directory entries.
Its three curve tags share the same 32-byte data block.
`experiments/icc-primitives.fern` checks that frozen input, unsigned 32-bit reads, signed fixed-point extremes, one matrix sample, and one power sample.
It is not a structural profile parser, complete curve evaluator, or color transform.
The matrix check uses exact dyadic values from the fixture.
The power check uses a 90-digit Decimal reference and a preselected finite absolute tolerance of `1e-12`.

Initial native, Wasm, ARM64/QEMU10, and Go interpreter probes pass those checks.
The compiled executions report 4/9/4 balanced allocations and frees, with zero live blocks.
These are whole-probe counters, not a transform allocation or memory bound.
The clean compiler source is `933843468f0bf6ef3d782c85b9e4d348ca312182`.
Its SHA-256 is `6602687e6c7ed8e67633bbc3b627b4ab61a9a461359d31bb090f248782b08610`.

`scripts/check-icc-primitives.sh` runs the pinned-input probe on `native`, `wasm`, or `arm64`.
It checks output and a nonzero balanced heap census, and records source/tool identity before and after execution.
Optional Go oracle checks use the same input.
CI adds real native/Wasm probe commands without changing the existing image or worker assertions.

Final portable records use source `b0b8b443fb6b79f9c79d3bf7433e73e408d1b611`.
All three compiled probes pass with nonzero balanced heap counts and zero live blocks.
The Go check and interpreter also pass on the same frozen fixture.
Each record preserves all 1,221 selected source/tool hashes before and after execution, including all compiler and 163 stdlib sources.
These include the offline LittleCMS shared library and the corresponding C extension.
Compiler and stdlib sources match the clean compiler build.

Records are [native](../benchmarks/results/icc-primitives-native.json),
[Wasm](../benchmarks/results/icc-primitives-wasm.json), and
[ARM64](../benchmarks/results/icc-primitives-arm64.json).
The native record additionally identifies its Go oracle, while the other records identify their execution runner.
The finite primitive checks do not qualify a transform or prove general numerical accuracy.

## Gates before color-management acceptance

- Check every header, extent, count, alias, gap, reserved-field, and version-policy boundary with independent constructed inputs.
- Test every admitted curve form at branch points, endpoints, nonfinite results, invalid domains, and monotonicity-policy boundaries.
- Preserve exact wire coefficients and compare each numeric stage with high-precision independent math.
- Predeclare finite color/byte tolerances, intent, adaptation, clipping, destination, and black-point policies before a transform campaign.
- Compare with pinned offline LittleCMS on generated inputs and licensed photographs, with every discrepancy retained.
- Test malformed profiles, bounded fuzz campaigns, all cancellation polls, alias isolation, and errors after private output allocation.
- Run native, Wasm, ARM execution, source/tool immutability checks, and strict nonzero balanced worker heap checks.
- Qualify physical/aggregate memory, performance, and production behavior separately before any corresponding claim.

The current primitive tolerance does not supply a transform accuracy bound.
No general ICC compatibility, color-accuracy, photograph, security, performance, or production acceptance follows from these observations.
An external runtime CMM requires explicit user approval and a new dependency decision.

## References

- [ICC.1:2022, profile version 4.4](https://www.color.org/specifications/ICC.1-2022-05.pdf): sections 6, 7.3, 8.3.3, 8.4.3, 10.6, 10.18, and Annex F.
- [ICC technical note, October 2021, embedding profiles](https://archive.color.org/files/technotes/ICC-Technote-ProfileEmbedding.pdf).
- [ICC registry, conversion through PCS](https://registry.color.org/rgb-registry/icctransform).
- [ICC current specification index](https://www.color.org/specifications/).

The official documents were read through the available public web source.
Direct executor PDF downloads returned HTTP 403, so this evidence does not include a local PDF digest.
No credentials, proxy, headers, or network policy changed to obtain access.
