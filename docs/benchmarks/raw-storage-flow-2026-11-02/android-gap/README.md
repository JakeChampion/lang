# Android constant-data gap

CI at `feaf3c45b375ce1d651ba92952f4a7005c228d62` rejects Android
executables with `invalid constant data layout`. The Android emitter still
uses the old page-tail gap. The shared compact ELF writer requires a full
target-alignment gap. The repair uses the same gap helper as Linux.

All four compiler stages match
`fd5b67c5cab98a6d6ee1f48ba773b2bb9abcb600c3f43eeb5aee163defd434a4`.
The compiler has 24078193 bytes. The source record binds each input and
the frozen embedding. Earlier cost records retain their original producer.
The new compiler emits byte-identical copies of all 117 benchmark assemblies
and 39 ARM binaries from the preceding qualification.

The Android CLI, Linux-only wrapper builds and both target legs of the
constant-aggregate test pass. The latter now uses the native host compiler
on ARM and x86. Its cases and expected results remain intact.
The Android corpus passes 1199 fixtures with no known divergences.
The harness excludes 274 compile-error or target-excluded fixtures.

The permission controls pass on Linux for both targets and Android PIE.
Android runs on the normal host page size and under QEMU with 64 KiB pages.
The controls reject writes to code and constants, and execution from data.
The large-page Android code probe stores to its current instruction, then
exits with status 3 if the store succeeds. The constant probe stores through
a string pointer. Both require SIGSEGV.

QEMU 7.2.22 omits file names from the large-page Android map report.
The direct probes avoid that report. The first fixture attempt supplied an
unsupported Linux-only harden flag. The next attempt found no named mapping.
Both failed fixture logs remain here. Neither failure establishes writable
code or constants.

The final controls pass without a skipped protection probe.

Source checks, lint, Go vet, formatting and the unchanged complexity gate pass.
The earlier source gate precedes the final Go test updates. The final Go
checks and test logs cover those updates. The first corpus command lacked
its opt-in flag and skipped that leg. `complexity.log` preserves that result.
The final corpus log executes every selected case.

`manifest.json` records file hashes. `source-binding.json` binds the final
test sources, Linux core compiler, parent and container image. The compressed
CI logs preserve the failures before this repair. Native x86 execution costs
and every other current-head non-Netlify check still gate merge.
