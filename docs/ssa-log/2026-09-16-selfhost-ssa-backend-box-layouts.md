# Measured 2026-09-16: the box layouts and runtime calls lifted (#9498)

PR #9498, on top of #9494. The production lift admits the flat backends' box
layouts and the two ways the stack machine calls the runtime (`ssa.fern`
kinds 40 to 50); the earlier entries in this directory hold the numbers
before it. Same container and method as those entries: arm64 Linux, native
arm64 execution, x86-64 output under qemu-x86_64.

## Coverage on the probes

| program | before | after |
|---|---|---|
| mixed (strings in `main`, integer helpers) | 54 of 120 | 87 of 120 |
| `proposals/prime_gaps` | 58 of 162 | 122 of 162 |
| `ownership/borrowed_forward_lifetime` | 2 of 10 | 6 of 10 |

The five integer probes were already whole. All fourteen rows (seven
programs, two ISAs) agree on stdout and exit status with the flat emit.

## Coverage on the compiler compiling itself

Measured 2026-09-17 on the build of #9566, which carries #9503's
`call_indirect` and aggregate rows and #9512's compile-time fix, so this is
the whole of the box-layout work rather than #9498 alone. `fern.fern`,
arm64-linux, the same container:

| | flat | ssa |
|---|---|---|
| functions through the SSA backend | | 8,828 of 9,021 |
| compile time | 123 s | 134 s |
| binary | 18.3 MB | 29.1 MB |
| stage-2 output on `lexer.fern`, `checker.fern` | identical | identical |

The declined functions, by the op that stopped them: `const_f64` 75, `env`
21, `read_file` 20, `exit` 10, `syscall3` 9, `strbuf_reset` 5,
`strbuf_append` 5, `raw_scratch` 4, `f64_from_bits` 4, `f64_bits` 4,
`raw_string` 3, `i32_to_f64` 3, and eight ops at one or two each. The float
rows are #9567; the rest are host calls and the string buffer.

The same run on the build before #9566 found the three bugs that PR fixes:
the SSA-built compiler overflowed its stack on `lexer.fern` (a frame slot
per value id, 2.6 MB for one parser function), prime_gaps' byte sieve
exited 1 on x86-64 (the runtime call's argument registers are allocatable
there), and eight corpus programs importing `std/json` would not assemble
(a loop body ending in a return lost its block). The per-function
`FERN_SSA_ONLY` sweep over prime_gaps found the second in 170 builds; lldb on
the stage-2 binary found the first from one frame pointer.

The corpus on the same build, every `examples/**/*.fern` outside `self_host`
built both ways for arm64-linux and run: 348 programs, 322 agree on stdout
and exit status, 3 differ only by nondeterminism (a timestamped path, a
`yes` cut by the timeout, a benchmark's microsecond column), 23 fail to
build on both paths as they did before the backend existed. 43,336 of the
53,545 functions across the corpus went through the backend; 55 programs
whole.


## What each op became

A record field is a load at `(i+1)*8` behind the shape word; an array's
length a load at 0; a string's at 8; a tuple element a load at `i*8`; an
Option box's tag and payload loads at 0 and 8. `variant_is` is a load of
the shape word compared with the variant's interned shape address, by
pointer equality as both flat backends do it. A construction is
`__fern_arr_box(cap)` followed by stores; a string literal the address of
its static box; a function value the address of its `__fn_` label. An
element read or a string byte keeps the flat arm's bounds check and its
branch to `__fern_oob_abort`. `str_concat`, `str_eq`, `str_cmp`,
`arr_slice` and `str_from_bytes` are the stack-ABI calls of the Fern-compiled
helpers with the same argument order the flat arm pushes; `arr_push`, its
owned and wide forms, `alloc` and `alloc_u8` are the register-ABI routines.

## Traps

- **The probe harness's `sed` renamed the output files on one side of the
  `cmp` only.** Every row read DIFFER with matching exit codes, including
  programs that print nothing; the tally, not the verdict, was the signal
  something else was wrong.
- **A runtime Fern body counts in the tally.** A program whose user
  functions all go through the backend still shows one declined function
  when it pulls `__fern_str_concat`, whose body uses `raw_alloc`. The gate's
  `runtime_calls` program pins its user functions rather than the module.
- **`call_indirect` on the x86-64 stack machine pops the target into
  `%r11`**, which the SSA emitter allocates. It does not matter while
  `call_indirect` is declined; the arm that lifts it must load the target
  into a scratch register instead.
