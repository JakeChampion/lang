# Forward JPEG block transform

The forward transform is an internal prerequisite for the baseline JPEG writer.
It does not produce a JPEG file.
All runtime computation uses Fern.

`dct.forward(samples, cancelled)` accepts exactly 64 eight-bit component samples in row order.
It subtracts 128 and computes the separable forward transform from T.81 A.3.3.
It returns 64 binary64 coefficients in natural order.
Index `v * 8 + u` identifies vertical frequency `v` and horizontal frequency `u`.

The forward and inverse transforms share the same basis constants.
The inverse transform retains its arithmetic order and quantizer contract.
Uniform blocks produce exact DC coefficients and zero AC coefficients.
Every block's DC coefficient is the exact sum of its shifted samples divided by eight.
The other coefficients use binary64 arithmetic.

An incorrect length returns `Shape` before cancellation or sample access.
Cancellation returns `Cancelled` without a partial coefficient array.
The transform checks cancellation before work, once per row or frequency in each pass, and before success.
The uniform path checks every eight output coefficients.
Input windows remain borrowed and unchanged. Results are owned and calls retain no shared state.

The finite reference collector uses the independent direct two-dimensional sum with 90-digit decimal arithmetic.
It retains the inverse-transform campaign's absolute tolerance of 0.000001 per coefficient.
It also requires exact DC output and zero AC output for every uniform block.
The targeted tests cover shape, orientation, ownership and every cancellation checkpoint.
The native and Wasm image scripts include them.

This stage does not choose quantizers, quality scales, color conversion, alpha handling or sample factors.
It does not establish JPEG precision certification, visual quality, throughput, physical memory bounds or production readiness.
