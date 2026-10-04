# 2026-10-04 — the arm64 code and image are bytes

`arm64_native`, `elf` and the CLI's arm64 paths. Slice 5 of #11452, refs
#8171. It does for arm64 what slices 2 and 3 did for x86.

## The shape

The arm64 assembler held .text as an `i32[]`, one element per byte. Each
encoded word from the emitter went in through `arm64_word_bytes`, which made
four `append` calls and then rebuilt the `Arm64Asm`. A branch to a label
already placed was patched there and then, and the literal pool patched each
`ldr Xd, =N` as it was flushed. The ELF writer then copied the text into an
`i32[]` image a byte at a time, and the CLI converted the whole image back
with `util.to_u8`.

- **The code buffer.** `Arm64Asm.code` is now a builder handle. A word record
  is one `buf_push_bytes_range`, and the text-path encoders push through
  `arm64_push_word` and `arm64_push_arr`. `arm64_asm_resolve` takes the bytes
  into `Arm64Asm.text`, a `u8[]`.
- **Patches.** A builder cannot be written at an offset. So a patch whose
  displacement is already known (a backward branch, or a literal-pool load) is
  queued in `patq`, one u64 per patch, and resolve applies it.
  - The range check still runs when the patch is queued, so the
    out-of-range list keeps its order.
  - Each kind's mask and immediate field now live in one place,
    `arm64_branch_keep` and `arm64_branch_field`, shared by the raw `i32[]`
    patchers and the byte patcher.
  - The adrp and imm12 fields likewise live in `arm64_adrp_field` and
    `arm64_imm12_field`.
  - `arm64_patch_b14`, `arm64_patch_ldr_lit`, `arm64_patch_addimm_off`,
    `arm64_lit_le64` and `arm64_word_bytes` are gone.
- **The link.** `arm64_gas_link` patches the page fixups in `text` directly.
- **The ELF image.** `elf_image_two_seg` and the W^X wrappers over it
  (`elf_image_wx`, `elf_image_wx_unwind`, `elf_image_pie`,
  `elf_static_executable_data_wx`, `elf_static_executable_bss_x86_wx_at`) now
  take the text as a `u8[]` and return a `u8[]`. As in `elf_program_x86`, they
  lay the image down in a byte builder, with .text as one range copy. The CLI
  writes the image as it is. Only `-g` widens it, for `elf_append_symtab`.
- **The Mach-O image.** `macho_executable` and `macho_executable_binds` take
  the text as a `u8[]`. They still build an `i32[]` image, because the code
  signature hashes that image, so the text is widened into it inside the
  writer.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under callgrind,
main at ce31f265 against this branch. Both compilers build `checker.fern` to a
byte-identical binary for arm64-linux, arm64-darwin and arm64-android, and
`-g` builds match too:

| | main | code and image as bytes |
|---|--:|--:|
| stage 2, arm64-linux target, total Ir | 22.489 G | 22.271 G (−0.97%) |
| `arm64_elf_binary`, inclusive | 664.1 M | 445.4 M |
| `arm64_gas_program_words`, inclusive | 473.8 M | 400.5 M |
| `arm64_word_bytes` | 77.5 M | gone (a 21.9 M range copy) |
| `elf_image_wx_unwind`, inclusive | 67.1 M | 6.0 M |
| `util.to_u8` on the image | 83.9 M | gone |

The code buffer alone nets almost nothing. Measured before the image moved to
bytes, the 73 M it saved in `arm64_gas_program_words` went into widening the
text back to `i32[]` for the image writers (22.487 G). The saving appears
only once the image takes bytes too, which is why the two land together.

Queuing a known patch costs a little more than patching in place did:
`arm64_asm_fixup_id` rises from 47.9 M to 52.3 M.

## What is left

- **The fixup queue.** The forward-fixup queue is still four parallel arrays
  appended through struct rebuilds (`fix_offs`, `fix_names`, `fix_kinds`,
  `fix_ids`). Slice 1 packed the x86 queue into builders; the arm64 one is
  next.
- **The Mach-O writer.** It still builds an `i32[]` image, and the CLI
  converts it with `util.to_u8`.
- **The symbol table.** `elf_append_symtab` still takes an `i32[]`, so `-g`
  widens the image on both targets.
