# ARM64 byte-alignment correction

The primary ARM64 assembler interpreted `.balign 8` as a power-of-two
exponent, inserting padding to 256 bytes. GAS and the bootstrap assembler
interpret `.balign` as a byte count. The primary assembler now makes that
distinction for both initialized data and BSS; `.align` and `.p2align`
retain their ARM64 exponent semantics.

The regression assembles tables of alignment directives and checks data
lengths, zero padding and symbol offsets in both sections. It also checks
already-aligned input. The old assembler fails the corrected fixture with
exit 2; the new one returns 0 when both are compiled by the same parent
compiler. Primary execution passes on Darwin, x86-64 Linux, ARM64 Linux
and WASM. The existing assembler data regression also passes, along with
the full unit suite and `make lint-all`.

At the original alignment checkpoint, the pinned Darwin bootstrap passes
its compiler and `tr` smoke tests. Stages two and three are byte-identical
at 14,344,993 bytes, SHA-256
`8ab5d33388576d24b204cfdf26ad229eec0cf23b80c98e32f6fbc945ef604faf`.
The latest integrated compiler's validation is recorded in
[the producer report](STRING-REGEX-UTF8-2026-10-01.md).

The issue surfaced while measuring the owned-byte RNG API. Its additional
empty-array constant needs 24 bytes in the Clang-assembled object, but the
old primary assembler added 256 bytes to the native data section. Compiling
the same RNG source with the corrected assembler changes native data from
832 to 600 bytes, matching the 24-byte addition over the 576-byte baseline.
Native text and unwind sizes are unchanged by the alignment fix. No size
baseline is raised.
