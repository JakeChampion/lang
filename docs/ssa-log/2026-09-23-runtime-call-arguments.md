# Runtime-call arguments move straight into place

The same dynamic census that found `__fern_arr_push`'s frame (see
`2026-09-23-append-without-a-frame.md`) ranks the register-to-register moves
in compiled code by what they sit next to. 24.3 M moves on a stage-2
x86-64 compile of `lexer.fern` staged a runtime call's arguments through the
scratch registers, 23.1 M of them before `__fern_arr_push_owned`:

```
movq %r12, %r11
movq %r14, %rcx
movq %rcx, %rsi
movq %r11, %rdi
call __fern_arr_push_owned
```

`ssa_rt_call` loaded argument 0 into `%r11` and argument 1 into `%rcx` so
that a home in `%rdi` or `%rsi` was read before either was written. It now
hands the two moves to `ssa_parallel_moves`, which already orders the moves
of a register call and parks a cycle in `%r11`, and `ssa_arg_prefs` asks for
`%rdi` and `%rsi` for those arguments as it asks for the register ABI's.
arm64's `ssa_rt_call` already loaded through `ssa_load_abi_args`; giving its
argument 0 a preference for `x0` moved `checker.fern` by one instruction, so
it is not taken.

`checker.fern` x86-64 static: 375,559 → 371,434 (−1.1%).

| bench | Ir change |
|---|---|
| `array_append` | −3.1% |
| `sort_ints` | −1.3% |
| `struct_drop` | −0.8% |
| `sort_strings` | −0.5% |
| `sort_inplace` | −0.4% |
| 9 others | 0% to −0.3% |
