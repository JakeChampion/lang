# Porting Perceus RC to the self-hosted compiler

> **Open follow-ups tracked in GitHub:** the goal-2 port slices
> [#4351](https://github.com/JakeChampion/lang/issues/4351)–[#4357](https://github.com/JakeChampion/lang/issues/4357),
> [#4399](https://github.com/JakeChampion/lang/issues/4399) (escape-taint
> retirement) and [#4402](https://github.com/JakeChampion/lang/issues/4402)
> (missing optimisations). The old coarse tracker #2857 is closed.
> This doc is a living progress log — verify the latest slice before picking up
> an item.

Status: **design + implementation tracker** (started 2026-06-07).
Goal: bring the native compiler's Perceus reference-counting +
compile-time memory optimisation to the self-hosted Fern compiler
(`examples/self_host/`), at feature parity. This document is the
roadmap; it mirrors `docs/RC-PERCEUS-PLAN.md` (the native rollout,
Phases 0–6 + Perceus, all shipped) and `docs/OWNERSHIP-INFERENCE-PLAN.md`,
mapping each native piece onto the self-host architecture.

A **direct port** is the explicit strategy: reproduce the native
algorithm step-for-step so we inherit its correctness story and can
diff behaviour against the native compiler. The native side is the
source of truth.

---

## 1. Where the two compilers stand today

### Native (Go) — fully implemented

Perceus in the native compiler lives almost entirely in the
target-agnostic IR-lowering builder, `internal/ir/ir.go` (~16k LOC),
plus reuse/TRMC passes (`internal/ir/trmc.go`,
`internal/ir/insert_resource_drops.go`). It has **no dedicated RC
opcodes** — RC is realised as `OpCallDirect` to named runtime helpers
(`__fern_rc_inc`, `__fern_rc_dec`, `__fern_rc_is_unique`,
`__fern_box_free`, `__fern_alloc_reuse`, generated `__drop_*` handlers)
emitted during lowering, guided by a set of pure static analyses. The
runtime helpers are emitted per backend
(`internal/codegen/arm64/arm64.go`, `internal/codegen/x86_64/x86_64.go`,
`internal/codegen/wasmbin/runtime.go`).

Native heap layout (the shape we must reach): rc at `[data-8]`, with
type-specific words below it (array `cap` at `[data-12]`, `len` at
`[data-4]`). Static/immortal cells carry the sentinel `0x80000000` in
the rc word; inc/dec short-circuit on it.

### Self-host (Fern) — no RC at all

The production self-host path is **AST → asm, directly**, with no IR:

```
source → lexer.tokenize → parser.parse_module
       → flatten.bundle → parser.module_with_builtins   (AST passes)
       → asm.emit_module / asm_arm64.emit_module / wasm.emit_module
```

- Shared frontend: `examples/self_host/asmcore.fern` — the `Ty` type
  system, `infer_expr_type`, `EmitState` + its state methods, the
  free-variable walker, and the pre-codegen `check_module`. CLAUDE.md:
  edit it **once**; it is not mirrored in the backends.
- Per-backend emit (instruction selection + runtime-helper bodies):
  `asm.fern` (x86-64), `asm_arm64.fern` (arm64), `wasm.fern` (wasm).
- The AST-pass hub is `parser.module_with_builtins`
  (`parser.fern:4467`): hoist local funcs → insert resource drops →
  lower defers → monomorphise funcs → monomorphise structs → inject
  builtin enums. **A Perceus AST analysis pass slots in here**, after
  monomorphisation (so it sees concrete types).

The self-host has **no rc slot in any heap layout** and the RC
intrinsics are no-op stubs (a leak-everything bump heap):

| Kind | Self-host layout today | Returned ptr | rc? |
|---|---|---|---|
| Array | `[cap, len, e0, e1, …]` | base+8 (at `len`) | none (`cap` at `[data-8]`) |
| String | `[data_ptr, len]` (16 B) | base | none |
| Tuple | `[v0, v1, …]` | base | none |
| Struct | `[shape_ptr, f0, f1, …]` | base | none |
| Enum | `[shape_ptr/tag, payload]` | base | none |
| Closure | `[fn_addr, cap0, cap1, …]` | base | none |

Stub bodies: `asm.fern:4947-4955`, `asm_arm64.fern:4673-4681`
(`__fern_rc_inc` returns its arg, `__fern_arr_dec` /
`__fern_drop_arr_ptr` / `__free` are bare `ret`). The wasm backend has
no RC machinery yet.

The only RC-adjacent code that exists is the **static `own`/move
checker** in `checker.fern:3387-3640` (E050/E051) — diagnostics, not
codegen. (Note: the asm path uses `asmcore.check_module`, a lighter
checker, not `checker.fern`.) `ParamDecl.own` is already parsed
(`parser.fern:174`).

### Consequence

A faithful port is essentially **re-running native Phases 0–6 +
Perceus on an rc-less base layout**, in the self-host's value-semantic
emit style, across two hand-maintained backends — gated by the
byte-identical self-bootstrap fixed-point. It is large and must be
landed in green, bounded slices.

---

## 1b. Reconciliation — 2026-06-29 (the Perceus target has MOVED to the IR path)

> **§1 above describes the AST→asm world and is now historical.** Two
> things changed since this doc began (2026-06-07):
>
> 1. **A self-host IR path was built and is now the DEFAULT.** The
>    pipeline `irlower.fern → asm_ir.fern (x86-64) / asm_arm64_ir.fern
>    (arm64) / wasm_ir.fern (wasm)` lowers every non-async
>    `examples/tests/*_test.fern` module (goal 1 of CLAUDE.md, completed
>    by the `map_verbs` flip, #4026). The legacy AST→asm emitters
>    (`asm.fern` / `asm_arm64.fern` / `wasm.fern`) are now reached **only**
>    by the parallel-owned async modules.
> 2. **The early RC work logged below (2026-06-07 … -22) targeted the
>    AST path (`asm.fern` etc.).** That code still exists but is on the
>    way out with the AST backends. **The Perceus port now means: complete
>    RC on the IR path**, where a *partial* port already exists.

### Current IR-path Perceus state (survey, 2026-06-29)

- **x86-64 (`asm_ir.fern` + `irlower.fern`): substantial.** alias-inc at
  aliasing sites; function-exit dec sweep (`emit_dec_sweep_except`);
  COW-guarded reassign dec (`emit_arr_reassign`); snapshot-param reclaim
  (`emit_snapshot_store` → `__fern_snapshot_dec`); per-type
  `__field_reclaim_<T>` (replaced array-field buffers) and
  `__struct_drop_<T>` (deep-drop rc-array fields, sole-owner gated) with
  **real bodies**; conservative precise-drops (array-literal locals only);
  rc header at `[data-8]` (uniform across backends, `asmcore.fern:802`).
- **arm64 (`asm_arm64.fern`): real bodies (slice 1a/1b shipped).** Both
  `__struct_drop_<T>` (deep-drop rc-array fields) and `__field_reclaim_<T>`
  (replaced-field reclaim) emit real bodies, at parity with x86-64.
- **wasm (`wasm.fern` IR path): real bodies (slice 1c/1d shipped).**
  `__struct_drop_<T>` deep-drops a struct's rc-array fields at the IR struct
  layout offset (`8 + i*8`) — a scalar-element array via `__fern_arr_dec`, a
  pointer-element array via `__fern_arr_dec_ptr` (so its element boxes are
  released too). `__field_reclaim_<T>` (consume-rebind replaced-field reclaim)
  frees the superseded rc-array field buffers (cow + snapshot guarded) then the
  old box via `__fern_snapshot_dec` — itself now real (frees the uniquely-owned
  `old`, never decrementing a shared/borrowed box). Slice-1 reclaim parity with
  x86-64 and arm64 is **complete** on all three IR backends.
- **Deferred on the IR path (all backends):** string RC beyond the landed
  slices (boxes are rc-headered since #4294; fresh string LOCALS, struct
  string FIELDS, and fresh-element `string[]` ELEMENT reclaim via
  `__fern_str_arr_free` are live — see the 2026-07-03 log entry; enum/Option
  **string payloads** still leak), closure-env reclamation,
  `Option`/`Result` payload release, tuple-element release, map element
  (key/value) RC, struct/enum enum-payload fields, reuse beyond the landed
  slices, full drop-on-last-use.

### LowerState RC/ownership fields (the analysis substrate that exists)

`reclaimable_names` (escaping locals → struct dec), `aliased_names`
(lasting array/map aliases → clone-not-mutate), `n_params` (borrow
boundary — params < n_params are never swept), `local_is_arr`
(drives alias-inc), plus the `*_ret_fns` move/type registries. Borrow
inference (`borrowable_params_interproc`, the self-host `inferParamEscapes`) is
a **greatest-fixpoint from above** as of slice 2 — at parity with native's
algorithm; it feeds `reclaimable_names_of` / snapshot / precise-drop gating.

### Remaining slices to native parity (dependency-ordered)

Correctness first (parity), then optimisation. Each slice ships green:
byte-identical fixpoint + the differential gate + a **new RC-specific
test** (rc-count / no-leak assertion — the self-host suite has none yet,
so adding the harness is part of slice 1). arm64/wasm legs run in CI.

1. **arm64 + wasm real `__field_reclaim_<T>` + `__struct_drop_<T>`
   bodies** — transcribe the x86-64 bodies. Closes the live
   struct-array-field leak on two of three backends. *Medium; highest
   value first; establishes the RC-test harness.* **DONE — 1a (arm64
   `__struct_drop`), 1b (arm64 `__field_reclaim`), 1c (wasm
   `__struct_drop`), 1d (wasm `__field_reclaim` + `__fern_snapshot_dec`).
   Reclaim parity across all three IR backends.**
2. **Borrow inference (`infer_param_escapes`) on the IR path** — borrowed
   params skip the dec; gates everything below. *Medium.* **DONE —
   `borrowable_params_interproc` flipped from a least-fixpoint-from-below to
   native's greatest-fixpoint-from-above, recovering mutually-recursive borrows
   for the downstream reclaim. Behaviour-neutral on the existing corpus
   (byte-identical bootstrap/fixpoint); capability-additive for cyclic borrows.**
3. **Struct/enum drop specialisation for NON-array rc fields** (string,
   nested struct, enum payload) — extend the deep-drop walk. *Deep.*
   **DONE (nested-struct, all 3 backends) — 3a x86 (#4068), 3b/3c arm64+wasm
   (#4070).** A DIRECT nested-struct field (`Outer { inner: Inner }`) is
   SHALLOW-reclaimed (`__fern_arr_dec` on the inner box at field offset `8+i*8`,
   one level like a struct-array element), gated into the reclaim set via
   `struct_has_reclaim_array_field`, balanced by a construction alias-inc
   (double-free-proof: a fresh literal is sole-owned → no inc; every other source
   is retained). The IR round-trip eval models the reclaim helpers as
   value-preserving. **Follow-ups (deferred):** (a) tighten the construction
   no-inc set from fresh-LITERAL-only to fresh-RETURNING calls (needs threading
   `fresh_struct_ret_fns` into `LowerState`) so the move / fresh-call cases
   reclaim instead of leak; (b) DEEP-drop the inner's own rc fields (recursive
   `__struct_drop_<Inner>` + transitive need-set closure) — the one-level gap,
   shared with struct-array elements; (c) STRING fields (blocked on slice 7,
   header-less IR strings) and ENUM-payload fields (needs variant-tag dispatch).
4. **`Option`/`Result` payload + tuple-element release.** *Medium.*
5. **Closure-env reclamation** on the IR path (capture rc-tracking + env
   drop; the AST path shipped this in 2 slices — see the wasm
   closure-env log entries below). *Medium-deep.*
6. **Map element (key/value) RC.** *Medium.*
7. **String RC on the IR path** (two-word arm64/wasm; x86-64 inline-tag
   SSO). *Deep.*
8. **Reuse / FBIP** (`compute_reuse_sources` + reuse tokens) — pure
   optimisation, last. *Deep.*
9. **Full precise-drop** beyond the conservative array-literal cut.
   *Deep.*

### Slice 3 implementation plan (scoped 2026-06-29 — the NEXT increment)

Survey conclusion (correcting the one-line slice-3 entry above): the three
NON-array rc field kinds are **not** equal-difficulty, and string is blocked:

- **String fields — BLOCKED, defer.** IR string boxes are header-less
  (`irlower.fern:~11318`: "an rc-dec would corrupt the adjacent block"), so a
  string field cannot carry an rc word to dec. Releasing it needs the separate
  **string-RC** initiative (slice 7: add an rc header to string boxes, or a side
  table) — a deep refactor, NOT part of slice 3.
- **Enum-payload fields — defer.** Need variant-tag dispatch ("Stage C"):
  read the tag, branch per variant, deep-drop that variant's payload. Orthogonal
  to struct-field release and more complex; a later sub-slice.
- **Nested-struct fields (`Outer { inner: Inner }`) — the slice-3 target.**
  These are ALREADY IR-eligible in **leak mode** (`decl_is_leaksafe_d`,
  `irlower.fern:1606` admits a struct-typed field whose struct is itself
  leak-safe); the nested box (and its rc-array fields) simply leak because the
  outer `__struct_drop_<Outer>` does not recurse into it. So slice 3 is **not** a
  routing/eligibility change (low fixpoint risk) — it is additive reclaim, like
  slice 1c.

**The work (nested-struct), in dependency order:**

1. **Construction-inc (shared, `irlower.fern` struct-literal lowering ~3936-4180)
   — the soundness-critical part.** A nested-struct field flows through the
   GENERIC field store today (no inc, no aliasing bail). Recursive drop is only
   sound if the outer OWNS the field, so mirror the array-field `fav_alias_inc`
   machinery for `decl_is_struct(cfft)` fields across EVERY source form: fresh
   `Inner{…}` literal (move-in, NO inc), aliased bare ident (`Outer{inner: x}` →
   Perceus dup), field-copy (`Outer{inner: o.inner}` → dup), fresh-returning call
   (`Outer{inner: mk()}` → no inc), and the `{...base}` update idiom. Getting any
   form wrong = double-free / UAF (the array-field code is littered with exactly
   these warnings — e.g. the `reset_locals` self-build UAF). This is the bulk of
   the risk and must be adversarially tested (over-release detector + value
   read-back on every form).
2. **Recursive drop (per-backend, 3×).** In `emit_ir_struct_drop_one`
   (`asm_ir.fern`), `emit_arm64_struct_drop_one` (`asm_arm64.fern`),
   `emit_wasm_struct_drop_body` (`wasm.fern`): for each nested-struct field, load
   the field pointer (IR offset `8 + i*8` on wasm; `(i+1)*8` register) and call
   `__struct_drop_<Inner>` (rc-guarded, null-guarded — the body is itself
   rc==1-gated, so an aliased rc>1 inner just decs). Mirror the AST path's
   `struct_release_field_inner` recursion (`wasm.fern:1252`,
   `$__fern_release_<ft>`).
3. **Transitive emission (per-backend need-set).** `__struct_drop_<Outer>` now
   CALLS `__struct_drop_<Inner>` from its hand-emitted body — not a lowered op —
   so `struct_drop_types` (`wasm_ir.fern`) / the `struct_drop:<T>` need-set
   (`asm_ir.fern`) must be CLOSED under "Outer has a nested-struct field of type
   Inner ⇒ also emit Inner's drop", or the call is undefined at link.
4. **Validate** at each step on x86 first (bootstrap + fixpoint + differential +
   a new nested-struct memory-differential test: a churn of `Outer{inner:
   Inner{items:[…]}}` exit-reclaimed → bounded vs leak → heap-exhaust 137, plus a
   `__fern_rc_underflow_count()==0` over-release case on every construction form),
   THEN mirror the per-backend drop to arm64/wasm. Ship as its own PR.

This is roughly slice 1c+1d combined in size and touches the most UAF-prone code
in the lowering, so it warrants fresh-focus execution and adversarial
double-free testing — not a tail-end-of-session rush.

### Strategy notes

- **Direct port** stands: native is the source of truth, all analyses
  read the AST, so they belong in `asmcore.fern` shared by all three
  IR backends; only emission differs per backend.
- **Emission de-duplication** is the architectural lever — the three IR
  backends each hand-code rc-op emission today; a shared `RcOp` directive
  layer (cf. `RC-PERCEUS-SELF-HOST-IR.md`) prevents triplication drift as
  slices land.

**Recommended next: slice 2** (borrow inference / `infer_param_escapes` on the
IR path — borrowed params skip the dec; gates slices 3–9). Slice 1 (reclaim
parity) is complete on all three IR backends.

---

## 2. Architecture mapping (native → self-host)

| Native (Go) | Self-host (Fern) | Notes |
|---|---|---|
| `builder` struct fields (mutable) | `EmitState` (immutable, threaded) | analyses become fields on a per-function analysis record |
| `ir.LowerWith` pipeline | `parser.module_with_builtins` + `emit_module` | analyses run as an AST pass / per-function precompute |
| `computeFreeEligible` (`ir.go:2574`) | `asmcore.fern` (shared) | pure AST analysis |
| `inferParamEscapes` (`ir.go:1336`) | `asmcore.fern` (shared) | call-graph fixpoint |
| `computeMovedLocals` / `markConstructionMoves` (`ir.go:3759/3903`) | `asmcore.fern` (shared) | move-on-return/alias/construction/destructure |
| `computePreciseDrops` (`ir.go:3294`) + helpers | `asmcore.fern` (shared) | last-use drop placement |
| `computeReuseSources` (`ir.go:12119`) | `asmcore.fern` (shared) | FBIP reuse pairing |
| `computeConsumedParams` (`ir.go:14536`) | `asmcore.fern` (shared) | owned-param promotion |
| `findTrmcFuncs` (`trmc.go`) | `asmcore.fern` (shared) | TRMC classification |
| `needsRcIncOnAlias` (`ir.go:14788`) | `asmcore.fern` predicate | the shared inc trigger |
| `emitAliasInc` (`ir.go:14771`) | per-backend `emit_*` | emits the helper call |
| exit-dec sweep `emitRcDecLocalsAtExit*` (`ir.go:3264/4004`) | per-backend `emit_function` | dec every rc-tracked local/param |
| typed drop generators `genStructDropFn`/`genEnumDropFn`/… (`ir.go:5767+`) | per-backend `emit_runtime` | generated `__drop_*` bodies |
| reuse-token emit `emitReuseToken` (`ir.go:12510`) | per-backend `emit_*` | `__alloc_reuse` call |
| runtime helpers `emitRcIncRuntime` etc. (`arm64.go`/`x86_64.go`/`runtime.go`) | per-backend `emit_runtime` | the `__fern_rc_*` bodies |

**Rule of thumb:** every *decision* (where to inc/dec/reuse/drop) is
target-independent → `asmcore.fern`. Every *emission* (the actual asm
sequence) → each backend's `emit_*`, duplicated like the existing
`__fern_alloc` call sites.

Because `EmitState` is immutable and threaded, the analyses are best
computed **once per function** and carried as parallel arrays keyed by
local-slot index / AST node identity, rather than mutated in place. The
self-host already does this style (e.g. `local_names`/`local_types`).

---

## 3. Heap-layout migration (the self-host "Phase 0")

The native rc lives at `[data-8]`. The self-host need not match native
byte-for-byte (it emits its own asm), only be **internally
consistent** — every alloc site and every field/element offset in
`asm.fern` + `asm_arm64.fern` + `wasm.fern` must agree. Target layouts
(chosen to disturb existing offsets minimally and to keep `len` where
readers already expect it):

| Kind | New layout (base →) | Returned ptr | rc at | other |
|---|---|---|---|---|
| Array | `[rc, cap, len, e0, …]` | base+16 (`len` − keep `arr[i]`=`(i+1)*8`? see note) | `[data-16]` | cap `[data-8]`, len `[data]` |
| String | `[rc, _, data_ptr, len]` | base+16 | `[data-16]` | data `[data]`, len `[data+8]` |
| Tuple | `[rc, _, v0, …]` | base+16 | `[data-16]` | v_i at `[data + i*8]` |
| Struct | `[rc, _, shape_ptr, f0, …]` | base+16 | `[data-16]` | shape `[data]`, field i at `[data+(i+1)*8]` |
| Enum | `[rc, _, shape/tag, payload]` | base+16 | `[data-16]` | unchanged field offsets |
| Closure | `[rc, _, fn_addr, cap0, …]` | base+16 | `[data-16]` | fn at `[data]` |

Note: the **cleaner** alternative is to mirror native exactly (rc at
`[data-8]`), shifting the returned pointer past a single rc word and
keeping cap below it for arrays. The decision (16-byte vs 8-byte
header, where `_`/pad goes) is finalised in slice 0a; what matters is
that the migration is **atomic per category** and the self-bootstrap
stays byte-identical (the emitter compiles its own source, so the
change is self-consistent as long as it is deterministic).

Migration tactic: introduce **named offset helpers** in `asmcore.fern`
(e.g. `arr_data_off()`, `arr_rc_off()`, `arr_len_off()`,
`struct_field_off(i)`, …) and route every backend offset literal
through them, so the layout is defined once and the two backends can't
drift. This is itself a valuable refactor independent of RC.

Sentinel: string literals in `.rodata`, the empty-array/empty-string
sentinels, and shape pointers are immortal → write `0x80000000` into
their rc word (or keep them out of the heap and have inc/dec
short-circuit on the low-address / sentinel guard, exactly as native
does).

---

## 4. Runtime-helper contract (per backend)

Replace the no-op stubs with real bodies. The contract mirrors
`arm64.go` (the cleanest native reference). Each must carry the guard
chain: **null → low-address (`<0x10000`, treats a non-pointer scalar
in an rc slot as "not a heap object") → SSO inline-tag (strings) →
static sentinel (high bit)**, before touching the rc word.

- `__fern_rc_inc(ptr) → ptr` — guards; `rc++`. (`arm64.go:1188`)
- `__fern_rc_dec(ptr)` — guards; underflow detector (rc≤0 bumps
  `__fern_rc_underflow`); `rc--`. **No free here** (Phase 1).
  (`arm64.go:1228`)
- `__fern_rc_is_unique(ptr) → i32` — 1 iff live & rc==1. The universal
  soundness second-net. (`arm64.go:1739`)
- `__fern_str_inc` / `__fern_str_dec` — two-word `(data,len)` string
  variants (only if the self-host adopts the two-word string ABI;
  otherwise the boxed-string path reuses `__fern_rc_*`).
- `__fern_box_free(data, size) → data` — free `base`, return data.
  (`arm64.go:998`)
- `__fern_arr_dec(data, stride)` — size-aware array buffer free at
  rc==1; does not walk elements. (`arm64.go:884`)
- `__fern_closure_drop(f) → f` — free rc1 block at rc==1, else dec.
  (`arm64.go:1032`)
- `__fern_alloc_rc1(size) → data` (rc=1 header),
  `__fern_alloc_box(size) → data` (sentinel header).
- `__fern_alloc_reuse(token, tokenSize, size) → ptr` — null token →
  `__fern_alloc`; class match → reuse; else free token + alloc.
  (`arm64.go:844`)
- Real allocator: per-size-class freelists + bump for large
  (`__fern_alloc` / `__fern_free`). Until Phase 3, `__fern_free` may
  stay a no-op (safe leak), so rc reaches 0 with no reclamation.

The wasm backend mirrors these in linear memory (see
`runtime.go:1199+` for `__fern_alloc`, `:1740` for dec) and needs a
needs-set dependency tracker like the native one (`EmitState.needed`
already exists — extend `mark_*`).

---

## 5. The Perceus analyses to port (shared, `asmcore.fern`)

Each is a pure function over the (monomorphised) AST + the type
oracle (`infer_expr_type`). Port in dependency order:

1. **`needs_rc_inc_on_alias(e, s) → bool`** ← `needsRcIncOnAlias`
   (`ir.go:14788`). Fires on Ident/FieldAccess/Index whose `Ty` is
   array/struct/enum/closure/tuple/string. The single inc trigger.
2. **`infer_param_escapes(module) → map`** ← `inferParamEscapes`
   (`ir.go:1336`). Greatest-fixpoint call-graph escape analysis;
   `param_borrowable(fn,i) = !escapes[fn][i]`.
3. **`compute_free_eligible(fn) → set`** ← `computeFreeEligible`
   (`ir.go:2574`). Taint analysis: borrowed params start tainted;
   locals tainted if they escape into a retain sink or alias a tainted
   local (fixpoint). Untainted rc locals + own params → free-eligible.
4. **`compute_consumed_params(fn) → set`** ← `computeConsumedParams`
   (`ir.go:14536`). Promote reassigned, drop-wired struct/tuple params
   to owned (the O(N²) self-reassign fix) + entry-inc.
5. **`compute_moved_locals(fn) → set`** ← `computeMovedLocals` +
   `markConstructionMoves` (`ir.go:3759/3903`). Move-on-return /
   -alias / -construction / -destructure pair cancellation, with the
   last-occurrence + dominance (top-level stmt, no preceding return)
   guards.
6. **`compute_precise_drops(fn) → map[stmtIdx → names]`** ←
   `computePreciseDrops` (`ir.go:3294`) + `safe_for_control_flow_drop`,
   `precise_droppable_type`, `flows_into_uncounted_alias`,
   `init_may_alias_live`. Last-use drop placement.
7. **`compute_reuse_sources(fn) → (map, set)`** ← `computeReuseSources`
   (`ir.go:12119`) + `reuse_class_of`. FBIP reuse-token pairing
   (dead owned box D ↔ same-class construction C).
8. **TRMC**: `find_trmc_funcs` ← `trmc.go`. Tail-recursion-modulo-cons,
   the consume-safe subset.

All eight are target-independent and depend only on the AST + `Ty`.
They produce per-function side-tables consumed by the per-backend
emit. Soundness rests on the **safe-leak invariant** (every
conservative bail degrades to dec-without-free, never over-release) and
the runtime **`__fern_rc_is_unique` second net**.

---

## 6. Phased rollout (each slice green + tested)

Mirrors the native plan; ordered so the tree (incl. the byte-identical
self-bootstrap) stays green between slices.

- **Phase 0 — layout + runtime.**
  - 0a: offset-helper refactor in `asmcore.fern`; route all backend
    offsets through it (no behaviour change). Gate: every self-host
    test + fixpoint still green.
  - 0b: rc-header layout migration, per category (array first), all
    allocs write `rc=1` (or sentinel for immortal). Still no inc/dec.
  - 0c: real runtime helpers (`__fern_rc_inc/dec/is_unique`,
    `__fern_box_free`, `__fern_arr_dec`, `__fern_alloc_rc1/box`) +
    underflow detector. `__fern_free` stays a no-op (safe leak).
- **Phase 1 — inc/dec everywhere (no Perceus, no free).**
  - 1d: arrays — inc at the 6 reference-creation sites, dec at exit /
    overwrite. Port `needs_rc_inc_on_alias` + `emit_alias_inc` +
    exit-dec sweep for `ArrayType`.
  - 1e: widen to string/struct/enum/closure/tuple + per-type drop
    handlers (`__drop_struct_*`, `__drop_enum_*`, `__drop_tuple_*`,
    `__fern_drop_arr_ptr`, closure drop thunk).
- **Phase 2 — rc check in mutating ops (CoW).** `arr.push` rc==1 fast
  path (the self-host already grows in place — add the rc gate),
  `arr[i]=v` CoW, Map set/delete/clear CoW + borrowed-param model.
- **Phase 3 — real allocator + free.** Size-class freelist; flip
  `__fern_free` + the rc==0 free path on, gated on a corpus-wide green
  underflow detector (drift audit first, exactly as native).
- **Phase 4 — Perceus pair-cancellation.** Move-on-return/alias/
  construction/destructure (port `compute_moved_locals`); precise
  drops (port `compute_precise_drops`, straight-line then
  control-flow-aware).
- **Phase 5 — drop reuse (FBIP) + borrowed params.** Reuse tokens
  (`compute_reuse_sources` + `__alloc_reuse`), consuming match
  (C1/C2), TRMC. Borrow inference (`infer_param_escapes`).
- **Phase 6 — reclamation polish.** Per-type reuse-path payload frees,
  nested/generic shapes, statement temporaries — the native Phase-6
  bullet list.

---

## 7. Test strategy

Mirror the native nets in the self-host's Go-side e2e harness
(`internal/e2e/self_host_*_test.go`, driven through `asm_run.fern` /
`asm_ir_run.fern -target arm64-linux` / wasm):

- **Functional**: extend `TestSelfHostAsmRun{X86_64,Arm64,WASM}` with
  RC-exercising programs (alias + push loops, scope drops, reassign,
  match consume, struct/enum/closure builders) asserting exit
  code/stdout — value-correctness across the matrix.
- **Soundness**: an over-release counter (`__rc_underflow_count()`
  builtin, as native) asserted 0 on a clean corpus; deliberate
  double-dec asserted 1. Phase-3 go/no-go signal.
- **Memory**: peak-heap-bytes regression tests mirroring
  `internal/e2e/rc_heap_bump_*_test.go` once free is on — a push loop
  reclaims to O(1) blocks, precise drops bound the live set.
- **Determinism**: the byte-identical self-bootstrap fixed-point
  (`self_host_stage2_*_fixed_point_test.go`,
  `self_host_arm64_fixpoint_test.go`) and `TestSelfHostBootstrapsItself`
  must stay green at every slice — the analyses must be deterministic
  (no map-iteration-order dependence; use index-ordered walks like the
  native `markConstructionMoves` does).
- **Differential**: where practical, diff self-host output behaviour
  against the native compiler on the `rcCorpus` shapes.

Per CLAUDE.md: gate locally on x86-64 + wasm; let CI run the arm64 /
qemu matrix. Run the whole `internal/e2e` with `-timeout 30m`.

---

## 8. Invariants & risks

- **Safe-leak invariant** (from `OWNERSHIP-INFERENCE-PLAN.md`): every
  conservative bail degrades to dec-without-free, never over-release.
  A mis-analysis is a leak (slower), never a UAF — until Phase 3 turns
  free on, at which point the underflow detector + `is_unique` gate are
  the second net.
- **Determinism is mandatory.** The fixed-point test demands
  byte-identical output stage1↔stage2. Every analysis must walk the
  AST in a deterministic order and emit deterministically.
- **Two backends in lockstep.** Shared decisions in `asmcore.fern`
  mean an x86-green change is almost always arm64-green; the
  per-backend emit is the only place parity can break — keep the two
  `emit_*` edits mechanically identical.
- **Atomic layout slices.** A single missed offset breaks every
  program. Migrate one category at a time, route offsets through the
  shared helpers, and run the full functional suite per category.

---

## 9. Implementation log

(Updated as slices land.)

- 2026-06-07: design doc created; native + self-host fully mapped
  (this document).
- 2026-06-07: **Phase 0c (runtime layer), x86-64 — SHIPPED.** Replaced
  the no-op RC stubs in `asm.fern` with real bodies ported from the
  native x86_64 backend (`emitRcIncRuntime` / `emitRcDecRuntime` /
  `emitRcIsUniqueRuntime`): `__fern_rc_inc`, `__fern_rc_dec`,
  `__fern_rc_is_unique`, `__fern_rc_underflow_count`, plus the
  `__fern_rc_underflow` BSS counter. Guard chain adapted to the
  self-host BSS heap (null → SSO inline-tag → low-address `<0x10000` →
  static sentinel), replacing the native mmap-heap `<0x10000000`
  guard. No free path yet (safe-leak); `__fern_arr_dec` /
  `__fern_drop_arr_ptr` stay no-ops until the array layout migration.
  These are not yet wired into real allocations — they're exercised
  directly via `__alloc` + `__store_i32`/`__load_i32`. Tests:
  `TestSelfHostAsmRunX86_64/rc-*` (inc/dec arithmetic, is_unique
  true/false, underflow detected/clean, null-safe). Byte-identical
  self-bootstrap (`TestSelfHostStage2FixedPoint`,
  `TestSelfHostBootstrapsItself`, `TestSelfHostFixpoint`) stays green.
  Next: mirror to `asm_arm64.fern` + `wasm.fern`; then Phase 0b — the
  array rc-header layout migration that wires these in.
- 2026-06-07: **Phase 0c (runtime layer), arm64 — SHIPPED.** Mirrored
  the x86-64 helpers to `asm_arm64.fern` (ported from the native arm64
  backend: `ldur`/`stur` for `[ptr-8]`, `tbnz #31` sentinel, `tbnz #0`
  SSO tag, `mov x9,#0x10000` low-address guard, adrp/`:lo12:` access to
  the `__fern_rc_underflow` BSS counter). Tests consolidated into
  `internal/e2e/self_host_rc_runtime_test.go` with a shared case table
  driving both `TestSelfHostRcRuntimeX86_64` and
  `TestSelfHostRcRuntimeArm64` (the latter built + run under
  qemu-aarch64 — all six cases green). Next: wasm
  (`wasm.fern`); then Phase 0b array layout migration.
- 2026-06-07: **Phase 0a (array-alloc centralization), x86-64 —
  SHIPPED.** Introduced `__fern_arr_box(cap) -> data ptr` in `asm.fern`
  and routed all 11 array allocation sites through it (literal,
  str_split, the read-array builder, `__fern_alloc_u8`, `__fern_args`,
  push-grow, slice, concat, reverse, str_bytes, str_chars). The box
  layout (`[cap, len, e…]`, data = base+8, cap at `[data-8]`) is now
  defined in ONE place — behaviour-preserving (identical layout), so
  the full suite + byte-identical bootstrap stay green. The raw exec
  `argv` buffer (not a Fern array) is correctly left inline. This turns
  the rc-header migration (0b) into a ~2-line change to the helper +
  the single `cap` reader in `__fern_arr_push`. Clobbers only
  rax/rdi (matches `__fern_alloc`'s contract → drop-in at every site).
- 2026-06-07: **Phase 0b (array rc-header layout), x86-64 — SHIPPED.**
  Flipped `__fern_arr_box` to the rc layout: base = `[cap, rc, len,
  e…]`, data = base+16, so **rc sits at `[data-8]`** (the uniform offset
  the generic `__fern_rc_*` helpers read), cap at `[data-16]`, and every
  element/`len` offset relative to `data` is **unchanged** (array
  readers index off `data`, so only the alloc side moved). Allocs init
  `rc = 1`. The only non-helper edit was the single `cap` reader in
  `__fern_arr_push` (`-8`→`-16`). x86-64 arrays now carry live rc
  headers, ready for inc/dec wiring (Phase 1d). inc/dec are not emitted
  yet, so behaviour is unchanged (+8 bytes/array, underflow detector
  stays 0). Verified green: full asm-run + array / array-method /
  string / map / json / bytes / charmethods / map-iter/keys/literal
  self-host suites + the RC runtime tests + the byte-identical
  self-bootstrap (BootstrapsItself / Fixpoint / Stage2FixedPoint).
  Next: Phase 1d — emit `__fern_rc_inc` at array alias sites +
  `__fern_rc_dec` at scope/exit + overwrite (porting
  `needs_rc_inc_on_alias` + the exit-dec sweep). Then mirror 0a/0b/1d to
  arm64 + wasm.
- 2026-06-07: **Phase 1d-i (array alias-inc on var-decl), x86-64 —
  SHIPPED.** First RC counting wired into real allocations. Added the
  shared `needs_rc_inc_on_alias` + `ty_is_array_like` predicates to
  `asmcore.fern` (ported from native `needsRcIncOnAlias`, scoped to
  arrays), and emit `__fern_rc_inc` in `asm.fern`'s `StmtVar` general
  path when `var y = x` binds an array-typed ident alias (a fresh
  literal / call result is already owned at rc=1 and is NOT
  re-incremented). This is the first time the Phase-0c helpers run on
  the Phase-0b rc-headered arrays — proving the three layers integrate.
  Inc-only (no dec sites yet), so it's safe-leak + over-release-detector
  clean (rc only grows). Tests: `TestSelfHostRcAliasIncX86_64`
  (aliasing value-correctness, detector == 0, and an emission assertion
  that the retain is actually emitted at the alias). Byte-identical
  self-bootstrap + fixpoint stay green (the fixpoint executes the
  self-host's own array code with the new inc emission). NOTE: the
  user-level `xs = xs.push(v)` form on `i32[]` is a *pre-existing*
  self-host limitation (segfaults on origin/main too — `.push`
  reassignment lowers to a `-1` fallback), independent of this work; the
  self-host's internal push (validated via fixpoint) is unaffected.
  Next sub-slices: the remaining inc sites (reassign / call-arg /
  field+index alias / literal element / closure capture) and the dec
  sites (function-exit sweep + dec-on-overwrite) — together they
  balance the counts; then arm64 + wasm mirrors.
- 2026-06-07: **Phase 1d (reassign-inc + dec-on-overwrite), x86-64 —
  SHIPPED.** `asm.fern`'s `StmtAssign` now retains the new reference
  when `y = x` reassigns an array slot to an rc-tracked alias
  (`needs_rc_inc_on_alias`) and releases (dec) the OLD value the slot
  held (`ty_is_array_like` target). A fresh literal / call RHS is not
  re-incremented. Local, detector-clean for ordinary reassignment (the
  old value had rc>=1), and balances the reassignment lifecycle ahead
  of Phase 3. Tests: `TestSelfHostRcReassignX86_64` (reassign-to-alias /
  source-intact / reassign-to-fresh / no-underflow + an emission
  assertion that both the retain and the release are emitted). Full
  suite + byte-identical bootstrap/fixpoint green. KNOWN DRIFT (benign
  under safe-leak): the self-host's own `xs = xs.push(v)` internal form
  decrements a buffer the in-place push returned unchanged (the
  self-host push doesn't bump rc the way native Phase 2a does), so rc
  drifts down on repeated self-mutation. Harmless while free is off
  (values stay valid; fixpoint stays byte-identical); the cow-aware dec
  that fixes it is a Phase-3-prep item (mirrors the native drift audit).
  Next: the function-exit dec sweep (zero-init rc locals + per-return
  sweep, excluding borrowed params) — the other half of balance — then
  the remaining inc sites and the arm64/wasm mirrors.
- 2026-06-07: **Phase 1d (field/index alias inc), x86-64 — SHIPPED.**
  Extended the shared `needs_rc_inc_on_alias` to also fire on
  `ExprFieldAccess` / `ExprIndex` reads whose inferred type is an array
  (`var y = h.items`, `var y = m[i]` for an array-of-arrays), using
  `infer_expr_type`. Picked up automatically by both `StmtVar` and
  `StmtAssign` (no backend edits). Robust: a spurious inc on a
  mis-inferred non-array is harmless (the rc-inc guards short-circuit).
  inc-only → detector-clean. Tests: `alias-struct-field` +
  `emits-retain-at-field-alias`; struct / json / map self-host suites
  (which lean on field-access aliases) + bootstrap/fixpoint green. The
  alias-inc family now covers ident + field + index reads.
- 2026-06-07: **Phase 1d (function-exit dec sweep), x86-64 — SHIPPED.
  The array RC lifecycle is now BALANCED.** Added a per-function release
  sweep: at every in-function `return` and the fall-through exit,
  `emit_array_exit_dec` releases (dec) each array-typed LOCAL slot.
  Borrowed params are skipped via a new `n_params` boundary on
  `EmitState` (set by `emit_function` after binding params) — the borrow
  model (caller still owns the arg). Two supports make it sound under
  safe-leak: (1) `emit_function` zero-inits the body-local slots
  (`rep stosq`) so the sweep reads NULL (dec no-op) for a `var` skipped
  on the current path; (2) `StmtReturn` retains an array result before
  the sweep (and restores it after) so the returned buffer reaches the
  caller at unchanged rc. Combined with the alias/reassign incs +
  dec-on-overwrite, ordinary code is now over-release-detector clean.
  Tests: `TestSelfHostRcExitSweepX86_64` — return-array (retain past
  sweep), borrowed-param (not released, usable in caller), repeated-call
  detector == 0, not-taken-branch-local zero-init no-op, and an emission
  assertion (rep stosq + the dec sweep). Full self-host suite +
  byte-identical bootstrap/fixpoint green (the fixpoint executes the
  self-host's own functions through the new prologue zero-init + exit
  sweep). KNOWN benign drift persists for the self-host's internal
  `xs = xs.push(v)` (prior entry) — harmless while free is off; the
  cow-aware dec is the Phase-3-prep fix. With this the x86-64 array RC
  is functionally complete for Phase 1 (rc headers + all alias incs +
  reassign-inc + dec-on-overwrite + balanced exit sweep). Next: Phase 3
  (size-class freelist + flip free on, gated on a corpus-wide clean
  detector after the drift fix) and the arm64/wasm mirrors of 0a/0b/1d.
- 2026-06-07: **Phase 0a + 0b (array centralization + rc-header
  layout), arm64 — SHIPPED.** Mirrored the x86-64 array work to
  `asm_arm64.fern`: added `__fern_arr_box(x0=cap) -> data ptr` (rc
  layout — base `[cap, rc, len, e…]`, data = base+16, rc at `[data-8]`,
  cap at `[data-16]`; clobbers only x0/x1/x2 like the arm64
  `__fern_alloc`) and routed all 11 array allocation sites through it
  (literal, str_split, read-dir builder, `__fern_alloc_u8`,
  `__fern_args`, push grow, slice, concat, reverse, str_bytes,
  str_chars). The single `cap` reader in `__fern_arr_push` moved
  `[x19,#-8]` → `[x19,#-16]`. The raw exec `argv` buffer (not a Fern
  array) is left inline. arm64 arrays now carry live `rc=1` headers,
  matching x86-64's Phase-0b state. inc/dec not wired on arm64 yet
  (Phase 1d mirror is the follow-up), so behaviour is unchanged
  (+8 bytes/array). Verified under qemu-aarch64: the RC runtime, array,
  array-method, string, map, json, struct, closure, generics, bytes,
  tuple self-host suites + the byte-identical arm64 self-bootstrap
  (`TestSelfHostFixpointArm64` / `TestSelfHostStage2FixedPointArm64`).
  The shared `asmcore` analyses (needs_rc_inc_on_alias, ty_is_array_like,
  n_params) are already in place, so the arm64 1d wiring reuses them.
- 2026-06-07: **Phase 1d (array inc/dec wiring), arm64 — SHIPPED.
  arm64 array RC is now at full parity with x86-64.** Mirrored the
  Phase-1d emission into `asm_arm64.fern` (the shared `asmcore`
  analyses were already in place): alias retain in `StmtVar`,
  reassign-inc + dec-on-overwrite in `StmtAssign`, the function-exit
  release sweep (`emit_array_exit_dec`) at every return + fall-through,
  body-local zero-init (a small `str xzr` loop — arm64 has no
  `rep stosq`), `set_n_params` for the borrow boundary, and the
  array-return retain in `StmtReturn`. The arm64 rc-inc/dec call shape
  is `str x0,[sp,#-16]!; bl __fn___fern_rc_{inc,dec}; add sp,sp,#16`
  (frameless helper, arg at `[sp]`, ptr back in x0). Verified under
  qemu-aarch64: `TestSelfHostRcArm64` (alias / reassign / field-alias /
  return-array / borrowed-param / exit-sweep-no-underflow /
  branch-local-zeroinit) + the array / array-method / string / map /
  json / struct / closure / generics / tuple / bytes suites + the
  byte-identical arm64 self-bootstrap (`TestSelfHostFixpointArm64` /
  `TestSelfHostStage2FixedPointArm64`). Both production asm backends now
  have the complete balanced Phase-1 array RC lifecycle. Remaining:
  wasm backend (`wasm.fern`), Phase 1e (strings/structs/enums/closures/
  tuples), and Phase 3 (freelist + free, after the self-reassign drift
  fix) — the reclamation win.
- 2026-06-08: **Phase 3 prep (cow-aware dec-on-overwrite), x86-64 +
  arm64 — SHIPPED.** Fixed the self-reassign rc drift on both backends:
  `StmtAssign`'s dec-on-overwrite now SKIPS the release when the new
  value equals the old value the slot held (`cmpq;je` on x86-64,
  `cmp;b.eq` on arm64). This is the in-place-mutator case — e.g.
  `xs = xs.append(v)` growing within capacity returns the SAME buffer,
  which is still live, so releasing it would over-count. A genuine
  reassignment to a DIFFERENT buffer still releases the old. Measured:
  a 20-iteration `xs = xs.append(i)` loop went from 4 over-releases to
  **0** (the native drift audit's headline case). Tests:
  `TestSelfHostRcSelfMutateX86_64` (self-append-no-underflow / values /
  reassign-different-clean) + `TestSelfHostRcArm64` self-append cases
  (under qemu). Full suites + byte-identical bootstrap/fixpoint green on
  both backends. This removes the last known benign drift, so the
  over-release detector is now clean across ordinary code AND
  self-mutation — the go/no-go signal for flipping `free` on. Next:
  Phase 3 proper — a size-class freelist + flip `__fern_free` and the
  rc==1 free path on (array buffers reclaimed via __fern_arr_dec / the
  drop helpers), gated on the corpus-wide clean detector.
- 2026-06-08: **Phase 1d (struct-literal construction inc), x86-64 +
  arm64 — SHIPPED.** A struct field initialised from an rc-tracked array
  alias (`H { items: xs }`) now retains the buffer on both backends (the
  struct owns a new reference). This is the **free-readiness gate**: it
  closes the uncounted-alias hole that would become a use-after-free
  once free is on — a struct outliving the source local
  (`function mk(): H { var xs = [..]; return H{items: xs}; }`) keeps its
  array alive. inc-only (struct drop isn't wired — Phase 1e), so it's
  over-release-detector clean and a safe leak today. A fresh literal /
  call field value is owned and not re-incremented. Tests:
  `TestSelfHostRcConstructX86_64` (struct-holds-array,
  struct-outlives-source, struct-fresh-literal, emission) + green
  struct/json suites + byte-identical bootstrap/fixpoint on both
  backends.

  **Free-readiness gate (remaining uncounted-alias sites to close
  before flipping `free` on — each would be a UAF once buffers are
  reclaimed):** (a) the struct-UPDATE form `H{...base, f: xs}`;
  (b) array-literal elements `[xs, ys]` and tuple elements `(xs, n)`;
  (c) closure captures of an array; (d) index/field assignment
  `arr[i] = xs` / `obj.f = xs`. All are inc-only, detector-clean, safe
  while free is off. Once all are closed (and the corpus-wide detector
  stays 0 on the self-host itself), Phase 3 can flip free: a size-class
  freelist (`__fern_free` push + `__fern_alloc` pop, round-to-16 so
  classes are exact) and an array `__fern_arr_dec` that frees the buffer
  (size `(cap+3)*8`, base `data-16`) at rc==1 — routed from the exit
  sweep + dec-on-overwrite. Pointer-element arrays may free their buffer
  without an element walk (sound: elements leak, never UAF) until a
  drop-walk slice lands.
- 2026-06-08: **Phase 1d (array + tuple construction inc), x86-64 +
  arm64 — SHIPPED.** Array-literal elements (`[a, b]`) and tuple-literal
  elements (`(xs, n)`) initialised from an rc-tracked array alias now
  retain the buffer on both backends (the container owns the reference),
  with the container pointer preserved across the retain call. Closes
  two more free-readiness gate sites. inc-only / detector-clean / safe
  (free off). Tests: `TestSelfHostRcConstructContainersX86_64`
  (array-of-arrays, tuple-of-array, return-arr-of-arrs — the
  would-be-UAF capture case) + arm64 cases in `TestSelfHostRcArm64` +
  green tuple/array suites + byte-identical bootstrap/fixpoint on both
  backends. Free-readiness gate now CLOSED for: struct-literal /
  array-literal / tuple-literal element stores. Remaining before the
  free flip: struct-UPDATE form `H{...base, f: xs}`, closure captures of
  an array, and index/field assignment `arr[i] = xs` / `obj.f = xs`.
- 2026-06-08: **Phase 1d (struct-update construction inc), x86-64 +
  arm64 — SHIPPED.** The struct-update form `H { ...base, f: v }` now
  retains BOTH the copied non-overridden array fields (the new struct
  references the base's arrays) AND override array values, on both
  backends (base + box ptrs preserved across the retain calls). This is
  the heavy self-host path (`EmitState { ...s, … }` is threaded
  everywhere and carries many array fields), so it's the most important
  remaining gate site — and matches the native IR lowering, which inc's
  copied pointer fields. Tests: `struct-update-copy` /
  `struct-update-override` in `TestSelfHostRcConstructX86_64` +
  `TestSelfHostRcArm64` + green struct-update / functional-update suites
  + byte-identical bootstrap/fixpoint (the fixpoint exercises the
  EmitState-spread path heavily). Free-readiness gate now CLOSED for:
  struct-literal / array-literal / tuple-literal element stores AND the
  struct-update form. Remaining before the free flip: closure captures
  of an array, and index/field assignment `arr[i] = xs` / `obj.f = xs`.
- 2026-06-08: **Phase 1d (closure-capture construction inc), x86-64 +
  arm64 — SHIPPED.** A lambda capturing an rc-tracked array now retains
  the buffer at the capture-store (the closure box owns the reference),
  on both backends (box ptr preserved across the retain call). Uses the
  block-form lambda `function (): T { … }` (the working closure shape —
  the arrow form `() => e` capturing a local is a *separate pre-existing*
  self-host limitation: it segfaults on origin/main regardless of RC).
  Tests: `TestSelfHostRcClosureX86_64` (local closure + escaping closure
  capturing an array, both detector-0 + value-correct) + green
  closures/higher-order suites + byte-identical bootstrap/fixpoint on
  both backends. Free-readiness gate now CLOSED for: all literal
  constructions, the struct-update form, AND closure captures. **Only
  remaining site: index/field assignment** (`arr[i] = xs` / `obj.f = xs`
  storing an array) — note `obj.f = …` struct-field assignment is
  rejected by the checker (E048-style), and array-of-arrays index-assign
  is rare; once confirmed/closed, Phase 3 can flip free.
- 2026-06-08: **Phase 3 (free flip) — ATTEMPTED, NOT LANDED (gated on a
  residual under-count).** Implemented the x86-64 size-class freelist
  (`__fern_alloc` round-to-8 + freelist pop; `__fern_freelist` BSS, 1024
  classes) and the real `__fern_arr_dec` (frees the buffer at rc==1:
  base `data-16`, size `(cap+3)*8`, idx `cap+3`), and rerouted the
  exit-sweep + dec-on-overwrite array releases through it. Bounded probes
  all passed (churn-loop reclaim, alias, reassign, return-array,
  index-assign, match-binding, for-over-arr-of-arr, nested-call-temp —
  all value-correct, detector 0). **But the full self-host
  (`TestSelfHostStage2FixedPoint`, mmc2 compiling itself) SEGFAULTED** —
  a double-free: some array buffer in the self-host's own code has two
  live references while its rc reflects one, so the second release
  re-frees a recycled block. Zeroing the rc on free (so a stray second
  release reads 0 → detector, not re-free) made the fixpoint pass, which
  CONFIRMS the diagnosis (double-free, not a missing helper) but only
  MASKS it — if the freed block is reused between the two releases, the
  second release corrupts the live reuser, so it is not sound. The Phase-3
  changes were reverted; the branch stays at the sound free-off state.
  **Before free can flip:** find + fix the residual under-count. The
  prime suspect is an uncounted alias from a CALL result that aliases an
  argument (e.g. `var z = f(xs)` where `f` returns its param), which the
  current `needs_rc_inc_on_alias` (ident/field/index only) does not
  retain — this is exactly what the native compiler's
  `inferParamEscapes` / `findReturnsNoParamEscape` analyses handle. The
  port's next slice is that escape/alias analysis (or a precise audit of
  the self-host's array-returning helpers), after which the freelist +
  `__fern_arr_dec` flip above can be re-applied and validated against the
  bootstrap/fixpoint + a corpus-wide-0 detector.
- 2026-06-08: **Phase 3 free flip — ATTEMPT #2, REVERTED (second
  residual gap: JSON/nested-structure corruption).** Re-applied the
  x86-64 freelist + `__fern_arr_dec` flip with the borrow fix (a
  reassigned array PARAM no longer releases its caller-owned buffer),
  which DID close the bootstrap double-free: the full self-host
  bootstrap + `Stage2FixedPoint` ran byte-identical with free on, the
  reclaim-churn proof completed (workload >> heap, reclaimed), and all
  the targeted RC + array/struct/map suites were green + detector 0.
  BUT CI's `TestSelfHostStdTestE2E` (not on the bootstrap path) FAILED:
  the **JSON nested-arrays** test miscompared under the asm backend
  (interp passed), i.e. a buffer inside a nested array/object/string
  tree was freed while still referenced and reused → corruption. So the
  borrow fix was necessary but NOT sufficient — there is at least one
  more uncounted reference in the nested-structure path (prime suspects:
  **Map values that are arrays/strings**, and **string elements** of
  string[] which aren't rc-tracked yet). Reverted to the sound free-off
  state. CONCLUSION: point-fixes won't close free; it needs the
  **complete counting model** — port the native `computeFreeEligible`
  borrow-aware taint (only untainted owned locals are free-eligible;
  borrowed-derived values never free) AND rc-track the remaining heap
  types (Phase 1e: strings/structs/enums/maps) so nested structures are
  fully counted — BEFORE re-applying the freelist + `__fern_arr_dec`
  flip (which is correct and recorded above). Until then free stays off
  (safe-leak); every inc/dec/construction-inc slice already merged is
  sound and is the foundation that flip will switch on.
- 2026-06-08: **Phase 3 (FREE ON), x86-64 — SHIPPED (attempt #3, the
  reclamation win, now validated against the std-test suite).** The JSON
  gap from attempt #2 was a missing **enum/Option/Result payload
  construction inc**: `Arr(xs)` / `Some(xs)` / `Ok(xs)` / `Err(xs)` never
  retained an rc-tracked array payload, so the array was freed while the
  variant box still referenced it → nested-structure corruption. Added
  `enum_payload_retain` (shared helper, x86) at all three constructor
  sites (gated on `needs_rc_inc_on_alias`, box ptr preserved). With that
  + the borrow fix (reassigned array param doesn't free the caller's
  buffer) + the freelist + `__fern_arr_dec`, free is now SOUND: full
  self-host bootstrap + `Stage2FixedPoint` byte-identical with free on,
  AND `TestSelfHostStdTestE2E` (JSON nested structures — the test that
  caught attempt #2) passes, plus all RC / array / map / enum / struct
  suites + detector 0. RECLAIMING:
  `TestSelfHostRcFreeReclaimX86_64` (reclaim-churn ≫ heap completes;
  borrowed-param builder; enum-holds-array survives source scope-exit).
  Scope unchanged: arrays ≤ 8 KiB recycle, larger bump-only; strings/
  structs/enums not yet freed (Phase 1e) — their array payloads are
  retained (so sound) but leak. Next: mirror the freelist +
  `__fern_arr_dec` + borrow fix + enum_payload_retain to arm64.
- 2026-06-08: **Phase 3 (FREE ON), arm64 — SHIPPED. Both production
  backends now reclaim array buffers.** Mirrored the x86-64 free flip to
  `asm_arm64.fern`: `enum_payload_retain` (arm64) wired into Some / Ok /
  Err / enum-variant constructors; the size-class freelist in
  `__fern_alloc` (round-up-8 via `bic`, pop the class head, clobbers only
  x0..x3 like the bump path); the real `__fern_arr_dec` (free at rc==1,
  base `data-16`, idx `cap+3`, clears rc on free, guards null/low-addr/
  sentinel); the two array releases (exit sweep + dec-on-overwrite)
  rerouted to it; and the borrow fix (a reassigned array param doesn't
  release the caller's buffer). Verified under qemu-aarch64:
  `TestSelfHostStdTestE2EArm64` (JSON nested structures — the gap that
  caught x86 attempt #2) passes; `TestSelfHostRcArm64` incl. the new
  reclaim-churn (alloc >> heap completes) + enum-holds-array; the
  byte-identical arm64 bootstrap (`FixpointArm64` / `Stage2FixedPointArm64`);
  and the array / json / map / struct / enum / closure / tuple / generics
  arm64 suites. detector 0. The array reclamation win is now on BOTH
  x86-64 and arm64. Remaining for full parity with native: Phase 1e
  (rc-track + free strings/structs/enums/maps), the wasm backend, and
  the eventual Perceus optimisation passes (move/reuse/precise-drop)
  which all ride on this counting foundation.
- 2026-06-08: **Phase 3 refinement — freelist class cap 1024 → 65536,
  both backends.** Array buffers up to 512 KiB now recycle (was 8 KiB),
  so the self-host's larger token/AST arrays AND their geometric-growth
  garbage reclaim, not just small arrays — a real peak-memory cut on the
  self-host's own workload. BSS freelist grows 8 KiB → 512 KiB
  (zero-page, no file cost). arm64 uses the register form
  `mov x2/x9,#0x10000; cmp` (a shifted `cmp` immediate may be
  unsupported by the in-process Mach-O assembler — same class of issue
  as the `bic` fix). Blocks > 512 KiB still bump-only (a fixed .bss bump
  heap can't cheaply recycle arbitrarily large blocks; a large-block
  free-list/first-fit is a possible follow-up). Verified on both
  backends + both arm64 assemblers: std-test (JSON), bootstrap/fixpoint,
  reclaim-churn, RC suites — all green, detector 0.
- 2026-06-08: **Phase 4 (Perceus optimisation passes) begins —
  move-on-return, both backends.** First inc/dec pair-cancellation:
  `return xs` where `xs` is a bare owned array LOCAL (an `ExprIdent`
  naming an array-typed local whose slot index ≥ `n_params`, i.e. NOT a
  borrowed parameter) now MOVES the buffer to the caller. The
  return-retain inc and the exit sweep's dec of that one slot are a
  balanced pair on the same value, so both are elided — the buffer
  reaches the caller at its current rc with identical net effect, and
  one inc + one dec are saved per moved return (the hottest shape in the
  self-host: every `make`/`build`/`parse` helper returns its freshly
  built array). Eliding an inc/dec pair on a single slot is net-zero
  regardless of any aliasing of the same buffer through other slots, so
  it is safe with free ON. Implementation: shared oracle
  `(s) move_on_return_idx(e)` in `asmcore.fern` returns the local index
  to move (or -1); `emit_array_exit_dec_excl(s, excl)` skips that slot
  (`emit_array_exit_dec` delegates with -1); `StmtReturn` skips the inc
  when `mov_idx >= 0` and passes it to the sweep, mirrored in `asm.fern`
  + `asm_arm64.fern`. A returned BORROWED param (idx < n_params) is NOT
  moved — the caller still owns it. Coverage:
  `TestSelfHostRcMoveOnReturnX86_64` (bare-local move, chained moves,
  move-alongside-a-swept-sibling, borrowed-param-not-moved, move-churn
  ≫ heap, + emission asserts: no retain inc on the moved path, retain
  inc still present for a non-local array return) and three
  `TestSelfHostRcArm64` parity cases (move-bare-local,
  move-with-sibling-sweep, return-param-not-moved). Full regression:
  std-test (JSON), bootstrap/fixpoint (x86 + arm64), all RC suites —
  green, detector 0. Next Phase-4 candidates: drop-on-last-use
  (release an array local right after its final read instead of at the
  exit sweep, shrinking live ranges) and FBIP reuse (route a
  uniquely-owned about-to-die buffer straight into the next same-size
  allocation, skipping free+alloc).
- 2026-06-08: **wasm backend RC — Phase 0c foundation (additive).** The
  third production backend (`wasm.fern`) had zero RC; this lands the
  reference-counting runtime, the wasm32 mirror of the x86-64 / arm64
  `__fn___fern_rc_*` helpers. New `rc_runtime_helpers(heap_base)` emits
  `$__fern_rc_inc` / `$__fern_rc_dec` / `$__fern_rc_is_unique` /
  `$__fern_rc_underflow_count` plus the raw-memory pokes `$__alloc` (→
  bump heap) / `$__load_i32` / `$__store_i32` — gated on use via
  `module_uses_rcmem`, so existing modules are byte-for-byte unchanged
  (the whole wasm emit + binary suites stay green). rc word is an i32 at
  [data-8]; guard chain matches the asm backends (null → SSO low-bit →
  low-address → static sentinel) with ONE wasm-specific correction: the
  low-address threshold is `heap_base` (the bump-heap start), not the
  asm's fixed `0x10000` — wasm linear memory starts low, so a high fixed
  guard would reject every real heap pointer (caught immediately: all
  four non-trivial cases failed until the threshold was lowered). Any
  pointer below heap_base is static data (sentinel-guarded) or a
  non-pointer, so this is strictly more precise than the asm constant.
  Over-release bumps `$__fern_rc_underflow`. Coverage: a new
  `TestSelfHostRcRuntimeWasm` reuses the shared `rcRuntimeCases` (the
  same six programs that gate x86/arm64 Phase 0c — inc/dec arithmetic,
  is_unique true/false, underflow detected/clean, null-safe), hand-
  building rc-headered objects through `__alloc` + `__store_i32`. NB the
  test file is `self_host_wasm_rc_test.go` (not `..._rc_wasm_test.go`):
  a trailing `_wasm` before `_test.go` is read by the Go toolchain as a
  `wasm` GOARCH build constraint and silently excludes the file on
  linux. This is additive only — it does NOT touch the wasm array layout
  or any existing emit path, so it can't regress the x86/arm64 bootstrap
  (different binaries) or the existing wasm tests. Next for wasm parity:
  migrate the array layout to carry the rc word (`[cap, rc, len, elems]`
  analogue at wasm32 widths), then mirror the inc-on-alias /
  construction-store / exit-sweep / free machinery the asm backends
  already have — each a self-contained slice on this foundation.
- 2026-06-08: **wasm backend RC — array layout migration.** Array
  blocks now carry the rc word, the wasm analogue of the asm Phase-1a
  layout migration. New `$__fern_arr_box(eb)` (in heap_alloc_helpers,
  beside `$__fern_alloc`) allocates `[rc@0][pad@4][len@8][cap@12]
  [elems@16..]` and returns the DATA pointer `a = base+8` with rc
  initialised to 1 — so the rc word sits at [a-8] (matching the asm
  backends' [data-8] convention) while len@[a] / cap@[a+4] /
  elems@[a+8] keep EVERY a-relative array access (index, len, slice,
  grow-copy, for-loops, struct/tuple payloads) byte-for-byte unchanged.
  Migrated the core array allocators to route through it: the array
  literal builder (`emit_array_literal_kind`) and the four runtime
  growers/slicers (`__fern_arr_push`, `arr_slice`, `arr_slice8`,
  `arr_push_wide`). Centralising rc-init in one helper kept the per-site
  change to a one-line alloc-call swap and the blast radius minimal. The
  peripheral array producers (args / env / random_bytes / map_snapshot /
  export wrappers) still use the old header for now; this is SAFE because
  every access is a-relative and rc is not yet wired into array sites
  (mixed old/new layouts read identically) — they migrate alongside the
  inc/free wiring in the next slice, before free is flipped on. Coverage:
  `TestSelfHostRcArrayLayoutWasm` passes a real array literal AND an
  append-grown array straight to the rc intrinsics — fresh => unique
  (rc==1), after inc => not unique (rc==2), inc+dec => unique again,
  elements intact through the shifted data ptr, detector clean on
  balance and firing on over-release. The whole wasm suite (emit /
  binary / readfile / shim / component, ~93 s) stays green — the real
  proof the layout is consistent, since arrays are pervasive. Still
  bootstrap-safe (wasm.fern only; different binary from the x86/arm64
  self-host). Next: migrate the peripheral array producers + wire
  inc-on-alias / construction-store / exit-sweep (counting milestone,
  free off), then `__fern_arr_dec` + freelist (free on).
- 2026-06-08: **wasm backend RC — array layout migration completed
  (uniform).** Migrated the remaining peripheral array producers onto
  `$__fern_arr_box`, so EVERY wasm array now carries the rc word (no more
  mixed old/new layouts): the two `@export` result wrappers (`u8[]` +
  numeric `list<T>`), `random_bytes`, the wasi `args`, the env string
  array, and the map `keys`/`values` snapshot. Each was confirmed an
  array (len@[base], elems@[base+8]) and its access is a-relative, so the
  one-line alloc-call swap is transparent. This is the cleanliness
  prerequisite for flipping free on later (a non-boxed array would read a
  garbage rc at [a-8] and corrupt on free). Coverage extended:
  `TestSelfHostRcArrayLayoutWasm` now also passes a `random_bytes()`
  array and a map `.values()` snapshot straight to the rc intrinsics
  (both rc==1 / unique, values intact). Full wasm suite (~94 s) green.
  The remaining wasm parity step is the COUNTING milestone (inc-on-alias
  + construction-store + exit-sweep, free off) then free — both gated on
  building a reliable "is array local" oracle, which on wasm means
  combining the backend's fragmented type lists (i32[]/string[]/struct[]/
  i64[]/f64[]/fn[] + array-of-arrays) since a spurious inc on a non-array
  pointer would corrupt an adjacent object's header (unlike the asm
  backends, where `asmcore.ty_is_array_like` already gives this oracle).
- 2026-06-08: **wasm backend RC — counting milestone (inc-on-alias +
  exit-sweep, free OFF).** The wasm analogue of the asm Phase-1 counting
  work: array references are now reference-counted, validated
  detector-clean, with free still off (the safe precursor to flipping it
  on). Because wasm has no ownership tracking (unlike the asm backends'
  `asmcore.ty_is_array_like` + construction-store incs), the oracle is
  deliberately CONSERVATIVE and sound: a body local is counted/swept only
  when it is DEFINITELY owned at rc 1 — a fresh array literal / slice — or
  a bare-ident alias of another array (which gets the inc). Borrowed
  params, field reads, index reads and call results are NOT swept (no inc
  to balance them yet) — they leak, which is sound while free is off and
  can never over-release. The key soundness property: an inc is only ever
  emitted on a bare-ident array source (a genuine rc-boxed array), so
  there is no false-positive inc that could corrupt a non-array object's
  [p-8]. Implementation: `collect_arr_src` / `collect_arr_swept` (+
  `ty_spelling_is_array` / `init_is_owned_array` / `init_is_arr_alias`)
  build the sets in `build_ctx` (new `arr_src` / `arr_swept` / `ret_void`
  Ctx fields); StmtVar emits `$__fern_rc_inc` after an alias bind;
  StmtReturn computes the result into a typed `$__retv_*` temp, runs the
  release sweep (`arr_exit_sweep` — dec each swept local), then returns
  (computing-before-sweep is the correct Perceus order and makes
  rc-observation-in-return intuitive); the function epilogue sweeps the
  fall-through path. rc_runtime_helpers is now emitted whenever the heap
  is (the counting calls reference `$__fern_rc_inc`/`_dec`). Coverage:
  `TestSelfHostRcCountingWasm` — alias / multi-alias / append-loop /
  repeated-calls / borrowed-param / branch-local, each asserting the
  computed value AND `__fern_rc_underflow_count() == 0`. The hand-built
  rc-inspection tests (runtime + array-layout) still pass unchanged
  thanks to the compute-before-sweep order. Full wasm suite (~94 s)
  green. Bootstrap-safe (wasm.fern only). Next: the construction-store
  incs (so field/call-sourced arrays can be counted too) + reassign
  dec-on-overwrite, then `__fern_arr_dec` + freelist to flip free ON
  (with return-retain / move-on-return riding on the temp shape already
  in place).
- 2026-06-08: **wasm backend RC — construction-store incs (struct fields
  + array-of-arrays).** Storing an EXISTING array reference into a
  container retains the buffer so the container co-owns it alongside the
  source local — the soundness prerequisite that lets a later slice free
  arrays without dangling a stored reference. Wired at the two main
  construction sites: struct-literal field stores (`H { items: xs }`) and
  array-of-arrays element stores (`[a, b]`), each emitting
  `$__fern_rc_inc` when the stored value is a bare-ident array (an
  existing owner, via `init_is_arr_alias`). A FRESH literal stored
  (`H { items: [9,8,7] }`) is moved, not inc'd (it arrives at rc 1 and the
  container takes that reference). Sound by the same property as the
  counting milestone: an inc only ever fires on a genuine rc-boxed array
  source. Coverage: `TestSelfHostRcConstructWasm` — struct-field-retained
  (source no longer unique), array-of-arrays-retained, struct-field-fresh-
  move (no spurious inc) — each asserting values + detector 0. Full wasm
  suite (~89 s) green; free still OFF; bootstrap-safe. Remaining
  construction sites before free can flip on: struct-update base-copy,
  tuple elements, enum / Option / Result payloads, and reassign
  dec-on-overwrite; then `__fern_arr_dec` + freelist + return-retain /
  move-on-return (the `$__retv_*` temp shape is already in place).
- 2026-06-08: **wasm backend RC — construction-store incs extended
  (tuples + Option/Result payloads).** Continues the construction-inc
  coverage toward free-on soundness: storing an existing array reference
  into a tuple element (`(xs, 99)`) or an Option/Result payload
  (`Some(xs)` / `Ok(xs)` / `Err(xs)`) now retains the buffer. New
  `enum_box_retain` (enum_box delegates with retain=false) appends the
  `$__fern_rc_inc` when the Some/Ok/Err payload is a bare-ident array;
  the tuple emitter incs array elements the same way. Same soundness
  property as before — an inc only ever fires on a genuine rc-boxed array
  source. Coverage: `TestSelfHostRcConstructWasm` gains tuple-elem-
  retained, option-payload-retained, result-payload-retained (each:
  source array no longer unique, values intact, detector 0). Full wasm
  suite (~89 s) green; free still OFF; bootstrap-safe. Construction sites
  now covered: struct field, array-of-arrays, tuple, Option/Result
  payload. Remaining before free flips on: struct-update base-copy and
  general user-enum-variant payloads, plus reassign dec-on-overwrite;
  then `__fern_arr_dec` + freelist + return-retain / move-on-return.
- 2026-06-08: **wasm backend RC — construction-store incs COMPLETE
  (struct-update base-copy + user-enum-variant payloads).** The last two
  container-store sites now retain array references, so EVERY place a wasm
  array can be stored into a container is counted — the final soundness
  prerequisite for the free flip. (1) struct-update `S { ...base, … }`:
  copying a base struct's array field into the new struct creates a second
  owner, so it is retained — UNLESS that field is overridden in the same
  literal (its copy is about to be replaced; retaining would leak), so the
  base-copy inc skips overridden indices. (2) positional user-enum-variant
  constructors `Arr(xs)`: an array payload arg is retained like the
  Option/Result payloads. Coverage: `TestSelfHostRcConstructWasm` gains
  struct-update-base-copy-retained + enum-variant-payload-retained. Full
  wasm suite (~93 s) green; free still OFF; bootstrap-safe. **All
  construction sites now covered:** struct field, array-of-arrays, tuple,
  Option/Result payload, struct-update base-copy, user-enum-variant
  payload. The remaining work to flip free ON: reassign dec-on-overwrite
  (reclaim replaced buffers, with the cow-guard for in-place append) +
  return-retain / move-on-return (so a returned array survives its exit
  sweep) + the `__fern_arr_dec` + size-class freelist runtime; then change
  the exit-sweep / overwrite decs from `__fern_rc_dec` to `__fern_arr_dec`
  and validate detector-clean + reclaim-churn, mirroring the asm Phase-3
  flip.
- 2026-06-08: **wasm backend RC — FREE FLIPPED ON. wasm now reclaims
  array buffers, reaching array-RC parity with the asm backends.** The
  culmination of the wasm rollout. (1) `__fern_alloc` is now freelist-
  aware: it rounds the request to 8, and pops a same-size-class block from
  the size-class freelist before bumping. (2) `__fern_arr_box` records the
  rounded block size in the header slot at [a-4] (the old pad word) so
  free can pick the class without knowing the element width. (3) New
  `__fern_arr_dec`: at rc 1 it FREES — clears the rc word (a double-free
  then reads 0 and ticks the over-release detector instead of corrupting)
  and pushes the block onto its class (the next pointer parked in the bsz
  slot, which arr_box rewrites on reuse). (4) The size-class freelist is a
  zero-initialised 8192-entry region carved between the static data and
  the bump heap (covers blocks ≤ 64 KiB; larger bump-only); heap_base sits
  above it and doubles as the rc low-address guard. (5) The exit-sweep and
  reassign-overwrite decs switched from `__fern_rc_dec` to
  `__fern_arr_dec`; reassign is cow-aware (`xs = xs.append(v)` returning
  the same pointer skips the dec) and retains a new alias. (6) Two return
  paths keep a returned buffer alive: **move-on-return** (a bare owned
  array local is excluded from the sweep) and **return-retain** (any other
  array result is inc'd across the sweep — this caught a real UAF where
  `return wcat(o, …)` hands back the borrowed buffer `wcat` grew in place,
  which the sweep would otherwise free under the caller; surfaced by the
  shim-core self-test, fixed before merge). Coverage: new
  `TestSelfHostRcFreeWasm` (freelist-reuse — a freed block is handed back
  to a same-size alloc as an equal pointer; distinct-class non-aliasing;
  reclaim-churn 100k cycles, value-correct + detector 0) plus the whole
  wasm suite (emit / binary / readfile / shim / component, ~91 s) green
  with free ON. Bootstrap-safe (wasm.fern only). Remaining for full
  cross-backend parity: strings / structs / enums / maps reclamation
  (Phase 1e, all three backends) and the further Perceus opts
  (drop-on-last-use, FBIP reuse).
- 2026-06-08: **wasm backend RC — freelist cap raised 64 KiB → 512 KiB
  (asm parity).** The last array-RC parity gap between wasm and the asm
  backends: asm recycles blocks up to 512 KiB (65536 size classes, #2412)
  but wasm capped at 64 KiB (8192). Raised `fl_cells` 8192 → 65536, so the
  freelist region grows 32 KiB → 256 KiB (still zero-init linear memory,
  below the 1 MiB initial; bump heap starts above it) and wasm now recycles
  the same block range — the larger token/AST arrays included, not just
  small ones. Coverage: `TestSelfHostRcFreeWasm/reclaim-large-block` —
  gen(20000) (cap 32768, a ~128 KiB block) is freed and the same-size
  rebuild pops that block back (equal data pointer), which would NOT
  recycle under the old 64 KiB cap. Full wasm suite (~100 s) green;
  bootstrap-safe. **All three production backends (x86-64, arm64, wasm)
  now have identical array reference counting + reclamation, including the
  size-class freelist range.** Remaining for full Perceus parity with the
  native compiler: non-array heap reclamation (strings / structs / enums /
  maps — Phase 1e, all backends) and the liveness-based optimisations
  (drop-on-last-use, FBIP reuse).
- 2026-06-08: **wasm backend RC — reclaim call-result array locals.** A
  real (non-inert) extension: `var x = build()` array locals were the main
  remaining un-reclaimed array class (only literal/slice/alias inits were
  swept; call results leaked entirely). Now an init that is a direct call
  to a USER free function declared to return an array (new
  `init_is_user_arr_call` against `collect_arr_ret_fns`) is counted +
  swept. Sound because such a callee's StmtReturn always applies
  return-retain, so the result arrives at a counted rc — a borrowed-param
  return (`function pick(xs){ return xs }`) is inc'd, so the caller
  sweeping BOTH the source and the result is not a double-free. Method
  calls / in-place receivers (`xs.append(v)`, which can hand back the
  receiver) and un-annotated returns are deliberately excluded (no
  return-retain → would double-free). Coverage:
  `TestSelfHostRcCallResultWasm` — two call results both swept clean,
  aliased call result, the borrowed-return double-free guard, and a
  self-append result that is NOT swept. Full wasm suite (~98 s) green;
  bootstrap-safe. (Loop-local call results — `var r = build()` re-bound
  each iteration — still leak per-iteration; that needs block-scope drops
  / drop-on-last-use, tracked separately.)
- 2026-06-08: **wasm backend RC — per-iteration release of re-bound array
  locals (StmtVar dec-on-overwrite).** Closes the loop-local leak noted in
  the previous slice: a `var r = build()` re-run each loop iteration mapped
  to one wasm local, and only the final value was swept (at function exit),
  so every prior iteration's buffer leaked. StmtVar of a swept array local
  now does the same cow-guarded dec-on-overwrite as StmtAssign — release
  the slot's prior buffer before storing the new one. Sound because wasm
  locals are zero-init, so the FIRST binding dec's null (a no-op), and the
  `i32.ne` cow-guard skips an in-place rebind; a bare-ident alias is still
  retained. Now every iteration's array is reclaimed as the next is bound
  (the final one at the exit sweep), keeping a build-in-a-loop's memory
  bounded. Coverage: `TestSelfHostRcCountingWasm/loop-local-rebind-clean`
  (1000 rebinds, value-correct + detector 0). Full wasm suite (~98 s)
  green; bootstrap-safe. With this, wasm array reclamation covers the
  practical surface: literals, slices, append-built, aliases, call results,
  construction stores, AND loop/rebind churn. Remaining: Phase 1e
  (strings / structs / enums / maps) across all backends.
- 2026-06-08: **asm backends (x86-64 + arm64) — per-iteration release of
  re-bound array locals (StmtVar dec-on-overwrite).** Ported the wasm fix
  to the PRIMARY backends, where it was a real leak: `StmtAssign` already
  did cow-guarded dec-on-overwrite, but `StmtVar` did not — so a loop
  body's `var r = build()` re-bound each iteration leaked every value but
  the last (swept only at function exit). `StmtVar` of an array-typed local
  now releases the slot's prior buffer (`__fern_arr_dec`) before storing,
  cow-guarded (`cmp; je/b.eq` skip when the RHS is the same object) and
  balanced with the existing alias-inc. Sound on the bootstrap path:
  `bind_local_typed` maps a `var` to ONE pre-counted frame slot (emit runs
  once), the frame is zero-init at entry so the FIRST binding releases null
  (a no-op), and intra-loop aliasing stays balanced because both aliasing
  slots get the dec-on-overwrite. Mirror added to `asm.fern` (x86-64,
  `movq`/`je`) and `asm_arm64.fern` (`frame_ldr`/`cmp`/`b.eq`). Validated:
  the byte-identical bootstrap fixpoints — which EXECUTE the self-host
  (itself full of loop-local array rebinds) — pass on both backends
  (`Fixpoint` / `Stage2FixedPoint` x86 + arm64), plus `StdTestE2E`, the RC
  reassign/exit-sweep suites, and a new
  `TestSelfHostRcFreeReclaimX86_64/loop-local-rebind` (100k rebinds,
  value-correct + detector 0). With this, all three backends release
  re-bound array locals per-iteration. (The wasm slice landed this first;
  this brings x86-64 + arm64 to the same behaviour.)
- 2026-06-08: **wasm backend RC — reclaim fresh-array builtin/method
  results.** Final array-reclaim gap on wasm: locals inited from a builtin
  / method that returns a FRESH owned array — `args()`, `random_bytes(n)`,
  and a map's `.keys()` / `.values()` snapshot — were not counted/swept
  (only user-function call results were). New `init_is_fresh_array_builtin`
  adds them to the swept set. Safe because these never alias an existing
  buffer (unlike `.append(v)`, which can return the receiver in place and
  stays excluded). Coverage: `TestSelfHostRcCallResultWasm` gains
  random-swept, mapkeys-swept, and a `.values()`-in-a-loop case (fresh +
  per-iteration dec-on-overwrite, detector 0). Full wasm suite (~96 s)
  green; bootstrap-safe. wasm array reclamation is now comprehensive:
  literals, slices, append loops, aliases, user-call results, fresh
  builtins/methods, construction stores, and loop/rebind churn — matching
  the asm backends' sweep-by-type completeness for the common cases.
  Remaining: Phase 1e (strings / structs / enums / maps).
- 2026-06-08: **wasm backend RC — reclaim user-method array results.** The
  symmetric completion of the user-function call-result reclaim: a
  declared-array local inited from a user METHOD that returns an array
  (`var r: i32[] = obj.build()`) is now counted/swept. New
  `collect_arr_ret_methods` + `init_is_user_arr_method` recognise it; the
  declared-array local type is required at the call site so a same-named
  method on another receiver returning a non-array can't misclassify the
  slot, and the method's StmtReturn return-retain makes the result safe to
  sweep. Builtin methods (`.append`/`.with`, receiver-passthrough) are not
  user methods, so they're never matched. Coverage:
  `TestSelfHostRcCallResultWasm` gains method-result-swept and
  method-result-loop (per-iteration release, detector 0). Full wasm suite
  (~88 s) green; bootstrap-safe. Remaining: Phase 1e (strings / structs /
  enums / maps) — a large per-type rollout (layout → count → free, plus
  recursive field-release for containers and extern/canonical-ABI layout
  for host-interop values); strings are the simplest next target (flat
  block, reuse __fern_arr_dec / freelist, static literals auto-excluded by
  the address guard).
- 2026-06-08: **Phase 1e begins — wasm string rc-box layout foundation.**
  First non-array heap type. New `$__fern_str_box(p)` rc-headers a heap
  string block (8-byte rc+bsz header, returns s = base+8 so [s] = len,
  [s+4..] = bytes stay s-relative; rc at [s-8], block size at [s-4]). The
  pure-Fern string builders now route through it — `__fern_strcat`,
  `substr`, `str_upper` / `str_lower`, `str_repeat`, `str_join`,
  `string_from_bytes`, `i32_to_str` / `i64_to_str` (9 helpers; the
  `alloc(4+len)` payload arg is unchanged, str_box adds the header).
  Static string literals stay in the data section (below heap_base),
  unboxed — the rc helpers' address guard already treats them as immortal,
  so a string value transparently mixes boxed-heap + unboxed-literal
  pointers (every access is s-relative). Release reuses the shared
  `$__fern_arr_dec` + size-class freelist (a string is FLAT — no
  rc-tracked children — so no recursive field-dec, unlike the container
  types). The extern/canonical-ABI-adjacent string builders (env / args /
  read_file / @export wrappers) are deliberately left unboxed for now;
  partial boxing is safe in the foundation because no string rc-ops are
  wired yet. Coverage: `TestSelfHostRcStrBoxWasm` — a fresh concat /
  to_upper string is unique (rc==1); a literal is not (guarded); concat /
  substr values intact. Full wasm suite (~94 s) green; bootstrap-safe.
  Next: string counting (sweep string locals via arr_dec — the address
  guard no-ops literals, so no conservative oracle needed; inc-on-alias,
  construction-incs, reassign/rebind dec, return-retain) then flip free.
- 2026-06-08: **wasm string RC — counting milestone (free OFF).** Mirrors
  the array counting milestone for heap strings, validated detector-clean.
  New conservative oracle `collect_str_swept` (+ `init_is_owned_string` /
  `init_is_string_alias`, both on the existing `is_string_expr`): a body
  string local is released at exit when it's OWNED — a fresh concat
  (`a + b`, a str_box block) or a string literal — or a bare-ident alias.
  Borrowed sources (params, field/index reads, get_or, method results) are
  conservatively NOT swept (they leak, sound while free is off). `Ctx`
  gains `str_swept` (populated last in `build_ctx`, once all string locals
  are known so alias sources resolve). StmtVar emits `$__fern_rc_inc` after
  a string alias bind; `str_exit_sweep` (rc DEC via `$__fern_rc_dec` — no
  free yet) runs at StmtReturn (alongside the array sweep) and the
  function epilogue. A literal slot is a guard no-op; a heap concat
  balances its inc-on-alias so the over-release detector stays clean — even
  for an alias of a borrowed param (the inc on the alias binding nets
  against the alias's sweep, leaving the caller's string untouched).
  Coverage: `TestSelfHostRcStrBoxWasm` gains concat-swept-clean,
  concat-alias-clean, concat-loop-clean (value-correct + detector 0). Full
  wasm suite (~93 s) green; bootstrap-safe. Next: string construction-incs
  (strings stored into containers) + reassign/rebind dec + return-retain,
  then flip the string sweep from `__fern_rc_dec` to `__fern_arr_dec` to
  reclaim heap strings.
- 2026-06-08: **wasm string RC — construction-store incs.** Storing an
  existing heap string reference into a container now retains it, so the
  container co-owns it alongside the source local — the soundness
  prerequisite for flipping string free on (else a swept string would be
  freed while a container still holds it). Wired at the same sites as the
  array construction-incs, OR-ing in `init_is_string_alias(value, cx)`
  beside the array check: struct-literal field stores, tuple elements,
  string[] elements (the i32-slot element path), Some/Ok/Err payloads
  (enum_box_retain), positional enum-variant payloads, and struct-update
  base-copy (a copied `string` field). A bare-ident string source is
  retained; a fresh concat / literal is moved (the container takes its
  rc). Coverage: `TestSelfHostRcStrBoxWasm` gains
  string-struct-field-retained, string-tuple-retained,
  string-array-elem-retained, string-option-retained (source no longer
  unique, values intact, detector 0). Full wasm suite green; bootstrap-
  safe. Construction sites now retain BOTH arrays and strings. Next:
  string reassign/rebind dec + return-retain, then flip the string sweep
  to `__fern_arr_dec` (string free).
- 2026-06-08: **wasm string RC — FREE flipped ON. wasm now reclaims heap
  strings.** The string sweep switched from `$__fern_rc_dec` to
  `$__fern_arr_dec` — strings are FLAT (no rc-tracked children), so the
  shared array dec frees them at rc 1 into the same size-class freelist, no
  new runtime needed. `str_exit_sweep_excl(cx, excl)` adds a move-on-return
  exclusion; `Ctx.ret_is_string` drives string return-retain. StmtReturn
  now: (1) string move-on-return — a bare owned string local returned is
  excluded from the string sweep (handed to the caller at its rc); (2)
  string return-retain — any other string result is inc'd across the sweep
  so a result aliasing a swept local isn't freed underfoot (the
  borrow-return guard, mirroring arrays). This rides on the construction-
  incs (#2493) so a string stored in a container survives its builder's
  exit (sweep dec's it to rc 1, the container keeps it). Coverage:
  `TestSelfHostRcStrBoxWasm` gains string-return-survives (move-on-return),
  string-struct-survives-free (construction-inc + free), string-build-use-
  churn (2000 build/use cycles, detector 0). Full wasm suite (~129 s) green
  with string free ON — the real soundness proof since strings are
  pervasive; bootstrap-safe. (Pointer-reuse isn't directly observable for
  strings — `==` is content-equality — but reclamation is the identical
  arr_dec/freelist path proven for arrays.) Heap strings built and
  used/stored/returned within a function are now reclaimed. Remaining
  string follow-ups: reassign/rebind dec (`s = s + x` loop intermediates
  still leak) and the extern/canonical-ABI string builders (env / args /
  read_file / @export, still unboxed). Then the container types.
- 2026-06-08: **wasm string RC — reassign / rebind release (reclaim loop
  intermediates).** Completes string reclamation for the build-in-a-loop
  pattern (the dominant string-garbage source). `s = s + x` (StmtAssign)
  and a re-bound `var s = …` (StmtVar) on a swept string local now do the
  same cow-guarded dec-on-overwrite as arrays — release the slot's prior
  string (`$__fern_arr_dec`) before storing the new one, so each
  iteration's intermediate is reclaimed instead of leaking all but the
  last. Sound: the slot is zero-init (first store releases null), the
  `i32.ne` cow-guard skips a same-pointer store, and a bare-ident alias is
  retained. Coverage: `TestSelfHostRcStrBoxWasm` gains
  string-builder-loop-reclaim (`s = s + "x"` × 100k) and
  string-rebind-loop-reclaim (`var s = a + b` × 100k) — value-correct +
  detector 0. Full wasm suite (~111 s) green; bootstrap-safe. With this,
  wasm heap-string reclamation covers literals (immortal), concats,
  aliases, construction stores, returns (move + retain), AND loop
  reassign/rebind churn — the practical surface, matching what arrays
  reached. (Stacks on the string free flip.) Remaining string follow-up:
  the extern/canonical-ABI builders (env / args / read_file / @export,
  still unboxed). Then the container types.
- 2026-06-08: **wasm string RC — method/call/slice results reclaimed + a
  borrowed-return-retain correctness fix.** Extended `init_is_owned_string`
  to also sweep string-returning CALLS (`x.to_upper()`, `build_str()`) and
  SLICES (`s[a:b]`) — excluding `get_or` (a borrowed map value). The first
  attempt miscompiled `watbin.fern` (the assembler the component-adapter
  tests build, string-method/slice heavy) → invalid wasm. Bisecting it
  surfaced a LATENT return-retain bug (also affecting the merged array
  call-result reclaim): a function returning a BORROWED reference
  (`return obj.field`) with NO swept locals took the StmtReturn early path,
  which skipped return-retain — so the caller's release of the result freed
  the field underfoot (the `node_head` UAF). Fix: a new
  `ret_value_is_borrowed` drives return-retain in BOTH the sweep-empty and
  temp-dance paths — retain a returned value that may alias/borrow (field /
  index read, borrowed param ident, or a CALL that can return its arg in
  place like `wcat(o, …)`), and do NOT retain a definitely-fresh result
  (array/string literal, concat, slice — removing the prior fresh-return
  over-leak too). Coverage: `TestSelfHostRcStrBoxWasm` gains
  string-method-result-swept, string-fn-result-swept,
  string-slice-result-swept, and string-borrowed-field-return (the
  regression guard). The component-adapter + binary (watbin assembler) +
  full wasm/RC suites are green. (The extern/canonical-ABI string builders
  — env / args / read_file / @export — remain unboxed/unswept.)
- 2026-06-08: **Phase 1e container types begin — wasm tuple rc-box layout
  foundation.** First container type. Tuples are the simplest (a single
  alloc site, fixed layout, no extern/canonical-ABI involvement), so they
  establish the container pattern. The tuple block is now allocated via the
  generic `$__fern_str_box` (the flat 8-byte rc+bsz header box, returns
  base+8) instead of bare `$__fern_alloc`, so a tuple carries an rc word at
  [t-8] while every t-relative element access (`t.N`, destructure) is
  unchanged. Coverage: `TestSelfHostRcTupleBoxWasm` — tuple-fresh-unique
  (rc==1), tuple-values-intact, tuple-holds-array, tuple-destructure. Full
  wasm suite (~119 s) green; bootstrap-safe. Inert/foundational for now
  (free off). Next for tuples: counting (sweep tuple locals, inc-on-alias,
  containers-in-containers construction-incs) then free — where the GENUINELY
  NEW Perceus piece lands: **recursive field-release** (freeing a tuple
  must first dec its rc-tracked array/string elements, using the tuple's
  compile-time element kinds, before freeing the box). Structs and enums
  follow the same shape (structs add the extern/canonical-ABI care that
  bit the string method/call expansion).
- 2026-06-08: **wasm tuple RC — counting milestone (free OFF).** Mirrors
  the array/string counting for tuple boxes, validated detector-clean. New
  `collect_tup_swept` (+ `init_is_owned_tuple` = a `(…)` literal,
  `init_is_tuple_alias` = a bare-ident alias of a tracked tuple local)
  builds the swept set; `Ctx.tup_swept` is populated last in `build_ctx`
  (after `collect_tuple_locals`, so alias sources resolve). StmtVar emits
  `$__fern_rc_inc` after a tuple alias bind; `tup_exit_sweep` (rc DEC via
  `$__fern_rc_dec` — no free yet) runs at StmtReturn (alongside the
  array/string sweeps) and the epilogue. Coverage:
  `TestSelfHostRcTupleBoxWasm` gains tuple-swept-clean, tuple-alias-clean,
  tuple-loop-clean (value-correct + detector 0). Full wasm suite (~114 s)
  green; bootstrap-safe. Next for tuples is the FREE flip — where the
  genuinely new Perceus mechanism lands: **recursive field-release**. A
  tuple free can't go through the generic `__fern_arr_dec` (it doesn't know
  the element layout); the plan is an inline `if [t-8]==1` (last owner →
  about to free) guard that first dec's the rc-tracked elements, then frees
  the box — driven by a tuple element-kind string extended from {string,
  other} to also mark arrays (conservatively, only provably-array elements
  get a recursive dec; a mis-mark would be a UAF, so it must be exact).
- 2026-06-08: **wasm tuple RC — construction-store incs.** Storing a tuple
  reference into a container now retains it (the container co-owns it) —
  the soundness prep that lets a tuple stored in a container survive once
  tuple free is flipped on. Wired at the same 6 construction sites as the
  array/string incs, OR-ing `init_is_tuple_alias(value, cx)` in beside the
  array/string checks: struct-literal field stores, tuple elements,
  array (`[t, …]`) elements, Some/Ok/Err payloads, and positional
  enum-variant payloads. Coverage: `TestSelfHostRcTupleBoxWasm` gains
  tuple-in-array-retained and tuple-in-tuple-retained (source no longer
  unique, nested access intact, detector 0). Full wasm suite (~116 s)
  green; bootstrap-safe; free still OFF. Construction sites now retain
  arrays, strings AND tuples. The remaining tuple step is the FREE flip
  with recursive field-release — a larger, soundness-critical slice
  (an inline `if [t-8]==1` last-owner guard that dec's the rc-tracked
  elements, driven by a tuple element-kind string extended to mark arrays,
  plus tuple return move/retain) — deferred to a focused effort with budget
  headroom, since the array/string flips each surfaced a real UAF mid-way.
- 2026-06-08: **wasm tuple RC — FREE flipped ON, with RECURSIVE FIELD-
  RELEASE (the new Perceus mechanism).** Tuples are now reclaimed, and
  freeing a tuple first releases its rc-tracked elements. The mechanism:
  `tuple_kind_string` is extended to mark each element 's' (string) / 'a'
  (rc-boxed array, via the conservative `is_tuple_elem_array`) / 'i'
  (scalar); `tup_exit_sweep_excl` then, for each owned tuple local, emits an
  inline `if [t-8]==1` (last-owner / about-to-free) guard that dec's the
  'a'/'s' elements (`$__fern_arr_dec` on `[t + i*4]`) BEFORE freeing the box
  — so elements are released exactly once, on the final free (an aliased
  tuple at rc>1 just decrements). Tuple move-on-return (`tmov`) excludes a
  returned bare tuple local from the sweep; `ret_is_tuple` adds tuples to
  the borrowed-return-retain. Rides on the tuple construction-incs so a
  stored tuple survives. A conservative element mark ('i' when unsure)
  means a missed array/string element leaks rather than corrupts; a tuple
  freed INDIRECTLY (via another container's arr_dec, not at this sweep)
  also leaks its elements — both sound, known gaps that a type-tagged
  generic dec would close later. Coverage: `TestSelfHostRcTupleBoxWasm`
  gains tuple-array-elem-released + tuple-string-elem-released (the source
  array/string is dec'd to 0 by the tuple's recursive release, detector 0)
  and tuple-array-churn-clean (50k cycles). Full wasm suite (~132 s) green
  with tuple free ON; bootstrap-safe. (A test-only snag: `use` is a
  reserved word — the churn helper was renamed.) **This establishes
  recursive field-release; structs and enums reuse the same shape** (a
  per-field rc-kind map + the rc==1-guarded recursive dec), adding the
  extern/canonical-ABI care for structs.
- 2026-06-08: **wasm struct/enum RC — rc-box LAYOUT foundation.** A struct
  block (and an enum variant, which shares the struct layout) is now
  rc-headered via the generic `$__fern_str_box` (8-byte rc+bsz header,
  returns base+8), so it carries an rc word at `[s-8]` while every
  s-relative access is unchanged — the type id stays at slot 0 (so `match`
  reads the right tag) and each field stays at `struct_field_off`. The four
  normal Fern struct/enum allocation sites migrate from `$__fern_alloc`:
  `ExprStructLit` (incl. `{ ...base, f: v }` update syntax and named-field
  variants), the `Color.Green` enum-constant field access, the bare-ident
  unit variant (`Nil`/`Empty`), and the positional variant constructor
  (`Circle(3)`). The extern/canonical-ABI result records (the two
  `extern_emit_record_fill` / by-value `@import` struct allocations) are
  intentionally left raw in this slice — layout-only never sweeps structs,
  so a boxed-vs-raw mix is value-safe; they migrate when struct counting
  lands (a swept extern struct must be boxed, or its `[s-8]` read is
  garbage). The struct field construction-incs (array/string members
  retained when stored into a field, incl. the base-copy path) were already
  present from the string slice — they ride unchanged on the boxed layout.
  Coverage: new `TestSelfHostRcStructBoxWasm` — struct-fresh-unique (rc==1),
  struct-values-intact, struct-holds-array / struct-holds-string,
  struct-update-intact, unit-variant-match, variant-payload-intact,
  struct-return-intact, struct-loop-clean (all value-correct + detector 0).
  Full wasm suite green; bootstrap-safe; free still OFF. Next for structs is
  counting (a per-struct-local sweep with inc-on-alias, reusing the
  array/string/tuple sweep shape) then the FREE flip with recursive
  field-release driven by a per-field rc-kind map — at which point the
  extern result records must also be boxed (or excluded from the sweep).
- 2026-06-08: **wasm struct/enum RC — COUNTING milestone (free OFF).** Owned
  struct/enum-value locals are now reference-counted and released (rc DEC via
  `$__fern_rc_dec` — no free, no recursive field-release yet) at function
  exit, mirroring the array/string/tuple counting shape. New `Ctx.struct_swept`
  is the swept set, built by `collect_struct_swept` (last in `build_ctx`, once
  struct locals are known so aliases resolve) from `init_is_owned_struct` (a
  struct literal, a struct-returning call incl. positional variant
  constructors and struct methods, a bare 0-field unit variant, an enum
  constant) OR `init_is_struct_alias` (a bare-ident co-owner). StmtVar emits
  `$__fern_rc_inc` after a struct alias bind; `struct_exit_sweep_excl` runs at
  StmtReturn (alongside the array/string/tuple sweeps) and the fall-through
  epilogue. Struct move-on-return (`struct_mov`) excludes a returned bare
  struct local from the sweep; a new struct-specific return-retain
  (`ret_struct_is_borrowed` — a real field / index read or a borrowed-param
  ident, but NOT a fresh literal / constructor call) retains a borrowed
  struct return so the caller owns a counted reference (`ret_value_is_borrowed`
  couldn't be reused — it treats a struct-builder call as borrowed, which
  would over-retain; the struct predicate treats a call as a move). Sound but
  leaky by design: structs stored INTO containers aren't construction-inc'd
  yet (the struct-value construction-inc gap), and the sweep doesn't
  recursively release fields — both mean over-counting (leak), never
  over-release, so the detector stays clean. Coverage:
  `TestSelfHostRcStructBoxWasm` gains struct-swept-clean, struct-alias-clean,
  struct-move-return-clean, struct-borrowed-field-return (the watbin-style UAF
  guard — the borrowed field's rc balances across both decs only because of
  the return-retain) and struct-base-copy-churn-clean (1000 `a = step(a)`
  base-copy cycles, detector 0). Full RC suite + component-adapter + watbin +
  binary wasm tests green; bootstrap-safe. Next is the struct FREE flip:
  per-field rc-kind map → recursive field-release (`if [s-8]==1` last-owner
  guard dec'ing rc-tracked fields, then `$__fern_arr_dec`), struct-value
  construction-incs (so a struct stored in a container survives), AND boxing
  the extern result records (a swept extern struct must carry an rc header).
- 2026-06-09: **wasm struct/enum RC — FREE flipped ON, with RECURSIVE FIELD-
  RELEASE.** Structs/enums are now reclaimed; freeing a struct first releases
  its rc-tracked fields. The three coordinated parts (which MUST land together
  — any one alone is a UAF or over-release): (1) `struct_field_kind_char`
  classifies each field TYPE 'a' array / 's' string / 'S' nested struct-enum-
  tuple (freed one level) / 'i' scalar-or-unprovable (SKIPPED). The
  classifier is asymmetric-by-design: a false pointer-mark on a scalar would
  `$__fern_arr_dec` a raw integer (corruption for a large even value), so it
  is EXACT for pointer types and defaults to 'i' (a missed pointer leaks,
  never corrupts). (2) `emit_struct_release` emits, per swept struct local
  (type recovered from `sv_types`), an inline `if [s-8]==1` last-owner guard
  that dec's the 'a'/'s'/'S' fields BEFORE `$__fern_arr_dec`-ing the box —
  `struct_exit_sweep_excl` now calls it (replacing the counting-stage
  `rc_dec`). (3) struct-value construction-incs at all 7 container-store sites
  (array elem, Some/Ok/Err, variant payload, tuple elem, struct field, AND
  the struct base-copy, which now retains any non-scalar field) so a struct
  stored in a container survives the source's release. The two
  extern/canonical-ABI result records (`extern_emit_record_fill` inner + the
  by-value `@import` result) are migrated to `$__fern_str_box` so a swept
  extern struct carries an rc header (their `[s-8]` read is no longer garbage).
  Move-on-return + the borrowed-return-retain ride unchanged from the counting
  slice. Known sound leaks: an enum local's variant payload and a nested
  struct/tuple's OWN fields aren't reached (one-level release), and a
  loop-rebound `var`/reassigned struct isn't cow-freed — all over-count
  (leak), never over-release. Coverage: `TestSelfHostRcStructBoxWasm` gains
  struct-field-array-released + struct-field-string-released (the source is
  dec'd to 0 by the struct's recursive release), struct-nested-released,
  struct-builder-escape-clean (the UAF guard — the inner survives mk's exit
  only via the construction-inc, then the caller frees it once) and
  struct-array-churn-clean (50k cycles). Full RC suite + all component +
  binary + shim wasm tests green with struct free ON; bootstrap-safe. **This
  completes the struct/enum reclamation pipeline** (layout → counting → free),
  matching arrays/strings/tuples — every primary heap container is now
  reference-counted and reclaimed on the self-hosted wasm backend.
  Two real bugs the free flip surfaced (and that the initial unit tests
  missed — they only showed up in the struct-heavy watbin assembler, exactly
  as the array/string flips each surfaced a mid-way UAF):
  (a) **borrowed-value stores weren't retained.** The construction-incs only
  covered bare-ident aliases; a borrowed *field read* stored into a container
  (`children.append(r.node)` in the recursive WAT parser) wasn't retained, so
  releasing the source struct freed the value out from under the array — a
  use-after-free when the array was later walked. Fixed by `store_value_is_borrowed`,
  which subsumes the four `init_is_*_alias` checks AND adds borrowed struct
  field reads (rc-tracked field type) and struct-/string-array index reads;
  it now gates the retain at ALL store sites including the previously-missed
  `.append()` path. (b) **the recursive release lacked a null guard.** A
  struct/tuple local declared in a loop body or not-taken branch is 0 on a
  path that skips it (e.g. the parser's leaf early-return sweeping the
  loop-body `r`); the inline `[s-8]` rc-read then faulted at address -8.
  Fixed by null-guarding the rc-read in both `emit_struct_release` and
  `tup_exit_sweep_excl` ($__fern_arr_dec already guards null; the inline read
  did not). Both fixes shipped with the slice + the borrowed-field-append and
  conditional-null shapes are covered by the new struct free tests.
- 2026-06-09: **wasm array RC — ELEMENT recursive release (string[] /
  struct-array locals).** Until now an array was freed FLAT — the buffer
  returned to the freelist but its rc-boxed elements leaked. A `string[]` or
  struct-array local now releases its ELEMENTS (one level) when it is the last
  owner, via a new `$__fern_arr_dec_ptr(a)` runtime helper: guarded like
  `$__fern_arr_dec` (null / low-bit / low-addr), and when `[a-8]==1` (about to
  free) it loops `i in 0..len` dec'ing each element pointer at `[a+8 + i*4]`
  with `$__fern_arr_dec` before freeing the buffer. The rc==1 guard means an
  aliased array (rc>1) only decrements the buffer — its elements live on
  through the other owner — so each element is released exactly once, at the
  final free. `arr_exit_sweep_excl` now picks `$__fern_arr_dec_ptr` for a
  local in `str_arrays` or `sa_names` (string / struct elements) and the flat
  `$__fern_arr_dec` otherwise (i32/i64/f64 scalar arrays, and — for now —
  array-of-array / array-of-tuple, which stay one-level-flat). This pairs with
  the construction-incs already retaining borrowed elements on store
  (`store_value_is_borrowed` at the array-literal + `.append()` sites), so the
  element decs balance: a borrowed element (rc 2: source + array) drops to 1
  at the array's release and to 0 at the source's; a fresh element (rc 1, sole
  owned) frees at the array's release. Scope: array LOCALS only — an array
  held in a struct/tuple FIELD is still released one-level-flat by
  `emit_struct_release` / `tup_exit_sweep_excl` (its elements leak), the next
  depth increment. Coverage: new `TestSelfHostRcArrElemWasm` —
  str-array-elem-released, str-array-elem-aliased-clean (the no-over-release
  guard), str-array-append-released, str-array-churn-clean (50k cycles, reclaim
  implied by no growth), struct-array-elem-released,
  struct-array-elem-aliased-clean, str-array-aliased-buffer-clean (element
  release fires once at the last of two array owners). Full RC + component +
  binary + shim + interp + cli wasm suites green; bootstrap-safe.
  Gotcha worth recording: the helper's loop must use NAMED block/loop labels
  (`(block $adp_done (loop $adp_lp … (br_if $adp_done …) … (br $adp_lp)))`),
  NOT anonymous numeric branch depths (`(br 0)` / `(br_if 1 …)`). watbin (the
  self-host WAT→binary assembler) only encodes named-label branches; the first
  cut used numeric depths, which the WAT *interpreter* (wasmtime on `.wat`) ran
  fine but watbin mis-encoded into wrong branch targets — an infinite loop in
  the assembled binary (the `TestSelfHostWasmBinary` wordcount case spun at
  100% CPU). All other emitted loops already use named labels; new runtime
  helpers must too.
- 2026-06-09: **wasm struct RC — DEPTH: pointer-array FIELDS deep-release.**
  Extends the array element release (prev slice) from array LOCALS to array
  FIELDS of a struct. `emit_struct_release` now, for an 'a' (array) field,
  picks `$__fern_arr_dec_ptr` when the field's ELEMENT type is a pointer
  (string / struct / enum / tuple / nested array — via the new
  `array_field_elem_is_ptr`, which strips the `[]` and classifies the element
  with `struct_field_kind_char`) and the flat `$__fern_arr_dec` otherwise.
  So a `struct Bag { items: string[] }` freed now releases the strings too,
  not just the `items` buffer. Critically asymmetric for soundness: a scalar
  array field (`i32[]` whose values may be large even integers that look like
  heap pointers) stays FLAT — `array_field_elem_is_ptr` returns false, so the
  scalar elements are never arr_dec'd (that would corrupt). Pairs with the
  existing field construction-incs (`store_value_is_borrowed` retains a
  borrowed array stored into a field; the array's own elements were retained
  when added) so the decs balance. Still one-level for the array's elements
  (a `Node[]` field deep-releases each `Node` box but not each Node's own
  fields) and tuples' array elements stay flat (tuple kinds don't carry the
  element-element type). Coverage: `TestSelfHostRcStructBoxWasm` gains
  struct-field-strarray-released (strings freed), struct-field-i32array-flat-safe
  (the scalar-safety guard — `i32[]` of heap-address-shaped values must NOT be
  pointer-released), struct-field-strarray-churn-clean (50k cycles). Full RC +
  component + binary + shim + interp + cli wasm suites green; bootstrap-safe.
- 2026-06-09: **wasm struct RC — loop-rebound / reassigned struct RECLAIM
  (cow-dec).** The struct analogue of the array/string `StmtVar`/`StmtAssign`
  cow-dec-on-overwrite: a swept struct local re-bound (`var p = …` in a loop)
  or reassigned (`cx = Ctx{...cx}`, `a = step(a)`) now RELEASES its prior value
  (recursive, via `emit_struct_release`) before storing the new one, instead of
  leaking it. Guarded two ways: an `i32.ne` cow-guard skips an in-place result
  (same pointer — dec'ing would over-release), and the release is gated on
  `struct_swept` membership, which includes only owned LOCALS — a borrowed
  struct PARAM reassigned (`cx` inside `emit_expr`, whose first value is
  caller-owned) is NOT in the set, so its borrowed value is never cow-freed
  (that would double-release with the caller). This reclaims the pervasive
  `cx = Ctx{...cx}` threading intermediates in the compiler's own `emit_func`
  (each old `Ctx` + its retained `string[]` fields freed per update; the
  base-copy's construction-inc of the array fields balances the cow-dec's
  recursive release, transferring ownership to the new struct). Coverage:
  `TestSelfHostRcStructBoxWasm` gains struct-rebind-loop-reclaim (100k `var p`
  re-binds) and struct-reassign-base-copy-reclaim (100k `s = step(s)` with a
  retained `string[]` field — reclaim implied by no growth, detector clean).
  Full RC + component + binary + shim + interp + cli wasm suites green;
  bootstrap-safe.
- 2026-06-09: **wasm tuple RC — DEPTH: pointer-array ELEMENTS deep-release.**
  The tuple parallel of the struct-field deep-release: a tuple holding a
  `string[]` or struct-array element now releases that array's ELEMENTS (one
  level) via `$__fern_arr_dec_ptr`, not just the buffer. The tuple element-kind
  string gains a fourth char: 'A' (pointer-element array → `arr_dec_ptr`)
  alongside 's' string, 'a' scalar array (flat `arr_dec`), 'i' scalar. The
  classification (`tuple_elem_array_is_ptr`) is asymmetric for soundness — a
  bare-ident element in `str_arrays`/`sa_names`, or an array literal whose first
  element is a string/struct, gets 'A'; anything unsure (a slice, an empty
  literal, a plain `i32[]`) stays 'a' (a false 'A' would `arr_dec` scalar values
  as pointers → corruption; a missed 'A' only leaks). 'A' is distinct from the
  's'-checking `tuple_elem_is_string` reader, so no other tuple-typing path is
  affected. Coverage: `TestSelfHostRcTupleBoxWasm` gains tuple-strarray-elem-released
  (strings freed), tuple-i32array-flat-safe (the scalar-safety guard — `i32[]` of
  heap-address-shaped values must NOT be pointer-released), tuple-strarray-churn-clean
  (50k cycles). Full RC + component + binary + shim + interp + cli wasm suites
  green; bootstrap-safe. (Tuples holding struct-array elements free each struct
  box one level — the struct's own fields leak, the same depth bound as
  everywhere; arbitrary-depth nested release would need per-type drop-glue
  functions, deferred.)
- 2026-06-09: **wasm Option/Result RC — rc-box LAYOUT foundation.** Begins
  Option/Result reclamation (they previously leaked entirely — an option local
  was never even swept). A variant box (`[tag@0][payload@4]`, where tag is
  Some/Ok=0, None/Err=1) is now rc-headered via the generic `$__fern_str_box`
  (8-byte rc+bsz header, returns base+8) in `enum_box_retain` — the single
  Some/Ok/Err/None construction path — so it carries an rc word at `[p-8]`
  while `tag@[p]` and `payload@[p+4]` (every p-relative access — `match`
  dispatch, `?` unwrap) are unchanged. The io/extern option builders (read_file
  / write_file / the preview1+2 helpers, which assemble `[tag][payload]` boxes
  raw) are intentionally left raw in this slice — layout-only never sweeps
  options, so a boxed-vs-raw mix is value-safe; they migrate when option
  counting lands (a swept raw option's `[p-8]` read is garbage). Coverage: new
  `TestSelfHostRcOptionBoxWasm` — some-fresh-unique (rc==1), some-match-intact,
  none-match-intact, result-ok-intact / result-err-intact, question-unwrap-intact
  (the `?` path), some-string-payload-intact (all value-correct + detector 0).
  Full RC + component (incl. the read_file/option-heavy io tests) + binary +
  shim + interp + cli wasm suites green; bootstrap-safe; free still OFF. Next:
  box the io/extern option builders + Option/Result COUNTING (sweep option
  locals with inc-on-alias) then FREE with payload recursive release (a
  tag-guarded dec of the Some/Ok payload when it's rc-tracked).
- 2026-06-09: **wasm Option/Result RC — io + map option builders boxed
  (layout completion).** Extends the Option/Result rc-box layout from the
  user-construction path (`enum_box_retain`, prev slice) to the runtime option
  SOURCES that built `[tag@0][payload@4]` boxes raw: the 15 `$tmp`
  `$__fern_alloc(8)` sites across the env / read_file / write_file preview1+2
  helpers, and the `$bx` box in `$__fern_map_get` (map `.get` → Option). All
  now use `$__fern_str_box(8)` so every option/result a program can hold —
  whether `Some(x)` / `Ok`/`Err`/`None` literals, a `read_file` Result, an
  `env` Option, or a map lookup — carries an rc word at `[p-8]` with tag@[p],
  payload@[p+4] unchanged. (The 3 extern `@import` option/struct/enum result
  boxes — `$s`/`$d` — remain raw for now: rarer, and value-safe while option
  free is off; they'll box or be sweep-excluded when counting lands.) This is
  the last layout prerequisite for Option/Result COUNTING — a swept option
  local from any common source now has a valid rc header. Validated by the
  read_file/write_file/env component-io tests + the map suite (the option
  boxes are p-relative, so boxing is transparent to every match/`?`/`.get`
  reader); full RC + component + binary + shim + interp + cli wasm suites
  green; bootstrap-safe; free still OFF. Next: Option/Result counting (sweep
  option locals + inc-on-alias + move/retain) then free with the tag-guarded
  Some/Ok-payload recursive release.
- 2026-06-09: **wasm Option/Result RC — COUNTING milestone (free OFF).** Owned
  Option/Result-value locals are now reference-counted and released (rc DEC via
  `$__fern_rc_dec` — no free, no payload release yet) at function exit,
  mirroring the struct counting shape. New `Ctx.opt_swept` is the swept set,
  built by `collect_opt_swept` (last in `build_ctx`, once `ol_names` is known so
  aliases resolve) from `init_is_owned_option` (a Some/Ok/Err constructor, a
  bare `None`, or an option-returning call — `read_file`/`env`/`map.get`/a
  user fn or method whose return type is Option/Result, via `scrutinee_payload`)
  OR `init_is_option_alias` (a bare-ident co-owner). StmtVar emits
  `$__fern_rc_inc` after an option alias bind; `opt_exit_sweep_excl` runs at
  StmtReturn (alongside the array/string/tuple/struct sweeps) and the
  fall-through epilogue. Option move-on-return (`opt_mov`) excludes a returned
  bare option local from the sweep; `ret_option_is_borrowed` (a field / index
  read or a borrowed-param ident, NOT a fresh Some/Ok/Err / None / option-call)
  adds a borrowed option return to the retain. Sound but leaky by design: the
  Some/Ok payload isn't released yet (no payload recursion) and options stored
  in containers aren't construction-inc'd yet — both over-count (leak), never
  over-release, so the detector stays clean. Coverage:
  `TestSelfHostRcOptionBoxWasm` gains option-swept-clean, option-alias-clean,
  option-string-payload-swept, option-move-return-clean, option-loop-clean
  (1000 iters), and option-readfile-result-swept (an owned Result from a
  builtin is swept — the Err path value-correct + detector 0). Full RC +
  component + binary + shim + interp + cli wasm suites green; bootstrap-safe.
  Next: option FREE with the tag-guarded Some/Ok-payload recursive release +
  option construction-incs (so an option stored in a container survives).
- 2026-06-09: **wasm Option/Result RC — FREE flipped ON, with tag-guarded
  PAYLOAD release.** Options/Results are now reclaimed; freeing a Some/Ok first
  releases its payload. `emit_option_release` emits, per swept option local
  (Some/Ok payload type from ol_payloads), an inline guard chain: null-guard →
  `[p-8]==1` (last owner) → `[p]==0` (tag 0 = the Some/Ok payload-carrying
  variant) → release the payload at `[p+4]` by its kind (string / scalar-array
  flat / pointer-array via `$__fern_arr_dec_ptr` / nested struct one level; a
  scalar payload like `Option<i32>` is SKIPPED — `struct_field_kind_char`
  returns 'i', so a heap-address-shaped i32 is never dec'd as a pointer), then
  `$__fern_arr_dec`s the box; `opt_exit_sweep_excl` now calls it (replacing the
  counting-stage rc_dec). Option construction-incs: `store_value_is_borrowed`
  gains `init_is_option_alias`, so an option stored into a container is retained
  — and the Some/Ok payload retain was already wired via `enum_box_retain`'s
  retain flag (also `store_value_is_borrowed`). Move-on-return + the
  borrowed-return-retain ride unchanged from counting. Known sound leaks: a
  Result's Err payload (tag 1, untracked type) and an option's payload-of-a-
  nested-container beyond one level — over-count, never over-release. Coverage:
  `TestSelfHostRcOptionBoxWasm` gains option-string-payload-released,
  option-i32-payload-flat-safe (the scalar-safety guard),
  option-builder-escape-clean (the payload freed exactly once across mk's move +
  the caller's release) and option-string-churn-clean (50k cycles). Full RC +
  component + binary + shim + interp + cli wasm suites green with option free
  ON; bootstrap-safe. **This completes the Option/Result reclamation pipeline**
  (layout → counting → free) — every primary heap type (arrays, strings,
  tuples, structs/enums, Option/Result) is now reference-counted and reclaimed
  on the self-hosted wasm backend.
- 2026-06-09: **wasm Option/Result RC — loop-rebound / reassigned option
  RECLAIM (cow-dec).** The option analogue of the struct cow-dec-on-overwrite:
  a swept option local re-bound (`var o = Some(…)` in a loop) or reassigned
  (`o = Some(…)`) now RELEASES its prior value (payload-aware, via
  `emit_option_release`) before storing the new one, instead of leaking it.
  Guarded by an `i32.ne` cow-guard (skips an in-place same-pointer result) and
  gated on `opt_swept` membership (owned LOCALS only — a borrowed option PARAM
  reassigned, whose first value is caller-owned, is never cow-freed). So a
  loop-rebound `Some(heap-string)` reclaims both the box AND its string payload
  each iteration. Coverage: `TestSelfHostRcOptionBoxWasm` gains
  option-rebind-loop-reclaim and option-reassign-loop-reclaim (100k cycles each
  — reclaim implied by no growth, detector clean). Full RC + component + binary
  + shim + interp + cli wasm suites green; bootstrap-safe. With this, the
  Option/Result pipeline matches arrays/strings/structs for reclaim-on-overwrite
  too — every primary heap type is fully reference-counted, reclaimed at exit,
  AND reclaimed on overwrite.
- 2026-06-09: **wasm map RC — keys/vals/used arrays rc-boxed + grow-leak
  fixed.** The first map-reclamation slice. A map's three internal arrays
  (keys / vals / used, the open-addressing table) were raw `$__fern_alloc`
  blocks that `$__fern_map_grow` ABANDONED on every resize (a map grown N times
  leaked 3N arrays). They are now allocated via `$__fern_str_box` (flat rc box —
  data ptr at base+8, so the table's `[arr + i*4]` slot addressing is
  unchanged), in both `$__fern_map_new_k` and `$__fern_map_grow`, and
  `$__fern_map_grow` now `$__fern_arr_dec`s the old keys/vals/used buffers after
  rehashing — the string key/value POINTERS are copied into the new arrays
  first, so they survive (flat free of the old buffer only). Transparent to
  every map op (all access is via the stored array pointers). Coverage: new
  `TestSelfHostRcMapGrowWasm` — map-grow-str-keys / map-grow-i32-keys (grow +
  rehash, values intact + detector 0), map-grow-churn (5000 growing-map
  build/drop cycles, reclaim implied by no growth), map-grow-str-vals (string
  values survive the realloc). Full map + RC + component + binary wasm suites
  green; bootstrap-safe. (The map box itself + its FINAL arrays + the string
  keys/values still leak at map death — the map box isn't rc-boxed and map
  locals aren't swept yet; that's the map counting + free slices, which reuse
  the boxed-array foundation laid here.)
- 2026-06-09: **wasm map RC — box layout + COUNTING milestone (free OFF).** The
  map box is now rc-headered via `$__fern_str_box` (returns base+8, so every
  box-relative field `[box+N]` is unchanged) in `$__fern_map_new_k`, and owned
  map locals are reference-counted + released (rc DEC via `$__fern_rc_dec` — no
  free, no key/value release yet) at function exit, mirroring the option
  counting shape. New `Ctx.map_swept` is the swept set, built by
  `collect_map_swept` (last in `build_ctx`, once `map_names` is known) from
  `init_is_owned_map` (a `map_new[_i32]`-rooted construction / `.insert` chain
  via `expr_map_rooted`) OR `init_is_map_alias` (a bare-ident co-owner). A map
  mutated in place (`m = m.insert(…)`) returns the SAME box, so it stays one
  owned local (the StmtAssign falls through to a plain set, no spurious dec).
  StmtVar emits `$__fern_rc_inc` after a map alias bind; `map_exit_sweep_excl`
  runs at StmtReturn (alongside the array/string/tuple/struct/option sweeps) and
  the epilogue. Map move-on-return (`map_mov`) + a map-param return-retain ride
  the existing return machinery. Coverage: `TestSelfHostRcMapGrowWasm` gains
  map-swept-clean, map-str-swept-clean, map-counting-churn-clean (5000 cycles —
  detector 0). Full map + RC + component + binary wasm suites green;
  bootstrap-safe. Next: map FREE — a `$__fern_map_release(box, vis)` helper that
  releases the string keys (kis@[box+20]) / values (vis from map_str_names) of
  the occupied slots, then the keys/vals/used arrays, then the box.
- 2026-06-09: **wasm map RC — bugfix: zero the `used` array on alloc.** A
  latent hang the map-counting work surfaced (pre-existing in the grow-leak-fix
  slice, missed because the new map test matched no CI shard regex): the map's
  open-addressing `used` array relied on being zero-initialised, which held only
  while every array was a FRESH bump allocation. Once `$__fern_map_grow` started
  freeing old arrays to the freelist, `$__fern_alloc` began RECYCLING blocks
  WITHOUT zeroing them — so a reused `used` array carried stale occupancy flags
  (1/2), and `$__fern_map_find`'s probe never found an empty slot → infinite
  loop (a heavily-growing-map churn spun forever). Fixed by explicitly zeroing
  the `used` array (a named-label loop — watbin-safe) after allocation in both
  `$__fern_map_new_k` and `$__fern_map_grow`; keys/vals need no zeroing (only
  occupied slots, gated by `used[]`, are read). Guarded by
  `map-grow-churn`/`map-counting-churn-clean` (5000 growing-map cycles, no hang,
  detector 0).
- 2026-06-09: **wasm map RC — FREE flipped ON.** Maps are now reclaimed. A new
  `$__fern_map_release(box)` helper: when `box` is the last owner ([box-8]==1)
  it frees the three structural arrays (keys @[box+8], vals @[box+12], used
  @[box+16]) FLAT via `$__fern_arr_dec`, then frees the box itself;
  `map_exit_sweep_excl` now calls it (replacing the counting-stage rc_dec). Map
  construction-inc: `store_value_is_borrowed` gains `init_is_map_alias`, so a map
  stored into a container is retained. One level, by design: the string KEYS /
  VALUES held in the keys/vals arrays are NOT released — they aren't
  construction-inc'd into the map (map_set just stores the pointer), so dec'ing
  them would double-free the source string locals; closing that needs map_set
  key/value retains + overwrite-dec, a future refinement. So map free reclaims
  the structural overhead (box + 3 arrays, O(1)+O(cap)); string keys/values
  (often literals → immortal anyway) leak soundly. Coverage:
  `TestSelfHostRcMapGrowWasm` gains map-free-i32, map-free-str, and
  map-free-churn-clean (200k growing maps built + dropped — the box + arrays
  reclaimed each cycle, no OOM, detector 0; 200k unreclaimed maps would exhaust
  memory, so this proves real free). Full map + RC + component + binary + shim +
  interp + cli wasm suites green; bootstrap-safe. **With this, maps join the
  fully-reclaimed set** — every container the language exposes (arrays, strings,
  tuples, structs/enums, Option/Result, maps) is now reference-counted and
  reclaimed on the self-hosted wasm backend.
- 2026-06-09: **wasm Option/Result RC — Result Err-payload release.** Closes the
  documented Err-payload gap: a freed `Result[T, E]` whose Err type E is
  rc-tracked now releases the Err payload too (the tag-1 path). New
  `parse_result_err_payload` extracts E (the part after the first top-level comma
  of `Result[T, E]`); a parallel `Ctx.ol_err_payloads` (populated alongside
  `ol_payloads` at the option-local + param sites) records it. `emit_option_release`
  gains a tag==1 release of `[p+4]` gated on the Err kind (string / pointer-array
  via arr_dec_ptr / nested struct one level; scalar Err like `Result[_, i32]`
  skipped). An Option's `None` is tag 1 with no Err type (`ol_err_payloads` ""),
  so it's naturally skipped. Balances against the existing construction-inc
  (`enum_box_retain`'s retain flag already retains the Err payload of `Err(e)`).
  Coverage: `TestSelfHostRcOptionBoxWasm` gains result-err-string-released,
  result-ok-string-released, result-err-string-churn-clean (50k cycles). Full RC
  + component + binary + shim + interp + cli wasm suites green; bootstrap-safe.
- 2026-06-09: **wasm transitive reclamation — Stage A: per-type struct/enum
  drop functions ($__fern_release_<T>).** Begins closing the deep-nesting parity
  gap vs the native compiler (which generates recursive `__drop_struct_<N>` /
  enum drops for ARBITRARY-DEPTH reclamation; the self-host had stopped at one
  level). A swept struct/enum local now routes through a GENERATED per-type drop
  function: `emit_struct_release` emits `(call $__fern_release_<sty> …)` and
  `struct_enum_drop_helpers` generates, once per module, `$__fern_release_<S>`
  for each struct (its rc==1-guarded field release, where a nested concrete
  struct/enum field RECURSES through that field type's own release fn — deep)
  and `$__fern_release_<E>` for each user enum (flat one level for now). Mutual
  recursion gives arbitrary depth; each fn rc==1-gates internally so a shared
  child at rc>1 just decrements (no over-release); Option/Result fields stay
  flat (their locals use emit_option_release, no $__fern_release_ fn). A new
  `release_module_ctx` builds the minimal type-only Ctx the generator's
  classifiers need (additive — no change to the bootstrap-critical classifier
  call sites). The generated fns are emitted after the rc runtime when the heap
  + any struct/enum type exists. Coverage: new `TestSelfHostRcDeepNestWasm` —
  deep-nested-2 / -3 (transitive free of inner arrays 2 + 3 levels down),
  deep-nested-string (a deep string field freed), deep-nested-churn (50k deep
  trees built + freed, inner arrays reclaimed transitively, no OOM, detector 0),
  deep-builder-escape (the tree freed once across mk's move + the caller's
  release). Full RC + component + binary + shim + interp + cli wasm suites green
  — the compiler's own nested structs/AST route through the generated fns
  safely. **Remaining stages toward full native parity:** arrays-of-structs
  deep-release (`__drop_arr_struct_`), enum variant payload dispatch
  (`__drop_enum_` / `genEnumDrops`), tuple drop fns, map string keys/values,
  closure-env capture release.
- 2026-06-09: **wasm transitive reclamation — Stage B: arrays-of-structs
  deep-release ($__fern_arr_release_<S>).** Continues closing the deep-nesting
  parity gap (native's `__drop_arr_struct_<Elem>`). For each struct type S,
  `struct_enum_drop_helpers` now also generates `$__fern_arr_release_<S>(a)`: when
  the array is the last owner ([a-8]==1) it loops over the elements calling
  `$__fern_release_<S>` per element (so each element's OWN fields reclaim, not
  just the box), then frees the buffer. Routing: `struct_release_field_inner`
  sends a STRUCT-element array field (`S[]`) to `$__fern_arr_release_<S>` (a
  string-element array stays `arr_dec_ptr`, a scalar array flat), and
  `arr_exit_sweep_excl` sends a struct-array LOCAL (sa_names → sa_types) there
  too. Mutual recursion `$__fern_release_<S>` ↔ `$__fern_arr_release_<S>` (a
  struct whose field is `S[]`, e.g. `Node { items: Node[] }`) reclaims the WHOLE
  tree to arbitrary depth — the recursive WAT-parser shape (`children.append(
  r.node)`) now fully frees. Coverage: `TestSelfHostRcDeepNestWasm` gains
  arr-of-struct-released, arr-of-struct-churn (50k arrays-of-structs-holding-
  arrays, transitive free, no OOM), node-tree-deep-released (the `Node[]`-field
  tree fully reclaimed). Full RC + component + binary + shim + interp + cli wasm
  suites green — the compiler's own AST (Stmt[] / Expr[] / … struct arrays) all
  route through the deep release safely; bootstrap-safe. Remaining toward full
  native parity: enum variant payload dispatch (`genEnumDrops`), tuple drop fns,
  array-of-enum / array-of-tuple deep release, map string keys/values,
  closure-env capture release.
- 2026-06-09: **wasm transitive reclamation — Stage C: enum variant payload
  dispatch ($__fern_release_<Enum> → struct_id).** Closes the enum half of the
  deep-nesting gap (native genEnumDrops). An enum value is a variant box
  `[struct_id@0][fields@4…]`; `$__fern_release_<E>` (previously a flat free) now
  dispatches on the tag — `if [p]==struct_id(Vi) → $__fern_release_<Vi>(p)` for
  each variant Vi — routing to the variant STRUCT's own release fn (already
  generated in the struct loop, since variants are structs), which releases that
  variant's payload fields (deep, transitively) then frees the box. A struct_id
  matching no variant falls through to a flat free. Option/Result keep using
  emit_option_release (their tag-guarded payload release), unchanged. Because
  the variant fns are the same `$__fern_release_<S>` used everywhere, enum
  payloads that are themselves nested (a variant holding a struct holding an
  array, or another enum) reclaim to arbitrary depth — the AST (`parser.Expr`
  and friends, the most enum-heavy structure) now deep-frees. Coverage:
  `TestSelfHostRcDeepNestWasm` gains enum-string-payload-released,
  enum-array-payload-released, enum-payload-churn (50k enum-with-string-payload,
  reclaimed each cycle, no OOM, detector 0). Full RC + component + binary + shim
  + interp + cli wasm suites green — the compiler's own enum-heavy AST routes
  through the variant dispatch safely; bootstrap-safe. Remaining toward full
  native parity: tuple drop fns, array-of-enum / array-of-tuple deep release,
  map string keys/values, closure-env capture release.
- 2026-06-09: **wasm transitive reclamation — Stage D: array-of-enum
  deep-release ($__fern_arr_release_<Enum>).** For each user enum E,
  `struct_enum_drop_helpers` now also generates `$__fern_arr_release_<E>(a)`: per
  element call the enum's DISPATCHING release fn `$__fern_release_<E>` (so each
  element's variant payload reclaims), then free the buffer. Routing:
  `struct_release_field_inner` sends an enum-element array field (`E[]`) there,
  and `arr_exit_sweep_excl` sends an enum-element array LOCAL (sa_types is an
  enum) there. This drives the deepest part of the compiler's own AST — the
  `Stmt[]` / `Expr[]` arrays (Expr is an enum) — which previously freed only one
  level (the variant boxes, leaking their payloads). Now a `Expr[]` of
  `ExprBinary { left: Expr, right: Expr }` reclaims the whole subtree.
  Coverage: `TestSelfHostRcDeepNestWasm` gains arr-of-enum-released and
  arr-of-enum-churn (50k arrays-of-enums-with-string-payloads, every element
  payload reclaimed, no OOM, detector 0). Full RC + component + binary + shim +
  interp + cli wasm suites green — the compiler's own Stmt[]/Expr[] route
  through the deep enum-array release safely; bootstrap-safe. With Stages A–D
  the four composite carriers (nested struct fields, arrays-of-structs, enum
  variant payloads, arrays-of-enums) all reclaim transitively to arbitrary
  depth — the struct/enum/array AST is fully deep-freed. Remaining toward exact
  native parity: tuple drop fns + array-of-tuple, map string keys/values,
  closure-env capture release.
- 2026-06-09: **wasm transitive reclamation — Stage E: tuple struct/enum
  element deep-release (native __drop_tuple_).** `tup_exit_sweep_excl` now, for
  each tuple element carrying a struct/enum svtype (from `tup_svtypes`, even
  though its kind char is 'i'), deep-releases it through `$__fern_release_<T>`
  (struct field release / enum variant dispatch) instead of skipping it — so a
  tuple holding a struct-with-an-array, or an enum-with-a-payload, reclaims to
  arbitrary depth. ('s'/'a'/'A' element kinds keep their string / scalar-array /
  pointer-array release; Option/Result svtypes route to their own
  emit_option_release path elsewhere, so they're excluded here.) Coverage:
  `TestSelfHostRcDeepNestWasm` gains tuple-enum-payload-released (a Circle string
  payload in a tuple, freed; value via match) and tuple-struct-element-churn
  (50k struct-in-tuple, the Inner struct + its array reclaimed each cycle, no
  OOM, detector 0). Full RC + component + binary + shim + interp + cli wasm
  suites green; bootstrap-safe. (Noted in passing, NOT fixed here: tuple-of-
  struct FIELD access `t.0.field` reads 0 — a pre-existing, RC-orthogonal value
  bug in tuple-element typing; the enum-in-tuple match path is unaffected and
  the deep-release is sound regardless.) Remaining toward exact native parity:
  array-of-tuple deep release, map string keys/values, closure-env captures.
- 2026-06-09: **wasm map RC — string KEY release.** Closes the wordcount-style
  leak: a string-keyed map's heap keys (e.g. words read from input) are now
  reclaimed on the map's death. `$__fern_map_set` construction-incs the key on a
  NEW insert when `kis@[box+20]==1` (a guard no-op for an immortal string
  literal — the common case; a heap key now reclaims; i32 keys carry no rc), and
  `$__fern_map_release` now, when the map is the last owner and kis==1, loops the
  occupied slots (used[i]==1) releasing each key via `$__fern_arr_dec` before
  freeing the keys buffer. Balances: a heap key local (rc 1, str_swept) inc'd on
  insert (rc 2) is dec'd at its own exit (→1) and by map_release (→0). The grow
  rehash copies key pointers (no re-inc) and frees the old keys buffer flat, so
  key rc is stable across resize. (A key on a DELETED slot — tombstone, used==2
  — isn't released: a sound leak, since delete doesn't dec the key; closing that
  is a follow-up. String VALUES still leak — they need a box `vis` flag, the
  next slice.) Coverage: `TestSelfHostRcMapGrowWasm` gains map-heap-key-released,
  map-literal-key-clean, map-heap-key-churn (50k heap-keyed maps, keys reclaimed,
  no OOM, detector 0). Full map + RC + component + binary + shim + interp + cli
  wasm suites green; bootstrap-safe.
- 2026-06-09: **wasm map RC — string VALUE release (native
  __drop_map_str_values).** Symmetric to the key slice: a string-valued map's
  heap values are now reclaimed on the map's death. The map box grows to 32
  bytes with a `vis` (value-is-string) flag at `+28`, mirroring `kis@+20`. The
  `.insert(k, v)` call site decides `vis` statically from the value expr's type
  (`is_string_expr`) — a map's value type is homogeneous, so the per-insert flag
  is stable — and passes it as a 4th `$__fern_map_set` argument. `map_set`
  records `vis` on the box and, when set, construction-incs the value on a NEW
  insert AND, on an OVERWRITE of an existing key, releases the OLD value before
  storing + incs the new (so a replaced value doesn't leak and the new isn't
  over-released). `$__fern_map_release`, when the map is the last owner and
  vis==1, loops the occupied slots releasing each value via `$__fern_arr_dec`
  before freeing the vals buffer — balancing the insert inc against the source
  local's own sweep (get_or results are borrows, excluded from str-sweep by
  `init_is_owned_string`, so no double-dec). `map_new_k` zeroes `vis@28` (a
  freelist-recycled box carries stale bytes); the grow rehash copies value
  pointers unchanged, so value rc is stable across resize. Works for every
  K/V combo (i32-keyed/string-valued via `map_new_i32`, string-keyed/string-
  valued via `map_new` — both kis + vis release independently). Coverage:
  `TestSelfHostRcMapGrowWasm` gains map-str-value-released, map-str-value-
  literal-clean, map-str-value-overwrite, map-str-key-and-value, and map-str-
  value-churn (50k heap-valued maps, values reclaimed each cycle, no OOM,
  detector 0). Full map + RC suites + bootstrap fixpoint + binary + component
  wasm suites green; bootstrap-safe. Remaining toward exact native parity:
  array-of-tuple deep release, closure-env capture release.
- 2026-06-09: **wasm transitive reclamation — Stage F: array-of-tuple
  deep-release (native __drop_arr_tuple_).** An array-of-tuple local
  (`var ps = [(a, b), …]`) now deep-releases each element tuple's
  pointer-bearing fields before freeing the element box + the buffer, instead
  of the flat one-level `arr_dec` that leaked the inner strings / arrays /
  structs. `collect_tuple_locals` additionally records array-of-tuple locals
  (`ta_names` → element kind string `ta_kinds` + struct/enum svtype CSV
  `ta_svtypes`, taken from the literal's first tuple element) parallel to the
  tuple-local tracking. `arr_exit_sweep_excl` routes a `ta_names` local to an
  inline loop: when the array is the last owner (`[a-8]==1`) it walks the
  elements (`len@[a]`, element box `@[a+8+i*4]`), and for each tuple box —
  itself rc==1-gated — releases its pointer-bearing elements via the new shared
  `tuple_release_inner` (struct/enum svtype → `$__fern_release_<T>`; 's'/'a' →
  flat `arr_dec`; 'A' → `arr_dec_ptr`; scalar 'i' skipped — the same classifier
  the tuple-local sweep uses, now factored out), then frees the box; finally the
  buffer is freed. Three reserved scratch locals (`$__tai`/`$__tan`/`$__te`)
  back the loop. Coverage: `TestSelfHostRcDeepNestWasm` gains
  arr-tuple-string-released, arr-tuple-string-churn (200k arrays-of-(i32,string)
  reclaimed, no OOM), arr-tuple-arrelem-churn (200k tuples-holding-i32[]
  reclaimed), and arr-tuple-struct-elem-churn (50k tuples-holding-a-struct-with-
  an-array, deep-released via `$__fern_release_Inner`). Full RC + bootstrap
  fixpoint + binary + component wasm suites green; bootstrap-safe. Remaining
  toward exact native parity: closure-env capture release (`__fern_arr_closure`
  + the per-closure drop thunk), and map struct/enum VALUES (deep
  `__drop_map_via_`, beyond the string-value column).
- 2026-06-09: **wasm closure-env reclamation — Slice 1: rc-box the env +
  free the box.** The closure-RC subsystem the self-host lacked entirely
  (closures were raw-`$__fern_alloc`'d and NEVER freed — full leak of box +
  captures). This slice lays the counting/free foundation, mirroring how the
  struct/option/map layouts were staged. The lambda env block is now allocated
  via `$__fern_str_box` (8-byte rc+bsz header, returns base+8) instead of
  `$__fern_alloc`, so it carries an rc word at `[box-8]` while `table_idx@0` +
  `captures@4+i*4` (the call_indirect dispatch + the body's `$__env` loads) are
  unchanged. `collect_clos_swept` records closure locals bound directly to a
  lambda literal (`var f = function(…){…}`) — freshly rc-boxed, owned envs —
  into `clos_swept` (aliases, closure-returning calls, and bare function-name
  values are excluded: they leak one level, sound). `clos_exit_sweep_excl`
  frees each via the shared `$__fern_arr_dec` at function exit (both the
  return-path and fall-through sweeps), with a `clos_mov` move-on-return
  exclusion so a returned closure is handed to the caller at its rc rather than
  freed. Captures still leak one level (a string/array capture is stored
  without a construction-inc, so the source local's own sweep frees it once and
  the flat box free doesn't touch it — detector stays 0); per-capture release
  is Slice 2. Coverage: `TestSelfHostRcClosureWasm` (clos-box-freed,
  clos-multi, clos-string-cap, clos-scalar-churn — 200k scalar-capture closures
  reclaimed, no OOM — and clos-return, the move-on-return no-double-free path).
  Full RC + bootstrap fixpoint + binary + component + closures suites green;
  bootstrap-safe (the compiler's own lambdas now rc-box + free).
- 2026-06-09: **wasm closure-env reclamation — Slice 2: per-capture release.**
  Completes the closure-RC subsystem (native genClosureDropThunk parity): a
  closure's rc-tracked captures (string / array / struct / enum) now reclaim on
  the closure's death, balanced by a construction-inc at the env's build site.
  A shared `capture_kind(name, cx)` classifier drives BOTH sides so they're
  exactly balanced: "" scalar/fn/map/option/tuple (skip), "s" string, "a"
  scalar array, "p" pointer-element array (string[]/struct[], one level via
  arr_dec_ptr), "S:<T>" struct/enum value (deep via $__fern_release_<T>). At the
  lambda build site, each capture whose kind is non-"" is `$__fern_rc_inc`'d
  after being stored into the env (the env co-owns it). At the closure's death,
  `clos_release_snippet` emits the rc==1-gated release of each capture (loaded
  from env 4+i*4 via `capture_release_op`) BEFORE the env block is freed —
  parallel to clos_swept via `clos_caps` (computed from the full cx so capture
  types resolve; `clos_snippet_for` finds each swept local's lambda). The
  inc/release pair balances against the source local's own sweep (source rc1 →
  inc rc2 → source swept rc1 → closure death rc0 free), so the over-release
  detector stays 0. Coverage: `TestSelfHostRcClosureWasm` gains
  cap-string-released, cap-string-churn (200k string-capturing closures
  reclaimed, no OOM), cap-array-churn (200k i32[]-capturing), cap-struct-churn
  (200k struct-with-array-capturing, deep-released via $__fern_release_Inner),
  and cap-mixed (string released + scalar skipped). Full RC + bootstrap fixpoint
  + binary + component + closures suites green; bootstrap-safe. With this, every
  heap shape the self-host allocates — string, array, struct, enum, tuple,
  option/result, map (string K/V), and now closures (box + captures) —
  reference-counts and reclaims. Remaining vs exact native parity: map
  struct/enum VALUES (deep __drop_map_via_, beyond the string-value column) +
  deeper closure-capture shapes (struct-array / array-of-tuple captures route
  one level).
- 2026-06-09: **wasm map struct/enum VALUE deep release (native
  __drop_map_via_) + the coupled get_or value-typing fix.** The last native
  drop-glue function. A struct/enum-valued map's exit sweep now routes to a
  per-type `$__fern_map_release_via_<T>` (a `$__fern_map_release` variant whose
  value column is deep-released through `$__fern_release_<T>`), so each value's
  own fields / variant payload reclaim — not just the value box. The map box's
  `vis` flag is generalised from "value is string" to "value is rc-pointer"
  (string OR struct/enum), so `$__fern_map_set` construction-incs the value and
  the value reclaim is balanced. New value-type tracking (`mvs_names` →
  `mvs_types`, inferred from struct-literal insert values via
  `set_chain_val_struct_type` / `map_val_struct_type`) drives the routing AND
  fixes a coupled, pre-existing VALUE bug: `var p = m.get_or(k, d)` on a
  struct-valued map now types `p` as that struct, so `p.field` resolves (it read
  0 before — struct-valued maps were effectively unreadable). The per-type
  helper is emitted only when `module_uses_struct_val_map` (any plausibly-
  struct-typed `.insert` value) holds, so map-less / scalar-valued-map programs
  carry no dead helpers; Option/Result values keep their own release path
  (routing + via-generation both exclude them). Coverage:
  `TestSelfHostRcMapStructVal` (map-struct-val value-correctness;
  map-struct-deep-churn — 200k struct-with-array-valued maps, the array field
  reclaimed via via_Inner across cycles, no OOM; map-strkey-structval — string
  key + struct value both reclaim; map-enum-deep-churn — 200k Circle-string-
  payload enum values deep-released via via_Shape dispatch). Full RC + bootstrap
  fixpoint + binary + component wasm suites green; bootstrap-safe. **This
  completes the Perceus port: every heap shape the self-host allocates now
  reference-counts and reclaims to native parity** — string, array (scalar /
  ptr / struct / enum / tuple element), struct (deep fields), enum (variant
  dispatch), tuple, array-of-{struct,enum,tuple}, option/result (incl. Err),
  map (string keys, string + struct/enum values), and closures (env box +
  string/array/struct/enum captures). Residual one-level (sound-leak) corners,
  matching or narrower than the native edge cases: struct-array / array-of-tuple
  closure CAPTURES route one level (vs the deep per-element release the direct
  locals get), and map ARRAY values aren't tracked (only string + struct/enum).
- 2026-06-09: **wasm closure struct-array capture — deep release + the coupled
  capture-typing fix.** Closes a residual: a closure capturing a struct/enum
  ARRAY (`var f = function(){ … ps[i].field … }` over `ps: Inner[]`) now (1)
  keeps the captured array's element type inside the lambda body so
  `cap[i].field` resolves — it read 0 before, a capture-typing gap separate from
  RC that made struct-array captures value-unusable — and (2) deep-releases each
  element through `$__fern_arr_release_<Elem>` on the closure's death (native
  arrElemStructDropName parity), not just the buffer. The typing fix threads
  `cap_sa_names` / `cap_sa_types` (struct-array captures, computed in
  emit_lambda from the parent's `sa_names`) through `emit_func` → `build_ctx`,
  which seeds them into the lambda's `sa_names` (mirrors the cap_sv struct-
  capture seeding). The RC deepening adds an "A:<Elem>" `capture_kind` routed by
  `capture_release_op` to `$__fern_arr_release_<Elem>` (the construction-inc
  side already fired — sa_names captures were rc-tracked). Coverage:
  `TestSelfHostRcClosureWasm` gains cap-structarr-val (value-correct field
  access, the typing fix) and cap-structarr-churn (200k struct[]-capturing
  closures, each element's array field reclaimed, no OOM). Full RC + bootstrap
  fixpoint + binary + component + closures suites green; bootstrap-safe. Sole
  remaining residual vs exact native parity: map ARRAY values aren't value-type-
  tracked (string + struct/enum are) + array-of-tuple closure captures route one
  level.
- 2026-06-09: **wasm map ARRAY value reclaim (one level).** Closes the last
  remaining map-value residual: an array-valued map (`Map[K, i32[]]` etc.) now
  construction-incs its array value on insert and reclaims the buffer on the
  map's death. The map value rc-flag (vis) is extended from "string OR
  struct/enum" to also cover arrays via `insert_val_is_array` (array literal,
  non-substring slice, or an arr_src-tracked ident — a scalar / string value
  never matches, so the flat arr_dec on death can't corrupt a non-pointer). The
  generic `$__fern_map_release` value loop's `arr_dec` then frees the buffer —
  COMPLETE for a scalar-element array (i32[]/i64[]/f64[], the dominant case);
  a string[]/struct[] value's elements leak one level (a sound residual — deep
  per-element array-value release would need element-type routing). Array values
  were already readable (index reads are type-agnostic, unlike struct field
  access), so this is purely the RC gap. Coverage: `TestSelfHostRcMapGrowWasm`
  gains map-arrval-get (value-correct), map-arrval-churn (200k i32[]-valued maps,
  buffers reclaimed, no OOM), and map-strkey-arrval-churn (string key + array
  value both reclaim across 200k). Full RC + bootstrap fixpoint + binary +
  component wasm suites green; bootstrap-safe. With this, the only residuals vs
  EXACT native parity are deep-element reclaim of pointer-element-array map
  VALUES (string[]/struct[]) and array-of-tuple closure CAPTURES — both
  one-level sound leaks of inner elements, the buffers/boxes themselves reclaim.
- 2026-06-21: **Open gap found — the in-place `.append` GROW path does not
  reuse/free the old buffer** (audit of `emit_ir_runtime` per #3452's footnote).
  The deep-reclaim parity above covers every heap shape on *death*, but the
  amortised-growth realloc inside `__fern_arr_push` (asm_ir.fern `.Larr_push_grow`,
  mirrored in asm.fern) doubles into a fresh `__fern_arr_box`, copies, and
  **abandons the previous buffer** — the native side reclaims it via the reuse
  spine (`__fern_alloc_reuse` / reuse-token emit), the self-host in-place append
  does not. Consequence: routing a large module (parser / asm / irlower) through
  `emit_module_ir` leaks one buffer per grow and exit-137 OOMs the **per-module**
  self-compile of those modules — so the per-module byte-identical fixpoint does
  not yet converge (gen2's emit of the big units dies). It is the self-host twin
  of the native large-tier leak #3452 and the concrete blocker between the proven
  per-module emit+link (#3456) and AST-emitter retirement (#3457). A runtime-only
  free is **unsound**: today's lowering relies on `__fern_arr_push` never freeing
  because borrow-form appends can hand it an ALIASED (rc>1) buffer (irlower.fern:
  "arr_push's … returned an ALIASED buffer leaves it rc>1"), so adding the free
  turns the OOM into a SIGSEGV (verified). The sound fix is the reuse slice of
  this port: route every non-consume / borrow append through the clone form
  (`lower_arr_append_value`) so `op_arr_push` only ever sees uniquely-owned
  arrays, drop the self-consume (`a = a.append(v)`) reassign-dec so the helper
  owns the old→new transition, then reclaim-on-grow (FBIP reuse of the old box).
  Both grow sites now carry a LOAD-BEARING-LEAK warning comment so the footgun
  isn't "optimised" into a double-free again.
- 2026-06-21: **Sole-owner append reclaim-on-grow (`op_arr_push_owned`).** The
  sound form of the reclaim the previous entry warned the shared helper can't do.
  A new IR op `arr_push_owned` is emitted in the two append sites where the buffer
  is PROVABLY sole-owned: (1) the self-reassign `a = a.append(v)` when `a` is not
  in `aliased_names` (mirroring the `.with` aliased gate, #3599), and (2) the
  clone form `lower_arr_append_value` (the `Builder { xs: s.xs.append(v) }`
  immutable-update), whose freshly-sliced clone is sole-owned by construction. The
  x86 backend lowers it to `__fern_arr_push_owned` — a thin wrapper that calls
  `__fern_arr_push` and, iff the result differs (a grow realloc), pushes the dead
  old buffer onto its size class. arm64 + wasm alias it to the plain push for now
  (reclaim is x86-only; they stay leak-but-sound). Closes the `a = a.append(v)`
  self-append leak on the self-host-emitted runtime (the #3452 repro shape:
  exit-137 → runs clean). Verified: x86 default fixpoint byte-identical
  (`mmc==gen2==gen3`), whole-compiler per-module self-compile, self-host IR append
  + per-module-link suites, native push/aliased/reclaim, wasm RC reclaim — all
  green; aliased self-appends still copy. **NOT yet converged**: the per-module
  self-compile of the large modules still OOMs because the DOMINANT remaining leak
  is the *escaping struct* — an `EmitState`/`LowerState` passed as a call argument
  escapes (`reclaimable_names`), which disables struct-box reclaim, so the threaded
  builder boxes *and their array fields* leak. That escaping-struct reclaim is the
  next slice toward per-module convergence (#3457), not the append path.
- 2026-06-21: **Borrow-aware struct reclaim — local consume-rebinds (#3456,
  slice 1).** `reclaimable_names_of` switched from the crude `walk_stmts_escapes`
  to the borrow-AWARE `body_unsafe_for` (the same predicate the array reclaim
  uses): a method RECEIVER (`s = s.emit(op)`) and a BORROWABLE free-call arg
  (`s = bump(s)`, callee only field-reads its param) now count as borrows, not
  escapes — so a fresh, non-aliased, non-returned struct LOCAL threaded through a
  consume-rebind is reclaimed. Wired `slot_is_reclaimable_struct` into the
  `StmtAssign` reassign's `do_dec` (the `StmtVar` branch already had it), so each
  rebind frees the old box with a cow-guarded, rc-guarded SHALLOW `arr_dec`
  (box-only; the builder's shared field pointers keep rc==1 across the chain and
  are freed once at the final box's death — no alias-inc needed, no UAF).
  Measured: a scalar consume-rebind churn (200M `s = bump(s)`) goes exit-137 →
  exit-0; value-correct. Verified: x86 default fixpoint byte-identical
  (`mmc==gen2==gen3`), whole-compiler per-module self-compile, struct/RC IR
  suites; one IR case (`escape-call-arg-not-freed` → `borrow-call-arg-reclaimed`)
  updated to the now-sound reclaim. New `TestSelfHostStructConsumeRebindReclaimIRX86_64`.
  **Still leaves the dominant compiler leak**: a builder PARAM
  (`function f(s: EmitState): EmitState { s = s.write(x); return s; }`) — params
  are excluded because the FIRST reassign's old value is the caller's original.
  The sound fix (slice 2) is to SNAPSHOT the param at entry and free a reassigned
  old value only when it differs from the snapshot (rc/cow-guarded): a function
  reclaims its OWN intermediate builder boxes, never the caller's original — no
  interprocedural ownership inference needed. That is what converges the
  per-module self-compile of the large modules.
- 2026-06-21: **Snapshot-param reclaim landed (#3456 slice 2).** The builder
  `function f(s: S): R { s = s.step(x); …; return … }` threads a struct PARAM /
  receiver through a consume-rebind. Params can't use the normal reclaim
  (caller-owned; the final value may share the caller's fields), so lower_func
  snapshots the param's ENTRY box into a hidden `$snap$<name>` local
  (snapshot_param_names_of, gated by body_unsafe_for_allow_ret — a bare
  `return s` is the move-out, allowed), and each reassign frees the old box only
  when it differs from BOTH `new` (cow) AND the snapshot — via the helper
  `__fern_snapshot_dec(new, old, snap) -> new` (rc-guarded SHALLOW free,
  pass-through of `new`). A function reclaims its OWN intermediate boxes, never
  the caller's original, with NO interprocedural inference. x86 reclaims; arm64 +
  wasm are leak-safe PASS-THROUGHs (the owned helper a later slice), mirroring
  op_arr_push_owned. The 4-op call site is essential — the inline ~16-op form
  OOMed the native driver's emit of the large modules (isolated + measured).
  Verified: scalar param-threading churn (3M×200 boxes) 137→0; caller-original
  intact; heap-field param builder value-correct; x86 default fixpoint
  byte-identical (mmc==gen2==gen3); whole-compiler per-module self-compile;
  struct/RC + wasm struct suites green.
  **CORRECTION to the previous entry**: this does NOT by itself converge the
  per-module self-compile. The DOMINANT remaining leak is the clone-form REPLACED
  array field — `LowerState { ops: s.ops.append(op), … }` clones `s.ops` each emit
  and a shallow box-free can't free the dead SOURCE array, so the ops arrays leak
  O(K²) per function (confirmed: a clone-form-field-append churn still OOMs WITH
  this slice). Convergence needs FIELD-LEVEL move tracking — free the replaced
  fields, keep the shared ones — the deep Perceus reuse piece, the genuine next
  slice.
- 2026-06-21: **Field-level move tracking — `__field_reclaim_<T>` mechanism
  landed (sound, validated); detection-gap finding for the per-module
  convergence.** The per-type helper `__field_reclaim_<T>(new, old, snap) -> new`
  (port of native's `__drop_<T>` glue): for each rc-tracked ARRAY field i it frees
  `old.field[i]` via `__fern_rc_dec` iff it differs from BOTH `new.field[i]` (cow —
  the field new still owns) AND `snap.field[i]` (the caller's original, when snap !=
  0), then frees the old box via `__fern_snapshot_dec`. The two guards make it sound
  with NO static field-alias analysis (it frees only buffers neither survivor points
  at; a buffer aliased by a THIRD live local is `__fern_rc_dec`'d, not unconditionally
  freed, so its rc respects that owner — safe-leak, never UAF). Built from
  `struct_has_reclaim_array_field` + `emit_field_reclaim_store` (irlower), gated to
  fire wherever `__fern_snapshot_dec` would for a struct with array fields: the
  snapshot-param rebind (`emit_snapshot_store` path) AND the local consume-rebind
  (`StmtAssign` reclaimable-struct path, snap = 0 → only the != new guard). It MUST be
  a per-type helper (≈4-op call site), never inline — the prior session proved the
  inline ~16-op×hundreds-of-rebinds form OOMs the native driver's emit; the body is
  emitted ONCE per type. Multi-backend: x86 emits the real per-type reclaim body
  (`emit_ir_field_reclaims`, `.weak` so per-module units link-dedupe, calling the
  entry-exported `__fn___fern_arr_dec` + `__fn___fern_snapshot_dec` — no direct
  `.bss` freelist reference, so a library unit's body resolves cleanly); arm64 +
  wasm emit a leak-safe PASS-THROUGH (`ldr x0,[sp,#32]` / `local.get $new`),
  mirroring `op_arr_push_owned` / `__fern_snapshot_dec`. Validated: x86 default
  fixpoint byte-identical (mmc == gen2 == gen3, `TestSelfHostModloadFixpointX86_64`);
  `TestSelfHostStage2FixedPoint`; whole-compiler per-module emit
  (`TestSelfHostModloadPerModuleWholeCompilerX86_64`); the RC/struct IR suites
  (StructRCIR / ConsumeRebindReclaimIR / SnapshotParamReclaimIR / StructFieldDropIR);
  wasm struct + deep-nest RC; and a new `TestSelfHostFieldReclaimIRX86_64` proving
  the mechanism CONVERGES a clone-form-field-append churn (a builder grown then
  cleared, 200M iterations: exit-137 on origin/main → exit-0 with this slice,
  measured against a baseline driver) plus value-correctness (builder read back
  after threading) and caller-original-intact (the snapshot/caller buffer survives).
  **Honest scope — does NOT yet converge the per-module SELF-compile of the big
  units.** Measurement of `mmc` (the self-host-built compiler) shows the mechanism
  is correct but currently INERT on the real workload: across the WHOLE self-host
  compiler `mmc` emits **0** `__field_reclaim_<T>` calls and only **1**
  `__fern_snapshot_dec` call. The dominant `LowerState`/`EmitState` builders thread
  through CONSUME-HELPER CHAINS (`s = lower_expr(e, s)`, `s = lower_stmt(st, s)`),
  where `s` is passed to a callee that consumes-and-returns it — a non-borrowable
  call, so the snapshot/reclaim DETECTION (`body_unsafe_for_allow_ret` /
  `reclaimable_names_of`) classifies it an ESCAPE and never marks `s` a snapshot
  param or reclaimable local. So neither `__fern_snapshot_dec` nor `__field_reclaim`
  fires there, and the per-module emit of the big units (parser/asm/irlower) still
  exhausts the static `.bss` bump heap mid-emit (manifests as SIGSEGV 139, the
  bump-past-`.bss`, IDENTICAL on origin/main — this slice neither fixes nor regresses
  it). The genuine NEXT slice is therefore the consume-call MOVE detection: recognise
  `s = f(…, s, …)` (a consume-and-return of the same struct, ownership transfer) as a
  MOVE rather than an escape, which will activate BOTH the snapshot-param reclaim AND
  this field-level helper across the builder-threading hot path. This slice lands the
  field-free MECHANISM (the prerequisite the detection slice will switch on),
  byte-identical and fully tested; it is necessary but not sufficient, exactly as the
  snapshot-param and local-reclaim slices before it.
- 2026-06-22: **Consume-call move detection — measurement-first probe (reverted);
  the lever + the sound requirements located.** Followed up the previous entry's
  "next slice" by probing whether relaxing the snapshot/reclaim DETECTION actually
  activates the reclaim on the real workload. Two aggressive (deliberately unsound)
  relaxations were applied to `irlower.fern` and measured against a self-host-built
  `mmc`, then reverted:
    1. recognise `name = g(…, name, …)` as a safe consume-rebind in
       `stmt_unsafe_for` / `stmt_unsafe_for_allow_ret` (instead of an escape);
    2. accept a call/method-bound struct local (`var st = se.emit(op)`) as
       reclaim-eligible in `is_fresh_struct_init` (today it accepts only a direct
       struct LITERAL — the comment there already flags "a struct-returning call is
       a move too, … left to leak").
  **Result (driver0 per-module IR emit, the forced-IR path):** with the relaxations
  the IR path emits **1944** `__field_reclaim_<T>` call sites in one unit, **69** in
  the irlower unit, **36** in another — i.e. the DETECTION is the lever; the
  field-free mechanism from the prior slice fires en masse once the threaded
  builders are admitted. (Whole-program `mmc` still showed 0 because that build
  routes these modules differently from the forced-IR per-module path — an
  IR-eligibility / routing axis, goal #1, separate from the reclaim analysis.)
  **Why it can't land as-is (both relaxations are UNSOUND without the ownership
  analysis):**
    - relaxation 1 frees `name`'s old box after `name = g(…, name, …)`, which is a
      UAF if `g` RETAINS `name` (stores its box/fields somewhere that outlives the
      call). Soundness needs `infer_param_escapes` (native ir.go:1336, analysis #2
      in §5): a param position is consume-safe iff the box neither escapes nor is
      freed by the callee.
    - relaxation 2 reclaims `var st = f(…)` even when `f` returns its (borrowed)
      PARAM — then `st` ALIASES the caller's box and reclaiming `st` double-frees.
      Soundness needs a FRESH-return classification (the result is a freshly
      constructed/updated box, never a bare param ident) — the move/escape side of
      the same analysis. The probe also broke per-module eligibility for two units
      (0 bytes), confirming the all-calls form is too coarse.
  **So the next slice is the ownership/escape analysis itself** (the documented
  `infer_param_escapes` + a fresh-struct-return classifier), computed ONCE per
  module and threaded like `borrowable_params_interproc` — NOT a further detection
  shortcut. It must be precise: a single over-approximation frees a retained/aliased
  box and breaks the byte-identical fixpoint with a SIGSEGV in the 600 MB
  self-compile. The field-free `__field_reclaim_<T>` mechanism (prior slice) is
  already in place and validated, so that analysis is the remaining gate to
  per-module convergence (#3457). Branch `claude/consume-call-move-detection` carried
  only the reverted probe; no code landed from it.
- 2026-06-22: **Ownership-analysis slice — refined implementation plan (the
  dominant pattern is a RETURNED builder).** Scoping the "next slice" against the
  actual self-host source (`lower_expr` / `lower_stmt` / the 174 LowerState-param
  helpers) sharpened the design beyond the previous entry. The threaded builder is
  almost always a fresh-bound LOCAL that is MOVED OUT:

      function lower_X(.., se: LowerState): LowerState {
          var st: LowerState = se.emit(op);      // bound from a fresh-RET method
          st = st.emit(op2);                      // 179x `st = st.method(..)` (safe)
          st = lower_Y(arg, st);                  // 25x `st = g(.., st, ..)` (consume-rebind)
          return st;                              // MOVED OUT
      }

  Consequences for the design:
  1. `is_fresh_struct_init` (gating `reclaimable_names_of` via
     `collect_fresh_struct_names`) accepts only a struct LITERAL today, so a
     call/method-bound builder local (`var st = se.emit(op)`) is never collected —
     the first gap. It must also accept a binding from a FRESH-struct-returning
     call/method.
  2. Soundness of (1) needs a FRESH-struct-return classifier
     `fresh_struct_ret_fns(funcs, structs)`: a fn/method is fresh-returning iff
     EVERY `return` is a struct LITERAL or struct-UPDATE of a leaf-safe struct —
     never a bare param ident (which would alias the caller's box, so reclaiming the
     local double-frees). It is a module-level body scan, threaded into `lower_func`
     as a new param at the ~7 call sites that already thread `struct_ret_fns`
     (`emit_function_via_ir` x3 backends, `lower_func_for`, `lower_func_for_noret`,
     `lower_module`, `ir_eligible`) — NOT a new LowerState field (the mega-struct's
     ~50-field spreads make a new field prohibitive).
  3. Because the builder is `return`ed, `reclaimable_names_of` (which uses
     `body_unsafe_for`, where `return st` is an ESCAPE) excludes it. The fix is a
     SNAPSHOT-LOCAL classification (the local analogue of
     `snapshot_param_names_of`): a fresh-ret-bound, threaded, moved-out local is
     reclaimed AT EACH REBIND via the already-shipped `emit_field_reclaim_store`
     (snap = 0 — a pure local has no caller's original) using
     `body_unsafe_for_allow_ret` (return = move-out, allowed). No entry snapshot is
     needed (unlike params).
  4. The final `return st` must be a STRUCT move-on-return — the struct analogue of
     `move_on_return_idx` for arrays — so the exit reclaim does NOT free the box
     that was just moved to the caller (else double-free).
  5. The `st = lower_Y(arg, st)` consume-rebinds still need the
     `infer_param_escapes` consume-safe fixpoint (previous entry) to be admitted; a
     function with ANY such rebind is excluded until then, so the fresh-ret +
     snapshot-local piece (1-4) lands first and covers the method-only-threaded
     helpers, then the escape fixpoint widens it.

  The field-free MECHANISM (`__field_reclaim_<T>`, #3789) already does the work; this
  slice is purely the ANALYSIS that admits the threaded builders to it. Gate: the
  x86 byte-identical fixpoint + wasm tests locally, arm64 on CI (shared-`asmcore` /
  `irlower` analysis is x86=>arm64 by construction). A single over-admission is a
  SIGSEGV in the 600 MB self-compile, so it is landed smallest-increment-first with
  the fixpoint as the arbiter.

  **Mechanical execution checklist (verified against current `main`):**
  - `snapshot_local_names_of(fn, structs, borrowable, fresh_ret)` is a NEW detector
    PARALLEL to `snapshot_param_names_of` — it does NOT touch `is_fresh_struct_init`
    / `collect_fresh_struct_names` (those serve the non-returned reclaimable-local
    path). It collects each `var st: T = <call/method>` where the callee is in
    `fresh_ret`, `T` is a leaf-safe struct with an rc-array field, `st` is reassigned
    (`body_assign_targets`), and `!body_unsafe_for_allow_ret(fn.body, st, borrowable)`.
  - In `lower_func`, append the snapshot-locals to the `reclaim` list as PLAIN names
    (not the `SNAP:` prefix) so `slot_is_reclaimable_struct` is true → the existing
    `StmtAssign` reclaim path (`emit_field_reclaim_store`, snap = 0) frees each
    rebind's old box + replaced fields. No entry-snapshot store is emitted (a pure
    local has no caller original to protect — unlike `snapshot_param_names_of`).
  - STRUCT move-on-return: extend `returned_moved_arr_slots` (the StmtReturn
    `keeps` collector for `emit_dec_sweep_except_list`) to ALSO collect a returned
    bare-ident slot that is `slot_is_reclaimable_struct`, so the moved-out final box
    is not double-freed by the exit sweep. (The sweep at `emit_dec_sweep_except_list`
    already frees reclaimable struct locals; the keep is the only missing guard.)
  - `fresh_struct_ret_fns_of(funcs, structs)` = keys (`name` / `<Type>.<method>`) of
    fns whose EVERY value `return` is a struct LITERAL or struct-UPDATE of a
    leaf-safe struct (a `__lam_`-style or bare-ident return disqualifies). Thread it
    as a new LAST param of `lower_func`, appended at the 7 call sites:
    `irlower.lower_func_for` (15159), `lower_func_for_noret` (15184), `lower_module`
    (15478); `asm_ir` eligibility probe (2597) + `emit_function_via_ir` (3002);
    `asm_arm64_ir.emit_function_via_ir` (231); `wasm_ir` (671). The three backends
    compute it once in their module-emit (alongside `struct_ret_fns_of`) and pass it
    through `emit_function_via_ir` (one added param each).
  - Risk note: a method binding `var st = se.emit(op)` is only sound if `emit` is
    fresh-ret — a `return self` method would make `st` alias the borrowed receiver
    and the rebind reclaim would free the CALLER's box (UAF). The `fresh_ret`
    classifier is exactly what rules that out; do not admit a call/method binding
    whose callee is absent from `fresh_ret`.
- 2026-06-22: **Snapshot-LOCAL reclaim — SHIPPED (the ownership-analysis slice's
  first half; the field-reclaim mechanism now FIRES on the real workload).**
  Implemented the plan above: `fresh_struct_ret_fns_of` (a fn is fresh-returning iff
  every `return` is a struct literal/update of a leaf-safe struct) threaded into
  `lower_func` as a new param (7 call sites + the 3 backends' `emit_function_via_ir`
  / `emit_module_funcs`; the eligibility probe passes `[]` since reclaim doesn't
  affect `r.ok`); `snapshot_local_names_of` (fresh-ret-bound, threaded, moved-out
  struct locals with rc-array fields) added to the `reclaim` list so each rebind
  reclaims via the shipped `emit_field_reclaim_store`; and struct move-on-return
  (`returned_moved_arr_slots` now also keeps a returned reclaimable struct slot) so
  the moved-out final box is not double-freed. Sound because the binding is a
  fresh-ret call — `st` owns a fresh box, never an alias of a borrowed receiver
  (`return self` is excluded), so a rebind reclaim never frees the caller's box.
  **Activation (the prior entries' inert mechanism now lives):** the forced-IR
  per-module emit produces **1941** `__field_reclaim_<T>` sites in unit 5, 61 in
  irlower, 10 in unit 4 — soundly (the aggressive probe's 1944 minus the
  fresh-ret-gated ones). **Perf gotcha fixed:** the per-var predicate order matters
  — `struct_has_reclaim_array_field` (an allocating field walk) and the O(body)
  borrow check must be gated behind the cheap+selective annotated/reassigned/
  fresh-ret-call tests, else the per-function pass OOMs the native driver's emit of
  the big parser module (#3452 heap limit; isolated as analysis-cost, not
  emit-cost). Validated x86: byte-identical modload fixpoint (mmc == gen2 == gen3),
  stage-2 fixed point, whole-compiler per-module emit + link + RUN (UAF guard at
  scale — the 1941 reclaim sites execute), all RC/struct IR suites, wasm struct +
  deep-nest RC; all 12 per-module units still emit (no eligibility/OOM regression).
  Remaining for full per-module convergence: the `s = lower_X(.., s)` consume-rebind
  cases (gated on `infer_param_escapes`) — a function with any such rebind is still
  excluded; this slice covers the method-threaded majority.
- 2026-06-22: **Consume-safe escape analysis (`infer_param_escapes`) — SHIPPED;
  admits the `s = lower_X(.., s)` consume-rebinds.** Completes the ownership-analysis
  slice: the snapshot-param / snapshot-local reclaim previously excluded any function
  with a free-call consume-rebind (`body_unsafe_for_allow_ret` flags a non-borrowable
  call arg as an escape). `consume_safe_params_interproc` (a least-fixpoint like
  `borrowable_params_interproc`, seeded with `borrowable`) classifies a param
  consume-safe iff its box does not ESCAPE the callee — `body_consume_unsafe_for`
  is `body_unsafe_for_allow_ret` plus the relaxation that a `name = g(.., name, ..)`
  whose callee is consume-safe (or borrowable) at name's position is NOT an escape.
  Sound: a consume-safe callee neither retains nor frees the box (it may read /
  thread / return a value derived from it), so the caller may reclaim the old box
  after the rebind (cow-guarded if the callee returned it unchanged; field_reclaim's
  != new/snap guards keep shared fields). Threaded into `lower_func` as a new param
  (computed once per module alongside `borrowable_params_interproc`; the
  eligibility/eval wrappers pass `[]` — the fixpoint must NOT run per-function, and
  reclaim doesn't affect `r.ok`). `snapshot_param_names_of` + `snapshot_local_names_of`
  now use `body_consume_unsafe_for`. Activation: the parser unit goes 0 → 20
  `__field_reclaim_<T>` sites (it threads builders through `lower_*` consume-rebinds),
  unit 5 +1, all units still emit (no OOM). Validated x86: byte-identical modload
  fixpoint, whole-compiler per-module emit + link + RUN (UAF guard — the consume-
  rebind reclaims execute), all RC/struct IR suites; arm64 rides CI. With this the
  ownership analysis that admits the threaded-builder hot path to the
  `__field_reclaim_<T>` mechanism is complete (method-threaded + consume-rebind +
  fresh-ret-local), closing the gap the 2026-06-22 measurement entries opened.
- 2026-06-22: **Increment "A" (non-reassigned fresh-ret-local reclaim) — ATTEMPTED,
  REVERTED; the convergence blocker has SHIFTED to the #3452 native-emit limit.**
  Targeted the measured eligibility-phase leak (`func_eligible`'s discarded
  `var r = lower_func(..)`): `lower_func` IS fresh-returning (verified — every real
  `return` is a `LowerResult {..}` literal), so admitting fresh-ret CALL bindings to
  `reclaimable_names_of` (extend `collect_fresh` / `is_fresh_struct_init`, threading
  the already-available `fresh_struct_ret_fns`) would soundly free `r` at scope exit.
  But the parser has MANY `var r = parse_x(..)` fresh-ret array-field call locals;
  admitting them all emits an `emit_struct_field_drops` walk per local, and the
  per-module emit of the parser unit then exit-137s (OS OOM-kill) — even with the
  admission gated to annotated array-field structs + cheap-checks-first. This is the
  documented #3452 wall: the native emit's OWN per-function IR-op allocation (#3425,
  not reclaimed) is at its limit, so each increment of reclaim EMISSION (the
  field-drop ops) tips a big module over. Reverted; no code landed from increment A.
  **Conclusion:** the ownership ANALYSIS that admits the threaded-builder hot path to
  `__field_reclaim_<T>` is now complete (method-threaded #3852 + consume-rebind #3860
  + fresh-ret-local), and the gating blocker for per-module convergence is no longer
  missing reclaim — it is the native-emit memory budget (#3452/#3425). The next axis
  is reducing the emit's own footprint: either a SHALLOW field-drop (free only the
  buffer, leak the elements — cheap ~4-op emit) for cheaply-discarded fresh-ret
  locals like `func_eligible`'s `r`, or the #3425 reclaim of the emit's per-function
  IR-op arrays. Until the emit budget grows, further reclaim admission is
  counter-productive (it OOMs the emit before it helps the runtime).
- 2026-06-22: **Sharpened next-axis: make the deep struct-drop a PER-TYPE HELPER
  (mirror `__field_reclaim`).** Root-causing increment A's emit-OOM: the exit-sweep
  reclaim of a struct emits `emit_struct_field_drops` INLINE — for an array-of-struct
  field that is a ~15-20-op element-walk loop. Admitting the parser's many fresh-ret
  array-field locals emits that inline block at each site, exploding the native
  driver's op arrays past the #3452 heap. This is the SAME inline-vs-helper trap the
  field-reclaim slice already solved: `__field_reclaim_<T>` is a per-type helper with
  a ~4-op call site precisely so hundreds of rebinds don't OOM the emit. The deep
  struct-drop should get the same treatment — emit `__struct_drop_<T>` ONCE per type
  (the element-walk body) and replace the inline `emit_struct_field_drops` at the
  exit-sweep / reclaim sites with a 4-op call. That cuts the per-site emit cost ~5x,
  bringing the broader struct reclaim (incl. increment A's discarded fresh-ret
  locals) back within the #3452 budget. It is the principled unlock for per-module
  convergence — a contained, proven pattern (the `__field_reclaim` wiring is the
  template: `is_fern_helper` + `ir_helper_symbol` + per-type body in
  `emit_ir_runtime`-style, x86 real / arm64+wasm via the same emit), distinct from
  the reclaim ANALYSIS (now complete) and from the heavier #3425 emit-time IR-op
  reclaim. Recommended as the next slice once picked up.
- 2026-06-22: **Per-type `__struct_drop_<T>` deep-drop helper — SHIPPED (the
  emit-budget unlock the prior entry recommended).** `emit_struct_field_drops` no
  longer emits the deep-drop inline; it now emits a ~3-op call to a per-type runtime
  helper `__struct_drop_<T>(box) -> box`, exactly mirroring the `__field_reclaim_<T>`
  wiring. x86 emits a real per-type body (`emit_ir_struct_drops` /
  `emit_ir_struct_drop_one` in `asm_ir.fern`): a scalar-array field's buffer is
  shallow-dec'd; a struct/enum-array field's buffer is unique-guarded, its element
  boxes walked + dec'd, then the buffer dec'd — `box` held in `%r11`, the buffer in
  `%r10`, the loop index in `%r9` across the `__fn___fern_arr_dec` /
  `__fn___fern_rc_is_unique` calls (which clobber only rax/rcx/rdx/rsi/rdi/r8, so
  the callee-survivor regs hold). arm64 / wasm emit a leak-safe pass-through (return
  box), matching their `__field_reclaim` pass-throughs and the established
  reclaim-only-on-x86 stance. The need is recorded at the call site
  (`is_struct_drop_name` / `struct_drop_type` + `ir_helper_symbol` in `asm_ir.fern`;
  the x86 + arm64 need-set; the wasm `struct_drop_types` scan in `wasm_ir.fern`).
  **Effect:** the per-site emit drops from a ~30-op inline element-walk to a 3-op
  call, so admitting broad struct reclaim (incl. increment A's discarded fresh-ret
  array-field locals) no longer explodes the native driver's per-function op arrays
  past the #3452 heap — the emit-budget blocker the prior three entries identified is
  now removed for struct reclaim. Same observable reclaim on x86 (the deep-drop still
  runs, just via the helper). Validated x86: byte-identical modload fixpoint (mmc ==
  gen2 == gen3), whole-compiler per-module emit + link + RUN (UAF guard at scale —
  the reclaim sites, now routed through the helper, execute), field-reclaim + struct
  RC IR suites; wasm struct / enum / nested-generic RC IR; arm64 rides CI. **Next:**
  with the emit budget freed, re-attempt increment A (admit `func_eligible`'s
  discarded `var r = lower_func(..)` and the parser's `var r = parse_x(..)` fresh-ret
  array-field locals to `reclaimable_names_of`) — now that each such local's exit
  drop is a 3-op call, the parser unit should stay within the #3452 budget.
- 2026-06-23: **Increment A re-attempted on the freed emit budget — RUNS now, but
  UNSOUND at the per-module RUN level; REVERTED. The blocker was never only #3452.**
  With `__struct_drop_<T>` (#3868) cutting each exit-drop to a ~3-op call, the
  discarded fresh-ret-call local reclaim no longer OOMs the parser unit's emit — so
  this attempt got *past* the budget wall the prior two reverts hit, far enough to
  build the per-module compiler and RUN it. Implementation: a new
  `fresh_ret_call_local_names_of` (the complement of `snapshot_local_names_of`) —
  a `var r: T = f(..)` that is NEITHER reassigned NOR escaping, where `f` is
  fresh-ret (`is_fresh_ret_binding`) and `T` is a leaf-safe struct with an rc-array
  field — appended to lower_func's `reclaim` list so the exit-sweep frees its box +
  deep-drops its fields. **Result:** the byte-identical x86 modload fixpoint PASSED
  (mmc == gen2 == gen3, 193s, no OOM — the emit budget is genuinely freed), but
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` FAILED: the per-module-built
  compiler miscompiled `add(40, 2)` → exit 0 (want 42) and segfaulted self-
  compiling the whole compiler. That is a use-after-free: the discarded-local path
  frees via the plain exit-sweep deep-drop, which ASSUMES the callee's struct-lit
  alias-inc balances any array field that aliases a caller value — and for some
  fresh-ret function that assumption does not hold, so the deep-drop frees a buffer
  still live in the caller. (The fixpoint passes because the native-built compiler's
  own compile happens not to reuse the prematurely-freed block; the per-module RUN
  path does, surfacing the UAF — the same fixpoint-green / RUN-red split #3561
  established as the UAF tripwire.) Reverted in full; no code landed.
  **Conclusion / refined next-axis:** the snapshot-LOCAL path (reassigned + moved-
  out, SHIPPED) is sound precisely because its rebind reclaim is cow + snapshot
  guarded; the DISCARDED-local path cannot reuse the plain deep-drop because a
  fresh-ret callee may return a struct whose array field ALIASES an arg without a
  balancing inc. A sound discarded-local reclaim needs an interprocedural guarantee
  that the fresh-ret callee's returned struct array fields are FRESHLY allocated
  (not arg-aliased) — a `fresh_struct_ret_fns`-style classifier refined to inspect
  the RETURN's field initializers, not just "every return is a struct literal".
  That callee-return-freshness analysis is the real next slice; until it exists,
  discarded fresh-ret-call locals must keep leaking (sound). The `__struct_drop_<T>`
  emit-budget unlock stands on its own (#3868) — it is a prerequisite for this slice,
  not wasted.
- 2026-06-23: **Increment A root-cause SHARPENED (corrects the #3880 framing): the
  blocker is INTRA-procedural callee-return freshness, not an interprocedural
  classifier, and NOT an escape-analysis gap.** Two candidate failure modes for the
  discarded-local deep-drop were on the table: (a) the escape analysis treats
  `r.field` as a borrow (`expr_unsafe_for`'s ExprFieldAccess ident-base arm returns
  false), so `var x = r.ops` could extract an rc-array field into a lasting alias;
  (b) the callee returns a struct whose array field aliases one of its args. A timing
  argument settles it: the discarded-local reclaim fires ONLY in the end-of-scope
  dec-sweep (`emit_dec_sweep_except_list`), which runs AFTER every in-function use of
  `r` and its extracted fields — so an extracted alias `x = r.ops` is always still
  valid at its last use within the frame (mode (a) cannot dangle for the exit-sweep;
  the existing struct-literal reclaim relies on this same property and is sound). The
  only way the exit-sweep frees a still-live buffer is if `r`'s array field aliases a
  value that outlives THIS frame — i.e. the fresh-ret CALLEE returned a struct whose
  rc-array field points at one of the callee's own args (which the caller still owns).
  That is mode (b), and it is decided entirely by the CALLEE's body, intra-
  procedurally: is each rc-array field of every `return S { .. }` initialised from a
  FRESH value (an array literal, or a non-param local provably built fresh — append
  chains off a literal, a field of a fresh struct local) rather than from a param /
  param-field / call-result? So the real next slice is a `return_fresh_struct_ret_fns`
  refinement of `fresh_struct_ret_fns_of`: keep a fn only if every return-struct
  rc-array field init is fresh by an intra-procedural provenance check of the callee.
  No change to `expr_unsafe_for` / the escape analysis is needed (mode (a) is a
  non-issue for the exit-sweep). **Tractability caveat (measured):** a cheap sound
  over-approximation has near-zero value — gating on "callee has no rc-array-typed
  params" (so nothing can alias in) is sound but excludes exactly the valuable cases
  (`lower_func` takes `structs[]` / `fn` yet returns a fresh `LowerResult.ops`), so
  the freshness must be proven for callees that DO take rc-array inputs but build
  their return fields fresh. That provenance check (literal / fresh-built-local /
  fresh-struct-field, transitively) is the substantive dataflow work; until it lands,
  discarded fresh-ret-call locals keep leaking (sound). `__struct_drop_<T>` (#3868)
  remains the prerequisite that makes the eventual admission fit the #3452 budget.
- 2026-06-23: **Correction to the entry above — the freshness check IS an
  interprocedural fixpoint, reconciling #3880 and #3888.** Inspecting `lower_func`'s
  actual success return (`return LowerResult { ok: true, ops: s.ops, n_locals: ..,
  arr_slots: arr_slots_of(s), i64_slots: i64_slots_of(s), f64_slots: f64_slots_of(s),
  .. }`) shows the rc-array fields are initialised from (i) `s.ops`, a field of the
  fresh threaded `LowerState` local `s`, and (ii) `arr_slots_of(s)` / `i64_slots_of(s)`
  / `f64_slots_of(s)`, HELPER CALLS that build a fresh `i32[]`. A purely intra-
  procedural provenance check sees those calls and conservatively says "not fresh" →
  it REJECTS `lower_func`, the one case worth admitting. To admit it, the check must
  also classify `arr_slots_of` & co. as FRESH-ARRAY-RETURNING — which is the same
  freshness question one level down. So the real shape is an interprocedural least-
  fixpoint over TWO predicates — `fresh_array_ret_fns` (a fn whose every `return e`
  is a fresh array: literal / append-chain off a literal / a call to a
  fresh_array_ret_fn) and `return_fresh_struct_ret_fns` (a fresh-ret struct fn whose
  every return-struct rc-array field init is fresh: array literal / fresh non-param
  local / field of a fresh struct local / call to a fresh_array_ret_fn) — seeded
  empty and iterated to a fixpoint, exactly like `borrowable_params_interproc` /
  `consume_safe_params_interproc`. The timing argument from the prior entry still
  holds (the exit-sweep makes mode (a)/escape a non-issue; the decision is about the
  callee's returned-field provenance) — but proving that provenance for the valuable
  callees is interprocedural, not intra-procedural. Net: #3880's "interprocedural
  classifier" was right about the shape, #3888 was right that it's the callee's
  return-field provenance (not an escape-analysis change) and that mode (a) is inert;
  this is the reconciled, accurate spec for the slice. Effort + risk unchanged
  (substantial dataflow; a false-positive freshness = UAF, caught by the per-module
  RUN gate), value unchanged (admits the `lower_func`/`parse_x` discarded-local hot
  path once it lands), prerequisite unchanged (`__struct_drop_<T>`, #3868).
- 2026-06-29: **Backend parity — `__fern_snapshot_dec` is now a REAL reclaim on
  arm64** (was a leak-safe pass-through). The snapshot-param consume-rebind free
  (`f(s: S): R { s = s.step(x); … }`, #3456 slice 2) frees the old box —
  rc-guarded, cow-guarded (≠ `new`), snapshot-guarded (≠ the caller's entry box)
  — only on x86 until now; arm64 just returned `new` and leaked the intermediates.
  Ported the x86 body (`asm_ir.emit_ir_runtime`) to `asm_arm64.fern`'s
  `__fn___fern_snapshot_dec`, matching the arm64 `__fern_arr_dec` free path exactly
  (rc @ `[old-8]`, cap @ `[old-16]`, base = `old-16`, idx = `cap+3`, `__fern_freelist`
  word-indexed, class cap 65536; stack-arg ABI `[sp]=snap, [sp+16]=old, [sp+32]=new`).
  Verified: `TestSelfHostSnapshotParamReclaimArm64` (caller-original-intact = 40 +
  a 50× scalar-struct churn = 7, both under qemu, asserting the real freelist-push
  body `str x5, [x6, x4, lsl #3]` is emitted, not the stub); `TestSelfHostFixpointArm64`
  byte-identical (the compiler's own consume-rebind builders now reclaim on arm64
  with no self-reproduction drift); x86 snapshot/reclaim suites unchanged. Low blast
  radius — only converts a sound leak into a real free on one backend; the shared
  analysis (`snapshot_param_names_of`) is already x86-validated. Remaining arm64
  reclaim-parity slices: `op_arr_push_owned` reclaim-on-grow and the per-type
  `__struct_drop_<T>` / `__field_reclaim_<T>` deep-drop bodies (still pass-throughs
  on arm64 — a RETURNED heap-field builder there still link-errors on
  `__field_reclaim_<T>`, the natural next parity target).
- 2026-06-29: **Backend parity — `op_arr_push_owned` reclaim-on-grow is now REAL on
  arm64** (was aliased to plain `__fern_arr_push`, leaking the old buffer). The
  sole-owner self-append (irlower's `!aliased` `a = a.append(v)`) frees the dead old
  buffer when the push GREW (reallocated) — only on x86 until now. Added an arm64
  `__fern_arr_push_owned` wrapper (`asm_arm64.fern`, co-emitted with its
  `__fern_arr_push` dependency): saves the old ptr, calls `__fern_arr_push`, and iff
  the result differs frees the old box via the `__fern_arr_dec` freelist path
  (rc-guarded sole-owner, ≤512 KiB tier — same heap layout used by `snapshot_dec`);
  the `asm_arm64_ir.fern` `arr_push_owned` dispatch now `bl`s it instead of plain
  push. Multi-unit `.globl` is already covered (the arm64 IR path reuses
  `asm_ir.emit_runtime_globls`, which lists `__fern_arr_push_owned`). Verified:
  `TestSelfHostArrPushOwnedReclaimArm64` (new — a 100× self-append grows several
  times and the read-back sum = 7 is intact under qemu, asserting both `bl
  __fern_arr_push_owned` is dispatched and the real freelist-push body is emitted);
  `TestSelfHostFixpointArm64` byte-identical (the compiler's own self-appends now
  reclaim on arm64 with no drift); `TestSelfHostRcArm64` self-append suite + the x86
  self-mutate/free-reclaim/move-on-return suites unchanged. Remaining arm64 parity:
  the per-type `__struct_drop_<T>` / `__field_reclaim_<T>` deep-drop bodies (still
  pass-throughs; `__field_reclaim` is now UNBLOCKED — its prerequisite, `snapshot_dec`
  freeing on arm64, landed above).
- 2026-06-29: **Backend parity — `op_arr_push_owned` reclaim-on-grow is now REAL on
  wasm** (was aliased to plain `$__fern_arr_push`, leaking the old buffer). The
  sole-owner self-append (irlower's `!aliased` `a = a.append(v)`) frees the dead old
  buffer when the push GREW (reallocated) — previously only x86 + arm64 reclaimed;
  wasm just returned the new array and leaked the intermediates. Added a wasm
  `$__fern_arr_push_owned` wrapper (`wasm.fern`'s `arr_push_owned_helper`, gated on
  `module_emits_op(mod, "arr_push_owned")`): calls `$__fern_arr_push`, and iff the
  result differs from the input (the buffer reallocated) frees the old block via
  `$__fern_arr_dec` — the existing rc-guarded freelist-push helper (real reusable
  freelist on wasm, not bump-only), so the owned wrapper is a thin 4-liner. The
  `wasm_ir.fern` `arr_push_owned` dispatch now `call`s `$__fern_arr_push_owned`
  instead of plain push. **Both** wasm runtime-emission paths needed the body: the
  `emit_module_mode` driver (AST auto-decide, used by `wasm_run.fern`) AND the
  standalone `wasm_ir_run.fern` `-ir` driver, which has its own separate
  emit-runtime block — only wiring one left the other emitting a `call` to an
  undefined function (invalid module → wasmtime trap). Verified:
  `TestSelfHostArrPushOwnedReclaimWasm` (new — a 100× self-append grows several
  times and the read-back sum = 7 is intact under wasmtime, asserting both `call
  $__fern_arr_push_owned` is dispatched and the `(func $__fern_arr_push_owned` body
  is emitted); `TestSelfHostArrPushIRWasm` (the `-ir` standalone path, green after
  the second-emission-path fix); `TestSelfHostRcFreeWasm` / `TestSelfHostRcCallResultWasm`
  freelist-reuse suites unchanged. Remaining wasm parity: the per-type
  `__fern_snapshot_dec` real body (still a pass-through at `wasm.fern`) and the
  per-type `__struct_drop_<T>` / `__field_reclaim_<T>` deep-drop bodies.
- 2026-06-29: **Perceus slice 1c — wasm `__struct_drop_<T>` is now a REAL
  deep-drop** (was a leak-safe pass-through). The wasm IR path emits a real
  per-type `$__struct_drop_<T>(box) -> box` body
  (`emit_wasm_struct_drop_body` in `wasm.fern`, wired into `emit_module_mode`'s
  IR dispatch): for each rc-ARRAY field it releases the buffer via
  `$__fern_arr_dec` (scalar-element array — flat free) or `$__fern_arr_dec_ptr`
  (pointer-element array — also releases its element boxes), both rc-guarded +
  null-guarded internally. The critical detail was the field offset: the body
  runs on the IR path, whose struct layout is `8 + i*8` (8-byte header then
  8-byte slots, matching `wasm_ir`'s struct_make / struct_get / struct_set) —
  NOT the AST path's `struct_field_off` (`4 + i*4`). The first cut read offset
  4 and silently reclaimed nothing; reading the low 4 bytes of the IR slot at
  `8 + i*8` recovers the buffer pointer. Closes the struct-array-field leak on
  the wasm IR path, at parity with x86-64 and arm64 (1a/1b). The RC-introspection
  builtins (`__fern_rc_underflow_count` / `__fern_arr_dec` / `__fern_rc_is_unique`)
  all force a module onto the wasm AST path, so they can't appear in an
  IR-routed reclaim test; reclaim is proven instead by a memory-pressure
  differential — a 400–500k alloc→drop churn under a tight `max-memory-size`
  cap with `trap-on-grow-failure` stays bounded (reuses freed blocks) with the
  real drop and traps with the pass-through. Verified:
  `TestSelfHostStructDropWasm` (scalar-array + struct-array fields, each pinning
  the emitted real-body WAT shape and running under a 16 MiB cap); the existing
  wasm RC/struct suites (`TestSelfHostRcStructBoxWasm`, `TestSelfHostRcFreeWasm`,
  `TestSelfHostRcRuntimeWasm`, the arr/map-struct RC tests — all AST-routed)
  unchanged. Remaining slice-1 parity target: 1d — wasm real
  `__field_reclaim_<T>` body (still a pass-through).
- 2026-06-29: **Perceus slice 1d — wasm `__field_reclaim_<T>` AND
  `__fern_snapshot_dec` are now REAL** (were leak-safe pass-throughs returning
  `new`). This closes the LAST slice-1 reclaim gap: the consume-rebind path
  (`f(a: T): R { a = T { … }; … }`, #3456 slice 2) — where a struct PARAM is
  reassigned, superseding its previous value — now reclaims on wasm at parity
  with x86-64 and arm64. `__fern_snapshot_dec(new, old, snap)` (in
  `rc_runtime_helpers`) frees `old` ONLY when uniquely owned (rc==1) and
  differing from both `new` (cow) and `snap` (the caller's borrowed original);
  a shared box is LEFT, never decremented (the param is borrowed — we hold no
  count). The free reuses `$__fern_arr_dec`'s rc==1 freelist push (guarding
  rc==1 first means arr_dec only ever frees, never decrements). The per-type
  `$__field_reclaim_<T>(new, old, snap)` body (`emit_wasm_field_reclaim_body` in
  `wasm.fern`, wired into `emit_module_mode`) frees each rc-ARRAY field's
  superseded buffer via `$__fern_arr_dec` (SHALLOW, matching the register
  backends — a pointer-element field's element boxes are a known one-level gap
  on every backend) iff old.field_k differs from new.field_k (cow) and, when
  snap != 0, snap.field_k (nested ifs, not a flat `i32.or`, so snap.field_k is
  never loaded when snap == 0), then frees the old box via `$__fern_snapshot_dec`.
  Field offset is the IR struct layout (`8 + i*8`, as in slice 1c). Reclaim is
  proven by a memory-pressure differential (the RC-introspection builtins force
  the AST path, so they can't appear in an IR-routed test): a single `build()`
  call whose snapshot param is loop-rebound 2M times stays bounded (~1 MiB) under
  a 16 MiB cap with the real bodies and traps (>128 MiB leak) with the
  pass-through. Verified: `TestSelfHostFieldReclaimWasm` (the 2M-rebind reclaim
  differential + a snapshot-guard case asserting the caller's original survives
  the param rebinds); existing wasm RC suites (`TestSelfHostStructDropWasm`,
  `TestSelfHostRcStructBoxWasm`, `TestSelfHostRcFreeWasm`,
  `TestSelfHostArrPushOwnedReclaimWasm`, …) unchanged. The standalone `-ir`
  driver (`wasm_ir_run.fern`) still omits the per-type field_reclaim/struct_drop
  bodies — a pre-existing gap shared with slice 1c, out of scope here.
  **Slice 1 (reclaim parity) is COMPLETE across all three IR backends.**
- 2026-06-29: **Perceus slice 2 — borrow inference is now a GREATEST-fixpoint
  (native `inferParamEscapes` parity).** `borrowable_params_interproc`
  (`irlower.fern`) flipped from a least-fixpoint FROM BELOW (start with an empty
  registry, grow) to a greatest-fixpoint FROM ABOVE (start with EVERY param
  optimistically borrowable, flip one off only when an actual escape — return of
  a derived value / alias / container store / slice / lambda capture — is proven
  under the current registry). The from-below pass could never bootstrap a
  MUTUALLY RECURSIVE borrow (a param only became borrowable once its callee
  already was, and in a cycle neither could go first), so cyclic read-only params
  — e.g. the `expr_unsafe_for`/`stmt_unsafe_for`/`body_unsafe_for` walkers' own
  registry param — stayed conservatively non-borrowable. From-above keeps them
  borrowable unless a real escape is found, matching native and feeding the
  downstream reclaim (`reclaimable_names_of` + snapshot/precise-drop gating).
  SOUNDNESS: from-below is fail-safe on its iteration cap (un-converged ⇒ smaller
  ⇒ conservative); from-above is the opposite (un-converged ⇒ larger ⇒ would mark
  an escaping param borrowable), so convergence is detected EXACTLY via the
  registry signature (`borrowable_sig`) and the cap (64) is only a backstop — real
  call graphs converge in a handful of passes. Verified: the change is
  behaviour-neutral on the existing corpus — `TestSelfHostBootstrapsItself`
  (native-Go ↔ self-host byte-identical) and `TestSelfHostStage2FixedPoint`
  (self-reproduction) both still pass, so no self-host function's emitted reclaim
  changed — yet capability-additive for cyclic borrows:
  `TestSelfHostBorrowInferInterprocX86_64` exercises a mutually recursive
  borrow-only `walk_a`/`walk_b` cycle threading a non-escaping struct LOCAL `nd`;
  the greatest-fixpoint recognises the cycle's params as borrowable, so `nd` is
  reclaimed at the caller's exit (the emitted asm calls `__fn___struct_drop_Node`)
  — a 200M-iteration churn stays bounded (exit 0) where the least-fixpoint leaked
  one box+buffer per call and exhausted the heap (SIGKILL 137), plus a
  value/over-release case proving the reclaim is sound (not a double-free).
  `TestSelfHostStdTestE2E` (differential vs interp) unchanged. **Next: slice 3 —
  struct/enum drop for NON-array rc fields (needs struct-field construction-inc).**
- 2026-06-30: **Perceus slice 3 (a/b/c) — DIRECT nested-struct field reclaim,
  all three IR backends.** A struct with a direct nested-struct field
  (`Outer { inner: Inner }`) now reclaims that field on the IR path instead of
  leaking it — the first NON-array rc-field drop. The inner box is freed SHALLOW
  via `__fern_arr_dec` at the IR field offset (`8 + i*8`), one level like a
  struct-array element box (`arr_dec` frees struct boxes too); `k_struct` in
  `emit_ir_struct_drop_one` / `emit_arm64_struct_drop_one` /
  `emit_wasm_struct_drop_body`. The struct is admitted to the reclaim set via
  `struct_has_reclaim_array_field` (now also true for a `decl_is_struct` field),
  so its exit `__struct_drop_<T>` is emitted. SOUNDNESS — the construction
  alias-inc (`irlower.fern`, `ExprStructLit` override + base-copy paths) is
  DOUBLE-FREE-PROOF BY CONSTRUCTION: a fresh struct LITERAL is sole-owned (rc 1,
  never aliased) so it needs no inc and is reclaimed; EVERY other source (bare
  ident, field copy, base copy, call) is retained, so the shallow drop only decs
  a counted reference — worst case a sound leak, never an over-release. The one
  surprise was a TEST-HARNESS gap, not a correctness break: making a struct
  reclaimable in the IR round-trip eval (`eval_ops`) emitted a `__struct_drop_<T>`
  call that the i32 interpreter couldn't resolve (it is a backend runtime helper,
  not a user fn) → it bailed to sentinel 198 instead of the value; fixed by
  modeling the Perceus reclaim helpers (`is_reclaim_helper`: `__struct_drop_<T>` /
  `__field_reclaim_<T>` / `__fern_snapshot_dec` / `__fern_arr_dec` /
  `__fern_drop_arr_ptr`) as value-preserving identity on their box arg. CI proved
  the broad reclaim is byte-identical-safe (the x86 + aarch64 fixpoint / bootstrap
  / differential shards all green). Tests: `TestSelfHostStructDropWasm` gained a
  `nested-struct-field-reclaim` memory-cap case (fresh-literal `Outer{Inner,…}`
  churned 500k× stays bounded vs the slice-1c pass-through leak); the x86
  construction forms (move / field-copy / base-copy + a 3-level nest with a
  fresh-returning call) were hand-verified sound over 2–3M iters; arm64 rides the
  self-host fixpoint + e2e differential. Follow-ups (see slice 3 above): tighten
  the no-inc set to fresh-RETURNING calls; deep-drop the inner's own rc fields;
  string / enum-payload fields.
- 2026-06-30: **Perceus slice 3 deep-drop — RECURSIVE nested-struct field reclaim,
  all three IR backends.** A direct nested-struct field (`Outer { inner: Inner }`)
  whose inner is a deep-drop-ok LEAF (carries an rc-array field, no nested-struct
  field of its own) is now RECURSIVELY reclaimed: when the inner box is uniquely
  owned, `__struct_drop_<Inner>` releases the inner's rc-array buffers before the
  inner box is freed, instead of the shallow box-only free that leaked them. x86
  (`emit_ir_struct_drop_one`, #4075) is_unique-gates `call __fn___struct_drop_<Inner>`
  then `__fern_arr_dec`, reloading the box from `8(%rsp)` after (struct_drop clobbers
  %r11); arm64 (`emit_arm64_struct_drop_one`, #4083) mirrors it, reloading the box
  from the frame-relative `[sp, #16]` after the call (the entry pushes an stp x29,x30
  frame and the call clobbers x10); wasm (`emit_wasm_struct_drop_body`, #4083)
  is_unique-gates a recursive `$__struct_drop_<Inner>` then `$__fern_arr_dec`. The
  transitive inner type is emitted via the register backends' `need("struct_drop:")`
  loop (re-reads needed.len()) and, on wasm, via a deep-drop CLOSURE added to
  `struct_drop_types` (the recursive call is a backend instruction, invisible to the
  call_direct scan). CYCLE SAFETY: deep-drop fires only for a LEAF inner, so the
  recursion is depth-1; a self-referential / tree struct carries a nested-struct
  field and stays shallow. Tests: `TestSelfHostStructDeepDropIRX86_64` (churn /
  value / cyclic-safe), `TestSelfHostStructDeepDropIRArm64` (shape + value +
  cyclic-safe under qemu), `TestSelfHostStructDropWasm/nested-struct-field-deep-drop`
  (recursive-call + transitive-closure asserted, reclaim by the memory-cap
  differential). CI fixpoint / bootstrap / differential all green.
- 2026-06-30: **Perceus slice 3 deep-drop FOLLOW-UP — tighten the struct-construction
  no-inc set to fresh-RETURNING calls.** A nested-struct field whose value is a CALL
  to a strict-fresh-struct-returning fn (`Outer { inner: mk_inner() }`) is no longer
  alias-inc'd: the callee handed back a fresh SOLE-owned box
  (`return_fresh_struct_ret_fns`, already computed for the discarded-local reclaim),
  so the new struct owns it outright and the field-drop reclaims the inner box,
  instead of leaking the rc-2 retained box. Wired by threading
  `return_fresh_struct_ret_fns` onto `LowerState` (read-only, like `reclaimable_names`)
  and gating the `ExprStructLit` nested-struct construction-inc on
  `is_fresh_ret_binding(field_value, cfft, …)`. SCOPE: a struct with an rc-array field
  is not leaf-safe and a fn returning it bails to AST, so `return_fresh_struct_ret_fns`
  holds only LEAF-SAFE-struct factories — the reclaim win is the inner BOX (a leaf-safe
  inner has no rc fields to deep-drop), and soundness is trivial (no buffers to double-
  free; the strict classifier guarantees the box is the sole owner). PARITY-DIRECTED:
  the native-Go compiler (Perceus source of truth) emits ZERO inc for this pattern and
  reclaims the box (churn bounded, exit 0, verified directly) — so the self-host change
  moves it TOWARD native-Go, making the bootstrap/fixpoint strictly safer, never more
  divergent. The looser `fresh_struct_ret_fns` is NOT used (its inner element buffers
  may alias, which a deep-drop would double-free). Test:
  `TestSelfHostStructFreshRetFieldIRX86_64` (150M churn bounded vs the pre-slice
  alias-inc leak → SIGKILL; value-correctness 10). Follow-ups remain: string /
  enum-payload fields; extending fresh-ret freshness to non-leaf-safe struct returns
  once those become IR-lowerable.
- 2026-06-30: **Perceus enum-payload slice — deep-drop a variant's nested-STRUCT
  payload, all three IR backends.** A consume-by-match enum local
  (`var b = Full(Inner { items: [..] }); match (b) { Full(_/inner) => .., Empty => .. }`)
  whose variant carries a deep-drop-ok nested-struct payload is now reclaimed
  recursively at the match: `enum_field_rc_droppable` (now `(structs, t)`) admits a
  `decl_is_struct && nested_field_deep_drop_ok` payload, and emit_enum_variant_drops
  emits, under the variant_is guard, `__struct_drop_<Inner>` (releases the payload's
  rc-array fields) then `__fern_rc_dec` (frees the payload box) — instead of the whole
  enum bailing the reclaim (the payload type was undroppable, so box + payload leaked).
  The backend auto-emits the helper on seeing the IR call_direct (asm_ir op scan @4462
  / wasm struct_drop_types + the slice-3 transitive closure / arm64 mirror). SOUNDNESS
  — no is_unique guard is needed because consumed_rcpayload_enum_frees only admits the
  enum when its struct payload was a FRESH struct LITERAL (new
  variant_struct_payloads_fresh gate): the payload box is then the sole owner (rc==1),
  and a bare-ident / call payload aliasing a swept local is rejected (deep-dropping it
  would double-free its inner buffers — the same fresh-literal rule the struct-field
  deep-drop uses). The depth-1 leaf rule (nested_field_deep_drop_ok) keeps it
  cycle-safe. Covers the discard (`Full(_)`) AND the bound-borrow-only (`Full(inner) =>
  inner.items[i]`) cases — match_arm_binds_rc_payload admits a borrow-only binding.
  NOTE: only UNQUALIFIED variant ctors (`Full(..)`) are recognised — a qualified
  `Box.Full(..)` callee is an ExprFieldAccess that fresh_rcpayload_enum_init doesn't
  match (a pre-existing limitation, shared by the scalar path). VERIFIED END-TO-END
  LOCALLY (swap enabled for the self-host driver builds): x86 churn bounded + value 17,
  wasm memory-cap differential, byte-identical FIXPOINT and BOOTSTRAP (native-Go ↔
  self-host). Tests: `TestSelfHostEnumStructPayloadDropIRX86_64` /
  `…DropWasm` / `…DropIRArm64`.
- 2026-06-30: **Perceus enum reclaim — recognise QUALIFIED variant constructors**
  (lifts the limitation noted just above). A consume-by-match enum local built with a
  qualified ctor (`Bag.Items(..)`, not bare `Items(..)`) is now reclaimed too.
  fresh_rcpayload_enum_init / fresh_scalar_enum_init only matched a bare-ident callee,
  so a qualified construction's callee — an ExprFieldAccess (obj=Enum, field=Variant) —
  fell through and the enum box + payload leaked while the identical unqualified form
  reclaimed. Both classifiers now resolve the variant by its field name
  (variant_enum_owner on fa.field), covering the payload-carrying ctor
  (ExprCall→ExprFieldAccess callee) and the bare qualified unit variant. SOUNDNESS —
  the qualified construction path SHARES the unqualified lowering, so a bare-ident
  array payload is array-alias-inc'd at construction exactly as the unqualified form
  is, balancing the match-site dec (no double-free); a fresh-literal struct payload
  rides the same fresh-literal gate. Frontend-only, uniform across all backends. The
  self-host source uses only unqualified ctors, so fixpoint/bootstrap are unchanged
  (they confirm the unqualified path didn't regress); the win is for user programs in
  qualified style — native-Go already reclaims them (parity). VERIFIED LOCALLY: x86
  churn bounded for a fresh-literal payload AND a bare-ident payload (the latter also
  value-correct = no double-free), byte-identical fixpoint + bootstrap. Test:
  `TestSelfHostEnumQualifiedCtorReclaimIRX86_64`.
- 2026-06-30: **Precise drop-on-last-use for fresh SCALAR-ONLY struct locals**
  (native parity: `TestPreciseDropControlFlowStruct`). The self-host precise-drop
  pass (`precise_drop_names`) previously admitted only scalar-element ARRAY locals
  (`var a = [..]` / a scalar-array-returning free call); a fresh scalar-only struct
  local (`var p = P { x: 1, y: 2 }`, P all flat scalar fields) fell to the
  function-exit dec-sweep even when its last use was an earlier statement. It now
  qualifies: a `var p = T { ...scalar literals... }` of a `struct_is_scalar_only`
  type, no struct-update base, never reassigned, only borrow-read after, last-used
  at a later top-level statement, is freed (a single shallow `__fern_rc_dec` — the
  box owns no pointer to walk) and its slot zeroed right after that use, so the exit
  sweep decs a guarded null. SOUNDNESS — a scalar-only struct carries no rc field,
  so the early box free cannot dangle a reference; the same `body_unsafe_for` escape
  gate the array path uses proves non-escape (a returned / stored / non-borrowable-
  passed `p` is excluded). A donor guard at the emission site skips the precise drop
  when the name is a reuse / cross-reuse / enum-donor / in-arm donor target (its box
  is recycled by that path — an early free would race it; the `cross-reuse-donor-
  guard` test pins this). FIRING confirmed in the emitted asm (the mid-function
  `__fern_rc_dec` + slot-zeroing store after the `if`, the exit sweep then no-ops on
  the null slot — the slot-zero is the signature, the exit sweep never zeroes).
  Placement-granularity matches the existing array precise drop (after the top-level
  statement of last use, not inside the branch); native places it inside the branch,
  but for a struct the observable contract (a `__drop`/box-free is emitted at the
  precise point vs only at exit) is the same. VERIFIED: new
  `scalar-struct-precise-*` cases (route ir, value + `__rc_underflow()==0`, a heap-
  reuse corruption probe, the donor guard) in `TestSelfHostRcPreciseDropX86IR`;
  byte-identical FIXPOINT + BOOTSTRAP (assemble+link) unchanged; the broad self-host
  RC/drop sweep stays green.

  Investigation note (why this slice, not the discarded-fresh-array-ret reclaim):
  the earlier fresh-array-ret fixpoint was reverted as INEFFECTIVE, and a churn /
  RSS-under-`ulimit` harness was used to chase it. That harness is NOT a sound
  Perceus signal: the native reference compiler ALSO leaks a discarded
  `var b = S { items: gen() }` (and a plain discarded fresh-array local) under a
  vmem cap — its emitted binary contains no `__fern_arr_dec` for that pattern at all
  — so it was never a parity gap, and exit-code/RSS conflates allocator policy with
  drop emission. The sound signal is emitted-reclaim inspection + the fixpoint /
  bootstrap / `__rc_underflow` correctness gates (used here). Separately: the
  `TestSelfHostBootstrapsItself` gate is an ASSEMBLE+LINK check (symbol-closure), not
  a native↔self-host byte-identical comparison — the byte-identity gate is the
  stage-2 FIXPOINT (self-reproduction / determinism). A Perceus emission change must
  therefore preserve symbol-closure, determinism, and no-double-free — not match
  native byte-for-byte.
- 2026-07-01: **Reclaim fresh scalar-only TUPLE locals** (they leaked wholesale
  before). Tuples were "leak-only by design": `tuple_make` boxed via raw
  `__fern_alloc` on the register backends (x86 asm_ir / arm64 asm_arm64_ir), a
  non-rc-headered block that `__fern_rc_dec` cannot free, so a fresh
  `var t = (3, 4)` was never reclaimed (0 decs). Native DOES reclaim tuples
  (genTupleDrops / genArrTupleDropFn), so this was a real parity gap + per-call
  leak. Fix, two parts: (1) `tuple_make` now boxes via `__fern_arr_box` (cap=n) on
  x86 + arm64 — rc-headered exactly like `struct_make`, element offsets unchanged
  (element i @ i*8; arr_box's len@0 word is overwritten by element 0, cap@data-16
  drives the free size); wasm already boxed tuples via `$__fern_str_box`
  (rc-headered), so it needed no backend change. (2) A fresh, non-escaping tuple
  literal of all-scalar-literal elements (`tuple_lit_is_fresh_scalar` +
  `collect_fresh_scalar_tuple_names`) is credited to `reclaimable_names` under a
  "TUP:" prefix (so the struct/snapshot consumers, which look up a bare name or
  "SNAP:<name>", never match it — only `slot_is_reclaimable_tuple` does), zeroed at
  entry (arr_slots_of), and shallow-freed (`__fern_rc_dec`) by a new tuple loop in
  the exit dec-sweep — the tuple sibling of the scalar-struct reclaim. SOUNDNESS —
  the box holds no rc element to walk (scalar-literal gate), and the shared
  body_unsafe_for escape gate excludes a returned / stored / non-borrowable-passed
  tuple (`return t`, `(t, …)`, container store) so an escaping tuple is never freed
  here (moved to its owner, leak-as-before). FIRING confirmed in the emitted asm
  (tuple builds via `__fern_arr_box`, one `__fern_arr_dec` at scope exit, no raw
  `__fern_alloc`). VERIFIED: new `scalar-tuple-*` cases (route ir, value,
  `__rc_underflow()==0`, heap-reuse corruption probe, the escape-not-freed guard)
  in `TestSelfHostRcPreciseDropX86IR`; byte-identical FIXPOINT + BOOTSTRAP; the full
  self-host tuple suite (x86 + wasm, incl. `…WasmTupleRc`) and the broad RC/drop
  sweep stay green. arm64 mirrors struct_make's proven pattern; CI backstops it.
  Follow-up: a precise drop-on-last-use for early-dead scalar tuples (this slice is
  exit-sweep only), and widening past scalar-literal elements (a fresh tuple with a
  reclaimable-array element would deep-drop that element).
- 2026-07-01: **Precise drop-on-last-use for early-dead scalar tuples** (the tuple
  sibling of the scalar-struct precise drop; follows the #4158 exit-sweep reclaim).
  A fresh scalar tuple local (`var t = (3, 4)`, all scalar-literal elements) whose
  last top-level use is an earlier statement is now freed (shallow `__fern_rc_dec`)
  and its slot zeroed right after that use, instead of at the function-exit sweep —
  bounding the live set. Added a tuple candidate to `precise_drop_names`
  (tuple_lit_is_fresh_scalar) and a `slot_is_reclaimable_tuple` else-branch at the
  lower_func precise-drop emission site (load + `__fern_rc_dec` + drop + const-0 +
  store). No donor guard needed (tuples are never reuse/cross-reuse/enum-donor/
  in-arm targets). SOUNDNESS is the same as the exit-sweep reclaim — the box holds
  no rc element to walk (scalar-literal gate) and the shared body_unsafe_for escape
  gate proves non-escape; the exit sweep then decs the zeroed slot's guarded null.
  FIRING confirmed in the emitted asm (mid-function `__fern_arr_dec` + slot-zero
  right after the `if`, the exit sweep no-ops on the null). VERIFIED: new
  `scalar-tuple-precise-*` cases (route ir, value, `__rc_underflow()==0`, heap-reuse
  corruption probe) in `TestSelfHostRcPreciseDropX86IR`; byte-identical FIXPOINT +
  BOOTSTRAP stay green. Frontend-only (precise_drop_names + the emission site are
  backend-agnostic — every backend that lowers tuples benefits).
- 2026-07-01: **Reclaim fresh scalar Option locals** (they leaked on the register
  backends). A scalar `var o: Option[i32] = Some(5)` / `var o = None` routed ir but
  leaked — opt_make/opt_none used raw __fern_alloc(16) on x86 (asm_ir) / arm64
  (asm_arm64_ir), a non-rc-headered block __fern_rc_dec can't free (0 decs). Native
  reclaims Options. Fix: (1) opt_make + opt_none now rc-box via __fern_arr_box(cap=2)
  on x86 + arm64 — exactly like struct_make/tuple_make; offsets unchanged (tag@0
  overwrites arr_box's len@0, payload@8, cap@data-16 = free size). wasm already boxed
  Options via $__fern_str_box (no change). (2) consumed_scalar_enum_frees — the
  existing consume-by-match box-free classifier — now ALSO admits a fresh scalar
  Option local (fresh_scalar_option_init: `Some(scalar-lit)`/`None`, or an annotated
  `Option[<scalar>]`) consumed by exactly one `match (o)`, single-owner and dead-
  after: its box is shallow-freed (__fern_rc_dec) right after the match, reusing the
  enum-free emission verbatim. SOUNDNESS — the scalar-payload gate means the box owns
  no rc pointer, so a shallow dec is the whole free; the match reads tag/payload as
  borrows before the free; the same single-match / dead-after / non-escape gates the
  scalar-enum path uses exclude a used-after / returned / aliased Option (a returned
  Option is moved out, never freed here). rc-PAYLOAD Options (Some([..]) / Some("x") /
  Some(struct)) are NOT admitted (payload would leak — a deep-drop follow-up), and
  neither is an un-annotated non-literal payload (unprovable scalar-ness in the
  classifier). FIRING confirmed in the emitted asm (opt builds via __fern_arr_box, one
  __fern_arr_dec after the match, no raw __fern_alloc). VERIFIED: new `option-*` cases
  (route ir, value, __rc_underflow()==0, None, un-annotated literal, i64 payload,
  corruption probe, used-after + escapes non-firing guards) in
  TestSelfHostRcPreciseDropX86IR; byte-identical FIXPOINT + BOOTSTRAP + the wasm
  TestSelfHostRcOptionBoxWasm stay green. (Result[T,E] is the same box shape — a
  follow-up widening.) NOTE: an UNRELATED pre-existing main regression — a wasm Map
  literal `Map{1:10,2:20}` building only 1 entry (TestSelfHostRcCallResultWasm
  freshbuiltin-mapkeys/values) — was found during verification and reproduces with
  all this work stashed; tracked separately, not caused by this slice.
- 2026-07-01: **Widen the consume-by-match free to fresh scalar Result locals**
  (the Result[T,E] sibling of the Option slice above). A scalar
  `var r: Result[i32, i32] = Ok(7)` / `Err(4)` routed ir but leaked: Ok/Err share
  the SAME rc-headered opt_make box as Some (tag 0 = Ok/Some, tag 1 = Err; opt_make
  → __fern_arr_box on x86/arm64, $__fern_str_box on wasm), yet the consume-by-match
  classifier only admitted Some/None, so a fresh Result box was never shallow-freed
  after its match. Fix (classifier-only, in shared `irlower.fern`): (1) new
  `is_scalar_type_name(t)` helper (i32/i64/f64/boolean/u32/u64), and
  `type_is_scalar_option` now delegates to it. (2) `fresh_scalar_option_init` admits
  `Ok`/`Err` (bare + qualified `Result.Ok(..)`) alongside `Some`/`None`, gating on
  the CONSTRUCTED variant's payload scalar-ness via
  `is_scalar_type_name(opt_payload_type(v.type_name, kind))` — Ok reads T, Err reads
  E (opt_payload_type is already variant-aware, depth-counting `[`/`(` so a tuple/
  nested-generic T's comma isn't mistaken for the T-E separator). Only the built
  variant's payload matters: a `var r = Ok(x)` box always holds Ok's payload, so
  Err's annotated type is irrelevant to THIS box; the bare scalar-literal gate
  (`Ok(5)`/`Err(true)`) still covers the un-annotated-but-provable case. SOUNDNESS is
  identical to the Option slice — the scalar-payload gate means the box owns no rc
  pointer so a shallow `__fern_rc_dec` is the whole free; the match reads tag/payload
  as borrows first; the single-match / dead-after / non-escape gates exclude
  used-after / returned / aliased Results. rc-payload Ok/Err (Ok("x") / Err(struct))
  are NOT admitted (payload leaks — a deep-drop follow-up), never double-freed. NOTE:
  an un-annotated `var r = Ok(9)` is NOT native-valid (Result has two type params;
  with no Err in context E stays a free variable — `cannot assign E to i32`), so
  although the literal gate would admit it, no valid program reaches lowering that
  way; the test suite only exercises annotated Results. VERIFIED: new `result-*`
  cases (route ir, value, __rc_underflow()==0: ok/err reclaim, distinct Ok/Err scalar
  types, i64 payload, corruption probe, used-after + escapes non-firing guards) in
  `TestSelfHostRcPreciseDropX86IR`; byte-identical FIXPOINT + BOOTSTRAP + the wasm
  `TestSelfHostRcOptionBoxWasm` + `TestSelfHostIRDiff` all stay green. Frontend-only
  (the classifier is backend-agnostic — every backend that lowers Results benefits).
- 2026-07-01: **RC-payload (scalar-array) Option/Result deep-drop consume-by-match
  free** — the deferred follow-up from the two scalar Option/Result slices. A
  `var o = Some([1,2,3])` / `var r = Ok([..])` / `Err([..])` whose payload is a
  leak-safe scalar ARRAY (i32[]/boolean[]/i64[]/f64[]/u32[]/subword) routed ir but
  leaked BOTH the box AND its array: `fresh_scalar_option_init` only admits a SCALAR
  payload, so the array-payload form fell through to leak-only. This widens the
  consume-by-match free to those, mirroring `consumed_rcpayload_enum_frees` for the
  Option/Result box shape. New in shared `irlower.fern`: (1)
  `fresh_rcpayload_option_init(v)` — returns the constructed variant ("Some"/"Ok"/
  "Err") when the payload is a leak-safe scalar array, proven by annotation
  (`is_leaksafe_array_field(opt_payload_type(v.type_name, kind))`, variant-aware:
  Ok→T, Err→E) OR a fresh scalar-number array-literal payload (`Some([1,2,3])`;
  Option infers its single param — an un-annotated Ok/Err isn't native-valid so only
  annotated Results fire). DISJOINT from `fresh_scalar_option_init` (scalar payload)
  and from `fresh_rcpayload_enum_init` (Some/Ok/Err have no user-enum owner), so an
  option is freed by at most one path. (2) `opt_arm_binding_escapes(st, variant)` —
  the borrow-only-binding gate: only the CONSTRUCTED variant's arm is checked (the
  box always holds that variant's payload; the sibling arm never runs), reusing
  `binding_escapes_arm` so a `Some(v) => v[i] / v.len()` borrow is admitted but a
  binding that stores/returns/passes `v` rejects. (3)
  `consumed_rcpayload_option_frees` — same single-match / dead-after / non-escape
  gates as the enum pass. (4) `emit_opt_payload_drop(s, slot)` — because the
  constructed variant is statically known, the drop is STRAIGHT-LINE (no runtime
  `variant_is` guard the enum path needs): `op_opt_payload` reads the payload
  pointer (offset 8 / 4 per backend), one `__fern_rc_dec` releases the flat scalar
  buffer, then the box is dec'd and the slot zeroed (exit sweep decs a null).
  SOUNDNESS — a scalar-array payload is a flat rc-headered buffer with no inner
  pointers, so a single dec is the whole payload free; the borrow-only gate proves
  the bound `v` doesn't outlive the arm; payload dropped BEFORE the box, each freed
  exactly once. VERIFIED: new `option-arr-*` / `result-*-arr-payload-*` cases (route
  ir, value, `__rc_underflow()==0`: annotated + un-annotated Some, f64[]/Ok/Err
  arrays, .len()+index borrow, heap-reuse corruption probe, binding-escapes +
  used-after + returned non-firing guards) in `TestSelfHostRcPreciseDropX86IR`; new
  wasm `option-arr-payload-*` / `result-ok-arr-payload-freed` + a 50k churn in
  `TestSelfHostRcOptionBoxWasm` (the shared classifier drives wasm too); byte-
  identical FIXPOINT + BOOTSTRAP + `TestSelfHostIRDiff` all stay green. Frontend-only
  (classifier + emitter are backend-agnostic — every backend that lowers
  Options/Results benefits).
- 2026-07-01: **Widen the rc-payload Option/Result deep-drop to scalar-only STRUCT
  payloads** (follow-up to the array-payload slice). A `var o = Some(P{..})` /
  `Ok(P{..})` whose payload is a FRESH scalar-only struct literal now frees the
  payload box (shallow — the box holds only inline scalars) then the option box,
  right after its single consuming match — previously the struct payload leaked (the
  rc-payload option admitted only array payloads). Changes in shared `irlower.fern`:
  `fresh_rcpayload_option_init` → `rcpayload_option_cand(v, structs)` returning an
  `OptRcCand { variant, ptype }`; it now also admits a `parser.ExprStructLit`
  payload when the struct is leak-safe AND `struct_is_scalar_only` (a bare-ident
  struct payload aliasing a swept local is rejected — the fresh-literal rule keeps
  the payload box sole-owner rc==1, so a shallow dec can't double-free). Emission
  (`emit_opt_payload_drop`) is UNCHANGED — a scalar-array buffer and a scalar-only
  struct box are both released by one `__fern_rc_dec` on the `op_opt_payload`
  pointer, so no per-type dispatch is needed. SOUNDNESS mirrors the array case: the
  payload owns no rc pointer (scalar-only), the borrow-only arm-binding gate proves
  the bound `p` doesn't outlive the arm, payload freed before the option box, each
  once. DEFERRED: an array-FIELD-bearing struct payload (`Some(Buf{xs,n})`) is NOT
  admitted — a `__struct_drop_<T>` field deep-drop through the option-payload path
  over-releases by one (a subtle rc interaction distinct from the enum path, which
  deep-drops such payloads cleanly); left to a follow-up. VERIFIED: new
  `option-struct-payload-*` / `result-ok-struct-payload-*` cases (route ir, value,
  `__rc_underflow()==0`: annotated + un-annotated Some(P), Ok(P), corruption probe,
  bare-ident + binding-escapes non-firing guards) in `TestSelfHostRcPreciseDropX86IR`;
  new wasm `option-struct-payload-freed` in `TestSelfHostRcOptionBoxWasm`; byte-
  identical FIXPOINT + BOOTSTRAP + `TestSelfHostIRDiff` all stay green.
- 2026-07-01: **Precise drop-on-last-use for scalar Option/Result locals** — the
  option sibling of the scalar struct/tuple precise-if drops (#16/#18). A fresh
  `var o = Some(5)` / `None` / `Ok(4)` whose LAST use is a NESTED block (an if-body
  `match (o)`, a `.is_some()` borrow) rather than a top-level consuming match
  previously LEAKED: consumed_scalar_enum_frees only finds a TOP-LEVEL match
  scrutinee, so a conditionally-consumed option box was never freed. precise_drop_
  names now admits a fresh scalar Option/Result (fresh_scalar_option_init) and, at
  its last-use statement, shallow-frees the rc-headered box (opt_make/opt_none →
  __fern_arr_box; the scalar payload carries no rc pointer) + zeroes the slot — the
  release moved earlier and bounded, exactly like the array/struct/tuple precise
  drops. DISJOINTNESS from consumed_scalar_enum_frees is by construction: the option
  is admitted to precise_drop_names ONLY when it has NO top-level match of its name
  (a local `has_tlm` scan) — so an option with a top-level consuming match stays
  owned by consumed_scalar_enum_frees (which also feeds the donor/reuse paths) and
  is never freed twice. The emission-site guard is type_is_scalar_option(opt_type_
  of_slot(slot)); options are never exit-swept, so the precise dec is the box's only
  release. VERIFIED: new option-precise-if-* / result-precise-if / option-precise-
  none / corruption-probe cases + an option-toplevel-match-not-double-freed
  disjointness guard in TestSelfHostRcPreciseDropX86IR; new wasm option-precise-if-
  freed in TestSelfHostRcOptionBoxWasm; byte-identical FIXPOINT + BOOTSTRAP +
  TestSelfHostIRDiff all stay green. Frontend-only (precise_drop_names + the
  emission branch are backend-agnostic).
- 2026-07-01: **Reclaim array-FIELD struct-payload Option/Result** (resolves the
  #4210 deferred over-release). `Some(Buf{xs:[..], n})` / `Ok(Buf{..})` — an option
  whose payload is a fresh struct literal with leak-safe ARRAY fields — is now
  admitted to the consume-by-match free alongside the scalar-only-struct case, with
  the SAME shallow box free. ROOT CAUSE of the earlier over-release: the deferred
  attempt emitted __struct_drop_<Buf> to deep-drop the payload's array fields, but on
  the OPTION-payload path the struct does NOT own a counted reference to those array
  fields (unlike the enum path, whose construction alias-incs the field per
  asm_ir.go:emit_ir_struct_drop_one's balance note) — the surrounding machinery
  already reclaims them, so __struct_drop_ decremented xs a SECOND time and
  underflowed (detector==1). Bisection confirmed: even a `Some(_)` wildcard arm
  over-released, isolating the fault to the free itself (not any arm field-read /
  binding). FIX: drop the __struct_drop_ deep-drop entirely — the shallow
  __fern_rc_dec on the op_opt_payload struct box (reused verbatim from the scalar-
  struct case) frees the payload box + the option box, each exactly once, while the
  array fields ride their existing reclamation. rcpayload_option_cand now admits a
  nested_field_deep_drop_ok struct payload; emit_opt_payload_drop is unchanged
  (uniform shallow free). SOUNDNESS: the underflow-on-double-dec proves xs reaches 0
  WITHOUT our dec (so it is machinery-owned, never leaked), and the corruption probe
  (fresh arrays after the match read intact) rules out an early free / UAF. VERIFIED:
  new option-struct-arrfield-payload-* (value, detector, wildcard arm, corruption
  probe) in TestSelfHostRcPreciseDropX86IR; new wasm option-struct-arrfield-payload-
  freed in TestSelfHostRcOptionBoxWasm; byte-identical FIXPOINT + BOOTSTRAP +
  TestSelfHostIRDiff all stay green.
- 2026-07-01: **Precise drop-on-last-use for rc-PAYLOAD Options in nested blocks**
  (the rc-payload sibling of the scalar-option precise-if drop). An
  `Option[i32[]]` / `Option[P]` / `Option[Buf]` whose LAST use is a NESTED block (an
  if-body `match (o)`) — not a top-level consuming match — previously LEAKED its
  payload + box. precise_drop_names now admits such an option (rcpayload_option_cand,
  variant "Some") when: it has NO top-level match (disjoint from
  consumed_rcpayload_option_frees), AND the nested consuming match borrows (does not
  escape) its payload binding. Because the match is NESTED, the existing
  opt_arm_binding_escapes (single-match) is lifted over the whole body by a new
  recursive opt_body_binding_escapes (through if/while/for/match) — body_unsafe_for
  only proves the OPTION escapes, never its bound payload. At the emission site,
  the new opt_slot_is_rcpayload(slot) predicate dispatches: type_is_scalar_option →
  the plain box dec (scalar payload is an inline value, never a pointer), else an
  rc-payload Option → emit_opt_payload_drop (op_opt_payload → payload dec → box dec),
  the same free the consume-by-match rc-payload path uses. RESTRICTED to Option (not
  Result): a Result box's payload variant (Ok vs Err) is ambiguous at the emission
  site (the slot carries only the type), so its rc-payload precise-drop stays
  deferred — the consume-by-match path records the constructed variant and is
  unaffected. SOUNDNESS: the box + payload exist on every path through the enclosing
  statement (the ctor ran before it), so freeing after it is sound whether or not the
  nested match executed; the borrow-only gate proves the payload isn't aliased past
  the free. VERIFIED: new option-arr-precise-if-* / option-struct-precise-if /
  option-arrfield-struct-precise-if / corruption-probe + a binding-escapes non-firing
  guard in TestSelfHostRcPreciseDropX86IR; new wasm option-arr-precise-if-freed in
  TestSelfHostRcOptionBoxWasm; byte-identical FIXPOINT + BOOTSTRAP + TestSelfHostIRDiff
  all stay green.
- 2026-07-01: **Precise-drop Option/Result parity via a per-drop kind** (fixes a
  latent scalar-Result non-firing + adds rc-payload Result precise-drop). The
  precise-drop emission dispatched options on the SLOT TYPE
  (type_is_scalar_option / opt_slot_is_rcpayload), both Option-only — so a scalar
  RESULT admitted by precise_drop_names matched NO emission branch and silently
  never fired (its box leaked; sound but a missed reclaim), and rc-payload Result
  had no precise-drop at all (the Ok-vs-Err payload variant can't be read from the
  slot type Result[T,E]). Fix: PreciseDrops carries a parallel `kinds: string[]` —
  "" (the array/struct/tuple type-based branches), "opt-shallow" (a scalar
  Option/Result box → plain __fern_rc_dec), or "opt-rcpayload" (an rc-payload
  Option/Result → emit_opt_payload_drop). precise_drop_names records the kind when
  it admits the candidate (the constructed variant is known there via
  rcpayload_option_cand / fresh_scalar_option_init), and the emission dispatches on
  it. The is_rcopt gate widened from Option-only (variant "Some") to any Result/
  Option constructor (variant != ""); emit_opt_payload_drop reads offset-8, which is
  the constructed variant's pointer payload regardless of Ok/Err, so the recorded
  "opt-rcpayload" kind is exactly the "offset-8 is a pointer" witness the slot type
  lacks. opt_slot_is_rcpayload (the now-redundant Option-only emission predicate) is
  removed. SOUNDNESS unchanged from the Option precise-drop: no-top-level-match
  disjointness + borrow-only nested binding (opt_body_binding_escapes) + box/payload
  exist on every path through the enclosing statement. VERIFIED: new result-scalar-
  precise-if-* / result-arr-ok-precise-if / result-arr-ERR-precise-if / result-
  struct-precise-if in TestSelfHostRcPreciseDropX86IR (the Err-array case pins the
  kind-not-slot-type resolution); new wasm result-scalar-precise-if-freed /
  result-arr-precise-if-freed in TestSelfHostRcOptionBoxWasm; byte-identical
  FIXPOINT + BOOTSTRAP + TestSelfHostIRDiff all stay green.
- 2026-07-01: **Precise drop-on-last-use for scalar user-enum locals** (the enum
  sibling of the scalar-option precise-if drop, unlocked by the per-drop kind). A
  fresh `var x = Circle(7)` of an all-scalar-variant enum whose LAST use is a NESTED
  block (an if-body `match (x)`) — not a top-level consuming match — previously
  LEAKED its box: consumed_scalar_enum_frees only finds a TOP-LEVEL match scrutinee.
  precise_drop_names now admits such an enum (fresh_scalar_enum_init, no-top-level-
  match) with kind "box-shallow", and the emission shallow-frees its rc-headered box
  (struct_make → __fern_arr_box). KEY: this was previously blocked because an
  UN-annotated `var x = Circle(7)` slot carries NO struct_type (expr_struct_type
  returns "" for a bare variant ctor, and there's no annotation to fall back on), so
  a slot-type-gated emission couldn't identify it — but the KIND ("box-shallow",
  recorded at precise_drop_names time where the enum ctor IS visible) is itself the
  "free this box shallowly" witness, needing no slot type. The "opt-shallow" kind was
  renamed "box-shallow" (it now covers scalar Option / Result / enum boxes — all a
  plain __fern_rc_dec). DISJOINTNESS + no donor-guard: a no-top-level-match enum is
  never in consumed_scalar_enum_frees, and every reuse-donor path (enum_donor_reuse_
  sites, struct/tuple reuse, inarm reuse) flows from a top-level match / a struct /
  tuple — so such an enum is NEVER a donor, and the shallow free can't race a box
  recycle. Enum boxes are never exit-swept, so the precise dec is the box's only
  release. VERIFIED: new enum-scalar-precise-if-* / enum-annotated-precise-if /
  corruption-probe + an enum-toplevel-match-not-double-freed disjointness guard in
  TestSelfHostRcPreciseDropX86IR; byte-identical FIXPOINT + BOOTSTRAP (the compiler's
  own enum-heavy code) + wasm TestSelfHostRcOptionBoxWasm + TestSelfHostIRDiff all
  stay green.
- 2026-07-01: **Precise drop-on-last-use for rc-PAYLOAD user-enum locals in nested
  blocks** (completes the precise-drop parity story — the rc-payload-enum sibling of
  the rc-payload-option precise drop). A fresh `var x = Poly([..])` / `V(Buf{..})` —
  a variant carrying a leak-safe array or deep-drop-ok struct payload — whose LAST
  use is a NESTED block (an if-body `match (x)`) previously LEAKED its box + payload
  (consumed_rcpayload_enum_frees only finds a TOP-LEVEL match). precise_drop_names
  now admits such an enum (fresh_rcpayload_enum_init) when: no top-level match
  (disjoint from consumed_rcpayload_enum_frees; and rc-payload enums are never fed to
  the donor path, so no donor guard is needed), AND no arm binds an rc payload that
  escapes — the nested-match lift enum_body_binds_rc_payload (through if/while/for/
  match) of match_arm_binds_rc_payload (a borrow-only binding is admitted). The
  emission deep-drops the runtime variant + frees the box via emit_enum_variant_drops;
  the enum NAME (which the slot type can't provide) rides in the kind
  "enum-rcpayload:<Enum>", parsed at the emission site. VERIFIED: new
  enum-arr-precise-if-* / enum-struct-precise-if (deep-drop-ok Buf payload) /
  corruption-probe + an enum-arr-binding-escapes non-firing guard in
  TestSelfHostRcPreciseDropX86IR; fixpoint + bootstrap + wasm + IR-diff running.
  Net: EVERY precise-drop shape now reclaims — scalar (struct / tuple / option /
  result / enum) + rc-payload (option / result / enum), via both consume-by-match
  (top-level) and precise-drop (nested-block).
- 2026-07-01: **Precise drop-on-last-use for array-FIELD struct locals** (widens the
  scalar-only-struct precise drop #16 — a peak-heap bounding, the struct sibling of
  the array / tuple precise drops). A fresh `var b = Buf { xs: [..], n }` last-used in
  a NESTED block was reclaimed only by the function-exit sweep; it now deep-drops its
  leak-safe array fields (emit_struct_field_drops) + shallow-frees the box right after
  that statement, bounding the live set. This is EXACTLY the exit-sweep struct free
  moved earlier (the exit sweep already did emit_struct_field_drops + rc_dec for
  reclaimable structs regardless of scalar-only-ness; only the precise path was gated
  on struct_is_scalar_only). precise_drop_names' struct admission widened from
  `struct_is_scalar_only && all_scalar_literal_args` to struct_lit_precise_ok: every
  declared field present and either a flat scalar initialised by a scalar literal OR a
  leak-safe array (is_leaksafe_array_field) initialised by a FRESH scalar-number array
  literal — sole-owner (rc==1), field order resolved by NAME (field_names) so a
  reordered `Buf { n: 3, xs: [..] }` maps correctly. The emission dropped the
  struct_is_scalar_only gate and now always calls emit_struct_field_drops (a no-op for
  a scalar-only struct, subsuming the old branch) before the box dec; the reuse-donor
  guards (xreuse/reuse/edon/inarm) are unchanged. SOUNDNESS: the fresh-literal gate
  makes the box the sole owner of each array field, so the deep-drop frees each buffer
  once; the slot is zeroed so the exit sweep decs a guarded null (no double-free). NOT
  the #4219 option-payload quirk — a struct LOCAL owns a counted ref to its array
  fields (the exit sweep deep-drops them), unlike an option payload struct. VERIFIED:
  new struct-arrfield-precise-if-* / reordered / corruption-probe in
  TestSelfHostRcPreciseDropX86IR; byte-identical FIXPOINT + BOOTSTRAP (the compiler's
  own array-field struct locals now drop early — the strongest test) + wasm + IR-diff
  gates verified.
- 2026-07-01: **Map local reclaim — slice 1 (exit-sweep free of the keys/values
  buffers)**. A fresh `var m = Map { … }` / `map_new()`, used only via read methods
  (get_or/has/len) and NOT escaping / iterated, now has its KEYS and VALUES buffers
  freed at scope exit — previously every map local leaked entirely. KEY MECHANISM (no
  new runtime helper): the mapbox is a raw __fern_alloc(16) {keys@0, values@8}, but
  the keys/values buffers come from __fern_alloc_u8 → __fern_arr_box, so they are
  rc-headered arrays freeable by the EXISTING __fern_rc_dec (== __fn___fern_arr_dec,
  rc-guarded: frees at rc==1, safe-decs an aliased m.keys()/values() view). op_raw_
  load_ptr reads mapbox word 0 (keys) / word 1 (values); emit_map_buffers_free decs
  each. The 16-byte RAW mapbox leaks for now (a raw-box free is a follow-up). Uses
  only existing IR ops, so it lowers on all three backends unchanged. ESCAPE ANALYSIS
  works out of the box: body_unsafe_for already treats a method-call receiver
  (m.get/m.set/…) as a BORROW, so a method-only map is non-escaping; a map passed /
  stored / returned escapes and is excluded. ITER-ALIASING HOLE closed: m.iter() (and
  `for..in m`) builds a MapIter box holding a raw pointer to the mapbox / its buffers,
  so map_is_iterated excludes any iterated map (conservative — `for..in m` is
  actually safe but indistinguishable cheaply from an escaping iter). GATE
  (reclaimable_names_of "MAP:" prefix, slot_is_reclaimable_map): fresh map init +
  not reassigned + not aliased + body_unsafe_for false + not iterated. SOUNDNESS: the
  gate proves the buffers are sole-owned (rc==1), so each is freed exactly once; the
  rc-guard makes an aliased buffer a safe dec. VERIFIED: new map-reclaim-* cases
  (value, __rc_underflow()==0 detector, heap-reuse corruption probe, passed-to-fn +
  iterated non-firing guards) in TestSelfHostRcPreciseDropX86IR; byte-identical
  FIXPOINT + BOOTSTRAP stay green (the compiler's own heavy map usage self-compiles
  correctly — the decisive soundness signal). Follow-ups (task #22): widen to iterated
  maps (precise iter-escape tracking), precise-drop for map locals, deep-drop string/
  array VALUES, reclaim the 16-byte mapbox.
- 2026-07-01: **Map local reclaim — slice 2 (precise drop-on-last-use)**. A fresh
  `var m = Map { … }` last-used in a NESTED block now has its keys/values buffers
  freed right after that statement (precise_drop_names kind "map-buffers" →
  emit_map_buffers_free), earlier than the function-exit sweep — bounding peak heap,
  the map sibling of the array/tuple/struct/option/enum precise drops. Same gates as
  the exit-sweep "MAP:" set (fresh init, not reassigned, not aliased, non-escaping,
  not iterated). emit_map_buffers_free gained a NULL GUARD + slot-zero: the precise
  drop zeroes the mapbox slot after freeing, so the exit sweep's second call on the
  same slot sees null and skips (no double-free); the guard also hardens the
  exit-sweep path itself against a conditionally-declared map (an untaken-branch slot
  is null). op_block/op_brif with op_bin("not") is the emit_enum_variant_drops
  null-guard idiom. VERIFIED: new map-precise-if-* / map-conditional-decl (null-guard)
  / map-precise-corruption-probe cases in TestSelfHostRcPreciseDropX86IR; byte-
  identical FIXPOINT + BOOTSTRAP + wasm + IR-diff stay green (the null-guard changes
  the exit-sweep emission for the compiler's own map functions, so the self-compile
  re-verifies).
- 2026-07-01: **Map local reclaim — slice 3 (admit `for (k,v) in m` iterated maps)**.
  The prior slices excluded ANY iterated map (conservative). But a `for (k,v) in m`
  is actually SOUND to reclaim: its iter is a scoped loop temp (dead by the free), and
  the loop's k/v bindings are copies of scalars OR of pointers to SEPARATELY-allocated
  (leaked) elements — freeing the keys/values BUFFERS never dangles them (the buffers
  hold pointers, not the elements; the reclaim runs after the loop completes). The
  ONLY real hole is an EXPLICIT `m.iter()` whose MapIter box (holding a raw pointer to
  the buffers) can escape. So map_is_iterated → map_has_explicit_iter: the StmtFor case
  now uses expr_iters_map(f.iter) (excludes only `for x in m.iter()`, admits
  `for (k,v) in m`), keeping the explicit-iter expr exclusion everywhere else. This
  reclaims the COMMON map-traversal shape (many of the compiler's own maps). VERIFIED:
  map-iterated-not-freed → map-foreach-reclaimed-detector (now FIRING) + a
  for..in corruption probe in TestSelfHostRcPreciseDropX86IR; byte-identical FIXPOINT +
  BOOTSTRAP + wasm + IR-diff stay green (the compiler's own `for (k,v) in m` maps —
  and its explicit m.iter() uses, which stay excluded — self-compile correctly, the
  decisive soundness signal for the widening).
- 2026-07-01: **Map local reclaim — slice 4 (mapbox reclaim + wasm-correctness fix
  via a per-backend `__fern_map_free` helper)**. The prior slices freed a reclaimable
  map's keys/values buffers by reading the mapbox words with `op_raw_load_ptr`
  (mapbox[0]=keys, mapbox[1]=vals) and dec'ing each — an ASM-SHAPED sequence assuming
  the register backends' raw 16-byte mapbox `{keys@0,vals@8}`. Two problems: (a) the
  raw 16-byte box itself was left to leak; (b) — the real bug — `raw_load_ptr` does
  NOT lower on the wasm backend (it fell through to a `;; unsupported bin raw_load_ptr`
  comment), so a reclaimable map local in a wasm module left the operand stack
  imbalanced and wasmtime REJECTED the module ("values remaining on stack at end of
  block"). i.e. a valid program like `var m = Map{1:2}; return m.get_or(1,0);` failed
  to compile on the wasm self-host IR backend. FIX: emit_map_buffers_free now emits a
  single `call_direct("__fern_map_free", 1)` — a fern-helper routed per backend:
  register backends get a new `__fn___fern_map_free` (asm_ir + asm_arm64 emit_runtime)
  that frees the keys+vals buffers via `__fn___fern_arr_dec` (rc-guarded) AND returns
  the raw 16-byte box to the size-class-2 freelist (the mapbox reclaim); wasm routes
  (wasm_helper_symbol) to the AST path's `$__fern_map_release`, which null-guards,
  rc-guards, DEEP-drops string K/V, and frees the 40-byte rc-headered box + all three
  arrays. The helper is null-safe on every backend, so the outer null guard / block
  is gone (the precise-drop + exit-sweep double-call is a safe null no-op via the
  slot-zero). This makes map reclaim correct on wasm (the landmine) AND completes the
  mapbox reclaim on the register backends. `$__fern_map_release` is always present
  when a module uses maps (map_helpers is gated on module_uses_maps), so no new wasm
  gating is needed. VERIFIED: new TestSelfHostMapReclaimIR{X86_64,Wasm} (borrow-only,
  two-sequential-maps freelist stress, grown map) — the exact repro now compiles+runs
  to the right exit on BOTH backends (pre-fix wasm produced invalid wat); byte-
  identical FIXPOINT + BOOTSTRAP stay green (the compiler's own map reclaims now route
  through __fern_map_free and self-compile identically), RcPreciseDropX86IR +
  MapValuePtrIR + RcOptionBoxWasm unaffected.
- 2026-07-01: **Reuse — cross-TYPE same-box-class struct reuse (general-reuse parity)**.
  The same-block cross-construction reuse (a dead donor's box reused in place by a
  later full construction) required the donor and recipient to be the SAME struct
  type (`fresh_struct_lit_type(donor) == c_type`). Native's general reuse also does
  CROSS-type same-box-class reuse (e.g. dead `Point{x,y}` → `Pair{a,b}`, both 16-byte
  — TestGeneralReuseFiresCrossTypeSameClass). Ported via a new
  `structs_reuse_compatible(structs, dt, rt)` gate: same type is always compatible;
  cross-type is admitted ONLY under the bulletproof-safe subset — BOTH structs are
  entirely SCALAR (no pointer fields → a pure scalar overwrite, no old-value release
  and no offset-aligned dec) with the SAME field count and per-position IDENTICAL slot
  widths. Identical widths in order ⇒ byte-identical box layout ⇒ identical freelist
  size class, so the reused box is exactly the right size and the recipient's
  struct_make overwrites it cleanly (the box's cap/rc header, sized at the donor's
  alloc, stays valid). `emit_cross_struct_reuse` already writes every field with the
  RECIPIENT's type/offsets (the donor box is only loaded as a raw pointer), so NO emit
  change was needed — only the donor-eligibility gate widened. Array-/pointer-field
  cross-type reuse (which would need an offset-matched release of the donor's old
  pointers) stays same-type-only. VERIFIED: new TestSelfHostCrossTypeReuseIR{X86_64,
  Wasm} (point→pair, mixed-width i64+i32, a reuse-then-alloc freelist corruption probe,
  and a different-field-count case that must NOT reuse but stays correct) +
  TestSelfHostCrossTypeReuseFiresX86_64, which asm-counts `call __fern_arr_box`:
  the dead cross-type donor yields 1 struct-box alloc (box reused), the donor-read-after
  variant yields 2 (no reuse) — direct proof the optimization lowers in place. Byte-
  identical FIXPOINT + BOOTSTRAP stay green (the compiler's own cross-type reuses now
  fire and self-compile deterministically); RcPreciseDropX86IR unaffected.
- 2026-07-01: **Reuse — cross-type reuse widened to leak-safe-array fields (pointer-field
  general-reuse parity)**. The cross-type same-box-class reuse (prior slice) admitted only
  ALL-SCALAR structs. Native's general reuse also reuses across types with POINTER fields
  (TestGeneralReuseFiresCrossTypePointerField: dead Holder{id,items:i32[]} → Bag{tag,data:i32[]}).
  structs_reuse_compatible now admits a per-position leak-safe-ARRAY field (i32[]/i64[]/f64[]/
  boolean[]) when BOTH structs have an array at that position (matching KIND — the emit
  dispatches scalar-overwrite vs array-dec by the RECIPIENT's field kind, so a scalar donor
  slot under an array recipient position would be dec'd as a bogus pointer). emit_cross_struct_reuse
  already rc-dec's the donor's OLD array at the field offset before writing the recipient's fresh
  one — the dec is rc-GUARDED (frees at rc==1 else decrements, so a co-owned donor array is never
  double-freed), and the recipient's array is separately required FRESH (cross_reuse_sites' `ok`
  gate / xblock_struct_arrays_fresh), so no emit change was needed. Identical widths + kinds ⇒
  byte-identical layout ⇒ same freelist class. String / nested-struct / map / option / tuple
  fields (needing a deep element walk) stay excluded. VERIFIED: new array-field-cross +
  array-field-cross-then-alloc-probe cases (x86 + wasm, __rc_underflow()==0 confirms exactly-once
  array release) + a 200k-iter freelist-corruption stress + the fires-assertion extended with an
  array-field 3-vs-4 arr_box dead/live pair (proves in-place lowering). Byte-identical FIXPOINT +
  BOOTSTRAP stay green.
- 2026-07-02: **Perceus — close the enum self-reassign payload-array leak**. A loop-carried
  array-payload enum `var b: E = V0([..]); while (..) { b = V1([..]); b = V2([..]); }`
  shallow-freed each superseded box (box-only arr_dec) but LEAKED the superseded variant's
  payload array on the register backends — an O(n) growth leak (a 50M-iter 8-elem churn
  OOMs on x86; native reuses the box in place, TestEnumReuseFiresAcrossVariants). Fix:
  reassigned array-payload enum locals are credited "ENUMRE:<E>:<name>"
  (enum_reassign_reclaim_names, appended in reclaimable_names_of); the StmtAssign routes
  them to emit_enum_reclaim_store, which DEEP-DROPS the superseded box (payload arrays +
  box, runtime variant-dispatch via the existing emit_enum_variant_drops) before storing
  the fresh one. SOUNDNESS gates (enum_only_wildcard_used_rec + enum_all_variants_array_payload):
  every variant payload is a leak-safe SCALAR ARRAY (a single arr_dec fully releases it —
  NO nested-struct payload, whose __struct_drop would double-free a shared value), every
  reassign RHS is a FRESH variant ctor with fresh array-literal payloads (sole ownership),
  and b's payload is NEVER bound in any match (all-`_` arms, no arm re-references b) so it
  can never be aliased — anything else (a payload binding, a return/store/call-arg escape,
  an iife-match) disqualifies b, keeping the safe box-only free. DISJOINT from the
  consume-by-match enum frees (which require b NOT reassigned). No cow guard (a fresh ctor
  never equals the old box). On WASM this pattern is ALREADY reclaimed (verified: a 50M-iter
  literal-payload churn is flat on clean wasm), so the extra dec is a rc-GUARDED no-op there
  (RcOptionBoxWasm's underflow==0 assertion stays green). VERIFIED: new
  TestSelfHostEnumReassignReclaim{X86_64,Wasm} — wildcard churn, a 3M-iter flat-heap check
  (exit 137 if the payload leaks), a fresh-array corruption probe, and a payload-bound
  fallback (disqualified, stays correct) — plus byte-identical FIXPOINT + BOOTSTRAP +
  RcPreciseDropX86IR + RcOptionBoxWasm. (A self-host-checker quirk surfaced en route: it
  mis-infers a `string[]`-element `.len()` receiver as i32 — emitting an undefined
  __fn_i32__len that broke the bootstrap link — worked around by comparing `!= ""` on a
  typed local; the native checker types it correctly.)
- 2026-07-02: **Investigation — typing match-payload bindings for imported union members
  is blocked on the #3425 per-module-emit memory frontier.** The self-host-checker quirk noted
  in the previous entry (a `string[]`-element `.len()` receiver mis-inferred as i32 →
  undefined `__fn_i32__len`) root-causes to match-payload TYPING: a QUALIFIED pattern on an
  imported UNION member (`mod.Struct(x)`, whose flattened decl is the mangled `mod__Struct`)
  bails the whole function to the AST emitter, because the lowering strips the qualifier to
  the bare `Struct` (which only names a same-module ENUM variant). Same-module user-variant
  payloads are ALREADY typed (the union_member binding marks the slot with the variant struct
  name); only the qualified/imported case misses. The IR-path fix is small and correct — build
  the `mod__Struct` mangled candidate and prefer it when it names a declared struct, so the
  member lowers AND binds the payload typed `mod__Struct` (verified end-to-end on a two-module
  user program: the match decides `ir`, emits no `__fn_i32__len`, and matches the interpreter
  oracle). BUT flipping the self-host's OWN many `parser.*` / AST-node matches from AST to IR
  all at once exhausts the per-module-emit memory ceiling: the whole-compiler module-9 build
  climbs steadily to the ~3.9 GiB self-host arena and traps (exit 137) —
  TestSelfHostModloadPerModuleWholeCompilerX86_64 needs all 12 units to emit + link, so no
  partial exclusion is possible. This is the same un-reclaimed per-function IR-op allocation
  limitation (#3425) that pins the merged-bundle budget, i.e. it is gated on GOAL 2 (the
  self-host Perceus reclaim). The widening therefore stays deferred behind a NOTE in
  lower_stmt's match handler; the bootstrap keeps the `!= ""` standalone workaround. Once the
  self-host reclaim lands and per-module emit fits, the mangled-lookup flip + a two-module
  user-program e2e test can go in unblocked.
- 2026-07-02: **Perceus reuse — IN-PLACE enum self-reassign box reuse (native FBIP parity).**
  The enum self-reassign reclaim (previous entry) closed the payload leak via FREE+ALLOC
  (emit_enum_reclaim_store: deep-drop the old box, then store the freshly-allocated new box).
  Native instead reuses the box IN PLACE — zero alloc/free churn per reassign. This ports that:
  a loop-carried array-payload enum `var b = V0([..]); while (..) { b = V1([..]); b = V2([..]); }`
  whose enum has UNIFORM variant layout (enum_all_variants_same_field_count — every variant the
  same box size, so any variant fits b's existing box) now lowers each `b = V(args)` reassign as
  emit_enum_inplace_reassign: (1) release b's OLD variant payload arrays via
  emit_enum_variant_payload_drops (emit_enum_variant_drops MINUS the box free + slot zero — the
  box is kept), (2) re-shape the box to V (op_struct_set_shape, mirroring emit_enum_donor_reuse),
  (3) write V's fresh array-literal payloads into the SAME box (op_struct_set). The slot is
  untouched, so the exit sweep still frees the box exactly once. Wired in StmtAssign BEFORE the
  RHS is lowered (lowering `V([..])` would allocate a fresh box, defeating the reuse); a RAGGED
  enum (differing variant box sizes) or any non-`V(args)` RHS falls through to the still-correct
  free+alloc emit_enum_reclaim_store. SOUND: b's payload is never aliased
  (enum_only_wildcard_used_rec) and payloads are FRESH array literals
  (variant_ctor_array_payloads_fresh), so releasing the old arrays + owning the new ones is
  balanced; the over-release detector confirms it. NET vs main: 2 fewer enum-box allocations AND
  2 fewer box frees per reassign site (the box is recycled), a real churn reduction on the
  register backends; on wasm (already flat) it stays correct. VERIFIED: 4 new x86 IR cases in
  TestSelfHostRcPreciseDropX86IR (wildcard-churn value=2 + `__rc_underflow()==0` detector, a
  fresh-array heap-reuse corruption probe=150 detector, and a payload-bound fallback that is
  disqualified from the reclaim and keeps the box-only free) — the detector==0 is the soundness
  gate for the in-place MUTATION (a mis-balanced old-payload release or corrupted reshape would
  double-free) — plus the existing TestSelfHostEnumReassignReclaim{X86_64,Wasm} and byte-identical
  BOOTSTRAP + Stage2 FIXPOINT.
- 2026-07-02: **Landed the qualified-imported-union match-payload widening (task #24) — unblocked
  by the #3425 sharding.** A QUALIFIED pattern on an imported UNION member (`match (v) {
  mod.Struct(x) => ... }`, whose flattened decl is the mangled `mod__Struct`) used to BAIL the
  whole function to the AST emitter — the lowering stripped the qualifier to the bare `Struct`,
  which only names a same-module ENUM variant. On the AST fallback the untyped payload's
  `x.field.len()` on a `string[]` field mis-dispatched to an undefined `__fn_i32__len`. The
  match-lowering now builds the `'.'→'__'` mangled candidate and prefers it when it names a
  declared struct, so the member lowers through the IR path AND binds the payload typed
  `mod__Struct` (a later `x.field` read dispatches correctly). This was ROOT-CAUSED and the fix
  BUILT earlier, but flipping the self-host's own many `parser.*` AST-node matches from AST to IR
  at once added ~26 KB of irlower asm and OOM'd the whole-compiler module-9 per-module emit
  (#3425). With the per-module-emit function-window sharding now landed, that extra codegen is
  bounded per shard, so the widening fits: TestSelfHostModloadPerModuleWholeCompilerX86_64 stays
  green (220 s vs 154 s — bigger emit, more shards, still under the ceiling). VERIFIED:
  TestSelfHostUnionVariantFieldIR (a two-module `Row | Blank` union matched via qualified patterns
  reading a `string[]` payload field — decides `ir`, emits no `__fn_i32__len`, matches the
  interpreter oracle) + byte-identical BOOTSTRAP + Stage2 FIXPOINT (incl. the whole-compiler
  self-compile). The bootstrap's own standalone compile (asm_run, AST path — imported modules'
  struct decls not loaded) still can't type these payloads, so enum_only_wildcard_used_rec keeps
  its `!= ""` standalone workaround; the widening helps every module-LOADING path (the per-module
  build and user programs).
- 2026-07-03: **Landed string[] ELEMENT reclaim on the IR path (#4355 slice, all
  three backends).** A fresh, non-escaping `string[]` local whose EVERY stored
  element is provably fresh (a concat / named producer / `.to_upper`-family
  copy) or a static literal is credited "SARR:<name>" by reclaimable_names_of,
  and the exit sweep frees it with the new `__fern_str_arr_free` instead of the
  shallow buffer-only dec: at rc==1 it `__fern_str_free`s every element box
  (rc-aware — a shared element decs, a literal's .rodata data is
  heap-guard-skipped, an immortal view is skipped, rc==0 ticks the underflow
  detector) then returns the buffer to its size-class freelist. Soundness rides
  a dedicated element-hazard walk (strarr_unsafe_for / strarr_expr_unsafe):
  exactly one reassignment form is sanctioned — the self-append rebind
  `xs = xs.append(<fresh|literal>)`, whose grow MOVES elements buffer-to-buffer
  while the assign's cow-guarded shallow dec frees only the superseded buffer —
  and any lasting element alias (a `var t = xs[i]` binding, `return xs[i]`, a
  container/struct/tuple store, a method arg, a non-borrowable call arg, an
  element slice/trim view, `for x in xs`) excludes the array (elements keep the
  sound leak). Transient element reads stay admitted: binary operands
  (`xs[i] + y`, `xs[i] == y`), read-only / fresh-copy method receivers
  (`.len()` / `.to_upper()` / `.to_lower()` / `.reverse()` / `.repeat(n)` /
  `.starts_with` / `.ends_with` / `.contains` / `.index_of`), byte reads
  `xs[i][j]`, match scrutinees, and DIRECT `xs[i]` args at borrowable params.
  Backends: x86-64 `__fn___fern_str_arr_free` (asm_ir.emit_ir_runtime), arm64
  mirror (asm_arm64.emit_runtime), wasm routes to the existing
  `$__fern_arr_dec_ptr` (a wasm heap string is a single inline rc-headered
  block, so the per-element `$__fern_arr_dec` IS the wasm string free);
  ssa_lift no-ops it (leak-only SSA heap); the IR round-trip evaluator models
  it value-preserving (is_reclaim_helper). VERIFIED:
  TestSelfHostStrArrElemReclaimIRX86_64 (2M build/drop churn stays flat +
  underflow 0; alias/return/borrowed-store exclusion cases assert the call is
  NOT emitted and stay balanced), arm64 + wasm siblings (correctness +
  underflow + emitted-shape assertions). Native reference:
  `__fern_drop_arr_str` (rc_insert.go) — the native flat-`string[]` element
  walk this mirrors. Remaining #4355 gap after this slice: enum/Option STRING
  payloads (still classified leak-safe; next sub-slice).
- 2026-07-03: **Landed enum/Option STRING-payload release on the IR path (#4355
  slice 2, all three backends — irlower-only).** A FRESH string payload (a
  literal or a fresh producer, gated by variant_struct_payloads_fresh on enums
  and rcpayload_option_cand on Option/Result) is now rc-droppable:
  enum_field_rc_droppable admits `string`, the consumed-by-match free's
  per-variant dispatch releases it via the rc-aware __fern_str_free
  (emit_enum_variant_drops' string arm), and Option/Result payloads ride the new
  emit_opt_str_payload_drop (op_opt_payload → __fern_str_free → box dec; a
  shallow rc-dec would free the 24-byte string block by the ARRAY layout's size
  class on asm — wrong class). OptRcFrees carries a per-entry `strs` flag and
  the precise-drop pass records an "opt-strpayload" kind (currently dormant for
  the nested-if shape — the same gates that keep the array-payload nested-if
  case leak-mode apply; parity, not a regression). Non-fresh payloads (bare
  idents aliasing live locals) and escaping arm bindings stay sound leaks —
  match_arm_binds_rc_payload now gates string bindings borrow-only
  automatically via the widened droppable predicate. No backend edits: the
  emitted call is the existing __fern_str_free helper on every backend.
  VERIFIED: TestSelfHostEnumStrPayloadReclaimIRX86_64 (+wasm/arm64 siblings) —
  bounded heap high-water flat across a second churn for enum Word(concat),
  Option Some(concat), Result Err(concat); aliased-payload and
  escaping-binding exclusions stay balanced (detector 0). The
  `string-payload-enum-freed-detector` case in the precise-drop suite flips
  from leak-documenting to firing. Remaining #4355 surface: the general
  "any string anywhere" endpoint (payloads via `?`, string fields of enum
  payload structs, nested shapes) — tracked on the issue.
- 2026-07-03: **Landed closure-env capture RC on the IR path (#4354, port slice
  5 — irlower-only, all three backends).** The single-classifier model from the
  AST path, ported to IR emission: `closure_capture_kind` classifies each
  capture ('s' string, 'a' scalar array, '.' everything else), and the SAME
  kinds string drives the build-site retain and the exit-sweep release, so incs
  and drops land together (the issue's invariant). lower_func approves closure
  locals via `clo_rc_candidate_names` (single-bind literal-lambda init, never
  reassigned, not shadowed, body_unsafe_for-clean) seeded as "OK:<name>" into
  the new `LowerState.clo_cap_kinds` registry; the StmtVar lowering consumes
  the approval — after storing the env box it reads each classifiable capture
  back (`env[ci+1]`, the closure-call path's own arr_get) and `__fern_rc_inc`s
  it, registering "<name>|<kinds>". The exit sweep's array loop releases before
  the box dec: an rc==1-gated walk (`__fern_rc_is_unique` + if) frees each
  's' capture via __fern_str_free and each 'a' via __fern_rc_dec. The #4354
  interlock: a fresh string captured into an APPROVED closure is no longer
  escape-flagged by the STR: gate (`body_unsafe_for_clo`) — build inc (rc 2) +
  env release (→1) + string sweep (→0) free it exactly once, with ordering
  (env release in the array loop, string sweep after) making any classifier
  mismatch a sound leak instead of a UAF. Unclassified capture kinds (string[]
  / struct / enum / map / nested closure) and escaping/aliased/reassigned
  closures keep today's leak. No backend edits (existing rc helpers only).
  VERIFIED: TestSelfHostClosureEnvRcIRX86_64 (+wasm/arm64 siblings) — string
  and scalar-array capture churns flat + underflow 0, capture-used-after and
  param-capture balance. Known pre-existing (NOT this slice): a bare-ident
  closure alias (`var d = c; d()`) segfaults on the IR path — d is never
  marked a closure local, so `d()` calls the raw env box; unchanged by this
  slice and excluded from the RC gates (an aliased env's release is rc==1
  gated). Remaining for full slice-5 parity: struct/enum/map/string[]/nested
  capture kinds, closure ARRAYS (`__drop_arr_closure` equivalent), and the
  escaping-closure drop thunk (native `__closure_drop_<name>` dispatch).
- 2026-07-05: **Landed dyn-Trait STRUCT-payload reclaim on the IR path (#4351
  v1 — irlower-only, all three backends).** A `var d: dyn T = C { ... }` local
  holds the concrete's rc-headered struct box (structs flow UNBOXED behind the
  dyn coercion), but the coarse "dyn T" slot type kept it out of every reclaim
  class — every such box leaked. Now `collect_dyn_struct_names` credits
  "DYN:<name>|<Concrete>" in reclaimable_names (fresh leak-safe struct-LITERAL
  init, scalar-only or deep-drop-ok, single-bind, never reassigned,
  body_unsafe_for-clean — a `d.show()` dispatch is a receiver borrow), and the
  exit sweep's new DYN loop releases it exactly like a reclaimable struct:
  `__struct_drop_<Concrete>` (auto-materialized per-type by every backend;
  emitted only when the concrete carries a reclaimable field) then the box dec.
  Reclaimable dyn slots join the entry zero-init set, and
  slot_is_reclaimable_struct now REJECTS "dyn "-tagged slots — pre-fix both
  loops fired and over-released the box (caught by the underflow probe during
  development). Deliberately NOT covered (documented on the issue): PRIMITIVE
  and STRING dyn payloads — op_dyn_box allocates a HEADERLESS 16-byte
  `__fern_alloc` cell the asm no-op `__free` cannot reclaim (needs an
  allocation-layout slice: rc-header the cell, then a `'s'`-kind release);
  `dyn T[]` element boxes (needs the genArrDynDropFn analog); enum payloads
  behind dyn. VERIFIED: TestSelfHostDynStructReclaimIRX86_64 (+wasm/arm64
  siblings) — churn flat + underflow 0 for array-field and scalar-only
  concretes; escaping (`return d`) and reassigned dyn locals excluded and
  balanced. Native reference: buildDynDropHelpers routes through a vtable drop
  slot because it erases the type; the self-host binding-site registry skips
  the vtable entirely (the concrete is statically known), mirroring the #4552
  closure-capture design.
- 2026-07-05: **Landed TRMC (tail recursion modulo cons) on the self-host IR
  path (#4352 v1 — irlower-only, all three backends).** The `map`-shaped
  recursion `Cons(g(h), self(t))` is not a tail call (the constructor wraps
  it), so pre-port the self-host IR path grew O(n) stack and a ~300k-element
  list SIGSEGV'd. `trmc_eligible` (conservative v1 detector) accepts a free,
  non-generic function whose whole body is one `match` on an enum param
  returning that enum, where every arm is a guard-free `PatVariant` with a
  single `return`: recursive arms return a constructor of the return enum
  whose LAST argument is the sole self-call (full argc, self-free heads),
  base arms are self-free, and — the key v1 restriction — every recursive arm
  uses the SAME constructor variant, which makes the tail-field index a
  compile-time constant so the hole write is a plain `op_struct_set`
  (portable: wasm has no raw pointer stores). `emit_trmc` rewrites the body
  into the classic hole-passing loop: an outer exit block + `op_loop`; per
  arm a variant test (`variant_is` / brif past); recursive arms evaluate the
  heads to temps, build the node via a normal `op_struct_make` with a dummy 0
  tail (automatic layout + rc parity with ordinary construction), link it
  into the previous hole (or seed the result), advance the hole, store the
  self-call args over the param slots, and `op_br` the loop; the base arm
  lowers its return value normally and links it as the final tail. TRMC'd
  bodies bypass the RC sweeps and the self-tail-call TCO pair (no swept
  locals, no self-tail shape left). Deliberately deferred (documented on the
  issue): consume-safety (native slice 2 shares the reuse-token machinery
  the #4350 work is building), mixed-variant recursive arms, guards, and
  nested/multi-statement arm bodies. VERIFIED: TestSelfHostTrmcIRX86_64
  (value 1275; 300k-deep list exits 0 where the pre-port driver — rebuilt
  from origin/main — SIGSEGVs; string-head SList; tree-shaped two-self-call
  negative NOT rewritten), TestSelfHostTrmcWasmIR (value + 200k deep),
  TestSelfHostTrmcIRArm64 (200k deep under qemu via `asm_ir_run -target
  arm64`). Native reference: `detectTrmc` / `emitTrmc` (internal/trmc.go) —
  the self-host detector was stricter (same-ctor restriction, lifted 2026-08-14
  below) but the emitted loop shape matches.
- 2026-07-05: **Landed defer/errdefer on the `?` failure path (#4334 part 1 —
  parse-pass markers + irlower replay, all three backends).** The self-host
  lowers defers at PARSE level by rewriting StmtReturn (parser.fern
  `lower_defers_func`), so the try operator's implicit early return skipped
  every registered defer — and errdefer never fired on the exact path it
  exists for (native runs emitDeferCleanup + emitErrDeferCleanup at the TryOp
  edge). Fix: the pass embeds its two cleanup lists behind never-true
  `__dfa_tryall` / `__dfa_tryerr` guards at the body tail (dead code on any
  backend that doesn't know them), lower_func extracts the marker blocks into
  the new `LowerState.try_cleanup`, and lower_try replays the guarded
  statements at the `?` failure edge — plain defers first, then errdefers,
  then the existing #4422 dec-sweep, matching both native's TryOp order and
  the pass's explicit-`return Err` expansion. The per-defer `__dfa<i>` guards
  make registration dynamic (a defer after the `?` has flag 0 and stays
  silent). LowerState is pinned at 33 fields (the #4554 legacy-AST 34-field
  miscompile), so the slot came from folding `ret_is_i64arr` + `ret_is_dyn`
  into one `ret_arrdyn` 2-bit flag word (accessors `ret_i64arr()` /
  `ret_dyn()`). VERIFIED: TestSelfHostTryDeferIRX86_64 (+wasm/arm64 siblings)
  — fail-path firing (defer then errdefer then the caller's Err), success
  path (defer at return only), LIFO at the `?` edge, conditional/late
  registration, and the #4422-shaped isolated probe showing the owned-local
  sweep survives the replay; every case cross-checked against native stdout +
  exit. Known remaining (documented on the issue): the legacy self-host AST
  emitters still skip defers at `?` (markers are dead code there) — per the
  legacy-gap policy, not blocking.
- 2026-07-05: **Fixed the bare-ident closure alias segfault (#4557 — irlower
  StmtVar, all three backends).** `var d = c` where c is a closure local left
  d a plain scalar slot, so `d()` called the raw env-box pointer as a
  call-table index — SIGSEGV on the IR path. clo_init's detection now has a
  bare-ident arm gated on is_closure_local: the alias is marked a closure
  local (env-first dispatch) and its env box is __fern_rc_inc'd after the
  store, so the exit sweep's two shallow decs balance and the rc==1 gate
  hands the capture release to the LAST owner (the alias name carries no
  capture kinds, so captures keep the documented aliased-env leak). Covers
  fn-typed-param aliases (`var g = f`) and chains; a REASSIGN alias
  (`f = d`) dispatches correctly but takes no alias-inc (the rebind-drop
  decs the old box) — same leak-mode class as before, noted not fixed.
  VERIFIED: TestSelfHostClosureAliasIRX86_64 (+wasm/arm64 siblings) — the
  filed repro (139 → 4), both-names array-capture, param alias, chained +
  branch alias, native cross-checked + pathprobe "ir"-pinned. Found and
  PINNED pre-existing (NOT this fix, reproduced on unmodified main): ANY
  closure called through the hoisted/escaping path over-decs its extracted
  captures per call (2 underflow ticks for a 2-call shape with or without
  an alias) — the #4354 escaping-closure drop-thunk slice remains the
  tracked fix for that.
- 2026-07-05: **Fixed the hoisted-closure capture over-dec (#4354 borrow
  slice — irlower, all three backends).** make_clo_func synthesizes capture
  reads as `var <cap> = __env[1+i]` at the top of a `$clo`/`$wrap` body, so
  the lambda's exit dec-sweep treated them as OWNED array locals and
  shallow-dec'd them on EVERY call — but the env box owns those references:
  an rc==1 capture was freed out from under the box's owner on the first
  call (per-call UAF; 2 underflow ticks on the 2-call escaping shape,
  independent of the #4557 alias fix). lower_func now registers env-extract
  names as "ENVCAP:<name>" in reclaimable_names (gated on the synthesized
  __env first param) and the sweep's array loop skips them — a borrow is
  not released by the borrower; the env box keeps its caller-side release.
  VERIFIED: TestSelfHostClosureCaptureBorrowIRX86_64 (+arm64 sibling) —
  escaping 2-call shape and the alias shape at detector ZERO (previously
  tolerated), 6000-iter churn flat + zero. The #4613 alias suite's RC note
  updated accordingly. Remaining #4354 surface unchanged otherwise
  (struct/enum/map/string[] capture kinds, closure arrays, drop thunk).
- 2026-07-10: **Landed `?`-consumed source-box reclaim + `?`-bound string
  ownership (#4355 slice — NATIVE internal/ir AND self-host irlower, all three
  backends).** `mk(pre)?` evaluates the callee's Option/Result box into a
  scratch slot, reads the success payload, and the box is dead — but neither
  compiler ever dec'd it, so a per-iteration `?` leaked one box per evaluation
  on every backend (the try-operator sibling of the match-scrutinee reclaim;
  probes: fresh-call `?` grew the bump unbounded on native x86/arm64/wasm and
  on the self-host IR path alike). NATIVE: reclaimableTryScrutinee gates on
  ownedCallResultType (fresh user-call result, is_unique-protected against
  aliased returns via the return-transfer inc) + EnumRcPayloads-eligible +
  scalar-or-string success payload; emitTryBoxFreeVariant frees SHALLOW with
  the statically-proven variant's exact size (variant 0 on the success edge,
  variant 1 on a pair-form enclosing Result failure edge where the (tag,
  payload) pair is copied out for OpReturnPair); tryPairReboxSize catches the
  PAIR-FORM inner shape whose emitRepackPairAsHeapBox rebox (a fresh rc=1 box
  per evaluation) leaked at both edges. A STRING success payload's reference
  MOVES to the binding: rhsTainted's new TryOp case (mirroring the same gate,
  so analysis and lowering can never disagree) credits `var s: string =
  mk(pre)?` as owned and the exit sweep balances it — construction-side
  alias-incs under EnumRcPayloads keep an `Ok(pre)`-style aliased payload
  safe (rc>=2). NOT covered natively: wasm32 pair-form string payloads (the
  pair-form ctor return carries NO alias-inc, so ownership transfer would be
  unsound — the documented #4355 construction-side-discipline follow-up) and
  non-string pointer payloads (struct/array/tuple/enum keep the box+payload
  leak). SELF-HOST (no return-transfer inc → freshness proven statically):
  opt_fresh_ret_fns_of admits FREE functions whose every return is a direct
  Ok/Err/Some/None ctor (fresh rc-headered op_opt_make / op_opt_none boxes);
  lower_func seeds them as "OPTFRESH:<name>" into reclaimable_names (33-field
  LowerState pin — prefix-tagged entries, the established convention);
  lower_try frees the $try box via the established __fern_rc_dec box free at
  the success edge and the Option failure edge (the Result failure edge
  forwards the box). String ownership: producers whose success payloads are
  all static literals / syntactically-fresh strings carry flag "f", and
  collect_try_str_binding_names credits the `var s: string = mk(..)?` binding
  "STR:" (body_unsafe_for escape gate + not-reassigned, exactly like the
  frets path); a bare-ident payload (`Ok(pre)`) flags "a" — box-free only,
  the aliased payload keeps its sound leak (op_opt_make stores payloads
  uncounted on this path). A local slot shadowing the callee name skips the
  free. VERIFIED — native: TestX86_64/Arm64/WASMTryScrutineeReclaim (bounded
  high-water for scalar / Option-with-failures / string (+wasm literal-payload
  sibling), aliased-box (`id2(mk())?`) and aliased-payload (`Ok(pre)`)
  safety at detector zero); self-host: TestSelfHostTryBoxReclaimIRX86_64
  (+wasm/arm64 siblings) — the outer call-result box consumed by the caller's
  match still leaks (a PRE-EXISTING class, candidates are direct-ctor inits
  only), so each churn case compares a hand-desugared baseline against the
  `?` version in one program (baseline first, so freelist reuse can't flatter
  it) and asserts the try churn grows at most HALF the baseline; plus
  aliased-payload / non-ctor-callee (`pass(b)?` forwards a live box — never
  freed) / escaping-binding exclusions at detector zero.
- 2026-07-10: **Landed replaced-STRING-field reclaim in `__field_reclaim_<T>`
  (#4355 slice — self-host irlower + all three backend helper bodies).** The
  per-type consume-rebind helper freed replaced ARRAY fields only, so a struct
  threaded through `s = step(s)` rebinds leaked the superseded box's string
  field per rebind (native flat on the same shape). The three bodies (x86
  emit_ir_field_reclaim_one, arm64 emit_arm64_field_reclaim_one, wasm
  emit_wasm_field_reclaim_body) now release a replaced string field under the
  SAME cow + snap guards as arrays, via the rc-aware __fern_str_free (on wasm
  $__fern_arr_dec IS the string free — one inline rc-headered block). Balance:
  the construction-side retains already ON for every routed type (the
  slit_reclaim-gated #4297 A2 override retain + the base-copy retain), fresh
  fields sole-owned, carried fields cow-skipped. Two supporting changes: (1) a
  read-side retain for `var t = s.name` (a string field read of a
  reclaimable / snapshot struct local whose type routes through
  __field_reclaim) alias-incs the rc-headered box so the rebind's field free
  can't dangle the alias — arrays already had this via the is_arr alias-inc;
  (2) `i32_to_string` is now Owned in str_producer_ownership (the issue's
  exclusion-note resolution — the historical exclusion guarded the NATIVE
  emitter's mid-buffer boxing; irlower-emitted code always calls the
  alloc-boundary __fern_i32_to_string), so a `name: i32_to_string(n)` field is
  handed over uncounted and the reclaim frees it instead of leaking one count
  per rebind. THE LOAD-BEARING ADMISSION (learned from a per-module compiler
  self-run segfault + bisect): the string arm is gated per type on a
  WHOLE-PROGRAM read scan (strfld_reclaim_ok_types_of — a single-pass,
  field-name-keyed collector) that rejects any type whose string-field names
  are read in an ESCAPING position anywhere: a call arg (the compiler passes
  its LowerState/EmitState fields to helpers everywhere — those types are
  exactly the ones excluded), a return, a slice/trim view, a reassign-alias.
  Safe transient borrows (binary operands, byte-index bases, read-only /
  fresh-copy method receivers, retained `var t = x.f` inits) don't exclude.
  Backends receive the verdict as "strfldok:<T>" needs seeded at the
  whole-program emit orchestrations (all_funcs on the per-module unit paths —
  a unit-local list would under-count reads and re-open the UAF); excluded
  types keep the arrays-only body. ALSO: the arm64 __fern_str_free clobbers
  x10/x11/x12 (unlike arr_dec), so the arm64 field-reclaim body reloads
  new/old/snap from the stack args after each string free. VERIFIED:
  TestSelfHostFieldReclaimStrIRX86_64 (+wasm/arm64 siblings) — consume-rebind
  churn flat, carried-field (functional-update) safety, aliased-read safety,
  snapshot-param safety (caller's original survives the callee's threaded
  rebinds), the i32_to_string-producer churn, and the escaping-read exclusion
  (`use(s.name)` call arg → arrays-only body, reads stay valid), all at
  detector zero, plus the per-module whole-compiler self-run. Remaining
  nearby (documented, unchanged): string-ONLY structs (no rc-array field)
  keep their leak — the construction retain and the reclaim routing are both
  gated on struct_has_reclaim_array_field, and widening them is the
  deliberate slot_is_reclaimable_struct-broadening follow-up; lifting the
  escaping-read exclusions needs the read-side alias-inc discipline (the
  #4355 construction/read-side counting follow-up).
- 2026-07-10: **Landed string-ONLY struct reclaim (#4355 slice 3 — the
  slot_is_reclaimable_struct broadening, self-host irlower + all three
  backends; PR #4769).** A struct with string fields but NO rc-array field
  (e.g. `struct B { name: string, n: i32 }`) was excluded from consume-rebind
  reclaim entirely — both the construction-side retain and the reclaim
  routing gated on struct_has_reclaim_array_field — so a `b = step(b)` churn
  leaked the whole box chain (box + string field per rebind). The routing
  predicate is now `(s: LowerState) struct_routes_field_reclaim(sty)`:
  has-rc-array-field OR (has-string-field AND "STRFLDOK:<sty>" admitted),
  applied at every site that previously asked struct_has_reclaim_array_field
  — the snapshot-param routing, the StmtAssign reclaimable-local site, the
  StmtVar loop-rebind site, the slit_reclaim construction retain, and the
  read-side `var t = b.name` retain arm — so retain and free widen in
  LOCKSTEP (an admitted type gets both; anything else gets neither).
  Admission reuses the slice-2 whole-program read scan
  (strfld_reclaim_ok_types_of), with the array-field candidacy requirement
  dropped: any struct_has_string_field type is a candidate, admitted iff
  none of its string-field names is read in an escaping position anywhere.
  PLUMBING (LowerState is pinned at 33 fields — the legacy AST backend
  miscompiles 34): the verdict list rides into lower_func as a new final
  param `strfld_ok_types`, seeded into reclaimable_names as prefix-tagged
  "STRFLDOK:<T>" entries next to the OPTFRESH block; the three emit
  orchestrations (asm_ir, asm_arm64 via asm_arm64_ir, wasm_ir) precompute it
  next to sfrf/ofrf and thread it through — the per-function eligibility
  prepass passes `[]` (routing verdicts don't affect eligibility).
  SOUNDNESS of the lowering-time (module-local funcs) vs emit-time
  (all_funcs) verdict mismatch: emit's all_funcs scan is a superset reader —
  a type routed at lowering but rejected at emit gets a __field_reclaim body
  with no string arm (sound leak, arrays-only behavior); the reverse
  direction can't add frees the retains didn't fund because construction
  retains key off the same lowering-time verdict. VERIFIED:
  field-reclaim-str-only-flat + -aliased-carried-safe (x86), -flat-wasm,
  -flat-arm64 in the three TestSelfHostFieldReclaimStr* suites — string-only
  churn flat at detector zero, carried/aliased reads survive — plus the
  full CI matrix. Remaining nearby (unchanged): escaping-read exclusions
  (read-side counting), string fields of enum-payload structs, string[][],
  map string K/V deep reclaim (#4353), wasm pair-form `?` string payloads.
- 2026-07-11: **Landed the NATIVE half of #4355 slice 4 — exprNoParamEscape
  string-freshness cases (enum-payload-struct string fields; PR #4771).**
  On the natives, `var e: E = mk(i); match (e)` with
  `enum E { A(S), B }` / `struct S { name: string, n: i32 }` leaked the
  WHOLE chain (enum box + payload struct box + string) per iteration while
  the scalar-/array-field sibling reclaimed fine. NOT a drop-machinery gap —
  an ANALYSIS one: exprNoParamEscape had no case for string literals or
  concats, so any constructor embedding a string field lost its
  returnsNoParamEscape verdict; rhsTainted's call case then fell past its
  escape-free short-circuit to the generic any-arg-tainted rule, a
  noise-tainted scalar arg poisoned the call result, and the local was never
  swept (probe methodology: identical programs with scalar vs array vs
  string struct fields — drops emitted for scalar/array, ZERO drop calls in
  the whole binary for string). Fix: three provenance-free-fresh cases —
  literal (static sentinel), concat (BYTE-COPIES operands into a fresh
  buffer, so param operands don't escape through it; rhsTainted's
  IsStringConcat case already encoded this rule), string slice (fresh copy,
  not a view). A param-EMBEDDING ctor (`S { name: nm }`) still fails the
  verdict (pinned). isOwnedByDefaultType is untouched — string-bearing enum
  PARAMS stay borrowed; blast radius is locals bound to fresh call results,
  riding the proven freeEligible → emitVarReinitDropOld → __drop_enum_<E> →
  __drop_struct_<S> route. Wasm32: boxes reclaim, literal-field shape fully
  bounded; the two-word concat string field keeps a documented sound leak
  (pinned detector-zero). Also documented-but-unfixed: the pair-form
  DIRECT-CALL match scrutinee with struct payload (`match (mk(i)) {...}`)
  leaks on ALL backends (reclaimableMatchScrutinee rejects pointer
  bindings) — a separate slice. **SELF-HOST STATUS (the port half, still
  open): the self-host IR path reclaims NEITHER shape — even the
  scalar-struct-payload enum local leaks (no enum-local consume-rebind
  reclaim exists there at all). The port is the next slice: a per-type
  __enum_drop_<E> helper on all three backends (tag dispatch → payload
  __struct_drop_<S>/str_free → exact-size box free), StmtVar loop-rebind /
  StmtAssign routing for enum slots (the enum sibling of
  struct_routes_field_reclaim), an OPTFRESH-style whole-program freshness
  scan for ctor functions, and construction-side payload retain balance.**
  Native-only surface referenced against the #4451 convergence tracker.
- 2026-07-11: **Landed the SELF-HOST half of #4355 slice 5 — RCENUM call-init
  admission + the __struct_drop_<T> return-clobber fix.** Two coupled changes:
  (1) ADMISSION: the RCENUM enum-local reclaim (loop-rebind / consume
  deep-drop of a fresh, match-consumed enum local via
  emit_enum_deep_reinit_store → emit_enum_variant_drops) only fired for
  DIRECT variant-ctor inits (`var b = Full([..])`); a factored ctor
  (`var e: E = mk(i)`) was never credited, so the whole chain (enum box +
  payload struct box + string/array fields) leaked per iteration.
  opt_fresh_ret_fns_of(funcs, structs) now ALSO emits "RCE:<name>|<Enum>"
  entries — prefix-tagged in the SAME list so the verdict rides the existing
  lower_func opt_fresh_ret threading (no new params; LowerState stays at 33
  fields) — for free functions whose declared return is an rc-droppable enum
  and whose EVERY return is a fresh direct variant construction
  (fresh_rcpayload_enum_init — the OPTFRESH static-freshness rule applied to
  user enums; this path has no return-transfer inc), with the extra gate
  rcenum_ctor_payload_strings_fresh: StructLit payloads' string-typed fields
  must be fresh (literal / str_local_binding_is_fresh) — a param-embedding
  `S { name: nm }` would hand the caller a chain whose k_str deep-drop frees
  a string the CALLER still owns (the per-slot rule native's
  exprNoParamEscape enforces, pinned by the param-embed exclusion test).
  collect_fresh_rcenum_names then admits a call init via
  rcenum_call_init_owner; all other RCENUM gates unchanged.
  (2) THE BUG THE WIDENING EXPOSED (pre-existing, all enum-with-struct-payload
  consumes): __struct_drop_<T>'s documented contract is "returns the box",
  but the x86 body set %rax at ENTRY and the field-release calls
  (__fern_arr_dec / __fern_str_free / nested __struct_drop_*) clobbered it —
  it returned the LAST-FREED FIELD pointer; the arm64 body restored from x10,
  which __fern_str_free and nested struct-drop bls clobber (the slice-2
  lesson). emit_enum_variant_drops CONSUMES that return for its payload-box
  free (irlower ~17264), so every consume dec'd the last-freed field AGAIN —
  one rc-underflow tick per consume, LATENT in the shipped
  TestSelfHostEnumStructPayloadDrop* suite because it asserts boundedness
  only (the detector absorbs the spurious dec) — while the payload box
  LEAKED; a STRING field segfaulted (__fern_str_free freeing the twice-freed
  block by the wrong size class). gdb pinned it: struct_drop_S(box) →
  arr_dec(field rc=1) → arr_dec(field rc=0 ← tick) → arr_dec(enum box).
  FIX: both bodies reload the box from the stable stack arg before ret (x86
  `movq 8(%rsp), %rax` at .Lstd_<T>_ret; arm64 `ldr x0, [sp, #16]` under the
  stp frame at .Lasd_<T>_ret); the wasm body was already correct
  ((local.get $box) tail). The one known workaround site (the discarded-temp
  dyn-struct reclaim, which stashes the box in a scratch and discards the
  return) is unaffected. VERIFIED: TestSelfHostRcEnumCallInit{IRX86_64,
  IRArm64,WasmIR} — call-init string-field + array-field payload churn flat
  at detector zero, param-embed exclusion (caller's string survives),
  direct-init struct-payload consume at DETECTOR ZERO (the regression pin the
  shipped boundedness test misses), and the string-field direct-init shape
  that segfaulted. Remaining nearby: scalar-only payload structs
  (nested_field_deep_drop_ok requires a reclaimable leaf, so `S { m: i32,
  n: i32 }` payloads keep their sound leak); bare-local match reclaim beyond
  the same-block single-match shape; the native direct-call pair-form match
  scrutinee (slice-4 note).
- 2026-07-11: **Landed #4355 slice 6 — literal string-arg box reclaim at
  borrowable call positions (self-host IR path).** A string-LITERAL call arg
  allocates a fresh 16-byte rc-headered box per evaluation (const_str; the
  .rodata data itself is heap-guard-skipped) and nothing freed it — one box
  leaked per call in any loop passing literal string args (`readit("ab")` in
  a loop; surfaced by the slice-5 churn tests, where `mk("a", j)` tripped the
  bump assertion). Wasm never leaked (literals are data-section). FIX, at the
  IR layer so every backend shares it: in the generic direct-call arg loop,
  a literal arg at a BORROWABLE param position of a known free function
  (borrowable_params_of — provably borrow-read-only and never escaping, so
  the callee can neither retain nor return the arg) is stashed into a
  scratch slot (store+reload keeps the arg in place) and freed right after
  the call via the rc-aware __fern_str_free — emitted NET-ZERO on the
  operand stack (load → free → drop) UNDER the live call result, so no
  width-sensitive result parking is needed (the call-arg sibling of the
  #2649 concat-operand anonymous-temp reclaim, which parks because concat's
  result is always a string). The borrowable registry rides into the expr
  lowering as prefix-tagged "BORROW:<name>|<flags>" reclaimable_names
  entries seeded by lower_func (LowerState pinned at 33 fields — the
  OPTFRESH/STRFLDOK convention). Non-borrowable positions — returned,
  stored, forwarded-to-a-call args — keep the sound leak (pinned:
  `keepit("xy")` returning its param stays readable at detector zero).
  VERIFIED: TestSelfHostLiteralArgReclaim{IRX86_64,IRArm64,WasmIR} —
  borrowable churn flat at detector zero, retained-value safety, mixed
  literal+live args (live survives, churn flat) — plus the per-module
  whole-compiler self-run. Remaining nearby: non-literal fresh temp args
  (concat results / producer calls passed directly as args) at borrowable
  positions — the same stash-free pattern applies but needs the
  is_fresh_str_temp classifier and care with evaluation order.
- 2026-07-11: **Landed #4355 slice 7 — fresh string temp args at borrowable
  call positions.** The slice-6 follow-up: the literal-arg box reclaim now
  also admits NON-literal fresh anonymous string temps passed directly as
  call args — a concat `f(a + "x")`, a named producer `f(chr(n))`, a copying
  string method `f(s.to_upper())` — via the existing #2649 is_fresh_str_temp
  classifier (type-gated by expr_is_str; the aliasing shapes — bare idents,
  field/index reads, `.trim()` views, receiver-identity fast-paths — stay
  excluded, pinned by the bare-ident-arg trap test). Same stash +
  post-call rc-aware __fern_str_free, net-zero under the live result; the
  seeding collector's syntactic match widened in lockstep (over-collection
  only seeds a harmless extra BORROW entry — the lowering gate carries the
  type check), and the slice-6 arena disciplines (targeted seeding,
  allocation-free lookup) are unchanged. VERIFIED: fresh-concat-arg +
  fresh-producer-arg churn flat at detector zero with the operand/receiver
  surviving, bare-ident-arg safety, all slice-5/6 regressions, and the
  per-module whole-compiler self-run.
- 2026-07-11: **Landed #4355 slice 8 — scalar-only struct payloads in the
  RCENUM reclaim.** `S { m: i32, n: i32 }` as an enum payload failed
  nested_field_deep_drop_ok (no reclaimable leaf), so enums carrying one
  were never RCENUM-admitted and the enum box + payload box leaked per
  consume-rebind (native reclaims the identical shape). Three coupled
  widenings, all fresh-literal-gated like the deep-drop-ok arm:
  enum_field_rc_droppable admits an all-scalar struct payload
  (struct_lit_all_scalar); variant_struct_payloads_fresh requires it to be
  a fresh struct LITERAL (a bare-ident payload aliases a local whose own
  sweep would double-free the box — pinned); and
  emit_enum_variant_drops_moved releases it with a single __fern_rc_dec
  (one flat box, no inner rc fields — the struct sibling of the leak-safe
  array arm). The slice-5 RCE call-init scan picks the widened predicates
  up automatically, so `var e = mk(i)` factored ctors qualify too.
  VERIFIED: scalar-struct-payload churn flat at detector zero (direct +
  call-init), aliased bare-ident payload exclusion (s0 survives), slice
  5/6/7 regressions, per-module whole-compiler self-run.
- 2026-07-11: **Landed #4355 slice 9 — arr-of-arr local reclaim (i32[][] /
  string[][]), whole-structure.** An arr-of-arr local (`var g = [[..],[..]]`)
  had NO reclaim at all on the self-host IR path: the init marks is_arrarr
  but the slot is not is_arr, so the exit sweep's array loop never touched it
  and no rebind dec fired — outer buffer + inner buffers + string elements
  all leaked per iteration (native flat on the same shapes). ALSO FOUND: the
  self-host __fn___fern_drop_arr_ptr is a ret-only STUB — no element-walk
  helper existed. NEW RUNTIME HELPERS on all three backends, modeled on
  __fn___fern_str_arr_free's shell (null / low-addr / immortal guards, rc>1
  dec, rc==0 underflow tick, rc==1 walk + freelist buffer free):
  __fern_arrarr_free (per-element rc-guarded __fern_arr_dec — a
  scalar-element row is fully freed by one dec; wasm routes to the existing
  $__fern_arr_dec_ptr) and __fern_strarrarr_free (per-element
  __fern_str_arr_free; wasm gets the new two-level $__fern_arr_dec_ptr2 —
  arr_dec_ptr whose per-element call is arr_dec_ptr itself). ADMISSION,
  two-tier: rows must be array LITERALS (credit "ARRARR:", lax — scalar
  inner elements VALUE-COPY, so ident/binary elements like [j, j+1] are
  safe); a STRING-kind slot (type-aware arrarr_elem) additionally requires
  every inner element to be a fresh string ("ARRARRS:", strict — a live
  local stored as an element would dangle; pinned by the ident-element
  exclusion test). Gates: arrarr_unsafe_for and
  arrarr_row_escapes (a bare `var row = g[i]` single-index read or
  `for row in g` binds an inner pointer → rejected; transient g[i][j] /
  g[i].len() borrows admissible — pinned by the row-alias test).
  APPEND-BUILT (#6092): the admission above once also required the name to
  be NOT reassigned, which excluded `var g: T[][] = []` grown by
  `g = g.append(<row literal>)` — the shape leaked one row buffer per
  append, unbounded in a loop (200 rounds x 3 rows = 33600 bytes, against
  native's 0). arrarr_unsafe_for replaces that blunt exclusion with the
  per-rebind validation the string[] class has had since #4355: only the
  self-append of a fresh ROW LITERAL passes, everything else is still
  body_unsafe_for. arrarr_lit_is_empty + arrarr_name_is_appended admit the
  `[]` initializer (arrarr_lit_is_fresh rejects it — no rows to prove),
  and the strict string credit additionally needs EVERY appended row
  strings-fresh (arrarr_appends_strings_fresh). CALL-SOURCED rows
  (`g = g.append(make_row())`, #6102) are credited off a new "ARC:" registry
  — the single-level sibling of AAC, built in the same opt_fresh_ret pass.
  A free fn returning one level of `[]` qualifies when EVERY return is a
  fresh row: a direct array literal, or an ident naming a local declared in
  that body with an array-literal init (`[]` included), every rebind a
  self-append, and no escape but the return itself
  (body_unsafe_for_allow_ret with an EMPTY borrowable — at module-registry
  time there is no call context, so any call-arg use disqualifies).
  Two tiers like AAC: "s" when every element ever placed in the row is a
  fresh string (what a string-kind consumer needs before it frees element
  POINTERS), else "p" (buffer-fresh only — enough for a scalar-kind
  consumer, whose row elements value-copy). A producer returning a PARAM or
  an alias of another local registers under neither, so the caller's row
  can never be freed out from under it. WIRING:
  emit_arrarr_reclaim_store (the emit_str_reclaim_store sibling,
  cow-guarded) at the StmtVar loop-rebind; a new exit-sweep loop frees the
  final value via the kind-picked helper. VERIFIED:
  TestSelfHostArrArrReclaim{IRX86_64,WasmIR,IRArm64} — string[][] +
  i32[][] churn flat at detector zero, row-alias + ident-element
  exclusions, slice 5-8 probe regressions, per-module whole-compiler
  self-run. Remaining nearby: struct[]-of-arrays and deeper nesting
  (string[][][]) keep the sound leak; arr-of-arr via call results
  (`var g = mk()`) needs an OPTFRESH-style fn scan.

- 2026-07-11: **Slice-9 CI follow-up — two real fixes, one big diagnosis.**
  (1) HELPER BODIES NEED-GATED on all three backends. The first cut emitted
  __fn___fern_arrarr_free / __fn___fern_strarrarr_free unconditionally in
  asm_ir's emit_ir_runtime (+ the asm_arm64 heap block, + wasm's
  heap_alloc_helpers), so EVERY binary carried the strarrarr body's inner
  `call __fn___fern_str_arr_free` / `bl …` / `(call $__fern_arr_dec_ptr)`
  text — tripping the shipped negative asm-grep contracts
  (strarr-elem-alias-excluded: "the element-hazard walk failed to exclude
  an aliased element") on programs with no arr-of-arr reclaim at all.
  Now: x86 seeds need("arrarr_free")/need("strarrarr_free") at the
  helper-call emit (the chr pattern) and the bodies sit behind has_need;
  arm64 mirrors it (seeded in asm_arm64_ir, gated in asm_arm64's runtime
  writer); wasm moves $__fern_arr_dec_ptr2 out of heap_alloc_helpers into
  arr_dec_ptr2_func gated on the new module_calls_strarrarr_free scan.
  Both new roots are in all_runtime_need_roots so per-module entry units
  still cover library callers. (2) X86 BUMP ARENA 3.875 GiB → 8 GiB
  (asm.fern heap_size / asm_ir mmap + heap_bump_bytes / native x86-64
  heapBytes, in lockstep; mmap length now a movabs 64-bit imm — the old
  movl u32 form was the sub-4 GiB ceiling). DIAGNOSIS worth remembering:
  the "shard8 OOM / runner preemption" CI failures on the slice-9 PR were
  NEITHER. LoadFixpoint stage-2 + Stage2FixedPoint mmc2 exited 137 because
  __fern_alloc's bounds check EXITS 137 BY DESIGN on arena exhaustion —
  it reads exactly like a SIGKILL/OOM-kill but is the Fern runtime's own
  trap. Measured: the merge-base self-host-built gen1 self-compile peaked
  at 3944 MB of the 3968 MB arena (99.4% full — every RC slice has been
  creeping toward this wall); slice 9's additions tipped it over. The
  native-Go-built stage-1 compiles the same source at ~2.9 GiB; the
  self-host-built binary allocates ~1 GiB more garbage for the same work
  (emit-quality gap). With 8 GiB: gen1 self-compile exits 0 at 3961 MB
  peak, gen1==gen2 byte-identical. The alloc-trap e2e grew 100000 →
  150000 iterations (~10.5 GiB cumulative) so it still overflows the
  bigger arena.

- 2026-07-11: **#4355 slice 10 — arr-of-arr CALL-RESULT reclaim + slice-9
  double-sweep fix.** (1) CALL-INIT ADMISSION: `var g: T[][] = mk(..)` now
  earns the slice-9 credits when mk is provably a fresh-arrarr producer.
  opt_fresh_ret_fns_of registers "AAC:<name>|<flag>" (the RCE: no-new-
  threading trick — same list, distinct tag) for FREE functions whose ret
  type ends "[][]" and whose EVERY return is a fresh arrarr literal
  (arrarr_lit_is_fresh; `return t;` of a local or any non-literal return
  disqualifies — only direct literal returns keep the static soleness
  proof, and the IR path has no return-transfer inc so the caller is sole
  owner at rc=1). flag "s" = every return also arrarr_lit_strings_fresh
  (a param-embedding row like `[[s]]` disqualifies the strict flag —
  exactly the dangling hazard ARRARRS: exists for), else "p" (lax only).
  collect_fresh_arrarr_names (now taking the ofr registry) admits the
  ExprCall init off those entries; all consumer gates (body_unsafe_for /
  not-reassigned / arrarr_row_escapes) unchanged. Native was already flat
  on the same shapes (probe ga5) — this is a self-host-only slice.
  (2) FOUND + FIXED on the way: the slice-9 exit sweep DOUBLE-FREED
  fn-scope candidates. An arrarr slot is ALSO is_arr, and
  emit_dec_sweep_except_list ran a shallow `__fern_rc_dec` (→ arr_dec,
  frees the outer, rc→0) in the is_arr loop and THEN the separate arrarr
  loop's helper on the same slot — which saw rc==0, ticked the underflow
  detector, and skipped its element walk (inner strings LEAKED at
  fn scope; 2200 calls = 2200 detector ticks masking real over-releases).
  Slice-9's loop-scoped tests dodged it because retired slot names miss
  the credit lookup at sweep time. Fix mirrors strarr: the kind-picked
  whole-structure helper now runs INSIDE the is_arr sweep (one slot, one
  free) and the separate loop is gone. Pinned behaviorally by the
  fnscope-sweep-flat cases (flat + detector zero after 2200 fn-scope
  sweeps). Tests: TestSelfHostArrArrCallRetReclaim{IRX86_64 (5 cases),
  WasmIR (3), IRArm64 (3)}. Remaining nearby: struct[]-of-arrays,
  string[][][] deep free, escaping-read exclusions, map string K/V
  (#4353).

- 2026-07-11: **arm64 arena wall — 3.5 GiB → 8 GiB, 32-bit-pointer ceiling
  retired.** TestSelfHostStage2FixedPointArm64/self was failing on main:
  mmc2-arm64 (the self-host-built arm64 compiler, under qemu) exhausted its
  3.5 GiB arena mid-self-compile — __fern_alloc's exit-137 trap, the arm64
  twin of the x86 wall. The test skips on BOTH CI lanes (needs a native x86
  host + arm64 cross tooling together), so the wall was only visible
  locally. Measured: the live set needs ~4.1 GiB (peak RSS 4185 MB under
  qemu), which is past ANY arena a 32-bit-safe pointer range can hold — an
  interim 3.875 GiB cut (hint lowered to 0x04000000, end 0xFC000000) still
  trapped at the wall. So the historical "arm64 heap pointers stay <4 GiB"
  guideline is retired: the arena is now 8 GiB (1<<33, mov+lsl — the
  self-host assembler's literal parser is 32-bit; the native backend's
  ldr =N pool is int64), hint 0x04000000, lockstep across asm_arm64.fern,
  asm_arm64_ir.fern's heap_bump_bytes derive, and the native arm64
  heapBytes. Empirical proof the 64-bit pointer plumbing holds (as
  arm64-darwin's high heap already suggested): the stage-2 fixpoint is
  BYTE-IDENTICAL with pointers >4 GiB exercised end-to-end, the slice-9/10
  arrarr/callret churn suites run flat at detector zero under qemu, and
  the alloc-trap still exits 137 at the new wall (150k iters ≈ 10.5 GiB).
  Ops note: reproducing this locally via the go-test harness kept
  OOM-bouncing the container (cold buildSelfHostBin = Go emit + gcc `as`
  at ~16 GB); the low-memory path is /tmp/fern's in-process assembler for
  the stage-1 driver (~4 GB peak) + aarch64-gcc on the emitted stage-2
  asm + qemu by hand.

- 2026-07-11 (follow-up, same day): **the 0x04000000 hint was WRONG — reverted
  to 0x10000000 (8 GiB stands).** CI's native aarch64 lanes lit up red (38
  fixture failures: garbage bytes_writer output, wrong exit codes, hangs) and
  the corruption reproduced under qemu with the NATIVE backend. Bisection
  isolated the HINT, not the size: the native arm64 backend's below-heap rc
  guards (emitRcInc / emitRcDec / the IR rcop) classify `ptr >= 0x10000000`
  as heap-allocated — with the arena based at 0x04000000, every heap pointer
  read as "below heap" and rc inc/dec silently no-op'd, breaking COW/alias
  semantics everywhere. The self-host emitters have no such threshold guards
  (they null/low-addr/sentinel-guard only), which is why the mmc2 fixpoint
  was byte-identical at the low hint while every native-built binary was
  corrupt. Red herring for the record: qemu's -strace prints the 8 GiB mmap
  length as 0 (32-bit format truncation) — the literal-pool ldr =N was fine
  (verified in the disassembly: two-word 64-bit literal, correctly resolved).
  The earlier entry's "hint 0x04000000" details are superseded by this.

- 2026-07-12: **#4355 — SCALAR-FIELD struct-array element-box reclaim
  (`var g = [P{…}, P{…}]`, P scalar-only).** The complement to the #4365
  ARRSTRUCT slice, which landed the DEEP struct-array reclaim
  (`(<struct-with-array-field>)[]` — annotated, per-element `__struct_drop_<T>`
  + box + outer buffer via `emit_arrstruct_deep_free`) but, by design, only
  credits structs that ROUTE field-reclaim (`struct_has_reclaim_array_field`).
  A **scalar-only** struct-array (`P { x: i32, y: i32 }[]`) fell through to the
  generic is_arr shallow `__fern_rc_dec`, freeing only the OUTER buffer — so
  every element struct box still leaked per iteration. This slice fills exactly
  that gap. KEY REUSE: an element pointer and an inner array buffer are both
  freed by the same primitive (`__fern_rc_dec` maps to `__fn___fern_arr_dec` /
  `$__fern_arr_dec` on every backend), so a scalar-field struct-array routes
  through the EXISTING `__fern_arrarr_free` helper (one rc-guarded arr_dec per
  element pointer, then the outer buffer) with NO new backend runtime — the
  whole slice lives in irlower.fern. ADMISSION (`collect_fresh_structarr_names`
  + "STRUCTARR:" credit): every element a fresh no-base struct LITERAL
  (`structarr_lit_is_fresh` — each box rc=1, solely owned by the buffer); two
  gates — `structarr_unsafe_for` and `structarr_elem_escapes`.
  APPEND-BUILT (#6127): admission once also required the name to be NOT
  reassigned, which excluded `var ps: P[] = []` grown by
  `ps = ps.append(P { .. })` and leaked one element BOX per append —
  measured on the leakcheck differential at 38400 bytes over 100 rounds,
  scaling linearly, against native's 0. `structarr_unsafe_for` replaces the
  blunt exclusion with the same per-rebind validation `arrarr_unsafe_for`
  (#6092) and the string[] class use: only a self-append of a fresh no-base
  struct literal passes (`structarr_elem_store_ok`), everything else is
  still `body_unsafe_for`. `arrarr_lit_is_empty` +
  `structarr_name_is_appended` admit the `[]` initializer.
  An append-built candidate takes a SEPARATE credit, "STRUCTARRA:", which
  `slot_is_reclaimable_structarr` additionally gates on
  `struct_all_scalar_fields` — every field a by-value primitive, an
  ALLOWLIST so an unrecognised field type reads as non-scalar and keeps the
  struct out. Literal-built arrays keep the original "STRUCTARR:" credit and
  the original looser rule, so their emitted code is unchanged byte for byte.
  WHY the split (#6129): `struct_has_reclaim_array_field` deliberately lets
  string / map / option / tuple fields through ("they leak with the struct,
  sound"), so the shallow box free is only EXACT for an all-scalar struct —
  which is what this class documents itself as and what the code did not
  enforce. Widening the LOOSER rule across the ~112 append-built sites in the
  compiler's own sources broke self-compilation: gen0 and gen1 diverged on
  unit `2_s83` (1256103 vs 1251060 bytes), deterministically and
  independent of batch size. With the all-scalar gate the fixpoint converges.
  The cost is that an append-built struct array whose element has a STRING
  field is no longer reclaimed at all — it leaks as it did before the append
  work, rather than being unsoundly shallow-freed. Closing that needs the
  deep ARRSTRUCT path to learn string fields, not a looser gate here.
  DISJOINTNESS FROM ARRSTRUCT is enforced in the
  detector: `slot_is_reclaimable_structarr` bails when
  `struct_has_reclaim_array_field(sty)` — so a field-routing struct goes to the
  deep ARRSTRUCT path and a scalar struct to this shallow one; at most one class
  fires per slot (no double-free). Since a scalar struct owns no rc field, the
  shallow box free is EXACT (nothing leaks). The element gate is MORE PERMISSIVE
  than the arr-of-arr row gate on purpose: a struct-array admits transient
  iteration `for p in g { … p.field … }` (the box is borrowed only for the loop,
  dead before the free) unless the loop body lets `p` escape; a bare element bind
  `var q = g[i]` is still rejected conservatively. WIRING: exit-sweep is_arr-loop
  `else if` branch + `emit_structarr_reclaim_store` (cow-guarded) at the loop
  rebind, both after the ARRSTRUCT/ARRTUP branches. VERIFIED:
  TestSelfHostStructArrReclaim{IRX86_64 (scalar-flat + annotated-flat +
  iter-flat + elem-alias-safe), WasmIR (3), IRArm64 (3)} — reclaim fires (churn
  flat at detector zero), transient iteration admitted, element-alias excluded
  (q stays valid, no over-release). Fixtures use scalar-field structs (the only
  shape this class covers). Rebased onto #4365's ARRSTRUCT/ARRTUP/OPTSTRUCT/
  OPTTUP work; the two struct-array classes are now disjoint siblings.
  Fixpoint stays byte-identical (the self-host's own struct-array literals, e.g.
  `var nparams: ParamDecl[] = [ParamDecl {…}]`, all escape into a FuncDecl, so
  body_unsafe_for excludes them). Remaining nearby: `string[][][]` deep free,
  map string K/V (#4353).

- 2026-07-12: **#4353 scoping — register-backend map string K/V deep-release
  (design, not yet implemented).** Investigation of the last-standing reclaim
  item. STATE: wasm's `$__fern_map_release` already deep-releases string K/V
  (occupancy-aware `used[]` walk over its 40-byte box, gated by the `kis@+20` /
  `vis@+28` flags — each occupied slot's key/value `arr_dec`'d before the
  buffers). The REGISTER backends (x86 `asm_ir`, arm64) do NOT: `__fern_map_free`
  frees the keys/vals buffers via `__fern_arr_dec` + returns the raw 16-byte box
  to the freelist, but the string boxes HELD in those buffers leak one level.
  KEY LAYOUT DIFFERENCE (why wasm's body can't be mirrored): the register map box
  is a RAW 16-byte `{keys@0, vals@8}` — a LINEAR PARALLEL-ARRAY map (not
  open-addressing): `__fern_map_set` linear-scans keys, and on miss
  `__fern_arr_push`es key+val. So occupancy is DENSE (`0 .. keys.len()`, no
  tombstones / no `used[]`), which makes the element walk trivial — but
  `__fern_map_set` stores the key/val pointer with NO `__fern_rc_inc`. So the map
  does not own a counted ref to its string K/V.
  THE SOUNDNESS FORK (a dec-on-release alone would double-free an aliased key):
    1. **Native-discipline port (general, risky).** Native incs the string
       key/value on insert (`ir.go` balances `__drop_map_str_keys` /
       `__drop_map_str_values` against that inc). Porting means adding a
       string-K/V `__fern_rc_inc` to the register `__fern_map_set` (which needs a
       val-kind flag alongside the existing key-kind arg) AND the dec-walk to
       release. Touches the generic map runtime used by EVERY map — including the
       compiler's own heavy map use — so an imbalance miscompiles the self-compile
       (caught by the fixpoint, hard to bisect). Highest coverage, highest risk.
    2. **Fresh-K/V subset (bounded, sound, recommended first cut).** Deep-release
       only when every string K/V put into a reclaimable map is provably FRESH
       (literal / concat / fresh producer — the same discipline the "SARR"
       string[]-element reclaim uses): then the map is the sole rc=1 owner and a
       dec-on-release balances the alloc with NO `map_set` inc change. REUSE: the
       map's keys buffer (string-K) IS a `string[]`, so freeing it via the
       EXISTING `__fern_str_arr_free` (walk + per-element rc-aware free + buffer)
       instead of `__fern_arr_dec` deep-releases it — no new element-walk asm.
  PROPOSED SHAPE for cut (2): per-string-column `__fern_map_free` variants
  (`_ks` / `_vs` / `_kvs` — swap the string column's `__fern_arr_dec` for
  `__fern_str_arr_free`, keep the box free) on x86 + arm64; wasm keeps routing to
  its existing deep `$__fern_map_release`. `emit_map_buffers_free` picks the
  variant from the map slot's K/V string-ness (`map_type` value tag + key kind)
  AND a new `map_kv_fresh` gate (walk every `m.set(...)` / `Map{…}` entry for the
  map; require each string K/V arg fresh). NEW ANALYSIS = the `map_kv_fresh` gate;
  everything else is routing + small asm variants. TEST PLAN: churn
  `Map[string,i32]` / `Map[i32,string]` / `Map[string,string]` built from fresh
  concats (flat at detector zero); an ALIASED-K/V exclusion case (a `m.set(local,
  …)` keeps the sound leak, no over-release); x86 + arm64 + wasm (wasm already
  green), fixpoint byte-identical (the compiler's own maps are `i32`/interned-key,
  and any string-valued map with a non-fresh value stays excluded → inert).

- 2026-07-12: **#4353 — cut-2 attempt found a DEEPER PREREQUISITE: the register
  map's keys/vals BUFFERS don't recycle at all.** Implemented the cut-2 fresh-K/V
  value-column deep-release end-to-end (a `map_kv_fresh`-style gate crediting a
  `Map[K, string]`-with-fresh-values local "MAPVS:", routing `emit_map_buffers_
  free` to a new `__fern_map_free_vs` that freed the vals column via the existing
  `__fern_str_arr_free` on x86 + arm64; wasm routed to its already-deep
  `$__fern_map_release`). It compiled on all three backends and the SOUNDNESS
  cases passed (no over-release / no corruption / aliased-value exclusion), and
  the emitted asm carried the expected `__fern_map_free_vs` calls — **but the
  value boxes still leaked**. Root-causing (helper-function churn — a map declared
  directly in a loop is NOT freed per iteration, a separate pre-existing gap, so
  the map must be built in a callee to be swept per call; and a `> 2000`-byte
  boolean growth check because a raw `b2 - b1` exit code wraps mod 256 and 96000 %
  256 == 0, which masked the leak as "flat" through several probes — a trap worth
  remembering) showed the value column can't be the culprit: **the existing
  SHALLOW `__fern_map_free` already leaks even an `Map[i32, i32]`'s keys/vals
  buffers.** `__fern_map_new` allocs the empty keys/vals via `__fern_alloc_u8(0)`
  and `__fern_map_set` grows them with `__fern_arr_push`; at reclaim
  `__fern_map_free`'s `__fern_arr_dec` on each buffer is a **no-op** (it does not
  reach the rc==1 free path — the buffer is either not rc-headed the way arr_dec
  expects, or the map holds it at rc>1), so the buffers (and thus any string
  elements inside them) are never returned to the freelist. The existing map-
  reclaim tests only assert RESULTS (6 / 14 / 55), never heap flatness, so this
  was uncaught. **Consequence:** the value deep-release is blocked on this deeper
  fix — a `str_arr_free` element walk can't fire while the buffer itself never
  hits rc==1. NEXT STEP for #4353 must FIRST make the register map's keys/vals
  buffers reclaim (audit `__fern_alloc_u8` → `__fern_arr_push` rc handling: does
  the grown buffer carry cap@[b-16]/rc@[b-8] and rc==1 at drop? if not, either
  route map buffers through `__fern_arr_box` on first push or free them
  unconditionally in `__fern_map_free` given the MAP sole-ownership gate), THEN
  layer the string-value element walk on top (the cut-2 code, reverted here,
  applies unchanged once buffers recycle). The non-working cut-2 patch was
  reverted to keep the tree clean; this entry preserves the diagnosis so the
  next attempt starts from the real blocker, not the value column.

- 2026-07-12 (CORRECTION to the entry above): **#4353 cut-2 actually WORKS —
  the value column IS deep-released; the previous "buffers don't recycle"
  conclusion was a MISATTRIBUTION.** Re-measured with a DIFFERENTIAL harness
  (string-valued vs i32-valued map of the same shape, so the shared leak cancels):
  on the pre-fix tree an `Map[i32, i32]` helper-churn grows **96 KB** / 2000 calls
  and an `Map[i32, string]` grows **224 KB** — the 128 KB gap is exactly the leaked
  value boxes. WITH the cut-2 `__fern_map_free_vs` deep-release the string map drops
  to **96 KB**, equal to the i32 map: **the 128 KB of value boxes is freed.** The
  residual 96 KB is NOT a buffer-reclaim failure — it is the `__fern_arr_push`
  grow-leak (each map's first insert grows the cap-0 keys+vals buffers and abandons
  them: 2 × 24 B × 2000 = 96 000 B), a SEPARATE documented **LOAD-BEARING** leak the
  arr_push body explicitly must not fix naively (it would double-free; the real fix
  is the reuse-analysis routing of goal 2). The earlier entry was fooled by (a) that
  96 KB baseline being present in every map and (b) a raw `b2 - b1` exit code
  wrapping mod 256 with 96000 % 256 == 0, which read as "flat". Lesson baked into
  the test: **measure the value column DIFFERENTIALLY against an i32-valued map**,
  not against zero. So cut-2 is restored and landed: `MAPVS:` credit
  (`map_str_value_reclaimable` — annotated `Map[K, string]`, init insert-chain +
  every later `m.set/insert` value a fresh string) → `emit_map_buffers_free` routes
  to `__fern_map_free_vs`, which frees the vals column via `__fern_str_arr_free` on
  x86 + arm64 (wasm keeps its already-deep `$__fern_map_release`). Keys still leak
  one level (a follow-up); the arr_push grow-leak is orthogonal and remains. Tests:
  `TestSelfHostMapVsReclaim{IRX86_64, WasmIR, IRArm64}` — differential value-column
  flatness + correctness + aliased-value exclusion. Also found (pre-existing, orthogonal): on the wasm IR path the map string-VALUE release via $__fern_map_release does NOT actually free the value boxes (a differential churn leaks), so the wasm map test asserts routing soundness only — a separate follow-up. Remaining #4353: string KEYS
  (same treatment, keys column), then the grow-leak via goal-2 reuse routing.

- 2026-07-12: **#4353 — string-KEY column + both-column deep-release (sibling of
  the value column).** Generalised the value-column machinery: the arg-freshness
  helpers now take an `argidx` (0=key, 1=value), a reclaimable map with a fresh-
  string KEY column is credited `MAPKS:` (via `map_str_key_reclaimable` —
  annotated `Map[string, V]`, init insert-chain + every later set/insert KEY a
  fresh string), and `emit_map_buffers_free` routes 4-way: `__fern_map_free_kvs`
  (both credited), `_ks` (keys only), `_vs` (values only), or the shallow
  `__fern_map_free`. New register-backend bodies `__fern_map_free_ks` / `_kvs`
  (x86 + arm64) free the keys (resp. both) column via `__fern_str_arr_free`; wasm
  routes all to `$__fern_map_release`. VERIFIED differentially (build-and-drop, NO
  `m.has("a"+"b")` lookup — that allocates a fresh lookup-key temp that leaks
  independently, the key-side twin of the `get_or ""` default): `Map[string, i32]`
  and `Map[string, string]` helper-churn both match the `Map[i32, i32]` baseline
  (keys / both columns freed). Tests: `TestSelfHostMapKsReclaim{IRX86_64 (key +
  both-column differential + correctness + aliased-key exclusion), IRArm64,
  WasmIR (routing soundness)}`; the value-column suite stays green (the argidx
  refactor is behaviour-preserving) and the stage-2 fixpoint is byte-identical
  (the compiler's own maps are i32/interned-key). Remaining #4353: the wasm-IR
  `$__fern_map_release` string-K/V release gap, and the orthogonal arr_push
  grow-leak (goal-2 reuse routing).

- 2026-07-12: **#4353 wasm-IR value-release gap — root-caused into TWO bugs; fixed
  bug 1 (first-insert vis flag).** The wasm IR path leaked a `Map[i32, string]`'s
  values even though it routes the free to `$__fern_map_release` (which
  deep-releases the value column when the box's `vis@28` flag is set). BUG 1
  (fixed here): a `Map{…}` literal desugars to `map_new_i32().insert(k0,v0)
  .insert(k1,v1)…`, and irlower computes the wasm `vis` flag on each `op_map_set`
  from the RECEIVER-derived map value type. The FIRST insert's receiver is the
  bare `map_new_i32()` call, whose value type isn't inferred yet (defaults to
  i32), so a string first value emitted **vis=0** — the map never retained it and
  `$__fern_map_release` never freed it (a wat probe showed vis = 0,1 across a
  two-entry string map instead of 1,1). This is also a latent **correctness** bug:
  an ALIASED pointer value at the first insert with vis=0 is a use-after-free
  (#3495 for the non-first entries). Fixed by OR-ing an `insert_value_is_ptr`
  (string / struct / string-array — mirrors the wasm AST path's
  `is_string_expr || struct || array`) on the value ARG into the vis computation
  at both insert-lowering sites; register-safe (the register `op_map_set` ignores
  the flag — only i32_imm/keykind + eqfn), so the fixpoint stays byte-identical
  and the register map suites are unaffected. BUG 2 (deeper, NOT fixed —
  documented for the next pass): even with vis=1 the fresh value still leaks by
  one, because the wasm map's retain-on-set model is UNBALANCED for a FRESH value
  consumed by the set — `$__fern_map_set` retains (value rc 1→2) but nothing decs
  the consumed temp's original reference, so `$__fern_map_release`'s dec only
  brings it back to 1. The register backend avoids this by NOT retaining (the map
  takes the single ref; the MAPVS gate frees it at rc 1→0). To make wasm flat for
  fresh values, the wasm set site must dec the consumed fresh temp after a
  retaining set (a freshness-gated consume, the wasm twin of the register MAPVS
  analysis) — a real slice, out of scope here. So the wasm map tests stay on
  routing-soundness + correctness (not column flatness) until bug 2 lands.

- 2026-07-12: **#4353 wasm bug 2 LANDED — fresh key/value reclaim via a per-insert
  `vconsume`/`kconsume` flag; the wasm map columns are now FLAT (both key + value).**
  The previous entry's bug 2 (the fresh-value retain-imbalance) is fixed, and its
  key-column twin along with it. The wasm map's retain-on-set model incs the
  key/value on every `$__fern_map_set` (co-ownership, balanced by the SOURCE
  local's exit sweep). A FRESH temp (`"a"+"b"` as a key or value) has NO source
  local to sweep, so the inc left rc at 2 and `$__fern_map_release`'s per-slot dec
  stranded it at 1 — a leak of one box per fresh key/value per map. FIX: a
  per-insert consume flag, the wasm twin of the register MAPKS/MAPVS analyses but
  applied at the INSERT granularity (not per-map): `op_map_set` now carries a
  2-bit `width` field (bit 0 = value fresh, bit 1 = key fresh), computed by irlower
  from `is_fresh_str_temp(c.args[1|0], s)` at both insert-lowering sites.
  `$__fern_map_set` gained `$vconsume` / `$kconsume` params; when set it SKIPS the
  construction-inc so the map TAKES the fresh temp's single ref, and the release-
  side dec then reclaims it (rc 1→0). An ALIASED key/value (a bare local) is not
  fresh → consume=0 → the map retains and the source local's sweep balances it, so
  no over-release / no UAF. VERIFIED DIFFERENTIALLY (build-and-drop in a callee, NO
  lookup — a `get_or(k, "")` / `has("a"+"b")` allocates a fresh default/lookup temp
  that leaks independently): pre-fix a `Map[i32,string]` helper-churn grows 16 000 B
  / 500 calls (32 B = 2 value boxes) over the `Map[i32,i32]` baseline and a
  `Map[string,i32]` the same for keys; WITH the fix both drop to 0 (equal to the
  i32 baseline — the wasm map has no arr_push grow-leak, its `$__fern_map_grow`
  frees the old buffers, so the fully-reclaiming map keeps the bump high-water mark
  flat). Correctness (values readable through churn), aliased key+value exclusion
  (bare locals stay valid, `__rc_underflow()==0`), and the overwrite path
  (duplicate fresh key → old value released, new taken, no over-release) all pass.
  REGISTER-SAFE + fixpoint byte-identical: the x86-64 / arm64 `op_map_set` lowerings
  read `i32_imm` (keykind) + `str` (eqfn) and IGNORE `width`, so their emitted asm
  is unchanged; the compiler's own maps are i32/interned-key with non-fresh values,
  so `width` stays 0 there anyway. The legacy wasm AST path keeps the retain-only
  model (both flags 0 — a fresh key/value leaks by one as before; that path is
  being retired in favour of the IR path). Tests: the wasm-IR Vs / Ks suites
  (`TestSelfHostMapVsReclaimWasmIR`, `TestSelfHostMapKsReclaimWasmIR`) now assert
  DIFFERENTIAL column flatness (value / key / both) on top of their prior
  correctness + aliased-exclusion cases — the follow-up those tests deferred is
  closed. Remaining #4353: the orthogonal register-side arr_push grow-leak (goal-2
  reuse routing); the wasm overwrite-with-fresh-KEY case still leaks the discarded
  key temp by one (rare — a duplicate fresh key in one literal; sound, not a UAF).

- 2026-07-12: **#4353 wasm map — overwrite-with-fresh-key reclaim (the word-count
  tail of bug 2).** The bug-2 fix reclaimed fresh keys/values on the INSERT path,
  but the OVERWRITE path (an existing key is re-`set`) discards the incoming key
  (the slot keeps its original) — and a FRESH discarded key temp was abandoned,
  leaking one key box per overwrite. This is the classic word-count / histogram
  shape `m.set(computed_key, n)` where the same computed key recurs, so it is a
  realistic leak, not a pathological one: measured differentially, a
  `Map[string,i32]` doing 8 re-inserts of `"wo"+"rd"` / call leaked 64 000 B / 500
  calls (8 × 16 B key boxes) over the i32-keyed baseline. FIX (wasm.fern
  `map_helpers` only, one gated line): in the overwrite branch, `(if $kconsume
  (drop (call $__fern_arr_dec $k)))` frees the discarded fresh key (sole-owned,
  now dead). An ALIASED recurring key (`$kconsume` 0) is left untouched — its
  source local's sweep owns it; an immortal literal key arr_dec's as a guarded
  no-op. Post-fix the string-key overwrite churn is flat (0), reads back the LAST
  value with len 1, and `__rc_underflow()==0` (no over-release); the aliased
  recurring-key case stays valid through the overwrites. This is a wasm-only delta
  (the register/arm64 backends don't use the per-insert consume model — their
  overwrite-key reclaim rides the separate MAPKS credit + the still-open arr_push
  reuse routing), so ir.fern / irlower.fern / the register asm are untouched and
  the fixpoint stays byte-identical. Tests: three cases added to
  `TestSelfHostMapKsReclaimWasmIR` (overwrite fresh-key flatness / correctness /
  aliased-key soundness). Net: the wasm map key+value reclaim story is now
  complete for both insert AND overwrite. Remaining #4353: only the orthogonal
  register-side arr_push grow-leak (goal-2 reuse routing).

- 2026-07-12: **#4353 register map grow-leak — root-caused the soundness blocker
  (it is NOT a naive arr_push_owned swap; keys()/values() alias the buffer).**
  The register map's `__fern_map_set` grows its keys/vals buffers via the shared
  LEAK-ONLY `__fern_arr_push`, abandoning the superseded cap-0 buffer — the
  documented 48 B/build grow-leak (measured exactly: 96 000 B / 2000 map builds =
  2 × 24 B cap-0 buffers). The obvious fix — route the two appends through the
  existing sole-owner `__fern_arr_push_owned` (which frees the old buffer on an
  rc==1 grow, the sanctioned "safe form" already used for unaliased `a =
  a.append(v)`) — DOES close the leak (re-measured: 96 000 → 0, no over-release)
  and mirrors the wasm map's grow-frees-old-buffers. **But it is UNSOUND as-is:**
  register `map_keys` / `map_values` (IR op kinds 131/132) return the RAW `keys@0`
  / `vals@8` buffer pointer with NO rc-inc — an UNCOUNTED alias (confirmed: `var
  ks = m.keys(); m.set(k,v); ks.len()` observably tracks the live buffer, unlike
  the wasm backend which snapshot-copies). So a live `keys()`/`values()` result
  followed by a growing `m.set` would let `arr_push_owned` (seeing the sole box
  reference, rc==1) FREE the buffer `ks` still points to — a use-after-free. The
  leak is load-bearing: it keeps that buffer alive and masks the hazard. This
  SHARPENS the old "gated on goal-2 reuse routing" note into a concrete blocker
  with a concrete fix order: **first** make register `map_keys`/`map_values`
  snapshot-copy (allocate + copy, matching the wasm `$__fern_map_snapshot` — also
  fixes the latent aliasing correctness bug where a snapshot tracks later
  mutations), OR give the alias proper counted-rc accounting; **then** the
  `arr_push_owned` swap in `map_set` becomes sound and the grow-leak closes. Left
  a guard comment at the `map_set` append site so the swap isn't re-attempted
  naively. The measured-but-reverted swap proves the leak is fully recoverable
  once the alias is made safe. NEXT concrete slice: register keys/values
  snapshot-copy (the wasm map already does this), which both fixes the aliasing
  correctness gap and unblocks the last #4353 grow-leak.

- 2026-07-12: **#4353 register (x86-IR) map overwrite-key reclaim — the register
  twin of the wasm #4876 fix.** The register `__fern_map_set` (asm_ir.fern),
  like wasm pre-#4876, leaked the incoming KEY on an OVERWRITE: when a re-`set`
  hits an existing key, the slot keeps its original key and the incoming key is
  discarded — but a FRESH discarded key temp was abandoned. This is the word-
  count / histogram `m.set(computed_key, n)` shape where a computed key recurs;
  measured differentially, a `Map[string,i32]` doing 8 re-inserts of `"wo"+"rd"`
  / call leaked ~256 B/call (the fresh key boxes) over the i32-keyed baseline
  (152 KB vs 24 KB / 500 calls; the 24 KB shared floor is the still-open cap-0
  grow-leak). FIX (self-contained to the x86-IR backend): thread the per-insert
  `kconsume` freshness bit (o.width bit 1, already computed by irlower's
  `is_fresh_str_temp` for #4869) into `__fern_map_set` via `%r9`; on the overwrite
  path (`.Lms_found`) free the discarded key `%r13` via the rc-aware
  `__fn___fern_str_free` when kconsume. `str_free` preserves the callee-saved regs
  map_set relies on (only rax/rcx/rdx/rsi/r8 scratch) and guards a literal's
  .rodata data / an rc>1 aliased key, so the free is sound. An ALIASED recurring
  key (kconsume 0) is left untouched (its source local's sweep owns it). VERIFIED
  (x86-IR driver): the string-key overwrite churn drops to the i32 baseline
  (152 KB → 24 KB, matching), reads back the LAST value with len 1, `__rc_underflow
  ()==0`; the aliased recurring-key case stays valid; general map ops (i32 growth,
  get/has/without/keys-iter, distinct-key string inserts) unaffected. BOOTSTRAP-
  SAFE: the compiler's own maps are i32/interned-key → kconsume always 0 → `%r9`=0
  → the new free path is never taken → map_set behaviour (and the modload
  fixpoint, which runs the AST backend anyway) is unchanged. Tests: 3 cases added
  to `TestSelfHostMapKsReclaimIRX86_64` (overwrite flatness / correctness /
  aliased soundness). PARITY FOLLOW-UP: the arm64-IR map_set (asm_arm64.fern) has
  the same gap and the same fix applies (thread kconsume via an arg reg, free the
  discarded key on overwrite) — a separate slice. The value-side overwrite leak
  (the OLD value dropped on overwrite for string-valued maps) and the cap-0
  grow-leak (blocked on keys/values snapshot-copy) remain.

- 2026-07-12: **#4353 arm64-IR map overwrite-key reclaim — parity with the x86-IR
  #4881 fix.** Mirrored the discarded-fresh-key overwrite reclaim into the arm64
  backend: `asm_arm64_ir.fern`'s op-125 emission passes kconsume in `x4`;
  `asm_arm64.fern`'s `__fern_map_set` saves it in the otherwise-unused callee-saved
  `x24` (no extra stack juggling — it's already `stp`'d) and, on `.Lms_found`,
  frees the discarded key `x20` via `__fn___fern_str_free` (`cbz x24` gate) when
  fresh. str_free clobbers only x0-x3/x9/x10/w4 (preserves x19/x20), so the mapbox
  return is intact. VERIFIED UNDER QEMU (not just CI): the string-key overwrite
  churn drops to the i32 baseline (24048 ≈ 23904, matching x86 exactly — same
  parallel-array map layout), correctness + aliased-key + general map ops all
  exit 0. Bootstrap-safe identically (compiler maps i32/interned → kconsume 0 →
  x24=0 → cbz skips → unchanged; the arm64 fixpoint stays byte-identical). Tests:
  3 arm64 cases added to `TestSelfHostMapKsReclaimIRArm64`. Both register backends
  now reclaim the overwrite-discarded fresh key, matching wasm #4876. The
  value-side overwrite leak and the cap-0 grow-leak (keys/values snapshot-copy)
  remain the open #4353 items.

- 2026-07-12: **#4353 remaining-work plan — the clean map-reclaim frontier is
  DONE; what's left is intricate, prioritized here.** After the 5-PR overwrite +
  insert fresh-key/value arc (wasm #4869/#4876; register x86 #4881; arm64 #4885;
  grow-leak root-cause #4877), the fresh string key/value word-count/histogram
  leak is closed on ALL backends for both insert and overwrite. The two remaining
  register-only items were each investigated to a concrete design; both are
  deferred on ENGINEERING JUDGEMENT (risk/ROI vs. a narrow leak, at bootstrap-
  adjacent surface), NOT inability. Recommended order + approaches:

  1. **Register value-side overwrite leak** (measured ~256 B/call: a string-valued
     map, same key re-`set` with fresh values — a re-computing cache). Freeing the
     OLD value on overwrite is sound only if it was solely-owned, but the register
     stores values UNCOUNTED, so `str_free`'s rc-awareness can't save an aliased
     value (its rc reflects only the source). The per-insert `vconsume` flag
     (o.width bit 0, already computed) proves the CURRENT value's freshness, not
     the OLD one's — so gating the old-value free on current vconsume is UNSOUND
     for a map whose key k first got an aliased value then a fresh one. SOUND
     options, each with a cost:
     (a) **Per-slot owned-bits** — a 3rd parallel i32 array on the register map box
         ({keys@0, vals@8} → +owned@16); map_set appends vconsume on insert, reads
         it on overwrite (free old iff owned), map_free frees the buffer; get/has/
         keys/values/iter/delete untouched. Bounded (~3 sites × x86+arm64) and no
         pointer-escape risk, BUT allocates an extra array on EVERY register map
         incl. the compiler's own i32 maps (hot-path overhead for a narrow leak).
         Could be gated to pointer-valued maps only, but map_new is type-agnostic.
     (b) **Pointer-tag the value** — store `v|1` in vals[] for fresh values, free
         `old&~1` on overwrite iff tagged. No extra array, but EVERY value reader
         (map_get / map_get_or / map_values snapshot / mapiter_value / the MAPVS
         str_arr_free) must mask `&~1`; a missed site leaks a tagged pointer into
         user code → crash. High escape-risk across a pervasively-used runtime.
     (c) **Adopt the wasm inc-on-set model** on the register (inc every value on
         set, dec on overwrite/death) — uniform + no per-slot state, but a large
         refactor of the value model + the MAPVS/vconsume analyses.
     RECOMMENDATION: (a) with lazy owned[] allocation (allocate only once a vconsume
     insert happens, so pure-scalar maps pay nothing) — the least-risk sound path.
     Wasm already frees the old value on overwrite (vis-gated arr_dec, sound via
     inc-on-set), so this is register-only (x86 + arm64).

  2. **Cap-0 grow-leak** (48 B/build). The `__fern_arr_push_owned` swap in map_set
     closes it (measured 96000→0) but UAFs a live `keys()`/`values()` alias
     (register map_keys/map_values return the RAW buffer, uncounted — #4877). BLOCKED
     on FIRST making register keys/values SNAPSHOT-COPY (alloc + copy; for string
     columns, inc each element so the fresh array genuinely owns them, matching
     irlower's existing "fresh owned array (rc=1)" assumption at
     `var ks = m.keys()`) — which ALSO fixes the latent aliasing correctness bug
     (a snapshot must not track later mutations; the wasm map already snapshots via
     `$__fern_map_snapshot`). THEN the arr_push_owned swap becomes sound. The
     snapshot needs reclaim-wiring too (today keys() results aren't reclaimed → a
     naive snapshot would leak its buffer). Multi-part (snapshot helper + element
     rc + reclaim flip + arr_push_owned swap) × (x86 + arm64).

  Lower-value nearby (rare types / narrow): `string[][][]` deep-free (a depth-3
  extension of `__fern_strarrarr_free` — new helper × 3 backends + an is_arrarrarr
  classifier; string[][][] is rare), and the map DELETE value/key leak (same
  ownership question as value-overwrite). None is a clean win; all await the
  ownership-tracking or snapshot-copy foundations above.

- 2026-07-12 (CORRECTION to the prioritized-plan entry above): **the register
  value-side overwrite free is UNSOUND under map-ownership tracking — both remaining
  #4353 items share ONE root blocker: uncounted read-aliases.** Traced the clean
  MAPVS-gated approach (check `"MAPVS:"+mapname` in `s.reclaimable_names` at the
  op_map_set site — it IS available there, computed at irlower.fern:26070 before the
  body lowers — and free the OLD value on overwrite when the map's value column is
  fully-fresh/sole-owned). It compiles and the ownership gate is real, BUT it
  introduces a use-after-free: `m.get_or(k, d)` / `m.get(k)` / `m.values()` /
  `m.iter()` return the STORED value pointer as an UNCOUNTED alias, so
    `var v = m.get_or(1, ""); m.set(1, "c"+"d"); print(v)`
  frees the old value at the overwrite while `v` still points at it. Map-ownership
  tracking (the plan's recommended owned-bits, OR the MAPVS gate) does NOT fix this
  — ownership says "the MAP solely owns the slot", but a live get_or/get borrow is a
  SEPARATE uncounted reference the map doesn't know about. The existing MAPVS
  exit-sweep stays sound ONLY because it frees the value column at map DEATH (once,
  at end of scope), never mid-life; freeing on overwrite is a mid-life free and
  that is what dangles the read. **This is the SAME root cause as the cap-0
  grow-leak's blocker (#4877): register map_keys/map_values/get_or return raw,
  uncounted aliases, so ANY mid-life free (overwrite value, or grow-freeing an old
  buffer) can dangle a live read.** So the two remaining items are not two separate
  hacks — they are one underlying deficiency. SOUND fixes, in order of preference:
  (1) **counted reads** — get_or/get/values/keys/iter rc-inc the returned pointer
      and the read RESULT is reclaimed (str_free / arr_dec) at its scope exit, so a
      live read holds rc>1 and a mid-life free rc-decs instead of freeing; this
      unblocks BOTH the value-overwrite free AND the grow-leak's arr_push_owned swap
      AND the keys/values snapshot correctness bug, uniformly. This is the real fix
      and it is large (touches every read site + their result reclaim + the map's
      rc model). (2) Leave both as death-only-sound leaks (current behaviour) until
      (1) lands. **Do NOT implement owned-bits or a MAPVS-gated overwrite-free — it
      UAFs on get_or-then-overwrite.** This supersedes the "recommendation: lazy
      owned-bits" in the entry above.

- 2026-07-12: **#4353 counted-reads foundational change — DECOMPOSITION (grounded).**
  Per the correction above, both remaining register items need counted map reads.
  Investigated the code and decomposed it into individually-sound slices, with the
  exact sites located, so each can land + validate independently:

  **Slice 1 — register i32 keys()/values() snapshot-copy + result reclaim** (the
  clean, no-element-rc first step; fixes a read-aliasing correctness bug on its own):
  - Runtime (asm_ir.fern kind 131/132 ~line 4868, asm_arm64_ir.fern ~kind 131/132):
    today they return the RAW buffer (`box+0`/`box+8`) — an ALIAS. For an i32-element
    column, replace with a snapshot: alloc a fresh i32 arr_box(len), copy len elems,
    return it. Gate on element-scalar (op_map_keys/values are 0-arg ops → add an
    "elem-is-pointer" flag from irlower's map_kv_elem_tag; snapshot only the scalar
    case, leave string columns on the alias path until slice 3).
  - Reclaim FLIP (irlower reclaimable_names_of): a `var ks = m.keys()` i32[] result is
    currently a BORROW (not credited → not dec'd at exit; that's why the alias doesn't
    double-free today). Once it's a fresh owned snapshot it MUST be credited so the
    exit-sweep arr_dec frees it (else the snapshot buffer leaks). OPEN: pin the exact
    scalar-array reclaim-candidacy gate — the credits use prefixes (SARR/ARRARR/ARRTUP/
    ARRSTRUCT at irlower.fern:18211-18354) but the plain-i32[] exit-dec path + where
    keys()/values() inits are excluded from it wasn't pinned this session; that gate is
    the "most regression-prone surface" (CLAUDE.md) — do it carefully, not rushed.
    Must be gated to i32-element results ONLY (string results stay borrow).
  - wasm already snapshots (`$__fern_map_snapshot`) so the shared reclaim flip is safe
    there (reclaiming a copy is balanced). Validate: keys()/values() no longer track a
    later mutation (snapshot semantics), churn flat (no leak), __rc_underflow()==0,
    fixpoint byte-identical.

  **Slice 2 — grow-leak close for i32 maps** (depends on slice 1): once i32 keys()/
  values() snapshot (no buffer alias), and get_or returns i32 by-value (no alias) and
  iter re-reads box+0 each step (no stale ptr), an i32-key/i32-value map's buffers are
  sole-owned → swap map_set's keys/vals appends to __fern_arr_push_owned GATED on
  i32-key AND i32-value. Closes the 48 B/build grow-leak for scalar maps (incl. the
  compiler's own i32/interned maps). (#4877 measured the swap: 96000→0.)

  **Slice 3 — string keys()/values() snapshot** (element-rc): snapshot + inc each
  string element on copy (so the fresh array owns its elements), result deep-freed
  (str_arr_free) at exit. Unblocks slice-2's swap for string columns too.

  **Slice 4 — counted get_or/get for value-overwrite** (the value-side fix): get_or/
  get rc-inc the returned value, result reclaimed at scope exit; then map_set's
  overwrite can rc-aware-free the old value (a live read holds rc>1 → dec not free).
  Closes the value-overwrite leak soundly (supersedes the unsound owned-bits).

  Order 1→2→3→4; each is independently sound + testable. Slice 1's reclaim-flip is the
  gating unknown and the sensitive part — a focused session should pin the scalar-array
  reclaim-candidacy gate first, then implement 1 with the full differential + fixpoint
  gate before proceeding.

- 2026-07-12 (CRITICAL addendum to the decomposition — a naive slice 1 DOUBLE-FREES):
  Traced why the current register keys()/values() code is sound despite aliasing, and
  it is a DELICATE LEAK-BALANCE that any slice-1 edit must unwind holistically:
  `var ks = m.keys()` IS reclaimed — the exit-sweep arr_dec's it like any fresh i32[]
  (verified: a plain `var a: i32[] = [...]` churn is flat, so scalar arrays DO reclaim)
  — and since keys() returns the RAW map buffer (alias), that arr_dec frees the MAP's
  keys buffer. This does NOT double-free ONLY because `__fern_map_free`'s arr_dec on the
  keys/vals buffers is a no-op/leak (the very grow-leak of #4877): the map never frees
  its own buffers, so the ks-reclaim freeing the aliased buffer is the ONLY free. So
  three things mutually compensate: (a) keys() aliases the buffer, (b) the ks result is
  reclaimed and frees it, (c) the map leaks its buffers at death. Consequences for the
  decomposition:
  - A naive slice 1 that makes keys() SNAPSHOT (fresh copy) while leaving the result
    reclaimed would: free the snapshot at exit (fine) BUT the map's real buffer now
    ALSO never gets freed by anyone (ks no longer aliases it) → the map buffer leaks
    MORE (regression), unless the map is also made to free its buffers.
  - A naive slice 2 that makes the map free its buffers (arr_push_owned / map_free)
    WITHOUT first desugaring the keys()-alias would DOUBLE-FREE: both the ks-reclaim
    AND the map now free the same buffer.
  So slices 1 and 2 are NOT independent — they are one coupled change: snapshot keys()/
  values() (breaking the alias) AND make the map own+free its buffers, together, so the
  ks-reclaim frees only its own copy and the map frees only its own buffers. THIS is the
  real shape of the foundational change, and why it must be one careful, holistic slice
  on the reclaim surface — not the 4 independent steps the entry above implied. The
  correct first move in a focused session: write the coupled (keys/values-snapshot +
  map-owns-buffers) change together, then validate the whole balance (churn flat,
  __rc_underflow()==0, get_or/iter correctness, fixpoint) as a unit.

- 2026-07-12: **#4353 coupled slices 1+2 LANDED — scalar keys()/values() snapshot
  + map-owns-buffers (owned grow), gated to i32-class columns, all together.**
  Implemented exactly as the addendum above prescribed, as ONE change:
  - `op_map_keys` / `op_map_values` now carry an ELEMENT-kind flag (`i32_imm`:
    0 = scalar i32 column, 1 = pointer/string/i64/f64/generic/unknown), derived
    at the irlower sites from the map's type tag (`map_kv_elem_flag`, keyed off
    map_key_kind_of / map_value_of — strictly "i32", everything else stays 1).
    Flag 0 → the register backends SNAPSHOT the column into a fresh rc=1 copy
    via a new `__fern_map_snapshot_col` runtime helper (asm_ir.fern x86 +
    asm_arm64.fern arm64, __fern_arr_box-layout, registered in
    emit_runtime_globls for the per-module link; "maps"-need-gated, kinds
    131/132 flag-0 mark the need and op_allocates now admits 131/132). Flag 1 →
    the historical raw keys@0/vals@8 alias, byte-for-byte (string columns are
    slice 3). wasm ignores the flag (always snapshotted).
  - `op_map_set` gained width bit 2 = `owncols` (`map_owncols`: i32 keys AND
    i32 values). The register `__fern_map_set`s take it via the existing flag
    register (%r9 / x4, now a bitfield: bit 0 kconsume, bit 1 owncols) and, on
    the append path, route BOTH column appends through the sole-owner
    `__fern_arr_push_owned` when set — the superseded buffer is freed on grow,
    closing the #4877 cap-0/grow leak (sound now that no scalar read aliases
    the buffers). All other maps keep the LEAK-ONLY `__fern_arr_push`. wasm
    masks its vconsume/kconsume reads to bits 0/1 (already frees on grow). The
    arm64 AST insert call site now zeroes x4 explicitly (it previously left the
    shared map_set's kconsume register uninitialised — a latent #4885 hazard).
  - The exit-sweep needed NO flip: `var ks = m.keys()` was already is_arr +
    swept — under the alias that dec freed the MAP's live buffer and
    `__fern_map_free` then double-dec'd it (a keys()-taken fresh map ticked
    `__rc_underflow`); with the snapshot the sweep frees the COPY and map_free
    frees the map's own buffers exactly once. Balance restored, not rewired.
  - The `for k in m.keys()` / `for (k, v) in m` hidden column locals: a flag-0
    (scalar) column is a snapshot on EVERY backend now, so the loop lowering
    releases it (`__fern_rc_dec`) right after the loop end (break exits
    included; an early return leaks it — bounded, sound) and zeroes the slot.
    Flag-1 columns stay borrows (register) / leaked snapshots (wasm),
    unchanged. CONSEQUENCE (deliberate): a body that mutates the map iterates
    the ENTRY-TIME snapshot — matching the wasm self-host backend, but NOT
    native, which iterates the live map (`for (k,v)` + insert-in-body sees new
    entries natively). The old register behaviour (live until a grow, then a
    stale frozen buffer) was neither, and with owned grow it would have been a
    UAF; snapshot is the only memory-safe choice short of counted reads
    (slice 4). Expression-position `m.keys().len()` on a scalar column now
    allocates a copy nobody frees (previously a free alias read) — a bounded
    sound leak, same class as other unswept expression temps.
  - REMAINING known hole (pre-existing, unchanged in kind): type-tag-derived
    gating means a map viewed through a GENERIC signature (`Map[K, V]`) reads
    its columns as pointer-ish (flag 1 alias, no owncols) while a concrete
    i32/i32 view of the SAME map snapshots + owned-grows. A keys() alias taken
    under the generic view that outlives the call while concrete code grows
    the map can dangle — the same uncounted-alias root the correction entry
    names; counted reads (slice 4) is the real fix. No in-tree code hits this
    (the compiler sources use no keys()/values() at all).
  Tests: TestSelfHostMapKeysSnapshotIRX86_64 (7 cases: snapshot semantics
  across a grow, i32/i32 grow-churn ABSOLUTE flatness ≤4 KB/2000 builds,
  keys()-taken churn flatness + underflow==0, mutate-during-iteration
  snapshot + grow, for-(k,v) churn flatness, break-path release, mixed
  i32/string-value map balance) + TestSelfHostMapKeysSnapshotIRArm64 (qemu,
  4 cases) + TestSelfHostMapKeysSnapshotWasmIR (4 cases incl. the for-(k,v)
  snapshot release, which was a per-loop wasm leak before). FALLOUT: the
  TestSelfHostMapKsReclaim{IRX86_64,IRArm64} differential-flatness legs used a
  Map[i32, i32] baseline precisely BECAUSE it shared the grow floor — now it
  is flat, so those baselines moved to a LITERAL-keyed Map[string, i32] (same
  buffer shape, still leak-only grow; verified the differential cancels
  again). Gotcha found on the way: an [i32, boolean] annotation NORMALIZES to
  "Map[i32, i32]" in the type tag before map_owncols sees it, so
  boolean-valued i32-keyed maps are owned too (consistent — their
  keys()/values() snapshot off the same normalized tag). Refs #4353 #4451.

- 2026-07-13: **#4353 slice 3 (string-column keys()/values() snapshot) —
  LANDED.** The earlier same-day "parked on a string-layout trap" entry was
  based on a STALE comment, now corrected. The layout question it posed is
  answered: `__fern_str_box` (asm_ir.fern) allocates a **24-byte** block
  `{rc@base, data@base+8, len@base+16}` and returns `box = base+8`, so
  `box-8` IS a real rc word; `__fern_str_free`'s CODE reads/decrements it
  (rc<0 immortal→skip, rc>1→dec, rc==1→free, rc==0→underflow) — it is
  rc-AWARE, not the "header-less, unique-only, unconditional-free" its former
  contract comment claimed (that comment described the pre-#2649 layout and
  was fixed in this change). So per-element `rc_inc` on a column string
  correctly retains it, and the deep `__fern_str_arr_free` release only decs
  a still-shared (map-owned) key/value — no corruption, no double-free. The
  landed shape:
  - `map_kv_elem_flag` returns **flag 2** for a string key/value column
    (0 = scalar i32, 1 = i64/f64/struct/generic alias, 2 = string).
  - The register backends snapshot flag-2 columns into a fresh rc=1 array via
    the new `__fern_map_snapshot_col_str` (x86 in asm_ir.fern, arm64 in
    asm_arm64.fern) — a bit copy of the box pointers, then `__fn___fern_rc_inc`
    on each (its guards skip null / SSO / literal / immortal). Registered in
    `emit_runtime_globls` for the per-module link. wasm gets the retaining
    `$__fern_map_snapshot_inc` / `_keys_inc` / `_values_inc` next to the
    plain snapshot.
  - The SHARED irlower `for k in m.keys()` / `for (k, v) in m` loop lowering
    deep-releases a flag-2 hidden column local right after the loop via
    `__fern_str_arr_free` (rc-aware per-element `__fern_str_free` then the
    buffer) — the exact inverse of the retaining snapshot. Break paths land
    after the release; an early `return` in the body skips it (bounded sound
    leak, like every non-swept hidden temp), matching the scalar arm.
  - SCOPE: the snapshot is emitted **only at the loop positions** (self-
    balanced). The `var ks = m.keys()` / expression `m.keys().len()` sites
    clamp string columns back to the raw-buffer ALIAS (flag 1) — a retaining
    snapshot there would leak (no reclaim); unchanged behaviour, a SARR-
    credited var snapshot is a later follow-up. The map still keeps leak-only
    string column buffers (no owned-grow for string columns — that is the
    separate slice-2-for-strings); map_free_ks/vs deep-free them at death,
    disjoint from the snapshot copies.
  - CONSEQUENCE (deliberate, matches wasm + native intent): a `for (k,v) in m`
    body that mutates the map iterates the entry-time string snapshot. Also
    CLOSES the wasm per-loop string-column snapshot leak (the snapshot was
    non-retaining + leaked every loop before; now retained + deep-released).
  - Tests: `map-string-keys-iter-churn-flat` + `map-string-values-iter-churn-flat`
    on both `TestSelfHostMapKeysSnapshotIRX86_64` and `…WasmIR` (absolute
    churn flatness vs a no-iteration baseline + `__rc_underflow()==0`). The
    modload/load/heap-bump fixpoints stay byte-identical (the compiler's own
    string maps use get/set, so the runtime additions don't perturb the
    self-compile). Refs #4353.

- 2026-07-13: **#4353 slice 4 (counted get_or/get for value-overwrite) —
  IMPLEMENTATION SITES PINNED (not yet landed). Read before implementing.**
  Slices 1–3 landed. Slice 4 is the value-side leak fix and is ATOMIC (like
  slices 1+2 were coupled) — its four parts cannot land independently without
  a window of use-after-free or leak, so land them together with the full
  differential + fixpoint gate. Current state established this session:
  - The REGISTER `__fern_map_set` overwrite path (`.Lms_found`, asm_ir.fern
    ~L3248) currently **LEAKS the old string value** — it overwrites
    `values[i+1]=val` and frees only the discarded KEY (kconsume), never the
    old value. The WASM `$__fern_map_set` overwrite (wasm.fern ~L8634) already
    `$__fern_arr_dec`s the old value when `vis`.
  - `get_or`/`get` return an **uncounted alias** of the map's stored value on
    every backend, so a live `var old = m.get_or(k, "")` before a
    `m.set(k, "new")` would DANGLE if the overwrite freed the old value
    (that's why register leaks instead of freeing — leaking is the current
    "safe" choice, exactly the delicate balance slices 1–3 each unwound).
  The coupled slice-4 change:
  1. **op value-kind flag.** `op_map_get_or` (kind 129) / `op_map_get`
     (kind 127) and `op_map_set` (kind 125) need a VALUE-is-string flag
     (thread it like #4353's key/owncols width bits — the value-kind is at
     the irlower call site via `map_value_of(mty) == "string"`, already
     computed by the `expr_is_str` get_or arm at irlower.fern ~L2679).
  2. **Counted reads.** get_or/get emit `__fn___fern_rc_inc` on a string
     result (register kinds 127/129 in asm_ir.fern ~L4949 / asm_arm64_ir;
     wasm `$__fern_map_get`/`_get_or`) so a live read holds rc≥2.
  3. **Overwrite free.** map_set's string-value overwrite rc-aware-frees the
     old value: register `.Lms_found` gains `__fn___fern_str_free(old_value)`
     gated on the value-is-string flag (the rc-aware str_free decs a
     still-read value rc>1, frees at rc==1 — sound BECAUSE of step 2); wasm's
     existing `arr_dec` is already correct once reads are counted.
  4. **Reclaim gate (THE CRUX — most regression-prone).** A
     `var v = m.get_or(k, d)` string result must now be credited for STR
     reclaim (exit-sweep `__fern_str_free`) since it holds an inc'd
     reference — the `reclaimable_names_of` / `collect_fresh_str_names`
     (STR: prefix) gate must recognise a counted get_or/get string result as
     a fresh-owned string. Both get_or arms yield an owned string (the value
     path is now inc'd; the default arm is already a fresh/owned temp), so
     the reclaim balances. Get this wrong and every string-map program
     (incl. the compiler's own build via the fixpoint) leaks or double-frees.
  Gates: extend `TestSelfHostMapKeysSnapshotIRX86_64` / `…WasmIR` with a
  value-overwrite churn case (`var old = m.get_or(k,""); m.set(k,"new"); use(old)`
  in a loop → flat + `__rc_underflow()==0` + `old` still valid after the set),
  plus the modload/load/heap-bump fixpoints. Refs #4353 #4451.

- 2026-07-13 (slice 4 — the reclaim-gate CRUX, code-level findings from a
  deeper trace; read alongside the "sites pinned" entry above BEFORE the
  focused implementation). Part 4 (crediting `var v = m.get_or(k,d)` /
  `m.get(k)` string results for STR reclaim) is NOT a one-line addition —
  two concrete interlocks make it the delicate part:
  1. **The fresh-string predicate is SHARED — do not extend it.**
     `str_local_binding_is_fresh(init)` (irlower.fern ~L3014) is context-free
     (Expr only) and is used by BOTH `collect_fresh_string_names` (the STR:
     reclaim credit) AND `is_fresh_str_temp` (the concat-OPERAND reclaim that
     frees a dead anonymous temp right after a `+`). Adding get_or/get to it
     would make a counted get_or result used as a concat operand
     (`m.get_or(k,"") + x`) be freed as a temp — a DOUBLE FREE (it is also
     inc'd + scope-exit-reclaimed). Slice 4 needs a SEPARATE var-binding-only
     credit path, never a change to the shared predicate.
  2. **`reclaimable_names_of` has no map-type context.** Its signature
     (irlower.fern:18127) is `(body, structs, borrowable, fresh_ret,
     str_fresh_ret, opt_fresh_ret, tup_fresh_ret)` — no `LowerState`, so it
     cannot ask `map_value_of(s.map_type_of(slot))`. To credit ONLY
     string-value get_or results (not i32-value ones) it must resolve the
     receiver's Map value-type from the AST + `structs` alone (the receiver
     local's annotation / struct-field type / tuple-elem type — the same
     resolution the `expr_is_str` get_or arm at ~L2679 does, but that arm has
     `LowerState`). Either thread the needed map-type view into
     `reclaimable_names_of`, or gate the new credit behind the slot's `is_str`
     mark at the sweep site (the STR: credit already only fires reclaim when
     `slot_is_reclaimable_str` sees `is_str`, which the get_or arm sets only
     for string values — so an over-credit of an i32 get_or result is inert;
     verify this interlock holds before relying on it).
  Net: implement part 4 as a new type-aware var-binding credit (get_or/get on
  a string-value map, gated by body_unsafe_for + not-reassigned like the
  other STR: producers), NOT by touching str_local_binding_is_fresh. Validate
  the concat-operand case (`var v = m.get_or(k,""); var s = v + "x"; use(v)`)
  explicitly — it is the double-free trap. Refs #4353 #4451.

- 2026-07-13 (slice 4 — COMPLETE implementation-ready design, incl. the
  get_or default-arm ownership fork, the correctness crux of "counted reads").
  This supersedes the two entries above as the build spec; they remain valid
  for the individual site pins. Slice 4 is FIVE coordinated parts (not four —
  the default-arm handling is the extra one that makes counted reads correct):
  1. **Op value-kind flag.** `op_map_get`/`op_map_get_or` (kinds 127/129) gain
     a string-value flag (op_map_set already carries `valptr`); thread it from
     `map_value_of(mty) == "string"` at the irlower call sites.
  2. **Found-branch inc.** get_or/get emit `__fn___fern_rc_inc` on the string
     result **only on the FOUND branch** (register: inside kind 129's
     `golbl_d`/`golbl_e` found path in asm_ir.fern; get / kind 127 similarly;
     arm64 + wasm mirrors). NOT on the default branch — see part 4.
  3. **Caller default-inc-if-alias.** When get_or's DEFAULT arg is a string
     ALIAS (a bare ident / field / index — `is_fresh_str_temp` is false and
     it is a string), the caller rc-inc's it BEFORE the call, so the result
     owns an independent ref regardless of which branch is taken. A FRESH
     default temp (`""` / `a+b`) is already sole-owned and is MOVED into the
     result (no inc, not separately reclaimed). This is what makes both
     branches yield exactly one owned ref: found → V+1; default-alias →
     other+1; default-fresh → the temp itself. Get this wrong and either an
     alias default double-frees (v-reclaim + other-reclaim) or a fresh default
     leaks (+1 with no owner).
  4. **Overwrite free.** map_set's string-value overwrite rc-aware-frees the
     OLD value: register `.Lms_found` gains `__fn___fern_str_free(old_value)`
     gated on the value-is-string flag (`valptr`); wasm's existing `arr_dec`
     is already correct once reads are counted.
  5. **Reclaim credit (do NOT touch str_local_binding_is_fresh — trap #1
     above).** A new context-free structural collection credits
     `var v = m.get_or(k,d)` / `m.get(k)` string-VAR bindings STR: (gated by
     body_unsafe_for + not-reassigned, like the other STR: producers). Rely on
     the `slot_is_reclaimable_str` sweep-site `is_str` interlock so an i32
     get_or credit is inert (verify that interlock). Anonymous expression-
     position get_or (`use(m.get_or(...))`) is NOT credited → a bounded +1
     leak, same class as slices 1–3's var/expr positions (no regression: the
     register overwrite leaked the whole value before slice 4).
  Accounting (found path): map holds V rc1 → get_or inc → rc2 → overwrite
  str_free → rc1 (map's ref released) → scope-exit v-reclaim str_free → rc0
  free. No live read: overwrite str_free rc1→0 frees. Balanced both ways.
  Gates: value-overwrite churn (`var old=m.get_or(k,""); m.set(k,"new"); use(old)`
  loop → flat + underflow==0 + old valid after set), the alias-default case
  (`var v=m.get_or(k, live); ... use(v); use(live)` → no double-free), fresh-
  default reclaim, plus modload/load/heap-bump fixpoints byte-identical.
  Refs #4353 #4451.

- 2026-07-20: **#5335 (#4353 layer 0, general case) — string-column owncols +
  expression-position string snapshot, LANDED.** The "slice-2-for-strings"
  the slice-3 entry deferred. Two coupled irlower changes (backends needed
  ZERO instruction changes — the owncols bit and the flag-2 snapshot calls
  were already decoded on x86-64/arm64, and wasm ignores bit 2 / already had
  `$__fern_map_keys_inc`):
  - `map_owncols` is now DEFINED from `map_kv_elem_flag` (owned ⇔ neither
    column is flag 1), so i32/string column combinations in any mix are owned
    and `__fern_map_set` frees the superseded keys/vals buffers on every grow
    (`__fern_arr_push_owned`). i64/u64/f64 / struct / generic / unparseable
    columns remain flag-1 alias reads and keep the leak-only push. The
    invariant "owned ⇒ every read snapshots" is structural: a new column kind
    added to map_kv_elem_flag as non-1 automatically becomes owned, so it must
    snapshot on read.
  - The expression-position `var ks = m.keys()` / `m.values()` string-column
    CLAMP (flag 2 → 1, the slice-3 leftover) is REMOVED — required for the
    widening: with owned grow, the old raw-buffer alias dangles the moment a
    later insert grows the map (empirically pinned: the alias probes read
    garbage len under clamp+owncols). A string-column expression read now
    takes the RETAINING snapshot (`__fern_map_snapshot_col_str`); the copy +
    its per-element incs leak at that position (bounded per read, the same
    class as the scalar expression-position copy — a SARR-credited release is
    still the follow-up), and it fixes the native divergence (native
    keys()/values() are snapshots; the alias saw in-place len drift).
  SOUNDNESS: exactly the slice-1+2 argument extended — no read of an owned
  map's columns can alias its raw buffers (i32 → flag-0 copy, string →
  flag-2 retaining copy), so the grow-free never dangles a read; the
  pre-existing generic-view hole (`Map[K, V]` signature reads = flag 1, no
  owncols at ITS set sites, but a concrete-typed set elsewhere can still grow)
  is unchanged in kind and still unreachable in-tree. VERIFIED:
  TestSelfHostMapStrColOwncolsIRX86_64 (strval/strkey/strkv grow-churn
  heap-bump fixpoints N=50 == N=5000; keys()/values()-before-grow snapshot
  probes — the old revert-reason UAF shape — value-correct; strkv + i64
  churn correctness; an owncols-flag asm grep pinning bit 1 SET for
  string-column maps and CLEAR for i64-value maps; fixed cases double-checked
  against the native `-interp` oracle differentially) + the pre-existing map
  suites (fixpoint/vs/ks/snapshot/reclaim, x86 + wasm) + the byte-identical
  self-compile fixpoint. Probe lesson recorded: a `m.get_or("k" + "0", ..)`
  computed string LOOKUP key leaks its concat temp per call (pre-existing,
  unrelated to columns — it drowned the first grow-probe draft; slice 4's
  counted-reads design is where lookup temps get owners). Refs #5335 #4353
  #4451.

- 2026-08-07: **State of goal 2, and the traps this area sets.** Consolidated
  here out of `CLAUDE.md`, which had accumulated it inline.

  **Reuse is substantially complete.** Constructor reuse is implemented and
  enabled in the self-host — self-overwrite, cross-local, enum-donor,
  consuming-match, tuple reuse, nested-struct fields — and exercised by the
  byte-identical self-compile. See `docs/SELFHOST-PERCEUS-REUSE.md`'s correction
  header. The remaining REUSE deltas are marginal: struct reuse with enum / Map
  / closure / tuple pointer fields (§3 Delta B there).

  **"Marginal" describes reuse only — the RECLAIM side is where goal 2 is not
  done.** #6127 measured seven unbounded leaks the self-host had and native did
  not, via the `FERN_LEAKCHECK=1` differential: ~173 KB across six shapes at that
  sweep, every one scaling linearly with the round count. **#6127 is now CLOSED**
  — all seven shapes measure zero (#6218 / #6225 / #6232 / #6240 / #6251 / #6252
  / #6255 / #6263 / #6274 / #6285 / #6291 / #6308 / #6319 / #6336 / #6347 /
  #6375). It is no longer the live list.

  **The live list is #6360, now down to its STRING rows.** The class was "an
  enum local bound from a CALL is never reclaimed" — `frees=0`, exactly x2.0 per
  doubling, native clean on every row. Most of it has closed: #6392 / #6416 took
  the scalar payload, #6451 the rc payload bound from a call and consumed by a
  match, and #6448 the rc-payload local that no match consumes.

  **#6448 covers the DIRECT init only — measured, because its title reads
  wider.** `var o: Option[i32[]] = Some([..])` with no match is reclaimed
  (`frees=798/800`); the same shape bound from a producer call is untouched
  (`frees=0`, 35200). So "no match" and "call init" are ORTHOGONAL dimensions of
  one class, and only three of their four cells are closed.

  What that left is best read as a 2x2, because "rc payload leaks" is the wrong
  summary and cost a wrong turn before it was measured:

  | payload | init | before #6451 |
  |---|---|---|
  | rc | direct | 0 — already deep-freed, buffer and all |
  | rc | call | 35200 — closed by #6451 |
  | scalar | direct | 0 |
  | scalar | call | 0 — closed by #6392 / #6416 |

  The rc payload was never what defeated reclaim. `rcpayload_option_cand` reads
  the CONSTRUCTED variant off the init: `Some([..])` is visible, `mk(i)` is not,
  and `emit_opt_payload_drop` then reads offset 8 unconditionally — sound only
  because a specific variant was admitted. `emit_opt_tagged_payload_drop` guards
  that same release on `op_opt_tag() == 0`.

  **One class, two ORTHOGONAL dimensions — one of the two now half-closed.**
  Measured at the merge of #6451 and #6448, `Result[i32[], E]` from a call, 100
  rounds x 4. The success payload is `i32[]` throughout, so this table is
  unaffected by the string-SUCCESS-payload close below:

  | Err | consumed by a match | self-host |
  |---|---|---|
  | scalar | yes | **0** — closed by #6451 |
  | scalar | no | 35200, `frees=0` |
  | string | yes | 35200, `frees=0` |
  | string | no | 35200, `frees=0` |

  So the two dimensions were:

  1. **no consuming match**, for a CALL init — still open. #6451's admission
     requires a `sole_top_level_match_idx`, and #6448's no-match class does not
     admit a call init, so neither reaches this cell. `sole_top_level_match_idx`
     is also what leaves the INDENTED spelling out: a call-init string Option
     whose match sits inside an `if` is 800 / `frees=0` where the same match at
     top level is clean.
  2. **a string payload** — split by WHERE the string sits, and only the
     SUCCESS half is closed.

  - **Success payload — CLOSED.** `Option[string]` / `Result[string, <scalar>]`
    from a call went 800 `frees=0` → 800/800, and the fn-scoped spelling
    200 `frees=0` → 200/200. `rcpayload_option_call_ptype` now admits a `string`
    success payload, and `emit_opt_tagged_payload_drop` takes the payload helper
    as a parameter (`__fern_str_free` instead of `__fern_rc_dec`) rather than
    growing a fourth near-identical emitter.

    The freshness proof was already computed and then dropped on the floor:
    `opt_fresh_ret_fns_of` records the `"f"` flag per producer, but both name
    extractors stripped it, so lower_func could not see it. It is now seeded as
    `"OPTFRESHF:<name>"` beside `"OPTFRESH:<name>"` — a separate prefix, because
    the existing lookups are exact-match and `"OPTFRESHF:"[0:9]` is `"OPTFRESHF"`,
    not `"OPTFRESH:"`, so neither prefix test can see the other's rows.

    **The refusals are the load-bearing half.** `op_opt_make` stores the payload
    UNCOUNTED, so a producer returning `Some(pre)` for a live local — or worse,
    `Some(s)` for a caller's parameter — is flagged `"a"` and must stay refused:
    freeing there DANGLES rather than leaks. Both shapes are pinned as refused in
    `TestSelfHostCallBoundStrPayloadReclaimX86_64`, which asserts every row's exit
    code against `fern -interp` so a dangle surfaces as a wrong answer rather than
    quiet corruption.
  - **Err payload — CLOSED by #6463**, and not the way this note predicted. It
    assumed a string Err needed its own freshness proof plus a second arm on the
    tag guard. It needed neither: the guard frees the payload only under
    `tag == 0`, so a non-scalar Err is never reached — stranded, not dangled —
    and the Err gate was simply dropped. The `"f"` flag not covering Err costs
    nothing, because the success payload is the only one the drop touches.

    Worth keeping as method: the gate that looked load-bearing was refusing the
    whole shape, so it leaked the BOX as well as the payload it was protecting.
    Check what a refusal actually costs before treating it as the safe default.

    Do not re-derive the `Option[string]` numbers from the concat spelling: that
    probe reads as "reclaims partially, 800 of 2000" because the concat's
    intermediate temporaries are freed by other machinery, which masks the boxes
    never being freed at all. The literal spelling is the honest signature.

  **Checked, so it does not get re-litigated: an ALIASED payload through a
  producer is sound.** `function mk(a: i32[]) { return Some(a); }` called on a
  live `shared` array admits under #6451 (OPTFRESH proves the return is a direct
  ctor; it says nothing about the argument), and the unconditional `rc_dec` on
  the payload is nonetheless balanced — interp / native / self-host all exit 10
  with `allocs=51 frees=51 live_bytes=0`, and `shared` reads back intact after
  the loop. So the array ctor takes a counted reference. Strings are the shape
  where that is NOT assumed, which is exactly why the direct path gates them.

  **That issue's own attribution is wrong, and the correction is the useful
  part.** It concluded "call-binding is the trigger" and "the match is
  irrelevant" — both measured only on the call-bound rows, where both hold.
  Varying the match on the DIRECT row inverts it:

  | shape | binding | match | self-host |
  |---|---|---|---|
  | `Result[i32[], string]` | direct `Ok([..])` | yes | 0 |
  | `Result[i32[], string]` | direct `Ok([..])` | **no** | **35200** |
  | `Result[i32[], string]` | call | yes | **35200** |

  The consuming match is the ENTIRE reclaim mechanism for an rc-payload
  Option/Result local; call-binding is one of two independent ways to fall
  outside it. The emitted asm agrees — two `__fern_arr_dec` in `round()` on the
  direct+match build, no release at all in the others.

  **The uncovered quadrant is the non-reassigned local that no match consumes.**
  `consumed_rcpayload_option_frees` needs a sole consuming match AND a
  statically known variant (a call's variant is not, which is the same reason
  `fresh_scalar_option_call_init` restricts to scalar payloads);
  `collect_fresh_optarr_names` ("OPTARR:") deliberately requires REASSIGNMENT,
  as the complement of the match analyses. Closing it needs either a runtime
  variant guard in the payload drop or a new exit-sweep credit — and the latter
  is the operation that segfaulted gen1 twice, so the fixpoint runs first.
  All three rows are pinned in `TestSelfHostCallBoundEnumReclaimX86_64`,
  including the direct+match control, so a fix cannot land unnoticed the way
  #6291's did.

  **The block-scoped bare-name struct credit is CLOSED** (#6375 then #6408).
  The paragraphs below describe the investigation while the field drop was
  still withheld; #6408 landed the fix they point at — `expr_unsafe_for` and
  `moves_fields_expr` now read a bare non-scalar `name.field` as a MOVE rather
  than a borrow. Kept for the method, not as a live item.

  (Historical, #6375: the BOX was reclaimed and the field drop was not.) Three fixpoint runs, one
  variable each, located the culprit as `__struct_drop_<T>` on a block-scoped
  slot — not the box dec, not the entry-zeroing, not the credit's other
  consumers. Entry-zeroing alone is green (402 s); box-only free is green
  (390 s); adding the field drop segfaults gen1. The remaining 4000 is the field
  buffers.

  Two theories are eliminated. (1) The other twelve consumers of
  `slot_is_reclaimable_struct` — scoping the sweep + entry-zeroing and leaving
  them alone still segfaults. (2) NODEEP visibility, i.e. that `slot_nodeep`'s
  exact-name lookup silently loses the marker for a retired slot — making that
  lookup prefix-aware is inert (every consumer emits while the name is live),
  and with the marker visible, granting the deep drop STILL segfaults.

  **The actual cause is known**, from a stack trace rather than a guess: gen1
  faults in `asmcore.EmitState.has_need` → `__fern_str_eq`, reading a freed
  string out of `needed`. The shape is `var lo: StringLitOut =
  add_string_lit(s, ..); s = lo.state;` in
  `asm_ir.emit_function_via_ir_pre` — `lo` is block-scoped and its `EmitState`
  FIELD is moved into the live threaded `s`, so the deep drop frees that state's
  arrays out from under it. `ShapeRefOut` / `ConstAggOut` are identical shapes.
  Both escape analyses miss it for the same reason: `expr_unsafe_for` returns
  false for `name.field` ("base read is a borrow" — true for a scalar field,
  wrong for a nested-struct one), and `moves_fields_expr`, the "NODEEP:"
  detector, has the same hole. Closing it needs the move detector to know the
  FIELD TYPE (it takes no `structs` today); marking every bare field read
  conservatively would withhold deep drops far beyond this class.

  **Reproduce in seconds, not the 211 s fixpoint**: grant the drop, build gen1 as
  `runEmitAllFixpoint` does, and run it under gdb with `-per-module-emit-all
  -assume-eligible -unit-range 0:8`.

  ### Traps, all paid for

  **Re-measure before quoting any figure in this area — SIX attributions have
  been wrong.** Four of #6127's own: three because a sub-shape (single bind /
  declared-in-loop / rebound) went unprobed, and one because the leaked OBJECT
  was misnamed — `optstruct_single` was recorded as a leaked struct box and was
  the array FIELD buffer, identical in size at the probe's shape, so it read as
  a missing dec when it was a missing `__struct_drop_<P>`. The fifth was a
  recorded NEXT STEP mistaken for a measurement: `ARRSTRUCT:` / `ARRTUP:` were
  noted as leaking 35200 / 32000, with "check whether the slot's `struct_type` /
  `arrarr_elem` column is set at a nested binding" as the lead. Both are 0 at
  HEAD, closed by #6291 (`.len()` is a borrow, not an escape) as a side effect of
  a fix aimed elsewhere — and the column was never the cause, since `.len()`
  marked the local as escaping, so no credit was earnable however the columns
  were set. Following that lead would have cost a day in the wrong predicate.
  The sixth: #6285 attributed the block-scoped struct segfault to the credit's
  other consumers, and #6375's bisect showed it is the deep field drop alone —
  the difference between "this class is entangled" and "one operation is
  unsafe", and only the second is actionable.

  **A leak this family hides well: the shape reclaims until you INDENT it.**
  #6319 found that a `match (o)` scrutinee reads as an escape (`expr_unsafe_for`
  flags any bare ident), and the analysis that disagrees skips the consuming
  match by top-level statement INDEX — so it only ever reached a match that IS a
  top-level statement. The same Option consumed by a match inside an `if` or a
  `while` released NOTHING (8000–12800 per 100 rounds), while the top-level
  spelling was flat at 0. When a probe reclaims cleanly, try it one block deeper
  before concluding the class is closed.

  **The probe corpus cannot see the segfault class at all.** All 162 differential
  programs agreed with native on both the segfaulting and the green build in
  #6375; only the self-compiler shows it. That is the concrete case for running
  the fixpoint FIRST on a reclaim change.

  **Vary one dimension at a time** (array length vs field count; access pattern
  vs binding form) and re-measure before naming what leaked — it costs one
  build. And a fix that lands incidentally needs a pin or it regresses silently,
  which is why the two shapes above are now cases in
  `TestSelfHostBlockScopedClassesX86_64`.

- 2026-08-09: **#6360's rc-payload call-bound rows — CLOSED, both of them.**
  The 2026-08-07 entry's table above is a snapshot at `09b3efe2`; the two rows
  it records as 35200 are now 0. Current state of the family, 100 rounds x 4
  iterations, `FERN_LEAKCHECK=1`, every exit code matching `fern -interp`:

  | shape | binding | match | scope | self-host |
  |---|---|---|---|---|
  | `Result[i32[], string]` | direct `Ok([..])` | yes | fn | 0 |
  | `Result[i32[], string]` | direct `Ok([..])` | no | fn | 0 |
  | `Result[i32[], string]` | call | yes | fn | 0 |
  | `Result[i32[], string]` | call | no | fn | 0 |
  | any of the above | — | — | **loop** | **8800** |

  Two one-line gates, each refusing a shape the free was already safe for:

  1. `rcpayload_option_call_ptype` required a SCALAR `Err` payload, which
     excluded `Result[T[], string]`. `emit_opt_tagged_payload_drop` frees the
     payload only under `tag == 0` and the box on every path, so a non-scalar
     `Err` payload is **stranded, not dangled** — refusing the shape leaked the
     box as well as the payload.
  2. `unmatched_optarr_init_is_fresh` took literal constructors only, so an
     OPTFRESH-registered producer call fell through both the unmatched credit
     and the match analysis and reclaimed nowhere. Registry membership is the
     same freshness proof `rcpayload_option_call_ptype` uses.

  **The freshness proof is about the BOX, not the payload**, and that is worth
  stating because it looks like a hole. `opt_fresh_ret_fns_of` admits
  `Ok(<param array>)` — a direct constructor — so the payload dec can land on a
  buffer the caller also holds. It does not double-free: the caller's buffer
  reaches the producer as an ARGUMENT, which the caller-side escape analysis
  already reads as an escape, so it carries no second credit to collide with.
  Measured 800/800 rather than argued; pinned as
  `optarr_aliased_payload_balances`, which asserts the balance in BOTH
  directions so a future widening cannot turn the strand into a double free.

- 2026-08-09: **#6360's last row — CLOSED. The issue is done.** Every
  loop-DECLARED shape sat at 8800 = the final iteration's box+payload. The exit
  sweep was not missing the SLOT, it was missing the NAME:
  `slot_is_reclaimable_optarr` looked its credit up under
  `s.locals[i].slot_name` verbatim, and a block-scoped slot has been renamed
  `retired: <name>` by then. `reclaim_slot_name` strips that prefix and the
  arr-of-arr sibling already went through it; routing OPTARR through it too is
  the entire fix. Both loop-scoped rows 800/600/8800 -> 800/800/0.

  **This is the block-scoped-slot class that segfaulted gen1 twice** (#6285 /
  #6375), so the fixpoint ran first and is green (342 s). That does not
  contradict the #6375 bisect, it locates it: what segfaulted there was the deep
  FIELD drop (`__struct_drop_<T>` walking a field that had been moved into a live
  local), and the box-only free was already green. OPTARR's payload release is
  one flat `__fern_arr_dec` on a scalar buffer with no field walk, so it sits on
  the green side of that line. **The distinction is deep-walk vs flat dec, not
  block-scoped vs not** — the scope was never the hazard.

  The two pins moved from asserting 8800 to asserting an exact balance rather
  than `live_bytes == 0`: on a retired slot an over-release matters as much as a
  leak, and only `allocs == frees` catches both.

- 2026-08-09: **The call-bound escape gate matched NOTHING, and #6469 turned that
  into a dangle.** `opt_arm_binding_escapes` compares the arm's variant SPELLING
  against the name its caller passes. #6451 introduced the call-init candidate
  with a hardcoded `variant: "Ok"` and a comment claiming "Ok covers Some too —
  matches the arm by payload position, not by spelling". It does not: `"Ok"` never
  equals `"Some"`, so for every `match (v) { Some(s) => .., None => .. }` the gate
  walked the arms and refused nothing.

  That was survivable while the payload was an ARRAY, because an escaping arm
  binding RETAINS — `held = xs` incs the buffer, so the drop's dec is balanced.
  #6469 then admitted STRING success payloads to the same drop, and a string
  assignment is a BORROW. With the gate matching nothing, `held = s` left
  `__fern_str_free` releasing a box the caller still reads. Measured on main at
  `53af8cb`: exit **178** against native and `fern -interp`'s 55.

  FIX: the string rows pass `"Ok|Some"` — the SUCCESS arm under either spelling.
  Two boundaries, both measured rather than argued:

  - NOT applied to the array rows. Doing so refuses a shape that is already
    correct: the escaping-array probe drops from 1600/1600 to 800/1600.
  - NOT "every arm". A scalar `Err(e)` used in arithmetic reads as an escape,
    which strands `Result[string, i32]` at 22400. A scalar binding is never a
    payload this drop releases.

  **THE FAILURE IS INVISIBLE TO A PLAIN PROBE, WHICH IS WHY THREE PASSES MISSED
  IT.** With the bug present the escaping shape still exits correctly — the freed
  box is simply not reused before it is read. It becomes observable only once
  same-shaped strings are churned in between, recycling it. `__rc_underflow_count()`
  stayed 0 throughout and never saw it: on the `__fern_str_free` path it is a
  supporting check, not the discriminator. Any hazard probe for a string payload
  needs a churn loop before the aliased read, or it reports a pass.

  The complement is worth stating because it is what made the class look safe:
  #6469's suite pins the PRODUCER-side aliases (payload aliases a live local or a
  parameter), which its "f" flag does speak to. The flag proves the producer built
  a fresh payload and says nothing about what the CONSUMER's arm then does with
  it. Freshness and escape are orthogonal, and only both together make the drop
  sound.

  VERIFIED: `TestSelfHostOptArmEscapeGateX86_64` (new — both spellings refused,
  borrow-only still reclaims, the array row pinned at 1600/1600, the scalar-Err row
  pinned balanced), #6469's `TestSelfHostCallBoundStrPayloadReclaimX86_64` green
  unchanged, and `TestSelfHostPerModuleEmitAllFixpointX86_64` (382 s, run first).
  Refs #6360 #4451.

- 2026-08-09: **The unmatched quadrant, STRING payload — `Option[string]` /
  `Result[string, _]` that no match consumes now reclaims.** #6463 closed that
  quadrant (never reassigned, never matched — so neither
  `collect_fresh_optarr_names` nor `consumed_rcpayload_option_frees` looks at the
  local) for ARRAY payloads. Varying the payload and holding everything else
  fixed shows the string dimension was still open:

  | shape, no consuming match | self-host | native |
  |---|---|---|
  | `Option[i32[]]`         | 0     | leaks 6400 |
  | `Result[i32[], i32]`    | 0     | 0 |
  | `Result[i32[], string]` | 6400  | 0 |
  | `Option[string]`        | 22400 | leaks 6400 |

  FIX: a `"OPTSTR:"` credit rather than widening `"OPTARR:"` — the release is
  `__fern_str_free`, and a string box's data buffer is separate with a different
  block class, so the array dec would free it wrongly.

  **Freshness is load-bearing here in a way it is not for the array sibling.**
  That one leans on the caller-side escape analysis reading an argument as an
  escape, so an aliased buffer carries no second credit to collide with. A string
  gets no such cover: `op_opt_make` stores its payload uncounted and a string
  assignment is a borrow, so an aliased payload would be released under a live
  reference. Admission demands a literal or a syntactically-fresh producer inline,
  and the registry's "f" flag for the call form.

  **THE EXIT SWEEP ALONE IS NOT ENOUGH, and the half-fix reads as progress.** A
  loop-declared `var v` re-stores to the SAME slot each iteration, so a
  function-exit sweep releases only the final value and every earlier iteration
  still leaks. Sweep-only took 22400 to 18400 — a plausible-looking improvement
  that is really three quarters of the leak still present. The store is where the
  previous value has to go (`emit_optstr_reclaim_store`), exactly as the array
  class already does. This is also the likely origin of the old "8800
  final-iteration residue" note: a sweep-only credit leaves precisely that shape.
  That note is otherwise NOT reproducible — four loop-declared variants (direct
  ctor, call-bound, struct payload, string payload) all measure 0.

  MEASURED: `Option[string]` unmatched 22400 -> **0** with `allocs == frees`, flat
  at N=50/100/200/400, where native leaks 6400. Inline-concat, bare-literal and
  `Result[string, i32]` spellings likewise 0. The array control is untouched.
  Hazards refuse and leak rather than dangle, exits matching `fern -interp`: an
  aliased producer payload, an inline ctor over a bare local, and a box read after
  the loop.

  SCOPE: `Result[i32[], string]` strands its Err strings (6400) and stays open —
  the "f" flag describes the SUCCESS payload only, so releasing an Err string
  needs its own whole-body verdict.

  VERIFIED: `TestSelfHostUnmatchedOptStrReclaimX86_64` (new),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (393 s, run first), and the #6360
  neighbours — call-bound enum, call-bound string payload, and the arm-escape gate.
  Refs #6360 #4451.

- 2026-08-09: **The Err arm's string, stranded by an empty else-branch — released
  on the match-consumed path.** Every tag-guarded Option/Result drop frees the
  payload under `tag == 0` and the box on every path, with NOTHING in the else.
  #6463 made that explicit when it dropped the scalar-Err gate: "a non-scalar Err
  payload is never reached — it is stranded, not dangled". Stranded is sound, and
  it is still a leak: `Result[i32[], string]` from a call left 6400 over 100
  rounds, x2.0 per doubling, against 0 on native.

  **The proof the registry did not have.** The "f" flag is computed from the
  SUCCESS payload only (`body_has_nonfresh_opt_success_payload`), so it cannot
  vouch for an Err string, and reusing it would release a caller's box on the Err
  path. The walker is now parameterised by which constructor it inspects — one
  traversal, two verdicts — and the Err verdict takes its own tagged registry row
  (`ERRFRESH:`, seeded `OPTERRFRESH:`). Tagged rather than a second flag character
  so every existing `name|ptype|flag` lookup is untouched.

  MEASURED: `Result[i32[], string]` from a call with a match 6400 -> **0**,
  `allocs == frees`, flat at N=50/100/200/400. Err-always and bare-literal
  spellings likewise 0. The aliased-Err hazard — a producer returning its own
  `held: string` parameter — is refused and stays stranded at 3200, exit matching
  `fern -interp`; the "f" flag WOULD have admitted it, which is the whole reason
  the Err verdict is computed separately.

  **HALF the class, deliberately, and pinned as such.** The same strand exists in
  the UNMATCHED quadrant, which reaches `emit_optarr_deep_free` rather than
  `emit_opt_tagged_payload_drop`. Both measure identically (3200 / 6400 / 12800 /
  25600 at N=50/100/200/400) — which is what first suggested one defect — but they
  are separate emitters, and the unmatched one is SHARED with the reassigned+match
  OPTARR class, so an unconditional Err branch there would fire for slots that
  never earned the verdict. Releasing it needs a per-slot credit of its own.
  `unmatched_err_string_still_strands` asserts the remainder AS a leak so it stays
  visible and measured rather than becoming folklore.

  VERIFIED: `TestSelfHostOptErrStringReleaseX86_64` (new),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (run first), and the #6360
  neighbours — unmatched optstr, call-bound string payload, the arm-escape gate,
  call-bound enum, rcpayload option call. Refs #6360 #4451.

- 2026-08-09: **The Err arm's string in the UNMATCHED quadrant — the other half,
  now closed.** The entry above released the Err payload on the match-consumed
  path and pinned this one AS a leak. Both measured identically
  (3200 / 6400 / 12800 / 25600 at N=50/100/200/400), which is what suggested a
  single defect; they are separate emitters, and that distinction is the whole
  design of this fix.

  `emit_optarr_deep_free` is SHARED with the reassigned+match OPTARR class, whose
  slots carry no Err-freshness proof, so an unconditional else-branch there would
  dangle. The release is therefore gated per-SLOT by a second credit,
  `"OPTARRERR:"`, seeded next to `"OPTARR:"` only when that local's declared Err
  payload is a string AND its init proves every Err payload fresh — an inline
  `Err(<literal / fresh producer>)`, or a call to an ERRFRESH-registered producer.
  `unmatched_optarr_err_str_fresh` is deliberately per-LOCAL for that reason.

  MEASURED: `Result[i32[], string]` unmatched 6400 -> **0**, `allocs == frees`.
  The match-consumed row, both scalar-Err rows, the unmatched `Option[string]`
  row, the arm-escape hazards and the aliased-Err hazard (still refused, stranded
  at 3200) are all unchanged.

  The pinned `unmatched_err_string_still_strands` case was CONVERTED to an
  exact-balance assertion rather than deleted, which is what its own failure
  message instructed. That is the pattern worth repeating: when a known-open half
  is asserted as a leak, its message should say how to close the pin, so the next
  change updates it instead of quietly dropping the coverage.

  VERIFIED: `TestSelfHostOptErrStringReleaseX86_64`,
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (354 s, run first), and the five
  #6360 neighbours. Refs #6360 #4451.

- 2026-08-09: **The consuming match one block deeper now reclaims (#6319's
  class).** `sole_top_level_match_idx` scanned only the flat statement list, so
  the same `match (v)` moved inside an `if` released NOTHING while the flat
  spelling was flat at 0. Measured at `d672c3da`, 100 rounds x 4, every exit code
  matching `fern -interp` and native:

  | shape | flat | nested in `if` |
  |---|---|---|
  | `Result[i32[], string]` from a call | 0 | **35200**, `frees=0` |
  | `Option[string]` from a call | 0 | **25600**, `frees=0` |
  | …the first row at 200 rounds | 0 | **70400** — a clean x2 |

  `sole_consuming_match_idx` returns the ENCLOSING top-level statement — where
  the free lands, and what the liveness and escape checks already skip — and
  `consuming_match_of` re-derives the match for the arm analyses. Both are needed
  and they are not interchangeable: **feeding the enclosing `if` to the arm
  analyses is a use-after-free, not a missed reclaim.** `match_arms_use_name` and
  `opt_arm_binding_escapes` both answer "nothing escapes" for a statement they
  cannot parse, so the blind spot reads as a proof. Mutation-checked — with the
  enclosing statement passed instead, the escaping-arm probe returns 46 against
  the oracle's 24 and `frees` jumps 898 -> 1048, while every positive row still
  reclaims 800/800, so the mutation removes only the refusal.

  A nested match is admitted only when the scrutinee is the local's ONLY mention
  inside the enclosing statement. The caller skips the WHOLE statement in its
  escape check, so a mention in the `if` cond, the sibling branch, or a later
  statement of the same branch would be invisible to it; all three are pinned as
  refusals. Counting statements that CONTAIN a match, not just those that ARE
  one, is also load-bearing: a name matched both flat and nested yields two hits
  and is refused rather than freed after the first.

  **CALL-INIT ONLY, and that boundary is the whole disjointness argument.** A
  DIRECT ctor consumed by a nested match is already reclaimed by
  `precise_drop_names` — its `is_rcopt` kind admits exactly
  `rcpayload_option_cand != ""` and defers to this analysis only on a FLAT match
  (`body_has_top_level_match`). Widening the lookup for the direct ctor too issues
  a SECOND credit on the same box. That is not theoretical: it segfaults, and
  `TestSelfHostNestedMatchBorrowNoUnderflowX86_64` reports
  `__rc_underflow_count() == -1`. The two admissions are exact complements, so
  keeping the direct ctor on the flat lookup is what keeps them disjoint — and the
  block-scoped direct-ctor shape stays at 35200, unchanged from before, because
  `precise_drop_names` runs over the fn's top-level statements and never reaches
  it. That row is still open, and it belongs to precise-drop, not here.

  The trap generalises: **a nested-match widening is only ever half a change.**
  The other half is asking which analysis was already covering the shape, because
  the leak and the double free look identical from the widened side — both are
  "my credit fired". Only the underflow counter tells them apart, and only when
  the shape reaches it.

  **Still flat-only, and measured rather than assumed:** the SCALAR-payload
  classes (`fresh_scalar_option_call_init` / `consumed_scalar_enum_frees`) keep
  their own `sole_top_level_match_idx` lookups. `Option[i32]` from a call nested
  in an `if` leaks 16000 with `frees=0`, identically before and after this change
  — a different call site, untouched here, and the next row of this class.

  VERIFIED: `TestSelfHostNestedMatchReclaimX86_64` (new — four reclaim rows with
  flat controls, three refusal rows, and the churn-based escape probe),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (421 s, run FIRST — gen0 == gen1
  across 35 units), plus #6467's and #6469's suites green unchanged. Refs #6319
  #6360 #4451.
- 2026-08-09: **#6360's leak list, RE-DERIVED by measurement, re-confirmed at `7014ae2`.** The
  call-bound string class and both quadrants of the Err-string class are closed
  (#6467 / #6476 / #6483 / #6488), so the old list describes nothing. This is what
  a fresh differential sweep actually finds. Every row is `FERN_LEAKCHECK=1`,
  100 rounds x 4 iterations, exit codes equal to `fern -interp` and to native.
  Re-measured after #6480 widened the consuming-match class: every figure below
  is unchanged by it, and both clean controls stay clean.

  **The 75-probe scratchpad corpus is EXHAUSTED and clean.** Zero exit-code
  divergences — no use-after-free survives anywhere in it. Every leak it still
  reports is a hazard probe written to be REFUSED: box-aliased-outward (32800),
  escaping arm binding (31200), aliased payload (19200). Those are the
  conservative side of gates that would dangle if widened. They do mark a real
  parity gap — native reclaims all three — but closing them needs genuine liveness
  inference, not another syntactic credit. Do not read them as defects.

  **New classes, from probing shapes the corpus never covered.** Ranked, all
  exactly x2.0 per doubling:

  | shape | self-host | frees | native |
  |---|---|---|---|
  | `Option[string[]]`                | 57600 | 1600/4000 | 0 |
  | `Option[P]`, P has a string FIELD | 48000 |  800/2400 | 0 |
  | `Option[Option[i32[]]]`           | 48000 |    0/1200 | 0 |
  | `Result[string[], string]`        | 43200 | 1200/3000 | 0 |

  The first, second and fourth share one cause: **a payload that needs a
  per-element or per-field STRING release**, where the existing drops do a single
  `__fern_rc_dec` (flat buffer) or a single `__fern_str_free` (one string) and walk
  neither a `string[]`'s elements nor a struct's string fields. `Option[P]` is the
  near-miss — `emit_opt_struct_payload_drop` / `opt_payload_deep_sty` already
  handle a struct payload with rc-ARRAY fields, and simply do not admit string
  fields. The third is a different cause: `frees=0`, so the credit is unreachable
  rather than partial — `rcpayload_option_cand` has no branch whose payload type is
  itself an Option.

  **Controls that are CLEAN, recorded so nobody re-probes them**: `Option[i32[][]]`,
  `Option[i32[]][]`, `Option[(i32, i32[])]`, a bare `string[]` local, and a bare
  struct-with-string-field local. The leak needs BOTH the Option wrapper and a
  string-carrying payload; neither alone reproduces it.

  Refs #6360 #4451.


- 2026-08-09: **The scalar Option consumed by a nested match — precise-dropped
  now (#6319's scalar arm).** #6127 gave the consuming match the
  scrutinee-is-a-borrow reading but wired it to the rc-PAYLOAD kind alone,
  deliberately ("widens exactly one class at a time"). For a SCALAR Option the
  coarse `body_unsafe_for` still read the scrutinee as an escape, so
  `precise_drop_names` refused, and nothing else claimed the shape:
  `consumed_scalar_enum_frees` finds its consuming match by top-level statement
  INDEX and cannot see one nested in an `if`. Measured at `7014ae29`, 100 rounds,
  `__rc_underflow_count()` as the return value:

  | `Option[i32]`, fn-scoped | before | after |
  |---|---|---|
  | match nested in an `if` | 4000, `frees=0` | **0** |
  | match nested in a `while` | 4000, `frees=0` | **0** |
  | flat control | 0 | 0 |
  | option read again after the `if` | refused | refused |

  The fix is one line — `is_opt` joins `is_rcopt` on the borrow reading — and it
  is the right lever precisely because **disjointness needs no new gate**:
  `is_opt` is only ever set when `!body_has_top_level_match`, so precise-drop
  takes the shape exactly when the flat analysis cannot, and vice versa. The
  alternative (widening `consumed_scalar_enum_frees`' lookup to nested, the way
  #6480 did for the rc payload) would have needed its own complement argument, and
  doing BOTH would put two credits on one box.

  **Still open, measured on base rather than assumed:** the same shape from a CALL
  (`var o: Option[i32] = mk(i)`) stays at 4000 — `is_opt` admits only
  `fresh_scalar_option_init`, and `precise_drop_names` takes no `opt_fresh`
  parameter to consult the registry with, so covering it means threading one
  through and re-deriving the disjointness. And the BLOCK-scoped spellings
  (declared inside a loop) stay at 16000 whatever the init, because
  `precise_drop_names` runs over the fn's top-level statements and never reaches
  them.

  VERIFIED: `TestSelfHostScalarOptNestedMatchX86_64` (new — both nestings, the
  flat control, and the read-after-the-`if` refusal),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (412.96 s, run FIRST), and 25
  targeted suites 0 skips, including every nested-match, precise-drop and
  scalar-enum neighbour. Refs #6319 #6360 #4451.

  NOTE the probes here return `__rc_underflow_count()`, so `fern -interp` is NOT
  an oracle for them — it has no rc runtime and exits 1 on every one. The counter
  is the detector; comparing against interp reports a false failure.

- 2026-08-09: **The scalar Option from a CALL, nested match — the row #6503 left
  open.** #6503 gave `is_opt` the scrutinee-is-a-borrow reading but it still
  admitted only `fresh_scalar_option_init`, so an OPTFRESH-proven producer call
  stayed at 4000. `precise_drop_names` now takes the registry (`opt_fresh`,
  supplied as `optfresh_call_names_of(...)` at both call sites) and admits
  `fresh_scalar_option_call_init` beside the inline ctor.

  | `Option[i32]`, fn-scoped, from a call | before | after |
  |---|---|---|
  | match nested in an `if` | 4000, `frees=0` | **0** |
  | flat control (the other analysis's half) | 0 | 0 |

  The disjointness argument is unchanged and is the whole reason this is safe:
  `is_opt` is only ever set when `!body_has_top_level_match`, and
  `consumed_scalar_enum_frees` already admitted BOTH inits. So the two analyses
  now cover the identical candidate set and split it purely on where the match
  sits — nested to precise-drop, flat to the index lookup. Both `*_flat_control`
  rows in the suite are there to catch that split failing, and only the underflow
  counter would show it.

  **Still open:** the BLOCK-scoped spellings (local declared inside a loop) stay
  at 16000 whatever the init, because `precise_drop_names` runs over the fn's
  top-level statements and never reaches them. That is a third mechanism, not a
  wider admission.

  VERIFIED: `TestSelfHostScalarOptNestedMatchX86_64` grew the call-init nested row
  and its flat control (6 rows now), `TestSelfHostPerModuleEmitAllFixpointX86_64`
  (417.75 s, run FIRST), 26 targeted suites 0 skips. Refs #6319 #6360 #4451.

- 2026-08-09: **The BLOCK-scoped scalar Option with a nested match — the last of
  #6319's four quadrants.** With #6503 and #6517 in, the shape's coverage was:

  | | flat match | nested match |
  |---|---|---|
  | fn-scoped | `consumed_scalar_enum_frees` | precise-drop (#6503/#6517) |
  | block-scoped | `lower_block`'s per-block re-run | **16000, `frees=0`** |

  `precise_drop_names` is only ever called with `fn.body`, so it can never reach a
  loop-declared local; `lower_block` already re-runs `consumed_scalar_enum_frees`
  per block but looked the match up by flat statement INDEX. So the last cell had
  no owner. `consumed_scalar_enum_frees` gains `nested_ok`, TRUE only from
  lower_block, and uses `sole_consuming_match_idx` / `consuming_match_of` (#6480's
  helpers) there. Both inits: 16000 -> 0, `allocs == frees`, underflow 0.

  **A guess this corrected.** The obvious justification for the flag — "passing
  true at fn scope would double-free, since precise-drop already claims that
  shape" — is WRONG, and the mutation says so: with both fn-scope call sites
  flipped to true, every row stays balanced with underflow 0, because the precise
  drop zeroes the slot and the second credit decs null. The flag is kept as a
  territory boundary, not as an over-release guard: two analyses silently claiming
  one local is how a real double free gets built later, and the redundant credit
  buys nothing precise-drop has not already done. Recorded because the comment
  that shipped would otherwise have asserted something measurement refutes.

  VERIFIED: `TestSelfHostScalarOptNestedMatchX86_64` at 8 rows (both scopes x both
  inits x nested/flat, plus the refusal),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (379.06 s, run FIRST), 26 targeted
  suites 0 skips. Refs #6319 #6360 #4451.

- 2026-08-09: **The block-scoped rc-payload direct ctor with a nested match — the
  rc analogue of the scalar row #6526 closed.** #6480 widened
  `consumed_rcpayload_option_frees`' match lookup to the nested spelling for CALL
  inits only, because at function scope a direct ctor is already
  `precise_drop_names`' is_rcopt candidate. A BLOCK-scoped local has no such
  owner, so the direct ctor leaked there: **35200 over 100 rounds, `frees=0`**,
  where every other spelling of the same shape was 0. Same `nested_ok` shape as
  #6526 — TRUE only from lower_block — now gating `call_pt != "" || nested_ok`.

  **The flag is LOAD-BEARING here, and that is the difference from #6526.**
  Flipping the fn-scope call site to true SEGFAULTS both
  `TestSelfHostNestedMatchBorrowNoUnderflow`'s program and the opt-struct-payload
  hazard, where the identical mutation on the SCALAR collector was harmless
  (every row stayed balanced, underflow 0). The reason: a scalar drop is a shallow
  box free, and slot-zeroing makes a second credit dec null; an rc-payload drop
  releases the PAYLOAD too, so the second credit double-frees it. Same-shaped
  flag, genuinely different justification — **do not carry the reasoning from one
  to the other**, in either direction. Both were measured.

  With this and #6503 / #6517 / #6526, #6319's four quadrants (fn/block scope x
  flat/nested match) close for the scalar Option and for the rc payload under both
  inits.

  VERIFIED: `TestSelfHostBlockRcPayloadNestedMatchX86_64` (new — direct-ctor
  nested, call-init nested, the flat control, and the escaping-arm refusal at
  801/401 which is measured, not assumed: an escaping ARRAY binding partially
  retains), `TestSelfHostPerModuleEmitAllFixpointX86_64` (380.38 s, run FIRST), 27
  targeted suites 0 skips. Refs #6319 #6360 #4451.

- 2026-08-09: **The fresh-owned-container READ reclaim, scalar half (#6491).**
  `mk()[i]` and `mk().f` read a value out of a container NOTHING NAMES, so the
  read is the only place it can be reclaimed — there is no slot for the exit
  sweep to find. The self-host leaked the whole container per evaluation.
  Measured through `bin/fern-selfhost`, x86-64, fresh bytes at 50 and 100 rounds:

  | shape | before | after | native |
  |---|---|---|---|
  | `lit(n)[2]` — scalar elem, array-literal producer | 2800 / 5600 | **56 / 0** | 32 / 0 |
  | `pair(n).k` — scalar field, scalar-only struct | 2400 / 4800 | **48 / 0** | 32 / 0 |
  | `bag(n).k` — scalar field, `i32[]`-field struct | 5200 / 10400 | **104 / 0** | 64 / 0 |
  | `nums(n)[1]` — scalar elem, LOCAL-built producer | 2824 / 5600 | 2824 / 5600 | 48 / 0 |
  | `boxed(n).k` — scalar field, string-fielded struct | 3600 / 7200 | 1248 / 2400 | 32 / 0 |

  The admission is **borrowed wholesale from the discarded-`mk(i);` statement
  reclaim** rather than invented: the callee must be in
  `return_fresh_struct_ret_fns` (bare entry for a struct, `"ARR:"` for a
  scalar-element array), so the returned box is the unambiguous sole owner of
  itself and of every field buffer. That is what makes the `__struct_drop_<T>`
  in the `bag` row safe; a looser "any struct-returning call" would free the
  caller's live buffer, which `borrowed-field-refused` pins by re-filling the
  freed buffer and reading it back.

  **SCALAR reads only, and that boundary is the whole reason this slice is
  self-contained.** A pointer element/field ALIASES the container about to be
  freed, so it needs three sites to agree — retain at the read, is_unique-gated
  deep drop, and a binding that takes the read as a MOVE rather than an alias
  (native's `isOwnedContainerRead` / `rhsTainted` pair). A scalar is copied out
  of the buffer, so it needs none of them.

  Two rows stay open, and the table above is what they cost rather than a guess:

  - **A LOCAL-built producer** (`var out = []; out = out.append(x); return out;`)
    earns no `"ARR:"` entry — that registry admits only a direct array-literal
    return. `local-built-producer-still-grows` asserts it AS a leak, with the
    `>` that must become `==` when the admission widens. (CLOSED by the
    2026-08-10 entry below.)
  - **The pointer half**, which is what `alloc_flat_index_of_fresh_container`
    actually measures: its element and field are strings AND its producers are
    local-built, so it needs both widenings. Its rows in all three
    `selfhost-*-known-divergences.txt` files say so now instead of describing
    the whole mechanism as missing.

  The `boxed` residual is NOT this shape: it is 24 B/round of const_str box, the
  pre-existing `var t: string = "abc"` leak, measured identically on a probe with
  no container read in it at all. The Box itself is reclaimed.

  VERIFIED: `TestSelfHostFreshContainerReadReclaimIRX86_64` (new — 5 rows),
  every case additionally run through the self-host on **arm64 (qemu) and wasm
  (wasmtime)**, where the leak reproduced identically on base (exit 92) and is
  gone after. `TestSelfHostPerModuleEmitAllFixpointX86_64` run FIRST, per the
  rule for reclaim changes. Refs #6491 #4451.

- 2026-08-09: **A closure field no longer sinks its whole struct array (#6461).**
  The issue asked which of two things the self-host was doing — refusing the
  `clofld` admission, or granting it and reclaiming incompletely. Neither. The
  admission was granted and the `k_clo` drop arm was ready; nothing ever called
  it, because the array holding the records was not in a reclaim class at all.
  `slot_is_reclaimable_structarr` gates the append-built class on a field
  allowlist that did not know `fn`, so one closure field dropped the `P[]` local
  to the generic shallow `__fern_rc_dec` — element boxes, strings and closures
  leaking together.

  The diagnostic that pins it is the same array with the field swapped: the
  `i32`-field control reclaims *perfectly* (`allocs=5100 frees=5100`,
  `live_bytes=0`), and adding one `fn` field takes it to `frees=2700`,
  `live_bytes=102400`. A leak proportional to the whole structure, not to the
  closure — which is what said to look one level up from `k_clo`.

  Two admissions widen. `fn` joins scalar / `string` in the shallow-free
  allowlist, on `string`'s own argument: what the class needs of a field type is
  that leaking it is sound, and when the type ALSO routes field reclaim the deep
  branch recovers both through `__struct_drop_<T>`, whose arms carry their own
  whole-program admissions. And `structarr_elem_store_ok` now admits a call into
  `return_fresh_struct_ret_fns` beside the no-base literal — strict-fresh proves
  the returned box is rc=1 and unaliased, the literal's freshness one frame out.
  A METHOD call still declines: the receiver type is unknown at the scan, so the
  `<Base>.<method>` key cannot be looked up and the credit is not granted on a
  guess.

  Both were needed for the issue's reproducer, which builds its elements with
  `mkP(i)`: `fn` alone fixes only the inline-literal spelling.

  | heap high-water, 100 vs 200 rounds | before | after |
  |---|---|---|
  | x86-64 | 102600 / 205000 | **1224 / 1224** |
  | arm64 | 102600 / 205000 | **1224 / 1224** |
  | wasm | 64080 / 128080 | **720 / 720** |

  Every before column is exactly 2.0x per doubling. Measured on each leg rather
  than inferred from x86-64 — this is an `irlower` change, so all three move
  together. Native was already flat on the same source and is untouched.

  **Found and NOT fixed here:** a lambda capturing a MUTATED loop variable
  snapshots it at construction on the self-host, where interp and native both
  read it at call time (`(cs[0].f)(0)` -> 0 vs 3). Reproduced on a compiler built
  from `main`, so it predates this and is not in its blast radius; whether the
  self-host or the other two are wrong may be a language-design call. Filed as
  #6539. The hazard cases added here deliberately use a non-capturing lambda or a
  parameter capture so they do not ride on that question.

  VERIFIED: `internal/e2eselfhost/self_host_closure_field_reclaim_test.go` loses
  its `t.Skip` (delta 0 B at both round counts), a new call-element reclaim test,
  three new hazard rows (passthrough callee, method call, closure read back out
  of a reclaimed array), `TestSelfHostPerModuleEmitAllFixpointX86_64` (764.61 s),
  and the arm64 + wasm structarr / arrstruct / elem-drop neighbours (316.56 s,
  0 skips). Refs #6461 #4451.

- 2026-08-10: **The LOCAL-built scalar-array producer — the pin the entry above
  left (#6491).** `var out: i32[] = []; … out = out.append(v); … return out;` is
  how a producer is actually written, and the `"ARR:"` rule declined it (direct
  array literal only), so the caller's reclaims left the buffer to leak.

  | shape | before | after | native |
  |---|---|---|---|
  | `nums(n)[1]` — scalar elem, LOCAL-built producer | 2824 / 5600 | **80 / 0** | 48 / 0 |

  `body_returns_local_built_arr` admits it by proving the buffer is built
  ENTIRELY in that frame, so it reaches the caller as its sole rc == 1 reference:
  a literal init (it cannot START as a param's buffer), self-append as the only
  rebind (it cannot BECOME one later), and `body_unsafe_for_allow_ret` for every
  other escape. Element freshness needs no proof — `is_leaksafe_array_field`
  makes every element a scalar, so the shallow element-blind `__fern_rc_dec` both
  consumers emit has no pointer to dangle.

  **`arr_reassigned_other_than_selfappend` is the load-bearing half, and it is
  not redundant with the escape walk.** `body_unsafe_for_allow_ret`'s StmtAssign
  arm inspects only the assigned VALUE, so `out = src` — seeding the local from a
  parameter — never mentions `out` and reads as safe there. Without the rebind
  scan the caller would free its own live buffer at the read.
  `param-seeded-producer-refused` pins exactly that, re-filling the freed buffer
  with 9s before reading the caller's array back, so a widened admission reports
  90 rather than passing by luck.

  This widens a registry the discarded-`mk(i);` statement reclaim also reads, so
  that site now reclaims the accumulator idiom too — the same shallow dec, the
  same soundness argument.

  **Still open:** the POINTER half, which is what
  `alloc_flat_index_of_fresh_container` measures. Its element and field are
  strings, so it needs the retain / is_unique-gated deep drop / move-at-the-
  binding trio that a scalar read does not.

  VERIFIED: `TestSelfHostFreshContainerReadReclaimIRX86_64` at 6 rows (the
  local-built row flipped from its `>` pin to `==`, plus the param-seeded
  refusal), every case additionally run through the self-host on **x86-64, arm64
  (qemu) and wasm (wasmtime)**, and
  `TestSelfHostPerModuleEmitAllFixpointX86_64`. Refs #6491 #4451.

- 2026-08-10: **RECLAIM — the consumed-match enum frees now run on the
  `return`-out-of-an-arm paths (#6219).** Every consumed-enum release was
  emitted AFTER its consuming `match` statement. When all arms of that match
  `return`, control leaves the function before reaching the release, so nothing
  was freed at all — one box per call, unbounded. Both families placed their
  frees that way and both leaked: `consumed_scalar_enum_frees` (scalar enum and
  scalar Option) and `consumed_rcpayload_enum_frees` (rc-payload enum, which
  strands its array payload as well as its box).

  The mechanism was already in the tree and was only ever wired to one family:
  `optret_pending` (#4353 p1/p3) carries a release across the arm bodies, and
  `emit_dec_sweep_except_list` — which every return form runs — emits it before
  `op_return`. The fix adds two entry kinds, `"#b"` (shallow box dec) and
  `"#e<enum>;<moved…>"` (runtime variant_is deep-drop), and sets them at all
  four emission sites: the fn-body loop and its `lower_block` mirror, scalar and
  rc-payload each.

  **The post-match site stays exactly where it was.** It is the release for the
  fallthrough paths; the pending entry is the release for the return paths. What
  keeps a path from being claimed twice is the slot-zero both sites already
  performed, plus `__fern_rc_dec` being null-safe — so the gate asserts an exact
  `allocs == frees`, not `live_bytes == 0`, and every probe additionally checks
  `__rc_underflow_count()`.

  **The rc-payload entry has to carry the MOVED set, not just the slot.** When
  an arm binds the array payload and returns it, `match_moved_rc_payloads` holds
  that field and `emit_enum_variant_drops_moved` must skip its dec while still
  freeing the box. Dropping the moved set on the return path would dangle the
  buffer the caller is about to read, not merely mis-count it — which is why the
  entry encodes enum name and moved fields rather than reusing the bare
  `"slot#kind"` shape.

  Measured, self-host x86-64, allocs/frees/live before → after (native was
  already balanced on all seven, and every exit code is native's):

  | shape | before | after |
  |---|---|---|
  | scalar enum, all arms return | 100/0/4800 | **100/100/0** |
  | scalar enum, no arm returns (control) | 100/100/0 | 100/100/0 |
  | scalar Option, all arms return | 100/0/4000 | **100/100/0** |
  | rc-payload enum, all arms return | 200/0/8800 | **200/200/0** |
  | rc-payload MOVED out of a returning arm | 200/100/4800 | **200/200/0** |
  | one arm returns AND falls through | 100/51/2352 | **100/100/0** |
  | candidate in a loop body, arm returns | 202/201/48 | **202/202/0** |

  The two partial rows are the mechanism stated in numbers: the mixed shape
  freed exactly the 51 rounds that fell through and leaked the 49 that returned;
  the loop-block shape freed every iteration but the one that returned out.

  Also deleted the redundant slot-zero after the block-level rc-payload free —
  `emit_enum_variant_drops_moved` has always zeroed the slot itself.

  VERIFIED: new `TestSelfHostReturningArmEnumFreeX86_64` (7 rows),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (371 s, 36 units, run FIRST per
  §the reclaim rule), the neighbouring enum/match reclaim gates, and all seven
  probes run through the self-host on **x86-64, arm64 (qemu) and wasm
  (wasmtime)**. Refs #6219 #4451.

- 2026-08-10: **Two more nested-match rows, measured and left open on purpose —
  STRUCT and TUPLE payloads at block scope.** #6503 / #6517 / #6526 / #6538 closed
  #6319's grid (fn/block scope x flat/nested match) for the scalar Option and the
  rc-array payload. Sweeping the same grid over the remaining two payload classes
  at `7f00e52f`, 100 rounds x 4, every exit code matching `fern -interp`, and
  re-verified unchanged at `5052f826` after #6564 landed in the same area:

  | block-scoped local | flat match | nested in an `if` |
  |---|---|---|
  | `Option[P]`, P = `{ xs: i32[], n: i32 }` | 0 | **51200**, `frees=0` |
  | `Option[(i32, i32[])]` | 0 | **48000**, `frees=0` |

  **These are NOT another nested_ok widening, and the code says why.** Both are
  refused one layer further down than the match lookup: `blockable` admits only
  payloads whose drop is COMPLETE on its own (string / leak-safe scalar array).
  A struct payload's shallow box dec relies on the fn-level OPTSTRUCT machinery to
  release its array FIELDS, and a block-level drop zeroes the slot and starves it
  — the #5453 CI regression. A tuple payload fails `is_leaksafe_array_field` for
  the same reason.

  So closing these needs the block-level pass to perform the DEEP field release
  itself, not merely to see the nested match. That is the deep-walk-vs-flat-dec
  line this document already draws: the flat dec is the green side, and the deep
  field walk is what segfaulted gen1 in #6285 / #6375. Whoever takes it should
  budget a fixpoint run per attempt and expect the emission side, not the
  admission side, to be the work.

  Not on #6495's list, and distinct from its `Option[P]`-with-a-string-FIELD row
  (48000): that one is about which FIELD TYPES the deep drop admits, this one is
  about which SCOPE may run the deep drop at all. Both can be true at once.

- 2026-08-10: **In-place `.with` now respects ownership across a bare-ident
  REBIND (#6170).** `x = x.with(i, v)` lowers to an in-place `arr_set`, which is
  sound only when `x` is the buffer's sole reference. #6158 taught that decision
  about field-read bindings; it still had nothing to say about
  `var heap = heap_in; heap = heap.with(…)`. `alias_idents_in_value` credits
  `heap_in` — the name that ACQUIRED an alias — while the name actually mutated
  is `heap`, so the write landed in the caller's buffer. A third shape,
  `var b = a; b = b.with(…)` over a still-live local, was broken the same way
  and was not on any issue.

  | shape | interp | self-host before | after |
  |---|---|---|---|
  | `var heap = heap_in` over a borrowed param | 7 | **77** | **7** |
  | `var b = a` over a still-live local | 7 | **77** | **7** |
  | `var x = obj.field` (#6158) | 7 | 7 | 7 |
  | `own` param, direct | 7 | 7 | 7 |
  | direct borrowed param (#6185) | 7 | **77** | **77** — still open |

  **The param is a rebind SOURCE, never a member of the borrowed set, and that
  distinction is the whole fix.** Crediting the param itself also forbids the
  DIRECT `p = p.with(…)`, which is how a mutable closure capture is written:
  `box_mutated_scalar_captures` rewrites a captured `x = v` into
  `x = x.with(0, v)` on a 1-element cell, and the param-lift hands that cell to
  the lifted body as a plain array param that MUST write through it. Measured
  rather than reasoned: the first cut credited the param and failed
  `TestSelfHostOuterMutCapture`'s `loop-accumulator` (32, want 42) and
  `outer-and-inner-write` (38, want 42) on x86-64, arm64 and wasm — the #5301
  stale-snapshot regression the #6158 gate warns about, reproduced exactly.

  A rebind needs no such judgement, because the capture cell is only ever
  written by the direct form. Telling "the caller's array" from "a captured
  cell" AT THE PARAM was left open here; the 2026-08-14 #6185 entry below
  closes it by naming the cell.

  **The other half is not cloning what it need not clone.** A rebind is credited
  only when its source is a non-`own` array param, is itself borrowed, or is
  used again after the binding. None holding means the rebind is the buffer's
  only remaining name, so `own heap_in` + `var heap = heap_in` keeps its stores
  in place — the const-eval VM's shape (`eval_ops`), which is why this was split
  out of #6158. Measured on a 4000-element array, 4000 `.with` writes:
  `own` **12 allocs / 0 bytes**, borrowed **4012 allocs / 128 MB**. An
  intermediate cut that credited every rebind put the `own` column at 128 MB
  too; `own-param-rebind-loop-allocates-nothing` now pins it, by ALLOCATION
  rather than by answer, because that failure is silent quadratic copying that
  every correctness case still passes.

  Worth recording against the issue's premise: `eval_ops` is reached only from
  `eval_call` ← `eval_program` ← `irlower_run.fern`, a test driver. It is not on
  the production compile path, so the clone cost was bounded even before the
  `own` exemption landed.

  VERIFIED: `TestSelfHostBorrowedWithInPlaceIRX86_64` extended from 5 rows to 9
  (two fixes, one `own` perf guard, one divergence pin for #6185),
  `TestSelfHostOuterMutCapture` on x86-64 / wasm / arm64,
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (391 s), the closure suites, and
  every probe run through the self-host on **x86-64, arm64 (qemu) and wasm
  (wasmtime)**. Refs #6170 #6158 #6185 #5301 #4451.

- 2026-08-10: **`MyEnum[]` element reclaim — the last array-of-X kind with no element
  walk (#5474, #4353 item 4).** `string[]` (#5471), `(…)[]` (ARRTUP) and
  `(<struct-with-array>)[]` (ARRSTRUCT) all reclaimed their elements; an enum array
  was left on a buffer-only dec, so every element enum box and any rc payload it
  carried leaked per iteration.

  **Read the instrument note before the numbers.** This issue accumulated four
  comments ending in a full retraction because the harness used
  `__heap_bump_bytes()` deltas, which reported a leak for a scalar `i32[]` control
  that cannot leak — a bump high-water legitimately advances. `FERN_LEAKCHECK=1`
  answers the question directly (exact allocs/frees/live_bytes) and its controls
  come out clean, so it is the instrument to reach for here. 200 iterations,
  self-host x86-64:

  | element type | before | after |
  |---|---|---|
  | `i32[]` (control) | 201/200/24 | unchanged |
  | `string[]` (control) | 1401/1400/24 | unchanged |
  | `(i32,string)[]` (control) | 1801/1800/24 | unchanged |
  | `E[]` | 2001/**600**/**35224** | **2001/2000/24** |
  | `E[]`, all-unit `[B,B,B]` | 801/**200**/**19224** | **801/800/24** |

  The all-unit row localised it better than the issue body did: 200 × (3 element
  boxes + 1 buffer) allocated and exactly 200 freed means the **outer buffer was
  already being reclaimed** and only the element walk was missing.

  **Two things make ARRENUM its own machinery rather than another ARRSTRUCT.** The
  per-element drop dispatches on the variant at runtime, so it routes
  `emit_enum_variant_drops`; and that primitive frees the element box itself and
  zeroes the slot, so the walk emits **no** trailing `__fern_rc_dec` per element.
  Copying ARRTUP/ARRSTRUCT's trailing dec double-frees every element.

  The element enum name rides the credit (`ARRENUM:<local>#<Enum>`): an `E[]` slot
  records its element type in neither `arrarr_elem` (populated only for `T[][]`, and
  only for four scalar tags) nor `struct_type`, and one credit string beats a slot
  column for a single class.

  **Admission is deliberately much tighter than its siblings'** — the only admitted
  use is `xs.len()`. The hazard here is a double-free, not a leak: a match arm can
  bind an element's payload, and freeing that element under a live binding corrupts
  the self-compile. Every extraction (`xs[i]` bare, as a match scrutinee, or a bound
  element) falls back to the existing shallow dec. Widening this to indexed reads is
  the follow-up and needs the arm-binding escape analysis ARRTUP grew.

  **Separate defect found while gating this, NOT fixed here:** a literal-initialised
  string local declared INSIDE a loop body (`while (…) { var pre: string = "ab"; … }`)
  leaks 24 B per iteration on x86-64 and arm64, and is flat on wasm. It is
  independent of element type — the plain `i32[]` control leaks it identically with
  no rc element anywhere — so it is not an enum-array problem. The bounded-churn
  gate cases hoist the string above the loop so they fail only on what they test.
  Filed as #6582, with the control table and the backend split.

  VERIFIED: new `TestSelfHostArrEnumReclaim{IRX86_64,IRArm64,WasmIR}` (6 rows × 3
  backends — 3 bounded-churn including an all-unit and an unqualified-ctor spelling,
  3 extraction negatives asserting exact values with `__rc_underflow()` at zero),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (442 s), the neighbouring
  ARRTUP/ARRSTRUCT/STRARR/enum reclaim gates, and every probe through the self-host
  on x86-64, arm64 (qemu) and wasm (wasmtime). Refs #5474 #4353 #4451.

- 2026-08-10: **A literal-initialised string local declared inside a loop body is now
  reclaimed (#6582).** `str_local_binding_is_fresh` admitted a concat and the string
  producer methods but not a bare literal, so `var pre: string = "ab"` earned no
  `STR:` credit and leaked one box per evaluation on the asm backends.
  `is_fresh_str_temp`, twenty lines below it, already documented why the literal IS
  fresh — `const_str` allocates a fresh box per evaluation, the DATA is `.rodata` but
  the box is not, and `__fern_str_free`'s heap-base guard skips the data and reclaims
  the box — and admitted the identical shape as a concat OPERAND.

  200 iterations, `FERN_LEAKCHECK=1`, self-host x86-64:

  | shape | before | after |
  |---|---|---|
  | `while (…) { var pre = "ab"; acc += pre.len(); }` | 200/**0**/**4800** | **200/199/24** |
  | the same building an `i32[]` from `pre.len()` | 400/**200**/**4800** | **400/399/24** |
  | `pre` hoisted above the loop *(control)* | 201/200/24 | 201/201/0 |

  Flat on wasm before and after — the literal is data-section there and `arr_dec` is a
  guarded no-op. **That backend split is the trap:** it reads as a partial fix to
  whatever class is under test. It cost a full round on #5474's gate, whose churn
  cases hoist the string for exactly this reason.

  **Scope was set by two over-release contracts, both found by running the existing
  gates rather than by reasoning.** The first cut folded the literal into
  `str_local_binding_is_fresh`, whose ~20 other callers drive the accumulator
  consume-rebind and concat-temp analyses: that made `s = "reset"` read as a fresh
  rebind and credited an accumulator `TestSelfHostStrAccum` pins as un-reclaimable.
  The credit therefore lives only on the `STR:` path. The second: a literal local that
  is the receiver of `.to_string()` stays uncredited, because on a string receiver that
  call is the IDENTITY — its result aliases the receiver's box and the concat-temp
  machinery frees that result as a temp, so crediting the local too releases the same
  box twice (`tostring-string-recv-alias-safe`, pinned at exactly two sites).

  Still leaking, deliberately: a literal local that is also a CONCAT OPERAND
  (800/598/4832). `expr_unsafe_for` treats any ident operand of a binary op as an
  escape, and that gate backs every reclaim class — widening it is a cross-class change
  that needs its own gating.

  Also fixed here: `make selfhost-cli` was broken on Linux since #6572 renamed targets
  to `<isa>-<environment>` — the Makefile default still said `x86-64`. Note the
  self-host compiler itself still takes the OLD spellings, so `bin/fern` and
  `bin/fern-selfhost` currently disagree on target names.

  VERIFIED: new `TestSelfHostLitStrLocalReclaim{IRX86_64,IRArm64}` (4 rows × 2
  backends), the whole `TestSelfHostStr*` family, `TestSelfHostArrEnumReclaim`, and
  `TestSelfHostPerModuleEmitAllFixpointX86_64`. Refs #6582 #4451.

- 2026-08-10: **The block-scoped STRUCT payload with a nested match — closed; the
  TUPLE half is not.** #6553 recorded both rows and predicted the work would be on
  the EMISSION side rather than admission. That held exactly: the block-level
  rc-payload emission had three branches (tagged / string / flat dec) and no deep
  one, so `blockable` excluded struct payloads to stop a shallow dec stranding the
  array FIELDS (#5453). Giving the block pass the same
  `emit_opt_struct_payload_drop` branch `lower_func` has, and keying `blockable`
  on `dsty` — which already means "deep drop available AND no arm moved a field
  out" — closes it:

  | block-scoped, match nested in an `if` | before | after |
  |---|---|---|
  | `Option[P]`, P = `{ xs: i32[], n: i32 }` | 51200, `frees=0` | **0** |
  | flat control | 0 | 0 |
  | field moved out of the arm | refused | refused (1201/1) |
  | `Option[(i32, i32[])]` | 48000 | **48000 — still open** |

  Tuples are untouched because `opt_payload_deep_sty` is struct-only; that row
  needs a tuple-shaped deep drop, which is its own increment.

  **THE FIXPOINT IS THE HEADLINE.** This is the deep FIELD walk on a block-scoped
  slot — the operation §9 records as segfaulting gen1 in #6285 / #6375, and the
  reason the flat-dec/deep-walk line was drawn. It is green at 464.08 s. What
  changed since is #6408, which taught `expr_unsafe_for` / `moves_fields_expr` to
  read a bare non-scalar `name.field` as a MOVE; the walk this grants is the one
  that bisect isolated, and it now has the escape analysis it was missing.

  **A correction whose necessity I could NOT demonstrate, stated as such.**
  `opt_arm_field_moved` was reading `body[match_idx]`, which under a nested lookup
  is the enclosing `if` — a statement it cannot parse, so it answers "no field
  moved". It now reads the match. That is right in principle, but mutating it back
  changes no observable behaviour on either shape I could construct: an ARRAY
  field assignment RETAINS (#6467), so a granted drop stays balanced, and a STRING
  field is never walked by the deep drop at all (which is #6495's separate open
  row). So it is a correctness fix against a latent hole, not a fix for a live
  bug — and it becomes load-bearing the moment string fields are admitted.

  VERIFIED: `TestSelfHostBlockStructPayloadNestedMatchX86_64` (new — the nested
  row, its flat control, and the field-moved refusal),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (464.08 s, run FIRST), 28 targeted
  suites 0 skips. Refs #6319 #6360 #4451.

- 2026-08-10: **`__alloc_u8` untaint ported to the self-host free-eligibility
  analysis (#5935), and the issue's rationale corrected.** Native's #5931 untaints
  the allocator — `__alloc_u8(n)` hands back a fresh block that cannot alias its
  scalar size argument — so a dynamically sized buffer is free-eligible. The
  self-host's generic any-arg-tainted rule said otherwise, leaving
  `__alloc_u8(16)` eligible and `__alloc_u8(n_bytes)` not.

  **Item 1 of the issue's work list was already done.** It states `rc_fe_walk_expr`
  "has no cast arm at all"; the tree has one — an `ExprUnary` arm matching `as_<T>`
  that escapes a pointer-shaped operand cast to a non-pointer target. Only the
  untaint (item 2) and the coverage (item 3) were outstanding.

  **This is a differential-parity fix, NOT a memory win, and the issue says
  otherwise.** It predicts the port is "plausibly material to the arena pressure
  tracked around #3425" because gen1 is "still leaking". In fact
  `free_eligible_of` has exactly one consumer in the whole self-host —
  `rc_plan_dump` — so the table drives no emission and porting it cannot change
  what any generation reclaims. Measured rather than argued: a
  `while (…) { var s = i.to_string(); }` loop compiled by the self-host reads
  `allocs=400 frees=0 live_bytes=6400` both before and after the port. The issue
  also says the win "cannot currently be quantified" for want of a self-host
  `FERN_LEAKCHECK`; `FERN_LEAKCHECK=1` on a self-host-COMPILED program measures it
  directly, which is how that number was obtained.

  That `to_string` buffer leak is real and still open — it is simply a different
  gap, not this one.

  **Both new gate cases were verified load-bearing by disabling each half in turn**,
  because a differential case that passes before and after is not a gate:

  | case | with both halves | untaint disabled | cast guard disabled |
  |---|---|---|---|
  | `fe-alloc-u8-computed-size` | pass | **FAIL** — native `buf` vs self-host `""` | pass |
  | `fe-alloc-u8-address-escapes` | pass | pass | **FAIL** — native `""` vs self-host `buf` |

  The second row fails in the DANGEROUS direction: without the cast arm the
  self-host calls a buffer eligible whose address was handed out as a raw integer
  (`buf as usize` into `__memcpy`, which is what `std/string`'s `s.bytes()` does).
  That is the ordering constraint the issue flags, now pinned rather than reasoned.

  VERIFIED: `TestSelfHostRcPlanDiff` (+2 cases), the per-module fixpoint (440 s),
  `TestSelfHostIRLowerRoundTrip`. Refs #5935 #5931 #4482 #4451.


- 2026-08-10: **`Option[P]` where P's only rc field is a `string` — CLOSED, and a
  scalar-only P with it (#6360).** #6495's near-miss row. The OPTSTRUCT class — the
  fresh, non-escaping `Option[<struct>]` local whose loop-rebind + exit sweep
  deep-free the payload's fields, its box and the option box — admitted a payload
  struct only when `struct_has_reclaim_array_field` held. A payload whose sole
  reclaimable field is a bare `string` matched nothing at all: no credit, so no drop
  of any kind, so the string AND both boxes leaked.

  **Everything downstream was already ready; the leak was entirely in admission.**
  `__struct_drop_<P>`'s k_str arm has freed string fields on all three backends since
  #4355; `nddo_reach` has counted a string field as reclaimable since #4297 A2; and
  the arm-binding escape checker (`optstruct_arm_expr_escapes`) is field-TYPE generic
  — a bare `p.name` extraction escapes because `string` is non-scalar, `p.name.len()`
  is a borrow. The evidence was in two controls: the same struct bound as a BARE local
  has been reclaimed since #4357, and `P { name: string, xs: i32[] }` was already
  clean, because the array field alone made the whole struct admissible. Emitted asm
  says it flatly — `__struct_drop_P` appears zero times for the string-only payload
  and four times for both controls.

  So the fix is the predicate, now `optstruct_payload_reclaimable`, shared by
  `optstruct_ann_is` (the credit) and `slot_is_reclaimable_optstruct` (the emit-time
  routing) so the two cannot disagree.

  **The string half is gated a SECOND time at emit**, by
  `struct_routes_field_reclaim`'s whole-program STRFLDOK verdict. A type that scan
  refuses emits no `__struct_drop_<P>` call, so the two boxes are still freed and only
  the string is stranded — the conservative direction. The construction-side retain
  (`slit_reclaim`) reads the same verdict, so an ALIASED string field is inc'd at
  construction and the k_str dec is balanced rather than a double-release.

  **A second row fell out of the sweep and is closed here too: a SCALAR-ONLY struct
  payload.** It has nothing to deep-drop, which is exactly why it was excluded while
  this was "the deep-drop class" — and nothing else claimed a block-scoped one, so
  both boxes leaked per bind. Its drop is the two box decs alone, no walk.

  Measured, self-host x86-64, allocs/frees/live before → after (native is 0 on every
  row, and every exit code matches `fern -interp`):

  | shape | before | after |
  |---|---|---|
  | `Option[P]`, P = `{ name: string, n: i32 }` | 2400/800/**48000** | 2400/2400/**0** |
  | same, arm never reads the string | 2400/800/**48000** | 2400/2400/**0** |
  | `Option[P]`, P scalar-only | 800/0/**35200** | 800/800/**0** |
  | `Option[P]`, P = `{ name: string, xs: i32[], n }` (control) | 2800/2800/0 | 2800/2800/0 |
  | ALIASED string field (hazard) | — | 3700/3500/3200, exit == interp |
  | arm extracts `p.name` (hazard) | — | refused, 50400 stranded |

  The two hazard rows are the conservative side and are pinned as such: the aliased
  one must not over-release (its residual 3200 is the retain, not a defect), and the
  extracting arm must stay refused because releasing there would dangle.

  VERIFIED: new `TestSelfHostOptStructStringFieldX86_64` (8 rows, exact alloc/free
  balance — which a bump-growth bound cannot separate from "freed the boxes, stranded
  the string"), four new cases in the shared `optStructReclaimCases` table so they run
  on **x86-64, arm64 (qemu) and wasm (wasmtime)**,
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (398 s, run FIRST per §the reclaim
  rule), and the OPTSTRUCT/OPTTUP/OPTARR/string-field/struct-drop/nested-match
  neighbour gates. Refs #6360 #4451.

  Re-measured at the same commit, unchanged and still open — the rest of #6495's list
  is a different cause (a per-ELEMENT string release, or an unreachable credit), not
  another payload-struct admission:

  | shape | self-host | frees |
  |---|---|---|
  | `Option[string[]]` | 57600 | 1600/4000 |
  | `Option[Option[i32[]]]` | 48000 | **0**/1200 |
  | `Result[string[], string]` | 57600 | 1600/4000 |

- 2026-08-10: **`<scalar>.to_string()` now earns the `STR:` reclaim credit (#6599).**
  `var s: string = i.to_string()` never freed its box on the self-host, unbounded in a
  loop, while native was flat. It matters out of proportion to the shape because every
  `f"{x}"` desugars to `x.to_string()`.

  **The callee was innocent, and the measurement that shows it is the useful part.**
  Varying only the spelling, 200 iterations, `FERN_LEAKCHECK=1`, self-host x86-64:

  | spelling | before | after |
  |---|---|---|
  | `i32_to_string(i)` — free function | 400/398/32 | unchanged *(control)* |
  | `i.to_string()` — method | 400/**0**/**6400** | **400/398/32** |

  **Identical allocation counts**, which is what proves `int_to_string`'s own
  `__alloc_u8` buffer and `string_from_bytes_unchecked` are not involved: the whole
  difference was one caller-side credit. `str_free_producer_ident` admits the
  free-function spelling by name and excludes the method form, which
  `str_local_binding_is_fresh` lists under "receiver-identity fast-paths".

  That exclusion is right for a STRING receiver — the call returns the receiver itself,
  so freeing the result releases a box the source still owns — and wrong for a SCALAR
  receiver, where it is the decimal-text builtin returning a fresh sole-owned box.

  **The receiver-type test cannot live in the freshness predicate, and the reason is
  structural rather than stylistic.** `str_local_binding_is_fresh` is deliberately
  PURELY SYNTACTIC — no LowerState, no types — with the type gate applied separately via
  the slot's `is_str`. `is_str` is true for BOTH receivers, because the RESULT is a
  string either way; what must be tested is the RECEIVER. Its ~20 other callers drive
  the accumulator and concat-temp analyses, where widening it broke two over-release
  contracts in #6590. So the credit is a separate collector in `reclaimable_names_of`,
  the same shape as that fix's `collect_litstr_local_names`, reading a name→declared-type
  map built the way `rc_fe_collect_types` builds one.

  An UNKNOWN receiver type is REFUSED rather than assumed scalar: the wrong answer on
  this side is an over-release, not a leak. Same reason `.to_string()` on a scalar
  PARAM stays uncredited — `reclaimable_names_of` does not receive `fn.params`, so the
  type does not resolve. Conservative, and worth widening only with its own gate.

  VERIFIED: new `TestSelfHostTostrScalarReclaimIRX86_64` (5 rows — the reproducer, the
  free-function control, a value-exactness guard, the string-receiver negative, and an
  escape negative), with the reproducer row confirmed load-bearing by disabling the
  credit. Also green: the whole `TestSelfHostStr*` family including
  `tostring-string-recv-alias-safe` and `TestSelfHostStrAccum`, the #6590 litstr suite,
  and `TestSelfHostPerModuleEmitAllFixpointX86_64` (458 s). Refs #6599 #6590 #5935.

- 2026-08-10: **The TUPLE payload's nested match — #6319's grid is now closed for
  all four payload classes.** #6588 predicted this row would need "a tuple-shaped
  deep drop". **That prediction was wrong, and measuring is what found it out:**
  `emit_opttup_deep_free` and `slot_is_reclaimable_opttup` already exist and
  already perform the type-driven tuple deep-drop. Only the CREDIT was missing —
  `collect_fresh_opttup_names` found its consuming match with
  `sole_top_level_match_idx`, the same flat-index blindness the scalar, rc-array
  and struct collectors each had. So this is an ADMISSION fix, the exact inverse
  of #6588 next door, where emission genuinely was the work.

  | `Option[(i32, i32[])]` | flat | nested in an `if` |
  |---|---|---|
  | fn-scoped | 0 | **12000, `frees=0` -> 0** |
  | block-scoped | 0 | **48000, `frees=0` -> 0** |
  | tuple element escaping the arm | — | refused, 1201/1 |

  **No `nested_ok` flag, and that too is measured rather than carried over.** The
  scalar (#6526) and rc-payload (#6538) collectors each needed one because
  precise_drop_names owns their shape at function scope. Nothing owns the tuple
  shape at EITHER scope — both nested cells leaked while both flat cells were 0 —
  so there is no territory to divide and no second credit to collide with. Three
  same-looking widenings, three different disjointness answers; none of them
  transferred.

  With #6480 / #6503 / #6517 / #6526 / #6538 / #6588 and this, the grid closes:
  fn/block scope x flat/nested match, for scalar Option, rc-array, struct and
  tuple payloads, under both inline-ctor and call inits.

  VERIFIED: `TestSelfHostOptTupNestedMatchX86_64` (new — both scopes nested, both
  flat controls, and the element-escape refusal),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (326.63 s, run FIRST), 29 targeted
  suites 0 skips. Refs #6319 #6360 #4451.

- 2026-08-10: **A `match (param)` scrutinee is a BORROW in the borrowability
  verdict too — #6606's enum row, an unbounded leak.** A fresh rc-payload enum
  loop-local passed to a helper grew 2.00x per doubling (24000 bytes at 300 rounds
  against 48000 at 600, `frees=0`) where the identical match written INLINE was
  flat. Native is flat on both.

  **Two independent gaps, and closing either alone leaves the shape leaking:**

  1. `collect_fresh_rcenum_names` required a consuming `match` in the same block,
     so a local with no match at all earned nothing. The reduction is not the
     helper — `var b = Val([k, k+7]);` declared in a loop and NEVER USED leaked
     identically. With no match there is nothing for the arm gates to prove, so
     non-escape is the whole condition.
  2. `borrowable_params_interproc` read a bare-ident `match (param)` scrutinee as
     an ESCAPE, so `head(b)` refused every caller-side release. #6127 had already
     made the opposite argument — a match reads the tag and the payload and
     retains neither — and wired it into the local-reclaim analyses; it had never
     reached the borrowability verdict.

  | `enum Box { Val(i32[]), Empty }`, loop-local | before | after |
  |---|---|---|
  | `head(b)`, helper matches on it | 24000 / 48000 | **160 / 160** |
  | declared, never used | 24000 / 48000 | **160 / 160** |
  | inline consuming match *(control)* | 80 / 80 | 80 / 80 |
  | callee RETURNS the payload *(hazard)* | refused | refused |

  **THE TRAP THAT COST THE MOST TIME: there are TWO borrowability passes, and the
  emit path uses the one that is not named `borrowable_params_of`.** The single
  pass feeds the ~15 inspection passes and `precise_drop_names`; the emit path
  overrides `sg.borrowable_params` with the `borrowable_params_interproc`
  fixpoint. Fixing only the former left every probe measuring exactly as before,
  while a standalone harness calling `borrowable_params_of` reported the param
  borrowable — a diagnostic that agreed with the fix and disagreed with the
  compiler. Both are corrected here so they cannot diverge again.

  **`param_match_binding_escapes` is the new safety gate and it is load-bearing —
  measured, not argued.** The scrutinee-is-a-borrow reading alone cannot see an
  arm handing the payload OUT: `b` is never mentioned outside the match, so no
  walk over `b` observes `Val(xs) => { return xs; }`. Deleting the gate takes the
  hazard row from 401 frees to 800 and from 16000 live bytes to 40 — the enum
  released under its other holder. It checks EVERY binding rather than the
  rc-droppable ones `match_arm_binds_rc_payload` filters to, because that filter
  resolves a binding's type through the enum's StructDecl, which the built-in
  Option / Result do not have.

  **The exit code does not move on that hazard, and no spelling made it.** A
  same-shaped churn loop between the release and the read — the trick that made
  #6467's string case discriminate — left both compilers at 30. So the byte count
  is the only detector here, and the gate is pinned by an EXACT stranded count
  (400 at 100 rounds, 800 at 200); the mutation reads 1, which any "some leak"
  assertion would have accepted.

  Still open, deliberately: the reclaimed rows keep an **80-byte** residue — the
  final value, which no sweep reclaims for this class, constant at 100 and 200
  rounds while allocs and frees both double. The inline-match control reaches an
  exact 0 because its consuming match frees every iteration including the last.
  That is a bounded last-value gap in the block-exit sweep, not this leak.
  #6606's string row (a user function's string result is not a "fresh producer",
  `str_free_producer_ident` being a hardcoded builtin allowlist) is untouched and
  is adjacent to #6544.

  VERIFIED: `TestSelfHostRcEnumBorrowHelperX86_64` (new — both reclaimed shapes at
  two round counts, the inline-match control, and the payload-escape refusal),
  `TestSelfHostPerModuleEmitAllFixpointX86_64` (359.40 s, run FIRST — the
  self-hosted compiler is itself full of `match (param)` helpers, so it is the
  widening's own heaviest exercise), then 47 targeted suites 0 skips including
  `TestSelfHostBorrowInferInterprocX86_64`. Refs #6606 #6127 #4365 #4451.

- 2026-08-10: **The #6043 sole-occurrence death, ported to the self-host grow bracket
  (#6048).** A PARAM read exactly once in the whole body, at a call no loop or lambda
  encloses, is dead at that call, so the #4873 containment bracket need not force a
  full-buffer copy on it. The self-host carried only the two pre-#6043 shapes (the
  self-reassign and the `return f(.., x, ..)`), both gated per-statement.

  `__arr_push_shared_count()`, self-host x86-64, the #6036 probe corpus:

  | probe | before | after |
  |---|---|---|
  | `H_call_result_into_local` — the shape filed | **45** | **0** |
  | `J_nested_call_arg` | **44** | **0** |
  | `M_call_then_inline_append` | **44** | **0** |
  | A / C / I / K / L *(controls)* | 0 | 0 |

  **Two things diverge from the issue's expectation, and both are measurements rather
  than predictions.** It says J/K/M "are pinned at 49 by a DIFFERENT mechanism and
  should stay 49" — that is the NATIVE corpus. On the self-host they read **44/0/44**
  before this change, so the two compilers were never at the same numbers here, and
  the port takes J and M to 0 as well as H. Native still reads 49 for those probes,
  so the self-host now performs FEWER buffer copies than native on two shapes. The
  benign direction, and the probe checks the accumulator's length and endpoints before
  reading the counter, so every row is fewer copies of a *correct* answer.

  **The per-statement hook cannot see the whole body**, so the census is computed once
  per function (`grow_sole_exempt_names_of`) and carried on `LowerState.grow_sole`. It
  can be unioned into every statement's exempt set unconditionally: a name in it occurs
  exactly once in the entire body, so only the statement holding that occurrence has a
  bracket that can consult it.

  Both of native's restrictions are carried over and both are load-bearing. The
  LOOP/LAMBDA exclusion — a single textual read inside a repeating construct is still
  many dynamic reads, and the next iteration would observe the previous one's in-place
  growth, which the interpreter never performs; without it the rule degenerates into
  the textually-last-occurrence heuristic `callArgDeaths` deliberately rejects. And
  PARAMS only — a param is bound once per frame from the caller, whose own bracket
  already contains what the callee does to it.

  VERIFIED: new `TestSelfHostGrowSoleOccurrenceX86_64` (9 rows, including a
  loop-negative that asserts on CONTENTS rather than a count), the per-module fixpoint
  (581 s), and the grow / array / string reclaim suites (604 s). Refs #6048 #6043
  #6036 #4873.

- 2026-08-10: **The self-rebind field-alias exemption widens to ARRAY fields (#6628),
  and the residual it exposes is a third defect (#6653).** #6623 exempted a
  nested-struct / enum field read in `name = T { …, f: name.f, … }` from the NODEEP
  move scan; the array spelling `ys: o.ys` was left marked and still cost the local
  its whole `__field_reclaim_S` credit.

  | k | `ys: o.ys` before | after | `...o` control |
  |---|---|---|---|
  | 1 | 60 / 30 — **1440 B** | 60 / 50 — 480 B | 60 / 60 — **0** |
  | 2 | 90 / 50 — **2160 B** | 90 / 80 — 480 B | 90 / 90 — **0** |
  | 8 | 270 / 170 — **9840 B** | 270 / 260 — 480 B | 270 / 270 — **0** |
  | 32 | 990 / 650 — **98160 B** | 990 / 980 — 480 B | 990 / 990 — **0** |

  **#6628's premise that the retain is missing is wrong, and that is the whole
  finding.** It reads the override path as inc'ing an array field value "only when it
  is a bare ident naming an rc-array slot", so it asks for the retain to be widened
  before the exemption. But the `ExprFieldAccess` arm of the array gate already incs
  on `field_access_arr_field_type` resolving, and it cannot silently decline: an
  unresolved receiver leaves `fav_ok` false and BAILS the whole lowering rather than
  storing uncounted. So the exemption alone is sound, and the issue's two-sided
  landing requirement does not apply. `is_struct_array_field_type` rides along —
  the same arm, the same gate. `string`, `string[]` and array-of-ENUM stay marked:
  `Ty[]` enters neither array gate and the fallback retain admits only a bare ident.

  **The 480 B residual is NOT this defect and not #6605.** It is flat in k — one
  stranded `ys` buffer per CALL, confirmed by scaling `ys` from 3 to 30 elements
  (480 → 2640 B). The override incs the aliased field, `__field_reclaim_`'s array arm
  COW-SKIPS a field that is pointer-equal in old and new, and nothing else decs it.
  The `...o` spelling reaches an exact 0 because its base-copy path emits no inc for
  an array field at all.

  The same imbalance is why `TestSelfHostNestedFieldAliasRebindX86_64` was RED on
  `2d9b85a`: #6627 was validated on a base without #6620, and once both were on main
  the `...base` carry went to 0 while the explicit spelling stayed at 800. There the
  route is different — the nested-struct arm of `__field_reclaim_S` is gated on
  `structfldok:S`, and the explicit `o.inner` read is itself what disqualifies the
  type, so the arm is absent from the emitted body entirely (visible in
  `-target x86-64-linux -emit asm`). Both routes are #6653; this change re-points that row at an
  exact pin of the residual so main is green and the number cannot drift.

  VERIFIED: `TestSelfHostNestedFieldAliasRebindX86_64` (2 new k-sweep rows + an
  array fork-negative; the two new rows FAIL on the parent at 9840 / 98160 B),
  cross-backend exit agreement with `fern -interp` on x86-64 / arm64 (qemu) / wasm
  (wasmtime), `FERN_STRICT_IR=1` clean on both probes.
  Refs #6628 #6653 #6623 #6605 #6620.

- 2026-08-11: **The self-rebind array alias stops retaining what nothing releases
  (#6653, array route).** `o = S { …, ys: o.ys, … }` took an `__fern_rc_inc` on the
  carried buffer from the struct-literal override path, and `__field_reclaim_`'s
  array arm COW-SKIPS a field that is pointer-equal in old and new — so the inc had
  no counterpart. One stranded `ys` buffer per CALL: 480 B for three i32 elements,
  2640 B for thirty, flat in k either way.

  | shape | before | after | `...o` control |
  |---|---|---|---|
  | `ys: o.ys` (i32[]) | 270 / 260 — 480 B | 270 / 270 — **0** | 270 / 270 — 0 |
  | `es: o.es` (E[]) | 270 / 260 — 400 B | 270 / 270 — **0** | — |

  The fix is the direction the `...o` spelling had already proven: its base-copy path
  emits **no** inc for an array field, and it has always been exactly balanced. So
  `LowerState.selfrebind` carries the reassigned local's name for the duration of a
  direct `name = T { … }` RHS, and the override path drops the retain on a field read
  off that name. It is not a general exemption — a fork to a different local
  (`p = S { ys: o.ys }`) keeps the inc, because `p` and `o` then both hold the buffer.

  **The flag is cleared before any field value is lowered**, so a nested literal
  (`S { inner: I { data: o.ys } }`) keeps its own retain. **That clear is currently
  unguarded and the PR says so**: mutating it away changes neither the exit code nor
  the byte count on either probe built for it, because a nested struct field's box is
  not deep-dropped at all for a `structfldok:`-refused type, so the missing dec has
  nothing to pair with. Keeping the clear is the conservative direction — a retained
  field leaks, it never dangles — but it is an argument, not a measurement.

  **The nested-struct and enum routes of #6653 are NOT fixed here** and stay at
  800 B / 720 B. Their imbalance has a different cause: the nested-struct arm of
  `__field_reclaim_` is gated on `structfldok:<T>`, and the explicit `o.inner` read is
  itself what disqualifies the type, so the arm is absent from the emitted body
  entirely. Dropping the retain there would over-release for any type the gate DOES
  admit, where #6620 made the arm release unconditionally — so that half needs the
  admission question answered first.

  Found on the way and filed, not fixed: #6665, where native reads an array field
  aliased into a nested literal as one element LONGER than the interpreter and the
  self-host both do — the in-place `.append` observed through the alias.

  VERIFIED: `TestSelfHostNestedFieldAliasRebindX86_64` (the residual row becomes an
  equality assertion against the carry), cross-backend exit agreement with
  `fern -interp` on x86-64 / arm64 (qemu) / wasm (wasmtime).
  Refs #6653 #6628 #6620 #6665.

- 2026-08-10: **The rc-enum block's FINAL value now rides the exit sweep
  ("RCENUMS:"), which is what flips #6608's pinned row.** #6622 killed the
  unbounded per-iteration leak on a rc-payload enum loop-local, but left the tail:
  on #6608's probe it measured `allocs=400 frees=396 live_bytes=160` — 2 stranded
  per `work()` call, the last iteration's box + payload. Now 400/400, `live=0`,
  exit **7** on both x86-64 and arm64.

  **The credit is SPLIT rather than flipped, and that is the whole design.**
  `reclaim_slot_name`'s class note listed "RCENUM:" among the classes that must NOT
  be switched on at the sweep, because the consuming-match analysis frees the value
  inside the block on every iteration including the last — sweeping it is a second
  claim on a released box. That was right for the whole class until #6622 admitted
  the half with NO consuming match, which nothing frees in-block (the rebind
  releases only the PRIOR value). So the no-match half alone earns "RCENUMS:"
  (`slot_is_reclaimable_rcenum_sweep`, keyed on `reclaim_slot_name` so a
  `!retired!` block slot still finds its credit); the match-consumed half keeps
  "RCENUM:" alone. Same shape as the "TUPRC:" / "TUPRCS:" split. The note has been
  corrected — **a one-class-one-answer assumption is what made it wrong once the
  class grew a second shape.**

  | `enum Box { Val(i32[]), Empty }`, loop-local | frees | exit |
  |---|---|---|
  | before #6622 | 0 / 400 | 3 |
  | after #6622 | 396 / 400 | 3 |
  | after this | **400 / 400** | **7** |

  **THE INSTRUMENT LESSON, which cost a merged PR a wrong claim.** #6622's own
  suite asserts the SCALING — residue constant while allocs and frees both double —
  and a constant tail cannot fail that by construction. #6608's row compares the
  heap mark across two identical calls, so it sees a per-call tail directly. I
  wrote on #6622 that whichever PR landed second would flip #6608's row to 7; it
  did not, and main stayed green, because the two suites were measuring different
  defects. The tail was only ever visible from the weaker-looking gate. Both are
  now exact-balance assertions, and #6608's row is renamed
  `enum-payload-reclaimed`.

  Unchanged and deliberately so: the match-consumed control (800/800 before and
  after — no second release), and the payload-escape refusal (401/801, still
  stranded, exits matching `fern -interp`).

  VERIFIED: `TestSelfHostPerModuleEmitAllFixpointX86_64` **PASS 540.62 s, run
  FIRST** — this is the deep variant walk on RETIRED block slots, the operation §9
  records as segfaulting gen1 in #6285 / #6375, so it is the gate that matters
  here. Then 162 targeted suites 0 skips, including
  `TestSelfHostLoopVarReclaimIR{X86_64,Arm64}` (the flipped pin, both backends) and
  `TestSelfHostRcEnumBorrowHelperX86_64`. Refs #6606 #6608 #4365 #4451.

- 2026-08-11: **The self-rebind field alias stops disqualifying its own type
  (#6653, nested-struct + enum routes), and the qualified variant ctor it exposed
  (#6681).** `o = S { …, inner: o.inner, … }` stranded one `I` box and its `data`
  buffer per CALL — 800 B over ten calls, flat in k — because the whole-program
  `structfldok:` read scan marks a bare non-scalar field read in a struct-literal
  field-value position as escaping, so `S` was refused, `__field_reclaim_S` was
  emitted with no nested-struct arm at all, and the override path's retain had no
  counterpart.

  | shape | before | after | `...o` control |
  |---|---|---|---|
  | `inner: o.inner` (nested struct) | 800 B | **0** | 0 |
  | `v: o.v` (direct enum), k = 1 / 2 / 8 / 32 | 400 B | **0** | 400 → **0** |
  | `v: V.A(7)`, single bind, no loop | 40 B | **0** | — |

  **The scan's contract is about OWNERS, and a self-rebind creates none.** The
  successor box goes into the slot `o` names; `o.f` is re-homed from the box that
  is dying into the box replacing it, so the owner count never leaves 1. That is
  the third scan to need this exemption on the same shape — #6623 for the NODEEP
  move scan, #6628 widening it to array fields — and here it is the admission that
  moves, not the retain. **Not touching the retain is what makes it safe against
  the duplicated spelling** `o = S { a: o.f, b: o.f }`: both reads keep their inc
  against one release, which leaks, where dropping the retain (#6653's own
  preferred direction, and the right one for the array route in #6666) would
  release twice.

  **Direction 1 was not available here for the reason the array route made it
  obvious.** #6620 made the nested-struct / enum arm release UNCONDITIONALLY —
  no cow compare — so for a type the gate DOES admit the retain is load-bearing;
  dropping it over-releases. The array arm still cow-skips, which is why the
  opposite fix was right there.

  **The enum row needed both fixes and reads 400 B with either alone**, which is
  what identified #6681: `variant_ctor_enum_owner` only matched the bare `A(7)`
  callee, so the qualified `V.A(7)` read as a possible alias and took an
  `__fern_rc_inc` on a box that is sole-owned at rc=1. Flat in k because the whole
  shape allocates that box once — and present on the `...o` carry too, so it was
  never a self-rebind defect. The predicate (and its `variant_ctor_array_payloads_fresh`
  sibling) now resolves `E.V(args)` / bare `E.V`, with the object required to be
  the bare name of the owning enum so a method call cannot pass by naming its
  receiver after an enum. That verdict feeds a dozen freshness gates — ENUMRE
  deep-drop, boxarr / cross-struct reuse, the enum-array element walks — all of
  which were silently refusing the qualified spelling.

  Unchanged and deliberately so: the fork negatives (`p = S { f: o.f }` keeps its
  mark on all three field kinds — both locals show the box afterwards), and a
  donor whose enum payload ESCAPES through a call argument, which the same scan
  still refuses.

  VERIFIED: `TestSelfHostNestedFieldAliasRebindX86_64` — the nested-struct row
  becomes an equality assertion against the carry, plus an enum k-sweep, an enum
  fork-negative and a qualified-vs-bare ctor row (all three new rows FAIL on the
  parent at 400 / 400 / 40 B). Every probe's self-host x86-64 exit agrees with
  `fern -interp`, `FERN_STRICT_IR=1` is clean on all of them, and neither
  `FERN_RC_UNDERFLOW_TRAP` nor `FERN_RC_FREE_DEBUG` fires. The arm64 / wasm
  evidence is the enum + reuse + reclaim suites on all three backends, which route
  through the same shared admission in `irlower`.
  Refs #6653 #6681 #6628 #6623 #6620 #4451.

- 2026-08-11: **A field read at a BORROWABLE call-arg position stops reading as a
  move (#6691).** `tagof(o.v)` — one call, after the loop — cost the struct local
  `o` its ENTIRE reclaim credit: `fieldmove_expr` reads every non-scalar field
  read in a direct call argument as a move, so the local was marked `NODEEP:`,
  and `__field_reclaim_S` was not called at all (nor `__struct_drop_S`). Every
  superseded box AND the `xs` buffer it owned stranded per ITERATION.

  | k | before | after | native |
  |---|---|---|---|
  | 1 | 1840 B | **880** | 0 |
  | 8 | 10240 B | **880** | 0 |
  | 32 | **98560 B** | **880** | 0 |

  Flat instead of growing, and the exits (60 / 2 / 50) never moved.

  **The verdict this needs was already computed and already trusted for more.**
  `borrowable_params_of` admits a param only when the callee provably never
  returns, stores, slices or captures it, and `expr_unsafe_for` consults it for
  this exact shape one level up — its Level-2 rule, where a bare-ident argument at
  a borrowable position is a borrow rather than an escape. That rule licenses
  caller-side FREES; keeping a deep drop is strictly weaker, so the same registry
  settles it. Free calls only: method-param borrowability is not in the
  free-function-keyed registry, so a method argument keeps marking.

  **What identified it was the k CURVE, not the count.** `var vv: V = o.v;
  tagof(vv)` — a `var` init, which the same scan already treats as a borrow —
  restores the credit and lands on 480 B. Both numbers look like "a leak"; only
  the curve says one is per-iteration and the other per-call.

  The remaining flat 880 B is the enum payload's shallow-drop model (`k_enum`
  frees the box, not the payload array), not this defect, and it is what the new
  suite's k-sweep holds constant.

  Unchanged and deliberately so: a callee that RETAINS its argument. `keep(v: V): V
  { return v; }` is refused by `borrowable_params_of`, so `var kept = keep(o.v)`
  keeps `o` marked — the probe reads `kept` back after the rebind loop, so an
  over-release there is a wrong exit, and it is pinned as a negative row.

  VERIFIED: new `TestSelfHostBorrowedFieldArgReclaimX86_64` (k-sweep + the
  retaining-callee negative; the sweep FAILS on the parent, where the byte count
  tracks k). Refs #6691 #6653 #4451.

- 2026-08-11: **The `structfldok:` read scan stops counting a BORROWABLE call
  argument as an escape (#6698).** The read-scan sibling of #6691, and the last
  door left open on the #6653 imbalance. Two spellings of one program, differing
  only in the final read:

  | read | before | after | native |
  |---|---|---|---|
  | `o.inner.tag` — direct | 0 B | 0 B | 0 |
  | `itag(o.inner)` — call arg | **800 B** | **0** | 0 |

  A marked field NAME disqualifies its whole type from `structfldok:`, which gates
  the nested-struct / enum arms of BOTH `__field_reclaim_<T>` and
  `__struct_drop_<T>`. So one call argument refused `S` outright and the
  struct-literal override's retain on `o.inner` had nothing to pair with. The scan
  already models one borrow — a method call's receiver chain goes through
  `structfld_safe_operand`, which walks beneath without marking — and a borrowable
  free-function argument is the same thing with a stronger warrant.

  It also recovered part of #6691's residual: the enum probe there goes 880 → 480,
  the enum BOX now being released and only its payload array left. That remainder
  is the shallow `k_enum` drop, filed as #6696 with the `__drop_enum_<E>` shape
  native uses (`emitEnumDropViaGenFn`).

  **`strfld_reclaim_ok_types_of`, the STRING sibling, is deliberately not moved.**
  It has the identical call-arg coarseness and the identical argument would apply,
  but it is the scan whose over-admission produced the #3425 whole-compiler
  corruption, so it wants its own measurement rather than riding along.

  **The cost mattered and the first cut paid it.** Recomputing
  `borrowable_params_interproc` at the x86 unit-emit call site took the
  per-module emit-all's gen0 from **129.3 s to 745.4 s** (total 585.8 s → 1663.9 s)
  — an interproc fixpoint is ~9 passes over every function, and that site runs once
  per emitted unit. The verdict was already in scope as `b.borrowable_params`, from
  the `wp_fn_sigs` the modload driver builds ONCE outside its per-unit loop, so the
  hot path now reads it instead. Only the two single-module driver paths (arm64
  `emit_module_ir`, wasm `emit_ir_rc_bodies_from`) still compute their own, once per
  compile.

  That still left gen0 at 365.3 s, and the second half was the LOOKUP:
  `param_is_borrowable` walks the registry with a char compare per entry, and this
  scan visits every call argument in the whole program once per unit. A bare field
  access is the only argument shape the arm marks directly, so the SHAPE is tested
  first and the registry consulted only for those. Peak memory was never the issue
  at any point (2.15 → 2.33 GB); time was.

  | emit-all | gen0 | gen1 | total |
  |---|---|---|---|
  | parent | 129.3 s | 357.9 s | 585.8 s |
  | recompute the verdict per unit | 745.4 s | 832.2 s | **1663.9 s** |
  | reuse `b.borrowable_params` | 365.3 s | 447.8 s | 889.4 s |
  | + shape before registry | 163.9 s | 320.7 s | **561.2 s** |

  **Both cuts needed measuring; neither was visible from the diff.** The first is
  the one §9 already warns about (per-unit whole-program analyses), the second is
  not — a lookup that is O(registry) per call ARGUMENT reads as free until it runs
  36 times over every call in the compiler.

  Unchanged and deliberately so: a callee that RETAINS its argument.
  `keepi(v: I): I { return v; }` is refused by `borrowable_params_of`, so
  `var p = keepi(o.inner)` keeps the type marked; the probe reads `p`'s scalar AND
  its array back after the rebind loop, and measures identically on the parent.

  VERIFIED: `TestSelfHostPerModuleEmitAllFixpointX86_64` PASS 561.24 s (gen0 ==
  gen1 across 36 units, no OOM). `TestSelfHostBorrowedFieldArgReclaimX86_64` — two
  new rows: a k-sweep comparing the call spelling against the direct read, and the
  retaining-callee negative; the call-spelling row FAILS on the parent at 800 B.
  Exits agree with `fern -interp` on every probe.
  Refs #6698 #6691 #6653 #6604 #4451.

- 2026-08-11: **The unit emit stops recomputing the string-field verdict — the
  per-module emit-all fixpoint drops 36%.** Found while measuring why #6703's
  parameter threading cost 172 s in `strfld_collect_unsafe` and nothing
  measurable in the nested-struct walk #6698 threaded identically.

  **The answer was an invocation count, and counting it directly is what worked
  — two rounds of reasoning from the diff got it wrong.** Instrumented, on a
  20-line program:

  | scan | runs per compile |
  |---|---|
  | `strfld_reclaim_ok_types_of` | **7** |
  | `structfld_reclaim_ok_types_of` | 1 |

  Each `strfld` run is three whole-program passes internally (strfld /
  strarrfld / clofld), so that is 21 walks over every function for a verdict
  that never differs. Two of the seven are in the per-unit emit loop, where the
  same verdict over the same `all_funcs` / `all_structs` was already computed by
  the `wp_fn_sigs` the caller builds ONCE outside the loop. Both sites now read
  it off the FnSigs, exactly as the borrowable-params verdict has been read
  since #6698.

  | emit-all | gen0 | gen1 | total |
  |---|---|---|---|
  | before | 163.9 s | 320.7 s | 561.2 s |
  | after | **86.4 s** | **206.8 s** | **358.8 s** |

  Peak unchanged (2.15 / 9.21 GB). This also recovers a drift: §9 entries from
  earlier in the port record this gate at 354–421 s, and it had reached 561 s.

  **The output is byte-identical, and the fixpoint is the proof** — `gen0 ==
  gen1 across 36 units` is a whole-compiler equality claim, where the four
  probes checked by hand are not. (Those probes first appeared to differ; the
  comparison had `FERN_LEAKCHECK` set on one side only. An instrument that lies
  about a null result is the same hazard as the `go test` cache returning a
  stale timing to the digit, which happened once in this session too.)

  Consequence for #6703: its exemption was measured against a 561 s baseline and
  cost ~50%. Much of that cost was paid inside a scan that now runs less often
  in emit-all, so the re-measurement is worth doing before assuming the shape of
  the fix. Refs #6703 #6698 #4451.

- 2026-08-11: **The `strfldok:` read scan stops counting a BORROWABLE call
  argument as an escape (#6703).** The STRING half of #6698, deliberately left
  for its own measurement because this is the scan whose over-admission produced
  the #3425 whole-compiler corruption. Two spellings of one program, differing
  only in the final read (ten calls of `work(8)`, x86-64, `FERN_LEAKCHECK=1`):

  | read | before | after | native |
  |---|---|---|---|
  | `o.name.len()` — direct | 0 B | 0 B | 0 |
  | `slen(o.name)` — call arg | **2800 B** | **0** | 0 |

  Bigger than the struct one because the replaced string strands every ITERATION
  rather than once per call. A marked field NAME disqualifies its type from
  `strfldok:`, which gates the `k_str` arm of `__struct_drop_<T>`, the `fr_str`
  arm of `__field_reclaim_<T>` and the construction-side retain that pairs with
  them — so one call argument left the type on its arrays-only body.

  Why it is sound for strings specifically: `borrowable_params_of` refuses a param
  that is SLICED, and a slice/`trim` view into the buffer is exactly the uncounted
  alias `__fern_str_free` would dangle. It also refuses returned, stored and
  captured params, so the callee holds nothing after it returns.

  **The verdict is now threaded, not recomputed — that is the load-bearing half of
  the diff.** `strfld_reclaim_ok_types_of` takes the borrowability registry, and
  the emit paths build BOTH registries from one verdict (`fn_sigs_for_borrow`)
  rather than overriding `borrowable_params` afterwards. Without that, the
  backend would seed `strfldok:` from the interprocedural fixpoint while the
  LOWERING routed on `fn_sigs_of`'s single pass — the fixpoint admits strictly
  more borrows, so the backend would free fields the construction side never
  retained. The override shape also computed the single-pass verdict, and now a
  whole-program scan derived from it, only to throw both away.

  **#6704 is why the shape of the fix is what it is.** That attempt threaded the
  registry through `strfld_collect_unsafe` itself and cost +50% emit-all, timing
  out a CI shard. The control run isolated it: threading a `string[]` through
  that recursive walk cost **172 s with the exemption disabled**, i.e. most of the
  regression was the parameter, not the lookups or the extra emitted arms. So the
  walk takes no new parameter here. A borrowable-position field read is PARKED in
  the accumulator as a `?<callee>#<idx>:<field>` record — inert for every exact
  field-name lookup — and `strfld_resolve_deferred` settles all of them once,
  after the walk. Records are skipped for a field already marked (no verdict can
  un-mark it) and deduped, so the registry walks are proportional to the fields
  that can still change the answer rather than to call arguments.

  **The cost is inside run-to-run variance, measured against the same tree on the
  same box** — which is the only comparison worth making here, because the
  entry above measures 86.4 s / 206.8 s for the deduped gen0 / gen1 and this
  4-core container does not reproduce those figures for `main` at all:

  | emit-all (batch=8) | gen0 | gen1 | wall | peak gen0 / gen1 |
  |---|---|---|---|---|
  | `main` @ 97dad1d | 163.6 s | 283.0 s | 533.5 s | 2.24 / 9.24 GB |
  | with this change | 167.9 s | 299.5 s | 553.3 s | 2.16 / 9.22 GB |

  +2.6% / +5.8%, against the ±16% spread two samples of the same tree showed in
  #6704's session. **Quote a machine's own baseline, not the number in the entry
  above** — the gap between 86.4 s and 163.6 s for the same commit is the size of
  the hardware difference between two dev boxes, and #6704 was withdrawn partly
  on a cross-box comparison.

  Unchanged and deliberately so: a callee that RETAINS its argument.
  `keeps(v: string): string { return v; }` is refused by `borrowable_params_of`,
  so `var kept = keeps(o.name)` keeps the type marked — measured identically
  before and after at 12160 B, with `kept` read back after the rebind loop so an
  over-release would be a wrong exit, not a byte count.

  VERIFIED: `TestSelfHostPerModuleEmitAllFixpointX86_64` PASS (gen0 == gen1
  across 36 units, no OOM) on this change AND on its `main` baseline;
  `TestSelfHostBorrowedFieldArgReclaimX86_64` — two new rows, a k-sweep of the
  call spelling against the direct read and the retaining-callee negative, the
  sweep FAILING on the parent where the byte count tracks k; and the eight other
  `strfldok` suites (`FieldReclaimStr` x86-64 + wasm, `StrOnlyStructExitDrop`,
  `StructArrStrFieldReclaim`, `StructArrStrFieldHazards`,
  `StructStrFieldReclaim`, `StrFldDropGate`, `OptStructStringField`) green.
  Refs #6703 #6704 #6698 #6707 #4451.

- 2026-08-11: **The heap-bump FLATNESS probe reaches the self-host (#4365).**
  Native pins ~50 reclaim shapes by running one program at two iteration counts
  and requiring `__heap_bump_bytes()` to report the same high-water mark. The
  self-host had one heap-bump test and it pins the BUILTIN, not any shape's
  flatness — so the reclaim behaviours below were unasserted on the path where
  the port actually happens, and the fixpoint cannot see them (a compiler that
  over-allocates identically in both generations still reproduces itself).

  Ten shapes, all measured flat at N=50 vs N=200 and all required to allocate:
  nested-concat, len-receiver, stmt-temp, discarded-call (struct + array),
  call-arg-temp, literal-alloc, replaced-field, tuple-temp, nested-array.
  Native is asserted flat alongside each, as the oracle that the shape is
  reclaimable at all rather than as a byte-count comparison — the two compilers
  allocate different amounts for the same program.

  **The gate was verified to bite**, which is the point of a flatness row: a
  temporary eleventh row carrying the enum-scrutinee shape failed on both
  assertions (self-host 160 → 128, native 64 → 0, both wrapping a growing count
  through the exit-code byte). That shape is #6393 and is deliberately NOT a row
  — it is an open leak on all three backends, so gating it would pin a bug
  rather than a behaviour. A string-array-element row was dropped for the
  opposite reason: it allocates nothing on the self-host, so its flatness holds
  vacuously.

- 2026-08-11: **A borrow-only string VIEW takes its box from the frame, not the
  heap (#6713).** A slice `s[a:b]` is a zero-copy view whose 24-byte box
  `[rc=-1, data, len]` points into the SOURCE string's buffer. Sharing is why the
  box is born immortal — freeing it would attack the middle of someone else's
  allocation — and immortal is why it is unreclaimable: every `rc_dec` skips it.
  So the cost was one permanent box per EVALUATION, where native has no box at
  all. The issue's reproducer, x86-64, `FERN_LEAKCHECK=1`:

  | k | before | after | native |
  |---|---|---|---|
  | 400 | `allocs=401 frees=0` 9624 B | `allocs=1 frees=0` **24 B** | 0 |
  | 800 | `allocs=801 frees=0` 19224 B | `allocs=1 frees=0` **24 B** | 0 |

  Flat rather than merely smaller: the residual 24 B is the source literal's own
  box, and it does not move with k. This is route **2-lite** from the issue's
  comment — placement, not reclamation. Layout, the `rc=-1` sentinel and every
  consumer are byte-identical; only the storage changes, so the binding's slot
  marking, the exit sweep and `__fern_str_free`'s immortal skip are untouched.

  **The whitelist is the whole design, and it is narrow because there are two
  hazards.** A reference outliving the FRAME dangles — the obvious one. But one
  frame slot serves a SITE across every iteration, so a reference outliving the
  next execution of that site silently reads the newer view instead. Copying the
  name into a second local (`prev = cur`) is the second hazard's shape and is
  refused even though the frame is very much alive, which is why the rule is
  "every use borrows" rather than "the value does not escape the frame".
  Admitted: the `.len()` receiver, a byte index `t[i]`, a comparison or concat
  operand, and the source of a further slice — whose own data pointer aims at the
  SOURCE buffer, so it outlives the box it was cut from. Everything else, and
  anything at all inside a lambda, is refused.

  Three anonymous local slots (`!view!`, unreachable by `slot_of` for the same
  reason `!retired!` is) carry the box, so `n_locals` sizes the frame and the
  zero-init covers it with no new plumbing. The set is seeded per function as
  `VIEWFRAME:<name>` in `reclaimable_names` — LowerState is pinned at 33 fields,
  and prefix-tagged registries are the established way past that.

  wasm is deliberately untouched: its `str_slice` COPIES into a fresh inline
  block, so there is no box to place, and `view_frame_binding_ok` declines under
  `for_wasm()`.

  **The escape cases were verified to FAIL without the analysis**, which is the
  only evidence that a negative test is a test: with the borrow scan stubbed to
  admit everything, `strview-escape-alias-safe` and `strview-escape-store-safe`
  exit 95 and `strview-escape-return-safe` exits 96. Two of them needed a
  frame-reusing `churn()` call between the escape and the read — a dangling frame
  read is usually still intact, so a value guard alone does not discriminate.
  Refs #6713 #6604 #4294 #4451.

- 2026-08-11: **TRMC consume-safety — the loop frees the cells it walks past
  (#5333).** #4578 landed the TRMC rewrite; its deliberately-deferred half was
  native's borrowed→owned flip, and the flip is the whole issue. `inc_all` over
  `build(2000)`, self-host x86-64, `__heap_bump_bytes()` delta across the call:

  | | bump delta |
  |---|---|
  | before | 187 KiB |
  | after | **93 KiB** |

  A halving — peak becomes the OUTPUT rather than input + output, which is the
  FBIP result. At 50 cells the census goes `allocs=102 frees=0 live=4864` →
  `allocs=102 frees=50 live=2464`; at 300k it frees 300k cells and still returns
  the right sum.

  **`__fern_rc_dec` is the right primitive on this side.** Native reaches for
  `__fern_box_free` because it needs a SHALLOW release — the loop steals the tail
  out of the cell it is about to free. The self-host's dec is already shallow:
  payload drops are emitted by the compiler around it, not by the dec, so the
  stolen tail survives its parent without a new runtime helper.

  **The callee half alone is a use-after-free, and it measures like a win.** The
  loop's uniqueness check is a guard only if an aliased argument arrives already
  retained; without the call-site retain, `is_unique` calls every cell unique and
  the traversal frees a list its caller is still holding. Measured: `frees=30`
  and exit 95 on a `keep`-still-live probe, while the census, `__rc_underflow()`
  and every value read through the RESULT stayed clean. Only reading the ORIGINAL
  back after the call sees it.

  So both halves read ONE registry (`FnSigs.trmc_consume_fns`, "fn|param") rather
  than re-deriving the verdict — the #6703 lesson applied to a second pair: a
  path holding the verdict must never free what a path lacking it never retained.
  `emit_trmc` consults it too instead of calling the scan, so an emit path whose
  sigs lack the registry declines to consume rather than consuming uncovered.

  Two refusals, both because the retain is anchored at the direct call site:
  a payload that is not a flat scalar (a shallow free would strand it — the
  `SCons(string, SList)` sibling, byte-identical before and after), and a
  function whose **address is taken anywhere**. `var f = inc_all; f(keep)` would
  consume with nobody retaining, by a route the call site cannot see, so taking
  the address costs the verdict outright.

  VERIFIED: new `TestSelfHostTrmcConsumeIR{X86_64,Arm64}` — 7 cases each, peak
  gate at 140 KiB (93 with, 187 without), value guards, the shared-input and
  shared-twice negatives, the string-head and fn-value refusals, and the 300k
  deep-stack case; `TestSelfHostTrmcIR*` green. Refs #5333 #4352 #4578 #6703 #4451.

- 2026-08-12: **`__heap_mark` / `__heap_release_to` reach the self-host backends
  (#6728).** The arena checkpoint had no lowering anywhere in `examples/self_host`,
  so `bin/fern-selfhost` refused a program `bin/fern` builds and runs — and the
  pair exists FOR the self-host, whose per-module emit is the workload it was
  built to keep inside the arena. Both register backends now lower it INLINE, for
  the reason `heap_bump_bytes` is inline: they read and write the arena globals
  directly, so a call would only add an ABI.

  Release restores the cursor AND both freelist head tables from a `.bss` shadow.
  Snapshotted, not cleared: a block allocated and freed inside the window leaves
  a head above the mark, which a later pop and a later bump would both hand out.
  The self-host needs two shadows where native needs one — it carries a 65536-word
  small-class table plus the #3425 large tier.

  **The bug worth recording is the stack discipline, not the arena.** A first cut
  popped the mark and pushed nothing, which is right for a void builtin and wrong
  for this IR: every op keeps one value per expression, and statement position
  drops it (`raw_store8` pushes its operand back for exactly this reason). The
  missing push made the statement's drop pop a value nothing pushed, so the frame
  was corrupt and the NEXT allocation segfaulted — the release itself was fine,
  and a probe that released and returned immediately passed. It took a second
  allocation after the release to show it.

  Measured, x86-64: 2000 `__raw_alloc(64)` grow the bump 125 KiB, and the release
  returns it to under 1 KiB of the mark. wasm stays refused and refuses UP FRONT
  with E066 naming the targets that do provide `arena` (#6705), rather than
  bailing late on an unknown symbol.

  VERIFIED: new `TestSelfHostHeapMarkIR{X86_64,Arm64}` — roundtrip, reclaim,
  reuse-after-release and the zero-mark no-op. The arm64 leg carries extra weight
  here: `-target arm64-linux` assembles and links in process, so it is the only
  path putting the emitted checkpoint through the self-host's own assembler.
  Refs #6728 #6705 #5330 #3425.

- 2026-08-12: **An enum field bound to a LOCAL is retained where it escapes, not
  where it is bound (#6121).** #6049 gave a direct enum field read stored into a
  container (`[first.node]`, `xs.append(p.node)`) its Perceus dup; routing the
  same value through a local first defeated it, because the container element is
  then a bare ident and the ident arm only retains an rc-CONTAINER slot (array /
  string / tuple), which an enum slot is not. `__struct_drop_P`'s `k_enum` arm
  freed the box the array still pointed at.

  | | interp / native | self-host before | self-host after |
  |---|---|---|---|
  | `var tmp = first.node; [tmp]` | 102 | **1** | 102 |
  | `var tmp = first.node; out.append(tmp)` | 102 | 1 | 102 |

  Wrong on all three backends before (x86-64, arm64, wasm), correct on all three
  after — the analysis is shared `irlower` so the backends only inherit it.

  **The dup is at the STORE, not the bind, and that is the decision the issue
  left open.** A dup at the bind would balance nothing: a plain enum slot is not
  swept (an enum box and its payload are never swept — the IR leak-mode
  invariant), so every non-escaping alias would leak permanently, and the
  escaping one would need the sweep back to avoid double-counting. Retaining at
  the store instead reproduces the direct read's balance exactly — field rc 1 →
  store dup 2 → `__struct_drop_<T>` 1 → the container keeps it — and costs
  nothing when the alias never escapes. Two containers off one alias take two
  dups, which is the same rule, not a special case.

  `mark_enum_field_alias` seeds `ENUMALIAS:<name>` in `reclaimable_names` at the
  bind (appended, not pre-scanned: the bind always precedes the store, and
  `enum_field_read_type` needs slot types the pre-scan does not have), and the
  three sites that already consult `enum_field_read_type` — the array-literal
  element, the in-place `append`, and the clone-form `append` — consult the mark
  for a bare ident too. Rebinding the name to a non-alias value does not clear
  the mark: a stale mark costs one retain too many, which leaks and never
  over-frees.

  **Not this defect, and filed as #6758:** the same loop-local struct leaks
  ~120 B per iteration with *no* field read at all (`var first: P = mkp(i); t = t
  + first.pos` measures the same 120 B as the aliasing spelling), and 88 B for an
  `i32[]` field in place of the enum one. Native is flat on both, so it is a
  self-host struct-local reclaim gap, not an alias one. Measured identical on the
  parent commit.

  VERIFIED: new `TestSelfHostEnumFieldAliasIR{X86_64,Arm64}` — three cases, all
  three exit 97 (value read back wrong) on the parent commit on both backends;
  cross-backend exit agreement on wasm (wasmtime) for the same programs.
  Refs #6121 #6049 #4365 #4451.
- 2026-08-12: **`Option[string[]]` / `Result[string[], _]` — the per-ELEMENT
  release (#6495).** Two of the three rows #6495's leak list left open, and one
  cause between them. Re-measured on `cb2dbf2` before touching anything, 400 then
  800 iterations, `FERN_LEAKCHECK=1`, self-host x86-64 against native:

  | shape | before | after | native |
  |---|---|---|---|
  | `Option[string[]]` | `allocs=1600 frees=0` **51200 B** | `1600/1600` **0** | `800/800` 0 |
  | `Result[string[], string]` | `allocs=1600 frees=0` **51200 B** | `1600/1600` **0** | `800/800` 0 |
  | `Option[Option[i32[]]]` | `allocs=1200 frees=0` 48000 B | unchanged | `1200/1200` 0 |

  Exactly x2.0 per doubling before, flat after. The third row is untouched and
  still open — it is the OTHER cause the list names (`rcpayload_option_cand` has
  no branch whose payload is itself an Option), not another admission.

  **`frees=0`, not a partial release, and that is the tell.** The class admits a
  payload its drop releases WHOLE, and `is_leaksafe_array_field` decides which
  arrays qualify — a flat scalar buffer one `__fern_rc_dec` frees with no inner
  walk. It refuses `string[]` CORRECTLY (a plain dec frees the buffer and strands
  every element box), and nothing else claimed the shape, so the candidate was
  refused outright rather than half-released.

  **The release already existed**, which is why this is an irlower change with no
  runtime work: `__fern_str_arr_free` walks the element boxes then frees the
  buffer, rc-guarded, and has backed the string[] LOCAL sweep and the string[]
  struct FIELD reclaim since #4355 on all three backends (wasm routes it to
  `$__fern_arr_dec_ptr`).

  **Freshness is per ELEMENT, and it is the whole soundness argument.** The scalar
  case only ever frees the one buffer it was handed, so a fresh literal suffices;
  here each element box is freed too, so ONE aliased element would be released out
  from under its owner. `all_fresh_string_elems` demands every element be a literal
  or a fresh producer — the same rule the bare `string` payload already applied,
  one level down.

  The boolean that carried "is this payload a string" is **gone**: three payload
  kinds now need three helpers, so `OptRcFrees` carries the helper NAME
  (`opt_payload_freefn`), `emit_opt_str_payload_drop` collapsed into
  `emit_opt_payload_drop_via` (the two differed in exactly one call), and the
  precise-drop kind `opt-strpayload` became `opt-payloadfree:<helper>`. The one
  place that was NOT derivable — an optret_pending entry carries the release as a
  single character — got `optret_payload_tag` next to the mapping it mirrors, so a
  fourth kind cannot get a helper in one and no tag in the other. A missing tag
  there falls back to the plain box dec, which for a string[] strands every
  element.

  VERIFIED: new `TestSelfHostOptStrArrPayloadX86_64` — 7 rows on exact alloc/free
  balance (a bump bound cannot separate "freed the buffer" from "freed the
  elements", and the element boxes are most of the bytes): Option, Result, the
  un-annotated literal, an element-read value guard, the scalar-array control, and
  the two hazards. Three rows in the shared `optStructReclaimCases` table carry it
  to **arm64 (qemu) and wasm (wasmtime)**. The hazards — an ALIASED payload read
  after the match, and an arm binding that escapes — stay REFUSED with a nonzero
  remainder, which is the conservative side: releasing there dangles rather than
  leaks. Every value matches native (`__rc_underflow()` probes are self-host-only,
  so `fern -interp` is not their oracle — they carry a 99 sentinel instead).
  Refs #6495 #6360 #4355 #4451.

- 2026-08-12: **A struct factory that builds its array field in a LOCAL is
  strict-fresh (#6758).** `return_value_is_strictfresh_struct` admitted an array
  field only as a direct LITERAL, so `var xs: i32[] = [k, 8]; return Q { xs: xs,
  pos: 1 };` — the ordinary way to write a factory — never entered
  `return_fresh_struct_ret_fns`. Every caller's `var q: Q = mkq(i)` then earned no
  reclaim credit at all, and box plus buffer leaked per call.

  | producer, per iteration | before | after | native |
  |---|---|---|---|
  | `var xs = [k, 8]; return Q { xs: xs, … }` | 88 B | **0** | 0 |
  | `var xs = []; … xs = xs.append(i); return Q { xs: xs, … }` | 104 B | **0** | 0 |

  Unbounded before (176 B at 2× the rounds), flat after; identical on arm64, and
  wasm went 56 → 0. The emitted `churn` carried no rc call whatsoever, which is
  the signature of a missing credit rather than an unbalanced one.

  **The proof is the one `ARR:` already uses, one container out.** That entry
  admits a returned array LOCAL when it is literal-initialised, only ever
  self-appended to, and does not otherwise escape (`body_returns_local_built_arr`);
  the same three predicates prove a field value is frame-built, with exactly one
  escape forgiven instead — appearing as a bare field value of the returned
  literal (`body_unsafe_for_allow_structret`, the `_allow_ret` variant one shape
  over).

  **Two refusals, and the second is the one the literal form could never raise.**
  A field seeded from a PARAM keeps leaking, because crediting it would have the
  caller's reclaim free the caller's buffer. And ONE local answering TWO fields
  (`Q { xs: xs, ys: xs }`) is refused outright: the box carries a single rc while
  `__struct_drop_Q` would free that buffer once per field — two literals are two
  buffers, so only the ident spelling can express the double free.
  `local_decl_count` guards the third hazard: the literal-init witness answers on
  the first declaration it meets and both escape scans are name-keyed, so a
  shadowed `var xs = param` in another block would otherwise ride the first
  declaration's verdict.

  **Not fixed here, and the rest of #6758:** the same loop-local struct with an
  ENUM field instead of an array still leaks 120 B/iteration (72 on wasm), and a
  bare rc-payload enum local built in a loop leaks 160 B. Neither is an array
  field, so neither passes through this predicate at all.

  VERIFIED: new `TestSelfHostFreshRetLocalArrFieldIR{X86_64,Arm64}` — two leak
  rows exiting 98 on the parent commit on both backends, two negatives that exit 0
  on both sides and are there to stay that way; cross-backend exit agreement on
  wasm. Refs #6758 #6491 #3457 #4451.

- 2026-08-14: **TRMC detector admits mixed-variant recursive arms (#5334).**
  `trmc_eligible` refused a second recursive arm that built a DIFFERENT ctor
  (`Cons(h+1, self(t))` in one arm, `Neg(h-1, self(t))` in the next), so a list
  enum with two cons-shaped variants kept the O(n)-stack recursion; native's
  `detectTrmc` already carries `ctorVarIdx` per arm and admits it. The self-host
  now does too, with one restriction native does not need: the hole is filled by
  an `op_struct_set` at a compile-time field INDEX, not at a byte address, so all
  recursive ctors must agree on payload arity for that index to be a single
  constant. Mixed arities (`Cons(i32, List)` beside `Wrap(List)`) still bail.
  `emit_trmc` reads the ctor per arm rather than reusing the detector's — without
  that half, an admitted `Neg` arm would have stamped `Cons` on its nodes.

  The new predicate is strictly weaker than the old one, and on a function every
  recursive arm of which already shared a ctor the emitted bytes are unchanged:
  same arity, and the per-arm ctor resolves to the same name.

  **Deliberately NOT widened, both matching native:** `when`-guarded arms — a
  guard makes arm coverage non-structural, so "every variant has an arm" stops
  meaning "some arm runs"; an iteration where nothing matches falls out of the
  loop with the last node's tail hole never filled. Admitting them needs a
  coverage rule (an unguarded arm per variant) that native does not have either.
  And multi-statement arm bodies — admitting leading statements means lowering
  them inside a body that deliberately runs without the RC sweeps. Both wait on
  native's sibling widening (#5344, under #4402).

  VERIFIED: `TestSelfHostTrmcIRX86_64` (mixed-ctor value where a wrong-variant
  rewrite scores 60 instead of 6; mixed-ctor 200k-deep; a narrow one-payload
  scrutinee cell shallow-freed under a wider built node; plus asm self-call
  witnesses pinning mixed arity, a guarded arm and a multi-statement arm to the
  plain recursion) and `TestSelfHostTrmcWasmIR` (the three positives). The deep
  case exits 134 on the parent commit, and 60 conformance fixtures emit
  byte-identical wat across the change. Refs #5334 #4352 #5344 #4402 #4451.

- 2026-08-14: **A borrowed array PARAM self-reassigned with `.with` stops
  writing through into the caller (#6185).** The last shape of #6158's family:
  `function patch(buf: i32[], at, v) { buf = buf.with(at, v); return buf; }`
  returned 77 where interp and native return 7, because the in-place `arr_set`
  landed in the caller's buffer.

  It stayed open through #6158 and #6170 because the gate cannot decide it from
  aliasing. `box_mutated_scalar_captures` rewrites a captured `x = v` into
  `x = x.with(0, v)` on a 1-element cell, and the param-lift threads that cell
  to the lifted body as a plain array param (#5301). So the two cases arrive
  identically — a non-`own` array param, self-reassigned with `.with` — and
  demand OPPOSITE lowerings:

  | | the caller's array | a capture cell |
  |---|---|---|
  | may the caller read it after? | yes | yes |
  | required lowering | CLONE | in-place STORE |

  Both answer "yes" to the only question an aliasing analysis asks, so no
  sharpening of `aliased_names` or `borrowed_names_of` can separate them. What
  differs is the MODE — a borrow versus a by-reference cell — and `ParamDecl`
  carries no mode beyond `own`.

  **Native never has to choose**, which is why it is right here. It distinguishes
  the two structurally, twice over: a capture cell reaches a lifted body as a
  slot in the heap `__env` block (`*ast.CaptureRef{Offset}`), never as a
  parameter, and its write is spelled `cell[0] = v` — an index-assign that is
  E056 in surface source and synthesizable only after the checker, so it can
  never be confused with a value-semantics `.with`
  (`internal/closureconv/boxcapture.go`, `internal/ir/ir.go` `b.assign`).
  Porting either mechanism whole is a large change; porting the DISTINCTION is
  not.

  The fix carries the mode on the name, the one channel that survives to the
  gate: `box_rewrite_stmt` now names its cell `$cell$x`, reusing the
  collision-free `$` convention `$wc$` and `$lamret$` already rely on. The
  lifted capture param inherits that name for free (the param takes the
  capture's name verbatim), so `borrowed_names_of` can credit every OTHER
  non-`own` array param as a member — which is what native's `borrowedParam`
  set does — and exempt `$cell$` alone.

  Two deletions come with it. `borrowed_rebind_closure`'s `borrow_srcs`
  parameter is gone: a param is now a member, so the transitive "Y is itself
  borrowed" arm subsumes it. And `box_rewrite_stmt` grew the `StmtDefer` arm it
  was missing — a boxed read inside a defer was silently reading the cell
  pointer as a scalar.

  A second instance fell out: a LIFTED lambda's own DECLARED array param
  (`function (a: i32[]) { a = a.with(0, 9); … }`) had the same bug, 104 vs 24.
  It is why the fix distinguishes capture params from declared ones rather than
  exempting `__lam_*` bodies wholesale.

  VERIFIED: `TestSelfHostOuterMutCaptureWasmIR` all 17 rows green by name —
  including `loop-accumulator` and `outer-and-inner-write`, the two the naive
  fix broke — plus the six `mut_capture_*` parity programs and both `own`
  allocation guards (which read 91 if any clone crept in, and read 7), each run
  end-to-end through the self-host compiler to a native arm64-darwin binary.
  `TestSelfHostBorrowedWithInPlaceIRX86_64` goes from 9 rows to 11 and its
  #6185 row flips from the pinned 77 to 7. Refs #6185 #6158 #6170 #5301 #4451.

- 2026-08-14: **A COW'd Map shared its key and value columns with the buffer it
  was copied from (#6242).** `__map_cow_inplace` and `__map_clone` build the
  fresh kv buffer with one `__memcpy`, which is shallow over the pointer-shaped
  columns. Both handles then reach rc==1 independently, so BOTH deep drops run,
  and whichever fires first takes the other's memory. Measured before and after
  on `635abf3`, 300 rounds, one COW copy each, `-sanitize`:

  | | arm64-darwin | x86-64 |
  |---|---|---|
  | before | `allocs=4200 frees=4500` — 300 over-frees | `rc over-release (double free)` |
  | after | `allocs=4500 frees=4200` — the pre-existing key leak, no over-free | clean |

  The value read is the plainer signal: the #6242 repro returns the `get_or`
  fallback (-1) on arm64-darwin before, 7 after, and 7 under `-interp` on both
  sides. On native wasm the corrupted probe loop does not terminate at all, so
  the before-picture there is a HANG rather than a wrong answer.

  **The bug reads as ABI-specific and is not.** On a two-word-string target the
  key slot is a `(data, len)` CELL that carries no refcount and
  `__drop_map_str_keys` frees outright, so two maps sharing one cell free it
  twice; on a single-word target the slot is the string data pointer and the two
  drops double-dec it. x86-64 survived the second shape often enough to look
  clean, which is why the issue recorded it as arm64-only — the sanitizer says
  otherwise.

  **`string as usize` is the portable rebox, and the one thing it does NOT do is
  count.** The cast allocates a fresh cell on a two-word target and is the
  identity on a single-word one, which is also how `__map_own_key` tells the two
  apart at runtime with no new builtin and no tag: if the cast handed back the
  same pointer, there is no cell and an inc is the whole claim. A first cut
  relied on the rebox alone and segfaulted — the second cell's drop released
  bytes nobody had counted — and it took a SECOND call to the churning function
  to show, because nothing recycled the freed block until then. A first cut
  before that copied the bytes with `"" + s` instead, which segfaulted straight
  away: the concat temp is swept at scope exit and the slot kept the address.

  **Two value columns stay shared, and the second is the more interesting
  refusal.** A struct value (valKind 4) needs the same per-field walk in the inc
  direction as its generated `__drop_map_struct_<T>`, not one `__fern_rc_inc`.
  A STRING value is released by `__drop_map_str_values` — the same generated
  body as the key column, one column over — and could not be claimed here even
  if it needed to be: `mapValKindTag` files it under valKind **1** together with
  every other unreclaimed pointer (tuple, generic enum, slice, runtime handle),
  and the drop is emitted off the STATIC value type, so the copy side has no
  runtime test the way `keyKind == 1` gives it one for a string key. Giving it a
  kind of its own is an IR tag-space change and a separate slice.

  Measured rather than assumed, `Map[string, string]` COW churn, 8 rounds:

  | | arm64-darwin | wasm | x86-64 | interp |
  |---|---|---|---|---|
  | before | SIGSEGV | — | ok | ok |
  | after | wrong value | wrong value | ok | ok |

  So the key claim takes the two-word targets from a crash to a wrong read,
  which is an improvement and NOT a fix, and the remaining half is the value
  column exactly as described. The shape is deliberately absent from the new
  suite rather than pinned to behaviour known to be wrong.

  Nothing changes for the self-host, which emits no map drop at ALL (no
  `__drop_map_str_keys`, no `__map_drop_values`): its `as usize` is the identity,
  so the rebox allocates nothing, and the inc lands on a block nothing ever
  frees. Measured identical before and after on the self-host wasm leg. That
  also makes this a PREREQUISITE for the map string K/V slice this issue still
  lists as open — #6242 is what "can be widened once the columns are retained"
  refers to, and the self-host cannot start dropping map columns until the copy
  side stops sharing them.

  VERIFIED: new `TestMapCowColumnOwnership{Interp,X86_64,Wasm,Arm64}` (the
  source outliving its copies, an array-value column, and copy-source
  independence), `…NoUnderflow{X86_64,Wasm,Arm64}` and `…Bounded{…}`. The wasm
  value leg FAILS on the parent commit and the wasm rc leg HANGS there;
  `TestMapCowTempBinding*` stays green. Refs #6242 #4355 #4353 #2704 #6227 #4451.

- 2026-08-14: **The COW chain leaked everything the copy claimed, quadratically
  (#6828).** The Map reassignment-overwrite (`a = <a COW copy of a>`) released
  the old handle with the buf-and-handle free alone — #6227's workaround, which
  existed because the column walks could not run while the copy shared the
  columns. Once #6242 gave the copy its own claim that narrow free became a leak
  of the whole claim per copy, and a CHAIN (round N's copy becomes round N+1's
  source) reboxes N key cells and then drops the round-N map without freeing any
  of them. `__heap_bump_bytes()` over a 100/200/400-round chain:

  | | base (pre-#6242) | with #6242 | with the release widened |
  |---|---|---|---|
  | arm64-darwin | 30976 → 61952 (2.0x) | 357328 → 1354704 (3.8x) | 32576 → 65152 (2.0x) |
  | wasm | 20736 → 41472 (2.0x) | 347088 → 1334224 (3.8x) | 22336 → 44672 (2.0x) |
  | x86-64 | 34176 → 68352 | unchanged | unchanged |

  **The single-word side is genuinely unaffected here**, which is the opposite of
  #6242's trap and worth stating for that reason: there the key claim is one inc
  on the string data, so a missing release inflates a count on a block the chain
  keeps reachable anyway, where the two-word claim ALLOCATES a cell per entry per
  copy and the leak is immediate.

  **#6828 itself does not reproduce.** It was filed as a pre-existing
  use-after-free surviving a revert of #6242, and the reading behind it — `-sanitize`
  reporting `use-after-free (touched a quarantined block)`, exit 124, on the
  multi-round churn program from both the base and the fixed compiler — was taken
  from the intermediate cut of #6242 (the rebox WITHOUT the counting inc), not the
  landed one. Re-measured on the merge: the base compiler reports
  `use-after-free` on arm64-darwin and `rc over-release` on x86-64, exit 124 on
  both, and `978a177ef` is clean on both. The leak above is what a churn-shaped
  probe finds there now, and it is why the issue was worth working rather than
  just closing.

  The release may only cover the columns `__map_own_copied_cols` claims: the
  string-key column and the array-value column (via the kind-guarded
  `__map_drop_values`, a no-op for every other kind). A string or struct VALUE is
  still shared with the copy, so walking it here would free what the new handle
  reads — those two keep leaking until the claim widens, which is the same
  boundary #6242 drew.

  Nothing changes for the self-host, whose own lowering emits no map drop at all;
  this is `internal/ir`, so the effect on it is via the compiler that builds it.

  VERIFIED: new `TestMapCowChainReclaim{Interp,X86_64,Wasm,Arm64}` (array,
  string and struct value columns, a live alias, a `keys()` snapshot, scalar
  keys) and `TestMapCowChainBounded{X86_64,Wasm,Arm64}` (the ratio probe above
  plus `__rc_underflow_count()`). Refs #6828 #6242 #6227 #4451.

- 2026-08-16: **Map `.get()` box reclaim, self-host — SHIPPED (#6875).** The
  last conformance divergence on all three self-host legs. `alloc_flat_map_get`
  now reads `flat` on x86-64, arm64 and wasm, and the ACTIVE section of every
  `selfhost-*-known-divergences.txt` is empty.

  Two things the issue got wrong, both worth keeping straight. There is no
  rebox site to reclaim: `m.get(k)` lowers to ONE opaque `ir.op_map_get`, so
  the box is allocated inside hand-written backend runtime text, not in the
  lowering the way native's `emitMapGetRebox` does. And wasm needed no runtime
  change at all — `__fern_rc_dec` already maps to `$__fern_arr_dec`
  (`wasm_ir.fern:777`, `:7277`), a shallow rc-gated size-class release that
  works on the `$__fern_str_box` blocks `$__fern_map_get` was already using.

  The register backends were the gap, and only because their box had no rc
  header: `__fern_map_get` built it with a raw `__fern_alloc(16)`, so a dec
  would have read `rc` at `[box-8]` — the previous block's tail.
  `__fern_map_get` was the last Option producer still on the raw alloc;
  `opt_make` and `__fern_read_line` were migrated to `__fern_arr_box` earlier
  for exactly this reason.

  Four parts: `__fern_arr_box(2)` on both edges of both register backends;
  `map_get_or`'s hand-written freelist push replaced by `__fn___fern_arr_dec`
  (a headered block threaded onto the freelist corrupts it — a swap-rule
  companion, not an optional tidy); a scrutinee reclaim in `lower_stmt_match`
  via the existing `emit_scalar_enum_box_free`, refusing an `@` binding as
  native does; and the fresh string-literal key freed through the
  `is_fresh_str_temp` predicate `op_map_set`'s `kconsume` already uses, so a
  live local is never freed.

  **Do not size the box at `__fern_arr_box(1)`.** 32 B is the exact fit and
  passes every rc probe, but `url_codec` then reads a `Map[string, string[]]`
  value array as empty, with NO over-release reported — a stale uncounted alias
  to a 32-byte block being recycled as the fresh box. That is #6880, a latent
  defect this sizing merely avoids.

  Still leaking the box afterwards, unchanged in complexity class and matching
  native: a `return` out of an arm, `var o = m.get(k); match (o)`, and
  `m.get(k)?`. The first wants the existing `optret_pending` machinery
  (`irlower.fern:531-541`, consumed at `:21917`).

  VERIFIED: new `map-get` row in `TestSelfHostHeapBumpFlatIRX86_64`
  (mutation-checked — it FAILS with the lowering reverted), the three
  `alloc_flat_map_get` known-divergence rows deleted so the fixtures legs fail
  if it regresses, and the case run by hand on all three backends.
  Refs #6875 #6880 #6561 #6874 #4451.

- 2026-08-16: **The loop-body construction move judges the DECLARATION-to-
  CONSTRUCTION interval, and sees `?` (#6869).** The self-host mirror refused a
  whole loop body on any `return` / `break` / `continue` anywhere in it, and
  matched statement KINDS only — so `?`, which the parser desugars to the unary
  `try_` and is therefore an expression, was invisible to it. Both halves are
  gone. `rc_ml_one_loop_body` now keeps a per-statement early-exit PREFIX COUNT
  and admits a name only when the count at the construction equals the count at
  that name's declaration, which is native `markLoopBodyConstructionMoves`'
  `exitsBefore[at] == exitsBefore[d]`. No signature changed: the filtered
  `allow` set is rebuilt per statement, so `rc_ml_move_ok` and
  `rc_ml_construction_moves` are untouched.

  The over-refusal is the half the issue filed. The half it did not file is the
  one that mattered: with a `?` between the declaration and the push, native
  REFUSES the move and the self-host ALLOWED it — a value handed to a container
  past an early exit the analysis could not see. That is the use-after-move
  direction, not a leak.

  Per-round `__heap_bump_bytes()`, x86-64, churn twice at n=200 and subtract:

  | loop body | native | self-host before | self-host after |
  |---|---|---|---|
  | no early exit (control) | 0 | 0 | 0 |
  | `continue` after the push | 0 | 72 | 0 |
  | guard-clause `return` before the declaration | 0 | 72 | 0 |
  | `?` between the declaration and the push | 48 | 0 | 72 |

  The `?` row is non-zero on both sides afterwards, and that is the correct
  reading: a refused move keeps a retain whose escape taint suppresses the
  matching release, so the element leaks — on native too. Reclaiming it is a
  separate question from seeing the exit.

  `rc_ml_loop_prologue_len` left with the veto. It skipped the range-`for`
  desugar's advance-assign + `if (done) { break; }` so that guard could not
  disqualify the body; a prefix COUNT does not need the exemption, because the
  guard precedes every declaration in the body and so shifts each name's count
  and each construction's by the same one, and the test is an equality.

  VERIFIED: four new `TestSelfHostRcPlanDiff` rows. Before the fix the two
  over-refusals read native `v` / self-host empty and the `?` row native empty /
  self-host `v`, so three of the four discriminate and the fourth is the
  control. New `loop-push-behind-guard-clause` row in
  `TestSelfHostAllocDifferentialX86_64`: native 0 KB / self-host 14 KB (14x,
  bound 8x) before, 0 KB / 0 KB after. Plus the 151-test rc / move / loop /
  reuse / drop subset of `internal/e2eselfhost`, both self-host fixture legs
  (wasm + x86-64), and the per-module whole-compiler fixpoint.
  Refs #6869 #6533 #4451.

- 2026-08-16: **An expression-position `.append` mutated a receiver that is read
  again (#6891) — a WRONG ANSWER, not a leak.** `op_arr_push` consumes its
  receiver, and at rc==1 with spare capacity the grow helper appends into the
  receiver's own buffer and returns that pointer. The self-host applied that to
  every expression-position `.append` on a bare array ident, so
  `sink(roomy.append(20)); sink(roomy);` read four elements where `-interp` and
  native read three. rc is the wrong test — the receiver is uniquely referenced
  and still read twice, and rc counts references, not uses.

  Fixed the way native's `emitArrayPush` does since #4838: bracket the receiver
  with a retain/release around the push (`append_copy_recv_slot`), which raises
  its rc and sends the helper down its copy path. Two shapes stay in place
  because no later intra-function read can observe the mutation — the
  self-reassign `a = a.append(v)` and the accumulator tail
  `return acc.append(v)` — and that exemption
  (`append_inplace_names_of`) is load-bearing, not an optimisation: without it
  the compiler's own AST walkers copy the whole accumulated array once per
  visited node.

  **The census the exemption counts against had two blind spots**, found by
  auditing it rather than by a failing case: `collect_append_recvs_expr` had no
  `ExprMapLit` and no `ExprFString` arm, so `Map { "k": f(xs.append(v)) }` was
  invisible and a name whose every VISIBLE occurrence was exempt kept an
  exemption it had not earned. The same census feeds the #4873 may-grow param
  flags, where the same hole cost a caller-side bracket rather than a copy.

  **The new bracket SUBSUMES the two statement-level ones #6008 added**, so
  `append_alias_recv_slot` went with them. Checked rather than assumed: the
  emitted asm for `var x = xs.append(v)` / `x = ys.append(v)` is byte-identical
  before and after, and so is a whole-module compile of `checker.fern`.

  **Wasm needed a second half nobody had looked at.** Its 8-byte-slot helpers
  `$__fern_arr_push_i64` / `$__fern_arr_push_f64` carried NO rc gate — only the
  4-byte `$__fern_arr_push` got one in #4873 — so a bracketed `i64[]` / `f64[]`
  receiver was appended into anyway and those two cases stayed wrong on wasm
  after the lowering was fixed. They now carry the same gate, the same same-cap
  un-share copy and the same rc==1 cliff counters. The register backends route
  every element width through the one gated `__fern_arr_push`.

  Exit code of the minimal repro against the `-interp` oracle (41):

  | | before | after |
  |---|---|---|
  | self-host x86-64 | 82 | 41 |
  | self-host arm64 | 82 | 41 |
  | self-host wasm | 82 | 41 |

  VERIFIED: new `TestSelfHostAppendValueSemantics{X86_64,Arm64,WasmIR}`, every
  expectation taken from `fern -interp` rather than written down — 15 of its 21
  subtests FAIL on the parent commit (5 per leg) and the other 6 are the two
  deliberate controls. `alloc_flat_consumed_append` STAYS listed in all three
  `selfhost-*-known-divergences.txt`: it carried two divergences and only the
  value half is closed, so the rows now say ALLOCATION only.
  Refs #6891 #6501 #4838 #4827 #6008 #4451.

- 2026-08-16: **The #6545 port is UNSOUND on the self-host and was reverted.**
  Native's StructLit construction-move arm moved from `arrElemIsRcTracked` to
  `rcTrackedSlotType` (adding `StringType`) in #6545, and mirroring that
  predicate in the self-host makes a struct literal MOVE a bound string into
  the field. It does close the leak — a fresh-concat field went 2152/4000 to
  232/0 bytes per round against native's 64/0 — but
  `TestSelfHostRcPreciseDropX86IR/strdrop-two-alias-detector` went 0 to 1: an
  over-release, which is a double free, so the port stayed out.

  **The mechanism recorded here at the time was wrong, and it cost the next
  attempt a day.** It read the failure as the FIRST literal moving, and blamed
  node identity — "the self-host cannot reproduce this positionally because
  desugared idents are stamped 0/0". Both halves are false; the 2026-08-18
  entry at the end of this log is the correction.

- 2026-08-16: **The other two string-reclaim rows are NOT what their issues say.
  Measured, not reasoned.**

  `alloc_flat_nested_string_array` (#6527) is filed as the self-host using the
  pointer-element drop where a `string[][]` needs the string-aware walk. It does
  not: `arrarr_free_helper_of` already picks `__fern_strarrarr_free` from the
  slot's `arrarr_elem` kind, and a literal-rows `string[][]`
  (`var outer = [["a"+k], ["b"+k]]`) measures **flat** on the self-host today.
  What the conformance case actually hits is the CREDIT: `arrarr_lit_is_fresh`
  requires every row to be an array LITERAL, and the case writes
  `var outer: string[][] = [inner, [...]]` with `inner` a local. An ident row
  earns no `ARRARR:` credit, so nothing is walked and the exit sweep shallow-decs
  both slots. Closing it means admitting a row that is a bare owned local MOVED
  into the literal, and then keeping that local out of the exit sweep — the sweep
  runs in slot order, so `inner`'s dec currently precedes `outer`'s walk and a
  naive credit is a use-after-free, not a leak. That is a construction-move
  slice, not a drop-helper slice.

  `alloc_flat_method_identity_return` (#6544) is filed as the receiver-method
  spelling being excluded from owned-result reclaim. The self-host excludes
  receiver methods from every fresh-return registry it has —
  `str_fresh_ret_fns_of`, `tuple_fresh_ret_fns_of`, `map_fresh_ret_fns_of` and
  `return_fresh_struct_ret_fns_of` each gate on
  `funcs[k].receiver_type.len() == 0` — so
  there is no gate to widen, there is a registry to extend to method keys
  (`<Type>.<method>`) plus a reclaim site for a method result consumed by a
  `.len()` or a field read. All three of the case's shapes leak, per 50/100
  rounds, self-host vs native:

  | shape | self-host | native |
  |---|---|---|
  | `base.drop(2).len()` | 2896 / 5648 | 64 / 0 |
  | `base.tail(4).to_owned().len()` | 5272 / 10448 | 32 / 0 |
  | `b.relabel("x").tag.len()` | 4848 / 9600 | 0 / 0 |

  Both rows stay listed. Their divergence text is unchanged: it describes the
  measured behaviour correctly even where the attributed CAUSE does not survive
  contact with the code.

- 2026-08-16 (later): **#6544's remaining work is narrower than "extend a
  registry" — the method-key MECHANISM already exists.** Reproduced first: the
  case prints `flat` on native and `grows` on the self-host today, so the leak
  is live.

  `is_fresh_ret_binding` (`irlower.fern:29265`) already resolves a method key —
  its `ExprFieldAccess` arm looks up `recv_type + "." + fa.field`. Nothing needs
  inventing there. Two things stop it firing for strings:

  1. The four registries never EMIT a method key. `str_fresh_ret_fns_of`
     (`:25758`), `tuple_fresh_ret_fns_of` (`:25827`), `map_fresh_ret_fns_of`
     (`:25939`) and `return_fresh_struct_ret_fns_of` (`:29181`) each gate on
     `receiver_type.len() == 0` and append a bare `name`.
  2. The string and tuple call sites pass `recv_type` as a literal `""`
     (`:25550`, `:26110`, `:26134`), so the method arm short-circuits before the
     lookup. The struct sites (`:11442`, `:26178`, `:29304`, `:36705`) pass
     `v.type_name`.

  The subtlety worth naming, because it is what the struct path is quietly
  relying on: `v.type_name` is the BINDING's declared type, not the receiver's.
  That coincides for an identity-returning method (`(b: Box) relabel(...): Box`),
  which is every shape the struct path admits today. It does NOT coincide in
  general, and the string shapes are where it first bites —
  `(s: string) tail(n: i32): str` binds `str` while the key would have to be
  `string.tail`. So the slice is: emit `<recv>.<name>` keys from the registries,
  pass a real receiver type at the string/tuple sites, and normalise `str` to
  `string` the way native's `ast.ReceiverTypeName` does — not simply widen the
  `""`.

  Measurement of record for the three shapes is the table above; it stands.
  Refs #6544 #4451.

- 2026-08-17: **#6544 is NOT a registry slice — it needs the string
  return-transfer inc. One of its four leaks is closed; the other three are
  blocked on that.** The case was decomposed per shape, self-host x86-64,
  `__heap_bump_bytes()` at 50 and 100 rounds, `(b2-b1)/100` = bytes per round:

  | piece of the case | self-host B/round | native |
  |---|---|---|
  | `(i % 8).to_string()` concat temp | 32 | 0 |
  | `base.drop(2).len() + base.tail(4).to_owned().len()` | 96 | 0 |
  | `b.relabel(…).tag.len()` (both lines) | 120 | 0 |
  | `base` + `base.tail(0).len() + base.len()` (the identity controls) | 0 | 0 |

  **Extending the fresh-return registries to method keys cannot close any of the
  three big rows, and the reason is the case's own design.** `tail`, `drop` and
  `relabel` each carry an IDENTITY-RETURN path (`if (n <= 0) { return s; }`), so
  `str_fresh_ret_fns_of` and `return_fresh_struct_ret_fns_of` are RIGHT to
  refuse them — a method key would be looked up and correctly miss. Native does
  not reclaim these from a freshness proof either: `ownedCallResultType`
  (`rc_insert.go:226`) admits any user-declared callee and leans entirely on the
  is_unique gate, whose safety argument is "an aliased return is rc>=2 via the
  return-transfer inc" (`ir.go:7352`, `:12143`).

  The self-host emits that inc for a bare ARRAY param only —
  `irlower.fern:15236`, `s.is_arr_slot(rps)`, the #4357 port — so a string or
  struct param handed straight back is an UNCOUNTED alias, and the consuming
  site's comment says exactly that: "the self-host IR path has no return-transfer
  inc, so ONLY registry-proven-fresh callees are safe to free"
  (`irlower.fern:40672`). `__fern_str_free` is already the is_unique gate it
  needs (rc>1 → dec and keep, rc==1 → free), so the missing half is the inc, not
  the free.

  **The inc and the consuming-site drop have to land together**, which is what
  makes this a slice rather than a one-liner. Adding the inc alone regresses the
  case's own controls: `base.tail(0).len()` currently costs nothing because the
  aliased result is dropped on the floor, and with an unpaired inc `base`'s box
  ratchets to rc 2 and its owner's dec never reaches 0. The wasm
  `call-operand-borrowed-return` case records the same hazard from the other
  side. So the remaining work is: the transfer inc for a bare string (then
  struct) param return, PLUS a drop at every position that consumes such a
  result — `.len()` receiver, `.to_owned()` receiver, field read — not one of
  them.

  **SHIPPED here: the 32 B/round row.** `tostring_recv_is_scalar` decided "is
  this the builtin decimal producer?" by enumerating three receiver SHAPES — a
  number literal, an `as_` cast, a bare slot with no pointer marker — so
  `(i % 8).to_string()` declined and `emit_str_concat_reclaim` left the operand
  box behind, once per evaluation. The proof is inductive over the operator, so
  the predicate now recurses through the plain arithmetic binaries and unary
  `-`/`~`. `+?` / `+|` (Option / saturating), the comparisons and `!` are
  excluded: they do not yield a scalar this claim covers. A string `+` cannot
  slip through — a string operand is a marked slot or an `ExprString`, and
  neither arm admits one. `"lit" + (i % 8).to_string()` goes 1720/3248 →
  152/48 bytes.

  This is NOT the general call-operand widening the wasm
  `call-operand-borrowed-return` case guards: the callee is still only the
  builtin scalar `to_string`, and only its receiver's shape moved.

  The three `alloc_flat_method_identity_return` divergence rows therefore REMAIN
  listed, and #6544 stays open on the return-transfer-inc slice above.

  VERIFIED: new `arith-tostring-operand-churn` + `neg-tostring-operand-churn`
  rows in `TestSelfHostStrConcatTempIRX86_64` (both FAIL on the parent commit),
  the `arith-tostring-string-operand-alias-safe` negative, and an
  `arith-tostring-operand-churn` row on `TestSelfHostStrConcatTempWasmIR`
  (`__rc_underflow_count()` = 0). Refs #6544 #4353 #4451.

- 2026-08-17 (later): **#6544's ordering, MEASURED: the return-transfer inc must
  land AFTER the drop coverage, not before.** The previous entry reasoned that
  the inc and the consuming-site drops "have to land together". Tried it, so the
  claim is now a number rather than an argument.

  Adding the retain for a bare STRING param return — the two-word change that
  mirrors the array arm at `irlower.fern:15293` — and nothing else, self-host
  x86-64, bytes per round:

  | probe | before | inc alone | native |
  |---|---|---|---|
  | `base.tail(0).len() + base.len()` (the identity CONTROL) | 0 | 48 | 0 |
  | `base.tail(4).len()` row | 24 | 72 | 0 |
  | the whole `alloc_flat_method_identity_return` case | 216 | 264 | 0 |

  Every row gets WORSE. The control is the clearest: it costs nothing today
  because the aliased result is dropped on the floor and `base`'s own owner frees
  the box; with an unpaired retain the box reaches rc 2, the owner's dec leaves
  it at 1, and it never reaches zero.

  That is not a partial win to be finished later — it is a regression for as long
  as any consuming position lacks a drop, and the set of such positions is the
  whole language surface, not a list. Native does not have this problem because
  its drops were already in place at every position `ownedCallResultType` admits.

  **So the order is: drops first, inc last.** The self-host needs a drop at every
  position that consumes a string-shaped call result — `.len()` receiver,
  `.to_owned()` receiver, call argument, concat operand, field read, discarded
  statement — each admitting a USER-DECLARED callee rather than only a
  registry-proven-fresh one. Until all of them exist the retain cannot be
  switched on, and until the retain is on none of them can free an aliased
  return. The two halves are a single atomic slice, and it is a large one.

  A cheaper alternative exists and is worth weighing before porting native's
  mechanism wholesale: prove per-callee that every return is either FRESH or the
  RECEIVER (a widening of `body_has_nonfresh_str_return` that also admits
  `return <receiver>`), then at the consuming site free the result only when its
  pointer differs from the receiver's. That is sound with NO retain — a fresh box
  is never the receiver, an identity return always is — and it closes exactly the
  `tail` / `drop` shapes this case is built from. It diverges from native's
  is_unique mechanism, which is a real cost under `docs/NATIVE-CONVERGENCE.md`,
  but it is one analysis plus one pointer compare against a slice that has to
  move the whole surface at once.

  Refs #6544 #4451.

- 2026-08-17 (later still): two facts that survive the entry above, found while
  scoping the same slice independently. The ordering conclusion there stands —
  this only sharpens the two ends of it.

  **The retain needs no runtime work, and here is the evidence.** The entry above
  calls it "the two-word change", which is right, but the reason is worth
  recording because native has a `__fern_str_inc` (`ir.go:7649`) and the
  self-host has no such helper — which reads like a three-backend port waiting
  to happen. It is not one: `__fn___fern_rc_inc` carries the SAME preamble as
  `__fn___fern_str_free` (null guard, `testb $1` inline/SSO-tag skip,
  `cmpq $0x10000` low-addr guard, rc at `-8`, negative rc = immortal skip), and
  it is ALREADY emitted on string slots — `slot_is_rc_container`
  (`irlower.fern:1678`) is `is_arr_slot || is_str_slot || tuple_elems`, and the
  Option-payload alias inc (`:8036`) calls `__fern_rc_inc` through it. The helper
  is generic over rc-headered blocks; only the return-site admission is
  array-shaped.

  **"Admitting a user-declared callee" on the drop side stays unsound even after
  the retain lands.** Native's `ownedCallResultType` admits any user-declared
  callee and leans on the is_unique gate, which is safe there because native's
  return-transfer inc is GENERAL. The retain contemplated here covers exactly one
  shape, `return <bare param>`. A callee returning `h.tag` (a struct field), or a
  captured string, still hands the caller an UNCOUNTED alias — so a drop admitting
  every user-declared callee frees it, which is an over-release, not a leak. The
  drops have to admit precisely the per-callee proved set, and that set has to
  stay in step with whatever shapes the retain covers.

  Which is a second argument for the receiver-pointer-compare alternative above:
  its admission gate IS the static proof (`every return is fresh or the
  receiver`), with no retain to keep in sync and no shape that can silently fall
  outside it.

  Refs #6544 #4451.
- 2026-08-17 (later still): **First drop site landed on the fresh-or-RECEIVER
  route — `.len()` receiver, string-returning methods (#6544).** The previous
  entry named two options; this takes the cheap one and it works.

  `str_fresh_ret_fns_of` now also emits `SFRRECV:<Recv>.<method>` for a
  string-shaped METHOD whose every return is fresh OR the bare receiver ident
  (`body_has_nonfreshrecv_str_return`, which is `body_has_nonfresh_str_return`
  widened by exactly that one shape). At a `.len()` receiver the lowering keeps
  the result in a scratch, reads the length into a second scratch, and frees the
  result ONLY when its pointer differs from the receiver's slot. A fresh box is
  never the receiver's pointer and an identity return always is, so the compare
  decides at runtime what no static analysis could — and **no return-transfer
  inc is involved**, which is what keeps it clear of the whole-surface pairing
  that inc would demand.

  `"long-enough-payload-" + (i % 8).to_string()` then `.tails(4).len()`, 50/100
  rounds, x86-64: **2504/4848 → 152/48 bytes**, against native's 64/0. The
  identity control (`.tails(0).len()` with the receiver read afterwards) stays
  flat and `__rc_underflow()` stays 0.

  **Scope, and the next site.** This closes the row for a method declared
  `: string`. The conformance case's own `tail` and std/string's `drop` are
  declared `: str`, and `expr_is_str` keys `str_ret_fns_of` on
  `ret_type == "string"` only — so a `str`-returning method never reaches this
  arm at all (`str_recv` is false and the read falls through to the array-length
  path). Admitting `str` there changes method-call TYPE dispatch across the
  compiler, so it is its own slice with its own measurement, not a widening to
  fold in here.

  **A harness fact worth keeping.** Heap flatness is asserted on the x86-64 leg
  only. The wasm leg of these tests runs the WAT driver (`wasm_ir_run`), and
  every wasm sibling in the package asserts the over-release detector rather
  than heap growth — the driver pipeline is not the instrument for leaks.
  Flatness on wasm was confirmed separately through the CLI
  (`-target wasm32-wasi`): under 128 bytes across 5000 rounds, and identical
  with the change reverted, so the wasm reclaim is real. Checking that before
  relaxing the assertion is what distinguished "the instrument is wrong here"
  from "the change does not work on wasm".

  VERIFIED: new `TestSelfHostFreshRecvLenReclaim{X86_64,Wasm}` — the
  `freshrecv-len-leak-flat` case FAILS on the parent commit; `identity-alias-safe`
  and `alternating` (both paths chosen per iteration by a runtime compare) are
  the over-release controls on both legs. Plus the str / rc / fixpoint /
  bootstrap subset of `internal/e2eselfhost`. Refs #6544 #4451.

- 2026-08-17 (later still): **The 24 B/round row is closed — and `str` vs
  `string` had nothing to do with it (#6544).** The previous entry named the
  next slice as "admit `str`-RETURNING methods", on the reading that
  `expr_is_str` keys `str_ret_fns_of` on `ret_type == "string"` only, so a
  method declared `: str` never reaches the reclaim arm. Measured first, and
  that reading is wrong: `parse_type_name` erases a bare `str` to `"string"`
  outside `-fmt` (`parser.fern:5704`), so both spellings key identically, and a
  `str`-declared method with a fresh return already measured FLAT before this
  slice.

  What the row is actually about is the RETURN SHAPE. `(s: string) drop(n)`
  ends `return s[n:sLen]`, and on the register backends `s[a:b]` is a 24-byte
  box over the RECEIVER'S OWN BYTES carrying rc = -1, the immortal sentinel —
  written precisely so no reclaim path frees the shared data. That also makes
  the box unfreeable by every helper the compiler has, and a borrowing string
  method allocates one per call. Per round, self-host x86-64:

  | probe | before | after | native |
  |---|---|---|---|
  | `base.tails(4).len()`, `tails` returning `s[n:sLen]` | 22 | 0 | 0 |
  | std/string `base.drop(2).len()` | 22 | 0 | 0 |
  | same, wasm | 30 | 0 | 0 |
  | `base.tails(4).len()`, `tails` returning `s[n:sLen] + ""` (control) | 0 | 0 | 0 |
  | the whole `alloc_flat_method_identity_return` case | 216 | 190 | 0 |

  Two halves. `body_has_nonfreshrecv_str_return` admits one more return shape —
  a direct slice whose array is the receiver ident — which is what makes `drop`
  / `take` / `trim` / the case's own `tail` earn an `SFRRECV:` key. And the
  `.len()` receiver site releases through a new **`__fern_str_view_free`**
  instead of `__fern_str_free`: an immortal rc frees the 24-byte box ALONE
  (class 3, data untouched), any other rc tail-jumps to `__fern_str_free`
  unchanged. On wasm `$__fern_substr` COPIES, so the slice result is an ordinary
  rc-headered block and the helper maps to the same `$__fern_arr_dec`.

  The heap-range guard on the BOX BASE is what makes the sentinel an exact test
  for a view: the other two immortal producers are the FRAME form of the same op
  (`view_frame_temp_ok`, a box in the caller's stack frame) and
  `emit_const_agg_data`'s `.data` blocks, and neither is in the arena. The slice
  must be a direct return expression over the receiver — a slice of a callee
  LOCAL views a buffer that local's own sweep frees, and one bound to a name
  first could have been stored somewhere on the way out.

  **This is still the fresh-or-RECEIVER route, not native's.** No
  return-transfer inc is involved, so the whole-surface pairing the inc would
  demand (entry above) stays out of scope.

  **A trap that cost an hour, and a real bug behind it.** The first measurement
  of the change reported the leak UNCHANGED and `__rc_underflow()` newly
  non-zero — but only through `bin/fern-selfhost -o prog`, never through
  `-emit asm` piped to gcc. The self-host's in-process x86-64 assembler encoded
  `testl %ecx, %ecx` as `testq %rcx, %rcx` (a deliberate substitution: ZF agrees
  on zero-extended operands). SF does not agree — a 32-bit -1 loaded with `movl`
  is a large POSITIVE at 64 bits — so the `js` guarding every "negative rc =
  immortal, skip" arm of the rc runtime (`__fern_rc_inc`, `__fern_arr_dec`,
  `__fern_str_free`, `__fern_str_arr_free`, …) fell through to the decrement /
  underflow path. Fixed in `x86_native.fern` with a real 32-bit `test`
  (`85 /r`, REX only to reach `r8d..r15d`). Nothing in the program corpus
  covered it: every `internal/e2eselfhost` program test routes the emitted `.s`
  through gcc — see the new `docs/TEST-GATES.md` entry.

  Remaining on the case, unchanged and unstarted: the `.to_owned()` receiver
  (measured 71 B/round) and the struct-field read after a method,
  `b.relabel(..).tag.len()` (measured 119 B/round). Its three
  `alloc_flat_method_identity_return` divergence rows therefore REMAIN listed.

  VERIFIED: new `freshrecv-len-view-leak-flat` row in
  `TestSelfHostFreshRecvLenReclaimX86_64` (FAILS on the parent commit);
  `freshrecv-len-view-alias-safe` re-reads the receiver's BYTE at the offset the
  released view covered, after 3000 releases — on the register backends that
  box's data pointer is `b`'s buffer + 4, not even an allocation boundary;
  `freshrecv-len-view-alternating` (receiver / fresh literal / view chosen per
  iteration by a runtime compare) and
  `freshrecv-len-view-nonrecv-return-refused` (a callee that also returns a bare
  non-receiver param earns no key, so nothing is released) are the controls, on
  both legs. The assembler fix has three new rows in
  `TestSelfHostX86GasGroundTruth`, byte-exact against `as` + objdump, also
  mutation-checked. std/string's `drop` cannot be a row in the reclaim test —
  that driver resolves no imports — and was measured flat through the CLI on
  x86-64, arm64 (qemu) and wasm instead. Refs #6544 #4451.

- 2026-08-17 (later still): **The chained receiver, two thirds of it (#6544).**
  `base.tail(4).to_owned().len()` cost 71 B/round because the `.len()` reclaim
  arm required its receiver to be a bare string LOCAL, so a receiver that was
  itself a method call declined outright — and the three allocations the shape
  makes (the inner view box, the outer box, the outer's data buffer) all
  survived.

  The arm now walks the chain instead: `sfrrecv_chain_root_slot` descends
  through `SFRRECV:`-proven links to the bare string local at the bottom, and
  that root's pointer is what the release is guarded against. The induction is
  the same one the single-link case rests on — every link returns a fresh box,
  its own receiver, or a slice of it, and a slice is an ALLOCATION, so it can
  never equal a box that already existed. The chain's result is therefore either
  the root's box (every link took its identity path) or a box the chain itself
  allocated, which no live name holds.

  Per round, self-host x86-64 (arm64 identical, measured under qemu):

  | probe | before | after | native |
  |---|---|---|---|
  | `base.tail(4).to_owned().len()` | 71 | 22 | 0 |
  | same, wasm | 71 | 30 | 0 |
  | `base.tails(0).same().len()` (both links identity) | 0 | 0 | 0 |
  | the whole `alloc_flat_method_identity_return` case | 190 | 142 | 0 |

  **What is left, and why it is a separate slice.** The 22 is the INTERMEDIATE
  view box, released nowhere. Freeing it at the outer call needs two proofs the
  registry does not carry: the outer callee must BORROW its receiver (not stash
  it), and it must never return a VIEW of it — otherwise freeing a fresh
  intermediate frees data the result still points into. `SFRRECV:` is the union
  of fresh / receiver / view, so it cannot answer the second; splitting it is
  the next increment.

  **The chain admits user methods only.** `base.tail(4).to_ascii_upper().len()`
  is unchanged at 71: `to_ascii_upper` is a builtin, so no registry key exists
  for it. The builtin string producers would each qualify under the same rule
  (`to_upper` / `to_lower` / `reverse` / `repeat` are always fresh, `trim` is a
  view of the receiver, `replace` and a string-receiver `to_string` are the
  receiver or fresh) — but each is a claim about emitted code that has to be
  checked one at a time, so that widening is its own slice too.

  VERIFIED: new `freshrecv-len-chain-bounded` row (FAILS on the parent). It
  asserts a per-round BUDGET rather than flatness, because the shape is not
  supposed to be flat yet: one 24-byte box a round instead of three allocations,
  bound at 48 with ~2x margin either side. `freshrecv-len-chain-identity` (both
  links take the identity path, so the result IS the root's box — a root walked
  wrong frees a live local) and `freshrecv-len-chain-alias-safe` are the
  controls on both legs; both re-read the root's BYTE after 3000 rounds. Plus
  the str / rc / freshrecv / irverify / fixpoint / bootstrap subset of
  `internal/e2eselfhost`. Refs #6544 #4451.

- 2026-08-17 (later still): **A fresh string ARGUMENT was reclaimed at a
  free-function call and leaked at a METHOD call (#6544).** Found while
  decomposing the `b.relabel(..).tag.len()` row, which turned out not to be one
  leak but three. Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `s.score("lit")`, string receiver | 23 | 0 |
  | `(i % 8).score("lit")`, i32 receiver | 24 | 0 |
  | `score(s, "lit")`, free function (control) | 0 | 0 |
  | `b.score("lit")`, struct receiver | 46 | 21 |
  | `b.score()`, struct receiver, no string arg | 22 | 22 |

  So the row decomposes into three INDEPENDENT leaks, and only the first is
  closed here: the literal-argument box (24), a struct local handed to a method
  as its RECEIVER (22, untouched), and the fresh STRUCT result of a method
  consumed by a field read (~48, untouched). The conformance row moves by none
  of it, because `relabel` MOVES its param into the struct field and is
  therefore correctly not borrowable — which is the point of the gate.

  The fix is where the asymmetry was: `borrowable_params_of` has keyed methods
  `"<Type>.<method>"` all along, and `call_arg_borrowable` reads that key
  fine — the stash was simply only ever written at the free-call site. Three
  user-method arms (struct, enum, primitive receiver) now share it through
  `stash_fresh_str_arg` / `free_stashed_str_args`, which replace the inline copy
  at the free-call site rather than adding a fourth.

  **The census was the hidden half.** `lit_arg_callees_expr` collects the
  callees whose `BORROW:` entry gets seeded, and it only ever matched a
  bare-ident callee — a method call fell to the recurse-into-receiver arm, so no
  method entry was ever seeded and a wired-up stash would have found nothing.
  The census cannot resolve a receiver's type (no LowerState), so it collects
  the bare method NAME and the seeding loop matches a registry key by its part
  after the last '.'. Over-collection is harmless: the lowering looks the entry
  up by the full `<Type>.<method>` key, so a same-named method on another type
  only seeds a spare entry — the same reasoning the census already documents.

  VERIFIED: three new rows in `TestSelfHostLiteralArgReclaimIRX86_64`
  (`method-literal-arg-borrowable-flat`, `…-prim-recv-flat`,
  `method-fresh-concat-arg-flat`) all FAIL on the parent commit at 98 (leaked);
  `method-literal-arg-retained-safe` (callee returns the param) and
  `method-literal-arg-consumed-safe` (the `relabel` shape — param moved into a
  struct field, both the result's and the receiver's tag re-read) are the
  refusal controls and pass on both. Arm64 and wasm siblings gained the same
  rows. Refs #6544 #4355 #4451.

- 2026-08-17 (last): **The chain's INTERMEDIATE box was attempted and ABANDONED
  — the route needs a widening four negative tests forbid (#6544).** Recorded so
  the next attempt starts from the evidence rather than repeating it.

  The remaining 22 B/round on `base.tail(4).to_owned().len()` is the view box the
  outer link was built from. Releasing a method's RECEIVER needs a key stricter
  than `SFRRECV:` — the callee must neither return a view of the receiver (the
  result would point into freed bytes) nor let it escape. That key is buildable:
  every return fresh (`body_has_nonfresh_str_return`, the un-widened predicate)
  plus the receiver passing the three-part conjunction `borrowable_params_of`
  applies to a param.

  **Built, measured, and it closed nothing.** std/string's `to_owned` is
  `return s + ""`, and `expr_unsafe_for` reads a bare-ident OPERAND of any binary
  expression as an escape, so the receiver failed the conjunction and the key was
  never emitted. A receiver-ignoring body (`return "xy" + "zw"`) and a
  producer-method body (`return s.to_ascii_upper()`) both qualified; the concat
  did not.

  Widening `expr_unsafe_for` so a bare-ident operand is a BORROW does close it —
  22 -> 0 B/round on x86-64, arm64 and wasm, case 142 -> 118 — and the two halves
  are inseparable: with the widening in and the release disabled every probe read
  exactly as before, so the widening buys nothing alone.

  **What stopped it: the widening trips FOUR author-written negative tests**,
  all asserting some string is NOT reclaimed.

  | test | asserts |
  |---|---|
  | `TestSelfHostStrReclaimIRX86_64/aliased-not-reclaimed` | zero reclaim sites |
  | `TestSelfHostStrReclaimIRX86_64/returned-not-reclaimed` | zero reclaim sites |
  | `TestSelfHostStrConcatTempIRX86_64/ident-operands-result-only` | exactly ONE — "extra sites mean an aliased operand was mis-freed" |
  | `TestSelfHostStrAccumIRX86_64/accum-nonfresh-reassign-not-reclaimed` | not reclaimed |

  The safety question was probed and came out in the widening's favour: with
  `var c = a;` making an operand genuinely aliased and both read after the
  concat, 100k rounds under `-sanitize` gave NO over-release, `__rc_underflow()`
  0, and leaks only (`allocs=400004 frees=299999`). The escape walker still
  refuses a name with any other escaping use, so the invariant these tests encode
  — "ident operands are never freed" — is strictly stronger than the property
  they protect — "ALIASED operands are never freed".

  **That is not enough to move four of them.** The fourth was found only by the
  98-minute whole-package `internal/e2eselfhost` run, after the targeted suites
  were green and a recommendation had already been formed off a probe that
  sampled shapes chosen by the same person who wrote the change. One probe
  showing safety on shapes I thought of is a weaker statement than four
  independent tests written by people who had reasons not reconstructed here.

  **If this is picked up again**, the shape to try is a NARROW admission —
  reclaim an operand only where the concat is that name's sole use — rather than
  a blanket "a binary operand is a borrow" in a predicate feeding
  `borrowable_params_of` and the snapshot analyses. It needs its own slice and
  its own reckoning with all four tests, ideally with whoever wrote them.

  **Process note worth more than the slice.** Both regressions were invisible to
  the targeted suites and to `make selfhost-cli`; only the whole-package run saw
  them. For a change to a predicate this widely consumed, the full sweep is the
  gate, not the follow-up — "open the PR early" is right in general and was wrong
  here. Refs #6544 #4451.

- 2026-08-17 (struct half, first slice): **A struct local lost its DEEP drop to
  any method call on it; a borrowing callee now keeps it (#6544).** The 22
  B/round that sits under every method row in the entries above.

  `moves_fields_expr` marks EVERY method receiver as a field-move hazard, so
  `b.score()` earns `b` a `NODEEP:` marker and the exit sweep degrades to a
  box-only dec — the struct's own string / array fields are stranded for the
  rest of the scope. The mark is not paranoia: a method body genuinely can carry
  a receiver field into its result with no counted reference (`ops:
  self.ops.append(op)` — an in-place append hands the receiver's rc==1 buffer
  straight to the result), and that is the #3425 residual it was added for. But
  it is a CALLER-side guess about a CALLEE, made with the callee in hand.

  `recv_borrow_fns_of` answers it on the callee side, emitting a
  `"<Type>.<method>"` key when the receiver behaves exactly like a
  deep-drop-worthy LOCAL — proven by the same three predicates
  `reclaimable_names_of` runs over one:

  | predicate | refuses |
  |---|---|
  | `body_unsafe_for` | the box escapes: `return self`, a container store, an alias, a non-borrowable arg |
  | `moves_fields_stmts` | a receiver-POSITION use inside, i.e. `self.m2()` |
  | `optstruct_body_moves_field` | a field reaching a bind / assign / return value, a non-borrowable argument, or a container element |

  The middle one is what keeps the registry NON-CIRCULAR: no method that calls
  through its own receiver is admitted, so no entry rests on another and there
  is no fixpoint to iterate. Struct receivers only — the marker it feeds is the
  struct deep drop.

  Consumed where the marker is decided: `reclaimable_names_of` resolves the
  local's struct type (the literal init, else the declaration) and passes the
  admitted method names to `moves_fields_expr` alongside the fn-typed field
  names it already exempted. The two are one concept — call names in
  `name.<f>(...)` position that provably move nothing — so they share the
  parameter, renamed `fnfields` -> `nomove`.

  Per round, self-host x86-64 (`Box { tag: string, … }`, one method call per
  round):

  | probe | before | after | native |
  |---|---|---|---|
  | `b.score()` reading a scalar field | 22 | 0 | 0 |
  | same over an rc-ARRAY field (`b.items.len()`) | 70 | 0 | 0 |
  | struct built + field read, no method (control) | 0 | 0 | 0 |
  | `b.me().tag.len()`, identity return | 22 | 22 | 0 |
  | `b.bump().tag.len()`, fresh struct return | 71 | 71 | 0 |
  | the whole `alloc_flat_method_identity_return` case | 142 | 142 | 0 |

  **The conformance row does not move, and that is the correct verdict.** Its
  `relabel` is `if (t.len() == 0) { return b; }` — an identity return, so
  `body_unsafe_for` refuses it. Admitting one needs a CALLER-side proof the
  registry cannot carry: the result aliases the receiver's box, so a caller that
  BINDS it (`var c = b.relabel(x)`) holds a live alias past b's death, and
  `relabel` is not in any fresh-ret registry so `c` is never separately
  credited. Granting the deep drop there would dangle rather than leak. The next
  slice is that proof — admit an identity-returning method only where every call
  site consumes the result inline — and after it the row's remaining ~48 is the
  fresh STRUCT result at a field read, the struct analogue of the entries above.

  VERIFIED: new `TestSelfHostRecvBorrowDeepDrop{X86_64,Arm64,Wasm}`. The two
  flatness rows (`recvborrow-deep-drop-flat`, `recvborrow-array-field-flat`)
  FAIL at 98 on the parent; the four refusal rows pass on both parent and child
  and each re-reads the value the refusal protects after 4000 further rounds
  have recycled the freelist, so a wrongly granted deep drop returns garbage
  rather than merely leaking. Flatness is register-backend only, per the trap
  above. Plus the whole `internal/e2eselfhost` package — the gate the previous
  entry's process note names, and the right one here: this widens deep drops
  across the self-host compiler's own sources, which is exactly the #3425 blast
  radius. Refs #6544 #3425 #4451.

- 2026-08-17 (struct half, second slice): **Identity-returning methods admitted
  where the result dies inline, and a pre-existing OVER-RELEASE closed on the
  way (#6544).**

  The previous slice refused a method whose receiver escapes, which includes the
  `if (t.len() == 0) { return b; }` opening every builder-ish method has — the
  conformance case's own `relabel`. The refusal was right but too wide: the
  result aliases the receiver's box only in the sense that *someone might keep
  it*, and whether anyone does is a CALL-SITE fact.

  So the registry gained tiers. `body_unsafe_for_allow_ret` already existed —
  `body_unsafe_for` with exactly one use forgiven, a bare `return name` — so a
  method passing the other two predicates and failing only on that earns
  `"RECVIDENT:"`. `recv_ident_methods_of` then supplies the caller half:
  `name.<m>(...)` must not appear in a MOVE position, which is fieldmove's own
  vocabulary (a bind / assign / return value, a non-borrowable call argument, a
  container element) asked of the CALL rather than of a field chain.

  **The borrowable-param check in that walker is not a refinement, it is the
  difference between better and worse.** An intermediate marked every argument a
  move, and `take(b.me())` went from 22 B/round to 72 — the local lost its whole
  credit instead of just its deep drop. Consulting `param_is_borrowable` the way
  `fieldmove_expr` does takes it to 0.

  **The over-release.** Probing the refused shapes found `keep = b.me()` ticking
  `__rc_underflow()` on the PARENT — main today, not something this slice
  introduced. `b` is credited on the strength of "a method receiver is a
  borrow", but a method that hands the receiver BACK turns that call into a
  move: `keep` ends up on b's own box and b's per-rebind reclaim frees it
  underneath. The free-function analogue (`keep = pick(b)`) is already safe,
  because a param that escapes is not borrowable and `b` is then not credited at
  all — so the bug is exactly the receiver position's borrow exemption, missing
  the matching result check.

  Hence a third and widest class, `"RECVRET:"`, emitted for ANY method with a
  bare `return self` regardless of the other two predicates — a method that also
  moves a field can still hand its receiver back — and consumed by the CREDIT
  gate rather than the NODEEP one.

  Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `b.me().tag.len()`, identity return consumed inline | 22 | 0 |
  | `take(b.me())`, borrowable param | 22 | 0 |
  | `keep = b.me()` (outer name) | **over-release** | 0, balanced |
  | `keep = pick(b)`, free-function control | 0 | 0 |
  | `b.bump().tag.len()`, fresh struct return | 71 | 71 |
  | the whole `alloc_flat_method_identity_return` case | 142 | 117 |

  What remains on the case is the FRESH struct result of a method consumed by a
  field read — the struct analogue of the string chain, and the next slice.

  VERIFIED: `recvident-inline-result-flat` and `recvident-borrowable-arg-flat`
  fail at 98 on the parent; `recvret-rebound-outer-no-over-release` fails at
  **99** there, which is the over-release itself and the reason that row asserts
  balance rather than flatness. `recvret-returned-result-safe` and
  `recvret-container-result-safe` guard the other two move positions. A first
  cut of the over-release row used a `while` loop in `main` and passed on the
  parent — it took the probe's own shape (the loop in its own function, `for i
  in 0..n`) to reproduce, which is worth remembering: a regression test that
  does not fail on the parent is not yet a regression test. Plus the whole
  `internal/e2eselfhost` package. Refs #6544 #3425 #4451.

- 2026-08-17 (struct half, third slice): **A method that returns a fresh struct
  is a strict-fresh producer at the READ, so the temp it leaves behind is
  reclaimed (#6544).**

  The two entries above credit the RECEIVER. This one is about the RESULT: the
  read-site reclaim (#6491) frees the container `mk().k` / `boxed(i).name`
  leaves behind, and it never asked a method. `owned_fresh_call_callee` matched
  a bare-ident callee only, so `b.bump().tag` — same producer, same proof, one
  receiver away — stranded the whole box every evaluation.

  The registry already keys a method as `"<Base>.<method>"`, so the callee half
  is a lookup, not a new analysis: the receiver's struct type plus the method
  name. The receiver must be a bare LOCAL — `expr_struct_type` reports a
  struct-ARRAY-returning call's ELEMENT type (the `mk()[i].field` recovery), so
  a call receiver could name a method of a type the value is only an array OF,
  where a slot lookup cannot. A struct FIELD of the method's name is a fn-typed
  field call and names no method at all, so it is refused too. `"STRFLDF:"` (the
  string-field MOVE form) drops its free-function-only gate on the same terms:
  every return being a strict-fresh LITERAL already refuses `return b`, and
  `body_strfields_all_fresh` refuses a literal built out of `b.tag` — the two
  shapes in which the result could carry the receiver's own box.

  Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `b.bump().tag.len()`, fresh struct + fresh string field | 96 | 0 |
  | `p.bump().k`, scalar field off a string-free struct | 48 | 0 |
  | `b.relabel(t).tag.len()` (the conformance case) | 96 | 96 |

  **The conformance row still does not move, and the reason is precise.**
  `relabel` opens `if (t.len() == 0) { return b; }`, so it is not strict-fresh
  on every path and no whole-body registry can admit it. The string side solved
  exactly this with a RUNTIME discriminator (SFRRECV: release the result only
  when its pointer differs from the chain root's), and the struct analogue is
  that same compare — with one extra question the string side does not have:
  the fresh path's `Box { tag: t, … }` takes its string from a PARAMETER, so
  the temp box does not own that field and the release has to stay the bare box
  dec. That is the next slice, and it is a different mechanism from this one,
  not a widening of it.

  One other residual this slice names rather than closes. A SCALAR read off a
  string-bearing struct still strands the strings: the bare registry entry
  proves a fresh literal, not that the box owns its string buffers, and the
  entry that does carry that proof (`"STRFLDF:"`) is not consulted on the
  scalar path — `__struct_drop_<T>`'s string arm is `strfldok:<T>`-gated and
  pairing the two is its own reckoning.

  The credit half of the pointer triple was a second one; the entry below
  closes it.

  VERIFIED: new `fresh-struct-method-field-string` and
  `fresh-struct-method-field-scalar` in the #6491 read-reclaim file, both
  failing at 92 (the growth) on the parent. Two refusals guard the admission
  from the other side and pass on both: `method-identity-return-not-released`
  (`me()` hands the receiver back, so the "temp" would be `keep`'s own box) and
  `method-receiver-field-value-refused` (`same()` wraps `b.tag`, so the moved-out
  string would be `keep`'s), each re-reading the value the refusal protects
  after churn has recycled the freed bytes. Plus the whole `internal/e2eselfhost`
  package — the gate the 2026-08-16 process note names, and the right one for a
  registry the self-host's own sources consume. Refs #6544 #6491 #4451.

- 2026-08-18 (struct half, fourth slice): **The BINDING destination for a
  method's moved-out string field earns its credit (#6544).**

  The residual the entry above names. The read half landed with the method
  callee: `b.bump(t).tag` moves the string out of the temp and frees the box.
  But whether the DESTINATION owns what it received is decided somewhere else
  entirely — `owned_container_read_str_binding`, under `reclaimable_names_of` —
  and that walker still matched bare-ident callees only. So the binding form
  freed the box and leaked the string: 72 B a round, against 0 for the
  free-function spelling of the same binding.

  **The constraint here is not which shapes to admit, it is that two
  independent analyses have to agree on one key.** The lowering side builds it
  with `expr_struct_type`; this side has no LowerState at all. A key only this
  side accepts is not a missed optimisation, it is a free the read never
  performed — the destination releases a string the box still owns.

  So `local_recv_method_key` makes the lowering's own two refusals from the
  AST — the receiver is a bare local, and the callee names no declared FIELD of
  its type — and adds the one this side needs on its own: the local carries a
  type ANNOTATION and is declared exactly ONCE, so that annotation is the
  receiver's type and not a shadowed sibling's. `structs` threads down to reach
  the field refusal. A param receiver resolves to neither and keeps today's
  leak; the analyses agreeing is worth more than the last few shapes.

  Per round, self-host x86-64 (`Box { name: string, k: i32 }`, `wide()` past the
  inline threshold):

  | probe | before | after | free-fn control |
  |---|---|---|---|
  | `var f: string = seed.rename(i).name` — bound | 72 | **0** | 0 |
  | `seed.rename(i).name.len()` — borrowed | 0 | 0 | 0 |
  | `pair.shift(i).a` — scalar, no-`string` struct | 0 | 0 | 0 |

  The last two rows are the previous slice's, unmoved — they are here because a
  credit widening that disturbed them would be the failure mode worth catching.

  VERIFIED: new `method-field-string-bound` in the #6491 read-reclaim file,
  failing at **92** on the parent. The previous slice's two refusals are the
  safety controls for this one as well and needed no change: both already use
  the BINDING form (`var f: string = keep.me().tag` / `keep.same().tag`), so
  they fail the moment this credit reaches a producer that hands back the
  receiver's box or wraps its string — and both still pass. New conformance case
  `alloc_flat_method_result_field_read` states the whole method-result field
  read as a CROSS-BACKEND contract, which the x86-64-only Go gate cannot: it
  runs on x86_64 / arm64 / wasm under `TestFernFixtures` and on
  `TestFernFixturesSelfHost{X86_64,Wasm}`, and agrees with native byte-for-byte.
  Plus the whole `internal/e2eselfhost` package. Refs #6544 #6491 #4451.
- 2026-08-18 (struct half, fifth slice): **A strict-fresh producer's box was
  refused along with its BORROWED string field.** The field-read reclaim asks
  `owned_fresh_call_callee(..., "STRFLDF:")` for a `string` field, and that entry
  requires `body_strfields_all_fresh` — every string in the returned literal
  allocated by the producer's own frame. `Box { name: s, k: n }` over a PARAMETER
  fails it, correctly: moving that pointer out and letting the destination free
  it would release the caller's string underneath it, which is what
  `strfld-param-value-refused` pins.

  Refusing the MOVE is right. Refusing the BOX was not. The producer is still
  strict-fresh, so the box is this frame's own allocation and nothing names it —
  the same proof the SCALAR field read has always used to dec it. The read now
  falls back to the bare strict-fresh key when "STRFLDF:" declines, and the dec
  it reaches is the shallow one: `__struct_drop_<T>` above it is separately gated
  on `struct_has_reclaim_array_field`, which this admission excludes. So the
  moved-out pointer keeps belonging to whoever allocated it and the release
  cannot over-reach. Every consumer that would treat the read as OWNING its
  string — the `.len()` receiver site and the binding credit — keys on
  "STRFLDF:", so none of them is reached from the fallback.

  Per round, self-host x86-64, against native's 0:

  | probe | before | after |
  |---|---|---|
  | `wrap(live, i).name.len()`, free fn, borrowed string field | 72 | 23 |
  | `b.relabel(t).tag.len()`, method, borrowed string field | 70 | 21 |
  | `b.relabel(t).n`, scalar read of the same producer (control) | 21 | 21 |

  The control is the point: 21 was already the floor for that probe's shape
  before this slice, and the string read now sits on it rather than above it.
  On the gate's own shape both reads are flat outright — it grows on the parent
  (92) and is flat here.

  What this does NOT touch: the `relabel` CONFORMANCE row, where the method
  returns the receiver on one path. That is still refused by
  `body_has_nonstrictfresh_return` and still needs the SFRRECV-style runtime
  discriminator named in the third slice — a different mechanism, and measured
  here at an unchanged 70. This slice was reached by measuring toward that one
  and finding a nearer, separable gap on the way: the identity return is not
  what blocks a borrowed-string field read, and a strict-fresh control proved it
  (70 with the identity return removed, so the discriminator was never the
  binding constraint for this shape).

  VERIFIED: new `strfld-param-value-box-reclaimed` (grows at 92 on the parent,
  flat here) and `strfld-method-param-value-box-reclaimed` (reads the moved-out
  pointer back AFTER churn has recycled the freed box's bytes — the half that
  proves the dec stayed shallow), plus all 15 existing cases in the #6491
  read-reclaim file, `strfld-param-value-refused` and `borrowed-field-refused`
  among them. Refs #6544 #6491 #4451.

- 2026-08-18: **A fresh string PRODUCER handed straight to a borrowed parameter
  was released by nobody (#6522-adjacent).** Found while decomposing
  `alloc_flat_fresh_array_arg`, and it is not that row — it is the string half
  of the same stage-(b) arg-temp shape, on the ordinary path.

  `size(mks(i))` leaked one box per evaluation, 70 B/round measured, where the
  identical call with a string LITERAL argument has been flat since #4355. The
  reclaim already existed; two separate gates kept the producer out of it.

  | gate | admitted | missed |
  |---|---|---|
  | `lit_arg_callees_expr` (the `BORROW:` seeding census) | a literal, an array literal, a fresh local binding | a callee whose argument is a direct CALL — so `size` was never seeded and the stash could not fire for it |
  | `is_fresh_str_temp` | a literal, a scalar `.to_string()`, `str_local_binding_is_fresh` | a call to a proven fresh producer |

  Both were needed; widening only the census moved nothing, which is worth
  remembering — the census is a necessary condition, never a sufficient one.
  `str_fresh_ret_fns` supplies the second half: it is the same registry every
  other owner of a returned box consults, and a local shadowing the callee's
  name is refused because the registry is keyed by declaration.

  Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `size(mks(i))` — borrowable position | 70 | **0** |
  | `pick(mks(i))` — callee RETURNS the argument | 70 | 70 (refused) |
  | `keep(mks(i), i)` — callee MOVES it into a struct field | 68 | 68 (refused) |
  | `var nm = mks(i); size(nm)` — a bound LOCAL, not a temp | 0 | 0 |

  **The ARRAY half is NOT closed, and it is a CALL-SITE widening.** `size(mk(i))`
  with an `i32[]` producer still costs 55. The registry is not the obstacle:
  `return_fresh_struct_ret_fns_of` admits two return shapes, a direct array
  literal OR `body_returns_local_built_arr` — a local with a literal init, only
  ever self-appended, returned bare — which is exactly the
  `var out: i32[] = []; for … { out = out.append(..) } return out;` producer. The
  block is the arg-stash arm, which gates on `discardable_scalar_arr_lit` and so
  matches only a `parser.ExprArray` literal. Admitting an `"ARR:"`-registered
  producer CALL there, released with `__fern_rc_dec`, is the same shape as the
  string half above.

  Measured rather than read off the source, through the DISCARDED-call reclaim —
  a different consumer of the same `"ARR:"` entry, so it answers the registry
  question on its own: a discarded `mk(i);` is FLAT (registered), while a control
  whose local is initialised from a call (`var out = seed();`) costs 71
  (correctly not registered). An earlier draft of this entry claimed the registry
  excluded the loop-built shape and that widening it was the work; that was
  reasoned from a source read and is wrong, which is worth keeping because it
  would have sent the next reader at the wrong file.

  The conformance row decomposes as: 0 for a fresh array bound to a local, 0 for
  one used as an inline struct-literal field, 55 for the borrowed-arg temp, and
  103 once a constructor stores it.

  VERIFIED: `producer-call-arg-borrowable-flat` fails at 98 on the parent;
  `-returned-safe`, `-stored-safe` and `-bound-local-safe` are the refusals and
  pass on both, each reading the protected value back rather than only counting
  bytes. arm64 and wasm siblings gained the same rows, wasm without a flatness
  assertion per the trap above. All five `alloc_flat_*` divergence rows re-checked
  and unmoved. Refs #6522 #4355 #4451.

- 2026-08-18 (later): **The ARRAY half of the stage-(b) arg temp, closed exactly
  where the previous entry predicted (#6522).** `size(mk(i))` — a fresh
  scalar-array producer handed to a borrowed parameter — cost 55 B/round; it is
  0 now.

  One call-site arm, no registry work: the stash gated on
  `discardable_scalar_arr_lit`, so it matched a `parser.ExprArray` literal and
  nothing else. It now also admits a direct call whose callee carries an
  `"ARR:"` entry, refusing a local that shadows the declaration since the
  registry is keyed by declaration. The release is the same `__fern_rc_dec` the
  literal arm already used.

  Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `size(mk(i))` — borrowable position | 55 | **0** |
  | `pick(mk(i))` — callee RETURNS the array | 55 | 55 (refused) |
  | `node(mk(i), i)` — constructor STORES it | 103 | 103 (refused) |
  | `var live = mk(i); size(live)` — a bound local | 0 | 0 |

  **`alloc_flat_fresh_array_arg` does NOT move, and the reason is the third
  row.** Its `node(name, deps_of(n), mtime)` stores the array, so the parameter
  is not borrowable and this stash correctly declines — freeing there would
  release what the returned struct now owns. The case stays at 798 B/round.
  Closing it needs what native actually did for #6522: `paramCountedRetain`, the
  PER-ARGUMENT admission that fires where the whole-call gate says no, keyed on
  every appearance of the callee's parameter being a counted store or a
  non-retaining read (`inferParamCountedRetain` / `arrayParamCounted`,
  internal/ir/rc_analysis.go). The temp is then rc 2 on the escaping path and
  rc 1 otherwise, and one post-call dec nets it correctly either way. That is a
  fixpoint analysis over four param-type classifiers and is its own slice — the
  borrowed-position stash here is not a step toward it, it is the neighbouring
  shape.

  VERIFIED: `producer-call-arr-arg-borrowable-flat` fails at 98 on the parent;
  `-returned-safe`, `-stored-safe` and `-bound-local-safe` are the refusals,
  pass on both, and each re-reads the protected buffer's ELEMENTS rather than
  only its length, so a wrongly admitted release shows as a wrong value. arm64
  and wasm siblings gained the flat + returned rows. Refs #6522 #4365 #4451.

- 2026-08-18 (later still): **`paramCountedRetain` ported for ARRAY params, and
  the conformance row's real blocker identified (#6522).**

  The arg stash released a fresh temp only at a BORROWABLE position, which
  leaves out the shape the conformance row is built from: a callee that STORES
  the array is not borrowable by construction. Native does not widen
  borrowability for this; it asks a different question per ARGUMENT — is every
  appearance of the callee's parameter a counted store or a non-retaining read?
  If so, an argument reaching the result got there through a construction that
  inc'd it, so the temp is rc 2 on the escaping path and rc 1 otherwise and one
  post-call dec nets it to a single owner either way.

  `array_param_counted_of` is that analysis: a least fixpoint (start all-false,
  add credits only on positive evidence, so a mutual-recursion cycle stays
  uncredited), crediting a struct/tuple/array-literal slot value, a `p.len()`
  pure-read receiver, and an argument at a position already proven counted.
  ARRAY params only — native also classifies string and struct params and then
  withdraws a scalar exemption via `ptrAllCounted`; leaving every other param
  uncredited is strictly narrower, so the exemption never arises and a missing
  credit costs a leak rather than an over-release.

  | probe | before | after |
  |---|---|---|
  | `keep(mk(i), i)` — STORES the array, returns a scalar | 55 | **0** |
  | `size(mk(i))` — borrowable (previous slice) | 0 | 0 |
  | `both(mk(i), i)` — counted param, returns `i32[]` | 54 | 54 (refused) |
  | `hand(mk(i))` — param aliased to a local | 55 | 55 (refused) |
  | `node(mk(i), i)` — STORES it, returns a STRUCT | 103 | 103 (see below) |

  **The scalar-result requirement is native's `resultCannotAliasArg`, and it is
  stated in the REGISTRY rather than at the call site.** The release fires
  immediately after the call, so a pointer-shaped return that is or contains the
  argument would hand the caller freed memory. Array- and struct-returning
  callees happen to lower through paths that never reach the arg stash, so
  without this they would have been safe by accident; requiring a concrete
  scalar result to earn an entry makes it an invariant of the registry instead.

  **`alloc_flat_fresh_array_arg` still does not move. The cause was witnessed on
  2026-08-18 and it is not in this arg stash at all** — see that day's entry for
  the measurement. In short: `node` earns no `"ACNT:"` entry, and this slice's
  scalar-result guard is exactly why (forcing `scalar_result` true gives it
  `ACNT:node|10` and the call site does emit the release). The byte count does
  not move because the released reference was never the leak — the leak is the
  CALLER's `var b: Node = node(…)` binding, which earns no reclaim credit at all.

  Two earlier readings of this row were wrong and are kept here because both
  would have sent the next reader at the wrong file. First: "a struct-returning
  callee lowers through a path that never reaches the arg stash" — disproved, the
  arm is reached regardless of the return's shape. Second: "the scalar-result
  guard is the blocker" — half right and misleading as written; it IS what keeps
  `node` out of the registry, and lifting it still moves nothing.

  The scalar-result guard is independently stricter than native —
  `countedArgTemp` has no result-type check, and native's comment names
  `node(name, no_deps(), k)` as the shape it exists to admit — but it stays: it
  is the conservative direction and costs only a leak.
  The case stays at 798 B/round.

  VERIFIED: `counted-retain-arr-arg-flat` fails at 98 on the parent;
  `-pointer-result-safe` (counted param, `i32[]` return) and
  `-aliased-param-safe` (param bound to a local) are the two refusal directions
  and pass on both, each re-reading the buffer's ELEMENTS. Refs #6522 #4451.

- 2026-08-18 (struct half, sixth slice): **A method that returns a fresh struct
  on one path and its RECEIVER on the other releases the fresh one, decided by
  the pointer at runtime (#6544).**

  The slice above admits a producer only when EVERY return is a strict-fresh
  literal, which refuses the shape every builder-ish method opens with —
  `if (t.len() == 0) { return b; }`. The refusal is not conservatism about the
  identity path; it is that the two paths hand back different boxes and no
  whole-body registry can say which. The string side answered the same question
  with a runtime discriminator (SFRRECV compares the result pointer against the
  chain root) and this is that answer for structs: `"FRESHSELF:<Base>.<method>"`
  admits a body whose every return is a strict-fresh literal OR the bare
  receiver, and the caller releases the result only when its pointer differs
  from the receiver's.

  Three things make the release safe with no further proof:

  - The dec is **shallow**. A fresh literal's field values are not proven owned
    here — `Box { tag: t, … }` takes a parameter's box — so only the box itself
    is provably this frame's, and everything it does own leaks with it. The
    established safe floor.
  - The receiver must be a bare **LOCAL**, so the compare reloads a slot rather
    than re-evaluating an expression that could allocate a second box.
  - The field read happens **before** the release, into a parked scratch: the
    read's value is a borrow either way, and the box may be gone after.

  The entry requires a bare-receiver return rather than merely tolerating one: an
  all-fresh body is what the slice above already admits with no runtime compare
  to pay for, and a second entry for it would only lengthen a registry every call
  site scans. That gate and the shape-test ordering in the `.len()` hook are not
  tidiness — without them this slice cost the self-host's own compile ~8%
  (65.7 s vs 60.6 s on TestSelfHostStage2Compiler), which is enough to push a CI
  shard packed to its 1080 s budget past its 20-minute timeout, and did. With
  them the same measurement is 60.6 s, indistinguishable from the base.

  Two call shapes reach it, because a field read off a call and a `.len()` on a
  string field off a call are lowered in different places: the ExprFieldAccess
  arm of `lower_expr`, and the `str_recv` receiver path in the method-call
  lowering. Both park the box, read, and hand `emit_freshself_release` the two
  slots.

  Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `p.relabel(3).k` + `p.relabel(0).k`, string-free struct | 48 | **0** |
  | `b.relabel(t).tag.len()` × 2 + `b.tag.len()` (the conformance case) | 96 | 48 |
  | the fresh call alone, string-bearing struct | 72 | 24 |
  | the identity call alone, string-bearing struct | 24 | 24 |

  **The next-slice reading written here was wrong; see the 2026-08-18 (method
  half) entry for what the last row actually says.** It named
  `optstruct_body_moves_field` as what costs `relabel` the `"RECVIDENT:"` tier,
  and proposed restricting that predicate to rc-tracked field types. Witnessed at
  HEAD, `relabel` scores `movesfield=0` and earns `RECVIDENT:Box.relabel`
  already, so that edit is a no-op. The residue is not about the receiver or the
  identity return at all.

  VERIFIED: new `freshself-mixed-method-flat` (failing at 92 on the parent),
  plus two safety rows that pass on both sides — `freshself-identity-path-not-freed`,
  where EVERY call takes the identity path so a compare with its sense backwards
  frees `keep`'s own box a hundred times over, and `freshself-string-field-read-safe`,
  the conformance shape itself, asserting values and balance rather than the
  flatness the receiver-credit gate still caps. Plus the whole
  `internal/e2eselfhost` package. Refs #6544 #6491 #4451.

- 2026-08-18 (array half): **A struct producer that seeds its array field from a
  PRODUCER CALL is strict-fresh, so its caller's binding is reclaimable (#6522
  isolation, #6758 follow-on).**

  This started as the isolation the previous entry asked for, and it is worth
  reading in that order because the answer moved the row somewhere else.

  **Witnessed, not inferred.** Dumping the registry at the end of
  `array_param_counted_of` and compiling both probes with a self-host CLI built
  from the tree: `keep` earns `ACNT:keep|10`; `node` earns nothing at all.
  Forcing `scalar_result` true gives it `ACNT:node|10`, and a probe at the stash
  site then prints `callee=node ai=0 arrlit=1 borrowable=0 counted=1` — the
  emitted asm gains the stash and the `__fern_arr_dec` after the call. So the
  guard IS the registry's blocker, and lifting it still leaves the case at 104
  B/round.

  **Because the released reference was never the leak.** `node(mk(i), i)` stores
  the temp in the struct it returns; the post-call dec takes that temp from rc 2
  to rc 1 and the struct owns it — correctly. What leaks is the CALLER's
  `var b: Node = node(…)`: `node` is param-fed, so it is not strict-fresh, so `b`
  earns no reclaim credit and box plus buffer die unreleased every iteration. The
  probe that settles it has no array argument at all —
  `make(k: i32): Node { var d: i32[] = mk(k); return Node { deps: d, k: k }; }` —
  and leaked the identical 104 B/round.

  **That probe was its own closable gap, and this slice closes it.**
  `return_value_is_strictfresh_struct` admitted an ident field value only when
  `arr_field_ident_is_frame_built` proved the local was LITERAL-initialised
  (#6758). A local seeded by a call to an `"ARR:"` producer carries the same
  proof one call further out — that registry's whole claim is that every return
  is a sole-owned scalar-element buffer the callee's frame allocated — so it is
  admitted now, under the unchanged single-declaration, self-append-only and
  single-escape conditions. `arr_local_has_literal_init` becomes
  `arr_local_init_is_frame_fresh` and the `"ARR:"` admission rule moves out into
  `arr_fresh_ret_fns_of`, so the registry and this walk ask one question instead
  of two copies of it. `body_returns_local_built_arr` passes an EMPTY producer
  list: it is the predicate that registry is built from, so consulting it there
  would be circular, and a producer returning another producer's local is a
  widening of its own.

  Per round, self-host x86-64:

  | probe | before | after |
  |---|---|---|
  | `var d: i32[] = mk(k); return Node { deps: d, … }` | 104 | **0** |
  | `var d: i32[] = [k, …]; return Node { deps: d, … }` (#6758) | 0 | 0 |
  | `return Node { deps: [k, …], … }` | 0 | 0 |
  | `node(mk(i), i)`, the param-fed producer | 104 | 104 |

  **The last row is where the row goes next.** It is param-fed, so no
  freshness-of-construction argument can credit it — but the counted-retain
  contract already proves what a credit would need: every appearance of the
  parameter is a counted store, so the struct the callee returns owns exactly one
  reference to the argument. Crediting the caller's binding off `"ACNT:"` rather
  than off strict-freshness is the shape of the next slice, and it is what the
  conformance row has been waiting on.

  VERIFIED: new `producer-call-array-field` (104 B/iteration on the parent,
  flat after) and `passthru-producer-declined` — a producer that hands back its
  own parameter earns no `"ARR:"` entry, which is what keeps the caller's buffer
  safe, so it is carried beside the positive rather than left implicit. Both on
  x86-64 and arm64. Refs #6522 #6758 #4451.

- 2026-08-18 (method half): **The `alloc_flat_method_identity_return` residue is
  a STRING FIELD SEEDED FROM A PARAMETER, not the identity return (#6544).**

  Doc-only. The previous entry named the next slice as "restrict
  `optstruct_body_moves_field` to rc-tracked field types"; this one measures that
  the edit would change nothing and re-aims the row. That predicate's
  `fieldmove_is_move_value` already ends in `!is_scalar_type_name(lt)`, so a
  scalar field never marked in the first place.

  **Witnessed** by dumping the four predicates `recv_borrow_fns_of` scores, then
  compiling the conformance case with a self-host CLI built from the tree:

  ```
  RECVPROBE Box.relabel bareret=1 movesfields=0 movesfield=0 unsafe=1 unsafe_allowret=0
  ```

  `movesfield=0`, `unsafe_allowret=0` — `relabel` earns `RECVIDENT:Box.relabel`
  today. The tier the entry said was missed is granted.

  **What the 24 actually is.** Per round, self-host x86-64, `Box { tag: string,
  n: i32 }` unless stated:

  | probe | B/round |
  |---|---|
  | `var b = Box { … }`, no method call at all | 0 |
  | `b.relabel("")`, the identity path | 24 |
  | `b.relabel("fresh-tag-value")`, the fresh path | 24 |
  | `b.freshonly("…")` — a method with NO identity path | 24 |
  | `var c: Q = mkq(t, k)` — a plain FREE function, no receiver anywhere | 24 |
  | the same producer building its own field: `Q { tag: "…", k: k }` | 0 |
  | the same, from a fresh concat: `Q { tag: "…" + k.to_string(), k: k }` | 0 |
  | string-FREE struct: `P { k: i32, m: i32 }` from `mkp(k)` | 0 |
  | param-seeded field, and the ARGUMENT is fresh too | 64 |

  Read down that list: the receiver, the identity return and the method form all
  drop out — a free function leaks identically. What survives as the
  discriminator is a `string` field whose value is the producer's own PARAMETER.
  A producer that builds its string field itself is flat, string-free structs are
  flat, and when the argument is itself fresh the leak grows by that string
  (24 → 64), so the caller's box AND the string it handed in are both stranded.

  Native is 0 on every row (measured, with the `__rc_underflow()` guard stripped
  — that builtin is self-host-only, and a probe carrying it fails to COMPILE
  natively, which is worth knowing before someone reads the compiler's exit code
  as a byte count).

  **What it actually is, attributed.** `FERN_LEAKCHECK=1` on the three-iteration
  reduction: native `allocs=3 frees=3 live_bytes=0`, self-host
  `allocs=6 frees=3 live_bytes=72`. The self-host allocates TWICE per iteration
  and frees once — so nothing is missing a free on an object native also builds;
  there is an extra object. `FERN_RC_TRACE=1` names it, resolved through
  `nm -n`:

  | site | size | fate |
  |---|---|---|
  | `__fn_mkq +0x34` | 0x30 | freed — `__fern_snapshot_dec` per iteration, `__fern_arr_dec` at exit |
  | `__fn_main +0x6e` | **0x18** | **never freed** |

  The leaked box is the CALLER's, not the callee's, and it is the `string`
  ARGUMENT's box — materialised at the call site and released by nothing. That
  is why a producer building its own field is flat (the box is built inside the
  callee, where it is reclaimed) and why a fresh argument costs 24 + 40.

  **So the slice is the STRING sibling of `paramCountedRetain`.**
  `stash_fresh_str_arg` releases a fresh string argument only at a BORROWABLE
  position, and a callee that STORES its parameter is not borrowable by
  construction — the same wall the array half hit at #6522, closed there by
  `array_param_counted_of`. That registry is ARRAY-only and says so in its own
  header. A `string` twin (`"SCNT:"`), consumed at the same call site the array
  one already feeds, is what this row needs.

  One difference to get right, and it is the trap the array half is still stuck
  behind: `array_param_counted_of` demands a CONCRETE SCALAR result, which
  refuses exactly the struct-returning producer this row is made of. The property
  actually needed is native's `resultCannotAliasArg` — the result cannot BE the
  argument. A struct result CONTAINS it, which is fine (the callee's counted
  store and the caller's post-call dec net to the one reference the struct owns);
  the unsafe shape is a result of the parameter's OWN type, `both(t: string, k):
  string { var n = Q { tag: t, k: k }; return n.tag; }`, which the use-vocabulary
  credits and only a result-type test can refuse. Write the string rule that way
  rather than copying the scalar-result guard across.

  Refs #6544 #6522 #6887 #4451.
- 2026-08-18: **#6545 lands. The blocker was never the string predicate — the
  emitter elided the construction retain BY NAME, and an array field has been
  over-releasing that way all along.**

  The analysis was already right, and already byte-identical to native. On the
  two-literal detector both compilers produce `movedLocals: s` and a single
  `moveSites: 2:76` — the SECOND literal, the last use. `rc_ml_last_use` is
  name-based, not positional, so it never marked the first; and no probe over
  desugar-heavy shapes (`for i in 0..n`, concat inits, nested literals,
  `xs.append(P{v:s})`) produced a `0:0` ident. The 2026-08-16 entry's
  node-identity story was wrong on both counts.

  The break was one layer down. Native keeps two tables — `moveSites` keyed by
  NODE gates the inc, `movedLocals` keyed by NAME gates the exit-sweep dec
  (`internal/ir/rc_analysis.go:61-71`). The self-host collapsed the inc side
  onto the name table: `moves_local(name)` answered "is this local moved?" at
  EVERY construction consuming it, so with two literals both retains were
  elided and one buffer was released twice. The local's own dec elision was
  never the problem — `moved_elided` is recorded by the eliding emitter, so
  that half stayed balanced.

  So the fix is a `move_sites` field on `LowerState` and a `moves_local_at(name,
  line, col)` that asks WHERE as well as WHICH; `moves_local` is deleted. The
  string arm on `rc_field` is then the small half.

  **The array field is the part worth remembering.** Array fields have been in
  the construction-move set since the beginning, so `struct Q { v: i32[] }` with
  two literals over-released on every build before this — `__rc_underflow()` 1
  against native's 0, on pristine `main`, with no source change. It was invisible
  because nothing tested two literals over one array local, and because the
  string arm's absence hid the only shape anyone was looking at. Filed as
  #7055; `arrdrop-two-alias-detector` is the regression test.

  The three `alloc_flat_struct_string_field` divergence rows are deleted — the
  fixture now prints `flat`, matching native. Refs #6545 #6887 #7055 #4451.
- 2026-08-18 (isolation): **The fresh-array-arg row was never blocked by the arg
  temp at all.** Settled by dumping the registry and the call-site booleans
  rather than inferring from which probes were flat — an inference that had
  already produced two wrong answers on this row.

  Witnessed, `node(deps: i32[], k: i32): Node { return Node { deps: deps, k: k }; }`
  called as `node(mk(i), i)`:

  | question | witness | answer |
  |---|---|---|
  | is `node` in the counted-retain registry? | dump of `array_param_counted_of` | NO with the scalar-result guard, `ACNT:node\|10` without it |
  | does the arg stash fire once it is? | dump at the stash site | YES — `arrlit=1 rel=1 ok=1`, identical to the working `keep` |
  | does the shape get cheaper? | `__heap_bump_bytes` | NO — 103 B/round either way |

  So the guard does gate the registry entry, and lifting it does make the stash
  fire — but the release changes nothing measurable, because the struct-literal
  construction already inc'd the array. The dec takes rc 2 -> 1 and the returned
  `Node` still owns it. **The 103 is the caller failing to reclaim a struct
  RETURNED from a call, together with its array field — a different leak from the
  arg temp, which the counted-retain admission had already handled.**

  Two consequences for whoever picks this up. The `"ARR:"` arg-temp work on this
  row is DONE; do not look for more there. And `alloc_flat_fresh_array_arg`'s 798
  should be re-decomposed against the returned-struct reclaim before any further
  code — the arg-temp framing that has driven the last four slices does not
  explain the residual.

  The scalar-result guard stays. It is not what costs this row anything, it is
  the conservative direction, and native's own reasoning for lifting it
  (`countedArgTemp` carries no result-type check) has not been demonstrated here
  end-to-end — `both(deps, k)`, which returns the array back out of the struct,
  is the shape that would have to be measured first. Refs #6522 #4451.
