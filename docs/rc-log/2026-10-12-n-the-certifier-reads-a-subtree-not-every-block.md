# 2026-10-12 — the certifier reads a subtree, not every block

`rccertify.open_region`, `rccertify.mark_taken_loads`, `ssadeps.Dom`. Refs
#8171. No emitted byte changes, no change to any verdict.

## The shape

The emitted-RC certifier runs on every function a compile has not certified
before, so a cold compile pays it in full. On a cold compile of `checker.fern`
it was 11.8% of the instructions. Two of its walks asked a dominator question
of every block where only a few blocks could answer yes:

- `open_region` adds a box proved unique by a test to every block the test's
  arm dominates. It tested every block of the function against the arm, once
  per uniqueness test and again on every token round.
- `mark_taken_loads` looks for the load a null store empties in the blocks
  that dominate the store. It tested every block against the store's.
- `used_after`, which it calls for each matching load, worked out which
  blocks the store reaches again for each load, though the answer depends
  only on the store.

## What changed

- `Dom` keeps `by_clock`, the block each value of the tree walk's clock
  enters. `pre` and `post` share that clock, so the blocks a block dominates
  are exactly those `by_clock` names between its `pre` and its `post`.
  `open_region` walks that range.
- `mark_taken_loads` walks the store's block and then its immediate
  dominators up to the entry. `nearer` picks the same load in any order:
  the later in the store's own block, else the one in the deeper dominator.
- The blocks the store reaches are found once per store and handed to
  `used_after`.

## Measured

One cold compile of `checker.fern` (`FERN_RC_CERTIFY_MEMO=0`) under
callgrind. Both compilers were built from the same tree.

| | Ir | certifier |
|---|--:|--:|
| before | 17.078 G | 2.021 G |
| a subtree, not every block | 16.812 G (−1.56%) | 1.750 G (−13.4%) |

`ssadeps.dom_holds` −188 M, `open_region` −98 M, against `pick_loads` +35 M,
which now does the scan `mark_taken_loads` did inline. A warm compile, where
the memo answers, is unchanged.

`FERN_RC_CERTIFY=report` prints the same findings and the same census, line
for line, under both compilers for `checker.fern` (1,549 lines) and the whole
compiler (6,702 lines). The emitted programs are identical, and the changed
compiler rebuilds itself byte for byte.
