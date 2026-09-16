# arm64-linux defaults to the SSA backend

**Date:** 2026-09-16

## The decision

`-backend ssa` becomes the default on **arm64-linux only**. x86-64-linux keeps
the stack-machine emitter, and `-backend ssa` still selects the SSA one there
explicitly.

The two targets got different answers because the evidence differs, not
because the flip is half-done.

## arm64-linux: flip

Self-host driver (`examples/self_host/asm_run.fern`), measured on the merged
emitter:

| | size | compile |
| --- | ---: | ---: |
| SSA (the new default) | 8,753,979 | 30.9 s |
| stack machine (`-backend flat`) | 10,113,457 | 22.9 s |

**13.4% smaller**, and 35% slower to compile. On runtime,
`docs/ssa-log/2026-09-16-full-sweep.md` has all 28 `examples/bench` programs:
after the string-reclaim work the SSA build is at or under the stack machine
on every one of them. The corpus differential runs 348 programs on both
backends on every change and all 328 that both build agree.

The compile-time cost is the honest price. It buys a smaller binary that runs
no slower on any program measured, and the binary is what ships.

## x86-64-linux: hold

Two things are unresolved there, and neither is true on arm64:

- The SSA driver is **2.0% larger** than the stack machine's (8,459,027
  against 8,294,824). 91% of that is 49,612 `movsxd`, all register-to-register
  at 3 bytes each — the i32 high-half fix applied whether or not any use reads
  those bits. The stack-machine emitter emits four in the whole driver.
- `string_rfind_byte` is 1.60x on the full sweep and not root-caused; the two
  backward AVX2 kernels differ only in how they enter the loop.

Lazy sign extension is the slice that settles the first, and it is worth
~149 KB on x86-64 and ~114 KB on arm64 (28,580 `sxtw`), so it is wanted on
both targets regardless. When it lands and the kernel outlier is understood,
x86-64 is a one-line change to the same function.

## What a build that needs something SSA does not serve gets

`resolveBackend` hands it to the stack-machine emitter. That is what makes
this a default rather than a migration: nobody learns a new flag to keep what
they had. The list is `--run`, `-cc`, `-export`, `-shared`, `-g`, `-cover`
and `-sanitize`.

The first three were missing from the first version of this change, and each
failed SILENTLY rather than loudly, which is why they are now enumerated in
the code rather than left to be rediscovered:

- `--run` reached the SSA block with an empty output path, and that block
  writes assembly to stdout when there is nowhere to write a binary. So
  `fern -target arm64-linux --run p.fern` printed the program's assembly and
  exited 0 instead of running it.
- `-cc` was ignored, because the SSA path links in process and never consults
  it. `-cc /bin/false` — a linker that cannot link — exited 0 and wrote a
  binary.
- `-export` is served by the stack-machine emitter through
  `EmitWithOptions{Exports}`, and the SSA path drops the list.

Naming the backend explicitly is a different matter, and the answer is not
uniform: `-shared`, `-g` and `-cover` are refused outright
(`ssaUnservedFlag`), `-sanitize` warns that the build carries no checks and
proceeds — `TestSanitizeWarnsForSSABackend` pins that deliberately, and
changing it is not this slice's call — and `--run`, `-cc` and `-export` are
simply not reached.

## Tests

- `TestDefaultBackendPerTarget`: the default build is byte-identical to the
  named one — `ssa` on arm64-linux, `flat` on x86-64-linux and wasm — and on
  arm64 the two names produce DIFFERENT images, so the first assertion cannot
  pass by both names reaching one emitter.
- `TestArm64DefaultBuildCarriesUnwindData`: a plain `fern -target arm64-linux`
  carries a CIE with the aarch64 profile's header, inside the R+X `PT_LOAD`,
  with a findable `PT_GNU_EH_FRAME`. The existing gates name a backend or
  build x86-64, so none of them covered what this target now ships — and a
  gate that checks a backend by name while the default moves under it is
  exactly how both SSA backends came to ship with no `.eh_frame` at all.
- `TestArm64DefaultFallsBackForFlagsSSACannotServe`: `-g` and `-cover` still
  build on arm64 without naming a backend.
- `TestResolveBackendKeepsTheEmitterThatServesTheFlag`: the whole list, as a
  table. Each entry fails silently when it is missing, so the table is the
  record of what the SSA arm64 block does not reach.
- `TestArm64DefaultHonoursExternalCC`: the predicate is half the fix, and this
  is the other half — `-cc /bin/false` has to fail the build. It passes
  `native=false`, as the CLI does unless `-native` is given, because
  `useNative` otherwise forces the in-process link and `cc` never gets a say.
- `TestBackendFlatIsTheDefaultEmitter` is gone. It pinned "flat == default" on
  every target, which this change makes false on one of them;
  `TestDefaultBackendPerTarget` is that test, per target.
