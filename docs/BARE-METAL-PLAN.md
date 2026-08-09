# Bare metal: hosts, guests, drivers, kernels

**Status: [plan] — the scope is decided, the sequencing below is accepted, none
of it is built.** Epic #6506 (the freestanding core) is the prerequisite and is
in flight; this doc is what comes after it and what it must not paint into a
corner.

## The decision

Fern targets the whole stack. Not "hosted, plus a freestanding library shape for
embedders" — **hosted applications, guest libraries linked into someone else's
firmware, device drivers, and kernels**, with the language able to be the top of
the artifact rather than always a guest inside one.

That is a widening of `docs/LANGUAGE-DIRECTION.md`'s positioning, which already
said general-purpose but drew its examples from CLI tools, edge handlers and the
self-host compiler — all hosted. Systems programming was listed as a known "poor"
in `LANGUAGE-REVIEW-2026-07` (no raw pointers, no volatile, no atomics, no inline
asm). It is now a target, not a gap we accept.

## Four postures, and what each actually demands

`docs/FREESTANDING-CORE.md` settled *which builtins need a host*. That is the
capability question. This is the different question of **who owns the machine**.

| Posture | Who owns reset / vectors / MMU | What Fern must add |
| --- | --- | --- |
| **Hosted app** | the kernel | nothing — shipped |
| **Guest library** | the embedder's C/asm | nothing beyond #6510–#6512 |
| **Driver** | a kernel Fern does not write | volatile/MMIO, barriers, interrupt-safe RC |
| **Kernel** | Fern | all of the above, plus entry, vectors, memory layout, atomics |

