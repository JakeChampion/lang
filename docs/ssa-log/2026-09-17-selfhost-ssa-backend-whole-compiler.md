# Measured 2026-09-17: the whole compiler through the self-host SSA backend

PRs #9566, #9567, #9569 and #9570, in that order, on the build #9512
merged. Same container and method as the earlier entries: arm64 Linux,
native arm64 execution, x86-64 output under qemu-x86_64; instruction
histograms from `-emit asm` on arm64-darwin on the same sources.

## Coverage, on the compiler compiling itself

| build | through the backend | declined on |
|---|---|---|
| #9566 (frame and lift fixes) | 8,828 of 9,020 | `const_f64` 75, `env` 21, `read_file` 20, `exit` 10, `syscall3` 9, the rest under 6 |
| #9567 (f64) | 8,986 of 9,092 | the host floor: `env` 21, `read_file` 20, `exit` 12, `syscall3` 9, `strbuf_*` 10, the rest under 5 |
| #9569 (host floor) | 9,105 of 9,119 | `strbuf_take` 5, the map ops 3, the byte buffer 4, `arr_push_shared_bytes`, `args` |
| #9570 (the rest, frame from sp) | 9,129 of 9,130 | `heap_bump_bytes`, an op that arrived on main meanwhile |

Every probe program goes through whole: the host, float, mixed and map
probes at 123 of 123, 104 of 104, 120 of 120 and 111 of 111; prime_gaps at
161 of 162 (`memcpy` in `__fern_str_concat` before #9570, whole after).

## Size and time, on the compiler compiling itself

| | flat | ssa, #9566 | ssa, #9570 |
|---|---|---|---|
| compile time | 128 s | 134 s | 142 s |
| binary | 18.5 MB | 29.1 MB | 21.5 MB |
| text, arm64 instructions | 4,206,795 | 7,008,226 | 5,013,831 |
| stage-2 output on `lexer.fern`, `checker.fern` | | identical | identical |

The binary was 36.1 MB before #9566's dense frame slots. Where the
2.8M extra instructions of #9566 were, and what #9570 did to them:

| class | flat | #9566 | #9570 |
|---|---|---|---|
| `sub x16, x29, #off` (frame address past 256 bytes) | 589,647 | 1,413,863 | 0 |
| `movz` (frame offsets past 4 KB, mostly) | 107,170 | 418,781 | under the top nine |
| frame loads and stores | 1,062,000 | 2,022,000 | 1,949,000, one instruction each |
| `b` | 162,517 | 445,317 | 192,561 |
| register-to-register `mov` | 61,559 | 864,905 | 865,114 |

A frame slot past 256 bytes below x29 cost a `sub x16, x29, #off` before
every access and past 4 KB a `movz` too; with the frame at rest the same
slot is at a positive offset from sp, which one scaled `ldr`/`str`
encodes up to 32 KB. The 250k branches removed were jumps to the block
emitted next. The 865k moves that remain split into 529,606 results moved
out of x0 and 335,412 operands moved into x0/x1/x2; the shared binary table
accounts for 57,025 of them, the 333k call results for the rest of the
stores. The next item is therefore the SSA-owned kinds writing to the
result's home and reading operands from theirs, not the table.

## The emitter items, on the compiler compiling itself

| build | instructions (arm64) | binary | compile |
|---|---|---|---|
| flat | 4,206,795 | 18.5 MB | 126 to 132 s |
| #9570 (frame from sp, no branch to the next block) | 5,013,831 | 21.5 MB | 142 s |
| #9571 (results to their home, operands from theirs) | 4,814,582 | 20.7 MB | 134 s |
| #9573 (a compare read only by its branch as flags) | 4,712,644 | 20.3 MB | 135 s |
| #9579 (values live across calls in callee-saved registers) | 4,691,722 | 20.2 MB | 137 s |
| #9595 (no reload into x0 of the value it holds) | 4,595,948 | | |

#9571 took the moves out of x0 from 529,606 to 376,860 and the moves into
x0/x1/x2 from 335,412 to 295,644; #9573 took `cset` from 44,047 to 9,109
and `cbz` from 100,742 to 65,927, with 51,073 `b.<cond>` in their place.
Widening the caller-saved set from seven registers to eleven (x4 to x7,
which no SSA sequence uses) changed the text by 3,385 instructions, 0.07%,
so the 1.96M frame loads and stores were call-crossing values, not register
pressure. #9579 gave those values callee-saved registers: frame loads fall
from 1,193,827 to 768,081 and stores from 772,393 to 589,593, the moves rise
to 1,069,458 since an operand that was loaded is now moved, and 25,190
register pairs are saved in prologues. #9595 then drops the 87,196 moves
that reloaded into x0 the value just moved out of it and the 15,420 slot
reloads of the same shape.

## The output's speed

The compiler built with `-backend ssa` compiling one of its own modules
with `-emit asm`, best of three, against the flat-built compiler; the
outputs are byte-identical on every row.

| | flat-built | SSA-built, #9578 | SSA-built, #9579 |
|---|---|---|---|
| checker.fern, arm64-darwin | 13,214 ms | 12,345 ms | 10,202 ms |
| irlower.fern, arm64-darwin | 4,311 ms | 2,687 ms | 2,208 ms |
| checker.fern, arm64-linux container | 13,352 ms | | 10,300 ms |
| irlower.fern, arm64-linux container | 4,281 ms | | 2,279 ms |

## The corpus

Every `examples/**/*.fern` outside `self_host`, built both ways for
arm64-linux and run: 348 programs, 322 agree on stdout and exit status, 3
differ only by nondeterminism (a timestamped path, a `yes` cut by the
timeout, a benchmark's microsecond column), 23 fail to build on both paths
as they did before the backend existed. The same on every build from #9569
to #9573. Before #9566's loop-end fix, eight of those programs built under
flat and not under SSA. Programs whole through the backend: 47 on #9570,
296 on #9572 once the string ops went through the stack machine's arm,
with 53,493 of the corpus's 53,550 functions. The eight functions #9566's
loop-end decline kept on the stack machine (an exhaustive match whose every
arm returns, standing last in a function) are lifted by #9578.

Two miscompiles the corpus caught between those builds, each fixed before
its PR merged: pmap_test exited 139 on #9571's first head because the
arm64 folded-aggregate arm stored x0 over the address it had just computed
into the result's home; and the x86-64 `args` arm of #9570 did not mark the
need that emits `__fern_args`, found by review.

## x86-64 against arm64, on the compiler compiling itself

Both ISAs at the head of the stack, `-emit asm` over the whole compiler,
counting emitted instructions:

| | flat | ssa | ssa over flat |
|---|---|---|---|
| arm64 | 4,266,714 | 4,603,769 | +8% |
| x86-64 | 3,211,906 | 4,580,160 | +43% |

The two SSA columns are within half a percent of each other. The two flat
columns are a million apart, and that is what the +43% is measuring: the
x86-64 stack machine is the compact one, not the x86-64 SSA backend the
loose one. `pushq -N(%rbp)` stages an operand in a single instruction,
where the arm64 stack machine needs an address and a store, so flat x86-64
carries 436,837 pushes of a frame slot against arm64 flat's none.

Where the SSA backend's own 1.37M extra instructions are on x86-64, by
`movq` class, against flat's same classes:

| movq | flat | ssa | excess |
|---|---|---|---|
| register to register | 28,664 | 887,817 | +859,153 |
| frame to register | 385,451 | 899,776 | +514,325 |
| register to frame | 468,395 | 669,352 | +200,957 |

The register-to-register half is the larger one and it is the item arm64
already names: a result computed in the scratch register and then moved to
its home, an operand moved from its home into a scratch register. arm64
carries 865,114 of those and x86-64 carries 887,817, so this is one item
across both ISAs rather than an x86-64 item. Of x86-64's, 382,990 move
into `%rax` and 429,579 move out of it, and only 11,587 sit directly after
a call — so this is the arithmetic selection routing through the scratch
register, not the call ABI. The shared binary table names `%rax` and
`%rcx` outright, which is what forces it. Neither stack machine folds a
frame slot into an arithmetic operand, so that is not the difference and
not the fix.

Correctness is not in question here: the compiler built for x86-64-linux
on each backend, run under qemu on a program carrying the slice-then-reuse
shape, emits byte-identical assembly at 144,505 bytes. The compiler binary
is 21.4 MB against flat's 15.0 MB.

## Staging a call argument, measured

The argument staging was the largest single item and it is now done. A call
argument used to load the value into the scratch register and push that;
it now pushes the value's home. On x86-64 that is `pushq -N(%rbp)` for a
spilled value, exactly what the stack machine emits, and the frame
addresses from `%rbp` so the pushes do not move it. On arm64 a
register-resident value stores straight from its home, and a spilled one
still goes through x0 because there is no store from memory.

| | flat | ssa before | ssa after | after, over flat |
|---|---|---|---|---|
| arm64 | 4,268,783 | 4,603,769 | 4,324,767 | +1.3% |
| x86-64 | 3,213,372 | 4,580,160 | 4,168,281 | +30% |

arm64 lands within 1.3% of the stack machine. x86-64 keeps a wider gap
because its stack machine pushes a frame slot in one instruction where
arm64's needs two, so the SSA backend had more of that saving already
banked and less left to take.

The x86-64 differential over all seventeen corpus programs agrees with the
stack machine on exit code and output, and the stage-two build is
identical.

**Measured and not taken.** Computing a binary into its result's home
instead of the scratch register, which is the register-to-register move
above, was built and measured: it moves 11,084 of the 18,737
register-form binaries off `%rax` and changes the whole-compiler total by
298 instructions, upward. Binaries whose result and left operand are both
register-resident are rare enough that the saved move and the added one
cancel. It is not worth the register-parameterised instruction table it
needs, so the table still names `%rax` and `%rcx`.

## The optimiser on the lifted function, measured

The backend runs `ssa.prune_dead` and nothing else. Calling `ssa.optimize`
on the lifted function instead fails the backend gate on `control_flow`
(exit 169 against the stack machine's 171) and `host_calls` (exit 63
against 58, and differing stdout).

Running each pass of the pipeline alone against the same gate isolates it
to one:

| pass | gate |
|---|---|
| `copy_propagate` | clean |
| `const_fold` | control_flow, host_calls fail |
| `algebraic_simplify` | clean |
| `cse` | clean |
| `branch_simplify` | clean |
| `merge_blocks` | clean |

`const_fold` folds a `binary` through `eval_binary(op, l, r)`, which takes
two i32s and returns one. The lifted binaries carry their width and
signedness in the instruction's `imm`, which that signature cannot see, so
a 32-bit or unsigned op folds at 64-bit signed width and the constant is
wrong. The other five passes are structurally conservative: each keys off
kinds 1, 2, 9 and 10 and leaves every production kind alone.

So enabling the optimiser here is one pass's worth of work, not the
pipeline's: `eval_binary` needs the width and the sign, and `const_fold`
needs to pass them.

## Traps

- **The runtime call's argument registers are allocatable on x86-64.**
  `ssa_rt_call` loaded the first argument into `%rdi` and then the second
  into `%rsi`; when the second's home was `%rdi` the first load overwrote
  it. Fixed in #9566 by reading both homes into scratch first, and
  reintroduced for an hour in #9570 by a reordering that moved into `%rdi`
  between the two reads. Both homes are read before either argument
  register is written; the machine stack is the staging area when there
  are more than two.
- **A push inside a call sequence moves sp.** With frame slots addressed
  from sp, a load between two argument pushes must add the bytes already
  pushed; `ssa_load_pushed` carries them, and the x29 form stands for a
  frame past 32 KB.
- **The lift's loop end dropped a live block.** A body whose last statement
  is a `return` reaches the loop's `end` as a live, terminated block that an
  earlier `if` already branches to; it was never appended. `std/json`'s
  array parser is that shape.
- **A frame slot per value id.** The lift's phi per local per merge made the
  id space hundreds of times the live values; one parser function reserved
  2.6 MB of frame and the SSA-built compiler ran off its stack on
  `lexer.fern`. `lldb --batch -o run -k bt` on the stage-2 binary gave the
  frame from one frame pointer.
- **A scratch-register tracker has to be forgotten where the result leaves
  elsewhere.** The x86 slice kernel loads the upper bound into `%rax`,
  overwrites it with the difference, and returns the box in `%rdx`, so its
  closing store re-asserted nothing and the tracker went on claiming the
  bound. A later read of that bound in the same block was then elided. A
  slice from offset 0 hides it, because there the difference equals the
  bound. The rule the sweep of both emitters settled on: a kernel that
  loads the scratch register and does not close by storing from it forgets
  before it returns.
- **The per-function sweep finds a miscompile in a few hundred builds.**
  `FERN_SSA_ONLY=<name>` over every function of a failing program, one
  build and run each, named prime_gaps' byte sieve in 170 builds.
