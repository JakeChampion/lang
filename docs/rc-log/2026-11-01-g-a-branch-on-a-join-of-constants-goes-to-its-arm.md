# 2026-11-01 — a branch on a join of constants goes to its arm

`semthread.split_constant_joins` (new, run after the decided branches), and
`__fern_buf_push` on x86-64 and arm64. Refs #8278.

## The shape

A call that returns its variant in two words meets the match on its result
as a join. The caller tests the tag word, each arm converts the payload word
to its type, and the arms meet in a block of phis: one phi per payload, and a
boolean phi that is true on the Ok edge and false on the Err edge. The match
then branches on the boolean phi.

```
  b0:  v41 = call BufWriter.settle v0 ; v42 = __pair_word v41 ; brif (v41 == 0) b36 b37
  b36: v43 = __word_as:BufWriter v42 ; br b38
  b37: v45 = __word_as:IoError v42   ; br b38
  b38: v51 = phi v43 zero ; v54 = phi zero v45 ; v56 = phi true false ; br b5
  b5:  brif v56 b6 b11
```

`semthread` decided a branch on a constant and a branch on a variant test.
A decision that reaches its branch through a phi was left to the lowered
graph. There, `ssa.thread_bool_joins` moves the arms of a join that holds
the boolean phi alone. This join holds the payload phis beside it, so neither
pass moved it.

The physical lowering then kept a word for each payload on each path, with a
`zero` where the path has no payload. It released that word at the end of
the path. `coreutils/lib/gnu.fern` runs `gnu.settle` once per output line.
It released a null `IoError` on the Ok path and a null `BufWriter` on the
Err path, each through a call.

## The rule

The pass splits a join when four conditions hold. The join holds only phis,
dead copies and constants. Its terminator is a branch on one of its phis, or
a jump through empty blocks to such a branch. That phi has a constant on
every edge. Each arm has the branch as its only predecessor and holds no phi.
Each predecessor then jumps to the arm its constant picks.

Every read of a join phi must lie in one arm: an instruction in a block the
arm dominates, or a phi operand on an edge from such a block. A read at a
merge of the arms keeps the join, because the merge needs a phi for it.

A payload phi an arm reads becomes the operand of the one predecessor the arm
takes. When the arm takes more predecessors, it gets a phi of their operands
at its head. When those operands are the same constant under different ids,
the arm gets one constant instead. A constant of the join moves to the entry
block. The join and the blocks between it and the branch are unreachable
afterwards and go.

Before a split, an empty block that jumps to a join is left out: each of its
predecessors jumps to the join, and each phi reads on the new edge what it
read on the old one. Only a predecessor that ends in a jump moves, so no edge
carries both a condition and a phi. Without this step, the None arm and the
Err arm of `gnu.settle` met in an empty block before the phi block. The read
on that edge lay in neither arm.

`gnu.settle` splits twice. The first split sends the Ok edge straight to the
cell test and the Err edge to the error path. The second split takes the join
of the inlined `kept_failure`. Its tag phi is false on the Some arm, true on
the None arm, and false on the Err arm. The None arm then returns the
writer the Ok edge converted, and no path holds a word for a payload it does
not have.

## The push kernel

`__fern_buf_push` set the range `0..len` and fell into `__fern_buf_push_range`,
which checked the range four times before it copied. The kernel now reads the
length, tests it for zero, tests the capacity and copies. The grow path is
shared with the range kernel. A push of the one-byte line terminator costs 20
instructions on x86-64, against 30.

## Measured

x86-64, `-O`, instructions from callgrind. The `seq.txt` input is
`seq 1 1000000`. The prose input is 30,000 paragraphs of the `fmt` bench
sentence, 4 MB.

| Workload | Push kernel | Both |
|---|--:|--:|
| `shuf -i 1-1000000` | 381,371,560 | 363,309,485 |
| `nl` of `seq.txt` | 222,787,120 | 219,892,015 |
| `fmt -w 40` of prose | 634,751,758 | 632,045,474 |
| `cat -n` of `seq.txt` | 212,035,653 | 212,032,293 |
| `uniq` of `seq.txt` | 201,865,275 | 201,860,665 |
| `cut -c1-3` of `seq.txt` | 188,675,779 | 188,668,366 |
| `fold -w 40` of prose | 51,152,456 | 51,144,546 |

Before both changes, `shuf -i 1-1000000` retired 391,363,995 instructions.
The output of each workload is byte for byte the same. `gnu.settle` retires
27 instructions a line against 41, and the release calls on its null words
are gone. What remains on the None path is the retain and release pair on the
`Option` the cell read hands out, 14 instructions a line.

The compiler compiled by itself, `fern.fern` built by a stage 1 with and
without the split:

| | Push kernel | Both |
|---|--:|--:|
| stage-2 compiler, bytes | 15,788,720 | 15,755,952 |
| stage 2 compiling `lexer.fern` to x86-64 asm, Ir | 2,228,812,982 | 2,224,225,599 |

The two stage-2 compilers emit the same assembly for `lexer.fern`.

## Tests

`TestSelfHostConstantJoinSplit` matches on a two-word result handed back
whole by a function that looked inside it. The caller that reads each word
in one arm has no phi left in its semantic graph. The caller that reads the
Ok word after its arms meet keeps its join. The program exits 42 on every
host target. `TestSelfHostConstantJoinSplitCensus` runs it under the leak
check and the sanitizer.

`TestSelfHostSSASelectsAreConditionalMoves` pinned a conditional move in
`tagged` and `phi_tag`, which match on the variant an inlined `probe`
returns. Each payload is read in one arm, so the join splits and each arm
goes straight to its own return or update. The two now pin that no
conditional move is left. `tag_kept` reads the tag twice, so its join stays,
and it pins the select on a tag phi instead.

## Next lead

The None arm of `kept_failure` retains the `Option` it reads out of the
writer's cell and releases it after the tag test. The pair is 14 of the 27
instructions `gnu.settle` still retires a line.
