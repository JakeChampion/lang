# Binary hardening (`-harden`)

`fern -harden -target arm64-linux -o OUT FILE.fern` builds an executable that
turns on two arm64 protections:

- Branch Target Identification (BTI);
- return-address signing with pointer authentication (PAC).

The flag is opt-in, and off by default.

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

## How it is verified

`TestSelfHostHardenArm64` checks the in-process image and the `-emit asm`
form linked by gcc:

- each carries the property note;
- each runs correctly under enforcement;
- each dies with SIGILL once its pads are turned into NOPs;
- each faults once its `paciasp`s are removed, because every `autiasp` then
  authenticates an address that was never signed.

A `-backtrace` abort still prints plain code addresses, and the test would see
a signature's high bits if one leaked through.

qemu-aarch64's linux-user mode emulates both features, and enforces BTI for
an image that asks for it, which is what makes these checks possible
off-hardware.

Every `tests/stdlib/*_test.fern` program was also built with `-harden` and
run under qemu: 247 of 247 passed, with no SIGILL.

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
  their own design.
- **RELRO**: a static image with no relocations has no GOT for RELRO to
  protect.
- **Stack canaries**: not started. #5549 scopes them to frames with buffers,
  since they cost on every call.
