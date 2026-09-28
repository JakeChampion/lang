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

Anywhere else, k and its aliases are read only as a borrow. The registry is a
greatest fixpoint, so a recursive threader holds by induction on the calls
that return.

Caller side:

- The three credits' gates admit `name = g(.., name, ..)` through a threader
  (`thread_rebind`, `strarr_thread_rebind`).
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
| `shared_fields` (appended elements' strings come from existing ones) | 11 / 4 | 11 / 11 |

`callee_keeps` (the callee stores the array in a struct) and `caller_keeps`
(the caller keeps the superseded array) are refused, and their counts do not
move. All rows are clean under `FERN_SANITIZE=1`, and the mixed legs
(`FERN_SEM_IR_SKIP=main` / `=walk`) leak without faulting.
`TestSelfHostThreadParam{X86_64,Arm64,Wasm,MixedX86_64}`.

## Not covered

- Threading through a function value: `fold(xs, acc, at_node)`, the #10357
  rows `struct_elem_walk` / `struct_elem_scope`, keep their pins.
- A threader the semantic lowering produced, for an AST caller.
- #10560, found here: an `append` result used as another append's receiver,
  or passed as a call argument, leaks its buffer on the AST lowering even for
  `i32[]`. `rec(n - 1, acc.append(x))` threads correctly, but it leaks every
  buffer, so the recursive row binds the grown array to a local first.
