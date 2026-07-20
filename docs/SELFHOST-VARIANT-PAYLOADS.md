# Self-host variant payloads — wide slots + multi-payload (plan)

**Goal.** Give the self-hosted Fern compiler (`examples/self_host/`) real
**multi-payload variants** and **wide enum payload slots** (64-bit ints /
floats), so the deferred `@import` extern variant ports (non-uniform,
mixed-width, multi-field) can finally land on the self-host side — the single
backend gate in front of several BYOW slices (see
`docs/WIT-BRING-YOUR-OWN.md`). The Go backend already does the full
position-wise canonical join for all of these; the self-host can't mirror it
until the language itself can carry the payloads.

This doc scopes that feature: the current representation, the exact
locations that hardcode the single-`i32`-payload assumption, the
representation decisions, and a slice plan that stays green (no self-compile
regression) per PR.

## Where we are today (recon)

The self-host has **two** codegen paths plus an interpreter, all of which
assume a single `i32` enum payload and 4-byte struct fields:

- **AST emitters** (direct AST→target): `wasm.fern` (→WAT, the BYOW path),
  `asm.fern` (→x86-64), `asm_arm64.fern` (→arm64).
- **SSA pipeline** (shared IR→target): `ssa.fern` → `ssa_wasm.fern` /
  `ssa_x86.fern` / `ssa_arm64.fern`.
- **Interpreter**: `interp.fern` (the self-compile/stage-2 path; i32-only).
  (A second evaluator, the bytecode VM `vm.fern`, used to sit alongside it
  here but was retired in #4392 — an unreachable fifth implementation of
  Fern semantics with no production consumer.)

**Key facts.**

- **Parser desugaring.** `parser.fern parse_enum_decl` (~2625–2668) turns a
  payload-bearing variant `V(T)` into a `StructDecl` with a **single marker
  field `__ev`** of the payload type. Multiple payloads `V(A, B)` parse but
  **only the first is kept** — the rest are consumed and dropped. Built-in
  variants (`add_builtin_variant`, ~2728) follow the same single-`__ev`
  shape. Comment at ~4798: "enum variants carry at most one payload (`__ev`)".
- **Checker.** `variant_payload_count` (checker.fern ~2715) returns 0 or 1;
  E015 (~2749) enforces *binding count == payload count*, so a multi-binding
  pattern `V(a, b)` is rejected up front.
- **Struct fields are 4-byte slots.** `wasm.fern struct_field_off(i) = 4 +
  i*4` (~2982) — every field is one `i32` word, tag/shape-id at offset 0.
  `ssa.fern struct_field_width` (~2174) only widens **pointer** fields
  (string/array/nested-struct) to 64 bits; `i64`/`f64` fields fall through
  to 32. **So no aggregate (struct or enum) can hold an `i64`/`f64` today.**
- **Enum box.** `wasm.fern enum_box` (~3415) is a fixed `[tag:i32 @0][payload
  :i32 @4]` 8-byte box for Option/Result; the user-variant constructor
  (~3732) allocates `4 + nfields*4` and `i32.store`s each arg at `4 + i*4`;
  `match` (~6410–6461) reads the payload with `i32.load` at offset 4.
- **64-bit values DO work as scalars.** `wasm.fern` has `emit_i64` /
  `emit_f64` / `is_i64_expr` / `func_param_is_i64` / `func_param_is_f64`, a
  `store_kind`/`load_kind` helper (~3229–3235: `i64`→`i64.store`,
  `f64`→`f64.store`, else `i32`), and **8-byte heap slots for `i64[]` /
  `f64[]` arrays** (element load/store at 8-byte stride). i64/f64 are only
  ever in locals/params/arithmetic/array-elements — **never in struct/enum
  fields**. The wide-slot machinery the array path uses is exactly what an
  enum payload needs.

### Locations that assume single i32 payload

| Assumption | File | ~Lines |
|---|---|---|
| `V(A,B)` keeps only first payload (`__ev`) | parser.fern | 2625–2668, 2728 |
| `variant_payload_count` ≤ 1; E015 single-binding | checker.fern | 2715, 2749 |
| `PatVariant.extra_bindings` is an arity error | parser.fern | 134–160, 1536 |
| Option/Result box `[tag@0][payload:i32@4]` | wasm.fern | 3415–3435 |
| User-variant construct: `i32.store` @ `4+i*4`, alloc `4+nf*4` | wasm.fern | 3732–3749 |
| Match payload bind: `i32.load` @ 4 | wasm.fern | 6445, 6456 |
| `struct_field_off = 4 + i*4` (uniform 4-byte) | wasm.fern | 2982 |
| `struct_field_width`: 64 only for pointers | ssa.fern | 2174–2190 |
| SSA match loads tag@0 (32-bit), binds whole/word | ssa.fern | 1388–1458 |
| asm / asm_arm64 8-byte box `[shape@0][payload@8]` | asm*.fern | 836 etc. |

