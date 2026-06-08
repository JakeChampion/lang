# Cell[T] — a sanctioned mutable cell for the immutable-data world

Date: 2026-06-07 (updated 2026-06-08).
Status: implemented for scalar **and `string`** element types. The Go
reference compiler does `cell_new` / `get` / `set` + cycle-free E057 + full
rc reclamation; the self-host backends handle `Cell[string]` for free
(single-pointer strings → the slot is one word, same as `Cell[i32]`),
verified across every backend. §3a (migrate the `lam_ctr`/`lamdefs`
array-cells to `Cell`, then `arr[i] = v` → E056) is the remaining
downstream step — now unblocked on both compilers.

## Purpose

Fern is converging on **immutable data structures only**: struct fields
are frozen after construction (E048), reference-typed closure captures
can't be written back (E049), and the collection mutators are
value-returning with their discard rejected (E055). The single invariant
all of that protects is **"no reference cycles,"** which is what lets
Perceus reference counting stay garbage-free with *no cycle collector*.

The last mutation *statement* still standing is `arr[i] = v`
(`docs/PURE-COLLECTION-API-PLAN.md` §3a). Removing it to make subscripts
read-only ran into a load-bearing idiom: a **1-element array used as a
mutable cell**. The self-host compiler itself relies on it —
`internal`/`examples/self_host/wasm.fern`'s `Ctx` has

```fern
lam_ctr: i32[],     // lam_ctr[0] is the next lambda's table index
lamdefs: string[],  // lamdefs[0] accumulates the emitted lambda bodies
```

mutated in place via `cx.lam_ctr[0] = idx + 1`. This works *because*
element assignment mutates through the array pointer without reassigning
the (immutable) field. There is **no value-returning replacement**:
`cx.lam_ctr = cx.lam_ctr.with(0, …)` is E048.

So array-element assignment is currently doubling as the language's only
**shared mutable state** primitive. Before `arr[i] = v` can go, that need
must have a real home. This doc specifies that home: `Cell[T]`.

## 1. Does a mutable cell even belong in an immutable language?

Only if it cannot reconstruct a reference cycle. The dividing line is
already drawn by E049, which **permits** writing back a captured
**scalar** ("the stateful counter closure stays legal") and forbids
writing back a reference — precisely because a scalar holds no pointer
and so can't be part of a cycle.

`Cell[T]` adopts the same line:

- A **general `Ref[T]`/`Cell[T]` over a reference type is rejected** — it
  reopens cycles (`a.set(b); b.set(a)`) and would undo the entire reason
  the language went immutable. We do **not** add it.
- A cell over a **cycle-free** `T` is sound: it's the same safe mutation
  E049 already blesses, given a name instead of being smuggled through a
  closure env or a 1-element array.

Aliasing a `Cell` (sharing it, mutating through it from several places) is
**fine for RC** even though it breaks value-semantics local reasoning:
sharing isn't cycles. The refcount tracks the aliases and frees the cell
at zero. `Cell` is therefore the *explicit, greppable* opt-out from value
semantics — value semantics stays the default; `Cell` is the marked
exception for the rare genuine mutable-state need (counters, accumulators,
single-pass IDs), bounded so it can never break the RC invariant.

## 2. The cycle-free restriction (the type rule)

`Cell[T]` is well-typed only when `T` is **cycle-free**: a value of `T`
transitively contains no heap reference that could point back at the cell.

**Scope — scalars + `string` (Go reference compiler):**

| `T` | allowed | why |
|---|---|---|
| `i32`, `i64`, `f64`, `bool`, `usize`, … (scalars) | ✅ **shipped** | hold no pointer; the slot needs no RC |
| `string` | ✅ **shipped (Go compiler)** | cycle-free (a buffer of bytes, references no other value); the owning slot now participates in the string rc arc (retain on new / get, release on overwrite / drop) |
| everything else (`struct`, `enum`, `T[]`, tuple, `Cell`, fn) | ❌ | can transitively hold a reference → cycle-capable |

