# A threaded accumulator keeps its element walk

On the AST lowering (`FERN_SEM_IR=`), a pointer-element array rebound through
a call that hands its argument back, or the argument grown, never freed its
elements (#10420):

```
function walk(n: i32, acc: Inst[]): Inst[] {
    if (n % 4 == 0) { return acc.append(Inst { name: "w" + "", depth: n }); }
    return acc;
}
... while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
```

## Cause

The credits that walk a struct array's or a `string[]`'s elements at exit
(STRUCTARR / ARRSTRUCT / SARR) admitted a rebind only from a self-append or
from a whole-program fresh producer. `walk` is neither, because its result
shares `acc`'s elements. `pending` therefore took the shallow buffer dec at
exit.

## Change

A whole-program registry of threaders. `thread_rows_of` registers
`THREAD<k>:<name>` in the `return_fresh_struct_ret_fns` list for a free
function whose return type is parameter k's `string[]` or struct-array type
and whose every return is one of these:

- `k`;
- `k.append(e)`, where `e` is a fresh string, a fresh struct literal, or a
  strict-fresh struct producer's result;
- a local alias of k rebuilt only in those ways;
- a call to another threader with such a value at its threaded position.

Anywhere else, k, its aliases and their elements are read only as a borrow,
judged by the element gate of k's class (`strarr_expr_unsafe` for
`string[]`; `arrstruct_elem_esc_expr` and `arrstruct_row_escapes` for a struct
array). The caller releases every element at exit, so an element the body
binds, stores, appends elsewhere, keeps past a loop iteration or extracts a
field from refuses the threader, even where that store is counted today. A
loop variable or match binding that rebinds a threaded name refuses it too, as
does a producer call through a name a local or parameter shadows. The registry
is a greatest fixpoint, so a recursive threader holds by induction on the calls
that return.

Caller side:

- The three credits' gates admit `name = g(.., name, ..)` through a threader
  (`thread_rebind`) when no other argument fails the class's element gate.
- An empty seed rebound that way is an append-built candidate ("A|").
- The ARRSTRUCT element-payload walk reads the threaded argument as handed
  back rather than escaping.
- The rebind store stays shallow (`arr_rebind_frees_elems`). The result
  carries the old elements uncounted, so the deep release the producer
  rebind takes would free them under the new array.

`ssarc.caller_rows` drops every threader row once the semantic lowering
produces a threader. The other rows were proved through that threader's
syntax, which the AST no longer lowers.

## Measured (allocs / frees, AST lowering, x86-64; wasm matches)

| shape | before | after |
|---|---|---|
| the issue's `Inst[]` | 4 / 1 | 4 / 4 |
| the issue's `string[]` (`n.to_string()`) | 7 / 4 | 7 / 7 |
| `grow` (40 appends, outgrows the buffer) | 45 / 5 | 45 / 45 |
| `alias_recursive` (alias + recursion + literal seed) | 52 / 31 | 52 / 52 |
| `string_arr_alias` | 121 / 81 | 121 / 121 |
| `array_field_elem` (`Node { kids: i32[] }`) | 14 / 2 | 14 / 14 |
| `shared_fields` (a caller's string, an existing element's scalar) | 7 / 4 | 7 / 7 |

Refused rows keep the shallow fallback, and their AST census is pinned to it.
Each pin is the row's census on main before this change:

| shape | allocs / frees |
|---|---|
| `callee_keeps` (the callee stores the array in a struct) | 42 / 0 |
| `caller_keeps` (the caller keeps the superseded array) | 8 / 5 |
| `elem_struct_field` (`Holder { x: acc[0] }`) | 26 / 14 |
| `elem_holder` (`hold(acc[0])` builds a holder) | 26 / 14 |
| `elem_other_array` (`ys = ys.append(acc[0])`) | 27 / 15 |
| `elem_loop_var` (`for p in acc { ys = ys.append(p); }`) | 37 / 25 |
| `elem_field_payload` (`Inst { name: acc[0].name, .. }` appended) | 25 / 13 |
| `elem_returned` (`acc.append(acc[acc.len() - 1])`) | 14 / 13 |
| `string_elem_struct_field` (`Hs { s: acc[0] }`) | 25 / 14 |
| `string_elem_other_array` (`ys = ys.append(acc[0])`) | 51 / 39 |
| `loop_var_shadows` (`for acc in qs { .. return acc; }`) | 5 / 3 |

Before the element gate, the first six element rows threaded and balanced:
every store they make is counted on this tree. The gate refuses them anyway,
because the credit's soundness would otherwise rest on that. `producer_shadowed`
(a parameter named like a strict-fresh producer) and `loop_var_shadows` were
already refused by another gate, so neither tells the two shadow checks apart;
`loop_var_shadows` pins its refused census, while `producer_shadowed` only has
to balance.

A value block between the threader and the handout does not get past the
element gate: `elem_value_block` (`var h: Holder = { Holder { x: acc[0] } }`)
is refused at 36 / 24, the same 12 live blocks as its flat twin
`elem_struct_field`, while `value_block_read` (`var d: i32 = { acc[0].depth }`)
threads and balances. `string_elem_loop_var` (a `string[]` loop variable
appended to another array) is refused at 61 / 49. All rows are clean under `FERN_SANITIZE=1`, and the
mixed legs (`FERN_SEM_IR_SKIP=main` / `=walk`) leak without faulting.
`TestSelfHostThreadParam{X86_64,Arm64,Wasm,MixedX86_64}`.

## Not covered

- Threading through a function value: `fold(xs, acc, at_node)`, the #10357
  rows `struct_elem_walk` / `struct_elem_scope`, keep their pins.
- A threader the semantic lowering produced, for an AST caller.
- #10560, found here: an `append` result used as another append's receiver,
  or passed as a call argument, leaks its buffer on the AST lowering even for
  `i32[]`. `rec(n - 1, acc.append(x))` threads correctly, but it leaks every
  buffer, so the recursive row binds the grown array to a local first.
