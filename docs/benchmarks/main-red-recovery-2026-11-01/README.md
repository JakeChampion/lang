# Main CI repair evidence

This report measures the #13982 integration with the helper selection
and CI repairs from #13959. It preserves the new E021 diagnostic,
negative fixture and three printer shards.

`static.json` records the source tree, compiler hash and all 117 rows from
`scripts/perf-bench-selfhost`. Every row passes the strict comparator
with the original tolerances. Neither baseline file has a higher value
than main `31cdefec4c`.

The primary compiler reproduces the original native map counts:

| Target | map_int | map_probe_chain |
| --- | ---: | ---: |
| x86-64 Linux | 8,478 | 8,482 |
| ARM64 Linux | 8,504 | 8,514 |

These counts are indented assembly lines. The x86 compiler image measures
15,717,856 bytes, below the unchanged 15,881,456-byte limit. The stack
verifier models all 8,357 functions, with zero skips. Under QEMU, that
compiler builds a program whose expected exit code is 42. The program
returns 42 with strict checks and zero verifier skips.

Both checkers reject the new missing-import fixture with the same E021
message and location. The numeric reducers and focused helper, lane,
release-tail and boxed-drop regressions pass on Linux ARM.
Unit-rest and lint-fast pass. The interpreter array regression passes
its unchanged 80,000-element bound.

All three full printer shards pass. They select 607, 607 and 606 files,
and check 358, 339 and 351 valid programs. The corpus has 1,820 files.
Selection, validity floors and deadlines remain unchanged.
The fresh playground bundle and documentation site build, and all
20 documentation browser tests pass.

The TCP receive fixture now lets its server bind port zero and announce
the bound port. This removes the released-port race before the client
connects. Its byte checks, empty-buffer result, EOF result and expected
exit code remain unchanged. Both affected TCP tests pass locally.

Full current-head CI remains required before merge. Local emulation
does not measure native x86 performance.
