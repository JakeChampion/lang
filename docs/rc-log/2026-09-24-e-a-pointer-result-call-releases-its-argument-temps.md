# A call with a pointer result releases its argument temps

2026-09-24. Native. #10168.

```
function add(st: RAdd, prog: RInst[], pc: i32, caps: i32[], ti: i32): RAdd {
    ...
    ISave(slot) => { return add(RAdd { ... }, prog, pc + 1, caps.with(slot, ti), ti); },
    _ => { return RAdd { ..., threads: ths.append(RThread { pc: pc, caps: caps }) }; }
}
```

The inline `caps.with(slot, ti)` leaked on every call. std/regex's
`__rx_addthread` has this shape. The same leak hit a struct literal passed
through a function value whose result is a pointer:

```
var f: (C) => Result[C, string] = bump;
match (f(C { value: 3 })) { ... }
```

## Cause

The call-level admission reclaims an argument temp only when the result is
a scalar. For a pointer result, the per-position admission `countedArgTemp`
asks `paramCountedRetain`, which says the callee holds that parameter only
through counted stores. Three things were missing:

- `arrayParamCounted` refused a parameter used as a `.with` receiver. It
  assumed `__fern_arr_cow_inplace` could hand the receiver back in place.
  For a parameter it cannot. An array is never owned by default, and an
  `own` parameter is not summarised. So the receiver is borrowed, and
  `computeArraySetIncs` retains it before the helper, which forces the copy.
  That left `caps` uncredited in `add`.
- `stashOwnedArgTemp` had no arm for a `.with` argument. A `.with` whose
  receiver took the forced-copy retain is a fresh rc 1 buffer that only the
  argument holds.
- A call through a function value had no per-position admission. No name
  says which function runs.

## Change

- `arrayParamCounted` credits a `.with` receiver.
  `TestArrayParamSetReceiverIsCounted` replaces the two tests that pinned
  the refusal. It also checks that the lowering retains the receiver before
  `cow_inplace`, which is what the credit rests on.
- `withCopyTempType` admits a `.with` argument under `arraySetInc`, next to
  `appendCopyTempType`.
- `indirectArgCounted(ai)` states the fact for every function an indirect
  call can reach. Each address-taken function, lifted lambdas included,
  must satisfy one of: counted at position `ai`, never returns a parameter,
  or has a scalar result. A lifted lambda's env is a trailing parameter, so
  positions line up. A builtin taken as a value answers false.
  `countedArgTemp` asks it for a function-typed local, and
  `emitIndirectCallArgs` asks it for every other function-value callee.

## Measured

x86-64 `-sanitize`, blocks freed before → after. Every case matches the
interpreter.

| shape | before | after |
|---|---|---|
| `add` above | 14 of 15 | 15 of 15 |
| `hold(caps.with(2, i))` into a struct, 3 calls | 4 of 7 | 7 of 7 |
| `set0([4, 5, 6])` returning `xs.with(0, 9)`, 3 calls | 4 of 7 | 7 of 7 |
| struct literal through a local, `Result` scrutinee | 2 of 3 | 3 of 3 |
| the same through a struct field, 4 calls | 9 of 13 | 13 of 13 |
| struct literal to `wrap` and a lambda through locals, 3 calls | 6 of 12 | 12 of 12 |
| struct literal to `id` and `wrap` through locals, 3 calls | 3 of 9 | 9 of 9 |

`TestPointerResultCallReleasesItsArgumentTemps` pins all seven on x86-64,
arm64 and wasm. All 21 legs fail without the change. Disabling one part at
a time on x86-64:

- without the receiver credit, `add` and `set0` fail;
- without `withCopyTempType`, `add` and `hold` fail;
- without `indirectArgCounted`, the four function-value rows fail.
`TestPointerResultCallKeepsATempAnIdentityCalleeReturns` checks that an
array identity reached through a function value still reads its argument.

Conformance census, 4535 → 320 unpaired:

| fixture | before | after |
|---|---|---|
| `regex_captures_assert` | 3741 | 0 |
| `closure_field_match` | 200 | 0 |
| `regex_named_groups` | 79 | 7 |
| `regex_captures_all` | 69 | 0 |
| `regex_captures` | 68 | 0 |
| `regex_replace_groups` | 64 | 0 |
| `try_op_in_closure` | 1 | 0 |

The fernsmith seeds 1392, 1596 and 1836, which segfaulted when the scalar
gate was once widened, run clean under the sanitizer and match the
interpreter.
