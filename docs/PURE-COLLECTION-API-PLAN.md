# Pure (immutable-looking) collection API — design plan

Date: 2026-06-07.
Status: planning. No compiler or `.fern` code changed by this doc.

## Purpose

`docs/IMMUTABILITY-MIGRATION-PLAN.md` froze the two *shared-value*
mutation paths (struct field assignment → E048, reference-capture
write-back → E049) so that reference cycles become unconstructible and
RC stays garbage-free with no cycle collector. It deliberately left the
**collection CoW paths in scope-out** (§1c): `arr.push(x)`, `arr[i] = v`,
`Map.set/delete` "return a (possibly fresh) handle … these stay."

Those surviving APIs are *mutable-looking*: you can write the bare
statement `m.set(k, v);` and discard the result. That single affordance
is the last incoherent corner of the data model, and it carries a real
defect. This doc proposes removing it — making **every** collection
operation immutable-looking (value-returning, never fire-and-forget) —
and specifies the four pieces that make that practical:

1. **Pure value-returning APIs + an unused-result error** (§3).
2. **A scoped, linear builder / transient** for bulk construction (§4).
3. **An opt-in "must be in-place" assertion**, reusing the existing
   `fip` mechanism (§5).
4. **CoW stays the default backing**; persistent (structural-sharing)
   structures are reserved for the cheap-snapshot cases (§6).

Every code claim below was verified against the file at the cited line
(checker / IR line numbers are as of this branch).

---

## 1. The defect we are closing — the bare-statement aliasing footgun

Collection mutators are lowered to flat calls whose result is the
(possibly fresh) receiver buffer. The CoW seam — `__map_cow_inplace`
(`internal/stdlib/core/map.fern:154`) and the nested-array CoW at
`internal/ir/ir.go:9885` — does the right thing at runtime:

- **rc ≤ 1 (uniquely held):** mutate in place, return the *same* handle.
- **rc > 1 (aliased):** deep-copy, return a *fresh* handle (rc = 1); the
  old handle is left for the normal dec-on-overwrite.

The reassignment form threads that result back:

```fern
m = m.set(k, v);   // correct under both branches
```

and the IR even recognises it: `isSelfMapMutation` (`internal/ir/ir.go`,
~`14211`) emits a *conditional* dec of the old handle only when CoW
actually copied. But the **bare-statement form discards the result**
(`*ast.ExprStmt` → `OpDrop`, `internal/ir/ir.go:7671`):

```fern
m.set(k, v);       // result dropped
```

This is correct *only* while `m` is unique. Introduce any live alias and
the edit is silently lost:

```fern
function bump(m: Map[i32, i32]): void {
    m.set(42, 999);          // rc > 1 here → CoW copies, copy is dropped
}

function main(): i32 {
    var m = map_new(4);
    m = m.set(1, 10);
    var snapshot = m;        // alias: rc(m) becomes 2
    bump(m);                 // mutation happens on a copy that is discarded
    return m.get_or(42, -1); // -1 — the edit vanished
}
```

There is **no checker analysis** that flags this — the checker only
gates E048/E049 (`internal/checker/checker.go:6882-6905`); `isSelfMapMutation`
is a *syntactic* check, not an aliasing analysis. The interpreter plan
already records the same hazard as "loses bare-statement mutation"
(`docs/INTERP-MAP-COW-PLAN.md:59`), but the compiled backends share it.

**The fix is to delete the affordance, not to analyse it.** If a
collection operation can never be a discarded statement, the only
correct form is the reassignment (`m = m.set(...)`), and the footgun is
unconstructible — the same "make the bad state unrepresentable" move
that E048/E049 made for struct fields and captures. This also closes the
**coherence seam**: today structs say "rebuild with `{...old, f: v}`"
while collections say "just call `.set()`". Afterwards there is one rule
for all data — *values are immutable; locals are rebindable* — which is
the exact sentence the cycle-free-RC axiom wants.

---

## 2. What stays the same

This is a **surface** change. The runtime is already CoW + Perceus, so
none of the following move:

- The CoW seam (`__map_cow_inplace`, nested-array CoW) and the
  unique-in-place fast path.
- `own` / borrowed parameters and the affine checker
  (`checkOwnedParams`, E050/E051).
- `RcFreeEnabled` / `RcReuseEnabled` reclamation + reuse
  (`docs/OWNERSHIP-INFERENCE-PLAN.md`).
- The `__fern_rc_is_unique` runtime gate that keeps every in-place reuse
  sound regardless of static precision.

