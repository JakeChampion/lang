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

## The result at self-host scale — unmoved, and unexplained

This is the part worth recording. Building the self-host with the patched
compiler and compiling a three-loop input:

| | allocs | frees | live_bytes |
| --- | --- | --- | --- |
| baseline | 11379 | 5206 | 530784 |
| with the credit | 11379 | 5206 | 530784 |

Byte-identical counters. The binaries are **not** identical — the patched
driver is 65 KB larger, which at roughly 30 bytes a reclaim sequence is
about the 2,204 `LowerState.emit` call sites the static census predicts.
So the transform fired at scale and freed nothing.

Two hypotheses were tested and both are wrong:

- *The containers are still live at exit.* No: the same shape with the
  container returned out of the builder and dropped only at `main`'s exit
  is fixed too (12/6 → 12/12).
- *`ir.Op` is scalar-only, so the class does not apply.* No: it carries
  `str: string`.

`paramCountedRetain["__method_irlower__LowerState_emit"]` goes
`[true false]` → `[true true]`, so the callee half is credited; the
caller half runs `stashOwnedArgTemp`, which has its own admission. Four
param positions change over the whole self-host, and 1842 → 1846 counted
positions total.

**So the leak the self-host driver has at those sites is one step further
down than this fix reaches, and what it is has not been established.**
Recorded as an open lead rather than a claim, because the tempting
reading — "2,204 sites credited, therefore the class is closed" — is the
aggregate-hiding-a-per-item-truth mistake this log exists to stop.

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