The two idioms that block §3a are an `i32` counter (`lam_ctr`) and a
`string` accumulator (`lamdefs`); both element types are now supported on
the Go reference compiler. `string` is cycle-free (it references no other
Fern value), and the `Cell[string]` slot now participates in the
completed string rc arc (docs/RC-STRINGS-PLAN.md): `cell_new` retains an
aliased element, `get` retains the returned buffer, `set` releases the old
slot value and retains the new, and the cell's drop releases the slot
before freeing the box. The predicate can widen further to "transitively
cycle-free" (e.g. `i32[]`) once a use case needs it. A checker rule
**E057** rejects `Cell[T]` for any not-yet-allowed `T`.

`Cell` lowers as a one-element heap box (same layout as a 1-element array
literal: `[cap|rc|len|slot]`, 16-byte header, data pointer at `base+16`),
so Perceus RCs the **box** via the standard rc word at `data-8`. Because
the box header is array-shaped, the cell's **drop must go through the
array reclamation path** (`__fern_arr_dec` for scalars, `__fern_drop_arr_str`
/ `__fern_drop_arr_ptr` for `string`, computing `base = data - 16`) — *not*
the struct `__fern_box_free` path, whose `data - 8` base assumption
mis-frees the cell's header (this was a latent over-/mis-free for
`Cell[i32]`, fixed alongside the `string` work).

**Self-host backends handle `Cell[string]` for free.** The self-host
emitters lower `Cell` as alloc + load/store with no slot RC (their heap is
leak-everything). Crucially, the self-host string representation is a
**single pointer** to a `[len][bytes]` block (not the Go compiler's
two-word `(data, len)`), so a `Cell[string]` slot is one pointer-word —
*identical* to a `Cell[i32]` slot. The existing single-slot cell machinery
therefore compiles `Cell[string]` correctly on every self-host backend
(asm x86-64 / arm64, SSA x86-64 / wasm, direct wasm), including cells
stored in struct fields and mutated through function params (the
`lam_ctr`/`lamdefs` shape). Verified by `cell-string*` cases across the
self-host prog / wasm-run / SSA-emit tests and a `cell_string` differential
case (Go vs self-host, x86-64 / arm64 / wasm). The only remaining
self-host gap is `checker.fern` E057 parity (it doesn't yet reject a cyclic
`Cell[T]`); since E057 isn't in the differential code set, the compilers
don't diverge.

## 3. Surface

```fern
var c: Cell[i32] = cell_new(0);   // construct with an initial value
var n: i32 = c.get();             // read the slot
c.set(n + 1);                     // in-place write — a statement, returns void
```

- `cell_new(initial: T): Cell[T]` — a builtin constructor (parallels
  `map_new`), inferring `T` from the argument.
- `(c: Cell[T]) get(): T` — load the slot.
- `(c: Cell[T]) set(v: T): void` — store the slot **in place**.

`set` returns **void** on purpose: `Cell` is exactly where in-place
mutation is sanctioned, so `c.set(v);` is a normal statement and is **not**
subject to E055 (which only fires on discarded *value-returning* results).
This is the deliberate asymmetry with collections — collections are
value-returning (you thread the new value), a `Cell` is the one place you
mutate and move on.

## 4. Representation & backends

A `Cell[T]` is a **single-slot heap box** — effectively the 1-element
array without the length prefix. It reuses the existing alloc + element
load/store machinery:

- `cell_new(v)` → `alloc(1 slot)`; store `v` at offset 0; yield the
  pointer.
- `get` → load offset 0.
- `set` → store offset 0.

