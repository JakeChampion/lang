# 2026-10-04 — the x86 image is bytes

`elf` and the CLI's x86 path. Follows slice 2 of #11452, refs #8171.

## The shape

After slice 2 the assembled .text was a `u8[]`, but `elf_program_x86` built
the program image as an `i32[]`: it copied the text in a byte at a time
(`elf_cat_u8`), and the CLI then turned the whole image back into bytes with
`util.to_u8` to write it.

`elf_program_x86` now builds the ELF and program headers as before, a few
hundred bytes, and lays the image down in a byte builder. The text goes in
as one range copy. The unwind data, the padding and the data blob go in a
byte at a time, and the result is a `u8[]` the CLI writes as it is. Only
`-g` still widens the image, because `elf_append_symtab` is shared with the
arm64 writers and takes an `i32[]`.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, against slice 2, both compilers building the same
`checker.fern` to a byte-identical binary:

| | slice 2 | image as bytes |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.452 G | 20.319 G (−0.65%) |
| `elf_program_x86`, inclusive | 64.2 M | 5.2 M |
| `util.to_u8` on the image | 74.5 M | gone |

The `-g` image is byte-identical as well.
