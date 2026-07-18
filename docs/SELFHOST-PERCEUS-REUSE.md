# Design: porting Perceus constructor-reuse (FBIP) to the self-hosted compiler

Status: **CORRECTED — constructor reuse is largely already implemented.** This
doc was written believing constructor reuse was the "one large remaining piece"
of #2649 goal 2. On closer inspection of `irlower.fern` that premise was **wrong**:
the self-host already implements self-overwrite, cross-local, enum-donor, and
consuming-match reuse (enabled unconditionally, exercised by the byte-identical
self-compile, and tested — see §2). The genuine remaining deltas vs the native
reuse are narrow **optimisations** — tuple reuse and richer struct field types
(§2, §3). Goal 2's memory-management-**behaviour** target (no leaks, in-place
reuse of the common cases) is substantially met. The native-mechanism decode
(§1, §6) remains accurate and useful; treat §3 as the (short) remaining-work
list, not a from-scratch port plan.

The RC/ownership slices (inc/dec insertion, borrow inference, per-type deep
`__struct_drop_<T>` / `__field_reclaim_<T>`, snapshot-param reclaim) landed
earlier on the self-host IR path. This document describes the native mechanism,
the self-host's actual current state, the remaining deltas, the soundness
invariants, and the testing strategy.

Why it matters: reuse turns a `free(old) + alloc(new)` pair at a construction
site into an in-place overwrite of the old box — zero-alloc updates for the
common "consume a value, build a same-shaped value" pattern (map/fold/tree
traversals, per-iteration struct churn). It is the difference between the
self-hosted compiler allocating on every loop iteration and reusing one box.

**This is memory-safety-critical.** A mis-paired or under-guarded reuse in the
self-hosted compiler is a use-after-free / double-free that miscompiles *every*
program the self-host builds. Every slice must be refcount-guarded, gated, and
validated by byte-for-byte differential + fixpoint tests before it is enabled.

---

## 1. What the native compiler does

Native reuse lives in `internal/ir/ir.go` (the builder), gated on two
independent flags in `internal/ast/ast.go`:

- `RcFreeEnabled` (default true) — the Phase-3 freelist allocator (recycling
  freed boxes). Reuse only makes sense when freeing.
- `RcReuseEnabled` (default true) — the constructor-reuse (FBIP) layer, a
  *separate axis on top of* `RcFreeEnabled`.

The moving parts:

| Native function (`ir.go`) | Role |
|---|---|
| `computeReuseSources()` | The general-FBIP **pairing analysis**: matches a construction site C (`var c = T{…}` / a tuple lit) to a **dead, owned** struct/tuple local D of the exact same box layout whose box C can repurpose. Returns the C→D map + the set of consumed D names (so precise-drop won't *also* drop them). |
| `structReuseEligible` / `tupleReuseEligible` / `enumReuseLoads` | **Eligibility**: box sizes match exactly; fields are i32-class scalars or single-word rc-tracked pointers (strings + wide/float scalars excluded); enum layout uniform; box ≤ 2048 (exact-fit freelist class). |
| `reuseClassOf` / `reuseSourceLayout` | The box "kind" (struct/tuple/enum), type name, and freelist class `(alloc+15)&-16` of `data + 8-byte rc header`. |
| `emitReuseToken(dName, dAlloc, cAlloc)` | **Codegen for the reuse allocation**: at C, if D's box is uniquely owned, reuse it; else fall back to a fresh alloc. |
| `emitReuseOldFieldDrops(...)` | On the reuse branch, **deep-drops D's old pointer fields** before C's stores overwrite them (D is dead, so it carries nothing into C). |
| `tryStructReuseOverwrite` / `tryEnumReuseOverwrite` | The **self-overwrite** special case (Phase 5b/5c): D == C, i.e. `a = T{…}` reassigning an owned local `a` reuses `a`'s own old box. Simpler than the general cross-local case. |
| `consumingReuseCtor` | The **consuming-match FBIP** traversal: `match (x) { V(p) => W(f(p)) }` reuses x's variant box for W. |

Runtime support (emitted on demand by the backends, e.g.
`internal/codegen/x86_64/x86_64.go`):

- `__fern_alloc_reuse` (gated on `usesAllocReuse`) — given a candidate dead box
  pointer and a size, return that box if it is live + uniquely owned (rc == 1)
  and the right freelist class, else allocate fresh.
- `__fern_rc_is_unique` (gated on `usesRcIsUnique`) — 1 iff a live heap object
  has rc == 1. The second, *runtime* gate: a statically-paired-but-shared D
  degrades to dec-then-fresh-alloc — never unsound, never a leak.

### The invariant (why it's sound)

A reuse is the pair **(static pairing, runtime guard)**:

1. **Static** (`computeReuseSources`, deliberately narrow first cut):
   - D and C are the same *kind* (struct↔struct / tuple↔tuple / enum↔enum) and
     the same exact box layout / freelist class.
   - D is a `var`, declared before C in the **same block** (function body, loop
     body, or if-arm), never reassigned, name-unique (no shadowing).
   - D is `freeEligible` — **owned**, never a borrowed param.
   - D is **dead from C onward** within its block: referenced in no statement at
     or after C's index. So C's fields never read D, and nothing observes D's box
     after C repurposes it.
   - The reuse **zeroes D's slot**, so the block-exit sweep (and any path that
     does *not* reach C) null-guards to a no-op / drops D normally.
