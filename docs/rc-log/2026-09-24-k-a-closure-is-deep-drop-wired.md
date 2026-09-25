# A closure field is deep-drop wired

2026-09-24. Native. Follows `2026-09-24-j-…`. Fixes #10240.

## What changed

`typeDeepDropWired` rejected every `*ast.FuncType`, so a struct or tuple
carrying a closure took the leak-mode model: its params were never
owned-by-default or promoted, and a replaced box was released with a flat dec
that frees nothing. `__drop_struct_*` and `__drop_tuple_*` release a closure
member through `__drop_closure_value`, which the IR emits for every backend.

A closure now counts as wired as a struct field or tuple element
(`memberDeepDropWired`), and nowhere else:

- A bare closure param stays borrowed.
- An array of closures stays unwired: an array param's borrow-tainted overwrite
  and its `.with` self-reassign release the buffer alone.
- An enum with a closure payload stays unwired: `genEnumDropFn`'s variant plan
  skips a closure payload.

Admitting those two shapes would have moved them from main's deep release to a
flat one.

The exit sweep's inline tuple drop had the same gap on its own. It released
each element through `dropStructField`, which has no closure arm, so a tuple
local holding a capturing closure stranded the pair and its environment: 64
bytes on x86-64. That leg now releases a closure element through
`__drop_closure_value`, as the generated tuple drop does.

## Measured

Rc-corpus cases, each on x86-64, arm64 and wasm, each reclaiming whole under
the leak gate:

- `closure_tuple_local_reclaimed`: a tuple local holding a capturing closure.
  Main leaks 64 bytes (allocs 3, frees 1).
- `closure_tuple_param_threaded_by_reassignment`: the same tuple threaded
  through a reassigned param. Main leaks the replaced box and the closure.
- `closure_field_struct_param_threaded_by_reassignment` now holds a capturing
  lambda, so the closure cell's release is on the measured path.

Two other results:

- A `check_func`-shaped program threading a struct with a closure field leaks
  42,400 bytes on main. With the change it leaks the 1,600 bytes the same
  program leaks with an `i32` in place of the closure, a separate path.
- The self-hosted compiler with a function-typed field on `asmcore.EmitState`,
  built natively, on `checker.fern`: 942 MB peak and 16.8 s, the same as
  main's compiler, where the leak-mode model peaked at 1,000 MB. Its output is
  byte-identical.
