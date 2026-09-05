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

A bare `OpConstFunc` value lifts to a capture-free `OpMakeClosure(0)`,
whose four words are all compile-time constants — so both native SSA
backends emit it as one immortal `.rodata` cell per target rather than
allocating one per evaluation, alongside the string literals and enum
sentinels. Those cells are what the immortal sentinel is for here: the
helpers must honour it, and the reuse pass must read `is_unique` false on
one, or a token would be handed back pointing at read-only memory.

## Approach

Mirror the native rc semantics **exactly** so the helper bodies port
almost verbatim (the instruction selection differs, the layout and guard
constants do not). This is forced, not chosen: the helpers and the IR
call sites already agree on the contract; the SSA path must join it.

### Allocator

`OpAlloc` hands back a bare 16-aligned block, exactly what the SSA evaluator's
heap does and what the IR expects: the IR writes `rc = 1` at `base+0` itself
and uses `base+8` as the data pointer, so the flat backend, the evaluator and
this emitter agree on where a block starts. The emitter-built cells - closure
environments and closure cells (`closureLines`), `dyn` boxes (`boxDynLines`) -
lay the same header down themselves, with the payload size at `base+4` for
`__fern_closure_drop`. The Option[IoError] and Result[void, IoError] boxes the
I/O helpers return (`emitOptionBox`, `emitResultUnitBox`) are ordinary rc = 1
boxes of 24 bytes in every arm, None included: that is the IR's uniform enum
box size plus the rc header, so the IR frees them like any other box. The flat
backend marks its helper-built boxes immortal instead. Reader and Writer
handles carry the immortal sentinel here too and are never freed.

Every block, whether from a compiled `OpAlloc`, a hand-written helper or
`__alloc` itself, comes out of one allocator, `__alloc(n)`: the size is rounded
to the flat backend's classes (16-byte exact-fit up to 2048 B, 3-significant-bit
capacities above, bump-only past 1 GiB), the class's freelist is popped when it
has a block, and otherwise the cursor advances by the rounded size. Compiled
code and the helpers that allocate mid-computation reach it through
`__ssa_alloc_pres`, a trampoline that preserves every register and the flags
(size in x16, base back in x16), so an allocation can be spliced anywhere
without knowing what is live around it. The sites that still bump inline (the
string producers, the helper-built boxes, two dirent scratch buffers) advance
the cursor by their unrounded size, and every allocation, `__alloc`'s bump path
included, 16-aligns its base: a block's physical extent is therefore at least
`roundup16(size)`, which is its class for every size at or below 2048 B and
for an exact power of two above it, so a later free never pushes a block onto
a class its extent does not cover. An inline site that bumps a non-power-of-two
size above 2048 B would break that and has to go through `__alloc`.

**What is reclaimed.** `__free(base, n)` pushes the block onto its class's
intrusive list, and everything the IR releases reaches it: struct and enum
boxes through `__fern_box_free(data, size)` (base `data-8`, `size+8` bytes),
arrays through `__fern_arr_dec` at rc == 1 (base `data - max(16, stride)`,
header plus `cap*stride`; the `__fern_drop_arr_*` wrappers release the elements
first), closure cells through `__fern_closure_drop` (the payload size at
`data-4`) and their environments through the IR's `__closure_drop_*` thunks,
map buffers and handles through `__fern_map_drop`, and a mispaired reuse token
through `__alloc_reuse`. `__fern_rc_dec` does not free at zero, like the flat
backend's: the IR gates every release on rc == 1 and frees through the
type-specific drop.

**What is not.** Strings: `__fern_str_dec` still leaks at rc == 1, because not
every string producer puts the block base at `ptr-8` (`strbuf_take` hands out
a 16-byte-headed buffer), and a push from the wrong base would hand `__alloc`
an undersized block. Fixing that means either a uniform string header or a
size word every producer writes. Until then a string-heavy loop still grows;
an allocation-heavy one over structs and arrays does not:
`examples/bench/pmap_insert.fern` holds at 2 MB peak RSS from 1000 to 8000
entries where it was 4 / 8 / 16 / 32 MB before (#8069), the same as the flat
build.

Reclamation is a memory fix, not a speed fix: disabling the flat backend's
freelist pop reproduced this backend's old memory profile row for row on the
allocation-heavy benchmarks and cost 3-15% of the time
(`docs/SSA-REGALLOC-PLAN.md`, "Reclamation is a memory fix, not a speed fix").

The x86-64 SSA emitter still adds its own 8-byte header under the IR's on every
`OpAlloc` and never frees; it is not in this slice.

### Helper port order (leaf-first)

1. **Allocator rc-header** + **`__fern_rc_is_unique`** (a leaf: guards +
   rc compare, no calls, no frame). Smallest coherent, testable unit — an
   SSA program that allocs a cell and calls `is_unique` returns `1`; on a
   null/sentinel operand returns `0`.
2. **`__fern_rc_inc` / `__fern_rc_dec`** — the same guard chain; `inc`
   bumps the rc word, `dec` decrements; neither frees (the drop helpers do).
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
- **RC-4** freelist reuse: landed with #8069. The struct/array drop thunks
  come from the IR and are shared with the flat backend. Open: string
  reclamation (see "What is not" above).

## Validation

Every slice diffs the SSA-emitted native binary's exit code against the
tree-walking interpreter oracle (`programMatchesInterp`) — the same
independent oracle the integer / Option / closure whole-program tests use
— plus focused unit tests on each helper's guard chain (null, low-address,
static sentinel, rc==1 vs rc>1).