Slot width follows the element-width rules already used for arrays
(`WidthPtr` on native, 4/8 on wasm by `T`). Backends to touch: the Go
reference compiler (`internal/checker` + `internal/ir`) and every
self-host emitter (`asm.fern`, `asm_arm64.fern`, `ssa.fern` +
`ssa_x86`/`ssa_arm64`/`ssa_wasm`, `wasm.fern`) plus the self-host
`checker.fern` for E057 parity. The self-host heap is leak-everything
(RC intrinsics are no-ops), so `Cell` there is just alloc + load/store.

### RC (Go reference compiler only)

The cell box is a heap object, so it's retained/released like any other.
The **slot's** RC depends on `T`:

- **scalar `T`** — no RC on the slot; the box itself is RC'd. Trivial.
- **`string` `T`** (shipped on the Go compiler) — the cell *owns* a
  reference to the string. `cell_new` retains an alias-shaped element (a
  fresh concat / literal is moved in); `get` retains the returned buffer
  (the cell keeps its slot copy); `set` **releases the old** slot value
  and **retains the new**; the cell's drop releases the slot before
  freeing the box. This reuses the completed string rc helpers
  (`__fern_str_inc` / `__fern_str_dec` on two-word ABIs, `__fern_rc_inc` /
  `__fern_rc_dec` on native single-word) — the same retain/release dance
  the array element-store already performs, scoped to one slot. Because
  `string` is cycle-free, this can't leak via cycles.

(The cycle-free restriction is what keeps this RC story sound: a
reference-typed-but-cyclic `T` would need a cycle collector, which is
exactly what we're refusing to build.)

## 5. How this unblocks §3a

With `Cell[T]` in place, the array-as-cell idioms migrate to a real type:

```fern
// before                         // after
lam_ctr: i32[],                   lam_ctr: Cell[i32],
lamdefs: string[],                lamdefs: Cell[string],
cx.lam_ctr[0] = idx + 1;          cx.lam_ctr.set(idx + 1);
cx.lam_ctr[0]                     cx.lam_ctr.get()
```

Once the (small handful of) array-as-cell sites are on `Cell`, the
remaining `arr[i] = v` uses are all genuine *local array element*
mutations, which **do** have a value-returning replacement
(`arr = arr.with(i, v)`, shipped). Then §3a completes: `arr[i] = v`
becomes **E056** (subscript assignment is read-only, like struct fields),
with `arr.with` / `Cell.set` as the two sanctioned writes.

## 6. Phasing

1. **`Cell[T]` scalars** — ✅ **done** on Go + all self-host backends;
   E057 (cycle-free restriction); e2e get/set on every backend.
2. **`Cell[string]`** — ✅ **done on every backend.** Go reference
   compiler: E057 widened to allow `string`, retain/release wired through
   `cell_new` / `get` / `set` / drop (reusing the completed string rc arc),
   and the cell drop routed through the array reclamation path (also fixing
   the latent `Cell[i32]` mis-free) — `TestCellElemTypeE057` +
   `cell_i32_churn` / `cell_string_overwrite_churn` rc-corpus entries.
   Self-host: works for free (single-pointer strings) — `cell-string*`
   cases in the prog / wasm-run / SSA-emit suites + a `cell_string`
   differential case (Go vs self-host, x86-64 / arm64 / wasm), incl. cells
   in struct fields mutated through params. *Remaining:* `checker.fern`
   E057 parity (a cyclic `Cell[T]` isn't rejected self-host yet; not in the
   differential code set, so no divergence).
3. **Migrate the array-as-cell idioms** (`lam_ctr`, `lamdefs`, and the
   handful of `obj.arr[i] = v` mutable-cell sites) to `Cell`.
4. **Remove `arr[i] = v`** → E056, migrating the remaining *local* array
   element writes to `arr = arr.with(i, v)`. Finishes
   `docs/PURE-COLLECTION-API-PLAN.md` §3a.

The general `Ref[T]` is explicitly **out of scope, permanently**: it is
incompatible with the no-cycles invariant the immutable-data model exists
to guarantee.
