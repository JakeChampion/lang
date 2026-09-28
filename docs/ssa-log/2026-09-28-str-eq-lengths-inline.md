# String equality answers unequal lengths inline

Both native SSA emitters lowered string `==` to a stack-ABI call of
`__fern_str_eq`. The operands were pushed, the helper set up a frame, and
then a length compare almost always settled the answer, because a compiler
mostly compares identifiers and mnemonics of differing length.

`ssa_str_eq` in `asm_ir.fern` (#10626) and `asm_arm64_ir.fern` (#10631) now
compares the two length words inline and answers 0 when they differ. Only
equal lengths call the helper, through its register entry.
`TestSelfHostSSAStrEqComparesLengthsInline` pins the shape on both ISAs and
runs it.

Stage-2 (self-host-built) x86-64 compilers, compiling `checker.fern` to a
binary, under callgrind:

| | Ir |
|---|--:|
| before | 58.72 G |
| after | 58.03 G (−1.2%) |

`__fern_str_eq` itself falls from 5.73 G to 2.01 G. The labels each comparison
site now carries take some of that back in the assembler. arm64 was not
measured: there is no instruction counter under qemu here.

## Tried and dropped: a same-box compare first

A pointer compare ahead of the length compare answers interned literals
without the call. On main 2cd4c9136:

| input | main | with the compare |
|---|--:|--:|
| `checker.fern` | 58.28 G | 56.22 G (−3.5%) |
| `ssa.fern` | 4.166 G | 4.230 G (+1.5%) |

Only 0.41 G of the `checker.fern` saving is in `__fern_str_eq.r`. The rest
moves with the label set the emitted code carries, in the x86 assembler's
hashing (`x86_name_bucket`, `x86_add_label`), so it is incidental. On
`ssa.fern` the extra compare costs more than it saves. Keyword and name
lookups (`util.has_str`, `lexer.is_keyword`) compare a token against literals
it is almost never the same box as.
