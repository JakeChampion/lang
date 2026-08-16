# RC string reclamation plan

Implementation plan for bringing `string` into the Perceus rc
reclamation system, the way arrays / structs / enums / closures /
tuples / Map values already are.

Date: 2026-05-26.
Status: design, not implementation. **NOT blocked on SSO** (the active
wasm backend already uses the two-word form; see "Prerequisite
(revised)"). The real prerequisites are a uniform heap-string rc header
+ static-literal sentinel, per backend — self-contained and shippable
wasm-first.

## Why

Every other heap value reclaims at its last reference (the merged rc
arc: array/struct/enum/closure/tuple locals + fields + elements +
payloads + captures, and the full `Map[K, V]` value matrix incl.
overwrite). `string` is the one pervasive heap value still excluded:

- `StringType` is absent from every rc predicate — `arrElemIsRcTracked`,
  `needsRcIncOnAlias`, the `emitDec` sweep's `rcTracked`, `zeroRcTracked`,
  `isOwnedRcLocal` — so heap strings are never inc'd, swept, or freed.
- A heap string from `__str_concat` / `string_from_bytes` /
  `int_to_string` / slicing leaks its buffer permanently. In a
  long-running edge handler that builds response strings per request,
  this is an unbounded leak — exactly the workload the rc work targets.

String reclamation is the largest remaining reclamation gap AND the
highest per-request payoff (header/body/JSON string churn).

## Why this is NOT a clean incremental slice (yet)

Unlike arrays/structs, `string` cannot simply be added to the rc
predicates, for two reasons:

### 1. Three-way representation — a wrong free is corruption

A `string` operand is one of three shapes, and the dec MUST
disambiguate (freeing/deref'ing the wrong one corrupts the heap, not
just leaks):

| form          | how to detect                          | reclaim? |
|---------------|----------------------------------------|----------|
| **inline**    | SSO flag bit set in the value/`len`    | NO — holds bytes, not a pointer |
| **static literal** | `data` points into the data segment (interned, no rc header) | NO — immortal |
| **heap**      | flag clear, `data` ≥ heap base, rc header | YES |

The current single-pointer `__fern_rc_dec` (low-address guard +
high-bit-sentinel) is insufficient: it cannot see the inline flag, and
the inline form's "pointer" is actually packed bytes that must never be
dereferenced.

### 2. The string ABI is heterogeneous across backends

`docs/SSO-PLAN.md` describes an in-progress SSO migration, but it is
**stale** with respect to the active backends. Actual current state
(surveyed 2026-05-26):

- **wasm (`internal/codegen/wasmbin`, the ACTIVE wasm backend — `cmd/fern`
  and the e2e suite both route through it)**: `string` is already
  **two-word `(data, len)`**. e.g. `__str_concat` is
  `(a_data, a_len, b_data, b_len) → (data, len)`. The inline (tiny-SSO)
  flag rides `len`'s top bit. So the "two-word flip" the SSO doc lists as
  remaining work is, for wasmbin, **done**. BUT heap results are
  allocated via bare `__fern_alloc` (`buildStrConcatBody`) — **no rc
  header**.
- **natives (x86-64 / arm64)**: `string` is a **single pointer** to a
  length-prefixed buffer (`__fern_strcat(a, b)` — one arg per string).
  Some string-producing helpers already alloc via `__fern_alloc_rc1`
  (rc header present), others don't — **rc-header presence is not
  uniform**.

So the representation differs by backend (two-word wasm vs single-ptr
native) and the rc header is missing or inconsistent. The string drop
emission therefore can't be purely target-agnostic IR the way the rest
of the rc arc is — it needs a representation-aware helper per backend
(like the existing `WidthString` drop marker that already fans a string
drop into two slots on wasm).

## Prerequisite (revised)

**NOT blocked on SSO** — the original gating ("wait for the two-word
flip") was based on the stale SSO doc; wasm already has the two-word
form, and natives don't have an inline form at all (so their dec doesn't
need a second word). The actual prerequisites are smaller and
self-contained:

1. **Uniform rc header on heap strings**, per backend — **DONE (wasm
   + x86_64 + arm64 single-word).** Every heap-string-producing path
   on each backend now allocates through `__fern_alloc_rc1` so
   `data-8` always holds a real rc.

   - wasm (`internal/codegen/wasmbin`): the audit shipped with the
     plan's first carrier work — `__str_concat`, `__str_slice`,
     `__fern_string_from_bytes`, `__fern_read_line`, etc.
   - x86_64: 12 producers converted across two PRs (`__fern_strcat`,
     `__fern_str_slice`, `string_from_bytes`, `__fern_strbuf_take`,
     `__fern_env` per-value copy, `__fern_args` per-arg copy,
     `__fern_read_line` Some, `__fern_reader_read_line` Some,
     `__fern_tcp_recv`, `__fern_read_file`,
     `__fern_reader_read_chunk`, `__fern_random_bytes`).
   - arm64 single-word: same 12 producers converted on the single-
     word ABI path. The two-word arm64 variants (`*2W_*`) belong to
     the in-progress `TwoWordOverride` migration and are untouched.

   Length readers keep reading at `data-4` — on natives the **L2
   layout** (rc at base+0, length-or-rc1-size at base+4, data at
   base+8) lets string length and rc1's payload-size slot overlap
   safely (strings compute their alloc size from length, not from
   data-4, so the rc1 size slot is sacrificed for the length). The
   alternative L1 layout (length appended after rc1's 8-byte header,
   total +8 bytes per string) was rejected as wasteful for the
   project's short-lived-process / edge-handler workload.
2. **Static-literal sentinel header** — **DONE (wasm + x86_64 + arm64).**
   `internString` on each backend now prefixes every interned
   heap-form literal with an 8-byte `0x80000000` rc-sentinel header.

   - wasm: original prereq-2 work.
   - natives (x86_64 + arm64): each `.LStr_N` label now sits at
     `base+8` with `[base+0]=0x80000000` (rc sentinel) and
     `[base+4]=length` — matching the L2 heap layout above. The
     0x80000000 sentinel makes `__fern_rc_inc/dec` short-circuit on
     literals so the native string-dec can safely run over
     container-stored / aliased literals without a fragile
     address-range guard.

   Literals were load-bearing because they intern at 1024+, ABOVE the
   low-address guard (1024), so the guard alone never covered them
   — a container-stored or aliased literal reaching the dec would
   have misread mid-data-segment bytes as an rc. With the header,
   uniform string inc/dec is safe over literals, unblocking the
   container-field reclamation slices.

3. **Native SSO inline-tag guard** — **DONE.** Added late in the
   x86_64 / arm64 reclaim work after CI surfaced a latent crash:
   native strings ≤7 bytes are SSO-packed inline with bit 0 of the
   "pointer" word set as the tag. Treating them as pointers and
   reading `[data-8]` corrupts memory. Fix: low-bit guard at the top
   of `__fern_rc_inc` / `__fern_rc_dec` on both natives. Heap
   pointers from `__fern_alloc` / `__fern_alloc_rc1` are always
   8-byte aligned (low bit clear), so the guard is a no-op for every
   non-string caller (arrays / structs / enums / closures / map
   handles / etc.) — the only effect is to safely skip inline-tagged
   string values. Hardens every string-rc call site uniformly.

These are per-backend runtime/codegen changes (wasm + x86-64 + arm64),
self-contained and shippable one backend at a time (reclamation can go
wasm-first, as the original rc arc effectively did). Once a backend has
(1) + (2), the reclamation slices below apply.

## Design

### Heap-string rc header (prerequisite 1)

`__str_concat` / `__str_slice` / `string_from_bytes` / `int_to_string`
and friends allocate heap strings through the rc-headered allocator
(`__fern_alloc_rc1`) so `data-8` holds rc=1 — identical to the
array/struct convention so `__fern_rc_inc` / `__fern_box_free` work
unchanged. Length readers keep their existing offset. Static literals
carry a `0x80000000` sentinel header (prerequisite 2); inline strings
(wasm) carry no pointer at all.

### The string dec helper

A representation-aware `__fern_str_dec` (two-word `(data, len)` on wasm;
single `(ptr)` on natives — natives have no inline form):

1. wasm only: `if IsInline(len) return;` — inline: no heap, nothing to
   free.
2. `__fern_rc_dec`/`box_free` of the heap buffer at `data-8`; the
   sentinel header makes static literals a no-op, so no fragile
   address-range guard is needed.

`__fern_str_inc` is the retain mirror. The IR emits these in place of
the plain `__fern_rc_inc` / `dec` for string-typed values; the existing
`WidthString` drop marker already carries the wasm two-slot fan-out.

### Predicate wiring (mirrors the merged rc arc)

Add `ast.StringType` to, and special-case the two-word shape in:

- `arrElemIsRcTracked` — strings in struct fields / array elements /
  enum-tuple payloads / closure captures become tracked.
- `needsRcIncOnAlias` — `var s2 = s1`, `return s`, struct/array/tuple
  element init, call args (alias sites) retain via `__fern_str_inc`.
- `rcTracked` + `zeroRcTracked` (the `emitDec` sweep) — string locals
  swept + zeroed.
- `isOwnedRcLocal` — move-on-return / move-on-alias for string locals.
- `rhsTainted` — a fresh `__str_concat` result (Call) is owned;
  literals (`OpConstStr`) are immortal (never freed, so treat as
  not-eligible / inert).
- `emitDec` — a new string branch: eligible → `__fern_str_dec`. The
  two-word value occupies two slots, so the sweep must load both
  `data` and `len` (the existing `WidthString` drop marker already
  fans a string drop into two slots on wasm — extend it).
- `decValueOnStack` / `dropStructField` — string fields/elements
  flat-dec via `__fern_str_dec`.

### Map keys / values (the last mile)

`Map[string, V]` keys and `Map[K, string]` values: the value column /
key column store two-word strings post-flip (SSO Step 8). Reclaim via
the same `mapValHasDrop`-style routing already built for struct/enum
values, with a string-specific per-element drop (`__fern_str_dec`) in
the generated `__drop_map_via_*` walk, plus retain on set/get and the
overwrite pre-drop (the `__map_lookup_val` machinery from the
struct-value overwrite PR generalizes).

## Slice breakdown (post-prerequisite)

Mirrors the rc arc's incremental shape; each ships with rc-correctness
corpus entries (churn-free + escapes) on x86-64 / arm64 / wasm, plus the
differential fuzz and `__rc_underflow_count` guard.

1. **Heap-string rc header + `__fern_str_inc/dec`** — runtime + the
   allocator change (folds into the SSO length-prefix-removal PR).
   No IR wiring yet; carrier-only. Test: alloc/free a heap string
   directly.
2. **String LOCALS** — `rcTracked` / `computeFreeEligible` /
   `zeroRcTracked` / `isOwnedRcLocal` / `rhsTainted` / `emitDec` string
   branch + `needsRcIncOnAlias`. The common case: a `__str_concat`
   result in a local frees at scope exit. Highest single payoff.
3. **String STRUCT FIELDS** — **DONE.** The eligibility gate in
   `emitAliasInc` was removed so string retain is now uniform (safe:
   views are gone, literals carry the sentinel header) — a borrowed
   read of a string out of a struct co-owns it. Both struct-local
   reclamation (inline `emitDec`, eligible branch) and the generated
   `genStructDropFn` dec direct string fields via a two-word
   `WidthString` load + `__fern_str_dec`, inside the `rc==1` (unique)
   guard only — never the escaped/non-eligible branch (where a still-
   reachable field would be freed → UAF; it leaks there instead, which
   is safe). `needsRcIncOnAlias` already retains alias-shaped field
   initialisers; fresh values (concat / literal / call) are moved in and
   freed by the drop. NOTE: other container constructions (array / enum
   / closure) now also retain strings uniformly but don't yet dec them
   on drop → they LEAK (safe, no over-release) until their own slice
   below lands. **TUPLE elements** — **DONE** (follow-up): tuple-local
   deep-drop dec's string elements (`WidthString` + `__fern_str_dec`,
   rc==1 guard), the destructure projection dups them via
   `__fern_str_inc` so the binding co-owns, and `tup.N` direct reads
   retain through `needsRcIncOnAlias`. **Nested tuples** (tuple in a
   struct / array / enum payload / tuple) — **DONE.** Each distinct
   tuple shape registered via `dropFnNameFor` gets a generated
   `__drop_tuple_<mangled>` recursive-drop fn (sibling of
   `__drop_struct_<Name>`): is_unique-gates the box, dec's every rc-
   tracked element (string elements split by wasm two-word vs native
   single-word ABI, the rest recurse via `appendChildDrop`), then
   returns the box to the freelist. arm64 two-word boxed strings stay
   excluded — same `__fern_str_dec` gate as the rest of the native
   string-reclaim path.