The guest posture is the cheap one, and it is the one #6510 currently assumes
without saying so: no `_start`, exported symbols, embedder-supplied heap
(#6511). Everything hard lives in the two right-hand rows.

**Entry is a target-descriptor property, not one canonical answer.** #6510 should
not settle on "exported symbols" as *the* freestanding shape — that is the guest
answer, and hard-coding it makes the kernel posture a rewrite rather than a
second descriptor field. A freestanding descriptor needs to be able to say *this
target is entered at a reset vector* as easily as *this target is a set of
exported symbols*.

## The thing that is actually in the way

Not inline asm. **The interrupt is the second thread.**

`docs/MULTICORE-RESEARCH.md` C1: Perceus refcounts are non-atomic and must never
be touched from two contexts. Every discussion of that constraint so far has been
about threads and cores, so the mitigation on file — share-nothing workers with
per-worker heaps (#5366) — is framed around multicore.

Bare metal reintroduces the same hazard **on a single core with no threads at
all**. An interrupt handler preempts the main context at an arbitrary
instruction. If the handler touches any RC'd Fern value that the main context can
also reach, the inc/dec is a read-modify-write racing against a preempted
read-modify-write, and the refcount is silently wrong. No `Mutex` fixes it,
because the main context cannot hold a lock across code it does not know is
interruptible.

This is the design constraint that shapes the whole posture, and it wants
deciding before any of the mechanical features below:

- **Handlers own nothing shared.** An interrupt handler gets its own region, or
  is restricted to a non-allocating subset (scalars, raw pointers, `__raw_*`),
  and the checker enforces it. This is #5366's share-nothing shape applied to a
  boundary that is not a thread — and it is the option that keeps RC non-atomic,
  which C1 wants to stay true.
- **Or** RC becomes atomic inside handler-reachable code, which is the
  marked-shared design C1 leaves open and a much larger change.

The first is right. The point to internalise: *the kernel story is a memory-model
question wearing a codegen question's clothes*, and getting it wrong produces
heisenbugs rather than build failures.

## The mechanical pieces, in dependency order

### 1. Volatile

Two triggers, and they are independent: MMIO (a device register read must not be
hoisted out of a spin loop, a write must not be coalesced or dropped) and, later,
shared memory.

**Free right now, expensive later.** There is no GVN, no load forwarding, and no
global load elimination anywhere in `internal/ir` or the SSA backends — `dce.go`
is reachability-only, so it cannot drop an unused load. The raw memory ops
survive today by accident, and `PERFORMANCE-RESEARCH.md`'s headline
recommendation is exactly the cross-block optimisation work that ends that.

So the cheap move is to **write the constraint down and pin it with a test before
the optimiser gets clever**, not to add a `volatile` type qualifier now. A
qualifier only earns its keep when users write drivers in Fern — which is the
same trigger as MMIO, so it can follow the need rather than precede it.

### 2. Effect intrinsics, not an asm block

The privileged instructions have no data-flow shape: `msr`/`mrs`, `csrrw`, `wfi`,
`dsb`/`isb`/`fence`, cache and TLB maintenance. They are pure effects.

Extend the existing floor (`docs/RUNTIME-INTRINSICS.md`) with named ops —
`__raw_barrier`, `__raw_wfi`, `__raw_csr_read`/`_write` — rather than opening a
general `asm` block. The floor's ~14 ops work because each is a *portable
concept* with a per-target emitter; that property is what a general asm block
gives up.

General inline asm costs a register-constraint sublanguage, clobber lists, and an
ABI contract — paid **four to six times**, because there are two independent
instruction-selection layers per ISA (`x86_64` and `x86_64ssa`, `arm64` and
`arm64ssa`), deliberately parallel per `CLAUDE.md`. Defer it until a real target
proves the intrinsic floor cannot express something, and expect that to be rarer
than it sounds.

Note the distinction that keeps getting blurred: the backends **already emit
hand-written asm** (`_start`, the syscall bundle, `asmcore`). A bare-metal reset
sequence is one more compiler-emitted blob. *Compiler-emitted asm is not the same
feature as user-writable `asm`, and only the latter is a language change.*

### 3. Function-shape attributes

Interrupt handlers need a specific prologue/epilogue and `eret`/`mret`; a reset
vector needs no prologue at all. That is a calling convention, so it wants
`naked` / `interrupt` function attributes — not asm, and not a new expression
form. Pairs directly with the handler-restriction rule from the memory-model
section: the attribute is the natural place to hang the checker's enforcement.

### 4. Memory layout

Real firmware needs section placement (`.text` at the reset address, `.bss` in
SRAM, an MMIO region that is never allocated into) and a defined stack. Fern's
in-process linkers emit *its* binaries, which is an advantage here — there is no
external linker script to interoperate with, so this can be a descriptor-driven
layout rather than a text format we have to parse. #6511's embedder-supplied heap
region is the first half of this already.

### 5. Atomics

Genuinely absent — there is no atomic surface in the language at all (the
`atomic` hits in the tree are Go test-harness `sync/atomic` and atomic *file*
writes). Needed
for SMP kernels and lock-free structures, and gated on the memory-model decision
above rather than on anyone writing a `Mutex`. Ordering it last is deliberate:
under the handler-owns-nothing rule, single-core drivers and kernels do not need
it.

## Vector

**Already planned, and not a bare-metal feature.** `docs/ATLAS-PLATFORM-PLAN.md`
settles it: fused SIMD intrinsics (§3), with a portable vector *type* explicitly
rejected as the wrong first shape (§1.2) and the CPU dispatcher shown unnecessary
because the declared baselines already promise SIMD (§1.1). Do not re-plan it
here.

Bare metal is an argument *against* SIMD rather than for it, and adds exactly
three deltas to that plan:

- Firmware commonly comes up with the SIMD/FPU unit **disabled** (ARM64 requires
  writing CPACR_EL1 to enable it), so a kernel posture must not assume vector
  instructions are legal at entry.
- Interrupt handlers avoid vector registers to keep save/restore cheap — which
  the fused-intrinsic design happens to make easy, since no vector value is live
  across an op boundary.
- Some freestanding targets have no vector unit at all, so the day a vector
  surface exists it wants to be a **capability** in `internal/platforms`
  (`simd`). That would be the first time E066 gates a *CPU* feature rather than
  an OS one — a real widening of what the capability system means, and worth
  deciding deliberately rather than discovering.

## Sequencing

1. **Finish #6506** — #6510, #6511, #6512. Nothing here is blocked on the
   features above, and inventing them before the freestanding shape is real means
   inventing the wrong ones.
2. **#6510 must keep both entry shapes open** (see above). This is the one place
   the in-flight epic can foreclose on the direction, so it is the one place to
   push now.
3. **Pin the no-load-elimination-of-raw-ops constraint** with a test and a note.
   Cheap insurance; a silent miscompile otherwise, on whatever day global
   optimisation lands.
4. **Decide the interrupt/RC rule.** Design work, no code, and it gates 3 of the
   5 mechanical pieces.
5. Then: effect intrinsics → function attributes → memory layout → volatile
   qualifier (if drivers materialise) → atomics (if SMP does).

## Non-goals, explicitly

- **A general `asm` block**, until the intrinsic floor demonstrably cannot
  express a real target's needs.
- **Atomic refcounts.** C1 stays true; the handler restriction is how.
- **A portable vector type.** ATLAS §1.2 already rejected it; bare metal does not
  reopen it.
- **A new backend.** riscv64/RV32 remain the motivation for this work and a
  separate roadmap decision, exactly as #6506 says.
