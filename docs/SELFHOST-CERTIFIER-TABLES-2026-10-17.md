# Reusing the certifier's existing tables

Three changes remove repeated work without changing the walk's facts:

- A walk under an assumption already inherits `base.uses`. Do not first compute
  the use counts it immediately discards.
- Token pruning rewrites instructions in place, preserving their result ids and
  positions. Reuse the base definition table when finding the rewritten roots.
- Takes are listed in block order, and `Takes.first` already indexes each block's
  contiguous group. Use it when building the live row instead of scanning all
  takes for every block. Inheriting takes updates their roots, not their order.

## Measurement

The control compiler is stage 2 of main at
`1a91fc49aae61ea1d0091e364ae2fe98ea47bd25`; it also built the candidate.
Both compile frozen sources and stdlib from that revision. The candidate source
is main at `d5b3f2fd0` with these changes. Valgrind 3.24 Callgrind counts guest
instructions in a fresh process with `FERN_RC_CERTIFY_MEMO=0`.

| Compile | Control instructions | Table reuse | Change |
| --- | ---: | ---: | ---: |
| Whole compiler, certification enabled | 127,342,044,044 | 126,615,771,239 | -0.570% |
| Whole compiler, certification disabled | 70,582,813,122 | 70,582,813,046 | approximately zero |
| Checker, certification enabled | 24,650,405,816 | 24,531,015,357 | -0.484% |

The added cost of certification falls by 1.28%. This does not meet #12181's
roughly 15% share target and leaves that issue open. These are instruction
counts, not native wall time.

## Correctness

The complete report over the frozen whole compiler is byte-identical: 7,533
lines, including findings and unproved counts. Its executable is byte-identical
with either compiler and with certification enabled or disabled: SHA-256
`af2931836bc31181dc1683e44d4d08a9d26cf73001addc79d8da949d9420e4d2`.
The frozen checker executable is also unchanged. The certifier cases, memo and
scale tests, unchanged complexity gate and source lint pass.

The candidate reproduces itself through bootstrap stages 2 and 3 at 12,287,536
bytes, SHA-256 `46a3ca1a525489e5a8ae04eea09ef1a0f545c32df6e3910b1963208706f7e673`.