4. **String ARRAY elements** (`string[]`) — **DONE.** New runtime helper
   `__fern_drop_arr_str` (mirrors `__fern_drop_arr_ptr`): on the array's
   last reference walks the two-word elements calling `__fern_str_dec`,
   then frees the buffer. Wired at the array-drop sites (`emitDec` array
   local, `dropStructField`, `appendChildDrop`) for `string[]` on wasm,
   gated eligible. Construction / push retain is the uniform alias-inc +
   escape-move machinery (pushed elements move in; the source escapes →
   not separately freed). Covers `string[]` locals, `string[]` struct
   fields, and read-out aliases (`var s = arr[i]` retains via
   `needsRcIncOnAlias`). `string[][]` inner buffers still flat-dec (a
   later array-of-array slice).
5. **String ENUM payloads** (`enum E { S(string) }`) — **DONE.** Both
   enum drop plans (`uniformEnumDropLoads`, `enumVariantDropPlan`) now
   classify a string payload (dropKind 3), and the three payload-drop
   emitters (`decValueOnStack`, `dropStructField`, `appendChildDrop`)
   reclaim it via the two-word `__fern_str_dec` (the payload load already
   uses the string-aware `payloadLoadOpFor`). No construction inc is
   needed: an eligible enum can only carry fresh/inline payloads (any
   alias-shaped arg — Ident / FieldAccess / Index — taints the variant
   `Call` ineligible), so the payload is always moved in and the drop
   frees it. Match bindings that extract the payload into an outliving
   local co-own correctly (verified by a churn corpus entry). Enums built
   from a borrowed local stay ineligible and leak (safe), as before.
