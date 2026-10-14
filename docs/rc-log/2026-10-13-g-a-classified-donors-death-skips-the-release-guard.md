# 2026-10-13 — a classified donor's death skips the release guard

`ssarc.classified_death`, `ir.sole`, `ssa_rc_dec` on x86-64 and arm64.
Fixes #11944's remaining half.

## The shape

#12230 reads a classified donor's uniqueness once, at its first take, into
the token slot; the later takes test the token. Its death on the unique arm
released the box through the ordinary `__fern_rc_dec`, which the register
backends render as the inline guard (floor, count above one, immortal) and a
call into the helper's `.u` entry, which reads the count a second time before
it reaches the free. In `deriv.d` and `cfold.reassoc` that is the "release helper
classifies again" step of #11944: the count is read at the take and twice more
at the death, and every read answers one. (`.u` is the entry
2026-10-13-f's guarded release added, past the helper's floor test.)

## What changed

The release the unique arm emits carries `ir.sole` (mark 5 in the SSA). The
fact holds by construction: `ssaunits.first_classified_take` admits nothing
between the take and the death that names the donor but its takes and tests,
and a box with one count has no other holder to retain it from elsewhere, so
the count read as one at the take is still one at the death. Both register
backends lower a sole release as one call into `__fn___fern_arr_dec.u` with
the box in the argument register and no guard in front of it: the box is a
heap box, so it is past the floor test the entry skips, and the entry's one
count read finds the one. Under `FERN_RC_FREE_DEBUG` the inline guard is already off and
the full helper runs, so the sanitizer keeps its poison check. wasm reads no
mark; `$__fern_arr_dec` tests as before.

The donor's shared arm is untouched: its count may have fallen since the
take, and the ordinary release reads it.

## Measured

x86-64, callgrind, the unchanged kernels built with `-O` by the compiler at
ee091d16 and by this change. Both outputs identical.

| Workload | Ir before | Ir after | Binary before | Binary after |
|---|--:|--:|--:|--:|
| deriv, 7 iterations | 20,056,943 | 19,967,617 (−0.45%) | 22,203 B | 21,859 B |
| cfold, depth 14 | 11,601,174 | 11,397,458 (−1.76%) | 18,739 B | 18,499 B |

Each of `deriv.d`'s three classified deaths loses the guard's floor test and
its `cmpl $1, -8(%r12)` and `cmpl $0, -8(%r12)`, and calls `.u` directly,
whose one read of the count finds the one: 27 fewer instructions in
`deriv.d`'s text. The native arm64 counts the issue
reports were taken under valgrind on hardware this container does not have;
the arm64 text has the same shape (`TestSelfHostSoleReleaseShapeArm64`).

The cross-language comparison in `docs/PERCEUS-COMPARISON.md` is not repeated
here: Koka and Lean are not installed on this machine, and the issue asks
that the negative result stand until a same-machine run replaces it.

## Gates

`TestSelfHostSoleReleaseShapeX86_64` / `Arm64` pin `depth`'s text: one
uniqueness test, one `.u` call, no `_rcdecd` guard label, and the program's
exit under the ordinary build, the census and the quarantine.
`TestSelfHostSoleReleaseWasm` runs it under wasmtime.
`TestSelfHostClassifiedTakeShapeX86_64` is unchanged and still passes.
