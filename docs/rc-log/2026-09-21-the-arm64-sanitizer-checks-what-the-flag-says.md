# 2026-09-21 — the arm64 self-host sanitizer checks what the flag says

#9882. `asm_ir.fern` had a use-after-free quarantine, an over-release report
and a leak verdict, all reached by `FERN_SANITIZE`; `asm_arm64_ir.fern` had
none of them. The flag was still accepted for an arm64 target, so a green run
under it was evidence of nothing and nothing said so. Found while fixing the
review finding on #9881, which had caught the inlined `rc_inc` dropping the
poison check on x86-64: fixing that exposed that arm64 never had it.

## What was missing

The poison word appeared three times in the x86-64 emitter and zero times in
the arm64 one, under any spelling. So on arm64:

- no rc-word load compared against `ast.RcPoison`, on any path;
- no free site wrote the poison, and every free site still pushed its block
  onto a freelist — a recycled block overwrites its own poison, so the
  quarantine would have been unsound even with the compare;
- `__fern_rc_underflow` was bumped but never reported, so an over-release
  stayed a counter nothing reads until the run is over;
- `FERN_LEAKCHECK` was deliberately NOT folded into `FERN_SANITIZE`, for the
  honest reason that a "sanitize" build printing only a census would
  misreport what it checks.

## What landed

The x86-64 shapes, in arm64's spelling, at every site the x86-64 emitter
guards: `__fn___fern_rc_inc`, `__fn___fern_rc_dec` (arm64's own stub, which
x86-64 does not have — its `rc_dec` maps to the freeing `arr_dec`),
`__fn___fern_arr_inc_elems`, `__fn___fern_alloc_reuse`,
`__fn___fern_snapshot_dec`, `__fn___fern_arr_dec`, `__fn___fern_str_free`,
`__fn___fern_str_view_free`, the four deep-free variants, `__fern_arr_push`'s
containment gate, `__fern_arr_push_owned`, `buf_block_free`, and the register
path's inlined retain in `ssa_rc_prim`.

Two differences from x86-64 forced by the ISA, both in the shared helpers so
no site spells them itself:

- **The poison needs two halves.** No 32-bit move immediate reaches
  `0x7EEDFACE`, so `rc_poison_into` emits `movz` + `movk` into a scratch
  w-register the site names, and the compare is register-to-register rather
  than against an immediate.
- **The trap is a `bl` through a live label, not a conditional branch to the
  symbol.** `__fern_san_abort_uaf` is emitted in the ENTRY unit while the rc
  helpers go into ordinary generated code, so under `-per-module-emit-all`
  the edge has to survive as an extern; `b.ne <live>` then `bl <abort>` uses
  the imm26 form that a sibling unit's `bl` already resolves against.

With the quarantine and the over-release report both present, the census fold
is no longer a misreport, so `leak_check_on` folds `FERN_SANITIZE` as x86-64
does, and the leak verdict line joins the summary. Both report texts are the
native backends' byte-for-byte: a `fern-sanitizer:` line must not tell you
which compiler produced the binary.

The one declared gap versus native's arm64 backend is the same one x86-64
declares: no backtrace under either report. Native routes through
`__fern_report`, which walks the frame-pointer chain; there is no such helper
here, so the message is the whole diagnostic.

## What is not reachable, and why the test says so instead

The over-release report has no program that reaches it in this runtime.
`__rc_dec` maps to the freeing `__fn___fern_arr_dec`, so a double free hits
the quarantine's poison one instruction before the underflow test would see a
zero that no longer exists — exactly what the x86-64 leg already documents on
`sanSelfHostDoubleFreeSrc`. So `TestSelfHostOverReleaseReportArm64` is an asm
contract (the bump arms call the report, the report body carries the native
text, both vanish with the flag) rather than a run, and says why.

## Gates

`internal/e2eselfhost/self_host_arm64_quarantine_test.go`. Note the filename:
`..._arm64_test.go` would have been read as a GOARCH build constraint and the
file would never have compiled on this host — it reported as a pass.

- `TestSelfHostUafQuarantineAsmContractArm64` — the poison store, the compare
  routed to the abort, the abort body and its text; no small-tier freelist
  push anywhere, with `__fern_alloc`'s own pop (x1) still present so the
  allocator keeps bumping over empty lists; and none of it with the flag off.
- `TestSelfHostUafIncAfterFreeReportedArm64` — the run, under qemu-aarch64,
  for `FERN_SANITIZE` and the standalone `FERN_RC_FREE_DEBUG` alike: exit 124
  and the diagnostic, and no census from the standalone flag.
- `TestSelfHostUafSilentWithoutFlagArm64` — exit 0, stderr empty.
- `TestSelfHostSanitizeCleanRunIsSilentArm64` — the census fold, and a clean
  run silent of `fern-sanitizer:` lines.
- `TestSelfHostSanitizeLeakVerdictArm64` — three unreclaimed blocks named in
  the verdict, main's exit code untouched.

`TestSelfHostSSARcPrimitivesAreInline`'s arm64 leg no longer skips the poison
assertion; both legs now derive the word from `ast.RcPoison` and differ only
in how their ISA spells the compare.