6. **String CLOSURE captures** — **DONE.** `hasRcCapture` now counts a
   string capture (so the per-closure `__closure_drop_<name>` thunk is
   generated), and the thunk dec's it via a two-word `WidthString` load +
   `__fern_str_dec` (capture offsets already use the string-aware
   `irCaptureSlotSize`). MakeClosure retains the alias-shaped capture via
   the uniform `emitAliasInc`. The `thunkSafe` gate was widened to
   strings: a string capture not inc'd at MakeEnv (e.g. a CaptureRef in a
   nested closure) forces the generic env-only drop so the thunk never
   over-releases it (the capture leaks then — safe). Verified for both
   scope-local and escaping (returned) closures.
7. **`Map[K, string]` VALUES** — **DONE (wasm + x86_64).** A
   `Map[K, string]` value is stored BOXED on wasm — the value column
   holds an 8-byte `(data, len)` cell pointer (`boxIntoCell` at set).
   On native single-word strings (x86_64) the cell holds the data
   pointer directly (no boxing — fits in the pointer-wide slot).
   Reclamation is driven by the IR static type (the runtime valKind
   stays 1, so `.values()` / runtime overwrite are undisturbed): a
   generated `__drop_map_str_values` column walk reclaims each value's
   buffer at the map's last reference — `__fern_str_dec` per cell on
   wasm (+ `__fern_cell_free`), `__fern_rc_dec` per data pointer on
   x86_64 (the generator branches on ptrW). The set retains an aliased
   value (`__fern_str_inc` on wasm, `__fern_rc_inc` on x86_64), `m.get`
   /`m.get_or` / `m.iter().value()` all retain the returned string, and
   a key OVERWRITE pre-drops the replaced buffer via `__map_lookup_val`
   + `__fern_str_dec` (wasm) or `__fern_rc_dec` (x86_64). The 8-byte
   cell on wasm leaks (like every boxed-value cell today — a minor
   follow-up); the dominant string buffer is reclaimed.

   **arm64 is excluded.** arm64 IR-lowering forces `TwoWordOverride=true`
   (see `internal/codegen/arm64/arm64.go`), so strings are stored boxed
   like wasm — but arm64 lacks the native `__fern_str_dec` and
   `__fern_cell_free` runtime helpers, so the boxed reclaim path can't
   run there yet. arm64 stays on the pre-slice (leaking-but-stable)
   behaviour pending a future PR that ports those helpers.

   The SSO inline-tag (`bit 0` of the data pointer on natives) and
   literal sentinel (`0x80000000` at data-8, from prereq 2) are both
   handled by guards at the top of `__fern_rc_inc`/`__fern_rc_dec` —
   added during the Slice 8 work and load-bearing for every native
   string-rc call site. Verified across set/get/get_or/iter/overwrite/
   escape + inline (≤7-byte) + literal + heap + churn.
