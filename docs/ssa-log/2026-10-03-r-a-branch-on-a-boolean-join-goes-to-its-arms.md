# 2026-10-03 — a branch on a boolean join goes to its arms

`ssa.thread_bool_joins`, run by `register_form`, both native targets. Refs
#8822.

## The shape

`semsource.cond` lowers an `&&` or `||` written in a condition as a chain of
branches. A predicate spliced into its caller is a `return` of the same
expression, so it arrives as a value instead: each arm leaves a 0 or 1, the
two meet in a phi, and the caller's `if` branches on the phi. `if
(!is_digit_b(x))` with `is_digit_b` returning `c >= 48 && c <= 57`:

```
  b10: v42 = binary le_s v36 v41 ; br b8
  b9:  v43 = const_int 0         ; br b8
  b8:  v45 = phi v42 v43         ; br b7
  b7:  brif v45 b5 b1
```

```
    cmpq $48, %r8
    jl .Lssa_numeric_key_70
    movq %r8, %r9
    cmpq $57, %r9
    setle %r9b
    movzbq %r9b, %r9
    jmp .Lssa_numeric_key_69
.Lssa_numeric_key_70:
    xorl %r9d, %r9d
.Lssa_numeric_key_69:
    testq %r9, %r9
    jz .Lssa_numeric_key_10
```

Spelling the test out at the call site was cheaper than calling the helper,
so `coreutils/sort.fern` spelled it out. Putting the calls back cost the
self-host build 4.8% of `sort -n` and 3.2% of `sort -k2,2n`.

## What changed

After the first `prune_dead`, an arm that ends in a plain branch to the join
branches where the join would have sent it: to the target its constant picks,
or on its own value. That value is the comparison the arm just made, which
the emitters fuse with the branch. A join no arm enters any more goes, with
the empty block holding its branch, and a second `prune_dead` drops the
constants that fed it:

```
  b11: v39 = binary ge_s v36 v38 ; brif v39 b10 b1
  b10: v42 = binary le_s v36 v41 ; brif v42 b5 b1
```

The join must hold the phi alone, read by nothing but the branch (its own,
or that of an empty block it falls into). Every arm must enter it from a
block laid out before it, and both targets must lie past it and have no
phis. A block that strictly dominates the join dominates each arm, so moving
an arm's edge changes no other block's dominator, and the range facts
`drop_low_wraps` found earlier still hold.

Two shapes are left alone because nothing produces them: an arm that leaves
through a conditional branch, which for a non-constant value would need an
edge block, and a target with phis, which would need an operand per new
edge. Compiling the self-host compiler and every coreutil, the pass moved
30,219 arms, 15,364 to a constant's target and 14,855 onto their own branch,
and met neither.

## Measured

Instructions under callgrind, 4-core x86-64 container, `-O` builds of
`coreutils/sort.fern` by the self-host compiler built from main at 25147179
and from this change on it. The 200k-line inputs are the first 200k lines of
`scripts/coreutils-bench.d/sort.sh`'s; "ties" is 200k `-n` keys with 50
distinct integer parts and a fraction, so `numcompare` decides them.

| workload | main | this change | this change, helpers called in `sort.fern` |
|---|--:|--:|--:|
| `sort -n` | 222.84 M | 222.85 M | 223.04 M |
| `sort` | 166.47 M | 166.47 M | 166.47 M |
| `sort -k2,2n` | 316.24 M | 282.13 M (−10.8%) | 282.32 M |
| `sort -k1,1` | 233.30 M | 223.02 M (−4.4%) | 223.42 M |
| `sort -n`, ties | 1,424.72 M | 1,384.90 M (−2.8%) | 1,385.10 M |
| `sort -c`, 500k sorted | 104.82 M | 104.83 M | 104.82 M |

`-k2,2n` and `-k1,1` gain on main's own source because `begfield` and
`limfield` call `is_blank_b`. With the helpers called rather than spelled
out, the self-host build is within 0.2% of the hand-written source, where on
main it was 3–5% behind. `numeric_key` is the same instructions either way;
what is left is one reload a line in `numeric_keys`, whose allocation the
spliced blocks shift.

The compiler itself, stage 2 built from main and from this change:

| | main | this change |
|---|--:|--:|
| stage 2 compiling `checker.fern`, total Ir | 23.797 G | 23.665 G (−0.55%) |
| stage-2 compiler, bytes | 11,331,104 | 11,293,152 (−0.33%) |
| `set*` in the compiler's x86-64 assembly | 2,308 | 1,352 |
| `cset` in the compiler's arm64 assembly | 2,298 | 1,342 |

## Witnessed

`TestSelfHostBoolJoinBranch` is new. `digits`, `skip_space` and `word_char`
branch on spliced `&&` / `||` predicates and must materialise no flag on
either target; `flag`, which returns the predicate as a value, must still
materialise one. On main the three materialise one, two and one flags on
both targets. The program checks each predicate at its boundaries and exits
42 on every host target.

Stage 2 and stage 3 are byte-identical. Also run: the self-host SSA suites
(`TestSelfHostSSA*`), the cond-branch, compare-fusion, short-circuit,
inline-leaf, lea, leaf-frame, result-register and redundant-wrap tests, the
arm64 branch and condition differentials, `TestFernFixturesSelfHost{X86_64,
Arm64,Wasm}`, and the `sort` leg of `TestSelfHostCoreutilsParity` (497
cases). `TestSortParity` ran against GNU 9.4, the only tree this container
has; its 21 failures are all 9.4's wording of two messages the corpus holds
to 9.12's.
