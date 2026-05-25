# RC + Perceus implementation plan

Implementation plan for refcounted heap values with compile-time
Perceus optimisation.

Date: 2026-05-20.
Status: design, not implementation.

## Why

Lang has value semantics: arrays, strings, structs, enums, closures
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
  cycle. Lang already has this property by accident (no mutable
  struct fields, no mutable closure captures). When mutability via
  fields is added later, either retain the no-cycles invariant or
  add a tracing fallback. For phase 1: no fallback, document the
  invariant.
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

```lang
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
lang surface is mostly there but has rough edges where the syntax
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

NOT YET STARTED. Each non-array type category needs:

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
   - **Remaining:** per-tag enum dispatch (non-uniform variants) +
     generic-enum type-arg substitution; closure / map drop handlers;
     the dec-on-overwrite site (entangled — `push`'s copy path
     transfers element ownership without an inc, so routing array
     overwrite through the drop would double-release; needs a
     per-method ownership audit or a self-push exclusion first); and
     deep (nested-of-nested) recursion via per-type `drop_<T>` rather
     than the flat `rc_dec` used for non-array fields/elements/
     payloads now (sufficient while free is off).
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
5. **Enable + verify.** Flip the flag on, run the entire e2e suite
   under the detector with identical exit codes/stdout/stderr, plus
   the rc-correctness fuzzer (random nested values).

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

### Phase 5: Drop reuse + borrowed params

Two separate PRs:
- Drop reuse: dec immediately followed by alloc of compatible size
  reuses the storage.
- Borrowed params: per-function escape analysis identifies args
  that don't escape; callers skip the inc.

### Phase 6: cleanups + measurements

End-state verification: run the benchmarks, compare RSS, build
the self-host through itself, profile hot allocations, retire
the `strbuf_*` primitive if Perceus + drop-reuse make it
redundant.

## Testing strategy

Three layers:

### Unit tests for rc helpers

Direct calls to `__fern_rc_inc` / `__fern_rc_dec` from a Go test
that pre-builds a heap layout, exercises the helper, and asserts
on the rc word + freelist state.

### Integration: rc-leak detector

Run every existing e2e test under a "leak detector" build mode
that, on program exit, walks the heap and reports any value with
rc > 0. (Like a poor-man's ASAN, scoped to lang values.)

### Integration: rc-correctness fuzzer

Generate random programs that build up and tear down complex
nested values (arrays of structs of arrays of closures of strings),
run them under the leak detector, and verify rc == 0 at exit.

### Property: every test program runs identically before/after

The semantic surface doesn't change. After each phase, the full
existing e2e suite must pass with identical exit codes, stdout,
and stderr.

## Open questions

These need decisions before implementation starts. Listed in
roughly-decreasing order of how blocking they are.

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
   a value that was already freed due to a bug.) The lang
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

9. **Recursive types: how to terminate?** Currently lang's types
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
