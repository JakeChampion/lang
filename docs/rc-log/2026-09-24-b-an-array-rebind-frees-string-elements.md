# An array rebind frees the old buffer when its elements hold strings

2026-09-24. Native.

```
enum N { C(i32), S(N[]), W(N) }
enum I { IC(i32), IS(string) }
function emit(prog: I[], n: N): I[] {
    match (n) {
        C(c) => { return prog.append(IC(c)); },
        S(xs) => { ... },
        W(inner) => {
            var pg: I[] = prog.append(IC(0));
            pg = emit(pg, inner);
            return pg;
        },
    }
}
```

`emit(start, W(S([C(1), C(2)])))` leaked two blocks on every native
target. std/regex's program emitter `__rx_emit` has this shape. A
`regex_captures("^(a+)", ...)` call leaked four blocks there.

## Cause

`pg` is not freeEligible, because it starts as an append to a parameter.
The rebind `pg = emit(pg, inner)` therefore reaches the array branch
through `selfReassignOwnedLocal` instead. For arrays that predicate
used `typeSelfDropSafeNoStrings`, which refuses any element type that
reaches a string. `IS(string)` is enough to refuse `I`. The rebind then
fell to the catch-all flat `__fern_rc_dec`, which never frees.

The exclusion's comment said the array overwrite "has no identity
guard". That was no longer true. The array branch now compares the old
pointer with the new one. When they match, the callee handed its
argument back, and only the shallow `__fern_arr_dec` runs. The deep
drop runs only when the buffer changed hands, and then the old buffer
still owns the element counts the copy took.

## Change

Arrays use `typeSelfDropSafe`, as structs and enums already did, and
`typeSelfDropSafeNoStrings` is deleted.

## Measured

- The shape above, on x86-64 and arm64: 9 of 11 blocks freed before,
  11 of 11 after. The wasm leak check fails before and balances after.
- A five-round loop whose callee returns its argument unchanged from
  one of its arms, which is the case the exclusion was protecting:
  65 of 100 freed before, 100 of 100 after, with the interpreter's
  result and no sanitizer finding.
- `TestArrayRebindWithStringElementsFreesTheOldBuffer` pins both shapes
  on all three targets. All six legs fail without the change.
- `TestConformanceLeakCensusX86_64`: every other row unchanged, and the
  total goes from 6549 to 4936 unpaired.

  | fixture | before | after |
  |---|---|---|
  | `regex_captures_assert` | 5220 | 3741 |
  | `regex_named_groups` | 124 | 79 |
  | `regex_captures` | 109 | 68 |
  | `regex_captures_all` | 104 | 69 |
  | `regex_replace_groups` | 77 | 64 |

## Next lead

Most of what the regex rows still leak is `__rx_addthread`'s inline
`caps.with(slot, ti)` argument. The argument is a fresh copy, but the
call returns a struct, and the direct-call loop reclaims an argument
temp only when the result cannot alias it (#10168).
