# 2026-11-04 — a fused compare takes no register

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

`__fern_str_cmp` is a leaf: it calls nothing. It still saved and restored
`%rbx` and `%r12`, and its body never used `%r12`.

The allocator gave a register to each compare that a branch reads. The
emitter fuses such a compare into the branch: it writes one `cmp` and one
conditional jump, and the compare's own value is never written. The
register stayed occupied for the value's interval all the same.

In `__fern_str_cmp`, the byte loop's compare took register 10, which is
`%r12`. `ssa_saved_regs` counts each register that a value holds, so the
prologue saved `%r12`. The `ls` program had four such functions.

## The rule

- `ssa_homeless` adds two kinds of values to the set that takes no home:
  the compares that `ssa_fuse` fuses into a block's branch, and the compare
  before the first select of a run that reads its flags.
- Both native backends pass that set to `regalloc_linear_fixed` in place of
  the immediates alone.
- The fusion predicates take a `FuseView`, which holds the use counts and
  the immediates. The emitter builds it from its frame, and the allocator
  builds it before a frame exists.

| x86-64, `ls` of 4000 names | before | after | GNU 9.4 |
|---|--:|--:|--:|
| whole run | 11,913,650 | 11,822,097 | 11,298,193 |
| callee-saved pushes in the program | 2,636 | 2,628 | |

## What is still behind in `__fern_str_cmp`

The function still saves `%rbx`. A value of the byte loop has an interval
that reaches the exit blocks, which come last in the order. The `tzcnt` exit
clobbers `%rcx` and `%rdx` inside that interval, and the value is dead
there. An interval with holes can give that value a caller-saved register.

## Tests

`TestSelfHostSSASavedRegistersUsed` compiles a program that reaches
`__fern_str_cmp` and requires that each compiled function uses each
callee-saved register it saves. It fails on main with
`__fn___fern_str_cmp r12`.
