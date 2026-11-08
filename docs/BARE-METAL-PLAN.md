# Bare metal: hosts, guests, drivers, kernels

Status: [plan] the scope is decided and the sequencing below is accepted. The
guest posture is built: the compiler emits a freestanding artifact (#14245, the
port of epic #6506's codegen half). Milestones 1 and 2 run on qemu in CI: a
kernel image on each ISA boots, takes timer interrupts and stops the VM. The
rest is not built. Epic #14248 tracks the work, with one sub-issue for each
piece and each milestone.

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
or between processes. An interrupt can arrive in the middle of an RC operation,
but its top half touches no RC value, and no other context runs RC code before
the main context continues. Per-process heaps make the switch between processes
safe (see the north-star section).

### The decision (#14250)

The rule above is the decision. These are its details.

**The shared state is raw memory.** Fern has no module-level mutable state, and a
top half holds no RC value. So the only state that a top half shares with the main
context is the IRQ region: raw memory that the memory layout reserves (#14254). The
region holds one pending word for each 32 IRQ lines. Both sides access it only
with `__mmio_load32` and `__mmio_store32`, so that no pass removes, merges or
moves an access (#14249).

**What a top half can do.** A top half has the no-RC property (#14253). It can:

- read its IRQ number, which is a scalar argument
- read and write device registers and the IRQ region with `__mmio_*`
- use scalar arithmetic and the effect intrinsics (#14252), for example to
  acknowledge the IRQ at the interrupt controller
- call only functions that have the no-RC property.

It cannot allocate, read or write an RC value, call a function without the
property, or wait.

**How a top half wakes the reactor.** The top half sets the bit of its IRQ in the
pending word, and returns. That is a read, an OR and a write of one word. The main
context clears bits only while IRQs are masked, so on one core the two
read-modify-write sequences cannot interleave. No atomic operation is necessary,
and C1 stays true. SMP needs one IRQ region for each core, or atomics (#14258).

**The idle wait cannot lose a wakeup.** When no task can run, the reactor:

1. masks IRQs with `__irq_mask()`
2. reads the pending word of each line that a task waits on, and clears the bit
   of the first line that fired
3. if no bit was set, waits for one interrupt with `__irq_wait()`, then goes
   back to step 2
4. unmasks IRQs with `__irq_unmask()`, and wakes the task of that line.

`__irq_wait()` starts and ends with IRQs masked. Between the two, the top
halves of the interrupts that arrive run. On arm64, it is `wfi`, then `msr
daifclr, #2`, then `msr daifset, #2`. A pending IRQ wakes WFI also when PSTATE
masks it, so an IRQ between steps 2 and 3 is not lost. The unmask then lets its
top half set the bit before the mask returns. On x86-64, it is `sti`, `hlt`,
`cli`. `sti` delays the interrupt until after the next instruction, so the same
IRQ wakes `hlt`, and its top half runs before `cli`.

A bit that step 2 does not clear stays set for the next wait. Thus two lines
that fire together each wake their task.

**The bottom half is a task.** Each IRQ line that a driver uses has one
`std/async` task. The task waits on `async.irq(line)`, a future that is ready
after the line fired. On a kernel target, the pairs of a wait name IRQ lines.
The machine's wait in `wait_any` is the loop above (`std/async`, chunk 32). It
is the only code that clears a pending bit, and it runs with IRQs masked. Thus
the read-modify-write of the top half and that of the reactor cannot
interleave on one core.

The task table holds RC values, and only the main context touches it. A kernel
has no clock, so a wait with a deadline stops the program with a message. The
IRQ region is `.bss` of the kernel image, `__fern_irq_pending`. It holds 32
words for 1024 lines, and `__irq_pending()` names it.

**A trap preempts a user process.** A timer IRQ during a user process enters the
kernel through a top half. The top half saves the registers of the process in raw
memory and sets the timer bit. Then it returns to the kernel main context at its
last wait. The kernel heap was not active, so nothing is half done.

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

The first one exists: `__fence()` (#14252). Every memory and device access before
it completes before any access after it starts. It is `dsb sy` on arm64 and
`mfence` on x86-64, and it does nothing on wasm and in the interpreters. It runs at
every privilege level, so it needs no `unsafe` block. A driver uses it, for
example, between the writes to a DMA ring and the MMIO write that rings the
doorbell. It takes the host-call path of the MMIO ops, so no pass removes it or
moves an access across it.

The first privileged ops exist too (#14252). Only the `kernel` environment
grants them, through the `privileged` capability, so a hosted target refuses
each one with E066:

- `__wfi()` waits for an interrupt. It is `wfi` on arm64.
- `__hvc(fid, a1, a2, a3)` and `__smc(fid, a1, a2, a3)` call the hypervisor and
  the secure monitor in the SMC calling convention. They put the arguments in
  x0 to x3 and return x0. A board stops the VM with PSCI `SYSTEM_OFF` through
  one of them. They need an `unsafe` block, because a call such as PSCI
  `CPU_ON` starts a core at an address.
- `__port_in8`, `__port_in16` and `__port_in32` read an x86 I/O port, and
  `__port_out8`, `__port_out16` and `__port_out32` write one. An x86-64 board
  reaches its serial port and the qemu exit device through them. They need an
  `unsafe` block. arm64 has no port space, so its emitter refuses them.
- `__boot_info()` gives the boot argument that the loader put in a register for
  the reset entry, as a `usize`. On `arm64-kernel` it is x0. The Linux boot
  protocol puts the address of the device tree there. qemu-virt gives 0 to an ELF
  image. On `x86-64-kernel` it is `%ebx`, the address of the PVH start info,
  which `std/pvh` reads.
- `__sysreg_read(name)` reads a system register, and `__sysreg_write(name,
  value)` writes one. Both need an `unsafe` block, because a read of a register
  such as `icc_iar1_el1` acknowledges an interrupt. The name is a string literal,
  because the instruction encodes the register. The checker refuses with E038 a
  name that is not a literal, that names no register, or that names a register
  without the access. `compiler/sysreg.fern` holds the register table.
  - On arm64, the name is a register that `mrs` and `msr` accept, such as
    `vbar_el1`, or the generic form `s3_0_c12_c0_0`. A write is `msr` and then
    `isb`, so it takes effect before the next instruction.
  - On x86-64, the name is a control register (`cr0`, `cr2`, `cr3`, `cr4` or
    `cr8`), a named MSR such as `efer` or `kernel_gs_base`, or the generic form
    `msr_c0000080`. An MSR goes through `rdmsr` and `wrmsr`.
  - Each backend refuses a register of the other ISA.
- `__cache_clean(address)` writes back the cache line that holds the address.
  `__cache_flush(address)` writes it back and invalidates it. They need an
  `unsafe` block, because the address must be mapped. A driver cleans a buffer
  before a device reads it, and flushes it after a device writes it, then calls
  `__fence()`.
  - On arm64, they are `dc cvac` and `dc civac`.
  - On x86-64, both are `clflush`, because the baseline has no `clwb`.
- `__tlb_flush(address)` drops the translation of the page that holds the
  address, and `__tlb_flush_all()` drops every translation. A kernel calls one
  after it changes a page table.
  - On arm64, they are `tlbi vaae1is` and `tlbi vmalle1is`, between `dsb ishst`
    and `dsb ish` and then `isb`. So every core of the inner-shareable domain
    drops the translation before the next instruction.
  - On x86-64, they are `invlpg` and a reload of `%cr3`. The reload keeps the
    global pages.

- `__irq_mask()` and `__irq_unmask()` set and clear the IRQ mask of the core:
  `msr daifset, #2` and `msr daifclr, #2` on arm64, `cli` and `sti` on x86-64.
  `__irq_wait()` is the idle wait of "The decision (#14250)". `__irq_pending()`
  gives the address of the IRQ region. The reactor of `std/async` uses the four,
  and a top half uses `__irq_pending()`. None needs an `unsafe` block, because
  the loads and stores on the region do.

They take the host-call path of the MMIO ops, so no pass removes or moves them.
x86-64 refuses `__hvc` and `__smc`, and writes `hlt` for `__wfi`.

Extend the existing floor (`docs/RUNTIME-INTRINSICS.md`) with named ops like
these, rather than opening a general `asm` block. The floor's ~14 ops work because each is a *portable
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

**The decision (#14253): the property is `@norc`.** The checker reports a breach
as E102. The rule is simple, so a reader can apply it too:

- Each parameter, the result and each annotated local is a scalar.
- A call names another `@norc` function or a raw memory intrinsic: the
  `__load_*` and `__store_*` floor, the `__mmio_*` accesses, `__fence`, the
  privileged ops and the bit counts.
- A string, an array, a struct, a closure, a method, a field access, an index,
  `for` and `match` are refused.

With only scalars, no value has a box, so nothing allocates and no RC operation
occurs. The rule does not use `fip`. A `fip` function can drop an `own` value
and can rebuild a struct in a reused box, and both touch the allocator. The
attribute changes no code. `compiler/norc.fern` and
`internal/check/checker/norc.go` hold the rule.

**`@interrupt("slot")` exists on both kernel targets (#14253).** It marks an
exception handler, and it implies `@norc`. E103 refuses these handlers:

- a handler with an unknown slot, or with a slot that another handler takes
- a handler with a parameter or a result
- a handler with a slot that the target does not have, or on a target that is
  not a kernel

On `arm64-kernel`:

- The slot is `"sync"`, `"irq"`, `"fiq"` or `"serror"`, for the current level
  with SP_ELx.
- The image has a 2 KiB vector table, and the reset entry writes its address to
  `VBAR_EL1`. Each slot with a handler branches to a trampoline, and every other
  slot parks the core.
- The trampoline saves x0 to x18, x29, x30, FPSR, and q0 to q7 and q16 to q31,
  so a handler can use floating point. It calls the handler, restores the
  registers and returns with `eret`. The handler reads the cause from
  `esr_el1`, and it can step over the instruction with `elr_el1`.
- A handler is a root of the tree shake and of the lowering, as an export is.
  `parser.named_outside` is the one test for both.
- The qemu boot test reads an EL2 register from EL1. The read is undefined, so
  the sync handler runs, records the exception class and steps over the read.

On `x86-64-kernel`:

- The slot is an exception mnemonic from `"de"` (vector 0) to `"cp"` (vector
  21), or a number from `"32"` to `"255"` for an external interrupt.
- The reset entry fills a 256-entry IDT in `.bss` with interrupt gates to a
  park stub. Then it writes the gate of each handler and loads the IDT with
  `lidt`.
- The trampoline saves the nine caller-saved general registers and ymm0 to
  ymm15, because the baseline has AVX2. It clears the direction flag and calls
  the handler. Then it restores the registers, drops the error code that the
  CPU pushes for some exceptions, and returns with `iretq`.
- The CPU pushes the interrupt frame onto the current stack. The x86-64 backend
  keeps no data below `%rsp`, so this is safe, and
  `TestSelfHostKernelX86UsesNoRedZone` keeps it so.
- `__breakpoint()` is `int3`, a trap, so the handler returns to the next
  instruction. On arm64 it is `brk #0`, which takes the sync exception.

`naked` comes next.

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

**The first slice (#14254): `-target arm64-kernel`.** The target's environment
is `kernel`, with the `EntryReset` shape and only the `privileged` capability
(section 2). A board module supplies `log`. `-ram BASE,SIZE` gives the RAM that the loader puts the image
in. It is a flag until a board module can supply it. The compiler writes an
ELF `ET_EXEC` image:

- The text segment starts at BASE and is not writable. The data segment follows
  it and is not executable.
- The loader enters EL1 with the MMU and data cache off. The reset `_start`
  parks the other cores, saves x0 for `__boot_info`, sets the stack at
  BASE + SIZE, and enables the FP and SIMD unit
  (`CPACR_EL1`).
- Then it clears `.bss` and enables a 40-bit identity map with 4 KiB tables.
  The configured RAM uses Normal non-cacheable memory. Other addresses use
  Device-nGnRnE memory with execute-never, so board MMIO keeps Device attributes.
  Table storage stays outside the heap.
- It gives the heap the RAM between the image and the
  1 MiB stack. It calls `main`, and after `main` it waits with `wfe`.
- The image has 64-bit addresses, and the end of the RAM must be at or below
  1 TiB, the 40-bit physical address space. That covers qemu-virt, where the
  RAM starts at `0x40000000`, and Firecracker, where it starts at `0x80000000`.
- qemu-virt puts the device tree at the start of its RAM for an image that is
  not a Linux kernel. So `-ram 0x40200000,126M` keeps the first 2 MiB for it.

`internal/testing/e2ecompiler/self_host_kernel_test.go` reads the program
headers and the entry point. `TestSelfHostKernelBootsOnQemu` boots the image on
`qemu-system-aarch64 -M virt`. The image prints through a PL011 provider. It
copies the device tree from the start of the RAM, and `std/fdt` finds the UART
and the PSCI conduit in it. Then the image stops the VM with PSCI
`SYSTEM_OFF`. The `kernel-boot` job of the self-host lane runs it.

**The second slice (#14260): `-target x86-64-kernel`.** The image has the same
environment and the same `-ram` flag. A VMM loads it at BASE and enters it
through the Xen PVH note (`PT_NOTE`, type 18) in 32-bit protected mode with
paging off. qemu `-M microvm` and Firecracker both boot an image this way, with
no boot loader.

- The 32-bit reset code is bytes that the compiler writes, because the
  assembler encodes only 64-bit code. Its addresses are fixed at compile time,
  so it needs no relocation.
- It identity-maps the first 4 GiB with 2 MiB pages. The page tables and the
  GDT take seven pages below the 1 MiB stack, outside the heap.
- It enables SSE, AVX and XSAVE, because the x86-64-v3 baseline uses AVX2. Then
  it enables long mode and returns far to the 64-bit code. A VM must give the
  guest a CPU with AVX2, for example `-cpu max` on qemu.
- The 64-bit code saves `%ebx` for `__boot_info`, sets the stack, clears
  `.bss`, gives the heap the RAM up to the tables, and calls `main`. After `main`
  it halts.
- The PVH entry and the linker take 32-bit addresses, so the end of the RAM
  must be at or below 2 GiB.

`internal/testing/e2ecompiler/self_host_kernel_x86_test.go` checks the program
headers and the note. `TestSelfHostKernelX86BootsOnQemu` boots the image on
`qemu-system-x86_64 -M microvm`. The image prints through COM1 with
`__port_out8` and exits through qemu's `isa-debug-exit` device. It reads the
PVH start info with `std/pvh`, prints the command line, and finds its RAM in the
memory map. The
`kernel-boot` job runs it too.

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

So the missing piece is a **capability provider**: a Fern function that implements
a capability for a target. The E066 gate then grants each capability that a
provider implements, in addition to the descriptor set.

**The decision (#14251).** A board module marks its provider with
`@provide("log")`, and the program names its board with an import, as a TamaGo
program does. No flag and no package-config entry is necessary. `log` and `exit`
take providers now. The `log` signature is `(data: usize, len: i32): void`, so the
call changes no reference count and does not allocate. The `exit` signature is
`(code: i32): void`, which is TamaGo's `Exit`. A kernel image calls it from
`exit` and with the result of `main`, so a VM stops when the program ends. A
`FERN_LEAKCHECK` build prints its census through the `log` provider first.

On a target that grants the capability, the descriptor wins and the provider is an
ordinary function. Thus a hosted build keeps its own implementation.
`docs/FREESTANDING-CORE.md` ("A board supplies a capability") has the rule and E101.
`now` and `random` can take providers in the same way when a board needs them.

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
  top of that, then a fresh heap per process. `std/frames` is the page-frame
  allocator, and `__heap_adopt` moves the heap onto its frames (sequencing,
  step 9).
- **DMA** (#14255). Volatile is not sufficient. A driver needs physically contiguous,
  uncached (or explicitly cache-maintained) allocations, and physical addresses
  distinct from virtual ones. This is an allocator feature, and it is the one
  driver requirement the list above genuinely misses. TamaGo keeps DMA regions
  outside the Go runtime, so that the GC never touches them. For Fern, DMA memory
  is a second region that the RC machinery never sees, used through the
  `__raw_*` surface. It is not a flag on ordinary boxes.
- **Panic scope** (#14257, decided). A Fern panic stops the program:
  `__fern_report` exits the process, and off the process entry it is a trap. Fern
  has no unwind, so the scope of a panic is the heap that the panicking code can
  change:
  - A panic in a user process ends only that process. The process has its own
    heap, so the kernel discards that heap and nothing else changes.
  - A panic in kernel code stops the machine, as it does today. The kernel heap is
    the one shared graph. A syscall can store a new value in a kernel structure
    before it panics. Then a release of only what the syscall allocated can free a
    value that the structure still holds.
  - Thus a syscall handler checks each argument at the syscall boundary, and
    returns an error for a bad one. It does not panic. A bad argument then ends
    only the process that made the call, if that process does not handle the
    error. This is the milestone 4 test.
  - eggshell's kernel continues after a panic during a syscall, because Go can
    recover. A per-syscall region with a commit point can give Fern the same
    result later. It is not part of the first kernel.
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
  once volatile and memory layout exist; no new language surface. `std/fdt`
  reads a device tree and `std/pvh` reads the PVH start info (#14260).

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
3. **Done: the interrupt/RC rule** (#14250). "The decision (#14250)" above records
   it: a checked top half, the IRQ region in raw memory, a masked idle wait, and a
   bottom half in the reactor.
4. **Done: capability providers** (#14251). A board module supplies `log` with a
   `@provide("log")` function (section 6).
5. **Done: the no-RC property** (#14253), as `@norc` (section 3). The
   `interrupt` and `naked` attributes wait for a kernel environment.
6. **First slice done: memory layout and the reset entry** (#14254), as
   `arm64-kernel` (section 4).
7. **Started: milestone 1** (#14260). An arm64 image on qemu-virt and an
   x86-64 image on qemu microvm boot, print and stop the VM in CI. `std/fdt`
   reads the device tree, and `std/pvh` reads the PVH start info through
   `__boot_info`. Firecracker remains. **Done: the effect intrinsics** (#14252), with
   `__fence`, `__wfi`, `__hvc`, `__smc`, `__boot_info`, the system register,
   cache, TLB and port ops, `__breakpoint` and the IRQ ops (section 2).
   **Done: `@interrupt`** (#14253) on both kernel targets. `naked` remains.
8. **Done: milestone 2** (#14261). On qemu-virt, the generic timer ticks through
   a GICv3 into an `@interrupt("irq")` top half. On qemu microvm, the local APIC
   timer ticks into an `@interrupt("32")` top half. A `std/async` task takes 20
   wakes from `async.irq(line)`. Between the wakes it allocates and frees for
   longer than a period, so ticks land inside RC operations. A `FERN_LEAKCHECK`
   image prints a balanced census at the end
   (`internal/testing/e2ecompiler/self_host_kernel_irq_test.go`, the
   `kernel-boot` job). The boards are the test's own modules. A `std/` board
   package can follow when a second board needs one. The x86-64 identity map is
   write-back everywhere. A board on real hardware needs the local APIC and its
   MMIO pages mapped uncached, before milestone 3. Then: `naked` (#14253) →
   volatile qualifier (if drivers materialise) → atomics (#14258, if SMP does).
9. **First slice done: milestone 3** (#14262). `std/frames` is a page-frame
   allocator over raw memory. Its header and bitmap live in the first frames
   of its region, and its `@norc` core takes only the base address. The kernel
   image reads its RAM from the device tree or the PVH memory map. It builds
   the frames over the RAM above the cursor of the bootstrap heap. It takes
   every frame and gives every one back. Then it moves the heap onto a run of
   frames with `__heap_adopt`, which the `kernel` profile grants as `arena`.

   The milestone 2 program then runs on the adopted heap, and a
   `FERN_LEAKCHECK` image prints a balanced census
   (`TestSelfHostKernelFramesOnQemu`, `TestSelfHostKernelX86FramesOnQemu`).
   The reset entry still seeds a bootstrap heap from `-ram`. The page tables
   are still the identity map of the reset entry. The second slice builds the
   kernel mapping in Fern, with the MMIO and DMA regions outside ordinary
   memory.
10. Beside those: the DMA region (#14255), the raw-pointer surface (#14256) and a
   TCP/IP stack in Fern (#14259). The panic scope is decided (#14257, see the
   north-star section).

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
