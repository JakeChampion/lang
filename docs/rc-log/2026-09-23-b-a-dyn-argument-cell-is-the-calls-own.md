# A dyn argument's cell is the call's own

2026-09-23. Native, x86-64. A value coerced to `dyn Trait` at a call argument
is boxed into a fresh 16-byte `{data, vtable}` cell (`OpBoxDyn`), and the cell
was released only when the concrete was itself a fresh temporary. A bare
local, a value from outside the loop, a nullary variant, an integer or a
string passed to a `dyn` parameter leaked the cell on every call. wasm, whose
dyn value is inline, leaked the primitive's value cell the same way. Closes
#10054.

`stashOwnedArgTemp` now stashes every coerced argument as the dyn it is and
releases it through `__drop_dyn_<set>` after the call. A concrete the call
does not own is retained for the dyn first (`emitDynConcreteInc`, the retain a
coercion into a local takes), so the release balances; a fresh one hands the
dyn its only unit, as before (#10053). arm64 still does not reclaim dyn values
(DYN-TRAITS.md §4.4 slice 4c) and stashes nothing.

A callee whose result is itself a dyn value releases the argument too: 20
trips over a local, an outer record, a fresh record and an integer passed to
one read 1280 bytes live before and 0 after. What stays broken is a callee
that returns its dyn parameter: it hands the caller the very cell the caller
built for the argument, a use-after-free on main before this change (#10072).

## Measured

x86-64 `-sanitize`, 20 trips passing a nullary variant, a local and an outer
record, a record held by an enum, an integer and two strings to a `dyn`
parameter: 3200 bytes live at exit before, 0 after; the answer is 52 on
x86-64, arm64 and wasm both ways.

`TestDynCoercedNonFreshArgBounded` grows with the churn length on x86-64 and
wasm without the change; `TestDynCoercedNonFreshArgReleasedAsDyn` pins the
answers on all three backends, which is what a wrong retain would break.
