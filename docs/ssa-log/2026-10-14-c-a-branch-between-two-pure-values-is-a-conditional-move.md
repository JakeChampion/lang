# 2026-10-14 — a branch between two pure values is a conditional move

x86-64 and arm64. `ssa.select_branches`, `ssaprepare.with_selects`, the
`select` SSA kind (36), and `ssa_select` in both register emitters.

## The shape

`if (a < b) { a } else { b }` compiled to a compare, a conditional jump and
a move on each arm. Neither ISA's emitter ever produced `cmov` or `csel` for
user code: the only five `cmov`s in `asm_ir.fern` were hand-written runtime
sequences. A branch the data decides at random mispredicts about half the
time.

Measured natively on the x86-64 container: a loop calling a `@noinline` min
100 M times on `(x >> 16) & 1023` of an LCG against 512 took 554 ms. The same
binary with the branch patched by hand to `cmovge` took 194 ms. (On the low
bits, `x & 1023`, the LCG's short period made the branch predictable and the
branchy version ran in 170 ms, which is why the benchmark reads the high
bits.)

## What changed

After the certifier has read the register form, `select_branches` flattens
each branch whose arms compute a few pure integer values and then meet:

- **At a join.** The join is entered from the two arms alone and holds at
  most two phis. Each phi becomes a select on the branch's condition, and the
  arms' instructions move up into the branching block.
- **At two returns.** Each arm returns, and the two return values become one
  select and one return.

An arm is the edge itself, or a block entered from the branch alone. It holds
at most three instructions, all constants, copies, the integer operators that
cannot trap, or the integer unaries; together the two arms hold at most four.
A line marker is not one of them, so a `-g` build keeps its branches, and
its stepping. A float the emitters keep in a float register stays a phi.

A branch whose condition the branching block computes with a call or an
inline rc primitive, or with a `!` of one, is left alone. The uniqueness test's guard chain branches
as it computes, and it sets the very flags the branch reads. Taking those
branches made `ordmap_insert` 14% slower in wall time: the select added a
materialized 0/1 and a second test, and every reuse-token test in
`__om_insert` went through it.

The emitters render a select as `cmov` (x86-64) or `csel` (arm64). When the
condition is a compare that only a run of selects right after it reads, the
first select sets the flags and the rest read them, since a move leaves them
alone. Otherwise the condition's value is tested. A select's result may take
the register of either value it picks, so `min` is `cmp; cmovge; ret`.

The pass runs after `irverifygate.certify_or_refuse`, on a form the
certifier has already proved. What it moves holds no reference-count
operation, so the proof still holds.

## Numbers

The min above: 554 ms before, 171–204 ms after, best of three, x86-64
native.

The compiler compiling itself: 1,110 `cmov`s, 1,136 fewer conditional jumps
and 4,267 fewer lines of x86-64 assembly. Stage 3 equals stage 4.

`scripts/perf-bench`, x86-64, against main b45a320a:

- `.text` shrinks on most rows, by up to 1.2% (`sort_ints`).
- `.ir` rises wherever a predictable branch became a select, because a
  select runs both arms:

  | Benchmark | `.ir` change |
  |---|---|
  | `sort_ints` | +5.5% |
  | `string_build` | +2.2% |
  | `struct_drop` | +1.7% |
  | `sort_strings` | +1.2% |
  | `http_hello` | +0.7% |
  | every other row | under 0.3% |

Callgrind charges every instruction the same and has no branch predictor.
Two other measurements:

- **Cachegrind's branch simulator.** Mispredicts fall on most rows:
  `struct_drop` 1.16 M to 0.44 M, `sort_inplace` 209 K to 152 K. Instructions
  plus 15 cycles per mispredict nets between −1.8% (`struct_drop`) and +4.0%
  (`sort_ints`).
- **Wall time.** `sort_ints` scaled up to 600 rounds is 5–10% faster than
  main (min and median of six), despite its 5.5% more instructions.
  `struct_drop` scaled up is within noise (−2% by the minimum, +4% by the
  median). Identical binaries vary by up to ±20% at the suite's 5 ms scale on
  this container, so the short rows' wall times are not evidence either way.

## Traps

- `inst_has_effect` treats every kind from 19 up as an effect unless it is
  listed, so the select had to be listed for `prune_dead` to drop an unread
  one.
- The x86-64 emitter loads a constant zero with `xor`, which clobbers the
  flags. A select reads no immediate (`ssa.imm_operands` keeps a home for
  anything it reads), and the arms' constants are hoisted ahead of the fused
  compare, so no constant is loaded between the compare and its `cmov`.
- The arms are hoisted ahead of the compare that ends the head, so the
  compare stays beside its select, but never ahead of a phi. When the
  condition was the join's own tag phi (a `match` on a variant an inlined
  function returns), the phi landed after the hoisted adds. The allocator
  then started its interval there and gave it the register of a payload phi
  the adds still read. `TestSelfHostScalarEnumBlockHazardsX86_64` caught it
  as a wrong answer; `phi_tag` in the select test pins it.
- `TestSelfHostCmpBranchFusion`'s shapes were `if` with constant arms, which
  are now selects. Their else arms call, so they stay branches, and the
  polarity matrix gained a calling consumer beside the two that are now
  selects.
- `TestSelfHostCtGateX86_64`'s secret-branch controls branched on a secret
  byte with arms that only return a constant. Those became a cmov, which
  memcheck rightly no longer reports as a secret-dependent jump. The
  controls now return through a call, so they stay branches.
- The retained-body drivers read the emitted form, selects included.
  `ssacreditscalar` refused the select as an unknown effect, and the driver
  then exited early with its census unbalanced. That read as a 252 KB leak
  until the driver's own output was checked. It now reads a select like a
  phi, choosing its operand when the condition is known. The compare-fusion
  control in `ssaretainedallocation` pinned `if (a > b) { return 7 } else {
  return 9 }`, which is now a select. Its else arm divides, so it stays the
  branch the control is about.
- A returned select asks for the result register, but a fused compare reads
  its operands at the select, so the register was still held by one of them
  and the select took an operand's register instead, then moved it to
  `%rax` before `ret` (`TestSelfHostReturnValueInResultRegister`). The
  allocator now gives a value its wished-for register when the holder dies
  at the definition and the instruction reads before it writes, the rule an
  operand's register already followed.
- `TestSelfHostOptimisationShapes/value_test_in_place` pinned arm64's
  `cbz` on a boolean whose arm only returned a parameter, which is now a
  `csel`. The arm calls, so it stays the branch the case is about.
- A stdlib root spelled with `..` made the benchmark harness's compiles fail
  as an "import cycle" (#12398), on main as well.
