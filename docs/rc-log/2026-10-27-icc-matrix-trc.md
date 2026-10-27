# Scalar ICC matrix/TRC composition

PR #13684 addresses native child #13683 under #13659.
The scalar API composes existing curve and matrix kernels into explicit unclipped relative D50 XYZ with an actual linear RGB trace.
It preserves numerical domains, explicit relative intent, cancellation, errors, and COW isolation.
It adds no destination RGB, adaptation, encoded-image admission, or runtime CMM dependency.

The issue declares all component, propagated, and secondary CMM criteria before implementation.
Source `581469fb562de3c41890722a986b65e49726f644` precedes final campaigns.
Each target passes 2,824 cases and 1,264 applicable host-side offline CMM comparisons.
All 1,059 fresh Fern workers report nonzero balanced zero-live heaps.
All selected source/tool hashes remain unchanged, and the three sample records agree exactly.

All 282 TAP cases pass on native, Wasm, and standalone ARM64/QEMU10.
The complete runs report 79, 55, and 40 balanced zero-live heap censuses respectively.
The committed Go check/interpreter, unchanged complexity gates, format/build/vet/lint checks, and nine protocol tests pass.
The initial Wasm fixture-preopen failure remains preserved beside its correction against the same module.

See [the API and finite criteria](../../fern-image/docs/icc-matrix-trc.md) and [qualification evidence](../../fern-image/benchmarks/icc-matrix-trc-2026-10-27/README.md).
Local results do not establish a substantive CI pass, universal error bound, exact CMM parity, or production acceptance.
All actual M5 and epic acceptance gates remain open.
