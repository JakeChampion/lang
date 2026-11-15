# std/deflate inflate against zlib and libdeflate, 2026-11-15

Status: [record] one measurement on one host. Measure again before you
quote a number.

`scripts/inflate-bench 517825ab4d` made the corpora from the git objects
and the files of commit 517825ab4d. The host was a 4-core x86-64 container
with other work on it. The system zlib was 1.3, and libdeflate was the
shared library of the distribution. "Before" is std/deflate and the compiler
at 517825ab4d. "After" is the inflate rewrite and the bounds prover change
that it needs.

The small corpus is 10000 zlib streams of git objects of 100 B to 4 KiB,
14 MB when decoded. The large corpus is one zlib stream of 65 MB. The
instructions are the count of callgrind for one round, less the count of a
run with no round. The time is the best of seven runs of one round, less
the best of seven runs with no round.

| corpus | decoder | instructions | ms |
| --- | --- | ---: | ---: |
| small | std/deflate before | 955,515,798 | 105 |
| small | std/deflate after | 533,126,047 | 70 to 78 |
| small | zlib, one state for each stream | 552,999,302 | 120 |
| small | zlib, one state with a reset | 547,857,666 | 110 to 113 |
| small | libdeflate | 311,161,618 | 59 |
| large | std/deflate before | 1,658,552,176 | 238 |
| large | std/deflate after | 1,094,770,159 | 174 to 176 |
| large | zlib | 1,171,933,814 | 295 to 297 |
| large | libdeflate | 544,428,689 | 131 |

The git port on branch feat/git-complete-2 ran `log --oneline` on a clone
of this repository, with 17632 commits. It took 2,442,803,095 instructions
before and 1,697,651,368 after. The system git took 1,318,112,462. Inflate
is about 55% of what the port does after the change.

## What separates std/deflate from libdeflate

On the small corpus, about a quarter of the instructions build the decode
tables. The sort of the symbols visits each symbol of the code. Each time a
table doubles, it calls the general concat kernel with its allocation
checks. libdeflate does each of these in a few instructions.

In the fast loop, the register allocator puts the bit buffer, the bit
count and the input position on the stack at the joins. So
each literal and each match also loads and stores them. A mask of the low
`n` bits takes four instructions where `bzhi` takes one. Past its first 32
bytes, the copy of a match keeps its bounds checks. The prover does not
bound a loop counter by the length of the match.

adler32 adds eight bytes for each word in 16-bit lanes, about two
instructions for each byte. libdeflate uses AVX2 for less than half an
instruction for each byte. The lane loads of std/simd take a `u8[]` and not a
`[u8]`, so adler32 cannot use them on its input.
