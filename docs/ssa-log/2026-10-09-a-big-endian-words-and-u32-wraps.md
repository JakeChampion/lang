# 2026-10-09 — big-endian words are one load, and u32 wraps go

`ssabounds.word_loads`, `ssa.drop_low_wraps`, the `bswap` op (411). Refs
#10615.

## What changed

- **Big-endian words.** `word_loads` (#12012) read a little-endian word
  assembled from bytes as one load. sha1, sha256, sha512 and sm3 assemble
  theirs big-endian (`b[o] as u32 << 24 | … | b[o + 3] as u32`), so they still
  made four or eight byte reads, shifts and ors per word. The pass now takes
  a word whose bytes all sit at their big-endian places too, and reads it as
  the little-endian load followed by a byte swap: `bswap` at 32 or 64 bits,
  `rev` on arm64, `bswap` on x86-64, and `$__fern_bswap32` /
  `$__fern_bswap64` on wasm. It lifts as a unary, as `clz` does, so it stays
  in a register.
- **u32 wraps.** `drop_low_wraps` dropped a signed 32-bit wrap that nothing
  read the high half of, but kept every u32 one, so each add in a u32 sum
  was followed by a zero extension (`mov w0, w0` / `movl %eax, %eax`). u32
  wraps now go by the same rule, and a 32-bit rotate or byte swap reads
  only its operand's low half.
- **arm64 rotates** copied their operand and rotated it in place; `ror`
  now reads its source register.

## Measured

`cksum -a ALG` over 64 MiB of random input, arm64-darwin (Apple M-series,
native), instructions retired from `/usr/bin/time -l`, cycles best of five.
Both compilers are built from source by the same compiler and compile the
same std/crypto (main's, so sha256 runs its software rounds). Baseline: main
at ede00ed4. Every digest matches main's output.

| digest | main | this change | change |
|---|--:|--:|--:|
| md5 | 1,072,157,482 | 786,515,433 | −26.6% |
| sha1 | 2,376,524,393 | 1,628,694,757 | −31.5% |
| sha224 | 4,576,874,060 | 3,488,646,174 | −23.8% |
| sha256 | 4,579,886,941 | 3,488,810,778 | −23.8% |
| sha384 | 2,689,410,815 | 2,187,603,013 | −18.7% |
| sha512 | 2,689,193,293 | 2,184,777,924 | −18.8% |
| sm3 | 4,501,110,613 | 3,431,040,467 | −23.8% |
| blake2b | 1,258,737,754 | 1,208,070,249 | −4.0% |

Cycles moved with them: sha256 956 M → 623 M, sm3 885 M → 622 M, md5
788 M → 517 M.

Compiling `compiler/fern.fern` for x86-64: 83.64 G instructions on main and
83.63–83.71 G with the change, the same within run-to-run noise. The
compiler built with it rebuilds itself byte for byte.

## Traps

- A fused rotate leaves its two shifts behind for `prune_dead`, and the
  right shift reads its operand's high half. Run `drop_low_wraps` before
  pruning and every rotated value keeps its wrap, which is most of the u32
  wraps in a digest round. `register_form` now prunes first.
- The first cut of this slice combined the bytes one layer down, on the
  register path's SSA (`combine_loads`, an `ld_word` instruction). It
  measured slightly better on md5 (the checked reads `word_loads` keeps as
  the run's bounds check are dead loads it does not remove), but it was a
  second pass for the rewrite #12012 had just landed, and it missed owned
  arrays and wasm. It went in favour of the big-endian case here.
- A checked read that `word_loads` keeps as the bounds check still loads its
  byte, which nothing reads. Removing that load needs the check split from
  the read.
