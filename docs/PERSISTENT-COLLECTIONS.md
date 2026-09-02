# Persistent collections — design, measurements, and what the compiler owes them

Issue #6794. Status: **shipped** — `std/ordmap`, `std/ordset`, `std/pmap`,
`std/pset`, `std/pvec` (2026-09-02), with the compiler fixes they needed and a
measured list of the ones they still want.

## The claim

Fern enforces immutable heap values (E048 / E056 / E049) and has a Perceus
reuse pass that turns a functional update into an in-place one when the value
is uniquely owned. That is the exact combination persistent data structures are
built for, and until now Fern had none: `core/map` is one mutable-looking open
addressed table, `T[]` copies its whole buffer on a shared write.

This library makes the claim concrete. Every collection here has ONE API, and
the compiler — not the library, not the user — decides what an update costs:

| the input is … | what `m = m.insert(k, v)` does | who decided |
|---|---|---|
| held by nothing else | writes the new path into the old nodes' boxes | the rc reuse pass, `is_unique` at runtime |
| still held by a snapshot | allocates the O(log n) path, shares the rest | the same code, `is_unique` false |

There is no transient / builder type, no `withMutations`, no `own` on any
receiver, and no thread-token. Clojure and Scala pay structural sharing on
every update and need a separate mutable mode to build fast; Rust's `im`
gets the unique path from `Rc::make_mut` but its methods take `&mut self` and
read as mutation; Lean and Koka have the runtime but no library. Fern is the
one language where the pure API *is* the fast API.

## What shipped

| module | structure | why this one |
|---|---|---|
| `std/ordmap` / `std/ordset` | weight-balanced tree, delta 3 / ratio 2 (Adams; Hirai & Yamamoto 2011), size in every node | the shape behind Haskell `Data.Map`: join-based `union` / `intersection` / `difference` in O(m log(n/m + 1)) (Blelloch et al., "Just Join"), rank access from the cached sizes, and every rebalance case is a same-size rebuild the reuse pass pairs |
| `std/pmap` / `std/pset` | 32-way HAMT (Bagwell 2001): `Leaf(hash, k, v)`, `Branch(bitmap, size, children)`, `Coll(hash, leaves)` | the shape behind Clojure / Scala / MoonBit; full hash cached in every leaf (Scala's `originalHashes` lesson — never re-hash on split or collision), murmur3 finaliser over the user hash (Clojure's 1.5 → 1.6 lesson), canonical collapse on remove (the `// TODO: collapse` Clojure never did), size cached per branch so `len()` is O(1) |
| `std/pvec` | 32-way bit-partitioned trie + tail (Clojure / Lean `PersistentArray`) | amortised O(1) append / pop through the tail, O(log32 n) `get` / `with`; the small case is tail-only, so a vector under 32 elements is one array |

