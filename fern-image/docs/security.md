# Security boundaries

Treat every external image descriptor and coordinate as untrusted.
M0 validates arithmetic, dimensions, stride, storage length, and coordinates before byte access.
Both caller budgets and the hard byte limit apply before allocation.

A forged public struct does not bypass access checks.

M0 accepts no encoded file or URL.
There is no network fetch path or parser to secure in this milestone.
Future decoders require input, output, metadata, CPU, and decompression limits.

Future services require origin allowlists or signatures and redirect validation.
They must reject private, loopback, link-local, and metadata destinations after each DNS resolution.
They must also prevent DNS rebinding and enforce tenant quotas.

The hard byte ceiling applies to one storage array.
It does not prevent aggregate memory exhaustion through repeated allocations or retained copies.
The allocator has no verified recoverable OOM path in this package.

Production execution requires process limits, concurrency limits, and cancellation tests.
Sanitizer checks cover the test workloads only. They are not a security proof.
