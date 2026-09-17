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

## The corpus

Every `examples/**/*.fern` outside `self_host`, built both ways for
arm64-linux and run, on the build of #9569: 348 programs, 322 agree on
stdout and exit status, 3 differ only by nondeterminism (a timestamped
path, a `yes` cut by the timeout, a benchmark's microsecond column), 23
fail to build on both paths as they did before the backend existed. Before
#9566's loop-end fix, eight of those programs built under flat and not
under SSA.

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
- **The per-function sweep finds a miscompile in a few hundred builds.**
  `FERN_SSA_ONLY=<name>` over every function of a failing program, one
  build and run each, named prime_gaps' byte sieve in 170 builds.
