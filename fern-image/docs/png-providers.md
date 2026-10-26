# PNG providers for row graphs

Issue #13414 connects the restricted PNG codec to the provider contract in milestone #13215.
Core image processing remains Fern. Pillow supplies an offline test reference only.

## Decoder sessions

`png.inspect_rows` validates chunks, CRCs, dimensions, pixel limits, and the full row plan.
It does not inflate compressed bytes or validate scanline filters.
`png.open_rows` repeats admission, inflates the complete filtered frame, and validates every filter byte.
A bad final-row filter rejects the session before an earlier selected row reaches a consumer.

The returned `RowDecoder` contains its plan and a `read` callback.
The callback accepts a row index and a cancellation callback.
It reconstructs each required prefix row in ascending order.
It retains one previous row for filter dependencies.
It permits a repeat of the most recent row and rejects older rows with `DecoderAccess`.
Out-of-range requests return `DecoderBounds`.

Every call to `open_rows` creates a private cursor and filtered frame.
An alias of one decoder shares that cursor. A new decoder has independent state.
Returned rows own tightly packed RGB8 or RGBA8 data.
Pixel updates preserve input bytes, retained rows, and later decoder results through copy-on-write ownership.

The existing `png.fold_rows` uses the same session implementation.
Its selected-prefix plan, generic consumer error, and output contract remain intact.

## Graph adapter

Import `packages/codecs/png/provider` and call `provider.source` with owned encoded bytes and codec, row, and graph limits.
The constructor inspects the full PNG row plan and captures the encoded input.
Each graph execution opens a fresh decoder after graph validation and cost admission.
An unused provider does not open a decoder.
The core pipeline has no PNG import.

The adapter declares `ForwardRows` access.
The pipeline requires room for two cached rows at every reachable node.
This cache preserves bilinear revisits without a decoder rewind.
Uncached `render_row` rejects the forward source.
Cached legacy and fused row folds and cropped tiles support the source.

Codec failures return `ProviderFailure` with the prefix `PNG ` and the codec error name.
Bounds, budget, cancellation, and access errors map to the corresponding pipeline variants.
A consumer failure preserves its generic error payload.
A new execution remains available after a failure or cancellation.
External consumer effects and retained values remain with the caller.

## Logical admission

The decoder plan charges these bytes:

```text
2 * encoded input length + complete filtered frame + 2 * source row stride
```

The provider declares the first two terms as fixed storage.
The ordinary two-row allowance covers the previous and new source rows.
The graph plan also charges its separate cache allowance and downstream row scratch.
Cached tiles add their existing tile allocation and copy charges.

Setup work charges `height + height * source row stride` once per graph execution.
This charge covers all filter bytes and every possible prefix byte.
Each source request also charges the source width through the normal sample plan.
The extra request charge remains conservative when the decoder repeats a row.
The graph checks all declared costs before its factory opens or inflates bytes.

These are logical payload and work charges.
They exclude allocator capacity slack, metadata, caller aliases, retained callback values, and callback allocations.
They do not measure CRC work, inflater work, callback CPU time, peak live bytes, or process RSS.
The constructor scans chunks and CRCs before graph execution admission.
Its codec row limits apply before compressed-byte preparation.

The filtered DEFLATE frame remains eager.
Cancellation checks cover preparation boundaries, filter validation, row bytes, provider callbacks, and graph execution phases.
The bulk chunk parser and inflater do not poll cancellation internally.
No hard deadline, recoverable allocator failure, physical scratch bound, aggregate service bound, or production safety follows.

## Validation

Seven targeted cases cover all five filters in RGB and RGBA, input windows, prefixes, repeat access, and backward rejection.
They cover 32 format, nearest/bilinear, and fusion schedules against eager rows and cropped pixels.
They also cover exact admission, codec errors, malformed final filters, input/output ownership, every cancellation poll, and heap consumer errors.
All seven pass on native Linux, Wasm, and ARM64 under QEMU with zero final live bytes.
Native and ARM64 checks use heap sanitization. Wasm supplies the leak census only.

The [PNG qualification record](png-qualification.md) describes the finite independent corpus and its exclusions.
The provider campaign also compares full graph rows and middle cropped tiles through legacy and fused execution.
Historical corpus records retain their original compiler and source hashes.
Generated gradients and seeded noise do not qualify photographs, broad codec compatibility, visual quality, or performance parity.

All 118 current-source image TAP cases pass on these three targets.
The native and Wasm scripts also pass the allocator/cache smoke modes.
The native script also passes the compiler restriction probes.
The portable ARM64 campaign covers nineteen executables and excludes Linux process RSS workloads.

The compiler reports `6964c5998912`. Its compiler and stdlib sources match integrated main `f28bb6945`.
Its SHA-256 is `3267853c7e9b38599ebbc1bb11fe7c07ccf86eae0b61d0b8411ebffb44453bd9`.
Full current-head GitHub CI remains unverified.
