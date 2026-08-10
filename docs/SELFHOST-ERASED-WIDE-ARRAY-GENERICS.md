# Erased-wide `T[]` generics silently miscompile on the self-host wasm path

**Status:** BOTH steps done. Step 1 made the shape refuse instead of
miscompiling; step 2 makes it compile correctly.
**Severity when found:** silent wrong values — not a bail, not a validator
rejection.

## Reproducer

```fern
import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.reverse(xs);
    return (ys[0] * 10.0) as i32;      // want 45 (4.5); self-host wasm gives 15
}
```

## Measured

| path | `reverse` | `rotate_left` | `drop` |
|---|---|---|---|
| native `-interp` (oracle) | 45 | 45 | 45 |
| native compiled `-target x86-64-linux` | 45 | 45 | 45 |
| **self-host `-target wasm`** | **15** | **0** | **0** |
| self-host `-target x86-64` | 45 | — | — |

So: **self-host wasm only.** Every other path is correct, including the
self-host's own x86-64 backend.

## Why wasm and nothing else

These are erased `T[]` generics — `reverse[T](xs: T[]): T[]` builds its result
with `out.append(xs[i])` over an erased element type. On the register backends
every slot is 8 bytes, so an erased element stride is harmless: the wrong
*nominal* width still reads and writes the whole slot. On wasm32 an i32 is 4
bytes and an f64 is 8, so the erased stride is genuinely wrong — the copy reads
and writes at 4-byte steps through an 8-byte-element array, and the result is
silently garbage rather than a trap.

This is the same erased-wide problem the monomorphisation promotion in
`parse_func` (clause (c), `has_bare_scalar_param` + `feeds_wide_container`,
guarded by `all_tp_count == 1`) exists to solve — but that clause triggers on a
**bare scalar param**. `reverse[T](xs: T[]): T[]` has no scalar param; its
parameter is already the container. So it is never promoted, never cloned per
concrete instantiation, and stays erased all the way to emit.

## The part that matters most

**The erased-wide deferral gate did not catch it.** `wasm_ir_deferrals_ok` /
`module_erased_wide` exist precisely to keep an un-lowerable erased-wide module
off the wasm IR path — and with the AST emitters gone, an unhandled construct is
supposed to be a diagnostic naming the bail site, not silent output.

Here the module sailed through: `FERN_STRICT_IR=1` reports nothing, the compiler
exits 0, and the program returns wrong numbers. A hole in a safety gate is worse
than a plain bug, because the gate's entire job is to make this class loud.

## Why the fixture corpus misses it

All 335 fixtures pass on both self-host legs. The wasm leg would have to
instantiate a `T[]`-param stdlib generic at a **wide** element type to see it,
and nothing in the corpus does. Note also that the byte-identity argument
recorded in `CLAUDE.md` for the #5464 promotion — "the stdlib generics that
match (`array.intersperse` / `async.gather`) are DCE'd uncalled in the
bootstrap" — is about which generics get *promoted*; it says nothing about the
much larger set that are never promoted at all.

## Fixing it

Two directions, and the choice is a real one:

1. **Widen the promotion** so a `T[]`-param generic with a wide instantiation is
   monomorphised like the bare-scalar-param shapes are. Fixes the programs. Risk
   is the bootstrap fixpoint: widening the trigger changes which generics get
   cloned in the self-compile, and the existing byte-identity safety argument
   rests on the current trigger set.
2. **Close the gate first** so the shape is refused rather than miscompiled.
   Strictly smaller, strictly safer, and it converts a silent wrong answer into
   a diagnostic — which is the project's stated posture (IR-or-error). Worth
   doing even if (1) follows immediately after.

Recommend (2) then (1): a loud failure is a correct failure, and it makes (1)'s
test surface obvious.

## Step 1 landed

The gate now refuses the shape. `fn_param_sigs_of` gained flag `'7'` for an
erased typevar ARRAY param (`is_erased_typevar_array`), and
`erased_wide_expr`'s wasm branch flags a call passing a wide-element array
(`expr_is_f64arr` / `expr_is_i64arr`) to such a param. `reverse` / `rotate_left`
/ `drop` at f64 now produce the standard IR-ineligibility diagnostic instead of
wrong numbers.

The gate keys on the ELEMENT width, not on the generic, so narrow
instantiations are untouched: `array.reverse` at `i32[]` and `string[]` still
lower and run. Both directions are pinned by
`TestSelfHostErasedWideArrayGate{,Narrow}Wasm`, which drive `fern.fern` — the
real CLI — because these programs `import "std/array"` and a loader-less driver
would silently ignore the import and report a verdict about a broken program.

