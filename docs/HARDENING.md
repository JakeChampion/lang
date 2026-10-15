# Binary hardening (`-harden`)

`fern -harden -target arm64-linux -o OUT FILE.fern` builds an executable that
turns on arm64 Branch Target Identification (BTI). The flag is opt-in, and
off by default.

## What it does

The arm64 backend emits only these indirect branches:

- `blr` for a call through a function value, which covers closures,
  function values and the combinators that call an element function;
- the kernel's entry into a signal handler.

Dyn dispatch is a compare chain ending in a direct `bl`, and every tail call
is a direct `b`. Nothing emits `br`.

So `-harden` puts a `bti c` landing pad at:

- each function's `.k` entry, which is the address a function value holds;
- `__fern_sig_catcher`.

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

`bti` is in the hint space, so on a core without BTI it is a NOP. The ARMv8.2-A
baseline does not change: a hardened binary runs everywhere an unhardened one
does, with the check only where the hardware has it.

## How it is verified

`TestSelfHostHardenArm64` checks the in-process image and the `-emit asm`
form linked by gcc:

- each carries the property note;
- each runs correctly under enforcement;
- each dies with SIGILL once its pads are turned into NOPs.

qemu-aarch64's linux-user mode enforces BTI for an image that asks for it,
which is what makes the last check possible off-hardware.

Every `tests/stdlib/*_test.fern` program was also built with `-harden` and
run under qemu: 247 of 247 passed, with no SIGILL.

## Not covered yet

- **PAC** (`paciasp` / `autiasp`, return-address signing). It needs:
  - `__fern_bt_exit`'s backtrace walk to strip the signature (`xpaci`) before
    it reads a saved return address;
  - the CFI to state `.cfi_negate_ra_state`, which neither `ssa_cfi` nor the
    in-process CFI parser knows.
- **`-shared`**: a shared object's property note is combined with its host's
  by the dynamic loader. The `.so` writer would need the note, and every
  export entry a `bti c`.
- **arm64-android**: the PIE image writer has no fourth program header yet.
- **arm64-darwin**: Apple's arm64e ABI, not BTI, is the mechanism there.
- **x86-64 CET**: the kernel enables user-space IBT for no static binary,
  and the shadow stack is enabled through `arch_prctl` at startup. Both are
  their own design.
- **RELRO**: a static image with no relocations has no GOT for RELRO to
  protect.
- **Stack canaries**: not started. #5549 scopes them to frames with buffers,
  since they cost on every call.
