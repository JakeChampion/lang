# Direct JPEG component planes

The internal component-plane connector writes complete baseline frames from prepared eight-bit planes.
The caller supplies explicit component metadata, tables, strides, edge policies, restart intervals and limits.
The connector assigns no color space or APP marker.
It does not select chroma filters, quality defaults or alpha behavior.

## Input and lifetime

Import `packages/codecs/jpeg/component_planes`.
Each `Plane` contains an owned sample array, a `PlaneLayout` and an explicit `Edge` policy.
`encode` reads the planes through a borrowed view.
It retains at most four descriptors during the synchronous frame producer call.
The descriptors share samples under Fern value semantics and preserve caller aliases.

Each plane must match the frame dimensions and its explicit horizontal and vertical sample factors.
The stride can include unused padding. Only the declared sample extent must exist.
The block limit counts every complete MCU block, including dummy blocks.
The existing geometry and frame modules define factor, component and table admission.

The connector extracts one padded block, applies the qualified FDCT and quantizer, then supplies the entropy consumer.
It does not retain a complete coefficient plane or a padded sample plane.
This storage pattern does not establish allocator capacity or a physical memory bound.

## Errors and cancellation

Invalid plane counts, dimensions, storage and block limits fail before callbacks.
The frame writer retains its metadata, table, restart and exact output-byte checks.
Every output marker, stuffed byte and EOI byte counts against the output limit.
Block, transform, quantization and frame failures retain separate error variants.
Cooperative cancellation from every stage returns `Cancelled` without a partial result.
The frame producer retains its first-error and closed-consumer guards.

## Independent reference

The reference computes sample addresses independently, including partial and complete dummy blocks.
A separate bit reader recovers every coefficient from the complete entropy stream.
It checks byte stuffing, restart sequences, DC predictors, AC positions, alignment and stream termination.
An independent serializer checks the complete marker and entropy bytes.
Offline libjpeg also recovers every physical component coefficient grid.

The mathematical reference uses the earlier 90-digit direct DCT sum.
It retains the earlier absolute coefficient tolerance of `0.000001` DCT units.
After quantization, a coefficient must belong to the integer interval from that bound.
The record counts ambiguous quantization boundaries explicitly.
Constant blocks require exact DC quantization and zero AC values, with no tolerance.
The reference does not increase a threshold after a failure.

Tests cover distinct selectors, one through four components, odd dimensions, nonintegral factors and padded strides.
They also cover malformed input, every output limit, all cancellation checkpoints and input/output aliases.
Run the native and Wasm reference campaigns with `scripts/test-jpeg-component-planes.sh`.
External libraries serve only as offline references. Runtime codec code remains pure Fern.

Full JPEG coverage, public image encoding, comparable native performance and physical memory qualification remain open.
