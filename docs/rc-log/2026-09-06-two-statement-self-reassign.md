# 2026-09-06 — the self-reassign shape, spelled as two statements

`x = f(…, x, …)` is the shape #4838's accumulator chains ride: the store
supersedes x, nothing can read the old buffer back through it, so the argument
dies at the call and #4873's containment bracket is skipped. Write the same
thing with the result named first and it was quadratic:

| loop body | before | after |
|---|---|---|
| `c = emit1(c, op);` | 1 ms | 0 ms |
| `var c2 = emit1(c, op); c = c2;` | 922 ms | 0 ms |
| `let (c2, p) = emit2(c, op); c = c2;` | 920 ms | 0 ms |

20000 iterations, `-O`, x86-64-linux, one struct with an `It[]` field.

## The tuple was a red herring

#8498's workaround paragraph reads the tuple form as "the tuple keeps a second
reference alive". It does not. The identical `emit2` reached through a wrapper
whose parameter is at its last occurrence is linear:

```
function step(c: C, i: i32): C {
  let (c2, p) = emit2(c, It { op: i, a: 0, b: 0 });
  return c2;
}
while (i < n) { c = step(c, i); i = i + 1; }        // 1 ms
```

Same tuple, same second element, same `emit2`. What differs is which shape in
`callArgDeaths` reaches the call: inside `step` it is the last-occurrence one,
and at the direct spelling it was none of them. The `var` form measures the
same 920 ms with no tuple anywhere, which is the plainer statement of it.

## The rule

Statement *i* binds from a call (`var y = f(…)` or `let (a, b) = f(…)`) and
statement *i+1* is `x = <value>` where the value does not read x. Then x dies
at that call, on the same argument as the one-statement form: the store runs
before any other statement, so no later read — including the next iteration of
an enclosing loop — can reach the buffer the callee grew.

The store's value must not read x, which is what refuses `var y = f(x); x =
g(x)`; and the two statements must be adjacent, which is what refuses `let (a,
b) = f(x); var n = x.insts.len(); x = a;`. Both are pinned.

Neither half matched before: the binding statement stores to a NEW name, so it
is not the `*ast.Assign` the one-statement shape looks for, and `c = c2` names
no call. Inside a loop the last-occurrence shapes are out too (`repeating`), so
nothing marked the argument dead at all.

## in_loop moved, and the loop gate did not

`internal/ir/call_arg_death_last_use_test.go`'s `in_loop` case is exactly this
shape — `var a = s.emit(i); s = a.emit(i + 1);` in a while body — and it was
pinned at NO deaths, on the argument that inside a loop one textual read is
many dynamic ones. That argument is about the last-occurrence shapes and does
not reach this one: the store at the end of the body means the next iteration
reads the value this one produced, never the grown buffer. The expectation is
now `s`, and the value semantics agree with the interpreter oracle on both
native backends (`loop-rebind-two-statement` in
`internal/e2e/append_borrowed_param_test.go`, 22 not 23).

The loop gate itself keeps its own case: `in_loop_live` reads `s` in a loop and
never stores it back, and still yields nothing.

## Witnessed

- `internal/ir/call_arg_death_last_use_test.go` — `des_rebind`, `var_rebind`
  admitted; `des_reads_after`, `des_gap`, `in_loop_live` refused.
- `internal/e2e/append_borrowed_param_test.go` — `destructure-rebind` and
  `loop-rebind-two-statement`, both backends. Each reads 23 with
  `computeGrowParams`' death propagation removed, so the containment leg is
  witnessed and not merely argued.
