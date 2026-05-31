# Map[K, V] specialization plan

Captures the proposed migration from the current **runtime-tag
dispatch** Map implementation to **compile-time-specialized**
monomorphic clones per (K, V) pair.

## Current state

`Map[K, V]` is implemented as a single set of runtime helpers
in `internal/prelude/prelude.fern` (`map_new_impl` /
`__map_set_impl` / `__map_get_impl` / `__map_lookup` /
`__map_hash` / `__map_delete_impl` / `__map_string_column` /
`__map_iter_impl` / etc.). All Map values — regardless of K
or V at the call site — funnel through the same prelude
bodies.

Discrimination between concrete K/V types happens at runtime
via two i32 tags stored in the Map's heap buffer header
alongside cap and len:

```
buf + 0  : cap
buf + 4  : len
buf + 8  : keyKind   (0 = i32-sized scalar; 1 = string)
buf + 12 : valKind   (0 = i32-sized scalar; 1 = pointer-shape)
```

The tags are injected by the IR lowering pass: every
`map_new(cap)` call gets two extra trailing args appended
(`OpConstI32 keyKind; OpConstI32 valKind`) using
`mapKeyKindTag(K)` / `mapValKindTag(V)` from
`internal/ir/ir.go`. Every hot-path helper then branches
on these tags inline:

- `__map_hash(k, keyKind)` — `if (keyKind == 0)` selects
  Wang's integer mix; the else-branch FNV-1a's the bytes of
  `k as string`.
- `__map_lookup(m, k)` — same branch for the per-bucket
  equality check (`entryK == k` vs `(entryK as string) == (k as
  string)`).
- `__map_delete_impl(m, k)` — ditto; the swap-with-last
  path re-runs `__map_hash` so the V-kind branch fires too.
- `__map_keys_impl` / `__map_values_impl` —
  `if (kind == 1)` routes through `__map_string_column`,
  which derefs cell-pointers via the `as string` cast (the
  cell-pointer boxing that lets V-of-string round-trip
  through fixed-width entry slots on flipped two-word arm64).

## The problem

Three concrete costs from runtime-tag dispatch in hot paths:

1. **Branch in every inner loop iteration.** `__map_lookup`'s
   linear probe loads each candidate's entry key and compares
   it; the comparison itself is an `if (keyKind == 0)` branch.
   For a string-keyed Map this is a strcmp; for an i32-keyed
   Map it's a register cmp. The branch predictor will steady-
   state correctly but the dispatch adds 3-4 ops to every
   probe step.
2. **Hash function is a runtime branch.** Computing the hash
   to find the starting bucket pays an `if (keyKind == 0)`
   per call — once on lookup, once on insert, twice on grow
   (rehash). For workloads dominated by Map ops (the edge-
   handler use case, where every request reads / writes a
   request-scoped Map), this is measurable in the noise.
3. **Code-size bloat.** Both branches are emitted into the
   same body, so every Map-using program pays the i32-keyed
   hash path AND the string-keyed hash path in its binary
   even when only one is used.

The codebase's tree-shaker (`internal/treeshake`) can't
eliminate dead branches inside a function body — it only
drops whole unreachable functions. The cost is paid in every
binary that touches `Map[K, V]`.

## The proposal

Compile-time specialization via the existing monomorpher
(`internal/monomorph`). The pipeline already monomorphizes
generic *user-defined* functions — e.g. `function id[T](x:
T): T { return x; }` lands as `id_i32` / `id_string` clones.
Map helpers would join that mechanism so each `(K, V)` pair
gets a dedicated set of bodies with the runtime branches
collapsed to compile-time constants.

Concrete shape:

```
// Before — single body, runtime-tag dispatch:
function __map_hash(k: i32, keyKind: i32): i32 {
    if (keyKind == 0) { /* Wang's mix on k */ }
    else { /* FNV-1a over (k as string) */ }
}

// After — two cloned bodies, each branch-free:
function __map_hash_i32_X(k: i32): i32 {
    /* Wang's mix on k */
}
function __map_hash_string_X(k: i32): i32 {
    /* FNV-1a over (k as string) */
}
```

