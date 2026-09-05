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

Two measurements remain undone, in this order:

1. **Pointer-tracked attribution.** Same trace, but match each free to its
   allocation by pointer and bucket the *survivors* by the allocating pair.
   This is the measurement the first run was meant to be.
2. **The oracle comparison, which has never been run on the compiler's own
   code.** Build one real `.fern` program with the native emitter and with the
   self-host emitter, both under `FERN_LEAKCHECK`, run both on the same input,
   and diff allocs / frees / live_bytes. Identical allocation counts with
   divergent free counts would localise goal-2's RECLAIM gap to the freeing
   side and make it attributable by size histogram — and unlike the whole-
   compiler self-compile, it fits in memory.

Peak is not the missing instrument: peak RSS (1512 MiB) already matches
exit-live (1548.7 MiB) for this workload, so the compiler accumulates
monotonically and exit-live *is* the peak. The open question is what that set
consists of, not when it is reached.
