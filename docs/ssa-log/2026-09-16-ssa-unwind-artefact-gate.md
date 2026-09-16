# The gate that should have caught the missing unwind data

**Date:** 2026-09-16

## Why

Both SSA backends shipped binaries with no `.eh_frame` at all (#9495, #9500),
and every gate the project had was green while they did. The corpus
differential ran 348 programs on both backends and reported 328 of 328
agreeing — it compares what programs DO. Nothing compared what the artefact
CARRIES.

`cmd/fern` already had the artefact tests, but neither could run against SSA:

- `TestNativeLinkPlacesEhFrame` builds with the DEFAULT backend, so it
  exercises whichever emitter is default and never the other one.
- `TestEveryUserFunctionHasAnFDE` uses the `-g` symbol table as its oracle for
  where each function starts and ends, and `-g` is refused on SSA until it
  emits `.debug_line` (#9493). It cannot be given a backend dimension as
  written.

## Change

`TestSSABackendsCarryUnwindData` builds the same three-function fixture for
each target with `-backend ssa` and reads the image, with no `-g` and no
symbol table:

- a CIE exists, and its header is the one the target's profile declares —
  `01 7a 52 00 01 78 10 01` on x86-64, `01 7a 52 00 04 78 1e 01` on arm64,
  which differ in the code alignment factor and the return-address column;
- `.eh_frame` is inside the R+X `PT_LOAD`, since unwinding happens at runtime;
- `PT_GNU_EH_FRAME` exists and opens `01 1b 03 3b`, because that header is the
  only way a running program reaches `.eh_frame`;
- its search table is sized for the FDEs it claims, and every row names a
  function inside the R+X segment and an FDE inside `.eh_frame` itself, whose
  extent comes from walking the entry lengths to the zero terminator.

It does not check that each FDE's range matches its function, which is what
`TestEveryUserFunctionHasAnFDE` uses the symbol table for. That stays out of
reach until SSA serves `-g`.

## Verified against the bug

Deleting `.cfi_startproc` and `.cfi_endproc` from `x86_64ssa` — the state
before #9495 — fails the x86-64 subtest with "no .eh_frame CIE in the
-backend ssa image", and leaves the arm64 subtest green. So the gate catches
the original bug, and catches it per target.

## The pattern

Three findings in one day share a shape, and it is worth naming: **a test that
says "the default" and means "the other backend" stops testing anything the
moment the default moves.** The third is on #4112 — every SSA differential
builds its baseline with no `-backend` flag, so the default-backend flip would
have them compare SSA against SSA and keep reporting 328 agree.