## Representation decisions

1. **Wide single payload — reuse the array 8-byte-slot machinery.** An enum
   payload's width is its declared `__ev` type: `i64`/`u64`/`f64` ⇒ 8-byte
   slot (`i64.store`/`f64.store`), everything else ⇒ 4-byte `i32` slot. Drive
   it through the existing `store_kind`/`load_kind` + `is_i64_expr`/`emit_i64`
   /`emit_f64` helpers — no new storage primitive. The box for a single
   8-byte payload is `[tag:i32 @0][payload @4]` sized to 12 bytes; the
   payload at offset 4 is 4-aligned, not 8-aligned, but wasm permits
   misaligned `i64`/`f64` access (alignment immediate is a hint, never traps)
   — the array path already relies on this for the unaligned self-host heap.

2. **Multi-payload — keep all `__ev`s, lay them out by width.** `V(A, B, …)`
   desugars to a struct with fields `__ev0, __ev1, …` (or keep `__ev` for the
   first to minimise churn and add `__ev1…`). Field offsets accumulate by
   per-field width (4 or 8), so the layout helper becomes width-aware
   (`struct_field_off` → a per-struct prefix-sum, not `4 + i*4`). The
   constructor stores each arg with its kind; `match` binds each with a
   width-aware load at the field's offset.

3. **Checker.** `variant_payload_count` returns the real count; E015 fires
   only on a genuine count mismatch; each binding is typed from its field.

4. **Scope by backend.** The BYOW extern ports run **only through
   `wasm.fern`**, so land the feature there first (it has the i64/f64
   machinery already). The shared parser/checker changes must not break the
   other backends' *existing* programs — and since no self-host source or
   test constructs a multi-payload / wide-payload variant on asm/ssa/vm, the
   parser keeping extra `__ev`s is inert for them until they're ported. Each
   later backend (ssa, asm, interp/vm) is its own slice, gated by a run test
   that actually exercises a wide/multi-payload variant on that target.

## Slice plan (each green, one PR)

1. **S1 — wide single payload in `wasm.fern`.** A single-payload variant
   `V(i64)` / `V(f64)` stores + binds its payload at full width. Box sizes
   the payload by `__ev` kind; the constructor uses `store_kind`/`emit_i64`/
   `emit_f64`; `match` uses the width-aware load; the bound local is typed so
   downstream `is_i64_expr` sees it. No parser/checker change (single payload
   already parses). *Gated by a `TestSelfHostWasm…` run test that round-trips
   a value needing 64 bits through an enum, + the self-compile oracle stays
   green.* **Unblocks the self-host mixed-width single-field extern port.**
2. **S2 — non-uniform same-width self-host extern port. ✅ Done (#5327,
   non-uniform i32 join).** The `f32` arm of a `{ i(s32), f(f32) }` set rides
   the canonical 32-bit (i32) join. The self-host has **no distinct f32** (it
   widens f32 to f64 everywhere, `is_float_type`), so an f32 enum payload is
   stored in an 8-byte f64 slot — a 4-vs-8-byte impedance bridged with the
   #4366 f32 demote/reinterpret machinery: the param side branches per-arm on
   the box's struct_id (`extern_variant_payload_join_i32_f32`) and coerces the
   f32 arm to the join (`f64.load` → `f32.demote_f64` → `i32.reinterpret_f32`);
   the result side branches on the disc (`extern_variant_result_store_f32`) and
   promotes the host's raw f32 bits back into the box's f64 slot
   (`f32.reinterpret_i32` → `f64.promote_f32`). The gate keeps the join i32:
   the variant must also carry an i32/u32 payload arm and no 64-bit arm.
   **Still deferred:** a uniform-f32 variant (every payloaded arm f32 — its
   canonical join is an f32 *signature* slot the self-host, with no distinct
   f32, can't carry) and an f32 arm mixed with a 64-bit arm (the same
   impedance against the i64 join).
   *Gated by `TestSelfHostExternVariantF32Arm{Param,Result}CustomProvider`.*