Not shipped, deliberately: RRB (relaxed) concatenation — every reference
system that shipped it carried correctness bugs for years (core.rrb-vector,
scala-rrb-vector, im's early size tables), and Scala 2.13 rejected relaxation
for its default vector to protect indexed access. `pvec.concat` is O(len(other))
and says so. CHAMP's two-bitmap layout was also passed over: it wins on the JVM
by removing null-marker branches from a single `Object[]`; with Fern's typed
enum boxes a `Leaf` is already a distinct box, and a second bitmap would cost
more indirection than it saves.

Research inputs: the workflow fan-out over Clojure, Scala 2.13, Rust `im` /
`imbl`, the Perceus family (Koka / Lean 4 / Roc) and MoonBit's `@immut`
(five of ten research legs completed before the session's agent quota ran
out; Swift, Haskell / OCaml, Immer, the "others" sweep and the papers leg did
not run). The ideas taken are named above; the ones rejected are in the
"not shipped" paragraph.

## Measurements

x86-64, 4-core container, `bin/fern -target x86-64-linux`, wall clock of one
run, bytes = `__heap_bump_bytes()` (fresh bytes the freelist could not
recycle — see `docs/LOCAL-DEV-LOOP.md`). Programs in the scratch bench set
described at the end of this section.

**Unique path** — 200,000 inserts of distinct i32 keys, then 200,000 lookups:

| | time | fresh bytes |
|---|---|---|
| `core/map` (mutable-looking, open addressing) | 0.063 s | 25.2 MB |
| `std/pmap` | 0.34 s | 11.9 MB |
| `std/ordmap` | 0.29 s | 15.4 MB |

**Sparse sharing** — the same 200,000 inserts, keeping a snapshot every
1,000th version alive (200 snapshots):

| | time | fresh bytes |
|---|---|---|
| `std/pmap` | 0.40 s | 73.8 MB |
| `std/ordmap` | 0.34 s | 86.4 MB |

**Dense sharing** — a 200,000-entry value with a snapshot kept after EVERY one
of 2,000 further updates (an undo history, a versioned environment):

| | time | fresh bytes |
|---|---|---|
| `T[]` with `.with` | 3.12 s | 3.67 GB |
| `std/pvec` with `.with` | 0.013 s | 4.2 MB |
| `core/map` with `.insert` | killed (OOM) after 52 s | — |
| `std/pmap` with `.insert` | 0.27 s | 14.8 MB |
| `std/ordmap` with `.insert` | 0.26 s | 16.9 MB |

**Random writes, unique** — 200,000 `.with` over 200,000 elements:

| | time | fresh bytes |
|---|---|---|
| `T[]` | 0.006 s | 3.7 MB |
| `std/pvec` | 0.16 s | 1.6 MB |

Reading: the mutable structures win by 5x (maps) to 28x (vector) when nothing
is ever shared, and lose by 250x in time and 900x in memory — or do not finish
— the moment versions are kept. Both halves are the design working: the
persistent structures pay O(log n) node rebuilds per update, and the compiler
turns those rebuilds into in-place writes exactly when it can prove them safe.

**The in-place path, measured directly.** Re-inserting every key of a
2,000-entry map into a uniquely held map (`m = m.insert(k, v)` in a loop),
fresh bytes over the whole loop: `std/ordmap` 48 B, `std/pmap` 21 KB (the
forced path-array copies, recycled by the freelist), `std/pvec` 1.2 KB over
5,000 `.with`. With a snapshot held first, the same loops allocate exactly
the copied nodes and nothing else, and `-sanitize` reports zero leaked blocks
for build / re-insert / remove / lookup / fold on every module.

## What the compiler needed (fixed in this PR)

Writing the library against the native compiler found seven bugs, all fixed
at their source with tests:

1. **Monomorphiser: an array of a generic enum inside its own variant**
   (`Br(H[T][])`) failed the re-check with "expected H[i32][], got H__i32[]".
   The clone pass substituted `Call.TypeArgs` / `ArrayLit.ElemType` but the
   mangling pass never rewrote those slots to the enum clone.
   `internal/monomorph/monomorph.go` `rewriteBlockTypes`; pinned by
   `TestRunRewritesSubstitutedCallTypeArgsAgainstEnumClone`.
2. **Checker: a bare payloadless variant of a generic enum** (`Leaf`) in a
   payload or field position of the instantiation was E036 / E043 — it
   types as the argless enum, which `unifyType` refused against `Tree[K, V]`.
   It now unifies binding nothing (the same "not yet resolved" rule the
   under-inferred variant call already had). Pinned by
   `TestBareNullaryVariantUnifiesWithGenericEnumInstantiation`.
3. **Checker: a bare variant call in a call-argument position** could not be
   disambiguated once the enum had several clones ("variant Leaf is declared
   in multiple enums"). The expected clone is now seeded for call arguments
   the way it already was for var / return / field destinations.
4. **RC soundness: `.with` on an array match-bound from a borrowed enum
   mutated the box in place.** A non-consuming match arm binds its payload
   with no retain, so the array sat at the box's own rc==1 and the cow write
   rewrote the payload of a node a snapshot still held; the old box's drop
   then released the stored element (use-after-free under `-sanitize`). The
   array-write guard (`computeArraySetIncs`) now treats bindings of a
   non-consuming match as borrows, exactly like borrowed parameters.
   `internal/ir/rc_analysis.go`; pinned by
   `TestArraySetOnBorrowedMatchBindingForcesCopy` and the
   `with_on_match_binding_of_borrowed_enum` rc-correctness corpus case.
5. **Checker: a cast retargeted a pinned generic call** —
   `v.get_or(i, 0) as i64` with `v: PVec[i32]` re-instantiated `get_or` at
   i64 and monomorph rejected its own clone. The destination-driven restamp
   (meant for `id(7) as i64`) read a T bound to plain `i32` as unsettled,
   because the default width is recorded as zero; it now fires only when
   every T-bearing argument is literal-shaped. Pre-existing on `main`; pinned
   by `TestCastDoesNotRetargetPinnedGenericCall`.
6. **RC leak: an `own` array accumulator threaded through recursion lost a
   buffer per call.** `rhsTainted` tainted `acc = into(l, acc)` whenever
   another argument was borrow-tainted (a binding of a borrowed enum), so
   `acc` lost `freeEligible`, `return acc` took the borrow-style transfer
   inc with no exit dec, and the next append after the call boundary copied
   and stranded the source. The ordered map only escaped because its
   single-payload enum is owned-by-default. An `own` parameter now seeds
   the fresh set in `findReturnsFreshBox` and stays fresh through owned
   rebinds. Pinned by `own_accumulator_fresh_return_test.go` and the
   `rc_own_accumulator_recursion` e2e census (x86-64 and wasm).
7. **RC leak: a fresh call result stored into a variant payload through a
   borrowed parameter was stranded at rc 2** — `inferParamCountedRetain`
   had no credit for a variant-constructor payload store, so the caller
   never released the temp the constructor had retained. This is what
   leaked every string key on insert. `variantCtorCountedIn` now credits
   the store under exactly `emitEnumNew`'s inc gate (payload-carrying
   variant, rc-eligible enum, no consuming-match-reuse site), and a
   variant construction counts as a fresh box in `returnsOwnBox`. Pinned
   by `variant_payload_counted_retain_test.go` and the
   `rc_variant_payload_arg_temp` e2e census. String-keyed inserts into
   both maps now leak zero blocks.

The self-hosted compiler needed its own fixes: its monomorphiser did not
recover a generic instantiation from a clone-typed match binding, an indexed
element, or a generic-struct field read; could not pin a bare nullary variant
in a payload position; and instantiated the free generics a generic-struct
method reaches with the struct's own type variables (`__om_insert__K__V`),
so every chain below `insert` dangled. Its rc lowering also returned a
match-bound array payload without a transfer retain (a use-after-free in the
vector's leaf descent under FERN_SANITIZE=1). Those live in
`examples/self_host/parser.fern` / `irlower.fern`, with rows in
`internal/e2eselfhost/self_host_generic_ctor_ir_test.go` (x86-64 and wasm
legs), `self_host_stdlib_modules_ir_test.go` (all five modules through the
self-host loader), and `self_host_arr_return_transfer_ir_test.go`. The
self-host does not yet reclaim these structures' nodes (the RECLAIM half of
goal 2), so its unique path allocates where native reuses; the answers agree.

## What the compiler still owes them (measured, tracked)

Each of these is a native rc precision gap the library works around by
choosing a shape; none affects correctness, and the sanitizer census is the
gate for closing them.

- **Array-carrying and string-carrying enums are excluded from
  owned-by-default** (`isOwnedByDefaultType`, `typeIsStringArrayFree`), so a
  HAMT or vector node is always a borrow inside the update and its child
  array is copied on every level even when the whole trie is unique. That is
  the 28x on `pvec.with` and the 21 KB on `pmap`: the boxes are reused, the
  32-slot arrays are not. Widening the gate (with the array-payload deep drop
  it needs) turns the unique path of both into the zero-copy path the ordered
  map already has.
- **A closure argument allocated per loop iteration is not reclaimed**
  (`update(k, f)` in a loop: one env box per call), and the split / join /
  glue paths of `union`, `filter` and `update` still strand some nodes
  (199 / 502 / 1,001 blocks in the census on 1,000-entry maps).
- **A method-call temp used as a receiver leaks its box**
  (`a.union(b).len()`), the existing "method results that may alias the
  receiver" fallback.

## Shapes the library uses on purpose

Read these before extending a module; each is there because a probe showed
the alternative allocating or leaking.

- **No methods on the node enums**; every operation is a free function and
  the public surface lives on the wrapper struct — the self-host does not
  monomorphise a generic enum that has receiver methods.
- **Bound methods, never operators**, on type-parameter values (`k.cmp(nk)`,
  `k.eq(other)`, `k.hash()`): `==` resolves through a `Type.eq` lookup the
  defining module cannot see (#6846).
- **Recursive results bound to locals** before being passed on — the shape
  the counted-retain analysis credits most precisely.
- **Bitmap arithmetic in i32, logical shifts through `u32`**, popcount via
  `count_ones()` — `u32 +` / `<<` are not truncated on every self-host
  backend.
- **The set types hold the tree root directly**, not a map inside a struct:
  the extra box cost one allocation per operation and leaked one per
  discarded temp.
- **Cross-module variants cannot be named** (`ordmap.Tip` is not a thing), so
  `std/ordset` / `std/pset` reach the node types through the `pub __om_*` /
  `pub __hm_*` functions their map exports.

## Bench programs

The measurement programs are nine ~20-line files (`b_coremap`, `b_pmap`,
`b_pmap_shared`, `b_ordmap`, `b_ordmap_shared`, `b_array_with`,
`b_array_with_shared`, `b_pvec_with`, `b_pvec_with_shared`, plus the five
`*_dense` variants) following the pattern in `examples/bench/README.md`: build
200,000 entries, then either update in place, update with a snapshot every
1,000th version retained, or update with every version retained, printing
`len`, a checksum, and `__heap_bump_bytes()`. They are not in
`examples/bench` because the perf lane compares retired-instruction counts
against a checked-in baseline and these runs are sized by wall clock, not by
instruction budget; re-create them from the tables above when re-measuring.
