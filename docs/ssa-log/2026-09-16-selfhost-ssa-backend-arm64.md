# Measured 2026-09-16: the self-host `-backend ssa` on arm64, at landing

PR #9484, `docs/SELFHOST-SSA-BACKEND.md`. Every number here is from an arm64
Linux container (12 cores, native execution, no qemu) with the self-host CLI
built by the Go compiler at the branch head, unless it says otherwise. Best
of three where a time is given.

## Coverage

The compiler compiling itself (`fern.fern`, `-target arm64-linux`,
`FERN_SSA_REPORT=1`): **941 of 8,924 functions through the SSA backend**,
7,983 declined. The op that stopped each declined function:

| op | functions | op | functions |
|---|---|---|---|
| `struct_get` | 1,997 | `struct_make` | 153 |
| `const_str` | 1,962 | `const_func` | 119 |
| `variant_is` | 947 | `str_index` | 78 |
| `arr_len` | 923 | `const_f64` | 65 |
| `arr_make` | 891 | `str_eq` | 24 |
| `str_len` | 476 | `tuple_get` | 20 |
| `arr_get` | 238 | `arr_push` | 13 |

Fourteen more ops account for the remaining 27. On the five probe programs
in the gate the lift admits every function; on the mixed one (strings in
`main`, integer helpers) 54 of 120.

## Correctness

- The whole `examples/**` corpus outside `self_host`, 348 programs, each
  built flat and with `-backend ssa` and both run: **323 agree** on stdout
  and exit status. `cli/yes` is killed by the harness timeout on both paths
  and its output length differs; `tests/batch8_test` prints a temp path. The
  other 23 do not build on either path (pre-existing refusals).
- The first corpus run disagreed on two more, `ownership/borrowed_forward_lifetime`
  (exit 11 against 0) and `proposals/prime_gaps` (its bit sieve counted no
  primes). Both were one allocator defect: a loop-invariant value carried
  through a header phi has the phi itself as its back-edge operand, which
  `regalloc_linear` did not extend through the loop, so a body temporary took
  the register. Fixed in `ssa.fern`; the gate carries the shape.
- Stage 2 through the SSA backend (the compiler built by itself with
  `-backend ssa`) compiles `lexer.fern` and `checker.fern` **byte-identically**
  to the flat stage 2.
- The flat path emits byte-identically to origin/main on `lexer.fern`
  (arm64-linux here, arm64-darwin on the Mac), so the need-marking refactor
  and the flag threading are inert for the default emitter.

## Size

| binary | bytes |
|---|---|
| stage 2, flat | 18,105,344 |
| stage 2, `-backend ssa` | 18,105,344 |

The same length, not the same bytes: the ELF writer rounds both to it. On
the probe programs the SSA text is a few percent longer than the flat text
(`lexer.fern -emit asm`: 951,826 against 947,818 bytes), which is the
constant-through-x0 and phi-staging shapes the plan doc lists.

## Speed

| input | stage 2 flat | stage 2 ssa |
|---|---|---|
| `lexer.fern -emit asm` | 0.515 s | 0.496 s |
| `checker.fern -emit asm` | 13.44 s | 13.82 s |

Parity within noise. A tenth of the functions and an emitter that spills
every call-crossing value cannot show more; the callee-saved registers are
the first thing the plan doc's list would change here.

## Compile time

The self-build by the Go-built CLI, flat against `-backend ssa`, once each
on a loaded machine (two timing scripts overlapped, so read the ratio, not
the seconds):

| build | seconds |
|---|---|
| flat | 132 |
| `-backend ssa` | 229 |

1.7x. The lift runs over every function whether or not it is admitted, the
allocator's `order_by_lo` is an insertion sort, and the pruning walks each
function once more; none of it has been profiled. Native's flip condition
of 1.5x is the number to hold this to.

## Driver sizes (x86-64 ELF, the size gate's method, in the container)

| driver | here | recorded | delta |
|---|---|---|---|
| `fern.fern` | 13,280,264 | 12,881,996 | +3.1% |
| `asm_load_run.fern` | 9,227,416 | 8,747,276 | +5.5% |
| `asm_modload_run.fern` | 8,840,936 | 8,357,356 | +5.8% |
| `asm_ir_run.fern` | 8,601,576 | 8,122,140 | +5.9% |
| `asm_run.fern` | 7,958,936 | 7,684,164 | +3.6% |

Two unchanged drivers measure above their rows in the same container
(`irlower_run` +3.5%, `checker_modload_run` +1.3%), so roughly half of each
delta is drift since the last refresh and the rest is the arm64 backend
linking the lift, the allocator and the emitter. The rows are refreshed from
the `driver-sizes` job's own figures.

## Traps

- **An executable overwritten in place is killed on macOS.** The first Mac
  build of the CLI ran; every rebuild to the same path died at exec with 137
  and a crash report reading `SIGKILL (Code Signature Invalid)`, while
  `codesign -vv` passed and a `cp` of the file ran. The kernel caches the
  verdict by inode. Hours went into the Mach-O layout before the inode was
  tested; both compilers now unlink before writing an executable.
- **`time`, `bc`, `ps` and `free` are absent in the container image.** Time
  with `perl -MTime::HiRes=time -e 'print time'`.
- **The report tally prints after the runtime bodies.** They go through the
  same per-function emit and count; a tally printed before them undercounts.
