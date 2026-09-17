# x86-64's call result: the same slice, plus one thing arm64 did not need

arm64 took the call-result slice and its output dropped 3.3% below the
stack machine (`2026-09-17-the-call-result-stays-in-x0.md`). x86-64 is
still 31.3% above, and 275,973 of its moves — 6.9% of the build — are the
same shape: `%rax` into the home the allocator gave a call's result.

The recipe transfers. One constraint does not.

## What transfers

Same three steps, same order, and the same trap.

1. Move the arms that are not call kinds off `%rax`. The pool is `%rsi`,
   `%rdi`, `%r8`-`%r11` and the callee-saved set, and the arms work in
   `%rax`, `%rcx` and `%rdx`. Taking `%r11` out of the pool and making it
   the working register keeps the pool at six once `%rax` joins.
2. Parameterise what the arms share with the stack machine: `ir_bin_asm`,
   and here also `ir_fround_rax`, `ir_f2i_sat_rax`, `ir_f2u_sat_rax` and
   `ir_i2f_rax`, all of which name `%rax` outright and are reached from the
   unary arm, which is not a call kind.
3. Put `%rax` in the pool, pass its index as `abi_reg` rather than -1, and
   fix the staging paths that assume it is scratch.

**The trap, which cost the arm64 slice a whole debugging cycle:** a call
arm forgets the scratch tracker on ENTRY, and while the working register
and the ABI result register were the same the closing store re-asserted it.
Once they differ the tracker outlives the `call` that clobbers the working
register. Every call arm has to forget AFTER the call as well. The gate's
seventeen programs do not catch it and the compiler self-build segfaults.

## What does not transfer

`idiv` is hardwired: the dividend must be in `%rax`, the remainder lands in
`%rdx`, and `ir_div_guarded` is built on that. Parameterising it is not
possible, so a division CLOBBERS `%rax` — and a division is an ordinary
binary, not a call kind, so the allocator's "no caller-saved value lives
across a call" rule does not cover it.

arm64 had nothing like this: `sdiv`/`udiv` take any registers, which is why
the whole of its instruction table could be parameterised and the pool
change was safe on its own.

So x86-64 needs one concept the allocator does not have: an instruction
that clobbers a fixed register without being a call. The shape that fits
what is already there is a second position list beside `calls` — the
positions of div/rem binaries — and a value may only be homed in `%rax`
when its interval spans none of them. `spans_call` and `calls_before`
already do exactly this for calls and can be reused.

Without that, putting `%rax` in the pool miscompiles any function that
divides.

## Sizing

| | sites |
|---|---|
| `%rax` in the `ssa_*` arms | 106 |
| `%eax` | 26 |
| `%al` | 14 |

A search for the 64-bit name misses a quarter of the work, and a missed
site is a silent miscompile rather than a build error.
