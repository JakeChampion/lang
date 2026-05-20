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
   `parser.lang` would need ~7 GB and `asm.lang` ~60 GB just to
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
__lang_rc_inc(ptr):
    if ptr == NULL: return
    rc = *(ptr - rcOffset)
    if rc == SENTINEL_STATIC: return        ; static const, never touch
    *(ptr - rcOffset) = rc + 1

__lang_rc_dec(ptr, drop_handler):
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
drop handler per concrete type, named like `__lang_drop_array_string`,
`__lang_drop_struct_Foo`, etc.

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
    - Call `__lang_arr_push(nfuncs, fd)`.
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
__lang_arr_push(arr, x):
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
inc loop. Generate two versions of `__lang_arr_push`: one for
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
| `m.set(k, v): void` | `m.set(k, v): Map[K, V]` | 2 | Caller writes `m = m.set(k, v)`. |
| `m.delete(k): bool` | `m.delete(k): (Map[K, V], bool)` | 2 | Returns the (possibly new) map plus the present-before flag. |
| `m.clear(): void` | `m.clear(): Map[K, V]` | 2 | Empties; returns a fresh empty map (or recycles via drop-reuse). |

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

### Phase 2: rc check in mutating ops

PR: `__lang_arr_push` and friends check rc. rc==1 path mutates in
place; rc>1 path keeps the copy semantics. The user-facing API
audit (see above) lands in the same phase — Map's void-returning
`set` / `delete` / `clear` become value-returning, and
`arr[i] = v` rewires to desugar through a copy-on-write `set`
method. Callers update in-tree at the same time the
implementation flips.

Effect: the self-host parser + asm.lang push loops become O(N).
This is the payoff phase. After Phase 2, no method's signature
implies in-place mutation; every collection looks immutable to
the user.

### Phase 3: real allocator

PR: replace the bump allocator with size-class freelists. Dec'd
values' storage actually gets reclaimed.

Effect: long-running programs (TCP handlers, etc.) stop leaking.
Memory usage drops.

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

Direct calls to `__lang_rc_inc` / `__lang_rc_dec` from a Go test
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
   - Generated: codegen emits `__lang_drop_<type_id>` for each
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

Specifically for the original motivation (compile asm.lang
through asm.lang):

After phase 2 (mutating ops use rc check), the parser's hot
loops are O(N). asm.lang's `acc.push(...)` patterns are O(N).
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
