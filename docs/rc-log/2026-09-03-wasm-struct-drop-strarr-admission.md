# 2026-09-03 — wasm's `__struct_drop_<T>` freed base-copied `string[]` fields ungated (#8119)

The self-host-built `asm_run.wasm` ran linear memory to 3.84 GB on
`function main(): i32 { return 0; }` (1.70 GB on `var a: i32 = 1; return a;`)
and trapped at address 0xffffffff inside `$__fern_strcat`. The native-built
wasm of the same source, and the self-host-built x86-64 binary, both compile
the program.

## Where it was read

`wasm-tools print --print-offsets` puts the trapping `call 92` at the
`"strfldok:" + sfok[sfoi]` concat in `asm_ir.emit_module_ir_unit_flat`, the loop
over `b.strfld_ok_types` right after `emit_module_funcs(…, b)` returns. The
element read was -1 and the array's length was garbage: the buffer had been
freed and recycled, so the loop walked a huge "length" concatenating garbage
strings until `memory.grow` refused.

A WAT edit settled who freed it: record `b.strfld_ok_types` at the function's
entry into a global, `unreachable` in `$__fern_arr_dec` / `$__fern_rc_dec` when
that pointer arrives. First dec:

```
__fern_arr_dec ← __fern_arr_dec_ptr ← __struct_drop_irlower__FnSigs
              ← asm_ir__emit_module_funcs ← asm_ir__emit_module_ir_unit_flat
```

## The shape

`emit_module_funcs` builds its per-module registry by functional update over the
whole-program one it was handed:

```
var sg: irlower.FnSigs = irlower.FnSigs { ...irlower.fn_sigs_with_dyn(base, …), … };
```

A base copy hands the new box every array field pointer with NO retain
(`lower_expr_struct_lit`'s base-copy arm — only nested-struct / enum / admitted
string fields are retained). So `base`, the `fn_sigs_with_dyn` temp and `sg`
hold each `string[]` field at rc 1 between them. `sg` is dropped at the
function's exit, and the drop is where the backends differed:

- x86-64 / arm64: `asmcore.struct_drop_field_ops` (#7736) gates a `string[]`
  field on `strfldok:arr:<T>` (deep) / `strfldok:arrbuf:<T>` (buffer). `FnSigs`
  is refused on both — its fields are stored from non-fresh values and read by
  element — so the field is left alone. Sound leak.
- wasm: `emit_wasm_struct_drop_body` kept its own classifier, which took
  every pointer-element array field to `$__fern_arr_dec_ptr` with no admission.
  At rc 1 that frees the elements AND the buffer while `b` still reads them.

`asmcore.fern`'s directive-table comment recorded exactly this as the one arm
wasm's classifier disagreed on, "an open question (§9)". The compiler's own
`FnSigs` answers it: the admission is load-bearing on wasm too.

## The fix

`emit_wasm_struct_drop_body` now walks `asmcore.struct_drop_field_ops` like the
register backends and keeps only the wasm selection per directive
(`box_walk_dec` / `str_arr_free` → `$__fern_arr_dec_ptr`; `str_free` /
`arr_dec` → `$__fern_arr_dec`, a wasm string being one inline rc block;
`deep_struct_drop` → the `rc_is_unique`-gated `$__struct_drop_<T>`; the two
`elems_drop_*` pre-walks unchanged). `emit_ir_rc_bodies_from` seeds the need
set the classifier reads exactly as `asm_ir.emit_module_ir_unit_flat` does —
one `strfldok:<row>` per admission row, the `FNPTR:` markers as-is. wasm's own
`string[]`-in-struct-drop classifier is gone with it.

## Measured

| program | before | after |
|---|---|---|
| reduced probe (`Sigs { ...with_dyn(base) }` dropped, then `base.ys` read after 8 junk allocs) — self-host wasm | 29 (corrupted) | 25 |
| the same, self-host x86-64 | 25 | 25 |
| `asm_run.wasm` (self-host-built) on `return 0` | trap, memory 0xe4ec0000 | exit 0, 490 B of asm |
| `asm_run.wasm` on `var a: i32 = 1; return a;` | trap, memory ~1.7 GB | exit 0, 593 B of asm |

`$__struct_drop_Sigs` on the probe went from three decs (the nested struct plus
`arr_dec_ptr` on both `string[]` fields) to one.

## Traps

- `__rc_underflow` reads 0 throughout: the freed block was at rc 1, so nothing
  under-flowed. The counter cannot see an uncounted co-owner.
- The `wasm_ir_run` driver, which the wasm-hosted differential already
  covered, never builds a `FnSigs` by functional update, so it was green on the
  same compiler. The new differential is on `asm_run`.
- The field-reclaim twin (`emit_wasm_field_reclaim_body`) still releases every
  `frk == "a"` field ungated (shallow, under the cow guard). A base-copy probe
  (`var t = Sigs { ...sg }; sg = Sigs { ...sg, xs: fresh() }`) did not route
  through `__field_reclaim_Sigs` on either backend, so it stays as the
  2026-09-01 entry left it: measured divergent, no witness.

Regression tests: `strarr-field-base-copy-co-owner` in
`self_host_strarr_field_buffer_release_test.go` (all three backends), and
`TestSelfHostWasmHostedX86CompilerMatchesNative`, the wasm-hosted `asm_run`
against the native-hosted one on the issue's two programs.
