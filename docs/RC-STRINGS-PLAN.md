# RC string reclamation plan

Implementation plan for bringing `string` into the Perceus rc
reclamation system, the way arrays / structs / enums / closures /
tuples / Map values already are.

Date: 2026-05-26.
Status: design, not implementation. **Gated on the SSO two-word ABI
flip** (see "Prerequisite" below) — do not start the reclamation
wiring until that lands.

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

### 2. The string ABI is mid-migration (SSO)

`docs/SSO-PLAN.md` is an in-progress, multi-PR migration of the string
representation. Current state:

- **wasm**: shipped the *single-i32 tiny-SSO* form (PRs #351–#362):
  3-byte inline cap, top-bit flag, else a heap pointer to a
  length-prefixed buffer. The **two-word `(data, len)` ABI flip
  (cap 3 → 7) is "remaining work" item #1.**
- **natives (x86-64 / arm64)**: not SSO'd — `string` is a single
  pointer to a `[len:4][bytes]` buffer. SSO is "remaining work" #2.
- Heap-string allocation paths differ (interned literals in the data
  segment with a length prefix; `__str_concat` results; `__fern_alloc_rc1`
  blocks for some carriers) — there is **no single, stable heap-string
  layout with a guaranteed rc header** today.

Wiring reclamation into this moving target would (a) have to handle the
transitional tiny-SSO form, (b) be redone after the two-word flip, and
(c) risk conflicting with the active SSO PRs. The dec also fundamentally
needs `len` on the stack to read the inline flag cheaply — which is
precisely what the two-word ABI delivers.

## Prerequisite

**SSO "remaining work" #1 (two-word ABI flip) on a backend, then #2
(native SSO).** After the flip:

- `string` is two words `(data, len)` on the operand stack; the inline
  flag is `len.top_bit` — readable without a heap load.
- Heap form has a **single stable layout**: `data` → raw bytes, length
  on the stack (the 4-byte prefix is gone per SSO Step 10 cleanup).
- This is the natural point to give heap strings a uniform rc header
  (rc at `data - 8`, mirroring arrays/structs) in the same PR that
  removes the length prefix.

Reclamation slices below assume this end-state. They can begin once the
flip lands on the **wasm** backend (reclamation can ship wasm-first, like
the original rc arc effectively validated wasm + x86 with arm64 via CI).

## Design (post-flip)

### Heap-string rc header

`__str_concat` / `__str_slice` / `string_from_bytes` / `int_to_string`
and friends allocate heap strings through a shared allocator that lays
out `[rc:4|pad:4 | bytes...]` with `data = base + 8`, rc=1 at `data-8`
— identical to the array/struct convention so `__fern_rc_inc` /
`__fern_box_free` work unchanged. Static literals keep their
data-segment offset (no rc header) and are distinguished by address
range; inline strings carry no pointer at all.

### The string dec helper

A new `__fern_str_dec(data, len)` (two-word) that:

1. `if IsInline(len) return;` — inline: no heap, nothing to free.
2. `if data < heap_base return;` — static literal / low-address: immortal.
3. else `__fern_box_free`-style dec/free of the heap buffer at `data-8`.

`__fern_str_inc(data, len)` is the retain mirror (skips inline + static).
Both are pure runtime helpers; the IR emits them in place of the
single-pointer `__fern_rc_inc` / `dec` for string-typed values.

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
- **Two-slot sweep accounting** — the string occupies two operand
  slots; the dec sweep, move analysis, and `seen`-dedup must treat the
  pair atomically (the `WidthString` marker already exists for this on
  the drop side). This is the fiddliest part and why it's gated on the
  two-word flip (which forces every string slot to be a pair uniformly).
- **Migration conflict** — starting before the SSO flip means redoing
  the work; coordinate so the rc header lands IN the flip PR.

## What this doc IS / IS NOT

- IS: the sequencing + design for string reclamation, gated on SSO.
- IS NOT: a claim it can ship before the two-word ABI flip, nor a
  scope estimate for any single slice (each has its own, ~8 slices
  above).

https://claude.ai/code/session_01Vrwb6rXeWdQ9jBLH34TSaQ
