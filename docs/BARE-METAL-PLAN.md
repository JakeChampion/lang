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
- `__cache_sync_code(address)` makes instruction fetch see the code that the
  kernel wrote to the line of the address. A kernel calls it for each line of
  the code that it loads for a process.
  - On arm64, it cleans the data line with `dc cvau` and then invalidates the
    instruction line with `ic ivau`, with `dsb ish` after each and an `isb`.
  - On x86-64, it emits no instruction, because instruction fetch sees each
    store.
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

**The decision on `naked` (#14253): an early-setup hook instead.** Fern has no
asm block. So a `naked` Fern function cannot control its registers, and the
register allocator can spill to a stack that does not exist yet. The reset
entry already sets the stack, clears `.bss` and builds the translation. So the
early setup is a hook that this code calls, and `naked` is not part of the
language:

- `@early` marks the hook. It implies `@norc`, so E102 applies to its body.
- E104 refuses a hook with a parameter or a result, a second hook, and a hook
  on a target that is not a kernel.
- The reset entry calls the hook after the stack, `.bss`, the translation and
  the vector table exist, and before the heap starts. On x86-64 the IDT now
  loads before the hook, as `VBAR_EL1` does on arm64. Thus a fault in the hook
  reaches a handler, and is not a triple fault.
- The hook is a root of the tree shake and of the lowering, as a handler is.

`internal/testing/e2ecompiler/self_host_early_test.go` checks the rules on both
checkers and the order of the reset code on both ISAs.
`TestSelfHostKernelEarlyHookOnQemu` and `TestSelfHostKernelX86EarlyHookOnQemu`
boot a hook that writes a system register and takes a breakpoint, and `main`
prints both results.

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

**`-emit image` writes an arm64 Linux `Image` (#14767).** Firecracker on arm64
loads only this form, and qemu-virt loads an initrd only for it. The `Image`
holds the same memory as the ELF form: each load segment is at its address
minus BASE. The 64-byte header takes the place of the ELF header:

- `code0` is a branch to `_start`, and the magic `ARM\x64` is at byte 56.
- The text offset is 2 MiB. A loader puts the `Image` 2 MiB above the start of
  its RAM, so BASE must be that start plus 2 MiB. qemu-virt and Firecracker
  both do this. qemu also moves an `Image` with a text offset of 0 up by
  2 MiB, because it keeps the bottom of the RAM for its own boot code.
- The image size is SIZE of `-ram`, so a loader that reads it keeps the device
  tree and the initrd out of the RAM of the image.
- The loader puts the device tree address in x0, so `__boot_info()` gives it,
  and `/chosen` names the initrd. `std/fdt` reads the range with
  `property_cells`.

`internal/testing/e2ecompiler/self_host_kernel_image_test.go` compares the
`Image` with the ELF form of the same program and checks the header.
`TestSelfHostKernelLinuxImageOnQemu` boots the `Image` on qemu-virt with an
initrd, and reads a file from it with `std/cpio`.

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

SMP kernels and lock-free structures need atomics (#14258). Under the
handler-owns-nothing rule, single-core drivers and kernels do not. The language
has four, on the 64-bit word at a raw address. They need `unsafe` and the
`privileged` capability, and a `@norc` function can call them:

| Intrinsic | Order | arm64 | x86-64 |
|---|---|---|---|
| `__atomic_load_acquire(addr)` | acquire | `ldar` | `mov` |
| `__atomic_store_release(addr, v)` | release | `stlr` | `mov` |
| `__atomic_fetch_add(addr, v)` | sequentially consistent | `ldaddal` | `lock xadd` |
| `__atomic_cas(addr, old, new)` | sequentially consistent | `casal` | `lock cmpxchg` |

`__atomic_fetch_add` and `__atomic_cas` give the old word. x86-64 orders each
load as acquire and each store as release, so both are plain moves there. The
ops lift as device ops, so no pass removes, merges or moves them, or moves a
plain access across them. Refcounts stay non-atomic (`docs/MULTICORE-RESEARCH.md`
C1).

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
`interrupt` attribute work plus a stable ABI decision, and it is the
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
  allocator, `std/pagetable` builds the kernel mapping in its frames, and
  `__heap_adopt` moves the heap onto them (sequencing, step 9).
- **DMA** (#14255). Volatile is not sufficient. A driver needs physically contiguous,
  uncached (or explicitly cache-maintained) allocations, and physical addresses
  distinct from virtual ones. This is an allocator feature, and it is the one
  driver requirement the list above genuinely misses. TamaGo keeps DMA regions
  outside the Go runtime, so that the GC never touches them. For Fern, DMA memory
  is a second region that the RC machinery never sees, used through the
  `__raw_*` surface. It is not a flag on ordinary boxes. `std/dma` is that
  region: a buffer is a run of frames from `std/frames`, which `std/pagetable`
  remaps as device memory, outside the run that holds the heap. The buffer
  carries its virtual and its physical address, equal under the identity
  mapping. A virtio driver that uses it is part of the network stack (#14259).
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
  a virtio-net driver. **The driver exists:** `std/virtio` is the virtio-mmio
  version 2 transport with split virtqueues in `std/dma` buffers.
  `std/virtio_net` sends and receives Ethernet frames over it.
  `std/ip` is Ethernet, ARP, IPv4, UDP and the TCP segments over the driver.
  `std/tcp/conn` is the TCP connection, `std/tcp` binds connections to an
  interface, and `std/tcp/serve` serves HTTP over it with the shape of
  `std/serve`. `std/serve` itself over `std/tcp` waits for milestone 4.
- **A sanctioned raw-pointer surface** (#14256) — **done.** `__raw_*` stays a
  self-host-compiler internal, the floor of the runtime helpers. The public
  surface is `std/raw`: an address is a `usize`, and `load_u8` to `load_u64`
  and `store_u8` to `store_u64` are `@norc` functions over the `__mem_*`
  intrinsics. The `privileged` capability gates the intrinsics, so a hosted
  program fails with E066, and a dependency needs the `privileged` package
  capability (`docs/PACKAGE-CAPABILITIES-BRIEF.md`).
- **A filesystem** (#14264). The first one is a read-only cpio archive, the
  format of a Linux initramfs: `std/cpio` reads it. A VMM loads it as the
  initrd, or a virtio-blk disk holds it, and `std/virtio_blk` reads the disk.
  A writable filesystem comes later, on the same driver.
- **ELF loading**, to run userspace binaries — adjacent to the in-process linkers
  Fern already ships, which emit ELF rather than shelling out to one. `std/elf`
  reads the header and the program headers of an ELF64 image, and
  `TestSelfHostElfReaderMatchesDebugElf` holds it to Go's `debug/elf` on the
  images of every ELF target. `std/user_process` maps the segments of a static
  executable into the tables of a process and runs it (sequencing, step 19).
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
virtio-mmio, with no PCI. Firecracker loads the kernel directly, with no
bootloader: an ELF image through its PVH note on x86-64, and an arm64 Linux
`Image` on arm64. That makes milestone 1 shorter.

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
`docs/FERNOS-LINUX-ABI.md` records the measured set: 18 calls, one thread,
and three `mmap` reservations at start.

## Sequencing

1. **Done: the freestanding codegen is in the compiler** (#14245). It reads the
   entry shape from the descriptor, so `EntryReset` can be a second value.
2. **Done: MMIO loads and stores that no pass removes** (#14249). The compiler
   still removes an unused plain raw load (see "Volatile"). The `mmio`
   capability gates the intrinsics: the `kernel` and the freestanding profiles
   grant it, a hosted target refuses them with E066, and a dependency needs
   the `mmio` package capability.
3. **Done: the interrupt/RC rule** (#14250). "The decision (#14250)" above records
   it: a checked top half, the IRQ region in raw memory, a masked idle wait, and a
   bottom half in the reactor.
4. **Done: capability providers** (#14251). A board module supplies `log` with a
   `@provide("log")` function (section 6).
5. **Done: the no-RC property** (#14253), as `@norc` (section 3).
6. **Done: memory layout and the reset entry** (#14254), as `arm64-kernel`
   and `x86-64-kernel` (section 4). The image layout tests read the program
   headers and the entry point of both.
7. **Done: milestone 1** (#14260). An arm64 image on qemu-virt and an
   x86-64 image on qemu microvm boot, print and stop the VM in CI. `std/fdt`
   reads the device tree, and `std/pvh` reads the PVH start info through
   `__boot_info`. Firecracker loads the same x86-64 ELF, and the `-emit
   image` form of the arm64 image (#14767). It stays the production target. **Done: the effect intrinsics** (#14252), with
   `__fence`, `__wfi`, `__hvc`, `__smc`, `__boot_info`, the system register,
   cache, TLB and port ops, `__breakpoint` and the IRQ ops (section 2).
   **Done: `@interrupt`** (#14253) on both kernel targets, and the `@early`
   hook in place of `naked` (section 3).
8. **Done: milestone 2** (#14261). On qemu-virt, the generic timer ticks through
   a GICv3 into an `@interrupt("irq")` top half. On qemu microvm, the local APIC
   timer ticks into an `@interrupt("32")` top half. A `std/async` task takes 20
   wakes from `async.irq(line)`. Between the wakes it allocates and frees for
   longer than a period, so ticks land inside RC operations. A `FERN_LEAKCHECK`
   image prints a balanced census at the end
   (`internal/testing/e2ecompiler/self_host_kernel_irq_test.go`, the
   `kernel-boot` job). The boards are the test's own modules. A `std/` board
   package can follow when a second board needs one. The x86-64 identity map is
   write-back everywhere, so the local APIC is cached until `main` activates
   the kernel mapping of step 9, which maps the device window uncached. Then:
   volatile qualifier (if drivers materialise) → atomics (#14258, if SMP does).
9. **Done: milestone 3** (#14262). `std/frames` is a page-frame allocator
   over raw memory. Its header and bitmap live in the first frames of its
   region, and its `@norc` core takes only the base address. The kernel image
   reads its RAM from the device tree or the PVH memory map. It builds the
   frames over the RAM above the cursor of the bootstrap heap. It takes every
   frame and gives every one back. Then it moves the heap onto a run of frames
   with `__heap_adopt`, which the `kernel` profile grants as `arena`
   (`TestSelfHostKernelFramesOnQemu`, `TestSelfHostKernelX86FramesOnQemu`).

   `std/pagetable` builds the kernel mapping in frames, with one descriptor
   format for each ISA behind a `target_arch()` fold. The RAM of the boot
   protocol is ordinary memory, the device window of the board is device
   memory, and every other address is unmapped. `activate` writes `TTBR0_EL1`
   or `CR3`, so the identity map of the reset entry serves only until `main`
   builds its own. A load from an unmapped address then faults: on arm64 the
   sync handler records the data abort and steps over it, and on x86-64 the
   page-fault handler ends the run (`TestSelfHostKernelMapsOnQemu`,
   `TestSelfHostKernelX86MapsOnQemu`). The tick program runs on the new
   mapping, and a `FERN_LEAKCHECK` image prints a balanced census. The reset
   entry still seeds a bootstrap heap from `-ram`, and the frames start above
   its cursor.
10. **The DMA region exists** (#14255), as `std/dma` (the DMA item of the
   north-star section). `std/pagetable` splits a 2 MiB block into pages when
   one page inside it changes. Thus a buffer in the middle of the RAM becomes
   device memory on its own. `TestSelfHostKernelDMAOnQemu` and
   `TestSelfHostKernelX86DMAOnQemu` write a pattern into a buffer, churn the
   heap through the tick program, and find the pattern intact and the frames
   returned. The issue stays open until a virtio driver uses the region.
11. **The raw-pointer surface exists** (#14256), as `std/raw` (the raw-pointer
   item of the north-star section). `TestSelfHostKernelRawOnQemu` and
   `TestSelfHostKernelX86RawOnQemu` store and load a frame at every width and
   see the bytes of each value where the little-endian layout puts them. The
   same program fails with E066 on a hosted target, and a dependency without
   the `privileged` grant fails at the package gate.
12. **The virtio-net driver exists** (#14259, first slice; it closes #14255).
   `std/virtio` probes a virtio-mmio version 2 transport, negotiates features
   and builds split virtqueues in `std/dma` buffers. `std/virtio_net` opens
   the network device with the MAC and virtio 1.0 features, keeps eight
   receive buffers posted and sends from eight transmit buffers.
   `TestSelfHostKernelVirtioNetOnQemu` and
   `TestSelfHostKernelX86VirtioNetOnQemu` send an ARP request and an ICMP
   echo to the gateway of qemu's user network and receive both replies. qemu
   needs `-global virtio-mmio.force-legacy=false` for version 2, and the
   microvm runs without ACPI so that its eight transports sit at
   `0xfeb00000`. The driver polls, and step 15 adds its IRQ line.
13. **The first layers of the stack exist** (#14259, second slice), as
   `std/ip`: Ethernet framing, an ARP cache in a page of raw memory with
   requests and replies, IPv4 and UDP, by polling the device.
   `TestSelfHostKernelUDPEchoOnQemu` and `TestSelfHostKernelX86UDPEchoOnQemu`
   run a UDP echo server on port 7 behind a forwarded host port. The host
   sends three datagrams and reads each one back. qemu's user network asks
   for the guest's MAC first, so the ARP reply is on the path.
14. **TCP exists** (#14259, third slice). `std/tcp/conn` is the state machine
   of one connection over `Segment` values and a clock the caller supplies,
   in the core partition, so `tests/stdlib/tcp_conn_test.fern` drives it
   with synthetic segments: the handshake, data in and out of order, the
   window, retransmission with the doubled timeout, both closes and the
   reset. `std/tcp` binds up to eight connections to a `std/ip` interface,
   and `std/ip` parses and builds the segments with their checksums.
   `TestSelfHostKernelHTTPOnQemu` and `TestSelfHostKernelX86HTTPOnQemu`
   answer three requests from Go's HTTP client with `std/http` over it.
15. **The device's IRQ line drives the stack** (#14259, fourth slice).
   `TestSelfHostKernelHTTPByInterruptOnQemu` and
   `TestSelfHostKernelX86HTTPByInterruptOnQemu` answer the same requests
   from wakes in `async.race` on the device's line and the timer's line. The
   device tree gives the SPI on arm64, and the `virtio_mmio.device=...:GSI`
   entry of the command line gives the IOAPIC input on x86-64. Both lines
   are edge-triggered. The bottom half acknowledges the device with
   `interrupt` of `std/virtio_net` and then drains the listener with `poll`
   of `std/tcp`. A buffer the device returns after the acknowledgement
   raises the line again, so nothing is lost.
16. **A handler serves from the kernel** (#14259, fifth slice).
   `std/tcp/serve` has the shape of `std/serve` over `std/tcp`: framing with
   `std/http` under its limits, keep-alive, pipelining, `100 Continue`, and
   the refusal of a request that does not frame. A program moves to a
   kernel by one import and the handler type, which has no `platform.Host`.
   `TestSelfHostKernelServeMatchesHostedOnQemu` and
   `TestSelfHostKernelX86ServeMatchesHostedOnQemu` run one handler under
   the hosted `std/serve` and under `std/tcp/serve` on the kernel, and the
   answers match but for the date header. `std/serve` itself over `std/tcp`
   waits for the Linux ABI of milestone 4: then `std/serve` is a user
   program and the kernel implements its `tcp_*` syscalls over `std/tcp`.
17. **A read-only filesystem exists** (#14264, the filesystem half of
   milestone 5). `std/cpio` reads a cpio archive in the `newc` format, the
   format of a Linux initramfs. `std/virtio_blk` reads and writes the
   sectors of a virtio-blk disk over `std/virtio`, and it sends a flush.
   qemu-virt loads no initrd for an arm64 ELF image. The `-emit image` form
   gets one, which `/chosen` names (section 4). On x86-64, qemu and
   Firecracker load the initrd as module 0 of the PVH start info, and
   `std/pvh` reads the module list. `TestSelfHostKernelVirtioBlkOnQemu` and
   `TestSelfHostKernelX86VirtioBlkOnQemu` read an archive from the disk,
   find a file by its path and sum a file of 100000 bytes, which takes two
   requests. The x86-64 image also reads the same archive as its initrd.
   Then both images write the last sector, and the Go test reads it in the
   disk file. The ELF loader is the other half of the milestone.
18. **User mode runs on arm64** (#14263, the first slice of milestone 4).
   `__user_run(frame)` enters EL0 with the state of a trap frame, much as a
   VMM runs a guest. Each exception from EL0 goes through a lower-EL slot of
   the vector table. The slot saves the user state back into the frame, and
   `__user_run` returns with the kind of the trap. Thus the syscall
   dispatch, demand paging and preemption are ordinary Fern code in a loop
   around `__user_run`, and a timer IRQ during EL0 returns to that loop. The
   kernel heap is never preempted:
   - The frame holds x0 to x30, `SP_EL0`, the PC, PSTATE, `TPIDR_EL0`, FPSR,
     FPCR and q0 to q31, 816 bytes in all.
   - `__user_run` keeps the callee-saved registers and the DAIF of the kernel.
     It also keeps the `TPIDR_EL0`, FPCR and FPSR of the kernel, because the
     frame gives the process its own values of them.
     It masks the exceptions before it writes `ELR_EL1` and `SPSR_EL1`, so
     that no nested exception overwrites them.
   - `std/pagetable` maps user pages with `map_user`. Each virtual page goes
     to a physical page, at AP 01 or AP 11 and PXN. The kernel mapping stays
     kernel-only, so a load of a kernel address from EL0 faults.

   `TestSelfHostKernelUserModeOnQemu` runs EL0 code that makes two `svc`
   calls. The kernel answers them through the frame. Then the code loads a
   kernel address and takes a data abort. x86-64 refuses `__user_run` until
   its slice, which uses `syscall` and `iretq`.
19. **Linux programs run as user processes on arm64** (#14263, the second
   slice of milestone 4). `std/user_process` loads a static `arm64-linux`
   executable into a fresh `std/pagetable` mapping, which holds the kernel
   mapping, and runs it with `__user_run`:
   - The pages that hold bytes of the file are copied at load, and
     `__cache_sync_code` makes the code reach instruction fetch. The rest of
     each segment, the start stack and each `mmap` reservation get a zeroed
     frame when the program first touches them. Thus the 16 GiB heap and the
     1 GiB stack of the Fern runtime cost only the pages that it uses.
   - The kernel answers the start calls of the runtime (`mmap`, `mprotect`,
     `sigaltstack`, `rt_sigaction`) and `ioctl`. `write`, `writev` and
     `exit_group` return to the caller as events. It reads each pointer
     argument through the tables of the process, so a bad pointer gives
     `EFAULT`.
   - A fault outside the regions of the process ends only that process, and
     `release` gives back each frame that it used.
   - `std/pagetable` gains `leaf`, `protect_user` and `free_user` for this.

   `TestSelfHostKernelProcessOnQemu` runs three programs from the initrd: a
   Fern program that prints its argument, uses its heap and its stack, and
   exits with 3, a hand-made ELF that gets `EFAULT`, and a Fern program that
   is killed for a load of a kernel address.
20. **Processes are preempted on arm64** (#14263, the third slice of
   milestone 4). Each process has its own tables and its own Fern heap. A
   timer tick during EL0 returns to the kernel loop as `Interrupted`. The
   top half then runs at EL1 and acknowledges the line, and the loop runs
   the next process. The top half touches no RC value, so the kernel heap is
   never preempted, and RC stays non-atomic.

   `TestSelfHostKernelSchedulerOnQemu` checks the three "done when" items of
   #14263 on arm64:
   - Two Fern workers allocate and free strings under a timer of 1 ms. Both
     are preempted, and both get the correct total.
   - A process with a bad syscall argument ends, and the workers continue.
   - A process that loads from the code address of a worker takes a fault,
     because that address is not in its tables, and the kernel ends it.
21. **A process reads the initrd through the Linux calls** (#14265, the
   first slice of milestone 6). `with_root` gives a process a `std/cpio`
   archive as a read-only root. The kernel answers `openat`, `close`,
   `read`, `lseek`, `getdents64`, `fstat`, `newfstatat` and `getcwd` from
   the archive. A path resolves `.`, `..` and symbolic links. A call that
   can change the filesystem gives `EROFS`. `std/cpio` gains
   `children`, the entries directly in a directory.

   `TestSelfHostKernelProcessOnQemu` runs a Fern program that reads files by
   an absolute path, a relative path and a symbolic link with the hosted
   `read_file`, and lists `/etc` with `read_dir`. A missing file and a
   `write_file` both fail.

   The kernel also answers `statx`, `readlinkat` and `clock_gettime`.
   `with_exe` makes `/proc/self/exe` a link to the program, so the compiler
   can read its own image for the key of its cache. The monotonic clocks
   read the generic timer, and `with_clock` sets the wall clock, which the
   test reads from the PL031 real-time clock of qemu-virt. The arm64
   baseline has no `RNDR`, so `std/virtio_rng` reads random bytes from the
   host, and `with_random` makes it the source of `getrandom`. A process
   with no source gets `ENOSYS`, not bytes that are not random.
22. **A process writes files in memory** (#14265, the next slice of
   milestone 6). The root of a process has a layer in memory above the
   archive. The kernel answers `mkdirat`, `openat` with `O_CREAT`, `write`,
   `renameat`, `unlinkat`, `fchmod` and `fsync` in that layer, and the
   archive stays read-only. `written` gives the kernel a file of the layer,
   such as the output of a compile. The compiler writes its cache of
   certified functions in `$HOME/.cache/fern` and its output at `-o`, so it
   needs only this layer.

   `TestSelfHostKernelProcessOnQemu` runs a Fern program that makes a
   directory tree, writes, renames, reads back, sets a mode, lists and
   removes files there. A change of the archive fails, and the kernel reads
   the output that the program leaves.
23. **The compiler compiles a program on the kernel** (#14265, the first
   "done when" item of milestone 6). The initrd holds the `arm64-linux`
   compiler, unmodified, with its stdlib embedded, and a source file. The
   compiler extracts its stdlib into the layer in memory, compiles the file,
   and writes the output there. The kernel then runs the output as a second
   process.
   - The compile needs more RAM than the other boot tests. qemu-virt loads
     the initrd above the RAM that the header of the Image claims, so the VM
     has RAM above the `-ram` range for it. Another loader can put the
     initrd in the RAM of the kernel. Thus `std/frames` gains `reserve`, and
     the kernel reserves the frames of the initrd before it allocates.
   - `TestSelfHostKernelCompilesOnQemu` compares the size and the FNV-1a
     hash of the output with the output of the same compiler on Linux,
     under qemu-user.
24. **The compiler compiles itself on the kernel** (#14265, the second
   "done when" item of milestone 6). The initrd holds the compiler, its
   sources and its `-embed` stdlib. In a VM of 6 GiB the compiler compiles
   itself in about 23 minutes. The result has the size and the FNV-1a hash
   of the result of the same compiler on Linux.
   - The run found a fault of the compiler: a cache directory that ends in
     `/`, such as the `HOME=/` of the kernel, made the cycle inside the
     stdlib an error (#14955). `cache_base` now gives a clean path.
   - It also found that a node of the layer in memory could take the inode
     of another node, and that each page-in flushed the whole TLB.
   - `TestSelfHostKernelSelfCompilesOnQemu` runs in `fernos-nightly.yml`,
     because it takes about 30 minutes.
25. Beside those: the panic scope is decided (#14257, see the north-star
   section).

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