The mangled-name scheme mirrors what the existing monomorpher
produces — `__map_hash_<K>` rather than the user-function
form `<name>_<K>_<V>` so the helper name encodes only the
dimension that affects it. `__map_lookup` and
`__map_delete_impl` need both K (for compare) and V (for
the entry-stride / column-deref; though stride is K-vs-V
independent in the current layout). `__map_keys_impl` /
`__map_values_impl` need only V. The mangling table:

| Helper                  | Specialized by |
|-------------------------|----------------|
| `__map_hash_<K>`        | K              |
| `__map_lookup_<K>`      | K              |
| `__map_set_<K>_<V>`     | K, V           |
| `__map_get_<K>_<V>`     | K, V           |
| `__map_get_or_<K>_<V>`  | K, V           |
| `__map_delete_<K>`      | K              |
| `__map_keys_<K>`        | K              |
| `__map_values_<V>`      | V              |
| `__map_iter_<K>_<V>`    | K, V           |

The runtime tags in the buffer header become **dead** once
every call site is specialized — the helpers no longer
consult them. Keeping the tag bytes is fine for ABI stability
(the buffer layout stays at 16 bytes; we just stop reading
keyKind / valKind) but a follow-up could reclaim the 8 bytes
for something more useful (gen-counter for invalidation-aware
iterators, etc.).

## Migration steps

Each step is independently testable + reviewable; the full
test suite stays green after every merge.

### Step 1 — make `__map_hash` specializable

`__map_hash` is the smallest helper and gets called from every
hot-path (lookup, insert, delete, grow). Two cloned bodies
land alongside the existing generic one; call sites pick the
right name based on the K of the call site's Map. The IR
already knows K at every `OpCallDirect "__map_hash"` site
(we stamp it via `mapKeyKindTag(K)`); rewriting that emit to
call the specialized name is a 1-line change.

> **Caveat (experimental, 2026-05-28).** A first cut — split
> `__map_hash` into `__map_hash_i32` / `__map_hash_wide` /
> `__map_hash_string` and keep the existing entrypoint as a
> tiny dispatcher (`if keyKind == 0 → call i32; …`) — was
> tested. The hope was the existing inline+fold pipeline
> (inlineMaxPasses=3) would propagate a compile-time-constant
> `keyKind` through the dispatcher and prune the dead arms.
> It doesn't. The IR's Fold pass doesn't propagate constants
> through inline-bound slot reads, so the dispatcher's
> branches stay live and all three specialized bodies inline
> verbatim: emitted asm size for a `Map[i32, i32]` program
> went from 5778 → 5836 lines and the wide-hash `load_i64`
> count climbed from 20 → 41. **Step 1 in isolation regresses
> code size.** The wins assumed here require either landing
> step 2 in the same change (so callers thread a compile-
> time-constant K straight to the specialized hash) or
> teaching Fold to track constant-slot bindings. Either way,
> "1-line change at the emit site" undersells the work —
> step 1 as a standalone PR isn't worth shipping.

Rollback if anything regresses: keep `__map_hash` for one
release, gate the rewrite behind a build-tag (`+build
mapspec`) so a CI matrix entry runs the generic path until
we're confident.

**Test plan**: existing Map tests cover both i32 and string
K. A new microbenchmark (`internal/e2e` is fine — just compare
exit codes / outputs) confirms behavioural equivalence and
no surprising codegen-size regression.

### Step 2 — specialize `__map_lookup` + `__map_set_impl`

The biggest hot-path wins live here. The bucket-equality
check (`if (keyKind == 0) entryK == k else (entryK as string)
== (k as string)`) is the per-probe-step branch step 1's
hash specialization didn't fix. The entry-stride math
(`entryStride = 2 * ptrW`) stays target-dependent but K/V-
independent, so the body shape doesn't change much beyond
inlining the equality.

`__map_set_impl` is the same pattern at the write seam:
allocate-or-update, then run the rehash-on-grow loop, both
of which now route to the specialized hash / lookup helpers.

### Step 3 — specialize `__map_keys_impl` + `__map_values_impl`

The current `__map_string_column` helper exists specifically
to keep the cell-pointer boxing for string K/V working
through the keys() / values() snapshot path. With V
specialized at compile time, the cell-pointer deref + push
loop becomes target-typed; the runtime `if (valKind == 1)`
branch in `__map_keys_impl` / `__map_values_impl` collapses
to either "memcpy the column directly" (scalar V) or "deref
each cell, push" (string V), with no per-iteration check.

