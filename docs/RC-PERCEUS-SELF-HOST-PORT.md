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
<<<<<<< HEAD
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
=======
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
>>>>>>> 7031dd4 (self-host wasm: reclaim user-method array results)
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
