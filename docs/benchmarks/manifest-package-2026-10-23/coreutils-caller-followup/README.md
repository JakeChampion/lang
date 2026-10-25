# Coreutils callers after package visibility

Caller source: `63dc2c64b9cc4c20c42710ad6b298608c83e94f5`.
Go check and oracle source: `a5aca3218a868c7b85492a12b9d563a6bd9fa44b`.

Coreutils owns its decimal output helpers and extended-precision power cache.
The table generator produces the cache from the same verified powers as the library.
The `stat` writer uses the public numeric writer method.
The `dd` buffers retain their application-owned drop implementation.
The byte-writer test uses the public constructor and preserves its error checks.
All 75 migrated library helpers remain private.

The Go run passes 2,461 checks across five packages without skips.
The source certificate verifies unchanged Go inputs after the last caller update.
All 106 coreutils programs pass both frontends, for 212 checks.
The package matrices pass 195 commands with 30 balanced allocation counts.
The decimal boundary probe passes ten commands with three balanced allocation counts.
The probe includes unsigned and signed integer limits on x86-64, ARM64, and WASI.

The byte-writer suite passes nine checks.
Its Darwin execution check is skipped on Linux after the image builds successfully.
Two additional Darwin images build through both frontends and are not executed locally.
All four affected example checks and two Fern runner checks pass.
The runner checks execute 27 Fern assertions.
The additional WASI and native compiler caller checks pass.

The GNU 9.12 parity run passes all 5,786 checks without skips.
The local timezone bundle restores the missing `PST8PDT` alias to Los Angeles.
The GNU `pr` process receives this bundle through a recorded wrapper.
The wrapper preserves its program name and the relative zone directory layout.
The product code and parity assertions retain their behavior.

Compiler, library, stage0 pin, and size-limit bytes match the earlier measured inputs.
The native image retains its recorded bootstrap and all 11 strict size gates.
The certificate identifies these paths and the native image hash.
The earlier native and installed-compiler records retain their original source scopes.