8. **`Map[string, V]` KEYS** — **DONE (wasm + x86_64).** String keys
   are stored boxed in the KEY column on wasm (an 8-byte `(data, len)`
   cell), like values; on native single-word strings (x86_64) the slot
   holds the data pointer directly. A generated `__drop_map_str_keys`
   column walk (the value walk parameterised on the column byte-offset:
   0 for keys, `ptrW` for values; ptrW-branched body — boxed deref on
   wasm, direct rc_dec on x86_64) reclaims each key buffer at the map's
   last reference, emitted alongside the value walk in the `emitDec`
   Map branch (both self-guard on rc==1). `set` retains an aliased
   string key (`__fern_str_inc` on wasm, `__fern_rc_inc` on x86_64).
   `m.iter().key()` retains the returned string analogously to value.

   Known accepted leak: an OVERWRITE discards the freshly-boxed-or-
   inc'd new key (the runtime keeps the existing one in place), so the
   discarded key buffer leaks the +1 — safe (no double free), bounded,
   and keys leaked entirely pre-slice. The 8-byte cell on wasm also
   leaks (as for all boxed cells).

   **arm64 excluded** for the same reason as Slice 7 (boxed strings
   without native str_dec / cell_free runtime).

   Verified across fresh / aliased / overwrite keys, `Map[string,
   string]` (both columns), churn, inline keys, literal keys.

   NB: the `wasmtime` CLI reports a **non-zero exit code** from a
   component as `invalid expected discriminant` (the WASI `run`
   `result` Err variant) — this is NOT a crash. RC corpus programs
   must return 0 on success (`(checksum - expected) + underflow`).

