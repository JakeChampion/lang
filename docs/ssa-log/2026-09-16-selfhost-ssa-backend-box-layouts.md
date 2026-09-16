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

SELF_COMPILE_ROWS

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
