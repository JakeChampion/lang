# The borrowed-param alias, measured at run time — and the leak next door

Addendum to `2026-09-15-selfhost-borrowed-param-alias.md`, which landed the leg
(#9291 / #9298). Two things that entry reasons to rather than measures, plus
one trap anyone re-running its probes will hit first.

## The self-host was not leaking, measured

#9291 asked outright whether the self-host's extra inc strands memory or only
differs, and called that the first thing to check. Two self-host CLIs — one from `5507774`, one
carrying the leg — x86-64, `FERN_LEAKCHECK=1` at COMPILE
time — it instruments the emitted program; setting it when RUNNING the program
does nothing, which is the first way to measure zero of anything here. 1024
rounds through a `@noinline` callee (without `@noinline` the callee is inlined
away and there is no call boundary left to probe):

```fern
@noinline function f(data: i32[]): i32 { var piece: i32[] = data; return piece[0] + data[1]; }
```

```
self-host before (5507774): allocs=1024 frees=1024 live_bytes=0
self-host after  (#9298):   allocs=1024 frees=1024 live_bytes=0
native:                     allocs=1024 frees=1024 live_bytes=0
```

Re-measured against the merged leg, not only the draft that preceded it.

Net-zero both sides, so the leg is a parity-and-speed fix and native's #9244
leak has no self-host counterpart. What the retain cost instead was the
rc == 1 fast paths for the whole call: the caller's buffer sat at rc 2 from the
bind to the frame's exit.

## Why the census cannot gate this leg on its own

The cancelled pair is net-zero, which cuts both ways: a half-landed
cancellation — the retain gone and the alias slot's sweep dec still in place,
or a `.with` mutating the caller's array through the alias — leaves
`allocs == frees` and moves only the ANSWER. So the leak matrix's
`*__alias_param` cells cannot fail on it.
`TestSelfHostBorrowedParamAliasCancelX86_64` is the gate that can: the
cancelled shape plus the two refusals, each asserting the value the program
computes (the copy carries the new element, the caller's `buf[0]` is
untouched) alongside the balance and the sanitizer leg.

## Trap: the native `.with` result is a different leak, and it is louder

Probing the `.with` refusal reads

```
native:   allocs=2816 frees=2560 live_bytes=1576960
selfhost: allocs=2816 frees=2816 live_bytes=0
```

at 256 rounds of a 1024-element array — and that is NOT this leg. The same
shape with no alias at all measures identically: `var w = xs.with(0, 99)`
inside a callee whose receiver is a borrowed param strands the whole copy on
native, one buffer per call. `computeFreeEligible` declines a `.with` result
outright because `__fern_arr_cow_inplace` can hand the receiver back
uncounted, without consulting the `arraySetInc` decision that already forced
the COPY for a borrowed-param receiver. #9299.

It is not the #6057/#6033 shape either: that was the ASSIGNMENT form
`buf = buf.with(i, v)`, whose entry in
`alloc-differential-known-divergences.txt` was retired when it came back within
bound. This is the BINDING form.

## The guard comment that outlived its instruction

`rc_analysis.go`'s arraySetConsumed guard still told the next porter that the
self-host "has not taken the dead-alias cancellation yet" and to bring this
guard across with it. Both halves were spent. The self-host needs no analogue
yet for a reason worth recording rather than re-deriving: it has no
value-position in-place `.with` to collide with. `lower_arr_with_value` clones
unconditionally (`base[0 : base.len()]`), and `lower_plain_arr_with_store`'s
in-place `arr_set` is reached only from `a = a.with(i, v)`, whose reassignment
`da_scan` already refuses for both names.
