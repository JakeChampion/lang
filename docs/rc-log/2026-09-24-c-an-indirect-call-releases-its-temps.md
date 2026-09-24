# A call through any function value releases its temps

2026-09-24. Native.

```
struct C { value: i32 }
struct S { name: string, run: (C) => i32 }
function val(c: C): i32 { return c.value; }
...
    var st: S = S { name: "s", run: val };
    acc = acc + st.run(C { value: i });
```

Each call leaked the `C { value: i }` argument. The same call through a
function-typed local, `var f = val; f(C { value: i })`, released it.

## Cause

The call lowering has six ways to reach a function value: a named local, a
capture, a struct or tuple field, a call's result, an array element, and a
closure literal. Only the named local and the closure literal went through
`emitIndirectCallArgs`, which stashes each fresh argument temp and releases it
after the call. The other four pushed their arguments with a plain `b.expr`.

`ownedCallResultType`, which lets a `match` or a discarded statement release a
call's result box, had the same gap. It admitted an indirect call only when the
callee was a named local. So `match (r.run(i)) { ... }` over a field returning
`Result` left the box behind too.

## Change

- `indirectCalleeFuncType` answers "is this callee a function value, and of
  what type" for the five non-local forms. The call lowering and
  `ownedCallResultType` both ask it.
- Every one of those forms lowers through `emitIndirectCall`, which is
  `emitIndirectCallArgs`, the callee, `OpCallIndirect`, then the argument temp
  drops.
- `ownedCallResultType` gives an indirect call's result to the caller whenever
  `indirectCallsReturnOwnBox` holds, as it already did for a named local. That
  fact is about every address-taken function in the program, so it does not
  depend on how the callee was spelled. The result type comes from the
  callee's signature, since `exprType` does not resolve a call through a field.

## Measured

Four calls each, x86-64 `-sanitize`, blocks freed before → after. Every case
matches the interpreter.

| callee | before | after |
|---|---|---|
| struct field, struct-literal argument | 1 of 5 | 5 of 5 |
| struct field, `Result` scrutinee | 1 of 9 | 9 of 9 |
| array element | 1 of 5 | 5 of 5 |
| a call's result | 0 of 4 | 4 of 4 |
| a capture inside a lambda | 4 of 8 | 8 of 8 |

`TestIndirectCallThroughAnyCalleeReleasesItsTemps` pins all five on x86-64,
arm64 and wasm. All 15 legs fail without the change.

`closure_field_match` in the conformance census: 550 unpaired → 200.

## Next lead

The 200 left are the argument temps of calls whose result is a pointer.
`emitIndirectCallArgs` reclaims only under `resultCannotAliasArg`, the same
restriction the direct-call loop has, and a named local fails it the same way
(#10168).
