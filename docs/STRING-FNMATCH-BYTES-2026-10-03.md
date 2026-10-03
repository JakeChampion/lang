# Byte-oriented filename matching

`fnmatch_bytes([u8], [u8])` accepts raw patterns and names, including NUL and
invalid UTF-8. The existing text API remains available. Both specialize one
matcher through a private input trait; the borrowed wrapper stays within the
byte entry point. Class names are compared against ASCII literals without
constructing strings from arbitrary pattern bytes.

The matcher preserves flags-zero, C-locale behavior: wildcards cross slashes,
leading dots are ordinary bytes, and backslashes quote the following byte.
Unknown classes invalidate a pattern even when negated. An unclosed bracket
falls back to matching a literal opening bracket. Scalar match statuses avoid
allocating tuples for ordinary matches and star retries; range parsing retains
its existing tuple result.

## Validation

The shared fixture checks all 256 byte values, nonzero-offset views, retained
aliases, raw collating symbols, malformed UTF-8 class names, and text/byte API
agreement on class, bracket, escape, Unicode-byte and wildcard cases.

The frozen Linux validation passes the Go interpreter, native and SSA targets,
both WASM modes, primary native/WASM tests with balanced allocation counts,
the existing GNU `ls`/`dircolors` corpus and primary compiler parity, the full
unit suite, and all lint gates. The target groups completed in 1.481 and
25.616 seconds; the GNU and primary consumer groups took 2.521 and 13.783
seconds. Actual reproduced stage-2 Darwin/core-WASM runs each report 4,675
allocations, 4,675 frees and zero live bytes. Its interpreter also passes.
Fresh Darwin Go/interpreter and primary native/interpreter groups pass in
3.808 and 39.743 seconds respectively.

## Controlled measurements

The before module is from `a69b8ca2f`; only the matcher changes. Both use the
same reproduced primary compiler, SHA-256
`a518ac78b011ab3eb8ae4ddc1f66a04d04269d74cac00333956743a74b229595`,
and its matching standard library. The ARM64 Darwin benchmark checks the
result of every workload, uses two warmups and seven alternating samples,
and scales from 16,384 to 1,048,576 calls. Task-owned heavy jobs were idle;
the rest of the desktop was not isolated. Allocation counts are collected
separately from timed binaries.

| Workload | Before median | After median | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: |
| Literal | 22.700 ms | 19.784 ms | 1,048,583 | 7 |
| Star | 85.777 ms | 85.103 ms | 9,437,191 | 7 |
| Classes | 116.261 ms | 85.669 ms | 10,485,767 | 7 |
| Invalid class | 44.631 ms | 29.608 ms | 4,194,311 | 7 |

Literal, class and invalid-class sample ranges are disjoint. Star ranges
overlap, so these measurements do not establish a star-matching speedup.
The seven allocations cover the whole measured program, not each call.
These four workloads do not establish allocation-free behavior for every
pattern shape.

Both native files occupy 49,761 bytes. Code shrinks from 18,380 to 17,544
bytes; unwind data grows from 3,156 to 3,660 bytes; data remains 832 bytes.
No size baseline changes. This prepares raw `dircolors` parsing; it does not
complete that consumer's conversion or the remaining #5714 acceptance work.
