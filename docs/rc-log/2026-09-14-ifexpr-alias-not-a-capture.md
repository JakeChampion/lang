# An if-expression's mention of a local is not a capture

#9192 was filed from the `.with` probe set as "an array handed out of an
if-expression is credited as moved". The credit was not the cause.

## What the debug print said

`a = a.with(0, 9 + i)` after `var b: i32[] = if (a[0] == 1) { a } else { c }`
reaches the StmtAssign arm with the target spelled `$cell$$binding$0$a`. The
if-expression desugars to a zero-argument lambda (`ORIGIN_IF_EXPR`), the
capture-lift pass finds that lambda in operand position, and an operand
lambda is escaping by construction (`box_nested_lambdas` →
`box_apply_lambda(…, esc = true)`), so its capture of a local the outer body
reassigns is boxed into a one-element cell (#5394). From then on `a` is a
cell for the whole function: every `a = a.with(…)` is a value-producing
`.with` clone stored into the cell's element, the superseded element is
never released, and no count-reading path applies to a cell. 103 allocations
and 4,800 live bytes for a hundred updates was the cell, not a move credit.

Two things were wrong at once:

- The lambda is inlined (`lower_iife` lowers a value block into the
  enclosing frame and reads its slots directly), so it is no capture of
  anything. `box_nested_lambdas` now passes a value-block lambda through and
  collects only the lambdas INSIDE its body (`box_nested_lambdas_stmts`);
  a real closure nested in an if-expression branch is still found, and
  conservatively as escaping, which it was not before this because the
  collector never descended into a collected lambda.
- With `a` unboxed, the leaf `{ a }` handed `b` an UNCOUNTED copy of the
  pointer: `lower_value_tail` lowered `return a` as a bare load into the
  result temp, while the binding `b` is `is_arr` through its annotation and
  the exit sweep releases it. So the function released the one buffer twice
  at exit. The probe never saw it because `__rc_underflow_count()` is read
  before the sweep runs; from a caller's frame the parent compiler exits 99
  on `if-expression-alias-exit-sweep`. The leaf now takes the alias retain
  (`retain_tos`) for the read shapes the `var` ladder retains — a bare array
  local, a struct field of any array kind (through the ladder's own field
  classifiers, `scalar_arr_field_type` and its struct / enum / nested
  siblings), a tuple element, a nested-array element
  (`ifexpr_leaf_is_array_read`) — so `b` owns the reference its sweep
  releases and the source's next update finds the buffer shared and copies
  once. A fresh producer in a branch (a literal, a slice, a call) is a move
  and stays unretained. The container-read leaves were the reviewer's
  catch, twice: each exited 99 the same way with only the ident covered,
  and an `i32[]` / `boolean[]` / `u8[]` field still did when the field arm
  asked only the width classifiers, which spell string, i64 and f64 fields
  and nothing else. A third catch was the index arm: `box.grid[0]` over a
  nested-array FIELD, and `m[0][1]`, answered no under `expr_yields_array`,
  whose index reading accepts an ident receiver alone. Nested arrays are a
  leak-only class, so that one did not exit 99: the binding's sweep freed a
  row the struct still held, and a caller handed the struct read 7 for 1
  once three small allocations had recycled the row (exit 2). The index arm
  now also asks `index_read_is_arr`, the compiler's own indexed-array-read
  decision, which spells both shapes — and, after the reviewer's fourth
  catch, a tuple element holding a nested array (`t.1[0]`, exit 2 the same
  way), which that decision's field arm now reads through the element's tag
  (`field_arr_tag`), on the return path as well — and, one index deeper, the
  fifth: `t.1[0][1]` over a tuple holding `T[][][]` asked the doubly-indexed
  classifier, whose field arm read the struct-field type alone; it reads the
  same tag now.

## Measured

Self-host x86-64, `FERN_LEAKCHECK=1` at emit, interpreter as oracle:

| shape | before | after |
| --- | --- | --- |
| `var b = if (c) { a } else { d }`, 100 updates of `a` | 103 / 3, live 4,800 | 3 / 3 |
| `var b = if (c) { a } else { mk() }`, 100 updates | 102 / 2, live 4,800 | 2 / 2 |
| the bind alone, underflow read after the frame exits | exit 99 | exit 0 |
| the bind alone, underflow read inside the frame | 2 / 2, exit 0 | 2 / 2, exit 0 |

The last row is the trap: a census that balances and an in-frame underflow
read of zero were both compatible with the double release.

## Gates

Twelve rows added to `TestSelfHostWithCowIR{X86_64,Arm64,Wasm}`:
`if-expression-alias`, `if-expression-fresh-arm`,
`if-expression-alias-exit-sweep`, the field (`u64[]`, `i32[]`,
`boolean[]`) / index / tuple `-leaf-exit-sweep` rows, and the four
`-handback` rows (a nested-array field indexed, a doubly-indexed element, a
tuple element's nested array indexed once and twice) that pin the
recycled-row read. Against the parent commit's lowering the
first three fail (103 / 3, 102 / 2, exit 99) and the other thirty pass; the
leaf rows exit 99 with the ident-only guard, and the handback rows exit 2
with the ident-receiver index arm. Also green: the whole-compiler emit-all fixpoint (gen0 == gen1, 370 s)
— the lift change touches every function with an if-expression — and
`TestSelfHostCoreutilsParity/(od|printf)`. The probe set of the predecessor
entry is otherwise unchanged; #9191 (alias + append) and #9190 (Option
payload typing) stand.
