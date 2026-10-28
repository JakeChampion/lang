# AArch64 memory tagging

Status: design for [SEC-034](https://github.com/JakeChampion/lang/issues/13536).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The allocator is described in [ALLOCATOR-AUDIT.md](ALLOCATOR-AUDIT.md) and the process report in [MITIGATION-REPORT.md](MITIGATION-REPORT.md).

## What the hardware offers

Memory tagging gives every 16-byte granule of a tagged mapping a 4-bit tag and every pointer a 4-bit tag in its top byte.
A load or store through a pointer whose tag differs from the granule's tag faults, synchronously or asynchronously as the process chooses.
Linux enables it per process through `prctl(PR_SET_TAGGED_ADDR_CTRL)` and per mapping through `PROT_MTE`.
QEMU 8.2.2 emulates it, and `std/mitigations` reports `mte` as present in the emulated processor and absent from the process.

## The design

1. Startup under `-harden` on arm64-linux asks for tagged addresses and synchronous faults with `prctl`. It continues where the kernel refuses, as the x86-64 shadow stack request does.
2. The arena is mapped with `PROT_MTE`.
3. A box allocation rounds its size to a 16-byte granule, which changes the size classes from multiples of 8 to multiples of 16. It tags the granules with a fresh random tag (`irg`, `st2g`), and the box pointer carries the tag.
4. A release retags the block with a different tag before it enters a free list, so a stale pointer faults on its next access. That is the use-after-free detector the sanitizer provides today without hardware.
5. The raw `__free` bounds check and every arena comparison strip the top byte before they compare.
6. The fault handler reads `si_code` and reports `fern: memory tag mismatch` with a backtrace, instead of the plain fault.

## Costs and open questions

- A 24-byte box takes 32 bytes. The size classes and the free lists change, and the allocation count baselines move.
- A reference-count header lives in the same granule as the first payload bytes, so a tag covers both. That is correct for a box, and wrong for a view into the middle of another box, which must carry the owner's tag.
- A string or array view that points inside a box keeps the owner's tag, which the view construction already does for the address.
- `__load_*` and `__store_*` through a raw pointer carry whatever tag the raw address holds. A raw pointer formed by arithmetic from a tagged one keeps the tag, and one formed from an integer has none and faults.
- The Go oracle and the interpreter need no change, and wasm has no tags.

## Verification plan

- A QEMU fault test for a use after free, a partial-granule read past a box, a retag after free, and a tag collision. Each case has a control.
- `std/mitigations` must report `mte` as present in the process.
- Physical verification needs a core with MTE, which no CI runner has today. The record stays "emulated" until one exists.

## Remaining limits

The design is not implemented.
It is the next hardware slice after the shadow stack, and it is gated on the size-class change, which moves every allocation baseline.
