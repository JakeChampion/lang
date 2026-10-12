# 2026-10-12 — a constant byte offset is a displacement

`ssa.imm_operands`, the x86-64 and arm64 `ssa_raw_inst`, `__fern_str_cmp`.

## What changed

- **A raw byte load or store reads a constant offset in place.** The offset of
  kinds 54 and 55 (`raw_load8`, `raw_store8`) is now an immediate operand:
  `movzbl 3(%r8)` on x86-64 and `ldrb w1, [x2, #3]` on arm64, where both read
  it from a register the constant was first loaded into. `__load_u8(p)` lowers
  to a load at offset 0, so every raw byte read through a pointer paid a
  register and a zeroing instruction. The 8-byte forms (52, 53) scale their
  index, so they keep the register.
- **`__fern_str_cmp` walks two pointers.** Main's eight-byte compare (5343efa5)
  carried both strings, both lengths, the shorter length, both data pointers
  and an index through its loops: more values than the six caller-saved
  registers x86-64 allocates from, so every call saved and restored five
  callee-saved ones. Strings shorter than eight bytes, most sort keys, paid
  that and the word loop's test for nothing; `bench/sort_strings` sorts
  2-to-6-byte names and ran 8.8% more instructions than with the byte loop.
  It now steps a pointer into each string up to `end`, reads the tail through
  `__load_u8` with no bounds check (`end` is the shorter length), and keeps
  the length difference instead of both lengths.

## Measured

x86-64, callgrind, `bench/sort_strings`:

| `__fern_str_cmp` | instructions |
|---|--:|
| byte loop, before 5343efa5 | 119,587,552 |
| eight bytes at a time, indexed (5343efa5) | 130,506,573 |
| pointer walk, offset in a register | 114,303,666 |
| pointer walk, offset a displacement | 107,223,962 |

Two callee-saved registers are still pushed: the word-hit value's live range
runs, in block order, across the byte loop, so linear scan holds its register
there.

On x86-64 every program that links `__fern_str_cmp` emits 41 fewer
instructions, which brings the `.text` rows main's merges had pushed past 1%
back under it.
