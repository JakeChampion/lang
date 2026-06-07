# Porting Perceus RC to the self-hosted compiler

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
`asm_arm64_run.fern` / wasm):

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
