# Primitive display roots

The method-arity change in #14754 exposed a mismatch in the primitive display rule.
Known receivers supply a qualified method root and a free-function call root.
The primitive display rule treated the free-function call root as an unknown receiver.
Thus, an integer display call retained every primitive numeric formatter.
The float formatters caused nine size regressions in the native performance gate.

The rule now checks the method root with arity zero for unknown receivers.
Known receivers still select their own primitive conversion.
Generic receivers retain the conservative method root until their type becomes known.
Module-qualified free functions retain their separate call roots.

The supported compiler build passes.
Its core SHA-256 is `c254d890d551680dfbf29fc03861977063f96a245eef4ebe2720fdf777cb9b22`.
The comparison uses main `1c692d8773dc9b84389083a7b78ea63a2be97dfe` with the same standard library sources.
The table records emitted assembly line counts on x86-64.

| Program | Main | Corrected |
| --- | ---: | ---: |
| string_build | 5285 | 1926 |
| string_scan | 5639 | 2166 |
| sort_strings | 6571 | 3098 |
| map_string | 12784 | 9311 |
| struct_drop | 5814 | 2341 |
| http_hello | 18368 | 15234 |
| utf8_ingest_unchecked | 10789 | 7735 |
| utf8_ingest_validated | 11372 | 8318 |
| video_qtrle | 9405 | 5680 |

All 40 native static measurements pass the strict performance gate.
The x86-64 video limit decreases from 5738 to 5680 in both baseline files.
No performance limit increases.

The 120 static measurements for x86-64, ARM64, and WASI complete.
Two existing self-host warnings remain: ARM64 call_overhead, 442 against 437, and x86-64 enum_match, 935 against 925.
Valgrind is absent, so this local run does not measure dynamic instruction counts.
CI must check those counts and run the full suite.

Eight new controls cover typed integers, display lowering, a live float conversion, generic primitives, generic structs, function values, and module-qualified calls.
All controls execute on native Linux and WASI.
They also compile for Android ARM64.
The generic display, receiver selection, and complexity checks pass.
The existing method-arity control and display controls also pass.
The imported receiver control still fails on three unreachable free functions.
The main compiler reproduces that separate regression, which #14772 tracks.

Issue: #14770. Parent: #14516.
