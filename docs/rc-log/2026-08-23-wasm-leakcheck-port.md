# the wasm backend gains the leak census — every backend now carries it

The #5362 wasm half, in the self-host: the last backend
`docs/SELFHOST-RC-PLAN-PROMOTION.md` listed as census-less. An instrument
entry, completing the set the x86 hev block started and the arm64 port
(2026-08-23-arm64-leakcheck-port.md) continued.

## The mode

`FERN_LEAKCHECK`, read at emit time. Mode 0 only — the preview1 command core
the harness runs under wasmtime; the component modes have no preview1
`proc_exit` to wrap and their `$fd_write` shims cannot be assumed present.
No sanitizer here either (the same honesty rule as arm64).

## The design, and where wasm made it easy

Unlike the register backends' open-coded free sites, wasm has exactly THREE
counting points: `$__fern_alloc` (bumped after the rounding and before the
freelist pop — a pop is an alloc too, matching the other backends),
`$__fern_arr_dec`'s rc==1 free, and `$__fern_alloc_reuse`'s mispaired-donor
free — each free bump placed before the freelist push overwrites the bsz slot
it reads. Every release funnels through `$__fern_arr_dec` (the `_ptr`
walkers call it per element), so three bumps cover what took thirteen on
arm64. Both sides count the allocator's rounded-8 block size, so an alloc
and its eventual free cancel exactly.

The exit wiring is one definition instead of two call sites: under the flag,
`preview1_imports` renames the raw import to `$__fern_proc_exit_raw` and a
flag-gated reporting `$proc_exit` is defined over it — the exit() op and
`$_start` both call `$proc_exit` already, so both report with no per-site
edits. The counter globals and the report live OUTSIDE the heap gate (the
x86 precedent): a heap-free program links and reports zeros, its report
buffer a fixed scratch at 8..40 (clear of the fd_write iovec at 0..8, free
to clobber at exit) rather than a `$__fern_alloc` a heap-free module lacks.
Labels are packed-word stores; digits are `$__fern_print_int64`'s loop with
fd 2. `$fd_write`'s import gate gains `leak_check_on()`.

## Non-vacuity

`self_host_wasm_leakcheck_test.go` (wasmtime): clean churn balances at the
oracle exit; the refused alias chain reads UNBALANCED; heap-free programs
report zeros through both exit paths; flag-off wat carries no census marker
and runs silent, flag-on carries the report, counters, and the renamed raw
import. Counts are properties, not exact numbers — wasm's reclaim
(rc-headered strings, one shared release) is its own fact, not the x86 row
transcribed.

Follow-up this unlocks: the wasm leak-matrix leg — the full grid on the
third backend, native-wasm verdicts against self-host-wasm.