We are changing **what you are allowed to write**, and adding a builder
and an assertion. The bytes the in-place path emits are unchanged.

---

## 3. Pure value-returning APIs + the unused-result error

### 3a. Rename for intent

Every collection op already returns the collection (verified in
`internal/checker/checker.go:1350-1404`: `set`→`Map`, `clear`→`Map`,
`delete`→`(Map, bool)`, `push`/indexed `set`→`T[]`). We rename so the
name *reads as a value producer*, matching the struct-update idiom and
the Roc/Clojure precedent:

| today (mutable-looking) | proposed (value-returning) | precedent |
|---|---|---|
| `m.set(k, v)`     | `m.insert(k, v)` / `m.with(k, v)` | Roc `Dict.insert` |
| `m.delete(k)`     | `m.without(k)`                    | Clojure `dissoc` |
| `m.clear()`       | `m.cleared()`                     | — |
| `arr.push(x)`     | `arr.append(x)` / `arr.pushed(x)` | — |
| `arr[i] = v`      | `arr.with(i, v)`                  | — |

`delete`'s `(Map, bool)` shape stays (the bool reports presence); the
rename is `without`. Old names can live one release as deprecation
aliases that emit a warning, then are removed.

Note `arr[i] = v` is included: indexed element assignment is the one CoW
*statement* form that survives today. It becomes `arr = arr.with(i, v)`,
so subscript-assignment is no longer a statement at all — subscripts are
read-only, exactly like struct fields after E048.

**Done (E056).** This is now enforced: the checker rejects an `*ast.Assign`
with an `*ast.Index` target (`arr[i] = v`, and compound `arr[i] += v`) as
**E056**, the subscript counterpart of E048. All ~83 sites are migrated to
`arr = arr.with(i, v)`. The genuine in-place mutators (`fip` insertion
sorts) stay allocation-free because **E053 accepts `.with` on an `own`
receiver** (the method-call form of the in-place write). (The E056 number
here supersedes the speculative "CoW hint" use sketched in §3/§5 below,
which is dropped.)

### 3b. The enforcement: E055 "unused result of a pure operation"

A new checker rule rejects an `*ast.ExprStmt` whose expression is a call
to a value-returning collection method (the `__method_Map_*` /
`__method_Array_*` lowerings, and any user `pub function (x: T) …`
returning non-`void`) when the result is discarded:

```
E055: result of `Map.insert` is unused; assign it back
      (`m = m.insert(k, v)`) — collection operations return a new value,
      they do not mutate in place.
```

Implementation site: the `*ast.ExprStmt` arm of the checker
(currently lowered straight to `OpDrop` at `internal/ir/ir.go:7671`).
The checker already has the method-name → signature map
(`c.info.FuncSigs`, populated at `checker.go:1350`), so the result type
is known at the statement; gate on "callee is a registered collection
mutator (or a non-`void`-returning user method on a collection/struct
type) **and** the statement discards it."

Scope guardrails (avoid false positives):
- Only fires for **value-returning** calls. `void` methods (logging,
  `Writer.write` style sinks) are unaffected.
- A genuine "compute and discard" can opt out with `_ = expr;`
  (assign to the hole), keeping the escape valve explicit.
- Does **not** fire for non-collection pure functions in general at
  first — start narrow (collection mutators + struct-update-shaped
  methods), widen later if the signal is good. Narrow-first mirrors how
  E048/E049 shipped.

This single rule is what converts the footgun from "silent wrong
answer" to "compile error at the exact line."

### 3c. Construction style shifts to expressions

With `push`-in-loop gone as a statement, idiomatic construction leans on
pipelines and the existing iteration combinators (`std/array.fern`
already has `map`/`filter`/fold-shaped helpers):

```fern
// today
var out: i32[] = [];
var i = 0;
while (i < n) { out.push(arr[i].abs()); i = i + 1; }

// after — expression form
var out = arr.map(|x| x.abs());

// after — pipeline form for maps
var m = Map.empty()
  |> Map.insert("a", 1)
  |> Map.insert("b", 2);
```

For the irregular loops that don't fit `map`/`filter`, §4 is the answer.

---

## 4. The builder / transient — bulk construction without O(n²)