2. **Runtime** (`__fern_alloc_reuse` → `__fern_rc_is_unique`): only reuse if D's
   box is uniquely owned *at that moment*. A shared D (rc > 1) copies —
   dec-then-fresh-alloc. This is the safety net that makes a conservative static
   analysis sound even under aliasing the static pass can't see.

A mispaired or shared D therefore always degrades to the pre-reuse behaviour;
it is *observationally transparent* — the native differential gates run
free-off vs free-on **and** reuse-off vs reuse-on and require byte-identical
results (see `internal/e2e/testdata/cases/general_reuse_struct/main.fern`).

---

## 2. What the self-hosted compiler has today

Already ported to the self-host IR path (`irlower.fern` + `asm_ir.fern` /
`asm_arm64_ir.fern`):

- The **freelist allocator** + rc header (equivalent to `RcFreeEnabled`).
- **inc/dec**: `__fern_rc_inc` (Perceus dup), `__fern_rc_dec` /
  `__fern_arr_dec` (release), exit-sweep drops.
- **Deep drop**: per-type `__struct_drop_<T>` (deep-drops rc-array struct
  fields) and `__field_reclaim_<T>` (frees replaced array-field buffers), plus
  `__fern_snapshot_dec` (snapshot-param consume-rebind reclaim).
- **`__fern_rc_is_unique`** — **the runtime uniqueness guard already exists**
  (`asm_ir.fern`, `__fn___fern_rc_is_unique`; also in `is_fern_helper` /
  `ir_helper_symbol`). This is the key building block: the reuse port does *not*
  need to invent the runtime guard, only the `alloc_reuse` wrapper around it.
- Aliasing analysis for **`.with()`** in-place vs clone (`aliased_names`): an
  `a = a.with(i, v)` self-reassign lowers in place only when `a` is unaliased,
  else a value-producing clone (`lower_arr_with_value`) — *"correct, if leak-y,
  ahead of the full Perceus refcount-guarded reuse"* (`irlower.fern` ~L355).

**CORRECTION (verified against `irlower.fern`)**: the constructor-reuse layer
is **already ported** — the earlier claim that it was "not yet ported" was wrong
(over-extrapolated from the `.with()`-only "ahead of the full Perceus reuse"
comment). What exists today, enabled unconditionally (so the byte-identical
self-compile already exercises it) and tested:

- `self_overwrite_reuse_sites` / `emit_self_overwrite_reuse` — the
  functional-update self-overwrite `var c = T{ ...d, f: v }` reusing d's dead
  same-type box in place (the record-update idiom; native's
  `tryStructReuseOverwrite`).
