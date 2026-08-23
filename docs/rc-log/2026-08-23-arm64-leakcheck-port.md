# the arm64 self-host backend gains the leak census

The #5362 arm64 half, in the self-host — the port
`docs/SELFHOST-RC-PLAN-PROMOTION.md` names as what extends the x86-only
instrument gate ("the matrix is x86-64-only today because asm_arm64_ir.fern /
wasm_ir.fern have no leakcheck emitter"). An instrument entry, not a fix.

## The mode

`FERN_LEAKCHECK`, read at emit time. On arm64 it is deliberately NOT implied
by `FERN_SANITIZE`: this backend has no over-release trap and no quarantine,
so a "sanitize" build that only printed a census would misreport what it
checks. Porting the sanitizer is its own slice.

## The design, and where it deviates from the x86 self-host

The x86 self-host routes counting through `__fern_hev_a`/`__fern_hev_f` call
hooks because its free sites have fifteen different live-register sets and a
hook clobbers nothing. On arm64 a `bl` hook would clobber x30 in the LEAF
free helpers, so the port uses native's inline-bump style instead — each site
gets a count+bytes bump emitted immediately BEFORE its freelist push, where
the push's own address/head registers (the bump's scratch) are still dead, so
no helper widens its documented clobber set.

- Counting is at the allocator's 8-byte-word granularity on both sides:
  `__fern_alloc`'s rounded request (bumped at entry, before the large tier
  re-rounds to class capacity) and each free site's own class index are the
  same number for the same block, so a block's alloc and eventual free cancel
  exactly. The large tier's capacity round-up is not counted — the free side
  never sees it — and `__fern_large_push` is the single free-side choke point
  for every >=512 KiB block, bumped at the caller-passed bytes after the
  >1 GiB drop test (a dropped block is a leak, not a free).
- 13 free-side bumps: alloc_reuse's mispaired donor, snapshot_dec, arr_dec,
  str_free (data + box), str_view_free, the four array walkers, map_free's
  box, arr_push_owned, and the large tier centrally.
- `__fern_lc_report` is native's arm64 report body verbatim (itoa + write
  subroutines, caller-saved discipline) with the syscall abstraction replaced
  by the self-host `mov x8 / svc` form darwinize already reskins. Called from
  BOTH exit paths — the `_start` epilogue and the exit() builtin — with the
  exit code parked in x19. The counters and report live OUTSIDE the heap
  gate, the x86 hev block's precedent: a heap-free program must still link,
  and reports zeros.

## Non-vacuity

`self_host_arm64_leakcheck_test.go`: a clean string-churn program balances
(allocs == frees, live 0) at the oracle exit; the refused alias chain reads
UNBALANCED — the half a green exit code cannot say; heap-free programs report
zeros through both exit paths; flag-off asm carries no `__fern_lc_` marker
and runs silent, flag-on carries the report and counters. Counts are asserted
as properties, not exact numbers — the x86 legs pin exact counts per shape,
and this suite gates the instrument.

Follow-ups this unlocks: an arm64 leak-matrix leg (verdict differential
against native-arm64), and the sanitizer port that would let the sanitize leg
run there too.
