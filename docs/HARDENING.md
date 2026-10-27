# Binary hardening (`-harden`)

Status: [reference] shipped. `-harden` is opt-in on arm64-linux and
x86-64-linux executables.

`fern -harden -target arm64-linux -o OUT FILE.fern` builds an executable that
turns on three protections:

- Branch Target Identification (BTI).
- Return-address signing with pointer authentication (PAC).
- A stack canary in every framed function.

`fern -harden -target x86-64-linux -o OUT FILE.fern` turns on the stack
canary. The flag is opt-in, and off by default.

## Executable memory mappings

The in-process x86-64 Linux and arm64 Linux linkers separate executable
code from writable data by default, independently of `-harden`. The code
and unwind tables load read-execute; constant boxes, globals and BSS load
read-write. Neither load segment is both writable and executable.
The Linux x86 and arm64 images declare a non-executable stack with `PT_GNU_STACK`.
The arm64 PIE writer declares the same stack permissions.

The x86-64 assembler relaxes branches before the linker chooses the
4 KiB-aligned data address, then resolves both RIP-relative references and
absolute pointers against that address. Debug symbols and variable
locations use the same final addresses. This also lets Valgrind associate
the executable mapping with the binary's function symbols under `-g`.

`TestSelfHostX86WXExecutable` runs relocated data and function pointers,
checks the mappings and symbols, and requires SIGSEGV for a write into
code and for execution from data. The unwind and bootstrap gates cover
the shared writer's layout and the compiler compiling itself.

## What it does

The arm64 backend emits only these indirect branches:

- `blr` for a call through a function value, which covers closures,
  function values and the combinators that call an element function;
- the kernel's entry into a signal handler;
- the FFI shim's `blr x9` (`emit_ccall_shim1_arm64`) to a C function.

Dyn dispatch is a compare chain ending in a direct `bl`, and every tail call
is a direct `b`. Nothing emits `br`.

So `-harden` puts a `bti c` landing pad at:

- each function's `.k` entry, which is the address a function value holds;
- `__fern_sig_catcher`.

The FFI shim's target is C code that `-harden` does not emit. In the
in-process image no C code is linked in, so the target lies outside the pages
the kernel maps as guarded, and BTI does not check the branch. An `-emit asm`
build linked with C objects is protected only if they were built with BTI:
the linker drops the image's BTI property when any input lacks it.

It also marks the image with a `.note.gnu.property` holding
`GNU_PROPERTY_AARCH64_FEATURE_1_AND` with the BTI bit:

- the in-process ELF writer adds a fourth program header, `PT_GNU_PROPERTY`,
  which moves `.text` 56 bytes on (`elf_wx_text_off(true)`);
- `-emit asm` text carries the section, and the linker combines it with its
  other inputs.

On a core with BTI, the kernel then maps the code as guarded pages. An
indirect branch that lands anywhere but a landing pad raises SIGILL, so an
attacker who redirects a function pointer can no longer reach the middle of
a function.

Every function the compiler emits with a frame also signs its return
address:

- `paciasp` signs x30 against the entry sp, before the frame record stores it;
- `autiasp` authenticates it on every exit, once the record is popped, ahead
  of the `ret` or the tail call's `b`;
- each flips the CFI's return-address state (`.cfi_negate_ra_state`, which
  the in-process CFI renderer encodes as DW_CFA_AARCH64_negate_ra_state).

A return address overwritten in the frame then fails authentication and
faults, instead of being followed. Leaf functions without a frame never
store x30, so they need no signature. The backtrace walk strips the
signature (`xpaclri`) before printing a saved return address.

`bti`, `paciasp`, `autiasp` and `xpaclri` are all in the hint space, so each
is a NOP on a core without the feature. The ARMv8.2-A baseline does not
change: a hardened binary runs everywhere an unhardened one does, with the
checks only where the hardware has them.

## Stack canaries

Both native backends store a canary in every function that owns a frame with
spill slots, frame boxes or saved registers. The word sits between the slots
and the saved registers. A write that runs past a frame box therefore reaches
the canary before it reaches a saved register or the return address. Each exit
reloads the word and compares it with the process canary before it restores
the saved registers. A mismatch branches to `__fern_canary_fail`, which
prints `fern: stack canary mismatch` and exits with status 134. The check
uses no allocation and no backtrace.

Startup reads the process canary from `getrandom` after the stack switch. A
short or failed read prints `fern: could not read the stack canary` and exits
with status 134 before `main`. Leaf functions and bare frames hold no slots,
so they carry no canary. The hand-written runtime frames carry none either.

## How it is verified

`TestSelfHostHardenArm64` checks the in-process image and the assembly form linked by gcc.
Both images must contain the BTI property note and produce the expected output.
The test checks PAC and BTI instructions in the in-process image.
A `-backtrace` abort must print plain code addresses without signature bits.

`TestSelfHostStackCanary` builds a function with saved registers and spill
slots under `-harden` on both targets. It checks the prologue store and the
exit check in the emitted text. It runs the in-process image, and it runs a
build whose prologue overwrites the canary slot. The overwritten build must
print the mismatch cause and exit 134. The controls print their sum. A build
without `-harden` must not reference the canary.

The test uses a compiler for the native host architecture.
It runs on the native ARM self-host shards without an x86 compiler.
It also runs with QEMU on the x86 self-host shards.
The log identifies native or QEMU execution before any fault test.
Each native fault test requires its CPU feature flag.
Unsupported features remain unverified.

Each enabled BTI test replaces its BTI instructions with NOPs and requires SIGILL.
The PAC fault test runs independently of BTI support.
It substitutes `bti c` for `paciasp` to preserve valid BTI targets.
It requires SIGSEGV or SIGILL during authentication or the subsequent return.
The same control must run correctly without authentication.
An arbitrary nonzero exit does not satisfy these fault tests.

QEMU Linux user mode emulates PAC and BTI.
Successful QEMU fault tests establish emulated behavior only.
Native fault results apply to the tested process and image variants on that host.
They do not certify handwritten runtime frames or every production host.

Every `tests/stdlib/*_test.fern` program was also built with `-harden` and
run, on x86-64-linux natively and on arm64-linux under QEMU: 532 of 538
runs passed, with no SIGILL and no canary mismatch. The six other runs are
the two runner self-tests that print `not ok` lines by design and the
Darwin-only time zone test, which fail the same way without the flag.

`std/mitigations` reports, for a running process, whether the processor lists
PAC, BTI and MTE (or the CET features on x86-64), whether the kernel enables
them, and whether the process runs with them active. It reads the kernel's
own records and infers nothing from the emitted code
(`docs/security/MITIGATION-REPORT.md`).

## Not covered yet

- **The runtime's hand-written frames** (the `__fern_*` helpers written as
  assembly text in `asm_arm64_ir.fern`) still store an unsigned return
  address. Compiled Fern functions, and the runtime helpers written in Fern,
  are signed.
- **`-shared`**: a shared object's property note is combined with its host's
  by the dynamic loader. The `.so` writer would need the note, and every
  export entry a `bti c`.
- **arm64-android**: the PIE image writer has no fourth program header yet.
- **arm64-darwin**: Apple's arm64e ABI, not BTI, is the mechanism there.
- **x86-64 CET**: the kernel enables user-space IBT for no static binary,
  and the shadow stack is enabled through `arch_prctl` at startup. Both are
  their own design. On x86-64 `-harden` is the canary alone.
- **RELRO**: a static image with no relocations has no GOT for RELRO to
  protect.
- **Stack canaries**: not started. #5549 scopes them to frames with buffers,
  since they cost on every call.
