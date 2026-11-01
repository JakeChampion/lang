# Division integration with helper reachability

This report preserves #13981's signed-division change and applies the
helper selection from #13959. The October 31 report retains the original
paired control. This report measures the combined source.

`static.json` records all 117 rows from `scripts/perf-bench-selfhost`,
the compiler hash, source tree and native map probes.
`FERN_IR_VERIFY=1` and `FERN_STRICT_IR=1` apply to every compile.
The strict comparator passes with the original tolerances.
Neither performance baseline contains a higher pin than main `31cdefec4c`.

The primary compiler also reproduces the original native map counts:

| Target | map_int | map_probe_chain |
| --- | ---: | ---: |
| x86-64 Linux | 8,478 | 8,482 |
| ARM64 Linux | 8,504 | 8,514 |

These counts are indented assembly lines, not byte sizes.
The x86 compiler image measures 15,718,000 bytes, below the unchanged
15,881,456-byte limit. The stack verifier models all 8,355 functions,
with zero skips.

The numeric reducer, both lane qualifiers, typed helper controls, boxed
drop fixtures and constant-division runtime cases pass locally.
Both full printer shards pass with 529 and 519 valid programs across
1,820 selected source files. All source selection, validity floors and
deadlines remain. The docs site builds, and its sidebar browser test passes.

The ARM division test uses the host CLI and retains every oracle case and
shape assertion. The IR strength driver uses the current execution wrapper.
Both now run on a Linux ARM host with QEMU, where the old guards skipped.
An AMD64 container attempt fails in stage0 with an illegal instruction
under host emulation. It supplies no semantic result.
Current-head CI must supply native x86 coverage before merge.

The later integration preserves the owner's `ccdca92680` changes
and public `parser.module_qualifier`. `owner-merge.json` records
the fresh compiler hash and all 117 static rows for that source.
All rows pass strict comparison, and the four native map counts remain
the same. The compiler image remains 15,718,000 bytes, with all 8,355
functions modelled and zero skips.

That generated compiler builds a two-function program under QEMU.
The program returns the expected 42.
Both lane qualifiers, all numeric reducer targets and both TCP port
regressions pass after the merge. The TCP tests let the server announce
its bound port before the client connects. All byte and EOF checks remain.
