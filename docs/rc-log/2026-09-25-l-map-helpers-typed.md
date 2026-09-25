# The map helpers take the typed path

`__fern_map_find`, `__fern_map_delete` and `__fern_map_delete_rel` were the last
helper sources outside the fs bundle that the checker refused. With them, 112
of the 114 helper sources check alone. The other two, `read_file` and
`open_with`, call helpers elsewhere in the fs bundle and check inside it.

## What they needed

- **A call through a code address.** An op site hands the helpers bare code
  addresses: the key column's eq function (`eqfn`) and the columns' release
  functions (`krel`, `vrel`). The AST source called them as `eqfn(k, key)` on
  an `i32` local. A Fern function value is a closure, so the typed spelling is
  a raw-floor call:
  - `__raw_call1(a, f)` and `__raw_call2(a, b, f)` lower to `call_indirect`.
  - The address is last because it is pushed last.
  - The result is the low half of the result word, `i32`, as the AST typer
    already had it.
- **The runtime names on raw words.** These calls are typed on `usize` words,
  as the helpers hold keys and values. They are rows of the raw-floor table,
  like the calls themselves, so semsource contracts them from it and the
  native-parity diff does not expect native to declare them:
  - `__fern_str_eq` answers `boolean`;
  - `__fern_str_free`, `__fern_str_arr_free` and `__fern_rc_dec` answer
    nothing;
  - `__fern_map_find` answers `i32` and `__fern_map_delete` answers `usize`.

  `map_delete_rel` releases a keyed column's box with `__fern_rc_dec(k)`.
  Before, it called `__fern_arr_dec(k)`, whose one-argument form irlower
  already lowered to that same call.
- **Argument order into a helper.** A runtime helper binds its parameters
  in reverse push order (#5666). The backends push a helper call's operands
  as they come, where a user call's go last first, so irlower pushes
  `__fern_map_find`'s arguments in reverse. ssarc's generic call path pushed
  them in order, and `map_delete`'s typed body crashed every delete on x86-64
  and arm64. ssarc now reverses them for that call.
- **A callee in the same module.** `map_delete_rel`'s typed body calls
  `__fern_map_delete`, and the prune in `runtime_bodies` keeps a body only
  when every call it makes has a body in that lowering. So
  `rt_src_map_delete_rel` now carries `map_delete`'s function too. A program
  that needs both emits that source alone, on x86-64 and arm64.

## Tests

`map-delete-releases-the-entry` in `TestSelfHostSemanticProduction` now
reports all three helpers produced, and still holds its absolute leak pin. It
covers string keys, a string value, and a keyed column of boxes, which goes
through `eqfn` for the search and `vrel` for the release.
`TestSelfHostMapDeleteRelease*` and `TestSelfHostMapIterationOrder*` cover
the same helpers under each backend's own test.
