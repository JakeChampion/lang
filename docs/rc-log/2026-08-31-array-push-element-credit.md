# The array push credited its receiver and not its element (2026-08-31)

Native `internal/ir`; all three backends, one shared analysis.

## The defect

`inferParamCountedRetain` asks whether every appearance of a callee's
parameter is a counted store or a non-retaining read. If so, an argument
reaching the callee's result got there through a construction that inc'd
it, so the temp is at rc 2 on the escaping path and rc 1 on the other,
and the caller's `is_unique`-gated drop nets it to one owner either way.

Its `__method_Array_push` arm credited `Args[0]` — the receiver — and
never `Args[1]`, the element. So for

```fern
function emit(s: St, o: Op): St { return St { ops: s.ops.append(o) }; }
```

`paramCountedRetain[emit][1]` was false, `countedArgTemp(1)` refused, and
a fresh temp handed to `emit` was never reclaimed.

## Measured, and what the axis actually is

The issue that filed this (#7867) said the state-threading idiom
`s = s.f(fresh)` was the leak. It is not. The deciding predicate is
`isOwnedByDefaultType`, which admits a struct only when it is
transitively string/array/slice/Map-free:

| element `Op` | 100 rounds | 200 rounds |
| --- | --- | --- |
| `{a: i32, b: i32}` | 208/208 clean | 409/409 clean |
| `{a: i32, s: string, b: i32}` | **308 / 108** | **609 / 209** |
| the same, temp bound to a `var` first | 308/308 clean | 609/609 clean |

A scalar-only element is owned-by-default, so the **callee** frees it —
`__fern_box_free` is in `__fn___method_St_emit`. Add one `string` field,
the type leaves the owned set, the parameter stays borrowed, and nothing
released the caller's reference. Three rounds with a heap string:
12 allocs / 6 frees, and 12/12 after.

## Why the credit is sound, and why native needed no callee change

`emitArrayPush` already emits the element retain:

```go
if needsRcIncOnAlias(n.Args[1], b) && !b.rc.moveSites[n.Args[1]] {
    b.emitAliasInc(n.Args[1])
}
```

A bare parameter satisfies both halves — `needsRcIncOnAlias` is true for
every pointer type, and `isOwnedRcLocal`, which gates every move site,
walks `info.Locals` only, so a parameter is never one.

That is the difference from the self-host, and it is worth stating
because the self-host's counted tier had to refuse the wrapping shape
(#7856, an `__rc_underflow`): there the construction-side retain is
conditional on the field type routing a reclaim, so `Box { o: o }` takes
none. Native's is unconditional. **The two compilers' answers to "is this
credit safe" genuinely differ, and the reason is one predicate.**

The interlock that keeps the credit from double-freeing is
`ownedByCalleeAt`: at an owned-by-default position the callee reclaims
the argument and the stage-(b) dec is suppressed. That is why the
scalar-only row above stays clean rather than over-releasing, and it is
pinned as its own corpus case.

## The result at self-host scale: the decs run and free nothing

Building the self-host with the patched compiler and compiling a
three-loop input, the aggregate counters do not move at all:

| | allocs | frees | live_bytes |
| --- | --- | --- | --- |
| baseline | 11387 | 5206 | 531040 |
| with the credit | 11387 | 5206 | 531040 |

The binaries are **not** identical — the patched driver is 65 KB larger,
about 30 bytes across the 2,204 `LowerState.emit` call sites the static
census predicts. So the obvious readings are that the transform did not
fire, or that it fired and the fix is worthless. `FERN_RC_TRACE` on both
drivers says it is neither:

| event | baseline | with the credit |
| --- | --- | --- |
| inc | 34262 | 34262 |
| **dec** | 76504 | **76586** |
| alloc | 11387 | 11387 |
| free | 5206 | 5206 |

**82 more decrements, zero more frees.** Pairing every `a` against its
`f` by pointer, the unpaired set is identical between the two builds —
6181 blocks, 567072 bytes, and the same count in every size class
(32 B x2654, 16 B x2012, 48 B x917, …).

So the credit is doing exactly what it was derived to do: the temp's
count goes 2 → 1 instead of staying at 2. None of those references was
the last one, because each element is still reachable from a container
the driver never releases. **The element credit is necessary and is not
sufficient; its runtime payoff is gated behind whatever leaks the
containers.**

Two hypotheses were tested first and both are wrong, which is worth
recording so they are not retried:

- *The containers are merely live at process exit.* No: the same shape
  with the container returned out of a builder and dropped only at
  `main`'s exit is fixed too (12/6 → 12/12).
- *`ir.Op` is scalar-only, so the class does not apply.* No: it carries
  `str: string`, so it is not `isOwnedByDefaultType`.

Two smaller facts from the same measurement. Only **four** parameter
positions change across the whole self-host (1842 → 1846 counted
positions): `LowerState_emit[1]`, `BState_emit[1]` and two others — the
2,204 figure is call SITES, not callees. And only 82 of those sites'
decs execute on this input, which is a three-loop program; the site count
is a static bound, not a runtime one.

The lead this leaves is precise rather than open-ended: find what holds
the 6181 unpaired blocks. `stashOwnedArgTemp` is the caller-side
admission the credit feeds, and it is not implicated here — the decs ran.

## The instrument trap, which cost four probes

`inline.go` inlines a single-reference callee, and a loop call site lifts
its cap to 160 ops. An inlined callee has no argument temp to reclaim, so
a probe of this shape reads clean whether or not the bug is present, and
`nm` showed no `__fn_emit` symbol at all. Every probe here carries
`@noinline` on the callee **and** on the producer. Added to
`docs/TEST-GATES.md` beside `FERN_LEAKCHECK`, together with the two
string-specific masks: a ≤7-byte or literal string allocates no block on
single-word x86-64, and a concat with a uniquely-owned left operand grows
in place.

## Next

Slices 2-4 of #7867, in order: the copying-builtin argument credit
(`strbuf_append`, ~770 sites, and it fixes the bound-local form too), the
missing enum and tuple tiers (~395 sites, dominated by the parser's node
constructors, and a second half in the stash classifier for variant
constructors), then tier parity for the array and string classifiers.
Before any of them, the self-host residue above wants tracing.
