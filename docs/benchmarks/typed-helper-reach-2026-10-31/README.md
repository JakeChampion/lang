# Typed helper reachability

The map benchmarks on main emit two unused helpers for Option[(i32, Range)].
Their drop and release bodies add 108 ARM64 lines and 97 x86-64 lines.
The candidate selects helpers through their typed physical origins and references from lowered bodies.
It retains all declarations, ordinary extra bodies, failed helpers, and reachable helper graphs.

The static table compares main with the candidate on all 39 benchmarks and three targets.
The candidate reduces 69 of 117 emitted-size rows. No row grows.
Both map benchmarks return to their current native pins exactly.
The baseline changes record only smaller measured values.

Reproduce the static measurement with the primary compiler:

```sh
bin/fern-selfhost -target arm64-linux -emit asm -o /tmp/map.s bench/map_int.fern internal/stdlib
awk '/^[[:space:]]/ {n++} END {print n}' /tmp/map.s
```

Repeat the commands for each source in bench and each target.
The targets are arm64-linux, x86-64-linux, and wasm32-wasi.
The same line count applies to native assembly and WebAssembly text.
The full project measurement is scripts/perf-bench-selfhost.

The regression test covers 12 graph controls for function values, nested constants, dynamic dispatch, cycles, and physical-origin indices.
All 13 current finalizer cases pass on Darwin and WASI with strict verification and zero skipped functions.
Their 26 runtime results match the expected output and balanced allocation counts, with zero live bytes.
The physical-contract probe also preserves the exact typed ABI and balances 73666 allocations.
