# arm64: the call result stays in x0, and the output drops below the stack machine

The slice `2026-09-17-the-call-result-move-is-the-x86-gap.md` specified, taken
on arm64.

## The numbers

Whole compiler, `-emit asm`, against the same main without the change:

| | flat | ssa before | ssa after | after, over flat |
|---|---|---|---|---|
| arm64 | 4,043,922 | 4,112,320 | 3,909,046 | **-3.3%** |
| x86-64 | 3,043,396 | 3,992,473 | 3,996,154 | +31.3% |

arm64 register-allocated output is **smaller than the stack machine's** for
the first time, by 134,876 instructions, having been 68,398 larger. Compile
time does not pay for it: `checker.fern` through `-backend ssa` is 7,430 ms
against 7,535 ms before, best of three.

x86-64 is unchanged because it passes `abi_reg` -1: its emitter still uses
`%rax` as its working register, which is the same step 1 this entry
describes, not yet done there.

## What the change is

Three pieces, in the order they have to happen.

**The arms move off x0.** Every arm that is not a call kind used x0 as its
working register — the binary arm forced its destination there, the unary
and memory arms too — so a value homed in x0 would be destroyed by the next
arithmetic instruction. Those arms now work in x4-x7. The arms that ARE call
kinds keep x0, because the allocator never leaves a caller-saved value live
across a call.

`ir_bin_asm` is shared with the stack machine and named x0/x1/x2 outright,
so it takes them as parameters: the stack machine passes its own, the
register path passes x4/x5/x6.

**x0 joins the pool** as index 0, and three staging paths that assumed it
was scratch had to stop: `ssa_push_operand` stages a spilled argument
through x4, `ssa_load_params` loads a parameter straight into its home
rather than through x0, and the arms that fill the ABI's argument registers
in order load the argument homed in x0 first, so nothing is read after it is
written.

**The allocator prefers it for a call result.** `regalloc_linear` takes the
pool index of the ABI's result register and gives it to a value a call
defines when it is free, and to everything else last. The store after the
call then finds the value already home and emits nothing.

## The trap this set, and how it was found

The gate's seventeen programs passed the whole way through. The compiler
compiling itself segfaulted.

The cause: a call arm forgets the scratch tracker on entry, and the closing
`ssa_store` used to re-assert it because the result and the working register
were both x0. With the working register moved to x4, the tracker was left
claiming x4 held the value that had been staged into it before the `bl` —
and the callee clobbers x4. A later read of that value was elided against a
register the call had destroyed.

Narrowing it: 520 conformance programs built on both backends and run,
6 disagreed; `FERN_SSA_SKIP` over the failing module's 21 functions named
the pair; the emitted text showed `ldr x4, [x4, #24]` reading a base the
`bl` above it had overwritten. **A single function through `FERN_SSA_ONLY`
never reproduced it** — the caller has to be register-allocated for the
shape to exist at all, so a one-function sweep cannot find this class.

Every call arm now forgets after the call as well as before.

## Checks

- The backend gate on arm64-darwin.
- 520 conformance programs, both backends, arm64-linux under the container:
  same exit and same output on every one.
- Stage two identical on both targets: the SSA-built and flat-built
  compilers emit byte-identical assembly for `checker.fern` on arm64 and
  `irlower.fern` on x86-64.
- The complexity ratchet, the source lint and the testname gate.
