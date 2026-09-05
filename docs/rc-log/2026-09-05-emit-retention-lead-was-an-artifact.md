# The `LowerState.emit` retention lead is a measurement artifact

*2026-09-05* — a negative result on #7954, recorded so the same aggregation is
not run again. The instrument it produced (`FERN_RC_TRACE_DEEP`, #8612) is
sound; the conclusion drawn from its first run was not.

## What was measured

`caller` (#8525) cannot attribute a sole-owner append: that reaches the
allocator as Fern code -> `__fern_arr_push_owned` -> `__fern_arr_push` ->
`__fern_arr_box`, so `caller` is the wrapper for every owned append in the
program. `FERN_RC_TRACE_DEEP` walks one link further, and a deep-traced
compiler lowering `examples/self_host/checker.fern` gave 23,971,095
allocations, 3.26 GB requested, 7,343,098 frees. Aggregating *net* bytes by
`(caller, caller2)` put `LowerState.emit` from `emit_dec_sweep_except_list` on
top at ~620 MB / 19%.

## Why the number is not a retention number

**A free does not bucket under the allocating function.** An `f` line's
`caller` is whatever drop helper released the block — `__fern_rc_dec`, a
`__struct_drop_*`, the exit sweep — never the code that asked for the memory.
So allocations bucket under producers, frees under releasers, and a
subtract-by-pair cancels nothing. Every pair's "net" is its *gross* allocation
with an unrelated quantity removed.

Attributing retention needs the free matched to its allocation **by pointer**,
which the pairing consumer `parseHev` already does for small traces and which
this run did not do.

One thing does survive the correction: the three top rows are three distinct
return addresses resolving to one symbol pair, and that is real rather than a
bucketing fault — `emit_dec_sweep_except_list` has 70 direct `.emit(` sites,
and these are its three hottest.

## The lead was wrong on its own terms too

`LowerState.emit` appends inside a struct-literal spread:

```fern
pub function (s: LowerState) emit(op: ir.Op): LowerState {
    return LowerState { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
```

`s` is a receiver, so the append cannot claim sole ownership by the
`slot < n_params` rule, and `lower_arr_with_value` says this shape clones —
"clone = base[0 : base.len()]", "every `Builder { xs: s.xs.append(v) }`
immutable-update (the EmitState/LowerState threading) flows through here". That
would be O(n) bytes per append and would explain both the churn and the 3.4×.

It does not happen. Under `FERN_LEAKCHECK`, 20,000 spread-appends:

| probe | allocs | frees | live_bytes |
|---|--:|--:|--:|
| `S { ...s, ops: s.ops.append(op), n: s.n + 1 }` | 20016 | 20014 | 262216 |
| `ops = ops.append(i)` (self-reassign) | 15 | 14 | 262168 |
| `T { ...t, n: t.n + 1 }` (spread, no append) | 20000 | 19999 | 48 |

Sixteen array allocations for 20,000 appends is doubling, not cloning, and
`live_bytes` is **1×** the final buffer rather than 2× — so the superseded
generations are reclaimed. The shape takes the runtime-guarded in-place grow
(the `inplace[rj]` arm of the struct-literal reuse emitter), not the
clone-then-grow path its comment advertises. Two live blocks at exit are the
final buffer and the final struct.

A ladder adding one `LowerState` trait per rung (2000 appends) does not move it
off that path either:

| rung | allocs | frees | live_bytes |
|---|--:|--:|--:|
| two fields | 2012 | 2010 | 16456 |
| + a `string` field | 2012 | 2010 | 16464 |
| + a nested struct field | 2014 | 2010 | 16536 |
| + a second array field | 2013 | 2010 | 16488 |
| element type is a struct (`Op[]`) | 4012 | 2010 | 112456 |

The last rung's 2002 extra live blocks are the element boxes the live array
holds at exit, not a leak: an `i32[]` stores its elements inline and a struct
array stores 2000 boxes.

`FERN_APPEND_REPORT` (#8511) is silent on all of this — it reports the
self-reassign form only, and the spread form never reaches it.

## What this rules out, and what is left

Ruled out: the compiler's busiest allocator is not a leak site, and the
`(caller, caller2)` grid cannot say which one is. Eight probes across two
sessions — owned `i32[]` / `string[]` appends, a returned array, an array in a
struct, a functionally-threaded struct carrying one, and the five rungs above —
all reclaim, so the retention still does not reproduce outside the compiler.

One measurement remains undone: **pointer-tracked attribution**. Same trace,
but match each free to its allocation by pointer and bucket the *survivors* by
the allocating pair. That is the measurement the first run was meant to be.

## The oracle comparison, run

The other one is done, and it is the number goal 2 has been missing.

`examples/self_host/asm_ir_run.fern` — the whole compiler front-end plus both
x86-64 backends — emitted **twice from one source**, once by native and once by
the self-host emitter, both under `FERN_LEAKCHECK`, then run on the same input.
The subjects are real compiler modules that happen to have **no imports**, so
the loaderless driver takes them whole:

| input | emitter | allocs | frees | freed | live_bytes |
|---|---|--:|--:|--:|--:|
| `x86_native.fern` (223 KB) | native | 3,053,976 | 2,530,142 | 82.8% | 28.9 MB |
| | self-host | 3,696,014 | 1,387,610 | **37.5%** | **265.9 MB** |
| `arm64_native.fern` (386 KB) | native | 4,188,444 | 3,524,780 | 84.2% | 32.4 MB |
| | self-host | 4,768,896 | 1,633,872 | **34.3%** | **269.0 MB** |

Both emitters produce byte-identical asm for each input (2,869,663 and
2,537,980 bytes), so this is one program's memory behaviour under two RC
implementations, not two programs.

**The gap is on the freeing side, and only there.** The self-host allocates 21%
and 14% more — a real but modest reuse gap. It frees **45% and 46% as many
blocks**, and retains **9.2× and 8.3×** the bytes. The roadmap's "reuse is
substantially complete; the RECLAIM side is where the work remains" now has a
measurement behind it.

The ratio is not a scale effect: the same driver pair on a 7-line program
(20,000 appends across 200 rounds) gives allocs 11,996 vs 14,437, frees 10,446
vs 4,606, live_bytes 85,472 vs 1,438,112 — 16.8×.

Nor is it a counting artifact. The allocation counts nearly agree, so both
runtimes tick the same events in `__fern_alloc`; it is the `__fern_free` side
that diverges, and `make distcheck`'s 13.3 GiB peak corroborates that the
retained bytes are real. 266 MB retained on a 223 KB module is the whole-
compiler OOM in miniature — and, unlike that OOM, it reproduces in a minute.

### Why this reproduction matters more than the numbers

Every previous attempt to attribute the retention needed either a synthetic
probe (which does not reproduce it) or the whole-compiler self-compile (which
does not fit in memory). This fits in both: 16 s to emit the driver, seconds to
run it, 3.7 M allocations rather than 24 M. It is small enough to trace with
pointer tracking, and it is real compiler code rather than a shape guessed at.

### Recipe

```
go run ./cmd/fern -target x86-64-linux -o fern_sh examples/self_host/fern.fern
FERN_LEAKCHECK=1 go run ./cmd/fern -target x86-64-linux -o drv_native \
    examples/self_host/asm_ir_run.fern
cd examples/self_host && FERN_LEAKCHECK=1 ../../fern_sh -target x86-64-linux \
    -emit asm asm_ir_run.fern ../../internal/stdlib -o drv_sh.s
gcc -nostdlib -no-pie -o drv_selfhost drv_sh.s
./drv_native   < examples/self_host/x86_native.fern > /dev/null
./drv_selfhost < examples/self_host/x86_native.fern > /dev/null
```

The self-host CLI takes its stdlib root as a **positional** argument after the
entry file; without it every `std/` import silently resolves to nothing and the
failure surfaces much later as "call to undefined function ... no module was
loaded".

Peak is not the missing instrument: peak RSS (1512 MiB) already matches
exit-live (1548.7 MiB) for this workload, so the compiler accumulates
monotonically and exit-live *is* the peak. The open question is what that set
consists of, not when it is reached.

## Pointer-tracked attribution, on that reproduction

Both remaining measurements are now done. The traced driver — the same emit
with `FERN_RC_TRACE=1 FERN_RC_TRACE_DEEP=1` added — compiling `x86_native.fern`
gives 3,695,957 allocations and 1,387,550 frees, every free matched to its
allocation by pointer (**0 unmatched**), leaving 2,308,407 surviving blocks
holding 274,270,688 bytes. That total agrees with the untraced run's
`live_bytes` to 3%, so the trace is accounting for the same retention the
oracle comparison measured.

Bucketing the **survivors** by their allocating `(site, caller, caller2)`:

| retained | share | blocks | site <- caller <- caller2 |
|--:|--:|--:|---|
| 29.8 MB | 10.9% | 3,113 | `__fern_arr_push` <- `LowerState.emit` <- `lower_expr_ident` |
| 24.4 MB | 8.9% | 1,804 | `__fern_arr_push` <- `LowerState.emit` <- `emit_str_concat_reclaim` |
| 19.6 MB | 7.2% | 175,281 | `pl_none` <- `peep_line` <- `peephole_push_pop` |
| 19.0 MB | 6.9% | 169,781 | `pl_classify` <- `peep_line` <- `peephole_push_pop` |
| 11.1 MB | 4.1% | 99,234 | `pl_one` <- `pl_classify` <- `peep_line` |
| 6.8 MB | 2.5% | 1,673 | `__fern_arr_push` <- `LowerState.emit` <- `lower_expr_dispatch` |
| 5.9 MB | 2.2% | 618 | `__fern_arr_push` <- `LowerState.emit` <- `LowerState.release` |

Two clusters carry it, and they have opposite shapes.

**The IR op accumulator, ~25%, in a few thousand blocks averaging 10 KB.**
Large and few is the signature of superseded array generations: one live ops
array per lowering is expected, thousands of multi-KB buffers are not. So the
first run's headline was pointing at a real retainer after all — it just could
not have known, since the quantity it ranked was gross allocation.

**The `asm_ir` peephole pass, ~20%, in 470,000 blocks averaging 110 bytes.**
Small and many: a per-line record allocated by `peep_line` and never released.
Self-contained in one pass, which makes it the easier of the two to attack.

### What `LowerState.emit` actually emits

Reading the driver's own asm rather than inferring it, `__fn_irlower__LowerState__emit`:

```
call __fn___fern_rc_is_unique     ; on the STRUCT BOX s, not on s.ops
jz   .Lemit_61                    ; unique -> skip
call __fn___fern_rc_inc           ; not unique: retain s.ops for the older box
.Lemit_61:
call __fern_arr_push              ; the LEAK-ON-GROW push, both paths
...
call __fn___fern_arr_dec          ; unique path only: release the old array
```

so the uniqueness test decides whether the superseded buffer is released, and
the not-unique path retains it deliberately, for a `LowerState` that still
points at it. The open question is why those retained buffers are never
released afterwards. Note that `__struct_drop_irlower__LowerState` is not among
the 187 drop helpers the self-host emits — but native emits only 9 helpers in
the same binary, so the two use different drop strategies and a missing helper
name proves nothing by itself. That is the thing to establish next, and it is
not established here.

### Reproducing

Add `FERN_RC_TRACE=1 FERN_RC_TRACE_DEEP=1` to the driver emit in the recipe
above, link, and pipe the run's stderr through a pointer-tracking aggregator
that buckets survivors by allocating triple; `nm` on the linked driver resolves
the addresses. The run takes about 40 s.

## The peephole cluster reproduces: #8628

The smaller-blocks cluster now has a shape. `peep_line` classifies one assembly
line into a `PLine` box and hands it to a fixed-size window that
`peep_flush` / `peep_close_run` periodically REPLACE — and a struct-literal
spread that overrides an array field never releases the superseded array:

```fern
struct W { src: string, w: i32[], n: i32 }
function w_flush(own q: W): W {
    if (q.w.len() < 8) { return q; }
    var keep: i32[] = [];
    return W { ...q, w: keep, n: 0 };
}
```

| emitter | allocs | frees | live_bytes |
|---|--:|--:|--:|
| native | 7,502 | 7,500 | 48 |
| self-host | 10,002 | 5,000 | 360,080 |

One leaked buffer per replacement. Neither the element type nor the `own`
annotation is the trigger — `P[]` elements leak 35,002 blocks, `i32[]` leak
5,002, dropping `own` still leaks — but removing the *replacement* makes the two
compilers agree allocation for allocation (2012 / 10 on both). In the emitted
code every carried field is `rc_inc`'d into the new box, so nothing moves out of
`q`, and neither `q.w` nor `q`'s own box is ever released.

This is the ninth probe of this investigation and the first to reproduce the
retention outside the compiler. The eight before it were guesses at a shape;
this one was read off an attribution.