3. **S3 — mixed-width self-host extern port. ✅ Done (#2515) + f64 arm.** The
   i64 join slot + per-arm coerce, mirroring the Go `appendVariantPayloadI64`
   path (i32 arm zero-extends, 64-bit arm `i64.load`s). Initially i32/i64
   (#2515); now also an **f64 arm** — an f64 has no width impedance (it rides
   the i64 join as its raw bits, `i64.load` being the value-preserving
   reinterpret the canonical f64→i64 coercion wants), so `{ i(s32), d(f64) }`
   works by accepting f64 single-field arms in the gate / `join_is64` /
   `arm_join_i64`. A **uniform-f64** variant (every arm f64) joins to an f64
   slot, not the i64 container — **now done** too (a separate
   `extern_variant_is_uniform_f64` path: an f64 import param, an `f64.load` of
   the box payload, an `f64.store` into the result box). Only **f32** arms stay
   deferred (the f32-as-f64 impedance above).
   *Gated by `TestSelfHostExternVariantMixedWidth…`,
   `TestSelfHostExternVariantF64Arm{Param,Result}CustomProvider`, and
   `TestSelfHostExternVariantUniformF64{Param,Result}CustomProvider`.*
4. **S4 — multi-payload variants in `wasm.fern` + parser + checker. ✅ Done.**
   The parser desugar keeps every payload as a field `__ev`, `__ev1`, … (no
   longer dropping all but the first); `variant_payload_count` (checker) counts
   the `__ev`-prefixed fields so E015 allows a matching multi-binding `V(a,
   b)`; `wasm.fern` binds each payload field at `struct_field_off(j)` (a
   multi-binding match) and collects the extra bindings as locals. The S1
   constructor already stored every arg, so keeping the fields also sizes the
   box correctly (closing the old multi-payload heap-overflow latent bug).
   Scoped to **i32 payload fields** (the 4-byte `struct_field_off` stride);
   single-payload stays byte-identical (S1 wide path untouched) and wide +
   multi-payload together is a later refinement. Gated by the
   `enum-multi-payload-{2,3,second-arm}` `TestSelfHostWasmRun` cases + the
   self-compile / printer / checker oracles (the self-host source has no
   multi-payload variant, so the shared desugar is unchanged for it).
   **Unblocks the self-host multi-field extern ports (S5).**
5. **S5 — multi-field self-host extern ports. ✅ Done (uniform i32).** With S4
   representing a `Click(i32, i32)` variant, the self-host `@import` extern
   marshalling gained the multi-field shape, mirroring Go's
   `appendVariantParamMultiField` / `…ResultStoreMultiField`. The gate accepts a
   variant with `>=2` i32 fields (`extern_variant_is_multifield` /
   `extern_variant_max_fields` = SlotCount); the import flattens to (disc,
   SlotCount×i32). **Param**: a per-slot lazy if/else chain on the box struct_id
   (`extern_variant_multifield_slot`) pushes the matched arm's field j or 0 to
   pad a shorter arm (lazy, since a shorter arm's box has no slot j). **Result**:
   the return area is sized for the widest arm, so the wrapper blind-copies all
   SlotCount slots into the box fields (a match on a shorter arm reads only its
   own — no per-arm result branch). Scoped to i32-class fields; mixed-width /
   float multi-field arms (the fully general join) stay deferred, as on the Go
   side. Gated by `TestSelfHostExternVariantMultiField{Param,Result}CustomProvider`
   (the same `{ click(tuple<u32,u32>), key(u32), close }` shape as the Go tests).
6. **S6+ — other backends** (ssa pipeline, asm/asm_arm64, interp/vm), each a
   slice with a target-specific run test, only as the language feature (not
   just BYOW) warrants it.

S1–S5 land the BYOW self-host parity the matrix is missing; S6+ generalise
the language feature to the remaining backends.

## Risks / notes

- **Self-compile oracle is the safety net.** The self-host compiles itself;
  every slice must keep `TestSelfHostSelfCompile` + the per-backend emit
  oracles byte-identical / green. Additive layout changes (a wider payload
  only when the field is actually 64-bit) keep existing all-i32 programs —
  including the compiler's own source — unchanged.
- **Misaligned 64-bit access** at payload offset 4 is fine on wasm (hint
  only); the array path already does this on the unaligned self-host heap.
  The asm/ssa backends (S6) will need real alignment handling.
- **Scope creep into general struct fields.** S1/S4 touch *enum* payload
  layout; making *all* struct fields width-aware (so a plain struct can hold
  an `i64`/`f64`) is a strictly larger change — keep it out of the BYOW
  critical path unless a port needs it.
