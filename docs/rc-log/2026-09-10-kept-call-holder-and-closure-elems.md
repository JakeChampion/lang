# A counted-share holder handed to a call keeps its box-only release, and a closure array has no rc elements

Refs #9016, #9017, #9023; the first two regressions of `2026-09-10-spread-carry-elems-secured.md`
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
| `test-e2e-selfhost-x86_64` shards 1 and 2 | two emission pins find the helper body's `call __fn___fern_rc_inc`; `all_ops` is 385 bytes over its ceiling |

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
value that can hold a struct, assigned to one, or returned, and whose callee
declares a return type that can hold one (`ret_type_fns`; a callee with no
row, a method, a closure or a generic, does not count). The `derived_anywhere`
form used for snapshot locals is too wide here: its return arm counts
`return (p.f.len() + …)`, and the `chain` / `always` / `respread` rows leaked
under it; without the return-type rule `return rd(p)` with `rd` returning an
`i32` withheld the deep drop too, and the string-only struct, wasm
struct-drop, borrow-inference and container-alias rows leaked. The rule keeps
`keepit(p)` allowed and `return infer(rebuild(result))` refused. The flip
honours the witness for the holder; the source's flip is unchanged.

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

## The helper body (three more rows)

`__fern_arr_inc_elems` was written into every x86-64 and arm64 binary, not
gated on a need like `__fern_arrarr_free` is. Its body calls
`__fn___fern_rc_inc`, so `TestSelfHostRcAliasIncX86_64/elides-retain-at-move-alias`
and `TestSelfHostRcMoveOnReturnX86_64/emits-no-inc-on-move`, which grep the
whole asm for that call, tripped on a program with no array copy in it, and
`TestSelfHostOperatorOverloadIRX86_64/all_ops` crossed its 18000-byte
small-output ceiling at 18385. Both backends now mark `arr_inc_elems` at the
call and emit the body under `has_need`; the root joins
`all_runtime_need_roots` so a per-module entry unit still links it.

## The handed-out element (#9023)

`TestSelfHostCheckerDifferentialX86_64/loop-map-shadow`: the self-host-built
checker dies on a signal binding `for (k, v) in m`. Under the sanitizer,
`__fern_arr_inc_elems` inside `Scope.bind` walks a `names` buffer whose element
`"k"` `__fern_str_arr_free` freed at the exit of `for_binding`. A hardware
watchpoint on the block's header found the free; the frame walk found the
walker.

`for_binding` splits the pattern into a fresh `string[]` and hands it to
`bind_tuple_destr_names`, whose parameter is box-borrowable, so the caller keeps
its deep free. Inside, `var nm: string = names[i]` reads an element that deep
free releases, and `out.bind(nm, et)` hands it to `bind`'s `name`, a parameter
that is stored (`s.names.append(name)`) and so neither borrowable nor counted.
A string parameter at such a position takes over the argument's reference; the
element had none to give, so the scope stored it uncounted. The deficit is
older than 59e40b4 — the shape ran on freed-but-intact memory before, and
leaks at afc6d0a in a sixty-line reproducer — and the element retain on the
un-share copy is what made it fault.

`retain_caller_elem_handoff` closes it at the handoff: a `str_param_elem_escapes`
argument (a `p[i]` of a borrowed `string[]` parameter, or a local such a read
bound) is retained at every call-argument site whose position is neither
borrowable nor `CNT:`-counted in the frame's registry, the same second owner
`xs.append(p[i])` takes inside one function. A position that keeps the value
for another reason leaks one count rather than freeing under a holder.

With the binding's name intact, `loop-map-pair-types` (`return k.len() + v`,
`v: i64`) then showed the checker typing a settled `i32 + i64` as `i32`:
`int_result` returned the left side where native's `commonIntegerWidth` widens
to the wider operand in either order. It now does too; a literal tree on either
side still reads at the other operand's width, so `a + 4611686018427387904`
against an `i32` stays E047 rather than becoming an `i64` (#8722).

## Gates

- `TestSelfHostKeptCallHolderX86_64`: the holder handed directly and through
  an alias, and the closure array as a local and as a struct field, pinned on
  the emit (`lift` carries no `__struct_drop_M`, `round` no
  `__fern_arr_inc_elems`) and run under the sanitizer against the
  interpreter's exit. Fails on 54ce1bf on every count, and the alias shape
  is a use-after-free there (sanitizer exit 124).
- `TestSelfHostStrElemHandoffX86_64`: the reproducer, pinned on the one
  retain in the handing function and run under the sanitizer against the
  interpreter's exit (a use-after-free on 54ce1bf). The checker differential
  gains five mixed-width cases, both operand orders at both return widths.
- `TestSelfHostConstFuncGen2`: green again; the four #9012 rows and
  `TestSelfHostSpreadCarryElemsX86_64` stay balanced; seed 301 passes;
  `TestSelfHostPerModuleEmitAllFixpointX86_64`, `make lint-all`, the
  complexity ratchet and the lane check are green.
