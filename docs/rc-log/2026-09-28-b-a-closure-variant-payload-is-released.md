# 2026-09-28 — a closure held in a variant payload is released (#9841)

AST lowering (`FERN_SEM_IR=`) only. The typed lowering was already balanced on
every shape here, and still is. Stacked on #10566, which gives every
closure-returning call's caller one count (`closure_value_is_counted`).

## The cause

`enum_field_rc_droppable` had no case for an `fn` payload. An enum with a
closure payload therefore failed `enum_all_variants_rc_droppable`, and every
release path keyed on that admission skipped the enum:

- the consuming-match free;
- the "RCE:" returned-enum registry;
- the loop-rebind release;
- the struct enum-field walk.

As a result, both the variant box and the closure leaked, however the payload
was built.

## What changed

- **Admission.** `enum_field_rc_droppable` admits `"fn"`.
  `emit_enum_variant_payload_drops` releases it with one rc-guarded dec of the
  env box, the same arm a scalar-array payload takes.
- **Construction.** `lower_variant_ctor_args` makes the variant hold a count on
  its closure, through `count_closure_payload`, for an admitted enum only:
  - a counted value (a lambda, a `__mkclo$` box, or a closure-returning call) brings its own count;
  - a closure local moved there hands over its sweep's count (`note_moved_elided`);
  - anything else is retained: a surviving local, a parameter, a top-level function.

  So the payload drop is balanced whatever the payload aliases. This is also
  what lets the move analysis (`rc_ml_ctor_arg_moves`, which now sees these
  enums) elide the retain safely.
- **Struct enum-field drops and enum arrays.** `enum_walk_payload_field` (a
  leak-safe array or `fn`) is what `enum_arr_elems_walk_ok` and
  `enum_walk_payload_offsets` read. The backends' `__enum_drop_` and
  `__enum_arr_elems_drop_` therefore release a closure payload of an enum held
  in a struct field or an array.
- **Arm bindings.** `payload_binding_escapes_arm` reads a direct call through a
  closure binding, `f(n)`, as a borrow. Any other mention escapes, a lambda
  capture included (`clo_binding_other_uses`). `moved_skip_applies` keeps the
  dec for a closure binding whose only other use is a bare `return f`, because
  #10566's return retains it. Any other escape skips the dec, which is safe:
  that path leaks, and nothing frees early. `param_match_binding_escapes`
  takes the same reading through `payload_type_at`, so a parameter matched and
  only called through stays borrowable, and its caller releases what it lent.

## Measured (`FERN_LEAKCHECK=1`, AST lowering, allocs / frees)

| program | before | after |
|---|---|---|
| `built`: lambda / local / call payloads, matched, bound, unused | 110 / 10 | 110 / 110 |
| `returned`: enums returned from functions, lent and matched | 93 / 50 | 93 / 93 |
| `struct_field`: enum in a struct field, loop rebind | 140 / 100 | 140 / 140 |
| `outlives`: the closure outlives the variant (guard) | 70 / 10 | 70 / 70 |
| #9841's probe | 29 / 1 | 29 / 2 |

x86-64 and wasm agree on every row, and arm64 matches in the test. The
`FERN_SANITIZE=1` x86-64 builds are silent. `TestSelfHostClosureVariantPayload{X86_64,Arm64,Wasm}`
holds the four programs to the interpreter's answer with a balanced census,
under both lowerings.

## Not covered

- #9841's own probe still leaks every chain it builds through `f(w)`. An rc
  enum returned by a call through a closure value has no fresh-producer
  verdict, whatever its payload: a string-payload twin reads 15 / 3. Filed as
  #10577.
- An enum array iterated by `for e in es` with a match on `e` is never
  released. This holds for an `i32[]` payload as well: 60 / 10. The same array
  with a closure payload, only read through `es.len()`, is 60 / 60. Filed as
  #10579.
- A closure binding captured by a lambda in its arm keeps the skip, so the
  closure leaks (30 / 20, from 30 / 10). A captured closure has no counted
  release in the capture model.
- An enum outside the admission, one with an `Option` payload for example,
  leaks as before. Its closure payload is not retained.
