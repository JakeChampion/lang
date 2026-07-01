# SSA RC / runtime-helper migration — design

Tracking: #4112 (SSA-level register allocation, child of the binary-size
epic #4109). This is the design for bringing the `__fern_*`
reference-counting runtime helpers to the SSA native path (`x86_64ssa`),
the next whole-program gap after closure *dispatch* (#4193 / #4194 /
#4196 / #4198).

## The problem

Non-escaping closures now run end-to-end on the SSA path
(`TestProgramRunClosure`). But a **stored or capturing** closure fails at
assembly:

```
function main(): i32 {
  var a: i32 = 10;
  var g: (i32) => i32 = (n: i32): i32 => n + a;   // captures a
  return g(5);
}
```

→ `undefined label "fn___fern_closure_drop"`,
`undefined label "fn___fern_rc_is_unique"`.

When a closure is bound to a variable (or captures), the IR inserts
reference-counting: the variable's scope-exit emits
`__fern_closure_drop`, which gates its work on `__fern_rc_is_unique`.
These `__fern_*` symbols are **hand-written runtime assembly** emitted
per-backend (`internal/codegen/arm64/arm64.go`,
`internal/codegen/x86_64/x86_64.go`), each gated by a `usesX` flag and an
`emitXRuntime()` method. The SSA real-asm emitter (`gas.go`) emits none of
them, so any IR that references one is unlinkable.

They also assume a **heap layout the SSA bump allocator does not
produce**. `__fern_rc_is_unique` reads a reference-count word at
`[ptr-8]`; the SSA allocator (`MemAlloc` / `closureLines` in `gas.go`)
hands back raw bump-pointer bytes with no header. So porting the helpers
requires first aligning the allocator with the native rc-header contract.

## The rc-header contract (reverse-engineered from `__fern_alloc_rc1`)

A reference-counted heap object is laid out as an 8-byte header followed
by the payload; the **data pointer** that flows through the program points
*past* the header:

```
 base+0  : rc         (4-byte)   ← [data-8]
 base+4  : payload_sz (4-byte)   ← [data-4]   (so drop can free w/o a size arg)
 base+8  : payload ...           ← data
```

- `__fern_alloc_rc1(size)`: bump `size + 8`, store `rc = 1` at `base+0`
  and `size` at `base+4`, return `base + 8`.
- `__fern_rc_is_unique(data) -> i32`: `0` if `data` is null, below the
  low-address guard (`< 0x10000`), or a **static sentinel** (top bit of
  the rc word set); else `rc == 1 ? 1 : 0`. The guard chain makes it safe
  to call on a slot that might hold a non-pointer scalar.
- Static / immortal cells carry rc word `0x80000000` (top bit) so
  `is_unique` — and therefore `dec`/`drop` — always skip them.

The SSA path currently makes **no** static cells: even a bare
`OpConstFunc` value lifts to a heap `OpMakeClosure(0)`. So on the SSA path
every function value is an `rc == 1` heap cell (the immortal-sentinel case
only needs to be *honoured* by the helpers, not produced, until/unless the
SSA path grows `.rodata` cells).

## Approach

Mirror the native rc semantics **exactly** so the helper bodies port
almost verbatim (the instruction selection differs, the layout and guard
constants do not). This is forced, not chosen: the helpers and the IR
call sites already agree on the contract; the SSA path must join it.

### Allocator

Give every SSA heap allocation the rc-header layout: bump `size + 8`,
write `rc = 1` at `base+0`, return `base + 8`. `MemLoad`/`MemStore` are
unaffected — they already operate on the returned data pointer, and every
payload offset is relative to it. The bump cursor simply advances by
`size + 8`. Uniformly heading every allocation (not just closure cells)
keeps the emitter simple and matches the native model, and is invisible to
the differential oracle (exit codes are offset-independent).

No freelist reuse initially: the SSA bump heap never frees. `__fern_free`
/ the closure-env reclamation can **no-op the physical free** (leak within
the process) while still performing the *observable* work — deciding
uniqueness, dispatching the embedded drop-fn over captures. Reuse analysis
and real reclamation are a later goal-2 slice; leaking is memory-safe and
correct for short-lived programs (the language's stated use case), and the
whole-program differential tests still pass because results don't depend
on reclamation.

### Helper port order (leaf-first)

1. **Allocator rc-header** + **`__fern_rc_is_unique`** (a leaf: guards +
   rc compare, no calls, no frame). Smallest coherent, testable unit — an
   SSA program that allocs a cell and calls `is_unique` returns `1`; on a
   null/sentinel operand returns `0`.
2. **`__fern_rc_inc` / `__fern_rc_dec`** — the same guard chain; `inc`
   bumps the rc word, `dec` decrements and (when it would hit 0) frees —
   here, no-ops the free.
3. **`__fern_closure_drop`** — on `is_unique`, dispatch the embedded
   per-closure drop-fn over the captures (`OpCallIndirect` on the drop
   sub-pair), then free the cell; else `dec`. Unblocks stored/capturing
   closures.
4. Whole-program **escaping-closure** tests vs the interpreter (extend
   `TestProgramRunClosure`), starting with linearly-used closures (rc
   stays 1) before rc-aliasing cases that need `inc` at the dup site.

Later (shared with the same machinery): struct / array field drop and the
per-type `__drop_*` thunks — the same is_unique-gated recursive-dec shape.

### Gating

Mirror the native `usesX` mechanism: scan each module's `OpCallDirect`
targets (and the transitive helper closure — `closure_drop` pulls in
`rc_is_unique`, etc.) and emit exactly the referenced helpers into the SSA
`.text`, so a program that never drops a closure links none of them.

## Sequencing (one PR per slice)

- **RC-1** allocator rc-header + `__fern_rc_is_unique` + gating scaffold.
- **RC-2** `__fern_rc_inc` / `__fern_rc_dec`.
- **RC-3** `__fern_closure_drop`; escaping-closure whole-program tests.
- **RC-4+** struct/array drop thunks; later, freelist reuse (goal 2).

## Validation

Every slice diffs the SSA-emitted native binary's exit code against the
tree-walking interpreter oracle (`programMatchesInterp`) — the same
independent oracle the integer / Option / closure whole-program tests use
— plus focused unit tests on each helper's guard chain (null, low-address,
static sentinel, rc==1 vs rc>1).
