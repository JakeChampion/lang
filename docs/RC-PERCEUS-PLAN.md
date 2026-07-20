# RC + Perceus implementation plan

> **Open follow-ups tracked in GitHub:**
> [#2704](https://github.com/JakeChampion/lang/issues/2704) (safe-leak
> classes), [#2705](https://github.com/JakeChampion/lang/issues/2705)
> (`Drop` trait), [#4113](https://github.com/JakeChampion/lang/issues/4113)
> (wide-struct in-place reuse). The old coarse tracker #2857 is closed.
> This doc is a living progress log — verify the latest slice before picking up
> an item.

Implementation plan for refcounted heap values with compile-time
Perceus optimisation.

Date: 2026-05-20 (design); implementation tracked inline below.
Status: IMPLEMENTED. Phases 0–3 + the Phase-6 reclamation work have
shipped (RC with compile-time drop placement, the real freelist allocator,
and per-type reclamation for arrays / structs / enums / maps / tuples /
closures / strings, including nested/generic shapes and statement
temporaries). Native heap-string rc (item 5g) is now ALSO working — the
SSO native flip went green on both backends (2026-06-03), unblocking it.
The remaining open items are collected at the end of the Phase-6 section
("Next Phase-6 steps (open)") — chiefly the ENUM reuse-path payload free
(the struct analog 5f shipped sound; see its note). The per-phase prose
below is kept as the historical record. The
design "Open questions" at the very bottom were all resolved during
implementation — see the resolutions noted there.

## Why

Fern has value semantics: arrays, strings, structs, enums, closures
all look immutable to the programmer. Today that's implemented by
*always copying* on any operation that conceptually mutates (`arr.push`,
hypothetical `arr.set`, etc.). Two consequences:

1. **O(N²) push loops.** Building an array via `xs = xs.push(...)` in
   a tight loop allocates a fresh `len+1` buffer per push and copies
   the predecessor's contents. For N pushes the total allocator
   traffic is N + (N-1) + ... + 1 = O(N²) bytes. The self-host
   `parser.fern` would need ~7 GB and `asm.fern` ~60 GB just to
   compile itself; both blow past the bump allocator's 512 MiB cap.
   See PR #1011's "Known wall ahead" section for the exact figures.

2. **No future for in-place algorithms.** Sort, reverse, etc. on a
   large array always copy — the compiler can't tell when it would
   be safe to mutate, and the language gives the programmer no way
   to assert it.

The goal is to keep the immutable surface and gain O(1) amortised
mutation when the value happens to be uniquely held — i.e. the
runtime decides "in-place vs copy" per operation based on the
refcount at that moment. The same Roc / Koka / Lean4 approach: RC
at runtime, Perceus statically elides as many inc/dec pairs as it
can prove are dead.

## Mental model

From the programmer's perspective: arrays / strings / structs /
enums / closures are immutable values. Reassigning a variable or
binding a new one creates a new reference to the same underlying
buffer; both references see the same value.

Mutating ops (`arr.push(x)`, `arr.set(i, x)`, ...) return a
*conceptually new* value. Whether that's actually a fresh
allocation or an in-place update of the shared buffer is the
implementation's choice — invisible to the user.

The implementation tracks refcounts:

- Every heap-allocated value carries an `rc: i32` (or `u32`) header
  field. Initialised to `1` at allocation.
- Every operation that creates a new reference to that value
  increments rc. Every operation that drops the last visible
  reference decrements; when rc hits 0, the storage is freed.
- Mutating ops check rc:
  - `rc == 1` → no one else holds a reference, mutate in place.
  - `rc > 1` → copy first, decrement the source's rc, mutate the
    copy.

Perceus is the *static analysis* layered on top: the compiler
proves that some inc/dec pairs are statically cancelling (e.g.
"this value is incremented, used once, then dropped") and elides
both. Real Perceus also reuses dropped allocations as the storage
for adjacent allocs ("drop reuse").

## Non-goals (or: things deferred)

These intentionally don't ship with the first phase. They're
designed-around but not built yet.

- **Garbage cycles.** Cycles in RC graphs leak. Roc disallows them
  via the type system: there's no mutable field that could form a
  cycle. Fern now has this property *by enforcement*: the checker
  rejects struct-field assignment (E048,
  `docs/IMMUTABILITY-MIGRATION-PLAN.md` §4 / `CYCLE-COLLECTION-
  ANALYSIS.md`), so a post-construction back-pointer — the only way
  to close a cycle when values are built bottom-up — can't be
  written. (Mutable closure-capture write-back is the remaining
  vector; its checker rejection is the follow-up that completes the
  guarantee.) No cycle collector and no tracing fallback: cycles are
  unconstructible, not collected.
- **Thread safety.** Refcounts are non-atomic. Single-threaded.
  When concurrency lands, either atomic ops (slower) or thread-
  local heaps with explicit sharing.
- **Backwards heap compatibility.** Old heap layouts are abandoned;
  every backend's bump allocator is replaced (or supplemented) with
  one that knows about the new headers + the freelist.

## What gets a refcount

Every heap-allocated value:

| Kind | Today's header | After phase 1 |
|---|---|---|
| Array | `[len:4 \| pad-to-stride]` | `[rc:4, cap:4, len:4 \| pad]` |
| String (heap form) | `[len:4]` | `[rc:4, len:4]` |
| String (inline SSO) | tag bit in pointer | unchanged (still inline; rc = ∞) |
| Struct (heap-boxed) | `[shape_ptr:8 \| fields]` | `[rc:4, pad:4, shape_ptr:8 \| fields]` |
| Enum (heap form) | `[tag:8 \| payload]` | `[rc:4, pad:4, tag:8 \| payload]` |
| Closure box | `[fn_addr:8, captures]` | `[rc:4, pad:4, fn_addr:8, captures]` |
| Slice header | `[data:8, len:8]` | `[rc:4, pad:4, data:8, len:8]` |
| Map | runtime-internal | rc on the outer handle, rc on string keys/values |

Statically allocated constants (string literals in `.rodata`, the
empty-array sentinel `.LArr_Empty`, the empty-string sentinel,
shape pointers) keep their current layout and are detected by the
inc/dec helpers, which short-circuit on them. Implementation: rc
field set to a magic sentinel value like `i32::MIN` (treated as
"don't touch, never free"). The inc/dec helpers branch on the
sentinel and become no-ops.

## Core operations

Two runtime helpers do almost all the work. Both are deliberately
tiny so the codegen can inline them when wanted.

```
__fern_rc_inc(ptr):
    if ptr == NULL: return
    rc = *(ptr - rcOffset)
    if rc == SENTINEL_STATIC: return        ; static const, never touch
    *(ptr - rcOffset) = rc + 1

__fern_rc_dec(ptr, drop_handler):
    if ptr == NULL: return
    rc = *(ptr - rcOffset)
    if rc == SENTINEL_STATIC: return
    if rc == 1:
        drop_handler(ptr)                    ; type-specific cleanup
        free(ptr - headerBytes)              ; return to allocator
        return
    *(ptr - rcOffset) = rc - 1
```

`rcOffset` is the same for every type by convention (e.g. always
`8` from the data pointer — high enough for arrays, structs, and
strings, with a pad slot if needed).

The `drop_handler` knows how to recursively decrement any pointer-
typed fields the value contains. For a `string[]` array, the
drop handler walks the elements and decrements each string before
freeing the array's buffer. For a primitive `i32[]` array, the
drop handler is a no-op (just free). The codegen emits one
drop handler per concrete type, named like `__fern_drop_array_string`,
`__fern_drop_struct_Foo`, etc.

Specialised inc/dec variants (no nil check, no sentinel check,
when the codegen can prove them unnecessary) are emitted inline
to save the call overhead.

## Allocator changes

The bump allocator is replaced with a real allocator with a
freelist. Rough shape:

- Per-size-class freelists for small allocations (8 / 16 / 24 / 32
  / 48 / 64 / 96 / 128 / 256 / 512 / 1024 / 2048 bytes — Roc's
  layout, easy to tune later).
- Large allocations (> 2 KiB) go to a bump region; freed large
  allocations get unmapped (`munmap`) rather than recycled. Keeps
  the implementation simple and gives the kernel back peak-RSS.
- Allocations track their size class in their pointer alignment or
  in a header word. Free path looks up the class, pushes onto the
  freelist.

Phase-1 simplification: skip the freelist. `free()` is a no-op
(memory leaks back into the bump heap). Memory usage is worse than
even today's bump allocator (because rc=1 paths still allocate
new buffers in the worst case), but correctness lands without
allocator complexity. Freelist arrives in a follow-up phase.

## Where inc/dec go

The codegen inserts inc/dec at specific syntactic positions. They
fall into three categories:

### Reference-creation sites (insert inc)

The value is being given to a new owner. The old owner still
exists.

- **Variable binding from another variable.** `var y = x` or
  `y = x` → inc on `x`'s value.
- **Function-call argument.** `f(x)` → inc on `x` (the callee
  is the new owner; it'll dec when done).
- **Return value.** `return x` → inc on `x` (the caller is the
  new owner) IF `x` is a local that's about to be dropped (its
  slot decremented as part of function exit); otherwise no inc
  needed (caller-side rc transfer).
- **Storing into a struct/array field.** `s.f = x` → inc on `x`
  (the struct owns it now).
- **Closure capture.** When building a closure, each captured
  variable's value is inc'd (the closure owns it).
- **Array literal element.** `[a, b, c]` → inc on a, b, c.
- **Struct/enum/tuple literal field.** Same: inc on each.

### Drop sites (insert dec)

The owner is going away.

- **Variable goes out of scope.** Block-scoped: at the closing
  `}` of the block where the variable was declared. Function-
  scoped: at every return point.
- **Variable reassignment.** `x = newValue` → dec the old value
  first, then assign the new (after inc-ing the new if from an
  alias).
- **Struct goes out of scope.** Recursively dec each pointer field
  (via the type's drop handler).
- **Function exit.** All locals dec'd. Parameters dec'd (callee
  was the owner). Return value escapes — the caller takes
  ownership; either inc-then-dec-locals (always safe) or pass
  ownership transfer via the call ABI.
- **Match-arm bindings going out of scope.** Each arm's pattern
  bindings have the scope of the arm body.
- **Temporary results.** An expression like `f(g(x))` produces a
  temporary for `g(x)`'s result. After `f` consumes it (via inc
  for the arg, or by direct call-ABI ownership transfer), the
  temp's last reference is gone. The codegen tracks temporaries
  and dec's them at statement end.

### Mutate-or-copy sites (rc check)

The op needs unique ownership to mutate; checks rc and either
mutates or copies.

- `arr.push(x)` — see "Mutating operations" below.
- (Future) `arr.set(i, x)`, `arr.sort()`, `arr.reverse()` in place,
  `arr.swap_remove(i)`, etc.

## The example, walked through

```Fern
var nfuncs: FuncDecl[] = into.funcs;        // (1)
var fi: i32 = 0;
while (fi < from.funcs.len()) {
    nfuncs = nfuncs.push(FuncDecl{...});    // (2)
    fi = fi + 1;
}
return nfuncs;                               // (3)
```

After codegen:

1. `nfuncs = into.funcs` →
    - Read `into.funcs` field, gives a pointer with rc ≥ 1.
    - `inc(into.funcs)` (the field still owns it, and `nfuncs` now
      also owns it).
    - Bind to `nfuncs` slot.
    - Now `into.funcs.rc == 2`, `nfuncs` and `into` both point at
      it.

2. `nfuncs = nfuncs.push(...)` →
    - Call `__fern_arr_push(nfuncs, fd)`.
    - Inside: check rc. rc == 2 (still aliased by `into.funcs`).
    - Allocate fresh buffer with cap = 2*oldCap. Copy old data.
      Set rc = 1 on the new buffer. Append fd. Dec old buffer
      (rc 2 → 1; not zero, no free).
    - Return the new buffer.
    - The old `nfuncs` slot's value is being overwritten. Dec it
      (the new buffer; rc = 1 → 0... wait, no, rc was just set to
      1 by push). Hmm — actually the *assignment* dec's the
      slot's old value (the *previous* nfuncs); the new buffer's
      rc 1 is what stays.
    - First iteration: `nfuncs`'s new buffer has rc=1.
    - Subsequent iterations: `nfuncs.rc == 1`, push's rc check
      sees unique, mutates in place. O(1) per push.

3. `return nfuncs` →
    - Inc the return value (callee transfers to caller).
    - On function exit: dec everything in locals (`nfuncs` was
      already inc'd to transfer; the dec cancels it. Net zero).
    - `into` dec'd (rc 1 → 0; recursively dec its fields, free).
    - `into.funcs` dec'd as part of `into`'s drop. (rc was 1 since
      we already decremented when nfuncs swapped to its fresh
      copy.)

Net result: the parser loop is O(N) instead of O(N²), aliasing is
respected, no static analysis required. All correct by the
runtime check.

## Mutating operations

### `arr.push(x)`

```
__fern_arr_push(arr, x):
    rc = *(arr - 8 - rcOffset)
    if rc == 1 AND len < cap:
        // fast path: unique + spare capacity
        *(arr + len * stride) = x
        len += 1
        return arr
    // slow path: shared OR full
    newCap = max(4, 2 * cap)
    newBuf = alloc(headerBytes + newCap * stride)
    newBuf.rc = 1
    newBuf.cap = newCap
    newBuf.len = len + 1
    memcpy(newBuf.data, arr, len * stride)
    *(newBuf.data + len * stride) = x
    if elem is pointer-typed:
        // We've duplicated each pointer into the new array; inc
        // each so the per-element rc reflects two arrays
        // referencing them. Then dec the old array's outer rc.
        // (When the old array's drop happens, its pointer fields
        // get dec'd; everything balances.)
        for each elem in newBuf: inc(elem)
    dec(arr)
    return newBuf
```

The per-element inc when copying is the key correctness point.
Without it: copy-on-push would create N references to each
pointer-typed element but only one had been counted.

For primitive (non-pointer) element arrays, skip the per-element
inc loop. Generate two versions of `__fern_arr_push`: one for
pointer-typed elements, one for primitive — picked at the
call site based on the element type.

### Future: `arr.set(i, x)`, `arr.sort()`, etc.

Same shape. Check rc. If unique, mutate in place. Else copy then
mutate.

## User-facing API audit

The Roc-style "immutable surface, mutate underneath" only works
if the surface IS immutable everywhere a user reaches. Today's
Fern surface is mostly there but has rough edges where the syntax
or method name implies mutation, which would mislead readers
once Phase 2 lands. The cleanup is allowed to break API: callers
update at the same time we flip the implementation.

Categories below. Each entry shows current → target, with the
phase where the rename lands.

### Already immutable-looking (no change)

- `arr.push(v)` → returns the new array. Today this LOOKS
  immutable and the user idiomatically writes `arr = arr.push(v)`.
  Phase 2 makes the common rc==1 case O(1) without changing the
  signature. **No rename.**
- `arr.sorted_asc()` / `arr.sorted_desc()` / `arr.sorted_str_*` →
  returns a fresh sorted array. **No rename**, but see the
  inconsistency note below.
- `arr.reversed()` (i32[]) → returns a fresh reversed array.
  **No rename.**
- `arr.abs_each()` / `arr.cumsum()` / `arr.pairwise_diffs()` →
  all return new arrays. **No rename.**
- `s.repeat(n)` / `s.capitalize()` / `s.snake_case()` etc. — every
  string method is already pure-return. **No change.**

### Inconsistency to fix

- `arr.reverse()` (string[]) returns a string[] but the name reads
  as in-place. The i32[] variant is `reversed`, which is the
  right shape. **Action**: rename string[]'s `reverse` →
  `reversed` for consistency. Phase 1e (when strings get rc) is
  a natural moment because the API surface is already being
  touched.

### Mutating-looking syntax to keep (with new semantics)

- `arr[i] = v` is syntactically a mutation. Today it's lowered as
  an in-place store into the underlying buffer — which is the same
  bug Phase 2 fixes for `push` (corrupts aliases). Two paths:
  1. **Keep the syntax**, change the lowering: `arr[i] = v`
     desugars to `arr = arr.set(i, v)` where `set` is a method
     that checks rc and either mutates in place (rc==1) or copies
     (rc>1). The current writer-target gets reassigned. Phase 2.
  2. **Remove the syntax**, force callers to write
     `arr = arr.set(i, v)` explicitly. More surgery, more honest.
  Recommend (1) — preserves existing code, and the mental model
  ("array indexing is sugar for a copy-on-write set") matches Roc.
  Implementation note: this is the assignment side of the IR's
  `*ast.Index` target in `b.assign` (currently emits
  `__arr_idx_*` + a raw store).

### Mutating-looking methods to rename

The Map API is the loudest case — it's void-returning today
(real in-place mutation under a single-owner assumption), and
that mutation IS the source of subtle aliasing bugs. Rewriting
to return Map fixes the API and the semantics in one go.

| Today | Target | Phase | Notes |
|-------|--------|-------|-------|
| `m.set(k, v): void` | `m.set(k, v): Map[K, V]` | 2 | **API SHIPPED (Phase 2c)** — value-returning, callers can write `m = m.set(k, v)`. Runtime still mutates in place underneath: aliased maps (`var m2 = m1; m2 = m2.set(k, v)`) see the mutation reflected in `m1`. The rc-check + copy path (full Map CoW) requires Phase 1e prereq (struct/handle rc layout); deferred to Phase 2d. |
| `m.delete(k): bool` | `m.delete(k): (Map[K, V], bool)` | 2 | **API SHIPPED (Phase 2c)** — returns the (possibly new) map plus the present-before flag. Same aliasing caveat as `m.set` until Phase 2d. |
| `m.clear(): void` | `m.clear(): Map[K, V]` | 2 | **API SHIPPED (Phase 2c)** — empties and returns the map. Same aliasing caveat as `m.set` until Phase 2d. |

Migration sequence:

1. Add the new return-typed variants alongside the existing
   void ones under names like `set_returning` / `delete_pair`
   (Phase 2 entry).
2. Migrate all in-tree callers to the new names.
3. Delete the void variants and rename `set_returning` → `set`
   (still Phase 2, end of the PR series).
4. Document the API in `docs/COLLECTIONS.md` once the migration
   settles (Phase 6).

### Considered and rejected

- **`s += t` for string concat as syntactic sugar.** Adding it
  would imply mutation. Keep `s = s + t` (or a future
  `s.concat(t)`).
- **`arr.append!(v)` / `arr.set!(i, v)` with bang-suffix.**
  Imports an OCaml/Ruby convention that doesn't earn its keep
  here — the rc check is the same on every call site, no need
  for a syntactic marker.
- **Two variants per method (`sort` mutating + `sorted` pure).**
  Doubles the API surface for no real gain once Phase 2 lands.

### Out of scope

- File I/O (`write_string`, `BytesWriter.write_bytes`), TCP
  (`tcp_send`), and Stream — these are inherently effectful
  (the side effect IS the point) and don't model as
  immutable-returning. They stay as-is.
- The reader/writer protocols already thread state via the
  return value (e.g. `reader_read_line` returns `(reader,
  Option[string])` in the upcoming refactor). No further
  audit needed.

## Perceus

Perceus is a follow-up *optimisation pass* that elides redundant
inc/dec pairs. The basic insight: if a variable's value gets
incremented and then immediately decremented within the same
function (e.g. it was passed to a function and immediately
dropped because the function consumed it), both ops cancel and
can be removed.

Rough rules:

1. **Pair cancellation.** `inc(x); ... dec(x);` where nothing
   between them creates an extra reference → drop both ops.

2. **Drop-reuse.** A `dec(x)` followed by an `alloc(...)` of the
   same size class → reuse `x`'s storage in place, skip the
   free + alloc.

3. **Borrowed parameters.** If a function only reads its argument
   and the caller still holds it after the call returns, no inc
   is needed at the call site — pass "borrowed". The callee
   skips the dec on exit (since it didn't own a reference).
   Requires per-function analysis of "does this function transfer
   ownership of its argument?".

4. **Linear locals.** If a local is bound, used once, and dropped
   — drop the inc/dec pair entirely; just transfer ownership.

For phase 1 we ship correctness without Perceus. The naive
implementation will have inc/dec pairs everywhere; performance
will be measurably worse than today's always-copy on
short-lived values. Perceus arrives in phase 4.

The static analysis required for Perceus is much more tractable
than full escape analysis because it doesn't have to prove
*safety* (the runtime check does that) — only *redundancy*. If
Perceus is wrong, the program is slower but still correct.

## Phased rollout

Designed so each PR is bounded and the tree stays green between
them.

### Phase 0: layout migration (no semantic change)

PR: add the `rc` slot to every heap-allocated value's header. All
runtime helpers updated to write `rc = 1` at allocation. No
inc/dec emitted yet. No allocator change.

Effect: every program is slightly slower (one extra store per
alloc) and uses 4-8 more bytes per heap value. Nothing else
changes. Caps the blast radius of this PR.

### Phase 1: inc/dec everywhere

PR: insert inc/dec at every reference-creation and drop site. No
Perceus. Mutating ops still always copy (we haven't wired the rc
check into them yet). The bump allocator is unchanged (allocs
leak rc-zero values).

Effect: every program is significantly slower (one inc per
reference move, one dec per drop). Mutating ops still O(N) per
call. But: the infrastructure is now in place; subsequent
phases turn on the optimisations.

This phase is the bulk of the work and the bulk of the bugs.
Reference counts are stored in memory; getting them right at
every codegen site is fiddly. Tests will catch leaks (rc>0 at
program exit on a value that should have been freed) and double-
frees (rc going negative).

#### Phase 1d: array-only sub-phases (SHIPPED)

Phase 1 turned out to be tractable only when sliced per
type-category. Phase 1d covers ARRAYS only — strings, structs,
enums, closures join in Phase 1e once their layout grows an rc
slot. Eight slices, all merged:

  - 1d-i  (#1069): inc on `var y = x;` ident-RHS
  - 1d-ii (#1073): inc on `var y = h.items;` / `var y = m[i];`
  - 1d-iii (#1079): inc on `y = x;` ident reassignment
  - 1d-iv (#1081): inc on `f(arr)` call-arg pass
  - 1d-v  (#1085, #1088): dec on every array-typed param +
    local at every function-exit path
  - 1d-vi (#1088): dec on the OLD value of `y` before `y = x;`
    overwrites the slot
  - 1d-vii (#1091): inc on closure capture
    `function f() { return arr; }`
  - 1d-viii (#1095): inc on lit-element / field-init
    `Holder { items: arr }` / `[arr]` / `(arr, n)`

Two safety-net helpers landed alongside the alias machinery:

  - `b.locals[name]`-resolved zero-init at function entry, so
    dec-sweep visits to array slots whose Var decl was skipped
    (e.g. inside a never-taken `if` branch) hit a NULL the
    helper short-circuits on.
  - Low-address guard (< 0x10000 = `mmap_min_addr`) inside
    `__fern_rc_dec` on every backend, so a slot that holds a
    non-pointer value (enum tag, small i32 literal, stack
    garbage that doesn't look pointer-shaped) is treated as
    "not a heap object, don't touch" instead of dereferencing
    it. The runtime guard lets the IR layer skip control-flow-
    sensitive liveness analysis under the Phase 1 contract;
    Phase 2's mutate-in-place check will sharpen the contract
    per call site and the guard can shrink to just the NULL
    check.

Race fix: `ast.CodegenMu` (PR #1080) serialises arm64 + x86_64
`Emit` calls so the package-level `TwoWordOverride` toggle
they share doesn't race under parallel-seed differential
testing. Pre-existing bug surfaced by Phase 1's growing test
volume.

#### Phase 1e: widen to strings / structs / enums / closures

SHIPPED, all categories. Structs, enums, closures, tuples and strings all
grew rc tracking + inc/dec at alias/exit/overwrite sites + per-type drop
handlers in the phases that followed (see Phase 3's "Drop handlers" record
and the Phase-6 bullets). Native heap strings — the last holdout, gated on
the SSO native flip (item 5g) — landed once that flip went green on both
backends (2026-06-03). The original checklist is kept for the record:

  - Layout migration (Phase 0-style) adding the rc slot.
  - rc=1 init at every alloc site.
  - inc emissions at every alias site (mostly the same
    `needsRcIncOnAlias`-shape predicate, just with the type
    check widened from `ArrayType` to also accept
    `StringType`, `StructType`, `EnumType`, closure values).
  - dec emissions at function exit + reassignment overwrite.
  - Per-type drop handlers for the eventual real allocator
    (Phase 3): the handler walks the value's pointer-shaped
    fields and dec's each before returning the storage to the
    freelist.

Closures are the trickiest: their captures hold mixed-type
pointers, so the drop handler must traverse the env-block
field types. The IR's `closureconv` pass already records
per-capture types on the hoisted function; the drop handler
emit can consume that side-table.

### Phase 2: rc check in mutating ops

#### Phase 2a: arr.push fast path (SHIPPED)

`__fern_arr_push_grow(arr, oldLen, stride) → new_data` —
runtime helper called from IR-level `emitArrayPush`. Reads
rc at `[arr-8]` and cap at `[arr-12]`:

  - rc == 1 AND oldLen < cap → mutate in place. Bump rc to
    2 (so the Phase 1d-vi dec-on-overwrite at the assign
    site drops it back to 1) and write `[arr-4] = oldLen+1`.
    Return arr.
  - else → allocate a new buffer with
    cap = max(2*newLen, 4), memcpy the old payload, set
    new rc=1, len, cap. Return new data pointer.

The IR-side `emitArrayPush` shrinks to: call the helper,
then a width-correct tail store at `[new_data + oldLen*stride]`.
The OpIf-in-IR approach the earlier attempt tried failed the
SSA Lift dominance check; moving the branch into a runtime
helper sidesteps the structural issue and keeps the IR
straight-line.

The rc=2 bump on the in-place path keeps the Phase 1d-vi
dec-on-overwrite uniform across in-place, copy, var-decl, and
non-self-assign callers — see the helper's preamble in
`arm64.go:emitArrPushGrowRuntime` for the per-caller
walkthrough. Tests on every backend cover both paths plus the
"aliased rc>1 must copy" semantics.

#### Phase 2b: arr[i] = v copy-on-write (SHIPPED)

`__fern_arr_cow_inplace(arr, stride) → buf` — runtime helper
called from the IR's Index-assign lowering for writable local-
ident array targets. Semantics:

  - rc == 1 → return arr unchanged (in-place mutation).
  - rc >  1 → allocate a fresh buffer with the SAME cap+len,
    memcpy the payload, decrement arr's rc (skipping when the
    rc word's high bit is set — static-sentinel marker), set
    rc=1 on the new header. Return new data pointer.

The IR emit shrinks to: call the helper, write the element at
`[buf + i*stride]` via the existing per-stride bounds-check
helper (`__arr_idx_<n>`), and store buf back into the ident's
slot. The helper internalises ALL rc bookkeeping — keeping the
Phase 1d-vi dec-on-overwrite would mis-coordinate on raw wasm
where heap addresses sit below 0x10000 and the
`__fern_rc_dec` low-address guard short-circuits (so the
bump-then-dec design from Phase 2a's first attempt didn't
work for the Index-assign site). Limitation: only simple
`arr[i] = v` ident targets route through CoW today. Complex
shapes (`obj.field[i] = v`, `m[k][i] = v`, slice writes) keep
the legacy in-place emit — follow-up PRs widen coverage.

Tests on every backend cover both paths plus the
"aliased rc>1 must copy" semantics + a u8-stride regression
guard for the int_to_string scratch[i] pattern that surfaced
the wasm raw-_start `__fern_rc_dec` guard issue.

#### Phase 2c: Map delete / clear value-returning — **API SHIPPED**

`m.delete(k)` now returns `(Map[K,V], bool)` (map handle +
found-flag); `m.clear()` returns `Map[K,V]`. Callers can chain
or destructure: `var (m2, ok) = m.delete(k)` or inspect the
bool alone with `m.delete(k).1`. Statement-position calls
auto-discard via `OpDrop`.

Implementation: checker registers updated return types;
`emitMapDeleteReturningTuple` and `emitMapClearReturningMap` in
`internal/ir/ir.go` construct the result at the IR level (keeping
`__map_delete_impl` / `__map_clear_impl` in map.fern unchanged to
avoid the `Option[usize]` layout constraint). Interpreter and
self-host interp/checker updated to match. 6 new e2e tests
(arm64 / x86_64 / wasm) cover tuple destructuring, `.1` field
access, and `m = m.clear()`.

After Phase 2c, the user-facing API of Map mirrors Array: every
collection method LOOKS value-returning. The runtime semantics
still differ — Map mutates in place underneath while Array has
real CoW (Phase 2a + 2b). Aliased maps will see each other's
mutations; Map CoW lands in Phase 2d once Phase 1e adds rc
tracking to non-array types.

#### Phase 2d-borrow: borrowed parameters (SHIPPED — Map CoW enabler)

Prerequisite for Map CoW. Previously a tracked argument was
inc'd at the call site (Phase 1d-iv) and the callee dec'd its
parameter at exit (Phase 1d-v) — an "owned parameter" model.
That breaks copy-on-write for maps passed to functions: the
arg-inc bumps the Map handle to rc=2, so the CoW check inside
`m.set(...)` sees an alias and copies, and the callee's mutation
(expected to be visible to the caller — `inner(trace)` mutating
`trace`) goes to the discarded copy.

The fix is the Perceus borrow model: **parameters are borrowed,
not owned.** No caller-side inc, no callee-side exit dec. A Map
passed to a function stays rc==1, so the callee mutates it in
place (visible to the caller). A genuine local alias
(`var m2 = m1`) still inc's at the Var/Assign site and so gets a
copy on write. Ownership transfers (Var init, struct/array/
closure-capture stores, assignment) keep their inc. Net rc
traffic across a call is unchanged (was +1/−1, now 0/0), so the
observable behavior is identical except where the exact rc was
asserted — the `*RcAliasIncCallArg` / `Arm64RcDecAtExit` tests
were updated to the borrow counts.

#### Phase 2d: Map.set CoW (handle rc=1 + `__map_cow_inplace`) — SHIPPED

Closes the `Map.set` half of the gap left by Phase 2c. Built on
the borrowed-parameter model above, which is what makes it sound:
a Map mutated through a borrow stays rc==1 and is updated in place
(ref semantics preserved for `f(m); m.set(...)`), while a genuine
local alias (`var m2 = m1`) bumps rc and so copies.

Shipped:

  - **Live rc=1 on the handle.** `map_new_impl` now writes `1`
    (not the `0x80000000` immortal sentinel) into the handle's
    rc word at `[m - 8]`, so inc/dec on alias actually track.
  - **`__map_cow_inplace(m) → m`** — written in Fern (not per-
    backend asm; the `__alloc` / `__memcpy` / `__load_i32` shims
    let it live in `core/map.fern`). rc <= 1 (sole owner, or the
    negative-reading sentinel) → return `m` unchanged; rc > 1 →
    deep-copy BOTH the handle cell AND the kv buffer (a shallow
    handle copy would still share the buffer), fresh handle gets
    rc=1, return it. The source's rc is left to the assignment
    site's dec-on-overwrite (Phase-1 no-free makes the exact rc
    immaterial; isolation comes from the copy).
  - `__map_set_impl` threads through `__map_cow_inplace` before
    mutating. No bump on the in-place path, so MapLit's repeated
    per-entry sets stay rc==1 (in-place) without spurious copies.

Test coverage: `Test{Arm64,X86_64,WASM}MapSetAliasedCopies` —
`var m2 = m1; m2 = m2.set(...)` leaves `m1` intact. The existing
defer / `query_parse` ref-mutation tests confirm function-passed
maps still mutate in place under the borrow model.

#### Phase 2d-ii: Map.delete / Map.clear CoW — SHIPPED

Extends CoW to the other two mutators. They return `bool` /
`void` at the impl level (the value-returning API is synthesised
by the IR wrappers `emitMapDeleteReturningTuple` /
`emitMapClearReturningMap`), so cow can't live inside the impls
the way it does for `set` — the new handle had nowhere to go. The
fix threads cow at the **wrapper**: right after stashing the
receiver in `mapSlot`, route it through `__map_cow_inplace` and
store the (possibly new) handle back into `mapSlot`. That slot is
already used as both the mutation target passed to the impl AND
the map placed in the result (tuple element 0 for delete, the
returned value for clear), so an aliased map is deep-copied before
the in-place delete/clear runs and the source alias keeps its
entries.

Test coverage: `Test{Arm64,X86_64,WASM}MapDeleteClearAliasedCopies`
— `var (m3, _) = m2.delete(k)` and `m4 = m4.clear()` on an aliased
map leave the original intact.

Prerequisites that landed during Phase 2-prep:

  - **Capacity field in array header.** Shipped — array
    layout is now `[pad:4, cap:4, rc:4, len:4 | data]` with
    cap at `data - 12`, rc at `data - 8`, len at `data - 4`.
  - **Real allocator (Phase 3 below).** Still gated. Without a
    freelist, mutate-in-place wins only the copy cost — the
    alloc itself is already O(1) under the bump allocator.
    The payoff numbers in the doc above ("self-host parser +
    asm.fern push loops become O(N)") only fully realise once
    Phase 3 lets the rc==0 path actually reclaim storage;
    Phase 2a + 2b give the algorithmic win on the rc==1 path
    (one bumped rc + len write OR no rc change, no alloc /
    memcpy).

### Phase 3: real allocator

PR series: replace the bump allocator with size-class freelists so
dec'd values' storage actually gets reclaimed.

Effect: long-running programs (TCP handlers, etc.) stop leaking;
the rc==1 in-place CoW paths (Phase 2a/2b/2d) finally realise
their O(N) win because the rc==0 path returns storage instead of
bumping forever.

#### Current state (read before implementing)

- **`rc_dec` has NO free path.** On every backend the dec helper
  (`buildRcDecBody` in `internal/codegen/wasmbin/runtime.go:1740`;
  `emitRcDecRuntime` in `internal/codegen/arm64/arm64.go` ~766 and
  the x86_64 mirror) does: null guard → low-address guard
  (`<0x10000`) → load rc at `[ptr-8]` → static-sentinel
  short-circuit (high bit) → `rc = rc - 1` → store. **When rc
  reaches 0 nothing happens** — the storage is never reclaimed and
  no drop handler runs.
- **`__fern_alloc` is a pure bump cursor** (wasm: `buildAllocBody`
  at `runtime.go:1199`, cursor at `mem[40]`; natives mirror it).
  No size class, no freelist, `free()` is implicit (leak).
- **No drop handlers exist yet.** Nothing recursively decrements a
  freed value's pointer-typed fields/elements.

#### Prerequisite: drift-free rc (THE blocker)

Phases 1–2 were explicitly built on "free is a no-op, so the
exact rc doesn't matter — functional correctness comes from the
copy decision, not the count." Several shipped paths deliberately
let the rc drift to or below 0:

- **Array push in-place** bumps rc to 2 so the dec-on-overwrite
  nets to 1 (Phase 2a); a statement-position `xs.push(x)` with no
  reassignment leaves it bumped.
- **Map CoW** (`__map_cow_inplace`, Phase 2d): the in-place path
  returns the handle unchanged and relies on the assignment site's
  dec-on-overwrite; a unique `m = m.set(k,v)` therefore dec's the
  live handle to rc=0, and a *second* self-assign dec's it to −1.
- **The borrow model** (PR #1280) removed the call-arg inc / param
  exit-dec, which is correct, but means any code that still assumes
  the old owned-parameter counts is off by the borrow delta.

Turning on real freeing converts every one of these drifts from
"harmless" into a **use-after-free** (a value dec'd to 0 while
still referenced gets reclaimed under another live alias). So
Phase 3 CANNOT start with the freelist — it must start by
*measuring and then eliminating* the drift.

#### Implementation sequence (safe order)

1. **rc-underflow detector (FIRST) — SHIPPED (all three backends).**
   `__fern_rc_dec`, after the null / low-address / sentinel guards,
   tests `rc <= 0` *before* decrementing and bumps an over-release
   counter: wasm at a fixed linear-memory slot (`rcUnderflowAddr =
   48`, in the reserved mem[44..64] gap); arm64 / x86_64 at a BSS
   global `__fern_rc_underflow`. The `__rc_underflow_count()`
   builtin lowers to a per-backend runtime helper
   (`__fern_rc_underflow_count`) that reads the right store, so the
   detector runs everywhere. Pure instrumentation, no behavior
   change. `Test{WASM,X86_64,Arm64}RcUnderflowDetector` pin both
   contracts (clean program → 0; a deliberate double-dec → 1), and
   the x86_64 / arm64 variants additionally confirm the IR-side
   drift fixes hold on the natives (map self-assign → 0
   over-releases), since those fixes live in the shared IR layer.
   This de-risks the eventual step-4 free flip: a corpus-wide
   green detector across all backends is the go/no-go signal.

   **First measurements (the drift Phases 1–2 left behind):**
   - `m = m.set(k, v)` repeated (the idiomatic value-returning
     form) → **underflow**. cow keeps the unique handle in place
     and returns it, then the assignment's dec-on-overwrite drops
     its rc 1→0; the *next* self-assign dec's 0→−1. Counted once
     per handle (after −1 the high bit trips the sentinel guard).
   - statement-form `m.set(k, v)` (no reassignment) → **clean (0)**
     — no dec-on-overwrite, cow stays in place at rc==1.

   So the dominant drift source is exactly `x = x.<mutator>()`
   self-assignment. That scopes step 2: make the dec-on-overwrite
   cow-aware (skip the dec when the value was kept in place),
   rather than the unconditional dec used today. Natives get their
   own counter slot once the SSO flip frees up the string work
   around the same `rc_dec` helper.
2. **Drift audit + fixes — map self-assign DONE (drift-free,
   leak-free).** The dominant source the detector found,
   `m = m.set(...)` / `m = m.clear()`, is fixed with a COW-AWARE
   dec in `b.assign` (`isSelfMapMutation`). Map mutators cow in
   place WITHOUT bumping rc (unlike array push, so that
   statement-position `m.set(...)` sequences and MapLit's repeated
   per-entry sets don't spuriously copy). The assignment therefore
   dec's the old handle **iff it differs from the call's result**
   (an `OpNe` guard):
   - rc==1 in-place → call returns the same handle → no dec →
     rc stays 1 (no over-release; the second self-assign that used
     to hit 0→−1 is gone).
   - rc>1 aliased → call returns a fresh copy → old handle is
     dec'd → source nets to its remaining aliases (no leak), copy
     starts at 1.
   `TestWASMRcMapSelfAssignNoUnderflow` pins 0 over-releases across
   overwrite + clear + re-add with correct contents;
   `Test*MapSetAliasedCopies` confirms the aliased copy path stays
   value-correct. (The earlier skip-only form left the aliased
   source over-counted by 1; the `OpNe` conditional closes that.)
   - Arrays / structs / enums / closures were already drift-free
     (arrays via the push/set in-place rc bump; struct/enum/closure
     reassignment genuinely releases the old value).
3. **Drop handlers (recursive dec, no free yet) — arrays SHIPPED
   ON ALL THREE BACKENDS.** Dispatch is IR-side, NOT in `rc_dec`:
   every dec site already holds the static `ast.Type`, so the dec
   emitter routes to a type-specific drop helper while `rc_dec`
   stays the generic rc-arithmetic / sentinel / underflow
   chokepoint (Open-Question #3 resolved in favour of generated
   direct calls, no header type-id). Shipped:
   - `__fern_drop_arr_ptr(ptr, stride)` — wasm runtime
     (`buildDropArrPtrBody`), x86_64 (`emitDropArrPtrRuntime`),
     arm64 (`emitDropArrPtrRuntime`). On the LAST reference (rc==1)
     walks the `len` elements, dec's each via `__fern_rc_dec`, then
     dec's the array. Returns the ptr to match `rc_dec`'s stack
     shape. Guards: null + **low-address (`<0x10000`)** + static
     sentinel before recursing.
   - `emitRcDecLocalsAtExit` routes an `ArrayType` whose element
     is pointer-shaped rc-tracked (`arrElemIsRcTracked`: array /
     struct / Map / enum / closure — **string excluded**, it's not
     rc-tracked until the SSO flip) through the drop helper on
     every backend (no longer gated on `ptrW==4`). This balances
     the per-element inc from Phase 1d-viii that previously leaked.
   - **Root cause of the earlier native regression (resolved).**
     The first native-parity attempt SIGSEGV'd `TestSelfHostVMX86_64`.
     The cause was NOT an inline-stored struct/enum or a non-`ptrW`
     stride (the elements are clean `ptrW` pointers): the drop
     helper was missing the **low-address guard** that `__fern_rc_dec`
     carries. The scope-exit dec sweep visits array-typed slots that
     can hold a non-pointer scalar — e.g. an enum tag like `2`, or
     stack garbage from a `Var` decl inside a never-taken branch
     (the `exec_program` opcode switch in `vm.fern` has many such
     `Value[]` locals). Reading `[ptr-8]` / `[ptr-4]` on such a
     value faults; the plain-dec baseline tolerated it precisely
     because `__fern_rc_dec` short-circuits sub-64-KiB "pointers".
     The drop helper now replicates the guard on all three backends.
     Per-element decs route through `__fern_rc_dec`, so garbage
     element slots are guarded there too.
   - Scope: still the scope-exit sweep only; dec-on-overwrite keeps
     the plain dec (no regression — its nested elements still leak
     as before). Since free isn't enabled, the only effect is
     correct element counting. `TestWASMRcDropArrayElements`,
     `TestX86_64RcDropArrayElements`, `TestArm64RcDropArrayElements`
     prove the drop fires (a nested element's rc returns to its
     pre-construction value) with 0 over-releases;
     `TestSelfHostVM{X86_64,Arm64}` exercise the low-address guard.
   - **Struct field drop — SHIPPED ON ALL THREE BACKENDS.** A user
     struct (`info.Structs[Name]` present) with pointer-shaped
     rc-tracked fields drops those fields on its LAST reference
     before dec'ing the box, balancing the per-field inc from
     Phase 1e-struct-ii. Dispatch is IR-side in `emitRcDecLocalsAtExit`:
     `__fern_rc_is_unique(ptr) → i32` (a new guarded helper —
     null / low-address / sentinel / `rc==1`, mirrored on wasm +
     arm64 + x86_64) gates an `OpIf`; inside, each pointer field is
     loaded at its `structFieldLayout` offset and dec'd via the
     shared `decValueOnStack` (array fields recurse one level through
     `__fern_drop_arr_ptr`; struct / enum / closure fields are
     flat-dec'd). Runtime handle types (Map / Reader / Writer /
     MapIter) have no `StructDecl`, so they fall through to the plain
     box dec — and the `is_unique` sentinel guard skips the
     sentinel-headered ones anyway. `Test{WASM,X86_64,Arm64}RcDropStructFields`
     pin: drop fires (aliased array field returns to rc 1), aliased
     struct (`var h2 = h1`) does NOT double-drop, and nested
     struct/array fields stay value-correct with 0 over-releases.
     `TestSelfHostVM*` (heavy struct users) stay green.
   - **Enum payload drop (uniform case) — SHIPPED ON ALL THREE
     BACKENDS.** A heap-boxed enum (`[rc@-8 | tag@0, payloads@…]`)
     drops its pointer-shaped payloads on its LAST reference before
     dec'ing the box. Same IR-side `__fern_rc_is_unique`-gated shape
     as the struct drop. `uniformEnumDropLoads` emits the payload
     decs unconditionally (no runtime tag switch) ONLY when every
     payload-carrying variant shares an identical droppable
     signature — same `payloadLayout` offsets, same array-vs-flat
     kind. That is exactly the union shape (`type Value = VInt | VArr
     | …`): each variant carries a single struct pointer at the same
     offset, so whatever the tag, the box holds a droppable pointer
     there. Payloadless variants (static sentinels, no box) don't
     constrain the signature; the `is_unique` sentinel guard skips
     them at runtime regardless. NON-uniform enums (JsonValue, where
     JArray carries a pointer but JBool doesn't) and GENERIC enums
     (Option / Result — `ParamType` payloads aren't statically
     droppable) return `(nil,false)` and fall through to the plain
     box dec: their payloads leak, which is safe under no-free and
     reports 0 over-releases. `Test{WASM,X86_64,Arm64}RcDropEnumPayload`
     pin fresh + aliased-widened + non-uniform shapes (all 0
     over-releases); `TestSelfHostVM*` (heavy `Value`-union users)
     stay green.
   - **Deep (nested-of-nested) recursion — Stages A + B LANDED.**
     Stage A: a nested CONCRETE-struct field recurses through a
     generated `__drop_struct_<N>` instead of the flat one-level
     `rc_dec`, so nested struct boxes reclaim on the owning value's
     last reference. Stage B: an eligible array-of-struct drop recurses
     through a generated `__drop_arr_struct_<Elem>` loop (calling
     `__drop_struct_<Elem>` per element, even for childless element
     structs, then `__fern_arr_dec` for the buffer) instead of
     `__fern_drop_arr_ptr`'s flat per-element `rc_dec`, so element boxes
     (and their nested fields) reclaim too. (Follow-ups route ANY nested
     array FIELD / payload to its buffer-freeing helper — array-of-struct
     to the loop, array-of-rc / plain arrays to drop_arr_ptr / arr_dec —
     so a `struct Grid { rows: Row[] }` or `struct Buf { data: i32[] }`
     frees its buffer, not just top-level locals.) Each generated fn
     is_unique-gates internally, so calling it on a shared child/element
     is safe (dec only; free on the last reference). Deep recursion
     fires ONLY in the free-eligible drop branch — an escaped/tainted
     value keeps the flat dec, since a nested box there may still be
     reachable through the escape (the premature-free that first
     surfaced as a self-hosted-compiler segfault). Generation is
     restricted to the structs actually nested inside / element-of a
     dropped value (`collectNestedDropTypes` + `collectArrayElemStructs`)
     so native backends don't carry a drop fn per declared struct.
     Stage C: a NON-UNIFORM enum's tag-dispatch (variant-plan) drop arm
     is tag-guarded, so the payload type is statically exact in each
     arm — a concrete-struct payload there recurses through
     `__drop_struct_<T>` (freeing its box + nested fields) instead of
     the flat dec. (The UNIFORM enum path can't: the variant at the
     shared offset differs, so it keeps the type-agnostic flat dec.)
     Reachable because composite-literal RHS is now free-eligible (the
     struct-escape PR), so `var nd = Variant(Foo{…})` enums are owned.
     A follow-up widened `dropFnNameFor` to ALL concrete user structs
     (not just rc-field-carrying ones), so a CHILDLESS nested-struct
     field / payload now frees its box too (genStructDropFn's field
     loop is just empty → is_unique + box_free).
     A follow-up extended the per-closure drop thunk (genClosureDropThunk)
     to deep-drop CONCRETE-struct and array-of-struct captures (via
     __drop_struct_ / __drop_arr_struct_) instead of flat-dec'ing them;
     the thunk only runs when every capture was inc'd at MakeEnv, so it
     stays balanced. Enum / nested-closure captures keep the flat dec.
     A further follow-up steers an eligible enum with any POINTER-shaped
     payload (struct / array / enum / closure / Map — `enumHasPointerPayload`)
     away from the branchless uniform path (which can only flat-dec a
     single static payload type) to the tag-dispatch variant-plan path,
     so uniform unions like `Value = VInt | VArr` deep-drop each
     variant's exact struct box, and array-payload enums (incl. generic
     `Option[i32[]]`) free their payload buffer per tag-guarded arm.
     A further follow-up reclaims a Map-typed nested field / payload /
     capture (the `struct Req { headers: Map[..] }` shape): dropStructField
     / appendChildDrop / the closure thunk route it through
     __map_drop_values + __fern_map_drop (both self-guard on the map's
     own rc==1) instead of flat-dec'ing the handle, freeing the whole
     map structure on the owner's last reference.
     A further follow-up reclaims GENERIC enums with a struct payload
     (Option[Item] / Result[Item, E]): the eligible enum drop
     substitutes the type args (et.Args) into the generic decl's
     ParamType payloads (substituteEnumDecl + resolveTypeParam),
     reproducing the concrete payloads emitEnumNew sized the box from,
     then deep-drops via the variant-plan path. Adopted ONLY when the
     substituted decl exposes a struct payload — that proves a
     heap-boxed (non-pair-form) instantiation, so scalar Option[i32]
     (pair-form, no box) is left on the flat path untouched.
     A further follow-up generates a tag-dispatched __drop_enum_<Name>
     for any CONCRETE (non-generic) enum with a payload-carrying variant
     and routes an enum-typed nested field / payload / capture to it
     (dropFnNameFor's enum case) — reading the runtime tag picks the
     exact per-variant type, so a `Holder { val: Value }` field reclaims
     the enum box + payload. Nested closures still keep the env-only drop.
   - **Remaining:** generic enum FIELDS (Option[Item] as a field needs
     the type-arg substitution the inline local path does), and generic
     enums whose payload is array-of-struct / nested ParamType; the
     map's own struct-typed VALUE column (the runtime is type-erased —
     needs a value-drop fn
     pointer; array values already reclaim); the dec-on-overwrite site
     (entangled — `push`'s copy path transfers element ownership without
     an inc, so routing array overwrite through the drop would
     double-release; needs a per-method ownership audit or a self-push
     exclusion first).
   - **rc-correctness corpus — LANDED (the step-4 go/no-go net).**
     `Test{X86_64,Arm64,WASM}RcCorrectnessCorpus` (`rc_correctness_test.go`)
     run a shared table of ~12 nested-value programs — array of
     structs, struct of arrays (aliased), nested arrays, unions,
     non-uniform enums, closures (array + scalar capture), string-kv
     maps, a push loop of structs, reassignment chains, borrowed
     params, and a deep mixed grid — each returning a folded
     value-correctness + `__rc_underflow_count()` check that must be
     0 on all three backends. Leak-only gaps (closures / maps /
     generic enums / deep nesting) still read 0 here (a leak doesn't
     bump the underflow counter), while any accidental over-release
     surfaces immediately. This is the corpus the step-4 flip is
     gated on; grow it as new shapes land. No existing drift was
     found when it was added.
4. **Freelist allocator, behind a build flag.** Per-size-class
   free lists (Roc's classes: 8/16/24/32/48/64/96/128/256/512/
   1024/2048; larger → bump+unmap). The free path needs the
   allocation's size at `rc==0`: store the size class in a header
   word (the rc word has spare bits, or add a class nibble) so
   free can find the right list. `__fern_alloc` checks the class
   list before bumping. Flag-gated so the no-free arena stays the
   default until the detector is green end-to-end.

   **Allocator mechanism — STARTED (x86_64).** Behind the
   `ast.RcFreeEnabled` codegen flag (default false → pure bump,
   byte-identical to every prior phase), x86_64 now carries a
   segregated freelist: `__fern_freelist_heads` is 128 BSS heads,
   one per 16-byte size class (16…2048; allocations are already
   16-byte-rounded, so classes are exact-fit, no waste). `__fern_free
   (base, size)` pushes a block onto its class's intrusive list
   (successor pointer in the block's first 8 bytes); `__fern_alloc`
   pops the matching class before bumping. Exposed to Fern as the
   `__free(ptr, size)` shim (companion to `__alloc`) so the path is
   testable in isolation. `Test{X86_64,Arm64,WASM}FreelistReuse` pin
   same-size reuse, different-class non-aliasing, and LIFO order —
   flag-on. **All three backends are now at freelist parity.** arm64
   mirrors the x86_64 BSS freelist line-for-line; wasm puts its 128
   i32 heads at a fixed `freelistHeadsAddr = 256` (region [256, 768))
   in the always-free reserved window [96, 1024) — the named
   low-memory scratch tops out at 92 and the bump cursor floor is the
   string-pool end, which never falls below `stringStart = 1024`.
   wasm allocations round to 16 (matching the natives' class
   granularity) only when the flag is on; flag-off keeps the 4-byte
   rounding, so the default wasm module is byte-identical.

   **First dec-site wiring — `__fern_drop_arr_ptr` tail-free (all
   backends).** Flag-on, after the drop walks + dec's an array's
   elements, on the last reference (rc==1) it returns the buffer to
   the freelist (`__free(base, size)`; base = data - headerBytes,
   size = headerBytes + cap*stride) instead of dec'ing to 0. So
   rc-tracked-element array buffers (`Foo[]`, `i32[][]`, …) are
   reclaimed at scope exit. Validated flag-on by
   `Test{X86_64,Arm64,WASM}ArrayDropFree`: a 50-cycle build/drop
   churn through a helper (each buffer freed + reused) plus the
   ENTIRE rc-correctness corpus re-run with free actually happening
   — the use-after-free net. All 20 corpus programs + the churn stay
   value-correct with 0 over-releases on x86_64 locally; arm64 + wasm
   ride CI.

   **Second dec-site wiring — `__fern_arr_dec` (all backends).** A
   size-aware array dec — dec the rc and, on the last reference,
   return the BUFFER to the freelist (base/size from cap+stride, no
   element walk). The IR routes two sites to it flag-on: plain-array
   (`i32[]`) scope-exit, and the **array dec-on-overwrite** (`xs =
   …`). The latter is the O(N²)→O(N) push-loop win: on a copy-grow
   the old buffer's pointer elements were transferred to the new
   buffer (no inc), so freeing the old BUFFER is correct, while an
   in-place push (rc bumped to 2) dec's to 1 and doesn't free.
   Struct / enum / closure overwrite targets keep the plain dec.
   Validated flag-on by adding a 200-grow push-loop (`xs = xs.push`)
   to `Test{X86_64,Arm64,WASM}ArrayDropFree` alongside the churn +
   the full corpus: the read-back sum stays correct only if every
   free+reuse is sound. Green on x86_64 locally; arm64 + wasm ride
   CI. The flip itself stays gated on a corpus-wide-green detector on
   all backends + explicit owner sign-off.
5. **Enable + verify. — DONE; flipped on by default.** Both
   over-release classes are closed and `RcFreeEnabled` now defaults to
   `true` (landed after the escape corpus + differential gate went
   green corpus-wide on all three backends in CI, a local x86 flip
   probe over the whole e2e suite incl. the self-host VM passed
   flag-on, and owner sign-off). History below.

   The first flip (`RcFreeEnabled = true`) passed the
   whole x86_64 + interp suite, the fixture differential gate, and the
   self-host VM under `RcFreeDebug` — but CI then caught two wasm-only
   tests (`TestWASMQueryParse` exit 3, `TestWASMJsonParse` exit 40)
   failing free-on. Reproduced on x86 too (so it's not wasm-specific;
   those programs simply had no x86 coverage — they're `TestWASM*` and
   weren't in the differential gate). Root cause: the **dual
   ESCAPE-OUT over-release**. The borrow-aware analysis closed the
   borrowed-IN class (values that flow in uncounted); the symmetric
   case is values that flow OUT uncounted — a local stored into a
   container that retains it WITHOUT an inc, then freed at scope-exit
   while the container still holds it (a free-then-reuse value
   corruption; the detector's quarantine mode hides it, since it's a
   stale READ, not an rc op).

   **Fix (borrow-aware escape taint).** `computeFreeEligible` now also
   taints any owned array local that escapes into a non-incrementing
   sink, so the owner never frees out from under the container. The
   non-incrementing sinks, confirmed by reading each lowering:
   `__method_Map_set` key + value, MapLit entries, `__method_Array_push`
   elements (emitArrayPush stores without inc), enum-constructor
   payloads (emitEnumNew stores without inc), and index / field /
   capture assignment targets (`grid[i] = row`, `p.items = arr`,
   `cap = arr`). StructLit / TupleLit construction DO inc their stored
   values (needsRcIncOnAlias at the alias sites), so escaping through
   those is already safe and needs no taint. Taint also flows backward
   across bare-Ident aliasing (`tmp = arr; m.set(k, tmp)` taints arr).
   New `rc_correctness` entries `escape_array_into_{map_value,
   pushed_element,enum_payload,index_assign,field_assign,struct_field}`
   exercise each sink free-on (churn-forced reuse so a wrongly-freed
   block corrupts the read-back); five fail free-on without the taint
   (the struct one is inc-protected). They run on all three backends
   via `Test{X86_64,Arm64,WASM}ArrayDropFree`.

   **Widening slice — composite-literal locals + conditional-alias
   taint (DONE).** `rhsTainted` now returns false for a `StructLit` RHS
   (a fresh struct is OWNED, not an alias of a borrowed value — same as
   the existing `ArrayLit` / `MakeClosure` cases), so `var s = S{…}`
   locals become free-eligible and reclaim their box at scope exit
   (previously they fell through `default: return true` and leaked).
   Enabling that surfaced a latent use-after-free the no-free default
   had masked: a local aliased through a CONDITIONAL value position —
   `var v1 = if (c) { v0 } else { v0 }` or `var v1 = match (x) { … =>
   v0 }` — is not inc'd (the alias-inc only fires for a direct Ident
   RHS, and a per-arm inc can't be emitted unconditionally since some
   arms yield fresh rc=1 values), so freeing `v0` stranded `v1`. The
   differential fuzz (seed 1836) caught it as an x86 segfault; the
   match-expr form was already a latent over-release for ARRAYS on the
   pre-existing code, just never reuse-corrupted in free mode.
   `computeFreeEligible`'s escape walk now taints any local that flows
   out of an `IfExpr` / `MatchExpr` arm, so the conditionally-aliased
   source is never freed. Corpus: `struct_literal_local_churn_free`,
   `ifexpr_alias_struct_no_free`, `matchexpr_alias_array_no_free`. The flip now waits only
   on (a) that corpus + the differential gate green corpus-wide on all
   three backends in CI and (b) explicit owner sign-off. The
   pre-step-5 no-free arena stays the default; a handful of tests pin
   it via save/restore. What LEAKS (safe — no over-release):
   borrowed/borrowed-derived buffers, generic enum boxes (Option/Result
   over scalar payloads), closure boxes, nested struct/enum fields (one
   level), and map entry keys/values. (Owned top-level arrays, maps,
   struct boxes, and enum boxes — uniform and non-uniform — now free;
   see the widening slices below.)

   **Widening slice — map structural reclamation (DONE).** A Map is a
   runtime handle (StructType "Map") whose rc already balances (it
   inc's as a struct in needsRcIncOnAlias). `__fern_map_drop(m)` — the
   Map analogue of `__fern_arr_dec`, on all three backends — returns
   the handle's storage to the freelist at the last reference (rc==1):
   first the buf (buckets+entries, size = 16 + cap*(4+entryStride),
   cap at [buf+0]), then the 16-byte handle cell. `computeFreeEligible`
   now marks owned Map locals eligible (same borrow-aware taint as
   arrays); `emitDec` routes eligible Map locals through the helper.
   Entry keys/values keep their existing accounting (they leak) — a
   follow-up converts `map.set` to retain-on-store so array-typed
   values can be freed too. Returned maps stay safe via the struct
   return-inc (the drop only frees at rc==1). Covered by
   `rc_correctness`'s `map_owned_churn_free` (50 build/free cycles) and
   `map_returned_not_freed`, run free-on on all three backends.

   **Widening slice — struct-box reclamation (DONE).** A user struct
   already drops its rc-tracked fields at the last reference (the
   `__fern_rc_is_unique` block in `emitDec`) and inc's as a struct on
   alias/return, so freeing its box is the natural next step. New
   `__fern_box_free(data, size) -> data` (all three backends) returns
   the box (base = data-8 rc header, freed size = size+8) to the
   freelist and returns `data` — the uniform-result shape the IR's
   `OpDrop` needs, which a direct void `__free` can't give on wasm. The
   IR pre-gates it on rc==1 and emits it after the field drops, so the
   helper is a thin guarded `__free` wrapper. `computeFreeEligible` now
   marks owned user-struct locals eligible (same borrow-aware taint;
   structs that escape into a container are tainted, and returned
   structs are protected by the struct return-inc + the rc==1 gate).
   Nested struct/enum fields now recurse through generated per-type
   `__drop_struct_` / `__drop_enum_` fns (transitive reclamation Stage
   A) rather than leaking at one level; arrays-of-struct elements
   reclaim their boxes too (Stage B). Map keys / non-array values and
   enum payloads remain later slices.
   Covered by `rc_correctness`'s `struct_box_churn_free` (200
   build/free cycles) and `struct_returned_not_freed`, plus the
   existing struct-heavy corpus entries, run free-on on all three
   backends. The same `__fern_box_free` will reclaim enum boxes next
   (per-variant size from the tag).

   **Widening slice — enum-box reclamation (DONE).** An owned enum
   reuses `__fern_box_free` to return its box at the last reference,
   gated on a STATIC box size: an enum box is alloc'd per-variant
   (`payloadLayout size + 8`), so freeing needs every payload-carrying
   variant to agree on size. `uniformEnumBoxSize` checks that, and the
   existing `uniformEnumDropLoads` already requires a uniform droppable
   layout (the drop emits no tag switch); `emitDec` frees only when
   BOTH hold (e.g. a union of single-pointer variants, or
   `A(i32[]) | B(i32[])`). Non-uniform (`JsonValue`) and generic
   (`Option`/`Result` over scalars) enums keep the plain box dec and
   leak as before. The `__fern_rc_is_unique` gate filters out
   payloadless static sentinels (their rc high-bit reads non-unique),
   so `__fern_box_free` only ever sees a real rc==1 box.
   `computeFreeEligible` marks owned enum locals eligible (same
   borrow-aware taint; returns protected by the enum return-inc +
   rc==1 gate). Covered by `rc_correctness`'s `enum_box_churn_free`
   (200 build/free cycles) and `enum_returned_not_freed`, run free-on
   on all three backends. Payloads still drop one level (array payloads
   flat-dec, like struct fields).

   **Widening slice — non-uniform enum boxes (DONE).** Enums whose
   payload-carrying variants disagree on droppable layout or box size
   (e.g. JsonValue, or `I(i32) | A(i32[])`) now free via a per-tag
   dispatch: at rc==1, read the tag at `[data+0]`, switch to the
   matching variant, drop that variant's droppable payloads, and free
   with that variant's exact box size (`enumVariantDropPlan`). The tag
   is stashed in a scratch local so later switch arms never read the
   (possibly freed) box. Bails to the plain box dec for generic
   ParamType payloads (unknown size / drop-kind — Option/Result over
   scalars still leak, safe). The uniform path stays the branchless
   fast-path; this is its fallback. Covered by `rc_correctness`'s
   `enum_nonuniform_box_free` (200 cycles churning two distinctly-sized
   boxes), run free-on on all three backends.

   **End-to-end lock-in — stdlib hot paths under free.** Two
   `rc_correctness` entries run real prelude code free-on across all
   three backends so the box-reclamation family is exercised together
   on the use-case shapes (not just synthetic churns):
   `stdlib_query_parse_roundtrip` (std/url → `Map[string, string[]]`:
   map-structural free + the escape-out path + string[] values) and
   `stdlib_json_roundtrip` (std/json `json_parse`/`json_encode` →
   `JsonValue`: non-uniform enum box free + nested Map/array). Both
   fold `__rc_underflow_count()` into the result, so any over-release
   in the combined drop paths trips the detector.

   **Widening slice — map growth buffer (DONE).** `__map_grow` doubles
   the kv buffer and re-hashes, leaving the old buffer leaked. It now
   `__free`s the old buffer after copying: the entries' key/value
   pointers are copied into the new buffer (the keys/values themselves
   are untouched), the handle is repointed, and `__map_set_impl` runs
   `__map_cow_inplace` first so the map is uniquely owned — nothing
   else aliases the old buffer. A map built incrementally (query_parse,
   json object parsing) no longer leaks each intermediate buffer.
   Covered by `rc_correctness`'s `map_growth_buffer_free` (100 inserts
   → several doublings), run free-on on all three backends. (This is a
   pure-Fern stdlib change, so it's backend-agnostic.)

   **What's left (larger design efforts, not yet done).** Map entry
   keys/values need full retain semantics — inc-on-store AND
   inc-on-get — because the borrow model returns get-results
   uncounted, so the map can't know a value isn't still borrowed when
   it drops (freeing it would UAF the borrow). Closure env blocks need
   per-closure drop glue (the FuncType drop site doesn't know which
   hoisted closure / capture set the slot holds, so neither the box
   size nor the capture types are statically available). Both are
   bigger than the box-family slices and reopen over-release risk;
   they're deferred.

   **Flip-readiness gate — LANDED (the step-5 differential).**
   `Test{X86_64,Arm64,WASM}FixturesFreeMatchesNoFree` run the entire
   data-driven fixture corpus (`testdata/cases`, ~82 runnable
   programs) BOTH flag-off and flag-on and assert byte-identical
   stdout + exit. Freeing is semantically invisible, so any
   divergence is a reclamation bug (a freed-then-reused block still
   referenced) surfaced by a real program. All 82 agree on x86_64
   locally; arm64 + wasm ride CI. This gate already paid for itself:
   it caught a latent flag-on link error — `__fern_arr_dec`
   referenced the `__fern_rc_underflow` BSS counter, which was only
   emitted under `usesRcDec`/`usesRcUnderflowCount`; the gate
   (`usesArrDec`) now pulls it in. The standing green-on-all-backends
   signal from this gate + the rc-underflow corpus is the evidence
   the default flip is waiting on (plus owner sign-off).

   **Flip attempt — BLOCKED by the self-host VM (still off by
   default).** With sign-off, the default was flipped to `true` and
   the suite run. The fixture-corpus gate is green free-on, but
   `TestSelfHostVMX86_64` (a far larger real program than any
   fixture) hit a use-after-free: `__fern_alloc`'s freelist pop
   dereferenced a corrupted next-pointer — a buffer freed while
   still referenced, then written through the stale ref. (An earlier
   "full e2e green free-on" probe was a FALSE green: the flag-on test
   helpers reset the global to `false` mid-suite, so most tests
   silently ran free-OFF. Making the helpers save/restore the prior
   value exposed the real failure — and is the fix for the
   false-green.)
   - **Root cause #1 (fixed): borrowed-param overwrite.** Under the
     borrow model a borrowed array param has rc==1 (no caller-side
     inc), so `ps = ps.push(...)` on a *parameter* freed the OLD
     buffer at the dec-on-overwrite — which the CALLER still
     references (`compile_expr(ops, …)` reassigning its `ops` param).
     Fix: the dec-on-overwrite free is now gated on
     `!isParamName` — params keep the plain dec; the caller owns and
     frees the buffer. This turned the SIGSEGV into a non-crash.
   - **Root cause #2 (OPEN): residual value-corruption.** After the
     param fix the VM no longer crashes but returns a wrong result
     (a different over-release frees a still-live buffer that's then
     reused, corrupting a value rather than the freelist). The flip
     stays OFF until this is diagnosed + fixed. This is the
     multi-bug reclamation-correctness slog the Risk register
     predicted; the self-host VM is the oracle for it (the fixture
     gate is necessary but not sufficient). Do NOT widen reclamation
     (struct / enum / closure / map box free) onto this base until
     the array over-releases are all closed — more free sites = more
     UAF surface.
   - **UAF detector (LANDED, x86_64) + root-cause-#2 lead.**
     `ast.RcFreeDebug` (set alongside `RcFreeEnabled`) turns the
     freelist into a use-after-free detector: the array free sites
     poison the freed block's rc word with `ast.RcPoison` and
     quarantine it (no recycle — `__fern_alloc` keeps bumping), and
     `__fern_rc_inc` / `__fern_rc_dec` `ud2`-trap the instant they
     touch a poisoned block (a stale reference to an over-released
     buffer). Running the self-host VM under gdb in this mode traps
     in `parser.parse_module`'s entry: it inc's a freed array —
     `Par { toks: toks }` on the borrowed `toks: Token[]` that
     `run_source` passes as `parse_module(lexer.tokenize(src))`. So
     root cause #2 is an over-release of a borrowed TEMPORARY array
     argument: the `tokenize(...)` result (rc=1, never bound to a
     local) is freed while `parse_module` still borrows it. The
     detector is the tool to finish the diagnosis; the fix (correct
     borrowed-temp-argument lifetime) is the next step and the
     remaining flip blocker.
   - **Return-value UAF (#2a) — FIXED.** The first instance the
     detector + a gdb watchpoint pinned down: `return <local array>`
     left the value on the operand stack while the exit sweep's
     `__fern_drop_arr_ptr` freed it (rc 1→0), handing the caller a
     dangling pointer (`lexer.tokenize` returning its `ts: Token[]`).
     Fixed by inc'ing a returned alias before the sweep
     (`needsRcIncOnAlias`, closures excluded to preserve defunc).
     Also fixed a symmetric latent underflow the no-free arena
     masked.
   - **THE CORE BLOCKER — borrow model ⇄ free are incompatible.**
     After #2a the detector traps next in `compile_block` dec'ing a
     freed `ops: Op[]`. A watchpoint shows the buffer freed at
     `compile_stmt`'s exit while `compile_block` still holds it.
     Root: `compile_stmt(st, ops, lt)` / `compile_block(body, ops,
     lt)` / `compile_expr` / `patch_jump` thread `ops` through
     BORROWED params and `CompileResult { ops, lt }` fields. Under
     the borrow model (Phase 2d) a borrowed arg gets NO caller-side
     inc, so the rc undercounts every borrowed alias — fine under
     no-free, but with free a dec-to-0 (a struct-field drop, a
     scope-exit drop, …) reclaims a buffer that a live borrow still
     references. Disabling the struct-field free just shifts the
     over-release elsewhere (the freelist makes the bugs interact
     non-locally), confirming this is not a series of one-off bugs
     but the borrow/free tension itself. **The flip cannot ship
     until it's resolved**, and the resolution is a design choice,
     not a patch:
       (a) inc borrowed args at the call (revert the Phase 2d borrow
           optimization) → rc accurate, free safe, but loses Map /
           array mutate-in-place;
       (b) a borrow-aware "don't free a borrowed value or anything
           aliased from one" analysis (taint borrowed-derived
           locals) — Perceus's actual rule; the principled but larger
           fix;
       (c) keep free OFF (today's safe default) until (a)/(b) lands.
     Until then `RcFreeEnabled` stays false; the array reclamation,
     freelist, gate, and detector are all in place to support
     whichever resolution is chosen.
   - **RESOLVED via borrow-aware free analysis (option b).**
     `computeFreeEligible` (in `lowerFunc`) computes, per function,
     which array-typed locals are OWNED: every value written to them
     is freshly owned (an array literal, or a call whose args+receiver
     are all owned), and they're never a parameter, a for-in / match /
     if-let / let-else / destructure binding, nor assigned a tainted
     ident / field / index / slice. Taint propagates to a fixpoint
     (a call passed a tainted arg yields a tainted result — the
     conservative inter-procedural alias). The array dec sites
     (scope-exit `emitDec`, the dec-on-overwrite) free ONLY eligible
     locals; borrowed-derived ones use a plain `rc_dec`. Struct fields
     and enum payloads never free (their borrow-ness isn't tracked —
     `decValueOnStack(..., mayFree=false)`). Result: the self-host VM
     runs CLEAN with free on — `RcFreeDebug` reports no use-after-free
     across the whole compile, where before it trapped. The flip's
     correctness blocker is closed; flipping the default is now a
     decision (owner sign-off) on top of the still-green gate.

#### Resolved design decisions (from Open Questions)

- **rc width:** i32, panic on overflow (Roc-style).
- **rc offset:** fixed `-8` from the data pointer (already the
  convention every backend's inc/dec uses), so the helpers stay
  polymorphic.
- **Drop dispatch:** generated `__fern_drop_<type>` direct calls
  (we already monomorphise) rather than a runtime vtable.


### Phase 4: Perceus pair-cancellation

PR: add the static analysis that removes provably-redundant
inc/dec pairs.

Effect: short-lived values stop paying the rc tax. Performance
on small benchmarks recovers and surpasses today's always-copy.

**Move-on-return (DONE).** First pair-cancellation slice, done
structurally at lowering (no separate pass / liveness analysis): when
a function returns a bare owned rc local, the return-transfer inc and
that local's exit-sweep dec cancel — the inc exists only to survive
the sweep. `emitRcDecLocalsAtExitExcept` skips the returned local in
the sweep and the inc is not emitted, so the value is handed to the
caller at its current rc with zero rc traffic (saves an inc + a dec
per rc-returning function). Excludes params (borrowed — never swept,
so the inc has no dec to cancel) and closures (different drop path).
Backend-agnostic (it's in `lowerFunc`). Covered by `move_on_return_test`
(fires for owned locals, keeps the inc for params); the differential
gate confirms the elision is observationally invisible.

**Move-on-alias (DONE).** Sibling slice: `var y = x` / `y = x` where
the source `x` is an owned rc local whose alias is its LAST occurrence
is dead afterward, so the alias transfer inc and `x`'s exit-sweep dec
cancel. `computeMovedLocals` indexes every Ident in pre-order and moves
the alias iff its read of `x` is `x`'s max-index occurrence — covering
multi-use locals (`var n = x[0]; var y = x`), not just single-use, and
ruling out any later read OR reassignment (a `var x` definition is not
an Ident node, so the max-index occurrence also being the alias means
nothing touches `x` afterward). Two dominance guards keep the global
sweep-exclusion leak-free: the alias must be a TOP-LEVEL statement (not
nested in a branch/loop that could skip it) and no `return` may precede
it at the top level — so `x` is moved on every path to an exit. Aliases
inside control flow keep their inc. Removing a balanced inc+dec pair
can't change the net rc (safe); the last-occurrence + dominance guards
prove no live read is stranded and nothing leaks. Composes with
move-on-return: `var x = […]; var y = x; return y` carries zero rc
traffic. Covered by `TestMoveOnAlias{ElidesIncForSingleUseLocal,
MovesAtLastUseEvenIfMultiUse, KeepsIncForBranchedAlias,
KeepsIncWhenReadAgain}`.

**Move-on-construction (DONE).** Third pair-cancellation slice, same
structural shape: when a `StructLit` built at a dominating top-level
statement consumes an OWNED rc local in a non-string rc-tracked field at
the local's LAST use (`var s = Wrap{ inner: x }`, `x` dead after), the
field-init inc and x's exit-sweep dec cancel — x's single reference is
moved into the field. `markConstructionMoves` (folded into
`computeMovedLocals`) reuses the same last-occurrence + dominance guards
(top-level statement, no preceding return) as move-on-alias, so x is
moved on every path to an exit; the StructLit inc sites (normal +
reuse-path) skip the inc when `b.moveSites[fieldIdent]` is set, and
`moved[x]` drops x from the exit sweep. The struct's own field-drop
(`emitDec`, which dec's pointer fields on drop regardless of free-
eligibility) releases the moved value exactly once, so the net rc is
unchanged. Eligibility mirrors the inc/drop sides exactly
(`arrElemIsRcTracked` field — array / struct / enum / closure / tuple;
strings excluded). Also covers ARRAY LITERAL elements (`var xs = [x]`):
an owned rc local consumed as an element at its last use is moved into
the array, balanced by `__fern_drop_arr_ptr`'s per-element dec at the
array's drop. (Tuple / enum literals are deferred — enum payloads aren't
inc'd on construction at all, so there's no pair to cancel; tuple
element drop isn't wired the same way.) Composes with move-on-return
(`var s = Wrap{inner: x}; return s` carries zero rc traffic) and with
the Phase 5 reuse path. Covered by IR `TestMoveOnConstruction{ElidesIncForLastUse,
KeepsIncWhenReadAgain, KeepsIncForBranched, ComposesWithReturn}` + e2e
`Test{X86_64,Arm64,WASM}MoveOnConstruction` (once / churn / returned,
value-correct + 0 over-release under free). The same machinery was then
extended to ARRAY elements, TUPLE elements, and CLOSURE captures (every
`*ast.{ArrayLit,TupleLit,MakeClosure}` inc site is gated on
`b.moveSites`), so move-on-construction covers all four inc'ing
container shapes.

**Move-on-destructure (DONE).** Final pair-cancellation slice: a
`var (a, b) = t` where `t` is an owned rc tuple local at its last use
moves `t` into the destructure temp — the temp's box-aliasing inc and
`t`'s exit-sweep dec cancel. Only the tuple-BOX inc/dec pair is removed;
the extracted elements keep their own dup-incs (so they survive the
temp's `box_free`). `computeMovedLocals` gained an `*ast.Destructure`
case; covered by IR `TestMoveOnDestructure{ElidesIncForLastUse,
KeepsIncWhenReadAgain}` + the e2e `destructure` case. With this the move
family is complete — an audit of all nine `emitAliasInc` sites confirms
every genuine last-use move is gated (see "Remaining frontier" under
Phase 5 for what's deliberately left).

**Precise drops — the garbage-free property (slices 1+2 SHIPPED, straight-line
subset).** This is the defining Perceus feature: free a value right after its
LAST USE rather than at scope/function exit, so peak memory matches the live
set. Until now drops were placed at the function-exit sweep (plus the move
optimisations + a runtime low-address guard that let the IR skip control-flow-
sensitive liveness). `computePreciseDrops` adds true last-use placement for
a deliberately narrow, obviously-sound subset: an owned, `freeEligible`,
single-declaration, non-reassigned, non-moved local that is an **ARRAY**
(`preciseDroppableType`) whose every reference is a top-level statement (none
inside a nested if / while / for / match block) is dropped right after its
last top-level use. `lowerFunc` iterates the body's top-level statements and
splices the per-statement `emitPreciseDrop` (`emitOwnedSlotDrop` + zero-slot)
in.

Type scope (arrays only so far):
  - **Slice 1 — primitive-element arrays** (`i32[]` / `u8[]` / `f32[]` ...):
    drop is a PURE buffer free (`__fern_arr_dec`, no element decs).
  - **Slice 2 — rc-element arrays** (`struct[]` / `enum[]` / `T[][]` /
    `tuple[]`): drop is the deep `__drop_arr_*` loop, so the element
    boxes / inner buffers reclaim too — the large arrays-of-boxes get their
    WHOLE structure freed early, not just the outer buffer. Sound via the
    same invariant + alias gates: each per-element drop is_unique-gates (a
    counted alias of an element only DECs), and `freeEligible` (taint)
    excludes arrays whose elements alias a live local.
  - **Slice 3 — `string[]`**: drop is `__fern_drop_arr_str` (two-word wasm /
    arm64-TwoWord) / `__fern_drop_arr_ptr` (native single-word x86_64) — each
    element string is str_dec'd, then the buffer freed, so the whole structure
    (buffer + heap strings) reclaims early. `emitOwnedSlotDrop` gained the
    string-element branch (which also fixes loop-reinit `string[]` drops). The
    per-element str_dec is_unique-gates, so a string element aliased into a
    live local only DECs. The array element scope is now COMPLETE (every
    element kind).
  - **Slice 4 — STRUCT + tuple boxes** (`preciseDroppableType` adds
    `StructType` / `TupleType`): a dead struct/tuple local is dropped at its
    last use via the deep `__drop_struct_` / `__drop_tuple_` fn — frees the box
    AND its rc-tracked fields. Sound because StructLit / TupleLit construction
    INCs its pointer fields, so the precise drop is rc-protected (a field
    aliased into a live local only DECs — the same reason slice-2 rc-element
    arrays are sound). Churned one rc-count golden test
    (`RcAliasIncFieldAndIndex/field_access`, a struct whose field is aliased —
    updated to keep the struct live through the `__rc_get` check, like slice 2's
    `index_load`).
  - **Excluded: ENUMs.** Enum construction does NOT rc-count its payloads
    (move/taint semantics — see the enum reuse-payload note), so a precise drop
    could free a payload aliased by a live local with no rc protection. They're
    also built via variant-constructor CALLS, which `initMayAliasLive` already
    gates, so including `EnumType` would buy nothing. A fresh-payload-only
    refinement (precise-drop an enum whose constructor args are all fresh
    literals) is a possible later slice; the broader fix is the same
    consuming-param / rc-counted-payload direction the enum reuse + match-arm
    items need.

Slice 2 churned exactly one rc-count golden test (`RcAliasIncFieldAndIndex/
index_load`, a `u8[][]` whose element is aliased): the array is now released
at its last use, so the test was updated to keep it live through the
`__rc_get` check (it asserts the fully-aliased rc — that's preserved). No
other rc test moved; the differential corpus stayed green.

Two alias gates keep it sound against UNCOUNTED aliases (a precise drop frees,
so a live uncounted alias would dangle — exactly the class the broader
exit-sweep avoids via the taint analysis):
  - `flowsIntoUncountedAlias`: skip a local that flows into a pointer-result
    call / slice / if-expr / match-expr after declaration (`f(x)` may RETURN
    x with no inc — `id(x)` / a borrowed-param-returning function).
  - `initMayAliasLive`: skip a local whose INIT is such an alias producer
    (`var v3 = id(v2)` binds v3 to v2's buffer uncounted). A scalar-arg call
    (`fill(100)`) returns a FRESH value and stays eligible — the common
    builder-call win. Both gates treat an unresolved-generic (`ParamType`)
    result as possibly-pointer, since `b.exprType` doesn't instantiate generic
    call results (the bug a `id[T]`-of-a-struct differential seed caught:
    `mayAliasResult`). Counted-alias inits (`var y = x` / `x.field` / `x[i]`,
    `needsRcIncOnAlias`) are also skipped — precise-dropping them only cancels
    the alias inc (sound, but marginal and churns the rc-count tests).

Soundness otherwise rests on **zeroing the slot after the drop**: the exit
sweep (and any earlier `return`'s sweep) loads the zeroed slot and
null-guards to a no-op, so there's no double-drop on any path; and the deeper
invariant is that **a precise drop is the exact dec the exit sweep would do,
moved earlier to a point with no later use — sound exactly when the
exit-sweep dec is, since rc accuracy is unchanged**. A mis-analysis surfaces
as a null-slot read (trap / wrong value caught by the differential corpus),
never a silent UAF.

Measured win (`internal/e2e/rc_heap_bump_precise_drop_test.go`): 4
sequentially-dead size-class arrays reclaim to ~1 block instead of 4 (wasm
416 B vs the live-set 4×; natives' freelist arena makes the bump probe
insensitive but the RSS benefit is real). Value-correctness + the
aliased-into-a-container + function-return-of-arg invariants + 0 over-release
pinned on all three backends; self-host + the full differential gate stay
green.

**Remaining for true garbage-free (later slices):** (a) ENUM-box precise drops
(the array element scope is complete through slice 3, and struct + tuple boxes
shipped in slice 4; only enums remain — gated on rc-counting their payloads,
the same direction the enum reuse / match-arm items need);
(b) control-flow-AWARE placement — **SHIPPED (slice 5).** The earlier slices
conservatively skipped any local used inside a nested block; that bail is
removed. `computePreciseDrops` now lets the last use sit inside a nested
if / while / for / match and drops the local right after the whole top-level
statement that contains it — by then the local is dead on EVERY path through
that statement, so a single top-level drop + zero-slot is sound (an early
`return` on a path keeps the value live to its own exit sweep; the zeroed slot
makes the post-statement drop a no-op on paths that already returned). This
reclaims before the often-long tail after an `if`/loop — the common
peak-memory case (`var big = …; if (c) { use(big) } …long tail…`: big freed
after the `if`, not at function exit, so the tail reuses its block — measured
416 B vs 832 on wasm). Reassignment is now detected at ANY depth (a nested
`name = …` excludes the local); freeEligible + the alias gates compose
unchanged. Slightly less precise than a per-branch drop (a local dead in only
one arm still waits for the join), but captures the dominant win.

**Element-scope gate (the nested-use extension is PRIMITIVE-element only).**
The straight-line slices 1-3 drop every array element kind; the slice-5
nested-use extension is restricted to PRIMITIVE-element arrays (`i32[]` /
`f64[]` / …), whose drop is a pure buffer free with no per-element rc to
balance. A POINTER-element array (`string[]` / `struct[]` / `T[][]` /
`tuple[]`) with a nested last use falls back to the exit sweep
(`isControlFlowStmt && arrayElemIsPointer` → skip). Why: its deep drop dec's
each element, and an element aliased OUT across an early drop relies on the
per-element retain/release balancing on EVERY backend — and on arm64 two-word
heap strings that balance rides the native heap-string reclamation path the
plan still defers (item 5g). The exact shape that exposed it is the self-host
driver's `main()`: `var av: string[] = args()` with `entry = av[1]` /
`root = av[2]`, last-used at `av[2]` inside `if (av.len() >= 3)`. A blanket
nested drop precise-dropped `av` after the `if`; on the arm64-native self-host
that corrupted a still-live element alias under allocation-reuse pressure (the
args buffer reclaimed and reused while `root` still pointed into it — surfaced
as `__fern_drop_arr_str` was the ONLY drop slice-5 added across the whole
self-host, confirmed via the `FERN_DROP_DEBUG` op dump; the rc-incs were
present in the IR, so the gap is the deferred arm64 codegen path, not the
analysis). The gate keeps the nested-use win for primitive arrays without
crossing that unverified arm64 boundary; widening it to pointer-element arrays
follows item 5g (native heap-string rc verified on hardware). Tests:
`Test{X86_64,Arm64,WASM}ControlFlowDrop` — the if-branch reclaim win + the
subtle early-return, loop-then-dead, aliased-into-a-container, and the
`args-alias` (self-host pointer-element shape, exit-sweep-reclaimed, 0
over-release) cases; the `TestSelfHostArm64NativeMmcMatchesCrossHost` gate
(arm64-native self-host byte-equal to cross-host) stays green. (c) reassigned /
multi-declaration locals;
(d) dead-alias cancellation (the counted-alias inits skipped above). And the
orthogonal big Perceus lever — **general FBIP
reuse tokens** (drop-guided in-place reuse of a freed cell by an adjacent
same-shape allocation, beyond today's pattern-matched
`tryStructReuseOverwrite` / `tryEnumReuseOverwrite` / array push).

### Phase 5: Drop reuse + borrowed params

Two separate PRs:
- Drop reuse: dec immediately followed by alloc of compatible size
  reuses the storage.
- Borrowed params: per-function escape analysis identifies args
  that don't escape; callers skip the inc.

#### Phase 5 drop-reuse — detailed plan (NEXT, not started)

Date: 2026-05-31. Status: design, code-grounded; nothing emitted yet.

##### Why the naive framing undersells it (and what the real target is)

The one-line sketch above ("dec then alloc of compatible size reuses
the storage") describes a win the **freelist already captures**. Since
Phase 3 step-4, `__fern_alloc` pops its size class's LIFO freelist
before bumping (`buildAllocBody`, `runtime.go:1512`; the arm64 /
x86_64 mirrors). A `__free(base, sz)` immediately followed by an
`__fern_alloc(sz)` of the *same 16-byte class* therefore already
returns the exact block just freed — storage reuse, for free, with no
new analysis. So plain storage-reuse is **not** the prize.

The prize is **constructor reuse / FBIP** (Functional But In-Place,
the Perceus paper's headline result): when a uniquely-owned value of
type `T` is dropped and a fresh `T` of the **same box shape** is
constructed in its immediate vicinity, reuse the *same memory in place*
and skip the whole round trip the freelist still pays:

  1. the drop-walk (`__fern_rc_is_unique` gate + per-field `dec`),
  2. the `__free` push,
  3. the `__fern_alloc` pop,
  4. **the full re-initialisation of every field** — the field that
     didn't change can keep its old value instead of being re-stored
     (and, if it's an rc field, its inc+dec cancel).

The canonical shape, and the one that motivates this for Fern's
self-host + edge-handler corpus:

```fern
// list map: the Cons cell is dropped and immediately rebuilt
function map_inc(xs: List): List {
    return match (xs) {
        Cons(h, t) => Cons(h + 1, map_inc(t)),   // reuse xs's box
        Nil => Nil,
    };
}
// record update: the struct box is dropped and immediately rebuilt
function bump(p: Point): Point {
    return Point { x: p.x + 1, y: p.y };          // reuse p's box
}
```

In both, the source (`xs` / `p`) is dead after the match-scrutinee /
field reads, its box is the same size as the result's, and today we
drop-walk + free + alloc + re-store every field. FBIP turns each into
"overwrite the changed field in the existing box, hand it back" — O(1),
zero allocator traffic, and on `bump` the `y` store disappears
entirely.

##### Mechanism: a reuse token threaded drop-site → alloc-site

Perceus introduces a *reuse token* (`reuse r = drop x; … Ctor@r{…}`).
We lower the same idea as a new IR op pair so the existing per-backend
`OpAlloc` lowering is the only codegen that changes:

  - `OpDropReuse` *(replaces a free-eligible `OpDrop`+drop-glue at a
    reuse-paired dec site)* — on the last reference returns the box
    **base pointer** as a "token" (an i32) instead of pushing it to the
    freelist; on a shared/null/sentinel value returns `0`. Drop-walk of
    rc fields still happens (their refs are going away regardless); only
    the **buffer free** is withheld so the token can carry it.
  - `OpAllocReuse (token i32, tokenSize i32, size i32) → ptr` — if
    `token != 0` AND `class(tokenSize) == class(size)`, return `token`
    (in-place reuse); else **free the token** (when non-null) and fall
    through to the normal `freelist-pop-or-bump` path of `__fern_alloc`.
    One new runtime helper, `__fern_alloc_reuse(token, tokenSize, size)`,
    on each backend (**SHIPPED, slice 5a** — see below).

The token is a plain pointer on the operand stack / in a scratch local
— no header bit, no type tag. It carries `tokenSize` (the dropped
block's static allocation size) alongside it so the helper can compute
`class(tokenSize)` and compare; a bare pointer can't recover its own
size from the intrusive freelist, and without it a class mismatch would
either overflow a too-small block (unsound) or leak the dropped one.
Size-class equality mirrors the freelist's existing class arithmetic
(`(sz+15)&-16`, exact-fit classes 16…2048). A token whose class differs
is **freed back to its own class** and a fresh block allocated; a `0`
token allocates directly. So a mispaired reuse is **never unsound and
never a leak** — it degrades to today's free+alloc. This preserves the
Phase-1 invariant that "rc/reuse decisions only ever affect speed,
never correctness".

##### Where the pairing is decided (the analysis)

Reuse pairing is a *local, intra-statement* match — deliberately
narrow for the first cut, because the high-value cases are syntactically
local (a `match` arm that reconstructs its scrutinee; a field-update
`T{…}` literal whose RHS reads a dying `T`). The analysis runs in
`lowerFunc` alongside the existing `computeMovedLocals` /
`computeFreeEligible` and produces `reusePairs: map[reuseSite]allocSite`.

A drop site `D` (an owned, free-eligible local going dead) pairs with a
construction site `C` when ALL hold:

  1. **Same box shape.** `D`'s static type and `C`'s constructed type
     have equal `payloadLayout` / `structFieldLayout` box size (so the
     same size class — exact, since allocs are 16-rounded). For enums,
     the dropped variant and constructed variant must agree on box size
     (the `uniformEnumBoxSize` predicate already computes this).
  2. **`D` is free-eligible and uniquely owned at `D`.** Reuse rides on
     the existing `computeFreeEligible` taint set — a borrowed or
     escaped value is never a reuse source (same rule that lets it be
     freed at all). The runtime `rc==1` check in `OpDropReuse` is the
     backstop: a shared value yields token `0`.
  3. **`D` dominates `C` and `D`'s value is dead at `C`.** `D` is the
     scrutinee / the source struct, already read into the fields of
     `C` before `C` constructs. Reuse `computeMovedLocals`' last-
     occurrence + dominance machinery: `C` may only read `D`'s fields
     *before* the reuse, never after.
  4. **No allocation between `D` and `C`.** Keeps the token live and the
     class fresh; any intervening alloc could have popped the same
     class. (Conservative; relaxable later.)

When a pair is found: the dec at `D` lowers to `OpDropReuse` (token →
scratch local), and `C`'s `OpAlloc` becomes `OpAllocReuse(token,
size)`. Unpaired drops keep `OpDrop` + today's `__fern_box_free` /
`__fern_arr_dec` glue; unpaired allocs keep `OpAlloc`. The
move-on-return / move-on-alias inc-elisions compose unchanged (they run
first; reuse pairing reads the post-move dec sites).

##### Field-store elision (the second-order win)

Once `C` reuses `D`'s box, a field whose `C`-value is provably the same
expression as `D`'s same-offset field (`Point{ x: p.x+1, y: p.y }` →
`y` unchanged) can skip its store. Slice 5d only; slices 5a–5c reuse
the *storage* (wins 1–3 above) and always re-store every field
(simplest correct form). The store-elision needs a syntactic
"`C.field_i` is exactly `D.field_i`" check and an rc-field inc/dec
cancellation — bounded, but separable, so it ships last.

##### Slices

  - **5a — runtime helper + shim, inert. SHIPPED (x86_64 verified;
    arm64 + wasm ride CI).** `__fern_alloc_reuse(token, tokenSize,
    size)` on all three backends (wasm `buildAllocReuseBody` calling
    `__fern_alloc` + `__free`; arm64 `emitAllocReuseRuntime` and the
    x86_64 mirror — each a small prologue that tail-calls
    `__fern_alloc`). No pairing emitted yet (the dedicated `OpDropReuse`
    / `OpAllocReuse` IR ops arrive with the pairing in 5b); the helper
    is exposed as the `__alloc_reuse(token, tokenSize, size)` builtin
    shim (checker sig, native call-name map + use-flag, wasm `needs()`
    dep) so the runtime branches are testable in isolation. Tests
    (`Test{X86_64,Arm64,WASM}AllocReuse`, flag-on): `sameClass`
    (in-place reuse — `b == a`), `nullToken` (degrades to a fresh
    distinct alloc — `b != 0 && b != a`), and `mismatch` (token freed +
    fresh alloc — `b != a` — and the freed token reappears from its
    class's freelist on the next same-class `__alloc` — `c == a`,
    proving slow-not-wrong without a leak).
  - **5b — struct field-update reuse. SHIPPED (x86_64 verified; arm64
    + wasm ride CI).** A self-overwrite `p = T{ ... }` of an OWNED,
    all-i32-scalar struct local reuses p's box in place when uniquely
    owned. Lowered in `b.tryStructReuseOverwrite` (hooked at the top of
    `b.assign`'s Ident case, replacing the normal expr + dec-on-
    overwrite): evaluate every field into an i32 temp first (so a field
    reading p — `x: p.x + 1`, or a swap `a: p.b, b: p.a` — sees the old
    value before the box is overwritten), then
    `token = __fern_rc_is_unique(old) ? base(old) : 0` (the aliased /
    null / sentinel branch dec's old and yields 0), then
    `__alloc_reuse(token, size+hdr, size+hdr)`, rc=1, store temps,
    store data ptr. Two gates make it sound: **freeEligible** (OWNED,
    never a borrowed param — a borrow can be rc==1 while the caller
    still holds it) and the **runtime is_unique** check (an aliased p
    copies, leaving the alias intact). Originally restricted to i32-class scalar
    fields; pointer fields joined in 5c, and wide/float scalars
    (i64/u64/f32/f64) joined in the #4356 field-kind widening — the
    reuse temps now carry a scratchType stamp so the backends size
    them width-correctly. Only two-word strings remain excluded. Gated on `ast.RcFreeEnabled`, so the flag-off
    arena stays byte-identical. Tests: IR-level `TestStructReuse*`
    (fires for the eligible shape incl. wide-scalar (#4356); skips
    borrowed-param / string-field) + e2e `Test{X86_64,Arm64,WASM}StructReuse` churn /
    aliased / swap (value-correct + 0 over-releases). The
    `FixturesFreeMatchesNoFree` differential gate already asserts
    reuse-on == reuse-off byte-identical (reuse rides `RcFreeEnabled`).
  - **5c — pointer-field struct reuse. SHIPPED (x86_64 verified; arm64
    + wasm ride CI).** Widens 5b's self-overwrite reuse from all-scalar
    structs to structs with single-word rc-tracked pointer fields
    (array / struct / Map / enum / closure / tuple — `arrElemIsRcTracked`;
    **strings still excluded**, two-word on wasm / boxed on arm64, and
    wide/float scalars still excluded — single-word i32/pointer temps).
    `structReuseEligible` replaces the all-scalar gate. Per-field rc:
    each new pointer value is retained on eval (`emitAliasInc`, as
    normal `StructLit`); on the **reuse branch only** (gated on the i32
    `is_unique` result, not the raw token — backend-safe truthiness) the
    box's OLD pointer-field values are **flat-dec'd** before the new
    ones overwrite them. A carried-over field (`items: p.items`)
    balances (eval-inc cancels the dec-old → rc unchanged); a replaced
    field releases its old reference (flat dec — leak-but-never-UAF; a
    freeing dec is a follow-up). Tests: IR `TestStructReuseFiresForPointerField`
    / `…SkipsStringField` + e2e `Test{X86_64,Arm64,WASM}StructReuse`
    `ptr_carried` (200 reuses, array carried — 0 over-release or it
    corrupts), `ptr_aliased` (alias declines reuse; field shared
    correctly), `ptr_replaced` (old released each iter). Differential
    gate + rc-correctness corpus + self-host VM stay green free-on.
    Still on the original plan: enum/Cons-cell reuse (a `match`-arm hook
    + tag-guarded payload release) and field-store elision.
  - **5d-i — field-store elision. SHIPPED (all three backends).** On the
    struct self-overwrite reuse path, a field carried over UNCHANGED
    (`f: p.f` — `fieldCarriedFrom`) keeps its value in the reused box, so
    its store + retain (`emitAliasInc`) + old-value release
    (`emitFieldDropOnStack`) are ALL elided on the reuse branch (the box
    already holds it, rc unchanged). They're emitted only on the
    FRESH-alloc branch (gated `reused == 0` — the `OpNot` guard), where a
    new box needs its own copy + reference. This is the dominant case of
    Fern's record-update idiom: E048 forbids field assignment, so an update
    is written `p = T{ changed: ..., rest: p.rest, ... }` — most fields are
    carried, so most stores+inc/decs vanish on reuse. Sound: a carried field
    is never released (its reference stays with the box); a swap
    (`x: p.y, y: p.x`) changes every field so nothing is carried/elided.
    Tests: IR `TestFieldStoreElisionFires*/Skips*` (the guard appears iff a
    field is carried) + e2e `Test{X86_64,Arm64,WASM}FieldStoreElision`
    (carried POINTER field survives 300 reuses untouched; aliased struct
    still COWs; 0 over-releases). Differential + self-host green.
  - **5d-ii — enum/Cons-cell match-arm reuse (WON'T-DO under the current
    borrow model — see finding).** Pairs a dropped enum scrutinee in a
    `match` arm with a same-box-size constructor in that arm (`map_inc`
    shape). **Finding (this session):** Perceus's headline FBIP win —
    `map(xs)` reusing cons cells in a recursive traversal — requires `xs`
    to be an OWNED (consuming) parameter so the callee can repurpose its
    cells. Fern's borrow model (Phase 2d) makes ALL params BORROWED (the
    caller still holds them), so a recursive `map`'s scrutinee is never
    `freeEligible` and reuse cannot fire there. The only shapes left are
    OWNED-LOCAL match-and-rebuild (`c = match (c) { … }`), which largely
    overlap the already-shipped self-overwrite reuse — so the big,
    self-host-risky match-lowering surgery buys little. Unlocking the
    real win needs an owned/consuming-parameter mode (a `move`/`own` param
    annotation or escape-inferred ownership), which is a language-level
    change tracked separately from RC-Perceus. Deferred deliberately —
    **now being unlocked**, see the `own`-parameter slices below.

##### Owned / consuming parameters (`own`) — the gate to recursive-traversal FBIP

The single feature standing between Fern's local Perceus optimisations
(reuse token, self-overwrite reuse, field-store elision — all SHIPPED) and
Perceus's *headline* win (a recursive `map`/`filter`/tree-update reusing the
structure's cells in place, as in Koka and Lean 4) is an **owned parameter
mode**. All Fern params are borrowed (Phase 2d), so a `match`-scrutinee
parameter is never `freeEligible` and the cons-cell reuse (5d-ii) can't fire.
`own` lets the caller transfer ownership so the callee can consume / reclaim /
reuse the argument. Sliced for risk:

  - **Slice A — `own` syntax + affine use-after-move checker. SHIPPED (no
    codegen).** `own` is a CONTEXTUAL keyword before a param name
    (`function map_inc(own xs: List)`); `ast.Param.Own` carries it (struct
    fields / borrowed params stay false; a param literally named `own` is
    unaffected — disambiguated by the token after it). The checker's
    `checkOwnedParams` (run from `checkFunction` after `checkBlock`) enforces
    the affine discipline: an owned param is **consumed at most once on every
    path**; a use after consume is **E050**. Consume = a WHOLE-VALUE use of the
    bare param (matched, returned, passed as a call arg, bound to a var, stored
    into a literal); borrow = a PROJECTION (`x.field` / `x[i]` / method receiver
    `x.m()`, recognised post-rewrite via `Call.Method`). The walk is
    flow-sensitive and divergence-aware (a `return`/`break`/`continue` branch's
    consume doesn't constrain the fall-through; a consume inside a loop body is
    flagged since a later iteration would use-after-move). The consume/borrow
    classification is deliberately FORWARD-COMPATIBLE: `f(x)` counts as a
    consume now, so code that would become a use-after-move once Slice B lowers
    `own` args as moves is rejected up front. No runtime change — owned params
    still lower as borrowed; this only establishes the invariant the transfer +
    reuse slices rely on. Tests: `internal/checker/owned_params_test.go` (11
    cases — consume-after-call / match / loop / non-diverging-branch FIRE;
    borrow* / diverging-branches / borrow-only / method-receiver are OK; the
    contextual-keyword disambiguation both ways) + the `E050` explanation file.
  - **Slice B — call-site ownership guard (E051). SHIPPED (no codegen).** The
    static half of ownership transfer: an argument passed to an `own` parameter
    must be a value the caller can TRANSFER — a fresh construction (struct /
    tuple / array / map literal, string concat, variant-constructor call) or
    another `own` parameter of the calling function. A borrowed value (borrowed
    param, field / index read, plain local, non-fresh call result) is E051.
    `checkOwnedParams` now also runs for callers that have no `own` params of
    their own (gated on the program declaring ANY owned-param function, via
    `c.ownFuncs` — a name→per-param-`own`-flags map built before body checking);
    `isOwnedExpr` is the conservative ownership classifier and `guardCallArgs`
    the per-call check (plain same-module callees; method / mangled callees are
    skipped for now). Still NO codegen — owned params lower as borrowed; this
    pins the invariant the transfer slice needs (you can't hand off something
    you don't own). Tests: `owned_params_test.go` E051 cases (borrowed-param /
    field-read / plain-local args FIRE; construction / own-param / variant-call
    args OK) + the `E051` explanation file. Full checker + parser + non-e2e +
    e2e suites green (no behaviour change — `ownFuncs` is empty for every
    program that doesn't use `own`).
  - **Slice B-codegen — ownership transfer. SHIPPED (gated on `own`).** The
    callee reclaims its `own` params; the caller transfers ownership instead of
    dropping. Four coordinated changes, all gated on the program declaring any
    `own` function (`checker.Info.OwnFuncs`, exposed from the checker) so non-`own`
    code is byte-identical:
      1. `computeFreeEligible`: `own` params are no longer borrow-tainted, AND —
         crucially — added to the eligibility result (params aren't in
         `info.Locals`, so the normal elig loop skips them; the missing elig
         entry, not the taint, was why a first attempt leaked).
      2. The exit sweep gained an `own`-param pass (after the locals pass), so an
         owned param is reclaimed at the callee's exit like an owned local; a
         moved one (passed onward) is in `seen` and skipped.
      3. `computeMovedLocals`: move-on-call — an `own` arg (this function's owned
         param) passed at its last use to an `own` parameter is consumed by the
         callee, so its caller-side drop is suppressed (`moved`; no inc to elide).
      4. Stage-(b) post-call owned-temp reclaim is suppressed at `own`-arg
         positions (the callee, not the caller, frees a fresh temp passed there).
    The chain `[…] → relay(own) → consume(own)` frees the array exactly once at
    `consume`. Tests: e2e `Test{X86_64,Arm64,WASM}OwnTransfer` (`fresh` temp
    transfer, `chain` two-hop move-on-call — value-correct + 0 over-release) + a
    wasm heap-bump bound (the transferred array is freed each turn, N=5000 ==
    N=50000) + the `own_transfer` corpus fixture (free-on == free-off AND reuse-on
    == reuse-off byte-identical, interp oracle). Full e2e (differential corpus +
    both self-host gates) + non-e2e green — and non-`own` codegen is unchanged
    (`OwnFuncs` empty ⇒ every branch is a no-op). Still missing for the recursive
    `map`: owned MATCH-BINDINGS (a pointer payload of an owned scrutinee is itself
    owned) — pairs with Slice C.
  - **Slice C1 — consuming match (owned bindings + box reclaim). SHIPPED (all
    three backends) — THE HEADLINE PERCEUS WIN.** A recursive traversal over an
    `own` parameter (`map`/`filter`/`length`) now reclaims the structure's cells
    as it goes — zero-leak FBIP, Koka/Lean parity for the canonical shape:
    ```fern
    function map_inc(own xs: List): List {
        match (xs) {
            Cons(h, t) => { return Cons(h + 1, map_inc(t)); },  // t owned; xs's cell freed
            Nil => { return Nil; },
        }
    }
    ```
    Two coordinated parts:
      + **Checker** — a pointer-typed binding of an OWNED scrutinee is itself
        owned for the arm's scope (`checkOwnedParams`' Match case adds it to the
        `owned` set), so `map_inc(t)` passes the E051 guard and `t` is affine-
        tracked. `isOwnedExpr` also now accepts a call to a function with only
        scalar params + a pointer result (it must construct fresh — `build(5)`),
        so a freshly-built list flows into an `own` param.
      + **IR** — a consuming match (`ownParamEnumScrutinee`: the scrutinee is a
        bare `own` param, uniform-droppable enum) MOVES the pointer payloads into
        the bindings (the existing no-inc load) and, after extraction in each
        matched arm (before the body / `return` — post-match code is dead for a
        traversal), frees the box SHALLOW (`emitConsumingMatchBoxFree`: box
        buffer only, NO per-payload deep-drop, is_unique-gated). The scrutinee is
        marked moved (`computeMovedLocals`) so the exit sweep doesn't also
        deep-drop it. The freed cell is recycled by the freelist into the arm's
        constructor — so it's already near-zero-ALLOC (the bump high-water is
        FLAT across N), with Slice C2 (explicit cons-cell reuse) the only thing
        left for true zero-alloc. Gated on `own` (`OwnFuncs`) throughout, so
        non-`own` matches are byte-identical. (A subtle bug found + fixed mid-
        implementation: the first box-free passed `data-8`/`size+8` and dropped
        no result — `__fern_box_free(data, size)` already does base = data-8 /
        size+8 and RETURNS data; the double-subtract corrupted the freelist →
        native crash, caught immediately by the value test.)
        Tests: e2e `Test{X86_64,Arm64,WASM}OwnConsumingMatch` (200-iter
        build→map→sum, value-correct + `__rc_underflow_count()==0` + a wasm
        heap-bump bound, FLAT across N=2000/20000) + the `own_consuming_match`
        corpus fixture (free-on==free-off AND reuse-on==reuse-off byte-identical,
        interp oracle). Full e2e (differential corpus + both self-host gates) +
        non-e2e green.
  - **Slice C2 — explicit cons-cell reuse (NEXT, optional).** Pair the consumed
    scrutinee box with the arm's same-box-class constructor and thread the reuse
    token (slices 5e-*) so the cell is reused IN PLACE — skipping the
    free→freelist→alloc round trip C1 already recycles through. True zero-alloc;
    a peephole on top of the now-working C1.
  - **Slice D (optional, separate) — TRMC** (tail-recursion-modulo-cons /
    constructor contexts), Koka's transform turning non-tail constructor
    recursion into in-place tail loops. Orthogonal and larger.
  - **5e — general reuse token across DIFFERENT locals (struct, all-scalar).
    SHIPPED (all three backends).** The first cut of the *general* FBIP win:
    a dead, owned, all-i32-scalar struct local `D` is paired with a LATER
    same-type construction `C` (a DIFFERENT local) in the same block, so `C`
    reuses `D`'s box in place — the Perceus reuse token threaded `D`'s drop →
    `C`'s alloc, beyond the self-overwrite `tryStructReuseOverwrite` (`D == C`).
    `computeReuseSources` (run in `lowerFunc` beside `computeMovedLocals` /
    `computeFreeEligible`) walks EVERY block (the body + each loop / if arm —
    the loop body is the high-value case: a per-iteration `var a = T{…}; …;
    var b = T{…}` reuses `a`'s box for `b` each turn) and pairs `C`'s
    `StructLit` with a `D` that is: same all-scalar struct type, a `var`
    declared earlier in the block, never reassigned, name-unique, `freeEligible`
    (OWNED), and DEAD from `C` onward in the block. The StructLit lowering, when
    `reuseSources[sl]` is set, emits `token = is_unique(D) ? base(D) : 0` (the
    shared/null branch dec's `D` so its alias keeps the box and `__alloc_reuse`
    falls through to a fresh alloc), zeroes `D`'s slot (consumed — so the exit
    sweep and any non-`C` path never double-release), and `base = __alloc_reuse
    (token, size+hdr, size+hdr)` in place of the bump alloc. `reuseConsumed[D]`
    excludes `D` from `computePreciseDrops` (the reuse subsumes its drop). Two
    gates make it sound, same as 5b: **freeEligible** (never a borrowed param)
    + the **runtime is_unique** check (a shared `D` copies). All-scalar in the
    first cut; the pointer-field widening is slice 5e-ii below.
    Tests: IR `TestGeneralReuse{FiresForDeadLocal,FiresInLoopBody,SkipsLiveSource,
    SkipsSourceReadInConstruction}` + e2e `Test{X86_64,Arm64,WASM}GeneralReuse`
    (`churn` 300-iter value/over-release, `aliased` runtime-decline soundness)
    + a wasm heap-bump win (a reused chain holds fewer live boxes than the
    simultaneously-live control). Full e2e (differential corpus + both
    self-host gates) + non-e2e suites green.
  - **5e-ii — general reuse, single-word POINTER-field structs. SHIPPED (all
    three backends).** Widens 5e from all-scalar `D` to `structReuseEligible`
    `D` (fields may be array / struct / Map / enum / closure / tuple — strings
    and wide/float scalars still excluded, same gate as the self-overwrite 5c
    path). Because `D` is DEAD at `C`, `C` never carries a field from `D`, so
    EVERY one of `D`'s old pointer-field references is released (deep freeing
    drop, `emitFieldDropOnStack`) on the reuse branch — gated on the is_unique
    result, before `C`'s stores overwrite them — and each of `C`'s new pointer
    fields is retained on eval as normal StructLit construction. On the decline
    branch the box is fresh, so nothing is released and the alias keeps `D`'s
    box + fields. Simpler than the self-overwrite 5c/5f case (no carried-field
    elision — `D` dead means all fields replaced). Tests: IR
    `TestGeneralReuse{FiresForPointerField,FiresForWideScalarField,FiresForFloatField,SkipsStringField}`
    + e2e `…GeneralReuse` {`ptr_churn` (200-iter array-field reuse, old array
    freed each turn, 0 over-release), `ptr_aliased` (runtime is_unique decline
    — `keep` retains `D`'s box + array, `b` fresh-allocs)}. Full e2e
    (differential corpus + both self-host gates) + non-e2e suites green.
  - **5e-iii — cross-TYPE reuse by box-class equality. SHIPPED (all three
    backends).** Drops the same-struct-name requirement: `D` and `C` may be
    DIFFERENT struct types when their allocations (`data + rc header`) fall in
    the SAME freelist class — `(alloc+15)&-16`, within the exact-fit ≤ 2048
    range — mirroring `__alloc_reuse`'s runtime class check. (Same-name pairs
    still match at any size, reusing `D`'s box as itself.) The lowering threads
    `D`'s OWN layout: `tokenSize = D_alloc` (so a runtime class mismatch frees
    `D`'s block to ITS class — never the wrong list), and the old-pointer-field
    release walks `D`'s offsets / field types (`reuseSrcSd` / `reuseSrcOffs`),
    while `C`'s stores use `C`'s layout. The two layouts are independent raw
    bytes in a block ≥ both sizes: `D`'s pointer fields are released (D's
    offsets), `C` fully initialises its own fields (C's offsets, every field of
    a non-update StructLit) — no overlap hazard since the release precedes the
    stores. Tests: IR `TestGeneralReuse{FiresCrossTypeSameClass,
    SkipsCrossTypeDifferentClass,FiresCrossTypePointerField}` + e2e
    `…GeneralReuse` {`crosstype_churn` (300-iter Point→Pair, value-correct at
    C's offsets), `crosstype_ptr` (200-iter Holder→Bag with array fields, D's
    old array freed at D's offset, 0 over-release)}. Full e2e (differential
    corpus + both self-host gates) + non-e2e suites green.
  - **5e-iv — TUPLE sources. SHIPPED (all three backends).** Extends the
    general reuse to tuple boxes: a dead, owned tuple local `D` is reused for a
    later TupleLit construction `C` of the SAME freelist class. Tuples are
    rc-header boxes with an element layout exactly like structs, so the
    machinery is shared — the token select + D-slot zero + `__alloc_reuse`
    (`emitReuseToken`) and the old-pointer-element release (`emitReuseOldFieldDrops`,
    walking `D`'s own layout via `reuseSourceLayout`, which now returns parallel
    (offset, type) slices for a struct OR tuple `D`) are factored helpers used by
    BOTH the StructLit and TupleLit hooks (so the proven struct path and the new
    tuple path are byte-identical, guarded by the existing struct tests). Pairing
    is restricted to the same KIND (struct↔struct or tuple↔tuple) — a tuple `D`
    never pairs with a struct `C` even at equal class; eligibility is the tuple
    analogue `tupleReuseEligible` (i32-scalar / single-word rc-tracked element;
    strings + wide/float excluded). Tests: IR `TestGeneralReuse{FiresForTuple,
    FiresForTuplePointerElem,SkipsTupleToStructKindMismatch}` + e2e
    `…GeneralReuse` {`tuple_churn` (300-iter (i32,i32) reuse, value-correct at
    element offsets), `tuple_ptr` (200-iter (i32,i32[]) — D's old array freed
    each turn, 0 over-release)}. Full e2e (differential corpus + both self-host
    gates) + non-e2e suites green.
  - **5e-v — ENUM sources. SHIPPED (all three backends).** Completes the
    reuse-source box kinds (struct / tuple / enum): a dead, owned enum local `D`
    is reused for a later payload-carrying variant construction `C` of the same
    enum type. Hooked in `emitEnumNew` with the SAME factored helpers
    (`emitReuseToken` + `emitReuseOldFieldDrops`); `reuseSourceLayout` /
    `reuseClassOf` gained an `EnumType` case backed by `enumReuseLoads`, which
    mirrors `tryEnumReuseOverwrite`'s gate exactly — a uniform box size and
    EITHER uniform-droppable with no string payload (the rc-pointer loads to
    free) OR scalar-only (nothing to free). The old-payload free walks `D`'s
    **uniform drop loads** (`uniformEnumDropLoads`), whose offsets are
    variant-INDEPENDENT, so no runtime tag guard is needed; soundness rides the
    same basis as the self-overwrite enum path — `freeEligible[D]` (via
    `rhsTainted` through variant-constructor args) guarantees `D`'s payloads
    alias nothing live, so freeing the old one reclaims the genuine last
    reference (and each drop is_unique-gates again). A sentinel `D` (payloadless
    variant at runtime) reads non-unique → declines → fresh alloc. Restricted to
    the SAME enum type for now (cross-enum same-class is a later micro-cut) and
    to the same KIND (an enum `D` never pairs with a struct/tuple `C`). Tests: IR
    `TestGeneralReuse{FiresForEnum,SkipsEnumToStructKindMismatch}` + e2e
    `…GeneralReuse` {`enum_churn` (200-iter `Wrap(i32[])`, old array freed at the
    uniform offset each turn, 0 over-release), `enum_cross_variant` (uniform
    two-variant `Bag{Keep,Swap}`, D and C differ in variant — exercises the
    variant-independent free)}. Full e2e (differential corpus + both self-host
    gates) + non-e2e suites green.
  - **5e-vi — cross-BLOCK reuse (dominance). SHIPPED (all three backends).**
    Relaxes the same-block constraint: a function-top-level local `D` is reused
    by a construction `C` NESTED inside a later top-level statement (an if /
    loop / block arm). `D` pairs with `C` when `D` is dead from that enclosing
    top-level statement onward across the WHOLE body — `deadFrom` over
    `body.Stmts` conservatively rejects ANY use after `k` on any path (a sibling
    branch, the rest of `C`'s block, or a post-merge use), so reusing `D`'s box
    on the `C`-path and zeroing its slot can never strand a live read; the
    not-taken path leaves `D`'s slot intact for the exit sweep (so neither path
    double-frees). The args-alias hazard the plan flagged is excluded
    STRUCTURALLY: reuse requires `freeEligible[D]` (a `D` whose field/element
    aliases a live local is tainted out), and arrays — the `string[]` args
    shape — are never reuse sources. The pairing was factored into a shared
    `attemptPair` used by both the (existing) same-block pass and the new
    cross-block pass; `D` selection is now **deterministic** (smallest decl
    index, tie-broken by name) — a latent non-determinism fix, since Go map
    iteration is per-process randomised and would otherwise make codegen
    non-reproducible when two `D`s qualify (fatal for the byte-equal self-host
    gate). Restricted to a function-top-level `D` (a loop-body `D` with a
    deeper-nested `C` is a further cut). Tests: IR
    `TestGeneralReuse{FiresCrossBlock,SkipsCrossBlockUsedAfter,
    SkipsCrossBlockSiblingUse}` + e2e `…GeneralReuse` {`crossblock_scalar` and
    `crossblock_ptr` — each calls a helper with the branch BOTH taken (reuse
    fires, old payload freed at `C`) and not-taken (`D` exit-swept), value-correct
    with 0 over-release, the adversarial double-free check}. Full e2e
    (differential corpus + both self-host gates) + non-e2e suites green.
  - **5e-vii — cross-block reuse in ANY block (loop bodies). SHIPPED (all three
    backends).** Generalises 5e-vi from a function-top-level `D` to a
    block-top-level `D` in EVERY block, so the dominant shape — a loop-body
    `var a = …` reused by a construction nested in an `if` inside the loop —
    fires every iteration (`a` is block-scoped, re-declared and reinit-dropped
    each turn; the reuse zeroes its slot, the reinit drop null-no-ops, the
    not-taken path reinit-drops the live box — no double-free, no leak). Blocks
    are visited **descendant-before-ancestor** (reversed pre-order) so a nested
    `C` pairs with the INNERMOST eligible `D` (the per-iteration reuse), and
    cross-block reuse composes across levels (a nested `c` ← loop `m`, and the
    now-unclaimed `m` ← outer `a`). Same soundness gates (`deadFrom` over the
    block, `freeEligible[D]`, runtime is_unique). Tests: IR
    `TestGeneralReuse{FiresCrossBlockInLoop,CrossBlockComposesLevels}` + e2e
    `…GeneralReuse` `crossblock_loop` (200-iter loop-body `Holder` reused by a
    nested `if` construction on EVEN iterations only — odd iterations exercise
    the not-taken reinit-drop, with a pointer field freed each way; value-correct,
    0 over-release). Full e2e (differential corpus + both self-host gates) +
    non-e2e suites green.
    With this the general FBIP reuse token covers all heap box kinds (struct /
    tuple / enum), scalar + single-word pointer fields, cross-type box-class
    equality, and cross-block dominance (function- and loop-level).

##### Test + safety contract (same bar as Phases 1–3)

  - Every slice ships the per-backend unit test + at least one
    `rc_correctness` corpus entry that **forces reuse to fire and reads
    the value back** (a churn loop whose result is only correct if every
    reuse wrote the right block), folded with `__rc_underflow_count()`.
  - The `Test{X86_64,Arm64,WASM}FixturesFreeMatchesNoFree` differential
    gate already asserts free-on == free-off byte-identical; reuse is a
    third axis. **DONE:** `ast.RcReuseEnabled` (default on) gates all three
    reuse entry points (`computeReuseSources`, `tryStructReuseOverwrite`,
    `tryEnumReuseOverwrite`); flipping it off only disables the optimisation
    (every site falls back to a fresh alloc + the normal drop), and the
    `Test{X86_64,Arm64,WASM}ReuseMatchesNoReuse` gate runs the whole
    `testdata/cases` corpus asserting reuse-on == reuse-off byte-identical
    output + exit. IR `TestGeneralReuseDisabledByFlag` pins zero
    `__alloc_reuse` with the flag off.
  - **`RcFreeDebug` extension:** `OpDropReuse` must poison-and-quarantine
    exactly like the free path when reuse does *not* fire (token
    returned but the paired alloc took a different class), so the UAF
    detector still covers the withheld-free path. When reuse *does* fire,
    the block is neither freed nor poisoned — it's live in its new
    identity; the detector's invariant (no inc/dec touches a poisoned
    block) is unchanged.

##### Why this is the right next slice

  - It is the **defining** missing Perceus feature — "Reuse" is in the
    paper's title; without it this is "RC + two peephole elisions", not
    Perceus.
  - It is **self-contained and low-risk**: one IR op pair, one runtime
    helper per backend, an analysis that *reuses* the existing
    `computeFreeEligible` taint + `computeMovedLocals` dominance
    machinery, and a runtime class-equality backstop that makes every
    mispairing slow-not-wrong.
  - It targets the exact shapes the project cares about: the self-host
    parser/asm `acc.push` + record-rebuild loops and the edge-handler
    request/response struct churn.
  - The two alternatives are worse first picks: heap-string rc is
    **blocked on the in-flight SSO native flip**
    (`docs/SSO-NATIVE-FLIP-STATUS.md`), and full map key/value
    reclamation **reopens the borrow ⇄ free over-release tension**
    (get-results return uncounted) that Phase 3 spent its hardest weeks
    closing.

##### Reference anchors (read before implementing)

  - `internal/codegen/wasmbin/runtime.go:1481` `buildAllocBody` (the
    freelist-pop-or-bump body `__fern_alloc_reuse` fronts);
    `:1608` `buildFreeBody`.
  - `internal/ir/ir.go:1696` `computeFreeEligible` (taint source for
    rule 2); `:2036` `computeMovedLocals` (dominance for rule 3);
    `:1984` `emitRcDecLocalsAtExit` + `:2107`
    `emitRcDecLocalsAtExitExcept` (where `OpDropReuse` replaces the
    paired dec); `:2382` the `__fern_box_free` struct-drop tail and
    `:4246` `emitEnumNew` (the alloc sites `OpAllocReuse` replaces).
  - `internal/ir/move_on_return_test.go` — template for the
    analysis-level tests.

#### Completion ordering (decided 2026-06-02)

Driving the plan to 100%. Ordering by dependency depth and risk, revised
as findings land:

1. **5d** — enum/Cons-cell reuse in `match` arms + field-store elision.
   Ready, no external deps, completes the reuse family.
2. **5h** — block-scoped drops for loop-body locals (the real
   unbounded-leak fix). **SHIPPED** — see §5h. The over-release hazards
   the risk analysis flagged are all closed by gating dec-on-reinit on
   `freeEligible` + `localNameUnique` + `!movedLocals` and skipping
   closures/tuples; verified on x86_64 + wasm + arm64 (qemu) through the
   free-on/free-off differential gate.
3. **5f** — build the alias analysis, then the freeing-dec for replaced
   pointer fields.
4. **SSO native flip** — arm64 §1→§9, then mirror on x86_64 (the deep
   prerequisite for 5g). Tracked in `docs/SSO-NATIVE-FLIP-STATUS.md`.
5. **5g** — heap-string rc (only after the SSO flip is green on both
   native backends).
6. **Phase 6** — measurements, self-host-through-itself, retire
   `strbuf_*` if reuse makes it redundant. (Evaluated: NOT redundant —
   keep it; see the Phase-6 open-items resolution + § "strbuf_* becomes a
   more specific optimisation".)

**Verification constraint (this environment).** Local toolchain is
x86_64-native + `wasmtime` (+ `wasm-tools`) only — **no `qemu-aarch64`
and no aarch64 cross-gcc**. Reclaim/free changes must therefore land
x86_64-local + wasm-local verified (full e2e + the free-on/free-off
differential gate + `__rc_underflow_count() == 0`), with **arm64 riding
CI** — the same x86_64-proven / arm64-rides-CI split #1724 established.
The arm64 differential gate is the non-negotiable check there precisely
because qemu user-mode masks the over-release class these changes risk;
when arm64 CI flags one, fall back to the safe-leak path on arm64 (as
the overwrite-string slice did) until it's verified on hardware.

#### Remaining frontier (code-grounded design + risk)

As of this writing the drop-reuse + pair-cancellation work is merged and
verified: drop-reuse for structs (5a/5b/5c) and **enums (5e, below)**,
plus the full pair-cancellation move family — move-on-return,
move-on-alias, move-on-construction (struct / array / tuple / closure
containers), and move-on-destructure. An audit of all nine
`b.emitAliasInc` call sites confirms every genuine last-use move
opportunity is now gated on `b.moveSites`; the only ungated sites are the
`return`-of-field/index path (not a local — nothing to move) and the
call-arg path (already inc-free under the Phase 2d borrow model).

5h (loop-body local drops) is now **SHIPPED** (see §5h). That leaves
**two** items, each deferred for a concrete, durable reason: 5f needs an
alias analysis the borrow model postponed, and 5g is hard-blocked on the
in-progress SSO native flip. Neither is a "just do it" slice — see below.

##### 5e — enum self-overwrite reuse (DONE)

`c = Variant(...)` reusing `c`'s box in place when uniquely owned —
`tryEnumReuseOverwrite` in `ir.go`, wired into `assign` after the struct
hook. Investigation overturned this doc's original "high risk" framing:

  - **The feared variant-dependent old-payload release does NOT exist.**
    The original worry was that the reused box might hold a different
    variant, needing `enumVariantDropPlan` tag-dispatch to release old
    payloads (the over-release surface). But that release isn't needed
    at all: enum construction (`emitEnumNew`) does **not** inc its
    payloads, and the baseline overwrite-dec for a non-array enum target
    is a flat `__fern_rc_dec` that leaks the old payloads. So the
    rc-correct reuse mirrors that exactly — **no arg inc, no old-payload
    release** — making it a pure alloc-elision that is provably
    rc-neutral vs. baseline, with *zero* over-release surface. This is
    the mirror image of the struct case: StructLit construction *incs*
    fields, so struct reuse *must* release old fields to balance; enum
    construction does neither, so enum reuse does neither.
  - **Gated on `uniformEnumBoxSize`** (every payload-carrying variant
    shares one box size ⇒ the constructed variant always fits the old
    box regardless of which variant it holds — cross-variant reuse is
    sound), runtime `is_unique`, and `freeEligible` (the borrow-model
    UAF guard). The `uniformEnumDropLoads` gate the original plan
    proposed is unnecessary precisely because no payloads are released.
  - **Eligibility boundary:** fires for free-eligible enum locals —
    pointer-payload enums built from non-literal args (the
    allocation-heavy case). Scalar enums built from literal args
    (`Fwd(0)`) are conservatively tainted by `rhsTainted`'s default and
    aren't eligible; they leak their box under the baseline too, so not
    reusing them loses nothing (covered by
    `TestEnumReuseSkipsLiteralScalar`). Lifting that would mean treating
    variant-constructor calls as fresh-owned producers in `rhsTainted` +
    escape-tainting their args in the eligibility walk — a taint-analysis
    change with broad blast radius, deliberately left out.
  - Tests: `internal/ir/enum_reuse_test.go` (6 cases) +
    `internal/e2e/testdata/cases/enum_reuse_churn` (cross-variant box
    reuse, array payloads, green on all four backends through both
    free-on/free-off differential gates).

##### 5f — freeing-dec for replaced pointer fields (SHIPPED + sound; the old "deferred" framing was wrong)

**SHIPPED and sound.** `tryStructReuseOverwrite` step 4 deep-drops the
box's OLD pointer-field values (`emitFieldDropOnStack` — a FREEING drop:
`__fern_arr_dec` frees the array buffer, `__drop_struct_`/`__drop_enum_`
free nested boxes) before the new ones overwrite them. A self-overwrite
`p = Box{ items: [..], n: .. }` therefore reclaims the replaced `items`
buffer every iteration (`Test{X86_64,Arm64,WASM}ReplacedFieldReclaim` —
flat high-water across 10x N on natives + wasm; the array-buffer leak the
prior flat `__fern_rc_dec` left is gone).

**Why the original "deferred — needs alias analysis" note was wrong.** The
note feared a NEW field value aliasing the old buffer *uncounted* (e.g.
`items: g(p.items)` via a `*ast.Call` that returns a view, which
`needsRcIncOnAlias` doesn't inc) — freeing the old buffer would then UAF
the new value. But the struct case is sound *for free*, because **StructLit
construction inc's its fields globally** (the baseline non-reuse path does
too). So the old buffer's rc reflects every live reference, including one
read in the self-overwrite RHS: the freeing drop is rc-protected and only
reclaims the genuine last reference (the field's own is_unique gate dec's a
shared buffer instead of freeing it). Verified against exactly the shapes
the note feared — `items: ident(p.items)` (Call returning the old field), a
branch-returning helper, and an aliased local kept live across the
overwrite — each value-correct with 0 over-releases on all three backends,
including an interleaved-allocation stress that would corrupt a
wrongly-freed buffer.

**The real open analog is the ENUM reuse path** (`tryEnumReuseOverwrite`),
which is NOT sound for a freeing drop and so still flat-leaks its replaced
payload (`e = Wrap([..])` in a loop: 1616 → 160016 B on wasm, unbounded).
The difference is exactly the inc: enum construction does NOT rc-count
payloads (it uses move/taint semantics), so a mid-loop freeing drop of the
old payload has no rc protection against a live uncounted alias — the UAF
class the struct path avoids via the construction inc. Closing it cleanly
means making enum payloads rc-counted like struct fields (inc on every
construct, free on every drop — most drop sites already free for uniform
enums), a wide but well-defined change tracked as the enum-reuse-payload
item under "Next Phase-6 steps".

##### 5g — heap-string rc (UNBLOCKED + working; 2026-06-03)

Was "the highest-value remaining item by memory impact," hard-blocked on
the SSO native flip. **The SSO native flip is now COMPLETE on both
backends** (see `docs/SSO-NATIVE-FLIP-STATUS.md` — green on arm64 +
x86_64, verified through the full e2e suite), so the blocker is cleared
AND native heap-string rc has effectively landed with it: a string is now
two-word on wasm + arm64 and (top-bit-tagged) on x86_64 with a uniform
rc-header home, `isOwnedRcLocal` admits `StringType` uniformly, and
**native heap strings (>15 B) reclaim to a bounded high-water and are
sound (0 over-releases) on both backends** — verified for loop var-reinit
(`var s = a + b`), reassignment-overwrite (`s = s + chunk`), and
concat-temp shapes. Bound-var short strings stay inline-SSO (no heap, so
0 bump) as before.

**Loop-reinit + owned-temp arm64 string reclaim — NOW WIRED (slice 5g
follow-up, this session).** The earlier "these comments are stale,
reclamation works" note was itself wrong for two paths: `emitOwnedSlotDrop`
(behind `emitVarReinitDropOld`, the loop-body `var s = …` reinit) and
`emitOwnedTempStackDrop` (fresh owned-string call-arg / statement temps)
genuinely STILL safe-leaked arm64 two-word heap strings — they only handled
wasm (`ptrW==4`) and x86_64 single-word, falling through for the arm64
two-word case. The EXIT SWEEP already reclaimed arm64 two-word strings
soundly (`UseTwoWordStrings(b.ptrW)` → `__fern_str_dec`), so wiring the same
branch into those two paths closes the loop-reinit / owned-temp leak —
mirroring shipped-sound code, gated identically (`freeEligible` /
`localNameUnique` / `!movedLocals`; `__fern_str_dec` is_unique-gates again).
Verified: `Test{X86_64,Arm64,WASM}LongStringReinitBounded` (a >15 B heap
string rebuilt into a loop-body `var s` each iteration — arm64 bump now holds
a bounded 96 B at N=50 and N=50000, vs the pre-fix divergence; value-correct,
0 over-release), the existing `TestArm64LoopVarReclaim/string`, and the full
arm64 e2e + self-host gates. (The arm64 two-word string TEMP-stack path in
`emitOwnedTempStackDrop` likewise now str_dec's instead of dropping both
words.) The ONLY arm64 string reclaim still deliberately safe-leaking is the
control-flow-nested precise drop of a `string[]` element (the args-alias
guard at `isControlFlowStmt && arrayElemIsPointer`), which is a soundness
boundary, not a wiring gap.

##### 5h — block-scoped drops for loop-body locals (DONE)

**Shipped** via shape (b) (zero-init + guarded dec-on-reinit), realised as
`emitVarReinitDropOld` in `ir.go`, called from the `*ast.Var` lowering
after the alias-inc and before the store. A loop-body `var row = …` now
releases the slot's previous value each iteration, so the freelist
reclaims it instead of leaking N-1 allocations (and the rc undercount no
longer pins the buffers live). The new value sits on the stack underneath
while the dec runs (net-zero load → dec → drop), so the store is
unaffected.

The three hazards the analysis below flagged are all closed by the gates:
  - **zero-init dedup / shadowing** → `localNameUnique(name)`: fire only
    for a name with exactly one `info.Locals` entry, whose single slot the
    Phase 1d-v net is guaranteed to have zeroed (NULL-guarding the first
    dec). Shadowed names keep the safe-leak.
  - **move-out over-release** → `!b.movedLocals[name]`: a var whose
    reference was moved out (all moves are top-level / last-use) is
    excluded; combined with the fact that loop-body aliases are never
    marked moved, no moved value is ever dec'd.
  - **unbalanced alias (the regression the corpus caught)** → gate the
    whole emission on `b.freeEligible[name]`, mirroring the EXIT sweep. A
    `var a1 = match (o) { _ => a0 }` aliases `a0` WITHOUT an inc (a
    matchexpr isn't an alias shape `needsRcIncOnAlias` recognises), so it
    doesn't own a reference; `freeEligible` is false for it, so dec-on-
    reinit skips it — no over-release of the shared buffer.

Dispatch is by exact declared type (`localDeclType`): owned arrays →
`__fern_arr_dec` (the O(N) buffer reclaim), struct / enum → flat
`__fern_rc_dec` (nested payloads leak, zero over-release surface, as the
baseline overwrite-dec does), owned strings → the str_dec path on
x86_64 / wasm only (arm64 deferred per 5g). **Closures and tuples are
deliberately skipped**: a dec spliced between `OpMakeClosure` and
`OpStoreLocal` breaks the defunctionalise / closure-pair-elide pattern
match (and a flat closure dec leaks captures anyway), and a tuple needs
the exit sweep's per-element deep drop. Gated on `ast.RcFreeEnabled` so
the free-off baseline is byte-identical.

Tests: `internal/ir/loop_var_drop_test.go` (dec-on-reinit fires for an
eligible loop-body array var; is skipped for a closure var) +
`internal/e2e/rc_loop_var_test.go` (array / struct / enum / string churn
loops, value-correct-only-if-reuse-sound folded with
`__rc_underflow_count()`, on x86_64 + arm64 + wasm) +
`internal/e2e/testdata/cases/loop_var_reclaim` (free-on == free-off
differential fixture). Full e2e + rc-correctness corpus + the existing
freelist / reuse suites stay green on all three backends.

The original design notes are kept below for the record.

##### 5h — original analysis (deferred: needs block-scope drop machinery)

The one item here that's a spec-vs-impl *gap* rather than a deferred
reclamation optimization. "Drop sites" above (§ *Variable goes out of
scope*) specifies block-scoped drops "at the closing `}` of the block
where the variable was declared." The lowering never emits them: the
only dec sweep is `emitRcDecLocalsAtExit` at each `return` (`ir.go`
`*ast.Return`), and the loop body's `closeScope()` in the `*ast.While` /
`*ast.For` cases is structural-only (`OpEnd` + `depth--`, no decs).
Every local is function-scoped — one slot per name, allocated once — so
a `var` *declared inside a loop body* reuses a single slot across
iterations.

Consequence: a loop-body `var row = [i, i+1]` (any fresh rc-tracked
value — array / struct / enum / tuple / closure / owned heap string)
allocates rc=1 into `row`'s slot every iteration and overwrites the
previous *without a dec*. The exit sweep dec's `row` exactly once (the
last iteration's value), so N-1 allocations leak. Safe under the bump
allocator, but — like the pre-#1724 string-reassign leak — the rc
undercount keeps the freelist from reclaiming them, so a hot loop that
builds-and-discards a fresh container per iteration grows unbounded.

Distinct from the two already-handled overwrite shapes:
  - `x = newValue` *reassignment* dec's the old value (the assign hook —
    arrays / structs / enums / closures, plus strings on x86_64/wasm
    since #1724).
  - `nfuncs = nfuncs.push(...)` (the walked example above) is also a
    reassignment — its slot always holds a prior value, so dec-on-
    overwrite is sound.
A loop-body `var` is *re-declaration*, not reassignment, and is NOT a
quick mirror of the assign hook: the slot is **uninitialized on the
first iteration**, so a naive dec-on-overwrite at the `var`-init store
would dec garbage and UB the first time through. That's the crux of why
it needs real block-scope machinery.

Two fix shapes:
  - **(a) Per-iteration block drop.** Track the locals first-declared
    inside each loop body (a body-scoped subset of `b.info.Locals`) and
    emit `emitDec` for them just before the back-edge `brTo(loopD)` / at
    the continue-block close in the `*ast.While` / `*ast.For` cases.
    Clean — drops fire exactly where § *Variable goes out of scope* says
    — but needs the lowering to carry per-block local sets it doesn't
    today (the "architectural" part), and `emitDec` would first have to
    be hoisted out of `emitRcDecLocalsAtExitExcept` into a reusable
    method (it's a closure there today, the same blocker the tuple
    reassignment-dec hit).
  - **(b) Zero-init + guarded dec-old on var-init.** Zero every loop-
    body local slot at function entry (extend the Phase 1d-v array
    zero-init safety net to all rc-tracked loop-body locals), then dec
    the slot's old value in the `*ast.Var` path the way the assign hook
    does. The zero makes the first-iteration dec a NULL-guarded no-op.
    Smaller diff, but leaves the dec at re-declaration rather than at
    scope close — fine for `var`, which can't be read before its own
    re-init.

###### Code-grounded findings for shape (b) (2026-06-02 investigation)

Shape (b) is the smaller diff, but a *naive* "dec-old on every var-init"
is unsound. Three concrete hazards, each confirmed against the current
lowering — a correct shape (b) must gate around all three:

1. **Zero-init dedups by name; shadowed vars have distinct slots.** The
   Phase 1d-v safety net (`ir.go` ~`zeroSeen[v.Name]`) zeroes each
   rc-tracked local *once keyed by name*. But `info.Locals[fn]` holds a
   *separate `*ast.Var` entry per declaration* (checker `:3322` appends
   unconditionally), so two same-name `var x` in sibling/nested scopes
   are distinct slots sharing one name — only one gets zeroed. A
   dec-old on the un-zeroed inner slot reads garbage → UB. Verified:
   `var x=[1,2]; if(..){ var x=[3,4,5]; sink(x);} return x.len()` returns
   2 on both interp and x86_64 (distinct slots, correctly managed), so
   shadowing is real and must be excluded.
   → **Gate: only fire when the name appears exactly once in
   `info.Locals[fn]`** (single slot, guaranteed zero-init'd). This is
   provably safe regardless of the scope-remap mechanism, and the
   loop-body leak target is virtually always a unique-name `var`.

2. **Move-out + re-declaration over-release (the decisive one).** If a
   loop-body `var`'s value is *moved out* — `b.moveSites` via
   move-on-alias (`var y = row`), move-on-return, move-on-construction —
   ownership transfers and `row` is excluded from the exit dec. A
   dec-old at `row`'s next-iteration re-declaration would then release a
   value whose ownership already moved → over-release / UAF under
   free-on. Call-arg use is *safe* (borrowed, inc-free under the Phase 2d
   borrow model — `consume(row)` does not move), so the hazard is
   specifically the move-site shapes.
   → **Gate: skip dec-old if any use of the var is a move site.**
   `moveSites` is keyed by AST node, not name, so this needs either a
   name-keyed "was moved" set derived alongside `computeMovedLocals`, or
   a conservative "this var participates in no `moveSites`" precheck.
   Resolving this cleanly is the remaining design work for shape (b).

3. **Closure dec-old leaks captures (acceptable, matches reassignment).**
   `isArrayTypeOfLocal` includes `FuncType`; a flat `__fern_rc_dec` of an
   old closure box frees the env but does *not* run the per-closure
   capture-drop thunk, so captures leak. This mirrors the existing
   reassignment dec-old exactly (the `else { rc_dec }` branch), so it's
   consistent and leak-but-never-UAF — not a blocker, just noted.

The dec body itself mirrors the reassignment Ident hook for the SAME
rc-tracked set and gates (owned arrays → `__fern_arr_dec`; struct / enum
/ closure → flat `__fern_rc_dec`; owned strings on x86_64/wasm,
arm64-excluded per 5g; tuples excluded as in the overwrite path), *minus*
the self-mutation / map-COW branches — those can't arise for a var-init
RHS, since a fresh binding can never reference its own prior slot value.
Gate the whole emission on `ast.RcFreeEnabled` so the free-off baseline
is byte-identical to today (no new ops), keeping the differential gate's
free-on == free-off comparison the meaningful one.

Risk / testing parity: any heap reclaim added here MUST follow the
x86_64-native + wasm-proven / arm64-deferred split #1724 established —
native-arm64 string reclaim (and, once 5g lands, the broader path)
over-releases in ways qemu user-mode masks. A pure leak is also awkward
to assert: there is no over-*retain* counter (only `__rc_underflow_count`
for over-release), so a regression test would assert *reuse* instead — a
loop-body-`var` program whose per-iteration buffers get reclaimed+reused
to a bounded high-water mark, mirroring `TestX86_64FreelistReuse` /
`pushLoopFreeSrc` in `internal/e2e/rc_freelist_test.go`.

##### Testing-parity note

Earlier slices (through the move family) were developed x86_64-only and
rode CI for arm64/wasm. 5e was developed and verified with the full
local toolchain — arm64 via `aarch64-linux-gnu-gcc` + `qemu-aarch64`,
wasm via `wasmtime` — through both free-on/free-off differential gates
on all three, plus the full e2e suite (`ok internal/e2e ~424s`). A
future 5f (if the alias analysis lands) should clear the same bar: its
failure mode (a freed-then-reused block) is the kind the no-free arena
masks, so the free-on differential gate on every backend is the
non-negotiable check.

### Phase 6: cleanups + measurements

End-state verification: run the benchmarks, compare RSS, build
the self-host through itself, profile hot allocations, retire
the `strbuf_*` primitive if Perceus + drop-reuse make it
redundant. (Evaluated: NOT redundant — strbuf is orthogonal to rc and
faster for hot string-building, and the self-host emitters depend on it;
keep it. See the Phase-6 open-items resolution.)

**Measurement probe — `__heap_bump_bytes()` (SHIPPED).** A builtin
returning the bump allocator's high-water mark in bytes (current cursor
− region base; 0 before the first alloc). The cursor advances only on a
fresh bump, never on a freelist reuse, so it's the direct "did reclaim
happen?" metric: a reclaiming loop keeps it flat regardless of iteration
count, a leak grows it linearly. Implemented on all three backends —
natives capture the mmap base in `__fern_heap_base` and the reader
returns `__fern_heap_ptr − __fern_heap_base`; wasm records the cursor
seed at `heapBaseAddr` and returns `cursor − seed`; the interpreter
returns 0 (Go allocator, no bump cursor). This unblocks the rest of
Phase 6 (it makes RSS/allocation behaviour assertable in tests and
profilable) and retroactively pins the reclamation wins:
`internal/e2e/rc_heap_bump_test.go` asserts a build-and-discard loop's
bump growth is identical at N=50 and N=5000 on x86_64 + arm64 + wasm —
the proof that Phase 5h / push-loop reclamation holds memory bounded,
which the soundness-only tests couldn't show.

**Leak detector — `FERN_LEAKCHECK=1` (SHIPPED, #5362 slice 1).** A
compile-time build mode (`ast.LeakCheckEnabled`, the `RcReuseDropGuided`
env-flag precedent) that turns the alloc/free seam into an exact leak
counter on the native backends. `__fern_alloc` bumps
`__fern_lc_alloc_count` / `__fern_lc_alloc_bytes` after its 16-byte
rounding (covering both the freelist-pop and bump paths, but NOT the
large tier's capacity round-up); `__fern_free` — which every
reclamation site funnels through (box_free, arr_dec, map_drop,
drop_arr_ptr/str, alloc_reuse's mismatch path, the `__free` builtin) —
bumps `__fern_lc_free_count` / `__fern_lc_free_bytes` at the identical
rounding, so a block's alloc and eventual free cancel exactly. At BOTH
exit seams (the `_start` epilogue and the `exit()` builtin's
`__fern_exit`), `__fern_lc_report` writes one line to stderr, exit code
and stdout untouched:

    leakcheck: allocs=<N> frees=<M> live_bytes=<K>

with `K = alloc_bytes − free_bytes` (exact, not high-water — unlike
`__heap_bump_bytes()` it distinguishes "still reachable at exit" from
"churned through the freelist"). `__fern_alloc_reuse`'s in-place path
counts as NEITHER an alloc nor a free: its class match requires equal
rounded sizes, so the reused block's original alloc still cancels
against its eventual free and live_bytes stays exact. x86-64 + arm64
(Linux and, structurally, arm64-darwin — the helper uses the portable
`syscall("write")` split); wasm/interp ignore the flag. Flag-off
emission is byte-identical to a build without the feature (verified by
`.s` diff; `TestLeakCheckOffEmitsNoSymbols` pins the no-symbols proxy).
Tests: `internal/e2e/leakcheck_test.go` (balanced `__alloc`/`__free`
loop, rc-driven drop-everything loop, deliberate leak with pinned
counts, exit-code + stdout preservation on both seams, both backends).
Slice 2 (parked): a uniform allocation header would upgrade the counts
to a real census — per-class live-block breakdown and leak-site
attribution — instead of the current aggregate bytes.

Next Phase-6 steps (at the time): wire `__heap_bump_bytes()` into a
profiling pass over compound workloads, then evaluate retiring `strbuf_*`.
(DONE — the profiling pass drove the tuple / map / nested-array / string
slices recorded below; the current open list lives at the END of this
section, not here.)

**Tuple reclamation — SHIPPED ON ALL THREE BACKENDS (2026-06-02).**
A `__heap_bump_bytes()` audit confirmed array / struct / string loop-body
vars reclaim to a flat high-water (array = 64 B at any N), but
**tuple loop-body vars leaked** — Phase 5h SKIPPED `TupleType` in
`emitVarReinitDropOld`, so a `var t = (…)` re-declared in a loop orphaned
every prior iteration's box (and its rc-tracked elements). The fix:
`emitVarReinitDropOld` (loop-body re-declaration) and the assignment
dec-on-overwrite (`b.assign` Ident case) now both route a tuple through
the new shared `b.emitTupleSlotDrop`, which mirrors the exit sweep's
inline `TupleType` branch — a needs-drop tuple (rc-tracked / string
elements) calls the generated `__drop_tuple_<mangled>` fn (`is_unique`
gate → per-element deep drop → `__fern_box_free`), registering the shape
into `b.genTupleDrops` for the post-pass worklist; a plain-element tuple
(`(i32, i32)`) emits the `is_unique`-gated `__fern_box_free` directly.
Gated identically to the array / string siblings on `RcFreeEnabled` +
`freeEligible` (+ `localNameUnique` + `!movedLocals` for the reinit
path). Routing through the existing generated `__drop_tuple_` fn avoids
duplicating `dropStructField` / `decValueOnStack`, the blocker the prior
"out of scope" note cited. Verified by `Test{X86_64,Arm64,WASM}TupleHeapBumpBounded`
(`internal/e2e/rc_heap_bump_tuple_test.go`): a plain `(i32, i32)` loop and
a deep-drop `(i32[], i32)` loop both hold a flat high-water at N=50 vs
N=5000 (pre-fix the latter grew 2400 → 240000 B); 0 over-releases.
**Destructure-binding reclamation — SHIPPED ON ALL THREE BACKENDS
(2026-06-02).** The follow-up the tuple slice flagged: `var (a, b) = p`
inside a loop reuses the synthetic destructure temp slot AND each binding
slot across iterations, but the `*ast.Destructure` lowering gave neither a
per-iteration dec-on-reinit — so every iteration but the last leaked the
tuple box and each rc-tracked element (the probe measured a `(i32[], i32)`
destructure loop growing 2400 → 240000 B). The fix routes both through the
existing `emitVarReinitDropOld` before their re-stores: the temp (an
untainted owned `TupleType` local → its deep-drop / `box_free`) right
before the temp `OpStoreLocal`, and each binding (made an owned co-owner
by the dup-on-projection inc the lowering already emits) right before its
binding `OpStoreLocal`. The rc stays balanced because each drop is
`is_unique`-gated: the temp's deep-drop dec's a shared element (e.g. rc
2→1) without freeing, and the binding's own dec frees it (1→0) — verified
both for the move-on-destructure case (`p` moved, temp sole owner) and the
alias case (`p` + temp co-own, the merged tuple slice dec's `p` first).
First-iteration-safe via the entry zero-init (the slot is NULL, so the
drop's `is_unique` / null guards no-op). Verified by
`Test{X86_64,Arm64,WASM}DestructureHeapBumpBounded`
(`internal/e2e/rc_heap_bump_destructure_test.go`): the `(i32[], i32)`
destructure loop holds a flat 96 B (plain tuple 32 B) at N=50 vs N=5000,
with 0 over-releases over 200 iterations and value-correct sums. The
differential gate + self-host VM/parser suites stay green.
**Struct / enum loop-var deep reclamation — SHIPPED ON ALL THREE
BACKENDS (2026-06-02).** Closes the gap the destructure slice flagged,
for ALL loop-body struct/enum vars (regular `var` re-decl + destructure
bindings, which both go through `emitVarReinitDropOld`).
`emitVarReinitDropOld`'s `StructType`/`EnumType` case did a flat
`__fern_rc_dec` — which neither frees the box (`rc_dec` has no free path)
nor recurses into rc-tracked fields/payloads — so a `var b = Box{ data:
[...] }` / `var e = Arr([...])` re-declared in a loop leaked its box AND
its nested heap field every iteration but the last (probe: 2400 →
240000 B). The fix routes the reinit drop through the generated
`__drop_struct_<N>` / `__drop_enum_<N>` fn via `dropFnNameFor` (the same
helper the exit sweep's `dropStructField` uses), which `is_unique`-gates,
deep-drops fields/payloads, then `__fern_box_free`s the box; generic-enum
instantiations register into `b.genEnumDrops` for the post-pass worklist.
Safe because the case is only reached for a `freeEligible` (owned,
untainted) local — the premature-free that bit escaped values can't arise
(they're ineligible and skipped) — and every nested drop `is_unique`-gates,
so a shared field box is only dec'd. Types `dropFnNameFor` declines (Map
handles, non-uniform / non-heap-boxed generic enums) fall back to the flat
box dec (leak-but-never-UAF). Verified by
`Test{X86_64,Arm64,WASM}StructEnumHeapBumpBounded`
(`internal/e2e/rc_heap_bump_struct_enum_test.go`): a `struct{ data: i32[] }`
and an `enum Arr(i32[])` loop both hold a flat 96 B at N=50 vs N=5000,
with 0 over-releases over 200 iterations and value-correct sums. The full
`internal/ir` + `internal/e2e` suite (incl. the heavy self-host VM/parser
struct/enum users + the free-on==free-off differential gate) stays green.
**Struct reassignment-overwrite deep reclamation — SHIPPED ON ALL THREE
BACKENDS (2026-06-02).** Closes the reassignment half of the struct gap.
A self-overwrite `b = Box{ data: [...], tag: i }` is intercepted by
`tryStructReuseOverwrite` (reuses the box in place), which releases the
box's OLD pointer fields before overwriting — but with a flat
`__fern_rc_dec` that doesn't free an array field's buffer, so a REPLACED
array field leaked its buffer every iteration (probe: 1648 → 160048 B).
The fix routes the old-field release through the new
`emitFieldDropOnStack` (per-field deep drop: `__fern_arr_dec` for arrays,
`__drop_*` for nested struct/enum/tuple, flat dec otherwise), freeing the
replaced buffer at rc 0. Each helper `is_unique`-gates internally, so a
CARRIED-OVER field (`data: b.data` — its eval-inc bumps it to rc>1) is
only dec'd, never freed; the rc arithmetic is unchanged (still one dec per
field), only the rc-0 free is added → zero over-release surface. The
genuine non-reuse dec-path (`b = call()`, alias) also now deep-drops via
the shared `emitStructEnumSlotDrop`, gated on `freeEligible` like the
array / string / tuple reassignment siblings (the conservative call-arg
taint keeps most call-RHS reassignments on the flat dec — safe). Verified
by `Test{X86_64,Arm64,WASM}StructReassignReclaim`
(`internal/e2e/rc_heap_bump_reassign_test.go`): a replaced-field
reassignment loop holds a flat 80 B at N=50 vs N=5000 (was 1648→160048),
and a carried-over `data: b.data` loop is value-correct with 0
over-releases over 200 iterations. The full `internal/ir` + `internal/e2e`
suite (incl. the heavy self-host struct-reuse users + the differential
gate) stays green.
Known gap (follow-up): the ENUM reuse path (`tryEnumReuseOverwrite`) is
rc-neutral by design (construction doesn't inc payloads, reuse doesn't
release them — payloads leak in both baseline and reuse); reclaiming them
needs a balanced payload-release, a separate slice.

**Map loop-var reclamation — SHIPPED ON ALL THREE BACKENDS
(2026-06-02).** Found by profiling diverse compound workloads with the
`__heap_bump_bytes()` probe: a `var m = map_new(8)` re-declared in a loop
leaked the entire map structure every iteration (6400 B → 640000 B),
even though the EXIT sweep already reclaims an owned Map. The reinit path
routed Map through `emitStructEnumSlotDrop`, whose `dropFnNameFor`
declines Map → a flat `__fern_rc_dec` that frees nothing. The fix
extracts the exit sweep's Map-drop body into a shared `emitMapSlotDrop`
(value column via `__map_drop_values` / `__drop_map_via_*` /
`__drop_map_str_values`; string-key column via `__drop_map_str_keys`;
then `__fern_map_drop` for buf + handle — every helper self-guards on
rc==1) and routes Map loop-var reinit through it. Verified by
`Test{X86_64,Arm64,WASM}MapReinitReclaim`
(`internal/e2e/rc_heap_bump_map_reinit_test.go`): a `Map[i32,i32]` loop
holds a flat high-water at N=50 vs N=5000 (was 6400→640000), and a
`Map[string,i32]` loop (string key + value columns) is value-correct with
0 over-releases over 200 iterations. The full `internal/ir` +
`internal/e2e` suite (incl. the heavy self-host map users + the
differential gate) stays green.

**Nested-array (array-of-array) inner-buffer reclamation — SHIPPED ON ALL
THREE BACKENDS (2026-06-02).** `i32[][]` is array-of-rc-element
(`arrElemIsRcTracked(ArrayType)` is true), so the outer drop freed only
the OUTER buffer — the exit sweep's `__fern_drop_arr_ptr` flat-`rc_dec`'d
each element and `emitVarReinitDropOld`'s plain `__fern_arr_dec` ignored
them, leaking every INNER buffer (profiling probe: 3264 B → 320064 B).
The fix adds an array-of-array case to `arrElemStructDropName`: when the
inner array's elements are PRIMITIVE (non-rc, non-string — freeable by a
plain `__fern_arr_dec`), it routes to a stride-keyed generated
`__drop_arr_arr_<innerStride>` loop (`genArrArrDropFn`: is_unique →
free each inner buffer via `__fern_arr_dec(elem, innerStride)` → free the
outer). Stride-keyed so `i32[][]` / `f32[][]` (both inner stride 4) share
one fn. Auto-applies at the exit-sweep / field / child-drop sites (all
call `arrElemStructDropName`); `emitVarReinitDropOld`'s ArrayType case now
consults the same dispatch (so array-of-struct / -tuple reinit also
deep-drops, matching the exit sweep). Inner arrays of rc / string elements
keep the flat `__fern_drop_arr_ptr` (recursive deep drop — a later slice).
Verified by `Test{X86_64,Arm64,WASM}NestedArrayReclaim`
(`internal/e2e/rc_heap_bump_nested_array_test.go`): a `var g = [[..],[..]]`
loop holds a flat 192 B at N=50 vs N=5000 (was 3264→320064), value-correct
with 0 over-releases over 200 iterations. The full suite (incl. self-host +
differential gate) stays green.

**String-concat bound-var — investigated + GUARDED (2026-06-02); the real
remaining leak is statement TEMPORARIES.** A closer look corrected the
earlier reading: a `var s = a + b` loop is actually BOUNDED, not leaking —
the 1600→64576 ramp is a freelist warmup that PLATEAUS (N=5000 == N=50000
== 64576 on wasm; natives read 0 because a short concat stays SSO-inline,
no heap). `Test{X86_64,Arm64,WASM}StringConcatBounded`
(`internal/e2e/rc_heap_bump_string_test.go`) now pins that bounded
high-water — the guard the over-release-only string tests lacked. Along
the way, fixed an uninitialised read in `__fern_str_dec`'s rc==1 free:
it freed `__fern_box_free(data, mem[data-4])`, but `__fern_alloc_rc1`
writes only rc at base+0, so `data-4` was garbage that misrouted the
freelist class; it now frees the actual `len`-byte payload (str_dec
already receives `len`), so an owned heap string returns to the class it
was allocated from. The full suite (incl. the reassign-free path that
exercises the rc==1 branch + the differential gate) stays green.
- **Statement TEMPORARIES — SHIPPED ON ALL THREE BACKENDS (2026-06-02).**
  An OWNED rc temporary materialised in a *consuming* position
  (`(a + b).len()`, `foo(a + b)`, a discarded `a + b;`) was never dec'd —
  the genuine unbounded leak (wasm `(a + b).len()`: 1600 → 160000 →
  1600000, linear, no plateau), because nothing released it
  (emitVarReinitDropOld only sees declared vars). Fixed as a three-stage
  slice gated on `RcFreeEnabled`, all reusing the `freshOwnedRcTempType`
  classifier (the fresh-allocating shapes `rhsTainted`/`computeFreeEligible`
  already treat as untainted-owned) + the shared `emitOwnedSlotDrop` per-type
  drop: **(a)** a discarded bare-ExprStmt temp decs in place instead of a
  floor `OpDrop`; **(b)** an owned call-arg temp passed to a CONCRETE-SCALAR-
  returning call (`resultCannotAliasArg` — number/bool/float/void) is stashed
  + dec'd after the call; **(c)** a value-consuming `.len()` receiver is
  stashed + dec'd after the length op. `Test{X86_64,Arm64,WASM}` ×
  `{StmtTempReclaim, CallArgTempReclaim, LenReceiverReclaim}` — flat
  bump high-water (was linear) on wasm, value-correct + 0 over-release on all
  three. Safety note: stage (b)'s gate is concrete-scalar-result ONLY — a
  pointer or UNRESOLVED-GENERIC result (`id[T](x)->x`, `pick[T](c,a,b)->a|b`;
  `b.exprType` reads the bare type var `T` as non-pointer) could RETURN the
  arg, and dec'ing an arg the caller then reads is a UAF (diff-oracle seeds
  1392/1596/1836 segfaulted on the first, too-loose "non-pointer" cut).
- **Enum reuse-path payloads** (`tryEnumReuseOverwrite`) — SHIPPED. The
  reuse branch now frees the OLD payload (step 3b) for uniform-droppable
  non-string enums, gated by the `freeEligible[e]` the reuse path already
  requires (the taint analysis guarantees no live alias). No global
  rc-counting was needed — the normal overwrite-free was already sound, and
  the reuse path just had to stop bypassing it. String / non-uniform-
  droppable payloads decline reuse and free via the normal path. See the
  "Enum reuse-path payloads — SHIPPED" entry under "Next Phase-6 steps".
- **Array-of-string[] (`string[][]`) — SHIPPED (2026-06-02).** The
  nested-array slice handled only primitive inner arrays; `string[][]`
  kept the flat `__fern_drop_arr_ptr`, leaking each inner `string[]`
  buffer + its strings (3264 B → 320064 B). Now routes through a generated
  `__drop_arr_arr_str` loop: per outer element reclaim the inner string[]
  via the ABI-correct helper (`__fern_drop_arr_str` two-word wasm/arm64;
  `__fern_drop_arr_ptr` native single-word x86_64), then free the outer
  buffer. `Test{X86_64,Arm64,WASM}ArrayOfStrReclaim` — flat 192 B at N=50
  vs N=5000, value-correct, 0 over-releases.
- **Array-of-(struct[]/array[]) inner (`P[][]`, `i32[][][]`) — SHIPPED
  (2026-06-02).** Generalised the array-of-array recursion to ANY
  rc-tracked inner element: `arrElemStructDropName` now routes an
  rc-inner-array through a generated `__drop_arr_of_<perElem>` loop whose
  per-element call is the INNER array's own deep drop —
  `arrElemStructDropName(inner.Elem)`, recursively (`__drop_arr_struct_P`
  for `P[][]`, `__drop_arr_arr_4` for `i32[][][]`; the worklist
  regenerates it transitively). Each helper is_unique-gates, so shared
  inner arrays only dec. `Test{X86_64,Arm64,WASM}ArrayOfRcReclaim` — `P[][]`
  480064→bounded, `i32[][][]` 640064→bounded, value-correct (incl. the
  3-level case), 0 over-releases.
- **Array-of-enum (`E[]`) — SHIPPED (2026-06-02).** Arrays of variant
  values (e.g. `Value[]`, pervasive in the self-host compiler) flat-rc_dec'd
  each element, leaking the enum boxes' rc-tracked payloads (4864 B →
  480064 B). `arrElemStructDropName` now routes a CONCRETE droppable enum
  element through a generated `__drop_arr_enum_<Name>` loop whose
  per-element call is the tag-dispatched `__drop_enum_<Name>`. Unlocks
  `E[][]` etc. via the recursive `__drop_arr_of_` path. UAF-safe despite
  enum construction not inc'ing payloads: a payload built from a local
  taints that local (escapes via the constructor), so its own drop is
  skipped (no double-free), and the deep-drop only fires at reinit/exit
  (after the shared local's last use); the reassignment-overwrite path
  keeps the flat arr_dec. `Test{X86_64,Arm64,WASM}ArrayOfEnumReclaim` —
  480064→bounded + an adversarial shared-payload (`[Arr(a)]`, `a` live)
  value-correct with 0 over-releases; `TestSelfHostVM*` (heavy `Value[]`
  user) green.
- **Generic-enum-array (`Option[T][]`) — SHIPPED.** Threaded the
  `genEnumDrops` registry into `arrElemStructDropName` and routed the enum
  element through `dropFnNameFor`, which substitutes the type args,
  registers the substituted decl, and returns the per-element
  `__drop_enum_<mangled>`. `Test{X86_64,Arm64,WASM}GenericEnumArrayReclaim`
  pin it (240064→bounded + adversarial shared-payload, 0 over-releases).
  Array-of-enum is now complete (concrete + generic).
- **`closure[]` inner — SHIPPED (drop-fn-pointer representation change,
  2026-06-03).** `arrElemStructDropName` now routes `FuncType` elements to a
  generic `__drop_arr_closure` loop; see the closure[] bullet below for the
  full account.
- BOUNDED (confirmed reclaiming): array literal (64 B), struct-of-array
  (96 B), map build (256 B), nested array `i32[][]` (192 B), `string[][]`
  (192 B), `P[][]` / `i32[][][]` (256/320 B wasm, 0 natives), `E[]`
  (256 B wasm), `Option[i32[]][]` (160 B wasm), `closure[]` (wasm 64720 B
  plateau scalar / 64832 B ptr-capture; natives 64–128 B — see below),
  string-concat bound-var (wasm 64576 B plateau / natives 0), nested concat
  `(a+b+c)` (wasm 64576 B plateau / x86_64 0 / arm64 144 B), and
  (post-SSO-flip) NATIVE heap strings `>15 B` — `var s = a + b` /
  `s = s + chunk` loops bounded + 0 over-releases on x86_64 + arm64 (item 5g).

Next Phase-6 steps (open):
  - **`closure[]` array-element drop — SHIPPED ON ALL THREE BACKENDS
    (2026-06-03), via the drop-fn-POINTER representation change (option b).**
    The obstacle was real: the element `FuncType` can't name WHICH closure it
    holds (distinct closures share a signature but have distinct capture
    layouts + per-closure `__closure_drop_<name>` thunks), so an array loop
    could only call the GENERIC `__fern_closure_drop` per element — which by
    design frees only the closure PAIR block and leaks the env. The fix makes
    the closure box self-describing:
      + **Representation change.** The closure PAIR grows from
        `{fn_ptr, env_ptr}` to `{fn_ptr, env_ptr, drop_fn, env_ptr}` —
        2→4 slots (wasm 8→16 B, natives 16→32 B). `drop_fn` is the
        table-index/address of the per-closure `__closure_drop_<name>` thunk
        (0 for a zero-capture closure with no env to free). The env_ptr is
        DUPLICATED at slot 3 so `{drop_fn@2, env_ptr@3}` is a self-contained
        callable sub-pair: a generic holder dispatches `OpCallIndirect` on
        `pair + 2*ptrW` to call `drop_fn(env)` WITHOUT any new IR op — it
        reuses the existing closure-call deref (fn@+0, env@+ptrW of the
        sub-pair). Duplicating env (one extra store in `emitMakeClosure`) was
        chosen over reordering slots so the HOT closure-CALL path
        (`OpCallIndirect`, load-bearing in the self-host emitters) stays
        byte-identical. `emitMakeClosure` on all three backends + the
        zero-capture pair were widened to the 4-slot shape; static
        `OpConstFunc` cells stay 2 slots (their sentinel rc makes the
        is_unique gate skip them before slot 2 is ever read).
      + **Generic per-array drop.** `arrElemStructDropName(FuncType)` routes to
        a single generated `__drop_arr_closure(buf)` loop (worklist-generated,
        like `__drop_arr_enum_`). Per element, gated on `is_unique(p)` (skips
        shared closures AND static cells) and `drop_fn != 0`, it
        `OpCallIndirect`s through the sub-pair to free the env (the thunk
        deep-drops rc-tracked captures, then `__fern_closure_drop(env)` frees
        the env block), then `__fern_closure_drop(p)` frees the pair block,
        then `__fern_arr_dec` frees the buffer. box_free reuse holds because
        every free sits behind a call (the enum-work lesson).
      + **Thunk for every captured MakeClosure target.** `genClosureDropThunk`
        now also emits for SCALAR-only captures (body = empty is_unique sweep +
        `__fern_closure_drop(env)`) so the pair's drop_fn always has a callable
        target; the thunk-gen loop gates on `hasRcCapture || is-MakeClosure-
        target` so an ELIDED scalar closure (bare env, no pair) still generates
        nothing. `LiveFunctionsWithAliases` enqueues `__closure_drop_<name>`
        from each live `OpMakeClosure` so the stored-pointer reference (not a
        call) survives wasm DCE.
      + **CAVEAT — the plan's old "natives elide, no heap" note was WRONG.**
        The lowered IR is IDENTICAL on all backends (2 `OpMakeClosure`, the
        array drop routes through `__fern_drop_arr_ptr`) — natives do NOT
        elide an array-stored closure. They looked bounded only because
        `__heap_bump_bytes()` measures the bump cursor, and on natives the
        small env/pair blocks come from the segregated-freelist arena, which
        the probe doesn't measure — so a native closure[] leak is INVISIBLE to
        the bump probe (it stays 64–128 B regardless). wasm bumps every alloc
        through the measured cursor, so it's the real arbiter: pre-fix
        3264 → 320064 B (100x N, unbounded); post-fix flat 64720 B across 10x N
        (5000 == 50000) — a freelist warmup plateau, same shape as rc string
        concat. The earlier "320064 → 192352" half-reclaim figure was the
        pair-only free; full env reclaim closes it.
      + Coverage: `internal/e2e/rc_heap_bump_closure_array_test.go` — scalar-
        and pointer-capture `(() => i32)[]` bounded across 10x N on all three
        backends, plus an aliased-array (`var gs = fs`, rc>1) AND a
        shared-element (`[f, f]`, the same pair twice + still-live `f`)
        adversarial case, each pinning `__rc_underflow_count() == 0` (no
        over-release / UAF). Self-host (heavy closure user) + the full
        differential gate stay green.
  - **`strbuf_*` — EVALUATED: KEEP (do not retire).** The open question was
    whether the string builder is now redundant given rc string concat/slice
    reclaim. It is not. Per the design conclusion below (§ "strbuf_* becomes
    a more specific optimisation"), strbuf builds bytes in a scratch buffer
    with NO per-byte rc bookkeeping, then `strbuf_take`s the result as one
    string — orthogonal to rc, and strictly faster for hot string-building.
    It is also load-bearing: the self-host emitters use it heavily for their
    O(N) output building (`asm.fern` 39×, `asm_arm64.fern` 52×; `wasm.fern`
    notes "asm.fern's strbuf optimisation is only needed for the huge
    output"), backed by `strbuf_append/data/len/reset/take` runtime helpers
    on all three backends. Retiring it would push that hot path onto
    rc-string concat and reintroduce the exact overhead strbuf exists to
    avoid. Keep it.
  - **Enum reuse-path payloads** (`tryEnumReuseOverwrite`) — SHIPPED ON ALL
    THREE BACKENDS. `e = Wrap([..])` self-overwrite reuses the box in place
    but used to keep the OLD payload, leaking it every iteration (probe: wasm
    1616 → 160016 B across 100x N; natives bounded only via the freelist-arena
    insensitivity, as with closure[]). The fix frees the OLD payload on the
    reuse branch (step 3b) at the uniform-droppable offsets via
    `emitFieldDropOnStack`, mirroring `tryStructReuseOverwrite` step 4.
      + **Why NO global rc-counting was needed (the earlier plan note was an
        over-estimate).** The investigation found the NORMAL enum overwrite
        (`emitStructEnumSlotDrop` → `__drop_enum_<E>`) ALREADY frees payloads
        soundly + bounded — a non-uniform enum self-overwrite is flat across
        N. The soundness doesn't come from rc-counting; it comes from the
        TAINT analysis: `rhsTainted` propagates through variant-constructor
        args, so an enum whose payload aliases a live-read-after local is
        tainted → NOT `freeEligible` → flat dec (no free, safe leak). Only
        fresh / non-aliased payloads make the enum `freeEligible`. The reuse
        path already requires `freeEligible[e]` (a whole-function property),
        so every value `e` holds there is alias-free — making the old-payload
        free sound WITHOUT any inc. The leak was purely that the reuse path
        BYPASSED the normal overwrite's free; step 3b restores it.
      + **Scope.** Only UNIFORM-droppable, non-string payloads free in place
        (covers `Wrap(i32[])` / cross-variant `Bag { Keep(i32[]), Swap(i32[]) }`
        — the shapes the reuse golden tests assert). String payloads (need the
        two-word `__fern_str_dec`, not `emitFieldDropOnStack`) and
        non-uniform-droppable enums DECLINE reuse and fall to the normal
        overwrite path, which frees soundly + bounded at the cost of a fresh
        box alloc. Scalar-only enums (which don't reuse anyway —
        `TestEnumReuseSkipsLiteralScalar`) have nothing to free.
      + Coverage: `internal/e2e/rc_heap_bump_enum_reuse_test.go` — the bump
        probe (1616→160016 ⇒ flat 80 B), cross-variant reuse value-correct,
        and an aliased-payload + forced-reuse adversarial, each
        `__rc_underflow_count() == 0` on x86_64 / arm64 / wasm. The reuse
        golden tests + self-host (heavy `Value`-union user) + the full
        differential gate stay green.
  - **Match-on-fresh-enum scrutinee** (`match (mk(i)) { A(x) => x, … }`) —
    **SHIPPED ON ALL THREE BACKENDS (2026-06-03).** A fresh enum scrutinee
    consumed by a match leaked its box every iteration; the stmt-form Match +
    expr-form MatchExpr lowerings now reclaim it after the match
    (`reclaimableMatchScrutinee`: an `ownedCallResultType` scrutinee with every
    arm BINDING — and, for the expr form, the RESULT — non-pointer, so no
    payload escapes the freed box; the dec is is_unique-gated, so an aliased
    scrutinee at rc>=2 only dec's). The enum-box-free was extracted from the
    exit sweep into the shared `emitEnumSlotDrop` (its `decValueOnStack` /
    `dropStructField` closures promoted to `*builder` methods).
    Two findings that reshaped the plan:
      + **The doc's `Box{Val(i32), Empty}` example is PAIR-FORM** (Option[i32]
        shape) — the callee never heap-boxes it, so `ownedCallResultType`
        correctly excludes it and the feature leaves it to the pair-form
        machinery. The real target is a GENUINELY heap-boxed enum (3+ variants
        / multi-payload). The "80000 → 800000" probe was the pre-existing
        match-expr pair-REBOX temp, a separate leak.
      + **An inline `__fern_box_free` doesn't return the box to the freelist on
        wasm, but the identical box_free inside a GENERATED drop FUNCTION does**
        (verified against the byte-identical `__drop_struct_` fn, which reuses).
        So the PER-ITERATION drop sites (match-scrutinee + loop-var reinit via
        `emitStructEnumSlotDrop`) route through the generated `__drop_enum_<N>`
        fn (`emitOwnedEnumDrop` → `emitEnumDropViaGenFn`; the worklist
        regenerates the body from `info.Enums`, covering scalar enums that
        `dropFnNameFor` declines). The once-per-call EXIT SWEEP stays inline
        (`emitEnumSlotDrop`) — its bounded leak doesn't need the gen-fn
        indirection, and keeping it inline preserves its golden-test codegen.
    Coverage: `internal/e2e/rc_heap_bump_match_scrutinee_test.go` (expr + stmt
    forms bounded across N on x86_64 / arm64 / wasm + an aliased-scrutinee
    UAF/over-release safety case).
  - **Scalar-literal arg taint — LITERAL HALF SHIPPED; BINARY HALF WON'T-DO
    (2026-06-03).** `rhsTainted` tainted every `NumberLit`/`FloatLit`/`BoolLit`
    (no case → tainted default) and every non-concat `Binary`
    (`!IsStringConcat`), so a fresh owned buffer whose only "borrowed" input is
    a literal / scalar-arithmetic size arg read as ineligible and wasn't
    reclaimed at its last reference.
      + **Literal half — SHIPPED.** `NumberLit`/`FloatLit`/`BoolLit` → untainted
        (they alias nothing), so a literal-sized scratch temp reclaims
        (`__alloc_u8(8)`; int_to_string_radix's 33-byte `digits` via
        `(n).to_hex()`). Two guards make it safe: (1) a **`usize`-cast escape
        taint** — a pointer-shaped local cast to a raw integer (`buf as usize`)
        is escape-tainted in `computeFreeEligible` (its liveness isn't
        rc-trackable), keeping int_to_string's `scratch` protected; (2)
        **`random_bytes`'s result is tainted explicitly** — the two-word
        backends alloc it as raw n bytes with NO rc header (`__fern_alloc`, not
        `__fern_alloc_rc1`), so str_dec'ing it over-released
        (TestArm64RandomBytes 83 vs 16); the literal arg used to protect it by
        accident. Tests: `rc_heap_bump_literal_alloc_test.go`.
      + **Reframing:** these buffers were already **bounded** on main (the
        segregated freelist caps them), so this is a steady-state high-water
        REDUCTION (to_hex 160→96 B x86, 144→80 B arm), not the unbounded-leak
        fix the original note implied.
      + **Binary half — WON'T-DO (over-releases).** Untainting non-concat
        `Binary` (so `__alloc_u8(out_len)`, `out_len` from `k + 1`, reclaims)
        made int_to_string_radix's RESULT buffer eligible and the exit sweep
        over-released it — `to_rgb_hex` returned the wrong hex (caught by the
        arm64 stdlib-bundle tests + `TestToRgbHexNoOverRelease`). The
        escape/move analysis can't prove those binary-sized buffers safe
        (string_from_bytes copies, but the freelist reuse still corrupted), and
        most are `as usize`-threaded or moved into the return anyway — marginal
        benefit, real risk. Left tainted; revisit only with a precise move /
        last-use analysis, not a blanket untaint.

This session (2026-06-03) ALSO SHIPPED the value-consuming-position +
fresh-result reclamation family on top of statement-temps a/b/c: owned
call-RESULT args (`take(mk(i))`), discarded owned call results (`mk(i);`),
match-EXPRESSION owned results (`var s = match … { 0 => a+b, _ => b+a }`,
rhsTainted MatchExpr case), index-of-fresh (`mk(i)[scalar]`) and field-of-
fresh (`mk(i).scalarfield` / tuple), all sharing `freshOwnedRcTempType` /
`ownedCallResultType` + the is_unique-gated `emitOwnedSlotDrop` + the
non-pointer-extract gate. Plus array-of-enum (`E[]`/`Option[T][]`/`E[][]`)
and an arm64 large-frame codegen fix (frame offsets / stack alloc past the
12-bit imm → movz/movk + register-operand add/sub), a latent self-host
blocker.

Statement-temporary reclamation is SHIPPED (all three stages + the
nested-concat-intermediate follow-up — see the bullets above; design note
retained below for the record).

### Statement-temporary reclamation — design note (SHIPPED, all three stages 2026-06-02)

The remaining unbounded leak. An OWNED rc temporary materialised in a
*consuming* position is never released, because the two things that
consume it don't dec:
  1. **Borrowed call args.** Under the Phase-2d borrow model the callee
     does NOT dec a borrowed arg, so `foo(a + b)` (a fresh concat passed
     to `foo(s: string)`) leaks the concat buffer — the caller created an
     owned temp, handed it over borrowed, and nothing frees it. This is
     the real-code case (the dominant source).
  2. **Value-consuming ops.** `(a + b).len()` lowers to `b.expr(receiver)`
     then `OpStrLen` (ir.go ~8869), which consumes the (data,len) and
     returns an i32 — the concat buffer is dropped on the floor. Same for
     other inline consumers.
  3. **Discarded ExprStmt results.** A bare `a + b;` / `xs.map(...);`
     statement `OpDrop`s an owned rc result without a dec (ir.go ~5624).
Measured (wasm): `(a + b).len()` in a loop leaks 1600→160000→1600000
(linear, no plateau). Bound-var / stored / returned temps are NOT leaks —
they already get a dec (emitVarReinitDropOld / exit sweep) or are retained.

**Mechanism.** Per-statement owned-temp tracking. While lowering an
expression, when `b.expr` materialises an OWNED rc temporary, stash its
pointer in a scratch slot and dec it at the enclosing statement boundary
(after the operand stack is consumed). "Owned temporary" is the same
classification `rhsTainted` / `computeFreeEligible` already compute:
fresh-allocating shapes (string concat `Binary{IsStringConcat}`, string
`SliceExpr`, `ArrayLit` / `StructLit` / `TupleLit` / `MakeClosure`,
fresh-returning calls) are owned; idents / field / index reads are
borrowed and must NOT be dec'd (over-release). A temp that ALSO flows
into a retain sink (stored into a container, returned, bound to a var)
must be excluded — it already has its dec / is retained — so reuse the
escape-taint walk to skip those.

**Hazards.** This is the highest double-free surface of the whole
Perceus effort — temporaries appear in every expression. A wrong "owned"
verdict on a borrowed value is an immediate UAF. Mitigations: gate on
`RcFreeEnabled` (free-off stays byte-identical), reuse the proven
`rhsTainted` classifier rather than inventing one, and lean on the full
differential + self-host gate (the self-host compiler is temp-dense).

**Safe incremental ordering.** (a) Discarded bare-ExprStmt owned temp
(localised at ir.go ~5624: dec instead of plain `OpDrop` when the expr is
an owned rc shape) — smallest, safest, but rare in real code. (b) Owned
call-arg temps (the real win — `foo(a + b)`): after the call, dec each
arg that was an owned temp and didn't escape. (c) Value-consuming
receivers (`(a+b).len()`): stash the receiver ptr before the consuming
op, dec after. Each stage ships with bounded-growth probes (`foo(a+b)` /
`(a+b).len()` loops flat at N=5000 vs N=50000) + 0-over-release + the full
gate. Likely a multi-PR slice on its own — budget a focused session.

**OUTCOME (shipped, three PRs).** Implemented as `freshOwnedRcTempType`
(the fresh-allocating shape classifier — ArrayLit/StructLit/TupleLit, string
concat, string slice; MakeClosure excluded as a bare closure temp is
effectively nonexistent and its capture-drop thunk is name-keyed) +
`emitOwnedSlotDrop` (the per-type slot drop extracted from
`emitVarReinitDropOld`). Two refinements vs the sketch above:
(1) **Stages dec at the precise consumption point, not a generic
"statement boundary"** — (a) on the discarded ExprStmt's stack value,
(b) right after the call, (c) right after the length op. This is simpler
and avoids tracking a per-statement temp list.
(2) **Stage (b)'s "didn't escape" became a hard CONCRETE-SCALAR-RESULT gate**
(`resultCannotAliasArg`: number/bool/float/void). The dec fires immediately
after the call, so the arg must be DEAD then — but a callee that returns its
arg (`id`/`pick`) makes the result alias the arg, and a non-pointer-only gate
let an unresolved generic result (`ast.ParamType` `T`, which reads
non-pointer) slip through → diff-oracle seeds 1392/1596/1836 SEGFAULTED.
Only a concrete scalar result provably can't be / contain the arg. Retain
sinks (`Map_set`/`Array_push` move a fresh arg in uncounted) are also
excluded. Stages (a) and (c) have no such hazard — the temp is fully
consumed and demonstrably dead, identical safety to a discarded temp.

**FOLLOW-UP: nested-concat intermediates (SHIPPED 2026-06-02).** The
per-statement mechanism above dec's the OUTERMOST owned temp of a
statement-position expression, but a sub-expression temp consumed
*mid-expression by another operator* slips through: in `a + b + c`
(= `(a + b) + c`) the inner `(a + b)` is consumed by the outer
`OpStrConcat`, which copies its bytes but never frees its buffer — so a
chained / parenthesised concat leaked one buffer per join (`(a+b+a).len()`
in a loop, 192288 B → 1632288 B, unbounded). Fixed in the concat lowering
(`b.binary`): an operand that is itself an owned string temp
(`isOwnedStringTemp` — a sub-concat or string slice) is stashed in a
scratch slot, used for the concat, then dec'd (ABI-correct __fern_str_dec
two-word / __fern_rc_dec x86_64). Recurses, so the whole left-/right-nested
chain reclaims; borrowed operands (idents / literals) are not stashed (a
live value would be freed). `Test{X86_64,Arm64,WASM}NestedConcatReclaim` —
`(a+b+a+b).len()` loop flat (was 1632288), deep-chain value-correct
(length + content) with 0 over-releases. Analogous sub-expression
intermediates for non-string operators (none today produce owned temps
fed to another op) would extend the same way.

### E3 drop-guided reuse evaluation — verdict (2026-07-13)

Plan item E3 (`docs/NICHE-BORROWS-PLAN.md`): the ICFP 2022
frame-limited / drop-guided source selection (Lorenzen & Leijen,
"Reference Counting with Frame-Limited Reuse") implemented behind
`ast.RcReuseDropGuided` (default OFF, env knob
`FERN_RC_REUSE_DROP_GUIDED=1`) in `internal/ir/rc_dropguided.go`,
as an evaluation — NOT a default flip. The flag swaps ONLY the
pair-selection scan inside `computeReuseSources`; every proposed
pair still passes the identical gates (`reuseClassOf`,
`freeEligible`, never-reassigned, name-unique, moved / borrow
exclusions) and the shared lowering (runtime `is_unique` guard,
degrade-to-fresh-alloc, slot zeroing, `reuseConsumed`
bookkeeping). Flag-OFF output verified byte-identical to the
pre-change tree (sha256 over emitted x86-64 asm for 5 reuse
fixtures — golden stash-diff).

**What was implemented.** Three token flows: (1) per-statement-list
forward scan — a token is born at the donor's last-use index + 1
and claimed FIFO in drop order by the first same-class
construction at/after it; (2) tokens flowing into dominated nested
regions — this is exactly the existing cross-block pass, shared
verbatim; (3) NEW: a token born at a drop point INSIDE a
dominated, non-loop if/match arm, claimed by a later construction
in the same arm. Flow 3 is the only source of new pairs: the PLDI
pairing structurally misses it (the donor is declared in the
parent list, so the same-block pass can't see it, yet still
referenced inside the enclosing statement, so the cross-block
`deadFrom` rejects it). Conservative token death: never across a
loop back-edge (a next iteration re-reads the donor), never with
sibling-arm references, never when the donor is the match
scrutinee (arm bindings are live uncounted views of its box).

**Finding: superset, not different.** On this codebase's shapes
the token flow selects a SUPERSET of the pairing — flows 1–2 are
provably equal in pair count to the PLDI passes (deadFrom(D, k) ≡
"token born at or before k"; both greedy orders achieve maximum
matching on the suffix-closed eligibility structure), and flow 3
adds the one arm-drop shape. Every `general_reuse_test.go` /
`struct_reuse_test.go` / `enum_reuse_test.go` /
`c2_consuming_reuse_test.go` expectation holds unchanged under the
flag (`internal/ir` suite green with `FERN_RC_REUSE_DROP_GUIDED=1`).

**Measured numbers (x86-64, free+freelist on).**

| program | bump growth OFF | bump growth ON |
|---|---|---|
| array build-up loop (2000 it) | 64000 B | 64000 B |
| struct churn loop (R3 loop shape, 2000 it) | 32 B | 32 B |
| R3 dead chain (straight-line) | 32 B | 32 B |
| arm-drop shape in a loop (Wide struct, 48 B class, 2000 it) | 144 B | 96 B |
| arm-drop shape straight-line (tuple, 16 B class) | 32 B | 16 B |
| arm-drop loop, arm on even iters only (tuple) | 48 B | 48 B |
| 400-function synthetic (tuple churn + per-fn arm shape) | 64 B | 64 B |

The win, where it exists, is ONE box class of peak high-water per
arm-drop site (the site really fires — straight-line tuple 32→16,
struct loop 144→96); in loop shapes where the arm-scoped
construction is dropped at arm exit anyway, the freelist recycles
the block within the iteration and the high-water difference
vanishes (48=48, 64=64 — despite the 400-fn synthetic emitting
2x the reuse sites under the flag, 400 OFF vs 800 ON). Cumulative
allocation traffic is identical in every case: with free +
freelist on, reuse moves the peak, never the total.

- Runtime contracts: all 13 `genReuseCases` pass with the flag ON
  (`TestX86_64GeneralReuseDropGuided`), zero `__rc_underflow_count`.
- Leak guards: `TestX86_64RcRequestLoopLeakGuardDropGuided` +
  `TestX86_64AppendCopyLeakBoundDropGuided` green.
- Differential: 224 fernsmith seeds, interp vs x86-64 flag OFF vs
  flag ON — 0 mismatches (`drop_guided_differential_test.go`).
- RC-family e2e sweep (x86-64 + wasm Reuse/Rc/HeapBump/Freelist/
  Leak/Trmc families) green under `FERN_RC_REUSE_DROP_GUIDED=1`.
- The new arm shape is value-correct + underflow-free on all three
  backends (`Test{X86_64,Arm64,WASM}DropGuidedArmShapeRuntime`).
- Self-compile (static): emitting the `asm_ir_run.fern` driver
  closure (1722 functions, ~982 MB asm) through the native x86-64
  backend — `__fern_alloc_reuse` sites: **10 flag OFF vs 10 flag
  ON**. Drop-guided finds ZERO additional pairs on the real
  self-host compiler codebase: the arm-drop shape does not occur
  there (self-host structs carry string fields, which taint
  eligibility long before selection strategy matters). The two
  emits differ by 29 asm bytes (donor-assignment order inside
  equal-count pairings), behaviourally identical.
- Self-compile (runtime): both flag-state driver binaries were
  assembled + linked (`gcc -static -nostdlib -no-pie -fuse-ld=lld`)
  and run on a 182 KB / 403-function fixture: **byte-identical
  2.61 MB asm output**, exit 0 both, peak RSS statistically equal
  (OFF 157.7–164.2 MB, ON 159.4–163.2 MB over 3 runs each — within
  run-to-run noise, as the 10 = 10 site count predicts).

**Verdict: KEEP PAIRING as the default; revisit at the self-host
port (E4).** Rationale, numbers over narrative: with the freelist
+ precise drops already in place, the drop-guided-only wins are
bounded to ONE box class of peak high-water per arm-drop site
(144→96 B above — the freelist recycles everything else either
way), and the self-compile site count shows the shape occurs
ZERO times in the largest real Fern codebase we have (10 reuse
sites either way). That is not worth (a) flipping a
memory-safety-critical default mid-way through the goal-2
self-host port, or (b) porting TWO selection algorithms. The flag, tests, and this verdict stay in-tree so the
self-host reuse port (`SELFHOST-PERCEUS-REUSE.md`) can (re)decide
with the same harness when it reaches the reuse slice — if the
port adopts one algorithm, drop-guided's frame-limited robustness
under transformation plus its strict-superset behaviour here make
it the better candidate to port ONCE, but that decision belongs to
E4, not this evaluation. `docs/REUSE-CONTRACT.md`'s "Known gaps"
entry points here.

## Testing strategy

Three layers:

### Unit tests for rc helpers

Direct calls to `__fern_rc_inc` / `__fern_rc_dec` from a Go test
that pre-builds a heap layout, exercises the helper, and asserts
on the rc word + freelist state.

### Integration: rc-leak detector

Run every existing e2e test under a "leak detector" build mode
that, on program exit, walks the heap and reports any value with
rc > 0. (Like a poor-man's ASAN, scoped to Fern values.)

### Integration: rc-correctness fuzzer

Generate random programs that build up and tear down complex
nested values (arrays of structs of arrays of closures of strings),
run them under the leak detector, and verify rc == 0 at exit.

### Property: every test program runs identically before/after

The semantic surface doesn't change. After each phase, the full
existing e2e suite must pass with identical exit codes, stdout,
and stderr.

## Open questions (ALL RESOLVED during implementation)

These were the pre-implementation decisions; every one was settled and the
shipped code follows the "Recommended" answer in each (kept below for the
rationale): rc is **i32, panic on overflow** (1); the rc word sits at a
**fixed `-8` offset** from the data pointer (2); drop handlers are
**per-type generated direct calls** — `__fern_drop_*` / `__drop_*`, no
runtime vtable (3); drops are placed **at the IR level** during lowering
(4); a drop-time fault **aborts** (5); strings are **special-cased per
backend** (two-word wasm / LSB-tagged x86_64 / boxed arm64), which is why
native-arm64 heap-string rc is still blocked on the SSO flip — item 5g
(6, 7). Original discussion follows.

1. **rc width: i32 or u32 or i64?**
   - i32: 4 bytes, 2 billion ref ceiling. Cheap. Saturation
     behavior on overflow: panic or saturate-and-leak? Roc
     panics.
   - u32: 4 bytes, 4 billion ref ceiling. Same overflow concern.
   - i64: 8 bytes. Never overflows for any realistic program.
     Costs 4 extra bytes per allocation. Probably overkill.
   - Recommended: i32, panic on overflow. Same as Roc.

2. **Where in the header does rc go?**
   - Before the existing header (data ptr at `+headerBytes`)?
   - Or at a fixed offset (`-8` from data ptr, regardless of
     existing header)?
   - Recommended: fixed offset. Easier to write polymorphic
     inc/dec helpers.

3. **Drop handler dispatch: per-type generated, or runtime
   table?**
   - Generated: codegen emits `__fern_drop_<type_id>` for each
     concrete type. Calls are direct.
   - Table-based: each heap value has a "type tag" or "shape
     pointer" that points to a vtable including the drop fn.
     One indirect call per drop.
   - Recommended: generated. Direct calls are faster and we
     already monomorphise.

4. **Where do drops go syntactically? At AST-level, IR-level, or
   later?**
   - AST: easier to implement, may emit redundant drops.
   - IR after liveness: more accurate placement, more compiler
     work upfront.
   - Recommended: AST first (correctness first), refine to IR
     in phase 4 alongside Perceus.

5. **What happens when a drop handler crashes?** (E.g. dereferences
   a value that was already freed due to a bug.) The Fern
   runtime today has no signal handlers. Probably: abort. Same
   as today.

6. **String layout: rc, or special-cased?** Strings already have
   inline-SSO. Heap-form strings need rc; inline-form strings
   don't (they're values, not pointers). Need to update every
   string operation to branch on inline vs heap.

7. **Two-word string ABI: where does rc live?** Today's heap-form
   layout is `[len:4 | data]` with `len` at `[data - 4]`. Adding
   `rc:4` makes it `[rc:4, len:4 | data]` with `len` still at
   `[data - 4]` and `rc` at `[data - 8]`. The two-word ABI
   (`(data, len)` register pair) doesn't change.

8. **Closure boxes: how is "captured by value" affected?**
   Closures capture by value today (i32 / pointer copy at
   creation). With rc, captures of pointer values must inc on
   creation. Drop of the closure decs each captured pointer.

9. **Recursive types: how to terminate?** Currently Fern's types
   are tree-shaped at the type level (no cycles). Recursive
   types like linked lists go through enum variants (e.g.
   `Cons(i32, Box<List>) | Nil`). The drop handler recurses,
   which on a long list could blow the call stack. Fix:
   convert recursive drop to iteration when the type is known
   to be linear-recursive. Optimisation, not correctness.

10. **Closures-of-closures with shared captures.** A closure
    that captures another closure that captures a value: rc
    must inc on both wrap-and-pass operations, dec on both
    drops. Already covered by the general rules, but worth
    test cases.

11. **Arena interaction.** Today's bump allocator has an
    `arena_save` / `arena_restore` pair used by TCP handlers
    to reclaim per-request memory cheaply. With per-value rc,
    arena reclaim must EITHER explicitly dec every still-live
    value before restoring (slow) OR skip rc on arena-allocated
    values (fast, requires per-allocation tag). Recommended:
    arena allocations get rc=SENTINEL_STATIC so dec is a no-op;
    the whole arena is reclaimed at restore. Programmer
    responsibility not to leak references past the arena boundary.

## Risk register

Top three risks that could derail this:

1. **Phase 1 leak-detector finds non-trivial leaks.** Likely.
   Plan for a multi-week debugging slog. Mitigation: tests are
   designed to print rc traces when a leak is found, so the
   path from "test failed" to "missing dec at site X" is short.

2. **Performance regression at phase 1.** Almost certain — every
   reference move is now slower. Mitigation: don't ship phase 1
   to users; gate it behind a flag until phase 4 lands and
   performance recovers. CI runs the unflagged path; merging
   doesn't break anyone.

3. **Drop-handler stack overflow on deep types.** Real for
   500k-deep linked lists. Mitigation: detect linear recursion
   in drop-handler codegen and emit a loop instead of a
   recursive call.

## Self-hosting context

Specifically for the original motivation (compile asm.fern
through asm.fern):

After phase 2 (mutating ops use rc check), the parser's hot
loops are O(N). asm.fern's `acc.push(...)` patterns are O(N).
Self-hosting through itself becomes tractable on a normal CI
runner (sub-GB peak RSS).

`strbuf_*` becomes a more specific optimisation: it still
beats rc-based string building because there's no rc bookkeeping
to skip for the scratch bytes. Keep it for hot string-building
sites; it composes orthogonally with rc.

## Reference reading

- Reinking et al. "Perceus: Garbage Free Reference Counting with
  Reuse" (PLDI 2021). The Perceus paper.
  https://www.microsoft.com/en-us/research/publication/perceus-garbage-free-reference-counting-with-reuse/
- Roc's stdlib RC implementation. The runtime library is in
  Zig; readable.
- Koka's runtime, also in C.
- Bacon & Rajan, "Concurrent Cycle Collection in Reference
  Counted Systems" (ECOOP 2001) — for when cycles eventually
  become a thing.

## Estimated effort

- Phase 0 (layout migration): 2-3 days. Mechanical, touches
  every backend.
- Phase 1 (inc/dec everywhere): 1-2 weeks. The bulk of the work.
  Real debugging happens here.
- Phase 2 (rc check in mutating ops): 2-3 days.
- Phase 3 (allocator with freelist): 1 week.
- Phase 4 (Perceus pair-cancellation): 1-2 weeks. Real compiler
  pass with dataflow.
- Phase 5 (drop reuse + borrowed params): 1-2 weeks.

Total realistic: 5-9 weeks of focused work. Spread across PRs
that each leave the tree shippable.
