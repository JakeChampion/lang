# The self-host SSA optimiser costs more than it saves, as written

No slice landed from this. It is here so nobody builds it twice.

`ssa.optimize` is the pass pipeline `build_func` was written for. The
backend does not call it: it runs `ssa.prune_dead` and goes straight to
register allocation. Turning it on for the production lift looked like the
next item, so it was built and measured. It should not be turned on.

## What the pipeline does to a lifted function

Measured by running each pass alone against the backend gate on
arm64-darwin, which is how `const_fold` was caught:

| pass | on a lifted function |
|---|---|
| `copy_propagate` | inert: the production lift emits no `copy` |
| `const_fold` | **wrong**: folds every binary to 0 |
| `algebraic_simplify` | inert |
| `cse` | partly live: keys match, commutativity does not |
| `branch_simplify` | live |
| `merge_blocks` | live |

The two that are wrong or half-working are wrong for one reason, and it is
not the one the earlier note in this directory guessed. `eval_binary`,
`simplify_binary` and `is_commutative` all switch on SOURCE operators —
`"+"`, `"=="`, `"<<"` — because that is what `build_func` put in an
instruction's `str`. The production lift puts an IR kind name there
(`"add"`, `"eq"`, `"shl_s"`), which every one of those comparisons misses.
`eval_binary` returns its trailing 0 for an unrecognised operator, so
`const_fold` replaces a binary of two constants with the constant 0 —
`control_flow` and `host_calls` both fail the gate on it. `simplify_binary`
matches nothing and rewrites nothing. `cse_key` still keys correctly
because it treats `str` as opaque, and only loses commutative matches.

Porting that vocabulary to IR kinds is the work the earlier note called
"one pass's worth". It is three functions, and `eval_binary` additionally
needs the width and signedness the kind and `imm` carry, since it folds
through i32 and the constant table is i32.

## Why it is not worth doing

The three passes that DO work were enabled alone — `copy_propagate`,
`cse`, `branch_simplify`, `merge_blocks`, then the existing `prune_dead` —
and measured on the whole compiler, against a compiler built from the same
main without them:

| | without | with | change |
|---|---|---|---|
| arm64 instructions | 4,117,381 | 4,068,467 | -1.2% |
| x86-64 instructions | 3,997,483 | 3,940,926 | -1.4% |
| compiling `checker.fern`, best of 3 | 7,521 ms | 9,180 ms | +22% |

A fifth of the compile time for a fiftieth of the output. Taking the
fixpoint out and running the sequence once changes neither side: 4,068,314
instructions and 9,065 ms. So the cost is not the iteration, it is the
passes.

Each pass rebuilds every block and every instruction of every function
whether or not it changes anything, and the compiler has no GC, so four
unconditional rebuilds per function replace the one `prune_dead` already
pays. `copy_propagate` is the only pass that already declines to rebuild
when it has nothing to do, through `has_copy`.

## The obvious fix was tried and does not work

The first version of this entry said a no-op guard on the passes would
make the 1.2% cheap enough to want. It was built and measured, and it
does not: `merge_blocks` already returns its input when it finds no
mergeable pair, and guarding the other two moves nothing.

| | baseline | optimiser on | optimiser on, guarded |
|---|---|---|---|
| arm64 instructions | 4,117,381 | 4,068,467 | 4,071,087 |
| x86-64 instructions | 3,997,483 | 3,940,926 | 3,943,576 |
| compiling `checker.fern`, best of 3 | 7,521 ms | 9,180 ms | 9,125 ms |

Fifty-five milliseconds of the sixteen hundred, and slightly worse output,
because a guard that declines a rewrite declines the rewrites it cannot
see either.

The reason is that the guard costs what the pass costs. `branch_simplify`
has to know whether any `brif` reads a constant, which means building the
constant table, and that table is an `i32[]` written through `.with` once
per constant — an array copy each time unless the value is uniquely owned.
`cse`'s guard walks every instruction and appends to a per-block array.
Neither scan allocates less than the rebuild it is trying to avoid.

So the per-pass cost is not "rebuilding when nothing changed". It is the
shape these passes are written in: dense per-value `i32[]` tables rebuilt
through `.with`, and a fresh `SBlock` array per pass per function, over
9,159 functions with no GC. Making the pipeline worth enabling means
changing that, not adding early exits to it — and 1.2% of the output is a
small prize for it.