## Risks

- **Freeing a static literal** → heap corruption. Mitigation: the
  address-range guard in `__fern_str_dec` + the literals' data-segment
  placement; an ir-level test that a literal-only string local emits no
  free, and a corpus entry interning a literal into a churned local.
- **Dereferencing an inline string** → reads garbage as a pointer.
  Mitigation: the inline-flag check is the FIRST thing `__fern_str_*`
  does; an ir test that an inline-form local skips the heap path.
- **Two-slot sweep accounting (wasm)** — a wasm string occupies two
  operand slots; the dec sweep, move analysis, and `seen`-dedup must
  treat the pair atomically (the `WidthString` marker already exists for
  this on the drop side). Natives stay single-slot. This per-backend
  shape difference is the fiddliest part.
- **Heterogeneous heap-string allocators** — prerequisite 1 must catch
  EVERY heap-string-producing path on EACH backend; a missed path leaves
  a header-less buffer that the dec misreads. Audit `__str_concat`,
  `__str_slice`, `string_from_bytes`, `int_to_string`, byte/HTTP
  marshaling, etc., per backend.

## Producer audit findings (wasm, in progress)

Surveyed while landing the carrier prerequisite. Headered so far
(`__fern_alloc` → `__fern_alloc_rc1`, carrier-only, full gauntlet green):
`__str_concat`, `__str_slice`, `__fern_string_from_bytes` (so
`int_to_string` / `bytes_to_lang_string` reclaim transitively),
`__fern_read_line`. `__fern_reader_read_line` delegates to read_line —
covered transitively.

