# A counted-share holder handed to a call keeps its box-only release, and a closure array has no rc elements

Refs #9016, #9017; both regressions of `2026-09-10-spread-carry-elems-secured.md`
(59e40b4, in #9014).

## What was red

Every generation-2 self-host binary crashed, and one fuzz seed with it, on
main from 54ce1bf:

| check | symptom |
| --- | --- |
| `Bootstrap / candidate-arm64-linux` | stage1 segfaults compiling `smoke.fern` |
| `selfhost-fixpoints-x86_64`, two-generation const_func check | `cfg_mmc2` segfaults on every program |
| `wasm-wholecompiler-link-x86_64` | the wasm-hosted compiler answers `main did not lower: unknown statement` |
| `cli-driver-tests-x86_64`, nested-arith | the self-host-built `wasm_ir_run.native` dies on a signal |
| `diff-selfhost-shard0`, seed 301 | the compiled program segfaults in `__fern_rc_inc` |

## The holder (#9016)

`FERN_RC_FREE_DEBUG=1 FERN_RC_TRACE=1` on gen 2: a use-after-free at the
`var b: ast.Stmt[] = fn.body` retain in `closure_ret_fns_of`; the block is a
`FuncDecl.body` buffer, freed by `__struct_drop_parser__Module` issued from
gen 2's own `irlower.lift_lambdas_view`. Bisected to one line of 59e40b4: the
`spread_sites` refusal in `enum_arr_field_share_read`. With it gone, the
`top_stmts: mod.top_stmts` read in `result`'s literal takes a counted share,
and the bind-site flip (`mark_enum_arr_share`) drops the holder's `NODEEP:`
marker, which was the only thing keeping `result` box-only. Its deep walk then
reaches `result.funcs` at rc 1 and frees `FuncDecl` boxes that
`parser.lower_defers_module(result)` has just copied, uncounted, into the
module it returns.

The refusal that gate provided was an accident: `lift_lambdas_view` spreads a
`FuncDecl`, whose `body` has the same field type. Restoring the line fixes gen
2 and re-opens `TestSelfHostArrEnumFieldReadShareX86_64/respread` at 700/400.

The fix names the hazard instead. `reclaimable_names_of` mints a `NOFLIP:`
witness next to `NODEEP:` when the reason is a move (`moves_fields_stmts`,
`optstruct_body_moves_field`) or the new `handed_to_kept_call`: the local, or
a local bound whole from it (`var alias = result`, chased to a fixpoint),
appears as a bare-ident argument of a call whose result is kept, bound to a
value that can hold a struct, assigned to one, or returned. The
`derived_anywhere` form used for snapshot locals is too wide here: its return
arm counts `return (p.f.len() + …)`, and the `chain` / `always` / `respread`
rows leaked under it. The bare-argument form keeps `keepit(p)` bound to an
`i32` allowed, and `return infer(rebuild(result))` refused. The flip honours
the witness for the holder; the source's flip is unchanged.

## The closure array (#9017)

Seed 301 holds `s0.with(1, …)` on a `((i32) => i32)[]`. `arr_expr_counted_elems`
read the slot's `"fn"` spelling as enum-like, so the clone was followed by
`__fern_arr_inc_elems`, and `__fern_rc_inc` wrote an rc word eight bytes
before a lambda's entry. Whether that faults depends on the bytes that happen
to sit there, which is why a small program with the same shape ran clean while
the seed died. A struct field of that type is spelled `"fn[]"` and
`is_enum_array_field_type` admits it the same way, so the value forms on
`h.hs` and the in-place field forms had the same hole. The exclusion sits in
`is_counted_elem_array_type`, the one predicate all four sites ask.

## Gates

- `TestSelfHostKeptCallHolderX86_64`: the holder handed directly and through
  an alias, and the closure array as a local and as a struct field, pinned on
  the emit (`lift` carries no `__struct_drop_M`, `round` no
  `__fern_arr_inc_elems`) and run under the sanitizer against the
  interpreter's exit. Fails on 54ce1bf on every count, and the alias shape
  is a use-after-free there (sanitizer exit 124).
- `TestSelfHostConstFuncGen2`: green again; the four #9012 rows and
  `TestSelfHostSpreadCarryElemsX86_64` stay balanced; seed 301 passes;
  `TestSelfHostPerModuleEmitAllFixpointX86_64`, `make lint-all`, the
  complexity ratchet and the lane check are green.
