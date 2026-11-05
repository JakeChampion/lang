# Bare metal: hosts, guests, drivers, kernels

Status: [plan] the scope is decided and the sequencing below is accepted. The
guest posture is built: the compiler emits a freestanding artifact (#14245, the
port of epic #6506's codegen half). The rest is not built. Epic #14248 tracks
the work, with one sub-issue for each piece and each milestone.

This plan names historical or proposed source paths. The reference documentation identifies current files.

## The decision

Fern targets the whole stack. Not "hosted, plus a freestanding library shape for
embedders" — **hosted applications, guest libraries linked into someone else's
firmware, device drivers, and kernels**, with the language able to be the top of
the artifact rather than always a guest inside one.

The long-term goal that anchors this is **an entire operating system written from
the ground up in Fern** — see the north-star section below for what it demands and
why the memory model makes it coherent rather than fantasy. It is unscheduled, and
deliberately so; it earns its place here as the thing the design must not
foreclose on, not as work in flight.

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
| **Guest library** | the embedder's C/asm | nothing — shipped as assembly (`docs/FREESTANDING-CORE.md`) |
| **Driver** | a kernel Fern does not write | volatile/MMIO, barriers, interrupt-safe RC |
| **Kernel** | Fern | all of the above, plus entry, vectors, memory layout, atomics |

The guest posture is the cheap one: no `_start`, exported symbols, and a heap
that the embedder supplies. Everything hard lives in the bottom two rows.

**Entry is a target-descriptor property, not one canonical answer.**
`Descriptor.Entry` in `internal/pkg/platforms` has three values: `EntryProcess`,
`EntryExports` and `EntryReset`. Freestanding is `EntryExports`, the guest answer.
A kernel target is a second descriptor with `EntryReset`, not a rewrite. The
compiler reads the entry shape (`platforms.entry_shape`), and its backends key on
`asmcore.process_entry`, not on the target name.

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

### Interrupts as messages: the TamaGo shape

TamaGo (see "Prior art" below) uses the first option in a real system. Its
interrupt handler is a few lines of assembler. The handler sends the IRQ number to
a goroutine (`ServiceInterrupts`, `os/signal.Relay`). The goroutine does the work
as ordinary code. When no goroutine can run, the `Idle` hook executes WFI or HLT.

The Fern form has three parts:

- **The top half** is a function with the `interrupt` attribute. The checker
  limits it to scalars and `__raw_*` operations, with no allocation and no RC
  operation. It records that the IRQ occurred, and then it returns.
- **The bottom half** is a `std/async` task that waits on an IRQ pollable. An
  `irq_pollable(id)` constructor is gated on a capability. This obeys the rule
  that authority lives on the constructor (`docs/FREESTANDING-CORE.md`).
- **Idle** is the empty wait of the reactor. When no task can run, the reactor
  executes WFI or HLT until the next interrupt.

With this shape, all code that touches a reference count runs in the main
context. The rule needs a checker, not a runtime mechanism.

**Do not preempt the kernel heap.** TamaGo has no asynchronous preemption of
goroutines, and it keeps at most one OS thread for each CPU (`M <= P`). The Fern
kernel takes the same rule. A context switch occurs only at a wait in the reactor
or between processes. Thus no interrupt stops the kernel heap in the middle of an
RC operation. Per-process heaps make the switch between processes safe (see the
north-star section).

## The mechanical pieces, in dependency order

### 1. Volatile

Two triggers, and they are independent: MMIO (a device register read must not be
hoisted out of a spin loop, a write must not be coalesced or dropped) and, later,
shared memory.

**The SSA backend already removes an unused load.** `ssa.prune_dead` in
`compiler/ssa.fern` keeps an instruction only if it has an effect or a reader.
`inst_has_effect` gives no effect to the load kinds, and this includes
`__raw_load_ptr` (kind 52) and `__raw_load8` (kind 54). Thus the compiler removes a
read of a read-to-clear device register if nothing reads its value.

The other passes do not move or merge a load. `ssa.merge_duplicates` leaves loads
alone, and `ssa.speculable` does not move a load out of a branch arm. No pass
forwards a store to a load or moves a load out of a loop.
`PERFORMANCE-RESEARCH.md`'s headline recommendation is the cross-block
optimisation work that ends those two facts.

So an MMIO access is not a plain load or store. The `unsafe` intrinsics
`__mmio_load8`, `__mmio_load16`, `__mmio_load32` and `__mmio_load64`, and the
matching `__mmio_store*`, lower to the IR ops `mmio_load` and `mmio_store`
(#14249). The SSA lift makes each one a host call (kind 50), which
`inst_has_effect` marks as an effect, `merge_duplicates` does not merge and
`speculable` does not move. Each backend then emits one load or store of the
width. `internal/testing/e2ecompiler/self_host_mmio_test.go` makes sure that an
unread MMIO load and two loads of one register stay in the output. A `volatile`
type qualifier can follow the need for drivers in Fern, not come before it.

### 2. Effect intrinsics, not an asm block

The privileged instructions have no data-flow shape: `msr`/`mrs`, `csrrw`, `wfi`,
`dsb`/`isb`/`fence`, cache and TLB maintenance. They are pure effects.

Extend the existing floor (`docs/RUNTIME-INTRINSICS.md`) with named ops —
`__raw_barrier`, `__raw_wfi`, `__raw_csr_read`/`_write` — rather than opening a
general `asm` block. The floor's ~14 ops work because each is a *portable
concept* with a per-target emitter; that property is what a general asm block
gives up.

General inline asm costs a register-constraint sublanguage, clobber lists, and an
ABI contract. Each emitter pays that cost: `compiler/asm_arm64_ir.fern` and
`compiler/asm_ir.fern`, which are deliberately parallel per `CLAUDE.md`. The SSA
lift must also admit an op that it cannot see into, and the register allocator
must obey the constraints of that op. Defer it until a real target
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

**One checked property serves three uses.** TamaGo requires `Hwinit0`, the early
hardware setup, to be assembler. The reason is that no code can allocate before
the Go runtime starts. Fern can do better with one property that the checker
enforces: no allocation and no RC operation. The property covers three cases:

- early setup before the heap exists, for example the FPU enable in
  `CPACR_EL1`, or the MMU setup
- the top half of an interrupt handler
- code that touches DMA memory.

Then the early setup is Fern code with effect intrinsics, not assembler.
`interrupt` implies the property, and a reset entry requires it.

### 4. Memory layout

Real firmware needs section placement (`.text` at the reset address, `.bss` in
SRAM, an MMIO region that is never allocated into) and a defined stack. Fern's
in-process linkers emit *its* binaries, which is an advantage here — there is no
external linker script to interoperate with, so this can be a descriptor-driven
layout rather than a text format we have to parse. #6511's embedder-supplied heap
region (`__fern_heap_init`) is the first half of this already.

The addresses belong to the board, not to the ISA. TamaGo first put its layout in
three variables of the board package (`RamStart`, `RamSize`, `RamStackOffset`) and
a `-T` link flag. Its `GOOS=custom` proposal moves the layout into a `mem`
package. Fern can do the same from the start: the descriptor holds the ISA rules,
and the board package (section 6) holds the addresses. A new board is then a new
package, not a compiler change.

### 5. Atomics

Genuinely absent — there is no atomic surface in the language at all (the
`atomic` hits in the tree are Go test-harness `sync/atomic` and atomic *file*
writes). Needed
for SMP kernels and lock-free structures, and gated on the memory-model decision
above rather than on anyone writing a `Mutex`. Ordering it last is deliberate:
under the handler-owns-nothing rule, single-core drivers and kernels do not need
it.

### 6. Board packages: who supplies a capability

A TamaGo board package supplies about ten functions and variables to the runtime:

- startup: `CPUInit` (the first instruction), `Hwinit0` (before the runtime
  starts) and `Hwinit1` (after it starts)
- services: `Printk(c byte)`, `Nanotime() int64` and `GetRandomData(b []byte)`
- memory: `RamStart`, `RamSize` and `RamStackOffset`
- optional: `Idle(until)`, `Exit(code)`, and `ProcID`, `Task` and `Wake` for SMP.

The Fern freestanding targets grant **no** capability, and the descriptor fixes
that set. But a board can supply several capabilities. A UART gives `log`, the
generic timer gives `now`, and `RNDR` or `RDRAND` gives `random`. The descriptor
cannot know which capabilities a board gives. Without a way to say so, each
freestanding program calls its own UART driver directly, and `print` is not
available.

So the missing piece is a **capability provider**: a Fern module that implements
a capability for a target. The E066 gate then grants each capability that the
provider implements, in addition to the descriptor set. The parity tests between
`internal/pkg/platforms` and `compiler/platforms.fern` must cover the provider
rule too. The mechanism that names the board module for a build is not decided.

Go gets this slot late. TamaGo keeps a Go fork for each Go release, and
golang/go#73608 (`GOOS=custom`) asks upstream Go to accept a user package as the
OS layer. Fern owns its compiler, so the slot can be part of the design from the
start.

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
  surface exists it wants to be a **capability** in `internal/pkg/platforms`
  (`simd`). That would be the first time E066 gates a *CPU* feature rather than
  an OS one — a real widening of what the capability system means, and worth
  deciding deliberately rather than discovering.

## The north star: an operating system written in Fern

The long-term goal is a complete OS built from the ground up in Fern — bootloader,
kernel, drivers, filesystem, userspace. It is not scheduled and nothing here
depends on it, but it is the right thing to design against, because it is a
**forcing function with an unambiguous finish line**: the self-host compiler
running *on* FernOS, compiling Fern. Nothing gets to be hand-waved past that test.

### The memory model and the OS design agree — which is why this is coherent

The interrupt hazard above reads like a reason an OS is out of reach. It is the
opposite. An OS's natural structure is **processes that share no memory**, and
that is precisely #5366's share-nothing shape: one heap per process, no object
graph spanning a boundary, so **RC stays non-atomic and C1 stays true even under
a preemptive scheduler.** A context switch between processes touches no shared
refcount because there is nothing shared to touch.

So the hard part collapses to a much smaller surface than "an OS needs atomic RC":

- **Userspace processes** — safe by construction, per-process heaps.
- **The kernel's own heap** — the one genuinely shared graph, and the only place
  the interrupt rule has to hold. Handlers own nothing shared, per above.

That is a real design result, not encouragement: the memory model people assume
disqualifies Fern from kernels is the same model that makes the process boundary
free.

### Fern on both sides of the syscall boundary

Today Fern *consumes* a syscall ABI (`__syscall3`/`4`/`5`). An OS makes it the
*provider*: trap entry, register save, dispatch, return-to-user. That is the
`naked` / `interrupt` attribute work plus a stable ABI decision, and it is the
point at which Fern's two roles need spelling separately.

**#6529 already built the slot.** Targets are `<isa>-<environment>` with
capabilities on the environment (`native`, `wasi`, `wasi-http`, `freestanding`).
Kernel-mode and user-mode FernOS are two more environments —
`arm64-fernos-kernel` grants MMIO and privileged effects and no `fs`;
`arm64-fernos` grants the ordinary hosted set over FernOS's own syscalls. The
capability system then does something neat: it enforces the kernel/user split at
check time, in the same mechanism that enforces the OS boundary today.

### What an OS needs that "a kernel" did not

- **Physical memory.** #6511's embedder-supplied heap is the guest half. A kernel
  *produces* memory: page-frame allocator, page tables, then the Fern heap on
  top of that, then a fresh heap per process.
- **DMA** (#14255). Volatile is not sufficient. A driver needs physically contiguous,
  uncached (or explicitly cache-maintained) allocations, and physical addresses
  distinct from virtual ones. This is an allocator feature, and it is the one
  driver requirement the list above genuinely misses. TamaGo keeps DMA regions
  outside the Go runtime, so that the GC never touches them. For Fern, DMA memory
  is a second region that the RC machinery never sees, used through the
  `__raw_*` surface. It is not a flag on ordinary boxes.
- **Panic scope** (#14257). A Fern panic stops the program: `__fern_report` exits the
  process, and off the process entry it is a trap (#14245). eggshell's kernel
  uses no unsafe code, and a panic during a syscall ends the process that made the call,
  not the kernel. Per-process heaps let FernOS do the same: discard the heap of
  the process and end only that process. Decide this before milestone 4,
  because it decides what a panic in kernel code means.
- **A network stack in Fern** (#14259). TamaGo uses the pure-Go gVisor TCP/IP stack over
  virtio-net. The Fern `tcp_*` builtins are host syscalls. A kernel or a
  unikernel needs a TCP/IP stack in Fern, core-safe by the `std/` partition, over
  a virtio-net driver.
- **A sanctioned raw-pointer surface** (#14256). `__raw_*` is a **self-host-compiler
  internal**. In `internal/`, only the capability tables of
  `internal/pkg/platforms` name it. An OS needs it as a
  *language* surface, capability-gated. Being self-host-only is the right side of
  `docs/NATIVE-CONVERGENCE.md` (new surface lands self-host-first), so this is a
  promotion to public surface rather than a port.
- **ELF loading**, to run userspace binaries — adjacent to the in-process linkers
  Fern already ships, which emit ELF rather than shelling out to one.
- **Boot protocol** — device tree, UEFI, or multiboot parsing. Ordinary Fern code
  once volatile and memory layout exist; no new language surface.

### Milestones, each producing something that runs

1. Boot to a serial "hello" on a microVM (Firecracker or QEMU `microvm`), and on
   qemu-virt for arm64 — proves entry, layout, MMIO (#14260).
2. Interrupts and a timer tick — proves the handler rule under real preemption
   (#14261).
3. A page-frame allocator and the Fern heap on top of it (#14262).
4. Two processes, separate heaps, preemptively scheduled — the C1 claim, tested
   (#14263).
5. A filesystem and an ELF loader (#14264).
6. The self-host compiler runs on it (#14265).

Step 4 is where the design either holds or does not, and it is reachable long
before anything resembling a usable OS. Worth getting to early for that reason
alone.

**Start on a microVM.** The most active TamaGo targets are Firecracker, Cloud
Hypervisor and QEMU `microvm`. Their device model is small: a serial port and
virtio-mmio, with no PCI. Firecracker loads the kernel ELF directly, with no
bootloader. That makes milestone 1 shorter.

A microVM also fits the workload that Fern is best at: a Fern HTTP handler that
boots directly on Firecracker is a product, not only a test.

**Reach milestone 6 through the Linux ABI.** eggshell is a TamaGo kernel that
implements the Linux syscall ABI. It runs unmodified static Linux programs, and
the Go toolchain is one of them. The Fern compiler is a static Linux ELF that
makes raw syscalls and uses no libc.

Thus a FernOS kernel that implements the syscalls of the compiler can run the
`arm64-linux` compiler with no change. `strace -c` on a compile gives that set
of syscalls. The eggshell list of limits shows what a first kernel can omit:
`ptrace`, SysV IPC, inotify, mounts and namespaces. An `arm64-fernos` user
environment over the FernOS syscalls can come later, as an improvement.

## Sequencing

1. **Done: the freestanding codegen is in the compiler** (#14245). It reads the
   entry shape from the descriptor, so `EntryReset` can be a second value.
2. **Done: MMIO loads and stores that no pass removes** (#14249). The compiler
   still removes an unused plain raw load (see "Volatile").
3. **Decide the interrupt/RC rule** (#14250). Design work, no code, and it gates 3 of the
   5 mechanical pieces. The proposed answer is the TamaGo shape above: a checked
   top half, a bottom half in the reactor, and no preemption of the kernel heap.
4. Then: capability providers (#14251) → effect intrinsics (#14252) → function
   attributes and the no-RC property (#14253) → memory layout (#14254) →
   volatile qualifier (if drivers materialise) → atomics (#14258, if SMP does).
5. Beside those: the DMA region (#14255), the raw-pointer surface (#14256), the
   panic scope decision (#14257, before milestone 4) and a TCP/IP stack in Fern
   (#14259).

## Prior art: TamaGo, GoTEE and eggshell

Sources: the READMEs and wikis of `usbarmory/tamago`, `usbarmory/GoTEE` and
`usbarmory/eggshell`, read 2026-11-05.

- **TamaGo** runs Go on bare metal with no C and no OS. It adds `GOOS=tamago` to
  the Go toolchain. The board package supplies the hardware layer (section 6).
  Targets include i.MX6UL and i.MX8M boards, Firecracker, Cloud Hypervisor, QEMU
  `microvm`, UEFI and RISC-V SoCs.
- **GoTEE** runs a privileged Go unikernel (the Trusted OS) and an unprivileged
  Go applet on one core, split by ARM TrustZone or RISC-V PMP. The applet calls
  the Trusted OS with `SVC`. The applet package maps `runtime.printk` onto a
  syscall, so `fmt` works in the applet. That is a capability provider whose
  implementation is a trap. It is the same split as `arm64-fernos-kernel` and
  `arm64-fernos`.
- **eggshell** is an early, experimental TamaGo kernel that implements the
  Linux syscall ABI. Each user thread is a goroutine. A syscall that blocks is a
  wait in the Go scheduler. The SSH server runs in the kernel as Go code. The
  README calls this a trade of privilege separation for a memory-safe parser.

The sections above contain the lessons. They are:

- interrupts as messages, and no preemption of the kernel heap
- one no-RC property, and DMA outside the heap
- board packages that supply capabilities
- panic scope, and a network stack in Fern
- microVMs first, and the Linux ABI for milestone 6.

Do not copy these parts:

- **A fork for each release.** TamaGo rebases its Go fork on each Go release.
  Fern owns its compiler and does not need a fork.
- **Ownership that a GC hides.** The TamaGo interrupt handler can ignore
  ownership because it is assembler and the GC owns all memory. Fern must enforce
  the handler rule in the checker.
- **The in-memory filesystem.** TamaGo took it from NaCl. It is a good start, not
  a design.

## Non-goals, explicitly

- **A general `asm` block**, until the intrinsic floor demonstrably cannot
  express a real target's needs.
- **Atomic refcounts.** C1 stays true; the handler restriction is how.
- **A portable vector type.** ATLAS §1.2 already rejected it; bare metal does not
  reopen it.
- **A new backend.** riscv64/RV32 remain the motivation for this work and a
  separate roadmap decision, exactly as #6506 says.
