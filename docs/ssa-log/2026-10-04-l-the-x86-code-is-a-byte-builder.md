# 2026-10-04 — the x86 code is a byte builder

`x86_native`, x86-64. Slice 2 of #11452, refs #8171. Stacked on slice 1.

## The shape

The assembler held .text as an `i32[]` with one element per byte, appended
through a rebuild of the `X86Asm` struct:
`a = X86Asm { ...a, code: a.code.append(b) }`. A run of byte records was
copied into it one `append` at a time, 3.3 MB of code twice, once per
relaxation round.

`X86Asm.code` is now a builder handle. A byte record is one
`buf_push_bytes_range` from the words. An encoder's bytes are one push per
byte, with no struct rebuild. `x86_resolve` takes the bytes out as a `u8[]`,
patches the fixups in place and leaves the result in `X86Asm.text`. The ELF
writer, the replay cache and the relaxation pass read `text`.

Three things go with the old buffer:

- **On-the-spot patching.** The raw encoder API patched a backward branch on
  the spot and queued only forward ones. A builder cannot be written at an
  offset, so every reference now queues, and `x86_resolve` patches all of
  them against the final layout. `x86_fixup_or_patch` becomes
  `x86_queue_rel32`.
- **The in-round memo copy.** Within the first round, a repeated text line
  copied the bytes its first occurrence produced, which reads the code back.
  It ran 6,180 times on `checker.fern`, so a repeat now encodes again, and
  `GasLine.memo` and `GasText.nmemo` are gone. The parse memo stays.
- **`x86_append_bytes`.** `x86_push_arr` does the same job on the handle.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, against slice 1 at its head after the merge of main 411e0f89,
both compilers building the same `checker.fern` to a byte-identical binary:

| | slice 1 | code as bytes |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.608 G | 20.452 G (−0.76%) |
| `x86_gas_assemble_words`, inclusive | 1,482 M | 1,321 M |
| `x86_gas_bytes`, self | 192 M | 0.3 M |
| `x86_gas_emit_op`, inclusive | 226 M | 110 M |

## What is left

The ELF image is still an `i32[]`. `elf_cat_u8` copies the text into it a
byte at a time (55 M) and `util.to_u8` converts the image back (74 M). That
is the next slice. Slices 3 and 4 of #11452, relaxation in place and record
lines out of `GasLine`, are unchanged by this one.
