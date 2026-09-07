# a frame cannot transfer what it borrowed (#8429)

A declared `own` position CONSUMES one reference. This compiler has no
caller-side retain — `rc_ml_owned_rc_param`'s header states the ABI cut, and
`emit_dec_sweep_except`'s `n_params` boundary is its other half — so a parameter
that is not itself `own` is a borrow the caller still holds, and the frame holds
no reference to hand on.

#4873's self-reassign admission is what lets one reach an `own` position anyway.
`h = absorb(h, …)` inside a plain-receiver method kills THIS binding, which is
all the checker asks; the caller's binding is untouched. The reference the
callee then spends is the caller's only one, so `__fern_rc_is_unique` in
`emit_self_overwrite_reuse` reads 1, takes the reuse arm, and rewrites the
caller's box in place.

```fern
struct S { buf: u8[], n: i32 }
function bump(own s: S): S { s = S { ...s, n: s.n + 1 }; return s; }
function (s: S) mbump(): S { s = bump(s); return s; }
function main(): i32 {
    var a: S = S { buf: zero(4), n: 0 };
    var keep: S = a;
    var b: S = a.mbump();
    return b.n * 100 + keep.n * 10 + a.n;      // expect 100
}
```

| engine | before | after |
| --- | --- | --- |
| `-interp` (oracle) | 100 | 100 |
| native x86-64 | 100 | 100 |
| native arm64 (qemu) | 100 | 100 |
| self-host x86-64 IR | **111** | 100 |
| self-host arm64 IR (qemu) | **111** | 100 |
| self-host wasm32-wasi IR (node WASI) | **111** | 100 |

111 is all three names reading `n == 1`: one box, reused under two aliases. The
issue's own hasher spelling (`var keep = h; var forked = h.update(c)`, a u64
total over 100 rebinds) is the same row and moves 6 to 7 on all three targets.

`emit_own_borrowed_param_arg` buys the reference the callee is about to spend,
at the argument, for a bare ident whose slot is a non-`own` PARAMETER holding a
struct or tuple box. It is the parameter-source sibling of #8267's
`own_lift_retain_sites_of`, which buys one for a lifted field on the same
grounds ("the retain is paid for by the `own` position that consumes it").

## The second name is not the trigger

`receiver-fork-single-name` drops `keep` entirely and still fails before the
fix: `a` alone staying live across the call is enough, because the count the
callee reads is the caller's one reference either way. Reading the repro as
"aliasing" rather than "borrowing" points the fix at the bind ladder, where it
does not belong — the string-field row already retains at the bind
(`slot_is_reclaimable_struct`'s struct limb) and is correct on both sides, which
is what separates the two.

## The cost, measured

The retain fires on the pure rebind idiom too, because the frame still borrows
and nothing at the call site can say otherwise. The reuse guard then sees rc 2
and forks a box per call. `FERN_LEAKCHECK` allocs, x86-64:

| program | native | self-host before | self-host after |
| --- | --- | --- | --- |
| 200x `h = h.update(chunk)`, `std/crypto` Sha256 | 433 | 1319 | 1519 |
| 50x `h = h.upd(1)`, struct-only rebind | 2 | 2 | 52 |
| `own`-param source control | 2 | 2 | 2 |
| dying-local source control | 2 | 2 | 2 |

Live bytes are unchanged on every row (crypto: 108144 either side), so this is
churn and not a leak — one struct box per consuming call through a borrowed
parameter, freed at the caller's rebind. The two controls are flat because the
frame owns its value there and the retain does not fire.

**The recovery is native's owned-by-default rung, and it is an ABI change, not a
tweak.** Native retains at the CALL (`calleeParamOwnedByDefault` +
`needsRcIncOnAlias && !moveSites`), where the caller can see that `h` dies at
`h = h.update(c)` and skip it; the callee's exit sweep spends the count. Moving
the retain there needs the whole two-sided pair plus a call-graph fixpoint over
"which parameters are consumed on to an `own` position", since an intermediate
frame must NOT retain again. `consumed_params_of`'s header enumerates the four
missing pieces. Placed callee-side, no fixpoint is needed and the balance is
exact — the callee spends exactly the reference bought — which is why this
lands first.

The emitted-size corpus does not move at all: `scripts/perf-bench-selfhost` over
`examples/bench`, working tree before vs after, 0 of 84 keys changed on all
three targets. No benchmark there carries the shape.

## What this does NOT fix — #8874

Making the base's count honest is exactly what stops `is_unique` from covering
for the FIELD. `emit_own_field_arg`'s not-unique branch retains the field value
and hands it to the `own` position, which is a second owner rather than a
private buffer; the callee's `dst = dst.with(at, v)` on an `own` array parameter
is a bare `arr_set` chosen STATICALLY (`is_aliased_name || is_borrowed_name`,
neither true of an `own` param), so it rewrites the shared buffer. Native has no
such hole because its `.with` routes through `__fern_arr_cow_inplace`, which
reads the count. The self-host emits no cow helper at all.

That is #8874, it reproduces on `main` with no borrowed parameter anywhere
(`a = S { ...a, buf: put(a.buf, 0, 9) }` under a live `var keep = a`: 9 on
interp and both natives, 99 on all three self-host targets), and it is why the
array-field row here asserts on the SCALAR field. A row over `buf[0]` belongs
with that fix.

## Next

- #8874, above — the cow store is the convergent half.
- The owned-by-default rung, which turns the churn row back to 2 allocs. Its
  precondition is the fixpoint named above, not the retain itself.