Two complications the remaining producers raise:

1. **Shared-buffer VIEW strings break the rc-header invariant.** —
   **RESOLVED (option a).** `__fern_args` / `__fern_env` / `__fern_env_at`
   used to return strings whose `data` points *into a shared buffer*
   (`argv_buf` / cached `environ`), not an individually-allocated buffer
   — so `data-8` was mid-buffer, not an rc header. These can't carry
   per-string headers. Fixed by **copying** each entry into its own
   owned string at production via the new `__fern_str_copy(ptr, len)`
   runtime helper (inline-pack ≤7 bytes, else rc1-headered heap copy).
   All five wasm view-producers route through it: `buildArgsBody`,
   `buildArgsBodyP2`, `buildEnvAtBody`, `buildEnvBody`, `buildEnvBodyP2`.
   This keeps the dec's invariant intact ("every non-inline,
   non-low-address heap string has an rc header") — so the structural
   container-drop slices can safely dec string fields/elements without a
   per-value eligibility gate (a view can no longer reach a container).
   The original alternative (b) — a dec-side immortal-region check —
   was rejected as fragile (argv_buf/environ live at dynamic heap
   addresses, not a static range).

2. **Multi-alloc builders need per-alloc classification.** e.g.
   `buildReadFileBody` reuses one `alloc` for the path_open scratch, the
   str-normalize temps, AND the file-content string. Swapping the shared
   `alloc` var → rc1 over-headers the temps (harmless carrier-side) and
   headers the content string — but only if the content is allocated via
   that var and returned as the string data; confirm per builder rather
   than swapping blindly.

Remaining to classify/handle: `read_file` (P1/P2), `reader_read_line_fd`
(P1/P2), `reader_read_chunk` (P1/P2 — returns string vs u8[]?),
`__fern_args` (P1/P2, view — needs option (a)/(b)), `__fern_env` (view),
the IR concat fast path. The dec can't turn on until all are headered or
explicitly skipped.

## Current status (Map API is the leading edge)

| Slice | wasm | x86_64 (single-word) | arm64 (two-word boxed) |
|---|---|---|---|
| Prereq 1 — uniform rc header on heap strings | DONE | DONE | DONE (single-word path only) |
| Prereq 2 — static-literal sentinel header | DONE | DONE | DONE |
| Prereq 3 — SSO inline-tag guard on rc_inc/dec | n/a | DONE | DONE |
| Slice 2 — string LOCALS | DONE | DONE | DONE |
| Slice 3 — string STRUCT fields | DONE | DONE | DONE |
| Slice 3 follow-up — string TUPLE elements | DONE | DONE | DONE |
| Slice 4 — string ARRAY elements (`string[]`) | DONE | DONE | DONE |
| Slice 5 — string ENUM payloads | DONE | DONE | DONE |
| Slice 6 — string CLOSURE captures | DONE | DONE | DONE |
| Slice 7 — `Map[K, string]` VALUES + retains | DONE | DONE | DONE |
| Slice 8 — `Map[string, V]` KEYS + retains | DONE | DONE | DONE |

Every row across the matrix is DONE in the sense the rows are scoped to:
`string` participates in rc reclamation across LOCALS / fields / array
elements / tuple elements / enum payloads / closure captures / Map
values + keys on wasm + x86_64 + arm64-TwoWordOverride.

