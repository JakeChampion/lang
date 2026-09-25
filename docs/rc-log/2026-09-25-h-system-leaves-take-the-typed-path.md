# The environment, process, signal and socket leaves take the typed path

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering,
continued from `2026-09-25-g-fs-bundle-and-settled-runtime.md`.

## What changed

The helpers outside the filesystem bundle are retyped with the same rules:
`env`, `environ`, `getcwd`, `hostname`, `uname_field`, `getgroups`, the
`proc_*` leaves and `subprocess` (its `sp_drain` / `sp_exec_at` helpers take
`usize` buffers now), the TCP and UDP leaves, `poll` (both kernels' shapes),
`timer_fd`, the termios pair, `rlimit_nofile`, the signal leaves,
`read_all_stdin` and `read_line`. A word stored into a record slot
(`__raw_store_ptr` of a timeout or an fd) is widened `as usize`.

`rlimit_nofile` had no semsource contract, so a module calling it kept the
AST lowering; it has one now, with an ssarc arm onto `op_rlimit_nofile`.

119 of the 128 helper sources check on their own. The nine that do not:
`read_file` and `open_with` call a helper their bundle carries, so they check
there; `print_int`, `read_int` and `i32_lcm` wait on #10244; `arr_slice`
needs an array's box address; `map_find`, `map_delete` and `map_delete_rel`
call through a bare code address.

## Measured

- Row `sys-helpers-take-the-typed-path` (stdin fed): the environment, host,
  limit and `read_line` leaves; the module and helpers produce and the
  answer matches native's.
- Row `proc-leaves-take-the-typed-path`: `subprocess` of `/bin/echo`, a
  forked child's exit status through `proc_waitpid`, `signal_mask` and
  `timer_fd`. Native compiles `subprocess` only under `-interp`, so the
  answer is checked against the AST leg; its typed body adds a runtime need,
  so this row also exercises the runtime's second pass.
