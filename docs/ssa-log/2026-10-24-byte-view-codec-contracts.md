# Byte views in codec and checksum contracts

Byte consumers accept borrowed `[u8]` inputs.
The checker and semantic contracts now use that type for CRC, byte sums, and BSD sums.
The lowering selects the string or packed-array kernel from the view representation.
A null empty CRC view returns the incoming seed unchanged.
The helpers do not copy the input.

A string constructor must own the bytes it returns.
The semantic builder combines `string_from_bytes_unchecked(__bytes_owned(view))` before the ownership planner runs.
The combined helper constructs the owned string directly from the borrowed source.
The guards retain ordinary behavior when a declaration shadows either builtin.

Unary integer results respect their checked width in the self-hosted interpreter.
The minimum i32 checksum seed therefore remains an i32 after negation.
Explicit i64 and u64 values retain their widths.

The byte fixtures compare string views, owned arrays, and subviews.
Both checkers and both interpreters pass.
The fixtures also compile for four targets and execute on Linux and WASI with sanitizer and leak checks.
All execution censuses have equal allocation and free counts and zero live bytes.
The [integration record](../benchmarks/codec-errors-2026-10-24/main-5e637-integration/README.md) retains the exact source and artifact hashes.