Both 335-fixture legs stayed green, which also answers the coverage question
above: no fixture depended on the miscompiled path.

## Step 2 landed

`parse_func` gained clause **(c-arr)**: the array-param sibling of clause (c),
promoting a single-typevar generic with a bare `T[]` param whose return feeds a
wide container (`has_bare_array_param`). Monomorphisation then clones it per
concrete element type, so the copy gets a real stride. `reverse` / `rotate_left`
/ `drop` at f64 now return the right values on wasm instead of being refused.

The exclusion this reverses was justified in-tree as "the wide value rides an
i32 array pointer, never trips the erased-wide gate, and promoting them would
over-monomorphise the stdlib". The first half was simply wrong — nothing wide is
passed by value, so the gate never fired, but the copy was silently wrong
anyway. The second half is a real cost, and is why the promotion stays guarded
to `all_tp_count == 1`.

**The gate from step 1 is still load-bearing, and that is deliberate.** A
two-typevar `map[T, U](xs: T[], f)` at a wide element type is NOT promoted, so
it is still erased — and still refused rather than miscompiled.
`TestSelfHostErasedWideArrayGateWasm` now pins exactly that case, so the gate
cannot rot back into silence once the headline shapes work. Fixing part of a
class is a reason to keep the guard for the rest of it, not to drop it.

## The wider deferral gate this sits inside

Clause (c-arr) is one member of a family. The full set of wasm-IR exclusions
lives in `wasm_ir_deferrals_ok` (`wasm_ir.fern`), and the only one that
genuinely remains is **erased-wide**: an i64/f64 value travelling through a
bare-typevar param. Two strategies close pieces of it.

