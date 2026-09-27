# A returned enum parameter is retained

2026-09-27. Touches `fresh_enum_fwd_fixpoint`, `ret_enum_param_handback`, `param_counted_of`'s
enum and struct tiers, `structfld_reclaim_ok_types_of`, `hoist_call_scrutinees`,
`fresh_enum_call_arg_type`, the discarded-call arm and the `enum-rcpayload:`
precise drop. Fixes #10443 and #10410.
Refs #10365, #10447, #4451.

## The wrong answer

On the AST lowering this answered 93 where 3 is right:

```fern
function hb_sc_param(e: Sc): Sc { return e; }
function mk(r: i32): Sc {
    var a0: Sc = SA(k_of(r));
    var a: Sc = hb_sc_param(a0);
    return a;
}
```

Like native, the plan does not taint a plain call argument, so it read
`hb_sc_param(a0)` as an owned result. It credited `a0`'s release, but `a` was
`a0`'s own box, handed back with no count. `mk`'s exit sweep freed the box under
the returned result. Native retains a returned borrowed parameter, and the AST
lowering did not.

## The contract, in three parts

1. **Temporaries release `ENUM:` results.** Three positions changed:
   - Argument. An rc-payload member result is stashed under the gate `RCE:`
     already uses: a counted position, or a callee that returns `i32`.
   - Match scrutinee. `hoist_call_scrutinees` turns the call into a binding,
     which then takes the `ECALL:` credit.
   - Discarded call. The result gets the rc-gated enum drop.

   Before this, only a scalar-payload argument was released, plus an `RCE:`
   callee's argument and rc scrutinee.
2. **The registry takes handbacks.** `return p` (a non-own parameter of the
   return type) and `return p.f` (an enum field of a non-own struct parameter)
   now make a function a member, provided `p` is never reassigned or redeclared.
   `ret_enum_param_handback` retains the value at that return, and
   `handback_params` no longer lists a member's positions.
3. **A lend there is a counted store.** For a member, the `ECNT:` and `PCNT:`
   tiers credit the retained return. The escape walkers already read the merged
   `CNT:` key, so the lender keeps its own release, and a fresh temporary at that
   position is stashed and released after the call. The structfld admission scan
   counts a member's `return p.f` as a counted share, like the `var` bind's dup.
   Without that, `HS` lost its `__field_reclaim_HS` field arm, and the loop
   rebind stranded the `SA` box of `hs.e`.

Part 3 has a consequence for releases. A local lent at a counted position may now
be shared with the call's result when it is released, so the last-use drop of a
fresh rc-payload enum (`precise_drop_names`' `enum-rcpayload:` kind) gates its
payload walk on `__fern_rc_is_unique`. Ungated, `var s = A([..]); var v =
passthru(s);` freed `s`'s array under `v`, and the underflow detector reported
it (`param_handback_counted`, formerly the `non_registered_producer_refused`
negative control, now 200/200).

Parts 2 and 3 depend on part 1. The retain alone turns every temporary-position
use of a handback result into a count that nobody gives back.

## Measured

x86-64, 100 rounds, AST lowering, allocs/frees. Answers are
interpreter-confirmed and unchanged unless shown.

| program | before | after |
|---|---|---|
| #10443 (`enumHandbackReturnSrc`) | 93, 200/200 | 3, 200/200 |
| #10410's pinned program | 1100/801 | 1100/1100 |
| `enumCallHandbackSrc` (params, fields, forwarding, each temporary position) | 1800/1001 | 1800/1800 |
| `enumCallTempSrc` (ctor and local members in each temporary position) | 2100/900 | 2100/2100 |
| `enumCallResultSrc` | 3150/3150 | 3150/3150 |

The sanitizer is silent on all four test programs, under all four lowerings.

## Still open (#10447)

These shapes read the same before and after: an enum method receiver
(`mk(r).val()`, 300/0), an array-literal element of a handback result, a
self-rebind `x = hb(x)`, and a `return e` from an arm that binds an rc payload
of `e`. A struct-literal temporary with an enum field, passed at a borrowable
position (`rd(HS { e: SA(..), n: 1 })`, 400/200), also leaks the same on main.