Forcing the value-returning form makes a naive append-in-loop O(n²) on
aliased data and allocation-heavy even when unique. The standard
resolution (Clojure transients, Roc's opportunistic reuse) is a
**scoped, linear builder** whose mutability provably cannot be observed
or aliased, so it does not reintroduce the cycle vector or the footgun.

### 4a. Shape

```fern
var out: i32[] = Array.build(|b| {
    for x in arr {
        b.append(x.abs());   // b is a *builder*, mutated in place
    }
});                          // freezes into an immutable i32[]
```

```fern
var m: Map[string, i32] = Map.build(|b| {
    for (k, v) in pairs {
        b.insert(k, v);
    }
});
```

### 4b. Why it is sound

The builder `b` is **linear and non-escaping**, enforced by machinery
Fern already has:

- `b` is an `own`-style binding scoped to the closure; the affine
  checker (`checkOwnedParams`, E050/E051) already rejects use-after-move
  and consumption-in-loop, and is the basis for forbidding `b` from
  being returned, stored in a field, or captured.
- Because `b` never aliases, its backing buffer is always rc = 1, so
  `b.append` is *always* the in-place fast path — no CoW check, no copy.
  This is just the unique-path we already emit, with the alias case
  proven impossible instead of checked at runtime.
- `build` returns the frozen value; the builder handle is consumed and
  cannot be touched afterward. No shared mutable box ever exists, so no
  cycle and no lost-write are representable.

On the builder, `append`/`insert` **are** statements (their whole job is
the side effect on `b`), so E055 does not apply inside `build`. That is
the one place imperative accumulation stays legal — and it is provably
local.

### 4c. New surface

- `Array.build(fn: (ArrayBuilder[T]) -> void): T[]`
- `Map.build(fn: (MapBuilder[K, V]) -> void): Map[K, V]`
- Builder methods: `append`, `with` (array); `insert`, `without` (map);
  `len`. Builders expose **no read-back of pointer-shaped contents**
  beyond `len` in v1, to keep the linearity story airtight.

The builder is a thin wrapper over the existing CoW buffers — no new
data structure, just a typestate that pins rc = 1 and forbids escape.

---

## 5. The "must be in-place" assertion — reuse `fip`

§3 removes the silent *correctness* footgun, but the immutable-looking
surface introduces a silent *performance* footgun: an op that you expect
to run in place will instead copy if the value turns out to be aliased.
We make that observable with the assertion mechanism **that already
exists** — `fip`.

`fip function` is checked at `internal/checker/checker.go:3920` and
already emits **E053** for the relevant violation:

> ``fip`` function %q may not write to a non-`own` heap value (triggers a
> copy-on-write)

So the tool is in the box: a function annotated `fip` is rejected at
compile time if any collection write inside it could trigger CoW (i.e.
if it writes through a borrowed rather than `own` value). That is
exactly "this code must run fully in place or fail to build."

This plan's contribution is **policy, not mechanism**:

1. Document `fip` as the supported way to pin hot paths once the
   value-returning surface lands (today it is framed around arithmetic /
   no-alloc kernels).
2. Extend `fipNonAllocMethods` (`checker.go:3918`, currently `{len}`) to
   whitelist the builder ops inside a `build` closure, so a `fip`
   function may construct via `Array.build`/`Map.build` without tripping
   the allocation check — the builder's writes are provably in-place.
3. Add a symmetric **diagnostic-only** CoW hint (non-fatal): when a
   non-`fip` function performs a collection write through a value the
   inference believes may be aliased, surface a note ("this `insert` may
   copy; mark the function `fip` or thread ownership to guarantee
   in-place"). Off by default; opt-in via a `-Whint-cow` flag so it
   never becomes noise. (NOTE: the E056 code was reassigned to the
   shipped subscript-read-only rule in §3a; this future hint, if built,
   needs a fresh code.)

The honest framing for the docs: this change trades a silent *wrong
answer* (today) for a silent *slowdown* (after), and `fip` is how you
convert that slowdown back into a compile error wherever it matters.
Without `fip`, performance is opportunistic; with it, it is guaranteed.

---

## 6. Backing: CoW default, persistent reserved

Two implementations can sit behind an immutable-looking surface:

- **CoW + uniqueness (Perceus / FBIP)** — what Fern has. Pure surface,
  in-place speed when unique, O(n) copy when shared, O(n) snapshot.
- **Persistent (HAMT / RRB-tree)** — structural sharing: O(log n)
  "modification" and O(1) snapshot, at a constant-factor cost and more
  allocation/indirection.

**Decision: keep CoW as the default.** The stated targets — small
fast-startup CLIs and short-lived, largely single-threaded edge handlers
— are exactly the workload where CoW wins: one owner, tight loops,
in-place updates, no GC, no persistent-tree constant factor. Snapshots
are rare in that workload, which is CoW's weak case.

Because the surface is now pure, the backing becomes a **per-type
implementation detail we can change without touching user code.** That
unlocks, as opt-in future work, persistent structures *only where cheap
snapshots pay off*:

- `PersistentMap[K, V]` / `PersistentVec[T]` for undo stacks,
  versioned/CoW-heavy state, speculative computation, or genuinely
  shared read-mostly tables.
- Same method names (`insert`/`without`/`with`/`append`) so swapping the
  type is the only edit.

This is the real long-game unlock: pure surface + race-free immutable
values also make **structured parallelism** sound (immutable values are
inherently sendable), which is the natural fit for fanning out edge /
HTTP work — but that is downstream of this plan, not in it.

---

## 7. Sequencing

Mirrors the IMMUTABILITY-MIGRATION-PLAN staging (build the replacement
before flipping the gate):

1. **Builder first.** Add `Array.build` / `Map.build` + `ArrayBuilder` /
   `MapBuilder` and the linearity enforcement. Tests: e2e construction
   parity (builder vs push-loop produce byte-identical results across
   x86-64 / wasm; arm64 on CI), and rejection fixtures for builder
   escape (return / store / capture → E050/E051).
2. **Rename with deprecation aliases.** Introduce `insert`/`without`/
   `with`/`append`/`cleared`; keep `set`/`push`/`delete`/`clear` as
   warn-on-use aliases. Migrate `internal/stdlib/` and `examples/` to
   the new names + the builder where loops are involved.
3. **Flip E055** (unused pure result). Convert the existing bare-statement
   fixtures (`conformance/cases/map_i32`,
   `map_string_key`, and the array equivalents) from
   *acceptance* fixtures into *reassignment* form, and add a
   **rejection** fixture for the discarded form — including the
   aliased-`bump`-through-fn case from §1, which is the regression this
   whole plan exists to prevent.
4. **Document `fip` policy** + extend the `fip` allow-set for builders
   (done for `.with` on `own`); land the opt-in CoW hint behind
   `-Whint-cow` (under a fresh code — E056 is now the subscript rule).
5. **Remove the deprecation aliases** and the indexed-assignment
   statement path (`arr[i] = v` → parse error pointing at `.with`).
6. **Self-host parity.** As with E048/E049, the bootstrap compiler lags
   until it has error-reporting infrastructure
   (`IMMUTABILITY-MIGRATION-PLAN.md` §4 "Remaining"); track E055 there as
   the same deferred item, not a blocker for the reference compiler.

Each step is independently shippable and leaves the language working.

## 8. Test plan (engineering-bar: every feature ships with tests)

- **Parser:** `arr[i] = v` rejection + `.with` suggestion; `build`
  closure parses.
- **Checker:** E055 fires on discarded collection result and is silent
  on `_ = expr;`, on `void` methods, and inside `build` bodies; builder
  escape → E050/E051; `fip` + builder accepted (E053 *not* raised).
- **e2e (x86-64 + wasm locally; arm64 on CI):** builder vs loop parity;
  the §1 aliased-bump regression now rejected; pipeline/`map` forms
  produce identical output to the old push-loops; `fip` hot-path
  function compiles and runs in place.
- **Self-host:** struct-update-style construction already covered
  (`self_host_functional_update_test.go`); add builder construction once
  step 1 lands on the bootstrap path.

## 9. Open questions

1. **Naming:** `with`/`without` (concise, Clojure-ish) vs
   `insert`/`remove`/`append` (explicit, Roc-ish). Leaning
   `insert`/`without`/`append` for maps and `with`/`append` for arrays;
   to be settled before step 2 since the aliases bake it in.
2. **E055 breadth:** collection-only at first vs all non-`void` pure
   calls. Start narrow; the wider rule risks false positives on
   side-effecting methods that also return a value.
3. **Builder read-back:** v1 forbids reading pointer-shaped contents to
   keep linearity airtight; revisit if real code needs read-during-build.
4. **Persistent types:** ship `PersistentMap`/`PersistentVec` in this
   line of work, or defer until a concrete snapshot-heavy use case
   (likely defer — CoW covers the stated targets).
5. **Parallelism:** the race-free-by-immutability property is a major
   downstream unlock (`docs/CONCURRENCY-RESEARCH.md`); out of scope here
   but this plan is its prerequisite.
