# A local bound from a call through any function value owns the result

2026-09-24. Native. #10149.

```
function run_with(st: Stage, t: Txn): i32 {
    var o: Out = st.run(st.name, t);
    return o.v;
}
```

The `Out` box leaked on every call. Calling `step(st.name, t)` directly
freed it.

## Cause

`rhsTainted` marks a local ineligible for release when any argument of the
call it is bound from is borrowed, and `st.name` is. Two carve-outs say a
call hands back a box of its own whatever its arguments are:
`returnsFreshBox` for a named function, and `indirectCallsReturnOwnBox` for
a call through a function value. The second applied only when the callee
was a named local. A struct field, an array element or a call's result as
the callee fell through to the argument rule.

## Change

`rhsTainted` applies the `indirectCallsReturnOwnBox` carve-out to every
callee `indirectCalleeFuncType` recognises, as `ownedCallResultType`
already does.

## Measured

x86-64 `-sanitize`, blocks freed before → after. Both match the
interpreter.

| shape | before | after |
|---|---|---|
| #10149's program, through a struct field | 2 of 3 | 3 of 3 |
| through an array element and through a call's result, 3 calls each | 1 of 7 | 7 of 7 |

`TestIndirectCallBoundResultIsReleased` pins both on x86-64, arm64 and
wasm. All six legs fail without the change.

No conformance fixture binds such a result, so the census does not move.
Its note on `field_read_off_a_call_through_a_function_field` is gone: that
row reached 0 when every function-value callee released its result box.
