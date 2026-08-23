# the self-host sanitizer gains the use-after-free quarantine

The native `RcFreeDebug` port (#5545's named gap; the #7409 companion
instrument "make the silent two-thirds of the failure space loud"). Not a leak
entry: this adds a detector, not a fix — the class it exists for is the one
whose census reads clean and whose underflow counter never moves (#7368,
#7393) until something reads recycled bytes.

## The mode

`FERN_RC_FREE_DEBUG`, or implied by `FERN_SANITIZE` (`ast.ApplySanitize`
parity). Compile-time, x86-64 self-host backend only — the honest subset,
matching where the census and the over-release report already live; arm64 and
wasm have no leakcheck emitter to build on.

Under it, NOTHING is recycled:

- Every free path writes `ast.RcPoison` (`0x7EEDFACE`) over the block's rc
  word where it has one — `quarantine_rc` replaces the defensive `rc = 0`
  store — and declines its freelist push (a recycled block would overwrite
  its own poison). 12 open-coded small-tier sites, `__fern_large_push`
  centrally, and `__fern_alloc_reuse`'s mispaired-donor path. The raw map box
  and string data buffers have no rc word; declining the push is their whole
  treatment.
- The rc readers trap on the poison with
  `fern-sanitizer: use-after-free (touched a quarantined block)` and exit 124
  (`__fern_san_abort_uaf`, self-contained so the flag works standalone):
  `rc_inc`, `arr_dec`, `str_free`, the five array-walking frees,
  `snapshot_dec`, `arr_push_owned`, and `arr_push`'s containment gate (an
  inlined rc read that would otherwise copy from a freed buffer). The poison
  is a large POSITIVE value precisely so the immortal `js` guard cannot
  swallow it. `rc_is_unique` stays unchecked (native parity: poison ≠ 1
  declines reuse safely); the `arr_rc` introspection op stays a probe.
- A quarantined block still counts as a FREE (the hev hook runs at the
  release), so the census composes — allocs == frees on a clean run, in
  quarantine mode too.

## The report that moved

The sanitize double-free probe (`__alloc_u8` + `__rc_dec` twice) now reports
**use-after-free**, not over-release: this runtime's `__rc_dec` maps to the
freeing `__fn___fern_arr_dec`, so the first dec reclaims and quarantines and
the second touches the poison. Native's plain `rc_dec` never frees, so the
same source there still reads rc 0 → over-release. An intrinsic-semantics
difference (the `__free`-is-a-no-op precedent), not a diagnostic divergence:
the TEXTS are byte-identical across compilers, and each fires for what
actually happened in its runtime. Native has the mirror asymmetry — its
comment calls UAF "the one check with no deterministic repro" because its
counter fires first; here the quarantine fires first.

## Non-vacuity

`self_host_uaf_quarantine_test.go`: inc-after-free and standalone-flag runs
red with the exact text and 124; no-flag runs silent to completion; the asm
contract pins the poison store, the `cmpl`+`je` routing, the abort body, and
— both directions — that flag-on has NO `%r8`-form freelist push left while
flag-off has them all and none of the markers. The sanitize suite re-pins the
clean-run census balance and the leak verdict unchanged.

Follow-up (next slice): run the leak-matrix cells under `FERN_SANITIZE` as a
second leg, so the latent class (a stray dec into an unclaimed box) goes red
at the cell instead of after its leak is fixed.
