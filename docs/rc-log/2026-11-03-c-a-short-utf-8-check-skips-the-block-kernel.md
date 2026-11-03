# 2026-11-03 — a short UTF-8 check skips the block kernel

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

`ls` of a directory of 4000 names retired 14,327,392 instructions on
x86-64, against 11,298,193 for GNU 9.4. `__fern_utf8_valid` took 1.1 M of
them, about 280 instructions for each name of about 12 bytes. The runtime
checks each name that `read_dir` returns.

The function called `__fern_utf8_blocks` at byte 3 of every string. That
kernel sets up seven vector tables, and for fewer than 35 bytes it returns
at once. The 32-byte ASCII skip did not run on a short string, so each byte
went through the full chain of tests.

## The rule

- `__fern_utf8_valid` calls the block kernel only when 32 bytes or more are
  left.
- After the 32-byte ASCII skip, an 8-byte skip takes ASCII words.

| x86-64, `ls` of 4000 names | before | after | GNU 9.4 |
|---|--:|--:|--:|
| `__fern_utf8_valid` | 1,111,698 | 485,465 | |
| whole run | 14,327,392 | 13,613,159 | 11,298,193 |

The change is in `compiler/runtime/common/__fern_utf8_valid.fern`, and
`scripts/gen-runtime-bundle.py` regenerates `compiler/rtbundle.fern`.

## What is still behind in `ls`

The sort's comparator is the next lead. `key_before` has `cmp_key` inlined
into it, so the name-only path pays the whole frame: five saved registers
and 152 bytes of stack for two loads and one `__fern_str_cmp` call.
Shrink-wrapping, or a rule that keeps a large callee out of a cold path,
can remove that cost.

## Tests

`TestSelfHostUtf8ValidityProperty`, `TestRunnerUtf8ValidityProperty`, the
`TestUtf8Validate` set on all four targets, `TestSelfHostReadFileUTF8Blocks`
on both native targets, the read-file validity tests, `TestSelfHostReadDirIno`,
`TestSelfHostReadDirNameLengths` and `TestEilseqIsInvalidUtf8EveryTarget`.