- `cross_reuse_sites` / `emit_cross_struct_reuse` — the general cross-local
  reuse (a dead donor local's box reused for a later same-type construction;
  native's `computeReuseSources`).
- `enum_donor_reuse_sites` / `emit_enum_donor_reuse` (re-shape a dead scalar-enum
  box as a same-size struct via `op_struct_set_shape`) and
  `consumed_inarm_reuse_sites` / `emit_inarm_match_reuse` (consuming-match FBIP;
  native's `consumingReuseCtor`).
- The in-place field stores (`op_struct_set`, `op_struct_set_shape`).
- The runtime uniqueness guard **at the reuse site**: as of #4350 (slices 1–5)
  **all five emitter families emit native's token shape** —
  `__fern_rc_is_unique(d)` selects a token, `__fern_alloc_reuse(token, slots)`
  hands back d's block or a fresh one, and a shared donor degrades to a fresh
  construction instead of corrupting. Per family: `emit_self_overwrite_reuse`
  (slice 1 — carried fields copied + rc-inc'd on the fresh arm),
  `emit_cross_struct_reuse` (slice 2 — shape word copied via the raw-memory
  ops on the fresh arm), `emit_cross_tuple_reuse` (slice 3 — `slots` becomes
  the tuple arity), `emit_enum_donor_reuse` (slice 4 — token select only;
  scalar payloads, unconditional re-shape), and `emit_enum_cross_reuse` +
  `emit_inarm_match_reuse` (slice 5 — the enum-cross fresh arm decs the donor
  reference instead of releasing its payload arrays, which the surviving
  aliases still own; the in-arm guard calls `alloc_reuse` per arm, sized by
  the matched variant, and its array cow-guard splits per guard arm: dec the
  replaced old array only when reusing, retain (`rc_inc`) a same-slot MOVE
  alias when degrading to a fresh box).
- Tests: `internal/e2e/rc_heap_bump_general_reuse_test.go`,
  `rc_heap_bump_enum_reuse_test.go`, `rc_c2_consuming_reuse_test.go`.

**The genuine remaining deltas vs the native reuse** are narrow, not a large
port:

1. **Tuple reuse — DONE.** Tuple locals are now reuse/donor targets on both the
   self-overwrite and cross-statement paths (`cross_tuple_reuse_sites` /
   `emit_cross_tuple_reuse`, `tuple_lit_ctor_eligible`), matching native's
   `general_reuse_tuple`. Covered by `TestSelfHostTupleReuseIRX86_64` and the
   `loop-tuple-*` / `cross-block-tuple-*` cases in the loop-reuse suite.
2. **Richer struct field types — NESTED-STRUCT DONE (both paths).** The self-host
   reuse-eligibility (`struct_fields_reusable`) admits *scalar* + *leak-safe-array*
   + **direct nested-struct** (`inner: Inner`) fields; native additionally admits
   the remaining single-word rc-pointer kinds (enum / Map / closure / tuple). A
   nested-struct field is now reused on BOTH the cross-statement
   (`emit_cross_struct_reuse`) and the functional-update self-overwrite
   (`emit_self_overwrite_reuse`) paths via the single `struct_fields_reusable`
   predicate: an overwritten `inner` field's old box is full-freeing-dropped
   (`__struct_drop_<FT>` when it has rc fields, then a box dec — native's
   `emitFieldDropOnStack`) before the fresh inner is written, and the new value is
   required to be a fresh (no-base) struct literal so the reused box solely owns it.
   The self-overwrite path is sound because override values may not reference `d`
   (existing gate), so there is no read-after-overwrite hazard, and a CARRIED
   (non-overridden) nested-struct field simply moves with the reused box (freed
   once at exit via `__struct_drop_<T>`). Covered by the `loop-nested-struct-field-*`
   and `loop-funcupdate-nested-struct-*` cases. REMAINING excluded kinds: enum /
   Map / closure / tuple pointer fields (leak-safe, just not reused in place).

Both were bounded optimisation deltas, not the memory-management-correctness gap
the earlier framing implied. Goal 2's *behavioural* target (no leaks, in-place
reuse of the common struct / enum / match cases) is substantially met.

---

## 3. Sliced port plan (smallest-safe-first)

**Superseded by the §2 correction.** The self-overwrite / cross-local / enum /
consuming-match slices this section originally proposed are **already
implemented and enabled** — do not re-implement them. Only the two bounded
deltas from §2 remain, and each is an *optimisation* (the excluded shapes are
already leak-safe, just not reused in place), so each is best done ON its
existing test scaffolding rather than gated off:

- **Delta A — tuple reuse. DONE.** Tuple locals are reused on the self-overwrite
  and cross-statement paths (`cross_tuple_reuse_sites` / `emit_cross_tuple_reuse`),
  reusing the same `__fern_rc_is_unique` guard + in-place stores. Pinned to
  native's `general_reuse_tuple` behaviour via `TestSelfHostTupleReuseIRX86_64`
  and the loop-reuse suite's `loop-tuple-*` / `cross-block-tuple-*` cases.
- **Delta B — richer struct field types. NESTED-STRUCT DONE (both paths).** The
  single `struct_fields_reusable` predicate admits a direct nested-struct pointer
  field; both `emit_cross_struct_reuse` (cross-statement, dead donor) and
  `emit_self_overwrite_reuse` (functional update `c = T{...d, inner: Inner{…}}`)
  release the old inner box with a FULL freeing drop (`__struct_drop_<FT>` when it
  owns rc fields, then a box dec — native's `emitFieldDropOnStack`) before writing
  the fresh inner literal (required by the eligibility gate, so the reused box
  solely owns it — rc=1, no alias-inc). Sound on cross because the donor is DEAD
  (nothing carried); sound on self-overwrite because override values may not
  reference `d` (existing gate → no read-after-overwrite hazard) and a carried
  nested-struct field moves with the box (freed once at exit via
  `__struct_drop_<T>`). Cross-*type* reuse stays scalar/array-only
  (`structs_reuse_compatible`'s per-position check rejects struct fields). Covered
  by `loop-nested-struct-field-*` (cross) and `loop-funcupdate-nested-struct-*`
  (self-overwrite): reuse fires (box 3 not 4), 5M-iter churn balanced.

  **Correction (2026-07-17, code audit):** the "REMAINING: enum / Map / closure
  / tuple pointer fields" claim this bullet used to end with was STALE — the
  widened `struct_fields_reusable_cross` (enum + string + Map + leak-safe
  tuple + leak-safe Option) already gates the **cross-statement**
  (`cross_reuse_sites`), **self-overwrite local** (`self_overwrite_reuse_sites`,
  with `donor_enum_fields_fresh` + per-kind override gates), and — as of the
  cross-block widening — **cross-block** (`xblock_scan_body`, sharing the
  `cross_recipient_fields_fresh` value gate) families. What GENUINELY remains
  vs native (which admits every field kind except string via
  `structReuseEligible`/`arrElemIsRcTracked`, uniformly across families):

  - **Closure fields.** The EXIT-drop prerequisite is now closed on the
    register backends: the clofld scan (the "clo:"-tagged half of
    `strfld_reclaim_ok_types_of` — fresh-lambda-only field values, no base
    copies, call-only reads) routes admitted fn-fielded types, and the
    `k_clo` / `fr_clo` arms shallow-free the field's env box (captures leak,
    the k_struct model); the `moves_fields` NODEEP walk exempts calls
    through a local's own fn fields — with the local's type resolved by
    the nesting-aware `fresh_struct_lit_type_deep`, so loop/if-nested
    declarations get the exemption too and the loop-REINIT re-bind routes
    through `__field_reclaim_<T>`'s `fr_clo` arm (the prior iteration's
    env box is reclaimed, no longer a per-iteration leak). Pinned by
    `TestSelfHostClofldDropIRX86_64` (the churn case asserts the reclaim
    call). The **REUSE admission (native's FuncType kind) is now closed**
    too: the coarse "fn" spelling reads as enum-like
    (`is_enum_like_name("fn")` is deliberately true), so the freshness
    walks test fn BEFORE their enum arm (`fn_field_value_is_fresh` — a
    lambda literal or its lifted `__mkclo$` spelling, required by
    `donor_enum_fields_fresh` / `cross_recipient_fields_fresh` / the
    self-overwrite override walk) and the enum-like release arm's shallow
    rc-guarded dec doubles as the k_clo env-box release. Cross /
    self-overwrite / enum-donor families fire (pinned by the
    `fn-field-*` reuse-differential cases + aliased-value exclusions); a
    donor whose own closure field is CALLED stays conservatively excluded
    by the general escape walk (method-shaped receiver use — same as
    every field kind). Still open: wasm has no k_clo/fr_clo drop arms.
  - **Rc-element arrays** (`string[]` / struct[] / enum[] fields): self-host
    admits only the leak-safe scalar arrays; native admits all. The
    **string[] EXIT-drop prerequisite is now closed**: the strarrfld scan
    (the "arr:"-tagged half of `strfld_reclaim_ok_types_of` —
    element-fresh stores, `.len()`-only reads) routes admitted types and
    the `k_str_arr` / `fr_str_arr` arms in both register backends'
    `__struct_drop` / `__field_reclaim` bodies deep-free the field via
    `__fern_str_arr_free` (pinned by the `strarr-field-*` cases in the
    string-field reclaim suites, x86 + arm64). The **string[] REUSE
    admission is now closed** too: `struct_fields_reusable_cross` admits
    string[] fields (the cross / cross-block / enum-donor / self-overwrite
    families), gated on element-fresh array-literal values on BOTH sides
    (`strarr_lit_all_elems_fresh` in `donor_enum_fields_fresh` — triggered
    via the widened `struct_has_enum_field` — plus
    `cross_recipient_fields_fresh` and the self-overwrite override walk);
    the reuse arms deep-free the superseded field via `__fern_str_arr_free`
    and the self-overwrite fresh arm rc-incs carried copies (pinned by the
    `strarr-field-*` reuse-differential cases + the aliased-value exclusion
    test). Struct[] / enum[] element arrays remain open (their element
    freshness has no literal-shaped proof yet), as does the own-param
    family (no bind literal to prove element freshness from).
  - **Own-param families** (`own_param_reuse_sites` /
    `own_param_self_overwrite_sites`): now on `struct_fields_reusable_param`
    (narrow ∪ Map / leak-safe tuple / leak-safe Option — the leak-only kinds,
    which need no release arm or freshness gate; pinned by the
    `own-param-donor-{map,tuple,opt}-field*` differential cases). Enum /
    string fields remain BLOCKED there — the alias-free release proof
    (`donor_enum_fields_fresh`) reads the donor's bind literal, which a
    parameter doesn't have. (Aside found while testing: a map-returning CALL
    as a struct-lit field value crashes on the self-host x86 path with reuse
    on OR off — a separate pre-existing bug; the reuse cases use ident-valued
    Map fields.)
  - **Enum-donor recipient** (`enum_donor_reuse_sites`): now on
    `struct_fields_reusable_cross` + the shared `cross_recipient_fields_fresh`
    value gate (pinned by the `enum-donor-{enum,tuple}-field-recipient*`
    differential cases). Sound because the donor's old slots are all scalars
    (no release needed) and the reused box's exit drop uses the same per-kind
    arms as a fresh construction (k_enum / k_struct; Map / tuple / Option
    leak-only).

These are marginal wins; whether to pursue the rest is a value call (see §7).
Validation for either: the reuse it changes is **on by default** (matching the
existing reuse), so correctness rests on the new differential churn e2e (reuse
result must match the interpreter byte-for-byte) **and** both fixpoint suites
staying green (the self-compile must remain byte-identical).

---

## 4. Risks & testing

- **Memory corruption** is the dominant risk. Mitigations: (a) the runtime
  `is_unique` guard makes a guarded reuse site degrade-safe — as of #4350
  slices 1–5 that covers **all five** emitter families, so a future
  escape-walk hole degrades to a fresh construction instead of corrupting;
  (b) each slice ships a
  differential e2e that stresses alloc/drop/reuse churn (the native
  `*reuse*/main.fern` cases) and compares self-host output to the interpreter
  **byte-for-byte**; (c) **both fixpoint suites must still converge** — reuse
  changes the emitted asm, so `TestSelfHostLoadFixpointX86_64` /
  `TestSelfHostModloadFixpointX86_64` prove the self-compiled compiler is still
  self-consistent; (d) the whole reuse layer can be switched off
  (`FERN_SELFHOST_NO_REUSE=1` → `irlower.reuse_layer_disabled()`, §6.5), and
  `TestSelfHostReuseDifferentialX86_64` asserts reuse-on vs reuse-off are
  observationally identical on firing shapes from every family.
- **Analysis divergence from native.** The self-host pairing must be *no more
  aggressive* than native's (conservative is safe — it only forgoes an
  optimisation). Reuse-on-self-host vs reuse-off-self-host must be
  observationally identical, exactly as native's reuse-on vs reuse-off gate
  asserts.
- **Fixpoint sensitivity.** Because the self-hosted compiler's own source is
  compiled *with* reuse on, a subtle bug surfaces as a fixpoint divergence (or
  a crash compiling the compiler). When bisecting such a failure,
  `FERN_SELFHOST_NO_REUSE=1` (§6.5) removes the whole reuse layer in one
  switch — if the divergence disappears, the regression is in a reuse
  pairing/emitter; if not, look elsewhere.

## 5. Open questions

- **Box-size / freelist-class parity.** Native's exact-fit `≤ 2048`,
  `(alloc+15)&-16` over `data + 8-byte rc header` must match the self-host
  freelist's class boundaries exactly, or `alloc_reuse` will hand back a
  wrong-class box. Confirm the self-host freelist uses the identical class
  function before Slice 0.
- **Where the pairing runs.** Native computes reuse sources per-function in the
  builder before lowering statements. The self-host equivalent is a pre-pass
  over each `FuncDecl` in `irlower` producing a C→D map threaded into
  `lower_stmt` — confirm the cleanest insertion point (alongside the existing
  precise-drop / aliased-names pre-passes).
- **Interaction with the existing `.with()` aliasing path.** Once general reuse
  lands, the "leak-y clone" fallback for aliased `.with()` can potentially be
  upgraded to refcount-guarded in-place reuse — a nice follow-up, out of scope
  for the initial port.

---

## 6. Implementation notes (verified from source)

Decoded from `internal/ir/ir.go` + `internal/codegen/x86_64/x86_64.go` +
`examples/self_host/asm_ir.fern`, resolving the §5 open questions so Slice 0/1
codegen is turn-key:

### 6.1 Native `__fern_alloc_reuse(token, tokenSize, size)` (`emitAllocReuseRuntime`)

Pure, no rc check of its own — the **caller** guarantees `token` is unique-or-null:

1. `token == 0` → tail-call `__fern_alloc(size)` (fresh).
2. else compare `class(tokenSize)` vs `class(size)`; **class match** → return
   `token` (reuse the block in place).
3. **mismatch** → `__fern_free(token)` (raw free by base), then fresh
   `__fern_alloc(size)`.

### 6.2 The call-site sequence (`emitReuseToken`) — the op recipe to mirror

For a construction C paired with dead owned local D (base = `D_data − 8`):

```
uniq  = __fern_rc_is_unique(D)          # i32, into a fresh local
if uniq { token = D_data − 8 }          # base pointer
else    { __fern_rc_dec(D); token = 0 } # shared → D's alias keeps the box
D_slot = 0                              # D consumed: zero its slot
base   = __alloc_reuse(token, D_alloc, C_alloc)   # box base, where OpAlloc's would sit
```

Then `emitReuseOldFieldDrops` deep-drops D's OLD pointer fields **gated on
`uniq`** (fresh box on the decline branch needs no drop), before C's stores
overwrite them. The self-host emits the identical `ir.Op` sequence
(`op_load_local` / `op_call_direct("__fern_rc_is_unique", 1)` / `op_if` /
`op_store_local` / …) — `irlower` already uses exactly these ops.

### 6.3 Self-host allocator specifics (differ from native — must adapt)

- **Freelist class is 8-byte-granular**: `__fern_alloc` rounds `(bytes+7)&−8`,
  then `class = bytes/8`, `< 65536`; `__fern_freelist` is 65536 × 8-byte
  buckets, an intrusive singly-linked free list (next-pointer at the block's
  first word). Native's class is `(bytes+15)&−16`. **The self-host
  `__fern_alloc_reuse` must class by `bytes/8`** (two boxes reuse iff
  `size1/8 == size2/8`, i.e. equal when both 8-aligned).
- **No standalone `__fern_free`**: the freelist-push lives inside the rc-guarded
  `__fn___fern_arr_dec` (push at rc==1). Slice 0 must factor a raw
  `class`-indexed push (`freelist[size/8] = block; block[0] = old_head`) out of
  that path — the mismatch branch frees a **known-unique** block, so it can push
  directly without the rc guard.
- **Box base / header**: struct boxes are `[rc@base, field0@base+8, …]` (rc is
  the 32-bit word at `data−8`, matching `__fern_rc_is_unique`'s `movl -8(%rcx)`),
  so `token = data − 8` and `alloc size = 8 + nfields*8`. (Array boxes differ —
  `[cap, rc, len, e…]`, base = `data−16` — which is why the first slice targets
  **structs only**; tuples/enums come later with their own base math.)

### 6.4 The invasive point: `op_struct_make`

The self-host lowers `T{…}` via a single `op_struct_make(type_name, ndecl)`
(irlower ~L4351) that the backends expand to alloc + rc-init + field stores.
Reuse must divert the ALLOC half only. Two options:

- **(a) inline at the reuse site** in `irlower`: when the self-overwrite /
  pairing fires, emit the §6.2 recipe + explicit `op_struct_set` field stores
  instead of `op_struct_make`, so `op_struct_make` itself is untouched
  (backend-agnostic; keeps the risky change in one file). **Recommended for
  Slice 1.**
- **(b) a reuse-aware `op_struct_make_reuse`** carrying a token operand — cleaner
  long-term but touches every backend's struct-make handler. Defer.

### 6.5 Gate — SHIPPED (env-var switch + differential suite)

Historical note: the original plan here ("`reuse_enabled` on `EmitState`,
default false, flipped by one final PR") predates how the port actually
landed — the reuse families shipped **on by default**, each gated by its own
detector/corruption-probe/fixpoint coverage, and (as of #4350) each carrying
the runtime `is_unique` token guard.

The reuse-on/off switch exists as `irlower.reuse_layer_disabled()`: setting
**`FERN_SELFHOST_NO_REUSE=1`** in the compiler's environment empties every
donor-based pairing (self-overwrite / cross-struct / cross-tuple / enum-donor
/ enum-cross / in-arm — the site lists stay empty, so their donor-free
suppressions never fire either) and drops the ENUMRE in-place reassign
upgrade back to `emit_enum_reclaim_store`'s free+alloc — mirroring native's
`ast.RcReuseEnabled=false`. The differential contract ("reuse-on vs reuse-off
must be observationally identical") is enforced by
`TestSelfHostReuseDifferentialX86_64`
(`internal/e2eselfhost/self_host_reuse_differential_test.go`): each firing
shape from every family compiles both ways, the switch is proven live (asm
differs; `alloc_reuse` present only on the ON side), and both binaries must
exit identically (detector cases pin leak-freedom on both sides).

---

## 7. Field-kind status & the exit-reclaim dependency (2026-07)

Constructor reuse now covers, on **both** the cross-statement
(`emit_cross_struct_reuse`) and functional-update self-overwrite
(`emit_self_overwrite_reuse`) paths, structs whose every field is:

- a flat scalar (`reuse_field_is_scalar`),
- a leak-safe rc array (`is_leaksafe_array_field` — `i32[]`/`boolean[]`/`i64[]`/`f64[]`),
- or a **direct nested-struct** pointer field (`inner: Inner`, `decl_is_struct`).

All gated by the single `struct_fields_reusable` predicate; an overwritten
nested-struct field's old inner box is released with a full freeing drop
(`__struct_drop_<FT>` when it owns rc fields, then a box dec) before the fresh
inner is written. Coverage: `internal/e2e/self_host_loop_reuse_ir_test.go`
`loop-nested-struct-field-*` (cross) and `loop-funcupdate-nested-struct-*`
(self-overwrite) — reuse fires (box 3 not 4) and 5M-iter churn stays balanced.

### Direct enum field — exit-reclaim LANDED on the register backends (#4297 A2)

A struct with a **direct enum field** (`Tagged { e: Shape, n: i32 }`) is now in
the exit-drop set on the register backends (x86-64 + arm64): `decl_is_enum` admits
it to `struct_has_reclaim_array_field`, `emit_ir_struct_drop_one` / `emit_arm64_struct_drop_one`
gained a `k_enum` arm that SHALLOW-frees the enum box via `__fern_arr_dec` (one
level — the variant payload leaks, matching the shallow `k_struct` gap), and the
ExprStructLit lowering alias-incs a NON-fresh enum field (a fresh variant ctor
`V(args)`/`V` is sole-owned, handed over with no inc). Enum boxes are rc-headered
(`op_struct_make`), so the inc/dec are sound. Pinned by
`TestSelfHostStructEnumFieldReclaimIR{X86_64,Arm64}` (fresh-gate churn, aliased,
base-copy, enum-alongside-array). Both x86 fixpoint suites stay byte-identical.

**wasm is deliberately NOT included and is UNCHANGED by this slice.** The wasm-IR
path does not use irlower's op-based construction (`emit_expr` in `wasm.fern`
emits struct literals directly) nor `emit_struct_field_drops` — it reclaims via a
SEPARATE `$__fern_release_<T>` family (`struct_enum_drop_helpers` /
`struct_release_field_inner`) with its own construction/release balance. So the
irlower Sites above emit ZERO ops on the wasm path (verified: no `rc_inc` in the
emitted WAT), leaving the pre-existing wasm enum-field leak exactly as-is (safe,
no double-free). Closing it is a SEPARATE follow-up in the `$__fern_release`
subsystem: `struct_release_field_inner` (wasm.fern:1378) already has enum-field
handling but `is_enum_type_name` keys off `cx.mr_types` (method-having types), so a
method-less enum field is misclassified as scalar; and the wasm construction side
would need a matching enum-field inc in `emit_expr` (both the override and
base-copy paths) or the `$__fern_release` recursion double-frees a base-copied
enum field. Do NOT reuse the register-backend irlower incs for it — they don't run
on the wasm path.

### The blocker for the remaining kinds (tuple / Map / closure fields)

These are **not** admissible yet, and the blocker is NOT reuse — it is
**struct-field exit-reclaim**. A struct with a direct tuple/Map/closure
field is not in the exit-drop set today (`struct_has_reclaim_array_field`
excludes them; it admits only leak-safe arrays, struct/enum ARRAYS, direct
nested-structs, and — since #4297 A2 — direct enum fields on the register
backends). Consequences if reuse admitted such a kind without exit-reclaim:

- reuse would release the *old* field on overwrite but the *new* field would
  never be dropped when the reused box is finally freed → a **per-iteration
  leak**, so the churn e2e (5M iters) would exhaust the heap and fail.

So each remaining field kind requires its exit-reclaim to land FIRST (extend
`struct_has_reclaim_array_field` + `emit_struct_field_drops` + each backend's
`__struct_drop` k-arm for the kind). That is the **A2 struct-field-reclaim**
work (issue #4297; `docs` #4314 root-cause; #4322 landed the `string`-field
case). It is memory-safety-critical and has been OOM-runaway-prone (see the
per-module note below), so the reuse layer deliberately waits on it rather than
editing it in parallel.

### Re-entry recipe (once a kind's exit-reclaim lands)

Mechanically identical to the nested-struct slice — for field kind K:

1. widen `struct_fields_reusable` to admit K (e.g. `decl_is_enum` / a tuple
   predicate), only after `struct_has_reclaim_array_field` reclaims K at exit;
2. add K's fresh-literal eligibility gate in `cross_reuse_sites` and
   `self_overwrite_reuse_sites` (so the reused box solely owns the new value);
3. add K's full-freeing-drop branch in both `emit_cross_struct_reuse` and
   `emit_self_overwrite_reuse` (K's per-type drop helper + a box dec), mirroring
   the nested-struct branch;
4. add the `loop-<K>-field-{reuse,donor-live,churn-safe}` test trio, and keep
   both x86-64 fixpoint suites byte-identical.

### Related: the per-module emit OOM (`module 9`, exit 137)

`TestSelfHostModloadPerModuleWholeCompilerX86_64` OOM-kills (exit 137) on
`module 9`'s isolated `-per-module-emit`. Reproduces on clean `main` (not
PR-specific), deterministically at ~24 GB local (16 GB RAM + 8 GB swap); standard
16 GB CI runners hit it under concurrent shard load, so
`test-e2e-selfhost-x86_64-shard9` flakes on every PR (non-blocking — the merge
API accepts a PR with it red). The arm64 sibling and the whole-compiler fixpoint
both pass, so it is specifically the per-module path peaking over runner memory —
the same class as the #4314 "per-call leak that OOMs irlower's emit" root cause,
i.e. part of the A2 track's surface, not the reuse layer's.
