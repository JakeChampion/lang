# Fern capability audit

Audit date: 2026-10-25. Host: Linux x86-64.

## Versions and evidence

Repository: https://github.com/JakeChampion/lang

Checkout commit: `ba2b7a8cb69a75160b6d8a9e7fcfa289ea15789d`.
The initial checkout contained no local changes.
No Fern compiler was on PATH.

The audit downloaded the Linux x86-64 nightly compiler.
Its version output was:

```text
fern 057be14b8ba8
built for x86-64-linux
```

Full compiler commit: `057be14b8ba89f2dfe1840edbf4b85c4e95d5f51`.
The compiler predates the checkout. These are separate versions.
The compiler contains its own standard library.

The audit also ran `make build` successfully from the checkout.
The first local build reproduced a formatter defect from the nightly.
The local source now contains a fix in `compiler/printer.fern`.
The audit rebuilt the compiler after that change.

Final compiler version: `fern ba2b7a8cb69a-dirty`, built for x86-64-linux.
The dirty suffix includes the local formatter fix and this package.
Final binary SHA-256:
`4aa66b6599ea950fb34ad15018bdc71b9b78257cd00e5450f6670f6ec98bb3fc`

The final checks use the rebuilt local compiler.
The compiler build uses the repository bootstrap pin:
`9eddfb57064f3eb65943ff596da8e819110ebdff`.
`bootstrap/stage0.lock` records its immutable release URL and platform hashes.

Archive SHA-256:
`471b781a1589738531c4b7b720ca1006cf9f0e53b5769e95fc7f78b80530b855`

Binary SHA-256:
`699318d44360e5e9430b2e405ed5783e31feda61e4dbe7aa2207a5f6afb40151`

The nightly archive came from:
`https://github.com/JakeChampion/lang/releases/download/nightly/fern-linux-x86_64.tar.gz`.
The nightly URL is mutable.
The final build command does not depend on that nightly URL.
Use `make build` with this checkout and the local formatter fix.

## Documentation sources

The audit inspected https://fern-lang.netlify.app/.
The home page describes reference counts and native compilation.
Its last-update field reports 2026-10-15.

The checkout README now links to https://jakechampion.github.io/lang/.
The audit also inspected these source documents at the checkout commit:

- `site/src/content/docs/reference/types.md`
- `site/src/content/docs/reference/language-features.md`
- `site/src/content/docs/reference/packages.md`
- `site/src/content/docs/reference/tooling.md`
- `docs/RC-DESIGN.md`
- `internal/stdlib/std/io.fern.md`

The table separates executable evidence from documentation claims.
Unknown capabilities do not support production features in M0.

## Capability matrix

| Capability | Status | Evidence or limit |
| --- | --- | --- |
| Native executable and local modules | Verified | Pixel example compiles and executes on x86-64 Linux |
| Package manifest | Verified | `fern.toml` selects the library entry, all package sources compile |
| Qualified module imports and public types | Verified | Example and suite import the pixel module |
| Standard test runner | Verified | `std/test` emits TAP and returns a failure exit code |
| Dependency resolution and remote packages | Unknown in execution | Reference documents path, URL, workspace, lockfile, and vendor support. M0 has no dependencies |
| Integer types | Documented | `i32`, `i64`, `u8`, `u32`, `u64`, `usize`. No `u16` |
| Wrapping and checked arithmetic | Verified for essential i32 operations | Essential probe checks wrap and checked overflow. Pixel suite checks multiplication and addition paths |
| Checked `usize` arithmetic | Unavailable | Negative probe rejects `*?` with E009 |
| Byte arrays and zero allocation | Verified | `__alloc_u8` and `u8[]` in essential probe and pixel suite |
| Array lengths and indices | Verified | i32 lengths, checked pixel offsets, channel layout tests |
| Array slices | Verified | Essential probe reads `[u8]` without explicit data conversion |
| File I/O and error values | Verified | I/O probe writes, reads, and handles a missing file |
| Result, Option, enums, structs, early return | Verified | Pixel module and tests compile and run |
| Struct and array mutation | Unavailable directly | Negative probes produce E048 and E056 |
| Value updates and alias isolation | Verified | `.with` and struct updates preserve the old image in tests |
| Reference counts and copy elision | Documented | RC design specifies automatic lifetime and non-atomic counts. No allocation complexity guarantee from M0 |
| Heap sanitizer | Verified | All five executables exit with zero live bytes |
| Ownership annotations and precise last-use optimization | Unknown in this workload | No throughput or copy-elision measurement |
| Concurrent tasks | Documented, not executed | `std/async` supports cooperative tasks |
| Native language threads | Unavailable per reference | Language reference states no threads. RC counts are non-atomic |
| Cancellation | Documented, not executed | Task cancellation returns through task control flow. M0 has no cancellation API |
| SIMD kernels | Unknown for application code | Backend uses vector instructions. No image SIMD API was verified |
| FFI | Unknown for this package | Target capability list includes `cabi`. No foreign call was compiled |
| Profiling | Documented, not executed | `-g`, `FERN_PROFILE`, and pprof output appear in tooling reference |
| Alternate targets | Listed, not verified | `fern -targets` lists ARM64 and WASI. M0 runs only on x86-64 Linux |
| Recoverable allocation failure | Unknown or absent in current API | `__alloc_u8` returns an array, not Result. No forced OOM probe |
| Native zlib/DEFLATE | Present in source, unverified | `std/deflate.fern.md` exists. PNG compatibility requires an independent audit |

`__alloc_u8` is an internal-style compiler builtin used by the standard library.
The probe verifies it on this compiler only.
Its long-term public stability is unknown.
M0 uses no raw pointers or manual buffer handles.

## Reproduce the audit

Run these commands from `fern-image`:

```sh
cd ..
make build
cd fern-image
FERN="$PWD/../bin/fern" ./scripts/test.sh
../bin/fern -check packages/pixels/pixels.fern
../bin/fern -targets
```

The test script compiles each positive probe with:

```sh
fern -check SOURCE
fern -target x86-64-linux -sanitize -o OUTPUT SOURCE
./OUTPUT
```

The essential probe contains one TAP case.
The pixel suite contains seven TAP cases.
Both suites pass. The I/O probe, example, and formatter regression return zero.

The sanitizer reports zero live bytes for every executable.
The negative probes reject direct array updates, direct field updates, and checked usize arithmetic.
Their expected diagnostics are E056, E048, and E009.

The `.fern.txt` files are complete minimal reproductions.
The test script copies them to the build directory before compilation.

## Blockers and risks

The core operations required by M0 work on the audited native compiler.
The formatter defect initially blocked the format check.
The [local issue record](formatter-issue.md) includes the reproduction and fix.
The regression verifies both match paths and formatter idempotence.

The full Go compiler suite did not run in this audit.
The system command named `go` is not the required Go toolchain.
There is no tested bounded allocator with a recoverable failure result.

Per-image budgets exclude allocator overhead and temporary copies.
Copy costs, alternate targets, task cancellation, FFI, and application SIMD remain unverified.
Do not use these unknown capabilities as a basis for production claims.

## Additional formatter checks

The audit extracted 107 literal fixtures from the compiler formatter parity corpus.
The rebuilt compiler formatted each fixture twice with identical output.
Ninety fixtures passed the original type check without additional module fixtures.
Their formatted output also passed the type check.
No fixture failed these checks.

This check does not replace the full Go parity harness.
It does not compare formatter output against the Go oracle.
The new parity case remains available for that harness in CI.
The compiler formatter source also passes `fern -fmt -d`.
