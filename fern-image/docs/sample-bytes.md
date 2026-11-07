# Exact shared sample words

The shared sample primitive reads and writes raw words with explicit byte order.
It prepares exact storage access for integer and floating-point image samples.
It does not extend the image layout or choose a numerical conversion policy.

## Contract

Import `packages/pixels/sample_bytes`.
`read_word(bytes, offset, width, order)` accepts a borrowed view and returns a `u64` word.
`write_word(bytes, offset, width, order, value)` returns an owned byte array.
The byte order is `Big` or `Little`. The width is one through eight bytes.
Offsets and widths count bytes in the supplied view or array.

An invalid width returns `Width` before any offset check.
A negative offset or incomplete word returns `Bounds`.
A value that exceeds the selected width returns `Value` after shape admission.
Eight-byte words accept the complete unsigned 64-bit range.
All input bytes outside the word remain unchanged.
Writes preserve caller aliases through ordinary copy-on-write semantics.

The primitive checks bounds with subtraction before byte access.
It avoids a shift by 64 when it validates value fit.
It does not interpret floating-point bit patterns, normalize values or apply color transforms.
Negative zero, subnormal, infinity and NaN patterns remain exact raw words.
Codec admission and numerical operations must define their own interpretation of such values.

The explicit byte loop accesses at most eight bytes after admission.
The primitive does not add allocation limits or cancellation callbacks.
An owned write can require an array copy when an alias retains the input.
The byte count does not establish a physical memory or time bound.

## Independent validation

The reference uses Python integer byte conversion in both directions.
It covers all unsigned sixteen-bit values in both byte orders.
It also covers every width, integer endpoints, invalid widths, bounds and values, and raw floating-point patterns.
The worker checks caller aliases and reports every numeric result.
The collector compares every output byte, preserves failed evidence and requires a balanced heap census.

Five TAP groups cover byte order, widths, aliases, admission and raw patterns.
Separate protocol controls reject wrong numbers, wrong bytes, bad lengths, incorrect error precedence and invalid heap evidence.
These tests qualify raw storage access, not full image formats or floating-point arithmetic.

```sh
FERN=/path/to/fern PYTHON=/path/to/python bash scripts/test-sample-bytes.sh native
FERN=/path/to/fern PYTHON=/path/to/python bash scripts/test-sample-bytes.sh wasm
uv run --no-project python -m unittest discover -s tests -p qualify_sample_bytes_test.py
```
