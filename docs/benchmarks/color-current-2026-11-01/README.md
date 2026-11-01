This record qualifies the current scalar color foundation for #13212.
The implementation and its numerical and memory contracts already exist.
The original implementation head CI `40189125104` ended as cancelled.
The later image-lane pass recorded in the issue did not establish full-main success.
This follow-up retains those limits and requires fresh full CI before merge.

The runtime source is integrated main `7ea3a2ad73378c33631492f61c1f66d81ee620d8`.
The frozen Fern compiler has SHA256
`16e3b37f335e08c99d673eb3d987cdcc5dde4d5046453ac2b63be49b0fabd1fa`.
Its own stages two and three are byte-identical.
No production code, fixture, numerical tolerance, image limit, or performance
baseline changes in this qualification. Dependency #13211 is complete.

The three unchanged programs contain one essentials case, seven pixel cases,
and six color cases. Each target passes all 14 TAP cases with `-sanitize`.
All twelve processes have nonzero balanced allocations and zero final live bytes.

| Target | Execution | TAP cases |
| --- | --- | ---: |
| ARM64 Darwin | Native | 14 |
| ARM64 Linux | Native Docker ARM64 | 14 |
| x86 Linux | QEMU 7.2.22 | 14 |
| Wasm32 WASI | Wasmtime 46.0.1 | 14 |

Wasm sanitization provides the leak census. It does not detect reference-count
over-release or use-after-free. QEMU does not establish native latency.

The [color contracts](../../../fern-image/docs/color.md) specify normalized
f64 transfer functions, clamps, NaN handling, and nearest-byte conversion
with ties upward. The transfer test retains its original `0.000001` tolerance
for all 256 encoded byte values and the threshold controls.
That finite test does not bound every f64 input or establish identical
floating-point results across all platforms.

The tests retain explicit linear-light compositing, transparent-color rules,
channel order, grayscale, padding, source isolation, and alpha preservation.
They reject malformed public storage, invalid budgets, and nonopaque
backgrounds. Integer byte premultiplication loses information at low alpha.
the API does not promise a one-byte round-trip bound.

Image conversion validates the source and allocates a complete destination.
Caller limits and the hard 67,108,864-byte image ceiling apply to each image.
Source, destination, aliases, and temporary values can coexist.
Those logical limits do not cap aggregate process memory, transient peaks,
or physical RSS. This qualification makes no new memory or performance claim.

[qualification.json](qualification.json) records every command, program output,
source/compiler/binary hash, and census. Reproduce the native pilot from the
repository root:

```sh
uv run --no-project docs/benchmarks/color-current-2026-11-01/qualify.py \
  --compiler /tmp/fern-13999-271-fixed-point --target arm64-darwin \
  --work /tmp/fern-13212-current-native
```

The same collector accepts `--target` and `--runner` for the other recorded
targets. Docker mounts must use the resolved output path on macOS.
The record preserves exact runner identities. Full current-head CI and
review remain mandatory before this follow-up can close #13212.