**Widening** — used where the box layout is already uniform. A wide value
through a bare-typevar-RETURN PASS-THROUGH fn (`id[T](x: T): T`, #5586) or a
bare-TUPLE-return fn (`pair[K, V](k, v): (K, V)`, #5593) lowers on the wasm IR
path with erased params / returns / locals typed i64 — the uniform 8-byte slot —
and the caller coerces its arg/result at the boundary. See `is_erased_typevar`,
`erased_widenable`, `erased_passthrough_safe` (the body-safety gate that keeps
`fold`-style bodies which USE the typevar off the widened path), the `for_wasm`
flag folded into `ret_arrdyn` bit 2, the `'6'` `fn_param_sigs` flag +
`callee_param_is_erased_widened`, and the result-narrow in `lower_expr`.

Tuples were tractable because their wasm box is ALREADY uniform 8-byte-per-
element, and an erased `(T, T)` has byte-identical layout to a concrete
`(i64, i32)`: the reader reads each element at its concrete width from the same
`N*8` offset, so no result narrow is needed.

**Monomorphising** — used where the box layout SHIFTS with the type, which is
where widening cannot go. An `Option` is 8 B/@4 for i32 but 16 B/@8 for i64/f64;
an array stride is 4 vs 8. So the single-type-arg erased-wide containers —
`Option[T]`, `T[]`, and single-typevar `Result` returns — are closed (#5464) by
cloning rather than widening. Parser targeted promotion (clause (c) in
`parse_func`: `has_bare_scalar_param` + `feeds_wide_container`, guarded by
`all_tp_count == 1`) promotes an erased `some1[T](x: T): Option[T]` /
`dup[T](x: T): T[]` / `okr[T](x: T): Result[T, string]` to BOUNDED, so
`monomorphize_module` clones it per concrete instantiation
(`some1__i64(x: i64): Option[i64]`, with the concrete 16 B box). After cloning,
no call passes a wide value through a bare-typevar param, so
`module_erased_wide` clears; `wasm_ir_run`'s `mono_ok` rescue then admits the
module by judging eligibility on the SAME monomorphised module it emits — and
only when the raw verdicts both defer, so existing programs keep their exact
IR/AST verdict and byte-identical output.

A bare wide LITERAL binds the clone's `T` by magnitude (`mono_infer` →
`literal_is_i64`, mirroring `infer_expr_width` in lowering), so
`some1(5000000000)` clones `some1__i64`, not the truncating `__i32`.
Byte-identity is safe because the stdlib generics that match
(`array.intersperse` / `async.gather` — the only bare-scalar-param + `T[]`-return
shapes) are DCE'd uncalled in the bootstrap. Pinned by
`TestSelfHostErasedWideContainerWasm`.

### Why `all_tp_count == 1` is load-bearing

It is what makes `Result` SOUND. A `Result` matched by `feeds_wide_container` is
promoted only when the type var is the fn's ONLY one, so the clone is fully
concrete: `okr__i64: Result[i64, string]`; `Result[T, T]` → `Result[i64, i64]`;
`errg[E]: Result[i32, E]` → `Result[i32, i64]`.

It also blocks the partial-promotion hazard. A multi-typevar generic where only
ONE var matches clause (c) — `scan[T, A](xs: T[], init: A, f: (A, T) => A): A[]`,
where A matches and T does not — would otherwise clone with an erased sibling
`T`: a malformed clone that crashes. Caught by `array_hof`.

### Two-typevar `Result[T, E]`

`okg[T, E](x: T): Result[T, E]` (`all_tp_count == 2`) is **not** deferred.
`parse_func` carries a separate clause (c′) — `all_tp_count == 2 &&
result_two_bare_vars(ret_type, unbounded_tps) && has_any_bare_scalar_param(...)`
— that promotes BOTH vars, so neither is stranded erased on the Err arm: `T`
binds from the scalar arg, `E` from the call-site return annotation
(`infer_inst_ret`). `result_two_bare_vars` requires BARE var args (a nested
`Result[Option[T], E]` does not match), which is what guarantees the clone is
fully concrete. The two paths stay disjoint — clause (c)'s `all_tp_count == 1`
guard excludes this shape.

### Closed, no longer avoid-list items

- **Runtime-helper migration to Fern is complete** — `chr`, `str_concat`,
  `i32_to_string`, `str_to_upper`/`lower`, `str_repeat`, `str_reverse`,
  `str_replace`, `string_from_bytes`, `str_split` all lower as Fern functions
  via the raw-memory intrinsics.
- **Filesystem ops** (`stat` / `read_dir` / `remove_file` / `remove_dir_all` /
  `temp_dir`) lower with IR-side struct-box construction (module type-ids via
  `struct_type_id`; `TestSelfHostStatIRWasm` et al.).
- **libm transcendentals** (`fexp` / `flog` / `fsin` / `fcos` / `fpow`) lower via
  polynomial-approx WAT helpers (`wasm.exp_func` / `log_func` / `pow_func` / …,
  the wasm siblings of the arm64 helpers, wired in `wasm_ir_run`).
- **Streaming file I/O** — `open_reader` / `open_writer` / `open_appender` +
  `Writer.write`: `$__fern_open_file` does `path_open` under preopen fd 3,
  `$__fern_writer_write` `fd_write`s the bytes (#4372).
- **`xs.join`** — via the `$__fern_arr_str_join` shim over the
  `$__fern_str_join` WAT worker (#5328).
- **8-byte-VALUE maps** — i64/u64 AND f64, end-to-end for `set` / `get_or` /
  `get` / `values` / `iter`, via `$__fern_map_{set,get_or,values,get,iter}_w64`.
  These box the 8-byte value into an rc cell riding the i32 value column (get →
  a 16-byte Option; values/iter → a fresh 8-byte-element array), selected by the
  `widekind` op flag; f64 rides the same raw-byte cells as i64/u64 with an
  f64↔i64 reinterpret at the scalar sites. The former
  `module_has_wide_map_val_cached` gate is retired (#5253).
- **Component-model async / readiness / sockets** — `poll` /
  `wasm_timer_pollable` / `wasm_pollable_drop` / `block` / `wasm_poll` / `tcp_*`
  lower via the component-model wasi interfaces (sub-issues #4315–#4320, all
  closed 2026-07).

### Clean error endpoints (not gaps)

`c_call`, `subprocess`, and `timer_fd` are rejected before emit by
`wasm_unsupported_builtin`. FFI `__c_call<n>` has no wasm C ABI, so refusing it
is the correct terminal answer, not a deferral to widen (#4375).

## Related: fn-typed tuple elements — CLOSED

A separate gap in the same neighbourhood (a type that the coarsener flattened
before the layout code could see it), recorded here because nothing else owns
it. It is closed end-to-end:

- `parse_type_name` coarsens a parenthesized `=>` type to `"fn"` only when there
  is NO depth-1 comma. A tuple's fn segments coarsen individually via
  `coarsen_fn_elems` → `"(fn, i32)"`.
- The lift pass wraps every fn-valued tuple element into a `__mkclo$` env box.
- `irlower`'s `"clo"` element tag drives env-first `t.N(args)` dispatch plus
  closure-local binding.

Pinned self-host-side by `TestSelfHostTupleFnIR*`, native-side by
`internal/e2e/tuple_fn_elem_test.go`.
