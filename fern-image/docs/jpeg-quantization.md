# JPEG coefficient quantization

The internal quantizer prepares coefficients for a future pure Fern JPEG writer.
It does not produce a JPEG file or choose an image quality scale.

`quantization.quantize(coefficients, quantizers, cancelled)` accepts two arrays of exactly 64 elements.
Binary64 coefficients use natural order.
Nonzero eight-bit quantizers use transmitted zig-zag order, as in the existing inverse transform.
The result contains owned natural-order integers.
The encoder and decoder share the same unchanged zig-zag permutation.

For each coefficient, the quantizer computes the binary64 quotient and rounds it to the nearest integer.
Exact half-integer quotients round away from zero.
The separate fractional comparison preserves representable values immediately below a tie.
The result must fit the inverse transform's signed sixteen-bit coefficient domain.
The future entropy writer must enforce its process-specific coefficient restrictions too.

`Shape` precedes cancellation and input access.
`Quantizer` rejects a zero step, `NonFinite` rejects NaN and infinities, and `Coefficient` rejects an out-of-domain result.
Cancellation checks precede allocation, occur every eight coefficients, and precede success.
An error returns no partial public array.
Borrowed input windows and retained caller aliases remain unchanged.

The reference collector uses exact Decimal conversion of each binary64 quotient and nearest-integer arithmetic.
It compares complete coefficient arrays and explicit errors without a tolerance.
The corpus covers every quantizer, signed ties and their neighbors, ordering, coefficient boundaries, nonfinite input and deterministic mixed blocks.
Fault controls require the collector to reject incorrect values, ordering, extra output and invalid heap reports.

[T.81 A.3.4](https://www.w3.org/Graphics/JPEG/itu-t81.pdf) describes nearest-integer coefficient quantization.
The tie rule above is an explicit implementation choice.
Finite tests do not establish combined JPEG precision certification, quality, performance or physical memory bounds.
