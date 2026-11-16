# An inline byte kernel keeps the registers

2026-11-16. x86-64. Follows
`2026-11-15-g-a-box-allocation-and-a-shared-release-keep-the-registers.md`.
Issue #16216.

## The shape

The x86-64 emitter writes the byte kernels inline (`emit_str_op`):
`__ascii_run`, `__memchr`, `__rmemchr`, `__count_byte`, `__count_below_u64`,
`__str_hash`, `__bsd_sum` and `__count_runs`. A kernel calls nothing, and it
changes a small set of registers. `__ascii_run` changes `%rax`, `%rdx`,
`%r8`, `%r9` and `%ymm0`.

The register allocator took each kernel as a call that changes each caller
register. A value that lived across a kernel took a saved register. When
the five saved registers were in use, it took a frame slot, and each pass of
the loop read and wrote the frame.

## The change

A new kind of position, `ssa.COLD_KERNEL`. `ssa_kernel_clobbers` in
`compiler/asm_ir.fern` gives each kernel position a mask of the caller
registers that the kernel changes. The mask goes into the clobber rows of
the fixed table (`ssafixed.with_clobbers`). A value that lives across the
kernel keeps each caller register that is not in the mask. The emitter saves
nothing.

No table holds the masks. The emitter writes the text of the kernel one time
before the allocation, and `ssa_text_clobbers` reads the register names from
that text. Thus a change to a kernel cannot make its mask incorrect. The
position stays a call when:

- the text names a saved register, or a name that the reader does not know
- the text has an instruction with an operand that it does not name: `call`,
  `syscall`, `rep`, `mul`, `div` and the string instructions

A float value still takes a frame slot across a kernel. The kernels use
vector registers, and the emitter saves no float register.

## Measured

x86-64, Cachegrind, against `4e126826`. The program has one loop with six
loop values and a string that live across `__ascii_run`. It makes 800,000
passes of the loop.

| | before | after | change |
| --- | ---: | ---: | ---: |
| Ir | 28,249,468 | 26,647,068 | -5.7% |
| values of the loop in frame slots | 3 | 0 | |

The bench corpus does not move. `string_count_byte` goes from 41,173,389 to
41,173,385 Ir. `utf8_ingest_validated`, `utf8_ingest_unchecked`,
`string_find_byte`, `string_rfind_byte` and `string_scan` build to equal
binaries.

## Traps

- A value that the loop only reads costs no instruction in a frame slot:
  the read folds into the instruction that uses it
  (`SpillCosts.fold_alu_rhs`). Only a value that the loop writes shows in an
  instruction count.
- A cold release in the loop can still send a value to a frame slot
  (`hard_spans`). That rule did not change.

What stays open:

- arm64 keeps the old kind. It has ten saved registers, so its loops reach
  this case less.
