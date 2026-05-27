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

1. **Uniform rc header on heap strings**, per backend: change every
   heap-string allocation path (`__str_concat`, `__str_slice`,
   `string_from_bytes`, `int_to_string`, …) to alloc through the
   rc-headered allocator (`__fern_alloc_rc1` exists; wasmbin's bare
   `__fern_alloc` paths switch to it), so `data-8` always holds a real
   rc. Length readers keep reading their existing offset.
2. **Static-literal sentinel header**: `OpConstStr` emits interned
   literals with a `0x80000000` rc-sentinel header (like enum
   sentinels), so `__fern_rc_inc/dec` short-circuit on them rather than
   relying on a fragile low-address guard.

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
3. **String STRUCT FIELDS / TUPLE elements** — `arrElemIsRcTracked` +
   the field/element drop + construction retain.
4. **String ARRAY elements** (`string[]`) — array drop deep-decs string
   elements; `string[]` buffer + each element string freed.
5. **String ENUM payloads** (`enum E { S(string) }`) — the enum
   variant-plan deep-drops string payloads.
6. **String CLOSURE captures** — the closure drop thunk decs captured
   strings.
7. **`Map[K, string]` VALUES** — generalize `mapValHasDrop` to a
   string-per-value drop in `__drop_map_via_*` + set/get retain +
   overwrite pre-drop.
8. **`Map[string, V]` KEYS** — the column-walk also decs the string
   keys (keys leak today for ALL key types; strings are the only
   heap key). Likely a dedicated `__drop_map_keys` walk.

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

1. **Shared-buffer VIEW strings break the rc-header invariant.**
   `__fern_args` (and `__fern_env`) return strings whose `data` points
   *into a shared buffer* (`argv_buf` / cached `environ`), not an
   individually-allocated buffer — so `data-8` is mid-buffer, not an rc
   header. These strings can't carry per-string headers. Options:
   (a) **copy** each into its own rc1 string at production (uniform, but
   changes args/env to allocate); (b) a **dec-side immortal-region
   check** that recognizes argv_buf/environ addresses and skips them.
   (a) is cleaner and keeps the dec's invariant ("every non-inline,
   non-low-address heap string has an rc header") intact — recommended.
   Until resolved, the dec must NOT be turned on, or it will misread a
   view string's `data-8`.

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

## What this doc IS / IS NOT

- IS: the sequencing + design for string reclamation, on the CURRENT
  (heterogeneous, per-backend) string ABI — not blocked on SSO.
- IS NOT: a scope estimate for any single slice (each has its own; ~8
  reclamation slices above, atop the 2 per-backend prerequisites).

https://claude.ai/code/session_01Vrwb6rXeWdQ9jBLH34TSaQ
