# 2026-10-04 — an index a dominating guard bounds is read unchecked

`ssabounds.proven_indices`, a new pass over the typed semantic graphs, run
by `semsource.complete`. Refs #8171. The emitted code changes, so there is no
emit identity. The compiler this tree builds through the pinned stage0 builds
itself, and that build reproduces itself byte for byte.

## What the profile named

Every `cmp; jae __fern_oob_abort` left in the stage-2 compile of
`checker.fern` came to about 2.8% of its instructions, spread over 4,355
executed sites: the hottest, `checker.Scope.binding_index`, is 4.2 M. The
parser's `elide_len_bounded_body` marks reads only under an ascending
`while (i < xs.len())` loop. It cannot see a guard written as an `if`, a
descending scan, or the length minus a constant, because it matches syntax.

## What changed

- **A block learns the compares on its way in.** A block whose only
  predecessor ends in a two-way `brif` learns that compare, true or false,
  and so does every block it dominates. Only compares of two i32 values
  count. The dominator tree is `ssadeps.dominates`, walked in preorder.
- **An `array_get` or `str_index` the facts bound is marked unchecked**, the
  `imm` 1 the parser's pass already sets and `ssarc` honours on every backend.
  The proof needs the index at least zero and below the length:
  - a fact `x < length(b)`, or `x <= y` / `x < y` with `y` below it;
  - `length(b) - c` for a constant `c >= 1`, and at least zero once a fact
    puts the length at `c` or more;
  - `y - c` with `y` below the length and at least zero where it is
    computed, so it cannot wrap;
  - `y + 1` with `y` at least zero and below some length where it is
    computed, which caps it at the largest i32;
  - a phi whose operands all satisfy the property, the phi assumed to:
    induction over the loop's trips. This is what takes a descending
    `while (i >= 0)` scan and an ascending loop whose guard is an `if`.
- **The same field read twice is one quantity.** A field, tuple element,
  element or length of the same value is the same everywhere, since all of
  them are immutable, so `s.names.len()` in a guard bounds `s.names[i]`, and
  `op.imm` guarded bounds a later `op.imm` read.

## Measured

| | main at 697baa22 | this change |
|---|--:|--:|
| stage 2 emitting the fixed tree's `checker.fern`, total Ir | 17.380 G | 17.364 G (−0.09%) |
| bounds-check sites in that `checker.fern` | 2,006 | 1,798 |
| the pass itself | | 37 M |

The thirty `examples/bench` programs: −0.11% in total, every exit status
unchanged. `tokenize` is −8.45%; the other rows that move are the
seed-dependent `map_string` and `utf8_ingest` ones.

## Witnessed

`TestSelfHostGuardBounds{X86_64,Arm64,Wasm}` run eight guard shapes against
the interpreter on all three backends. On x86-64 four of them emit fewer
`__fern_oob_abort` sites than a twin whose guard proves nothing, which fails
with the pass removed. Eight shapes a guard does not cover (the guard on
another array, no lower bound, `k <= len`, the index moved after the guard,
the last element of an empty array, a descending walk past zero, a step of
two, a read after the loop) each read out of range and still abort with 134.
Stage 2 == stage 3.
