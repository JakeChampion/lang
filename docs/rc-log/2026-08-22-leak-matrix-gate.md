# The leak sweep, made a standing gate

Not a reclaim change — a gate. Every RECLAIM gap so far was found by accident:
#7364 while building #7360's controls, #7281/#7282 alongside a tuple fix, and
the #6127 seven-leak sweep was a hand-run session recorded in an issue and
never re-runnable. `TestSelfHostLeakMatrixX86_64` generates one program per
cell of **value kind × scope × consumption** (9 kinds × 5 scopes × 2
consumptions = 90 cells), compiles each under BOTH compilers with
`FERN_LEAKCHECK=1`, classifies each side clean-vs-leak by live bytes, and pins
the verdict pair per cell in
`internal/e2eselfhost/testdata/selfhost-leak-matrix.txt`.

The file is the live gap list for the covered shapes: a leak row carries its
reason, flipping it to clean belongs to the change that earns it, and a clean
row going leak fails loudly. Exit codes must MATCH between compilers on every
cell — a mismatch is a miscompile, never a matrix update — and the underflow
guard (exit 99) fails hard on either side, listed or not. Verdicts, not byte
counts: layout, capacity schedules and #7351's per-string split move totals
legitimately, so zero-vs-nonzero live is the layout-free classification (the
alloc differential's cliff-counter argument, one gate over). ~17 s wall.

## What the first run found

75 of 90 clean-clean pairs (the rest of the leak side):

| cell | verdict | reading |
| --- | --- | --- |
| `str__rebind__{read,unused}` | selfhost leak | the `"STR:"` credit is single-bind only; a rebound string local is refused and never swept |
| `str_arr__rebind__{read,unused}` | selfhost leak | the `string[]` sibling of the same refusal |
| `opt_arr__rebind__unused` | selfhost leak | a rebound `Option[i32[]]` with NO consuming match — the match cell is clean, so the rebind release exists and only the no-match sweep half is missing |

All five are in the REBIND scope, none in `if_block`, `loop_local` or
`shadow_siblings` — the scopes this week's fixes covered measure clean across
every kind, including the two shapes fixed this week (#7360's rc-enum
if-block, #7364's producer-call string payload), which now sit in the matrix
as clean rows that would fail on regression.

## Two traps the build hit, kept here so the next kind addition avoids them

- **String production is not portable between the pipelines.** `i.to_string()`
  is E043 in native without `import "std/i32"`, and every string METHOD
  (`.repeat` included) is stdlib-gated the same way — while the self-host
  driver resolves no stdlib and accepts them as builtins. (That asymmetry —
  self-host accepting importless method calls native rejects — is the
  #7311/#7293 checker-parity family, visible here as `error/clean` pairs.) The
  importless fresh producer BOTH accept is a user concat fn (`mkstr(a) = a +
  "!"`), which neither pipeline const-folds at a call site.
- **The CLI and the test driver measure different programs when anything
  folds** — the CLI const-folds a literal-literal concat, the driver does not
  (the #7364 entry's trap, hit again from the other side). Every generated
  construction embeds the loop variable or routes through a call for exactly
  this reason.

## Regenerating

`FERN_LEAK_MATRIX_DUMP=1` prints every cell's measured line in file format
instead of comparing. Verdicts in the testdata file come from that run, never
from a hand-run CLI.