A DONE row is not a claim that every SHAPE built out of that slice is
leak-free — the matrix tracks whether the slice's retain/release wiring
exists, and shapes that compose two slices have their own gaps. Known
open ones, each with a conformance case pinning the fixed half:

  - A struct literal consuming a string local that is READ AFTER the
    literal leaks the buffer once per construction. The last-use
    spelling moves the string into the field and is flat
    (`alloc_flat_struct_string_field`'s `keep` control is the leaking
    shape, deliberately outside that case's measured rounds).
  - A FREE FUNCTION with an identity return (`if (…) { return s; }`)
    whose materialised result is consumed by another call leaks that
    result. The receiver-method spelling of the same body is reclaimed
    (`alloc_flat_method_identity_return`).
  - `Cell[string]` reclaims neither the cell box's string nor, on
    x86_64, routes its element through the string-aware walk — 32 B per
    construction on all three backends.

The Map-key OVERWRITE path still accepts an aliased-overwrite-leak on
every backend (safe, no double-free; the runtime keeps the existing key
so the freshly-boxed key's inc has no balancing dec) — documented at the
gate.

The native Map work landed across these PRs (all merged): #1616 #1618
#1621 #1625 (carrier prereqs), #1628 #1635 #1638 #1641 #1643 (Slice 7
+ Slice 8 + retain/overwrite gaps + the SSO inline-tag guard).

Every native single-word string-reclaim slice has now landed on
x86_64:

  - Slice 2 (LOCALS): commit `66577ec1` — adds `StringType` to
    `rcTracked` / the alias-inc fall-through under
    `b.ptrW == 4 || (b.ptrW == 8 && !ast.UseTwoWordStrings(b.ptrW))`,
    so a fresh string local (concat / slice / call result) frees at
    scope exit via `__fern_rc_dec`. Aliased string locals get
    retained at alias points via `__fern_rc_inc`. The SSO
    inline-tag low-bit guard keeps short inline strings safe.
  - Slice 3 (STRUCT fields): commit `30341852` — adds the parallel
    `OpLoad WidthPtr` + `__fern_rc_dec` + drop branch to
    `genStructDropFn`'s inline field loop, `decValueOnStack`,
    `dropStructField`, and `appendChildDrop` for native single-word.
  - Slice 3 follow-up (TUPLE elements): commit `5519258c` — tuple-
    local deep-drop releases string elements on native single-word
    (`__fern_str_dec`, rc==1 guard), the destructure projection dups
    them via `__fern_rc_inc` so the binding co-owns, and `tup.N`
    direct reads retain through `needsRcIncOnAlias`. Nested tuples
    reach the same releases through the generated
    `__drop_tuple_<mangled>` (see the TUPLE elements entry above).
  - Slice 4 (ARRAY elements): commit `78942f4d` — adds the
    single-word ptr → `__fern_rc_dec` branch to `arrElemDec` and
    the matching alias-inc path so `string[]` element overwrites
    + scope-exit sweeps free correctly.
  - Slice 5 (ENUM payloads): commit `f69ff7b0` — `dropKind`
    classification now returns kind 4 for native single-word
    strings, so the existing `payloadLoadOpFor` (WidthPtr) +
    `decValueOnStack` / `appendChildDrop` emitters (updated in
    Slice 3) reclaim enum-variant string payloads.
  - Slice 6 (CLOSURE captures): commit `49d54a63` — three
    target-aware gates flip together: `hasRcCapture` (triggers
    per-closure thunk generation), `genClosureDropThunk` (emits
    `OpLoad WidthPtr` + `__fern_rc_dec` from the generated
    `__closure_drop_<name>` thunk, vs the wasm two-word
    `WidthString` + `__fern_str_dec` branch), and the `thunkSafe`
    gate (the closureTarget pin that demands every rc-tracked
    capture was inc'd at MakeEnv — needed because the matching
    `__fern_rc_inc` rides through `emitAliasInc`'s native fall-
    through on aliased captures only, so a non-alias source would
    over-release at the thunk).

**Next: the two-word arm64 column.** Porting `__fern_str_dec` and
`__fern_cell_free` to `arm64.go` unlocks every slice in that column
in one move — Slices 2-8 (plus the TUPLE follow-up) flip to DONE
together. No more per-slice follow-ups on natives; the remaining
work is one cross-cutting backend port.

## What this doc IS / IS NOT

- IS: the sequencing + design for string reclamation, on the CURRENT
  (heterogeneous, per-backend) string ABI — not blocked on SSO.
- IS NOT: a scope estimate for any single slice (each has its own; ~8
  reclamation slices above, atop the 3 per-backend prerequisites).

https://claude.ai/code/session_01Vrwb6rXeWdQ9jBLH34TSaQ
