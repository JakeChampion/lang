# Restricted JFIF inspection

The new inspector identifies a restricted JFIF header profile.
It does not validate entropy bytes or EOI.
JFIF 1.02 pages 2, 5, and 7 define the marker and component fields.
The local specification SHA256 is `1135325aa12ee6b3def5a374fecdf3d6d0de88dc4f205af7de8d25a9c949e173`.

The first segment must be APP0 with `JFIF\0`.
Versions 1.00, 1.01, and 1.02 are accepted.
Units must be 0, 1, or 2, and both densities must be nonzero.
The thumbnail payload must contain exactly three bytes per thumbnail pixel.
The subset rejects a zero thumbnail dimension with a nonzero partner dimension.

The frame must contain component 1 alone, or components 1, 2, and 3 in that order.
The first profile means Y alone.
The second profile means Y, Cb, and Cr.
Sampling factors retain the existing raw header restrictions.

All additional APP0 segments and every APP1 through APP15 segment are unsupported.
This includes JFXX, EXIF, XMP, ICC, Adobe, and unknown application metadata.
This policy is narrower than JFIF, which permits optional extension segments.
COM segments remain permitted after the first JFIF segment.
The interface reports density and thumbnail dimensions but applies no aspect-ratio resample or thumbnail decode.

The existing header parser runs first and retains its limits and structural errors.
The second marker scan checks the complete first-scan header and permits JPEG marker fill bytes.
No source arrays or borrowed views escape through the result.
Cancellation remains cooperative and polls at each marker and fill byte.

## API and errors

`jfif.inspect(input, limits, cancelled)` returns `Result[JfifInfo, JfifError]`.
The limits use the existing `header.HeaderLimits` structure.
Defaults admit at most 16 MiB of encoded input, 1 MiB of header bytes, and 1,024 segments.
The absolute segment ceiling remains 4,096.
The existing pixel-layout admission also remains active.

`JfifInfo.header` owns the parsed tables and frame data.
`color` is `Gray` for component 1 alone, or `YCbCr` for components 1, 2, and 3.
The result also reports the minor version, density units, both densities, and thumbnail dimensions.
No borrowed source view escapes through the result.
Input updates through an owned alias use normal copy-on-write behavior.

`Header(error)` preserves structural header errors.
`Missing` means that the first segment is not APP0.
`Declaration` covers the identifier, density, units, and thumbnail shape or length.
`Version` rejects versions outside 1.00 through 1.02.
`Components` rejects another frame identifier sequence.

`Metadata` rejects every additional application segment.
`Cancelled` reports a cooperative cancellation request.

Structural header checks run before the profile checks.
A file with both a broken JPEG header and unsupported metadata can therefore return a structural error first.
The second scan uses checked segment bounds within the validated header extent.
It polls each marker and fill byte, then polls again before success.
Cancellation does not provide a hard execution deadline.

This interface accepts a complete first-scan header without entropy data or EOI.
Success identifies the declared profile only.
A future pixel decoder must also validate the complete entropy stream and its final framing.
The raw header and raw plane interfaces keep their independent metadata policies.

## Scope

The density fields describe aspect ratio or physical density, but this interface applies neither resampling nor orientation changes.
The thumbnail bytes remain outside the returned image data.
The profile uses top-down JFIF sample interpretation without an extra gamma correction.
ICC transforms, EXIF orientation, Adobe color transforms, JFXX thumbnails, and unknown application metadata remain unsupported.

The byte and segment limits are logical admission rules.
They do not bound array capacity, allocator overhead, caller retention, peak live storage, RSS, or process memory.
This slice adds no pixel decoder, encoder, external runtime codec dependency, compatibility guarantee, or production claim.

## Local qualification

The clean compiler revision is `6a28836a28abe9c2bf6905bb3a24445b195a7151`.
Its SHA256 is `99bbb2558676f1e59603bdbb49589ba645b37c86c5fac54fa1a5e08194be34a2`.
A standard two-stage build and the bootstrap smoke checks passed.
This compiler includes the runtime module split and the current native hardening changes.
The feature changes no compiler or stdlib implementation.

Eight TAP groups pass on native sanitizer, Wasm leak census, standalone ARM64/QEMU10, and the primary interpreter.
Native and ARM each report 48,542 balanced allocations, while Wasm reports 66,996.
All three targets report zero final live bytes.
The tests cover 36 independent valid field combinations, malformed declarations, application marker families, component identifiers, and every truncated header prefix.
They also cover exact inherited limits, caller alias updates, retained owned tables, and every observed cancellation poll.
Both unchanged compiler and stdlib complexity gates pass without skips.

These finite checks establish the stated profile behavior only.
They do not qualify a complete JPEG decode or broad interoperability.

All 205 image TAP cases pass on native sanitizer, Wasm leak census, and standalone ARM64/QEMU10.
The complete native and Wasm scripts finish with 68 and 44 balanced heap censuses respectively.
The portable ARM run finishes 29 executables with balanced heaps.
Every census reports zero final live bytes.
The native script also passes the unchanged compiler rejection and formatter probes.

All 2,012 Fern format checks, Go build/vet, workflow lint, test selectors, CI self-tests, and runtime-bundle freshness checks pass.
These local observations remain separate from GitHub CI status.
