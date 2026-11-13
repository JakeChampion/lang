# 2026-11-13 — a reuse allocation tests its token inline

x86-64 and arm64. `ssa.inline_reuse_call`, `ssa.fresh_boxes`, and
`ssa_reuse_alloc` in each native emitter. Part of #15610.

## The shape

`__fern_alloc_reuse(token, size)` returns the token when the token is a box
of that size. It allocates when the token is null. It releases the token and
allocates when the token has another size.

Each reuse allocation was a call. The helper ran five instructions for a
token of the right size. The call also made each value that lives across it
take a callee-saved register or a stack slot. `ins` of the red-black tree
workloads has two such calls on its hot path. It has more values than
callee-saved registers.

## What changed

The emitters test the token inline:

```
    testq %rax, %rax
    jz    fresh
    cmpq  %rsi, -16(%rax)
    je    done
    call  __fn___fern_alloc_reuse.r     a token of another size
    jmp   done
fresh:
    movq  %rsi, %rdi
    call  __fern_arr_box
done:
```

The allocator gives the instruction the `COLD_SLOW` kind
(`ssa.cold_positions`). The two calls save the caller registers that hold
live values, as the slow call of `arr_push` does. A value can thus stay in a
caller register across a reuse allocation.

A reuse allocation in a cold block keeps the `COLD_BRANCH` kind. A `-size`
build keeps the call.

arm64 has a new register entry, `__fn___fern_alloc_reuse.r`. It takes the
token in `x0` and the size in `x1`.

The physical lowering puts some reuse allocations on the null side of a test
of the token. `ssa.split_tokens` gives others a constant null token.
`ssa.fresh_boxes` marks each of them with `ssa.REUSE_NULL`, and the emitters
call `__fern_arr_box` for a marked instruction, with no test.

## Measured

x86-64, Callgrind instructions, against main at `a509fb08`. The answers are
equal on x86-64 and on arm64 under qemu.

| Workload | Size | main | this change | |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 100,000 | 193,069,472 | 183,292,328 | -5.06% |
| rbtree-ck | 100,000 | 194,746,401 | 186,488,235 | -4.24% |
| deriv | 6 | 2,765,146 | 2,772,789 | +0.28% |
| nqueens | 8 | 1,165,486 | 1,165,486 | 0 |
| cfold | 12 | 2,193,684 | 2,194,615 | +0.04% |

Stack traffic in `ins` fell from 31.4% of its instructions to 26.9% before
the null mark was added.

## Traps

- `deriv` rises because its tokens are null more frequently. `powr` has one
  reuse allocation with a live caller register. The slow path needs save
  slots, so `powr` now has a frame, which is three instructions for each
  call.

- A rewrite of the marked instruction to `ARR_BOX` made `cfold` 1% slower.
  The allocator gave the two operand constants no home, and the values of
  `cfold` moved to worse registers. The mark keeps the instruction as main
  has it, so only the emitted call changes.

- The immediate of a `CALL` is in use: 1 to 3, 4 and 5 for the count
  primitives, and 6 for a call that returns a lane vector. `REUSE_NULL` is 7.
  A first attempt with 6 made the result a vector, and the stage 1 compiler
  failed its smoke test.