Bonus: with K specialized, `__map_keys_impl_<K>` for scalar K
becomes a flat memcpy of the entries column into a fresh
i32[] — no per-element work at all. Today the loop steps the
shared entry stride; specialization unrolls that.

### Step 4 — drop the keyKind / valKind tags

Once nothing reads them, the four bytes at `buf + 8` and `buf
+ 12` are dead. Two options:

- **Reclaim**: shrink the header from 16 to 8 bytes; every
  helper's `entriesBase` computation drops by 8. Modest
  binary-size win.
- **Repurpose**: leave the slots as a forward-compat gen-
  counter for iterator-invalidation detection (cheap insert-
  during-iter check).

The repurpose option is more flexible and the cost is zero —
we keep the existing layout, the iterator helpers gain a
"snapshot the counter at iter creation; check on each next()"
invariant, and the existing code that allocated those bytes
doesn't need re-running.

### Step 5 — remove the runtime-tag dispatch entry points

Once specialization is the only path, the generic
`__map_hash` / `__map_lookup` / etc. become dead — drop them.
The IR lowering layer (`mapKeyKindTag` / `mapValKindTag` in
`internal/ir/ir.go`) keeps existing to drive the mangling
decisions in step 1's emit rewrite.

## Risks + open questions

- **Binary size**. Specialization trades runtime branches for
  cloned bodies. For a typical edge-handler binary using
  `Map[string, string]` plus `Map[i32, V]` somewhere, the
  i32-keyed clones would be additive dead code that the tree-
  shaker drops only if nothing reaches them. The dominant
  Maps in lang's stated use case are `Map[string, string]`
  (request headers, env vars, JSON object backing) so the
  worst case is two Map shapes per binary; binary-size impact
  should be small but worth measuring.
- **Mangling collisions**. `__map_hash_string` collides with
  any user-defined function named `__map_hash_string` (the
  prelude reserves the `__` prefix, but the checker doesn't
  enforce that — the user can shadow). Either bump the
  mangling format (`__map$hash$string`?) or have the
  checker reject `__`-prefixed user functions.
- **Iterator API**. The current `__map_iter_impl` is a single
  function; specialization gives two — one for (K=i32, V=*)
  and one for (K=string, V=*) — each producing a
  monomorphized iterator struct. The `MapIter.next()` ABI
  has to land at the same shape in both, which the existing
  pair-form return mechanism (`OpCallDirectPair`) already
  handles.
- **Tree-shaker integration**. Specialization SHOULD play
  nicely with `internal/treeshake` (each clone is a separate
  FuncDecl and gets pruned independently). Worth verifying
  the clones don't accidentally survive due to a transitive
  reachability bug.

## Why this isn't a single PR

Five steps, each touching a different prelude helper +
needing its own e2e test pass on all three backends (wasm /
arm64-linux / arm64-darwin / x86-64). The risk profile is
"every Map-using example must keep working" — that's roughly
half of `examples/wasm/*.fern` plus the HTTP handler tests.
The doc exists so the work can be picked up incrementally
without re-deriving the design each time.

## Alternative considered: bytecode-level specialization

Instead of cloning at the AST / prelude layer, the IR could
keep the generic bodies AND have the constant-propagation
pass (`internal/ir/constprop.go`) discover that `keyKind` /
`valKind` are constants at each call site and fold the
branches accordingly.

This was considered and rejected for two reasons:

1. constprop doesn't propagate across `OpCallDirect`
   boundaries. The keyKind arg is a constant at the call
   site (it's a literal `OpConstI32` we just emitted) but
   inside the callee it's a function parameter — constprop
   would need to be inter-procedural, which is a substantial
   compiler feature on its own.
2. The inliner could be a third option (inline `__map_hash`
   at the call site, then let constprop see the constant
   keyKind inside the inlined body), but `__map_hash` is too
   big for the current inliner's size budget (and inlining it
   into every call site bloats binaries faster than
   specialization does).

Specialization at the monomorpher layer is the path of least
resistance: the machinery already exists, the mangling
scheme is well-understood, and the trade-offs match what
similar languages (Go's lack of generic specialization
notwithstanding; Rust + Swift + Zig all do this) settled on.
