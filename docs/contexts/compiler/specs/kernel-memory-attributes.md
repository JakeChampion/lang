# Kernel memory attributes

**Status:** In progress
**Context(s):** Compiler, runtime
**Date:** 2026-11-07

## Problem

The reset entry calls main with the MMU off. Arm64 then treats data accesses
as Device memory. Ordinary string copies can fault on an unaligned load.

## Solution

Before main, enable an identity map. Map the configured RAM as Normal memory.
Keep addresses outside that RAM as Device memory for board MMIO.

## User Stories

1. A kernel author can use ordinary strings and arrays after reset.
2. A board author can retain Device attributes for MMIO.
3. A loader can place the image below or above 4 GiB within the supported RAM range.

## Implementation Decisions

- Extend the existing EL1 reset contract. The loader enters with the MMU and
  data cache off, initialized RAM, and no stack requirement.
- Use 4 KiB translation tables across the existing 40-bit address space.
  Use blocks for uniform ranges and pages at RAM boundaries.
- Round the RAM end to its containing page for translation attributes.
  Keep the heap and stack inside the exact configured byte range.
- Use Normal non-cacheable RAM and Device-nGnRnE MMIO. Cache activation is a
  separate contract. Mark Device mappings execute-never.
- Keep the table region outside the heap. Retain the boot core, FP/SIMD,
  BSS clearing, heap initialization and board capability contracts.
- Extend the assembler with EL1 translation registers, TLBI and data addends.
- Reject numeric overflow before the configured RAM range reaches the emitter.

## Testing Strategy

- Verify assembler encodings against an independent assembler.
- Walk emitted table descriptors across RAM boundaries, high addresses,
  large blocks, the final physical page and the MMIO range.
- Boot the original QEMU board/FDT program below and above 4 GiB.
- Retain the original RAM, image, privileged-op, FDT and source gates.

## Out of Scope

EL2 transitions, virtual relocation, cache activation, SMP startup and user isolation.

## Open Questions

None. The user selected Normal RAM and Device MMIO on 2026-11-07.
