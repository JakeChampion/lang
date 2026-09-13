# printf's two aarch64 failures are the clone volume under a ten-minute wall

`Test units / test-units-aarch64` failed on exactly two cases —
`TestSelfHostCoreutilsParity/printf/{tiny_values,f_range}` — while
`test-units-x86_64` was green on the same commit (run 34757224111, head
004a088b7). Both feed `coreutils/lib/ld.fern`'s exact decimal expansion a value
far below the subnormal floor (`4e-4951`, `1e-5000`), which is `core/bigint`'s
`to_string` / `__bi_mul_small` over a few hundred limbs.

Neither hypothesis the failure was picked up with survives measurement.

## The self-host binary is right, on both arches

`coreutils/printf.fern` built by the self-host compiler, native build of the
same source alongside, both cases:

| | stdout | stderr | status |
| --- | --- | --- | --- |
| x86-64 | identical | identical | identical |
| arm64 (under `qemu-aarch64`) | identical | identical | identical |

So it is not an arm64 codegen or float-formatting divergence. `%f` of
`4e-4951` is `0.000000` and the three directives each report
`Numerical result out of range`, self-host and native alike.

## It is not a leak either

`FERN_LEAKCHECK=1` at emit, `allocs / frees / live_bytes`:

| case | arch | native | self-host |
| --- | --- | --- | --- |
| tiny_values | x86-64 | 5,906 / 5,879 / 16,240 | 4,985,143 / 4,984,980 / 196,608 |
| tiny_values | arm64 | 5,928 / 5,901 / 31,536 | 5,024,390 / 5,024,227 / 196,976 |
| f_range | x86-64 | 4,549 / 4,508 / 12,720 | 2,648,251 / 2,647,995 / 221,488 |
| f_range | arm64 | 4,596 / 4,552 / 48,592 | 2,660,995 / 2,660,727 / 221,920 |

Frees track allocs and `live_bytes` stays under 250 KB, so the
`.with`-clone reclaim holds — what `2026-09-13-with-clone-supersede-release.md`
fixed for `od` is fixed here too, and peak RSS is 18 MB against native's 10 MB.

What is left is that entry's own closing section: the VOLUME. 844x / 848x the
native allocations on `tiny_values` and 582x / 579x on `f_range` — within 1%
of each other on the two arches, because the static clone gate is
target-agnostic. Wall time follows it: 8.4 s and 3.6 s against native's 0.03 s
and 0.02 s (x86-64), 44 s and 20 s against 0.14 s and 0.07 s under
`qemu-aarch64`. They are the whole cost of the utility's parity leg — the
arm64 cross run of `TestSelfHostCoreutilsParity/printf` is 145 s, of which
`tiny_values` is 100 s and `f_range` 43 s and the other 337 cases are the
remainder.

The sites are the ones already named there — `out = out.with(i, …)` on a slot
bound from a borrowed param (`__bi_mul_small`) or a struct field
(`BigInt.to_string`, `cur = a.mag`), one whole-array clone per iteration.

## Why CI named these two

Nothing about them failed. `internal/coreutils` compiles all ~70 utilities
twice — native and self-host — and runs both corpora, and the units lane passed
no `-timeout`, so the binary ran against go test's ten-minute DEFAULT. The
aarch64 leg crossed it; the x86-64 leg did not. Both cases pass when the
package is run on its own: `TestSelfHostCoreutilsParity/printf` is 339/339 in
34 s natively on x86-64 and 339/339 in 145 s on the arm64 cross leg
(`FERN_COREUTILS_TARGET=arm64-linux`, `FERN_COREUTILS_QEMU=qemu-aarch64`).

The signature in the log is what a timeout panic leaves, not what a diff
leaves: no `--- FAIL` line, no `stdout differs` text, and an elapsed of
`(unknown)` on exactly the tests that were in flight — the two slowest
self-host printf cases, their two parents, and the package: gotestsum's
"5 failures". Everything alphabetically after `printf` never ran at all,
which is the aarch64 leg's `DONE 38689 tests` against the x86-64 leg's
`DONE 52197` on the same commit. Both legs' `go test (units)` step clocked
630 s.

The lane now passes `-timeout 25m`, and
`sourcelint.TestUnitLaneSetsAnExplicitTestTimeout` keeps it there — the same
false-report class as that file's other two gates, from the killed-suite side
rather than the skipped-suite side.

## Still open: the volume

Unchanged from `2026-09-13-with-clone-supersede-release.md`. Native reads the
refcount at RUN time (`cmp dword ptr [rax-8], 1` then
`__fern_arr_cow_inplace`), so it clones once and mutates in place afterwards;
the self-host's gate is static and name-level, and a static gate cannot
separate the first iteration of `out = out.with(i, …)` from the rest, because
both are one lowering of one statement.

Promoting it to the runtime test is still not a transcription:
`__fern_rc_is_unique` is an ownership oracle only where every alias is counted,
and `struct_lit_unretained_borrow_field` records string, string[] and
enum-array fields reaching a new box uncounted. An in-place write under a wrong
uniqueness answer is a wrong ANSWER, not a leak. Which alias kinds are counted
is the measurement that has to come first; it has not been made.
