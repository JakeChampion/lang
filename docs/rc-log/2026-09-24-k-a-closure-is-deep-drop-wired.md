# A closure is deep-drop wired

2026-09-24. Native. Follows `2026-09-24-j-…`.

## What changed

`typeDeepDropWired` rejected `*ast.FuncType`, so a struct or tuple carrying a
closure was on the leak-mode model. Its params were never owned-by-default or
promoted, and a replaced box was released with a flat dec that frees nothing.
The generated `__drop_struct_*` / `__drop_tuple_*` already release a closure
field through `__drop_closure_value`, which is emitted in the IR for every
backend, so the exclusion no longer described anything. It is admitted now.
A bare closure param still stays borrowed: `ownedByDefaultShape` does not
admit a `FuncType`.

## Measured

- `closure_tuple_param_threaded_by_reassignment`, a new rc-corpus case: main
  leaks the replaced 32-byte tuple box (allocs 2, frees 1). With the change it
  reclaims whole, on x86-64, arm64 and wasm.
- `closure_field_struct_param_threaded_by_reassignment` now carries a
  capturing lambda, so the closure cell's release is on the path the leak gate
  measures.
- A `check_func`-shaped program threading a struct with a closure field: main
  leaks 42,400 bytes. With the change it leaks the 1,600 bytes the same
  program leaks with an `i32` in place of the closure. That remaining leak is
  a separate path.
- The self-hosted compiler with a function-typed field on `asmcore.EmitState`,
  built natively, on `checker.fern`: 942 MB peak and 16.8 s, the same as
  main's compiler, where the leak-mode model peaked at 1,000 MB. Its output is
  byte-identical.
