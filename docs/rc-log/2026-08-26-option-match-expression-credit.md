# The Option match-EXPRESSION credit — and the whole container-sink grid is clean

`option__moved` and `option__live` were the last two. All 21 cells now measure
clean on both compilers.

## A correction first

I wrote in #7528's rc-log, and repeated it in #7532's and #7533's PR bodies, that
closing these needs a release **placement** rather than a predicate — that the
free has to be positioned after the match value is computed and before the
return. **That was wrong.**

`return (match (o) { .. })` desugars to a zero-arg IIFE, and `lower_iife` lowers
it INLINE (`lower_value_stmt` for the single-statement body the match desugar
produces). So `o` is read in the current frame, never captured, and the
return-path dec sweep already runs after the value is computed. The placement was
never the problem. The blocker was that `sole_top_level_match_idx` scans top-level
statements for a `StmtMatch` and never looks inside the return's IIFE.

I found this by testing the claim rather than building on it — a throwaway
widening of that predicate took `option_moved` from 300/0 to a clean 300/300 with
nothing else moving. Three PR bodies carry the wrong version; this entry is the
correction of record.

## Where it belongs, and why that mattered

The throwaway left `option_fresh` 100 short per round, and I noted the residual
was probably an artifact of WHERE the special case was bolted on rather than a
second bug. That turned out to be exactly right, and `consuming_match_of`'s own
header had already said why:

> Returned as a 0-or-1 element array so a miss needs no invented Stmt: handing
> back `st` would make the arm analyses read an `if`, and both answer "no escape"
> for a statement they cannot parse, which reads as a proof when it is a blind
> spot.

Bolting the case onto `sole_top_level_match_idx` handed `match_arms_use_name` the
RETURN statement — the same blind spot, one statement kind over. Adding the arm to
`consuming_match_of` instead, so it returns the inner `StmtMatch`, resolved the
residual with no second mechanism.

The OPTSTRUCT collector was also still on the old `sole_top_level_match_idx` +
`stmts[match_idx]` pair while its OPTTUP sibling had moved to
`sole_consuming_match_idx` + `consuming_match_of` in #6319. Converging it is most
of this diff, and is why the nested-block spelling comes along for free.

## The live cell needed one more thing

With the arm in, `option__moved` flipped and `option__live` did not. The reason is
in the cell: its read is `(match (o) { .. }) + p.xs[0]`, so the returned value is
an `ExprBinary` whose LEFT operand is the IIFE, where the moved cell's value IS
the IIFE. The search now descends the operand shapes a returned value takes.

Descending needs a guard, because the free lands after the whole return
statement: a second mention of the name in a sibling operand would read a freed
box. `rc_ml_count_in_expr(r.value, name) != 1` refuses those. Verified rather than
asserted — `return (match (o) { .. }) + peek(o)` measures 300/0 (credit refused, a
safe leak) with the right answer and no underflow.

## Measured

| probe | before | after |
|---|---|---|
| `option_moved` | 300/0 | **300/300** |
| `option_fresh` | 300/0 | **300/300** |
| `option_live_expr` | 300/0 | **300/300** |
| `option_expr_second_mention` | — | 300/0, refused by the guard |

Every other probe unchanged, every exit code matches native, nothing exits 99.
The escape shapes stay safe leaks and should: the option outlives the function, so
nothing may release it there.

## The grid is a regression gate now, not a to-do list

It opened with eight positions leaking. Its header has been rewritten to say what
a new cell should be read against, because the eight slices converged on one
shape: the construction retains a bare-ident element (skipped at a move site), an
`APOWNED:` stamp names the sites the container owns, the container's credit admits
the counted element, and BOTH releases are rc-gated.

That last pairing is the one worth repeating, because three separate slices in
this run got it half-right first: with one owner gated and the other walking
statically, whichever sweep runs first frees the buffers and the other frees them
again. It surfaces as exit 99 and is INVISIBLE in the census, which reads
`allocs == frees` at `live_bytes 0`.
