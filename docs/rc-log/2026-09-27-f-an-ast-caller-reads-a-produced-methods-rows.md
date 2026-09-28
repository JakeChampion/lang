# An AST caller reads a produced method's rows

`ssarc.caller_rows` rewrites a semantic-lowered callee's rows for its
AST-lowered callers. For a method, `semlower` passed it the bare declaration
name, but the AST looks a method up as `<Base>.<name>` (#10515). The fix keys
the rows correctly, and the key change surfaced two further gaps, which are
fixed here too.

## Cause

Under the bare name, a produced method's rows went to the wrong place:

- The method's syntax rows under `<Base>.<name>` were never erased. So an AST
  caller still read what the AST inferred from a body it no longer lowered.
- The contract rows (`ARROWN:`, `ENUM:`, the tuple rows, the struct row,
  `borrowable_params`, `param_counted`) were written under `<name>`, where
  only a free function of that name reads them. Their parameter flags also
  counted the receiver, which is parameter 0 of the `ssasem.Func` but has no
  position in the AST's per-parameter rows (`borrowable_params_of`,
  `param_counted_entries_of` walk `fn.params`, which excludes it). A
  same-named free function therefore read flags shifted by one position.

## Change

- `semlower` passes `call_key` (`<Base>.<name>` for a method), the key the
  grow-mask seeds already used. `caller_rows` starts the parameter tiers and
  the bare flags at position 1 for a method. The receiver's own verdict stays
  with `recv_borrow_fns`, which this boundary does not write.
- The `method` guard on the struct row, added by #10415, is gone.
  `method_name_collision` still passes on every lowering, because the method's
  verdict now lands under `Ints.mk`.
- Erasing the method's syntax rows exposed two readers that only its
  counted-class row (`cnt_struct_ret_fns`) had been reaching:
  - The read-through `m(..).arrfield` release is admitted by the counted class
    alone (`fsself`). Free functions already sat at that floor: #10415's
    erase dropped the class and wrote nothing back. A record that
    `built_result` proves every return constructed is the class's
    strict-fresh member with no handback position. So `caller_rows` now
    writes `CNTRET:<key>` and the `cnt_struct_ret_fns` row for it, as the
    syntax does for a strict-fresh producer.
  - `discard_method_call` had no strict-fresh arm, so a discarded method call
    took the counted class's shallow dec and stranded the record's fields. It
    now shares `discard_fresh_struct` with `discard_free_call`. This also
    fixes a pure-AST leak.

## Measured (allocs / frees; x86-64 leakcheck and wasm agree on counts)

| row (`TestSelfHostMixedStructRelease`) | lowering | before | after |
|---|---|---|---|
| `method_array_result` | `ast_main` | 11 / 5 | 11 / 11 |
| `method_record_results` | `ast_main` | 62 / 58 | 62 / 62 |
| `method_record_results` | `ast`, `ast_callees` | 90 / 86 | 90 / 90 |
| `free_record_read_through` | `ast_main` | 20 / 12 | 20 / 20 |

A free `peek(xs: string[], k: i32): Box` and a method
`(s: Ints) peek(xs: string[]): i32` in one module, under `FERN_SEM_IR_SKIP=main`,
went from 14 / 5 to 14 / 8. The method's shifted flags no longer land on the
free function. The remaining 6 blocks are the AST floor for `Box { v: xs }`,
which pure AST also leaks. `ssa_rc`'s unit driver pins the rows directly:
checks 202–206 cover the `<Base>.<name>` key, the receiver dropped from the
`CNT:` / `PCNT:` flags, the syntax rows erased, and no bare row for a method
that keeps nothing of its receiver.

`method_array_result` still leaks 11 / 5 where the method itself is
AST-lowered (`ast`, `ast_callees`). That is the syntax's own `ARROWN:` refusal
for a body that may return a receiver field, and the row checks its census
only on the lowerings that produce the callee.

## Still leaking

- A produced result that is not a built box (a returned parameter, a forwarded
  call) keeps the AST caller's leak floor, as #10415 recorded.
- Generic instances get no `caller_rows` pass at all. An AST caller of a
  produced instance still reads the template's syntax rows.
